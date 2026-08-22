// InventoryItemFormScreen — create/edit form for inventory items.
//
// This is the TUI counterpart to the web InventoryItemFormPage
// (frontend/src/pages/InventoryItemFormPage.tsx + inventoryItemSchema). It
// mirrors the FULL field set the web form exposes so an operator at the
// workstation can register or amend an item without switching to the browser
// ([[ship-complete-features]]). The structure follows po_create.go's
// field-by-field entry with sub-phase pickers for the foreign keys.
//
// It renders through the columnar "JD Edwards" layer (jde_form.go, sc-dnhx):
// labels right-aligned into one column, band headings over the sections a paper
// form would print, and a persistent action bar naming the keys that apply where
// the cursor is standing. Field kinds and how each is edited:
//
//	text / number — a bubbles textinput; type to edit, validated on submit
//	toggle        — a bool, drawn "< Yes >"; ←/→ (or space) flips it (case-based
//	                / ML reorder alerts / hazardous / serialized / active).
//	                Flipping one shows or hides its dependent fields
//	                (minimum_cases, the NFPA block, serial_tracking_mode).
//	select        — a fixed option list, drawn "< value >"; ←/→ cycles
//	                (shelf_position, serial_tracking_mode, count mode/level)
//	picker        — a foreign key chosen from a filtered list in a sub-phase;
//	                Ctrl-E opens it (category, location)
//	chain         — the packaging chain; Ctrl-E opens its own list sub-phase
//
// Navigation: tab / ↑↓ move between the visible fields, PgUp/PgDn page, enter
// saves, esc cancels. The form is far longer than the pane, so the body windows
// around the focused field while the bar stays pinned to the bottom.
//
// This was bead #1 of the ScanTTY parity program; the pattern here was reused by
// the follow-on create/edit forms (assets, category/location/supplier CRUD, …),
// and all of them now share jde_form.go rather than each rolling their own row.
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

// Field identifiers. The order here is the canonical top-to-bottom order the
// form renders (conditional fields are filtered out when their toggle is off —
// see rebuildFields).
const (
	fName = iota
	fDescription
	fSKU
	fImageURL
	fCurrentStock
	fMinimumStock
	fReorderQuantity
	fUseCaseBasedReorder
	fMinimumCases
	fReorderCases
	// fKitComponents is the kit's bill of materials (op-8n0) — present only when
	// the edited item IS a kit, which the sheet only learns from a separate
	// /kits/ fetch (inventory_item_form_kit.go).
	fKitComponents
	fBaseUnit
	fPackChain
	fCountMode
	fCountLevel
	fReorderAlertsEnabled
	fCategory
	fLocation
	fShelfPosition
	fIsHazardous
	fMSDSURL
	fNFPAHealth
	fNFPAFire
	fNFPAInstability
	fNFPASpecial
	fIsSerialized
	fSerialTrackingMode
	fNotes
	fIsActive
	fIsRetired
	fFieldMax
)

type itemFieldKind int

const (
	kindText itemFieldKind = iota
	kindNumber
	kindToggle
	kindSelect
	kindPicker
	// kindChain is the packaging-chain editor: a summary row that opens its own
	// list sub-phase, since a rung has two values and a chain has any number of
	// rungs (the sc-ue4 nested sub-list idiom).
	kindChain
	// kindKit is the kit's bill of materials, the same nested sub-list idiom:
	// a component has a quantity and a note, and a kit has any number of them.
	kindKit
)

type itemFormPhase int

const (
	itemFormPhaseForm itemFormPhase = iota
	itemFormPhaseCategoryPick
	itemFormPhaseLocationPick
	// itemFormPhaseChain lists the packaging rungs, largest first;
	// itemFormPhaseChainRow edits one rung's name + size. Both are purely
	// client-side — the chain is saved nested with the item, not per row.
	itemFormPhaseChain
	itemFormPhaseChainRow
	// itemFormPhaseKit lists a kit's components; itemFormPhaseKitRow edits one
	// component's quantity + notes; itemFormPhaseKitPick chooses the inventory
	// item a new component points at. All three are client-side — the bill of
	// materials is saved nested with the kit, not per row.
	itemFormPhaseKit
	itemFormPhaseKitRow
	itemFormPhaseKitPick
)

type selectOption struct{ value, label string }

// shelfPositionOptions mirrors the web form's Shelf Position select. The empty
// value ("Not specified") is omitted from the payload.
var shelfPositionOptions = []selectOption{
	{"", "Not specified"},
	{"top", "Top shelf"},
	{"bottom", "Bottom shelf"},
}

// serialModeOptions mirrors the web form's Tracking mode select. Only sent when
// the item is flagged serialized.
var serialModeOptions = []selectOption{
	{"consumable", "Consumable (used up)"},
	{"reusable", "Reusable (installed / removed)"},
}

var itemFieldLabel = map[int]string{
	fName:                 "Name",
	fDescription:          "Description",
	fSKU:                  "SKU",
	fImageURL:             "Image URL",
	fCurrentStock:         "Current stock",
	fMinimumStock:         "Minimum stock",
	fReorderQuantity:      "Reorder quantity",
	fUseCaseBasedReorder:  "Case-based reordering",
	fMinimumCases:         "Minimum cases",
	fReorderCases:         "Reorder cases",
	fKitComponents:        "Kit components",
	fBaseUnit:             "Base unit",
	fPackChain:            "Packaging chain",
	fCountMode:            "Count mode",
	fCountLevel:           "Counted in",
	fReorderAlertsEnabled: "ML reorder alerts",
	fCategory:             "Category",
	fLocation:             "Location",
	fShelfPosition:        "Shelf position",
	fIsHazardous:          "Hazardous material",
	fMSDSURL:              "MSDS/SDS URL",
	fNFPAHealth:           "NFPA health",
	fNFPAFire:             "NFPA fire",
	fNFPAInstability:      "NFPA instability",
	fNFPASpecial:          "NFPA special hazards",
	fIsSerialized:         "Track serial numbers",
	fSerialTrackingMode:   "Tracking mode",
	fNotes:                "Notes",
	fIsActive:             "Active",
	fIsRetired:            "Retired",
}

// itemFieldHint is the muted note drawn AFTER the input area: a scale, a format,
// what a blank means. It carries what used to sit inside the labels (a
// parenthetical there widens the shared label column and shoves every input area
// right) and inside the long placeholders (which filled the input and hid the
// underscores that say a field is empty). The unit notes are dynamic — see
// fieldHint.
var itemFieldHint = map[int]string{
	fName:            "required",
	fSKU:             "auto-generated if blank",
	fImageURL:        "https://…",
	fMSDSURL:         "https://… safety data sheet",
	fNFPAHealth:      "0-4",
	fNFPAFire:        "0-4",
	fNFPAInstability: "0-4",
	fNFPASpecial:     "W · OX · COR · ACID",
}

// itemFieldWidth sizes the input areas that are not the default: the four one-
// digit NFPA ratings and the counts are narrow, the descriptive fields long.
func itemFieldWidth(id int) int {
	switch id {
	case fNFPAHealth, fNFPAFire, fNFPAInstability:
		return 3
	case fNFPASpecial:
		return 12
	case fCurrentStock, fMinimumStock, fReorderQuantity, fMinimumCases, fReorderCases:
		return 8
	case fDescription, fNotes, fImageURL, fMSDSURL:
		return 40
	}
	return 0
}

// itemFieldBand groups the form into the sections a printed sheet would have.
// Bands must be CONTIGUOUS in the render order rebuildFields produces, because
// the heading is emitted where the band changes.
type itemFieldBand int

const (
	itemBandDetails itemFieldBand = iota
	itemBandStock
	itemBandKit
	itemBandPackaging
	itemBandPlacement
	itemBandHazard
	itemBandSerial
	itemBandStatus
)

var itemBandLabel = map[itemFieldBand]string{
	itemBandDetails:   "Item",
	itemBandStock:     "Stock & reordering",
	itemBandKit:       "Kit",
	itemBandPackaging: "Units & packaging",
	itemBandPlacement: "Alerts & placement",
	itemBandHazard:    "Hazardous material",
	itemBandSerial:    "Serial tracking",
	itemBandStatus:    "Notes & status",
}

func itemFieldBandOf(id int) itemFieldBand {
	switch id {
	case fCurrentStock, fMinimumStock, fReorderQuantity, fUseCaseBasedReorder, fMinimumCases, fReorderCases:
		return itemBandStock
	case fKitComponents:
		return itemBandKit
	case fBaseUnit, fPackChain, fCountMode, fCountLevel:
		return itemBandPackaging
	case fReorderAlertsEnabled, fCategory, fLocation, fShelfPosition:
		return itemBandPlacement
	case fIsHazardous, fMSDSURL, fNFPAHealth, fNFPAFire, fNFPAInstability, fNFPASpecial:
		return itemBandHazard
	case fIsSerialized, fSerialTrackingMode:
		return itemBandSerial
	case fNotes, fIsActive, fIsRetired:
		return itemBandStatus
	}
	return itemBandDetails
}

