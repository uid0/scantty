package tui

// list_paging_test.go — the four workspace lists walk past OMS's first page.
//
// THE DEFECT. Inventory, Assets, Maintenance and Purchasing each asked OMS for
// ONE page and dropped `next`, and OMS pages every list at PAGE_SIZE 50
// (config/settings.py). So item 51 of a shop's catalogue could not be reached by
// browsing — only by name, through the ctrl+k palette — and the Inventory list
// had no search and no filter at all, where the web list pages, searches and
// filters by stock status and retired visibility (InventoryListPage.tsx).
//
// EVERY BODY THE FAKE SERVES IS RECORDED (internal/omsapi/testdata/README.md),
// off one seeded OMS whose lists run to a second page. A fixture written from
// listRow cannot tell this suite whether OMS names a next page the way the layer
// reads one, and a page of five hand-written rows cannot put row 51 anywhere.
//
// THE PAGED SET IS DERIVED, from the surfaces the bar sweeps already derive
// (listBarSurfaces) filtered on the spec's pager, and it is asserted against the
// four workspaces the parity report names — so a fifth paged list joins the
// sweeps here by existing, and one of the four falling back to a single page
// fails rather than leaving the others to vouch for it.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/uid0/scantty/internal/omsapi"
)

// listPagingRecorded reads one recorded OMS body.
func listPagingRecorded(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "omsapi", "testdata", name))
	if err != nil {
		t.Fatalf("recorded fixture %s: %v", name, err)
	}
	return b
}

// listPagingEnvelope is what a test needs to know about a recorded page without
// decoding it the way the code under test does: the server's count, whether it
// named a next page, and each row's id as the listRow will carry it.
type listPagingEnvelope struct {
	count int
	more  bool
	ids   []string
}

func listPagingDecode(t *testing.T, body []byte) listPagingEnvelope {
	t.Helper()
	var raw struct {
		Count int              `json:"count"`
		Next  *string          `json:"next"`
		Rows  []map[string]any `json:"results"`
	}
	dec := json.NewDecoder(strings.NewReader(string(body)))
	dec.UseNumber()
	if err := dec.Decode(&raw); err != nil {
		t.Fatalf("decode recorded page: %v", err)
	}
	env := listPagingEnvelope{count: raw.Count, more: raw.Next != nil && *raw.Next != ""}
	for _, r := range raw.Rows {
		env.ids = append(env.ids, anyToString(r["id"]))
	}
	return env
}

func anyToString(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case json.Number:
		return x.String()
	}
	return ""
}

// listPagedWorkspace is one paged workspace list with the endpoint it pages and
// the two recorded pages of it.
type listPagedWorkspace struct {
	name        string
	ws          Workspace
	path        string
	page1       string
	page2       string
	defaultKeys url.Values // params every request of this list carries
}

// listPagedWorkspaces is the parity report's four lists. It is the EXPECTATION
// the derived set is checked against, not the set the sweeps walk.
func listPagedWorkspaces() []listPagedWorkspace {
	return []listPagedWorkspace{
		{"inventory", WSInventory, "/api/inventory/items/",
			"inventory_items_page1.json", "inventory_items_page2.json",
			url.Values{"with_metrics": {"1"}, "include_retired": {"true"}}},
		{"purchasing", WSPurchasing, "/api/reorders/purchase-orders/",
			"purchase_orders_page1.json", "purchase_orders_page2.json", nil},
		{"assets", WSAssets, "/api/inventory/assets/",
			"assets_page1.json", "assets_page2.json", nil},
		{"maintenance", WSMaintenance, "/api/inventory/work-orders/",
			"work_orders_page1.json", "work_orders_page2.json", nil},
	}
}

// listPagedSurfaces is every list surface whose spec pages, derived.
func listPagedSurfaces() []listBarSurface {
	var out []listBarSurface
	for _, s := range listBarSurfaces() {
		if s.build().spec.pager != nil {
			out = append(out, s)
		}
	}
	return out
}

