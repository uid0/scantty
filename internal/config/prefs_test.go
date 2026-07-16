package config

import (
	"path/filepath"
	"testing"

	"github.com/uid0/scantty/internal/theme"
)

func setRequiredConfigEnv(t *testing.T) {
	t.Helper()
	t.Setenv(envOMSURL, "https://oms.example.test")
	t.Setenv(envForgeKeyURL, "https://forgekey.example.test")
	t.Setenv(envOMSToken, "")
	t.Setenv(envForgeKeyToken, "")
	t.Setenv(envCachePath, "")
	t.Setenv(envScannerSource, "")
}

func TestLoadResolvesThemeDefaultPrefsAndEnv(t *testing.T) {
	setRequiredConfigEnv(t)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv(envTheme, "")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Theme != theme.DefaultName {
		t.Fatalf("default theme = %q, want %q", cfg.Theme, theme.DefaultName)
	}

	if err := SavePrefs(Prefs{Theme: "green"}); err != nil {
		t.Fatal(err)
	}
	cfg, err = Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Theme != "green" {
		t.Fatalf("saved-pref theme = %q, want green", cfg.Theme)
	}

	t.Setenv(envTheme, "blue")
	cfg, err = Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Theme != "blue" {
		t.Fatalf("env theme = %q, want blue", cfg.Theme)
	}
}

func TestLoadInvalidThemeFallsBackToDefault(t *testing.T) {
	setRequiredConfigEnv(t)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv(envTheme, "")

	if err := SavePrefs(Prefs{Theme: "not-a-theme"}); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Theme != theme.DefaultName {
		t.Fatalf("invalid saved theme = %q, want %q", cfg.Theme, theme.DefaultName)
	}

	if err := SavePrefs(Prefs{Theme: "green"}); err != nil {
		t.Fatal(err)
	}
	t.Setenv(envTheme, "not-a-theme")
	cfg, err = Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Theme != theme.DefaultName {
		t.Fatalf("invalid env theme = %q, want %q", cfg.Theme, theme.DefaultName)
	}
}

func TestPrefsRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "scantty", "prefs.json")

	if err := SavePrefsFile(path, Prefs{Theme: "teal"}); err != nil {
		t.Fatal(err)
	}
	got, err := LoadPrefsFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.Theme != "teal" {
		t.Fatalf("round-trip theme = %q, want teal", got.Theme)
	}
}
