// Package keymap translates characters into physical key strokes for a given
// XKB keyboard layout. It is used on Linux, where uinput can only press keys
// and the desktop's active layout decides which character a key produces.
package keymap

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Modifier bits used in Stroke.
const (
	ModShift uint8 = 1 << iota
	ModAltGr
)

// Stroke is how to type one character: optionally a dead key first, then a key.
// Codes are Linux evdev key codes.
type Stroke struct {
	Key      uint16
	Mods     uint8
	DeadKey  uint16
	DeadMods uint8
}

// Layout is a character → stroke table for one XKB layout/variant.
type Layout struct {
	ID      string
	Name    string
	Strokes map[rune]Stroke
}

// Lookup returns the stroke for r. Newline and tab are handled by callers.
func (l *Layout) Lookup(r rune) (Stroke, bool) {
	if r == ' ' {
		return Stroke{Key: 57}, true
	}
	s, ok := l.Strokes[r]
	return s, ok
}

var byID = func() map[string]*Layout {
	m := make(map[string]*Layout, len(generated))
	for _, l := range generated {
		m[l.ID] = l
	}
	return m
}()

// Default is the fallback layout.
const Default = "us"

// Get returns the layout with the given id (e.g. "cz" or "cz(qwerty)").
// Unknown variants fall back to the base layout, unknown layouts to "us".
func Get(id string) *Layout {
	id = strings.ToLower(strings.TrimSpace(id))
	if l, ok := byID[id]; ok {
		return l
	}
	if i := strings.IndexByte(id, '('); i > 0 {
		if l, ok := byID[id[:i]]; ok {
			return l
		}
	}
	return byID[Default]
}

// Has reports whether an exact layout id exists.
func Has(id string) bool {
	_, ok := byID[id]
	return ok
}

// Info is a layout id + display name.
type Info struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// List returns all layouts sorted by name.
func List() []Info {
	out := make([]Info, 0, len(generated))
	for _, l := range generated {
		out = append(out, Info{l.ID, l.Name})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

var (
	reDefault = regexp.MustCompile(`^\s*(XKBLAYOUT|XKBVARIANT)\s*=\s*"?([^"\n]*)"?`)
	reXorg    = regexp.MustCompile(`(?i)Option\s+"Xkb(Layout|Variant)"\s+"([^"]*)"`)
	reKDE     = regexp.MustCompile(`^(LayoutList|VariantList|Use)=(.*)$`)
)

// Detect guesses the keyboard layout: the user's KDE Plasma layouts
// (~/.config/kxkbrc, when running as the desktop user), then Debian/Raspberry
// Pi OS (/etc/default/keyboard), then X11 config
// (/etc/X11/xorg.conf.d/00-keyboard.conf, written by localectl on most
// distros). Returns "" if unknown.
func Detect() string {
	if dir, err := os.UserConfigDir(); err == nil {
		if id := detectKDE(filepath.Join(dir, "kxkbrc")); id != "" {
			return id
		}
	}
	var layout, variant string
	read := func(path string, re *regexp.Regexp) bool {
		f, err := os.Open(path)
		if err != nil {
			return false
		}
		defer f.Close()
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			m := re.FindStringSubmatch(sc.Text())
			if m == nil {
				continue
			}
			switch strings.ToLower(m[1]) {
			case "xkblayout", "layout":
				layout = m[2]
			case "xkbvariant", "variant":
				variant = m[2]
			}
		}
		return layout != ""
	}
	if !read("/etc/default/keyboard", reDefault) {
		read("/etc/X11/xorg.conf.d/00-keyboard.conf", reXorg)
	}
	return Normalize(layout, variant)
}

// detectKDE reads the first layout of a KDE kxkbrc file, if KDE manages layouts.
func detectKDE(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	var layout, variant string
	use := false
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		m := reKDE.FindStringSubmatch(strings.TrimSpace(sc.Text()))
		switch {
		case m == nil:
		case m[1] == "LayoutList":
			layout = m[2]
		case m[1] == "VariantList":
			variant = m[2]
		case m[1] == "Use":
			use = m[2] == "true"
		}
	}
	if !use {
		return ""
	}
	return Normalize(layout, variant)
}

// Normalize turns an XKB layout/variant pair (possibly comma lists, first
// entry wins) into a layout id known to this package, or "".
func Normalize(layout, variant string) string {
	layout = strings.ToLower(strings.TrimSpace(strings.Split(layout, ",")[0]))
	variant = strings.ToLower(strings.TrimSpace(strings.Split(variant, ",")[0]))
	if layout == "" {
		return ""
	}
	if variant != "" && Has(layout+"("+variant+")") {
		return layout + "(" + variant + ")"
	}
	if Has(layout) {
		return layout
	}
	return ""
}