// TestListPaging_TheFourWorkspaceListsArePaged is the completeness half: the
// derivation reaches exactly the lists the parity gap names.
func TestListPaging_TheFourWorkspaceListsArePaged(t *testing.T) {
	var got, want []string
	for _, s := range listPagedSurfaces() {
		got = append(got, s.name)
	}
	for _, w := range listPagedWorkspaces() {
		want = append(want, w.name)
	}
	sort.Strings(got)
	sort.Strings(want)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("paged list surfaces = %v, want %v", got, want)
	}
}

// listPagingServer serves recorded bodies by path and query, and records every
// query it was asked. `answer` returns the status and the fixture file.
type listPagingServer struct {
	*httptest.Server
	mu      sync.Mutex
	queries []url.Values
}

func newListPagingServer(t *testing.T, answer func(r *http.Request) (int, string)) *listPagingServer {
	t.Helper()
	ls := &listPagingServer{}
	ls.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ls.mu.Lock()
		ls.queries = append(ls.queries, r.URL.Query())
		ls.mu.Unlock()
		status, file := answer(r)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write(listPagingRecorded(t, file))
	}))
	t.Cleanup(ls.Close)
	return ls
}

func (ls *listPagingServer) last() url.Values {
	ls.mu.Lock()
	defer ls.mu.Unlock()
	if len(ls.queries) == 0 {
		return nil
	}
	return ls.queries[len(ls.queries)-1]
}

// listPagingUpdate feeds one message to a list and hands back the list.
func listPagingUpdate(t *testing.T, s *ListScreen, msg tea.Msg) (*ListScreen, tea.Cmd) {
	t.Helper()
	next, cmd := s.Update(msg)
	out, ok := next.(*ListScreen)
	if !ok {
		t.Fatalf("Update returned %T, want *ListScreen", next)
	}
	return out, cmd
}

// listPagingPane is the clipped pane at 80x24, the way Root draws it.
func listPagingPane(s *ListScreen) string {
	return clampToBox(s.View(), screenBodyCells(80), screenBodyRows(24))
}

