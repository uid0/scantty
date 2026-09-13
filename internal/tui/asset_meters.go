// AssetMetersScreen — an asset's usage meters, and the bench actions on them.
//
// THIS IS THE SCREEN THE TASK EXISTS FOR. An operator standing at a machine
// reads a number off its display; until now they had to walk back to a browser
// to write it down. Everything else here — defining a meter, reading the ledger
// — is in service of that one gesture, which is why it is the key Enter is bound
// to on the grid.
//
// RECORD AND ADJUST ARE DIFFERENT OPERATIONS AND THE SCREEN NEVER BLURS THEM.
// A reading says what the machine says NOW; an adjustment CORRECTS the history
// and the server requires a reason for it, which it stores on the ledger row.
// They are separate phases with separate headings, separate bar labels, separate
// status wording and separate submit paths, because they land as different rows
// (`manual` vs `manual_adjust`) in an append-only ledger that somebody will read
// months later to work out what happened. Collapsing them into one form with a
// flag would be cheaper here and would destroy that distinction at the point it
// is made.
//
// EVERY VALUE CARRIES ITS UNIT. A runtime-hour reading and a cycle count are not
// interchangeable, and an unlabelled number invites the wrong entry — so the
// grid, both forms' hints, the confirm and the status line all draw the figure
// through meterFigure (asset_meter_value.go) and never a bare number.
//
// THE NUMBER IS SHOWN WHOLE OR DROPPED AND MARKED. The value column is a FACT in
// the report-table sense: it never gives ground, the NAME abbreviates around it,
// and where a pane is too narrow to hold even the figure the column is DROPPED
// and the header says so (meterGridPlan). A cut figure reads as a different
// number, which on this screen is the whole of what is being communicated.
//
// Keys, and the reasoning behind the two that are new to this file:
//
//	Enter      record a reading on the highlighted meter — the bench action
//	Ctrl-A     adjust it (a correction, with a reason)
//	Ctrl-E     open what the row IS: the meter's reading ledger
//	n          define a new manual meter on this asset
//	r          refresh
//	Esc        back to the asset
//	UP/DN      move · PgUp/PgDn page, exactly where the layer says they move
//
// Ctrl-A is a chord because it is the only spelling of an operation that has no
// other one — unlike the emacs movement chords list_nav.go retired, which
// duplicated keys their bars already named. Ctrl-N is deliberately NOT used for
// the new-meter form: it is one of the four RETIRED chords, and rebinding a key
// this program has taught operators does nothing would be worse than spending a
// letter. `n` is that letter, and it matches the `r` the sibling attachment grid
// already binds bare.
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

// assetLabelW is the label column the asset meter and document HEADERS share.
//
// renderJDEField TRUNCATES a label wider than the width it is handed, with no
// mark — so a header row built at the attachment grid's 4 drew "Document" as
// "Docu" and "Now reads" as "Now ", on the frames that name what an
// irreversible key is about to act on. Nine cells is the widest label any of
// them carries ("Now reads"), and one column shared across both screens is what
// keeps their headers reading alike.
const assetLabelW = 9

type meterPhase int

const (
	meterPhaseList meterPhase = iota
	meterPhaseRecord
	meterPhaseAdjust
	meterPhaseNew
	// meterPhaseConfirm is the one frame that asks before writing, and it is
	// reached ONLY from the record / adjust forms and only when the entry is
	// suspicious (asset_meter_sanity.go).
	meterPhaseConfirm
	meterPhaseCount
)

// Record-form rows. Basis and Estimated are choice rows (←/→), Value is typed.
const (
	meterRecordValue = iota
	meterRecordBasis
	meterRecordEstimated
	meterRecordFieldCount
)

// Adjust-form rows. Both are typed and BOTH are required — the server refuses a
// blank reason with a 400, so the form asks for it rather than discovering it.
const (
	meterAdjustTarget = iota
	meterAdjustReason
	meterAdjustFieldCount
)

// New-meter rows.
const (
	meterNewName = iota
	meterNewType
	meterNewUnit
	meterNewFieldCount
)

// meterTypeOptions is the choice set for a new meter, transcribed from OMS's
// AssetMeter.MeterType with the unit the web form defaults alongside it.
//
// The unit is PREFILLED from the type and stays editable, because the server
// takes free text there and a shop's own word for it ("hrs", "gal") is what the
// operator will recognise on the grid afterwards.
var meterTypeOptions = []struct {
	Value string
	Label string
	Unit  string
}{
	{omsapi.MeterTypeRuntimeHours, "Runtime hours", "hours"},
	{omsapi.MeterTypeVolumeGallons, "Volume (gallons)", "gallons"},
	{omsapi.MeterTypeCycles, "Cycles", "cycles"},
	{omsapi.MeterTypeKWh, "Energy (kWh)", "kWh"},
	{omsapi.MeterTypeGenericCount, "Generic count", "count"},
}

type AssetMetersScreen struct {
	deps          Deps
	assetID       string
	assetName     string
	meters        []omsapi.AssetMeter
	loading       bool
	loadErr       string
	loadConfirmed bool
	loadSeq       int
	jdeScreen

	phase  meterPhase
	cursor int
	errMsg string
	// note is the screen's answer to the last keypress — what it DID, at a
	// level. It rides the layer's status row, which no budget can trim.
	note      string
	noteLevel StatusLevel

	// Record form.
	valueInput      textinput.Model
	recordAbsolute  bool
	recordEstimated bool
	recordFocus     int

	// Adjust form.
	targetInput textinput.Model
	reasonInput textinput.Model
	adjustFocus int

	// New-meter form.
	nameInput   textinput.Model
	unitInput   textinput.Model
	newTypeIdx  int
	newFocus    int
	newUnitEdit bool

	// A write in flight, and the confirm standing between a suspicious entry
	// and that write.
	saving bool
	// pending is what the confirm is about: which action, against which meter,
	// with what verdict. confirmFrom is the phase Esc returns to, so the
	// operator lands back on their own typed value rather than on the grid.
	pendingAdjust bool
	pendingMeter  omsapi.AssetMeter
	pendingVal    meterEntryVerdict
	confirmFrom   meterPhase
	confirmScroll int
}

type assetMetersLoadedMsg struct {
	meters []omsapi.AssetMeter
	err    error
	seq    int
}

type meterReadingWrittenMsg struct {
	res    *omsapi.MeterReadingResult
	adjust bool
	err    error
}

type meterCreatedMsg struct {
	meter *omsapi.AssetMeter
	err   error
}

func NewAssetMetersScreen(deps Deps, assetID, assetName string) *AssetMetersScreen {
	s := &AssetMetersScreen{
		deps:      deps,
		assetID:   assetID,
		assetName: assetName,
		loading:   true,
		// An ABSOLUTE reading is the default because it is what a machine's
		// display gives you: the operator reads a total off the panel. It is
		// also the server's default, so the two agree — and the request sends
		// the flag explicitly anyway (RecordMeterReadingRequest's note).
		recordAbsolute: true,
	}

	// No placeholders on a columnar sheet: a placeholder fills the fixed input
	// area and hides the underscores that say the field is empty, so what the
	// field wants rides beside it as a Hint.
	mk := func(limit int) textinput.Model {
		in := textinput.New()
		in.Prompt = ""
		in.CharLimit = limit
		return in
	}
	// 20 characters is more than numeric(14,4) can hold, so the box never
	// truncates a number the record could have stored — the add-line cost box's
	// lesson, where a CharLimit of 14 cut a value and Enter posted the fragment.
	s.valueInput = mk(20)
	s.targetInput = mk(20)
	s.reasonInput = mk(300)
	s.nameInput = mk(100)
	s.unitInput = mk(20)
	s.unitInput.SetValue(meterTypeOptions[0].Unit)
	return s
}

