package tui

import (
	"context"
	"encoding/json"
	"fmt"
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

// po_create_picker_status_test.go — "the screen just hangs after I press enter",
// and the rule the fix generalises to.
//
// THE RULE: any operator action on the New PO screen and its pickers that
// performs work off the terminal must report that it is WORKING and must report
// FAILURE — and any key that declines to act must say why. A keystroke handled
// by doing nothing and saying nothing renders a screen byte-for-byte identical
// to the one before the press, which from the operator's seat is a wedged
// program. That is what the report was: nothing blocked, nothing in flight.
//
// These drive Root.Update against a stateful httptest fake, because no live OMS
// is reachable from a task worktree (AGENTS.md), and read the CLIPPED
// Root.View() at 80 columns, because that is what the terminal shows.

// poPickFake is a supplier whose catalog is served in PAGES, so a test can put
// an item past page one — the state in which the old picker answered a search
// for a real item with "No inventory items match".
type poPickFake struct {
	mu       sync.Mutex
	requests []string

	catalog  int // item-suppliers this supplier sells
	pageSize int
	assets   int
	// suppliers is how many suppliers the picker can choose between, so a test
	// can move the order off one supplier while its catalog is still in flight.
	suppliers int

	// reorder is how many rows the supplier's reorder queue holds, so the
	// third picker can be driven the same way the other two are.
	reorder int

	// The three OPTIONAL header lookups. Every source-chooser test ran with
	// these at zero — the fake fell through to an empty envelope — so the g / w
	// / c rows were never on the frame, and the four rows they cost were what
	// pushed the collapsed cart clean off an 18-row pane with nothing on it
	// saying a cart existed.
	agreements int
	workOrders int
	committees int

	failItems   bool
	failAssets  bool
	failReorder bool

	// failCreate answers the submit with a gateway page rather than JSON.
	// omsapi.parseError puts the ENTIRE raw body in APIError.Message when the
	// envelope carries no code, which is what makes the failure line on the
	// last step of this flow an unbounded string — the case the screen's own
	// error surface went four rounds without folding or budgeting.
	failCreate bool

	// OMS-supplied names long enough to exercise the 51-column pane. Every
	// other fixture uses "Acme Supply" and "Annual 1", which is why the
	// supplier header shipped unbounded: at those widths it never reached the
	// cut it was drawn past for a real supplier.
	supplierName  string
	agreementName string
}

// poGatewayHTML is the shape of body a proxy, WAF or Django debug page returns:
// several hundred characters, no JSON envelope, and newlines in it.
const poGatewayHTML = `<!DOCTYPE html>
<html><head><title>502 Bad Gateway</title></head>
<body><h1>502 Bad Gateway</h1><p>The upstream server did not respond to the ` +
	`request in time and the gateway gave up waiting on it. Retry the request ` +
	`or contact the administrator of the OpenMakerSuite deployment if this ` +
	`keeps happening for every order you try to submit.</p></body></html>`

// seen is every request the fake has served, so a test can assert what a
// lookup CARRIED and not merely that one happened.
func (f *poPickFake) seen() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.requests...)
}

func (f *poPickFake) hits(substr string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, r := range f.requests {
		if strings.Contains(r, substr) {
			n++
		}
	}
	return n
}

func (f *poPickFake) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.requests = append(f.requests, r.Method+" "+r.URL.Path+"?"+r.URL.RawQuery)
		f.mu.Unlock()

		page := 1
		if p := r.URL.Query().Get("page"); p != "" {
			fmt.Sscanf(p, "%d", &page)
		}
		size := f.pageSize
		if size <= 0 {
			size = 25
		}
		envelope := func(rows []map[string]any, total int) {
			var next any
			if page*size < total {
				next = fmt.Sprintf("http://oms.test/?page=%d", page+1)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"count": total, "next": next, "previous": nil, "results": rows,
			})
		}

		switch {
		case strings.Contains(r.URL.Path, "/reorder_data/"):
			if f.failReorder {
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte(`{"detail":"reorder data exploded"}`))
				return
			}
			items := []map[string]any{}
			for i := 0; i < f.reorder; i++ {
				items = append(items, map[string]any{
					"item_supplier_id":   i + 1,
					"item_name":          fmt.Sprintf("Bolt %d", i+1),
					"sku":                fmt.Sprintf("BLT-%03d", i+1),
					"suggested_quantity": 2,
					"unit_cost":          "1.50",
				})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"suppliers": []map[string]any{{"id": 1, "name": "Acme Supply", "items": items}},
			})
		case strings.Contains(r.URL.Path, "/item-suppliers/"):
			if f.failItems {
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte(`{"detail":"item-suppliers exploded"}`))
				return
			}
			rows := []map[string]any{}
			for i := (page - 1) * size; i < page*size && i < f.catalog; i++ {
				rows = append(rows, map[string]any{
					"id": i + 1, "item": fmt.Sprintf("it-%d", i+1),
					"item_name":    fmt.Sprintf("Widget %d", i+1),
					"supplier":     1,
					"supplier_sku": fmt.Sprintf("SKU-%03d", i+1),
					"unit_cost":    "3.50", "quantity_per_package": 1,
				})
			}
			envelope(rows, f.catalog)
		case strings.Contains(r.URL.Path, "/assets/"):
			if f.failAssets {
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte(`{"detail":"assets exploded"}`))
				return
			}
			search := strings.ToLower(r.URL.Query().Get("search"))
			rows := []map[string]any{}
			for i := 0; i < f.assets; i++ {
				name := fmt.Sprintf("Lathe %d", i+1)
				if search != "" && !strings.Contains(strings.ToLower(name), search) {
					continue
				}
				rows = append(rows, map[string]any{
					"id": fmt.Sprintf("as-%d", i+1), "name": name,
					"asset_tag": fmt.Sprintf("TAG-%03d", i+1),
				})
			}
			envelope(rows, len(rows))
		case strings.Contains(r.URL.Path, "/supplier-agreements/"):
			rows := []map[string]any{}
			for i := 0; i < f.agreements; i++ {
				name := fmt.Sprintf("Annual %d", i+1)
				if f.agreementName != "" {
					name = f.agreementName
				}
				rows = append(rows, map[string]any{
					"id": i + 1, "name": name, "supplier": 1,
				})
			}
			envelope(rows, len(rows))
		case strings.Contains(r.URL.Path, "/work-orders/"):
			rows := []map[string]any{}
			// ListActiveWorkOrders asks twice, once per status, so only the
			// first status answers or the picker would hold each job twice.
			if r.URL.Query().Get("status") == "open" {
				for i := 0; i < f.workOrders; i++ {
					rows = append(rows, map[string]any{
						"id": 1000 + i, "title": fmt.Sprintf("Lathe teardown %d", i+1),
					})
				}
			}
			envelope(rows, len(rows))
		case strings.Contains(r.URL.Path, "/sigs/"):
			rows := []map[string]any{}
			for i := 0; i < f.committees; i++ {
				rows = append(rows, map[string]any{
					"id": 50 + i, "name": fmt.Sprintf("Shop %d", i+1),
				})
			}
			envelope(rows, len(rows))
		case strings.Contains(r.URL.Path, "/purchase-orders/"):
			if f.failCreate {
				w.WriteHeader(http.StatusBadGateway)
				_, _ = w.Write([]byte(poGatewayHTML))
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"id": 900, "po_number": "PO-900"})
		case strings.Contains(r.URL.Path, "/suppliers/"):
			n := f.suppliers
			if n <= 0 {
				n = 1
			}
			first := "Acme Supply"
			if f.supplierName != "" {
				first = f.supplierName
			}
			rows := []map[string]any{{"id": 1, "name": first}}
			for i := 2; i <= n; i++ {
				rows = append(rows, map[string]any{"id": i, "name": fmt.Sprintf("Supplier %d", i)})
			}
			envelope(rows, len(rows))
		default:
			envelope(nil, 0)
		}
	}
}

// poPickerAt opens the New PO screen at the source chooser with a supplier
// committed, sized to `width`, and returns the Root plus the screen.
func poPickerAt(t *testing.T, fake *poPickFake, width int) (Root, *PurchaseOrderCreateScreen) {
	t.Helper()
	return poPickerAtSize(t, fake, width, 30)
}

// poPickerAtSize is the same fixture at an explicit terminal height, so a
// journey can be replayed on the tight pane as well as the roomy one.
func poPickerAtSize(t *testing.T, fake *poPickFake, width, height int) (Root, *PurchaseOrderCreateScreen) {
	t.Helper()
	srv := httptest.NewServer(fake.handler())
	t.Cleanup(srv.Close)

	deps := Deps{OMS: omsapi.New(srv.URL), Ctx: context.Background()}
	screen := NewPurchaseOrderCreateScreen(deps)
	r := newTestRoot(screen)
	r.deps = deps
	next, _ := r.Update(tea.WindowSizeMsg{Width: width, Height: height})
	r = next.(Root)
	r = pump(t, r, screen.Init(), 0)
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter}) // commit the only supplier
	return r, screen
}

// poType feeds a string one rune at a time, the way a keyboard delivers it.
//
// Straight through Root.Update rather than through key(): the only command a
// rune into a textinput produces is the cursor blink, which pump() waits out a
// fifth of a second at a time, and the client-side filter these tests are about
// is applied synchronously inside Update.
func poType(t *testing.T, r Root, text string) Root {
	t.Helper()
	for _, ch := range text {
		next, _ := r.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{ch}})
		after, ok := next.(Root)
		if !ok {
			t.Fatalf("Root.Update returned %T, want Root", next)
		}
		r = after
	}
	return r
}

// TestPOItemPicker_SearchThenEnterPicksTheItem is the report, from the seat:
// open a draft order, go to "Items sold by this supplier", press /, type a
// search, press Enter on the result.
//
// It used to take TWO enters and say nothing in between. The first only set
// typing=false — the filter had already been applied on the keystroke before —
// so the redraw was identical to what was on screen: same rows, same caret in
// the search box. Nothing named a second enter, so the operator read the picker
// as hung and reported it as one.
func TestPOItemPicker_SearchThenEnterPicksTheItem(t *testing.T) {
	fake := &poPickFake{catalog: 12, pageSize: 5}
	r, screen := poPickerAt(t, fake, 80)

	r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("i")})
	if screen.phase != poPhaseItemPick {
		t.Fatalf("phase = %v, want the inventory-item picker", screen.phase)
	}

	before := r.View()
	r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	r = poType(t, r, "Widget 7")
	if r.View() == before {
		t.Error("typing a search changed nothing on screen")
	}

	// ONE enter.
	afterTyping := r.View()
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})

	if screen.phase != poPhaseLine {
		t.Fatalf("one enter left the picker in phase %v — the pick still needs a second press", screen.phase)
	}
	if r.View() == afterTyping {
		t.Error("enter redrew a byte-for-byte identical screen — this is the hang")
	}
	out := r.View()
	if !strings.Contains(out, "Widget 7") {
		t.Errorf("the line form does not name the item that was picked:\n%s", out)
	}
	if !strings.Contains(out, "picked Widget 7") {
		t.Errorf("nothing on screen says what enter did:\n%s", out)
	}
}

// TestPOItemPicker_ScannerBurstPicksInOnePress: a barcode scanner is a keyboard
// that delivers a burst plus Enter (AGENTS.md). A scan into the search box that
// resolves to exactly one item therefore has to be one press, not two.
func TestPOItemPicker_ScannerBurstPicksInOnePress(t *testing.T) {
	fake := &poPickFake{catalog: 40, pageSize: 5}
	r, screen := poPickerAt(t, fake, 80)
	r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("i")})
	r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})

	// A scanner sends the SKU as one burst of runes, then Enter.
	r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("SKU-033")})
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})

	if screen.phase != poPhaseLine {
		t.Fatalf("a scan that matched one item left the picker in phase %v", screen.phase)
	}
	if got := screen.lineInputs[poLineFieldDesc].Value(); got != "Widget 33" {
		t.Errorf("the scan staged %q, want Widget 33", got)
	}
}

// TestPOItemPicker_AmbiguousSearchDoesNotGuess: several matches must NOT stage
// one of them — a wrong line on a purchase order is worse than a second
// keypress. But the screen has to visibly move and name the next key, which is
// the whole difference from the silence that was reported.
func TestPOItemPicker_AmbiguousSearchDoesNotGuess(t *testing.T) {
	fake := &poPickFake{catalog: 12, pageSize: 5}
	r, screen := poPickerAt(t, fake, 80)
	r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("i")})
	r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	r = poType(t, r, "Widget 1") // Widget 1, 10, 11, 12

	before := r.View()
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})

	if screen.phase != poPhaseItemPick {
		t.Fatalf("an ambiguous search staged a line anyway (phase %v)", screen.phase)
	}
	if len(screen.lines) != 0 {
		t.Fatalf("an ambiguous search put %d line(s) in the cart", len(screen.lines))
	}
	out := r.View()
	if out == before {
		t.Error("enter over an ambiguous search redrew an identical screen")
	}
	if !strings.Contains(out, "4 of 12 match") {
		t.Errorf("the screen does not say how many matched:\n%s", out)
	}
	if !strings.Contains(out, "enter picks") {
		t.Errorf("the screen does not name the key that finishes the job:\n%s", out)
	}
	// And that second enter does finish it.
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})
	if screen.phase != poPhaseLine {
		t.Fatalf("the key the screen named did not work (phase %v)", screen.phase)
	}
}

// TestPOItemPicker_NoMatchIsNeverSilent is the state that was a genuine dead
// end. Enter over an empty filtered list returned nil — forever, from any
// number of presses — and the body said "No inventory items match.", a sentence
// that names no key and is equally true of a supplier with no catalog at all.
func TestPOItemPicker_NoMatchIsNeverSilent(t *testing.T) {
	fake := &poPickFake{catalog: 12, pageSize: 5}
	r, screen := poPickerAt(t, fake, 80)
	r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("i")})
	r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	r = poType(t, r, "flux capacitor")

	for i := 0; i < 3; i++ {
		r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})
	}
	if screen.phase != poPhaseItemPick {
		t.Fatalf("a search that matched nothing staged something (phase %v)", screen.phase)
	}

	out := r.View()
	// At 80 columns the pane is 51 wide and Root.View() TRUNCATES, so each of
	// these has to be a whole line or it is not on the operator's screen.
	for _, want := range []string{
		`no match for "flux capacitor"`, // what was searched for
		"12 in catalog",                 // and against what
		"edit the search",               // and the way out
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the 80-column render is missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "No inventory items match.") {
		t.Errorf("the old sentence that named no key is still here:\n%s", out)
	}
	// The search box stays open and focused so the query can be fixed in place.
	if !screen.itemSuppliersTyping {
		t.Error("a no-match enter closed the search box the operator has to edit")
	}
}

// TestPOItemPicker_LoadsTheWholeCatalogNotJustPageOne: the picker filters
// client-side, so the page count is a CORRECTNESS property. It used to fetch
// page one and drop the envelope's `next`; every item past it was invisible to
// the search, and the screen reported that as "No inventory items match" — a
// different and false statement, with no key on the screen able to reach the
// item.
func TestPOItemPicker_LoadsTheWholeCatalogNotJustPageOne(t *testing.T) {
	fake := &poPickFake{catalog: 40, pageSize: 5} // 8 pages
	r, screen := poPickerAt(t, fake, 80)
	r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("i")})

	if got := len(screen.itemSuppliersAll); got != 40 {
		t.Fatalf("loaded %d catalog items, want all 40 — the picker is still page-one only", got)
	}
	if n := fake.hits("/item-suppliers/"); n < 8 {
		t.Errorf("only %d request(s) went out for an 8-page catalog", n)
	}

	// An item on the LAST page is findable and pickable.
	r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	r = poType(t, r, "Widget 38")
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})
	if screen.phase != poPhaseLine {
		t.Fatalf("an item on page 8 could not be picked (phase %v)", screen.phase)
	}
	if got := screen.lineInputs[poLineFieldDesc].Value(); got != "Widget 38" {
		t.Errorf("staged %q, want Widget 38", got)
	}
}

// TestPOItemPicker_SaysWhatItIsWorkingOn: while the request is out, the screen
// names the WORK — not a spinner, and not the word "Loading" over a rectangle.
func TestPOItemPicker_SaysWhatItIsWorkingOn(t *testing.T) {
	fake := &poPickFake{catalog: 12, pageSize: 5}
	r, screen := poPickerAt(t, fake, 80)

	// Enter the picker WITHOUT pumping the load, so the in-flight frame is
	// the one under test.
	next, _ := r.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("i")})
	r = next.(Root)
	if !screen.itemSuppliersLoad {
		t.Fatal("entering the picker did not mark a load in flight")
	}
	out := r.View()
	if !strings.Contains(out, "Looking up the items Acme Supply sells") {
		t.Errorf("the in-flight frame does not name the work at 80 columns:\n%s", out)
	}

	// And once it lands, it says what came back.
	r = pump(t, r, screen.loadItemSuppliersForSupplier(), 0)
	if out := r.View(); !strings.Contains(out, "12 catalog item(s) loaded") {
		t.Errorf("the loaded frame does not report the result:\n%s", out)
	}
}

// TestPOItemPicker_FailureSaysSoAndLeavesAWayOut: silence on error is the
// defect even when the underlying call would eventually have succeeded.
func TestPOItemPicker_FailureSaysSoAndLeavesAWayOut(t *testing.T) {
	fake := &poPickFake{catalog: 12, pageSize: 5, failItems: true}
	r, screen := poPickerAt(t, fake, 80)
	r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("i")})

	out := r.View()
	if !strings.Contains(out, "looking up this supplier's items failed") {
		t.Errorf("a failed lookup does not say so at 80 columns:\n%s", out)
	}
	if !strings.Contains(out, "b picks another line source") {
		t.Errorf("a failed lookup leaves the operator nowhere to act:\n%s", out)
	}

	// And the operator can actually act: b is named, so b must work.
	r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("b")})
	if screen.phase != poPhaseSource {
		t.Fatalf("the key the error frame names did not work (phase %v)", screen.phase)
	}
}

// TestPOItemPicker_RetryAfterAFailureShowsTheList: renderItemPick shows the
// error INSTEAD of the list, so a stale error string left behind by a fixed
// request would hide a load that worked — a second, quieter way for the screen
// to stop telling the truth.
func TestPOItemPicker_RetryAfterAFailureShowsTheList(t *testing.T) {
	fake := &poPickFake{catalog: 12, pageSize: 5, failItems: true}
	r, screen := poPickerAt(t, fake, 80)
	r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("i")})
	if screen.itemSuppliersErr == "" {
		t.Fatal("the failing fixture did not fail")
	}

	fake.mu.Lock()
	fake.failItems = false
	fake.mu.Unlock()

	r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("b")})
	r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("i")})

	if screen.itemSuppliersErr != "" {
		t.Errorf("the previous failure survived a successful load: %q", screen.itemSuppliersErr)
	}
	if out := r.View(); !strings.Contains(out, "Widget 1") {
		t.Errorf("the recovered list is not on screen:\n%s", out)
	}
}

// TestPOAssetPicker_SearchSaysItIsSearchingThenSaysWhatItFound: unlike the item
// picker this really does go off the terminal on enter, so the note has to be
// posted BEFORE the request leaves and repainted with the result.
func TestPOAssetPicker_SearchSaysItIsSearchingThenSaysWhatItFound(t *testing.T) {
	fake := &poPickFake{assets: 3}
	r, screen := poPickerAt(t, fake, 80)
	r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
	r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	r = poType(t, r, "Lathe 2")

	// The in-flight frame, before the reply is pumped.
	next, cmd := r.Update(tea.KeyMsg{Type: tea.KeyEnter})
	r = next.(Root)
	if !screen.assetsLoading {
		t.Fatal("enter in the asset search did not mark a request in flight")
	}
	if out := r.View(); !strings.Contains(out, "Looking up the assets Acme Supply supplied") {
		t.Errorf("the in-flight frame does not name the work:\n%s", out)
	}
	r = pump(t, r, cmd, 0)

	if out := r.View(); !strings.Contains(out, "1 asset(s)") {
		t.Errorf("the finished search does not report what it found:\n%s", out)
	}
}

// TestPOAssetPicker_NoMatchAndEmptyEnterAreNeverSilent — the same dead end the
// item picker had, on the picker next door.
func TestPOAssetPicker_NoMatchAndEmptyEnterAreNeverSilent(t *testing.T) {
	fake := &poPickFake{assets: 3}
	r, screen := poPickerAt(t, fake, 80)
	r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
	r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	r = poType(t, r, "hovercraft")
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})

	out := r.View()
	if !strings.Contains(out, `no asset matches "hovercraft"`) {
		t.Errorf("a search that found nothing does not say so:\n%s", out)
	}
	if strings.Contains(out, "No assets match.") {
		t.Errorf("the old sentence that named no key is still here:\n%s", out)
	}

	// Enter over the now-empty list must answer too.
	before := r.View()
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})
	if screen.phase != poPhaseAssetPick {
		t.Fatalf("enter over an empty asset list staged something (phase %v)", screen.phase)
	}
	if out := r.View(); out == before && !strings.Contains(out, "no asset matches") {
		t.Errorf("enter over an empty asset list said nothing:\n%s", out)
	}
}

// TestPOAssetPicker_PagingEdgesAnswer: `]` and `[` are named only alongside a
// page that exists, but the arms still have to answer rather than swallow the
// press.
func TestPOAssetPicker_PagingEdgesAnswer(t *testing.T) {
	fake := &poPickFake{assets: 3}
	r, screen := poPickerAt(t, fake, 80)
	r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})

	for _, tc := range []struct{ key, want string }{
		{"[", "already on the first page"},
		{"]", "already on the last page"},
	} {
		_, cmd := screen.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(tc.key)})
		if cmd == nil {
			t.Fatalf("%q at the edge of the pager said nothing", tc.key)
		}
		msg, ok := cmd().(StatusMsg)
		if !ok {
			t.Fatalf("%q produced %T, want a status", tc.key, cmd())
		}
		if !strings.Contains(msg.Text, tc.want) {
			t.Errorf("%q said %q, want it to mention %q", tc.key, msg.Text, tc.want)
		}
	}
	_ = r
}

