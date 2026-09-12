// LocationReconcileScreen — counting a whole LOCATION from the terminal:
// walk the room with a scanner, put a number against every item on the shelf,
// and submit the lot in one write.
//
// It is the terminal twin of the web's /inventory/locations/:id/reconcile
// (frontend/src/pages/InventoryReconciliationPage.tsx), and it is the workflow
// this program exists for: counting a room is a scanner-and-keyboard job, and
// until now the terminal could only count ONE ITEM AT A TIME through the
// per-item cycle count on the inventory detail. That cycle count is untouched —
// it reaches a different endpoint and it is the right instrument for a single
// recount — and this is the room-scale one beside it.
//
// It renders through jde_form.go's shared columnar layer exactly as
// receive_form.go and po_edit.go do: one shared label column, a body the
// operator pages, a pinned answer to the last keypress, and an action bar
// naming exactly the keys that act in the state being drawn. There is no local
// copy of the scroll arithmetic, the status bound or the field layout.
//
// # THE SCANNER IS THE PRIMARY INSTRUMENT, WHICH IS WHY THE SCAN ROW LEADS
//
// The form opens with the cursor on the Scan box and comes BACK to it after
// every count, so the rhythm is the operator's own: scan the shelf label, type
// what is there, Enter, scan the next. Finding an item by arrow-keying down a
// list of everything in the room is the fallback, not the design — a store room
// holds hundreds of SKUs and the list is alphabetical, not shelf-ordered.
//
// A scan is resolved LOCALLY FIRST (findItem): the grid already carries every
// item's SKU and id, so the common case — a barcode that is the SKU — seats the
// cursor with no round trip at all, which is what makes the rhythm hold at the
// speed a gun fires. Only a code the grid cannot place is sent to OMS's own
// scanner dispatch, the same resolver the web page uses, and an item it
// resolves that is NOT stored here says so rather than silently doing nothing.
//
// # WHICH UNIT A NUMBER IS, SAID ON EVERY ROW AND IN EVERY PAYLOAD
//
// Stock is COUNTED in individual base units; a purchase order is PLACED in
// cases. Both are correct, neither is unified, and this screen is the one where
// confusing them is a silently wrong stock level rather than a visible mistake.
//
// So the unit is never inferred here and never left implicit:
//
//   - The SERVER says which unit each row is counted in. The grid payload
//     carries `count_unit` and `projected_at_unit` for exactly that purpose
//     (OMS op-ev14, and its serializer docstring says so), and this screen
//     draws both — the box's hint is the unit noun, and the row's on-file
//     figure is stated in the same unit with the base-unit figure named beside
//     it whenever the two differ.
//   - The PAYLOAD states it too. Every row sends `at_level` whether it is true
//     or false (omsapi.ReconciliationRow), so a recorded request can never say
//     what was counted without saying what it was counted in.
//   - The MINIMUM is in that same unit, which is why the reorder forecast below
//     compares the typed count directly against it. `minimum_stock` is re-read
//     as a threshold in the item's counting rung for every pack-counting mode.
//
// The one thing the web page does NOT do is use those fields: it draws
// `projected` (base units) unlabelled and compares a typed number against
// `minimum_stock` without asking which unit either is in, so on a case-counted
// item its own "will reorder" hint is wrong by the pack size. This screen is
// not a transcription of that.
//
// # THE REORDER SIDE EFFECT IS PART OF THE CONTRACT, NOT A SURPRISE
//
// Submitting auto-creates a ReorderRequest for every row counted at or below
// its minimum. That is the workflow, not a side effect, so the operator is told
// twice before it happens: on the row, as the number is typed, and again on the
// review frame as a total they have to pass through to submit.
//
// The forecast is HONEST ABOUT ITS OWN LIMIT. OMS also declines to file for a
// RETIRED item, and `is_retired` is not on the reconcile grid — so the number
// this screen names is a CEILING and says so (reconReorderCeiling). Inventing
// the missing fact, or quietly dropping the caveat, would both be worse than
// naming it.
//
// # A FAILED SUBMIT KEEPS EVERY TYPED COUNT
//
// The batch is ALL OR NOTHING server-side: OMS resolves every item and checks
// permission on all of them before it opens a transaction, then applies the
// rows inside it. So there is no partial landing to report and nothing to
// reconcile — and a refusal is guaranteed to have consumed nothing.
//
// This screen is built on that: a failed submit leaves the operator ON THE
// REVIEW frame with every count, reason, note and skip flag exactly as they
// typed them, the server's own sentence on the status row and its detail folded
// under it, and Ctrl+E one press away back to the grid to fix the row it named.
// Nothing is cleared, nothing is re-entered. A room count is an hour of work;
// losing it to a 502 is not an acceptable failure mode.
//
// # The key scheme is the columnar one
//
//	Enter          FIND on the scan row; from a count box, back to the scan row
//	               (the scanner rhythm); SUBMIT on the review
//	Up/Down        move between rows
//	Tab/Shift-Tab  the same, on the phases WITH fields
//	PgUp/PgDn      page, when the body is taller than the pane
//	Ctrl+E         open the row's reason / notes / skip / open-tally detail —
//	               and, from the review, back to the grid
//	Ctrl+R         review what will be submitted
//	←→             change a choice, on the row detail's choice rows
//	r              re-read the grid (the blocked frame, and the summary)
//	Esc            back to the location (and the bar says when that DISCARDS)
//
//	Up/Down also SCROLL the three read-only frames (loading, blocked, done),
//	which own no navigable row at all and are drawn with an offset rather than a
//	cursor-anchored window — see scrolledPhase.
//
// location_reconcile_drive_test.go drives the whole flow through Root.Update
// and asserts the REQUESTS actually sent; location_reconcile_sweep_test.go
// presses the whole key space at every phase, with the phase list derived from
// the iota and the state fingerprint derived by reflect over the screen struct.
package tui

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/uid0/scantty/internal/omsapi"
	"github.com/uid0/scantty/internal/scanner"
)

// reconPhase tracks the stages of the count.
type reconPhase int

const (
	// reconLoading is the grid fetch. A phase rather than a flag because it has
	// a bar of its own and a body naming the work and its subject.
	reconLoading reconPhase = iota
	// reconBlocked is "no count can be entered here", and it covers TWO facts
	// that must not be collapsed: the fetch failed (could not tell what is in
	// this room), or it succeeded and the room holds no active items (there is
	// nothing to count). blockedBody is where the split lives.
	reconBlocked
	// reconCount is the grid itself: the scan row, then one count box per item.
	reconCount
	// reconRow is one row's reason, notes, skip-reorder flag and — for an
	// open/closed item — its open-container tally.
	//
	// A phase of its own rather than more rows on the grid, and the reason is
	// the pane rather than taste. Four more fields per item would put a
	// 24-item room at a hundred navigable rows and leave the count boxes —
	// the thing the operator is actually here to fill — scattered between
	// them. The common row needs none of these: OMS's own default reason is
	// what a routine recount is, and the grid marks a row that has been given
	// anything else so the override is never invisible.
	reconRow
	// reconReview is the read-only list of what will be submitted, the reorder
	// forecast, and the last frame before the write.
	reconReview
	// reconDone is the summary of a landed batch.
	reconDone
	// reconPhaseCount is the sentinel the sweep walks to, so a phase added above
	// it is pressed by the key space the day it is written rather than the day
	// somebody remembers to extend a table.
	reconPhaseCount
)

func (p reconPhase) String() string {
	switch p {
	case reconLoading:
		return "loading"
	case reconBlocked:
		return "blocked"
	case reconCount:
		return "count"
	case reconRow:
		return "row"
	case reconReview:
		return "review"
	case reconDone:
		return "done"
	}
	return fmt.Sprintf("reconPhase(%d)", int(p))
}

// The fixed rows of the count form, ahead of the per-item count boxes.
//
// SCAN LEADS, and on this screen that is not a preference. A barcode scanner is
// a keyboard that fires a burst the moment a label is put under it, and a burst
// has to land somewhere it means something: with the cursor in a COUNT box the
// first digits of a scanned code would become a stock figure, which is the one
// wrong number on this screen nobody would catch.
const (
	reconRowScan = iota
	reconRowFirstItem
)

// The fields of the row detail, in the order an operator fills them.
//
// Reason leads because it is the only one of the four that is always recorded —
// every batch row carries one — and skip-reorder follows it because the two
// together are what decide whether this row files a purchase request. The free
// text and the open tally come after the decisions.
const (
	reconFieldReason = iota
	reconFieldSkip
	reconFieldNotes
	// reconFieldOpen exists only for an open/closed item; rowFields() is the
	// one place that is decided, so the cursor bound, the bar and the body
	// cannot come to different views of how many rows the sheet has.
	reconFieldOpen
)

// reconLabels is this screen's label column, in ONE place so every phase hangs
// off the same leader — the columnar rule that a value never moves sideways when
// the frame changes under it.
//
// It is EXACTLY the labels this screen draws and no more, in both directions. A
// label the list does not know is CLIPPED on the row it is drawn on, because
// jdeLabelWidth sizes the column from this set; and a label listed but never
// drawn widens the column for every row that IS drawn, which on a narrow pane
// costs the value the room it was going to be cut by.
var reconLabels = []string{
	"Scan", "Count", "Reason", "Reorder", "Notes", "Open",
}

func reconLabelWidth() int {
	fields := make([]jdeField, len(reconLabels))
	for i, l := range reconLabels {
		fields[i] = jdeField{Label: l}
	}
	return jdeLabelWidth(fields)
}

// reconMetaIndent lines a row's caveats up under its NAME rather than under the
// leader column: they are a continuation of the row above them, not a value
// hanging off a label.
const reconMetaIndent = jdeIndent + "   "

// reconRowState is what the operator has decided about one item, held parallel
// to the grid's own rows.
//
// The typed COUNT is not here: it lives in its textinput, which is the box the
// layer draws and bounds. Everything here is what the row detail sets, and all
// of it survives every phase change and every failed submit — see the file note
// on why that is the whole point.
type reconRowState struct {
	reasonIx    int
	notes       string
	skipReorder bool
	// openCount is the open-container tally as TYPED, kept as text so a blank
	// ("leave the stored tally alone") and a zero ("there are none open") stay
	// different facts all the way to the payload.
	openCount string
}

// reconDefaultReasonIx is the reason a row starts on. It is the same default
// the per-item cycle count and the web grid both use, and it is the honest one
// for a routine recount: the count on file was wrong.
func reconDefaultReasonIx() int { return defaultCycleCountReasonIx() }

type LocationReconcileScreen struct {
	deps Deps
	// jdeScreen carries the pane geometry and the framing, embedded rather than
	// copied so bodyWidth(), statusRow() and frameWrapped() read here exactly as
	// they do on every other converted sheet.
	jdeScreen

	locID string
	// locName is what the caller already knew, so the title and the working
	// line can name the room before the grid lands. The grid's own name wins
	// once it arrives — it is the fresher read of the same fact.
	locName string

	grid  *omsapi.LocationReconcileGrid
	items []omsapi.LocationReconcileItem
	rows  []reconRowState

	phase   reconPhase
	loading bool
	pending bool

	scan   textinput.Model
	counts []textinput.Model
	// focused is the count form's cursor: reconRowScan, or reconRowFirstItem+i.
	focused int

	// The row detail (reconRow). rowIdx indexes items/rows/counts; rowField is
	// the cursor within the sheet.
	rowIdx int
	// fieldCursor is the row sheet's cursor over its fields. It is NAMED for
	// what it holds — the operator's place — because the derived sweeps
	// fingerprint a screen's position off its int field NAMES (jdePlaceWords),
	// so a cursor called anything else is a cursor no sweep can see move.
	fieldCursor int
	notes       textinput.Model
	open        textinput.Model

	// reviewCursor is the read-only review list's own cursor.
	reviewCursor int

	// sheetOffset is the scroll offset of the three READ-ONLY frames — loading,
	// blocked and done. They own no navigable row at all, and a body with no
	// row is PINNED on a cursor-anchored frame: jdeLines.block() answers (0,0)
	// for a row nothing owns, so everything past the window's last line would be
	// unreachable while the frame went on drawing "↓ N more below". An offset
	// and frameScrolled is the shape that answers it, and it is what the
	// purchase-order pad and the add-line confirm already use.
	sheetOffset int

	// result is the batch as the SERVER answered it, written only by a reply
	// off the wire.
	result *omsapi.ReconciliationBatchResult

	// failHead / failDetail are the failure line's two halves, always written
	// together by setFail so a stale detail can never be drawn under a fresh
	// headline. The headline goes on the status row, which cannot fold; the
	// detail is folded and bounded in the header, because omsapi.parseError puts
	// the ENTIRE raw response body into APIError.Message whenever the JSON
	// envelope carries no code.
	failHead   string
	failDetail string

	// note is the screen's answer to the last keypress, drawn in the pinned
	// header. The status bar carries the same words, but a flash expires after
	// four seconds and the operator who pressed a key and saw nothing is still
	// looking.
	note pickerNote

	// parked is the box currentInput answers with on a phase that holds none.
	// It is never drawn and never read; it exists so "who owns the caret?" is
	// TOTAL over the phases rather than a nil plus a guard at every call site.
	parked textinput.Model
}

