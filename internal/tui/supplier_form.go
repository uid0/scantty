// Supplier CRUD — list + create/edit form.
//
// TUI counterpart to the web SupplierFormPage.tsx (+ the /inventory/suppliers
// list). Mirrors the FULL field set of the web form / supplierSchema: name,
// supplier_type (local/online/national), website, account_number,
// tax_free_paperwork_filed and notes.
//
// SupplierListScreen is reached from the sidebar menu (Inventory > Suppliers).
// enter opens
// the supplier detail screen; c/E/x create/edit/delete. Like the other taxonomy
// lists it flips to raw input only during the delete confirm. (Supplier
// analytics — lead-time / price trends — is a separate web page and stays a
// follow-up, not part of this screen.)
//
// The FORM renders through the columnar "JD Edwards" layer (jde_form.go,
// sc-dnhx): labels right-aligned into one column, the type and the tax-free flag
// as "< value >" choice rows, and a persistent action bar naming the keys that
// apply — Enter saves, Esc cancels, Up/Down move, ←/→ change a choice. The list
// screen keeps its own keys; only the form was in the sweep.
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

// supplierTypeOptions mirror the web form's Type select. "local" is the web
// form's default for a new supplier.
var supplierTypeOptions = []selectOption{
	{"local", "Local"},
	{"online", "Online"},
	{"national", "National"},
}

func supplierTypeIndex(v string) int {
	for i, o := range supplierTypeOptions {
		if o.value == v {
			return i
		}
	}
	return 0
}

// ===========================================================================
// SupplierFormScreen
// ===========================================================================

const (
	sfName = iota
	sfType
	sfWebsite
	sfAccountNumber
	sfTaxFree
	sfNotes
	sfFieldMax
)

var supplierFieldLabel = map[int]string{
	sfName:          "Name",
	sfType:          "Type",
	sfWebsite:       "Website",
	sfAccountNumber: "Account number",
	sfTaxFree:       "Tax-free paperwork filed",
	sfNotes:         "Notes",
}

func supplierFieldIsText(id int) bool {
	switch id {
	case sfName, sfWebsite, sfAccountNumber, sfNotes:
		return true
	}
	return false
}

// supplierFieldWidth sizes the input areas that are not the default: a website
// and a note are the two places an operator writes something long.
func supplierFieldWidth(id int) int {
	switch id {
	case sfWebsite, sfNotes:
		return 40
	}
	return 0
}

// supplierFieldHint is the muted note drawn AFTER the input area. A columnar
// form marks what is required rather than tagging everything else "(optional)",
// and the notes that used to sit in the placeholders live here — a placeholder
// long enough to fill the field leaves no underscores, which is what makes an
// empty green-screen row read as empty.
var supplierFieldHint = map[int]string{
	sfName:    "required",
	sfWebsite: "https://…",
}

type SupplierFormScreen struct {
	deps  Deps
	edit  bool
	supID string

	loading bool
	loadErr string
	saving  bool
	errMsg  string

	sup *omsapi.Supplier

	jdeScreen

	inputs  []textinput.Model
	typeIdx int
	taxFree bool

	fields []int
	cursor int
}

type supplierFormLoadedMsg struct {
	sup *omsapi.Supplier
	err error
}

type supplierSavedMsg struct {
	sup *omsapi.Supplier
	err error
}

func NewSupplierFormScreen(deps Deps, supID string) *SupplierFormScreen {
	edit := strings.TrimSpace(supID) != ""
	s := &SupplierFormScreen{
		deps:  deps,
		edit:  edit,
		supID: strings.TrimSpace(supID),
	}
	s.inputs = make([]textinput.Model, sfFieldMax)
	for id := 0; id < sfFieldMax; id++ {
		if !supplierFieldIsText(id) {
			continue
		}
		ti := textinput.New()
		ti.Prompt = ""
		ti.CharLimit = supplierCharLimit(id)
		ti.Placeholder = supplierPlaceholder(id)
		s.inputs[id] = ti
	}
	s.fields = []int{sfName, sfType, sfWebsite, sfAccountNumber, sfTaxFree, sfNotes}
	if edit {
		s.loading = true
	}
	s.syncFocus()
	return s
}

func supplierCharLimit(id int) int {
	switch id {
	case sfName:
		return 200
	case sfWebsite:
		return 300
	case sfAccountNumber:
		return 100
	case sfNotes:
		return 1000
	}
	return 200
}

