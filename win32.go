//go:build windows

package main

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"time"
	"unsafe"

	"github.com/tailscale/win"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"

	"github.com/wojtekadams/Tray-Password-Valut/internal/hotkey"
	"github.com/wojtekadams/Tray-Password-Valut/internal/placement"
)

const (
	appName     = "TrayPasswordVault"
	runKeyPath  = `Software\Microsoft\Windows\CurrentVersion\Run`
	modNoRepeat = 0x4000

	wmPowerBroadcast     = 0x0218
	wmWTSSessionChange   = 0x02B1
	pbtAPMSuspend        = 0x0004
	wtsSessionLock       = 0x0007
	notifyForThisSession = 0

	dwmWindowCornerPreference = 33
	dwmCornerRound            = 2
)

var (
	user32                   = windows.NewLazySystemDLL("user32.dll")
	procRegisterHotKey       = user32.NewProc("RegisterHotKey")
	procGetMessage           = user32.NewProc("GetMessageW")
	procRegisterClipboardFmt = user32.NewProc("RegisterClipboardFormatW")
	procMonitorFromPoint     = user32.NewProc("MonitorFromPoint")

	procGetLastInputInfo = user32.NewProc("GetLastInputInfo")
	procGetTickCount     = windows.NewLazySystemDLL("kernel32.dll").NewProc("GetTickCount")
	procWTSRegister      = windows.NewLazySystemDLL("wtsapi32.dll").NewProc("WTSRegisterSessionNotification")

	procDwmSetWindowAttribute = windows.NewLazySystemDLL("dwmapi.dll").NewProc("DwmSetWindowAttribute")
)