type reconGridMsg struct {
	grid *omsapi.LocationReconcileGrid
	err  error
}

type reconSubmittedMsg struct {
	result *omsapi.ReconciliationBatchResult
	err    error
}

// reconScanMsg is the server's answer to a code the grid could not place
// locally. code is echoed so a stale reply cannot be read as an answer about a
// different scan.
type reconScanMsg struct {
	code   string
	result *omsapi.LookupResult
	err    error
}

func NewLocationReconcileScreen(deps Deps, locationID, locationName string) *LocationReconcileScreen {
	s := &LocationReconcileScreen{
		deps:    deps,
		locID:   strings.TrimSpace(locationID),
		locName: strings.TrimSpace(locationName),
		phase:   reconLoading,
		loading: true,
	}
	// None of these boxes carries a Width: the width is the LAYER's. A text row
	// hands it the box (jdeField.Input) and jdeFitInputValue sizes a copy
	// against the input area the row was given, so the value scrolls and the
	// caret is always on the pane. A width fixed here would be a bound computed
	// against a terminal this screen has not been told about.
	s.scan = textinput.New()
	s.scan.Prompt = ""
	s.scan.CharLimit = 64
	s.notes = textinput.New()
	s.notes.Prompt = ""
	s.notes.CharLimit = 200
	s.open = textinput.New()
	s.open.Prompt = ""
	s.open.CharLimit = 9
	return s
}

func (s *LocationReconcileScreen) Title() string {
	if name := s.locationName(); name != "" {
		return "Count: " + name
	}
	return "Count location"
}

// WantsRawInput keeps every keypress on this screen. A scanner gun can be
// configured to emit TAB as a field separator, and tab is the root's key for the
// sidebar menu — so a scan would jump the operator into the menu mid-code.
func (s *LocationReconcileScreen) WantsRawInput() bool { return true }

func (s *LocationReconcileScreen) ctx() context.Context {
	if s.deps.Ctx != nil {
		return s.deps.Ctx
	}
	return context.Background()
}

func (s *LocationReconcileScreen) Init() tea.Cmd { return s.loadGrid() }

func (s *LocationReconcileScreen) loadGrid() tea.Cmd {
	deps, ctx, id := s.deps, s.ctx(), s.locID
	if deps.OMS == nil {
		return func() tea.Msg {
			return reconGridMsg{err: fmt.Errorf("no OMS client configured")}
		}
	}
	return func() tea.Msg {
		grid, err := deps.OMS.GetLocationReconcileGrid(ctx, id)
		return reconGridMsg{grid: grid, err: err}
	}
}

// locationName is the room as the frames name it. The GRID's name wins because
// it is the fresher read; the caller's is what lets the loading frame say which
// room it is fetching.
func (s *LocationReconcileScreen) locationName() string {
	if s.grid != nil && strings.TrimSpace(s.grid.LocationName) != "" {
		return s.grid.LocationName
	}
	if s.locName != "" {
		return s.locName
	}
	if s.locID != "" {
		return "location " + s.locID
	}
	return ""
}

func (s *LocationReconcileScreen) paneWidth() int {
	if w := s.bodyWidth(); w > 0 {
		return w
	}
	return screenBodyWidth(80)
}

// ---------------------------------------------------------------------------
// Rows, boxes and the caret
// ---------------------------------------------------------------------------

// countRows is the number of navigable rows on the count form: the scan row
// plus one per item.
func (s *LocationReconcileScreen) countRows() int { return reconRowFirstItem + len(s.items) }

// itemAt maps a count-form cursor row to an item index.
func (s *LocationReconcileScreen) itemAt(row int) (int, bool) {
	i := row - reconRowFirstItem
	if i < 0 || i >= len(s.items) {
		return 0, false
	}
	return i, true
}

// rowFields is how many fields the row detail has for the item it is open on.
// The open tally exists only where the item is counted open/closed, and this is
// the ONE place that is decided — the cursor bound, the bar and the body all
// ask it, so they cannot disagree about the sheet's shape.
func (s *LocationReconcileScreen) rowFields() int {
	if i, ok := s.itemAt(reconRowFirstItem + s.rowIdx); ok && s.items[i].CountMode == omsapi.CountModeOpenClosed {
		return reconFieldOpen + 1
	}
	return reconFieldOpen
}

// currentInput is the box holding the caret, TOTAL over the phases.
func (s *LocationReconcileScreen) currentInput() *textinput.Model {
	switch s.phase {
	case reconCount:
		if s.focused == reconRowScan {
			return &s.scan
		}
		if i, ok := s.itemAt(s.focused); ok {
			return &s.counts[i]
		}
	case reconRow:
		switch s.fieldCursor {
		case reconFieldNotes:
			return &s.notes
		case reconFieldOpen:
			return &s.open
		}
	}
	return &s.parked
}

func (s *LocationReconcileScreen) allBoxes() []*textinput.Model {
	out := []*textinput.Model{&s.scan, &s.notes, &s.open, &s.parked}
	for i := range s.counts {
		out = append(out, &s.counts[i])
	}
	return out
}

func (s *LocationReconcileScreen) blurAll() {
	for _, b := range s.allBoxes() {
		b.Blur()
	}
}

func (s *LocationReconcileScreen) focusCurrent() {
	s.blurAll()
	if s.pending || s.loading {
		return
	}
	box := s.currentInput()
	if box == &s.parked {
		return
	}
	box.Focus()
}

// caretOn answers whether the reverse-video "type here" fill belongs on a row.
// It is a different question from whether the row carries the operator's PLACE:
// a request in flight takes the fill away — the box is not typeable — while the
// row keeps its focused styling, so the operator does not lose their place while
// a gateway thinks.
func (s *LocationReconcileScreen) caretOn(row int) bool {
	return s.focused == row && !s.pending && !s.loading
}

func (s *LocationReconcileScreen) caretOnField(field int) bool {
	return s.fieldCursor == field && !s.pending && !s.loading
}

// ---------------------------------------------------------------------------
// The count, and what it means
// ---------------------------------------------------------------------------

// reconCount parses a typed count. Empty is "not counted" and is not an error;
// anything that is not a whole number at or above zero is refused rather than
// guessed at, because a guess here is a stock figure.
func reconParseCount(raw string) (int, bool) {
	t := strings.TrimSpace(raw)
	if t == "" {
		return 0, false
	}
	n, err := strconv.Atoi(t)
	if err != nil || n < 0 {
		return 0, false
	}
	return n, true
}

// counted reports the quantity typed against item i, in that item's own COUNT
// unit, and whether the box holds a usable number at all.
func (s *LocationReconcileScreen) counted(i int) (int, bool) {
	if i < 0 || i >= len(s.counts) {
		return 0, false
	}
	return reconParseCount(s.counts[i].Value())
}

// typedOn reports whether the operator has put anything in item i's box,
// usable or not. A box holding "12x" is work they did and must not vanish.
func (s *LocationReconcileScreen) typedOn(i int) bool {
	return i >= 0 && i < len(s.counts) && strings.TrimSpace(s.counts[i].Value()) != ""
}

// countedRows is how many items carry a usable number, which is the size of the
// batch a submit would send.
func (s *LocationReconcileScreen) countedRows() int {
	n := 0
	for i := range s.items {
		if _, ok := s.counted(i); ok {
			n++
		}
	}
	return n
}

// badRows is how many boxes hold something that is not a count. They are the
// reason a submit can be refused locally, and the refusal names them.
func (s *LocationReconcileScreen) badRows() int {
	n := 0
	for i := range s.items {
		if s.typedOn(i) {
			if _, ok := s.counted(i); !ok {
				n++
			}
		}
	}
	return n
}

func (s *LocationReconcileScreen) badOpenRows() []int {
	var rows []int
	for i, it := range s.items {
		if it.CountMode != omsapi.CountModeOpenClosed {
			continue
		}
		raw := strings.TrimSpace(s.rows[i].openCount)
		if raw == "" {
			continue
		}
		if _, ok := reconParseCount(raw); !ok {
			rows = append(rows, i+1)
		}
	}
	return rows
}

// anythingTyped reports whether leaving would DISCARD work — counts, notes,
// overridden reasons, skip flags and open tallies alike. The Esc label reads off
// it, because saying what leaving costs after the screen is gone is too late.
func (s *LocationReconcileScreen) anythingTyped() bool {
	for i := range s.items {
		if s.typedOn(i) {
			return true
		}
	}
	for _, r := range s.rows {
		if r.reasonIx != reconDefaultReasonIx() || r.skipReorder ||
			strings.TrimSpace(r.notes) != "" || strings.TrimSpace(r.openCount) != "" {
			return true
		}
	}
	return strings.TrimSpace(s.scan.Value()) != ""
}

// buildBatch assembles the rows a submit would send, in grid order.
//
// A row is included only when its box holds a usable whole number: a blank box
// is "not counted this visit", which is the ordinary state of most of a room,
// and an unparseable one is refused before the batch is built (submitRefusal).
//
// AtLevel is set from the ITEM's own count mode, so the number goes up in the
// unit the row was labelled with and the payload says which that is. OpenCount
// rides only where the item is counted open/closed — the server refuses it
// outright anywhere else, which would refuse the whole batch.
func (s *LocationReconcileScreen) buildBatch() []omsapi.ReconciliationRow {
	var out []omsapi.ReconciliationRow
	for i, it := range s.items {
		qty, ok := s.counted(i)
		if !ok {
			continue
		}
		st := s.rows[i]
		row := omsapi.ReconciliationRow{
			ItemID:      it.ItemID,
			ActualCount: qty,
			Reason:      cycleCountReasons[st.reasonIx].Value,
			Notes:       strings.TrimSpace(st.notes),
			SkipReorder: st.skipReorder,
			AtLevel:     it.CountsInPacks(),
		}
		if it.CountMode == omsapi.CountModeOpenClosed {
			if n, ok := reconParseCount(st.openCount); ok {
				open := n
				row.OpenCount = &open
			}
		}
		out = append(out, row)
	}
	return out
}

// reordersForecast is how many of the rows a submit would send will file a
// reorder request, as far as the GRID can tell.
//
// A CEILING and not a prediction: OMS also declines to file for a RETIRED item
// and the grid payload does not carry that flag, so the real number can be
// lower. Every surface that reports this number says so — see
// reconReorderCeiling — because a figure an operator acts on must carry its own
// uncertainty rather than borrow confidence from being printed.
func (s *LocationReconcileScreen) reordersForecast() int {
	n := 0
	for i, it := range s.items {
		qty, ok := s.counted(i)
		if !ok {
			continue
		}
		row := omsapi.ReconciliationRow{ActualCount: qty, SkipReorder: s.rows[i].skipReorder}
		if row.FilesAReorder(it.MinimumStock) {
			n++
		}
	}
	return n
}

// reconUnitPhrase is a quantity with the unit it is in, pluralised — "12 boxes",
// "1 bolt". Every number this screen draws that is a stock quantity goes through
// it, so no figure is ever printed naked.
func reconUnitPhrase(n int, unit string) string {
	return fmt.Sprintf("%d %s", n, pluralizeUnit(unit, n))
}

// ---------------------------------------------------------------------------
// Update
// ---------------------------------------------------------------------------

func (s *LocationReconcileScreen) Update(msg tea.Msg) (Screen, tea.Cmd) {
	// The note answers the last keypress IN THE FRAME THAT KEY WAS PRESSED
	// AGAINST, and the header pins it on every phase — so a note that outlives
	// the state it describes is a pinned line contradicting the bar under it.
	// Retired by anything that ends the frame it answers about: a reply off the
	// wire, another keypress (handleKey), or a resize, which moves the row
	// budget and so can change what the bar names. The cursor blink is
	// deliberately not one of those: it moves a caret inside the frame rather
	// than changing the frame's shape, and a note retired by it would expire on
	// a timer rather than on an event.
	switch msg.(type) {
	case reconGridMsg, reconSubmittedMsg, reconScanMsg, tea.WindowSizeMsg:
		s.note.clear()
	}

	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		s.setSize(m)
		return s, nil
	case reconGridMsg:
		return s, s.handleGrid(m)
	case reconSubmittedMsg:
		return s, s.handleSubmitted(m)
	case reconScanMsg:
		return s, s.handleScan(m)
	case tea.KeyMsg:
		return s.handleKey(m)
	}

	// Anything that is not a key belongs to whichever box holds the caret — the
	// blink, chiefly. A frozen phase has no focused box, so nothing here can
	// move under a request in flight.
	if s.pending || s.loading {
		return s, nil
	}
	var cmd tea.Cmd
	box := s.currentInput()
	*box, cmd = box.Update(msg)
	return s, cmd
}

func (s *LocationReconcileScreen) handleKey(m tea.KeyMsg) (Screen, tea.Cmd) {
	// The header is measured BEFORE the arms run and every arm carries it, so a
	// press is judged against ONE frame: the frame whose bar the operator was
	// reading when they pressed.
	headerRows := len(s.headerLines())
	s.note.clear()
	switch s.phase {
	case reconLoading:
		return s.keyLoading(m, headerRows)
	case reconBlocked:
		return s.keyBlocked(m, headerRows)
	case reconRow:
		return s.keyRow(m, headerRows)
	case reconReview:
		return s.keyReview(m, headerRows)
	case reconDone:
		return s.keyDone(m, headerRows)
	}
	return s.keyCount(m, headerRows)
}

