package tui

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/uid0/scantty/internal/forgekeyapi"
)

// fwMode selects which overlay the firmware screen is showing. fwModeView is
// the read-only body (versions + rollouts + recent updates); the other modes
// are the New-Rollout form and its firmware-version picker sub-phase, both of
// which capture every key via WantsRawInput so the global hotkey layer can't
// steal characters from the textinputs or the picker cursor.
type fwMode int

const (
	fwModeView fwMode = iota
	fwModeCreate
	fwModePickVersion
)

type FirmwareScreen struct {
	deps     Deps
	versions []forgekeyapi.FirmwareVersion
	updates  []forgekeyapi.FirmwareUpdate
	rollouts []forgekeyapi.FirmwareRollout
	loading  bool
	loadErr  string

	mode          fwMode
	cursor        int // selected rollout (view mode)
	actionPending bool

	// New-Rollout form (mirrors the web New-Rollout form: firmware version,
	// batch %, interval, optional name).
	cvVersion    *forgekeyapi.FirmwareVersion
	cvBatchIn    textinput.Model
	cvIntervalIn textinput.Model
	cvNameIn     textinput.Model
	cvFocus      int // 0 = version, 1 = batch, 2 = interval, 3 = name
	cvErr        string
	cvPending    bool

	// Firmware-version picker sub-phase. cvOptions is the active + non-ePaper
	// version list offered for an MQTT rollout (ePaper versions take the
	// parallel HTTPS-pull pipeline — a separate viewset out of this screen's
	// scope — so offering them here would create a wrong-fleet rollout).
	pickCursor int
	cvOptions  []forgekeyapi.FirmwareVersion

	terminalWidth  int
	terminalHeight int
	windowStart    int // first rollout row the view window draws
	pickStart      int // first version row the picker window draws
}

type firmwareLoadedMsg struct {
	versions []forgekeyapi.FirmwareVersion
	updates  []forgekeyapi.FirmwareUpdate
	rollouts []forgekeyapi.FirmwareRollout
	err      error
}

type fwRolloutCreatedMsg struct {
	rollout *forgekeyapi.FirmwareRollout
	err     error
}

func NewFirmwareScreen(deps Deps) *FirmwareScreen {
	return &FirmwareScreen{deps: deps, loading: true}
}

func (s *FirmwareScreen) Title() string { return "Firmware" }

func (s *FirmwareScreen) Init() tea.Cmd { return s.load() }

// WantsRawInput routes every key to the screen while the create form or the
// version picker is open, so the textinputs and picker cursor receive keys the
// global dispatcher (m/a/s/…) would otherwise claim.
//
// Not while a load is in flight or has failed: that frame is drawn in place of
// every mode and answers keys by its own bar, whose `esc back` is Root's.
func (s *FirmwareScreen) WantsRawInput() bool {
	return s.mode != fwModeView && !s.loading && s.loadErr == ""
}

// HandlesKey claims the two rollout action keys that collide with the global
// hotkey layer — 's' (nav → Settings) and 'a' (global → Authorizations) — so
// they act on the selected rollout instead. Only claimed while a rollout list
// is actually present in view mode; with no rollouts the keys fall through to
// their global meaning (sc-k7p LocalKeyScreen pattern). The other action keys
// (c/p/x/j/k/r) aren't globals, so they reach the screen without a claim.
func (s *FirmwareScreen) HandlesKey(key string) bool {
	if s.mode != fwModeView || len(s.rollouts) == 0 {
		return false
	}
	return key == "s" || key == "a"
}

func (s *FirmwareScreen) load() tea.Cmd {
	deps := s.deps
	ctx := deps.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return func() tea.Msg {
		versions, err := deps.ForgeKey.ListFirmwareVersions(ctx, nil)
		if err != nil {
			return firmwareLoadedMsg{err: err}
		}
		updates, _ := deps.ForgeKey.ListFirmwareUpdates(ctx, nil)
		rollouts, _ := deps.ForgeKey.ListFirmwareRollouts(ctx, nil)
		return firmwareLoadedMsg{versions: versions, updates: updates, rollouts: rollouts}
	}
}

