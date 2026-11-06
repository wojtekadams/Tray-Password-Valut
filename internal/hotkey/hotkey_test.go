package hotkey

import "testing"

func TestParse(t *testing.T) {
	cases := []struct {
		in   string
		want Hotkey
	}{
		{"Win+W", Hotkey{ModWin, 'W'}},
		{"win + shift + w", Hotkey{ModWin | ModShift, 'W'}},
		{"Ctrl+Alt+W", Hotkey{ModControl | ModAlt, 'W'}},
		{"Ctrl+Shift+Space", Hotkey{ModControl | ModShift, 0x20}},
		{"Alt+F12", Hotkey{ModAlt, 0x7B}},
		{"Ctrl+1", Hotkey{ModControl, '1'}},
	}
	for _, c := range cases {
		got, err := Parse(c.in)
		if err != nil || got != c.want {
			t.Errorf("Parse(%q) = %+v, %v; want %+v", c.in, got, err, c.want)
		}
	}
}

func TestParseRejects(t *testing.T) {
	for _, in := range []string{"", "W", "Ctrl+", "Foo+W", "Ctrl+F99", "Ctrl+Fx", "Ctrl+!"} {
		if got, err := Parse(in); err == nil {
			t.Errorf("Parse(%q) = %+v, want error", in, got)
		}
	}
}
