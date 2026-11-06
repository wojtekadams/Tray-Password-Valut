// Package hotkey parses shortcut strings such as "Ctrl+Alt+W" into the values
// expected by the Win32 RegisterHotKey function.
package hotkey

import (
	"fmt"
	"strconv"
	"strings"
)

// Modifier flags of RegisterHotKey.
const (
	ModAlt     = 0x0001
	ModControl = 0x0002
	ModShift   = 0x0004
	ModWin     = 0x0008
)

// Hotkey is a parsed shortcut.
type Hotkey struct {
	Modifiers uint32
	Key       uint32 // Windows virtual-key code
}

var namedKeys = map[string]uint32{
	"SPACE": 0x20, "ENTER": 0x0D, "TAB": 0x09, "INSERT": 0x2D, "DELETE": 0x2E,
	"HOME": 0x24, "END": 0x23, "PAGEUP": 0x21, "PAGEDOWN": 0x22,
}

// Parse reads a "+"-separated shortcut. It requires at least one modifier, so
// a typo cannot capture a plain key system-wide.
func Parse(s string) (Hotkey, error) {
	var h Hotkey
	parts := strings.Split(s, "+")
	for _, part := range parts[:len(parts)-1] {
		switch strings.ToUpper(strings.TrimSpace(part)) {
		case "WIN", "WINDOWS":
			h.Modifiers |= ModWin
		case "CTRL", "CONTROL":
			h.Modifiers |= ModControl
		case "ALT":
			h.Modifiers |= ModAlt
		case "SHIFT":
			h.Modifiers |= ModShift
		default:
			return Hotkey{}, fmt.Errorf("unknown modifier %q in %q", part, s)
		}
	}
	if h.Modifiers == 0 {
		return Hotkey{}, fmt.Errorf("shortcut %q needs a modifier", s)
	}

	key := strings.ToUpper(strings.TrimSpace(parts[len(parts)-1]))
	switch {
	case len(key) == 1 && (key[0] >= 'A' && key[0] <= 'Z' || key[0] >= '0' && key[0] <= '9'):
		h.Key = uint32(key[0]) // VK codes of letters and digits are their ASCII codes
	case namedKeys[key] != 0:
		h.Key = namedKeys[key]
	case strings.HasPrefix(key, "F"):
		n, err := strconv.Atoi(key[1:])
		if err != nil || n < 1 || n > 24 {
			return Hotkey{}, fmt.Errorf("unknown key %q in %q", key, s)
		}
		h.Key = 0x70 + uint32(n) - 1
	default:
		return Hotkey{}, fmt.Errorf("unknown key %q in %q", key, s)
	}
	return h, nil
}
