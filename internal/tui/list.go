package tui

import (
	"context"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/uid0/scantty/internal/forgekeyapi"
	"github.com/uid0/scantty/internal/omsapi"
)

type listScreenSpec struct {
	kind   string
	loader func(ctx context.Context, deps Deps) ([]listRow, error)
	// searchLoader, when non-nil, gives the list a server-side search input
	// (opened with '/'). The typed query is forwarded to the backend loader —
	// e.g. as ?search= on the OMS list endpoint — and the returned rows replace
	// the list. When nil the list is load-once with local sort only, and '/'
	// falls through to the global search palette.
	searchLoader func(ctx context.Context, deps Deps, query string) ([]listRow, error)
	detail       func(id string, deps Deps) Screen
	// newScreen, when non-nil, gives the list an `n` (new) action that opens a
	// create form for this resource. The list claims 'n' via HandlesKey (it
	// collides with the global notifications hotkey) and advertises it in the
	// footer. Editing/deleting an existing row still lives on the detail screen
	// (or, for resources with no edit endpoint, nowhere) — this hook is create
	// only, so a list without a create form simply leaves it nil.
	newScreen func(deps Deps) Screen
	// filters, when non-empty (with filterLoader), gives the list an `f` key
	// that cycles server-side filtered views — e.g. purchase orders by status.
	// filters[0] is the view the list opens on, so make it the unfiltered one
	// and the landing list stays what it was. The list claims 'f' via
	// HandlesKey (it collides with the global firmware hotkey).
	//
	// Cycling re-fetches rather than filtering the rows already in hand: a
	// status that isn't on the loaded page would otherwise be unreachable.
	filters []listFilter
	// filterLoader fetches rows for the active filter's query. When set
	// alongside filters it REPLACES loader — a filter-driven list leaves
	// loader nil and expresses its unfiltered view as filters[0].
	filterLoader func(ctx context.Context, deps Deps, q url.Values) ([]listRow, error)
}

// listFilter is one view in a list's filter cycle: a label for the header and
// the query params handed to the filtered loader. A nil query is the
// unfiltered view.
type listFilter struct {
	label string
	query url.Values
}

type listRow struct {
	ID        string
	Title     string
	Subtitle  string
	Tag       string
	CreatedAt time.Time
	// Optional secondary timestamp shown in the date column when CreatedAt is
	// zero (e.g. for resources that only expose updated_at or last_seen).
	FallbackDate time.Time
	// MetricsLine, when non-empty, is a pre-styled line rendered verbatim beneath
	// the row title — the inventory list uses it for the per-item Q's & Costs
	// metrics row (bold labels). It is emitted as-is and NOT re-wrapped in a muted
	// style: it already carries its own bold/plain styling, and double-wrapping
	// would let the inner reset codes clobber the outer style. A row can carry
	// both a Subtitle and a MetricsLine, but the inventory loader sets only one.
	MetricsLine string
}

func (r listRow) sortDate() time.Time {
	if !r.CreatedAt.IsZero() {
		return r.CreatedAt
	}
	return r.FallbackDate
}

type listSortMode int

const (
	sortDefault listSortMode = iota
	sortDateDesc
	sortDateAsc
	sortTitleAsc
)

func (m listSortMode) label() string {
	switch m {
	case sortDateDesc:
		return "newest"
	case sortDateAsc:
		return "oldest"
	case sortTitleAsc:
		return "title"
	}
	return "default"
}

type listLoadedMsg struct {
	rows []listRow
	err  error
}

// listSearchedMsg carries the result of a server-side search. seq lets the
// screen drop a stale response whose query the operator has already typed past.
type listSearchedMsg struct {
	seq  int
	rows []listRow
	err  error
}

const listWindowSize = 20

// The search overlay's own row, stated once so its two halves cannot disagree
// about how wide the other is. listSearchInputWidth is the scrolling viewport
// bubbles gives the query: the pane, less the prompt, less the cell the cursor
// takes past the end of the value, less the widest suffix View can append
// after the box.
//
// Without a Width, bubbles emits the whole value and clampToBox cut the caret
// off the right edge past roughly the 43rd character — every further keystroke
// redrew the row byte for byte, which is the reported hang reached by typing.
// The suffix is reserved rather than left to be cut because a width also makes
// bubbles PAD a short value out to it, so an unreserved count would be pushed
// past the pane edge on every query instead of only on long ones.
const (
	listSearchPrompt = "search ▸ "
	// "  " + the longest count View writes, at four digits of results.
	listSearchWidestSuffix = "  9999 match(es)"
)

var listSearchInputWidth = screenBodyWidth(80) -
	len([]rune(listSearchPrompt)) - 1 - len(listSearchWidestSuffix)

