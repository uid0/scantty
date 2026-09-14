package tui

import (
	"context"
	"encoding/json"
	"io"
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

// Drives of the fixture refill screens through a real Root, against a fake
// serving the bodies RECORDED off a real OMS (internal/omsapi/testdata/README.md,
// "Fixtures and refill requests"), so what these screens are shown is what the
// server sends rather than what their structs expect.

const driveFixtureID = "352067b3-6d6a-4a9b-ac83-90a0e1958fa3"

// fixtureFake serves the recorded reads and answers each write with a recorded
// body at a status the test chooses, keeping every write body it was sent.
type fixtureFake struct {
	mu     sync.Mutex
	gets   map[string]int
	writes map[string][]string

	resolveStatus, resolveAllStatus, scanStatus int
	resolveBody, resolveAllBody, scanBody       string
}

func newFixtureFake(t *testing.T) (*fixtureFake, Deps) {
	t.Helper()
	f := &fixtureFake{
		gets: map[string]int{}, writes: map[string][]string{},
		resolveStatus: http.StatusOK, resolveBody: "fixture_refill_request_resolve.json",
		resolveAllStatus: http.StatusOK, resolveAllBody: "fixture_resolve_all.json",
		scanStatus: http.StatusCreated, scanBody: "fixture_scan.json",
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		reply := func(status int, name string) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			if strings.HasPrefix(name, "<") {
				_, _ = w.Write([]byte(name)) // a gateway's page, not a recording
				return
			}
			_, _ = w.Write(driveRecorded(t, name))
		}
		p := r.URL.Path
		if r.Method == http.MethodGet {
			f.gets[p]++
			switch {
			case p == "/api/inventory/fixture-refill-requests/" && r.URL.Query().Get("fixture") != "":
				reply(http.StatusOK, "fixture_refill_requests_for_fixture.json")
			case p == "/api/inventory/fixture-refill-requests/":
				reply(http.StatusOK, "fixture_refill_requests_pending.json")
			case strings.HasPrefix(p, "/api/inventory/fixtures/"):
				reply(http.StatusOK, "fixture_detail.json")
			case p == "/api/inventory/locations/1200041/fixtures/":
				reply(http.StatusOK, "location_fixtures.json")
			default:
				t.Errorf("unexpected GET %s", r.URL)
				w.WriteHeader(http.StatusNotFound)
			}
			return
		}
		body, _ := io.ReadAll(r.Body)
		f.writes[p] = append(f.writes[p], string(body))
		switch {
		case strings.HasSuffix(p, "/resolve/"):
			reply(f.resolveStatus, f.resolveBody)
		case strings.HasSuffix(p, "/resolve_all/"):
			reply(f.resolveAllStatus, f.resolveAllBody)
		case strings.HasSuffix(p, "/scan/"):
			reply(f.scanStatus, f.scanBody)
		default:
			t.Errorf("unexpected %s %s", r.Method, p)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return f, Deps{OMS: omsapi.New(srv.URL), Ctx: context.Background()}
}

func (f *fixtureFake) sent(path string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.writes[path]...)
}

func (f *fixtureFake) got(path string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.gets[path]
}

func driveRecorded(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "omsapi", "testdata", name))
	if err != nil {
		t.Fatalf("recorded response %s: %v", name, err)
	}
	return b
}

// fixtureDrive opens a screen in a sized Root and pumps its load.
func fixtureDrive(t *testing.T, deps Deps, screen Screen) Root {
	t.Helper()
	r := newTestRoot(screen)
	r.deps = deps
	next, _ := r.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	r = next.(Root)
	return pump(t, r, screen.Init(), 0)
}

func fixturePane(r Root) string {
	return stripANSI(clampToBox(r.screen.View(), screenBodyCells(80), screenBodyRows(24)))
}

// fixtureType types into the focused box WITHOUT pumping: typing is synchronous,
// and what a typed rune hands back is the caret's blink timer, which pump would
// wait out at its per-key budget (AGENTS.md, Gotchas).
func fixtureType(t *testing.T, r Root, text string) Root {
	t.Helper()
	for _, c := range text {
		next, _ := r.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{c}})
		r = next.(Root)
	}
	return r
}

