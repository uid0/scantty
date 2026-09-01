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
	"github.com/charmbracelet/lipgloss"

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
	lipgloss.Width(listSearchPrompt) - 1 - lipgloss.Width(listSearchWidestSuffix)

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
	terminalWidth  int

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

// The list pane's fixed chrome, named so the budget and the REFUSAL below can
// be built out of the same numbers rather than each counting the rows for
// itself. A budget that disagrees with what the renderer draws is how the bar
// came to be cut off the bottom in the first place.
const (
	// listHeaderRows: the "Sort: … · N rows" line, drawn by the loaded pane and
	// by the empty one (headerLine says why the empty one needs it too).
	listHeaderRows = 1

	// listIndicatorRows: the "↑ more above" / "↓ more below" pair, each on the
	// row its arrow points away from. Reserved only when the rows really do
	// outrun the body (listOverflows) AND the pane can afford both — see
	// markerRows, which spends ONE row instead where it cannot, and why that is
	// a reservation rather than a mutilation.
	listIndicatorRows = 2

	// listMinBodyRows: the floor on an EMPTY list, where the body is the "no
	// rows" sentence and nothing else. A loaded list's floor is bigger and is
	// asked of the rows themselves (minBodyLines) — a ROW is what its body has
	// to be able to draw, and a row is not one line.
	//
	// Below the floor there is nothing to draw a frame around and the pane is
	// refused rather than mutilated — the stance jdeTooShort takes on the
	// columnar layer, for the same reason: a footer with rows cut off the bottom
	// names some keys and hides the rest, silently, and the operator cannot tell
	// which.
	listMinBodyRows = 1

	// listMaxRowLines: the most lines ONE row can render to — its title, its
	// Subtitle and its MetricsLine. Derived from rowLineCost rather than guessed
	// at, and it exists only so minBodyLines can stop walking once it has found
	// a row that cannot be beaten.
	listMaxRowLines = 3
)

// listPaneRows is how many rows this screen's View() may really draw into.
//
// screenBodyRows and NOT screenBodyHeight, which floors at four and is
// therefore a LIE below a terminal height of ten — and a caller that has to fit
// a footer pinned to the bottom of the pane cannot budget against a lie
// (layout.go says so in as many words). Budgeting against the floor is exactly
// what put the folded footer past the bottom edge: at 80x14 the purchase-order
// list assembled nine rows into a pane with eight, and clampToBox drops from
// the BOTTOM, so what went was "· N new PO · Q pending reorders" — the same
// claim off the same edge for the fourth time, horizontally, then vertically,
// then by counting rows where the renderer counts lines, and now by budgeting
// against a floored height.
//
// An UNSIZED screen has no pane to measure, and the standing answer for that is
// the columnar layer's: draw whole and let clampToBox decide. So it keeps
// screenBodyHeight's floor as something to size a window with, and paneSized
// below is what stops it being REFUSED for a height nobody has told it yet.
//
// Named listPaneRows and not paneRows because jdeScreen.paneRows is marked
// jde:layer-only: a list is not a columnar sheet and this is its own arithmetic,
// but sharing the name is how the two would come to be read as one.
func (s *ListScreen) listPaneRows() int {
	if !s.paneSized() {
		return screenBodyHeight(s.terminalHeight)
	}
	return screenBodyRows(s.terminalHeight)
}

func (s *ListScreen) paneSized() bool { return s.terminalHeight > 0 }

// barRows is the rows the bar this pane will actually draw occupies, plus the
// blank separator that travels with it.
//
// The searching branch measures listSearchBarHint — the CEILING — for the
// reason that constant carries: the live overlay bar sheds keys as the result
// count changes, and a body budget that moved with it would make the list jump
// under the operator's hands while they type.
func (s *ListScreen) barRows() int {
	if s.searching {
		// The overlay replaces the browse footer rather than sitting above it
		// (bodyView drops the footer while searching), and adds an input line,
		// its folded bar and a blank separator of its own.
		return 2 + len(pickerWrap(listSearchBarHint, s.listPaneCells()))
	}
	return s.footerRows()
}

// listOverflows reports whether the rows outrun the body the pane can give them
// once the header and the bar are paid for — which is exactly when an indicator
// row can appear.
//
// Asked WITHOUT the indicator reservation on purpose, and that is what makes it
// well founded rather than circular: if every row fits in the body that is left
// when nothing is reserved, then the window holds all of them, windowStart
// stays 0, end reaches len(rows), and NEITHER marker is drawn — so reserving
// for them would be spending two rows on markers that cannot appear. If they do
// not fit, the pair is reserved and at most both are drawn. Either way the
// assembled pane is bounded by listPaneRows, which is the property the refusal
// below depends on.
func (s *ListScreen) listOverflows() bool {
	if len(s.rows) == 0 {
		return false
	}
	return s.rowsOutrun(s.listPaneRows() - listHeaderRows - s.barRows())
}