func fieldKind(id int) itemFieldKind {
	switch id {
	case fName, fDescription, fSKU, fImageURL, fMSDSURL, fNFPASpecial, fNotes, fBaseUnit:
		return kindText
	case fCurrentStock, fMinimumStock, fReorderQuantity, fMinimumCases, fReorderCases,
		fNFPAHealth, fNFPAFire, fNFPAInstability:
		return kindNumber
	case fUseCaseBasedReorder, fReorderAlertsEnabled, fIsHazardous, fIsSerialized, fIsActive, fIsRetired:
		return kindToggle
	case fShelfPosition, fSerialTrackingMode, fCountMode, fCountLevel:
		return kindSelect
	case fCategory, fLocation:
		return kindPicker
	case fPackChain:
		return kindChain
	case fKitComponents:
		return kindKit
	}
	return kindText
}

func isTextKind(id int) bool {
	k := fieldKind(id)
	return k == kindText || k == kindNumber
}

// itemPickOption is one row in the category/location sub-picker. clear marks the
// synthetic "(none)" row that unsets the field.
type itemPickOption struct {
	id    int
	label string
	clear bool
}

type InventoryItemFormScreen struct {
	deps   Deps
	edit   bool
	itemID string

	loading bool
	loadErr string
	saving  bool
	errMsg  string

	// Loaded reference data for the pickers and edit-mode hydration.
	categories  []omsapi.Category
	locations   []omsapi.Location
	item        *omsapi.Item
	refArrived  bool
	itemArrived bool

	jdeScreen

	// Text/number field storage, indexed by field id. Non-text slots are left
	// as zero-value models and never rendered/updated.
	inputs []textinput.Model

	// Toggle + select state.
	useCaseBased  bool
	reorderAlerts bool
	isHazardous   bool
	isSerialized  bool
	isActive      bool
	isRetired     bool
	shelfPos      int
	serialMode    int

	// Picker selections (nil == unset).
	categoryID *int
	locationID *int

	// Kit support (op-8n0), all of it in inventory_item_form_kit.go. `kit` is
	// non-nil ONLY when the /kits/ fetch succeeded, which is the only thing that
	// makes this a kit — the item serializer carries no `is_kit` — and it is what
	// puts the fKitComponents row on the sheet at all. kitRows is the editable
	// bill of materials; savedKitSig is what the server already has, so a save
	// that never opened the editor omits the key entirely.
	kit         *omsapi.Kit
	kitArrived  bool
	kitRows     []kitComponentRow
	kitNextKey  int
	savedKitSig string

	// kitErr is "the /kits/ question went UNANSWERED" — anything that is not the
	// 404 meaning "ordinary item". It is held, not dropped, for the same reason
	// the item detail holds it (inventory_detail_kit.go's renderKitErrLine): a
	// sheet built on a failed question looks exactly like an ordinary item's, and
	// its save then goes down the wrong route. Two screens must not answer the
	// same question differently.
	kitErr string

	kitCursor     int
	kitRowEditing int
	kitRowQty     textinput.Model
	kitRowNotes   textinput.Model
	kitRowFocus   int
	kitRowErr     string

	// The component picker's catalogue, loaded lazily the first time it opens.
	kitItems        []omsapi.Item
	kitItemsLoading bool
	kitItemsErr     string
	kitPickOptions  []kitPickOption
	kitPickCursor   int
	kitPickErr      string

	// Visible-field navigation. The cursor runs past the fields into the
	// read-only suppliers band (inventory_item_form_suppliers.go), which is what
	// rowCount() counts and every cursor bound below measures against.
	fields []int
	cursor int

	// supplierWarn is the band's Ctrl-E door asking before it leaves a sheet with
	// unsaved edits; baseline is the sheet as it loaded, which is how dirty()
	// knows there are any.
	supplierWarn bool
	baseline     string

	// Category/location sub-picker state.
	phase       itemFormPhase
	pickCursor  int
	pickSearch  textinput.Model
	pickOptions []itemPickOption

	// Packaging matrix (OMS #979/#981, web #983). packRows is the editable pack
	// chain, largest rung first; packNextKey mints the client-only row keys that
	// let countLevelKey survive a reorder, insert or delete (a rung's pk is
	// positional server-side, so it cannot be that identity).
	//
	// countModeIx indexes countModeOptions; countLevelKey names a ROW, and its pk
	// is resolved out of the SAVED chain at submit time.
	packRows      []packagingRow
	packNextKey   int
	countModeIx   int
	countLevelKey int

	// What the server already has, so a save that touches none of it sends no
	// packaging request at all — which is the common case, and what keeps an
	// each-mode item's write byte-identical to before this section existed.
	savedChainSig  string
	savedBaseUnit  string
	savedCountMode string
	// savedCountLevel is the stored rung pk; savedCountLevelSort is that rung's
	// POSITION in the stored chain, which is what decides whether a chain write
	// can keep it (see packagingPlan). -1 when there is no stored level.
	savedCountLevel     *int
	savedCountLevelSort int
	packErr             string
	chainCursor         int
	chainRowEditing     int // index being edited, or -1 while adding a new row
	chainRowName        textinput.Model
	chainRowUnits       textinput.Model
	chainRowFocus       int // chainRowField* — which row of the rung editor
	chainRowErr         string
}

type itemFormRefLoadedMsg struct {
	categories []omsapi.Category
	locations  []omsapi.Location
	err        error
}

type itemFormItemLoadedMsg struct {
	item *omsapi.Item
	err  error
}

// itemFormKitLoadedMsg answers "is the edited item a kit, and what does it
// contain?". A NOT-FOUND is a successful answer of "no" and arrives with both
// fields nil; anything else left the question unanswered, and the sheet must NOT
// then offer a components row it cannot save (see maybeFinalizeLoad).
type itemFormKitLoadedMsg struct {
	kit *omsapi.Kit
	err error
}

// itemFormSavedMsg is the result of a save. packErr is the packaging half
// failing on its own — the item itself DID save by then, so the operator has to
// be told which half went wrong rather than seeing one blanket error. detached
// records that the counting mode was cleared before the item write (see
// packagingPlan), so a failure can say so and a retry can plan correctly.
type itemFormSavedMsg struct {
	item     *omsapi.Item
	err      error
	packErr  error
	detached bool
}

// NewInventoryItemFormScreen builds the create/edit form. An empty itemID opens
// create mode with sensible defaults; a non-empty id opens edit mode and
// hydrates every field from the fetched item.
func NewInventoryItemFormScreen(deps Deps, itemID string) *InventoryItemFormScreen {
	edit := strings.TrimSpace(itemID) != ""
	s := &InventoryItemFormScreen{
		deps:      deps,
		edit:      edit,
		itemID:    strings.TrimSpace(itemID),
		loading:   true,
		isActive:  true,  // web default
		isRetired: false, // web default (a new item starts un-retired)
	}

	s.inputs = make([]textinput.Model, fFieldMax)
	for id := 0; id < fFieldMax; id++ {
		if !isTextKind(id) {
			continue
		}
		ti := textinput.New()
		ti.Prompt = ""
		ti.CharLimit = itemCharLimitFor(id)
		ti.Placeholder = itemPlaceholderFor(id)
		s.inputs[id] = ti
	}

	// Create-mode numeric defaults mirror inventoryItemSchema.
	if !edit {
		s.inputs[fCurrentStock].SetValue("0")
		s.inputs[fMinimumStock].SetValue("0")
		s.inputs[fReorderQuantity].SetValue("1")
	}

	s.pickSearch = textinput.New()
	s.pickSearch.Prompt = ""
	s.pickSearch.Placeholder = "filter"
	s.pickSearch.CharLimit = 60

	// Packaging: a new item starts each-mode with no chain, which is exactly the
	// state every existing item is in. savedChainSig therefore matches, so a
	// create that never opens the chain editor sends no packaging_levels key.
	s.savedChainSig = chainSignature(nil)
	s.savedCountMode = omsapi.CountModeEach
	s.savedCountLevelSort = -1
	s.chainRowEditing = -1
	s.chainRowName = textinput.New()
	s.chainRowName.Prompt = ""
	s.chainRowName.Placeholder = "case"
	s.chainRowName.CharLimit = 50
	s.chainRowUnits = textinput.New()
	s.chainRowUnits.Prompt = ""
	s.chainRowUnits.Placeholder = "1000"
	s.chainRowUnits.CharLimit = 9

	// Kit components: an item that is not a kit keeps an empty list, whose
	// signature matches savedKitSig, so a save sends no `components` key.
	s.savedKitSig = kitSignature(nil)
	s.kitRowEditing = -1
	s.kitRowQty = textinput.New()
	s.kitRowQty.Prompt = ""
	s.kitRowQty.Placeholder = "1"
	s.kitRowQty.CharLimit = 6
	s.kitRowNotes = textinput.New()
	s.kitRowNotes.Prompt = ""
	s.kitRowNotes.Placeholder = "optional"
	s.kitRowNotes.CharLimit = 200

	s.rebuildFields()
	s.syncFocus()
	return s
}

