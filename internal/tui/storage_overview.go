// The storage overview — one glanceable ASCII grid per rack.
//
// The read this exists for: stand in the aisle, pull the rack up, look for the
// coloured cells, go deal with those. So the screen is laid out like the steel —
// a row per level with the HIGH levels first, one column per position, holes in
// the racking left as holes — and one character per slot:
//
//	          1         2
//	 12345678901234567890
//	Z...P...C...LLLL...E.
//	Y...........LLLL...E.
//	D..PP.PPPPP.PP.PPPP..
//	A......PPP.PP.P.P.P.P
//
// P = a member's project, C = committee, L = logistics, E = class, "." = an
// empty slot (or no slot at that position). ONLY project cells are ever
// coloured — yellow for expiring soon, red for expired and past — and that
// decision is the BACKEND's: the payload carries a colour name and this screen
// only maps the name to a style. A committee has held its slot for two years
// and will tomorrow; colouring that would drown the one expired member project,
// which is the entire point of looking at the grid.
//
// The screen is also where the C/L/E half of the racking is administered: `a`
// hands a free slot to a committee / crew / class and `x` takes it back. A
// project stint is NOT released here — that is the member's own lifecycle, and
// enter walks to the slot (and from there to the stint) to do it properly.
//
// There is no web counterpart — the OMS frontend has neither an overview page
// nor an assignment surface — so the serializer + viewset are the parity
// target; see internal/omsapi/storage_overview.go for the verified contract.
//
// The keys the grid answers are a proseBar RECORD (StorageOverviewScreen.proseBar),
// not a list here: which of them is named depends on where the cursor stands, and
// a copy of that in a comment is a second answer that drifts.
package tui

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/uid0/scantty/internal/omsapi"
)

// storageGridEmptyGlyph is what an empty slot AND a hole in the racking both
// render as. They are told apart by STYLE (a hole and a retired slot are dim)
// and named outright on the cursor line, rather than by inventing a second
// glyph — the grid is read as a picture of the rack, and one character per slot
// with one meaning is what makes it readable at a glance.
const storageGridEmptyGlyph = "."

// storageGridStyles maps a cell class to its style. The two colour keys are the
// backend's own colour names: no status judgement is made here, so a new
// alarming stint status becomes visible the moment the server starts colouring
// it. Bold (not a different hue) is what makes a single coloured character
// carry across a wall of dots, and it costs no display width.
var storageGridStyles = map[string]lipgloss.Style{
	omsapi.StorageOverviewColorYellow: lipgloss.NewStyle().Foreground(colorWarn).Bold(true),
	omsapi.StorageOverviewColorRed:    lipgloss.NewStyle().Foreground(colorError).Bold(true),
	// A hole has no slot and a retired slot is not on offer: both are dim, so
	// the eye reads them as "nothing to hand out here".
	"hole":    lipgloss.NewStyle().Foreground(colorBorder),
	"retired": lipgloss.NewStyle().Foreground(colorMuted),
	"empty":   lipgloss.NewStyle(),
	"held":    lipgloss.NewStyle(),
}

// storageGridCellClass picks a cell's style key. Colour wins over everything
// else — a project that needs moving is the one thing the screen exists to
// show, and a retired slot holding an expired project is still an expired
// project.
func storageGridCellClass(cell *omsapi.StorageOverviewCell) string {
	if cell == nil {
		return "hole"
	}
	if cell.Color != "" {
		if _, ok := storageGridStyles[cell.Color]; ok {
			return cell.Color
		}
		// An unrecognized colour name (the backend grew a third one) still
		// deserves to stand out rather than render as an ordinary cell.
		return omsapi.StorageOverviewColorYellow
	}
	if !cell.IsActive {
		return "retired"
	}
	if cell.Type == "" {
		return "empty"
	}
	return "held"
}

// storageGridGlyph is the single character a cell paints: its type letter, or
// the empty glyph for an empty slot / a hole.
func storageGridGlyph(cell *omsapi.StorageOverviewCell) string {
	if cell == nil || cell.Type == "" {
		return storageGridEmptyGlyph
	}
	return cell.Type
}

// storageTypeName spells a grid letter out for the cursor line and the assign
// confirmations, where a bare "C" would be a puzzle.
func storageTypeName(letter string) string {
	switch letter {
	case omsapi.StorageTypeLetterProject:
		return "Project"
	case omsapi.StorageTypeLetterCommittee:
		return "Committee"
	case omsapi.StorageTypeLetterLogistics:
		return "Logistics"
	case omsapi.StorageTypeLetterClass:
		return "Class"
	}
	return letter
}

type StorageOverviewScreen struct {
	deps Deps

	overview *omsapi.StorageOverview
	loading  bool
	loadErr  string

	// The cursor is kept as the ADDRESS it points at (rack number, level
	// letter, position), not as indices: a refresh can add a rack, retire the
	// last slot of a level or widen a row, and indices kept across that land
	// the warden on someone else's slot.
	rackNumber int
	level      string
	position   int

	// Horizontal window over the positions, for a rack wider than the pane.
	colStart int

	// Vertical window over the levels, for a rack with more shelves than rows.
	rowStart int

	terminalWidth  int
	terminalHeight int

	confirmingRelease bool
	releasing         bool
}

type storageOverviewLoadedMsg struct {
	overview *omsapi.StorageOverview
	err      error
}

type storageOverviewReleasedMsg struct {
	code     string
	occupant string
	err      error
	// notHeld: the grid said C/L/E but the live assignment was already gone —
	// someone else released it, or this grid is stale. A distinct outcome from
	// both success and failure, because the honest answer is "reload".
	notHeld bool
}

// NewStorageOverviewScreen opens the grid on the first rack.
func NewStorageOverviewScreen(deps Deps) *StorageOverviewScreen {
	return NewStorageOverviewScreenAt(deps, 0, "", 0)
}

