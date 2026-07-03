// AssetFormScreen — create/edit form for hard assets.
//
// This is the TUI counterpart to the web AssetFormPage
// (frontend/src/pages/AssetFormPage.tsx + assetFormSchema). It mirrors the FULL
// writable field set the web form exposes so an operator at the workstation can
// register or amend an asset without switching to the browser
// ([[ship-complete-features]]). The structure follows inventory_item_form.go's
// field-by-field entry with sub-phase pickers for the foreign keys — bead #2 of
// the ScanTTY parity program deliberately reuses bead #1's idiom so the later
// CRUD forms (category/location/supplier, …) can mirror it in turn.
//
// Field kinds and how each is edited:
//
//	text / number — a bubbles textinput; type to edit, validated on submit
//	toggle        — a bool; space flips it (is_donation, is_active, the
//	                advanced needs_* / is_chargeable / training_required /
//	                report_only flags). Flipping is_donation shows/hides the
//	                donor-name field.
//	select        — a fixed option list; space or ←/→ cycles (status,
//	                ownership_type). Cycling ownership to "group" reveals the
//	                owning-SIG picker.
//	picker        — a single foreign key chosen from a searchable list in a
//	                sub-phase; space opens it (inventory item, category,
//	                location, owning group)
//	multi-picker  — required_certifications: same sub-phase but space toggles
//	                membership and enter closes; multiple certs can be selected
//
// Two families of AssetFormPage controls are intentionally NOT reproduced here,
// each for a concrete reason rather than to "simplify":
//
//   - The image + manual_pdf file uploads: binary uploads have no meaningful
//     TTY affordance and the asset schema carries no URL-based alternative
//     (unlike the item form's image_url). The inventory item form set the same
//     precedent by dropping its File inputs.
//   - The Supplies (AssetPart) and Maintenance (MaintenanceItem) inline
//     sub-editors: those write *separate* API resources chained after the asset
//     save, not Asset fields, and warrant their own parity beads. The mirror
//     reference (inventory_item_form.go) likewise carries no child-resource
//     editor.
//
// Navigation: tab / ↑↓ move between visible fields, enter saves, esc cancels.
// The form is longer than the pane, so it scrolls to keep the focused field on
// screen.
package tui

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/uid0/scantty/internal/omsapi"
)

// Field identifiers. The order here is the canonical top-to-bottom order the
// form renders (conditional fields are filtered out when their toggle/select is
// off — see rebuildFields).
const (
	afName = iota
	afDescription
	afSerialNumber
	afInventoryItem // picker (UUID pk)
	afCategory      // picker
	afLocation      // picker
	afDateReceived
	afAmountPaid
	afIsDonation  // toggle
	afDonorName   // conditional on afIsDonation
	afStatus      // select
	afOwnership   // select
	afOwningGroup // picker, conditional on ownership == group
	afIsActive    // toggle
	afWikiPageURL
	afProductURL
	afNeedsCompressedAir // toggle
	afNeedsVentilation   // toggle
	afIsChargeable       // toggle
	afTrainingRequired   // toggle
	afRequiredCerts      // multi-picker
	afReportOnly         // toggle
	afConditionNotes
	afNotes
	afFieldMax
)

type assetFieldKind int

const (
	akText assetFieldKind = iota
	akNumber
	akToggle
	akSelect
	akPicker
	akMultiPicker
)

type assetFormPhase int

const (
	assetPhaseForm assetFormPhase = iota
	assetPhasePick
)

// assetStatusOptions mirrors the web form's Status select (assetFormSchema
// status enum). Default is "active".
var assetStatusOptions = []selectOption{
	{"implementing", "Implementing"},
	{"testing", "Testing"},
	{"active", "Active"},
	{"maintenance", "Maintenance"},
	{"retired", "Retired"},
	{"lost", "Lost"},
	{"donated_out", "Donated out"},
}

// assetOwnershipOptions mirrors the web form's Ownership select. Only
// owning_group actually persists (see AssetWrite); "user" is a dead-end in the
// web form too (it has no owning_user picker), so the TUI rejects it on submit
// with a clear message rather than silently no-op'ing.
var assetOwnershipOptions = []selectOption{
	{"space", "Space (Makerspace)"},
	{"group", "Group (SIG)"},
	{"user", "User"},
}

