// Storage reservation slots — the browsable racking (list surface).
//
// A slot is one addressable place in the project-storage pallet racking, named
// by the code printed on its card: `1A1` = rack 1, level A, position 1. This
// screen is the warden's view of the racking — browse a rack, see at a glance
// what is free and who is in the rest, and drive the four things a warden
// actually does with the layout: add a slot, generate a whole rack, retire or
// delete one, and print the cards that go on the uprights.
//
// There is NO web counterpart (the OMS frontend has no storage-slot page at
// all), so the serializer + viewset are the parity target — see
// internal/omsapi/storage_slots.go for the verified contract. Every verb is
// gated IsStorageAdminOrStaff server-side; the screen mounts under Facilities,
// which is already staff-only, and a 403 still surfaces as a clean message.
//
// Keys: the whole movement vocabulary · enter open · n new slot · b generate a
// rack · E edit · x delete · space select for printing · c clear selection ·
// p print cards · f cycle the occupancy filter · / scope to a rack (or a rack +
// level) · r refresh. The bar is a RECORD (proseBar, prose_bar.go) naming
// exactly the ones that act where the cursor is. n/G/f// collide with global
// hotkeys and are claimed via HandlesKey; the screen goes raw-input only while
// an overlay is up.
package tui

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/uid0/scantty/internal/omsapi"
)

// slotFilter is one entry in the `f` cycle. filters[0] is the UNFILTERED view,
// which keeps the landing screen identical to "everything" (the sc-mvqn
// ListScreen filter-cycle rule). The two axes are deliberately folded into one
// cycle rather than two independent toggles: these are the four questions a
// warden actually asks, and a matrix of five booleans is not one of them.
type slotFilter struct {
	label string
	query url.Values
}

var storageSlotFilters = []slotFilter{
	{label: "all", query: url.Values{}},
	// "What can I hand out?" — free AND in service; a retired slot is not on
	// offer even when nothing is sitting in it.
	{label: "free", query: url.Values{"occupied": {"false"}, "is_active": {"true"}}},
	{label: "occupied", query: url.Values{"occupied": {"true"}}},
	{label: "retired", query: url.Values{"is_active": {"false"}}},
}

type StorageSlotsScreen struct {
	deps Deps

	rows           []omsapi.StorageSlot
	cursor         int
	windowStart    int
	windowSize     int
	loading        bool
	loadErr        string
	terminalWidth  int
	terminalHeight int

	filterIdx int

	// Rack scope, set from the `/` prompt. rack 0 means "every rack"; level is
	// only meaningful with a rack.
	scopeRack  int
	scopeLevel string

	scoping    bool
	scopeInput textinput.Model
	scopeErr   string
	// scopeRefused is the value scopeErr refused, so the bar can tell a box
	// still holding it — where enter can only refuse again — from one that has
	// been edited since.
	scopeRefused string

	// Print selection, in TOGGLE order — the cards endpoint prints explicit ids
	// in the order they were given, which is what makes "reprint these four
	// scuffed cards" come off the printer the way the operator listed them.
	selection []int
	selected  map[int]bool

	confirmingDelete bool
	deleting         bool

	card slotCardPrompt
}

type storageSlotsLoadedMsg struct {
	rows []omsapi.StorageSlot
	err  error
}

type storageSlotDeletedMsg struct {
	code string
	err  error
}

type storageSlotCardsRenderedMsg struct {
	pdf      *omsapi.SlotCardPDF
	path     string
	fallback string
	err      error
}

func NewStorageSlotsScreen(deps Deps) *StorageSlotsScreen {
	ti := textinput.New()
	ti.Prompt = ""
	ti.CharLimit = 8
	ti.Placeholder = "rack, or rack+level (1, 1A) — blank for all"
	return &StorageSlotsScreen{
		deps:       deps,
		loading:    true,
		windowSize: 20,
		scopeInput: ti,
		selected:   map[int]bool{},
		card:       newSlotCardPrompt(),
	}
}

func (s *StorageSlotsScreen) Title() string { return "Storage Slots" }

// HandlesKey claims the keys that collide with global hotkeys — `n` (global
// notifications) for new, `G` (global categories) for bottom-of-list, `f`
// (global firmware) for the filter cycle and `/` (global search palette) for
// the rack scope. E/x/b/c/p/r/space/enter are not global hotkeys, so they reach
// us through the root's fall-through.
func (s *StorageSlotsScreen) HandlesKey(key string) bool {
	if s.WantsRawInput() {
		return false // raw input already routes every key here
	}
	return key == "n" || key == "G" || key == "f" || key == "/"
}

