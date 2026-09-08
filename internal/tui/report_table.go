package tui

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/uid0/scantty/internal/omsapi"
)

// ---------------------------------------------------------------------------
// Shared report-table rendering + number formatting
//
// The OMS web Reports section (Inventory / Purchasing / Asset pages) is a set
// of tabbed, sortable TABLES. A terminal can't render the analytics charts, so
// scantty mirrors the underlying report DATA as aligned text tables. These
// helpers are shared by the generic tabbed ReportTableScreen (below) and the
// AnalyticsPulseScreen (analytics_pulse.go).
// ---------------------------------------------------------------------------

// colAlign controls per-column text alignment.
type colAlign int

const (
	alignLeft colAlign = iota
	alignRight
)

// reportColumn is one table column: a header and its alignment (numbers right,
// text left).
type reportColumn struct {
	header string
	align  colAlign
}

// reportColWidths returns the NATURAL display width of each column: the max
// ANSI-aware width across the header and every row cell, so columns stay
// aligned as the table scrolls. It is what the table would like; fitReportTable
// is what the pane will give.
func reportColWidths(cols []reportColumn, rows [][]string) []int {
	w := make([]int, len(cols))
	for i, c := range cols {
		w[i] = lipgloss.Width(c.header)
	}
	for _, row := range rows {
		for i := 0; i < len(cols) && i < len(row); i++ {
			if cw := lipgloss.Width(row[i]); cw > w[i] {
				w[i] = cw
			}
		}
	}
	return w
}

// --- fitting a table to the pane it is drawn in -----------------------------
//
// Root.View CLIPS the pane from the right with no mark (clampToBox), so a table
// laid out at its natural widths and handed to a pane that cannot hold it does
// not "overflow" — it silently becomes a different table. Measured at 80
// columns, which is the width this project checks against and where the pane is
// 51 cells: the purchasing Lead time tab drew "1.5      75" for an on-time rate
// of 75%, the % taken off the end so a RATE read as a count; the reorders
// Supplier perf tab drew a 33.3% late rate as "33." and a $12,345.67 order
// value as "$12,3"; and with a realistic supplier and item name every numeric
// column on the lead-time tab was off the pane altogether, under a header line
// ending "Or". A cut number is worse than an absent one — it reads as a number
// somebody meant — which is why the give-order below never touches one.
//
// THE ORDER GROUND IS GIVEN IN, and every part of a row is one of exactly two
// things:
//
//  1. An IDENTIFIER (a left-aligned column: supplier, item, asset, month). It
//     names a row and an operator can still tell rows apart from a prefix, so
//     these abbreviate — widest first, down to reportNameFloor — and the
//     ellipsis says so.
//  2. A FACT (a right-aligned column: a count, a rate, a variance, money).
//     These NEVER give, header included: a fact column is never narrowed below
//     the widest thing in it, so no figure in this package is ever clipped.
//
// Where even that will not fit, whole columns are dropped from the RIGHT rather
// than any figure being cut. Dropping from the right keeps the columns that
// stay in the places the operator learned them in, the header carries
// reportDropMark so the loss is visible on the same row it happened to, and the
// note below the table NAMES what went. The mark leads and the names follow,
// because a short pane takes the note before it takes the header.

const (
	// reportColGutter is the gap joinReportRow puts between two columns.
	reportColGutter = 2

	// reportNameFloor is the fewest cells an identifier column keeps before the
	// table gives up a whole column instead. Five leaves "Acme…", which still
	// tells two suppliers apart; below that a name stops naming anything and
	// the pane is better spent on a column that still says something, which is
	// why there is a floor at all rather than a slide down to one character.
	//
	// It is five and not six because of the purchasing Lead time tab at 80
	// columns, which is the most crowded table here and the one the captain
	// chases vendors from: at six its two identifier columns cost one cell more
	// than the pane has and On-time — the headline vendor figure — was the
	// column that dropped. A five-cell name beats a missing rate.
	reportNameFloor = 5

	// reportRowLead is the two cells every table row starts with: "  " on a
	// plain row and "▸ " on the one the cursor is on. The column header is
	// indented by the same two, so the header lines up with the cells under it,
	// and the room the fit is given is the pane LESS this.
	reportRowLead = 2

	// reportDropMark rides the header row when the pane could not hold every
	// column. It is one cell, and the gutter before it is reserved with it.
	reportDropMark = "…"
)

// reportTableFit is one table laid out inside the pane it will be drawn in:
// the width of each column that IS drawn, and the headers of the columns that
// are not.
type reportTableFit struct {
	widths  []int
	dropped []string
}

// fitReportTable lays cols/rows out inside `room` display cells.
//
// room <= 0 means "no pane to fit to": the table is drawn at its natural widths
// and whatever overruns is clampToBox's to take. Only AnalyticsPulseScreen
// passes that — it renders its tables once into a TextScroller at load time and
// records no terminal width, so it has no live pane to bound against. It
// carries no lateness or variance figure, so nothing here is load-bearing for
// it; the unbounded path is its pre-existing behaviour, named rather than left
// implicit.
func fitReportTable(cols []reportColumn, rows [][]string, room int) reportTableFit {
	natural := reportColWidths(cols, rows)
	if room <= 0 || len(cols) == 0 {
		return reportTableFit{widths: natural}
	}
	for shown := len(cols); shown >= 1; shown-- {
		budget := room - reportColGutter*(shown-1)
		if shown < len(cols) {
			budget -= reportColGutter + lipgloss.Width(reportDropMark)
		}
		w := make([]int, shown)
		copy(w, natural[:shown])
		if reportShrinkNames(cols[:shown], w, budget) {
			return reportTableFit{widths: w, dropped: reportHeaderNames(cols[shown:])}
		}
	}
	// The pane cannot hold even the first column at its floor. Draw that one
	// column clipped to whatever there is, which is the only state in which
	// this package clips a cell that may be a fact — and it is reachable only
	// on a table whose FIRST column is right-aligned, of which there are none
	// (TestReportTable_EveryFigureIsWholeOrNamedAsMissing sweeps every tab at
	// every width Root draws at and would report one).
	if len(cols) > 1 {
		room -= reportColGutter + lipgloss.Width(reportDropMark)
	}
	if room < 1 {
		room = 1
	}
	return reportTableFit{widths: []int{room}, dropped: reportHeaderNames(cols[1:])}
}

// reportShrinkNames narrows the IDENTIFIER columns, widest first, until the
// widths fit the budget. It reports whether they did; a fact column is never
// touched, so a table whose facts alone outrun the budget fails here and the
// caller gives up a column instead.
func reportShrinkNames(cols []reportColumn, w []int, budget int) bool {
	total := 0
	for _, x := range w {
		total += x
	}
	for total > budget {
		pick, widest := -1, reportNameFloor
		for i, c := range cols {
			if c.align == alignLeft && w[i] > widest {
				pick, widest = i, w[i]
			}
		}
		if pick < 0 {
			return false
		}
		w[pick]--
		total--
	}
	return true
}

// reportHeaderNames is the header text of each column, in order — what the note
// names when a column is dropped, and what the yardstick mark is looked for on.
func reportHeaderNames(cols []reportColumn) []string {
	if len(cols) == 0 {
		return nil
	}
	out := make([]string, len(cols))
	for i, c := range cols {
		out[i] = c.header
	}
	return out
}

// padCell aligns a cell to width w within its column.
func padCell(s string, w int, align colAlign) string {
	n := w - lipgloss.Width(s)
	if n < 0 {
		n = 0
	}
	if align == alignRight {
		return strings.Repeat(" ", n) + s
	}
	return s + strings.Repeat(" ", n)
}

