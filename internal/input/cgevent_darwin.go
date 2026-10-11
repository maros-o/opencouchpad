//go:build darwin

package input

import (
	"errors"
	"fmt"
	"log/slog"
	"runtime"
	"sync"
	"time"
	"unicode/utf16"
	"unsafe"

	"github.com/ebitengine/purego"
)

// The driver talks to CoreGraphics through purego, so the binary stays
// cgo-free and cross-compiles from Linux like the other targets.

func init() {
	// The Text Input Sources API (used to read the keyboard layout) must run
	// on the main thread on recent macOS. Locking here keeps main() on it, and
	// NewNative is called from main().
	runtime.LockOSThread()
}

type cgPoint struct{ X, Y float64 }

type cgRect struct{ Origin, Size cgPoint }

func (r cgRect) contains(p cgPoint) bool {
	return p.X >= r.Origin.X && p.X < r.Origin.X+r.Size.X && p.Y >= r.Origin.Y && p.Y < r.Origin.Y+r.Size.Y
}

var (
	cgEventCreate                   func(src uintptr) uintptr
	cgEventGetLocation              func(ev uintptr) cgPoint
	cgEventCreateMouseEvent         func(src uintptr, typ uint32, pos cgPoint, button uint32) uintptr
	cgEventCreateKeyboardEvent      func(src uintptr, key uint16, down bool) uintptr
	cgEventCreateScrollWheelEvent2  func(src uintptr, units uint32, count uint32, w1, w2, w3 int32) uintptr
	cgEventKeyboardSetUnicodeString func(ev uintptr, n uint64, s *uint16)
	cgEventSetFlags                 func(ev uintptr, flags uint64)
	cgEventSetType                  func(ev uintptr, typ uint32)
	cgEventSetIntegerValueField     func(ev uintptr, field uint32, v int64)
	cgEventPost                     func(tap uint32, ev uintptr)
	cgEventSourceCreate             func(state int32) uintptr
	cgGetActiveDisplayList          func(max uint32, ids *uint32, count *uint32) int32
	cgDisplayBounds                 func(id uint32) cgRect
	cfRelease                       func(ref uintptr)
	cfDictionaryCreate              func(alloc uintptr, keys, vals *uintptr, n int64, kcb, vcb uintptr) uintptr
	cfDataGetBytePtr                func(data uintptr) uintptr
	axIsProcessTrusted              func() bool
	axIsProcessTrustedWithOptions   func(opts uintptr) bool
	tisCopyCurrentKeyboardLayoutSrc func() uintptr
	tisGetInputSourceProperty       func(src, key uintptr) uintptr
	lmGetKbdType                    func() uint8
	ucKeyTranslate                  func(layout uintptr, code, action uint16, mods, kbdType, opts uint32, dead *uint32, max uint64, n *uint64, buf *uint16) int32
	objcGetClass                    func(name string) uintptr
	selRegisterName                 func(name string) uintptr
	objcMsgSend                     func(id, sel uintptr) uintptr
	objcMsgSendDouble               func(id, sel uintptr) float64
	objcMsgSendOtherEvent           func(cls, sel uintptr, typ uint64, loc cgPoint, flags uint64, ts float64, win int64, ctx uintptr, subtype int16, d1, d2 int64) uintptr
	objcAutoreleasePoolPush         func() uintptr
	objcAutoreleasePoolPop          func(pool uintptr)
)

const (
	evLeftMouseDown    = 1
	evLeftMouseUp      = 2
	evRightMouseDown   = 3
	evRightMouseUp     = 4
	evMouseMoved       = 5
	evLeftMouseDragged = 6
	evRightDragged     = 7
	evKeyDown          = 10
	evKeyUp            = 11
	evFlagsChanged     = 12
	evOtherMouseDown   = 25
	evOtherMouseUp     = 26
	evOtherDragged     = 27

	fieldClickState     = 1
	fieldButtonNumber   = 3
	fieldDeltaX         = 4
	fieldDeltaY         = 5
	fieldScrollContinue = 88

	flagShift     = 0x20000
	flagControl   = 0x40000
	flagOption    = 0x80000
	flagCommand   = 0x100000
	flagNumPad    = 0x200000
	flagSecondary = 0x800000 // fn

	hidEventTap      = 0
	scrollUnitPixel  = 0
	sourcePrivate    = -1
	scrollPxPerNotch = 40

	kvkReturn    = 0x24
	kvkTab       = 0x30
	kvkBackspace = 0x33
)

