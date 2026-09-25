// PMDueScreen — preventive maintenance that is DUE: overdue, due this week, due
// this month, and one key that generates work orders for all of it.
//
// TUI counterpart to the web's PM Dashboard page (MaintenanceDashboard.tsx). It
// reads the two due lists OpenMakerSuite serves and draws them in the web's
// three sections, in the web's order:
//
//	Overdue            the week list's items flagged is_overdue
//	Due this week      the rest of the week list
//	Due this month     the month list less anything already in the week's
//
// THE MONTH LIST IS A SUPERSET OF THE WEEK'S, so drawing both whole would show
// every weekly item twice — which is why the third section is a difference and
// not a list. And both windows are ROLLING from now, not calendar periods, and
// both carry the past: an overdue item, or one never completed, is "due this
// week" to the server (omsapi.ListMaintenanceItemsDueThisWeek carries the note).
//
// GENERATING IS THE SERVER'S CHOICE OF ITEMS, NOT THE OPERATOR'S. The web's
// "Generate due WOs" posts `{}` and the server picks the week list's predicate
// itself, skipping any item that already has an open or in-progress work order —
// and the reply is a count, with no word about what it skipped. So nothing here
// selects rows for the write, the confirm names the count the operator is
// LOOKING AT and says the server may create fewer, and the result states both
// numbers rather than implying the difference was a failure. The key is offered
// to every signed-in operator because the endpoint and the web both are.
//
// Keys:
//
//	j/k ↑↓     move
//	enter      open the PM item (single-item generate, complete, edit live there)
//	a          open the item's asset — the web's "View Asset"
//	W          generate work orders for everything due this week (confirm: y / n)
//	r          refresh · esc back
package tui

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/uid0/scantty/internal/omsapi"
)

// pmDueSection is which of the web's three sections a row sits in.
type pmDueSection int

const (
	pmDueOverdue pmDueSection = iota
	pmDueThisWeek
	pmDueThisMonth
)

type pmDueRow struct {
	item    omsapi.MaintenanceItem
	section pmDueSection
}

type PMDueScreen struct {
	deps Deps

	rows []pmDueRow
	// weekCount is how many items the server's week list held when it was last
	// read — the set the bulk generation acts on, and the count its confirm
	// names.
	weekCount int
	cursor    int
	loading   bool
	loadErr   string

	confirming bool
	generating bool
	// note is the answer to the last generation, on the PANE as well as the
	// status toast — the toast expires and the operator who looked away is
	// still looking at this list.
	note      string
	noteLevel StatusLevel

	now func() time.Time

	terminalWidth  int
	terminalHeight int
	windowStart    int
}

type pmDueLoadedMsg struct {
	week, month []omsapi.MaintenanceItem
	err         error
}

type pmDueGeneratedMsg struct {
	result *omsapi.MaintenanceBulkGeneration
	due    int
	err    error
}

func NewPMDueScreen(deps Deps) *PMDueScreen {
	return &PMDueScreen{deps: deps, loading: true, now: time.Now}
}

func (s *PMDueScreen) Title() string { return "PM due" }

// WantsRawInput claims the keyboard only while the confirm is up, so `esc`
// cancels it rather than leaving the screen, and `n` is not read as anything
// else. The list stays non-raw so the sidebar keeps working.
func (s *PMDueScreen) WantsRawInput() bool { return s.confirming }

func (s *PMDueScreen) Init() tea.Cmd { return s.load() }

func (s *PMDueScreen) ctx() context.Context {
	if s.deps.Ctx != nil {
		return s.deps.Ctx
	}
	return context.Background()
}

// load reads BOTH lists, as the web does, and reports a failure of either as a
// failure of the screen: half a picture of what is due would draw "nothing
// overdue" over a week list that simply did not arrive.
func (s *PMDueScreen) load() tea.Cmd {
	deps, ctx := s.deps, s.ctx()
	return func() tea.Msg {
		week, err := deps.OMS.ListMaintenanceItemsDueThisWeek(ctx)
		if err != nil {
			return pmDueLoadedMsg{err: err}
		}
		month, err := deps.OMS.ListMaintenanceItemsDueThisMonth(ctx)
		if err != nil {
			return pmDueLoadedMsg{err: err}
		}
		return pmDueLoadedMsg{week: week, month: month}
	}
}