// joinReportRow renders one row of cells padded to the column widths, joined by
// the gutter. A cell wider than its column is CLIPPED first, because padCell
// pads and never truncates — so without this the row would run past the pane
// and clampToBox would take the tail of the LAST column rather than the one
// that overflowed. Only an identifier column is ever narrower than its content
// (fitReportTable never narrows a fact), so the clip is a no-op on every figure.
func joinReportRow(cells []string, widths []int, cols []reportColumn) string {
	parts := make([]string, len(widths))
	for i := range widths {
		cell := ""
		if i < len(cells) {
			cell = cells[i]
		}
		align := alignLeft
		if i < len(cols) {
			align = cols[i].align
		}
		parts[i] = padCell(pickerClip(cell, widths[i]), widths[i], align)
	}
	return strings.Join(parts, strings.Repeat(" ", reportColGutter))
}

// reportTableLines builds the styled header line and the plain (unstyled) body
// lines for a table fitted to `room` display cells, plus the headers of any
// columns the pane could not hold. Callers overlay cursor highlighting /
// windowing as needed. See fitReportTable for what room <= 0 means.
func reportTableLines(cols []reportColumn, rows [][]string, room int) (header string, body []string, dropped []string) {
	fit := fitReportTable(cols, rows, room)
	header, body = reportTableRows(cols, rows, fit)
	return header, body, fit.dropped
}

// reportTableRows is reportTableLines for a caller that already has the fit —
// the screen, which computes it once and hands the same one to every reader.
func reportTableRows(cols []reportColumn, rows [][]string, fit reportTableFit) (header string, body []string) {
	shown := cols
	if len(fit.widths) < len(cols) {
		shown = cols[:len(fit.widths)]
	}
	head := joinReportRow(reportHeaderNames(shown), fit.widths, shown)
	if len(fit.dropped) > 0 {
		head += strings.Repeat(" ", reportColGutter) + reportDropMark
	}
	header = StyleMuted.Render(head)
	body = make([]string, len(rows))
	for i, r := range rows {
		body[i] = joinReportRow(r, fit.widths, shown)
	}
	return header, body
}

// --- number / value formatting -------------------------------------------

// commaGroup inserts thousands separators into a plain decimal string like
// "1234.50" → "1,234.50" (tolerates a leading '-' and an optional fraction).
func commaGroup(s string) string {
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	intPart, frac := s, ""
	if dot := strings.IndexByte(s, '.'); dot >= 0 {
		intPart, frac = s[:dot], s[dot:]
	}
	n := len(intPart)
	if n > 3 {
		var b strings.Builder
		pre := n % 3
		if pre > 0 {
			b.WriteString(intPart[:pre])
			b.WriteByte(',')
		}
		for i := pre; i < n; i += 3 {
			b.WriteString(intPart[i : i+3])
			if i+3 < n {
				b.WriteByte(',')
			}
		}
		intPart = b.String()
	}
	out := intPart + frac
	if neg {
		out = "-" + out
	}
	return out
}

// fmtMoney formats a float money value (inventory / purchasing reports serialize
// money as JSON floats) as "$1,234.50".
func fmtMoney(f float64) string {
	return "$" + commaGroup(fmt.Sprintf("%.2f", f))
}

// fmtMoneyStr formats a Decimal-as-string money value (asset TCO + analytics
// category_spend serialize money as strings) as "$175.00"; blank → "—".
func fmtMoneyStr(s string) string {
	if strings.TrimSpace(s) == "" {
		return "—"
	}
	return "$" + commaGroup(s)
}

// orDash returns "—" for an empty/whitespace string, else the string.
func orDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "—"
	}
	return s
}

// intOrDash renders a *int, using "—" for nil.
func intOrDash(p *int) string {
	if p == nil {
		return "—"
	}
	return fmt.Sprintf("%d", *p)
}

// dateOnly trims an RFC3339 datetime string to its date ("2026-04-01T…" →
// "2026-04-01"); passes through bare dates and "" unchanged.
func dateOnly(s string) string {
	if len(s) >= 10 && s[4] == '-' && s[7] == '-' {
		return s[:10]
	}
	return s
}

// ---------------------------------------------------------------------------
// ReportTableScreen — a generic, tabbed, scrollable set of report tables.
//
// Each tab lazy-loads its rows on first view (loaders return pre-formatted
// string cells so this screen is type-agnostic). ←/→ or [/] switch
// tabs; j/k/pgup/pgdn/g/G scroll rows; r refreshes the active tab; esc returns
// to the Reports hub. Read-only — mirrors the web report DATA, not its chart
// widgets (there are none on these pages).
// ---------------------------------------------------------------------------

// reportBody is what a loader hands back.
//
// It is a struct rather than a bare [][]string because some payloads answer a
// question the TAB cannot: which promise its lateness columns are scored
// against. That answer has to travel back through the tea.Msg with the rows —
// a loader runs in a command goroutine and View runs on the main loop, so a
// value stashed in a closure and read at render time is a data race, not a
// shortcut. One shape for every loader, so a tab that grows a data-carried fact
// later has somewhere to put it instead of inventing a second loader kind.
type reportBody struct {
	rows [][]string
	// yardstick is what the payload said the figures scored against a promise
	// are measured against — OMS's variance_measured_against, agreed across
	// every row.
	//
	// "" means the payload did not say, which is NOT a shorthand for the
	// current answer: an OMS too old to serve the key has said nothing, and a
	// screen that fills that silence in asserts a promise nobody made. The
	// legend says so rather than naming one — "could not tell" and "found
	// nothing" are different facts.
	yardstick string
}

// reportTab is one tab: a label, its columns, and a row loader.
type reportTab struct {
	label   string
	columns []reportColumn
	loader  func(ctx context.Context, deps Deps) (reportBody, error)
	// note is an optional caption under the table (e.g. the default window).
	note string
}

type reportTabState struct {
	loaded      bool
	loading     bool
	err         string
	rows        [][]string
	yardstick   string
	cursor      int
	windowStart int

	// fit is the column layout for `fitCells` cells, kept because View, the
	// row budget and the legend all need the same answer and a report can carry
	// a row per supplier/item PAIR — recomputing it per caller walks every cell
	// of the table several times for one keystroke. Invalidated by a load
	// (fitOK) and by a resize (fitCells).
	fit      reportTableFit
	fitCells int
	fitOK    bool
}

type ReportTableScreen struct {
	deps           Deps
	title          string
	tabs           []reportTab
	states         []reportTabState
	active         int
	terminalWidth  int
	terminalHeight int
}

type reportTabLoadedMsg struct {
	tab       int
	rows      [][]string
	yardstick string
	err       error
}

func NewReportTableScreen(deps Deps, title string, tabs []reportTab) *ReportTableScreen {
	return &ReportTableScreen{
		deps:   deps,
		title:  title,
		tabs:   tabs,
		states: make([]reportTabState, len(tabs)),
	}
}

func (s *ReportTableScreen) Title() string { return s.title }

// HandlesKey claims the two keys the global nav layer would otherwise eat: G
// (global = inventory categories) so end-of-table works, and esc so the screen
// returns to the Reports hub rather than jumping to the home screen.
func (s *ReportTableScreen) HandlesKey(key string) bool {
	return key == "G" || key == "esc"
}

func (s *ReportTableScreen) Init() tea.Cmd {
	if len(s.tabs) == 0 {
		return nil
	}
	s.states[s.active].loading = true
	return s.loadTab(s.active)
}

func (s *ReportTableScreen) loadTab(i int) tea.Cmd {
	deps := s.deps
	ctx := deps.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	loader := s.tabs[i].loader
	return func() tea.Msg {
		body, err := loader(ctx, deps)
		return reportTabLoadedMsg{tab: i, rows: body.rows, yardstick: body.yardstick, err: err}
	}
}