// WantsRawInput claims every key while an overlay is up (the rack-scope prompt,
// the delete confirm, the print prompt) so typed text, y/n and esc land here
// instead of the root's globals. The plain list stays non-raw so workspace
// switching keeps working.
func (s *StorageSlotsScreen) WantsRawInput() bool {
	return s.scoping || s.confirmingDelete || s.card.active
}

func (s *StorageSlotsScreen) ctx() context.Context {
	if s.deps.Ctx != nil {
		return s.deps.Ctx
	}
	return context.Background()
}

// query folds the rack scope and the active filter into one request. Cycling
// RE-FETCHES rather than filtering the rows in hand: occupancy is a server-side
// Exists() over live stints, and a slot absent from the loaded set could never
// be reached by a local filter.
func (s *StorageSlotsScreen) query() url.Values {
	q := url.Values{}
	for k, vs := range storageSlotFilters[s.filterIdx].query {
		for _, v := range vs {
			q.Add(k, v)
		}
	}
	if s.scopeRack > 0 {
		q.Set("rack", strconv.Itoa(s.scopeRack))
		if s.scopeLevel != "" {
			q.Set("level", s.scopeLevel)
		}
	}
	return q
}

func (s *StorageSlotsScreen) Init() tea.Cmd {
	deps := s.deps
	ctx := s.ctx()
	q := s.query()
	return func() tea.Msg {
		rows, err := deps.OMS.ListAllStorageSlots(ctx, q)
		return storageSlotsLoadedMsg{rows: rows, err: err}
	}
}

// computeWindowSize is how many rows a PAGE is: the body budget the frame
// draws the list into while the BAR is the foot, which is the only foot the
// pager is ever pressed under (every overlay owns the keyboard).
//
// IT USED TO BE `screenBodyHeight - 6`, a constant counting the header, the
// blank, a footer and "overlay room" together, and both halves of that were
// wrong: a footer that names what the screen binds is a folded record several
// rows tall at 80 columns, and slotCardPrompt's overlay took five, so a long list assembled a
// frame taller than the pane with the print prompt open and clampToBox took the
// prompt's own keys off the bottom. The two costs are separate now — the bar's
// here, measured off its ceiling, and an overlay's in foot, measured off what it
// draws — and neither is a number written down.
func (s *StorageSlotsScreen) computeWindowSize() int {
	cells := s.paneCells()
	return proseFlatListBudget(s.head(cells), s.terminalHeight, cells, s.ceilingBar())
}

// paneCells is the width this list folds, clips and budgets against.
func (s *StorageSlotsScreen) paneCells() int { return proseBarCells(s.terminalWidth) }

func (s *StorageSlotsScreen) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		s.terminalWidth, s.terminalHeight = m.Width, m.Height
		s.windowSize = s.computeWindowSize()
		s.scrollIntoView()
		return s, nil

	case storageSlotsLoadedMsg:
		s.loading = false
		if m.err != nil {
			s.loadErr = slotCardErrorText(m.err)
		} else {
			s.loadErr = ""
			s.rows = m.rows
		}
		if s.cursor >= len(s.rows) {
			s.cursor = 0
		}
		s.windowSize = s.computeWindowSize()
		s.scrollIntoView()
		return s, nil

	case storageSlotDeletedMsg:
		s.deleting = false
		s.confirmingDelete = false
		if m.err != nil {
			return s, Status("delete failed: "+slotCardErrorText(m.err), StatusError)
		}
		s.loading = true
		return s, tea.Batch(Status("slot "+m.code+" deleted", StatusOK), s.Init())

	case storageSlotCardsRenderedMsg:
		s.card.close()
		if m.err != nil {
			return s, Status("card render failed: "+slotCardErrorText(m.err), StatusError)
		}
		path, replaced, err := saveSlotCardPDF(m.path, m.pdf, m.fallback)
		if err != nil {
			return s, Status("could not write PDF: "+err.Error(), StatusError)
		}
		return s, Status(slotCardSavedSummary(path, len(m.pdf.Data), replaced), StatusOK)

	case tea.KeyMsg:
		switch {
		case s.scoping:
			return s.updateScope(m)
		case s.confirmingDelete:
			return s.updateConfirmDelete(m)
		case s.card.active:
			return s.updateCardPrompt(m)
		}
		return s.updateList(m)
	}

	// Blink and other non-key messages feed whichever textinput is up.
	if s.scoping {
		var cmd tea.Cmd
		s.scopeInput, cmd = s.scopeInput.Update(msg)
		return s, cmd
	}
	if s.card.active {
		var cmd tea.Cmd
		s.card.path, cmd = s.card.path.Update(msg)
		return s, cmd
	}
	return s, nil
}

