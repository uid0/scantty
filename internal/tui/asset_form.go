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
//	toggle        — a bool drawn "< Yes >"; ←/→ flips it (is_donation,
//	                is_active, the advanced needs_* / is_chargeable /
//	                training_required / report_only flags). Flipping
//	                is_donation shows/hides the donor-name field.
//	select        — a fixed option list, also "< value >"; ←/→ cycles (status,
//	                ownership_type). Cycling ownership to "group" or "user"
//	                reveals the matching owner picker.
//	picker        — a single foreign key chosen from a filtered list in a
//	                sub-phase; Ctrl-E opens it (inventory item, category,
//	                location, owning group, owning user)
//	multi-picker  — required_certifications: the same sub-phase, but enter
//	                toggles membership and esc is done; multiple certs can be
//	                selected
//
// Two families of AssetFormPage controls are intentionally NOT reproduced here,
// each for a concrete reason rather than to "simplify":
//
//   - The image upload: unlike manual_pdf (entered as an absolute local path
//     and sent as multipart), the image input has no TTY-native affordance and
//     no URL-based alternative in the asset schema. The inventory item form set
//     the same precedent by dropping its File inputs.
//   - The Supplies (AssetPart) and Maintenance (MaintenanceItem) inline
//     sub-EDITORS: those write *separate* API resources chained after the asset
//     save, not Asset fields, and warrant their own parity beads. The mirror
//     reference (inventory_item_form.go) likewise carries no child-resource
//     editor. The supplies are nonetheless LISTED on the sheet as of sc-hf1z —
//     see asset_form_supplies.go, which draws the parts nested on the fetched
//     asset as a read-only detail grid below the fields.
//
// Navigation: tab / ↑↓ move between visible fields, enter saves, esc cancels.
// The form is longer than the pane, so it scrolls to keep the focused field on
// screen.
//
// It renders through the columnar "JD Edwards" layer (jde_form.go,
// sc-h412/sc-dnhx): one right-aligned label column, a persistent action bar
// naming exactly the keys that apply where the cursor is standing, and — because
// thirty-one fields in one undivided run is a wall — the sheet broken into the
// bands a printed asset record would have (see assetFieldBandOf).
package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
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
	afAssetTag
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
	afOwningUser  // picker, conditional on ownership == user
	afIsActive    // toggle
	afWikiPageURL
	afProductURL
	afManualPDFPath
	afNeedsCompressedAir   // toggle
	afNeedsVentilation     // toggle
	afGeneratesHeatOrFlame // toggle
	afNeedsChilling        // toggle
	afIsChargeable         // toggle
	afTrainingRequired     // toggle
	afRequiredCerts        // multi-picker
	afReportOnly           // toggle
	afSpecialRequirements
	afWorkSafetyNotes
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

// assetOwnershipOptions mirrors the web form's Ownership select.
var assetOwnershipOptions = []selectOption{
	{"space", "Space (Makerspace)"},
	{"group", "Group (SIG)"},
	{"user", "User"},
}

var assetFieldLabel = map[int]string{
	afName:                 "Name",
	afDescription:          "Description",
	afAssetTag:             "Asset tag",
	afSerialNumber:         "Serial number",
	afInventoryItem:        "Inventory item type",
	afCategory:             "Category",
	afLocation:             "Location",
	afDateReceived:         "Date received",
	afAmountPaid:           "Amount paid",
	afIsDonation:           "Donation",
	afDonorName:            "Donor name",
	afStatus:               "Status",
	afOwnership:            "Ownership",
	afOwningGroup:          "Owning SIG",
	afOwningUser:           "Owning user",
	afIsActive:             "Active",
	afWikiPageURL:          "Wiki page",
	afProductURL:           "Product page",
	afManualPDFPath:        "Manual PDF path",
	afNeedsCompressedAir:   "Needs compressed air",
	afNeedsVentilation:     "Needs ventilation",
	afGeneratesHeatOrFlame: "Generates heat or flame",
	afNeedsChilling:        "Needs chilling",
	afIsChargeable:         "Chargeable use",
	afTrainingRequired:     "Training required",
	afRequiredCerts:        "Required certifications",
	afReportOnly:           "Report-only",
	afSpecialRequirements:  "Special requirements",
	afWorkSafetyNotes:      "Work safety notes",
	afConditionNotes:       "Condition notes",
	afNotes:                "Notes",
}

