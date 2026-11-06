//go:build windows

package main

import (
	"runtime"
	"testing"
	"unsafe"

	"github.com/tailscale/win"
	"golang.org/x/sys/windows"
)

// testWindow returns a window to own the clipboard: SetClipboardData fails if
// the clipboard was opened without one. Both are tied to the calling thread.
func testWindow(t *testing.T) win.HWND {
	t.Helper()
	runtime.LockOSThread()
	t.Cleanup(runtime.UnlockOSThread)
	class, _ := windows.UTF16PtrFromString("STATIC")
	hwnd := win.CreateWindowEx(0, class, nil, 0, 0, 0, 0, 0, 0, 0, 0, nil)
	if hwnd == 0 {
		t.Fatal("CreateWindowEx failed")
	}
	t.Cleanup(func() { win.DestroyWindow(hwnd) })
	return hwnd
}

func readClipboard(t *testing.T, hwnd win.HWND) string {
	t.Helper()
	if err := openClipboard(hwnd); err != nil {
		t.Fatal(err)
	}
	defer win.CloseClipboard()
	return clipboardText()
}

func TestSetClipboardKeepsPasswordOutOfHistory(t *testing.T) {
	hwnd := testWindow(t)
	if err := setClipboard(hwnd, "sekret-ąć"); err != nil {
		t.Fatal(err)
	}
	defer clearClipboardIf(hwnd, "sekret-ąć")
	if got := readClipboard(t, hwnd); got != "sekret-ąć" {
		t.Fatalf("clipboard = %q", got)
	}
	for _, format := range []string{
		"ExcludeClipboardContentFromMonitorProcessing",
		"CanIncludeInClipboardHistory",
		"CanUploadToCloudClipboard",
	} {
		name, _ := windows.UTF16PtrFromString(format)
		id, _, _ := procRegisterClipboardFmt.Call(uintptr(unsafe.Pointer(name)))
		if !win.IsClipboardFormatAvailable(uint32(id)) {
			t.Errorf("%s missing", format)
		}
	}
}

func TestClearClipboardIf(t *testing.T) {
	hwnd := testWindow(t)
	if err := setClipboard(hwnd, "password"); err != nil {
		t.Fatal(err)
	}
	clearClipboardIf(hwnd, "something else") // the user copied over it
	if got := readClipboard(t, hwnd); got != "password" {
		t.Fatalf("foreign content was cleared, clipboard = %q", got)
	}
	clearClipboardIf(hwnd, "password")
	if got := readClipboard(t, hwnd); got != "" {
		t.Fatalf("clipboard = %q", got)
	}
}

// The test shortcuts use keys that are absent from real keyboards.
func TestHotkeyListener(t *testing.T) {
	first := newHotkeyListener(func() {})
	second := newHotkeyListener(func() {})
	const a, b = "Ctrl+Alt+Shift+F23", "Ctrl+Alt+Shift+F24"

	if err := first.set(a); err != nil {
		t.Fatal(err)
	}
	if err := first.set(a); err != nil {
		t.Errorf("setting the active shortcut again: %v", err)
	}
	if err := second.set(a); err == nil {
		t.Error("a shortcut in use was accepted")
	}
	if err := first.set("Ctrl+Nonsense"); err == nil {
		t.Error("an invalid shortcut was accepted")
	}
	// A failed attempt must not cost the current shortcut.
	if err := second.set(a); err == nil {
		t.Error("the current shortcut was lost after a failed change")
	}
	if err := first.set(b); err != nil {
		t.Fatal(err)
	}
	if err := second.set(a); err != nil {
		t.Errorf("the previous shortcut was not released: %v", err)
	}
}
