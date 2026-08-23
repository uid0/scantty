// po_create_pickers.go — Phase 3 of the New PO state machine.
//
// Holds the three picker phases (reorder queue, inventory items,
// assets) so po_create.go stays focused on the supplier picker, the
// source-chooser menu, and the line-entry form. Each phase shares
// the same shape: load list → render windowed list → j/k navigate,
// enter to commit, b to go back, esc to cancel. The inventory and
// assets pickers add `/` for filter / search.
package tui

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/uid0/scantty/internal/omsapi"
)

// ---------------------------------------------------------------------------
// Saying something — the rule these pickers broke
// ---------------------------------------------------------------------------

// Every operator action on these screens that performs work off the terminal
// must report that it is WORKING, and must report FAILURE. A key that declines
// to act must say why. The pickers used to answer several keys with a bare
// `return s, nil`, which redraws a screen byte-for-byte identical to the one
// before the press — and from the operator's seat a keystroke that changes
// nothing and says nothing is indistinguishable from a wedged program.
//
// That is what the "the screen just hangs after I press enter" report actually
// was. Nothing was blocked and nothing was in flight: enter inside the item
// picker's search box only CLOSED the box (the pick needed a second enter), and
// enter over an empty filtered list returned nil. Both redrew the same pixels,
// so the operator concluded the program had stopped. The three states below —
// working, succeeded, failed — are now all visible, and no arm of these
// switches is allowed to be silent.

// pickerNote is a picker's own answer to the last keypress, rendered in the
// screen BODY. The status bar carries the same words, but a Flash expires after
// four seconds (status.go) and the operator who pressed enter and saw nothing is
// exactly the operator still staring at the picker a minute later — so the body
// line is the one that has to survive.
type pickerNote struct {
	text  string
	level StatusLevel
}

// render styles the note for the BODY, folded to the pane by pickerWrap. text
// may also carry explicit newlines, which stay as forced breaks.
//
// Folding is not cosmetic here. Root.View() TRUNCATES the pane rather than
// wrapping it, and the tail of one of these sentences is where the key that
// gets the operator OUT of the state is named — so a clipped hint is the
// silence this whole file exists to remove, wearing a tick mark, and it is
// worse than no hint at all because the operator believes they read it.
func (n pickerNote) render() string {
	if n.text == "" {
		return ""
	}
	mark, style := "", StyleMuted
	switch n.level {
	case StatusError:
		mark, style = "✗ ", StyleStatusError
	case StatusWarn:
		mark, style = "! ", StyleStatusWarn
	case StatusOK:
		mark, style = "✓ ", StyleStatusOK
	}
	// The mark eats two cells of the first line. Budgeting it off every line is
	// two columns conservative on the continuations and costs nothing.
	width := pickerPaneWidth
	if mark != "" {
		width -= lipgloss.Width(mark)
	}
	lines := pickerWrap(n.text, width)
	out := make([]string, 0, len(lines))
	for i, line := range lines {
		if i == 0 {
			out = append(out, style.Render(mark+line))
			continue
		}
		// Continuation lines are muted and already indented by pickerWrap:
		// they carry the way OUT of the state, not the state itself.
		out = append(out, StyleMuted.Render(line))
	}
	return strings.Join(out, "\n")
}

// flash is the note reduced to ONE line for the status bar, which has no room
// for the continuation.
func (n pickerNote) flash() string {
	if i := strings.IndexByte(n.text, '\n'); i >= 0 {
		return n.text[:i]
	}
	return n.text
}

// say records the note and flashes the same words on the status bar. Both, not
// either: the bar is where an operator's eye already goes for "did that work",
// and the body line is what is still there once the flash has gone.
func (n *pickerNote) say(text string, level StatusLevel) tea.Cmd {
	n.text, n.level = text, level
	return Status(n.flash(), level)
}

// cellPrefix returns the longest prefix of text that draws within max CELLS.
// It is the measurement half of every bound on these screens, and it makes ONE
// forward pass: it stops as soon as the budget is spent, so its cost is the
// budget rather than the length of what it was handed.
//
// That is the whole reason it exists beside truncateVisible (layout.go), which
// does the same job by dropping ONE rune off the end and re-measuring the whole
// remaining string — O(n²) with an O(n) allocation per step. Harmless on a
// label; not on the values these screens clip, because omsapi.parseError puts
// the ENTIRE raw response body into APIError.Message whenever the JSON envelope
// carries no code, and the source chooser re-renders the row carrying it about
// ten times per frame. Measured against a 20 KB gateway page: 711ms for one
// clip, and 1.5s for one fold of a 5 KB unspaced token — seconds of freeze per
// keystroke, which is the symptom this whole change exists to remove.
//
// Escape sequences are stepped over rather than measured, and the scan only
// ever returns on a boundary between them, so a cut never splits one and never
// bleeds colour into the next column — the property truncateVisible's doc
// comment is about. A rune's width is asked of lipgloss one rune at a time,
// which over-counts a multi-rune grapheme cluster (an emoji built from a ZWJ
// run) rather than under-counting it: the error is on the side of clipping
// early, so the result is never WIDER than the budget it was given.
func cellPrefix(text string, max int) string {
	if max <= 0 {
		return ""
	}
	const (
		plain = iota
		afterEsc
		inEsc
	)
	state, used := plain, 0
	for i, r := range text {
		switch state {
		case afterEsc:
			state = inEsc
			continue
		case inEsc:
			if (r >= 0x40 && r <= 0x7e) || r == 0x07 {
				state = plain
			}
			continue
		}
		if r == 0x1b {
			state = afterEsc
			continue
		}
		w := lipgloss.Width(string(r))
		if used+w > max {
			return text[:i]
		}
		used += w
	}
	return text
}

// pickerClip bounds an operator-supplied string before it goes into a note. The
// pane is 51 columns at the terminal's narrowest supported width and Root.View()
// truncates, so an unbounded search term or supplier name would push the rest of
// the sentence — the part naming the key to press — off the right edge.
//
// `max` is CELLS, not runes, because every caller computes its budget in cells
// (lipgloss.Width against pickerPaneWidth). Counting runes here made the bound
// disagree with the budget it was asked for: one CJK or emoji rune is two
// cells, so a clipped value could render twice as wide as the room reserved for
// it and clampToBox would take the tail — the very cut the clip exists to stop,
// reached with a different alphabet.
//
// Nothing here measures the WHOLE string: cellPrefix stops at the budget, so
// clipping a multi-KB OMS error body costs the same as clipping a supplier
// name. An unbounded value reaching a clip must stay cheap, because the row
// carrying one is redrawn on every keystroke.
func pickerClip(text string, max int) string {
	if max <= 0 {
		return ""
	}
	if head := cellPrefix(text, max); head == text {
		return text
	}
	if max <= 1 {
		return cellPrefix(text, max)
	}
	return cellPrefix(text, max-1) + "…"
}

func (n *pickerNote) clear() { n.text, n.level = "", StatusInfo }

// pickerPaneWidth is the columns a picker frame actually gets at the narrowest
// terminal this project checks against: Root.View() clamps the body to
// screenBodyWidth(80) = 51 (AGENTS.md) and TRUNCATES what does not fit.
//
// Every note and every fixed hint on these screens is folded to it rather than
// hand-counted against it. Hand-counting is what produced the class of bug this
// constant exists to close — a note reads fine at the width its author had in
// mind and then grows a prefix, a supplier name or a match count and silently
// loses the key it was written to name.
var pickerPaneWidth = screenBodyWidth(80)

// pickerWrap folds text onto as many lines as it needs to fit width, and
// indents every line after the first by two so a folded sentence still reads as
// one. Explicit newlines in text are forced breaks.
//
// It folds at the " · " joints these hints are built from before it falls back
// to spaces, because those joints separate whole claims ("b picks another line
// source", "esc cancels the order") and a claim split across two lines is
// harder to read than one claim per line. The separator itself is dropped at a
// fold — the indent already says the line is a continuation.
func pickerWrap(text string, width int) []string {
	const indent = "  "
	if width < 12 {
		width = 12
	}
	var out []string
	budget := func() int {
		if len(out) == 0 {
			return width
		}
		return width - lipgloss.Width(indent)
	}
	push := func(line string) {
		if len(out) == 0 {
			out = append(out, line)
			return
		}
		out = append(out, indent+line)
	}
	for _, para := range strings.Split(text, "\n") {
		cur := ""
		flush := func() {
			if cur != "" {
				push(cur)
				cur = ""
			}
		}
		for _, seg := range strings.Split(para, " · ") {
			if seg == "" {
				continue
			}
			if cur != "" && lipgloss.Width(cur)+3+lipgloss.Width(seg) <= budget() {
				cur += " · " + seg
				continue
			}
			flush()
			if lipgloss.Width(seg) <= budget() {
				cur = seg
				continue
			}
			// One claim too long for a line of its own — fold it on spaces
			// rather than let clampToBox take the end off it.
			for _, word := range pickerWords(seg, width-lipgloss.Width(indent)) {
				switch {
				case cur == "":
					cur = word
				case lipgloss.Width(cur)+1+lipgloss.Width(word) <= budget():
					cur += " " + word
				default:
					flush()
					cur = word
				}
			}
		}
		flush()
	}
	return out
}

// pickerWords splits a run of text into pieces no wider than width, breaking
// mid-token when a token is wider than that on its own. The tokens that need it
// are not English: a failed lookup carries whatever OMS put in the response
// body, and a URL or an unspaced JSON blob has nowhere to fold. Better to break
// one in the middle than to hand clampToBox a 200-cell line and lose all but
// the first 51 of it.
func pickerWords(text string, width int) []string {
	if width < 4 {
		width = 4
	}
	var out []string
	for _, word := range strings.Fields(text) {
		for {
			// The cut is measured in CELLS: slicing `width` RUNES off a
			// double-width token would hand back a piece up to twice the line
			// it was cut to fit. cellPrefix walks forward and stops at the
			// budget, so a token is split in one pass over it rather than one
			// pass per piece — an unspaced 5 KB body took 1.5 seconds to fold
			// when each piece re-measured the rest of the token.
			head := cellPrefix(word, width)
			if head == word || head == "" {
				break
			}
			out = append(out, head)
			word = word[len(head):]
		}
		if word != "" {
			out = append(out, word)
		}
	}
	return out
}

// pickerHint renders a fixed muted line — a way out, a working line, a summary,
// a list's action bar — folded to the pane. Nothing writes such a line directly
// any more: routing them all through one function is what keeps the next one
// from being the one that overruns.
//
// Named for the pickers it was written for, but it is the project's pane-local
// folder generally, and deliberately outside internal/tui/jde_form.go: the list
// screens and the New PO help line are not on the columnar layer and must not
// have to join it just to be legible at 80 columns.
func pickerHint(text string) string {
	lines := pickerWrap(text, pickerPaneWidth)
	for i, line := range lines {
		lines[i] = StyleMuted.Render(line)
	}
	return strings.Join(lines, "\n")
}

