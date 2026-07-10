package tui

import (
	"context"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/uid0/scantty/internal/omsapi"
)

// BatchScanSerialsScreen is the dedicated rapid-fire serial-intake screen for a
// serialized inventory item. It combines scan.go's raw scanner-gun capture
// (WantsRawInput swallows every key so a code containing workspace shortcuts
// doesn't navigate away mid-scan) with receive_form.go's repeat-loop: each
// scanned serial calls the idempotent scan_receive endpoint, which creates the
// unit and accessions it into stock in one hop. A re-scan of the same serial is
// a tolerated no-op (reported "already received") rather than a duplicate error.
//
// Flow: launched with item context from the inventory detail (b) → an optional
// setup step for a batch lot + expiration applied to every scan → rapid-fire
// scanning. A running list shows new-vs-duplicate with counts; ctrl+z undoes the
// last newly-created unit (a just-received unit is in_stock, from which no
// lifecycle action reaches a terminal state in one hop, so undo DELETEs the
// erroneous record — the true undo of a mis-scan).
type BatchScanSerialsScreen struct {
	deps     Deps
	itemID   string
	itemName string

	phase      batchPhase
	setupFocus int // bsfLot | bsfExp
	lotIn      textinput.Model
	expIn      textinput.Model
	setupErr   string

	serialIn textinput.Model

	// Batch attributes captured in the setup step, applied to every scan.
	lot string
	exp omsapi.DateOnly

	// entries is the scan log, most-recent-first (like scan.go's history).
	entries   []batchScanEntry
	newCount  int
	dupCount  int
	failCount int

	pending bool
	undoing bool
	result  string
	level   StatusLevel

	terminalHeight int
}

type batchPhase int

const (
	batchPhaseSetup batchPhase = iota
	batchPhaseScan
)

// Setup-step field indices.
const (
	bsfLot = iota
	bsfExp
)

// batchScanEntry is one recorded scan: the serial, whether this scan newly
// created the unit (vs. a tolerated duplicate re-scan), the unit id (for undo),
// and whether it was later undone.
type batchScanEntry struct {
	serial  string
	created bool
	id      string
	undone  bool
}

type batchScanDoneMsg struct {
	serial string
	res    *omsapi.ScanReceiveResult
	err    error
}

type batchUndoDoneMsg struct {
	idx int // index into entries of the unit being undone
	err error
}

// NewBatchScanSerialsScreen builds the batch-scan screen for one serialized
// item. itemID is the scan_receive `item`; itemName is display-only.
func NewBatchScanSerialsScreen(deps Deps, itemID, itemName string) *BatchScanSerialsScreen {
	lot := textinput.New()
	lot.Prompt = ""
	lot.Placeholder = "batch / lot (optional)"
	lot.CharLimit = 100
	lot.Focus()

	exp := textinput.New()
	exp.Prompt = ""
	exp.Placeholder = "YYYY-MM-DD (optional)"
	exp.CharLimit = 10

	serial := textinput.New()
	serial.Prompt = ""
	serial.Placeholder = "scan or type serial number"
	serial.CharLimit = 200

	return &BatchScanSerialsScreen{
		deps:     deps,
		itemID:   itemID,
		itemName: itemName,
		phase:    batchPhaseSetup,
		lotIn:    lot,
		expIn:    exp,
		serialIn: serial,
	}
}

func (s *BatchScanSerialsScreen) Title() string {
	if s.itemName != "" {
		return "Batch scan: " + s.itemName
	}
	return "Batch scan serials"
}

// WantsRawInput keeps every keypress in this screen (setup inputs + the serial
// buffer) so scanner-gun input — rapid ASCII that routinely contains characters
// matching workspace shortcuts — never navigates away mid-scan, exactly like
// the Scan screen.
func (s *BatchScanSerialsScreen) WantsRawInput() bool { return true }

func (s *BatchScanSerialsScreen) Init() tea.Cmd { return textinput.Blink }