// macKey is how a named key is produced on macOS: a virtual key code, a
// system-defined media key (nx), or a key code with fixed modifiers.
type macKey struct {
	code  uint16
	flags uint64 // extra flags a real keyboard sets (fn, numpad) or a fixed shortcut
	mod   uint64 // nonzero for modifier keys: the flag it toggles
	nx    int    // NX_KEYTYPE_* + 1 for media keys
}

var macKeys = map[string]macKey{
	"Escape":      {code: 0x35},
	"Backspace":   {code: kvkBackspace},
	"Tab":         {code: kvkTab},
	"Enter":       {code: kvkReturn},
	" ":           {code: 0x31},
	"Space":       {code: 0x31},
	"Spacebar":    {code: 0x31},
	"Control":     {code: 0x3B, mod: flagControl},
	"Shift":       {code: 0x38, mod: flagShift},
	"Alt":         {code: 0x3A, mod: flagOption},
	"AltGraph":    {code: 0x3D, mod: flagOption},
	"Meta":        {code: 0x37, mod: flagCommand},
	"OS":          {code: 0x37, mod: flagCommand},
	"Super":       {code: 0x37, mod: flagCommand},
	"CapsLock":    {code: 0x39},
	"ContextMenu": {code: 0x6E},
	"Insert":      {code: 0x72, flags: flagSecondary}, // Help, where Mac keyboards have Insert
	"Delete":      {code: 0x75, flags: flagSecondary},
	"Home":        {code: 0x73, flags: flagSecondary},
	"End":         {code: 0x77, flags: flagSecondary},
	"PageUp":      {code: 0x74, flags: flagSecondary},
	"PageDown":    {code: 0x79, flags: flagSecondary},
	"ArrowUp":     {code: 0x7E, flags: flagSecondary | flagNumPad},
	"ArrowDown":   {code: 0x7D, flags: flagSecondary | flagNumPad},
	"ArrowLeft":   {code: 0x7B, flags: flagSecondary | flagNumPad},
	"ArrowRight":  {code: 0x7C, flags: flagSecondary | flagNumPad},

	"F1": {code: 0x7A, flags: flagSecondary}, "F2": {code: 0x78, flags: flagSecondary},
	"F3": {code: 0x63, flags: flagSecondary}, "F4": {code: 0x76, flags: flagSecondary},
	"F5": {code: 0x60, flags: flagSecondary}, "F6": {code: 0x61, flags: flagSecondary},
	"F7": {code: 0x62, flags: flagSecondary}, "F8": {code: 0x64, flags: flagSecondary},
	"F9": {code: 0x65, flags: flagSecondary}, "F10": {code: 0x6D, flags: flagSecondary},
	"F11": {code: 0x67, flags: flagSecondary}, "F12": {code: 0x6F, flags: flagSecondary},
	"F13": {code: 0x69, flags: flagSecondary}, "F14": {code: 0x6B, flags: flagSecondary},
	"F15": {code: 0x71, flags: flagSecondary}, "F16": {code: 0x6A, flags: flagSecondary},
	"F17": {code: 0x40, flags: flagSecondary}, "F18": {code: 0x4F, flags: flagSecondary},
	"F19": {code: 0x50, flags: flagSecondary}, "F20": {code: 0x5A, flags: flagSecondary},

	"AudioVolumeUp":      {nx: 0 + 1},
	"AudioVolumeDown":    {nx: 1 + 1},
	"BrightnessUp":       {nx: 2 + 1},
	"BrightnessDown":     {nx: 3 + 1},
	"AudioVolumeMute":    {nx: 7 + 1},
	"MediaPlayPause":     {nx: 16 + 1},
	"MediaTrackNext":     {nx: 17 + 1},
	"MediaTrackPrevious": {nx: 18 + 1},

	// No dedicated keys on a Mac: send the usual shortcuts instead.
	"BrowserBack":    {code: 0x21, flags: flagCommand}, // Cmd+[
	"BrowserForward": {code: 0x1E, flags: flagCommand}, // Cmd+]
	"BrowserRefresh": {code: 0x0F, flags: flagCommand}, // Cmd+R
}

