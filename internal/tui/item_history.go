package tui

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/uid0/scantty/internal/omsapi"
)

// item_history.go — an item's two READINGS, opened from the item sheet with `h`:
// how its stock level moved, and who used what. They are the web item page's
// "Stock History" and "Usage Logs" tabs (InventoryItemDetailPage.tsx). ScanTTY
// could write a usage log from the item sheet and could show neither, so the
// operator who had just logged a use could not see it, or anything before it.
//
// ONE SCREEN, TWO VIEWS, switched by `[`/`]` (and `←`/`→`), the keys the tabbed
// report table already uses for the same act. Both readings are fetched together
// on the way in so a switch is never a wait. It writes nothing.
//
// WHAT THE STOCK VIEW IS, AND IS NOT. The web draws a chart; this draws the same
// payload as a DATED TABLE, newest first, each row a level and its change from the
// reading before it. Three things about that payload decide the wording, and
// omsapi's item_history.go carries the wire rationale for all three:
//
//   - a COUNT row's level is the level ON RECORD when the count was taken, before
//     the count replaced it — not the number counted. The row is labelled
//     `count (before)` and the head says so in a sentence, because a table
//     labelling it "counted" would be wrong by exactly the correction the count
//     made;
//   - a REORDER row has a date and no level, so its level and change cells are
//     blank rather than carried from a neighbour;
//   - every level is BASE units while the reorder point and the desired level are
//     in the item's COUNT unit, so the head names each figure's unit separately
//     rather than drawing all of them as one axis the way the chart does.
//
// WHAT THE USAGE VIEW IS. One row per log, newest first: when, how many, who, and
// the note the web draws. The quantity is BASE units whatever unit it was typed
// in, because that is all the row stores, and the head says "as stored". WHO is a
// user NUMBER, because the only endpoint turning a pk into a name is staff-only;
// a row with no recorder says so rather than guessing "anonymous", since the
// work-order material path writes its actor into the note instead.
//
// THE BAR IS A RECORD (prose_bar.go) in every state, the window is
// proseFlatListFrame's line-packed one — a usage note is any number of lines, so
// a row is not one line — and a row taller than the window is clipped with its
// cut named.

// itemHistoryView is which reading the screen is drawing.
type itemHistoryView int

const (
	itemHistoryStock itemHistoryView = iota
	itemHistoryUsage
)

// ItemHistoryScreen is the item's stock history and usage logs.
type ItemHistoryScreen struct {
	deps Deps
	item *omsapi.Item

	view itemHistoryView

	history *omsapi.StockHistory
	logs    []omsapi.UsageLog
	// stockErr and usageErr are ONE reading's failure, drawn on that reading's
	// view with the other still reachable. Both are set when both failed, and then
	// loadErr carries the frame instead — there is nothing either view could draw.
	stockErr string
	usageErr string

	loading bool
	loadErr string
	// pending counts the readings of the load in flight still out. A reload
	// pressed over a load resets it, so the older load's answers can end the
	// frame early — with readings a moment older than the ones still coming,
	// which then land over them. No answer is dropped and none is stale for long.
	pending int

	stockCursor int
	usageCursor int
	stockStart  int
	usageStart  int

	terminalWidth  int
	terminalHeight int
}

// itemHistoryStockMsg and itemHistoryUsageMsg are the two readings' answers.
type itemHistoryStockMsg struct {
	history *omsapi.StockHistory
	err     error
}

type itemHistoryUsageMsg struct {
	logs []omsapi.UsageLog
	err  error
}

// NewItemHistoryScreen opens the history of `item`, which must be the item the
// sheet has loaded: its units are what the figures are labelled in.
func NewItemHistoryScreen(deps Deps, item *omsapi.Item) *ItemHistoryScreen {
	return &ItemHistoryScreen{deps: deps, item: item, loading: true, pending: 2}
}

func (s *ItemHistoryScreen) Title() string {
	if s.item != nil && s.item.Name != "" {
		return "History: " + s.item.Name
	}
	return "Item history"
}

// Init fetches the first load; the constructor has already counted its two
// readings out.
func (s *ItemHistoryScreen) Init() tea.Cmd { return s.fetch() }

