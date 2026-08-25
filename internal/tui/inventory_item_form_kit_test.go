// The item form's kit-components editor (op-8n0).
//
// The contract these hold it to:
//
//	only a kit gets the row  — an ordinary item's sheet is exactly what it was,
//	                           and "the question went unanswered" is not a kit
//	the components are       — read AND written: the API makes the bill of
//	editable                   materials nested-writable on the kit, so ScanTTY
//	                           edits it rather than showing it read-only
//	the save goes to /kits/  — /items/ 404s for a kit and has no `components`
//	                           field, so the ordinary item PATCH would fail twice
//	an untouched list is not — sending an empty list is a validation ERROR
//	sent                       upstream, not a no-op, so an unopened editor must
//	                           omit the key entirely
//	the bar is honest        — Ctrl-E is named where it works and works where it
//	                           is named, on every phase
//	it fits the floor        — measured on the CLIPPED render at 80, 100 and 120
package tui

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/uid0/scantty/internal/omsapi"
)

// kitFormFixture is the widest realistic kit: a long name, a component whose
// name runs past any affordable column, a note, and a quantity that is not 1.
func kitFormFixture() *omsapi.Kit {
	return &omsapi.Kit{
		Item: omsapi.Item{
			ID: "kit-1", Name: "Eufy printer maintenance kit (CMYK + cleaning)",
			SKU: "EIK-4", IsActive: true, ReorderQuantity: 1,
		},
		IsKit:          true,
		ComponentCount: 2,
		Components: []omsapi.KitComponent{
			{
				ID: 7, Component: "itm-c",
				ComponentName: "Cyan ink cartridge, high yield, for the wide-format printer",
				ComponentSKU:  "CI-100-XL", Quantity: 1, Notes: "CMYK set — do not split",
			},
			{ID: 8, Component: "itm-m", ComponentName: "Magenta ink", ComponentSKU: "MI-100", Quantity: 2},
		},
	}
}

// kitFormSheet is a loaded EDIT-mode item form in whichever kit state is being
// measured — the state the form reaches once BOTH the item and the kit answer
// have arrived.
func kitFormSheet(t *testing.T, kit *omsapi.Kit, width int) *InventoryItemFormScreen {
	t.Helper()
	s := NewInventoryItemFormScreen(Deps{}, "kit-1")
	s.loading = false
	s.categories = []omsapi.Category{{ID: 1, Name: "Consumables"}}
	s.locations = []omsapi.Location{{ID: 2, Name: "Print room"}}
	s.item = &omsapi.Item{ID: "kit-1", Name: "Eufy Ink Kit", SKU: "EIK-4", IsActive: true, ReorderQuantity: 1}
	if kit != nil {
		s.item = &kit.Item
	}
	s.kit = kit
	s.hydrate()
	s.hydrateKit()
	s.rebuildFields()
	s.syncFocus()
	s.snapshotBaseline()
	s.Update(tea.WindowSizeMsg{Width: width, Height: jdeSweepHeight})
	return s
}

// kitFormCursorTo puts the sheet's cursor on a field id, failing if it is not on
// the sheet at all.
func kitFormCursorTo(t *testing.T, s *InventoryItemFormScreen, id int) {
	t.Helper()
	for i, fid := range s.fields {
		if fid == id {
			s.cursor = i
			s.syncFocus()
			return
		}
	}
	t.Fatalf("field %d is not on the sheet", id)
}

func kitFormHasField(s *InventoryItemFormScreen, id int) bool {
	for _, fid := range s.fields {
		if fid == id {
			return true
		}
	}
	return false
}

// TestItemFormKit_OnlyAKitGetsTheComponentsRow is acceptance criterion 4 on this
// screen: an ordinary item's sheet is untouched. It also covers the third state
// — the /kits/ question failed — which must behave like an ordinary item rather
// than offering an editor whose save has nowhere to go.
func TestItemFormKit_OnlyAKitGetsTheComponentsRow(t *testing.T) {
	if kitFormHasField(kitFormSheet(t, nil, 120), fKitComponents) {
		t.Error("an ordinary item's sheet grew a kit-components row")
	}
	s := kitFormSheet(t, kitFormFixture(), 120)
	if !kitFormHasField(s, fKitComponents) {
		t.Fatal("a kit's sheet has no kit-components row")
	}
	if !strings.Contains(s.viewForm(), "Kit components") {
		t.Errorf("the row is not rendered:\n%s", s.viewForm())
	}
}

// TestItemFormKit_TheRowSummarisesTheBillOfMaterials. The summary row is what an
// operator sees without opening anything, so it has to carry the quantities —
// "2× Magenta ink" is the fact, "2 components" alone is not.
func TestItemFormKit_TheRowSummarisesTheBillOfMaterials(t *testing.T) {
	s := kitFormSheet(t, kitFormFixture(), 120)
	value, empty := s.kitFieldValue()
	if empty {
		t.Fatalf("a kit with two components read as empty: %q", value)
	}
	for _, want := range []string{"1×", "2×", "Magenta ink", "2 components"} {
		if !strings.Contains(value, want) {
			t.Errorf("summary %q is missing %q", value, want)
		}
	}
}

// TestItemFormKit_TheStockRowSaysAKitCarriesNone. The serializer refuses to save
// a kit with stock, so the row says so where the operator is standing rather
// than letting them type a number the save will reject.
func TestItemFormKit_TheStockRowSaysAKitCarriesNone(t *testing.T) {
	kitSheet := kitFormSheet(t, kitFormFixture(), 120)
	if hint := kitSheet.fieldHint(fCurrentStock); !strings.Contains(hint, "no stock") {
		t.Errorf("a kit's Current stock hint = %q", hint)
	}
	plain := kitFormSheet(t, nil, 120)
	if hint := plain.fieldHint(fCurrentStock); strings.Contains(hint, "no stock") {
		t.Errorf("an ordinary item's Current stock hint changed: %q", hint)
	}
}

// TestItemFormKit_CtrlEOpensTheEditorAndTheBarSaysSo is the bar-honesty half of
// the JDE contract: the key that works on this row is named on the bar, and the
// key named on the bar does what it says.
func TestItemFormKit_CtrlEOpensTheEditorAndTheBarSaysSo(t *testing.T) {
	s := kitFormSheet(t, kitFormFixture(), 120)
	kitFormCursorTo(t, s, fKitComponents)

	named := false
	for _, item := range s.formBar(s.formLines()) {
		if item.Key == "Ctrl-E" && strings.Contains(item.Label, "component") {
			named = true
		}
	}
	if !named {
		t.Errorf("the bar does not name Ctrl-E on the components row: %+v", s.formBar(s.formLines()))
	}

	s.Update(tea.KeyMsg{Type: tea.KeyCtrlE})
	if s.phase != itemFormPhaseKit {
		t.Fatalf("Ctrl-E did not open the components list (phase %d)", s.phase)
	}
	out := s.View()
	for _, want := range []string{"Kit components", "Per kit", "CI-100-XL", "(add a component)"} {
		if !strings.Contains(out, want) {
			t.Errorf("the components list is missing %q:\n%s", want, out)
		}
	}
}

// TestItemFormKit_EditingAComponentChangesItsQuantity walks the whole gesture an
// operator makes: open the list, open a row, retype the quantity, save the row.
func TestItemFormKit_EditingAComponentChangesItsQuantity(t *testing.T) {
	s := kitFormSheet(t, kitFormFixture(), 120)
	kitFormCursorTo(t, s, fKitComponents)
	s.Update(tea.KeyMsg{Type: tea.KeyCtrlE}) // list
	s.kitCursor = 1                          // the magenta row, quantity 2
	s.Update(tea.KeyMsg{Type: tea.KeyCtrlE}) // its editor

	if s.phase != itemFormPhaseKitRow {
		t.Fatalf("Ctrl-E on a component did not open its editor (phase %d)", s.phase)
	}
	if out := s.View(); !strings.Contains(out, "Magenta ink") || !strings.Contains(out, "Per kit") {
		t.Errorf("the component editor does not name what it edits:\n%s", out)
	}

	s.kitRowQty.SetValue("")
	s.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("5")})
	s.Update(tea.KeyMsg{Type: tea.KeyEnter})

	if s.phase != itemFormPhaseKit {
		t.Fatalf("Enter did not return to the list (phase %d)", s.phase)
	}
	if s.kitRows[1].quantity != 5 {
		t.Errorf("quantity = %d, want 5", s.kitRows[1].quantity)
	}
	// And the sheet now knows it has unsaved edits, which is what stops the
	// suppliers door walking off with them.
	if !s.dirty() {
		t.Error("editing the bill of materials did not make the sheet dirty")
	}
}

