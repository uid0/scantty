// AssetPartFormScreen — create/edit form for an asset's parts (the web's
// "consumable supplies" / AssetSuppliesSection). It mirrors the FULL writable
// AssetPartSerializer set the web form carries so an operator can add or amend
// the parts an asset consumes/wears without switching to the browser
// ([[ship-complete-features]]).
//
// The owning asset is fixed context (the form is always opened from one asset's
// parts list), so it is shown in the header, not as an editable field — matching
// the web, where the supplies editor is embedded inside a single asset's form.
// The editable field set is exactly the web's five inputs:
//
//	Part                  — picker, the InventoryItem this part is (UUID pk), required
//	Quantity needed       — number, default 1, min 1
//	Required              — toggle, default true ("required for the asset to operate")
//	Replace every (days)  — number, nullable ("blank = on demand" — clears the field)
//	Notes                 — text
//
// last_replaced_at is writable server-side but the web never edits it as a form
// field; it is driven by the mark-replaced action on the parts list, so it is
// intentionally absent here. Structure follows asset_form.go's field-id-iota +
// field-kind + form/pick sub-phase idiom and reuses its ak* kind constants.
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

// Field identifiers, in render order.
const (
	apfPart     = iota // picker (InventoryItem UUID pk)
	apfQuantity        // number
	apfRequired        // toggle
	apfInterval        // number (nullable)
	apfNotes           // text
	apfFieldMax
)

func assetPartFieldKindOf(id int) assetFieldKind {
	switch id {
	case apfPart:
		return akPicker
	case apfQuantity, apfInterval:
		return akNumber
	case apfRequired:
		return akToggle
	case apfNotes:
		return akText
	}
	return akText
}

func assetPartIsTextKind(id int) bool {
	k := assetPartFieldKindOf(id)
	return k == akText || k == akNumber
}

var assetPartFieldLabel = map[int]string{
	apfPart:     "Part (inventory item)",
	apfQuantity: "Quantity needed",
	apfRequired: "Required",
	apfInterval: "Replace every (days)",
	apfNotes:    "Notes",
}

type AssetPartFormScreen struct {
	deps      Deps
	edit      bool
	assetID   string
	assetName string
	partID    string // stringified AssetPart pk (edit mode)

	loading bool
	loadErr string
	saving  bool
	errMsg  string

	items       []omsapi.Item     // part picker source
	part        *omsapi.AssetPart // edit-mode hydration
	refArrived  bool
	partArrived bool

	terminalHeight int

	inputs     []textinput.Model // text/number slots, indexed by field id
	isRequired bool
	partItemID *string // selected InventoryItem UUID (nil == unset)

	fields []int
	cursor int

	// Picker sub-phase (the single part picker).
	phase       assetFormPhase
	pickCursor  int
	pickSearch  textinput.Model
	pickTyping  bool
	pickOptions []assetPickRow
}

type assetPartRefLoadedMsg struct {
	items []omsapi.Item
	err   error
}

type assetPartFormLoadedMsg struct {
	part *omsapi.AssetPart
	err  error
}

type assetPartFormSavedMsg struct {
	part *omsapi.AssetPart
	err  error
}

// NewAssetPartFormScreen builds the create/edit form. An empty partID opens
// create mode for assetID; a non-empty partID opens edit mode and hydrates from
// the fetched part. assetName is used only for the header.
func NewAssetPartFormScreen(deps Deps, assetID, assetName, partID string) *AssetPartFormScreen {
	edit := strings.TrimSpace(partID) != ""
	s := &AssetPartFormScreen{
		deps:       deps,
		edit:       edit,
		assetID:    strings.TrimSpace(assetID),
		assetName:  assetName,
		partID:     strings.TrimSpace(partID),
		loading:    true,
		isRequired: true, // web default
	}

	s.inputs = make([]textinput.Model, apfFieldMax)
	for id := 0; id < apfFieldMax; id++ {
		if !assetPartIsTextKind(id) {
			continue
		}
		ti := textinput.New()
		ti.Prompt = ""
		ti.CharLimit = assetPartCharLimitFor(id)
		ti.Placeholder = assetPartPlaceholderFor(id)
		s.inputs[id] = ti
	}
	if !edit {
		s.inputs[apfQuantity].SetValue("1") // web default (min 1)
	}

	s.pickSearch = textinput.New()
	s.pickSearch.Prompt = ""
	s.pickSearch.Placeholder = "filter"
	s.pickSearch.CharLimit = 60

	s.fields = []int{apfPart, apfQuantity, apfRequired, apfInterval, apfNotes}
	s.syncFocus()
	return s
}