func (s *StorageSlotsScreen) updateList(m tea.KeyMsg) (Screen, tea.Cmd) {
	if proseLoadKeyHidden(s.loading, s.loadErr, s.loadBar(), m.String()) {
		return s, nil
	}
	// The header folds, and selecting a slot can add a row to it, so a page is
	// asked of the frame as it stands rather than of the one last sized.
	s.windowSize = s.computeWindowSize()
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
		s.cursor = len(s.rows) - 1
		if s.cursor < 0 {
			s.cursor = 0
		}
		s.scrollIntoView()
	case "r":
		s.loading = true
		s.loadErr = ""
		return s, s.Init()
	case "f":
		s.filterIdx = (s.filterIdx + 1) % len(storageSlotFilters)
		// A re-fetch replaces the whole row set, so an index kept from the old
		// view would park the cursor on an unrelated slot.
		s.cursor, s.windowStart = 0, 0
		s.loading = true
		s.loadErr = ""
		return s, s.Init()
	case "/":
		s.scoping = true
		s.scopeErr = ""
		s.scopeInput.SetValue(s.scopeLabel())
		s.scopeInput.CursorEnd()
		s.scopeInput.Focus()
		return s, textinput.Blink
	case "enter":
		if row, ok := s.selectedRow(); ok {
			return s, SwitchTo(WSFacilities, NewStorageSlotDetailScreen(s.deps, row.Code))
		}
	case "n":
		return s, SwitchTo(WSFacilities, NewStorageSlotFormScreen(s.deps, ""))
	case "E":
		if row, ok := s.selectedRow(); ok {
			return s, SwitchTo(WSFacilities, NewStorageSlotFormScreen(s.deps, row.Code))
		}
	case "b":
		return s, SwitchTo(WSFacilities, NewStorageSlotGenerateScreen(s.deps, s.scopeRack))
	case "x":
		if _, ok := s.selectedRow(); ok {
			s.confirmingDelete = true
		}
	case " ":
		s.toggleSelection()
	case "c":
		if len(s.selection) == 0 {
			return s, nil
		}
		n := len(s.selection)
		s.selection = nil
		s.selected = map[int]bool{}
		return s, Status(fmt.Sprintf("cleared %d selected slot(s)", n), StatusOK)
	case "p":
		return s.openCardPrompt()
	}
	return s, nil
}

// toggleSelection adds or removes the highlighted slot from the print set.
// Membership survives a filter change or a refresh — a slot picked under the
// "free" view is still picked after cycling to "all", which is what makes
// "collect the four scuffed cards, then print" work.
func (s *StorageSlotsScreen) toggleSelection() {
	row, ok := s.selectedRow()
	if !ok {
		return
	}
	if s.selected[row.ID] {
		delete(s.selected, row.ID)
		for i, id := range s.selection {
			if id == row.ID {
				s.selection = append(s.selection[:i], s.selection[i+1:]...)
				break
			}
		}
		return
	}
	s.selected[row.ID] = true
	s.selection = append(s.selection, row.ID)
}

// openCardPrompt picks the print mode from what the operator has set up:
// an explicit selection wins, else the rack they scoped to, else the rack the
// cursor is sitting on. The two modes are mutually exclusive server-side, so
// exactly one is built.
func (s *StorageSlotsScreen) openCardPrompt() (Screen, tea.Cmd) {
	if len(s.selection) > 0 {
		req := omsapi.SlotCardsForIDs(append([]int(nil), s.selection...))
		summary := fmt.Sprintf("%d selected slot(s), in the order picked", len(s.selection))
		s.card.open(summary, req, "storage_slot_cards.pdf")
		return s, textinput.Blink
	}

	rack, level := s.scopeRack, s.scopeLevel
	if rack == 0 {
		row, ok := s.selectedRow()
		if !ok {
			return s, Status("nothing to print — select slots with space, or scope to a rack with /", StatusWarn)
		}
		// The cursor's rack, NOT its level: a card print is a rack job unless
		// the operator narrowed the view themselves.
		rack, level = row.Rack, ""
	}
	req := omsapi.SlotCardsForRack(rack, level, false)
	summary := fmt.Sprintf("rack %d", rack)
	if level != "" {
		summary += ", level " + level
	}
	summary += " · in code order"
	s.card.open(summary, req, fmt.Sprintf("storage_slot_cards_rack%d%s.pdf", rack, level))
	return s, textinput.Blink
}

