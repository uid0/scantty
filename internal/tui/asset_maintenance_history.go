// AssetMaintenanceHistoryScreen — what has been DONE to a machine, including the
// work nobody logged at the time.
//
// TUI counterpart to the web asset page's Maintenance History section
// (MaintenanceHistorySection.tsx). It reads `assets/<id>/maintenance-history/`,
// which merges two sources the server keeps apart:
//
//	Logged work     MaintenanceRecords — the backdated log a staff member types
//	                in from an invoice or a paper book (`historical`)
//	Outsourced WO   CLOSED third-party work orders (`workorder`)
//
// ONLY A LOGGED ROW IS A RECORD, and only a logged row is editable. A
// work-order row's id is a work order's, so PATCHing it as a record would be a
// 404 at best; the web offers its edit only where `source` is historical, and so
// does Ctrl-E here (historyEditOffered).
//
// A RECORD IS A LOG ENTRY AND NOTHING ELSE. Creating one does not move any PM
// item's last-completed date, does not create a work order and does not feed the
// maintenance dashboard's costs — it appears here and nowhere else. The form says
// so, because "I logged the belt change" is exactly the moment somebody expects
// the PM schedule to reset.
//
// WHO MAY WRITE is the WEB's rule, not the server's. The server lets staff,
// Logistics and any SIG admin create and edit records; the web shows the
// controls to STAFF only, and this sheet mirrors the web (Deps.InitialStaff, the
// same mirror auth_lockout.go's grant makes). A non-staff operator sees the
// history and no key that writes.
//
// WHAT THE WEB EDITS IS WHAT THIS EDITS: the notes. The API would take a new
// date, cost or vendor on the same PATCH, and the web deliberately does not offer
// that — so the PATCH here names `notes` alone, which also keeps an edit from a
// stale row from rewriting a figure somebody else corrected. Attachments (a file
// on create and on edit, in the web) are NOT built: no terminal flow uploads a
// file to a record, and the list says when a row carries one.
//
// THE WEB'S "INTERNAL STAFF" PATH IS BROKEN AND THIS ONE IS NOT. The web looks
// the username up at `/api/auth/users/`, a route OMS does not serve, swallows the
// 404 and posts a null performer — which the server refuses. This form resolves
// the username against the staff user directory (`/api/membership/users/`) and
// requires an EXACT match, so a name that matches nobody is refused on the frame
// where it was typed rather than arriving at the server as nobody.
//
// Keys:
//
//	Enter    log historical work (staff)
//	Ctrl-E   edit the highlighted logged row's notes (staff)
//	Ctrl-F   filter by date range and source
//	UP/DN move · PgUp/PgDn page · r refresh · Esc back
package tui

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/uid0/scantty/internal/omsapi"
)

type histPhase int

const (
	histPhaseList histPhase = iota
	histPhaseFilter
	histPhaseCreate
	histPhaseEditNotes
	histPhaseCount
)

// The create form's rows. Which of them are ON the form depends on the
// performer choice (createRows), so these are ids and never positions.
const (
	histFieldTitle = iota
	histFieldDesc
	histFieldDate
	histFieldPerformer
	histFieldVendor
	histFieldUsername
	histFieldCost
	histFieldInvoice
	histFieldNotes
	histFieldCount
)

// The filter form's rows.
const (
	histFilterSince = iota
	histFilterUntil
	histFilterSource
	histFilterCount
)

// histSources is the web's source chips, in its order and with its words.
var histSources = []struct{ value, label string }{
	{omsapi.MaintenanceSourceAll, "All"},
	{omsapi.MaintenanceSourceWorkOrder, "Outsourced WO"},
	{omsapi.MaintenanceSourceHistorical, "Logged work"},
}

func histSourceLabel(source string) string {
	for _, s := range histSources {
		if s.value == source {
			return s.label
		}
	}
	return source
}

type AssetMaintenanceHistoryScreen struct {
	deps      Deps
	assetID   string
	assetName string
	jdeScreen

	history *omsapi.MaintenanceHistory
	query   omsapi.MaintenanceHistoryQuery
	loading bool
	loadErr string
	loadSeq int
	cursor  int
	phase   histPhase
	// note is the answer to the last write, on the list's status row.
	note string

	// Filter form.
	filterInputs [histFilterCount]textinput.Model
	filterSource int
	filterFocus  int
	filterErr    string
	// filtering is a load out on the FILTER form's behalf: it answers on that
	// form, so a refused date is shown where it was typed and nothing is lost.
	filtering bool

	// Create form.
	inputs         [histFieldCount]textinput.Model
	createFocus    int // an index into createRows()
	internal       bool
	vendors        []omsapi.Vendor
	vendorIdx      int
	vendorsLoading bool
	vendorsErr     string
	saving         bool
	errMsg         string

	// Edit-notes form.
	editID    string
	editTitle string
	editDate  string
	editNotes textinput.Model

	now func() time.Time
}

type histLoadedMsg struct {
	history *omsapi.MaintenanceHistory
	query   omsapi.MaintenanceHistoryQuery
	err     error
	seq     int
}

