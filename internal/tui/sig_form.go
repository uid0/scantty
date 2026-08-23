// SIG (Special Interest Group) CRUD — list + create/edit form + member drill-in.
//
// SIGFormScreen renders through the columnar "JD Edwards" layer (jde_form.go)
// as of sc-lmsi, sweep E of the sc-h412 redesign: right-aligned labels in one
// column, a persistent action bar naming exactly the keys that apply, and no
// letter accelerators. The list below is a browse surface, not a form, and is
// untouched by that sweep.
//
// TUI counterpart to the web SIGDashboard create modal (+ the SIG list). A SIG
// *is* a Django auth Group; the backend SIGCreateSerializer exposes exactly two
// writable fields — name (required, unique) and group_email (optional, stored on
// the SIGProfile). There is deliberately no description/color/lead/is_active
// field: the serializer defines none, so the form ships the FULL writable set,
// not a subset ([[ship-complete-features]]).
//
// SIGListScreen replaces the plain browse list at the SIGs nav workspace ('7')
// with a create/edit/delete surface, mirroring the CategoryList / electrical
// drill-in idiom: n new, E edit, x delete (y/n confirm), enter drills into the
// member-management screen, v opens the read-only detail. n and G collide with
// global hotkeys so the screen claims them via LocalKeyScreen; WantsRawInput is
// asserted only while the delete confirm is up so y/n land here. SIG create /
// update / delete are staff-gated server-side — a non-staff operator sees the
// list but 403s on save/delete, surfaced as a clear status message.
package tui

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/uid0/scantty/internal/omsapi"
)

// ===========================================================================
// SIGFormScreen
// ===========================================================================

const (
	sigfName = iota
	sigfGroupEmail
	sigfFieldMax
)

var sigFieldLabel = map[int]string{
	sigfName:       "Name",
	sigfGroupEmail: "Group email",
}

// sigLabelWidth is this sheet's own label column. The SIG form is opened from
// the SIG list and returns to it — it is never on screen beside another
// converted sheet, so there is no family here to share a column with (the
// storage batch's rule, sc-6qsk).
var sigLabelWidth = jdeLabelWidth(jdeLabelFields(sigFieldLabel))

// sigFieldHint is what the placeholders used to say. "SIG name" was a
// placeholder that showed no DEFAULT, so it only filled the input area and hid
// the underscores that say a field is empty (sc-dnhx); what is worth keeping is
// which field is required and the shape of the other.
var sigFieldHint = map[int]string{
	sigfName:       "required",
	sigfGroupEmail: "e.g. sig@example.org",
}

// sigGroupEmailNote is what the old footer under the sheet said. It is about
// ONE field, so it belongs under that field rather than under the form
// (sc-ye0i) — and it is far too long to ride as a Hint, so it wraps as a note.
const sigGroupEmailNote = "New reorder requests for this SIG are emailed here; blank notifies admins individually."

func sigFieldWidth(id int) int {
	if id == sigfGroupEmail {
		return 34
	}
	return 30
}

type SIGFormScreen struct {
	deps  Deps
	edit  bool
	sigID int

	loading bool
	loadErr string
	saving  bool
	errMsg  string

	sig *omsapi.SIG

	jdeScreen

	inputs []textinput.Model
	fields []int
	cursor int
}

type sigFormLoadedMsg struct {
	sig *omsapi.SIG
	err error
}

type sigSavedMsg struct {
	sig *omsapi.SIG
	err error
}

// NewSIGFormScreen opens create mode when sigID is empty, otherwise edit mode
// (hydrating from the fetched SIG). sigID is a string to match the other form
// constructors; it is parsed to the int pk the membership API expects.
func NewSIGFormScreen(deps Deps, sigID string) *SIGFormScreen {
	id, _ := strconv.Atoi(strings.TrimSpace(sigID))
	edit := strings.TrimSpace(sigID) != "" && id > 0
	s := &SIGFormScreen{
		deps:    deps,
		edit:    edit,
		sigID:   id,
		loading: edit, // create mode has nothing to load; render immediately
	}
	s.inputs = make([]textinput.Model, sigfFieldMax)
	for id := 0; id < sigfFieldMax; id++ {
		ti := textinput.New()
		ti.Prompt = ""
		ti.CharLimit = sigCharLimit(id)
		s.inputs[id] = ti
	}
	s.fields = []int{sigfName, sigfGroupEmail}
	s.syncFocus()
	return s
}

