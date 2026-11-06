// Package placement computes where the popup appears relative to the taskbar.
package placement

// Rect is a screen rectangle in pixels; Right and Bottom are exclusive.
type Rect struct{ Left, Top, Right, Bottom int32 }

// Popup returns the top-left corner of a width x height window placed in the
// corner of the work area next to the tray. monitor is the full monitor, work
// the part not covered by the taskbar, so the difference between them shows
// which edge the taskbar is on. The default is the bottom-right corner, which
// also covers an auto-hidden taskbar.
func Popup(monitor, work Rect, width, height, margin int32) (x, y int32) {
	x = work.Right - width - margin
	y = work.Bottom - height - margin
	if work.Left > monitor.Left {
		x = work.Left + margin
	}
	if work.Top > monitor.Top {
		y = work.Top + margin
	}
	return x, y
}
