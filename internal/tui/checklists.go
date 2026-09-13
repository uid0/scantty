package tui

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/uid0/scantty/internal/omsapi"
)

// ChecklistsScreen lists active checklists with their step counts and
// SIG ownership, plus a panel of recent in-progress runs so an
// operator can spot resumable work at a glance. The detail-view +
// step-scan + finalize flows that complete a checklist run are queued
// for v2 — this v1 is the list/start surface that the rest will plug
// into.
//
// Hotkey: capital `K` (lowercase `k` is the universal up-arrow). The
// Facilities menu entry rides the same capital K for that reason — its
// lowercase k is the menu's own cursor-up key, so an entry sitting there
// is unreachable by its own letter (sc-5dqy).
// Workspace: WSFacilities (checklists are shop-floor operations).
type ChecklistsScreen struct {
	deps             Deps
	rows             []omsapi.ChecklistSummary
	completions      []omsapi.ChecklistCompletion
	cursor           int
	completionCursor int
	focusCompletions bool
	loading          bool
	loadErr          string

	terminalWidth  int
	terminalHeight int
	windowStart    int
}

type checklistsLoadedMsg struct {
	rows        []omsapi.ChecklistSummary
	completions []omsapi.ChecklistCompletion
	err         error
}

type checklistStartedMsg struct {
	run *omsapi.ChecklistCompletion
	err error
}

func NewChecklistsScreen(deps Deps) *ChecklistsScreen {
	return &ChecklistsScreen{deps: deps, loading: true}
}

func (s *ChecklistsScreen) Title() string { return "Checklists" }

func (s *ChecklistsScreen) Init() tea.Cmd { return s.load() }

func (s *ChecklistsScreen) load() tea.Cmd {
	deps := s.deps
	ctx := deps.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return func() tea.Msg {
		out := checklistsLoadedMsg{}
		page, err := deps.OMS.ListChecklists(ctx, url.Values{"is_active": []string{"true"}})
		if err != nil {
			return checklistsLoadedMsg{err: err}
		}
		out.rows = page.Results
		// Recent in-progress runs — silently swallow any error so a stale
		// JWT doesn't block the rest of the screen.
		if runs, err := deps.OMS.ListChecklistCompletions(ctx, url.Values{
			"status":   []string{"in_progress"},
			"ordering": []string{"-started_at"},
		}); err == nil {
			out.completions = runs.Results
		}
		return out
	}
}

func (s *ChecklistsScreen) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		s.terminalWidth, s.terminalHeight = m.Width, m.Height
		return s, nil
	case checklistsLoadedMsg:
		s.loading = false
		if m.err != nil {
			s.loadErr = m.err.Error()
			return s, nil
		}
		s.loadErr = ""
		s.rows = m.rows
		s.completions = m.completions
		if s.cursor >= len(s.rows) {
			s.cursor = 0
		}
		if s.completionCursor >= len(s.completions) {
			s.completionCursor = 0
		}
		s.settleFocus()
		return s, nil
	case checklistStartedMsg:
		if m.err != nil {
			return s, Status("start failed: "+m.err.Error(), StatusError)
		}
		// Navigate straight into the stepper so the operator can begin
		// scanning steps. A status flash names the new run; the
		// in-progress panel will catch up on the next ChecklistsScreen
		// load.
		return s, tea.Batch(
			Status(fmt.Sprintf("started %s · run %s", m.run.ChecklistName, m.run.ID[:8]), StatusOK),
			SwitchTo(WSFacilities, NewChecklistRunScreen(s.deps, m.run.ID)),
		)
	case tea.KeyMsg:
		if proseLoadKeyHidden(s.loading, s.loadErr, s.loadBar(), m.String()) {
			return s, nil
		}
		switch m.String() {
		case "j", "down":
			if s.focusCompletions {
				if s.completionCursor < len(s.completions)-1 {
					s.completionCursor++
				}
			} else {
				if s.cursor < len(s.rows)-1 {
					s.cursor++
				}
			}
		case "k", "up":
			if s.focusCompletions {
				if s.completionCursor > 0 {
					s.completionCursor--
				}
			} else {
				if s.cursor > 0 {
					s.cursor--
				}
			}
		case "shift+tab":
			// Swap focus between the in-progress panel and the active
			// checklists list. Only useful when both are non-empty.
			//
			// SHIFT+TAB ALONE, because that is all that ever reached here: Root
			// answers a bare `tab` itself, before the screen, by moving the
			// keyboard into the sidebar (app.go), and this screen neither wants
			// raw input nor claims the key. The arm used to list `tab` too, and
			// the footer called it the swap — a key named for an action the app
			// never delivered. TestChecklists_TabReachesTheSidebarAndShiftTabSwaps
			// presses both through a real Root.
			if len(s.completions) > 0 && len(s.rows) > 0 {
				s.focusCompletions = !s.focusCompletions
			}
		case "r":
			s.loading = true
			s.loadErr = ""
			return s, s.load()
		case "enter":
			deps := s.deps
			ctx := deps.Ctx
			if ctx == nil {
				ctx = context.Background()
			}
			if s.focusCompletions {
				if s.completionCursor >= len(s.completions) {
					return s, nil
				}
				run := s.completions[s.completionCursor]
				return s, SwitchTo(WSFacilities, NewChecklistRunScreen(s.deps, run.ID))
			}
			if s.cursor >= len(s.rows) {
				return s, nil
			}
			row := s.rows[s.cursor]
			return s, func() tea.Msg {
				run, err := deps.OMS.StartChecklist(ctx, row.ID, "")
				return checklistStartedMsg{run: run, err: err}
			}
		}
	}
	return s, nil
}

