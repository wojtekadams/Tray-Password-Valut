//go:build windows

package main

import (
	"os"
	"path/filepath"
	"syscall"
	"unsafe"

	"github.com/tailscale/walk"
	. "github.com/tailscale/walk/declarative"
	"github.com/tailscale/win"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"

	"github.com/wojtekadams/Tray-Password-Valut/internal/theme"
)

const (
	personalizeKeyPath      = `Software\Microsoft\Windows\CurrentVersion\Themes\Personalize`
	dwmUseImmersiveDarkMode = 20
	dwmBorderColor          = 34
	firstDarkModeBuild      = 18362 // Windows 10 1903, where the ordinals below exist
	appModeAllowDark        = 1

	// Undocumented uxtheme.dll exports, also used by File Explorer. If they
	// are missing, the application simply stays light.
	ordinalRefreshPolicy    = 104
	ordinalAllowDarkWindow  = 133
	ordinalPreferredAppMode = 135
	ordinalFlushMenuThemes  = 136
)

var (
	procFindWindowEx       = user32.NewProc("FindWindowExW")
	procSetWindowRgn       = user32.NewProc("SetWindowRgn")
	gdi32                  = windows.NewLazySystemDLL("gdi32.dll")
	procCreateRoundRectRgn = gdi32.NewProc("CreateRoundRectRgn")

	allowDarkModeForWindow uintptr // 0 if dark mode is unavailable
)

// initDarkMode lets menus and controls follow the system's dark mode. It must
// run before any window is created.
func initDarkMode() {
	_, _, build := windows.RtlGetNtVersionNumbers()
	if build&0xFFFF < firstDarkModeBuild {
		return
	}
	uxtheme, err := windows.LoadLibraryEx("uxtheme.dll", 0, windows.LOAD_LIBRARY_SEARCH_SYSTEM32)
	if err != nil {
		return
	}
	procs := map[uintptr]uintptr{}
	for _, ordinal := range []uintptr{
		ordinalRefreshPolicy, ordinalAllowDarkWindow, ordinalPreferredAppMode, ordinalFlushMenuThemes,
	} {
		proc, err := windows.GetProcAddressByOrdinal(uxtheme, ordinal)
		if err != nil {
			return
		}
		procs[ordinal] = proc
	}
	syscall.SyscallN(procs[ordinalPreferredAppMode], appModeAllowDark)
	syscall.SyscallN(procs[ordinalRefreshPolicy])
	syscall.SyscallN(procs[ordinalFlushMenuThemes])
	allowDarkModeForWindow = procs[ordinalAllowDarkWindow]
}

func registryDWORD(path, name string) (uint32, bool) {
	key, err := registry.OpenKey(registry.CURRENT_USER, path, registry.QUERY_VALUE)
	if err != nil {
		return 0, false
	}
	defer key.Close()
	value, _, err := key.GetIntegerValue(name)
	return uint32(value), err == nil
}

// systemPalette returns the palette for the Windows light or dark app mode.
func systemPalette() theme.Palette {
	light, ok := registryDWORD(personalizeKeyPath, "AppsUseLightTheme")
	return theme.New(allowDarkModeForWindow != 0 && ok && light == 0)
}

func walkColor(c theme.Color) walk.Color { return walk.RGB(c.R, c.G, c.B) }

// uiFont returns the Windows 11 font, or its Windows 10 predecessor.
func uiFont() Font {
	family := "Segoe UI"
	if _, err := os.Stat(filepath.Join(os.Getenv("WINDIR"), "Fonts", "SegUIVar.ttf")); err == nil {
		family = "Segoe UI Variable Text"
	}
	return Font{Family: family, PointSize: 10}
}

// updateTheme follows changes of the Windows settings. It is cheap, so it runs
// each time the popup opens.
func (a *application) updateTheme() {
	if p := systemPalette(); p != a.palette {
		a.applyPalette(p)
	}
}

// brushes paint the palette's surfaces.
type brushes struct {
	background, input, border, accent, selection *walk.SolidColorBrush
}

func newBrushes(p theme.Palette) (brushes, error) {
	var b brushes
	for _, item := range []struct {
		brush **walk.SolidColorBrush
		color theme.Color
	}{
		{&b.background, p.Background},
		{&b.input, p.Input},
		{&b.border, p.Border},
		{&b.accent, p.Accent},
		{&b.selection, p.Selection},
	} {
		brush, err := walk.NewSolidColorBrush(walkColor(item.color))
		if err != nil {
			b.dispose()
			return brushes{}, err
		}
		*item.brush = brush
	}
	return b, nil
}

