package tui

import (
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/uid0/scantty/internal/theme"
)

func TestApplyThemeUpdatesAccentDerivedStyles(t *testing.T) {
	t.Cleanup(func() { ApplyTheme(theme.DefaultName) })
	ApplyTheme(theme.DefaultName)

	defaultTitle := StyleTitle.GetForeground()
	defaultSidebar := StyleSidebarItemActive.GetForeground()

	if got := ApplyTheme("blue"); got != "blue" {
		t.Fatalf("ApplyTheme returned %q, want blue", got)
	}
	if got, want := StyleTitle.GetForeground(), lipgloss.Color("#1971c2"); got != want {
		t.Fatalf("StyleTitle foreground = %#v, want %#v", got, want)
	}
	if got, want := StyleSidebarItemActive.GetForeground(), lipgloss.Color("#1971c2"); got != want {
		t.Fatalf("StyleSidebarItemActive foreground = %#v, want %#v", got, want)
	}
	if StyleTitle.GetForeground() == defaultTitle {
		t.Fatal("StyleTitle foreground did not change from default")
	}
	if StyleSidebarItemActive.GetForeground() == defaultSidebar {
		t.Fatal("StyleSidebarItemActive foreground did not change from default")
	}
	if got, want := StyleStatusOK.GetForeground(), lipgloss.Color("42"); got != want {
		t.Fatalf("StyleStatusOK foreground = %#v, want unchanged %#v", got, want)
	}

	if got := ApplyTheme("missing"); got != theme.DefaultName {
		t.Fatalf("invalid theme applied %q, want fallback %q", got, theme.DefaultName)
	}
	if got, want := StyleTitle.GetForeground(), lipgloss.Color(theme.Default().Accent); got != want {
		t.Fatalf("fallback StyleTitle foreground = %#v, want %#v", got, want)
	}
}

func TestSettingsThemePickerCyclesAppliesAndPersists(t *testing.T) {
	t.Cleanup(func() { ApplyTheme(theme.DefaultName) })
	ApplyTheme(theme.DefaultName)

	var saved string
	s := NewSettingsScreen(Deps{
		SaveThemePreference: func(name string) error {
			saved = name
			return nil
		},
	})

	if !s.HandlesKey("c") {
		t.Fatal("settings screen must claim c so global hotkeys do not intercept it")
	}
	next, cmd := s.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("c")})
	if next != s {
		t.Fatalf("settings c returned %T, want same screen", next)
	}
	if CurrentTheme() != "blue" {
		t.Fatalf("current theme = %q, want blue", CurrentTheme())
	}
	if saved != "blue" {
		t.Fatalf("saved theme = %q, want blue", saved)
	}
	if cmd == nil {
		t.Fatal("theme picker should emit a status command")
	}
	if !strings.Contains(s.View(), "Theme: Blue") {
		t.Fatalf("settings view does not show current theme:\n%s", s.View())
	}
}

func TestSettingsThemePickerReportsSaveFailure(t *testing.T) {
	t.Cleanup(func() { ApplyTheme(theme.DefaultName) })
	ApplyTheme(theme.DefaultName)

	s := NewSettingsScreen(Deps{
		SaveThemePreference: func(string) error {
			return errors.New("disk full")
		},
	})

	next, cmd := s.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("c")})
	if next != s {
		t.Fatalf("settings c returned %T, want same screen", next)
	}
	if CurrentTheme() != "blue" {
		t.Fatalf("current theme = %q, want live-applied blue despite save failure", CurrentTheme())
	}
	if cmd == nil {
		t.Fatal("save failure should emit a status command")
	}
}