// leave returns to the location this screen was opened from.
func (s *LocationReconcileScreen) leave() tea.Cmd {
	return SwitchTo(WSInventory, NewLocationDetailScreen(s.deps, s.locID))
}

func (s *LocationReconcileScreen) handleGrid(m reconGridMsg) tea.Cmd {
	s.loading = false
	if m.err != nil {
		s.phase = reconBlocked
		head, detail := reconFailure("nothing to count: "+s.locationName()+" could not be read", m.err)
		s.setFail(head, detail)
		s.blurAll()
		return Status(head, StatusError)
	}
	s.clearFail()
	s.grid = m.grid
	if m.grid != nil {
		s.applyGrid(m.grid)
	}
	if len(s.items) == 0 {
		s.phase = reconBlocked
		s.blurAll()
		return nil
	}
	s.phase = reconCount
	s.focused = reconRowScan
	s.focusCurrent()
	return nil
}

// applyGrid seats the grid's rows, CARRYING FORWARD everything the operator has
// already typed against an item that is still there.
//
// A re-read is something they ask for (r on the summary, or after a failure),
// and it must not be a way to lose a count. Rows are matched by item id rather
// than by position, because the server orders by name and a rename between two
// reads would otherwise move a typed number onto a different item.
func (s *LocationReconcileScreen) applyGrid(grid *omsapi.LocationReconcileGrid) {
	prevCounts := map[string]string{}
	prevRows := map[string]reconRowState{}
	for i, it := range s.items {
		if i < len(s.counts) {
			prevCounts[it.ItemID] = s.counts[i].Value()
		}
		if i < len(s.rows) {
			prevRows[it.ItemID] = s.rows[i]
		}
	}

	s.items = grid.Items
	s.counts = make([]textinput.Model, len(s.items))
	s.rows = make([]reconRowState, len(s.items))
	for i, it := range s.items {
		box := textinput.New()
		box.Prompt = ""
		box.CharLimit = 9
		if v, ok := prevCounts[it.ItemID]; ok {
			box.SetValue(v)
		}
		s.counts[i] = box
		if st, ok := prevRows[it.ItemID]; ok {
			s.rows[i] = st
		} else {
			s.rows[i] = reconRowState{reasonIx: reconDefaultReasonIx()}
		}
	}
	if s.focused >= s.countRows() {
		s.focused = reconRowScan
	}
	if s.rowIdx >= len(s.items) {
		s.rowIdx = 0
	}
	if s.reviewCursor >= len(s.items) {
		s.reviewCursor = 0
	}
}

func (s *LocationReconcileScreen) handleSubmitted(m reconSubmittedMsg) tea.Cmd {
	s.pending = false
	if m.err != nil {
		// EVERY TYPED COUNT STAYS. The batch is all-or-nothing server-side, so
		// nothing was consumed and there is nothing to reconcile — the operator
		// is left on the review frame with their work intact, the server's own
		// sentence on the status row, and Ctrl+E one press from the row it
		// named. The phase is deliberately NOT moved.
		head, detail := reconFailure("nothing written: the count for "+s.locationName()+" was refused", m.err)
		s.setFail(head, detail)
		s.focusCurrent()
		return Status(head, StatusError)
	}
	s.clearFail()
	s.result = m.result
	s.phase = reconDone
	s.blurAll()
	return Status(s.doneHeadline(), StatusOK)
}

func (s *LocationReconcileScreen) handleScan(m reconScanMsg) tea.Cmd {
	s.pending = false
	s.focusCurrent()
	if m.err != nil {
		head, detail := reconFailure("not found: the lookup for "+poQuotedClip(m.code, reconQuotedCells)+" failed", m.err)
		s.setFail(head, detail)
		return Status(head, StatusError)
	}
	if m.result == nil || m.result.Type == "" {
		return s.say(reconNoMatch(m.code), StatusWarn)
	}
	if m.result.Type != "item" {
		return s.say(fmt.Sprintf("%s is a %s, not an inventory item — nothing here to count against it.",
			poQuotedClip(m.code, reconQuotedCells), reconTargetNoun(m.result.Type)), StatusWarn)
	}
	id := fmt.Sprint(m.result.ID)
	if i, ok := s.indexOfItemID(id); ok {
		return s.seat(i, m.code)
	}
	// Resolved, and stored somewhere else. That is a different fact from "no
	// such code" and the operator acts differently on it: they are holding an
	// item that belongs in another room.
	name := strings.TrimSpace(m.result.Name)
	if name == "" {
		name = "that item"
	}
	return s.say(fmt.Sprintf("%s is %s, which is not stored in %s — this count covers this room only.",
		poQuotedClip(m.code, reconQuotedCells), pickerClip(name, reconNameCells), s.locationName()), StatusWarn)
}

// reconTargetNoun names what a scanner dispatch resolved to, in words a decline
// can be built out of.
func reconTargetNoun(kind string) string {
	switch kind {
	case "asset":
		return "an asset"
	case "work_order":
		return "a work order"
	case "location":
		return "a location"
	case "supplier":
		return "a supplier"
	case "purchase_order":
		return "a purchase order"
	}
	return "a " + strings.ReplaceAll(kind, "_", " ")
}

func (s *LocationReconcileScreen) setFail(head, detail string) {
	s.failHead, s.failDetail = head, detail
}

func (s *LocationReconcileScreen) clearFail() { s.setFail("", "") }

func (s *LocationReconcileScreen) failDetailText() string { return s.failDetail }

// reconFailure splits an error into the headline the status row carries and the
// unbounded detail the header folds.
//
// THE HEADLINE LEADS WITH THE LOAD-BEARING CLAUSE and lets the circumstance be
// what the cut takes. fitStatus gives the row bodyWidth-2 cells — 49 at 80
// columns — and it cannot fold, so a headline shaped "<what> failed" puts the
// one word that matters at the TAIL: "Submitting the count for Machine shop
// mezzanine …" is a sentence that reads as work in progress. "nothing written:"
// first says the thing an operator acts on, and it is also the batch's own
// guarantee.
//
// The server's OWN SENTENCE is the detail, and these endpoints hand-write their
// refusals as {"detail": "…"} — so they never reach DRF's exception handler and
// omsapi.parseError puts the whole raw body into the message. AsDetailRefusal
// recovers the prose; without it the operator reads JSON on the one screen where
// the reason decides whether they retype a row or go and fetch somebody.
func reconFailure(head string, err error) (string, string) {
	if err == nil {
		return "", ""
	}
	if prose, ok := omsapi.AsDetailRefusal(err); ok {
		return head, prose
	}
	return head, err.Error()
}

// ---------------------------------------------------------------------------
// Bounds on what a sentence may spend
// ---------------------------------------------------------------------------

const (
	// reconQuotedCells is the room a quoted operator-supplied value gets inside
	// a sentence. The quoted form is bounded as ONE string rather than bounded
	// and then quoted, because Quote escapes and the expansion would land
	// outside the budget.
	reconQuotedCells = 16
	// reconNameCells is the room an OMS-supplied item name gets inside a
	// sentence, as opposed to on a row of its own where it has the whole pane.
	reconNameCells = 22
)

// ---------------------------------------------------------------------------
// Answering a keypress
// ---------------------------------------------------------------------------

func (s *LocationReconcileScreen) say(text string, level StatusLevel) tea.Cmd {
	return s.note.say(text, level)
}

// decline answers a key that does not act in the state being drawn. The lead
// NAMES the key, which is not decoration: two keys sharing one sentence would
// let the second press redraw the pane the first one left, and on a frame with
// no cursor and no caret that is indistinguishable from a wedged program.
func (s *LocationReconcileScreen) decline(key string, headerRows int) tea.Cmd {
	return s.say(key+" does nothing here · "+s.waysOut(headerRows), StatusWarn)
}

// declineFrozen is decline for a key an in-flight request has made inert. It
// says WHY rather than "does nothing", because the key does work — one second
// from now — and an operator watching a slow gateway is exactly the operator who
// will press it again.
func (s *LocationReconcileScreen) declineFrozen(key string, headerRows int) tea.Cmd {
	return s.say(key+" is frozen until "+s.inFlightSubject()+" answers · "+
		s.waysOut(headerRows), StatusWarn)
}

// inFlightSubject is the request the screen is waiting on, in the words a
// decline is built out of. It is the same expression workingLine reads, so a
// decline and the status row above it cannot disagree about what is out.
func (s *LocationReconcileScreen) inFlightSubject() string {
	if s.loading {
		return "the grid"
	}
	if s.phase == reconReview {
		return "the count"
	}
	return "the lookup"
}

// waysOut names the keys that DO act, read off the bar of the frame the key was
// pressed against, so a decline cannot advertise a key that frame did not honour
// — nor omit one it did.
func (s *LocationReconcileScreen) waysOut(headerRows int) string {
	items := s.barFor(headerRows)
	parts := make([]string, 0, len(items))
	for _, it := range items {
		parts = append(parts, strings.ToLower(it.Key)+" "+strings.ToLower(it.Label))
	}
	if len(parts) == 0 {
		return "nothing here acts yet"
	}
	return strings.Join(parts, " · ")
}

// typeInto hands a key to the focused box and ANSWERS for it when the box does
// not.
//
// The box's OWN answer is what decides rather than a roster of the keys it
// binds: the key goes to the box, and if the box did not take it the frame
// declines by name. Derived from bubbles itself, so a binding a version bump
// adds or drops changes this answer with it. "Took it" is asked of the VALUE,
// the CARET and the COMMAND — the command because bubbles handles Paste as
// `return m, Paste`, so against a value/caret comparison a paste is
// indistinguishable from a key the box ignored, and throwing that command away
// would be a silent discard and a false claim in one press.
func (s *LocationReconcileScreen) typeInto(box *textinput.Model, m tea.KeyMsg, headerRows int) tea.Cmd {
	before, at := box.Value(), box.Position()
	next, cmd := box.Update(m)
	*box = next
	if cmd == nil && box.Value() == before && box.Position() == at {
		return s.decline(m.String(), headerRows)
	}
	return cmd
}

// ---------------------------------------------------------------------------
// Finding an item — the scanner path
// ---------------------------------------------------------------------------

func (s *LocationReconcileScreen) indexOfItemID(id string) (int, bool) {
	id = strings.TrimSpace(id)
	if id == "" {
		return 0, false
	}
	for i, it := range s.items {
		if strings.EqualFold(strings.TrimSpace(it.ItemID), id) {
			return i, true
		}
	}
	return 0, false
}

// findLocal resolves a scanned code against the grid ALONE — no round trip.
//
// This is the path a barcode gun takes in the ordinary case, and it is first
// because the rhythm depends on it: the grid already carries every item's SKU
// and id, so a label that is the SKU seats the cursor in the time it takes to
// redraw. An OMS item URL is parsed here too (scanner.ParseOMSURL), because the
// QR codes OMS prints for shelves carry the item's id in the path and resolving
// that over the network would be asking the server to read back something the
// code already said.
//
// SKU is matched case-insensitively because a scanner's output case is a
// property of how the gun was configured, not of the item.
func (s *LocationReconcileScreen) findLocal(code string) (int, bool) {
	code = strings.TrimSpace(code)
	if code == "" {
		return 0, false
	}
	for i, it := range s.items {
		if sku := strings.TrimSpace(it.SKU); sku != "" && strings.EqualFold(sku, code) {
			return i, true
		}
	}
	if i, ok := s.indexOfItemID(code); ok {
		return i, true
	}
	if scanner.Classify(code) == scanner.KindOMSURL {
		if target, err := scanner.ParseOMSURL(code); err == nil && target != nil {
			switch target.Kind {
			case "item", "code":
				if i, ok := s.indexOfItemID(target.ResourceID); ok {
					return i, true
				}
			}
		}
	}
	return 0, false
}

// seat puts the cursor on item i's count box and clears the scan row, which is
// the whole point of a scan: the next thing typed is the count for the thing
// just scanned.
//
// The box's existing value is SELECTED in the sense that matters here — the
// caret goes to the end of it and the row says what is already there — rather
// than cleared: re-scanning an item already counted is how an operator checks
// themselves, and wiping the number they are checking against would be a silent
// discard.
func (s *LocationReconcileScreen) seat(i int, code string) tea.Cmd {
	s.scan.SetValue("")
	s.focused = reconRowFirstItem + i
	s.focusCurrent()
	s.counts[i].CursorEnd()
	it := s.items[i]
	lead := fmt.Sprintf("%s is row %d, %s", poQuotedClip(code, reconQuotedCells), i+1,
		pickerClip(it.Name, reconNameCells))
	if had := strings.TrimSpace(s.counts[i].Value()); had != "" {
		return s.say(lead+fmt.Sprintf(" — already counted %s; retype to correct it.",
			poQuotedClip(had, reconQuotedCells)), StatusWarn)
	}
	return s.say(lead+fmt.Sprintf(" — type the count in %s.", pluralUnit(it.Unit())), StatusOK)
}

// reconNoMatch is what a code nothing could place says. It names the code and
// what the operator can do instead, because "no match" alone leaves them holding
// a box with nowhere to put it.
func reconNoMatch(code string) string {
	return poQuotedClip(code, reconQuotedCells) +
		" matched no item here and OMS did not recognise it — check the label, or find the row with UP/DN."
}