func (s *ReportTableScreen) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		s.terminalWidth = m.Width
		s.terminalHeight = m.Height
		s.scrollIntoView()
		return s, nil
	case reportTabLoadedMsg:
		if m.tab >= 0 && m.tab < len(s.states) {
			st := &s.states[m.tab]
			st.loaded = true
			st.loading = false
			st.rows = m.rows
			st.yardstick = m.yardstick
			st.fitOK = false
			if m.err != nil {
				st.err = m.err.Error()
			} else {
				st.err = ""
			}
			if st.cursor >= len(st.rows) {
				st.cursor = len(st.rows) - 1
			}
			if st.cursor < 0 {
				st.cursor = 0
			}
		}
		s.scrollIntoView()
		return s, nil
	case tea.KeyMsg:
		return s.updateKey(m)
	}
	return s, nil
}

func (s *ReportTableScreen) updateKey(m tea.KeyMsg) (Screen, tea.Cmd) {
	if len(s.tabs) == 0 {
		if m.String() == "esc" {
			return s, SwitchTo(WSReports, NewReportsScreen(s.deps))
		}
		return s, nil
	}
	st := &s.states[s.active]
	switch m.String() {
	case "esc", "backspace":
		return s, SwitchTo(WSReports, NewReportsScreen(s.deps))
	case "right", "]":
		// tab / shift+tab used to be synonyms here. They are the root's keys
		// for the sidebar menu now, and a key that means two things depending
		// on the screen is what phase 3 is retiring; ←/→ and [/] already do
		// this, so nothing was lost with them.
		return s.switchTab((s.active + 1) % len(s.tabs))
	case "left", "[":
		return s.switchTab((s.active - 1 + len(s.tabs)) % len(s.tabs))
	case "j", "down":
		if st.cursor < len(st.rows)-1 {
			st.cursor++
			s.scrollIntoView()
		}
	case "k", "up":
		if st.cursor > 0 {
			st.cursor--
			s.scrollIntoView()
		}
	case "pgdown":
		st.cursor += s.windowSize()
		if st.cursor >= len(st.rows) {
			st.cursor = len(st.rows) - 1
		}
		if st.cursor < 0 {
			st.cursor = 0
		}
		s.scrollIntoView()
	case "pgup":
		st.cursor -= s.windowSize()
		if st.cursor < 0 {
			st.cursor = 0
		}
		s.scrollIntoView()
	case "g", "home":
		st.cursor = 0
		s.scrollIntoView()
	case "G", "end":
		st.cursor = len(st.rows) - 1
		if st.cursor < 0 {
			st.cursor = 0
		}
		s.scrollIntoView()
	case "r":
		st.loaded = false
		st.loading = true
		st.err = ""
		return s, s.loadTab(s.active)
	}
	return s, nil
}

// switchTab moves to tab i, triggering a lazy load if its rows aren't in hand.
func (s *ReportTableScreen) switchTab(i int) (Screen, tea.Cmd) {
	s.active = i
	s.scrollIntoView()
	st := &s.states[i]
	if !st.loaded && !st.loading {
		st.loading = true
		return s, s.loadTab(i)
	}
	return s, nil
}

// reportPaneCells is how many COLUMNS this screen's View really gets. The
// report-prefixed names are deliberate: paneRows and paneCells are the COLUMNAR
// layer's own (jde_form.go, marked jde:layer-only), and this screen is not on
// that layer — ListScreen names its pair listPaneRows / listPaneCells for the
// same reason. Unsized, it
// answers the width that must HOLD (screenBodyWidth(80) = 51) rather than a
// guess at the terminal: a table laid out for a pane wider than the real one is
// the clipped-figure defect fitReportTable exists to remove, and laying out for
// the narrowest supported terminal can only be conservative.
//
// screenBodyCells, not screenBodyWidth: the latter floors at 20, which is four
// cells more than Root gives at the narrowest width it draws, and a bound
// spending cells the pane does not have is not a bound.
func (s *ReportTableScreen) reportPaneCells() int {
	if s.terminalWidth <= 0 {
		return pickerPaneWidth
	}
	// Floored at one cell, not because Root ever draws such a pane — it refuses
	// below a content width of 20 — but because every bound below divides or
	// clips by this, and a zero would make a folder return no lines at all on a
	// frame that then indexes the first one.
	if n := screenBodyCells(s.terminalWidth); n > 0 {
		return n
	}
	return 1
}

// reportTableCells is the room the table itself gets: the pane less the two cells
// every row is led with.
//
// The cursor row is drawn with the highlight's horizontal padding REMOVED
// (reportRowHighlight), which is why nothing more is reserved here. Left on, it
// cost two cells that only the selected row spent — so a table that fitted
// until a row was selected lost its last column on exactly the keypress that
// selected it — and shifted that row one cell right of every other, putting the
// cursor row's columns out of line with the header they are read under.
func (s *ReportTableScreen) reportTableCells() int {
	if n := s.reportPaneCells() - reportRowLead; n > 0 {
		return n
	}
	return 1
}

// reportRowHighlight is the cursor row's style: the workspace highlight without
// the sidebar padding it inherits, which a table row must not carry. Built per
// call rather than at package init because the theme rebuilds
// StyleSidebarItemActive.
func reportRowHighlight() lipgloss.Style {
	return StyleSidebarItemActive.Padding(0, 0)
}

// reportPaneRows is how many rows this screen's View really gets. screenBodyRows, not
// screenBodyHeight: the latter floors at four and is therefore a lie below a
// terminal height of 10, and a budget that claims rows the pane does not have
// is how the footer and the note ended up under clampToBox in the first place.
func (s *ReportTableScreen) reportPaneRows() int {
	if s.terminalHeight <= 0 {
		return listWindowSize + reportFixedChromeRows
	}
	return screenBodyRows(s.terminalHeight)
}

// reportErrRows is the CEILING on how much of a failed load's body reaches the
// pane, and the pane itself can lower it (frameRows). A report error is an OMS
// response and can be a whole HTML error page; the ceiling is what lets the
// detail be bounded before it is folded rather than after. The LAST of the rows
// it keeps says so whenever either cut bit — same bound, same mark, same reason
// as po_create.go's poFailDetailRows, which is where the wordings and the
// spend-a-row-rather-than-add-one trade come from.
//
// It is a ceiling and NOT a budget, which is the distinction that was got wrong:
// read as a budget it claimed six rows of a pane that might have three, and
// clampToBox then took the footer — the only place esc is named — off a frame
// an operator had reached by a load FAILING.
const reportErrRows = 6

// reportErrMinRows is the give-order's floor on that block: the first line of
// the error and the row saying the rest of it was cut. The mark never gives —
// an error body trimmed with nothing saying so leaves the operator unable to
// tell they are missing the sentence that says what failed — so where the pane
// cannot hold two rows the frame runs over rather than dropping it.
const reportErrMinRows = 2

const (
	// reportFrameChromeRows is what EVERY frame spends: the tab bar, the blank
	// under it, and the blank above the footer.
	reportFrameChromeRows = 3

	// reportFixedChromeRows is that plus the column header, which only the
	// TABLE frame draws. Everything else — the legend, the notes, the footer's
	// own fold, the marker row — is DERIVED from what is really assembled,
	// because a constant is exactly how the previous budget came to claim rows
	// the pane did not have.
	reportFixedChromeRows = reportFrameChromeRows + 1
)