func (s *FirmwareScreen) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		s.terminalWidth, s.terminalHeight = m.Width, m.Height
		return s, nil
	case firmwareLoadedMsg:
		s.loading = false
		if m.err != nil {
			s.loadErr = m.err.Error()
		}
		s.versions = m.versions
		s.updates = m.updates
		s.rollouts = m.rollouts
		if s.cursor >= len(s.rollouts) {
			s.cursor = maxInt(0, len(s.rollouts)-1)
		}
		return s, nil
	case fwRolloutCreatedMsg:
		s.cvPending = false
		if m.err != nil {
			s.cvErr = m.err.Error()
			return s, Status("create rollout failed: "+m.err.Error(), StatusError)
		}
		s.mode = fwModeView
		s.cursor = 0 // the new draft lands at the top of the -created_at ordering
		return s, tea.Batch(Status("rollout created", StatusOK), s.load())
	case fkActionResultMsg:
		s.actionPending = false
		if m.err != nil {
			return s, Status(fmt.Sprintf("%s failed: %s", m.action, m.err.Error()), StatusError)
		}
		return s, tea.Batch(Status(m.action, StatusOK), s.load())
	case tea.KeyMsg:
		if s.loading || s.loadErr != "" {
			// The load frame is drawn in place of EVERY mode, so it answers by
			// the view's handler and its own bar whatever mode lies under it.
			if proseLoadKeyHidden(s.loading, s.loadErr, s.loadBar(), m.String()) {
				return s, nil
			}
			return s.handleViewKey(m)
		}
		switch s.mode {
		case fwModeCreate:
			return s.handleCreateKey(m)
		case fwModePickVersion:
			return s.handlePickKey(m)
		}
		return s.handleViewKey(m)
	}
	return s, nil
}

func (s *FirmwareScreen) handleViewKey(m tea.KeyMsg) (Screen, tea.Cmd) {
	switch m.String() {
	case "r":
		s.loading = true
		s.loadErr = ""
		return s, s.load()
	case "c":
		return s.openCreateForm()
	case "j", "down":
		if s.cursor < len(s.rollouts)-1 {
			s.cursor++
		}
		return s, nil
	case "k", "up":
		if s.cursor > 0 {
			s.cursor--
		}
		return s, nil
	case "s":
		// Start a draft, or resume a paused rollout — the backend `start`
		// action accepts both, matching the web Start/Resume buttons.
		return s.rolloutAction("start", func(st string) bool {
			return st == "draft" || st == "paused"
		}, "only draft/paused rollouts can be started")
	case "p":
		return s.rolloutAction("pause", func(st string) bool { return st == "active" }, "only active rollouts can be paused")
	case "a":
		return s.rolloutAction("advance", func(st string) bool { return st == "active" }, "only active rollouts can be advanced")
	case "x":
		return s.rolloutAction("cancel", func(st string) bool {
			return st == "active" || st == "paused"
		}, "only active/paused rollouts can be cancelled")
	}
	return s, nil
}