// pickerFail renders a failed lookup as headline + detail. The detail goes on
// its own folded continuation because an OMS error string is arbitrarily long:
// on one line the label alone ("looking up this supplier's items failed:" is 40
// of the 51 columns) leaves the operator reading a colon and nothing after it.
// rows caps how many rendered rows the whole failure block may occupy, and the
// DETAIL is what gets sacrificed to fit — never the way-out bar or the verdict
// note the callers write under it.
//
// The detail is an OMS response body and it is unbounded: omsapi.parseError
// puts the ENTIRE raw body in APIError.Message whenever the JSON envelope
// carries no code, so a gateway page or a Django debug page is multi-KB, and
// pickerWords folds an unspaced blob at one line per 47 cells. Before the cap,
// roughly 380 characters pushed the verdict note off an 80x24 pane — a
// declining key on the failure frame answering into the four-second flash
// alone — and roughly 470 took "esc cancels the order" with it, stranding the
// operator on an error frame naming no way out. Folding had traded the
// horizontal cut for a vertical one, exactly as it did for the list footer.
//
// rows <= 0 means the caller has no height yet (terminalHeight unset); nothing
// is TRIMMED then, because a guess would be worse than the clip clampToBox
// already applies — but the detail is still bounded before it is folded.
//
// Bounding it first is the point: at most `rows` lines of it can ever be drawn,
// so folding the whole body is work whose result is thrown away, and the body
// has no size limit. Folding a 5 KB unspaced payload took 1.5 seconds, and this
// block is rebuilt on every keystroke. Below the bound the hidden-row count is
// exact; above it the marker stops counting rather than name a number that is
// only true of the part that was folded.
func pickerFail(what, detail string, rows int) string {
	fold := rows
	if fold <= 0 {
		fold = pickerFailUnsizedRows
	}
	long := false
	if detail != "" {
		if head := cellPrefix(detail, fold*pickerPaneWidth); head != detail {
			detail, long = head, true
		}
	}
	text := what
	if detail != "" {
		text += "\n" + detail
	}
	out := pickerNote{text, StatusError}.render()
	if rows <= 0 {
		return out
	}
	lines := strings.Split(out, "\n")
	if len(lines) <= rows {
		return out
	}
	if rows == 1 {
		return lines[0]
	}
	// A block that cannot fit says how many rows it hid, the same contract
	// renderWindowedList's markers keep.
	hid := fmt.Sprintf("  … %d more line(s) of the error", len(lines)-(rows-1))
	if long {
		hid = "  … more of the error than this pane can hold"
	}
	kept := append([]string{}, lines[:rows-1]...)
	kept = append(kept, StyleMuted.Render(hid))
	return strings.Join(kept, "\n")
}

// pickerFailUnsizedRows is how much of an error body a failure block folds when
// the caller has no pane height yet. Nothing is trimmed in that state, so this
// is only a ceiling on the WORK: deeper than any terminal this app is driven
// at, and finite, which is what an OMS response body is not.
const pickerFailUnsizedRows = 40

// pickerWayOut is what every picker frame says when the list itself cannot help
// — both keys are live in every non-typing picker state.
const pickerWayOut = "b picks another line source · esc cancels the order"

// searchBoxWayOut is the same sentence while the SEARCH BOX owns the keyboard,
// where it would be a lie: b is a letter going into the query and esc only
// closes the box, it does not cancel the order. A frame that prints both claims
// at once is the bar-honesty defect, two frames away from the eight instances
// of it just fixed on the list screens.
const searchBoxWayOut = "esc closes the search"

// ---------------------------------------------------------------------------
// Async loaders + msg types
// ---------------------------------------------------------------------------

// Every picker reply echoes the supplierID it was asked about, and
// handlePickerLoaded drops one whose supplier is no longer the order's —
// the same guard poAgreementsLoadedMsg already carries.
//
// It is not a nicety on these three. Every row they carry is scoped to a
// supplier, and an ItemSupplier id belongs to exactly one: a reply for supplier
// A landing after the operator has moved to supplier B would repaint B's picker
// with A's catalog and report it as a success, and the line staged from it
// names an item-supplier B does not sell. That is a wrong purchase order with
// nothing on screen to flag it. The catalog load is now N sequential page
// requests rather than one, so the window is wide enough to hit.
type poReorderItemsLoadedMsg struct {
	supplierID int
	items      []omsapi.ReorderDataItem
	err        error
}

type poItemSuppliersLoadedMsg struct {
	supplierID int
	rows       []omsapi.ItemSupplier
	err        error
}

// poAssetsLoadedMsg carries a request generation as well as the supplier,
// because the asset picker is the one picker whose reply depends on more than
// the supplier: it answers a query and a page. Two lookups for the SAME
// supplier therefore cannot be told apart by supplierID, and the in-flight flag
// that used to keep there from being two is cleared by
// resetSupplierScopedPickers, so a supplier round trip reopened the race.
type poAssetsLoadedMsg struct {
	supplierID int
	seq        int
	rows       []omsapi.Asset
	hasNext    bool
	err        error
}

func (s *PurchaseOrderCreateScreen) loadReorderItemsForSupplier() tea.Cmd {
	deps := s.deps
	ctx := deps.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	supplierID := s.supplierID
	return func() tea.Msg {
		data, err := deps.OMS.GetReorderData(ctx)
		if err != nil {
			return poReorderItemsLoadedMsg{supplierID: supplierID, err: err}
		}
		// reorder_data is grouped by supplier — pick our slice.
		for _, sup := range data.Suppliers {
			if sup.ID == supplierID {
				return poReorderItemsLoadedMsg{supplierID: supplierID, items: sup.Items}
			}
		}
		return poReorderItemsLoadedMsg{supplierID: supplierID, items: nil}
	}
}

func (s *PurchaseOrderCreateScreen) loadItemSuppliersForSupplier() tea.Cmd {
	deps := s.deps
	ctx := deps.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	supplierID := s.supplierID
	return func() tea.Msg {
		rows, err := deps.OMS.ListItemSuppliersForSupplier(ctx, supplierID)
		if err != nil {
			return poItemSuppliersLoadedMsg{supplierID: supplierID, err: err}
		}
		return poItemSuppliersLoadedMsg{supplierID: supplierID, rows: rows}
	}
}

func (s *PurchaseOrderCreateScreen) loadAssetsForSupplier(search string) tea.Cmd {
	deps := s.deps
	ctx := deps.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	supplierID := s.supplierID
	page := s.assetsPage
	if page <= 0 {
		page = 1
	}
	// Stamped here rather than at the five arms that fire a load, so a sixth
	// one cannot be added without a generation: this is the only place an asset
	// request is built.
	s.assetsSeq++
	seq := s.assetsSeq
	return func() tea.Msg {
		p, err := deps.OMS.ListAssetsForSupplier(ctx, supplierID, search, page)
		if err != nil {
			return poAssetsLoadedMsg{supplierID: supplierID, seq: seq, err: err}
		}
		hasNext := p.Next != nil && *p.Next != ""
		return poAssetsLoadedMsg{supplierID: supplierID, seq: seq, rows: p.Results, hasNext: hasNext}
	}
}

// pickerReplySupplier reports which supplier a picker reply was asked about.
// One place, so a fourth picker message cannot be added without the question
// being asked of it too.
func pickerReplySupplier(msg tea.Msg) (int, bool) {
	switch m := msg.(type) {
	case poReorderItemsLoadedMsg:
		return m.supplierID, true
	case poItemSuppliersLoadedMsg:
		return m.supplierID, true
	case poAssetsLoadedMsg:
		return m.supplierID, true
	}
	return 0, false
}

// handlePickerLoaded dispatches the three async results to the right
// state slot. Returns nil so the caller can chain into tea.Cmd.
func (s *PurchaseOrderCreateScreen) handlePickerLoaded(msg tea.Msg) tea.Cmd {
	if id, ok := pickerReplySupplier(msg); ok && id != s.supplierID {
		// The operator committed another supplier while this was in flight.
		// Dropping it whole is deliberate: the loading flag belongs to the
		// request that is still out for the CURRENT supplier, and clearing it
		// here would paint that one's reply as already arrived.
		return nil
	}
	switch m := msg.(type) {
	case poReorderItemsLoadedMsg:
		s.reorderLoading = false
		s.reorderNote.clear() // the reply's own frame answers for this one
		if m.err != nil {
			s.reorderLoadErr = m.err.Error()
			return Status("load reorder items failed: "+m.err.Error(), StatusError)
		}
		s.reorderLoadErr = ""
		s.reorderItems = m.items
		s.reorderCursor = 0
		// Selections index into the list we just replaced — drop them so a
		// stale index can never mark (and bulk-add) the wrong row.
		s.reorderSelected = map[int]bool{}
	case poItemSuppliersLoadedMsg:
		s.itemSuppliersLoad = false
		if m.err != nil {
			s.itemSuppliersErr = m.err.Error()
			s.itemSuppliersNote.clear() // the error line answers for the screen
			return Status("looking up this supplier's items failed: "+m.err.Error(), StatusError)
		}
		// Clear the PREVIOUS failure. renderItemPick shows the error instead of
		// the list, so a stale string left here would hide a load that worked.
		s.itemSuppliersErr = ""
		s.itemSuppliersAll = m.rows
		s.itemSuppliersFor = m.supplierID
		s.applyItemSupplierFilter()
		s.itemSuppliersCur = 0
		if len(m.rows) == 0 {
			// FOUND NOTHING and COULD NOT TELL are different facts, and only
			// one of them is safe to act on. A green "✓ 0 catalog item(s)
			// loaded" says the first about a screen that may mean the second,
			// and it advertises a '/' that can only ever answer "no match".
			// Say what an empty catalog is — the same sentence the frame falls
			// back to, so the two can never diverge — and mark it as the
			// warning it is. The asset path next door already does this.
			return s.itemSuppliersNote.say(s.noCatalogSentence(), StatusWarn)
		}
		if s.itemSuppliersTyping || strings.TrimSpace(s.itemSuppliersSearch.Value()) != "" {
			// A query was typed while the walk was out. Answer THAT rather than
			// the whole catalog — and through the shared gate, so the box being
			// open decides whether '/' may be named. With the box open '/' is a
			// character going into the query, not a key that searches.
			s.itemSuppliersNote = s.itemFilterOrVerdict("")
			return Status(s.itemSuppliersNote.flash(), s.itemSuppliersNote.level)
		}
		return s.itemSuppliersNote.say(
			fmt.Sprintf("%d catalog item(s) loaded · / searches", len(m.rows)), StatusOK)
	case poAssetsLoadedMsg:
		if m.seq != s.assetsSeq {
			// An older lookup for this same supplier. Dropped whole, flag
			// included: assetsLoading belongs to the request that is still out,
			// and clearing it here would paint that one's reply as arrived.
			return nil
		}
		s.assetsLoading = false
		if m.err != nil {
			s.assetsErr = m.err.Error()
			s.assetsNote.clear()
			return Status("looking up this supplier's assets failed: "+m.err.Error(), StatusError)
		}
		s.assetsErr = ""
		s.assets = m.rows
		s.assetsHasNext = m.hasNext
		s.assetsCursor = 0
		return s.assetLoadedNote(len(m.rows))
	}
	return nil
}

// ---------------------------------------------------------------------------
// One statement of what works here
// ---------------------------------------------------------------------------

