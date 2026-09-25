package tui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/uid0/scantty/internal/omsapi"
)

// Drives of the stint sheet's enforcement actions through a real Root, against
// a fake that serves RECORDED OMS bodies (internal/omsapi/testdata/README.md).
//
// The detail_* recordings are before-and-after SEQUENCES off one stint, so what
// the fake answers after a write is what the server really answered after it —
// a fake built from ScanTTY's own struct could only agree with the struct.

// stintWire reads a recorded body.
func stintWire(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "omsapi", "testdata", name))
	if err != nil {
		t.Fatalf("recorded response %s: %v", name, err)
	}
	return b
}

type stintReply struct {
	status  int
	fixture string
}

type stintRequest struct {
	method, path string
	body         map[string]any
}

// stintFake answers each "METHOD path" with its queue of recorded replies,
// repeating the last, and remembers every request it was sent.
type stintFake struct {
	t      *testing.T
	mu     sync.Mutex
	routes map[string][]stintReply
	sent   []stintRequest
}

func newStintFake(t *testing.T, routes map[string][]stintReply) (*stintFake, Deps) {
	t.Helper()
	f := &stintFake{t: t, routes: routes}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		req := stintRequest{method: r.Method, path: r.URL.Path}
		if r.Body != nil {
			_ = json.NewDecoder(r.Body).Decode(&req.body)
		}
		f.sent = append(f.sent, req)
		key := r.Method + " " + r.URL.Path
		queue := f.routes[key]
		if len(queue) == 0 {
			t.Errorf("the fake was sent %s, which no recording answers", key)
			w.WriteHeader(http.StatusNotImplemented)
			return
		}
		reply := queue[0]
		if len(queue) > 1 {
			f.routes[key] = queue[1:]
		}
		if strings.HasSuffix(reply.fixture, ".html") {
			w.Header().Set("Content-Type", "text/html")
		} else {
			w.Header().Set("Content-Type", "application/json")
		}
		w.WriteHeader(reply.status)
		_, _ = w.Write(stintWire(t, reply.fixture))
	}))
	t.Cleanup(srv.Close)
	return f, Deps{OMS: omsapi.New(srv.URL), Ctx: context.Background()}
}

// posts is every write the fake received.
func (f *stintFake) posts() []stintRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []stintRequest
	for _, r := range f.sent {
		if r.method != http.MethodGet {
			out = append(out, r)
		}
	}
	return out
}

const (
	stintAlice = "/api/project-storage/stints/PS-JWBRFM4F/"
	stintBob   = "/api/project-storage/stints/PS-DTR7MFEG/"
)

// stintDriveHeight is tall enough for the whole sheet to be on the pane, so a
// drive can read a fact anywhere in it. Whether the sheet's keys survive a SHORT
// pane is the prose-bar sweeps' question, asked at every pane Root draws.
const stintDriveHeight = 60

// stintDrive opens the stint sheet through Root at 80 columns and loads it.
func stintDrive(t *testing.T, deps Deps, stintID string) (Root, *ProjectStorageDetailScreen) {
	t.Helper()
	screen := NewProjectStorageDetailScreen(deps, stintID)
	r := newTestRoot(screen)
	r.deps = deps
	r = pump(t, r, func() tea.Msg { return tea.WindowSizeMsg{Width: 80, Height: stintDriveHeight} }, 0)
	r = pump(t, r, screen.Init(), 0)
	return r, screen
}

// stintPane is the body pane an operator reads at 80 columns — the screen's render
// clipped exactly as Root clips it — without the status bar, whose toast would
// satisfy a substring the pane itself no longer carries.
func stintPane(r Root) string {
	return stripANSI(clampToBox(r.screen.View(), screenBodyCells(80), screenBodyRows(stintDriveHeight)))
}

func stintKey(t *testing.T, r Root, k string) Root {
	t.Helper()
	return key(t, r, listRuneKey(k))
}

func stintType(t *testing.T, r Root, text string) Root {
	t.Helper()
	for _, ch := range text {
		r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{ch}})
	}
	return r
}

