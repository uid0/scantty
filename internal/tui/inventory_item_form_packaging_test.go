package tui

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/uid0/scantty/internal/omsapi"
)

// Packaging matrix on the item form (OMS #979/#981, web #983).

// newPackagingForm returns a loaded create-mode form ready to drive.
func newPackagingForm(t *testing.T) *InventoryItemFormScreen {
	t.Helper()
	s := NewInventoryItemFormScreen(Deps{}, "")
	s.loading = false
	s.terminalHeight = 40
	s.inputs[fName].SetValue("Copy paper")
	return s
}

// typeInto feeds a string into the focused chain-row input one rune at a time,
// which is how the real key path reaches it.
func typeInto(s *InventoryItemFormScreen, text string) {
	for _, r := range text {
		s.Update(runeKey(r))
	}
}

// addChainRow drives the real key path: the chain list's "a", the row editor's
// two fields, and enter to commit.
func addChainRow(s *InventoryItemFormScreen, name, units string) {
	s.Update(runeKey('a'))
	typeInto(s, name)
	s.Update(tea.KeyMsg{Type: tea.KeyTab})
	typeInto(s, units)
	s.Update(ccEnterKey())
}

// TestItemForm_CountLevelRowFollowsMode pins the conditional field: the counting
// level exists only for the two pack-counting modes, exactly as the web renders
// its select conditionally.
func TestItemForm_CountLevelRowFollowsMode(t *testing.T) {
	s := newPackagingForm(t)

	// Every item starts each-mode, so the base-unit / chain / mode rows are
	// offered but the counting level is not.
	for _, id := range []int{fBaseUnit, fPackChain, fCountMode} {
		if !fieldsContain(s.fields, id) {
			t.Errorf("field %d should always be offered", id)
		}
	}
	if fieldsContain(s.fields, fCountLevel) {
		t.Error("each mode must not offer a counting level")
	}

	s.setCursorToField(fCountMode)
	s.cycleSelect(fCountMode, +1)
	if s.countMode() != omsapi.CountModeByLevel {
		t.Fatalf("count mode = %q, want by_level", s.countMode())
	}
	if !fieldsContain(s.fields, fCountLevel) {
		t.Error("a pack-counting mode must offer a counting level")
	}

	// Dropping back to each clears the pick as well as hiding the row — the
	// backend refuses "each" while a level is set, so a stale pick could only 400.
	s.countLevelKey = 4
	s.setCursorToField(fCountMode)
	s.cycleSelect(fCountMode, -1)
	if s.countMode() != omsapi.CountModeEach {
		t.Fatalf("count mode = %q, want each", s.countMode())
	}
	if s.countLevelKey != 0 {
		t.Errorf("dropping to each must clear the counting level, got %d", s.countLevelKey)
	}
	if fieldsContain(s.fields, fCountLevel) {
		t.Error("each mode must hide the counting level again")
	}
}