// pmDueSections sorts the two lists into the web's three sections. Within a
// section the server's own order is kept.
func pmDueSections(week, month []omsapi.MaintenanceItem) []pmDueRow {
	inWeek := make(map[string]bool, len(week))
	var overdue, thisWeek, thisMonth []pmDueRow
	for _, it := range week {
		inWeek[it.ID] = true
		if it.IsOverdue {
			overdue = append(overdue, pmDueRow{item: it, section: pmDueOverdue})
		} else {
			thisWeek = append(thisWeek, pmDueRow{item: it, section: pmDueThisWeek})
		}
	}
	for _, it := range month {
		if !inWeek[it.ID] {
			thisMonth = append(thisMonth, pmDueRow{item: it, section: pmDueThisMonth})
		}
	}
	out := append(overdue, thisWeek...)
	return append(out, thisMonth...)
}

func (s *PMDueScreen) sectionCount(sec pmDueSection) int {
	n := 0
	for _, r := range s.rows {
		if r.section == sec {
			n++
		}
	}
	return n
}

func (s *PMDueScreen) addressed() (pmDueRow, bool) {
	if s.cursor < 0 || s.cursor >= len(s.rows) {
		return pmDueRow{}, false
	}
	return s.rows[s.cursor], true
}

// generateOffered is the ONE expression behind `W`: the bar names it where it
// holds and the arm acts only where it holds. An empty week list would ask the
// server to create nothing, and a write already out would be a second run racing
// the first.
func (s *PMDueScreen) generateOffered() bool {
	return !s.loading && s.loadErr == "" && s.weekCount > 0 && !s.generating
}

func (s *PMDueScreen) refreshOffered() bool {
	return !s.generating
}

func (s *PMDueScreen) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		s.terminalWidth, s.terminalHeight = m.Width, m.Height
		return s, nil

	case pmDueLoadedMsg:
		s.loading = false
		if m.err != nil {
			s.loadErr = m.err.Error()
			return s, nil
		}
		s.loadErr = ""
		prev, had := s.addressed()
		s.rows = pmDueSections(m.week, m.month)
		s.weekCount = len(m.week)
		s.cursor = 0
		if had {
			for i, r := range s.rows {
				if r.item.ID == prev.item.ID {
					s.cursor = i
					break
				}
			}
		}
		return s, nil

	case pmDueGeneratedMsg:
		s.generating = false
		if m.err != nil {
			s.note = "nothing generated: " + omsRefusalSentence(m.err)
			s.noteLevel = StatusError
			return s, Status("generate work orders failed: "+omsRefusalSentence(m.err), StatusError)
		}
		s.note, s.noteLevel = pmDueResultNote(m.result.Created, m.due), StatusOK
		s.loading = true
		return s, tea.Batch(Status(s.note, StatusOK), s.load())

	case tea.KeyMsg:
		if s.confirming {
			return s.updateConfirm(m)
		}
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
			if s.refreshOffered() {
				s.loading = true
				return s, s.load()
			}
		case "enter":
			if row, ok := s.addressed(); ok {
				return s, SwitchTo(WSMaintenance, NewMaintenanceItemDetailScreen(s.deps, row.item.ID))
			}
		case "a":
			if row, ok := s.addressed(); ok && row.item.Asset != "" {
				return s, SwitchTo(WSAssets, NewAssetDetailScreen(s.deps, row.item.Asset))
			}
		case "W":
			if s.generateOffered() {
				s.confirming = true
				s.note = ""
			}
		}
	}
	return s, nil
}

func (s *PMDueScreen) updateConfirm(m tea.KeyMsg) (Screen, tea.Cmd) {
	switch m.String() {
	case "y":
		due := s.weekCount
		s.confirming = false
		s.generating = true
		deps, ctx := s.deps, s.ctx()
		return s, func() tea.Msg {
			res, err := deps.OMS.GenerateDueMaintenanceWorkOrders(ctx)
			return pmDueGeneratedMsg{result: res, due: due, err: err}
		}
	case "n", "esc":
		s.confirming = false
	}
	return s, nil
}

// pmDueResultNote words the server's count against the count the operator
// confirmed. FEWER IS NOT A FAILURE and must not read as one: the server skips an
// item that already has an open or in-progress work order and says nothing about
// it, and the list may also have moved between the read and the write. So both
// numbers are stated and the reason is the server's documented rule, offered as
// the explanation it is rather than as a per-item fact nobody reported.
func pmDueResultNote(created, due int) string {
	word := "work orders"
	if created == 1 {
		word = "work order"
	}
	if created == due {
		return fmt.Sprintf("Created %d %s.", created, word)
	}
	return fmt.Sprintf("Created %d %s for %d due · the server skips items that already "+
		"have an open or in-progress work order.", created, word, due)
}