// NewStorageOverviewScreenAt opens the grid with the cursor aimed at one slot.
// Used to come BACK from the assign form (or the slot detail) onto the cell the
// warden was standing on, instead of resetting them to the top of rack one.
// A zero rack / blank level / zero position means "wherever the grid starts".
func NewStorageOverviewScreenAt(deps Deps, rack int, level string, position int) *StorageOverviewScreen {
	return &StorageOverviewScreen{
		deps:       deps,
		loading:    true,
		rackNumber: rack,
		level:      strings.ToUpper(strings.TrimSpace(level)),
		position:   position,
	}
}

func (s *StorageOverviewScreen) Title() string {
	if r := s.rack(); r != nil {
		return fmt.Sprintf("Storage Overview · rack %d", r.Rack)
	}
	return "Storage Overview"
}

// HandlesKey still claims `a` (assign) and `l` (the right-hand vim move). The
// globals they collided with are gone since phase 3, so the claim no longer
// rescues them from anything — it is kept as the screen naming the keys it owns
// and costs nothing. Everything else reaches us through the root's fall-through.
func (s *StorageOverviewScreen) HandlesKey(key string) bool {
	if s.WantsRawInput() {
		return false
	}
	return key == "a" || key == "l"
}

// WantsRawInput claims every key while the release confirm is up, so `n` = no
// lands here instead of leaking to the global notifications hotkey.
func (s *StorageOverviewScreen) WantsRawInput() bool { return s.confirmingRelease }

func (s *StorageOverviewScreen) ctx() context.Context {
	if s.deps.Ctx != nil {
		return s.deps.Ctx
	}
	return context.Background()
}

func (s *StorageOverviewScreen) Init() tea.Cmd {
	deps := s.deps
	ctx := s.ctx()
	return func() tea.Msg {
		// Every rack in one request: the builder is three queries whatever the
		// slot count, so paging between racks locally beats a round trip each.
		ov, err := deps.OMS.GetStorageOverview(ctx, 0)
		return storageOverviewLoadedMsg{overview: ov, err: err}
	}
}

func (s *StorageOverviewScreen) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		s.terminalWidth, s.terminalHeight = m.Width, m.Height
		s.scrollIntoView()
		return s, nil

	case storageOverviewLoadedMsg:
		s.loading = false
		if m.err != nil {
			s.loadErr = slotCardErrorText(m.err)
			return s, nil
		}
		s.loadErr = ""
		s.overview = m.overview
		s.resolveCursor()
		return s, nil

	case storageOverviewReleasedMsg:
		s.releasing = false
		s.confirmingRelease = false
		switch {
		case m.err != nil:
			return s, Status("release failed: "+slotCardErrorText(m.err), StatusError)
		case m.notHeld:
			// The grid said C/L/E but the assignment is already gone — someone
			// else released it, or the grid is stale. Say which, and reload.
			s.loading = true
			return s, tea.Batch(
				Status("no live holding on "+m.code+" any more — reloading the grid", StatusWarn),
				s.Init(),
			)
		}
		s.loading = true
		return s, tea.Batch(
			Status(storageReleasedText(m.code, m.occupant), StatusOK),
			s.Init(),
		)

	case tea.KeyMsg:
		// A load in flight or failed answers only the keys its bar names — the
		// rule the other converted screens keep (prose_bar.go). Movement here
		// would walk a cursor over a grid the frame no longer draws, and `x`
		// would arm a confirm nobody can read.
		if proseLoadKeyHidden(s.loading, s.loadErr, s.loadBar(), m.String()) {
			return s, nil
		}
		if s.confirmingRelease {
			return s.updateConfirmRelease(m)
		}
		return s.updateGrid(m)
	}
	return s, nil
}

func (s *StorageOverviewScreen) updateGrid(m tea.KeyMsg) (Screen, tea.Cmd) {
	switch m.String() {
	case "h", "left":
		s.movePosition(-1)
	case "l", "right":
		s.movePosition(+1)
	case "k", "up":
		s.moveLevel(-1)
	case "j", "down":
		s.moveLevel(+1)
	case "home":
		s.position = 1
		s.scrollIntoView()
	case "end":
		if r := s.rack(); r != nil {
			s.position = r.MaxPosition
			s.scrollIntoView()
		}
	case "pgdown":
		// Rack-to-rack used to be tab / shift+tab. `tab` is the root's key for
		// moving the keyboard into the sidebar menu now, and a key that means
		// two things depending on which screen is up is exactly what phase 3
		// is retiring — so the rack axis took the page keys instead. A rack IS
		// the page of this grid, so they read as themselves.
		s.moveRack(+1)
	case "pgup":
		s.moveRack(-1)
	case "r":
		s.loading = true
		s.loadErr = ""
		return s, s.Init()
	case "enter":
		if cell := s.cell(); cell != nil {
			return s, SwitchTo(WSFacilities, NewStorageSlotDetailScreen(s.deps, cell.Code))
		}
		return s, Status(s.noSlotText(), StatusWarn)
	case "a":
		return s.openAssign()
	case "x":
		return s.startRelease()
	}
	return s, nil
}

// openAssign refuses up front what the backend would refuse anyway, but names
// the remedy: a live stint is the member's to end, another holding has to be
// released first, and a retired slot is not on offer at all.
func (s *StorageOverviewScreen) openAssign() (Screen, tea.Cmd) {
	cell := s.cell()
	if cell == nil {
		return s, Status(s.noSlotText(), StatusWarn)
	}
	switch {
	case cell.Type == omsapi.StorageTypeLetterProject:
		return s, Status(cell.Code+" holds a live project stint ("+cell.Occupant+") — enter opens the slot, then the stint, to resolve it", StatusWarn)
	case cell.Type != "":
		return s, Status(cell.Code+" is already assigned to "+cell.Occupant+" ("+storageTypeName(cell.Type)+") — press x to release it first", StatusWarn)
	case !cell.IsActive:
		return s, Status(cell.Code+" is out of service — put it back in service (slot → E → Active) before assigning it", StatusWarn)
	}
	return s, SwitchTo(WSFacilities, NewStorageAssignFormScreen(s.deps, cell.Code, s.backHere()))
}