type histVendorsMsg struct {
	vendors []omsapi.Vendor
	err     error
}

type histSavedMsg struct {
	err  error
	edit bool
}

func NewAssetMaintenanceHistoryScreen(deps Deps, assetID, assetName string) *AssetMaintenanceHistoryScreen {
	s := &AssetMaintenanceHistoryScreen{deps: deps, assetID: assetID, assetName: assetName, loading: true, now: time.Now}
	for i := range s.filterInputs {
		in := textinput.New()
		in.Prompt, in.CharLimit = "", 10
		s.filterInputs[i] = in
	}
	limits := map[int]int{
		histFieldTitle: 200, histFieldDesc: 2000, histFieldDate: 10, histFieldUsername: 150,
		histFieldCost: 14, histFieldInvoice: 64, histFieldNotes: 2000,
	}
	for i := range s.inputs {
		in := textinput.New()
		in.Prompt, in.CharLimit = "", limits[i]
		s.inputs[i] = in
	}
	s.editNotes = textinput.New()
	s.editNotes.Prompt, s.editNotes.CharLimit = "", 2000
	s.query = s.defaultQuery()
	return s
}

// defaultQuery is the web's default range: the last three years up to today,
// every source.
func (s *AssetMaintenanceHistoryScreen) defaultQuery() omsapi.MaintenanceHistoryQuery {
	today := s.today()
	return omsapi.MaintenanceHistoryQuery{
		Since:  today.AddDate(-3, 0, 0).Format("2006-01-02"),
		Until:  today.Format("2006-01-02"),
		Source: omsapi.MaintenanceSourceAll,
	}
}

// today is the SERVER's today: OMS compares `completed_on` against its own
// `timezone.localdate()` with TIME_ZONE UTC, and the web's defaults are UTC
// dates too. An operator east of Greenwich logging work just after their own
// midnight would otherwise be offered tomorrow's UTC date — and told it is in the
// future.
func (s *AssetMaintenanceHistoryScreen) today() time.Time {
	n := s.now().UTC()
	return time.Date(n.Year(), n.Month(), n.Day(), 0, 0, 0, 0, time.UTC)
}

func (s *AssetMaintenanceHistoryScreen) Title() string {
	if s.assetName != "" {
		return "Maintenance history · " + s.assetName
	}
	return "Maintenance history"
}

// WantsRawInput claims the keyboard on the three forms, so their boxes take
// letters and Esc cancels the form rather than leaving the sheet.
func (s *AssetMaintenanceHistoryScreen) WantsRawInput() bool { return s.phase != histPhaseList }

func (s *AssetMaintenanceHistoryScreen) Init() tea.Cmd { return s.load(s.query) }

func (s *AssetMaintenanceHistoryScreen) ctx() context.Context {
	if s.deps.Ctx != nil {
		return s.deps.Ctx
	}
	return context.Background()
}

func (s *AssetMaintenanceHistoryScreen) load(q omsapi.MaintenanceHistoryQuery) tea.Cmd {
	s.loadSeq++
	seq, deps, ctx, id := s.loadSeq, s.deps, s.ctx(), s.assetID
	return func() tea.Msg {
		h, err := deps.OMS.GetAssetMaintenanceHistory(ctx, id, q)
		return histLoadedMsg{history: h, query: q, err: err, seq: seq}
	}
}

// loadVendors walks EVERY page of the vendor directory. The web reads page one,
// so a shop with more than fifty vendors cannot log work by the fifty-first; a
// choice row that cannot reach a vendor is a dead end with no word on it.
func (s *AssetMaintenanceHistoryScreen) loadVendors() tea.Cmd {
	s.vendorsLoading, s.vendorsErr = true, ""
	deps, ctx := s.deps, s.ctx()
	return func() tea.Msg {
		all, err := deps.OMS.ListAllVendors(ctx, nil)
		if err != nil {
			return histVendorsMsg{err: err}
		}
		return histVendorsMsg{vendors: all}
	}
}

func (s *AssetMaintenanceHistoryScreen) rows() []omsapi.MaintenanceHistoryEntry {
	if s.history == nil {
		return nil
	}
	return s.history.Results
}

func (s *AssetMaintenanceHistoryScreen) addressed() (omsapi.MaintenanceHistoryEntry, bool) {
	rows := s.rows()
	if s.cursor < 0 || s.cursor >= len(rows) {
		return omsapi.MaintenanceHistoryEntry{}, false
	}
	return rows[s.cursor], true
}

// staff is the web section's canManage flag.
func (s *AssetMaintenanceHistoryScreen) staff() bool { return s.deps.InitialStaff }

// createOffered and historyEditOffered are the ONE expression behind Enter and
// Ctrl-E on the list: the bar names each exactly where it holds, and the arm
// acts exactly there.
func (s *AssetMaintenanceHistoryScreen) createOffered() bool { return s.staff() }

