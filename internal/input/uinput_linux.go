//go:build linux

package input

import (
	"encoding/binary"
	"fmt"
	"os"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"github.com/maros-o/opencouchpad/internal/keymap"
)

// Linux input event constants (linux/input-event-codes.h, linux/uinput.h).
const (
	evSyn = 0x00
	evKey = 0x01
	evRel = 0x02

	synReport = 0

	relX           = 0x00
	relY           = 0x01
	relHWheel      = 0x06
	relWheel       = 0x08
	relWheelHiRes  = 0x0b
	relHWheelHiRes = 0x0c

	btnLeft   = 0x110
	btnRight  = 0x111
	btnMiddle = 0x112
	btnSide   = 0x113
	btnExtra  = 0x114

	keyLeftShift = 42
	keyRightAlt  = 100
	keyEnter     = 28
	keyTab       = 15
	keyBackspace = 14

	busVirtual = 0x06

	uiDevCreate  = 0x5501
	uiDevDestroy = 0x5502
	uiDevSetup   = 0x405c5503 // _IOW('U', 3, struct uinput_setup) (92 bytes)
	uiSetEvBit   = 0x40045564
	uiSetKeyBit  = 0x40045565
	uiSetRelBit  = 0x40045566
)

// struct input_event is {struct timeval; u16 type; u16 code; s32 value}.
// timeval is two native longs, so the size differs between 32 and 64-bit.
const longSize = int(unsafe.Sizeof(uintptr(0)))
const eventSize = 2*longSize + 8

type uinputDevice struct {
	f   *os.File
	buf []byte
}

func ioctl(fd uintptr, req uintptr, arg uintptr) error {
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, fd, req, arg); e != 0 {
		return e
	}
	return nil
}

func newDevice(name string, product uint16, evs []int, keys []int, rels []int) (*uinputDevice, error) {
	f, err := os.OpenFile("/dev/uinput", os.O_WRONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, fmt.Errorf("open /dev/uinput: %w (is the uinput module loaded and do you have permission? see README)", err)
	}
	fd := f.Fd()
	set := func(req uintptr, vals []int) error {
		for _, v := range vals {
			if err := ioctl(fd, req, uintptr(v)); err != nil {
				return err
			}
		}
		return nil
	}
	if err := set(uiSetEvBit, evs); err != nil {
		f.Close()
		return nil, fmt.Errorf("UI_SET_EVBIT: %w", err)
	}
	if err := set(uiSetKeyBit, keys); err != nil {
		f.Close()
		return nil, fmt.Errorf("UI_SET_KEYBIT: %w", err)
	}
	if err := set(uiSetRelBit, rels); err != nil {
		f.Close()
		return nil, fmt.Errorf("UI_SET_RELBIT: %w", err)
	}
	// struct uinput_setup { struct input_id {u16 bustype, vendor, product, version}; char name[80]; u32 ff_effects_max; }
	var setup [92]byte
	binary.LittleEndian.PutUint16(setup[0:], busVirtual)
	binary.LittleEndian.PutUint16(setup[2:], 0x4f52) // "OR"
	binary.LittleEndian.PutUint16(setup[4:], product)
	binary.LittleEndian.PutUint16(setup[6:], 1)
	copy(setup[8:87], name)
	if err := ioctl(fd, uiDevSetup, uintptr(unsafe.Pointer(&setup[0]))); err != nil {
		f.Close()
		return nil, fmt.Errorf("UI_DEV_SETUP: %w", err)
	}
	if err := ioctl(fd, uiDevCreate, 0); err != nil {
		f.Close()
		return nil, fmt.Errorf("UI_DEV_CREATE: %w", err)
	}
	return &uinputDevice{f: f, buf: make([]byte, 0, eventSize*16)}, nil
}

// add appends an event to the pending batch.
func (d *uinputDevice) add(typ, code uint16, value int32) {
	var ev [eventSize]byte
	off := 2 * longSize // timestamps are filled in by the kernel
	binary.NativeEndian.PutUint16(ev[off:], typ)
	binary.NativeEndian.PutUint16(ev[off+2:], code)
	binary.NativeEndian.PutUint32(ev[off+4:], uint32(value))
	d.buf = append(d.buf, ev[:]...)
}

// flush appends SYN_REPORT and writes the whole batch in one syscall.
func (d *uinputDevice) flush() error {
	d.add(evSyn, synReport, 0)
	_, err := d.f.Write(d.buf)
	d.buf = d.buf[:0]
	return err
}

func (d *uinputDevice) close() {
	ioctl(d.f.Fd(), uiDevDestroy, 0)
	d.f.Close()
}

type uinputDriver struct {
	mu        sync.Mutex
	mouse     *uinputDevice
	kbd       *uinputDevice
	wheelAccY int32
	wheelAccX int32
	held      map[string]keymap.Stroke // char keys currently held via Key(down)
}