// THE NOTICE: offered on the expired stint, confirmed on a frame naming the
// stint, the member and the address, sent as the web sends it, and answered on
// the sheet the server re-reads.
func TestStintNotice_TheConfirmNamesTheMemberAndTheWriteLands(t *testing.T) {
	fake, deps := newStintFake(t, map[string][]stintReply{
		"GET " + stintAlice:                             {{200, "project_storage_detail_expired.json"}, {200, "project_storage_detail_warned.json"}},
		"POST " + stintAlice + "send-violation-notice/": {{200, "project_storage_send_notice.json"}},
	})
	r, _ := stintDrive(t, deps, "PS-JWBRFM4F")

	if pane := stintPane(r); !strings.Contains(pane, "n send notice") || strings.Contains(pane, "P purgatory") {
		t.Fatalf("an EXPIRED stint offers the notice and not purgatory:\n%s", pane)
	}

	r = stintKey(t, r, "n")
	frame := stintPane(r)
	for _, want := range []string{"PS-JWBRFM4F", "Alice Smith (@alice)", "alice@example.org", "y/Y send", "n/N/esc cancel"} {
		if !strings.Contains(frame, want) {
			t.Errorf("the notice confirm does not carry %q:\n%s", want, frame)
		}
	}
	if len(fake.posts()) != 0 {
		t.Fatalf("opening the confirm wrote something: %v", fake.posts())
	}

	r = stintKey(t, r, "y")
	posts := fake.posts()
	if len(posts) != 1 || posts[0].path != stintAlice+"send-violation-notice/" || len(posts[0].body) != 0 {
		t.Fatalf("sent %+v, want one POST of {} to send-violation-notice/", posts)
	}
	pane := stintPane(r)
	if !strings.Contains(pane, "Violation notice sent to alice@example.org.") {
		t.Errorf("the landed notice is not answered on the sheet:\n%s", pane)
	}
	// The sheet re-read the stint, so the deadline the notice set is drawn and
	// purgatory is now on offer.
	if !strings.Contains(pane, "Purgatory due:") || !strings.Contains(pane, "P purgatory") {
		t.Errorf("after the notice the sheet should be the warned stint the server re-served:\n%s", pane)
	}
}

// A REFUSAL IS THE SERVER'S SENTENCE, on the sheet, and not the raw JSON body
// parseError hands over for a hand-built {"detail", "code"}.
func TestStintNotice_TheServersRefusalIsDrawnInItsOwnWords(t *testing.T) {
	fake, deps := newStintFake(t, map[string][]stintReply{
		"GET " + stintAlice:                             {{200, "project_storage_detail_expired.json"}},
		"POST " + stintAlice + "send-violation-notice/": {{422, "project_storage_notice_missing_email.json"}},
	})
	r, _ := stintDrive(t, deps, "PS-JWBRFM4F")
	r = stintKey(t, r, "n")
	r = stintKey(t, r, "y")
	if len(fake.posts()) != 1 {
		t.Fatalf("sent %d writes, want the one notice", len(fake.posts()))
	}
	pane := stintPane(r)
	if !strings.Contains(pane, "✗ no notice sent: Member has no on-file email") {
		t.Errorf("the 422's sentence is not on the sheet:\n%s", pane)
	}
	if strings.Contains(pane, `"code"`) || strings.Contains(pane, "missing_email") {
		t.Errorf("the sheet drew the raw refusal body rather than its sentence:\n%s", pane)
	}
}

// PURGATORY: offered on the warned stint, the location box sends what was typed,
// and the sheet re-reads the purgatory the server recorded.
func TestStintPurgatory_TheTypedLocationIsSentAndTheSheetReReads(t *testing.T) {
	fake, deps := newStintFake(t, map[string][]stintReply{
		"GET " + stintAlice:                         {{200, "project_storage_detail_warned.json"}, {200, "project_storage_detail_purgatory.json"}},
		"POST " + stintAlice + "move-to-purgatory/": {{200, "project_storage_move_to_purgatory_shelf9.json"}},
	})
	r, _ := stintDrive(t, deps, "PS-JWBRFM4F")
	r = stintKey(t, r, "P")
	frame := stintPane(r)
	for _, want := range []string{"Move PS-JWBRFM4F to purgatory?", "Alice Smith (@alice)", "Purgatory location:", "enter move"} {
		if !strings.Contains(frame, want) {
			t.Errorf("the purgatory confirm does not carry %q:\n%s", want, frame)
		}
	}
	r = stintType(t, r, "Shelf 9")
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})

	posts := fake.posts()
	if len(posts) != 1 || posts[0].path != stintAlice+"move-to-purgatory/" ||
		posts[0].body["purgatory_location_name"] != "Shelf 9" || len(posts[0].body) != 1 {
		t.Fatalf("sent %+v, want one POST carrying exactly purgatory_location_name=Shelf 9", posts)
	}
	pane := stintPane(r)
	if !strings.Contains(pane, "moved to Shelf 9.") || !strings.Contains(pane, "Moved to purgatory:") {
		t.Errorf("the landed purgatory is not on the re-read sheet:\n%s", pane)
	}
	if strings.Contains(pane, "P purgatory") || strings.Contains(pane, "n send notice") {
		t.Errorf("a stint IN purgatory offers neither action on the web:\n%s", pane)
	}
}

