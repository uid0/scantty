package tui

import (
	"context"
	"fmt"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/uid0/scantty/internal/forgekeyapi"
)

type OperationalModesScreen struct {
	deps    Deps
	rows    []forgekeyapi.OperationalMode
	cursor  int
	loading bool
	loadErr string

	terminalWidth  int
	terminalHeight int
	windowStart    int
}

type opModesLoadedMsg struct {
	rows []forgekeyapi.OperationalMode
	err  error
}

func NewOperationalModesScreen(deps Deps) *OperationalModesScreen {
	return &OperationalModesScreen{deps: deps, loading: true}
}

func (s *OperationalModesScreen) Title() string { return "Operational Modes" }

func (s *OperationalModesScreen) Init() tea.Cmd { return s.load() }

func (s *OperationalModesScreen) load() tea.Cmd {
	deps := s.deps
	ctx := deps.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return func() tea.Msg {
		rows, err := deps.ForgeKey.ListOperationalModes(ctx, nil)
		return opModesLoadedMsg{rows: rows, err: err}
	}
}

// bar names every key that acts on a list of `rows` operational modes, as a
// record the honesty sweep can press (prose_bar.go).
//
// It used to be the literal "j/k move · c toggle classroom mode · r refresh · esc
// back": the arrows moved the cursor unnamed, and it was written under every row
// with no window, so a fleet longer than the pane took the footer off the bottom.
// `c` needs a row to act on and is not offered without one.
func (s *OperationalModesScreen) bar(rows int) proseBar {
	out := proseNavStep(listNavMoves(rows))
	if rows > 0 {
		out = append(out, proseBarItem{Keys: []string{"c"}, Hint: "c toggle classroom mode"})
	}
	return append(out, proseBarRefresh, proseBarEsc)
}

// proseBar is the bar this screen is DRAWING, in every state: a load in flight or
// failed draws loadBar's.
func (s *OperationalModesScreen) proseBar() proseBar {
	if s.loading || s.loadErr != "" {
		return s.loadBar()
	}
	return s.bar(len(s.rows))
}

// loadBar is this list's bar while its load is out or has failed — what its key
// switch still answers with no rows drawn (prose_bar.go carries the defect and
// the decision). `c` still acts on the row a refresh kept under the cursor,
// which the frame no longer draws: named because it acts, and a candidate for
// gating.
func (s *OperationalModesScreen) loadBar() proseBar {
	var out proseBar
	if len(s.rows) > 0 {
		out = append(out, proseBarItem{Keys: []string{"c"}, Hint: "c toggle classroom mode"})
	}
	return append(out, proseBarReloadFor(s.loadErr != ""), proseBarEsc)
}

func (s *OperationalModesScreen) paneCells() int { return proseBarCells(s.terminalWidth) }

func (s *OperationalModesScreen) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		s.terminalWidth, s.terminalHeight = m.Width, m.Height
		return s, nil
	case opModesLoadedMsg:
		s.loading = false
		if m.err != nil {
			s.loadErr = m.err.Error()
		}
		s.rows = m.rows
		if s.cursor >= len(s.rows) {
			s.cursor = 0
		}
		return s, nil
	case fkActionResultMsg:
		if m.err != nil {
			return s, Status(fmt.Sprintf("%s failed: %s", m.action, m.err.Error()), StatusError)
		}
		return s, tea.Batch(Status(m.action, StatusOK), s.load())
	case tea.KeyMsg:
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
			return s, s.load()
		case "c":
			if s.cursor >= len(s.rows) {
				return s, nil
			}
			row := s.rows[s.cursor]
			deps := s.deps
			ctx := deps.Ctx
			if ctx == nil {
				ctx = context.Background()
			}
			id := fmt.Sprint(row.ID)
			if row.ClassroomModeEnabled {
				return s, func() tea.Msg {
					err := deps.ForgeKey.DisableClassroomMode(ctx, id)
					return fkActionResultMsg{action: fmt.Sprintf("classroom mode OFF for asset %v", row.Asset), err: err}
				}
			}
			return s, func() tea.Msg {
				err := deps.ForgeKey.EnableClassroomMode(ctx, id)
				return fkActionResultMsg{action: fmt.Sprintf("classroom mode ON for asset %v", row.Asset), err: err}
			}
		}
	}
	return s, nil
}

func (s *OperationalModesScreen) View() string {
	if s.loading {
		return proseLoadingFrame("Loading operational modes…", s.paneCells(), s.proseBar())
	}
	if s.loadErr != "" {
		return proseFailedFrame(s.loadErr, s.terminalHeight, s.paneCells(), s.proseBar())
	}
	if len(s.rows) == 0 {
		return StyleMuted.Render("No operational modes.") + "\n\n" + s.proseBar().render(s.paneCells())
	}
	rows := make([]string, len(s.rows))
	for i, r := range s.rows {
		caret := "  "
		if i == s.cursor {
			caret = "▸ "
		}
		mode := r.Mode
		switch r.Mode {
		case "AVAILABLE":
			mode = StyleStatusOK.Render(r.Mode)
		case "LOCKED_OUT":
			mode = StyleStatusError.Render(r.Mode)
		case "CLASSROOM":
			mode = StyleStatusWarn.Render(r.Mode)
		}
		label := r.AssetName
		if label == "" {
			label = fmt.Sprintf("asset %v", r.Asset)
		}
		if r.AssetLocationName != "" {
			label = fmt.Sprintf("%s · %s", label, r.AssetLocationName)
		}
		title := fmt.Sprintf("%s%s  [%s]", caret, label, mode)
		if r.ClassroomModeEnabled {
			title += "  " + StyleStatusWarn.Render("📚 classroom")
		}
		if i == s.cursor {
			title = StyleSidebarItemActive.Render(title)
		}
		rows[i] = title
	}
	return proseFlatListFrame("", rows, s.cursor, &s.windowStart, s.terminalHeight,
		s.paneCells(), s.bar(proseFlatCeilingRows), s.proseBar())
}