func sigCharLimit(id int) int {
	switch id {
	case sigfName:
		return 150 // Django Group.name max_length
	case sigfGroupEmail:
		return 254 // EmailField default max_length
	}
	return 150
}

func (s *SIGFormScreen) Title() string {
	if s.edit {
		if s.sig != nil && s.sig.Name != "" {
			return "Edit SIG: " + s.sig.Name
		}
		return "Edit SIG"
	}
	return "New SIG"
}

func (s *SIGFormScreen) WantsRawInput() bool { return true }

func (s *SIGFormScreen) Init() tea.Cmd {
	cmds := []tea.Cmd{textinput.Blink}
	if s.edit {
		cmds = append(cmds, s.loadSIG())
	}
	return tea.Batch(cmds...)
}

func (s *SIGFormScreen) ctx() context.Context {
	if s.deps.Ctx != nil {
		return s.deps.Ctx
	}
	return context.Background()
}

func (s *SIGFormScreen) loadSIG() tea.Cmd {
	deps := s.deps
	ctx := s.ctx()
	id := s.sigID
	return func() tea.Msg {
		sig, err := deps.OMS.GetSIG(ctx, id)
		return sigFormLoadedMsg{sig: sig, err: err}
	}
}

func (s *SIGFormScreen) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		s.setSize(m)
		return s, nil
	case sigFormLoadedMsg:
		s.loading = false
		if m.err != nil {
			s.loadErr = m.err.Error()
		} else {
			s.sig = m.sig
			s.hydrate()
		}
		s.syncFocus()
		return s, nil
	case sigSavedMsg:
		s.saving = false
		if m.err != nil {
			s.errMsg = m.err.Error()
			return s, Status("save failed: "+m.err.Error(), StatusError)
		}
		verb := "created"
		if s.edit {
			verb = "updated"
		}
		name := ""
		if m.sig != nil {
			name = m.sig.Name
		}
		return s, tea.Batch(
			Status(fmt.Sprintf("SIG %s: %s", verb, name), StatusOK),
			SwitchTo(WSSIGs, NewSIGListScreen(s.deps)),
		)
	case tea.KeyMsg:
		if s.loading {
			if m.String() == "esc" {
				return s, s.cancelCmd()
			}
			return s, nil
		}
		return s.updateForm(m)
	}

	if id, ok := s.currentFieldID(); ok {
		var cmd tea.Cmd
		s.inputs[id], cmd = s.inputs[id].Update(msg)
		return s, cmd
	}
	return s, nil
}

func (s *SIGFormScreen) hydrate() {
	if s.sig == nil {
		return
	}
	s.inputs[sigfName].SetValue(s.sig.Name)
	s.inputs[sigfGroupEmail].SetValue(s.sig.GroupEmail)
}

func (s *SIGFormScreen) currentFieldID() (int, bool) {
	if s.cursor < 0 || s.cursor >= len(s.fields) {
		return 0, false
	}
	return s.fields[s.cursor], true
}

func (s *SIGFormScreen) syncFocus() {
	for id := 0; id < len(s.inputs); id++ {
		s.inputs[id].Blur()
	}
	if id, ok := s.currentFieldID(); ok {
		s.inputs[id].Focus()
	}
}

func (s *SIGFormScreen) updateForm(m tea.KeyMsg) (Screen, tea.Cmd) {
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
	var cmd tea.Cmd
	s.inputs[id], cmd = s.inputs[id].Update(m)
	return s, cmd
}

func (s *SIGFormScreen) moveCursor(delta int) {
	n := len(s.fields)
	if n == 0 {
		return
	}
	s.cursor = (s.cursor + delta + n) % n
	s.syncFocus()
}

// pageCursor moves a whole pane's worth of rows, clamping where moveCursor
// wraps — a page is for covering ground, not for losing your place.
func (s *SIGFormScreen) pageCursor(dir int) {
	if len(s.fields) == 0 {
		return
	}
	s.cursor = jdePageCursor(s.cursor, len(s.fields), s.windowRows(s.formLines(), s.cursor, 0), dir)
	s.syncFocus()
}

func (s *SIGFormScreen) submit() (Screen, tea.Cmd) {
	body, err := s.buildPayload()
	if err != nil {
		s.errMsg = err.Error()
		return s, Status(err.Error(), StatusError)
	}
	s.saving = true
	s.errMsg = ""
	deps := s.deps
	ctx := s.ctx()
	edit := s.edit
	id := s.sigID
	return s, func() tea.Msg {
		var sig *omsapi.SIG
		var e error
		if edit {
			sig, e = deps.OMS.UpdateSIG(ctx, id, body)
		} else {
			sig, e = deps.OMS.CreateSIG(ctx, body)
		}
		return sigSavedMsg{sig: sig, err: e}
	}
}