func (s *AssetMaintenanceHistoryScreen) historyEditOffered() bool {
	row, ok := s.addressed()
	return s.staff() && !s.loading && ok && row.Source == omsapi.MaintenanceSourceHistorical
}

func (s *AssetMaintenanceHistoryScreen) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		s.setSize(m)
		return s, nil

	case histLoadedMsg:
		if m.seq != s.loadSeq {
			return s, nil
		}
		s.loading = false
		wasFiltering := s.filtering
		s.filtering = false
		if m.err != nil {
			if wasFiltering {
				// The query the operator typed was refused: say why ON the
				// filter form and keep what they typed. The history already on
				// the list is still the answer to the query it was loaded with.
				s.filterErr = omsRefusalSentence(m.err)
				return s, Status("filter refused: "+s.filterErr, StatusError)
			}
			s.loadErr = omsRefusalSentence(m.err)
			return s, Status("load maintenance history failed: "+s.loadErr, StatusError)
		}
		s.loadErr, s.filterErr = "", ""
		prev, had := s.addressed()
		s.history, s.query = m.history, m.query
		if wasFiltering {
			s.phase = histPhaseList
			s.blurAll()
		}
		s.reseat(prev, had)
		return s, nil

	case histVendorsMsg:
		s.vendorsLoading = false
		if m.err != nil {
			s.vendorsErr = omsRefusalSentence(m.err)
			return s, nil
		}
		s.vendors = m.vendors
		if s.vendorIdx >= len(s.vendors) {
			s.vendorIdx = 0
		}
		return s, nil

	case histSavedMsg:
		s.saving = false
		if m.err != nil {
			s.errMsg = "nothing saved: " + omsRefusalSentence(m.err)
			return s, Status(s.errMsg, StatusError)
		}
		s.errMsg = ""
		if m.edit {
			s.note = "notes saved"
			s.editID = ""
			s.editNotes.SetValue("")
		} else {
			s.note = "work logged"
			for i := range s.inputs {
				s.inputs[i].SetValue("")
			}
			s.internal = false
		}
		s.phase = histPhaseList
		s.blurAll()
		s.loading = true
		return s, tea.Batch(Status(s.note, StatusOK), s.load(s.query))

	case tea.KeyMsg:
		switch s.phase {
		case histPhaseFilter:
			return s.updateFilter(m)
		case histPhaseCreate:
			return s.updateCreate(m)
		case histPhaseEditNotes:
			return s.updateEdit(m)
		default:
			return s.updateList(m)
		}
	}

	// A caret blink, or any other message a focused box asks for.
	var cmd tea.Cmd
	switch s.phase {
	case histPhaseFilter:
		if s.filterFocus != histFilterSource {
			s.filterInputs[s.filterFocus], cmd = s.filterInputs[s.filterFocus].Update(msg)
		}
	case histPhaseCreate:
		if id := s.createFocusID(); histFieldIsText(id) {
			s.inputs[id], cmd = s.inputs[id].Update(msg)
		}
	case histPhaseEditNotes:
		s.editNotes, cmd = s.editNotes.Update(msg)
	}
	return s, cmd
}

// reseat follows the operator's place to its own ROW across a reload, by
// identity rather than position.
func (s *AssetMaintenanceHistoryScreen) reseat(prev omsapi.MaintenanceHistoryEntry, had bool) {
	rows := s.rows()
	if had {
		for i, r := range rows {
			if r.ID == prev.ID && r.Source == prev.Source {
				s.cursor = i
				return
			}
		}
	}
	if s.cursor >= len(rows) {
		s.cursor = len(rows) - 1
	}
	if s.cursor < 0 {
		s.cursor = 0
	}
}

func (s *AssetMaintenanceHistoryScreen) blurAll() {
	for i := range s.filterInputs {
		s.filterInputs[i].Blur()
	}
	for i := range s.inputs {
		s.inputs[i].Blur()
	}
	s.editNotes.Blur()
}

// ---------------------------------------------------------------------------
// The list
// ---------------------------------------------------------------------------

func (s *AssetMaintenanceHistoryScreen) updateList(m tea.KeyMsg) (Screen, tea.Cmd) {
	switch m.String() {
	case "esc":
		return s, SwitchTo(WSAssets, NewAssetDetailScreen(s.deps, s.assetID))
	case "up", "down":
		delta := 1
		if m.String() == "up" {
			delta = -1
		}
		if next, ok := s.pickRow(s.cursor, len(s.rows()), delta, len(s.listHeader()), s.listBar()); ok {
			s.cursor = next
		}
	case "pgup", "pgdown":
		dir := 1
		if m.String() == "pgup" {
			dir = -1
		}
		if next, ok := s.pageRow(s.listLines(), s.cursor, len(s.rows()), dir,
			len(s.listHeader()), s.listBar(), s.listBarItems(true)); ok {
			s.cursor = next
		}
	case "r":
		s.loading, s.loadErr, s.note = true, "", ""
		return s, s.load(s.query)
	case "enter":
		if !s.createOffered() {
			return s, nil
		}
		return s, s.openCreate()
	case "ctrl+e":
		if !s.historyEditOffered() {
			return s, nil
		}
		row, _ := s.addressed()
		return s, s.openEdit(row)
	case "ctrl+f":
		return s, s.openFilter()
	}
	return s, nil
}

