package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

type SettingsScreen struct {
	deps Deps
}

func NewSettingsScreen(deps Deps) *SettingsScreen { return &SettingsScreen{deps: deps} }

func (s *SettingsScreen) Init() tea.Cmd                       { return nil }
func (s *SettingsScreen) Update(msg tea.Msg) (Screen, tea.Cmd) { return s, nil }
func (s *SettingsScreen) Title() string                       { return "Settings" }

func (s *SettingsScreen) View() string {
	var b strings.Builder
	b.WriteString(StyleMuted.Render("Runtime configuration (env vars)") + "\n\n")
	if s.deps.OMS != nil {
		token := s.deps.OMS.AccessToken()
		tokenState := "set"
		if token == "" {
			tokenState = "unset"
		}
		b.WriteString(fmt.Sprintf("  OMS auth token: %s\n", tokenState))
	}
	b.WriteString("\n")
	b.WriteString(StyleMuted.Render("Environment variables:") + "\n")
	for _, line := range []string{
		"SCANTTY_OMS_URL",
		"SCANTTY_OMS_TOKEN",
		"SCANTTY_FORGEKEY_URL",
		"SCANTTY_FORGEKEY_TOKEN",
		"SCANTTY_FORGEKEY_CLIENT_CERT",
		"SCANTTY_FORGEKEY_CLIENT_KEY",
		"SCANTTY_FORGEKEY_CA_CERT",
		"SCANTTY_CACHE_PATH",
		"SCANTTY_SCANNER_SOURCE",
	} {
		b.WriteString("  - " + line + "\n")
	}
	return b.String()
}
