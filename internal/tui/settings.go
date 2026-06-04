package tui

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/uid0/scantty/internal/omsapi"
)

type SettingsScreen struct {
	deps      Deps
	site      *omsapi.SiteSettings
	siteErr   string
	siteReady bool
}

type siteSettingsLoadedMsg struct {
	site *omsapi.SiteSettings
	err  error
}

func NewSettingsScreen(deps Deps) *SettingsScreen { return &SettingsScreen{deps: deps} }

func (s *SettingsScreen) Init() tea.Cmd {
	deps := s.deps
	ctx := deps.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return func() tea.Msg {
		site, err := deps.OMS.GetSiteSettings(ctx)
		return siteSettingsLoadedMsg{site: site, err: err}
	}
}

func (s *SettingsScreen) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch m := msg.(type) {
	case siteSettingsLoadedMsg:
		s.siteReady = true
		s.site = m.site
		if m.err != nil {
			s.siteErr = m.err.Error()
		}
		return s, nil
	}
	return s, nil
}
func (s *SettingsScreen) Title() string { return "Settings" }

func (s *SettingsScreen) View() string {
	var b strings.Builder

	// Site identity — read from /api/customization/settings/, which is
	// AllowAny so it works pre-login. Gives the operator a one-glance
	// sanity check that scantty is pointed at the right deployment.
	if s.siteReady && s.site != nil {
		b.WriteString(StyleTitle.Render("Site") + "\n")
		rows := [][2]string{
			{"Name", s.site.SiteName},
			{"Tagline", s.site.SiteTagline},
			{"Contact", strings.TrimSpace(strings.Join([]string{s.site.ContactEmail, s.site.ContactPhone}, " · "))},
			{"Website", s.site.WebsiteURL},
		}
		for _, r := range rows {
			if r[1] == "" {
				continue
			}
			b.WriteString(fmt.Sprintf("  %s %s\n", StyleMuted.Render(r[0]+":"), r[1]))
		}
		b.WriteString("\n")
	} else if s.siteReady && s.siteErr != "" {
		b.WriteString(StyleMuted.Render("Site: ") + StyleStatusError.Render(s.siteErr) + "\n\n")
	}

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
		"SENTRY_DSN",
		"SENTRY_DISABLED",
		"SENTRY_ENVIRONMENT",
		"SENTRY_RELEASE",
	} {
		b.WriteString("  - " + line + "\n")
	}
	return b.String()
}