// omsRefusalSentence is the server's own sentence for a refused write — the
// field's sentence from the validation envelope, a hand-written `detail`, or the
// coded envelope's message — and the whole error only where it is none of those.
func omsRefusalSentence(err error) string {
	if msg, ok := omsapi.AsFieldRefusal(err); ok {
		return msg
	}
	if msg, ok := omsapi.AsDetailRefusal(err); ok {
		return msg
	}
	var api *omsapi.APIError
	if errors.As(err, &api) && api.Code != "" && api.Message != "" {
		return api.Message
	}
	return err.Error()
}

// ---------------------------------------------------------------------------
// Bars
// ---------------------------------------------------------------------------

// bar names every key that acts on a list of `rows` due items. `enter` and `a`
// need a row to act on; `W` is generateOffered's to decide.
func (s *PMDueScreen) bar(rows int) proseBar {
	out := proseNavStep(listNavMoves(rows))
	if rows > 0 {
		out = append(out,
			proseBarItem{Keys: []string{"enter"}, Hint: "enter open"},
			proseBarItem{Keys: []string{"a"}, Hint: "a asset"},
		)
	}
	if s.generateOffered() {
		out = append(out, proseBarItem{Keys: []string{"W"}, Hint: "W generate due WOs"})
	}
	if s.refreshOffered() {
		out = append(out, proseBarRefresh)
	}
	return append(out, proseBarEsc)
}

// ceilingBar is the tallest shape the list's bar takes — every segment on — so
// the window is budgeted against a fixed point (proseListWindow carries why).
func (s *PMDueScreen) ceilingBar() proseBar {
	return append(proseNavStep(true),
		proseBarItem{Keys: []string{"enter"}, Hint: "enter open"},
		proseBarItem{Keys: []string{"a"}, Hint: "a asset"},
		proseBarItem{Keys: []string{"W"}, Hint: "W generate due WOs"},
		proseBarRefresh, proseBarEsc)
}

// pmDueConfirmBar is the confirm's own keys, as a record like every other state's.
var pmDueConfirmBar = proseBar{
	{Keys: []string{"y"}, Hint: "y generate"},
	{Keys: []string{"n", "esc"}, Hint: "n/esc cancel"},
}

// proseBar is the bar this screen is DRAWING, in every state: the confirm's,
// loadBar's while a load is out or has failed, and the list's otherwise.
func (s *PMDueScreen) proseBar() proseBar {
	switch {
	case s.confirming:
		return pmDueConfirmBar
	case s.loading || s.loadErr != "":
		return s.loadBar()
	}
	return s.bar(len(s.rows))
}

// loadBar is the bar while a load is out or has failed. Only the reload and the
// way back are named, and every other key is IGNORED there (proseLoadKeyHidden):
// the rows a refresh keeps are not drawn, so opening one or arming a generation
// against a count the frame does not show would act on something unseen.
func (s *PMDueScreen) loadBar() proseBar {
	return proseBar{proseBarReloadFor(s.loadErr != ""), proseBarEsc}
}

func (s *PMDueScreen) paneCells() int { return proseBarCells(s.terminalWidth) }

// ---------------------------------------------------------------------------
// View
// ---------------------------------------------------------------------------

func (s *PMDueScreen) View() string {
	cells := s.paneCells()
	if s.loading {
		return proseLoadingFrame("Reading what PM is due…", cells, s.proseBar())
	}
	if s.loadErr != "" {
		return proseFailedFrame(s.loadErr, s.terminalHeight, cells, s.proseBar())
	}

	var head strings.Builder
	for _, line := range s.headLines(cells) {
		head.WriteString(line + "\n")
	}
	if len(s.rows) == 0 {
		return head.String() + StyleMuted.Render(pickerClip("Nothing is due in the next 30 days.", cells)) +
			"\n\n" + s.proseBar().render(cells)
	}

	rows := make([]string, len(s.rows))
	for i := range s.rows {
		rows[i] = s.renderRow(i, cells)
	}
	footRows := s.ceilingBar().rows(cells)
	foot := s.proseBar().render(cells)
	if s.confirming {
		lines := s.confirmLines(cells)
		foot = strings.Join(lines, "\n") + "\n\n" + foot
		footRows = len(lines) + 1 + pmDueConfirmBar.rows(cells)
		if ceiling := s.ceilingBar().rows(cells); footRows < ceiling {
			footRows = ceiling
		}
	}
	return proseFlatListFrameFoot(head.String(), rows, s.cursor, &s.windowStart, s.terminalHeight, footRows, foot)
}