// backHere is the return trip the assign form takes on save or cancel: the same
// grid, on the same cell.
func (s *StorageOverviewScreen) backHere() func(Deps) Screen {
	rack, level, position := s.rackNumber, s.level, s.position
	return func(d Deps) Screen {
		return NewStorageOverviewScreenAt(d, rack, level, position)
	}
}

func (s *StorageOverviewScreen) startRelease() (Screen, tea.Cmd) {
	cell := s.cell()
	if cell == nil {
		return s, Status(s.noSlotText(), StatusWarn)
	}
	switch {
	case cell.Type == omsapi.StorageTypeLetterProject:
		return s, Status(cell.Code+" holds a project stint, not a staff assignment — enter opens the slot, then the stint", StatusWarn)
	case cell.Type == "":
		return s, Status(cell.Code+" is not assigned to anyone", StatusWarn)
	}
	s.confirmingRelease = true
	return s, nil
}

func (s *StorageOverviewScreen) updateConfirmRelease(m tea.KeyMsg) (Screen, tea.Cmd) {
	if s.releasing {
		return s, nil
	}
	switch m.String() {
	case "y", "Y":
		cell := s.cell()
		if cell == nil {
			s.confirmingRelease = false
			return s, nil
		}
		s.releasing = true
		deps := s.deps
		ctx := s.ctx()
		code, occupant := cell.Code, cell.Occupant
		return s, func() tea.Msg {
			// The grid cell carries the occupant but not the assignment id, so
			// resolve the live holding first. The partial unique constraint
			// guarantees at most one, which is why this is exact.
			assignment, err := deps.OMS.ActiveStorageAssignmentForSlot(ctx, code)
			if err != nil {
				return storageOverviewReleasedMsg{code: code, err: err}
			}
			if assignment == nil {
				return storageOverviewReleasedMsg{code: code, notHeld: true}
			}
			_, err = deps.OMS.ReleaseStorageAssignment(ctx, assignment.ID)
			return storageOverviewReleasedMsg{
				code:     code,
				occupant: firstNonEmpty(assignment.OccupantDisplay, occupant),
				err:      err,
			}
		}
	case "n", "N", "esc":
		s.confirmingRelease = false
	}
	return s, nil
}

func storageReleasedText(code, occupant string) string {
	text := "released " + code
	if strings.TrimSpace(occupant) != "" {
		text += " from " + occupant
	}
	return text + " — the slot is assignable again"
}

// ---------------------------------------------------------------------------
// Cursor
// ---------------------------------------------------------------------------

func (s *StorageOverviewScreen) rack() *omsapi.StorageOverviewRack {
	if s.overview == nil || len(s.overview.Racks) == 0 {
		return nil
	}
	if r := s.overview.FindRack(s.rackNumber); r != nil {
		return r
	}
	return &s.overview.Racks[0]
}

func (s *StorageOverviewScreen) rackIndex() int {
	if s.overview == nil {
		return -1
	}
	for i := range s.overview.Racks {
		if s.overview.Racks[i].Rack == s.rackNumber {
			return i
		}
	}
	return -1
}

func (s *StorageOverviewScreen) cell() *omsapi.StorageOverviewCell {
	return s.rack().CellAt(s.level, s.position)
}

// resolveCursor pins the address onto whatever the fetch actually returned. It
// runs after every load, so a rack that vanished, a level that emptied out or a
// row that got narrower moves the cursor to a real place instead of leaving it
// pointing at nothing.
func (s *StorageOverviewScreen) resolveCursor() {
	r := s.rack()
	if r == nil {
		s.rackNumber, s.level, s.position = 0, "", 0
		return
	}
	s.rackNumber = r.Rack
	if !containsLevel(r.Levels, s.level) {
		if len(r.Levels) > 0 {
			s.level = r.Levels[0]
		} else {
			s.level = ""
		}
	}
	if s.position < 1 {
		s.position = 1
	}
	if s.position > r.MaxPosition {
		s.position = r.MaxPosition
	}
	s.scrollIntoView()
}

func containsLevel(levels []string, want string) bool {
	for _, l := range levels {
		if l == want {
			return true
		}
	}
	return false
}

func (s *StorageOverviewScreen) movePosition(delta int) {
	next, ok := s.positionTarget(delta)
	if !ok {
		return
	}
	s.position = next
	s.scrollIntoView()
}

// positionTarget is where h/l would put the cursor, and whether that is
// anywhere — the ONE predicate the arm and the bar both read, so a direction is
// named exactly where the key moves.
func (s *StorageOverviewScreen) positionTarget(delta int) (int, bool) {
	r := s.rack()
	if r == nil {
		return 0, false
	}
	next := s.position + delta
	return next, next >= 1 && next <= r.MaxPosition
}

// moveLevel steps DOWN the printed grid for a positive delta — the rows are in
// descending level order, so "j" moves toward the ground, which is what the
// picture shows.
func (s *StorageOverviewScreen) moveLevel(delta int) {
	next, ok := s.levelTarget(delta)
	if !ok {
		return
	}
	s.level = next
	s.scrollIntoView()
}

// levelTarget is positionTarget for j/k.
func (s *StorageOverviewScreen) levelTarget(delta int) (string, bool) {
	r := s.rack()
	if r == nil {
		return "", false
	}
	idx := levelIndex(r.Levels, s.level)
	next := idx + delta
	if idx < 0 || next < 0 || next >= len(r.Levels) {
		return "", false
	}
	return r.Levels[next], true
}

// levelIndex is where a level sits in the printed order, or -1.
func levelIndex(levels []string, level string) int {
	for i, l := range levels {
		if l == level {
			return i
		}
	}
	return -1
}

// moveRack pages between racks and lands on the top-left of the new one — a
// level letter and a position carried over from another rack mean nothing here.
func (s *StorageOverviewScreen) moveRack(delta int) {
	r, ok := s.rackTarget(delta)
	if !ok {
		return
	}
	s.rackNumber = r.Rack
	s.level = ""
	if len(r.Levels) > 0 {
		s.level = r.Levels[0]
	}
	s.position = 1
	s.colStart, s.rowStart = 0, 0
	s.scrollIntoView()
}

