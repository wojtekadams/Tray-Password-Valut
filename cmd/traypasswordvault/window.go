//go:build windows

package main

import (
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/tailscale/walk"
	. "github.com/tailscale/walk/declarative"
	"github.com/tailscale/win"

	"github.com/wojtekadams/Tray-Password-Valut/internal/theme"
	"github.com/wojtekadams/Tray-Password-Valut/internal/vault"
)

const (
	title          = "Tray Password Vault"
	clipboardClear = 30 * time.Second

	copiedNotice = "Copied ✓"

	// Popup size in pixels at 100% scaling: room for the search box and five rows.
	popupWidth, popupHeight, popupMargin = 380, 213, 12

	// Sizes at 100% scaling, matching the mock-up on the project's web page.
	padding    = 10 // inside the popup
	searchEdit = 20 // height of the text field inside the search box
	searchLine = 2  // underline of the search box
	// The search box: top border, padding, text, padding, underline.
	searchHeight = 1 + 7 + searchEdit + 5 + searchLine
	buttonWidth  = 34
	rowHeight    = 30
	columnGap    = 8

	columnSeparator = 2 // pixels the list theme draws at the end of a column
	cornerSize      = 8 // diameter of rounded corners

	loginColumn, passwordColumn = 1, 2
)

// defaultHotkeys are tried in order. Windows 11 usually keeps Win+W for Widgets.
var defaultHotkeys = []string{"Win+W", "Win+Shift+W", "Ctrl+Alt+W"}

type application struct {
	cfg   config
	vault *vault.Vault // nil while locked

	mw           *walk.MainWindow
	search       *walk.LineEdit
	searchFrame  *walk.Composite // the search box, showing as its underline
	searchBorder *walk.Composite // its outline
	searchInner  *walk.Composite // its padding around the text field
	table        *walk.TableView
	addButton    *walk.CustomWidget
	lists        []win.HWND     // the native lists inside table
	rowImages    win.HIMAGELIST // sets the row height
	rowPixels    int

	hotkeys      *hotkeyListener
	activeHotkey string

	palette theme.Palette
	brushes brushes
	model   *entryModel

	tray       *walk.NotifyIcon
	lockAction *walk.Action
	autostart  *walk.Action

	modal        int // open modal dialogs
	hiddenAt     time.Time
	exiting      bool
	clipSecret   string      // password on the clipboard, "" if none
	clipGen      int         // identifies the latest copy, so older timers do nothing
	dialog       *form       // open dialog, if any
	copied       vault.Entry // entry and column showing copiedNotice, if showCopied
	copiedColumn int
	showCopied   bool
	copiedGen    int
}

type entryModel struct {
	walk.TableModelBase
	rows []vault.Entry
}

func (m *entryModel) RowCount() int { return len(m.rows) }

func (m *entryModel) Value(row, col int) any {
	switch col {
	case 0:
		return m.rows[row].Title
	case 1:
		return m.rows[row].Username
	default:
		return vault.Mask(m.rows[row].Password)
	}
}