func (b brushes) dispose() {
	for _, brush := range []*walk.SolidColorBrush{b.background, b.input, b.border, b.accent, b.selection} {
		if brush != nil {
			brush.Dispose()
		}
	}
}

func (a *application) applyPalette(p theme.Palette) {
	b, err := newBrushes(p)
	if err != nil {
		return
	}
	old := a.brushes
	a.palette, a.brushes = p, b

	a.mw.SetBackground(b.background)
	setDarkTitle(a.mw.Handle(), p.Dark)
	setBorderColor(a.mw.Handle(), p.Border)
	a.themeChildren(a.mw)
	a.searchBorder.SetBackground(b.border)
	a.searchInner.SetBackground(b.input)
	a.paintSearchLine()
	a.addButton.Invalidate()
	for _, list := range a.lists {
		win.SendMessage(list, win.LVM_SETBKCOLOR, 0, uintptr(walkColor(p.Background)))
		setControlTheme(list, p.Dark, "DarkMode_Explorer")
	}
	a.table.Invalidate()

	old.dispose()
}

// paintSearchLine underlines the search box in the accent colour while it has
// the focus, like a Windows 11 text box.
func (a *application) paintSearchLine() {
	color := a.palette.Border
	if a.search.Focused() {
		color = a.palette.Accent
	}
	// A brush of its own: walk disposes of a brush once no window uses it as
	// background any more, and the shared ones are also used for drawing.
	if brush, err := walk.NewSolidColorBrush(walkColor(color)); err == nil {
		a.searchFrame.SetBackground(brush)
	}
}

// paintAddButton draws the add button in the style of the search box.
func (a *application) paintAddButton(canvas *walk.Canvas, _ walk.Rectangle) error {
	p, w := a.palette, a.addButton
	px := w.IntFrom96DPI
	bounds := w.ClientBoundsPixels()
	corner := walk.Size{Width: px(cornerSize), Height: px(cornerSize)}
	canvas.FillRectanglePixels(a.brushes.background, bounds)
	canvas.FillRoundedRectanglePixels(a.brushes.border, bounds, corner)
	inner := walk.Rectangle{
		X: bounds.X + px(1), Y: bounds.Y + px(1),
		Width: bounds.Width - 2*px(1), Height: bounds.Height - px(1) - px(searchLine),
	}
	canvas.FillRoundedRectanglePixels(a.brushes.input, inner, corner)
	return canvas.DrawTextPixels("+", w.Font(), walkColor(p.Text), inner,
		walk.TextCenter|walk.TextVCenter|walk.TextSingleLine)
}

// themeChildren colours the widgets of a window or dialog.
func (a *application) themeChildren(container walk.Container) {
	p := a.palette
	children := container.Children()
	for i := 0; i < children.Len(); i++ {
		switch w := children.At(i).(type) {
		case *walk.Label:
			w.SetTextColor(walkColor(p.Text))
		case *walk.LineEdit:
			w.SetBackground(a.brushes.input)
			w.SetTextColor(walkColor(p.Text))
			setControlTheme(w.Handle(), p.Dark, "DarkMode_CFD")
		case *walk.PushButton:
			setControlTheme(w.Handle(), p.Dark, "DarkMode_Explorer")
		case *walk.Composite:
			a.themeChildren(w)
		}
	}
}