// rackTarget is the rack pgup/pgdn would land on. It answers ok for any loaded
// racking — the arm has always paged, and wraps — and rackPageMoves is what
// says whether the landing is anywhere the operator is not already standing.
func (s *StorageOverviewScreen) rackTarget(delta int) (*omsapi.StorageOverviewRack, bool) {
	if s.overview == nil || len(s.overview.Racks) == 0 {
		return nil, false
	}
	idx := s.rackIndex()
	if idx < 0 {
		idx = 0
	}
	next := (idx + delta + len(s.overview.Racks)) % len(s.overview.Racks)
	return &s.overview.Racks[next], true
}

// rackPageMoves reports whether pgup/pgdn change what the operator sees.
//
// NOT "is there a second rack", and the difference is a key that works unnamed.
// moveRack WRAPS and always lands on the top-left of the rack it reaches, so on
// racking with ONE rack the pair lands on the top-left of the rack already
// drawn: nothing from the first slot, and a real jump from anywhere else. A bar
// gated on a second rack would leave that jump unnamed; naming it "rack" there
// would be a claim about a rack that is not there. So the landing is compared
// with where the cursor stands, and storageRackPageHint words the two cases apart.
// (The window resets with the cursor and is a function of it, so an unmoved
// cursor is an unmoved pane.)
func (s *StorageOverviewScreen) rackPageMoves() bool {
	r, ok := s.rackTarget(+1)
	if !ok {
		return false
	}
	level := ""
	if len(r.Levels) > 0 {
		level = r.Levels[0]
	}
	return r.Rack != s.rackNumber || level != s.level || s.position != 1
}

// ---------------------------------------------------------------------------
// Windowing — a rack can be wider and taller than the pane
// ---------------------------------------------------------------------------

// gridColumns is how many POSITIONS fit beside the level-letter column.
//
// Measured against proseBarCells, the number every other line on this frame is
// folded or clipped against, so the grid and the words about it agree on where
// the pane ends. A level is one letter
// (OMS validates `^[A-Za-z]$`), so the letter column is one cell.
func (s *StorageOverviewScreen) gridColumns() int {
	avail := proseBarCells(s.terminalWidth) - 1 // the level-letter column
	if avail < 8 {
		avail = 8
	}
	return avail
}

// gridRows is how many LEVEL rows the window draws — storageGridLayout's answer.
func (s *StorageOverviewScreen) gridRows() int {
	return s.layout(s.rack()).rows
}

// storageGridLayout is the vertical give-order of the loaded frame, decided in
// one place so the window the arms scroll and the frame View draws are one
// answer.
type storageGridLayout struct {
	// rows is how many level rows the window draws.
	rows int
	// legend is whether the colour legend is drawn. It is a fixed explanation
	// the screen owns, so it is drawn WHOLE or not at all — proseRefusalFrame's
	// rule for its note — rather than losing a fold off its end that nothing
	// marks.
	legend bool
}

// layout is the frame's row budget, DERIVED from what will be drawn.
//
// WHAT IT REPLACES. The grid was budgeted by `const chrome = 11`: a header, two
// rulers, a cursor line, a legend and a hint of ONE row each. None of the four
// prose lines is one row at 80 columns — the header, the legend and the bar are
// each past the 51 cells the pane gives, and clampToBox took their tails with
// no mark: `r refresh` was never on the pane. Folding them is the fix, and a
// fold spends rows, so the budget has to be counted from the folds.
//
// THE GIVE-ORDER, stated. The foot — the bar, or the release confirm drawn in
// its place — never gives, and neither do the header, the rulers or the cursor
// line, which says what the cursor is on. The grid gives rows down to the
// window floor (or to the whole rack, where it is shorter); then the legend
// goes, whole; then the grid gives down to ONE level, the one the cursor is on.
// Below that the frame is taller than the pane, the band proseBarFrameFits
// scopes the sweeps around.
//
// BOTH SCROLL MARKERS are reserved whenever the rack is taller than the window,
// for the reason proseListFixedRows gives: a cursor in the middle draws both.
//
// THE BAR IS COUNTED AT ITS CEILING (storageGridCeilingRows), for the reason
// proseSizeScroller gives: which directions are named changes with the cursor,
// and a budget taken from the live bar would move the window under a key that
// only meant to move the cursor.
//
// AN UNSIZED SCREEN DRAWS EVERY LEVEL, the layer's standing answer for no pane.
func (s *StorageOverviewScreen) layout(r *omsapi.StorageOverviewRack) storageGridLayout {
	if r == nil {
		return storageGridLayout{legend: true}
	}
	levels := len(r.Levels)
	if s.terminalHeight <= 0 {
		return storageGridLayout{rows: levels, legend: true}
	}
	cells := proseBarCells(s.terminalWidth)
	fixed := len(s.headerLines(r, cells)) + 1 + // header, blank
		s.rulerRows(r) + // tens and digit rulers
		1 + 1 + // blank, cursor line
		s.footRows(cells)
	legend := 1 + len(storageGridLegendLines(cells)) // blank, legend
	avail := screenBodyRows(s.terminalHeight) - fixed
	fit := func(avail int) int {
		if levels <= avail {
			return levels
		}
		if rows := avail - 2; rows > 1 {
			return rows
		}
		return 1
	}
	floor := levels
	if floor > proseListWindowFloor {
		floor = proseListWindowFloor
	}
	if left := avail - legend; left >= floor+storageMarkerRows(levels, floor) {
		return storageGridLayout{rows: fit(left), legend: true}
	}
	return storageGridLayout{rows: fit(avail)}
}

// storageMarkerRows is the rows both scroll markers take over a window of
// `rows` levels: none where the whole rack fits.
func storageMarkerRows(levels, rows int) int {
	if levels <= rows {
		return 0
	}
	return 2
}

