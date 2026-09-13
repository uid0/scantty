package tui

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/uid0/scantty/internal/forgekeyapi"
)

type UsageScreen struct {
	deps    Deps
	rows    []forgekeyapi.Usage
	cursor  int
	loading bool
	loadErr string

	terminalWidth  int
	terminalHeight int
	windowStart    int
}

type usageLoadedMsg struct {
	rows []forgekeyapi.Usage
	err  error
}

func NewUsageScreen(deps Deps) *UsageScreen {
	return &UsageScreen{deps: deps, loading: true}
}

func (s *UsageScreen) Title() string { return "Usage Sessions" }

func (s *UsageScreen) Init() tea.Cmd { return s.load() }

func (s *UsageScreen) load() tea.Cmd {
	deps := s.deps
	ctx := deps.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return func() tea.Msg {
		rows, err := deps.ForgeKey.ListUsage(ctx, nil)
		return usageLoadedMsg{rows: rows, err: err}
	}
}

// bar names every key that acts on a list of `rows` usage sessions, as a record
// the honesty sweep can press (prose_bar.go).
//
// It used to be the literal "j/k move · e end session · r refresh · esc back":
// the arrows moved the cursor unnamed, and it was written under every row with no
// window, so a list longer than the pane took the footer off the bottom. `e`
// needs a row to act on and is not offered without one; on a session already
// ended it answers why rather than acting, which is a key that says something.
func (s *UsageScreen) bar(rows int) proseBar {
	out := proseNavStep(listNavMoves(rows))
	if rows > 0 {
		out = append(out, proseBarItem{Keys: []string{"e"}, Hint: "e end session"})
	}
	return append(out, proseBarRefresh, proseBarEsc)
}

// proseBar is the bar this screen is DRAWING, in every state: a load in flight or
// failed draws loadBar's.
func (s *UsageScreen) proseBar() proseBar {
	if s.loading || s.loadErr != "" {
		return s.loadBar()
	}
	return s.bar(len(s.rows))
}

// loadBar is this list's bar while its load is out or has failed — what its key
// switch still answers with no rows drawn (prose_bar.go carries the defect and
// the decision). `e` still acts on the row a refresh kept under the cursor,
// which the frame no longer draws: named because it acts, and a candidate for
// gating.
func (s *UsageScreen) loadBar() proseBar {
	var out proseBar
	if len(s.rows) > 0 {
		out = append(out, proseBarItem{Keys: []string{"e"}, Hint: "e end session"})
	}
	return append(out, proseBarReloadFor(s.loadErr != ""), proseBarEsc)
}

func (s *UsageScreen) paneCells() int { return proseBarCells(s.terminalWidth) }

func (s *UsageScreen) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		s.terminalWidth, s.terminalHeight = m.Width, m.Height
		return s, nil
	case usageLoadedMsg:
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
		case "e":
			if s.cursor >= len(s.rows) {
				return s, nil
			}
			row := s.rows[s.cursor]
			if row.EndedAt != nil {
				return s, Status("session already ended", StatusWarn)
			}
			deps := s.deps
			ctx := deps.Ctx
			if ctx == nil {
				ctx = context.Background()
			}
			id := fmt.Sprint(row.ID)
			return s, func() tea.Msg {
				err := deps.ForgeKey.EndSession(ctx, id)
				return fkActionResultMsg{action: fmt.Sprintf("ended session %s", id), err: err}
			}
		}
	}
	return s, nil
}

func (s *UsageScreen) View() string {
	if s.loading {
		return proseLoadingFrame("Loading sessions…", s.paneCells(), s.proseBar())
	}
	if s.loadErr != "" {
		return proseFailedFrame(s.loadErr, s.terminalHeight, s.paneCells(), s.proseBar())
	}
	if len(s.rows) == 0 {
		return StyleMuted.Render("No sessions.") + "\n\n" + s.proseBar().render(s.paneCells())
	}
	var head strings.Builder
	active := 0
	for _, r := range s.rows {
		if r.EndedAt == nil {
			active++
		}
	}
	if active > 0 {
		head.WriteString(StyleStatusOK.Render(fmt.Sprintf("● %d active session(s)", active)) + "\n\n")
	}
	rows := make([]string, len(s.rows))
	for i, r := range s.rows {
		caret := "  "
		if i == s.cursor {
			caret = "▸ "
		}
		state := StyleStatusOK.Render("active")
		if r.EndedAt != nil {
			state = StyleMuted.Render("ended")
		}
		who := r.UserName
		if who == "" {
			who = fmt.Sprintf("user %v", r.User)
		}
		what := r.AssetName
		if what == "" {
			what = fmt.Sprintf("asset %v", r.Asset)
		}
		title := fmt.Sprintf("%s%s → %s  [%s]", caret, who, what, state)
		if i == s.cursor {
			title = StyleSidebarItemActive.Render(title)
		}
		if !r.StartedAt.IsZero() {
			line := "    " + StyleMuted.Render("started "+r.StartedAt.Format("01-02 15:04"))
			if r.EndedAt != nil {
				line += StyleMuted.Render(" · ended " + r.EndedAt.Format("01-02 15:04"))
			}
			title += "\n" + line
		}
		rows[i] = title
	}
	return proseFlatListFrame(head.String(), rows, s.cursor, &s.windowStart, s.terminalHeight,
		s.paneCells(), s.bar(proseFlatCeilingRows), s.proseBar())
}
