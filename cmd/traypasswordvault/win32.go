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

	wmHotkeyRequest = win.WM_APP + 1

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
	procUnregisterHotKey     = user32.NewProc("UnregisterHotKey")
	procPostThreadMessage    = user32.NewProc("PostThreadMessageW")
	procRegisterHotKey       = user32.NewProc("RegisterHotKey")
	procGetMessage           = user32.NewProc("GetMessageW")
	procRegisterClipboardFmt = user32.NewProc("RegisterClipboardFormatW")
	procMonitorFromPoint     = user32.NewProc("MonitorFromPoint")

	procWTSRegister = windows.NewLazySystemDLL("wtsapi32.dll").NewProc("WTSRegisterSessionNotification")

	procDwmSetWindowAttribute = windows.NewLazySystemDLL("dwmapi.dll").NewProc("DwmSetWindowAttribute")
)

// acquireSingleInstance returns false if the application is already running.
func acquireSingleInstance() bool {
	name, _ := windows.UTF16PtrFromString(`Local\` + appName)
	// The handle is deliberately kept open for the life of the process.
	_, err := windows.CreateMutex(nil, false, name)
	return !errors.Is(err, windows.ERROR_ALREADY_EXISTS)
}

// hotkeyListener owns the global shortcut. RegisterHotKey ties a shortcut to the
// calling thread, whose queue then receives WM_HOTKEY, so registering and
// listening both happen on one dedicated thread.
type hotkeyListener struct {
	thread   uint32
	requests chan hotkeyRequest
}

type hotkeyRequest struct {
	key   hotkey.Hotkey
	reply chan bool
}

// newHotkeyListener starts the thread. onPress is called on that thread.
func newHotkeyListener(onPress func()) *hotkeyListener {
	l := &hotkeyListener{requests: make(chan hotkeyRequest, 1)}
	ready := make(chan struct{})
	go l.run(onPress, ready)
	<-ready
	return l
}

// set makes name the shortcut. If it cannot be registered, the previous
// shortcut stays active.
func (l *hotkeyListener) set(name string) error {
	key, err := hotkey.Parse(name)
	if err != nil {
		return err
	}
	reply := make(chan bool, 1)
	l.requests <- hotkeyRequest{key, reply}
	procPostThreadMessage.Call(uintptr(l.thread), wmHotkeyRequest, 0, 0)
	if !<-reply {
		return fmt.Errorf("%s is already in use.", name)
	}
	return nil
}

func (l *hotkeyListener) run(onPress func(), ready chan<- struct{}) {
	runtime.LockOSThread()
	l.thread = windows.GetCurrentThreadId()
	var msg win.MSG
	// A thread has no message queue, and cannot be posted to, until it asks.
	win.PeekMessage(&msg, 0, 0, 0, win.PM_NOREMOVE)
	close(ready)

	var current hotkey.Hotkey
	var id uintptr // 0 until a shortcut is registered, then alternates 1 and 2
	for {
		r, _, _ := procGetMessage.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
		if int32(r) <= 0 {
			return
		}
		switch msg.Message {
		case win.WM_HOTKEY:
			onPress()
		case wmHotkeyRequest:
			request := <-l.requests
			if request.key == current {
				request.reply <- true
				continue
			}
			// The new one first: if it is taken, the old one must survive.
			next := 3 - id
			if id == 0 {
				next = 1
			}
			ok, _, _ := procRegisterHotKey.Call(0, next,
				uintptr(request.key.Modifiers|modNoRepeat), uintptr(request.key.Key))
			if ok != 0 {
				if id != 0 {
					procUnregisterHotKey.Call(0, id)
				}
				id, current = next, request.key
			}
			request.reply <- ok != 0
		}
	}
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

// afterMessages calls onSize each time the window has been resized, and
// onPaint each time it has painted itself.
func afterMessages(hwnd win.HWND, onSize, onPaint func(win.HWND)) {
	original := win.GetWindowLongPtr(hwnd, win.GWLP_WNDPROC)
	proc := windows.NewCallback(func(hwnd win.HWND, msg uint32, wParam, lParam uintptr) uintptr {
		result := win.CallWindowProc(original, hwnd, msg, wParam, lParam)
		switch msg {
		case win.WM_SIZE:
			onSize(hwnd)
		case win.WM_PAINT:
			onPaint(hwnd)
		}
		return result
	})
	win.SetWindowLongPtr(hwnd, win.GWLP_WNDPROC, proc)
}

// cursorInRow returns the cursor's distance from the left edge of the list,
// if it is over a row. walk's activation event only tells the row, not where
// in it the click was.
func cursorInRow(lists []win.HWND) (x int, ok bool) {
	var cursor win.POINT
	win.GetCursorPos(&cursor)
	for _, list := range lists {
		hit := win.LVHITTESTINFO{Pt: cursor}
		win.ScreenToClient(list, &hit.Pt)
		win.SendMessage(list, win.LVM_SUBITEMHITTEST, 0, uintptr(unsafe.Pointer(&hit)))
		if hit.IItem >= 0 {
			return int(hit.Pt.X), true
		}
	}
	return 0, false
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