func assetPartCharLimitFor(id int) int {
	switch id {
	case apfQuantity, apfInterval:
		return 6
	case apfNotes:
		return 1000
	default:
		return 200
	}
}

func assetPartPlaceholderFor(id int) string {
	switch id {
	case apfQuantity:
		return "1"
	case apfInterval:
		return "blank = on demand"
	case apfNotes:
		return "optional"
	default:
		return ""
	}
}

func (s *AssetPartFormScreen) Title() string {
	if s.edit {
		return "Edit part"
	}
	if s.assetName != "" {
		return "New part: " + s.assetName
	}
	return "New part"
}

func (s *AssetPartFormScreen) WantsRawInput() bool { return true }

func (s *AssetPartFormScreen) Init() tea.Cmd {
	cmds := []tea.Cmd{s.loadRefData(), textinput.Blink}
	if s.edit {
		cmds = append(cmds, s.loadPart())
	}
	return tea.Batch(cmds...)
}

func (s *AssetPartFormScreen) ctx() context.Context {
	if s.deps.Ctx != nil {
		return s.deps.Ctx
	}
	return context.Background()
}

// loadRefData fetches the InventoryItem list backing the part picker. It pulls
// EVERY item (ListAllItems, not a single page): the part FK is required, so an
// item beyond page 1 must still be selectable on create, and on edit the linked
// part must be present so re-opening the picker can't silently clear it. A
// failure here means the form can't be used — surface it as a load error rather
// than an empty picker.
func (s *AssetPartFormScreen) loadRefData() tea.Cmd {
	deps := s.deps
	ctx := s.ctx()
	return func() tea.Msg {
		items, err := deps.OMS.ListAllItems(ctx)
		if err != nil {
			return assetPartRefLoadedMsg{err: err}
		}
		return assetPartRefLoadedMsg{items: items}
	}
}

func (s *AssetPartFormScreen) loadPart() tea.Cmd {
	deps := s.deps
	ctx := s.ctx()
	id := s.partID
	return func() tea.Msg {
		p, err := deps.OMS.GetAssetPart(ctx, id)
		return assetPartFormLoadedMsg{part: p, err: err}
	}
}

func (s *AssetPartFormScreen) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		s.terminalHeight = m.Height
		return s, nil

	case assetPartRefLoadedMsg:
		s.refArrived = true
		if m.err != nil {
			s.loadErr = m.err.Error()
		} else {
			s.items = m.items
		}
		return s, s.maybeFinalizeLoad()

	case assetPartFormLoadedMsg:
		s.partArrived = true
		if m.err != nil {
			s.loadErr = m.err.Error()
		} else {
			s.part = m.part
		}
		return s, s.maybeFinalizeLoad()

	case assetPartFormSavedMsg:
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
		if m.part != nil {
			name = m.part.PartName
		}
		return s, tea.Batch(
			Status(strings.TrimSpace(fmt.Sprintf("part %s %s", verb, name)), StatusOK),
			SwitchTo(WSAssets, NewAssetPartsScreen(s.deps, s.assetID, s.assetName)),
		)

	case tea.KeyMsg:
		if s.loading {
			if m.String() == "esc" {
				return s, s.cancelCmd()
			}
			return s, nil
		}
		if s.phase == assetPhasePick {
			return s.updatePickPhase(m)
		}
		return s.updateFormPhase(m)
	}

	// Non-key messages (cursor blink) go to whichever input owns the caret.
	if s.phase == assetPhasePick {
		var cmd tea.Cmd
		s.pickSearch, cmd = s.pickSearch.Update(msg)
		return s, cmd
	}
	if id, ok := s.currentFieldID(); ok && assetPartIsTextKind(id) {
		var cmd tea.Cmd
		s.inputs[id], cmd = s.inputs[id].Update(msg)
		return s, cmd
	}
	return s, nil
}

