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

func (s *UsageScreen) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch m := msg.(type) {
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
			id := row.ID
			return s, func() tea.Msg {
				err := deps.ForgeKey.EndSession(ctx, id)
				return fkActionResultMsg{action: fmt.Sprintf("ended session %d", id), err: err}
			}
		}
	}
	return s, nil
}

func (s *UsageScreen) View() string {
	if s.loading {
		return StyleMuted.Render("Loading sessions…")
	}
	if s.loadErr != "" {
		return StyleStatusError.Render("Error: ") + s.loadErr + "\n\n" + StyleMuted.Render("r retry · esc back")
	}
	if len(s.rows) == 0 {
		return StyleMuted.Render("No sessions.")
	}
	var b strings.Builder
	active := 0
	for _, r := range s.rows {
		if r.EndedAt == nil {
			active++
		}
	}
	if active > 0 {
		b.WriteString(StyleStatusOK.Render(fmt.Sprintf("● %d active session(s)", active)) + "\n\n")
	}
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
			who = fmt.Sprintf("user %d", r.User)
		}
		what := r.AssetName
		if what == "" {
			what = fmt.Sprintf("asset %d", r.Asset)
		}
		title := fmt.Sprintf("%s%s → %s  [%s]", caret, who, what, state)
		if i == s.cursor {
			title = StyleSidebarItemActive.Render(title)
		}
		b.WriteString(title + "\n")
		if !r.StartedAt.IsZero() {
			line := "    " + StyleMuted.Render("started "+r.StartedAt.Format("01-02 15:04"))
			if r.EndedAt != nil {
				line += StyleMuted.Render(" · ended "+r.EndedAt.Format("01-02 15:04"))
			}
			b.WriteString(line + "\n")
		}
	}
	b.WriteString("\n" + StyleMuted.Render("j/k move · e end session · r refresh · esc back"))
	return b.String()
}