// rowLineCost is what ONE row renders to, counted the way bodyView draws it: a
// title line, plus a line for a Subtitle and another for a MetricsLine.
func rowLineCost(r listRow) int {
	cost := 1
	if r.Subtitle != "" {
		cost++
	}
	if r.MetricsLine != "" {
		cost++
	}
	return cost
}

// rowsOutrun reports whether the whole row set renders to more LINES than
// `budget`. Rows are counted in lines because that is what the pane spends —
// counting them as rows is what put twenty lines into an eighteen-line pane.
//
// It answers the comparison rather than the total, and stops as soon as the
// answer is settled, so its cost is the BUDGET and not the length of the list.
// scrollIntoView walks the window start in a loop and every step asks the body
// budget, so a total computed over every row would make one keypress on a long
// list quadratic — the shape of the truncateVisible hang this project has
// already paid for once.
func (s *ListScreen) rowsOutrun(budget int) bool {
	total := 0
	for _, r := range s.rows {
		total += rowLineCost(r)
		if total > budget {
			return true
		}
	}
	return false
}

// minBodyLines is the smallest body a LOADED list can honestly be drawn into:
// enough lines for its TALLEST row.
//
// A row is not one line — purchaseOrderRows gives a PO with a supplier or a
// total a Subtitle, loadInventoryItems gives an item with metrics a MetricsLine
// — and rowsFittingFrom will not return an empty window, so it hands back a row
// that costs more lines than the budget rather than nothing at all. Floored at
// ONE line, that is the budget overflowing by whatever the row's extra lines
// come to, and what the overflow pushes off the bottom is the footer: at 80x14
// the maintenance list drew a two-line row into a one-line body and lost
// "enter open · M PM items" off the end of its bar. Found by sweeping the
// CURSOR through the list at every drawable height rather than at two — the
// window is packed from wherever the cursor is, so the rows in it change as the
// operator moves and only the TALLEST one bounds the answer.
//
// The maximum over the whole list rather than the cost of the row the cursor
// happens to be on, because this feeds needRows and a refusal that flickered as
// the cursor moved would be worse than either answer.
func (s *ListScreen) minBodyLines() int {
	max := listMinBodyRows
	for _, r := range s.rows {
		if cost := rowLineCost(r); cost > max {
			max = cost
			if max == listMaxRowLines {
				break // nothing can be taller; the rest of the walk is dead work
			}
		}
	}
	return max
}

// markerSlack is what the pane has left for the ↑/↓ markers once the header,
// the bar and a whole ROW of body are paid for. It is the one number both the
// reservation and the refusal are computed from, so they cannot disagree.
func (s *ListScreen) markerSlack() int {
	return s.listPaneRows() - listHeaderRows - s.barRows() - s.minBodyLines()
}

// markerRows is how many rows the ↑/↓ markers get: none when the rows fit, the
// PAIR when the pane can afford one apiece, and ONE — shared, listMarkerLine —
// when it cannot.
//
// THE SHARED ROW IS WHAT LETS THE REFUSAL COME DOWN A ROW, and reserving one
// without it would be a defect rather than a saving. The tempting version is
// "reserve one, because ↑ only appears once windowStart > 0 and the pane opens
// at 0" — true about the OPENING state and false about the next keypress: at
// the boundary the body is exactly one row, so the first `j` scrolls, both
// markers apply, and there is nothing left for the second to come out of. The
// assembled pane then overruns by a row and clampToBox takes it off the BOTTOM,
// where the bar is, which is the whole defect this budget exists to close. (
// Re-asking the refusal after the scroll is worse still: the frame would flip
// to the notice mid-scroll with the movement keys held, which is a dead end.)
// So the reservation is honoured by the RENDERER instead — at most one marker
// ROW is ever drawn where only one is reserved, and it carries both facts.
//
// IT IS NOT MONOTONE IN TERMINAL HEIGHT, and nothing may be built on a claim
// that it is: as the pane grows it runs 1 → 2 → 0, because a taller pane first
// affords the second marker row and then stops overflowing at all. What the
// too-short notice rests on is the FIXED POINT, and that argument is written
// once, at needRows below — do not restate it here, or the two statements of a
// subtle property drift apart, which is how this sentence came to say the
// opposite of the one under it.
func (s *ListScreen) markerRows() int {
	if !s.listOverflows() {
		return 0
	}
	if s.markerSlack() >= listIndicatorRows {
		return listIndicatorRows
	}
	return 1
}

