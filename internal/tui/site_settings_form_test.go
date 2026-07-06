package tui

import (
	"os"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/uid0/scantty/internal/omsapi"
)

func strptr(s string) *string { return &s }

// TestSiteSettingsForm_BuildPayload walks the happy path: the full editable set
// maps onto a SiteSettingsWrite, incl. the two toggles and the int window.
func TestSiteSettingsForm_BuildPayload(t *testing.T) {
	s := NewSiteSettingsFormScreen(Deps{})
	s.settings = &omsapi.SiteSettings{}
	s.buildFields()

	s.inputs[ssName].SetValue("Acme Makerspace")
	s.inputs[ssTagline].SetValue("build things")
	s.inputs[ssLogoAlt].SetValue("Acme logo")
	s.inputs[ssPrimaryColor].SetValue("#112233")
	s.inputs[ssSecondaryColor].SetValue("#445566")
	s.inputs[ssFooterText].SetValue("© Acme")
	s.inputs[ssContactEmail].SetValue("hi@acme.test")
	s.inputs[ssContactPhone].SetValue("555-1234")
	s.inputs[ssWebsiteURL].SetValue("https://acme.test")
	s.inputs[ssDashboardTitle].SetValue("Ops")
	s.inputs[ssDashboardSubtitle].SetValue("live")
	s.inputs[ssPmAutoBundle].SetValue("14")
	s.showLogo = true

	w, err := s.buildPayload()
	if err != nil {
		t.Fatalf("buildPayload: %v", err)
	}
	if w.SiteName != "Acme Makerspace" || w.SiteTagline != "build things" {
		t.Errorf("name/tagline = %q / %q", w.SiteName, w.SiteTagline)
	}
	if w.PrimaryColor != "#112233" || w.SecondaryColor != "#445566" {
		t.Errorf("colors = %q / %q", w.PrimaryColor, w.SecondaryColor)
	}
	if w.FooterText != "© Acme" || w.ContactEmail != "hi@acme.test" || w.WebsiteURL != "https://acme.test" {
		t.Errorf("footer/email/url = %q / %q / %q", w.FooterText, w.ContactEmail, w.WebsiteURL)
	}
	if w.DashboardTitle != "Ops" || w.DashboardSubtitle != "live" {
		t.Errorf("dashboard = %q / %q", w.DashboardTitle, w.DashboardSubtitle)
	}
	if !w.ShowLogoOnDashboard {
		t.Errorf("ShowLogoOnDashboard = false, want true")
	}
	if w.PmAutoBundleDueWithinDays != 14 {
		t.Errorf("PmAutoBundleDueWithinDays = %d, want 14", w.PmAutoBundleDueWithinDays)
	}
	if w.LogoPath != "" || w.FaviconPath != "" || w.RemoveLogo {
		t.Errorf("no file action expected: %+v", w)
	}
}

// TestSiteSettingsForm_Validation covers name-required, required hex colors, and
// the non-negative int window.
func TestSiteSettingsForm_Validation(t *testing.T) {
	s := NewSiteSettingsFormScreen(Deps{})
	s.settings = &omsapi.SiteSettings{}
	s.buildFields()

	// Empty name rejected.
	if _, err := s.buildPayload(); err == nil {
		t.Errorf("expected error for empty name")
	}
	s.inputs[ssName].SetValue("Acme")

	// Colors are required (blank rejected — model columns are non-blank).
	s.inputs[ssPrimaryColor].SetValue("")
	if _, err := s.buildPayload(); err == nil {
		t.Errorf("expected error for blank primary color")
	}
	s.inputs[ssPrimaryColor].SetValue("#112233")
	s.inputs[ssSecondaryColor].SetValue("nope")
	if _, err := s.buildPayload(); err == nil {
		t.Errorf("expected error for bad secondary color")
	}
	s.inputs[ssSecondaryColor].SetValue("#445566")

	// Non-numeric / negative PM window rejected.
	for _, bad := range []string{"abc", "-1"} {
		s.inputs[ssPmAutoBundle].SetValue(bad)
		if _, err := s.buildPayload(); err == nil {
			t.Errorf("expected error for pm window %q", bad)
		}
	}
	// Blank PM window is allowed and means 0.
	s.inputs[ssPmAutoBundle].SetValue("")
	w, err := s.buildPayload()
	if err != nil {
		t.Fatalf("blank pm window should validate: %v", err)
	}
	if w.PmAutoBundleDueWithinDays != 0 {
		t.Errorf("blank pm window = %d, want 0", w.PmAutoBundleDueWithinDays)
	}
}

// TestSiteSettingsForm_Hydrate fills inputs + toggle from a fetched record and
// defaults blank colors to the model defaults.
func TestSiteSettingsForm_Hydrate(t *testing.T) {
	s := NewSiteSettingsFormScreen(Deps{})
	s.settings = &omsapi.SiteSettings{
		SiteName:                  "Acme",
		SiteTagline:               "tag",
		LogoAltText:               "alt",
		PrimaryColor:              "", // blank → default
		SecondaryColor:            "#445566",
		ContactEmail:              "hi@acme.test",
		ShowLogoOnDashboard:       true,
		PmAutoBundleDueWithinDays: 9,
	}
	s.hydrate()
	if s.inputs[ssName].Value() != "Acme" {
		t.Errorf("name = %q", s.inputs[ssName].Value())
	}
	if s.inputs[ssPrimaryColor].Value() != "#007cba" {
		t.Errorf("blank primary color should default to #007cba, got %q", s.inputs[ssPrimaryColor].Value())
	}
	if s.inputs[ssPmAutoBundle].Value() != "9" {
		t.Errorf("pm window = %q, want 9", s.inputs[ssPmAutoBundle].Value())
	}
	if !s.showLogo {
		t.Errorf("showLogo = false, want true")
	}
}

