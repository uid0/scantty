package tui

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/uid0/scantty/internal/omsapi"
)

// The whole location count, driven through Root.Update against a stateful
// httptest fake — the shape AGENTS.md records for a screen with no shared OMS
// to reach: the real key handlers, the real frames, and assertions on the
// REQUESTS that actually left the terminal.
//
// Nothing here asserts on an internal predicate where the payload can be read
// instead. The unit rule, the reorder forecast and the all-or-nothing failure
// are all claims about what goes on the wire or what the operator can read off
// the pane, so that is where they are checked.

// reconFake is the two endpoints this screen drives, plus the scanner dispatch
// its scan path falls back to.
type reconFake struct {
	mu sync.Mutex

	grid *omsapi.LocationReconcileGrid

	// batches records every submitted payload, decoded, so a test can say which
	// unit each row claimed and whether anything was sent twice.
	batches [][]map[string]any
	// batchStatus and batchBody let a test refuse a submit the way OMS does.
	batchStatus int
	batchBody   string

	// dispatch is the scanner lookup's answer, keyed by the payload sent.
	dispatch map[string]string
	// dispatched records every code that reached the SERVER, which is how the
	// local-first claim is checked: a code the grid could place must not appear.
	dispatched []string
}

func (f *reconFake) handler(t *testing.T) http.Handler {
	t.Helper()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/reconcile/"):
			grid := f.grid
			if grid == nil {
				grid = reconGridFixture()
			}
			_ = json.NewEncoder(w).Encode(grid)
		case r.URL.Path == "/api/inventory/reconciliations/batch/":
			body, _ := io.ReadAll(r.Body)
			var payload struct {
				Rows []map[string]any `json:"rows"`
			}
			if err := json.Unmarshal(body, &payload); err != nil {
				t.Errorf("batch body did not decode: %v (%s)", err, body)
			}
			f.batches = append(f.batches, payload.Rows)
			if f.batchStatus >= 400 {
				w.WriteHeader(f.batchStatus)
				_, _ = io.WriteString(w, f.batchBody)
				return
			}
			reorders := 0
			for _, row := range payload.Rows {
				if skip, _ := row["skip_reorder"].(bool); !skip {
					if q, ok := row["actual_count"].(float64); ok && q <= 9 {
						reorders++
					}
				}
			}
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"reconciled": len(payload.Rows), "reorders_created": reorders,
				"reconciliations": []any{},
			})
		case r.URL.Path == "/api/scanner/dispatch/":
			body, _ := io.ReadAll(r.Body)
			var payload map[string]string
			_ = json.Unmarshal(body, &payload)
			code := payload["payload"]
			f.dispatched = append(f.dispatched, code)
			id, ok := f.dispatch[code]
			if !ok {
				_ = json.NewEncoder(w).Encode(map[string]any{"action": "unknown"})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"action": "navigate", "target_type": "item",
				"target_id": id, "target_name": "Scanned item",
			})
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	})
}

func (f *reconFake) lastBatch(t *testing.T) []map[string]any {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.batches) == 0 {
		t.Fatal("no batch reached the server")
	}
	return f.batches[len(f.batches)-1]
}

// reconDrive opens the count screen against the fake and pumps the grid fetch.
func reconDrive(t *testing.T, fake *reconFake) (Root, *LocationReconcileScreen) {
	t.Helper()
	srv := httptest.NewServer(fake.handler(t))
	t.Cleanup(srv.Close)
	deps := Deps{OMS: omsapi.New(srv.URL), Ctx: context.Background()}
	s := NewLocationReconcileScreen(deps, "7", "Machine shop mezzanine")
	r := newTestRoot(s)
	r.deps = deps
	sized, _ := r.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	r = sized.(Root)
	r = pump(t, r, s.Init(), 0)
	return r, s
}

// reconType sends a string into whatever box holds the caret. Typing is
// synchronous, so it needs no settling at all.
func reconType(t *testing.T, r Root, value string) Root {
	t.Helper()
	for _, ch := range value {
		next, _ := r.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{ch}})
		r = next.(Root)
	}
	return r
}

