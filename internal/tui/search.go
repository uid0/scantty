package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/uid0/scantty/internal/omsapi"
)

// SearchPalette is the universal search: a live query box over OMS's
// cross-model search, and the results it answers with as a cursor list.
//
// ITS BAR IS A RECORD (proseBar), and what that record replaced is worth
// keeping. View wrote every result and then a literal —
// `↑/↓ move · enter open · esc close`, or `Type to search · ` in front of it —
// with no window, no width bound and no budget, so two things reached an
// operator. The literal named `↑/↓` and `enter` over an empty box and over a
// query that matched nothing, where neither does anything; and a result list
// longer than the pane pushed it off the bottom, because clampToBox drops from
// the BOTTOM. Measured before the conversion at 80x24: twenty ordinary results
// assembled a 44-row frame against an 18-row pane, rows drawn 73 cells wide
// against 51, and no `esc close` anywhere on the pane — so the one surface that
// says how to leave the palette was the first thing a useful search took away.
//
// THE QUERY BOX LEADS AND THE BAR CLOSES, and everything between them gives.
// The box is what the operator is typing into, so it is never traded for
// anything; the bar is how they leave; the results, the failure and the
// separators are budgeted into what the two leave (see View). Where the pane
// is too short for even the box and the bar — one row, at the 80x7 floor — the
// frame keeps the BOX, which is what the palette drew there before this
// conversion. Which of the two a one-row pane should keep is an open design
// question, and this record deliberately does not answer it.
type SearchPalette struct {
	deps    Deps
	input   textinput.Model
	results []omsapi.SearchResult
	cursor  int
	// start is the first result the window draws, refitted to the cursor inside
	// View for the reason proseFlatListFrame gives.
	start int
	// loading is a search SCHEDULED OR OUT: set by the keystroke that changed the
	// query (the debounce is part of the search, not a pause before it) and
	// cleared by the answer to the query the box holds. It is named `loading`,
	// with loadErr beside it, so the load-state sweep derives this screen the way
	// it derives every other one (proseBarLoadReceivers).
	loading bool
	loadErr string
	// answered is the query the results (or loadErr) are the answer to. "No
	// matches." is a claim about a query that was SEARCHED, and without this the
	// palette drew it for a query the debounce had not yet sent — found-nothing
	// where the fact was not-asked.
	answered string
	lastQ    string
	// terminalWidth and terminalHeight are the terminal Root last reported; zero
	// until it has, which draws every result (proseFlatListFrame's convention).
	terminalWidth  int
	terminalHeight int
}

type searchResponseMsg struct {
	query   string
	results []omsapi.SearchResult
	err     error
}

type searchTickMsg struct {
	query string
}

func NewSearchPalette(deps Deps) *SearchPalette {
	ti := textinput.New()
	ti.Prompt = "▸ "
	ti.Placeholder = "search items / assets / POs / suppliers / locations…"
	ti.CharLimit = 200
	ti.Focus()
	return &SearchPalette{deps: deps, input: ti}
}

func (s *SearchPalette) Title() string { return "Search" }

func (s *SearchPalette) WantsRawInput() bool { return true }

func (s *SearchPalette) Init() tea.Cmd { return textinput.Blink }

// query is the box's value as it is searched: trimmed. Every comparison against
// a search's own query reads this rather than the raw value, because the search
// is SENT trimmed — comparing the raw value dropped every answer to a query with
// a trailing space, and the tick that would have sent it, so such a query was
// never searched at all.
func (s *SearchPalette) query() string { return strings.TrimSpace(s.input.Value()) }