// headLines is the count summary — the web's stat cards — and the answer to the
// last generation, each bounded to one row of the pane.
func (s *PMDueScreen) headLines(cells int) []string {
	var out []string
	if s.generating {
		out = append(out, StyleMuted.Render(pickerClip(
			fmt.Sprintf("Generating work orders for %d due PM items…", s.weekCount), cells)))
	} else if s.note != "" {
		style := StyleStatusOK
		if s.noteLevel == StatusError {
			style = StyleStatusError
		}
		out = append(out, style.Render(pickerClip(s.note, cells)))
	}
	summary := fmt.Sprintf("%d overdue · %d due this week · %d more this month",
		s.sectionCount(pmDueOverdue), s.sectionCount(pmDueThisWeek), s.sectionCount(pmDueThisMonth))
	out = append(out, StyleMuted.Render(pickerClip(summary, cells)))
	return out
}

// confirmLines is the question and what the server does with it, folded to the
// pane. The COUNT leads the question, so a narrow pane that folds it still has
// the number on its first row.
func (s *PMDueScreen) confirmLines(cells int) []string {
	q := fmt.Sprintf("Generate work orders for %d PM items overdue or due in the next 7 days?", s.weekCount)
	lines := []string{}
	for _, l := range pickerWrap(q, cells) {
		lines = append(lines, StyleStatusWarn.Render(l))
	}
	for _, l := range pickerWrap("The server skips any that already have an open or in-progress work order.", cells) {
		lines = append(lines, StyleMuted.Render(l))
	}
	return lines
}

// pmDueBadge is the web's badge for a row: `Nd overdue` (or `Overdue` for an
// item with no date to be late against — never completed), else `Due in Nd`
// rounded UP as the web rounds it. An item with neither a date nor the overdue
// flag has an interval of 0 and no due date at all, and says so.
func pmDueBadge(it omsapi.MaintenanceItem, now time.Time) string {
	if it.IsOverdue {
		if it.DaysOverdue != nil && it.NextDueAt != nil {
			return fmt.Sprintf("%dd overdue", *it.DaysOverdue)
		}
		return "Overdue"
	}
	if it.NextDueAt == nil {
		return "no due date"
	}
	days := int(math.Ceil(it.NextDueAt.Sub(now).Hours() / 24))
	return fmt.Sprintf("Due in %dd", days)
}

// renderRow is one due item: its title and badge, then the asset, tag and
// materials the web draws under it. The BADGE is a fact and never gives; the
// title is the identifier that abbreviates around it, with the cut marked.
func (s *PMDueScreen) renderRow(i, cells int) string {
	row := s.rows[i]
	it := row.item
	caret := "  "
	if i == s.cursor {
		caret = "▸ "
	}
	badge := pmDueBadge(it, s.now())
	room := cells - StyleSidebarItemActive.GetHorizontalPadding() - len(caret) - 2 - len(badge)
	if room < 1 {
		room = 1
	}
	// Clipped LINE BY LINE and not flattened: a stored title can carry a newline,
	// and the window packs a row by the lines it really draws (proseCursorWindow).
	title := proseClipEachLine(it.Title, room)
	badgeStyle := StyleMuted
	switch row.section {
	case pmDueOverdue:
		badgeStyle = StyleStatusError
	case pmDueThisWeek:
		badgeStyle = StyleStatusWarn
	}
	line := caret + title + "  " + badgeStyle.Render(badge)
	if i == s.cursor {
		line = StyleSidebarItemActive.Render(line)
	}
	meta := []string{}
	if it.AssetName != "" {
		meta = append(meta, it.AssetName)
	}
	if it.AssetTag != "" {
		meta = append(meta, it.AssetTag)
	}
	if n := len(it.Materials); n > 0 {
		word := "materials"
		if n == 1 {
			word = "material"
		}
		meta = append(meta, fmt.Sprintf("%d %s needed", n, word))
	}
	if len(meta) == 0 {
		return line
	}
	const indent = "    "
	return line + "\n" + indent + StyleMuted.Render(pickerClip(jdeStatusOneLine(strings.Join(meta, " · ")), cells-len(indent)))
}