var assetFieldLabel = map[int]string{
	afName:               "Name",
	afDescription:        "Description",
	afSerialNumber:       "Serial number",
	afInventoryItem:      "Inventory item type",
	afCategory:           "Category",
	afLocation:           "Location",
	afDateReceived:       "Date received",
	afAmountPaid:         "Amount paid",
	afIsDonation:         "Donation",
	afDonorName:          "Donor name",
	afStatus:             "Status",
	afOwnership:          "Ownership",
	afOwningGroup:        "Owning SIG",
	afIsActive:           "Active",
	afWikiPageURL:        "Wiki page",
	afProductURL:         "Product page",
	afNeedsCompressedAir: "Needs compressed air",
	afNeedsVentilation:   "Needs ventilation",
	afIsChargeable:       "Chargeable use",
	afTrainingRequired:   "Training required",
	afRequiredCerts:      "Required certifications",
	afReportOnly:         "Report-only",
	afConditionNotes:     "Condition notes",
	afNotes:              "Notes",
}

func assetFieldKindOf(id int) assetFieldKind {
	switch id {
	case afName, afDescription, afSerialNumber, afDateReceived, afDonorName,
		afWikiPageURL, afProductURL, afConditionNotes, afNotes:
		return akText
	case afAmountPaid:
		return akNumber
	case afIsDonation, afIsActive, afNeedsCompressedAir, afNeedsVentilation,
		afIsChargeable, afTrainingRequired, afReportOnly:
		return akToggle
	case afStatus, afOwnership:
		return akSelect
	case afInventoryItem, afCategory, afLocation, afOwningGroup:
		return akPicker
	case afRequiredCerts:
		return akMultiPicker
	}
	return akText
}

func assetIsTextKind(id int) bool {
	k := assetFieldKindOf(id)
	return k == akText || k == akNumber
}

// assetPickRow is one row in a picker sub-phase. key is the option's id as a
// string (a UUID for inventory items, strconv.Itoa for the int-pk fields).
// clear marks the synthetic "(none)" row that unsets a single-value field.
type assetPickRow struct {
	key   string
	label string
	clear bool
}

type AssetFormScreen struct {
	deps    Deps
	edit    bool
	assetID string

	loading bool
	loadErr string
	saving  bool
	errMsg  string

	// Loaded reference data for the pickers + edit-mode hydration.
	categories   []omsapi.Category
	locations    []omsapi.Location
	items        []omsapi.Item
	sigs         []omsapi.SIG
	certs        []omsapi.CertificationOption
	asset        *omsapi.Asset
	refArrived   bool
	assetArrived bool

	terminalHeight int

	// Text/number field storage, indexed by field id. Non-text slots are left
	// as zero-value models and never rendered/updated.
	inputs []textinput.Model

	// Toggle + select state.
	isDonation         bool
	isActive           bool
	needsCompressedAir bool
	needsVentilation   bool
	isChargeable       bool
	trainingRequired   bool
	reportOnly         bool
	statusIdx          int
	ownershipIdx       int

	// Picker selections (nil == unset). inventory_item's pk is a UUID string;
	// the rest are int pks.
	inventoryItemID *string
	categoryID      *int
	locationID      *int
	owningGroupID   *int
	certIDs         []int // sorted set of required-certification pks

	// Visible-field navigation.
	fields []int
	cursor int

	// Picker sub-phase state.
	phase       assetFormPhase
	pickField   int
	pickCursor  int
	pickSearch  textinput.Model
	pickTyping  bool
	pickOptions []assetPickRow
}

type assetFormRefLoadedMsg struct {
	categories []omsapi.Category
	locations  []omsapi.Location
	items      []omsapi.Item
	sigs       []omsapi.SIG
	certs      []omsapi.CertificationOption
	err        error
}

type assetFormLoadedMsg struct {
	asset *omsapi.Asset
	err   error
}

type assetFormSavedMsg struct {
	asset *omsapi.Asset
	err   error
}

// NewAssetFormScreen builds the create/edit form. An empty assetID opens create
// mode with sensible defaults; a non-empty id opens edit mode and hydrates
// every field from the fetched asset.
func NewAssetFormScreen(deps Deps, assetID string) *AssetFormScreen {
	edit := strings.TrimSpace(assetID) != ""
	s := &AssetFormScreen{
		deps:         deps,
		edit:         edit,
		assetID:      strings.TrimSpace(assetID),
		loading:      true,
		isActive:     true, // web default
		statusIdx:    assetStatusIndex("active"),
		ownershipIdx: assetOwnershipIndex("space"),
	}

	s.inputs = make([]textinput.Model, afFieldMax)
	for id := 0; id < afFieldMax; id++ {
		if !assetIsTextKind(id) {
			continue
		}
		ti := textinput.New()
		ti.Prompt = ""
		ti.CharLimit = assetCharLimitFor(id)
		ti.Placeholder = assetPlaceholderFor(id)
		s.inputs[id] = ti
	}
	if !edit {
		s.inputs[afAmountPaid].SetValue("0") // schema default
	}

	s.pickSearch = textinput.New()
	s.pickSearch.Prompt = ""
	s.pickSearch.Placeholder = "filter"
	s.pickSearch.CharLimit = 60

	s.rebuildFields()
	s.syncFocus()
	return s
}