func (s *StorageSlotsScreen) updateCardPrompt(m tea.KeyMsg) (Screen, tea.Cmd) {
	fire, cancel, cmd := s.card.handleKey(m)
	switch {
	case cancel:
		s.card.close()
		return s, nil
	case fire:
		deps := s.deps
		ctx := s.ctx()
		req := s.card.request()
		path := s.card.path.Value()
		fallback := s.cardFallbackName(req)
		return s, func() tea.Msg {
			pdf, err := deps.OMS.RenderStorageSlotCards(ctx, req)
			return storageSlotCardsRenderedMsg{pdf: pdf, path: path, fallback: fallback, err: err}
		}
	}
	return s, cmd
}

func (s *StorageSlotsScreen) cardFallbackName(req omsapi.SlotCardBatchRequest) string {
	if req.Rack == nil {
		return "storage_slot_cards.pdf"
	}
	return fmt.Sprintf("storage_slot_cards_rack%d%s.pdf", *req.Rack, req.Level)
}

// updateScope handles the `/` rack-scope prompt. A blank entry clears the scope
// (back to every rack); "1" scopes to a rack and "1A" narrows to one level.
// Parsing happens here so a typo is a message rather than a request that
// silently matches nothing.
func (s *StorageSlotsScreen) updateScope(m tea.KeyMsg) (Screen, tea.Cmd) {
	switch m.String() {
	case "esc":
		s.scoping = false
		s.scopeErr = ""
		s.scopeInput.Blur()
		return s, nil
	case "enter":
		rack, level, err := parseSlotScope(s.scopeInput.Value())
		if err != nil {
			s.scopeErr = err.Error()
			s.scopeRefused = s.scopeInput.Value()
			return s, nil
		}
		s.scoping = false
		s.scopeErr = ""
		s.scopeInput.Blur()
		s.scopeRack, s.scopeLevel = rack, level
		s.cursor, s.windowStart = 0, 0
		s.loading = true
		s.loadErr = ""
		return s, s.Init()
	}
	var cmd tea.Cmd
	s.scopeInput, cmd = s.scopeInput.Update(m)
	return s, cmd
}

// parseSlotScope reads "", "1" or "1A" (case-insensitively) into a rack and an
// optional level. A whole slot code ("1A1") is rejected rather than silently
// dropping the position — the operator meant to open that slot, not scope to it.
func parseSlotScope(in string) (rack int, level string, err error) {
	v := strings.ToUpper(strings.TrimSpace(in))
	if v == "" {
		return 0, "", nil
	}
	digits := 0
	for digits < len(v) && v[digits] >= '0' && v[digits] <= '9' {
		digits++
	}
	if digits == 0 {
		return 0, "", fmt.Errorf("scope looks like a rack (1) or a rack+level (1A)")
	}
	n, convErr := strconv.Atoi(v[:digits])
	if convErr != nil || n < 1 {
		return 0, "", fmt.Errorf("rack must be a positive number")
	}
	rest := v[digits:]
	switch {
	case rest == "":
		return n, "", nil
	case len(rest) == 1 && rest[0] >= 'A' && rest[0] <= 'Z':
		return n, rest, nil
	default:
		return 0, "", fmt.Errorf("scope is a rack (1) or a rack+level (1A) — %q looks like a slot code", v)
	}
}

func (s *StorageSlotsScreen) updateConfirmDelete(m tea.KeyMsg) (Screen, tea.Cmd) {
	if s.deleting {
		return s, nil
	}
	switch m.String() {
	case "y", "Y":
		row, ok := s.selectedRow()
		if !ok {
			s.confirmingDelete = false
			return s, nil
		}
		s.deleting = true
		deps := s.deps
		ctx := s.ctx()
		code := row.Code
		return s, func() tea.Msg {
			return storageSlotDeletedMsg{code: code, err: deps.OMS.DeleteStorageSlot(ctx, code)}
		}
	case "n", "N", "esc":
		s.confirmingDelete = false
	}
	return s, nil
}

func (s *StorageSlotsScreen) selectedRow() (omsapi.StorageSlot, bool) {
	if s.cursor < 0 || s.cursor >= len(s.rows) {
		return omsapi.StorageSlot{}, false
	}
	return s.rows[s.cursor], true
}

func (s *StorageSlotsScreen) scrollIntoView() {
	if s.windowSize <= 0 {
		s.windowSize = 20
	}
	if s.cursor < s.windowStart {
		s.windowStart = s.cursor
	}
	if s.cursor >= s.windowStart+s.windowSize {
		s.windowStart = s.cursor - s.windowSize + 1
	}
	if s.windowStart < 0 {
		s.windowStart = 0
	}
	if len(s.rows) <= s.windowSize {
		s.windowStart = 0
	}
}

func (s *StorageSlotsScreen) scopeLabel() string {
	if s.scopeRack == 0 {
		return ""
	}
	return strconv.Itoa(s.scopeRack) + s.scopeLevel
}