// rulerRows is how many ruler lines the grid draws: the digit ruler, and the
// tens ruler wherever a multiple of ten is on screen.
func (s *StorageOverviewScreen) rulerRows(r *omsapi.StorageOverviewRack) int {
	if storageGridTensRuler(s.colStart, s.visibleColumns(r)) != "" {
		return 2
	}
	return 1
}

// visibleColumns is how many positions the grid really draws: the window, or
// the rest of the rack past colStart where that is narrower.
func (s *StorageOverviewScreen) visibleColumns(r *omsapi.StorageOverviewRack) int {
	cols := s.gridColumns()
	if cols > r.MaxPosition-s.colStart {
		cols = r.MaxPosition - s.colStart
	}
	if cols < 1 {
		cols = 1
	}
	return cols
}

// footRows is what the foot takes, its blank separator included: the release
// confirm where it is up, the bar's ceiling otherwise.
func (s *StorageOverviewScreen) footRows(cells int) int {
	if s.confirmingRelease {
		return 1 + len(strings.Split(s.releaseConfirmText(cells), "\n"))
	}
	return storageGridCeilingRows(cells)
}

func (s *StorageOverviewScreen) scrollIntoView() {
	r := s.rack()
	if r == nil {
		s.colStart, s.rowStart = 0, 0
		return
	}

	cols := s.gridColumns()
	if r.MaxPosition <= cols {
		s.colStart = 0
	} else {
		if s.position-1 < s.colStart {
			s.colStart = s.position - 1
		}
		if s.position-1 >= s.colStart+cols {
			s.colStart = s.position - cols
		}
		if max := r.MaxPosition - cols; s.colStart > max {
			s.colStart = max
		}
		if s.colStart < 0 {
			s.colStart = 0
		}
	}

	rows := s.gridRows()
	idx := levelIndex(r.Levels, s.level)
	if idx < 0 {
		idx = 0
	}
	if len(r.Levels) <= rows {
		s.rowStart = 0
		return
	}
	if idx < s.rowStart {
		s.rowStart = idx
	}
	if idx >= s.rowStart+rows {
		s.rowStart = idx - rows + 1
	}
	if max := len(r.Levels) - rows; s.rowStart > max {
		s.rowStart = max
	}
	if s.rowStart < 0 {
		s.rowStart = 0
	}
}

// ---------------------------------------------------------------------------
// View
// ---------------------------------------------------------------------------

func (s *StorageOverviewScreen) View() string {
	cells := proseBarCells(s.terminalWidth)
	if s.loading {
		return proseLoadingFrame("Loading the racking…", cells, s.loadBar())
	}
	if s.loadErr != "" {
		return proseRefusalFrame("Error: ", StyleStatusError, s.loadErr,
			"the overview is staff / Storage Admin only", s.terminalHeight, cells, s.loadBar())
	}
	r := s.rack()
	if r == nil {
		return pickerHintAt("No racking yet.\nStorage slots (Facilities → R) is where a rack is generated.", cells) +
			"\n\n" + s.proseBar().render(cells)
	}

	// The window is a function of the pane, the rack and the cursor, and
	// scrollIntoView is idempotent given the three — so asking it here, where
	// the frame is drawn, cannot disagree with the frame. It is asked here at
	// all because the layout also depends on the FOOT: the release confirm is
	// not the bar's height, and `x` opening it moves no cursor.
	s.scrollIntoView()
	lay := s.layout(r)

	var b strings.Builder
	b.WriteString(strings.Join(s.headerLines(r, cells), "\n") + "\n\n")
	b.WriteString(s.gridBlock(r, lay.rows))
	b.WriteString("\n")
	b.WriteString(s.cursorLine(cells) + "\n")
	if lay.legend {
		b.WriteString("\n" + strings.Join(storageGridLegendLines(cells), "\n") + "\n")
	}

	if s.confirmingRelease {
		b.WriteString("\n" + s.releaseConfirmText(cells))
		return b.String()
	}
	b.WriteString("\n" + s.proseBar().render(cells))
	return b.String()
}

// ---------------------------------------------------------------------------
// The bar
// ---------------------------------------------------------------------------

// storageGridOffers is which of the grid's keys would act where the cursor
// stands — every field is a question an arm answers the same way.
type storageGridOffers struct {
	left, right, up, down bool
	home, end             bool
	// racks is whether pgup/pgdn move the pane, and otherRack whether they land
	// on a DIFFERENT rack — see rackPageMoves.
	racks, otherRack bool
	open             bool // enter: there is a slot under the cursor
	assign           bool // a: that slot is free and in service
	release          bool // x: that slot holds a C/L/E assignment
}

// offers reads storageGridOffers off the screen.
//
// THE DIRECTIONS ARE GATED ONE AT A TIME, because the movement is two
// dimensional and each axis has two edges. The cursor lists this record grew
// up on name `j/k ↑↓` as one PAIR wherever there is a second row, and on a
// list that is the right grain: a cursor at the top has one direction to go
// and the bar says there is movement. A grid cursor in a corner has two of four
// directions, and one on a rack a single level tall has no vertical movement at
// all — so a pair-grained bar would name `k ↑` in the top row of a tall rack,
// where it does nothing, and the whole vertical pair over a one-level rack.
// Each direction is asked of the SAME predicate its arm moves by
// (positionTarget, levelTarget), so a name and a move cannot come apart.
//
// `home`/`end` set the position unconditionally, so they move exactly where the
// cursor is not already on that position.
//
// The row actions follow the cell, as on the slot detail: `a` where
// openAssign would open the form, `x` where startRelease would open the
// confirm. Elsewhere those keys still answer, with a toast naming the remedy —
// a decline that says why, which changes no pane and so is not an act the bar
// owes a word to.
func (s *StorageOverviewScreen) offers() storageGridOffers {
	var o storageGridOffers
	r := s.rack()
	if r == nil {
		return o
	}
	_, o.left = s.positionTarget(-1)
	_, o.right = s.positionTarget(+1)
	_, o.up = s.levelTarget(-1)
	_, o.down = s.levelTarget(+1)
	o.home = s.position != 1
	o.end = s.position != r.MaxPosition
	o.racks = s.rackPageMoves()
	o.otherRack = o.racks && len(s.overview.Racks) > 1
	if cell := s.cell(); cell != nil {
		o.open = true
		o.assign = storageCellAssignable(cell)
		o.release = storageCellReleasable(cell)
	}
	return o
}

