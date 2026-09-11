package tui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/uid0/scantty/internal/omsapi"
)

// Where does ScanTTY present transparency ORDER data?
//
// THE DERIVED SET, from the one client method that fetches the feed —
// omsapi.Client.ReorderTransparency — and every caller of it, which is the
// three transparency tabs of NewReorderAnalyticsReportScreen among every tab
// of every report screen in reportScreenFixtures
// (TestTransparency_TheDerivedSetIsTheSetSwept derives it by running every
// report tab's loader):
//
//   - "Trans. orders" renders `orders[]`, one row per ReorderRequest. This is
//     where OMS #1057 withdrew `supplier_name` and `estimated_cost`: a
//     ReorderRequest has no supplier relationship and records no estimate, so
//     both were the ITEM's, resolved when the response was built, published
//     under the order's name.
//   - "Trans. POs" renders `purchase_orders[]`. Its Supplier is a PurchaseOrder
//     FK and its totals are the order's own, so every column stays — but the
//     three vendor keys are WITHHELD from a caller the server does not let see
//     vendors, which "—" used to render as "none recorded".
//   - "Transparency" renders `summary`, aggregates over the same two sets. It
//     carries no per-order vendor figure and the server never withholds it.
//
// DELIBERATE EXCLUSIONS. `ledger[]` is not decoded at all (omsapi's doc on
// ReorderTransparency: every key it carries is on `orders[]`). The reorder
// QUEUE's estimated cost (reorder_queue.go) is ReorderRequestSerializer's, on
// requests nobody has placed yet — a price at today's supplier is what a
// not-yet-placed request WOULD cost, which is an honest estimate there and the
// fabrication only once the request is history.
//
// EVERY BODY IS RECORDED, never written: internal/omsapi/testdata/README.md
// carries the provenance. A fixture in the NEW shape alone cannot tell a screen
// that dropped a column from one with nothing left to fill it, so the pre-#1057
// recording — the same rows, served by the parent commit — is what the
// substituted-supplier check is asked of.

// transparencyWire reads a recorded transparency response.
func transparencyWire(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "omsapi", "testdata", name))
	if err != nil {
		t.Fatalf("recorded response %s: %v", name, err)
	}
	return b
}

// transparencyTab is the index of the tab with this label, read off the screen
// rather than written down, so a tab inserted ahead of it cannot point these
// checks at a different report.
func transparencyTab(t *testing.T, label string) int {
	t.Helper()
	for i, tab := range NewReorderAnalyticsReportScreen(Deps{}).tabs {
		if tab.label == label {
			return i
		}
	}
	t.Fatalf("the reorders analytics report has no %q tab", label)
	return -1
}

// transparencyPane drives the REAL screen through Root against a server that
// answers the transparency route with `body`: it lands on the first tab, walks
// right to `label` one keypress at a time, and returns the pane Root clipped to
// a w x h terminal — the only render worth asserting a bound on.
func transparencyPane(t *testing.T, body []byte, label string, w, h int) []string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		rw.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/reorders/analytics/transparency/" {
			_, _ = rw.Write(body)
			return
		}
		_, _ = rw.Write([]byte(`[]`))
	}))
	t.Cleanup(srv.Close)
	deps := Deps{OMS: omsapi.New(srv.URL), Ctx: context.Background()}
	s := NewReorderAnalyticsReportScreen(deps)
	r := newTestRoot(s)
	r.deps = deps
	next, _ := r.Update(tea.WindowSizeMsg{Width: w, Height: h})
	r = next.(Root)
	r = pump(t, r, s.Init(), 0)
	for i := 0; i < transparencyTab(t, label); i++ {
		r = key(t, r, tea.KeyMsg{Type: tea.KeyRight})
	}
	if s.tabs[s.active].label != label {
		t.Fatalf("walked to tab %q, want %q", s.tabs[s.active].label, label)
	}
	if st := s.states[s.active]; !st.loaded || st.err != "" {
		t.Fatalf("%q did not load: loaded=%v err=%q", label, st.loaded, st.err)
	}
	return reportPaneLines(strings.Split(r.View(), "\n"))
}

// transparencyHeader is the pane's column-header row: the one line carrying
// every header the tab declares that is drawn first. Asked of the header TEXT
// the tab declares, so it is found whatever columns follow it.
func transparencyHeader(t *testing.T, pane []string, first string) (int, string) {
	t.Helper()
	for i, line := range pane {
		if fields := strings.Fields(line); len(fields) > 0 && fields[0] == first {
			return i, line
		}
	}
	t.Fatalf("no column-header row starting %q on the pane:\n%s", first, strings.Join(pane, "\n"))
	return -1, ""
}