// Grid columns: the completion DATE and the COST are facts and never give; the
// title is the identifier that abbreviates between them.
const (
	histDateW    = 10
	histCostMaxW = 12
)

func (s *AssetMaintenanceHistoryScreen) costW() int {
	w := len("Cost")
	for _, r := range s.rows() {
		w = jdeGridFactW(w, histCostMaxW, histCost(r.Cost))
	}
	return w
}

func histCost(d omsapi.DecimalString) string {
	if m := formatMoney(d); m != "" {
		return m
	}
	return "—"
}

func (s *AssetMaintenanceHistoryScreen) titleW() int {
	width := 76
	if w := s.bodyWidth(); w > 0 {
		width = w
	}
	w := width - (len(jdeIndent) + histDateW + 2 + 2 + s.costW())
	if w < 8 {
		w = 8
	}
	return w
}

func (s *AssetMaintenanceHistoryScreen) gridRow(date, title, cost string) string {
	titleW := s.titleW()
	return jdeIndent + strings.TrimRight(strings.Join([]string{
		jdeGridFactCell(date, histDateW, alignLeft),
		padCell(fitCell(jdeStatusOneLine(title), titleW), titleW, alignLeft),
		jdeGridFactCell(cost, s.costW(), alignRight),
	}, "  "), " ")
}

func (s *AssetMaintenanceHistoryScreen) listLines() *jdeLines {
	l := &jdeLines{}
	indent := strings.Repeat(" ", len(jdeIndent)+histDateW+2)
	for i, r := range s.rows() {
		row := s.gridRow(r.CompletedOn.String(), r.Title, histCost(r.Cost))
		if i == s.cursor {
			row = StyleJDEFieldFocused.Render(row)
		}
		l.AddRow(i, row)
		tokens := []jdeToken{{text: histSourceLabel(r.Source), style: StyleMuted}}
		var by []string
		if v := r.PerformedBy.Vendor; v != nil && v.Name != "" {
			by = append(by, v.Name)
		}
		if u := r.PerformedBy.InternalUser; u != nil && u.Username != "" {
			by = append(by, "@"+u.Username)
		}
		if len(by) > 0 {
			tokens = append(tokens, jdeToken{text: strings.Join(by, " / "), style: StyleMuted})
		}
		if r.InvoiceNumber != "" {
			tokens = append(tokens, jdeToken{text: "invoice " + r.InvoiceNumber, style: StyleMuted})
		}
		if r.AttachmentURL != nil && *r.AttachmentURL != "" {
			tokens = append(tokens, jdeToken{text: "attachment on the web", style: StyleMuted})
		}
		if r.Notes != "" {
			tokens = append(tokens, jdeToken{text: "Notes: " + jdeStatusOneLine(r.Notes), style: StyleMuted})
		}
		for _, line := range jdeWrapTokens(tokens, indent, s.bodyWidth()) {
			l.AddRow(i, line)
		}
	}
	return l
}

func (s *AssetMaintenanceHistoryScreen) rangeText() string {
	since, until := s.query.Since, s.query.Until
	if since == "" {
		since = "any"
	}
	if until == "" {
		until = "any"
	}
	return fmt.Sprintf("%s → %s · %s", since, until, histSourceLabel(s.query.Source))
}

func (s *AssetMaintenanceHistoryScreen) listHeader() jdeHeader {
	w := s.bodyWidth()
	h := jdeHeader(nil)
	if s.loadErr != "" {
		h = h.add(jdeHeadContext, StyleStatusError.Render("Error: ")+fitCellIf(s.loadErr, w-poErrPrefixW))
	}
	h = h.add(jdeHeadContext, StyleJDEHeading.Render(fitCellIf("Maintenance history", w)))
	if s.history == nil {
		msg := "Reading the history…"
		if s.loadErr != "" {
			msg = "Could not read the history — there may still be records."
		}
		return h.add(jdeHeadEssential, jdeIndent+StyleMuted.Render(fitCellIf(msg, w-len(jdeIndent))))
	}
	total := fmt.Sprintf("Total %s across %d records", histCost(s.history.TotalCost), s.history.Count)
	h = h.add(jdeHeadEssential, jdeIndent+fitCellIf(total, w-len(jdeIndent)))
	h = h.add(jdeHeadContext, jdeIndent+StyleMuted.Render(fitCellIf(s.rangeText(), w-len(jdeIndent))))
	if len(s.rows()) == 0 {
		empty := "Nothing recorded in this range."
		if s.createOffered() {
			empty += " Enter logs work done."
		}
		return h.add(jdeHeadContext, jdeIndent+StyleMuted.Render(fitCellIf(empty, w-len(jdeIndent))))
	}
	return h.add(jdeHeadContext, StyleMuted.Render(s.gridRow("Completed", "Title", "Cost")))
}

