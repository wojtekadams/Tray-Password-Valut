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
	autoLockAfter  = 15 * time.Minute

	copiedNotice = "Copied ✓"

	// Popup size in pixels at 100% scaling.
	popupWidth, popupHeight, popupMargin = 340, 180, 12

	loginColumn, passwordColumn = 1, 2
)

// defaultHotkeys are tried in order. Windows 11 usually keeps Win+W for Widgets.
var defaultHotkeys = []string{"Win+W", "Win+Shift+W", "Ctrl+Alt+W"}

type application struct {
	cfg   config
	vault *vault.Vault // nil while locked

	mw        *walk.MainWindow
	search    *walk.LineEdit
	table     *walk.TableView
	addButton *walk.PushButton

	palette                     theme.Palette
	backgroundBrush, inputBrush *walk.SolidColorBrush
	model                       *entryModel

	tray       *walk.NotifyIcon
	lockAction *walk.Action
	autostart  *walk.Action

	modal      int // open modal dialogs
	hiddenAt   time.Time
	exiting    bool
	clipSecret string      // password on the clipboard, "" if none
	clipGen    int         // identifies the latest copy, so older timers do nothing
	dialog     *form       // open dialog, if any
	copied     vault.Entry // entry whose login shows copiedNotice, if showCopied
	showCopied bool
	copiedGen  int
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
		Layout:   VBox{Margins: Margins{Left: 8, Top: 8, Right: 8, Bottom: 8}, Spacing: 6},
		Children: []Widget{
			Composite{
				Layout: HBox{MarginsZero: true, Spacing: 6},
				Children: []Widget{
					LineEdit{AssignTo: &a.search, CueBanner: "Search…", OnTextChanged: a.searchChanged},
					PushButton{AssignTo: &a.addButton, Text: "+", MaxSize: Size{Width: 32}, ToolTipText: "Add", OnClicked: a.addEntry},
				},
			},
			TableView{
				AssignTo:            &a.table,
				Model:               a.model,
				HeaderHidden:        true,
				StyleCell:           a.styleCell,
				LastColumnStretched: true,
				Columns: []TableViewColumn{
					{Title: "Name", Width: 120},
					{Title: "Login", Width: 100},
					{Title: "Password"},
				},
				OnItemActivated: a.cellClicked,
			},
		},
	}.Create()
	if err != nil {
		return err
	}
	enableSingleClick(a.table.Handle())
	// walk always creates a toolbar. Empty, it reserves no space but is still
	// stretched across the top of the window, covering the search box. It has to
	// be removed: SetVisible(false) is a no-op while the window itself is hidden.
	toolBar := a.mw.ToolBar()
	a.mw.SetToolBar(nil)
	toolBar.Dispose()
	makePopup(a.mw.Handle())
	removeBorder(a.table.Handle())
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
	candidates := defaultHotkeys
	if a.cfg.Hotkey != "" {
		candidates = []string{a.cfg.Hotkey}
	}
	active := listenHotkey(candidates, func() { a.mw.Synchronize(a.toggle) })
	switch {
	case active == "":
		a.tray.ShowWarning(title, "Could not register the shortcut "+strings.Join(candidates, ", ")+
			". Click the tray icon to open the vault.")
	case active != candidates[0]:
		a.tray.SetToolTip(title + " (" + active + ")")
		if a.cfg.DatabasePath == "" {
			a.tray.ShowInfo(title, candidates[0]+" is taken by Windows. Shortcut: "+active)
		}
	default:
		a.tray.SetToolTip(title + " (" + active + ")")
	}
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

// startAutoLock locks the vault when the session locks, the system sleeps or
// the user has been idle for autoLockAfter.
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
	go func() {
		for range time.Tick(30 * time.Second) {
			if idleTime() >= autoLockAfter {
				autoLock()
			}
		}
	}()
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

// cellClicked copies the clicked value. The list sits in single-click mode, so
// this runs once the mouse button is released: a popup hidden any earlier would
// send the release to the application underneath.
func (a *application) cellClicked() {
	switch clickedColumn(a.table.Handle()) {
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
	a.hide()
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
	// The popup stays open so the password can be copied next.
	a.copied, a.showCopied = *e, true
	a.copiedGen++
	generation := a.copiedGen
	a.table.Invalidate()
	time.AfterFunc(1500*time.Millisecond, func() {
		a.mw.Synchronize(func() {
			if a.copiedGen == generation {
				a.showCopied = false
				a.table.Invalidate()
			}
		})
	})
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
		if msg.HWnd == a.addButton.Handle() {
			return false
		}
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