func itemCharLimitFor(id int) int {
	switch id {
	case fName:
		return 200
	case fSKU:
		return 100
	case fDescription:
		return 500
	case fImageURL, fMSDSURL:
		return 500
	case fNFPASpecial:
		return 20
	case fNotes:
		return 1000
	case fBaseUnit:
		return 50
	case fNFPAHealth, fNFPAFire, fNFPAInstability:
		return 1
	default:
		return 12
	}
}

// itemPlaceholderFor keeps only the DEFAULTS — the value a blank field saves as,
// which is worth seeing sitting in the input. Everything the others said (a
// format, a scale, "optional") is now a hint beside the field instead, where it
// cannot fill the input area and hide the underscores. See itemFieldHint.
func itemPlaceholderFor(id int) string {
	switch id {
	case fCurrentStock, fMinimumStock:
		return "0"
	case fReorderQuantity, fMinimumCases, fReorderCases:
		return "1"
	case fBaseUnit:
		// The smallest thing you count — stock is always stored in these.
		return "unit"
	default:
		return ""
	}
}

func (s *InventoryItemFormScreen) Title() string {
	if s.edit {
		if s.item != nil && s.item.Name != "" {
			return fmt.Sprintf("Edit item: %s", s.item.Name)
		}
		return "Edit item"
	}
	return "New inventory item"
}

func (s *InventoryItemFormScreen) WantsRawInput() bool { return true }

func (s *InventoryItemFormScreen) Init() tea.Cmd {
	cmds := []tea.Cmd{s.loadRefData(), textinput.Blink}
	if s.edit {
		cmds = append(cmds, s.loadItem(), s.loadKit())
	}
	return tea.Batch(cmds...)
}

func (s *InventoryItemFormScreen) ctx() context.Context {
	if s.deps.Ctx != nil {
		return s.deps.Ctx
	}
	return context.Background()
}

// loadRefData fetches the first page of categories and locations for the
// pickers. Like the New-PO supplier picker this loads a single page — installs
// rarely have more than a page of either, and the web item form likewise reads
// only results[0..n] of the first page.
func (s *InventoryItemFormScreen) loadRefData() tea.Cmd {
	deps := s.deps
	ctx := s.ctx()
	return func() tea.Msg {
		cats, err := deps.OMS.ListCategories(ctx, nil)
		if err != nil {
			return itemFormRefLoadedMsg{err: err}
		}
		locs, err := deps.OMS.ListLocations(ctx, nil)
		if err != nil {
			return itemFormRefLoadedMsg{err: err}
		}
		return itemFormRefLoadedMsg{categories: cats.Results, locations: locs.Results}
	}
}

func (s *InventoryItemFormScreen) loadItem() tea.Cmd {
	deps := s.deps
	ctx := s.ctx()
	id := s.itemID
	return func() tea.Msg {
		item, err := deps.OMS.GetItem(ctx, id)
		return itemFormItemLoadedMsg{item: item, err: err}
	}
}

// loadKit asks whether the edited id is a kit, and if so what it contains
// (op-8n0). It has to be a second request: the kit fields live only on the
// /kits/ route, and that route's queryset is filtered to kits — so its 404 is
// the answer "ordinary item" rather than a failure (omsapi.IsNotKit).
func (s *InventoryItemFormScreen) loadKit() tea.Cmd {
	deps := s.deps
	ctx := s.ctx()
	id := s.itemID
	return func() tea.Msg {
		kit, err := deps.OMS.GetKit(ctx, id)
		if omsapi.IsNotKit(err) {
			return itemFormKitLoadedMsg{}
		}
		return itemFormKitLoadedMsg{kit: kit, err: err}
	}
}

func (s *InventoryItemFormScreen) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		s.setSize(m)
		return s, nil

	case itemFormRefLoadedMsg:
		s.refArrived = true
		if m.err != nil {
			s.loadErr = m.err.Error()
		} else {
			s.categories = m.categories
			s.locations = m.locations
		}
		return s, s.maybeFinalizeLoad()

	case itemFormKitLoadedMsg:
		s.kitArrived = true
		// A failure here is deliberately NOT a loadErr: the sheet still LOADS, so
		// the operator can read every field on it. What it must not do is (a)
		// offer a kit affordance — which it cannot, since only a non-nil kit does
		// that — or (b) SAVE, which is where the harm is. An unanswered question
		// leaves the sheet unable to tell a kit from an ordinary item, and the two
		// save down different routes: a kit PATCHed to /items/ is a flat 404 for a
		// record the operator is looking at, with the reason never named. So the
		// error is kept, said on screen, and blocks the save until it is known.
		s.kitErr = ""
		if m.err != nil {
			s.kitErr = m.err.Error()
		} else {
			s.kit = m.kit
		}
		return s, s.maybeFinalizeLoad()

	case itemFormKitItemsMsg:
		s.kitItemsLoading = false
		if m.err != nil {
			s.kitItemsErr = m.err.Error()
		} else {
			s.kitItemsErr = ""
			s.kitItems = m.items
		}
		s.applyKitPickFilter()
		return s, nil

	case itemFormItemLoadedMsg:
		s.itemArrived = true
		if m.err != nil {
			s.loadErr = m.err.Error()
		} else {
			s.item = m.item
		}
		return s, s.maybeFinalizeLoad()

	case itemFormSavedMsg:
		s.saving = false
		if m.detached {
			// The counting mode really was cleared server-side before the item
			// write, so the snapshot has to say so — otherwise a retry would plan
			// another detach and its own error message would be wrong.
			s.savedCountMode = omsapi.CountModeEach
			s.savedCountLevel = nil
			s.savedCountLevelSort = -1
		}
		if m.err != nil {
			s.errMsg = m.err.Error()
			if m.detached {
				s.errMsg += " (the item's counting mode was cleared first and is still 'each')"
			}
			return s, Status("save failed: "+s.errMsg, StatusError)
		}
		if m.packErr != nil {
			// The item saved; only the counting-mode follow-up failed. Adopt the
			// saved id so a retry PATCHes THIS item rather than creating a second
			// one, and re-snapshot from the response (the chain write landed with
			// the item, so only the mode/level pair is still outstanding).
			if m.item != nil {
				s.edit = true
				s.itemID = m.item.ID
				s.item = m.item
				s.adoptSavedPackaging(m.item)
			}
			s.errMsg = "item saved, but the packaging setup failed: " + m.packErr.Error()
			return s, Status(s.errMsg, StatusError)
		}
		id := s.itemID
		name := ""
		if m.item != nil {
			id = m.item.ID
			name = m.item.Name
		}
		verb := "created"
		if s.edit {
			verb = "updated"
		}
		return s, tea.Batch(
			Status(fmt.Sprintf("item %s: %s", verb, name), StatusOK),
			SwitchTo(WSInventory, NewInventoryDetailScreen(s.deps, id)),
		)

	case tea.KeyMsg:
		if s.loading {
			if m.String() == "esc" {
				return s, s.cancelCmd()
			}
			return s, nil
		}
		switch s.phase {
		case itemFormPhaseCategoryPick, itemFormPhaseLocationPick:
			return s.updatePickPhase(m)
		case itemFormPhaseChain:
			return s.updateChainPhase(m)
		case itemFormPhaseChainRow:
			return s.updateChainRowPhase(m)
		case itemFormPhaseKit:
			return s.updateKitPhase(m)
		case itemFormPhaseKitRow:
			return s.updateKitRowPhase(m)
		case itemFormPhaseKitPick:
			return s.updateKitPickPhase(m)
		default:
			return s.updateFormPhase(m)
		}
	}

	// Non-key messages (cursor blink) go to whichever input owns the caret.
	if s.phase == itemFormPhaseCategoryPick || s.phase == itemFormPhaseLocationPick ||
		s.phase == itemFormPhaseKitPick {
		var cmd tea.Cmd
		s.pickSearch, cmd = s.pickSearch.Update(msg)
		return s, cmd
	}
	if s.phase == itemFormPhaseKitRow {
		var cmd tea.Cmd
		switch s.kitRowFocus {
		case kitRowFieldQty:
			s.kitRowQty, cmd = s.kitRowQty.Update(msg)
		case kitRowFieldNotes:
			s.kitRowNotes, cmd = s.kitRowNotes.Update(msg)
		}
		return s, cmd
	}
	if s.phase == itemFormPhaseChainRow {
		var cmd tea.Cmd
		switch s.chainRowFocus {
		case chainRowFieldUnits:
			s.chainRowUnits, cmd = s.chainRowUnits.Update(msg)
		case chainRowFieldName:
			s.chainRowName, cmd = s.chainRowName.Update(msg)
		}
		return s, cmd
	}
	if id, ok := s.currentFieldID(); ok && isTextKind(id) {
		var cmd tea.Cmd
		s.inputs[id], cmd = s.inputs[id].Update(msg)
		return s, cmd
	}
	return s, nil
}

// maybeFinalizeLoad flips out of the loading state once every in-flight fetch
// has reported. In edit mode that's both the reference data and the item; in
// create mode just the reference data.
func (s *InventoryItemFormScreen) maybeFinalizeLoad() tea.Cmd {
	if !s.refArrived {
		return nil
	}
	if s.edit && (!s.itemArrived || !s.kitArrived) {
		return nil
	}
	s.loading = false
	if s.loadErr == "" && s.edit && s.item != nil {
		s.hydrate()
		s.hydrateKit()
	}
	s.rebuildFields()
	s.syncFocus()
	s.snapshotBaseline()
	return nil
}