func (s *AssetMaintenanceHistoryScreen) listBar() []actionBarItem {
	return s.listBarItems(s.listPages())
}

func (s *AssetMaintenanceHistoryScreen) listBarItems(paging bool) []actionBarItem {
	var items []actionBarItem
	if s.createOffered() {
		items = append(items, actionBarItem{"Enter", "Log work"})
	}
	items = append(items, actionBarItem{"Esc", "Back"})
	if jdeRowMoves(len(s.rows())) {
		items = append(items, actionBarItem{"UP/DN", "Move"})
	}
	if s.historyEditOffered() {
		items = append(items, actionBarItem{"Ctrl-E", "Edit notes"})
	}
	items = append(items, actionBarItem{"Ctrl-F", "Filter"})
	if paging {
		items = append(items, actionBarItem{"PgUp/PgDn", "Page"})
	}
	return append(items, actionBarItem{"r", "Refresh"})
}

func (s *AssetMaintenanceHistoryScreen) listPages() bool {
	if len(s.rows()) == 0 {
		return false
	}
	return s.bodyPagesForBar(s.listLines(), len(s.rows()), len(s.listHeader()), s.listBarItems(true))
}

func (s *AssetMaintenanceHistoryScreen) viewList() string {
	status := s.statusRow(s.loading, "Reading this asset's maintenance history…", "")
	if !s.loading && s.note != "" {
		status = s.statusAnswer(StatusOK, s.note)
	}
	return s.frameWrapped(s.listHeader(), s.listLines(), s.cursor, status, s.listBar())
}

// ---------------------------------------------------------------------------
// The filter
// ---------------------------------------------------------------------------

func (s *AssetMaintenanceHistoryScreen) openFilter() tea.Cmd {
	s.phase = histPhaseFilter
	s.filterErr = ""
	s.filterInputs[histFilterSince].SetValue(s.query.Since)
	s.filterInputs[histFilterUntil].SetValue(s.query.Until)
	s.filterSource = 0
	for i, src := range histSources {
		if src.value == s.query.Source {
			s.filterSource = i
		}
	}
	s.filterFocus = histFilterSince
	s.blurAll()
	s.filterInputs[histFilterSince].Focus()
	return textinput.Blink
}

func (s *AssetMaintenanceHistoryScreen) filterBar() []actionBarItem {
	items := []actionBarItem{}
	if !s.filtering {
		items = append(items, actionBarItem{"Enter", "Apply"})
	}
	return append(items, actionBarItem{"Esc", "Cancel"}, actionBarItem{"UP/DN", "Fields"}, actionBarItem{"←→", "Source"})
}

func (s *AssetMaintenanceHistoryScreen) updateFilter(m tea.KeyMsg) (Screen, tea.Cmd) {
	switch m.String() {
	case "esc":
		// Leaving discards nothing that was ever applied: the list still answers
		// the query shown above it, and the boxes are refilled from it next time.
		// A FILTER load still out is abandoned, or its answer would re-query the
		// list behind the operator's back. Only that one: a refresh started from
		// the list before the form opened is still the list's, and dropping it
		// would leave the list reading forever.
		if s.filtering {
			s.loadSeq++
		}
		s.phase, s.filterErr, s.filtering = histPhaseList, "", false
		s.blurAll()
		return s, nil
	case "tab", "down", "shift+tab", "up":
		delta := 1
		if m.String() == "up" || m.String() == "shift+tab" {
			delta = -1
		}
		next, ok := s.moveRow(s.filterFocus, histFilterCount, delta, len(s.filterHeader()), s.filterBar())
		if !ok {
			return s, nil
		}
		s.blurAll()
		s.filterFocus = next
		if next != histFilterSource {
			s.filterInputs[next].Focus()
		}
		return s, textinput.Blink
	case "left", "right":
		if s.filterFocus != histFilterSource {
			break
		}
		delta := 1
		if m.String() == "left" {
			delta = -1
		}
		s.filterSource = (s.filterSource + delta + len(histSources)) % len(histSources)
		return s, nil
	case "enter":
		if s.filtering {
			return s, nil
		}
		// The dates go to the server AS TYPED: it is the authority on what a
		// date is, and its refusal is drawn on this frame in its own words.
		q := omsapi.MaintenanceHistoryQuery{
			Since:  strings.TrimSpace(s.filterInputs[histFilterSince].Value()),
			Until:  strings.TrimSpace(s.filterInputs[histFilterUntil].Value()),
			Source: histSources[s.filterSource].value,
		}
		s.filtering, s.filterErr = true, ""
		return s, s.load(q)
	}
	if s.filterFocus != histFilterSource {
		var cmd tea.Cmd
		s.filterInputs[s.filterFocus], cmd = s.filterInputs[s.filterFocus].Update(m)
		return s, cmd
	}
	return s, nil
}

