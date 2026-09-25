// Item↔supplier link management — the "manage suppliers" surface for one
// inventory item.
//
// The item detail screen already DISPLAYS an item's suppliers (Primary supplier
// + All suppliers). This file adds the EDIT path the web only stubs
// (SupplierRelationshipForm is wired into InventoryItemFormPage but its save is a
// TODO): a working add / edit / delete / set-primary management view backed by
// the fully-CRUD ItemSupplierViewSet.
//
// ItemSuppliersScreen (list) is reached with `s` from the item detail. It stays
// non-raw except during the delete confirm, and claims esc (back to the item
// detail, not the global Welcome) + G (scroll-to-bottom) via LocalKeyScreen. Its
// action keys (c/E/x/p/r) are chosen to dodge the global nav hotkeys, matching
// SupplierListScreen.
//
// ItemSupplierFormScreen (create/edit) mirrors the web SupplierRelationshipForm's
// field set — supplier, supplier_sku, supplier_url, unit_cost, package_cost,
// quantity_per_package, average_lead_time, is_primary — with the supplier chosen
// from a searchable sub-picker (the inventory_item_form pattern). It's a raw-input
// form, so every key reaches it.
package tui

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/uid0/scantty/internal/omsapi"
)

// ===========================================================================
// ItemSuppliersScreen — list / manage an item's supplier links
// ===========================================================================

type ItemSuppliersScreen struct {
	deps     Deps
	itemID   string
	itemName string

	rows           []omsapi.ItemSupplier
	cursor         int
	windowStart    int
	windowSize     int
	loading        bool
	loadErr        string
	terminalHeight int
	terminalWidth  int

	confirmingDelete bool
	deleting         bool
	busy             bool // a set-primary PATCH is in flight

	// stale is the server's refusal of a set-primary or a delete made from rows
	// this list loaded before somebody else wrote them, held until a load
	// lands. The rows on the pane ARE that stale copy, so the answer is a
	// reload the operator asks for — never the write sent again, and above all
	// never re-sent at the version the refusal reports (omsapi's
	// item_supplier_version.go says why).
	stale *omsapi.StaleSupplierLink
}

type itemSuppliersLoadedMsg struct {
	rows []omsapi.ItemSupplier
	err  error
}

type itemSupplierDeletedMsg struct {
	err error
}

type itemSupplierPrimaryMsg struct {
	err error
}

// NewItemSuppliersScreen lists every supplier link for one inventory item so an
// operator can add / edit / remove / set-primary against the item in front of
// them.
func NewItemSuppliersScreen(deps Deps, itemID, itemName string) *ItemSuppliersScreen {
	return &ItemSuppliersScreen{
		deps:       deps,
		itemID:     itemID,
		itemName:   itemName,
		loading:    true,
		windowSize: 20,
	}
}

func (s *ItemSuppliersScreen) Title() string {
	if s.itemName != "" {
		return "Suppliers: " + s.itemName
	}
	return "Item suppliers"
}

// WantsRawInput claims every key only during the delete confirm, so y/n/esc land
// here instead of the root's global hotkeys.
func (s *ItemSuppliersScreen) WantsRawInput() bool { return s.confirmingDelete }

// HandlesKey claims esc (back to the owning item detail rather than the global
// Welcome) and G (scroll-to-bottom, otherwise the global Categories hotkey). The
// other action keys dodge the globals and reach Update as the fallback.
func (s *ItemSuppliersScreen) HandlesKey(key string) bool {
	return key == "esc" || key == "G"
}

func (s *ItemSuppliersScreen) Init() tea.Cmd { return s.load() }

func (s *ItemSuppliersScreen) ctx() context.Context {
	if s.deps.Ctx != nil {
		return s.deps.Ctx
	}
	return context.Background()
}

func (s *ItemSuppliersScreen) load() tea.Cmd {
	deps := s.deps
	ctx := s.ctx()
	id := s.itemID
	return func() tea.Msg {
		rows, err := deps.OMS.ListItemSuppliersForItem(ctx, id)
		return itemSuppliersLoadedMsg{rows: rows, err: err}
	}
}

// suppliersPaneCells is how wide this screen's View() may really draw, in
// display cells. pickerPaneWidth only while genuinely UNSIZED — the standing
// answer the rest of the program gives for no pane: draw to the width the
// interface is modelled on and let clampToBox decide.
//
// The LIVE pane and not a fixed 51, because a bound computed against a width
// the terminal may not have is not a bound: folded against pickerPaneWidth a
// 120-column terminal would draw every supplier row folded with forty columns
// of pane left blank, and a 60-column one would fold past its own edge and hand
// clampToBox the tail to cut — which is the unmarked truncation the fold exists
// to remove.
func (s *ItemSuppliersScreen) suppliersPaneCells() int {
	if s.terminalWidth <= 0 {
		return pickerPaneWidth
	}
	return screenBodyCells(s.terminalWidth)
}

// suppliersBar is the list's action bar as a record.
//
// THE LITERAL IT REPLACED NAMED `j/k move` AND NOTHING ELSE OF THE VOCABULARY,
// while this screen's switch binds all of it — the arrows, pgup/pgdn and
// g/G/home/end — and `enter` opens the same edit form `E` does. Those were keys
// that acted with no word for them, which is the defect the record exists for.
//
// `primary` is whether `p` would WRITE: on the row that is already the primary
// supplier the arm answers a warning toast and changes nothing, so naming it
// there would be a bar claiming a key that only declines. The movement segments
// come off where there is no second row (listNavMoves), and every row action
// comes off an empty list, where the arms find no selected row and do nothing.
func (s *ItemSuppliersScreen) suppliersBar(moves, primary bool) proseBar {
	out := proseNavCursor(moves)
	out = append(out, proseBarItem{Keys: []string{"c"}, Hint: "c add"})
	if len(s.rows) > 0 {
		out = append(out, proseBarItem{Keys: []string{"E", "enter"}, Hint: "E/enter edit"})
		if primary {
			out = append(out, proseBarItem{Keys: []string{"p"}, Hint: "p primary"})
		}
		out = append(out, proseBarItem{Keys: []string{"x"}, Hint: "x remove"})
	}
	return append(out, proseBarRefresh, proseBarEsc)
}

// suppliersCeiling is the bar at its TALLEST, which the row budget is measured
// against for the reason proseSizeScroller gives: `p` comes off on the primary
// row and the movement segments off a one-row list, so a budget taken from the
// live bar would change as the cursor moves — and the budget decides which rows
// the cursor can see.
func (s *ItemSuppliersScreen) suppliersCeiling() proseBar { return s.suppliersBar(true, true) }

// proseBar is the bar this screen is DRAWING. Nil where the frame is something
// else: the one-line delete confirm that names its own two keys, and a
// set-primary PATCH while it is out — every key is held then (Update returns
// before its switch), so a bar would name keys that do nothing. A load in flight
// or failed draws loadBar's.
func (s *ItemSuppliersScreen) proseBar() proseBar {
	if s.loading || s.loadErr != "" {
		return s.loadBar()
	}
	if s.confirmingDelete || s.busy {
		return nil
	}
	row, ok := s.selected()
	return s.suppliersBar(listNavMoves(len(s.rows)), ok && !row.IsPreferred)
}

// loadBar is the link list's bar while its load is out or has failed — what its
// key switch still answers with no rows drawn (prose_bar.go carries the defect
// and the decision). `c` opens the add form whatever the list holds. On the
// link a refresh kept under the cursor, which the frame no longer draws,
// `E`/`enter` still opens the edit form and `p` still WRITES the primary
// supplier — named because they act, and candidates for gating. `x` is not
// named: all it does here is arm a confirm the frame does not draw. `esc` is
// this screen's own arm, back to the item.
func (s *ItemSuppliersScreen) loadBar() proseBar {
	out := proseBar{{Keys: []string{"c"}, Hint: "c add"}}
	if row, ok := s.selected(); ok {
		out = append(out, proseBarItem{Keys: []string{"E", "enter"}, Hint: "E/enter edit"})
		if !row.IsPreferred {
			out = append(out, proseBarItem{Keys: []string{"p"}, Hint: "p primary"})
		}
	}
	return append(out, proseBarReloadFor(s.loadErr != ""), proseBarEsc)
}