// hydrate fills every field from the fetched item (edit mode).
func (s *InventoryItemFormScreen) hydrate() {
	it := s.item
	set := func(id int, v string) { s.inputs[id].SetValue(v) }

	set(fName, it.Name)
	set(fDescription, it.Description)
	set(fSKU, it.SKU)
	if strings.HasPrefix(it.Image, "http") {
		set(fImageURL, it.Image)
	}
	set(fCurrentStock, strconv.Itoa(it.Stock))
	set(fMinimumStock, strconv.Itoa(it.MinimumStock))
	set(fReorderQuantity, strconv.Itoa(it.ReorderQuantity))

	s.useCaseBased = it.UseCaseBasedReorder
	if it.MinimumCases != nil {
		set(fMinimumCases, strconv.Itoa(int(*it.MinimumCases)))
	}
	if it.ReorderCases != nil {
		set(fReorderCases, strconv.Itoa(int(*it.ReorderCases)))
	}
	// ML reorder-alert opt-in (op-1); defaults false on the model.
	s.reorderAlerts = it.ReorderAlertsEnabled

	s.categoryID = it.Category
	// The serializer returns location as a name string, so resolve it back to
	// an id against the loaded locations. If it isn't on the loaded page the
	// picker stays unset and a PATCH simply omits location (leaving it intact).
	if it.Location != "" {
		for _, loc := range s.locations {
			if loc.Name == it.Location {
				id := loc.ID
				s.locationID = &id
				break
			}
		}
	}

	s.isHazardous = it.IsHazardous
	set(fMSDSURL, it.MSDSURL)
	if it.NFPAHealthHazard != nil {
		set(fNFPAHealth, strconv.Itoa(*it.NFPAHealthHazard))
	}
	if it.NFPAFireHazard != nil {
		set(fNFPAFire, strconv.Itoa(*it.NFPAFireHazard))
	}
	if it.NFPAInstabilityHazard != nil {
		set(fNFPAInstability, strconv.Itoa(*it.NFPAInstabilityHazard))
	}
	set(fNFPASpecial, it.NFPASpecialHazards)

	s.isSerialized = it.IsSerialized
	s.serialMode = serialModeIndex(it.SerialTrackingMode)

	// Packaging matrix: hydrate the chain + counting granularity, and snapshot
	// what the server already has so a save that touches none of it sends no
	// packaging request. A backend predating the matrix returns no fields at all,
	// which hydrates as "each mode, no chain" — the same as an opted-out item.
	set(fBaseUnit, it.BaseUnit)
	s.packRows, s.packNextKey = toPackagingRows(it.PackagingLevels, s.packNextKey)
	s.countModeIx = countModeIndex(it.CountMode)
	s.countLevelKey = 0
	s.savedCountLevelSort = -1
	if it.CountLevel != nil {
		for i, row := range s.packRows {
			if row.id == *it.CountLevel {
				s.countLevelKey = row.key
				s.savedCountLevelSort = i
				break
			}
		}
	}
	s.savedChainSig = chainSignature(s.packRows)
	s.savedBaseUnit = strings.TrimSpace(it.BaseUnit)
	s.savedCountMode = countModeOptions[s.countModeIx].value
	s.savedCountLevel = it.CountLevel

	set(fNotes, it.Notes)
	// is_active defaults true on the model; honour the fetched value.
	s.isActive = it.IsActive
	// is_retired defaults false; honour the fetched phase-out state.
	s.isRetired = it.IsRetired
}

func serialModeIndex(mode string) int {
	for i, o := range serialModeOptions {
		if o.value == mode {
			return i
		}
	}
	return 0
}

// rebuildFields recomputes the visible-field list from the current toggle
// state, preserving the cursor on the same field id where possible.
func (s *InventoryItemFormScreen) rebuildFields() {
	var focused int = -1
	if id, ok := s.currentFieldID(); ok {
		focused = id
	}

	f := []int{fName, fDescription, fSKU, fImageURL, fCurrentStock, fMinimumStock, fReorderQuantity, fUseCaseBasedReorder}
	if s.useCaseBased {
		f = append(f, fMinimumCases, fReorderCases)
	}
	// Units & packaging (web #983's own section). Base unit, the chain and the
	// count mode are always offered — that IS the opt-in — while the counting
	// level only exists for the two pack-counting modes, exactly as the web
	// renders its select conditionally.
	// The bill of materials sits with the stock it explains, and exists only for
	// a kit: an ordinary item's sheet is exactly what it was before kits.
	if s.isKit() {
		f = append(f, fKitComponents)
	}
	f = append(f, fBaseUnit, fPackChain, fCountMode)
	if countModeOptions[s.countModeIx].value != omsapi.CountModeEach {
		f = append(f, fCountLevel)
	}
	f = append(f, fReorderAlertsEnabled, fCategory, fLocation, fShelfPosition, fIsHazardous)
	if s.isHazardous {
		f = append(f, fMSDSURL, fNFPAHealth, fNFPAFire, fNFPAInstability, fNFPASpecial)
	}
	f = append(f, fIsSerialized)
	// The mode row hangs off a flag a kit's save always clears, so a kit never
	// grows one — offering a select whose value the payload then drops is the
	// same broken promise the frozen toggle above it exists to avoid.
	if s.isSerialized && !s.isKit() {
		f = append(f, fSerialTrackingMode)
	}
	f = append(f, fNotes, fIsActive, fIsRetired)
	s.fields = f

	if focused >= 0 {
		s.setCursorToField(focused)
	}
	// The clamp measures the whole SHEET, not just the fields: a conditional
	// field appearing or disappearing must not knock the cursor off the
	// suppliers band below them.
	if s.cursor >= s.rowCount() {
		s.cursor = s.rowCount() - 1
	}
	if s.cursor < 0 {
		s.cursor = 0
	}
}

func (s *InventoryItemFormScreen) currentFieldID() (int, bool) {
	if s.cursor < 0 || s.cursor >= len(s.fields) {
		return 0, false
	}
	return s.fields[s.cursor], true
}

func (s *InventoryItemFormScreen) setCursorToField(id int) {
	for i, fid := range s.fields {
		if fid == id {
			s.cursor = i
			return
		}
	}
}

func (s *InventoryItemFormScreen) syncFocus() {
	for id := 0; id < len(s.inputs); id++ {
		if isTextKind(id) {
			s.inputs[id].Blur()
		}
	}
	if id, ok := s.currentFieldID(); ok && isTextKind(id) {
		s.inputs[id].Focus()
	}
}

// ---------------------------------------------------------------------------
// Form phase
// ---------------------------------------------------------------------------

func (s *InventoryItemFormScreen) updateFormPhase(m tea.KeyMsg) (Screen, tea.Cmd) {
	// The suppliers band's confirm is modal while it is up — including over the
	// system keys, since the two it overrides (enter saves, esc discards) are
	// exactly the two outcomes it exists to ask about.
	if s.supplierWarn {
		return s.updateSupplierWarn(m)
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
		// EDIT opens whatever the highlighted row IS: a foreign-key picker, or
		// the packaging chain's own list. A row you simply type into opens
		// nothing, which is why the bar drops the key there.
		return s, s.openFocusedRow()
	}

	id, ok := s.currentFieldID()
	if !ok {
		return s, nil
	}
	switch fieldKind(id) {
	case kindToggle:
		// A two-value choice row: it flips whichever way it is cycled — unless
		// the row is frozen, which for a kit's serialized toggle it is. The
		// guard sits HERE as well as in the default arm because a toggle never
		// reaches that arm: flipping it would change a value the save asserts
		// back anyway, and would make the sheet dirty() for a change the server
		// refuses outright.
		if s.fieldReadOnly(id) {
			return s, nil
		}
		switch m.String() {
		case " ", "right", "left":
			s.flipToggle(id)
		}
		return s, nil
	case kindSelect:
		switch m.String() {
		case " ", "right":
			s.cycleSelect(id, +1)
		case "left":
			s.cycleSelect(id, -1)
		}
		return s, nil
	case kindPicker, kindChain:
		// Nothing to type on these, and no accelerators left to press.
		return s, nil
	default:
		if s.fieldReadOnly(id) {
			// A row that renders read-only must BE read-only: letting the
			// keystroke through would edit a value the save overwrites anyway,
			// and would make the sheet dirty() for a change that can never land.
			return s, nil
		}
		var cmd tea.Cmd
		s.inputs[id], cmd = s.inputs[id].Update(m)
		return s, cmd
	}
}

// openFocusedRow is what Ctrl-E does: it opens whatever the highlighted row IS.
func (s *InventoryItemFormScreen) openFocusedRow() tea.Cmd {
	id, ok := s.currentFieldID()
	if !ok {
		// Not a field — the suppliers band, whose door is the screen that
		// manages the links this one only lists.
		if _, onBand := s.onSupplierRow(); onBand {
			return s.openSupplierRow()
		}
		return nil
	}
	switch fieldKind(id) {
	case kindPicker:
		s.openPicker(id)
		return textinput.Blink
	case kindChain:
		s.openChain()
		return nil
	case kindKit:
		s.openKitList()
		return nil
	}
	return nil
}