func assetCharLimitFor(id int) int {
	switch id {
	case afName, afDonorName:
		return 200
	case afSerialNumber:
		return 100
	case afDescription, afConditionNotes, afNotes:
		return 1000
	case afWikiPageURL, afProductURL:
		return 500
	case afDateReceived:
		return 10
	case afAmountPaid:
		return 12
	default:
		return 200
	}
}

func assetPlaceholderFor(id int) string {
	switch id {
	case afName:
		return "asset name or model"
	case afSerialNumber:
		return "serial number or unique identifier"
	case afDateReceived:
		return "YYYY-MM-DD"
	case afAmountPaid:
		return "0.00"
	case afWikiPageURL:
		return "https://wiki.example.com/asset"
	case afProductURL:
		return "https://manufacturer.example.com/product"
	case afDonorName:
		return "name of donor"
	case afDescription, afConditionNotes, afNotes:
		return "optional"
	default:
		return ""
	}
}

func assetStatusIndex(v string) int {
	for i, o := range assetStatusOptions {
		if o.value == v {
			return i
		}
	}
	return 2 // "active"
}

func assetOwnershipIndex(v string) int {
	for i, o := range assetOwnershipOptions {
		if o.value == v {
			return i
		}
	}
	return 0 // "space"
}

func (s *AssetFormScreen) Title() string {
	if s.edit {
		if s.asset != nil && s.asset.Name != "" {
			return fmt.Sprintf("Edit asset: %s", s.asset.Name)
		}
		return "Edit asset"
	}
	return "New asset"
}

func (s *AssetFormScreen) WantsRawInput() bool { return true }

func (s *AssetFormScreen) Init() tea.Cmd {
	cmds := []tea.Cmd{s.loadRefData(), textinput.Blink}
	if s.edit {
		cmds = append(cmds, s.loadAsset())
	}
	return tea.Batch(cmds...)
}

func (s *AssetFormScreen) ctx() context.Context {
	if s.deps.Ctx != nil {
		return s.deps.Ctx
	}
	return context.Background()
}

// loadRefData fetches the picker reference data. Categories, locations, and
// inventory items are required — a failure on any of them means the form can't
// be used, so it surfaces as a load error. SIGs (owning-group picker) and the
// certifications catalogue are best-effort: owning_group and
// required_certifications are optional, and the certs endpoint is staff-only
// (403 for members), so a hiccup there leaves those pickers empty rather than
// blocking an operator from registering an asset. This mirrors the web form,
// which wraps the certifications load in a catch that falls back to [].
func (s *AssetFormScreen) loadRefData() tea.Cmd {
	deps := s.deps
	ctx := s.ctx()
	return func() tea.Msg {
		cats, err := deps.OMS.ListCategories(ctx, nil)
		if err != nil {
			return assetFormRefLoadedMsg{err: err}
		}
		locs, err := deps.OMS.ListLocations(ctx, nil)
		if err != nil {
			return assetFormRefLoadedMsg{err: err}
		}
		items, err := deps.OMS.ListItems(ctx, nil)
		if err != nil {
			return assetFormRefLoadedMsg{err: err}
		}
		msg := assetFormRefLoadedMsg{
			categories: cats.Results,
			locations:  locs.Results,
			items:      items.Results,
		}
		if sigs, err := deps.OMS.ListSIGs(ctx, nil); err == nil {
			msg.sigs = sigs.Results
		}
		if certs, err := deps.OMS.ListAvailableCertifications(ctx); err == nil {
			msg.certs = certs
		}
		return msg
	}
}

func (s *AssetFormScreen) loadAsset() tea.Cmd {
	deps := s.deps
	ctx := s.ctx()
	id := s.assetID
	return func() tea.Msg {
		a, err := deps.OMS.GetAsset(ctx, id)
		return assetFormLoadedMsg{asset: a, err: err}
	}
}