// frameRows is how many rows the frame's OWN block gets, whichever branch View
// is about to draw: the pane less the chrome every frame spends and less the
// FOLDED footer.
//
// ONE budget for five branches. Only the table branch used to consult one, so
// the loading, failed and empty frames were drawn against nothing at all and
// the failed one against a flat six-row constant — at 80 columns it assembled
// about ten rows and needed a terminal of sixteen, where the frame it replaced
// needed eleven. An operator on an 11-to-15-row terminal whose load had just
// FAILED got six lines of gateway HTML and no named way off the screen.
//
// THE FOOTER NEVER GIVES, on any branch, because it is the only place `esc` is
// named and a frame nobody can leave is worse than a frame that says less. What
// gives is the frame's own block, from the END.
// TestReportTable_TheScreenAssemblesNoMoreRowsThanThePaneHas walks every branch
// at every pane Root draws and is the check that claim is made on.
func (s *ReportTableScreen) frameRows() int {
	if len(s.tabs) == 0 {
		return s.reportPaneRows() - reportFrameChromeRows - 1
	}
	st := &s.states[s.active]
	return s.reportPaneRows() - reportFrameChromeRows -
		len(pickerWrap(s.footerHint(len(st.rows)), s.reportPaneCells()))
}

// frameFits reports whether the pane can hold the FLOOR of the frame View is
// about to draw — what the give-order promises whatever else it gives up. Below
// it the frame runs over and clampToBox takes the tail; that band is this
// predicate's own answer rather than a height written down anywhere, because it
// moves with every wording on the frame and it grows TALLER as the terminal
// gets NARROWER, the legend, the notes and the footer folding onto more rows.
//
// One expression, asked of the same branches View draws, so the residual band
// cannot be one thing in the code and another in a comment.
func (s *ReportTableScreen) frameFits() bool {
	if len(s.tabs) == 0 {
		// A fixed line with nothing under it to give. There is no report screen
		// in the program with no tabs, and no budget could buy anything here.
		return true
	}
	st := &s.states[s.active]
	switch {
	case st.loading || !st.loaded:
		return s.frameRows() >= 1
	case st.err != "":
		return s.frameRows() >= reportErrMinRows
	case len(st.rows) == 0:
		return s.frameRows() >= 1
	}
	avail, floor := s.rowBudget()
	return avail >= floor
}

// reportBlockRows clamps a frame's own block to the rows it has, giving from
// the END and never returning fewer than one line — the give-order every
// non-table branch follows.
func reportBlockRows(lines []string, rows int) []string {
	if rows < 1 {
		rows = 1
	}
	if len(lines) > rows {
		return lines[:rows]
	}
	return lines
}

// layoutRows shares the pane out between the table body and the block under it,
// and it is the ONE answer both View and the key arms read — a budget the
// renderer and the pager disagree about is a cursor that walks off the pane.
//
// GROUND IS GIVEN IN A STATED ORDER, because clampToBox drops from the BOTTOM
// and whatever is drawn last is what a short pane silently eats. The LEGEND and
// the FOOTER never give: the first says what a figure means and the second is
// the only place a key is named, and a screen that loses either is asserting
// something it cannot support. The BODY floors at one row. What gives is the
// block UNDER the table, from the END — its standing note first (context an
// operator can do without) and the dropped-columns note after it, which is the
// more load-bearing of the two and is also the one whose fact the header's own
// mark still carries once the words are gone.
//
// This is the TABLE frame's half of the order; the footer half of it holds on
// every branch View draws and `frameRows` is where that is spent, so the
// loading, failed and empty frames give up their own block rather than the way
// off the screen.
//
// Where even the FLOOR will not fit — legend, header, one body row with the
// marker row it needs beside it, and the folded footer — the frame runs over
// and clampToBox takes the tail. That band is `frameFits`'s own answer, so
// derive it from there rather than from a height written down here: it moves
// with every wording on the frame and it is WIDTH-dependent in the wrong
// direction — a narrow terminal folds the legend, the notes and the footer onto
// more rows, so the budget shrinks and the band gets TALLER as the pane gets
// narrower. `TestReportTable_TheScreenAssemblesNoMoreRowsThanThePaneHas` walks
// both sides of that boundary on every branch at every pane Root draws and is
// the check this claim is made on. The band is left as it is rather than
// half-built into a refusal: the legend LEADS, so a figure is never drawn
// without it at any height, which is the property this screen has to keep.
func (s *ReportTableScreen) layoutRows() (body, below int) {
	if len(s.tabs) == 0 {
		return listWindowSize, 0
	}
	st := &s.states[s.active]
	avail, floor := s.rowBudget()
	below = len(s.belowLines(s.tabs[s.active], st))
	if short := floor - (avail - below); short > 0 {
		below -= short
		if below < 0 {
			below = 0
		}
	}
	body = avail - below
	if len(st.rows) > body {
		body--
	}
	if body < 1 {
		body = 1
	}
	return body, below
}

// rowBudget is what the body, its marker row and the block under the table have
// to share (`avail`), and the fewest of those rows the give-order promises
// (`floor`). ONE expression, because the marker has to be reserved in the SAME
// place the block below is clamped.
//
// It used to be taken out of the BODY afterwards — `body--` once the rows
// outran it — and the floor at one row then handed that row straight back
// without anything giving it up, so the frame assembled `avail + 1` rows
// whenever the block below squeezed the body to one. The renderer draws the
// marker on its own condition (View's listMarkerLine fires exactly when the
// rows outrun the drawn window), so the row was real and clampToBox took the
// tail: the FOOTER, which this doc and AGENTS.md both say never gives — the
// operator left at 80x20 on the reorders Supplier perf tab with `r refresh ·
// esc back` gone and no named way off the screen.
//
// The marker is ONE row carrying both facts (listMarkerLine) rather than a row
// apiece, so the budget cannot change as the cursor moves and the table does
// not jump under the operator's hands. Whether it is drawn depends on the body,
// and the body depends on whether it was reserved, so the circle is settled at
// the RESERVING case the way the columnar layer settles its action-bar height
// against the tallest bar: more than one row means the marker is possible, so
// it is paid for. Where the rows turn out to fit after all the reservation
// costs nothing — `body + below` still comes to `avail`, the body simply has a
// row it does not fill.
func (s *ReportTableScreen) rowBudget() (avail, floor int) {
	tab, st := s.tabs[s.active], &s.states[s.active]
	avail = s.frameRows() - (reportFixedChromeRows - reportFrameChromeRows) -
		len(s.legendLines(tab, st))
	floor = 1
	if len(st.rows) > 1 {
		floor++
	}
	return avail, floor
}

// windowSize is how many table rows the pane can hold. See layoutRows.
func (s *ReportTableScreen) windowSize() int {
	body, _ := s.layoutRows()
	return body
}

func (s *ReportTableScreen) scrollIntoView() {
	if len(s.states) == 0 {
		return
	}
	st := &s.states[s.active]
	win := s.windowSize()
	if st.cursor < st.windowStart {
		st.windowStart = st.cursor
	}
	if st.cursor >= st.windowStart+win {
		st.windowStart = st.cursor - win + 1
	}
	if st.windowStart < 0 {
		st.windowStart = 0
	}
	if maxStart := len(st.rows) - win; maxStart > 0 && st.windowStart > maxStart {
		st.windowStart = maxStart
	}
	if len(st.rows) <= win {
		st.windowStart = 0
	}
}

// --- what the table says about itself ---------------------------------------

// reportYardstickMark rides the header of a column whose figure is scored
// against a promise rather than standing on its own — a lateness or a variance.
// One cell, which is the whole reason it is a mark and not the yardstick spelled
// out per column: at 80 columns these tables have 51 cells and the words would
// cost more than the figures they explain.
//
// A HEADER CARRYING IT IS THE ONLY DECLARATION. The legend, the columns it
// names and the mark on the pane are one string, so there is no second roster
// to keep in step and no way to mark a column the legend does not cover or
// cover one the pane did not draw. Nothing else in these reports ends a header
// in "*".
const reportYardstickMark = "*"

