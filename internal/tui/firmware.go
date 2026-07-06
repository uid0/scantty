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
func (s *FirmwareScreen) WantsRawInput() bool { return s.mode != fwModeView }

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

// rolloutStatusStyle maps a rollout status to its badge style, mirroring the
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

func (s *FirmwareScreen) View() string {
	if s.loading {
		return StyleMuted.Render("Loading firmware…")
	}
	if s.loadErr != "" {
		return StyleStatusError.Render("Error: ") + s.loadErr + "\n\n" + StyleMuted.Render("r retry · esc back")
	}
	switch s.mode {
	case fwModeCreate:
		return s.renderCreateForm()
	case fwModePickVersion:
		return s.renderPicker()
	}

	var b strings.Builder

	// Rollouts first — the actionable surface this screen adds.
	b.WriteString(StyleTitle.Render("Rollouts") + "\n")
	if len(s.rollouts) == 0 {
		b.WriteString(StyleMuted.Render("No rollout campaigns. Press c to stage one.") + "\n\n")
	} else {
		for i, r := range s.rollouts {
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
			b.WriteString(line + "\n")
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
			b.WriteString(StyleMuted.Render(detail) + "\n")
		}
		b.WriteString("\n")
	}

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
		b.WriteString("\n")
	} else {
		b.WriteString(StyleMuted.Render("No firmware versions registered.") + "\n\n")
	}

	if len(s.updates) > 0 {
		b.WriteString(StyleTitle.Render("Recent updates") + "\n")
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
		b.WriteString("\n")
	}

	b.WriteString(StyleMuted.Render(s.footerHint()))
	return b.String()
}

func (s *FirmwareScreen) footerHint() string {
	parts := []string{"c new rollout"}
	if len(s.rollouts) > 0 {
		parts = append(parts, "j/k select", "s start/resume", "a advance", "p pause", "x cancel")
	}
	parts = append(parts, "r refresh", "esc back")
	return strings.Join(parts, " · ")
}

func (s *FirmwareScreen) renderCreateForm() string {
	var b strings.Builder
	b.WriteString(StyleTitle.Render("New rollout") + "\n")
	b.WriteString(StyleMuted.Render("Stage a firmware version across its device fleet in waves.") + "\n\n")

	versionVal := StyleMuted.Render("‹press enter to pick›")
	if s.cvVersion != nil {
		versionVal = s.cvVersion.Version
		if label := firmwareDeviceTypeLabel(*s.cvVersion); label != "" {
			versionVal += " " + StyleMuted.Render("("+label+")")
		}
	}
	rows := []struct {
		label string
		value string
	}{
		{"Firmware version", versionVal},
		{"Batch size %", s.cvBatchIn.View()},
		{"Interval (min)", s.cvIntervalIn.View()},
		{"Name", s.cvNameIn.View()},
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
		b.WriteString("\n" + StyleStatusError.Render("✗ "+s.cvErr) + "\n")
	}
	if s.cvPending {
		b.WriteString("\n" + StyleMuted.Render("Creating…"))
	} else {
		b.WriteString("\n" + StyleMuted.Render("tab/↑↓ move · enter pick/submit · esc cancel"))
	}
	return b.String()
}

func (s *FirmwareScreen) renderPicker() string {
	var b strings.Builder
	b.WriteString(StyleTitle.Render("Pick firmware version") + "\n\n")
	if len(s.cvOptions) == 0 {
		b.WriteString(StyleMuted.Render("No active firmware versions available to roll out.") + "\n\n")
		b.WriteString(StyleMuted.Render("esc back"))
		return b.String()
	}
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
		b.WriteString(line + "\n")
	}
	b.WriteString("\n" + StyleMuted.Render("j/k move · enter select · esc back"))
	return b.String()
}
