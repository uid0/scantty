package tui

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/uid0/scantty/internal/omsapi"
)

// RecordChecklistsScreen lists the checklists with a step that scans ONE asset,
// inventory item or location, and starts a run of the one chosen — the terminal's
// half of the web's scan landing pages (AssetScanPage, ScanPage, LocationScanPage),
// which offer exactly that list under "Are you completing a checklist?". `K` on
// the asset, item and location detail sheets opens it.
//
// THE LIST IS THE SERVER'S AND SO IS EVERY REFUSAL. The three OMS actions behind
// it apply the reader filter themselves (omsapi.ListAssetChecklists carries it),
// so a checklist the reader may not run is ABSENT rather than drawn and refused,
// and nothing on this side keeps a copy of who may run what. The one refusal the
// list cannot prevent — the checklist was deactivated, or made private, between
// the list and the press — comes back from the start as a hand-written
// {"detail": …}, and the pane draws that sentence (checklistStartRefusal) rather
// than the raw JSON parseError hands over.
//
// THE RUN IS THE EXISTING ONE. A started run opens ChecklistRunScreen exactly as
// the global list's does; nothing about the run knows which record it was
// started from, because nothing about OMS's completion does either.
type RecordChecklistsScreen struct {
	deps   Deps
	record checklistRecord

	rows    []omsapi.ChecklistSummary
	cursor  int
	loading bool
	loadErr string

	// starting names the checklist whose start is out, "" while none is. The
	// start key comes off the bar for as long as it is set, and the head says why.
	starting string
	// startErr is the last refused start in the server's words. It rides the
	// head rather than a toast alone, because StatusBar.Flash expires and the
	// operator who looked away is still reading the pane. A new start or a
	// refresh clears it.
	startErr string

	terminalWidth  int
	terminalHeight int
	windowStart    int
}

// checklistRecord is the one record a RecordChecklistsScreen is about: what to
// call it, which workspace its sheets live in, and which of the three OMS routes
// lists its checklists.
type checklistRecord struct {
	noun string
	name string
	ws   Workspace
	list func(context.Context, *omsapi.Client) ([]omsapi.ChecklistSummary, error)
}

func assetChecklistRecord(id, name string) checklistRecord {
	return checklistRecord{noun: "asset", name: name, ws: WSAssets,
		list: func(ctx context.Context, c *omsapi.Client) ([]omsapi.ChecklistSummary, error) {
			return c.ListAssetChecklists(ctx, id)
		}}
}

func itemChecklistRecord(id, name string) checklistRecord {
	return checklistRecord{noun: "item", name: name, ws: WSInventory,
		list: func(ctx context.Context, c *omsapi.Client) ([]omsapi.ChecklistSummary, error) {
			return c.ListItemChecklists(ctx, id)
		}}
}

func locationChecklistRecord(id int, name string) checklistRecord {
	return checklistRecord{noun: "location", name: name, ws: WSInventory,
		list: func(ctx context.Context, c *omsapi.Client) ([]omsapi.ChecklistSummary, error) {
			return c.ListLocationChecklists(ctx, id)
		}}
}

type recordChecklistsLoadedMsg struct {
	rows []omsapi.ChecklistSummary
	err  error
}

type recordChecklistStartedMsg struct {
	run *omsapi.ChecklistCompletion
	err error
}

func NewAssetChecklistsScreen(deps Deps, assetID, name string) *RecordChecklistsScreen {
	return newRecordChecklistsScreen(deps, assetChecklistRecord(assetID, name))
}

func NewItemChecklistsScreen(deps Deps, itemID, name string) *RecordChecklistsScreen {
	return newRecordChecklistsScreen(deps, itemChecklistRecord(itemID, name))
}

func NewLocationChecklistsScreen(deps Deps, locationID int, name string) *RecordChecklistsScreen {
	return newRecordChecklistsScreen(deps, locationChecklistRecord(locationID, name))
}