// The three picker bars below are each picker's WAY-OUT line, and they are read
// by BOTH surfaces that used to state it separately: the screen's action bar
// (helpText, drawn at the top of the pane) and the frame's own hint (drawn
// beside the note). Keeping those two in sync by hand is what produced a bar
// promising "enter picks the match" four rows above a note saying enter closes
// the search — with enter doing neither, because with several matches it
// declines and says so. Two surfaces, one sentence, no drift.
//
// They are NOT the only place a picker names a key, and the earlier wording of
// this comment claimed they were. The notes name keys too, and they have to:
// a note is what answers a specific press, so it can be narrower than the bar
// (itemFilterNote's zero-match arm names '/' to edit the query, which the bar's
// empty-list arm — reached for a filter that matched nothing AND for a supplier
// that sells nothing — does not name, because it cannot tell those two apart
// from the row count alone). The rule the bars enforce is the weaker and real
// one: whatever a bar names must act in the state being drawn, and the bar and
// the note must not make CONTRADICTORY claims about the same key.
//
// Reconciling that empty-list arm — so the bar can say "no match, / edits the
// search" separately from "sells nothing, r reloads" — is deferred to the
// queued columnar conversion of these screens, which restates every bar in the
// JD Edwards layer anyway. It is a gap in coverage, not a contradiction: the
// note is the more specific of the two and both are true.
//
// Deferred to the same conversion, and for the same reason, is the REPEAT of
// the way-out line on the two failure frames. Drawing the note under the bar
// (so a declining key produces a visible change there) means the item and asset
// error frames print "r retries the lookup · b picks another line source · esc
// cancels the order" as the bar and then again as the verdict note's second
// line, with a third copy in helpText at the top of the pane. Both copies are
// true, no key is dead and nothing wrong is staged; it costs two rows of an
// 18-row pane. Dropping the way-out tail from catalogVerdict and
// assetVerdictNote when the frame already draws the bar is the fix, and it is
// a bar-layout decision that belongs with the conversion rather than another
// hand-folded hint here.
//
// "Acts" means CHANGES something. A key that declines and says why — enter over
// an empty list, `]` at the last page — is not acting, and is deliberately left
// unnamed: naming it would advertise a dead end, and the project's rule is that
// such an arm must answer, not that the bar must promise it.

// itemPickBar names the keys that act in the item picker's current state.
func (s *PurchaseOrderCreateScreen) itemPickBar() string {
	switch {
	case s.itemSuppliersTyping:
		// enter's outcome depends on the match count, so the bar states the
		// CONDITION and the note states this moment's outcome. Neither can
		// contradict the other, and "picks the match" — which promised the
		// multi-match staging the design deliberately refuses — is gone.
		//
		// Both of those hold only while the catalog is ON SCREEN. The box opens
		// mid-walk on purpose (the rows are on their way and the query lands
		// with them) and it survives a failed reload, because the frame keeps
		// the rows it was showing; in both of those states commitSearchedItem
		// declines at its first line. Naming enter there put the pane's ONLY
		// claim about enter in flat contradiction with what enter does, so name
		// the two keys that really work: runes go into the query, esc shuts the
		// box.
		if !s.itemListOnScreen() {
			return "type to filter · " + searchBoxWayOut
		}
		return "type to filter · enter picks when one row is left · esc closes the search"
	case s.itemSuppliersLoad:
		// A query typed now is applied when the rows land, so '/' is real here.
		// r is not: a walk is already out.
		return "/ searches · " + pickerWayOut
	case s.itemSuppliersErr != "":
		return "r retries the lookup · " + pickerWayOut
	case len(s.itemSuppliers) == 0:
		return "r reloads · " + pickerWayOut
	}
	return "j/k ↑↓ move · enter picks · / searches · r reloads · " + pickerWayOut
}

// assetPickBar names the keys that act in the asset picker's current state.
func (s *PurchaseOrderCreateScreen) assetPickBar() string {
	switch {
	case s.assetsTyping:
		if s.assetsLoading {
			// Enter is gated while a search is in flight (see the typing arm of
			// updateAssetPickPhase), so it is not named here. The failure frame
			// is deliberately NOT gated: with nothing in flight there is no
			// race, and enter out of the box is the retry "/ retries with a
			// search" promised one frame earlier.
			return "type to search · " + searchBoxWayOut
		}
		return "type to search · enter runs the search · esc closes the search"
	case s.assetsLoading:
		return "/ searches · " + pickerWayOut
	case s.assetsErr != "":
		return "/ retries with a search · " + pickerWayOut
	case len(s.assets) == 0:
		return "/ searches · " + pickerWayOut
	}
	bar := "j/k ↑↓ move · enter picks · / searches"
	if s.assetsHasNext {
		bar += " · ] next page"
	}
	if s.assetsPage > 1 {
		bar += " · [ prev page"
	}
	return bar + " · " + pickerWayOut
}

// reorderPickBar names the keys that act in the reorder picker's current state.
func (s *PurchaseOrderCreateScreen) reorderPickBar() string {
	if !s.reorderListOnScreen() || len(s.reorderItems) == 0 {
		return pickerWayOut
	}
	return "j/k ↑↓ move · space marks · a adds all · enter adds · " + pickerWayOut
}

// supplierPickBar names the keys that act in the supplier picker's current
// state. The supplier list is the fourth picker frame on this screen and had
// the same hole the other three did: while the suppliers were still loading —
// or had failed — the bar promised "j/k move · enter commits" over a frame that
// renders nothing, and enter with no highlighted row returned nil in silence.
func (s *PurchaseOrderCreateScreen) supplierPickBar() string {
	switch {
	case s.supplierLoading:
		return "esc cancels the order"
	case s.supplierLoadErr != "":
		return "esc cancels the order"
	case len(s.suppliers) == 0:
		return "esc cancels the order"
	case s.pending && s.supplierHighlightIsCommitted():
		// The highlighted row is the supplier the order already carries, so
		// enter commits nothing and only goes back to the source chooser —
		// navigation, not a change, and the one way back into the order from
		// this frame while the POST is out. Named for exactly that row.
		return "j/k ↑↓ move · enter goes back · esc cancels the order"
	case s.pending:
		// A DIFFERENT supplier: committing it would re-target the request, so
		// it is frozen with the rest of the payload (updateSupplierPhase) and
		// the bar drops it. j/k still move a highlight — onto the committed row
		// among others — and esc still leaves.
		return "j/k ↑↓ move · esc cancels the order"
	}
	return "j/k ↑↓ move · enter commits · esc cancels the order"
}

// supplierListOnScreen reports whether renderSupplierPhase is drawing rows.
func (s *PurchaseOrderCreateScreen) supplierListOnScreen() bool {
	return !s.supplierLoading && s.supplierLoadErr == "" && len(s.suppliers) > 0
}

// supplierVerdictNote says which of the three off-screen states a declining key
// is answering from. It is a pickerNote and not a bare Status for the reason
// the other two pickers already are: a flash expires after four seconds and
// renderSupplierPhase drew nothing at all in two of these three states, so the
// pane never moved and the operator who pressed the key saw exactly what the
// report described.
func (s *PurchaseOrderCreateScreen) supplierVerdictNote(prefix string) tea.Cmd {
	lead := ""
	if prefix != "" {
		lead = prefix + " · "
	}
	switch {
	case s.supplierLoading:
		return s.supplierNote.say(lead+"still looking up the suppliers…", StatusInfo)
	case s.supplierLoadErr != "":
		return s.supplierNote.say(lead+"loading suppliers failed\nesc cancels the order", StatusError)
	}
	return s.supplierNote.say(lead+"no suppliers are configured\nesc cancels the order", StatusWarn)
}

// supplierSwitchBar names the confirm's two keys, and deliberately carries no
// supplier NAME: a 20-cell name is what pushed the decline claim off the bottom
// of a 24-row pane, and the decline is the safe answer on a destructive confirm.
func (s *PurchaseOrderCreateScreen) supplierSwitchBar() string {
	return fmt.Sprintf("ctrl+x drops %d line(s) and switches · esc keeps the cart and this supplier",
		s.supplierScopedLineCount())
}

// itemListOnScreen / assetListOnScreen / reorderListOnScreen report whether the
// picker's renderer is actually DRAWING its rows. Each renderer returns early
// on its working frame and on its failure frame, and none of the three clears
// the rows it was holding when it does — deliberately, because a reload that
// fails should not also destroy what the operator was looking at.
//
// The keys that act on a row have to ask. A picker that holds twenty rows
// behind a "Reloading…" line still had a cursor the operator could move with
// j/k and a row enter would stage, and neither was on the pane: an item going
// onto a purchase order that the operator cannot see is a wrong purchase order.
// The failure frame is worse again, because it names r/b/esc and nothing else,
// so enter acting there is also the bar naming one set of keys while another
// set works.
//
// itemListOnScreen answers for the SEARCH BOX too, not just the row keys: the
// item filter runs client-side over the loaded catalog, so enter inside the box
// picks out of the same invisible slice j/k would move through. The asset box
// is gated on assetsLoading instead — that search really goes off the terminal,
// and there the hazard is a second request racing the first rather than a pick
// out of nothing.
func (s *PurchaseOrderCreateScreen) itemListOnScreen() bool {
	return !s.itemSuppliersLoad && s.itemSuppliersErr == ""
}

func (s *PurchaseOrderCreateScreen) assetListOnScreen() bool {
	return !s.assetsLoading && s.assetsErr == ""
}

func (s *PurchaseOrderCreateScreen) reorderListOnScreen() bool {
	return !s.reorderLoading && s.reorderLoadErr == ""
}

// assetLoadedNote words what a finished asset search found — through the same
// typing gate the item picker's notes go through. The reply can land with the
// search box still OPEN (press 'a', then '/' while the request is out), and
// there enter runs the search again rather than picking, while '/' and 'b' are
// characters going into the query.
func (s *PurchaseOrderCreateScreen) assetLoadedNote(rows int) tea.Cmd {
	if rows == 0 {
		// A search that found nothing is a RESULT, not a blank screen: say
		// what was searched for so the operator can tell "no such asset"
		// from "I mistyped" — and read assetsQuery, the query this reply
		// actually answers, not the live box, which may already hold
		// something nobody has submitted.
		if q := strings.TrimSpace(s.assetsQuery); q != "" {
			tail := "/ edits the search · b picks another source"
			if s.assetsTyping {
				tail = "edit the search to widen it"
			}
			return s.assetsNote.say(
				"no asset matches "+strconv.Quote(pickerClip(q, 16))+"\n"+tail, StatusWarn)
		}
		return s.assetsNote.say("this supplier has no assets on file", StatusWarn)
	}
	if s.assetsTyping {
		// "again" asserts a previous run, which is false whenever the box was
		// opened over an unfiltered page-1 load — the rows landing here answer
		// no query at all.
		runs := "enter runs the search"
		if strings.TrimSpace(s.assetsQuery) != "" {
			runs = "enter runs the search again"
		}
		return s.assetsNote.say(
			fmt.Sprintf("%d asset(s) · %s · esc closes it", rows, runs), StatusInfo)
	}
	return s.assetsNote.say(
		fmt.Sprintf("%d asset(s) · enter picks the highlighted row", rows), StatusOK)
}

// assetVerdictNote and reorderVerdictNote are the asset and reorder pickers'
// equivalents of catalogVerdictNote: they say which of the two off-screen
// states a declining key is answering from, and name the key that leaves it.
// prefix, when given, leads with what the key did — the same shape
// reportItemFilterState uses, and for the same reason: esc out of the search
// box lands here whenever the lookup is still out, and without a lead it
// re-emits the note the declining keypress before it already put on the pane,
// leaving a query in the box, the same two lines under it and only the caret
// moving.
func (s *PurchaseOrderCreateScreen) assetVerdictNote(prefix string) tea.Cmd {
	lead := ""
	if prefix != "" {
		lead = prefix + " · "
	}
	if s.assetsLoading {
		return s.assetsNote.say(
			lead+"still looking up the assets "+s.supplierLabel()+" supplied…", StatusInfo)
	}
	return s.assetsNote.say(
		lead+"the asset lookup failed\n/ retries with a search · "+pickerWayOut, StatusError)
}