// rolloutAction fires a lifecycle transition on the selected rollout, guarded
// by the same status precondition the web applies (so an inapplicable key just
// flashes a hint rather than provoking a 400). On success the whole list is
// reloaded so every card's status + progress refreshes together.
func (s *FirmwareScreen) rolloutAction(action string, allowed func(status string) bool, wrongState string) (Screen, tea.Cmd) {
	if s.actionPending {
		return s, nil
	}
	if s.cursor >= len(s.rollouts) {
		return s, nil
	}
	r := s.rollouts[s.cursor]
	if !allowed(r.Status) {
		return s, Status(wrongState, StatusWarn)
	}
	id := fmt.Sprint(r.ID)
	label := r.FirmwareVersionStr
	if label == "" {
		label = "rollout"
	}
	fk := s.deps.ForgeKey
	ctx := s.deps.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	s.actionPending = true
	verb := action
	return s, func() tea.Msg {
		var err error
		switch action {
		case "start":
			_, err = fk.StartFirmwareRollout(ctx, id)
		case "pause":
			_, err = fk.PauseFirmwareRollout(ctx, id)
		case "advance":
			_, err = fk.AdvanceFirmwareRollout(ctx, id)
		case "cancel":
			_, err = fk.CancelFirmwareRollout(ctx, id)
		}
		return fkActionResultMsg{action: fmt.Sprintf("%s %s", verb, label), err: err}
	}
}

// --- New-Rollout form ------------------------------------------------------

func (s *FirmwareScreen) openCreateForm() (Screen, tea.Cmd) {
	// Offer only active, non-ePaper versions — see cvOptions comment.
	s.cvOptions = s.cvOptions[:0]
	for _, v := range s.versions {
		if v.IsActive && v.DeviceTypeCode != "epaper_screen" {
			s.cvOptions = append(s.cvOptions, v)
		}
	}
	s.cvVersion = nil
	batch := textinput.New()
	batch.Prompt = ""
	batch.CharLimit = 3
	batch.SetValue("20")
	interval := textinput.New()
	interval.Prompt = ""
	interval.CharLimit = 6
	interval.SetValue("60")
	name := textinput.New()
	name.Prompt = ""
	name.Placeholder = "optional label"
	name.CharLimit = 200
	s.cvBatchIn = batch
	s.cvIntervalIn = interval
	s.cvNameIn = name
	s.cvFocus = 0
	s.cvErr = ""
	s.cvPending = false
	s.mode = fwModeCreate
	return s, nil
}

func (s *FirmwareScreen) handleCreateKey(m tea.KeyMsg) (Screen, tea.Cmd) {
	switch m.Type {
	case tea.KeyEsc:
		s.mode = fwModeView
		return s, nil
	case tea.KeyTab, tea.KeyDown:
		s.setCreateFocus((s.cvFocus + 1) % 4)
		return s, nil
	case tea.KeyShiftTab, tea.KeyUp:
		s.setCreateFocus((s.cvFocus + 3) % 4)
		return s, nil
	case tea.KeyEnter:
		if s.cvFocus == 0 {
			// On the version field, enter opens the picker (a version must be
			// chosen from the list, not typed).
			return s.openPicker()
		}
		if s.cvPending {
			return s, nil
		}
		return s.submitCreate()
	}
	// Space on the version field also opens the picker.
	if s.cvFocus == 0 {
		if m.String() == " " {
			return s.openPicker()
		}
		return s, nil
	}
	var cmd tea.Cmd
	switch s.cvFocus {
	case 1:
		s.cvBatchIn, cmd = s.cvBatchIn.Update(m)
	case 2:
		s.cvIntervalIn, cmd = s.cvIntervalIn.Update(m)
	case 3:
		s.cvNameIn, cmd = s.cvNameIn.Update(m)
	}
	return s, cmd
}

func (s *FirmwareScreen) setCreateFocus(f int) {
	s.cvFocus = f
	s.cvBatchIn.Blur()
	s.cvIntervalIn.Blur()
	s.cvNameIn.Blur()
	switch f {
	case 1:
		s.cvBatchIn.Focus()
	case 2:
		s.cvIntervalIn.Focus()
	case 3:
		s.cvNameIn.Focus()
	}
}