// storageCellAssignable is openAssign's "this opens the form" — a free slot in
// service.
func storageCellAssignable(cell *omsapi.StorageOverviewCell) bool {
	return cell.Type == "" && cell.IsActive
}

// storageCellReleasable is startRelease's "this opens the confirm" — a C/L/E
// holding, never a project stint.
func storageCellReleasable(cell *omsapi.StorageOverviewCell) bool {
	return cell.Type != "" && cell.Type != omsapi.StorageTypeLetterProject
}

// storageGridBar is the bar for a set of offers. The order is the order drawn:
// the movement, the row actions, then the reload and the way back.
//
// A DIRECTION SPELLS BOTH ITS KEYS — the letter and the arrow — because the arms
// bind both, and a pair axis whose two directions both move is ONE segment so
// the bar does not spend a joint on each.
//
// THE WORDS ARE CUT TO FOLD THE CEILING ONTO THREE ROWS at the 51 cells an
// 80-column pane gives, and every row a fold spends is a level row the grid does
// not draw. `home/end row ends` rather than `first/last position` is the whole of
// the fourth row: the longer wording put the first fold one cell past the pane.
func storageGridBar(o storageGridOffers) proseBar {
	var out proseBar
	axis := func(back, fwd bool, backKeys, fwdKeys [2]string, noun string) {
		switch {
		case back && fwd:
			out = append(out, proseBarItem{
				Keys: []string{backKeys[0], fwdKeys[0], backKeys[1], fwdKeys[1]},
				Hint: backKeys[0] + "/" + fwdKeys[0] + " " + storageArrow(backKeys[1]) + storageArrow(fwdKeys[1]) + " " + noun,
			})
		case back:
			out = append(out, proseBarItem{Keys: backKeys[:], Hint: backKeys[0] + " " + storageArrow(backKeys[1]) + " " + noun})
		case fwd:
			out = append(out, proseBarItem{Keys: fwdKeys[:], Hint: fwdKeys[0] + " " + storageArrow(fwdKeys[1]) + " " + noun})
		}
	}
	axis(o.left, o.right, [2]string{"h", "left"}, [2]string{"l", "right"}, "position")
	axis(o.up, o.down, [2]string{"k", "up"}, [2]string{"j", "down"}, "level")
	switch {
	case o.home && o.end:
		out = append(out, proseBarItem{Keys: []string{"home", "end"}, Hint: "home/end row ends"})
	case o.home:
		out = append(out, proseBarItem{Keys: []string{"home"}, Hint: "home row start"})
	case o.end:
		out = append(out, proseBarItem{Keys: []string{"end"}, Hint: "end row end"})
	}
	if o.racks {
		out = append(out, proseBarItem{Keys: []string{"pgup", "pgdown"}, Hint: storageRackPageHint(o.otherRack)})
	}
	if o.open {
		out = append(out, proseBarItem{Keys: []string{"enter"}, Hint: "enter open slot"})
	}
	if o.assign {
		out = append(out, proseBarItem{Keys: []string{"a"}, Hint: "a assign C/L/E"})
	}
	if o.release {
		out = append(out, proseBarItem{Keys: []string{"x"}, Hint: "x release"})
	}
	return append(out, proseBarRefresh, proseBarEsc)
}

// storageRackPageHint words pgup/pgdn: a page to another rack, or — on racking
// with one rack — the jump to its top-left that moveRack's wrap makes.
func storageRackPageHint(otherRack bool) string {
	if otherRack {
		return "pgup/pgdn rack"
	}
	return "pgup/pgdn top-left"
}

// storageArrow is the glyph a direction's arrow key is drawn as.
func storageArrow(key string) string {
	return map[string]string{"left": "←", "right": "→", "up": "↑", "down": "↓"}[key]
}

// storageGridCeilingRows is the TALLEST the bar can fold to at `cells`: every
// direction, both jumps, every row action and the longer rack wording at once.
//
// No cursor draws that union — `a` and `x` never share a cell, and the two rack
// wordings never share racking — and it does not need to: a ceiling only has to
// be at least as tall as any bar that IS drawn, and the bars fold at their ` · `
// joints greedily, where taking a segment out never adds a row.
func storageGridCeilingRows(cells int) int {
	all := storageGridOffers{
		left: true, right: true, up: true, down: true, home: true, end: true,
		racks: true, open: true, assign: true, release: true,
	}
	rows := storageGridBar(all).rows(cells)
	all.otherRack = true
	if other := storageGridBar(all).rows(cells); other > rows {
		rows = other
	}
	return rows
}

// proseBar is the bar the grid is DRAWING: nil while the release confirm is up,
// which names its own two keys in the bar's place. A load in flight or failed
// draws loadBar's; racking with no rack at all offers the reload and the way
// out, since every other key there answers with a toast.
func (s *StorageOverviewScreen) proseBar() proseBar {
	if s.loading || s.loadErr != "" {
		return s.loadBar()
	}
	if s.confirmingRelease {
		return nil
	}
	return storageGridBar(s.offers())
}

// loadBar is the bar while the load is out or has failed — what the key switch
// answers with no grid drawn (prose_bar.go carries the defect and the decision).
// On a FIRST load there is no racking, so the reload and the way back are all.
// A REFRESH keeps the grid it had, and two keys still act on the cell under
// that cursor, which the frame no longer draws: `enter` opens the slot and `a`
// opens the assign form where the cell is free. Named because they act, and
// candidates for gating. The movement keys and `x` are not named, and the gate
// in Update ignores them: all they would do is move a cursor nothing draws, or
// arm a confirm nobody can read.
func (s *StorageOverviewScreen) loadBar() proseBar {
	var out proseBar
	if cell := s.cell(); cell != nil {
		out = append(out, proseBarItem{Keys: []string{"enter"}, Hint: "enter open slot"})
		if storageCellAssignable(cell) {
			out = append(out, proseBarItem{Keys: []string{"a"}, Hint: "a assign C/L/E"})
		}
	}
	return append(out, proseBarReloadFor(s.loadErr != ""), proseBarEsc)
}