// assetFieldBand groups the sheet into the sections a printed asset record
// would have. Thirty-one fields in one run is a wall (the item form learned this
// in sweep A), and the bands must stay CONTIGUOUS in the rebuildFields order —
// a heading is drawn where its band starts, not wherever the field happens to
// be.
type assetFieldBand int

const (
	assetBandIdentity assetFieldBand = iota
	assetBandClassification
	assetBandAcquisition
	assetBandOwnership
	assetBandDocumentation
	assetBandFacility
	assetBandAccess
	assetBandNotes
)

var assetBandLabel = map[assetFieldBand]string{
	assetBandIdentity:       "Asset",
	assetBandClassification: "Classification & placement",
	assetBandAcquisition:    "Acquisition",
	assetBandOwnership:      "Status & ownership",
	assetBandDocumentation:  "Documentation",
	assetBandFacility:       "Facility requirements",
	assetBandAccess:         "Access & training",
	assetBandNotes:          "Notes",
}

func assetFieldBandOf(id int) assetFieldBand {
	switch id {
	case afInventoryItem, afCategory, afLocation:
		return assetBandClassification
	case afDateReceived, afAmountPaid, afIsDonation, afDonorName:
		return assetBandAcquisition
	case afStatus, afOwnership, afOwningGroup, afOwningUser, afIsActive:
		return assetBandOwnership
	case afWikiPageURL, afProductURL, afManualPDFPath:
		return assetBandDocumentation
	case afNeedsCompressedAir, afNeedsVentilation, afGeneratesHeatOrFlame, afNeedsChilling:
		return assetBandFacility
	case afIsChargeable, afTrainingRequired, afRequiredCerts, afReportOnly:
		return assetBandAccess
	case afSpecialRequirements, afWorkSafetyNotes, afConditionNotes, afNotes:
		return assetBandNotes
	}
	return assetBandIdentity
}

// assetFieldHint carries the format notes that used to live in the
// placeholders. A placeholder long enough to fill the input area leaves no
// underscores, so an empty green-screen row stops reading as empty; the note
// rides after the input instead. A columnar form marks what is REQUIRED rather
// than tagging everything else "(optional)".
var assetFieldHint = map[int]string{
	afName:          "required",
	afAssetTag:      "DMS-ABCD1234",
	afDateReceived:  "YYYY-MM-DD",
	afWikiPageURL:   "https://…",
	afProductURL:    "https://…",
	afManualPDFPath: "absolute local path",
}

// assetFieldWidth sizes the input areas that are not the default.
//
// An input area and its hint have to SHARE what is left of the pane after the
// label column: clampToBox truncates an over-wide row with no ellipsis, so a
// hint pushed past the edge is a hint the operator silently loses (sc-ye0i).
// With this sheet's 23-column labels there are 49 columns for value + hint, and
// the three fields that carry both a wide box and a note are sized against that
// — hence 36 rather than 40 for the URLs, and 26 for the path, whose note is the
// longest on the sheet. A textinput scrolls horizontally, so a long value still
// fits in a short box; a clipped hint has nowhere to go.
func assetFieldWidth(id int) int {
	switch id {
	case afDescription, afSpecialRequirements, afWorkSafetyNotes, afConditionNotes, afNotes:
		return 44
	case afWikiPageURL, afProductURL:
		return 36
	case afManualPDFPath:
		return 26
	case afName:
		return 34
	case afDateReceived:
		return 12
	case afAmountPaid:
		return 10
	}
	return 0
}

