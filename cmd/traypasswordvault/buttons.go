//go:build windows

package main

import (
	"github.com/tailscale/win"
	"golang.org/x/sys/windows"

	"github.com/wojtekadams/Tray-Password-Valut/internal/theme"
)

// Push buttons are painted here, in the style of the project's web page: the
// default button filled with the accent colour, the others outlined. The
// Windows theme would give them a light border in dark mode.

const bstHot = 0x0200 // BM_GETSTATE: the mouse is over the button

var (
	buttonProcs   = map[win.HWND]uintptr{} // original window procedures
	buttonPalette func() theme.Palette     // set by the first styleButton
	buttonProc    = windows.NewCallback(styledButtonProc)
)

// styleButton makes a push button paint itself in the current palette.
func (a *application) styleButton(hwnd win.HWND) {
	buttonPalette = func() theme.Palette { return a.palette }
	if _, ok := buttonProcs[hwnd]; !ok {
		buttonProcs[hwnd] = win.SetWindowLongPtr(hwnd, win.GWLP_WNDPROC, buttonProc)
	}
	win.InvalidateRect(hwnd, nil, false)
}

func styledButtonProc(hwnd win.HWND, msg uint32, wParam, lParam uintptr) uintptr {
	original := buttonProcs[hwnd]
	switch msg {
	case win.WM_PAINT:
		var ps win.PAINTSTRUCT
		hdc := win.BeginPaint(hwnd, &ps)
		paintButton(hwnd, hdc, buttonPalette())
		win.EndPaint(hwnd, &ps)
		return 0
	case win.WM_ERASEBKGND:
		return 1
	case win.WM_NCDESTROY:
		delete(buttonProcs, hwnd)
		return win.CallWindowProc(original, hwnd, msg, wParam, lParam)
	}
	result := win.CallWindowProc(original, hwnd, msg, wParam, lParam)
	switch msg {
	// The button may have drawn itself outside WM_PAINT for these.
	case win.WM_SETFOCUS, win.WM_KILLFOCUS, win.WM_ENABLE, win.BM_SETSTATE, win.BM_SETSTYLE,
		win.WM_SETTEXT, win.WM_MOUSEMOVE, win.WM_MOUSELEAVE, win.WM_LBUTTONDOWN, win.WM_LBUTTONUP:
		win.InvalidateRect(hwnd, nil, false)
	}
	return result
}

func paintButton(hwnd win.HWND, hdc win.HDC, p theme.Palette) {
	var rc win.RECT
	win.GetClientRect(hwnd, &rc)
	state := win.SendMessage(hwnd, win.BM_GETSTATE, 0, 0)
	main := uint32(win.GetWindowLong(hwnd, win.GWL_STYLE))&0xF == win.BS_DEFPUSHBUTTON
	enabled := win.IsWindowEnabled(hwnd)

	fill, border, text := p.Input, p.Border, p.Text
	if main {
		fill, border, text = p.Accent, p.Accent, p.OnAccent
	}
	switch {
	case !enabled:
		fill, border, text = p.Input, p.Border, p.Subtle
	case state&win.BST_PUSHED != 0:
		fill = theme.Mix(fill, p.Text, 0.25)
	case state&bstHot != 0:
		fill = theme.Mix(fill, p.Text, 0.12)
	}
	if enabled && !main && state&win.BST_FOCUS != 0 {
		border = p.Accent // keyboard focus
	}
	if main {
		border = fill
	}

	scale := func(v int32) int32 { return v * int32(win.GetDpiForWindow(hwnd)) / 96 }
	brush := func(c theme.Color) win.HBRUSH {
		return win.CreateBrushIndirect(&win.LOGBRUSH{LbStyle: win.BS_SOLID, LbColor: win.COLORREF(walkColor(c))})
	}
	round := func(r win.RECT, c theme.Color) {
		b := brush(c)
		defer win.DeleteObject(win.HGDIOBJ(b))
		old := win.SelectObject(hdc, win.HGDIOBJ(b))
		defer win.SelectObject(hdc, old)
		// Without a pen RoundRect leaves out the right and bottom edges.
		d := scale(cornerSize)
		win.RoundRect(hdc, r.Left, r.Top, r.Right+1, r.Bottom+1, d, d)
	}

	background := brush(p.Background)
	win.FillRect(hdc, &rc, background)
	win.DeleteObject(win.HGDIOBJ(background))
	oldPen := win.SelectObject(hdc, win.GetStockObject(win.NULL_PEN))
	defer win.SelectObject(hdc, oldPen)
	round(rc, border)
	inner := rc
	inset := scale(1)
	if !main && state&win.BST_FOCUS != 0 {
		inset = scale(2)
	}
	inner.Left, inner.Top, inner.Right, inner.Bottom = rc.Left+inset, rc.Top+inset, rc.Right-inset, rc.Bottom-inset
	round(inner, fill)

	if font := win.HGDIOBJ(win.SendMessage(hwnd, win.WM_GETFONT, 0, 0)); font != 0 {
		old := win.SelectObject(hdc, font)
		defer win.SelectObject(hdc, old)
	}
	win.SetBkMode(hdc, win.TRANSPARENT)
	win.SetTextColor(hdc, win.COLORREF(walkColor(text)))
	buf := make([]uint16, 256)
	n := win.GetWindowText(hwnd, &buf[0], int32(len(buf)))
	win.DrawTextEx(hdc, &buf[0], n, &rc,
		win.DT_CENTER|win.DT_VCENTER|win.DT_SINGLELINE|win.DT_HIDEPREFIX, nil)
}