func (s *AssetMetersScreen) Title() string {
	if s.assetName != "" {
		return fmt.Sprintf("Meters · %s", s.assetName)
	}
	return "Asset meters"
}

// WantsRawInput claims every key on the three forms and on the confirm, so the
// typed boxes and the confirm's own keys land here rather than in the root's
// global layer. The grid stays non-raw so workspace switching and the global
// back-step keep working while browsing.
func (s *AssetMetersScreen) WantsRawInput() bool { return s.phase != meterPhaseList }

func (s *AssetMetersScreen) Init() tea.Cmd { return s.beginLoad() }

func (s *AssetMetersScreen) ctx() context.Context {
	if s.deps.Ctx != nil {
		return s.deps.Ctx
	}
	return context.Background()
}

func (s *AssetMetersScreen) load() tea.Cmd {
	s.loadSeq++
	seq := s.loadSeq
	deps, id, ctx := s.deps, s.assetID, s.ctx()
	return func() tea.Msg {
		meters, err := deps.OMS.ListAssetMeters(ctx, id)
		return assetMetersLoadedMsg{meters: meters, err: err, seq: seq}
	}
}

func (s *AssetMetersScreen) beginLoad() tea.Cmd {
	s.loading = true
	s.loadErr = ""
	s.loadConfirmed = false
	return s.load()
}

func (s *AssetMetersScreen) meterValuesConfirmed() bool {
	return s.loadConfirmed && !s.loading && s.loadErr == ""
}

// addressedMeter is the one guarded read every s.meters[s.cursor] goes through.
//
// A reload can land under an open form — every write fires load() — and the
// meter the operator was standing on can be gone by then (another terminal
// deleted it, or the list came back empty). A positional index followed blindly
// would then name one meter on the confirm and write to another, which is the
// defect po_edit.go's reseatLineIndexes exists to stop.
func (s *AssetMetersScreen) addressedMeter() (omsapi.AssetMeter, bool) {
	if s.cursor < 0 || s.cursor >= len(s.meters) {
		return omsapi.AssetMeter{}, false
	}
	return s.meters[s.cursor], true
}

func (s *AssetMetersScreen) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		s.setSize(m)
		return s, nil

	case assetMetersLoadedMsg:
		if m.seq != s.loadSeq {
			return s, nil
		}
		s.loading = false
		if m.err != nil {
			s.loadConfirmed = false
			s.loadErr = m.err.Error()
			return s, Status("load meters failed: "+m.err.Error(), StatusError)
		}
		s.loadErr = ""
		s.loadConfirmed = true
		prev, had := s.addressedMeter()
		s.meters = m.meters
		s.reseatCursor(prev, had)
		return s, nil

	case meterReadingWrittenMsg:
		return s.applyWrite(m)

	case meterCreatedMsg:
		s.saving = false
		if m.err != nil {
			s.errMsg = meterRefusal(m.err)
			return s, Status("create meter failed: "+s.errMsg, StatusError)
		}
		s.phase = meterPhaseList
		s.errMsg = ""
		s.blurAll()
		name := ""
		if m.meter != nil {
			name = m.meter.Name
		}
		s.setNote(StatusOK, fmt.Sprintf("meter %q created.", name))
		return s, tea.Batch(Status("meter created", StatusOK), s.beginLoad())

	case tea.KeyMsg:
		switch s.phase {
		case meterPhaseRecord:
			return s.updateRecord(m)
		case meterPhaseAdjust:
			return s.updateAdjust(m)
		case meterPhaseNew:
			return s.updateNew(m)
		case meterPhaseConfirm:
			return s.updateConfirm(m)
		default:
			return s.updateList(m)
		}
	}

	// Anything else (a cursor blink) goes to whichever box holds the caret.
	if box := s.focusedBox(); box != nil {
		var cmd tea.Cmd
		*box, cmd = box.Update(msg)
		return s, cmd
	}
	return s, nil
}

// reseatCursor follows the operator's place to its own METER across a reload,
// by identity rather than by position. A meter that is gone leaves the cursor
// clamped and says so, rather than silently addressing whatever now sits at that
// index.
func (s *AssetMetersScreen) reseatCursor(prev omsapi.AssetMeter, had bool) {
	if had && prev.ID != "" {
		for i, m := range s.meters {
			if m.ID == prev.ID {
				s.cursor = i
				return
			}
		}
		if s.phase != meterPhaseList {
			// The form or confirm standing open is about a meter that is no
			// longer there. Close back to the grid and say why; writing to it
			// would be a write to something the operator cannot see.
			s.phase = meterPhaseList
			s.blurAll()
			s.setNote(StatusWarn, meterGoneNote)
		}
	}
	if s.cursor >= len(s.meters) {
		s.cursor = len(s.meters) - 1
	}
	if s.cursor < 0 {
		s.cursor = 0
	}
}

const meterGoneNote = "that meter is no longer on this asset — nothing was written."

// applyWrite handles the reply to a record-reading or an adjustment.
//
// The success note REPORTS WHAT THE SERVER STORED rather than what was typed:
// the reply carries the meter as it now stands, and on a screen whose whole
// point is a number, the number worth showing is the one on the record.
func (s *AssetMetersScreen) applyWrite(m meterReadingWrittenMsg) (Screen, tea.Cmd) {
	s.saving = false
	if m.err != nil {
		s.errMsg = meterRefusal(m.err)
		s.phase = s.confirmFrom
		if s.phase == meterPhaseList {
			s.phase = meterPhaseRecord
		}
		s.focusFirst()
		verb := "record reading"
		if m.adjust {
			verb = "adjust meter"
		}
		return s, Status(verb+" failed: "+s.errMsg, StatusError)
	}

	s.errMsg = ""
	s.phase = meterPhaseList
	s.blurAll()
	s.valueInput.SetValue("")
	s.targetInput.SetValue("")
	s.reasonInput.SetValue("")

	verb, what := "Reading recorded", "recorded"
	if m.adjust {
		verb, what = "Meter adjusted", "adjusted"
	}
	if m.res != nil {
		fig := meterFigure(m.res.Meter.CurrentValue, m.res.Meter.Unit, m.res.Meter.MeterTypeDisplay)
		s.setNote(StatusOK, fmt.Sprintf("%s — %s now reads %s.", verb, m.res.Meter.Name, fig))
	} else {
		s.setNote(StatusOK, verb+".")
	}
	return s, tea.Batch(Status("meter "+what, StatusOK), s.beginLoad())
}

// meterRefusal recovers the server's own sentence from a refusal.
//
// Both write actions write `{"detail": …}` BY HAND in the view body, so they
// never reach DRF's exception handler and parseError hands the whole raw JSON
// over — `{"detail": "reason is required for an adjustment"}` on the status row
// rather than the sentence inside it. AsDetailRefusal is the narrow recogniser
// for exactly that shape; anything else keeps the form it arrived in.
func meterRefusal(err error) string {
	if err == nil {
		return ""
	}
	if prose, ok := omsapi.AsDetailRefusal(err); ok {
		return prose
	}
	return err.Error()
}