func (a *application) createWindow() error {
	a.model = &entryModel{}
	err := MainWindow{
		AssignTo: &a.mw,
		Visible:  false,
		Title:    title,
		Font:     uiFont(),
		Size:     Size{Width: popupWidth, Height: popupHeight},
		Layout:   VBox{Margins: Margins{Left: padding, Top: padding, Right: padding, Bottom: padding}, Spacing: 8},
		Children: []Widget{
			Composite{
				MinSize: Size{Height: searchHeight},
				MaxSize: Size{Height: searchHeight},
				Layout:  HBox{MarginsZero: true, Spacing: 6},
				Children: []Widget{
					// The search box is drawn from nested coloured composites,
					// as the native frame of a text box cannot be recoloured:
					// the underline shows below the outline, which surrounds
					// the padding around the text field.
					Composite{
						AssignTo: &a.searchFrame,
						// The button is greedy too; without this the two would
						// split the row and the button would leave its half empty.
						StretchFactor: 100,
						Layout:        VBox{Margins: Margins{Bottom: searchLine}},
						Children: []Widget{
							Composite{
								AssignTo: &a.searchBorder,
								Layout:   VBox{Margins: Margins{Left: 1, Top: 1, Right: 1}},
								Children: []Widget{
									Composite{
										AssignTo: &a.searchInner,
										Layout:   HBox{Margins: Margins{Left: 9, Top: 7, Right: 9, Bottom: 5}},
										Children: []Widget{
											LineEdit{
												AssignTo:      &a.search,
												CueBanner:     "Search…",
												MinSize:       Size{Height: searchEdit},
												MaxSize:       Size{Height: searchEdit},
												OnTextChanged: a.searchChanged,
											},
										},
									},
								},
							},
						},
					},
					CustomWidget{
						AssignTo:            &a.addButton,
						MinSize:             Size{Width: buttonWidth},
						MaxSize:             Size{Width: buttonWidth},
						ClearsBackground:    true,
						InvalidatesOnResize: true,
						PaintPixels:         a.paintAddButton,
						ToolTipText:         "Add (Ctrl+N)",
						OnMouseUp: func(_, _ int, button walk.MouseButton) {
							if button == walk.LeftButton {
								a.addEntry()
							}
						},
					},
				},
			},
			TableView{
				AssignTo:      &a.table,
				StretchFactor: 100, // the search row is greedy too, but has a fixed height
				Model:         a.model,
				HeaderHidden:  true,
				StyleCell:     a.styleCell,
				// One column: styleCell draws the name, login and password.
				Columns:         []TableViewColumn{{Title: "Name"}},
				OnItemActivated: a.cellClicked,
			},
		},
	}.Create()
	if err != nil {
		return err
	}
	a.lists = listViews(a.table.Handle())
	enableSingleClick(a.table.Handle())
	a.table.SetScrollbarOrientation(walk.Vertical)
	a.table.SizeChanged().Attach(a.fitColumn)
	removeBorder(a.search.Handle())
	a.search.FocusedChanged().Attach(a.paintSearchLine)
	a.searchFrame.SizeChanged().Attach(func() {
		roundCorners(a.searchFrame.Handle(), int32(a.searchFrame.IntFrom96DPI(cornerSize)))
	})
	// walk always creates a toolbar. Empty, it reserves no space but is still
	// stretched across the top of the window, covering the search box. It has to
	// be removed: SetVisible(false) is a no-op while the window itself is hidden.
	toolBar := a.mw.ToolBar()
	a.mw.SetToolBar(nil)
	toolBar.Dispose()
	makePopup(a.mw.Handle())
	removeBorder(a.table.Handle())
	for _, list := range a.lists {
		removeBorder(list)
	}
	a.updateRowHeight()
	a.applyPalette(systemPalette())

	if icon := appIcon(); icon != nil {
		a.mw.SetIcon(icon)
	}
	a.mw.Closing().Attach(func(canceled *bool, _ walk.CloseReason) {
		if !a.exiting {
			*canceled = true
			a.hide()
		}
	})
	a.mw.Deactivating().Attach(func() {
		if a.modal == 0 {
			a.hide()
		}
	})
	return nil
}

func appIcon() *walk.Icon {
	icon, err := walk.NewIconFromResource("APP")
	if err != nil {
		return nil
	}
	return icon
}

func (a *application) createTray() error {
	var err error
	if a.tray, err = walk.NewNotifyIcon(); err != nil {
		return err
	}
	if icon := appIcon(); icon != nil {
		a.tray.SetIcon(icon)
	}
	a.tray.SetToolTip(title)
	a.tray.MouseDown().Attach(func(_, _ int, button walk.MouseButton) {
		// Pressing the icon first deactivates an open popup and hides it, so
		// without this check the click could never close it.
		if button == walk.LeftButton && time.Since(a.hiddenAt) > 300*time.Millisecond {
			a.show()
		}
	})

	menu := a.tray.ContextMenu().Actions()
	item := func(text string, handler func()) *walk.Action {
		action := walk.NewAction()
		action.SetText(text)
		action.Triggered().Attach(handler)
		menu.Add(action)
		return action
	}
	item("Open", a.show)
	a.lockAction = item("Lock", a.lock)
	a.lockAction.SetEnabled(false)
	item("Change database…", func() {
		if a.modal == 0 && a.chooseDatabase() {
			a.show()
		}
	})
	item("Set shortcut…", a.changeHotkey)
	a.autostart = item("Start with Windows", func() {
		if err := setAutostart(!autostartEnabled()); err != nil {
			a.errorBox(err)
		}
		a.autostart.SetChecked(autostartEnabled())
	})
	a.autostart.SetCheckable(true)
	a.autostart.SetChecked(autostartEnabled())
	menu.Add(walk.NewSeparatorAction())
	item("Exit", a.exit)

	return a.tray.SetVisible(true)
}

func (a *application) startHotkey() {
	a.hotkeys = newHotkeyListener(func() { a.mw.Synchronize(a.toggle) })
	candidates := defaultHotkeys
	if a.cfg.Hotkey != "" {
		candidates = append([]string{a.cfg.Hotkey}, defaultHotkeys...)
	}
	for _, name := range candidates {
		if a.hotkeys.set(name) == nil {
			a.setActiveHotkey(name)
			break
		}
	}
	switch {
	case a.activeHotkey == "":
		a.tray.ShowWarning(title, "No shortcut is available. Choose one with Set shortcut in the tray menu.")
	case a.activeHotkey != candidates[0] && (a.cfg.Hotkey != "" || a.cfg.DatabasePath == ""):
		a.tray.ShowInfo(title, candidates[0]+" is not available. Shortcut: "+a.activeHotkey)
	}
}

