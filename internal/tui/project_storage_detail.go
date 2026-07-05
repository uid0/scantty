package tui

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/uid0/scantty/internal/omsapi"
)

// ProjectStorageDetailScreen mirrors InventoryDetailScreen: it fetches one
// stint by stint_id, renders its lifecycle fields, and — because a terminal
// can't draw the printed PNG — reproduces the printed label as a faithful
// TEXT block ("LABEL PREVIEW") carrying the same field set the physical tag
// does.
type ProjectStorageDetailScreen struct {
	deps              Deps
	stintID           string
	stint             *omsapi.ProjectStorageStint
	loadErr           string
	loading           bool
	scroller          *TextScroller
	terminalHeight    int
	confirmingReprint bool
	reprinting        bool
}

type projectStorageDetailLoadedMsg struct {
	stint *omsapi.ProjectStorageStint
	err   error
}

type projectStorageReprintedMsg struct {
	err error
}

// WantsRawInput claims every keypress only while the re-print confirmation
// is up, so y/n/esc land here instead of the root's global hotkeys. In the
// normal view the screen stays non-raw so workspace switching and the
// global shortcuts keep working. Mirrors InventoryDetailScreen's delete
// confirm.
func (s *ProjectStorageDetailScreen) WantsRawInput() bool { return s.confirmingReprint }

func NewProjectStorageDetailScreen(deps Deps, stintID string) *ProjectStorageDetailScreen {
	return &ProjectStorageDetailScreen{
		deps:     deps,
		stintID:  stintID,
		loading:  true,
		scroller: NewTextScroller(defaultDetailHeight),
	}
}

func (s *ProjectStorageDetailScreen) Title() string {
	if s.stint != nil {
		return "Stint: " + s.stintID
	}
	return "Project Storage"
}

func (s *ProjectStorageDetailScreen) Init() tea.Cmd {
	deps := s.deps
	stintID := s.stintID
	ctx := deps.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return func() tea.Msg {
		st, err := deps.OMS.GetProjectStorageStint(ctx, stintID)
		return projectStorageDetailLoadedMsg{stint: st, err: err}
	}
}

func (s *ProjectStorageDetailScreen) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		s.terminalHeight = m.Height
		return s, nil
	case projectStorageDetailLoadedMsg:
		s.loading = false
		if m.err != nil {
			s.loadErr = m.err.Error()
		}
		s.stint = m.stint
		s.scroller.Set(s.renderBody())
		return s, nil
	case projectStorageReprintedMsg:
		s.reprinting = false
		s.confirmingReprint = false
		if m.err != nil {
			return s, Status("reprint failed: "+m.err.Error(), StatusError)
		}
		// Re-fetch so the new reprint audit event shows in the timeline and
		// the operator gets a fresh confirmation the queue re-surfaced it.
		s.loading = true
		s.loadErr = ""
		return s, tea.Batch(Status("queued for reprint", StatusOK), s.Init())
	case tea.KeyMsg:
		if s.confirmingReprint {
			return s.updateConfirmReprint(m)
		}
		if s.scroller.Handle(m) {
			return s, nil
		}
		switch m.String() {
		case "r":
			s.loading = true
			s.loadErr = ""
			return s, s.Init()
		case "p":
			// Re-print the claim ticket by re-surfacing the stint in the
			// Pi-daemon print queue. Guarded by a y/n confirm since it
			// spends label stock on the shop printer.
			if s.stint != nil {
				s.confirmingReprint = true
				return s, nil
			}
		}
	}
	return s, nil
}

// updateConfirmReprint handles the y/n prompt shown before re-queuing a
// stint's claim ticket. The screen is in raw-input mode here
// (WantsRawInput), so esc/n reach us instead of the root's global handlers.
func (s *ProjectStorageDetailScreen) updateConfirmReprint(m tea.KeyMsg) (Screen, tea.Cmd) {
	if s.reprinting {
		return s, nil
	}
	switch m.String() {
	case "y", "Y":
		if s.stint == nil {
			s.confirmingReprint = false
			return s, nil
		}
		s.reprinting = true
		deps := s.deps
		ctx := deps.Ctx
		if ctx == nil {
			ctx = context.Background()
		}
		stintID := s.stintID
		return s, func() tea.Msg {
			return projectStorageReprintedMsg{err: deps.OMS.ReprintProjectStorageStint(ctx, stintID, "reprint requested via ScanTTY")}
		}
	case "n", "N", "esc":
		s.confirmingReprint = false
	}
	return s, nil
}