type ListScreen struct {
	deps           Deps
	title          string
	spec           listScreenSpec
	rawRows        []listRow
	rows           []listRow
	cursor         int
	windowStart    int
	windowSize     int
	sort           listSortMode
	loading        bool
	loadErr        string
	terminalHeight int

	// filter indexes spec.filters — which server-side view the list is
	// showing. Cycled with 'f'; 0 (the unfiltered view) on entry.
	filter int

	// Server-side search (only when spec.searchLoader != nil). searching flips
	// the screen into raw-input mode so the query textinput gets every key;
	// searchQuery is the query currently reflected in rawRows; searchSeq guards
	// against stale async responses; searchPending shows a subtle indicator
	// while a reload is in flight (the prior rows stay visible meanwhile).
	searching     bool
	searchInput   textinput.Model
	searchQuery   string
	searchSeq     int
	searchPending bool
}

// listBodyLines is how many LINES of list body the pane has left after the
// chrome around it.
//
// Chrome accounted for here, in addition to the global screen body math
// in layout.go:
//
//	1 row for the "Sort: … · N rows" header
//	1 row each for ↑/↓ indicators when the list overflows the window
//	1 blank separator above the hint
//	however many rows the FOLDED hint actually occupies
func (s *ListScreen) listBodyLines() int {
	const listHeaderRows = 1
	// Reserve both indicator slots up front; we'd rather waste one row
	// when only one indicator shows than clip a row when the list
	// overflows.
	const listIndicatorRows = 2

	avail := screenBodyHeight(s.terminalHeight) - listHeaderRows - listIndicatorRows
	if s.searching {
		// The overlay replaces the browse footer rather than sitting above it
		// (bodyView drops the footer while searching), and adds an input line,
		// its folded bar and a blank separator of its own.
		avail -= 2 + len(pickerWrap(listSearchBarHint, pickerPaneWidth))
	} else {
		avail -= s.footerRows()
	}
	if avail < 2 {
		avail = 2
	}
	return avail
}

// rowsFittingFrom returns how many rows starting at `start` fit in the body,
// counting the LINES each one actually renders: a title line, plus a line for
// a subtitle and another for a metrics line where the loader supplies them.
// Falls back to the line budget itself when no rows are loaded yet.
//
// The count depends on WHICH rows are in the window, which is why nothing may
// hold on to an answer computed for a different start. Sizing once at load
// time and keeping it through every scroll is what put twenty lines into an
// eighteen-line pane on a list whose first rows are plain and whose later ones
// carry both extra lines (an inventory list is exactly that shape) — and what
// clampToBox then dropped was the folded footer, taking "N new PO" and its
// siblings with it. The same claim, cut off the same edge, for the third time:
// horizontally, then vertically, then by counting rows where the renderer
// counts lines.
func (s *ListScreen) rowsFittingFrom(start int) int {
	avail := s.listBodyLines()
	if len(s.rows) == 0 {
		return avail
	}
	if start < 0 {
		start = 0
	}
	used, count := 0, 0
	for i := start; i < len(s.rows); i++ {
		cost := 1
		if s.rows[i].Subtitle != "" {
			cost++
		}
		if s.rows[i].MetricsLine != "" {
			cost++
		}
		if used+cost > avail {
			break
		}
		used += cost
		count++
	}
	if count < 1 {
		// One row over the budget beats zero rows: a window of none renders a
		// list with no rows in it, and scrollIntoView's walk would never
		// terminate. It takes a pane too short for a single fat row to get
		// here, which 24 lines is not.
		count = 1
	}
	return count
}

func NewListScreen(deps Deps, title string, spec listScreenSpec) *ListScreen {
	return &ListScreen{
		deps:       deps,
		title:      title,
		spec:       spec,
		loading:    true,
		windowSize: listWindowSize,
		sort:       sortDateDesc,
	}
}

func (s *ListScreen) Title() string { return s.title }

// HandlesKey claims lowercase 's' (cycle sort) so it beats the global
// s=settings nav; settings stays reachable from every screen that doesn't
// own a local 's'. Other list keys don't collide with globals, so they reach
// this screen through the normal fallthrough.
//
// When the list supports server-side search it also claims '/', overriding the
// global search-palette hotkey so '/' filters THIS list against the backend;
// ctrl+k still opens the universal palette from here.
//
// A list with a filter cycle likewise claims 'f' over the global firmware
// hotkey. Both claims are conditional, so a list without the feature leaves
// the key to the global layer.
func (s *ListScreen) HandlesKey(key string) bool {
	if key == "s" {
		return true
	}
	if key == "n" && s.spec.newScreen != nil {
		return true
	}
	if key == "f" && s.hasFilters() {
		return true
	}
	return key == "/" && s.spec.searchLoader != nil
}

// hasFilters reports whether this list has a working filter cycle (both halves
// of the spec are needed: the views and the loader that fetches them).
func (s *ListScreen) hasFilters() bool {
	return s.spec.filterLoader != nil && len(s.spec.filters) > 0
}