func reconCtrl(t *testing.T, r Root, typ tea.KeyType) Root {
	t.Helper()
	return key(t, r, tea.KeyMsg{Type: typ})
}

// reconPress is press() that PUMPS whatever the key kicked off.
//
// press() throws the command away, and on this screen the command IS the point:
// Enter on the review is the batch write, Enter on the scan row can be the
// server lookup, r is the grid re-read. A drive that dropped them would assert
// against a screen frozen mid-request and call it the answer.
func reconPress(t *testing.T, r Root, k string) Root {
	t.Helper()
	var msg tea.KeyMsg
	switch k {
	case "enter":
		msg = tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		msg = tea.KeyMsg{Type: tea.KeyEsc}
	case "tab":
		msg = tea.KeyMsg{Type: tea.KeyTab}
	case "up":
		msg = tea.KeyMsg{Type: tea.KeyUp}
	case "down":
		msg = tea.KeyMsg{Type: tea.KeyDown}
	case "left":
		msg = tea.KeyMsg{Type: tea.KeyLeft}
	case "right":
		msg = tea.KeyMsg{Type: tea.KeyRight}
	default:
		msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
	}
	return key(t, r, msg)
}

// TestReconcileDrive_ScanSeatsTheCursorWithNoRoundTrip is the scanner path's
// core claim: a code the grid can place is resolved LOCALLY, so the cursor is on
// the item's count box before any request could have returned.
//
// The SERVER side is asserted too, because "it worked" and "it worked without
// asking" are different facts and only the second one holds at the speed a gun
// fires.
func TestReconcileDrive_ScanSeatsTheCursorWithNoRoundTrip(t *testing.T) {
	fake := &reconFake{}
	r, s := reconDrive(t, fake)

	if s.phase != reconCount {
		t.Fatalf("phase = %v, want the count form", s.phase)
	}
	if s.focused != reconRowScan {
		t.Fatalf("the form opened on row %d, not the scan row — a scanner burst would "+
			"have gone into a count box", s.focused)
	}

	// The SKU of the THIRD row, so seating cannot pass by landing on row 1.
	r = reconType(t, r, "IPA-99-1L")
	r = reconPress(t, r, "enter")

	if want := reconRowFirstItem + 2; s.focused != want {
		t.Fatalf("after scanning IPA-99-1L the cursor is on row %d, want %d", s.focused, want)
	}
	if s.scan.Value() != "" {
		t.Fatalf("the scan box still holds %q — the next scan would append to it", s.scan.Value())
	}
	fake.mu.Lock()
	dispatched := append([]string(nil), fake.dispatched...)
	fake.mu.Unlock()
	if len(dispatched) != 0 {
		t.Fatalf("a code the grid could place was sent to the server anyway: %v", dispatched)
	}

	// And the count goes into the box the scan opened, not somewhere else.
	r = reconType(t, r, "3")
	if got := s.counts[2].Value(); got != "3" {
		t.Fatalf("row 3's box holds %q after typing 3 at the seated cursor", got)
	}
	_ = r
}

// TestReconcileDrive_ScanFallsBackToTheServerAndNamesWhatCameBack: a code the
// grid cannot place IS sent, and an item resolved to another room says so rather
// than silently doing nothing.
func TestReconcileDrive_ScanFallsBackToTheServerAndNamesWhatCameBack(t *testing.T) {
	fake := &reconFake{dispatch: map[string]string{
		// Resolves, but to an item this room does not hold.
		"ALT-CODE-9": "99999999-9999-9999-9999-999999999999",
	}}
	r, s := reconDrive(t, fake)

	r = reconType(t, r, "ALT-CODE-9")
	r = reconPress(t, r, "enter")

	fake.mu.Lock()
	dispatched := append([]string(nil), fake.dispatched...)
	fake.mu.Unlock()
	if len(dispatched) != 1 {
		t.Fatalf("dispatched = %v, want the one code the grid could not place", dispatched)
	}
	if s.focused != reconRowScan {
		t.Fatalf("the cursor moved to row %d for an item this room does not hold", s.focused)
	}
	pane := reconFlat(reconPane(t, s))
	if !strings.Contains(pane, "not stored in") {
		t.Fatalf("the frame does not say the scanned item belongs elsewhere:\n%s", pane)
	}
}

