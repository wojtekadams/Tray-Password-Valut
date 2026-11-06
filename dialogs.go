//go:build windows

package main

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/tailscale/walk"
	. "github.com/tailscale/walk/declarative"

	"github.com/wojtekadams/Tray-Password-Valut/internal/vault"
)

// minMasterPassword is the shortest master password accepted for a new database.
const minMasterPassword = 12

const kdbxFilter = "KeePass database (*.kdbx)|*.kdbx|All files (*.*)|*.*"

// form is a dialog whose accept action runs in the background, because key
// derivation can take seconds. The dialog stays open and shows the error if the
// action fails.
type form struct {
	dlg        *walk.Dialog
	ok, cancel *walk.PushButton
	status     *walk.Label
	busy       bool
}

func (f *form) buttons(okText string, onOK func()) []Widget {
	return []Widget{
		Label{AssignTo: &f.status, TextColor: walk.RGB(192, 0, 0)},
		Composite{
			Layout: HBox{MarginsZero: true},
			Children: []Widget{
				HSpacer{},
				PushButton{AssignTo: &f.ok, Text: okText, OnClicked: onOK},
				PushButton{AssignTo: &f.cancel, Text: "Cancel", OnClicked: func() { f.dlg.Cancel() }},
			},
		},
	}
}

// run executes work off the UI thread and accepts the dialog when it succeeds.
func (f *form) run(work func() error) {
	f.busy = true
	f.ok.SetEnabled(false)
	f.cancel.SetEnabled(false)
	f.status.SetText("Please wait…")
	go func() {
		err := work()
		f.dlg.Synchronize(func() {
			f.busy = false
			f.ok.SetEnabled(true)
			f.cancel.SetEnabled(true)
			if err != nil {
				f.status.SetText(errorText(err))
				return
			}
			f.dlg.Accept()
		})
	}()
}

func errorText(err error) string {
	if errors.Is(err, vault.ErrWrongPassword) {
		return "Wrong password."
	}
	return err.Error()
}

// exec shows a dialog built from d, focuses the widget returned by focus, and
// returns true if the dialog was accepted.
func (a *application) exec(d Dialog, f *form, focus func() walk.Widget) bool {
	d.AssignTo = &f.dlg
	d.DefaultButton = &f.ok
	d.CancelButton = &f.cancel
	d.FixedSize = true
	d.Font = uiFont()
	if err := d.Create(a.mw); err != nil {
		a.errorBox(err)
		return false
	}
	f.dlg.SetBackground(a.backgroundBrush)
	setDarkTitle(f.dlg.Handle(), a.palette.Dark)
	a.themeChildren(f.dlg)
	f.status.SetTextColor(walkColor(a.palette.Error))
	f.dlg.Closing().Attach(func(canceled *bool, _ walk.CloseReason) {
		*canceled = f.busy // the background work must finish first
	})
	if icon := appIcon(); icon != nil {
		f.dlg.SetIcon(icon)
	}
	f.dlg.Synchronize(func() {
		placeOnTop(f.dlg.Handle())
		focus().SetFocus()
	})
	a.modal++
	a.dialog = f
	defer func() {
		a.modal--
		a.dialog = nil
	}()
	return f.dlg.Run() == walk.DlgCmdOK
}

// unlock asks for the master password and returns true once a database is open.
func (a *application) unlock() bool {
	if a.cfg.DatabasePath == "" {
		return a.chooseDatabase()
	}
	path := a.cfg.DatabasePath
	var (
		f        form
		password *walk.LineEdit
		opened   *vault.Vault
	)
	accepted := a.exec(Dialog{
		Title:   title,
		MinSize: Size{Width: 380},
		Layout:  VBox{},
		Children: append([]Widget{
			Label{Text: "Password for " + filepath.Base(path)},
			LineEdit{AssignTo: &password, PasswordMode: true},
		}, f.buttons("Unlock", func() {
			text := password.Text()
			f.run(func() (err error) {
				opened, err = vault.Open(path, text)
				return err
			})
		})...),
	}, &f, func() walk.Widget { return password })
	if !accepted {
		return false
	}
	a.setVault(opened)
	return true
}

// chooseDatabase selects a file to open or create, and unlocks it.
func (a *application) chooseDatabase() bool {
	switch a.box("Do you already have a KeePass database?\n\nIf not, a new one will be created.",
		walk.MsgBoxYesNoCancel|walk.MsgBoxIconQuestion) {
	case walk.DlgCmdYes:
		dlg := walk.FileDialog{Title: "Select KeePass database", Filter: kdbxFilter}
		if !a.fileDialog(func() (bool, error) { return dlg.ShowOpen(a.mw) }) {
			return false
		}
		previous := a.cfg.DatabasePath
		a.cfg.DatabasePath = dlg.FilePath
		if !a.unlock() {
			a.cfg.DatabasePath = previous
			return false
		}
		return a.databaseChosen()
	case walk.DlgCmdNo:
		dlg := walk.FileDialog{Title: "New KeePass database", Filter: kdbxFilter, FilePath: "passwords.kdbx"}
		if !a.fileDialog(func() (bool, error) { return dlg.ShowSave(a.mw) }) {
			return false
		}
		path := dlg.FilePath
		if !strings.EqualFold(filepath.Ext(path), ".kdbx") {
			path += ".kdbx"
		}
		if !a.createDatabase(path) {
			return false
		}
		a.cfg.DatabasePath = path
		return a.databaseChosen()
	}
	return false
}