// TestItemFormKit_AQuantityBelowOneIsRefusedBesideTheRow. The serializer's own
// floor — a component quantity of zero would credit nothing on receipt — checked
// where the operator can see the row it is about, not at the end of a save.
func TestItemFormKit_AQuantityBelowOneIsRefusedBesideTheRow(t *testing.T) {
	s := kitFormSheet(t, kitFormFixture(), 120)
	kitFormCursorTo(t, s, fKitComponents)
	s.Update(tea.KeyMsg{Type: tea.KeyCtrlE})
	s.Update(tea.KeyMsg{Type: tea.KeyCtrlE})

	s.kitRowQty.SetValue("0")
	s.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if s.phase != itemFormPhaseKitRow {
		t.Fatal("a zero quantity was accepted")
	}
	if !strings.Contains(s.View(), "at least 1") {
		t.Errorf("no reason was given:\n%s", s.View())
	}
	if s.kitRows[0].quantity != 1 {
		t.Errorf("the row was mutated anyway: %d", s.kitRows[0].quantity)
	}
}

// TestItemFormKit_RemovingAComponentDropsIt. Removal lives on the component's
// own editor, which is the only place the operator can see what they are about
// to drop — the reduced key scheme has no letter left to hide a delete behind.
func TestItemFormKit_RemovingAComponentDropsIt(t *testing.T) {
	s := kitFormSheet(t, kitFormFixture(), 120)
	kitFormCursorTo(t, s, fKitComponents)
	s.Update(tea.KeyMsg{Type: tea.KeyCtrlE})
	s.Update(tea.KeyMsg{Type: tea.KeyCtrlE})

	s.kitRowFocus = kitRowFieldRemove
	s.syncKitRowFocus()
	named := false
	for _, item := range []actionBarItem{{"Ctrl-E", "Remove"}} {
		if strings.Contains(s.View(), item.Label) {
			named = true
		}
	}
	if !named {
		t.Errorf("the remove row does not name its key:\n%s", s.View())
	}
	s.Update(tea.KeyMsg{Type: tea.KeyCtrlE})

	if len(s.kitRows) != 1 || s.kitRows[0].component != "itm-m" {
		t.Fatalf("remove left %+v", s.kitRows)
	}
	if s.phase != itemFormPhaseKit {
		t.Errorf("remove did not return to the list (phase %d)", s.phase)
	}
}

// TestItemFormKit_AddingPicksAnItemAndLandsOnItsQuantity. Adding is a ROW you
// navigate to, and the picker's commit drops the operator on the quantity —
// which is the number the row exists for and the one "1" is only a guess at.
func TestItemFormKit_AddingPicksAnItemAndLandsOnItsQuantity(t *testing.T) {
	s := kitFormSheet(t, kitFormFixture(), 120)
	s.kitItems = []omsapi.Item{
		{ID: "itm-y", Name: "Yellow ink", SKU: "YI-100", Stock: 9},
		{ID: "itm-m", Name: "Magenta ink", SKU: "MI-100"},            // already listed
		{ID: "kit-1", Name: "Eufy printer maintenance kit"},          // the kit itself
		{ID: "itm-s", Name: "Serialized widget", IsSerialized: true}, // cannot be a component
	}
	kitFormCursorTo(t, s, fKitComponents)
	s.Update(tea.KeyMsg{Type: tea.KeyCtrlE}) // list
	s.kitCursor = s.kitAddRow()
	s.Update(tea.KeyMsg{Type: tea.KeyCtrlE}) // picker

	if s.phase != itemFormPhaseKitPick {
		t.Fatalf("the add row did not open a picker (phase %d)", s.phase)
	}
	// A component already on the list, and the kit itself, are not choices — the
	// serializer rejects both, so they are not offered at all.
	for _, opt := range s.kitPickOptions {
		if opt.item.ID == "itm-m" || opt.item.ID == "kit-1" {
			t.Errorf("the picker offered %q, which cannot be added", opt.item.Name)
		}
	}
	// A serialized item IS offered, dimmed, with the reason — "why can't I add
	// this?" is a question the screen has to answer.
	var serialized *kitPickOption
	for i := range s.kitPickOptions {
		if s.kitPickOptions[i].item.ID == "itm-s" {
			serialized = &s.kitPickOptions[i]
		}
	}
	if serialized == nil || serialized.why == "" {
		t.Fatalf("the serialized item is not shown as unpickable: %+v", s.kitPickOptions)
	}

	// Picking it does not commit, and says why.
	for i, opt := range s.kitPickOptions {
		if opt.item.ID == "itm-s" {
			s.kitPickCursor = i
		}
	}
	s.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if len(s.kitRows) != 2 {
		t.Fatalf("an illegal component was added: %+v", s.kitRows)
	}
	if !strings.Contains(s.View(), "cannot be a kit component") {
		t.Errorf("no reason was given for the refusal:\n%s", s.View())
	}

	// Picking a legal one adds it at quantity 1 and opens its editor.
	for i, opt := range s.kitPickOptions {
		if opt.item.ID == "itm-y" {
			s.kitPickCursor = i
		}
	}
	s.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if len(s.kitRows) != 3 || s.kitRows[2].component != "itm-y" || s.kitRows[2].quantity != 1 {
		t.Fatalf("add left %+v", s.kitRows)
	}
	if s.phase != itemFormPhaseKitRow {
		t.Errorf("adding did not land on the new component's quantity (phase %d)", s.phase)
	}
}

// TestItemFormKit_AnUntouchedListIsNotSent. The empty list is a validation ERROR
// upstream, not a no-op, so a save that never opened the editor must omit the
// key — and one that DID must send exactly what is on screen.
func TestItemFormKit_AnUntouchedListIsNotSent(t *testing.T) {
	s := kitFormSheet(t, kitFormFixture(), 120)
	if got := s.kitComponentsPayload(); got != nil {
		t.Errorf("an untouched bill of materials would be sent: %+v", *got)
	}
	// A non-kit sheet never sends it either, whatever else it does.
	if got := kitFormSheet(t, nil, 120).kitComponentsPayload(); got != nil {
		t.Errorf("an ordinary item's save carried components: %+v", *got)
	}

	s.kitRows[0].quantity = 4
	got := s.kitComponentsPayload()
	if got == nil || len(*got) != 2 {
		t.Fatalf("an edited bill of materials was not sent: %v", got)
	}
	if (*got)[0].Component != "itm-c" || (*got)[0].Quantity != 4 || (*got)[0].Notes != "CMYK set — do not split" {
		t.Errorf("payload row 1 = %+v", (*got)[0])
	}
}

// ---------------------------------------------------------------------------
// The save
// ---------------------------------------------------------------------------

// kitFormServer records every write so a test can prove which endpoint the save
// reached — which is the whole question here, since /items/ 404s for a kit.
type kitFormServer struct {
	mu     sync.Mutex
	writes []string
	bodies []map[string]any
}

func (f *kitFormServer) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPatch || r.Method == http.MethodPost {
			body := map[string]any{}
			if raw, _ := io.ReadAll(r.Body); len(raw) > 0 {
				_ = json.Unmarshal(raw, &body)
			}
			f.writes = append(f.writes, r.Method+" "+r.URL.Path)
			f.bodies = append(f.bodies, body)
			_, _ = w.Write([]byte(`{"id":"kit-1","name":"Eufy Ink Kit","is_kit":true}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"detail":"not found"}`))
	}
}