func (s *PurchaseOrderCreateScreen) reorderVerdictNote(prefix string) tea.Cmd {
	lead := ""
	if prefix != "" {
		lead = prefix + " · "
	}
	if s.reorderLoading {
		return s.reorderNote.say(
			lead+"still looking up what "+s.supplierLabel()+" has flagged for reorder…", StatusInfo)
	}
	return s.reorderNote.say(
		lead+"reading the reorder queue failed\n"+pickerWayOut, StatusError)
}

// noCatalogSentence is the single wording of "this supplier sells nothing".
// The load that finds an empty catalog and the frame that renders one both go
// through it, so the note and the fallback line under it can never end up
// saying two different things about the same fact.
func (s *PurchaseOrderCreateScreen) noCatalogSentence() string {
	return s.supplierLabel() + " has no active catalog items on file"
}

// itemPickEntryNote is what the picker says when it opens on a catalog it
// already holds, in place of the "Looking up…" frame it would otherwise show
// over work that is not happening. Same three-way split as a fresh load, minus
// the tick: nothing was just fetched, so nothing succeeded.
func (s *PurchaseOrderCreateScreen) itemPickEntryNote() tea.Cmd {
	if !s.catalogAnswered() || len(s.itemSuppliersAll) == 0 {
		return s.catalogVerdictNote("")
	}
	return s.itemSuppliersNote.say(
		fmt.Sprintf("%d catalog item(s) · / searches · r reloads", len(s.itemSuppliersAll)), StatusInfo)
}

// applyItemSupplierFilter populates itemSuppliers from itemSuppliersAll
// using the search-input value (case-insensitive substring on
// ItemName / SupplierSKU). Backend has no ?search= on this endpoint
// today, so we do it client-side over the whole catalog — which
// ListItemSuppliersForSupplier pages in for exactly this reason.
func (s *PurchaseOrderCreateScreen) applyItemSupplierFilter() {
	q := strings.ToLower(strings.TrimSpace(s.itemSuppliersSearch.Value()))
	if q == "" {
		s.itemSuppliers = s.itemSuppliersAll
		return
	}
	filtered := make([]omsapi.ItemSupplier, 0, len(s.itemSuppliersAll))
	for _, r := range s.itemSuppliersAll {
		if strings.Contains(strings.ToLower(r.ItemName), q) ||
			strings.Contains(strings.ToLower(r.SupplierSKU), q) {
			filtered = append(filtered, r)
		}
	}
	s.itemSuppliers = filtered
}

// ---------------------------------------------------------------------------
// Phase 3a: Reorder-queue picker
// ---------------------------------------------------------------------------

// reorderEmptyNote answers every key that acts on the reorder queue — a row
// (j / k / space / enter) or all of it (a) — while the picker is drawing a list
// with no rows in it. Say so in the BODY as well as
// the flash: the frame's fixed "Nothing flagged…" line is already on the pane,
// so a Status alone left it byte-for-byte unchanged and expired four seconds
// later with nothing recording the press. The lead is what the key DID, so two
// different keys do not answer with the same sentence.
func (s *PurchaseOrderCreateScreen) reorderEmptyNote(lead string) tea.Cmd {
	if lead != "" {
		lead += " · "
	}
	return s.reorderNote.say(lead+"nothing flagged for reorder here\n"+pickerWayOut, StatusWarn)
}

func (s *PurchaseOrderCreateScreen) updateReorderPickPhase(m tea.KeyMsg) (Screen, tea.Cmd) {
	if !s.reorderListOnScreen() {
		switch m.String() {
		case "j", "down", "k", "up":
			return s, s.reorderVerdictNote(m.String() + " moves nothing")
		case " ":
			return s, s.reorderVerdictNote("nothing to mark")
		case "a":
			return s, s.reorderVerdictNote("nothing to add")
		case "enter":
			// Its own lead, not `a`'s: this frame draws no rows, no highlight
			// and no focused input, so two keys sharing one sentence redraw a
			// byte-for-byte identical pane on the second press.
			return s, s.reorderVerdictNote("nothing to pick")
		}
	}
	switch m.String() {
	case "esc":
		return s, SwitchTo(WSPurchasing, nil)
	case "b":
		s.phase = poPhaseSource
		return s, nil
	case "j", "down":
		// Drawn and EMPTY, which the !reorderListOnScreen() gate above does not
		// catch: the frame is showing "Nothing flagged for reorder…" and these
		// arms answered with nil, so the pane did not move and there was not
		// even a highlight to see stay put.
		if len(s.reorderItems) == 0 {
			return s, s.reorderEmptyNote(m.String() + " moves nothing")
		}
		if s.reorderCursor < len(s.reorderItems)-1 {
			s.reorderCursor++
		}
	case "k", "up":
		if len(s.reorderItems) == 0 {
			return s, s.reorderEmptyNote(m.String() + " moves nothing")
		}
		if s.reorderCursor > 0 {
			s.reorderCursor--
		}
	case " ":
		// Mark/unmark this row for a bulk add. Marking several rows and
		// pressing enter is the middle ground between adding one item at a
		// time and taking the supplier's whole queue with 'a' (sc-ytr5).
		if len(s.reorderItems) == 0 {
			return s, s.reorderEmptyNote("nothing to mark")
		}
		if s.reorderCursor >= 0 && s.reorderCursor < len(s.reorderItems) {
			if s.reorderSelected == nil {
				s.reorderSelected = map[int]bool{}
			}
			if s.reorderSelected[s.reorderCursor] {
				delete(s.reorderSelected, s.reorderCursor)
			} else {
				s.reorderSelected[s.reorderCursor] = true
			}
		}
		return s, nil
	case "a":
		// Add ALL of this supplier's reorder items in one press — the fix for
		// "a supplier with 15 items is ~30 keystrokes". Every row is staged
		// with its suggested_quantity; the review cart is where individual
		// lines get adjusted (ctrl+e), so land there.
		return s, s.addReorderLines(s.reorderItems)
	case "enter":
		// With rows marked, enter stages exactly those (in list order).
		// Otherwise it keeps the original one-row behavior: open the line
		// form pre-filled so quantity/date/cost can be set before staging.
		if len(s.reorderSelected) > 0 {
			picked := make([]omsapi.ReorderDataItem, 0, len(s.reorderSelected))
			for i, it := range s.reorderItems {
				if s.reorderSelected[i] {
					picked = append(picked, it)
				}
			}
			return s, s.addReorderLines(picked)
		}
		if len(s.reorderItems) == 0 {
			return s, s.reorderEmptyNote("nothing to pick")
		}
		if s.reorderCursor < 0 || s.reorderCursor >= len(s.reorderItems) {
			s.reorderCursor = 0
		}
		it := s.reorderItems[s.reorderCursor]
		qty := it.SuggestedQuantity
		if qty <= 0 {
			qty = 1
		}
		unitCost := 0.0
		if v, err := strconv.ParseFloat(string(it.UnitCost), 64); err == nil {
			unitCost = v
		}
		desc := it.ItemName
		if desc == "" {
			desc = it.SKU
		}
		// The reorder_data row carries no quantity_per_package, so a reorder
		// line stays single-basis (qpp 0) — the case-cost toggle is offered
		// from the inventory-items picker, which does expose qpp. (op-7j8v)
		s.enterLinePhase(it.ItemSupplierID, nil, desc, qty, unitCost, 0, 0)
		s.reorderNote.clear()
		return s, tea.Batch(
			Status("picked "+desc+" — set quantity and cost, enter adds the line", StatusOK),
			textinput.Blink,
		)
	}
	return s, nil
}

// reorderCartLine stages one reorder-queue row as a cart line without going
// through the Phase-4 form, producing exactly what the single-row enter path
// produces once the operator accepts the prefill: suggested_quantity (floored
// at 1) and the item's name (SKU when unnamed) as the label.
//
// Cost follows the same rule the form does. The row's unit_cost is the
// item-supplier's stored cost (reorder_data reads item_supplier.unit_cost), and
// the line form now seeds its cost field with it on a catalog line, so a bulk
// add carries it too — the operator sees the same price whether the line was
// added one at a time or fifteen at once, and ctrl+e can change or clear it
// (sc-gnzw). A row whose catalog cost is unset stays blank rather than pinning
// an explicit $0, which leaves the backend pricing the line (sc-5yr). A row
// without an item_supplier_id can only be created as a freeform line, and that
// branch REQUIRES a cost, so its unit_cost is always sent (0 when the row
// carries none — visible as "@ $0" in the cart, and fixable with ctrl+e).
func reorderCartLine(it omsapi.ReorderDataItem) poCartLine {
	qty := it.SuggestedQuantity
	if qty <= 0 {
		qty = 1
	}
	desc := it.ItemName
	if desc == "" {
		desc = it.SKU
	}
	line := omsapi.PurchaseOrderCreateItem{
		Description:    desc,
		Quantity:       qty,
		ItemSupplierID: it.ItemSupplierID,
	}
	unitCost := 0.0
	if v, err := strconv.ParseFloat(string(it.UnitCost), 64); err == nil {
		unitCost = v
	}
	if it.ItemSupplierID == nil || unitCost > 0 {
		line.UnitCost = &unitCost
	}
	label := desc
	if label == "" {
		label = "line"
	}
	return poCartLine{item: line, label: label}
}

// addReorderLines stages every supplied reorder row and drops the operator in
// the review cart, where any individual line can be adjusted with ctrl+e or
// dropped with ctrl+x. Clears the marks so the picker is clean if it is
// re-entered for a second batch.
func (s *PurchaseOrderCreateScreen) addReorderLines(items []omsapi.ReorderDataItem) tea.Cmd {
	if len(items) == 0 {
		// Through the picker's own note, like the j / k / space / enter arms
		// one switch above: this was the last arm answering through the
		// SCREEN-level failure line, which is where a failed submit's detail
		// lives. Writing errMsg alone left errDetail standing, so after a 502
		// the pane drew "nothing to add" with several folded rows of the
		// gateway's HTML underneath it, presented as that sentence's reason.
		return s.reorderEmptyNote("nothing to add")
	}
	for _, it := range items {
		s.lines = append(s.lines, reorderCartLine(it))
	}
	s.reorderSelected = map[int]bool{}
	s.reorderNote.clear()
	s.setErr("", "")
	s.phase = poPhaseReview
	s.reviewCursor = len(s.lines) - len(items) // first line of this batch
	s.poNotes.Focus()
	return tea.Batch(
		Status(fmt.Sprintf("added %d line(s) (%d in cart)", len(items), len(s.lines)), StatusOK),
		textinput.Blink,
	)
}