// TestReconcileDrive_TheBatchStatesEveryRowsUnit is the standing unit rule, on
// the wire, driven end to end: a case-counted row goes up as PACKS with
// at_level true, an each-counted row as base units with at_level false, and
// NEITHER omits the flag.
//
// The quantities differ by row so a payload that mixed the two would not pass by
// coincidence.
func TestReconcileDrive_TheBatchStatesEveryRowsUnit(t *testing.T) {
	fake := &reconFake{}
	r, s := reconDrive(t, fake)

	// 9 BOXES against 14 on file (1400 gloves).
	r = reconPress(t, r, "down")
	r = reconType(t, r, "9")
	// 240 BOLTS against 250 on file.
	r = reconPress(t, r, "down")
	r = reconType(t, r, "240")

	r = reconCtrl(t, r, tea.KeyCtrlR)
	if s.phase != reconReview {
		t.Fatalf("ctrl+r left the screen on %v", s.phase)
	}
	r = reconPress(t, r, "enter")

	rows := fake.lastBatch(t)
	if len(rows) != 2 {
		t.Fatalf("batch carried %d rows, want 2", len(rows))
	}
	byID := map[string]map[string]any{}
	for _, row := range rows {
		id, _ := row["item_id"].(string)
		byID[id] = row
	}
	gloves := byID["11111111-1111-1111-1111-111111111111"]
	if gloves == nil {
		t.Fatalf("the case-counted row is missing from the batch: %v", rows)
	}
	if gloves["actual_count"] != float64(9) {
		t.Fatalf("gloves actual_count = %v, want 9 (BOXES, as typed and as labelled)", gloves["actual_count"])
	}
	if gloves["at_level"] != true {
		t.Fatalf("gloves at_level = %v — 9 would be read as nine GLOVES, not nine boxes",
			gloves["at_level"])
	}
	bolts := byID["22222222-2222-2222-2222-222222222222"]
	if bolts["actual_count"] != float64(240) {
		t.Fatalf("bolts actual_count = %v, want 240", bolts["actual_count"])
	}
	atLevel, present := bolts["at_level"]
	if !present {
		t.Fatal("the each-counted row omitted at_level: the payload does not say what unit 240 is")
	}
	if atLevel != false {
		t.Fatalf("bolts at_level = %v, want false", atLevel)
	}
	// The row that was never counted is not in the batch at all: a blank box is
	// "not counted this visit", which is most of a room.
	if _, sent := byID["33333333-3333-3333-3333-333333333333"]; sent {
		t.Fatal("an uncounted row was submitted, which would write a stock figure nobody counted")
	}
	if s.phase != reconDone {
		t.Fatalf("after a landed batch the screen is on %v, want the summary", s.phase)
	}
}

// TestReconcileDrive_TheRowSaysWhichUnitItIsCountedIn is the operator-facing
// half of the same rule, read off the CLIPPED pane.
//
// Three claims, and the third is the one a base-unit-only screen fails: the box
// is labelled in the row's own unit, the on-file figure is stated in that unit,
// and the base-unit figure is NAMED beside it rather than left as a bare number.
func TestReconcileDrive_TheRowSaysWhichUnitItIsCountedIn(t *testing.T) {
	fake := &reconFake{}
	_, s := reconDrive(t, fake)
	pane := reconPane(t, s)

	for _, want := range []string{
		"boxes",             // the case-counted row's box hint
		"On file: 14 boxes", // its projection, in the unit it is counted in
		"(1400 base units)", // and the same shelf in base units, NAMED
	} {
		if !strings.Contains(pane, want) {
			t.Errorf("the count form does not carry %q:\n%s", want, pane)
		}
	}
	if strings.Contains(pane, "On file: 1400 ") {
		t.Errorf("the case-counted row states its projection in BASE units under a box "+
			"labelled in boxes:\n%s", pane)
	}
}