// databaseChosen persists the choice. The first database also enables
// autostart, since the vault is meant to ask for the password at logon.
func (a *application) databaseChosen() bool {
	first := false
	if previous, err := loadConfig(); err == nil {
		first = previous.DatabasePath == ""
	}
	if err := saveConfig(a.cfg); err != nil {
		a.errorBox(err)
	}
	if first {
		if err := setAutostart(true); err != nil {
			a.errorBox(err)
		}
		a.autostart.SetChecked(autostartEnabled())
	}
	return true
}

func (a *application) createDatabase(path string) bool {
	var (
		f               form
		password, again *walk.LineEdit
		created         *vault.Vault
	)
	accepted := a.exec(Dialog{
		Title:   "New database",
		MinSize: Size{Width: 380},
		Layout:  VBox{},
		Children: append([]Widget{
			Label{Text: "Master password"},
			LineEdit{AssignTo: &password, PasswordMode: true},
			Label{Text: "Repeat password"},
			LineEdit{AssignTo: &again, PasswordMode: true},
		}, f.buttons("Create", func() {
			text := password.Text()
			switch {
			case utf8.RuneCountInString(text) < minMasterPassword:
				f.status.SetText(fmt.Sprintf("Use at least %d characters.", minMasterPassword))
			case text != again.Text():
				f.status.SetText("Passwords do not match.")
			default:
				f.run(func() (err error) {
					created, err = vault.Create(path, text)
					return err
				})
			}
		})...),
	}, &f, func() walk.Widget { return password })
	if !accepted {
		return false
	}
	a.setVault(created)
	return true
}

// addEntry asks for a new entry and saves it to the database.
func (a *application) addEntry() {
	if a.vault == nil || a.modal > 0 {
		return
	}
	v := a.vault
	var (
		f                    form
		name, user, password *walk.LineEdit
		reveal               *walk.PushButton
	)
	accepted := a.exec(Dialog{
		Title:   "Add password",
		MinSize: Size{Width: 380},
		Layout:  VBox{},
		Children: append([]Widget{
			Label{Text: "Name"},
			LineEdit{AssignTo: &name},
			Label{Text: "Username"},
			LineEdit{AssignTo: &user},
			Label{Text: "Password"},
			Composite{
				Layout: HBox{MarginsZero: true},
				Children: []Widget{
					LineEdit{AssignTo: &password, PasswordMode: true},
					// A button, because a themed check box ignores text colours
					// and would stay black in dark mode.
					PushButton{AssignTo: &reveal, Text: "Show", MaxSize: Size{Width: 60}, OnClicked: func() {
						hidden := !password.PasswordMode()
						password.SetPasswordMode(hidden)
						if hidden {
							reveal.SetText("Show")
						} else {
							reveal.SetText("Hide")
						}
					}},
				},
			},
		}, f.buttons("Save", func() {
			title, username, secret := strings.TrimSpace(name.Text()), user.Text(), password.Text()
			if title == "" {
				f.status.SetText("Enter a name.")
				return
			}
			f.run(func() error { return v.Add(title, username, secret) })
		})...),
	}, &f, func() walk.Widget { return name })
	if accepted {
		a.refresh()
	}
	if a.mw.Visible() {
		a.placePopup()
		a.search.SetFocus()
	}
}

// changeHotkey asks for a shortcut. A shortcut that cannot be used keeps the
// dialog open, so the next one can be tried straight away.
func (a *application) changeHotkey() {
	if a.modal > 0 {
		return
	}
	var (
		f     form
		field *walk.LineEdit
	)
	a.exec(Dialog{
		Title:   "Set shortcut",
		MinSize: Size{Width: 380},
		Layout:  VBox{},
		Children: append([]Widget{
			Label{Text: "Shortcut, for example Win+Y or Ctrl+Alt+K"},
			LineEdit{AssignTo: &field, Text: a.activeHotkey},
		}, f.buttons("OK", func() {
			name := strings.TrimSpace(field.Text())
			if err := a.hotkeys.set(name); err != nil {
				f.status.SetText(err.Error() + " Try another one.")
				return
			}
			a.setActiveHotkey(name)
			a.cfg.Hotkey = name
			if err := saveConfig(a.cfg); err != nil {
				f.status.SetText(err.Error())
				return
			}
			f.dlg.Accept()
		})...),
	}, &f, func() walk.Widget { return field })
}

func (a *application) box(message string, style walk.MsgBoxStyle) int {
	a.modal++
	defer func() { a.modal-- }()
	return walk.MsgBox(a.mw, title, message, style)
}

func (a *application) errorBox(err error) {
	a.box(err.Error(), walk.MsgBoxIconError)
}

func (a *application) fileDialog(show func() (bool, error)) bool {
	a.modal++
	accepted, err := show()
	a.modal--
	if err != nil {
		a.errorBox(err)
		return false
	}
	return accepted
}