// activeFilter is the view the list is currently showing. The zero listFilter
// (no label, nil query) stands in for a list with no filter cycle, so callers
// can test f.query == nil without first checking hasFilters.
func (s *ListScreen) activeFilter() listFilter {
	if !s.hasFilters() {
		return listFilter{}
	}
	if s.filter < 0 || s.filter >= len(s.spec.filters) {
		return s.spec.filters[0]
	}
	return s.spec.filters[s.filter]
}

// WantsRawInput routes every keypress to the screen while the search input is
// open, so the global hotkey layer stops eating letters the operator is typing.
func (s *ListScreen) WantsRawInput() bool { return s.searching }

// Init loads the CURRENT view: the filtered loader bound to the active
// filter's query when the list has a filter cycle, else the plain loader. It
// is also the reload path for 'r' (refresh) and for the filter cycle itself,
// so both stay on whichever view is selected.
func (s *ListScreen) Init() tea.Cmd {
	ctx := s.deps.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	deps := s.deps
	if s.hasFilters() {
		loader := s.spec.filterLoader
		q := s.activeFilter().query
		return func() tea.Msg {
			rows, err := loader(ctx, deps, q)
			return listLoadedMsg{rows: rows, err: err}
		}
	}
	if s.spec.loader == nil {
		s.loading = false
		return nil
	}
	loader := s.spec.loader
	return func() tea.Msg {
		rows, err := loader(ctx, deps)
		return listLoadedMsg{rows: rows, err: err}
	}
}

func (s *ListScreen) applySort() {
	rows := make([]listRow, len(s.rawRows))
	copy(rows, s.rawRows)
	switch s.sort {
	case sortDateDesc:
		sort.SliceStable(rows, func(i, j int) bool {
			return rows[i].sortDate().After(rows[j].sortDate())
		})
	case sortDateAsc:
		sort.SliceStable(rows, func(i, j int) bool {
			a, b := rows[i].sortDate(), rows[j].sortDate()
			if a.IsZero() {
				return false
			}
			if b.IsZero() {
				return true
			}
			return a.Before(b)
		})
	case sortTitleAsc:
		sort.SliceStable(rows, func(i, j int) bool {
			return strings.ToLower(rows[i].Title) < strings.ToLower(rows[j].Title)
		})
	}
	s.rows = rows
	if s.cursor >= len(s.rows) {
		s.cursor = len(s.rows) - 1
	}
	if s.cursor < 0 {
		s.cursor = 0
	}
	s.scrollIntoView()
}

// scrollIntoView keeps the cursor inside the window AND re-derives how many
// rows that window holds. The two cannot be separated: rowsFittingFrom packs
// by the LINES each row renders, so the answer depends on which rows the
// window starts at, and every key that moves the cursor moves that start.
func (s *ListScreen) scrollIntoView() {
	if len(s.rows) == 0 {
		s.windowStart = 0
		s.windowSize = s.rowsFittingFrom(0)
		return
	}
	if s.cursor < 0 {
		s.cursor = 0
	}
	if s.cursor >= len(s.rows) {
		s.cursor = len(s.rows) - 1
	}
	if s.windowStart > s.cursor {
		s.windowStart = s.cursor
	}
	if s.windowStart < 0 {
		s.windowStart = 0
	}
	// Walk the start forward until the cursor is inside the window that start
	// can actually afford, re-packing at each step because moving the start
	// changes which rows are counted. It terminates at windowStart == cursor,
	// where a window of one row is enough.
	for s.windowStart < s.cursor && s.cursor >= s.windowStart+s.rowsFittingFrom(s.windowStart) {
		s.windowStart++
	}
	// Then back, while the rows below still reach the end of the list: a
	// window that runs off the end wastes pane on blank space (a taller
	// terminal, or a filter that shortened the list, is the ordinary way to
	// get there). Pulling back is only allowed while the cursor stays inside
	// it — this is the old maxStart clamp, asked of the packed window instead
	// of a row count.
	for s.windowStart > 0 {
		prev := s.windowStart - 1
		fits := s.rowsFittingFrom(prev)
		if prev+fits < len(s.rows) || prev+fits <= s.cursor {
			break
		}
		s.windowStart = prev
	}
	s.windowSize = s.rowsFittingFrom(s.windowStart)
}

