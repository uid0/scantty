package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/uid0/scantty/internal/omsapi"
)

// The reorder queue's lifecycle, driven through the REAL key handler against a
// stateful fake OMS — Root.Update in, an HTTP request out — because a test that
// calls the client method itself proves only that the method exists. What was
// broken was that no key reached it.
//
// Every assertion here is about the request that was really SENT: its path, and
// the keys its body did and did not carry.

// reorderFakeRow is one request row the fake serves, in the shape
// ReorderRequestSerializer writes.
type reorderFakeRow struct {
	id       int
	status   string
	name     string
	sku      string
	quantity int
}

func (r reorderFakeRow) payload() map[string]any {
	return map[string]any{
		"id":       r.id,
		"item":     fmt.Sprintf("item-uuid-%d", r.id),
		"quantity": r.quantity,
		"status":   r.status,
		"priority": "normal",
		"item_details": map[string]any{
			"id":            fmt.Sprintf("item-uuid-%d", r.id),
			"name":          r.name,
			"sku":           r.sku,
			"current_stock": 4,
			"minimum_stock": 25,
		},
		"estimated_cost": "35.00",
		"days_pending":   2,
	}
}

// reorderFake answers the four endpoints this screen drives and remembers every
// write it was asked to make.
type reorderFake struct {
	mu   sync.Mutex
	rows []reorderFakeRow

	// pageSize mirrors the backend's PAGE_SIZE so a test can put a row on page
	// two and prove the screen still reaches it.
	pageSize int

	// writes records (method, path, decoded body) for every POST.
	writes []reorderWrite

	// refuse, when set, is the {"detail": …} sentence mark_received answers
	// with instead of succeeding — the shape a cancelled request gets.
	refuse string
}

type reorderWrite struct {
	path string
	body map[string]any
}

func (f *reorderFake) row(id int) *reorderFakeRow {
	for i := range f.rows {
		if f.rows[i].id == id {
			return &f.rows[i]
		}
	}
	return nil
}

func (f *reorderFake) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		path := r.URL.Path

		if r.Method == http.MethodPost {
			body := map[string]any{}
			if raw, _ := io.ReadAll(r.Body); len(raw) > 0 {
				_ = json.Unmarshal(raw, &body)
			}
			f.writes = append(f.writes, reorderWrite{path: path, body: body})
			seg := strings.Split(strings.Trim(path, "/"), "/")
			id, _ := strconv.Atoi(seg[len(seg)-2])
			action := seg[len(seg)-1]
			if action == "mark_received" && f.refuse != "" {
				w.WriteHeader(http.StatusBadRequest)
				_ = json.NewEncoder(w).Encode(map[string]any{"detail": f.refuse})
				return
			}
			row := f.row(id)
			if row == nil {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			switch action {
			case "approve":
				row.status = omsapi.ReorderStatusApproved
			case "cancel":
				row.status = omsapi.ReorderStatusCancelled
			case "mark_ordered":
				row.status = omsapi.ReorderStatusOrdered
			case "mark_received":
				row.status = omsapi.ReorderStatusReceived
			}
			_ = json.NewEncoder(w).Encode(row.payload())
			return
		}

		if strings.HasSuffix(path, "/requests/pending/") {
			out := []map[string]any{}
			for _, row := range f.rows {
				if row.status == omsapi.ReorderStatusPending {
					out = append(out, row.payload())
				}
			}
			_ = json.NewEncoder(w).Encode(out)
			return
		}
		if strings.HasSuffix(path, "/requests/") {
			size := f.pageSize
			if size <= 0 {
				size = 50
			}
			page, _ := strconv.Atoi(r.URL.Query().Get("page"))
			if page < 1 {
				page = 1
			}
			start := (page - 1) * size
			end := start + size
			if start > len(f.rows) {
				start = len(f.rows)
			}
			if end > len(f.rows) {
				end = len(f.rows)
			}
			out := []map[string]any{}
			for _, row := range f.rows[start:end] {
				out = append(out, row.payload())
			}
			var next any
			if end < len(f.rows) {
				next = fmt.Sprintf("http://example.invalid/api/reorders/requests/?page=%d", page+1)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"count": len(f.rows), "next": next, "previous": nil, "results": out,
			})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}
}