func (s *SIGFormScreen) buildPayload() (omsapi.SIGWrite, error) {
	var w omsapi.SIGWrite
	name := strings.TrimSpace(s.inputs[sigfName].Value())
	if name == "" {
		return w, errors.New("name is required")
	}
	email := strings.TrimSpace(s.inputs[sigfGroupEmail].Value())
	if err := validateOptionalEmail(email); err != nil {
		return w, err
	}
	w = omsapi.SIGWrite{Name: name, GroupEmail: email}
	return w, nil
}

// validateOptionalEmail accepts an empty string (no contact email) or an
// address with a non-empty local part, an '@', a non-empty domain and no
// whitespace. It is intentionally looser than Django's EmailValidator so it
// never rejects an address the backend would accept — the server stays the
// authority and any 400 is surfaced; this only catches gross typos before the
// round-trip.
func validateOptionalEmail(v string) error {
	if v == "" {
		return nil
	}
	at := strings.Index(v, "@")
	if at <= 0 || at >= len(v)-1 || strings.ContainsAny(v, " \t\r\n") {
		return errors.New("group email must be a valid address like sig@example.org")
	}
	return nil
}

func (s *SIGFormScreen) cancelCmd() tea.Cmd {
	return SwitchTo(WSSIGs, NewSIGListScreen(s.deps))
}

func (s *SIGFormScreen) View() string {
	if s.loading {
		return StyleMuted.Render("Loading…")
	}
	if s.loadErr != "" {
		return StyleStatusError.Render("Error: ") + s.loadErr + "\n\n" + StyleMuted.Render("esc to go back")
	}
	body := s.formLines()
	return s.frame(body, s.cursor, jdeStatusLine(s.saving, "Saving…", s.errMsg), s.formBar(body))
}

// formFields describes the sheet as columnar rows. A SIG has exactly two
// writable fields, so every row here is typed into.
func (s *SIGFormScreen) formFields() []jdeField {
	out := make([]jdeField, len(s.fields))
	for i, id := range s.fields {
		focused := i == s.cursor
		out[i] = jdeField{
			Label:   sigFieldLabel[id],
			Kind:    jdeText,
			Input:   &s.inputs[id],
			Width:   sigFieldWidth(id),
			Hint:    sigFieldHint[id],
			Focused: focused,
		}
	}
	return out
}

func (s *SIGFormScreen) formLines() *jdeLines {
	fields := s.formFields()
	l := &jdeLines{}
	l.Add(StyleJDEHeading.Render("SIG"))
	for i, id := range s.fields {
		l.AddRow(i, renderJDEField(fields[i], sigLabelWidth, s.bodyWidth()))
		if id == sigfGroupEmail {
			// Tagged with the row it is about, so the window keeps the note and
			// the field it explains on screen together.
			for _, line := range jdeNoteLines(sigGroupEmailNote, sigLabelWidth, s.bodyWidth()) {
				l.AddRow(i, line)
			}
		}
	}
	return l
}

// formBar names the keys that apply where the cursor is standing — and only
// those, so the bar never teaches a key that does nothing here. Every row of
// this sheet is text, so there is never anything for ←→ or Ctrl-E to do.
func (s *SIGFormScreen) formBar(body *jdeLines) []actionBarItem {
	items := []actionBarItem{{"Enter", "Save"}, {"Esc", "Cancel"}, {"UP/DN", "Fields"}}
	if avail := s.bodyRows(); avail > 0 && body.Len() > avail {
		items = append(items, actionBarItem{"PgUp/PgDn", "Page"})
	}
	return items
}

// ===========================================================================
// SIGListScreen
// ===========================================================================

type SIGListScreen struct {
	deps           Deps
	rows           []omsapi.SIG
	cursor         int
	windowStart    int
	windowSize     int
	loading        bool
	loadErr        string
	terminalHeight int

	confirmingDelete bool
	deleting         bool
}

type sigListLoadedMsg struct {
	rows []omsapi.SIG
	err  error
}

type sigDeletedMsg struct {
	err error
}

func NewSIGListScreen(deps Deps) *SIGListScreen {
	return &SIGListScreen{deps: deps, loading: true, windowSize: 18}
}

