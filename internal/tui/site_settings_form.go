// Site Settings — singleton view + edit.
//
// TUI counterpart to the web SiteSettingsPage.tsx (/settings/site). The site
// settings are a single row edited in place (GET + PATCH on
// /api/customization/settings/), so this is an edit form with no list. It
// mirrors the FULL writable field set of SiteSettingsUpdateSerializer — a
// superset of what the web page happens to expose (the web omits favicon,
// footer, the dashboard title/subtitle, show-logo and the PM auto-bundle
// window) — because completeness is the bar ([[ship-complete-features]]) and the
// serializer is the authoritative contract.
//
// logo + favicon are image uploads: type an absolute path to replace one, leave
// blank to keep it. The logo can additionally be removed (the backend deletes it
// on an empty value); favicon has no such delete hook server-side, so there is no
// favicon-remove. Any file action flips the omsapi call to multipart, exactly as
// the web always posts FormData.
//
// The write path is superuser-only on the backend. SettingsScreen only offers
// the `E` edit affordance to superusers, but the form still surfaces a clean 403
// if permissions changed between load and save.
package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/uid0/scantty/internal/omsapi"
)

const (
	ssName = iota
	ssTagline
	ssLogoAlt
	ssLogoPath
	ssRemoveLogo
	ssFaviconPath
	ssPrimaryColor
	ssSecondaryColor
	ssFooterText
	ssContactEmail
	ssContactPhone
	ssWebsiteURL
	ssDashboardTitle
	ssDashboardSubtitle
	ssShowLogo
	ssPmAutoBundle
	ssFieldMax
)

var ssFieldLabel = map[int]string{
	ssName:              "Site name",
	ssTagline:           "Tagline",
	ssLogoAlt:           "Logo alt text",
	ssLogoPath:          "Logo image",
	ssRemoveLogo:        "Remove logo",
	ssFaviconPath:       "Favicon image",
	ssPrimaryColor:      "Primary color",
	ssSecondaryColor:    "Secondary color",
	ssFooterText:        "Footer text",
	ssContactEmail:      "Contact email",
	ssContactPhone:      "Contact phone",
	ssWebsiteURL:        "Website URL",
	ssDashboardTitle:    "Dashboard title",
	ssDashboardSubtitle: "Dashboard subtitle",
	ssShowLogo:          "Show logo on dashboard",
	ssPmAutoBundle:      "PM auto-bundle window (days)",
}

// ssKind reuses the shared asset field-kind enum: the two booleans are toggles,
// the PM window is a number, everything else (incl. the two file-path inputs and
// the hex colors) is free text.
func ssKind(id int) assetFieldKind {
	switch id {
	case ssRemoveLogo, ssShowLogo:
		return akToggle
	case ssPmAutoBundle:
		return akNumber
	}
	return akText
}

func ssIsTextKind(id int) bool { return ssKind(id) == akText || ssKind(id) == akNumber }

type SiteSettingsFormScreen struct {
	deps Deps

	loading bool
	loadErr string
	saving  bool
	errMsg  string

	settings *omsapi.SiteSettings

	inputs     []textinput.Model
	showLogo   bool
	removeLogo bool

	fields []int
	cursor int

	terminalHeight int
}

type siteSettingsFormLoadedMsg struct {
	settings *omsapi.SiteSettings
	err      error
}

type siteSettingsSavedMsg struct {
	settings *omsapi.SiteSettings
	err      error
}

func NewSiteSettingsFormScreen(deps Deps) *SiteSettingsFormScreen {
	s := &SiteSettingsFormScreen{deps: deps, loading: true}
	s.inputs = make([]textinput.Model, ssFieldMax)
	for id := 0; id < ssFieldMax; id++ {
		if !ssIsTextKind(id) {
			continue
		}
		ti := textinput.New()
		ti.Prompt = ""
		ti.CharLimit = ssCharLimit(id)
		ti.Placeholder = ssPlaceholder(id)
		s.inputs[id] = ti
	}
	// A conservative default field list so the screen renders before load
	// completes; buildFields refines it (logo-remove row) once settings arrive.
	s.buildFields()
	return s
}

func ssCharLimit(id int) int {
	switch id {
	case ssName, ssLogoAlt, ssDashboardTitle:
		return 200
	case ssTagline, ssDashboardSubtitle:
		return 300
	case ssPrimaryColor, ssSecondaryColor:
		return 7
	case ssContactPhone:
		return 20
	case ssContactEmail:
		return 254
	case ssWebsiteURL:
		return 500
	case ssFooterText:
		return 1000
	case ssLogoPath, ssFaviconPath:
		return 1024
	case ssPmAutoBundle:
		return 6
	}
	return 200
}