func (s *AssetFormScreen) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		s.terminalHeight = m.Height
		return s, nil

	case assetFormRefLoadedMsg:
		s.refArrived = true
		if m.err != nil {
			s.loadErr = m.err.Error()
		} else {
			s.categories = m.categories
			s.locations = m.locations
			s.items = m.items
			s.sigs = m.sigs
			s.certs = m.certs
		}
		return s, s.maybeFinalizeLoad()

	case assetFormLoadedMsg:
		s.assetArrived = true
		if m.err != nil {
			s.loadErr = m.err.Error()
		} else {
			s.asset = m.asset
		}
		return s, s.maybeFinalizeLoad()

	case assetFormSavedMsg:
		s.saving = false
		if m.err != nil {
			s.errMsg = m.err.Error()
			return s, Status("save failed: "+m.err.Error(), StatusError)
		}
		id := s.assetID
		name := ""
		if m.asset != nil {
			id = fmt.Sprint(m.asset.ID)
			name = m.asset.Name
		}
		verb := "created"
		if s.edit {
			verb = "updated"
		}
		return s, tea.Batch(
			Status(fmt.Sprintf("asset %s: %s", verb, name), StatusOK),
			SwitchTo(WSAssets, NewAssetDetailScreen(s.deps, id)),
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
	if id, ok := s.currentFieldID(); ok && assetIsTextKind(id) {
		var cmd tea.Cmd
		s.inputs[id], cmd = s.inputs[id].Update(msg)
		return s, cmd
	}
	return s, nil
}

// maybeFinalizeLoad flips out of the loading state once every in-flight fetch
// has reported. In edit mode that's both the reference data and the asset; in
// create mode just the reference data.
func (s *AssetFormScreen) maybeFinalizeLoad() tea.Cmd {
	if !s.refArrived {
		return nil
	}
	if s.edit && !s.assetArrived {
		return nil
	}
	s.loading = false
	if s.loadErr == "" && s.edit && s.asset != nil {
		s.hydrate()
	}
	s.rebuildFields()
	s.syncFocus()
	return nil
}

// hydrate fills every field from the fetched asset (edit mode).
func (s *AssetFormScreen) hydrate() {
	a := s.asset
	set := func(id int, v string) { s.inputs[id].SetValue(v) }

	set(afName, a.Name)
	set(afDescription, a.Description)
	set(afSerialNumber, a.SerialNumber)
	if id, ok := a.InventoryItemID(); ok {
		v := id
		s.inventoryItemID = &v
	}
	s.categoryID = copyIntPtr(a.Category)
	s.locationID = copyIntPtr(a.Location)

	set(afDateReceived, a.DateReceived)
	if !a.AmountPaid.Empty() {
		set(afAmountPaid, a.AmountPaid.String())
	}
	s.isDonation = a.IsDonation
	set(afDonorName, a.DonorName)

	set(afWikiPageURL, a.WikiPageURL)
	set(afProductURL, a.ProductURL)

	s.statusIdx = assetStatusIndex(a.Status)
	// ownership_type is not part of the AssetSerializer payload, so infer it
	// from owning_group: a group-owned asset carries an owning_group, anything
	// else reads back as space-owned.
	if a.OwningGroup != nil {
		s.ownershipIdx = assetOwnershipIndex("group")
		s.owningGroupID = copyIntPtr(a.OwningGroup)
	} else {
		s.ownershipIdx = assetOwnershipIndex("space")
	}
	s.isActive = a.IsActive

	s.needsCompressedAir = a.NeedsCompressedAir
	s.needsVentilation = a.NeedsVentilation
	s.isChargeable = a.IsChargeable
	s.trainingRequired = a.TrainingRequired
	s.reportOnly = a.ReportOnly
	if len(a.RequiredCertifications) > 0 {
		s.certIDs = append([]int(nil), a.RequiredCertifications...)
		sort.Ints(s.certIDs)
	}

	set(afConditionNotes, a.ConditionNotes)
	set(afNotes, a.Notes)
}

func copyIntPtr(p *int) *int {
	if p == nil {
		return nil
	}
	v := *p
	return &v
}

// rebuildFields recomputes the visible-field list from the current toggle /
// select state, preserving the cursor on the same field id where possible.
func (s *AssetFormScreen) rebuildFields() {
	focused := -1
	if id, ok := s.currentFieldID(); ok {
		focused = id
	}

	f := []int{afName, afDescription, afSerialNumber, afInventoryItem, afCategory,
		afLocation, afDateReceived, afAmountPaid, afIsDonation}
	if s.isDonation {
		f = append(f, afDonorName)
	}
	f = append(f, afStatus, afOwnership)
	if assetOwnershipOptions[s.ownershipIdx].value == "group" {
		f = append(f, afOwningGroup)
	}
	f = append(f, afIsActive, afWikiPageURL, afProductURL, afNeedsCompressedAir,
		afNeedsVentilation, afIsChargeable, afTrainingRequired, afRequiredCerts,
		afReportOnly, afConditionNotes, afNotes)
	s.fields = f

	if focused >= 0 {
		s.setCursorToField(focused)
	}
	if s.cursor >= len(s.fields) {
		s.cursor = len(s.fields) - 1
	}
	if s.cursor < 0 {
		s.cursor = 0
	}
}