// reportYardstickTokenCells bounds a yardstick this build has no prose for. It
// is long enough for any name OMS is plausibly going to coin and short enough
// that an unbounded value cannot become the whole legend.
const reportYardstickTokenCells = 48

// reportYardstickQuotedWords is the ONE place this screen spells OMS's
// quoted_lead_time in the words a person reads. Taken from the wire constant
// rather than restated per tab, so the three tabs cannot come to name three
// different promises.
const reportYardstickQuotedWords = "the supplier's standing quoted lead time"

// legendLines is the marked columns' legend, folded to the pane.
//
// It is drawn ABOVE the table, and that is a decision rather than a layout
// habit: clampToBox drops from the BOTTOM, so anything under the rows is what a
// short pane takes — and a figure whose yardstick has been trimmed off the pane
// is exactly the bare lateness this whole surface exists to stop asserting.
// Whatever must survive must lead. Rows are what give instead, which is the
// safe direction: fewer figures, each still explained.
func (s *ReportTableScreen) legendLines(tab reportTab, st *reportTabState) []string {
	marked := s.markedColumnsDrawn(tab, st)
	if len(marked) == 0 {
		return nil
	}
	return pickerWrap(reportYardstickLegend(marked, st.yardstick), s.reportPaneCells())
}

// markedColumnsDrawn is the headers the pane actually DREW that end in
// reportYardstickMark. There is no roster anywhere of which columns are scored
// against a promise — the mark on the header is the only declaration — so this
// reads the drawn headers rather than consulting one.
//
// Asked of the DRAWN columns and not of the declared ones, because a legend for
// a column the fit dropped is a claim about a mark that is not on the pane —
// the same false claim in the other direction. Empty while the table is not
// drawn at all, so a loading or empty tab makes no yardstick claim.
func (s *ReportTableScreen) markedColumnsDrawn(tab reportTab, st *reportTabState) []string {
	if len(st.rows) == 0 || !st.loaded || st.err != "" {
		return nil
	}
	var out []string
	for _, name := range reportHeaderNames(s.shownColumns(tab, st)) {
		if strings.HasSuffix(name, reportYardstickMark) {
			out = append(out, name)
		}
	}
	return out
}

// tabFit is the ONE column layout every reader of this tab uses — View, the row
// budget, the legend and the dropped-columns note. One answer, so the header
// the operator reads and the note saying what is missing from it cannot
// disagree.
func (s *ReportTableScreen) tabFit(tab reportTab, st *reportTabState) reportTableFit {
	cells := s.reportTableCells()
	if !st.fitOK || st.fitCells != cells {
		st.fit = fitReportTable(tab.columns, st.rows, cells)
		st.fitCells = cells
		st.fitOK = true
	}
	return st.fit
}

// shownColumns is the columns the pane holds for this tab, by the same fit View
// draws with.
func (s *ReportTableScreen) shownColumns(tab reportTab, st *reportTabState) []reportColumn {
	fit := s.tabFit(tab, st)
	if len(fit.widths) < len(tab.columns) {
		return tab.columns[:len(fit.widths)]
	}
	return tab.columns
}

// reportYardstickLegend says what the mark on those columns means.
//
// Three answers, because there are three facts. The server naming the quote is
// the live one. The server naming something else is a yardstick this build has
// no prose for, and passing its own word through is honest where inventing one
// would not be. The server saying NOTHING is not the same as either: an OMS too
// old to serve variance_measured_against has not told us these are scored
// against the quote, so the legend says so instead of filling the silence in.
func reportYardstickLegend(marked []string, yardstick string) string {
	if len(marked) == 0 {
		return ""
	}
	lead := reportYardstickMark + " " + reportJoinAnd(marked)
	switch {
	case yardstick == omsapi.VarianceYardstickQuotedLeadTime:
		return lead + " are measured against " + reportYardstickQuotedWords +
			" · not the delivery dates confirmed on the orders"
	case yardstick != "":
		// The token is whatever the server sent, so it is bounded BEFORE the
		// legend is folded: a bound expressed in an unbounded value is not a
		// bound, and pickerWrap would otherwise be handed the whole of it.
		return lead + " are measured against " +
			pickerClip(strings.ReplaceAll(yardstick, "_", " "), reportYardstickTokenCells) +
			" · that is the server's own name for the yardstick"
	default:
		return lead + ": this server did not say what they are measured against" +
			" · do not read them as plain lateness"
	}
}

// reportYardstickOf reduces a payload's PER-ROW variance_measured_against to the
// one answer a legend can make about the whole table.
//
// It requires agreement: rows that disagree, or a single row that omitted the
// key, leave the table unable to say what its figures are scored against, and
// "" is that answer rather than the majority one. Naming the yardstick most
// rows carried would put a promise on the pane that some of the figures under
// it were never measured against.
func reportYardstickOf(tokens []string) string {
	if len(tokens) == 0 || tokens[0] == "" {
		return ""
	}
	for _, t := range tokens[1:] {
		if t != tokens[0] {
			return ""
		}
	}
	return tokens[0]
}

// reportJoinAnd lists column names the way a sentence does.
func reportJoinAnd(names []string) string {
	switch len(names) {
	case 0:
		return ""
	case 1:
		return names[0]
	case 2:
		return names[0] + " and " + names[1]
	default:
		return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
	}
}

// belowLines is everything drawn under the table: the tab's standing note, then
// the names of any columns the pane could not hold.
//
// The DROP is named here and marked in the header, which is the same
// lead-with-what-must-survive split the legend makes one level up: the mark is
// on the header row, above the rows and safe from the bottom-up clip, and the
// names are the detail a short pane may take. A column that is simply absent
// still reads as absent; what would be silent is nothing saying it was ever
// there.
func (s *ReportTableScreen) belowLines(tab reportTab, st *reportTabState) []string {
	cells := s.reportPaneCells()
	var out []string
	if tab.note != "" {
		out = append(out, pickerWrap(tab.note, cells)...)
	}
	if !st.loaded || st.err != "" || len(st.rows) == 0 {
		return out
	}
	dropped := s.tabFit(tab, st).dropped
	if len(dropped) == 0 {
		return out
	}
	out = append(out, pickerWrap(reportDropMark+" "+
		fmt.Sprintf("%d %s off the pane: %s", len(dropped), plural("column", len(dropped)), strings.Join(dropped, ", "))+
		" · widen the terminal to see "+reportDropPronoun(len(dropped)), cells)...)
	return out
}

func reportDropPronoun(n int) string {
	if n == 1 {
		return "it"
	}
	return "them"
}