func (s *AssetMaintenanceHistoryScreen) filterFields() []jdeField {
	return []jdeField{
		{Label: "Since", Kind: jdeText, Input: &s.filterInputs[histFilterSince], Width: 10,
			Hint: "YYYY-MM-DD, blank for any", Focused: s.filterFocus == histFilterSince},
		{Label: "Until", Kind: jdeText, Input: &s.filterInputs[histFilterUntil], Width: 10,
			Hint: "YYYY-MM-DD, blank for any", Focused: s.filterFocus == histFilterUntil},
		{Label: "Source", Kind: jdeChoice, Value: histSources[s.filterSource].label,
			Focused: s.filterFocus == histFilterSource},
	}
}

func (s *AssetMaintenanceHistoryScreen) filterHeader() jdeHeader {
	w := s.bodyWidth()
	return jdeHeader(nil).
		add(jdeHeadDecorative, StyleJDEHeading.Render(fitCellIf("Filter maintenance history", w)), "").
		add(jdeHeadEssential, renderJDEField(jdeField{
			Label: "Asset", Kind: jdeValue,
			Value: fitCellIf(s.assetName, jdeStripWidth(w, assetLabelW)),
		}, assetLabelW, w)).
		add(jdeHeadDecorative, "")
}

func (s *AssetMaintenanceHistoryScreen) viewFilter() string {
	fields := s.filterFields()
	body := &jdeLines{}
	body.AddFittedFields(fields, jdeLabelWidth(fields), s.bodyWidth(), 0)
	return s.frameWrapped(s.filterHeader(), body, s.filterFocus,
		s.statusRow(s.filtering, "Reading the history for that range…", s.filterErr), s.filterBar())
}

// ---------------------------------------------------------------------------
// Log historical work
// ---------------------------------------------------------------------------

func histFieldIsText(id int) bool {
	return id != histFieldPerformer && id != histFieldVendor
}

// createRows is the form's rows for the performer chosen: a vendor row OR a
// staff-username row, never both, because the one not chosen would be a box the
// save ignores.
func (s *AssetMaintenanceHistoryScreen) createRows() []int {
	who := histFieldVendor
	if s.internal {
		who = histFieldUsername
	}
	return []int{histFieldTitle, histFieldDesc, histFieldDate, histFieldPerformer, who,
		histFieldCost, histFieldInvoice, histFieldNotes}
}

func (s *AssetMaintenanceHistoryScreen) createFocusID() int {
	rows := s.createRows()
	if s.createFocus < 0 || s.createFocus >= len(rows) {
		return histFieldTitle
	}
	return rows[s.createFocus]
}

// openCreate opens the form. A DRAFT LEFT BY ESC IS KEPT: the boxes are cleared
// only by a save that landed, so an operator who stepped out to check an invoice
// number comes back to what they typed.
func (s *AssetMaintenanceHistoryScreen) openCreate() tea.Cmd {
	s.phase = histPhaseCreate
	s.errMsg, s.note = "", ""
	if strings.TrimSpace(s.inputs[histFieldDate].Value()) == "" {
		s.inputs[histFieldDate].SetValue(s.today().Format("2006-01-02"))
	}
	s.createFocus = 0
	s.blurAll()
	s.inputs[histFieldTitle].Focus()
	cmds := []tea.Cmd{textinput.Blink}
	if s.vendors == nil && !s.vendorsLoading {
		cmds = append(cmds, s.loadVendors())
	}
	return tea.Batch(cmds...)
}

func (s *AssetMaintenanceHistoryScreen) saveDecisionOffered() bool {
	return !s.saving
}

func (s *AssetMaintenanceHistoryScreen) createBar() []actionBarItem {
	items := []actionBarItem{}
	if s.saveDecisionOffered() {
		items = append(items, actionBarItem{"Enter", "Save"}, actionBarItem{"Esc", "Cancel"})
	}
	return append(items, actionBarItem{"UP/DN", "Fields"}, actionBarItem{"←→", "Choose"})
}

func (s *AssetMaintenanceHistoryScreen) updateCreate(m tea.KeyMsg) (Screen, tea.Cmd) {
	id := s.createFocusID()
	switch m.String() {
	case "esc":
		if s.saveDecisionOffered() {
			s.phase, s.errMsg = histPhaseList, ""
			s.blurAll()
		}
		return s, nil
	case "tab", "down", "shift+tab", "up":
		delta := 1
		if m.String() == "up" || m.String() == "shift+tab" {
			delta = -1
		}
		next, ok := s.moveRow(s.createFocus, len(s.createRows()), delta, len(s.createHeader()), s.createBar())
		if !ok {
			return s, nil
		}
		s.blurAll()
		s.createFocus = next
		if nid := s.createFocusID(); histFieldIsText(nid) {
			s.inputs[nid].Focus()
		}
		return s, textinput.Blink
	case "left", "right":
		delta := 1
		if m.String() == "left" {
			delta = -1
		}
		switch id {
		case histFieldPerformer:
			s.internal = !s.internal
			return s, nil
		case histFieldVendor:
			if n := len(s.vendors); n > 0 {
				s.vendorIdx = (s.vendorIdx + delta + n) % n
			}
			return s, nil
		}
	case "enter":
		if !s.saveDecisionOffered() {
			return s, nil
		}
		return s, s.submitCreate()
	}
	if histFieldIsText(id) {
		var cmd tea.Cmd
		s.inputs[id], cmd = s.inputs[id].Update(m)
		return s, cmd
	}
	return s, nil
}

