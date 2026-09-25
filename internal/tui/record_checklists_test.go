package tui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/uid0/scantty/internal/omsapi"
)

// The per-record checklist list, driven through Root from each of the three
// detail sheets against RECORDED OMS bodies (internal/omsapi/testdata/README.md
// carries their provenance), so the route each sheet reaches, the rows it draws
// and the refusal it relays are the server's rather than a fake's.

const (
	rcAssetID    = "702421aa-97e5-4e54-8765-9a0e3a4ab301"
	rcKitID      = "1a3ccb1b-e843-4f6b-b58b-aa9763193698"
	rcLocationID = 1200041
	rcOpeningID  = "463fa56f-6454-4b37-b3b1-02cfe4063d81"
	rcRunID      = "89fa43c1-c5b6-4de4-bd07-5c511b8c6398"
)

func rcWire(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "omsapi", "testdata", name))
	if err != nil {
		t.Fatalf("recorded response %s: %v", name, err)
	}
	return b
}

// rcReply is one recorded answer the fake gives a route.
type rcReply struct {
	status  int
	fixture string
}

// rcFake answers each METHOD+PATH with a recorded body and keeps every request it
// was asked, query included.
type rcFake struct {
	t       *testing.T
	replies map[string]rcReply
	mu      sync.Mutex
	seen    []string
}