// suppliersFooterLines folds the CEILING bar to the live pane — the rows the
// plan reserves for whatever is drawn at the foot of the frame.
//
// The bar is 63 cells and more against the 51 an 80-column pane gives, so
// written straight out it lost its tail to clampToBox — keys that work, unnamed,
// on the only surface that names them. A bar the operator cannot read is not
// honest, it is absent.
func (s *ItemSuppliersScreen) suppliersFooterLines() []string {
	return pickerWrap(s.suppliersCeiling().hint(), s.suppliersPaneCells())
}

// bodyLines is how many lines the row window may spend.
//
// Folding the bar SPENDS ROWS, so the budget moves with it: the count comes
// from the bar that is about to be DRAWN rather than from a constant, or the
// fold that saved the bar horizontally pushes it off the bottom instead — the
// same claim lost at the other edge.
//
// screenBodyRows and not screenBodyHeight, because the latter floors at four
// and is therefore a LIE below a terminal height of ten (layout.go says so),
// and a budget that claims rows the pane does not have hands clampToBox the
// bar to cut.
func (s *ItemSuppliersScreen) bodyLines() int {
	return s.bodyPlan().body
}

// suppliersPlan is how one frame's rows are spent, decided once and read by
// everything that draws.
type suppliersPlan struct {
	body  int  // lines the row window gets
	count bool // draw the "N supplier link(s)" line
	gap   bool // draw the blank line above the bar
}

// bodyPlan gives ground BY RANK, so what a short pane loses is decided here
// rather than by where clampToBox happens to cut.
//
// The ACTION BAR never gives: it is the only surface naming the keys that work,
// and a bar with rows cut off it names some keys and hides the rest silently,
// which is worse than naming none. Then the decorative blank goes, then the
// count line (context — how many links there are is worth knowing and is not
// worth the row an actual link needs), and the body floors at one line.
//
// screenBodyRows and not screenBodyHeight: the latter floors at four and is a
// LIE below a terminal height of ten (layout.go says so), and a plan built on a
// lie claims rows the pane does not have.
func (s *ItemSuppliersScreen) bodyPlan() suppliersPlan {
	// The working line of a set-primary PATCH is drawn IN PLACE of the bar
	// rather than above it, so it spends rows the ceiling already reserved and a
	// write going out does not move the window under the operator.
	avail := screenBodyRows(s.terminalHeight) - len(s.suppliersFooterLines())
	plan := suppliersPlan{count: true, gap: true}
	// One line for the count, one for the gap, and at least one for the body.
	switch {
	case avail >= 3:
		plan.body = avail - 2
	case avail == 2:
		plan.gap = false
		plan.body = 1
	default:
		// Too short for the bar plus a line of rows. Everything that can give
		// has given; the body keeps its floor of one and the frame runs over,
		// which is the state clampToBox is for.
		plan.gap, plan.count = false, false
		plan.body = 1
	}
	return plan
}

// rowsFittingFrom returns how many rows starting at `start` fit the body,
// counting the LINES each one really renders rather than the rows.
//
// A supplier row is not one line: it is a name, then the folded fact lines
// under it, then a clipped URL, so how many rows fit DEPENDS ON WHICH ROWS —
// three links with long barcodes fill a pane that would hold eight bare ones.
// The previous budget counted ROWS against a flat chrome of four and so put
// twenty lines into an eighteen-line pane the moment the rows grew: at 80x24
// with eight links the action bar was already gone before a barcode was added
// to the row, and the marker saying more rows were below went with it.
//
// This is the rule ListScreen.rowsFittingFrom already states; the answer is
// re-derived on every move rather than taken once at load, because a size
// measured over the short rows at the top of a list is wrong for the tall ones
// further down.
func (s *ItemSuppliersScreen) rowsFittingFrom(start int) int {
	budget := s.bodyLines()
	// A marker row is one line, and it is reserved where it will be drawn.
	if start > 0 {
		budget--
	}
	used, n := 0, 0
	for i := start; i < len(s.rows); i++ {
		lines := len(strings.Split(s.renderRow(i), "\n"))
		// The LAST row that fits may still need a "more below" marker under it.
		remaining := budget - used - lines
		if i < len(s.rows)-1 && remaining < 1 {
			remaining = -1
		}
		if used+lines > budget || remaining < 0 {
			break
		}
		used += lines
		n++
	}
	// Never hand back an empty window: a pane too short for even one row still
	// draws the row it is on, and the overflow is that row's own extra lines.
	if n == 0 {
		n = 1
	}
	return n
}

func (s *ItemSuppliersScreen) computeWindowSize() int {
	return s.rowsFittingFrom(s.windowStart)
}

func (s *ItemSuppliersScreen) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		s.terminalHeight = m.Height
		s.terminalWidth = m.Width
		s.windowSize = s.computeWindowSize()
		s.scrollIntoView()
		return s, nil
	case itemSuppliersLoadedMsg:
		s.loading = false
		if m.err != nil {
			s.loadErr = m.err.Error()
		} else {
			s.loadErr = ""
			s.rows = m.rows
			// A landed load IS the reload a refusal asked for, whichever key
			// fired it: the rows now carry the versions the server holds.
			s.stale = nil
		}
		if s.cursor >= len(s.rows) {
			s.cursor = 0
		}
		s.windowSize = s.computeWindowSize()
		s.scrollIntoView()
		return s, nil
	case itemSupplierDeletedMsg:
		s.deleting = false
		s.confirmingDelete = false
		if refusal, ok := omsapi.AsStaleSupplierLink(m.err); ok {
			s.stale = refusal
			return s, Status(refusal.Message, StatusError)
		}
		if m.err != nil {
			return s, Status("delete failed: "+m.err.Error(), StatusError)
		}
		s.loading = true
		return s, tea.Batch(Status("supplier link removed", StatusOK), s.load())
	case itemSupplierPrimaryMsg:
		s.busy = false
		if refusal, ok := omsapi.AsStaleSupplierLink(m.err); ok {
			s.stale = refusal
			return s, Status(refusal.Message, StatusError)
		}
		if m.err != nil {
			return s, Status("set primary failed: "+m.err.Error(), StatusError)
		}
		s.loading = true
		return s, tea.Batch(Status("primary supplier updated", StatusOK), s.load())
	case tea.KeyMsg:
		if proseLoadKeyHidden(s.loading, s.loadErr, s.loadBar(), m.String()) {
			return s, nil
		}
		if s.confirmingDelete {
			return s.updateConfirmDelete(m)
		}
		if s.busy {
			return s, nil
		}
		switch m.String() {
		case "esc":
			return s, SwitchTo(WSInventory, NewInventoryDetailScreen(s.deps, s.itemID))
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
		case "r":
			s.loading = true
			s.loadErr = ""
			return s, s.load()
		case "c":
			return s, SwitchTo(WSInventory, NewItemSupplierFormScreen(s.deps, s.itemID, s.itemName, nil))
		case "E", "enter":
			if row, ok := s.selected(); ok {
				return s, SwitchTo(WSInventory, NewItemSupplierFormScreen(s.deps, s.itemID, s.itemName, &row))
			}
		case "p":
			return s.setPrimary()
		case "x":
			if _, ok := s.selected(); ok {
				s.confirmingDelete = true
			}
		}
	}
	return s, nil
}

func (s *ItemSuppliersScreen) setPrimary() (Screen, tea.Cmd) {
	row, ok := s.selected()
	if !ok {
		return s, nil
	}
	if row.IsPreferred {
		return s, Status("already the primary supplier", StatusWarn)
	}
	s.busy = true
	deps := s.deps
	ctx := s.ctx()
	id, version := row.ID, row.Version
	return s, func() tea.Msg {
		_, err := deps.OMS.SetItemSupplierPrimary(ctx, id, version)
		return itemSupplierPrimaryMsg{err: err}
	}
}

func (s *ItemSuppliersScreen) updateConfirmDelete(m tea.KeyMsg) (Screen, tea.Cmd) {
	if s.deleting {
		return s, nil
	}
	switch m.String() {
	case "y", "Y":
		row, ok := s.selected()
		if !ok {
			s.confirmingDelete = false
			return s, nil
		}
		s.deleting = true
		deps := s.deps
		ctx := s.ctx()
		id, version := row.ID, row.Version
		return s, func() tea.Msg {
			return itemSupplierDeletedMsg{err: deps.OMS.DeleteItemSupplier(ctx, id, version)}
		}
	case "n", "N", "esc":
		s.confirmingDelete = false
	}
	return s, nil
}