// ---------------------------------------------------------------------------
// The frame's lines
// ---------------------------------------------------------------------------

// headerLines is the rack's summary folded to the pane: which rack, the slot
// counts, which positions the window shows, and how many cells need attention.
//
// FOLDED, where it used to be one line past the pane: at 80 columns a paged
// rack's summary is past 51 cells before the window slice or the attention count
// is added, and clampToBox took the tail with no mark — which is where both of
// those facts sit. The attention count keeps its warning colour on whichever
// fold it lands.
func (s *StorageOverviewScreen) headerLines(r *omsapi.StorageOverviewRack, cells int) []string {
	parts := []string{fmt.Sprintf("Rack %d", r.Rack)}
	if n := len(s.overview.Racks); n > 1 {
		idx := s.rackIndex()
		if idx < 0 {
			idx = 0
		}
		parts[0] += fmt.Sprintf(" (%d of %d — pgup/pgdn)", idx+1, n)
	}

	var slots, free, held, retired, attention int
	for _, row := range r.Rows {
		for _, cell := range row.Cells {
			if cell == nil {
				continue
			}
			slots++
			switch {
			case cell.Type != "":
				held++
			case !cell.IsActive:
				retired++
			default:
				free++
			}
			if cell.Color != "" {
				attention++
			}
		}
	}
	parts = append(parts, fmt.Sprintf("%d slots", slots), fmt.Sprintf("%d free", free), fmt.Sprintf("%d held", held))
	if retired > 0 {
		parts = append(parts, fmt.Sprintf("%d out of service", retired))
	}
	if cols := s.gridColumns(); r.MaxPosition > cols {
		parts = append(parts, fmt.Sprintf("positions %d–%d of %d",
			s.colStart+1, s.colStart+cols, r.MaxPosition))
	}

	warn := ""
	if attention > 0 {
		warn = fmt.Sprintf("%d need attention", attention)
		parts = append(parts, warn)
	}
	lines := pickerWrap(strings.Join(parts, " · "), cells)
	for i, line := range lines {
		if warn != "" && strings.HasSuffix(line, warn) {
			lines[i] = StyleMuted.Render(strings.TrimSuffix(line, warn)) + StyleStatusWarn.Render(warn)
			continue
		}
		lines[i] = StyleMuted.Render(line)
	}
	return lines
}

// gridBlock draws the rulers and a window of `rows` level rows.
func (s *StorageOverviewScreen) gridBlock(r *omsapi.StorageOverviewRack, rows int) string {
	cols := s.visibleColumns(r)

	var b strings.Builder
	if tens := storageGridTensRuler(s.colStart, cols); tens != "" {
		b.WriteString(StyleMuted.Render(tens) + "\n")
	}
	b.WriteString(StyleMuted.Render(storageGridPositionRuler(s.colStart, cols)) + "\n")

	end := s.rowStart + rows
	if end > len(r.Rows) {
		end = len(r.Rows)
	}
	if s.rowStart > 0 {
		b.WriteString(StyleMuted.Render(fmt.Sprintf(" ↑ %d more level(s) above", s.rowStart)) + "\n")
	}
	for i := s.rowStart; i < end; i++ {
		b.WriteString(s.renderGridRow(r.Rows[i], s.colStart, cols) + "\n")
	}
	if end < len(r.Rows) {
		b.WriteString(StyleMuted.Render(fmt.Sprintf(" ↓ %d more level(s) below", len(r.Rows)-end)) + "\n")
	}
	return b.String()
}

// storageGridPositionRuler draws the LAST DIGIT of each position, offset by one
// column for the level letters — the classic rack ruler, where 1234567890
// repeats and the tens row above says which ten you are in.
func storageGridPositionRuler(colStart, cols int) string {
	var b strings.Builder
	b.WriteByte(' ')
	for i := 0; i < cols; i++ {
		b.WriteByte(byte('0' + (colStart+i+1)%10))
	}
	return b.String()
}

// storageGridTensRuler labels the groups of ten above the digit ruler, each
// label ENDING on its own column (so "10" ends on position 100). Returns "" when
// no multiple of ten is on screen — a short rack needs no second ruler line.
func storageGridTensRuler(colStart, cols int) string {
	buf := []byte(strings.Repeat(" ", 1+cols))
	drew := false
	for p := colStart + 1; p <= colStart+cols; p++ {
		if p%10 != 0 {
			continue
		}
		label := strconv.Itoa(p / 10)
		// buf[0] is the level-letter column, so position p sits at p-colStart.
		// The label ENDS on its own column (so "10" ends on position 100) and
		// is clipped on the left if it would spill into the letter column.
		end := p - colStart
		start := end - len(label) + 1
		trim := 0
		if start < 1 {
			trim, start = 1-start, 1
		}
		if trim < len(label) {
			copy(buf[start:], label[trim:])
			drew = true
		}
	}
	if !drew {
		return ""
	}
	return string(buf)
}

// renderGridRow paints one level. Consecutive cells of the same class are
// rendered as ONE styled run so a 40-wide rack is a handful of escape sequences
// rather than one per character.
func (s *StorageOverviewScreen) renderGridRow(row omsapi.StorageOverviewRow, colStart, cols int) string {
	var b strings.Builder
	b.WriteString(StyleMuted.Render(row.Level))

	runClass, run := "", strings.Builder{}
	flush := func() {
		if run.Len() == 0 {
			return
		}
		b.WriteString(storageGridStyles[runClass].Render(run.String()))
		run.Reset()
	}

	for i := 0; i < cols; i++ {
		position := colStart + i + 1
		var cell *omsapi.StorageOverviewCell
		if position-1 < len(row.Cells) {
			cell = row.Cells[position-1]
		}
		glyph := storageGridGlyph(cell)
		class := storageGridCellClass(cell)

		// The cursor cell is drawn reversed rather than recoloured, so it stays
		// unmistakable without hiding what colour the cell actually is.
		if row.Level == s.level && position == s.position {
			flush()
			b.WriteString(storageGridStyles[class].Reverse(true).Render(glyph))
			runClass = ""
			continue
		}
		if class != runClass {
			flush()
			runClass = class
		}
		run.WriteString(glyph)
	}
	flush()
	return b.String()
}