// usKeys is the ANSI US key code for printable ASCII, used when the active
// layout can't be read.
var usKeys = map[rune]macStroke{}

func init() {
	plain := "asdfhgzxcv\x00bqweryt123465=97-80]ou[ip\x00lj'k;\\,/nm.\x00\x00`"
	shifted := "ASDFHGZXCV\x00BQWERYT!@#$^%+(&_*)}OU{IP\x00LJ\"K:|<?NM>\x00\x00~"
	for i := range len(plain) {
		if plain[i] != 0 {
			usKeys[rune(plain[i])] = macStroke{uint16(i), 0}
			usKeys[rune(shifted[i])] = macStroke{uint16(i), flagShift}
		}
	}
}

type macStroke struct {
	code  uint16
	flags uint64
}

func loadFramework(path string) (uintptr, error) {
	return purego.Dlopen(path, purego.RTLD_NOW|purego.RTLD_GLOBAL)
}

// symbolValue reads a global pointer variable such as a CFStringRef constant.
func symbolValue(lib uintptr, name string) uintptr {
	addr, err := purego.Dlsym(lib, name)
	if err != nil || addr == 0 {
		return 0
	}
	p := *(**uintptr)(unsafe.Pointer(&addr))
	return *p
}

func loadLibs() error {
	cg, err := loadFramework("/System/Library/Frameworks/CoreGraphics.framework/CoreGraphics")
	if err != nil {
		return err
	}
	cf, err := loadFramework("/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation")
	if err != nil {
		return err
	}
	as, err := loadFramework("/System/Library/Frameworks/ApplicationServices.framework/ApplicationServices")
	if err != nil {
		return err
	}
	purego.RegisterLibFunc(&cgEventCreate, cg, "CGEventCreate")
	purego.RegisterLibFunc(&cgEventGetLocation, cg, "CGEventGetLocation")
	purego.RegisterLibFunc(&cgEventCreateMouseEvent, cg, "CGEventCreateMouseEvent")
	purego.RegisterLibFunc(&cgEventCreateKeyboardEvent, cg, "CGEventCreateKeyboardEvent")
	purego.RegisterLibFunc(&cgEventCreateScrollWheelEvent2, cg, "CGEventCreateScrollWheelEvent2")
	purego.RegisterLibFunc(&cgEventKeyboardSetUnicodeString, cg, "CGEventKeyboardSetUnicodeString")
	purego.RegisterLibFunc(&cgEventSetFlags, cg, "CGEventSetFlags")
	purego.RegisterLibFunc(&cgEventSetType, cg, "CGEventSetType")
	purego.RegisterLibFunc(&cgEventSetIntegerValueField, cg, "CGEventSetIntegerValueField")
	purego.RegisterLibFunc(&cgEventPost, cg, "CGEventPost")
	purego.RegisterLibFunc(&cgEventSourceCreate, cg, "CGEventSourceCreate")
	purego.RegisterLibFunc(&cgGetActiveDisplayList, cg, "CGGetActiveDisplayList")
	purego.RegisterLibFunc(&cgDisplayBounds, cg, "CGDisplayBounds")
	purego.RegisterLibFunc(&cfRelease, cf, "CFRelease")
	purego.RegisterLibFunc(&cfDictionaryCreate, cf, "CFDictionaryCreate")
	purego.RegisterLibFunc(&cfDataGetBytePtr, cf, "CFDataGetBytePtr")
	purego.RegisterLibFunc(&axIsProcessTrusted, as, "AXIsProcessTrusted")
	purego.RegisterLibFunc(&axIsProcessTrustedWithOptions, as, "AXIsProcessTrustedWithOptions")
	return nil
}