func assetFieldKindOf(id int) assetFieldKind {
	switch id {
	case afName, afDescription, afAssetTag, afSerialNumber, afDateReceived, afDonorName,
		afWikiPageURL, afProductURL, afManualPDFPath, afSpecialRequirements, afWorkSafetyNotes,
		afConditionNotes, afNotes:
		return akText
	case afAmountPaid:
		return akNumber
	case afIsDonation, afIsActive, afNeedsCompressedAir, afNeedsVentilation,
		afGeneratesHeatOrFlame, afNeedsChilling, afIsChargeable, afTrainingRequired, afReportOnly:
		return akToggle
	case afStatus, afOwnership:
		return akSelect
	case afInventoryItem, afCategory, afLocation, afOwningGroup, afOwningUser:
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
	users        []omsapi.User
	certs        []omsapi.CertificationOption
	asset        *omsapi.Asset
	refArrived   bool
	assetArrived bool

	jdeScreen

	// Text/number field storage, indexed by field id. Non-text slots are left
	// as zero-value models and never rendered/updated.
	inputs []textinput.Model

	// Toggle + select state.
	isDonation           bool
	isActive             bool
	needsCompressedAir   bool
	needsVentilation     bool
	generatesHeatOrFlame bool
	needsChilling        bool
	isChargeable         bool
	trainingRequired     bool
	reportOnly           bool
	statusIdx            int
	ownershipIdx         int

	// Picker selections (nil == unset). inventory_item's pk is a UUID string;
	// the rest are int pks.
	inventoryItemID *string
	categoryID      *int
	locationID      *int
	owningGroupID   *int
	owningUserID    *int
	certIDs         []int // sorted set of required-certification pks

	// Visible-field navigation. The cursor runs past the fields and into the
	// supplies band, which is what rowCount() counts and every cursor bound
	// below measures against (asset_form_supplies.go).
	fields []int
	cursor int

	// supplyWarn is the supplies band's Ctrl-E door asking before it leaves a
	// sheet with unsaved edits; baseline is the sheet as it loaded, which is how
	// dirty() knows there are any (sc-lvp7).
	supplyWarn bool
	baseline   string

	// Picker sub-phase state.
	phase       assetFormPhase
	pickField   int
	pickCursor  int
	pickSearch  textinput.Model
	pickOptions []assetPickRow
}

type assetFormRefLoadedMsg struct {
	categories []omsapi.Category
	locations  []omsapi.Location
	items      []omsapi.Item
	sigs       []omsapi.SIG
	users      []omsapi.User
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
	case afAssetTag, afSerialNumber:
		return 100
	case afDescription, afSpecialRequirements, afWorkSafetyNotes, afConditionNotes, afNotes:
		return 1000
	case afWikiPageURL, afProductURL, afManualPDFPath:
		return 500
	case afDateReceived:
		return 10
	case afAmountPaid:
		return 12
	default:
		return 200
	}
}

// assetPlaceholderFor is empty for every field now — the formats and examples
// moved to assetFieldHint, which rides after the input area instead of filling
// it (see jde_form.go).
func assetPlaceholderFor(id int) string { return "" }

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
		if users, err := deps.OMS.ListUsers(ctx, nil); err == nil {
			msg.users = users.Results
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
		s.setSize(m)
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
			s.users = m.users
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
	s.snapshotBaseline()
	return nil
}