func (s *ListScreen) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		s.terminalHeight = m.Height
		s.scrollIntoView()
		return s, nil
	case listLoadedMsg:
		s.loading = false
		s.rawRows = m.rows
		if m.err != nil {
			s.loadErr = m.err.Error()
		} else {
			s.loadErr = ""
		}
		s.applySort()
		// scrollIntoView re-derives the window now that rows are known:
		// rowsFittingFrom walks the actual subtitle population, and the sizing
		// pass from WindowSizeMsg had only the 0-row fallback to go on.
		s.scrollIntoView()
		return s, nil
	case listSearchedMsg:
		if m.seq != s.searchSeq {
			return s, nil // a fresher query has been typed; drop this response
		}
		s.searchPending = false
		s.rawRows = m.rows
		if m.err != nil {
			s.loadErr = m.err.Error()
		} else {
			s.loadErr = ""
		}
		s.cursor = 0
		s.windowStart = 0
		s.applySort()
		s.scrollIntoView()
		return s, nil
	case tea.KeyMsg:
		if s.searching {
			return s.updateSearch(m)
		}
		switch m.String() {
		case "j", "down":
			if s.cursor < len(s.rows)-1 {
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
			if s.cursor >= len(s.rows) {
				s.cursor = len(s.rows) - 1
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
			s.cursor = len(s.rows) - 1
			if s.cursor < 0 {
				s.cursor = 0
			}
			s.scrollIntoView()
		case "s":
			s.sort = (s.sort + 1) % 4
			s.applySort()
		case "f":
			// Cycle the server-side view (e.g. PO status). `f` reaches us only
			// because HandlesKey claims it over the global firmware hotkey,
			// and only when the list actually has a filter cycle. The whole
			// row set is replaced, so the cursor goes back to the top rather
			// than pointing at whatever now occupies its old index.
			if !s.hasFilters() {
				return s, nil
			}
			s.filter = (s.filter + 1) % len(s.spec.filters)
			s.cursor = 0
			s.windowStart = 0
			s.loading = true
			return s, s.Init()
		case "r":
			s.loading = true
			return s, s.Init()
		case "n":
			// Open the create form for this resource, when it has one. `n`
			// reaches us only because HandlesKey claims it (it's a global
			// hotkey otherwise); a list with no create form leaves newScreen
			// nil and never claims the key.
			if s.spec.newScreen == nil {
				return s, nil
			}
			return s, SwitchTo(workspaceForKind(s.spec.kind), s.spec.newScreen(s.deps))
		case "/":
			if s.spec.searchLoader == nil {
				return s, nil
			}
			return s.enterSearch()
		case "enter":
			return s.openSelected()
		}
		// The uppercase sibling-surface accelerators. Last, so a letter the list
		// itself binds always wins — and checked from the SAME table the footer
		// prints, which is what stops the two from drifting apart again.
		for _, sc := range listShortcuts(s.spec.kind) {
			if m.String() == sc.key {
				return s, SwitchTo(workspaceForKind(s.spec.kind), sc.open(s.deps))
			}
		}
	}
	return s, nil
}

// enterSearch opens the server-side search input over the current list. The
// already-loaded rows stay visible until the operator types a query.
func (s *ListScreen) enterSearch() (Screen, tea.Cmd) {
	s.searching = true
	in := textinput.New()
	in.Prompt = listSearchPrompt
	in.Placeholder = "name / tag / serial…"
	in.CharLimit = 120
	in.Width = listSearchInputWidth
	in.SetValue(s.searchQuery)
	in.CursorEnd()
	in.Focus()
	s.searchInput = in
	s.scrollIntoView()
	return s, textinput.Blink
}

// updateSearch owns key handling while the search input is open. Arrow keys
// move the result cursor and enter opens the highlighted row (mirroring the
// search palette); esc closes search and restores the unfiltered list; every
// other key edits the query and, on change, fires a fresh backend search.
func (s *ListScreen) updateSearch(m tea.KeyMsg) (Screen, tea.Cmd) {
	switch m.Type {
	case tea.KeyEsc:
		s.searching = false
		s.searchInput.Blur()
		s.scrollIntoView()
		if s.searchQuery != "" {
			// Restore the full list the plain loader produces.
			s.searchQuery = ""
			s.searchPending = false
			s.loading = true
			return s, s.Init()
		}
		return s, nil
	case tea.KeyUp:
		if s.cursor > 0 {
			s.cursor--
			s.scrollIntoView()
		}
		return s, nil
	case tea.KeyDown:
		if s.cursor < len(s.rows)-1 {
			s.cursor++
			s.scrollIntoView()
		}
		return s, nil
	case tea.KeyEnter:
		return s.openSelected()
	}
	prev := s.searchInput.Value()
	var cmd tea.Cmd
	s.searchInput, cmd = s.searchInput.Update(m)
	if s.searchInput.Value() != prev {
		s.searchQuery = strings.TrimSpace(s.searchInput.Value())
		s.searchPending = true
		return s, tea.Batch(cmd, s.runSearch())
	}
	return s, cmd
}

// runSearch forwards the current query to the backend via spec.searchLoader.
// The bumped seq is stamped on the response so a slow reply for a query the
// operator has already typed past is dropped on arrival.
func (s *ListScreen) runSearch() tea.Cmd {
	if s.spec.searchLoader == nil {
		return nil
	}
	s.searchSeq++
	seq := s.searchSeq
	query := s.searchQuery
	loader := s.spec.searchLoader
	deps := s.deps
	ctx := deps.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return func() tea.Msg {
		rows, err := loader(ctx, deps, query)
		return listSearchedMsg{seq: seq, rows: rows, err: err}
	}
}