// TestReconcileDrive_TheReorderForecastIsShownBeforeTheWriteAndIsACeiling: the
// auto-created reorder is part of the contract, so the operator is told on the
// row as they type and again on the review — and the review's figure names its
// own limit rather than claiming a precision the grid cannot support.
func TestReconcileDrive_TheReorderForecastIsShownBeforeTheWriteAndIsACeiling(t *testing.T) {
	fake := &reconFake{}
	r, s := reconDrive(t, fake)

	// 9 boxes against a 12-box minimum.
	r = reconPress(t, r, "down")
	r = reconType(t, r, "9")
	row := reconPane(t, s)
	if !strings.Contains(row, "files a reorder") {
		t.Errorf("typing a count below the minimum does not say a reorder will be filed:\n%s", row)
	}
	if !strings.Contains(row, "minimum of 12 boxes") {
		t.Errorf("the reorder warning does not name the threshold in the row's own unit:\n%s", row)
	}

	r = reconCtrl(t, r, tea.KeyCtrlR)
	review := reconPane(t, s)
	if !strings.Contains(review, "will file a reorder request") {
		t.Errorf("the review does not say submitting files reorder requests:\n%s", review)
	}
	if !strings.Contains(review, "At most") {
		t.Errorf("the review states the reorder count as a fact:\n%s", review)
	}
	// The QUALIFIER survives the fold whole, not just the words "At most". The
	// note block folds from the tail, so a reason that is cut is a claim left
	// standing without the thing that qualifies it.
	review = reconFlat(review)
	if !strings.Contains(review, "retired item files none") {
		t.Errorf("the review does not say WHY the forecast is only a ceiling:\n%s", review)
	}
	if !strings.Contains(review, "all land or none do") {
		t.Errorf("the review does not say the write is all-or-nothing:\n%s", review)
	}
	if n := s.reordersForecast(); n != 1 {
		t.Fatalf("forecast = %d, want 1", n)
	}
}

// TestReconcileDrive_SkipReorderSuppressesItOnTheWireAndSaysSo drives the row
// detail: the flag reaches the payload, and the grid row says so afterwards.
func TestReconcileDrive_SkipReorderSuppressesItOnTheWireAndSaysSo(t *testing.T) {
	fake := &reconFake{}
	r, s := reconDrive(t, fake)

	r = reconPress(t, r, "down")
	r = reconType(t, r, "9")
	r = reconCtrl(t, r, tea.KeyCtrlE)
	if s.phase != reconRow {
		t.Fatalf("ctrl+e left the screen on %v", s.phase)
	}
	// Reason first, then the reorder row.
	r = reconPress(t, r, "right") // reason: one step off the default
	r = reconPress(t, r, "down")
	r = reconPress(t, r, "right") // reorder: File if low -> Skip
	r = reconPress(t, r, "down")
	r = reconType(t, r, "box was open")
	r = reconPress(t, r, "esc")

	if s.phase != reconCount {
		t.Fatalf("esc left the row sheet on %v", s.phase)
	}
	if !s.rows[0].skipReorder {
		t.Fatal("the reorder row did not take")
	}
	if s.rows[0].notes != "box was open" {
		t.Fatalf("notes = %q — esc off the row sheet discarded what was typed", s.rows[0].notes)
	}
	pane := reconPane(t, s)
	if !strings.Contains(pane, "no reorder") {
		t.Errorf("the grid row does not show the override, so it is invisible:\n%s", pane)
	}
	if s.reordersForecast() != 0 {
		t.Fatal("the forecast still counts a row set to file no reorder")
	}

	r = reconCtrl(t, r, tea.KeyCtrlR)
	r = reconPress(t, r, "enter")
	sent := fake.lastBatch(t)[0]
	if sent["skip_reorder"] != true {
		t.Fatalf("skip_reorder = %v on the wire", sent["skip_reorder"])
	}
	if sent["notes"] != "box was open" {
		t.Fatalf("notes = %v on the wire", sent["notes"])
	}
	if sent["reason"] == cycleCountReasons[reconDefaultReasonIx()].Value {
		t.Fatal("the reason override did not reach the wire")
	}
}

