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
// Keys: h/j/k/l or arrows move · enter open the slot · a assign · x release ·
// pgup/pgdn page racks · r refresh.
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
	r := s.rack()
	if r == nil {
		return
	}
	next := s.position + delta
	if next < 1 || next > r.MaxPosition {
		return
	}
	s.position = next
	s.scrollIntoView()
}

// moveLevel steps DOWN the printed grid for a positive delta — the rows are in
// descending level order, so "j" moves toward the ground, which is what the
// picture shows.
func (s *StorageOverviewScreen) moveLevel(delta int) {
	r := s.rack()
	if r == nil {
		return
	}
	idx := -1
	for i, l := range r.Levels {
		if l == s.level {
			idx = i
			break
		}
	}
	next := idx + delta
	if idx < 0 || next < 0 || next >= len(r.Levels) {
		return
	}
	s.level = r.Levels[next]
	s.scrollIntoView()
}

// moveRack pages between racks and lands on the top-left of the new one — a
// level letter and a position carried over from another rack mean nothing here.
func (s *StorageOverviewScreen) moveRack(delta int) {
	if s.overview == nil || len(s.overview.Racks) == 0 {
		return
	}
	idx := s.rackIndex()
	if idx < 0 {
		idx = 0
	}
	next := (idx + delta + len(s.overview.Racks)) % len(s.overview.Racks)
	r := s.overview.Racks[next]
	s.rackNumber = r.Rack
	s.level = ""
	if len(r.Levels) > 0 {
		s.level = r.Levels[0]
	}
	s.position = 1
	s.colStart, s.rowStart = 0, 0
	s.scrollIntoView()
}

// ---------------------------------------------------------------------------
// Windowing — a rack can be wider and taller than the pane
// ---------------------------------------------------------------------------

// gridColumns is how many POSITIONS fit beside the level-letter column.
func (s *StorageOverviewScreen) gridColumns() int {
	avail := screenBodyWidth(s.terminalWidth) - 1 // the level-letter column
	if avail < 8 {
		avail = 8
	}
	return avail
}

// gridRows is how many LEVEL rows fit. The chrome is the header, the two ruler
// lines, the cursor line, the legend and the hint, plus their separators.
func (s *StorageOverviewScreen) gridRows() int {
	const chrome = 11
	avail := screenBodyHeight(s.terminalHeight) - chrome
	if avail < 3 {
		avail = 3
	}
	return avail
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
	idx := 0
	for i, l := range r.Levels {
		if l == s.level {
			idx = i
			break
		}
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
	if s.loading {
		return StyleMuted.Render("Loading the racking…")
	}
	if s.loadErr != "" {
		return StyleStatusError.Render("Error: ") + s.loadErr + "\n\n" +
			StyleMuted.Render("press r to retry · the overview is staff / Storage Admin only")
	}
	r := s.rack()
	if r == nil {
		return StyleMuted.Render("No racking yet.") + "\n" +
			StyleMuted.Render("Storage slots (Facilities → R) is where a rack is generated.")
	}

	var b strings.Builder
	b.WriteString(s.headerLine(r) + "\n\n")
	b.WriteString(s.gridBlock(r))
	b.WriteString("\n")
	b.WriteString(s.cursorLine() + "\n\n")
	b.WriteString(storageGridLegend() + "\n")

	if s.confirmingRelease {
		b.WriteString(s.releaseConfirmText())
		return b.String()
	}
	b.WriteString(StyleMuted.Render(
		"h/j/k/l move · enter open slot · a assign C/L/E · x release · pgup/pgdn rack · r refresh"))
	return b.String()
}

func (s *StorageOverviewScreen) headerLine(r *omsapi.StorageOverviewRack) string {
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

	line := StyleMuted.Render(strings.Join(parts, " · "))
	if attention > 0 {
		line += "  " + StyleStatusWarn.Render(fmt.Sprintf("%d need attention", attention))
	}
	return line
}

// gridBlock draws the rulers and the level rows.
func (s *StorageOverviewScreen) gridBlock(r *omsapi.StorageOverviewRack) string {
	cols := s.gridColumns()
	if cols > r.MaxPosition-s.colStart {
		cols = r.MaxPosition - s.colStart
	}
	if cols < 1 {
		cols = 1
	}

	var b strings.Builder
	if tens := storageGridTensRuler(s.colStart, cols); tens != "" {
		b.WriteString(StyleMuted.Render(tens) + "\n")
	}
	b.WriteString(StyleMuted.Render(storageGridPositionRuler(s.colStart, cols)) + "\n")

	rows := s.gridRows()
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
func (s *StorageOverviewScreen) cursorLine() string {
	r := s.rack()
	if r == nil {
		return ""
	}
	addr := fmt.Sprintf("%d%s%d", r.Rack, s.level, s.position)
	cell := s.cell()
	if cell == nil {
		return StyleTitle.Render(addr) + StyleMuted.Render("  no slot at this position")
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

	line := StyleTitle.Render(cell.Code) + "  " + truncateOneLine(strings.Join(parts, " · "), 60)
	if cell.Status != "" && cell.Status != omsapi.StorageOverviewStatusEmpty {
		status := strings.ReplaceAll(cell.Status, "_", " ")
		if class := storageGridCellClass(cell); class == omsapi.StorageOverviewColorRed ||
			class == omsapi.StorageOverviewColorYellow {
			line += "  " + storageGridStyles[class].Render(status)
		} else {
			line += StyleMuted.Render("  " + status)
		}
	}
	return line
}

func storageGridLegend() string {
	return StyleMuted.Render("P=Project  C=Committee  L=Logistics  E=Class  ·  . = empty  ·  ") +
		storageGridStyles[omsapi.StorageOverviewColorYellow].Render("yellow = expiring soon") +
		StyleMuted.Render("  ") +
		storageGridStyles[omsapi.StorageOverviewColorRed].Render("red = expired, move to purgatory") +
		StyleMuted.Render("  ·  dim = no slot / out of service")
}

func (s *StorageOverviewScreen) noSlotText() string {
	r := s.rack()
	if r == nil {
		return "no racking loaded"
	}
	return fmt.Sprintf("no slot at %d%s%d — the racking has a hole there", r.Rack, s.level, s.position)
}

func (s *StorageOverviewScreen) releaseConfirmText() string {
	cell := s.cell()
	if cell == nil {
		return ""
	}
	if s.releasing {
		return StyleMuted.Render("Releasing…")
	}
	body := StyleStatusWarn.Render(fmt.Sprintf("Release %s from %s (%s)?",
		cell.Code, firstNonEmpty(cell.Occupant, "its holder"), storageTypeName(cell.Type))) + "\n"
	body += StyleMuted.Render("The holding is kept as history — the slot just becomes assignable again.") + "\n"
	body += StyleMuted.Render("y release · n/esc cancel")
	return body
}
