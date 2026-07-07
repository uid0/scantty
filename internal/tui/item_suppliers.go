// Item↔supplier link management — the "manage suppliers" surface for one
// inventory item.
//
// The item detail screen already DISPLAYS an item's suppliers (Primary supplier
// + All suppliers). This file adds the EDIT path the web only stubs
// (SupplierRelationshipForm is wired into InventoryItemFormPage but its save is a
// TODO): a working add / edit / delete / set-primary management view backed by
// the fully-CRUD ItemSupplierViewSet.
//
// ItemSuppliersScreen (list) is reached with `s` from the item detail. It stays
// non-raw except during the delete confirm, and claims esc (back to the item
// detail, not the global Welcome) + G (scroll-to-bottom) via LocalKeyScreen. Its
// action keys (c/E/x/p/r) are chosen to dodge the global nav hotkeys, matching
// SupplierListScreen.
//
// ItemSupplierFormScreen (create/edit) mirrors the web SupplierRelationshipForm's
// field set — supplier, supplier_sku, supplier_url, unit_cost, package_cost,
// quantity_per_package, average_lead_time, is_primary — with the supplier chosen
// from a searchable sub-picker (the inventory_item_form pattern). It's a raw-input
// form, so every key reaches it.
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
// ItemSuppliersScreen — list / manage an item's supplier links
// ===========================================================================

type ItemSuppliersScreen struct {
	deps     Deps
	itemID   string
	itemName string

	rows           []omsapi.ItemSupplier
	cursor         int
	windowStart    int
	windowSize     int
	loading        bool
	loadErr        string
	terminalHeight int

	confirmingDelete bool
	deleting         bool
	busy             bool // a set-primary PATCH is in flight
}

type itemSuppliersLoadedMsg struct {
	rows []omsapi.ItemSupplier
	err  error
}

type itemSupplierDeletedMsg struct {
	err error
}

type itemSupplierPrimaryMsg struct {
	err error
}

// NewItemSuppliersScreen lists every supplier link for one inventory item so an
// operator can add / edit / remove / set-primary against the item in front of
// them.
func NewItemSuppliersScreen(deps Deps, itemID, itemName string) *ItemSuppliersScreen {
	return &ItemSuppliersScreen{
		deps:       deps,
		itemID:     itemID,
		itemName:   itemName,
		loading:    true,
		windowSize: 20,
	}
}

func (s *ItemSuppliersScreen) Title() string {
	if s.itemName != "" {
		return "Suppliers: " + s.itemName
	}
	return "Item suppliers"
}

// WantsRawInput claims every key only during the delete confirm, so y/n/esc land
// here instead of the root's global hotkeys.
func (s *ItemSuppliersScreen) WantsRawInput() bool { return s.confirmingDelete }

// HandlesKey claims esc (back to the owning item detail rather than the global
// Welcome) and G (scroll-to-bottom, otherwise the global Categories hotkey). The
// other action keys dodge the globals and reach Update as the fallback.
func (s *ItemSuppliersScreen) HandlesKey(key string) bool {
	return key == "esc" || key == "G"
}

func (s *ItemSuppliersScreen) Init() tea.Cmd { return s.load() }

func (s *ItemSuppliersScreen) ctx() context.Context {
	if s.deps.Ctx != nil {
		return s.deps.Ctx
	}
	return context.Background()
}

func (s *ItemSuppliersScreen) load() tea.Cmd {
	deps := s.deps
	ctx := s.ctx()
	id := s.itemID
	return func() tea.Msg {
		rows, err := deps.OMS.ListItemSuppliersForItem(ctx, id)
		return itemSuppliersLoadedMsg{rows: rows, err: err}
	}
}

func (s *ItemSuppliersScreen) computeWindowSize() int {
	const chrome = 4
	avail := screenBodyHeight(s.terminalHeight) - chrome
	if avail < 3 {
		avail = 3
	}
	return avail
}