func (s *ItemSuppliersScreen) selected() (omsapi.ItemSupplier, bool) {
	if s.cursor < 0 || s.cursor >= len(s.rows) {
		return omsapi.ItemSupplier{}, false
	}
	return s.rows[s.cursor], true
}

// scrollIntoView chooses the window START and its SIZE together, every time.
//
// They cannot be chosen separately: how many rows fit depends on which row the
// window starts at, so a size carried over from the previous start is a size
// for a different window. Walking the start BACK one row at a time and
// re-asking is what keeps the cursor's row whole on the pane rather than
// half-drawn at the bottom of it.
func (s *ItemSuppliersScreen) scrollIntoView() {
	if len(s.rows) == 0 {
		s.windowStart, s.windowSize = 0, 0
		return
	}
	if s.windowStart > len(s.rows)-1 {
		s.windowStart = len(s.rows) - 1
	}
	if s.cursor < s.windowStart {
		s.windowStart = s.cursor
	}
	if s.windowStart < 0 {
		s.windowStart = 0
	}
	// Push the start down until the cursor's row is inside the window.
	for s.windowStart < s.cursor && s.cursor >= s.windowStart+s.rowsFittingFrom(s.windowStart) {
		s.windowStart++
	}
	// Then pull it back up while the whole list still fits from higher up, so
	// a short list is never scrolled and the top is preferred.
	for s.windowStart > 0 {
		prev := s.windowStart - 1
		if prev+s.rowsFittingFrom(prev) <= s.cursor {
			break
		}
		s.windowStart = prev
	}
	s.windowSize = s.rowsFittingFrom(s.windowStart)
}

func (s *ItemSuppliersScreen) View() string {
	if s.loading {
		return proseLoadingFrame("Loading suppliers…", s.suppliersPaneCells(), s.proseBar())
	}
	if s.loadErr != "" {
		return proseFailedFrame(s.loadErr, s.terminalHeight, s.suppliersPaneCells(), s.proseBar())
	}
	if s.confirmingDelete {
		name := ""
		if row, ok := s.selected(); ok {
			name = itemSupplierName(row)
		}
		var prompt string
		if s.deleting {
			prompt = StyleMuted.Render("Removing…")
		} else {
			prompt = StyleStatusWarn.Render(fmt.Sprintf("Remove supplier link %q? This can't be undone.  y remove · n/esc cancel", name))
		}
		return prompt
	}
	if len(s.rows) == 0 {
		return StyleMuted.Render("No suppliers linked to this item yet.") + "\n\n" +
			s.proseBar().render(s.suppliersPaneCells())
	}

	room := s.suppliersPaneCells()
	muted := func(text string) string { return StyleMuted.Render(pickerClip(text, room)) }

	plan := s.bodyPlan()

	var b strings.Builder
	if plan.count {
		if s.stale != nil {
			b.WriteString(StyleStatusError.Render(pickerClip(suppliersStaleNote, room)) + "\n")
		} else {
			b.WriteString(muted(fmt.Sprintf("%d supplier link(s)", len(s.rows))) + "\n")
		}
	}

	// The body is assembled as LINES and then bounded as a whole, because a row
	// is not a line: the markers and the folded fact lines under each name all
	// come out of the same budget, and counting rows against it is what put
	// twenty lines into an eighteen-line pane.
	var body []string
	if s.windowStart > 0 {
		body = append(body, muted("  ↑ more above"))
	}
	end := s.windowStart + s.windowSize
	if end > len(s.rows) {
		end = len(s.rows)
	}
	for i := s.windowStart; i < end; i++ {
		body = append(body, strings.Split(s.renderRow(i), "\n")...)
	}
	if end < len(s.rows) {
		body = append(body, muted(fmt.Sprintf("  ↓ %d more below", len(s.rows)-end)))
	}
	// A pane too short to hold even the row the cursor is on keeps the START of
	// that row — its name and its leading facts — and MARKS the cut.
	//
	// The mark is a LINE OF ITS OWN rather than an ellipsis appended to the last
	// line kept, which is what foldKeepRows does: these lines are already
	// STYLED, and cutting a styled string mid-sequence takes its closing SGR
	// reset with it and colours everything drawn afterwards. It spends the LAST
	// of the rows the body already had, so marking a cut cannot itself push the
	// action bar off the bottom — which would be the cut this exists to report,
	// committed by the report.
	if budget := plan.body; len(body) > budget {
		keep := budget - 1
		if keep < 0 {
			keep = 0
		}
		body = append(body[:keep:keep], muted(suppliersRowCutMark))
	}
	for _, line := range body {
		b.WriteString(line + "\n")
	}

	if plan.gap {
		b.WriteString("\n")
	}
	if s.busy {
		b.WriteString(muted("Working…"))
	} else {
		b.WriteString(s.proseBar().render(room))
	}
	return strings.TrimRight(b.String(), "\n")
}

// suppliersStaleNote stands on the pane after the server refused a set-primary
// or a delete made from rows it had written since this list loaded them.
//
// The server's own sentence goes to the bottom line, whole or marked where it is
// cut (StatusBar.View), and it is the complete account. That line is a FLASH,
// though, and an operator who looked away is still looking at a list that is
// out of date — so the pane keeps the fact for as long as it is true, in the
// count line's slot: that line is context this frame can spare, and the fact
// replacing it is the one the rows beneath cannot be read without.
//
// Worded for the one row it gets, so what must survive LEADS: "nothing saved"
// first, then why, then the key — which the bar also names, as `r refresh`.
// 50 cells, inside the 51 the narrowest pane gives.
const suppliersStaleNote = "✗ nothing saved: list out of date · r refreshes it"

// indent is the gutter every fact line under a supplier's name sits in. Named
// because it is spent twice — once as the prefix that is written, once as the
// budget the fold is given — and a fold measured against a different number
// from the one it is drawn behind is off the pane by the difference.
const indent = "    "

// suppliersNameFloor is how few cells a vendor name may be abbreviated to
// before the badges beside it are dropped instead.
//
// Eight, because an MRO vendor name is distinguished by its head ("McMaster-…",
// "Grainger…", "Fastenal…") and below about that the row stops naming anybody.
const suppliersNameFloor = 8

// suppliersRowCutMark says a supplier row was cut short by the pane, and names
// the remedy.
//
// It names the REMEDY and not just the loss: a warning an operator cannot act
// on is a dead end, and the only thing that brings these lines back is a taller
// terminal. The remedy LEADS, so every prefix a narrow pane leaves still
// carries the way out.
const suppliersRowCutMark = "  … a taller terminal shows the rest of this link"