// TestPOReorderPicker_EmptyEnterIsNeverSilent — the third picker, same rule.
func TestPOReorderPicker_EmptyEnterIsNeverSilent(t *testing.T) {
	for _, h := range poPaneSizes {
		s := reorderScreen()
		s.phase = poPhaseReorderPick
		s.terminalHeight = h

		before := strings.Join(poPaneLinesAt(t, s, h), "\n")
		_, cmd := s.Update(tea.KeyMsg{Type: tea.KeyEnter})
		if cmd == nil {
			t.Fatalf("80x%d: enter over an empty reorder queue said nothing", h)
		}
		after := strings.Join(poPaneLinesAt(t, s, h), "\n")

		// The BODY, not the flash. This frame's "Nothing flagged…" line was
		// already on the pane, so a Status alone left it byte-for-byte what it
		// was and expired four seconds later with nothing recording the press.
		if before == after {
			t.Errorf("80x%d: enter redrew an identical pane — only the flash moved:\n%s", h, after)
		}
		if !poPaneHasLine(t, s, "nothing to pick") {
			t.Errorf("80x%d: the pane does not say what enter did:\n%s", h, after)
		}
		poAssertFits(t, fmt.Sprintf("empty reorder queue at 80x%d", h), s)
	}
}

// TestPOCreate_EveryPickerKeyThatDeclinesToActSaysWhy is the rule, as a rule.
//
// It walks the states in which a picker key CANNOT do the thing it normally
// does — an empty list, a search that matched nothing, either edge of the
// pager — and requires an answer from every one of them. Before the fix each of
// these was `return s, nil`: the identical redraw the report called a hang.
func TestPOCreate_EveryPickerKeyThatDeclinesToActSaysWhy(t *testing.T) {
	cases := []struct {
		name  string
		setup func(*PurchaseOrderCreateScreen)
		key   tea.KeyMsg
	}{
		{"item picker, nothing loaded", func(s *PurchaseOrderCreateScreen) {
			s.phase = poPhaseItemPick
		}, tea.KeyMsg{Type: tea.KeyEnter}},
		{"item picker, search matched nothing", func(s *PurchaseOrderCreateScreen) {
			s.phase = poPhaseItemPick
			s.itemSuppliersAll = []omsapi.ItemSupplier{{ID: 1, ItemName: "Widget"}}
			s.itemSuppliersSearch.SetValue("nope")
			s.applyItemSupplierFilter()
		}, tea.KeyMsg{Type: tea.KeyEnter}},
		{"item search box, matched nothing", func(s *PurchaseOrderCreateScreen) {
			s.phase = poPhaseItemPick
			s.itemSuppliersTyping = true
			s.itemSuppliersAll = []omsapi.ItemSupplier{{ID: 1, ItemName: "Widget"}}
			s.itemSuppliersSearch.SetValue("nope")
			s.applyItemSupplierFilter()
		}, tea.KeyMsg{Type: tea.KeyEnter}},
		{"asset picker, nothing loaded", func(s *PurchaseOrderCreateScreen) {
			s.phase = poPhaseAssetPick
		}, tea.KeyMsg{Type: tea.KeyEnter}},
		{"asset picker, first page", func(s *PurchaseOrderCreateScreen) {
			s.phase = poPhaseAssetPick
			s.assetsPage = 1
		}, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("[")}},
		{"asset picker, last page", func(s *PurchaseOrderCreateScreen) {
			s.phase = poPhaseAssetPick
			s.assetsPage = 1
			s.assetsHasNext = false
		}, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("]")}},
		{"reorder picker, nothing flagged", func(s *PurchaseOrderCreateScreen) {
			s.phase = poPhaseReorderPick
		}, tea.KeyMsg{Type: tea.KeyEnter}},
		{"reorder picker, add-all with nothing to add", func(s *PurchaseOrderCreateScreen) {
			s.phase = poPhaseReorderPick
		}, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")}},
		// The cursor keys and the mark key sit in the same switch as the arms
		// above and were the ones still answering with nil: a list that is
		// DRAWN and empty is past the !listOnScreen() gate, and comparing a
		// cursor against len-1 there does nothing and says nothing.
		{"reorder picker, mark with nothing flagged", func(s *PurchaseOrderCreateScreen) {
			s.phase = poPhaseReorderPick
		}, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(" ")}},
		{"reorder picker, move with nothing flagged", func(s *PurchaseOrderCreateScreen) {
			s.phase = poPhaseReorderPick
		}, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")}},
		{"item picker, move over a list that matched nothing", func(s *PurchaseOrderCreateScreen) {
			s.phase = poPhaseItemPick
			s.itemSuppliersAll = []omsapi.ItemSupplier{{ID: 1, ItemName: "Widget"}}
			s.itemSuppliersFor = s.supplierID
			s.itemSuppliersSearch.SetValue("nope")
			s.applyItemSupplierFilter()
		}, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("k")}},
		{"asset picker, move over a search that matched nothing", func(s *PurchaseOrderCreateScreen) {
			s.phase = poPhaseAssetPick
			s.assetsQuery = "hovercraft"
		}, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, h := range poPaneSizes {
				s := NewPurchaseOrderCreateScreen(Deps{})
				s.supplierID = 1
				s.terminalHeight = h
				tc.setup(s)

				before := strings.Join(poPaneLinesAt(t, s, h), "\n")
				_, cmd := s.Update(tc.key)
				if cmd == nil {
					t.Fatalf("80x%d: the key was answered with nothing at all — this is the hang", h)
				}
				after := strings.Join(poPaneLinesAt(t, s, h), "\n")

				// A StatusMsg is NOT the standard. StatusBar.Flash expires
				// after four seconds and the operator who saw nothing is still
				// looking at the picker, so the BODY has to carry the answer.
				if before == after {
					t.Errorf("80x%d: the key redrew an identical pane:\n%s", h, after)
				}
				poAssertFits(t, fmt.Sprintf("%s at 80x%d", tc.name, h), s)
			}
		})
	}
}

// TestPOCreate_PendingHeaderLookupsSayTheyArePending: the agreement and
// work-order/committee lists load in the background and gate nothing, which is
// why their keys only appear once there is something to pick. But "no row yet"
// and "this supplier has no agreements" render identically, and only the second
// is a conclusion an operator may act on.
func TestPOCreate_PendingHeaderLookupsSayTheyArePending(t *testing.T) {
	s := NewPurchaseOrderCreateScreen(Deps{})
	s.supplierID = 1
	s.phase = poPhaseSource
	s.agreementLoading = true
	s.assoc.workOrderLoad = true
	s.assoc.committeeLoad = true

	r := newTestRoot(s)
	next, _ := r.Update(tea.WindowSizeMsg{Width: 80, Height: 30})
	r = next.(Root)

	out := r.View()
	if !strings.Contains(out, "still looking up") {
		t.Errorf("the source chooser does not say the header lookups are in flight:\n%s", out)
	}
	// And it must NOT name g/w/c while they are, because those keys do nothing
	// yet — the bar may only name keys that work.
	if strings.Contains(s.helpText(), "g agreement") {
		t.Error("the bar names g while the agreement list is still loading")
	}
}

// ---------------------------------------------------------------------------
// The pane is 51 columns and Root.View() TRUNCATES
// ---------------------------------------------------------------------------

// poPaneLines is the picker frame as the CONTENT PANE actually shows it: the
// screen's own body clipped to screenBodyWidth(80), which is exactly what
// Root.View() does to it (app.go clamps the joined content before rendering).
//
// The distinction matters and is why the first round of these tests passed over
// a broken screen. Root.View() also paints an 80-column status bar underneath
// the pane, so a strings.Contains over the whole frame can be satisfied by the
// four-second Flash while the DURABLE body line — the one the operator who
// pressed enter and saw nothing is still reading a minute later — has had the
// key it names cut off the right-hand edge.
// poPaneSizes is every terminal height these frames are driven at. 24 is the
// classic terminal and the tighter of the two; a frame checked only at the
// roomier one is a frame whose bottom nobody has looked at.
var poPaneSizes = []int{24, 30}

// poFrameHeight is the terminal height the frame under test was actually sized
// to, so an assertion clips it exactly as Root would. Reading it off the screen
// rather than taking a constant is what lets one set of helpers serve a journey
// driven at 24 and the same journey driven at 30.
func poFrameHeight(s Screen) int {
	switch v := s.(type) {
	case *PurchaseOrderCreateScreen:
		if v.terminalHeight > 0 {
			return v.terminalHeight
		}
	case poFrameScreen:
		if v.height > 0 {
			return v.height
		}
	}
	return poPaneSizes[len(poPaneSizes)-1]
}

func poPaneLinesAt(t *testing.T, s Screen, termHeight int) []string {
	t.Helper()
	return strings.Split(
		clampToBox(s.View(), screenBodyWidth(80), screenBodyHeight(termHeight)), "\n")
}

// poPaneLines is the pane as the terminal this frame was sized for shows it.
func poPaneLines(t *testing.T, s Screen) []string {
	t.Helper()
	return poPaneLinesAt(t, s, poFrameHeight(s))
}

// poPaneHasLine reports whether some WHOLE clipped line contains want.
func poPaneHasLine(t *testing.T, s Screen, want string) bool {
	t.Helper()
	for _, line := range poPaneLines(t, s) {
		if strings.Contains(line, want) {
			return true
		}
	}
	return false
}

// poWantPaneLine fails unless want survives the clip on one line of the pane.
func poWantPaneLine(t *testing.T, s Screen, want string) {
	t.Helper()
	if !poPaneHasLine(t, s, want) {
		t.Errorf("the pane at 80x%d does not carry %q on any whole line:\n%s",
			poFrameHeight(s), want, strings.Join(poPaneLines(t, s), "\n"))
	}
}

// poRejectPaneLine fails if want appears anywhere in the pane.
func poRejectPaneLine(t *testing.T, s Screen, want string) {
	t.Helper()
	if poPaneHasLine(t, s, want) {
		t.Errorf("the pane at 80x%d still says %q:\n%s",
			poFrameHeight(s), want, strings.Join(poPaneLines(t, s), "\n"))
	}
}

// poAssertFits fails for anything the operator cannot see: a line the
// 51-column pane cuts, or a ROW the pane's height drops.
//
// Both axes, because they fail independently and this project has now shipped
// each of them in turn — folding the bars to survive the width cut is what
// spent the rows that then fell off the bottom. A row dropped from a PICKER is
// worse than a dropped hint: the cursor can still be moved onto it and enter
// still stages it, so the operator puts an item on a purchase order they cannot
// see.
func poAssertFits(t *testing.T, what string, s Screen) {
	t.Helper()
	lines := strings.Split(strings.TrimSuffix(s.View(), "\n"), "\n")
	budget := screenBodyWidth(80)
	for i, line := range lines {
		if w := lipgloss.Width(line); w > budget {
			t.Errorf("%s: line %d is %d cells and is cut at %d, losing %q\n\tfull line: %q",
				what, i+1, w, budget, string([]rune(line)[budget:]), line)
		}
	}
	h := poFrameHeight(s)
	if rows := screenBodyHeight(h); len(lines) > rows {
		t.Errorf("%s: the frame is %d rows and the pane at 80x%d keeps %d, dropping %q",
			what, len(lines), h, rows, strings.Join(lines[rows:], " / "))
	}
}

// poFrameScreen adapts a pre-rendered frame to the Screen the pane helpers
// take, for the table cases that build a frame directly from one renderer.
type poFrameScreen struct {
	frame  string
	height int
}

func (f poFrameScreen) Init() tea.Cmd                    { return nil }
func (f poFrameScreen) Update(tea.Msg) (Screen, tea.Cmd) { return f, nil }
func (f poFrameScreen) View() string                     { return f.frame }
func (f poFrameScreen) Title() string                    { return "frame" }

// poWidestSupplierScreen is a create screen whose supplier name is at the
// 20-character clip supplierLabel allows. Every "Looking up what <supplier>…"
// line has to survive THAT, not the short name the other fixtures use — the
// reorder working line was 52 cells for an eleven-character supplier.
func poWidestSupplierScreen() *PurchaseOrderCreateScreen {
	s := NewPurchaseOrderCreateScreen(Deps{})
	s.suppliers = []omsapi.Supplier{{ID: 1, Name: "Northern Tool & Die Supply Co"}}
	s.supplierID = 1
	s.terminalHeight = poPaneSizes[0]
	return s
}

// TestPOPickerFrames_NeverLoseTheKeyTheyName walks every frame the three
// pickers can draw and fails on any line the 51-column pane would truncate.
//
// This is the test the width defect needed and did not have. A hint the
// operator cannot finish reading is worse than no hint, because they believe
// they read it — and the part that goes over the edge is always the tail, which
// is where these lines name the key that gets the operator out.
func TestPOPickerFrames_NeverLoseTheKeyTheyName(t *testing.T) {
	// An OMS error string is NOT bounded: omsapi.parseError puts the whole raw
	// response body in APIError.Message when the JSON envelope carries no code,
	// so a gateway or Django-debug page arrives multi-KB and pickerWords folds an
	// unspaced blob at one line per 47 cells. 200 characters fitted an 80x24 pane
	// and hid the defect; this does not.
	long := strings.Repeat("x", 900)

	cases := []struct {
		name  string
		setup func(*PurchaseOrderCreateScreen)
		phase poPhase
		// want is a phrase that has to survive on one line of the frame, not
		// merely somewhere in the string.
		want []string
	}{
		{"reorder, working", func(s *PurchaseOrderCreateScreen) {
			s.reorderLoading = true
		}, poPhaseReorderPick, []string{"reorder"}},

		{"reorder, failed", func(s *PurchaseOrderCreateScreen) {
			s.reorderLoadErr = long
		}, poPhaseReorderPick,
			[]string{"reading the reorder queue failed", "b picks another line source", "esc cancels the order"}},

		{"reorder, empty", func(s *PurchaseOrderCreateScreen) {},
			poPhaseReorderPick,
			[]string{"b picks another line source", "esc cancels the order"}},

		{"reorder, list", func(s *PurchaseOrderCreateScreen) {
			s.reorderItems = []omsapi.ReorderDataItem{
				reorderItem("Bolt M8", 11, 4, ""),
				reorderItem("Bolt M10", 12, 6, ""),
			}
		}, poPhaseReorderPick, []string{"in the queue"}},

		{"items, working", func(s *PurchaseOrderCreateScreen) {
			s.itemSuppliersLoad = true
		}, poPhaseItemPick, []string{"Looking up the items"}},

		{"items, failed", func(s *PurchaseOrderCreateScreen) {
			s.itemSuppliersErr = long
		}, poPhaseItemPick,
			[]string{"looking up this supplier's items failed", "r retries the lookup",
				"b picks another line source", "esc cancels the order"}},

		{"items, failed while the search box is open", func(s *PurchaseOrderCreateScreen) {
			s.itemSuppliersErr = long
			s.itemSuppliersTyping = true
		}, poPhaseItemPick,
			[]string{"looking up this supplier's items failed", "esc closes the search"}},

		{"items, empty catalog", func(s *PurchaseOrderCreateScreen) {
			s.itemSuppliersNote = pickerNote{s.noCatalogSentence(), StatusWarn}
		}, poPhaseItemPick,
			[]string{"has no active catalog items", "b picks another line source", "esc cancels the order"}},

		{"items, no note at all", func(s *PurchaseOrderCreateScreen) {},
			poPhaseItemPick,
			[]string{"has no active catalog items", "b picks another line source"}},

		{"items, search matched nothing", func(s *PurchaseOrderCreateScreen) {
			s.itemSuppliersAll = poCatalog(400)
			s.itemSuppliersTyping = true
			s.itemSuppliersSearch.SetValue("a search nobody would type")
			s.applyItemSupplierFilter()
			s.itemSuppliersNote = itemFilterNote(s.itemSuppliersSearch.Value(), 0, 400, "", true)
		}, poPhaseItemPick,
			[]string{"no match for", "in catalog", "edit the search"}},

		{"items, several matched, after esc closed the box", func(s *PurchaseOrderCreateScreen) {
			s.itemSuppliersAll = poCatalog(400)
			s.itemSuppliersSearch.SetValue("Widget 1")
			s.applyItemSupplierFilter()
			s.itemSuppliersNote = itemFilterNote("Widget 1", len(s.itemSuppliers), 400, "search closed", false)
		}, poPhaseItemPick,
			[]string{"search closed", "match", "j/k choose", "enter picks"}},

		{"items, exactly one matched, after esc closed the box", func(s *PurchaseOrderCreateScreen) {
			s.itemSuppliersAll = poCatalog(400)
			s.itemSuppliersSearch.SetValue("Widget 137")
			s.applyItemSupplierFilter()
			s.itemSuppliersNote = itemFilterNote("Widget 137", 1, 400, "search closed", false)
		}, poPhaseItemPick,
			[]string{"search closed", "enter picks it"}},

		{"items, unfiltered count", func(s *PurchaseOrderCreateScreen) {
			s.itemSuppliersAll = poCatalog(400)
			s.applyItemSupplierFilter()
			s.itemSuppliersNote = itemFilterNote("", 400, 400, "search closed", false)
		}, poPhaseItemPick,
			[]string{"search closed", "enter picks the highlighted row"}},

		{"assets, working", func(s *PurchaseOrderCreateScreen) {
			s.assetsLoading = true
		}, poPhaseAssetPick, []string{"Looking up the assets"}},

		{"assets, failed", func(s *PurchaseOrderCreateScreen) {
			s.assetsErr = long
		}, poPhaseAssetPick,
			[]string{"looking up this supplier's assets failed", "/ retries with a search",
				"b picks another line source", "esc cancels the order"}},

		{"assets, failed while the search box is open", func(s *PurchaseOrderCreateScreen) {
			s.assetsErr = long
			s.assetsTyping = true
		}, poPhaseAssetPick,
			[]string{"looking up this supplier's assets failed", "esc closes the search"}},

		{"assets, empty", func(s *PurchaseOrderCreateScreen) {
			s.assetsNote = pickerNote{"this supplier has no assets on file", StatusWarn}
		}, poPhaseAssetPick,
			[]string{"no assets on file", "/ searches", "b picks another line source", "esc cancels the order"}},

		{"items, a list longer than the pane", func(s *PurchaseOrderCreateScreen) {
			s.itemSuppliersAll = poCatalog(400)
			s.itemSuppliersFor = s.supplierID
			s.applyItemSupplierFilter()
			s.itemSuppliersCur = len(s.itemSuppliers) - 1
			s.itemSuppliersNote = itemFilterNote("", 400, 400, "search closed", false)
		}, poPhaseItemPick,
			[]string{"more above", "Widget 400"}},

		{"items, mid-reload over rows already held", func(s *PurchaseOrderCreateScreen) {
			s.itemSuppliersAll = poCatalog(400)
			s.itemSuppliersFor = s.supplierID
			s.itemSuppliersLoad = true
			s.applyItemSupplierFilter()
		}, poPhaseItemPick, []string{"Reloading the items"}},

		{"reorder, a queue longer than the pane", func(s *PurchaseOrderCreateScreen) {
			for i := 0; i < 40; i++ {
				s.reorderItems = append(s.reorderItems, reorderItem(fmt.Sprintf("Bolt %d", i+1), 100+i, 2, ""))
			}
			s.reorderCursor = 39
		}, poPhaseReorderPick,
			[]string{"more above", "Bolt 40", "in the queue"}},

		{"assets, a list longer than the pane with a pager", func(s *PurchaseOrderCreateScreen) {
			for i := 0; i < 40; i++ {
				s.assets = append(s.assets, omsapi.Asset{
					ID: fmt.Sprintf("as-%d", i+1), Name: fmt.Sprintf("Lathe %d", i+1)})
			}
			s.assetsCursor = 39
			s.assetsPage = 2
			s.assetsHasNext = true
		}, poPhaseAssetPick,
			[]string{"more above", "Lathe 40", "] next", "[ prev"}},

		{"supplier switch confirm", func(s *PurchaseOrderCreateScreen) {
			s.suppliers = append(s.suppliers, omsapi.Supplier{ID: 2, Name: "Southern Fastener Supply"})
			s.supplierCursor = 1
			id := 7
			s.lines = []poCartLine{
				{item: omsapi.PurchaseOrderCreateItem{ItemSupplierID: &id, Quantity: 1}, label: "Hex bolt"},
				{item: omsapi.PurchaseOrderCreateItem{Description: "Shop rags", Quantity: 2}, label: "Shop rags"},
			}
		}, poPhaseSupplierSwitch,
			[]string{"Changing supplier drops part of the cart",
				"ctrl+x drops 1 line(s) and switches", "esc keeps the cart and this supplier"}},

		{"assets, search matched nothing", func(s *PurchaseOrderCreateScreen) {
			s.assetsSearch.SetValue("hovercraft full of eels")
			s.assetsNote = pickerNote{
				"no asset matches " + strconv.Quote(pickerClip("hovercraft full of eels", 16)) +
					"\n/ edits the search · b picks another source", StatusWarn}
		}, poPhaseAssetPick,
			[]string{"no asset matches", "/ edits the search", "b picks another source"}},
	}

	for _, tc := range cases {
		for _, h := range poPaneSizes {
			t.Run(fmt.Sprintf("%s/80x%d", tc.name, h), func(t *testing.T) {
				// The REAL screen, not the frame in isolation: View() spends
				// rows on the folded help line, a blank and the supplier
				// header before the frame gets any, so measuring the frame
				// alone was ~5 rows more generous than the terminal and let a
				// long error push the way-out bar off the bottom unnoticed.
				s := poWidestSupplierScreen()
				s.phase = tc.phase
				s.terminalHeight = h
				tc.setup(s)

				poAssertFits(t, tc.name, s)
				for _, want := range tc.want {
					poWantPaneLine(t, s, want)
				}
			})
		}
	}
}

func poCatalog(n int) []omsapi.ItemSupplier {
	rows := make([]omsapi.ItemSupplier, n)
	for i := range rows {
		rows[i] = omsapi.ItemSupplier{
			ID: i + 1, ItemName: fmt.Sprintf("Widget %d", i+1),
			SupplierSKU: fmt.Sprintf("SKU-%03d", i+1),
		}
	}
	return rows
}

// TestPOItemPicker_AmbiguousNoteSurvivesTheClip is the same journey as
// TestPOItemPicker_AmbiguousSearchDoesNotGuess, asserted where it counts.
//
// That test's strings.Contains(Root.View(), "enter picks") passed over a body
// line that was cut at "j/k c", because Root.View() also renders the
// full-80-column status bar and the flash satisfied the substring. The note is
// the durable half — it is still on screen when the flash has expired — so it
// is the half that has to be checked inside the pane.
func TestPOItemPicker_AmbiguousNoteSurvivesTheClip(t *testing.T) {
	fake := &poPickFake{catalog: 12, pageSize: 5}
	r, screen := poPickerAt(t, fake, 80)
	r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("i")})
	r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	r = poType(t, r, "Widget 1") // Widget 1, 10, 11, 12
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})

	poWantPaneLine(t, screen, "4 of 12 match")
	poWantPaneLine(t, screen, "enter picks")
	poAssertFits(t, "ambiguous search", screen)

	// And the esc-closes-the-box variant, whose "search closed · " prefix is
	// what pushed the same note from 53 cells to 69.
	r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEsc})
	poWantPaneLine(t, screen, "search closed")
	poWantPaneLine(t, screen, "enter picks")
	poAssertFits(t, "search closed", screen)
	_ = r
}