// hydrate fills every field from the fetched asset (edit mode).
func (s *AssetFormScreen) hydrate() {
	a := s.asset
	set := func(id int, v string) { s.inputs[id].SetValue(v) }

	set(afName, a.Name)
	set(afDescription, a.Description)
	set(afAssetTag, a.AssetTag)
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
	// from the persisted owner fields returned by the detail serializer.
	if a.OwningUser != nil {
		s.ownershipIdx = assetOwnershipIndex("user")
		s.owningUserID = copyIntPtr(a.OwningUser)
	} else if a.OwningGroup != nil {
		s.ownershipIdx = assetOwnershipIndex("group")
		s.owningGroupID = copyIntPtr(a.OwningGroup)
	} else {
		s.ownershipIdx = assetOwnershipIndex("space")
	}
	s.isActive = a.IsActive

	s.needsCompressedAir = a.NeedsCompressedAir
	s.needsVentilation = a.NeedsVentilation
	s.generatesHeatOrFlame = a.GeneratesHeatOrFlame
	s.needsChilling = a.NeedsChilling
	s.isChargeable = a.IsChargeable
	s.trainingRequired = a.TrainingRequired
	s.reportOnly = a.ReportOnly
	if len(a.RequiredCertifications) > 0 {
		s.certIDs = append([]int(nil), a.RequiredCertifications...)
		sort.Ints(s.certIDs)
	}

	set(afSpecialRequirements, a.SpecialRequirements)
	set(afWorkSafetyNotes, a.WorkSafetyNotes)
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

	f := []int{afName, afDescription, afAssetTag, afSerialNumber, afInventoryItem, afCategory,
		afLocation, afDateReceived, afAmountPaid, afIsDonation}
	if s.isDonation {
		f = append(f, afDonorName)
	}
	f = append(f, afStatus, afOwnership)
	switch assetOwnershipOptions[s.ownershipIdx].value {
	case "group":
		f = append(f, afOwningGroup)
	case "user":
		f = append(f, afOwningUser)
	}
	f = append(f, afIsActive, afWikiPageURL, afProductURL, afManualPDFPath, afNeedsCompressedAir,
		afNeedsVentilation, afGeneratesHeatOrFlame, afNeedsChilling, afIsChargeable,
		afTrainingRequired, afRequiredCerts, afReportOnly, afSpecialRequirements,
		afWorkSafetyNotes, afConditionNotes, afNotes)
	s.fields = f

	if focused >= 0 {
		s.setCursorToField(focused)
	}
	// Clamped against the whole sheet, not just the fields: the supplies band
	// below them is navigable too, and a cursor parked there must survive a
	// rebuild (which a conditional field appearing or disappearing triggers).
	if s.cursor >= s.rowCount() {
		s.cursor = s.rowCount() - 1
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
	// The supplies band's confirm is modal while it is up — including over the
	// system keys, since the two it overrides (enter saves, esc discards) are
	// exactly the two outcomes it exists to ask about.
	if s.supplyWarn {
		return s.updateSupplyWarn(m)
	}
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
		// EDIT opens whatever the highlighted row IS: a picker, or — on the
		// supplies band — the parts screen that manages the rows this one only
		// lists. A row you simply type into opens nothing, which is why the bar
		// drops the key there.
		if id, ok := s.currentFieldID(); ok {
			switch assetFieldKindOf(id) {
			case akPicker, akMultiPicker:
				s.openPicker(id)
				return s, textinput.Blink
			}
			return s, nil
		}
		if _, onSupply := s.onSupplyRow(); onSupply {
			return s, s.openSupplyRow()
		}
		return s, nil
	}

	id, ok := s.currentFieldID()
	if !ok {
		return s, nil
	}
	switch assetFieldKindOf(id) {
	case akToggle:
		// A toggle is a two-value choice row, so it flips on the same ←/→ every
		// other bounded set takes (space stays as the pilot's synonym).
		switch m.String() {
		case " ", "left", "right":
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
		// A picker row has nothing to type into and no accelerators left.
		return s, nil
	default:
		var cmd tea.Cmd
		s.inputs[id], cmd = s.inputs[id].Update(m)
		return s, cmd
	}
}

func (s *AssetFormScreen) moveCursor(delta int) {
	body := s.formLines()
	next, ok := s.moveRow(s.cursor, s.rowCount(), delta, 0, s.formBar(body))
	if !ok {
		return
	}
	s.cursor = next
	s.syncFocus()
}

// pageCursor moves a whole pane's worth of rows, clamping where moveCursor
// wraps — on a sheet this long a page that jumped from the last row back to the
// first would lose the operator's place rather than save them keystrokes.
func (s *AssetFormScreen) pageCursor(dir int) {
	body := s.formLines()
	next, ok := s.pageRow(body, s.cursor, s.rowCount(), dir, 0,
		s.formBar(body), s.formBarItems(true))
	if !ok {
		return
	}
	s.cursor = next
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
	case afGeneratesHeatOrFlame:
		s.generatesHeatOrFlame = !s.generatesHeatOrFlame
	case afNeedsChilling:
		s.needsChilling = !s.needsChilling
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
	case afGeneratesHeatOrFlame:
		return s.generatesHeatOrFlame
	case afNeedsChilling:
		return s.needsChilling
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
		switch assetOwnershipOptions[s.ownershipIdx].value {
		case "group":
			s.owningUserID = nil
		case "user":
			s.owningGroupID = nil
		default:
			s.owningGroupID = nil
			s.owningUserID = nil
		}
		s.rebuildFields()
		s.syncFocus()
	}
}