func (s *InventoryItemFormScreen) moveCursor(delta int) {
	n := s.rowCount()
	if n == 0 {
		return
	}
	s.cursor = (s.cursor + delta + n) % n
	s.syncFocus()
}

func (s *InventoryItemFormScreen) pageCursor(dir int) {
	if s.rowCount() == 0 {
		return
	}
	s.cursor = jdePageCursor(s.cursor, s.rowCount(), s.windowRows(s.formLines(), s.cursor, 0), dir)
	s.syncFocus()
}

func (s *InventoryItemFormScreen) flipToggle(id int) {
	switch id {
	case fUseCaseBasedReorder:
		s.useCaseBased = !s.useCaseBased
	case fReorderAlertsEnabled:
		s.reorderAlerts = !s.reorderAlerts
	case fIsHazardous:
		s.isHazardous = !s.isHazardous
	case fIsSerialized:
		s.isSerialized = !s.isSerialized
	case fIsActive:
		s.isActive = !s.isActive
	case fIsRetired:
		s.isRetired = !s.isRetired
	}
	s.rebuildFields()
	s.syncFocus()
}

func (s *InventoryItemFormScreen) cycleSelect(id, delta int) {
	switch id {
	case fShelfPosition:
		n := len(shelfPositionOptions)
		s.shelfPos = (s.shelfPos + delta + n) % n
	case fSerialTrackingMode:
		n := len(serialModeOptions)
		s.serialMode = (s.serialMode + delta + n) % n
	case fCountMode:
		n := len(countModeOptions)
		s.countModeIx = (s.countModeIx + delta + n) % n
		if countModeOptions[s.countModeIx].value == omsapi.CountModeEach {
			// The backend refuses "each" while a level is set, so dropping to it
			// clears the pick rather than leaving a value that can only 400.
			s.countLevelKey = 0
		}
		// The counting-level row appears and disappears with the mode.
		s.rebuildFields()
		s.syncFocus()
	case fCountLevel:
		s.cycleCountLevel(delta)
	}
}

// cycleCountLevel walks the pick across the NAMED chain rows (an unnamed row has
// nothing to show and cannot be a legal count level anyway), starting from
// whatever is picked now. A chain with no named rows leaves the pick empty, and
// the field renders the same "add a packaging level first" hint the web's
// placeholder does.
func (s *InventoryItemFormScreen) cycleCountLevel(delta int) {
	named := make([]int, 0, len(s.packRows))
	for _, row := range s.packRows {
		if strings.TrimSpace(row.name) != "" {
			named = append(named, row.key)
		}
	}
	if len(named) == 0 {
		s.countLevelKey = 0
		return
	}
	at := -1
	for i, key := range named {
		if key == s.countLevelKey {
			at = i
			break
		}
	}
	if at < 0 {
		// Nothing picked yet: step onto the first (or last) named row rather than
		// skipping one, so a single keypress always lands somewhere.
		if delta >= 0 {
			s.countLevelKey = named[0]
		} else {
			s.countLevelKey = named[len(named)-1]
		}
		return
	}
	s.countLevelKey = named[(at+delta+len(named))%len(named)]
}

// countMode is the wire value of the picked counting mode.
func (s *InventoryItemFormScreen) countMode() string {
	return countModeOptions[s.countModeIx].value
}

// baseUnitValue is the base unit as typed, falling back to the backend default
// so labels never read "1 " with a hole in them.
func (s *InventoryItemFormScreen) baseUnitValue() string {
	if u := strings.TrimSpace(s.inputs[fBaseUnit].Value()); u != "" {
		return u
	}
	return defaultBaseUnit
}

// thresholdUnit is the unit minimum_stock / reorder_quantity are read in, or ""
// when the item is counted in base units. Phase 2a reinterpreted that pair as
// COUNT-level quantities for the pack-counting modes, so the labels have to say
// which unit they mean (the same shift the web's thresholdUnit expresses).
func (s *InventoryItemFormScreen) thresholdUnit() string {
	if s.countMode() == omsapi.CountModeEach {
		return ""
	}
	idx := packagingRowIndex(s.packRows, s.countLevelKey)
	if idx < 0 {
		return ""
	}
	return strings.TrimSpace(s.packRows[idx].name)
}

// fieldUnit is the unit a quantity field is read in, or "" when there is none to
// name. Phase 2a reinterpreted minimum_stock / reorder_quantity as COUNT-level
// quantities for the pack-counting modes, so the form has to say which unit it
// means — but it says it in the HINT beside the input rather than in the label,
// because the label column is shared by every field on the sheet and a unit that
// appeared and vanished with the counting mode would shove every input area
// sideways under the operator's eye.
func (s *InventoryItemFormScreen) fieldUnit(id int) string {
	unit := s.thresholdUnit()
	if unit == "" {
		return ""
	}
	switch id {
	case fCurrentStock:
		// Stock stays canonical in BASE units even for a pack-counted item; the
		// at-level entry lives on the item detail's count flow.
		return pluralizeUnit(s.baseUnitValue(), 2)
	case fMinimumStock, fReorderQuantity:
		return pluralizeUnit(unit, 2)
	}
	return ""
}

// fieldHint is itemFieldHint plus the unit notes that depend on the counting
// mode.
func (s *InventoryItemFormScreen) fieldHint(id int) string {
	// A kit's stock is zero by construction — receiving one credits its component
	// items, never itself — so the row is read-only and says why, in the width the
	// row actually affords at 80 columns. What SAVING does to a non-zero figure is
	// a longer and separate thing, wrapped under the row by kitStockWarnLines
	// (op-8n0).
	if s.fieldReadOnly(id) {
		return s.kitReadOnlyHint(id)
	}
	if unit := s.fieldUnit(id); unit != "" {
		return unit
	}
	return itemFieldHint[id]
}

func (s *InventoryItemFormScreen) toggleState(id int) bool {
	switch id {
	case fUseCaseBasedReorder:
		return s.useCaseBased
	case fReorderAlertsEnabled:
		return s.reorderAlerts
	case fIsHazardous:
		return s.isHazardous
	case fIsSerialized:
		return s.isSerialized
	case fIsActive:
		return s.isActive
	case fIsRetired:
		return s.isRetired
	}
	return false
}

// ---------------------------------------------------------------------------
// Category / location picker sub-phase
// ---------------------------------------------------------------------------

func (s *InventoryItemFormScreen) openPicker(id int) {
	if id == fCategory {
		s.phase = itemFormPhaseCategoryPick
	} else {
		s.phase = itemFormPhaseLocationPick
	}
	s.pickSearch.SetValue("")
	// The filter is always live in a columnar picker, so it holds the caret for
	// as long as the picker is open.
	s.pickSearch.Focus()
	s.applyPickFilter()

	// Start the cursor on the currently-selected option so re-picking is a
	// no-op keystroke.
	s.pickCursor = 0
	sel := s.categoryID
	if s.phase == itemFormPhaseLocationPick {
		sel = s.locationID
	}
	if sel != nil {
		for i, o := range s.pickOptions {
			if !o.clear && o.id == *sel {
				s.pickCursor = i
				break
			}
		}
	}
}

func (s *InventoryItemFormScreen) applyPickFilter() {
	q := strings.ToLower(strings.TrimSpace(s.pickSearch.Value()))
	opts := []itemPickOption{{clear: true, label: "(none)"}}
	if s.phase == itemFormPhaseCategoryPick {
		for _, c := range s.categories {
			if q == "" || strings.Contains(strings.ToLower(c.Name), q) {
				opts = append(opts, itemPickOption{id: c.ID, label: c.Name})
			}
		}
	} else {
		for _, l := range s.locations {
			label := l.Name
			if l.Code != "" {
				label = fmt.Sprintf("%s (%s)", l.Name, l.Code)
			}
			if q == "" || strings.Contains(strings.ToLower(label), q) {
				opts = append(opts, itemPickOption{id: l.ID, label: label})
			}
		}
	}
	s.pickOptions = opts
	if s.pickCursor >= len(s.pickOptions) {
		s.pickCursor = 0
	}
}

