// Finding a kit and making one (web parity: /inventory/kits and
// /inventory/kits/new).
//
// The contract these hold:
//
//	a kit is FINDABLE      — the list drives /api/inventory/kits/, which is the
//	                         only browsable route there is: /items/ excludes kits
//	                         server-side, so no other list can ever show one
//	the search is the       — every rune forwards the term as ?search=, which the
//	SCANNER path             viewset matches against the supplier's own part
//	                         number as well as the name and SKU, so a scanned
//	                         box code narrows the list to its kit
//	a kit is CREATABLE     — `n` opens the SAME item sheet the kit editor already
//	                         lives on, and Enter POSTs /kits/ with the bill of
//	                         materials in the one request the serializer requires
//	a refusal is the        — the server's own sentence about the server's own
//	SERVER's                 field, on the one marked status row, with nothing
//	                         invented on this side and nothing navigated away
//
// Driven through Root.Update against a stateful httptest fake, because the keys
// in question (`n`, `K`, `I`, and every rune typed into the search box) are
// keys the global hotkey layer also binds — a screen-level drive would pass
// while the real app opened another workspace.
package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/uid0/scantty/internal/omsapi"
)

// kitAPIFake serves the three endpoints a kit browse-and-create session
// touches, and records every request so a test can assert what was really sent
// rather than what a client method was called with.
//
// refuse, when set, is the body the NEXT POST /kits/ answers with under a 400 —
// OMS's standard envelope, written out in full rather than built, so the test
// fixture is the shape config/api_errors.py documents and not this package's
// idea of it.
type kitAPIFake struct {
	mu       sync.Mutex
	requests []string
	bodies   []map[string]any
	kits     []map[string]any
	items    []map[string]any
	refuse   string
}

func (f *kitAPIFake) log(r *http.Request) {
	q := ""
	if raw := r.URL.RawQuery; raw != "" {
		q = "?" + raw
	}
	f.requests = append(f.requests, r.Method+" "+r.URL.Path+q)
}

func (f *kitAPIFake) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.log(r)
		w.Header().Set("Content-Type", "application/json")

		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/inventory/kits/":
			body := map[string]any{}
			if raw, _ := io.ReadAll(r.Body); len(raw) > 0 {
				_ = json.Unmarshal(raw, &body)
			}
			f.bodies = append(f.bodies, body)
			if f.refuse != "" {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(f.refuse))
				return
			}
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": "kit-new", "name": body["name"], "is_kit": true,
			})

		case r.URL.Path == "/api/inventory/kits/":
			rows := f.kits
			if term := strings.TrimSpace(r.URL.Query().Get("search")); term != "" {
				rows = nil
				for _, kit := range f.kits {
					// The viewset's own four columns, icontains — the supplier's
					// part number included, which is the one a scanner reads.
					for _, field := range []string{"name", "sku", "description", "supplier_sku"} {
						if v, _ := kit[field].(string); v != "" &&
							strings.Contains(strings.ToLower(v), strings.ToLower(term)) {
							rows = append(rows, kit)
							break
						}
					}
				}
			}
			if rows == nil {
				rows = []map[string]any{}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"count": len(rows), "next": nil, "previous": nil, "results": rows,
			})

		case r.URL.Path == "/api/inventory/items/":
			rows := f.items
			if rows == nil {
				rows = []map[string]any{}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"count": len(rows), "next": nil, "previous": nil, "results": rows,
			})

		default:
			// Everything else — the categories and locations the form's pickers
			// read, and the detail fetches a navigation kicks off.
			_ = json.NewEncoder(w).Encode(map[string]any{
				"count": 0, "next": nil, "previous": nil, "results": []any{},
			})
		}
	}
}

func (f *kitAPIFake) sawRequest(want string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, got := range f.requests {
		if got == want {
			return true
		}
	}
	return false
}

func (f *kitAPIFake) requestLog() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.requests...)
}

