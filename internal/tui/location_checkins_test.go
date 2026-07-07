package tui

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/uid0/scantty/internal/omsapi"
)

// checkinTestServer stands up a mux that answers the endpoints the check-in
// form touches: GetLocation (confirm), ListLocations (lookup, a BARE array per
// the LocationViewSet), and the checkin action. It records the last search
// param and whether a check-in was ever posted so tests can assert the
// "confirm before check-in" contract.
type checkinRecorder struct {
	gotSearch    string
	checkinBody  string
	checkinHits  int
	getLocHits   int
	lastGetLocID string
}

func newCheckinScreen(t *testing.T, handler http.HandlerFunc) (*LocationCheckinsScreen, func()) {
	t.Helper()
	srv := httptest.NewServer(handler)
	deps := Deps{OMS: omsapi.New(srv.URL), Ctx: context.Background()}
	s := NewLocationCheckinsScreen(deps)
	// Open the log form directly (the list loader is irrelevant to these tests).
	next, _ := s.Update(runeKey('n'))
	s = next.(*LocationCheckinsScreen)
	if !s.logging || s.step != checkinEntry {
		t.Fatalf("n should open the entry step; logging=%v step=%v", s.logging, s.step)
	}
	return s, srv.Close
}

// TestCheckin_ResolveConfirmsName is the core contract: after an id is entered,
// the id is resolved to its location NAME and shown for confirmation — the
// screen must NOT check in yet.
func TestCheckin_ResolveConfirmsName(t *testing.T) {
	rec := &checkinRecorder{}
	s, closeSrv := newCheckinScreen(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasPrefix(r.URL.Path, "/api/inventory/locations/") && r.Method == http.MethodGet:
			rec.getLocHits++
			rec.lastGetLocID = strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/inventory/locations/"), "/")
			_, _ = w.Write([]byte(`{"id":7,"name":"Main Shop","access_code":"SHOP1","is_active":true}`))
		case strings.Contains(r.URL.Path, "/checkin/"):
			rec.checkinHits++
			w.WriteHeader(http.StatusBadRequest) // must never be reached in this test
		}
	})
	defer closeSrv()

	s.locInput.SetValue("7")
	next, cmd := s.Update(tea.KeyMsg{Type: tea.KeyEnter})
	s = next.(*LocationCheckinsScreen)
	if !s.resolving {
		t.Fatalf("enter should start a resolve, not check in")
	}
	if cmd == nil {
		t.Fatalf("resolve should schedule a GetLocation cmd")
	}
	msg := cmd()
	rmsg, ok := msg.(locationResolvedMsg)
	if !ok {
		t.Fatalf("resolve msg = %T, want locationResolvedMsg", msg)
	}
	next, _ = s.Update(rmsg)
	s = next.(*LocationCheckinsScreen)

	if rec.getLocHits != 1 || rec.lastGetLocID != "7" {
		t.Fatalf("GetLocation hits=%d id=%q, want 1 hit for id 7", rec.getLocHits, rec.lastGetLocID)
	}
	if rec.checkinHits != 0 {
		t.Fatalf("resolve must not check in; checkinHits=%d", rec.checkinHits)
	}
	if s.step != checkinConfirm {
		t.Fatalf("step = %v, want checkinConfirm after a successful resolve", s.step)
	}
	if s.resolved == nil || s.resolved.Name != "Main Shop" {
		t.Fatalf("resolved = %+v, want the Main Shop location", s.resolved)
	}
	if v := s.View(); !strings.Contains(v, "Main Shop") {
		t.Fatalf("confirm view should show the resolved name; got:\n%s", v)
	}
}