func (s *ItemSuppliersScreen) renderRow(i int) string {
	sup := s.rows[i]
	room := s.suppliersPaneCells()
	marker := "  "
	if i == s.cursor {
		marker = "▸ "
	}

	// The badges are the row's STATE, and they are assembled before the name is
	// clipped so the name is measured against what they really leave.
	badge := ""
	if sup.IsPreferred {
		badge += " " + StyleStatusOK.Render("★ primary")
	}
	if sup.IsDiscontinued {
		badge += " " + StyleMuted.Render("[discontinued]")
	} else if !sup.IsActive {
		badge += " " + StyleMuted.Render("[inactive]")
	}

	// The identity row, BOUNDED AS ASSEMBLED rather than part by part.
	//
	// It was written straight to the pane, so a full-length OMS vendor name ran
	// past the edge and clampToBox took the tail with no ellipsis — an operator
	// reading a cut name cannot tell it is cut, and on the row that says WHICH
	// vendor this link is that is the worst place in the screen for it. At the
	// 45-column floor Root drew at when this was written, "McMaster-Carr Supply
	// Company" assembled to 34 cells into a pane of 16; the size contract has put
	// the narrowest pane at 51, and the bound stays because the name is
	// unbounded, not because the pane was.
	//
	// The NAME keeps the room and the BADGES give, which is the give-order the
	// purchasing rows already use: a badge beside a name cut to a character
	// distinguishes nothing, and the name is what the operator is looking for.
	// Where a badge is dropped the row says so with poRowDropMark rather than
	// simply ending — a row that quietly lost "[discontinued]" is a row
	// claiming the link is live.
	//
	// The highlight's padding is reserved on EVERY row and not just the
	// highlighted one, or a row that fits until it is selected is cut on
	// exactly the keypress that selects it.
	pad := StyleSidebarItemActive.GetHorizontalPadding()
	nameRoom := room - lipgloss.Width(marker) - pad
	withBadge := nameRoom - lipgloss.Width(badge)
	name := itemSupplierName(sup)
	if withBadge >= suppliersNameFloor || lipgloss.Width(name) <= withBadge {
		name = pickerClip(name, withBadge)
	} else {
		// No room for both: the name takes what is left and the badge goes,
		// marked.
		badge = poRowDropMark
		name = pickerClip(name, nameRoom-lipgloss.Width(badge))
	}
	head := marker + name
	if i == s.cursor {
		head = StyleSidebarItemActive.Render(marker + name)
	}
	out := head + badge + "\n"

	meta := []string{}
	if sup.SupplierSKU != "" {
		meta = append(meta, "SKU "+sup.SupplierSKU)
	}
	// BOTH barcodes, each named, and only where the vendor recorded one.
	//
	// They are codes on two different physical things — package_upc is printed
	// on the packaged quantity this supplier ships, unit_upc on an individual
	// unit when it differs (backend/inventory/models/core.py) — so an operator
	// holding a box and an operator holding a part scan different numbers, and
	// a row carrying only one of them leaves the other unmatchable against what
	// is in the hand. unit_upc is blank on most rows (omsapi/po_line_entry.go
	// says so), so the second entry costs nothing on the ordinary link.
	//
	// The WORDS are receiveScanKindLabel's, not the wire's: the receiving form
	// already teaches an operator that package_upc is the "box barcode" and
	// unit_upc the "unit barcode", and one fact spelled two ways across two
	// screens is a fact the operator has to translate. "package_upc" on the
	// pane is the wire talking to itself.
	if sup.PackageUPC != "" {
		meta = append(meta, "box barcode "+sup.PackageUPC)
	}
	if sup.UnitUPC != "" {
		meta = append(meta, "unit barcode "+sup.UnitUPC)
	}
	if sup.PackQuantity > 0 {
		meta = append(meta, fmt.Sprintf("pack %d", sup.PackQuantity))
	}
	if !sup.UnitCost.Empty() {
		meta = append(meta, "$"+sup.UnitCost.String()+"/unit")
	}
	if !sup.PackageCost.Empty() {
		meta = append(meta, "$"+sup.PackageCost.String()+"/pkg")
	}
	if sup.LeadTimeDays > 0 {
		meta = append(meta, "lead "+leadTimeText(sup.LeadTimeDays, sup.LeadTimeSource))
	}
	// FOLDED at its own " · " joints against the live pane, never written
	// straight to it.
	//
	// This row was already past the edge before a barcode was added to it: at
	// 80 columns the pane is 51 cells and an ordinary MRO link —
	// "SKU 91290A115 · pack 100 · $0.1450/unit · $14.50/pkg · lead 5d" — drew
	// as "SKU 91290A115 · pack 100 · $0.1450/unit · $14.5", so clampToBox took
	// the LEAD TIME off the pane entirely and left the package cost reading
	// $14.5, which is not a shortened price but a different one. Both are the
	// failure AGENTS.md records for the picker rows: a cut number reads as a
	// complete number, and a fact silently off the edge is a fact the operator
	// believes they were shown.
	//
	// pickerWrap folds at the joints these are built from, so a fold costs a
	// LINE and loses nothing — which is why the answer here is the fold rather
	// than a clip with a mark. It is additive on a wide terminal by
	// construction: at 100 and 120 columns the whole list still fits one line
	// and the row draws exactly as it did before.
	if len(meta) > 0 {
		for _, line := range s.metaLines(meta) {
			out += indent + StyleMuted.Render(line) + "\n"
		}
	}
	// CLIPPED and MARKED rather than folded: a URL carries no " · " joints and
	// pickerWords would break it at nothing, so a fold would scatter one
	// unbreakable token down the pane. An operator reads this to recognise the
	// listing, and pickerClip's ellipsis says where it stops — which is the
	// whole of what was missing when it was written straight to the pane.
	if sup.URL != "" {
		out += indent + StyleMuted.Render(pickerClip(sup.URL, s.suppliersPaneCells()-len(indent))) + "\n"
	}
	return strings.TrimRight(out, "\n")
}

// metaLines folds a row's facts to the live pane, dropping whole any fact too
// long for a line of its own and MARKING that it did.
//
// pickerWrap falls back to folding on SPACES when one claim outruns a line, and
// for a claim that is a NUMBER that is not a shortening but a corruption: at
// the 45-column floor Root drew at when this was written, the pane gives 16
// cells and
// "box barcode 00812345678905" came out as "box barcode" / "00812345678" /
// "905" — three lines an operator reads as a broken code, and two of them as
// digits belonging to nothing. A barcode, a SKU and a price are FACTS: whole,
// or gone and said to be gone. Only the ellipsis is drawn, never half a number.
//
// The drop MARK rides the last line it can, so saying a fact went does not
// itself cost the row another line. Marking is not optional: a row that
// silently lost its unit barcode is a row asserting the vendor recorded none.
func (s *ItemSuppliersScreen) metaLines(meta []string) []string {
	room := s.suppliersPaneCells() - len(indent)
	// pickerWrap indents continuations by two, so a fact must fit the NARROWEST
	// line it could land on or it is not guaranteed whole anywhere.
	fits := room - 2

	kept := make([]string, 0, len(meta))
	dropped := false
	for _, fact := range meta {
		if lipgloss.Width(fact) <= fits {
			kept = append(kept, fact)
			continue
		}
		dropped = true
	}
	if len(kept) == 0 {
		if dropped {
			return []string{pickerClip(strings.TrimSpace(poRowDropMark), room)}
		}
		return nil
	}
	lines := pickerWrap(strings.Join(kept, " · "), room)
	if !dropped {
		return lines
	}
	last := len(lines) - 1
	if lipgloss.Width(lines[last])+lipgloss.Width(poRowDropMark) <= room {
		lines[last] += poRowDropMark
	} else {
		lines = append(lines, strings.TrimSpace(poRowDropMark))
	}
	return lines
}

// itemSupplierName returns the best display name for a link row, falling back to
// the numeric supplier pk when the serializer didn't embed supplier_name.
func itemSupplierName(sup omsapi.ItemSupplier) string {
	if sup.SupplierName != "" {
		return sup.SupplierName
	}
	return fmt.Sprintf("supplier %d", sup.Supplier)
}

// ===========================================================================
// ItemSupplierFormScreen — create / edit one supplier link
// ===========================================================================

const (
	isSupplier = iota
	isSKU
	isURL
	isUnitCost
	isPackageCost
	isQtyPerPackage
	isLeadTime
	isPrimary
	isFieldMax
)

// itemSupplierFieldLabel is the shared label column. Keep the derived cue in
// the unit-cost label: short panes can omit its folded focus hint while leaving
// the field row visible. Keep labels no wider than "Quantity per package" to
// avoid shifting every input in the form.
var itemSupplierFieldLabel = map[int]string{
	isSupplier:      "Supplier",
	isSKU:           "Supplier SKU",
	isURL:           "Supplier URL",
	isUnitCost:      "Unit cost (derived)",
	isPackageCost:   "Package cost",
	isQtyPerPackage: "Quantity per package",
	isLeadTime:      "Average lead time",
	isPrimary:       "Primary supplier",
}

// itemSupplierFieldHint is the muted note drawn AFTER the input area: the unit a
// number is in, the shape of a URL, and which two fields the backend insists on.
// It carries what used to sit in the labels (a parenthetical there would widen
// the shared label column and shove every input right) and in the long
// placeholders (which filled the input area and hid the underscores that say a
// field is empty).
var itemSupplierFieldHint = map[int]string{
	isSupplier:      "required",
	isSKU:           "required",
	isURL:           "https://…",
	isUnitCost:      "per unit",
	isPackageCost:   "per package",
	isLeadTime:      "days",
	isQtyPerPackage: "units",
}