func (s *AssetFormScreen) currentFieldID() (int, bool) {
	if s.cursor < 0 || s.cursor >= len(s.fields) {
		return 0, false
	}
	return s.fields[s.cursor], true
}

func (s *AssetFormScreen) setCursorToField(id int) {
	for i, fid := range s.fields {
		if fid == id {
			s.cursor = i
			return
		}
	}
}

func (s *AssetFormScreen) syncFocus() {
	for id := 0; id < len(s.inputs); id++ {
		if assetIsTextKind(id) {
			s.inputs[id].Blur()
		}
	}
	if id, ok := s.currentFieldID(); ok && assetIsTextKind(id) {
		s.inputs[id].Focus()
	}
}

// ---------------------------------------------------------------------------
// Form phase
// ---------------------------------------------------------------------------

func (s *AssetFormScreen) updateFormPhase(m tea.KeyMsg) (Screen, tea.Cmd) {
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
	switch assetFieldKindOf(id) {
	case akToggle:
		if m.String() == " " {
			s.flipToggle(id)
		}
		return s, nil
	case akSelect:
		switch m.String() {
		case " ", "right":
			s.cycleSelect(id, +1)
		case "left":
			s.cycleSelect(id, -1)
		}
		return s, nil
	case akPicker, akMultiPicker:
		if m.String() == " " {
			s.openPicker(id)
			return s, textinput.Blink
		}
		return s, nil
	default:
		var cmd tea.Cmd
		s.inputs[id], cmd = s.inputs[id].Update(m)
		return s, cmd
	}
}

func (s *AssetFormScreen) moveCursor(delta int) {
	n := len(s.fields)
	if n == 0 {
		return
	}
	s.cursor = (s.cursor + delta + n) % n
	s.syncFocus()
}

func (s *AssetFormScreen) flipToggle(id int) {
	switch id {
	case afIsDonation:
		s.isDonation = !s.isDonation
	case afIsActive:
		s.isActive = !s.isActive
	case afNeedsCompressedAir:
		s.needsCompressedAir = !s.needsCompressedAir
	case afNeedsVentilation:
		s.needsVentilation = !s.needsVentilation
	case afIsChargeable:
		s.isChargeable = !s.isChargeable
	case afTrainingRequired:
		s.trainingRequired = !s.trainingRequired
	case afReportOnly:
		s.reportOnly = !s.reportOnly
	}
	s.rebuildFields()
	s.syncFocus()
}

func (s *AssetFormScreen) toggleState(id int) bool {
	switch id {
	case afIsDonation:
		return s.isDonation
	case afIsActive:
		return s.isActive
	case afNeedsCompressedAir:
		return s.needsCompressedAir
	case afNeedsVentilation:
		return s.needsVentilation
	case afIsChargeable:
		return s.isChargeable
	case afTrainingRequired:
		return s.trainingRequired
	case afReportOnly:
		return s.reportOnly
	}
	return false
}

func (s *AssetFormScreen) cycleSelect(id, delta int) {
	switch id {
	case afStatus:
		n := len(assetStatusOptions)
		s.statusIdx = (s.statusIdx + delta + n) % n
	case afOwnership:
		n := len(assetOwnershipOptions)
		s.ownershipIdx = (s.ownershipIdx + delta + n) % n
		// ownership → group reveals (or hides) the owning-SIG picker.
		s.rebuildFields()
		s.syncFocus()
	}
}

// ---------------------------------------------------------------------------
// Picker sub-phase (inventory item / category / location / owning group /
// required certifications)
// ---------------------------------------------------------------------------

func (s *AssetFormScreen) openPicker(id int) {
	s.phase = assetPhasePick
	s.pickField = id
	s.pickTyping = false
	s.pickSearch.SetValue("")
	s.pickSearch.Blur()
	s.applyPickFilter()

	// Start the cursor on the currently-selected option (single-value pickers)
	// so re-picking is a no-op keystroke.
	s.pickCursor = 0
	var selKey string
	switch id {
	case afInventoryItem:
		if s.inventoryItemID != nil {
			selKey = *s.inventoryItemID
		}
	case afCategory:
		if s.categoryID != nil {
			selKey = strconv.Itoa(*s.categoryID)
		}
	case afLocation:
		if s.locationID != nil {
			selKey = strconv.Itoa(*s.locationID)
		}
	case afOwningGroup:
		if s.owningGroupID != nil {
			selKey = strconv.Itoa(*s.owningGroupID)
		}
	}
	if selKey != "" {
		for i, o := range s.pickOptions {
			if !o.clear && o.key == selKey {
				s.pickCursor = i
				break
			}
		}
	}
}