// TestReconcileDrive_AFailedSubmitKeepsEveryTypedCount is the claim a room count
// lives or dies by. The batch is all-or-nothing server-side, so a refusal has
// consumed nothing — and the screen must not consume anything either.
func TestReconcileDrive_AFailedSubmitKeepsEveryTypedCount(t *testing.T) {
	fake := &reconFake{
		batchStatus: http.StatusForbidden,
		batchBody:   `{"detail": "You do not have permission to reconcile item Nitrile gloves, powder-free, blue, medium."}`,
	}
	r, s := reconDrive(t, fake)

	r = reconPress(t, r, "down")
	r = reconType(t, r, "9")
	r = reconPress(t, r, "down")
	r = reconType(t, r, "240")
	r = reconCtrl(t, r, tea.KeyCtrlE)
	r = reconPress(t, r, "down")
	r = reconPress(t, r, "down")
	r = reconType(t, r, "shelf was restacked")
	r = reconPress(t, r, "esc")

	r = reconCtrl(t, r, tea.KeyCtrlR)
	r = reconPress(t, r, "enter")

	if s.pending {
		t.Fatal("the screen is still frozen after the refusal answered")
	}
	if s.phase != reconReview {
		t.Fatalf("a failed submit moved the screen to %v — the operator's work is on the "+
			"review frame and that is where the failure has to be answerable", s.phase)
	}
	if got := s.counts[0].Value(); got != "9" {
		t.Fatalf("the first count is now %q; a refusal that consumed nothing must keep everything", got)
	}
	if got := s.counts[1].Value(); got != "240" {
		t.Fatalf("the second count is now %q", got)
	}
	if s.rows[1].notes != "shelf was restacked" {
		t.Fatalf("the note is now %q", s.rows[1].notes)
	}

	pane := reconPane(t, s)
	if !strings.Contains(pane, "do not have permission") {
		t.Errorf("the server's own sentence did not reach the operator — they are reading "+
			"raw JSON or nothing at all:\n%s", pane)
	}

	// And the retry is one keypress away, with the same rows.
	fake.mu.Lock()
	fake.batchStatus, fake.batchBody = 0, ""
	fake.mu.Unlock()
	r = reconPress(t, r, "enter")
	if s.phase != reconDone {
		t.Fatalf("the retry left the screen on %v", s.phase)
	}
	fake.mu.Lock()
	n := len(fake.batches)
	fake.mu.Unlock()
	if n != 2 {
		t.Fatalf("%d batches were sent; the refused one must not have written anything", n)
	}
	if rows := fake.lastBatch(t); len(rows) != 2 {
		t.Fatalf("the retry sent %d rows, want the same 2", len(rows))
	}
}

// TestReconcileDrive_AnOpenClosedRowCarriesItsOpenTallyAndNoOtherRowDoes: the
// open tally is accepted only for an item counted open/closed, so sending it
// anywhere else would refuse the WHOLE batch.
func TestReconcileDrive_AnOpenClosedRowCarriesItsOpenTallyAndNoOtherRowDoes(t *testing.T) {
	fake := &reconFake{}
	r, s := reconDrive(t, fake)

	// The open/closed row is the third.
	r = reconPress(t, r, "down")
	r = reconPress(t, r, "down")
	r = reconPress(t, r, "down")
	r = reconType(t, r, "3")
	r = reconCtrl(t, r, tea.KeyCtrlE)
	if s.rowFields() != reconFieldOpen+1 {
		t.Fatalf("the open/closed row's sheet has %d fields; it must offer the open tally",
			s.rowFields())
	}
	r = reconPress(t, r, "down")
	r = reconPress(t, r, "down")
	r = reconPress(t, r, "down") // onto the open tally
	r = reconType(t, r, "2")
	r = reconPress(t, r, "enter")

	// And an each-counted row beside it, whose sheet has no such field.
	r = reconPress(t, r, "up")
	r = reconType(t, r, "240")

	r = reconCtrl(t, r, tea.KeyCtrlR)
	r = reconPress(t, r, "enter")

	rows := fake.lastBatch(t)
	for _, row := range rows {
		id, _ := row["item_id"].(string)
		open, present := row["open_count"]
		switch id {
		case "33333333-3333-3333-3333-333333333333":
			if !present || open != float64(2) {
				t.Fatalf("the open/closed row's open_count = %v (present=%v), want 2", open, present)
			}
		default:
			if present {
				t.Fatalf("row %s sent open_count = %v; OMS refuses it for an item that is "+
					"not counted open/closed, which would refuse the whole batch", id, open)
			}
		}
	}
}