// itemSupplierFieldFocusHint explains the consequences of editing either cost.
// It is focus-only to preserve vertical space; the unit-cost label carries the
// standing derived cue. OpenMakerSuite's
// `inventory.services.suppliers.derive_costs` owns the exact derivation rule.
var itemSupplierFieldFocusHint = map[int]string{
	isUnitCost:    "derived from package cost ÷ qty · per unit · editing only this re-prices the package",
	isPackageCost: "per package · governs when both change · clearing it alone clears both prices",
}

func itemSupplierFieldIsText(id int) bool {
	switch id {
	case isSKU, isURL, isUnitCost, isPackageCost, isQtyPerPackage, isLeadTime:
		return true
	}
	return false
}

// itemSupplierFieldWidth sizes the input areas that are not the default: a URL
// is the one long field, and the four numbers are narrow enough that a
// full-width box would read as a text field.
func itemSupplierFieldWidth(id int) int {
	switch id {
	case isURL:
		return 40
	case isUnitCost, isPackageCost:
		return 12
	case isQtyPerPackage, isLeadTime:
		return 8
	}
	return 0
}

type itemSupplierFormPhase int

const (
	isPhaseForm itemSupplierFormPhase = iota
	isPhaseSupplierPick
)

// itemSupplierPickOption is one row in the supplier sub-picker.
type itemSupplierPickOption struct {
	id    int
	label string
}

type ItemSupplierFormScreen struct {
	deps     Deps
	edit     bool
	itemID   string
	itemName string
	rowID    int // ItemSupplier pk (edit mode)
	existing *omsapi.ItemSupplier

	loading bool
	loadErr string
	saving  bool
	errMsg  string

	// stale is the server's refusal of a save made from a copy of the link that
	// somebody else — a person, the lead-time measuring task, or a promotion
	// that demoted this link — wrote after this form was opened on it. While it
	// stands the form offers Ctrl-R, which re-reads the link, and withholds
	// Enter, because the copy's version only ever falls further behind and a
	// second save of it can only be refused again. Nothing here ever re-sends
	// with the version the refusal reports: that is the overwrite the token
	// exists to stop (omsapi's item_supplier_version.go).
	stale *omsapi.StaleSupplierLink
	// reloading is the Ctrl-R read in flight.
	reloading bool

	suppliers []omsapi.Supplier

	jdeScreen

	inputs         []textinput.Model
	supplierID     *int
	isPrimary      bool
	loadedLeadTime string

	fields []int
	cursor int

	// Supplier sub-picker state.
	phase       itemSupplierFormPhase
	pickCursor  int
	pickSearch  textinput.Model
	pickOptions []itemSupplierPickOption
}

type itemSupplierFormSuppliersMsg struct {
	suppliers []omsapi.Supplier
	err       error
}

type itemSupplierSavedMsg struct {
	link *omsapi.ItemSupplier
	err  error
}

// itemSupplierReloadedMsg answers Ctrl-R on a stale form: the link as stored.
type itemSupplierReloadedMsg struct {
	link *omsapi.ItemSupplier
	err  error
}

// NewItemSupplierFormScreen builds the create/edit form. A nil existing opens
// create mode (against itemID); a non-nil existing opens edit mode and hydrates
// every field from that link row (no re-fetch — the list already has it).
func NewItemSupplierFormScreen(deps Deps, itemID, itemName string, existing *omsapi.ItemSupplier) *ItemSupplierFormScreen {
	s := &ItemSupplierFormScreen{
		deps:     deps,
		edit:     existing != nil,
		itemID:   itemID,
		itemName: itemName,
		existing: existing,
		loading:  true,
	}
	if existing != nil {
		s.rowID = existing.ID
	}

	s.inputs = make([]textinput.Model, isFieldMax)
	for id := 0; id < isFieldMax; id++ {
		if !itemSupplierFieldIsText(id) {
			continue
		}
		ti := textinput.New()
		ti.Prompt = ""
		ti.CharLimit = itemSupplierCharLimit(id)
		ti.Placeholder = itemSupplierPlaceholder(id)
		s.inputs[id] = ti
	}
	// Create-mode numeric defaults mirror the ItemSupplier model defaults — all
	// but the LEAD TIME, whose box starts BLANK (its placeholder still shows the
	// 7). OMS labels a lead time by how it was obtained (omsapi.LeadTimeSource):
	// a number SENT is a recorded quote and an omitted key is the planning
	// default. A box pre-filled with "7" posted that 7 back untouched, so every
	// link added from this terminal was stored as a supplier who had quoted a
	// week — the very reading the provenance mark exists to stop, produced by
	// the screen that draws it. Blank now omits the key (buildPayload).
	if existing == nil {
		s.inputs[isQtyPerPackage].SetValue("1")
	}

	s.pickSearch = textinput.New()
	s.pickSearch.Prompt = ""
	s.pickSearch.Placeholder = "filter"
	s.pickSearch.CharLimit = 60

	s.fields = []int{isSupplier, isSKU, isURL, isUnitCost, isPackageCost, isQtyPerPackage, isLeadTime, isPrimary}
	s.syncFocus()
	return s
}

func itemSupplierCharLimit(id int) int {
	switch id {
	case isSKU:
		return 100
	case isURL:
		return 300
	case isUnitCost, isPackageCost:
		return 20
	case isQtyPerPackage, isLeadTime:
		return 10
	}
	return 100
}

// itemSupplierPlaceholder keeps only the two DEFAULTS — a blank quantity means
// one per package and a blank lead time means a week, which is worth seeing
// sitting in the field. Everything the others said is now a hint beside the
// input (see itemSupplierFieldHint).
func itemSupplierPlaceholder(id int) string {
	switch id {
	case isQtyPerPackage:
		return "1"
	case isLeadTime:
		return "7"
	}
	return ""
}

func (s *ItemSupplierFormScreen) Title() string {
	if s.edit {
		return "Edit supplier link"
	}
	if s.itemName != "" {
		return "Add supplier: " + s.itemName
	}
	return "Add supplier link"
}

func (s *ItemSupplierFormScreen) WantsRawInput() bool { return true }

func (s *ItemSupplierFormScreen) Init() tea.Cmd {
	return tea.Batch(s.loadSuppliers(), textinput.Blink)
}

func (s *ItemSupplierFormScreen) ctx() context.Context {
	if s.deps.Ctx != nil {
		return s.deps.Ctx
	}
	return context.Background()
}

func (s *ItemSupplierFormScreen) loadSuppliers() tea.Cmd {
	deps := s.deps
	ctx := s.ctx()
	return func() tea.Msg {
		sups, err := deps.OMS.ListAllSuppliers(ctx)
		return itemSupplierFormSuppliersMsg{suppliers: sups, err: err}
	}
}

func (s *ItemSupplierFormScreen) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		s.setSize(m)
		return s, nil
	case itemSupplierFormSuppliersMsg:
		s.loading = false
		if m.err != nil {
			s.loadErr = m.err.Error()
		} else {
			s.suppliers = m.suppliers
			if s.edit {
				s.hydrate()
			}
		}
		s.syncFocus()
		return s, nil
	case itemSupplierSavedMsg:
		s.saving = false
		if refusal, ok := omsapi.AsStaleSupplierLink(m.err); ok {
			s.stale = refusal
			s.errMsg = itemSupplierStaleHeadline(refusal)
			return s, Status(refusal.Message, StatusError)
		}
		if m.err != nil {
			s.errMsg = m.err.Error()
			return s, Status("save failed: "+m.err.Error(), StatusError)
		}
		verb := "added"
		if s.edit {
			verb = "updated"
		}
		name := ""
		if m.link != nil {
			name = itemSupplierName(*m.link)
		}
		return s, tea.Batch(
			Status(fmt.Sprintf("supplier link %s: %s", verb, name), StatusOK),
			SwitchTo(WSInventory, NewItemSuppliersScreen(s.deps, s.itemID, s.itemName)),
		)
	case itemSupplierReloadedMsg:
		return s.reloaded(m)
	case tea.KeyMsg:
		if s.loading {
			if m.String() == "esc" {
				return s, s.cancelCmd()
			}
			return s, nil
		}
		if s.phase == isPhaseSupplierPick {
			return s.updatePickPhase(m)
		}
		return s.updateFormPhase(m)
	}

	// Non-key messages (cursor blink) go to whichever input owns the caret.
	if s.phase == isPhaseSupplierPick {
		var cmd tea.Cmd
		s.pickSearch, cmd = s.pickSearch.Update(msg)
		return s, cmd
	}
	if id, ok := s.currentFieldID(); ok && itemSupplierFieldIsText(id) {
		var cmd tea.Cmd
		s.inputs[id], cmd = s.inputs[id].Update(msg)
		return s, cmd
	}
	return s, nil
}