func (s *ItemSuppliersScreen) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		s.terminalHeight = m.Height
		s.windowSize = s.computeWindowSize()
		s.scrollIntoView()
		return s, nil
	case itemSuppliersLoadedMsg:
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
	case itemSupplierDeletedMsg:
		s.deleting = false
		s.confirmingDelete = false
		if m.err != nil {
			return s, Status("delete failed: "+m.err.Error(), StatusError)
		}
		s.loading = true
		return s, tea.Batch(Status("supplier link removed", StatusOK), s.load())
	case itemSupplierPrimaryMsg:
		s.busy = false
		if m.err != nil {
			return s, Status("set primary failed: "+m.err.Error(), StatusError)
		}
		s.loading = true
		return s, tea.Batch(Status("primary supplier updated", StatusOK), s.load())
	case tea.KeyMsg:
		if s.confirmingDelete {
			return s.updateConfirmDelete(m)
		}
		if s.busy {
			return s, nil
		}
		switch m.String() {
		case "esc":
			return s, SwitchTo(WSInventory, NewInventoryDetailScreen(s.deps, s.itemID))
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
			return s, s.load()
		case "c":
			return s, SwitchTo(WSInventory, NewItemSupplierFormScreen(s.deps, s.itemID, s.itemName, nil))
		case "E", "enter":
			if row, ok := s.selected(); ok {
				return s, SwitchTo(WSInventory, NewItemSupplierFormScreen(s.deps, s.itemID, s.itemName, &row))
			}
		case "p":
			return s.setPrimary()
		case "x":
			if _, ok := s.selected(); ok {
				s.confirmingDelete = true
			}
		}
	}
	return s, nil
}

func (s *ItemSuppliersScreen) setPrimary() (Screen, tea.Cmd) {
	row, ok := s.selected()
	if !ok {
		return s, nil
	}
	if row.IsPreferred {
		return s, Status("already the primary supplier", StatusWarn)
	}
	s.busy = true
	deps := s.deps
	ctx := s.ctx()
	id := row.ID
	return s, func() tea.Msg {
		_, err := deps.OMS.SetItemSupplierPrimary(ctx, id)
		return itemSupplierPrimaryMsg{err: err}
	}
}

func (s *ItemSuppliersScreen) updateConfirmDelete(m tea.KeyMsg) (Screen, tea.Cmd) {
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
		ctx := s.ctx()
		id := row.ID
		return s, func() tea.Msg {
			return itemSupplierDeletedMsg{err: deps.OMS.DeleteItemSupplier(ctx, id)}
		}
	case "n", "N", "esc":
		s.confirmingDelete = false
	}
	return s, nil
}

func (s *ItemSuppliersScreen) selected() (omsapi.ItemSupplier, bool) {
	if s.cursor < 0 || s.cursor >= len(s.rows) {
		return omsapi.ItemSupplier{}, false
	}
	return s.rows[s.cursor], true
}