// kitFixtureRows is a catalogue with one kit in it, carrying the vendor part
// number a scanner reads off the box — a value that appears in NO other field,
// so a search that finds this kit by it cannot have found it by the name.
func kitFixtureRows() []map[string]any {
	return []map[string]any{{
		"id": "kit-1", "name": "Eufy printer maintenance kit", "sku": "EIK-4",
		"description": "CMYK plus cleaning", "supplier_sku": "VND-88117",
		"is_kit": true, "is_active": true, "component_count": 3,
	}}
}

// kitDriveRoot opens a screen through a real Root against the fake, loaded.
func kitDriveRoot(t *testing.T, fake *kitAPIFake, srv *httptest.Server, build func(Deps) Screen) (Root, Deps) {
	t.Helper()
	deps := Deps{OMS: omsapi.New(srv.URL), Ctx: context.Background()}
	screen := build(deps)
	r := newTestRoot(screen)
	r.deps = deps
	sized, _ := r.Update(tea.WindowSizeMsg{Width: 100, Height: jdeSweepHeight})
	root := sized.(Root)
	root = pump(t, root, root.screen.Init(), 0)
	return root, deps
}

// ---------------------------------------------------------------------------
// Browsing
// ---------------------------------------------------------------------------

// TestKits_TheListDrivesTheKitEndpoint is the whole browsing half in one
// assertion: the rows an operator sees came from /api/inventory/kits/, which is
// the only endpoint that will ever admit a kit exists.
func TestKits_TheListDrivesTheKitEndpoint(t *testing.T) {
	fake := &kitAPIFake{kits: kitFixtureRows()}
	srv := httptest.NewServer(fake.handler())
	defer srv.Close()

	root, _ := kitDriveRoot(t, fake, srv, func(d Deps) Screen { return NewKitListScreen(d) })

	if !fake.sawRequest("GET /api/inventory/kits/?page=1") {
		t.Fatalf("the kit list never asked the kit endpoint: %v", fake.requestLog())
	}
	list, ok := root.screen.(*ListScreen)
	if !ok {
		t.Fatalf("screen = %T, want *ListScreen", root.screen)
	}
	if len(list.rows) != 1 || list.rows[0].ID != "kit-1" {
		t.Fatalf("rows = %+v, want the one kit the server served", list.rows)
	}
	// The subtitle says what KIND of record this is — a name alone reads like
	// any other catalogue row — and the count leads because the row is clipped.
	if got := list.rows[0].Subtitle; !strings.HasPrefix(got, "3 components") ||
		!strings.Contains(got, "SKU EIK-4") {
		t.Errorf("subtitle = %q, want the component count leading and the item SKU behind it", got)
	}
}

// TestKits_AZeroComponentCountIsDroppedRatherThanShown. `component_count` has no
// omitempty upstream and a kit always has at least one component, so a zero here
// means the SERVER did not send the key — and "0 components" would be a figure
// nobody reported, on the row that says what the record is.
func TestKits_AZeroComponentCountIsDroppedRatherThanShown(t *testing.T) {
	got := kitListSubtitle(omsapi.Kit{Item: omsapi.Item{SKU: "EIK-4"}})
	if strings.Contains(got, "0 component") {
		t.Errorf("subtitle = %q, want the unreported count dropped rather than drawn as zero", got)
	}
	if got != "SKU EIK-4" {
		t.Errorf("subtitle = %q, want the SKU alone", got)
	}
}

