package tui

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/uid0/scantty/internal/omsapi"
)

// PMBoardScreen renders the urgency-sorted preventive-maintenance
// board: overdue tasks first, then warning, then ok, then never.
// `j/k` navigates, `enter` logs a "performed now" service entry
// (one-tap from the floor — a member tapping "I changed the filter"
// is the canonical use case).
//
// Hotkey: capital `P` (lowercase `p` is per-screen: asset_detail's
// "log problem", forgekey_device_detail's "ping"). Workspace:
// WSMaintenance.
type PMBoardScreen struct {
	deps    Deps
	board   *omsapi.PMBoard
	cursor  int
	loading bool
	loadErr string

	terminalWidth  int
	terminalHeight int
	windowStart    int
}

type pmBoardLoadedMsg struct {
	board *omsapi.PMBoard
	err   error
}

type pmServiceLoggedMsg struct {
	taskName string
	err      error
}

func NewPMBoardScreen(deps Deps) *PMBoardScreen {
	return &PMBoardScreen{deps: deps, loading: true}
}

func (s *PMBoardScreen) Title() string { return "PM dashboard" }

func (s *PMBoardScreen) Init() tea.Cmd { return s.load() }

func (s *PMBoardScreen) load() tea.Cmd {
	deps := s.deps
	ctx := deps.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return func() tea.Msg {
		board, err := deps.OMS.GetPMBoard(ctx)
		return pmBoardLoadedMsg{board: board, err: err}
	}
}

// schedules is the board's rows, or none before a board has come back.
func (s *PMBoardScreen) schedules() int {
	if s.board == nil {
		return 0
	}
	return len(s.board.Schedules)
}

// bar names every key that acts on a board of `rows` schedules, as a record the
// honesty sweep can press (prose_bar.go).
//
// It used to be the literal "j/k move · enter log service now · r refresh · esc
// back": the arrows moved the cursor unnamed, and it was written under every
// schedule with no window — two lines each, under a tier summary — so a backlog
// of a dozen took the footer off an 80x24 pane. `enter` needs a schedule to log
// against and is not offered without one.
func (s *PMBoardScreen) bar(rows int) proseBar {
	out := proseNavStep(listNavMoves(rows))
	if rows > 0 {
		out = append(out, proseBarItem{Keys: []string{"enter"}, Hint: "enter log service now"})
	}
	return append(out, proseBarRefresh, proseBarEsc)
}

// proseBar is the bar this screen is DRAWING, in every state: a load in flight or
// failed draws loadBar's.
func (s *PMBoardScreen) proseBar() proseBar {
	if s.loading || s.loadErr != "" {
		return s.loadBar()
	}
	return s.bar(s.schedules())
}

// loadBar is this list's bar while its load is out or has failed — what its key
// switch still answers with no rows drawn (prose_bar.go carries the defect and
// the decision). `enter` still acts on the row a refresh kept under the cursor,
// which the frame no longer draws: named because it acts, and a candidate for
// gating.
func (s *PMBoardScreen) loadBar() proseBar {
	var out proseBar
	if s.schedules() > 0 {
		out = append(out, proseBarItem{Keys: []string{"enter"}, Hint: "enter log service now"})
	}
	return append(out, proseBarReloadFor(s.loadErr != ""), proseBarEsc)
}

func (s *PMBoardScreen) paneCells() int { return proseBarCells(s.terminalWidth) }