func (s *FirmwareScreen) submitCreate() (Screen, tea.Cmd) {
	if s.cvVersion == nil {
		s.cvErr = "pick a firmware version first"
		return s, nil
	}
	batch, err := strconv.Atoi(strings.TrimSpace(s.cvBatchIn.Value()))
	if err != nil || batch < 1 || batch > 100 {
		s.cvErr = "batch size must be an integer 1–100"
		return s, nil
	}
	interval, err := strconv.Atoi(strings.TrimSpace(s.cvIntervalIn.Value()))
	if err != nil || interval < 1 {
		s.cvErr = "interval must be an integer ≥ 1 (minutes)"
		return s, nil
	}
	req := forgekeyapi.FirmwareRolloutCreate{
		FirmwareVersion:  fmt.Sprint(s.cvVersion.ID),
		BatchSizePercent: batch,
		IntervalMinutes:  interval,
		Name:             strings.TrimSpace(s.cvNameIn.Value()),
	}
	s.cvPending = true
	s.cvErr = ""
	fk := s.deps.ForgeKey
	ctx := s.deps.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return s, func() tea.Msg {
		rollout, err := fk.CreateFirmwareRollout(ctx, req)
		return fwRolloutCreatedMsg{rollout: rollout, err: err}
	}
}

// --- Version picker sub-phase ----------------------------------------------

func (s *FirmwareScreen) openPicker() (Screen, tea.Cmd) {
	s.pickCursor = 0
	for i := range s.cvOptions {
		if s.cvVersion != nil && fmt.Sprint(s.cvOptions[i].ID) == fmt.Sprint(s.cvVersion.ID) {
			s.pickCursor = i
			break
		}
	}
	s.mode = fwModePickVersion
	return s, nil
}

func (s *FirmwareScreen) handlePickKey(m tea.KeyMsg) (Screen, tea.Cmd) {
	switch m.String() {
	case "esc":
		s.mode = fwModeCreate
		return s, nil
	case "j", "down":
		if s.pickCursor < len(s.cvOptions)-1 {
			s.pickCursor++
		}
		return s, nil
	case "k", "up":
		if s.pickCursor > 0 {
			s.pickCursor--
		}
		return s, nil
	case "enter", " ":
		if s.pickCursor < len(s.cvOptions) {
			v := s.cvOptions[s.pickCursor]
			s.cvVersion = &v
			s.setCreateFocus(1) // advance to batch size
		}
		s.mode = fwModeCreate
		return s, nil
	}
	return s, nil
}

// firmwareDeviceTypeLabel renders the human device-type for a firmware row.
// The raw device_type field is an integer FK id; the readable name/code arrive
// as separate serializer fields, so prefer those and show nothing when absent
// rather than a meaningless number.
func firmwareDeviceTypeLabel(v forgekeyapi.FirmwareVersion) string {
	if v.DeviceTypeName != "" {
		return v.DeviceTypeName
	}
	return v.DeviceTypeCode
}

// rolloutStatusLabel maps a rollout status to its badge style, mirroring the
// web STATUS_COLORS (draft grey, active blue/ok, paused warn, completed ok,
// cancelled error).
func rolloutStatusLabel(status string) string {
	switch status {
	case "active", "completed":
		return StyleStatusOK.Render(status)
	case "paused":
		return StyleStatusWarn.Render(status)
	case "cancelled":
		return StyleStatusError.Render(status)
	default:
		return StyleMuted.Render(status)
	}
}

// proseBar is the bar this screen is DRAWING, in every state: a load in flight or
// failed draws loadBar's, and each of the two surfaces drawn in place of the
// rollout list — the New-Rollout form and its version picker — draws its own.
func (s *FirmwareScreen) proseBar() proseBar {
	if s.loading || s.loadErr != "" {
		return s.loadBar()
	}
	switch s.mode {
	case fwModeCreate:
		return s.createBar()
	case fwModePickVersion:
		return fwPickBar(len(s.cvOptions))
	}
	return s.viewBar()
}