// ---------------------------------------------------------------------------
// A frame may only name keys that work in the state it is drawing
// ---------------------------------------------------------------------------

// TestPOItemPicker_SearchBoxNeverNamesTheKeysItIsSwallowing: with the box open,
// 'b' is a letter going into the query and esc only closes the box. The empty
// frame used to print "b picks another line source · esc cancels the order"
// anyway, one line under a note that correctly said "esc then b for another
// source" — two contradictory claims at once, which is the same bar-honesty
// class just closed in eight places on the list screens.
func TestPOItemPicker_SearchBoxNeverNamesTheKeysItIsSwallowing(t *testing.T) {
	fake := &poPickFake{catalog: 12, pageSize: 5}
	r, screen := poPickerAt(t, fake, 80)
	r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("i")})
	r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	r = poType(t, r, "flux capacitor")

	poRejectPaneLine(t, screen, "b picks another line source")
	poRejectPaneLine(t, screen, "esc cancels the order")
	poWantPaneLine(t, screen, "esc closes the search")

	// 'b' does what the box says it does, not what the old line claimed.
	r = poType(t, r, "b")
	if screen.phase != poPhaseItemPick {
		t.Fatalf("'b' left the picker (phase %v) — the frame that named it was right after all", screen.phase)
	}
	if got := screen.itemSuppliersSearch.Value(); got != "flux capacitorb" {
		t.Errorf("'b' in the search box produced query %q, want it typed into the query", got)
	}
	_ = r
}

// TestPOAssetPicker_SearchBoxNeverNamesTheKeysItIsSwallowing — same frame, same
// rule, on the picker next door, where '/' is swallowed too.
func TestPOAssetPicker_SearchBoxNeverNamesTheKeysItIsSwallowing(t *testing.T) {
	fake := &poPickFake{assets: 0}
	r, screen := poPickerAt(t, fake, 80)
	r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
	r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})

	poRejectPaneLine(t, screen, "b picks another line source")
	poRejectPaneLine(t, screen, "esc cancels the order")
	poRejectPaneLine(t, screen, "/ searches")
	poWantPaneLine(t, screen, "esc closes the search")

	r = poType(t, r, "b")
	if screen.phase != poPhaseAssetPick {
		t.Fatalf("'b' left the asset picker (phase %v)", screen.phase)
	}
	if got := screen.assetsSearch.Value(); got != "b" {
		t.Errorf("'b' in the asset search produced query %q", got)
	}
	_ = r
}

// TestPOAssetPicker_FailedLoadWithTheBoxOpenNamesOnlyEsc: the third site — a
// failure landing while the search box is open. '/' cannot retry from there
// either; it is a slash in the query.
func TestPOAssetPicker_FailedLoadWithTheBoxOpenNamesOnlyEsc(t *testing.T) {
	fake := &poPickFake{assets: 3, failAssets: true}
	r, screen := poPickerAt(t, fake, 80)
	r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
	if screen.assetsErr == "" {
		t.Fatal("the failing fixture did not fail")
	}
	r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})

	poWantPaneLine(t, screen, "looking up this supplier's assets failed")
	poWantPaneLine(t, screen, "esc closes the search")
	poRejectPaneLine(t, screen, "/ retries with a search")
	poRejectPaneLine(t, screen, "b picks another line source")
	_ = r
}

// ---------------------------------------------------------------------------
// Found nothing and could-not-tell are different facts
// ---------------------------------------------------------------------------

// TestPOItemPicker_EmptyCatalogIsReportedAsEmpty: a supplier with no catalog
// used to get "✓ 0 catalog item(s) loaded · / searches" — a green tick on an
// empty result, advertising a '/' that can only ever answer "no match", and
// telling the operator the supplier sells nothing in the same voice a
// successful load uses. It also suppressed the sentence written for exactly
// this case, because that fallback only renders when the note is empty.
func TestPOItemPicker_EmptyCatalogIsReportedAsEmpty(t *testing.T) {
	fake := &poPickFake{catalog: 0, pageSize: 5}
	r, screen := poPickerAt(t, fake, 80)
	r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("i")})

	poRejectPaneLine(t, screen, "0 catalog item(s) loaded")
	poWantPaneLine(t, screen, "has no active catalog items on file")
	poWantPaneLine(t, screen, "b picks another line source")
	if screen.itemSuppliersNote.level != StatusWarn {
		t.Errorf("an empty catalog is reported at level %v, want a warning", screen.itemSuppliersNote.level)
	}

	// A failed load must NOT read the same as an empty one.
	fake.mu.Lock()
	fake.failItems = true
	fake.mu.Unlock()
	r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
	poWantPaneLine(t, screen, "looking up this supplier's items failed")
	poRejectPaneLine(t, screen, "has no active catalog items on file")
}

// ---------------------------------------------------------------------------
// A reply for the supplier the order has moved off is a WRONG reply
// ---------------------------------------------------------------------------

// TestPOPickers_LateReplyForTheOldSupplierIsDropped: staging an ItemSupplier id
// belonging to supplier A onto a purchase order for supplier B is a wrong
// purchase order, and the backend accepts it — nothing downstream would flag
// it. The catalog is now walked a page at a time, so the window between "press
// i" and "the rows land" is wide enough to change supplier inside.
func TestPOPickers_LateReplyForTheOldSupplierIsDropped(t *testing.T) {
	fake := &poPickFake{catalog: 6, pageSize: 5, assets: 2, suppliers: 2}
	r, screen := poPickerAt(t, fake, 80) // supplier 1 committed

	r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("i")})
	if len(screen.itemSuppliersAll) != 6 {
		t.Fatalf("setup: supplier 1's catalog did not load (%d rows)", len(screen.itemSuppliersAll))
	}

	// Move the order to supplier 2.
	r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("b")}) // → source chooser
	r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("b")}) // → supplier picker
	r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})
	if screen.supplierID != 2 {
		t.Fatalf("setup: the order is still on supplier %d", screen.supplierID)
	}
	if len(screen.itemSuppliersAll) != 0 {
		t.Errorf("committing another supplier left %d of the previous one's catalog rows in the picker",
			len(screen.itemSuppliersAll))
	}

	// Supplier 1's reply finally lands.
	ghost := []omsapi.ItemSupplier{{ID: 999, ItemName: "Ghost Widget"}}
	next, _ := r.Update(poItemSuppliersLoadedMsg{supplierID: 1, rows: ghost})
	r = next.(Root)
	next, _ = r.Update(poAssetsLoadedMsg{supplierID: 1, rows: []omsapi.Asset{{ID: "ghost", Name: "Ghost Lathe"}}})
	r = next.(Root)

	for _, row := range screen.itemSuppliersAll {
		if row.ID == 999 {
			t.Fatal("a reply for the previous supplier populated this order's catalog")
		}
	}
	if screen.itemSuppliersFor == 1 {
		t.Error("the picker is holding supplier 1's catalog against an order for supplier 2")
	}
	for _, a := range screen.assets {
		if a.Name == "Ghost Lathe" {
			t.Fatal("a reply for the previous supplier populated this order's asset picker")
		}
	}

	// And the picker for supplier 2 loads its own rows on demand.
	r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("i")})
	if len(screen.itemSuppliersAll) != 6 {
		t.Fatalf("supplier 2's catalog did not load (%d rows)", len(screen.itemSuppliersAll))
	}
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})
	if screen.phase != poPhaseLine {
		t.Fatalf("could not stage a line for supplier 2 (phase %v)", screen.phase)
	}
	if got := *screen.pickedItemSup; got == 999 {
		t.Fatal("the staged line names the previous supplier's item-supplier id")
	}
}

// ---------------------------------------------------------------------------
// The catalog is fetched when it is needed, and the frame says so only then
// ---------------------------------------------------------------------------

// TestPOItemPicker_CatalogIsNotRewalkedForEveryLine: the picker is re-entered
// once per line, and the correctness fix turned one request into one per page.
// A ten-line order against a 500-item catalog would be ~200 sequential requests
// for an answer that cannot have changed — and every one of them would show the
// "Looking up the items … sells…" frame, which is the working/succeeded/failed
// rule broken from the other side: that frame is a claim that work is
// happening.
func TestPOItemPicker_CatalogIsNotRewalkedForEveryLine(t *testing.T) {
	fake := &poPickFake{catalog: 40, pageSize: 5} // 8 pages
	r, screen := poPickerAt(t, fake, 80)

	r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("i")})
	first := fake.hits("/item-suppliers/")
	if first < 8 {
		t.Fatalf("setup: only %d request(s) for an 8-page catalog", first)
	}

	// Stage a line and come back for the next one, the way a multi-line order
	// is actually built.
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter}) // pick the highlighted row
	if screen.phase != poPhaseLine {
		t.Fatalf("setup: the pick did not open the line form (phase %v)", screen.phase)
	}
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEsc}) // back to the source chooser

	// Re-entering must NOT re-walk, and must NOT claim to be working.
	next, _ := r.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("i")})
	r = next.(Root)
	if screen.itemSuppliersLoad {
		t.Error("re-entering the picker started another walk of the same catalog")
	}
	poRejectPaneLine(t, screen, "Looking up the items")
	poWantPaneLine(t, screen, "Widget 1")
	poWantPaneLine(t, screen, "r reloads")
	if got := fake.hits("/item-suppliers/"); got != first {
		t.Errorf("re-entering the picker cost %d more request(s)", got-first)
	}

	// r is named, so r must work — and it is the one entry that SHOULD show the
	// working frame, because work is genuinely happening.
	next, cmd := r.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
	r = next.(Root)
	if !screen.itemSuppliersLoad {
		t.Fatal("r did not start a reload")
	}
	poWantPaneLine(t, screen, "Reloading the items")
	r = pump(t, r, cmd, 0)
	if got := fake.hits("/item-suppliers/"); got <= first {
		t.Errorf("r did not go back to OMS (%d requests, was %d)", got, first)
	}
	poWantPaneLine(t, screen, "Widget 1")
}

// TestPOItemPicker_EnterAgainstAnEmptyCatalogKeepsSayingItIsEmpty: with no
// catalog at all there is no filter to report on, and the unfiltered wording
// ("0 item(s) · enter picks the highlighted row") would answer the key that
// just declined to act by offering that same key — and would overwrite the
// warning the empty load posted with something less true.
func TestPOItemPicker_EnterAgainstAnEmptyCatalogKeepsSayingItIsEmpty(t *testing.T) {
	fake := &poPickFake{catalog: 0, pageSize: 5}
	r, screen := poPickerAt(t, fake, 80)
	r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("i")})

	for _, press := range []tea.KeyMsg{
		{Type: tea.KeyEnter},
		{Type: tea.KeyRunes, Runes: []rune("/")},
		{Type: tea.KeyEnter},
	} {
		r = key(t, r, press)
		poWantPaneLine(t, screen, "has no active catalog items on file")
		poRejectPaneLine(t, screen, "0 item(s)")
		poAssertFits(t, "empty catalog", screen)
	}
	if screen.phase != poPhaseItemPick {
		t.Fatalf("an empty catalog staged something (phase %v)", screen.phase)
	}
	// '/' declines too: the filter is client-side over the loaded catalog, so
	// with nothing loaded the box could only ever answer "no match" while
	// taking the keyboard away from the two keys that still work.
	if screen.itemSuppliersTyping {
		t.Error("'/' opened a search box over a catalog with nothing in it to search")
	}
	poWantPaneLine(t, screen, "b picks another line source")
}

// ---------------------------------------------------------------------------
// "Found nothing" and "could not tell" are still different mid-walk
// ---------------------------------------------------------------------------

// TestPOItemPicker_KeysPressedMidWalkDoNotClaimAnEmptyCatalog: an empty
// itemSuppliersAll is equally true of a supplier that sells nothing, a walk
// still in flight, and a walk that failed. The catalog is now fetched a page at
// a time, so the in-flight window is wide enough for an operator to press a key
// inside it — and the answer they used to get was the conclusion of a lookup
// that had not finished.
func TestPOItemPicker_KeysPressedMidWalkDoNotClaimAnEmptyCatalog(t *testing.T) {
	fake := &poPickFake{catalog: 40, pageSize: 5} // 8 sequential page requests
	r, screen := poPickerAt(t, fake, 80)

	// Enter the picker WITHOUT pumping: the walk is genuinely out.
	next, load := r.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("i")})
	r = next.(Root)
	if !screen.itemSuppliersLoad || len(screen.itemSuppliersAll) != 0 {
		t.Fatalf("setup: want an in-flight walk over an empty catalog slice")
	}

	// enter mid-walk: says the walk is still out, never that there is nothing.
	next, cmd := r.Update(tea.KeyMsg{Type: tea.KeyEnter})
	r = next.(Root)
	r = pump(t, r, cmd, 0)
	poWantPaneLine(t, screen, "Looking up the items Acme Supply sells")
	poRejectPaneLine(t, screen, "has no active catalog items")
	if out := r.View(); !strings.Contains(out, "still looking up") {
		t.Errorf("enter mid-walk does not say the lookup is still out:\n%s", out)
	} else if strings.Contains(out, "has no active catalog items") {
		t.Errorf("enter mid-walk claims the supplier sells nothing:\n%s", out)
	}
	if screen.phase != poPhaseItemPick {
		t.Fatalf("enter mid-walk staged something (phase %v)", screen.phase)
	}

	// '/' mid-walk OPENS the box — the rows are on their way, so there is
	// something for the query to filter once they land.
	next, _ = r.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	r = next.(Root)
	if !screen.itemSuppliersTyping {
		t.Error("'/' mid-walk refused to open the search box")
	}
	poAssertFits(t, "mid-walk", screen)

	// And the query typed mid-walk is honoured when the rows arrive.
	r = poType(t, r, "Widget 38")
	r = pump(t, r, load, 0)
	if screen.itemSuppliersLoad {
		t.Fatal("the walk never finished")
	}
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})
	if screen.phase != poPhaseLine {
		t.Fatalf("the mid-walk search could not be completed (phase %v)", screen.phase)
	}
	if got := screen.lineInputs[poLineFieldDesc].Value(); got != "Widget 38" {
		t.Errorf("staged %q, want Widget 38", got)
	}
}

// TestPOItemPicker_KeysAfterAFailedWalkReportTheFailure: the other state an
// empty slice hides. A failed lookup answering "this supplier has no catalog"
// is a conclusion drawn from a request that never came back.
func TestPOItemPicker_KeysAfterAFailedWalkReportTheFailure(t *testing.T) {
	fake := &poPickFake{catalog: 12, pageSize: 5, failItems: true}
	r, screen := poPickerAt(t, fake, 80)
	r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("i")})
	if screen.itemSuppliersErr == "" {
		t.Fatal("setup: the failing fixture did not fail")
	}

	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})
	poWantPaneLine(t, screen, "looking up this supplier's items failed")
	poWantPaneLine(t, screen, "r retries the lookup")
	poRejectPaneLine(t, screen, "has no active catalog items")
	if out := r.View(); !strings.Contains(out, "the catalog lookup failed") {
		t.Errorf("enter after a failed walk does not report the failure:\n%s", out)
	} else if strings.Contains(out, "has no active catalog items") {
		t.Errorf("enter after a failed walk claims the supplier sells nothing:\n%s", out)
	}

	// r is named on that frame, so r must work — and it recovers.
	fake.mu.Lock()
	fake.failItems = false
	fake.mu.Unlock()
	r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
	if screen.itemSuppliersErr != "" {
		t.Errorf("r did not clear the failure: %q", screen.itemSuppliersErr)
	}
	poWantPaneLine(t, screen, "Widget 1")
}

// ---------------------------------------------------------------------------
// The zero-match note must not keep open-box wording once the box is closed
// ---------------------------------------------------------------------------

// TestPOItemPicker_ZeroMatchNoteMatchesTheBoxState: with the box OPEN, esc
// closes the search; with it CLOSED, esc cancels the whole purchase order. The
// zero-match note used to emit the open-box wording in both, so the closed
// frame carried "esc then b for another source" one line above "esc cancels the
// order" — and the reading that costs the operator their order is the wrong one.
func TestPOItemPicker_ZeroMatchNoteMatchesTheBoxState(t *testing.T) {
	fake := &poPickFake{catalog: 12, pageSize: 5}
	r, screen := poPickerAt(t, fake, 80)
	r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("i")})
	r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	r = poType(t, r, "flux capacitor")

	// Box OPEN: nothing may claim esc leads anywhere but out of the search.
	poWantPaneLine(t, screen, "edit the search")
	poWantPaneLine(t, screen, "esc closes the search")
	poRejectPaneLine(t, screen, "esc cancels the order")
	poRejectPaneLine(t, screen, "esc then b for another source")
	poAssertFits(t, "zero match, box open", screen)

	// esc closes it. The note must now speak for the CLOSED frame, and must
	// acknowledge what esc just did rather than dropping the lead in silence.
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEsc})
	if screen.itemSuppliersTyping {
		t.Fatal("esc did not close the search box")
	}
	poWantPaneLine(t, screen, "search closed")
	poWantPaneLine(t, screen, "no match for")
	poWantPaneLine(t, screen, "/ edits the search")
	poWantPaneLine(t, screen, "esc cancels the order")
	poRejectPaneLine(t, screen, "esc then b for another source")
	poAssertFits(t, "zero match, box closed", screen)

	// And the keys the closed frame names do what it says.
	r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("b")})
	if screen.phase != poPhaseSource {
		t.Fatalf("b on the closed zero-match frame did not pick another source (phase %v)", screen.phase)
	}
}

// poNoteLines is a pickerNote as the pane will show it: rendered, then clipped
// to screenBodyWidth(80). Assert a note's own wording here rather than over the
// whole pane — the phase help line at the top names the picker's keys
// generally, so a pane-wide substring check cannot tell a note that claims a
// dead key from the bar that legitimately lists it.
func poNoteLines(t *testing.T, n pickerNote) []string {
	t.Helper()
	return strings.Split(clampToBox(n.render(), screenBodyWidth(80), 10), "\n")
}

// poNoteSays reports whether the rendered, clipped note carries want.
func poNoteSays(t *testing.T, n pickerNote, want string) bool {
	t.Helper()
	for _, line := range poNoteLines(t, n) {
		if strings.Contains(line, want) {
			return true
		}
	}
	return false
}

// TestPOItemPicker_TypingMidWalkDoesNotConcludeTheCatalogIsEmpty: the live
// filter note runs on every keystroke, and mid-walk it was filtering a slice
// that is empty because the request has not come back. `no match for "w" (0 in
// catalog)` is a statement about a catalog nobody has seen yet, and esc then
// flashes that sentence onto the status bar.
//
// Asserted WHILE the walk is out, not after it lands — checking only the
// settled state is how the previous test over this journey missed it.
func TestPOItemPicker_TypingMidWalkDoesNotConcludeTheCatalogIsEmpty(t *testing.T) {
	fake := &poPickFake{catalog: 40, pageSize: 5} // 8 sequential page requests
	r, screen := poPickerAt(t, fake, 80)

	next, load := r.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("i")})
	r = next.(Root)
	next, _ = r.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	r = next.(Root)
	if !screen.itemSuppliersTyping || !screen.itemSuppliersLoad {
		t.Fatalf("setup: want the box open over an in-flight walk (typing=%v load=%v)",
			screen.itemSuppliersTyping, screen.itemSuppliersLoad)
	}

	// Every keystroke, still mid-walk.
	for _, ch := range "wid" {
		next, _ = r.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{ch}})
		r = next.(Root)
		if !screen.itemSuppliersLoad {
			t.Fatal("the walk finished early — this test needs it in flight")
		}
		// The body says the work is out...
		poWantPaneLine(t, screen, "Looking up the items")
		// ...and the note behind it holds no verdict about a catalog nobody
		// has seen. It is what esc is about to put on the status bar.
		if poNoteSays(t, screen.itemSuppliersNote, "in catalog") {
			t.Errorf("typing mid-walk concluded a catalog size: %q", screen.itemSuppliersNote.text)
		}
		if !poNoteSays(t, screen.itemSuppliersNote, "still looking up") {
			t.Errorf("typing mid-walk does not say the walk is still out: %q", screen.itemSuppliersNote.text)
		}
	}

	// esc mid-walk flashes that note. It must not announce a verdict either.
	next, cmd := r.Update(tea.KeyMsg{Type: tea.KeyEsc})
	r = next.(Root)
	r = pump(t, r, cmd, 0)
	if !screen.itemSuppliersLoad {
		t.Fatal("esc finished the walk — this assertion needs it in flight")
	}
	if out := r.View(); strings.Contains(out, "in catalog") {
		t.Errorf("esc mid-walk flashed a catalog verdict:\n%s", out)
	} else if !strings.Contains(out, "still looking up") {
		t.Errorf("esc mid-walk does not say the walk is still out:\n%s", out)
	}

	// When the rows land, the note answers the query that was typed.
	r = pump(t, r, load, 0)
	if screen.itemSuppliersLoad {
		t.Fatal("the walk never finished")
	}
	poWantPaneLine(t, screen, "match")
	poAssertFits(t, "rows landed after a mid-walk search", screen)
}

// TestPOItemPicker_RowsLandingWithTheBoxOpenDoNotAdvertiseSlash: '/' is a
// character going into the query while the box is open, so the loaded note may
// not name it as the key that searches.
func TestPOItemPicker_RowsLandingWithTheBoxOpenDoNotAdvertiseSlash(t *testing.T) {
	fake := &poPickFake{catalog: 40, pageSize: 5}
	r, screen := poPickerAt(t, fake, 80)

	next, load := r.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("i")})
	r = next.(Root)
	next, _ = r.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	r = next.(Root)
	r = poType(t, r, "Widget 38")
	r = pump(t, r, load, 0)

	if !screen.itemSuppliersTyping {
		t.Fatal("setup: the box closed before the rows landed")
	}
	if poNoteSays(t, screen.itemSuppliersNote, "/ searches") {
		t.Errorf("the loaded note advertises / while / is a character in the query: %q",
			screen.itemSuppliersNote.text)
	}
	if !poNoteSays(t, screen.itemSuppliersNote, "enter picks") {
		t.Errorf("the loaded note does not name the key that finishes the search: %q",
			screen.itemSuppliersNote.text)
	}
	poAssertFits(t, "rows landed with the box open", screen)

	// And that key works.
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})
	if screen.phase != poPhaseLine {
		t.Fatalf("the key the note named did not work (phase %v)", screen.phase)
	}
	if got := screen.lineInputs[poLineFieldDesc].Value(); got != "Widget 38" {
		t.Errorf("staged %q, want Widget 38", got)
	}
}