func (s *ProjectStorageDetailScreen) View() string {
	if s.loading {
		return StyleMuted.Render("Loading stint…")
	}
	if s.loadErr != "" {
		return StyleStatusError.Render("Error: ") + s.loadErr + "\n\n" + StyleMuted.Render("press r to retry")
	}
	if s.stint == nil {
		return StyleMuted.Render("Stint not found.")
	}
	s.scroller.SetViewHeight(scrollerViewHeight(s.terminalHeight, detailFooterRows))
	if s.confirmingReprint {
		var prompt string
		if s.reprinting {
			prompt = StyleMuted.Render("Queuing reprint…")
		} else {
			prompt = StyleStatusWarn.Render("Re-print claim ticket for " + s.stintID + "?  y print · n/esc cancel")
		}
		return s.scroller.View() + "\n\n" + prompt
	}
	hint := "j/k scroll · pgup/pgdn page · p re-print ticket · r refresh · esc back"
	return s.scroller.View() + "\n\n" + StyleMuted.Render(hint)
}

func (s *ProjectStorageDetailScreen) renderBody() string {
	st := s.stint
	var b strings.Builder

	b.WriteString(StyleTitle.Render(projectStorageOwner(st)))
	if st.Status != "" {
		b.WriteString("  " + StyleMuted.Render("["+st.Status+"]"))
	}
	b.WriteString("\n")
	b.WriteString(StyleMuted.Render("Stint " + st.StintID))
	if st.Username != "" {
		b.WriteString(StyleMuted.Render(" · @" + st.Username))
	}
	if st.Email != "" {
		b.WriteString(StyleMuted.Render(" · " + st.Email))
	}
	b.WriteString("\n\n")

	b.WriteString(StyleTitle.Render("Project") + "\n")
	b.WriteString(projectTitleOrPersonal(st) + "\n\n")

	b.WriteString(StyleTitle.Render("Timeline") + "\n")
	if !st.StartedAt.IsZero() {
		b.WriteString(StyleMuted.Render("Started: ") + st.StartedAt.Format("2006-01-02") + "\n")
	}
	switch {
	case st.ExpiresAt != nil && !st.ExpiresAt.IsZero():
		b.WriteString(StyleMuted.Render("Expires: ") + st.ExpiresAt.Format("2006-01-02") +
			StyleMuted.Render(fmt.Sprintf("  (%s)", expiryWeekDay(st))) + "\n")
	case st.ExpiryWeek > 0 || st.ExpiryDayOfYear > 0:
		b.WriteString(StyleMuted.Render("Expiry: ") + expiryWeekDay(st) + "\n")
	}
	if st.NoticeSentAt != nil && !st.NoticeSentAt.IsZero() {
		b.WriteString(StyleMuted.Render("Notice sent: ") + st.NoticeSentAt.Format("2006-01-02") + "\n")
	}
	if st.MovedToPurgatoryAt != nil && !st.MovedToPurgatoryAt.IsZero() {
		b.WriteString(StyleMuted.Render("Moved to purgatory: ") + st.MovedToPurgatoryAt.Format("2006-01-02") + "\n")
	}
	if st.RemovedAt != nil && !st.RemovedAt.IsZero() {
		b.WriteString(StyleMuted.Render("Removed: ") + st.RemovedAt.Format("2006-01-02") + "\n")
	}
	b.WriteString("\n")

	b.WriteString(StyleTitle.Render("Location") + "\n")
	storage := st.StorageLocationName
	if storage == "" {
		storage = "—"
	}
	b.WriteString(StyleMuted.Render("Storage: ") + storage + "\n")
	if st.PurgatoryLocationName != "" {
		b.WriteString(StyleMuted.Render("Purgatory: ") + st.PurgatoryLocationName + "\n")
	}
	b.WriteString("\n")

	if strings.TrimSpace(st.Notes) != "" {
		b.WriteString(StyleTitle.Render("Notes") + "\n")
		b.WriteString(st.Notes + "\n\n")
	}

	if len(st.Events) > 0 {
		b.WriteString(StyleTitle.Render(fmt.Sprintf("Events (%d)", len(st.Events))) + "\n")
		for _, ev := range st.Events {
			line := "  · "
			if !ev.CreatedAt.IsZero() {
				line += ev.CreatedAt.Format("2006-01-02") + " "
			}
			line += ev.Action
			b.WriteString(line + "\n")
			meta := []string{}
			if ev.ActorUsername != "" {
				meta = append(meta, "by @"+ev.ActorUsername)
			}
			if ev.Notes != "" {
				meta = append(meta, ev.Notes)
			}
			if len(meta) > 0 {
				b.WriteString("    " + StyleMuted.Render(strings.Join(meta, " · ")) + "\n")
			}
		}
		b.WriteString("\n")
	}

	b.WriteString(s.renderLabelPreview())
	return b.String()
}