func (s *PurchaseOrderCreateScreen) renderReorderPick() string {
	// Every frame draws reorderNote UNDER its own line, the way the item and
	// asset frames do: this picker used to answer a declining key with a Status
	// flash alone, so the body was byte-for-byte unchanged and four seconds
	// later nothing on the pane recorded that the key had been pressed.
	note := ""
	if n := s.reorderNote.render(); n != "" {
		note = "\n" + n
	}
	if s.reorderLoading {
		return pickerHint("Looking up what "+s.supplierLabel()+" has flagged for reorder…") + note
	}
	if s.reorderLoadErr != "" {
		// The tail is measured BEFORE the failure block is built, so the error
		// detail is budgeted against what is left rather than the bar and note
		// against what the error happens to leave.
		tail := pickerHint(s.reorderPickBar()) + note
		return pickerFail("reading the reorder queue failed", s.reorderLoadErr,
			s.bodyRowBudget(poRenderedRows(tail))) + "\n" + tail
	}
	if len(s.reorderItems) == 0 {
		return pickerHint("Nothing flagged for reorder under this supplier.") + "\n" +
			pickerHint(s.reorderPickBar()) + note
	}
	// Counts only. The keys are the bar's job, and a summary that also named
	// them was a second copy of the same claim waiting to go stale.
	summary := fmt.Sprintf("%d in the queue", len(s.reorderItems))
	if n := len(s.reorderSelected); n > 0 {
		summary = fmt.Sprintf("%d marked of %d in the queue", n, len(s.reorderItems))
	}
	tail := "\n" + pickerHint(summary) + note

	var b strings.Builder
	b.WriteString(renderWindowedList(
		len(s.reorderItems), s.reorderCursor, s.bodyRowBudget(poRenderedRows(tail)),
		func(i, room int) string {
			it := s.reorderItems[i]
			// Checkbox for the bulk-add marks, so a marked row still reads as
			// marked once the highlight moves off it.
			mark := "[ ] "
			if s.reorderSelected[i] {
				mark = "[x] "
			}
			tag := ""
			if it.HasActiveReorderReq {
				tag = " " + StyleStatusOK.Render(fmt.Sprintf("[reorder %s]", it.ReorderRequestStatus))
			}
			cost := ""
			if it.UnitCost != "" {
				cost = "  " + StyleMuted.Render(fmt.Sprintf("@ %s", it.UnitCost))
			}
			// The mark is the row's own state and never gives. What is being
			// ORDERED — the suggested quantity — is the fact; the stock levels
			// behind it, the price and the request flag are the decorations,
			// dropped from the right so the columns that stay keep their places.
			levels := fmt.Sprintf(" (current %d / min %d)", it.CurrentStock, it.MinimumStock)
			return mark + poFitRow(room-lipgloss.Width(mark), it.ItemName,
				fmt.Sprintf("  qty %d", it.SuggestedQuantity), levels, cost, tag)
		},
	))
	b.WriteString(tail)
	return b.String()
}

// ---------------------------------------------------------------------------
// Phase 3b: Inventory items picker
// ---------------------------------------------------------------------------

func (s *PurchaseOrderCreateScreen) updateItemPickPhase(m tea.KeyMsg) (Screen, tea.Cmd) {
	if s.itemSuppliersTyping {
		switch m.Type {
		case tea.KeyEsc:
			// Close the box but KEEP the filter — this is the browse path, so
			// it has to say that the rows still on screen are a filtered subset
			// and that j/k now move again.
			s.itemSuppliersTyping = false
			s.itemSuppliersSearch.Blur()
			return s, s.reportItemFilterState("search closed")
		case tea.KeyEnter:
			return s, s.commitSearchedItem()
		}
		var cmd tea.Cmd
		s.itemSuppliersSearch, cmd = s.itemSuppliersSearch.Update(m)
		s.applyItemSupplierFilter()
		if s.itemSuppliersCur >= len(s.itemSuppliers) {
			s.itemSuppliersCur = 0
		}
		// Live count as they type, so "nothing matches" is visible BEFORE the
		// enter that used to answer it with silence — but only once the walk
		// has ANSWERED. Filtering an empty slice that is empty because the
		// request has not come back yet produced `no match for "w" (0 in
		// catalog)`, which is a conclusion about a catalog nobody has seen:
		// the same found-nothing / could-not-tell conflation catalogVerdict
		// exists to close, reached from the typing path instead of a key arm.
		s.itemSuppliersNote = s.itemFilterOrVerdict("")
		return s, cmd
	}
	if !s.itemListOnScreen() {
		switch m.String() {
		case "j", "down", "k", "up":
			return s, s.catalogVerdictNote(m.String() + " moves nothing")
		case "enter":
			return s, s.catalogVerdictNote("nothing to pick")
		}
	}
	switch m.String() {
	case "esc":
		return s, SwitchTo(WSPurchasing, nil)
	case "b":
		s.phase = poPhaseSource
		return s, nil
	case "j", "down":
		// The gate above catches a list that is not DRAWN; this catches one
		// that is drawn and EMPTY, which is the state the report is about — a
		// search that matched nothing. `return s, nil` there redrew a
		// byte-for-byte identical pane with not even a cursor to see stay put,
		// because the frame has no rows and the box is shut.
		//
		// Only the empty list. An EDGE is a weaker case: the highlight is on
		// screen and visibly at the end, so the press has answered itself.
		if len(s.itemSuppliers) == 0 {
			return s, s.reportItemFilterState(m.String() + " moves nothing")
		}
		if s.itemSuppliersCur < len(s.itemSuppliers)-1 {
			s.itemSuppliersCur++
		}
	case "k", "up":
		if len(s.itemSuppliers) == 0 {
			return s, s.reportItemFilterState(m.String() + " moves nothing")
		}
		if s.itemSuppliersCur > 0 {
			s.itemSuppliersCur--
		}
	case "/":
		if s.itemSuppliersErr != "" {
			// The failure frame names r, b and esc. '/' is not among them, and
			// opening the box here would redraw the frame with "esc closes the
			// search" WHERE "r retries the lookup" was — an unnamed key that
			// acts and erases the only key that repairs the state. Mid-load is
			// different and still opens: rows are on their way.
			return s, s.catalogVerdictNote("search needs the catalog")
		}
		if s.catalogAnswered() && len(s.itemSuppliersAll) == 0 {
			// This filter is client-side over the loaded catalog, so against a
			// supplier we KNOW sells nothing the box can only ever answer "no
			// match" — and opening it would take the keyboard away from b and
			// esc, the two keys that can still do something here. Decline, and
			// say why. Mid-walk is a different state: the rows are on their
			// way, so the box opens and the query is applied when they land.
			return s, s.catalogVerdictNote("search needs the catalog")
		}
		s.itemSuppliersTyping = true
		s.itemSuppliersSearch.Focus()
		// Same split as the typing arm of itemPickBar: opening the box while
		// the walk is out is allowed, but promising a pick out of it is not.
		opened := "type to search · enter picks the ONE match\nesc closes the search and keeps the filter"
		if !s.itemListOnScreen() {
			opened = "type to search · esc closes the search and keeps the filter"
		}
		return s, tea.Batch(
			s.itemSuppliersNote.say(opened, StatusInfo),
			textinput.Blink,
		)
	case "r":
		if s.itemSuppliersLoad {
			// A walk is already out; the bar does not name r there.
			return s, s.catalogVerdictNote("already reloading")
		}
		// The catalog is held per supplier and re-entering the picker no longer
		// re-walks it (see the 'i' arm in po_create.go), so there has to be a
		// named way to go and ask again — a cache with no refresh is its own
		// silent-wrong-answer bug. Named in the bar, and it does exactly what
		// it says: the frame goes back to a working line because work really is
		// happening this time.
		//
		// itemSuppliersFor is deliberately LEFT set: it is what tells the
		// working frame this is a reload rather than a first look, and the
		// reply overwrites it either way. catalogAnswered() is false while
		// itemSuppliersLoad is up, so nothing reads it as an answer meanwhile.
		s.itemSuppliersLoad = true
		s.itemSuppliersErr = ""
		s.itemSuppliersNote.clear() // the working line speaks for this one
		return s, tea.Batch(
			Status("reloading what "+s.supplierLabel()+" sells…", StatusInfo),
			s.loadItemSuppliersForSupplier(),
		)
	case "enter":
		return s, s.commitHighlightedItem()
	}
	return s, nil
}

// commitSearchedItem is enter inside the item picker's SEARCH box, and the
// centre of the "it just kinda hangs there" report.
//
// It used to set typing=false, re-apply the filter and return nil. Every one of
// those is invisible: the filter had already been applied on the keystroke
// before, so the redraw was byte-for-byte what was already on screen — same
// rows, same caret still blinking in the search box. The pick needed a SECOND
// enter, which nothing on the screen said. The operator's model ("enter selects
// the item") was the right one; the screen's ("enter closes the box") was never
// stated anywhere.
//
// So enter now selects, with the one exception that protects the cart:
//
//   - exactly one row matches → take it. This is also the SCANNER path, since a
//     barcode arrives as a burst of runes plus enter, and a scan that resolves
//     to one item must be one press.
//   - several match → do NOT guess which. Close the box, hand j/k back, and say
//     how many matched and what to press. The screen visibly changes and names
//     the next key, which is the whole difference from the old behaviour.
//   - none match → keep the box OPEN and focused so the query can be edited in
//     place, and say what was searched for. This is the state that used to be a
//     dead end: enter over an empty list returned nil forever, and no key on the
//     screen could reach the item.
func (s *PurchaseOrderCreateScreen) commitSearchedItem() tea.Cmd {
	if !s.itemListOnScreen() {
		return s.catalogVerdictNote("searched again")
	}
	s.applyItemSupplierFilter()
	switch {
	case len(s.itemSuppliers) == 0:
		// Through the gate, not around it: with the walk still out this says so
		// rather than concluding "no match … (N in catalog)" about a catalog
		// that has not finished arriving.
		//
		// The lead is here for the same reason it is on the multi-match arm
		// below, and this arm is where the original report survived longest:
		// the box STAYS OPEN on no-match, so enter changed the cursor position
		// not at all, the rows not at all, and the note not at all — the filter
		// had already been applied on the keystroke before, so
		// reportItemFilterState("") re-emitted the note character for character.
		// The only moving thing on the pane was the caret, which is precisely
		// what "it just kinda hangs there" describes. "searched again" is what
		// the key DID; the clause after it is the outcome.
		s.itemSuppliersCur = 0
		return s.reportItemFilterState("searched again")
	case len(s.itemSuppliers) == 1:
		s.itemSuppliersTyping = false
		s.itemSuppliersSearch.Blur()
		s.itemSuppliersCur = 0
		return s.pickItemSupplier(0)
	default:
		s.itemSuppliersTyping = false
		s.itemSuppliersSearch.Blur()
		if s.itemSuppliersCur < 0 || s.itemSuppliersCur >= len(s.itemSuppliers) {
			s.itemSuppliersCur = 0
		}
		// The lead is the whole point. Without it this arm produced the note
		// the screen was ALREADY showing — same count, same query, same keys —
		// so the only thing that changed was the caret leaving the search box,
		// and "the screen just hangs after I press enter" survived on the
		// multi-match path inside its own fix.
		return s.reportItemFilterState("too many to pick")
	}
}

// commitHighlightedItem is enter over the item list itself. The empty case is
// the other half of the dead end: with no rows there is nothing to pick, and
// answering that with nil is how the picker told the operator nothing at all.
func (s *PurchaseOrderCreateScreen) commitHighlightedItem() tea.Cmd {
	if !s.itemListOnScreen() {
		return s.catalogVerdictNote("nothing to pick")
	}
	if len(s.itemSuppliers) == 0 {
		return s.reportItemFilterState("nothing to pick")
	}
	if s.itemSuppliersCur < 0 || s.itemSuppliersCur >= len(s.itemSuppliers) {
		s.itemSuppliersCur = 0
	}
	return s.pickItemSupplier(s.itemSuppliersCur)
}

