//go:build windows

// Tray Password Vault is a small Windows tray password manager working on
// KeePass (KDBX) files.
package main

import (
	"fmt"
	"os"

	"github.com/tailscale/walk"
)

func main() {
	if !acquireSingleInstance() {
		return
	}
	app, err := walk.InitApp()
	if err != nil {
		fatal(err)
	}
	a := &application{}
	if a.cfg, err = loadConfig(); err != nil {
		fatal(fmt.Errorf("read configuration: %w", err))
	}
	if err := a.createWindow(); err != nil {
		fatal(err)
	}
	if err := a.createTray(); err != nil {
		fatal(err)
	}
	defer a.tray.Dispose()

	app.AddGlobalPreTranslateHandler(a)
	a.startHotkey()
	if autostartEnabled() {
		setAutostart(true) // follow the exe if it was moved
	}
	// At logon only ask for the password; the list opens on the shortcut.
	a.mw.Synchronize(func() { a.unlock() })
	app.Run()
}

func fatal(err error) {
	walk.MsgBox(nil, title, err.Error(), walk.MsgBoxIconError)
	os.Exit(1)
}