func ssPlaceholder(id int) string {
	switch id {
	case ssName:
		return "Makerspace name"
	case ssTagline, ssLogoAlt, ssFooterText, ssContactEmail, ssContactPhone,
		ssWebsiteURL, ssDashboardTitle, ssDashboardSubtitle:
		return "optional"
	case ssPrimaryColor:
		return "#007cba"
	case ssSecondaryColor:
		return "#417690"
	case ssLogoPath, ssFaviconPath:
		return "absolute path (blank = keep current)"
	case ssPmAutoBundle:
		return "0 disables"
	}
	return ""
}

func (s *SiteSettingsFormScreen) Title() string       { return "Edit site settings" }
func (s *SiteSettingsFormScreen) WantsRawInput() bool { return true }

func (s *SiteSettingsFormScreen) Init() tea.Cmd {
	return tea.Batch(s.loadSettings(), textinput.Blink)
}

func (s *SiteSettingsFormScreen) ctx() context.Context {
	if s.deps.Ctx != nil {
		return s.deps.Ctx
	}
	return context.Background()
}

func (s *SiteSettingsFormScreen) loadSettings() tea.Cmd {
	deps := s.deps
	ctx := s.ctx()
	return func() tea.Msg {
		st, err := deps.OMS.GetSiteSettings(ctx)
		return siteSettingsFormLoadedMsg{settings: st, err: err}
	}
}

func (s *SiteSettingsFormScreen) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		s.terminalHeight = m.Height
		return s, nil
	case siteSettingsFormLoadedMsg:
		s.loading = false
		if m.err != nil {
			s.loadErr = m.err.Error()
			return s, nil
		}
		s.settings = m.settings
		s.hydrate()
		s.buildFields()
		s.syncFocus()
		return s, nil
	case siteSettingsSavedMsg:
		s.saving = false
		if m.err != nil {
			s.errMsg = m.err.Error()
			return s, Status("save failed: "+m.err.Error(), StatusError)
		}
		return s, tea.Batch(
			Status("site settings updated", StatusOK),
			SwitchTo(WSSettings, NewSettingsScreen(s.deps)),
		)
	case tea.KeyMsg:
		if s.loading {
			if m.String() == "esc" {
				return s, s.cancelCmd()
			}
			return s, nil
		}
		return s.updateFormPhase(m)
	}

	if id, ok := s.currentFieldID(); ok && ssIsTextKind(id) {
		var cmd tea.Cmd
		s.inputs[id], cmd = s.inputs[id].Update(msg)
		return s, cmd
	}
	return s, nil
}

func (s *SiteSettingsFormScreen) hydrate() {
	st := s.settings
	if st == nil {
		return
	}
	s.inputs[ssName].SetValue(st.SiteName)
	s.inputs[ssTagline].SetValue(st.SiteTagline)
	s.inputs[ssLogoAlt].SetValue(st.LogoAltText)
	s.inputs[ssPrimaryColor].SetValue(defaultIfBlank(st.PrimaryColor, "#007cba"))
	s.inputs[ssSecondaryColor].SetValue(defaultIfBlank(st.SecondaryColor, "#417690"))
	s.inputs[ssFooterText].SetValue(st.FooterText)
	s.inputs[ssContactEmail].SetValue(st.ContactEmail)
	s.inputs[ssContactPhone].SetValue(st.ContactPhone)
	s.inputs[ssWebsiteURL].SetValue(st.WebsiteURL)
	s.inputs[ssDashboardTitle].SetValue(st.DashboardTitle)
	s.inputs[ssDashboardSubtitle].SetValue(st.DashboardSubtitle)
	s.inputs[ssPmAutoBundle].SetValue(strconv.Itoa(st.PmAutoBundleDueWithinDays))
	s.showLogo = st.ShowLogoOnDashboard
	s.removeLogo = false
}

func defaultIfBlank(v, def string) string {
	if strings.TrimSpace(v) == "" {
		return def
	}
	return v
}

// hasLogo reports whether a logo is currently set (drives the conditional
// Remove-logo row — a remove is meaningless when there's nothing to delete).
func (s *SiteSettingsFormScreen) hasLogo() bool {
	return s.settings != nil && s.settings.LogoURL != nil && strings.TrimSpace(*s.settings.LogoURL) != ""
}

func (s *SiteSettingsFormScreen) buildFields() {
	s.fields = []int{ssName, ssTagline, ssLogoAlt, ssLogoPath}
	if s.hasLogo() {
		s.fields = append(s.fields, ssRemoveLogo)
	}
	s.fields = append(s.fields,
		ssFaviconPath, ssPrimaryColor, ssSecondaryColor,
		ssFooterText, ssContactEmail, ssContactPhone, ssWebsiteURL,
		ssDashboardTitle, ssDashboardSubtitle, ssShowLogo, ssPmAutoBundle)
	if s.cursor >= len(s.fields) {
		s.cursor = len(s.fields) - 1
	}
}

