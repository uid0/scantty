package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
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

	// itemName overrides the catalog's generated name for the FIRST row. Every
	// cart fixture used the generated "Widget 1", eight cells, so no test ever
	// staged a line whose OMS-supplied name could push the quantity, the price
	// and the type badge off the 51-column pane — which is the row the operator
	// reads to confirm the order. Only the first row takes it, so a picker
	// fixture is a MIXED list the way a real catalog is, and the long row can
	// be told from its neighbours.
	itemName string

	// assetName / assetSerial / reorderName are the same knob for the other two
	// pickers' first row. Their generated names are "Lathe 1" and "Bolt 1",
	// seven cells, so every picker LIST shipped unbounded: the rows an operator
	// picks FROM were the last OMS-supplied values on these screens that no
	// fixture ever made long enough to reach the cut.
	assetName   string
	assetSerial string
	reorderName string

	// itemSKU / assetTag are the row's other OMS-supplied IDENTIFIER. Every
	// fixture used "SKU-001" and "TAG-001", seven cells, so no test ever put a
	// real manufacturer part number in the column poFitRow treats as facts —
	// and an unbounded value sitting in the part that never gives is what
	// pushed the unit price off the pane and drew "@ 3.".
	itemSKU  string
	assetTag string

	// assetSearchServerSide makes the asset lookup answer a query with every
	// asset rather than filtering on the generated NAME. OMS's asset search
	// covers the tag, the serial and the description as well, so a query that
	// matches nothing a fixture row draws can still come back with a full page
	// — which is the only way to reach a committed query AND a next page at
	// once, and that pair is where the `Showing` row's two bounds meet.
	assetSearchServerSide bool

	// The three OPTIONAL header lookups. Every source-chooser test ran with
	// these at zero — the fake fell through to an empty envelope — so the g / w
	// / c rows were never on the frame, and the four rows they cost were what
	// pushed the collapsed cart clean off an 18-row pane with nothing on it
	// saying a cart existed.
	agreements int
	workOrders int
	committees int

	failItems bool
	// itemsErrBody replaces the catalog failure's JSON envelope. omsapi puts a
	// body with no error code into APIError.Message WHOLE, so this is how a
	// gateway page — or one in a language whose runes are two cells wide —
	// reaches the failure frame's folder.
	itemsErrBody string
	failAssets   bool
	failReorder  bool

	// workOrdersErrBody makes the OPTIONAL work-order lookup answer with a raw
	// gateway body rather than JSON, which is how an unbounded string reaches
	// the source chooser's attribution row — a row redrawn on every keystroke.
	workOrdersErrBody string

	// createErrBody replaces the gateway page failCreate answers with. The
	// failure block's two cuts drop content for different reasons and mark it
	// with different wordings, so a fixture has to be able to land on either
	// side of the cellPrefix bound rather than only on the far side of it,
	// which is where poGatewayHTML falls.
	createErrBody string

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

// catalogItemName is the name the catalog reports for row n: the generated one
// unless a test wants a realistic MRO name, which is long enough to matter.
func (f *poPickFake) catalogItemName(n int) string {
	if f.itemName != "" && n == 1 {
		return f.itemName
	}
	return fmt.Sprintf("Widget %d", n)
}