// TestListPaging_RowFiftyOneIsReachableOnEveryPagedWorkspace is the reported
// gap, closed on each of the four lists: the list opens on OMS's first page and
// says how much of the server's list that is; going to the bottom fetches the
// second page, with the working line on the pane over rows that stay drawn and
// a footer that stays whole; and a row that only the second page carries can be
// put under the cursor and opened.
func TestListPaging_RowFiftyOneIsReachableOnEveryPagedWorkspace(t *testing.T) {
	for _, pw := range listPagedWorkspaces() {
		t.Run(pw.name, func(t *testing.T) {
			first := listPagingDecode(t, listPagingRecorded(t, pw.page1))
			second := listPagingDecode(t, listPagingRecorded(t, pw.page2))
			if !first.more || second.more || len(first.ids) != 50 || first.count <= 50 {
				t.Fatalf("recorded pages do not reach a second page: page1 count=%d rows=%d more=%v, page2 more=%v",
					first.count, len(first.ids), first.more, second.more)
			}
			srv := newListPagingServer(t, func(r *http.Request) (int, string) {
				if r.URL.Path != pw.path {
					t.Errorf("request to %s, want %s", r.URL.Path, pw.path)
				}
				if r.URL.Query().Get("page") == "2" {
					return http.StatusOK, pw.page2
				}
				return http.StatusOK, pw.page1
			})
			deps := Deps{OMS: omsapi.New(srv.URL), Ctx: context.Background()}
			s := newScreenFor(pw.ws, deps).(*ListScreen)
			s, _ = listPagingUpdate(t, s, tea.WindowSizeMsg{Width: 80, Height: 24})
			s, _ = listPagingUpdate(t, s, s.Init()())

			q := srv.last()
			if _, ok := q["page"]; ok {
				t.Errorf("the first request carries ?page=%s; page 1 is the request the list always sent", q.Get("page"))
			}
			for k := range pw.defaultKeys {
				if q.Get(k) != pw.defaultKeys.Get(k) {
					t.Errorf("first request %s=%q, want %q", k, q.Get(k), pw.defaultKeys.Get(k))
				}
			}
			if len(s.rows) != 50 {
				t.Fatalf("after the first page the list holds %d rows, want 50", len(s.rows))
			}
			wantOf := "50 of " + strconv.Itoa(first.count) + " rows"
			if pane := listPagingPane(s); !strings.Contains(pane, wantOf) {
				t.Errorf("the pane does not say how much of the list is loaded (%q):\n%s", wantOf, pane)
			}

			// To the bottom of what is loaded: that is what asks for page 2.
			s, cmd := listPagingUpdate(t, s, listRuneKey("G"))
			if cmd == nil {
				t.Fatal("G onto the last loaded row fetched nothing, with the server naming a next page")
			}
			arrived := s.rows[s.cursor].ID
			pane := listPagingPane(s)
			if !strings.Contains(pane, "Loading rows 51+ of "+strconv.Itoa(first.count)) {
				t.Errorf("the working line is not on the pane while page 2 is out:\n%s", pane)
			}
			if !strings.Contains(pane, "r refresh") || !strings.Contains(pane, "▸ ") {
				t.Errorf("the rows or the footer went while page 2 was out:\n%s", pane)
			}
			// A second press while it is out asks for nothing more.
			if _, again := listPagingUpdate(t, s, listRuneKey("j")); again != nil {
				t.Error("a movement key fetched page 2 again while the first request was still out")
			}

			s, _ = listPagingUpdate(t, s, cmd())
			if got := srv.last().Get("page"); got != "2" {
				t.Errorf("the next-page request sent page=%q, want 2", got)
			}
			for k := range pw.defaultKeys {
				if got := srv.last().Get(k); got != pw.defaultKeys.Get(k) {
					t.Errorf("page 2 request %s=%q, want %q", k, got, pw.defaultKeys.Get(k))
				}
			}
			if len(s.rows) != first.count {
				t.Fatalf("after page 2 the list holds %d rows, want the server's %d", len(s.rows), first.count)
			}
			if s.rows[s.cursor].ID != arrived {
				t.Errorf("the page arriving moved the cursor off the row it was on (%s -> %s)",
					arrived, s.rows[s.cursor].ID)
			}
			pane = listPagingPane(s)
			if strings.Contains(pane, "Loading rows") || strings.Contains(pane, " of "+strconv.Itoa(first.count)+" rows") {
				t.Errorf("the pane still reads as partly loaded after the last page:\n%s", pane)
			}

			// Put a row ONLY page 2 carries under the cursor, by keys, and open it.
			target := second.ids[len(second.ids)-1]
			idx := -1
			for i, r := range s.rows {
				if r.ID == target {
					idx = i
				}
			}
			if idx < 0 {
				t.Fatalf("row %s from page 2 is not in the list", target)
			}
			s, _ = listPagingUpdate(t, s, listRuneKey("g"))
			for i := 0; i < idx; i++ {
				var more tea.Cmd
				s, more = listPagingUpdate(t, s, listRuneKey("j"))
				if more != nil {
					t.Fatal("a movement key fetched a page after the server named none")
				}
			}
			if s.rows[s.cursor].ID != target {
				t.Fatalf("the cursor reached %s, want page 2's row %s", s.rows[s.cursor].ID, target)
			}
			_, open := listPagingUpdate(t, s, listRuneKey("enter"))
			if open == nil {
				t.Fatalf("enter on page 2's row %s opened nothing", target)
			}
			if _, ok := open().(SwitchScreenMsg); !ok {
				t.Fatalf("enter on page 2's row produced %T, want SwitchScreenMsg", open())
			}
		})
	}
}