// listMarkerLine is the ↑/↓ markers as ONE bounded row — the shared row on a
// pane that can spare only one, and each marker on its own where the pane paid
// for the pair. All three draw sites go through it so a bound applied to the
// shared row cannot leave the other two unbounded beside it.
//
// BOTH FACTS OR NEITHER, and the give-order is what makes that true at a width
// rather than only at 51 cells. The shared row was assembled at full length and
// then clipped to the pane, so below the width it fits the operator read
// "  ↑ more above · ↓ 1" (45 columns) or "  ↑ more above · ↓ 12 more b"
// (60) — a row promising two facts and delivering one and a half, with the
// truncation unmarked and, at 45, A COUNT CUT MID-NUMBER. A cut number reads as
// a number: "↓ 1" over twelve rows below is not a shortened fact, it is a wrong
// one, which is the price-column rule (@ 3.50 drawn as @ 3.) on a different row.
//
// So the PROSE gives, in order, and the ARROWS and the COUNT never do: the row
// says "more above"/"more below", then "above"/"below", then nothing but the
// arrows and the figure. Where even that will not fit, the COUNT is DROPPED
// rather than cut and the row carries the ellipsis that says a fact was given
// up — the arrows still say there is more in both directions, which is the half
// an operator acts on, and no number is stated that the list does not have.
func listMarkerLine(above bool, below, cells int) string {
	var forms []string
	var bare string
	switch {
	case above && below > 0:
		forms = []string{
			fmt.Sprintf("  ↑ more above · ↓ %d more below", below),
			fmt.Sprintf("  ↑ above · ↓ %d below", below),
			fmt.Sprintf("  ↑ · ↓ %d", below),
		}
		bare = "  ↑ · ↓"
	case above:
		forms = []string{"  ↑ more above", "  ↑ above", "  ↑"}
		bare = "  ↑"
	case below > 0:
		forms = []string{
			fmt.Sprintf("  ↓ %d more below", below),
			fmt.Sprintf("  ↓ %d below", below),
			fmt.Sprintf("  ↓ %d", below),
		}
		bare = "  ↓"
	default:
		return ""
	}
	if cells <= 0 {
		return ""
	}
	for _, form := range forms {
		if lipgloss.Width(form) <= cells {
			return form
		}
	}
	if cells == 1 {
		return "…"
	}
	return cellPrefix(bare, cells-1) + "…"
}

// listBodyLines is how many LINES of list body the pane has left after the
// chrome around it: the header, the indicator pair where it can appear, and the
// bar with its separator.
//
// It floors at listMinBodyRows rather than at two, and the floor is only ever
// reached on a pane paneDrawn has already refused — so it is a guard against a
// negative window size, not a claim about rows the pane does not have. That
// distinction is the whole of this repair: the old floor of two handed the
// renderer two rows it could not draw, and the renderer drew them.
func (s *ListScreen) listBodyLines() int {
	avail := s.listPaneRows() - listHeaderRows - s.markerRows() - s.barRows()
	if floor := s.minBodyLines(); avail < floor {
		avail = floor
	}
	return avail
}

// needRows is the shortest pane this list can draw its footer WHOLE into, in
// screen-body rows.
//
// Two shapes, because the two panes are different: the EMPTY one is fixed —
// header, blank, the "no rows" sentence, then the footer's own blank and its
// folded lines — while the LOADED one has to keep room for its TALLEST ROW
// (minBodyLines) and for the markers that say there are more.
//
// THE HEIGHT IT NAMES IS A HEIGHT THAT DRAWS, which is the only property that
// matters about it, and it holds at the FIXED POINT rather than by needRows
// being monotone (it is not: markerRows can grow from one row to two as the
// pane does). A refused pane has a marker slack of 0 or less, so markerRows is
// 1 there and the height named is header + 1 + minBodyLines + bar. At that
// height the slack is exactly 1, which is exactly what markerRows still asks
// for — so need equals the pane and the frame draws. footerRows and
// minBodyLines are functions of the footer and the rows alone and do not move
// with the pane at all; if the taller pane stops overflowing, markerRows falls
// to nothing and need only gets smaller. Same fixed-point argument
// jdeTooShortRows sets out, and the same reason there is no loop to converge.
// IT IS ASKED OF barRows, NOT footerRows, AND THAT IS WHAT PUTS THE SEARCH
// OVERLAY INSIDE THE RULE. The overlay's bar is a bar: it is drawn from the
// input line down, so on a pane that cannot hold the whole assembly clampToBox
// takes it off the bottom exactly as it takes the browse footer. Asked of
// footerRows this function answered for a bar the searching pane does not draw,
// which is why the exemption below it used to read as safe. barRows is the one
// expression that answers for the bar the pane will really draw, and every other
// budget on this screen (markerSlack, listBodyLines) already read it.
func (s *ListScreen) needRows() int {
	if len(s.rows) == 0 {
		// The overlay's empty branch is "No matches." on its own — it draws
		// neither the header row nor a footer, because the bar above it has
		// already named the keys that work.
		if s.searching {
			return s.barRows() + 1
		}
		return listHeaderRows + 2 + s.barRows()
	}
	return listHeaderRows + s.markerRows() + s.minBodyLines() + s.barRows()
}

