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
//
// The sheet renders through the columnar "JD Edwards" layer (jde_form.go) as of
// sc-lmsi, sweep E of the sc-h412 redesign: right-aligned labels in one column,
// band headings over sixteen fields, what is currently stored for each image as
// a note under the row that replaces it, and a persistent action bar naming
// exactly the keys that apply.
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
	// "(days)" is the unit, not the name: at 28 columns the old label was past
	// jdeLabelMaxWidth and would have been TRUNCATED into the shared column
	// (sc-ye0i's parenthetical rule, with teeth).
	ssPmAutoBundle: "PM auto-bundle window",
}

// ssLabelWidth is this sheet's own label column. Site settings is a singleton
// opened from Settings and returns there — it is never on screen beside another
// converted sheet, so there is no family to share a column with (the storage
// batch's rule, sc-6qsk).
var ssLabelWidth = jdeLabelWidth(jdeLabelFields(ssFieldLabel))

// ssFieldHint carries the units, the formats and what is REQUIRED — everything
// the placeholders used to say from inside the input area, where they left no
// underscores and so made an empty row stop reading as empty (sc-dnhx). The two
// hex defaults are kept as placeholders instead: they show a DEFAULT, which is
// the one thing a placeholder is still good for.
var ssFieldHint = map[int]string{
	ssName:           "required",
	ssLogoPath:       "absolute path",
	ssFaviconPath:    "absolute path",
	ssRemoveLogo:     "deletes the stored logo",
	ssPrimaryColor:   "#RRGGBB",
	ssSecondaryColor: "#RRGGBB",
	ssWebsiteURL:     "https://…",
	ssPmAutoBundle:   "days · 0 disables",
}

// ssBandHeading starts a band at the field that leads it. Sixteen fields in one
// undivided run is a wall (sc-dnhx); the bands have to be CONTIGUOUS in
// buildFields order, which they are — the sheet already ran identity, then
// branding, then contact, then the dashboard.
var ssBandHeading = map[int]string{
	ssName:           "Site",
	ssLogoAlt:        "Branding",
	ssFooterText:     "Footer & contact",
	ssDashboardTitle: "Dashboard",
	ssPmAutoBundle:   "Maintenance",
}