func (s *ItemSuppliersScreen) scrollIntoView() {
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

func (s *ItemSuppliersScreen) View() string {
	if s.loading {
		return StyleMuted.Render("Loading suppliers…")
	}
	if s.loadErr != "" {
		return StyleStatusError.Render("Error: ") + s.loadErr + "\n\n" + StyleMuted.Render("r retry · esc back")
	}
	if s.confirmingDelete {
		name := ""
		if row, ok := s.selected(); ok {
			name = itemSupplierName(row)
		}
		var prompt string
		if s.deleting {
			prompt = StyleMuted.Render("Removing…")
		} else {
			prompt = StyleStatusWarn.Render(fmt.Sprintf("Remove supplier link %q? This can't be undone.  y remove · n/esc cancel", name))
		}
		return prompt
	}
	if len(s.rows) == 0 {
		return StyleMuted.Render("No suppliers linked to this item yet.") + "\n\n" +
			StyleMuted.Render("c add supplier · r refresh · esc back")
	}

	var b strings.Builder
	b.WriteString(StyleMuted.Render(fmt.Sprintf("%d supplier link(s)", len(s.rows))) + "\n")
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
	if s.busy {
		b.WriteString(StyleMuted.Render("Working…") + "\n")
	}
	b.WriteString(StyleMuted.Render("j/k move · c add · E edit · p primary · x remove · r refresh · esc back"))
	return b.String()
}

func (s *ItemSuppliersScreen) renderRow(i int) string {
	sup := s.rows[i]
	marker := "  "
	if i == s.cursor {
		marker = "▸ "
	}
	name := itemSupplierName(sup)
	line := marker + name
	if sup.IsPreferred {
		line += " " + StyleStatusOK.Render("★ primary")
	}
	if sup.IsDiscontinued {
		line += " " + StyleMuted.Render("[discontinued]")
	} else if !sup.IsActive {
		line += " " + StyleMuted.Render("[inactive]")
	}
	if i == s.cursor {
		line = StyleSidebarItemActive.Render(marker+name)
		if sup.IsPreferred {
			line += " " + StyleStatusOK.Render("★ primary")
		}
		if sup.IsDiscontinued {
			line += " " + StyleMuted.Render("[discontinued]")
		} else if !sup.IsActive {
			line += " " + StyleMuted.Render("[inactive]")
		}
	}
	out := line + "\n"

	meta := []string{}
	if sup.SupplierSKU != "" {
		meta = append(meta, "SKU "+sup.SupplierSKU)
	}
	if sup.PackQuantity > 0 {
		meta = append(meta, fmt.Sprintf("pack %d", sup.PackQuantity))
	}
	if !sup.UnitCost.Empty() {
		meta = append(meta, "$"+sup.UnitCost.String()+"/unit")
	}
	if !sup.PackageCost.Empty() {
		meta = append(meta, "$"+sup.PackageCost.String()+"/pkg")
	}
	if sup.LeadTimeDays > 0 {
		meta = append(meta, fmt.Sprintf("lead %gd", sup.LeadTimeDays))
	}
	if len(meta) > 0 {
		out += "    " + StyleMuted.Render(strings.Join(meta, " · ")) + "\n"
	}
	if sup.URL != "" {
		out += "    " + StyleMuted.Render(sup.URL) + "\n"
	}
	return strings.TrimRight(out, "\n")
}

// itemSupplierName returns the best display name for a link row, falling back to
// the numeric supplier pk when the serializer didn't embed supplier_name.
func itemSupplierName(sup omsapi.ItemSupplier) string {
	if sup.SupplierName != "" {
		return sup.SupplierName
	}
	return fmt.Sprintf("supplier %d", sup.Supplier)
}

// ===========================================================================
// ItemSupplierFormScreen — create / edit one supplier link
// ===========================================================================

const (
	isSupplier = iota
	isSKU
	isURL
	isUnitCost
	isPackageCost
	isQtyPerPackage
	isLeadTime
	isPrimary
	isFieldMax
)

var itemSupplierFieldLabel = map[int]string{
	isSupplier:      "Supplier",
	isSKU:           "Supplier SKU",
	isURL:           "Supplier URL",
	isUnitCost:      "Unit cost",
	isPackageCost:   "Package cost",
	isQtyPerPackage: "Quantity per package",
	isLeadTime:      "Average lead time (days)",
	isPrimary:       "Primary supplier",
}

func itemSupplierFieldIsText(id int) bool {
	switch id {
	case isSKU, isURL, isUnitCost, isPackageCost, isQtyPerPackage, isLeadTime:
		return true
	}
	return false
}

type itemSupplierFormPhase int

const (
	isPhaseForm itemSupplierFormPhase = iota
	isPhaseSupplierPick
)

// itemSupplierPickOption is one row in the supplier sub-picker.
type itemSupplierPickOption struct {
	id    int
	label string
}

type ItemSupplierFormScreen struct {
	deps     Deps
	edit     bool
	itemID   string
	itemName string
	rowID    int // ItemSupplier pk (edit mode)
	existing *omsapi.ItemSupplier

	loading bool
	loadErr string
	saving  bool
	errMsg  string

	suppliers []omsapi.Supplier

	terminalHeight int

	inputs     []textinput.Model
	supplierID *int
	isPrimary  bool

	fields []int
	cursor int

	// Supplier sub-picker state.
	phase       itemSupplierFormPhase
	pickCursor  int
	pickSearch  textinput.Model
	pickTyping  bool
	pickOptions []itemSupplierPickOption
}

type itemSupplierFormSuppliersMsg struct {
	suppliers []omsapi.Supplier
	err       error
}

type itemSupplierSavedMsg struct {
	link *omsapi.ItemSupplier
	err  error
}

// NewItemSupplierFormScreen builds the create/edit form. A nil existing opens
// create mode (against itemID); a non-nil existing opens edit mode and hydrates
// every field from that link row (no re-fetch — the list already has it).
func NewItemSupplierFormScreen(deps Deps, itemID, itemName string, existing *omsapi.ItemSupplier) *ItemSupplierFormScreen {
	s := &ItemSupplierFormScreen{
		deps:     deps,
		edit:     existing != nil,
		itemID:   itemID,
		itemName: itemName,
		existing: existing,
		loading:  true,
	}
	if existing != nil {
		s.rowID = existing.ID
	}

	s.inputs = make([]textinput.Model, isFieldMax)
	for id := 0; id < isFieldMax; id++ {
		if !itemSupplierFieldIsText(id) {
			continue
		}
		ti := textinput.New()
		ti.Prompt = ""
		ti.CharLimit = itemSupplierCharLimit(id)
		ti.Placeholder = itemSupplierPlaceholder(id)
		s.inputs[id] = ti
	}
	// Create-mode numeric defaults mirror the ItemSupplier model defaults.
	if existing == nil {
		s.inputs[isQtyPerPackage].SetValue("1")
		s.inputs[isLeadTime].SetValue("7")
	}

	s.pickSearch = textinput.New()
	s.pickSearch.Prompt = ""
	s.pickSearch.Placeholder = "filter"
	s.pickSearch.CharLimit = 60

	s.fields = []int{isSupplier, isSKU, isURL, isUnitCost, isPackageCost, isQtyPerPackage, isLeadTime, isPrimary}
	s.syncFocus()
	return s
}

func itemSupplierCharLimit(id int) int {
	switch id {
	case isSKU:
		return 100
	case isURL:
		return 300
	case isUnitCost, isPackageCost:
		return 20
	case isQtyPerPackage, isLeadTime:
		return 10
	}
	return 100
}

func itemSupplierPlaceholder(id int) string {
	switch id {
	case isSKU:
		return "supplier's product SKU"
	case isURL:
		return "https://… (optional)"
	case isUnitCost:
		return "e.g. 1.50 (optional)"
	case isPackageCost:
		return "e.g. 12.00 (optional)"
	case isQtyPerPackage:
		return "1"
	case isLeadTime:
		return "7"
	}
	return ""
}

func (s *ItemSupplierFormScreen) Title() string {
	if s.edit {
		return "Edit supplier link"
	}
	if s.itemName != "" {
		return "Add supplier: " + s.itemName
	}
	return "Add supplier link"
}

func (s *ItemSupplierFormScreen) WantsRawInput() bool { return true }

func (s *ItemSupplierFormScreen) Init() tea.Cmd {
	return tea.Batch(s.loadSuppliers(), textinput.Blink)
}

func (s *ItemSupplierFormScreen) ctx() context.Context {
	if s.deps.Ctx != nil {
		return s.deps.Ctx
	}
	return context.Background()
}

func (s *ItemSupplierFormScreen) loadSuppliers() tea.Cmd {
	deps := s.deps
	ctx := s.ctx()
	return func() tea.Msg {
		sups, err := deps.OMS.ListAllSuppliers(ctx)
		return itemSupplierFormSuppliersMsg{suppliers: sups, err: err}
	}
}

func (s *ItemSupplierFormScreen) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		s.terminalHeight = m.Height
		return s, nil
	case itemSupplierFormSuppliersMsg:
		s.loading = false
		if m.err != nil {
			s.loadErr = m.err.Error()
		} else {
			s.suppliers = m.suppliers
			if s.edit {
				s.hydrate()
			}
		}
		s.syncFocus()
		return s, nil
	case itemSupplierSavedMsg:
		s.saving = false
		if m.err != nil {
			s.errMsg = m.err.Error()
			return s, Status("save failed: "+m.err.Error(), StatusError)
		}
		verb := "added"
		if s.edit {
			verb = "updated"
		}
		name := ""
		if m.link != nil {
			name = itemSupplierName(*m.link)
		}
		return s, tea.Batch(
			Status(fmt.Sprintf("supplier link %s: %s", verb, name), StatusOK),
			SwitchTo(WSInventory, NewItemSuppliersScreen(s.deps, s.itemID, s.itemName)),
		)
	case tea.KeyMsg:
		if s.loading {
			if m.String() == "esc" {
				return s, s.cancelCmd()
			}
			return s, nil
		}
		if s.phase == isPhaseSupplierPick {
			return s.updatePickPhase(m)
		}
		return s.updateFormPhase(m)
	}

	// Non-key messages (cursor blink) go to whichever input owns the caret.
	if s.phase == isPhaseSupplierPick {
		var cmd tea.Cmd
		s.pickSearch, cmd = s.pickSearch.Update(msg)
		return s, cmd
	}
	if id, ok := s.currentFieldID(); ok && itemSupplierFieldIsText(id) {
		var cmd tea.Cmd
		s.inputs[id], cmd = s.inputs[id].Update(msg)
		return s, cmd
	}
	return s, nil
}