// TestPOAssetPicker_SearchClosedNoteNamesOnlyLiveKeys: esc out of the asset
// search used to post "j/k move · enter picks" unconditionally, and that note
// IS the body of the empty frame — so it named three keys over nothing to move
// through and nothing to pick.
func TestPOAssetPicker_SearchClosedNoteNamesOnlyLiveKeys(t *testing.T) {
	fake := &poPickFake{assets: 3}
	r, screen := poPickerAt(t, fake, 80)
	r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})

	// Search for something that is not there, so the list is left empty.
	r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	r = poType(t, r, "hovercraft")
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})
	if len(screen.assets) != 0 {
		t.Fatalf("setup: the search returned %d asset(s)", len(screen.assets))
	}

	// Re-open the box and back out of it: THIS is the note that overwrote the
	// honest one.
	r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEsc})

	poWantPaneLine(t, screen, "search closed")
	if poNoteSays(t, screen.assetsNote, "j/k move") || poNoteSays(t, screen.assetsNote, "enter picks") {
		t.Errorf("the empty asset frame names keys that do nothing: %q", screen.assetsNote.text)
	}
	if !poNoteSays(t, screen.assetsNote, "no asset matches") {
		t.Errorf("the empty asset frame does not say why it is empty: %q", screen.assetsNote.text)
	}
	poAssertFits(t, "asset search closed over an empty list", screen)

	// The keys it DOES name work: / reopens the box.
	r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	if !screen.assetsTyping {
		t.Error("the note names / but / did not reopen the search")
	}

	// With rows present the same path may name j/k and enter, because there
	// they do something.
	for range "hovercraft" {
		next, _ := r.Update(tea.KeyMsg{Type: tea.KeyBackspace})
		r = next.(Root)
	}
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})
	if len(screen.assets) == 0 {
		t.Fatalf("setup: clearing the query returned no assets")
	}
	r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEsc})
	if !poNoteSays(t, screen.assetsNote, "j/k move") || !poNoteSays(t, screen.assetsNote, "enter picks") {
		t.Errorf("with rows on screen the note stopped naming the keys that work: %q", screen.assetsNote.text)
	}
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})
	if screen.phase != poPhaseLine {
		t.Fatalf("the note named enter but enter did not pick (phase %v)", screen.phase)
	}
}

// TestPOPickers_FitEveryPaneSizeAndNeverHideTheCursor drives the pickers the
// operator actually drives, at BOTH supported terminal heights, and requires
// two things of every frame: nothing is cut off either edge, and the row the
// cursor is on is on the pane.
//
// The second is the one that matters. A windowed list that overflows does not
// merely lose a hint: j still moves the highlight onto a row the pane has
// dropped and enter still stages it, so the operator puts an item on a purchase
// order they never saw. The frame must instead SAY how many rows it hid.
func TestPOPickers_FitEveryPaneSizeAndNeverHideTheCursor(t *testing.T) {
	for _, height := range poPaneSizes {
		t.Run(fmt.Sprintf("height %d", height), func(t *testing.T) {
			t.Run("item picker", func(t *testing.T) {
				fake := &poPickFake{catalog: 40, pageSize: 40}
				r, screen := poPickerAtSize(t, fake, 80, height)
				r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("i")})
				poAssertFits(t, "item picker, loaded", screen)

				// Walk the cursor to the bottom; every step must stay visible.
				for i := 0; i < len(screen.itemSuppliers)-1; i++ {
					r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
					poAssertFits(t, "item picker, scrolling", screen)
					want := screen.itemSuppliers[screen.itemSuppliersCur].ItemName
					if !poPaneHasLine(t, screen, want) {
						t.Fatalf("the highlighted row %q is not on the 80x%d pane:\n%s",
							want, height, strings.Join(poPaneLines(t, screen), "\n"))
					}
				}
				// And the frame says rows are hidden rather than dropping them.
				poWantPaneLine(t, screen, "more above")

				// Enter stages the row the operator is looking at.
				staged := screen.itemSuppliers[screen.itemSuppliersCur].ItemName
				r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})
				if got := screen.lineInputs[poLineFieldDesc].Value(); got != staged {
					t.Errorf("staged %q, want the highlighted %q", got, staged)
				}
			})

			t.Run("item picker with a search note", func(t *testing.T) {
				fake := &poPickFake{catalog: 40, pageSize: 40}
				r, screen := poPickerAtSize(t, fake, 80, height)
				r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("i")})
				r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
				r = poType(t, r, "Widget 1")
				poAssertFits(t, "item picker, box open", screen)
				r = key(t, r, tea.KeyMsg{Type: tea.KeyEsc})
				poAssertFits(t, "item picker, box closed over a note", screen)
				poWantPaneLine(t, screen, "search closed")
				_ = r
			})

			t.Run("asset picker with a pager", func(t *testing.T) {
				fake := &poPickFake{assets: 12, pageSize: 5}
				r, screen := poPickerAtSize(t, fake, 80, height)
				r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
				if !screen.assetsHasNext {
					t.Fatalf("setup: wanted a pager, got %d asset(s) and no next page", len(screen.assets))
				}
				poAssertFits(t, "asset picker, loaded", screen)
				// The pager is drawn after the list, so it is the first thing an
				// unbudgeted list pushes off the bottom — and it names ']'.
				poWantPaneLine(t, screen, "] next")
				for i := 0; i < len(screen.assets)-1; i++ {
					r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
					poAssertFits(t, "asset picker, scrolling", screen)
					if want := screen.assets[screen.assetsCursor].Name; !poPaneHasLine(t, screen, want) {
						t.Fatalf("the highlighted asset %q is not on the 80x%d pane:\n%s",
							want, height, strings.Join(poPaneLines(t, screen), "\n"))
					}
				}
				poWantPaneLine(t, screen, "] next")
			})
		})
	}
}

// TestPOItemPicker_ReloadDoesNotLetAVerdictPastTheGate: 'r' leaves the previous
// rows in place while the new walk is out, so catalogAnswered() goes false with
// itemSuppliersAll still full. A guard written as len(itemSuppliersAll) == 0
// sails straight past that, and the commit paths then word a verdict about a
// request that has not come back — one keystroke after the typing path
// correctly said it could not tell.
func TestPOItemPicker_ReloadDoesNotLetAVerdictPastTheGate(t *testing.T) {
	fake := &poPickFake{catalog: 20, pageSize: 20}
	r, screen := poPickerAt(t, fake, 80)
	r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("i")})
	if !screen.catalogAnswered() || len(screen.itemSuppliersAll) != 20 {
		t.Fatalf("setup: catalog not loaded (answered=%v rows=%d)",
			screen.catalogAnswered(), len(screen.itemSuppliersAll))
	}

	// Start a reload WITHOUT pumping it: stale rows held, no answer in hand.
	next, reload := r.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
	r = next.(Root)
	if screen.catalogAnswered() || len(screen.itemSuppliersAll) == 0 {
		t.Fatalf("setup: want an unanswered reload over held rows (answered=%v rows=%d)",
			screen.catalogAnswered(), len(screen.itemSuppliersAll))
	}

	next, _ = r.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	r = next.(Root)
	r = poType(t, r, "zzz")
	if poNoteSays(t, screen.itemSuppliersNote, "in catalog") {
		t.Errorf("typing mid-reload concluded a catalog size: %q", screen.itemSuppliersNote.text)
	}

	// enter, the site that used to bypass the gate because rows were held.
	next, cmd := r.Update(tea.KeyMsg{Type: tea.KeyEnter})
	r = next.(Root)
	r = pump(t, r, cmd, 0)
	if poNoteSays(t, screen.itemSuppliersNote, "in catalog") {
		t.Errorf("enter mid-reload concluded a catalog size: %q", screen.itemSuppliersNote.text)
	}
	if !poNoteSays(t, screen.itemSuppliersNote, "still looking up") {
		t.Errorf("enter mid-reload does not say the walk is out: %q", screen.itemSuppliersNote.text)
	}
	if out := r.View(); strings.Contains(out, "in catalog") {
		t.Errorf("enter mid-reload flashed a catalog verdict:\n%s", out)
	}

	// Leaving and re-entering the picker mid-reload must not post an entry note
	// that counts rows the reload may be about to replace.
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEsc})
	r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("b")})
	next, _ = r.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("i")})
	r = next.(Root)
	if poNoteSays(t, screen.itemSuppliersNote, "catalog item(s) ·") {
		t.Errorf("re-entering mid-reload counted rows still in transit: %q", screen.itemSuppliersNote.text)
	}
	if got := fake.hits("/item-suppliers/"); got > 2 {
		t.Errorf("re-entering mid-reload fired another walk (%d requests)", got)
	}

	// Once it lands, the gate opens and a verdict is a verdict again.
	r = pump(t, r, reload, 0)
	if !screen.catalogAnswered() {
		t.Fatal("the reload never landed")
	}
	r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	r = poType(t, r, "zzz")
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})
	if !poNoteSays(t, screen.itemSuppliersNote, "in catalog") {
		t.Errorf("after the reload landed the screen still will not answer: %q",
			screen.itemSuppliersNote.text)
	}
	if screen.phase != poPhaseItemPick {
		t.Fatalf("a no-match search staged something (phase %v)", screen.phase)
	}
}

// TestPOItemPicker_EnterOverAnAmbiguousSearchMovesTheNote is the report itself,
// on the path that still had it: several matches.
//
// The assertion is the point. The old one was `Root.View() != before`, which
// the textinput CARET leaving the search box satisfies all on its own — so
// enter could leave a body note byte-for-byte identical to the one already on
// screen and still pass. That is exactly "I press enter and it just kinda hangs
// there". This compares the rendered NOTE, which a blinking cursor cannot move.
func TestPOItemPicker_EnterOverAnAmbiguousSearchMovesTheNote(t *testing.T) {
	fake := &poPickFake{catalog: 12, pageSize: 12}
	r, screen := poPickerAt(t, fake, 80)
	r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("i")})
	r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	r = poType(t, r, "Widget 1") // Widget 1, 10, 11, 12

	before := strings.Join(poNoteLines(t, screen.itemSuppliersNote), "\n")
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})
	after := strings.Join(poNoteLines(t, screen.itemSuppliersNote), "\n")

	if before == after {
		t.Errorf("enter over an ambiguous search left the note unchanged — only the caret moved:\n%s", after)
	}
	if screen.itemSuppliersTyping {
		t.Error("enter did not close the search box")
	}
	if len(screen.lines) != 0 || screen.phase != poPhaseItemPick {
		t.Fatalf("an ambiguous search staged something (phase %v, %d line(s))", screen.phase, len(screen.lines))
	}
	// It says what enter DID and hands the operator the keys that now work.
	poWantPaneLine(t, screen, "too many to pick")
	poWantPaneLine(t, screen, "j/k choose")
	poAssertFits(t, "ambiguous enter", screen)
}

// TestPOItemPicker_OpenBoxNoteNamesOnlyTheKeysTheBoxLeavesAlive: with the
// search box open j and k are characters going into the query and enter only
// picks when exactly one row is left, so a note naming "j/k choose · enter
// picks" over eleven matches names three keys of which two do something else.
func TestPOItemPicker_OpenBoxNoteNamesOnlyTheKeysTheBoxLeavesAlive(t *testing.T) {
	fake := &poPickFake{catalog: 12, pageSize: 12}
	r, screen := poPickerAt(t, fake, 80)
	r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("i")})
	r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})

	// Empty query, box open: every row "matches" but enter will not pick one.
	if poNoteSays(t, screen.itemSuppliersNote, "enter picks the highlighted row") {
		t.Errorf("the open box claims enter picks the highlighted row: %q", screen.itemSuppliersNote.text)
	}

	r = poType(t, r, "Widget 1") // 4 matches, box still open
	if !screen.itemSuppliersTyping {
		t.Fatal("setup: the box closed")
	}
	if poNoteSays(t, screen.itemSuppliersNote, "j/k choose") {
		t.Errorf("the open box claims j/k choose, where they are query characters: %q",
			screen.itemSuppliersNote.text)
	}
	poWantPaneLine(t, screen, "4 of 12 match")
	poAssertFits(t, "open box, several matches", screen)

	// j really is a character here, which is why naming it would be a lie.
	r = poType(t, r, "j")
	if got := screen.itemSuppliersSearch.Value(); got != "Widget 1j" {
		t.Errorf("'j' with the box open produced query %q, want it typed in", got)
	}

	// Narrow to one and the wording that DOES hold in both states appears.
	next, _ := r.Update(tea.KeyMsg{Type: tea.KeyBackspace}) // drop the 'j'
	r = next.(Root)
	r = poType(t, r, "2") // "Widget 12"
	if len(screen.itemSuppliers) != 1 {
		t.Fatalf("setup: %d match(es), want 1", len(screen.itemSuppliers))
	}
	if !poNoteSays(t, screen.itemSuppliersNote, "enter picks it") {
		t.Errorf("a single match does not name the key that takes it: %q", screen.itemSuppliersNote.text)
	}
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})
	if screen.phase != poPhaseLine {
		t.Fatalf("the key the note named did not pick (phase %v)", screen.phase)
	}
}

// TestPOPickers_RowKeysDeclineWhenTheFrameDrawsNoList sweeps the rule across
// all three pickers and both off-screen states.
//
// Every picker holds the rows it was showing while a reload is out and after
// one fails — deliberately, so a failed refresh does not also destroy what was
// on screen — but neither frame DRAWS them. A cursor the operator cannot see is
// still a cursor j/k will move and enter will stage, and an item going onto a
// purchase order that the operator cannot see is a wrong purchase order. The
// failure frame compounds it: it names r/b/esc and nothing else.
func TestPOPickers_RowKeysDeclineWhenTheFrameDrawsNoList(t *testing.T) {
	// item picker, reload in flight over held rows.
	t.Run("items, reloading", func(t *testing.T) {
		fake := &poPickFake{catalog: 20, pageSize: 20}
		r, screen := poPickerAt(t, fake, 80)
		r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("i")})
		r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
		r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
		wantCur := screen.itemSuppliersCur

		next, reload := r.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
		r = next.(Root)
		if !screen.itemSuppliersLoad || len(screen.itemSuppliers) == 0 {
			t.Fatalf("setup: want a reload over held rows (load=%v rows=%d)",
				screen.itemSuppliersLoad, len(screen.itemSuppliers))
		}
		poRejectPaneLine(t, screen, "Widget 1  ")

		for _, k := range []tea.KeyMsg{
			{Type: tea.KeyRunes, Runes: []rune("j")},
			{Type: tea.KeyRunes, Runes: []rune("k")},
			{Type: tea.KeyEnter},
		} {
			next, cmd := r.Update(k)
			r = next.(Root)
			r = pump(t, r, cmd, 0)
			if screen.phase != poPhaseItemPick {
				t.Fatalf("%v staged a row the frame is not drawing (phase %v)", k, screen.phase)
			}
			if screen.itemSuppliersCur != wantCur {
				t.Fatalf("%v moved a cursor the operator cannot see (%d -> %d)",
					k, wantCur, screen.itemSuppliersCur)
			}
			if out := r.View(); !strings.Contains(out, "still looking up") {
				t.Errorf("%v mid-reload said nothing about the walk:\n%s", k, out)
			}
		}
		// Once it lands the same keys work again.
		r = pump(t, r, reload, 0)
		r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})
		if screen.phase != poPhaseLine {
			t.Fatalf("enter stopped working after the reload landed (phase %v)", screen.phase)
		}
	})

	t.Run("items, failed reload", func(t *testing.T) {
		fake := &poPickFake{catalog: 20, pageSize: 20}
		r, screen := poPickerAt(t, fake, 80)
		r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("i")})
		fake.mu.Lock()
		fake.failItems = true
		fake.mu.Unlock()
		r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
		if screen.itemSuppliersErr == "" || len(screen.itemSuppliers) == 0 {
			t.Fatalf("setup: want a failed reload over held rows (err=%q rows=%d)",
				screen.itemSuppliersErr, len(screen.itemSuppliers))
		}
		// The frame names r, b and esc — and not enter.
		poWantPaneLine(t, screen, "r retries the lookup")
		poRejectPaneLine(t, screen, "Widget 1  ")

		r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})
		if screen.phase != poPhaseItemPick {
			t.Fatalf("enter staged a row from the failure frame (phase %v)", screen.phase)
		}
		if out := r.View(); !strings.Contains(out, "the catalog lookup failed") {
			t.Errorf("enter on the failure frame said nothing:\n%s", out)
		}
	})

	t.Run("assets, page load in flight", func(t *testing.T) {
		fake := &poPickFake{assets: 12, pageSize: 5}
		r, screen := poPickerAt(t, fake, 80)
		r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
		if !screen.assetsHasNext {
			t.Fatalf("setup: wanted a next page, got %d asset(s)", len(screen.assets))
		}
		wantCur := screen.assetsCursor

		next, page := r.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("]")})
		r = next.(Root)
		if !screen.assetsLoading || len(screen.assets) == 0 {
			t.Fatalf("setup: want a page load over held rows (load=%v rows=%d)",
				screen.assetsLoading, len(screen.assets))
		}
		for _, k := range []tea.KeyMsg{
			{Type: tea.KeyRunes, Runes: []rune("j")},
			{Type: tea.KeyEnter},
		} {
			next, cmd := r.Update(k)
			r = next.(Root)
			r = pump(t, r, cmd, 0)
			if screen.phase != poPhaseAssetPick {
				t.Fatalf("%v staged an asset the frame is not drawing (phase %v)", k, screen.phase)
			}
			if screen.assetsCursor != wantCur {
				t.Fatalf("%v moved an invisible cursor (%d -> %d)", k, wantCur, screen.assetsCursor)
			}
			if out := r.View(); !strings.Contains(out, "still looking up the assets") {
				t.Errorf("%v mid-load said nothing about the request:\n%s", k, out)
			}
		}
		r = pump(t, r, page, 0)
		r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})
		if screen.phase != poPhaseLine {
			t.Fatalf("enter stopped working after the page landed (phase %v)", screen.phase)
		}
	})

	t.Run("reorder, reload in flight", func(t *testing.T) {
		fake := &poPickFake{reorder: 6}
		r, screen := poPickerAt(t, fake, 80)
		r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
		if len(screen.reorderItems) != 6 {
			t.Fatalf("setup: loaded %d reorder row(s), want 6", len(screen.reorderItems))
		}
		r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("b")})

		next, reload := r.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
		r = next.(Root)
		if !screen.reorderLoading || len(screen.reorderItems) == 0 {
			t.Fatalf("setup: want a reload over held rows (load=%v rows=%d)",
				screen.reorderLoading, len(screen.reorderItems))
		}
		poRejectPaneLine(t, screen, "Bolt 1  ")

		for _, k := range []tea.KeyMsg{
			{Type: tea.KeyRunes, Runes: []rune("j")},
			{Type: tea.KeyRunes, Runes: []rune(" ")},
			{Type: tea.KeyRunes, Runes: []rune("a")},
			{Type: tea.KeyEnter},
		} {
			next, cmd := r.Update(k)
			r = next.(Root)
			r = pump(t, r, cmd, 0)
			if screen.phase != poPhaseReorderPick {
				t.Fatalf("%v acted on rows the frame is not drawing (phase %v)", k, screen.phase)
			}
			if len(screen.lines) != 0 {
				t.Fatalf("%v staged %d line(s) mid-reload", k, len(screen.lines))
			}
			if len(screen.reorderSelected) != 0 {
				t.Fatalf("%v marked an invisible row", k)
			}
			if out := r.View(); !strings.Contains(out, "still looking up what") {
				t.Errorf("%v mid-reload said nothing about the walk:\n%s", k, out)
			}
		}
		r = pump(t, r, reload, 0)
		r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})
		if screen.phase != poPhaseLine {
			t.Fatalf("enter stopped working after the reload landed (phase %v)", screen.phase)
		}
	})
}

// TestPOAssetPicker_RowsLandingWithTheBoxOpenDoNotClaimEnterPicks: the asset
// reply can arrive with the search box still open, where enter runs the search
// again rather than picking — the same typing gate the item picker's notes have.
func TestPOAssetPicker_RowsLandingWithTheBoxOpenDoNotClaimEnterPicks(t *testing.T) {
	fake := &poPickFake{assets: 3}
	r, screen := poPickerAt(t, fake, 80)

	next, load := r.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
	r = next.(Root)
	next, _ = r.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	r = next.(Root)
	if !screen.assetsTyping || !screen.assetsLoading {
		t.Fatalf("setup: want the box open over an in-flight load (typing=%v load=%v)",
			screen.assetsTyping, screen.assetsLoading)
	}
	r = pump(t, r, load, 0)

	if !screen.assetsTyping {
		t.Fatal("the reply closed the search box")
	}
	if poNoteSays(t, screen.assetsNote, "enter picks the highlighted row") {
		t.Errorf("the open box claims enter picks a row: %q", screen.assetsNote.text)
	}
	poAssertFits(t, "assets landed with the box open", screen)

	// esc closes it and the closed-box wording returns.
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEsc})
	if !poNoteSays(t, screen.assetsNote, "enter picks") {
		t.Errorf("the closed box stopped naming the key that picks: %q", screen.assetsNote.text)
	}
}

