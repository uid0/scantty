// Location problems — per-location list + read-only detail + resolve action.
//
// TUI counterpart to the web LocationProblemsPanel + LocationProblemDetailPage
// (oms-sd1). Reached from the location detail screen with `p`. Lists the
// problems reported against one location (open/resolved/all filter, mirroring
// the web LocationProblemsListPage's SegmentedControl), lets an operator open a
// read-only detail of any report, file a new report (n → the report form), and
// resolve/close an open one (R → notes + resolved|closed → the resolve @action).
//
// The backend LocationProblemViewSet is read-only (no create/update/delete); the
// only writes are the report_problem create @action and this resolve @action, so
// there is deliberately NO edit or delete key here. Promote-to-work-order (the
// web detail page's staff action) is still deferred HERE: unlike the asset
// twin — see asset_problems.go, where the corrective work order anchors to the
// problem's asset and needs no picker — location promote-standard requires a
// MaintenanceItem uuid in the body, so it needs a picker this screen does not
// have. The vendor half is already built there and can be lifted across.
//
// n and f collide with global hotkeys (notifications / op-modes) and G with the
// categories surface, so the screen implements LocalKeyScreen to claim them; it
// flips to raw input while the detail or resolve overlay is up so esc / typing /
// tab land here instead of the root's global switch.
package tui

import (
	"context"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/uid0/scantty/internal/omsapi"
)

type lpFilter int

const (
	lpFilterOpen lpFilter = iota
	lpFilterResolved
	lpFilterAll
)

func (f lpFilter) label() string {
	switch f {
	case lpFilterResolved:
		return "resolved"
	case lpFilterAll:
		return "all"
	default:
		return "open"
	}
}

type LocationProblemsScreen struct {
	deps    Deps
	locID   int
	locName string

	rows           []omsapi.LocationProblem
	cursor         int
	windowStart    int
	windowSize     int
	loading        bool
	loadErr        string
	terminalHeight int
	filter         lpFilter

	// read-only detail overlay
	viewing bool

	// resolve overlay
	resolving         bool
	submittingResolve bool
	resolveTarget     string
	resolveNotes      textinput.Model
	resolveClosed     bool // false → mark resolved, true → mark closed
}

type locationProblemsLoadedMsg struct {
	rows []omsapi.LocationProblem
	err  error
}

type locationProblemResolvedMsg struct {
	problem *omsapi.LocationProblem
	err     error
}

// NewLocationProblemsScreen builds the problems list for one location. Default
// filter is "open" (the actionable set), matching the web list page default.
func NewLocationProblemsScreen(deps Deps, locID int, locName string) *LocationProblemsScreen {
	notes := textinput.New()
	notes.Prompt = ""
	notes.CharLimit = 1000
	notes.Placeholder = "optional resolution notes"
	return &LocationProblemsScreen{
		deps:         deps,
		locID:        locID,
		locName:      strings.TrimSpace(locName),
		loading:      true,
		windowSize:   18,
		filter:       lpFilterOpen,
		resolveNotes: notes,
	}
}

func (s *LocationProblemsScreen) Title() string {
	if s.locName != "" {
		return "Problems: " + s.locName
	}
	return "Location problems"
}

// WantsRawInput claims every key while the detail or resolve overlay is up so
// esc / typing / tab / enter land here instead of the global hotkeys.
func (s *LocationProblemsScreen) WantsRawInput() bool { return s.viewing || s.resolving }

// HandlesKey claims the action keys that collide with global hotkeys (n new,
// f filter, G bottom) so they reach this screen instead of the global nav switch.
func (s *LocationProblemsScreen) HandlesKey(key string) bool {
	return key == "n" || key == "f" || key == "G"
}

func (s *LocationProblemsScreen) Init() tea.Cmd { return s.load() }

func (s *LocationProblemsScreen) ctx() context.Context {
	if s.deps.Ctx != nil {
		return s.deps.Ctx
	}
	return context.Background()
}

func (s *LocationProblemsScreen) load() tea.Cmd {
	deps := s.deps
	ctx := s.ctx()
	locID := s.locID
	return func() tea.Msg {
		rows, err := deps.OMS.ListLocationProblems(ctx, omsapi.LocationProblemListParams{Location: locID})
		return locationProblemsLoadedMsg{rows: rows, err: err}
	}
}

// visible returns the rows matching the active filter. Navigation + selection
// operate on this filtered view, so the cursor always indexes a shown row.
func (s *LocationProblemsScreen) visible() []omsapi.LocationProblem {
	if s.filter == lpFilterAll {
		return s.rows
	}
	out := make([]omsapi.LocationProblem, 0, len(s.rows))
	for _, p := range s.rows {
		switch s.filter {
		case lpFilterResolved:
			if p.IsResolved() {
				out = append(out, p)
			}
		default: // open
			if !p.IsResolved() {
				out = append(out, p)
			}
		}
	}
	return out
}

