//go:build windows

package main

import (
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/tailscale/walk"
	"github.com/tailscale/win"

	"github.com/wojtekadams/Tray-Password-Valut/internal/theme"
	"github.com/wojtekadams/Tray-Password-Valut/internal/vault"
)

var (
	procWindowFromPoint = user32.NewProc("WindowFromPoint")
	procSetCursorPos    = user32.NewProc("SetCursorPos")
	procMouseEvent      = user32.NewProc("mouse_event")

	coveredControls []string // results of checkPopup
	wrongColors     []string
	clickProblems   []string
	popupError      error
)

const mouseLeftDown, mouseLeftUp = 0x0002, 0x0004

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

func TestClickingACellCopiesIt(t *testing.T) {
	if popupError != nil {
		t.Fatal(popupError)
	}
	for _, problem := range clickProblems {
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
				"add":    a.addButton.Handle(),
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
		clickChecks(a)
		a.mw.Synchronize(func() {
			a.exiting = true
			app.Exit(0)
		})
	}()
	app.Run()
	return nil
}

// clickChecks clicks the real mouse on the login and password cells.
func clickChecks(a *application) {
	for _, step := range []struct {
		name        string
		column      int // left edge of a spot inside the cell, at 100% scaling
		wantClip    string
		wantVisible bool
	}{
		{"login", 170, "bob", true},
		{"password", 270, "secret-password", false},
	} {
		a.mw.Synchronize(func() {
			var rc win.RECT
			win.GetWindowRect(a.table.Handle(), &rc)
			procSetCursorPos.Call(uintptr(rc.Left)+uintptr(a.table.IntFrom96DPI(step.column)),
				uintptr(rc.Top)+uintptr(a.table.IntFrom96DPI(10)))
		})
		time.Sleep(300 * time.Millisecond)
		procMouseEvent.Call(mouseLeftDown, 0, 0, 0, 0)
		procMouseEvent.Call(mouseLeftUp, 0, 0, 0, 0)
		time.Sleep(700 * time.Millisecond)
		a.mw.Synchronize(func() {
			if err := openClipboard(a.mw.Handle()); err != nil {
				clickProblems = append(clickProblems, err.Error())
				return
			}
			defer win.CloseClipboard()
			if got := clipboardText(); got != step.wantClip {
				clickProblems = append(clickProblems, fmt.Sprintf("%s click: clipboard %q, want %q", step.name, got, step.wantClip))
			}
			if got := a.mw.Visible(); got != step.wantVisible {
				clickProblems = append(clickProblems, fmt.Sprintf("%s click: popup visible = %v", step.name, got))
			}
		})
	}
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