func TestStintPurgatory_TheServersRefusalIsDrawnInItsOwnWords(t *testing.T) {
	_, deps := newStintFake(t, map[string][]stintReply{
		"GET " + stintAlice:                         {{200, "project_storage_detail_warned.json"}},
		"POST " + stintAlice + "move-to-purgatory/": {{409, "project_storage_purgatory_notice_required.json"}},
	})
	r, _ := stintDrive(t, deps, "PS-JWBRFM4F")
	r = stintKey(t, r, "P")
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})
	if pane := stintPane(r); !strings.Contains(pane, "✗ not moved: Send the violation notice first") {
		t.Errorf("the 409's sentence is not on the sheet:\n%s", pane)
	}
}

// THE QR: generated on a stint with none, and the sheet then names where the
// image is served — off the retrieve, the one endpoint that sends it absolute.
func TestStintQR_GeneratingReReadsTheImageLocation(t *testing.T) {
	fake, deps := newStintFake(t, map[string][]stintReply{
		"GET " + stintBob: {{200, "project_storage_detail_active_no_qr.json"}, {200, "project_storage_detail_active_qr.json"}},
		// The write's own reply is not drawn, so any recorded 200 serves.
		"POST " + stintBob + "generate-qr/": {{200, "project_storage_generate_qr.json"}},
	})
	r, _ := stintDrive(t, deps, "PS-DTR7MFEG")
	if pane := stintPane(r); !strings.Contains(pane, "q generate QR") || !strings.Contains(pane, "none generated yet") {
		t.Fatalf("a stint with no QR should offer to generate one:\n%s", pane)
	}
	r = stintKey(t, r, "q")
	posts := fake.posts()
	if len(posts) != 1 || posts[0].body["include_logo"] != true {
		t.Fatalf("sent %+v, want one generate-qr POST carrying include_logo=true", posts)
	}
	// The URL is folded onto continuation rows, so it is read with the folds
	// joined back up.
	pane := strings.ReplaceAll(stintPane(r), "\n  ", "")
	if !strings.Contains(pane, "QR image generated for PS-DTR7MFEG.") ||
		!strings.Contains(pane, "/media/project_storage/qrcodes/project_storage_qr_PS-DTR7MFEG.png") ||
		!strings.Contains(pane, "q regenerate QR") {
		t.Errorf("the generated QR is not on the re-read sheet:\n%s", pane)
	}
}

func TestStintQR_TheRateLimitIsDrawnInItsOwnWords(t *testing.T) {
	_, deps := newStintFake(t, map[string][]stintReply{
		"GET " + stintBob:                   {{200, "project_storage_detail_active_no_qr.json"}},
		"POST " + stintBob + "generate-qr/": {{429, "project_storage_qr_rate_limited.json"}},
	})
	r, _ := stintDrive(t, deps, "PS-DTR7MFEG")
	r = stintKey(t, r, "q")
	if pane := stintPane(r); !strings.Contains(pane, "✗ no QR image generated: Rate limit exceeded") {
		t.Errorf("the 429's sentence is not on the sheet:\n%s", pane)
	}
}