// viewBar names every key that acts on the rollout list, as a record the honesty
// sweep can press (prose_bar.go).
//
// It used to be the literal "c new rollout · j/k select · s start/resume · a
// advance · p pause · x cancel · r refresh · esc back", written under all three
// sections with no window, so a fleet with a rollout history longer than the
// pane took the footer off the bottom. The arrows moved the cursor unnamed. And
// it named all four lifecycle keys on every rollout, where each acts only on the
// statuses rolloutAction allows — on the others it answers a warning and sends
// nothing — so a completed rollout was offered four writes that were each
// refused. They follow the selected rollout's status now, the way the reorder
// queue's lifecycle keys follow its row (rolloutActionItems).
func (s *FirmwareScreen) viewBar() proseBar {
	out := proseNavStep(listNavMoves(len(s.rollouts)))
	out = append(out, s.rolloutActionItems()...)
	return append(out, fwCreateItem, proseBarRefresh, proseBarEsc)
}

// fwViewCeiling is the TALLEST shape viewBar takes, which the window is budgeted
// against for the reason proseListWindow gives: every lifecycle key at once,
// which no single status offers, so no status the cursor lands on can fold the
// bar onto a row the budget did not reserve.
func fwViewCeiling() proseBar {
	out := proseNavStep(true)
	out = append(out,
		proseBarItem{Keys: []string{"s"}, Hint: "s resume"},
		proseBarItem{Keys: []string{"a"}, Hint: "a advance"},
		proseBarItem{Keys: []string{"p"}, Hint: "p pause"},
		proseBarItem{Keys: []string{"x"}, Hint: "x cancel"},
	)
	return append(out, fwCreateItem, proseBarRefresh, proseBarEsc)
}

var fwCreateItem = proseBarItem{Keys: []string{"c"}, Hint: "c new rollout"}

// rolloutActionItems are the lifecycle keys the SELECTED rollout's status
// allows, in the order the literal named them — the same preconditions
// handleViewKey hands rolloutAction, read here rather than restated as a second
// table. Nothing while a transition is already out: rolloutAction declines every
// one of them silently until it answers.
func (s *FirmwareScreen) rolloutActionItems() proseBar {
	if s.actionPending || s.cursor >= len(s.rollouts) {
		return nil
	}
	var out proseBar
	switch st := s.rollouts[s.cursor].Status; st {
	case "draft":
		out = append(out, proseBarItem{Keys: []string{"s"}, Hint: "s start"})
	case "paused":
		out = append(out, proseBarItem{Keys: []string{"s"}, Hint: "s resume"})
	case "active":
		out = append(out,
			proseBarItem{Keys: []string{"a"}, Hint: "a advance"},
			proseBarItem{Keys: []string{"p"}, Hint: "p pause"},
		)
	}
	if st := s.rollouts[s.cursor].Status; st == "active" || st == "paused" {
		out = append(out, proseBarItem{Keys: []string{"x"}, Hint: "x cancel"})
	}
	return out
}

// loadBar is the rollout list's bar while its load is out or has failed — what
// the view's key switch still answers with no rows drawn (prose_bar.go carries
// the defect and the decision). A refresh keeps the rollouts until it answers,
// so the selected rollout's lifecycle keys still send their transition: named
// because they act, and candidates for gating. A failed load replaces them with
// nothing, so they come off. j/k move a cursor the frame does not draw and `c`
// opens a form it does not draw either, so neither is named and both are
// ignored.
func (s *FirmwareScreen) loadBar() proseBar {
	return append(s.rolloutActionItems(), proseBarReloadFor(s.loadErr != ""), proseBarEsc)
}

// createBar is the New-Rollout form's bar. Its focus WRAPS over the four fields
// (setCreateFocus), so the whole focus segment acts from every field; enter
// opens the version picker on the first field — as space does — and submits on
// the others, where it is held while a create is out.
//
// It used to be "tab/↑↓ move · enter pick/submit · esc cancel" on every field,
// naming neither shift+tab nor space, and it vanished under "Creating…" while
// the focus keys and esc went on working.
func (s *FirmwareScreen) createBar() proseBar {
	out := proseBar{proseBarFieldFocus}
	switch {
	case s.cvFocus == 0:
		out = append(out, proseBarItem{Keys: []string{"enter", " "}, Hint: "enter/space pick version"})
	case !s.cvPending:
		out = append(out, proseBarItem{Keys: []string{"enter"}, Hint: "enter create rollout"})
	}
	return append(out, proseBarItem{Keys: []string{"esc"}, Hint: "esc cancel"})
}