// ssFieldWidth sizes the input areas that are not the default.
func ssFieldWidth(id int) int {
	switch id {
	case ssPrimaryColor, ssSecondaryColor:
		return 9
	case ssPmAutoBundle:
		return 6
	case ssLogoPath, ssFaviconPath:
		return 28
	case ssTagline, ssFooterText, ssDashboardSubtitle:
		return 40
	}
	return 30
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

	jdeScreen
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

// ssPlaceholder is down to the two colors: a placeholder earns its place only
// when it shows a DEFAULT the operator would otherwise have to know (sc-dnhx).
// Everything else moved to ssFieldHint, after the input area.
func ssPlaceholder(id int) string {
	switch id {
	case ssPrimaryColor:
		return "#007cba"
	case ssSecondaryColor:
		return "#417690"
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
		s.setSize(m)
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
	// The system keys, first and everywhere: they mean the same thing on every
	// row, which is the whole point of the reduced scheme (sc-h412).
	switch m.String() {
	case "esc":
		return s, s.cancelCmd()
	case "tab", "down":
		s.moveCursor(+1)
		return s, textinput.Blink
	case "shift+tab", "up":
		s.moveCursor(-1)
		return s, textinput.Blink
	case "pgdown":
		s.pageCursor(+1)
		return s, textinput.Blink
	case "pgup":
		s.pageCursor(-1)
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
		// A toggle is a two-value choice row, so it flips on the same ←/→ every
		// other bounded set takes (space stays as the pilot's synonym).
		switch m.String() {
		case " ", "left", "right":
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
	body := s.formLines()
	next, ok := s.moveRow(s.cursor, len(s.fields), delta, 0, s.formBar(body))
	if !ok {
		return
	}
	s.cursor = next
	s.syncFocus()
}

// pageCursor moves a whole pane's worth of rows, clamping where moveCursor
// wraps — a page is for covering ground, not for losing your place. This is the
// longest sheet in the batch, so it is the one that needs it.
func (s *SiteSettingsFormScreen) pageCursor(dir int) {
	body := s.formLines()
	next, ok := s.pageRow(body, s.cursor, len(s.fields), dir, 0,
		s.formBar(body), s.formBarItems(true))
	if !ok {
		return
	}
	s.cursor = next
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

	body := s.formLines()
	return s.frame(body, s.cursor, s.statusRow(s.saving, "Saving…", s.errMsg), s.formBar(body))
}

// formFields describes the sheet as columnar rows: two toggles as bounded sets,
// and the rest — including the two image PATHS and the two hex colors — typed
// into.
func (s *SiteSettingsFormScreen) formFields() []jdeField {
	out := make([]jdeField, len(s.fields))
	for i, id := range s.fields {
		f := jdeField{
			Label:   ssFieldLabel[id],
			Width:   ssFieldWidth(id),
			Hint:    ssFieldHint[id],
			Focused: i == s.cursor,
		}
		if ssKind(id) == akToggle {
			f.Kind, f.Value = jdeChoice, jdeYesNo(s.toggleState(id))
		} else {
			f.Kind, f.Input = jdeText, &s.inputs[id]
			if id == ssPrimaryColor || id == ssSecondaryColor {
				// Same treatment as the category sheet's Color: one shared
				// helper, live off the input. A blank row shows no sample —
				// its placeholder is naming a default it has not taken yet.
				f = jdeColorRow(f, s.inputs[id].Value())
			}
		}
		out[i] = f
	}
	return out
}

// toggleState is a bool row's current value.
func (s *SiteSettingsFormScreen) toggleState(id int) bool {
	if id == ssRemoveLogo {
		return s.removeLogo
	}
	return s.showLogo
}

func (s *SiteSettingsFormScreen) formLines() *jdeLines {
	fields := s.formFields()
	l := &jdeLines{}
	for i, id := range s.fields {
		if heading, ok := ssBandHeading[id]; ok {
			if i > 0 {
				l.Add("")
			}
			l.Add(StyleJDEHeading.Render(heading))
		}
		l.AddRow(i, renderJDEField(fields[i], ssLabelWidth, s.bodyWidth()))
		// What is on file for an image belongs UNDER the row that replaces it —
		// it was a header line above the whole sheet, which is the one place an
		// operator deciding whether to remove the logo would not look (sc-6qsk).
		if note := s.imageNote(id); note != "" {
			for _, line := range jdeNoteLines(note, ssLabelWidth, s.bodyWidth()) {
				l.AddRow(i, line)
			}
		}
	}
	return l
}

// imageNote is what is currently stored for an image row, or "" for every other
// row. It leads with what a BLANK path does, because that is the question an
// operator looking at an empty upload field is actually asking — and it is the
// half a long URL's wrap can never push off the end.
func (s *SiteSettingsFormScreen) imageNote(id int) string {
	switch id {
	case ssLogoPath:
		if !s.hasLogo() {
			return "no logo set yet"
		}
		return "blank keeps the current logo: " + s.currentImageLabel(true)
	case ssFaviconPath:
		if s.settings == nil || s.settings.FaviconURL == nil || strings.TrimSpace(*s.settings.FaviconURL) == "" {
			return "no favicon set yet"
		}
		return "blank keeps the current favicon: " + s.currentImageLabel(false)
	}
	return ""
}

// formBar names the keys that work on the form, with PgUp/PgDn on it exactly
// when the body moves under the bar that is about to be drawn.
//
// The paging claim is measured against formBarItems(true) — the bar WITH the
// pair on it — because naming them costs cells, cells fold the bar onto another
// row, and a folded bar leaves the body one row fewer. The tallest bar is the
// fixed point, so the answer cannot oscillate between frames.
func (s *SiteSettingsFormScreen) formBar(body *jdeLines) []actionBarItem {
	return s.formBarItems(s.bodyScrollsForBar(body, 0, s.formBarItems(true)))
}

// formBarItems is formBar for a given paging state, so the bar that is
// MEASURED is the bar that is drawn.
//
// It names the keys that apply where the cursor is standing — and only those,
// so the bar never teaches a key that does nothing here. Nothing on this sheet
// OPENS, so Ctrl-E is never offered.
func (s *SiteSettingsFormScreen) formBarItems(paging bool) []actionBarItem {
	items := []actionBarItem{{"Enter", "Save"}, {"Esc", "Cancel"}, {"UP/DN", "Fields"}}
	if id, ok := s.currentFieldID(); ok && ssKind(id) == akToggle {
		items = append(items, actionBarItem{"←→", "Change"})
	}
	if paging {
		items = append(items, actionBarItem{"PgUp/PgDn", "Page"})
	}
	return items
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
