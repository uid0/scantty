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
	"fmt"
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
	s.kitItems = kitFormMixedCatalogue()
	kitFormOpenPicker(t, s)

	// A component already on the list, and the kit itself, are not choices — the
	// serializer rejects both, so they are not offered at all. They are the only
	// two exclusions left: a serialized item is an ordinary option now, which
	// TestItemFormKit_ASerializedItemIsAPickableComponent is about.
	for _, opt := range s.kitPickOptions {
		if opt.item.ID == "itm-m" || opt.item.ID == "kit-1" {
			t.Errorf("the picker offered %q, which cannot be added", opt.item.Name)
		}
	}

	kitFormSelectPick(t, s, "itm-y")
	s.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if len(s.kitRows) != 3 || s.kitRows[2].component != "itm-y" || s.kitRows[2].quantity != 1 {
		t.Fatalf("add left %+v", s.kitRows)
	}
	if s.phase != itemFormPhaseKitRow {
		t.Errorf("adding did not land on the new component's quantity (phase %d)", s.phase)
	}
}

// TestItemFormKit_EveryOptionOnThePickerCommits is the structural half of the
// lift, and it replaces a test that pinned a per-row refusal dying when the
// cursor moved off it.
//
// That refusal was the only one the picker had, and the property it needed —
// "an answer about THAT ITEM must not outlive that item being under the cursor"
// — is now unreachable rather than merely unused: nothing left is about a row.
// So what is pinned instead is why it is unreachable: EVERY option this picker
// offers commits. Derived over the whole list rather than over the one row
// somebody thought to check, so an exclusion re-introduced as a dim fails here
// whichever row it lands on.
func TestItemFormKit_EveryOptionOnThePickerCommits(t *testing.T) {
	s := kitFormSheet(t, kitFormFixture(), 120)
	s.kitItems = append(kitFormMixedCatalogue(), kitPickFillers(6)...)
	kitFormOpenPicker(t, s)
	if len(s.kitPickOptions) < 2 {
		t.Fatalf("the fixture offers nothing to sweep: %+v", s.kitPickOptions)
	}

	// Walked one at a time, each from a freshly opened picker, so a commit that
	// changes the list cannot hide the row after it.
	for i := range s.kitPickOptions {
		fresh := kitFormSheet(t, kitFormFixture(), 120)
		fresh.kitItems = append(kitFormMixedCatalogue(), kitPickFillers(6)...)
		kitFormOpenPicker(t, fresh)
		opt := fresh.kitPickOptions[i]
		fresh.kitPickCursor = i
		before := len(fresh.kitRows)
		fresh.Update(tea.KeyMsg{Type: tea.KeyEnter})
		if len(fresh.kitRows) != before+1 {
			t.Errorf("Enter refused %q (%s): %+v", opt.item.Name, opt.item.ID, fresh.kitRows)
			continue
		}
		if got := fresh.kitRows[before].component; got != opt.item.ID {
			t.Errorf("Enter on %q added %q instead", opt.item.ID, got)
		}
		if fresh.phase != itemFormPhaseKitRow {
			t.Errorf("Enter on %q did not open its editor (phase %d)", opt.item.ID, fresh.phase)
		}
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

// TestItemFormKit_AnAnswerDiesWithTheOptionsItWasAbout. The picker's one
// remaining answer is about the LIST — why there was nothing to add — so it
// cannot outlive the list it was about: left standing over a rebuilt one it
// answers a keypress nobody made, on a frame that now has rows.
//
// Every path that rebuilds the options is walked, because the clear lives in the
// ONE function they all come through and that is the property worth pinning:
// typing into the filter, backspacing back out of it, the catalogue landing, and
// leaving and reopening the picker.
func TestItemFormKit_AnAnswerDiesWithTheOptionsItWasAbout(t *testing.T) {
	s := kitFormSheet(t, kitFormFixture(), 120)
	s.kitItems = kitFormMixedCatalogue()
	kitFormOpenPicker(t, s)

	// Filter it down to nothing, and ask.
	for _, r := range "zzzz" {
		s.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	s.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if !strings.Contains(s.View(), kitPickNoMatchNote) {
		t.Fatalf("the answer was not given in the first place:\n%s", s.View())
	}

	// Backspacing rebuilds the list, so the message about the old one goes.
	s.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	if strings.Contains(s.View(), kitPickNoMatchNote) {
		t.Errorf("a stale answer survived a filter change:\n%s", s.View())
	}

	// It comes back for a second ask, and leaving and reopening clears it.
	s.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("z")})
	s.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if !strings.Contains(s.View(), kitPickNoMatchNote) {
		t.Fatalf("the answer did not come back for a second attempt:\n%s", s.View())
	}
	s.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if s.phase != itemFormPhaseKit {
		t.Fatalf("Esc did not leave the picker (phase %d)", s.phase)
	}
	s.Update(tea.KeyMsg{Type: tea.KeyCtrlE}) // reopen on the add row
	if strings.Contains(s.View(), kitPickNoMatchNote) {
		t.Errorf("a stale answer was still on screen over a freshly opened picker:\n%s", s.View())
	}

	// And the catalogue ARRIVING is the fourth path: an operator who pressed
	// Enter into a list that had not loaded must not be left reading "still
	// loading" over the rows it landed with.
	fresh := kitFormSheet(t, kitFormFixture(), 120)
	kitFormOpenPicker(t, fresh)
	fresh.kitItems = nil
	fresh.kitItemsLoading = true
	fresh.applyKitPickFilter()
	fresh.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if !strings.Contains(fresh.View(), kitPickLoadingNote) {
		t.Fatalf("Enter over a loading list said nothing:\n%s", fresh.View())
	}
	fresh.Update(itemFormKitItemsMsg{items: kitFormMixedCatalogue()})
	if strings.Contains(fresh.View(), kitPickLoadingNote) {
		t.Errorf("the answer outlived the load it was about:\n%s", fresh.View())
	}
	if len(fresh.kitPickOptions) == 0 {
		t.Errorf("the arrived catalogue produced no options: %+v", fresh.kitPickOptions)
	}
}

// kitPickFillers are ordinary pickable items, enough of them to push the option
// list past the pane. Named so nothing in the assertions can match them.
func kitPickFillers(n int) []omsapi.Item {
	out := make([]omsapi.Item, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, omsapi.Item{
			ID:   fmt.Sprintf("itm-f%d", i),
			Name: fmt.Sprintf("Filler stock %d", i),
			SKU:  fmt.Sprintf("FS-%03d", i),
		})
	}
	return out
}