// reload starts a new load.
func (s *ItemHistoryScreen) reload() tea.Cmd {
	s.pending = 2
	s.loading = true
	s.loadErr = ""
	return s.fetch()
}

// fetch asks for both readings at once for the load in flight, and the frame
// stays loading until BOTH have answered, so it goes from loading to loaded in
// one step and a view switch never waits.
func (s *ItemHistoryScreen) fetch() tea.Cmd {
	deps := s.deps
	ctx := deps.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	id := ""
	if s.item != nil {
		id = s.item.ID
	}
	return tea.Batch(
		func() tea.Msg {
			h, err := deps.OMS.GetItemStockHistory(ctx, id)
			return itemHistoryStockMsg{history: h, err: err}
		},
		func() tea.Msg {
			logs, err := deps.OMS.ListItemUsageLogs(ctx, id)
			return itemHistoryUsageMsg{logs: logs, err: err}
		},
	)
}

func (s *ItemHistoryScreen) paneCells() int { return proseBarCells(s.terminalWidth) }

// ---------------------------------------------------------------------------
// The bar
// ---------------------------------------------------------------------------

// itemHistorySwitch is the view switch, worded for where it goes. All four
// keystrokes go there: with two views, forward and back are the same view.
func itemHistorySwitch(to itemHistoryView) proseBarItem {
	return proseBarItem{
		Keys: []string{"[", "]", "left", "right"},
		Hint: "[/] ←→ " + itemHistoryViewName(to),
	}
}

func itemHistoryViewName(v itemHistoryView) string {
	if v == itemHistoryUsage {
		return "usage logs"
	}
	return "stock history"
}

func (v itemHistoryView) other() itemHistoryView {
	if v == itemHistoryUsage {
		return itemHistoryStock
	}
	return itemHistoryUsage
}

// bar names every key that acts on the current view holding `rows` rows.
//
// NO PAGER, because none is bound: a page of rows of different heights is the
// question AssetPartsScreen's proseBarUnconverted entry records as undecided, and
// the jumps to either end are what a long usage list needs. The movement segments
// come off below two rows (listNavMoves).
func (s *ItemHistoryScreen) bar(rows int) proseBar {
	out := proseNavList(listNavMoves(rows), false)
	return append(out, itemHistorySwitch(s.view.other()), proseBarReloadFor(s.viewErr() != ""), proseBarEsc)
}

// proseBar is the bar this screen is DRAWING, in every state.
func (s *ItemHistoryScreen) proseBar() proseBar {
	if s.loading || s.loadErr != "" {
		return s.loadBar()
	}
	return s.bar(s.rowCount())
}

// loadBar is the bar while the load is out or both readings failed: the reload
// and the way back. The view switch and the movement keys are NOT answered there
// — the frame draws neither view, so a switch would change a field nothing draws
// and a later frame would open on a view the operator never chose.
func (s *ItemHistoryScreen) loadBar() proseBar {
	return proseBar{proseBarReloadFor(s.loadErr != ""), proseBarEsc}
}

// viewErr is the failure of the reading the current view draws.
func (s *ItemHistoryScreen) viewErr() string {
	if s.view == itemHistoryUsage {
		return s.usageErr
	}
	return s.stockErr
}

// ---------------------------------------------------------------------------
// Update
// ---------------------------------------------------------------------------

func (s *ItemHistoryScreen) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		s.terminalWidth, s.terminalHeight = m.Width, m.Height
		return s, nil
	case itemHistoryStockMsg:
		s.history, s.stockErr = m.history, ""
		if m.err != nil {
			s.history, s.stockErr = nil, m.err.Error()
		}
		s.answered()
		return s, nil
	case itemHistoryUsageMsg:
		s.logs, s.usageErr = m.logs, ""
		if m.err != nil {
			s.logs, s.usageErr = nil, m.err.Error()
		}
		s.answered()
		return s, nil
	case tea.KeyMsg:
		key := m.String()
		if key == "r" {
			return s, s.reload()
		}
		if s.loading || s.loadErr != "" {
			return s, nil
		}
		cursor := &s.stockCursor
		if s.view == itemHistoryUsage {
			cursor = &s.usageCursor
		}
		last := s.rowCount() - 1
		switch key {
		case "[", "]", "left", "right":
			s.view = s.view.other()
		case "j", "down":
			if *cursor < last {
				*cursor++
			}
		case "k", "up":
			if *cursor > 0 {
				*cursor--
			}
		case "g", "home":
			*cursor = 0
		case "G", "end":
			if last > 0 {
				*cursor = last
			}
		}
	}
	return s, nil
}