func (s *InventoryItemFormScreen) updatePickPhase(m tea.KeyMsg) (Screen, tea.Cmd) {
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

// movePick walks the option cursor, clamping at both ends — a picker list is a
// set of choices, not a ring, so running off the bottom must not reappear at the
// "(none)" row that clears the field.
func (s *InventoryItemFormScreen) movePick(delta int) {
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

func (s *InventoryItemFormScreen) closePicker() {
	s.phase = itemFormPhaseForm
	s.pickSearch.SetValue("")
	s.pickSearch.Blur()
	s.syncFocus()
}

func (s *InventoryItemFormScreen) commitPick() {
	if s.pickCursor >= 0 && s.pickCursor < len(s.pickOptions) {
		opt := s.pickOptions[s.pickCursor]
		switch s.phase {
		case itemFormPhaseCategoryPick:
			if opt.clear {
				s.categoryID = nil
			} else {
				id := opt.id
				s.categoryID = &id
			}
		case itemFormPhaseLocationPick:
			if opt.clear {
				s.locationID = nil
			} else {
				id := opt.id
				s.locationID = &id
			}
		}
	}
	s.closePicker()
}

// ---------------------------------------------------------------------------
// Submit
// ---------------------------------------------------------------------------

func (s *InventoryItemFormScreen) submit() (Screen, tea.Cmd) {
	// Refuse the save outright while "is this a kit?" is unanswered. The sheet
	// cannot know which endpoint to write to — /kits/ for a kit, /items/ for
	// anything else — and guessing wrong is a 404 on a record that is visibly on
	// screen. Enter is still the save key and the bar still names it: this is the
	// same shape as every other refusal here (a missing name, an impossible pack
	// chain), where pressing it reports why rather than doing nothing.
	//
	// The message carries neither the server's text nor the full sentence: the
	// status line is one UNWRAPPED row with 49 columns at the 80-column floor,
	// and kitErrLines already states the whole thing — reason included — WRAPPED
	// at the top of the body, where it cannot be cut. Repeating it here only lost
	// it, since the server's text is unbounded.
	if s.kitErr != "" {
		s.errMsg = "kit status unavailable — cannot save"
		return s, Status(s.errMsg, StatusError)
	}
	// Refuse an impossible chain here rather than sending it: the backend rejects
	// the same combinations, but by then the item write would already have landed.
	if errs := validatePackagingChain(s.packRows); len(errs) > 0 {
		s.errMsg = strings.Join(errs, " ")
		return s, Status(s.errMsg, StatusError)
	}
	if msg := resolveCountLevelError(s.countMode(), s.countLevelKey, s.packRows); msg != "" {
		s.errMsg = msg
		return s, Status(msg, StatusError)
	}

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
	id := s.itemID
	plan := s.packagingPlan()
	isKit := s.isKit()
	components := s.kitComponentsPayload()
	return s, func() tea.Msg {
		// Clear the counting mode BEFORE the item write only when the item write
		// would otherwise be rejected — the backend refuses to save a chain that
		// no longer holds the rung count_level points at, and refuses any write at
		// all while a pack mode has no level.
		if plan.detachBefore {
			if _, e := deps.OMS.SetItemCountMode(ctx, id, omsapi.CountModeEach, nil); e != nil {
				// Nothing has been written yet, so this is a plain save failure —
				// NOT a packErr, which means "the item saved but its packaging
				// did not".
				return itemFormSavedMsg{err: fmt.Errorf("could not clear the counting mode first: %w", e)}
			}
		}

		var item *omsapi.Item
		var e error
		switch {
		case isKit:
			// A kit is saved through /kits/, never /items/, and for two separate
			// reasons: the item viewset filters kits out of its queryset, so the
			// ordinary PATCH is a flat 404 for a kit id, and `components` is not a
			// field on the item serializer at all — the bill of materials has no
			// other write path (op-8n0).
			var kit *omsapi.Kit
			kit, e = deps.OMS.UpdateKit(ctx, id, omsapi.KitWrite{ItemWrite: body, Components: components})
			if kit != nil {
				item = &kit.Item
			}
		case edit:
			item, e = deps.OMS.UpdateInventoryItem(ctx, id, body)
		default:
			item, e = deps.OMS.CreateInventoryItem(ctx, body)
		}
		if e != nil {
			return itemFormSavedMsg{err: e, detached: plan.detachBefore}
		}

		// Everything below runs AFTER the item is safely saved, so a failure here
		// is a packErr — the item landed, its counting granularity did not.
		switch {
		case plan.detachAfter:
			// Switching to "each" when the item write did not need it done first:
			// waiting means a failed item write leaves the stored pack mode intact
			// instead of stranding the item in "each".
			updated, ce := deps.OMS.SetItemCountMode(ctx, item.ID, omsapi.CountModeEach, nil)
			if ce != nil {
				return itemFormSavedMsg{item: item, detached: plan.detachBefore, packErr: ce}
			}
			item = updated
		case plan.attach:
			// A pack level is a pk, so it is only knowable from the chain the write
			// just saved. Positions are the identity — the rung at the picked row's
			// index is the picked rung.
			level, ok := savedCountLevelID(item, plan.levelIndex)
			if !ok {
				return itemFormSavedMsg{
					item:     item,
					detached: plan.detachBefore,
					packErr:  errors.New("the saved packaging chain did not come back with the counting level"),
				}
			}
			updated, ce := deps.OMS.SetItemCountMode(ctx, item.ID, plan.mode, &level)
			if ce != nil {
				return itemFormSavedMsg{item: item, detached: plan.detachBefore, packErr: ce}
			}
			item = updated
		}
		return itemFormSavedMsg{item: item, detached: plan.detachBefore || plan.detachAfter}
	}
}

// packagingPlan is what the counting-mode writes have to do around the item
// write, given what the server already has.
//
// The pair (count_mode, count_level) can only be written together and only ever
// as a separate request, because a pack level is a pk that does not exist until
// the chain has been saved. So a save is up to three steps — detach, item, attach
// — of which an each-mode item with an unchanged chain needs exactly none.
// detachBefore vs detachAfter is the whole subtlety: clearing the pair is only
// done ahead of the item write when the item write would otherwise be REJECTED.
// When clearing is merely the target state, it waits until the item is saved, so
// an unrelated item-write failure cannot strand a pack-counted item in "each".
type itemPackagingPlan struct {
	detachBefore bool
	detachAfter  bool
	attach       bool
	mode         string
	levelIndex   int
}

func (s *InventoryItemFormScreen) packagingPlan() itemPackagingPlan {
	mode := s.countMode()
	chainDirty := chainSignature(s.packRows) != s.savedChainSig
	storedPack := s.savedCountMode != "" && s.savedCountMode != omsapi.CountModeEach
	plan := itemPackagingPlan{mode: mode, levelIndex: packagingRowIndex(s.packRows, s.countLevelKey)}

	// The item write carries no count_mode/count_level, so the backend validates
	// the STORED pair against it. It rejects the write in exactly two cases, both
	// mirrored here — and only for an item already opted into a pack mode, so an
	// each-mode item's save is untouched by any of this:
	//
	//   - no usable stored level (a pack mode whose count_level went null — the FK
	//     is SET_NULL, so another writer's chain edit can produce it): EVERY write
	//     is rejected until the mode is cleared, so clearing it is the repair.
	//   - a chain write that drops the stored level's POSITION: the backend checks
	//     exactly that the stored sort_order is among the ones being saved. A chain
	//     edit that KEEPS the position validates fine — the common case (renaming
	//     or resizing a rung) — and so needs nothing done first.
	storedLevelOK := s.savedCountLevel != nil && s.savedCountLevelSort >= 0
	plan.detachBefore = s.edit && storedPack &&
		(!storedLevelOK || (chainDirty && s.savedCountLevelSort >= len(s.packRows)))

	if mode == omsapi.CountModeEach {
		// Clearing the pair IS "switch to each" — the backend refuses "each" while
		// a level is set, and ItemWrite deliberately cannot express the pair. It
		// happens after the item write unless the write needed it done first.
		plan.detachAfter = s.edit && storedPack && !plan.detachBefore
		return plan
	}
	// Skip the follow-up only when the stored pair is already exactly what it
	// would write: same mode, chain untouched, and the picked row still the rung
	// whose pk the item points at.
	if !plan.detachBefore && !chainDirty && s.savedCountMode == mode && s.savedCountLevel != nil &&
		plan.levelIndex >= 0 && s.packRows[plan.levelIndex].id == *s.savedCountLevel {
		return plan
	}
	plan.attach = true
	return plan
}

// savedCountLevelID is the pk of the rung at sortOrder in a just-saved item's
// chain — the position IS the rung's identity on the wire, so the row the
// operator picked comes back as the rung with that sort_order.
func savedCountLevelID(item *omsapi.Item, sortOrder int) (int, bool) {
	if item == nil || sortOrder < 0 {
		return 0, false
	}
	for _, level := range item.PackagingLevels {
		if level.SortOrder == sortOrder {
			return level.ID, true
		}
	}
	return 0, false
}

// adoptSavedPackaging re-snapshots the chain from a saved item, keeping the
// counting-level pick on the same rung by POSITION (the chain came back in the
// order it was sent). Used when the item saved but its counting mode did not, so
// a retry writes only what is still outstanding.
func (s *InventoryItemFormScreen) adoptSavedPackaging(it *omsapi.Item) {
	idx := packagingRowIndex(s.packRows, s.countLevelKey)
	rows, next := toPackagingRows(it.PackagingLevels, s.packNextKey)
	s.packRows, s.packNextKey = rows, next
	s.countLevelKey = 0
	if idx >= 0 && idx < len(rows) {
		s.countLevelKey = rows[idx].key
	}
	s.savedChainSig = chainSignature(rows)
	s.savedBaseUnit = strings.TrimSpace(it.BaseUnit)
	s.savedCountMode = it.CountMode
	if s.savedCountMode == "" {
		s.savedCountMode = omsapi.CountModeEach
	}
	s.savedCountLevel = it.CountLevel
	s.savedCountLevelSort = -1
	if it.CountLevel != nil {
		for i, row := range rows {
			if row.id == *it.CountLevel {
				s.savedCountLevelSort = i
				break
			}
		}
	}
	s.rebuildFields()
}

func (s *InventoryItemFormScreen) buildPayload() (omsapi.ItemWrite, error) {
	var w omsapi.ItemWrite

	name := strings.TrimSpace(s.inputs[fName].Value())
	if name == "" {
		return w, errors.New("name is required")
	}
	cur, err := parseCount(s.inputs[fCurrentStock].Value(), "current stock", 0)
	if err != nil {
		return w, err
	}
	if s.isKit() {
		// current_stock has no omitempty, so it rides EVERY save, and KitSerializer
		// validates it against the stored value when the key is absent — omitting
		// it therefore cannot rescue a kit that already carries stray stock, it
		// would just make that kit unsaveable from ScanTTY for good. So the sheet
		// asserts the only value a kit may hold. That this OVERWRITES a real
		// number in shared data is exactly why the row is read-only and why a
		// non-zero figure gets an unmissable note before the save (op-8n0).
		cur = 0
	}
	minStock, err := parseCount(s.inputs[fMinimumStock].Value(), "minimum stock", 0)
	if err != nil {
		return w, err
	}
	roq, err := parseCount(s.inputs[fReorderQuantity].Value(), "reorder quantity", 1)
	if err != nil {
		return w, err
	}

	// is_serialized rides every save (no omitempty), and for a kit the sheet
	// asserts the only value one may hold rather than passing the row's reading
	// through. Same two reasons as current_stock above: KitSerializer.validate
	// REFUSES a truthy is_serialized on a kit ("its components are stocked, not
	// it"), so relaying a stray true would just fail every save the kit ever
	// gets — and because validate falls back to the STORED value when the key is
	// absent, omitting it would fail them just as surely. Asserting false is what
	// lets a kit carrying a stray flag be saved at all. That it OVERWRITES a real
	// stored fact is why the row is read-only and why a true one is called out
	// before the save (op-8n0).
	serialized := s.isSerialized
	if s.isKit() {
		serialized = false
	}

	w = omsapi.ItemWrite{
		Name:                name,
		Description:         strPtrTrim(s.inputs[fDescription].Value()),
		SKU:                 strPtrTrim(s.inputs[fSKU].Value()),
		ImageURL:            strPtrTrim(s.inputs[fImageURL].Value()),
		CurrentStock:        cur,
		MinimumStock:        minStock,
		ReorderQuantity:     roq,
		UseCaseBasedReorder: s.useCaseBased,
		// Sent unconditionally so switching the watch OFF reaches the backend.
		ReorderAlertsEnabled: s.reorderAlerts,
		Category:             s.categoryID,
		Location:             intPtrToStr(s.locationID),
		ShelfPosition:        selectValuePtr(shelfPositionOptions, s.shelfPos),
		IsHazardous:          s.isHazardous,
		IsSerialized:         serialized,
		IsActive:             s.isActive,
		IsRetired:            s.isRetired,
		Notes:                strPtrTrim(s.inputs[fNotes].Value()),
	}

	if s.useCaseBased {
		mc, err := parseCount(s.inputs[fMinimumCases].Value(), "minimum cases", 1)
		if err != nil {
			return w, err
		}
		rc, err := parseCount(s.inputs[fReorderCases].Value(), "reorder cases", 1)
		if err != nil {
			return w, err
		}
		w.MinimumCases = &mc
		w.ReorderCases = &rc
	}

	if s.isHazardous {
		msds := strings.TrimSpace(s.inputs[fMSDSURL].Value())
		if msds == "" {
			return w, errors.New("MSDS/SDS URL is required for hazardous materials")
		}
		w.MSDSURL = &msds
		if v, ok, err := parseNFPA(s.inputs[fNFPAHealth].Value(), "NFPA health"); err != nil {
			return w, err
		} else if ok {
			w.NFPAHealthHazard = &v
		}
		if v, ok, err := parseNFPA(s.inputs[fNFPAFire].Value(), "NFPA fire"); err != nil {
			return w, err
		} else if ok {
			w.NFPAFireHazard = &v
		}
		if v, ok, err := parseNFPA(s.inputs[fNFPAInstability].Value(), "NFPA instability"); err != nil {
			return w, err
		} else if ok {
			w.NFPAInstabilityHazard = &v
		}
		w.NFPASpecialHazards = strPtrTrim(s.inputs[fNFPASpecial].Value())
	}

	if serialized {
		mode := serialModeOptions[s.serialMode].value
		w.SerialTrackingMode = &mode
	}

	// Base unit is sent only when it CHANGED, for the same reason the chain is:
	// the backend's own default is "unit", so an each-mode item that has never
	// opted in would otherwise PATCH base_unit:"unit" back at it — a no-op that
	// still breaks "an un-opted-in item's write is what it always was". Blank
	// means "leave it alone" (the column is not nullable and rejects ""), so it
	// is never sent as an empty string.
	if bu := strings.TrimSpace(s.inputs[fBaseUnit].Value()); bu != "" && bu != s.savedBaseUnit {
		w.BaseUnit = &bu
	}

	// The chain is sent ONLY when it actually changed. Sending it unconditionally
	// would mean a form that failed to hydrate (an older backend, a partial
	// response) silently wiped a stored chain — and an each-mode item with no
	// packaging keeps writing exactly the request it always did.
	if chainSignature(s.packRows) != s.savedChainSig {
		levels := toPackagingPayload(s.packRows)
		w.PackagingLevels = &levels
	}

	return w, nil
}

// parseCount parses a non-negative integer with a minimum. An empty value is
// treated as 0 when min is 0 (matching the web form's default-0 stock fields);
// when min > 0 an empty value is an error.
func parseCount(raw, label string, min int) (int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		if min <= 0 {
			return 0, nil
		}
		return 0, fmt.Errorf("%s is required", label)
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("%s must be a whole number", label)
	}
	if n < min {
		return 0, fmt.Errorf("%s must be at least %d", label, min)
	}
	return n, nil
}

// parseNFPA parses an optional 0-4 rating. ok is false when the field is blank.
func parseNFPA(raw, label string) (val int, ok bool, err error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, false, nil
	}
	n, e := strconv.Atoi(raw)
	if e != nil {
		return 0, false, fmt.Errorf("%s must be a number 0-4", label)
	}
	if n < 0 || n > 4 {
		return 0, false, fmt.Errorf("%s must be between 0 and 4", label)
	}
	return n, true, nil
}

