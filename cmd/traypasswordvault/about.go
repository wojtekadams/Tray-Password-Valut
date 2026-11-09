//go:build windows

package main

import (
	"fmt"
	"runtime/debug"
	"strings"

	"github.com/tailscale/walk"
	. "github.com/tailscale/walk/declarative"
	"github.com/tailscale/win"
	"golang.org/x/sys/windows"
)

const website = "https://wojtekadams.github.io/Tray-Password-Valut/"

// version is set by release builds: -ldflags "-X main.version=v1.2.3".
var version string

// versionText describes the version of this build. Without one set by the
// release build it falls back to what Go recorded from git, if anything.
func versionText() string {
	v := version
	if info, ok := debug.ReadBuildInfo(); v == "" && ok && info.Main.Version != "(devel)" {
		v = info.Main.Version
	}
	if v == "" {
		return "Development build"
	}
	return "Version " + strings.TrimPrefix(v, "v")
}

// about shows the logo, the version and a link to the website.
func (a *application) about() {
	if a.modal > 0 {
		return
	}
	linkFont, err := walk.NewFont(uiFont().Family, uiFont().PointSize, walk.FontUnderline)
	if err != nil {
		a.errorBox(err)
		return
	}
	defer linkFont.Dispose()
	var (
		dlg   *walk.Dialog
		close *walk.PushButton
		logo  *walk.ImageView
		link  *walk.CustomWidget
	)
	err = Dialog{
		AssignTo:      &dlg,
		Title:         "About " + title,
		Font:          uiFont(),
		FixedSize:     true,
		MinSize:       Size{Width: 380},
		DefaultButton: &close,
		CancelButton:  &close,
		Layout:        VBox{Margins: Margins{Left: 20, Top: 20, Right: 20, Bottom: 16}, Spacing: 12},
		Children: []Widget{
			Composite{
				Layout: HBox{MarginsZero: true, Spacing: 16},
				Children: []Widget{
					ImageView{AssignTo: &logo, MinSize: Size{Width: 64, Height: 64}, MaxSize: Size{Width: 64, Height: 64}, Mode: ImageViewModeShrink},
					Composite{
						Layout: VBox{MarginsZero: true, Spacing: 4},
						Children: []Widget{
							Label{Text: title, Font: Font{Family: uiFont().Family, PointSize: 14, Bold: true}},
							Label{Text: versionText()},
							// Drawn, not a SysLink control, whose link colour
							// is unreadable on the dark background.
							CustomWidget{
								AssignTo:            &link,
								Font:                Font{Family: linkFont.Family(), PointSize: linkFont.PointSize(), Underline: true},
								MinSize:             Size{Width: textWidth(website, linkFont), Height: 20},
								MaxSize:             Size{Height: 20},
								ClearsBackground:    true,
								InvalidatesOnResize: true,
								ToolTipText:         "Open the website",
								PaintPixels: func(canvas *walk.Canvas, _ walk.Rectangle) error {
									bounds := link.ClientBoundsPixels()
									canvas.FillRectanglePixels(a.brushes.background, bounds)
									return canvas.DrawTextPixels(website, link.Font(), walkColor(a.palette.Accent), bounds,
										walk.TextLeft|walk.TextVCenter|walk.TextSingleLine)
								},
								OnMouseUp: func(_, _ int, button walk.MouseButton) {
									if button == walk.LeftButton {
										openURL(website)
									}
								},
							},
						},
					},
				},
			},
			Label{Text: "Usage", Font: Font{Family: uiFont().Family, PointSize: 11, Bold: true}},
			Composite{
				Layout:   Grid{Columns: 2, MarginsZero: true, Spacing: 6},
				Children: a.usageRows(),
			},
			Composite{
				Layout: HBox{MarginsZero: true},
				Children: []Widget{
					HSpacer{},
					PushButton{AssignTo: &close, Text: "Close", OnClicked: func() { dlg.Accept() }},
				},
			},
		},
	}.Create(a.mw)
	if err != nil {
		a.errorBox(err)
		return
	}
	if icon, err := walk.NewIconFromResourceWithSize("APP", walk.Size{Width: 64, Height: 64}); err == nil {
		logo.SetImage(icon)
		dlg.SetIcon(icon)
	}
	link.SetCursor(walk.CursorHand())
	dlg.SetBackground(a.brushes.background)
	setDarkTitle(dlg.Handle(), a.palette.Dark)
	a.themeChildren(dlg)
	dlg.Synchronize(func() {
		placeOnTop(dlg.Handle())
		close.SetFocus()
	})
	a.modal++
	defer func() { a.modal-- }()
	dlg.Run()
}

// usageRows lists the shortcuts, input in bold beside its action.
func (a *application) usageRows() []Widget {
	hotkey := a.activeHotkey
	if hotkey == "" {
		hotkey = "No shortcut set"
	}
	bold := Font{Family: uiFont().Family, PointSize: uiFont().PointSize, Bold: true}
	var rows []Widget
	for _, row := range [][2]string{
		{hotkey, "Open or hide the popup"},
		{"Typing", "Search by name or login"},
		{"Enter, click the password", fmt.Sprintf("Copy the password (cleared after %d s)", int(clipboardClear.Seconds()))},
		{"Ctrl+U, click the login", "Copy the login"},
		{"Ctrl+N, +", "Add an entry"},
		{"Ctrl+E", "Edit the selected entry"},
		{"Esc", "Hide"},
	} {
		rows = append(rows,
			Composite{ // its right margin separates the columns
				Layout:   HBox{Margins: Margins{Right: 16}},
				Children: []Widget{Label{Text: row[0], Font: bold}},
			},
			Label{Text: row[1]})
	}
	return rows
}

// textWidth returns how wide text is in font at 100% scaling, plus a little
// room, or 0 if it cannot be measured.
func textWidth(text string, font *walk.Font) int {
	bmp, err := walk.NewBitmapForDPI(walk.Size{Width: 1, Height: 1}, 96)
	if err != nil {
		return 0
	}
	defer bmp.Dispose()
	canvas, err := walk.NewCanvasFromImage(bmp)
	if err != nil {
		return 0
	}
	defer canvas.Dispose()
	bounds, _, err := canvas.MeasureTextPixels(text, font, walk.Rectangle{Width: 10000, Height: 100},
		walk.TextLeft|walk.TextSingleLine|walk.TextCalcRect)
	if err != nil {
		return 0
	}
	return bounds.Width + 4
}

// openURL opens a web page in the default browser.
func openURL(url string) {
	verb, _ := windows.UTF16PtrFromString("open")
	file, _ := windows.UTF16PtrFromString(url)
	win.ShellExecute(0, verb, file, nil, nil, win.SW_SHOWNORMAL)
}
