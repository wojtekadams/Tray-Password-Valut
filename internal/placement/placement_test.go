package placement

import "testing"

func TestPopup(t *testing.T) {
	monitor := Rect{0, 0, 1920, 1080}
	cases := []struct {
		name string
		work Rect
		x, y int32
	}{
		{"taskbar bottom", Rect{0, 0, 1920, 1032}, 1570, 672},
		{"taskbar hidden", Rect{0, 0, 1920, 1080}, 1570, 720},
		{"taskbar top", Rect{0, 48, 1920, 1080}, 1570, 60},
		{"taskbar left", Rect{48, 0, 1920, 1080}, 60, 720},
		{"taskbar right", Rect{0, 0, 1872, 1080}, 1522, 720},
	}
	for _, c := range cases {
		x, y := Popup(monitor, c.work, 338, 348, 12)
		if x != c.x || y != c.y {
			t.Errorf("%s: got %d,%d want %d,%d", c.name, x, y, c.x, c.y)
		}
	}

	// A second monitor to the left has negative coordinates.
	left := Rect{-1920, 0, 0, 1080}
	x, y := Popup(left, Rect{-1920, 0, 0, 1032}, 338, 348, 12)
	if x != -350 || y != 672 {
		t.Errorf("left monitor: got %d,%d", x, y)
	}
}