func (s *BatchScanSerialsScreen) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		s.terminalHeight = m.Height
		return s, nil

	case batchScanDoneMsg:
		s.pending = false
		if m.err != nil {
			s.failCount++
			s.result = "scan " + m.serial + " failed: " + m.err.Error()
			s.level = StatusError
			return s, Status(s.result, StatusError)
		}
		entry := batchScanEntry{serial: m.serial}
		if m.res != nil {
			entry.created = m.res.Created
			entry.id = m.res.ID
		}
		s.entries = append([]batchScanEntry{entry}, s.entries...)
		if entry.created {
			s.newCount++
			s.result = "received " + m.serial
			s.level = StatusOK
		} else {
			s.dupCount++
			s.result = m.serial + " already received"
			s.level = StatusWarn
		}
		return s, Status(s.result, s.level)

	case batchUndoDoneMsg:
		s.undoing = false
		if m.err != nil {
			s.result = "undo failed: " + m.err.Error()
			s.level = StatusError
			return s, Status(s.result, StatusError)
		}
		serial := ""
		if m.idx >= 0 && m.idx < len(s.entries) {
			s.entries[m.idx].undone = true
			serial = s.entries[m.idx].serial
		}
		if s.newCount > 0 {
			s.newCount--
		}
		s.result = "undid " + serial
		s.level = StatusOK
		return s, Status(s.result, StatusOK)

	case tea.KeyMsg:
		if s.phase == batchPhaseSetup {
			return s.handleSetupKey(m)
		}
		return s.handleScanKey(m)
	}
	return s, nil
}

// handleSetupKey drives the optional batch lot + expiration step. enter validates
// the expiration and moves into scanning; esc leaves to the item detail.
func (s *BatchScanSerialsScreen) handleSetupKey(m tea.KeyMsg) (Screen, tea.Cmd) {
	switch m.Type {
	case tea.KeyEsc:
		return s, s.back()
	case tea.KeyTab, tea.KeyDown:
		s.setupFocusTo(bsfExp)
		return s, textinput.Blink
	case tea.KeyShiftTab, tea.KeyUp:
		s.setupFocusTo(bsfLot)
		return s, textinput.Blink
	case tea.KeyEnter:
		exp, err := parseOptionalDateOnly(s.expIn.Value())
		if err != nil {
			s.setupErr = err.Error()
			return s, nil
		}
		s.lot = strings.TrimSpace(s.lotIn.Value())
		s.exp = exp
		s.setupErr = ""
		s.phase = batchPhaseScan
		s.lotIn.Blur()
		s.expIn.Blur()
		s.serialIn.Focus()
		return s, textinput.Blink
	}
	var cmd tea.Cmd
	if s.setupFocus == bsfLot {
		s.lotIn, cmd = s.lotIn.Update(m)
	} else {
		s.expIn, cmd = s.expIn.Update(m)
	}
	return s, cmd
}

func (s *BatchScanSerialsScreen) setupFocusTo(field int) {
	s.setupFocus = field
	if field == bsfLot {
		s.lotIn.Focus()
		s.expIn.Blur()
	} else {
		s.expIn.Focus()
		s.lotIn.Blur()
	}
}

// handleScanKey drives rapid-fire scanning. enter submits the buffered serial;
// ctrl+z undoes the last newly-created unit; esc clears a partial buffer, then
// (on a second press) leaves to the item detail.
func (s *BatchScanSerialsScreen) handleScanKey(m tea.KeyMsg) (Screen, tea.Cmd) {
	switch m.Type {
	case tea.KeyEsc:
		if strings.TrimSpace(s.serialIn.Value()) != "" {
			s.serialIn.SetValue("")
			return s, nil
		}
		return s, s.back()
	case tea.KeyCtrlZ:
		return s.undoLast()
	case tea.KeyEnter:
		if s.pending {
			return s, nil
		}
		return s.submitScan()
	}
	var cmd tea.Cmd
	s.serialIn, cmd = s.serialIn.Update(m)
	return s, cmd
}

// submitScan fires scan_receive for the buffered serial. A blank buffer is
// ignored (a stray Enter). The buffer is cleared immediately so the next scan
// starts fresh; the round-trip result lands as a batchScanDoneMsg.
func (s *BatchScanSerialsScreen) submitScan() (Screen, tea.Cmd) {
	serial := strings.TrimSpace(s.serialIn.Value())
	if serial == "" {
		return s, nil
	}
	s.serialIn.SetValue("")
	s.pending = true
	s.result = ""
	req := omsapi.ScanReceiveRequest{
		Item:           s.itemID,
		SerialNumber:   serial,
		Lot:            s.lot,
		ExpirationDate: s.exp,
	}
	deps := s.deps
	ctx := deps.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return s, func() tea.Msg {
		res, err := deps.OMS.ScanReceive(ctx, req)
		return batchScanDoneMsg{serial: serial, res: res, err: err}
	}
}