// ---------------------------------------------------------------------------
// Bar
// ---------------------------------------------------------------------------

// bar names every key that acts on the list, as a record the honesty sweep can
// press (prose_bar.go).
//
// It used to be two literals written under the rows —
//
//	j/k move · enter open · n new · b generate rack · E edit · x delete
//	space select · c clear · p print cards · f filter · / rack · r refresh
//
// — which named two of the ten movement keystrokes the switch binds, named the
// row actions over an empty list where they do nothing, named `c` with nothing
// selected and `p` where it can only decline, and ran past the 51 cells an
// 80-column pane gives, so clampToBox took the end of both lines. Each segment
// is now gated on the fact its arm is gated on: a second row (proseNavCursor),
// a row under the cursor, a selection to clear, and something to print
// (canPrint, the same precedence openCardPrompt reads).
func (s *StorageSlotsScreen) bar(moves, onRow, selected, printable bool) proseBar {
	out := proseNavCursor(moves)
	if onRow {
		out = append(out, proseBarItem{Keys: []string{"enter"}, Hint: "enter open"})
	}
	out = append(out,
		proseBarItem{Keys: []string{"n"}, Hint: "n new"},
		proseBarItem{Keys: []string{"b"}, Hint: "b generate rack"},
	)
	if onRow {
		out = append(out,
			proseBarItem{Keys: []string{"E"}, Hint: "E edit"},
			proseBarItem{Keys: []string{"x"}, Hint: "x delete"},
			proseBarItem{Keys: []string{" "}, Hint: "space select"},
		)
	}
	if selected {
		out = append(out, proseBarItem{Keys: []string{"c"}, Hint: "c clear"})
	}
	if printable {
		out = append(out, proseBarItem{Keys: []string{"p"}, Hint: "p print cards"})
	}
	return append(out,
		proseBarItem{Keys: []string{"f"}, Hint: "f filter"},
		proseBarItem{Keys: []string{"/"}, Hint: "/ rack"},
		proseBarRefresh,
		proseBarEsc,
	)
}

// ceilingBar is the bar at its TALLEST, which is what the body is budgeted
// against for the reason proseListWindow gives: a budget taken from the live bar
// changes when a segment comes off, and the rows it leaves are an input to what
// the list shows.
func (s *StorageSlotsScreen) ceilingBar() proseBar { return s.bar(true, true, true, true) }

// canPrint reports whether `p` opens the print prompt rather than declining:
// a selection, a rack scope, or a row under the cursor to take the rack from.
func (s *StorageSlotsScreen) canPrint() bool {
	_, onRow := s.selectedRow()
	return len(s.selection) > 0 || s.scopeRack > 0 || onRow
}

// proseBar is the bar this screen is DRAWING: loadBar's while a load is out or
// has failed, the scope prompt's while it is open, the list's otherwise — and
// nil under the delete confirm and the print prompt, which name their own keys.
// The print prompt is slotCardPrompt, a recorded exception (proseBarUnconverted)
// rather than a surface this record answers for; what this screen owes it is
// ROOM, which foot gives it.
func (s *StorageSlotsScreen) proseBar() proseBar {
	switch {
	case s.loading || s.loadErr != "":
		return s.loadBar()
	case s.scoping:
		return s.scopeBar()
	case s.confirmShown(), s.card.active:
		return nil
	}
	_, onRow := s.selectedRow()
	return s.bar(listNavMoves(len(s.rows)), onRow, len(s.selection) > 0, s.canPrint())
}

// loadBar is the list's bar while its load is out or has failed — what its key
// switch still answers with no rows drawn (prose_bar.go carries the defect and
// the decision). `n` and `b` open their forms whatever the list holds, `f`
// reloads under the next filter, and after a failed REFRESH `enter` and `E` still
// open the slot the kept rows leave under the cursor, which the frame no longer
// draws — named because they act, and candidates for gating. The movement keys,
// `x`, space, `c`, `p` and `/` would change only state no load frame draws (a
// cursor, an armed confirm, a selection, a prompt), so they are not named and
// are ignored.
func (s *StorageSlotsScreen) loadBar() proseBar {
	var out proseBar
	_, onRow := s.selectedRow()
	if onRow {
		out = append(out, proseBarItem{Keys: []string{"enter"}, Hint: "enter open"})
	}
	out = append(out,
		proseBarItem{Keys: []string{"n"}, Hint: "n new"},
		proseBarItem{Keys: []string{"b"}, Hint: "b generate rack"},
	)
	if onRow {
		out = append(out, proseBarItem{Keys: []string{"E"}, Hint: "E edit"})
	}
	return append(out,
		proseBarItem{Keys: []string{"f"}, Hint: "f filter"},
		proseBarReloadFor(s.loadErr != ""),
		proseBarEsc,
	)
}

