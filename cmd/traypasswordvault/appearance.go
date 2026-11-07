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
	procFindWindowEx = user32.NewProc("FindWindowExW")

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

func (a *application) applyPalette(p theme.Palette) {
	background, err := walk.NewSolidColorBrush(walkColor(p.Background))
	if err != nil {
		return
	}
	input, err := walk.NewSolidColorBrush(walkColor(p.Input))
	if err != nil {
		background.Dispose()
		return
	}
	oldBackground, oldInput := a.backgroundBrush, a.inputBrush
	a.palette, a.backgroundBrush, a.inputBrush = p, background, input

	a.mw.SetBackground(background)
	setDarkTitle(a.mw.Handle(), p.Dark)
	a.themeChildren(a.mw)
	for _, list := range listViews(a.table.Handle()) {
		win.SendMessage(list, win.LVM_SETBKCOLOR, 0, uintptr(walkColor(p.Background)))
		setControlTheme(list, p.Dark, "DarkMode_Explorer")
	}
	a.table.Invalidate()

	if oldBackground != nil {
		oldBackground.Dispose()
		oldInput.Dispose()
	}
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
			w.SetBackground(a.inputBrush)
			w.SetTextColor(walkColor(p.Text))
			setControlTheme(w.Handle(), p.Dark, "DarkMode_CFD")
		case *walk.PushButton:
			setControlTheme(w.Handle(), p.Dark, "DarkMode_Explorer")
		case *walk.Composite:
			a.themeChildren(w)
		}
	}
}

// styleCell paints a list cell. walk does not let the selection colour be
// changed, so the cells are drawn here.
func (a *application) styleCell(style *walk.CellStyle) {
	if style.Col() < 0 || style.Row() >= len(a.model.rows) {
		return
	}
	canvas := style.Canvas()
	if canvas == nil {
		return
	}
	p := a.palette
	selected := style.Row() == a.table.CurrentIndex()
	fill, foreground := p.Background, p.Text
	if selected {
		fill = p.Selection
	} else if style.Col() == passwordColumn {
		foreground = p.Subtle
	}

	bounds := style.BoundsPixels()
	if brush, err := walk.NewSolidColorBrush(walkColor(fill)); err == nil {
		canvas.FillRectanglePixels(brush, bounds)
		brush.Dispose()
	}
	if selected && style.Col() == 0 {
		inset := a.table.IntFrom96DPI(4)
		marker := walk.Rectangle{
			X: bounds.X, Y: bounds.Y + inset,
			Width: a.table.IntFrom96DPI(3), Height: bounds.Height - 2*inset,
		}
		if brush, err := walk.NewSolidColorBrush(walkColor(p.Accent)); err == nil {
			canvas.FillRectanglePixels(brush, marker)
			brush.Dispose()
		}
	}

	padding := a.table.IntFrom96DPI(8)
	bounds.X += padding
	bounds.Width -= 2 * padding
	text, _ := a.model.Value(style.Row(), style.Col()).(string)
	if a.showCopied && style.Col() == a.copiedColumn && a.model.rows[style.Row()] == a.copied {
		text = copiedNotice
	}
	canvas.DrawTextPixels(text, a.table.Font(), walkColor(foreground), bounds,
		walk.TextLeft|walk.TextVCenter|walk.TextSingleLine|walk.TextEndEllipsis)
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

func removeBorder(hwnd win.HWND) {
	style := uint32(win.GetWindowLong(hwnd, win.GWL_STYLE))
	win.SetWindowLong(hwnd, win.GWL_STYLE, int32(style&^win.WS_BORDER))
	win.SetWindowPos(hwnd, 0, 0, 0, 0, 0,
		win.SWP_NOMOVE|win.SWP_NOSIZE|win.SWP_NOZORDER|win.SWP_FRAMECHANGED)
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