// ---------------------------------------------------------------------------
// Picker sub-phase (inventory item / category / location / owning group /
// owning user / required certifications)
// ---------------------------------------------------------------------------

func (s *AssetFormScreen) openPicker(id int) {
	s.phase = assetPhasePick
	s.pickField = id
	s.pickSearch.SetValue("")
	// The filter is always live in a columnar picker, so it holds the caret for
	// as long as the picker is open.
	s.pickSearch.Focus()
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
	case afOwningUser:
		if s.owningUserID != nil {
			selKey = strconv.Itoa(*s.owningUserID)
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
	case afOwningUser:
		for _, u := range s.users {
			add(strconv.Itoa(u.ID), assetUserLabel(u))
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
	switch act, delta := jdePickKey(m); act {
	case jdePickCancel:
		// In the MULTI picker esc is "done", not "cancel": every toggle was
		// applied as it was made, so there is nothing to roll back. The bar says
		// so rather than pretending one of the two exits is special.
		s.closePicker()
	case jdePickCommit:
		if s.pickField == afRequiredCerts {
			// Enter toggles membership here — the always-live filter owns space,
			// and a multi picker's "select" IS the toggle.
			s.toggleCurrentCert()
			return s, nil
		}
		s.commitPick()
	case jdePickMove:
		s.movePick(delta)
	case jdePickPage:
		header, body := s.pickView()
		if next, ok := s.pageRow(body, s.pickCursor, len(s.pickOptions), delta, len(header),
			s.pickBar(header, body), s.pickBarItems(true)); ok {
			s.pickCursor = next
		}
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

// movePick walks the option cursor, clamping at both ends — a picker list is a
// set of choices, not a ring, so running off the bottom must not reappear at the
// "(none)" row that clears the field.
func (s *AssetFormScreen) movePick(delta int) {
	header, body := s.pickView()
	next, ok := s.pickRow(s.pickCursor, len(s.pickOptions), delta, len(header), s.pickBar(header, body))
	if !ok {
		return
	}
	s.pickCursor = next
}

func (s *AssetFormScreen) closePicker() {
	s.phase = assetPhaseForm
	s.pickSearch.SetValue("")
	s.pickSearch.Blur()
	s.syncFocus()
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
		case afOwningUser:
			s.owningUserID = pickInt(opt)
		}
	}
	s.closePicker()
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
	var owningUser *int
	switch ownership {
	case "group":
		if s.owningGroupID == nil {
			return w, errors.New("owning SIG is required when ownership is Group")
		}
		owningGroup = s.owningGroupID
	case "user":
		if s.owningUserID == nil {
			return w, errors.New("owning user is required when ownership is User")
		}
		owningUser = s.owningUserID
	}

	var donor *string
	if s.isDonation {
		donor = strPtrTrim(s.inputs[afDonorName].Value())
	}

	manualPath := strings.TrimSpace(s.inputs[afManualPDFPath].Value())
	if manualPath != "" {
		if !filepath.IsAbs(manualPath) {
			return w, errors.New("manual PDF path must be absolute")
		}
		info, err := os.Stat(manualPath)
		if err != nil {
			return w, fmt.Errorf("manual PDF path: %w", err)
		}
		if info.IsDir() {
			return w, errors.New("manual PDF path must be a file")
		}
	}

	w = omsapi.AssetWrite{
		Name:                   name,
		AssetTag:               strings.TrimSpace(s.inputs[afAssetTag].Value()),
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
		OwningUser:             owningUser,
		IsActive:               s.isActive,
		NeedsCompressedAir:     s.needsCompressedAir,
		NeedsVentilation:       s.needsVentilation,
		GeneratesHeatOrFlame:   s.generatesHeatOrFlame,
		NeedsChilling:          s.needsChilling,
		SpecialRequirements:    strPtrTrim(s.inputs[afSpecialRequirements].Value()),
		WorkSafetyNotes:        strPtrTrim(s.inputs[afWorkSafetyNotes].Value()),
		IsChargeable:           s.isChargeable,
		TrainingRequired:       s.trainingRequired,
		RequiredCertifications: s.certIDs,
		ReportOnly:             s.reportOnly,
		Notes:                  strPtrTrim(s.inputs[afNotes].Value()),
		ConditionNotes:         strPtrTrim(s.inputs[afConditionNotes].Value()),
		ManualPDFPath:          manualPath,
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
	body := s.formLines()
	return s.frame(body, s.cursor, s.statusRow(s.saving, "Saving…", s.errMsg), s.formBar(body))
}

// formFields describes the visible fields as columnar rows. Toggles and selects
// are both bounded sets, so both draw "< value >"; the five foreign keys and the
// certifications set show what is chosen today and are opened with Ctrl-E.
func (s *AssetFormScreen) formFields() []jdeField {
	out := make([]jdeField, len(s.fields))
	for i, id := range s.fields {
		f := jdeField{
			Label:   assetFieldLabel[id],
			Width:   assetFieldWidth(id),
			Hint:    assetFieldHint[id],
			Focused: i == s.cursor,
		}
		switch assetFieldKindOf(id) {
		case akToggle:
			f.Kind, f.Value = jdeChoice, jdeYesNo(s.toggleState(id))
		case akSelect:
			f.Kind, f.Value = jdeChoice, s.selectValue(id)
		case akPicker:
			value, dim := s.pickerValue(id)
			f.Kind, f.Value, f.Dim = jdeValue, value, dim
			if f.Focused {
				f.Hint = "Ctrl-E picks"
			}
		case akMultiPicker:
			value, dim := s.certsValue()
			f.Kind, f.Value, f.Dim = jdeValue, value, dim
			if f.Focused {
				f.Hint = "Ctrl-E chooses"
			}
		default:
			f.Kind, f.Input = jdeText, &s.inputs[id]
		}
		out[i] = f
	}
	return out
}

// formLines builds the body, breaking the fields into the bands a printed asset
// record would have and drawing the focused select's whole option set under it.
func (s *AssetFormScreen) formLines() *jdeLines {
	fields := s.formFields()
	labelWidth := jdeLabelWidth(fields)

	l := &jdeLines{}
	band := assetFieldBand(-1)
	for i, id := range s.fields {
		if b := assetFieldBandOf(id); b != band {
			if band != assetFieldBand(-1) {
				l.Add("")
			}
			l.Add(StyleJDEHeading.Render(assetBandLabel[b]))
			band = b
		}
		l.AddRow(i, renderJDEField(fields[i], labelWidth, s.bodyWidth()))
		if i == s.cursor {
			if strip := s.selectStrip(id, jdeStripWidth(s.bodyWidth(), labelWidth)); strip != "" {
				l.AddRow(i, jdeStripIndent(labelWidth)+StyleMuted.Render(strip))
			}
		}
	}
	// The inventory items this asset consumes, below the fields it owns: a
	// detail grid whose rows continue the cursor past the sheet, not fields of
	// the asset (asset_form_supplies.go). The label column above is computed
	// over the FIELDS alone, so a long part name can never move it.
	s.supplyBand(l)
	return l
}

// formBar names the keys that work on the form, with PgUp/PgDn on it exactly
// when the body moves under the bar that is about to be drawn.
//
// The paging claim is measured against formBarItems(true) — the bar WITH the
// pair on it — because naming them costs cells, cells fold the bar onto another
// row, and a folded bar leaves the body one row fewer. The tallest bar is the
// fixed point, so the answer cannot oscillate between frames.
func (s *AssetFormScreen) formBar(body *jdeLines) []actionBarItem {
	return s.formBarItems(s.bodyPagesForBar(body, s.rowCount(), 0, s.formBarItems(true)))
}

// formBarItems is formBar for a given paging state, so the bar that is
// MEASURED is the bar that is drawn.
//
// It names the keys that apply where the cursor is standing — and only those,
// so the bar never teaches a key that does nothing here.
func (s *AssetFormScreen) formBarItems(paging bool) []actionBarItem {
	if s.supplyWarn {
		// The confirm owns the bar while it is up: these two keys are the only
		// ones that do anything, and the bar is the only place that says so.
		return []actionBarItem{{"Ctrl-E", "Discard & open"}, {"Esc", "Stay here"}}
	}
	items := []actionBarItem{{"Enter", "Save"}, {"Esc", "Cancel"}, {"UP/DN", "Fields"}}
	if _, onSupply := s.onSupplyRow(); onSupply {
		items = append(items, actionBarItem{"Ctrl-E", "Parts"})
	}
	if id, ok := s.currentFieldID(); ok {
		switch assetFieldKindOf(id) {
		case akToggle, akSelect:
			items = append(items, actionBarItem{"←→", "Change"})
		case akPicker:
			items = append(items, actionBarItem{"Ctrl-E", "Pick"})
		case akMultiPicker:
			items = append(items, actionBarItem{"Ctrl-E", "Choose"})
		}
	}
	if paging {
		items = append(items, actionBarItem{"PgUp/PgDn", "Page"})
	}
	return items
}

// selectValue is what goes between a select row's angle brackets.
func (s *AssetFormScreen) selectValue(id int) string {
	switch id {
	case afStatus:
		if s.statusIdx >= 0 && s.statusIdx < len(assetStatusOptions) {
			return assetStatusOptions[s.statusIdx].label
		}
	case afOwnership:
		if s.ownershipIdx >= 0 && s.ownershipIdx < len(assetOwnershipOptions) {
			return assetOwnershipOptions[s.ownershipIdx].label
		}
	}
	return ""
}

// selectStrip is the whole option set drawn under the FOCUSED select row, so a
// fixed list is never cycled blind. Toggles get nothing — "< Yes >" already says
// what the other value is.
func (s *AssetFormScreen) selectStrip(id, width int) string {
	var opts []selectOption
	var idx int
	switch id {
	case afStatus:
		opts, idx = assetStatusOptions, s.statusIdx
	case afOwnership:
		opts, idx = assetOwnershipOptions, s.ownershipIdx
	default:
		return ""
	}
	labels := make([]string, len(opts))
	for i, o := range opts {
		labels[i] = o.label
	}
	return jdeOptionStrip(labels, idx, width)
}

// pickerValue is an FK row's text, and whether it is an empty state rather than
// a value. It returns PLAIN text with a flag instead of pre-styled muted text,
// because a focused row has to be able to reverse-video the whole field — an
// inner reset sequence would end the highlight partway through it.
func (s *AssetFormScreen) pickerValue(id int) (string, bool) {
	switch id {
	case afInventoryItem:
		if s.inventoryItemID == nil {
			return "(none)", true
		}
		for _, it := range s.items {
			if it.ID == *s.inventoryItemID {
				return it.Name, false
			}
		}
		if s.asset != nil && s.asset.InventoryItemName != "" {
			return s.asset.InventoryItemName, false
		}
		return "#" + *s.inventoryItemID, false
	case afCategory:
		if s.categoryID == nil {
			return "(none)", true
		}
		for _, c := range s.categories {
			if c.ID == *s.categoryID {
				return c.Name, false
			}
		}
		return fmt.Sprintf("#%d", *s.categoryID), false
	case afLocation:
		if s.locationID == nil {
			return "(none)", true
		}
		for _, l := range s.locations {
			if l.ID == *s.locationID {
				return l.Name, false
			}
		}
		return fmt.Sprintf("#%d", *s.locationID), false
	case afOwningGroup:
		if s.owningGroupID == nil {
			return "(none)", true
		}
		for _, g := range s.sigs {
			if g.ID == *s.owningGroupID {
				return g.Name, false
			}
		}
		return fmt.Sprintf("#%d", *s.owningGroupID), false
	case afOwningUser:
		if s.owningUserID == nil {
			return "(none)", true
		}
		for _, u := range s.users {
			if u.ID == *s.owningUserID {
				return assetUserLabel(u), false
			}
		}
		if s.asset != nil && s.asset.OwningUserName != "" {
			return s.asset.OwningUserName, false
		}
		return fmt.Sprintf("#%d", *s.owningUserID), false
	}
	return "", false
}

func assetUserLabel(u omsapi.User) string {
	if strings.TrimSpace(u.DisplayName) != "" {
		if u.Username != "" {
			return fmt.Sprintf("%s (%s)", u.DisplayName, u.Username)
		}
		return u.DisplayName
	}
	name := strings.TrimSpace(strings.Join([]string{u.FirstName, u.LastName}, " "))
	if name != "" {
		if u.Username != "" {
			return fmt.Sprintf("%s (%s)", name, u.Username)
		}
		return name
	}
	if u.Username != "" {
		return u.Username
	}
	return fmt.Sprintf("#%d", u.ID)
}

// certsValue is the multi-picker row's text, and whether it is an empty state.
func (s *AssetFormScreen) certsValue() (string, bool) {
	if len(s.certIDs) == 0 {
		return "(none)", true
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
	return strings.Join(names, ", "), false
}

// pickView builds the open picker's pinned header and its option list. The
// certifications picker marks membership with a checkbox in the label itself —
// the layer never learns what is being picked, so the caller draws the mark.
func (s *AssetFormScreen) pickView() (jdeHeader, *jdeLines) {
	multi := s.pickField == afRequiredCerts
	note := "Row 1 is none — it clears the field."
	if multi {
		note = "Enter toggles a certification on and off; esc is done."
	}
	return jdePickList{
		Title:  s.pickWhatLabel(),
		For:    strings.TrimSpace(s.inputs[afName].Value()),
		Note:   note,
		Filter: s.pickSearch,
		Count:  len(s.pickOptions),
		Label:  s.pickRowLabel,
		Dim:    func(i int) bool { return s.pickOptions[i].clear },
		Cursor: s.pickCursor,
		Empty:  "(no matches)",
	}.render(s.bodyWidth())
}

func (s *AssetFormScreen) pickRowLabel(i int) string {
	opt := s.pickOptions[i]
	if s.pickField != afRequiredCerts || opt.clear {
		return opt.label
	}
	mark := "[ ] "
	if id, err := strconv.Atoi(opt.key); err == nil && s.certSelected(id) {
		mark = "[x] "
	}
	return mark + opt.label
}

// pickBar is the picker's bar, with PgUp/PgDn on it exactly when the option
// list moves under the bar about to be drawn — measured against the bar WITH
// the pair on it, because the tallest bar is the fixed point.
func (s *AssetFormScreen) pickBar(header jdeHeader, body *jdeLines) []actionBarItem {
	return s.pickBarItems(s.bodyPagesForBar(body, len(s.pickOptions), len(header), s.pickBarItems(true)))
}

// pickBarItems is pickBar for a given paging state. The multi picker's esc is
// "done", not "cancel" — its toggles were applied as they were made.
func (s *AssetFormScreen) pickBarItems(paging bool) []actionBarItem {
	if s.pickField == afRequiredCerts {
		return jdePickBarWith("Toggle", "Done", paging)
	}
	return jdePickBar("Select", paging)
}

func (s *AssetFormScreen) viewPick() string {
	header, body := s.pickView()
	return s.frameWithHeader(header, body, s.pickCursor,
		s.statusRow(false, "", ""), s.pickBar(header, body))
}

// pickWhatLabel names what the open picker is picking. It is the picker's
// HEADING now rather than the tail of a "Pick …" sentence, so it reads as the
// field's own label does.
func (s *AssetFormScreen) pickWhatLabel() string {
	if label, ok := assetFieldLabel[s.pickField]; ok {
		return label
	}
	return ""
}