// answered counts one reading in, and once both are in ends the load: the
// frame's failure is set only where BOTH failed, since either view alone still
// has something to draw.
func (s *ItemHistoryScreen) answered() {
	if s.pending > 0 {
		s.pending--
		if s.pending > 0 {
			return
		}
	}
	s.loading = false
	s.loadErr = ""
	if s.stockErr != "" && s.usageErr != "" {
		s.loadErr = s.stockErr
		if s.usageErr != s.stockErr {
			s.loadErr = "stock history: " + s.stockErr + " · usage logs: " + s.usageErr
		}
	}
	s.stockCursor = clampIndex(s.stockCursor, len(itemHistoryStockRows(s.history)))
	s.usageCursor = clampIndex(s.usageCursor, len(s.logs))
}

// clampIndex keeps an index inside a list of n, at 0 for an empty one.
func clampIndex(i, n int) int {
	if i >= n {
		i = n - 1
	}
	if i < 0 {
		i = 0
	}
	return i
}

// rowCount is how many rows the current view draws.
func (s *ItemHistoryScreen) rowCount() int {
	if s.view == itemHistoryUsage {
		return len(s.logs)
	}
	return len(itemHistoryStockRows(s.history))
}

// ---------------------------------------------------------------------------
// The stock table
// ---------------------------------------------------------------------------

// itemStockRowKind is what a stock-history row records.
type itemStockRowKind int

const (
	itemStockSnapshot itemStockRowKind = iota
	itemStockCount
	itemStockReorder
)

// itemStockRow is one dated reading. level and change are absent on a reorder
// row, which has no level; change is absent on the first reading that has one.
type itemStockRow struct {
	kind      itemStockRowKind
	date      string
	level     int
	hasLevel  bool
	change    int
	hasChange bool
}

// itemStockLabels are the row labels. A count row says `before` because its
// level is the one the count REPLACED (see the file comment).
var itemStockLabels = map[itemStockRowKind]string{
	itemStockSnapshot: "weekly snapshot",
	itemStockCount:    "count (before)",
	itemStockReorder:  "reorder requested",
}

// itemHistoryStockRows merges the three lists into one table, NEWEST FIRST.
//
// The change is taken chronologically — each level against the reading before it
// that HAS a level — and the order within one date is the order the three lists
// are named in the payload, so a same-day snapshot, count and reorder read
// top-down as reorder, count, snapshot once reversed. Nothing is merged away: the
// web chart lets a same-day count overwrite a snapshot, and a table has room for
// both facts.
func itemHistoryStockRows(h *omsapi.StockHistory) []itemStockRow {
	if h == nil {
		return nil
	}
	var rows []itemStockRow
	for _, p := range h.Series {
		rows = append(rows, itemStockRow{kind: itemStockSnapshot, date: p.Date.Format("2006-01-02"), level: p.Count, hasLevel: true})
	}
	for _, p := range h.CycleCounts {
		rows = append(rows, itemStockRow{kind: itemStockCount, date: p.Date.Format("2006-01-02"), level: p.Count, hasLevel: true})
	}
	for _, e := range h.ReorderEvents {
		rows = append(rows, itemStockRow{kind: itemStockReorder, date: e.Date.Format("2006-01-02")})
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].date < rows[j].date })
	prev, seen := 0, false
	for i := range rows {
		if !rows[i].hasLevel {
			continue
		}
		if seen {
			rows[i].change, rows[i].hasChange = rows[i].level-prev, true
		}
		prev, seen = rows[i].level, true
	}
	for i, j := 0, len(rows)-1; i < j; i, j = i+1, j-1 {
		rows[i], rows[j] = rows[j], rows[i]
	}
	return rows
}