// renderLabelPreview reproduces the printed claim tag as text. It is NOT an
// attempt to draw the PNG — it carries the same field set the physical label
// prints (stint id, owner, project, expiry week/day, storage + purgatory
// locations, status) so an operator can verify a tag against the record from
// a terminal.
func (s *ProjectStorageDetailScreen) renderLabelPreview() string {
	st := s.stint
	lines := []string{
		"STINT    " + st.StintID,
		"OWNER    " + projectStorageOwner(st),
		"PROJECT  " + projectTitleOrPersonal(st),
		"EXPIRY   " + expiryWeekDay(st),
	}
	storage := st.StorageLocationName
	if storage == "" {
		storage = "—"
	}
	lines = append(lines, "STORAGE  "+storage)
	if st.PurgatoryLocationName != "" {
		lines = append(lines, "PURGTRY  "+st.PurgatoryLocationName)
	}
	lines = append(lines, "STATUS   "+st.Status)

	var b strings.Builder
	b.WriteString(StyleTitle.Render("LABEL PREVIEW") + "\n")
	b.WriteString(StyleMuted.Render("text of what the printed label carries — not the image itself") + "\n")
	b.WriteString(boxText(lines))
	return b.String()
}

// projectStorageOwner is the human name for a stint, preferring the
// backend-computed display_name and falling back to first/last then the
// bare username so the header/label never renders blank.
func projectStorageOwner(st *omsapi.ProjectStorageStint) string {
	if strings.TrimSpace(st.DisplayName) != "" {
		return st.DisplayName
	}
	if name := strings.TrimSpace(st.FirstName + " " + st.LastName); name != "" {
		return name
	}
	return st.Username
}

// projectTitleOrPersonal renders the project title, or "(personal)" for a
// blank title — matching the web overview's treatment of personal storage.
func projectTitleOrPersonal(st *omsapi.ProjectStorageStint) string {
	if strings.TrimSpace(st.ProjectTitle) != "" {
		return st.ProjectTitle
	}
	return "(personal)"
}

// expiryWeekDay formats the computed expiry as the label's "Wk NN / Day DDD"
// (week zero-padded to 2, day-of-year to 3).
func expiryWeekDay(st *omsapi.ProjectStorageStint) string {
	return fmt.Sprintf("Wk %02d / Day %03d", st.ExpiryWeek, st.ExpiryDayOfYear)
}

// boxText frames PLAIN (unstyled) text lines in a light box so the label
// preview reads like a physical tag. Lines must be unstyled — the frame
// width is measured with lipgloss.Width, so embedded ANSI would misalign
// the right border.
func boxText(lines []string) string {
	width := 0
	for _, l := range lines {
		if w := lipgloss.Width(l); w > width {
			width = w
		}
	}
	inner := width + 2 // one space of padding on each side
	var b strings.Builder
	b.WriteString("┌" + strings.Repeat("─", inner) + "┐\n")
	for _, l := range lines {
		pad := width - lipgloss.Width(l)
		b.WriteString("│ " + l + strings.Repeat(" ", pad) + " │\n")
	}
	b.WriteString("└" + strings.Repeat("─", inner) + "┘")
	return b.String()
}