// A RESOLVE WITH A BLANK NOTES BOX KEEPS THE REPORTER'S NOTE: the body carries no
// notes key at all, because `{"notes": ""}` is recorded erasing it.
func TestFixtureRefills_TheQueueResolvesARequestAndKeepsTheReportersNote(t *testing.T) {
	fake, deps := newFixtureFake(t)
	r := fixtureDrive(t, deps, NewFixtureRefillQueueScreen(deps))
	pane := fixturePane(r)
	for _, want := range []string{"Pending fixture refill requests (3)", "Wood shop hand wash station", "Anonymous", "Almost empty, pump sputters"} {
		if !strings.Contains(pane, want) {
			t.Fatalf("the queue does not show %q:\n%s", want, pane)
		}
	}

	r = key(t, r, woRuneKey("j"))
	r = key(t, r, woRuneKey("R"))
	pane = fixturePane(r)
	for _, want := range []string{"Resolve refill request", "maker", "Their note: Almost empty", "replace the request's own note", "enter resolve"} {
		if !strings.Contains(pane, want) {
			t.Fatalf("the resolve prompt does not say %q:\n%s", want, pane)
		}
	}

	listed := fake.got("/api/inventory/fixture-refill-requests/")
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})
	sent := fake.sent("/api/inventory/fixture-refill-requests/9bd248a2-f660-40e0-872a-2abc24be5c34/resolve/")
	if len(sent) != 1 {
		t.Fatalf("enter sent %d resolves to the row under the cursor, want 1 (writes: %v)", len(sent), fake.writes)
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(sent[0]), &body); err != nil || len(body) != 0 {
		t.Errorf("a blank notes box sent %s; only an absent key keeps the reporter's note", sent[0])
	}
	if fake.got("/api/inventory/fixture-refill-requests/") != listed+1 {
		t.Error("a landed resolve did not reload the queue")
	}
	if pane := fixturePane(r); !strings.Contains(pane, "✓ Resolved the request from maker") {
		t.Errorf("the queue does not say what the write did:\n%s", pane)
	}
}

// RESOLVE ALL SAYS WHAT IT WILL DO TO THE RECORD, sends what was typed, and
// relays the server's own sentence.
func TestFixtureRefills_ResolveAllNamesWhatItOverwritesAndRelaysTheServer(t *testing.T) {
	fake, deps := newFixtureFake(t)
	r := fixtureDrive(t, deps, NewFixtureDetailScreen(deps, driveFixtureID))
	pane := fixturePane(r)
	for _, want := range []string{"Bathroom 1 soap dispenser", "tag FIX-0001", "Refill: SOAP-1000 · Foaming hand soap", "Pending refill requests (2)", "A resolve all"} {
		if !strings.Contains(pane, want) {
			t.Fatalf("the fixture detail does not show %q:\n%s", want, pane)
		}
	}

	r = key(t, r, woRuneKey("A"))
	pane = fixturePane(r)
	for _, want := range []string{"including any filed since", "replace every request's own note"} {
		if !strings.Contains(strings.Join(strings.Fields(pane), " "), want) {
			t.Fatalf("the resolve-all prompt does not say %q:\n%s", want, pane)
		}
	}
	r = fixtureType(t, r, "Refilled from the storeroom")
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})

	sent := fake.sent("/api/inventory/fixtures/" + driveFixtureID + "/resolve_all/")
	if len(sent) != 1 || !strings.Contains(sent[0], `"notes":"Refilled from the storeroom"`) {
		t.Fatalf("resolve all sent %v, want the typed notes once", sent)
	}
	if pane := fixturePane(r); !strings.Contains(pane, "✓ Resolved 1 pending refill request(s)") {
		t.Errorf("the detail does not relay the server's sentence:\n%s", pane)
	}
}

// A REFUSAL ABOUT THE RECORD CLOSES THE PROMPT, relays the server's sentence and
// reloads the list the operator acted on.
func TestFixtureRefills_ARefusalIsTheServersSentenceAndReloads(t *testing.T) {
	fake, deps := newFixtureFake(t)
	fake.resolveStatus, fake.resolveBody = http.StatusBadRequest, "fixture_refill_request_resolve_completed.json"
	r := fixtureDrive(t, deps, NewFixtureDetailScreen(deps, driveFixtureID))
	listed := fake.got("/api/inventory/fixture-refill-requests/")

	r = key(t, r, woRuneKey("R"))
	r = fixtureType(t, r, "done")
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})

	s := r.screen.(*FixtureRefillScreen)
	if s.prompt != fixturePromptNone {
		t.Error("a refusal about the record left the prompt open over a list that was wrong")
	}
	if pane := fixturePane(r); !strings.Contains(pane, "✗ This request is already completed") || strings.Contains(pane, `"error"`) {
		t.Errorf("the refusal is not the server's own sentence:\n%s", pane)
	}
	if fake.got("/api/inventory/fixture-refill-requests/") != listed+1 {
		t.Error("a refusal about the record did not reload the list")
	}
}