func (s *SearchPalette) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		s.terminalWidth, s.terminalHeight = m.Width, m.Height
		return s, nil
	case searchResponseMsg:
		if m.query != s.query() {
			// An answer to a query the box no longer holds. The search for the one
			// it does hold is still scheduled or out, so `loading` stays as it is.
			return s, nil
		}
		s.loading = false
		s.answered = m.query
		s.start = 0
		if m.err != nil {
			s.loadErr = m.err.Error()
			s.results = nil
		} else {
			s.loadErr = ""
			s.results = m.results
			if s.cursor >= len(s.results) {
				s.cursor = 0
			}
		}
		return s, nil
	case searchTickMsg:
		if m.query != s.query() {
			return s, nil
		}
		return s.runQuery(m.query)
	case tea.KeyMsg:
		switch m.Type {
		case tea.KeyDown:
			if s.cursor < len(s.results)-1 {
				s.cursor++
			}
			return s, nil
		case tea.KeyUp:
			if s.cursor > 0 {
				s.cursor--
			}
			return s, nil
		case tea.KeyEnter:
			return s.openSelected()
		case tea.KeyEsc:
			return s, SwitchTo(WSScan, NewScanScreen(s.deps))
		}
	}
	var cmd tea.Cmd
	s.input, cmd = s.input.Update(msg)
	q := s.query()
	if q != s.lastQ {
		s.lastQ = q
		if q == "" {
			s.results, s.loading, s.loadErr, s.answered = nil, false, "", ""
			return s, cmd
		}
		s.loading = true
		return s, tea.Batch(cmd, tea.Tick(220*time.Millisecond, func(time.Time) tea.Msg {
			return searchTickMsg{query: q}
		}))
	}
	return s, cmd
}

func (s *SearchPalette) runQuery(q string) (Screen, tea.Cmd) {
	if s.deps.OMS == nil {
		// Nothing will answer, so nothing is out.
		s.loading = false
		return s, nil
	}
	s.loading = true
	deps := s.deps
	ctx := deps.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return s, func() tea.Msg {
		resp, err := deps.OMS.Search(ctx, q)
		if resp == nil {
			return searchResponseMsg{query: q, err: err}
		}
		return searchResponseMsg{query: q, results: resp.Results, err: err}
	}
}

func (s *SearchPalette) openSelected() (Screen, tea.Cmd) {
	if s.cursor >= len(s.results) {
		return s, nil
	}
	r := s.results[s.cursor]
	id := fmt.Sprint(r.ID)
	switch r.Type {
	case "item", "inventory":
		// The OMS backend search (search/views.py) tags InventoryItems as
		// "inventory"; "item" is kept as a defensive alias in case the
		// backend ever emits it. Both open the inventory item detail.
		return s, SwitchTo(WSInventory, NewInventoryDetailScreen(s.deps, id))
	case "asset":
		return s, SwitchTo(WSAssets, NewAssetDetailScreen(s.deps, id))
	case "purchase_order":
		return s, SwitchTo(WSPurchasing, NewPurchaseOrderDetailScreen(s.deps, id))
	case "work_order":
		return s, SwitchTo(WSMaintenance, NewWorkOrderDetailScreen(s.deps, id))
	case "location":
		return s, SwitchTo(WSInventory, NewLocationDetailScreen(s.deps, id))
	case "supplier":
		return s, SwitchTo(WSInventory, NewSupplierDetailScreen(s.deps, id))
	case "sig":
		return s, SwitchTo(WSSIGs, NewSIGDetailScreen(s.deps, id))
	}
	return s, Status(fmt.Sprintf("%s %v (no detail screen yet)", r.Type, r.ID), StatusWarn)
}

// proseBar is the bar the palette is drawing, in every state: an empty box, a
// search scheduled or out, a failed search, a query that matched nothing, and
// a result list.
//
// THE RESULTS DECIDE IT AND THE LOAD DOES NOT, which is PR 210's rule read on a
// screen whose load keeps its rows drawn. A search out over the previous
// query's results leaves those results ON the pane — hiding them on every
// keystroke would blank the list the operator is narrowing — so `↑↓` visibly
// moves the cursor through them and `enter` opens the row under it, and both are
// named for exactly as long as there are rows to act on. A failed search clears
// the rows, so its bar names the way out alone. Whether `enter` should open a
// result the box's query no longer describes is the stale-row question
// prose_bar.go leaves for a decision; this bar names it truthfully meanwhile.
//
// THE BOX OWNS THE LETTERS (AGENTS.md: a live query box routes letters to
// itself), so the cursor moves on the arrows alone — proseNavArrows — and a
// literal naming `j/k` would be the bar claiming two keys the query eats.
func (s *SearchPalette) proseBar() proseBar { return s.barFor(len(s.results)) }