// TestItemForm_ChainEditorAddsRows drives the sub-phases through real keypresses.
func TestItemForm_ChainEditorAddsRows(t *testing.T) {
	s := newPackagingForm(t)
	s.setCursorToField(fPackChain)

	// space opens the chain list; the empty state says the item is counted in
	// base units rather than showing a bare empty table.
	s.Update(runeKey(' '))
	if s.phase != itemFormPhaseChain {
		t.Fatalf("space on the chain row should open the editor, got phase %d", s.phase)
	}
	if out := s.View(); !strings.Contains(out, "No packaging levels") {
		t.Errorf("empty chain view = %q", out)
	}

	addChainRow(s, "case", "5000")
	addChainRow(s, "ream", "500")
	addChainRow(s, "sheet", "1")
	if s.phase != itemFormPhaseChain {
		t.Fatalf("committing a row should return to the list, got phase %d", s.phase)
	}
	if len(s.packRows) != 3 {
		t.Fatalf("rows = %d, want 3", len(s.packRows))
	}
	if s.packRows[0].name != "case" || s.packRows[0].baseUnits != 5000 {
		t.Errorf("first row = %+v", s.packRows[0])
	}
	// New rungs are appended innermost, which is where a base unit belongs, so
	// the typed order needs no reordering and the chain validates.
	if errs := validatePackagingChain(s.packRows); len(errs) != 0 {
		t.Errorf("typed chain should validate: %v", errs)
	}
	// The list shows the derived "1 case = 10 reams" ratio.
	if out := s.View(); !strings.Contains(out, "10 reams") {
		t.Errorf("chain view missing derived ratio: %q", out)
	}

	// A rung with no size is refused by the row editor, not silently accepted.
	s.Update(runeKey('a'))
	typeInto(s, "pallet")
	s.Update(ccEnterKey())
	if s.phase != itemFormPhaseChainRow {
		t.Errorf("a sizeless rung must keep the editor open, got phase %d", s.phase)
	}
	if s.chainRowErr == "" {
		t.Error("a sizeless rung must report an error")
	}
	if len(s.packRows) != 3 {
		t.Errorf("rows = %d, want the rejected rung not added", len(s.packRows))
	}
	s.Update(ccEscKey())

	// esc leaves the editor and re-derives the form's field list.
	s.Update(ccEscKey())
	if s.phase != itemFormPhaseForm {
		t.Errorf("esc should return to the form, got phase %d", s.phase)
	}
	if !strings.Contains(s.chainSummary(), "case › ream › sheet") {
		t.Errorf("chain summary = %q", s.chainSummary())
	}
}

// TestItemForm_ChainEditKeepsCountLevelPick is why rows carry a client-side key:
// the pick has to survive a reorder, and has to be dropped when the picked rung
// is deleted (a dangling key would fail validation with a confusing message).
func TestItemForm_ChainEditKeepsCountLevelPick(t *testing.T) {
	s := newPackagingForm(t)
	s.setCursorToField(fPackChain)
	s.Update(runeKey(' '))
	addChainRow(s, "case", "100")
	addChainRow(s, "bag", "1")

	caseKey := s.packRows[0].key
	s.countLevelKey = caseKey

	// Move the picked rung down: the pick rides the ROW, not the position.
	s.chainCursor = 0
	s.Update(runeKey('J'))
	if s.packRows[1].key != caseKey {
		t.Fatalf("J should have moved the case rung down, rows = %+v", s.packRows)
	}
	if s.countLevelKey != caseKey {
		t.Errorf("the pick must follow the row through a reorder, got %d", s.countLevelKey)
	}
	// Move it back so the chain validates again.
	s.Update(runeKey('K'))

	// Editing the picked rung in place keeps the pick.
	s.chainCursor = 0
	s.Update(ccEnterKey())
	if s.phase != itemFormPhaseChainRow {
		t.Fatalf("enter should open the row editor, got phase %d", s.phase)
	}
	s.chainRowName.SetValue("carton")
	s.Update(ccEnterKey())
	if s.packRows[0].name != "carton" {
		t.Errorf("edited row name = %q", s.packRows[0].name)
	}
	if s.countLevelKey != caseKey {
		t.Errorf("an in-place edit must keep the pick, got %d", s.countLevelKey)
	}

	// Removing the picked rung clears the pick.
	s.chainCursor = 0
	s.Update(runeKey('x'))
	if len(s.packRows) != 1 {
		t.Fatalf("rows = %d, want 1 after removal", len(s.packRows))
	}
	if s.countLevelKey != 0 {
		t.Errorf("removing the picked rung must clear the pick, got %d", s.countLevelKey)
	}
}