// TestItemFormKit_SavesThroughTheKitsEndpoint. Two independent reasons this must
// not go to /items/: that queryset excludes kits, so the PATCH is a 404; and
// `components` is not a field on the item serializer, so the bill of materials
// would be silently dropped even if it were not.
func TestItemFormKit_SavesThroughTheKitsEndpoint(t *testing.T) {
	fake := &kitFormServer{}
	srv := httptest.NewServer(fake.handler())
	defer srv.Close()

	s := kitFormSheet(t, kitFormFixture(), 120)
	s.deps = Deps{OMS: omsapi.New(srv.URL), Ctx: context.Background()}
	s.kitRows[0].quantity = 4

	_, cmd := s.submit()
	if cmd == nil {
		t.Fatal("submit produced no command")
	}
	if msg, ok := cmd().(itemFormSavedMsg); !ok || msg.err != nil {
		t.Fatalf("save failed: %+v", msg)
	}

	fake.mu.Lock()
	defer fake.mu.Unlock()
	if len(fake.writes) != 1 || fake.writes[0] != "PATCH /api/inventory/kits/kit-1/" {
		t.Fatalf("writes = %v, want a single PATCH to /kits/", fake.writes)
	}
	body := fake.bodies[0]
	if body["name"] == nil {
		t.Errorf("the item fields did not ride along: %v", body)
	}
	rows, ok := body["components"].([]any)
	if !ok || len(rows) != 2 {
		t.Fatalf("components = %v", body["components"])
	}
	if rows[0].(map[string]any)["quantity"].(float64) != 4 {
		t.Errorf("the edited quantity did not reach the wire: %v", rows[0])
	}
}

// TestItemFormKit_AnOrdinaryItemStillSavesThroughItems. The routing above must
// not have moved every item onto the kit endpoint.
func TestItemFormKit_AnOrdinaryItemStillSavesThroughItems(t *testing.T) {
	fake := &kitFormServer{}
	srv := httptest.NewServer(fake.handler())
	defer srv.Close()

	s := kitFormSheet(t, nil, 120)
	s.deps = Deps{OMS: omsapi.New(srv.URL), Ctx: context.Background()}
	_, cmd := s.submit()
	if cmd == nil {
		t.Fatal("submit produced no command")
	}
	cmd()

	fake.mu.Lock()
	defer fake.mu.Unlock()
	if len(fake.writes) != 1 || fake.writes[0] != "PATCH /api/inventory/items/kit-1/" {
		t.Fatalf("writes = %v, want a single PATCH to /items/", fake.writes)
	}
	if _, present := fake.bodies[0]["components"]; present {
		t.Errorf("an ordinary item's save carried components: %v", fake.bodies[0])
	}
}

// TestItemFormKit_TheKitAnswerGatesTheSheet. The sheet must not finish loading —
// and so must not render a bill-of-materials row it has not got — until the
// /kits/ question has been answered one way or the other.
func TestItemFormKit_TheKitAnswerGatesTheSheet(t *testing.T) {
	s := NewInventoryItemFormScreen(Deps{}, "kit-1")
	s.Update(itemFormRefLoadedMsg{})
	s.Update(itemFormItemLoadedMsg{item: &omsapi.Item{ID: "kit-1", Name: "Eufy Ink Kit"}})
	if !s.loading {
		t.Fatal("the sheet finished loading before the kit answer arrived")
	}
	s.Update(itemFormKitLoadedMsg{kit: kitFormFixture()})
	if s.loading {
		t.Fatal("the sheet is still loading after every answer arrived")
	}
	if !kitFormHasField(s, fKitComponents) {
		t.Error("the components row did not appear once the kit answer arrived")
	}
}

// ---------------------------------------------------------------------------
// Width
// ---------------------------------------------------------------------------

// TestItemFormKit_EveryPhaseSurvivesTheClip is acceptance criterion 3 for this
// screen, asserted on the CLIPPED render: Root hands every screen through
// clampToBox, which truncates an over-wide row with nothing to show it did.
//
// The measurement is deliberately narrowed to the lines this bead OWNS. At 80
// columns the item sheet already loses content that predates kits entirely —
// four field rows overrun and so does the action bar on every columnar screen —
// which is filed as sc-xxpa; what a kit contributes has to fit the floor
// regardless.
func TestItemFormKit_EveryPhaseSurvivesTheClip(t *testing.T) {
	for _, width := range kitTestWidths {
		budget := screenBodyWidth(width)
		check := func(label string, lines []string) {
			for _, line := range lines {
				if w := lipgloss.Width(line); w > budget {
					t.Errorf("%s at %d columns: a line is %d wide but the pane is %d — it is clipped: %q",
						label, width, w, budget, line)
				}
			}
		}

		// The summary row on the sheet.
		s := kitFormSheet(t, kitFormFixture(), width)
		kitFormCursorTo(t, s, fKitComponents)
		fields := s.formFields()
		labelWidth := jdeLabelWidth(fields)
		for i, id := range s.fields {
			if id == fKitComponents {
				check("the components row", []string{renderJDEField(fields[i], labelWidth, 0)})
			}
		}

		// The list.
		s.Update(tea.KeyMsg{Type: tea.KeyCtrlE})
		check("the components list", kitFormPhaseLines(t, s))

		// One component's editor.
		s.Update(tea.KeyMsg{Type: tea.KeyCtrlE})
		check("the component editor", kitFormPhaseLines(t, s))
		s.kitRowFocus = kitRowFieldRemove
		s.syncKitRowFocus()
		check("the component editor's remove row", kitFormPhaseLines(t, s))

		// The picker's OPTION rows. Its shared header (the always-live filter box
		// jde_form.go draws for every picker in the app) is 70 columns wide and
		// overruns the 80-column floor on every picker there has ever been — that
		// is sc-xxpa, not this bead, and narrowing it here would change every
		// screen in the app. What this picker CONTRIBUTES has to fit regardless.
		s.Update(tea.KeyMsg{Type: tea.KeyEsc})
		s.kitCursor = s.kitAddRow()
		s.kitItems = []omsapi.Item{
			{ID: "itm-y", Name: "Yellow ink cartridge, high yield, wide-format", SKU: "YI-100-XL", Stock: 9},
			{ID: "itm-s", Name: "Serialized calibration widget assembly", IsSerialized: true},
		}
		s.Update(tea.KeyMsg{Type: tea.KeyCtrlE})
		_, pickBody := s.kitPickView()
		check("the component picker", pickBody.text)

		// And an EMPTY kit, whose warning is the longest line either phase draws.
		empty := kitFormSheet(t, &omsapi.Kit{Item: omsapi.Item{ID: "kit-0", Name: "Empty kit"}, IsKit: true}, width)
		kitFormCursorTo(t, empty, fKitComponents)
		empty.Update(tea.KeyMsg{Type: tea.KeyCtrlE})
		check("an empty kit's list", kitFormPhaseLines(t, empty))
	}
}

// kitFormPhaseLines is the current phase's render, minus the action bar — which
// overruns 80 columns on every columnar screen in the app and is filed as
// sc-xxpa, so measuring it here would only re-report a defect this bead did not
// introduce and cannot fix from one screen.
func kitFormPhaseLines(t *testing.T, s *InventoryItemFormScreen) []string {
	t.Helper()
	lines := strings.Split(s.View(), "\n")
	if n := len(lines); n > actionBarRows {
		lines = lines[:n-actionBarRows]
	}
	return lines
}

// kitFormBigKit is a bill of materials taller than any pane: paging only exists
// for a list that outgrows its window, so a fixture that fits would prove
// nothing about the key the bar names.
func kitFormBigKit(n int) *omsapi.Kit {
	kit := &omsapi.Kit{
		Item:  omsapi.Item{ID: "kit-1", Name: "Bulk consumables kit", SKU: "BCK-1", IsActive: true, ReorderQuantity: 1},
		IsKit: true, ComponentCount: n,
	}
	for i := 0; i < n; i++ {
		kit.Components = append(kit.Components, omsapi.KitComponent{
			ID:            i + 1,
			Component:     "itm-" + strconv.Itoa(i),
			ComponentName: "Component " + strconv.Itoa(i),
			ComponentSKU:  "C-" + strconv.Itoa(i),
			Quantity:      1,
		})
	}
	return kit
}

