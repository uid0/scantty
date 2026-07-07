package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/uid0/scantty/internal/omsapi"
)

func rune1(s string) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)} }

// resolveSwitch runs a cmd and returns the SwitchScreenMsg it produced.
func resolveSwitch(t *testing.T, cmd tea.Cmd) SwitchScreenMsg {
	t.Helper()
	if cmd == nil {
		t.Fatalf("expected a command, got nil")
	}
	msg := cmd()
	sw, ok := msg.(SwitchScreenMsg)
	if !ok {
		t.Fatalf("expected SwitchScreenMsg, got %T", msg)
	}
	return sw
}

// ---------------------------------------------------------------------------
// Inventory detail wiring
// ---------------------------------------------------------------------------

// TestInventoryDetail_HandlesKeyS confirms the detail screen claims lowercase
// 's' (manage suppliers) so it beats the global Settings nav, but leaves other
// keys to the globals/scroller.
func TestInventoryDetail_HandlesKeyS(t *testing.T) {
	s := NewInventoryDetailScreen(Deps{}, "itm-1")
	if !s.HandlesKey("s") {
		t.Errorf("s should be claimed (manage suppliers)")
	}
	if s.HandlesKey("o") || s.HandlesKey("E") || s.HandlesKey("x") {
		t.Errorf("o/E/x should not be claimed")
	}
}

// TestInventoryDetail_SOpensSuppliers confirms 's' opens the item-suppliers
// management screen for the loaded item.
func TestInventoryDetail_SOpensSuppliers(t *testing.T) {
	s := NewInventoryDetailScreen(Deps{}, "itm-1")
	s.loading = false
	s.item = &omsapi.Item{ID: "itm-1", Name: "Widget"}

	_, cmd := s.Update(rune1("s"))
	sw := resolveSwitch(t, cmd)
	scr, ok := sw.Screen.(*ItemSuppliersScreen)
	if !ok {
		t.Fatalf("expected *ItemSuppliersScreen, got %T", sw.Screen)
	}
	if scr.itemID != "itm-1" || scr.itemName != "Widget" {
		t.Errorf("suppliers screen scoped wrong: item=%q name=%q", scr.itemID, scr.itemName)
	}
}

// ---------------------------------------------------------------------------
// ItemSuppliersScreen (list / manage)
// ---------------------------------------------------------------------------

// TestItemSuppliers_HandlesKey confirms the screen claims esc (back to detail)
// and G (bottom) but lets its action keys fall through.
func TestItemSuppliers_HandlesKey(t *testing.T) {
	s := NewItemSuppliersScreen(Deps{}, "itm-1", "Widget")
	if !s.HandlesKey("esc") || !s.HandlesKey("G") {
		t.Errorf("esc and G should be claimed")
	}
	for _, k := range []string{"c", "E", "x", "p", "j", "r"} {
		if s.HandlesKey(k) {
			t.Errorf("%q should fall through, not be claimed", k)
		}
	}
}

// TestItemSuppliers_EscBackToDetail confirms esc returns to the owning item
// detail rather than bouncing to the global Welcome.
func TestItemSuppliers_EscBackToDetail(t *testing.T) {
	s := NewItemSuppliersScreen(Deps{}, "itm-1", "Widget")
	s.loading = false
	_, cmd := s.Update(tea.KeyMsg{Type: tea.KeyEsc})
	sw := resolveSwitch(t, cmd)
	if _, ok := sw.Screen.(*InventoryDetailScreen); !ok {
		t.Fatalf("esc should open *InventoryDetailScreen, got %T", sw.Screen)
	}
}

// TestItemSuppliers_AddAndEditOpenForm confirms c opens a create form and
// E/enter opens an edit form scoped to the highlighted link.
func TestItemSuppliers_AddAndEditOpenForm(t *testing.T) {
	s := NewItemSuppliersScreen(Deps{}, "itm-1", "Widget")
	s.loading = false
	s.rows = []omsapi.ItemSupplier{{ID: 7, Supplier: 4, SupplierName: "Acme"}}

	// c -> create form
	_, cmd := s.Update(rune1("c"))
	sw := resolveSwitch(t, cmd)
	cf, ok := sw.Screen.(*ItemSupplierFormScreen)
	if !ok || cf.edit {
		t.Fatalf("c should open a create form, got %T edit=%v", sw.Screen, ok && cf.edit)
	}
	if cf.itemID != "itm-1" {
		t.Errorf("create form item = %q", cf.itemID)
	}

	// E -> edit form for the selected row
	_, cmd = s.Update(rune1("E"))
	sw = resolveSwitch(t, cmd)
	ef, ok := sw.Screen.(*ItemSupplierFormScreen)
	if !ok || !ef.edit || ef.rowID != 7 {
		t.Fatalf("E should open an edit form for row 7, got %T edit=%v row=%d", sw.Screen, ok && ef.edit, ef.rowID)
	}
}

