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
// Keys: j/k move · enter open · n new slot · b generate a rack · E edit ·
// x delete · space select for printing · c clear selection · p print cards ·
// f cycle the occupancy filter · / scope to a rack (or a rack + level) ·
// r refresh. n/G/f// collide with global hotkeys and are claimed via
// HandlesKey; the screen goes raw-input only while an overlay is up.
package tui

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

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
	terminalHeight int

	filterIdx int

	// Rack scope, set from the `/` prompt. rack 0 means "every rack"; level is
	// only meaningful with a rack.
	scopeRack  int
	scopeLevel string

	scoping    bool
	scopeInput textinput.Model
	scopeErr   string

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

func (s *StorageSlotsScreen) computeWindowSize() int {
	const chrome = 6 // header + blank + help + overlay room
	avail := screenBodyHeight(s.terminalHeight) - chrome
	if avail < 3 {
		avail = 3
	}
	return avail
}

func (s *StorageSlotsScreen) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		s.terminalHeight = m.Height
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

func (s *StorageSlotsScreen) View() string {
	if s.loading {
		return StyleMuted.Render("Loading storage slots…")
	}
	if s.loadErr != "" {
		return StyleStatusError.Render("Error: ") + s.loadErr + "\n\n" +
			StyleMuted.Render("press r to retry · slot management is staff / Storage Admin only")
	}

	var b strings.Builder
	b.WriteString(s.header() + "\n\n")

	if len(s.rows) == 0 {
		b.WriteString(s.emptyText() + "\n")
	} else {
		end := s.windowStart + s.windowSize
		if end > len(s.rows) {
			end = len(s.rows)
		}
		if s.windowStart > 0 {
			b.WriteString(StyleMuted.Render(fmt.Sprintf("  ↑ %d more above", s.windowStart)) + "\n")
		}
		for i := s.windowStart; i < end; i++ {
			b.WriteString(s.renderRow(i) + "\n")
		}
		if end < len(s.rows) {
			b.WriteString(StyleMuted.Render(fmt.Sprintf("  ↓ %d more below", len(s.rows)-end)) + "\n")
		}
	}

	b.WriteString("\n")
	switch {
	case s.scoping:
		b.WriteString(StyleTitle.Render("Scope to rack: ") + s.scopeInput.View() + "\n")
		if s.scopeErr != "" {
			b.WriteString(StyleStatusError.Render("✗ "+s.scopeErr) + "\n")
		}
		b.WriteString(StyleMuted.Render("enter apply · blank shows every rack · esc cancel"))
	case s.confirmingDelete:
		b.WriteString(s.deleteConfirmText())
	case s.card.active:
		b.WriteString(s.card.view())
	default:
		b.WriteString(StyleMuted.Render(
			"j/k move · enter open · n new · b generate rack · E edit · x delete\n" +
				"space select · c clear · p print cards · f filter · / rack · r refresh"))
	}
	return b.String()
}

func (s *StorageSlotsScreen) header() string {
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
	return StyleMuted.Render(strings.Join(parts, " · "))
}

// emptyText distinguishes "this org has no racking" from "nothing matches the
// view you are in" — a bare "no slots" under a free/rack filter reads as the
// former and sends a warden looking for a bug.
func (s *StorageSlotsScreen) emptyText() string {
	filtered := s.filterIdx != 0 || s.scopeRack > 0
	if !filtered {
		return StyleMuted.Render("No storage slots yet.") + "\n" +
			StyleMuted.Render("press b to generate a rack, or n to add a single slot")
	}
	return StyleMuted.Render(fmt.Sprintf("No slots in the %q view.", storageSlotFilters[s.filterIdx].label)) + "\n" +
		StyleMuted.Render("press f to cycle the filter · / to change the rack scope")
}

func (s *StorageSlotsScreen) renderRow(i int) string {
	slot := s.rows[i]
	mark := "   "
	if s.selected[slot.ID] {
		mark = "[✓]"
	}
	caret := "  "
	if i == s.cursor {
		caret = "▸ "
	}
	line := fmt.Sprintf("%s%s %-6s %-42s %s",
		caret, mark, slot.Code, slotOccupancyText(slot), slotFlagsText(slot))
	if i == s.cursor {
		return StyleSidebarItemActive.Render(strings.TrimRight(line, " "))
	}
	return strings.TrimRight(line, " ")
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
	if slot.OwningGroupName != "" {
		parts = append(parts, slot.OwningGroupName)
	}
	return strings.Join(parts, " · ")
}

func (s *StorageSlotsScreen) deleteConfirmText() string {
	row, ok := s.selectedRow()
	if !ok {
		return ""
	}
	if s.deleting {
		return StyleMuted.Render("Deleting…")
	}
	warn := "Delete slot " + row.Code + "? This RELEASES its AprilTag permanently."
	body := StyleStatusWarn.Render(warn) + "\n"
	if row.CurrentStint != nil {
		body += StyleMuted.Render("A live stint ("+row.CurrentStint.StintID+") is in this slot — the backend will refuse.") + "\n"
	}
	if a := row.CurrentAssignment; a != nil {
		body += StyleMuted.Render(storageTypeName(a.TypeLetter)+" storage ("+
			firstNonEmpty(a.OccupantDisplay, "unnamed")+") holds this slot — the backend will refuse.") + "\n"
	}
	body += StyleMuted.Render("Retiring it instead (E → Active off) keeps the tag and the history.") + "\n"
	body += StyleMuted.Render("y delete · n/esc cancel")
	return body
}
