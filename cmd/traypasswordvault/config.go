//go:build windows

package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/wojtekadams/Tray-Password-Valut/internal/theme"
	"github.com/wojtekadams/Tray-Password-Valut/internal/vault"
)

// config is stored in %APPDATA%\TrayPasswordVault\config.json.
type config struct {
	DatabasePath string `json:"databasePath"`
	// Hotkey overrides the default shortcuts, e.g. "Ctrl+Shift+Space".
	Hotkey string `json:"hotkey,omitempty"`
	// Theme is "light", "dark" or empty to follow Windows.
	Theme theme.Mode `json:"theme,omitempty"`
	// Mask is "full" or "part"; empty means "part".
	Mask vault.MaskMode `json:"mask,omitempty"`
}

func configPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "TrayPasswordVault", "config.json"), nil
}

func loadConfig() (config, error) {
	var cfg config
	path, err := configPath()
	if err != nil {
		return cfg, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return cfg, nil
	}
	if err != nil {
		return cfg, err
	}
	return cfg, json.Unmarshal(data, &cfg)
}

func saveConfig(cfg config) error {
	path, err := configPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}