func newRecordChecklistsScreen(deps Deps, record checklistRecord) *RecordChecklistsScreen {
	return &RecordChecklistsScreen{deps: deps, record: record, loading: true}
}

func (s *RecordChecklistsScreen) Title() string { return "Checklists: " + s.record.name }

func (s *RecordChecklistsScreen) Init() tea.Cmd { return s.load() }

func (s *RecordChecklistsScreen) ctx() context.Context {
	if s.deps.Ctx != nil {
		return s.deps.Ctx
	}
	return context.Background()
}

func (s *RecordChecklistsScreen) load() tea.Cmd {
	deps, ctx, list := s.deps, s.ctx(), s.record.list
	return func() tea.Msg {
		rows, err := list(ctx, deps.OMS)
		return recordChecklistsLoadedMsg{rows: rows, err: err}
	}
}

func (s *RecordChecklistsScreen) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		s.terminalWidth, s.terminalHeight = m.Width, m.Height
		return s, nil
	case recordChecklistsLoadedMsg:
		s.loading = false
		if m.err != nil {
			s.loadErr = m.err.Error()
			return s, nil
		}
		s.loadErr = ""
		s.rows = m.rows
		if s.cursor >= len(s.rows) {
			s.cursor = 0
		}
		return s, nil
	case recordChecklistStartedMsg:
		s.starting = ""
		if m.err != nil {
			s.startErr = checklistStartRefusal(m.err)
			return s, Status("checklist not started: "+s.startErr, StatusError)
		}
		s.startErr = ""
		return s, tea.Batch(
			Status(fmt.Sprintf("started %s · run %s", m.run.ChecklistName, cellPrefix(m.run.ID, 8)), StatusOK),
			SwitchTo(s.record.ws, NewChecklistRunScreen(s.deps, m.run.ID)),
		)
	case tea.KeyMsg:
		if proseLoadKeyHidden(s.loading, s.loadErr, s.loadBar(), m.String()) {
			return s, nil
		}
		switch m.String() {
		case "j", "down":
			if s.cursor < len(s.rows)-1 {
				s.cursor++
			}
		case "k", "up":
			if s.cursor > 0 {
				s.cursor--
			}
		case "r":
			s.loading = true
			s.loadErr = ""
			s.startErr = ""
			return s, s.load()
		case "enter":
			if !s.startOffered() {
				return s, nil
			}
			row := s.rows[s.cursor]
			s.starting, s.startErr = row.Name, ""
			deps, ctx := s.deps, s.ctx()
			return s, func() tea.Msg {
				run, err := deps.OMS.StartChecklist(ctx, row.ID, "")
				return recordChecklistStartedMsg{run: run, err: err}
			}
		}
	}
	return s, nil
}

// startOffered is the one predicate the bar and the enter arm both read: there
// is a checklist under the cursor and no start already out. A second start fired
// over the first would open a second run the operator never sees, because the
// first answer to land switches the screen away.
func (s *RecordChecklistsScreen) startOffered() bool {
	return s.cursor < len(s.rows) && s.starting == ""
}

// checklistStartRefusal is the sentence a refused start is reported in. OMS's
// start action writes every refusal it makes by hand as {"detail": "<prose>"}
// (403 "You do not have permission to start this checklist.", 400 "This
// checklist is not currently active." — both recorded in
// internal/omsapi/testdata), which never reaches the exception handler, so
// without AsDetailRefusal the operator reads the JSON. Anything else keeps the
// shape it arrived in.
func checklistStartRefusal(err error) string {
	if prose, ok := omsapi.AsDetailRefusal(err); ok {
		return prose
	}
	return err.Error()
}

// recordChecklistsBar names every key that acts on a list of `rows` checklists,
// as a record the honesty sweep can press (prose_bar.go). `enter` is named only
// where startOffered holds, so it comes off for an empty list and while a start
// is out, exactly where the arm declines.
func recordChecklistsBar(rows int, start bool) proseBar {
	out := proseNavStep(listNavMoves(rows))
	if start {
		out = append(out, proseBarItem{Keys: []string{"enter"}, Hint: "enter start a run"})
	}
	return append(out, proseBarRefresh, proseBarEsc)
}