// scopeBar is the `/` prompt's bar. Every other key goes into the box, which is
// what makes it a typing surface rather than a list.
//
// `enter` comes off while the box still holds the value it last refused: there
// it re-parses the same text into the same refusal, which redraws the pane byte
// for byte, and the refusal row above the bar has already said why. Any edit
// puts it back.
func (s *StorageSlotsScreen) scopeBar() proseBar {
	var out proseBar
	if s.scopeErr == "" || s.scopeInput.Value() != s.scopeRefused {
		out = append(out, proseBarItem{Keys: []string{"enter"}, Hint: "enter apply (blank: every rack)"})
	}
	return append(out, proseBarItem{Keys: []string{"esc"}, Hint: "esc cancel"})
}

// confirmShown reports whether the delete confirm is what the foot draws. It is
// armed only over a row, so the second half is a guard rather than a state.
func (s *StorageSlotsScreen) confirmShown() bool {
	_, ok := s.selectedRow()
	return s.confirmingDelete && ok
}

// slotListStaffNote is the standing fact under a failed load: the likeliest
// failure is a 403, and it is not the operator's to retry away.
const slotListStaffNote = "slot management is staff / Storage Admin only"

// ---------------------------------------------------------------------------
// View
// ---------------------------------------------------------------------------

// View draws the slots as a window between the header and the FOOT
// (proseFlatListFrameFoot), where the foot is whichever of the bar, the scope
// prompt, the delete confirm and the print prompt is up.
//
// THE FOOT IS SPENT BEFORE THE WINDOW GETS A LINE, and that is the whole of the
// fix. Every one of those was written under a window budgeted by a constant, so
// on a long list the print prompt's five rows ran the frame past the pane and
// clampToBox, which drops from the BOTTOM, took `enter render · esc cancel` —
// measured at 80x24 before the conversion: 21 rows for an 18-row pane. A slot
// whose stored group name carries newlines drew every one of them, 50 rows for
// the same pane with the bar gone; proseWriteRows clips such a row to the budget
// and says how much it left out.
func (s *StorageSlotsScreen) View() string {
	cells := s.paneCells()
	if s.loading {
		return proseLoadingFrame("Loading storage slots…", cells, s.proseBar())
	}
	if s.loadErr != "" {
		return proseRefusalFrame("Error: ", StyleStatusError, s.loadErr, slotListStaffNote, s.terminalHeight, cells, s.proseBar())
	}
	rows := make([]string, len(s.rows))
	for i := range s.rows {
		rows[i] = s.renderRow(i, cells)
	}
	foot, footRows := s.foot(cells)
	return proseFlatListFrameFoot(s.head(cells), rows, s.cursor, &s.windowStart, s.terminalHeight, footRows, foot)
}

// head is what the window is drawn under: the header, a blank, and on an empty
// list the sentence saying which kind of empty it is.
func (s *StorageSlotsScreen) head(cells int) string {
	h := s.header(cells) + "\n\n"
	if len(s.rows) == 0 {
		h += s.emptyText() + "\n"
	}
	return h
}

// foot is what is drawn under the rows, and every row it takes — the blank above
// it included.
//
// THE BAR'S COST AND AN OVERLAY'S ARE MEASURED SEPARATELY. The bar is counted at
// its ceiling (see ceilingBar), which is also what a page is
// (computeWindowSize). An overlay is counted as it is DRAWN — the scope prompt,
// the delete confirm, slotCardPrompt — because each is a surface of its own
// shape, and a reservation standing in for all of them was the constant this
// replaced: too little for the print prompt and too much for everything else.
func (s *StorageSlotsScreen) foot(cells int) (string, int) {
	var foot string
	switch {
	case s.scoping:
		foot = s.scopePrompt(cells)
	case s.confirmShown():
		foot = s.deleteConfirmText()
	case s.card.active:
		foot = s.card.view(cells)
	default:
		return s.proseBar().render(cells), s.ceilingBar().rows(cells)
	}
	return foot, 1 + strings.Count(foot, "\n") + 1
}

// scopePrompt is the `/` prompt as a foot: the box, bounded to the pane so the
// caret stays on it; the refusal, held to one marked row; and the bar.
func (s *StorageSlotsScreen) scopePrompt(cells int) string {
	const prefix = "Scope to rack: "
	var b strings.Builder
	b.WriteString(StyleTitle.Render(prefix) + woBoxView(s.scopeInput, cells, strings.Repeat(" ", lipgloss.Width(prefix))) + "\n")
	if s.scopeErr != "" {
		b.WriteString(StyleStatusError.Render("✗ "+proseFormLine(s.scopeErr, cells-2)) + "\n")
	}
	b.WriteString("\n" + s.scopeBar().render(cells))
	return b.String()
}