// supplierPlaceholder is empty for every field now: what these said — the
// field's own name, "(optional)", a URL shape — is either the label above it or
// the hint beside it, and a placeholder that fills the input area hides the
// underscores that say the field is empty. See supplierFieldHint.
func supplierPlaceholder(id int) string { return "" }

func (s *SupplierFormScreen) Title() string {
	if s.edit {
		if s.sup != nil && s.sup.Name != "" {
			return "Edit supplier: " + s.sup.Name
		}
		return "Edit supplier"
	}
	return "New supplier"
}

func (s *SupplierFormScreen) WantsRawInput() bool { return true }

func (s *SupplierFormScreen) Init() tea.Cmd {
	if s.edit {
		return tea.Batch(s.loadSupplier(), textinput.Blink)
	}
	return textinput.Blink
}

func (s *SupplierFormScreen) ctx() context.Context {
	if s.deps.Ctx != nil {
		return s.deps.Ctx
	}
	return context.Background()
}

func (s *SupplierFormScreen) loadSupplier() tea.Cmd {
	deps := s.deps
	ctx := s.ctx()
	id := s.supID
	return func() tea.Msg {
		sup, err := deps.OMS.GetSupplier(ctx, id)
		return supplierFormLoadedMsg{sup: sup, err: err}
	}
}

func (s *SupplierFormScreen) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		s.setSize(m)
		return s, nil
	case supplierFormLoadedMsg:
		s.loading = false
		if m.err != nil {
			s.loadErr = m.err.Error()
		} else {
			s.sup = m.sup
			s.hydrate()
		}
		s.syncFocus()
		return s, nil
	case supplierSavedMsg:
		s.saving = false
		if m.err != nil {
			s.errMsg = m.err.Error()
			return s, Status("save failed: "+m.err.Error(), StatusError)
		}
		verb := "created"
		if s.edit {
			verb = "updated"
		}
		id := s.supID
		name := ""
		if m.sup != nil {
			id = strconv.Itoa(m.sup.ID)
			name = m.sup.Name
		}
		return s, tea.Batch(
			Status(fmt.Sprintf("supplier %s: %s", verb, name), StatusOK),
			SwitchTo(WSInventory, NewSupplierDetailScreen(s.deps, id)),
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

	if id, ok := s.currentFieldID(); ok && supplierFieldIsText(id) {
		var cmd tea.Cmd
		s.inputs[id], cmd = s.inputs[id].Update(msg)
		return s, cmd
	}
	return s, nil
}

func (s *SupplierFormScreen) hydrate() {
	if s.sup == nil {
		return
	}
	s.inputs[sfName].SetValue(s.sup.Name)
	s.typeIdx = supplierTypeIndex(s.sup.SupplierType)
	s.inputs[sfWebsite].SetValue(s.sup.Website)
	s.inputs[sfAccountNumber].SetValue(s.sup.AccountNumber)
	s.taxFree = s.sup.TaxFreePaperworkFiled
	s.inputs[sfNotes].SetValue(s.sup.Notes)
}

func (s *SupplierFormScreen) currentFieldID() (int, bool) {
	if s.cursor < 0 || s.cursor >= len(s.fields) {
		return 0, false
	}
	return s.fields[s.cursor], true
}

func (s *SupplierFormScreen) syncFocus() {
	for id := 0; id < len(s.inputs); id++ {
		if supplierFieldIsText(id) {
			s.inputs[id].Blur()
		}
	}
	if id, ok := s.currentFieldID(); ok && supplierFieldIsText(id) {
		s.inputs[id].Focus()
	}
}

func (s *SupplierFormScreen) updateFormPhase(m tea.KeyMsg) (Screen, tea.Cmd) {
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
	if !supplierFieldIsText(id) {
		// A choice row: nothing to type into it, so ←/→ (and space, the pilot's
		// synonym) cycle the value in place.
		switch m.String() {
		case " ", "right":
			s.cycleChoice(id, +1)
		case "left":
			s.cycleChoice(id, -1)
		}
		return s, nil
	}
	var cmd tea.Cmd
	s.inputs[id], cmd = s.inputs[id].Update(m)
	return s, cmd
}

// cycleChoice advances a choice row. A two-value set (the tax-free flag) flips
// whichever way it is cycled, which is what a bool means.
func (s *SupplierFormScreen) cycleChoice(id, delta int) {
	switch id {
	case sfType:
		n := len(supplierTypeOptions)
		s.typeIdx = (s.typeIdx + delta + n) % n
	case sfTaxFree:
		s.taxFree = !s.taxFree
	}
}