func TestReconcileDrive_InvalidOpenTallyNeverReachesTheServer(t *testing.T) {
	fake := &reconFake{}
	r, s := reconDrive(t, fake)

	r = reconPress(t, r, "down")
	r = reconPress(t, r, "down")
	r = reconPress(t, r, "down")
	r = reconType(t, r, "3")
	r = reconCtrl(t, r, tea.KeyCtrlE)
	r = reconPress(t, r, "down")
	r = reconPress(t, r, "down")
	r = reconPress(t, r, "down")
	r = reconType(t, r, "2x")
	r = reconPress(t, r, "enter")

	pane := reconPane(t, s)
	if !strings.Contains(pane, "!") || !strings.Contains(reconFlat(pane), "not a whole open tally") {
		t.Fatalf("the invalid open tally is not marked and explained before review:\n%s", pane)
	}

	r = reconCtrl(t, r, tea.KeyCtrlR)
	if s.phase != reconCount {
		t.Fatalf("invalid open tally opened phase %v, want count", s.phase)
	}
	if pane = reconPane(t, s); !strings.Contains(reconFlat(pane), "open tally on row(s) 3 is not a whole count") {
		t.Fatalf("the refusal does not locate the invalid open tally:\n%s", pane)
	}
	fake.mu.Lock()
	n := len(fake.batches)
	fake.mu.Unlock()
	if n != 0 {
		t.Fatalf("invalid open tally sent %d batch requests, want none", n)
	}
}

// TestReconcileDrive_ARoomWithNothingInItSaysSoAndOffersAWayOn keeps the two
// blocked facts apart: "nothing is stored here" is not "we could not read it".
func TestReconcileDrive_ARoomWithNothingInItSaysSoAndOffersAWayOn(t *testing.T) {
	fake := &reconFake{grid: &omsapi.LocationReconcileGrid{
		LocationID: "7", LocationName: "Machine shop mezzanine",
	}}
	_, s := reconDrive(t, fake)
	if s.phase != reconBlocked {
		t.Fatalf("an empty room left the screen on %v", s.phase)
	}
	pane := reconPane(t, s)
	if !strings.Contains(pane, "No active items are stored") {
		t.Errorf("the empty room does not say it is empty:\n%s", pane)
	}
	if strings.Contains(pane, "could not be read") {
		t.Errorf("an empty room is reported as an unreadable one:\n%s", pane)
	}
	if !strings.Contains(pane, "r=Re-read") {
		t.Errorf("the blocked frame names no way on:\n%s", pane)
	}
}

// reconFlat collapses a drawn pane's whitespace so a sentence that FOLDED
// across lines can still be matched whole.
//
// It is the right instrument for PROSE and the wrong one for a claim about a
// row: a folded row reads as present here even where the operator sees it
// split. Every assertion that uses it is about a sentence surviving the fold,
// never about a row fitting.
func reconFlat(pane string) string {
	return strings.Join(strings.Fields(pane), " ")
}

// reconPane is the screen's own frame as the terminal really draws it: clipped
// to the PANE Root gives it at 80 columns, which is where the cut this project
// checks against actually falls.
//
// Clipped rather than raw, because clampToBox truncates in Root.View() and not
// in the screen — so a test reading screen.View() passes while the terminal
// shows a cut line. The screen's pane rather than Root's whole frame, because
// the nav column sits to the left of every body row: flattened for a prose
// assertion, Root's frame interleaves sidebar entries into the sentence.
func reconPane(t *testing.T, s *LocationReconcileScreen) string {
	t.Helper()
	return clampToBox(s.View(), screenBodyWidth(80), screenBodyRows(24))
}