// TestPOSupplierPicker_IsWindowedLikeEveryOtherBlock: the supplier list was the
// one scrolling block outside the shared budget, and it carried the marker bug
// that budget's own windower was fixed for — a newline inside Render, which
// makes lipgloss pad the block and leak twenty columns onto the row underneath.
func TestPOSupplierPicker_IsWindowedLikeEveryOtherBlock(t *testing.T) {
	for _, height := range poPaneSizes {
		t.Run(fmt.Sprintf("height %d", height), func(t *testing.T) {
			fake := &poPickFake{suppliers: 30}
			srv := httptest.NewServer(fake.handler())
			t.Cleanup(srv.Close)

			deps := Deps{OMS: omsapi.New(srv.URL), Ctx: context.Background()}
			screen := NewPurchaseOrderCreateScreen(deps)
			r := newTestRoot(screen)
			r.deps = deps
			next, _ := r.Update(tea.WindowSizeMsg{Width: 80, Height: height})
			r = next.(Root)
			r = pump(t, r, screen.Init(), 0)
			if screen.phase != poPhaseSupplier || len(screen.suppliers) != 30 {
				t.Fatalf("setup: phase %v with %d supplier(s)", screen.phase, len(screen.suppliers))
			}

			// Walk down past the point the window starts scrolling; every step
			// must keep the highlighted supplier on the pane and the frame must
			// say how many it hid.
			for i := 0; i < 29; i++ {
				r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
				poAssertFits(t, "supplier picker, scrolling", screen)
				want := fmt.Sprintf("(#%d)", screen.suppliers[screen.supplierCursor].ID)
				if !poPaneHasLine(t, screen, want) {
					t.Fatalf("the highlighted supplier %s is not on the 80x%d pane:\n%s",
						want, height, strings.Join(poPaneLines(t, screen), "\n"))
				}
			}
			poWantPaneLine(t, screen, "more above")

			// The row under the ↑ marker keeps its own indentation: a marker
			// that pads its block pushes that row's "(#id)" past the cut.
			for _, line := range poPaneLines(t, screen) {
				if strings.Contains(line, "(#") && strings.HasPrefix(line, "        ") {
					t.Errorf("a supplier row is indented by the marker above it: %q", line)
				}
			}

			// And enter commits the supplier the operator is looking at.
			want := screen.suppliers[screen.supplierCursor].ID
			r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})
			if screen.supplierID != want {
				t.Errorf("enter committed supplier %d, want the highlighted %d", screen.supplierID, want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// The bar-honesty rule, as ONE sweep, over the New PO pickers
// ---------------------------------------------------------------------------

// poPickerBarKeys maps a bar segment's first token to the keystroke it claims.
// Every token the three pickers can emit must be here: an unrecognised one is a
// claim the sweep would skip in silence, which is how a dead key survives.
var poPickerBarKeys = map[string][]string{
	// The arrows are aliases of j/k, not keys of their own: every arm that
	// moves a picker cursor is written `case "j", "down":` / `case "k", "up":`,
	// so the token "j/k move" is the claim that covers all four. Naming the
	// arrows separately in a 51-column bar would spend cells on a synonym.
	"j/k":    {"j", "k", "down", "up"},
	"enter":  {"enter"},
	"/":      {"/"},
	"r":      {"r"},
	"b":      {"b"},
	"esc":    {"esc"},
	"]":      {"]"},
	"[":      {"["},
	"space":  {" "},
	"a":      {"a"},
	"type":   nil, // "type to filter" names no single key
	"ctrl+x": {"ctrl+x"},
}

// poPickerVocabulary is every keystroke the sweep presses. A key outside the
// bar's claim must leave the screen alone; one inside it must do something.
// poPickerVocabulary is what the sweep presses. A key ABSENT from this list is
// never pressed in either direction, so it is untested BOTH as a claim the bar
// makes and as an action the screen takes — which is exactly how two defects in
// this run survived: 'N' on the purchase-order list sat outside poAllBarKeys,
// and 'tab' committing the order's supplier sat outside this list.
//
// So it covers every key any of these phases binds, not just the ones the bars
// happen to name: the cursor keys and their arrow aliases, the paging pair, the
// source-chooser letters (which must do NOTHING inside a picker), the line
// form's tab/shift+tab, and the two ctrl chords the review and switch frames
// use. Adding a key here is cheap; leaving one out is invisible.
var poPickerVocabulary = []string{
	"j", "k", "down", "up", "enter", "esc", "tab", "shift+tab",
	"/", "r", "b", "]", "[", " ",
	"a", "i", "f", "g", "w", "c", "d", "x",
	"ctrl+e", "ctrl+x",
}

func poPickerNamedKeys(t *testing.T, bar string) map[string]bool {
	t.Helper()
	named := map[string]bool{}
	for _, seg := range strings.Split(bar, " · ") {
		fields := strings.Fields(strings.TrimSpace(seg))
		if len(fields) == 0 {
			continue
		}
		keys, ok := poPickerBarKeys[fields[0]]
		if !ok {
			t.Fatalf("bar segment %q starts with an unknown token — add it to poPickerBarKeys (bar: %q)", seg, bar)
		}
		for _, k := range keys {
			named[k] = true
		}
	}
	return named
}

// poPickerState is everything a key can CHANGE. Deliberately excludes the note
// and the status flash: a key that declines and says why has not acted, and the
// project's rule is that such an arm must answer, not that the bar must promise
// it. Anything in here moving means the key did something.
func poPickerState(s *PurchaseOrderCreateScreen) string {
	return fmt.Sprint(s.phase, "|", s.itemSuppliersCur, s.itemSuppliersTyping, s.itemSuppliersLoad,
		s.itemSuppliersErr, s.itemSuppliersSearch.Value(), len(s.itemSuppliers),
		"|", s.assetsCursor, s.assetsTyping, s.assetsLoading, s.assetsErr,
		s.assetsSearch.Value(), s.assetsPage, len(s.assets),
		"|", s.reorderCursor, s.reorderLoading, s.reorderLoadErr, len(s.reorderSelected),
		"|", s.supplierCursor, s.supplierLoading, s.supplierLoadErr,
		"|", len(s.lines), s.supplierID)
}

// poCmdActs reports whether a command does anything beyond posting a status.
// esc leaves the screen entirely and b changes phase, so a fingerprint alone
// would call the first of those dead.
func poCmdActs(cmd tea.Cmd) bool {
	if cmd == nil {
		return false
	}
	msg := cmd()
	switch m := msg.(type) {
	case nil:
		return false
	case StatusMsg:
		return false
	case tea.BatchMsg:
		for _, c := range m {
			if poCmdActs(c) {
				return true
			}
		}
		return false
	}
	return true
}

// TestPOPickers_PaneNamesExactlyTheKeysThatWork sweeps the rule over every
// non-typing state of the three pickers.
//
// The typing states are excluded on purpose and not by oversight: with a search
// box open every printable key "acts" by going into the query, so the reverse
// direction is meaningless there, and enter's claim is a CONDITION ("picks when
// one row is left") rather than a promise. Those states are covered by the
// wording tests below instead.
//
// This is the sweep the list screens have had since the first round and the New
// PO pickers never did — which is why a bar that named '/' on a frame where '/'
// erased the only repair key went five rounds unnoticed.
func TestPOPickers_PaneNamesExactlyTheKeysThatWork(t *testing.T) {
	type picker struct {
		name  string
		fake  func() *poPickFake
		enter func(*testing.T, Root) Root // reach the state under test
		bar   func(*PurchaseOrderCreateScreen) string
	}
	press := func(k string) func(*testing.T, Root) Root {
		return func(t *testing.T, r Root) Root { return key(t, r, poPickerKeyMsg(k)) }
	}
	// midFlight presses a key WITHOUT pumping its command, so the request is
	// genuinely still out — the working frame is a state the sweep has to reach
	// while it is real, not after it resolves.
	midFlight := func(pumped []string, k string) func(*testing.T, Root) Root {
		return func(t *testing.T, r Root) Root {
			for _, p := range pumped {
				r = key(t, r, poPickerKeyMsg(p))
			}
			next, _ := r.Update(poPickerKeyMsg(k))
			return next.(Root)
		}
	}
	pickers := []picker{
		{"items, loaded", func() *poPickFake { return &poPickFake{catalog: 8, pageSize: 8} },
			press("i"), (*PurchaseOrderCreateScreen).itemPickBar},
		{"items, empty catalog", func() *poPickFake { return &poPickFake{catalog: 0} },
			press("i"), (*PurchaseOrderCreateScreen).itemPickBar},
		{"items, failed", func() *poPickFake { return &poPickFake{catalog: 8, failItems: true} },
			press("i"), (*PurchaseOrderCreateScreen).itemPickBar},
		{"items, first walk in flight", func() *poPickFake { return &poPickFake{catalog: 8, pageSize: 2} },
			midFlight(nil, "i"), (*PurchaseOrderCreateScreen).itemPickBar},
		{"items, reload in flight", func() *poPickFake { return &poPickFake{catalog: 8, pageSize: 2} },
			midFlight([]string{"i"}, "r"), (*PurchaseOrderCreateScreen).itemPickBar},
		{"assets, loaded with a pager", func() *poPickFake { return &poPickFake{assets: 12, pageSize: 5} },
			press("a"), (*PurchaseOrderCreateScreen).assetPickBar},
		{"assets, empty", func() *poPickFake { return &poPickFake{assets: 0} },
			press("a"), (*PurchaseOrderCreateScreen).assetPickBar},
		{"assets, failed", func() *poPickFake { return &poPickFake{assets: 3, failAssets: true} },
			press("a"), (*PurchaseOrderCreateScreen).assetPickBar},
		{"assets, load in flight", func() *poPickFake { return &poPickFake{assets: 12, pageSize: 5} },
			midFlight(nil, "a"), (*PurchaseOrderCreateScreen).assetPickBar},
		{"assets, page load in flight", func() *poPickFake { return &poPickFake{assets: 12, pageSize: 5} },
			midFlight([]string{"a"}, "]"), (*PurchaseOrderCreateScreen).assetPickBar},
		{"reorder, loaded", func() *poPickFake { return &poPickFake{reorder: 6} },
			press("r"), (*PurchaseOrderCreateScreen).reorderPickBar},
		{"reorder, empty", func() *poPickFake { return &poPickFake{reorder: 0} },
			press("r"), (*PurchaseOrderCreateScreen).reorderPickBar},
		{"reorder, failed", func() *poPickFake { return &poPickFake{failReorder: true} },
			press("r"), (*PurchaseOrderCreateScreen).reorderPickBar},
		{"reorder, load in flight", func() *poPickFake { return &poPickFake{reorder: 6} },
			midFlight(nil, "r"), (*PurchaseOrderCreateScreen).reorderPickBar},
		// The supplier list is the fourth picker frame on this screen. It is
		// reached by backing out of the source chooser, and its loaded/loading
		// states have the same two directions to check.
		{"suppliers, loaded", func() *poPickFake { return &poPickFake{suppliers: 4} },
			press("b"), (*PurchaseOrderCreateScreen).supplierPickBar},
	}

	for _, p := range pickers {
		for _, height := range poPaneSizes {
			t.Run(fmt.Sprintf("%s at 80x%d", p.name, height), func(t *testing.T) {
				fresh := func(probe []string) (Root, *PurchaseOrderCreateScreen) {
					r, screen := poPickerAtSize(t, p.fake(), 80, height)
					r = p.enter(t, r)
					for _, k := range probe {
						next, _ := r.Update(poPickerKeyMsg(k))
						r = next.(Root)
					}
					return r, screen
				}

				_, screen := fresh(nil)
				bar := p.bar(screen)
				named := poPickerNamedKeys(t, bar)

				// Every claim the bar makes has to be READABLE on the pane it
				// is drawn on, at this height, or it is not a claim.
				for _, seg := range strings.Split(bar, " · ") {
					if !poPaneHasLine(t, screen, seg) {
						t.Errorf("the bar claims %q but the 80x%d pane does not carry it:\n%s",
							seg, height, strings.Join(poPaneLines(t, screen), "\n"))
					}
				}
				poAssertFits(t, p.name, screen)

				// Probed from more than one position, because j does nothing at
				// the bottom of a list and k nothing at the top: a key is dead
				// only if it does nothing from ANY of them. Same reasoning the
				// list sweep uses.
				for _, k := range poPickerVocabulary {
					acted := false
					for _, probe := range [][]string{nil, {"j"}} {
						pr, ps := fresh(probe)
						before := poPickerState(ps)
						_, cmd := pr.Update(poPickerKeyMsg(k))
						if poPickerState(ps) != before || poCmdActs(cmd) {
							acted = true
						}
					}
					switch {
					case named[k] && !acted:
						t.Errorf("%s names %q but pressing it changes nothing (bar: %q)", p.name, k, bar)
					case !named[k] && acted:
						t.Errorf("%s does not name %q, but pressing it acts (bar: %q)", p.name, k, bar)
					}
				}
			})
		}
	}
}

func poPickerKeyMsg(k string) tea.KeyMsg {
	switch k {
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "tab":
		return tea.KeyMsg{Type: tea.KeyTab}
	case "shift+tab":
		return tea.KeyMsg{Type: tea.KeyShiftTab}
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "ctrl+e":
		return tea.KeyMsg{Type: tea.KeyCtrlE}
	case "ctrl+x":
		return tea.KeyMsg{Type: tea.KeyCtrlX}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
}

// TestPOItemPicker_BarAndNoteNeverDisagreeAboutEnter: with the box open over
// SEVERAL matches the pane used to carry "enter picks the match" from the
// action bar and "enter closes the search" from the note, four rows apart —
// and enter did neither, because with more than one match it declines. The
// operator who trusts the bar presses enter expecting a staged line, which is
// the reported defect wearing a different hat.
func TestPOItemPicker_BarAndNoteNeverDisagreeAboutEnter(t *testing.T) {
	for _, height := range poPaneSizes {
		t.Run(fmt.Sprintf("height %d", height), func(t *testing.T) {
			fake := &poPickFake{catalog: 12, pageSize: 12}
			r, screen := poPickerAtSize(t, fake, 80, height)
			r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("i")})
			r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
			r = poType(t, r, "Widget 1") // 4 matches

			// Nothing on the pane may promise that enter picks here.
			poRejectPaneLine(t, screen, "enter picks the match")
			poRejectPaneLine(t, screen, "enter pick,")
			// The bar states the condition, once.
			poWantPaneLine(t, screen, "enter picks when one row is left")
			poAssertFits(t, "open box over several matches", screen)

			// And that is what enter does: it declines to guess.
			r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})
			if screen.phase == poPhaseLine || len(screen.lines) != 0 {
				t.Fatalf("enter over several matches staged a line (phase %v, %d line(s))",
					screen.phase, len(screen.lines))
			}

			// With exactly one match left it does pick, so the condition holds.
			r2, screen2 := poPickerAtSize(t, &poPickFake{catalog: 12, pageSize: 12}, 80, height)
			r2 = key(t, r2, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("i")})
			r2 = key(t, r2, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
			r2 = poType(t, r2, "Widget 12")
			poWantPaneLine(t, screen2, "enter picks when one row is left")
			r2 = key(t, r2, tea.KeyMsg{Type: tea.KeyEnter})
			if screen2.phase != poPhaseLine {
				t.Fatalf("enter over the one match did not pick (phase %v)", screen2.phase)
			}
			_ = r
		})
	}
}

// TestPOAssetPicker_PagerDoesNotStepOverAPageThatFailed: the failure frame keeps
// assetsHasNext from the page that DID load, so an ungated ']' increments past
// the page that just 500'd — and the retry silently skips it.
func TestPOAssetPicker_PagerDoesNotStepOverAPageThatFailed(t *testing.T) {
	fake := &poPickFake{assets: 12, pageSize: 5}
	r, screen := poPickerAt(t, fake, 80)
	r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
	if !screen.assetsHasNext {
		t.Fatalf("setup: wanted a next page, got %d asset(s)", len(screen.assets))
	}

	fake.mu.Lock()
	fake.failAssets = true
	fake.mu.Unlock()
	r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("]")})
	if screen.assetsErr == "" {
		t.Fatal("setup: the failing page did not fail")
	}
	failedPage := screen.assetsPage
	if !screen.assetsHasNext {
		t.Fatal("setup: this trace needs hasNext still set from the page that loaded")
	}

	before := fake.hits("/assets/")
	r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("]")})
	if screen.assetsPage != failedPage {
		t.Errorf("']' stepped from the failed page %d to %d — the retry would skip it",
			failedPage, screen.assetsPage)
	}
	if got := fake.hits("/assets/"); got != before {
		t.Errorf("']' fired %d more request(s) from a frame that does not name it", got-before)
	}
	// The frame still names the key that repairs it, and that key works.
	poWantPaneLine(t, screen, "/ retries with a search")
	fake.mu.Lock()
	fake.failAssets = false
	fake.mu.Unlock()
	r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	if !screen.assetsTyping {
		t.Fatal("the key the failure frame names did not open the search")
	}
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})
	if screen.assetsErr != "" {
		t.Errorf("the retry the frame named did not clear the failure: %q", screen.assetsErr)
	}
}

// TestPOSupplierPicker_KeysDeclineWhileTheListIsNotDrawn: the supplier list is
// the frame the whole order starts on, and while it is loading (or has failed)
// renderSupplierPhase draws nothing at all — yet the bar promised j/k and enter
// and enter answered with silence, which is the reported symptom exactly.
func TestPOSupplierPicker_KeysDeclineWhileTheListIsNotDrawn(t *testing.T) {
	for _, height := range poPaneSizes {
		t.Run(fmt.Sprintf("height %d", height), func(t *testing.T) {
			fake := &poPickFake{suppliers: 3}
			srv := httptest.NewServer(fake.handler())
			t.Cleanup(srv.Close)

			deps := Deps{OMS: omsapi.New(srv.URL), Ctx: context.Background()}
			screen := NewPurchaseOrderCreateScreen(deps)
			r := newTestRoot(screen)
			r.deps = deps
			next, _ := r.Update(tea.WindowSizeMsg{Width: 80, Height: height})
			r = next.(Root)
			if !screen.supplierLoading {
				t.Fatal("setup: the supplier load is not in flight")
			}

			// The bar promises only esc here, and j/k/enter say why they cannot.
			poRejectPaneLine(t, screen, "j/k move")
			poRejectPaneLine(t, screen, "enter commits")
			poWantPaneLine(t, screen, "esc cancels the order")
			poAssertFits(t, "suppliers, load in flight", screen)

			for _, k := range []tea.KeyMsg{
				{Type: tea.KeyRunes, Runes: []rune("j")},
				{Type: tea.KeyRunes, Runes: []rune("k")},
				{Type: tea.KeyEnter},
			} {
				next, cmd := r.Update(k)
				r = next.(Root)
				r = pump(t, r, cmd, 0)
				if screen.phase != poPhaseSupplier || screen.supplierID != 0 {
					t.Fatalf("%v acted while the supplier list was not drawn (phase %v, supplier %d)",
						k, screen.phase, screen.supplierID)
				}
				if out := r.View(); !strings.Contains(out, "still looking up the suppliers") {
					t.Errorf("%v mid-load said nothing:\n%s", k, out)
				}
			}

			// Once the list lands, the bar names them and they work.
			r = pump(t, r, screen.Init(), 0)
			poWantPaneLine(t, screen, "j/k move")
			poWantPaneLine(t, screen, "enter commits")
			r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
			want := screen.suppliers[screen.supplierCursor].ID
			r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})
			if screen.supplierID != want {
				t.Errorf("enter committed supplier %d, want the highlighted %d", screen.supplierID, want)
			}
		})
	}
}

// TestPOItemPicker_EnterOverAZeroMatchSearchMovesTheNote is the LAST arm of
// commitSearchedItem that still re-emitted the note already on the pane.
//
// The zero-match arm deliberately keeps the search box OPEN so the query can be
// edited in place, so enter there changed no rows, no cursor and — because the
// filter had already run on the keystroke before — no note. Character for
// character the same frame, with only the caret blinking: the original "it just
// kinda hangs there", surviving one arm away from its own fix. The assertion is
// the rendered note LINE before and after, which a caret cannot satisfy.
func TestPOItemPicker_EnterOverAZeroMatchSearchMovesTheNote(t *testing.T) {
	fake := &poPickFake{catalog: 12, pageSize: 12}
	r, screen := poPickerAt(t, fake, 80)
	r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("i")})
	r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	r = poType(t, r, "zzz")

	if len(screen.itemSuppliers) != 0 {
		t.Fatalf("setup: %d row(s) matched a query nothing should match", len(screen.itemSuppliers))
	}
	before := strings.Join(poNoteLines(t, screen.itemSuppliersNote), "\n")
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})
	after := strings.Join(poNoteLines(t, screen.itemSuppliersNote), "\n")

	if before == after {
		t.Errorf("enter over a zero-match search left the note unchanged — only the caret moved:\n%s", after)
	}
	if !screen.itemSuppliersTyping {
		t.Error("enter closed the box on a zero-match search — the query can no longer be edited in place")
	}
	// It says what the key DID, and still carries the two facts that tell the
	// operator whether to retype or to give up on this supplier.
	poWantPaneLine(t, screen, "searched again")
	poWantPaneLine(t, screen, "in catalog")
	if screen.phase != poPhaseItemPick || len(screen.lines) != 0 {
		t.Fatalf("a zero-match enter staged something (phase %v, %d line(s))", screen.phase, len(screen.lines))
	}
	poAssertFits(t, "zero-match enter", screen)
}

// TestPOItemPicker_SearchBoxOverAnUnansweredCatalogDoesNotPromiseAPick: the
// box opens mid-walk on purpose — the rows are on their way and the query is
// applied when they land — but commitSearchedItem declines at its first line
// while the catalog is not on screen. The pane said the opposite twice: the
// action bar's typing arm named "enter picks when one row is left" and the note
// '/' posts named "enter picks the ONE match", so both of the pane's claims
// about enter were false for as long as the walk was out.
//
// The second half is the decline itself. renderItemPick's loading branch
// returned after the working line without drawing the note, so the press that
// declined produced no visible change at all — the same defect, one frame over.
func TestPOItemPicker_SearchBoxOverAnUnansweredCatalogDoesNotPromiseAPick(t *testing.T) {
	fake := &poPickFake{catalog: 40, pageSize: 5} // 8 sequential pages
	r, screen := poPickerAt(t, fake, 80)

	next, load := r.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("i")})
	r = next.(Root)
	next, _ = r.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	r = next.(Root)
	if !screen.itemSuppliersTyping || !screen.itemSuppliersLoad {
		t.Fatalf("setup: want the box open over an in-flight walk (typing=%v load=%v)",
			screen.itemSuppliersTyping, screen.itemSuppliersLoad)
	}

	// Neither surface promises the key that cannot act, and both name the one
	// that can.
	poRejectPaneLine(t, screen, "enter picks")
	poRejectPaneLine(t, screen, "enter picks when one row is left")
	poWantPaneLine(t, screen, "esc closes the search")

	// The note the box opened with is not a conclusion, so enter has something
	// to change. Without the render fix neither note reaches the pane and this
	// comparison sees the same frame twice.
	beforePane := strings.Join(poPaneLines(t, screen), "\n")
	next, cmd := r.Update(tea.KeyMsg{Type: tea.KeyEnter})
	r = next.(Root)
	if !screen.itemSuppliersLoad {
		t.Fatal("enter finished the walk — this assertion needs it in flight")
	}
	if beforePane == strings.Join(poPaneLines(t, screen), "\n") {
		t.Errorf("enter mid-walk redrew an identical pane — the decline is invisible:\n%s", beforePane)
	}
	// The working line still speaks first; the decline is drawn UNDER it, not
	// in place of it, so the frame never trades the fact for the reply.
	poWantPaneLine(t, screen, "Looking up the items")
	poWantPaneLine(t, screen, "still looking up")
	if screen.phase != poPhaseItemPick || len(screen.lines) != 0 {
		t.Fatalf("enter mid-walk staged something (phase %v, %d line(s))", screen.phase, len(screen.lines))
	}
	poAssertFits(t, "enter mid-walk with the box open", screen)

	// Typing mid-walk keeps the query and never reaches a verdict either.
	r = pump(t, r, cmd, 0)
	r = poType(t, r, "Widget 38")
	if !screen.itemSuppliersLoad {
		t.Fatal("the walk finished early — this test needs it in flight")
	}
	poWantPaneLine(t, screen, "still looking up")
	poRejectPaneLine(t, screen, "in catalog")

	// And enter AFTER typing is the case the first comparison above cannot
	// reach: every keystroke leaves catalogVerdict's own wording on the pane,
	// so a gate that answers with the same wording answers with the note the
	// rune before it already drew. The enter arm never touches the textinput,
	// so not even the caret moves — the reported hang, on the search box the
	// report was about. Compare the NOTE LINE, which no caret can satisfy.
	beforeNote := strings.Join(poNoteLines(t, screen.itemSuppliersNote), "\n")
	next, cmd2 := r.Update(tea.KeyMsg{Type: tea.KeyEnter})
	r = next.(Root)
	if !screen.itemSuppliersLoad {
		t.Fatal("the second enter finished the walk — this assertion needs it in flight")
	}
	if beforeNote == strings.Join(poNoteLines(t, screen.itemSuppliersNote), "\n") {
		t.Errorf("enter after typing mid-walk re-emitted the note already on the pane:\n%s", beforeNote)
	}
	poWantPaneLine(t, screen, "searched again")
	poWantPaneLine(t, screen, "Looking up the items")
	if screen.phase != poPhaseItemPick || len(screen.lines) != 0 {
		t.Fatalf("enter mid-walk staged something (phase %v, %d line(s))", screen.phase, len(screen.lines))
	}
	poAssertFits(t, "enter after typing mid-walk", screen)
	r = pump(t, r, cmd2, 0)

	// When the rows land the promise comes back, and it is true.
	r = pump(t, r, load, 0)
	if screen.itemSuppliersLoad {
		t.Fatal("the walk never finished")
	}
	poWantPaneLine(t, screen, "enter picks")
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})
	if screen.phase != poPhaseLine {
		t.Fatalf("the key the settled pane named did not work (phase %v)", screen.phase)
	}
	if got := screen.lineInputs[poLineFieldDesc].Value(); got != "Widget 38" {
		t.Errorf("staged %q, want Widget 38", got)
	}
}