// header is the scope, the filter, the count and the selection, folded to the
// pane at its ` · ` joints: the selection count is the last fact on it and the
// one a clip would take.
func (s *StorageSlotsScreen) header(cells int) string {
	scope := "all racks"
	if s.scopeRack > 0 {
		scope = "rack " + strconv.Itoa(s.scopeRack)
		if s.scopeLevel != "" {
			scope += " level " + s.scopeLevel
		}
	}
	parts := []string{
		"Scope: " + scope,
		"Filter: " + storageSlotFilters[s.filterIdx].label,
		fmt.Sprintf("%d slots", len(s.rows)),
	}
	if n := len(s.selection); n > 0 {
		parts = append(parts, fmt.Sprintf("%d selected", n))
	}
	return pickerHintAt(strings.Join(parts, " · "), cells)
}

// emptyText distinguishes "this org has no racking" from "nothing matches the
// view you are in" — a bare "no slots" under a free/rack filter reads as the
// former and sends a warden looking for a bug.
func (s *StorageSlotsScreen) emptyText() string {
	filtered := s.filterIdx != 0 || s.scopeRack > 0
	cells := s.paneCells()
	if !filtered {
		return pickerHintAt("No storage slots yet.", cells) + "\n" +
			pickerHintAt("press b to generate a rack, or n to add a single slot", cells)
	}
	return pickerHintAt(fmt.Sprintf("No slots in the %q view.", storageSlotFilters[s.filterIdx].label), cells) + "\n" +
		pickerHintAt("press f to cycle the filter · / to change the rack scope", cells)
}

// slotOccupancyCells is the occupancy column's width where the pane has room
// for it, and slotOccupancyFloorCells the least it gives up to on a narrow one.
const (
	slotOccupancyCells      = 42
	slotOccupancyFloorCells = 12
)

// renderRow is one slot, fitted to the pane with every cut marked.
//
// THE OCCUPANCY COLUMN GIVES BEFORE THE FLAGS DO. It was padded to a fixed 42
// cells, so at 80 columns the code, the caret gutter and the padding alone
// spent the pane and clampToBox took every flag off every row — the marker id a
// warden scans for included — with no mark saying so. The column now takes what
// the widest FACTS on the list leave (tag, pallet jack, retired), down to a
// floor, the same width on every row so the facts still line up, and the name in
// it abbreviates with an ellipsis. The owning group's NAME is not one of those
// facts: it is an identifier, it sits last, and it is clipped to what the facts
// leave on its own row. A stored value with a newline in it keeps the newline,
// clipped line by line, for the reason proseCursorWindow gives.
func (s *StorageSlotsScreen) renderRow(i int, cells int) string {
	slot := s.rows[i]
	mark := "   "
	if s.selected[slot.ID] {
		mark = "[✓]"
	}
	caret := "  "
	if i == s.cursor {
		caret = "▸ "
	}
	room := cells - StyleSidebarItemActive.GetHorizontalPadding()
	lead := fmt.Sprintf("%s%s %-6s ", caret, mark, slot.Code)
	occW := room - lipgloss.Width(lead) - 1 - s.widestFacts()
	if occW > slotOccupancyCells {
		occW = slotOccupancyCells
	}
	if occW < slotOccupancyFloorCells {
		occW = slotOccupancyFloorCells
	}
	occ := pickerClip(slotOccupancyText(slot), occW)
	occ += strings.Repeat(" ", occW-lipgloss.Width(occ))
	line := lead + occ + " " + slotFactsText(slot)
	if name := slot.OwningGroupName; name != "" {
		const joint = " · "
		left := room - lipgloss.Width(line) - lipgloss.Width(joint)
		if left < 1 {
			left = 1
		}
		lines := strings.Split(name, "\n")
		for n, l := range lines {
			lines[n] = pickerClip(l, left)
		}
		line += joint + strings.Join(lines, "\n")
	}
	line = proseClipEachLine(strings.TrimRight(line, " "), room)
	if i == s.cursor {
		return StyleSidebarItemActive.Render(line)
	}
	return line
}

// widestFacts is the widest run of facts any slot on the list carries — the
// width the occupancy column is sized around.
func (s *StorageSlotsScreen) widestFacts() int {
	widest := 0
	for _, slot := range s.rows {
		if w := lipgloss.Width(slotFactsText(slot)); w > widest {
			widest = w
		}
	}
	return widest
}