func (f *rcFake) handler(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.seen = append(f.seen, r.Method+" "+r.URL.RequestURI())
	f.mu.Unlock()
	reply, ok := f.replies[r.Method+" "+r.URL.Path]
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"detail":"Not found."}`))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(reply.status)
	_, _ = w.Write(rcWire(f.t, reply.fixture))
}

func (f *rcFake) requests(prefix string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, s := range f.seen {
		if strings.HasPrefix(s, prefix) {
			out = append(out, s)
		}
	}
	return out
}

func rcDeps(t *testing.T, replies map[string]rcReply) (Deps, *rcFake) {
	t.Helper()
	fake := &rcFake{t: t, replies: replies}
	srv := httptest.NewServer(http.HandlerFunc(fake.handler))
	t.Cleanup(srv.Close)
	return Deps{OMS: omsapi.New(srv.URL), Ctx: context.Background()}, fake
}

// rcRoot puts a LOADED detail sheet under a sized Root.
func rcRoot(t *testing.T, deps Deps, screen Screen) Root {
	t.Helper()
	r := newTestRoot(screen)
	r.deps = deps
	return pump(t, r, func() tea.Msg { return tea.WindowSizeMsg{Width: 80, Height: 24} }, 0)
}

func rcAssetDetail(deps Deps) Screen {
	s := NewAssetDetailScreen(deps, rcAssetID)
	next, _ := s.Update(assetDetailLoadedMsg{asset: &omsapi.Asset{ID: rcAssetID, Name: "SawStop PCS 3HP cabinet saw"}})
	return next
}

func rcItemDetail(deps Deps) Screen {
	s := NewInventoryDetailScreen(deps, rcKitID)
	next, _ := s.Update(inventoryDetailLoadedMsg{item: &omsapi.Item{ID: rcKitID, Name: "Saw blade change kit"}})
	return next
}

func rcLocationDetail(deps Deps) Screen {
	s := NewLocationDetailScreen(deps, "1200041")
	next, _ := s.Update(locationDetailLoadedMsg{loc: &omsapi.Location{ID: rcLocationID, Name: "Wood shop, south wall"}})
	return next
}

// rcPane is the list screen's own frame clipped to the 80-column pane, stripped
// and with its whitespace collapsed, so a sentence the frame folds still reads
// as one.
func rcPane(r Root) string {
	return strings.Join(strings.Fields(stripANSI(clampToBox(r.screen.View(), screenBodyWidth(80), screenBodyHeight(24)))), " ")
}

// THE KEY ON EACH SHEET REACHES THAT RECORD'S ROUTE — the one the web's scan
// landing page calls for it — and draws what came back. The item is a KIT, which
// is the case include_kits exists for.
func TestRecordChecklists_EachDetailSheetListsItsOwnRecordsChecklists(t *testing.T) {
	cases := []struct {
		name    string
		sheet   func(Deps) Screen
		route   string
		query   string
		fixture string
		want    string
	}{
		{"asset", rcAssetDetail, "/api/inventory/assets/" + rcAssetID + "/checklists/", "", "checklists_asset_member.json", "Opening walkthrough — wood shop"},
		{"item", rcItemDetail, "/api/inventory/items/" + rcKitID + "/checklists/", "?include_kits=true", "checklists_item_kit.json", "Blade kit audit"},
		{"location", rcLocationDetail, "/api/inventory/locations/1200041/checklists/", "", "checklists_location.json", "Opening walkthrough — wood shop"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			deps, fake := rcDeps(t, map[string]rcReply{"GET " + tc.route: {http.StatusOK, tc.fixture}})
			r := rcRoot(t, deps, tc.sheet(deps))
			r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'K'}})
			if _, ok := r.screen.(*RecordChecklistsScreen); !ok {
				t.Fatalf("K on the %s sheet left the screen at %T", tc.name, r.screen)
			}
			if got := fake.requests("GET /api/inventory/"); len(got) != 1 || got[0] != "GET "+tc.route+tc.query {
				t.Fatalf("asked %v, want exactly GET %s%s", got, tc.route, tc.query)
			}
			pane := rcPane(r)
			if !strings.Contains(pane, tc.want) || !strings.Contains(pane, "enter start a run") {
				t.Errorf("the list did not draw the recorded checklist and the key that starts it:\n%s", pane)
			}
		})
	}
}

// THE BAR OF EACH SHEET NAMES THE KEY. A key that acts must be named, and the two
// sheets still on a literal footer are invisible to the prose-bar sweep, so they
// are read here directly.
func TestRecordChecklists_EachDetailSheetNamesTheKey(t *testing.T) {
	deps := Deps{}
	for name, sheet := range map[string]func(Deps) Screen{
		"asset": rcAssetDetail, "item": rcItemDetail, "location": rcLocationDetail,
	} {
		s := sheet(deps)
		s, _ = s.Update(tea.WindowSizeMsg{Width: 120, Height: 60})
		if got := strings.Join(strings.Fields(stripANSI(s.View())), " "); !strings.Contains(got, "K checklists") {
			t.Errorf("the %s sheet does not name K:\n%s", name, got)
		}
	}
}

// ENTER STARTS A RUN OF THE CHECKLIST UNDER THE CURSOR AND OPENS THE EXISTING
// RUN SCREEN ON IT — one POST, however often enter is pressed while it is out.
func TestRecordChecklists_EnterStartsTheRunOnce(t *testing.T) {
	deps, fake := rcDeps(t, map[string]rcReply{
		"GET /api/inventory/assets/" + rcAssetID + "/checklists/":    {http.StatusOK, "checklists_asset_member.json"},
		"POST /api/checklists/checklists/" + rcOpeningID + "/start/": {http.StatusCreated, "checklist_start.json"},
	})
	r := rcRoot(t, deps, rcAssetDetail(deps))
	r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'K'}})

	// Two presses before anything is pumped: the second lands while the first
	// start is out, where the bar no longer names enter.
	next, first := r.Update(tea.KeyMsg{Type: tea.KeyEnter})
	r = next.(Root)
	if pane := rcPane(r); !strings.Contains(pane, "Starting Opening walkthrough") || strings.Contains(pane, "enter start a run") {
		t.Errorf("a start in flight must say so and take enter off the bar:\n%s", pane)
	}
	next, second := r.Update(tea.KeyMsg{Type: tea.KeyEnter})
	r = next.(Root)
	if second != nil {
		t.Error("enter while a start is out issued a command")
	}
	r = pump(t, r, first, 0)

	if got := fake.requests("POST "); len(got) != 1 {
		t.Fatalf("POSTs = %v, want exactly one start", got)
	}
	run, ok := r.screen.(*ChecklistRunScreen)
	if !ok {
		t.Fatalf("after the start the screen is %T, want the checklist run", r.screen)
	}
	if run.completionID != rcRunID {
		t.Errorf("the run screen opened on %q, want the recorded run %q", run.completionID, rcRunID)
	}
}

// A REFUSED START IS THE SERVER'S SENTENCE, ON THE PANE — not the raw JSON the
// error carries, and not only a toast that expires.
func TestRecordChecklists_ARefusedStartSaysTheServersSentence(t *testing.T) {
	deps, _ := rcDeps(t, map[string]rcReply{
		"GET /api/inventory/assets/" + rcAssetID + "/checklists/":    {http.StatusOK, "checklists_asset_member.json"},
		"POST /api/checklists/checklists/" + rcOpeningID + "/start/": {http.StatusForbidden, "checklist_start_forbidden.json"},
	})
	r := rcRoot(t, deps, rcAssetDetail(deps))
	r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'K'}})
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})

	if _, ok := r.screen.(*RecordChecklistsScreen); !ok {
		t.Fatalf("a refused start left the list for %T", r.screen)
	}
	pane := rcPane(r)
	if !strings.Contains(pane, "✗ You do not have permission to start this checklist.") {
		t.Errorf("the refusal is not drawn in the server's words:\n%s", pane)
	}
	if strings.Contains(pane, `{"detail"`) || strings.Contains(pane, "http 403") {
		t.Errorf("the refusal is drawn as the raw error:\n%s", pane)
	}
	// And enter is offered again: the refusal is an answer, not a wedge.
	if !strings.Contains(pane, "enter start a run") {
		t.Errorf("after a refusal the start key is gone:\n%s", pane)
	}
}

// A RECORD NO CHECKLIST NAMES SAYS SO. The web hides the section; a screen the
// operator pressed a key to reach cannot.
func TestRecordChecklists_ARecordNoChecklistNamesSaysSo(t *testing.T) {
	deps, _ := rcDeps(t, map[string]rcReply{
		"GET /api/inventory/locations/1200041/checklists/": {http.StatusOK, "checklists_location_none.json"},
	})
	r := rcRoot(t, deps, rcLocationDetail(deps))
	r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'K'}})
	pane := rcPane(r)
	if !strings.Contains(pane, "No checklist you can run has a step on this location.") {
		t.Errorf("the empty list does not say what it found:\n%s", pane)
	}
	if strings.Contains(pane, "enter") {
		t.Errorf("the empty list names enter, which has nothing to start:\n%s", pane)
	}
}

// THE GLOBAL LIST READS available/, so an ordinary member sees the checklists
// they can run rather than the empty management page (the recorded pair in
// internal/omsapi/testdata pins the difference).
func TestChecklists_TheGlobalListReadsWhatTheReaderCanRun(t *testing.T) {
	deps, fake := rcDeps(t, map[string]rcReply{
		"GET /api/checklists/checklists/available/": {http.StatusOK, "checklists_available_member.json"},
		"GET /api/checklists/checklists/":           {http.StatusOK, "checklists_list_member.json"},
	})
	screen := NewChecklistsScreen(deps)
	r := rcRoot(t, deps, screen)
	r = pump(t, r, screen.Init(), 0)
	pane := rcPane(r)
	for _, want := range []string{"Blade kit audit", "Opening walkthrough — wood shop"} {
		if !strings.Contains(pane, want) {
			t.Errorf("the global list is missing %q:\n%s", want, pane)
		}
	}
	if got := fake.requests("GET /api/checklists/checklists/"); len(got) != 1 || got[0] != "GET /api/checklists/checklists/available/" {
		t.Errorf("the list asked %v, want only available/", got)
	}
}