// TestItemFormKit_TheListPagesWhenItSaysItDoes is the invariant AGENTS.md states
// outright: a key the bar names must DO something. kitListBar advertises
// PgUp/PgDn the moment the component list outgrows the pane, and the phase used
// to drop both keys on the floor — so an operator with a long bill of materials
// read an offer the screen did not honour.
func TestItemFormKit_TheListPagesWhenItSaysItDoes(t *testing.T) {
	s := kitFormSheet(t, kitFormBigKit(40), 110)
	s.Update(tea.WindowSizeMsg{Width: 110, Height: 20})
	kitFormCursorTo(t, s, fKitComponents)
	s.Update(tea.KeyMsg{Type: tea.KeyCtrlE})
	if s.phase != itemFormPhaseKit {
		t.Fatalf("Ctrl-E did not open the components list (phase %d)", s.phase)
	}

	named := false
	for _, item := range s.kitListBar(s.kitListLines()) {
		if item.Key == "PgUp/PgDn" {
			named = true
		}
	}
	if !named {
		t.Fatalf("a list taller than the pane does not advertise paging: %+v", s.kitListBar(s.kitListLines()))
	}

	s.Update(tea.KeyMsg{Type: tea.KeyPgDown})
	if s.kitCursor == 0 {
		t.Fatal("PgDn is named on the bar but moved nothing")
	}
	// A page is more than a row — otherwise it is just Down wearing another name.
	if s.kitCursor < 2 {
		t.Errorf("PgDn moved %d row(s), which is not a page", s.kitCursor)
	}
	// It CLAMPS rather than wrapping: overshooting stops at the trailing add row,
	// which is the last thing the cursor can stand on.
	for i := 0; i < 40; i++ {
		s.Update(tea.KeyMsg{Type: tea.KeyPgDown})
	}
	if s.kitCursor != s.kitAddRow() {
		t.Errorf("PgDn clamped to %d, want the add row %d", s.kitCursor, s.kitAddRow())
	}
	for i := 0; i < 40; i++ {
		s.Update(tea.KeyMsg{Type: tea.KeyPgUp})
	}
	if s.kitCursor != 0 {
		t.Errorf("PgUp clamped to %d, want the first row", s.kitCursor)
	}
}

// TestItemFormKit_AShortListDoesNotOfferPaging is the other half of the same
// convention: the bar must not name a key that has nothing to do.
func TestItemFormKit_AShortListDoesNotOfferPaging(t *testing.T) {
	s := kitFormSheet(t, kitFormFixture(), 110)
	kitFormCursorTo(t, s, fKitComponents)
	s.Update(tea.KeyMsg{Type: tea.KeyCtrlE})
	for _, item := range s.kitListBar(s.kitListLines()) {
		if item.Key == "PgUp/PgDn" {
			t.Errorf("a two-row list that fits the pane offers paging: %+v", s.kitListBar(s.kitListLines()))
		}
	}
}

// TestItemFormKit_ARefusalDiesWithTheOptionsItWasAbout. The refusal is about one
// option — "that item", the row the cursor was on — so it cannot outlive the
// list that option was in: left standing over a rebuilt picker it reads as a
// refusal of whatever is now under the cursor.
func TestItemFormKit_ARefusalDiesWithTheOptionsItWasAbout(t *testing.T) {
	s := kitFormSheet(t, kitFormFixture(), 120)
	s.kitItems = []omsapi.Item{
		{ID: "itm-y", Name: "Yellow ink", SKU: "YI-100"},
		{ID: "itm-s", Name: "Serialized widget", IsSerialized: true},
	}
	kitFormCursorTo(t, s, fKitComponents)
	s.Update(tea.KeyMsg{Type: tea.KeyCtrlE}) // list
	s.kitCursor = s.kitAddRow()
	s.Update(tea.KeyMsg{Type: tea.KeyCtrlE}) // picker

	for i, opt := range s.kitPickOptions {
		if opt.item.ID == "itm-s" {
			s.kitPickCursor = i
		}
	}
	s.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if !strings.Contains(s.View(), "cannot be a kit component") {
		t.Fatalf("the refusal was not shown in the first place:\n%s", s.View())
	}

	// Typing a filter rebuilds the option list, so the message about the old one
	// goes with it.
	s.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	if strings.Contains(s.View(), "cannot be a kit component") {
		t.Errorf("a stale refusal survived a filter change:\n%s", s.View())
	}

	// And so does leaving the picker and opening it again.
	for i, opt := range s.kitPickOptions {
		if opt.item.ID == "itm-s" {
			s.kitPickCursor = i
		}
	}
	s.pickSearch.SetValue("")
	s.applyKitPickFilter()
	for i, opt := range s.kitPickOptions {
		if opt.item.ID == "itm-s" {
			s.kitPickCursor = i
		}
	}
	s.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if !strings.Contains(s.View(), "cannot be a kit component") {
		t.Fatalf("the refusal did not come back for a second attempt:\n%s", s.View())
	}
	s.Update(tea.KeyMsg{Type: tea.KeyEsc}) // back to the list
	if s.phase != itemFormPhaseKit {
		t.Fatalf("Esc did not leave the picker (phase %d)", s.phase)
	}
	s.kitCursor = s.kitAddRow()
	s.Update(tea.KeyMsg{Type: tea.KeyCtrlE}) // reopen
	if strings.Contains(s.View(), "cannot be a kit component") {
		t.Errorf("a stale refusal was still on screen over a freshly opened picker:\n%s", s.View())
	}
}