// barFor is the bar over this many results. Its ceiling — the tallest shape it
// takes — is barFor(proseFlatCeilingRows), which is what View budgets against
// for the reason proseListWindow gives.
func (s *SearchPalette) barFor(results int) proseBar {
	out := proseNavArrows(listNavMoves(results))
	if results > 0 {
		out = append(out, proseBarItem{Keys: []string{"enter"}, Hint: "enter open"})
	}
	return append(out, proseBarItem{Keys: []string{"esc"}, Hint: "esc close"})
}

// View is the query box, what the palette has to say under it, and the bar.
//
// THE GIVE-ORDER, top to bottom of importance: the box and the bar never give;
// the blank under the box gives first (it is decoration); then the body — the
// failure, or the results window — gives from its end, with every cut marked.
// The box ROW carries the palette's standing status (`searching…`,
// `N match(es)`), so the fact that a search is out or how many results there
// are is never budgeted away with the body that would otherwise say it: it is
// the one mark left where the body has no row at all.
func (s *SearchPalette) View() string {
	cells := proseBarCells(s.terminalWidth)
	// The body's lines: -1 is "every line", on a screen not yet told its pane.
	lines, gap := -1, true
	if s.terminalHeight > 0 {
		free := screenBodyRows(s.terminalHeight) - 1 - s.barFor(proseFlatCeilingRows).rows(cells)
		switch {
		case free >= 2:
			lines = free - 1
		case free == 1:
			lines, gap = 1, false
		default:
			lines = 0
		}
	}
	var b strings.Builder
	b.WriteString(s.queryRow(cells) + "\n")
	if body := s.body(cells, lines); len(body) > 0 {
		if gap {
			b.WriteString("\n")
		}
		b.WriteString(strings.Join(body, "\n") + "\n")
	}
	return b.String() + "\n" + s.proseBar().render(cells)
}

// queryRow is the box, bounded to the pane with its caret kept on it, and the
// standing status beside it.
//
// THE STATUS IS RESERVED BEFORE THE BOX IS SIZED, for the reason
// listSearchInputWidth gives: a width makes bubbles pad a short value out to
// it, so a suffix the box was not sized around is pushed off the pane on every
// query rather than only on a long one.
func (s *SearchPalette) queryRow(cells int) string {
	suffix := ""
	switch q := s.query(); {
	case q == "":
	case s.loading:
		suffix = "  searching…"
	case s.answered == q && s.loadErr == "":
		suffix = fmt.Sprintf("  %d match(es)", len(s.results))
	}
	boxCells := cells - lipgloss.Width(suffix)
	row := woBoxView(s.input, boxCells, "")
	if pad := boxCells - lipgloss.Width(row); pad > 0 {
		row += strings.Repeat(" ", pad)
	}
	return row + StyleMuted.Render(suffix)
}

// body is what the palette draws between the box and the bar, in at most
// `lines` lines (every line where lines < 0): the failure, the fact that the
// query matched nothing, or the results window.
func (s *SearchPalette) body(cells, lines int) []string {
	if lines == 0 {
		return nil
	}
	if s.loadErr != "" {
		// An OMS body, so bounded: omsapi.parseError hands a gateway's whole
		// page over, and written out it pushed the bar off the pane. Flattened,
		// folded and cut with the cut MARKED by failDetailLines, as
		// proseRefusalFrame does.
		rows := lines
		if rows < 0 {
			rows = proseFailedUnsizedRows
		}
		const lead = "Error: "
		out := failDetailLines(lead+jdeStatusOneLine(s.loadErr), cells, rows)
		if len(out) > 0 && strings.HasPrefix(out[0], lead) {
			out[0] = StyleStatusError.Render(lead) + strings.TrimPrefix(out[0], lead)
		}
		return out
	}
	if len(s.results) == 0 {
		if q := s.query(); q != "" && !s.loading && s.answered == q {
			return []string{StyleMuted.Render("No matches.")}
		}
		return nil
	}
	return s.window(cells, lines)
}

// searchRow is one result as the window draws it: its lines, each already
// bounded to the pane, and the ONE line it collapses to where the window has a
// single line to give it.
type searchRow struct {
	lines   []string
	compact string
}