// promptAccessibility asks macOS to show the Accessibility permission dialog
// and reports whether the permission is already granted.
func promptAccessibility() bool {
	as, _ := loadFramework("/System/Library/Frameworks/ApplicationServices.framework/ApplicationServices")
	cf, _ := loadFramework("/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation")
	key := symbolValue(as, "kAXTrustedCheckOptionPrompt")
	val := symbolValue(cf, "kCFBooleanTrue")
	kcb, _ := purego.Dlsym(cf, "kCFTypeDictionaryKeyCallBacks")
	vcb, _ := purego.Dlsym(cf, "kCFTypeDictionaryValueCallBacks")
	if key == 0 || val == 0 || kcb == 0 || vcb == 0 {
		return axIsProcessTrusted()
	}
	dict := cfDictionaryCreate(0, &key, &val, 1, kcb, vcb)
	if dict == 0 {
		return axIsProcessTrusted()
	}
	defer cfRelease(dict)
	return axIsProcessTrustedWithOptions(dict)
}

// layoutStrokes maps characters to key codes in the current keyboard layout,
// so that shortcuts like Cmd+Z hit the right key on QWERTZ or AZERTY.
func layoutStrokes() map[rune]macStroke {
	carbon, err := loadFramework("/System/Library/Frameworks/Carbon.framework/Carbon")
	if err != nil {
		return nil
	}
	purego.RegisterLibFunc(&tisCopyCurrentKeyboardLayoutSrc, carbon, "TISCopyCurrentKeyboardLayoutInputSource")
	purego.RegisterLibFunc(&tisGetInputSourceProperty, carbon, "TISGetInputSourceProperty")
	purego.RegisterLibFunc(&lmGetKbdType, carbon, "LMGetKbdType")
	purego.RegisterLibFunc(&ucKeyTranslate, carbon, "UCKeyTranslate")
	prop := symbolValue(carbon, "kTISPropertyUnicodeKeyLayoutData")
	src := tisCopyCurrentKeyboardLayoutSrc()
	if src == 0 || prop == 0 {
		return nil
	}
	defer cfRelease(src)
	data := tisGetInputSourceProperty(src, prop)
	if data == 0 {
		return nil
	}
	layout := cfDataGetBytePtr(data)
	kbd := uint32(lmGetKbdType())
	out := map[rune]macStroke{}
	// Modifier states are Carbon modifiers >> 8: shift = 2, option = 8.
	for _, m := range []struct {
		state uint32
		flags uint64
	}{{0, 0}, {2, flagShift}, {8, flagOption}, {10, flagShift | flagOption}} {
		for code := uint16(0); code < 128; code++ {
			var dead uint32
			var n uint64
			var buf [4]uint16
			// kUCKeyActionDown, kUCKeyTranslateNoDeadKeysBit
			if ucKeyTranslate(layout, code, 0, m.state, kbd, 1, &dead, 4, &n, &buf[0]) != 0 || n != 1 {
				continue
			}
			r := rune(buf[0])
			if r < 0x20 || r == 0x7F {
				continue
			}
			if _, ok := out[r]; !ok {
				out[r] = macStroke{code, m.flags}
			}
		}
	}
	if len(out) < 40 {
		return nil
	}
	return out
}

type cgDriver struct {
	mu      sync.Mutex
	src     uintptr
	mods    uint64               // modifier flags currently held by phones
	buttons map[Button]bool      // held mouse buttons, for drag events
	held    map[string]macStroke // char keys held via Key(down)
	strokes map[rune]macStroke

	// double-click detection: macOS only sees a double click if the event says so
	clickBtn   Button
	clickAt    time.Time
	clickPos   cgPoint
	clickCount int64
	dblClick   time.Duration

	displays   []cgRect
	displaysAt time.Time

	scrollX, scrollY int32 // sub-pixel scroll remainder

	trusted   bool
	checkedAt time.Time

	nsEvent, otherEvent, cgEventSel uintptr
}