// TestItemFormKit_ARefusalDiesWhenTheCursorLeavesItsRow. The refusal names
// neither the item nor the reason — it says "that item", and "that item" is
// whichever row the cursor is on. So moving off the refused row has to take the
// message with it: left standing, the status line asserts that the row now
// highlighted cannot be a component when it can, and pressing Enter then
// silently succeeds underneath a refusal that contradicts it.
//
// Every movement key is driven, because the clear has to hold for the movement
// paths as a class rather than for the one that was reported.
func TestItemFormKit_ARefusalDiesWhenTheCursorLeavesItsRow(t *testing.T) {
	for _, move := range []struct {
		name string
		key  tea.KeyMsg
	}{
		{"down", tea.KeyMsg{Type: tea.KeyDown}},
		{"tab", tea.KeyMsg{Type: tea.KeyTab}},
		{"up", tea.KeyMsg{Type: tea.KeyUp}},
		{"shift+tab", tea.KeyMsg{Type: tea.KeyShiftTab}},
		{"pgdown", tea.KeyMsg{Type: tea.KeyPgDown}},
		{"pgup", tea.KeyMsg{Type: tea.KeyPgUp}},
	} {
		t.Run(move.name, func(t *testing.T) {
			s := kitFormSheet(t, kitFormFixture(), 120)
			s.kitItems = []omsapi.Item{
				{ID: "itm-s", Name: "Serialized widget", IsSerialized: true},
				{ID: "itm-y", Name: "Yellow ink", SKU: "YI-100"},
			}
			kitFormCursorTo(t, s, fKitComponents)
			s.Update(tea.KeyMsg{Type: tea.KeyCtrlE}) // list
			s.kitCursor = s.kitAddRow()
			s.Update(tea.KeyMsg{Type: tea.KeyCtrlE}) // picker

			for i, opt := range s.kitPickOptions {
				if opt.item.ID == "itm-s" {
					s.kitPickCursor = i
				}
			}
			s.Update(tea.KeyMsg{Type: tea.KeyEnter})
			if !strings.Contains(s.View(), "cannot be a kit component") {
				t.Fatalf("the refusal was not shown in the first place:\n%s", s.View())
			}

			s.Update(move.key)
			if strings.Contains(s.View(), "cannot be a kit component") {
				t.Errorf("a refusal about the old row survived %s, and now describes the one under the cursor:\n%s",
					move.name, s.View())
			}
			// The options themselves are untouched — the message died, not the
			// picker — so the pickable row still commits.
			for i, opt := range s.kitPickOptions {
				if opt.item.ID == "itm-y" {
					s.kitPickCursor = i
				}
			}
			before := len(s.kitRows)
			s.Update(tea.KeyMsg{Type: tea.KeyEnter})
			if len(s.kitRows) != before+1 {
				t.Errorf("the pickable row no longer commits after %s: %+v", move.name, s.kitRows)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// A kit's Current stock row
// ---------------------------------------------------------------------------

// kitFormStockedKit is a kit that already carries stock. It should not exist —
// the model forbids it — but InventoryItem.save() never runs full_clean(), so a
// stray figure can be sitting there, and that is precisely the case the save is
// about to overwrite.
func kitFormStockedKit(stock int) *omsapi.Kit {
	kit := kitFormFixture()
	kit.Stock = stock
	return kit
}

// TestItemFormKit_ANonZeroKitStockSaysWhatSavingDoesToIt. Sending current_stock:
// 0 writes over a number that is really there in shared data. Zeroing a value a
// kit was never allowed to hold is defensible; doing it invisibly as a side
// effect of renaming the kit is a silent overwrite, so the sheet shows both the
// figure and its fate before anything is pressed.
func TestItemFormKit_ANonZeroKitStockSaysWhatSavingDoesToIt(t *testing.T) {
	for _, width := range kitTestWidths {
		s := kitFormSheet(t, kitFormStockedKit(7), width)
		body := strings.Join(s.formLines().text, "\n")
		// The note WRAPS at a narrow pane, so the sentence is read off the
		// collapsed text rather than off one line — wrapping is the point.
		flat := strings.Join(strings.Fields(body), " ")
		// Both halves: what is recorded now, and what saving does to it.
		if !strings.Contains(flat, "Recorded as 7 on hand") {
			t.Errorf("at %d columns the sheet hides the recorded figure:\n%s", width, body)
		}
		for _, want := range []string{"holds no stock of its own", "saving this sheet clears that to 0"} {
			if !strings.Contains(flat, want) {
				t.Errorf("at %d columns the sheet does not say %q:\n%s", width, want, body)
			}
		}
		// Shown WITHOUT the cursor ever going near the row — an operator renaming
		// a kit has no reason to visit a row they cannot type into.
		if id, ok := s.currentFieldID(); ok && id == fCurrentStock {
			t.Fatal("the fixture starts on the stock row, which defeats the point of the check")
		}
		// And it WRAPS rather than clipping: this is the sentence a 51-column
		// pane would otherwise cut in half.
		budget := screenBodyWidth(width)
		for _, line := range s.formLines().text {
			if !strings.Contains(line, "!") && !strings.Contains(line, "Recorded as") &&
				!strings.Contains(line, "stock of its own, so") {
				continue
			}
			if w := lipgloss.Width(line); w > budget {
				t.Errorf("at %d columns the warning is %d wide against a %d pane: %q", width, w, budget, line)
			}
		}
	}
}

// TestItemFormKit_AZeroStockKitGetsNoNotice. The normal case must stay quiet:
// there is nothing to overwrite, so there is nothing to warn about.
func TestItemFormKit_AZeroStockKitGetsNoNotice(t *testing.T) {
	s := kitFormSheet(t, kitFormFixture(), 120)
	if body := strings.Join(s.formLines().text, "\n"); strings.Contains(body, "Recorded as") {
		t.Errorf("a kit with no stock was warned about losing it:\n%s", body)
	}
	// Nor does an ordinary item that DOES carry stock.
	plain := kitFormSheet(t, nil, 120)
	plain.inputs[fCurrentStock].SetValue("12")
	if body := strings.Join(plain.formLines().text, "\n"); strings.Contains(body, "Recorded as") {
		t.Errorf("an ordinary item was warned about losing its stock:\n%s", body)
	}
}

// TestItemFormKit_TheStockRowIsReadOnlyForAKit. A row that renders read-only has
// to BE read-only: accepting the keystroke anyway would edit a value the save
// overwrites regardless, and would make the sheet dirty() for a change that can
// never land — which is what the suppliers door then asks the operator about.
func TestItemFormKit_TheStockRowIsReadOnlyForAKit(t *testing.T) {
	s := kitFormSheet(t, kitFormStockedKit(7), 120)
	kitFormCursorTo(t, s, fCurrentStock)
	before := s.inputs[fCurrentStock].Value()

	s.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("9")})
	if got := s.inputs[fCurrentStock].Value(); got != before {
		t.Errorf("a kit's Current stock accepted typing: %q -> %q", before, got)
	}
	if s.dirty() {
		t.Error("typing into a read-only row made the sheet dirty")
	}
	// The bar must not name a key that does nothing where the cursor stands. A
	// number row never offered one, so the invariant is that none appeared.
	for _, item := range s.formBar(s.formLines()) {
		if item.Key == "Ctrl-E" || item.Key == "←→" {
			t.Errorf("the bar offers %q on a read-only row: %+v", item.Key, item)
		}
	}
}

// TestItemFormKit_AnOrdinaryItemsStockRowStillEdits is acceptance criterion 4 on
// this row: only a kit's is frozen.
func TestItemFormKit_AnOrdinaryItemsStockRowStillEdits(t *testing.T) {
	fake := &kitFormServer{}
	srv := httptest.NewServer(fake.handler())
	defer srv.Close()

	s := kitFormSheet(t, nil, 120)
	s.deps = Deps{OMS: omsapi.New(srv.URL), Ctx: context.Background()}
	kitFormCursorTo(t, s, fCurrentStock)
	s.inputs[fCurrentStock].SetValue("")
	s.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("9")})
	if got := s.inputs[fCurrentStock].Value(); got != "9" {
		t.Fatalf("an ordinary item's Current stock stopped accepting typing: %q", got)
	}
	if !s.dirty() {
		t.Error("editing an ordinary item's stock did not make the sheet dirty")
	}

	_, cmd := s.submit()
	if cmd == nil {
		t.Fatal("submit produced no command")
	}
	cmd()
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if len(fake.bodies) != 1 {
		t.Fatalf("writes = %v", fake.writes)
	}
	if got := fake.bodies[0]["current_stock"]; got != float64(9) {
		t.Errorf("current_stock = %v, want the typed 9", got)
	}
}

// TestItemFormKit_TheKitSaveAssertsZeroStock. current_stock has no omitempty so
// it rides every save; omitting it cannot help, because KitSerializer validates
// an absent key against the STORED value and a kit with stray stock would then
// be unsaveable from ScanTTY for good. So the sheet asserts the only value a kit
// may hold.
func TestItemFormKit_TheKitSaveAssertsZeroStock(t *testing.T) {
	fake := &kitFormServer{}
	srv := httptest.NewServer(fake.handler())
	defer srv.Close()

	s := kitFormSheet(t, kitFormStockedKit(7), 120)
	s.deps = Deps{OMS: omsapi.New(srv.URL), Ctx: context.Background()}
	_, cmd := s.submit()
	if cmd == nil {
		t.Fatal("submit produced no command")
	}
	if msg, ok := cmd().(itemFormSavedMsg); !ok || msg.err != nil {
		t.Fatalf("save failed: %+v", msg)
	}

	fake.mu.Lock()
	defer fake.mu.Unlock()
	if len(fake.bodies) != 1 || fake.writes[0] != "PATCH /api/inventory/kits/kit-1/" {
		t.Fatalf("writes = %v", fake.writes)
	}
	got, present := fake.bodies[0]["current_stock"]
	if !present {
		t.Fatalf("current_stock was omitted, which leaves a stocked kit unsaveable: %v", fake.bodies[0])
	}
	if got != float64(0) {
		t.Errorf("current_stock = %v, want 0 — a kit may hold no other value", got)
	}
}

// ---------------------------------------------------------------------------
// An unanswered "is this a kit?"
// ---------------------------------------------------------------------------

// kitFormUnanswered is a loaded sheet whose /kits/ question failed with
// something that is NOT the 404 meaning "ordinary item".
func kitFormUnanswered(t *testing.T, width int) *InventoryItemFormScreen {
	t.Helper()
	s := NewInventoryItemFormScreen(Deps{}, "kit-1")
	s.Update(tea.WindowSizeMsg{Width: width, Height: jdeSweepHeight})
	s.Update(itemFormRefLoadedMsg{})
	s.Update(itemFormItemLoadedMsg{item: &omsapi.Item{
		ID: "kit-1", Name: "Eufy Ink Kit", SKU: "EIK-4", IsActive: true, ReorderQuantity: 1,
	}})
	s.Update(itemFormKitLoadedMsg{err: errors.New("server error (500)")})
	if s.loading {
		t.Fatal("the sheet never finished loading")
	}
	return s
}

// TestItemFormKit_AnUnansweredQuestionIsSaidAndBlocksTheSave. The item loads
// fine, so the sheet looks exactly like an ordinary item's — and its save then
// PATCHes /items/, which is a flat 404 for a kit id, with the reason never
// named. The detail screen already refuses to swallow this; the form must answer
// the same question the same way.
func TestItemFormKit_AnUnansweredQuestionIsSaidAndBlocksTheSave(t *testing.T) {
	fake := &kitFormServer{}
	srv := httptest.NewServer(fake.handler())
	defer srv.Close()

	s := kitFormUnanswered(t, 120)
	s.deps = Deps{OMS: omsapi.New(srv.URL), Ctx: context.Background()}

	if body := strings.Join(s.formLines().text, "\n"); !strings.Contains(body, "Kit status unavailable") {
		t.Errorf("the failed kit question was swallowed:\n%s", body)
	}
	// It must not have guessed either way about the affordance.
	if kitFormHasField(s, fKitComponents) {
		t.Error("an unanswered question grew a components row it cannot save")
	}

	_, cmd := s.submit()
	if cmd != nil {
		if msg, ok := cmd().(itemFormSavedMsg); ok {
			t.Fatalf("the save went out anyway: %+v", msg)
		}
	}
	fake.mu.Lock()
	writes := append([]string(nil), fake.writes...)
	fake.mu.Unlock()
	if len(writes) != 0 {
		t.Fatalf("a save reached the API while the kit question was open: %v", writes)
	}
	// Refused with a reason, not silently: Enter is still the save key and the
	// bar still names it, so pressing it has to say why nothing happened.
	if !strings.Contains(s.errMsg, "kit status unavailable") {
		t.Errorf("the refusal gave no reason: %q", s.errMsg)
	}
}

// TestItemFormKit_TheUnavailableLineFitsTheFloor. It is the longest thing the
// sheet grows for this state, and the body is CLIPPED, not wrapped.
func TestItemFormKit_TheUnavailableLineFitsTheFloor(t *testing.T) {
	for _, width := range kitTestWidths {
		s := kitFormUnanswered(t, width)
		budget := screenBodyWidth(width)
		for _, line := range s.formLines().text {
			if !strings.Contains(line, "Kit status") && !strings.Contains(line, "cannot be saved") {
				continue
			}
			if w := lipgloss.Width(line); w > budget {
				t.Errorf("at %d columns the unavailable line is %d wide against a %d pane: %q",
					width, w, budget, line)
			}
		}
	}
}

// TestItemFormKit_A404LeavesTheSheetOrdinary. A 404 is the ANSWER "not a kit",
// not a failure, so nothing about the sheet may change and the save must still
// go to /items/.
func TestItemFormKit_A404LeavesTheSheetOrdinary(t *testing.T) {
	fake := &kitFormServer{}
	srv := httptest.NewServer(fake.handler())
	defer srv.Close()

	s := NewInventoryItemFormScreen(Deps{OMS: omsapi.New(srv.URL), Ctx: context.Background()}, "itm-1")
	s.Update(tea.WindowSizeMsg{Width: 120, Height: jdeSweepHeight})
	s.Update(itemFormRefLoadedMsg{})
	s.Update(itemFormItemLoadedMsg{item: &omsapi.Item{
		ID: "itm-1", Name: "Cyan ink", SKU: "CI-100", IsActive: true, ReorderQuantity: 1,
	}})
	s.Update(itemFormKitLoadedMsg{}) // what loadKit sends for omsapi.IsNotKit

	if body := strings.Join(s.formLines().text, "\n"); strings.Contains(body, "Kit status unavailable") {
		t.Errorf("a 404 was reported as a failure — it is the ANSWER:\n%s", body)
	}
	_, cmd := s.submit()
	if cmd == nil {
		t.Fatal("the save was blocked for an ordinary item")
	}
	cmd()
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if len(fake.writes) != 1 || fake.writes[0] != "PATCH /api/inventory/items/itm-1/" {
		t.Fatalf("writes = %v, want a single PATCH to /items/", fake.writes)
	}
}

// TestItemFormKit_TheFrozenStockRowExplainsItselfAtTheFloor. The row is
// read-only, so the one line saying WHY has to survive the clip — and it did
// not: at 80 columns the pane is 51 and the row spends 33 of it before the hint
// gets any, so a 33-column sentence was cut to "a kit carries no s". The
// explanation of a frozen row being itself unreadable at the canonical width is
// the exact defect this whole change has been closing.
//
// Asserted on the CLIPPED render, which is what the operator sees.
func TestItemFormKit_TheFrozenStockRowExplainsItselfAtTheFloor(t *testing.T) {
	for _, width := range kitTestWidths {
		s := kitFormSheet(t, kitFormFixture(), width) // stock 0: the ordinary kit
		raw, clipped := kitFormRow(t, s, width, "Current stock")
		// The clamp changing the row at all IS the defect: it cuts with nothing
		// to show it did.
		if clipped != raw {
			t.Errorf("at %d columns the frozen row is cut from %q to %q", width, raw, clipped)
		}
		if !strings.Contains(clipped, "no stock") {
			t.Errorf("at %d columns the frozen row does not say why it is frozen: %q", width, clipped)
		}
	}
}

// TestItemFormKit_TheClearingWarningSurvivesTheFloorToo is the other half, and
// the constraint the terse hint must not have cost: when a kit DOES carry stock,
// the sheet still says so in full and still says what saving does to it.
func TestItemFormKit_TheClearingWarningSurvivesTheFloorToo(t *testing.T) {
	for _, width := range kitTestWidths {
		s := kitFormSheet(t, kitFormStockedKit(7), width)
		budget := screenBodyWidth(width)
		clipped := clampToBox(strings.Join(s.formLines().text, "\n"), budget, 200)
		flat := strings.Join(strings.Fields(clipped), " ")
		for _, want := range []string{
			"Recorded as 7 on hand",
			"A kit holds no stock of its own, so saving this sheet clears that to 0.",
		} {
			if !strings.Contains(flat, want) {
				t.Errorf("at %d columns the clipped sheet lost %q:\n%s", width, want, clipped)
			}
		}
		// And the row does not ALSO carry the terse hint: two sentences about a
		// kit holding no stock would bury the half that says what saving does.
		if _, row := kitFormRow(t, s, width, "Current stock"); strings.Contains(row, "no stock") {
			t.Errorf("at %d columns the warning is duplicated on the row itself: %q", width, row)
		}
	}
}

// kitFormRow is the named field's row as rendered and as the operator actually
// sees it — the second return is the first put through the same clamp Root
// applies, so a test can assert the clamp changed nothing.
func kitFormRow(t *testing.T, s *InventoryItemFormScreen, width int, label string) (string, string) {
	t.Helper()
	budget := screenBodyWidth(width)
	for _, line := range s.formLines().text {
		if !strings.Contains(line, label) {
			continue
		}
		return line, clampToBox(line, budget, 1)
	}
	t.Fatalf("no %q row on the sheet at %d columns", label, width)
	return "", ""
}

// ---------------------------------------------------------------------------
// The serialized row
// ---------------------------------------------------------------------------

// kitFormSerializedKit is the stray-data case: a kit whose stored is_serialized
// is true. The server REFUSES to put one in that state — KitSerializer.validate
// rejects a truthy is_serialized for a kit, and the model's _clean_kit says the
// same — so this is not a state an operator can reach through the sheet. It is
// reachable the way stray stock is: InventoryItem.save() never runs
// full_clean(), so a direct write never meets either rule.
func kitFormSerializedKit() *omsapi.Kit {
	kit := kitFormFixture()
	kit.IsSerialized = true
	kit.SerialTrackingMode = "asset"
	return kit
}

// TestItemFormKit_TheSerializedRowIsReadOnlyForAKit. A kit's components carry
// the serials, not the kit, and the serializer refuses the flag outright — so a
// live toggle here is a promise the save can never keep. Frozen means frozen on
// all four counts: the keys do nothing, the sheet does not go dirty, the row
// draws as a value rather than a choice, and the bar stops naming ←→.
func TestItemFormKit_TheSerializedRowIsReadOnlyForAKit(t *testing.T) {
	s := kitFormSheet(t, kitFormFixture(), 120)
	kitFormCursorTo(t, s, fIsSerialized)
	before := s.isSerialized

	for _, key := range []tea.KeyMsg{
		{Type: tea.KeyRunes, Runes: []rune(" ")},
		{Type: tea.KeyRight},
		{Type: tea.KeyLeft},
	} {
		s.Update(key)
		if s.isSerialized != before {
			t.Fatalf("%v flipped a kit's serialized row: %v -> %v", key, before, s.isSerialized)
		}
	}
	if s.dirty() {
		t.Error("pressing a toggle key on a read-only row made the sheet dirty")
	}
	// A frozen row must not draw the angle brackets that promise ←/→ works, and
	// the bar must not name the key either.
	row, _ := kitFormRow(t, s, 120, "Track serial numbers")
	if strings.Contains(row, "<") {
		t.Errorf("a kit's frozen serialized row still renders as a choice: %q", row)
	}
	for _, item := range s.formBar(s.formLines()) {
		if item.Key == "←→" {
			t.Errorf("the bar offers ←→ on a kit's frozen serialized row: %+v", item)
		}
	}
	// And the mode row that hangs off the flag is not offered either — a select
	// whose value the kit payload drops is the same broken promise.
	if kitFormHasField(s, fSerialTrackingMode) {
		t.Error("a kit was offered a serial tracking mode its save always clears")
	}
	if kitFormHasField(kitFormSheet(t, kitFormSerializedKit(), 120), fSerialTrackingMode) {
		t.Error("a stray-serialized kit was offered a serial tracking mode")
	}
}

// TestItemFormKit_AnOrdinaryItemsSerializedRowStillToggles is acceptance
// criterion 4 on this row: only a kit's is frozen, and an ordinary item keeps
// the toggle, the bar entry and the mode row that follows it.
func TestItemFormKit_AnOrdinaryItemsSerializedRowStillToggles(t *testing.T) {
	s := kitFormSheet(t, nil, 120)
	kitFormCursorTo(t, s, fIsSerialized)
	before := s.isSerialized

	s.Update(tea.KeyMsg{Type: tea.KeyRight})
	if s.isSerialized == before {
		t.Fatalf("an ordinary item's serialized row stopped toggling (still %v)", before)
	}
	if !s.dirty() {
		t.Error("toggling an ordinary item's serialized row did not make the sheet dirty")
	}
	if !kitFormHasField(s, fSerialTrackingMode) {
		t.Error("turning serial tracking on did not offer the mode row")
	}
	var named bool
	for _, item := range s.formBar(s.formLines()) {
		if item.Key == "←→" {
			named = true
		}
	}
	if !named {
		t.Error("the bar stopped naming ←→ on an ordinary item's toggle row")
	}
	row, _ := kitFormRow(t, s, 120, "Track serial numbers")
	if !strings.Contains(row, "<") {
		t.Errorf("an ordinary item's serialized row stopped rendering as a choice: %q", row)
	}
}

// TestItemFormKit_TheKitSaveAssertsNotSerialized. is_serialized has no omitempty
// so it rides every save, and omitting it could not help: KitSerializer.validate
// falls back to the STORED value for an absent key, so a kit carrying a stray
// true would be unsaveable from ScanTTY for good — not just for that field. The
// sheet therefore asserts the only value a kit may hold.
func TestItemFormKit_TheKitSaveAssertsNotSerialized(t *testing.T) {
	for _, tc := range []struct {
		name string
		kit  *omsapi.Kit
	}{
		{"an ordinary kit", kitFormFixture()},
		{"a kit carrying a stray flag", kitFormSerializedKit()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := &kitFormServer{}
			srv := httptest.NewServer(fake.handler())
			defer srv.Close()

			s := kitFormSheet(t, tc.kit, 120)
			s.deps = Deps{OMS: omsapi.New(srv.URL), Ctx: context.Background()}
			_, cmd := s.submit()
			if cmd == nil {
				t.Fatal("submit produced no command")
			}
			if msg, ok := cmd().(itemFormSavedMsg); !ok || msg.err != nil {
				t.Fatalf("save failed: %+v", msg)
			}

			fake.mu.Lock()
			defer fake.mu.Unlock()
			if len(fake.bodies) != 1 || fake.writes[0] != "PATCH /api/inventory/kits/kit-1/" {
				t.Fatalf("writes = %v", fake.writes)
			}
			got, present := fake.bodies[0]["is_serialized"]
			if !present {
				t.Fatalf("is_serialized was omitted, which leaves a stray-flagged kit unsaveable: %v", fake.bodies[0])
			}
			if got != false {
				t.Errorf("is_serialized = %v, want false — a kit may hold no other value", got)
			}
			// The mode is what the flag hangs off, so it must not ride along
			// asserting a tracking scheme for a kit that tracks nothing.
			if mode, present := fake.bodies[0]["serial_tracking_mode"]; present {
				t.Errorf("serial_tracking_mode = %v rode a kit's save", mode)
			}
		})
	}
}