// TestListPaging_AFailedPageKeepsTheRowsAndSaysWhy drives a next page the
// server REFUSES — the recorded out-of-range reply — and then answers. The
// failure is the load state the header owns: the rows already loaded stay, the
// footer stays whole, the header leads with what failed and the server's reason,
// and the next press on the last row asks again. A success clears the failure,
// because a "✗" left standing over a list that did load is the stale-error
// defect the working/failed rule was written against.
func TestListPaging_AFailedPageKeepsTheRowsAndSaysWhy(t *testing.T) {
	first := listPagingDecode(t, listPagingRecorded(t, "inventory_items_page1.json"))
	refuse := true
	srv := newListPagingServer(t, func(r *http.Request) (int, string) {
		if r.URL.Query().Get("page") == "2" {
			if refuse {
				return http.StatusNotFound, "inventory_items_page_out_of_range.json"
			}
			return http.StatusOK, "inventory_items_page2.json"
		}
		return http.StatusOK, "inventory_items_page1.json"
	})
	deps := Deps{OMS: omsapi.New(srv.URL), Ctx: context.Background()}
	s := newScreenFor(WSInventory, deps).(*ListScreen)
	s, _ = listPagingUpdate(t, s, tea.WindowSizeMsg{Width: 80, Height: 24})
	s, _ = listPagingUpdate(t, s, s.Init()())

	s, cmd := listPagingUpdate(t, s, listRuneKey("G"))
	if cmd == nil {
		t.Fatal("G fetched nothing")
	}
	s, _ = listPagingUpdate(t, s, cmd())
	pane := listPagingPane(s)
	lead := "✗ rows 51+ of " + strconv.Itoa(first.count) + " failed"
	if !strings.Contains(pane, lead) {
		t.Errorf("a refused page is not stated on the pane (want %q):\n%s", lead, pane)
	}
	if len(s.rows) != 50 || !strings.Contains(pane, "r refresh") {
		t.Errorf("a refused page cost the rows (%d) or the footer:\n%s", len(s.rows), pane)
	}
	if s.loadErr != "" {
		t.Errorf("a refused NEXT page replaced the loaded list with an error pane: %q", s.loadErr)
	}

	refuse = false
	s, retry := listPagingUpdate(t, s, listRuneKey("j"))
	if retry == nil {
		t.Fatal("a movement key on the last row after a failed page did not ask again")
	}
	if pane := listPagingPane(s); strings.Contains(pane, "✗") || !strings.Contains(pane, "Loading rows 51+") {
		t.Errorf("the retry did not trade the failure for the working line:\n%s", pane)
	}
	s, _ = listPagingUpdate(t, s, retry())
	if pane := listPagingPane(s); strings.Contains(pane, "✗") || len(s.rows) != first.count {
		t.Errorf("a page that loaded left the failure standing or the rows short (%d):\n%s", len(s.rows), pane)
	}
}