// transparencyCell reads one column of one row off the rendered pane, by where
// the HEADER sits: a right-aligned column ends where its header ends, a
// left-aligned one starts where its header starts. Nothing here knows which
// column is last or how many there are, so a check reading "Actual" keeps
// reading Actual whatever the table grows or loses around it.
func transparencyCell(t *testing.T, header, row, col string, align colAlign) string {
	t.Helper()
	rr := []rune(row)
	start := strings.Index(header, col)
	if start < 0 {
		t.Fatalf("no %q column on the header row %q", col, header)
	}
	start = len([]rune(header[:start]))
	if align == alignRight {
		end := start + len([]rune(col))
		if end > len(rr) {
			end = len(rr)
		}
		fields := strings.Fields(string(rr[:end]))
		if len(fields) == 0 {
			return ""
		}
		return fields[len(fields)-1]
	}
	if start >= len(rr) {
		return ""
	}
	rest := string(rr[start:])
	if i := strings.Index(rest, "  "); i >= 0 {
		rest = rest[:i]
	}
	return strings.TrimSpace(rest)
}

// transparencyRow is the pane row whose item/PO identifier starts with `lead`.
func transparencyRow(t *testing.T, pane []string, below int, lead string) string {
	t.Helper()
	for _, line := range pane[below+1:] {
		trimmed := strings.TrimLeft(strings.TrimPrefix(strings.TrimLeft(line, " "), "▸"), " ")
		if strings.HasPrefix(trimmed, lead) {
			return line
		}
	}
	t.Fatalf("no row starting %q under the header:\n%s", lead, strings.Join(pane, "\n"))
	return ""
}