// TestItemFormKit_AStraySerializedFlagSaysWhatSavingDoesToIt. Same rule as the
// stray stock figure: asserting false writes over a fact that is really there in
// shared data, and doing that invisibly as a side effect of renaming the kit is
// the harm. Measured on the CLIPPED render, because the pane is 51 columns at
// the floor and this note is far longer than that.
func TestItemFormKit_AStraySerializedFlagSaysWhatSavingDoesToIt(t *testing.T) {
	for _, width := range kitTestWidths {
		s := kitFormSheet(t, kitFormSerializedKit(), width)
		budget := screenBodyWidth(width)
		clipped := clampToBox(strings.Join(s.formLines().text, "\n"), budget, 200)
		flat := strings.Join(strings.Fields(clipped), " ")
		for _, want := range []string{
			"Recorded as serialized.",
			"A kit's components carry the serials, not the kit, so saving this sheet clears that to No.",
		} {
			if !strings.Contains(flat, want) {
				t.Errorf("at %d columns the clipped sheet lost %q:\n%s", width, want, clipped)
			}
		}
		// Shown without the cursor going near the row: an operator renaming a
		// kit has no reason to visit a row they cannot change.
		if id, ok := s.currentFieldID(); ok && id == fIsSerialized {
			t.Fatal("the fixture starts on the serialized row, which defeats the point of the check")
		}
		// And the frozen row still explains ITSELF at the floor rather than
		// being cut: the clamp changing the row at all is the defect.
		raw, row := kitFormRow(t, s, width, "Track serial numbers")
		if row != raw {
			t.Errorf("at %d columns the frozen serialized row is cut from %q to %q", width, raw, row)
		}
	}
}