func (s *AssetMetersScreen) setNote(level StatusLevel, msg string) {
	s.note, s.noteLevel = msg, level
}

func (s *AssetMetersScreen) blurAll() {
	for _, b := range []*textinput.Model{
		&s.valueInput, &s.targetInput, &s.reasonInput, &s.nameInput, &s.unitInput,
	} {
		b.Blur()
	}
}

// focusedBox is the box holding the caret on the phase being drawn, or nil where
// the phase has none. It is the ONE place that mapping is made, so a message
// that is not a key (the blink) reaches the same box a keystroke would.
func (s *AssetMetersScreen) focusedBox() *textinput.Model {
	switch s.phase {
	case meterPhaseRecord:
		if s.recordFocus == meterRecordValue {
			return &s.valueInput
		}
	case meterPhaseAdjust:
		if s.adjustFocus == meterAdjustTarget {
			return &s.targetInput
		}
		return &s.reasonInput
	case meterPhaseNew:
		switch s.newFocus {
		case meterNewName:
			return &s.nameInput
		case meterNewUnit:
			return &s.unitInput
		}
	}
	return nil
}

func (s *AssetMetersScreen) focusFirst() {
	s.blurAll()
	switch s.phase {
	case meterPhaseRecord:
		s.recordFocus = meterRecordValue
		s.valueInput.Focus()
	case meterPhaseAdjust:
		s.adjustFocus = meterAdjustTarget
		s.targetInput.Focus()
	case meterPhaseNew:
		s.newFocus = meterNewName
		s.nameInput.Focus()
	}
}

// ---------------------------------------------------------------------------
// The grid
// ---------------------------------------------------------------------------

// listNames reports whether the grid's bar currently names this key.
//
// The handler ASKS THE BAR rather than re-deriving the condition beside it,
// which is what makes "a key the bar does not name does nothing" true by
// construction rather than by two copies of an expression staying in step.
func (s *AssetMetersScreen) listNames(key string) bool {
	for _, it := range s.listBar() {
		if it.Key == key {
			return true
		}
	}
	return false
}

func (s *AssetMetersScreen) updateList(m tea.KeyMsg) (Screen, tea.Cmd) {
	switch m.String() {
	case "esc":
		return s, SwitchTo(WSAssets, NewAssetDetailScreen(s.deps, s.assetID))
	case "up":
		s.moveList(-1)
	case "down":
		s.moveList(+1)
	case "pgup":
		if s.listNames("PgUp/PgDn") {
			s.pageList(-1)
		}
	case "pgdown":
		if s.listNames("PgUp/PgDn") {
			s.pageList(+1)
		}
	case "r":
		s.setNote(StatusInfo, "")
		return s, s.beginLoad()
	case "n":
		return s, s.openNew()
	case "enter":
		if !s.listNames("Enter") {
			return s, nil
		}
		return s, s.openEntry(meterPhaseRecord)
	case "ctrl+a":
		if !s.listNames("Ctrl-A") {
			return s, nil
		}
		return s, s.openEntry(meterPhaseAdjust)
	case "ctrl+e":
		if !s.listNames("Ctrl-E") {
			return s, nil
		}
		meter, ok := s.addressedMeter()
		if !ok {
			return s, nil
		}
		return s, SwitchTo(WSAssets, NewAssetMeterReadingsScreen(s.deps, s.assetID, s.assetName, meter))
	}
	return s, nil
}

// moveList / pageList walk the grid's cursor. It is a LIST cursor, so it clamps
// rather than wrapping, and both DECLINE on a pane the frame is not drawn into —
// the highlight they would move is not on screen to be seen, and growing the
// terminal back would find the cursor on a different meter.
func (s *AssetMetersScreen) moveList(delta int) {
	next, ok := s.pickRow(s.cursor, len(s.meters), delta, len(s.listHeader()), s.listBar())
	if !ok {
		return
	}
	s.cursor = next
}

func (s *AssetMetersScreen) pageList(dir int) {
	next, ok := s.pageRow(s.listLines(), s.cursor, len(s.meters), dir,
		len(s.listHeader()), s.listBar(), s.listBarItems(true))
	if !ok {
		return
	}
	s.cursor = next
}

// openEntry opens the record or adjust form on the highlighted meter.
//
// Both go through one function because the GUARD is the same — there must be a
// meter under the cursor — and because the phase is the only thing that differs.
// What must NOT be shared is anything downstream of it: the two forms ask for
// different things and post to different endpoints.
func (s *AssetMetersScreen) openEntry(phase meterPhase) tea.Cmd {
	if _, ok := s.addressedMeter(); !ok {
		return nil
	}
	s.phase = phase
	s.errMsg = ""
	s.setNote(StatusInfo, "")
	s.focusFirst()
	return textinput.Blink
}

func (s *AssetMetersScreen) openNew() tea.Cmd {
	s.phase = meterPhaseNew
	s.errMsg = ""
	s.setNote(StatusInfo, "")
	s.newTypeIdx = 0
	s.nameInput.SetValue("")
	s.newUnitEdit = false
	s.unitInput.SetValue(meterTypeOptions[0].Unit)
	s.focusFirst()
	return textinput.Blink
}

// Grid column widths. The number column and the VALUE column are facts; the
// meter's NAME is the identifier that abbreviates around them.
const (
	meterNumW = 3
	// The ceiling on the measured index column (meterNumWidth). An asset carries
	// a handful of meters rather than a thousand, so this ceiling is never
	// reached in practice — the column is measured anyway, because the
	// alternative is a bound that rests on a plausibility argument somebody has
	// to keep being right about, and padCell pads without truncating: an index
	// wider than its column widens the whole ROW and clampToBox takes the figure
	// off the end of it.
	meterNumMaxW = 6
	// meterNameFloor is the narrowest the NAME column may be before the value
	// column is dropped instead of squeezing it further.
	//
	// There is deliberately no matching floor for the VALUE column: it never
	// narrows at all. A value column narrowed to a floor would cut a figure,
	// which is the one thing this grid may not do — so the column is either wide
	// enough for the widest reading or it is dropped whole (meterGridPlan).
	meterNameFloor = 10
)

// meterNumWidth reserves the index column at the widest index this asset's meters
// will really draw, and meterRowIndent hangs a continuation line under the name
// column beside it — both off the MEASURED width, so the readings under a row stay
// lined up with the column they belong to.
func (s *AssetMetersScreen) meterNumWidth() int {
	return jdeGridFactW(meterNumW, meterNumMaxW, strconv.Itoa(len(s.meters)))
}

func (s *AssetMetersScreen) meterRowIndent() string {
	return strings.Repeat(" ", len(jdeIndent)+s.meterNumWidth()+2)
}