// NewNative creates the CoreGraphics-backed driver.
func NewNative() (Driver, error) {
	if err := loadLibs(); err != nil {
		return nil, fmt.Errorf("load CoreGraphics: %w", err)
	}
	d := &cgDriver{
		src:      cgEventSourceCreate(sourcePrivate),
		buttons:  map[Button]bool{},
		held:     map[string]macStroke{},
		dblClick: 500 * time.Millisecond,
	}
	d.strokes = layoutStrokes()
	if d.strokes == nil {
		slog.Warn("could not read the macOS keyboard layout, shortcuts assume US key positions")
		d.strokes = usKeys
	}
	d.loadAppKit()
	d.trusted = promptAccessibility()
	d.checkedAt = time.Now()
	if !d.trusted {
		slog.Warn("macOS has not given OpenCouchpad Accessibility access yet, so input is ignored. " +
			"Allow it in System Settings > Privacy & Security > Accessibility (for Terminal if you started it there), then restart OpenCouchpad.")
	}
	return d, nil
}

// loadAppKit enables media keys, which macOS only accepts as NSEvents.
func (d *cgDriver) loadAppKit() {
	if _, err := loadFramework("/System/Library/Frameworks/AppKit.framework/AppKit"); err != nil {
		return
	}
	objc, err := loadFramework("/usr/lib/libobjc.A.dylib")
	if err != nil {
		return
	}
	msgSend, err := purego.Dlsym(objc, "objc_msgSend")
	if err != nil {
		return
	}
	purego.RegisterLibFunc(&objcGetClass, objc, "objc_getClass")
	purego.RegisterLibFunc(&selRegisterName, objc, "sel_registerName")
	purego.RegisterLibFunc(&objcAutoreleasePoolPush, objc, "objc_autoreleasePoolPush")
	purego.RegisterLibFunc(&objcAutoreleasePoolPop, objc, "objc_autoreleasePoolPop")
	purego.RegisterFunc(&objcMsgSend, msgSend)
	purego.RegisterFunc(&objcMsgSendDouble, msgSend)
	purego.RegisterFunc(&objcMsgSendOtherEvent, msgSend)
	d.nsEvent = objcGetClass("NSEvent")
	if d.nsEvent == 0 {
		return
	}
	d.otherEvent = selRegisterName("otherEventWithType:location:modifierFlags:timestamp:windowNumber:context:subtype:data1:data2:")
	d.cgEventSel = selRegisterName("CGEvent")
	if s := objcMsgSendDouble(d.nsEvent, selRegisterName("doubleClickInterval")); s > 0.1 && s < 5 {
		d.dblClick = time.Duration(s * float64(time.Second))
	}
}

func (d *cgDriver) Name() string { return "cgevent" }

var errNotTrusted = errors.New("macOS blocks input: allow OpenCouchpad in System Settings > Privacy & Security > Accessibility, then restart it")

// allowed reports whether macOS will accept our events. Without Accessibility
// access CGEventPost silently drops them, so tell the phone instead.
func (d *cgDriver) allowed() error {
	if !d.trusted && time.Since(d.checkedAt) > 3*time.Second {
		d.trusted = axIsProcessTrusted()
		d.checkedAt = time.Now()
	}
	if !d.trusted {
		return errNotTrusted
	}
	return nil
}

func (d *cgDriver) post(ev uintptr) error {
	if ev == 0 {
		return errors.New("CGEvent create failed")
	}
	cgEventPost(hidEventTap, ev)
	cfRelease(ev)
	return nil
}

func (d *cgDriver) cursor() cgPoint {
	ev := cgEventCreate(0)
	if ev == 0 {
		return cgPoint{}
	}
	defer cfRelease(ev)
	return cgEventGetLocation(ev)
}