// TestItemFormKit_AnUnserializedKitGetsNoNotice. The ordinary case stays quiet —
// there is nothing to overwrite — and the row instead carries the terse hint
// that says why it is frozen, which must survive the clip at every width.
func TestItemFormKit_AnUnserializedKitGetsNoNotice(t *testing.T) {
	for _, width := range kitTestWidths {
		s := kitFormSheet(t, kitFormFixture(), width)
		if body := strings.Join(s.formLines().text, "\n"); strings.Contains(body, "Recorded as serialized") {
			t.Errorf("at %d columns a kit that is not serialized was warned about losing it:\n%s", width, body)
		}
		raw, row := kitFormRow(t, s, width, "Track serial numbers")
		if row != raw {
			t.Errorf("at %d columns the frozen serialized row is cut from %q to %q", width, raw, row)
		}
		if !strings.Contains(row, "its components do") {
			t.Errorf("at %d columns the frozen serialized row does not say why it is frozen: %q", width, row)
		}
	}
	// Nor is an ordinary serialized item warned about anything.
	plain := kitFormSheet(t, nil, 120)
	kitFormCursorTo(t, plain, fIsSerialized)
	plain.Update(tea.KeyMsg{Type: tea.KeyRight})
	if body := strings.Join(plain.formLines().text, "\n"); strings.Contains(body, "Recorded as serialized") {
		t.Errorf("an ordinary serialized item was warned about losing its flag:\n%s", body)
	}
}

