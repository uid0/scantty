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

	failItems  bool
	failAssets bool
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
		case strings.Contains(r.URL.Path, "/suppliers/"):
			n := f.suppliers
			if n <= 0 {
				n = 1
			}
			rows := []map[string]any{{"id": 1, "name": "Acme Supply"}}
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
	srv := httptest.NewServer(fake.handler())
	t.Cleanup(srv.Close)

	deps := Deps{OMS: omsapi.New(srv.URL), Ctx: context.Background()}
	screen := NewPurchaseOrderCreateScreen(deps)
	r := newTestRoot(screen)
	r.deps = deps
	next, _ := r.Update(tea.WindowSizeMsg{Width: width, Height: 30})
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
	s := reorderScreen()
	s.phase = poPhaseReorderPick
	_, cmd := s.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("enter over an empty reorder queue said nothing")
	}
	msg, ok := cmd().(StatusMsg)
	if !ok {
		t.Fatalf("produced %T, want a status", cmd())
	}
	if !strings.Contains(msg.Text, "nothing flagged for reorder") {
		t.Errorf("said %q", msg.Text)
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
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := NewPurchaseOrderCreateScreen(Deps{})
			s.supplierID = 1
			tc.setup(s)

			_, cmd := s.Update(tc.key)
			if cmd == nil {
				t.Fatalf("the key was answered with nothing at all — this is the hang")
			}
			msg := cmd()
			status, ok := msg.(StatusMsg)
			if !ok {
				if batch, isBatch := msg.(tea.BatchMsg); isBatch {
					for _, c := range batch {
						if st, is := c().(StatusMsg); is {
							status, ok = st, true
							break
						}
					}
				}
			}
			if !ok {
				t.Fatalf("the key produced %T, want words for the operator", msg)
			}
			if strings.TrimSpace(status.Text) == "" {
				t.Fatal("the key produced an empty status")
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
func poPaneLines(t *testing.T, s Screen) []string {
	t.Helper()
	return strings.Split(clampToBox(s.View(), screenBodyWidth(80), 400), "\n")
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
		t.Errorf("the 51-column pane does not carry %q on any whole line:\n%s",
			want, strings.Join(poPaneLines(t, s), "\n"))
	}
}

// poRejectPaneLine fails if want appears anywhere in the pane.
func poRejectPaneLine(t *testing.T, s Screen, want string) {
	t.Helper()
	if poPaneHasLine(t, s, want) {
		t.Errorf("the pane still says %q:\n%s", want, strings.Join(poPaneLines(t, s), "\n"))
	}
}

// poAssertFits fails for every line of a picker frame that the pane would cut.
func poAssertFits(t *testing.T, what, frame string) {
	t.Helper()
	budget := screenBodyWidth(80)
	for i, line := range strings.Split(frame, "\n") {
		if w := lipgloss.Width(line); w > budget {
			t.Errorf("%s: line %d is %d cells and is cut at %d, losing %q\n\tfull line: %q",
				what, i+1, w, budget, string([]rune(line)[budget:]), line)
		}
	}
}

// poWidestSupplierScreen is a create screen whose supplier name is at the
// 20-character clip supplierLabel allows. Every "Looking up what <supplier>…"
// line has to survive THAT, not the short name the other fixtures use — the
// reorder working line was 52 cells for an eleven-character supplier.
func poWidestSupplierScreen() *PurchaseOrderCreateScreen {
	s := NewPurchaseOrderCreateScreen(Deps{})
	s.suppliers = []omsapi.Supplier{{ID: 1, Name: "Northern Tool & Die Supply Co"}}
	s.supplierID = 1
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
	long := strings.Repeat("x", 200) // an OMS error string is not bounded

	cases := []struct {
		name  string
		setup func(*PurchaseOrderCreateScreen)
		frame func(*PurchaseOrderCreateScreen) string
		// want is a phrase that has to survive on one line of the frame, not
		// merely somewhere in the string.
		want []string
	}{
		{"reorder, working", func(s *PurchaseOrderCreateScreen) {
			s.reorderLoading = true
		}, (*PurchaseOrderCreateScreen).renderReorderPick, []string{"reorder"}},

		{"reorder, failed", func(s *PurchaseOrderCreateScreen) {
			s.reorderLoadErr = long
		}, (*PurchaseOrderCreateScreen).renderReorderPick,
			[]string{"reading the reorder queue failed", "b picks another line source", "esc cancels the order"}},

		{"reorder, empty", func(s *PurchaseOrderCreateScreen) {},
			(*PurchaseOrderCreateScreen).renderReorderPick,
			[]string{"b picks another line source", "esc cancels the order"}},

		{"reorder, list", func(s *PurchaseOrderCreateScreen) {
			s.reorderItems = []omsapi.ReorderDataItem{
				reorderItem("Bolt M8", 11, 4, ""),
				reorderItem("Bolt M10", 12, 6, ""),
			}
		}, (*PurchaseOrderCreateScreen).renderReorderPick, []string{"a adds all"}},

		{"items, working", func(s *PurchaseOrderCreateScreen) {
			s.itemSuppliersLoad = true
		}, (*PurchaseOrderCreateScreen).renderItemPick, []string{"Looking up the items"}},

		{"items, failed", func(s *PurchaseOrderCreateScreen) {
			s.itemSuppliersErr = long
		}, (*PurchaseOrderCreateScreen).renderItemPick,
			[]string{"looking up this supplier's items failed", "r retries the lookup",
				"b picks another line source", "esc cancels the order"}},

		{"items, failed while the search box is open", func(s *PurchaseOrderCreateScreen) {
			s.itemSuppliersErr = long
			s.itemSuppliersTyping = true
		}, (*PurchaseOrderCreateScreen).renderItemPick,
			[]string{"looking up this supplier's items failed", "esc closes the search"}},

		{"items, empty catalog", func(s *PurchaseOrderCreateScreen) {
			s.itemSuppliersNote = pickerNote{s.noCatalogSentence(), StatusWarn}
		}, (*PurchaseOrderCreateScreen).renderItemPick,
			[]string{"has no active catalog items", "b picks another line source", "esc cancels the order"}},

		{"items, no note at all", func(s *PurchaseOrderCreateScreen) {},
			(*PurchaseOrderCreateScreen).renderItemPick,
			[]string{"has no active catalog items", "b picks another line source"}},

		{"items, search matched nothing", func(s *PurchaseOrderCreateScreen) {
			s.itemSuppliersAll = poCatalog(400)
			s.itemSuppliersTyping = true
			s.itemSuppliersSearch.SetValue("a search nobody would type")
			s.applyItemSupplierFilter()
			s.itemSuppliersNote = itemFilterNote(s.itemSuppliersSearch.Value(), 0, 400, "", true)
		}, (*PurchaseOrderCreateScreen).renderItemPick,
			[]string{"no match for", "in catalog", "edit the search"}},

		{"items, several matched, after esc closed the box", func(s *PurchaseOrderCreateScreen) {
			s.itemSuppliersAll = poCatalog(400)
			s.itemSuppliersSearch.SetValue("Widget 1")
			s.applyItemSupplierFilter()
			s.itemSuppliersNote = itemFilterNote("Widget 1", len(s.itemSuppliers), 400, "search closed", false)
		}, (*PurchaseOrderCreateScreen).renderItemPick,
			[]string{"search closed", "match", "j/k choose", "enter picks"}},

		{"items, exactly one matched, after esc closed the box", func(s *PurchaseOrderCreateScreen) {
			s.itemSuppliersAll = poCatalog(400)
			s.itemSuppliersSearch.SetValue("Widget 137")
			s.applyItemSupplierFilter()
			s.itemSuppliersNote = itemFilterNote("Widget 137", 1, 400, "search closed", false)
		}, (*PurchaseOrderCreateScreen).renderItemPick,
			[]string{"search closed", "enter picks it"}},

		{"items, unfiltered count", func(s *PurchaseOrderCreateScreen) {
			s.itemSuppliersAll = poCatalog(400)
			s.applyItemSupplierFilter()
			s.itemSuppliersNote = itemFilterNote("", 400, 400, "search closed", false)
		}, (*PurchaseOrderCreateScreen).renderItemPick,
			[]string{"search closed", "enter picks the highlighted row"}},

		{"assets, working", func(s *PurchaseOrderCreateScreen) {
			s.assetsLoading = true
		}, (*PurchaseOrderCreateScreen).renderAssetPick, []string{"Looking up the assets"}},

		{"assets, failed", func(s *PurchaseOrderCreateScreen) {
			s.assetsErr = long
		}, (*PurchaseOrderCreateScreen).renderAssetPick,
			[]string{"looking up this supplier's assets failed", "/ retries with a search",
				"b picks another line source", "esc cancels the order"}},

		{"assets, failed while the search box is open", func(s *PurchaseOrderCreateScreen) {
			s.assetsErr = long
			s.assetsTyping = true
		}, (*PurchaseOrderCreateScreen).renderAssetPick,
			[]string{"looking up this supplier's assets failed", "esc closes the search"}},

		{"assets, empty", func(s *PurchaseOrderCreateScreen) {
			s.assetsNote = pickerNote{"this supplier has no assets on file", StatusWarn}
		}, (*PurchaseOrderCreateScreen).renderAssetPick,
			[]string{"no assets on file", "/ searches", "b picks another line source", "esc cancels the order"}},

		{"assets, search matched nothing", func(s *PurchaseOrderCreateScreen) {
			s.assetsSearch.SetValue("hovercraft full of eels")
			s.assetsNote = pickerNote{
				"no asset matches " + strconv.Quote(pickerClip("hovercraft full of eels", 16)) +
					"\n/ edits the search · b picks another source", StatusWarn}
		}, (*PurchaseOrderCreateScreen).renderAssetPick,
			[]string{"no asset matches", "/ edits the search", "b picks another source"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := poWidestSupplierScreen()
			tc.setup(s)
			frame := tc.frame(s)
			poAssertFits(t, tc.name, frame)

			clipped := clampToBox(frame, screenBodyWidth(80), 400)
			for _, want := range tc.want {
				found := false
				for _, line := range strings.Split(clipped, "\n") {
					if strings.Contains(line, want) {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("the clipped frame does not carry %q on any whole line:\n%s", want, clipped)
				}
			}
		})
	}
}

// poCatalog builds n catalog rows named the way the httptest fake names them.
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
	poAssertFits(t, "ambiguous search", screen.View())

	// And the esc-closes-the-box variant, whose "search closed · " prefix is
	// what pushed the same note from 53 cells to 69.
	r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEsc})
	poWantPaneLine(t, screen, "search closed")
	poWantPaneLine(t, screen, "enter picks")
	poAssertFits(t, "search closed", screen.View())
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
		poAssertFits(t, "empty catalog", screen.View())
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
	poAssertFits(t, "mid-walk", screen.View())

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
	poAssertFits(t, "zero match, box open", screen.View())

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
	poAssertFits(t, "zero match, box closed", screen.View())

	// And the keys the closed frame names do what it says.
	r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("b")})
	if screen.phase != poPhaseSource {
		t.Fatalf("b on the closed zero-match frame did not pick another source (phase %v)", screen.phase)
	}
}