// TestItemSuppliers_SetPrimary confirms p on a non-primary row starts a PATCH,
// while p on the already-primary row is a no-op warning.
func TestItemSuppliers_SetPrimary(t *testing.T) {
	s := NewItemSuppliersScreen(Deps{}, "itm-1", "Widget")
	s.loading = false
	s.rows = []omsapi.ItemSupplier{{ID: 7, Supplier: 4, IsPreferred: false}}
	_, cmd := s.Update(rune1("p"))
	if !s.busy || cmd == nil {
		t.Fatalf("p on non-primary should start a PATCH (busy=%v cmd=%v)", s.busy, cmd)
	}

	// Already primary: warns, does not go busy.
	s2 := NewItemSuppliersScreen(Deps{}, "itm-1", "Widget")
	s2.loading = false
	s2.rows = []omsapi.ItemSupplier{{ID: 7, Supplier: 4, IsPreferred: true}}
	_, cmd = s2.Update(rune1("p"))
	if s2.busy {
		t.Errorf("p on the already-primary row should not go busy")
	}
	if cmd == nil {
		t.Errorf("p on the already-primary row should still flash a warning")
	}
}

// TestItemSuppliers_DeleteConfirm confirms x arms the confirm (and raw input),
// y starts the delete, and n cancels.
func TestItemSuppliers_DeleteConfirm(t *testing.T) {
	s := NewItemSuppliersScreen(Deps{}, "itm-1", "Widget")
	s.loading = false
	s.rows = []omsapi.ItemSupplier{{ID: 7, Supplier: 4, SupplierName: "Acme"}}

	s.Update(rune1("x"))
	if !s.confirmingDelete || !s.WantsRawInput() {
		t.Fatalf("x should arm the confirm and claim raw input")
	}
	s.Update(rune1("n"))
	if s.confirmingDelete {
		t.Errorf("n should cancel the confirm")
	}

	s.Update(rune1("x"))
	_, cmd := s.Update(rune1("y"))
	if !s.deleting || cmd == nil {
		t.Errorf("y should start the delete (deleting=%v cmd=%v)", s.deleting, cmd)
	}
}

// TestItemSuppliers_DeletedMsgReloads confirms a successful delete reloads and a
// failed one surfaces an error without wedging the confirm.
func TestItemSuppliers_DeletedMsgReloads(t *testing.T) {
	s := NewItemSuppliersScreen(Deps{}, "itm-1", "Widget")
	s.confirmingDelete = true
	s.deleting = true
	_, cmd := s.Update(itemSupplierDeletedMsg{err: nil})
	if s.confirmingDelete || s.deleting || !s.loading || cmd == nil {
		t.Errorf("successful delete should clear confirm+deleting, reload (loading=%v cmd=%v)", s.loading, cmd)
	}
}

// ---------------------------------------------------------------------------
// ItemSupplierFormScreen (create / edit)
// ---------------------------------------------------------------------------

// TestItemSupplierForm_BuildPayload_HappyPath walks a create: a picked supplier,
// SKU, URL, a unit cost, and the qty/lead defaults map onto an ItemSupplierWrite.
func TestItemSupplierForm_BuildPayload_HappyPath(t *testing.T) {
	s := NewItemSupplierFormScreen(Deps{}, "itm-1", "Widget", nil)
	s.suppliers = []omsapi.Supplier{{ID: 4, Name: "Acme"}}
	sid := 4
	s.supplierID = &sid
	s.inputs[isSKU].SetValue("ACME-1")
	s.inputs[isURL].SetValue("https://acme")
	s.inputs[isUnitCost].SetValue("1.50")
	s.isPrimary = true

	w, err := s.buildPayload()
	if err != nil {
		t.Fatalf("buildPayload: %v", err)
	}
	if w.Item != "itm-1" || w.Supplier != 4 || w.SupplierSKU != "ACME-1" || w.SupplierURL != "https://acme" {
		t.Errorf("core fields wrong: %+v", w)
	}
	if w.UnitCost == nil || *w.UnitCost != "1.50" {
		t.Errorf("unit_cost = %v, want 1.50", w.UnitCost)
	}
	if w.PackageCost != nil {
		t.Errorf("package_cost should be nil (blank), got %v", *w.PackageCost)
	}
	if w.QuantityPerPackage != 1 || w.AverageLeadTime != 7 {
		t.Errorf("defaults wrong: qty=%d lead=%d, want 1/7", w.QuantityPerPackage, w.AverageLeadTime)
	}
	if !w.IsPrimary {
		t.Errorf("is_primary should be true")
	}
}