func (a *application) setActiveHotkey(name string) {
	a.activeHotkey = name
	a.tray.SetToolTip(title + " (" + name + ")")
}

func (a *application) toggle() {
	if a.mw.Visible() {
		a.hide()
		return
	}
	a.show()
}

// show opens the window, unlocking the database first if needed.
func (a *application) show() {
	if a.modal > 0 {
		return
	}
	a.updateTheme()
	if a.vault == nil && !a.unlock() {
		return
	}
	a.refresh()
	a.mw.Show()
	a.placePopup()
	a.search.SetFocus()
	a.search.SetTextSelection(0, -1) // typing replaces the previous search
	if v := a.vault; v.Changed() {
		go a.reload(v)
	}
}

// placePopup puts the window in its place above the tray and focuses it.
func (a *application) placePopup() {
	scale := func(v int) int32 { return int32(a.mw.IntFrom96DPI(v)) }
	placeAboveTray(a.mw.Handle(), scale(popupWidth), scale(popupHeight), scale(popupMargin))
	a.updateRowHeight() // the popup may have moved to a monitor with other scaling
}

// updateRowHeight makes the list rows as tall as on the web page. A list view
// takes its row height from its image list, so it gets an empty one.
func (a *application) updateRowHeight() {
	height := a.table.IntFrom96DPI(rowHeight)
	if height == a.rowPixels {
		return
	}
	images := win.ImageList_Create(1, int32(height), win.ILC_COLOR32, 0, 0)
	if images == 0 {
		return
	}
	for _, list := range a.lists {
		win.SendMessage(list, win.LVM_SETIMAGELIST, win.LVSIL_SMALL, uintptr(images))
	}
	if a.rowImages != 0 {
		win.ImageList_Destroy(a.rowImages)
	}
	a.rowImages, a.rowPixels = images, height
}

// fitColumn makes the list's only column fill the list. The Windows 11 theme
// draws a separator in the last two pixels of a column, so the column reaches
// that far past the list's edge, out of sight. There is no horizontal scroll
// bar to reveal it.
func (a *application) fitColumn() {
	for _, list := range a.lists {
		// walk's list for frozen columns has no width and stays alone.
		var rc win.RECT
		if win.GetClientRect(list, &rc) && rc.Right > 0 {
			win.SendMessage(list, win.LVM_SETCOLUMNWIDTH, 0, uintptr(rc.Right+columnSeparator))
		}
	}
}

// listWidth returns the width of the visible part of the list, in pixels.
func (a *application) listWidth() int {
	width := 0
	for _, list := range a.lists {
		var rc win.RECT
		if win.GetClientRect(list, &rc) {
			width = max(width, int(rc.Right))
		}
	}
	return width
}

// reload picks up changes made by KeePass or a sync client.
func (a *application) reload(v *vault.Vault) {
	err := v.Reload()
	a.mw.Synchronize(func() {
		if a.vault != v {
			return
		}
		switch {
		case errors.Is(err, vault.ErrWrongPassword): // master password changed elsewhere
			a.lock()
		case err != nil:
			a.tray.ShowWarning(title, "Could not refresh the database: "+err.Error())
		default:
			a.refresh()
		}
	})
}

// hide keeps the search and selection: copying a login, pasting it elsewhere
// hides the popup, and the password of the same entry is then one shortcut away.
func (a *application) hide() {
	a.mw.Hide()
	a.hiddenAt = time.Now()
}

func (a *application) lock() {
	if a.dialog != nil && !a.dialog.busy {
		a.dialog.dlg.Cancel()
	}
	a.vault = nil
	a.lockAction.SetEnabled(false)
	a.model.rows = nil
	a.model.PublishRowsReset()
	a.wipeClipboard()
	a.search.SetText("")
	if a.mw.Visible() {
		a.hide()
	}
}

// startAutoLock locks the vault when the session locks or the system sleeps.
func (a *application) startAutoLock() {
	autoLock := func() {
		a.mw.Synchronize(func() {
			if a.vault != nil {
				a.lock()
			}
		})
	}
	if err := watchSession(a.mw.Handle(), autoLock); err != nil {
		a.tray.ShowWarning(title, "Auto-lock on screen lock is unavailable: "+err.Error())
	}
}

func (a *application) exit() {
	a.wipeClipboard()
	a.exiting = true
	walk.App().Exit(0)
}

func (a *application) setVault(v *vault.Vault) {
	a.vault = v
	a.lockAction.SetEnabled(true)
}