func (s *ItemSupplierFormScreen) hydrate() {
	ex := s.existing
	if ex == nil {
		return
	}
	id := ex.Supplier
	s.supplierID = &id
	s.inputs[isSKU].SetValue(ex.SupplierSKU)
	s.inputs[isURL].SetValue(ex.URL)
	if !ex.UnitCost.Empty() {
		s.inputs[isUnitCost].SetValue(ex.UnitCost.String())
	}
	if !ex.PackageCost.Empty() {
		s.inputs[isPackageCost].SetValue(ex.PackageCost.String())
	}
	qty := ex.PackQuantity
	if qty <= 0 {
		qty = 1
	}
	s.inputs[isQtyPerPackage].SetValue(strconv.Itoa(qty))
	s.inputs[isLeadTime].SetValue(strconv.Itoa(int(ex.LeadTimeDays)))
	s.isPrimary = ex.IsPreferred
}

func (s *ItemSupplierFormScreen) currentFieldID() (int, bool) {
	if s.cursor < 0 || s.cursor >= len(s.fields) {
		return 0, false
	}
	return s.fields[s.cursor], true
}

func (s *ItemSupplierFormScreen) syncFocus() {
	for id := 0; id < len(s.inputs); id++ {
		if itemSupplierFieldIsText(id) {
			s.inputs[id].Blur()
		}
	}
	if id, ok := s.currentFieldID(); ok && itemSupplierFieldIsText(id) {
		s.inputs[id].Focus()
	}
}