// TestItemForm_CountLevelCycleWalksNamedRows pins the counting-level select.
func TestItemForm_CountLevelCycleWalksNamedRows(t *testing.T) {
	s := newPackagingForm(t)
	s.packRows = []packagingRow{
		{key: 1, name: "case", baseUnits: 100},
		{key: 2, name: "bag", baseUnits: 1},
	}
	s.countModeIx = countModeIndex(omsapi.CountModeByLevel)
	s.rebuildFields()

	// Nothing picked: one press lands on the first rung rather than skipping it.
	s.cycleSelect(fCountLevel, +1)
	if s.countLevelKey != 1 {
		t.Fatalf("first cycle = %d, want the outermost rung", s.countLevelKey)
	}
	s.cycleSelect(fCountLevel, +1)
	if s.countLevelKey != 2 {
		t.Errorf("second cycle = %d, want the inner rung", s.countLevelKey)
	}
	// Wraps around.
	s.cycleSelect(fCountLevel, +1)
	if s.countLevelKey != 1 {
		t.Errorf("cycle should wrap, got %d", s.countLevelKey)
	}
	s.cycleSelect(fCountLevel, -1)
	if s.countLevelKey != 2 {
		t.Errorf("backwards cycle = %d", s.countLevelKey)
	}

	// An unnamed rung is not offerable — it cannot be a legal counting level.
	s.packRows = []packagingRow{{key: 5, name: "  ", baseUnits: 1}}
	s.countLevelKey = 0
	s.cycleSelect(fCountLevel, +1)
	if s.countLevelKey != 0 {
		t.Errorf("an unnamed rung must not be pickable, got %d", s.countLevelKey)
	}
	if !strings.Contains(s.countLevelSummary(), "none selected") {
		t.Errorf("count level summary = %q", s.countLevelSummary())
	}
	// With no chain at all the field says to add one first, like the web's
	// placeholder.
	s.packRows = nil
	if !strings.Contains(s.countLevelSummary(), "add a packaging level first") {
		t.Errorf("empty-chain summary = %q", s.countLevelSummary())
	}
}

// TestItemForm_ThresholdLabelsFollowCountUnit pins the phase-2a contract shift:
// minimum_stock / reorder_quantity are read in the COUNT unit for the pack modes,
// so the labels have to say so — and must NOT for an each-mode item.
func TestItemForm_ThresholdLabelsFollowCountUnit(t *testing.T) {
	s := newPackagingForm(t)
	if got := s.fieldLabel(fMinimumStock); got != "Minimum stock" {
		t.Errorf("each-mode minimum label = %q, want the plain one", got)
	}
	if got := s.fieldLabel(fCurrentStock); got != "Current stock" {
		t.Errorf("each-mode stock label = %q, want the plain one", got)
	}

	s.inputs[fBaseUnit].SetValue("sheet")
	s.packRows = []packagingRow{
		{key: 1, name: "case", baseUnits: 5000},
		{key: 2, name: "ream", baseUnits: 500},
		{key: 3, name: "sheet", baseUnits: 1},
	}
	s.countModeIx = countModeIndex(omsapi.CountModeByLevel)
	s.countLevelKey = 2 // counted in reams
	s.rebuildFields()

	if got := s.fieldLabel(fMinimumStock); got != "Minimum stock (reams)" {
		t.Errorf("minimum label = %q", got)
	}
	if got := s.fieldLabel(fReorderQuantity); got != "Reorder quantity (reams)" {
		t.Errorf("reorder label = %q", got)
	}
	// Stock itself stays canonical in BASE units, so it names those instead.
	if got := s.fieldLabel(fCurrentStock); got != "Current stock (sheets)" {
		t.Errorf("stock label = %q", got)
	}

	// A pack mode with nothing picked has no unit to name yet.
	s.countLevelKey = 0
	if got := s.fieldLabel(fMinimumStock); got != "Minimum stock" {
		t.Errorf("unpicked minimum label = %q, want the plain one", got)
	}
}

