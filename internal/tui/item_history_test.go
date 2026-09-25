package tui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/uid0/scantty/internal/omsapi"
)

// item_history_test.go — ItemHistoryScreen driven off the RECORDED OMS bodies
// (internal/omsapi/testdata/item_*.json, provenance in that directory's README),
// through the real client and a real Root, from the item sheet's own key. A
// fixture built from ScanTTY's structs cannot disagree with them, which is why
// none of these tests writes a history by hand; the bar sweeps
// (prose_bar_item_history_test.go) are where hand-built lengths belong.

// itemHistoryServer answers the two readings with recorded bodies, or with a
// gateway failure where the file name is empty.
func itemHistoryServer(t *testing.T, stockFile, usageFile string) *httptest.Server {
	t.Helper()
	read := func(name string) []byte {
		if name == "" {
			return nil
		}
		b, err := os.ReadFile(filepath.Join("..", "omsapi", "testdata", name))
		if err != nil {
			t.Fatalf("recorded response %s: %v", name, err)
		}
		return b
	}
	stock, usage := read(stockFile), read(usageFile)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body []byte
		switch {
		case strings.HasSuffix(r.URL.Path, "/stock_history/"):
			body = stock
		case r.URL.Path == "/api/inventory/usage-logs/":
			body = usage
		default:
			http.NotFound(w, r)
			return
		}
		if body == nil {
			w.Header().Set("Content-Type", "text/html")
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte(proseLoadGatewayPage))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// itemHistoryFromSheet opens the history the way an operator does: an item sheet
// with its item loaded, `h` pressed through Root, and every command pumped.
func itemHistoryFromSheet(t *testing.T, stockFile, usageFile string) (Root, *ItemHistoryScreen) {
	t.Helper()
	srv := itemHistoryServer(t, stockFile, usageFile)
	deps := Deps{OMS: omsapi.New(srv.URL), Ctx: context.Background()}
	sheet := NewInventoryDetailScreen(deps, "4a2c5325-66fa-447d-8541-33627c903af8")
	sheet.Update(inventoryDetailLoadedMsg{item: &omsapi.Item{
		ID: "4a2c5325-66fa-447d-8541-33627c903af8", Name: "Blue nitrile gloves (M)", BaseUnit: "glove", Stock: 360,
	}})
	r := newTestRoot(sheet)
	r.deps = deps
	next, _ := r.Update(tea.WindowSizeMsg{Width: 100, Height: 36})
	r = next.(Root)
	if !strings.Contains(stripANSI(r.View()), "h history") {
		t.Fatalf("the item sheet does not name `h history`:\n%s", stripANSI(r.View()))
	}
	r = key(t, r, runeKey('h'))
	screen, ok := r.screen.(*ItemHistoryScreen)
	if !ok {
		t.Fatalf("`h` on the item sheet left the screen as %T, want the item history", r.screen)
	}
	return r, screen
}

func itemHistoryPane(r Root) string { return stripANSI(r.View()) }

func TestItemHistory_AnOlderLoadCannotOverwriteARefresh(t *testing.T) {
	s := NewItemHistoryScreen(Deps{}, &omsapi.Item{ID: "itm-1", Name: "Gloves", BaseUnit: "glove"})
	initialUsage := []omsapi.UsageLog{{ID: 1, QuantityUsed: 1}}
	refreshedUsage := []omsapi.UsageLog{{ID: 2, QuantityUsed: 7}}

	next, _ := s.Update(itemHistoryStockMsg{history: proseBarStockHistory(1), loadID: 1})
	s = next.(*ItemHistoryScreen)
	next, _ = s.Update(runeKey('r'))
	s = next.(*ItemHistoryScreen)
	next, _ = s.Update(itemHistoryStockMsg{history: proseBarStockHistory(2), loadID: 2})
	s = next.(*ItemHistoryScreen)
	next, _ = s.Update(itemHistoryUsageMsg{logs: refreshedUsage, loadID: 2})
	s = next.(*ItemHistoryScreen)
	next, _ = s.Update(itemHistoryUsageMsg{logs: initialUsage, loadID: 1})
	s = next.(*ItemHistoryScreen)

	if s.loading {
		t.Fatal("the refreshed load did not finish")
	}
	if len(s.logs) != 1 || s.logs[0].ID != refreshedUsage[0].ID {
		t.Fatalf("usage logs = %+v, want refreshed response %+v", s.logs, refreshedUsage)
	}
}

// TestItemHistory_TheStockViewDrawsTheRecordedReadingsNewestFirst: the recorded
// history as a dated table — newest first, each level with its change from the
// reading before it, the count row's level named as the level BEFORE the count,
// the reorder row with no level, and each threshold in its own unit.
func TestItemHistory_TheStockViewDrawsTheRecordedReadingsNewestFirst(t *testing.T) {
	r, s := itemHistoryFromSheet(t, "item_stock_history.json", "item_usage_logs.json")
	if s.loading {
		t.Fatal("the history is still loading after both readings were pumped")
	}
	pane := itemHistoryPane(r)
	want := []string{
		"2026-09-14            reorder requested",
		"2026-09-14  393  -17  count (before)",
		"2026-09-07  410  -45  weekly snapshot",
		"2026-08-31  455  -15  weekly snapshot",
		"2026-08-24  470  -50  weekly snapshot",
		"2026-08-17  520       weekly snapshot",
	}
	at := -1
	for _, w := range want {
		i := strings.Index(pane, w)
		if i < 0 {
			t.Fatalf("the stock view does not draw %q:\n%s", w, pane)
		}
		if i < at {
			t.Fatalf("%q is drawn above the row before it; the table is newest first:\n%s", w, pane)
		}
		at = i
	}
	for _, w := range []string{
		"Now 360 gloves · reorder point 100 gloves · desired 400 gloves",
		"A count row shows the level on record before the count.",
		"▸ Stock history",
		"[/] ←→ usage logs",
	} {
		if !strings.Contains(pane, w) {
			t.Errorf("the stock view does not draw %q:\n%s", w, pane)
		}
	}
}

// TestItemHistory_TheUsageViewSaysWhoWhenAndHowMany: `]` reaches the usage logs,
// each with its time, its quantity in the base unit, who recorded it — or that
// nobody did — and every line of its note.
func TestItemHistory_TheUsageViewSaysWhoWhenAndHowMany(t *testing.T) {
	r, s := itemHistoryFromSheet(t, "item_stock_history.json", "item_usage_logs.json")
	r = key(t, r, runeKey(']'))
	if s.view != itemHistoryUsage {
		t.Fatal("`]` did not switch to the usage logs")
	}
	pane := itemHistoryPane(r)
	stamp := func(iso string) string {
		ts, err := time.Parse(time.RFC3339Nano, iso)
		if err != nil {
			t.Fatal(err)
		}
		return ts.Local().Format("2006-01-02 15:04")
	}
	for _, w := range []string{
		"3 usage logs, newest first · quantities in gloves, as stored",
		stamp("2026-09-14T04:54:38.141277Z") + "  20 gloves  by user #1",
		"Paint booth",
		stamp("2026-09-14T04:54:38.089406Z") + "  12 gloves  by user #2",
		"Laser cutter orientation, Saturday class",
		"Two boxes opened at the bench",
		stamp("2026-09-14T04:52:32.700079Z") + "  5 gloves  no recorder",
		"▸ Usage logs",
		"[/] ←→ stock history",
	} {
		if !strings.Contains(pane, w) {
			t.Errorf("the usage view does not draw %q:\n%s", w, pane)
		}
	}
	r = key(t, r, runeKey('['))
	if s.view != itemHistoryStock || !strings.Contains(itemHistoryPane(r), "weekly snapshot") {
		t.Errorf("`[` did not come back to the stock history:\n%s", itemHistoryPane(r))
	}
}

// TestItemHistory_NoHistorySaysSoPlainly: an item with none of either reading is
// told apart from a failure (standing rule 3), on both views.
func TestItemHistory_NoHistorySaysSoPlainly(t *testing.T) {
	r, _ := itemHistoryFromSheet(t, "item_stock_history_empty.json", "item_usage_logs_empty.json")
	if pane := itemHistoryPane(r); !strings.Contains(pane, "No stock history for this item yet") ||
		strings.Contains(pane, "Error") {
		t.Errorf("the empty stock view does not say plainly there is no history:\n%s", pane)
	}
	r = key(t, r, runeKey(']'))
	if pane := itemHistoryPane(r); !strings.Contains(pane, "No usage logged for this item.") ||
		strings.Contains(pane, "Error") {
		t.Errorf("the empty usage view does not say plainly there is none:\n%s", pane)
	}
}

// TestItemHistory_OneFailedReadingLeavesTheOtherReachable: a usage list that
// failed is drawn as a failure on its own view, with the stock history that
// loaded one key away — not a whole-screen error hiding a reading that arrived.
func TestItemHistory_OneFailedReadingLeavesTheOtherReachable(t *testing.T) {
	r, s := itemHistoryFromSheet(t, "item_stock_history.json", "")
	if s.loadErr != "" {
		t.Fatalf("one failed reading failed the whole screen: %q", s.loadErr)
	}
	if !strings.Contains(itemHistoryPane(r), "weekly snapshot") {
		t.Fatalf("the stock history that loaded is not drawn:\n%s", itemHistoryPane(r))
	}
	r = key(t, r, runeKey(']'))
	pane := itemHistoryPane(r)
	for _, w := range []string{"Usage logs: ", "502", "r retry", "[/] ←→ stock history"} {
		if !strings.Contains(pane, w) {
			t.Errorf("the failed usage view does not draw %q:\n%s", w, pane)
		}
	}
}

// TestItemHistory_TheSheetNamesTheKeyExactlyWhereItWorks: `h` is named on the item
// sheet where it opens the history, and neither named nor acting while the item
// has not loaded — the bar-and-dispatch agreement the sheet's other keys hold.
func TestItemHistory_TheSheetNamesTheKeyExactlyWhereItWorks(t *testing.T) {
	loading := NewInventoryDetailScreen(Deps{}, "itm-1")
	if strings.Contains(loading.View(), "h history") {
		t.Errorf("the sheet names `h history` before the item has loaded:\n%s", loading.View())
	}
	if _, cmd := loading.Update(runeKey('h')); cmd != nil {
		t.Error("`h` acted on a sheet with no item loaded")
	}

	for _, serialized := range []bool{false, true} {
		s := NewInventoryDetailScreen(Deps{}, "itm-1")
		s.Update(inventoryDetailLoadedMsg{item: &omsapi.Item{ID: "itm-1", Name: "Relay", IsSerialized: serialized}})
		if !strings.Contains(s.View(), "h history") {
			t.Errorf("serialized=%v: the loaded sheet does not name `h history`:\n%s", serialized, s.View())
		}
		_, cmd := s.Update(runeKey('h'))
		if cmd == nil {
			t.Fatalf("serialized=%v: `h` did nothing on a loaded sheet", serialized)
		}
		msg, ok := cmd().(SwitchScreenMsg)
		if !ok {
			t.Fatalf("serialized=%v: `h` did not switch screens", serialized)
		}
		if _, ok := msg.Screen.(*ItemHistoryScreen); !ok {
			t.Errorf("serialized=%v: `h` opened %T, want the item history", serialized, msg.Screen)
		}
	}
}

// TestItemHistory_AChangeIsAgainstTheReadingBeforeItThatHasALevel: a reorder row
// sitting between two readings neither carries a change nor breaks the one after
// it, and the oldest reading has no change at all.
func TestItemHistory_AChangeIsAgainstTheReadingBeforeItThatHasALevel(t *testing.T) {
	day := func(s string) omsapi.DateOnly {
		d, _ := time.Parse("2006-01-02", s)
		return omsapi.DateOnly{Time: d}
	}
	rows := itemHistoryStockRows(&omsapi.StockHistory{
		Series:        []omsapi.StockHistoryPoint{{Date: day("2026-01-05"), Count: 50}, {Date: day("2026-01-19"), Count: 80}},
		ReorderEvents: []omsapi.StockHistoryEvent{{Date: day("2026-01-12")}},
	})
	if len(rows) != 3 {
		t.Fatalf("rows = %d, want 3", len(rows))
	}
	if r := rows[0]; r.date != "2026-01-19" || !r.hasChange || r.change != 30 {
		t.Errorf("newest row = %+v, want 2026-01-19 at +30 against the snapshot before the reorder", r)
	}
	if r := rows[1]; r.kind != itemStockReorder || r.hasLevel || r.hasChange {
		t.Errorf("middle row = %+v, want a reorder with no level and no change", r)
	}
	if r := rows[2]; r.hasChange {
		t.Errorf("oldest row = %+v, want no change — there is no reading before it", r)
	}
}

func TestItemHistory_SameDayLevelsDoNotInventAChronology(t *testing.T) {
	day := func(s string) omsapi.DateOnly {
		d, _ := time.Parse("2006-01-02", s)
		return omsapi.DateOnly{Time: d}
	}
	rows := itemHistoryStockRows(&omsapi.StockHistory{
		Series: []omsapi.StockHistoryPoint{
			{Date: day("2026-01-05"), Count: 100},
			{Date: day("2026-01-12"), Count: 50},
			{Date: day("2026-01-19"), Count: 60},
		},
		CycleCounts: []omsapi.StockHistoryPoint{{Date: day("2026-01-12"), Count: 100}},
	})
	if len(rows) != 4 {
		t.Fatalf("rows = %d, want 4", len(rows))
	}
	if r := rows[0]; r.date != "2026-01-19" || r.hasChange {
		t.Errorf("level after ambiguous date = %+v, want no change", r)
	}
	for i := 1; i <= 2; i++ {
		if r := rows[i]; r.date != "2026-01-12" || r.hasChange {
			t.Errorf("same-day row %d = %+v, want no change", i, r)
		}
	}
	if r := rows[3]; r.date != "2026-01-05" || r.hasChange {
		t.Errorf("oldest row = %+v, want no change", r)
	}
}
