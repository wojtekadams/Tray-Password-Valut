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

	"github.com/wojtekadams/Tray-Password-Valut/internal/theme"
	"github.com/wojtekadams/Tray-Password-Valut/internal/vault"
)

var (
	procWindowFromPoint = windows.NewLazySystemDLL("user32.dll").NewProc("WindowFromPoint")

	coveredControls []string // results of checkPopup
	wrongColors     []string
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

func TestPopupIsPaintedWithThePalette(t *testing.T) {
	if popupError != nil {
		t.Fatal(popupError)
	}
	for _, problem := range wrongColors {
		t.Error(problem)
	}
}

func checkPopup() error {
	app, err := walk.InitApp()
	if err != nil {
		return err
	}
	initDarkMode()
	a := &application{modal: 1} // keeps the popup open if it does not get focus
	if err := a.createWindow(); err != nil {
		return err
	}
	a.model.rows = []vault.Entry{{Title: "Bank", Username: "bob", Password: "secret-password"}}
	a.model.PublishRowsReset()
	a.table.SetCurrentIndex(0)
	a.mw.Show()
	a.placePopup()

	// Layout and painting run in the message loop, so inspect from inside it.
	go func() {
		time.Sleep(2 * time.Second)
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
		})
		for _, p := range []theme.Palette{theme.New(true, theme.DefaultAccent), theme.New(false, theme.DefaultAccent)} {
			a.mw.Synchronize(func() { a.applyPalette(p) })
			time.Sleep(time.Second)
			a.mw.Synchronize(func() { checkColors(a, p) })
		}
		a.mw.Synchronize(func() {
			a.exiting = true
			app.Exit(0)
		})
	}()
	app.Run()
	return nil
}

// checkColors reads real pixels off the screen, in places free of text.
func checkColors(a *application, p theme.Palette) {
	screen := win.GetDC(0)
	defer win.ReleaseDC(0, screen)
	var table, window win.RECT
	win.GetWindowRect(a.table.Handle(), &table)
	win.GetWindowRect(a.mw.Handle(), &window)
	for name, check := range map[string]struct {
		x, y int32
		want theme.Color
	}{
		"selected row": {table.Right - 6, table.Top + 6, p.Selection},
		"empty list":   {table.Right - 6, table.Bottom - 6, p.Background},
		"window":       {window.Left + 3, (window.Top + window.Bottom) / 2, p.Background},
	} {
		got := win.GetPixel(screen, check.x, check.y)
		if want := win.COLORREF(walkColor(check.want)); got != want {
			wrongColors = append(wrongColors, fmt.Sprintf("dark=%v %s: pixel %#06x, want %#06x", p.Dark, name, got, want))
		}
	}
}

// windowAtCenter returns the window a click on the middle of hwnd would reach.
func windowAtCenter(hwnd win.HWND) win.HWND {
	var rc win.RECT
	win.GetWindowRect(hwnd, &rc)
	x, y := (rc.Left+rc.Right)/2, (rc.Top+rc.Bottom)/2
	found, _, _ := procWindowFromPoint.Call(uintptr(uint32(x)) | uintptr(uint32(y))<<32)
	return win.HWND(found)
}