// TestItemForm_PayloadOmitsUntouchedPackaging is the invariant in form terms: a
// save that never touched the packaging section writes exactly what it always did.
func TestItemForm_PayloadOmitsUntouchedPackaging(t *testing.T) {
	s := newPackagingForm(t)
	w, err := s.buildPayload()
	if err != nil {
		t.Fatalf("buildPayload: %v", err)
	}
	if w.PackagingLevels != nil {
		t.Errorf("untouched chain must be omitted, got %v", *w.PackagingLevels)
	}
	if w.BaseUnit != nil {
		t.Errorf("blank base unit must be omitted, got %q", *w.BaseUnit)
	}

	// Hydrating an item with a chain and re-saving unchanged also sends nothing:
	// a form that failed to hydrate must never be able to wipe a stored chain.
	e := NewInventoryItemFormScreen(Deps{}, "abc")
	e.item = paperItem()
	e.hydrate()
	e.loading = false
	w, err = e.buildPayload()
	if err != nil {
		t.Fatalf("buildPayload(edit): %v", err)
	}
	if w.PackagingLevels != nil {
		t.Errorf("re-saving an unchanged chain must send nothing, got %v", *w.PackagingLevels)
	}
	// An unchanged base unit is not re-sent either — the backend's own default is
	// "unit", so round-tripping it would mean every legacy item's edit PATCHed a
	// base_unit it never had before.
	if w.BaseUnit != nil {
		t.Errorf("an unchanged base unit must be omitted, got %q", *w.BaseUnit)
	}

	// The strict form of the invariant: a legacy each-mode item carrying the
	// backend default must produce a payload with NO packaging keys at all.
	legacy := NewInventoryItemFormScreen(Deps{}, "abc")
	legacy.item = &omsapi.Item{
		ID: "abc", Name: "Widget", Stock: 12, MinimumStock: 1, ReorderQuantity: 1,
		BaseUnit: "unit", CountMode: omsapi.CountModeEach,
	}
	legacy.hydrate()
	w, err = legacy.buildPayload()
	if err != nil {
		t.Fatalf("buildPayload(legacy): %v", err)
	}
	if w.BaseUnit != nil || w.PackagingLevels != nil {
		t.Errorf("a legacy item's write must carry no packaging keys, got base=%v chain=%v",
			w.BaseUnit, w.PackagingLevels)
	}
	if plan := legacy.packagingPlan(); plan.detachBefore || plan.detachAfter || plan.attach {
		t.Errorf("a legacy item's save must make no packaging request, got %+v", plan)
	}

	// Changing the base unit does send it.
	legacy.inputs[fBaseUnit].SetValue("glove")
	w, err = legacy.buildPayload()
	if err != nil {
		t.Fatalf("buildPayload(renamed unit): %v", err)
	}
	if w.BaseUnit == nil || *w.BaseUnit != "glove" {
		t.Errorf("a changed base unit must be sent, got %v", w.BaseUnit)
	}
}

// TestItemForm_PayloadSendsDirtyChain pins the write half.
func TestItemForm_PayloadSendsDirtyChain(t *testing.T) {
	s := newPackagingForm(t)
	s.inputs[fBaseUnit].SetValue("  sheet  ")
	s.packRows = []packagingRow{
		{key: 1, name: "case", baseUnits: 5000},
		{key: 2, name: "sheet", baseUnits: 1},
	}
	w, err := s.buildPayload()
	if err != nil {
		t.Fatalf("buildPayload: %v", err)
	}
	if w.BaseUnit == nil || *w.BaseUnit != "sheet" {
		t.Fatalf("base unit = %v, want trimmed", w.BaseUnit)
	}
	if w.PackagingLevels == nil {
		t.Fatal("a changed chain must be sent")
	}
	levels := *w.PackagingLevels
	if len(levels) != 2 || levels[0].SortOrder != 0 || levels[1].SortOrder != 1 {
		t.Errorf("levels = %+v", levels)
	}

	// Clearing a hydrated chain sends an explicit empty list, which is what
	// deletes the stored rungs.
	e := NewInventoryItemFormScreen(Deps{}, "abc")
	e.item = paperItem()
	e.hydrate()
	e.packRows = nil
	e.countLevelKey = 0
	e.countModeIx = countModeIndex(omsapi.CountModeEach)
	w, err = e.buildPayload()
	if err != nil {
		t.Fatalf("buildPayload(cleared): %v", err)
	}
	if w.PackagingLevels == nil {
		t.Fatal("a cleared chain must be sent as an explicit empty list")
	}
	if len(*w.PackagingLevels) != 0 {
		t.Errorf("cleared chain = %+v, want empty", *w.PackagingLevels)
	}
}