func (s *ItemSupplierFormScreen) updateFormPhase(m tea.KeyMsg) (Screen, tea.Cmd) {
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
	case isSupplier:
		if m.String() == " " || m.String() == "enter" {
			s.openPicker()
		}
		return s, nil
	case isPrimary:
		if m.String() == " " {
			s.isPrimary = !s.isPrimary
		}
		return s, nil
	default:
		var cmd tea.Cmd
		s.inputs[id], cmd = s.inputs[id].Update(m)
		return s, cmd
	}
}

func (s *ItemSupplierFormScreen) moveCursor(delta int) {
	n := len(s.fields)
	if n == 0 {
		return
	}
	s.cursor = (s.cursor + delta + n) % n
	s.syncFocus()
}

// ---------------------------------------------------------------------------
// Supplier sub-picker
// ---------------------------------------------------------------------------

func (s *ItemSupplierFormScreen) openPicker() {
	s.phase = isPhaseSupplierPick
	s.pickTyping = false
	s.pickSearch.SetValue("")
	s.pickSearch.Blur()
	s.applyPickFilter()

	// Start the cursor on the currently-selected supplier so re-picking is a
	// no-op keystroke.
	s.pickCursor = 0
	if s.supplierID != nil {
		for i, o := range s.pickOptions {
			if o.id == *s.supplierID {
				s.pickCursor = i
				break
			}
		}
	}
}

func (s *ItemSupplierFormScreen) applyPickFilter() {
	q := strings.ToLower(strings.TrimSpace(s.pickSearch.Value()))
	opts := []itemSupplierPickOption{}
	for _, sup := range s.suppliers {
		label := sup.Name
		if sup.SupplierType != "" {
			label = fmt.Sprintf("%s (%s)", sup.Name, sup.SupplierType)
		}
		if q == "" || strings.Contains(strings.ToLower(label), q) {
			opts = append(opts, itemSupplierPickOption{id: sup.ID, label: label})
		}
	}
	s.pickOptions = opts
	if s.pickCursor >= len(s.pickOptions) {
		s.pickCursor = 0
	}
}