// openSelected opens the detail screen for the row under the cursor, if the
// list has a detail builder. Shared by the plain list and the search overlay.
func (s *ListScreen) openSelected() (Screen, tea.Cmd) {
	if s.spec.detail == nil || s.cursor < 0 || s.cursor >= len(s.rows) {
		return s, nil
	}
	next := s.spec.detail(s.rows[s.cursor].ID, s.deps)
	if next == nil {
		return s, nil
	}
	return s, SwitchTo(workspaceForKind(s.spec.kind), next)
}

func workspaceForKind(kind string) Workspace {
	switch kind {
	case "inventory_items":
		return WSInventory
	case "assets":
		return WSAssets
	case "purchase_orders":
		return WSPurchasing
	case "work_orders":
		return WSMaintenance
	case "sigs":
		return WSSIGs
	case "fk_devices":
		return WSForgeKey
	case "project_storage":
		// Opened from the Facilities menu; keep the nav highlight there
		// when drilling into a stint detail.
		return WSFacilities
	}
	return WSDashboard
}

func (s *ListScreen) View() string {
	// The search overlay renders above whatever body state follows, so the
	// operator can keep editing the query even when a search returns nothing.
	if s.searching {
		var head strings.Builder
		head.WriteString(s.searchInput.View())
		switch {
		case s.searchPending:
			head.WriteString("  " + StyleMuted.Render("searching…"))
		case s.searchQuery != "" && s.loadErr == "":
			head.WriteString("  " + StyleMuted.Render(fmt.Sprintf("%d match(es)", len(s.rows))))
		}
		head.WriteString("\n")
		head.WriteString(pickerHint(listSearchBarHint) + "\n\n")
		return head.String() + s.bodyView()
	}
	return s.bodyView()
}

func (s *ListScreen) bodyView() string {
	if s.loading {
		return StyleMuted.Render("Loading…")
	}
	if s.loadErr != "" {
		return StyleStatusError.Render("Error: ") + s.loadErr + "\n\n" + StyleMuted.Render("press r to retry")
	}
	if len(s.rows) == 0 {
		if s.searching {
			return StyleMuted.Render("No matches.")
		}
		// An empty FILTERED view is real information ("there are no drafts"),
		// not an empty resource — name the view so it can't be misread, and
		// point at the key that gets back out of it.
		if f := s.activeFilter(); f.query != nil {
			return StyleMuted.Render(fmt.Sprintf("No rows in the %q view.", f.label)) +
				"\n\n" + StyleMuted.Render("press f to cycle the filter")
		}
		return StyleMuted.Render("No rows.")
	}

	var b strings.Builder
	header := fmt.Sprintf("Sort: %s · %d rows", s.sort.label(), len(s.rows))
	if s.hasFilters() {
		header = fmt.Sprintf("Sort: %s · Filter: %s · %d rows",
			s.sort.label(), s.activeFilter().label, len(s.rows))
	}
	b.WriteString(StyleMuted.Render(header) + "\n")
	if s.windowStart > 0 {
		b.WriteString(StyleMuted.Render("  ↑ more above") + "\n")
	}

	end := s.windowStart + s.windowSize
	if end > len(s.rows) {
		end = len(s.rows)
	}
	for i := s.windowStart; i < end; i++ {
		row := s.rows[i]
		marker := "  "
		if i == s.cursor {
			marker = "▸ "
		}
		title := row.Title
		if row.Tag != "" {
			title = title + " " + StyleMuted.Render("("+row.Tag+")")
		}
		date := ""
		if d := row.sortDate(); !d.IsZero() {
			date = StyleMuted.Render(d.Format("2006-01-02") + "  ")
		}
		line := marker + date + title
		if i == s.cursor {
			line = StyleSidebarItemActive.Render(line)
		}
		b.WriteString(line)
		b.WriteString("\n")
		if row.Subtitle != "" {
			b.WriteString("    ")
			b.WriteString(StyleMuted.Render(row.Subtitle))
			b.WriteString("\n")
		}
		// MetricsLine is already styled (bold labels); emit it verbatim rather
		// than muting it, so its inner reset codes don't clobber an outer style.
		if row.MetricsLine != "" {
			b.WriteString("    ")
			b.WriteString(row.MetricsLine)
			b.WriteString("\n")
		}
	}

	if end < len(s.rows) {
		b.WriteString(StyleMuted.Render(fmt.Sprintf("  ↓ %d more below", len(s.rows)-end)) + "\n")
	}

	if s.searching {
		// The search overlay owns the keyboard: updateSearch swallows every
		// key that is not an arrow, enter or esc straight into the query, so
		// pressing N here types "N". Drawing the browse footer under the
		// overlay's own bar put two contradictory action bars on the pane at
		// once, and folding the footer is what made all eight sibling-surface
		// claims legible in the one state where none of them work. A frame
		// names only the keys that work in the state it is drawing.
		return b.String()
	}
	b.WriteString("\n")
	// FOLDED, not truncated — and listBodyLines reserves footerRows() for
	// what the fold produces. Folding without moving that budget is how the
	// first attempt at this traded a horizontal cut for a vertical one and
	// dropped the same claim off the BOTTOM of the pane instead of the right
	// edge; the number of rows the bar occupies is derived from the bar, never
	// assumed.
	// footerHint is one long sentence and the content
	// pane is 51 columns at 80 (screenBodyWidth), where clampToBox cuts rather
	// than wraps — the fixed prefix alone filled all 51, so every sibling-
	// surface key the footer restored ("N new PO" and its seven siblings) was
	// off the right edge on every list. A bar the operator cannot read is not
	// an honest bar, it is an absent one, and the report this came from opens
	// "as the command line says". pickerWrap is pane-local (po_create_pickers.go)
	// so this needs nothing from the shared JD Edwards layer.
	b.WriteString(pickerHint(s.footerHint()))
	return b.String()
}