// paneDrawn is the ONE predicate for "is this list's frame on the pane at all",
// read by bodyView (which draws the refusal when it is false) and by the
// movement gate in Update (which holds the operator's place while it is). One
// expression, so the frame and the key cannot part company — the same shape
// frameDrawn has on the columnar layer.
//
// The exempt states are the ones with NO BAR TO CUT: a loading pane is one
// muted line and an error pane is a sentence and a retry hint, neither of which
// names a movement key, so there is nothing for a short pane to take.
//
// THE SEARCH OVERLAY IS NOT ONE OF THEM, and the sentence that said it was
// claimed the overlay "pins its bar to the TOP of the pane, where clampToBox
// cannot reach it". clampToBox drops from the BOTTOM: View draws the input
// line, the folded overlay bar and a blank separator, and everything below that
// is the body — so a pane too short for the assembly loses the body first and
// then, one row further in, the bar itself. At 80x7 (one real row) the overlay
// drew its input line alone: no bar, no rows, no notice, and every key it names
// still live. That is the bar-less pane this whole refusal exists to remove,
// reached through the one state that had been excused from it on a premise
// about a direction clampToBox does not cut in.
func (s *ListScreen) paneDrawn() bool {
	if !s.paneSized() || s.loading || s.loadErr != "" {
		return true
	}
	return s.listPaneRows() >= s.needRows()
}

// listTooShortWayOut is the refusal's LEADING CLAUSE, in the words it is drawn
// in — the rule and its one exception together. A constant so the notice and the
// sweep that proves it survives every trim read one record rather than two that
// can drift.
//
// IT IS WORDED RULE-FIRST, EXCEPTION-AFTER, the order jdeTooShort states ("No
// keys are named: the action bar would be cut. … Esc still leaves."), and it
// carries BOTH in one clause because the two constraints on this row point in
// opposite directions. Split across sentences, rule-first puts the denial on the
// line that leads and the way out on the line a one-row pane drops, which is a
// refusal nobody can act on; way-out-first, which is what this said before,
// leaves a pane reading "Esc leaves" above a sentence flatly denying that any
// key is named — the notice contradicting itself about the only key on it. In
// one clause the order is rule then exception AND the way out still leads, so
// every trim of the notice either keeps the clause whole or takes it entire;
// there is no prefix of it that denies the key without naming it.
//
// The prose beneath does not repeat the denial as a count — it says why the bar
// is absent and what Esc does — so nothing below can contradict what leads.
const listTooShortWayOut = "No keys but Esc"