// itemHistorySigned is a change with its sign, so a rise and a fall are told
// apart without colour.
func itemHistorySigned(n int) string {
	if n > 0 {
		return "+" + strconv.Itoa(n)
	}
	return strconv.Itoa(n)
}

// baseQty is n in the item's base unit, pluralised.
func (s *ItemHistoryScreen) baseQty(n int) string {
	return fmt.Sprintf("%d %s", n, pluralizeUnit(baseUnitOf(s.item), n))
}

// countQty is n in the item's COUNT unit — the unit minimum_stock and
// reorder_quantity are stored in.
func (s *ItemHistoryScreen) countQty(n int) string {
	return fmt.Sprintf("%d %s", n, pluralizeUnit(countUnitOf(s.item), n))
}

// rowCells is the room one row line has: the pane, less the two cells of padding
// the cursor's highlight adds, reserved on EVERY row so a row that fits is not
// cut on the press that highlights it.
func (s *ItemHistoryScreen) rowCells() int {
	return s.paneCells() - StyleSidebarItemActive.GetHorizontalPadding()
}

func (s *ItemHistoryScreen) stockRows(rows []itemStockRow) []string {
	levelW, changeW := 0, 0
	for _, r := range rows {
		if r.hasLevel && len(strconv.Itoa(r.level)) > levelW {
			levelW = len(strconv.Itoa(r.level))
		}
		if r.hasChange && len(itemHistorySigned(r.change)) > changeW {
			changeW = len(itemHistorySigned(r.change))
		}
	}
	out := make([]string, len(rows))
	for i, r := range rows {
		level, change := "", ""
		if r.hasLevel {
			level = strconv.Itoa(r.level)
		}
		if r.hasChange {
			change = itemHistorySigned(r.change)
		}
		caret := "  "
		if i == s.stockCursor {
			caret = "▸ "
		}
		line := fmt.Sprintf("%s%s  %*s  %*s  %s", caret, r.date, levelW, level, changeW, change, itemStockLabels[r.kind])
		line = pickerClip(line, s.rowCells())
		if i == s.stockCursor {
			line = StyleSidebarItemActive.Render(line)
		}
		out[i] = line
	}
	return out
}

// stockHead is what the stock view opens with: the view strip, the live level and
// the two thresholds each in its own unit, and — only where a count row is drawn —
// the sentence saying what its level is.
func (s *ItemHistoryScreen) stockHead(rows []itemStockRow) string {
	var b strings.Builder
	b.WriteString(s.viewStrip())
	if h := s.history; h != nil {
		facts := fmt.Sprintf("Now %s · reorder point %s · desired %s",
			s.baseQty(h.CurrentStock), s.countQty(h.Thresholds.ReorderPoint), s.countQty(h.Thresholds.Desired))
		b.WriteString(s.foldedHead(facts))
	}
	for _, r := range rows {
		if r.kind == itemStockCount {
			b.WriteString(s.foldedHead("A count row shows the level on record before the count."))
			break
		}
	}
	return b.String()
}

// ---------------------------------------------------------------------------
// The usage list
// ---------------------------------------------------------------------------

func (s *ItemHistoryScreen) usageRows() []string {
	out := make([]string, len(s.logs))
	for i, l := range s.logs {
		caret := "  "
		if i == s.usageCursor {
			caret = "▸ "
		}
		who := "no recorder"
		if l.ChargedBy != nil {
			who = "by user #" + strconv.Itoa(*l.ChargedBy)
		}
		line := pickerClip(fmt.Sprintf("%s%s  %s  %s", caret,
			l.UsageDate.Local().Format("2006-01-02 15:04"), s.baseQty(l.QuantityUsed), who), s.rowCells())
		if i == s.usageCursor {
			line = StyleSidebarItemActive.Render(line)
		}
		// The note as the web draws it, every stored line kept and each clipped
		// with its cut marked (proseClipEachLine says why line by line), and
		// styled a line at a time so no styling spans a newline.
		if note := strings.TrimRight(l.Notes, "\n"); strings.TrimSpace(note) != "" {
			const indent = "    "
			for _, n := range strings.Split(proseClipEachLine(note, s.rowCells()-len(indent)), "\n") {
				line += "\n" + indent + StyleMuted.Render(n)
			}
		}
		out[i] = line
	}
	return out
}

