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

// Text must stay readable (WCAG AA) on the selection for any accent the user
// can pick, including the extremes.
func TestContrast(t *testing.T) {
	accents := []Color{
		DefaultAccent, {0, 0, 0}, {255, 255, 255}, {255, 255, 0}, {0, 255, 255},
		{255, 0, 0}, {0, 255, 0}, {0, 0, 255}, {255, 0, 255}, {255, 140, 0},
	}
	for _, dark := range []bool{false, true} {
		for _, accent := range accents {
			p := New(dark, accent)
			for name, pair := range map[string][2]Color{
				"text on background":   {p.Text, p.Background},
				"subtle on background": {p.Subtle, p.Background},
				"text on input":        {p.Text, p.Input},
				"text on selection":    {p.Text, p.Selection},
				"error on background":  {p.Error, p.Background},
			} {
				if got := contrast(pair[0], pair[1]); got < 4.5 {
					t.Errorf("dark=%v accent=%v: %s contrast %.2f", dark, accent, name, got)
				}
			}
		}
	}
}

func TestFromABGR(t *testing.T) {
	if got, want := FromABGR(0xFFC06700), DefaultAccent; got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestMix(t *testing.T) {
	a, b := Color{0, 100, 200}, Color{100, 200, 0}
	if got := Mix(a, b, 0); got != a {
		t.Errorf("t=0: %+v", got)
	}
	if got := Mix(a, b, 1); got != b {
		t.Errorf("t=1: %+v", got)
	}
	if got := Mix(a, b, 0.5); got != (Color{50, 150, 100}) {
		t.Errorf("t=0.5: %+v", got)
	}
}
