package tui

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/uid0/scantty/internal/omsapi"
	"github.com/uid0/scantty/internal/theme"
)

type SettingsScreen struct {
	deps      Deps
	site      *omsapi.SiteSettings
	siteErr   string
	siteReady bool

	// Superuser gates the edit affordance: site-settings writes are
	// superuser-only on the backend, so a non-superuser only ever sees the
	// read view (mirrors the web, which redirects non-superusers away from the
	// edit page). profileReady stays false — and the edit hint stays hidden —
	// until the profile fetch resolves, so we never flash an affordance the
	// operator can't use.
	superuser    bool
	profileReady bool
}

type siteSettingsLoadedMsg struct {
	site *omsapi.SiteSettings
	err  error
}

type settingsProfileLoadedMsg struct {
	superuser bool
}

func NewSettingsScreen(deps Deps) *SettingsScreen { return &SettingsScreen{deps: deps} }

func (s *SettingsScreen) HandlesKey(key string) bool { return key == "c" }

func (s *SettingsScreen) Init() tea.Cmd {
	deps := s.deps
	ctx := deps.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	loadSite := func() tea.Msg {
		site, err := deps.OMS.GetSiteSettings(ctx)
		return siteSettingsLoadedMsg{site: site, err: err}
	}
	loadProfile := func() tea.Msg {
		// A failed profile fetch (e.g. pre-login) just leaves edit hidden.
		p, err := deps.OMS.GetProfile(ctx)
		return settingsProfileLoadedMsg{superuser: err == nil && p != nil && p.IsSuperuser}
	}
	return tea.Batch(loadSite, loadProfile)
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
	case settingsProfileLoadedMsg:
		s.profileReady = true
		s.superuser = m.superuser
		return s, nil
	case tea.KeyMsg:
		switch m.String() {
		case "E":
			switch {
			case s.superuser:
				return s, SwitchTo(WSSettings, NewSiteSettingsFormScreen(s.deps))
			case !s.profileReady:
				// Profile fetch still in flight — don't wrongly deny a superuser.
				return s, Status("checking permissions… try again", StatusInfo)
			default:
				return s, Status("site settings are superuser-only", StatusWarn)
			}
		case "t":
			// Tax receipt lookup is listed under Settings in the web sidebar; it
			// backs onto a public endpoint, so it's open to any operator (no
			// superuser gate, unlike site-settings edit).
			return s, SwitchTo(WSSettings, NewTaxReceiptLookupScreen(s.deps))
		case "S":
			// Service status — which external dependencies are working right
			// now. It mounts HERE, on a screen the sidebar already reaches,
			// rather than taking a new global letter: the JDE redesign is
			// retiring those, and the status bar's degraded chip is the thing
			// that sends an operator looking in the first place. Open to any
			// operator, like the IsAuthenticated endpoint behind it.
			//
			// Uppercase S: lowercase s is the Settings nav hotkey itself, and
			// S is free in app.go's global switch, so it reaches this screen
			// through the root's fall-through with no HandlesKey claim.
			return s, SwitchTo(WSSettings, NewServiceStatusScreen(s.deps))
		case "c":
			next := ApplyTheme(theme.Next(CurrentTheme()))
			if s.deps.SaveThemePreference != nil {
				if err := s.deps.SaveThemePreference(next); err != nil {
					return s, Status("theme changed; preference save failed: "+err.Error(), StatusWarn)
				}
			}
			return s, Status("theme: "+theme.Label(next), StatusOK)
		}
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
		if s.profileReady && s.superuser {
			b.WriteString("\n  " + StyleMuted.Render("E edit site settings") + "\n")
		}
		b.WriteString("\n")
	} else if s.siteReady && s.siteErr != "" {
		b.WriteString(StyleMuted.Render("Site: ") + StyleStatusError.Render(s.siteErr) + "\n\n")
	}

	b.WriteString("  " + StyleMuted.Render("t tax receipt lookup") + "\n")
	b.WriteString("  " + serviceStatusHint() + "\n")
	// Name what is wrong right here too. An operator who saw the chip on the
	// status bar and came looking should not have to open another screen to
	// find out whether it is still true.
	if degraded := s.deps.Health.Degraded(); len(degraded) > 0 {
		b.WriteString("  " + StyleStatusWarn.Render("⚠ "+serviceStatusSummary(degraded)) + "\n")
	}
	b.WriteString("\n")

	b.WriteString(StyleMuted.Render("Appearance") + "\n\n")
	b.WriteString(fmt.Sprintf("  Theme: %s\n", theme.Label(CurrentTheme())))
	b.WriteString("  " + StyleMuted.Render("c change theme") + "\n\n")

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
		"SCANTTY_THEME",
		"SENTRY_DSN",
		"SENTRY_DISABLED",
		"SENTRY_ENVIRONMENT",
		"SENTRY_RELEASE",
	} {
		b.WriteString("  - " + line + "\n")
	}
	return b.String()
}