// settleFocus puts the focus on a section that has rows, where exactly one of
// the two does.
//
// WITHOUT IT A LOADED LIST COULD STRAND ITS OWN ROWS. shift+tab swaps focus only
// while BOTH sections hold rows, and the focus was never moved off a section that
// came back empty — so a shop with in-progress runs and no active checklists
// opened with the focus on the empty half: the runs were drawn, and j/k, enter
// and shift+tab all did nothing to them. The same happened the other way round
// when a refresh emptied the in-progress panel the operator had swapped onto.
// Nothing about a key changes here — shift+tab still swaps, and only where both
// sections hold rows — the focus simply starts where the keys have rows to act on.
func (s *ChecklistsScreen) settleFocus() {
	switch {
	case len(s.completions) == 0:
		s.focusCompletions = false
	case len(s.rows) == 0:
		s.focusCompletions = true
	}
}

// focusedRows is how many rows the section holding the focus has — the rows
// j/k move through and enter acts on.
func (s *ChecklistsScreen) focusedRows() int {
	if s.focusCompletions {
		return len(s.completions)
	}
	return len(s.rows)
}

// checklistsBar names every key that acts on this list, as a record the honesty
// sweep can press (prose_bar.go): `focused` rows in the section with the focus,
// `both` when each section holds rows, and which section that is.
//
// It used to be one of three literals chosen by focus — "j/k move · enter start
// a run · tab focus in-progress · r refresh · esc back" and its two siblings —
// written under every row of BOTH sections with no window, so a shop with more
// checklists than the pane has rows took the footer off the bottom, and the
// arrows moved the cursor unnamed. It named `tab` as the swap, which Root takes
// to the sidebar before this screen sees it, and never named shift+tab, which is
// the key that swaps. Its movement and enter segments were also drawn over a
// section with nothing in it, where both do nothing; they follow the FOCUSED
// section's rows now, and shift+tab is named only where both sections hold rows,
// which is the only place it swaps.
func checklistsBar(focused int, both, focusRuns bool) proseBar {
	out := proseNavStep(listNavMoves(focused))
	if focused > 0 {
		enter := proseBarItem{Keys: []string{"enter"}, Hint: "enter start a run"}
		if focusRuns {
			enter.Hint = "enter open run"
		}
		out = append(out, enter)
	}
	if both {
		tab := proseBarItem{Keys: []string{"shift+tab"}, Hint: "shift+tab in-progress runs"}
		if focusRuns {
			tab.Hint = "shift+tab active checklists"
		}
		out = append(out, tab)
	}
	return append(out, proseBarRefresh, proseBarEsc)
}

// proseBar is the bar this screen is DRAWING, in every state: a load in flight or
// failed draws loadBar's.
func (s *ChecklistsScreen) proseBar() proseBar {
	if s.loading || s.loadErr != "" {
		return s.loadBar()
	}
	return checklistsBar(s.focusedRows(), len(s.rows) > 0 && len(s.completions) > 0, s.focusCompletions)
}

// ceiling is the TALLEST shape the bar takes, which the window is budgeted
// against for the reason proseListWindow gives. The two focus wordings differ
// in length, so it is whichever folds onto more rows at this pane: budgeted
// against the drawn one, shift+tab would change how many rows the list shows.
func (s *ChecklistsScreen) ceiling(cells int) proseBar {
	runs := checklistsBar(proseFlatCeilingRows, true, true)
	if active := checklistsBar(proseFlatCeilingRows, true, false); active.rows(cells) > runs.rows(cells) {
		return active
	}
	return runs
}

// loadBar is this list's bar while its load is out or has failed — what its key
// switch still answers with no rows drawn (prose_bar.go carries the defect and
// the decision). A refresh keeps both sections, so `enter` still starts a run
// on the checklist under the cursor, or opens the in-progress run: named because
// it acts, and a candidate for gating. j/k and shift+tab only move a cursor or a
// focus the frame does not draw, so they are not named and are ignored.
func (s *ChecklistsScreen) loadBar() proseBar {
	var out proseBar
	if s.focusedRows() > 0 {
		enter := proseBarItem{Keys: []string{"enter"}, Hint: "enter start a run"}
		if s.focusCompletions {
			enter.Hint = "enter open run"
		}
		out = append(out, enter)
	}
	return append(out, proseBarReloadFor(s.loadErr != ""), proseBarEsc)
}

func (s *ChecklistsScreen) paneCells() int { return proseBarCells(s.terminalWidth) }