// TestSiteSettingsForm_RemoveLogoRowConditional confirms the Remove-logo row is
// present only when a logo currently exists.
func TestSiteSettingsForm_RemoveLogoRowConditional(t *testing.T) {
	s := NewSiteSettingsFormScreen(Deps{})

	s.settings = &omsapi.SiteSettings{LogoURL: nil}
	s.buildFields()
	if fieldsContain(s.fields, ssRemoveLogo) {
		t.Errorf("no logo → Remove-logo row must be absent")
	}

	s.settings = &omsapi.SiteSettings{LogoURL: strptr("http://x/logo.png")}
	s.buildFields()
	if !fieldsContain(s.fields, ssRemoveLogo) {
		t.Errorf("logo present → Remove-logo row must be shown")
	}
}

// TestSiteSettingsForm_RemoveGatedOnExistingLogo confirms the RemoveLogo flag is
// only emitted when a logo actually exists (and no replacement was picked).
func TestSiteSettingsForm_RemoveGatedOnExistingLogo(t *testing.T) {
	s := NewSiteSettingsFormScreen(Deps{})
	s.settings = &omsapi.SiteSettings{LogoURL: strptr("http://x/logo.png")}
	s.hydrate() // fills default hex colors so validation passes
	s.buildFields()
	s.inputs[ssName].SetValue("Acme")
	s.removeLogo = true

	w, err := s.buildPayload()
	if err != nil {
		t.Fatalf("buildPayload: %v", err)
	}
	if !w.RemoveLogo {
		t.Errorf("RemoveLogo should be set when a logo exists and remove is toggled")
	}

	// No logo present → remove flag is inert.
	s.settings = &omsapi.SiteSettings{LogoURL: nil}
	s.removeLogo = true
	w, err = s.buildPayload()
	if err != nil {
		t.Fatalf("buildPayload: %v", err)
	}
	if w.RemoveLogo {
		t.Errorf("RemoveLogo must be inert when no logo exists")
	}
}

// TestSiteSettingsForm_ImagePathValidation checks the logo/favicon path guard:
// blank is fine, relative/missing/dir are rejected, an absolute file passes.
func TestSiteSettingsForm_ImagePathValidation(t *testing.T) {
	dir := t.TempDir()
	file := dir + "/logo.png"
	if err := os.WriteFile(file, []byte("PNG"), 0o600); err != nil {
		t.Fatalf("fixture: %v", err)
	}

	if p, err := validateImagePath("", "logo"); err != nil || p != "" {
		t.Errorf("blank path = (%q,%v), want (\"\",nil)", p, err)
	}
	if _, err := validateImagePath("relative/logo.png", "logo"); err == nil {
		t.Errorf("relative path should be rejected")
	}
	if _, err := validateImagePath(dir+"/missing.png", "logo"); err == nil {
		t.Errorf("missing path should be rejected")
	}
	if _, err := validateImagePath(dir, "logo"); err == nil {
		t.Errorf("directory should be rejected")
	}
	if p, err := validateImagePath(file, "logo"); err != nil || p != file {
		t.Errorf("valid file = (%q,%v)", p, err)
	}
}

// TestSiteSettingsForm_RenderSmoke guards the load + form render paths.
func TestSiteSettingsForm_RenderSmoke(t *testing.T) {
	s := NewSiteSettingsFormScreen(Deps{})
	s.loading = false
	s.settings = &omsapi.SiteSettings{SiteName: "Acme", LogoURL: strptr("http://x/l.png")}
	s.hydrate()
	s.buildFields()
	s.terminalHeight = 40
	out := s.View()
	if !strings.Contains(out, "Site name") {
		t.Errorf("form view missing Site name: %q", out)
	}
	if !strings.Contains(out, "current logo") {
		t.Errorf("form view missing current-logo line: %q", out)
	}
}

// TestSettingsScreen_SuperuserGatesEdit confirms E only opens the form for a
// superuser; a non-superuser gets a status message instead.
func TestSettingsScreen_SuperuserGatesEdit(t *testing.T) {
	s := NewSettingsScreen(Deps{})

	// Definite non-superuser (profile resolved): E must not open the form.
	s.profileReady = true
	s.superuser = false
	_, cmd := s.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("E")})
	if cmd == nil {
		t.Fatalf("E should always produce a command")
	}
	if _, ok := cmd().(SwitchScreenMsg); ok {
		t.Errorf("non-superuser E must not switch to the form")
	}

	s.superuser = true
	_, cmd = s.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("E")})
	if cmd == nil {
		t.Fatalf("superuser E should switch")
	}
	sw, ok := cmd().(SwitchScreenMsg)
	if !ok {
		t.Fatalf("superuser E should emit SwitchScreenMsg, got %T", cmd())
	}
	if _, ok := sw.Screen.(*SiteSettingsFormScreen); !ok {
		t.Errorf("E should switch to *SiteSettingsFormScreen, got %T", sw.Screen)
	}
}
