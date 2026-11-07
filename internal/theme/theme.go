// Package theme defines the application's colours, taken from the logo: navy
// and yellow. It has no Windows dependencies, so the palettes can be tested on
// any platform.
package theme

// Color is an sRGB colour.
type Color struct{ R, G, B uint8 }

// Logo colours.
var (
	Navy   = Color{0x2C, 0x4F, 0x9E}
	Yellow = Color{0xF5, 0xC5, 0x18}
)

// Palette holds the colours of one appearance.
type Palette struct {
	Dark       bool
	Background Color // window and list
	Input      Color // text boxes
	Border     Color // outlines of the window and the text boxes
	Text       Color
	Subtle     Color // secondary text on the background, such as masked passwords
	Accent     Color // marker of the selected row and underline of the focused search
	Selection  Color // background of the selected row
	Error      Color
}

// New returns the palette for the light or dark Windows app mode.
func New(dark bool) Palette {
	if dark {
		return Palette{
			Dark:       true,
			Background: Color{0x1A, 0x21, 0x2D},
			Input:      Color{0x22, 0x2B, 0x3A},
			Border:     Color{0x2C, 0x36, 0x47},
			Text:       Color{0xE7, 0xEA, 0xF0},
			Subtle:     Color{0x9A, 0xA5, 0xB5},
			Accent:     Yellow,
			Selection:  Navy,
			Error:      Color{0xFF, 0x99, 0xA4},
		}
	}
	// Yellow is unreadable on a light background, so navy takes its place.
	return Palette{
		Background: Color{0xF4, 0xF6, 0xFA},
		Input:      Color{0xFF, 0xFF, 0xFF},
		Border:     Color{0xCF, 0xD6, 0xE2},
		Text:       Color{0x13, 0x19, 0x23},
		Subtle:     Color{0x4F, 0x5B, 0x6D},
		Accent:     Navy,
		Selection:  Color{0xD6, 0xE0, 0xF5},
		Error:      Color{0xC4, 0x2B, 0x1C},
	}
}
