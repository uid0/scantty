package tui

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/uid0/scantty/internal/omsapi"
)

// A scanned barcode reaches the endpoint that can resolve it (sc-classify-hex).
//
// scanner.Classify used to claim every 8-to-20-character hex string as a
// ForgeKey badge and the scan screen answered one with "badge lookup not yet
// wired" WITHOUT a round trip — and since a UPC is digits, and digits are hex,
// that is every barcode format OMS's dispatcher knows: UPC-A (12), UPC-E (8),
// EAN-13 (13), ITF-14 (14), the whole of `resolvers._UPC_LENGTHS`.
//
// This is the behavioural half of the fix and it asserts the thing the unit
// tests cannot: that the payload really leaves the process. A classifier test
// alone would go green on a screen that still short-circuited somewhere else.
//
// The lengths are the SERVER's roster rather than a sample, because the retired
// rule was a LENGTH rule and a single fixture would have pinned one length of
// the four it broke.
func TestScan_ABarcodeReachesTheDispatcher(t *testing.T) {
	for _, tc := range []struct{ name, code string }{
		{"UPC-A", "036000291452"},
		{"UPC-E", "04252614"},
		{"EAN-13", "4006381333931"},
		{"EAN-8", "96385074"},
		{"ITF-14", "10036000291459"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var dispatched string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.URL.Path != "/api/scanner/dispatch/" {
					// Whatever the detail screen fetches once the scan lands.
					// It must not overwrite the record of what was dispatched.
					_, _ = w.Write([]byte(`{"id":"item-9","name":"Hex bolt M8x40 zinc"}`))
					return
				}
				raw, _ := io.ReadAll(r.Body)
				body := map[string]string{}
				_ = json.Unmarshal(raw, &body)
				dispatched = body["payload"]
				_ = json.NewEncoder(w).Encode(map[string]any{
					"action":      "inventory_receive",
					"target_type": "inventory_item",
					"target_id":   "item-9",
					"target_name": "Hex bolt M8x40 zinc",
					"target_url":  "/inventory/items/item-9",
				})
			}))
			defer srv.Close()

			deps := Deps{OMS: omsapi.New(srv.URL), Ctx: context.Background()}
			root := newTestRoot(NewScanScreen(deps))
			root.deps = deps

			// A scanner gun is a keyboard burst plus Enter.
			kitDriveType(t, &root, tc.code)
			next, cmd := root.Update(tea.KeyMsg{Type: tea.KeyEnter})
			root = next.(Root)
			root = pump(t, root, cmd, 0)

			if dispatched != tc.code {
				t.Fatalf("the dispatcher saw payload %q, want the scanned barcode %q "+
					"(empty means the scan never left the process)", dispatched, tc.code)
			}
			detail, ok := root.screen.(*InventoryDetailScreen)
			if !ok {
				t.Fatalf("the scan left the operator on %T, want the item it identified", root.screen)
			}
			if detail.itemID != "item-9" {
				t.Errorf("detail id = %q, want the scanned record", detail.itemID)
			}
		})
	}
}

// A badge that nothing resolved says so, and says it is a MAYBE.
//
// This is what replaces the pre-emptive refusal, and the difference is where it
// happens: the dispatcher is asked FIRST, so a code that turns out to be a
// barcode still opens, and only a code nothing placed gets the caveat. A bare
// "no match" would leave an operator holding a card to conclude the card is
// broken, when what is really true is that ScanTTY cannot look badges up at all
// — OMS exposes no member-by-badge read endpoint.
func TestScan_AnUnmatchedBadgeExplainsItself(t *testing.T) {
	var dispatched string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		raw, _ := io.ReadAll(r.Body)
		body := map[string]string{}
		_ = json.Unmarshal(raw, &body)
		dispatched = body["payload"]
		// The dispatcher's own answer for a payload it cannot place.
		_ = json.NewEncoder(w).Encode(map[string]any{"action": "unknown"})
	}))
	defer srv.Close()

	deps := Deps{OMS: omsapi.New(srv.URL), Ctx: context.Background()}
	root := newTestRoot(NewScanScreen(deps))
	root.deps = deps

	const badge = "1234567890" // 10 digits — the documented ForgeKey badge shape.
	kitDriveType(t, &root, badge)
	next, cmd := root.Update(tea.KeyMsg{Type: tea.KeyEnter})
	root = next.(Root)
	root = pump(t, root, cmd, 0)

	// Asked, not pre-empted: the badge went to the server like anything else.
	if dispatched != badge {
		t.Fatalf("the dispatcher saw payload %q, want the badge %q — a badge is asked "+
			"about before it is given up on", dispatched, badge)
	}
	note := noMatchNote(badge)
	if !strings.Contains(note, "access badge") || !strings.Contains(note, badge) {
		t.Fatalf("the no-match note for a badge was %q, want it to name the code and "+
			"say an access badge cannot be looked up yet", note)
	}
	// A code that is NOT badge-shaped must not collect the caveat, or the
	// sentence stops carrying any information at all.
	if plain := noMatchNote("WIDGET-001"); strings.Contains(plain, "access badge") {
		t.Errorf("a non-badge code got the badge caveat: %q", plain)
	}
}