func (s *ItemSupplierFormScreen) hydrate() {
	ex := s.existing
	if ex == nil {
		return
	}
	id := ex.Supplier
	s.supplierID = &id
	s.inputs[isSKU].SetValue(ex.SupplierSKU)
	s.inputs[isURL].SetValue(ex.URL)
	// A price the link does not carry is written as a BLANK rather than skipped:
	// hydrate is also the reload a stale refusal offers, and skipping would leave
	// a price the operator typed standing in a form that now claims to show the
	// link as stored.
	s.inputs[isUnitCost].SetValue("")
	if !ex.UnitCost.Empty() {
		s.inputs[isUnitCost].SetValue(ex.UnitCost.String())
	}
	s.inputs[isPackageCost].SetValue("")
	if !ex.PackageCost.Empty() {
		s.inputs[isPackageCost].SetValue(ex.PackageCost.String())
	}
	qty := ex.PackQuantity
	if qty <= 0 {
		qty = 1
	}
	s.inputs[isQtyPerPackage].SetValue(strconv.Itoa(qty))
	s.loadedLeadTime = strconv.Itoa(int(ex.LeadTimeDays))
	s.inputs[isLeadTime].SetValue(s.loadedLeadTime)
	s.isPrimary = ex.IsPreferred
}

func (s *ItemSupplierFormScreen) currentFieldID() (int, bool) {
	if s.cursor < 0 || s.cursor >= len(s.fields) {
		return 0, false
	}
	return s.fields[s.cursor], true
}

func (s *ItemSupplierFormScreen) syncFocus() {
	for id := 0; id < len(s.inputs); id++ {
		if itemSupplierFieldIsText(id) {
			s.inputs[id].Blur()
		}
	}
	if id, ok := s.currentFieldID(); ok && itemSupplierFieldIsText(id) {
		s.inputs[id].Focus()
	}
}

func (s *ItemSupplierFormScreen) updateFormPhase(m tea.KeyMsg) (Screen, tea.Cmd) {
	// The system keys, first and everywhere: they mean the same thing on every
	// row, which is the whole point of the reduced scheme (sc-h412).
	switch m.String() {
	case "esc":
		return s, s.cancelCmd()
	case "tab", "down":
		s.moveCursor(+1)
		return s, textinput.Blink
	case "shift+tab", "up":
		s.moveCursor(-1)
		return s, textinput.Blink
	case "pgdown":
		s.pageCursor(+1)
		return s, textinput.Blink
	case "pgup":
		s.pageCursor(-1)
		return s, textinput.Blink
	case "enter":
		// SUBMIT saves the link from any row. The supplier row used to swallow
		// enter to open its picker; that is Ctrl-E now, so enter means the same
		// thing here as it does everywhere else on the form.
		if s.saving || s.reloading {
			return s, nil
		}
		if s.stale != nil {
			// Unnamed while the refusal stands, and it DECLINES rather than
			// sending: the copy's version cannot catch up, so the request could
			// only be refused again. The row changes so the press is answered.
			s.errMsg = itemSupplierStaleEnterNote
			return s, nil
		}
		return s.submit()
	case "ctrl+r":
		return s.reload()
	case "ctrl+e":
		if id, ok := s.currentFieldID(); ok && id == isSupplier {
			s.openPicker()
			return s, textinput.Blink
		}
		return s, nil
	}

	id, ok := s.currentFieldID()
	if !ok {
		return s, nil
	}
	switch id {
	case isSupplier:
		// A picker row has nothing to type into and no accelerators left.
		return s, nil
	case isPrimary:
		// A two-value choice row: it flips whichever way it is cycled.
		switch m.String() {
		case " ", "right", "left":
			s.isPrimary = !s.isPrimary
		}
		return s, nil
	default:
		var cmd tea.Cmd
		s.inputs[id], cmd = s.inputs[id].Update(m)
		return s, cmd
	}
}

func (s *ItemSupplierFormScreen) moveCursor(delta int) {
	body := s.formLines()
	next, ok := s.moveRow(s.cursor, len(s.fields), delta, len(s.formHeader()), s.formBar(body))
	if !ok {
		return
	}
	s.cursor = next
	s.syncFocus()
}

func (s *ItemSupplierFormScreen) pageCursor(dir int) {
	body := s.formLines()
	next, ok := s.pageRow(body, s.cursor, len(s.fields), dir, len(s.formHeader()),
		s.formBar(body), s.formBarItems(true))
	if !ok {
		return
	}
	s.cursor = next
	s.syncFocus()
}

// ---------------------------------------------------------------------------
// Supplier sub-picker
// ---------------------------------------------------------------------------

func (s *ItemSupplierFormScreen) openPicker() {
	s.phase = isPhaseSupplierPick
	s.pickSearch.SetValue("")
	// The filter is always live in a columnar picker, so it holds the caret for
	// as long as the picker is open.
	s.pickSearch.Focus()
	s.applyPickFilter()

	// Start the cursor on the currently-selected supplier so re-picking is a
	// no-op keystroke.
	s.pickCursor = 0
	if s.supplierID != nil {
		for i, o := range s.pickOptions {
			if o.id == *s.supplierID {
				s.pickCursor = i
				break
			}
		}
	}
}

func (s *ItemSupplierFormScreen) applyPickFilter() {
	q := strings.ToLower(strings.TrimSpace(s.pickSearch.Value()))
	opts := []itemSupplierPickOption{}
	for _, sup := range s.suppliers {
		label := sup.Name
		if sup.SupplierType != "" {
			label = fmt.Sprintf("%s (%s)", sup.Name, sup.SupplierType)
		}
		if q == "" || strings.Contains(strings.ToLower(label), q) {
			opts = append(opts, itemSupplierPickOption{id: sup.ID, label: label})
		}
	}
	s.pickOptions = opts
	if s.pickCursor >= len(s.pickOptions) {
		s.pickCursor = 0
	}
}

func (s *ItemSupplierFormScreen) updatePickPhase(m tea.KeyMsg) (Screen, tea.Cmd) {
	switch act, delta := jdePickKey(m); act {
	case jdePickCancel:
		s.closePicker()
	case jdePickCommit:
		s.commitPick()
	case jdePickMove:
		s.movePick(delta)
	case jdePickPage:
		header, body := s.pickView()
		if next, ok := s.pageRow(body, s.pickCursor, len(s.pickOptions), delta, len(header),
			s.pickBar(header, body), jdePickBarCeiling("Select", "Cancel")); ok {
			s.pickCursor = next
		}
	default:
		// Anything else is filter text: the box is always live, so there is no
		// mode to enter and no "/" to remember.
		var cmd tea.Cmd
		s.pickSearch, cmd = s.pickSearch.Update(m)
		s.applyPickFilter()
		return s, cmd
	}
	return s, nil
}

// movePick walks the option cursor, clamping at both ends.
func (s *ItemSupplierFormScreen) movePick(delta int) {
	header, body := s.pickView()
	next, ok := s.pickRow(s.pickCursor, len(s.pickOptions), delta, len(header), s.pickBar(header, body))
	if !ok {
		return
	}
	s.pickCursor = next
}

func (s *ItemSupplierFormScreen) closePicker() {
	s.phase = isPhaseForm
	s.pickSearch.SetValue("")
	s.pickSearch.Blur()
	s.syncFocus()
}

func (s *ItemSupplierFormScreen) commitPick() {
	if s.pickCursor >= 0 && s.pickCursor < len(s.pickOptions) {
		id := s.pickOptions[s.pickCursor].id
		s.supplierID = &id
	}
	s.closePicker()
}