// TestListPaging_APageForAViewTheOperatorLeftIsDropped: a next page still out
// when the row set is REPLACED — a refresh, a filter cycle, a search keystroke —
// must not be appended to the new one. Page 2 of every inventory item landing on
// the low-stock view would list in-stock items under "Filter: low stock".
func TestListPaging_APageForAViewTheOperatorLeftIsDropped(t *testing.T) {
	srv := newListPagingServer(t, func(r *http.Request) (int, string) {
		q := r.URL.Query()
		switch {
		case q.Get("low_stock") == "true":
			return http.StatusOK, "inventory_items_low_stock.json"
		case q.Get("search") != "":
			return http.StatusOK, "inventory_items_search.json"
		case q.Get("page") == "2":
			return http.StatusOK, "inventory_items_page2.json"
		}
		return http.StatusOK, "inventory_items_page1.json"
	})
	deps := Deps{OMS: omsapi.New(srv.URL), Ctx: context.Background()}
	lowStock := listPagingDecode(t, listPagingRecorded(t, "inventory_items_low_stock.json"))
	search := listPagingDecode(t, listPagingRecorded(t, "inventory_items_search.json"))

	for _, leave := range []struct {
		name  string
		press func(t *testing.T, s *ListScreen) (*ListScreen, tea.Msg)
		rows  int
	}{
		{"filter cycle", func(t *testing.T, s *ListScreen) (*ListScreen, tea.Msg) {
			s, cmd := listPagingUpdate(t, s, listRuneKey("f"))
			return s, cmd()
		}, len(lowStock.ids)},
		{"refresh", func(t *testing.T, s *ListScreen) (*ListScreen, tea.Msg) {
			s, cmd := listPagingUpdate(t, s, listRuneKey("r"))
			return s, cmd()
		}, 50},
		{"search", func(t *testing.T, s *ListScreen) (*ListScreen, tea.Msg) {
			s, _ = listPagingUpdate(t, s, listRuneKey("/"))
			for _, r := range "nitrile" {
				s, _ = listPagingUpdate(t, s, listRuneKey(string(r)))
			}
			return s, s.runSearch()()
		}, len(search.ids)},
	} {
		t.Run(leave.name, func(t *testing.T) {
			s := newScreenFor(WSInventory, deps).(*ListScreen)
			s, _ = listPagingUpdate(t, s, tea.WindowSizeMsg{Width: 80, Height: 24})
			s, _ = listPagingUpdate(t, s, s.Init()())
			s, pageCmd := listPagingUpdate(t, s, listRuneKey("G"))
			if pageCmd == nil {
				t.Fatal("G fetched nothing")
			}
			stale := pageCmd()
			s, fresh := leave.press(t, s)
			s, _ = listPagingUpdate(t, s, fresh)
			s, _ = listPagingUpdate(t, s, stale)
			if len(s.rows) != leave.rows {
				t.Fatalf("after leaving the view, a stale page 2 landed: %d rows, want the new view's %d",
					len(s.rows), leave.rows)
			}
			if s.pageLoading {
				t.Error("the header still says a page is loading for a view the operator left")
			}
		})
	}
}

// TestListPaging_AnOverlappingPageListsNoRowTwice: pages are fetched by
// NUMBER, so a row created between two fetches shifts every later page by one
// and its neighbour arrives on both — and OMS's purchase-order list is served
// with NO ORDER BY at all (the voided-order count drops the model's ordering),
// so two of its pages can overlap on any afternoon. The worst case is served
// here, recorded: page 2 answered with page 1's own body. Nothing is listed
// twice, and the cursor stays on the row it was on.
func TestListPaging_AnOverlappingPageListsNoRowTwice(t *testing.T) {
	srv := newListPagingServer(t, func(r *http.Request) (int, string) {
		return http.StatusOK, "purchase_orders_page1.json"
	})
	deps := Deps{OMS: omsapi.New(srv.URL), Ctx: context.Background()}
	s := newScreenFor(WSPurchasing, deps).(*ListScreen)
	s, _ = listPagingUpdate(t, s, tea.WindowSizeMsg{Width: 80, Height: 24})
	s, _ = listPagingUpdate(t, s, s.Init()())
	s, cmd := listPagingUpdate(t, s, listRuneKey("G"))
	if cmd == nil {
		t.Fatal("G fetched nothing")
	}
	on := s.rows[s.cursor].ID
	s, _ = listPagingUpdate(t, s, cmd())
	ids := map[string]int{}
	for _, r := range s.rows {
		ids[r.ID]++
		if ids[r.ID] > 1 {
			t.Errorf("row %s is listed twice", r.ID)
		}
	}
	if len(s.rows) != 50 || s.rows[s.cursor].ID != on {
		t.Errorf("an overlapping page left %d rows and the cursor on %s, want 50 and %s",
			len(s.rows), s.rows[s.cursor].ID, on)
	}
}