// TestItemForm_PackagingPlan covers the three-step save ordering, which exists
// because the (count_mode, count_level) pair is only legal together and a pack
// level is a pk that does not exist until the chain has been saved.
func TestItemForm_PackagingPlan(t *testing.T) {
	// An each-mode create that never touched packaging needs no packaging write.
	s := newPackagingForm(t)
	plan := s.packagingPlan()
	if plan.detachBefore || plan.detachAfter || plan.attach {
		t.Errorf("an opted-out save must write no packaging, got %+v", plan)
	}

	// A create that opts into a pack mode attaches afterwards: the level pk only
	// exists once the chain is saved.
	s.packRows = []packagingRow{
		{key: 1, name: "case", baseUnits: 100},
		{key: 2, name: "bag", baseUnits: 1},
	}
	s.countModeIx = countModeIndex(omsapi.CountModeByLevel)
	s.countLevelKey = 1
	plan = s.packagingPlan()
	if plan.detachBefore || plan.detachAfter {
		t.Error("a create has no stored mode to detach")
	}
	if !plan.attach || plan.mode != omsapi.CountModeByLevel || plan.levelIndex != 0 {
		t.Errorf("plan = %+v, want attach by_level at index 0", plan)
	}

	// Editing a pack item without touching the chain or the pick writes nothing:
	// the stored pair already is what would be written.
	e := NewInventoryItemFormScreen(Deps{}, "abc")
	e.item = paperItem()
	e.hydrate()
	plan = e.packagingPlan()
	if plan.detachBefore || plan.detachAfter || plan.attach {
		t.Errorf("an unchanged pack item must write no packaging, got %+v", plan)
	}

	// Switching a pack item to each detaches (which is the whole write) and
	// attaches nothing.
	e2 := NewInventoryItemFormScreen(Deps{}, "abc")
	e2.item = paperItem()
	e2.hydrate()
	e2.countModeIx = countModeIndex(omsapi.CountModeEach)
	e2.countLevelKey = 0
	plan = e2.packagingPlan()
	// The item write would have been accepted with the stored pair intact, so the
	// clear waits until AFTER it — a failed item write must not strand the item in
	// "each".
	if plan.detachBefore {
		t.Error("switching to each must not clear the mode before a write that would succeed")
	}
	if !plan.detachAfter {
		t.Error("switching to each must clear the stored level after the item write")
	}
	if plan.attach {
		t.Error("each mode has nothing to attach")
	}

	// A chain edit that KEEPS the counting level's position needs no detach: the
	// backend only refuses a chain write that drops the stored sort_order, so the
	// item and its chain go in one request and the mode is re-attached after.
	// Avoiding the detach matters — it is a real write, and a later failure would
	// strand the item in "each".
	e3 := NewInventoryItemFormScreen(Deps{}, "abc")
	e3.item = paperItem()
	e3.hydrate()
	e3.packRows[1].baseUnits = 250 // resize the counted rung, same position
	plan = e3.packagingPlan()
	if plan.detachBefore || plan.detachAfter {
		t.Error("a chain edit that keeps the level's position must not detach")
	}
	if !plan.attach || plan.levelIndex != 1 {
		t.Errorf("plan = %+v, want attach at the picked row's position", plan)
	}

	// A chain edit that drops the counting level's position DOES detach first —
	// otherwise the backend rejects the chain write.
	e4 := NewInventoryItemFormScreen(Deps{}, "abc")
	e4.item = paperItem()
	e4.hydrate()
	e4.packRows = e4.packRows[:1] // only the outermost rung survives
	e4.packRows[0].baseUnits = 1
	e4.countLevelKey = e4.packRows[0].key
	plan = e4.packagingPlan()
	if !plan.detachBefore {
		t.Error("a chain edit that drops the stored level's position must detach first")
	}
	if !plan.attach || plan.levelIndex != 0 {
		t.Errorf("plan = %+v, want attach at index 0", plan)
	}

	// A pack item whose count_level went null (the FK is SET_NULL, so another
	// writer's chain edit can produce it) cannot be written at all until the mode
	// is cleared — so the detach is the repair, chain dirty or not.
	e5 := NewInventoryItemFormScreen(Deps{}, "abc")
	broken := paperItem()
	broken.CountLevel = nil
	e5.item = broken
	e5.hydrate()
	e5.countModeIx = countModeIndex(omsapi.CountModeByLevel)
	e5.countLevelKey = e5.packRows[1].key
	plan = e5.packagingPlan()
	if !plan.detachBefore {
		t.Error("a pack item with no stored level must detach to become writable")
	}
	if !plan.attach {
		t.Error("and must then attach the mode it should have")
	}
}