// TestItemFormKit_ARefusalDiesWhenTheCursorLeavesItsRow was here, and it is gone
// rather than reworded.
//
// It drove every movement key against a refusal that said "that item" — the row
// the cursor was on — to prove the message could not be left describing, and
// defaming, whatever the cursor moved to. The only refusal that produced it was
// the serialized one, which the server retired, and there is no per-row message
// left for a cursor to move out from under: pickRow declines with a count of
// zero, so the empty-list answer has no row to go stale against.
//
// What replaced it is TestItemFormKit_EveryOptionOnThePickerCommits, which pins
// the reason it is unreachable rather than the behaviour of a state that no
// longer exists. toKitPick keeps the clear anyway, and says why in its own
// comment: it is the ONE place the highlight lands, so a per-row answer added
// later inherits the rule by arriving there.

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

// TestItemFormKit_EveryPickerRowFitsThePaneAtEveryWidth is what is left of
// TestItemFormKit_ThePickRefusalIsReadableAtTheFloor once the refusal it was
// about was retired.
//
// The half that survives is the half that was never about the refusal: a picker
// list is drawn straight into the pane, so a row cut by clampToBox loses its
// tail with nothing to say it had. kitPickLabel FITS each row instead, so it
// carries what the pane can hold and ellipsizes visibly where it cannot — which
// is why the fixture holds a name well past any affordable column beside a short
// one, at every width the kit surfaces are measured at.
//
// Serialized items are in the fixture on purpose: they are ordinary rows now, and
// this is where "ordinary" is checked to mean the same shape as everything else
// rather than merely the same commit.
func TestItemFormKit_EveryPickerRowFitsThePaneAtEveryWidth(t *testing.T) {
	const (
		longName  = "Serialized calibration widget assembly"
		shortName = "Cal widget"
	)
	// The other direction of the same rule: 51 is the width that must HOLD, not
	// the width to render as though we had. A row clipped to a fixed floor would
	// draw abbreviated on a 120-column terminal with forty columns of pane going
	// spare, on the rows an operator picks FROM.
	seen := map[int]int{}
	for _, width := range kitTestWidths {
		s := kitFormSheet(t, kitFormFixture(), width)
		s.kitItems = []omsapi.Item{
			{ID: "itm-y", Name: "Yellow ink cartridge, high yield, wide-format", SKU: "YI-100-XL", Stock: 9},
			{ID: "itm-s", Name: longName, Stock: 3, IsSerialized: true},
			{ID: "itm-t", Name: shortName, Stock: 0, IsSerialized: true},
		}
		kitFormOpenPicker(t, s)
		seen[width] = lipgloss.Width(kitFormPickRow(t, s, longName))

		// Derived over every row the picker drew, so a shape added for one kind
		// of item is measured without anyone remembering this test.
		_, body := s.kitPickView()
		if len(body.text) == 0 {
			t.Fatalf("at %d columns the picker drew nothing", width)
		}
		for _, line := range body.text {
			if trimmed := clampToBox(line, screenBodyWidth(width), 1); trimmed != line {
				t.Errorf("at %d columns a picker row is cut from %q to %q", width, line, trimmed)
			}
		}
		for _, name := range []string{longName, shortName} {
			row := kitFormPickRow(t, s, name)
			if !strings.Contains(row, name) && !strings.HasSuffix(row, "…") {
				t.Errorf("at %d columns %q's row neither names it nor marks itself short: %q", width, name, row)
			}
		}
		// A short name leaves room for the reading beside it, at every width.
		if row := kitFormPickRow(t, s, shortName); !strings.Contains(row, "0 on hand") {
			t.Errorf("at %d columns a row with room to spare lost its stock reading: %q", width, row)
		}
	}
	if seen[120] <= seen[80] {
		t.Errorf("a long row is %d cells at 120 columns and %d at 80 — the pane's extra width is going unused",
			seen[120], seen[80])
	}
}