func (s *ItemHistoryScreen) usageHead() string {
	unit := pluralUnit(baseUnitOf(s.item))
	noun := "usage logs"
	if len(s.logs) == 1 {
		noun = "usage log"
	}
	return s.viewStrip() + s.foldedHead(fmt.Sprintf("%d %s, newest first · quantities in %s, as stored", len(s.logs), noun, unit))
}

// ---------------------------------------------------------------------------
// View
// ---------------------------------------------------------------------------

// viewStrip names both views and marks the one drawn, as a whole line. The mark
// is a glyph and not only a colour, so the switch is visible on a terminal with
// none.
func (s *ItemHistoryScreen) viewStrip() string {
	names := []string{"  Stock history", "  Usage logs"}
	names[s.view] = "▸ " + strings.TrimSpace(names[s.view])
	return StyleTitle.Render(pickerClip(strings.Join(names, "   "), s.paneCells())) + "\n"
}

// foldedHead is one head sentence folded to the pane, every row a whole line.
func (s *ItemHistoryScreen) foldedHead(text string) string {
	var b strings.Builder
	for _, line := range pickerWrap(text, s.paneCells()) {
		b.WriteString(StyleMuted.Render(line) + "\n")
	}
	return b.String()
}

func (s *ItemHistoryScreen) View() string {
	cells := s.paneCells()
	if s.loading {
		return proseLoadingFrame("Loading stock history and usage logs…", cells, s.proseBar())
	}
	if s.loadErr != "" {
		return proseFailedFrame(s.loadErr, s.terminalHeight, cells, s.proseBar())
	}
	drawn := s.proseBar()
	ceiling := s.bar(proseFlatCeilingRows)

	if s.view == itemHistoryStock {
		if s.stockErr != "" {
			return s.viewFailed(s.viewStrip(), "Stock history: ", s.stockErr, drawn)
		}
		rows := itemHistoryStockRows(s.history)
		head := s.stockHead(rows)
		if len(rows) == 0 {
			return head + s.foldedHead("No stock history for this item yet: no weekly snapshot, count or reorder on record.") +
				"\n" + drawn.render(cells)
		}
		return proseFlatListFrame(head, s.stockRows(rows), s.stockCursor, &s.stockStart,
			s.terminalHeight, cells, ceiling, drawn)
	}
	if s.usageErr != "" {
		return s.viewFailed(s.viewStrip(), "Usage logs: ", s.usageErr, drawn)
	}
	if len(s.logs) == 0 {
		return s.viewStrip() + s.foldedHead("No usage logged for this item.") + "\n" + drawn.render(cells)
	}
	return proseFlatListFrame(s.usageHead(), s.usageRows(), s.usageCursor, &s.usageStart,
		s.terminalHeight, cells, ceiling, drawn)
}

// viewFailed is one view whose reading failed while the other did not: the strip
// (so the view that DID load is named as reachable), the failure bounded to what
// the pane leaves once the bar has its rows, and the bar.
//
// Bounded for the reason proseFailedFrame gives — an OMS body can be a whole
// gateway page — and floored at one row, for the reason it gives about a failure
// frame that says nothing about the failure.
func (s *ItemHistoryScreen) viewFailed(head, lead, detail string, bar proseBar) string {
	cells := s.paneCells()
	rows := proseFailedUnsizedRows
	if s.terminalHeight > 0 {
		rows = screenBodyRows(s.terminalHeight) - strings.Count(head, "\n") - bar.rows(cells)
	}
	if rows < 1 {
		rows = 1
	}
	var b strings.Builder
	b.WriteString(head)
	for i, line := range failDetailLines(lead+jdeStatusOneLine(detail), cells, rows) {
		if i == 0 && strings.HasPrefix(line, lead) {
			line = StyleStatusError.Render(lead) + strings.TrimPrefix(line, lead)
		}
		b.WriteString(line + "\n")
	}
	return b.String() + "\n" + bar.render(cells)
}
