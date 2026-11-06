# Tray Password Vault

<img src="logo.jpg" alt="Tray Password Vault logo" width="128">

A small password manager for Windows 11 that lives in the system tray. It works on
KeePass (`.kdbx`) files, so the same database can also be opened in KeePass and
KeePassXC. Everything ships as a single `TrayPasswordVault.exe`, with no installer
and no runtime to install.

## Usage

1. Run the exe and pick an existing `.kdbx` database or create a new one. The app
   adds itself to autostart; you can turn this off in the tray icon menu.
2. After Windows starts, a window asks for the database password.
3. The keyboard shortcut (or clicking the tray icon) opens a small popup above the tray.

| Input | Action |
| --- | --- |
| Typing | Filters by name and username |
| Up / Down | Selects an entry |
| Enter, double-click, **Password** button | Copies the password and hides the popup |
| Ctrl+U, **Login** button | Copies the username and keeps the popup open |
| Ctrl+N, **+** button | Adds an entry (name, username, password) |
| Esc, clicking outside | Hides the popup |

Passwords are cleared from the clipboard after 30 seconds and are kept out of
clipboard history (Win+V).

## Keyboard shortcut

Windows 11 usually reserves Win+W for Widgets, so the app tries `Win+W`,
`Win+Shift+W` and `Ctrl+Alt+W` in that order. The tray icon tooltip shows the active
shortcut. To set your own, edit `%APPDATA%\TrayPasswordVault\config.json` and restart
the app:

```json
{
  "databasePath": "C:\\Users\\me\\passwords.kdbx",
  "hotkey": "Ctrl+Shift+Space"
}
```

## KeePass compatibility

- Supported: KDBX 3.1, 4.0 and 4.1, with AES, ChaCha20 or Twofish encryption and
  AES-KDF or Argon2 key derivation.
- Not supported: databases that require a key file or a hardware key.
- Entries in the recycle bin are hidden. New entries go to the root group.
- Every save starts from the current file on disk, so changes made in KeePass are not
  overwritten. The new version is verified before it replaces the file, and the
  previous one is kept as `<database>.kdbx.bak`.

## Security

- Passwords and a hash of the master password stay in process memory while the
  database is unlocked, and Go offers no reliable way to wipe them. The tray menu's **Lock** option
  releases the references, but does not guarantee they leave RAM immediately.
- The vault locks itself when the screen locks, when the system sleeps, and after 15
  minutes without keyboard or mouse input. The password is asked for again on next use.
- Databases created by this app use weak Argon2 settings (1 MiB of memory, 2 iterations)
  and accept any master password length, so a stolen file is easy to brute-force. Until
  this is fixed, use a long master password, or create the database in KeePass or
  KeePassXC and open it here. Their settings are kept when this app saves.
- The exe is not signed, so SmartScreen may show a warning on first launch.

## Building

GitHub Actions builds the exe: every push produces a `TrayPasswordVault` artifact, and
a `v*` tag creates a Release. To build locally (also works from macOS or Linux):

```sh
go install github.com/tc-hib/go-winres@latest
go-winres make --arch amd64
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags "-H=windowsgui -s -w" -o TrayPasswordVault.exe .
go test ./internal/...
```
