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

	dwmWindowCornerPreference = 33
	dwmCornerRound            = 2
)

var (
	user32                    = windows.NewLazySystemDLL("user32.dll")
	procRegisterHotKey        = user32.NewProc("RegisterHotKey")
	procGetMessage            = user32.NewProc("GetMessageW")
	procRegisterClipboardFmt  = user32.NewProc("RegisterClipboardFormatW")
	procGetClipboardSeqNumber = user32.NewProc("GetClipboardSequenceNumber")
	procMonitorFromPoint      = user32.NewProc("MonitorFromPoint")

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
// history (Win+V), cloud clipboard and clipboard monitors. It returns the
// clipboard sequence number identifying this content.
func setClipboard(hwnd win.HWND, text string) (uint32, error) {
	utf16, err := windows.UTF16FromString(text)
	if err != nil {
		return 0, err
	}
	if err := openClipboard(hwnd); err != nil {
		return 0, err
	}
	defer win.CloseClipboard()

	if !win.EmptyClipboard() {
		return 0, errors.New("EmptyClipboard failed")
	}
	mem, err := globalCopy(unsafe.Slice((*byte)(unsafe.Pointer(&utf16[0])), len(utf16)*2))
	if err != nil {
		return 0, err
	}
	if win.SetClipboardData(win.CF_UNICODETEXT, win.HANDLE(mem)) == 0 {
		win.GlobalFree(mem)
		return 0, errors.New("SetClipboardData failed")
	}

	zero := make([]byte, 4)
	for _, format := range []string{
		"ExcludeClipboardContentFromMonitorProcessing",
		"CanIncludeInClipboardHistory",
		"CanUploadToCloudClipboard",
	} {
		name, _ := windows.UTF16PtrFromString(format)
		id, _, _ := procRegisterClipboardFmt.Call(uintptr(unsafe.Pointer(name)))
		mem, err := globalCopy(zero)
		if id == 0 || err != nil {
			return 0, fmt.Errorf("clipboard format %s: %w", format, err)
		}
		if win.SetClipboardData(uint32(id), win.HANDLE(mem)) == 0 {
			win.GlobalFree(mem)
		}
	}
	return clipboardSequence(), nil
}

// clearClipboard empties the clipboard if it still holds the content identified
// by seq, so that anything copied by the user afterwards is left alone.
func clearClipboard(hwnd win.HWND, seq uint32) {
	if clipboardSequence() != seq || openClipboard(hwnd) != nil {
		return
	}
	defer win.CloseClipboard()
	win.EmptyClipboard()
}

func clipboardSequence() uint32 {
	r, _, _ := procGetClipboardSeqNumber.Call()
	return uint32(r)
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

func globalCopy(data []byte) (win.HGLOBAL, error) {
	mem := win.GlobalAlloc(win.GMEM_MOVEABLE, uintptr(len(data)))
	if mem == 0 {
		return 0, errors.New("GlobalAlloc failed")
	}
	p := win.GlobalLock(mem)
	if p == nil {
		win.GlobalFree(mem)
		return 0, errors.New("GlobalLock failed")
	}
	copy(unsafe.Slice((*byte)(p), len(data)), data)
	win.GlobalUnlock(mem)
	return mem, nil
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