func strPtrTrim(v string) *string {
	v = strings.TrimSpace(v)
	if v == "" {
		return nil
	}
	return &v
}

func intPtrToStr(id *int) *string {
	if id == nil {
		return nil
	}
	v := strconv.Itoa(*id)
	return &v
}

func selectValuePtr(opts []selectOption, idx int) *string {
	if idx < 0 || idx >= len(opts) {
		return nil
	}
	v := opts[idx].value
	if v == "" {
		return nil
	}
	return &v
}

func (s *InventoryItemFormScreen) cancelCmd() tea.Cmd {
	if s.edit && s.itemID != "" {
		return SwitchTo(WSInventory, NewInventoryDetailScreen(s.deps, s.itemID))
	}
	return SwitchTo(WSInventory, newScreenFor(WSInventory, s.deps))
}

// ---------------------------------------------------------------------------
// View
// ---------------------------------------------------------------------------

func (s *InventoryItemFormScreen) View() string {
	if s.loading {
		return StyleMuted.Render("Loading…")
	}
	// A load error means the reference data or the edited item didn't arrive,
	// so the form can't be used — show the error and let the operator back out.
	if s.loadErr != "" {
		return StyleStatusError.Render("Error: ") + s.loadErr + "\n\n" + StyleMuted.Render("esc to go back")
	}
	switch s.phase {
	case itemFormPhaseCategoryPick, itemFormPhaseLocationPick:
		return s.viewPick()
	case itemFormPhaseChain:
		return s.viewChain()
	case itemFormPhaseChainRow:
		return s.viewChainRow()
	case itemFormPhaseKit:
		return s.viewKitList()
	case itemFormPhaseKitRow:
		return s.viewKitRow()
	case itemFormPhaseKitPick:
		return s.viewKitPick()
	}
	return s.viewForm()
}

func (s *InventoryItemFormScreen) viewForm() string {
	body := s.formLines()
	return s.frame(body, s.cursor, jdeStatusLine(s.saving, "Saving…", s.errMsg), s.formBar(body))
}

