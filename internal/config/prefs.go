package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type Prefs struct {
	Theme string `json:"theme,omitempty"`
}

func LoadPrefs() (Prefs, error) {
	path, err := defaultPrefsPath()
	if err != nil {
		return Prefs{}, err
	}
	return LoadPrefsFile(path)
}

func SavePrefs(p Prefs) error {
	path, err := defaultPrefsPath()
	if err != nil {
		return err
	}
	return SavePrefsFile(path, p)
}

func LoadPrefsFile(path string) (Prefs, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Prefs{}, nil
	}
	if err != nil {
		return Prefs{}, fmt.Errorf("read prefs: %w", err)
	}
	if strings.TrimSpace(string(b)) == "" {
		return Prefs{}, nil
	}
	var p Prefs
	if err := json.Unmarshal(b, &p); err != nil {
		return Prefs{}, fmt.Errorf("parse prefs: %w", err)
	}
	return p, nil
}

func SavePrefsFile(path string, p Prefs) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create prefs dir: %w", err)
	}
	b, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal prefs: %w", err)
	}
	b = append(b, '\n')
	if err := os.WriteFile(path, b, 0o600); err != nil {
		return fmt.Errorf("write prefs: %w", err)
	}
	return nil
}

func defaultPrefsPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "scantty", "prefs.json"), nil
}