// footerHint names every key an operator can SEE act in the state it is
// drawing, with ONE recorded exception, and is FOLDED by the caller: at 80
// columns it is 58 cells against a pane of 51, so drawn straight it lost "esc
// back" — the way out of the screen — off the right edge with nothing saying it
// had.
//
// SEEN ACT is the whole of what the claim covers, and it is not a hedge: it is
// the same reading of "acts" the rest of this package's bar-honesty sweeps use,
// where a keypress that redraws the pane byte for byte has not acted. The state
// that turns on it is the LOAD, where `r` is bound and fires a second identical
// request whose only product is the "Loading …" line the pane is already
// drawing — naming it there would advertise a key nothing on the frame can be
// seen to answer. What is NOT covered by that reading is a key with a visible
// product, and the loading branch used to omit ←/→ and [/] on exactly those
// grounds while switchTab moved the highlight and replaced the body under it:
// a false claim in the one state every operator lands in, the screen's own
// first frame. They are named there now, in the words the error branch already
// uses, so the two states cannot describe one affordance two ways.
//
// THE EXCEPTION IS `backspace`, which updateKey binds alongside `esc` and this
// bar deliberately does not spell. It is a universal esc alias across this app,
// named by no other surface, so naming it here alone would make this one bar
// disagree with every other one for two cells it would rather spend on a key an
// operator has to be told about — the same trade poFormNavAliases records for
// Tab / Shift-Tab on the columnar forms. Recorded rather than left implicit,
// because an unqualified "exactly" is a documented claim the code does not
// honour.
//
// NO BAR-HONESTY SWEEP COVERS THIS BAR, said plainly because a claim in prose
// either states what a named check proves or should not be written:
// ReportTableScreen is neither a *ListScreen nor a jdeScreen, so
// list_bar_honesty_test.go (which walks the ListScreen footers) and the
// columnar sweep in po_view_jde_test.go (which walks the []actionBarItem bars)
// both miss it, and nothing presses the key space against this one. Giving it
// that coverage means giving the screen a machine-readable bar; until then the
// claim above is held by reading, which is exactly how the alias got lost.
func (s *ReportTableScreen) footerHint(rowCount int) string {
	if len(s.tabs) == 0 {
		return "esc back"
	}
	st := &s.states[s.active]
	switch {
	case st.loading || !st.loaded:
		return "←/→ [/] switch report · esc back"
	case st.err != "":
		return "r retry · ←/→ [/] switch report · esc back"
	case rowCount == 0:
		return "←/→ [/] switch report · r refresh · esc back"
	}
	hint := fmt.Sprintf("%d %s", rowCount, plural("row", rowCount))
	if rowCount > 1 {
		// The movement keys are named because they are BOUND: the footer used
		// to say "j/k move" alone while the arrows, pgup/pgdn, g/G and home/end
		// all worked, which is the standing vocabulary named in list_nav.go
		// half-spelled. Named only above one row, because that is where any of
		// them moves anything.
		hint += " · j/k ↑↓ move · pgup/pgdn page · g/G home/end top/bottom"
	}
	return hint + " · ←/→ [/] switch report · r refresh · esc back"
}

func (s *ReportTableScreen) View() string {
	cells := s.reportPaneCells()
	var b strings.Builder
	b.WriteString(s.renderTabBar() + "\n\n")

	if len(s.tabs) == 0 {
		b.WriteString(StyleMuted.Render("No reports.") + "\n")
		b.WriteString("\n" + StyleMuted.Render(s.footerHint(0)))
		return b.String()
	}

	tab := s.tabs[s.active]
	st := &s.states[s.active]

	writeMuted := func(lines []string) {
		for _, line := range lines {
			b.WriteString(StyleMuted.Render(line) + "\n")
		}
	}
	writeFooter := func(rowCount int) {
		b.WriteString("\n")
		writeMuted(pickerWrap(s.footerHint(rowCount), cells))
	}

	if st.loading || !st.loaded {
		writeMuted(reportBlockRows(pickerWrap("Loading "+tab.label+"…", cells), s.frameRows()))
		writeFooter(0)
		return strings.TrimRight(b.String(), "\n")
	}
	if st.err != "" {
		// The detail is bounded BEFORE the folder sees it. omsapi.parseError
		// puts the ENTIRE raw payload into APIError.Message whenever the JSON
		// envelope carries no code, so this string can be a 20 KB gateway page —
		// and at most reportErrRows lines of it can ever be drawn, so folding
		// the rest is work thrown away on a frame the operator is waiting for.
		//
		// WHAT A ROW GIVES UP IT MARKS, and an error body is the worst place in
		// the program to break that: an operator reading six folded lines of a
		// gateway page with nothing saying a tail went cannot tell they are
		// missing the sentence that says what actually failed. This is
		// po_create.go's failLines / poFailDetailRows, copied rather than
		// reinvented, and its three decisions come with it. The mark spends the
		// LAST of the block's OWN rows instead of growing the block, so the
		// height does not move. The TWO wordings stay, because the two cuts know
		// different things: the FOLD knows how many lines it left and names the
		// number, while the cellPrefix bound has already thrown the rest away
		// and can only say more exists than the pane can hold — a count there
		// would be a count of the PREFIX, which is a figure about nothing. And
		// the mark row is itself bounded, because the row saying something was
		// cut may not be the row that runs off the pane.
		detail := "Error: " + st.err
		// The ceiling is reportErrRows and the PANE can lower it (frameRows),
		// never below the floor that keeps the first line and its mark. The
		// fold-cut-and-mark itself is failDetailLines (pane_text.go), shared
		// with po_create.go and po_add_line.go — this block's own comment
		// already said it was po_create's "copied rather than reinvented", and
		// the third copy had by then lost the mark.
		room := reportErrRows
		if r := s.frameRows(); r < room {
			room = r
		}
		if room < reportErrMinRows {
			room = reportErrMinRows
		}
		lines := failDetailLines(detail, cells, room)
		b.WriteString(StyleStatusError.Render(lines[0]) + "\n")
		writeMuted(lines[1:])
		writeFooter(0)
		return strings.TrimRight(b.String(), "\n")
	}
	if len(st.rows) == 0 {
		// The block under the table gives FIRST and the fact that there are no
		// rows gives last, the same order layoutRows uses on a drawn table.
		rows := s.frameRows()
		lines := reportBlockRows(pickerWrap("No rows for "+tab.label+".", cells), rows)
		below := s.belowLines(tab, st)
		if left := rows - len(lines); left < len(below) {
			if left < 0 {
				left = 0
			}
			below = below[:left]
		}
		writeMuted(lines)
		writeMuted(below)
		writeFooter(0)
		return strings.TrimRight(b.String(), "\n")
	}

	writeMuted(s.legendLines(tab, st))

	header, body := reportTableRows(tab.columns, st.rows, s.tabFit(tab, st))
	b.WriteString(strings.Repeat(" ", reportRowLead) + header + "\n")

	win, shownBelow := s.layoutRows()
	end := st.windowStart + win
	if end > len(body) {
		end = len(body)
	}
	for i := st.windowStart; i < end; i++ {
		line := strings.Repeat(" ", reportRowLead) + body[i]
		if i == st.cursor {
			line = reportRowHighlight().Render("▸ " + body[i])
		}
		b.WriteString(line + "\n")
	}
	if marker := listMarkerLine(st.windowStart > 0, len(body)-end, cells); marker != "" {
		b.WriteString(StyleMuted.Render(marker) + "\n")
	}

	belowAll := s.belowLines(tab, st)
	if shownBelow < len(belowAll) {
		belowAll = belowAll[:shownBelow]
	}
	writeMuted(belowAll)
	writeFooter(len(st.rows))
	return strings.TrimRight(b.String(), "\n")
}