// TestItemForm_SavedCountLevelID resolves the pk out of a saved chain by
// position, which is the identity the serializer upserts on.
func TestItemForm_SavedCountLevelID(t *testing.T) {
	item := paperItem()
	if got, ok := savedCountLevelID(item, 1); !ok || got != 9 {
		t.Errorf("savedCountLevelID(1) = %d/%v, want the ream pk 9", got, ok)
	}
	if _, ok := savedCountLevelID(item, 7); ok {
		t.Error("a position the chain does not have must report not-ok")
	}
	if _, ok := savedCountLevelID(item, -1); ok {
		t.Error("no pick must report not-ok")
	}
	if _, ok := savedCountLevelID(nil, 0); ok {
		t.Error("a nil item must report not-ok")
	}
}

// TestItemForm_RefusesImpossibleChainBeforeSaving keeps a bad chain off the wire:
// the backend rejects the same combinations, but by then the item write would
// already have landed.
func TestItemForm_RefusesImpossibleChainBeforeSaving(t *testing.T) {
	s := newPackagingForm(t)
	// Two base rungs — a chain the backend rejects.
	s.packRows = []packagingRow{
		{key: 1, name: "case", baseUnits: 1},
		{key: 2, name: "bag", baseUnits: 1},
	}
	if _, cmd := s.submit(); cmd == nil {
		t.Fatal("submit should have returned a status command")
	}
	if s.saving {
		t.Error("an invalid chain must not start a save")
	}
	if !strings.Contains(s.errMsg, "base unit") {
		t.Errorf("errMsg = %q, want the chain rule", s.errMsg)
	}

	// A pack mode with no counting level is refused too.
	s2 := newPackagingForm(t)
	s2.packRows = []packagingRow{
		{key: 1, name: "case", baseUnits: 100},
		{key: 2, name: "bag", baseUnits: 1},
	}
	s2.countModeIx = countModeIndex(omsapi.CountModeOpenClosed)
	s2.countLevelKey = 0
	s2.submit()
	if s2.saving {
		t.Error("a pack mode with no level must not start a save")
	}
	if !strings.Contains(s2.errMsg, "counted in") {
		t.Errorf("errMsg = %q, want the count-level rule", s2.errMsg)
	}
}

// TestItemForm_PackErrorAdoptsSavedItem: when the item saved but its counting
// mode did not, the form must adopt the new id so a retry PATCHes that item
// instead of creating a second one.
func TestItemForm_PackErrorAdoptsSavedItem(t *testing.T) {
	s := newPackagingForm(t)
	s.packRows = []packagingRow{
		{key: 1, name: "case", baseUnits: 100},
		{key: 2, name: "bag", baseUnits: 1},
	}
	s.countModeIx = countModeIndex(omsapi.CountModeByLevel)
	s.countLevelKey = 1
	s.saving = true

	saved := bagItem()
	saved.CountMode = omsapi.CountModeEach
	saved.CountLevel = nil
	s.Update(itemFormSavedMsg{item: saved, packErr: errAssert("boom")})

	if !s.edit || s.itemID != saved.ID {
		t.Errorf("form should have adopted the saved item, edit=%v id=%q", s.edit, s.itemID)
	}
	if !strings.Contains(s.errMsg, "packaging setup failed") {
		t.Errorf("errMsg = %q", s.errMsg)
	}
	// The chain landed with the item, so only the mode/level pair is outstanding:
	// a retry must plan attach-only, and must not re-send the chain.
	if s.savedChainSig != chainSignature(s.packRows) {
		t.Errorf("the saved chain should be adopted as the new baseline")
	}
	plan := s.packagingPlan()
	if plan.detachBefore || plan.detachAfter {
		t.Error("a retry must not detach: the adopted item is already each-mode")
	}
	if !plan.attach {
		t.Error("a retry must still attach the counting mode")
	}
	w, err := s.buildPayload()
	if err != nil {
		t.Fatalf("buildPayload: %v", err)
	}
	if w.PackagingLevels != nil {
		t.Errorf("a retry must not re-send the chain, got %v", *w.PackagingLevels)
	}
}