func (s *LocationProblemsScreen) openCount() int {
	n := 0
	for _, p := range s.rows {
		if !p.IsResolved() {
			n++
		}
	}
	return n
}

func (s *LocationProblemsScreen) selected() (omsapi.LocationProblem, bool) {
	vis := s.visible()
	if s.cursor < 0 || s.cursor >= len(vis) {
		return omsapi.LocationProblem{}, false
	}
	return vis[s.cursor], true
}

func (s *LocationProblemsScreen) computeWindowSize() int {
	const chrome = 4
	avail := screenBodyHeight(s.terminalHeight) - chrome
	if avail < 3 {
		avail = 3
	}
	return avail
}

func (s *LocationProblemsScreen) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		s.terminalHeight = m.Height
		s.windowSize = s.computeWindowSize()
		s.scrollIntoView()
		return s, nil
	case locationProblemsLoadedMsg:
		s.loading = false
		if m.err != nil {
			s.loadErr = m.err.Error()
		} else {
			s.loadErr = ""
			s.rows = m.rows
		}
		s.clampCursor()
		s.windowSize = s.computeWindowSize()
		s.scrollIntoView()
		return s, nil
	case locationProblemResolvedMsg:
		s.submittingResolve = false
		s.resolving = false
		if m.err != nil {
			return s, Status("resolve failed: "+m.err.Error(), StatusError)
		}
		verb := "resolved"
		if m.problem != nil && m.problem.StatusDisplay != "" {
			verb = strings.ToLower(m.problem.StatusDisplay)
		}
		s.loading = true
		return s, tea.Batch(Status("problem "+verb, StatusOK), s.load())
	case tea.KeyMsg:
		if s.resolving {
			return s.updateResolve(m)
		}
		if s.viewing {
			return s.updateView(m)
		}
		return s.updateList(m)
	}
	return s, nil
}

func (s *LocationProblemsScreen) updateList(m tea.KeyMsg) (Screen, tea.Cmd) {
	vis := s.visible()
	switch m.String() {
	case "j", "down":
		if s.cursor < len(vis)-1 {
			s.cursor++
			s.scrollIntoView()
		}
	case "k", "up":
		if s.cursor > 0 {
			s.cursor--
			s.scrollIntoView()
		}
	case "pgdown":
		s.cursor += s.windowSize
		if s.cursor >= len(vis) {
			s.cursor = len(vis) - 1
		}
		if s.cursor < 0 {
			s.cursor = 0
		}
		s.scrollIntoView()
	case "pgup":
		s.cursor -= s.windowSize
		if s.cursor < 0 {
			s.cursor = 0
		}
		s.scrollIntoView()
	case "g", "home":
		s.cursor = 0
		s.scrollIntoView()
	case "G", "end":
		s.cursor = len(vis) - 1
		if s.cursor < 0 {
			s.cursor = 0
		}
		s.scrollIntoView()
	case "f":
		s.filter = (s.filter + 1) % 3
		s.cursor = 0
		s.windowStart = 0
		s.scrollIntoView()
	case "r":
		s.loading = true
		s.loadErr = ""
		return s, s.load()
	case "n":
		return s, SwitchTo(WSInventory, NewLocationProblemFormScreen(s.deps, s.locID, s.locName))
	case "v", "enter":
		if _, ok := s.selected(); ok {
			s.viewing = true
		}
	case "R":
		if p, ok := s.selected(); ok {
			if p.IsResolved() {
				return s, Status("already "+strings.ToLower(p.StatusDisplay), StatusWarn)
			}
			s.openResolve(p)
			return s, textinput.Blink
		}
	}
	return s, nil
}

func (s *LocationProblemsScreen) updateView(m tea.KeyMsg) (Screen, tea.Cmd) {
	switch m.String() {
	case "esc", "enter", "q", "v":
		s.viewing = false
	case "R":
		// Resolve straight from the detail view, mirroring the web detail page.
		if p, ok := s.selected(); ok && !p.IsResolved() {
			s.viewing = false
			s.openResolve(p)
			return s, textinput.Blink
		}
	}
	return s, nil
}

func (s *LocationProblemsScreen) openResolve(p omsapi.LocationProblem) {
	s.resolving = true
	s.resolveTarget = p.ID
	s.resolveClosed = false
	s.resolveNotes.SetValue("")
	s.resolveNotes.Focus()
}