// TestReconcileEntry_TheLocationDetailOffersTheCountAndNamesIt holds the way IN.
//
// Both halves, because each fails on its own: a key that works and is not named
// is a feature nobody finds, and a name with no key behind it is the bar-honesty
// rule broken on the surface an operator reads first.
func TestReconcileEntry_TheLocationDetailOffersTheCountAndNamesIt(t *testing.T) {
	s := NewLocationDetailScreen(Deps{}, "7")
	s.Update(locationDetailLoadedMsg{loc: &omsapi.Location{
		ID: 7, Name: "Machine shop mezzanine", IsActive: true,
	}})

	if !strings.Contains(s.View(), "c count") {
		t.Errorf("the location detail's footer does not name the count key:\n%s", s.View())
	}

	next, cmd := s.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}})
	if next != Screen(s) {
		t.Fatalf("c replaced the screen with %T instead of switching", next)
	}
	if cmd == nil {
		t.Fatal("c on a loaded location did nothing")
	}
	msg := cmd()
	switched, ok := msg.(SwitchScreenMsg)
	if !ok {
		t.Fatalf("c produced %T, want a screen switch", msg)
	}
	recon, ok := switched.Screen.(*LocationReconcileScreen)
	if !ok {
		t.Fatalf("c opened %T, want the location count", switched.Screen)
	}
	if recon.locID != "7" {
		t.Fatalf("the count opened on location %q", recon.locID)
	}
	// The room's NAME is carried across, so the loading frame can say which room
	// it is reading rather than "location 7".
	if recon.locName != "Machine shop mezzanine" {
		t.Fatalf("the count opened without the room's name: %q", recon.locName)
	}
}

// TestReconcileEntry_TheCountKeyReachesTheScreenThroughRoot is the other half of
// the entry point: `c` has to survive Root's own key layer.
//
// Asserted through a real Root rather than off the screen's Update, because the
// root is where a global accelerator would have claimed the letter first — which
// is exactly how keys in this program have been shadowed before.
func TestReconcileEntry_TheCountKeyReachesTheScreenThroughRoot(t *testing.T) {
	s := NewLocationDetailScreen(Deps{}, "7")
	s.Update(locationDetailLoadedMsg{loc: &omsapi.Location{
		ID: 7, Name: "Machine shop mezzanine", IsActive: true,
	}})
	r := newTestRoot(s)
	r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}})
	if _, ok := r.screen.(*LocationReconcileScreen); !ok {
		t.Fatalf("c through Root left the screen as %T", r.screen)
	}
}

// TestReconcileEntry_TheLocationDetailFooterFitsThePane: the legend the count
// key was added to is 69 cells and the pane gives 51, so every segment has to
// survive the clip — on its own line where it must.
//
// Measured against the CLIPPED pane, because clampToBox truncates in Root.View()
// and not in the screen: a check on screen.View() passes while the terminal
// shows a cut line.
func TestReconcileEntry_TheLocationDetailFooterFitsThePane(t *testing.T) {
	s := NewLocationDetailScreen(Deps{}, "7")
	s.Update(locationDetailLoadedMsg{loc: &omsapi.Location{
		ID: 7, Name: "Machine shop mezzanine", IsActive: true,
	}})
	pane := clampToBox(s.View(), screenBodyWidth(80), 60)
	for _, want := range []string{
		"c count", "p problems", "f fixtures", "g gen-QR", "E edit", "x delete", "r refresh", "esc back",
	} {
		if !strings.Contains(pane, want) {
			t.Errorf("the clipped pane lost %q from the footer:\n%s", want, pane)
		}
	}
}