// meterGridPlan sizes the grid's columns against the pane it is about to be
// drawn into, and says what had to give.
//
// THE VALUE NEVER GIVES AND THE NAME ABBREVIATES AROUND IT — the report table's
// identifier/fact rule, applied here because the fact is the entire subject of
// the screen. Where the pane cannot hold the widest figure AND a readable name,
// the value column is dropped WHOLE and `dropped` says so, so the header can
// mark it and the readings line underneath can carry the figure instead. A
// figure clipped to fit would be a different number drawn as though it were
// this one.
func (s *AssetMetersScreen) meterGridPlan() (nameW, valueW int, dropped bool) {
	width := 76
	if w := assetPaneCells(s.terminalWidth); w > 0 {
		width = w
	}
	for _, m := range s.meters {
		if n := lipgloss.Width(meterFigure(m.CurrentValue, m.Unit, m.MeterTypeDisplay)); n > valueW {
			valueW = n
		}
	}
	avail := width - (len(jdeIndent) + s.meterNumWidth() + 2)
	// nameOnly is the name column when the value has no column of its own. It is
	// capped at what the pane really leaves rather than floored above it: padCell
	// pads and never truncates, so a floor wider than `avail` would push the row
	// past the pane, which is the overrun this whole plan exists to avoid.
	nameOnly := avail
	if nameOnly < meterNameFloor && avail > meterNameFloor {
		nameOnly = meterNameFloor
	}
	if valueW == 0 {
		return nameOnly, 0, false
	}
	if avail-(valueW+2) < meterNameFloor {
		// The figure and a readable name will not both fit. The name stays on
		// the row — it is what the cursor is choosing between — and the figure
		// moves to a line of its own, marked.
		return nameOnly, 0, true
	}
	return avail - (valueW + 2), valueW, false
}

// meterGridRow lays one meter out in its columns. The figure is right-aligned,
// as every fact column in this program is, and both facts go through
// jdeGridFactCell so neither can widen the row.
func meterGridRow(num, name, figure string, numW, nameW, valueW int) string {
	row := jdeIndent + jdeGridFactCell(num, numW, alignRight) + "  " +
		padCell(name, nameW, alignLeft)
	if valueW > 0 {
		row += "  " + jdeGridFactCell(figure, valueW, alignRight)
	}
	return strings.TrimRight(row, " ")
}

// listLines builds the grid, tagging each line with the meter it belongs to so
// the window keeps a whole entry — the row plus its readings — on the pane
// rather than the first line of it.
func (s *AssetMetersScreen) listLines() *jdeLines {
	l := &jdeLines{}
	nameW, valueW, dropped := s.meterGridPlan()
	for i, m := range s.meters {
		figure := meterFigure(m.CurrentValue, m.Unit, m.MeterTypeDisplay)
		row := meterGridRow(strconv.Itoa(i+1), fitCell(m.Name, nameW), figure,
			s.meterNumWidth(), nameW, valueW)
		if i == s.cursor {
			row = StyleJDEFieldFocused.Render(row)
		}
		l.AddRow(i, row)

		// The readings under the row carry what the grid has no column for —
		// and, where the pane was too narrow for the value column, the FIGURE
		// itself, which is the one thing that may not simply disappear.
		if dropped && figure != "" {
			// Its OWN line, at the row indent, and never a jdeWrapTokens token:
			// that helper CLIPS a token too wide for the line, and a clipped
			// token here is a cut number.
			l.AddRow(i, jdeIndent+StyleJDEHeading.Render(meterFigureLine(
				figure, meterValueText(m.CurrentValue),
				assetPaneCells(s.terminalWidth)-len(jdeIndent))))
		}
		var tokens []jdeToken
		if m.CurrentIsEstimated {
			tokens = append(tokens, jdeToken{text: "estimated", style: StyleStatusWarn})
		}
		if src := m.SourceDisplay; src != "" {
			tokens = append(tokens, jdeToken{text: src, style: StyleMuted})
		}
		if !m.IsActive {
			tokens = append(tokens, jdeToken{text: "inactive", style: StyleStatusWarn})
		}
		for _, line := range jdeWrapTokens(tokens, s.meterRowIndent(), s.bodyWidth()) {
			l.AddRow(i, line)
		}
	}
	return l
}

// listHeader is the grid's chrome, PINNED above the rows rather than leading the
// body: a body line that belongs to no navigable row is a line no key can reach,
// because jdeLines.Window anchors on the cursor's block and a columnar cursor
// cannot go above its first row.
//
// The COLUMN HEADER is essential and the count is context — an operator reading
// a grid needs to know which column is which, and the count is restated by the
// row numbers themselves. Where the value column was DROPPED the header says so
// there, beside the columns it is about, because that is a claim about the
// layout rather than an answer to a keypress.
func (s *AssetMetersScreen) listHeader() jdeHeader {
	h := jdeHeader(nil)
	if s.loadErr != "" {
		h = h.add(jdeHeadContext,
			StyleStatusError.Render("Error: ")+fitCellIf(s.loadErr, s.bodyWidth()-poErrPrefixW), "")
	}
	if len(s.meters) == 0 {
		h = h.add(jdeHeadContext, StyleJDEHeading.Render("Meters (0)"))
		if s.loadErr != "" {
			// FOUND-NOTHING AND COULD-NOT-TELL ARE DIFFERENT FACTS, and the
			// endpoint is staff-gated, so a 403 is an ordinary way to get here.
			return h.add(jdeHeadEssential, jdeIndent+StyleMuted.Render(
				fitCellIf("Could not read the meter list — there may still be meters.",
					s.bodyWidth()-len(jdeIndent))))
		}
		return h.add(jdeHeadEssential, jdeIndent+StyleMuted.Render(
			fitCellIf("No meters here. n defines one.", s.bodyWidth()-len(jdeIndent))))
	}

	nameW, valueW, dropped := s.meterGridPlan()
	h = h.add(jdeHeadContext, StyleJDEHeading.Render(fmt.Sprintf("Meters (%d)", len(s.meters))))
	if dropped {
		// WHERE THE VALUE COLUMN IS GONE, THE NOTE TAKES THE ESSENTIAL ROW and
		// the column header gives it up. jdeFitHeader trims by rank, and a
		// header may mark exactly ONE row essential — so the two compete, and
		// the note is what must survive: without the value column the header
		// reads "#  Meter", which says nothing an operator could not see, while
		// the note is the only thing on the pane saying where the reading went.
		// Ranked the other way round, at 45x18 the column was dropped and the
		// pane carried no mark at all.
		//
		// A SHORT FIXED FACT TAKES THE ROW AND THE SENTENCE FOLDS BEHIND IT AS
		// CONTEXT — chainHeader's shape, and the reason is the defect this
		// replaced: the whole sentence was marked essential, jdeCaveatLines
		// folded it onto two rows at 80x11, and jdeFitHeader keeps ONE, so the
		// row the builder promised was not on the pane. The lead is bounded
		// against the LIVE pane, since an essential row is a promise about the
		// row and jdeFitHeader does no width fitting at all.
		return h.add(jdeHeadContext, StyleMuted.Render(
			meterGridRow("#", "Meter", "Current", s.meterNumWidth(), nameW, valueW))).
			add(jdeHeadEssential, jdeIndent+StyleMuted.Render(fitCellIf(
				"Value "+meterDropMark+" below", s.bodyWidth()-len(jdeIndent)))).
			addCaveatBlock(jdeHeadContext, jdeCaveatLines(meterDropNote, s.bodyWidth()))
	}
	return h.add(jdeHeadEssential, StyleMuted.Render(
		meterGridRow("#", "Meter", "Current", s.meterNumWidth(), nameW, valueW)))
}

// meterDropNote is the sentence under the drop mark below, named rather than
// written inline so the builder and the fold-mark sweep read one string.
const meterDropNote = "The pane is too narrow to hold each meter's current reading on its row, " +
	"so it is drawn whole underneath instead."

// meterDropMark says a column was moved off the row rather than cut on it. It is
// the mark half of "shown whole or dropped and marked".
const meterDropMark = "↓"

// listBar names the keys that work on the grid — and only those. Every row
// action is dropped where the cursor addresses nothing, and the movement pair
// where there is nothing to move between.
func (s *AssetMetersScreen) listBar() []actionBarItem {
	return s.listBarItems(s.listPages())
}