// View draws both sections as ONE line-packed window (proseFlatListFrame) over
// their rows in order — the in-progress runs, then the active checklists — with
// each section's title riding its first row, so the window's arithmetic counts
// the title as the lines it really draws.
//
// ONE WINDOW, NOT ONE PER SECTION. The screen wrote every row of both sections
// and then the footer, so the footer was on the pane only while both sections
// together were shorter than it. A window per section would have to split the
// pane between them by some rule nobody chose; one window over both needs no
// such rule, because j/k moves the focused section's cursor and the window
// follows that row wherever it is — and `↑ more above` / `↓ N more below` mark
// whatever is cut, of either section, and the row a key brings back.
func (s *ChecklistsScreen) View() string {
	cells := s.paneCells()
	if s.loading {
		return proseLoadingFrame("Loading checklists…", cells, s.proseBar())
	}
	if s.loadErr != "" {
		return proseFailedFrame(s.loadErr, s.terminalHeight, cells, s.proseBar())
	}
	if len(s.rows) == 0 && len(s.completions) == 0 {
		return StyleMuted.Render("No active checklists.") + "\n\n" + s.proseBar().render(cells)
	}

	rows := make([]string, 0, len(s.completions)+len(s.rows)+1)
	for i, c := range s.completions {
		row := s.completionRow(i, c)
		if i == 0 {
			title := fmt.Sprintf("In-progress runs (%d)", len(s.completions))
			if s.focusCompletions {
				title += "  " + StyleMuted.Render("[focused]")
			}
			row = StyleTitle.Render(title) + "\n" + row
		}
		if i == len(s.completions)-1 {
			row += "\n"
		}
		rows = append(rows, row)
	}
	if len(s.rows) == 0 {
		rows = append(rows, StyleMuted.Render("No active checklists."))
	}
	for i, r := range s.rows {
		row := s.checklistRow(i, r, cells)
		if i == 0 {
			row = StyleTitle.Render("Active checklists") + "\n" + row
		}
		rows = append(rows, row)
	}
	cursor := len(s.completions) + s.cursor
	if s.focusCompletions {
		cursor = s.completionCursor
	}
	return proseFlatListFrame("", rows, cursor, &s.windowStart, s.terminalHeight,
		cells, s.ceiling(cells), s.proseBar())
}

// completionRow is one in-progress run.
func (s *ChecklistsScreen) completionRow(i int, c omsapi.ChecklistCompletion) string {
	label := c.ChecklistName
	if label == "" {
		// Bounded rather than sliced: a checklist id shorter than eight bytes
		// panicked the whole program here.
		label = "checklist " + cellPrefix(c.Checklist, 8)
	}
	progress := fmt.Sprintf("%d/%d", c.CompletedStepsCount, c.TotalStepsCount)
	required := ""
	if c.RequiredStepsTotal > 0 {
		required = fmt.Sprintf(" (req %d/%d)", c.RequiredStepsCompleted, c.RequiredStepsTotal)
	}
	who := c.UserUsername
	if who == "" {
		who = c.UserName
	}
	if who == "" {
		who = "anonymous"
	}
	ts := ""
	if c.StartedAt != nil {
		ts = " · started " + c.StartedAt.Format("01-02 15:04")
	}
	selected := s.focusCompletions && i == s.completionCursor
	caret := "  · "
	if selected {
		caret = "  ▸ "
	}
	line := fmt.Sprintf("%s%s  %s%s  %s%s",
		caret,
		label,
		StyleMuted.Render(progress),
		StyleMuted.Render(required),
		StyleMuted.Render(who),
		StyleMuted.Render(ts),
	)
	if selected {
		line = StyleSidebarItemActive.Render(line)
	}
	return line
}

// checklistRow is one active checklist, and its description under it.
//
// The description is CLIPPED TO THE PANE, line by line, with the cut marked. It
// used to be cut at 77 BYTES, which is neither the pane — 51 cells at 80 columns,
// so clampToBox took the rest of the line unmarked — nor a character boundary,
// so a multi-byte character straddling the cut drew as a broken glyph. Line by
// line because a stored newline is a line the window has to count.
func (s *ChecklistsScreen) checklistRow(i int, r omsapi.ChecklistSummary, cells int) string {
	const indent = "    "
	caret := "  "
	if i == s.cursor {
		caret = "▸ "
	}
	header := fmt.Sprintf("%s%s  %s", caret, r.Name, StyleMuted.Render(fmt.Sprintf("(%d steps)", r.StepCount)))
	if r.IsPublic {
		header += "  " + StyleMuted.Render("[public]")
	}
	if r.SIGName != "" {
		header += "  " + StyleMuted.Render("· "+r.SIGName)
	}
	if i == s.cursor {
		header = StyleSidebarItemActive.Render(header)
	}
	if r.Description == "" {
		return header
	}
	lines := strings.Split(r.Description, "\n")
	for j, line := range lines {
		lines[j] = indent + StyleMuted.Render(pickerClip(line, cells-len(indent)))
	}
	return header + "\n" + strings.Join(lines, "\n")
}