// ---------------------------------------------------------------------------
// Submit
// ---------------------------------------------------------------------------

func (s *ItemSupplierFormScreen) submit() (Screen, tea.Cmd) {
	body, err := s.buildPayload()
	if err != nil {
		s.errMsg = err.Error()
		return s, Status(err.Error(), StatusError)
	}
	s.saving = true
	s.errMsg = ""
	deps := s.deps
	ctx := s.ctx()
	edit := s.edit
	rowID := s.rowID
	return s, func() tea.Msg {
		var link *omsapi.ItemSupplier
		var e error
		if edit {
			link, e = deps.OMS.UpdateItemSupplier(ctx, rowID, body)
		} else {
			link, e = deps.OMS.CreateItemSupplier(ctx, body)
		}
		return itemSupplierSavedMsg{link: link, err: e}
	}
}

func (s *ItemSupplierFormScreen) buildPayload() (omsapi.ItemSupplierWrite, error) {
	var w omsapi.ItemSupplierWrite
	if strings.TrimSpace(s.itemID) == "" {
		return w, errors.New("no item context")
	}
	if s.supplierID == nil {
		return w, errors.New("supplier is required")
	}
	sku := strings.TrimSpace(s.inputs[isSKU].Value())
	if sku == "" {
		return w, errors.New("supplier SKU is required")
	}

	qty, err := itemSupplierParseInt(s.inputs[isQtyPerPackage].Value(), 1)
	if err != nil || qty < 1 {
		return w, errors.New("quantity per package must be a whole number ≥ 1")
	}
	lead, err := itemSupplierParseInt(s.inputs[isLeadTime].Value(), 7)
	if err != nil || lead < 0 {
		return w, errors.New("average lead time must be a whole number ≥ 0")
	}
	// A blank box on CREATE omits the key, so OMS stores its planning default
	// AS the default; sending the 7 would store it as a quote. On EDIT an
	// unchanged value is omitted so an unrelated edit preserves its source; a
	// blank or changed value keeps the form's existing write semantics.
	//
	// The omission OUTLIVED the version token on purpose. Against an OMS with
	// #1091 it is redundant: a copy whose lead time differs from storage is
	// stale, and the version below gets it refused, while an equal echo keeps
	// its source anyway. But a server before #1091 ignores the version, and a
	// row it served carries none, so there the omission is still the only thing
	// keeping an unrelated edit from writing a stale 7 over a newer measurement.
	// It costs nothing where it is redundant. (The web retired its equivalent
	// because it ships in lockstep with the server; this terminal does not.)
	leadTime := &lead
	if !s.edit && strings.TrimSpace(s.inputs[isLeadTime].Value()) == "" {
		leadTime = nil
	} else if s.edit && s.inputs[isLeadTime].Value() == s.loadedLeadTime {
		leadTime = nil
	}

	unitCost, err := itemSupplierParseMoney(s.inputs[isUnitCost].Value())
	if err != nil {
		return w, errors.New("unit cost must be a number")
	}
	packageCost, err := itemSupplierParseMoney(s.inputs[isPackageCost].Value())
	if err != nil {
		return w, errors.New("package cost must be a number")
	}

	// An edit states the version the link was LOADED at, so a link somebody
	// wrote since is refused rather than overwritten. It is the copy's own
	// version and nothing else — a reload replaces the copy, and with it this.
	version := 0
	if s.edit && s.existing != nil {
		version = s.existing.Version
	}

	w = omsapi.ItemSupplierWrite{
		Item:               s.itemID,
		Supplier:           *s.supplierID,
		SupplierSKU:        sku,
		SupplierURL:        strings.TrimSpace(s.inputs[isURL].Value()),
		UnitCost:           unitCost,
		PackageCost:        packageCost,
		QuantityPerPackage: qty,
		AverageLeadTime:    leadTime,
		IsPrimary:          s.isPrimary,
		Version:            version,
	}
	return w, nil
}

// itemSupplierParseInt parses an optional whole-number field, returning def when
// the field is blank.
func itemSupplierParseInt(raw string, def int) (int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return def, nil
	}
	return strconv.Atoi(raw)
}

// itemSupplierParseMoney validates an optional decimal field and returns it as a
// *string: nil when blank (sent as explicit null so an edit can clear it), the
// trimmed original string otherwise (preserving the entered precision — the
// backend parses the decimal).
func itemSupplierParseMoney(raw string) (*string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	if _, err := strconv.ParseFloat(raw, 64); err != nil {
		return nil, err
	}
	return &raw, nil
}

// ---------------------------------------------------------------------------
// A save refused because the copy is stale
// ---------------------------------------------------------------------------

// reload answers Ctrl-R, and only while a stale refusal stands.
//
// A link that was CHANGED is re-read into this form, which is the web item
// form's "Reload suppliers": what the operator typed is replaced by the link as
// stored, so their next save starts from a copy the server accepts and they see
// what the other writer did before making their change again. A link that was
// DELETED has no current copy to read, so the reload is the item's supplier
// list, loaded afresh — the server's own sentence asks for exactly that.
func (s *ItemSupplierFormScreen) reload() (Screen, tea.Cmd) {
	if s.stale == nil || s.saving || s.reloading {
		return s, nil
	}
	if s.stale.Deleted() {
		return s, s.cancelCmd()
	}
	s.reloading = true
	s.errMsg = ""
	deps := s.deps
	ctx := s.ctx()
	id := s.rowID
	return s, func() tea.Msg {
		link, err := deps.OMS.GetItemSupplier(ctx, id)
		return itemSupplierReloadedMsg{link: link, err: err}
	}
}

// reloaded lands the Ctrl-R read.
func (s *ItemSupplierFormScreen) reloaded(m itemSupplierReloadedMsg) (Screen, tea.Cmd) {
	s.reloading = false
	if m.err != nil {
		var api *omsapi.APIError
		if errors.As(m.err, &api) && api.IsNotFound() {
			// Deleted between the refusal and the reload: the list is the only
			// current copy there is.
			return s, tea.Batch(Status(itemSupplierGoneNote, StatusWarn), s.cancelCmd())
		}
		// The refusal still stands — nothing about the copy changed — so the
		// key stays on offer and the row says why the press did not land.
		s.errMsg = "reload failed: " + m.err.Error()
		return s, Status(s.errMsg, StatusError)
	}
	if m.link == nil {
		s.errMsg = "reload failed: the server sent no link"
		return s, Status(s.errMsg, StatusError)
	}
	s.existing = m.link
	s.stale = nil
	s.errMsg = ""
	s.hydrate()
	s.syncFocus()
	return s, Status("supplier link reloaded as stored — make your change again", StatusOK)
}

// itemSupplierStaleHeadline is the status row's account of a stale refusal.
//
// The row is one line of 49 cells at 80 columns and cannot fold, so it LEADS
// with what the operator cannot act without — nothing was saved — and says
// which of the two refusals it is. The server's complete sentence is the pinned
// header's (formHeader) and the bottom line's, and Ctrl-R is named on the bar.
func itemSupplierStaleHeadline(r *omsapi.StaleSupplierLink) string {
	if r.Deleted() {
		return itemSupplierStaleDeletedNote
	}
	return itemSupplierStaleChangedNote
}

const (
	itemSupplierStaleChangedNote = "nothing saved: link changed since it was opened"
	itemSupplierStaleDeletedNote = "nothing saved: this link was deleted meanwhile"
	// itemSupplierStaleEnterNote answers Enter while the refusal stands. It is
	// worded differently from both headlines so the press visibly lands.
	itemSupplierStaleEnterNote = "nothing sent: this copy is out of date"
	// itemSupplierGoneNote is the flash when the reload finds the link deleted.
	itemSupplierGoneNote = "that supplier link was deleted — showing the item's suppliers"
	// itemSupplierReloadCaveat is the consequence of Ctrl-R, stated before the
	// key is pressed: it discards what was typed, which is the point, and is
	// never something the operator should discover afterwards.
	itemSupplierReloadCaveat = "Reloading replaces everything typed on this form with the link as it is stored now."
)