// catalogSKU is the part number row n reports, long-form on the first row only
// so a picker fixture is the MIXED list a real catalog is.
func (f *poPickFake) catalogSKU(n int) string {
	if f.itemSKU != "" && n == 1 {
		return f.itemSKU
	}
	return fmt.Sprintf("SKU-%03d", n)
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
				name := fmt.Sprintf("Bolt %d", i+1)
				if f.reorderName != "" && i == 0 {
					name = f.reorderName
				}
				items = append(items, map[string]any{
					"item_supplier_id":   i + 1,
					"item_name":          name,
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
				body := `{"detail":"item-suppliers exploded"}`
				if f.itemsErrBody != "" {
					body = f.itemsErrBody
				}
				_, _ = w.Write([]byte(body))
				return
			}
			rows := []map[string]any{}
			for i := (page - 1) * size; i < page*size && i < f.catalog; i++ {
				rows = append(rows, map[string]any{
					"id": i + 1, "item": fmt.Sprintf("it-%d", i+1),
					"item_name":    f.catalogItemName(i + 1),
					"supplier":     1,
					"supplier_sku": f.catalogSKU(i + 1),
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
			if f.assetSearchServerSide {
				search = ""
			}
			rows := []map[string]any{}
			for i := 0; i < f.assets; i++ {
				name := fmt.Sprintf("Lathe %d", i+1)
				if f.assetName != "" && i == 0 {
					name = f.assetName
				}
				if search != "" && !strings.Contains(strings.ToLower(name), search) {
					continue
				}
				tag := fmt.Sprintf("TAG-%03d", i+1)
				if f.assetTag != "" && i == 0 {
					tag = f.assetTag
				}
				row := map[string]any{
					"id": fmt.Sprintf("as-%d", i+1), "name": name,
					"asset_tag": tag,
				}
				if f.assetSerial != "" {
					row["serial_number"] = f.assetSerial
				}
				rows = append(rows, row)
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
			if f.workOrdersErrBody != "" {
				w.WriteHeader(http.StatusBadGateway)
				_, _ = w.Write([]byte(f.workOrdersErrBody))
				return
			}
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
				body := poGatewayHTML
				if f.createErrBody != "" {
					body = f.createErrBody
				}
				w.WriteHeader(http.StatusBadGateway)
				_, _ = w.Write([]byte(body))
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
	if !strings.Contains(out, "Enter=Pick item") {
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
		`no match for "flux capacitor"`,       // what was searched for
		"12 in catalog",                       // and against what
		"Esc=Close search",                    // and the way out, on the bar
		"No catalog item matches the filter.", // and what the LIST is
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
	// The way out is on the BAR, which the layer draws on every frame at the
	// bottom of the pane and never trims. It used to be a way-out line printed
	// in the body AND repeated as the frame's own bar, two statements of one
	// claim on one pane.
	if !strings.Contains(out, "b=Line sources") {
		t.Errorf("a failed lookup leaves the operator nowhere to act:\n%s", out)
	}

	// And the operator can actually act: b is named, so b must work.
	r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("b")})
	if screen.phase != poPhaseSource {
		t.Fatalf("the key the error frame names did not work (phase %v)", screen.phase)
	}
}

// TestPOItemPicker_RetryAfterAFailureShowsTheList: itemBody draws its "the
// lookup failed" line INSTEAD of the list, so a stale error string left behind by a fixed
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
	// The WORK and the SUBJECT, on the layer's status row: which request is
	// out, against whom, and — because this search really goes off the terminal
	// — what it is searching for.
	if out := r.View(); !strings.Contains(out, `Searching Acme Supply's assets for "Lathe 2"`) {
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
		// BOTH dimensions: the columnar frame pins its action bar to the pane
		// and draws the rule at the pane's width, so a screen given a height
		// and no width draws a bar sized for the layer's unsized fallback.
		s.Update(tea.WindowSizeMsg{Width: 80, Height: h})

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
		}, tea.KeyMsg{Type: tea.KeyDown}},
		{"item picker, move over a list that matched nothing", func(s *PurchaseOrderCreateScreen) {
			s.phase = poPhaseItemPick
			s.itemSuppliersAll = []omsapi.ItemSupplier{{ID: 1, ItemName: "Widget"}}
			s.itemSuppliersFor = s.supplierID
			s.itemSuppliersSearch.SetValue("nope")
			s.applyItemSupplierFilter()
		}, tea.KeyMsg{Type: tea.KeyUp}},
		{"asset picker, move over a search that matched nothing", func(s *PurchaseOrderCreateScreen) {
			s.phase = poPhaseAssetPick
			s.assetsQuery = "hovercraft"
		}, tea.KeyMsg{Type: tea.KeyDown}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, h := range poPaneSizes {
				s := NewPurchaseOrderCreateScreen(Deps{})
				s.supplierID = 1
				s.Update(tea.WindowSizeMsg{Width: 80, Height: h})
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
	if poBarNamedKeys(t, s.bar())["g"] {
		t.Errorf("the bar names g while the agreement list is still loading: %s", poBarText(s.bar()))
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

// poPaneWidths is every terminal width these frames are driven at, and it is
// three rather than one because the two failures are opposite: at 80 a value
// may not OVERFLOW, and at 100 or 120 a value may not be DISCARDED for a pane
// the terminal never had. A helper that hard-codes 80 cannot see the second.
var poPaneWidths = []int{80, 100, 120}

// poFrameWidth is the terminal WIDTH the frame under test was sized to, so an
// assertion clips it exactly as Root would. Unsized frames (a pre-rendered
// poFrameScreen) get 80, the narrowest supported.
func poFrameWidth(s Screen) int {
	if v, ok := s.(*PurchaseOrderCreateScreen); ok && v.terminalWidth > 0 {
		return v.terminalWidth
	}
	return poPaneWidths[0]
}

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
		clampToBox(s.View(), screenBodyWidth(poFrameWidth(s)), screenBodyHeight(termHeight)), "\n")
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
	budget := screenBodyWidth(poFrameWidth(s))
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
	s.Update(tea.WindowSizeMsg{Width: 80, Height: poPaneSizes[0]})
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
			[]string{"reading the reorder queue failed", "b=Line sources", "Esc=Cancel order"}},

		{"reorder, empty", func(s *PurchaseOrderCreateScreen) {},
			poPhaseReorderPick,
			[]string{"b=Line sources", "Esc=Cancel order"}},

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
			[]string{"looking up this supplier's items failed", "r=Retry",
				"b=Line sources", "Esc=Cancel order"}},

		{"items, failed while the search box is open", func(s *PurchaseOrderCreateScreen) {
			s.itemSuppliersErr = long
			s.itemSuppliersTyping = true
		}, poPhaseItemPick,
			[]string{"looking up this supplier's items failed", "Esc=Close search"}},

		{"items, empty catalog", func(s *PurchaseOrderCreateScreen) {
			s.itemSuppliersNote = pickerNote{s.noCatalogSentence(), StatusWarn}
		}, poPhaseItemPick,
			[]string{"has no active catalog items", "b=Line sources", "Esc=Cancel order"}},

		{"items, no note at all", func(s *PurchaseOrderCreateScreen) {},
			poPhaseItemPick,
			[]string{"has no active catalog items", "b=Line sources"}},

		{"items, search matched nothing", func(s *PurchaseOrderCreateScreen) {
			s.itemSuppliersAll = poCatalog(400)
			s.itemSuppliersTyping = true
			s.itemSuppliersSearch.SetValue("a search nobody would type")
			s.applyItemSupplierFilter()
			s.itemSuppliersNote = itemFilterNote(s.itemSuppliersSearch.Value(), 0, 400, "", true)
		}, poPhaseItemPick,
			[]string{"no match for", "400 in", "Esc=Close search"}},

		{"items, several matched, after esc closed the box", func(s *PurchaseOrderCreateScreen) {
			s.itemSuppliersAll = poCatalog(400)
			s.itemSuppliersSearch.SetValue("Widget 1")
			s.applyItemSupplierFilter()
			s.itemSuppliersNote = itemFilterNote("Widget 1", len(s.itemSuppliers), 400, "search closed", false)
		}, poPhaseItemPick,
			[]string{"search closed", "match", "UP/DN=Move", "Enter=Pick item"}},

		{"items, exactly one matched, after esc closed the box", func(s *PurchaseOrderCreateScreen) {
			s.itemSuppliersAll = poCatalog(400)
			s.itemSuppliersSearch.SetValue("Widget 137")
			s.applyItemSupplierFilter()
			s.itemSuppliersNote = itemFilterNote("Widget 137", 1, 400, "search closed", false)
		}, poPhaseItemPick,
			[]string{"search closed", "Enter=Pick item"}},

		{"items, unfiltered count", func(s *PurchaseOrderCreateScreen) {
			s.itemSuppliersAll = poCatalog(400)
			s.applyItemSupplierFilter()
			s.itemSuppliersNote = itemFilterNote("", 400, 400, "search closed", false)
		}, poPhaseItemPick,
			[]string{"search closed", "Enter=Pick item"}},

		{"assets, working", func(s *PurchaseOrderCreateScreen) {
			s.assetsLoading = true
		}, poPhaseAssetPick, []string{"Looking up the assets"}},

		{"assets, failed", func(s *PurchaseOrderCreateScreen) {
			s.assetsErr = long
		}, poPhaseAssetPick,
			[]string{"looking up this supplier's assets failed", "/=Retry with a search",
				"b=Line sources", "Esc=Cancel order"}},

		{"assets, failed while the search box is open", func(s *PurchaseOrderCreateScreen) {
			s.assetsErr = long
			s.assetsTyping = true
		}, poPhaseAssetPick,
			[]string{"looking up this supplier's assets failed", "Esc=Close search"}},

		{"assets, empty", func(s *PurchaseOrderCreateScreen) {
			s.assetsNote = pickerNote{"this supplier has no assets on file", StatusWarn}
		}, poPhaseAssetPick,
			[]string{"no assets on file", "/=Search", "b=Line sources", "Esc=Cancel order"}},

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
			[]string{"more above", "Lathe 40", "]=Next page", "[=Prev page"}},

		{"supplier switch confirm", func(s *PurchaseOrderCreateScreen) {
			s.suppliers = append(s.suppliers, omsapi.Supplier{ID: 2, Name: "Southern Fastener Supply"})
			s.supplierCursor = 1
			id := 7
			s.lines = []poCartLine{
				{item: omsapi.PurchaseOrderCreateItem{ItemSupplierID: &id, Quantity: 1}, label: "Hex bolt"},
				{item: omsapi.PurchaseOrderCreateItem{Description: "Shop rags", Quantity: 2}, label: "Shop rags"},
			}
		}, poPhaseSupplierSwitch,
			[]string{"fill them", "Ctrl-X=Drop & switch", "Esc=Keep cart"}},

		{"assets, search matched nothing", func(s *PurchaseOrderCreateScreen) {
			s.assetsSearch.SetValue("hovercraft full of eels")
			s.assetsQuery = "hovercraft full of eels"
			s.assetsNote = pickerNote{
				"no asset matches " + strconv.Quote(pickerClip("hovercraft full of eels", 16)), StatusWarn}
		}, poPhaseAssetPick,
			[]string{"no asset matches", "/=Search", "b=Line sources"}},
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
				s.Update(tea.WindowSizeMsg{Width: 80, Height: h})
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
	poWantPaneLine(t, screen, "Enter=Pick item")
	poAssertFits(t, "ambiguous search", screen)

	// And the esc-closes-the-box variant, whose "search closed · " prefix is
	// what pushed the same note from 53 cells to 69.
	r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEsc})
	poWantPaneLine(t, screen, "search closed")
	poWantPaneLine(t, screen, "Enter=Pick item")
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

	poRejectPaneLine(t, screen, "b=Line sources")
	poRejectPaneLine(t, screen, "Esc=Cancel order")
	poWantPaneLine(t, screen, "Esc=Close search")

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

	poRejectPaneLine(t, screen, "b=Line sources")
	poRejectPaneLine(t, screen, "Esc=Cancel order")
	poRejectPaneLine(t, screen, "/=Search")
	poWantPaneLine(t, screen, "Esc=Close search")

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
	poWantPaneLine(t, screen, "Esc=Close search")
	poRejectPaneLine(t, screen, "/=Retry with a search")
	poRejectPaneLine(t, screen, "b=Line sources")
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
	poWantPaneLine(t, screen, "b=Line sources")
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
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEsc})                       // → supplier picker
	r = key(t, r, tea.KeyMsg{Type: tea.KeyDown})
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
	poWantPaneLine(t, screen, "r=Reload")
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
	poWantPaneLine(t, screen, "b=Line sources")
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
	poWantPaneLine(t, screen, "r=Retry")
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
	poRejectPaneLine(t, screen, "Enter=")
	poWantPaneLine(t, screen, "Esc=Close search")
	poRejectPaneLine(t, screen, "Esc=Cancel order")
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
	poWantPaneLine(t, screen, "/=Search")
	poWantPaneLine(t, screen, "Esc=Cancel order")
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
					r = key(t, r, tea.KeyMsg{Type: tea.KeyDown})
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
				poWantPaneLine(t, screen, "]=Next page")
				for i := 0; i < len(screen.assets)-1; i++ {
					r = key(t, r, tea.KeyMsg{Type: tea.KeyDown})
					poAssertFits(t, "asset picker, scrolling", screen)
					if want := screen.assets[screen.assetsCursor].Name; !poPaneHasLine(t, screen, want) {
						t.Fatalf("the highlighted asset %q is not on the 80x%d pane:\n%s",
							want, height, strings.Join(poPaneLines(t, screen), "\n"))
					}
				}
				poWantPaneLine(t, screen, "]=Next page")
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
	poWantPaneLine(t, screen, "UP/DN=Move")
	poAssertFits(t, "ambiguous enter", screen)
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
		r = key(t, r, tea.KeyMsg{Type: tea.KeyDown})
		r = key(t, r, tea.KeyMsg{Type: tea.KeyDown})
		wantCur := screen.itemSuppliersCur

		next, reload := r.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
		r = next.(Root)
		if !screen.itemSuppliersLoad || len(screen.itemSuppliers) == 0 {
			t.Fatalf("setup: want a reload over held rows (load=%v rows=%d)",
				screen.itemSuppliersLoad, len(screen.itemSuppliers))
		}
		poRejectPaneLine(t, screen, "Widget 1  ")

		for _, k := range []tea.KeyMsg{
			{Type: tea.KeyDown},
			{Type: tea.KeyUp},
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
		poWantPaneLine(t, screen, "r=Retry")
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
			{Type: tea.KeyDown},
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
			{Type: tea.KeyDown},
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
				r = key(t, r, tea.KeyMsg{Type: tea.KeyDown})
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

// poPickerVocabulary is every keystroke this sweep presses.
//
// It is the ONE curated roster left in this package, and what makes that safe
// is the phase sweep beside it (po_create_phase_sweep_test.go): that one
// presses the whole KEY SPACE — every printable ASCII rune plus the named
// specials — against every phase of the iota, so a key missing from this list
// is still pressed, in both directions, one file over. What this sweep earns
// its keep on is the other axis: it walks the picker STATES the phase sweep
// does not reach (empty, failed, mid-flight, mid-page), and pressing the whole
// space against fifteen of those at two pane heights is minutes of wall clock
// for coverage the phase sweep already has.
//
// A key ABSENT from this list is pressed in NEITHER direction, so it is
// untested rather than passing — which is exactly how two defects in this
// project's history survived. Adding one here is cheap; leaving one out is
// invisible. It covers every key any of these phases binds, not only the ones
// a bar happens to name.
//
// It was rewritten wholesale by the columnar conversion, and the diff IS the
// key-scheme change: j/k are gone (the arrows and the paging pair moved in),
// the source chooser's bare `x` became Ctrl-X, and PgUp/PgDn arrived with the
// layer's windowed body.
var poPickerVocabulary = []string{
	"down", "up", "pgdown", "pgup", "enter", "esc", "tab", "shift+tab",
	"/", "r", "b", "]", "[", " ",
	"a", "i", "f", "g", "w", "c", "d",
	"ctrl+e", "ctrl+x",
}

// poPickerState is everything a key can CHANGE. Deliberately excludes the note
// and the status flash: a key that declines and says why has not acted, and the
// project's rule is that such an arm must answer, not that the bar must promise
// it. Anything in here moving means the key did something.
//
// Which fields it reads is not a matter of taste any more:
// TestPOCreateScreen_EveryFieldIsClassified reflects over the struct and fails
// on a field that is in neither this fingerprint nor poStateDeclined. `pending`
// and `errMsg` were both missing from it, so no review-phase arm could be
// judged by it at all — the same silent-omission shape as poAllBarKeys and
// poPickerVocabulary, one layer down.
//
// Text inputs contribute their VALUE and never their View: a blinking caret
// moving is not a key acting, and a fingerprint a caret can move is a test any
// keypress passes.
func poPickerState(s *PurchaseOrderCreateScreen) string {
	var b strings.Builder
	fmt.Fprint(&b, s.phase, "|", s.pending, s.paneForState(),
		"|", s.itemSuppliersCur, s.itemSuppliersTyping, s.itemSuppliersLoad,
		s.itemSuppliersErr, s.itemSuppliersSearch.Value(),
		len(s.itemSuppliers), len(s.itemSuppliersAll), s.itemSuppliersFor,
		"|", s.assetsCursor, s.assetsTyping, s.assetsLoading, s.assetsErr,
		s.assetsSearch.Value(), s.assetsPage, s.assetsHasNext, s.assetsSeq,
		s.assetsQuery, len(s.assets),
		"|", s.reorderCursor, s.reorderLoading, s.reorderLoadErr,
		len(s.reorderSelected), len(s.reorderItems),
		"|", s.supplierCursor, s.supplierLoading, s.supplierLoadErr,
		len(s.suppliers), s.supplierID,
		"|", s.agreementCursor, s.agreementLoading, s.agreementLoadErr,
		len(s.agreements), poDerefInt(s.agreementID),
		"|", s.workOrderCursor, s.workOrderID, s.committeeCursor, s.committeeID,
		s.assoc.workOrdersOffered(), s.assoc.committeesOffered(),
		"|", s.lineFocused, s.pickedQPP, s.costBasisCase,
		poDerefInt(s.pickedItemSup), poDerefStr(s.pickedAssetID),
		"|", len(s.lines), s.reviewCursor, s.poNotes.Value(),
		s.editIndex, s.editReturn)
	// FOCUS, for every input on the screen. It is not a struct field of the
	// screen — it lives inside the textinput — so the reflect check over field
	// names cannot reach it, and a key that only moves a caret into a field was
	// therefore invisible to every fingerprint here. That is exactly what `d`
	// did while a submit was out: it re-focused the notes the submit had
	// blurred, and no sweep could see it.
	fmt.Fprint(&b, "|", s.poNotes.Focused(),
		s.itemSuppliersSearch.Focused(), s.assetsSearch.Focused())
	for _, in := range s.lineInputs {
		fmt.Fprint(&b, "|", in.Value(), in.Focused())
	}
	return b.String()
}

// poFocusFingerprinted is every input whose FOCUS poPickerState carries, keyed
// by the screen field that holds it. Derived against the struct below, so an
// input added tomorrow fails by name until somebody puts its caret in the
// fingerprint — the same guarantee poStateFingerprinted gives the fields.
var poFocusFingerprinted = map[string]bool{
	"poNotes":             true,
	"itemSuppliersSearch": true,
	"assetsSearch":        true,
	"lineInputs":          true,
}

// TestPOCreateScreen_EveryInputFocusIsFingerprinted walks the struct for
// textinputs rather than listing them: focus is state a key can move, and the
// field-name check one function up cannot see it because it is inside the
// value rather than beside it.
func TestPOCreateScreen_EveryInputFocusIsFingerprinted(t *testing.T) {
	typ := reflect.TypeOf(PurchaseOrderCreateScreen{})
	input := reflect.TypeOf(textinput.Model{})
	found := map[string]bool{}
	for i := 0; i < typ.NumField(); i++ {
		f := typ.Field(i)
		ft := f.Type
		for ft.Kind() == reflect.Slice || ft.Kind() == reflect.Array || ft.Kind() == reflect.Ptr {
			ft = ft.Elem()
		}
		if ft != input {
			continue
		}
		found[f.Name] = true
		if !poFocusFingerprinted[f.Name] {
			t.Errorf("field %q holds a textinput whose focus poPickerState does not carry — "+
				"a key that only moves the caret into it would be judged by nothing", f.Name)
		}
	}
	if len(found) == 0 {
		t.Fatal("the derivation found no textinput fields at all — it is broken, not the screen")
	}
	for name := range poFocusFingerprinted {
		if !found[name] {
			t.Errorf("poFocusFingerprinted names %q, which is no longer a textinput on the screen", name)
		}
	}
}

// poFocusedInput names the input this phase DRAWS if it is holding the caret,
// or "" when none is. While a submit is out that answer must be "": the payload
// has gone, so a caret on the pane is the screen inviting input it will refuse.
//
// Drawn is the qualifier that makes it an invariant rather than a tidiness
// check. A line-form input stays focused after the form is left — nothing
// blurs it and nothing draws it either, so no caret reaches the operator — and
// failing on that would be measuring bookkeeping instead of the pane.
func poFocusedInput(s *PurchaseOrderCreateScreen) string {
	switch s.phase {
	case poPhaseReview:
		if s.poNotes.Focused() {
			return "the PO-notes input"
		}
	case poPhaseLine:
		for i, in := range s.lineInputs {
			if in.Focused() {
				return fmt.Sprintf("line-form field %d", i)
			}
		}
	case poPhaseItemPick:
		if s.itemSuppliersSearch.Focused() {
			return "the item search box"
		}
	case poPhaseAssetPick:
		if s.assetsSearch.Focused() {
			return "the asset search box"
		}
	}
	return ""
}

func poDerefInt(p *int) string {
	if p == nil {
		return "-"
	}
	return fmt.Sprint(*p)
}

func poDerefStr(p *string) string {
	if p == nil {
		return "-"
	}
	return *p
}

// paneForState keeps the pane SIZE — both dimensions, because both change what
// is drawn — in the fingerprint without letting a resize masquerade as a
// keypress: no key changes either, so they can only differ between two presses
// if the harness itself moved.
func (s *PurchaseOrderCreateScreen) paneForState() string {
	return fmt.Sprintf("%dx%d/%d", s.terminalWidth, s.terminalHeight, s.switchScroll)
}

// poStateFingerprinted / poStateDeclined classify EVERY field of the screen.
// The struct is enumerated by reflection, so a field added tomorrow fails the
// check by name until somebody decides which half it belongs in — the omission
// cannot be made quietly, which is the whole point.
var poStateFingerprinted = map[string]bool{
	"phase": true, "pending": true,
	// The pane geometry is one embedded struct now (jdeScreen), so the two
	// dimensions are classified together — and paneForState still reads both,
	// because both change what is drawn.
	"jdeScreen": true, "switchScroll": true,
	"suppliers": true, "supplierLoading": true, "supplierLoadErr": true,
	"supplierCursor": true, "supplierID": true,
	"agreements": true, "agreementLoading": true, "agreementLoadErr": true,
	"agreementCursor": true, "agreementID": true,
	"assoc": true, "workOrderCursor": true, "workOrderID": true,
	"committeeCursor": true, "committeeID": true,
	"reorderItems": true, "reorderLoading": true, "reorderLoadErr": true,
	"reorderCursor": true, "reorderSelected": true,
	"itemSuppliers": true, "itemSuppliersAll": true, "itemSuppliersFor": true,
	"itemSuppliersLoad": true, "itemSuppliersErr": true, "itemSuppliersCur": true,
	"itemSuppliersSearch": true, "itemSuppliersTyping": true,
	"assets": true, "assetsLoading": true, "assetsSeq": true, "assetsErr": true,
	"assetsCursor": true, "assetsSearch": true, "assetsTyping": true,
	"assetsPage": true, "assetsHasNext": true, "assetsQuery": true,
	"lineInputs": true, "lineFocused": true, "pickedItemSup": true,
	"pickedAssetID": true, "pickedQPP": true, "costBasisCase": true,
	"lines": true, "reviewCursor": true, "poNotes": true,
	"editIndex": true, "editReturn": true,
}

// poStateDeclined is every field a key may move WITHOUT having acted, with the
// reason. All of them are the screen answering a decline: counting them would
// call "j moves nothing" an action and invert the rule the sweep enforces.
var poStateDeclined = map[string]string{
	"deps":              "injected dependencies; no keystroke reaches them",
	"errMsg":            "the failure line's headline — a validation decline writes here",
	"errDetail":         "the failure line's unbounded half, written with errMsg",
	"supplierNote":      "the supplier picker's decline note",
	"reorderNote":       "the reorder picker's decline note",
	"itemSuppliersNote": "the item picker's decline note",
	"assetsNote":        "the asset picker's decline note",
	"sourceNote":        "the source chooser's decline note",
	"switchLead":        "what a declined key did on the supplier-switch confirm",
	"pendingLead":       "what a key the submit made inert just did",
}

// TestPOCreateScreen_EveryFieldIsClassified is the completeness guarantee for
// the fingerprint above. `pending` and `errMsg` fell out of it unnoticed, and
// nothing failed — a review-phase arm could not be judged at all.
func TestPOCreateScreen_EveryFieldIsClassified(t *testing.T) {
	typ := reflect.TypeOf(PurchaseOrderCreateScreen{})
	seen := map[string]bool{}
	for i := 0; i < typ.NumField(); i++ {
		name := typ.Field(i).Name
		seen[name] = true
		_, in := poStateFingerprinted[name]
		_, out := poStateDeclined[name]
		switch {
		case in && out:
			t.Errorf("field %q is in BOTH poStateFingerprinted and poStateDeclined", name)
		case !in && !out:
			t.Errorf("field %q is in neither poStateFingerprinted nor poStateDeclined — "+
				"a key that moves it would be judged by nothing", name)
		}
	}
	for name := range poStateFingerprinted {
		if !seen[name] {
			t.Errorf("poStateFingerprinted names %q, which the screen no longer has", name)
		}
	}
	for name := range poStateDeclined {
		if !seen[name] {
			t.Errorf("poStateDeclined names %q, which the screen no longer has", name)
		}
	}
}

// poCmdActs reports whether a command does anything beyond posting a status.
// esc leaves the screen entirely and b changes phase, so a fingerprint alone
// would call the first of those dead.
func poCmdActs(cmd tea.Cmd) bool {
	if cmd == nil {
		return false
	}
	if poIsBlink(cmd()) {
		// A CARET blinking is not a key acting, and every key that reaches a
		// focused textinput comes back with one: bubbles falls through to
		// Cursor.Update for anything its own switch does not handle, and that
		// returns a blink tick unconditionally. Counting it made every
		// unhandled key inside a search box look like an act — the fingerprint
		// has excluded the caret since it was written, for exactly this
		// reason, and the command had to be excluded with it.
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
		bar   func(*PurchaseOrderCreateScreen) []actionBarItem
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
			press("i"), (*PurchaseOrderCreateScreen).bar},
		{"items, empty catalog", func() *poPickFake { return &poPickFake{catalog: 0} },
			press("i"), (*PurchaseOrderCreateScreen).bar},
		{"items, failed", func() *poPickFake { return &poPickFake{catalog: 8, failItems: true} },
			press("i"), (*PurchaseOrderCreateScreen).bar},
		{"items, first walk in flight", func() *poPickFake { return &poPickFake{catalog: 8, pageSize: 2} },
			midFlight(nil, "i"), (*PurchaseOrderCreateScreen).bar},
		{"items, reload in flight", func() *poPickFake { return &poPickFake{catalog: 8, pageSize: 2} },
			midFlight([]string{"i"}, "r"), (*PurchaseOrderCreateScreen).bar},
		{"assets, loaded with a pager", func() *poPickFake { return &poPickFake{assets: 12, pageSize: 5} },
			press("a"), (*PurchaseOrderCreateScreen).bar},
		{"assets, empty", func() *poPickFake { return &poPickFake{assets: 0} },
			press("a"), (*PurchaseOrderCreateScreen).bar},
		{"assets, failed", func() *poPickFake { return &poPickFake{assets: 3, failAssets: true} },
			press("a"), (*PurchaseOrderCreateScreen).bar},
		{"assets, load in flight", func() *poPickFake { return &poPickFake{assets: 12, pageSize: 5} },
			midFlight(nil, "a"), (*PurchaseOrderCreateScreen).bar},
		{"assets, page load in flight", func() *poPickFake { return &poPickFake{assets: 12, pageSize: 5} },
			midFlight([]string{"a"}, "]"), (*PurchaseOrderCreateScreen).bar},
		{"reorder, loaded", func() *poPickFake { return &poPickFake{reorder: 6} },
			press("r"), (*PurchaseOrderCreateScreen).bar},
		{"reorder, empty", func() *poPickFake { return &poPickFake{reorder: 0} },
			press("r"), (*PurchaseOrderCreateScreen).bar},
		{"reorder, failed", func() *poPickFake { return &poPickFake{failReorder: true} },
			press("r"), (*PurchaseOrderCreateScreen).bar},
		{"reorder, load in flight", func() *poPickFake { return &poPickFake{reorder: 6} },
			midFlight(nil, "r"), (*PurchaseOrderCreateScreen).bar},
		// The supplier list is the fourth picker frame on this screen. It is
		// reached by backing out of the source chooser, and its loaded/loading
		// states have the same two directions to check.
		{"suppliers, loaded", func() *poPickFake { return &poPickFake{suppliers: 4} },
			press("esc"), (*PurchaseOrderCreateScreen).bar},
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
				named := poBarNamedKeys(t, bar)

				// Every claim the bar makes has to be READABLE on the pane it
				// is drawn on, at this height, or it is not a claim. The layer
				// tightens the gutter and then WRAPS rather than dropping an
				// item, so this is a check on the pane's height as much as its
				// width — a bar folded onto a fourth line on a pane with three
				// to give is a key the operator cannot discover.
				for _, it := range bar {
					want := it.Key + "=" + it.Label
					if !poPaneHasLine(t, screen, want) {
						t.Errorf("the bar claims %q but the 80x%d pane does not carry it:\n%s",
							want, height, strings.Join(poPaneLines(t, screen), "\n"))
					}
				}
				poAssertFits(t, p.name, screen)
				// The one-surface rule on the NON-typing states. Both notes
				// that named keys lived out here — an empty source chooser and
				// a loaded catalog — while the only body scan in the package
				// ran over the TYPING states, which is a guard scoped to where
				// somebody expected the defect rather than to where it was.
				poAssertBodyNamesNoKey(t, p.name, screen, height)

				// Probed from more than one position, because j does nothing at
				// the bottom of a list and k nothing at the top: a key is dead
				// only if it does nothing from ANY of them. Same reasoning the
				// list sweep uses.
				for _, k := range poPickerVocabulary {
					acted := false
					for _, probe := range [][]string{nil, {"down"}} {
						pr, ps := fresh(probe)
						before := poPickerState(ps)
						_, cmd := pr.Update(poPickerKeyMsg(k))
						if poPickerState(ps) != before || poCmdActs(cmd) {
							acted = true
						}
					}
					switch {
					case named[k] && !acted:
						t.Errorf("%s names %q but pressing it changes nothing (bar: %s)", p.name, k, poBarText(bar))
					case !named[k] && acted:
						t.Errorf("%s does not name %q, but pressing it acts (bar: %s)", p.name, k, poBarText(bar))
					}
				}
			})
		}
	}
}