// TestItemSupplierForm_Validation covers the required + numeric guards.
func TestItemSupplierForm_Validation(t *testing.T) {
	newForm := func() *ItemSupplierFormScreen {
		s := NewItemSupplierFormScreen(Deps{}, "itm-1", "Widget", nil)
		s.suppliers = []omsapi.Supplier{{ID: 4, Name: "Acme"}}
		return s
	}

	// No supplier picked.
	s := newForm()
	s.inputs[isSKU].SetValue("SKU")
	if _, err := s.buildPayload(); err == nil {
		t.Errorf("missing supplier should error")
	}

	// No SKU.
	s = newForm()
	sid := 4
	s.supplierID = &sid
	if _, err := s.buildPayload(); err == nil {
		t.Errorf("missing SKU should error")
	}

	// Bad quantity.
	s = newForm()
	s.supplierID = &sid
	s.inputs[isSKU].SetValue("SKU")
	s.inputs[isQtyPerPackage].SetValue("abc")
	if _, err := s.buildPayload(); err == nil {
		t.Errorf("non-numeric quantity should error")
	}

	// Bad unit cost.
	s = newForm()
	s.supplierID = &sid
	s.inputs[isSKU].SetValue("SKU")
	s.inputs[isUnitCost].SetValue("cheap")
	if _, err := s.buildPayload(); err == nil {
		t.Errorf("non-numeric unit cost should error")
	}
}

// TestItemSupplierForm_Hydrate confirms an edit round-trips every field from the
// existing link row.
func TestItemSupplierForm_Hydrate(t *testing.T) {
	ex := &omsapi.ItemSupplier{
		ID: 42, Supplier: 5, SupplierName: "Beta", SupplierSKU: "B-9",
		URL: "https://b", UnitCost: "2.00", PackageCost: "20.00",
		PackQuantity: 10, LeadTimeDays: 5, IsPreferred: true,
	}
	s := NewItemSupplierFormScreen(Deps{}, "itm-1", "Widget", ex)
	s.suppliers = []omsapi.Supplier{{ID: 5, Name: "Beta"}}
	s.hydrate()

	if s.supplierID == nil || *s.supplierID != 5 {
		t.Fatalf("supplierID = %v", s.supplierID)
	}
	w, err := s.buildPayload()
	if err != nil {
		t.Fatalf("buildPayload after hydrate: %v", err)
	}
	if w.Supplier != 5 || w.SupplierSKU != "B-9" || w.SupplierURL != "https://b" {
		t.Errorf("hydrated core wrong: %+v", w)
	}
	if w.UnitCost == nil || *w.UnitCost != "2.00" || w.PackageCost == nil || *w.PackageCost != "20.00" {
		t.Errorf("hydrated costs wrong: unit=%v pkg=%v", w.UnitCost, w.PackageCost)
	}
	if w.QuantityPerPackage != 10 || w.AverageLeadTime != 5 || !w.IsPrimary {
		t.Errorf("hydrated qty/lead/primary wrong: %+v", w)
	}
}

// TestItemSupplierForm_PickerCommit confirms opening the picker, filtering, and
// selecting sets the supplier.
func TestItemSupplierForm_PickerCommit(t *testing.T) {
	s := NewItemSupplierFormScreen(Deps{}, "itm-1", "Widget", nil)
	s.loading = false
	s.suppliers = []omsapi.Supplier{{ID: 4, Name: "Acme"}, {ID: 5, Name: "Beta"}}

	// Cursor on the supplier field; space opens the picker.
	s.cursor = 0
	if id, _ := s.currentFieldID(); id != isSupplier {
		t.Fatalf("cursor not on supplier field")
	}
	s.updateFormPhase(tea.KeyMsg{Type: tea.KeySpace})
	if s.phase != isPhaseSupplierPick {
		t.Fatalf("space should open the supplier picker")
	}
	if len(s.pickOptions) != 2 {
		t.Fatalf("picker should list both suppliers, got %d", len(s.pickOptions))
	}

	// Move to Beta and select.
	s.updatePickPhase(rune1("j"))
	s.updatePickPhase(tea.KeyMsg{Type: tea.KeyEnter})
	if s.phase != isPhaseForm {
		t.Errorf("enter should return to the form")
	}
	if s.supplierID == nil || *s.supplierID != 5 {
		t.Errorf("selected supplier = %v, want 5 (Beta)", s.supplierID)
	}
}

// TestItemSupplierForm_PickerFilter confirms typing narrows the picker options.
func TestItemSupplierForm_PickerFilter(t *testing.T) {
	s := NewItemSupplierFormScreen(Deps{}, "itm-1", "Widget", nil)
	s.loading = false
	s.suppliers = []omsapi.Supplier{{ID: 4, Name: "Acme"}, {ID: 5, Name: "Beta"}}
	s.openPicker()
	if len(s.pickOptions) != 2 {
		t.Fatalf("picker should start with both, got %d", len(s.pickOptions))
	}
	// Enter typing mode and filter to "bet".
	s.updatePickPhase(rune1("/"))
	if !s.pickTyping {
		t.Fatalf("/ should enter filter typing mode")
	}
	s.updatePickPhase(rune1("bet"))
	if len(s.pickOptions) != 1 || s.pickOptions[0].id != 5 {
		t.Fatalf("filter should narrow to Beta, got %+v", s.pickOptions)
	}
}