// proseBar is the bar this screen is DRAWING, in every state: a load in flight or
// failed draws loadBar's.
func (s *RecordChecklistsScreen) proseBar() proseBar {
	if s.loading || s.loadErr != "" {
		return s.loadBar()
	}
	return recordChecklistsBar(len(s.rows), s.startOffered())
}

// loadBar is this list's bar while its load is out or has failed: the reload and
// the way back. A refresh keeps the rows, but `enter` is NOT named there and so
// is dropped (proseLoadKeyHidden) — starting a run on a checklist the frame no
// longer draws would open a run of something the operator cannot see they chose.
// The movement keys walk rows the frame does not draw, so they are dropped too.
func (s *RecordChecklistsScreen) loadBar() proseBar {
	return proseBar{proseBarReloadFor(s.loadErr != ""), proseBarEsc}
}

func (s *RecordChecklistsScreen) paneCells() int { return proseBarCells(s.terminalWidth) }

// head is what the list opens with: which record the checklists scan, and — while
// a start is out or after one was refused — the answer to the last `enter`.
//
// EVERY LINE OF IT IS BOUNDED, because every one carries an OMS value (a record
// name, a checklist name, a refusal sentence) and the window budgets the rows
// against the head's line count: a line that ran past the pane would be cut
// unmarked, and one that folded unbudgeted would push the bar off the bottom. The
// refusal gets TWO rows through failDetailLines, which marks its own cut — the
// recorded 403 is 51 cells, which is the whole pane at 80 columns before its mark.
func (s *RecordChecklistsScreen) head(cells int) string {
	var b strings.Builder
	b.WriteString(StyleTitle.Render(pickerClip("Checklists that scan this "+s.record.noun, cells)) + "\n")
	b.WriteString(StyleMuted.Render(pickerClip(s.record.name, cells)) + "\n")
	switch {
	case s.starting != "":
		b.WriteString(StyleMuted.Render(pickerClip("Starting "+s.starting+"…", cells)) + "\n")
	case s.startErr != "":
		for i, line := range failDetailLines(jdeStatusOneLine(s.startErr), cells-2, recordChecklistsRefusalRows) {
			lead := "  "
			if i == 0 {
				lead = StyleStatusError.Render("✗ ")
			}
			b.WriteString(lead + line + "\n")
		}
	}
	b.WriteString("\n")
	return b.String()
}

// recordChecklistsRefusalRows is how many rows a refused start's sentence gets on
// the head.
const recordChecklistsRefusalRows = 2

// View draws the checklists as a line-packed window (proseFlatListFrame) under
// the head, with the bar the window is budgeted around.
func (s *RecordChecklistsScreen) View() string {
	cells := s.paneCells()
	if s.loading {
		return proseLoadingFrame("Looking up the checklists for "+s.record.name+"…", cells, s.proseBar())
	}
	if s.loadErr != "" {
		return proseFailedFrame(s.loadErr, s.terminalHeight, cells, s.proseBar())
	}
	head := s.head(cells)
	if len(s.rows) == 0 {
		// The web hides the section outright when this list is empty; a terminal
		// the operator pressed a key to reach has to say what it found.
		empty := pickerWrap("No checklist you can run has a step on this "+s.record.noun+".", cells)
		return head + StyleMuted.Render(strings.Join(empty, "\n")) + "\n\n" + s.proseBar().render(cells)
	}
	rows := make([]string, len(s.rows))
	for i, r := range s.rows {
		rows[i] = checklistSummaryRow(i == s.cursor, r, cells)
	}
	return proseFlatListFrame(head, rows, s.cursor, &s.windowStart, s.terminalHeight,
		cells, recordChecklistsBar(proseFlatCeilingRows, true), s.proseBar())
}