// rawOrders is the recorded body's orders[] as the server sent them.
func rawOrders(t *testing.T, body []byte) []map[string]any {
	t.Helper()
	var raw struct {
		Orders []map[string]any `json:"orders"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		t.Fatalf("recorded body is not JSON: %v", err)
	}
	if len(raw.Orders) == 0 {
		t.Fatal("the recorded body has no orders, so every check over it would be vacuous")
	}
	return raw.Orders
}

// THE REPORTED DEFECT, asked of an OMS that still SENDS the substituted keys.
//
// Before #1057 the feed put the item's current supplier and a live re-quote on
// each order row under the names `supplier_name` and `estimated_cost`, and this
// tab drew them under "Supplier" and "Est" — an attribution no order ever had.
// A deployment that has not taken #1057 still sends them, so the screen has to
// keep them off the pane by not reading them, not by the server having stopped.
// Measured on the pane at every width Root draws, because a column the fit drops
// is absent at 80 and drawn at 120 — which is how the pre-change screen passed
// at the narrow end and put `McMaster-Carr Supply Company` beside a $1,243.70
// actual at the wide one.
func TestTransOrders_AnItemsSubstitutedSupplierNeverReachesThePane(t *testing.T) {
	body := transparencyWire(t, "transparency_pre1057_signed_in.json")
	var names, quotes []string
	for _, o := range rawOrders(t, body) {
		if n, _ := o["supplier_name"].(string); n != "" {
			names = append(names, n)
		}
		if q, ok := o["estimated_cost"].(float64); ok {
			quotes = append(quotes, fmtMoney(q))
		}
	}
	if len(names) == 0 || len(quotes) == 0 {
		t.Fatal("the pre-#1057 recording carries no supplier_name or estimated_cost, so this " +
			"check could not see the defect it names")
	}
	for _, w := range jdeDrawableWidths() {
		pane := transparencyPane(t, body, "Trans. orders", w, 40)
		flat := strings.Join(pane, "\n")
		_, header := transparencyHeader(t, pane, "Item")
		for _, h := range strings.Fields(header) {
			if h == "Est" || h == "Supplier" || strings.HasPrefix(h, "Supp") {
				t.Fatalf("width %d: the order ledger heads a column %q, an order-scoped fact "+
					"a reorder request does not record:\n%s", w, h, flat)
			}
		}
		for _, n := range names {
			// The first four runes, because a column that abbreviates draws a
			// prefix and an ellipsis, and "McMa…" is the same attribution.
			if prefix := string([]rune(n)[:4]); strings.Contains(flat, prefix) {
				t.Fatalf("width %d: the item's supplier %q (drawn as %q…) is on the order "+
					"ledger:\n%s", w, n, prefix, flat)
			}
		}
		for _, q := range quotes {
			if strings.Contains(flat, q) {
				t.Fatalf("width %d: the live re-quote %s is on the order ledger, where it reads "+
					"as this order's estimate:\n%s", w, q, flat)
			}
		}
	}
}

// RULE 3, live on the one money column the ledger keeps. Three facts reach it:
// a cost was recorded (a figure, a donation's $0.00 among them), no cost was
// recorded (`actual_cost: null`), and the server declined to tell THIS reader
// (the key omitted, `vendor_data_withheld: true` on the row). The pre-change
// screen drew the last two as the same "—", so a withheld figure read as one
// nobody recorded. Checked on the pane, per row, by reading the Actual column
// wherever its header puts it.
func TestTransOrders_ActualSaysRecordedNoneOrWithheldApart(t *testing.T) {
	type want struct{ lead, cell string }
	signedIn := transparencyWire(t, "transparency_signed_in.json")
	var wants []want
	sawZero, sawNull := false, false
	for _, o := range rawOrders(t, signedIn) {
		lead := string([]rune(o["item_name"].(string))[:4])
		switch v := o["actual_cost"].(type) {
		case nil:
			wants = append(wants, want{lead, "—"})
			sawNull = true
		case float64:
			wants = append(wants, want{lead, fmtMoney(v)})
			sawZero = sawZero || v == 0
		}
	}
	if !sawZero || !sawNull {
		t.Fatalf("the recording must carry a recorded $0.00 and a null actual_cost (zero=%v "+
			"null=%v), or this cannot tell the three apart", sawZero, sawNull)
	}
	anonymous := transparencyWire(t, "transparency_anonymous.json")
	for _, o := range rawOrders(t, anonymous) {
		if _, present := o["actual_cost"]; present || o["vendor_data_withheld"] != true {
			t.Fatal("the anonymous recording is expected to OMIT actual_cost and mark the row " +
				"withheld; without that this check proves nothing about withholding")
		}
	}
	for _, w := range []int{80, 100, 120} {
		pane := transparencyPane(t, signedIn, "Trans. orders", w, 40)
		at, header := transparencyHeader(t, pane, "Item")
		for _, x := range wants {
			if got := transparencyCell(t, header, transparencyRow(t, pane, at, x.lead), "Actual", alignRight); got != x.cell {
				t.Errorf("width %d: %s… Actual = %q, want %q:\n%s", w, x.lead, got, x.cell,
					strings.Join(pane, "\n"))
			}
		}
		pane = transparencyPane(t, anonymous, "Trans. orders", w, 40)
		at, header = transparencyHeader(t, pane, "Item")
		for _, x := range wants {
			if got := transparencyCell(t, header, transparencyRow(t, pane, at, x.lead), "Actual", alignRight); got != "withheld" {
				t.Errorf("width %d: %s… Actual = %q on a row the server WITHHELD — "+
					"\"—\" there says no cost was recorded:\n%s", w, x.lead, got,
					strings.Join(pane, "\n"))
			}
		}
	}
}

// 80 COLUMNS HOLD, AND THE ROOM GOES TO WHAT REMAINS. The pre-change table spent
// the 80-column pane on an Est column that could only draw "—" and dropped
// ACTUAL — the money paid — off the right edge to make room for it. With the
// withdrawn columns gone every column the tab declares is on the pane at 80,
// and a wider terminal gives its room to the item name rather than to padding.
func TestTransOrders_EveryColumnHoldsAt80AndTheItemTakesTheRoomWhenWider(t *testing.T) {
	body := transparencyWire(t, "transparency_signed_in.json")
	cols := NewReorderAnalyticsReportScreen(Deps{}).tabs[transparencyTab(t, "Trans. orders")].columns
	longest := ""
	for _, o := range rawOrders(t, body) {
		if n := o["item_name"].(string); len(n) > len(longest) {
			longest = n
		}
	}
	pane := transparencyPane(t, body, "Trans. orders", 80, 40)
	flat := strings.Join(strings.Fields(strings.Join(pane, " ")), " ")
	_, header := transparencyHeader(t, pane, cols[0].header)
	for _, c := range cols {
		// A FACT's header never gives, so it is asked whole; an identifier's
		// abbreviates by design, so it is asked for the prefix the name floor
		// always keeps.
		want := c.header
		if c.align == alignLeft && len([]rune(want)) > reportNameFloor-1 {
			want = string([]rune(want)[:reportNameFloor-1])
		}
		if !strings.Contains(header, want) {
			t.Errorf("80 columns: %q is not on the header row %q", c.header, header)
		}
	}
	if strings.Contains(flat, reportDropNoteLead) {
		t.Errorf("80 columns: the ledger drops a column it declares:\n%s", strings.Join(pane, "\n"))
	}
	if strings.Contains(flat, longest) {
		t.Fatalf("80 columns drew %q whole, so the wide half of this check could not tell "+
			"a table that uses the room from one that does not", longest)
	}
	wide := strings.Join(transparencyPane(t, body, "Trans. orders", 120, 40), "\n")
	if !strings.Contains(wide, longest) {
		t.Errorf("120 columns: the item name %q is still abbreviated with room to spare:\n%s",
			longest, wide)
	}
}

// The PO ledger KEEPS its Supplier and totals — a purchase order has a supplier
// FK and records its own totals, so they are the order's — and is where the
// withholding reaches three columns at once. Signed in they are the figures;
// withheld they must say so rather than read as "none recorded".
func TestTransPOs_AWithheldVendorBlockIsNotReadAsNoneRecorded(t *testing.T) {
	cases := []struct {
		file                  string
		supplier, est, actual string
	}{
		{"transparency_signed_in.json", "McMaster-Carr Supply Company", "$248.74", "—"},
		{"transparency_anonymous.json", "withheld", "withheld", "withheld"},
	}
	for _, c := range cases {
		body := transparencyWire(t, c.file)
		var raw struct {
			POs []map[string]any `json:"purchase_orders"`
		}
		if err := json.Unmarshal(body, &raw); err != nil || len(raw.POs) == 0 {
			t.Fatalf("%s: no purchase_orders to check (%v)", c.file, err)
		}
		pane := transparencyPane(t, body, "Trans. POs", 160, 40)
		at, header := transparencyHeader(t, pane, "PO")
		row := transparencyRow(t, pane, at, raw.POs[0]["po_number"].(string))
		if got := transparencyCell(t, header, row, "Supplier", alignLeft); got != c.supplier {
			t.Errorf("%s: Supplier = %q, want %q:\n%s", c.file, got, c.supplier, strings.Join(pane, "\n"))
		}
		if got := transparencyCell(t, header, row, "Est total", alignRight); got != c.est {
			t.Errorf("%s: Est total = %q, want %q:\n%s", c.file, got, c.est, strings.Join(pane, "\n"))
		}
		if got := transparencyCell(t, header, row, "Actual total", alignRight); got != c.actual {
			t.Errorf("%s: Actual total = %q, want %q:\n%s", c.file, got, c.actual, strings.Join(pane, "\n"))
		}
	}
}

// transparencySurfaces is every tab that presents the transparency feed, each
// with the check that holds it. TestTransparency_TheDerivedSetIsTheSetSwept
// derives the membership rather than trusting this list.
var transparencySurfaces = map[string]string{
	"Transparency":  "TestReorderTransparencySummary_Loader",
	"Trans. orders": "TestTransOrders_*",
	"Trans. POs":    "TestTransPOs_AWithheldVendorBlockIsNotReadAsNoneRecorded",
}

// TestTransparency_TheDerivedSetIsTheSetSwept is what the derived set at the
// top of this file is claimed on. Every tab of every report screen in
// reportScreenFixtures runs its loader against a server that records the route,
// and the tabs that ask for the transparency feed must be exactly
// transparencySurfaces.
func TestTransparency_TheDerivedSetIsTheSetSwept(t *testing.T) {
	const route = "/api/reorders/analytics/transparency/"
	found := map[string]bool{}
	for name, build := range reportScreenFixtures {
		for _, tab := range build().tabs {
			hit := false
			srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
				hit = hit || r.URL.Path == route
				rw.Header().Set("Content-Type", "application/json")
				_, _ = rw.Write([]byte(`{}`))
			}))
			func() {
				// A loader for another backend (ForgeKey) dereferences a client
				// this fixture does not build; it cannot be asking OMS for the
				// transparency feed, which is all this is asking.
				defer func() { _ = recover() }()
				_, _ = tab.loader(context.Background(), Deps{OMS: omsapi.New(srv.URL), Ctx: context.Background()})
			}()
			srv.Close()
			if hit {
				found[name+"/"+tab.label] = true
				if _, swept := transparencySurfaces[tab.label]; !swept || name != "NewReorderAnalyticsReportScreen" {
					t.Errorf("%s tab %q presents the transparency feed and nothing here sweeps it", name, tab.label)
				}
			}
		}
	}
	for label := range transparencySurfaces {
		if !found["NewReorderAnalyticsReportScreen/"+label] {
			t.Errorf("transparencySurfaces has %q, which no longer asks for the transparency feed", label)
		}
	}
}