// undoLast deletes the most-recent newly-created unit (skipping duplicates and
// already-undone entries). A just-received unit is in_stock, from which no
// lifecycle action reaches a terminal state in one hop, so deleting the record
// is the true undo of a mis-scan.
func (s *BatchScanSerialsScreen) undoLast() (Screen, tea.Cmd) {
	if s.pending || s.undoing {
		return s, nil
	}
	idx := -1
	for i := range s.entries {
		if s.entries[i].created && !s.entries[i].undone && s.entries[i].id != "" {
			idx = i
			break
		}
	}
	if idx < 0 {
		s.result = "nothing to undo"
		s.level = StatusWarn
		return s, Status(s.result, StatusWarn)
	}
	s.undoing = true
	s.result = ""
	id := s.entries[idx].id
	deps := s.deps
	ctx := deps.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return s, func() tea.Msg {
		err := deps.OMS.DeleteSerializedComponent(ctx, id)
		return batchUndoDoneMsg{idx: idx, err: err}
	}
}

// back returns to the owning item's detail screen.
func (s *BatchScanSerialsScreen) back() tea.Cmd {
	return SwitchTo(WSInventory, NewInventoryDetailScreen(s.deps, s.itemID))
}

func (s *BatchScanSerialsScreen) View() string {
	if s.phase == batchPhaseSetup {
		return s.viewSetup()
	}
	return s.viewScan()
}

func (s *BatchScanSerialsScreen) viewSetup() string {
	var b strings.Builder
	b.WriteString(StyleTitle.Render("Batch scan — "+s.displayItem()) + "\n")
	b.WriteString(StyleMuted.Render("Optionally set a lot + expiration applied to every unit, then scan.") + "\n\n")

	b.WriteString(s.renderSetupField(bsfLot, "Lot") + "\n")
	b.WriteString(s.renderSetupField(bsfExp, "Expiration") + "\n\n")

	if s.setupErr != "" {
		b.WriteString(StyleStatusError.Render(s.setupErr) + "\n\n")
	}
	b.WriteString(StyleMuted.Render("tab/↑↓ move · enter start scanning · esc back"))
	return b.String()
}

func (s *BatchScanSerialsScreen) renderSetupField(idx int, label string) string {
	caret := "  "
	if idx == s.setupFocus {
		caret = "▸ "
	}
	in := s.lotIn
	if idx == bsfExp {
		in = s.expIn
	}
	return caret + StyleTitle.Render(label+": ") + in.View()
}

func (s *BatchScanSerialsScreen) viewScan() string {
	var b strings.Builder

	prompt := StyleTitle.Render("▶ ") + s.serialIn.Value()
	cursor := "_"
	if s.pending {
		cursor = StyleMuted.Render("…")
	}
	b.WriteString(prompt + cursor + "\n")

	scope := s.displayItem()
	if s.lot != "" {
		scope += " · lot " + s.lot
	}
	if !s.exp.IsZero() {
		scope += " · exp " + s.exp.String()
	}
	b.WriteString(StyleMuted.Render(scope) + "\n\n")

	summary := fmt.Sprintf("%s new · %s dup", StyleStatusOK.Render(fmt.Sprint(s.newCount)), fmt.Sprint(s.dupCount))
	if s.failCount > 0 {
		summary += " · " + StyleStatusError.Render(fmt.Sprintf("%d failed", s.failCount))
	}
	b.WriteString(summary + "\n")
	if s.undoing {
		b.WriteString(StyleMuted.Render("Undoing…") + "\n")
	} else if s.result != "" {
		b.WriteString(RenderStatus(s.result, s.level) + "\n")
	}
	b.WriteString("\n")

	if len(s.entries) == 0 {
		b.WriteString(StyleMuted.Render("Scan a serial and press Enter. Each scan receives one unit into stock.") + "\n")
	} else {
		b.WriteString(StyleTitle.Render("Scanned") + "\n")
		shown := s.entries
		if len(shown) > 8 {
			shown = shown[:8]
		}
		for _, e := range shown {
			b.WriteString(s.renderEntry(e) + "\n")
		}
		if len(s.entries) > len(shown) {
			b.WriteString(StyleMuted.Render(fmt.Sprintf("  … %d more", len(s.entries)-len(shown))) + "\n")
		}
	}

	b.WriteString("\n" + StyleMuted.Render("enter scan · ctrl+z undo last · esc back"))
	return b.String()
}

func (s *BatchScanSerialsScreen) renderEntry(e batchScanEntry) string {
	switch {
	case e.undone:
		return StyleMuted.Render("↩ "+e.serial) + " " + StyleMuted.Render("(undone)")
	case e.created:
		return StyleStatusOK.Render("✓ "+e.serial) + " " + StyleMuted.Render("received")
	default:
		return StyleStatusWarn.Render("· "+e.serial) + " " + StyleMuted.Render("already received")
	}
}

func (s *BatchScanSerialsScreen) displayItem() string {
	if s.itemName != "" {
		return s.itemName
	}
	return "item " + shortID(s.itemID)
}