// fwPickBar is the version picker's bar over `n` versions. Enter and space pick
// the highlighted version; over an empty list they go back to the form exactly
// as esc does, and the bar says so rather than naming esc alone.
func fwPickBar(n int) proseBar {
	out := proseNavStep(listNavMoves(n))
	if n == 0 {
		return append(out, proseBarItem{Keys: []string{"enter", " ", "esc"}, Hint: "enter/space/esc back"})
	}
	return append(out,
		proseBarItem{Keys: []string{"enter", " "}, Hint: "enter/space select"},
		proseBarItem{Keys: []string{"esc"}, Hint: "esc back"},
	)
}

func (s *FirmwareScreen) paneCells() int { return proseBarCells(s.terminalWidth) }

func (s *FirmwareScreen) View() string {
	cells := s.paneCells()
	if s.loading {
		return proseLoadingFrame("Loading firmware…", cells, s.proseBar())
	}
	if s.loadErr != "" {
		return proseFailedFrame(s.loadErr, s.terminalHeight, cells, s.proseBar())
	}
	switch s.mode {
	case fwModeCreate:
		return s.renderCreateForm(cells)
	case fwModePickVersion:
		return s.renderPicker(cells)
	}
	return s.renderView(cells)
}

// renderView draws the three sections — rollouts, versions, recent updates — and
// the bar, in a GIVE-ORDER that keeps the bar on the pane.
//
// WHAT IT REPLACES. Every row of all three sections was written and then the
// footer, with no window, so a shop with a rollout history or a version list
// longer than the pane lost the footer off the bottom — clampToBox drops from
// the BOTTOM — and every key with it.
//
// THE ORDER, and why the sections are not one window. The ROLLOUTS are the only
// section a key moves through, so they get a line-packed window of their own
// (proseLineWindow) with `↑ more above` / `↓ N more below` — markers j/k answer.
// The VERSIONS and RECENT UPDATES under them are read-only, and no key on this
// screen scrolls them: folding them into the rollouts' window would put them
// below a `↓ N more below` that pressing j at the last rollout never reaches — a
// marker promising what no key fetches. So they take the lines the rollouts
// leave, and where they run out the cut is marked with the one mark this layer
// uses for lines no key brings back (`… N more lines`, proseWriteRows' mark for
// an oversized row). The bar never gives; the rollouts give down to the window's
// floor; the read-only sections give first.
//
// One line is reserved for that mark whenever there is a section below to cut,
// so the rollouts can never take the room the mark needs.
func (s *FirmwareScreen) renderView(cells int) string {
	var b strings.Builder
	b.WriteString(StyleTitle.Render("Rollouts") + "\n")

	tail := s.tailLines()
	ceilRows := fwViewCeiling().rows(cells)
	used := 1 // the section title
	if len(s.rollouts) == 0 {
		b.WriteString(StyleMuted.Render("No rollout campaigns. Press c to stage one.") + "\n")
		used++
	} else {
		rows := make([]string, len(s.rollouts))
		for i, r := range s.rollouts {
			rows[i] = s.rolloutRow(i, r)
		}
		from, to, budget := 0, len(rows), 0
		if s.terminalHeight > 0 {
			budget = screenBodyHeight(s.terminalHeight) - used - 2 - ceilRows
			if len(tail) > 0 {
				budget--
			}
			if budget < proseListWindowFloor {
				budget = proseListWindowFloor
			}
			from, to = proseLineWindow(proseRowHeights(rows), s.cursor, s.windowStart, budget)
			s.windowStart = from
		}
		before := b.Len()
		proseWriteRows(&b, rows, from, to, budget)
		used += strings.Count(b.String()[before:], "\n")
	}

	if s.terminalHeight > 0 {
		room := screenBodyHeight(s.terminalHeight) - used - ceilRows
		if room < 1 {
			room = 1
		}
		if len(tail) > room && room < 3 {
			// No room for the separator as well as a line and the mark under it.
			tail = tail[1:]
		}
		if len(tail) > room {
			kept := room - 1
			omitted := len(tail) - kept
			tail = append(tail[:kept:kept], StyleMuted.Render(fmt.Sprintf("… %d more lines", omitted)))
		}
	}
	for _, line := range tail {
		b.WriteString(line + "\n")
	}
	b.WriteString("\n" + s.proseBar().render(cells))
	return b.String()
}