// A KEY THE BAR DOES NOT NAME WRITES NOTHING: the notice and purgatory on an
// ACTIVE stint decline on the status row and never reach the wire.
func TestStintEnforcement_AnActiveStintOffersNeitherWrite(t *testing.T) {
	fake, deps := newStintFake(t, map[string][]stintReply{
		"GET " + stintBob: {{200, "project_storage_detail_active_no_qr.json"}},
	})
	r, _ := stintDrive(t, deps, "PS-DTR7MFEG")
	pane := stintPane(r)
	if strings.Contains(pane, "n send notice") || strings.Contains(pane, "P purgatory") {
		t.Fatalf("an ACTIVE stint names a write the web does not offer:\n%s", pane)
	}
	for _, k := range []string{"n", "P", "y", "enter"} {
		r = stintKey(t, r, k)
	}
	if posts := fake.posts(); len(posts) != 0 {
		t.Errorf("unoffered keys reached the wire: %+v", posts)
	}
	if s := r.screen.(*ProjectStorageDetailScreen); s.confirming() {
		t.Error("an unoffered key opened a confirm")
	}
	if status := stripANSI(r.View()); !strings.Contains(status, "no purgatory") {
		t.Errorf("the declining key did not say why:\n%s", status)
	}
}

// EMAIL DOWN WITHHOLDS THE NOTICE — the web disables the button on the same
// service key — and leaves purgatory, which is database-only, on offer.
func TestStintNotice_EmailDownWithholdsTheNoticeAndSaysSo(t *testing.T) {
	fake, deps := newStintFake(t, map[string][]stintReply{
		"GET " + stintAlice: {{200, "project_storage_detail_warned.json"}},
	})
	deps.Health = proseBarEmailDown().Health
	r, _ := stintDrive(t, deps, "PS-JWBRFM4F")
	pane := stintPane(r)
	if strings.Contains(pane, "n send notice") || !strings.Contains(pane, "P purgatory") {
		t.Errorf("with email down the bar should drop the notice and keep purgatory:\n%s", pane)
	}
	if !strings.Contains(pane, "Email delivery is temporarily unavailable") {
		t.Errorf("the sheet does not say why the notice is gone:\n%s", pane)
	}
	r = stintKey(t, r, "n")
	r = stintKey(t, r, "y")
	if posts := fake.posts(); len(posts) != 0 {
		t.Errorf("the withheld notice reached the wire: %+v", posts)
	}
}

// THE MEMBER'S STINTS are one key from the stint, and a row opens as a stint.
func TestStintMember_TheMembersStintsOpenFromTheStint(t *testing.T) {
	_, deps := newStintFake(t, map[string][]stintReply{
		"GET " + stintAlice: {{200, "project_storage_detail_warned.json"}},
		"GET /api/project-storage/stints/by-member/alice/": {{200, "project_storage_by_member.json"}},
		"GET /api/project-storage/stints/PS-TS92W2RY/":     {{200, "project_storage_detail_expired.json"}},
	})
	r, _ := stintDrive(t, deps, "PS-JWBRFM4F")
	r = stintKey(t, r, "m")
	list, ok := r.screen.(*ListScreen)
	if !ok {
		t.Fatalf("m opened %T, want the member's stint list", r.screen)
	}
	if list.Title() != "Stints held by @alice" {
		t.Errorf("title = %q", list.Title())
	}
	r = pump(t, r, func() tea.Msg { return tea.WindowSizeMsg{Width: 80, Height: 24} }, 0)
	pane := stripANSI(clampToBox(list.View(), screenBodyCells(80), list.listPaneRows()))
	for _, want := range []string{"PS-JWBRFM4F", "PS-TS92W2RY", "(purgatory warned)", "(removed)", "removed 2026-07-21"} {
		if !strings.Contains(pane, want) {
			t.Errorf("the member's list does not carry %q:\n%s", want, pane)
		}
	}
	r = key(t, r, tea.KeyMsg{Type: tea.KeyDown})
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})
	if detail, ok := r.screen.(*ProjectStorageDetailScreen); !ok || detail.stintID != "PS-TS92W2RY" {
		t.Errorf("enter on the second row opened %T, want that stint's sheet", r.screen)
	}
}

// A DOTTED USERNAME IS NAMED AS THE ROUTE'S LIMIT, not relayed as a bare 404
// that reads as "no such member".
func TestStintMember_ADottedUsernameSaysTheRouteCannotMatchIt(t *testing.T) {
	_, deps := newStintFake(t, map[string][]stintReply{
		"GET " + stintBob: {{200, "project_storage_detail_active_no_qr.json"}},
		"GET /api/project-storage/stints/by-member/bob.jones/": {{404, "project_storage_by_member_dotted_404.html"}},
	})
	r, _ := stintDrive(t, deps, "PS-DTR7MFEG")
	r = stintKey(t, r, "m")
	r = pump(t, r, func() tea.Msg { return tea.WindowSizeMsg{Width: 80, Height: 24} }, 0)
	view := stripANSI(r.screen.View())
	if !strings.Contains(view, "cannot match a username") || strings.Contains(view, "<!doctype") {
		t.Errorf("the dotted-username 404 is not explained:\n%s", view)
	}
}