// TestPOAssetPicker_SearchBoxDoesNotRunASecondSearchOverTheFirst: the asset
// reply carries no request generation — only the supplierID the stale-reply
// guard reads — so two searches in flight for two different queries resolve in
// whatever order the network returns them, and the rows left on screen can
// belong to a query the search box no longer holds. An operator picking the
// asset they can SEE and getting a different one is the wrong purchase order.
//
// So the typing arm declines while a lookup is out, and both surfaces stop
// naming enter for exactly as long as that is true. The failure frame is not
// gated: nothing is in flight there, so enter out of the box is the retry the
// bar's "/ retries with a search" promised one frame earlier.
func TestPOAssetPicker_SearchBoxDoesNotRunASecondSearchOverTheFirst(t *testing.T) {
	fake := &poPickFake{assets: 3}
	r, screen := poPickerAt(t, fake, 80)

	next, load := r.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
	r = next.(Root)
	next, _ = r.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	r = next.(Root)
	if !screen.assetsTyping || !screen.assetsLoading {
		t.Fatalf("setup: want the box open over an in-flight lookup (typing=%v loading=%v)",
			screen.assetsTyping, screen.assetsLoading)
	}
	poRejectPaneLine(t, screen, "enter runs the search")
	poWantPaneLine(t, screen, "esc closes the search")

	r = poType(t, r, "Lathe 2")
	beforePane := strings.Join(poPaneLines(t, screen), "\n")
	next, cmd := r.Update(tea.KeyMsg{Type: tea.KeyEnter})
	r = next.(Root)
	// Run whatever enter returned: if it fired a lookup the fake records it.
	// The first lookup is still un-pumped, so the count here is exactly the
	// number of requests enter itself sent.
	r = pump(t, r, cmd, 0)
	if got := fake.hits("/assets/"); got != 0 {
		t.Errorf("enter fired %d asset lookup(s) over the one already in flight", got)
	}
	if !screen.assetsTyping {
		t.Error("the declining enter closed the search box anyway")
	}
	if beforePane == strings.Join(poPaneLines(t, screen), "\n") {
		t.Errorf("enter over an in-flight lookup redrew an identical pane:\n%s", beforePane)
	}
	poWantPaneLine(t, screen, "Looking up the assets")
	poWantPaneLine(t, screen, "still looking up")
	poAssertFits(t, "enter over an in-flight asset lookup", screen)

	// The first reply lands, and the key comes back — named and working.
	r = pump(t, r, load, 0)
	if screen.assetsLoading {
		t.Fatal("the lookup never finished")
	}
	settled := fake.hits("/assets/")
	poWantPaneLine(t, screen, "enter runs the search")
	r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})
	if got := fake.hits("/assets/"); got != settled+1 {
		t.Fatalf("the key the settled pane named did not search (%d requests, want %d)", got, settled+1)
	}
}

// TestPOItemPicker_EnterOverAnEmptyFilteredListMovesTheNote is the browse-path
// sibling of the search-box arm: with the box CLOSED over a filter that matched
// nothing, enter reaches commitHighlightedItem, which used to answer with
// reportItemFilterState("") — itemFilterOrVerdict re-entered with identical
// arguments, so the note came back character for character. The filter row
// above it is a blurred textinput with no caret, so the pane was byte-for-byte
// what it already was and only the four-second status flash moved.
//
// The reload is what makes the FIRST press silent: it clears the note and keeps
// the query, so the reply words the filter outcome and the enter after it words
// the same one again.
func TestPOItemPicker_EnterOverAnEmptyFilteredListMovesTheNote(t *testing.T) {
	fake := &poPickFake{catalog: 12, pageSize: 12}
	r, screen := poPickerAt(t, fake, 80)
	r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("i")})
	r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	r = poType(t, r, "zzz")
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEsc})
	r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})

	if screen.itemSuppliersTyping {
		t.Fatal("setup: the search box is still open — this is the browse path")
	}
	if len(screen.itemSuppliers) != 0 || len(screen.itemSuppliersAll) != 12 {
		t.Fatalf("setup: want a filtered-empty list over a loaded catalog (%d of %d)",
			len(screen.itemSuppliers), len(screen.itemSuppliersAll))
	}
	before := strings.Join(poNoteLines(t, screen.itemSuppliersNote), "\n")
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})
	after := strings.Join(poNoteLines(t, screen.itemSuppliersNote), "\n")

	if before == after {
		t.Errorf("enter over an empty filtered list redrew the same note:\n%s", after)
	}
	poWantPaneLine(t, screen, "nothing to pick")
	poWantPaneLine(t, screen, "in catalog")
	if screen.phase != poPhaseItemPick || len(screen.lines) != 0 {
		t.Fatalf("enter over an empty list staged something (phase %v, %d line(s))", screen.phase, len(screen.lines))
	}
	poAssertFits(t, "enter over an empty filtered list", screen)
}

// TestPOAssetPicker_EnterOverAnEmptySearchResultMovesTheNote is the same arm in
// the asset picker: the reply that found nothing and the enter that declines to
// pick from it worded the outcome identically.
func TestPOAssetPicker_EnterOverAnEmptySearchResultMovesTheNote(t *testing.T) {
	fake := &poPickFake{assets: 3}
	r, screen := poPickerAt(t, fake, 80)
	r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
	r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	r = poType(t, r, "zzz")
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})

	if len(screen.assets) != 0 || screen.assetsLoading {
		t.Fatalf("setup: want a settled empty search result (%d row(s), loading=%v)",
			len(screen.assets), screen.assetsLoading)
	}
	before := strings.Join(poNoteLines(t, screen.assetsNote), "\n")
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})
	after := strings.Join(poNoteLines(t, screen.assetsNote), "\n")

	if before == after {
		t.Errorf("enter over an empty asset search redrew the same note:\n%s", after)
	}
	poWantPaneLine(t, screen, "nothing to pick")
	if screen.phase != poPhaseAssetPick || len(screen.lines) != 0 {
		t.Fatalf("enter over no assets staged something (phase %v, %d line(s))", screen.phase, len(screen.lines))
	}
	poAssertFits(t, "enter over an empty asset search", screen)
}

// TestPOAssetPicker_ClosingTheSearchMidLookupSaysWhatEscDid: esc out of the box
// while the lookup is still out lands on the verdict, which the declining enter
// before it had already put on the pane. The query stays in the box so the
// "search:" row does not go with it, and without a lead the only thing that
// moved was the caret.
func TestPOAssetPicker_ClosingTheSearchMidLookupSaysWhatEscDid(t *testing.T) {
	fake := &poPickFake{assets: 3}
	r, screen := poPickerAt(t, fake, 80)
	next, load := r.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
	r = next.(Root)
	next, _ = r.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	r = next.(Root)
	r = poType(t, r, "Lathe")
	next, _ = r.Update(tea.KeyMsg{Type: tea.KeyEnter}) // declines, lookup still out
	r = next.(Root)
	if !screen.assetsLoading || !screen.assetsTyping {
		t.Fatalf("setup: want the box open over an in-flight lookup (typing=%v loading=%v)",
			screen.assetsTyping, screen.assetsLoading)
	}

	before := strings.Join(poNoteLines(t, screen.assetsNote), "\n")
	next, _ = r.Update(tea.KeyMsg{Type: tea.KeyEsc})
	r = next.(Root)
	after := strings.Join(poNoteLines(t, screen.assetsNote), "\n")

	if screen.assetsTyping {
		t.Fatal("esc did not close the search box")
	}
	if before == after {
		t.Errorf("esc mid-lookup redrew the same note:\n%s", after)
	}
	poWantPaneLine(t, screen, "search closed")
	poWantPaneLine(t, screen, "Looking up the assets")
	poAssertFits(t, "esc out of the asset search mid-lookup", screen)
	r = pump(t, r, load, 0)
	_ = r
}

// TestPOAssetPicker_ReenteringDoesNotRaceTheLookupAlreadyOut is the race the
// search-box gate did not close: 'b' is named on the working frame and has to
// keep working, so leaving mid-lookup and pressing 'a' again fired an
// unfiltered page-1 request over a search that was still out. Whichever landed
// last won, and the losing order painted a FILTERED SUBSET as the supplier's
// whole asset list — empty search box, no "search:" row, "N asset(s)" with a
// green tick.
func TestPOAssetPicker_ReenteringDoesNotRaceTheLookupAlreadyOut(t *testing.T) {
	fake := &poPickFake{assets: 3}
	r, screen := poPickerAt(t, fake, 80)
	r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")}) // first load settles
	r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	r = poType(t, r, "Lathe 2")

	next, search := r.Update(tea.KeyMsg{Type: tea.KeyEnter}) // search out, un-pumped
	r = next.(Root)
	if !screen.assetsLoading {
		t.Fatal("setup: the search did not go out")
	}
	next, _ = r.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("b")})
	r = next.(Root)
	if screen.phase != poPhaseSource {
		t.Fatalf("setup: 'b' did not leave the picker (phase %v)", screen.phase)
	}

	sent := fake.hits("/assets/")
	next, reenter := r.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
	r = next.(Root)
	r = pump(t, r, reenter, 0)

	if got := fake.hits("/assets/"); got != sent {
		t.Errorf("re-entering fired %d lookup(s) over the one already out", got-sent)
	}
	if screen.phase != poPhaseAssetPick {
		t.Fatalf("'a' did not open the picker (phase %v)", screen.phase)
	}
	if got := screen.assetsSearch.Value(); got != "Lathe 2" {
		t.Errorf("re-entry cleared the query the in-flight request owns: %q", got)
	}
	poWantPaneLine(t, screen, "Looking up the assets")

	// The one reply that IS out lands, and the rows match the box.
	r = pump(t, r, search, 0)
	if screen.assetsLoading {
		t.Fatal("the lookup never finished")
	}
	if len(screen.assets) != 1 {
		t.Errorf("the settled list holds %d row(s), want the 1 match for the query in the box", len(screen.assets))
	}
	poWantPaneLine(t, screen, "search: Lathe 2")
	poAssertFits(t, "re-entered the asset picker mid-lookup", screen)
}

// TestPOReorderPicker_ReenteringDoesNotRaceTheLookupAlreadyOut is the same
// re-entry shape on the third picker. Its two replies carry the same rows, so
// nothing is painted wrong — but the second request is still one the first was
// already going to answer, and the guard belongs at every load site or the next
// parameter added to the call makes it the asset bug again.
func TestPOReorderPicker_ReenteringDoesNotRaceTheLookupAlreadyOut(t *testing.T) {
	fake := &poPickFake{reorder: 3}
	r, screen := poPickerAt(t, fake, 80)

	next, load := r.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
	r = next.(Root)
	if !screen.reorderLoading {
		t.Fatal("setup: the reorder lookup did not go out")
	}
	next, _ = r.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("b")})
	r = next.(Root)

	sent := fake.hits("/reorder_data/")
	next, reenter := r.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
	r = next.(Root)
	r = pump(t, r, reenter, 0)

	if got := fake.hits("/reorder_data/"); got != sent {
		t.Errorf("re-entering fired %d reorder lookup(s) over the one already out", got-sent)
	}
	if screen.phase != poPhaseReorderPick {
		t.Fatalf("'r' did not open the picker (phase %v)", screen.phase)
	}
	poWantPaneLine(t, screen, "Looking up what")

	r = pump(t, r, load, 0)
	if screen.reorderLoading {
		t.Fatal("the lookup never finished")
	}
	if len(screen.reorderItems) != 3 {
		t.Errorf("the settled queue holds %d row(s), want 3", len(screen.reorderItems))
	}
}

// TestPOAssetPicker_ReenteringDoesNotPaintTheLastLookupOverThisOne: the source
// chooser's 'a' arm reset the query, the page and the typing flag but not the
// note, so re-entering the picker drew a settled "✓ 3 asset(s) · enter picks
// the highlighted row" UNDER the working line of a request still out — a green
// tick on a count belonging to a reply nobody asked for again, naming a key the
// working frame has just gated off.
func TestPOAssetPicker_ReenteringDoesNotPaintTheLastLookupOverThisOne(t *testing.T) {
	fake := &poPickFake{assets: 3}
	r, screen := poPickerAt(t, fake, 80)
	r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
	if len(screen.assets) != 3 {
		t.Fatalf("setup: the first lookup returned %d row(s), want 3", len(screen.assets))
	}
	poWantPaneLine(t, screen, "3 asset(s)")

	next, _ := r.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("b")})
	r = next.(Root)
	next, reload := r.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
	r = next.(Root)
	if !screen.assetsLoading {
		t.Fatal("setup: re-entering did not start a fresh lookup")
	}

	poWantPaneLine(t, screen, "Looking up the assets")
	poRejectPaneLine(t, screen, "3 asset(s)")
	poRejectPaneLine(t, screen, "enter picks the highlighted row")
	poAssertFits(t, "re-entered the asset picker", screen)

	r = pump(t, r, reload, 0)
	if screen.assetsLoading {
		t.Fatal("the lookup never finished")
	}
	poWantPaneLine(t, screen, "3 asset(s)")
}

// TestPOSupplierPicker_DecliningKeysMoveTheBody: renderSupplierPhase returned
// "" while the supplier lookup was out and after it failed, so the gate added
// for those states answered j/k/enter/tab into the four-second flash and left
// the pane exactly as it was — the standard the item and asset pickers are held
// to, applied to the frame that chooses the order's supplier.
func TestPOSupplierPicker_DecliningKeysMoveTheBody(t *testing.T) {
	states := []struct {
		name  string
		setup func(*PurchaseOrderCreateScreen)
		says  string
	}{
		{"lookup still out", func(s *PurchaseOrderCreateScreen) {
			s.supplierLoading = true
		}, "still looking up the suppliers"},
		{"lookup failed", func(s *PurchaseOrderCreateScreen) {
			s.supplierLoading = false
			s.supplierLoadErr = "suppliers exploded"
		}, "loading suppliers failed"},
		{"none configured", func(s *PurchaseOrderCreateScreen) {
			s.supplierLoading = false
			s.suppliers = nil
		}, "no suppliers are configured"},
	}
	keys := []tea.KeyMsg{
		{Type: tea.KeyRunes, Runes: []rune("j")},
		{Type: tea.KeyEnter},
	}

	for _, st := range states {
		for _, k := range keys {
			for _, h := range poPaneSizes {
				s := NewPurchaseOrderCreateScreen(Deps{})
				s.phase = poPhaseSupplier
				s.terminalHeight = h
				st.setup(s)

				before := strings.Join(poPaneLinesAt(t, s, h), "\n")
				_, cmd := s.Update(k)
				if cmd == nil {
					t.Fatalf("%s / %s at 80x%d: the key said nothing at all", st.name, k.String(), h)
				}
				after := strings.Join(poPaneLinesAt(t, s, h), "\n")
				if before == after {
					t.Errorf("%s / %s at 80x%d: the pane did not move:\n%s", st.name, k.String(), h, after)
				}
				if !poPaneHasLine(t, s, st.says) {
					t.Errorf("%s / %s at 80x%d: the body does not say why:\n%s", st.name, k.String(), h, after)
				}
				poAssertFits(t, fmt.Sprintf("%s at 80x%d", st.name, h), s)
			}
		}
	}
}

// TestPOReorderPicker_DecliningKeysMoveTheBodyMidLookup is the reorder half of
// the same rule: its loading and failure frames drew one line each and every
// gated key answered into the flash alone.
func TestPOReorderPicker_DecliningKeysMoveTheBodyMidLookup(t *testing.T) {
	states := []struct {
		name  string
		setup func(*PurchaseOrderCreateScreen)
		says  string
	}{
		{"lookup still out", func(s *PurchaseOrderCreateScreen) {
			s.reorderLoading = true
		}, "still looking up what"},
		{"lookup failed", func(s *PurchaseOrderCreateScreen) {
			s.reorderLoadErr = "reorder exploded"
		}, "reading the reorder queue failed"},
	}
	keys := []tea.KeyMsg{
		{Type: tea.KeyRunes, Runes: []rune("j")},
		{Type: tea.KeyRunes, Runes: []rune(" ")},
		{Type: tea.KeyEnter},
	}

	for _, st := range states {
		for _, k := range keys {
			for _, h := range poPaneSizes {
				s := NewPurchaseOrderCreateScreen(Deps{})
				s.phase = poPhaseReorderPick
				s.supplierID = 1
				s.terminalHeight = h
				st.setup(s)

				before := strings.Join(poPaneLinesAt(t, s, h), "\n")
				_, cmd := s.Update(k)
				if cmd == nil {
					t.Fatalf("%s / %s at 80x%d: the key said nothing at all", st.name, k.String(), h)
				}
				after := strings.Join(poPaneLinesAt(t, s, h), "\n")
				if before == after {
					t.Errorf("%s / %s at 80x%d: the pane did not move:\n%s", st.name, k.String(), h, after)
				}
				if !poPaneHasLine(t, s, st.says) {
					t.Errorf("%s / %s at 80x%d: the body does not say why:\n%s", st.name, k.String(), h, after)
				}
				poAssertFits(t, fmt.Sprintf("%s at 80x%d", st.name, h), s)
			}
		}
	}
}

// TestPOPickers_EveryGatedKeyMovesTheBody is the by-construction check behind
// the rule: for the item and asset pickers, in each state where the frame draws
// no list, press every key the gate intercepts and require the clipped pane to
// change. It completes the matrix the supplier and reorder sweeps above cover
// for the other two frames.
//
// The states are built from a settled list and then pushed off-screen, so the
// note already on the pane is the SETTLED one — which is how the reported hang
// survived five rounds: each fix made the transition into a state visible and
// left the arm that answers from inside it re-emitting what was already there.
func TestPOPickers_EveryGatedKeyMovesTheBody(t *testing.T) {
	rows := []omsapi.ItemSupplier{{ID: 1, ItemName: "Widget 1"}, {ID: 2, ItemName: "Widget 2"}}
	assets := []omsapi.Asset{{ID: "as-1", Name: "Lathe 1"}, {ID: "as-2", Name: "Lathe 2"}}

	cases := []struct {
		name  string
		setup func(*PurchaseOrderCreateScreen)
		keys  []tea.KeyMsg
	}{
		{"items, walk in flight", func(s *PurchaseOrderCreateScreen) {
			s.phase = poPhaseItemPick
			s.itemSuppliersAll, s.itemSuppliers, s.itemSuppliersFor = rows, rows, 1
			s.itemSuppliersLoad = true
			s.itemSuppliersNote = s.catalogVerdict("")
		}, []tea.KeyMsg{
			{Type: tea.KeyRunes, Runes: []rune("j")},
			{Type: tea.KeyRunes, Runes: []rune("k")},
			{Type: tea.KeyEnter},
			{Type: tea.KeyRunes, Runes: []rune("r")},
		}},
		{"items, walk failed", func(s *PurchaseOrderCreateScreen) {
			s.phase = poPhaseItemPick
			s.itemSuppliersAll, s.itemSuppliers, s.itemSuppliersFor = rows, rows, 1
			s.itemSuppliersErr = "items exploded"
			s.itemSuppliersNote = s.catalogVerdict("")
		}, []tea.KeyMsg{
			{Type: tea.KeyRunes, Runes: []rune("j")},
			{Type: tea.KeyEnter},
			{Type: tea.KeyRunes, Runes: []rune("/")},
		}},
		{"items, search box over a walk in flight", func(s *PurchaseOrderCreateScreen) {
			s.phase = poPhaseItemPick
			s.itemSuppliersAll, s.itemSuppliers, s.itemSuppliersFor = rows, rows, 1
			s.itemSuppliersTyping = true
			s.itemSuppliersSearch.SetValue("Widget 1")
			s.applyItemSupplierFilter()
			s.itemSuppliersLoad = true
			s.itemSuppliersNote = s.itemFilterOrVerdict("")
		}, []tea.KeyMsg{{Type: tea.KeyEnter}}},
		{"assets, lookup in flight", func(s *PurchaseOrderCreateScreen) {
			s.phase = poPhaseAssetPick
			s.assets, s.assetsHasNext, s.assetsPage = assets, true, 2
			s.assetsLoading = true
			_ = s.assetVerdictNote("")
		}, []tea.KeyMsg{
			{Type: tea.KeyRunes, Runes: []rune("j")},
			{Type: tea.KeyEnter},
			{Type: tea.KeyRunes, Runes: []rune("]")},
			{Type: tea.KeyRunes, Runes: []rune("[")},
		}},
		{"assets, lookup failed", func(s *PurchaseOrderCreateScreen) {
			s.phase = poPhaseAssetPick
			s.assets, s.assetsHasNext, s.assetsPage = assets, true, 2
			s.assetsErr = "assets exploded"
			_ = s.assetVerdictNote("")
		}, []tea.KeyMsg{
			{Type: tea.KeyRunes, Runes: []rune("j")},
			{Type: tea.KeyEnter},
			{Type: tea.KeyRunes, Runes: []rune("]")},
		}},
		{"assets, search box over a lookup in flight", func(s *PurchaseOrderCreateScreen) {
			s.phase = poPhaseAssetPick
			s.assets = assets
			s.assetsTyping = true
			s.assetsSearch.SetValue("Lathe")
			s.assetsLoading = true
			_ = s.assetVerdictNote("")
		}, []tea.KeyMsg{{Type: tea.KeyEnter}, {Type: tea.KeyEsc}}},
	}

	for _, tc := range cases {
		for _, k := range tc.keys {
			for _, h := range poPaneSizes {
				name := fmt.Sprintf("%s/%s/80x%d", tc.name, k.String(), h)
				t.Run(name, func(t *testing.T) {
					s := NewPurchaseOrderCreateScreen(Deps{})
					s.supplierID = 1
					s.suppliers = []omsapi.Supplier{{ID: 1, Name: "Acme Supply"}}
					s.terminalHeight = h
					tc.setup(s)

					before := strings.Join(poPaneLinesAt(t, s, h), "\n")
					next, cmd := s.Update(k)
					s = next.(*PurchaseOrderCreateScreen)
					if cmd == nil {
						t.Fatalf("the key was answered with nothing at all — this is the hang")
					}
					if before == strings.Join(poPaneLinesAt(t, s, h), "\n") {
						t.Errorf("the key re-emitted the pane already on screen:\n%s", before)
					}
					poAssertFits(t, name, s)
				})
			}
		}
	}
}

