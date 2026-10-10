//go:build windows

package input

import (
	"fmt"
	"sync"
	"syscall"
	"unicode/utf16"
	"unsafe"
)

var (
	user32            = syscall.NewLazyDLL("user32.dll")
	procSendInput     = user32.NewProc("SendInput")
	procVkKeyScanW    = user32.NewProc("VkKeyScanW")
	procMapVirtualKey = user32.NewProc("MapVirtualKeyW")
)

const (
	inputMouse    = 0
	inputKeyboard = 1

	mouseeventfMove       = 0x0001
	mouseeventfLeftDown   = 0x0002
	mouseeventfLeftUp     = 0x0004
	mouseeventfRightDown  = 0x0008
	mouseeventfRightUp    = 0x0010
	mouseeventfMiddleDown = 0x0020
	mouseeventfMiddleUp   = 0x0040
	mouseeventfXDown      = 0x0080
	mouseeventfXUp        = 0x0100
	mouseeventfWheel      = 0x0800
	mouseeventfHWheel     = 0x1000

	keyeventfExtended = 0x0001
	keyeventfKeyUp    = 0x0002
	keyeventfUnicode  = 0x0004

	vkShift   = 0x10
	vkControl = 0x11
	vkMenu    = 0x12
	vkReturn  = 0x0D
	vkTab     = 0x09
	vkBack    = 0x08
)

type mouseInput struct {
	dx, dy    int32
	mouseData uint32
	flags     uint32
	time      uint32
	extraInfo uintptr
}

// mouseINPUT has exactly the size/alignment of the Win32 INPUT struct
// (40 bytes on 64-bit, 28 on 32-bit) because MOUSEINPUT is the largest member.
type mouseINPUT struct {
	typ uint32
	mi  mouseInput
}

type keybdInput struct {
	vk        uint16
	scan      uint16
	flags     uint32
	time      uint32
	extraInfo uintptr
}

// keybdINPUT is padded to the same size as mouseINPUT.
type keybdINPUT struct {
	typ uint32
	ki  keybdInput
	_   [8]byte
}

const inputSize = unsafe.Sizeof(mouseINPUT{})

func init() {
	if unsafe.Sizeof(keybdINPUT{}) != inputSize {
		panic("INPUT struct size mismatch")
	}
}

type sendInputDriver struct {
	mu   sync.Mutex
	held map[string][]keybdINPUT // release events for char keys held via Key(down)
}

// NewNative creates the SendInput-backed driver.
func NewNative() (Driver, error) {
	if err := procSendInput.Find(); err != nil {
		return nil, err
	}
	return &sendInputDriver{held: map[string][]keybdINPUT{}}, nil
}

func (d *sendInputDriver) Name() string { return "sendinput" }

func sendMouse(ins ...mouseINPUT) error {
	n, _, err := procSendInput.Call(uintptr(len(ins)), uintptr(unsafe.Pointer(&ins[0])), inputSize)
	if int(n) != len(ins) {
		return fmt.Errorf("SendInput: %v (blocked by UIPI? run as administrator to control elevated windows)", err)
	}
	return nil
}

func sendKeys(ins []keybdINPUT) error {
	if len(ins) == 0 {
		return nil
	}
	n, _, err := procSendInput.Call(uintptr(len(ins)), uintptr(unsafe.Pointer(&ins[0])), inputSize)
	if int(n) != len(ins) {
		return fmt.Errorf("SendInput: %v (blocked by UIPI? run as administrator to control elevated windows)", err)
	}
	return nil
}

func mouse(flags uint32, data int32, dx, dy int32) mouseINPUT {
	return mouseINPUT{typ: inputMouse, mi: mouseInput{dx: dx, dy: dy, mouseData: uint32(data), flags: flags}}
}

func (d *sendInputDriver) Move(dx, dy int32) error {
	if dx == 0 && dy == 0 {
		return nil
	}
	return sendMouse(mouse(mouseeventfMove, 0, dx, dy))
}

func (d *sendInputDriver) Scroll(dx, dy int32) error {
	switch {
	case dx != 0 && dy != 0:
		return sendMouse(mouse(mouseeventfWheel, -dy, 0, 0), mouse(mouseeventfHWheel, dx, 0, 0))
	case dy != 0:
		return sendMouse(mouse(mouseeventfWheel, -dy, 0, 0)) // Windows: positive = forward/up
	case dx != 0:
		return sendMouse(mouse(mouseeventfHWheel, dx, 0, 0))
	}
	return nil
}