func (s *AssetPartFormScreen) maybeFinalizeLoad() tea.Cmd {
	if !s.refArrived {
		return nil
	}
	if s.edit && !s.partArrived {
		return nil
	}
	s.loading = false
	if s.loadErr == "" && s.edit && s.part != nil {
		s.hydrate()
	}
	s.syncFocus()
	return nil
}

func (s *AssetPartFormScreen) hydrate() {
	p := s.part
	if p.QuantityNeeded > 0 {
		s.inputs[apfQuantity].SetValue(strconv.Itoa(p.QuantityNeeded))
	}
	s.isRequired = p.IsRequired
	if p.MaintenanceIntervalDays != nil {
		s.inputs[apfInterval].SetValue(strconv.Itoa(*p.MaintenanceIntervalDays))
	}
	s.inputs[apfNotes].SetValue(p.Notes)
	if strings.TrimSpace(p.Part) != "" {
		v := p.Part
		s.partItemID = &v
	}
}

func (s *AssetPartFormScreen) currentFieldID() (int, bool) {
	if s.cursor < 0 || s.cursor >= len(s.fields) {
		return 0, false
	}
	return s.fields[s.cursor], true
}

func (s *AssetPartFormScreen) syncFocus() {
	for id := 0; id < len(s.inputs); id++ {
		if assetPartIsTextKind(id) {
			s.inputs[id].Blur()
		}
	}
	if id, ok := s.currentFieldID(); ok && assetPartIsTextKind(id) {
		s.inputs[id].Focus()
	}
}

// ---------------------------------------------------------------------------
// Form phase
// ---------------------------------------------------------------------------

func (s *AssetPartFormScreen) updateFormPhase(m tea.KeyMsg) (Screen, tea.Cmd) {
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
	switch assetPartFieldKindOf(id) {
	case akToggle:
		if m.String() == " " {
			s.isRequired = !s.isRequired
		}
		return s, nil
	case akPicker:
		if m.String() == " " {
			s.openPicker()
			return s, textinput.Blink
		}
		return s, nil
	default:
		var cmd tea.Cmd
		s.inputs[id], cmd = s.inputs[id].Update(m)
		return s, cmd
	}
}

func (s *AssetPartFormScreen) moveCursor(delta int) {
	n := len(s.fields)
	if n == 0 {
		return
	}
	s.cursor = (s.cursor + delta + n) % n
	s.syncFocus()
}

// ---------------------------------------------------------------------------
// Picker sub-phase (the part / InventoryItem)
// ---------------------------------------------------------------------------

func (s *AssetPartFormScreen) openPicker() {
	s.phase = assetPhasePick
	s.pickTyping = false
	s.pickSearch.SetValue("")
	s.pickSearch.Blur()
	s.applyPickFilter()

	s.pickCursor = 0
	if s.partItemID != nil {
		for i, o := range s.pickOptions {
			if !o.clear && o.key == *s.partItemID {
				s.pickCursor = i
				break
			}
		}
	}
}