// listTooShort is what a list draws when the pane cannot hold its footer whole.
//
// The stance is the columnar layer's (jdeTooShort) and so is the reasoning: the
// footer is the only place an operator learns what works here, and a footer with
// rows cut off the bottom names some keys and hides the rest with nothing on the
// pane to say a fragment is what they are reading. Drawing the rows and losing
// the bar entirely — which is what this pane did — is the same trade with all of
// the bar hidden.
//
// WHATEVER MUST SURVIVE MUST LEAD, the structural rule AGENTS.md records three
// instances of, and on a REFUSAL the load-bearing clause is the WAY OUT. So
// listTooShortWayOut leads, in its own fold segment, then the height to RESIZE
// TO, then the height the operator already HAS — which their own window manager
// is showing them. Every trim, on either axis, then takes the expendable tail
// and can never leave a wrong number standing. That clause carries the rule AND
// its exception together for the reason its own comment gives, so leading with
// the way out and leading with the rule are the same wording here rather than a
// choice between them.
//
// ESC IS NAMED BECAUSE ESC WORKS HERE, and that is checked rather than assumed:
// WantsRawInput is false on a refused pane and HandlesKey never claims `esc`,
// which means the key reaches Root's global back step and leaves the screen —
// popping the back-stack, or falling home from the bottom of it. It is the ONE
// key named on a frame that names none, exactly as jdeTooShort names it, and
// for the reason that function gives: the way out of a pane too short to work
// in must stay open or the refusal is one the operator cannot act on. It is not
// a legend and it does not soften the sentence below it — esc does not act ON
// the list, it leaves the list.
//
// WHAT GIVES AND WHERE, said plainly because it is a real loss. A ONE-ROW pane
// keeps only the first folded line — that is terminal height 7, since
// screenBodyRows is height − 6 — and whether that line still carries the height
// depends on the WIDTH: at 51 cells it reads `No keys but Esc · needs 15 rows ·
// has 7…`, at 31 it keeps the figure, and at the 16 cells width 45 gives it
// folds after the clause alone and the height is gone. So the loss is confined to a
// one-row pane on a narrow terminal, and the way out survives every one of
// them; from two rows up both facts are on the pane at every drawable width.
//
// The height figure used to lead, and before that the whole thing read
// "Too short: needs 16 rows, has 12." as ONE segment, which at 16 cells folded
// on spaces into "Too short:" and scattered the figure across lines a short pane
// drops — and, bounded against a fixed 51 cells on a screen that did not record
// the terminal width, was CLIPPED at 60 columns to
// "Too short: needs 16 rows, has 1", an operator asked to act on a number that
// is not the one the code computed.
//
// Both bounds are the LIVE pane: `cells` comes from screenBodyCells, the
// unfloored width, because screenBodyWidth's floor of 20 is four cells more
// than Root really draws at width 45. The fold is pickerWrap and the mark is
// cellPrefix, both single forward passes over cells rather than runes.
//
// The height is stated in TERMINAL rows, the only unit an operator can resize:
// screenChromeRows is screenBodyRows' own inverse, so the two cannot drift.
//
// The last sentence is scoped to WHILE THIS NOTICE IS UP, and the scope is the
// whole of its truth rather than a hedge. The gate in Update holds the
// navigation vocabulary and `s` exactly when paneDrawn is false, which is
// exactly when this notice is what the pane draws — so the operator comes back
// to where they were rather than to wherever an invisible cursor wandered or a
// sort nobody could see put them.
//
// It said "until it fits" first, and that was a promise the gate does not keep:
// paneDrawn also answers TRUE while a load is out, so `r` on a refused pane
// replaces the notice with the working line and `G` then walks the cursor
// against rows nobody can see. The keys-act-invisibly-during-a-load half is
// pre-existing and belongs to every list state, not to the refusal; what was
// new was a sentence claiming otherwise. A notice denying a loss that can happen
// is the same kind of lie as one claiming a loss that cannot.
func listTooShort(cells, rows, terminalHeight, needRows int) string {
	if rows <= 0 || cells <= 0 {
		return ""
	}
	lines := pickerWrap(fmt.Sprintf("%s · needs %d rows · has %d",
		listTooShortWayOut, needRows+screenChromeRows, terminalHeight), cells)
	lines = append(lines, pickerWrap("The action bar would be cut, so it is not "+
		"drawn. Esc leaves; moving and sorting do nothing while this notice is up, "+
		"so you come back where you were.", cells)...)
	cut := len(lines) > rows
	if cut {
		lines = lines[:rows]
	}
	for i, line := range lines {
		// MARK the cut, for the reason jdeTooShort marks its own: a sentence cut
		// clean reads as a finished one, and this is the notice the operator is
		// meant to act on. The width test is separate from the height one
		// because pickerWrap floors its own budget at twelve cells, so a pane
		// narrower than that would otherwise be overrun without a mark.
		if lipgloss.Width(line) > cells || (cut && i == len(lines)-1) {
			line = cellPrefix(line, cells-1) + "…"
		}
		lines[i] = StyleMuted.Render(line)
	}
	return strings.Join(lines, "\n")
}

// listPaneCells is how wide this screen's View() may really draw, in display
// cells. pickerPaneWidth only while genuinely UNSIZED, which is the same
// standing answer the columnar layer gives for no pane: draw to the width the
// interface is modelled on and let clampToBox decide.
func (s *ListScreen) listPaneCells() int {
	if s.terminalWidth <= 0 {
		return pickerPaneWidth
	}
	return screenBodyCells(s.terminalWidth)
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
	return s.rowsFittingIn(start, s.listBodyLines())
}