// poPickerKeyMsg turns one of poKeySpace's names into the message a terminal
// really sends.
//
// It is COMPLETE over that space, and the completeness is checked
// (TestPOCreate_EveryKeyNameTranslates) rather than trusted. It used to fall
// through to KeyRunes for anything it did not recognise, which is silent and
// wrong in the one direction that matters: "pgup" reached a focused search box
// as the four letters p-g-u-p, so the sweep pressing it saw the query change
// and reported the picker as acting on a key its bar does not name. A
// translator that spells an unknown name as text turns every new key name into
// a false positive, and the failure reads as a defect in the screen.
//
// The single-rune fallback stays, because that IS how a printable key arrives.
func poPickerKeyMsg(k string) tea.KeyMsg {
	if t, ok := poNamedKeyTypes[k]; ok {
		return tea.KeyMsg{Type: t}
	}
	if r := []rune(k); len(r) == 1 {
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: r}
	}
	// A NAME this switch does not know is resolved against bubbletea's own key
	// types rather than typed as text. The bare fallback below used to take
	// every unknown name, so adding "ctrl+k" to poKeySpace would have pressed
	// the six characters c-t-r-l-+-k into whatever box held the caret and the
	// sweep would have gone on passing while testing nothing whatever — the
	// silent degradation receiveNamedKeyMsg exists to stop, and the reason it
	// is asked here too rather than only in the receiving sweeps.
	if msg, ok := receiveNamedKeyMsg(k); ok {
		return msg
	}
	panic("no key type is named " + k + " — a sweep would press it as literal text")
}

