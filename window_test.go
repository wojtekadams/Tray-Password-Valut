//go:build windows

package main

import (
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/tailscale/walk"
	"github.com/tailscale/win"
	"golang.org/x/sys/windows"
)

var (
	procWindowFromPoint = windows.NewLazySystemDLL("user32.dll").NewProc("WindowFromPoint")

	coveredControls []string // result of checkPopup
	popupError      error
)

// TestMain opens the popup first because walk only runs on the main goroutine,
// which is the one running TestMain and not the tests themselves.
func TestMain(m *testing.M) {
	popupError = checkPopup()
	os.Exit(m.Run())
}

// Regression test: walk's empty toolbar used to lie over the search row.
func TestPopupControlsAreNotCovered(t *testing.T) {
	if popupError != nil {
		t.Fatal(popupError)
	}
	for _, problem := range coveredControls {
		t.Error(problem)
	}
}

func checkPopup() error {
	app, err := walk.InitApp()
	if err != nil {
		return err
	}
	a := &application{modal: 1} // keeps the popup open if it does not get focus
	if err := a.createWindow(); err != nil {
		return err
	}
	a.mw.Show()
	scale := func(v int) int32 { return int32(a.mw.IntFrom96DPI(v)) }
	placeAboveTray(a.mw.Handle(), scale(popupWidth), scale(popupHeight), scale(popupMargin))

	// Layout runs in the message loop, so inspect the window from inside it.
	time.AfterFunc(2*time.Second, func() {
		a.mw.Synchronize(func() {
			for name, control := range map[string]win.HWND{
				"search": a.search.Handle(),
				"add":    a.buttons[len(a.buttons)-1].Handle(),
				"login":  a.copyLogin.Handle(),
			} {
				if got := windowAtCenter(control); got != control {
					coveredControls = append(coveredControls, fmt.Sprintf("%s is covered by window %#x", name, got))
				}
			}
			a.exiting = true
			app.Exit(0)
		})
	})
	app.Run()
	return nil
}

// windowAtCenter returns the window a click on the middle of hwnd would reach.
func windowAtCenter(hwnd win.HWND) win.HWND {
	var rc win.RECT
	win.GetWindowRect(hwnd, &rc)
	x, y := (rc.Left+rc.Right)/2, (rc.Top+rc.Bottom)/2
	found, _, _ := procWindowFromPoint.Call(uintptr(uint32(x)) | uintptr(uint32(y))<<32)
	return win.HWND(found)
}