func (s *ItemSupplierFormScreen) updatePickPhase(m tea.KeyMsg) (Screen, tea.Cmd) {
	if s.pickTyping {
		switch m.Type {
		case tea.KeyEsc:
			s.pickTyping = false
			s.pickSearch.Blur()
			return s, nil
		case tea.KeyEnter:
			s.pickTyping = false
			s.pickSearch.Blur()
			s.applyPickFilter()
			s.pickCursor = 0
			return s, nil
		}
		var cmd tea.Cmd
		s.pickSearch, cmd = s.pickSearch.Update(m)
		s.applyPickFilter()
		return s, cmd
	}

	switch m.String() {
	case "esc":
		s.phase = isPhaseForm
		s.syncFocus()
	case "j", "down":
		if s.pickCursor < len(s.pickOptions)-1 {
			s.pickCursor++
		}
	case "k", "up":
		if s.pickCursor > 0 {
			s.pickCursor--
		}
	case "/":
		s.pickTyping = true
		s.pickSearch.Focus()
		return s, textinput.Blink
	case "enter":
		s.commitPick()
	}
	return s, nil
}

func (s *ItemSupplierFormScreen) commitPick() {
	if s.pickCursor >= 0 && s.pickCursor < len(s.pickOptions) {
		id := s.pickOptions[s.pickCursor].id
		s.supplierID = &id
	}
	s.phase = isPhaseForm
	s.pickTyping = false
	s.pickSearch.SetValue("")
	s.pickSearch.Blur()
	s.syncFocus()
}

// ---------------------------------------------------------------------------
// Submit
// ---------------------------------------------------------------------------

func (s *ItemSupplierFormScreen) submit() (Screen, tea.Cmd) {
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
	rowID := s.rowID
	return s, func() tea.Msg {
		var link *omsapi.ItemSupplier
		var e error
		if edit {
			link, e = deps.OMS.UpdateItemSupplier(ctx, rowID, body)
		} else {
			link, e = deps.OMS.CreateItemSupplier(ctx, body)
		}
		return itemSupplierSavedMsg{link: link, err: e}
	}
}

func (s *ItemSupplierFormScreen) buildPayload() (omsapi.ItemSupplierWrite, error) {
	var w omsapi.ItemSupplierWrite
	if strings.TrimSpace(s.itemID) == "" {
		return w, errors.New("no item context")
	}
	if s.supplierID == nil {
		return w, errors.New("supplier is required")
	}
	sku := strings.TrimSpace(s.inputs[isSKU].Value())
	if sku == "" {
		return w, errors.New("supplier SKU is required")
	}

	qty, err := itemSupplierParseInt(s.inputs[isQtyPerPackage].Value(), 1)
	if err != nil || qty < 1 {
		return w, errors.New("quantity per package must be a whole number ≥ 1")
	}
	lead, err := itemSupplierParseInt(s.inputs[isLeadTime].Value(), 7)
	if err != nil || lead < 0 {
		return w, errors.New("average lead time must be a whole number ≥ 0")
	}

	unitCost, err := itemSupplierParseMoney(s.inputs[isUnitCost].Value())
	if err != nil {
		return w, errors.New("unit cost must be a number")
	}
	packageCost, err := itemSupplierParseMoney(s.inputs[isPackageCost].Value())
	if err != nil {
		return w, errors.New("package cost must be a number")
	}

	w = omsapi.ItemSupplierWrite{
		Item:               s.itemID,
		Supplier:           *s.supplierID,
		SupplierSKU:        sku,
		SupplierURL:        strings.TrimSpace(s.inputs[isURL].Value()),
		UnitCost:           unitCost,
		PackageCost:        packageCost,
		QuantityPerPackage: qty,
		AverageLeadTime:    lead,
		IsPrimary:          s.isPrimary,
	}
	return w, nil
}