// TestCheckin_EndToEnd drives resolve → confirm → check-in and asserts the
// backend receives the check-in only after confirmation, keyed to the resolved
// location id.
func TestCheckin_EndToEnd(t *testing.T) {
	rec := &checkinRecorder{}
	s, closeSrv := newCheckinScreen(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasPrefix(r.URL.Path, "/api/inventory/locations/") && r.Method == http.MethodGet:
			_, _ = w.Write([]byte(`{"id":7,"name":"Main Shop","access_code":"SHOP1"}`))
		case strings.Contains(r.URL.Path, "/checkin/"):
			rec.checkinHits++
			buf, _ := io.ReadAll(r.Body)
			rec.checkinBody = string(buf)
			_, _ = w.Write([]byte(`{"id":"ci-1","location":7,"location_name":"Main Shop","checkin_type":"anonymous","checked_in_at":"2026-07-07T00:00:00Z"}`))
		}
	})
	defer closeSrv()

	s.locInput.SetValue("7")
	_, cmd := s.Update(tea.KeyMsg{Type: tea.KeyEnter})
	s.Update(cmd()) // deliver resolve
	if s.step != checkinConfirm {
		t.Fatalf("step = %v, want confirm", s.step)
	}

	next, ccmd := s.Update(tea.KeyMsg{Type: tea.KeyEnter})
	s = next.(*LocationCheckinsScreen)
	if !s.submitting || ccmd == nil {
		t.Fatalf("enter on confirm should submit the check-in")
	}
	submitted, ok := ccmd().(locationCheckinSubmittedMsg)
	if !ok {
		t.Fatalf("submit msg = %T, want locationCheckinSubmittedMsg", ccmd())
	}
	next, _ = s.Update(submitted)
	s = next.(*LocationCheckinsScreen)

	if rec.checkinHits != 1 {
		t.Fatalf("checkinHits = %d, want exactly 1", rec.checkinHits)
	}
	if !strings.Contains(rec.checkinBody, `"location_id":7`) {
		t.Fatalf("check-in body = %q, want location_id 7", rec.checkinBody)
	}
	if s.logging {
		t.Fatalf("a successful check-in should close the form")
	}
	if s.locInput.Value() != "" {
		t.Fatalf("a successful check-in should clear the id field; got %q", s.locInput.Value())
	}
}

// TestCheckin_InvalidIDLocal confirms a non-numeric id is rejected locally with
// no backend request and no check-in.
func TestCheckin_InvalidIDLocal(t *testing.T) {
	rec := &checkinRecorder{}
	s, closeSrv := newCheckinScreen(t, func(w http.ResponseWriter, r *http.Request) {
		rec.getLocHits++
		rec.checkinHits++
	})
	defer closeSrv()

	s.locInput.SetValue("abc")
	next, cmd := s.Update(tea.KeyMsg{Type: tea.KeyEnter})
	s = next.(*LocationCheckinsScreen)
	if cmd != nil {
		t.Fatalf("an invalid id must not schedule any request")
	}
	if s.resolving {
		t.Fatalf("an invalid id must not enter the resolving state")
	}
	if s.step != checkinEntry {
		t.Fatalf("step = %v, want to stay on entry", s.step)
	}
	if s.submitErr == "" {
		t.Fatalf("an invalid id should surface an error message")
	}
	if rec.getLocHits != 0 || rec.checkinHits != 0 {
		t.Fatalf("no backend call expected; getLoc=%d checkin=%d", rec.getLocHits, rec.checkinHits)
	}
}