// slotOccupancyText is the one-line answer to "can I put something here?" —
// who is in it, or free. Retired is called out because a free retired slot is
// NOT on offer, which "free" alone would imply.
//
// Occupancy has two halves: a member's project stint (P) and a staff-assigned
// committee/logistics/class holding (C/L/E). BOTH make the slot unavailable, so
// both are named — reading occupancy off `current_stint` alone would report the
// welding SIG's shelf as free and hand it out twice.
func slotOccupancyText(slot omsapi.StorageSlot) string {
	if slot.CurrentStint != nil {
		who := strings.TrimSpace(slot.CurrentStint.DisplayName)
		if who == "" {
			who = slot.CurrentStint.Username
		}
		text := who + " · " + slot.CurrentStint.StintID
		if p := strings.TrimSpace(slot.CurrentStint.ProjectTitle); p != "" {
			text += " · " + p
		}
		// 41 + the ellipsis truncateOneLine appends = the 42-wide column, so a
		// long project title can't push the marker column out of alignment.
		return truncateOneLine(text, 41)
	}
	if a := slot.CurrentAssignment; a != nil {
		text := firstNonEmpty(a.OccupantDisplay, storageTypeName(a.TypeLetter)) +
			" · " + storageTypeName(a.TypeLetter) + " storage"
		return truncateOneLine(text, 41)
	}
	if !slot.IsActive {
		return "retired — not offered"
	}
	if slot.IsOccupied {
		// is_occupied true with neither half filled shouldn't happen (they
		// derive from the same lookup), but say "occupied" rather than "free"
		// if it ever does — a wrong "free" hands the same shelf out twice.
		return "occupied"
	}
	return "free"
}

// slotFlagsText carries the two things a warden needs beside occupancy: the
// permanent marker id (blank means the tag family ran dry — the slot works by
// code but has nothing to scan) and whether reaching it needs a pallet jack.
func slotFlagsText(slot omsapi.StorageSlot) string {
	if slot.OwningGroupName == "" {
		return slotFactsText(slot)
	}
	return slotFactsText(slot) + " · " + slot.OwningGroupName
}

// slotFactsText is slotFlagsText without the owning group's name: the fixed
// facts a row never gives up, as opposed to the identifier it clips.
func slotFactsText(slot omsapi.StorageSlot) string {
	var parts []string
	if slot.AprilTagID != nil {
		parts = append(parts, fmt.Sprintf("tag %d", *slot.AprilTagID))
	} else {
		parts = append(parts, "tag —")
	}
	if slot.RequiresPalletJack {
		parts = append(parts, "jack")
	}
	if !slot.IsActive {
		parts = append(parts, "retired")
	}
	return strings.Join(parts, " · ")
}

// deleteConfirmText is the delete confirm, drawn as the foot under the rows.
//
// FOLDED TO THE PANE, because every sentence on it was past the 51 cells an
// 80-column pane gives and clampToBox took the tails — the tail of the refusal
// warning being "the backend will refuse". That clause LEADS its sentence now,
// so a fold cannot split it from the line that opens it. The OMS values it carries are
// flattened and the occupant's name is clipped before it is folded, so the
// confirm is a bounded number of rows whatever the record holds, and the keys on
// its last row stay on the pane.
func (s *StorageSlotsScreen) deleteConfirmText() string {
	row, ok := s.selectedRow()
	if !ok {
		return ""
	}
	if s.deleting {
		return StyleMuted.Render("Deleting…")
	}
	cells := s.paneCells()
	fold := func(style lipgloss.Style, text string) string {
		return style.Render(strings.Join(pickerWrap(jdeStatusOneLine(text), cells), "\n")) + "\n"
	}
	code := pickerClip(jdeStatusOneLine(row.Code), cells/2)
	body := fold(StyleStatusWarn, "Delete slot "+code+"? This RELEASES its AprilTag permanently.")
	if row.CurrentStint != nil {
		stint := pickerClip(jdeStatusOneLine(row.CurrentStint.StintID), cells/2)
		body += fold(StyleMuted, "The backend will refuse: a live stint ("+stint+") is in this slot.")
	}
	if a := row.CurrentAssignment; a != nil {
		who := pickerClip(jdeStatusOneLine(firstNonEmpty(a.OccupantDisplay, "unnamed")), cells/2)
		body += fold(StyleMuted, "The backend will refuse: "+storageTypeName(a.TypeLetter)+" storage ("+who+") holds this slot.")
	}
	body += fold(StyleMuted, "Retiring it instead (E → Active off) keeps the tag and the history.")
	body += StyleMuted.Render("y delete · n/esc cancel")
	return body
}
