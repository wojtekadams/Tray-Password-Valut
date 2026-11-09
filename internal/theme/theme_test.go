package theme

import (
	"math"
	"testing"
)

func luminance(c Color) float64 {
	linear := func(v uint8) float64 {
		s := float64(v) / 255
		if s <= 0.03928 {
			return s / 12.92
		}
		return math.Pow((s+0.055)/1.055, 2.4)
	}
	return 0.2126*linear(c.R) + 0.7152*linear(c.G) + 0.0722*linear(c.B)
}

func contrast(a, b Color) float64 {
	la, lb := luminance(a), luminance(b)
	return (math.Max(la, lb) + 0.05) / (math.Min(la, lb) + 0.05)
}

func TestMix(t *testing.T) {
	a, b := Color{0, 100, 255}, Color{100, 200, 55}
	for _, check := range []struct {
		t    float64
		want Color
	}{
		{0, a}, {1, b}, {0.5, Color{50, 150, 155}},
	} {
		if got := Mix(a, b, check.t); got != check.want {
			t.Errorf("Mix(%v, %v, %v) = %v, want %v", a, b, check.t, got, check.want)
		}
	}
}

func TestModeIsDark(t *testing.T) {
	for _, check := range []struct {
		mode             Mode
		systemDark, want bool
	}{
		{Auto, false, false}, {Auto, true, true},
		{Light, false, false}, {Light, true, false},
		{Dark, false, true}, {Dark, true, true},
		{"unknown", true, true}, {"unknown", false, false},
	} {
		if got := check.mode.IsDark(check.systemDark); got != check.want {
			t.Errorf("%q.IsDark(%v) = %v, want %v", check.mode, check.systemDark, got, check.want)
		}
	}
}

// Text must be readable (WCAG AA, 4.5:1) and the selected-row marker visible
// (3:1, as for non-text elements) in both appearances.
func TestContrast(t *testing.T) {
	for _, dark := range []bool{false, true} {
		p := New(dark)
		for name, check := range map[string]struct {
			pair [2]Color
			min  float64
		}{
			"text on background":   {[2]Color{p.Text, p.Background}, 4.5},
			"subtle on background": {[2]Color{p.Subtle, p.Background}, 4.5},
			"text on input":        {[2]Color{p.Text, p.Input}, 4.5},
			"text on selection":    {[2]Color{p.Text, p.Selection}, 4.5},
			"error on background":  {[2]Color{p.Error, p.Background}, 4.5},
			"marker on selection":  {[2]Color{p.Accent, p.Selection}, 3},
			"underline on input":   {[2]Color{p.Accent, p.Input}, 3},
			"text on main button":  {[2]Color{p.OnAccent, p.Accent}, 4.5},
			"button outline":       {[2]Color{p.Accent, p.Background}, 3},
		} {
			if got := contrast(check.pair[0], check.pair[1]); got < check.min {
				t.Errorf("dark=%v: %s contrast %.2f, want at least %.1f", dark, name, got, check.min)
			}
		}
	}
}
