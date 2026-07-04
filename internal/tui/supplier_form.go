// Supplier CRUD — list + create/edit form.
//
// TUI counterpart to the web SupplierFormPage.tsx (+ the /inventory/suppliers
// list). Mirrors the FULL field set of the web form / supplierSchema: name,
// supplier_type (local/online/national), website, account_number,
// tax_free_paperwork_filed and notes.
//
// SupplierListScreen is reached with the global `U` hotkey (app.go). enter opens
// the supplier detail screen; c/E/x create/edit/delete. Like the other taxonomy
// lists it flips to raw input only during the delete confirm. (Supplier
// analytics — lead-time / price trends — is a separate web page and stays a
// follow-up, not part of this screen.)
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

type SupplierFormScreen struct {
	deps  Deps
	edit  bool
	supID string

	loading bool
	loadErr string
	saving  bool
	errMsg  string

	sup *omsapi.Supplier

	terminalHeight int

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

func supplierPlaceholder(id int) string {
	switch id {
	case sfName:
		return "supplier name"
	case sfWebsite:
		return "https://example.com (optional)"
	case sfAccountNumber:
		return "account # with this supplier (optional)"
	case sfNotes:
		return "optional"
	}
	return ""
}

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
		s.terminalHeight = m.Height
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
	switch id {
	case sfType:
		switch m.String() {
		case " ", "right":
			s.typeIdx = (s.typeIdx + 1) % len(supplierTypeOptions)
		case "left":
			s.typeIdx = (s.typeIdx - 1 + len(supplierTypeOptions)) % len(supplierTypeOptions)
		}
		return s, nil
	case sfTaxFree:
		if m.String() == " " {
			s.taxFree = !s.taxFree
		}
		return s, nil
	default:
		var cmd tea.Cmd
		s.inputs[id], cmd = s.inputs[id].Update(m)
		return s, cmd
	}
}

func (s *SupplierFormScreen) moveCursor(delta int) {
	n := len(s.fields)
	if n == 0 {
		return
	}
	s.cursor = (s.cursor + delta + n) % n
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
	var b strings.Builder
	b.WriteString(StyleMuted.Render(s.helpText()) + "\n\n")
	for i := range s.fields {
		b.WriteString(s.renderField(i) + "\n")
	}
	b.WriteString("\n")
	if s.saving {
		b.WriteString(StyleMuted.Render("Saving…"))
	} else if s.errMsg != "" {
		b.WriteString(StyleStatusError.Render("✗ " + s.errMsg))
	}
	return b.String()
}

func (s *SupplierFormScreen) renderField(i int) string {
	id := s.fields[i]
	caret := "  "
	if i == s.cursor {
		caret = "▸ "
	}
	label := supplierFieldLabel[id]
	var value string
	switch id {
	case sfType:
		value = "‹ " + supplierTypeOptions[s.typeIdx].label + " ›"
	case sfTaxFree:
		if s.taxFree {
			value = StyleStatusOK.Render("[x] yes")
		} else {
			value = StyleMuted.Render("[ ] no")
		}
	default:
		value = s.inputs[id].View()
	}
	return caret + StyleTitle.Render(label+": ") + value
}

func (s *SupplierFormScreen) helpText() string {
	kindHelp := "type to edit"
	if id, ok := s.currentFieldID(); ok {
		switch id {
		case sfType:
			kindHelp = "space/←→ change"
		case sfTaxFree:
			kindHelp = "space toggle"
		}
	}
	return kindHelp + " · tab/↑↓ move · enter save · esc cancel"
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
	const chrome = 4
	avail := screenBodyHeight(s.terminalHeight) - chrome
	if avail < 3 {
		avail = 3
	}
	return avail
}

func (s *SupplierListScreen) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		s.terminalHeight = m.Height
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
	b.WriteString(StyleMuted.Render("j/k move · enter open · c new · E edit · x delete · r refresh · esc back"))
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
