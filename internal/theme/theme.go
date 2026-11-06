// Package theme defines the application's Windows 11 style colours. It has no
// Windows dependencies, so the palettes can be tested on any platform.
package theme

// Color is an sRGB colour.
type Color struct{ R, G, B uint8 }

// DefaultAccent is the Windows 11 default accent colour.
var DefaultAccent = Color{0x00, 0x67, 0xC0}

// Palette holds the colours of one appearance.
type Palette struct {
	Dark       bool
	Background Color // window and list
	Input      Color // text boxes
	Text       Color
	Subtle     Color // secondary text on the background, such as masked passwords
	Accent     Color // marker of the selected row
	Selection  Color // background of the selected row
	Error      Color
}

// New returns the palette for an appearance and accent colour. The selection
// is only a tint of the accent, so text stays readable on any accent colour.
func New(dark bool, accent Color) Palette {
	if dark {
		background := Color{0x20, 0x20, 0x20}
		return Palette{
			Dark:       true,
			Background: background,
			Input:      Color{0x2D, 0x2D, 0x2D},
			Text:       Color{0xFF, 0xFF, 0xFF},
			Subtle:     Color{0xC8, 0xC8, 0xC8},
			Accent:     accent,
			Selection:  Mix(background, accent, 0.25),
			Error:      Color{0xFF, 0x99, 0xA4},
		}
	}
	background := Color{0xF3, 0xF3, 0xF3}
	return Palette{
		Background: background,
		Input:      Color{0xFF, 0xFF, 0xFF},
		Text:       Color{0x1B, 0x1B, 0x1B},
		Subtle:     Color{0x5C, 0x5C, 0x5C},
		Accent:     accent,
		Selection:  Mix(background, accent, 0.18),
		Error:      Color{0xC4, 0x2B, 0x1C},
	}
}

// Mix returns a blended with b, t being the share of b.
func Mix(a, b Color, t float64) Color {
	channel := func(x, y uint8) uint8 { return uint8(float64(x) + (float64(y)-float64(x))*t + 0.5) }
	return Color{channel(a.R, b.R), channel(a.G, b.G), channel(a.B, b.B)}
}

// FromABGR converts the 0xAABBGGRR value of the registry's AccentColor.
func FromABGR(v uint32) Color {
	return Color{uint8(v), uint8(v >> 8), uint8(v >> 16)}
}