func (s *AssetMetersScreen) listBarItems(paging bool) []actionBarItem {
	var items []actionBarItem
	if len(s.meters) > 0 {
		items = append(items, actionBarItem{"Enter", "Record"}, actionBarItem{"Ctrl-A", "Adjust"})
	}
	items = append(items, actionBarItem{"Esc", "Back"})
	if jdeRowMoves(len(s.meters)) {
		items = append(items, actionBarItem{"UP/DN", "Move"})
	}
	if len(s.meters) > 0 {
		items = append(items, actionBarItem{"Ctrl-E", "Ledger"})
	}
	items = append(items, actionBarItem{"n", "New meter"})
	if paging {
		items = append(items, actionBarItem{"PgUp/PgDn", "Page"})
	}
	return append(items, actionBarItem{"r", "Refresh"})
}

// listPages is the ONE condition both the bar and (through listNames) the
// handler read, settled against the bar WITH the paging entry on it — the
// tallest bar and therefore the smallest body budget, which cannot oscillate.
func (s *AssetMetersScreen) listPages() bool {
	if len(s.meters) == 0 {
		return false
	}
	return s.bodyPagesForBar(s.listLines(), len(s.meters), len(s.listHeader()), s.listBarItems(true))
}

func (s *AssetMetersScreen) viewList() string {
	return s.frameWrapped(s.listHeader(), s.listLines(), s.cursor, s.listStatus(), s.listBar())
}

// listStatus is the grid's status row: what is in flight, then what the last
// keypress DID. The ANSWER leads nothing here — the grid pins no typed box, so
// the header has a row to spare and the working line keeps the status row to
// itself (po_create.go's statusPlan rule).
func (s *AssetMetersScreen) listStatus() string {
	if s.loading {
		return s.statusRow(true, "Reading this asset's meters…", "")
	}
	if s.note != "" {
		return s.statusAnswer(s.noteLevel, s.note)
	}
	return s.statusRow(false, "", "")
}

// ---------------------------------------------------------------------------
// Record a reading — what the machine says NOW
// ---------------------------------------------------------------------------

// meterRecordBar is the record form's bar, said ONCE: the movement arm asks the
// layer whether the frame is drawn before it moves the caret, and a second
// literal beside the view's would be a bar measured that is not the bar drawn.
//
// The commit label says RECORD and never "Save", because the operator has two
// instruments on this screen and the bar is the surface that tells them which
// one they are holding.
var meterRecordBar = []actionBarItem{
	{"Enter", "Record"}, {"Esc", "Cancel"}, {"UP/DN", "Fields"}, {"←→", "Change"},
}

func (s *AssetMetersScreen) updateRecord(m tea.KeyMsg) (Screen, tea.Cmd) {
	switch m.String() {
	case "esc":
		s.phase = meterPhaseList
		s.errMsg = ""
		s.blurAll()
		return s, nil
	case "tab", "down", "shift+tab", "up":
		return s, s.moveFormRow(m.String(), &s.recordFocus, meterRecordFieldCount,
			len(s.entryHeader("Record a reading")), meterRecordBar)
	case "left", "right":
		// A choice row cycles in place; the VALUE row is typed into, and left /
		// right there are the caret's own — bubbles owns them, so they fall
		// through rather than being swallowed on behalf of a row they are not on.
		switch s.recordFocus {
		case meterRecordBasis:
			s.recordAbsolute = !s.recordAbsolute
			return s, nil
		case meterRecordEstimated:
			s.recordEstimated = !s.recordEstimated
			return s, nil
		}
	case "enter":
		if s.saving {
			return s, nil
		}
		return s, s.submitRecord()
	}
	if s.recordFocus == meterRecordValue {
		var cmd tea.Cmd
		s.valueInput, cmd = s.valueInput.Update(m)
		return s, cmd
	}
	return s, nil
}

// moveFormRow walks a field cursor. A FIELD form WRAPS — a short sheet has no
// edge worth defending, and clamped, Down on the last row would blur and
// re-focus the same field while the bar named UP/DN.
//
// headerRows is the PINNED HEADER the phase is really drawing, and it is passed
// rather than left at zero because moveRow asks the layer whether the frame is
// drawn at all and that answer is a budget question: every form here pins a
// header, so a zero would have the caret move at heights where the pane draws
// nothing but the too-short notice, and growing the terminal back would find the
// operator on a row they never walked to.
func (s *AssetMetersScreen) moveFormRow(key string, focus *int, count, headerRows int, bar []actionBarItem) tea.Cmd {
	delta := +1
	if key == "up" || key == "shift+tab" {
		delta = -1
	}
	next, ok := s.moveRow(*focus, count, delta, headerRows, bar)
	if !ok {
		return nil
	}
	s.blurAll()
	*focus = next
	if box := s.focusedBox(); box != nil {
		box.Focus()
	}
	return textinput.Blink
}

// submitRecord validates locally, then either writes or opens the confirm.
//
// The local refusals are the two the SERVER cannot usefully make: an empty box
// (the server answers "value must be a number", which is true and unhelpful
// about a field the operator has not filled in) and a box that is not a number.
// Every refusal leads with the load-bearing clause — `nothing recorded: …` —
// because fitStatus gives an error one row and lets the circumstance be what a
// narrow pane takes.
func (s *AssetMetersScreen) submitRecord() tea.Cmd {
	meter, ok := s.addressedMeter()
	if !ok {
		s.phase = meterPhaseList
		s.blurAll()
		s.setNote(StatusWarn, meterGoneNote)
		return nil
	}
	typed := strings.TrimSpace(s.valueInput.Value())
	if typed == "" {
		s.errMsg = "nothing recorded: type the reading first."
		return Status(s.errMsg, StatusError)
	}
	if _, ok := meterRat(typed); !ok {
		s.errMsg = fmt.Sprintf("nothing recorded: %q is not a number.", typed)
		return Status(s.errMsg, StatusError)
	}

	verdict := meterEntryCheck(meter.CurrentValue, typed, s.recordAbsolute)
	verdict.Unchecked = !s.meterValuesConfirmed()
	if verdict.Suspicious() {
		s.pendingAdjust = false
		s.pendingMeter = meter
		s.pendingVal = verdict
		s.confirmFrom = meterPhaseRecord
		s.confirmScroll = 0
		s.phase = meterPhaseConfirm
		s.errMsg = ""
		s.blurAll()
		return nil
	}
	return s.writeRecord(meter, typed)
}

func (s *AssetMetersScreen) writeRecord(meter omsapi.AssetMeter, typed string) tea.Cmd {
	s.saving = true
	s.errMsg = ""
	deps, ctx := s.deps, s.ctx()
	req := omsapi.RecordMeterReadingRequest{
		Value:       typed,
		IsAbsolute:  s.recordAbsolute,
		IsEstimated: s.recordEstimated,
	}
	id := meter.ID
	return func() tea.Msg {
		res, err := deps.OMS.RecordMeterReading(ctx, id, req)
		return meterReadingWrittenMsg{res: res, err: err}
	}
}