func (s *AssetFormScreen) applyPickFilter() {
	q := strings.ToLower(strings.TrimSpace(s.pickSearch.Value()))
	var opts []assetPickRow
	if s.pickField != afRequiredCerts {
		opts = append(opts, assetPickRow{clear: true, label: "(none)"})
	}
	add := func(key, label string) {
		if q == "" || strings.Contains(strings.ToLower(label), q) {
			opts = append(opts, assetPickRow{key: key, label: label})
		}
	}
	switch s.pickField {
	case afInventoryItem:
		for _, it := range s.items {
			add(it.ID, it.Name)
		}
	case afCategory:
		for _, c := range s.categories {
			add(strconv.Itoa(c.ID), c.Name)
		}
	case afLocation:
		for _, l := range s.locations {
			label := l.Name
			if l.Code != "" {
				label = fmt.Sprintf("%s (%s)", l.Name, l.Code)
			}
			add(strconv.Itoa(l.ID), label)
		}
	case afOwningGroup:
		for _, g := range s.sigs {
			add(strconv.Itoa(g.ID), g.Name)
		}
	case afRequiredCerts:
		for _, c := range s.certs {
			add(strconv.Itoa(c.ID), c.Name)
		}
	}
	s.pickOptions = opts
	if s.pickCursor >= len(s.pickOptions) {
		s.pickCursor = 0
	}
}

func (s *AssetFormScreen) updatePickPhase(m tea.KeyMsg) (Screen, tea.Cmd) {
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

	multi := s.pickField == afRequiredCerts
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
	case " ":
		// In the multi picker, space toggles membership without closing.
		if multi {
			s.toggleCurrentCert()
		}
	case "enter":
		if multi {
			// The multi picker applies toggles live; enter just closes.
			s.phase = assetPhaseForm
			s.syncFocus()
		} else {
			s.commitPick()
		}
	}
	return s, nil
}

func (s *AssetFormScreen) commitPick() {
	if s.pickCursor >= 0 && s.pickCursor < len(s.pickOptions) {
		opt := s.pickOptions[s.pickCursor]
		switch s.pickField {
		case afInventoryItem:
			if opt.clear {
				s.inventoryItemID = nil
			} else {
				v := opt.key
				s.inventoryItemID = &v
			}
		case afCategory:
			s.categoryID = pickInt(opt)
		case afLocation:
			s.locationID = pickInt(opt)
		case afOwningGroup:
			s.owningGroupID = pickInt(opt)
		}
	}
	s.phase = assetPhaseForm
	s.pickTyping = false
	s.pickSearch.SetValue("")
	s.pickSearch.Blur()
	s.syncFocus()
}

// pickInt maps a picker row to an int pk pointer, or nil for the "(none)" row /
// an unparseable key.
func pickInt(opt assetPickRow) *int {
	if opt.clear {
		return nil
	}
	if n, err := strconv.Atoi(opt.key); err == nil {
		return &n
	}
	return nil
}

func (s *AssetFormScreen) toggleCurrentCert() {
	if s.pickCursor < 0 || s.pickCursor >= len(s.pickOptions) {
		return
	}
	opt := s.pickOptions[s.pickCursor]
	if opt.clear {
		return
	}
	id, err := strconv.Atoi(opt.key)
	if err != nil {
		return
	}
	for i, c := range s.certIDs {
		if c == id {
			s.certIDs = append(s.certIDs[:i], s.certIDs[i+1:]...)
			return
		}
	}
	s.certIDs = append(s.certIDs, id)
	sort.Ints(s.certIDs)
}