// renderTabBar draws a WINDOW around the active tab rather than every tab in
// order, with ‹ / › for the ones off either side.
//
// Drawn in full it is one long line and Root clips it from the right, so at 80
// columns the reorders report showed its first three tabs and nothing else —
// including when the operator was standing on the sixth, where the pane carried
// no highlight at all and nothing said which report they were reading. A bar
// that cannot show the active tab is not a shortened bar, it is a wrong one.
func (s *ReportTableScreen) renderTabBar() string {
	if len(s.tabs) == 0 {
		return ""
	}
	cells := s.reportPaneCells()
	// The ACTIVE label is drawn through StyleSidebarItemActive, which carries
	// Padding(0, 1) — two cells the plain labels do not have. Asked of the style
	// rather than counted, so a theme that changes the padding changes the
	// measurement with it; unreserved, it pushed the "›" that says the rest of
	// the tabs exist off the pane.
	pad := StyleSidebarItemActive.GetHorizontalPadding()
	labels := make([]string, len(s.tabs))
	for i, t := range s.tabs {
		labels[i] = " " + t.label + " "
	}
	// The active label is CLIPPED to the pane before anything else is measured.
	// It is the one thing the bar must show — a bar that cannot say which report
	// you are reading is worse than a short one — so it can never be what is
	// given up, and at the narrowest pane Root draws (16 cells) a label like
	// " Spend by supplier " does not fit whole. Unclipped it overran the pane and
	// clampToBox took the "›" and part of the label with it.
	//
	// The marks are asked of the MINIMAL window [active, active] because that is
	// the one this clip has to leave drawable: the window can only grow from
	// there, and every growth step measures its own candidate window against the
	// whole pane. So this reserves exactly the markers a bar showing the active
	// tab alone would draw, not markers a wider window will not.
	sep := "│"
	activeRoom := cells - s.tabBarMarks(s.active, s.active) - pad
	if lipgloss.Width(labels[s.active]) > activeRoom {
		labels[s.active] = fitCell(labels[s.active], activeRoom)
	}
	labelCells := func(i int) int {
		w := lipgloss.Width(labels[i])
		if i == s.active {
			w += pad
		}
		return w
	}

	first, last := s.active, s.active
	width := labelCells(s.active)
	// Grow outwards from the active tab, left first so the bar reads in order
	// and the active tab keeps as much context behind it as ahead.
	for {
		grew := false
		if first > 0 {
			w := width + lipgloss.Width(sep) + labelCells(first-1)
			if w+s.tabBarMarks(first-1, last) <= cells {
				first--
				width = w
				grew = true
			}
		}
		if last < len(s.tabs)-1 {
			w := width + lipgloss.Width(sep) + labelCells(last+1)
			if w+s.tabBarMarks(first, last+1) <= cells {
				last++
				width = w
				grew = true
			}
		}
		if !grew {
			break
		}
	}

	var parts []string
	for i := first; i <= last; i++ {
		if i == s.active {
			parts = append(parts, StyleSidebarItemActive.Render(labels[i]))
		} else {
			parts = append(parts, StyleMuted.Render(labels[i]))
		}
	}
	// The markers sit OUTSIDE the separator join: "‹│ Spend by category" reads
	// as a first tab with a broken name, where "‹ Spend by category" reads as
	// what it is.
	bar := strings.Join(parts, StyleMuted.Render(sep))
	if first > 0 {
		bar = StyleMuted.Render("‹") + bar
	}
	if last < len(s.tabs)-1 {
		bar += StyleMuted.Render("›")
	}
	return bar
}

// tabBarMarks is what the ‹ / › markers cost for a window of [first, last].
// Reserved BEFORE a tab is admitted, so admitting one can never be what pushes
// the marker that says the rest exist off the pane.
//
// ONE cell per marker, and only for a side that will really be drawn. They sit
// OUTSIDE the separator join (renderTabBar), so a marker costs the marker and
// nothing else — the separators between labels are already counted by the
// growth loop's own lipgloss.Width(sep). Charging two apiece made this the one
// accounting on the screen that discarded room the terminal had: on the 6-tab
// reorders report at 80 columns it could withhold a tab, or abbreviate the
// active label, over cells that were never going to be spent (house rule 5).
func (s *ReportTableScreen) tabBarMarks(first, last int) int {
	n := 0
	if first > 0 {
		n++
	}
	if last < len(s.tabs)-1 {
		n++
	}
	return n
}

// ---------------------------------------------------------------------------
// Concrete report screens (the three web report pages)
// ---------------------------------------------------------------------------

// NewInventoryReportScreen mirrors the web /reports/inventory page: 3 tabs.
func NewInventoryReportScreen(deps Deps) *ReportTableScreen {
	return NewReportTableScreen(deps, "Inventory report", []reportTab{
		{
			label:   "Stock by category",
			columns: []reportColumn{{"Category", alignLeft}, {"Items", alignRight}, {"Stock", alignRight}, {"Value", alignRight}, {"Low", alignRight}},
			loader: func(ctx context.Context, deps Deps) (reportBody, error) {
				rows, err := deps.OMS.InventoryStockByCategory(ctx)
				if err != nil {
					return reportBody{}, err
				}
				out := make([][]string, len(rows))
				for i, r := range rows {
					out[i] = []string{orDash(r.CategoryName), itoa(r.TotalItems), itoa(r.TotalStock), fmtMoney(r.TotalValue), itoa(r.LowStockCount)}
				}
				return reportBody{rows: out}, nil
			},
		},
		{
			label:   "Reorder frequency",
			columns: []reportColumn{{"Item", alignLeft}, {"SKU", alignLeft}, {"Category", alignLeft}, {"Reorders", alignRight}},
			note:    "Default window: trailing 12 months (the web default).",
			loader: func(ctx context.Context, deps Deps) (reportBody, error) {
				rows, err := deps.OMS.InventoryReorderFrequency(ctx)
				if err != nil {
					return reportBody{}, err
				}
				out := make([][]string, len(rows))
				for i, r := range rows {
					out[i] = []string{orDash(r.ItemName), orDash(r.ItemSKU), orDash(r.CategoryName), itoa(r.ReorderCount)}
				}
				return reportBody{rows: out}, nil
			},
		},
		{
			label:   "Value by location",
			columns: []reportColumn{{"Location", alignLeft}, {"Items", alignRight}, {"Stock", alignRight}, {"Value", alignRight}},
			loader: func(ctx context.Context, deps Deps) (reportBody, error) {
				rows, err := deps.OMS.InventoryValueByLocation(ctx)
				if err != nil {
					return reportBody{}, err
				}
				out := make([][]string, len(rows))
				for i, r := range rows {
					out[i] = []string{orDash(r.LocationName), itoa(r.TotalItems), itoa(r.TotalStock), fmtMoney(r.TotalValue)}
				}
				return reportBody{rows: out}, nil
			},
		},
	})
}