// findItem is Enter on the scan row: the local pass first, then the server's own
// resolver for a code the grid could not place.
func (s *LocationReconcileScreen) findItem(headerRows int) tea.Cmd {
	code := strings.TrimSpace(s.scan.Value())
	if code == "" {
		return s.say("enter needs a code to find — scan a label, or find the row with UP/DN · "+
			s.waysOut(headerRows), StatusWarn)
	}
	if i, ok := s.findLocal(code); ok {
		return s.seat(i, code)
	}
	if s.deps.OMS == nil {
		return s.say(reconNoMatch(code), StatusWarn)
	}
	// Not in the grid by SKU, id or URL. Ask OMS's own scanner dispatch — the
	// same resolver the web page uses — because a barcode need not be the SKU:
	// it can be a printed asset tag, an alternate code, or a QR for a shelf.
	s.pending = true
	s.blurAll()
	deps, ctx := s.deps, s.ctx()
	return tea.Batch(
		s.say("Looking "+poQuotedClip(code, reconQuotedCells)+" up…", StatusInfo),
		func() tea.Msg {
			res, err := deps.OMS.LookupCode(ctx, code)
			return reconScanMsg{code: code, result: res, err: err}
		},
	)
}

// ---------------------------------------------------------------------------
// The count form
// ---------------------------------------------------------------------------

// reconEnter is what Enter does on the count form, which is not one thing.
type reconEnter int

const (
	// reconEnterNothing — Enter can only refuse, so the bar must not name it.
	reconEnterNothing reconEnter = iota
	// reconEnterFind — the cursor is in the scan box and it holds a code.
	reconEnterFind
	// reconEnterNextScan — the cursor is on a count box, and Enter closes the
	// scanner loop by handing the keyboard back to the scan row.
	reconEnterNextScan
)

// enterAction is the ONE predicate the bar and the Enter arm both read.
//
// FIND wins wherever the scan box holds something, and that ordering is the
// reason this is a function rather than a pair of ifs: a scanner fires a burst
// and then an Enter, so if Enter meant anything else while a code sat unresolved
// in the box the operator would have moved the cursor instead of finding a row.
func (s *LocationReconcileScreen) enterAction() reconEnter {
	if s.focused == reconRowScan {
		if strings.TrimSpace(s.scan.Value()) != "" {
			return reconEnterFind
		}
		return reconEnterNothing
	}
	if _, ok := s.itemAt(s.focused); ok {
		return reconEnterNextScan
	}
	return reconEnterNothing
}

func (s *LocationReconcileScreen) keyLoading(m tea.KeyMsg, headerRows int) (Screen, tea.Cmd) {
	k := m.String()
	if k == "esc" {
		return s, s.leave()
	}
	if reconIsScrollKey(k) {
		return s, s.scrollSheet(k, headerRows)
	}
	return s, s.declineFrozen(k, headerRows)
}

// reconIsScrollKey is the keystroke set jdeScrollStep understands, named here so
// the arms that route to it and the bar that names it cannot disagree.
func reconIsScrollKey(k string) bool {
	switch k {
	case "up", "down", "pgup", "pgdown", "home", "end":
		return true
	}
	return false
}

func (s *LocationReconcileScreen) keyBlocked(m tea.KeyMsg, headerRows int) (Screen, tea.Cmd) {
	switch m.String() {
	case "esc":
		return s, s.leave()
	case "r":
		s.loading = true
		s.phase = reconLoading
		s.sheetOffset = 0
		s.clearFail()
		s.blurAll()
		return s, s.loadGrid()
	}
	if reconIsScrollKey(m.String()) {
		return s, s.scrollSheet(m.String(), headerRows)
	}
	return s, s.decline(m.String(), headerRows)
}

func (s *LocationReconcileScreen) keyCount(m tea.KeyMsg, headerRows int) (Screen, tea.Cmd) {
	k := m.String()
	if s.pending {
		// Frozen: esc is the one key that acts, so it is the one key named.
		// Esc is deliberately NOT gated — a frame with no way out while a slow
		// gateway thinks is the worse defect — and leaving does not cancel the
		// lookup, which simply lands on a screen that is gone.
		if k == "esc" {
			return s, s.leave()
		}
		return s, s.declineFrozen(k, headerRows)
	}

	switch k {
	case "esc":
		return s, s.leave()
	case "enter":
		switch s.enterAction() {
		case reconEnterFind:
			return s, s.findItem(headerRows)
		case reconEnterNextScan:
			s.focused = reconRowScan
			s.focusCurrent()
			return s, s.say("Back on the scan row — scan the next label.", StatusOK)
		}
		return s, s.say("enter needs a code to find — scan a label, or find the row with UP/DN · "+
			s.waysOut(headerRows), StatusWarn)
	case "up", "shift+tab":
		return s, s.moveFocus(-1, headerRows)
	case "down", "tab":
		return s, s.moveFocus(1, headerRows)
	case "pgup", "pgdown":
		return s, s.pageCount(k, headerRows)
	case "ctrl+e":
		return s, s.openRowDetail(headerRows)
	case "ctrl+r":
		return s, s.openReview(headerRows)
	}
	return s, s.typeInto(s.currentInput(), m, headerRows)
}

// moveFocus walks the count form's cursor. It WRAPS, which is the field-form
// rule rather than the list one: a short form has no edge worth defending, and
// the same movement on a list would land the cursor on a row that clears a
// field. The layer's own gate decides whether the key acts at all — a frame the
// pane cannot draw holds the operator's place rather than moving it invisibly.
func (s *LocationReconcileScreen) moveFocus(delta, headerRows int) tea.Cmd {
	// Asked of the bar really DRAWN. tooShort is monotone in bar height, so
	// asking the CEILING would decline the key at heights where the frame IS
	// drawn — which is the state a taller-than-drawn bar creates: at 80x11 the
	// drawn bar is one row, the frame draws, and Down did nothing at all.
	next, ok := s.moveRow(s.focused, s.countRows(), delta, headerRows,
		s.countBarItems(s.countPagesFor(headerRows)))
	if !ok {
		return nil
	}
	s.focused = next
	s.focusCurrent()
	return nil
}

// pageCount pages the cursor through the item rows. Paging CLAMPS where the
// field walk wraps: a page that jumped from the last row to the first would lose
// the operator's place in a room of two hundred SKUs.
func (s *LocationReconcileScreen) pageCount(k string, headerRows int) tea.Cmd {
	dir := 1
	if k == "pgup" {
		dir = -1
	}
	bar := s.countBarItems(s.countPagesFor(headerRows))
	if !s.frameDrawn(headerRows, bar) {
		return nil
	}
	if s.paneSized(headerRows, bar) && !s.countPagesFor(headerRows) {
		return s.say(k+" pages nothing — every row is already on the pane · "+
			s.waysOut(headerRows), StatusWarn)
	}
	next, ok := s.pageRow(s.countBody(), s.focused, s.countRows(), dir, headerRows,
		bar, s.countBarCeiling())
	if !ok {
		return nil
	}
	if next == s.focused {
		return s.say(k+" is already at the "+reconEdge(dir)+" row · "+s.waysOut(headerRows), StatusWarn)
	}
	s.focused = next
	s.focusCurrent()
	return nil
}

func reconEdge(dir int) string {
	if dir < 0 {
		return "first"
	}
	return "last"
}

// countPagesFor is the ONE expression the bar and the paging arm both read.
//
// TWO conditions, because the keys make two claims and both have to hold: the
// body must MOVE, and there must be another ROW to land on — PgUp/PgDn move the
// cursor and let the window follow, so a body taller than the pane with one
// navigable row pages nothing at all. Both halves are the layer's
// (bodyPagesForBar) rather than a private copy of a rule thirty other sheets
// also need.
func (s *LocationReconcileScreen) countPagesFor(headerRows int) bool {
	return s.bodyPagesForBar(s.countBody(), s.countRows(), headerRows, s.countBarCeiling())
}

// ---------------------------------------------------------------------------
// The row detail
// ---------------------------------------------------------------------------

// openRowDetail opens the reason / notes / skip sheet for the row the cursor is
// on. It refuses — by name, saying why — from the scan row, which is about no
// item at all.
func (s *LocationReconcileScreen) openRowDetail(headerRows int) tea.Cmd {
	i, ok := s.itemAt(s.focused)
	if !ok {
		return s.say("ctrl+e opens one row's reason and notes — move onto an item first · "+
			s.waysOut(headerRows), StatusWarn)
	}
	s.rowIdx = i
	s.fieldCursor = reconFieldReason
	s.notes.SetValue(s.rows[i].notes)
	s.open.SetValue(s.rows[i].openCount)
	s.phase = reconRow
	s.focusCurrent()
	return nil
}

// storeRowDetail writes the sheet's boxes back into the row's state. Called
// after EVERY key the sheet handles, so nothing the operator typed can be lost
// by whichever arm they leave through.
func (s *LocationReconcileScreen) storeRowDetail() {
	if s.rowIdx < 0 || s.rowIdx >= len(s.rows) {
		return
	}
	s.rows[s.rowIdx].notes = s.notes.Value()
	s.rows[s.rowIdx].openCount = s.open.Value()
}

func (s *LocationReconcileScreen) closeRowDetail() {
	s.storeRowDetail()
	s.phase = reconCount
	s.focused = reconRowFirstItem + s.rowIdx
	s.focusCurrent()
}

func (s *LocationReconcileScreen) keyRow(m tea.KeyMsg, headerRows int) (Screen, tea.Cmd) {
	k := m.String()
	switch k {
	case "enter", "esc":
		s.closeRowDetail()
		return s, nil
	case "up", "shift+tab":
		s.moveRowField(-1, headerRows)
		return s, nil
	case "down", "tab":
		s.moveRowField(1, headerRows)
		return s, nil
	case "left", "right":
		if cmd, handled := s.changeRowChoice(k, headerRows); handled {
			return s, cmd
		}
	}
	cmd := s.typeInto(s.currentInput(), m, headerRows)
	s.storeRowDetail()
	return s, cmd
}

// moveRowField walks the sheet's cursor, storing the boxes first so a value
// typed and then moved away from is kept.
func (s *LocationReconcileScreen) moveRowField(delta, headerRows int) {
	s.storeRowDetail()
	next, ok := s.moveRow(s.fieldCursor, s.rowFields(), delta, headerRows, s.rowBarItems())
	if !ok {
		return
	}
	s.fieldCursor = next
	s.focusCurrent()
}

// changeRowChoice is ←/→ on a choice row. It answers `handled` false on a TEXT
// row so the key falls through to the box, where left and right move the caret —
// declining them there would take caret movement away from a field the operator
// is editing.
func (s *LocationReconcileScreen) changeRowChoice(k string, headerRows int) (tea.Cmd, bool) {
	if s.rowIdx < 0 || s.rowIdx >= len(s.rows) {
		return nil, false
	}
	delta := 1
	if k == "left" {
		delta = -1
	}
	switch s.fieldCursor {
	case reconFieldReason:
		n := len(cycleCountReasons)
		s.rows[s.rowIdx].reasonIx = (s.rows[s.rowIdx].reasonIx + delta + n) % n
		return nil, true
	case reconFieldSkip:
		s.rows[s.rowIdx].skipReorder = !s.rows[s.rowIdx].skipReorder
		return nil, true
	}
	return nil, false
}

// ---------------------------------------------------------------------------
// Review and submit
// ---------------------------------------------------------------------------

// submitRefusal is why a submit cannot be attempted, or "" when it can. It is
// the ONE predicate the bar, the Ctrl+R arm and the Enter arm read, so the bar
// loses the key on exactly the keystroke that makes it refuse.
func (s *LocationReconcileScreen) submitRefusal() string {
	if bad := s.badRows(); bad > 0 {
		return fmt.Sprintf("%d row(s) hold something that is not a whole count — clear or correct them first", bad)
	}
	if rows := s.badOpenRows(); len(rows) > 0 {
		labels := make([]string, len(rows))
		for i, row := range rows {
			labels[i] = strconv.Itoa(row)
		}
		return fmt.Sprintf("open tally on row(s) %s is not a whole count — clear or correct it first", strings.Join(labels, ", "))
	}
	if s.countedRows() == 0 {
		return "nothing is counted yet — put a number against at least one row"
	}
	return ""
}

func (s *LocationReconcileScreen) openReview(headerRows int) tea.Cmd {
	if why := s.submitRefusal(); why != "" {
		return s.say("ctrl+r has nothing to review — "+why+" · "+s.waysOut(headerRows), StatusWarn)
	}
	s.phase = reconReview
	s.reviewCursor = 0
	s.blurAll()
	return nil
}

// reviewRows are the item indexes a submit would send, in grid order. The review
// list draws exactly these, so what is read is what goes.
func (s *LocationReconcileScreen) reviewRows() []int {
	var out []int
	for i := range s.items {
		if _, ok := s.counted(i); ok {
			out = append(out, i)
		}
	}
	return out
}