// TestReconcile_AWideTerminalDrawsMoreOfTheRowAndNeverLess holds the wide half
// of the layout rule: extra columns are ADDITIVE, so a 120-column terminal shows
// more of a name than an 80-column one and nothing the 80-column layout draws is
// traded away to get it.
//
// Both directions, because each fails on its own. A row clipped to a FIXED 51
// cells would draw abbreviated with forty columns of pane left blank; a row with
// no bound at all would overrun the narrow pane and clampToBox would take the
// tail with no mark.
func TestReconcile_AWideTerminalDrawsMoreOfTheRowAndNeverLess(t *testing.T) {
	widths := []int{80, 100, 120}
	rows := map[int]string{}
	for _, w := range widths {
		s := reconFixture(nil)
		s.Update(tea.WindowSizeMsg{Width: w, Height: 30})
		pane := clampToBox(s.View(), screenBodyWidth(w), screenBodyRows(30))
		var found string
		for _, line := range strings.Split(pane, "\n") {
			if strings.Contains(line, "Nitrile gloves") {
				found = line
				break
			}
		}
		if found == "" {
			t.Fatalf("no item row at width %d:\n%s", w, pane)
		}
		if got := lipgloss.Width(found); got > screenBodyWidth(w) {
			t.Errorf("the item row is %d cells against a %d-cell pane at width %d: %q",
				got, screenBodyWidth(w), w, found)
		}
		rows[w] = found
	}
	// The NAME is what grows. At 80 it is cut and MARKED; by 120 the whole of it
	// is drawn, which is the point of reading the live pane rather than a
	// constant.
	if !strings.Contains(rows[80], "…") {
		t.Errorf("the 80-column row is not marked as cut: %q", rows[80])
	}
	if !strings.Contains(rows[120], "Nitrile gloves, powder-free, blue, medium") {
		t.Errorf("a 120-column terminal still abbreviates the name: %q", rows[120])
	}
	if lipgloss.Width(rows[120]) <= lipgloss.Width(rows[80]) {
		t.Errorf("the row does not grow with the pane: 80=%q 120=%q", rows[80], rows[120])
	}
}

// TestReconcile_AGatewayPageRefusalStaysOneMarkedLineAndKeepsTheBar is the
// refusal-legibility rule under the worst body the wire can produce.
//
// omsapi.parseError puts the ENTIRE raw response into APIError.Message whenever
// the JSON envelope carries no code, so an nginx 502 arrives here as seven lines
// of HTML. The status row is ONE unwrapped line of the frame: unflattened it
// pushes the frame six rows over, and clampToBox drops from the BOTTOM — so what
// goes is the whole action bar, every key on the screen unnamed at once, with
// the operator staring at markup.
func TestReconcile_AGatewayPageRefusalStaysOneMarkedLineAndKeepsTheBar(t *testing.T) {
	gateway := "<html>\r\n<head><title>502 Bad Gateway</title></head>\r\n<body>\r\n" +
		"<center><h1>502 Bad Gateway</h1></center>\r\n<hr><center>nginx</center>\r\n</body>\r\n</html>\r\n"
	fake := &reconFake{batchStatus: http.StatusBadGateway, batchBody: gateway}
	r, s := reconDrive(t, fake)

	r = reconPress(t, r, "down")
	r = reconType(t, r, "9")
	r = reconCtrl(t, r, tea.KeyCtrlR)
	r = reconPress(t, r, "enter")

	for _, size := range [][2]int{{80, 24}, {80, 14}, {120, 30}} {
		w, h := size[0], size[1]
		sized, _ := r.Update(tea.WindowSizeMsg{Width: w, Height: h})
		r = sized.(Root)
		pane := clampToBox(s.View(), screenBodyWidth(w), screenBodyRows(h))
		lines := strings.Split(pane, "\n")

		var marked int
		for _, line := range lines {
			if strings.Contains(line, "✗") {
				marked++
			}
			if lipgloss.Width(line) > screenBodyWidth(w) {
				t.Errorf("at %dx%d a frame line is %d cells against a %d-cell pane: %q",
					w, h, lipgloss.Width(line), screenBodyWidth(w), line)
			}
		}
		if marked != 1 {
			t.Errorf("at %dx%d the refusal is on %d marked lines, want exactly one:\n%s",
				w, h, marked, pane)
		}
		// The bar SURVIVES, which is the half a six-row status row destroys.
		if !strings.Contains(pane, "Enter=Submit") {
			t.Errorf("at %dx%d the action bar is gone under the gateway page:\n%s", w, h, pane)
		}
		// And the counts are still there to retry with.
		if s.counts[0].Value() != "9" {
			t.Errorf("at %dx%d a 502 consumed the typed count", w, h)
		}
	}
}