func (s *LocationProblemsScreen) updateResolve(m tea.KeyMsg) (Screen, tea.Cmd) {
	if s.submittingResolve {
		return s, nil
	}
	switch m.String() {
	case "esc":
		s.resolving = false
		s.resolveNotes.Blur()
		return s, nil
	case "tab":
		s.resolveClosed = !s.resolveClosed
		return s, nil
	case "enter":
		return s.submitResolve()
	}
	var cmd tea.Cmd
	s.resolveNotes, cmd = s.resolveNotes.Update(m)
	return s, cmd
}

// resolveBody maps the overlay's toggle + notes into the resolve payload.
// Extracted so the resolved/closed mapping + notes trimming are unit-testable
// without a network round-trip.
func (s *LocationProblemsScreen) resolveBody() omsapi.LocationProblemResolve {
	st := omsapi.LocationProblemResolved
	if s.resolveClosed {
		st = omsapi.LocationProblemClosed
	}
	return omsapi.LocationProblemResolve{
		Status:          st,
		ResolutionNotes: strings.TrimSpace(s.resolveNotes.Value()),
	}
}

func (s *LocationProblemsScreen) submitResolve() (Screen, tea.Cmd) {
	body := s.resolveBody()
	s.submittingResolve = true
	deps := s.deps
	ctx := s.ctx()
	id := s.resolveTarget
	return s, func() tea.Msg {
		p, err := deps.OMS.ResolveLocationProblem(ctx, id, body)
		return locationProblemResolvedMsg{problem: p, err: err}
	}
}

func (s *LocationProblemsScreen) clampCursor() {
	if s.cursor >= len(s.visible()) {
		s.cursor = 0
	}
	if s.cursor < 0 {
		s.cursor = 0
	}
}

func (s *LocationProblemsScreen) scrollIntoView() {
	if s.windowSize <= 0 {
		s.windowSize = 18
	}
	n := len(s.visible())
	if s.cursor < s.windowStart {
		s.windowStart = s.cursor
	}
	if s.cursor >= s.windowStart+s.windowSize {
		s.windowStart = s.cursor - s.windowSize + 1
	}
	if s.windowStart < 0 {
		s.windowStart = 0
	}
	if n <= s.windowSize {
		s.windowStart = 0
	}
}

func (s *LocationProblemsScreen) View() string {
	if s.loading {
		return StyleMuted.Render("Loading problems…")
	}
	if s.loadErr != "" {
		return StyleStatusError.Render("Error: ") + s.loadErr + "\n\n" + StyleMuted.Render("r retry · esc back")
	}
	if s.resolving {
		return s.viewResolve()
	}
	if s.viewing {
		return s.viewDetail()
	}
	return s.viewList()
}

func (s *LocationProblemsScreen) viewList() string {
	var b strings.Builder
	header := s.locName
	if header == "" {
		header = "Location"
	}
	b.WriteString(StyleTitle.Render(header+" — problems") + "  " +
		StyleMuted.Render(fmt.Sprintf("[%s]  (%d open · %d total)", s.filter.label(), s.openCount(), len(s.rows))) + "\n")

	vis := s.visible()
	if len(vis) == 0 {
		b.WriteString("\n" + StyleMuted.Render("No "+s.filter.label()+" problems here.") + "\n\n")
		b.WriteString(StyleMuted.Render("n report · f filter · r refresh · esc back"))
		return b.String()
	}

	if s.windowStart > 0 {
		b.WriteString(StyleMuted.Render("  ↑ more above") + "\n")
	}
	end := s.windowStart + s.windowSize
	if end > len(vis) {
		end = len(vis)
	}
	for i := s.windowStart; i < end; i++ {
		b.WriteString(s.renderRow(vis, i) + "\n")
	}
	if end < len(vis) {
		b.WriteString(StyleMuted.Render(fmt.Sprintf("  ↓ %d more below", len(vis)-end)) + "\n")
	}
	b.WriteString("\n")
	b.WriteString(StyleMuted.Render("j/k move · n report · enter view · R resolve · f filter · r refresh · esc back"))
	return b.String()
}

func (s *LocationProblemsScreen) renderRow(vis []omsapi.LocationProblem, i int) string {
	p := vis[i]
	marker := "  "
	if i == s.cursor {
		marker = "▸ "
	}
	sev := lpSeverityStyle(p.Severity).Render("[" + strings.ToUpper(firstNonEmpty(p.SeverityDisplay, p.Severity)) + "]")
	desc := truncateOneLine(p.Description, 70)
	meta := []string{firstNonEmpty(p.StatusDisplay, p.Status)}
	if !p.ReportedAt.IsZero() {
		meta = append(meta, p.ReportedAt.Format("2006-01-02"))
	}
	if p.ReportedBy != "" {
		meta = append(meta, "by "+p.ReportedBy)
	}
	line := fmt.Sprintf("%s%s %s %s", marker, sev, desc, StyleMuted.Render("("+strings.Join(meta, " · ")+")"))
	if i == s.cursor {
		return StyleSidebarItemActive.Render(line)
	}
	return line
}