func (s *LocationReconcileScreen) keyReview(m tea.KeyMsg, headerRows int) (Screen, tea.Cmd) {
	k := m.String()
	if s.pending {
		if k == "esc" {
			return s, s.leave()
		}
		return s, s.declineFrozen(k, headerRows)
	}
	switch k {
	case "esc":
		return s, s.leave()
	case "enter":
		if why := s.submitRefusal(); why != "" {
			return s, s.say("enter has nothing to submit — "+why+" · "+s.waysOut(headerRows), StatusWarn)
		}
		return s.submit()
	case "ctrl+e":
		s.phase = reconCount
		s.focusCurrent()
		return s, nil
	case "up", "down":
		return s, s.moveReviewCursor(k, headerRows)
	case "pgup", "pgdown":
		return s, s.pageReviewCursor(k, headerRows)
	}
	return s, s.decline(k, headerRows)
}

func (s *LocationReconcileScreen) moveReviewCursor(k string, headerRows int) tea.Cmd {
	delta := 1
	if k == "up" {
		delta = -1
	}
	rows := len(s.reviewRows())
	bar := s.reviewBarItems(s.reviewPagesFor(headerRows))
	next, ok := s.pickRow(s.reviewCursor, rows, delta, headerRows, bar)
	if !ok {
		// Refused pane: silence, for the reason scrollSheet records.
		return nil
	}
	if next == s.reviewCursor {
		if !jdeRowMoves(rows) {
			return s.say(k+" moves nothing — there is one row to submit · "+
				s.waysOut(headerRows), StatusWarn)
		}
		return s.say(k+" is already at the "+reconEdge(delta)+" row · "+
			s.waysOut(headerRows), StatusWarn)
	}
	s.reviewCursor = next
	return nil
}

func (s *LocationReconcileScreen) pageReviewCursor(k string, headerRows int) tea.Cmd {
	dir := 1
	if k == "pgup" {
		dir = -1
	}
	rows := len(s.reviewRows())
	bar := s.reviewBarItems(s.reviewPagesFor(headerRows))
	if !s.frameDrawn(headerRows, bar) {
		return nil
	}
	if s.paneSized(headerRows, bar) && !s.reviewPagesFor(headerRows) {
		return s.say(k+" pages nothing — every row is already on the pane · "+
			s.waysOut(headerRows), StatusWarn)
	}
	next, ok := s.pageRow(s.reviewBody(), s.reviewCursor, rows, dir, headerRows,
		bar, s.reviewBarCeiling())
	if !ok {
		return nil
	}
	if next == s.reviewCursor {
		return s.say(k+" is already at the "+reconEdge(dir)+" row · "+s.waysOut(headerRows), StatusWarn)
	}
	s.reviewCursor = next
	return nil
}

func (s *LocationReconcileScreen) reviewPagesFor(headerRows int) bool {
	return s.bodyPagesForBar(s.reviewBody(), len(s.reviewRows()), headerRows, s.reviewBarCeiling())
}

func (s *LocationReconcileScreen) submit() (Screen, tea.Cmd) {
	rows := s.buildBatch()
	if len(rows) == 0 {
		return s, s.say("nothing is counted yet — put a number against at least one row", StatusWarn)
	}
	if s.deps.OMS == nil {
		return s, s.say("no OMS client is configured, so the count cannot be sent.", StatusError)
	}
	s.pending = true
	s.clearFail()
	s.blurAll()
	deps, ctx := s.deps, s.ctx()
	return s, func() tea.Msg {
		res, err := deps.OMS.SubmitReconciliationBatch(ctx, rows)
		return reconSubmittedMsg{result: res, err: err}
	}
}

func (s *LocationReconcileScreen) keyDone(m tea.KeyMsg, headerRows int) (Screen, tea.Cmd) {
	switch m.String() {
	case "enter", "esc":
		return s, s.leave()
	case "r":
		// Count again: re-read the grid so the projections are the ones the
		// batch just wrote. Every box is cleared first — the counts that landed
		// are on the record now, and leaving them in the boxes would let a
		// reflexive second submit book the same room twice.
		s.result = nil
		s.loading = true
		s.phase = reconLoading
		s.clearFail()
		for i := range s.counts {
			s.counts[i].SetValue("")
		}
		for i := range s.rows {
			s.rows[i] = reconRowState{reasonIx: reconDefaultReasonIx()}
		}
		s.scan.SetValue("")
		s.sheetOffset = 0
		s.blurAll()
		return s, s.loadGrid()
	}
	if reconIsScrollKey(m.String()) {
		return s, s.scrollSheet(m.String(), headerRows)
	}
	return s, s.decline(m.String(), headerRows)
}

// ---------------------------------------------------------------------------
// The action bars
// ---------------------------------------------------------------------------

func (s *LocationReconcileScreen) bar() []actionBarItem {
	return s.barFor(len(s.headerLines()))
}

// scrolledPhase reports whether the phase being drawn is one of the READ-ONLY
// frames that scroll an offset rather than anchoring a window on a cursor.
//
// It is one predicate read by View, by the bars and by the key arms, so a frame
// cannot be drawn scrolled while its bar names no scroll key or its arm moves no
// offset.
func (s *LocationReconcileScreen) scrolledPhase() bool {
	switch s.phase {
	case reconLoading, reconBlocked, reconDone:
		return true
	}
	return false
}

// paneSized reports whether the terminal has told this screen a size yet.
//
// Asked as bodyAvailForBar()==0, which is the LAYER's own spelling of "no pane"
// and the accessor a sheet is allowed to read — the terminal's row count is the
// layer's to know (paneRows is jde:layer-only), and a sheet that reached for it
// would be keeping a second copy of the geometry the frame windows with.
//
// It exists because EVERY geometric decline on this screen has to be gated on
// it. Unsized there is no window for a body to overflow, so "does this body
// scroll?" cannot be answered from geometry at all — and the layer's standing
// answer for no pane is to ACT, draw whole, and let clampToBox decide. A decline
// written without this gate turns that into a refusal: pageRow skips its own
// scroll test when unsized, so the guard in front of it was the only thing
// saying no, and PgDn stopped paging on a terminal that had not been sized.
func (s *LocationReconcileScreen) paneSized(headerRows int, items []actionBarItem) bool {
	return s.bodyAvailForBar(headerRows, items) > 0
}

// sheetScrollsFor is whether the read-only body has more than the pane shows.
// Asked of the CEILING bar, because naming the scroll keys costs cells, cells
// fold the bar onto another row, and a folded bar leaves the body one row fewer
// — so the tallest bar is the fixed point and the answer cannot oscillate
// between frames.
func (s *LocationReconcileScreen) sheetScrollsFor(headerRows int) bool {
	body, _ := s.body()
	return s.bodyScrollsForBar(body, headerRows, s.sheetBarCeiling(s.phase))
}

// sheetMovesFor is the conjunction the scroll ARMS read: the body must have
// more to show AND the frame must actually be drawn. The two halves are asked
// of different bars on purpose — drawability of the bar really DRAWN, since
// tooShort is monotone in bar height and a taller bar would decline a key at a
// height the frame IS drawn at.
func (s *LocationReconcileScreen) sheetMovesFor(headerRows int) bool {
	if !s.frameDrawn(headerRows, s.barFor(headerRows)) {
		return false
	}
	// An UNSIZED terminal SKIPS the scroll half rather than failing it. There is
	// no window for the body to overflow, so the geometric question cannot be
	// answered from geometry — and the layer's standing answer for no pane is
	// "act, draw whole, and let clampToBox decide", which is what pageRow does
	// one level down.
	//
	if !s.paneSized(headerRows, s.barFor(headerRows)) {
		return true
	}
	return s.sheetScrollsFor(headerRows)
}

// scrollSheet moves the read-only offset.
//
// THE TWO WAYS IT CAN DECLINE ARE DIFFERENT FACTS AND ARE ANSWERED DIFFERENTLY,
// which is the whole reason this is not one bool.
//
// A REFUSED PANE answers with nothing at all. A gated movement arm's whole
// product WAS the position, so once the move is refused there is nothing left to
// report — and a note written there is not drawn now and IS drawn when the
// terminal grows back, answering a press the operator has moved on from.
//
// A FRAME THE OPERATOR CAN SEE, whose body simply does not scroll, must SAY SO.
// That is not a refused pane: the press changed nothing on a frame that is
// fully drawn, which is the "it just kinda hangs there" report exactly. The
// lead NAMES the key, because two keys sharing one sentence would let the second
// press redraw the pane the first one left.
func (s *LocationReconcileScreen) scrollSheet(key string, headerRows int) tea.Cmd {
	if !s.frameDrawn(headerRows, s.barFor(headerRows)) {
		return nil
	}
	if !s.sheetMovesFor(headerRows) {
		return s.say(key+" moves nothing here — this frame is on the pane whole · "+
			s.waysOut(headerRows), StatusWarn)
	}
	body, _ := s.body()
	step := s.scrollRows(headerRows, s.barFor(headerRows))
	next := jdeScrollStep(key, s.sheetOffset, body.Len(), step)
	if next == s.sheetOffset {
		return s.say(key+" is already at the "+reconScrollEdge(key)+" of this frame · "+
			s.waysOut(headerRows), StatusWarn)
	}
	s.sheetOffset = next
	return nil
}

// reconScrollEdge names the end a read-only body is already resting against, so
// a key that cannot move further says which way it could not go.
func reconScrollEdge(key string) string {
	switch key {
	case "up", "pgup", "home":
		return "top"
	}
	return "bottom"
}

// sheetScrollBar appends the read-only scroll keys to a bar, for exactly as long
// as the body has more than the pane shows.
func sheetScrollBar(items []actionBarItem, scroll bool) []actionBarItem {
	if !scroll {
		return items
	}
	return append(items,
		actionBarItem{"UP/DN", "Scroll"},
		actionBarItem{"PgUp/PgDn", "Page"},
		actionBarItem{"Home/End", "Top/End"})
}

// sheetBarCeiling is the tallest bar a read-only phase can draw: its own keys
// plus every scroll key.
func (s *LocationReconcileScreen) sheetBarCeiling(phase reconPhase) []actionBarItem {
	return sheetScrollBar(reconSheetKeys(phase), true)
}

// reconSheetKeys are a read-only phase's own keys, before the scroll pair.
func reconSheetKeys(phase reconPhase) []actionBarItem {
	switch phase {
	case reconBlocked:
		return []actionBarItem{{"r", "Re-read"}, {"Esc", "Back to location"}}
	case reconDone:
		return []actionBarItem{{"Enter/Esc", "Back to location"}, {"r", "Count again"}}
	}
	return []actionBarItem{{"Esc", "Back to location"}}
}

func (s *LocationReconcileScreen) barFor(headerRows int) []actionBarItem {
	switch s.phase {
	case reconLoading, reconBlocked, reconDone:
		return sheetScrollBar(reconSheetKeys(s.phase), s.sheetScrollsFor(headerRows))
	case reconRow:
		return s.rowBarItems()
	case reconReview:
		return s.reviewBarItems(s.reviewPagesFor(headerRows))
	}
	return s.countBarItems(s.countPagesFor(headerRows))
}

// barCeiling is the tallest bar the phase being drawn can produce.
//
// A genuine FIXED POINT and that is what it is for: a taller bar is a smaller
// body, so a body that overflows the budget this leaves also overflows every
// larger one, and the header allowance measured against it cannot oscillate
// between frames. Every optional item is present with the LONGEST wording it can
// take.
func (s *LocationReconcileScreen) barCeiling() []actionBarItem {
	switch s.phase {
	case reconLoading, reconBlocked, reconDone:
		return s.sheetBarCeiling(s.phase)
	case reconRow:
		return s.rowBarCeiling()
	case reconReview:
		return s.reviewBarCeiling()
	}
	return s.countBarCeiling()
}

// countBarItems is the count form's bar for a given paging state, so the bar
// that is MEASURED is the bar that is drawn.
func (s *LocationReconcileScreen) countBarItems(paging bool) []actionBarItem {
	if s.pending {
		return []actionBarItem{{"Esc", "Back to location"}}
	}
	var items []actionBarItem
	switch s.enterAction() {
	case reconEnterFind:
		items = append(items, actionBarItem{"Enter", "Find item"})
	case reconEnterNextScan:
		items = append(items, actionBarItem{"Enter", "Next scan"})
	}
	// The Esc label says what leaving COSTS: leaving destroys the screen and
	// with it every count in every box, and saying so afterwards is too late.
	back := "Back to location"
	if s.anythingTyped() {
		back = "Discard & back"
	}
	items = append(items, actionBarItem{"Esc", back})
	if jdeRowMoves(s.countRows()) {
		items = append(items, actionBarItem{"UP/DN", "Rows"})
	}
	if paging {
		items = append(items, actionBarItem{"PgUp/PgDn", "Page"})
	}
	// Named for exactly as long as each would ACT, off the arms' own
	// predicates, so the bar and the key cannot disagree.
	if _, ok := s.itemAt(s.focused); ok {
		items = append(items, actionBarItem{"Ctrl+E", "Row detail"})
	}
	if s.submitRefusal() == "" {
		items = append(items, actionBarItem{"Ctrl+R", "Review"})
	}
	return items
}

func (s *LocationReconcileScreen) countBarCeiling() []actionBarItem {
	return []actionBarItem{
		{"Enter", "Find item"},
		{"Esc", "Discard & back"},
		{"UP/DN", "Rows"},
		{"PgUp/PgDn", "Page"},
		{"Ctrl+E", "Row detail"},
		{"Ctrl+R", "Review"},
	}
}