// TestKits_SearchSendsTheScannedCodeToTheServer is the scanner path, driven the
// way a scanner drives it: `/` opens the box, the burst goes in a rune at a
// time, and the query reaches the SERVER — which is what matters, because the
// vendor part number a scanner reads is matched by KitViewSet's ?search= and by
// nothing on this side. A client-side filter would also have answered "no kits
// match" about page one while the kit sat on page two.
func TestKits_SearchSendsTheScannedCodeToTheServer(t *testing.T) {
	fake := &kitAPIFake{kits: kitFixtureRows()}
	srv := httptest.NewServer(fake.handler())
	defer srv.Close()

	root, _ := kitDriveRoot(t, fake, srv, func(d Deps) Screen { return NewKitListScreen(d) })
	root = key(t, root, listRuneKey("/"))
	for _, r := range "VND-88117" {
		root = key(t, root, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}

	if !fake.sawRequest("GET /api/inventory/kits/?page=1&search=VND-88117") {
		t.Fatalf("the scanned code never reached the server as ?search=: %v", fake.requestLog())
	}
	list := root.screen.(*ListScreen)
	if len(list.rows) != 1 || list.rows[0].ID != "kit-1" {
		t.Fatalf("rows = %+v, want the kit the vendor code names", list.rows)
	}
}

// TestKits_EnterOpensTheRecordsOwnDetail. A kit IS an InventoryItem and
// InventoryDetailScreen already draws its bill of materials and offers `E` to
// the editor, so enter must land there rather than on a second detail screen for
// a record that has one.
func TestKits_EnterOpensTheRecordsOwnDetail(t *testing.T) {
	fake := &kitAPIFake{kits: kitFixtureRows()}
	srv := httptest.NewServer(fake.handler())
	defer srv.Close()

	root, _ := kitDriveRoot(t, fake, srv, func(d Deps) Screen { return NewKitListScreen(d) })
	next, cmd := root.Update(listRuneKey("enter"))
	root = next.(Root)
	if cmd == nil {
		t.Fatal("enter on a kit row issued no command")
	}
	msg, ok := cmd().(SwitchScreenMsg)
	if !ok {
		t.Fatalf("enter produced %T, want a screen switch", cmd())
	}
	if msg.Workspace != WSInventory {
		t.Errorf("workspace = %q, want the Inventory nav highlight to stay put", msg.Workspace)
	}
	detail, ok := msg.Screen.(*InventoryDetailScreen)
	if !ok {
		t.Fatalf("enter opened %T, want the item detail", msg.Screen)
	}
	if detail.itemID != "kit-1" {
		t.Errorf("detail id = %q, want the kit the cursor was on", detail.itemID)
	}
}

// ---------------------------------------------------------------------------
// Creating
// ---------------------------------------------------------------------------

// TestKits_NewOpensTheKitSheet. `n` is claimed by the list (it is a global
// hotkey otherwise), and what it opens has to be the kit sheet — the same item
// form, with the bill-of-materials row live from the first frame, because the
// row is conditional on isKit() and a sheet built before that was set would not
// carry it until some unrelated toggle rebuilt the field list.
func TestKits_NewOpensTheKitSheet(t *testing.T) {
	fake := &kitAPIFake{kits: kitFixtureRows()}
	srv := httptest.NewServer(fake.handler())
	defer srv.Close()

	root, _ := kitDriveRoot(t, fake, srv, func(d Deps) Screen { return NewKitListScreen(d) })
	next, cmd := root.Update(listRuneKey("n"))
	root = next.(Root)
	if cmd == nil {
		t.Fatal("n on the kit list issued no command")
	}
	msg, ok := cmd().(SwitchScreenMsg)
	if !ok {
		t.Fatalf("n produced %T, want a screen switch", cmd())
	}
	if msg.Workspace != WSInventory {
		t.Errorf("workspace = %q, want the Inventory nav highlight to stay put", msg.Workspace)
	}
	form, ok := msg.Screen.(*InventoryItemFormScreen)
	if !ok {
		t.Fatalf("n opened %T, want the item form in kit mode", msg.Screen)
	}
	if !form.isKit() {
		t.Fatal("the sheet n opened is not a kit sheet")
	}
	if form.edit {
		t.Error("the sheet opened in edit mode")
	}
	if got := form.Title(); got != "New kit" {
		t.Errorf("title = %q, want the sheet to say which door it was opened by", got)
	}
	if !kitFormHasField(form, fKitComponents) {
		t.Error("the bill-of-materials row is absent from the sheet a kit is created on")
	}
}

// kitCreateSheet drives a kit create from `n` on the list to a filled sheet with
// one component on it, entirely through Root — the picker, the pick and the
// quantity editor included.
func kitCreateSheet(t *testing.T, fake *kitAPIFake, srv *httptest.Server) (Root, *InventoryItemFormScreen) {
	t.Helper()
	root, deps := kitDriveRoot(t, fake, srv, func(d Deps) Screen { return NewKitListScreen(d) })

	next, cmd := root.Update(listRuneKey("n"))
	root = next.(Root)
	form := cmd().(SwitchScreenMsg).Screen.(*InventoryItemFormScreen)
	form.deps = deps
	root.screen = form
	root = pump(t, root, form.Init(), 0)

	kitFormCursorTo(t, form, fName)
	kitDriveType(t, &root, "Ink kit")
	kitFormCursorTo(t, form, fDescription)
	kitDriveType(t, &root, "CMYK set")

	// Open the bill of materials, walk to its trailing add row, open the picker
	// and take the first item it offers.
	kitFormCursorTo(t, form, fKitComponents)
	root = key(t, root, tea.KeyMsg{Type: tea.KeyCtrlE})
	if form.phase != itemFormPhaseKit {
		t.Fatalf("ctrl+e on the components row left phase %d", form.phase)
	}
	root = key(t, root, tea.KeyMsg{Type: tea.KeyCtrlE})
	if form.phase != itemFormPhaseKitPick {
		t.Fatalf("ctrl+e on the add row left phase %d, want the picker", form.phase)
	}
	if len(form.kitPickOptions) == 0 {
		t.Fatalf("the picker offered nothing: err=%q loading=%v", form.kitItemsErr, form.kitItemsLoading)
	}
	root = key(t, root, tea.KeyMsg{Type: tea.KeyEnter})
	if form.phase != itemFormPhaseKitRow {
		t.Fatalf("the pick did not open the quantity editor (phase %d)", form.phase)
	}
	root = key(t, root, tea.KeyMsg{Type: tea.KeyEnter}) // commit the quantity
	root = key(t, root, tea.KeyMsg{Type: tea.KeyEnter}) // close the list
	if form.phase != itemFormPhaseForm {
		t.Fatalf("the sheet did not come back to the form (phase %d)", form.phase)
	}
	if len(form.kitRows) != 1 {
		t.Fatalf("kitRows = %+v, want the one component that was picked", form.kitRows)
	}
	return root, form
}

// kitDriveType sends a string a rune at a time through Root, which is how a
// keyboard (and a scanner) delivers one.
func kitDriveType(t *testing.T, root *Root, text string) {
	t.Helper()
	for _, r := range text {
		*root = key(t, *root, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
}

// TestKits_CreatePostsTheKitAndItsComponents. Two things have to be true on the
// wire and neither is visible from this side of the client: the POST goes to
// /kits/ (an item create would produce an ordinary item, since `is_kit` is not
// an item-serializer field at all), and the bill of materials rides WITH it,
// because KitSerializer.create refuses a kit that has none and there is no
// endpoint that could add them afterwards.
func TestKits_CreatePostsTheKitAndItsComponents(t *testing.T) {
	fake := &kitAPIFake{
		kits:  kitFixtureRows(),
		items: []map[string]any{{"id": "itm-c", "name": "Cyan cartridge", "sku": "CI-100"}},
	}
	srv := httptest.NewServer(fake.handler())
	defer srv.Close()

	root, _ := kitCreateSheet(t, fake, srv)
	root = key(t, root, tea.KeyMsg{Type: tea.KeyEnter})

	if !fake.sawRequest("POST /api/inventory/kits/") {
		t.Fatalf("the create never reached the kit endpoint: %v", fake.requestLog())
	}
	for _, got := range fake.requestLog() {
		if strings.HasPrefix(got, "POST /api/inventory/items/") {
			t.Fatalf("the create also posted an ordinary item: %v", fake.requestLog())
		}
	}

	fake.mu.Lock()
	defer fake.mu.Unlock()
	if len(fake.bodies) != 1 {
		t.Fatalf("bodies = %d, want exactly one create", len(fake.bodies))
	}
	body := fake.bodies[0]
	if body["name"] != "Ink kit" {
		t.Errorf("name = %v, want what was typed", body["name"])
	}
	if body["description"] != "CMYK set" {
		t.Errorf("description = %v, want what was typed", body["description"])
	}
	// `is_kit` is KitSerializer.create's own decision; sending it would be a
	// second copy of a decision already made upstream.
	if _, present := body["is_kit"]; present {
		t.Errorf("the payload asserted is_kit, which the serializer sets itself: %v", body)
	}
	rows, ok := body["components"].([]any)
	if !ok || len(rows) != 1 {
		t.Fatalf("components = %v, want the one component that was picked", body["components"])
	}
	row := rows[0].(map[string]any)
	if row["component"] != "itm-c" || row["quantity"].(float64) != 1 {
		t.Errorf("component row = %v, want the picked item at quantity 1", row)
	}
}

// TestKits_AnEmptyBillOfMaterialsIsTheServersRefusalToMake. The sheet does NOT
// pre-check that a kit has a component: the rule is the serializer's, its
// wording is the serializer's, and a copy here could disagree with it. What this
// pins is that the key is OMITTED rather than sent as `[]` — and that the
// request is made at all, so the refusal is one the operator can read.
func TestKits_AnEmptyBillOfMaterialsIsTheServersRefusalToMake(t *testing.T) {
	fake := &kitAPIFake{
		refuse: `{"error":{"code":"validation_failed",` +
			`"message":"One or more fields failed validation.",` +
			`"details":{"components":["A kit must contain at least one component."]}}}`,
	}
	srv := httptest.NewServer(fake.handler())
	defer srv.Close()

	root, deps := kitDriveRoot(t, fake, srv, func(d Deps) Screen { return NewKitFormScreen(d) })
	form := root.screen.(*InventoryItemFormScreen)
	form.deps = deps
	kitFormCursorTo(t, form, fName)
	kitDriveType(t, &root, "Ink kit")
	root = key(t, root, tea.KeyMsg{Type: tea.KeyEnter})

	fake.mu.Lock()
	bodies := append([]map[string]any(nil), fake.bodies...)
	fake.mu.Unlock()
	if len(bodies) != 1 {
		t.Fatalf("bodies = %d, want the create to have been attempted", len(bodies))
	}
	if _, present := bodies[0]["components"]; present {
		t.Errorf("an unopened bill of materials was sent as a value: %v", bodies[0]["components"])
	}
	if form.errMsg != "components: A kit must contain at least one component." {
		t.Errorf("errMsg = %q, want the server's own sentence about the server's own field", form.errMsg)
	}
}

// TestKits_ARefusedCreateShowsTheServersSentenceOnOneMarkedRow. The envelope's
// own `message` is "One or more fields failed validation." for every refusal the
// catalogue can make, so relaying it tells the operator nothing; the sentences
// are in `details`. This drives a REAL refusal through Root and reads the
// rendered pane, because a field assertion would pass while the row drew
// something else.
func TestKits_ARefusedCreateShowsTheServersSentenceOnOneMarkedRow(t *testing.T) {
	fake := &kitAPIFake{
		items: []map[string]any{{"id": "itm-c", "name": "Cyan cartridge", "sku": "CI-100"}},
		kits:  kitFixtureRows(),
		refuse: `{"error":{"code":"validation_failed",` +
			`"message":"One or more fields failed validation.",` +
			`"details":{"sku":["Inventory item with this SKU already exists."],` +
			`"supplier_terms":{"supplier":["Incorrect type. Expected pk value, received str."]}}}}`,
	}
	srv := httptest.NewServer(fake.handler())
	defer srv.Close()

	root, form := kitCreateSheet(t, fake, srv)
	root = key(t, root, tea.KeyMsg{Type: tea.KeyEnter})

	// The operator is still on the sheet they can fix: a refused create must not
	// navigate, or the typed work is gone with the reason.
	if _, ok := root.screen.(*InventoryItemFormScreen); !ok {
		t.Fatalf("a refused create left the sheet for %T", root.screen)
	}
	if form.saving {
		t.Error("the sheet is still marked saving after the refusal came back")
	}

	want := "sku: Inventory item with this SKU already exists. · " +
		"supplier_terms.supplier: Incorrect type. Expected pk value, received str."
	if form.errMsg != want {
		t.Errorf("errMsg = %q, want %q", form.errMsg, want)
	}
	if strings.Contains(form.errMsg, "One or more fields failed validation") {
		t.Error("the envelope's generic message reached the operator instead of the sentences")
	}

	// ONE marked line, bottom of the frame, left-justified. Read off the frame
	// the layer assembles rather than off the field, since the field says
	// nothing about how many rows it became.
	row := kitStatusRowOf(t, form)
	if !strings.HasPrefix(row, jdeStatusErrMark) {
		t.Errorf("the refusal row %q does not lead with the error mark %q", row, jdeStatusErrMark)
	}
	if strings.Contains(row, "\n") {
		t.Errorf("the refusal became more than one row: %q", row)
	}
	if !strings.Contains(row, "sku: Inventory item with this SKU") {
		t.Errorf("the refusal row %q does not carry the server's leading sentence", row)
	}
}

// kitStatusRowOf pulls the status row out of a rendered sheet: the last
// non-blank line above the action bar's rule.
func kitStatusRowOf(t *testing.T, s *InventoryItemFormScreen) string {
	t.Helper()
	body := s.statusRow(s.saving, "Saving…", s.errMsg)
	if body == "" {
		t.Fatal("the sheet drew no status row at all")
	}
	stripped := stripANSI(body)
	if !strings.Contains(stripANSI(s.View()), strings.TrimRight(stripped, " ")) {
		t.Fatalf("the status row %q is not on the sheet the operator sees", stripped)
	}
	return stripped
}

// TestKits_AnOrdinaryItemCreateIsUntouched. The routing above must not have
// moved every create onto the kit endpoint — an item sheet opened the ordinary
// way still posts an ordinary item, and carries no components key.
func TestKits_AnOrdinaryItemCreateIsUntouched(t *testing.T) {
	fake := &kitAPIFake{}
	srv := httptest.NewServer(fake.handler())
	defer srv.Close()

	deps := Deps{OMS: omsapi.New(srv.URL), Ctx: context.Background()}
	s := NewInventoryItemFormScreen(deps, "")
	s.loading = false
	s.inputs[fName].SetValue("A bolt")
	_, cmd := s.submit()
	if cmd == nil {
		t.Fatal("submit produced no command")
	}
	cmd()

	if !fake.sawRequest("POST /api/inventory/items/") {
		t.Fatalf("an ordinary item create did not reach /items/: %v", fake.requestLog())
	}
	for _, got := range fake.requestLog() {
		if strings.HasPrefix(got, "POST /api/inventory/kits/") {
			t.Fatalf("an ordinary item create reached the kit endpoint: %v", fake.requestLog())
		}
	}
}

// TestKits_BackingOutOfACreateReturnsToTheKitList. esc must not land on the item
// list, which is the one list in the app that can never show what was just
// abandoned — /items/ filters kits out server-side.
func TestKits_BackingOutOfACreateReturnsToTheKitList(t *testing.T) {
	s := NewKitFormScreen(Deps{})
	s.loading = false
	cmd := s.cancelCmd()
	if cmd == nil {
		t.Fatal("esc on a kit create issued no command")
	}
	msg, ok := cmd().(SwitchScreenMsg)
	if !ok {
		t.Fatalf("esc produced %T, want a screen switch", cmd())
	}
	list, ok := msg.Screen.(*ListScreen)
	if !ok || list.spec.kind != kitListKind {
		t.Fatalf("esc landed on %T (kind %q), want the kit list", msg.Screen, listKindOf(msg.Screen))
	}
}

func listKindOf(s Screen) string {
	if l, ok := s.(*ListScreen); ok {
		return l.spec.kind
	}
	return fmt.Sprintf("%T", s)
}

// ---------------------------------------------------------------------------
// Scanning
// ---------------------------------------------------------------------------

// TestKits_AScannedKitOpensItsDetail is the scanner half of reachability, and
// what it really pins is the TARGET TYPE: every arm of
// backend/scanner/resolvers.py that lands on an InventoryItem writes
// "inventory_item" and none writes "item", which is all this screen read — so
// every dispatcher-resolved item scan, kit or not, stopped at a status line
// naming the record it had just identified.
//
// A kit reaches this arm like any other item because NEITHER resolver filters
// on is_kit: the detail that opens already draws the bill of materials
// (inventory_detail_kit.go). There is deliberately no assertion here about a
// code resolving to the kit LIST — no endpoint does that, which is why the
// list's search box is where a typed or scanned vendor code goes instead.
//
// The payload is a SKU-shaped string rather than a UPC on purpose, and the
// reason OUTLIVED the defect it was first written about. That defect is now
// fixed — scanner.Classify used to claim every 8-to-20-character hex string as
// a ForgeKey badge, so an 8/12/13/14-digit UPC never reached the dispatcher at
// all (sc-classify-hex) — but a UPC here would still exercise the classifier
// rather than this arm, which is about the TARGET TYPE the reply carries. The
// barcode path has its own coverage in scan_barcode_test.go, where a failure
// names the classifier instead of implicating kits.
func TestKits_AScannedKitOpensItsDetail(t *testing.T) {
	var dispatched string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path != "/api/scanner/dispatch/" {
			// Everything the detail screen fetches once the scan lands. It has
			// to be served SOMETHING, and it must not overwrite the record of
			// what was dispatched — those requests carry no payload at all.
			_, _ = w.Write([]byte(`{"id":"kit-1","name":"Eufy printer maintenance kit"}`))
			return
		}
		raw, _ := io.ReadAll(r.Body)
		body := map[string]string{}
		_ = json.Unmarshal(raw, &body)
		dispatched = body["payload"]
		// The dispatcher's own shape, target_type included.
		_ = json.NewEncoder(w).Encode(map[string]any{
			"action":      "inventory_receive",
			"target_type": "inventory_item",
			"target_id":   "kit-1",
			"target_name": "Eufy printer maintenance kit",
			"target_url":  "/inventory/items/kit-1",
		})
	}))
	defer srv.Close()

	deps := Deps{OMS: omsapi.New(srv.URL), Ctx: context.Background()}
	scan := NewScanScreen(deps)
	root := newTestRoot(scan)
	root.deps = deps

	// A scanner is a keyboard burst plus Enter.
	kitDriveType(t, &root, "EIK-4")
	next, cmd := root.Update(tea.KeyMsg{Type: tea.KeyEnter})
	root = next.(Root)
	root = pump(t, root, cmd, 0)

	if dispatched != "EIK-4" {
		t.Fatalf("the scan payload the server saw was %q", dispatched)
	}
	detail, ok := root.screen.(*InventoryDetailScreen)
	if !ok {
		t.Fatalf("the scan left the operator on %T, want the item detail it had just identified", root.screen)
	}
	if detail.itemID != "kit-1" {
		t.Errorf("detail id = %q, want the scanned record", detail.itemID)
	}
}