// NewNative creates the uinput-backed driver.
func NewNative() (Driver, error) {
	mouse, err := newDevice("OpenCouchpad Mouse", 1,
		[]int{evKey, evRel},
		[]int{btnLeft, btnRight, btnMiddle, btnSide, btnExtra},
		[]int{relX, relY, relWheel, relHWheel, relWheelHiRes, relHWheelHiRes})
	if err != nil {
		return nil, err
	}
	// Advertise only keys we can send: the main block (minus KEY_POWER) plus
	// named media/browser keys. Not advertising power/rfkill/sleep keys keeps
	// the virtual keyboard from ever being able to switch off Wi-Fi or the PC.
	var keys []int
	for k := 1; k <= 127; k++ {
		if k != 116 { // KEY_POWER
			keys = append(keys, k)
		}
	}
	for _, def := range namedKeys {
		if def.linux > 127 {
			keys = append(keys, int(def.linux))
		}
	}
	kbd, err := newDevice("OpenCouchpad Keyboard", 2, []int{evKey}, keys, nil)
	if err != nil {
		mouse.close()
		return nil, err
	}
	// Give udev/libinput a moment to pick the new devices up, otherwise the
	// very first events can be dropped.
	time.Sleep(150 * time.Millisecond)
	return &uinputDriver{mouse: mouse, kbd: kbd, held: map[string]keymap.Stroke{}}, nil
}

func (d *uinputDriver) Name() string { return "uinput" }

func (d *uinputDriver) Move(dx, dy int32) error {
	if dx == 0 && dy == 0 {
		return nil
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if dx != 0 {
		d.mouse.add(evRel, relX, dx)
	}
	if dy != 0 {
		d.mouse.add(evRel, relY, dy)
	}
	return d.mouse.flush()
}

func (d *uinputDriver) Scroll(dx, dy int32) error {
	if dx == 0 && dy == 0 {
		return nil
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	// Hi-res axis for smooth scrolling + legacy notch axis every 120 units
	// for clients that only understand REL_WHEEL. Linux wheel: positive = up.
	if dy != 0 {
		d.mouse.add(evRel, relWheelHiRes, -dy)
		d.wheelAccY += -dy
		if n := d.wheelAccY / 120; n != 0 {
			d.mouse.add(evRel, relWheel, n)
			d.wheelAccY -= n * 120
		}
	}
	if dx != 0 {
		d.mouse.add(evRel, relHWheelHiRes, dx)
		d.wheelAccX += dx
		if n := d.wheelAccX / 120; n != 0 {
			d.mouse.add(evRel, relHWheel, n)
			d.wheelAccX -= n * 120
		}
	}
	return d.mouse.flush()
}

func (d *uinputDriver) Button(b Button, down bool) error {
	code := map[Button]uint16{ButtonLeft: btnLeft, ButtonRight: btnRight, ButtonMiddle: btnMiddle,
		ButtonBack: btnSide, ButtonForward: btnExtra}[b]
	if code == 0 {
		return fmt.Errorf("unknown button %d", b)
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.mouse.add(evKey, code, b2i(down))
	return d.mouse.flush()
}

func b2i(b bool) int32 {
	if b {
		return 1
	}
	return 0
}

func (d *uinputDriver) Key(name string, down bool, layout string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if def, ok := namedKeys[name]; ok && def.linux != 0 {
		d.kbd.add(evKey, def.linux, b2i(down))
		return d.kbd.flush()
	}
	r, ok := IsChar(name)
	if !ok {
		return ErrUnknownKey
	}
	if !down {
		s, ok := d.held[name]
		if !ok {
			return nil
		}
		delete(d.held, name)
		d.keyMods(s.Mods, false)
		d.kbd.add(evKey, s.Key, 0)
		return d.kbd.flush()
	}
	s, ok := keymap.Get(layout).Lookup(r)
	if !ok || s.DeadKey != 0 {
		return ErrUnknownKey
	}
	d.held[name] = s
	d.keyMods(s.Mods, true)
	d.kbd.add(evKey, s.Key, 1)
	return d.kbd.flush()
}

func (d *uinputDriver) keyMods(mods uint8, down bool) {
	if mods&keymap.ModShift != 0 {
		d.kbd.add(evKey, keyLeftShift, b2i(down))
	}
	if mods&keymap.ModAltGr != 0 {
		d.kbd.add(evKey, keyRightAlt, b2i(down))
	}
}

// tap presses and releases a key with modifiers. Each step is its own SYN
// frame so that every client (X11, Wayland, XWayland) sees a clean sequence.
func (d *uinputDriver) tap(code uint16, mods uint8) error {
	if mods != 0 {
		d.keyMods(mods, true)
		if err := d.kbd.flush(); err != nil {
			return err
		}
	}
	d.kbd.add(evKey, code, 1)
	if err := d.kbd.flush(); err != nil {
		return err
	}
	d.kbd.add(evKey, code, 0)
	if err := d.kbd.flush(); err != nil {
		return err
	}
	if mods != 0 {
		d.keyMods(mods, false)
		return d.kbd.flush()
	}
	return nil
}

func (d *uinputDriver) Type(text string, layout string) error {
	l := keymap.Get(layout)
	var missing []rune
	for _, r := range text {
		d.mu.Lock()
		var err error
		switch r {
		case '\n', '\r':
			err = d.tap(keyEnter, 0)
		case '\t':
			err = d.tap(keyTab, 0)
		case '\b':
			err = d.tap(keyBackspace, 0)
		default:
			s, ok := l.Lookup(r)
			if !ok {
				missing = append(missing, r)
				break
			}
			if s.DeadKey != 0 {
				if err = d.tap(s.DeadKey, s.DeadMods); err != nil {
					break
				}
			}
			err = d.tap(s.Key, s.Mods)
		}
		d.mu.Unlock()
		if err != nil {
			return err
		}
		// Long pastes: yield briefly so slow consumers (XWayland, terminals)
		// never drop keys, while short typing stays instant.
		if len(text) > 16 {
			time.Sleep(time.Millisecond)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("layout %s cannot type %q", l.ID, string(missing))
	}
	return nil
}

func (d *uinputDriver) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.mouse.close()
	d.kbd.close()
	return nil
}