// TestListPaging_ASearchAndALoadNeverLandOnEachOther holds the two races a
// searchable paged list opens, both driven with recorded bodies.
//
// A search typed while the list's FIRST load is still out: the load's reply is
// for the whole catalogue and must not replace the matches, and the matches
// must not be drawn under a "Loading…" nothing would ever clear. And a search
// still out when esc restores the whole list: its matches must not land on the
// restored rows under a header that no longer says they are filtered.
func TestListPaging_ASearchAndALoadNeverLandOnEachOther(t *testing.T) {
	srv := newListPagingServer(t, func(r *http.Request) (int, string) {
		if r.URL.Query().Get("search") != "" {
			return http.StatusOK, "inventory_items_search.json"
		}
		return http.StatusOK, "inventory_items_page1.json"
	})
	deps := Deps{OMS: omsapi.New(srv.URL), Ctx: context.Background()}
	matches := len(listPagingDecode(t, listPagingRecorded(t, "inventory_items_search.json")).ids)
	typeQuery := func(s *ListScreen) *ListScreen {
		s, _ = listPagingUpdate(t, s, listRuneKey("/"))
		for _, r := range "nitrile" {
			s, _ = listPagingUpdate(t, s, listRuneKey(string(r)))
		}
		return s
	}

	t.Run("search typed over the first load", func(t *testing.T) {
		s := newScreenFor(WSInventory, deps).(*ListScreen)
		s, _ = listPagingUpdate(t, s, tea.WindowSizeMsg{Width: 80, Height: 24})
		firstLoad := s.Init()()
		s = typeQuery(s)
		s, _ = listPagingUpdate(t, s, s.runSearch()())
		s, _ = listPagingUpdate(t, s, firstLoad)
		if len(s.rows) != matches {
			t.Errorf("the first load landed over the search: %d rows, want the %d matches", len(s.rows), matches)
		}
		if pane := listPagingPane(s); s.loading || strings.Contains(pane, "Loading…") {
			t.Errorf("the matches are drawn under a load nothing will finish:\n%s", pane)
		}
	})

	t.Run("search out when esc restores the list", func(t *testing.T) {
		s := newScreenFor(WSInventory, deps).(*ListScreen)
		s, _ = listPagingUpdate(t, s, tea.WindowSizeMsg{Width: 80, Height: 24})
		s, _ = listPagingUpdate(t, s, s.Init()())
		s = typeQuery(s)
		pending := s.runSearch()
		s, restore := listPagingUpdate(t, s, listRuneKey("esc"))
		if restore == nil {
			t.Fatal("esc after a query restored nothing")
		}
		s, _ = listPagingUpdate(t, s, restore())
		s, _ = listPagingUpdate(t, s, pending())
		if len(s.rows) != 50 {
			t.Errorf("a search answered after esc replaced the restored list: %d rows, want 50", len(s.rows))
		}
	})
}

