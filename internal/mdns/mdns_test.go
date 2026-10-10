package mdns

import "testing"

func TestSanitizeLabel(t *testing.T) {
	cases := map[string]string{
		"remote":      "remote",
		"Living Room": "living-room",
		"my_pc.local": "my-pc-local",
		"---":         "remote",
		"Obývák":      "obvk",
		"":            "remote",
	}
	for in, want := range cases {
		if got := sanitizeLabel(in); got != want {
			t.Errorf("sanitizeLabel(%q)=%q want %q", in, got, want)
		}
	}
}