// itemSupplierParseInt parses an optional whole-number field, returning def when
// the field is blank.
func itemSupplierParseInt(raw string, def int) (int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return def, nil
	}
	return strconv.Atoi(raw)
}

// itemSupplierParseMoney validates an optional decimal field and returns it as a
// *string: nil when blank (sent as explicit null so an edit can clear it), the
// trimmed original string otherwise (preserving the entered precision — the
// backend parses the decimal).
func itemSupplierParseMoney(raw string) (*string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	if _, err := strconv.ParseFloat(raw, 64); err != nil {
		return nil, err
	}
	return &raw, nil
}

func (s *ItemSupplierFormScreen) cancelCmd() tea.Cmd {
	return SwitchTo(WSInventory, NewItemSuppliersScreen(s.deps, s.itemID, s.itemName))
}

// ---------------------------------------------------------------------------
// View
// ---------------------------------------------------------------------------

func (s *ItemSupplierFormScreen) View() string {
	if s.loading {
		return StyleMuted.Render("Loading suppliers…")
	}
	if s.loadErr != "" {
		return StyleStatusError.Render("Error: ") + s.loadErr + "\n\n" + StyleMuted.Render("esc to go back")
	}
	if s.phase == isPhaseSupplierPick {
		return s.viewPick()
	}
	return s.viewForm()
}

func (s *ItemSupplierFormScreen) viewForm() string {
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

func (s *ItemSupplierFormScreen) renderField(i int) string {
	id := s.fields[i]
	caret := "  "
	if i == s.cursor {
		caret = "▸ "
	}
	label := itemSupplierFieldLabel[id]
	var value string
	switch id {
	case isSupplier:
		value = s.supplierLabel()
	case isPrimary:
		if s.isPrimary {
			value = StyleStatusOK.Render("[x] yes")
		} else {
			value = StyleMuted.Render("[ ] no")
		}
	default:
		value = s.inputs[id].View()
	}
	return caret + StyleTitle.Render(label+": ") + value
}

func (s *ItemSupplierFormScreen) supplierLabel() string {
	if s.supplierID == nil {
		return StyleMuted.Render("(press space to pick)")
	}
	for _, sup := range s.suppliers {
		if sup.ID == *s.supplierID {
			return sup.Name
		}
	}
	if s.existing != nil && s.existing.SupplierName != "" {
		return s.existing.SupplierName
	}
	return fmt.Sprintf("#%d", *s.supplierID)
}

func (s *ItemSupplierFormScreen) helpText() string {
	kindHelp := "type to edit"
	if id, ok := s.currentFieldID(); ok {
		switch id {
		case isSupplier:
			kindHelp = "space to pick"
		case isPrimary:
			kindHelp = "space toggle"
		}
	}
	return kindHelp + " · tab/↑↓ move · enter save · esc cancel"
}

func (s *ItemSupplierFormScreen) viewPick() string {
	var b strings.Builder
	b.WriteString(StyleMuted.Render("Pick supplier — j/k move · / filter · enter select · esc back") + "\n\n")
	if s.pickTyping || s.pickSearch.Value() != "" {
		b.WriteString(StyleMuted.Render("filter: ") + s.pickSearch.View() + "\n\n")
	}
	if len(s.pickOptions) == 0 {
		b.WriteString(StyleMuted.Render("(no matching suppliers — create one first with the U hotkey)"))
		return b.String()
	}

	const window = 12
	start, end := fieldWindow(s.pickCursor, len(s.pickOptions), window)
	if start > 0 {
		b.WriteString(StyleMuted.Render(fmt.Sprintf("  ↑ %d more above", start)) + "\n")
	}
	for i := start; i < end; i++ {
		caret := "    "
		if i == s.pickCursor {
			caret = "  ▸ "
		}
		label := s.pickOptions[i].label
		line := caret + label
		if i == s.pickCursor {
			line = StyleSidebarItemActive.Render(caret + label)
		}
		b.WriteString(line + "\n")
	}
	if end < len(s.pickOptions) {
		b.WriteString(StyleMuted.Render(fmt.Sprintf("  ↓ %d more below", len(s.pickOptions)-end)) + "\n")
	}
	return b.String()
}