func (s *LocationProblemsScreen) viewDetail() string {
	p, ok := s.selected()
	if !ok {
		return StyleMuted.Render("No problem selected.") + "\n\n" + StyleMuted.Render("esc back")
	}
	var b strings.Builder
	b.WriteString(StyleTitle.Render(firstNonEmpty(p.LocationName, s.locName)) + "\n")
	b.WriteString(lpSeverityStyle(p.Severity).Render(firstNonEmpty(p.SeverityDisplay, p.Severity)) +
		"  " + StyleMuted.Render("status: ") + firstNonEmpty(p.StatusDisplay, p.Status) + "\n")
	if p.ReportedBy != "" || !p.ReportedAt.IsZero() {
		who := p.ReportedBy
		if who == "" {
			who = "anonymous"
		}
		when := ""
		if !p.ReportedAt.IsZero() {
			when = " on " + p.ReportedAt.Format("2006-01-02 15:04")
		}
		b.WriteString(StyleMuted.Render("reported by "+who+when) + "\n")
	}
	b.WriteString("\n" + StyleTitle.Render("Description") + "\n")
	b.WriteString(firstNonEmpty(p.Description, StyleMuted.Render("(none)")) + "\n")

	if p.IsResolved() || p.ResolutionNotes != "" {
		b.WriteString("\n" + StyleTitle.Render("Resolution") + "\n")
		if p.ResolutionNotes != "" {
			b.WriteString(p.ResolutionNotes + "\n")
		}
		if p.ResolvedBy != "" || p.ResolvedAt != nil {
			who := p.ResolvedBy
			when := ""
			if p.ResolvedAt != nil {
				when = " on " + p.ResolvedAt.Format("2006-01-02 15:04")
			}
			b.WriteString(StyleMuted.Render("resolved by "+firstNonEmpty(who, "?")+when) + "\n")
		}
	}

	if p.IsPromoted() {
		b.WriteString("\n" + StyleTitle.Render("Promoted to") + "\n")
		if p.WorkOrderShortID != "" {
			b.WriteString(StyleMuted.Render("PM work order: ") + p.WorkOrderShortID + "\n")
		}
		if p.ThirdPartyWorkOrderShortID != "" {
			b.WriteString(StyleMuted.Render("3rd-party work order: ") + p.ThirdPartyWorkOrderShortID + "\n")
		}
	}

	if p.PhotoURL != "" || p.PaperFormURL != "" {
		b.WriteString("\n" + StyleTitle.Render("Attachments") + "\n")
		if p.PhotoURL != "" {
			b.WriteString(StyleMuted.Render("photo: ") + p.PhotoURL + "\n")
		}
		if p.PaperFormURL != "" {
			b.WriteString(StyleMuted.Render("paper form: ") + p.PaperFormURL + "\n")
		}
	}

	b.WriteString("\n")
	if p.IsResolved() {
		b.WriteString(StyleMuted.Render("esc/enter back"))
	} else {
		b.WriteString(StyleMuted.Render("R resolve · esc/enter back"))
	}
	return b.String()
}

func (s *LocationProblemsScreen) viewResolve() string {
	var b strings.Builder
	b.WriteString(StyleTitle.Render("Resolve problem") + "\n")
	if p, ok := s.selected(); ok {
		b.WriteString(StyleMuted.Render(truncateOneLine(p.Description, 70)) + "\n")
	}
	b.WriteString("\n")
	if s.submittingResolve {
		b.WriteString(StyleMuted.Render("Resolving…"))
		return b.String()
	}
	b.WriteString(StyleTitle.Render("Resolution notes: ") + s.resolveNotes.View() + "\n")
	statusWord := "resolved"
	if s.resolveClosed {
		statusWord = "closed"
	}
	b.WriteString(StyleTitle.Render("Status: ") + "‹ " + statusWord + " ›" + "\n\n")
	b.WriteString(StyleMuted.Render("enter submit · tab toggle resolved/closed · esc cancel"))
	return b.String()
}

func lpSeverityStyle(severity string) lipgloss.Style {
	switch severity {
	case omsapi.LocationProblemSeverityUrgent:
		return StyleStatusError
	case omsapi.LocationProblemSeverityHigh:
		return StyleStatusWarn
	case omsapi.LocationProblemSeverityLow:
		return StyleMuted
	default:
		return StyleTitle
	}
}

// firstNonEmpty returns a if it is non-empty, else b — used to fall back from a
// serializer display label to the raw code when the label is absent.
func firstNonEmpty(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}