func (s *SiteSettingsFormScreen) currentFieldID() (int, bool) {
	if s.cursor < 0 || s.cursor >= len(s.fields) {
		return 0, false
	}
	return s.fields[s.cursor], true
}

func (s *SiteSettingsFormScreen) syncFocus() {
	for id := 0; id < len(s.inputs); id++ {
		if ssIsTextKind(id) {
			s.inputs[id].Blur()
		}
	}
	if id, ok := s.currentFieldID(); ok && ssIsTextKind(id) {
		s.inputs[id].Focus()
	}
}

func (s *SiteSettingsFormScreen) updateFormPhase(m tea.KeyMsg) (Screen, tea.Cmd) {
	switch m.String() {
	case "esc":
		return s, s.cancelCmd()
	case "tab", "down":
		s.moveCursor(+1)
		return s, textinput.Blink
	case "shift+tab", "up":
		s.moveCursor(-1)
		return s, textinput.Blink
	case "enter":
		if s.saving {
			return s, nil
		}
		return s.submit()
	}

	id, ok := s.currentFieldID()
	if !ok {
		return s, nil
	}
	if ssKind(id) == akToggle {
		if m.String() == " " {
			switch id {
			case ssRemoveLogo:
				s.removeLogo = !s.removeLogo
			case ssShowLogo:
				s.showLogo = !s.showLogo
			}
		}
		return s, nil
	}
	var cmd tea.Cmd
	s.inputs[id], cmd = s.inputs[id].Update(m)
	return s, cmd
}

func (s *SiteSettingsFormScreen) moveCursor(delta int) {
	n := len(s.fields)
	if n == 0 {
		return
	}
	s.cursor = (s.cursor + delta + n) % n
	s.syncFocus()
}

func (s *SiteSettingsFormScreen) submit() (Screen, tea.Cmd) {
	body, err := s.buildPayload()
	if err != nil {
		s.errMsg = err.Error()
		return s, Status(err.Error(), StatusError)
	}
	s.saving = true
	s.errMsg = ""
	deps := s.deps
	ctx := s.ctx()
	return s, func() tea.Msg {
		st, e := deps.OMS.UpdateSiteSettings(ctx, body)
		return siteSettingsSavedMsg{settings: st, err: e}
	}
}

func (s *SiteSettingsFormScreen) buildPayload() (omsapi.SiteSettingsWrite, error) {
	var w omsapi.SiteSettingsWrite
	name := strings.TrimSpace(s.inputs[ssName].Value())
	if name == "" {
		return w, errors.New("site name is required")
	}
	primary := strings.TrimSpace(s.inputs[ssPrimaryColor].Value())
	if err := requireHexColor(primary, "primary color"); err != nil {
		return w, err
	}
	secondary := strings.TrimSpace(s.inputs[ssSecondaryColor].Value())
	if err := requireHexColor(secondary, "secondary color"); err != nil {
		return w, err
	}
	pm, err := parseSettingsCount(s.inputs[ssPmAutoBundle].Value())
	if err != nil {
		return w, fmt.Errorf("PM auto-bundle window %w", err)
	}
	logoPath, err := validateImagePath(s.inputs[ssLogoPath].Value(), "logo")
	if err != nil {
		return w, err
	}
	faviconPath, err := validateImagePath(s.inputs[ssFaviconPath].Value(), "favicon")
	if err != nil {
		return w, err
	}
	// A remove only fires when a logo exists and no replacement was picked
	// (an upload supersedes a remove).
	removeLogo := s.removeLogo && s.hasLogo() && logoPath == ""

	w = omsapi.SiteSettingsWrite{
		SiteName:                  name,
		SiteTagline:               strings.TrimSpace(s.inputs[ssTagline].Value()),
		LogoAltText:               strings.TrimSpace(s.inputs[ssLogoAlt].Value()),
		PrimaryColor:              primary,
		SecondaryColor:            secondary,
		FooterText:                strings.TrimSpace(s.inputs[ssFooterText].Value()),
		ContactEmail:              strings.TrimSpace(s.inputs[ssContactEmail].Value()),
		ContactPhone:              strings.TrimSpace(s.inputs[ssContactPhone].Value()),
		WebsiteURL:                strings.TrimSpace(s.inputs[ssWebsiteURL].Value()),
		DashboardTitle:            strings.TrimSpace(s.inputs[ssDashboardTitle].Value()),
		DashboardSubtitle:         strings.TrimSpace(s.inputs[ssDashboardSubtitle].Value()),
		ShowLogoOnDashboard:       s.showLogo,
		PmAutoBundleDueWithinDays: pm,
		LogoPath:                  logoPath,
		FaviconPath:               faviconPath,
		RemoveLogo:                removeLogo,
	}
	return w, nil
}