// ---------------------------------------------------------------------------
// The status line at the floor
// ---------------------------------------------------------------------------

// kitFormStatusRow is the row jdeScreen.statusRow draws immediately above the
// pinned action bar, as rendered AND as the operator actually sees it — the
// second return is the first put through the same clamp Root applies.
//
// That row is a single unwrapped line: frame() places it exactly one row above
// the bar, so it is bounded by the layer (fitStatus) rather than folded — and
// before that bound existed it was CUT by the pane, with no ellipsis to show
// anything was lost. Reading it out of the real View() rather than off the field
// is what makes the assertion hold when the prefix ("✗ ") or the pane budget
// changes — a test that only measured the constant would not notice either.
func kitFormStatusRow(t *testing.T, s *InventoryItemFormScreen, width int) (string, string) {
	t.Helper()
	budget := screenBodyWidth(width)
	for _, line := range strings.Split(s.View(), "\n") {
		if !strings.Contains(line, "✗") {
			continue
		}
		return line, clampToBox(line, budget, 1)
	}
	t.Fatalf("no refusal is on screen at %d columns:\n%s", width, s.View())
	return "", ""
}

// TestItemFormKit_TheQuantityRefusalIsReadableAtTheFloor. A refusal the operator
// cannot read is a refusal that did not happen, and the floor value is the only
// actionable part of this one — it sits at the END of the sentence, which is
// exactly what an unwrapped cut takes first.
func TestItemFormKit_TheQuantityRefusalIsReadableAtTheFloor(t *testing.T) {
	for _, width := range kitTestWidths {
		s := kitFormSheet(t, kitFormFixture(), width)
		kitFormCursorTo(t, s, fKitComponents)
		s.Update(tea.KeyMsg{Type: tea.KeyCtrlE}) // list
		s.Update(tea.KeyMsg{Type: tea.KeyCtrlE}) // the first component's editor

		s.kitRowQty.SetValue("0")
		s.Update(tea.KeyMsg{Type: tea.KeyEnter})
		if s.phase != itemFormPhaseKitRow {
			t.Fatalf("at %d columns a zero quantity was accepted", width)
		}

		raw, clipped := kitFormStatusRow(t, s, width)
		if clipped != raw {
			t.Errorf("at %d columns the quantity refusal is cut from %q to %q", width, raw, clipped)
		}
		if !strings.Contains(clipped, "at least 1") {
			t.Errorf("at %d columns the operator cannot read the floor: %q", width, clipped)
		}
	}
}

// TestItemFormKit_ThePickRefusalIsReadableAtTheFloor is the same rule on the
// picker, where the message used to be built from the item's name and the reason
// — 113 columns for a realistic name, so it was cut at 80, 100 AND 120, and at
// the floor the reason never appeared at all.
//
// Shortening it costs nothing because the picker ROW the cursor is sitting on
// already carries both readings (kitPickLabel appends "— <why>"), which the
// second half of this test pins: the status line was DUPLICATING the row and
// losing the copy. The row is FITTED to the pane rather than cut by it, so it
// carries both readings wherever the pane can hold them and ellipsizes visibly
// where it cannot — which is why the fixture holds a long name and a short one.
func TestItemFormKit_ThePickRefusalIsReadableAtTheFloor(t *testing.T) {
	const (
		longName  = "Serialized calibration widget assembly"
		shortName = "Cal widget"
		why       = "serialized — receiving a kit records no serials"
	)
	for _, width := range kitTestWidths {
		s := kitFormSheet(t, kitFormFixture(), width)
		s.kitItems = []omsapi.Item{
			{ID: "itm-y", Name: "Yellow ink cartridge, high yield, wide-format", SKU: "YI-100-XL", Stock: 9},
			{ID: "itm-s", Name: longName, IsSerialized: true},
			{ID: "itm-t", Name: shortName, IsSerialized: true},
		}
		kitFormCursorTo(t, s, fKitComponents)
		s.Update(tea.KeyMsg{Type: tea.KeyCtrlE}) // list
		s.kitCursor = s.kitAddRow()
		s.Update(tea.KeyMsg{Type: tea.KeyCtrlE}) // picker

		// Refuse the one with the realistic long name: that is the case whose
		// message used to run to 113 columns.
		long := kitFormSelectPick(t, s, "itm-s")
		if long.why == "" {
			t.Fatalf("at %d columns the serialized item is not unpickable: %+v", width, s.kitPickOptions)
		}
		s.Update(tea.KeyMsg{Type: tea.KeyEnter})
		if len(s.kitRows) != 2 {
			t.Fatalf("at %d columns an illegal component was added: %+v", width, s.kitRows)
		}

		raw, clipped := kitFormStatusRow(t, s, width)
		if clipped != raw {
			t.Errorf("at %d columns the pick refusal is cut from %q to %q", width, raw, clipped)
		}
		if !strings.Contains(clipped, "cannot be a kit component") {
			t.Errorf("at %d columns the operator cannot read the refusal: %q", width, clipped)
		}

		// What the status line no longer says, the rows still do.
		for _, name := range []string{longName, shortName} {
			row := kitFormPickRow(t, s, name)
			if trimmed := clampToBox(row, screenBodyWidth(width), 1); trimmed != row {
				t.Errorf("at %d columns %q's row is cut from %q to %q", width, name, row, trimmed)
			}
			if !strings.Contains(row, name) && !strings.HasSuffix(row, "…") {
				t.Errorf("at %d columns %q's row neither names it nor marks itself short: %q", width, name, row)
			}
			if lipgloss.Width(row) < lipgloss.Width(name+" — "+why) {
				continue // the pane cannot hold the whole reading; the ellipsis says so
			}
			for _, want := range []string{name, why} {
				if !strings.Contains(row, want) {
					t.Errorf("at %d columns %q's row lost %q: %q", width, name, want, row)
				}
			}
		}
	}
}

// kitFormSelectPick puts the picker's cursor on one option and returns it.
func kitFormSelectPick(t *testing.T, s *InventoryItemFormScreen, id string) kitPickOption {
	t.Helper()
	for i := range s.kitPickOptions {
		if s.kitPickOptions[i].item.ID == id {
			s.kitPickCursor = i
			return s.kitPickOptions[i]
		}
	}
	t.Fatalf("the picker does not offer %q: %+v", id, s.kitPickOptions)
	return kitPickOption{}
}

// kitFormPickRow is the picker row for the item with this name, as the list
// draws it — matched on the leading run of the name, which survives the fitting.
func kitFormPickRow(t *testing.T, s *InventoryItemFormScreen, name string) string {
	t.Helper()
	head := name
	if len(head) > 10 {
		head = head[:10]
	}
	_, body := s.kitPickView()
	for _, line := range body.text {
		if strings.Contains(line, head) {
			return strings.TrimSpace(line)
		}
	}
	t.Fatalf("no picker row for %q:\n%s", name, strings.Join(body.text, "\n"))
	return ""
}

// TestItemFormKit_TheUnansweredSaveRefusalIsReadableAtTheFloor. The third
// message this bead sends to the status line: the save refused because "is this
// a kit?" went unanswered. It used to carry the server's own error text, which
// is unbounded — and the body above it already states the whole thing, WRAPPED,
// so the status row was duplicating a note it could only lose.
func TestItemFormKit_TheUnansweredSaveRefusalIsReadableAtTheFloor(t *testing.T) {
	for _, width := range kitTestWidths {
		s := kitFormUnanswered(t, width)
		s.submit()

		raw, clipped := kitFormStatusRow(t, s, width)
		if clipped != raw {
			t.Errorf("at %d columns the save refusal is cut from %q to %q", width, raw, clipped)
		}
		if !strings.Contains(clipped, "cannot save") {
			t.Errorf("at %d columns the operator cannot read what was refused: %q", width, clipped)
		}
		// And the full reason is still on screen, in the body, where it wraps.
		body := clampToBox(strings.Join(s.formLines().text, "\n"), screenBodyWidth(width), 200)
		if flat := strings.Join(strings.Fields(body), " "); !strings.Contains(flat, "Kit status unavailable") {
			t.Errorf("at %d columns the reason is nowhere on screen:\n%s", width, body)
		}
	}
}