// refresh rebuilds the list from the vault and the search text, keeping the
// selected entry when it is still listed.
func (a *application) refresh() {
	if a.vault == nil {
		return
	}
	var previous vault.Entry
	if e := a.selected(); e != nil {
		previous = *e
	}
	query := strings.ToLower(a.search.Text())
	rows := slices.DeleteFunc(a.vault.Entries(), func(e vault.Entry) bool {
		return !strings.Contains(strings.ToLower(e.Title), query) &&
			!strings.Contains(strings.ToLower(e.Username), query)
	})
	a.model.rows = rows
	a.model.PublishRowsReset()
	a.fitColumn() // the scroll bar may have come or gone
	if len(rows) > 0 {
		a.table.SetCurrentIndex(max(slices.Index(rows, previous), 0))
	}
}

func (a *application) searchChanged() {
	a.refresh()
	if len(a.model.rows) > 0 {
		a.table.SetCurrentIndex(0) // Enter takes the best match, not the old selection
	}
}

func (a *application) selected() *vault.Entry {
	i := a.table.CurrentIndex()
	if i < 0 || i >= len(a.model.rows) {
		return nil
	}
	return &a.model.rows[i]
}

// cellClicked copies the clicked value.
func (a *application) cellClicked() {
	x, ok := cursorInRow(a.lists)
	if !ok {
		return
	}
	px := a.table.IntFrom96DPI
	switch columnAt(x, columnsOf(a.listWidth(), px(padding), px(columnGap))) {
	case loginColumn:
		a.copyUsername()
	case passwordColumn:
		a.copyPassword()
	}
}

func (a *application) copyPassword() {
	e := a.selected()
	if e == nil {
		return
	}
	if err := setClipboard(a.mw.Handle(), e.Password); err != nil {
		a.errorBox(err)
		return
	}
	a.clipSecret = e.Password
	a.clipGen++
	generation := a.clipGen
	time.AfterFunc(clipboardClear, func() {
		a.mw.Synchronize(func() {
			if a.clipGen == generation {
				a.wipeClipboard()
			}
		})
	})
	a.notifyCopied(e, passwordColumn)
}

func (a *application) copyUsername() {
	e := a.selected()
	if e == nil {
		return
	}
	if err := setClipboard(a.mw.Handle(), e.Username); err != nil {
		a.errorBox(err)
		return
	}
	a.notifyCopied(e, loginColumn)
}

// notifyCopied briefly replaces the copied cell's text with copiedNotice. The
// popup stays open so that the other value can be copied next.
func (a *application) notifyCopied(e *vault.Entry, column int) {
	if a.showCopied {
		a.redrawRow(a.copied) // the previous notice, if still shown
	}
	a.copied, a.copiedColumn, a.showCopied = *e, column, true
	a.copiedGen++
	generation := a.copiedGen
	a.redrawRow(a.copied)
	time.AfterFunc(1500*time.Millisecond, func() {
		a.mw.Synchronize(func() {
			if a.copiedGen == generation {
				a.showCopied = false
				a.redrawRow(a.copied)
			}
		})
	})
}

// redrawRow repaints only the row showing e. Invalidating the whole table
// erases its background first, which makes the list flicker.
func (a *application) redrawRow(e vault.Entry) {
	if i := slices.Index(a.model.rows, e); i >= 0 {
		a.model.PublishRowChanged(i)
	}
}

func (a *application) wipeClipboard() {
	if a.clipSecret != "" {
		clearClipboardIf(a.mw.Handle(), a.clipSecret)
		a.clipSecret = ""
	}
}

// OnPreTranslate handles the window's shortcuts. They are taken here because
// the focused control would otherwise consume Enter and the arrow keys.
func (a *application) OnPreTranslate(msg *win.MSG) bool {
	if msg.Message != win.WM_KEYDOWN || a.modal > 0 || !a.mw.Visible() ||
		win.GetAncestor(msg.HWnd, win.GA_ROOT) != a.mw.Handle() {
		return false
	}
	switch key := walk.Key(msg.WParam); {
	case key == walk.KeyEscape:
		a.hide()
	case key == walk.KeyReturn:
		a.copyPassword()
	case (key == walk.KeyDown || key == walk.KeyUp) && msg.HWnd == a.search.Handle():
		delta := 1
		if key == walk.KeyUp {
			delta = -1
		}
		if i := a.table.CurrentIndex() + delta; i >= 0 && i < len(a.model.rows) {
			a.table.SetCurrentIndex(i)
		}
	case key == walk.KeyU && walk.ControlDown():
		a.copyUsername()
	case key == walk.KeyN && walk.ControlDown():
		a.addEntry()
	default:
		return false
	}
	return true
}
