package keymap

import "testing"

func TestUSCoversASCII(t *testing.T) {
	l := Get("us")
	for r := rune(0x20); r < 0x7f; r++ {
		if _, ok := l.Lookup(r); !ok {
			t.Errorf("us: missing %q", r)
		}
	}
	if s, _ := l.Lookup('A'); s.Key != 30 || s.Mods != ModShift {
		t.Errorf("us 'A' = %+v", s)
	}
}

func TestCzech(t *testing.T) {
	l := Get("cz")
	cases := map[rune]Stroke{
		'ě': {Key: 3},                 // AE02
		'z': {Key: 21},                // QWERTZ: z on the US 'y' key
		'y': {Key: 44},                // and y on the US 'z' key
		'1': {Key: 2, Mods: ModShift}, // digits need shift
		'@': {Key: 3, Mods: ModAltGr}, // AltGr+ě
	}
	for r, want := range cases {
		got, ok := l.Lookup(r)
		if !ok || got != want {
			t.Errorf("cz %q = %+v, want %+v", r, got, want)
		}
	}
	// ó is typed with the dead acute key followed by o.
	if s, ok := l.Lookup('ó'); !ok || s.DeadKey == 0 || s.Key != 24 {
		t.Errorf("cz 'ó' = %+v ok=%v", s, ok)
	}
}

func TestGetFallback(t *testing.T) {
	if Get("cz(nonexistent)").ID != "cz" {
		t.Error("variant fallback")
	}
	if Get("klingon").ID != "us" {
		t.Error("layout fallback")
	}
}

func TestNormalize(t *testing.T) {
	cases := [][3]string{
		{"cz,us", "qwerty,", "cz(qwerty)"},
		{"de", "", "de"},
		{"de", "bogus", "de"},
		{"xx", "", ""},
		{"", "", ""},
	}
	for _, c := range cases {
		if got := Normalize(c[0], c[1]); got != c[2] {
			t.Errorf("Normalize(%q,%q)=%q want %q", c[0], c[1], got, c[2])
		}
	}
}