// TestPOAssetPicker_EscOutOfAnUncommittedSearchDoesNotReportAResult: the asset
// search is SERVER-side and runs only on enter, so a box the operator typed
// into and escaped without committing has produced no result at all. The note
// used to read the live textinput and conclude "no asset matches
// \"hovercraft\"" against a supplier that simply has no assets on file —
// found-nothing stated where could-not-tell is the fact, pointing at '/' to
// retype when the only useful key is 'b'.
func TestPOAssetPicker_EscOutOfAnUncommittedSearchDoesNotReportAResult(t *testing.T) {
	fake := &poPickFake{assets: 0}
	r, screen := poPickerAt(t, fake, 80)
	r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
	if len(screen.assets) != 0 || screen.assetsLoading {
		t.Fatalf("setup: want a settled empty asset list (%d row(s), loading=%v)",
			len(screen.assets), screen.assetsLoading)
	}
	poWantPaneLine(t, screen, "has no assets on file")

	sent := fake.hits("/assets/")
	r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	r = poType(t, r, "hovercraft")
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEsc})

	if got := fake.hits("/assets/"); got != sent {
		t.Fatalf("setup: typing fired %d lookup(s); this test needs the query UNSUBMITTED", got-sent)
	}
	poRejectPaneLine(t, screen, "no asset matches")
	poWantPaneLine(t, screen, "was never run")
	// The box is SHUT here, so enter stages rather than searching. A note that
	// names it as the way to run the search points the operator at the key that
	// puts an unrelated row on the order.
	poRejectPaneLine(t, screen, "enter runs it")
	poAssertFits(t, "esc out of an uncommitted asset search", screen)

	// And the pager asks for a PAGE, not for the text nobody submitted.
	r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("]")})
	for _, req := range fake.seen() {
		if strings.Contains(req, "search=hovercraft") {
			t.Errorf("a lookup carried the uncommitted query: %s", req)
		}
	}
	_ = r
}

// TestPOAssetPicker_PagerCarriesTheQueryThatWasRun is the committed half: once
// enter has submitted a search, ']' must page THAT search rather than dropping
// it, so the two halves of the fix cannot be satisfied by ignoring the box
// entirely.
func TestPOAssetPicker_PagerCarriesTheQueryThatWasRun(t *testing.T) {
	fake := &poPickFake{assets: 3}
	r, screen := poPickerAt(t, fake, 80)
	r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
	r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	r = poType(t, r, "Lathe")
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})
	if screen.assetsQuery != "Lathe" {
		t.Fatalf("setup: the committed query is %q, want \"Lathe\"", screen.assetsQuery)
	}

	screen.assetsHasNext = true
	r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("]")})

	last := ""
	for _, req := range fake.seen() {
		if strings.Contains(req, "/assets/") {
			last = req
		}
	}
	if !strings.Contains(last, "search=Lathe") {
		t.Errorf("paging dropped the committed search: %s", last)
	}
	_ = r
}

// TestPOItemPicker_ReenteringMidWalkKeepsTheTypedQuery: the 'i' arm cleared the
// search box before its own in-flight guard, so 'i' → '/' → type → 'b' → 'i'
// while the catalog walk was still out threw the operator's query away with
// nothing on the pane saying it had gone. The guard belongs ahead of the reset,
// as it does on the 'a' arm.
func TestPOItemPicker_ReenteringMidWalkKeepsTheTypedQuery(t *testing.T) {
	fake := &poPickFake{catalog: 40, pageSize: 5}
	r, screen := poPickerAt(t, fake, 80)

	next, load := r.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("i")})
	r = next.(Root)
	next, _ = r.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	r = next.(Root)
	r = poType(t, r, "Widget 38")
	next, _ = r.Update(tea.KeyMsg{Type: tea.KeyEsc})
	r = next.(Root)
	next, _ = r.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("b")})
	r = next.(Root)
	if screen.phase != poPhaseSource {
		t.Fatalf("setup: 'b' did not leave the picker (phase %v)", screen.phase)
	}

	sent := fake.hits("/item-suppliers/")
	next, reenter := r.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("i")})
	r = next.(Root)
	r = pump(t, r, reenter, 0)

	if !screen.itemSuppliersLoad {
		t.Fatal("the walk finished early — this test needs it in flight")
	}
	if got := fake.hits("/item-suppliers/"); got != sent {
		t.Errorf("re-entering fired %d walk(s) over the one already out", got-sent)
	}
	if got := screen.itemSuppliersSearch.Value(); got != "Widget 38" {
		t.Errorf("re-entering mid-walk discarded the typed query: %q", got)
	}

	// The query survives to answer the rows when they land.
	r = pump(t, r, load, 0)
	if screen.itemSuppliersLoad {
		t.Fatal("the walk never finished")
	}
	if len(screen.itemSuppliers) != 1 {
		t.Errorf("the preserved query matched %d row(s), want 1", len(screen.itemSuppliers))
	}
}

// TestPOSupplierPicker_ListFrameDrawsItsNote: the supplier picker's LIST frame
// was the one frame of the four pickers that returned without drawing its
// pickerNote, so a note set while rows were on screen would have been swallowed
// whole — the same silent keypress the other three frames were fixed for.
func TestPOSupplierPicker_ListFrameDrawsItsNote(t *testing.T) {
	for _, h := range poPaneSizes {
		s := NewPurchaseOrderCreateScreen(Deps{})
		s.phase = poPhaseSupplier
		s.terminalHeight = h
		s.supplierLoading = false
		s.suppliers = []omsapi.Supplier{{ID: 1, Name: "Acme Supply"}, {ID: 2, Name: "Beta Tool"}}

		before := strings.Join(poPaneLinesAt(t, s, h), "\n")
		_ = s.supplierNote.say("nothing to commit · the list is right here", StatusWarn)
		after := strings.Join(poPaneLinesAt(t, s, h), "\n")

		if before == after {
			t.Errorf("80x%d: the supplier list frame swallowed its note:\n%s", h, after)
		}
		if !poPaneHasLine(t, s, "nothing to commit") {
			t.Errorf("80x%d: the note is not on the pane:\n%s", h, after)
		}
		poAssertFits(t, fmt.Sprintf("supplier list with a note at 80x%d", h), s)
	}
}

// TestPOAssetPicker_UncommittedEscOverRowsNamesNeitherEnterNorTheWrongList is
// the dangerous half the assets:0 fixture cannot reach: with rows on the pane,
// enter STAGES the highlighted asset, so a note reading "enter runs it" sends
// an operator who asked for a search away with a line from an unrelated row.
func TestPOAssetPicker_UncommittedEscOverRowsNamesNeitherEnterNorTheWrongList(t *testing.T) {
	fake := &poPickFake{assets: 3}
	r, screen := poPickerAt(t, fake, 80)
	r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
	if len(screen.assets) != 3 {
		t.Fatalf("setup: want 3 rows on the pane, got %d", len(screen.assets))
	}

	r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	r = poType(t, r, "hovercraft")
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEsc})

	poRejectPaneLine(t, screen, "enter runs it")
	poWantPaneLine(t, screen, "was never run")
	// …and the pane says what the rows on it DO answer.
	poWantPaneLine(t, screen, "whole list")
	// The label may not present the unsubmitted draft as the result set.
	poRejectPaneLine(t, screen, "search: hovercraft")
	poWantPaneLine(t, screen, "search (not run): hovercraft")
	poAssertFits(t, "uncommitted esc over rows", screen)

	// Paging replaces the note; the label must still not claim the draft ran.
	screen.assetsHasNext = true
	r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("]")})
	poRejectPaneLine(t, screen, "search: hovercraft")
	poWantPaneLine(t, screen, "search (not run): hovercraft")
	for _, req := range fake.seen() {
		if strings.Contains(req, "search=hovercraft") {
			t.Errorf("a lookup carried the uncommitted query: %s", req)
		}
	}
	poAssertFits(t, "paged after an uncommitted esc", screen)
}

// TestPOAssetPicker_EmptyingACommittedSearchSaysWhatTheRowsStillAnswer: commit
// a query, reopen the box, backspace it empty and escape. Quoting the empty box
// named no query at all, and the "search:" row vanished with it, so the pane
// showed a list still filtered by the committed query with nothing saying so.
func TestPOAssetPicker_EmptyingACommittedSearchSaysWhatTheRowsStillAnswer(t *testing.T) {
	fake := &poPickFake{assets: 3}
	r, screen := poPickerAt(t, fake, 80)
	r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
	r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	r = poType(t, r, "Lathe")
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})
	if screen.assetsQuery != "Lathe" || len(screen.assets) == 0 {
		t.Fatalf("setup: query %q over %d row(s), want \"Lathe\" over some rows",
			screen.assetsQuery, len(screen.assets))
	}

	r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	for i := 0; i < len("Lathe"); i++ {
		next, _ := r.Update(tea.KeyMsg{Type: tea.KeyBackspace})
		r = next.(Root)
	}
	if screen.assetsSearch.Value() != "" {
		t.Fatalf("setup: the box still holds %q", screen.assetsSearch.Value())
	}
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEsc})

	poRejectPaneLine(t, screen, `"" was never run`)
	poWantPaneLine(t, screen, "emptied without running")
	poWantPaneLine(t, screen, `the rows still answer "Lathe"`)
	// The label row went with the box; it has to name the query the rows answer.
	poWantPaneLine(t, screen, `showing: "Lathe"`)
	poAssertFits(t, "emptied a committed asset search", screen)
	_ = r
}

// TestPOSupplierPicker_TabDoesNotCommit: tab used to commit the order's
// supplier while supplierPickBar named only j/k, enter and esc — an unnamed key
// taking the most consequential action on the frame. It is dropped rather than
// named: tab is "next field" in this same screen's line form, and an accidental
// tab silently choosing the supplier is exactly the class this change removes.
func TestPOSupplierPicker_TabDoesNotCommit(t *testing.T) {
	for _, h := range poPaneSizes {
		fake := &poPickFake{suppliers: 3, catalog: 2, pageSize: 5}
		r, screen := poPickerAtSize(t, fake, 80, h)
		// poPickerAt commits the first supplier on entry; go back to the picker.
		r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("b")})
		if screen.phase != poPhaseSupplier {
			t.Fatalf("80x%d: setup left phase %v, want the supplier picker", h, screen.phase)
		}
		r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
		want := screen.suppliers[screen.supplierCursor].ID
		if want == screen.supplierID {
			t.Fatalf("80x%d: setup did not move onto a DIFFERENT supplier", h)
		}

		before := strings.Join(poPaneLinesAt(t, screen, h), "\n")
		next, _ := r.Update(tea.KeyMsg{Type: tea.KeyTab})
		r = next.(Root)

		if screen.supplierID == want {
			t.Errorf("80x%d: tab committed supplier #%d, which no bar on the pane names", h, want)
		}
		if screen.phase != poPhaseSupplier {
			t.Errorf("80x%d: tab left the supplier picker (phase %v)", h, screen.phase)
		}
		if after := strings.Join(poPaneLinesAt(t, screen, h), "\n"); after != before {
			t.Errorf("80x%d: tab changed the pane:\n%s", h, after)
		}

		// enter, which the bar DOES name, still commits.
		r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})
		if screen.supplierID != want {
			t.Errorf("80x%d: enter did not commit supplier #%d (supplierID %d)", h, want, screen.supplierID)
		}
	}
}

// TestPOAssetPicker_TypingOverAnUnsearchedListLabelsWhatTheRowsAnswer: the
// label branch for an OPEN box read the live textinput, so the pane could read
// "search: hovercraft" over three rows that answer no query at all — the same
// mislabel the shut-box branches were fixed for, one state over. The note under
// it said "enter runs the search AGAIN" about a search that never ran.
//
// The box is opened while the unfiltered page-1 load is still out, which is
// what makes the reply land with assetsTyping true.
func TestPOAssetPicker_TypingOverAnUnsearchedListLabelsWhatTheRowsAnswer(t *testing.T) {
	fake := &poPickFake{assets: 3}
	r, screen := poPickerAt(t, fake, 80)

	next, load := r.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
	r = next.(Root)
	next, _ = r.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	r = next.(Root)
	r = poType(t, r, "hovercraft")
	r = pump(t, r, load, 0)

	if !screen.assetsTyping || screen.assetsQuery != "" || len(screen.assets) != 3 {
		t.Fatalf("setup: typing=%v query=%q rows=%d, want the box open over 3 unfiltered rows",
			screen.assetsTyping, screen.assetsQuery, len(screen.assets))
	}

	// The box may still be labelled `search:` — it IS the search box, and the
	// caret is in it — but it can no longer be the ONLY label: the line above
	// has to say what the rows on the pane actually answer.
	poWantPaneLine(t, screen, "showing: all of this supplier's assets")
	// …and the note must not claim a search has already run.
	poRejectPaneLine(t, screen, "runs the search again")
	poWantPaneLine(t, screen, "enter runs the search")
	poAssertFits(t, "typing over an unsearched asset list", screen)

	// Once a search HAS run, "again" is true and the label names it.
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})
	r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	if !screen.assetsTyping || screen.assetsQuery != "hovercraft" {
		t.Fatalf("setup: typing=%v query=%q, want the box reopened over a run search",
			screen.assetsTyping, screen.assetsQuery)
	}
	poWantPaneLine(t, screen, `showing: "hovercraft"`)
	poAssertFits(t, "typing over a searched asset list", screen)
	_ = r
}

// TestPOAssetPicker_EmptyingASearchThatFoundNothingKeepsTheNoMatch: the
// emptied-box arm worded its second line from assetsQuery alone, so on the
// zero-result path it asserted "the rows still answer X" on a frame with no
// rows — and because that note IS the body of the empty frame, it replaced the
// one line saying the search had found nothing.
func TestPOAssetPicker_EmptyingASearchThatFoundNothingKeepsTheNoMatch(t *testing.T) {
	fake := &poPickFake{assets: 3}
	r, screen := poPickerAt(t, fake, 80)
	r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
	r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	r = poType(t, r, "hovercraft")
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})
	if screen.assetsQuery != "hovercraft" || len(screen.assets) != 0 {
		t.Fatalf("setup: query %q over %d row(s), want a committed search that found nothing",
			screen.assetsQuery, len(screen.assets))
	}

	r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	for i := 0; i < len("hovercraft"); i++ {
		next, _ := r.Update(tea.KeyMsg{Type: tea.KeyBackspace})
		r = next.(Root)
	}
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEsc})

	poWantPaneLine(t, screen, "emptied without running")
	// The frame has no rows, so it must not claim any.
	poRejectPaneLine(t, screen, "the rows still answer")
	poWantPaneLine(t, screen, "no asset matches")
	poAssertFits(t, "emptied a search that found nothing", screen)
	_ = r
}

// TestPOCreate_EveryFixedHintOnTheseScreensSurvivesTheClip drives the frames
// whose fixed hints were written straight to the pane rather than through
// pickerHint — the association pickers' caveat, the source chooser's d row, and
// the three line-form hints, one of which names ctrl+t. Each was cut mid-
// sentence at 51 columns; the caveat lost the "does NOT" it exists to state.
func TestPOCreate_EveryFixedHintOnTheseScreensSurvivesTheClip(t *testing.T) {
	id := 7
	cases := []struct {
		name  string
		setup func(*PurchaseOrderCreateScreen)
		want  []string
	}{
		{"work-order picker caveat", func(s *PurchaseOrderCreateScreen) {
			s.phase = poPhaseWorkOrder
			s.assoc.workOrders = []omsapi.WorkOrder{{ID: 1001, Title: "Lathe teardown"}}
		}, []string{"does not change", "what a committee is billed"}},

		{"line form, catalog line", func(s *PurchaseOrderCreateScreen) {
			s.enterLinePhase(&id, nil, "Widget", 1, 0, 0, 0)
		}, []string{"Cost is optional", "supplier catalog"}},

		{"line form, case-packed cost basis", func(s *PurchaseOrderCreateScreen) {
			s.enterLinePhase(&id, nil, "Widget", 1, 0, 0, 12)
		}, []string{"ctrl+t"}},

		{"line form, freeform line has no date field", func(s *PurchaseOrderCreateScreen) {
			s.enterLinePhase(nil, nil, "Shop rags", 1, 0, 0, 0)
		}, []string{"send/receive"}},
	}

	for _, tc := range cases {
		for _, h := range poPaneSizes {
			t.Run(fmt.Sprintf("%s/80x%d", tc.name, h), func(t *testing.T) {
				s := poWidestSupplierScreen()
				s.terminalHeight = h
				tc.setup(s)

				poAssertFits(t, tc.name, s)
				for _, want := range tc.want {
					poWantPaneLine(t, s, want)
				}
			})
		}
	}
}

// TestPOPickers_JKOverAnEmptyListSaysWhy: the reported hang's exact shape, on
// the state the report is about.
//
// The !listOnScreen() gates answer j/k while a lookup is out or has failed, but
// a list that is DRAWN and empty — a search that matched nothing, a supplier
// with nothing flagged — fell through to the cursor arms, which compared
// against len-1 and returned nil. Nothing moved: no rows to scroll, no
// highlight to see stay put, and with the search box shut not even a caret.
//
// The note LINE is compared before and after, not the whole pane, because a
// blinking cursor satisfies a comparison of the pane — and the pane is checked
// too, clipped, at both supported heights.
func TestPOPickers_JKOverAnEmptyListSaysWhy(t *testing.T) {
	cases := []struct {
		name string
		fake *poPickFake
		// open drives the screen to a picker showing a drawn, empty list.
		open func(t *testing.T, r Root, screen *PurchaseOrderCreateScreen) Root
		note func(s *PurchaseOrderCreateScreen) *pickerNote
		rows func(s *PurchaseOrderCreateScreen) int
	}{
		{
			name: "items, a search that matched nothing",
			fake: &poPickFake{catalog: 12, pageSize: 5, assets: 1},
			open: func(t *testing.T, r Root, screen *PurchaseOrderCreateScreen) Root {
				r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("i")})
				r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
				r = poType(t, r, "flux capacitor")
				return key(t, r, tea.KeyMsg{Type: tea.KeyEsc})
			},
			note: func(s *PurchaseOrderCreateScreen) *pickerNote { return &s.itemSuppliersNote },
			rows: func(s *PurchaseOrderCreateScreen) int { return len(s.itemSuppliers) },
		},
		{
			name: "assets, a search that matched nothing",
			fake: &poPickFake{catalog: 2, assets: 3},
			open: func(t *testing.T, r Root, screen *PurchaseOrderCreateScreen) Root {
				r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
				r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
				r = poType(t, r, "hovercraft")
				return key(t, r, tea.KeyMsg{Type: tea.KeyEnter})
			},
			note: func(s *PurchaseOrderCreateScreen) *pickerNote { return &s.assetsNote },
			rows: func(s *PurchaseOrderCreateScreen) int { return len(s.assets) },
		},
		{
			name: "reorder, nothing flagged",
			fake: &poPickFake{catalog: 2, assets: 1, reorder: 0},
			open: func(t *testing.T, r Root, screen *PurchaseOrderCreateScreen) Root {
				return key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
			},
			note: func(s *PurchaseOrderCreateScreen) *pickerNote { return &s.reorderNote },
			rows: func(s *PurchaseOrderCreateScreen) int { return len(s.reorderItems) },
		},
	}

	for _, tc := range cases {
		for _, h := range poPaneSizes {
			r, screen := poPickerAtSize(t, tc.fake, 80, h)
			r = tc.open(t, r, screen)
			if got := tc.rows(screen); got != 0 {
				t.Fatalf("%s at 80x%d: setup left %d rows on screen", tc.name, h, got)
			}
			// In SEQUENCE, with no reset between them: these frames draw no
			// rows and no caret, so if j and k shared a lead the second press
			// would redraw a byte-for-byte identical pane and only the
			// four-second flash would move. Each names the key it answers.
			for _, k := range []string{"j", "k", "j"} {
				beforeNote := strings.Join(poNoteLines(t, *tc.note(screen)), "\n")
				beforePane := strings.Join(poPaneLinesAt(t, screen, h), "\n")
				r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)})
				if after := strings.Join(poNoteLines(t, *tc.note(screen)), "\n"); after == beforeNote {
					t.Errorf("%s at 80x%d: %q left the note line byte-for-byte identical:\n%s",
						tc.name, h, k, after)
				}
				if after := strings.Join(poPaneLinesAt(t, screen, h), "\n"); after == beforePane {
					t.Errorf("%s at 80x%d: %q redrew an identical pane:\n%s", tc.name, h, k, after)
				}
				poAssertFits(t, tc.name+" after "+k, screen)
			}
		}
	}
}