// TestInventoryList_FiltersAndSearchSendTheWebsParams pins the Inventory list's
// filter cycle and search to the params the web inventory list sends
// (InventoryListPage.tsx buildListParams) — each view against its RECORDED
// answer, so the rows drawn under a label are the rows OMS serves for it.
func TestInventoryList_FiltersAndSearchSendTheWebsParams(t *testing.T) {
	views := []struct {
		label   string
		want    url.Values // params that must be present with these values
		absent  []string
		fixture string
	}{
		{"all", url.Values{"include_retired": {"true"}, "with_metrics": {"1"}}, []string{"low_stock", "search", "page"}, "inventory_items_page1.json"},
		{"low stock", url.Values{"low_stock": {"true"}, "with_metrics": {"1"}}, []string{"search", "page"}, "inventory_items_low_stock.json"},
		{"in stock", url.Values{"low_stock": {"false"}, "with_metrics": {"1"}}, []string{"search", "page"}, "inventory_items_in_stock.json"},
		{"retired hidden", url.Values{"include_retired": {"false"}, "with_metrics": {"1"}}, []string{"low_stock", "search", "page"}, "inventory_items_retired_hidden.json"},
	}
	if len(views) != len(inventoryItemFilters) {
		t.Fatalf("the cycle has %d views and this test pins %d", len(inventoryItemFilters), len(views))
	}
	fixtureFor := func(q url.Values) string {
		switch {
		case q.Get("search") != "":
			return "inventory_items_search.json"
		case q.Get("low_stock") == "true":
			return "inventory_items_low_stock.json"
		case q.Get("low_stock") == "false":
			return "inventory_items_in_stock.json"
		case q.Get("include_retired") == "false":
			return "inventory_items_retired_hidden.json"
		}
		return "inventory_items_page1.json"
	}
	srv := newListPagingServer(t, func(r *http.Request) (int, string) {
		return http.StatusOK, fixtureFor(r.URL.Query())
	})
	deps := Deps{OMS: omsapi.New(srv.URL), Ctx: context.Background()}
	s := newScreenFor(WSInventory, deps).(*ListScreen)
	s, _ = listPagingUpdate(t, s, tea.WindowSizeMsg{Width: 80, Height: 24})
	if !s.HandlesKey("f") || !s.HandlesKey("/") {
		t.Fatal("the Inventory list does not claim f and / over the global hotkeys")
	}
	for i, v := range views {
		if i == 0 {
			s, _ = listPagingUpdate(t, s, s.Init()())
		} else {
			var cmd tea.Cmd
			s, cmd = listPagingUpdate(t, s, listRuneKey("f"))
			s, _ = listPagingUpdate(t, s, cmd())
		}
		q := srv.last()
		for k := range v.want {
			if q.Get(k) != v.want.Get(k) {
				t.Errorf("%s view sent %s=%q, want %q (query %v)", v.label, k, q.Get(k), v.want.Get(k), q)
			}
		}
		for _, k := range v.absent {
			if _, ok := q[k]; ok {
				t.Errorf("%s view sent %s=%q, want it absent", v.label, k, q.Get(k))
			}
		}
		rec := listPagingDecode(t, listPagingRecorded(t, v.fixture))
		if s.activeFilter().label != v.label || len(s.rows) != len(rec.ids) {
			t.Errorf("%s view: label %q, %d rows, want %d from the recorded answer",
				v.label, s.activeFilter().label, len(s.rows), len(rec.ids))
		}
		if pane := listPagingPane(s); !strings.Contains(pane, "Filter: "+v.label) {
			t.Errorf("the pane does not name the %q view:\n%s", v.label, pane)
		}
	}

	// Back to "low stock", then search inside it: the web composes search with
	// its filters, so the terminal does.
	for s.activeFilter().label != "low stock" {
		var cmd tea.Cmd
		s, cmd = listPagingUpdate(t, s, listRuneKey("f"))
		s, _ = listPagingUpdate(t, s, cmd())
	}
	s, _ = listPagingUpdate(t, s, listRuneKey("/"))
	for _, r := range "nitrile" {
		s, _ = listPagingUpdate(t, s, listRuneKey(string(r)))
	}
	s, _ = listPagingUpdate(t, s, s.runSearch()())
	q := srv.last()
	if q.Get("search") != "nitrile" || q.Get("low_stock") != "true" || q.Get("with_metrics") != "1" {
		t.Errorf("a search inside the low-stock view sent %v, want search=nitrile with low_stock=true", q)
	}
	rec := listPagingDecode(t, listPagingRecorded(t, "inventory_items_search.json"))
	if len(s.rows) != len(rec.ids) {
		t.Errorf("search drew %d rows, want the recorded %d", len(s.rows), len(rec.ids))
	}

	// And the page after a search's first carries the query and the view with
	// it, or row 51 of a search would be row 51 of the whole catalogue.
	if q2 := s.pageQuery(2); q2.Get("search") != "nitrile" || q2.Get("low_stock") != "true" || q2.Get("page") != "2" {
		t.Errorf("the next page of a filtered search asks %v", q2)
	}
}

// listPageStates are the paging states a paged list's bar and header must stay
// honest in, over rows the fixture shapes the way the loaders do.
func listPageStates() map[string]func(s *ListScreen) {
	return map[string]func(s *ListScreen){
		"more to load": func(s *ListScreen) {
			s.pagesLoaded, s.total, s.more = 1, 173, true
		},
		"next page loading": func(s *ListScreen) {
			s.pagesLoaded, s.total, s.more, s.pageLoading = 1, 173, true, true
		},
		"next page failed": func(s *ListScreen) {
			s.pagesLoaded, s.total, s.more = 1, 173, true
			s.pageErr = takePageErr(errHugeBody())
		},
	}
}