// formHeader pins the stale refusal above the form, and is nil otherwise.
//
// The server's sentence is the complete account and is unbounded prose, so it
// goes in as a FITTED block: a short pane re-draws it at fewer rows with the cut
// marked (jdeHeader.addFitted) rather than leaving a fragment that reads as the
// whole. The reload caveat under it is a CAVEAT, not an account, so it is drawn
// whole or given up whole (jdeHeader.addCaveat): half of a warning about what
// Ctrl-R discards reads as a different warning. Nothing here is essential. The two things the operator cannot act
// without — that nothing was saved, and the key that reloads — ride the status
// row and the bar, which no budget trims; the rows kept for the body are what
// keeps the field under the cursor on the pane.
func (s *ItemSupplierFormScreen) formHeader() jdeHeader {
	if s.stale == nil {
		return nil
	}
	width := s.bodyWidth()
	message := s.stale.Message
	styled := func(rows int) []string {
		return jdeCaveatLinesStyled(message, width, rows, StyleStatusError)
	}
	h := jdeHeader{}.addFitted(jdeHeadContext, jdeHeadContext, styled(0), styled)
	if !s.stale.Deleted() {
		h = h.addCaveat(jdeHeadContext, jdeCaveatLines(itemSupplierReloadCaveat, width))
	}
	return h.add(jdeHeadDecorative, "")
}

func (s *ItemSupplierFormScreen) cancelCmd() tea.Cmd {
	return SwitchTo(WSInventory, NewItemSuppliersScreen(s.deps, s.itemID, s.itemName))
}

// ---------------------------------------------------------------------------
// View
// ---------------------------------------------------------------------------

func (s *ItemSupplierFormScreen) View() string {
	if s.loading {
		return StyleMuted.Render("Loading suppliers…")
	}
	if s.loadErr != "" {
		return StyleStatusError.Render("Error: ") + s.loadErr + "\n\n" + StyleMuted.Render("esc to go back")
	}
	if s.phase == isPhaseSupplierPick {
		return s.viewPick()
	}
	return s.viewForm()
}

func (s *ItemSupplierFormScreen) viewForm() string {
	body := s.formLines()
	verb := "Saving…"
	if s.reloading {
		verb = "Reloading the supplier link…"
	}
	return s.frameWithHeader(s.formHeader(), body, s.cursor,
		s.statusRow(s.saving || s.reloading, verb, s.errMsg), s.formBar(body))
}

// formFields describes the link as columnar rows: the supplier is a picker, the
// primary flag a two-value choice, everything else typed into.
func (s *ItemSupplierFormScreen) formFields() []jdeField {
	out := make([]jdeField, len(s.fields))
	for i, id := range s.fields {
		f := jdeField{
			Label:   itemSupplierFieldLabel[id],
			Width:   itemSupplierFieldWidth(id),
			Hint:    itemSupplierFieldHint[id],
			Focused: i == s.cursor,
		}
		switch id {
		case isSupplier:
			value, dim := s.supplierValue()
			f.Kind, f.Value, f.Dim = jdeValue, value, dim
			if f.Focused {
				f.Hint = "Ctrl-E picks · required"
			}
		case isPrimary:
			f.Kind, f.Value = jdeChoice, jdeYesNo(s.isPrimary)
		default:
			f.Kind, f.Input = jdeText, &s.inputs[id]
			if id == isLeadTime {
				f.Hint = s.leadTimeHint()
			}
			if f.Focused {
				if hint, ok := itemSupplierFieldFocusHint[id]; ok {
					f.Hint = hint
				}
			}
		}
		out[i] = f
	}
	return out
}

// leadTimeHint is the lead-time box's hint. The box SHOWS a lead time, so it
// says where that number came from — but only while the box still holds the
// number the link was opened on: once the operator has typed another, the
// stored provenance describes a number that is no longer on the screen, and
// what the server will call the new one is its decision, not a prediction to
// draw here. On create the box starts blank, and a blank is the default.
func (s *ItemSupplierFormScreen) leadTimeHint() string {
	hint := itemSupplierFieldHint[isLeadTime]
	if !s.edit {
		return hint + " · blank = default"
	}
	ex := s.existing
	if ex == nil || s.inputs[isLeadTime].Value() != strconv.Itoa(int(ex.LeadTimeDays)) {
		return hint
	}
	if mark := leadTimeMark(ex.LeadTimeSource); mark != "" {
		return hint + " " + mark
	}
	return hint
}

func (s *ItemSupplierFormScreen) formLines() *jdeLines {
	fields := s.formFields()
	l := &jdeLines{}
	heading := StyleJDEHeading.Render("Supplier link")
	if s.itemName != "" {
		heading += "  " + StyleMuted.Render("for ") + s.itemName
	}
	l.Add(heading)
	l.AddFittedFields(fields, jdeLabelWidth(fields), s.bodyWidth(), 0)
	return l
}

// formBar names the keys that work on the form, with PgUp/PgDn on it exactly
// when the body moves under the bar that is about to be drawn.
//
// The paging claim is measured against formBarItems(true) — the bar WITH the
// pair on it — because naming them costs cells, cells fold the bar onto another
// row, and a folded bar leaves the body one row fewer. The tallest bar is the
// fixed point, so the answer cannot oscillate between frames.
func (s *ItemSupplierFormScreen) formBar(body *jdeLines) []actionBarItem {
	return s.formBarItems(s.bodyPagesForBar(body, len(s.fields), len(s.formHeader()), s.formBarItems(true)))
}

// formBarItems is formBar for a given paging state, so the bar that is
// MEASURED is the bar that is drawn.
func (s *ItemSupplierFormScreen) formBarItems(paging bool) []actionBarItem {
	// A stale refusal trades Enter for Ctrl-R (reload's doc says why), and a
	// reload in flight names neither: both arms decline until it lands.
	var items []actionBarItem
	switch {
	case s.stale == nil:
		items = append(items, actionBarItem{"Enter", "Save"})
	case !s.reloading:
		items = append(items, actionBarItem{"Ctrl-R", "Reload"})
	}
	items = append(items, actionBarItem{"Esc", "Cancel"}, actionBarItem{"UP/DN", "Fields"})
	if id, ok := s.currentFieldID(); ok {
		switch id {
		case isSupplier:
			items = append(items, actionBarItem{"Ctrl-E", "Pick"})
		case isPrimary:
			items = append(items, actionBarItem{"←→", "Change"})
		}
	}
	if paging {
		items = append(items, actionBarItem{"PgUp/PgDn", "Page"})
	}
	return items
}

// supplierValue is the supplier row's text and whether it is an empty state.
// Plain text plus a flag, not pre-styled muted text: a focused row reverse-
// videos the whole field, and an inner reset would end the highlight partway.
func (s *ItemSupplierFormScreen) supplierValue() (string, bool) {
	if s.supplierID == nil {
		return "(none chosen yet)", true
	}
	for _, sup := range s.suppliers {
		if sup.ID == *s.supplierID {
			return sup.Name, false
		}
	}
	if s.existing != nil && s.existing.SupplierName != "" {
		return s.existing.SupplierName, false
	}
	return fmt.Sprintf("#%d", *s.supplierID), false
}

// pickView builds the supplier picker's pinned header and its option list.
func (s *ItemSupplierFormScreen) pickView() (jdeHeader, *jdeLines) {
	return jdePickList{
		Title:  "Supplier",
		For:    s.itemName,
		Filter: s.pickSearch,
		Count:  len(s.pickOptions),
		Label:  func(i int) string { return s.pickOptions[i].label },
		Cursor: s.pickCursor,
		Empty:  "(no matching suppliers — create one first from the menu: Inventory › Suppliers)",
	}.render(s.bodyWidth())
}

// pickBar is the picker's bar, with PgUp/PgDn on it exactly when the option
// list moves under the bar about to be drawn — measured against the bar WITH
// the pair on it, because the tallest bar is the fixed point.
func (s *ItemSupplierFormScreen) pickBar(header jdeHeader, body *jdeLines) []actionBarItem {
	return jdePickBar("Select", len(s.pickOptions), s.bodyPagesForBar(body, len(s.pickOptions), len(header), jdePickBarCeiling("Select", "Cancel")))
}

func (s *ItemSupplierFormScreen) viewPick() string {
	header, body := s.pickView()
	return s.frameWithHeader(header, body, s.pickCursor,
		s.statusRow(false, "", ""), s.pickBar(header, body))
}