// kitFormStatusLine is the phase's status row as the terminal draws it: the row
// immediately above the action bar's rule, read off the CLIPPED render and
// failed if the clamp cut it.
//
// kitFormStatusRow finds a row by its "✗" mark and cannot see this one: an
// answer that rides the WORKING line is muted and carries no mark at all, which
// is exactly the row the empty picker's loading answer lands on.
func kitFormStatusLine(t *testing.T, s *InventoryItemFormScreen, width int) string {
	t.Helper()
	lines := strings.Split(s.View(), "\n")
	// The bar is the last actionBarRows lines and opens with its rule, so the
	// status row is the line before it.
	if len(lines) <= actionBarRows {
		t.Fatalf("at %d columns the frame has no status row:\n%s", width, s.View())
	}
	raw := lines[len(lines)-actionBarRows-1]
	if clipped := clampToBox(raw, screenBodyWidth(width), 1); clipped != raw {
		t.Errorf("at %d columns the status row is cut from %q to %q", width, raw, clipped)
	}
	return raw
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

// ---------------------------------------------------------------------------
// A serialized item is an ordinary component
// ---------------------------------------------------------------------------
//
// The captain's decision, applied on the screen that was still enforcing the
// rule it overturned.
//
// OpenMakerSuite lifted the ban on serialized kit components deliberately —
// KitComponent.clean() on its default branch carries the whole argument — because
// the hazard it named (stock credited with no serial recorded) was never unique
// to kits, and what replaced it is `serials_outstanding`, which REPORTS the gap
// on every receive path rather than forbidding one configuration.
//
// What did NOT change, and what the guard below exists for, is where a serial
// goes: a kit is bought as one SKU and stocked as its PARTS, so its own stock is
// permanently zero and a serial written against the kit's id names a unit nothing
// can ever draw down. Lifting the ban must not loosen that by one inch.

// kitFormMixedCatalogue is the picker's catalogue with a serialized item in it,
// beside an ordinary one and the two rows that are still not choices: the kit's
// own id, and an item the kit already lists.
func kitFormMixedCatalogue() []omsapi.Item {
	return []omsapi.Item{
		{ID: "itm-y", Name: "Yellow ink", SKU: "YI-100", Stock: 9},
		{ID: "itm-s", Name: "Serialized widget", SKU: "SW-1", Stock: 4,
			IsSerialized: true, SerialTrackingMode: "reusable"},
		{ID: "itm-m", Name: "Magenta ink", SKU: "MI-100"},   // already a component
		{ID: "kit-1", Name: "Eufy printer maintenance kit"}, // the kit itself
	}
}

// kitFormOpenPicker walks the sheet to the component picker the way an operator
// does — the components row, Ctrl-E to the list, DOWN to the trailing add row,
// Ctrl-E again — so everything asserted afterwards was reached by keys rather
// than by assignment. The walk is BOUNDED: a declined key would otherwise hang
// the package and name whichever test happened to be running (AGENTS.md).
func kitFormOpenPicker(t *testing.T, s *InventoryItemFormScreen) {
	t.Helper()
	kitFormCursorTo(t, s, fKitComponents)
	s.Update(tea.KeyMsg{Type: tea.KeyCtrlE})
	if s.phase != itemFormPhaseKit {
		t.Fatalf("the components row did not open the list (phase %d)", s.phase)
	}
	for i := 0; !s.onKitAddRow(); i++ {
		if i > len(s.kitRows)+2 {
			t.Fatalf("the cursor never reached the add row (%d of %d)", s.kitCursor, s.kitAddRow())
		}
		s.Update(tea.KeyMsg{Type: tea.KeyDown})
	}
	s.Update(tea.KeyMsg{Type: tea.KeyCtrlE})
	if s.phase != itemFormPhaseKitPick {
		t.Fatalf("the add row did not open the picker (phase %d)", s.phase)
	}
}

// kitFormClippedPane is the whole phase as the terminal really draws it: the
// screen's own render put through the same clamp Root.View() applies, so a claim
// about what an operator can read is measured on the pane and not on a frame the
// screen was allowed to overrun.
func kitFormClippedPane(s *InventoryItemFormScreen, width int) string {
	lines := strings.Split(s.View(), "\n")
	return clampToBox(s.View(), screenBodyWidth(width), len(lines))
}

// kitOldProhibition is every wording the retired rule was ever stated in on this
// screen. Kept as one roster so the sweep below cannot check for the phrase
// somebody happened to remember: a surface still implying the ban fails whichever
// of its sentences it kept.
var kitOldProhibition = []string{
	"cannot be a kit component",
	"records no serials",
	"credits stock without recording serials",
	"Serialized items cannot be kit components",
}

// TestItemFormKit_ASerializedItemIsAPickableComponent is the decision applied: a
// serialized item is an ORDINARY option here — listed, undimmed, picked with
// Enter like any other, landing on its quantity.
//
// Driven through the real screen at every width the kit surfaces are measured
// at, and read off the CLIPPED render, because the claim is about what an
// operator sees rather than about what a frame contains.
func TestItemFormKit_ASerializedItemIsAPickableComponent(t *testing.T) {
	for _, width := range kitTestWidths {
		s := kitFormSheet(t, kitFormFixture(), width)
		s.kitItems = kitFormMixedCatalogue()
		kitFormOpenPicker(t, s)

		// It is offered, and its row is not dimmed away as an impossible pick.
		row := kitFormPickRow(t, s, "Serialized widget")
		for _, gone := range kitOldProhibition {
			if strings.Contains(row, gone) {
				t.Errorf("at %d columns the picker row still states the retired rule: %q", width, row)
			}
		}

		// Enter picks it, exactly as it picks anything else.
		kitFormSelectPick(t, s, "itm-s")
		before := len(s.kitRows)
		s.Update(tea.KeyMsg{Type: tea.KeyEnter})
		if len(s.kitRows) != before+1 {
			t.Fatalf("at %d columns Enter refused a serialized component: %+v", width, s.kitRows)
		}
		added := s.kitRows[before]
		if added.component != "itm-s" || added.quantity != 1 {
			t.Fatalf("at %d columns the wrong row was added: %+v", width, added)
		}
		if s.phase != itemFormPhaseKitRow {
			t.Errorf("at %d columns picking did not land on the quantity (phase %d)", width, s.phase)
		}

		// And nothing anywhere on the pane still says it could not be done.
		pane := kitFormClippedPane(s, width)
		for _, gone := range kitOldProhibition {
			if strings.Contains(pane, gone) {
				t.Errorf("at %d columns the pane still states the retired rule (%q):\n%s", width, gone, pane)
			}
		}
	}
}

// TestItemFormKit_ASerializedComponentNeverSerializesTheKit is the line the lift
// must not cross.
//
// A serial belongs to the COMPONENT identity that goes on the shelf and never to
// the kit's own id. The kit editor's share of that guard is a single fact: this
// sheet can never write `is_serialized: true` on a kit, whatever its bill of
// materials contains — so the toggle stays frozen and the save ASSERTS false
// (KitSerializer.validate falls back to the STORED value when the key is absent,
// so omitting it would be no guard at all).
//
// The second leg is that the kit's own id is never offered as a component of
// itself. That used to be excluded TWICE for a stray-serialized kit — by its id
// and by the serialized filter — and the filter is gone, so the fixture here is
// the stray-serialized kit precisely because it is the case where only one
// exclusion is left.
func TestItemFormKit_ASerializedComponentNeverSerializesTheKit(t *testing.T) {
	fake := &kitFormServer{}
	srv := httptest.NewServer(fake.handler())
	defer srv.Close()

	s := kitFormSheet(t, kitFormSerializedKit(), 120)
	s.deps = Deps{OMS: omsapi.New(srv.URL), Ctx: context.Background()}
	s.kitItems = kitFormMixedCatalogue()
	kitFormOpenPicker(t, s)

	// The kit itself is not a choice, even though the serialized filter that
	// used to be a second gate on it is gone.
	for _, opt := range s.kitPickOptions {
		if opt.item.ID == "kit-1" {
			t.Fatalf("the picker offered the kit its own id: %+v", opt.item)
		}
	}

	kitFormSelectPick(t, s, "itm-s")
	s.Update(tea.KeyMsg{Type: tea.KeyEnter}) // adds it, lands on the quantity
	s.Update(tea.KeyMsg{Type: tea.KeyEnter}) // saves the row, back to the list
	if s.phase != itemFormPhaseKit {
		t.Fatalf("the component editor did not commit (phase %d)", s.phase)
	}

	// With a serialized component on the list, the KIT's own serialized row is
	// still frozen: no key on it flips the flag.
	s.Update(tea.KeyMsg{Type: tea.KeyEsc}) // back to the sheet
	kitFormCursorTo(t, s, fIsSerialized)
	before := s.isSerialized
	for _, key := range []tea.KeyMsg{
		{Type: tea.KeyRight}, {Type: tea.KeyLeft}, {Type: tea.KeySpace},
		{Type: tea.KeyRunes, Runes: []rune("y")}, {Type: tea.KeyEnter},
	} {
		s.Update(key)
		if s.isSerialized != before {
			t.Fatalf("%v serialized the KIT: %v -> %v", key, before, s.isSerialized)
		}
	}

	// And the wire says so: the component rides in `components`, and the kit's
	// own is_serialized goes as false whatever was stored.
	_, cmd := s.submit()
	if cmd == nil {
		t.Fatal("submit produced no command")
	}
	if msg, ok := cmd().(itemFormSavedMsg); !ok || msg.err != nil {
		t.Fatalf("save failed: %+v", msg)
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if len(fake.bodies) != 1 {
		t.Fatalf("writes = %v, want a single PATCH", fake.writes)
	}
	body := fake.bodies[0]
	if serialized, ok := body["is_serialized"].(bool); !ok || serialized {
		t.Errorf("the kit went to the wire serialized: is_serialized = %v", body["is_serialized"])
	}
	rows, ok := body["components"].([]any)
	if !ok {
		t.Fatalf("the bill of materials did not reach the wire: %v", body)
	}
	found := false
	for _, raw := range rows {
		row, _ := raw.(map[string]any)
		if row["component"] == "itm-s" {
			found = true
		}
		if row["component"] == "kit-1" {
			t.Errorf("the kit was written as a component of itself: %v", row)
		}
	}
	if !found {
		t.Errorf("the serialized component did not reach the wire: %v", rows)
	}
}

// TestItemFormKit_AnEmptyPickerAnswersEnter. With no option under the cursor
// Enter used to return a byte-identical pane — the reported-hang shape rule 1
// forbids, and after the lift the ONLY refusal this picker has left.
//
// The four answers are four different facts, because the remedy differs and
// because "could not tell" and "found nothing" are never the same answer: the
// catalogue has not arrived, it did not arrive, the filter excluded everything,
// or there is genuinely nothing left to add. Each is read off the CLIPPED status
// row, where an unwrapped sentence is cut from the tail — which is where the
// remedy is.
func TestItemFormKit_AnEmptyPickerAnswersEnter(t *testing.T) {
	cases := []struct {
		name  string
		reach func(t *testing.T, s *InventoryItemFormScreen)
		want  string
	}{
		{"loading", func(t *testing.T, s *InventoryItemFormScreen) {
			s.kitItems = nil
			s.kitItemsLoading = true
		}, kitPickLoadingNote},
		// The loading row is the one that has to carry TWO facts, so it is
		// checked for both: see the second half of the loop.

		{"unloaded", func(t *testing.T, s *InventoryItemFormScreen) {
			s.kitItems = nil
			s.kitItemsLoading = true
			s.Update(itemFormKitItemsMsg{err: errors.New("oms: http 502: bad gateway")})
		}, kitPickUnloadedNote},
		{"no match", func(t *testing.T, s *InventoryItemFormScreen) {
			s.Update(itemFormKitItemsMsg{items: kitFormMixedCatalogue()})
			for _, r := range "zzzz" {
				s.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
			}
		}, kitPickNoMatchNote},
		{"nothing left", func(t *testing.T, s *InventoryItemFormScreen) {
			// Every item the catalogue holds is either the kit itself or already
			// a component, so the drops leave nothing — with no filter typed.
			s.Update(itemFormKitItemsMsg{items: []omsapi.Item{
				{ID: "kit-1", Name: "Eufy printer maintenance kit"},
				{ID: "itm-c", Name: "Cyan ink"},
				{ID: "itm-m", Name: "Magenta ink"},
			}})
		}, kitPickNoOptionsNote},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, width := range kitTestWidths {
				s := kitFormSheet(t, kitFormFixture(), width)
				kitFormOpenPicker(t, s)
				tc.reach(t, s)
				s.applyKitPickFilter()
				if len(s.kitPickOptions) != 0 {
					t.Fatalf("at %d columns the fixture is not the empty state: %+v", width, s.kitPickOptions)
				}

				before := kitFormClippedPane(s, width)
				s.Update(tea.KeyMsg{Type: tea.KeyEnter})
				after := kitFormClippedPane(s, width)
				if after == before {
					t.Fatalf("at %d columns Enter redrew a byte-identical pane:\n%s", width, after)
				}
				row := kitFormStatusLine(t, s, width)
				if !strings.Contains(row, tc.want) {
					t.Errorf("at %d columns the answer is %q, want %q", width, row, tc.want)
				}
				// A load in flight keeps its own subject on the row: the answer
				// LEADS it, it does not replace it.
				if s.kitItemsLoading && !strings.Contains(row, kitPickLoadingVerb) {
					t.Errorf("at %d columns the answer displaced the work in flight: %q", width, row)
				}
			}
		})
	}
}

// TestItemFormKit_EveryPickAnswerFitsTheFloor was here and is gone: it measured
// a hand-kept roster of the answer constants against the status row's budget,
// which is an enumeration where a derivation exists.
//
// TestItemFormKit_AnEmptyPickerAnswersEnter above drives all four states through
// the real screen at every width and reads the DRAWN row, failing a clipped one
// — so a fifth answer is measured by being reachable rather than by being added
// to a list. The one thing the roster could see and the drive cannot is a
// constant no state produces, which is not a property worth a test.
