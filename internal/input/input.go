// Package input injects mouse and keyboard events into the host OS.
package input

import (
	"errors"
	"unicode/utf8"
)

// Button is a mouse button.
type Button uint8

const (
	ButtonLeft Button = iota + 1
	ButtonRight
	ButtonMiddle
	ButtonBack
	ButtonForward
)

// Valid reports whether b is a known button.
func (b Button) Valid() bool { return b >= ButtonLeft && b <= ButtonForward }

// ErrUnknownKey is returned for key names the driver cannot map.
var ErrUnknownKey = errors.New("unknown key")

// Driver injects input. Implementations must be safe for concurrent use.
//
// Scroll units are 1/120 of a wheel notch (the Windows/Linux hi-res convention);
// positive dy scrolls down (content moves up), positive dx scrolls right.
//
// Key names follow the web KeyboardEvent.key values ("Enter", "ArrowUp",
// "Control", "MediaPlayPause", ...) or a single printable character ("c", "+").
// layout is the XKB layout id of the target desktop (used on Linux only).
type Driver interface {
	Name() string
	Move(dx, dy int32) error
	Scroll(dx, dy int32) error
	Button(b Button, down bool) error
	Key(name string, down bool, layout string) error
	Type(text string, layout string) error
	Close() error
}

// IsChar reports whether a key name is a single printable character rather
// than a named key.
func IsChar(name string) (rune, bool) {
	r, n := utf8.DecodeRuneInString(name)
	if r == utf8.RuneError || n != len(name) || r < 0x20 {
		return 0, false
	}
	return r, true
}

// keyDef maps a named key to platform codes. Zero means unsupported.
type keyDef struct {
	linux uint16 // evdev KEY_*
	win   uint16 // Windows virtual-key code
	ext   bool   // Windows KEYEVENTF_EXTENDEDKEY
}

var namedKeys = map[string]keyDef{
	"Escape":      {1, 0x1B, false},
	"Backspace":   {14, 0x08, false},
	"Tab":         {15, 0x09, false},
	"Enter":       {28, 0x0D, false},
	" ":           {57, 0x20, false},
	"Space":       {57, 0x20, false},
	"Spacebar":    {57, 0x20, false},
	"Control":     {29, 0xA2, false},
	"Shift":       {42, 0xA0, false},
	"Alt":         {56, 0xA4, false},
	"AltGraph":    {100, 0xA5, true},
	"Meta":        {125, 0x5B, true},
	"OS":          {125, 0x5B, true},
	"Super":       {125, 0x5B, true},
	"CapsLock":    {58, 0x14, false},
	"NumLock":     {69, 0x90, true},
	"ScrollLock":  {70, 0x91, false},
	"Pause":       {119, 0x13, false},
	"PrintScreen": {99, 0x2C, true},
	"ContextMenu": {127, 0x5D, true},
	"Insert":      {110, 0x2D, true},
	"Delete":      {111, 0x2E, true},
	"Home":        {102, 0x24, true},
	"End":         {107, 0x23, true},
	"PageUp":      {104, 0x21, true},
	"PageDown":    {109, 0x22, true},
	"ArrowUp":     {103, 0x26, true},
	"ArrowDown":   {108, 0x28, true},
	"ArrowLeft":   {105, 0x25, true},
	"ArrowRight":  {106, 0x27, true},

	"F1": {59, 0x70, false}, "F2": {60, 0x71, false}, "F3": {61, 0x72, false},
	"F4": {62, 0x73, false}, "F5": {63, 0x74, false}, "F6": {64, 0x75, false},
	"F7": {65, 0x76, false}, "F8": {66, 0x77, false}, "F9": {67, 0x78, false},
	"F10": {68, 0x79, false}, "F11": {87, 0x7A, false}, "F12": {88, 0x7B, false},

	"AudioVolumeUp":      {115, 0xAF, true},
	"AudioVolumeDown":    {114, 0xAE, true},
	"AudioVolumeMute":    {113, 0xAD, true},
	"MediaPlayPause":     {164, 0xB3, true},
	"MediaStop":          {166, 0xB2, true},
	"MediaTrackNext":     {163, 0xB0, true},
	"MediaTrackPrevious": {165, 0xB1, true},
	"BrowserBack":        {158, 0xA6, true},
	"BrowserForward":     {159, 0xA7, true},
	"BrowserHome":        {172, 0xAC, true},
	"BrowserRefresh":     {173, 0xA8, true},
	"BrightnessUp":       {225, 0, false},
	"BrightnessDown":     {224, 0, false},
}

func init() {
	// F13..F24: evdev 183..194, VK 0x7C..0x87.
	for i := 0; i < 12; i++ {
		namedKeys["F"+itoa(13+i)] = keyDef{uint16(183 + i), uint16(0x7C + i), false}
	}
}

func itoa(i int) string {
	if i < 10 {
		return string(rune('0' + i))
	}
	return itoa(i/10) + string(rune('0'+i%10))
}

// KnownKey reports whether name is a supported named key or a character.
func KnownKey(name string) bool {
	if _, ok := namedKeys[name]; ok {
		return true
	}
	_, ok := IsChar(name)
	return ok
}