// cursorLine says what the cell under the cursor actually is — the grid's one
// character can only carry so much, and this is where "who is in 1A4, and why
// is it yellow?" gets answered.
//
// ONE ROW, CLIPPED WITH THE CUT MARKED. The occupant is an OMS display name and
// is the only unbounded part, so it is what gives: the code leads and the status
// trails, and the words between are cut to the cells those two leave. It is not
// folded, because a row count that changed with the cell under the cursor would
// move the window on a key that only meant to move the cursor. It used to be
// cut at 60 runes and then again, unmarked, at the pane's 51 cells.
func (s *StorageOverviewScreen) cursorLine(cells int) string {
	r := s.rack()
	if r == nil {
		return ""
	}
	addr := fmt.Sprintf("%d%s%d", r.Rack, s.level, s.position)
	cell := s.cell()
	if cell == nil {
		return StyleTitle.Render(addr) + StyleMuted.Render(pickerClip("  no slot at this position", cells-lipgloss.Width(addr)))
	}

	parts := []string{}
	if cell.Type != "" {
		parts = append(parts, storageTypeName(cell.Type))
		if occ := strings.TrimSpace(cell.Occupant); occ != "" {
			parts = append(parts, occ)
		}
	} else {
		parts = append(parts, "free")
	}
	if !cell.IsActive {
		parts = append(parts, "out of service — not offered")
	}

	code := pickerClip(jdeStatusOneLine(cell.Code), cells)
	status, statusStyle := "", StyleMuted
	if cell.Status != "" && cell.Status != omsapi.StorageOverviewStatusEmpty {
		status = "  " + jdeStatusOneLine(strings.ReplaceAll(cell.Status, "_", " "))
		if class := storageGridCellClass(cell); class == omsapi.StorageOverviewColorRed ||
			class == omsapi.StorageOverviewColorYellow {
			statusStyle = storageGridStyles[class]
		}
	}
	room := cells - lipgloss.Width(code) - 2 - lipgloss.Width(status)
	if room < 1 && status != "" {
		// A pane too narrow for the status beside the code keeps the code and
		// what the cell holds, which is what the line is for.
		status = ""
		room = cells - lipgloss.Width(code) - 2
	}
	line := StyleTitle.Render(code)
	if room > 0 {
		line += "  " + pickerClip(jdeStatusOneLine(strings.Join(parts, " · ")), room)
	}
	if status != "" {
		line += statusStyle.Render(status)
	}
	return line
}

// storageGridLegendSegments are the legend's claims, in the order drawn, with
// the colour each is written in. Only the two colour claims are coloured — they
// are the ones that say what a coloured cell MEANS.
var storageGridLegendSegments = []struct {
	text  string
	style lipgloss.Style
}{
	{"P=Project  C=Committee  L=Logistics  E=Class", StyleMuted},
	{". = empty", StyleMuted},
	{"yellow = expiring soon", storageGridStyles[omsapi.StorageOverviewColorYellow]},
	{"red = expired, move to purgatory", storageGridStyles[omsapi.StorageOverviewColorRed]},
	{"dim = no slot / out of service", StyleMuted},
}

// storageGridLegendLines is the legend folded to the pane at its ` · ` joints,
// each claim in its own colour.
//
// FOLDED, where it used to be one line of about 130 cells: at 80 columns
// clampToBox kept the letters and took every colour claim off the pane, with no
// mark — so the one screen built to be read by its colours never said what they
// meant.
func storageGridLegendLines(cells int) []string {
	texts := make([]string, len(storageGridLegendSegments))
	styles := map[string]lipgloss.Style{}
	for i, seg := range storageGridLegendSegments {
		texts[i] = seg.text
		styles[seg.text] = seg.style
	}
	lines := pickerWrap(strings.Join(texts, " · "), cells)
	for i, line := range lines {
		body := strings.TrimLeft(line, " ")
		indent := line[:len(line)-len(body)]
		pieces := strings.Split(body, " · ")
		for j, piece := range pieces {
			style, ok := styles[piece]
			if !ok {
				style = StyleMuted
			}
			pieces[j] = style.Render(piece)
		}
		lines[i] = indent + strings.Join(pieces, StyleMuted.Render(" · "))
	}
	return lines
}

func (s *StorageOverviewScreen) noSlotText() string {
	r := s.rack()
	if r == nil {
		return "no racking loaded"
	}
	return fmt.Sprintf("no slot at %d%s%d — the racking has a hole there", r.Rack, s.level, s.position)
}

// releaseConfirmText is the confirm drawn in the bar's place, folded to the
// pane. It names its own two keys, the shape every y/n confirm on these screens
// keeps, and layout counts its folded rows as the foot — the question and the
// keys that answer it are what a short pane must not take.
func (s *StorageOverviewScreen) releaseConfirmText(cells int) string {
	cell := s.cell()
	if cell == nil {
		return ""
	}
	if s.releasing {
		return StyleMuted.Render(pickerClip("Releasing…", cells))
	}
	fold := func(style lipgloss.Style, text string) string {
		lines := pickerWrap(text, cells)
		for i, line := range lines {
			lines[i] = style.Render(line)
		}
		return strings.Join(lines, "\n")
	}
	return fold(StyleStatusWarn, jdeStatusOneLine(fmt.Sprintf("Release %s from %s (%s)?",
		cell.Code, firstNonEmpty(cell.Occupant, "its holder"), storageTypeName(cell.Type)))) + "\n" +
		fold(StyleMuted, "The holding is kept as history — the slot just becomes assignable again.") + "\n" +
		fold(StyleMuted, "y release · n/esc cancel")
}