func (s *SupplierFormScreen) moveCursor(delta int) {
	body := s.formLines()
	next, ok := s.moveRow(s.cursor, len(s.fields), delta, 0, s.formBar(body))
	if !ok {
		return
	}
	s.cursor = next
	s.syncFocus()
}

func (s *SupplierFormScreen) pageCursor(dir int) {
	body := s.formLines()
	next, ok := s.pageRow(body, s.cursor, len(s.fields), dir, 0,
		s.formBar(body), s.formBarItems(true))
	if !ok {
		return
	}
	s.cursor = next
	s.syncFocus()
}

func (s *SupplierFormScreen) submit() (Screen, tea.Cmd) {
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
	id := s.supID
	return s, func() tea.Msg {
		var sup *omsapi.Supplier
		var e error
		if edit {
			sup, e = deps.OMS.UpdateSupplier(ctx, id, body)
		} else {
			sup, e = deps.OMS.CreateSupplier(ctx, body)
		}
		return supplierSavedMsg{sup: sup, err: e}
	}
}

func (s *SupplierFormScreen) buildPayload() (omsapi.SupplierWrite, error) {
	var w omsapi.SupplierWrite
	name := strings.TrimSpace(s.inputs[sfName].Value())
	if name == "" {
		return w, errors.New("name is required")
	}
	if s.typeIdx < 0 || s.typeIdx >= len(supplierTypeOptions) {
		return w, errors.New("supplier type is required")
	}
	w = omsapi.SupplierWrite{
		Name:                  name,
		SupplierType:          supplierTypeOptions[s.typeIdx].value,
		Website:               strings.TrimSpace(s.inputs[sfWebsite].Value()),
		AccountNumber:         strings.TrimSpace(s.inputs[sfAccountNumber].Value()),
		TaxFreePaperworkFiled: s.taxFree,
		Notes:                 strings.TrimSpace(s.inputs[sfNotes].Value()),
	}
	return w, nil
}

func (s *SupplierFormScreen) cancelCmd() tea.Cmd {
	if s.edit && s.supID != "" {
		return SwitchTo(WSInventory, NewSupplierDetailScreen(s.deps, s.supID))
	}
	return SwitchTo(WSInventory, NewSupplierListScreen(s.deps))
}

func (s *SupplierFormScreen) View() string {
	if s.loading {
		return StyleMuted.Render("Loading…")
	}
	if s.loadErr != "" {
		return StyleStatusError.Render("Error: ") + s.loadErr + "\n\n" + StyleMuted.Render("esc to go back")
	}
	body := s.formLines()
	return s.frame(body, s.cursor, s.statusRow(s.saving, "Saving…", s.errMsg), s.formBar(body))
}

// formFields describes the form as columnar rows: the type and the tax-free flag
// are bounded sets, everything else is typed into.
func (s *SupplierFormScreen) formFields() []jdeField {
	out := make([]jdeField, len(s.fields))
	for i, id := range s.fields {
		f := jdeField{
			Label:   supplierFieldLabel[id],
			Width:   supplierFieldWidth(id),
			Hint:    supplierFieldHint[id],
			Focused: i == s.cursor,
		}
		if supplierFieldIsText(id) {
			f.Kind, f.Input = jdeText, &s.inputs[id]
		} else {
			f.Kind, f.Value = jdeChoice, s.choiceLabel(id)
		}
		out[i] = f
	}
	return out
}

// choiceLabel is what goes between a choice row's angle brackets.
func (s *SupplierFormScreen) choiceLabel(id int) string {
	switch id {
	case sfType:
		if s.typeIdx >= 0 && s.typeIdx < len(supplierTypeOptions) {
			return supplierTypeOptions[s.typeIdx].label
		}
	case sfTaxFree:
		return jdeYesNo(s.taxFree)
	}
	return ""
}

func (s *SupplierFormScreen) formLines() *jdeLines {
	fields := s.formFields()
	labelWidth := jdeLabelWidth(fields)

	l := &jdeLines{}
	l.Add(StyleJDEHeading.Render("Supplier details"))
	for i, f := range fields {
		l.AddFittedField(i, f, labelWidth, s.bodyWidth())
		// The whole set under the FOCUSED choice row, so a short fixed list is
		// never cycled blind (jdeOptionStrip returns nothing for a yes/no).
		if i == s.cursor && s.fields[i] == sfType {
			labels := make([]string, len(supplierTypeOptions))
			for j, o := range supplierTypeOptions {
				labels[j] = o.label
			}
			if strip := jdeOptionStrip(labels, s.typeIdx, jdeStripWidth(s.bodyWidth(), labelWidth)); strip != "" {
				l.AddRow(i, jdeStripIndent(labelWidth)+StyleMuted.Render(strip))
			}
		}
	}
	return l
}