// rolloutRow is one rollout: its version, status and label, and its progress
// line under it.
func (s *FirmwareScreen) rolloutRow(i int, r forgekeyapi.FirmwareRollout) string {
	caret := "  "
	if i == s.cursor {
		caret = "▸ "
	}
	head := r.FirmwareVersionStr
	if head == "" {
		head = fmt.Sprintf("v#%v", r.FirmwareVersion)
	}
	meta := []string{}
	if r.DeviceTypeName != "" {
		meta = append(meta, r.DeviceTypeName)
	}
	if r.Name != "" {
		meta = append(meta, r.Name)
	}
	line := caret + head + "  [" + rolloutStatusLabel(r.Status) + "]"
	if len(meta) > 0 {
		line += " " + StyleMuted.Render(strings.Join(meta, " · "))
	}
	p := r.Progress
	inFlight := p.Pending + p.InProgress
	detail := fmt.Sprintf("      %d%%/wave · %dmin · %d on target · %d in flight · %d remaining",
		r.BatchSizePercent, r.IntervalMinutes, p.OnTarget, inFlight, p.Remaining)
	if p.Total > 0 {
		detail += fmt.Sprintf(" of %d", p.Total)
	}
	if p.Failed > 0 {
		detail += fmt.Sprintf(" · %d failed", p.Failed)
	}
	return line + "\n" + StyleMuted.Render(detail)
}

// tailLines are the read-only sections under the rollouts, as the LINES they
// draw — a blank separator first — so a stored newline in a version string is a
// line the give-order counts.
func (s *FirmwareScreen) tailLines() []string {
	var b strings.Builder
	b.WriteString("\n")
	if len(s.versions) > 0 {
		b.WriteString(StyleTitle.Render("Versions") + "\n")
		for _, v := range s.versions {
			line := "  · " + v.Version
			if label := firmwareDeviceTypeLabel(v); label != "" {
				line += " " + StyleMuted.Render("("+label+")")
			}
			if v.IsActive {
				line += " " + StyleStatusOK.Render("active")
			}
			if v.CreatedByUsername != "" {
				line += " " + StyleMuted.Render("by "+v.CreatedByUsername)
			}
			b.WriteString(line + "\n")
		}
	} else {
		b.WriteString(StyleMuted.Render("No firmware versions registered.") + "\n")
	}

	if len(s.updates) > 0 {
		b.WriteString("\n" + StyleTitle.Render("Recent updates") + "\n")
		limit := len(s.updates)
		if limit > 20 {
			limit = 20
		}
		for _, u := range s.updates[:limit] {
			name := u.DeviceMACAddress
			if name == "" {
				name = fmt.Sprintf("device %v", u.Device)
			}
			ver := u.FirmwareVersionStr
			if ver == "" {
				ver = fmt.Sprintf("v#%v", u.FirmwareVersion)
			}
			line := fmt.Sprintf("  · %s → %s", name, ver)
			if u.Status != "" {
				line += " " + StyleMuted.Render("["+u.Status+"]")
			}
			if !u.RequestedAt.IsZero() {
				line += " " + StyleMuted.Render(u.RequestedAt.Format("01-02 15:04"))
			}
			b.WriteString(line + "\n")
		}
	}
	return strings.Split(strings.TrimSuffix(b.String(), "\n"), "\n")
}