// meterBasisLabel names the two things a typed number can mean, in the words a
// machine's display would use rather than in the API's.
//
// It is a CHOICE row and not a checkbox because the two readings are equally
// ordinary — a total off a panel, a delta off a shift log — and a checkbox makes
// one of them the exception.
//
// It carries NO unit, deliberately: the Reading row one line above it already
// says "in hours", and repeating that here would spend cells of a 51-column pane
// restating the row above. The wording says what the BOX MEANS, which is the one
// thing the unit does not answer.
func meterBasisLabel(absolute bool) string {
	if absolute {
		return "the counter now reads this"
	}
	return "add this to the counter"
}

func (s *AssetMetersScreen) recordFields() []jdeField {
	meter, _ := s.addressedMeter()
	unit := meterUnitLabel(meter.Unit, meter.MeterTypeDisplay)
	hint := "the number on the machine"
	if unit != "" {
		hint = "in " + unit
	}
	return []jdeField{
		{
			Label: "Reading", Kind: jdeText, Input: &s.valueInput, Width: 18,
			Hint: hint, Focused: s.recordFocus == meterRecordValue,
		},
		{
			Label: "Means", Kind: jdeChoice,
			Value:   meterBasisLabel(s.recordAbsolute),
			Focused: s.recordFocus == meterRecordBasis,
		},
		{
			Label: "Estimated", Kind: jdeChoice, Value: jdeYesNo(s.recordEstimated),
			Hint: "eyeballed, not measured", Focused: s.recordFocus == meterRecordEstimated,
		},
	}
}

// entryHeader pins WHICH METER and WHERE IT STANDS, because a reading typed
// against the wrong meter is the mistake this frame exists to prevent and the
// current value is what the operator checks their entry against.
//
// The meter's identity is ESSENTIAL — jdeFitHeader keeps the essential row
// wherever the frame is drawn at all — and its current reading is context, so a
// pane too short for both still says what is being written to.
func (s *AssetMetersScreen) entryHeader(title string) jdeHeader {
	meter, ok := s.addressedMeter()
	h := jdeHeader(nil).add(jdeHeadDecorative, StyleJDEHeading.Render(title), "")
	if !ok {
		return h.add(jdeHeadEssential, jdeIndent+StyleStatusWarn.Render(meterGoneNote))
	}
	h = h.add(jdeHeadEssential, renderJDEField(jdeField{
		Label: "Meter", Kind: jdeValue,
		Value:   fitCellIf(meter.Name, jdeStripWidth(s.bodyWidth(), assetLabelW)),
		Focused: true,
	}, assetLabelW, s.bodyWidth()))
	now := meterFigure(meter.CurrentValue, meter.Unit, meter.MeterTypeDisplay)
	if now == "" {
		now = "(no reading recorded)"
	}
	if meter.CurrentIsEstimated {
		now += " · estimated"
	}
	h = h.add(jdeHeadContext, renderJDEField(jdeField{
		Label: "Now reads", Kind: jdeValue, Value: fitCellIf(now, jdeStripWidth(s.bodyWidth(), assetLabelW)),
	}, assetLabelW, s.bodyWidth()))
	return h.add(jdeHeadDecorative, "")
}

func (s *AssetMetersScreen) viewRecord() string {
	fields := s.recordFields()
	body := &jdeLines{}
	body.AddFittedFields(fields, jdeLabelWidth(fields), s.bodyWidth(), 0)
	return s.frameWrapped(s.entryHeader("Record a reading"), body, s.recordFocus,
		s.statusRow(s.saving, "Recording the reading…", s.errMsg), meterRecordBar)
}

// ---------------------------------------------------------------------------
// Adjust — a CORRECTION to the history
// ---------------------------------------------------------------------------

var meterAdjustBar = []actionBarItem{
	{"Enter", "Adjust"}, {"Esc", "Cancel"}, {"UP/DN", "Fields"},
}

func (s *AssetMetersScreen) updateAdjust(m tea.KeyMsg) (Screen, tea.Cmd) {
	switch m.String() {
	case "esc":
		s.phase = meterPhaseList
		s.errMsg = ""
		s.blurAll()
		return s, nil
	case "tab", "down", "shift+tab", "up":
		return s, s.moveFormRow(m.String(), &s.adjustFocus, meterAdjustFieldCount,
			len(s.adjustHeader()), meterAdjustBar)
	case "enter":
		if s.saving {
			return s, nil
		}
		return s, s.submitAdjust()
	}
	if box := s.focusedBox(); box != nil {
		var cmd tea.Cmd
		*box, cmd = box.Update(m)
		return s, cmd
	}
	return s, nil
}

// submitAdjust refuses a blank reason HERE rather than relaying the server's.
//
// The server does refuse it — `{"detail": "reason is required for an
// adjustment"}` — and that refusal would be surfaced legibly if it arrived. It
// is caught first because the round trip buys nothing: the operator is standing
// on the frame with the empty box in front of them, so this is a refusal they
// can satisfy without leaving it, which is the test receive_form.go sets for
// when a refusal is legitimate at all.
func (s *AssetMetersScreen) submitAdjust() tea.Cmd {
	meter, ok := s.addressedMeter()
	if !ok {
		s.phase = meterPhaseList
		s.blurAll()
		s.setNote(StatusWarn, meterGoneNote)
		return nil
	}
	typed := strings.TrimSpace(s.targetInput.Value())
	reason := strings.TrimSpace(s.reasonInput.Value())
	if typed == "" {
		s.errMsg = "nothing adjusted: type the corrected total first."
		return Status(s.errMsg, StatusError)
	}
	if _, ok := meterRat(typed); !ok {
		s.errMsg = fmt.Sprintf("nothing adjusted: %q is not a number.", typed)
		return Status(s.errMsg, StatusError)
	}
	if reason == "" {
		s.errMsg = "nothing adjusted: a correction needs a reason — it goes on the record beside it."
		return Status(s.errMsg, StatusError)
	}

	verdict := meterAdjustCheck(meter.CurrentValue, typed)
	verdict.Unchecked = !s.meterValuesConfirmed()
	if verdict.Suspicious() {
		s.pendingAdjust = true
		s.pendingMeter = meter
		s.pendingVal = verdict
		s.confirmFrom = meterPhaseAdjust
		s.confirmScroll = 0
		s.phase = meterPhaseConfirm
		s.errMsg = ""
		s.blurAll()
		return nil
	}
	return s.writeAdjust(meter, typed, reason)
}

func (s *AssetMetersScreen) writeAdjust(meter omsapi.AssetMeter, typed, reason string) tea.Cmd {
	s.saving = true
	s.errMsg = ""
	deps, ctx, id := s.deps, s.ctx(), meter.ID
	req := omsapi.AdjustMeterRequest{Target: typed, Reason: reason}
	return func() tea.Msg {
		res, err := deps.OMS.AdjustMeter(ctx, id, req)
		return meterReadingWrittenMsg{res: res, adjust: true, err: err}
	}
}

func (s *AssetMetersScreen) adjustFields() []jdeField {
	meter, _ := s.addressedMeter()
	unit := meterUnitLabel(meter.Unit, meter.MeterTypeDisplay)
	hint := "what the meter SHOULD read"
	if unit != "" {
		hint = "what it should read, in " + unit
	}
	return []jdeField{
		{
			Label: "Correct to", Kind: jdeText, Input: &s.targetInput, Width: 18,
			Hint: hint, Focused: s.adjustFocus == meterAdjustTarget,
		},
		{
			Label: "Reason", Kind: jdeText, Input: &s.reasonInput, Width: 30,
			Hint:    "required — it is stored with the correction",
			Focused: s.adjustFocus == meterAdjustReason,
		},
	}
}