func (s *AssetPartFormScreen) applyPickFilter() {
	q := strings.ToLower(strings.TrimSpace(s.pickSearch.Value()))
	opts := []assetPickRow{{clear: true, label: "(none)"}}
	found := false
	for _, it := range s.items {
		label := it.Name
		if it.SKU != "" {
			label = fmt.Sprintf("%s (%s)", it.Name, it.SKU)
		}
		if s.partItemID != nil && it.ID == *s.partItemID {
			found = true
		}
		if q == "" || strings.Contains(strings.ToLower(label), q) {
			opts = append(opts, assetPickRow{key: it.ID, label: label})
		}
	}
	// Belt-and-suspenders: if the currently-linked part isn't in the loaded set
	// at all (e.g. the InventoryItem was since deactivated/deleted), still show
	// it as a selectable row so the cursor can rest on it and enter re-selects
	// rather than falling on "(none)" and clearing a required FK on edit.
	if s.partItemID != nil && !found {
		opts = append(opts, assetPickRow{key: *s.partItemID, label: s.selectedPartLabel()})
	}
	s.pickOptions = opts
	if s.pickCursor >= len(s.pickOptions) {
		s.pickCursor = 0
	}
}

// selectedPartLabel renders a human label for the currently-linked part using
// the fetched AssetPart's denormalized name/SKU — used both for the injected
// picker fallback and the form's picker-value line when the item isn't in the
// loaded list.
func (s *AssetPartFormScreen) selectedPartLabel() string {
	if s.part != nil && strings.TrimSpace(s.part.PartName) != "" {
		if s.part.PartSKU != "" {
			return fmt.Sprintf("%s (%s)", s.part.PartName, s.part.PartSKU)
		}
		return s.part.PartName
	}
	if s.partItemID != nil {
		return "#" + *s.partItemID
	}
	return ""
}

func (s *AssetPartFormScreen) updatePickPhase(m tea.KeyMsg) (Screen, tea.Cmd) {
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
		s.phase = assetPhaseForm
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

func (s *AssetPartFormScreen) commitPick() {
	if s.pickCursor >= 0 && s.pickCursor < len(s.pickOptions) {
		opt := s.pickOptions[s.pickCursor]
		if opt.clear {
			s.partItemID = nil
		} else {
			v := opt.key
			s.partItemID = &v
		}
	}
	s.phase = assetPhaseForm
	s.pickTyping = false
	s.pickSearch.SetValue("")
	s.pickSearch.Blur()
	s.syncFocus()
}

// ---------------------------------------------------------------------------
// Submit
// ---------------------------------------------------------------------------

func (s *AssetPartFormScreen) submit() (Screen, tea.Cmd) {
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
	id := s.partID
	return s, func() tea.Msg {
		var p *omsapi.AssetPart
		var e error
		if edit {
			p, e = deps.OMS.UpdateAssetPart(ctx, id, body)
		} else {
			p, e = deps.OMS.CreateAssetPart(ctx, body)
		}
		return assetPartFormSavedMsg{part: p, err: e}
	}
}

func (s *AssetPartFormScreen) buildPayload() (omsapi.AssetPartWrite, error) {
	var w omsapi.AssetPartWrite

	if s.assetID == "" {
		return w, errors.New("missing asset")
	}
	if s.partItemID == nil {
		return w, errors.New("part is required")
	}

	qty, err := parseAssetPartCount(s.inputs[apfQuantity].Value(), 1)
	if err != nil {
		return w, fmt.Errorf("quantity needed: %w", err)
	}
	if qty < 1 {
		return w, errors.New("quantity needed must be at least 1")
	}

	var interval *int
	if raw := strings.TrimSpace(s.inputs[apfInterval].Value()); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil {
			return w, errors.New("replace-every days must be a whole number")
		}
		if n < 1 {
			return w, errors.New("replace-every days must be at least 1")
		}
		interval = &n
	}

	w = omsapi.AssetPartWrite{
		Asset:                   s.assetID,
		Part:                    *s.partItemID,
		QuantityNeeded:          qty,
		IsRequired:              s.isRequired,
		MaintenanceIntervalDays: interval,
		Notes:                   strings.TrimSpace(s.inputs[apfNotes].Value()),
	}
	return w, nil
}

// parseAssetPartCount parses a positive-integer field, applying def for an empty
// value (quantity defaults to 1, matching the web's NumberInput default).
func parseAssetPartCount(raw string, def int) (int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return def, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return 0, errors.New("must be a whole number")
	}
	return n, nil
}