// listSearchBarHint is the search overlay's action bar. A named constant so the
// renderer and listBodyLines's row reservation read the same string — a bar
// whose rows are budgeted from a different literal is a bar that gets cut.
const listSearchBarHint = "↑/↓ move · enter open · esc cancel"

// footerRows is how many rows the folded footer occupies, plus its blank
// separator. Derived from the hint that will actually be drawn rather than
// assumed: the hint grows a segment whenever a list gains a sibling surface,
// and a constant here silently spends the extra row out of the pane's bottom.
func (s *ListScreen) footerRows() int {
	return 1 + len(pickerWrap(s.footerHint(), pickerPaneWidth))
}

// footerHint is the list's action bar: every key that works here, and nothing
// else. A method rather than a local so the honesty sweep can read the CLAIM
// structurally and press each key it makes, instead of scraping the last line
// of a rendered pane (list_bar_honesty_test.go).
//
// Each conditional arm is the bar's half of a guard the handler also applies —
// `f` only with a filter cycle, `/` only with a search loader, `n` only with a
// create form. Keep them paired: an arm added here without its guard in Update
// is the exact defect this shape exists to prevent.
func (s *ListScreen) footerHint() string {
	hint := "j/k ↑↓ move · pgup/pgdn page · g/G home/end top/bottom · s sort"
	if s.hasFilters() {
		hint += " · f filter"
	}
	hint += " · r refresh"
	if s.spec.detail != nil {
		hint += " · enter open"
	}
	if s.spec.searchLoader != nil {
		hint += " · / search"
	}
	if s.spec.newScreen != nil {
		hint += " · n new"
	}
	for _, sc := range listShortcuts(s.spec.kind) {
		hint += " · " + sc.key + " " + sc.label
	}
	return hint
}

// listShortcut is one SIBLING SURFACE a list advertises in its footer: an
// uppercase letter, the words printed after it, and the screen it opens.
//
// It exists because the footer used to name these as a hand-written string —
// "· N new PO · Q pending reorders" and its four siblings — pointing at global
// letter accelerators in app.go. Phase 3 of the redesign deleted every one of
// those globals ("the root holds no letter", app.go) and moved their
// destinations into the nav tree. The hint strings stayed. The result was that
// EIGHT advertised keys did nothing at all — N, Q, I, L, U, M, A dead, and G
// worse than dead, since the same footer already binds G to "go to bottom" —
// and the one the captain reached for first was N on the purchase-order list.
//
// A hint appended as a literal beside a handler that never learned about it can
// only drift. So the letter, the words and the destination are ONE record, read
// by the footer and by Update, and the honesty sweep walks this table: a key
// here is named and works, or it is not here at all.
//
// The case carries meaning. LOWERCASE acts on this list (j/k move, s sort, f
// filter, r refresh, n create, enter open); UPPERCASE leaves it for another
// surface of the same workspace. G is the exception that proves it, and is why
// categories is C: `G` was already the list's own go-to-bottom.
type listShortcut struct {
	key   string
	label string
	open  func(Deps) Screen
}

// listShortcuts is the per-kind table. Every destination here is also a row of
// the nav tree (workspaceSurfaces, route.go) — these are accelerators for the
// surface an operator on this list reaches for most, not the only way there.
func listShortcuts(kind string) []listShortcut {
	switch kind {
	case "purchase_orders":
		return []listShortcut{
			{"N", "new PO", func(d Deps) Screen { return NewPurchaseOrderCreateScreen(d) }},
			{"Q", "pending reorders", func(d Deps) Screen { return NewReorderQueueScreen(d) }},
		}
	case "inventory_items":
		// Editing/deleting an item lives on its detail screen (E / x).
		return []listShortcut{
			{"I", "new item", func(d Deps) Screen { return NewInventoryItemFormScreen(d, "") }},
			{"C", "categories", func(d Deps) Screen { return NewCategoryListScreen(d) }},
			{"L", "locations", func(d Deps) Screen { return NewLocationListScreen(d) }},
			{"U", "suppliers", func(d Deps) Screen { return NewSupplierListScreen(d) }},
		}
	case "work_orders":
		// The Maintenance landing lists work orders; PM items are created,
		// edited and acted on (complete / clone / generate-WO) over there.
		return []listShortcut{
			{"M", "PM items", func(d Deps) Screen { return NewMaintenanceItemsScreen(d) }},
		}
	case "assets":
		// Edit/delete of an existing asset live on its detail screen (E / x).
		return []listShortcut{
			{"A", "new asset", func(d Deps) Screen { return NewAssetFormScreen(d, "") }},
		}
	}
	return nil
}