// errHugeBody is a gateway page's worth of error with NO SPACE IN IT, the shape
// a minified error page arrives in. It is unspaced on purpose: the header is fit
// a token at a time, and a reason with a space early in it can be cut at that
// space whatever bounds the lead — so only an unspaced one can tell a lead bound
// to the pane from one handed to the token fit whole, which would drop the reason
// and "failed:" with it.
func errHugeBody() error {
	return errors.New("<html><body>" + strings.Repeat("<div>bad-gateway</div>", 900))
}

// TestList_APagedListNamesExactlyTheKeysThatWorkInEveryPageState is the footer
// rule pressed over the whole key space in the states paging adds. Paging binds
// no key of its own — reaching the last row is what fetches — so the claim to
// hold is that a named key still acts and an unnamed one still does not, with a
// page outstanding, loading or failed.
func TestList_APagedListNamesExactlyTheKeysThatWorkInEveryPageState(t *testing.T) {
	probes := [][]string{nil, {"G"}, {"pgdown"}}
	for _, surface := range listPagedSurfaces() {
		for state, set := range listPageStates() {
			t.Run(surface.name+"/"+state, func(t *testing.T) {
				build := func() *ListScreen {
					s := surface.build()
					set(s)
					return s
				}
				named := listNamedKeys(t, listWithRows(build, 8).footerHint())
				for _, key := range listKeySpace() {
					var changed, issued bool
					for _, probe := range probes {
						c, i := listKeyEffectAt(build, 8, probe, key)
						changed = changed || c
						issued = issued || i
					}
					switch {
					case named[key] && !changed && !issued:
						t.Errorf("the footer names %q but pressing it does nothing", key)
					case !named[key] && (changed || issued):
						t.Errorf("the footer does not name %q, but pressing it acts", key)
					}
				}
			})
		}
	}
}

// TestList_ThePageStateLeadsAHeaderThatFitsThePane: the header row cannot fold,
// so at every drawable width and height it fits the pane, and where a page is
// loading or failed the state LEADS it — a header that kept "Sort: newest" and
// lost "failed" would be keeping the less important fact. A failure is marked
// where its reason was cut, including an unspaced gateway page the token fit
// could otherwise only drop whole.
func TestList_ThePageStateLeadsAHeaderThatFitsThePane(t *testing.T) {
	widths, heights := jdeDrawableWidths(), jdePaneHeights()
	leads := map[string]string{
		"more to load":      "Sort: ",
		"next page loading": "Loading rows 9+ of 173…",
		"next page failed":  "✗ rows 9+ of 173 failed: <html>",
	}
	drawn := 0
	for _, surface := range listPagedSurfaces() {
		for state, set := range listPageStates() {
			for _, w := range widths {
				for _, h := range heights {
					s := surface.build()
					s.loading = false
					s.rawRows = listFixtureRows(8)
					s.applySort()
					set(s)
					s, _ = listPagingUpdate(t, s, tea.WindowSizeMsg{Width: w, Height: h})
					if !s.paneDrawn() {
						continue
					}
					drawn++
					header := stripANSI(strings.Split(s.View(), "\n")[0])
					if got := lipgloss.Width(header); got > screenBodyCells(w) {
						t.Errorf("%s/%s %dx%d: header is %d cells, pane is %d: %q",
							surface.name, state, w, h, got, screenBodyCells(w), header)
					}
					if !strings.HasPrefix(header, leads[state]) {
						t.Errorf("%s/%s %dx%d: header does not lead with %q: %q",
							surface.name, state, w, h, leads[state], header)
					}
					if state == "next page failed" && !strings.Contains(header, paneCutMark) {
						t.Errorf("%s %dx%d: a cut failure reason is not marked: %q", surface.name, w, h, header)
					}
				}
			}
		}
	}
	if drawn == 0 {
		t.Fatal("no pane was drawn — the sweep measured nothing")
	}
}