// adjustCaveat is what an adjustment IS, said on the frame that performs one.
//
// It is here and not on the record form because the two are being kept apart:
// the operator reaching for a correction should see that the earlier reading is
// not erased — the ledger is append-only, so the mistake and its correction both
// stay on the record, which is the whole reason the server asks for a reason.
const adjustCaveat = "A correction does not erase the earlier reading: it is written beside it, " +
	"with this reason, so the record shows both."

// adjustHeader is a method rather than lines assembled inside viewAdjust so the
// header sweep measures the SAME expression the frame draws. A header built a
// second time beside the check is a header the check cannot report on.
func (s *AssetMetersScreen) adjustHeader() jdeHeader {
	h := s.entryHeader("Adjust the meter")
	width := s.bodyWidth()
	return h.addCaveatBlock(jdeHeadContext, jdeCaveatLines(adjustCaveat, width))
}

func (s *AssetMetersScreen) viewAdjust() string {
	fields := s.adjustFields()
	body := &jdeLines{}
	body.AddFittedFields(fields, jdeLabelWidth(fields), s.bodyWidth(), 0)
	return s.frameWrapped(s.adjustHeader(), body, s.adjustFocus,
		s.statusRow(s.saving, "Posting the correction…", s.errMsg), meterAdjustBar)
}

// ---------------------------------------------------------------------------
// Define a new meter
// ---------------------------------------------------------------------------

var meterNewBar = []actionBarItem{
	{"Enter", "Create"}, {"Esc", "Cancel"}, {"UP/DN", "Fields"}, {"←→", "Change"},
}

func (s *AssetMetersScreen) updateNew(m tea.KeyMsg) (Screen, tea.Cmd) {
	switch m.String() {
	case "esc":
		s.phase = meterPhaseList
		s.errMsg = ""
		s.blurAll()
		return s, nil
	case "tab", "down", "shift+tab", "up":
		return s, s.moveFormRow(m.String(), &s.newFocus, meterNewFieldCount,
			len(s.newHeader()), meterNewBar)
	case "left", "right":
		if s.newFocus != meterNewType {
			break
		}
		delta := +1
		if m.String() == "left" {
			delta = -1
		}
		s.newTypeIdx = (s.newTypeIdx + delta + len(meterTypeOptions)) % len(meterTypeOptions)
		if !s.newUnitEdit {
			// The unit FOLLOWS the type until the operator touches it, and
			// stops following the moment they do: overwriting a unit somebody
			// typed would be discarding their input, which is never this
			// program's answer to a keypress on another row.
			s.unitInput.SetValue(meterTypeOptions[s.newTypeIdx].Unit)
		}
		return s, nil
	case "enter":
		if s.saving {
			return s, nil
		}
		return s, s.submitNew()
	}
	if box := s.focusedBox(); box != nil {
		if s.newFocus == meterNewUnit {
			s.newUnitEdit = true
		}
		var cmd tea.Cmd
		*box, cmd = box.Update(m)
		return s, cmd
	}
	return s, nil
}

func (s *AssetMetersScreen) submitNew() tea.Cmd {
	name := strings.TrimSpace(s.nameInput.Value())
	if name == "" {
		s.errMsg = "nothing created: a meter needs a name."
		return Status(s.errMsg, StatusError)
	}
	opt := meterTypeOptions[s.newTypeIdx]
	s.saving = true
	s.errMsg = ""
	deps, ctx := s.deps, s.ctx()
	req := omsapi.CreateAssetMeterRequest{
		Asset:     s.assetID,
		Name:      name,
		MeterType: opt.Value,
		Unit:      strings.TrimSpace(s.unitInput.Value()),
		// A meter created from the terminal is a MANUAL one, stated rather than
		// defaulted: the terminal cannot attach a rollup source, and a meter
		// that silently advanced from two directions would make every manual
		// reading against it a correction of the server's own arithmetic.
		Source: omsapi.MeterSourceManual,
	}
	return func() tea.Msg {
		meter, err := deps.OMS.CreateAssetMeter(ctx, req)
		return meterCreatedMsg{meter: meter, err: err}
	}
}

func (s *AssetMetersScreen) newFields() []jdeField {
	return []jdeField{
		{
			Label: "Name", Kind: jdeText, Input: &s.nameInput, Width: 30,
			Hint: "e.g. Spindle runtime", Focused: s.newFocus == meterNewName,
		},
		{
			Label: "Measures", Kind: jdeChoice, Value: meterTypeOptions[s.newTypeIdx].Label,
			Focused: s.newFocus == meterNewType,
		},
		{
			Label: "Unit", Kind: jdeText, Input: &s.unitInput, Width: 16,
			Hint: "shown beside every reading", Focused: s.newFocus == meterNewUnit,
		},
	}
}

// newMeterCaveat says what this screen can and cannot create.
//
// The terminal defines MANUAL meters only, and that is worth stating where the
// form is rather than leaving an operator to wonder why the rollup-driven meter
// they meant is not on offer: the automatic sources are configured server-side
// and a meter created here that claimed one would advance from two directions.
const newMeterCaveat = "A meter created here is entered by hand. The automatic ones — usage " +
	"sessions, telemetry — are configured on the server."

func (s *AssetMetersScreen) newHeader() jdeHeader {
	h := jdeHeader(nil).add(jdeHeadDecorative, StyleJDEHeading.Render("New meter"), "")
	h = h.add(jdeHeadEssential, renderJDEField(jdeField{
		Label: "Asset", Kind: jdeValue,
		Value: fitCellIf(s.assetName, jdeStripWidth(s.bodyWidth(), assetLabelW)),
	}, assetLabelW, s.bodyWidth()))
	width := s.bodyWidth()
	h = h.addCaveatBlock(jdeHeadContext, jdeCaveatLines(newMeterCaveat, width))
	return h.add(jdeHeadDecorative, "")
}

func (s *AssetMetersScreen) viewNew() string {
	fields := s.newFields()
	body := &jdeLines{}
	body.AddFittedFields(fields, jdeLabelWidth(fields), s.bodyWidth(), 0)
	return s.frameWrapped(s.newHeader(), body, s.newFocus,
		s.statusRow(s.saving, "Creating the meter…", s.errMsg), meterNewBar)
}

// ---------------------------------------------------------------------------
// The confirm — the server will take this, and here is what it means
// ---------------------------------------------------------------------------

// CTRL-X CONFIRMS, NOT ENTER. Enter is the key that OPENED this frame and the
// one a hand reaches for next, so binding the write to it would make a reflex
// enough to send a probable typo — the same choice the PO line-delete confirm
// and the New PO supplier switch make.
//
// Esc goes back to the FORM the entry was typed on, not to the grid: the
// operator's number is still in the box, and throwing it away to make them
// retype it would be discarding input in answer to "are you sure".
func (s *AssetMetersScreen) updateConfirm(m tea.KeyMsg) (Screen, tea.Cmd) {
	switch m.String() {
	case "ctrl+x":
		if s.saving || !s.confirmWrites() {
			return s, nil
		}
		s.setNote(StatusInfo, "")
		if s.pendingAdjust {
			return s, s.writeAdjust(s.pendingMeter,
				strings.TrimSpace(s.targetInput.Value()), strings.TrimSpace(s.reasonInput.Value()))
		}
		return s, s.writeRecord(s.pendingMeter, strings.TrimSpace(s.valueInput.Value()))
	case "esc":
		s.phase = s.confirmFrom
		s.focusFirst()
		return s, textinput.Blink
	case "up", "down", "pgup", "pgdown", "home", "end":
		if !s.frameDrawn(len(s.confirmHeader()), s.confirmBar()) {
			// Refused pane: the caveat is not drawn, so scrolling it would move
			// the operator's place invisibly. An arm whose whole product is a
			// POSITION says nothing rather than declining out loud.
			return s, nil
		}
		if !s.confirmScrolls() {
			s.setNote(StatusInfo, m.String()+" moves nothing — the whole warning is on the pane.")
			return s, nil
		}
		s.confirmScroll = jdeScrollStep(m.String(), s.confirmScroll, s.confirmBody().Len(),
			s.scrollRows(len(s.confirmHeader()), s.confirmBar()))
	default:
		// Every other key ANSWERS. This frame holds no cursor and no caret, so a
		// silent return redraws a pane that is a pure function of unchanged
		// state — byte for byte identical, which reads as a wedged program.
		s.setNote(StatusInfo, m.String()+" does nothing here — this frame only sends or goes back.")
	}
	return s, nil
}