// submitCreate refuses, ON THE FRAME, only what the operator can fix standing
// on it — the web's own client checks — and hands everything else to the
// server, whose refusal is drawn in its own words. Nothing typed is cleared by a
// refusal.
func (s *AssetMaintenanceHistoryScreen) submitCreate() tea.Cmd {
	val := func(id int) string { return strings.TrimSpace(s.inputs[id].Value()) }
	refuse := func(msg string) tea.Cmd {
		s.errMsg = "nothing saved: " + msg
		return Status(s.errMsg, StatusError)
	}
	if val(histFieldTitle) == "" || val(histFieldDesc) == "" {
		return refuse("a title and a description are required.")
	}
	date := val(histFieldDate)
	if date == "" {
		return refuse("the completion date is required.")
	}
	if d, err := time.Parse("2006-01-02", date); err == nil && d.After(s.today()) {
		return refuse("the completion date cannot be in the future.")
	}
	body := omsapi.MaintenanceRecordWrite{
		Asset: s.assetID, Title: val(histFieldTitle), Description: val(histFieldDesc),
		CompletedOn: date, InvoiceNumber: val(histFieldInvoice), Notes: val(histFieldNotes),
	}
	if c := val(histFieldCost); c != "" {
		body.Cost = &c
	}
	username := val(histFieldUsername)
	if s.internal {
		if username == "" {
			return refuse("type the staff member's username, or ←→ back to a vendor.")
		}
	} else {
		if s.vendorIdx >= len(s.vendors) {
			return refuse("pick a vendor, or ←→ to internal staff.")
		}
		id := s.vendors[s.vendorIdx].ID
		body.Vendor = &id
	}
	s.saving, s.errMsg = true, ""
	deps, ctx, internal := s.deps, s.ctx(), s.internal
	return func() tea.Msg {
		if internal {
			id, err := histResolveUser(ctx, deps, username)
			if err != nil {
				return histSavedMsg{err: err}
			}
			body.PerformedByInternal = &id
		}
		_, err := deps.OMS.CreateMaintenanceRecord(ctx, body)
		return histSavedMsg{err: err}
	}
}

// histNoSuchUser is a username the directory holds no exact match for.
type histNoSuchUser string

func (e histNoSuchUser) Error() string {
	return fmt.Sprintf("no user is named %q — the username must match exactly.", string(e))
}

// histResolveUser turns a typed username into the user's pk by an EXACT match
// in the user directory. The directory's `search` is a substring match across
// names and email, so the first result is not the answer; only a row whose
// username IS what was typed is.
func histResolveUser(ctx context.Context, deps Deps, username string) (int, error) {
	users, err := deps.OMS.ListAllUsers(ctx, url.Values{"search": {username}})
	if err != nil {
		return 0, err
	}
	for _, u := range users {
		if u.Username == username {
			return u.ID, nil
		}
	}
	return 0, histNoSuchUser(username)
}

var histLabels = map[int]string{
	histFieldTitle: "Title", histFieldDesc: "Description", histFieldDate: "Completed on",
	histFieldPerformer: "Performed by", histFieldVendor: "Vendor", histFieldUsername: "Staff user",
	histFieldCost: "Cost", histFieldInvoice: "Invoice no.", histFieldNotes: "Notes",
}

func (s *AssetMaintenanceHistoryScreen) vendorValue() string {
	switch {
	case s.vendorsLoading:
		return "(reading vendors…)"
	case s.vendorsErr != "":
		return "(could not read vendors)"
	case len(s.vendors) == 0:
		return "(no vendors on file)"
	}
	return s.vendors[s.vendorIdx].Name
}

func (s *AssetMaintenanceHistoryScreen) createFields() []jdeField {
	var fields []jdeField
	for i, id := range s.createRows() {
		f := jdeField{Label: histLabels[id], Focused: s.createFocus == i}
		switch id {
		case histFieldPerformer:
			f.Kind, f.Value = jdeChoice, "Vendor"
			if s.internal {
				f.Value = "Internal staff"
			}
		case histFieldVendor:
			f.Kind, f.Value = jdeChoice, s.vendorValue()
			if len(s.vendors) > 1 {
				f.Hint = fmt.Sprintf("%d of %d", s.vendorIdx+1, len(s.vendors))
			}
		default:
			f.Kind, f.Input, f.Width = jdeText, &s.inputs[id], 30
			switch id {
			case histFieldTitle, histFieldDesc:
				f.Hint = "required"
			case histFieldDate:
				f.Width, f.Hint = 10, "YYYY-MM-DD, not after today (UTC)"
			case histFieldUsername:
				f.Width, f.Hint = 20, "exact username"
			case histFieldCost:
				f.Width, f.Hint = 12, "optional"
			case histFieldInvoice, histFieldNotes:
				f.Hint = "optional"
			}
		}
		fields = append(fields, f)
	}
	return fields
}