func loadInventoryItems(ctx context.Context, deps Deps) ([]listRow, error) {
	// Ask for the embedded metrics so each row can show the Q's & Costs line at a
	// glance (Ian UX). On a backend that predates ?with_metrics the items carry
	// no metrics and each row falls back to the plain SKU/stock subtitle.
	page, err := deps.OMS.ListItemsWithMetrics(ctx)
	if err != nil {
		return nil, err
	}
	rows := make([]listRow, 0, len(page.Results))
	for _, it := range page.Results {
		tag := ""
		if it.NeedsReorder {
			tag = "needs-reorder"
		}
		// A retired item is never flagged for reorder server-side (op-jv7r), so
		// the two are mutually exclusive; surface the phase-out with its own tag.
		if it.IsRetired {
			tag = "retired"
		}
		row := listRow{
			ID:           it.ID,
			Title:        it.Name,
			Tag:          tag,
			CreatedAt:    it.CreatedAt,
			FallbackDate: it.UpdatedAt,
		}
		// The list keeps the SKU cell (it identifies which item the numbers
		// belong to) and bolds the headers. Without metrics, degrade to the
		// former SKU/stock subtitle so the row still says something.
		if it.Metrics != nil {
			row.MetricsLine = formatItemMetricsRow(it.Metrics, it.SKU, metricsRowOpts{withSKU: true, boldLabels: true})
		} else {
			row.Subtitle = fmt.Sprintf("SKU %s · stock %d", it.SKU, it.Stock)
		}
		rows = append(rows, row)
	}
	return rows, nil
}

func loadAssets(ctx context.Context, deps Deps) ([]listRow, error) {
	return assetRows(ctx, deps, nil)
}

// searchAssets forwards the operator's query to the OMS asset endpoint as
// ?search=, which AssetViewSet.get_queryset matches (icontains) against name /
// description / serial_number / asset_tag / manufacturer_name. This is what
// makes an asset findable from the list by its DMS-YYANNNSS asset_tag — the
// plain page-1 loader (loadAssets) can't surface a tag past the first page.
func searchAssets(ctx context.Context, deps Deps, query string) ([]listRow, error) {
	var q url.Values
	if term := strings.TrimSpace(query); term != "" {
		q = url.Values{"search": []string{term}}
	}
	return assetRows(ctx, deps, q)
}

func assetRows(ctx context.Context, deps Deps, q url.Values) ([]listRow, error) {
	page, err := deps.OMS.ListAssets(ctx, q)
	if err != nil {
		return nil, err
	}
	rows := make([]listRow, 0, len(page.Results))
	for _, a := range page.Results {
		subtitle := a.Description
		if a.AssetTag != "" {
			if subtitle != "" {
				subtitle = a.AssetTag + " · " + subtitle
			} else {
				subtitle = a.AssetTag
			}
		}
		fallback := a.UpdatedAt
		if a.LastScannedAt != nil && !a.LastScannedAt.IsZero() {
			fallback = *a.LastScannedAt
		}
		rows = append(rows, listRow{
			ID:           fmt.Sprint(a.ID),
			Title:        a.Name,
			Subtitle:     subtitle,
			Tag:          a.Status,
			CreatedAt:    a.CreatedAt,
			FallbackDate: fallback,
		})
	}
	return rows, nil
}

// purchaseOrderFilters is the PO list's status-filter cycle, mirroring the
// "Filter by Status" options on the web PO list page (op-nr6h). filters[0] is
// unfiltered, so the Purchasing landing list is unchanged; "draft" comes next
// because a saved-but-unsent order is the one an operator has to come BACK to
// — creating a PO leaves it in draft, and po_detail's `s` (send to supplier)
// is what resumes it — and until this cycle existed a draft was only findable
// by scrolling the mixed list.
//
// cancelled/voided are deliberately absent, matching the web's option set (a
// voided PO with no live lines is dropped from the list endpoint anyway).
//
// The backend shows drafts to AUTHENTICATED users only: PurchaseOrderViewSet
// restricts an anonymous list to sent/confirmed/partially_received/received
// before applying ?status=, so a logged-out session gets an empty draft view
// rather than an error.
var purchaseOrderFilters = []listFilter{
	{label: "all"},
	{label: "draft", query: url.Values{"status": []string{"draft"}}},
	{label: "sent", query: url.Values{"status": []string{"sent"}}},
	{label: "confirmed", query: url.Values{"status": []string{"confirmed"}}},
	{label: "partially received", query: url.Values{"status": []string{"partially_received"}}},
	{label: "received", query: url.Values{"status": []string{"received"}}},
}