func (s *AssetPartFormScreen) cancelCmd() tea.Cmd {
	return SwitchTo(WSAssets, NewAssetPartsScreen(s.deps, s.assetID, s.assetName))
}

// ---------------------------------------------------------------------------
// View
// ---------------------------------------------------------------------------

func (s *AssetPartFormScreen) View() string {
	if s.loading {
		return StyleMuted.Render("Loading…")
	}
	if s.loadErr != "" {
		return StyleStatusError.Render("Error: ") + s.loadErr + "\n\n" + StyleMuted.Render("esc to go back")
	}
	if s.phase == assetPhasePick {
		return s.viewPick()
	}
	return s.viewForm()
}

func (s *AssetPartFormScreen) viewForm() string {
	var b strings.Builder
	b.WriteString(StyleMuted.Render(s.helpText()) + "\n")
	if s.assetName != "" {
		b.WriteString(StyleMuted.Render("Asset: ") + s.assetName + "\n")
	}
	b.WriteString("\n")

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

func (s *AssetPartFormScreen) renderField(i int) string {
	id := s.fields[i]
	caret := "  "
	if i == s.cursor {
		caret = "▸ "
	}
	label := assetPartFieldLabel[id]

	var value string
	switch assetPartFieldKindOf(id) {
	case akText, akNumber:
		value = s.inputs[id].View()
	case akToggle:
		if s.isRequired {
			value = StyleStatusOK.Render("[x] yes")
		} else {
			value = StyleMuted.Render("[ ] no")
		}
	case akPicker:
		value = s.partLabel()
	}
	return caret + StyleTitle.Render(label+": ") + value
}

func (s *AssetPartFormScreen) partLabel() string {
	if s.partItemID == nil {
		return StyleMuted.Render("(none — required)")
	}
	for _, it := range s.items {
		if it.ID == *s.partItemID {
			if it.SKU != "" {
				return fmt.Sprintf("%s (%s)", it.Name, it.SKU)
			}
			return it.Name
		}
	}
	// Fallback when the linked item isn't in the loaded set (deactivated/deleted).
	return s.selectedPartLabel()
}

func (s *AssetPartFormScreen) helpText() string {
	kindHelp := "type to edit"
	if id, ok := s.currentFieldID(); ok {
		switch assetPartFieldKindOf(id) {
		case akToggle:
			kindHelp = "space toggle"
		case akPicker:
			kindHelp = "space to pick"
		}
	}
	return kindHelp + " · tab/↑↓ move · enter save · esc cancel"
}

func (s *AssetPartFormScreen) viewPick() string {
	var b strings.Builder
	b.WriteString(StyleMuted.Render("Pick inventory item — j/k move · / filter · enter select · esc back") + "\n\n")
	if s.pickTyping || s.pickSearch.Value() != "" {
		b.WriteString(StyleMuted.Render("filter: ") + s.pickSearch.View() + "\n\n")
	}
	if len(s.pickOptions) == 0 {
		b.WriteString(StyleMuted.Render("(no matches)"))
		return b.String()
	}

	const window = 12
	start, end := fieldWindow(s.pickCursor, len(s.pickOptions), window)
	if start > 0 {
		b.WriteString(StyleMuted.Render(fmt.Sprintf("  ↑ %d more above", start)) + "\n")
	}
	for i := start; i < end; i++ {
		opt := s.pickOptions[i]
		caret := "    "
		if i == s.pickCursor {
			caret = "  ▸ "
		}
		label := opt.label
		if opt.clear {
			label = StyleMuted.Render(opt.label)
		}
		line := caret + label
		if i == s.pickCursor {
			line = StyleSidebarItemActive.Render(caret + opt.label)
		}
		b.WriteString(line + "\n")
	}
	if end < len(s.pickOptions) {
		b.WriteString(StyleMuted.Render(fmt.Sprintf("  ↓ %d more below", len(s.pickOptions)-end)) + "\n")
	}
	return b.String()
}