// clamp keeps the pointer on a screen: posted events are not limited by the
// display edges like a real mouse.
func (d *cgDriver) clamp(from, to cgPoint) cgPoint {
	if time.Since(d.displaysAt) > 2*time.Second {
		var ids [16]uint32
		var n uint32
		if cgGetActiveDisplayList(uint32(len(ids)), &ids[0], &n) == 0 {
			d.displays = d.displays[:0]
			for _, id := range ids[:n] {
				d.displays = append(d.displays, cgDisplayBounds(id))
			}
		}
		d.displaysAt = time.Now()
	}
	for _, r := range d.displays {
		if r.contains(to) {
			return to
		}
	}
	for _, r := range d.displays {
		if r.contains(from) {
			to.X = min(max(to.X, r.Origin.X), r.Origin.X+r.Size.X-1)
			to.Y = min(max(to.Y, r.Origin.Y), r.Origin.Y+r.Size.Y-1)
			return to
		}
	}
	return to
}

func (d *cgDriver) Move(dx, dy int32) error {
	if dx == 0 && dy == 0 {
		return nil
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.allowed(); err != nil {
		return err
	}
	from := d.cursor()
	to := d.clamp(from, cgPoint{from.X + float64(dx), from.Y + float64(dy)})
	typ, btn := uint32(evMouseMoved), uint32(0)
	switch {
	case d.buttons[ButtonLeft]:
		typ = evLeftMouseDragged
	case d.buttons[ButtonRight]:
		typ, btn = evRightDragged, 1
	case d.buttons[ButtonMiddle]:
		typ, btn = evOtherDragged, 2
	}
	ev := cgEventCreateMouseEvent(d.src, typ, to, btn)
	if ev == 0 {
		return errors.New("CGEvent create failed")
	}
	cgEventSetIntegerValueField(ev, fieldDeltaX, int64(dx))
	cgEventSetIntegerValueField(ev, fieldDeltaY, int64(dy))
	cgEventSetFlags(ev, d.mods)
	return d.post(ev)
}

func (d *cgDriver) Scroll(dx, dy int32) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.allowed(); err != nil {
		return err
	}
	// 1/120 notch → pixels, keeping the remainder for smooth slow scrolls.
	d.scrollX += dx * scrollPxPerNotch
	d.scrollY += dy * scrollPxPerNotch
	px, py := d.scrollX/120, d.scrollY/120
	d.scrollX -= px * 120
	d.scrollY -= py * 120
	if px == 0 && py == 0 {
		return nil
	}
	// macOS: positive wheel values scroll up / left.
	ev := cgEventCreateScrollWheelEvent2(d.src, scrollUnitPixel, 2, -py, -px, 0)
	if ev == 0 {
		return errors.New("CGEvent create failed")
	}
	cgEventSetIntegerValueField(ev, fieldScrollContinue, 1)
	cgEventSetFlags(ev, d.mods)
	return d.post(ev)
}

func (d *cgDriver) Button(b Button, down bool) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.allowed(); err != nil {
		return err
	}
	var typ, btn uint32
	switch b {
	case ButtonLeft:
		typ = uint32(pick(down, evLeftMouseDown, evLeftMouseUp))
	case ButtonRight:
		typ, btn = uint32(pick(down, evRightMouseDown, evRightMouseUp)), 1
	case ButtonMiddle, ButtonBack, ButtonForward:
		typ = uint32(pick(down, evOtherMouseDown, evOtherMouseUp))
		btn = map[Button]uint32{ButtonMiddle: 2, ButtonBack: 3, ButtonForward: 4}[b]
	default:
		return fmt.Errorf("unknown button %d", b)
	}
	pos := d.cursor()
	if down {
		near := abs(pos.X-d.clickPos.X) < 5 && abs(pos.Y-d.clickPos.Y) < 5
		if b == d.clickBtn && near && time.Since(d.clickAt) < d.dblClick {
			d.clickCount++
		} else {
			d.clickCount = 1
		}
		d.clickBtn, d.clickAt, d.clickPos = b, time.Now(), pos
		d.buttons[b] = true
	} else {
		delete(d.buttons, b)
	}
	ev := cgEventCreateMouseEvent(d.src, typ, pos, btn)
	if ev == 0 {
		return errors.New("CGEvent create failed")
	}
	cgEventSetIntegerValueField(ev, fieldClickState, d.clickCount)
	cgEventSetIntegerValueField(ev, fieldButtonNumber, int64(btn))
	cgEventSetFlags(ev, d.mods)
	return d.post(ev)
}