// AN OVERSIZED MEMBER ROW KEEPS ITS STINT ID. A project title is free text a
// member typed at the kiosk, and a list title abbreviates from the right — so a
// row whose title led with the project lost the one identifier a warden acts on,
// exactly where the project was long. Every row line is bounded by the pane and
// every cut is marked.
func TestStintMember_AnOversizedRowKeepsItsStintIDAndMarksTheCut(t *testing.T) {
	var stints []omsapi.ProjectStorageStint
	if err := json.Unmarshal(stintWire(t, "project_storage_by_member.json"), &stints); err != nil {
		t.Fatalf("recorded by-member body: %v", err)
	}
	for i := range stints {
		stints[i].ProjectTitle = "Full-size replica of the Bridgeport series 2 knee mill head casting, " +
			"in three sections, awaiting the second coat of primer"
		stints[i].LocationDisplay = "Rack 12, top shelf behind the welding curtains on the north wall"
	}
	rows := projectStorageMemberRows(stints)
	for _, w := range jdeDrawableWidths() {
		if w > 120 {
			continue
		}
		s := NewProjectStorageMemberStintsScreen(Deps{}, "alice")
		s.loading = false
		s.rows = rows
		s.windowSize = len(rows)
		next, _ := s.Update(tea.WindowSizeMsg{Width: w, Height: 30})
		s = next.(*ListScreen)
		lines := strings.Split(s.View(), "\n")
		for _, st := range stints {
			var title string
			for _, l := range lines {
				if strings.Contains(stripANSI(l), st.StintID) {
					title = stripANSI(l)
				}
			}
			if title == "" {
				t.Errorf("at width %d no row line carries the stint id %s:\n%s", w, st.StintID, stripANSI(s.View()))
				continue
			}
			if !strings.Contains(title, paneCutMark) {
				t.Errorf("at width %d the abbreviated title %q does not mark its cut", w, title)
			}
		}
		for _, l := range lines {
			if lipgloss.Width(l) > screenBodyCells(w) {
				t.Errorf("at width %d a line runs past the %d-cell pane: %q", w, screenBodyCells(w), stripANSI(l))
			}
		}
	}
}

// EVERY ENFORCEMENT KEY ACTS EXACTLY WHERE IT IS NAMED — the biconditional, over
// every lifecycle status × email health × a write in flight.
//
// The prose-bar sweep cannot hold this: a key that DECLINES raises a toast, and
// a command counts as acting there, so a bar naming `n` on an active stint would
// pass it (verified by making the segment unconditional). What an enforcement key
// does is open a confirm, and that is what is asked here.
func TestStintEnforcement_EveryKeyActsExactlyWhereItIsNamed(t *testing.T) {
	statuses := []string{"active", "expiring_soon", "expired", "purgatory_warned", "purgatory", "removed"}
	named, unnamed := 0, 0
	for _, status := range statuses {
		for _, emailDown := range []bool{false, true} {
			for _, busy := range []bool{false, true} {
				for _, k := range []string{"n", "P"} {
					deps := Deps{}
					if emailDown {
						deps = proseBarEmailDown()
					}
					s := proseBarStint(deps, status, nil)
					s.generatingQR = busy
					proseBarSize(s, 80, 40)
					isNamed := s.proseBar().names(k)
					next, _ := s.Update(listRuneKey(k))
					acted := next.(*ProjectStorageDetailScreen).confirming()
					if isNamed != acted {
						t.Errorf("status %s, email down %v, write out %v: %q named=%v but opened a confirm=%v",
							status, emailDown, busy, k, isNamed, acted)
					}
					if isNamed {
						named++
					} else {
						unnamed++
					}
				}
			}
		}
	}
	if named == 0 || unnamed == 0 {
		t.Fatalf("named %d, unnamed %d — both sides of the biconditional must be reached", named, unnamed)
	}
}