// confirmWrites is the ONE expression behind "may Ctrl-X send". The bar reads it
// to decide whether to name the key and the arm reads it to decide whether to
// act, so the legend and the key cannot come apart.
func (s *AssetMetersScreen) confirmWrites() bool {
	return !s.saving && s.pendingMeter.ID != ""
}

// confirmHeadline is the one row that says what is about to happen, assembled
// and THEN bounded — a bound applied to one part of a row that is afterwards
// added to is not a bound (the assetScopeRows rule).
func (s *AssetMetersScreen) confirmHeadline() string {
	m := s.pendingMeter
	lead := "Record: "
	if s.pendingAdjust {
		lead = "Adjust: "
	}
	from := meterFigure(m.CurrentValue, m.Unit, m.MeterTypeDisplay)
	if s.pendingVal.Unchecked {
		from += "?"
	}
	to := meterFigure(s.pendingVal.Result, m.Unit, m.MeterTypeDisplay)
	// ASSEMBLED, THEN BOUNDED. A bound applied to one part of a row that is
	// afterwards added to is not a bound — the defect removalHeadline had on the
	// one row that says what an irreversible action is about to do.
	//
	// THE GIVE-ORDER IS THE OPPOSITE OF removalHeadline's, AND THAT IS A
	// DECISION. There, the FACTS give and the NAME keeps the room, because a
	// quantity disambiguates a name and so presupposes one. Here the two figures
	// ARE the question being asked — "did you mean to go from A to B" — and the
	// meter's name is on the grid one screen back, in the prose below, and on
	// the form Esc returns to. So the name gives, down to a floor and then
	// away entirely, and the figures are the last thing to go.
	facts := " · " + from + " → " + to
	room := assetPaneCells(s.terminalWidth) - len(jdeIndent)
	if room <= 0 {
		return lead + m.Name + facts
	}
	if left := room - visibleCells(lead+facts); left >= meterNameFloor {
		return lead + fitCell(m.Name, left) + facts
	}
	// No room for a readable name beside them. The figures stay whole and the
	// name goes; where even that will not fit, the pane is narrower than any
	// wording of this row and the clip is marked.
	if visibleCells(lead+facts) <= room {
		return lead + strings.TrimPrefix(facts, " · ")
	}
	return fitCell(lead+m.Name+facts, room)
}

// confirmHeader pins WHAT and HOW MUCH: the action, the meter, and both figures
// with their unit. The headline is ESSENTIAL, so wherever this frame is drawn at
// all it names what Ctrl-X will send.
func (s *AssetMetersScreen) confirmHeader() jdeHeader {
	title := "Check this reading"
	if s.pendingAdjust {
		title = "Check this correction"
	}
	h := jdeHeader(nil).add(jdeHeadDecorative, StyleStatusWarn.Render(title), "")
	h = h.add(jdeHeadEssential, jdeIndent+s.confirmHeadline())
	return h.add(jdeHeadDecorative, "")
}

// confirmBody is the caveat, said ONCE so the lines the bar is measured against
// are the lines the frame draws. It owns no navigable row on purpose: there is
// nothing here to type into, so the window over it is positioned by an OFFSET.
//
// WHAT IT SAYS, IN ORDER: what is odd (with both figures), then what cannot be
// taken back where that is true, then that the SERVER would accept this — the
// last because an operator who meant it needs to know the program is not going
// to stop them, and one who did not needs the first sentence.
func (s *AssetMetersScreen) confirmBody() *jdeLines {
	body := &jdeLines{}
	add := func(text string) {
		if text == "" {
			return
		}
		for _, line := range jdeCaveatLines(text, s.bodyWidth()) {
			body.Add(line)
		}
	}
	add(meterVerdictReason(s.pendingMeter, s.pendingVal))
	add(meterRuntimeCaveat(s.pendingMeter, s.pendingVal))
	if s.pendingAdjust {
		add("The server accepts it either way. Ctrl-X posts the correction with your reason.")
	} else {
		add("The server accepts it either way — nothing here refuses a number. " +
			"Ctrl-X records it; Esc goes back to your entry.")
	}
	return body
}

func (s *AssetMetersScreen) confirmScrolls() bool {
	return s.bodyScrollsForBar(s.confirmBody(), len(s.confirmHeader()),
		s.confirmBarItems(s.confirmWrites(), true))
}

func (s *AssetMetersScreen) confirmBar() []actionBarItem {
	return s.confirmBarItems(s.confirmWrites(), s.confirmScrolls())
}

func (s *AssetMetersScreen) confirmBarItems(writes, scroll bool) []actionBarItem {
	verb := "Record it"
	if s.pendingAdjust {
		verb = "Post it"
	}
	items := []actionBarItem{{"Ctrl-X", verb}, {"Esc", "Back"}}
	if !writes {
		// Esc still LEAVES while the write is out — a frame with no way off it
		// is the worse defect — so the bar keeps one key and drops the one that
		// would not act.
		items = []actionBarItem{{"Esc", "Back"}}
	}
	if scroll {
		items = append(items,
			actionBarItem{"UP/DN", "Scroll"},
			actionBarItem{"PgUp/PgDn", "Page"},
			actionBarItem{"Home/End", "Top/End"})
	}
	return items
}

func (s *AssetMetersScreen) confirmStatus() string {
	if !s.saving {
		return s.statusAnswer(s.noteLevel, s.note)
	}
	verb := "Recording the reading…"
	if s.pendingAdjust {
		verb = "Posting the correction…"
	}
	switch room := s.bodyWidth(); {
	case s.note == "":
	case room > 0:
		verb = poLeadOnto(s.note, verb, room)
	default:
		verb = s.note + poLeadJoint + verb
	}
	return s.statusRow(true, verb, "")
}

func (s *AssetMetersScreen) viewConfirm() string {
	frame, offset := s.frameScrolled(s.confirmHeader(), s.confirmBody(), s.confirmScroll,
		s.confirmStatus(), s.confirmBar())
	// Stored back so the offset this screen holds is the one that was DRAWN.
	s.confirmScroll = offset
	return frame
}

func (s *AssetMetersScreen) View() string {
	switch s.phase {
	case meterPhaseRecord:
		return s.viewRecord()
	case meterPhaseAdjust:
		return s.viewAdjust()
	case meterPhaseNew:
		return s.viewNew()
	case meterPhaseConfirm:
		return s.viewConfirm()
	}
	return s.viewList()
}