// purchaseOrderRows loads the PO list under the given query params — the
// active filter's ?status=, or nil for everything. The status filter is
// applied server-side (PurchaseOrderViewSet.get_queryset), which is what makes
// a draft findable at all: a local filter could only ever narrow the first
// page. Each row keeps its status as the row Tag, so a draft still reads
// "(draft)" in the mixed view.
func purchaseOrderRows(ctx context.Context, deps Deps, q url.Values) ([]listRow, error) {
	page, err := deps.OMS.ListPurchaseOrders(ctx, q)
	if err != nil {
		return nil, err
	}
	rows := make([]listRow, 0, len(page.Results))
	for _, po := range page.Results {
		title := po.Number
		if title == "" {
			title = fmt.Sprintf("PO #%v", po.ID)
		}
		subtitle := poListSubtitle(po)
		created := po.CreatedAt
		if created.IsZero() {
			created = po.OrderDate
		}
		rows = append(rows, listRow{
			ID:           fmt.Sprint(po.ID),
			Title:        title,
			Subtitle:     subtitle,
			Tag:          po.Status,
			CreatedAt:    created,
			FallbackDate: po.UpdatedAt,
		})
	}
	return rows, nil
}

func loadWorkOrders(ctx context.Context, deps Deps) ([]listRow, error) {
	page, err := deps.OMS.ListWorkOrders(ctx, nil)
	if err != nil {
		return nil, err
	}
	rows := make([]listRow, 0, len(page.Results))
	for _, wo := range page.Results {
		rows = append(rows, listRow{
			ID:           fmt.Sprint(wo.ID),
			Title:        wo.Title,
			Subtitle:     wo.AssetName,
			Tag:          wo.Status,
			CreatedAt:    wo.CreatedAt,
			FallbackDate: wo.UpdatedAt,
		})
	}
	return rows, nil
}

func loadSIGs(ctx context.Context, deps Deps) ([]listRow, error) {
	page, err := deps.OMS.ListSIGs(ctx, nil)
	if err != nil {
		return nil, err
	}
	rows := make([]listRow, 0, len(page.Results))
	for _, sig := range page.Results {
		subtitleParts := []string{}
		if sig.MemberCount > 0 {
			subtitleParts = append(subtitleParts, fmt.Sprintf("%d members", sig.MemberCount))
		}
		if sig.AssetCount > 0 {
			subtitleParts = append(subtitleParts, fmt.Sprintf("%d assets", sig.AssetCount))
		}
		if sig.InventoryCount > 0 {
			subtitleParts = append(subtitleParts, fmt.Sprintf("%d items", sig.InventoryCount))
		}
		tag := ""
		if sig.IsUserAdmin {
			tag = "admin"
		}
		rows = append(rows, listRow{
			ID:       fmt.Sprintf("%d", sig.ID),
			Title:    sig.Name,
			Subtitle: strings.Join(subtitleParts, " · "),
			Tag:      tag,
		})
	}
	return rows, nil
}

func loadForgeKeyDevices(ctx context.Context, deps Deps) ([]listRow, error) {
	devices, err := deps.ForgeKey.ListDevices(ctx, nil)
	if err != nil {
		return nil, err
	}
	return forgekeyDeviceRows(devices), nil
}

func forgekeyDeviceRows(devices []forgekeyapi.Device) []listRow {
	rows := make([]listRow, 0, len(devices))
	for _, d := range devices {
		tag := "offline"
		if d.IsOnline {
			tag = "online"
		}
		typeLabel := d.DeviceTypeName
		if typeLabel == "" {
			typeLabel = fmt.Sprintf("%v", d.DeviceType)
		}
		subtitle := fmt.Sprintf("%s · %s", typeLabel, d.MACAddress)
		if d.Location != nil {
			subtitle += fmt.Sprintf(" · loc #%d", *d.Location)
		}
		rows = append(rows, listRow{
			ID:           fmt.Sprint(d.ID),
			Title:        d.Name,
			Subtitle:     subtitle,
			Tag:          tag,
			FallbackDate: d.LastSeen,
		})
	}
	return rows
}

func poListSubtitle(po omsapi.PurchaseOrder) string {
	parts := []string{}
	supplier := po.SupplierName
	if supplier == "" {
		supplier = po.SupplierDetails
	}
	if supplier != "" {
		parts = append(parts, supplier)
	}
	if !po.EstimatedTotal.Empty() {
		curr := po.Currency
		if curr == "" {
			curr = "USD"
		}
		parts = append(parts, "$"+string(po.EstimatedTotal)+" "+curr)
	} else if po.Total > 0 {
		curr := po.Currency
		if curr == "" {
			curr = "USD"
		}
		parts = append(parts, fmt.Sprintf("%.2f %s", po.Total, curr))
	}
	if po.TotalItems > 0 {
		parts = append(parts, fmt.Sprintf("%d items", po.TotalItems))
	}
	return strings.Join(parts, " · ")
}