// histRecordCaveat is what a record does NOT do, said on the frame that writes
// one — see the file comment.
const histRecordCaveat = "A record is a log entry only: it does not reset any PM schedule, " +
	"create a work order or count toward the maintenance dashboard's costs."

func (s *AssetMaintenanceHistoryScreen) createHeader() jdeHeader {
	w := s.bodyWidth()
	h := jdeHeader(nil).
		add(jdeHeadDecorative, StyleJDEHeading.Render(fitCellIf("Log historical work", w)), "").
		add(jdeHeadEssential, renderJDEField(jdeField{
			Label: "Asset", Kind: jdeValue,
			Value: fitCellIf(s.assetName, jdeStripWidth(w, assetLabelW)),
		}, assetLabelW, w))
	h = h.addCaveat(jdeHeadContext, jdeCaveatLines(histRecordCaveat, w))
	if s.vendorsErr != "" && !s.internal {
		h = h.add(jdeHeadContext, StyleStatusError.Render("Vendors: ")+fitCellIf(s.vendorsErr, w-len("Vendors: ")))
	}
	return h.add(jdeHeadDecorative, "")
}

func (s *AssetMaintenanceHistoryScreen) viewCreate() string {
	fields := s.createFields()
	body := &jdeLines{}
	body.AddFittedFields(fields, jdeLabelWidth(fields), s.bodyWidth(), 0)
	return s.frameWrapped(s.createHeader(), body, s.createFocus,
		s.statusRow(s.saving, "Saving the record…", s.errMsg), s.createBar())
}

// ---------------------------------------------------------------------------
// Edit notes
// ---------------------------------------------------------------------------

// openEdit opens the notes box on a logged row. The box is prefilled from the
// row — unless this is the SAME record whose edit was left with Esc, in which
// case what was typed is still there.
func (s *AssetMaintenanceHistoryScreen) openEdit(row omsapi.MaintenanceHistoryEntry) tea.Cmd {
	if s.editID != row.ID {
		s.editNotes.SetValue(row.Notes)
	}
	s.editID, s.editTitle, s.editDate = row.ID, row.Title, row.CompletedOn.String()
	s.phase, s.errMsg, s.note = histPhaseEditNotes, "", ""
	s.blurAll()
	s.editNotes.Focus()
	return textinput.Blink
}

func (s *AssetMaintenanceHistoryScreen) editBar() []actionBarItem {
	if !s.saveDecisionOffered() {
		return nil
	}
	return []actionBarItem{{"Enter", "Save"}, {"Esc", "Cancel"}}
}

func (s *AssetMaintenanceHistoryScreen) updateEdit(m tea.KeyMsg) (Screen, tea.Cmd) {
	switch m.String() {
	case "esc":
		if s.saveDecisionOffered() {
			s.phase, s.errMsg = histPhaseList, ""
			s.blurAll()
		}
		return s, nil
	case "enter":
		if !s.saveDecisionOffered() {
			return s, nil
		}
		s.saving, s.errMsg = true, ""
		deps, ctx, id, notes := s.deps, s.ctx(), s.editID, s.editNotes.Value()
		return s, func() tea.Msg {
			_, err := deps.OMS.UpdateMaintenanceRecordNotes(ctx, id, notes)
			return histSavedMsg{err: err, edit: true}
		}
	}
	var cmd tea.Cmd
	s.editNotes, cmd = s.editNotes.Update(m)
	return s, cmd
}

func (s *AssetMaintenanceHistoryScreen) editHeader() jdeHeader {
	w := s.bodyWidth()
	name := s.editTitle
	if s.editDate != "" {
		name = s.editDate + " · " + name
	}
	return jdeHeader(nil).
		add(jdeHeadDecorative, StyleJDEHeading.Render(fitCellIf("Edit record notes", w)), "").
		add(jdeHeadEssential, renderJDEField(jdeField{
			Label: "Record", Kind: jdeValue, Focused: true,
			Value: fitCellIf(jdeStatusOneLine(name), jdeStripWidth(w, assetLabelW)),
		}, assetLabelW, w)).
		add(jdeHeadDecorative, "")
}

func (s *AssetMaintenanceHistoryScreen) viewEdit() string {
	fields := []jdeField{{Label: "Notes", Kind: jdeText, Input: &s.editNotes, Width: 34, Focused: true}}
	body := &jdeLines{}
	body.AddFittedFields(fields, jdeLabelWidth(fields), s.bodyWidth(), 0)
	return s.frameWrapped(s.editHeader(), body, 0,
		s.statusRow(s.saving, "Saving the notes…", s.errMsg), s.editBar())
}

func (s *AssetMaintenanceHistoryScreen) View() string {
	switch s.phase {
	case histPhaseFilter:
		return s.viewFilter()
	case histPhaseCreate:
		return s.viewCreate()
	case histPhaseEditNotes:
		return s.viewEdit()
	}
	return s.viewList()
}