func pick(c bool, a, b int) int {
	if c {
		return a
	}
	return b
}

func abs(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}

func (d *cgDriver) key(code uint16, down bool, flags uint64) error {
	ev := cgEventCreateKeyboardEvent(d.src, code, down)
	if ev == 0 {
		return errors.New("CGEvent create failed")
	}
	cgEventSetFlags(ev, flags)
	return d.post(ev)
}

// mediaKey posts a system-defined media key event (volume, play, brightness).
func (d *cgDriver) mediaKey(nx int, down bool) error {
	if d.otherEvent == 0 {
		return ErrUnknownKey
	}
	pool := objcAutoreleasePoolPush()
	defer objcAutoreleasePoolPop(pool)
	state := int64(0xB) // key up
	if down {
		state = 0xA
	}
	const systemDefined, auxControlButtons = 14, 8
	ns := objcMsgSendOtherEvent(d.nsEvent, d.otherEvent, systemDefined, cgPoint{}, uint64(state<<8), 0, 0, 0,
		auxControlButtons, int64(nx)<<16|state<<8, -1)
	if ns == 0 {
		return errors.New("NSEvent create failed")
	}
	ev := objcMsgSend(ns, d.cgEventSel) // owned by the NSEvent
	if ev == 0 {
		return errors.New("NSEvent has no CGEvent")
	}
	cgEventPost(hidEventTap, ev)
	return nil
}

func (d *cgDriver) Key(name string, down bool, _ string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.allowed(); err != nil {
		return err
	}
	if k, ok := macKeys[name]; ok {
		switch {
		case k.nx != 0:
			return d.mediaKey(k.nx-1, down)
		case k.mod != 0:
			if down {
				d.mods |= k.mod
			} else {
				d.mods &^= k.mod
			}
			ev := cgEventCreateKeyboardEvent(d.src, k.code, down)
			if ev == 0 {
				return errors.New("CGEvent create failed")
			}
			cgEventSetType(ev, evFlagsChanged)
			cgEventSetFlags(ev, d.mods)
			return d.post(ev)
		}
		return d.key(k.code, down, d.mods|k.flags)
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
		return d.key(s.code, false, d.mods|s.flags)
	}
	s, ok := d.strokes[r]
	if !ok {
		return ErrUnknownKey
	}
	d.held[name] = s
	return d.key(s.code, true, d.mods|s.flags)
}

func (d *cgDriver) Type(text string, _ string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.allowed(); err != nil {
		return err
	}
	tap := func(code uint16) error {
		if err := d.key(code, true, 0); err != nil {
			return err
		}
		return d.key(code, false, 0)
	}
	for _, r := range text {
		var err error
		switch r {
		case '\n', '\r':
			err = tap(kvkReturn)
		case '\t':
			err = tap(kvkTab)
		case '\b':
			err = tap(kvkBackspace)
		default:
			// A Unicode string event types any character regardless of the layout.
			u := utf16.Encode([]rune{r})
			for _, down := range []bool{true, false} {
				ev := cgEventCreateKeyboardEvent(d.src, 0, down)
				if ev == 0 {
					return errors.New("CGEvent create failed")
				}
				cgEventKeyboardSetUnicodeString(ev, uint64(len(u)), &u[0])
				cgEventSetFlags(ev, 0)
				if err = d.post(ev); err != nil {
					break
				}
			}
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func (d *cgDriver) Close() error {
	if d.src != 0 {
		cfRelease(d.src)
		d.src = 0
	}
	return nil
}