func (s *LocationReconcileScreen) rowBarItems() []actionBarItem {
	items := []actionBarItem{{"Enter/Esc", "Back to count"}}
	if jdeRowMoves(s.rowFields()) {
		items = append(items, actionBarItem{"UP/DN", "Fields"})
	}
	// ←→ is named only on the rows it changes. On the notes and open-tally
	// boxes left and right move the CARET, which is the box's own and not an
	// affordance this bar offers.
	if s.fieldCursor == reconFieldReason || s.fieldCursor == reconFieldSkip {
		items = append(items, actionBarItem{"←→", "Change"})
	}
	return items
}

func (s *LocationReconcileScreen) rowBarCeiling() []actionBarItem {
	return []actionBarItem{
		{"Enter/Esc", "Back to count"},
		{"UP/DN", "Fields"},
		{"←→", "Change"},
	}
}

func (s *LocationReconcileScreen) reviewBarItems(paging bool) []actionBarItem {
	if s.pending {
		return []actionBarItem{{"Esc", "Back to location"}}
	}
	var items []actionBarItem
	if s.submitRefusal() == "" {
		items = append(items, actionBarItem{"Enter", "Submit"})
	}
	items = append(items,
		actionBarItem{"Ctrl+E", "Back to count"},
		actionBarItem{"Esc", "Discard & back"},
	)
	if jdeRowMoves(len(s.reviewRows())) {
		items = append(items, actionBarItem{"UP/DN", "Rows"})
	}
	if paging {
		items = append(items, actionBarItem{"PgUp/PgDn", "Page"})
	}
	return items
}

func (s *LocationReconcileScreen) reviewBarCeiling() []actionBarItem {
	return []actionBarItem{
		{"Enter", "Submit"},
		{"Ctrl+E", "Back to count"},
		{"Esc", "Discard & back"},
		{"UP/DN", "Rows"},
		{"PgUp/PgDn", "Page"},
	}
}

// ---------------------------------------------------------------------------
// The bodies
// ---------------------------------------------------------------------------

// body is the phase's scrollable body and the row the window is anchored on,
// answered in ONE place so a sweep asking what the frame draws cannot end up
// asking a different builder from the one View picks.
func (s *LocationReconcileScreen) body() (*jdeLines, int) {
	switch s.phase {
	case reconLoading:
		return s.loadingBody(), 0
	case reconBlocked:
		return s.blockedBody(), 0
	case reconRow:
		return s.rowBody(), s.fieldCursor
	case reconReview:
		return s.reviewBody(), s.reviewCursor
	case reconDone:
		return s.doneBody(), 0
	}
	return s.countBody(), s.focused
}

// loadingBody says what is being fetched and what it is for. Every line belongs
// to row 0, because no key on this phase moves a CURSOR: a line belonging to no
// row is a line the layer would count behind an "↑ more above" marker that no
// key can act on.
//
// NO LEAD IS DECLARED on this body or on the two beside it, and that is a
// consequence rather than an omission. jdeLines.DeclareLead names the one line a
// PINNED body must keep — a body whose window nothing can move, where the tail
// is an accepted loss. These three are not pinned: they are drawn through
// frameScrolled with an offset (scrolledPhase), their bars name the scroll keys
// for exactly as long as there is more to show, and every line of them is
// therefore reachable. A lead declared here would be a claim about a sacrifice
// this screen does not make.
func (s *LocationReconcileScreen) loadingBody() *jdeLines {
	l := &jdeLines{}
	width := s.paneWidth()
	l.AddRow(0, jdeIndent+StyleMuted.Render("Reading what "+pickerClip(s.locationName(), reconNameCells)+" is meant to hold."))
	for _, line := range reconCaveatLines(reconUnitsStanding, StyleMuted, width) {
		l.AddRow(0, line)
	}
	return l
}

// blockedBody is the frame that cannot take a count, and it keeps TWO facts
// apart: the grid could not be read (we do not know what is in this room), or it
// was read and the room holds no active items (there is nothing to count). An
// operator standing in a store room acts differently on each.
func (s *LocationReconcileScreen) blockedBody() *jdeLines {
	l := &jdeLines{}
	width := s.paneWidth()
	for _, line := range reconCaveatLines(s.blockedReason(), StyleStatusWarn, width) {
		l.AddRow(0, line)
	}
	return l
}

func (s *LocationReconcileScreen) blockedReason() string {
	if s.grid == nil {
		return "The grid for " + pickerClip(s.locationName(), reconNameCells) +
			" could not be read, so nothing here knows what this room is meant to hold."
	}
	return "No active items are stored in " + pickerClip(s.locationName(), reconNameCells) +
		", so there is nothing to count. Items are assigned a location on the item itself."
}

// countBody is the grid: the scan row, then one block per item.
//
// EVERY LINE BELONGS TO A NAVIGABLE ROW. jdeLines.Window anchors the window on
// the CURSOR's block and no key here moves a cursor above the first row, so a
// line added ahead of the first block is stranded the moment the body overflows
// — with the frame drawing "↑ N more above" and every key the bar names refusing
// to fetch it. Anything that would have been a lead-in is a pinned HEADER row
// instead (headerLines), which is trimmed by rank and claims nothing about what
// it dropped.
func (s *LocationReconcileScreen) countBody() *jdeLines {
	l := &jdeLines{}
	lw := reconLabelWidth()
	width := s.bodyWidth()

	// The FIELD leads its block: a block that will not fit keeps its START, and
	// a hint drawn above the box is a hint that pushes the box off a short pane
	// — and this is the box a scanner is already firing into.
	l.AddFittedFields([]jdeField{{
		Label:   "Scan",
		Kind:    jdeText,
		Input:   &s.scan,
		Width:   26,
		Hint:    "barcode or SKU",
		Focused: s.caretOn(reconRowScan),
	}}, lw, width, reconRowScan)
	for _, line := range reconCaveatLines(s.scanCaveat(), StyleMuted, width) {
		l.AddRow(reconRowScan, line)
	}
	l.AddRow(reconRowScan, "")

	for i := range s.items {
		if i > 0 {
			// EVERY separator closes the block ABOVE it rather than opening the
			// one below. Window keeps a block's START when the block will not
			// fit, so a blank tagged to the block below is the first line that
			// block draws — a pane naming nothing at all about the row the
			// cursor just moved to.
			l.AddRow(reconRowFirstItem+i-1, "")
		}
		s.addItemBlock(l, i, lw, width)
	}
	return l
}

// scanCaveat is what the scan row says about itself. It names the UNIT rule
// rather than the keys, because the bar names the keys and one surface names a
// key.
func (s *LocationReconcileScreen) scanCaveat() string {
	return fmt.Sprintf("%d item(s) here · %d counted. Each row is counted in its own unit; the box says which.",
		len(s.items), s.countedRows())
}

// reconUnitsStanding is the fact this whole screen turns on, in one sentence.
const reconUnitsStanding = "Every count is entered in that item's own unit — base units for most, whole packs for an item stocked by the case."

// addItemBlock draws one item: its identity, the count box, what the number
// typed there would MEAN, and what is on file.
//
// What follows the box is in a SACRIFICE ORDER, and it is written down because a
// block taller than the window loses its tail with NO key able to fetch it —
// Window keeps a block's start and nothing scrolls inside one. So the block runs
// from what the operator cannot do without to what they can:
//
//	what the typed number MEANS      the delta, and whether it files a reorder
//	what this row will RECORD        a reason, note or skip that is not the
//	                                 default, so an override is never invisible
//	what is ON FILE                  the projection, in the row's own unit and
//	                                 in base units where those differ
//
// The MEANING leads because it is about the number the operator just typed, and
// it is what stops a digit slip becoming a stock figure. ON FILE is last because
// with nothing typed the two lines above it are empty, so it is the first thing
// drawn in exactly the state where it is what the operator needs.
func (s *LocationReconcileScreen) addItemBlock(l *jdeLines, i, lw, width int) {
	it := s.items[i]
	row := reconRowFirstItem + i

	l.AddRow(row, jdeIndent+s.itemHeading(i, width))
	// The box's HINT is the unit, right beside the number. It is not decoration
	// and it is not repeated for style: this is the one field on this screen
	// where a number in the wrong unit is a silently wrong stock level, and the
	// hint is drawn where the number is typed rather than only in a line under
	// it that a short pane can drop.
	l.AddFittedFields([]jdeField{{
		Label:   "Count",
		Kind:    jdeText,
		Input:   &s.counts[i],
		Width:   9,
		Hint:    pluralUnit(it.Unit()),
		Focused: s.caretOn(row),
	}}, lw, width, row)

	for _, line := range s.meaningLines(i, width) {
		l.AddRow(row, line)
	}
	for _, line := range s.overrideLines(i, width) {
		l.AddRow(row, line)
	}
	for _, line := range reconCaveatLines(reconOnFile(it), StyleMuted, width) {
		l.AddRow(row, line)
	}
	// The owning SIG is the TAIL of the block on purpose. It matters only when
	// a batch is refused — OMS lets staff, or the owning group's SIG admin,
	// reconcile an item — and that refusal names the item itself, so this is the
	// row's most expendable fact and belongs where a short pane drops it.
	if g := strings.TrimSpace(it.OwningGroupName); g != "" {
		for _, line := range reconCaveatLines("Owned by "+g, StyleMuted, width) {
			l.AddRow(row, line)
		}
	}
}

// itemHeading is an item's identity row: the row number, its name and its SKU.
//
// BOUNDED AS ASSEMBLED rather than part by part. A bound applied to the name and
// then added to is not a bound, and this is the row that says WHICH item a count
// is about — the one row where drawing the wrong thing is a count against the
// wrong item.
//
// Where the pane cannot hold both identifiers the SKU gives and the NAME keeps
// the room. An operator standing at a shelf reads the name; the SKU is what the
// scanner matched on, and the scan's own answer has already named the row.
func (s *LocationReconcileScreen) itemHeading(i, width int) string {
	it := s.items[i]
	room := width - len(jdeIndent)
	if width <= 0 {
		room = screenBodyWidth(80) - len(jdeIndent)
	}
	// The HIGHLIGHT's own padding comes out of the room, on EVERY row and not
	// only the highlighted one. lipgloss adds it around the rendered string, so
	// a row that fits until it is selected is a row cut on exactly the press
	// that selects it — and the cut is clampToBox's, from the right, with no
	// mark. Asked of the style rather than counted, so a change there moves the
	// reservation with it.
	room -= reconHighlightPad()
	lead := fmt.Sprintf("%2d ", i+1)
	mark := ""
	switch {
	case s.typedOn(i) && !s.isCounted(i), s.openCountInvalid(i):
		mark = " !"
	case s.isCounted(i):
		mark = " ✓"
	}
	sku := ""
	if raw := strings.TrimSpace(it.SKU); raw != "" {
		sku = "  " + pickerClip(raw, reconSKUCells)
	}
	nameRoom := room - lipgloss.Width(lead) - lipgloss.Width(sku) - lipgloss.Width(mark)
	if nameRoom < reconNameFloor {
		sku = ""
		nameRoom = room - lipgloss.Width(lead) - lipgloss.Width(mark)
	}
	if nameRoom < 1 {
		nameRoom = 1
	}
	out := lead + pickerClip(it.Name, nameRoom) + sku + mark
	if s.focused == reconRowFirstItem+i && s.phase == reconCount {
		return StyleSidebarItemActive.Render(fitCell(out, room))
	}
	return fitCell(out, room)
}

// reconHighlightPad is what StyleSidebarItemActive adds around a row it
// renders. It is ASKED rather than counted, so the reservation cannot drift
// away from the style it is reserving for.
func reconHighlightPad() int { return StyleSidebarItemActive.GetHorizontalPadding() }

const (
	// reconSKUCells is the room the SKU gets on a heading row. It is an
	// IDENTIFIER in the facts column, so it is clipped before the row is
	// assembled rather than allowed to push the name out.
	reconSKUCells = 14
	// reconNameFloor is the least room a name may be squeezed to before the SKU
	// gives instead.
	reconNameFloor = 10
)

func (s *LocationReconcileScreen) isCounted(i int) bool {
	_, ok := s.counted(i)
	return ok
}

func (s *LocationReconcileScreen) openCountInvalid(i int) bool {
	if s.items[i].CountMode != omsapi.CountModeOpenClosed {
		return false
	}
	raw := strings.TrimSpace(s.rows[i].openCount)
	if raw == "" {
		return false
	}
	_, ok := reconParseCount(raw)
	return !ok
}