func (s *AssetFormScreen) certSelected(id int) bool {
	for _, c := range s.certIDs {
		if c == id {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// Submit
// ---------------------------------------------------------------------------

func (s *AssetFormScreen) submit() (Screen, tea.Cmd) {
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
	id := s.assetID
	return s, func() tea.Msg {
		var a *omsapi.Asset
		var e error
		if edit {
			a, e = deps.OMS.UpdateAsset(ctx, id, body)
		} else {
			a, e = deps.OMS.CreateAsset(ctx, body)
		}
		return assetFormSavedMsg{asset: a, err: e}
	}
}

func (s *AssetFormScreen) buildPayload() (omsapi.AssetWrite, error) {
	var w omsapi.AssetWrite

	name := strings.TrimSpace(s.inputs[afName].Value())
	if name == "" {
		return w, errors.New("name is required")
	}

	amount, err := parseAssetAmount(s.inputs[afAmountPaid].Value())
	if err != nil {
		return w, err
	}

	if dr := strings.TrimSpace(s.inputs[afDateReceived].Value()); dr != "" && !validAssetDate(dr) {
		return w, errors.New("date received must be YYYY-MM-DD")
	}

	ownership := assetOwnershipOptions[s.ownershipIdx].value
	var owningGroup *int
	switch ownership {
	case "group":
		if s.owningGroupID == nil {
			return w, errors.New("owning SIG is required when ownership is Group")
		}
		owningGroup = s.owningGroupID
	case "user":
		// The web form has no owning_user picker either, so a user-owned asset
		// can't be completed here — say so instead of silently saving with no
		// owner set.
		return w, errors.New("user-owned assets can't be set from the TUI — use the web app")
	}

	var donor *string
	if s.isDonation {
		donor = strPtrTrim(s.inputs[afDonorName].Value())
	}

	w = omsapi.AssetWrite{
		Name:                   name,
		Description:            strPtrTrim(s.inputs[afDescription].Value()),
		SerialNumber:           strPtrTrim(s.inputs[afSerialNumber].Value()),
		InventoryItem:          s.inventoryItemID,
		Category:               s.categoryID,
		Location:               s.locationID,
		DateReceived:           strPtrTrim(s.inputs[afDateReceived].Value()),
		AmountPaid:             amount,
		IsDonation:             s.isDonation,
		DonorName:              donor,
		WikiPageURL:            strPtrTrim(s.inputs[afWikiPageURL].Value()),
		ProductURL:             strPtrTrim(s.inputs[afProductURL].Value()),
		Status:                 assetStatusOptions[s.statusIdx].value,
		OwnershipType:          ownership,
		OwningGroup:            owningGroup,
		IsActive:               s.isActive,
		NeedsCompressedAir:     s.needsCompressedAir,
		NeedsVentilation:       s.needsVentilation,
		IsChargeable:           s.isChargeable,
		TrainingRequired:       s.trainingRequired,
		RequiredCertifications: s.certIDs,
		ReportOnly:             s.reportOnly,
		Notes:                  strPtrTrim(s.inputs[afNotes].Value()),
		ConditionNotes:         strPtrTrim(s.inputs[afConditionNotes].Value()),
	}
	return w, nil
}

// parseAssetAmount validates the amount-paid field as a non-negative decimal
// and returns it as the string the DecimalField write expects. An empty value
// defaults to "0" (the schema default).
func parseAssetAmount(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "0", nil
	}
	v, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return "", errors.New("amount paid must be a number")
	}
	if v < 0 {
		return "", errors.New("amount paid can't be negative")
	}
	return raw, nil
}

func validAssetDate(v string) bool {
	_, err := time.Parse("2006-01-02", v)
	return err == nil
}

func (s *AssetFormScreen) cancelCmd() tea.Cmd {
	if s.edit && s.assetID != "" {
		return SwitchTo(WSAssets, NewAssetDetailScreen(s.deps, s.assetID))
	}
	return SwitchTo(WSAssets, newScreenFor(WSAssets, s.deps))
}

// ---------------------------------------------------------------------------
// View
// ---------------------------------------------------------------------------

func (s *AssetFormScreen) View() string {
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

func (s *AssetFormScreen) viewForm() string {
	var b strings.Builder
	b.WriteString(StyleMuted.Render(s.helpText()) + "\n\n")

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

func (s *AssetFormScreen) renderField(i int) string {
	id := s.fields[i]
	caret := "  "
	if i == s.cursor {
		caret = "▸ "
	}
	label := assetFieldLabel[id]

	var value string
	switch assetFieldKindOf(id) {
	case akText, akNumber:
		value = s.inputs[id].View()
	case akToggle:
		if s.toggleState(id) {
			value = StyleStatusOK.Render("[x] yes")
		} else {
			value = StyleMuted.Render("[ ] no")
		}
	case akSelect:
		value = s.selectLabel(id)
	case akPicker:
		value = s.pickerLabel(id)
	case akMultiPicker:
		value = s.certsLabel()
	}
	return caret + StyleTitle.Render(label+": ") + value
}

func (s *AssetFormScreen) selectLabel(id int) string {
	switch id {
	case afStatus:
		if s.statusIdx >= 0 && s.statusIdx < len(assetStatusOptions) {
			return "‹ " + assetStatusOptions[s.statusIdx].label + " ›"
		}
	case afOwnership:
		if s.ownershipIdx >= 0 && s.ownershipIdx < len(assetOwnershipOptions) {
			return "‹ " + assetOwnershipOptions[s.ownershipIdx].label + " ›"
		}
	}
	return ""
}

func (s *AssetFormScreen) pickerLabel(id int) string {
	switch id {
	case afInventoryItem:
		if s.inventoryItemID == nil {
			return StyleMuted.Render("(none)")
		}
		for _, it := range s.items {
			if it.ID == *s.inventoryItemID {
				return it.Name
			}
		}
		if s.asset != nil && s.asset.InventoryItemName != "" {
			return s.asset.InventoryItemName
		}
		return "#" + *s.inventoryItemID
	case afCategory:
		if s.categoryID == nil {
			return StyleMuted.Render("(none)")
		}
		for _, c := range s.categories {
			if c.ID == *s.categoryID {
				return c.Name
			}
		}
		return fmt.Sprintf("#%d", *s.categoryID)
	case afLocation:
		if s.locationID == nil {
			return StyleMuted.Render("(none)")
		}
		for _, l := range s.locations {
			if l.ID == *s.locationID {
				return l.Name
			}
		}
		return fmt.Sprintf("#%d", *s.locationID)
	case afOwningGroup:
		if s.owningGroupID == nil {
			return StyleMuted.Render("(none)")
		}
		for _, g := range s.sigs {
			if g.ID == *s.owningGroupID {
				return g.Name
			}
		}
		return fmt.Sprintf("#%d", *s.owningGroupID)
	}
	return ""
}

func (s *AssetFormScreen) certsLabel() string {
	if len(s.certIDs) == 0 {
		return StyleMuted.Render("(none)")
	}
	names := make([]string, 0, len(s.certIDs))
	for _, id := range s.certIDs {
		name := fmt.Sprintf("#%d", id)
		for _, c := range s.certs {
			if c.ID == id {
				name = c.Name
				break
			}
		}
		names = append(names, name)
	}
	return strings.Join(names, ", ")
}

func (s *AssetFormScreen) helpText() string {
	kindHelp := "type to edit"
	if id, ok := s.currentFieldID(); ok {
		switch assetFieldKindOf(id) {
		case akToggle:
			kindHelp = "space toggle"
		case akSelect:
			kindHelp = "space/←→ change"
		case akPicker:
			kindHelp = "space to pick"
		case akMultiPicker:
			kindHelp = "space to choose certs"
		}
	}
	return kindHelp + " · tab/↑↓ move · enter save · esc cancel"
}

// visibleRows returns how many field rows fit given the current terminal
// height, reserving space for the help line, indicators, spacing, and the
// status line.
func (s *AssetFormScreen) visibleRows() int {
	const chrome = 6 // help + blank + blank + status + 2 scroll indicators
	avail := screenBodyHeight(s.terminalHeight) - chrome
	if avail < 3 {
		avail = 3
	}
	return avail
}

func (s *AssetFormScreen) viewPick() string {
	var b strings.Builder
	multi := s.pickField == afRequiredCerts
	what := s.pickWhatLabel()
	if multi {
		b.WriteString(StyleMuted.Render("Choose "+what+" — j/k move · space toggle · / filter · enter done · esc back") + "\n\n")
	} else {
		b.WriteString(StyleMuted.Render("Pick "+what+" — j/k move · / filter · enter select · esc back") + "\n\n")
	}
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
		prefix := ""
		if multi && !opt.clear {
			if id, err := strconv.Atoi(opt.key); err == nil && s.certSelected(id) {
				prefix = "[x] "
			} else {
				prefix = "[ ] "
			}
		}
		label := opt.label
		if opt.clear {
			label = StyleMuted.Render(opt.label)
		}
		line := caret + prefix + label
		if i == s.pickCursor {
			line = StyleSidebarItemActive.Render(caret + prefix + opt.label)
		}
		b.WriteString(line + "\n")
	}
	if end < len(s.pickOptions) {
		b.WriteString(StyleMuted.Render(fmt.Sprintf("  ↓ %d more below", len(s.pickOptions)-end)) + "\n")
	}
	return b.String()
}

func (s *AssetFormScreen) pickWhatLabel() string {
	switch s.pickField {
	case afInventoryItem:
		return "inventory item type"
	case afCategory:
		return "category"
	case afLocation:
		return "location"
	case afOwningGroup:
		return "owning SIG"
	case afRequiredCerts:
		return "required certifications"
	}
	return ""
}