func (s *PMBoardScreen) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		s.terminalWidth, s.terminalHeight = m.Width, m.Height
		return s, nil
	case pmBoardLoadedMsg:
		s.loading = false
		if m.err != nil {
			s.loadErr = m.err.Error()
			return s, nil
		}
		s.loadErr = ""
		s.board = m.board
		if s.board != nil && s.cursor >= len(s.board.Schedules) {
			s.cursor = 0
		}
		return s, nil
	case pmServiceLoggedMsg:
		if m.err != nil {
			return s, Status(fmt.Sprintf("log %s failed: %s", m.taskName, m.err.Error()), StatusError)
		}
		return s, tea.Batch(
			Status("logged "+m.taskName, StatusOK),
			s.load(),
		)
	case tea.KeyMsg:
		switch m.String() {
		case "j", "down":
			if s.board != nil && s.cursor < len(s.board.Schedules)-1 {
				s.cursor++
			}
		case "k", "up":
			if s.cursor > 0 {
				s.cursor--
			}
		case "r":
			s.loading = true
			return s, s.load()
		case "enter":
			if s.board == nil || s.cursor >= len(s.board.Schedules) {
				return s, nil
			}
			row := s.board.Schedules[s.cursor]
			deps := s.deps
			ctx := deps.Ctx
			if ctx == nil {
				ctx = context.Background()
			}
			return s, func() tea.Msg {
				_, err := deps.OMS.LogPMService(ctx, row.ID, "")
				return pmServiceLoggedMsg{
					taskName: fmt.Sprintf("%s · %s", row.AssetName, row.TaskName),
					err:      err,
				}
			}
		}
	}
	return s, nil
}

func (s *PMBoardScreen) View() string {
	if s.loading {
		return proseLoadingFrame("Loading PM board…", s.paneCells(), s.proseBar())
	}
	if s.loadErr != "" {
		return proseFailedFrame(s.loadErr, s.terminalHeight, s.paneCells(), s.proseBar())
	}
	if s.board == nil || len(s.board.Schedules) == 0 {
		return StyleMuted.Render("No active PM schedules.") + "\n\n" + s.proseBar().render(s.paneCells())
	}

	var head strings.Builder
	// Per-tier counters so the operator sees how big the backlog is at
	// a glance even before scrolling.
	counts := map[string]int{}
	for _, r := range s.board.Schedules {
		counts[r.Status]++
	}
	summary := []string{}
	if counts["overdue"] > 0 {
		summary = append(summary, StyleStatusError.Render(fmt.Sprintf("%d overdue", counts["overdue"])))
	}
	if counts["warning"] > 0 {
		summary = append(summary, StyleStatusWarn.Render(fmt.Sprintf("%d warning", counts["warning"])))
	}
	if counts["ok"] > 0 {
		summary = append(summary, StyleStatusOK.Render(fmt.Sprintf("%d ok", counts["ok"])))
	}
	if counts["never"] > 0 {
		summary = append(summary, StyleMuted.Render(fmt.Sprintf("%d never", counts["never"])))
	}
	if len(summary) > 0 {
		head.WriteString(strings.Join(summary, "  ") + "\n\n")
	}

	rows := make([]string, len(s.board.Schedules))
	for i, r := range s.board.Schedules {
		caret := "  "
		if i == s.cursor {
			caret = "▸ "
		}
		status := r.Status
		switch r.Status {
		case "overdue":
			status = StyleStatusError.Render(r.Status)
		case "warning":
			status = StyleStatusWarn.Render(r.Status)
		case "ok":
			status = StyleStatusOK.Render(r.Status)
		case "never":
			status = StyleMuted.Render(r.Status)
		}
		due := "—"
		if r.DaysUntilDue != nil {
			d := *r.DaysUntilDue
			if d < 0 {
				due = fmt.Sprintf("%dd over", -d)
			} else {
				due = fmt.Sprintf("in %dd", d)
			}
		}
		line := fmt.Sprintf("%s%s · %s  [%s] %s", caret, r.AssetName, r.TaskName, status, StyleMuted.Render(due))
		if i == s.cursor {
			line = StyleSidebarItemActive.Render(line)
		}
		meta := []string{fmt.Sprintf("every %dd", r.IntervalDays)}
		if r.LocationName != nil && *r.LocationName != "" {
			meta = append(meta, *r.LocationName)
		}
		if r.LastPerformedAt != nil {
			meta = append(meta, "last "+r.LastPerformedAt.Format("2006-01-02"))
		} else {
			meta = append(meta, "never performed")
		}
		rows[i] = line + "\n    " + StyleMuted.Render(strings.Join(meta, " · "))
	}
	return proseFlatListFrame(head.String(), rows, s.cursor, &s.windowStart, s.terminalHeight,
		s.paneCells(), s.bar(proseFlatCeilingRows), s.proseBar())
}