// requireHexColor rejects a blank color (the model columns are non-blank with a
// hex default) and otherwise defers to the shared #RGB / #RRGGBB validator.
func requireHexColor(v, label string) error {
	if strings.TrimSpace(v) == "" {
		return fmt.Errorf("%s is required (e.g. #007cba)", label)
	}
	if err := validateHexColor(v); err != nil {
		return fmt.Errorf("%s: %w", label, err)
	}
	return nil
}

// parseSettingsCount parses a non-negative whole number; blank means 0.
func parseSettingsCount(raw string) (int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return 0, errors.New("must be a whole number")
	}
	if n < 0 {
		return 0, errors.New("can't be negative")
	}
	return n, nil
}

// validateImagePath accepts a blank path (leave the image unchanged) or an
// absolute path to an existing file. The bytes are read later by the omsapi
// layer; this just fails fast with a clear message.
func validateImagePath(raw, label string) (string, error) {
	p := strings.TrimSpace(raw)
	if p == "" {
		return "", nil
	}
	if !filepath.IsAbs(p) {
		return "", fmt.Errorf("%s path must be absolute", label)
	}
	info, err := os.Stat(p)
	if err != nil {
		return "", fmt.Errorf("%s path: %w", label, err)
	}
	if info.IsDir() {
		return "", fmt.Errorf("%s path must be a file", label)
	}
	return p, nil
}

func (s *SiteSettingsFormScreen) cancelCmd() tea.Cmd {
	return SwitchTo(WSSettings, NewSettingsScreen(s.deps))
}

func (s *SiteSettingsFormScreen) View() string {
	if s.loading {
		return StyleMuted.Render("Loading…")
	}
	if s.loadErr != "" {
		return StyleStatusError.Render("Error: ") + s.loadErr + "\n\n" + StyleMuted.Render("esc to go back")
	}

	var b strings.Builder
	b.WriteString(StyleMuted.Render(s.helpText()) + "\n")
	b.WriteString(StyleMuted.Render("current logo: "+s.currentImageLabel(true)+"  ·  favicon: "+s.currentImageLabel(false)) + "\n\n")

	visible := s.visibleRows()
	start, end := fieldWindow(s.cursor, len(s.fields), visible)
	if start > 0 {
		b.WriteString(StyleMuted.Render(fmt.Sprintf("  ↑ %d more above", start)) + "\n")
	}
	for i := start; i < end; i++ {
		b.WriteString(s.renderField(i) + "\n")
	}
	if end < len(s.fields) {
		b.WriteString(StyleMuted.Render(fmt.Sprintf("  ↓ %d more below", len(s.fields)-end)) + "\n")
	}

	b.WriteString("\n")
	if s.saving {
		b.WriteString(StyleMuted.Render("Saving…"))
	} else if s.errMsg != "" {
		b.WriteString(StyleStatusError.Render("✗ " + s.errMsg))
	}
	return b.String()
}

func (s *SiteSettingsFormScreen) currentImageLabel(logo bool) string {
	if s.settings == nil {
		return "(none)"
	}
	url := s.settings.FaviconURL
	if logo {
		url = s.settings.LogoURL
	}
	if url == nil || strings.TrimSpace(*url) == "" {
		return "(none)"
	}
	return *url
}

func (s *SiteSettingsFormScreen) renderField(i int) string {
	id := s.fields[i]
	caret := "  "
	if i == s.cursor {
		caret = "▸ "
	}
	label := ssFieldLabel[id]
	var value string
	switch ssKind(id) {
	case akToggle:
		on := s.showLogo
		if id == ssRemoveLogo {
			on = s.removeLogo
		}
		value = elecToggleLabel(on)
	default:
		value = s.inputs[id].View()
	}
	return caret + StyleTitle.Render(label+": ") + value
}

func (s *SiteSettingsFormScreen) helpText() string {
	kindHelp := "type to edit"
	if id, ok := s.currentFieldID(); ok && ssKind(id) == akToggle {
		kindHelp = "space toggle"
	}
	return kindHelp + " · tab/↑↓ move · enter save · esc cancel"
}

func (s *SiteSettingsFormScreen) visibleRows() int {
	const chrome = 7
	avail := screenBodyHeight(s.terminalHeight) - chrome
	if avail < 3 {
		avail = 3
	}
	return avail
}
