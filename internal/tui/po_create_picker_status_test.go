package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

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
			envelope([]map[string]any{{"id": 1, "name": "Acme Supply"}}, 1)
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