// meaningLines is what the number currently in the box would MEAN, drawn under
// the box as it is typed.
//
// Both facts are stated in the row's own COUNT unit, and the reorder line names
// the threshold in that unit too — because `minimum_stock` IS in that unit for a
// pack-counted item. Comparing a base-unit figure against it, which is what the
// web page does, is wrong by the pack size.
func (s *LocationReconcileScreen) meaningLines(i, width int) []string {
	it := s.items[i]
	raw := strings.TrimSpace(s.counts[i].Value())
	if raw == "" {
		return nil
	}
	qty, ok := reconParseCount(raw)
	if !ok {
		return reconCaveatLines(poQuotedClip(raw, reconQuotedCells)+
			" is not a whole count, so this row cannot be sent.", StyleStatusError, width)
	}

	unit := it.Unit()
	var out []string
	switch delta := qty - it.ProjectedAtUnit; {
	case delta == 0:
		out = append(out, reconCaveatLines(fmt.Sprintf("%s — matches the count on file.",
			reconUnitPhrase(qty, unit)), StyleStatusOK, width)...)
	case delta < 0:
		out = append(out, reconCaveatLines(fmt.Sprintf("%s — %s fewer than on file.",
			reconUnitPhrase(qty, unit), reconUnitPhrase(-delta, unit)), StyleStatusWarn, width)...)
	default:
		out = append(out, reconCaveatLines(fmt.Sprintf("%s — %s more than on file.",
			reconUnitPhrase(qty, unit), reconUnitPhrase(delta, unit)), StyleStatusWarn, width)...)
	}

	row := omsapi.ReconciliationRow{ActualCount: qty, SkipReorder: s.rows[i].skipReorder}
	switch {
	case row.FilesAReorder(it.MinimumStock):
		out = append(out, reconCaveatLines(fmt.Sprintf(
			"At or below the minimum of %s: submitting files a reorder for %s.",
			reconUnitPhrase(it.MinimumStock, unit), reconUnitPhrase(it.ReorderQuantity, unit)),
			StyleStatusWarn, width)...)
	case s.rows[i].skipReorder && qty <= it.MinimumStock:
		out = append(out, reconCaveatLines(fmt.Sprintf(
			"At or below the minimum of %s, but this row is set to file no reorder.",
			reconUnitPhrase(it.MinimumStock, unit)), StyleMuted, width)...)
	}
	return out
}

// overrideLines say what this row will RECORD when it differs from the default.
// Drawn only when something was changed, so the ordinary row stays three lines
// and an override is never invisible.
func (s *LocationReconcileScreen) overrideLines(i, width int) []string {
	st := s.rows[i]
	var parts []string
	if st.reasonIx != reconDefaultReasonIx() {
		parts = append(parts, "reason: "+cycleCountReasons[st.reasonIx].Label)
	}
	if st.skipReorder {
		parts = append(parts, "no reorder")
	}
	if n := strings.TrimSpace(st.notes); n != "" {
		parts = append(parts, "note: "+poQuotedClip(n, reconQuotedCells))
	}
	if o := strings.TrimSpace(st.openCount); o != "" && !s.openCountInvalid(i) {
		parts = append(parts, "open: "+o)
	}
	var out []string
	if len(parts) > 0 {
		out = append(out, reconCaveatLines(strings.Join(parts, " · "), StyleMuted, width)...)
	}
	if o := strings.TrimSpace(st.openCount); o != "" && s.openCountInvalid(i) {
		out = append(out, reconCaveatLines(poQuotedClip(o, reconQuotedCells)+
			" is not a whole open tally, so this row cannot be sent.", StyleStatusError, width)...)
	}
	return out
}

// reconOnFile is what OMS currently believes is on the shelf, in the unit the
// row is counted in — and in BASE units beside it whenever the two differ.
//
// It is kept to ONE row at the 80-column pane: it is what the operator compares
// their count against, and jdeLines.Window keeps a block's START, so a two-row
// on-file line would push the rest of the block's tail off a short pane for the
// sake of a fact that is already complete after the first clause.
//
// The base-unit figure is named rather than implied. "14 boxes" and "1400" are
// the same shelf, and a screen that prints one number without saying which unit
// it is in is the defect this whole file is written against. Where the two
// coincide — every each-counted item, which is most of a catalogue — there is
// nothing to say and nothing is said.
func reconOnFile(it omsapi.LocationReconcileItem) string {
	out := "On file: " + reconUnitPhrase(it.ProjectedAtUnit, it.Unit())
	if it.Projected != it.ProjectedAtUnit {
		out += fmt.Sprintf(" (%d base units)", it.Projected)
	}
	if it.CountMode == omsapi.CountModeOpenClosed {
		out += fmt.Sprintf(" · %d open", it.OpenContainerCount)
	}
	return out
}

// reconCaveatLines folds one caveat under the row it belongs to, indented to the
// row's own content: it is a continuation of the row above it, not a value
// hanging off a label.
func reconCaveatLines(text string, style lipgloss.Style, width int) []string {
	room := 0
	if width > 0 {
		if room = width - len(reconMetaIndent); room < 1 {
			room = 1
		}
	}
	wrapped := jdeWrapNote(text, room)
	out := make([]string, 0, len(wrapped))
	for _, line := range wrapped {
		out = append(out, reconMetaIndent+style.Render(line))
	}
	return out
}

// rowBody is one row's reason, notes, skip flag and open tally.
//
// The item it is about leads, and it belongs to row 0 along with everything that
// is not a field — the sheet's cursor only ever stands on a FIELD, so a line
// tagged to no row would be a line no key can reach.
func (s *LocationReconcileScreen) rowBody() *jdeLines {
	l := &jdeLines{}
	lw := reconLabelWidth()
	width := s.bodyWidth()
	i := s.rowIdx
	if i < 0 || i >= len(s.items) {
		l.AddRow(0, jdeIndent+StyleMuted.Render("That row is gone."))
		return l
	}
	it := s.items[i]

	fields := []jdeField{
		{
			Label:   "Reason",
			Kind:    jdeChoice,
			Value:   reconChoiceValue(cycleCountReasons[s.rows[i].reasonIx].Label, lw, width),
			Focused: s.caretOnField(reconFieldReason),
		},
		{
			Label: "Reorder",
			Kind:  jdeChoice,
			// The value says what WILL happen rather than answering a negative.
			// "Skip reorder ..... < No >" is a double negative on the row that
			// decides whether this count files a purchase request, and the one
			// thing an operator must not misread here is which way round it is.
			Value:   reconChoiceValue(reconReorderChoice(s.rows[i].skipReorder), lw, width),
			Focused: s.caretOnField(reconFieldSkip),
		},
		{
			Label:   "Notes",
			Kind:    jdeText,
			Input:   &s.notes,
			Width:   30,
			Hint:    "optional",
			Focused: s.caretOnField(reconFieldNotes),
		},
	}
	if s.rowFields() > reconFieldOpen {
		fields = append(fields, jdeField{
			Label:   "Open",
			Kind:    jdeText,
			Input:   &s.open,
			Width:   9,
			Hint:    "open " + pluralUnit(it.Unit()) + ", blank leaves it",
			Focused: s.caretOnField(reconFieldOpen),
		})
	}
	// EACH FIELD IS EMITTED WITH ITS OWN CAVEAT DIRECTLY UNDER IT, one row at a
	// time, and the loop that used to add every field first and hang the caveats
	// off the end afterwards was wrong in two ways at once. It DREW the sentence
	// explaining the Reorder row underneath Notes, which is the reader's first
	// complaint; and jdeLines.block() spans a row's FIRST to LAST line, so a row
	// whose lines are not contiguous swallows every row emitted between them —
	// anchoring the window on the Reorder row would have windowed the Notes and
	// Open rows as part of it.
	//
	// The FIELD leads its own block, because Window keeps a block's START and a
	// sentence drawn ahead of a field is a row that pushes the focused box off a
	// short pane.
	caveats := map[int]string{
		reconFieldReason: "Row " + strconv.Itoa(i+1) + ": " +
			pickerClip(it.Name, reconNameCells) + " · counted in " + pluralUnit(it.Unit()),
		reconFieldSkip: reconSkipExplains,
	}
	for field, f := range fields {
		l.AddFittedFields([]jdeField{f}, lw, width, field)
		for _, line := range reconCaveatLines(caveats[field], StyleMuted, width) {
			l.AddRow(field, line)
		}
	}
	return l
}

// reconReorderChoice is the reorder row's value: what submitting this row will
// do, in the positive.
func reconReorderChoice(skip bool) string {
	if skip {
		return "Skip"
	}
	return "File if low"
}

// reconChoiceValue bounds a choice row's value to the pane.
//
// jdeFitRow returns early for a non-text row and jdePaneFieldWidth caps only an
// input area, so "shortening a choice value is a content decision each sheet
// makes for itself" — this is this sheet's. Without it a long reason label
// ("Used without scanning" is 21 cells inside its brackets) runs past the pane
// and clampToBox takes the tail with no mark, on the row that says what will be
// recorded against a stock change.
//
// The floor is ONE cell rather than zero: pickerClip of one cell is the
// ellipsis, and a value cut to a mark still says "there is a value here, and you
// are not seeing it". An empty pair of brackets says the opposite.
func reconChoiceValue(v string, labelWidth, bodyWidth int) string {
	if bodyWidth <= 0 {
		return v
	}
	// "< " and " >" are jdeFieldArea's own decoration around the value.
	const brackets = 4
	room := bodyWidth - len(jdeIndent) - labelWidth - len(jdeLeader) - brackets
	if room < 1 {
		room = 1
	}
	return pickerClip(v, room)
}

// reconSkipExplains says what the skip flag is FOR. It sits on the skip row
// rather than at the top of the sheet because a sentence ahead of the first
// field is a sentence a short pane drops while the field it explains stays.
const reconSkipExplains = "Skip files no reorder request for this row, however low the count lands."

// reviewBody is the read-only list of exactly what a submit would send.
func (s *LocationReconcileScreen) reviewBody() *jdeLines {
	l := &jdeLines{}
	width := s.paneWidth()
	rows := s.reviewRows()
	if len(rows) == 0 {
		for _, line := range reconCaveatLines("Nothing is counted, so there is nothing to submit.",
			StyleStatusWarn, width) {
			l.AddRow(0, line)
		}
		return l
	}
	for n, i := range rows {
		if n > 0 {
			l.AddRow(n-1, "")
		}
		qty, _ := s.counted(i)
		l.AddRow(n, jdeIndent+s.reviewHeading(n, i, qty, width))
		for _, line := range s.reviewDetailLines(i, qty, width) {
			l.AddRow(n, line)
		}
	}
	return l
}

// reviewHeading is one review row: its number, the item, and the counted
// quantity WITH ITS UNIT.
//
// The quantity is a FACT and never gives; the name abbreviates. A cut number
// would read as a different number, and this is the last frame before the write.
func (s *LocationReconcileScreen) reviewHeading(n, i, qty, width int) string {
	it := s.items[i]
	// The highlight's padding is reserved on every row, for the reason
	// itemHeading records.
	room := width - len(jdeIndent) - reconHighlightPad()
	if room < 1 {
		room = 1
	}
	lead := fmt.Sprintf("%2d ", n+1)
	fact := "  " + reconUnitPhrase(qty, it.Unit())
	nameRoom := room - lipgloss.Width(lead) - lipgloss.Width(fact)
	if nameRoom < 1 {
		nameRoom = 1
	}
	out := lead + pickerClip(it.Name, nameRoom) + fact
	if n == s.reviewCursor {
		return StyleSidebarItemActive.Render(fitCell(out, room))
	}
	return fitCell(out, room)
}

func (s *LocationReconcileScreen) reviewDetailLines(i, qty, width int) []string {
	it := s.items[i]
	st := s.rows[i]
	unit := it.Unit()
	parts := []string{cycleCountReasons[st.reasonIx].Label}
	if delta := qty - it.ProjectedAtUnit; delta != 0 {
		sign := "+"
		if delta < 0 {
			sign = "−"
			delta = -delta
		}
		parts = append(parts, fmt.Sprintf("%s%s", sign, reconUnitPhrase(delta, unit)))
	} else {
		parts = append(parts, "no change")
	}
	row := omsapi.ReconciliationRow{ActualCount: qty, SkipReorder: st.skipReorder}
	if row.FilesAReorder(it.MinimumStock) {
		parts = append(parts, "files a reorder")
	} else if st.skipReorder {
		parts = append(parts, "no reorder")
	}
	if n := strings.TrimSpace(st.notes); n != "" {
		parts = append(parts, poQuotedClip(n, reconQuotedCells))
	}
	if o := strings.TrimSpace(st.openCount); o != "" {
		parts = append(parts, o+" open")
	}
	return reconCaveatLines(strings.Join(parts, " · "), StyleMuted, width)
}

// doneBody is the summary of a landed batch.
func (s *LocationReconcileScreen) doneBody() *jdeLines {
	l := &jdeLines{}
	width := s.paneWidth()
	// FOLDED, not clipped. This line is what the visit produced and the figures
	// in it are the server's own, so it gives ground by taking another row
	// rather than by losing a number off the end of the pane.
	for _, line := range reconCaveatLines(s.doneHeadline(), StyleStatusOK, width) {
		l.AddRow(0, line)
	}
	if s.result != nil && s.result.ReordersCreated > 0 {
		for _, rec := range s.result.Reconciliations {
			if rec.TriggeredReorderID == nil {
				continue
			}
			for _, line := range reconCaveatLines("Reorder filed: "+
				pickerClip(rec.ItemName, reconNameCells), StyleMuted, width) {
				l.AddRow(0, line)
			}
		}
	}
	for _, line := range reconCaveatLines(reconDoneExplains, StyleMuted, width) {
		l.AddRow(0, line)
	}
	return l
}

// doneHeadline is what the batch DID, in the server's own figures. Nothing here
// is predicted: `reconciled` and `reorders_created` are what came back.
func (s *LocationReconcileScreen) doneHeadline() string {
	if s.result == nil {
		return "The count was recorded."
	}
	return fmt.Sprintf("%d row(s) recorded · %d reorder request(s) filed.",
		s.result.Reconciled, s.result.ReordersCreated)
}