// TestPOAssetPicker_ASupplierRoundTripDoesNotLetAStaleLookupLand closes the
// last hole in "one asset lookup at a time".
//
// Every arm that fires one declines while assetsLoading is up, but
// resetSupplierScopedPickers clears that flag — reasonably, since a picker
// stranded on a "looking up…" frame for an answer nobody will use is its own
// defect — and the reply is identified by supplierID alone. So going A → B → A
// through the supplier picker made supplier A current again with A's search
// still in flight and every guard reset, and the search reply landing last
// replaced the unfiltered list with its own matches: a FILTERED SUBSET drawn as
// the supplier's whole asset list, at StatusOK with a green tick, an empty
// search box and no label to contradict it.
//
// The in-flight search is held here rather than simulated: r.Update hands back
// the command the keystroke produced, and not running it is exactly what a slow
// request looks like from the screen's side.
func TestPOAssetPicker_ASupplierRoundTripDoesNotLetAStaleLookupLand(t *testing.T) {
	fake := &poPickFake{assets: 3, catalog: 2, suppliers: 2}
	r, screen := poPickerAt(t, fake, 80)

	r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
	if len(screen.assets) != 3 {
		t.Fatalf("setup: supplier 1's assets did not load (%d rows)", len(screen.assets))
	}

	// Run a search and leave it in flight.
	r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	r = poType(t, r, "Lathe 1")
	next, inFlight := r.Update(tea.KeyMsg{Type: tea.KeyEnter})
	r = next.(Root)
	if !screen.assetsLoading {
		t.Fatalf("setup: enter in the search box did not start a lookup")
	}

	// A → B → A, which resets the picker twice and clears the in-flight flag.
	r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("b")}) // → source
	r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("b")}) // → supplier picker
	r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})
	r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("b")})
	r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("k")})
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})
	if screen.supplierID != 1 {
		t.Fatalf("setup: the order is on supplier %d, want 1 again", screen.supplierID)
	}

	// The picker is opened again and this time the lookup lands.
	r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
	if len(screen.assets) != 3 {
		t.Fatalf("setup: re-entering the picker did not load all 3 assets (%d rows)", len(screen.assets))
	}
	if strings.TrimSpace(screen.assetsQuery) != "" {
		t.Fatalf("setup: the fresh lookup carried %q", screen.assetsQuery)
	}

	// Now the abandoned search finally answers.
	r = pump(t, r, inFlight, 0)

	if len(screen.assets) != 3 {
		t.Errorf("the stale search replaced the supplier's asset list: %d rows, want 3", len(screen.assets))
	}
	unfiltered := false
	for _, a := range screen.assets {
		if a.Name == "Lathe 2" {
			unfiltered = true
		}
	}
	if !unfiltered {
		t.Errorf("the rows on screen are the stale search's matches, not this supplier's list: %+v", screen.assets)
	}
	if screen.assetsLoading {
		t.Error("the stale reply cleared the in-flight flag of a request it does not answer")
	}
	poWantPaneLine(t, screen, "Lathe 2")
	poRejectPaneLine(t, screen, "search: ")
	poAssertFits(t, "asset picker after a stale reply", screen)
}

// poStageCostlessLine picks the highlighted catalog row and CLEARS the cost
// field the picker prefilled, which is the only route to a staged line with no
// unit cost: the backend prices those from the item-supplier at save time, so
// the cart's total becomes a floor and renderCart draws the "priced from the
// supplier catalog" caveat under it.
//
// Every fixture used to stage lines that all carried a cost, so that caveat —
// 63 cells, and two rows once it goes through the folder — never rendered in
// any row-budget test.
func poStageCostlessLine(t *testing.T, r Root, screen *PurchaseOrderCreateScreen) Root {
	t.Helper()
	r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("i")})
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter}) // pick the highlighted row
	for screen.lineFocused != poLineFieldCost {
		r = key(t, r, tea.KeyMsg{Type: tea.KeyTab})
	}
	for i := 0; i < 12; i++ {
		r = key(t, r, tea.KeyMsg{Type: tea.KeyBackspace})
	}
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})
	if _, noCost := poCartTotal(screen.lines); noCost == 0 {
		t.Fatalf("setup: no staged line is priced from the catalog (%d lines)", len(screen.lines))
	}
	return r
}

// TestPOSourceChooser_OneOptionalRowIsNeverDroppedForNothing: the source
// chooser sacrifices the optional g / w / c rows when the pane is too short,
// and the first version of that decision asked only whether the layout WITH
// them fits — never whether dropping them helps.
//
// With exactly ONE of them offered it does not help: the substitute notice is
// one rendered row in the same blank-plus-row slot the row occupied, and the
// bar folds to the same height with or without that one clause. So at 80x24 a
// supplier carrying only a committee had its committee row replaced by
// "optional rows need more height" — false, the height was sufficient — while
// the bar stopped naming `c` and the `c` arm declined. A false sentence plus a
// disabled working key is the bar-honesty rule inside out.
//
// Every fixture that turned the optional lookups on turned on all THREE, where
// hiding genuinely saves rows, which is why nothing could see it.
func TestPOSourceChooser_OneOptionalRowIsNeverDroppedForNothing(t *testing.T) {
	cases := []struct {
		name  string
		fake  func() *poPickFake
		row   string
		key   string
		named string
		phase poPhase
	}{
		{"agreement only", func() *poPickFake {
			return &poPickFake{reorder: 15, catalog: 2, assets: 1, agreements: 1}
		}, "Agreement (optional)", "g", "g agreement", poPhaseAgreement},
		{"work orders only", func() *poPickFake {
			return &poPickFake{reorder: 15, catalog: 2, assets: 1, workOrders: 2}
		}, "Work order (optional)", "w", "w work order", poPhaseWorkOrder},
		{"committees only", func() *poPickFake {
			return &poPickFake{reorder: 15, catalog: 2, assets: 1, committees: 1}
		}, "Committee (optional)", "c", "c committee", poPhaseCommittee},
	}

	for _, tc := range cases {
		for _, h := range poPaneSizes {
			t.Run(fmt.Sprintf("%s at 80x%d", tc.name, h), func(t *testing.T) {
				r, screen := poPickerAtSize(t, tc.fake(), 80, h)
				r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
				r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")}) // add all 15
				r = key(t, r, tea.KeyMsg{Type: tea.KeyEsc})                       // review → source
				if screen.phase != poPhaseSource || len(screen.lines) != 15 {
					t.Fatalf("setup: phase %v with %d line(s)", screen.phase, len(screen.lines))
				}

				what := fmt.Sprintf("source chooser, %s, 15-line cart at 80x%d", tc.name, h)
				poAssertFits(t, what, screen)

				// The row is drawn, the frame does not claim otherwise, and the
				// bar still names the key.
				poWantPaneLine(t, screen, tc.row)
				poRejectPaneLine(t, screen, "optional rows need more height")
				if !strings.Contains(screen.helpText(), tc.named) {
					t.Errorf("the bar stopped naming %q while the row is on the pane: %q",
						tc.named, screen.helpText())
				}

				// And the key still WORKS — a named key must act.
				r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(tc.key)})
				if screen.phase != tc.phase {
					t.Fatalf("%q did not open its picker (phase %v, want %v)", tc.key, screen.phase, tc.phase)
				}
				poAssertFits(t, what+" after "+tc.key, screen)
			})
		}
	}
}

// TestPOSourceChooser_ACartItCannotListSaysSoAndItsKeysDecline is the source
// chooser's half of "nothing may act on a row the operator cannot see".
//
// At 80x24 the chooser's fixed chrome — the folded bar, the supplier header,
// the four source rows and the d row — leaves the cart fewer rows than its own
// header and total need. bodyRowBudget floors at three rows it does not have,
// so the cart was drawn past the bottom of the pane and clampToBox took the
// HIGHLIGHTED line, the "N more below" marker that would have said rows were
// hidden, and the total — while x still removed and ctrl+e still edited the
// line nobody could see.
//
// So: when the rows fit, they are listed with their highlight and the keys act.
// When they do not, one sentence says how many lines there are, what they come
// to, that they are not listed and which key opens them; the bar stops naming
// the four keys; and each of those keys declines without touching the cart
// while still moving the body.
//
// The second fixture is the one every earlier version of this test could not
// build. A supplier carrying an agreement, work orders AND committees draws
// three more header rows and lengthens the bar that measures against them, and
// a line priced from the catalog folds the cart's caveat onto two rows: those
// four rows put the whole collapsed sentence off the pane, so the frame said
// nothing about the cart at all while j/k/x/ctrl+e answered into a four-second
// flash. The optional rows are what yield now, and they say they have.
func TestPOSourceChooser_ACartItCannotListSaysSoAndItsKeysDecline(t *testing.T) {
	cases := []struct {
		name     string
		fake     func() *poPickFake
		costless bool
	}{
		{"plain supplier", func() *poPickFake {
			return &poPickFake{reorder: 15, catalog: 2, assets: 1}
		}, false},
		{"supplier with agreement, work orders and committees", func() *poPickFake {
			return &poPickFake{reorder: 15, catalog: 2, assets: 1,
				agreements: 1, workOrders: 2, committees: 1}
		}, true},
	}

	for _, tc := range cases {
		for _, h := range poPaneSizes {
			t.Run(fmt.Sprintf("%s at 80x%d", tc.name, h), func(t *testing.T) {
				r, screen := poPickerAtSize(t, tc.fake(), 80, h)
				r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
				r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")}) // add all 15
				r = key(t, r, tea.KeyMsg{Type: tea.KeyEsc})                       // review → source chooser
				if tc.costless {
					// addLine lands back on the source chooser, ready for the
					// next line, so no esc is needed here.
					r = poStageCostlessLine(t, r, screen)
				}
				want := 15
				if tc.costless {
					want = 16
				}
				if len(screen.lines) != want {
					t.Fatalf("setup staged %d line(s), want %d", len(screen.lines), want)
				}
				if screen.phase != poPhaseSource {
					t.Fatalf("setup left the screen on phase %v", screen.phase)
				}
				// A highlight in the MIDDLE of the cart, which is where the
				// reported failure lives: poCartWindow centres the window on it,
				// so at 80x24 the highlighted row and the "N more below" marker
				// under it were both past the bottom of the pane while x still
				// removed that very line.
				screen.reviewCursor = 7

				what := fmt.Sprintf("source chooser with a %d-line cart at 80x%d", want, h)
				poAssertFits(t, what, screen)

				if screen.cartListedOnScreen() {
					// The roomy pane lists them, so the highlight is on screen
					// and the keys that act on it are named and do act.
					poWantPaneLine(t, screen, fmt.Sprintf("▸ %d)", screen.reviewCursor+1))
					poWantPaneLine(t, screen, "x remove it")
					before := len(screen.lines)
					r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
					if len(screen.lines) != before-1 {
						t.Errorf("the cart is listed but x removed nothing (%d lines)", len(screen.lines))
					}
					return
				}

				// The count, that the lines are NOT listed, the key that lists
				// them and what they come to — all on the pane, whatever else
				// the frame had to give up to keep them there.
				poWantPaneLine(t, screen, fmt.Sprintf("d lists the %d line(s) · not listed here", want))
				total, noCost := poCartTotal(screen.lines)
				money := fmtMoney(total)
				if noCost > 0 {
					money = "at least " + money
				}
				poWantPaneLine(t, screen, money)
				// A key the bar names must act, so a bar that still named these
				// would be advertising the four keys the collapse made inert.
				for _, gone := range []string{"j/k highlight", "ctrl+e edit", "x remove"} {
					poRejectPaneLine(t, screen, gone)
				}
				// And whatever the frame dropped to make room says it is gone,
				// with the bar dropping the keys that named those rows.
				if !screen.sourceAttributionShown() {
					poWantPaneLine(t, screen, "optional rows need more height")
					for _, gone := range []string{"g agreement", "w work order", "c committee"} {
						if strings.Contains(screen.helpText(), gone) {
							t.Errorf("the bar still names %q with those rows off the pane: %q",
								gone, screen.helpText())
						}
					}
				}

				// Pressed IN SEQUENCE, with no state reset between them. The
				// previous version cleared the lead before every key, so every
				// press was measured from the un-led summary and no two presses
				// were ever compared against each other — which is why j and k
				// could share one lead and redraw a byte-for-byte identical
				// pane, on a frame with no cursor, no highlighted row and no
				// focused textinput for anything else to move.
				before := strings.Join(poPaneLinesAt(t, screen, h), "\n")
				press := func(name string, k tea.KeyMsg) {
					t.Helper()
					lines, cur, phase := len(screen.lines), screen.reviewCursor, screen.phase
					r = key(t, r, k)
					if len(screen.lines) != lines {
						t.Errorf("%q changed the cart (%d lines, was %d) with no line on the pane",
							name, len(screen.lines), lines)
					}
					if screen.reviewCursor != cur {
						t.Errorf("%q moved a highlight that is not on the pane", name)
					}
					if screen.phase != phase {
						t.Errorf("%q left the source chooser (phase %v)", name, screen.phase)
					}
					after := strings.Join(poPaneLinesAt(t, screen, h), "\n")
					if after == before {
						t.Errorf("%q redrew a byte-for-byte identical pane:\n%s", name, after)
					}
					before = after
					poAssertFits(t, what+" after "+name, screen)
				}
				press("j", tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
				press("k", tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("k")})
				press("x", tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
				press("ctrl+e", tea.KeyMsg{Type: tea.KeyCtrlE})
				// Back round the loop: k after ctrl+e, then j after k, so the
				// pair that shared a lead is compared in both orders.
				press("k", tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("k")})
				press("j", tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})

				// The keys whose rows were dropped decline the same way, and
				// say so in the body rather than only in the flash — in
				// sequence too, for the same reason.
				if !screen.sourceAttributionShown() {
					for _, k := range []string{"g", "w", "c"} {
						phase := screen.phase
						r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)})
						if screen.phase != phase {
							t.Fatalf("%q opened a picker whose row the frame is not drawing (phase %v)",
								k, screen.phase)
						}
						after := strings.Join(poPaneLinesAt(t, screen, h), "\n")
						if after == before {
							t.Errorf("%q redrew a byte-for-byte identical pane:\n%s", k, after)
						}
						before = after
						poAssertFits(t, what+" after "+k, screen)
					}
				}

				// d is the way out the summary names, and it has to be a real
				// one: the review phase must list the lines and show the
				// highlight at this very pane height, or the escape the
				// operator is told to take is a worse dead end than no escape.
				r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")})
				if screen.phase != poPhaseReview {
					t.Fatalf("d did not open the full cart (phase %v)", screen.phase)
				}
				poWantPaneLine(t, screen, fmt.Sprintf("▸ %d)", screen.reviewCursor+1))
				poWantPaneLine(t, screen, "more below")
				poAssertFits(t, fmt.Sprintf("review with a %d-line cart at 80x%d", want, h), screen)
			})
		}
	}
}

// TestPOCreate_TheSupplierHeaderKeepsBothNamesOnThePane pins the one row that
// is drawn on EVERY phase of this screen — including review, where it is the
// last thing seen before submit.
//
// Both values on it are OMS-supplied and were unbounded, so at 51 columns
// "Supplier: <name> (#1)  · agreement: <name>" was cut mid-agreement, and a
// longer supplier name took the agreement and the `(#id)` off the row
// altogether: the confirm-before-submit surface silently losing which pricing
// agreement the order is placed under. Every other fixture on this screen names
// the supplier "Acme Supply" and the agreement "Annual 1", which is exactly why
// nothing saw it.
func TestPOCreate_TheSupplierHeaderKeepsBothNamesOnThePane(t *testing.T) {
	for _, h := range poPaneSizes {
		t.Run(fmt.Sprintf("80x%d", h), func(t *testing.T) {
			fake := &poPickFake{
				catalog:       2,
				agreements:    1,
				supplierName:  "Northern Tool & Die Supply Company of Wisconsin",
				agreementName: "Annual 2026 Structural Steel Contract",
			}
			r, screen := poPickerAtSize(t, fake, 80, h)
			// g opens the agreement picker; row 1 is the first real agreement.
			r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("g")})
			if screen.phase != poPhaseAgreement {
				t.Fatalf("g left the screen on phase %v, want the agreement picker", screen.phase)
			}
			r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
			r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})
			_ = r
			if screen.agreementID == nil {
				t.Fatalf("the agreement was not committed")
			}

			what := fmt.Sprintf("supplier header at 80x%d", h)
			poAssertFits(t, what, screen)

			// The header is ONE row and it carries both facts: which supplier,
			// and that a pricing agreement is attached. Neither may be the
			// thing the pane drops.
			header := ""
			for _, line := range poPaneLinesAt(t, screen, h) {
				if strings.Contains(line, "Supplier:") {
					header = line
					break
				}
			}
			if header == "" {
				t.Fatalf("no supplier header on the pane:\n%s",
					strings.Join(poPaneLinesAt(t, screen, h), "\n"))
			}
			// Both values are clipped at this width — 51 columns cannot hold
			// two long OMS names — so what is pinned is that BOTH are still
			// identifiable, the id among them, rather than one silently gone.
			for _, want := range []string{"Northern", "(#1)", "agreement:", "Annu"} {
				if !strings.Contains(header, want) {
					t.Errorf("the header lost %q: %q", want, header)
				}
			}
			// A clipped value says it was clipped, the way renderAssocValue's
			// does — otherwise the operator reads a truncated agreement name as
			// the whole of it.
			if !strings.Contains(header, "…") {
				t.Errorf("both names were shortened but nothing on the row says so: %q", header)
			}
		})
	}
}

// TestPOCreate_AFailedSubmitSaysWhyWithoutTakingTheCartWithIt is requirement
// (4) of the report on the LAST step of the flow: when it fails the screen must
// say what went wrong and leave the operator somewhere they can act.
//
// The failure line was the one surface on this screen still written straight to
// the pane, and its content is an OMS response body — omsapi.parseError puts
// the ENTIRE raw payload in APIError.Message whenever the JSON envelope carries
// no code, so a gateway's HTML page arrives here whole. Unfolded it was cut at
// 51 columns to "✗ oms: http 502: <!DOCTYPE html><htm"; unbudgeted it was
// counted as ONE row by frameRowsWith while rendering as several, so every
// budget on the screen was computed against a wrong number and the line pushed
// itself off the bottom of the source chooser entirely.
//
// Driven at both supported heights and asserted through the real clipped pane,
// on the review phase where the failure happens AND on the source chooser the
// operator reaches with esc, where the frame is tightest.
func TestPOCreate_AFailedSubmitSaysWhyWithoutTakingTheCartWithIt(t *testing.T) {
	const headline = "submitting this purchase order failed"

	for _, h := range poPaneSizes {
		t.Run(fmt.Sprintf("80x%d", h), func(t *testing.T) {
			fake := &poPickFake{reorder: 15, catalog: 2, assets: 1,
				committees: 1, failCreate: true}
			r, screen := poPickerAtSize(t, fake, 80, h)
			r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
			r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")}) // add all 15
			r = key(t, r, tea.KeyMsg{Type: tea.KeyEsc})                       // review → source chooser
			r = poStageCostlessLine(t, r, screen)
			if len(screen.lines) != 16 {
				t.Fatalf("setup staged %d line(s), want 16", len(screen.lines))
			}

			r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")})
			if screen.phase != poPhaseReview {
				t.Fatalf("d left the screen on phase %v, want review", screen.phase)
			}
			r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter}) // submit → 502
			if screen.errMsg == "" {
				t.Fatalf("the submit failed and the screen recorded nothing")
			}

			what := fmt.Sprintf("review after a failed submit at 80x%d", h)
			poAssertFits(t, what, screen)
			poWantPaneLine(t, screen, headline)
			// The operator is still in the field they were typing into: the
			// failure line's rows are reserved, so the cart gives them up.
			poWantPaneLine(t, screen, "PO notes:")
			// The headline must not read as the whole story: either OMS's own
			// words reach the pane folded under it, or the block says how many
			// rows of them it hid. An 18-row pane under a 16-line cart has room
			// for the second and not the first, which is the sacrifice order
			// working rather than a silence.
			if !poPaneHasLine(t, screen, "502") &&
				!poPaneHasLine(t, screen, "more line(s) of the error") {
				t.Errorf("the failure names neither a detail nor what it hid at 80x%d:\n%s",
					h, strings.Join(poPaneLinesAt(t, screen, h), "\n"))
			}

			// esc carries the failure back to the source chooser, which is the
			// tightest frame on this screen: four line sources, an optional
			// committee row, the d row and a cart too long to list.
			r = key(t, r, tea.KeyMsg{Type: tea.KeyEsc})
			_ = r
			if screen.phase != poPhaseSource {
				t.Fatalf("esc left the screen on phase %v, want the source chooser", screen.phase)
			}

			what = fmt.Sprintf("source chooser carrying a failed submit at 80x%d", h)
			poAssertFits(t, what, screen)
			poWantPaneLine(t, screen, headline)
			poWantPaneLine(t, screen, "d  Done")

			// The cart's VALUE is on the pane either way: listed with a total
			// row when the rows fit, or carried by the collapsed sentence when
			// they do not. It is the last thing this screen gives up, and the
			// title is what goes instead.
			if screen.cartListedOnScreen() {
				poWantPaneLine(t, screen, "Total:")
			} else {
				poWantPaneLine(t, screen, "d lists the 16 line(s)")
				poWantPaneLine(t, screen, "at least $")
			}
		})
	}
}

// TestPOSourceChooser_TheTitleGivesBeforeTheCartsTotal pins the step of the
// sacrifice order that makes "never the cart's total" true rather than lucky.
//
// cartHiddenSentence is ordered by what may be sacrificed — the key, the count
// and "not listed here" lead it, so the TOTAL is what a one-row overflow takes.
// With one optional row offered the chooser sits on an 18-row pane with nothing
// spare, so the frame gives up "Where should this line come from?" and its
// blank line: two rows naming no key, with the four r/i/a/f rows right under
// them still saying what the screen is.
func TestPOSourceChooser_TheTitleGivesBeforeTheCartsTotal(t *testing.T) {
	const title = "Where should this line come from?"

	for _, h := range poPaneSizes {
		t.Run(fmt.Sprintf("80x%d", h), func(t *testing.T) {
			fake := &poPickFake{reorder: 15, catalog: 2, assets: 1,
				committees: 1, failCreate: true}
			r, screen := poPickerAtSize(t, fake, 80, h)
			r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
			r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
			r = key(t, r, tea.KeyMsg{Type: tea.KeyEsc})
			r = poStageCostlessLine(t, r, screen)
			r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")})
			r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})
			r = key(t, r, tea.KeyMsg{Type: tea.KeyEsc})
			_ = r

			poAssertFits(t, fmt.Sprintf("source chooser at 80x%d", h), screen)
			if screen.sourceTitleShown() {
				// Room for it: then it is drawn, and dropping it would be the
				// "hiding a row that costs nothing" defect.
				poWantPaneLine(t, screen, title)
				return
			}
			poRejectPaneLine(t, screen, title)
			// What the title bought: the whole cart sentence, total included.
			poWantPaneLine(t, screen, "d lists the 16 line(s)")
			poWantPaneLine(t, screen, "at least $")
			// And the rows the title named are still there, so nothing the
			// operator can act on went with it.
			for _, row := range []string{"r  Reorder queue", "i  Inventory items",
				"a  Assets purchased", "f  Freeform line"} {
				poWantPaneLine(t, screen, row)
			}
		})
	}
}
