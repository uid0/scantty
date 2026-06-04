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
// Hotkey: capital `K` (lowercase `k` is the universal up-arrow).
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
		case "tab", "shift+tab":
			// Swap focus between the in-progress panel and the active
			// checklists list. Only useful when both are non-empty.
			if len(s.completions) > 0 && len(s.rows) > 0 {
				s.focusCompletions = !s.focusCompletions
			}
		case "r":
			s.loading = true
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

func (s *ChecklistsScreen) View() string {
	if s.loading {
		return StyleMuted.Render("Loading checklists…")
	}
	if s.loadErr != "" {
		return StyleStatusError.Render("Error: ") + s.loadErr + "\n\n" + StyleMuted.Render("r retry · esc back")
	}

	var b strings.Builder
	if len(s.completions) > 0 {
		title := fmt.Sprintf("In-progress runs (%d)", len(s.completions))
		if s.focusCompletions {
			title += "  " + StyleMuted.Render("[focused]")
		}
		b.WriteString(StyleTitle.Render(title) + "\n")
		for i, c := range s.completions {
			label := c.ChecklistName
			if label == "" {
				label = "checklist " + c.Checklist[:8]
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
			caret := "  · "
			if s.focusCompletions && i == s.completionCursor {
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
			if s.focusCompletions && i == s.completionCursor {
				line = StyleSidebarItemActive.Render(line)
			}
			b.WriteString(line + "\n")
		}
		b.WriteString("\n")
	}

	if len(s.rows) == 0 {
		b.WriteString(StyleMuted.Render("No active checklists.") + "\n\n")
		b.WriteString(StyleMuted.Render("r refresh · esc back"))
		return b.String()
	}

	b.WriteString(StyleTitle.Render("Active checklists") + "\n")
	for i, r := range s.rows {
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
		b.WriteString(header + "\n")
		if r.Description != "" {
			desc := r.Description
			if len(desc) > 80 {
				desc = desc[:77] + "…"
			}
			b.WriteString("    " + StyleMuted.Render(desc) + "\n")
		}
	}
	footer := "j/k move · enter start a run"
	if s.focusCompletions {
		footer = "j/k move · enter open run · tab focus active"
	} else if len(s.completions) > 0 {
		footer = "j/k move · enter start a run · tab focus in-progress"
	}
	footer += " · r refresh · esc back"
	b.WriteString("\n" + StyleMuted.Render(footer))
	return b.String()
}