// TestCheckin_NotFoundNoCheckin confirms a 404 on resolve surfaces a clear
// message and leaves the operator on the entry step — never checking in against
// an id that doesn't resolve.
func TestCheckin_NotFoundNoCheckin(t *testing.T) {
	rec := &checkinRecorder{}
	s, closeSrv := newCheckinScreen(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/api/inventory/locations/") && r.Method == http.MethodGet:
			rec.getLocHits++
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"detail":"Not found."}`))
		case strings.Contains(r.URL.Path, "/checkin/"):
			rec.checkinHits++
		}
	})
	defer closeSrv()

	s.locInput.SetValue("999")
	_, cmd := s.Update(tea.KeyMsg{Type: tea.KeyEnter})
	msg := cmd()
	next, _ := s.Update(msg)
	s = next.(*LocationCheckinsScreen)

	if rec.getLocHits != 1 {
		t.Fatalf("resolve should have queried the location once; got %d", rec.getLocHits)
	}
	if rec.checkinHits != 0 {
		t.Fatalf("a 404 resolve must not check in; checkinHits=%d", rec.checkinHits)
	}
	if s.step != checkinEntry {
		t.Fatalf("step = %v, want to stay on entry after a 404", s.step)
	}
	if !strings.Contains(s.submitErr, "999") {
		t.Fatalf("submitErr = %q, want it to name the missing id", s.submitErr)
	}
}

// TestCheckin_LookupFiltersAndSelects opens the lookup, types a query (forwarded
// as ?search=), and selects a row — filling the id and jumping to confirm.
func TestCheckin_LookupFiltersAndSelects(t *testing.T) {
	rec := &checkinRecorder{}
	s, closeSrv := newCheckinScreen(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// ListLocations returns a BARE array (LocationViewSet overrides list()).
		if r.URL.Path == "/api/inventory/locations/" {
			rec.gotSearch = r.URL.Query().Get("search")
			_, _ = w.Write([]byte(`[{"id":12,"name":"Welding Bay","access_code":"WELD"},` +
				`{"id":13,"name":"Wood Shop","access_code":"WOOD"}]`))
		}
	})
	defer closeSrv()

	// 'l' opens the searchable lookup from the (focused) id field.
	next, _ := s.Update(runeKey('l'))
	s = next.(*LocationCheckinsScreen)
	if s.step != checkinLookup {
		t.Fatalf("'l' should open the lookup picker; step=%v", s.step)
	}

	for _, r := range "wel" {
		next, _ = s.Update(runeKey(r))
		s = next.(*LocationCheckinsScreen)
	}
	if s.pickInput.Value() != "wel" {
		t.Fatalf("pick query = %q, want the typed text", s.pickInput.Value())
	}

	// Run the freshest search and feed the result back, as the runtime would.
	lmsg, ok := s.runLookup()().(locationLookupMsg)
	if !ok {
		t.Fatalf("lookup msg wrong type")
	}
	next, _ = s.Update(lmsg)
	s = next.(*LocationCheckinsScreen)

	if rec.gotSearch != "wel" {
		t.Fatalf("backend search = %q, want the typed query forwarded", rec.gotSearch)
	}
	if len(s.pickRows) != 2 {
		t.Fatalf("pickRows = %d, want 2 matches", len(s.pickRows))
	}
	if v := s.View(); !strings.Contains(v, "Welding Bay") {
		t.Fatalf("lookup view should list the matches; got:\n%s", v)
	}

	// Move to the second row and select it: the id fills and we jump to confirm.
	next, _ = s.Update(tea.KeyMsg{Type: tea.KeyDown})
	s = next.(*LocationCheckinsScreen)
	next, _ = s.Update(tea.KeyMsg{Type: tea.KeyEnter})
	s = next.(*LocationCheckinsScreen)

	if s.step != checkinConfirm {
		t.Fatalf("selecting a row should jump to confirm; step=%v", s.step)
	}
	if s.locInput.Value() != "13" {
		t.Fatalf("selection should fill the id field; got %q", s.locInput.Value())
	}
	if s.resolved == nil || s.resolved.Name != "Wood Shop" {
		t.Fatalf("resolved = %+v, want the picked Wood Shop", s.resolved)
	}
}

// TestCheckin_LookupBlankQueryOmitsSearch confirms the initial (unfiltered) load
// sends no ?search= param, so the picker opens showing every active location.
func TestCheckin_LookupBlankQueryOmitsSearch(t *testing.T) {
	var hadSearch bool
	s, closeSrv := newCheckinScreen(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/inventory/locations/" {
			_, hadSearch = r.URL.Query()["search"]
			_, _ = w.Write([]byte(`[{"id":1,"name":"Front Desk"}]`))
		}
	})
	defer closeSrv()

	next, _ := s.Update(runeKey('l'))
	s = next.(*LocationCheckinsScreen)
	lmsg := s.runLookup()().(locationLookupMsg)
	s.Update(lmsg)
	if hadSearch {
		t.Fatalf("a blank lookup query must not send a ?search= param")
	}
}

// TestCheckin_LookupKeyIsLiteralInNotes confirms the lookup shortcut only fires
// from the id field: with notes focused, 'l' is ordinary text.
func TestCheckin_LookupKeyIsLiteralInNotes(t *testing.T) {
	s, closeSrv := newCheckinScreen(t, func(w http.ResponseWriter, r *http.Request) {})
	defer closeSrv()

	// Tab to the notes field, then type 'l'.
	next, _ := s.Update(tea.KeyMsg{Type: tea.KeyTab})
	s = next.(*LocationCheckinsScreen)
	if !s.focusNotes {
		t.Fatalf("tab should focus notes")
	}
	next, _ = s.Update(runeKey('l'))
	s = next.(*LocationCheckinsScreen)
	if s.step != checkinEntry {
		t.Fatalf("'l' in notes must not open the lookup; step=%v", s.step)
	}
	if s.notesInput.Value() != "l" {
		t.Fatalf("'l' in notes should be literal text; got %q", s.notesInput.Value())
	}
}

// TestCheckin_StaleResolveDropped confirms a confirm reply for an id the operator
// has since changed is ignored (no jump to confirm on the wrong location).
func TestCheckin_StaleResolveDropped(t *testing.T) {
	s := &LocationCheckinsScreen{logging: true, step: checkinEntry, resolving: true, resolveID: 42}
	stale := locationResolvedMsg{id: 41, loc: &omsapi.Location{ID: 41, Name: "Wrong"}}
	next, _ := s.Update(stale)
	s = next.(*LocationCheckinsScreen)
	if s.step == checkinConfirm || s.resolved != nil {
		t.Fatalf("a stale resolve reply must be dropped; step=%v resolved=%+v", s.step, s.resolved)
	}
	if !s.resolving {
		t.Fatalf("dropping a stale reply should keep waiting for the fresh one")
	}
}

// TestCheckin_StaleLookupDropped confirms the picker's seq guard drops an
// out-of-order search response.
func TestCheckin_StaleLookupDropped(t *testing.T) {
	s := &LocationCheckinsScreen{logging: true, step: checkinLookup, pickSeq: 5}
	s.pickRows = []omsapi.Location{{ID: 1, Name: "Fresh"}}
	stale := locationLookupMsg{seq: 4, rows: []omsapi.Location{{ID: 2, Name: "Stale"}}}
	next, _ := s.Update(stale)
	s = next.(*LocationCheckinsScreen)
	if len(s.pickRows) != 1 || s.pickRows[0].Name != "Fresh" {
		t.Fatalf("stale lookup response should be dropped; rows=%+v", s.pickRows)
	}
}

// TestCheckin_ResolveDroppedWhenFieldEdited closes the stale-resolve hole: if
// the operator keeps typing while GetLocation is in flight, the reply for the
// old id must NOT confirm — the confirm has to match what's on screen.
func TestCheckin_ResolveDroppedWhenFieldEdited(t *testing.T) {
	s := NewLocationCheckinsScreen(Deps{Ctx: context.Background()})
	s.openEntry()
	s.locInput.SetValue("7")
	next, _ := s.Update(tea.KeyMsg{Type: tea.KeyEnter}) // resolve id 7
	s = next.(*LocationCheckinsScreen)
	if !s.resolving {
		t.Fatalf("expected a resolve in flight")
	}
	// Operator keeps typing before the reply lands: the field is now 78.
	s.locInput.SetValue("78")
	// The in-flight reply for 7 arrives.
	next, _ = s.Update(locationResolvedMsg{id: 7, loc: &omsapi.Location{ID: 7, Name: "Main Shop"}})
	s = next.(*LocationCheckinsScreen)
	if s.step == checkinConfirm {
		t.Fatalf("a resolve reply for an id the operator has since edited must not confirm")
	}
	if s.resolving {
		t.Fatalf("resolving should clear after the stale reply is dropped")
	}
}

// TestCheckin_SubmittedNilRowNoPanic guards the success path against a nil row
// (a nil-deref there would crash the whole TUI).
func TestCheckin_SubmittedNilRowNoPanic(t *testing.T) {
	s := NewLocationCheckinsScreen(Deps{Ctx: context.Background()})
	s.openEntry()
	s.step = checkinConfirm
	next, _ := s.Update(locationCheckinSubmittedMsg{row: nil, err: nil})
	s = next.(*LocationCheckinsScreen)
	if s.logging {
		t.Fatalf("a successful check-in should close the form even with a nil row")
	}
}

// TestCheckin_ConfirmEscEditsID confirms esc on the confirm step returns to the
// id field without checking in.
func TestCheckin_ConfirmEscEditsID(t *testing.T) {
	s := NewLocationCheckinsScreen(Deps{Ctx: context.Background()})
	s.openEntry() // initialize the textinputs the real flow always creates first
	s.step = checkinConfirm
	s.resolved = &omsapi.Location{ID: 3, Name: "Bay"}
	next, _ := s.Update(tea.KeyMsg{Type: tea.KeyEsc})
	s = next.(*LocationCheckinsScreen)
	if s.step != checkinEntry {
		t.Fatalf("esc on confirm should return to the entry step; step=%v", s.step)
	}
	if !s.logging {
		t.Fatalf("esc on confirm should stay in the form, not close it")
	}
}