const reconDoneExplains = "Stock is updated and the count is on each item's reconciliation history. r re-reads the room to count it again."

// ---------------------------------------------------------------------------
// The pinned header
// ---------------------------------------------------------------------------

// reconNoteRows is the note block's CEILING. It is a reservation rather than a
// measurement: the block is a pure function of the pane and never asks whether a
// note is currently held, so writing or retiring a note cannot move the pinned
// header by a row — and therefore cannot add or remove PgUp/PgDn from the bar
// drawn under it, which is the circle this constant exists to break.
//
// FIVE, because the review's standing note carries TWO facts an operator is
// being asked to decide on — how many reorder requests this submit will file,
// and that a refusal costs them nothing — and at 80 columns four rows is 196
// cells against the ~205 the shortest honest wording of both comes to. The
// fold takes the TAIL, so a four-row block reached the operator with the
// atomicity sentence cut to "Enter …". Shorten the SENTENCES before raising
// this again; both wordings here have already been cut once for it.
const reconNoteRows = 5

// reconNoteDropMark is what the note leaves behind when it does not fit. Every
// other bound on these screens that CUTS a value marks the cut, and the tail of
// one of these sentences is where the key that gets the operator out is named.
const reconNoteDropMark = " …"

// reconFailDetailRows is the CEILING of the failure detail. The sentence naming
// what failed is on the status row above it and never gives; what a short
// terminal loses here is the tail of the server's reason.
const reconFailDetailRows = 3

// reconBodyFloor is the rows the body keeps before the header gives anything up.
// A form that draws nothing is the worst outcome on this screen.
const reconBodyFloor = 3

// reconHeaderFloor is what the header cannot give up with nothing but a note in
// it: one row for the note — so a decline always has somewhere to be drawn — and
// the blank that keeps it off the body.
const reconHeaderFloor = 2

// reconHeaderAlloc is the header's row allocation on this pane, written ONCE and
// read by everything that asks. Two blocks with floors in one header cannot be
// budgeted separately: giving the note its whole ceiling first and handing the
// detail the remainder turns "the detail gives FIRST" into "the detail gives
// EVERYTHING", and on a short terminal that is a failed submit whose reason the
// operator never sees.
type reconHeaderAlloc struct{ note, detail int }

func (s *LocationReconcileScreen) headerBudget() int {
	return s.bodyAvailForBar(0, s.barCeiling())
}

func (s *LocationReconcileScreen) headerRoom() int {
	budget := s.headerBudget()
	floor := reconHeaderFloor
	if s.failDetailText() != "" {
		floor++
	}
	if room := budget - reconBodyFloor; room > floor {
		return room
	}
	if floor > budget-1 {
		floor = budget - 1
	}
	if floor < reconHeaderFloor {
		floor = reconHeaderFloor
	}
	return floor
}

// headerSplit divides headerRoom between the note and the failure detail.
//
// EVERY PARTICIPANT HAS A FLOOR and the giving is by degree rather than by
// elimination, in this order:
//
//	the status headline   never gives — it is on the status row, outside this
//	                      budget entirely
//	the note              one row, always: it is the answer to a keypress, and
//	                      a decline with nowhere to be drawn is a press the
//	                      operator gets no answer to
//	the failure detail    one row whenever a detail exists, so it loses its
//	                      TAIL and never itself
//	the separator         one row, so the block is not read as body
//	the body              everything left, and never nothing
//
// Then the surplus fills the NOTE to reconNoteRows before the DETAIL to
// reconFailDetailRows: what is served last is what is given up first.
func (s *LocationReconcileScreen) headerSplit() reconHeaderAlloc {
	want := 0
	if s.failDetailText() != "" {
		want = reconFailDetailRows
	}
	if s.headerBudget() <= 0 {
		// Unsized: the frame draws whole and Root's clampToBox decides, so
		// there is no geometry to divide.
		return reconHeaderAlloc{note: reconNoteRows, detail: want}
	}
	content := s.headerRoom() - 1 // the separator is not divisible
	note, detail := 1, 0
	if want > 0 && content >= note+1 {
		detail = 1
	}
	spare := content - note - detail
	if grow := reconNoteRows - note; grow > 0 && spare > 0 {
		if grow > spare {
			grow = spare
		}
		note += grow
		spare -= grow
	}
	if grow := want - detail; grow > 0 && spare > 0 {
		if grow > spare {
			grow = spare
		}
		detail += grow
	}
	return reconHeaderAlloc{note: note, detail: detail}
}

func (s *LocationReconcileScreen) noteRows() int       { return s.headerSplit().note }
func (s *LocationReconcileScreen) failDetailRows() int { return s.headerSplit().detail }

// headerLines is the screen's answer to the last keypress, PINNED above the
// scrollable body on every frame.
//
// Pinned rather than appended, because the answer is the one line that must not
// scroll away: inside the body it would sit below a cursor the operator had
// walked down a long item list, and a key that answers off the pane has not
// answered.
//
// The NOTE'S FIRST LINE is the one essential row, and no more: the smallest
// drawable budget on a screen with a pinned header keeps exactly one
// (jdeMinBudget), and a row marked essential that the geometry drops anyway is
// the same false claim jdeHeadRank exists to remove.
func (s *LocationReconcileScreen) headerLines() jdeHeader {
	out := jdeHeader(nil)
	for i, line := range s.noteLines() {
		rank := jdeHeadContext
		if i == 0 {
			rank = jdeHeadEssential
		}
		out = out.add(rank, line)
	}
	return out.add(jdeHeadContext, s.failDetailLines()...).add(jdeHeadDecorative, "")
}

// noteLines renders the answer to the last keypress into the rows noteRows
// reserves, folded to the pane the terminal really gave and padded out when it
// is shorter or absent — so the block's height is constant in everything a
// keypress controls.
func (s *LocationReconcileScreen) noteLines() []string {
	width := s.paneWidth() - len(jdeIndent)
	rows := s.noteRows()
	out := make([]string, 0, rows)
	for _, line := range s.fittedNote(width, rows).renderLines(width) {
		if len(out) == rows {
			break
		}
		out = append(out, jdeIndent+line)
	}
	for len(out) < rows {
		out = append(out, "")
	}
	return out
}

// fittedNote is the note shortened to the rows reserved for it, carrying the
// mark that says so — the TEXT bounded rather than the rendered lines, because
// renderLines emits styled runs and dropping one can take a closing SGR reset
// with it.
//
// The BUDGET is spent first, in one forward pass, before anything is folded:
// rows × width cells is everything that can be DRAWN, so folding what lies past
// it is work whose result is thrown away on a block rebuilt several times a
// frame. cellPrefix walks forward and stops when the budget is spent, so the
// cost is the budget rather than the length of what it was handed — and an
// OMS-supplied string reaching say() is one arm away.
//
// The LEAD SURVIVES in every branch. The word walk stops at keep > 0, so a text
// with no word boundary at all — a minified JSON body, which is exactly what
// omsapi.parseError hands over — falls through to the cell walk rather than to a
// note whose whole content is the drop mark. A note reading "…" names no key,
// which is the two-keys-one-pane defect the decline lead exists to prevent.
func (s *LocationReconcileScreen) fittedNote(width, rows int) pickerNote {
	note := s.shownNote()
	if note.text == "" || rows <= 0 || width <= 0 {
		return note
	}
	bounded := cellPrefix(note.text, rows*width)
	if bounded == note.text && len(note.renderLines(width)) <= rows {
		return note
	}
	words := strings.Fields(bounded)
	for keep := len(words) - 1; keep > 0; keep-- {
		trial := note
		trial.text = strings.Join(words[:keep], " ") + reconNoteDropMark
		if len(trial.renderLines(width)) <= rows {
			return trial
		}
	}
	for room := rows * width; room > 0; room-- {
		trial := note
		trial.text = cellPrefix(bounded, room) + reconNoteDropMark
		if len(trial.renderLines(width)) <= rows {
			return trial
		}
	}
	trial := note
	trial.text = strings.TrimSpace(reconNoteDropMark)
	return trial
}

// shownNote is what the note block draws: the answer to the last keypress
// whenever one is standing, and otherwise the phase's STANDING FACT.
//
// The answer wins, always. It is the reply to what the operator just pressed and
// it names the key, which is what keeps two declining keys from redrawing one
// pane; a standing fact that outranked it would be a keypress with no visible
// answer. The fact comes back on the very next press that writes none, because
// handleKey retires the note on every press.
func (s *LocationReconcileScreen) shownNote() pickerNote {
	if s.note.text != "" {
		return s.note
	}
	return s.standingNote()
}

// standingNote is the fact about the PHASE that a frame with nothing to answer
// draws instead of a blank row — so "nothing to say" and "the row scrolled away"
// are different states.
//
// On the review it is the atomicity of the write and the reorder forecast
// together, because those are the two things the operator is being asked to
// decide about and this is the last frame before the write.
func (s *LocationReconcileScreen) standingNote() pickerNote {
	switch s.phase {
	case reconReview:
		return pickerNote{text: s.reviewStanding(), level: StatusWarn}
	case reconCount:
		return pickerNote{text: reconUnitsStanding, level: StatusInfo}
	case reconRow:
		return pickerNote{text: reconRowStanding, level: StatusInfo}
	}
	return pickerNote{}
}

const reconRowStanding = "These apply to this row only. Every other row keeps its own reason and files its own reorder."

// reviewStanding is the review frame's standing fact: what the write DOES, what
// it costs if it fails, and how many reorder requests it will file.
//
// The reorder figure is named as a CEILING and the reason is given, because OMS
// also declines to file for a RETIRED item and the reconcile grid does not carry
// that flag. A number an operator acts on must carry its own uncertainty rather
// than borrow confidence from being printed.
func (s *LocationReconcileScreen) reviewStanding() string {
	rows := len(s.reviewRows())
	// THE REORDER FACT LEADS, and "at most" leads IT. The note block folds from
	// the TAIL, so whatever must survive must come first — and what must survive
	// here is the thing the operator is being asked to decide about: submitting
	// files purchase requests. Written the other way round (atomicity first) the
	// fold took "at most, since a retired item files none", leaving a bare count
	// of reorders stated as fact, which is the one claim this screen is not
	// entitled to make.
	var out string
	if n := s.reordersForecast(); n > 0 {
		out = fmt.Sprintf("At most %d %s here %s at or below minimum and will file a reorder request — %s",
			n, pluralizeUnit("row", n), reconIsAre(n), reconReorderCeiling)
	} else {
		out = "No row here is at or below its minimum, so no reorder request is filed."
	}
	return out + fmt.Sprintf(" Enter writes all %d at once: all land or none do, so a refusal keeps your counts.",
		rows)
}

func reconIsAre(n int) string {
	if n == 1 {
		return "is"
	}
	return "are"
}

// reconReorderCeiling is the honest limit of the forecast. OMS never files for a
// RETIRED item and `is_retired` is not on the reconcile grid payload, so this
// screen cannot tell a retired row from an ordinary one. The figure is therefore
// an upper bound, and saying so is the only alternative to inventing the fact.
const reconReorderCeiling = "a retired item files none and this grid cannot say which."

func (s *LocationReconcileScreen) failDetailLines() []string {
	detail := s.failDetailText()
	if detail == "" {
		return nil
	}
	width := s.paneWidth() - len(jdeIndent)
	if width < 12 {
		width = 12
	}
	var out []string
	for _, line := range failDetailLines(detail, width, s.failDetailRows()) {
		out = append(out, jdeIndent+StyleMuted.Render(line))
	}
	return out
}

// ---------------------------------------------------------------------------
// View
// ---------------------------------------------------------------------------

// workingLine names the work AND the subject: "Loading…" tells an operator
// nothing they could act on while a gateway thinks.
func (s *LocationReconcileScreen) workingLine() string {
	switch {
	case s.loading:
		return "Reading what " + s.locationName() + " holds…"
	case s.phase == reconReview:
		return fmt.Sprintf("Writing %d counted row(s) for %s…", len(s.reviewRows()), s.locationName())
	}
	return "Looking " + poQuotedClip(strings.TrimSpace(s.scan.Value()), reconQuotedCells) + " up…"
}

func (s *LocationReconcileScreen) View() string {
	// Both the working line and the failure headline go through the LAYER's
	// status row, which flattens a multi-line body and bounds it to the pane.
	// An nginx 502 page is seven lines, which omsapi.parseError hands over
	// whole, and one unwrapped line of the frame carrying it would push the
	// frame six rows over — clampToBox drops from the BOTTOM, so what goes is
	// the entire action bar.
	status := s.statusRow(s.pending || s.loading, s.workingLine(), "")
	if status == "" {
		status = s.statusRow(false, "", s.failHead)
	}
	body, cursor := s.body()
	header, items := s.headerLines(), s.bar()
	if s.scrolledPhase() {
		// The clamped offset is STORED, so a terminal dragged short and grown
		// again comes back where the operator left it rather than at the top.
		frame, offset := s.frameScrolled(header, body, s.sheetOffset, status, items)
		s.sheetOffset = offset
		return frame
	}
	return s.frameWrapped(header, body, cursor, status, items)
}