// row renders result i.
//
// EVERY LINE IS BOUNDED, with the cut marked. Title and Subtitle are OMS
// values of any length, and nothing between an API write and this pane refuses
// a newline in either, so each is clipped LINE BY LINE (proseClipEachLine's
// reason) rather than as one string. The room reserves the highlight's padding
// on EVERY row, not only the highlighted one, because a row that fits until it
// is selected is cut on exactly the press that selects it.
func (s *SearchPalette) row(i, cells int) searchRow {
	r := s.results[i]
	room := cells - StyleSidebarItemActive.GetHorizontalFrameSize()
	caret := "  "
	if i == s.cursor {
		caret = "▸ "
	}
	lead := caret + r.Type + " · "
	styleLead := func(line string) string {
		if strings.HasPrefix(line, lead) {
			return caret + StyleMuted.Render(r.Type) + " · " + strings.TrimPrefix(line, lead)
		}
		return line
	}
	title := strings.Split(r.Title, "\n")
	var body []string
	for j, t := range title {
		if j == 0 {
			body = append(body, styleLead(pickerClip(lead+t, room)))
			continue
		}
		body = append(body, pickerClip("    "+t, room))
	}
	if i == s.cursor {
		body = strings.Split(StyleSidebarItemActive.Render(strings.Join(body, "\n")), "\n")
	}
	if r.Subtitle != "" {
		for _, sub := range strings.Split(r.Subtitle, "\n") {
			body = append(body, "    "+StyleMuted.Render(pickerClip(sub, room-4)))
		}
	}
	out := searchRow{lines: body, compact: body[0]}
	if extra := len(body) - 1; extra > 0 {
		// The mark is reserved out of the room before the first line is clipped,
		// so the collapsed row is as wide as any other.
		mark := fmt.Sprintf(" +%d lines", extra)
		if extra == 1 {
			mark = " +1 line"
		}
		first := styleLead(pickerClip(lead+title[0], room-len(mark)))
		if i == s.cursor {
			first = StyleSidebarItemActive.Render(first)
		}
		out.compact = first + StyleMuted.Render(mark)
	}
	return out
}

// window is the results in at most `lines` lines around the cursor.
//
// PACKED BY LINES, NOT ROWS (proseLineWindow), because a result with a
// subtitle is two lines and one whose OMS title carries newlines is more.
// BOTH scroll markers are reserved wherever the window does not hold every
// line, for the reason proseListFixedRows gives; on a body of two lines one
// marker is all there is room for and it points where the rows are, and on a
// body of one the box row's `N match(es)` is the mark. A row taller than the
// window keeps what fits and names how many lines it left out — on a window of
// ONE line that is its first line and the count, so the row still says what it
// is and the arrows still visibly move from one result to the next.
func (s *SearchPalette) window(cells, lines int) []string {
	rows := make([]searchRow, len(s.results))
	heights := make([]int, len(rows))
	total := 0
	for i := range rows {
		rows[i] = s.row(i, cells)
		heights[i] = len(rows[i].lines)
		total += heights[i]
	}
	var out []string
	if lines < 0 || total <= lines {
		s.start = 0
		for _, r := range rows {
			out = append(out, r.lines...)
		}
		return out
	}
	budget := lines - 2
	if budget < 1 {
		budget = 1
	}
	from, to := proseLineWindow(heights, s.cursor, s.start, budget)
	s.start = from
	above := StyleMuted.Render("  ↑ more above")
	below := StyleMuted.Render(fmt.Sprintf("  ↓ %d more below", len(rows)-to))
	if lines >= 3 && from > 0 {
		out = append(out, above)
	}
	for _, r := range rows[from:to] {
		switch {
		case len(r.lines) <= budget:
			out = append(out, r.lines...)
		case budget == 1:
			out = append(out, r.compact)
		default:
			kept := budget - 1
			out = append(out, r.lines[:kept]...)
			out = append(out, StyleMuted.Render(fmt.Sprintf("… %d more lines", len(r.lines)-kept)))
		}
	}
	switch {
	case lines >= 3:
		if to < len(rows) {
			out = append(out, below)
		}
	case lines == 2 && to < len(rows):
		out = append(out, below)
	case lines == 2 && from > 0:
		out = append(out, above)
	}
	return out
}