// rowsFittingIn is rowsFittingFrom against a budget the caller already has.
//
// scrollIntoView walks the window start and asks this at every step, and the
// budget cannot change while it walks (it is a function of the pane and the
// rows, neither of which the walk touches) — so asking for it once is exact as
// well as cheap. Recomputing it per step made the walk quadratic in the row
// count the moment the budget stopped being O(1).
func (s *ListScreen) rowsFittingIn(start, avail int) int {
	if len(s.rows) == 0 {
		return avail
	}
	if start < 0 {
		start = 0
	}
	used, count := 0, 0
	for i := start; i < len(s.rows); i++ {
		cost := rowLineCost(s.rows[i])
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
//
// A REFUSED PANE OWNS NO KEYBOARD, because it draws no overlay for the keyboard
// to belong to. Claiming raw input there sent `esc` to updateSearch, which
// closes the overlay — and the browse shape of this list needs MORE rows than
// the searching one (footerHint folds to more lines than listSearchBarHint), so
// every height that refuses the overlay refuses the browse pane too: the key
// the notice names redrew the notice byte for byte and the operator was still
// on it. Released, `esc` reaches Root's back step and leaves, which is the one
// claim the notice makes, true in the searching state exactly as in the browse
// one. The query itself is untouched, so a terminal dragged short and back
// comes back to the search the operator was running.
func (s *ListScreen) WantsRawInput() bool { return s.searching && s.paneDrawn() }

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
	avail := s.listBodyLines()
	for s.windowStart < s.cursor && s.cursor >= s.windowStart+s.rowsFittingIn(s.windowStart, avail) {
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
		fits := s.rowsFittingIn(prev, avail)
		if prev+fits < len(s.rows) || prev+fits <= s.cursor {
			break
		}
		s.windowStart = prev
	}
	s.windowSize = s.rowsFittingIn(s.windowStart, avail)
}

func (s *ListScreen) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		s.terminalHeight = m.Height
		// The WIDTH is kept for the same reason the height is: something on this
		// screen has to be bounded against the pane the terminal really gives,
		// and a bound computed against a width the terminal may not have is not
		// a bound. listTooShort is the one that cannot be got wrong — it is the
		// only surface here asking the operator to act on a NUMBER.
		s.terminalWidth = m.Width
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
		// THE MOVEMENT KEYS DECLINE WHERE THE FOOTER STOPS NAMING THEM, and the
		// gate is here — one place, over the whole vocabulary — rather than a
		// count condition repeated on six switch arms, which is how a condition
		// comes to be applied to five of them.
		//
		// Silently, because a movement arm's whole product IS the position: with
		// nowhere to move there is nothing left to report, and the footer has
		// already stopped claiming otherwise (listNavHint). It is not the silence
		// rule 1 forbids — the footer CHANGED when the rows went away, so the
		// pane is not a byte-identical answer to the keypress; it is the same
		// answer a list EDGE gives, where the highlight is visibly at the end.
		//
		// It also closes a state nothing could see: on an EMPTY list `pgdown`
		// ran `s.cursor = len(s.rows) - 1` and left the cursor at -1, which no
		// row index can be and which the next loaded page would have inherited.
		//
		// AND WHERE THE FRAME IS NOT DRAWN AT ALL. A movement arm's whole
		// product is the position, so on a pane refused as too short there is
		// nothing it could report and everything it could destroy: `end` would
		// walk the cursor to the bottom of a list nobody can see, and the
		// operator who drags the terminal back finds somewhere they never went.
		// paneDrawn is the frame's own predicate, so the notice's promise that
		// moving keys are held is the same expression that holds them.
		//
		// THE HELD SET ON A REFUSED PANE IS {the navigation vocabulary, `s`},
		// and it is that short because it is DERIVED from what each key's whole
		// product is rather than from "a refused pane is risky". A movement
		// key's product is the position; `s` re-orders locally and its only
		// visible product is headerLine, which the refusal does not draw, and
		// needRows is invariant under re-ordering — so the pane comes back byte
		// for byte, which is standing rule 1 broken by the refusal itself.
		// Nothing else qualifies: `r` and `f` set loading and redraw as
		// "Loading…", and enter/n/the uppercase shortcuts leave the screen
		// altogether. Do NOT widen this to every key — one of them navigating
		// away is the operator's way OUT of a pane too short to work in, and a
		// refusal nobody can act on is its own defect.
		if !s.paneDrawn() && (listNavBinds(m.String()) || m.String() == "s") {
			return s, nil
		}
		if listNavBinds(m.String()) && !listNavMoves(len(s.rows)) {
			return s, nil
		}
		switch m.String() {
		case "j", "down":
			if s.cursor < len(s.rows)-1 {
				s.cursor++
				s.scrollIntoView()
			}
			return s, nil
		case "k", "up":
			if s.cursor > 0 {
				s.cursor--
				s.scrollIntoView()
			}
			return s, nil
		case "pgdown":
			s.cursor += s.windowSize
			if s.cursor >= len(s.rows) {
				s.cursor = len(s.rows) - 1
			}
			s.scrollIntoView()
			return s, nil
		case "pgup":
			s.cursor -= s.windowSize
			if s.cursor < 0 {
				s.cursor = 0
			}
			s.scrollIntoView()
			return s, nil
		case "g", "home":
			s.cursor = 0
			s.scrollIntoView()
			return s, nil
		case "G", "end":
			s.cursor = len(s.rows) - 1
			if s.cursor < 0 {
				s.cursor = 0
			}
			s.scrollIntoView()
			return s, nil
		case "s":
			s.sort = (s.sort + 1) % 4
			s.applySort()
			return s, nil
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
		// The uppercase sibling-surface accelerators. Last, and reached only by
		// a key the switch above did NOT handle — every arm of it returns, so
		// the precedence this comment claims is the control flow rather than a
		// property of which letters the table happens to hold today. A shortcut
		// added on a letter the list already binds (G was, before categories
		// was re-keyed to C) would otherwise scroll the list AND navigate away
		// on one press. Checked from the SAME table the footer prints, which is
		// what stops the two from drifting apart again.
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
	// REFUSED RATHER THAN MUTILATED, and the check is HERE rather than in
	// bodyView because the overlay is drawn by this function: left one level
	// down, a refused searching pane would draw the notice UNDERNEATH the very
	// input line and bar the pane cannot hold, which is the mutilation with an
	// explanation stapled to it. The notice replaces the whole frame, so what
	// the operator sees is the height they need and the key that leaves.
	if !s.paneDrawn() {
		return listTooShort(s.listPaneCells(), s.listPaneRows(), s.terminalHeight, s.needRows())
	}
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
		head.WriteString(pickerHintAt(s.searchBarHint(), s.listPaneCells()) + "\n\n")
		return head.String() + s.bodyView()
	}
	return s.bodyView()
}