// styleCell paints a row. walk does not let the selection colour be changed,
// so rows are drawn here. The list has a single column holding the name, login
// and password side by side: the Windows 11 list theme would draw lines between
// real columns.
func (a *application) styleCell(style *walk.CellStyle) {
	if style.Col() != 0 || style.Row() >= len(a.model.rows) {
		return
	}
	canvas := style.Canvas()
	if canvas == nil {
		return
	}
	p, px := a.palette, a.table.IntFrom96DPI
	row := style.Row()
	selected := row == a.table.CurrentIndex()
	// The row is drawn across the visible list. The cell itself starts a few
	// pixels in and runs past the list's right edge.
	bounds := style.BoundsPixels()
	bounds.X, bounds.Width = 0, a.listWidth()

	canvas.FillRectanglePixels(a.brushes.background, bounds)
	if selected {
		// A rounded bar whose left edge, the marker, is in the accent colour.
		corner := walk.Size{Width: px(cornerSize), Height: px(cornerSize)}
		marker := px(3)
		canvas.FillRoundedRectanglePixels(a.brushes.accent, bounds, corner)
		canvas.FillRectanglePixels(a.brushes.selection, walk.Rectangle{
			X: bounds.X + marker, Y: bounds.Y,
			Width: bounds.Width - marker - corner.Width, Height: bounds.Height,
		})
		canvas.FillRoundedRectanglePixels(a.brushes.selection, walk.Rectangle{
			X: bounds.X + bounds.Width - 2*corner.Width, Y: bounds.Y,
			Width: 2 * corner.Width, Height: bounds.Height,
		}, corner)
	}

	cols := columnsOf(bounds.Width, px(padding), px(columnGap))
	for col, c := range cols {
		foreground := p.Text
		if !selected && col == passwordColumn {
			foreground = p.Subtle
		}
		text, _ := a.model.Value(row, col).(string)
		if a.showCopied && col == a.copiedColumn && a.model.rows[row] == a.copied {
			text = copiedNotice
		}
		canvas.DrawTextPixels(text, a.mw.Font(), walkColor(foreground),
			walk.Rectangle{X: bounds.X + c.start, Y: bounds.Y, Width: c.end - c.start, Height: bounds.Height},
			walk.TextLeft|walk.TextVCenter|walk.TextSingleLine|walk.TextEndEllipsis)
	}
}

func setControlTheme(hwnd win.HWND, dark bool, darkTheme string) {
	name := "Explorer"
	if dark {
		name = darkTheme
	}
	if allowDarkModeForWindow != 0 {
		var allow uintptr
		if dark {
			allow = 1
		}
		syscall.SyscallN(allowDarkModeForWindow, uintptr(hwnd), allow)
	}
	themeName, _ := windows.UTF16PtrFromString(name)
	win.SetWindowTheme(hwnd, themeName, nil)
	win.InvalidateRect(hwnd, nil, true)
}

func setDarkTitle(hwnd win.HWND, dark bool) {
	var value uint32
	if dark {
		value = 1
	}
	procDwmSetWindowAttribute.Call(uintptr(hwnd), dwmUseImmersiveDarkMode,
		uintptr(unsafe.Pointer(&value)), unsafe.Sizeof(value))
}

// setBorderColor colours the outline Windows 11 draws around the popup. Older
// versions ignore it.
func setBorderColor(hwnd win.HWND, c theme.Color) {
	value := uint32(walkColor(c))
	procDwmSetWindowAttribute.Call(uintptr(hwnd), dwmBorderColor,
		uintptr(unsafe.Pointer(&value)), unsafe.Sizeof(value))
}

func removeBorder(hwnd win.HWND) {
	style := uint32(win.GetWindowLong(hwnd, win.GWL_STYLE))
	win.SetWindowLong(hwnd, win.GWL_STYLE, int32(style&^win.WS_BORDER))
	exStyle := uint32(win.GetWindowLong(hwnd, win.GWL_EXSTYLE))
	win.SetWindowLong(hwnd, win.GWL_EXSTYLE, int32(exStyle&^(win.WS_EX_CLIENTEDGE|win.WS_EX_STATICEDGE)))
	win.SetWindowPos(hwnd, 0, 0, 0, 0, 0,
		win.SWP_NOMOVE|win.SWP_NOSIZE|win.SWP_NOZORDER|win.SWP_FRAMECHANGED)
}

// roundCorners clips a child window to a rectangle with rounded corners of
// the given diameter.
func roundCorners(hwnd win.HWND, diameter int32) {
	var rc win.RECT
	if !win.GetClientRect(hwnd, &rc) {
		return
	}
	region, _, _ := procCreateRoundRectRgn.Call(0, 0, uintptr(rc.Right+1), uintptr(rc.Bottom+1),
		uintptr(diameter), uintptr(diameter))
	if region == 0 {
		return
	}
	// The window owns the region from now on.
	if r, _, _ := procSetWindowRgn.Call(uintptr(hwnd), region, 1); r == 0 {
		win.DeleteObject(win.HGDIOBJ(region))
	}
}

// listViews returns the native list controls inside a walk TableView.
func listViews(parent win.HWND) []win.HWND {
	class, _ := windows.UTF16PtrFromString("SysListView32")
	var lists []win.HWND
	var after uintptr
	for {
		found, _, _ := procFindWindowEx.Call(uintptr(parent), after, uintptr(unsafe.Pointer(class)), 0)
		if found == 0 {
			return lists
		}
		lists = append(lists, win.HWND(found))
		after = found
	}
}