// pickItemSupplier stages row i as the line under construction and says which
// item it took. Naming the item matters more here than anywhere else on the
// screen: the operator pressed enter over a filtered list, and "which of the
// rows did it take" is the question the old silence left open.
func (s *PurchaseOrderCreateScreen) pickItemSupplier(i int) tea.Cmd {
	row := s.itemSuppliers[i]
	id := row.ID
	unitCost := 0.0
	if v, err := strconv.ParseFloat(string(row.UnitCost), 64); err == nil {
		unitCost = v
	}
	pkgCost := 0.0
	if v, err := strconv.ParseFloat(string(row.PackageCost), 64); err == nil {
		pkgCost = v
	}
	desc := row.ItemName
	if desc == "" {
		desc = row.SupplierSKU
	}
	// PackQuantity (quantity_per_package) drives the case-cost toggle: when
	// > 1 the line form offers per-case entry prefilled from package_cost
	// (deriving unit_cost = case_cost / qpp — op-7j8v).
	s.enterLinePhase(&id, nil, desc, 1, unitCost, pkgCost, row.PackQuantity)
	s.itemSuppliersNote.clear() // the picker is behind us; the line form speaks now
	label := desc
	if label == "" {
		label = fmt.Sprintf("item-supplier #%d", id)
	}
	return tea.Batch(
		Status("picked "+label+" — set quantity and cost, enter adds the line", StatusOK),
		textinput.Blink,
	)
}

// catalogAnswered reports whether the picker actually HAS an answer about this
// supplier's catalog, as opposed to merely holding an empty slice.
//
// itemSuppliersFor is the flag that means it: it is set only by a reply that
// arrived for the supplier the order is on. An empty itemSuppliersAll is
// equally true while the walk is in flight (which is now N sequential page
// requests, so the window is wide) and after it failed, and neither of those is
// the fact "this supplier sells nothing".
func (s *PurchaseOrderCreateScreen) catalogAnswered() bool {
	return s.supplierID > 0 &&
		s.itemSuppliersFor == s.supplierID &&
		!s.itemSuppliersLoad &&
		s.itemSuppliersErr == ""
}

// catalogVerdictNote answers a key that needed the catalog when there are no
// rows to give it — and says which of the four reasons that is.
//
// "Found nothing" and "could not tell" are different facts and only one of them
// is safe to act on. This is the same distinction the loaded path draws; drawn
// here too, because a key pressed mid-walk used to report the conclusion of a
// walk that had not finished.
// prefix leads with what the key DID. Without it this was the last way the
// reported hang could still be produced: the typing branch sets the note to
// catalogVerdict("") on every keystroke, so enter mid-walk answered with the
// note the rune before it had already drawn — same text, same level, same
// working line above it, and a focused textinput the enter arm never touches.
func (s *PurchaseOrderCreateScreen) catalogVerdictNote(prefix string) tea.Cmd {
	s.itemSuppliersNote = s.catalogVerdict(prefix)
	return Status(s.itemSuppliersNote.flash(), s.itemSuppliersNote.level)
}

// catalogVerdict is the same four-way answer as a pickerNote, without posting
// it. The typing path needs the wording on every keystroke but must not fire a
// status flash per rune, so the note and the flash are separated here.
func (s *PurchaseOrderCreateScreen) catalogVerdict(prefix string) pickerNote {
	way := pickerWayOut
	if s.itemSuppliersTyping {
		way = searchBoxWayOut
	}
	// The lead travels down the verdict path too, not just the filter path.
	// itemFilterOrVerdict used to drop it here, so esc out of the search box
	// mid-walk answered with the same "still looking up…" the last keystroke
	// had already left on the pane — a query still in the box, the same line
	// under it, and only the caret leaving. Same for enter over a catalog that
	// really is empty.
	lead := ""
	if prefix != "" {
		lead = prefix + " · "
	}
	switch {
	case s.itemSuppliersLoad:
		return pickerNote{lead + "still looking up the items " + s.supplierLabel() + " sells…", StatusInfo}
	case s.itemSuppliersErr != "":
		// r is a letter going into the query while the box is open, so it is
		// only named when it is really the retry.
		if s.itemSuppliersTyping {
			return pickerNote{lead + "the catalog lookup failed\n" + searchBoxWayOut, StatusError}
		}
		return pickerNote{lead + "the catalog lookup failed\nr retries the lookup · " + pickerWayOut, StatusError}
	case !s.catalogAnswered():
		if s.itemSuppliersTyping {
			return pickerNote{lead + "this supplier's catalog has not been looked up yet\n" + searchBoxWayOut, StatusWarn}
		}
		return pickerNote{lead + "this supplier's catalog has not been looked up yet\nr looks it up · " + pickerWayOut, StatusWarn}
	}
	return pickerNote{lead + s.noCatalogSentence() + "\n" + way, StatusWarn}
}

// reportItemFilterState is the note for "the filter changed and nothing was
// picked". prefix, when given, leads with what the key did.
func (s *PurchaseOrderCreateScreen) reportItemFilterState(prefix string) tea.Cmd {
	s.itemSuppliersNote = s.itemFilterOrVerdict(prefix)
	return Status(s.itemSuppliersNote.flash(), s.itemSuppliersNote.level)
}

// itemFilterOrVerdict is the ONE place a filter outcome is worded, and the one
// gate in front of it: a filter result is only a fact once the catalog walk has
// answered. Filtering a slice that is empty because the request has not come
// back yet reads out as `no match for "w" (0 in catalog)` — a conclusion about
// a catalog nobody has seen, which is the found-nothing / could-not-tell
// conflation catalogVerdict exists to close. Both the live count typed into the
// box and the note esc leaves behind come through here so neither can drift
// past the gate on its own.
func (s *PurchaseOrderCreateScreen) itemFilterOrVerdict(prefix string) pickerNote {
	// Not answered, or answered with nothing: neither is a filter outcome.
	// The emptiness test is not enough on its own — an 'r' reload leaves the
	// previous rows in place while the walk is out, so a guard written as
	// len(itemSuppliersAll) == 0 sails past a mid-reload frame and words a
	// verdict about a request that has not come back.
	if !s.catalogAnswered() || len(s.itemSuppliersAll) == 0 {
		return s.catalogVerdict(prefix)
	}
	return itemFilterNote(
		strings.TrimSpace(s.itemSuppliersSearch.Value()),
		len(s.itemSuppliers), len(s.itemSuppliersAll), prefix, s.itemSuppliersTyping)
}

// itemFilterNote words the three outcomes of a filter. A zero-match note names
// the query AND the catalog size, because those two facts together are what
// tell the operator whether to retype or to conclude this supplier does not
// sell the thing — "No inventory items match" alone said neither, and said it
// about a catalog that (before ListItemSuppliersForSupplier paged) might not
// even have been fully loaded.
//
// typing is which of two screens this note is going onto, and EVERY arm needs
// it, not just the zero-match one. These notes are rendered in both states and
// the keys differ completely between them: with the box open j and k are
// characters going into the query and enter only picks when exactly one row
// matches; with it shut j/k move the highlight, enter picks it, and esc cancels
// the whole purchase order. Gating one arm and leaving the other two is how the
// ORDINARY search path — type a few letters, see eleven matches — ended up
// naming three keys of which two did something else.
func itemFilterNote(query string, matched, total int, prefix string, typing bool) pickerNote {
	lead := ""
	if prefix != "" {
		lead = prefix + " · "
	}
	q := strconv.Quote(pickerClip(query, 16))
	switch {
	case query == "":
		if typing {
			return pickerNote{
				fmt.Sprintf("%s%d item(s) · type to narrow · esc closes the search", lead, total),
				StatusInfo,
			}
		}
		return pickerNote{fmt.Sprintf("%s%d item(s) · enter picks the highlighted row", lead, total), StatusInfo}
	case matched == 0:
		// The lead is kept here too: esc having just closed the box is the
		// thing the operator most needs acknowledged on this frame, and
		// dropping it was why nothing on screen answered that press.
		tail := "/ edits the search · b picks another source"
		if typing {
			tail = "edit the search to widen it"
		}
		return pickerNote{
			fmt.Sprintf("%sno match for %s (%d in catalog)\n%s", lead, q, total, tail),
			StatusWarn,
		}
	case matched == 1:
		// The one arm whose wording holds in both states: a single match is
		// taken by enter inside the box (the scanner path) and by enter over
		// the one-row list alike.
		return pickerNote{fmt.Sprintf("%s1 of %d match %s · enter picks it", lead, total, q), StatusOK}
	}
	if typing {
		return pickerNote{
			fmt.Sprintf("%s%d of %d match %s · keep typing to narrow\nenter closes the search and hands j/k back", lead, matched, total, q),
			StatusOK,
		}
	}
	return pickerNote{
		fmt.Sprintf("%s%d of %d match %s · j/k choose · enter picks", lead, matched, total, q),
		StatusOK,
	}
}

func (s *PurchaseOrderCreateScreen) renderItemPick() string {
	var b strings.Builder
	if s.itemSuppliersSearch.Value() != "" || s.itemSuppliersTyping {
		b.WriteString(StyleMuted.Render(poItemFilterLabel) + s.itemSuppliersSearch.View() + "\n\n")
	}
	if s.itemSuppliersLoad {
		// Name the WORK, not the wait. "Loading…" tells the operator a
		// rectangle is busy; this tells them which request is out and against
		// whom, which is the difference between a status line and a spinner.
		//
		// The working line is UNCONDITIONAL here. It used to defer to a note
		// when one was set, which let a key pressed mid-walk paint its own
		// answer over the frame — and the answer a key gets while the catalog
		// is empty-because-unfetched used to be "this supplier has no catalog",
		// so the operator was told the conclusion of a walk still in flight.
		verb := "Looking up"
		if s.itemSuppliersFor == s.supplierID && s.supplierID > 0 {
			verb = "Reloading"
		}
		b.WriteString(pickerHint(verb + " the items " + s.supplierLabel() + " sells…"))
		// …and the note goes UNDER it, not instead of it. A key pressed while
		// the walk is out declines and says why (catalogVerdictNote), but this
		// branch used to return before anything drew that answer, so the press
		// left the pane byte-for-byte unchanged — the original hang, one state
		// over. The working line still speaks first: it is the fact, the note
		// is the reply to the key.
		if note := s.itemSuppliersNote.render(); note != "" {
			b.WriteString("\n" + note)
		}
		return b.String()
	}
	if s.itemSuppliersErr != "" {
		// Same reason as the loading branch above: the failure frame is durable,
		// so without the note a key pressed on it answered only into the
		// four-second status flash and the body never moved.
		//
		// Keys ABOVE the reply, as on the supplier-switch confirm: clampToBox
		// drops from the bottom, and of these two lines the one that must
		// survive a short terminal is the one naming r/b/esc. Both are built
		// FIRST so the unbounded error detail is budgeted against what they
		// leave, rather than the other way round.
		tail := pickerHint(s.itemPickBar())
		if note := s.itemSuppliersNote.render(); note != "" {
			tail += "\n" + note
		}
		b.WriteString(pickerFail("looking up this supplier's items failed", s.itemSuppliersErr,
			s.bodyRowBudget(poRenderedRows(b.String())+poRenderedRows(tail))) + "\n")
		b.WriteString(tail)
		return b.String()
	}
	if len(s.itemSuppliers) == 0 {
		// Never the bare "No inventory items match." this used to print: that
		// sentence is the same whether the supplier sells nothing, the search
		// missed, or the catalog failed to load, and it names no key out.
		if note := s.itemSuppliersNote.render(); note != "" {
			b.WriteString(note)
		} else {
			b.WriteString(pickerHint(s.noCatalogSentence() + "."))
		}
		b.WriteString("\n" + pickerHint(s.itemPickBar()))
		return b.String()
	}
	if note := s.itemSuppliersNote.render(); note != "" {
		b.WriteString(note + "\n\n")
	}
	b.WriteString(renderWindowedList(
		len(s.itemSuppliers), s.itemSuppliersCur, s.bodyRowBudget(poRenderedRows(b.String())),
		func(i, room int) string {
			it := s.itemSuppliers[i]
			sku := it.SupplierSKU
			if sku == "" {
				sku = "—"
			}
			cost := ""
			if it.UnitCost != "" {
				cost = "  " + StyleMuted.Render(fmt.Sprintf("@ %s", it.UnitCost))
			}
			// Flag case-packed items so the operator knows a case-cost entry
			// will be offered on the line form (op-7j8v).
			pack := ""
			if it.PackQuantity > 1 {
				pack = "  " + StyleStatusOK.Render(fmt.Sprintf("case ×%d", it.PackQuantity))
			}
			lead := ""
			if it.LeadTimeDays > 0 {
				lead = "  " + StyleMuted.Render(fmt.Sprintf("lead %gd", it.LeadTimeDays))
			}
			// The SKU and the price are what the row is picked ON, so they are
			// facts; the case and lead-time flags are context and go first.
			return poFitRow(room, it.ItemName, "  "+sku+cost, pack, lead)
		},
	))
	return b.String()
}