// A FAILURE THAT SAYS NOTHING ABOUT THE RECORD KEEPS WHAT WAS TYPED, so enter is a
// retry.
func TestFixtureRefills_AGatewayFailureKeepsTheTypedNotes(t *testing.T) {
	fake, deps := newFixtureFake(t)
	r := fixtureDrive(t, deps, NewFixtureDetailScreen(deps, driveFixtureID))
	fake.mu.Lock()
	fake.resolveAllStatus, fake.resolveAllBody = http.StatusBadGateway, proseLoadGatewayPage
	fake.mu.Unlock()

	r = key(t, r, woRuneKey("A"))
	r = fixtureType(t, r, "Refilled")
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})

	s := r.screen.(*FixtureRefillScreen)
	if s.prompt != fixturePromptResolveAll || s.notes.Value() != "Refilled" {
		t.Fatalf("a gateway failure closed the prompt or dropped the notes: prompt=%d notes=%q", s.prompt, s.notes.Value())
	}
	if pane := fixturePane(r); !strings.Contains(pane, "✗ ") || !strings.Contains(pane, "enter resolve all") {
		t.Errorf("the kept prompt does not show the failure and its retry key:\n%s", pane)
	}
}

// A NEW REFILL REQUEST is filed with the typed notes, and is not offered on an
// inactive fixture — whose head says why.
func TestFixtureRefills_ARequestIsFiledOnlyOnAnActiveFixture(t *testing.T) {
	fake, deps := newFixtureFake(t)
	r := fixtureDrive(t, deps, NewFixtureDetailScreen(deps, driveFixtureID))
	r = key(t, r, woRuneKey("n"))
	r = fixtureType(t, r, "Pump sputters")
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})
	sent := fake.sent("/api/inventory/fixtures/" + driveFixtureID + "/scan/")
	if len(sent) != 1 || !strings.Contains(sent[0], `"notes":"Pump sputters"`) {
		t.Fatalf("n sent %v, want one refill request carrying the notes", sent)
	}
	if pane := fixturePane(r); !strings.Contains(pane, "✓ Refill request filed") {
		t.Errorf("the detail does not say the request was filed:\n%s", pane)
	}

	inactive := proseBarFixtureDetail(2, false)
	if inactive.proseBar().names("n") {
		t.Errorf("an inactive fixture's bar names n: %s", inactive.proseBar().hint())
	}
	if _, cmd := inactive.Update(woRuneKey("n")); cmd != nil || inactive.prompt != fixturePromptNone {
		t.Error("n on an inactive fixture opened a prompt for a request OMS refuses")
	}
	if !strings.Contains(stripANSI(inactive.View()), "Inactive: OMS refuses new refill requests.") {
		t.Errorf("the inactive fixture does not say why n is withheld:\n%s", stripANSI(inactive.View()))
	}
}

// THE WAYS IN: a location's `f` opens its fixtures, a fixture's enter opens its
// requests, a queue row's enter opens its fixture, and the queue is a row of the
// Inventory workspace.
func TestFixtureRefills_EveryWayInOpensTheFixture(t *testing.T) {
	fake, deps := newFixtureFake(t)

	loc := NewLocationDetailScreen(deps, "1200041")
	loc.Update(locationDetailLoadedMsg{loc: &omsapi.Location{ID: 1200041, Name: "Front bathroom", IsActive: true}})
	if !strings.Contains(loc.View(), "f fixtures") {
		t.Errorf("the location detail's footer does not name f:\n%s", loc.View())
	}
	r := newTestRoot(loc)
	r.deps = deps
	next, _ := r.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	r = key(t, next.(Root), woRuneKey("f"))
	if _, ok := r.screen.(*LocationFixturesScreen); !ok {
		t.Fatalf("f on a location opened %T, want its fixtures", r.screen)
	}
	pane := fixturePane(r)
	for _, want := range []string{"Front bathroom — fixtures (2)", "Bathroom 1 soap dispenser · 2 pending", "Bathroom 1 towel dispenser"} {
		if !strings.Contains(pane, want) {
			t.Fatalf("the location's fixtures do not show %q:\n%s", want, pane)
		}
	}
	if strings.Contains(pane, "Old soap dispenser") {
		t.Errorf("an inactive fixture reached a list OMS does not serve it on:\n%s", pane)
	}

	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})
	detail, ok := r.screen.(*FixtureRefillScreen)
	if !ok || detail.fixtureID != driveFixtureID {
		t.Fatalf("enter on a fixture opened %T (%+v), want its refill requests", r.screen, r.screen)
	}
	if fake.got("/api/inventory/fixtures/"+driveFixtureID+"/") == 0 {
		t.Error("the fixture detail never read the fixture")
	}

	q := openFromMenu(t, newTestRoot(NewWelcomeScreen()), WSInventory, "Fixture refills")
	if s, ok := q.screen.(*FixtureRefillScreen); !ok || !s.isQueue() {
		t.Fatalf("the Inventory workspace's Fixture refills row opened %T", q.screen)
	}

	queue := fixtureDrive(t, deps, NewFixtureRefillQueueScreen(deps))
	queue = key(t, queue, tea.KeyMsg{Type: tea.KeyEnter})
	if s, ok := queue.screen.(*FixtureRefillScreen); !ok || s.fixtureID != "f008fb58-f602-4bb3-938f-2509017e2d81" {
		t.Fatalf("enter on the queue's first row opened %T, want that row's fixture", queue.screen)
	}
}