// renderCreateForm draws the New-Rollout form. It has no window — four fields
// and a fixed head — so its height is bounded by bounding the lines that carry a
// value the screen does not control: the chosen version's name, a create
// failure's OMS body, and the typed boxes, each held to one row of the pane
// (proseFormLine, woBoxView). The explanation under the title is FOLDED to the
// pane rather than left to clampToBox, which cut it unmarked at 80 columns.
func (s *FirmwareScreen) renderCreateForm(cells int) string {
	const labelCells = 2 + 16 + 1 // caret, padded label, space
	var b strings.Builder
	b.WriteString(StyleTitle.Render("New rollout") + "\n")
	b.WriteString(StyleMuted.Render(strings.Join(
		pickerWrap("Stage a firmware version across its device fleet in waves.", cells), "\n")) + "\n\n")

	versionVal := StyleMuted.Render("‹press enter to pick›")
	if s.cvVersion != nil {
		versionVal = s.cvVersion.Version
		if label := firmwareDeviceTypeLabel(*s.cvVersion); label != "" {
			versionVal += " (" + label + ")"
		}
		versionVal = proseFormLine(versionVal, cells-labelCells)
	}
	pad := strings.Repeat(" ", labelCells)
	rows := []struct {
		label string
		value string
	}{
		{"Firmware version", versionVal},
		{"Batch size %", woBoxView(s.cvBatchIn, cells, pad)},
		{"Interval (min)", woBoxView(s.cvIntervalIn, cells, pad)},
		{"Name", woBoxView(s.cvNameIn, cells, pad)},
	}
	for i, row := range rows {
		cursor := "  "
		if s.cvFocus == i {
			cursor = "▸ "
		}
		label := fmt.Sprintf("%-16s", row.label+":")
		line := cursor + StyleMuted.Render(label) + " " + row.value
		if s.cvFocus == i {
			line = StyleTitle.Render(cursor+label) + " " + row.value
		}
		b.WriteString(line + "\n")
	}

	if s.cvErr != "" {
		b.WriteString("\n" + StyleStatusError.Render("✗ "+proseFormLine(s.cvErr, cells-2)) + "\n")
	}
	if s.cvPending {
		b.WriteString("\n" + StyleMuted.Render("Creating…") + "\n")
	}
	b.WriteString("\n" + s.proseBar().render(cells))
	return b.String()
}

// renderPicker draws the version picker as a line-packed window
// (proseFlatListFrame). It used to write every version and then its footer, so a
// fleet with more firmware than the pane has rows took the footer off the
// bottom — on the one surface whose esc is the way back to the half-filled form.
func (s *FirmwareScreen) renderPicker(cells int) string {
	head := StyleTitle.Render("Pick firmware version") + "\n\n"
	if len(s.cvOptions) == 0 {
		return head + StyleMuted.Render("No active firmware versions available to roll out.") +
			"\n\n" + s.proseBar().render(cells)
	}
	rows := make([]string, len(s.cvOptions))
	for i, v := range s.cvOptions {
		cursor := "  "
		if i == s.pickCursor {
			cursor = "▸ "
		}
		line := cursor + v.Version
		if label := firmwareDeviceTypeLabel(v); label != "" {
			line += " " + StyleMuted.Render("("+label+")")
		}
		if i == s.pickCursor {
			line = StyleTitle.Render(line)
		}
		rows[i] = line
	}
	return proseFlatListFrame(head, rows, s.pickCursor, &s.pickStart, s.terminalHeight,
		cells, fwPickBar(proseFlatCeilingRows), s.proseBar())
}