// poNamedKeyTypes is every key poKeySpace names that is not a printable rune.
// One table, read by both translators, so the two sweeps cannot disagree about
// what a key name means.
var poNamedKeyTypes = map[string]tea.KeyType{
	"enter":     tea.KeyEnter,
	"esc":       tea.KeyEsc,
	"tab":       tea.KeyTab,
	"shift+tab": tea.KeyShiftTab,
	"up":        tea.KeyUp,
	"down":      tea.KeyDown,
	"left":      tea.KeyLeft,
	"right":     tea.KeyRight,
	"home":      tea.KeyHome,
	"end":       tea.KeyEnd,
	"pgup":      tea.KeyPgUp,
	"pgdown":    tea.KeyPgDown,
	"backspace": tea.KeyBackspace,
	"delete":    tea.KeyDelete,
	"ctrl+e":    tea.KeyCtrlE,
	"ctrl+x":    tea.KeyCtrlX,
	"ctrl+t":    tea.KeyCtrlT,
	"ctrl+p":    tea.KeyCtrlP,
	"ctrl+n":    tea.KeyCtrlN,
}

// TestPOCreate_EveryKeyNameTranslates walks poKeySpace and fails on any name
// the translator would spell out as text instead of sending as a key.
//
// DERIVED from the space rather than from the table, so a key added to
// poKeySpace tomorrow fails here until somebody teaches the translator about
// it — which is the direction the silent fallback made impossible to see.
func TestPOCreate_EveryKeyNameTranslates(t *testing.T) {
	for _, k := range poKeySpace() {
		msg := poPickerKeyMsg(k)
		if len([]rune(k)) == 1 {
			if msg.Type != tea.KeyRunes || string(msg.Runes) != k {
				t.Errorf("the printable key %q translates to %v, want the rune itself", k, msg.Type)
			}
			continue
		}
		if msg.Type == tea.KeyRunes {
			t.Errorf("the key name %q translates to the literal text %q — a focused text box "+
				"would receive it as typing, and a sweep pressing it would report the screen "+
				"as acting on a key nothing bound", k, string(msg.Runes))
		}
		if got := poPhaseKeyMsg(k); got.Type != msg.Type {
			t.Errorf("the two translators disagree about %q: %v and %v", k, msg.Type, got.Type)
		}
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
	poWantPaneLine(t, screen, "/=Retry with a search")
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
// supplierBody drew nothing at all — yet the bar promised j/k and enter
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
			// The claim is spelled as the bar spells it — "j/k move" stopped
			// being the wording when the bar started naming the arrows it
			// binds, and a reject assertion for a string the pane can no longer
			// print passes without looking at anything.
			poRejectPaneLine(t, screen, "UP/DN=Move")
			poRejectPaneLine(t, screen, "Enter=Commit supplier")
			poWantPaneLine(t, screen, "Esc=Cancel order")
			poAssertFits(t, "suppliers, load in flight", screen)

			for _, k := range []tea.KeyMsg{
				{Type: tea.KeyDown},
				{Type: tea.KeyUp},
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
			poWantPaneLine(t, screen, "UP/DN=Move")
			poWantPaneLine(t, screen, "Enter=Commit supplier")
			r = key(t, r, tea.KeyMsg{Type: tea.KeyDown})
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
// The second half is the decline itself. The item picker's loading branch
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
	poWantPaneLine(t, screen, "Esc=Close search")

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
	// With ONE row left the box's Enter says which of its two acts it is about
	// to do, so the claim is "Pick it" rather than the browse frame's
	// "Pick item" — a single label for every match count is what the prose bar
	// had, and it promised a pick over eleven matches and over none.
	poWantPaneLine(t, screen, "Enter=Pick it")
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
// bar's "/=Retry with a search" promised one frame earlier.
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
	poRejectPaneLine(t, screen, "Enter=Run search")
	poWantPaneLine(t, screen, "Esc=Close search")

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
	poWantPaneLine(t, screen, "still looking up the assets")
	poWantPaneLine(t, screen, "still looking up")
	poAssertFits(t, "enter over an in-flight asset lookup", screen)

	// The first reply lands, and the key comes back — named and working.
	r = pump(t, r, load, 0)
	if screen.assetsLoading {
		t.Fatal("the lookup never finished")
	}
	settled := fake.hits("/assets/")
	poWantPaneLine(t, screen, "Enter=Run search")
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
	poWantPaneLine(t, screen, "still looking up the assets")
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
	poWantPaneLine(t, screen, "still looking up the assets")

	// The one reply that IS out lands, and the rows match the box.
	r = pump(t, r, search, 0)
	if screen.assetsLoading {
		t.Fatal("the lookup never finished")
	}
	if len(screen.assets) != 1 {
		t.Errorf("the settled list holds %d row(s), want the 1 match for the query in the box", len(screen.assets))
	}
	poWantPaneLine(t, screen, `Showing ..... "Lathe 2"`)
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

	// The WORK is on the layer's status row now, naming the request and the
	// subject; the note is cleared by the arm that fires it, so the previous
	// lookup's count cannot stand over a fresh one.
	poWantPaneLine(t, screen, "Looking up the assets bought from")
	poRejectPaneLine(t, screen, "3 asset(s)")
	poRejectPaneLine(t, screen, "Enter=Pick asset")
	poAssertFits(t, "re-entered the asset picker", screen)

	r = pump(t, r, reload, 0)
	if screen.assetsLoading {
		t.Fatal("the lookup never finished")
	}
	poWantPaneLine(t, screen, "3 asset(s)")
}

// TestPOSupplierPicker_DecliningKeysMoveTheBody: the supplier body returned
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
		}, "loading the suppliers failed"},
		{"none configured", func(s *PurchaseOrderCreateScreen) {
			s.supplierLoading = false
			s.suppliers = nil
		}, "no suppliers are configured"},
	}
	keys := []tea.KeyMsg{
		{Type: tea.KeyDown},
		{Type: tea.KeyEnter},
	}

	for _, st := range states {
		for _, k := range keys {
			for _, h := range poPaneSizes {
				s := NewPurchaseOrderCreateScreen(Deps{})
				s.phase = poPhaseSupplier
				s.Update(tea.WindowSizeMsg{Width: 80, Height: h})
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
		{Type: tea.KeyDown},
		{Type: tea.KeyRunes, Runes: []rune(" ")},
		{Type: tea.KeyEnter},
	}

	for _, st := range states {
		for _, k := range keys {
			for _, h := range poPaneSizes {
				s := NewPurchaseOrderCreateScreen(Deps{})
				s.phase = poPhaseReorderPick
				s.supplierID = 1
				s.Update(tea.WindowSizeMsg{Width: 80, Height: h})
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
			{Type: tea.KeyDown},
			{Type: tea.KeyUp},
			{Type: tea.KeyEnter},
			{Type: tea.KeyRunes, Runes: []rune("r")},
		}},
		{"items, walk failed", func(s *PurchaseOrderCreateScreen) {
			s.phase = poPhaseItemPick
			s.itemSuppliersAll, s.itemSuppliers, s.itemSuppliersFor = rows, rows, 1
			s.itemSuppliersErr = "items exploded"
			s.itemSuppliersNote = s.catalogVerdict("")
		}, []tea.KeyMsg{
			{Type: tea.KeyDown},
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
			{Type: tea.KeyDown},
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
			{Type: tea.KeyDown},
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
					s.Update(tea.WindowSizeMsg{Width: 80, Height: h})
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
		s.Update(tea.WindowSizeMsg{Width: 80, Height: h})
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
	poWantPaneLine(t, screen, `Not run ..... hovercraft`)
	poAssertFits(t, "uncommitted esc over rows", screen)

	// Paging replaces the note; the label must still not claim the draft ran.
	screen.assetsHasNext = true
	r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("]")})
	poRejectPaneLine(t, screen, "search: hovercraft")
	poWantPaneLine(t, screen, `Not run ..... hovercraft`)
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
	poWantPaneLine(t, screen, `Showing ..... "Lathe"`)
	poAssertFits(t, "emptied a committed asset search", screen)
	_ = r
}

// TestPOSupplierPicker_TabDoesNotCommit: tab used to commit the order's
// supplier while the bar named only the arrows, enter and esc — an unnamed key
// taking the most consequential action on the frame. It is dropped rather than
// named: tab is "next field" in this same screen's line form, and an accidental
// tab silently choosing the supplier is exactly the class this change removes.
func TestPOSupplierPicker_TabDoesNotCommit(t *testing.T) {
	for _, h := range poPaneSizes {
		fake := &poPickFake{suppliers: 3, catalog: 2, pageSize: 5}
		r, screen := poPickerAtSize(t, fake, 80, h)
		// poPickerAt commits the first supplier on entry; go back to the picker.
		r = key(t, r, tea.KeyMsg{Type: tea.KeyEsc})
		if screen.phase != poPhaseSupplier {
			t.Fatalf("80x%d: setup left phase %v, want the supplier picker", h, screen.phase)
		}
		r = key(t, r, tea.KeyMsg{Type: tea.KeyDown})
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
	poWantPaneLine(t, screen, "Showing ..... all of this supplier's assets")
	// …and the note must not claim a search has already run.
	poRejectPaneLine(t, screen, "runs the search again")
	poWantPaneLine(t, screen, "Enter=Run search")
	poAssertFits(t, "typing over an unsearched asset list", screen)

	// Once a search HAS run, "again" is true and the label names it.
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})
	r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	if !screen.assetsTyping || screen.assetsQuery != "hovercraft" {
		t.Fatalf("setup: typing=%v query=%q, want the box reopened over a run search",
			screen.assetsTyping, screen.assetsQuery)
	}
	poWantPaneLine(t, screen, `Showing ..... "hovercraft"`)
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

		// The cost row's HINT says whether a cost is required, and its note
		// says what a blank field does — both drawn by the layer under the row
		// they are about, so neither can be cut and neither can be read as
		// belonging to the wrong field.
		{"line form, catalog line", func(s *PurchaseOrderCreateScreen) {
			s.enterLinePhase(&id, nil, "Widget", 1, 0, 0, 0)
		}, []string{"optional", "supplier catalog"}},

		// The basis-toggle KEY is on the bar, where it says which basis the row
		// is on; the note under the row does the arithmetic. It used to name
		// ctrl+t as well, which is the two-surfaces defect in miniature.
		{"line form, case-packed cost basis", func(s *PurchaseOrderCreateScreen) {
			s.enterLinePhase(&id, nil, "Widget", 1, 0, 0, 12)
		}, []string{"Ctrl-T=Unit/case cost", "The CASE cost"}},

		{"line form, freeform line has no date field", func(s *PurchaseOrderCreateScreen) {
			s.enterLinePhase(nil, nil, "Shop rags", 1, 0, 0, 0)
		}, []string{"send/receive"}},
	}

	for _, tc := range cases {
		for _, h := range poPaneSizes {
			t.Run(fmt.Sprintf("%s/80x%d", tc.name, h), func(t *testing.T) {
				s := poWidestSupplierScreen()
				s.Update(tea.WindowSizeMsg{Width: 80, Height: h})
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
			for _, k := range []string{"down", "up", "down"} {
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
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEsc})                       // → supplier picker
	r = key(t, r, tea.KeyMsg{Type: tea.KeyDown})
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEsc})
	r = key(t, r, tea.KeyMsg{Type: tea.KeyUp})
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
// the cart's total becomes a floor and cartBody draws the "priced from the
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

// TestPOSourceChooser_AnOptionalKeyNeverStopsWorking.
//
// This used to be a test about a ROW. The source chooser dropped its optional
// agreement / work-order / committee rows when the pane ran short, the bar
// stopped naming g/w/c for exactly as long, and the three arms declined — so a
// supplier carrying exactly ONE optional row had it replaced by "optional rows
// need more height" at 80x24, which was FALSE (hiding one row freed nothing,
// because the substitute notice took the same slot), while a working key was
// disabled to match the lie.
//
// The conversion removed the coupling rather than repairing the arithmetic. The
// rows are pinned header rows now, ranked as context, and what a short pane
// gives up is the layer's decision (jdeHeadRank) — but a row here is a LABEL
// and a VALUE, never an affordance, so dropping one costs the operator a fact
// and never a key. So the check is the one that matters: at every pane height
// this project draws, the bar names the key and the key opens its picker.
//
// The row is asserted where it survives, which is what keeps this honest in the
// other direction: a conversion that quietly stopped drawing the values would
// pass a bar-only check.
func TestPOSourceChooser_AnOptionalKeyNeverStopsWorking(t *testing.T) {
	cases := []struct {
		name  string
		fake  func() *poPickFake
		row   string
		key   string
		phase poPhase
	}{
		{"agreement only", func() *poPickFake {
			return &poPickFake{reorder: 15, catalog: 2, assets: 1, agreements: 1}
		}, "Agreement", "g", poPhaseAgreement},
		{"work orders only", func() *poPickFake {
			return &poPickFake{reorder: 15, catalog: 2, assets: 1, workOrders: 2}
		}, "Work order", "w", poPhaseWorkOrder},
		{"committees only", func() *poPickFake {
			return &poPickFake{reorder: 15, catalog: 2, assets: 1, committees: 1}
		}, "Committee", "c", poPhaseCommittee},
		// All three at once is the fixture the old test could not tell apart
		// from one: with three rows to drop, hiding them genuinely freed rows,
		// so the defect only showed with a single row offered. Both are swept
		// now, because the rule no longer depends on how many there are.
		{"all three", func() *poPickFake {
			return &poPickFake{reorder: 15, catalog: 2, assets: 1,
				agreements: 1, workOrders: 2, committees: 1}
		}, "Committee", "c", poPhaseCommittee},
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

				// The bar names the key at every height, and there is no
				// substitute notice to make a false claim with.
				if !poBarNamedKeys(t, screen.bar())[tc.key] {
					t.Errorf("the bar stopped naming %q: %s", tc.key, poBarText(screen.bar()))
				}
				poRejectPaneLine(t, screen, "optional rows need more height")
				// The row itself is drawn at the heights this project checks.
				poWantPaneLine(t, screen, tc.row)

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

// TestPOSourceChooser_TheHighlightedLineIsAlwaysOnThePane.
//
// This used to be a test about a cart the chooser could NOT list. A long cart
// on a short pane was replaced by a one-line summary and the four keys that act
// on a row — j, k, x and ctrl+e — declined, because a line clampToBox had
// dropped was still a line x would remove and ctrl+e would edit: a wrong
// purchase order with nothing on the pane to show it.
//
// The whole apparatus is gone, and so is the state it protected. jdeLines.Window
// anchors on the CURSOR's block, so the highlighted row is on the pane by
// construction at every height the frame is drawn at; there is no "cannot list"
// case left to collapse into, no substitute sentence, and no gate. What this
// asserts is that property, directly, at the position the reported failure
// lived at: a highlight in the MIDDLE of a fifteen-line cart, where the old
// window centred and pushed both the row and its "N more below" marker past the
// bottom of the pane.
func TestPOSourceChooser_TheHighlightedLineIsAlwaysOnThePane(t *testing.T) {
	for _, phase := range []struct {
		name string
		to   []tea.KeyMsg
	}{
		{"source chooser", nil},
		{"review cart", []tea.KeyMsg{{Type: tea.KeyRunes, Runes: []rune("d")}}},
	} {
		for _, h := range poPaneSizes {
			t.Run(fmt.Sprintf("%s at 80x%d", phase.name, h), func(t *testing.T) {
				fake := &poPickFake{reorder: 15, catalog: 2, assets: 1,
					agreements: 1, workOrders: 2, committees: 1}
				r, screen := poPickerAtSize(t, fake, 80, h)
				r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
				r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")}) // add all 15
				r = key(t, r, tea.KeyMsg{Type: tea.KeyEsc})
				for _, k := range phase.to {
					r = key(t, r, k)
				}
				if len(screen.lines) != 15 {
					t.Fatalf("setup staged %d line(s), want 15", len(screen.lines))
				}

				// Walk the highlight down the WHOLE cart. Every position, not
				// the one somebody picked: the defect this replaces was
				// position-dependent, and a single probe in the middle is how it
				// would come back unseen at the ends.
				for want := 0; want < len(screen.lines); want++ {
					if screen.reviewCursor != want {
						t.Fatalf("the highlight is on line %d, want %d", screen.reviewCursor, want)
					}
					what := fmt.Sprintf("%s, 15-line cart at 80x%d, highlight on %d",
						phase.name, h, want+1)
					poAssertFits(t, what, screen)
					poWantPaneLine(t, screen, fmt.Sprintf("▸ %d)", want+1))
					if want < len(screen.lines)-1 {
						r = key(t, r, tea.KeyMsg{Type: tea.KeyDown})
					}
				}

				// …and the key that acts on it really does, from the position
				// the old window used to hide.
				screen.reviewCursor = 7
				poWantPaneLine(t, screen, "▸ 8)")
				before := len(screen.lines)
				r = key(t, r, tea.KeyMsg{Type: tea.KeyCtrlX})
				if len(screen.lines) != before-1 {
					t.Errorf("ctrl+x removed nothing (%d lines, was %d)", len(screen.lines), before)
				}
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
			r = key(t, r, tea.KeyMsg{Type: tea.KeyDown})
			r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})
			_ = r
			if screen.agreementID == nil {
				t.Fatalf("the agreement was not committed")
			}

			what := fmt.Sprintf("supplier header at 80x%d", h)
			poAssertFits(t, what, screen)

			// TWO rows now, one value each. They used to be one — "Supplier:
			// Acme (#1)  · agreement: Annual 2026 Structural Steel Contract" is
			// 67 cells, so at 51 a long supplier name took the agreement and
			// the (#id) with it, on the row drawn on every phase including
			// review. Neither may be the thing the pane drops, and each has to
			// stay identifiable at a width that cannot hold both in full.
			pane := poPaneLinesAt(t, screen, h)
			find := func(label string) string {
				for _, line := range pane {
					if strings.Contains(line, label+" .....") {
						return line
					}
				}
				return ""
			}
			supplierRow, agreementRow := find("Supplier"), find("Agreement")
			if supplierRow == "" || agreementRow == "" {
				t.Fatalf("the header lost a row (supplier %q, agreement %q):\n%s",
					supplierRow, agreementRow, strings.Join(pane, "\n"))
			}
			for _, want := range []string{"Northern", "(#1)"} {
				if !strings.Contains(supplierRow, want) {
					t.Errorf("the supplier row lost %q: %q", want, supplierRow)
				}
			}
			if !strings.Contains(agreementRow, "Annu") {
				t.Errorf("the agreement row lost its name: %q", agreementRow)
			}
			// A clipped value says it was clipped — otherwise the operator
			// reads a truncated agreement name as the whole of it.
			for _, row := range []string{supplierRow, agreementRow} {
				if !strings.Contains(row, "…") {
					t.Errorf("a long name was shortened but nothing on the row says so: %q", row)
				}
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
			poWantPaneLine(t, screen, "PO notes")
			// The headline must not read as the whole story: either OMS's own
			// words reach the pane folded under it, or the block says how many
			// rows of them it hid. An 18-row pane under a 16-line cart has room
			// for the second and not the first, which is the sacrifice order
			// working rather than a silence.
			// Two wordings for what it hid, because the block bounds the body
			// BEFORE folding it: under that bound the hidden count is exact,
			// over it the marker stops counting rather than name a number that
			// is only true of the part it folded.
			hid := poPaneHasLine(t, screen, "more line(s) of the error") ||
				poPaneHasLine(t, screen, "more of the error than this pane can hold")
			if !poPaneHasLine(t, screen, "502") && !hid {
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
			// The way to review is on the BAR, which is where every key on this
			// screen is named now — the chooser's own "d  Done" row went with
			// the rest of the block that spelled the bar a second time.
			if !poBarNamedKeys(t, screen.bar())["d"] {
				t.Errorf("the chooser carrying a failed submit stopped naming d: %s",
					poBarText(screen.bar()))
			}
			// The cart's rows are listed at every height, and the window is
			// anchored on the HIGHLIGHT — so whichever line the cursor is on is
			// the one on the pane, which is the property that retired the
			// collapsed-cart sentence and the four keys it used to gate. What
			// the cart COMES TO is pinned above it and never scrolls at all.
			poWantPaneLine(t, screen, fmt.Sprintf("▸ %d)", screen.reviewCursor+1))
			poWantPaneLine(t, screen, "Total: at least $")
		})
	}
}

// TestPOSourceChooser_TheHeaderGivesGroundByRank replaces a test about the
// screen's own sacrifice order.
//
// That order was four predicates and two substitute notices, and the step this
// test used to pin was "the TITLE goes before the cart's total": the chooser
// gave up "Where should this line come from?" and its blank line — two rows
// naming no key — so that the collapsed cart sentence kept its money.
//
// jdeHeadRank is that idea in the layer, and the title is not the interesting
// case any more because there is no title: the block that spelled the bar a
// second time went with it. What survives is the RULE, and this is it stated
// against the header the frame is really handed — most expendable first, and
// the row the builder marked essential last of all, at every height the frame
// is drawn at.
func TestPOSourceChooser_TheHeaderGivesGroundByRank(t *testing.T) {
	fake := func() *poPickFake {
		return &poPickFake{reorder: 15, catalog: 2, assets: 1,
			agreements: 1, workOrders: 2, committees: 1}
	}
	// Every height Root will draw, not the two the rest of this file uses: the
	// rank only bites where the budget is short, and 24 and 30 are both roomy.
	for h := 8; h <= 30; h++ {
		t.Run(fmt.Sprintf("80x%d", h), func(t *testing.T) {
			r, screen := poPickerAtSize(t, fake(), 80, h)
			r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
			r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
			r = key(t, r, tea.KeyMsg{Type: tea.KeyEsc})
			_ = r
			if screen.phase != poPhaseSource || len(screen.lines) != 15 {
				t.Fatalf("setup: phase %v with %d line(s)", screen.phase, len(screen.lines))
			}
			poAssertFits(t, fmt.Sprintf("source chooser at 80x%d", h), screen)

			header := screen.headerLines()
			essential := 0
			for _, row := range header {
				if row.Rank == jdeHeadEssential {
					essential++
				}
			}
			if essential != 1 {
				t.Fatalf("the chooser marks %d header rows essential, want exactly 1 — the "+
					"smallest drawable budget keeps one, so two is a claim the geometry "+
					"cannot honour", essential)
			}

			// What the frame REALLY drew. A frame the layer refused draws no
			// header at all and says so, which is a different rule
			// (jdeTooShort) and not this one.
			shown := strings.Join(poPaneLinesAt(t, screen, h), "\n")
			if strings.Contains(shown, "Too short:") {
				return
			}
			kept := map[jdeHeadRank]int{}
			for _, row := range header {
				if strings.TrimSpace(row.Text) == "" {
					continue
				}
				if strings.Contains(shown, truncateVisible(strings.TrimRight(row.Text, " "),
					screenBodyWidth(80))) {
					kept[row.Rank]++
				}
			}
			total := map[jdeHeadRank]int{}
			for _, row := range header {
				if strings.TrimSpace(row.Text) != "" {
					total[row.Rank]++
				}
			}
			if kept[jdeHeadEssential] != total[jdeHeadEssential] {
				t.Errorf("the pane at 80x%d dropped the row the chooser marked essential:\n%s", h, shown)
			}
			// A context row may only be dropped once every decorative row is
			// gone, which is the rank order seen from the pane rather than
			// asserted of the function that implements it.
			if kept[jdeHeadContext] < total[jdeHeadContext] && kept[jdeHeadDecorative] > 0 {
				t.Errorf("the pane at 80x%d dropped a context row while keeping %d decorative "+
					"one(s) — the header is giving ground by position, not by rank:\n%s",
					h, kept[jdeHeadDecorative], shown)
			}
		})
	}
}

// TestPOReorder_AddAllDoesNotInheritTheLastFailuresDetail is the stale-detail
// leak in the one arm that still wrote the screen-level failure line by hand.
//
// The failure line is a headline plus an unbounded OMS body underneath it, and
// the two are set together for exactly this reason: writing only the headline
// leaves the previous failure's DETAIL standing, so "nothing to add" was drawn
// with several folded rows of a 502 gateway page beneath it, reading as the
// gateway's explanation of a validation message. The picker's own note is where
// this decline belongs — the same place j / k / space / enter answer.
func TestPOReorder_AddAllDoesNotInheritTheLastFailuresDetail(t *testing.T) {
	for _, h := range poPaneSizes {
		t.Run(fmt.Sprintf("80x%d", h), func(t *testing.T) {
			fake := &poPickFake{catalog: 2, reorder: 0, failCreate: true}
			r, screen := poPickerAtSize(t, fake, 80, h)
			r = poStageCostlessLine(t, r, screen)
			if len(screen.lines) != 1 {
				t.Fatalf("setup staged %d line(s), want 1", len(screen.lines))
			}

			r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")})
			r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter}) // submit → 502 HTML
			if screen.errDetail == "" {
				t.Fatalf("setup: the failed submit recorded no detail to leak")
			}
			r = key(t, r, tea.KeyMsg{Type: tea.KeyEsc}) // → source chooser
			r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
			if screen.phase != poPhaseReorderPick {
				t.Fatalf("r left the screen on phase %v, want the reorder picker", screen.phase)
			}
			if len(screen.reorderItems) != 0 {
				t.Fatalf("setup: the reorder queue is not empty (%d)", len(screen.reorderItems))
			}

			before := strings.Join(poPaneLinesAt(t, screen, h), "\n")
			r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
			_ = r
			if len(screen.lines) != 1 || screen.phase != poPhaseReorderPick {
				t.Fatalf("add-all over an empty queue staged or navigated: %d line(s), phase %v",
					len(screen.lines), screen.phase)
			}
			after := strings.Join(poPaneLinesAt(t, screen, h), "\n")
			if after == before {
				t.Errorf("a redrew a byte-for-byte identical pane:\n%s", after)
			}

			poAssertFits(t, fmt.Sprintf("reorder picker after add-all at 80x%d", h), screen)
			// The decline is on the pane, and the failure line still carries
			// the headline its detail belongs to rather than this sentence.
			poWantPaneLine(t, screen, "nothing to add")
			poWantPaneLine(t, screen, "submitting this purchase order failed")

			lines := poPaneLinesAt(t, screen, h)
			decline, gateway := -1, -1
			for i, line := range lines {
				if decline < 0 && strings.Contains(line, "nothing to add") {
					decline = i
				}
				if gateway < 0 && (strings.Contains(line, "502") || strings.Contains(line, "DOCTYPE")) {
					gateway = i
				}
			}
			if gateway >= 0 && gateway > decline {
				submit := -1
				for i, line := range lines {
					if strings.Contains(line, "submitting this purchase order failed") {
						submit = i
					}
				}
				if submit < 0 || submit > gateway {
					t.Errorf("the gateway body on row %d is attributed to the decline on row %d:\n%s",
						gateway, decline, strings.Join(lines, "\n"))
				}
			}
		})
	}
}

// TestPOPickers_NoTwoGatedKeysShareASentence presses the keys that decline
// TOGETHER in one gated state, in sequence and with no reset between them.
//
// These frames draw no rows, no highlight and no focused input, so a lead
// shared by two keys means the second press redraws the pane the first one
// left — the reported hang's shape. Each of these arms used to share one:
// `a` and `enter` on a reorder frame that is loading or failed, `]` and `[` on
// the asset equivalent, and `j` and `k` on the supplier picker.
func TestPOPickers_NoTwoGatedKeysShareASentence(t *testing.T) {
	cases := []struct {
		name  string
		setup func(*PurchaseOrderCreateScreen)
		keys  []tea.KeyMsg
	}{
		{"reorder lookup failed: a then enter", func(s *PurchaseOrderCreateScreen) {
			s.phase = poPhaseReorderPick
			s.reorderLoadErr = "reorder data exploded"
		}, []tea.KeyMsg{
			{Type: tea.KeyRunes, Runes: []rune("a")},
			{Type: tea.KeyEnter},
		}},
		{"asset lookup failed: ] then [", func(s *PurchaseOrderCreateScreen) {
			s.phase = poPhaseAssetPick
			s.assetsErr = "assets exploded"
			s.assetsPage = 2
		}, []tea.KeyMsg{
			{Type: tea.KeyRunes, Runes: []rune("]")},
			{Type: tea.KeyRunes, Runes: []rune("[")},
		}},
		{"supplier lookup failed: j then k", func(s *PurchaseOrderCreateScreen) {
			s.phase = poPhaseSupplier
			s.supplierLoadErr = "suppliers exploded"
		}, []tea.KeyMsg{
			{Type: tea.KeyDown},
			{Type: tea.KeyUp},
		}},
	}

	for _, tc := range cases {
		for _, h := range poPaneSizes {
			t.Run(fmt.Sprintf("%s at 80x%d", tc.name, h), func(t *testing.T) {
				s := NewPurchaseOrderCreateScreen(Deps{})
				s.supplierID = 1
				s.Update(tea.WindowSizeMsg{Width: 80, Height: h})
				tc.setup(s)

				before := strings.Join(poPaneLinesAt(t, s, h), "\n")
				for _, k := range tc.keys {
					next, cmd := s.Update(k)
					s = next.(*PurchaseOrderCreateScreen)
					if cmd == nil {
						t.Fatalf("%q was answered with nothing at all — this is the hang", k.String())
					}
					after := strings.Join(poPaneLinesAt(t, s, h), "\n")
					if after == before {
						t.Errorf("%q redrew the pane the press before it left:\n%s", k.String(), after)
					}
					before = after
					poAssertFits(t, tc.name+" after "+k.String(), s)
				}
			})
		}
	}
}

// poTypedRow returns the whole clipped pane line the marker sits on, or a
// sentinel when the pane does not carry it. The line, never the pane: a caret
// blinking somewhere else, or a status flash, must not be able to satisfy
// "this keystroke did something".
func poTypedRow(t *testing.T, s Screen, h int, marker string) string {
	t.Helper()
	for _, line := range poPaneLinesAt(t, s, h) {
		if strings.Contains(line, marker) {
			return line
		}
	}
	return "<not on the pane>"
}

// TestPOTypedRows_EveryKeystrokeMovesTheRow is the reported hang reached by
// TYPING rather than by pressing enter: a field whose value has outgrown the
// row it is drawn on.
//
// bubbles treats Width as the width of a scrolling viewport onto the value,
// and handleOverflow returns early when it is zero — so an unset Width makes
// View() emit the entire value plus the cursor, the row grows past the pane,
// and clampToBox takes the tail. Past that column the operator keeps typing
// into a 500-character notes field (or a 200-character line description) while
// the pane comes back byte for byte identical, which is exactly the complaint
// this whole change exists to answer, on the one row the source chooser's
// sacrifice order spends rows keeping visible.
//
// Distinct runes, because a scrolling viewport full of one repeated character
// looks the same however far it has scrolled — an assertion a repeated key can
// pass is not an assertion about scrolling.
func TestPOTypedRows_EveryKeystrokeMovesTheRow(t *testing.T) {
	// runes stays under each field's CharLimit. A full field refusing the next
	// rune is a different question from a row that cannot show it — the value
	// visibly stops growing there — and the caps (60 on the two search boxes,
	// 200 and 500 on the line description and the notes) are deliberate.
	cases := []struct {
		name   string
		fake   *poPickFake
		open   []string
		marker string
		runes  int
	}{
		// The MARKER is the columnar leader now: the label right-aligned into
		// the shared column, then " ..... ". The layer draws it, so a row that
		// stopped being a jdeField would stop matching here rather than pass
		// with a hand-drawn prefix that happens to read the same.
		{"PO notes", &poPickFake{catalog: 4, suppliers: 1},
			[]string{"i", "enter", "enter", "d"}, poNotesLabel + jdeLeader, 120},
		{"line description", &poPickFake{catalog: 4, suppliers: 1},
			[]string{"f"}, poLineFieldLabel(poLineFieldDesc) + jdeLeader, 120},
		{"item filter", &poPickFake{catalog: 4, suppliers: 1},
			[]string{"i", "/"}, poItemFilterLabel + jdeLeader, 55},
		{"asset search", &poPickFake{assets: 3, suppliers: 1},
			[]string{"a", "/"}, poAssetSearchLabel + jdeLeader, 55},
	}

	for _, tc := range cases {
		for _, h := range poPaneSizes {
			t.Run(fmt.Sprintf("%s at 80x%d", tc.name, h), func(t *testing.T) {
				r, screen := poPickerAtSize(t, tc.fake, 80, h)
				for _, k := range tc.open {
					r = key(t, r, poPhaseKeyMsg(k))
				}
				before := poTypedRow(t, screen, h, tc.marker)
				if before == "<not on the pane>" {
					t.Fatalf("the %s row is not on the pane to begin with:\n%s",
						tc.name, strings.Join(poPaneLinesAt(t, screen, h), "\n"))
				}
				for i := 0; i < tc.runes; i++ {
					r = poType(t, r, string(rune('a'+i%26)))
					after := poTypedRow(t, screen, h, tc.marker)
					if after == before {
						t.Fatalf("keystroke %d into the %s row redrew it byte for byte — "+
							"the value has outgrown the row and the pane is cutting the caret off:\n\t%q",
							i+1, tc.name, after)
					}
					before = after
				}
				poAssertFits(t, fmt.Sprintf("%s after %d runes", tc.name, tc.runes), screen)
			})
		}
	}
}

// TestPOReview_ALongCatalogNameKeepsTheFactsOnTheRow is the cart row held to
// the rule every other OMS-supplied value on these screens already obeys.
//
// "1/4-20 x 1 Hex Cap Screw, Zinc" is an ordinary MRO name and 30 cells; the
// row's fixed parts — the index, the quantity, the unit price and the type
// badge — are 36 more, and clampToBox takes the TAIL, so the three facts the
// operator is confirming used to go off the right edge of the review pane
// while the name they belong to stayed. The name is what may be shortened, and
// the ellipsis is what says it was.
func TestPOReview_ALongCatalogNameKeepsTheFactsOnTheRow(t *testing.T) {
	// Both alphabets, because the row's budget is in CELLS: the second name is
	// 20 runes and 26 cells, so a clip that counted runes would hand back a
	// value up to twice the room reserved for it and the pane would take the
	// facts anyway — the same defect the clip exists to stop.
	names := []string{"1/4-20 x 1 Hex Cap Screw, Zinc", "六角ボルト 亜鉛メッキ 1/4-20"}

	for _, name := range names {
		for _, h := range poPaneSizes {
			t.Run(fmt.Sprintf("%s at 80x%d", name, h), func(t *testing.T) {
				r, screen := poPickerAtSize(t, &poPickFake{catalog: 2, suppliers: 1, itemName: name}, 80, h)
				// An expected shipment date is the ordinary shape here, not an
				// edge: lineTakesDate offers the field on item-supplier lines
				// and only those, which are exactly the lines carrying the
				// widest badge. Staged without one, the row was the single
				// shape where every fixed part happened to fit.
				for _, k := range []string{"i", "enter", "tab", "tab", "tab"} {
					r = key(t, r, poPhaseKeyMsg(k))
				}
				if got := screen.lineFocused; got != poLineFieldDate {
					t.Fatalf("three tabs left the line form on field %d, want the expected-date field", got)
				}
				r = poType(t, r, "2026-09-01")
				for _, k := range []string{"enter", "d"} {
					r = key(t, r, poPhaseKeyMsg(k))
				}
				if screen.phase != poPhaseReview || len(screen.lines) != 1 {
					t.Fatalf("setup landed on phase %v with %d line(s)", screen.phase, len(screen.lines))
				}

				row := poTypedRow(t, screen, h, "1) ")
				if row == "<not on the pane>" {
					t.Fatalf("the cart row is not on the pane at all:\n%s",
						strings.Join(poPaneLinesAt(t, screen, h), "\n"))
				}
				// The date gives before the badge does, and says so with the
				// same ellipsis the label carries — a shortened value must
				// never read as the whole one.
				for _, fact := range []string{"×1", "@ $3.5", "[Inventory item]", "exp…"} {
					if !strings.Contains(row, fact) {
						t.Errorf("the cart row lost %q to the 51-column cut — the facts are what the\n"+
							"operator confirms and the name is what may be shortened:\n\t%q", fact, row)
					}
				}
				if !strings.Contains(row, "…") {
					t.Errorf("the cart row shows no ellipsis, so a shortened name reads as the whole "+
						"name (%d-cell label %q):\n\t%q", lipgloss.Width(name), name, row)
				}
				if strings.Contains(row, name) {
					t.Errorf("the %d-cell name was drawn whole, so nothing was clipped:\n\t%q",
						lipgloss.Width(name), row)
				}
				poAssertFits(t, "review cart with a real catalog name", screen)
			})
		}
	}
}

// TestPOItemPicker_AWideRuneFailureBodyStillFitsThePane holds the folder to the
// same unit the clips are held to.
//
// omsapi.parseError puts a response body carrying no error code into
// APIError.Message whole, so the failure frame folds whatever the gateway sent
// — and pickerWords has to break a token that has no spaces in it. Cutting that
// token at `width` RUNES rather than cells handed back a piece up to twice the
// line it was cut to fit, which clampToBox then took the end off: the way-out
// keys the failure frame exists to name are what sits under it.
func TestPOItemPicker_AWideRuneFailureBodyStillFitsThePane(t *testing.T) {
	body := strings.Repeat("障害発生", 40)

	for _, h := range poPaneSizes {
		t.Run(fmt.Sprintf("80x%d", h), func(t *testing.T) {
			fake := &poPickFake{catalog: 12, failItems: true, itemsErrBody: body}
			r, screen := poPickerAtSize(t, fake, 80, h)
			r = key(t, r, poPhaseKeyMsg("i"))
			if screen.itemSuppliersErr == "" {
				t.Fatalf("setup: the catalog lookup did not fail")
			}

			poAssertFits(t, "item picker, wide-rune failure body", screen)
			poWantPaneLine(t, screen, "b=Line sources")
			_ = r
		})
	}
}

// poHugeErrorBody is the body an OMS failure can actually carry: 20 KB with no
// whitespace in it at all, the shape DRF and json.Marshal emit and the shape a
// minified gateway page arrives in. omsapi.parseError puts a body whose JSON
// envelope has no error code into APIError.Message WHOLE, so this is what the
// screens' folders and clips are handed, not the short literals every other
// error fixture here uses.
var poHugeErrorBody = strings.Repeat("A", 20000)

// TestPOCreate_AHugeErrorBodyDoesNotFreezeTheFrame is the freeze this project
// exists to remove, arriving through the fix for the last one.
//
// Bounding every width in CELLS is right, but it was done by delegating to
// truncateVisible, which drops ONE rune off the end and re-measures the whole
// remaining string: quadratic, and cubic once pickerWords cuts an unspaced
// token one piece at a time. Measured against this body before the fix, one
// clip took 711ms and one fold of a fifth of it took 1.5 seconds — and the
// source chooser rebuilds the row carrying an unavailable lookup about ten
// times per frame, so the operator got seconds of dead terminal per keystroke.
//
// The assertion is wall-clock because the defect is wall-clock. The margin is
// three orders of magnitude, not a hair: after the fix these frames render in
// single-digit milliseconds, and before it a SINGLE render of the item failure
// frame did not come back inside a minute.
func TestPOCreate_AHugeErrorBodyDoesNotFreezeTheFrame(t *testing.T) {
	const budget = 2 * time.Second
	const renders = 5

	cases := []struct {
		name  string
		fake  *poPickFake
		open  []string
		frame string
	}{
		{
			"item picker failure frame",
			&poPickFake{catalog: 12, suppliers: 1, failItems: true, itemsErrBody: poHugeErrorBody},
			[]string{"i"},
			"the failure frame folds the body",
		},
		{
			// The attribution row is the worse of the two: it is not a failure
			// frame the operator chose to look at, it is a line on the screen
			// they build the whole order from, redrawn on every press.
			"source chooser with an unavailable work-order lookup",
			&poPickFake{catalog: 4, suppliers: 1, workOrdersErrBody: poHugeErrorBody},
			nil,
			"the attribution row clips the body",
		},
	}

	for _, tc := range cases {
		for _, h := range poPaneSizes {
			t.Run(fmt.Sprintf("%s at 80x%d", tc.name, h), func(t *testing.T) {
				r, _ := poPickerAtSize(t, tc.fake, 80, h)
				for _, k := range tc.open {
					r = key(t, r, poPhaseKeyMsg(k))
				}
				// Warm the frame once outside the clock: the first render of any
				// screen builds strings the rest reuse, and the defect is not a
				// one-off cost, it is a per-keystroke one.
				_ = r.View()

				start := time.Now()
				for i := 0; i < renders; i++ {
					_ = r.View()
				}
				if took := time.Since(start); took > budget {
					t.Fatalf("%d renders of %s took %s (budget %s) — a %d-byte OMS body is "+
						"being measured end to end on every frame, which is the freeze",
						renders, tc.frame, took, budget, len(poHugeErrorBody))
				}
			})
		}
	}
}

// TestPOPickers_ALongNameKeepsTheFactsOnEveryPickerRow holds the rows an
// operator picks FROM to the rule the cart row, the supplier header and the
// association rows already obey.
//
// Every picker fixture used "Widget 1" / "Lathe 1" / "Bolt 1", seven or eight
// cells, so no test ever drew a picker row with a name of the length OMS
// actually carries. At 51 columns an ordinary MRO name pushed the SKU and the
// unit price off the right edge — and clampToBox cuts without a mark, so
// "@ 3.50" was drawn as "@ 3.", a whole-looking price that is not the price.
// The name is what may be abbreviated; the facts are what the row is picked on.
//
// Both highlight states, because StyleSidebarItemActive pads what it wraps: a
// row that fits until it is selected is cut on exactly the press that stages it.
func TestPOPickers_ALongNameKeepsTheFactsOnEveryPickerRow(t *testing.T) {
	const (
		mro        = "1/4-20 x 1 Hex Cap Screw, Zinc"
		machine    = "Haas VF-2SS Vertical Machining Center"
		supplier   = "Fastenal Industrial & Construction Supplies"
		partNumber = "M8CS-1.25X40-A2-70-DIN912-BOX100"
		assetTag   = "LATHE-HAAS-ST20Y-2021-ASSET-0000123456"
	)

	cases := []struct {
		picker string
		fake   *poPickFake
		open   []string
		long   string
		facts  []string
	}{
		{
			// The SKU is a real manufacturer part number, so the column
			// poFitRow calls facts carries an unbounded identifier as well as
			// the one number on the row. The PRICE is what must survive whole.
			"items", &poPickFake{catalog: 3, suppliers: 1, itemName: mro, itemSKU: partNumber},
			[]string{"i"}, mro, []string{"@ 3.50"},
		},
		{
			"assets", &poPickFake{
				assets: 3, suppliers: 1, assetName: machine,
				assetTag: assetTag, assetSerial: "SN-8891-2231-A",
			},
			[]string{"a"}, machine, []string{"LATHE-HAAS"},
		},
		{
			"reorder", &poPickFake{reorder: 3, suppliers: 1, reorderName: mro},
			[]string{"r"}, mro, []string{"qty 2"},
		},
		{
			"suppliers", &poPickFake{catalog: 1, suppliers: 3, supplierName: supplier},
			[]string{"esc"}, supplier, []string{"(#1)"},
		},
	}

	for _, tc := range cases {
		for _, h := range poPaneSizes {
			t.Run(fmt.Sprintf("%s at 80x%d", tc.picker, h), func(t *testing.T) {
				r, screen := poPickerAtSize(t, tc.fake, 80, h)
				for _, k := range tc.open {
					r = key(t, r, poPhaseKeyMsg(k))
				}

				// The long row is the first one, so it is highlighted to start
				// with and plain after one j — the same row, drawn both ways.
				// Five runes: the name gives down to poHeaderValueFloor on the
				// tightest of these rows, so a longer marker would be looking
				// for cells the row has already given up.
				head := string([]rune(tc.long)[:5])
				check := func(what, marker string) {
					t.Helper()
					row := poTypedRow(t, screen, h, marker)
					if row == "<not on the pane>" {
						t.Fatalf("%s: the %s row is not on the pane at all:\n%s",
							tc.picker, what, strings.Join(poPaneLinesAt(t, screen, h), "\n"))
					}
					for _, fact := range tc.facts {
						if !strings.Contains(row, fact) {
							t.Errorf("%s: the %s row lost %q to the 51-column cut — a fact the row is "+
								"picked on, and a number cut without a mark reads as a whole one:\n\t%q",
								tc.picker, what, fact, row)
						}
					}
					if !strings.Contains(row, "…") {
						t.Errorf("%s: the %s row carries no ellipsis, so a shortened %d-cell name reads "+
							"as the whole name:\n\t%q", tc.picker, what, lipgloss.Width(tc.long), row)
					}
					if strings.Contains(row, tc.long) {
						t.Errorf("%s: the %s row drew the %d-cell name whole, so nothing was bounded:\n\t%q",
							tc.picker, what, lipgloss.Width(tc.long), row)
					}
					poAssertFits(t, tc.picker+" picker, "+what+" row", screen)
				}

				check("highlighted", "▸")
				r = key(t, r, poPhaseKeyMsg("down"))
				check("unhighlighted", head)
			})
		}
	}
}

// TestPOPickers_AWideTerminalDrawsTheWholeRow is the other half of the bound:
// 80 columns is the width that must HOLD, not the width to render as though we
// had.
//
// Every clip on these screens was measured against pickerPaneWidth — the
// 51-cell pane an 80-column terminal gets — evaluated once at package level,
// and the screen recorded only the terminal's HEIGHT. So a 120-column terminal
// drew every picker row abbreviated to 45 cells with forty columns of pane left
// blank. Folding a hint narrow costs an extra line and loses nothing; clipping
// a value narrow destroys the tail of a name the operator had room to read, on
// the rows they pick FROM.
//
// The assertion is comparative rather than a hand-computed width, because what
// is at stake is that the extra columns are USED: the same row, same data, must
// draw wider at 120 than at 80. A future narrowing fails here rather than on
// the operator's terminal.
func TestPOPickers_AWideTerminalDrawsTheWholeRow(t *testing.T) {
	const (
		mro        = "1/4-20 x 1 Hex Cap Screw, Zinc"
		machine    = "Haas VF-2SS Vertical Machining Center"
		supplier   = "Fastenal Industrial & Construction Supplies"
		partNumber = "M8CS-1.25X40-A2-70-DIN912-BOX100"
		assetTag   = "LATHE-HAAS-ST20Y-2021-ASSET-0000123456"
	)

	cases := []struct {
		picker string
		fake   func() *poPickFake
		open   []string
		// whole is what the WIDEST pane has room to draw in full: at 120 the
		// operator sees the identifier OMS actually stores, not its head.
		whole []string
	}{
		{
			"items",
			func() *poPickFake {
				return &poPickFake{catalog: 3, suppliers: 1, itemName: mro, itemSKU: partNumber}
			},
			[]string{"i"}, []string{mro, partNumber, "@ 3.50"},
		},
		{
			"assets",
			func() *poPickFake {
				return &poPickFake{assets: 3, suppliers: 1, assetName: machine, assetTag: assetTag}
			},
			[]string{"a"}, []string{machine, assetTag},
		},
		{
			"reorder",
			func() *poPickFake { return &poPickFake{reorder: 3, suppliers: 1, reorderName: mro} },
			[]string{"r"}, []string{mro, "qty 2"},
		},
		{
			"suppliers",
			func() *poPickFake { return &poPickFake{catalog: 1, suppliers: 3, supplierName: supplier} },
			[]string{"esc"}, []string{supplier, "(#1)"},
		},
	}

	for _, tc := range cases {
		for _, h := range poPaneSizes {
			t.Run(fmt.Sprintf("%s at %d rows", tc.picker, h), func(t *testing.T) {
				rows := map[int]string{}
				for _, w := range poPaneWidths {
					r, screen := poPickerAtSize(t, tc.fake(), w, h)
					for _, k := range tc.open {
						r = key(t, r, poPhaseKeyMsg(k))
					}
					_ = r
					// Nothing may overflow at ANY of the three, measured
					// against the pane that width really gives.
					poAssertFits(t, fmt.Sprintf("%s picker at %dx%d", tc.picker, w, h), screen)

					row := poTypedRow(t, screen, h, "▸")
					if row == "<not on the pane>" {
						t.Fatalf("%s: no highlighted row on the pane at %dx%d:\n%s",
							tc.picker, w, h, strings.Join(poPaneLinesAt(t, screen, h), "\n"))
					}
					rows[w] = row
				}

				narrow, wide := rows[poPaneWidths[0]], rows[poPaneWidths[len(poPaneWidths)-1]]
				if lipgloss.Width(wide) <= lipgloss.Width(narrow) {
					t.Errorf("%s: the row is %d cells at %d columns and %d at %d — the wider pane's "+
						"columns are left blank while the value is clipped as though they were not there:"+
						"\n\t%q\n\t%q",
						tc.picker, lipgloss.Width(narrow), poPaneWidths[0],
						lipgloss.Width(wide), poPaneWidths[len(poPaneWidths)-1], narrow, wide)
				}
				for _, want := range tc.whole {
					if !strings.Contains(wide, want) {
						t.Errorf("%s: at %d columns the row still does not carry %q whole, so a value "+
							"was discarded for a pane the terminal never had:\n\t%q",
							tc.picker, poPaneWidths[len(poPaneWidths)-1], want, wide)
					}
				}
			})
		}
	}
}

// ---------------------------------------------------------------------------
// The search boxes: the bar names exactly the keys the box leaves alive
// ---------------------------------------------------------------------------

// This replaces FIVE tests, and the reason they collapse into one is the whole
// shape of the conversion.
//
// Each of them was about a NOTE naming keys. With a search box open, `/` is a
// slash going into the query, `b` is a letter and `r` is a letter; esc closes
// the box rather than cancelling the order; and enter's meaning depends on how
// many rows matched. The pickers stated all of that TWICE — in a prose action
// bar at the top of the pane and again in the frame's own note — so each state
// needed a test pinning the two surfaces to each other, and they still drifted
// (TestPOItemPicker_BarAndNoteNeverDisagreeAboutEnter existed because they had).
//
// The notes name no keys at all now. There is one surface, the action bar, and
// the rule it is held to is the rule this presses: in every search-box state,
// every key the bar names ACTS and every key it does not name does not. The
// key space is the whole space, so a key bound behind one of these boxes
// tomorrow is pressed by this today — the typing states are the ones the pane
// sweep excludes, precisely because printable runes act there by design, so
// this is where they get their coverage.
//
// Printable runes and the field-editing keys are skipped in the REVERSE
// direction only: typing into the box is what the box is for.
func TestPOSearchBoxes_TheBarNamesExactlyTheKeysThatWork(t *testing.T) {
	type boxState struct {
		name  string
		fake  func() *poPickFake
		reach func(*testing.T, Root) Root
	}
	open := func(source string, query string) func(*testing.T, Root) Root {
		return func(t *testing.T, r Root) Root {
			r = key(t, r, poPickerKeyMsg(source))
			r = key(t, r, poPickerKeyMsg("/"))
			for _, c := range query {
				r = poType(t, r, string(c))
			}
			return r
		}
	}
	// The box opened OVER a lookup that is still out: the source key is fired
	// with a bare Update so its command never runs, and '/' opens the box on
	// top of it. That is the state the asset box's Enter is gated in, and it is
	// reachable exactly this way — pressing Enter inside the box would close
	// the box, which is why the obvious reach does not produce it.
	openMidFlight := func(source string) func(*testing.T, Root) Root {
		return func(t *testing.T, r Root) Root {
			next, _ := r.Update(poPickerKeyMsg(source))
			r = next.(Root)
			next, _ = r.Update(poPickerKeyMsg("/"))
			return next.(Root)
		}
	}
	states := []boxState{
		{"item filter, several matched", func() *poPickFake { return &poPickFake{catalog: 12, pageSize: 12} },
			open("i", "Widget 1")},
		{"item filter, exactly one matched", func() *poPickFake { return &poPickFake{catalog: 12, pageSize: 12} },
			open("i", "Widget 12")},
		{"item filter, matched nothing", func() *poPickFake { return &poPickFake{catalog: 12, pageSize: 12} },
			open("i", "flux capacitor")},
		{"item filter, unfiltered", func() *poPickFake { return &poPickFake{catalog: 12, pageSize: 12} },
			open("i", "")},
		{"item filter, opened over a walk in flight", func() *poPickFake {
			return &poPickFake{catalog: 12, pageSize: 2}
		}, openMidFlight("i")},
		{"asset search, box open", func() *poPickFake { return &poPickFake{assets: 3} },
			open("a", "Lathe")},
		{"asset search, opened over a lookup in flight", func() *poPickFake {
			return &poPickFake{assets: 3}
		}, openMidFlight("a")},
	}

	for _, st := range states {
		for _, h := range poPaneSizes {
			t.Run(fmt.Sprintf("%s at 80x%d", st.name, h), func(t *testing.T) {
				fresh := func() (Root, *PurchaseOrderCreateScreen) {
					r, s := poPickerAtSize(t, st.fake(), 80, h)
					return st.reach(t, r), s
				}
				_, screen := fresh()
				if !screen.itemSuppliersTyping && !screen.assetsTyping {
					t.Fatalf("the reach left no search box open, so this state is not the one "+
						"under test:\n%s", strings.Join(poPaneLinesAt(t, screen, h), "\n"))
				}
				bar := screen.bar()
				named := poBarNamedKeys(t, bar)
				poAssertFits(t, st.name, screen)

				// Every claim the bar makes is READABLE on the pane it is drawn
				// on. A key named on a bar the pane cut is a key the operator
				// cannot discover.
				for _, it := range bar {
					poWantPaneLine(t, screen, it.Key+"="+it.Label)
				}
				// And the BODY names none of them: one surface, which is what
				// the five tests this replaces were each policing a corner of.
				// DERIVED from poBarKeyNames — the roster of six phrases that
				// used to stand here could only find duplication somebody had
				// already thought of, and two notes naming keys sat under it.
				poAssertBodyNamesNoKey(t, st.name, screen, h)

				for _, k := range poKeySpace() {
					if (poIsPrintable(k) || poFieldKeys[k]) && !named[k] {
						continue
					}
					r, s := fresh()
					before := poPickerState(s)
					_, cmd := r.Update(poPickerKeyMsg(k))
					acted := poPickerState(s) != before || poCmdActs(cmd)
					switch {
					case named[k] && !acted:
						t.Errorf("%s names %q but pressing it changes nothing (bar: %s)",
							st.name, k, poBarText(bar))
					case !named[k] && acted:
						t.Errorf("%s does not name %q, but pressing it acts (bar: %s)",
							st.name, k, poBarText(bar))
					}
				}
			})
		}
	}
}

// poIsBlink recognises the cursor tick bubbles returns for any key a focused
// textinput does not handle itself. Matched by TYPE NAME rather than by
// importing the message, and checked against textinput.Blink() by the test
// below, so a bubbles rename fails loudly instead of silently turning this into
// a filter that skips a real message.
func poIsBlink(msg tea.Msg) bool {
	return strings.Contains(strings.ToLower(fmt.Sprintf("%T", msg)), "blink")
}

func TestPOCreate_TheBlinkIsWhatTheSweepsIgnore(t *testing.T) {
	if !poIsBlink(textinput.Blink()) {
		t.Fatalf("textinput.Blink now produces %T, which poIsBlink does not match — every "+
			"key that reaches a focused box would read as acting again", textinput.Blink())
	}
	for _, msg := range []tea.Msg{
		StatusMsg{},
		poItemSuppliersLoadedMsg{},
		poCreatedMsg{},
		SwitchScreenMsg{},
	} {
		if poIsBlink(msg) {
			t.Errorf("poIsBlink matches %T, which the sweeps must not ignore", msg)
		}
	}
}
