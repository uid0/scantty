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
//
// It renders through the columnar "JD Edwards" layer (jde_form.go, sc-dnhx):
// one right-aligned label column, the Required flag as a "< Yes >" choice row,
// and a persistent action bar. Enter saves, Esc cancels, Up/Down move, ←/→
// change the flag, and Ctrl-E opens the part picker — whose filter is always
// live, so typing narrows it and the j/k it used to carry are gone.
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
	apfPart:     "Part",
	apfQuantity: "Quantity needed",
	apfRequired: "Required",
	apfInterval: "Replace every",
	apfNotes:    "Notes",
}

// assetPartFieldHint is the muted note drawn AFTER the input area — the unit a
// number is in, what a blank means, which field the backend insists on. It
// carries what used to sit in the labels (a parenthetical there widens the
// shared label column and shoves every input right) and in the placeholders.
var assetPartFieldHint = map[int]string{
	apfQuantity: "per asset",
	apfInterval: "days · blank = on demand",
}

// assetPartFieldWidth sizes the two number fields, which would otherwise read as
// text fields at the default width.
func assetPartFieldWidth(id int) int {
	switch id {
	case apfQuantity, apfInterval:
		return 8
	case apfNotes:
		return 40
	}
	return 0
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

	jdeScreen

	inputs     []textinput.Model // text/number slots, indexed by field id
	isRequired bool
	partItemID *string // selected InventoryItem UUID (nil == unset)

	fields []int
	cursor int

	// Picker sub-phase (the single part picker).
	phase       assetFormPhase
	pickCursor  int
	pickSearch  textinput.Model
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

// assetPartPlaceholderFor keeps only the DEFAULT quantity — worth seeing sitting
// in the field, since a blank one saves as 1. What the others said is now a hint
// beside the input (see assetPartFieldHint).
func assetPartPlaceholderFor(id int) string {
	if id == apfQuantity {
		return "1"
	}
	return ""
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
		s.setSize(m)
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
	case "ctrl+e":
		if id, ok := s.currentFieldID(); ok && assetPartFieldKindOf(id) == akPicker {
			s.openPicker()
			return s, textinput.Blink
		}
		return s, nil
	}

	id, ok := s.currentFieldID()
	if !ok {
		return s, nil
	}
	switch assetPartFieldKindOf(id) {
	case akToggle:
		// A two-value choice row: it flips whichever way it is cycled.
		switch m.String() {
		case " ", "right", "left":
			s.isRequired = !s.isRequired
		}
		return s, nil
	case akPicker:
		// A picker row has nothing to type into and no accelerators left.
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

func (s *AssetPartFormScreen) pageCursor(dir int) {
	if len(s.fields) == 0 {
		return
	}
	s.cursor = jdePageCursor(s.cursor, len(s.fields), s.windowRows(s.formLines(), s.cursor, 0), dir)
	s.syncFocus()
}

// ---------------------------------------------------------------------------
// Picker sub-phase (the part / InventoryItem)
// ---------------------------------------------------------------------------

func (s *AssetPartFormScreen) openPicker() {
	s.phase = assetPhasePick
	s.pickSearch.SetValue("")
	// The filter is always live in a columnar picker, so it holds the caret for
	// as long as the picker is open.
	s.pickSearch.Focus()
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
	switch act, delta := jdePickKey(m); act {
	case jdePickCancel:
		s.closePicker()
	case jdePickCommit:
		s.commitPick()
	case jdePickMove:
		s.movePick(delta)
	case jdePickPage:
		header, body := s.pickView()
		s.movePick(delta * s.windowRows(body, s.pickCursor, len(header)))
	default:
		// Anything else is filter text: the box is always live, so there is no
		// mode to enter and no "/" to remember.
		var cmd tea.Cmd
		s.pickSearch, cmd = s.pickSearch.Update(m)
		s.applyPickFilter()
		return s, cmd
	}
	return s, nil
}

// movePick walks the option cursor, clamping at both ends — running off the
// bottom must not reappear on the "(none)" row, which clears a required FK.
func (s *AssetPartFormScreen) movePick(delta int) {
	next := s.pickCursor + delta
	if next < 0 {
		next = 0
	}
	if next > len(s.pickOptions)-1 {
		next = len(s.pickOptions) - 1
	}
	if next < 0 {
		next = 0
	}
	s.pickCursor = next
}

func (s *AssetPartFormScreen) closePicker() {
	s.phase = assetPhaseForm
	s.pickSearch.SetValue("")
	s.pickSearch.Blur()
	s.syncFocus()
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
	s.closePicker()
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
	body := s.formLines()
	return s.frame(body, s.cursor, jdeStatusLine(s.saving, "Saving…", s.errMsg), s.formBar(body))
}

// formFields describes the part as columnar rows: the part itself is a picker,
// Required a two-value choice, the rest typed into.
func (s *AssetPartFormScreen) formFields() []jdeField {
	out := make([]jdeField, len(s.fields))
	for i, id := range s.fields {
		f := jdeField{
			Label:   assetPartFieldLabel[id],
			Width:   assetPartFieldWidth(id),
			Hint:    assetPartFieldHint[id],
			Focused: i == s.cursor,
		}
		switch assetPartFieldKindOf(id) {
		case akToggle:
			f.Kind, f.Value = jdeChoice, jdeYesNo(s.isRequired)
		case akPicker:
			value, dim := s.partValue()
			f.Kind, f.Value, f.Dim = jdeValue, value, dim
			f.Hint = "required"
			if f.Focused {
				f.Hint = "Ctrl-E picks · required"
			}
		default:
			f.Kind, f.Value = jdeText, jdeInputValue(s.inputs[id], f.Focused)
		}
		out[i] = f
	}
	return out
}

func (s *AssetPartFormScreen) formLines() *jdeLines {
	fields := s.formFields()
	l := &jdeLines{}
	heading := StyleJDEHeading.Render("Part")
	if s.assetName != "" {
		// The owning asset is fixed context, not a field: it names what the
		// sheet is about, the way a JD Edwards form header does.
		heading += "  " + StyleMuted.Render("of ") + s.assetName
	}
	l.Add(heading)
	l.AddFields(fields, jdeLabelWidth(fields), 0)
	return l
}

func (s *AssetPartFormScreen) formBar(body *jdeLines) []actionBarItem {
	items := []actionBarItem{{"Enter", "Save"}, {"Esc", "Cancel"}, {"UP/DN", "Fields"}}
	if id, ok := s.currentFieldID(); ok {
		switch assetPartFieldKindOf(id) {
		case akPicker:
			items = append(items, actionBarItem{"Ctrl-E", "Pick"})
		case akToggle:
			items = append(items, actionBarItem{"←→", "Change"})
		}
	}
	if avail := s.bodyRows(); avail > 0 && body.Len() > avail {
		items = append(items, actionBarItem{"PgUp/PgDn", "Page"})
	}
	return items
}

// partValue is the part row's text and whether it is an empty state. Plain text
// plus a flag, not pre-styled muted text: a focused row reverse-videos the whole
// field, and an inner reset would end the highlight partway through it.
func (s *AssetPartFormScreen) partValue() (string, bool) {
	if s.partItemID == nil {
		return "(none chosen yet)", true
	}
	for _, it := range s.items {
		if it.ID == *s.partItemID {
			if it.SKU != "" {
				return fmt.Sprintf("%s (%s)", it.Name, it.SKU), false
			}
			return it.Name, false
		}
	}
	// Fallback when the linked item isn't in the loaded set (deactivated/deleted).
	return s.selectedPartLabel(), false
}

// pickView builds the part picker's pinned header and its option list.
func (s *AssetPartFormScreen) pickView() ([]string, *jdeLines) {
	return jdePickList{
		Title:  "Part",
		For:    s.assetName,
		Note:   "Row 1 is none — but a part is required to save.",
		Filter: s.pickSearch,
		Count:  len(s.pickOptions),
		Label:  func(i int) string { return s.pickOptions[i].label },
		Dim:    func(i int) bool { return s.pickOptions[i].clear },
		Cursor: s.pickCursor,
		Empty:  "(no matching inventory items)",
	}.render()
}

func (s *AssetPartFormScreen) viewPick() string {
	header, body := s.pickView()
	paging := false
	if avail := s.bodyRows(); avail > 0 && body.Len() > avail-len(header) {
		paging = true
	}
	return s.frameWithHeader(header, body, s.pickCursor,
		jdeStatusLine(false, "", ""), jdePickBar("Select", paging))
}
