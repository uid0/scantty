package tui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/uid0/scantty/internal/omsapi"
)

// newAssetListScreen builds the Assets list through the REAL app wiring
// (newScreenFor), so the search tests exercise the spec app.go ships rather
// than a copy of it.
func newAssetListScreen(deps Deps) *ListScreen {
	return newScreenFor(WSAssets, deps).(*ListScreen)
}

// TestSearchAssets_ForwardsSearchParam is the load-bearing contract for the
// bead: the asset list forwards the operator's query to the OMS asset
// endpoint as ?search=, which the backend matches against asset_tag (among
// other fields). The matched asset's tag also surfaces in the row subtitle.
func TestSearchAssets_ForwardsSearchParam(t *testing.T) {
	var gotPath, gotSearch string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotSearch = r.URL.Query().Get("search")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"count":1,"next":null,"previous":null,"results":[` +
			`{"id":"a-1","name":"Metal lathe","asset_tag":"DMS-2507001SS","status":"active"}]}`))
	}))
	defer srv.Close()

	deps := Deps{OMS: omsapi.New(srv.URL), Ctx: context.Background()}
	s := newAssetListScreen(deps)
	s.searchQuery = "DMS-2507001SS"
	page, err := s.spec.pager(context.Background(), deps, s.pageQuery(1))
	if err != nil {
		t.Fatalf("assetPage: %v", err)
	}
	rows := page.rows
	if gotPath != "/api/inventory/assets/" {
		t.Fatalf("path = %q, want /api/inventory/assets/", gotPath)
	}
	if gotSearch != "DMS-2507001SS" {
		t.Fatalf("search param = %q, want the asset tag forwarded", gotSearch)
	}
	if len(rows) != 1 || rows[0].ID != "a-1" || rows[0].Title != "Metal lathe" {
		t.Fatalf("rows = %+v, want the matched asset", rows)
	}
	// The tag rides in the subtitle so an operator sees which asset matched.
	if !strings.Contains(rows[0].Subtitle, "DMS-2507001SS") {
		t.Fatalf("subtitle = %q, want it to include the asset tag", rows[0].Subtitle)
	}
}

// TestSearchAssets_BlankQueryOmitsParam confirms a whitespace-only query sends
// no ?search= at all (so the endpoint returns the default listing, not an
// empty-string filter). Typed through the overlay, because trimming the query
// is the overlay's job and the query the pager is handed is the layer's.
func TestSearchAssets_BlankQueryOmitsParam(t *testing.T) {
	var hadSearch bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, hadSearch = r.URL.Query()["search"]
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"count":0,"next":null,"previous":null,"results":[]}`))
	}))
	defer srv.Close()

	deps := Deps{OMS: omsapi.New(srv.URL), Ctx: context.Background()}
	s := newAssetListScreen(deps)
	next, _ := s.Update(runeKey('/'))
	s = next.(*ListScreen)
	var cmd tea.Cmd
	for _, r := range "   " {
		next, cmd = s.Update(runeKey(r))
		s = next.(*ListScreen)
	}
	if cmd == nil {
		t.Fatalf("typing should schedule a search")
	}
	if _, ok := s.runSearch()().(listSearchedMsg); !ok {
		t.Fatalf("the search reply should be a listSearchedMsg")
	}
	if hadSearch {
		t.Fatalf("a blank query must not send a ?search= param")
	}
}