func (d *sendInputDriver) Button(b Button, down bool) error {
	var flags uint32
	var data int32
	switch b {
	case ButtonLeft:
		flags = pick(down, mouseeventfLeftDown, mouseeventfLeftUp)
	case ButtonRight:
		flags = pick(down, mouseeventfRightDown, mouseeventfRightUp)
	case ButtonMiddle:
		flags = pick(down, mouseeventfMiddleDown, mouseeventfMiddleUp)
	case ButtonBack:
		flags, data = pick(down, mouseeventfXDown, mouseeventfXUp), 1
	case ButtonForward:
		flags, data = pick(down, mouseeventfXDown, mouseeventfXUp), 2
	default:
		return fmt.Errorf("unknown button %d", b)
	}
	return sendMouse(mouse(flags, data, 0, 0))
}

func pick(c bool, a, b uint32) uint32 {
	if c {
		return a
	}
	return b
}

func vkEvent(vk uint16, ext, up bool) keybdINPUT {
	scan, _, _ := procMapVirtualKey.Call(uintptr(vk), 0) // MAPVK_VK_TO_VSC
	var flags uint32
	if ext {
		flags |= keyeventfExtended
	}
	if up {
		flags |= keyeventfKeyUp
	}
	return keybdINPUT{typ: inputKeyboard, ki: keybdInput{vk: vk, scan: uint16(scan), flags: flags}}
}

// charKey resolves a character to a virtual key plus the modifier VKs needed
// in the current keyboard layout of this thread.
func charKey(r rune) (vk uint16, mods []uint16, ok bool) {
	if r > 0xFFFF {
		return 0, nil, false
	}
	res, _, _ := procVkKeyScanW.Call(uintptr(r))
	if int16(res) == -1 {
		return 0, nil, false
	}
	vk = uint16(res & 0xFF)
	state := (res >> 8) & 0xFF
	if state&1 != 0 {
		mods = append(mods, vkShift)
	}
	if state&2 != 0 {
		mods = append(mods, vkControl)
	}
	if state&4 != 0 {
		mods = append(mods, vkMenu)
	}
	return vk, mods, true
}

func (d *sendInputDriver) Key(name string, down bool, _ string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if def, ok := namedKeys[name]; ok && def.win != 0 {
		return sendKeys([]keybdINPUT{vkEvent(def.win, def.ext, !down)})
	}
	r, ok := IsChar(name)
	if !ok {
		return ErrUnknownKey
	}
	if !down {
		rel, ok := d.held[name]
		if !ok {
			return nil
		}
		delete(d.held, name)
		return sendKeys(rel)
	}
	vk, mods, ok := charKey(r)
	if !ok {
		return ErrUnknownKey
	}
	var press, release []keybdINPUT
	for _, m := range mods {
		press = append(press, vkEvent(m, false, false))
	}
	press = append(press, vkEvent(vk, false, false))
	release = append(release, vkEvent(vk, false, true))
	for i := len(mods) - 1; i >= 0; i-- {
		release = append(release, vkEvent(mods[i], false, true))
	}
	d.held[name] = release
	return sendKeys(press)
}

func (d *sendInputDriver) Type(text string, _ string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	var batch []keybdINPUT
	flush := func() error {
		err := sendKeys(batch)
		batch = batch[:0]
		return err
	}
	for _, r := range text {
		switch r {
		case '\n', '\r':
			batch = append(batch, vkEvent(vkReturn, false, false), vkEvent(vkReturn, false, true))
		case '\t':
			batch = append(batch, vkEvent(vkTab, false, false), vkEvent(vkTab, false, true))
		case '\b':
			batch = append(batch, vkEvent(vkBack, false, false), vkEvent(vkBack, false, true))
		default:
			// KEYEVENTF_UNICODE types any character regardless of the layout.
			for _, u := range utf16.Encode([]rune{r}) {
				batch = append(batch,
					keybdINPUT{typ: inputKeyboard, ki: keybdInput{scan: u, flags: keyeventfUnicode}},
					keybdINPUT{typ: inputKeyboard, ki: keybdInput{scan: u, flags: keyeventfUnicode | keyeventfKeyUp}})
			}
		}
		if len(batch) >= 128 {
			if err := flush(); err != nil {
				return err
			}
		}
	}
	return flush()
}

func (d *sendInputDriver) Close() error { return nil }