// headerLine is the list's standing status row: how it is sorted, which view it
// is showing, and how many rows that comes to.
//
// A method, and drawn by the EMPTY branch as well as the loaded one, because it
// is the only place `s sort` has a visible effect: a re-order of nothing looks
// exactly like the nothing it started from.
func (s *ListScreen) headerLine() string {
	if s.hasFilters() {
		return fmt.Sprintf("Sort: %s · Filter: %s · %d rows",
			s.sort.label(), s.activeFilter().label, len(s.rows))
	}
	return fmt.Sprintf("Sort: %s · %d rows", s.sort.label(), len(s.rows))
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
			// The overlay above has already drawn its own bar (searchBarHint) and
			// owns the keyboard, so the browse footer stays off for the reason the
			// rows>0 branch gives below: two contradictory bars on one pane.
			return StyleMuted.Render("No matches.")
		}
		// AN EMPTY LIST IS A STATE, NOT AN ABSENCE OF ONE, and it used to be the
		// one state on this screen that drew NO BAR AT ALL — an early return with
		// "No rows." and nothing else, while `s`, `r`, `n`, `f`, `/` and every
		// sibling-surface letter all worked. The bar's contract is that every key
		// that works is on it; here it named none of them, on the state where
		// "there is nothing here, now what?" is the operator's actual question and
		// the answer is a key. That is a dead end, which is its own defect
		// (standing rule 11) on top of the omission.
		//
		// The footer is the SAME method the loaded list draws, so what it names
		// here is what is true here: footerHint already drops the movement
		// segments below two rows and `enter open` below one, so an empty list's
		// bar is exactly the keys that act on an empty list.
		var b strings.Builder
		// THE HEADER IS DRAWN HERE TOO, and that is what makes `s sort` honest
		// rather than merely named: sorting is a local re-order whose ONLY
		// visible product is this line, so on an empty list the key changed
		// s.sort and redrew a byte-identical pane — standing rule 1, and drawing
		// the footer without this would have moved the violation from "acts and
		// is not named" to "is named and cannot be seen to act" rather than
		// removing it. It also puts the row count where every other count on
		// this screen is stated, and listBodyLines has always reserved a row for
		// it (listHeaderRows), so nothing is spent that was not already budgeted.
		b.WriteString(StyleMuted.Render(s.headerLine()) + "\n\n")
		// An empty FILTERED view is real information ("there are no drafts"), not
		// an empty resource — name the view so it cannot be misread. It used to
		// point at the key out of it as well; that clause is gone because the
		// footer under it now names `f filter`, and ONE surface names a key.
		if f := s.activeFilter(); f.query != nil {
			b.WriteString(StyleMuted.Render(fmt.Sprintf("No rows in the %q view.", f.label)))
		} else {
			b.WriteString(StyleMuted.Render("No rows."))
		}
		b.WriteString("\n\n")
		b.WriteString(pickerHintAt(s.footerHint(), s.listPaneCells()))
		return b.String()
	}

	var b strings.Builder
	b.WriteString(StyleMuted.Render(s.headerLine()) + "\n")

	end := s.windowStart + s.windowSize
	if end > len(s.rows) {
		end = len(s.rows)
	}
	// The markers are drawn in their places where the pane reserved a row for
	// each, and SHARED on one row where it could only reserve one — markerRows
	// is the single expression the budget, the refusal and this both read, so a
	// pane can never draw a marker row it did not pay for. The shared row rides
	// above the list because that is where a reader meets it before the rows it
	// is about, and because the ↓ half carries its own arrow.
	shared := s.markerRows() == 1
	above, below := s.windowStart > 0, len(s.rows)-end
	if shared {
		if line := listMarkerLine(above, below, s.listPaneCells()); line != "" {
			b.WriteString(StyleMuted.Render(line) + "\n")
		}
	} else if above {
		b.WriteString(StyleMuted.Render(listMarkerLine(true, 0, s.listPaneCells())) + "\n")
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

	if !shared && below > 0 {
		b.WriteString(StyleMuted.Render(listMarkerLine(false, below, s.listPaneCells())) + "\n")
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
	b.WriteString(pickerHintAt(s.footerHint(), s.listPaneCells()))
	return b.String()
}

// listSearchBarHint is the search overlay's action bar AT ITS TALLEST — every
// key it can name, whatever the result count is. A named constant so the
// renderer and listBodyLines's row reservation read the same string — a bar
// whose rows are budgeted from a different literal is a bar that gets cut.
//
// The BUDGET measures against this ceiling and the RENDER draws searchBarHint,
// which is the same trade the columnar layer makes for every bar that can lose a
// key (jdePickBarCeiling): the live bar shrinks and grows with every keystroke
// as the result count changes, and a body budget that moved with it would make
// the list jump under the operator's hands while they type. The ceiling is a
// fixed point; the live bar is what the operator reads.
const listSearchBarHint = "↑/↓ move · enter open · esc cancel"

// searchBarHint is the overlay's bar for the results actually on the pane.
//
// A SEARCH THAT MATCHED NOTHING IS THE STATE THIS BAR SPENDS ITS LIFE IN — it is
// what the operator sees for every prefix of every query that has not landed
// yet — and the constant above named `↑/↓ move` and `enter open` in it, over no
// rows at all: both arms decline in silence (the cursor guards are `> 0` and
// `< len-1`, and openSelected returns the screen it was handed on an
// out-of-range cursor), so the pane came back byte for byte.
//
// `enter open` needs ONE row where the movement pair needs TWO, the same two
// thresholds footerHint keeps apart, and for the same reason: opening the row
// you are on is not moving to another one.
func (s *ListScreen) searchBarHint() string {
	var parts []string
	if listNavMoves(len(s.rows)) {
		parts = append(parts, "↑/↓ move")
	}
	if s.spec.detail != nil && len(s.rows) > 0 {
		parts = append(parts, "enter open")
	}
	return strings.Join(append(parts, "esc cancel"), " · ")
}

// footerRows is how many rows the folded footer occupies, plus its blank
// separator. Derived from the hint that will actually be drawn rather than
// assumed: the hint grows a segment whenever a list gains a sibling surface,
// and a constant here silently spends the extra row out of the pane's bottom.
// MEASURED AT THE PANE THE TERMINAL REALLY GAVE, not at pickerPaneWidth. A fold
// is normally safe at the fixed 51 — an extra line costs a row and loses no
// words — but that is only true while the pane HAS 51 cells. Root draws from a
// terminal width of 45, where the pane is 16, and a bar folded at 51 then runs
// past it and clampToBox takes the tail: the same claim off the same edge the
// horizontal fold exists to prevent. Both bars on this screen do it, so both
// read listPaneCells; at 80 columns the two numbers are the same 51 and nothing
// moves. It is a function of the footer and the pane's WIDTH, so it still does
// not move with the HEIGHT, which is what needRows' fixed point rests on.
func (s *ListScreen) footerRows() int {
	return 1 + len(pickerWrap(s.footerHint(), s.listPaneCells()))
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
	// The movement segments come from the app's ONE navigation vocabulary
	// (list_nav.go) rather than from a literal here, and they are CONDITIONAL:
	// every one of them needs a second row to be true, and this footer used to
	// promise all three over an empty list. listNavHint carries why an empty list
	// is a state rather than an exception.
	var hint string
	if nav := listNavHint(len(s.rows)); nav != "" {
		hint = nav + " · "
	}
	hint += "s sort"
	if s.hasFilters() {
		hint += " · f filter"
	}
	hint += " · r refresh"
	// `enter open` needs a ROW to open, which is a different threshold from the
	// movement segments above (they need a SECOND row). openSelected already
	// declines on an out-of-range cursor and does it silently, so on an empty
	// list this segment named a key that returned the pane it was pressed on —
	// found by sweeping the footer at every row count rather than at the eight
	// every fixture used to carry.
	if s.spec.detail != nil && len(s.rows) > 0 {
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
// cancelled/voided are deliberately absent, matching the web's option set.
//
// The list endpoint hides an order only when ALL THREE hold: it HAS line items,
// none of them survives unvoided, and it is OUTSIDE PRE_SUPPLIER_STATUSES
// (PurchaseOrderViewSet.get_queryset, oms-a8o). So the `draft` filter below
// finds a draft whose last line was deleted, and one whose lines are all voided
// ghosts: an order still being built is always listed, whatever it holds. What
// the filter still hides is an order EMPTIED BY VOIDING AFTER IT LEFT THE SHOP,
// which no filter here can reach and which ctrl+k search can — AGENTS.md's
// line-removal note carries that fact and why the VOID prompt, not the delete
// confirm, is where the terminal warns about it.
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