// errAssert is a tiny error value for table-free assertions.
type errAssert string

func (e errAssert) Error() string { return string(e) }

// TestItemForm_PackagingRenderSmoke exercises both new sub-phase renderers with
// a fully configured chain, guarding the windowed/indexed rendering.
func TestItemForm_PackagingRenderSmoke(t *testing.T) {
	s := NewInventoryItemFormScreen(Deps{}, "abc")
	s.item = paperItem()
	s.hydrate()
	s.loading = false
	s.terminalHeight = 40

	if out := s.View(); !strings.Contains(out, "Count mode") {
		t.Errorf("form view missing the count-mode row: %q", out)
	}
	s.openChain()
	out := s.View()
	for _, want := range []string{"Packaging chain", "case", "10 reams", "[counted here]"} {
		if !strings.Contains(out, want) {
			t.Errorf("chain view missing %q: %q", want, out)
		}
	}
	s.openChainRow(0)
	if out := s.View(); !strings.Contains(out, "Edit packaging level") {
		t.Errorf("row view = %q", out)
	}
	s.openChainRow(-1)
	if out := s.View(); !strings.Contains(out, "Add packaging level") {
		t.Errorf("add-row view = %q", out)
	}

	// A single-character base unit must not panic the row editor's label casing.
	s.inputs[fBaseUnit].SetValue("g")
	if out := s.View(); out == "" {
		t.Error("row view empty with a one-character base unit")
	}
}

// TestItemForm_SwitchToEachClearsModeAfterTheItemWrite pins the ORDER, not just
// the plan: the counting mode is cleared only once the item is safely saved, so an
// item-write failure cannot strand a pack-counted item in "each".
func TestItemForm_SwitchToEachClearsModeAfterTheItemWrite(t *testing.T) {
	var seq []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		// The mode write is the one that carries count_mode; the item write is the
		// one that carries name.
		if _, isMode := body["count_mode"]; isMode {
			seq = append(seq, "mode")
		} else {
			seq = append(seq, "item")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"abc","name":"Copy paper","current_stock":2500,
			"minimum_stock":2,"reorder_quantity":4,"count_mode":"each"}`))
	}))
	defer srv.Close()

	s := NewInventoryItemFormScreen(Deps{OMS: omsapi.New(srv.URL)}, "abc")
	s.item = paperItem()
	s.hydrate()
	s.loading = false
	// Switch the by_level item to each, touching nothing else.
	s.countModeIx = countModeIndex(omsapi.CountModeEach)
	s.countLevelKey = 0

	_, cmd := s.submit()
	if cmd == nil {
		t.Fatal("submit returned no command")
	}
	msg, ok := cmd().(itemFormSavedMsg)
	if !ok {
		t.Fatalf("msg = %T", cmd())
	}
	if msg.err != nil || msg.packErr != nil {
		t.Fatalf("save failed: err=%v packErr=%v", msg.err, msg.packErr)
	}
	if len(seq) != 2 || seq[0] != "item" || seq[1] != "mode" {
		t.Errorf("request order = %v, want the item write BEFORE the mode clear", seq)
	}
	if !msg.detached {
		t.Error("a completed clear must be reported as detached")
	}
}