// formFields describes the visible fields as columnar rows. Toggles and selects
// are both bounded sets, so both draw "< value >"; the two foreign keys and the
// packaging chain show what is set today and are opened with Ctrl-E.
func (s *InventoryItemFormScreen) formFields() []jdeField {
	out := make([]jdeField, len(s.fields))
	for i, id := range s.fields {
		f := jdeField{
			Label:   itemFieldLabel[id],
			Width:   itemFieldWidth(id),
			Hint:    s.fieldHint(id),
			Focused: i == s.cursor,
		}
		if s.fieldReadOnly(id) {
			// Shown, not changed: the value is worth SEEING (it is what the save
			// is about to assert over) but it is not the operator's to set, so it
			// draws as a dimmed value rather than as an input the caret sits in
			// or a choice whose angle brackets promise ←/→ does something.
			f.Kind, f.Value, f.Dim = jdeValue, s.readOnlyValue(id), true
			out[i] = f
			continue
		}
		switch fieldKind(id) {
		case kindToggle:
			f.Kind, f.Value = jdeChoice, jdeYesNo(s.toggleState(id))
		case kindSelect:
			value, ok := s.selectValue(id)
			if !ok {
				// Nothing to cycle through yet (a counting level with no named
				// packaging rungs): it is a state, not a choice, so it must not
				// draw the angle brackets that promise ←/→ does something.
				f.Kind, f.Value, f.Dim = jdeValue, value, true
				break
			}
			f.Kind, f.Value = jdeChoice, value
		case kindPicker:
			value, dim := s.pickerValue(id)
			f.Kind, f.Value, f.Dim = jdeValue, value, dim
			if f.Focused {
				f.Hint = "Ctrl-E picks"
			}
		case kindChain:
			value, dim := s.chainValue()
			f.Kind, f.Value, f.Dim = jdeValue, value, dim
			if f.Focused {
				f.Hint = "Ctrl-E edits the levels"
			}
		case kindKit:
			value, dim := s.kitFieldValue()
			f.Kind, f.Value, f.Dim = jdeValue, value, dim
			if f.Focused {
				f.Hint = "Ctrl-E edits the components"
			}
			// Component names are long and this row is CLIPPED, not wrapped, so
			// the summary (and, when it must, the hint) is fitted to the pane.
			f = s.kitRowField(f)
		default:
			f.Kind, f.Value = jdeText, jdeInputValue(s.inputs[id], f.Focused)
		}
		out[i] = f
	}
	return out
}

// formLines builds the body, breaking the fields into the bands a printed sheet
// would have and drawing the focused choice row's whole option set under it.
func (s *InventoryItemFormScreen) formLines() *jdeLines {
	fields := s.formFields()
	labelWidth := jdeLabelWidth(fields)

	l := &jdeLines{}
	for _, line := range s.kitErrLines() {
		l.Add(line)
	}
	band := itemFieldBand(-1)
	for i, id := range s.fields {
		if b := itemFieldBandOf(id); b != band {
			if band != itemFieldBand(-1) {
				l.Add("")
			}
			l.Add(StyleJDEHeading.Render(itemBandLabel[b]))
			band = b
		}
		l.AddRow(i, renderJDEField(fields[i], labelWidth))
		// UNCONDITIONALLY, not only when focused: the operator has no reason to
		// move the cursor onto a read-only row, and this note is the one thing
		// that stops the save silently clearing a figure they can see.
		for _, line := range s.kitRowWarnLines(id, labelWidth) {
			l.AddRow(i, line)
		}
		if i == s.cursor {
			if strip := s.selectStrip(id, jdeStripWidth(s.bodyWidth(), labelWidth)); strip != "" {
				l.AddRow(i, jdeStripIndent(labelWidth)+StyleMuted.Render(strip))
			}
		}
	}
	// The suppliers band comes last, after every field: it is a detail grid, not
	// more fields, so it hangs off no leader column and its rows continue the
	// sheet's cursor space past the fields.
	s.supplierBand(l)
	return l
}

func (s *InventoryItemFormScreen) formBar(body *jdeLines) []actionBarItem {
	if s.supplierWarn {
		// The confirm owns the bar while it is up, because it owns the keyboard:
		// naming Enter=Save beside a question about discarding the sheet would
		// name a key that no longer does that.
		return []actionBarItem{{"Ctrl-E", "Discard & open"}, {"Esc", "Stay here"}}
	}
	items := []actionBarItem{{"Enter", "Save"}, {"Esc", "Cancel"}, {"UP/DN", "Fields"}}
	if _, onBand := s.onSupplierRow(); onBand {
		items = append(items, actionBarItem{"Ctrl-E", "Suppliers"})
	}
	if id, ok := s.currentFieldID(); ok {
		switch fieldKind(id) {
		case kindToggle:
			// Not on a frozen row: the bar must not teach a key that does
			// nothing where the cursor is standing.
			if !s.fieldReadOnly(id) {
				items = append(items, actionBarItem{"←→", "Change"})
			}
		case kindSelect:
			// A select with nothing to cycle through offers no ←/→ — the bar must
			// not teach a key that does nothing where the cursor is standing.
			if _, ok := s.selectValue(id); ok {
				items = append(items, actionBarItem{"←→", "Change"})
			}
		case kindPicker:
			items = append(items, actionBarItem{"Ctrl-E", "Pick"})
		case kindChain:
			items = append(items, actionBarItem{"Ctrl-E", "Edit levels"})
		case kindKit:
			items = append(items, actionBarItem{"Ctrl-E", "Edit components"})
		}
	}
	if avail := s.bodyRows(); avail > 0 && body.Len() > avail {
		items = append(items, actionBarItem{"PgUp/PgDn", "Page"})
	}
	return items
}

// selectValue is what goes between a select row's angle brackets, and whether
// the row has anything to cycle through at all.
func (s *InventoryItemFormScreen) selectValue(id int) (string, bool) {
	switch id {
	case fShelfPosition:
		if s.shelfPos >= 0 && s.shelfPos < len(shelfPositionOptions) {
			return shelfPositionOptions[s.shelfPos].label, true
		}
	case fSerialTrackingMode:
		if s.serialMode >= 0 && s.serialMode < len(serialModeOptions) {
			return serialModeOptions[s.serialMode].label, true
		}
	case fCountMode:
		if s.countModeIx >= 0 && s.countModeIx < len(countModeOptions) {
			return countModeOptions[s.countModeIx].label, true
		}
	case fCountLevel:
		return s.countLevelValue()
	}
	return "", false
}

// selectStrip is the focused select row's whole option set, bracketed on the
// current one — nothing for a two-value set, and nothing for the counting level,
// whose options are the packaging chain drawn two rows above.
func (s *InventoryItemFormScreen) selectStrip(id, width int) string {
	var opts []selectOption
	var idx int
	switch id {
	case fShelfPosition:
		opts, idx = shelfPositionOptions, s.shelfPos
	case fSerialTrackingMode:
		opts, idx = serialModeOptions, s.serialMode
	case fCountMode:
		opts, idx = countModeOptions, s.countModeIx
	default:
		return ""
	}
	labels := make([]string, len(opts))
	for i, o := range opts {
		labels[i] = o.label
	}
	return jdeOptionStrip(labels, idx, width)
}

// pickerValue is a foreign-key row's text and whether it is an empty state
// rather than a value. Plain text plus a flag, not pre-styled muted text: a
// focused row reverse-videos the whole field, and an inner reset sequence would
// end the highlight partway through it.
func (s *InventoryItemFormScreen) pickerValue(id int) (string, bool) {
	if id == fCategory {
		if s.categoryID == nil {
			return "(none)", true
		}
		for _, c := range s.categories {
			if c.ID == *s.categoryID {
				return c.Name, false
			}
		}
		return fmt.Sprintf("#%d", *s.categoryID), false
	}
	// location
	if s.locationID == nil {
		return "(none)", true
	}
	for _, l := range s.locations {
		if l.ID == *s.locationID {
			return l.Name, false
		}
	}
	return fmt.Sprintf("#%d", *s.locationID), false
}

// fieldWindow returns [start,end) so cursor stays roughly centred within a
// window of at most `visible` rows.
func fieldWindow(cursor, total, visible int) (int, int) {
	if visible >= total {
		return 0, total
	}
	start := cursor - visible/2
	if start < 0 {
		start = 0
	}
	end := start + visible
	if end > total {
		end = total
		start = end - visible
		if start < 0 {
			start = 0
		}
	}
	return start, end
}

// pickView builds the open picker's pinned header and its option list.
func (s *InventoryItemFormScreen) pickView() ([]string, *jdeLines) {
	title, empty := "Category", "(no matching categories)"
	if s.phase == itemFormPhaseLocationPick {
		title, empty = "Location", "(no matching locations)"
	}
	return jdePickList{
		Title:  title,
		Note:   "Row 1 is none — it leaves the field unset.",
		Filter: s.pickSearch,
		Count:  len(s.pickOptions),
		Label:  func(i int) string { return s.pickOptions[i].label },
		Dim:    func(i int) bool { return s.pickOptions[i].clear },
		Cursor: s.pickCursor,
		Empty:  empty,
	}.render()
}

func (s *InventoryItemFormScreen) viewPick() string {
	header, body := s.pickView()
	paging := false
	if avail := s.bodyRows(); avail > 0 && body.Len() > avail-len(header) {
		paging = true
	}
	return s.frameWithHeader(header, body, s.pickCursor,
		jdeStatusLine(false, "", ""), jdePickBar("Select", paging))
}