// TestListScreen_AssetSearchFlow drives the whole keyboard flow: '/' opens the
// in-list search, typing an asset tag forwards it to the backend, the matched
// asset replaces the list, and enter opens its detail screen.
func TestListScreen_AssetSearchFlow(t *testing.T) {
	var gotSearch string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotSearch = r.URL.Query().Get("search")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"count":1,"next":null,"previous":null,"results":[` +
			`{"id":"a-9","name":"Bench lathe","asset_tag":"DMS-2507001SS","status":"active"}]}`))
	}))
	defer srv.Close()

	deps := Deps{OMS: omsapi.New(srv.URL), Ctx: context.Background()}
	s := newAssetListScreen(deps)
	next, _ := s.Update(tea.WindowSizeMsg{Width: 80, Height: 40})
	s = next.(*ListScreen)

	// '/' opens search and flips the screen into raw-input mode so the global
	// hotkey layer stops eating the operator's keystrokes.
	next, _ = s.Update(runeKey('/'))
	s = next.(*ListScreen)
	if !s.searching {
		t.Fatalf("'/' should open the in-list search")
	}
	if !s.WantsRawInput() {
		t.Fatalf("search mode should claim raw input")
	}

	// Type a full asset tag; each keystroke updates the query and schedules a
	// backend search.
	var cmd tea.Cmd
	for _, r := range "DMS-2507001SS" {
		next, cmd = s.Update(runeKey(r))
		s = next.(*ListScreen)
	}
	if s.searchQuery != "DMS-2507001SS" {
		t.Fatalf("searchQuery = %q, want the typed tag", s.searchQuery)
	}
	if !s.searchPending {
		t.Fatalf("typing should mark a search pending")
	}
	if cmd == nil {
		t.Fatalf("typing should schedule a search cmd")
	}

	// Run the freshest search and feed the result back, as the runtime would.
	msg := s.runSearch()()
	sr, ok := msg.(listSearchedMsg)
	if !ok {
		t.Fatalf("msg = %T, want listSearchedMsg", msg)
	}
	if sr.err != nil {
		t.Fatalf("search err: %v", sr.err)
	}
	next, _ = s.Update(sr)
	s = next.(*ListScreen)

	if gotSearch != "DMS-2507001SS" {
		t.Fatalf("backend received search = %q, want the tag", gotSearch)
	}
	if len(s.rows) != 1 || s.rows[0].ID != "a-9" {
		t.Fatalf("rows = %+v, want the tag-matched asset", s.rows)
	}
	if s.searchPending {
		t.Fatalf("searchPending should clear once results arrive")
	}

	// enter opens the highlighted asset's detail screen.
	_, ecmd := s.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if ecmd == nil {
		t.Fatalf("enter should open the selected asset")
	}
	switch m := ecmd().(type) {
	case SwitchScreenMsg:
		if m.Workspace != WSAssets {
			t.Fatalf("switch workspace = %v, want assets", m.Workspace)
		}
		if _, ok := m.Screen.(*AssetDetailScreen); !ok {
			t.Fatalf("switch target = %T, want *AssetDetailScreen", m.Screen)
		}
	default:
		t.Fatalf("enter msg = %T, want SwitchScreenMsg", m)
	}
}

// TestListScreen_SearchStaleResponseDropped confirms the seq guard: a response
// for a query the operator has already typed past is ignored, so a slow reply
// can't clobber fresher results.
func TestListScreen_SearchStaleResponseDropped(t *testing.T) {
	deps := Deps{OMS: omsapi.New("http://example.invalid"), Ctx: context.Background()}
	s := newAssetListScreen(deps)
	next, _ := s.Update(runeKey('/'))
	s = next.(*ListScreen)

	s.rawRows = []listRow{{ID: "fresh", Title: "Fresh"}}
	s.applySort()
	s.searchSeq = 5 // pretend a newer search has since been issued
	stale := listSearchedMsg{seq: 4, rows: []listRow{{ID: "stale", Title: "Stale"}}}
	next, _ = s.Update(stale)
	s = next.(*ListScreen)
	if len(s.rows) != 1 || s.rows[0].ID != "fresh" {
		t.Fatalf("stale response should be dropped; rows = %+v", s.rows)
	}
}

// TestListScreen_SearchEscRestoresList confirms esc after a query closes search
// and reloads the full (unfiltered) list.
func TestListScreen_SearchEscRestoresList(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("search") != "" {
			_, _ = w.Write([]byte(`{"count":1,"next":null,"previous":null,"results":[` +
				`{"id":"a-9","name":"Bench lathe","asset_tag":"DMS-2507001SS","status":"active"}]}`))
			return
		}
		_, _ = w.Write([]byte(`{"count":2,"next":null,"previous":null,"results":[` +
			`{"id":"a-1","name":"Drill"},{"id":"a-2","name":"Saw"}]}`))
	}))
	defer srv.Close()

	deps := Deps{OMS: omsapi.New(srv.URL), Ctx: context.Background()}
	s := newAssetListScreen(deps)
	next, _ := s.Update(runeKey('/'))
	s = next.(*ListScreen)
	s.searchInput.SetValue("DMS")
	s.searchQuery = "DMS"

	// Apply a filtered result, then esc.
	next, _ = s.Update(s.runSearch()())
	s = next.(*ListScreen)
	if len(s.rows) != 1 {
		t.Fatalf("filtered rows = %d, want 1", len(s.rows))
	}

	next, cmd := s.Update(tea.KeyMsg{Type: tea.KeyEsc})
	s = next.(*ListScreen)
	if s.searching {
		t.Fatalf("esc should close search")
	}
	if s.searchQuery != "" {
		t.Fatalf("esc should clear the query, got %q", s.searchQuery)
	}
	if cmd == nil {
		t.Fatalf("esc after a query should reload the full list")
	}
	reload, ok := cmd().(listLoadedMsg)
	if !ok {
		t.Fatalf("reload msg = %T, want listLoadedMsg", cmd())
	}
	next, _ = s.Update(reload)
	s = next.(*ListScreen)
	if len(s.rows) != 2 {
		t.Fatalf("after esc reload, rows = %d, want the full list of 2", len(s.rows))
	}
}

// TestListScreen_SlashClaimGatedOnSearchLoader confirms '/' is claimed (and
// opens search) only when the list has a server search; a plain list leaves '/'
// to the global search palette and treats a direct '/' as a no-op.
func TestListScreen_SlashClaimGatedOnSearchLoader(t *testing.T) {
	deps := Deps{Ctx: context.Background()}

	withSearch := newAssetListScreen(deps)
	if !withSearch.HandlesKey("/") {
		t.Fatalf("assets list should claim '/'")
	}
	if !withSearch.HandlesKey("s") {
		t.Fatalf("list should still claim 's' (sort)")
	}

	plain := NewListScreen(deps, "SIGs", listScreenSpec{kind: "sigs", loader: loadSIGs})
	if plain.HandlesKey("/") {
		t.Fatalf("a list without a server search must not claim '/'")
	}
	next, cmd := plain.Update(runeKey('/'))
	ps := next.(*ListScreen)
	if ps.searching {
		t.Fatalf("'/' must not open search without a server search")
	}
	if cmd != nil {
		t.Fatalf("'/' no-op should return no cmd")
	}
}