func (f *reorderFake) writesTo(action string) []reorderWrite {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []reorderWrite
	for _, wr := range f.writes {
		if strings.HasSuffix(wr.path, "/"+action+"/") {
			out = append(out, wr)
		}
	}
	return out
}

// reorderDrive builds a Root over the screen, wired to the fake and sized to
// the 80x24 terminal this project checks against.
func reorderDrive(t *testing.T, f *reorderFake) (Root, *ReorderQueueScreen) {
	t.Helper()
	srv := httptest.NewServer(f.handler())
	t.Cleanup(srv.Close)

	deps := Deps{OMS: omsapi.New(srv.URL), Ctx: context.Background()}
	screen := NewReorderQueueScreen(deps)
	r := newTestRoot(screen)
	r.deps = deps
	next, _ := r.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	r = next.(Root)
	r = pump(t, r, screen.Init(), 0)
	return r, screen
}

// reorderFlat collapses runs of whitespace so an assertion about a SENTENCE is
// not really an assertion about where the fold happened to break it.
func reorderFlat(pane string) string {
	return strings.Join(strings.Fields(pane), " ")
}

func reorderKey(s string) tea.KeyMsg {
	switch s {
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

// cycleTo presses `f` until the screen is showing `want`, so a test names the
// view it wants rather than counting keystrokes.
func cycleTo(t *testing.T, r Root, s *ReorderQueueScreen, want reorderView) Root {
	t.Helper()
	for i := 0; i < int(reorderViewCount); i++ {
		if s.view == want {
			return r
		}
		r = key(t, r, reorderKey("f"))
	}
	t.Fatalf("f never reached the %s view (stuck on %s)", want.label(), s.view.label())
	return r
}

// TestReorderQueue_MarkOrderedPostsTheOneClickAction is the first half of the
// dead end this work closes: an APPROVED request could not be moved on from the
// terminal at all.
//
// It asserts the BODY as well as the path, and the assertion that matters is
// the NEGATIVE one. mark_ordered reads its three optional fields with
// `if "<key>" in request.data`, so a key sent EMPTY overwrites the stored value
// while a key left out leaves it alone — and `order_number` is carried onto the
// request by the Purchase Order domain. A well-meaning `{"order_number": ""}`
// would erase the PO's own number, silently, on every mark-ordered.
func TestReorderQueue_MarkOrderedPostsTheOneClickAction(t *testing.T) {
	fake := &reorderFake{rows: []reorderFakeRow{
		{id: 41, status: omsapi.ReorderStatusApproved, name: "Hex bolt M8x40 zinc", sku: "HEX-M8-40", quantity: 100},
	}}
	r, screen := reorderDrive(t, fake)
	r = cycleTo(t, r, screen, reorderViewApproved)
	if len(screen.rows) != 1 {
		t.Fatalf("approved view has %d rows, want the one approved request", len(screen.rows))
	}

	key(t, r, reorderKey("o"))

	writes := fake.writesTo("mark_ordered")
	if len(writes) != 1 {
		t.Fatalf("mark_ordered was posted %d times, want exactly once (writes: %+v)", len(writes), fake.writes)
	}
	if got, want := writes[0].path, "/api/reorders/requests/41/mark_ordered/"; got != want {
		t.Errorf("posted to %q, want %q", got, want)
	}
	for _, key := range []string{"order_number", "estimated_delivery", "actual_cost"} {
		if _, present := writes[0].body[key]; present {
			t.Errorf("body carried %q — an absent key leaves the stored value alone, "+
				"a present one overwrites it: %+v", key, writes[0].body)
		}
	}
	if fake.row(41).status != omsapi.ReorderStatusOrdered {
		t.Errorf("request 41 is %q, want ordered", fake.row(41).status)
	}
}

// TestReorderQueue_MarkReceivedPostsAfterTheConfirm is the second half, and it
// drives the confirm the way an operator does — `d` to ask, `y` to answer.
// `d` alone must write NOTHING: this is the one key on the screen that credits
// stock, and stock credited by a mis-keystroke is not undone by another one.
func TestReorderQueue_MarkReceivedPostsAfterTheConfirm(t *testing.T) {
	fake := &reorderFake{rows: []reorderFakeRow{
		{id: 77, status: omsapi.ReorderStatusOrdered, name: "Hex bolt M8x40 zinc", sku: "HEX-M8-40", quantity: 100},
	}}
	r, screen := reorderDrive(t, fake)
	r = cycleTo(t, r, screen, reorderViewOrdered)
	if len(screen.rows) != 1 {
		t.Fatalf("ordered view has %d rows, want the one ordered request", len(screen.rows))
	}

	r = key(t, r, reorderKey("d"))
	if got := len(fake.writesTo("mark_received")); got != 0 {
		t.Fatalf("`d` alone posted %d receipts — it must only ASK", got)
	}
	if screen.confirm != reorderConfirmReceive {
		t.Fatalf("`d` did not open the confirm (confirm=%v)", screen.confirm)
	}
	// The prompt names the quantity in the unit it is really credited in.
	pane := screen.View()
	if flat := reorderFlat(pane); !strings.Contains(flat, "Credits 100 to stock") ||
		!strings.Contains(flat, "not cases") {
		t.Errorf("the confirm does not say what it credits, or in which unit:\n%s", pane)
	}

	key(t, r, reorderKey("y"))

	writes := fake.writesTo("mark_received")
	if len(writes) != 1 {
		t.Fatalf("mark_received was posted %d times, want exactly once (writes: %+v)", len(writes), fake.writes)
	}
	if got, want := writes[0].path, "/api/reorders/requests/77/mark_received/"; got != want {
		t.Errorf("posted to %q, want %q", got, want)
	}
	// No actual_delivery: the terminal has no date field, and the server stamps
	// today. Sending "" would be a different claim, not a missing one.
	if _, present := writes[0].body["actual_delivery"]; present {
		t.Errorf("body carried actual_delivery: %+v", writes[0].body)
	}
	if fake.row(77).status != omsapi.ReorderStatusReceived {
		t.Errorf("request 77 is %q, want received", fake.row(77).status)
	}
}

// TestReorderQueue_ADeclinedConfirmWritesNothing — `n` backs out, and the
// screen SAYS so rather than redrawing a pane that is a pure function of
// unchanged state.
func TestReorderQueue_ADeclinedConfirmWritesNothing(t *testing.T) {
	fake := &reorderFake{rows: []reorderFakeRow{
		{id: 77, status: omsapi.ReorderStatusOrdered, name: "Hex bolt", sku: "HEX", quantity: 10},
	}}
	r, screen := reorderDrive(t, fake)
	r = cycleTo(t, r, screen, reorderViewOrdered)
	r = key(t, r, reorderKey("d"))
	r = key(t, r, reorderKey("n"))

	if got := len(fake.writesTo("mark_received")); got != 0 {
		t.Fatalf("declining the confirm still posted %d receipts", got)
	}
	if screen.confirm != reorderConfirmNone {
		t.Error("the confirm is still open after n")
	}
	if !strings.Contains(reorderFlat(screen.View()), "nothing written") {
		t.Errorf("the decline said nothing:\n%s", screen.View())
	}
	_ = r
}

// TestReorderQueue_ARefusalReachesTheOperatorAsItsSentence.
//
// mark_received refuses a cancelled request by RETURNING
// `Response({"detail": …}, 400)` from the view body, which never reaches DRF's
// exception handler — so parseError finds no code and hands the whole raw JSON
// over as the message. Without AsDetailRefusal the operator reads
// `oms: http 400: {"detail": "Cannot receive a cancelled reorder request."}`.
func TestReorderQueue_ARefusalReachesTheOperatorAsItsSentence(t *testing.T) {
	const sentence = "Cannot receive a cancelled reorder request."
	fake := &reorderFake{
		rows:   []reorderFakeRow{{id: 9, status: omsapi.ReorderStatusOrdered, name: "Hex bolt", sku: "HEX", quantity: 10}},
		refuse: sentence,
	}
	r, screen := reorderDrive(t, fake)
	r = cycleTo(t, r, screen, reorderViewOrdered)
	r = key(t, r, reorderKey("d"))
	key(t, r, reorderKey("y"))

	// The answer FOLDS at the pane, so the sentence is compared with runs of
	// whitespace collapsed: a check that only passes on an unfolded line would
	// fail the moment the wording grew past 51 cells, which is the width this
	// screen is built for.
	pane := screen.View()
	if !strings.Contains(reorderFlat(pane), sentence) {
		t.Errorf("the refusal's own sentence is not on the pane:\n%s", pane)
	}
	if strings.Contains(pane, `{"detail"`) {
		t.Errorf("the raw JSON body reached the operator:\n%s", pane)
	}
	if !strings.Contains(reorderFlat(pane), "nothing written") {
		t.Errorf("the pane does not say the write did not happen:\n%s", pane)
	}
}

// TestReorderQueue_AKeyIsRefusedWhereItsActionIsWrong. OMS gates neither
// mark_ordered nor mark_received on a status at all: `o` on a PENDING request
// would flip it straight to ordered, skipping the approval this project treats
// as a real gate, and the server would accept it. The workflow is stated on the
// CLIENT, so the refusal has to be tested here — nothing upstream will catch it.
func TestReorderQueue_AKeyIsRefusedWhereItsActionIsWrong(t *testing.T) {
	fake := &reorderFake{rows: []reorderFakeRow{
		{id: 3, status: omsapi.ReorderStatusPending, name: "Hex bolt", sku: "HEX", quantity: 10},
	}}
	r, screen := reorderDrive(t, fake)
	if len(screen.rows) != 1 {
		t.Fatalf("pending view has %d rows, want one", len(screen.rows))
	}

	r = key(t, r, reorderKey("o"))
	if got := len(fake.writesTo("mark_ordered")); got != 0 {
		t.Fatalf("`o` on a pending request posted %d times", got)
	}
	if pane := screen.View(); !strings.Contains(reorderFlat(pane), "nothing written") {
		t.Errorf("`o` refused in silence:\n%s", pane)
	}

	r = key(t, r, reorderKey("d"))
	if got := len(fake.writesTo("mark_received")); got != 0 {
		t.Fatalf("`d` on a pending request posted %d times", got)
	}
	if screen.confirm != reorderConfirmNone {
		t.Error("`d` opened a receive confirm on a pending request")
	}
	_ = r
}

// TestReorderQueue_TheBarNamesOnlyTheKeysThatWorkOnTheRow. The bar is where
// this screen states the workflow, and it changes shape as the cursor moves
// because the keys do. A bar that named `o` on a pending row would be
// advertising a write the screen refuses.
func TestReorderQueue_TheBarNamesOnlyTheKeysThatWorkOnTheRow(t *testing.T) {
	cases := []struct {
		status  string
		view    reorderView
		named   []string
		unnamed []string
	}{
		{omsapi.ReorderStatusPending, reorderViewPending,
			[]string{"a approve", "x cancel"}, []string{"o mark ordered", "d mark received"}},
		{omsapi.ReorderStatusApproved, reorderViewApproved,
			[]string{"o mark ordered", "x cancel"}, []string{"a approve", "d mark received"}},
		{omsapi.ReorderStatusOrdered, reorderViewOrdered,
			[]string{"d mark received"}, []string{"a approve", "o mark ordered", "x cancel"}},
		{omsapi.ReorderStatusReceived, reorderViewAll,
			nil, []string{"a approve", "o mark ordered", "d mark received", "x cancel"}},
	}
	for _, tc := range cases {
		t.Run(tc.status, func(t *testing.T) {
			fake := &reorderFake{rows: []reorderFakeRow{
				{id: 5, status: tc.status, name: "Hex bolt", sku: "HEX", quantity: 10},
			}}
			r, screen := reorderDrive(t, fake)
			cycleTo(t, r, screen, tc.view)
			bar := strings.Join(screen.barSegments(), " · ")
			for _, seg := range tc.named {
				if !strings.Contains(bar, seg) {
					t.Errorf("bar on a %s row does not name %q: %s", tc.status, seg, bar)
				}
			}
			for _, seg := range tc.unnamed {
				if strings.Contains(bar, seg) {
					t.Errorf("bar on a %s row names %q, which does nothing there: %s", tc.status, seg, bar)
				}
			}
		})
	}
}

// TestReorderQueue_AnApprovedRowOnPageTwoIsStillReachable. The list endpoint
// ignores `?status=`, so every non-pending view is the whole paged list
// narrowed here — and a client that read page one and called it "all" (which is
// what the web dashboard does) would hide exactly the rows the operator came to
// close, with no sign that it had.
func TestReorderQueue_AnApprovedRowOnPageTwoIsStillReachable(t *testing.T) {
	fake := &reorderFake{pageSize: 2}
	for i := 1; i <= 5; i++ {
		fake.rows = append(fake.rows, reorderFakeRow{
			id: i, status: omsapi.ReorderStatusCancelled,
			name: fmt.Sprintf("Filler %d", i), sku: "F", quantity: 1,
		})
	}
	fake.rows[4] = reorderFakeRow{
		id: 5, status: omsapi.ReorderStatusApproved,
		name: "Hex bolt M8x40 zinc", sku: "HEX-M8-40", quantity: 100,
	}

	r, screen := reorderDrive(t, fake)
	r = cycleTo(t, r, screen, reorderViewApproved)
	if len(screen.rows) != 1 {
		t.Fatalf("approved view has %d rows, want the one on page three", len(screen.rows))
	}

	key(t, r, reorderKey("o"))
	if got := len(fake.writesTo("mark_ordered")); got != 1 {
		t.Fatalf("a row past the first page could not be acted on: %d writes", got)
	}
}

// TestReorderQueue_ASevenDigitIdPostsItsDigits. The pk decodes into an `any`,
// and `fmt.Sprint` over a JSON number formats a float64 with %g — so a
// seven-digit request id becomes "1e+06" and the POST 404s. AGENTS.md records
// this screen as one of the two live sites of that defect; IDString is the one
// answer and this is the measurement of it.
func TestReorderQueue_ASevenDigitIdPostsItsDigits(t *testing.T) {
	fake := &reorderFake{rows: []reorderFakeRow{
		{id: 1000000, status: omsapi.ReorderStatusApproved, name: "Hex bolt", sku: "HEX", quantity: 10},
	}}
	r, screen := reorderDrive(t, fake)
	r = cycleTo(t, r, screen, reorderViewApproved)
	key(t, r, reorderKey("o"))

	writes := fake.writesTo("mark_ordered")
	if len(writes) != 1 {
		t.Fatalf("mark_ordered posted %d times", len(writes))
	}
	if got, want := writes[0].path, "/api/reorders/requests/1000000/mark_ordered/"; got != want {
		t.Fatalf("posted to %q, want %q", got, want)
	}
}

// TestReorderQueue_EveryLifecycleKeyActsExactlyWhereItIsNamed presses all four
// lifecycle keys against a row in each of the five states and holds the
// BICONDITIONAL: a key the bar names writes, and a key it does not name writes
// nothing. Reading the bar alone proves only that the legend is self-consistent;
// what an operator needs is that the legend and the requests agree.
//
// It matters here more than on most screens because OMS gates NONE of these four
// actions on a status. Every refusal is this client's, so a key that quietly
// stopped matching its legend would reach the server and be obeyed.
func TestReorderQueue_EveryLifecycleKeyActsExactlyWhereItIsNamed(t *testing.T) {
	// key -> the bar segment that names it, and the path it posts to.
	keys := map[string]struct{ segment, action string }{
		"a": {"a approve", "approve"},
		"x": {"x cancel", "cancel"},
		"o": {"o mark ordered", "mark_ordered"},
		"d": {"d mark received", "mark_received"},
	}
	views := map[string]reorderView{
		omsapi.ReorderStatusPending:   reorderViewPending,
		omsapi.ReorderStatusApproved:  reorderViewApproved,
		omsapi.ReorderStatusOrdered:   reorderViewOrdered,
		omsapi.ReorderStatusReceived:  reorderViewAll,
		omsapi.ReorderStatusCancelled: reorderViewAll,
	}
	var acted, declined int
	for status, view := range views {
		for press, want := range keys {
			t.Run(status+"/"+press, func(t *testing.T) {
				fake := &reorderFake{rows: []reorderFakeRow{
					{id: 5, status: status, name: "Hex bolt", sku: "HEX", quantity: 10},
				}}
				r, screen := reorderDrive(t, fake)
				r = cycleTo(t, r, screen, view)
				if len(screen.rows) != 1 {
					t.Fatalf("%s view has %d rows, want one", view.label(), len(screen.rows))
				}
				named := strings.Contains(strings.Join(screen.barSegments(), " · "), want.segment)

				r = key(t, r, reorderKey(press))
				// `d` only ASKS; answering it is what writes.
				if screen.confirm == reorderConfirmReceive {
					r = key(t, r, reorderKey("y"))
				}
				wrote := len(fake.writesTo(want.action)) > 0

				switch {
				case named && !wrote:
					t.Errorf("the bar names %q on a %s row and the key wrote nothing",
						want.segment, status)
				case !named && wrote:
					t.Errorf("%q wrote on a %s row the bar does not name it for", press, status)
				}
				if named {
					acted++
					return
				}
				declined++
				// A key that declines says why: on a frame with no focused box
				// and no moving highlight, a silent decline redraws the pane the
				// press before it left, which is the reported "it just hangs".
				if !strings.Contains(reorderFlat(screen.View()), "nothing") {
					t.Errorf("%q declined on a %s row in silence:\n%s", press, status, screen.View())
				}
			})
		}
	}
	if acted == 0 || declined == 0 {
		t.Fatalf("the biconditional was only tested one way: %d acting, %d declining", acted, declined)
	}
}

// TestReorderQueue_NoSecondWriteGoesOutOverTheFirst. OMS's mark_received is
// idempotent and would not double-credit stock, but nothing says the next
// action added here will be — and a bar naming a key that returns nil is the
// "it just hangs" report either way. The gate is one predicate the bar and the
// arms both read, so both halves are asserted together.
func TestReorderQueue_NoSecondWriteGoesOutOverTheFirst(t *testing.T) {
	fake := &reorderFake{rows: []reorderFakeRow{
		{id: 5, status: omsapi.ReorderStatusOrdered, name: "Hex bolt", sku: "HEX", quantity: 10},
	}}
	r, screen := reorderDrive(t, fake)
	r = cycleTo(t, r, screen, reorderViewOrdered)

	// Reached by hand rather than by a keystroke: pump drives a command to
	// completion, so no key sequence leaves a write genuinely in flight.
	screen.busyID, screen.busyAction = screen.rows[0].IDString(), "mark received"

	if bar := strings.Join(screen.barSegments(), " · "); strings.Contains(bar, "d mark received") {
		t.Errorf("the bar still names a key that refuses while a write is out: %s", bar)
	}
	for _, press := range []string{"a", "x", "o", "d"} {
		r = key(t, r, reorderKey(press))
	}
	if got := len(fake.writes); got != 0 {
		t.Errorf("%d writes went out over one already in flight: %+v", got, fake.writes)
	}
	if screen.confirm != reorderConfirmNone {
		t.Error("`d` opened a receive confirm over a receipt already in flight")
	}
	if !strings.Contains(reorderFlat(screen.View()), "still out") {
		t.Errorf("the refusal did not say why:\n%s", screen.View())
	}
}