func (s *SIGListScreen) Title() string { return "SIGs" }

// WantsRawInput claims every key only while the delete confirm is up (y/n/esc).
func (s *SIGListScreen) WantsRawInput() bool { return s.confirmingDelete }

// HandlesKey claims the two action keys that collide with global hotkeys (n new,
// G bottom-of-list) so they reach this screen instead of the global nav switch.
// E / x / v / enter are not globals, so they arrive via the root's fall-through.
func (s *SIGListScreen) HandlesKey(key string) bool {
	return key == "n" || key == "G"
}

func (s *SIGListScreen) Init() tea.Cmd {
	deps := s.deps
	ctx := deps.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return func() tea.Msg {
		page, err := deps.OMS.ListSIGs(ctx, nil)
		if err != nil {
			return sigListLoadedMsg{err: err}
		}
		return sigListLoadedMsg{rows: page.Results}
	}
}

func (s *SIGListScreen) computeWindowSize() int {
	const chrome = 4
	avail := screenBodyHeight(s.terminalHeight) - chrome
	if avail < 3 {
		avail = 3
	}
	return avail
}

func (s *SIGListScreen) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		s.terminalHeight = m.Height
		s.windowSize = s.computeWindowSize()
		s.scrollIntoView()
		return s, nil
	case sigListLoadedMsg:
		s.loading = false
		if m.err != nil {
			s.loadErr = m.err.Error()
		} else {
			s.loadErr = ""
			s.rows = m.rows
		}
		if s.cursor >= len(s.rows) {
			s.cursor = 0
		}
		s.windowSize = s.computeWindowSize()
		s.scrollIntoView()
		return s, nil
	case sigDeletedMsg:
		s.deleting = false
		s.confirmingDelete = false
		if m.err != nil {
			return s, Status("delete failed: "+m.err.Error(), StatusError)
		}
		s.loading = true
		return s, tea.Batch(Status("SIG deleted", StatusOK), s.Init())
	case tea.KeyMsg:
		if s.confirmingDelete {
			return s.updateConfirmDelete(m)
		}
		switch m.String() {
		case "j", "down":
			if s.cursor < len(s.rows)-1 {
				s.cursor++
				s.scrollIntoView()
			}
		case "k", "up":
			if s.cursor > 0 {
				s.cursor--
				s.scrollIntoView()
			}
		case "ctrl+d", "pgdown":
			s.cursor += s.windowSize
			if s.cursor >= len(s.rows) {
				s.cursor = len(s.rows) - 1
			}
			s.scrollIntoView()
		case "ctrl+u", "pgup":
			s.cursor -= s.windowSize
			if s.cursor < 0 {
				s.cursor = 0
			}
			s.scrollIntoView()
		case "g", "home":
			s.cursor = 0
			s.scrollIntoView()
		case "G", "end":
			s.cursor = len(s.rows) - 1
			if s.cursor < 0 {
				s.cursor = 0
			}
			s.scrollIntoView()
		case "r":
			s.loading = true
			s.loadErr = ""
			return s, s.Init()
		case "n":
			return s, SwitchTo(WSSIGs, NewSIGFormScreen(s.deps, ""))
		case "E":
			if row, ok := s.selected(); ok {
				return s, SwitchTo(WSSIGs, NewSIGFormScreen(s.deps, strconv.Itoa(row.ID)))
			}
		case "enter":
			// Drill into member management (the actionable child surface),
			// mirroring the electrical list→children convention.
			if row, ok := s.selected(); ok {
				return s, SwitchTo(WSSIGs, NewSIGMembersScreen(s.deps, row.ID, row.Name))
			}
		case "v":
			// View the read-only overview (counts / admins / members).
			if row, ok := s.selected(); ok {
				return s, SwitchTo(WSSIGs, NewSIGDetailScreen(s.deps, strconv.Itoa(row.ID)))
			}
		case "x":
			if _, ok := s.selected(); ok {
				s.confirmingDelete = true
			}
		}
	}
	return s, nil
}

func (s *SIGListScreen) updateConfirmDelete(m tea.KeyMsg) (Screen, tea.Cmd) {
	if s.deleting {
		return s, nil
	}
	switch m.String() {
	case "y", "Y":
		row, ok := s.selected()
		if !ok {
			s.confirmingDelete = false
			return s, nil
		}
		s.deleting = true
		deps := s.deps
		ctx := deps.Ctx
		if ctx == nil {
			ctx = context.Background()
		}
		id := row.ID
		return s, func() tea.Msg {
			return sigDeletedMsg{err: deps.OMS.DeleteSIG(ctx, id)}
		}
	case "n", "N", "esc":
		s.confirmingDelete = false
	}
	return s, nil
}