// ---------------------------------------------------------------------------
// Phase 3c: Assets-from-supplier picker (server-side search + pagination)
// ---------------------------------------------------------------------------

func (s *PurchaseOrderCreateScreen) updateAssetPickPhase(m tea.KeyMsg) (Screen, tea.Cmd) {
	if s.assetsTyping {
		switch m.Type {
		case tea.KeyEsc:
			s.assetsTyping = false
			s.assetsSearch.Blur()
			return s, s.assetsSearchClosedNote()
		case tea.KeyEnter:
			if s.assetsLoading {
				// A search is already out. The reply's generation now drops
				// the loser of a race outright, but firing a second search is
				// still the wrong answer to this key: it would leave the
				// operator watching a lookup whose result is discarded, and it
				// is the sequence that used to leave the rows on screen
				// belonging to a query the search box no longer held.
				//
				// This declines rather than cancelling, which is why the
				// typing arm of assetPickBar stops naming enter here: a key
				// that cannot act must not be advertised, and the note says
				// what is happening instead of the screen sitting still.
				return s, s.assetVerdictNote("searched again")
			}
			// Unlike the item picker this really does go off the terminal, so
			// the note says so BEFORE the request leaves: the reply repaints it
			// with the result (handlePickerLoaded), and a slow or failed lookup
			// leaves the operator reading "searching…" rather than a screen that
			// has not moved.
			s.assetsTyping = false
			s.assetsSearch.Blur()
			s.assetsPage = 1
			s.assetsLoading = true
			s.assetsErr = ""
			s.assetsQuery = s.assetsSearch.Value()
			q := strings.TrimSpace(s.assetsQuery)
			what := "this supplier's assets"
			if q != "" {
				what = strconv.Quote(q)
			}
			return s, tea.Batch(
				s.assetsNote.say("searching "+what+"…", StatusInfo),
				s.loadAssetsForSupplier(s.assetsQuery),
			)
		}
		var cmd tea.Cmd
		s.assetsSearch, cmd = s.assetsSearch.Update(m)
		return s, cmd
	}
	if !s.assetListOnScreen() {
		switch m.String() {
		case "j", "down", "k", "up":
			return s, s.assetVerdictNote(m.String() + " moves nothing")
		case "enter":
			return s, s.assetVerdictNote("nothing to pick")
		}
	}
	switch m.String() {
	case "esc":
		return s, SwitchTo(WSPurchasing, nil)
	case "b":
		s.phase = poPhaseSource
		return s, nil
	case "j", "down":
		if len(s.assets) == 0 {
			return s, s.assetEmptyNote(m.String() + " moves nothing")
		}
		if s.assetsCursor < len(s.assets)-1 {
			s.assetsCursor++
		}
	case "k", "up":
		if len(s.assets) == 0 {
			return s, s.assetEmptyNote(m.String() + " moves nothing")
		}
		if s.assetsCursor > 0 {
			s.assetsCursor--
		}
	case "/":
		s.assetsTyping = true
		s.assetsSearch.Focus()
		// The bar names '/' on the working frame too, so the box can open with
		// a lookup still out — and there enter is gated. Promising the key the
		// box cannot run is the same false claim the bar just stopped making.
		opened := "type to search · enter runs the search"
		if s.assetsLoading {
			opened = "type to search · " + searchBoxWayOut
		}
		return s, tea.Batch(
			s.assetsNote.say(opened, StatusInfo),
			textinput.Blink,
		)
	case "]":
		// The two paging keys are named only alongside a page that exists, so
		// the bar stays honest — but the arm still has to answer when the state
		// moved underneath the operator between the render and the press.
		if !s.assetListOnScreen() {
			// Neither the working frame nor the failure frame names them, and
			// stepping the page over one that just FAILED means the retry
			// silently skips it. Each pager key names ITSELF: with one page
			// loaded both decline, and a shared sentence would make the second
			// press redraw the pane the first one left.
			return s, s.assetVerdictNote("] stays on this page")
		}
		if !s.assetsHasNext {
			return s, s.assetsNote.say(fmt.Sprintf("already on the last page (page %d)", s.assetsPage), StatusWarn)
		}
		s.assetsPage++
		s.assetsLoading = true
		s.assetsErr = ""
		// assetsQuery, never the live box: it can hold text nobody submitted,
		// and paging with that would answer a key that asked for the next page
		// with a search the operator never ran.
		return s, tea.Batch(
			s.assetsNote.say(fmt.Sprintf("loading page %d…", s.assetsPage), StatusInfo),
			s.loadAssetsForSupplier(s.assetsQuery),
		)
	case "[":
		if !s.assetListOnScreen() {
			return s, s.assetVerdictNote("[ stays on this page")
		}
		if s.assetsPage <= 1 {
			return s, s.assetsNote.say("already on the first page", StatusWarn)
		}
		s.assetsPage--
		s.assetsLoading = true
		s.assetsErr = ""
		return s, tea.Batch(
			s.assetsNote.say(fmt.Sprintf("loading page %d…", s.assetsPage), StatusInfo),
			s.loadAssetsForSupplier(s.assetsQuery),
		)
	case "enter":
		if len(s.assets) == 0 {
			return s, s.assetEmptyNote("nothing to pick")
		}
		if s.assetsCursor < 0 || s.assetsCursor >= len(s.assets) {
			s.assetsCursor = 0
		}
		a := s.assets[s.assetsCursor]
		// Asset.ID is a polyglot `any` (UUID strings + int rows both
		// occur in inventory). Convert to a string for the PO create
		// payload — backend's asset_id accepts the string repr.
		idStr := fmt.Sprintf("%v", a.ID)
		desc := a.Name
		if a.AssetTag != "" {
			desc = fmt.Sprintf("%s (%s)", a.Name, a.AssetTag)
		}
		// Assets are not case-packed (qpp 0): single per-unit cost, unchanged.
		s.enterLinePhase(nil, &idStr, desc, 1, 0, 0, 0)
		s.assetsNote.clear()
		return s, tea.Batch(
			Status("picked "+desc+" — set quantity and cost, enter adds the line", StatusOK),
			textinput.Blink,
		)
	}
	return s, nil
}

// assetEmptyNote answers any key that acts on a row when the picker is drawing
// a list with no rows in it. Every such key gets the SAME sentence with a
// different lead: enter, j and k all used to reach separate arms, and the two
// cursor arms answered with nil — a byte-for-byte identical pane, which is the
// reported hang's exact shape one key over from where it was reported.
//
// It words the outcome from assetsQuery, never the live box: that query is what
// the rows on screen actually answer, and a box the operator typed into without
// pressing enter has run nothing.
func (s *PurchaseOrderCreateScreen) assetEmptyNote(lead string) tea.Cmd {
	if lead != "" {
		lead += " · "
	}
	if q := strings.TrimSpace(s.assetsQuery); q != "" {
		return s.assetsNote.say(lead+"no asset matches "+strconv.Quote(pickerClip(q, 16))+
			"\n/ edits the search · b picks another source", StatusWarn)
	}
	return s.assetsNote.say(lead+"this supplier has no assets on file\n"+pickerWayOut, StatusWarn)
}

// assetsSearchClosedNote answers esc out of the asset search box. It has to
// branch on what is actually in the list: the unconditional "j/k move · enter
// picks" it used to post names three keys that do nothing over an empty one —
// j/k hit the no-op cursor guards and enter answers "no asset matches" — which
// is the bar-honesty rule broken by the frame's own note. The item picker's esc
// path already branches this way through reportItemFilterState.
func (s *PurchaseOrderCreateScreen) assetsSearchClosedNote() tea.Cmd {
	// The rows this note counts are only an ANSWER once the lookup has come
	// back. esc can close the box with a search still out or after one failed —
	// the frame keeps the rows it was showing either way — and concluding "this
	// supplier has no assets on file" there is a verdict about a request nobody
	// has seen. That used to expire with the status flash; the working and
	// failure frames now DRAW their note, so it would be a false line sitting
	// on the pane.
	if !s.assetListOnScreen() {
		return s.assetVerdictNote("search closed")
	}
	// Text in the box that was never submitted is not a result. This search is
	// server-side and runs only on enter, so the rows below answer assetsQuery
	// and say nothing at all about what is typed here — concluding "no asset
	// matches X" would be found-nothing where could-not-tell is the fact, and
	// it would point at '/' to retype when the supplier may simply have none.
	if q, ran := strings.TrimSpace(s.assetsSearch.Value()), strings.TrimSpace(s.assetsQuery); q != ran {
		// This note is only ever drawn with the box SHUT, so enter is the key
		// that STAGES the highlighted row here, not the one that runs a search.
		// Naming it "enter runs it" pointed the operator who wanted a search at
		// the key that puts an unrelated line on their purchase order, and the
		// bar four rows above said "enter picks" at the same time.
		typed := strconv.Quote(pickerClip(q, 16)) + " was never run"
		if q == "" {
			typed = "the box was emptied without running"
		}
		// …and say what the rows on the pane DO answer, which means asking
		// whether any came back. Reading assetsQuery alone put "the rows still
		// answer \"Lathe\"" on a frame with no rows at all, and because this
		// note IS the body of the empty frame it replaced the one line that
		// said the search for "Lathe" had found nothing.
		var answers string
		switch {
		case len(s.assets) == 0 && ran != "":
			answers = "no asset matches " + strconv.Quote(pickerClip(ran, 16))
		case len(s.assets) == 0:
			answers = "this supplier has no assets on file"
		case ran != "":
			answers = "the rows still answer " + strconv.Quote(pickerClip(ran, 16))
		default:
			answers = "the rows are this supplier's whole list"
		}
		return s.assetsNote.say(
			"search closed · "+typed+"\n"+answers+" · / reopens the search", StatusWarn)
	}
	if len(s.assets) > 0 {
		return s.assetsNote.say(
			fmt.Sprintf("search closed · %d asset(s) · j/k move · enter picks", len(s.assets)), StatusInfo)
	}
	if q := strings.TrimSpace(s.assetsQuery); q != "" {
		return s.assetsNote.say(
			"search closed · no asset matches "+strconv.Quote(pickerClip(q, 16))+
				"\n/ edits the search · b picks another source", StatusWarn)
	}
	return s.assetsNote.say(
		"search closed · this supplier has no assets on file\n/ searches · b picks another source", StatusWarn)
}