// acquireSingleInstance returns false if the application is already running.
func acquireSingleInstance() bool {
	name, _ := windows.UTF16PtrFromString(`Local\` + appName)
	// The handle is deliberately kept open for the life of the process.
	_, err := windows.CreateMutex(nil, false, name)
	return !errors.Is(err, windows.ERROR_ALREADY_EXISTS)
}

// listenHotkey registers the first available shortcut and calls onPress for
// each press. It runs on its own thread because WM_HOTKEY is delivered to the
// queue of the registering thread. It returns the shortcut that was registered,
// or "" if none could be.
func listenHotkey(candidates []string, onPress func()) string {
	active := make(chan string, 1)
	go func() {
		runtime.LockOSThread()
		registered := ""
		for _, name := range candidates {
			h, err := hotkey.Parse(name)
			if err != nil {
				continue
			}
			ok, _, _ := procRegisterHotKey.Call(0, 1, uintptr(h.Modifiers|modNoRepeat), uintptr(h.Key))
			if ok != 0 {
				registered = name
				break
			}
		}
		active <- registered
		if registered == "" {
			return
		}
		var msg win.MSG
		for {
			r, _, _ := procGetMessage.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
			if int32(r) <= 0 {
				return
			}
			if msg.Message == win.WM_HOTKEY {
				onPress()
			}
		}
	}()
	return <-active
}

// setClipboard stores text, marked so that it stays out of Windows clipboard
// history (Win+V), cloud clipboard and clipboard monitors. The marks go first:
// if one fails, the text is never placed on the clipboard.
func setClipboard(hwnd win.HWND, text string) error {
	utf16, err := windows.UTF16FromString(text)
	if err != nil {
		return err
	}
	if err := openClipboard(hwnd); err != nil {
		return err
	}
	defer win.CloseClipboard()

	if !win.EmptyClipboard() {
		return errors.New("EmptyClipboard failed")
	}
	for _, format := range []string{
		"ExcludeClipboardContentFromMonitorProcessing",
		"CanIncludeInClipboardHistory",
		"CanUploadToCloudClipboard",
	} {
		name, _ := windows.UTF16PtrFromString(format)
		id, _, _ := procRegisterClipboardFmt.Call(uintptr(unsafe.Pointer(name)))
		if id == 0 {
			win.EmptyClipboard()
			return fmt.Errorf("register clipboard format %s", format)
		}
		if err := putClipboardData(uint32(id), make([]byte, 4)); err != nil {
			win.EmptyClipboard()
			return err
		}
	}
	data := unsafe.Slice((*byte)(unsafe.Pointer(&utf16[0])), len(utf16)*2)
	if err := putClipboardData(win.CF_UNICODETEXT, data); err != nil {
		win.EmptyClipboard()
		return err
	}
	return nil
}

// clearClipboardIf empties the clipboard if it still holds text, so that
// anything copied by the user afterwards is left alone.
func clearClipboardIf(hwnd win.HWND, text string) {
	if openClipboard(hwnd) != nil {
		return
	}
	defer win.CloseClipboard()
	if clipboardText() == text {
		win.EmptyClipboard()
	}
}

// clipboardText returns the clipboard's text. The clipboard must be open.
func clipboardText() string {
	mem := win.HGLOBAL(win.GetClipboardData(win.CF_UNICODETEXT))
	if mem == 0 {
		return ""
	}
	p := win.GlobalLock(mem)
	if p == nil {
		return ""
	}
	defer win.GlobalUnlock(mem)
	return windows.UTF16PtrToString((*uint16)(p))
}

func putClipboardData(format uint32, data []byte) error {
	mem := win.GlobalAlloc(win.GMEM_MOVEABLE, uintptr(len(data)))
	if mem == 0 {
		return errors.New("GlobalAlloc failed")
	}
	p := win.GlobalLock(mem)
	if p == nil {
		win.GlobalFree(mem)
		return errors.New("GlobalLock failed")
	}
	copy(unsafe.Slice((*byte)(p), len(data)), data)
	win.GlobalUnlock(mem)
	if win.SetClipboardData(format, win.HANDLE(mem)) == 0 {
		win.GlobalFree(mem)
		return errors.New("SetClipboardData failed")
	}
	return nil
}

// openClipboard retries because other applications hold the clipboard briefly.
func openClipboard(hwnd win.HWND) error {
	for range 10 {
		if win.OpenClipboard(hwnd) {
			return nil
		}
		time.Sleep(20 * time.Millisecond)
	}
	return errors.New("clipboard is in use by another application")
}

// enableSingleClick makes the lists inside a TableView activate items on one
// click, which also shows a hand cursor over them.
func enableSingleClick(table win.HWND) {
	for _, list := range listViews(table) {
		win.SendMessage(list, win.LVM_SETEXTENDEDLISTVIEWSTYLE,
			win.LVS_EX_ONECLICKACTIVATE, win.LVS_EX_ONECLICKACTIVATE)
	}
}

// clickedColumn returns the column under the cursor, or -1. walk's activation
// event only tells the row.
func clickedColumn(table win.HWND) int {
	var cursor win.POINT
	win.GetCursorPos(&cursor)
	for _, list := range listViews(table) {
		hit := win.LVHITTESTINFO{Pt: cursor}
		win.ScreenToClient(list, &hit.Pt)
		win.SendMessage(list, win.LVM_SUBITEMHITTEST, 0, uintptr(unsafe.Pointer(&hit)))
		if hit.IItem >= 0 {
			return int(hit.ISubItem)
		}
	}
	return -1
}

// monitorUnderCursor returns the bounds of the monitor the cursor is on.
func monitorUnderCursor() (monitor, work placement.Rect, ok bool) {
	var cursor win.POINT
	win.GetCursorPos(&cursor)
	handle, _, _ := procMonitorFromPoint.Call(
		uintptr(uint32(cursor.X))|uintptr(uint32(cursor.Y))<<32, win.MONITOR_DEFAULTTONEAREST)
	var info win.MONITORINFO
	info.CbSize = uint32(unsafe.Sizeof(info))
	if !win.GetMonitorInfo(win.HMONITOR(handle), &info) {
		return monitor, work, false
	}
	return placement.Rect(info.RcMonitor), placement.Rect(info.RcWork), true
}

// placeOnTop centers a dialog on the monitor under the cursor and brings it
// forward. Topmost keeps a password prompt visible at logon, when Windows may
// refuse to give this process the foreground.
func placeOnTop(hwnd win.HWND) {
	var rc win.RECT
	if _, work, ok := monitorUnderCursor(); ok && win.GetWindowRect(hwnd, &rc) {
		x := work.Left + (work.Right-work.Left-(rc.Right-rc.Left))/2
		y := work.Top + (work.Bottom-work.Top-(rc.Bottom-rc.Top))/2
		win.SetWindowPos(hwnd, win.HWND_TOPMOST, x, y, 0, 0, win.SWP_NOSIZE)
	}
	win.SetForegroundWindow(hwnd)
}

// placeAboveTray shows the popup in the corner next to the system tray.
func placeAboveTray(hwnd win.HWND, width, height, margin int32) {
	if monitor, work, ok := monitorUnderCursor(); ok {
		x, y := placement.Popup(monitor, work, width, height, margin)
		win.SetWindowPos(hwnd, win.HWND_TOPMOST, x, y, width, height, 0)
	}
	win.SetForegroundWindow(hwnd)
}

// makePopup turns a normal window into a borderless one with rounded corners
// and no taskbar button.
func makePopup(hwnd win.HWND) {
	style := uint32(win.GetWindowLong(hwnd, win.GWL_STYLE))
	style &^= win.WS_CAPTION | win.WS_THICKFRAME | win.WS_SYSMENU | win.WS_MINIMIZEBOX | win.WS_MAXIMIZEBOX
	win.SetWindowLong(hwnd, win.GWL_STYLE, int32(style|win.WS_POPUP))
	exStyle := uint32(win.GetWindowLong(hwnd, win.GWL_EXSTYLE))
	win.SetWindowLong(hwnd, win.GWL_EXSTYLE, int32(exStyle&^win.WS_EX_APPWINDOW|win.WS_EX_TOOLWINDOW))

	corners := uint32(dwmCornerRound)
	procDwmSetWindowAttribute.Call(uintptr(hwnd), dwmWindowCornerPreference,
		uintptr(unsafe.Pointer(&corners)), unsafe.Sizeof(corners))

	win.SetWindowPos(hwnd, 0, 0, 0, 0, 0,
		win.SWP_NOMOVE|win.SWP_NOSIZE|win.SWP_NOZORDER|win.SWP_FRAMECHANGED)
}

// watchSession calls onLock when the session is locked or the system is about
// to sleep. Both arrive as window messages, which walk does not expose, so the
// window procedure is wrapped. onLock runs inside that procedure.
func watchSession(hwnd win.HWND, onLock func()) error {
	if ok, _, err := procWTSRegister.Call(uintptr(hwnd), notifyForThisSession); ok == 0 {
		return fmt.Errorf("WTSRegisterSessionNotification: %w", err)
	}
	original := win.GetWindowLongPtr(hwnd, win.GWLP_WNDPROC)
	proc := windows.NewCallback(func(hwnd win.HWND, msg uint32, wParam, lParam uintptr) uintptr {
		if msg == wmWTSSessionChange && wParam == wtsSessionLock ||
			msg == wmPowerBroadcast && wParam == pbtAPMSuspend {
			onLock()
		}
		return win.CallWindowProc(original, hwnd, msg, wParam, lParam)
	})
	win.SetWindowLongPtr(hwnd, win.GWLP_WNDPROC, proc)
	return nil
}

// idleTime returns how long ago the user last touched the keyboard or mouse.
func idleTime() time.Duration {
	info := struct {
		size uint32
		time uint32
	}{size: 8}
	if ok, _, _ := procGetLastInputInfo.Call(uintptr(unsafe.Pointer(&info))); ok == 0 {
		return 0
	}
	now, _, _ := procGetTickCount.Call()
	return time.Duration(uint32(now)-info.time) * time.Millisecond
}

func autostartEnabled() bool {
	key, err := registry.OpenKey(registry.CURRENT_USER, runKeyPath, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer key.Close()
	_, _, err = key.GetStringValue(appName)
	return err == nil
}

func setAutostart(enabled bool) error {
	key, err := registry.OpenKey(registry.CURRENT_USER, runKeyPath, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer key.Close()
	if !enabled {
		return key.DeleteValue(appName)
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	return key.SetStringValue(appName, `"`+exe+`"`)
}