// formBar names the keys that work on the form, with PgUp/PgDn on it exactly
// when the body moves under the bar that is about to be drawn.
//
// The paging claim is measured against formBarItems(true) — the bar WITH the
// pair on it — because naming them costs cells, cells fold the bar onto another
// row, and a folded bar leaves the body one row fewer. The tallest bar is the
// fixed point, so the answer cannot oscillate between frames.
func (s *SupplierFormScreen) formBar(body *jdeLines) []actionBarItem {
	return s.formBarItems(s.bodyPagesForBar(body, len(s.fields), 0, s.formBarItems(true)))
}

// formBarItems is formBar for a given paging state, so the bar that is
// MEASURED is the bar that is drawn.
//
// It names the keys that apply where the cursor is standing — and only those,
// so the bar never teaches a key that does nothing here.
func (s *SupplierFormScreen) formBarItems(paging bool) []actionBarItem {
	items := []actionBarItem{{"Enter", "Save"}, {"Esc", "Cancel"}, {"UP/DN", "Fields"}}
	if id, ok := s.currentFieldID(); ok && !supplierFieldIsText(id) {
		items = append(items, actionBarItem{"←→", "Change"})
	}
	if paging {
		items = append(items, actionBarItem{"PgUp/PgDn", "Page"})
	}
	return items
}

// ===========================================================================
// SupplierListScreen
// ===========================================================================

type SupplierListScreen struct {
	deps           Deps
	rows           []omsapi.Supplier
	cursor         int
	windowStart    int
	windowSize     int
	loading        bool
	loadErr        string
	terminalHeight int
	terminalWidth  int

	confirmingDelete bool
	deleting         bool
}

type supplierListLoadedMsg struct {
	rows []omsapi.Supplier
	err  error
}

type supplierDeletedMsg struct {
	err error
}

func NewSupplierListScreen(deps Deps) *SupplierListScreen {
	return &SupplierListScreen{deps: deps, loading: true, windowSize: 20}
}

func (s *SupplierListScreen) Title() string { return "Suppliers" }

func (s *SupplierListScreen) WantsRawInput() bool { return s.confirmingDelete }

func (s *SupplierListScreen) Init() tea.Cmd {
	deps := s.deps
	ctx := deps.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return func() tea.Msg {
		page, err := deps.OMS.ListSuppliers(ctx, nil)
		if err != nil {
			return supplierListLoadedMsg{err: err}
		}
		return supplierListLoadedMsg{rows: page.Results}
	}
}

func (s *SupplierListScreen) computeWindowSize() int {
	return proseListWindow(s.terminalHeight, s.paneCells(), s.bar(true))
}

// paneCells is the width this list folds and budgets against: the pane the
// terminal really gave, never the 51 an 80-column one happens to leave.
func (s *SupplierListScreen) paneCells() int { return proseBarCells(s.terminalWidth) }

// bar names every key that acts on this list, as a RECORD rather than a literal
// — prose_bar.go carries the conversion, proseNavCursor why the movement half
// is gated on one threshold, and proseListWindow what it costs the body.
//
// It used to be
//
//	j/k move · enter open · c new · E edit · x delete · r refresh · esc back
//
// 72 cells against the 51 an 80-column pane gives, so clampToBox was already
// taking the end of it — and it named two of the ten movement keystrokes
// this screen's own switch binds: the arrows, pgup/pgdn and g/G/home/end all
// moved the cursor and no word on the bar said so.
func (s *SupplierListScreen) bar(moves bool) proseBar {
	return append(proseNavCursor(moves),
		proseBarItem{Keys: []string{"enter"}, Hint: "enter open"},
		proseBarItem{Keys: []string{"c"}, Hint: "c new"},
		proseBarItem{Keys: []string{"E"}, Hint: "E edit"},
		proseBarItem{Keys: []string{"x"}, Hint: "x delete"},
		proseBarRefresh,
		proseBarEsc,
	)
}