func (s *SIGListScreen) selected() (omsapi.SIG, bool) {
	if s.cursor < 0 || s.cursor >= len(s.rows) {
		return omsapi.SIG{}, false
	}
	return s.rows[s.cursor], true
}

func (s *SIGListScreen) scrollIntoView() {
	if s.windowSize <= 0 {
		s.windowSize = 18
	}
	if s.cursor < s.windowStart {
		s.windowStart = s.cursor
	}
	if s.cursor >= s.windowStart+s.windowSize {
		s.windowStart = s.cursor - s.windowSize + 1
	}
	if s.windowStart < 0 {
		s.windowStart = 0
	}
	if len(s.rows) <= s.windowSize {
		s.windowStart = 0
	}
}

func (s *SIGListScreen) View() string {
	if s.loading {
		return StyleMuted.Render("Loading SIGs…")
	}
	if s.loadErr != "" {
		return StyleStatusError.Render("Error: ") + s.loadErr + "\n\n" + StyleMuted.Render("r retry · n new · esc back")
	}
	if s.confirmingDelete {
		return s.viewConfirm()
	}
	if len(s.rows) == 0 {
		return StyleMuted.Render("No SIGs.") + "\n\n" + StyleMuted.Render("n new SIG · esc back")
	}

	var b strings.Builder
	b.WriteString(StyleMuted.Render(fmt.Sprintf("%d SIGs", len(s.rows))) + "\n")
	if s.windowStart > 0 {
		b.WriteString(StyleMuted.Render("  ↑ more above") + "\n")
	}
	end := s.windowStart + s.windowSize
	if end > len(s.rows) {
		end = len(s.rows)
	}
	for i := s.windowStart; i < end; i++ {
		b.WriteString(s.renderRow(i) + "\n")
	}
	if end < len(s.rows) {
		b.WriteString(StyleMuted.Render(fmt.Sprintf("  ↓ %d more below", len(s.rows)-end)) + "\n")
	}
	b.WriteString("\n")
	b.WriteString(StyleMuted.Render("j/k move · n new · E edit · enter members · v view · x delete · r · esc"))
	return b.String()
}

func (s *SIGListScreen) viewConfirm() string {
	row, ok := s.selected()
	if !ok {
		return ""
	}
	if s.deleting {
		return StyleMuted.Render("Deleting…")
	}
	// Heads-up on what the delete unlinks: members drop, owned assets/items
	// fall back to space-owned (owning_group is SET_NULL, so nothing 409s).
	impact := []string{}
	if row.MemberCount > 0 {
		impact = append(impact, fmt.Sprintf("%d members", row.MemberCount))
	}
	if row.AssetCount > 0 {
		impact = append(impact, fmt.Sprintf("%d assets", row.AssetCount))
	}
	if row.InventoryCount > 0 {
		impact = append(impact, fmt.Sprintf("%d items", row.InventoryCount))
	}
	warn := ""
	if len(impact) > 0 {
		warn = StyleStatusWarn.Render("  (" + strings.Join(impact, " · ") + " will be unlinked)")
	}
	return StyleStatusWarn.Render(fmt.Sprintf("Delete SIG %q? This can't be undone.  y delete · n/esc cancel", row.Name)) + warn
}

func (s *SIGListScreen) renderRow(i int) string {
	sig := s.rows[i]
	marker := "  "
	if i == s.cursor {
		marker = "▸ "
	}
	line := marker + sig.Name
	if i == s.cursor {
		line = StyleSidebarItemActive.Render(line)
	}
	meta := []string{}
	if sig.GroupEmail != "" {
		meta = append(meta, sig.GroupEmail)
	}
	if sig.MemberCount > 0 {
		meta = append(meta, fmt.Sprintf("%d members", sig.MemberCount))
	}
	if sig.AssetCount > 0 {
		meta = append(meta, fmt.Sprintf("%d assets", sig.AssetCount))
	}
	if sig.InventoryCount > 0 {
		meta = append(meta, fmt.Sprintf("%d items", sig.InventoryCount))
	}
	if sig.IsUserAdmin {
		meta = append(meta, "you admin")
	}
	if len(meta) > 0 {
		line += " " + StyleMuted.Render("("+strings.Join(meta, " · ")+")")
	}
	return line
}