func (s *PurchaseOrderCreateScreen) renderAssetPick() string {
	var b strings.Builder
	// The label is the first thing an operator reads to know WHAT a list is, so
	// it has to name the query the rows answer — assetsQuery — and never the
	// live textinput. Reading the box let an uncommitted esc plus a page draw
	// "search: hovercraft" over an unfiltered page 2 with a green tick and a
	// count, and the only line that said the query was never run had by then
	// been overwritten by the reply's own note.
	draft := strings.TrimSpace(s.assetsSearch.Value())
	ran := strings.TrimSpace(s.assetsQuery)
	switch {
	case s.assetsTyping:
		// Being edited, so the box is one of TWO subjects and cannot be the
		// only label: the rows underneath still answer assetsQuery, and this
		// branch used to draw `search: hovercraft` over an unfiltered list
		// while nothing had ever been searched for. Same two-row shape the
		// shut-box branch below builds, with the live textinput in place of
		// the frozen draft so the caret is where the operator is typing.
		shown := "all of this supplier's assets"
		if ran != "" {
			shown = strconv.Quote(pickerClip(ran, 16))
		}
		b.WriteString(StyleMuted.Render("showing: "+shown) + "\n")
		b.WriteString(StyleMuted.Render(poAssetSearchLabel) + s.assetsSearch.View() + "\n\n")
	case draft != ran:
		shown := "all of this supplier's assets"
		if ran != "" {
			shown = strconv.Quote(pickerClip(ran, 16))
		}
		b.WriteString(StyleMuted.Render("showing: "+shown) + "\n")
		if draft != "" {
			b.WriteString(StyleMuted.Render("search (not run): "+pickerClip(draft, 16)) + "\n")
		}
		b.WriteString("\n")
	case ran != "":
		b.WriteString(StyleMuted.Render(poAssetSearchLabel) + s.assetsSearch.View() + "\n\n")
	}
	if s.assetsLoading {
		b.WriteString(pickerHint("Looking up the assets " + s.supplierLabel() + " supplied…"))
		// The note goes UNDER the working line, never instead of it — the same
		// split the item picker's loading frame makes. Every key this frame
		// does not name now declines through assetVerdictNote, INCLUDING enter
		// inside the search box, and a decline the frame does not draw is a
		// keypress that changes nothing: the four-second status flash expires
		// and the operator who missed it is still looking at a screen that has
		// not moved.
		if note := s.assetsNote.render(); note != "" {
			b.WriteString("\n" + note)
		}
		return b.String()
	}
	// Same gate as the item picker: with the search box open, '/' is a slash in
	// the query, 'b' is a letter, and esc closes the box rather than the order.
	if s.assetsErr != "" {
		// Keys above the reply: clampToBox drops from the bottom, and of these
		// two lines the one that must survive a short terminal is the one
		// naming the way out. Measured first so the error detail is what the
		// budget trims.
		tail := pickerHint(s.assetPickBar())
		if note := s.assetsNote.render(); note != "" {
			tail += "\n" + note
		}
		b.WriteString(pickerFail("looking up this supplier's assets failed", s.assetsErr,
			s.bodyRowBudget(poRenderedRows(b.String())+poRenderedRows(tail))) + "\n")
		b.WriteString(tail)
		return b.String()
	}
	if len(s.assets) == 0 {
		if note := s.assetsNote.render(); note != "" {
			b.WriteString(note)
		} else {
			b.WriteString(pickerHint(s.supplierLabel() + " has no assets on file."))
		}
		b.WriteString("\n" + pickerHint(s.assetPickBar()))
		return b.String()
	}
	if note := s.assetsNote.render(); note != "" {
		b.WriteString(note + "\n\n")
	}
	// The pager is built BEFORE the list so the list can be budgeted against
	// it. It is drawn after, and a block sized without counting what follows it
	// pushes exactly that block off the bottom — here, the two paging keys the
	// frame names.
	pager := fmt.Sprintf("page %d", s.assetsPage)
	if s.assetsHasNext {
		pager += " · ] next"
	}
	if s.assetsPage > 1 {
		pager += " · [ prev"
	}
	tail := "\n" + pickerHint(pager)
	b.WriteString(renderWindowedList(
		len(s.assets), s.assetsCursor,
		s.bodyRowBudget(poRenderedRows(b.String())+poRenderedRows(tail)),
		func(i, room int) string {
			a := s.assets[i]
			tag := a.AssetTag
			if tag == "" {
				tag = "—"
			}
			serial := ""
			if a.SerialNumber != "" {
				serial = "  " + StyleMuted.Render("s/n "+a.SerialNumber)
			}
			// The asset TAG is what the machine is called on the shop floor, so
			// it keeps its cells; the serial is the piece that gives.
			return poFitRow(room, a.Name, "  "+tag, serial)
		},
	))
	b.WriteString(tail)
	return b.String()
}

// ---------------------------------------------------------------------------
// Shared windowed-list renderer
// ---------------------------------------------------------------------------

// windowedListDefaultRows is the block height a caller that has not measured
// its pane gets. It is the old fixed ten-row window plus its two markers, so an
// unbudgeted caller draws exactly what it always did.
const windowedListDefaultRows = 12

// renderWindowedList draws `total` items via the supplied formatter, keeping
// `cursor` on screen inside a block of `rows` terminal lines — MARKERS
// INCLUDED. Same pattern as the supplier picker so all four pickers look
// consistent, and a free function rather than a method on the create screen,
// because the PO edit screen's association pickers draw their lists the same
// way.
//
// rows is a budget, not a preference: `clampToBox` drops whatever runs past the
// bottom of the pane, and a row it drops out of a PICKER is a row the cursor
// can still be moved onto and enter can still stage. An item going onto a
// purchase order that the operator cannot see is a wrong purchase order, so a
// list that does not fit says how many rows it hid rather than losing them
// silently — and the markers that say it are counted inside the budget, not
// added on top of it. rows <= 0 keeps the historic ten.
//
// The formatter is handed the CELLS its row may draw into as well as the index,
// because the horizontal cut is the same defect as the vertical one: a row
// clampToBox trims loses its right-hand end — the SKU and the price an item is
// picked on — with no mark to say it happened. The room is computed once here
// (windowedListRoom) rather than by each formatter, so no picker can be the one
// that forgets the caret or the highlight.
func renderWindowedList(total, cursor, rows int, formatRow func(i, room int) string) string {
	if rows <= 0 {
		rows = windowedListDefaultRows
	}
	if rows < 3 {
		rows = 3
	}
	// Fit the item rows and their markers together. Reserving a marker shrinks
	// the window, which can move it to an edge and remove the need for that
	// marker, so this settles rather than assuming: at most two passes change
	// anything, and a spare row left over beats a clipped one.
	visible, start, end := rows, 0, 0
	for i := 0; i < 3; i++ {
		start, end = windowedListSpan(total, cursor, visible)
		markers := 0
		if start > 0 {
			markers++
		}
		if end < total {
			markers++
		}
		if visible+markers <= rows {
			break
		}
		if visible = rows - markers; visible < 1 {
			visible = 1
		}
	}

	var b strings.Builder
	if start > 0 {
		// The newline stays OUTSIDE Render: lipgloss treats a styled string
		// containing one as a two-line block and pads the short line, which
		// leaked twenty columns of padding onto the row underneath the marker
		// and pushed that row past the 51-column cut.
		b.WriteString(StyleMuted.Render(fmt.Sprintf("  ↑ %d more above", start)) + "\n")
	}
	room := windowedListRoom()
	for i := start; i < end; i++ {
		caret := "    "
		if i == cursor {
			caret = "  ▸ "
		}
		line := caret + formatRow(i, room)
		if i == cursor {
			line = StyleSidebarItemActive.Render(line)
		}
		b.WriteString(line + "\n")
	}
	if end < total {
		b.WriteString(StyleMuted.Render(fmt.Sprintf("  ↓ %d more below", total-end)) + "\n")
	}
	return b.String()
}

// windowedListCaretCells is the fixed gutter every row carries — four cells,
// highlighted ("  ▸ ") or not ("    ").
const windowedListCaretCells = 4

// windowedListRoom is the cells a formatted row may draw into.
//
// The HIGHLIGHT's padding is reserved on every row, not only the highlighted
// one: StyleSidebarItemActive pads what it wraps, so a row that fits until it
// is selected is a row the pane cuts on exactly the press that stages it — and
// the padding is asked of the style rather than counted, the same way
// renderCart asks.
func windowedListRoom() int {
	room := pickerPaneWidth - windowedListCaretCells - StyleSidebarItemActive.GetHorizontalPadding()
	if room < poHeaderValueFloor {
		room = poHeaderValueFloor
	}
	return room
}

// poFitRow assembles one windowed-list row inside `room` cells with a STATED
// order of sacrifice, the same shape the cart row gives its own parts.
//
// name is the only piece that may be ABBREVIATED — it is the identifier, and a
// shortened one is still recognisable beside the code the operator typed.
// facts never give: they are the SKU, the price and the quantity, the numbers a
// picker exists to be read for, and a number cut by clampToBox is worse than an
// absent one because "@ 3." reads as a whole price. trailers are the row's
// decorations and are dropped from the LAST one backwards, keeping the columns
// that remain in the order they were written — column position is how a
// columnar row is read.
//
// Whatever it shortens says so: the name keeps pickerClip's ellipsis, and a
// dropped trailer leaves one of its own at the end of the row, so a row that
// gave something up never reads as a whole one.
func poFitRow(room int, name, facts string, trailers ...string) string {
	need := lipgloss.Width(name)
	if need > poHeaderValueFloor {
		need = poHeaderValueFloor
	}
	tail := func(n int) string { return strings.Join(trailers[:n], "") }
	keep, dropped := len(trailers), ""
	for keep > 0 {
		spent := need + lipgloss.Width(facts) + lipgloss.Width(tail(keep)) + lipgloss.Width(dropped)
		if spent <= room {
			break
		}
		keep--
		// Spaced off the column before it: an ellipsis butted against the last
		// surviving fact reads as THAT fact having been cut, which is the
		// mangled-value defect this bound exists to stop.
		dropped = "  …"
	}
	suffix := facts + tail(keep) + dropped
	space := room - lipgloss.Width(suffix)
	if space < need {
		space = need
	}
	return pickerClip(name, space) + suffix
}

// windowedListSpan centres a window of `size` item rows on cursor.
func windowedListSpan(total, cursor, size int) (start, end int) {
	if size >= total {
		return 0, total
	}
	start = cursor - size/2
	if start < 0 {
		start = 0
	}
	end = start + size
	if end > total {
		end = total
		start = end - size
		if start < 0 {
			start = 0
		}
	}
	return start, end
}