// proseBar is the bar this screen is DRAWING, and nil in the states that draw
// something else instead — a load in flight, a failure, a prompt that replaces
// the footer, and the EMPTY list, whose shorter footer is still a literal. So
// "this state has no bar" and "this state's bar is empty" stay different answers
// to the honesty sweep, and what this conversion leaves behind is a STATE rather
// than a screen.
func (s *SupplierListScreen) proseBar() proseBar {
	if s.loading || s.loadErr != "" || s.confirmingDelete || len(s.rows) == 0 {
		return nil
	}
	return s.bar(listNavMoves(len(s.rows)))
}

func (s *SupplierListScreen) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		s.terminalHeight = m.Height
		s.terminalWidth = m.Width
		s.windowSize = s.computeWindowSize()
		s.scrollIntoView()
		return s, nil
	case supplierListLoadedMsg:
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
	case supplierDeletedMsg:
		s.deleting = false
		s.confirmingDelete = false
		if m.err != nil {
			return s, Status("delete failed: "+m.err.Error(), StatusError)
		}
		s.loading = true
		return s, tea.Batch(Status("supplier deleted", StatusOK), s.Init())
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
		case "pgdown":
			s.cursor += s.windowSize
			if s.cursor >= len(s.rows) {
				s.cursor = len(s.rows) - 1
			}
			s.scrollIntoView()
		case "pgup":
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
		case "c":
			return s, SwitchTo(WSInventory, NewSupplierFormScreen(s.deps, ""))
		case "enter":
			if row, ok := s.selected(); ok {
				return s, SwitchTo(WSInventory, NewSupplierDetailScreen(s.deps, strconv.Itoa(row.ID)))
			}
		case "E":
			if row, ok := s.selected(); ok {
				return s, SwitchTo(WSInventory, NewSupplierFormScreen(s.deps, strconv.Itoa(row.ID)))
			}
		case "x":
			if _, ok := s.selected(); ok {
				s.confirmingDelete = true
			}
		}
	}
	return s, nil
}

func (s *SupplierListScreen) updateConfirmDelete(m tea.KeyMsg) (Screen, tea.Cmd) {
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
		id := strconv.Itoa(row.ID)
		return s, func() tea.Msg {
			return supplierDeletedMsg{err: deps.OMS.DeleteSupplier(ctx, id)}
		}
	case "n", "N", "esc":
		s.confirmingDelete = false
	}
	return s, nil
}

func (s *SupplierListScreen) selected() (omsapi.Supplier, bool) {
	if s.cursor < 0 || s.cursor >= len(s.rows) {
		return omsapi.Supplier{}, false
	}
	return s.rows[s.cursor], true
}

func (s *SupplierListScreen) scrollIntoView() {
	if s.windowSize <= 0 {
		s.windowSize = 20
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

func (s *SupplierListScreen) View() string {
	if s.loading {
		return StyleMuted.Render("Loading suppliers…")
	}
	if s.loadErr != "" {
		return StyleStatusError.Render("Error: ") + s.loadErr + "\n\n" + StyleMuted.Render("r retry · c new · esc back")
	}
	if s.confirmingDelete {
		name := ""
		if row, ok := s.selected(); ok {
			name = row.Name
		}
		var prompt string
		if s.deleting {
			prompt = StyleMuted.Render("Deleting…")
		} else {
			prompt = StyleStatusWarn.Render(fmt.Sprintf("Delete supplier %q? This can't be undone.  y delete · n/esc cancel", name))
		}
		return prompt
	}
	if len(s.rows) == 0 {
		return StyleMuted.Render("No suppliers.") + "\n\n" + StyleMuted.Render("c new supplier · esc back")
	}

	var b strings.Builder
	b.WriteString(StyleMuted.Render(fmt.Sprintf("%d suppliers", len(s.rows))) + "\n")
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
	b.WriteString(s.proseBar().render(s.paneCells()))
	return b.String()
}

func (s *SupplierListScreen) renderRow(i int) string {
	sup := s.rows[i]
	marker := "  "
	if i == s.cursor {
		marker = "▸ "
	}
	meta := []string{}
	if sup.SupplierType != "" {
		meta = append(meta, sup.SupplierType)
	}
	if sup.ItemCount > 0 {
		meta = append(meta, fmt.Sprintf("%d items", sup.ItemCount))
	}
	if sup.TaxFreePaperworkFiled {
		meta = append(meta, "tax-free")
	}
	line := marker + sup.Name
	if len(meta) > 0 {
		line += " " + StyleMuted.Render("("+strings.Join(meta, " · ")+")")
	}
	if i == s.cursor {
		line = StyleSidebarItemActive.Render(line)
	}
	return line
}
