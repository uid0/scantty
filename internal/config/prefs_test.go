package config

import (
	"path/filepath"
	"testing"

	"github.com/uid0/scantty/internal/theme"
)

// setRequiredConfigEnv gives a test the minimum environment Load() insists on,
// and — just as importantly — points every user-directory lookup the config
// package makes at a throwaway directory that dies with the test.
//
// The isolation is not optional decoration. SavePrefs() writes through
// defaultPrefsPath() -> os.UserConfigDir(), and os.UserConfigDir() on darwin is
// $HOME/Library/Application Support: it ignores XDG_CONFIG_HOME entirely.
// These tests used to set only XDG_CONFIG_HOME, which is a no-op on macOS, so
// every run saved theme "green" into the developer's REAL
// ~/Library/Application Support/scantty/prefs.json. The first run on a machine
// passed; every run after it read back its own leftovers and failed the
// "no prefs file yet -> default theme" assertion with
// `default theme = "green", want "purple"`. Setting HOME is what actually
// isolates darwin; XDG_CONFIG_HOME/XDG_CACHE_HOME cover Linux, where
// os.UserConfigDir()/os.UserCacheDir() prefer them over $HOME. Set all three
// so the test is hermetic on both.
func setRequiredConfigEnv(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
	t.Setenv(envOMSURL, "https://oms.example.test")
	t.Setenv(envForgeKeyURL, "https://forgekey.example.test")
	t.Setenv(envOMSToken, "")
	t.Setenv(envForgeKeyToken, "")
	t.Setenv(envCachePath, "")
	t.Setenv(envScannerSource, "")
}

func TestLoadResolvesThemeDefaultPrefsAndEnv(t *testing.T) {
	setRequiredConfigEnv(t)
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