// NewPurchasingReportScreen mirrors the web /reports/purchasing page: 4 tabs.
func NewPurchasingReportScreen(deps Deps) *ReportTableScreen {
	return NewReportTableScreen(deps, "Purchasing report", []reportTab{
		{
			label:   "Spend by supplier",
			columns: []reportColumn{{"Supplier", alignLeft}, {"Orders", alignRight}, {"Spend", alignRight}, {"Avg order", alignRight}},
			loader: func(ctx context.Context, deps Deps) (reportBody, error) {
				rows, err := deps.OMS.PurchasingSpendBySupplier(ctx)
				if err != nil {
					return reportBody{}, err
				}
				out := make([][]string, len(rows))
				for i, r := range rows {
					out[i] = []string{orDash(r.SupplierName), itoa(r.TotalOrders), fmtMoney(r.TotalSpend), fmtMoney(r.AvgOrderValue)}
				}
				return reportBody{rows: out}, nil
			},
		},
		{
			label:   "Spend by category",
			columns: []reportColumn{{"Category", alignLeft}, {"Items", alignRight}, {"Qty", alignRight}, {"Spend", alignRight}},
			loader: func(ctx context.Context, deps Deps) (reportBody, error) {
				rows, err := deps.OMS.PurchasingSpendByCategory(ctx)
				if err != nil {
					return reportBody{}, err
				}
				out := make([][]string, len(rows))
				for i, r := range rows {
					out[i] = []string{orDash(r.CategoryName), itoa(r.TotalItems), itoa(r.TotalQuantity), fmtMoney(r.TotalSpend)}
				}
				return reportBody{rows: out}, nil
			},
		},
		{
			label: "Lead time",
			// "Quote" is avg_estimated_lead_time, which is the supplier link's
			// STANDING QUOTE and not a per-order estimate — the yardstick itself,
			// named in the table so the two marked columns beside it have
			// something on the pane to be measured against.
			columns: []reportColumn{{"Supplier", alignLeft}, {"Item", alignLeft}, {"Ord", alignRight}, {"Quote", alignRight}, {"Actual", alignRight}, {"Var" + reportYardstickMark, alignRight}, {"On-time" + reportYardstickMark, alignRight}},
			note:    "Averages in days over the trailing 6 months · Quote is the supplier link's standing quoted lead time.",
			loader: func(ctx context.Context, deps Deps) (reportBody, error) {
				rows, err := deps.OMS.PurchasingLeadTimeAnalysis(ctx)
				if err != nil {
					return reportBody{}, err
				}
				out := make([][]string, len(rows))
				yards := make([]string, len(rows))
				for i, r := range rows {
					// on_time_rate is ALREADY a percentage (backend computes
					// on_time_count/total*100), so append "%" — do NOT ×100 again.
					out[i] = []string{orDash(r.SupplierName), orDash(r.ItemName), itoa(r.TotalOrders), trimFloat(r.AvgEstimatedLeadTime), trimFloat(r.AvgActualLeadTime), trimFloat(r.AvgVariance), trimFloat(r.OnTimeRate) + "%"}
					yards[i] = r.VarianceMeasuredAgainst
				}
				return reportBody{rows: out, yardstick: reportYardstickOf(yards)}, nil
			},
		},
		{
			label:   "Price trends",
			columns: []reportColumn{{"Item", alignLeft}, {"Supplier", alignLeft}, {"Changes", alignRight}, {"Min", alignRight}, {"Max", alignRight}, {"Latest", alignRight}, {"Change", alignRight}},
			note:    "Default window: trailing 12 months.",
			loader: func(ctx context.Context, deps Deps) (reportBody, error) {
				rows, err := deps.OMS.PurchasingPriceTrends(ctx)
				if err != nil {
					return reportBody{}, err
				}
				out := make([][]string, len(rows))
				for i, r := range rows {
					change := "—"
					if r.PriceChangePercentage != nil {
						change = trimFloat(*r.PriceChangePercentage) + "%"
					}
					out[i] = []string{orDash(r.ItemName), orDash(r.SupplierName), itoa(r.PriceChanges), fmtMoney(r.MinUnitCost), fmtMoney(r.MaxUnitCost), fmtMoney(r.LatestUnitCost), change}
				}
				return reportBody{rows: out}, nil
			},
		},
	})
}

// NewAssetReportScreen mirrors the web /reports/assets page: 5 tabs.
func NewAssetReportScreen(deps Deps) *ReportTableScreen {
	return NewReportTableScreen(deps, "Asset report", []reportTab{
		{
			label:   "By status",
			columns: []reportColumn{{"Status", alignLeft}, {"Count", alignRight}},
			loader: func(ctx context.Context, deps Deps) (reportBody, error) {
				rows, err := deps.OMS.AssetsByStatus(ctx)
				if err != nil {
					return reportBody{}, err
				}
				out := make([][]string, len(rows))
				for i, r := range rows {
					label := r.StatusDisplay
					if label == "" {
						label = r.Status
					}
					out[i] = []string{orDash(label), itoa(r.Count)}
				}
				return reportBody{rows: out}, nil
			},
		},
		{
			label:   "Maintenance due",
			columns: []reportColumn{{"Asset", alignLeft}, {"Tag", alignLeft}, {"Part", alignLeft}, {"SKU", alignLeft}, {"Interval d", alignRight}, {"Since d", alignRight}, {"Overdue d", alignRight}, {"Last replaced", alignLeft}},
			loader: func(ctx context.Context, deps Deps) (reportBody, error) {
				rows, err := deps.OMS.AssetMaintenanceDue(ctx)
				if err != nil {
					return reportBody{}, err
				}
				out := make([][]string, len(rows))
				for i, r := range rows {
					part := r.PartName
					if r.Status == "in_maintenance" {
						part = "(in maintenance)"
					}
					out[i] = []string{orDash(r.AssetName), orDash(r.AssetTag), orDash(part), orDash(r.PartSKU), intOrDash(r.MaintenanceIntervalDays), intOrDash(r.DaysSinceReplacement), intOrDash(r.DaysOverdue), orDash(dateOnly(r.LastReplacedAt))}
				}
				return reportBody{rows: out}, nil
			},
		},
		{
			label:   "Utilization",
			columns: []reportColumn{{"Asset", alignLeft}, {"Tag", alignLeft}, {"Sessions", alignRight}, {"Hours", alignRight}, {"Avg/session", alignRight}},
			note:    "ForgeKey session hours. Default window: trailing 30 days.",
			loader: func(ctx context.Context, deps Deps) (reportBody, error) {
				rows, err := deps.OMS.AssetUtilization(ctx)
				if err != nil {
					return reportBody{}, err
				}
				out := make([][]string, len(rows))
				for i, r := range rows {
					out[i] = []string{orDash(r.AssetName), orDash(r.AssetTag), itoa(r.TotalSessions), trimFloat(r.TotalHours), trimFloat(r.AvgHoursPerSession)}
				}
				return reportBody{rows: out}, nil
			},
		},
		{
			label:   "Total cost of ownership",
			columns: []reportColumn{{"Asset", alignLeft}, {"Tag", alignLeft}, {"Maint d/90", alignRight}, {"Scheduled", alignRight}, {"Unscheduled", alignRight}, {"Preventive", alignRight}, {"Vendor", alignRight}, {"Total 90d", alignRight}},
			note:    "Costs over the trailing 90 days.",
			loader: func(ctx context.Context, deps Deps) (reportBody, error) {
				rows, err := deps.OMS.AssetTCO(ctx)
				if err != nil {
					return reportBody{}, err
				}
				out := make([][]string, len(rows))
				for i, r := range rows {
					out[i] = []string{orDash(r.AssetName), orDash(r.AssetTag), itoa(r.MaintenanceDaysLast90), fmtMoneyStr(r.ScheduledMaintenanceCost), fmtMoneyStr(r.UnscheduledMaintenanceCost), fmtMoneyStr(r.PreventiveMaintenanceCost), fmtMoneyStr(r.VendorMaintenanceCost), fmtMoneyStr(r.TotalMaintenanceCost90d)}
				}
				return reportBody{rows: out}, nil
			},
		},
		{
			label:   "Supplies used",
			columns: []reportColumn{{"Asset", alignLeft}, {"Source", alignLeft}, {"Item", alignLeft}, {"Serial/Qty", alignLeft}, {"Action", alignLeft}, {"Used at", alignLeft}, {"Cost", alignRight}},
			note:    "Serialized installs/consumes + PM-work-order consumables. Default window: trailing 30 days.",
			loader: func(ctx context.Context, deps Deps) (reportBody, error) {
				// nil query → server default window (trailing 30 days), matching
				// how the other windowed tabs mirror the web's default view.
				rows, err := deps.OMS.AssetSuppliesUsed(ctx, nil)
				if err != nil {
					return reportBody{}, err
				}
				out := make([][]string, len(rows))
				for i, r := range rows {
					// Serial/Qty column folds the two source shapes: a serial
					// number for serialized rows, quantity+unit for consumables.
					serialQty := r.SerialNumber
					if r.Source == "consumable" {
						serialQty = strings.TrimSpace(r.Quantity + " " + r.Unit)
					}
					action := r.ActionDisplay
					if action == "" {
						action = r.Action // consumable rows have no action verb → "—"
					}
					out[i] = []string{orDash(r.AssetName), orDash(r.Source), orDash(r.ItemName), orDash(serialQty), orDash(action), orDash(dateOnly(r.UsedAt)), fmtMoneyStr(r.EstimatedCost)}
				}
				return reportBody{rows: out}, nil
			},
		},
	})
}

// itoa renders an int as a decimal string, used across the report loaders.
func itoa(n int) string { return fmt.Sprintf("%d", n) }
