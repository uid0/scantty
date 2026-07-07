// ForgeKey device record edit — a focused metadata form.
//
// TUI counterpart to the OMS web device-management edit surface. The web edits
// exactly ONE field on a device RECORD through the device-update endpoint: the
// default `location` assignment (ForgeKeyDevicesPage's inline per-device
// dropdown PATCHes {location}). So this form mirrors that field set exactly — a
// single Location picker — rather than the serializer's full __all__ set:
// name/description/device_type are not editable anywhere in the web UI, and the
// device-reported status / live sub-state / enrollment-identity fields are
// read-only, so exposing any of them would drift past the web. is_active is
// changed through the separate staff-only retire/reactivate actions, not here.
//
// Reached with `E` from the device detail screen (#64); returns there on save so
// the operator sees the refreshed record. Like the other CRUD forms it flips to
// raw input for its whole lifetime (WantsRawInput always true).
package tui

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/uid0/scantty/internal/forgekeyapi"
	"github.com/uid0/scantty/internal/omsapi"
)

type fkDeviceFormPhase int

const (
	fkDeviceFormLoading fkDeviceFormPhase = iota
	fkDeviceFormEdit
	fkDeviceFormPick
)

// ForgeKeyDeviceFormScreen edits a device record's location assignment — the one
// field the web device-update endpoint exposes.
type ForgeKeyDeviceFormScreen struct {
	deps   Deps
	device *forgekeyapi.Device

	phase   fkDeviceFormPhase
	loadErr string
	saving  bool
	errMsg  string

	locations []omsapi.Location

	locationID *int // nil = unassigned

	// Location picker state.
	pickCursor  int
	pickSearch  textinput.Model
	pickTyping  bool
	pickOptions []assetPickRow

	terminalHeight int
}

type fkDeviceFormLocsMsg struct {
	locations []omsapi.Location
	err       error
}

type fkDeviceFormSavedMsg struct {
	device *forgekeyapi.Device
	err    error
}

// NewForgeKeyDeviceFormScreen builds the edit form from the already-loaded device
// the detail screen is showing (no re-fetch). Locations load asynchronously for
// the picker + labels.
func NewForgeKeyDeviceFormScreen(deps Deps, device *forgekeyapi.Device) *ForgeKeyDeviceFormScreen {
	s := &ForgeKeyDeviceFormScreen{
		deps:   deps,
		device: device,
		phase:  fkDeviceFormLoading,
	}
	if device != nil && device.Location != nil {
		v := *device.Location
		s.locationID = &v
	}
	s.pickSearch = textinput.New()
	s.pickSearch.Prompt = ""
	s.pickSearch.Placeholder = "filter"
	s.pickSearch.CharLimit = 60
	return s
}

func (s *ForgeKeyDeviceFormScreen) Title() string {
	if s.device != nil && s.device.Name != "" {
		return "Edit device: " + s.device.Name
	}
	return "Edit device"
}

func (s *ForgeKeyDeviceFormScreen) WantsRawInput() bool { return true }

func (s *ForgeKeyDeviceFormScreen) Init() tea.Cmd {
	return tea.Batch(s.loadLocations(), textinput.Blink)
}

func (s *ForgeKeyDeviceFormScreen) ctx() context.Context {
	if s.deps.Ctx != nil {
		return s.deps.Ctx
	}
	return context.Background()
}

func (s *ForgeKeyDeviceFormScreen) loadLocations() tea.Cmd {
	deps := s.deps
	ctx := s.ctx()
	return func() tea.Msg {
		page, err := deps.OMS.ListLocations(ctx, nil)
		if err != nil {
			return fkDeviceFormLocsMsg{err: err}
		}
		return fkDeviceFormLocsMsg{locations: page.Results}
	}
}

func (s *ForgeKeyDeviceFormScreen) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		s.terminalHeight = m.Height
		return s, nil
	case fkDeviceFormLocsMsg:
		if m.err != nil {
			s.loadErr = m.err.Error()
		} else {
			s.locations = m.locations
		}
		// The location list is a convenience (labels + picker); a failure
		// still lets the operator save the unchanged value, so drop into the
		// edit view either way rather than trapping them on a load error.
		if s.phase == fkDeviceFormLoading {
			s.phase = fkDeviceFormEdit
		}
		return s, nil
	case fkDeviceFormSavedMsg:
		s.saving = false
		if m.err != nil {
			s.errMsg = m.err.Error()
			return s, Status("save failed: "+m.err.Error(), StatusError)
		}
		id, name := "", ""
		if m.device != nil {
			id = fmt.Sprint(m.device.ID)
			name = m.device.Name
		} else if s.device != nil {
			id = fmt.Sprint(s.device.ID)
		}
		status := "device updated"
		if name != "" {
			status = "device updated: " + name
		}
		return s, tea.Batch(
			Status(status, StatusOK),
			SwitchTo(WSForgeKey, NewForgeKeyDeviceDetailScreen(s.deps, id)),
		)
	case tea.KeyMsg:
		if s.phase == fkDeviceFormLoading {
			if m.String() == "esc" {
				return s, s.cancelCmd()
			}
			return s, nil
		}
		if s.phase == fkDeviceFormPick {
			return s.updatePick(m)
		}
		return s.updateEdit(m)
	}
	// Forward stray msgs (e.g. cursor blink) to the filter input while typing.
	if s.phase == fkDeviceFormPick && s.pickTyping {
		var cmd tea.Cmd
		s.pickSearch, cmd = s.pickSearch.Update(msg)
		return s, cmd
	}
	return s, nil
}

func (s *ForgeKeyDeviceFormScreen) updateEdit(m tea.KeyMsg) (Screen, tea.Cmd) {
	switch m.String() {
	case "esc":
		return s, s.cancelCmd()
	case " ":
		s.openPicker()
		return s, textinput.Blink
	case "enter":
		if s.saving {
			return s, nil
		}
		return s.submit()
	}
	return s, nil
}

func (s *ForgeKeyDeviceFormScreen) openPicker() {
	s.phase = fkDeviceFormPick
	s.pickTyping = false
	s.pickSearch.SetValue("")
	s.pickSearch.Blur()
	s.applyPickFilter()
	s.pickCursor = 0
	// Rest the cursor on the current selection so re-opening is a no-op.
	if s.locationID != nil {
		sel := strconv.Itoa(*s.locationID)
		for i, o := range s.pickOptions {
			if !o.clear && o.key == sel {
				s.pickCursor = i
				break
			}
		}
	}
}

func (s *ForgeKeyDeviceFormScreen) applyPickFilter() {
	q := strings.ToLower(strings.TrimSpace(s.pickSearch.Value()))
	// The clear row is always offered (unfiltered): location is nullable and the
	// web clears it with an empty selection → {location: null}.
	opts := []assetPickRow{{clear: true, label: "(none — unassigned)"}}
	for _, l := range s.locations {
		label := l.Name
		if l.Code != "" {
			label = fmt.Sprintf("%s (%s)", l.Name, l.Code)
		}
		if q == "" || strings.Contains(strings.ToLower(label), q) {
			opts = append(opts, assetPickRow{key: strconv.Itoa(l.ID), label: label})
		}
	}
	s.pickOptions = opts
	if s.pickCursor >= len(s.pickOptions) {
		s.pickCursor = 0
	}
}

func (s *ForgeKeyDeviceFormScreen) updatePick(m tea.KeyMsg) (Screen, tea.Cmd) {
	if s.pickTyping {
		switch m.Type {
		case tea.KeyEsc:
			s.pickTyping = false
			s.pickSearch.Blur()
			return s, nil
		case tea.KeyEnter:
			s.pickTyping = false
			s.pickSearch.Blur()
			s.applyPickFilter()
			s.pickCursor = 0
			return s, nil
		}
		var cmd tea.Cmd
		s.pickSearch, cmd = s.pickSearch.Update(m)
		s.applyPickFilter()
		return s, cmd
	}

	switch m.String() {
	case "esc":
		s.phase = fkDeviceFormEdit
	case "j", "down":
		if s.pickCursor < len(s.pickOptions)-1 {
			s.pickCursor++
		}
	case "k", "up":
		if s.pickCursor > 0 {
			s.pickCursor--
		}
	case "/":
		s.pickTyping = true
		s.pickSearch.Focus()
		return s, textinput.Blink
	case "enter":
		s.commitPick()
	}
	return s, nil
}

func (s *ForgeKeyDeviceFormScreen) commitPick() {
	if s.pickCursor >= 0 && s.pickCursor < len(s.pickOptions) {
		s.locationID = pickInt(s.pickOptions[s.pickCursor])
	}
	s.phase = fkDeviceFormEdit
	s.pickTyping = false
	s.pickSearch.SetValue("")
	s.pickSearch.Blur()
}

func (s *ForgeKeyDeviceFormScreen) submit() (Screen, tea.Cmd) {
	if s.device == nil {
		return s, nil
	}
	body := forgekeyapi.DeviceWrite{Location: s.locationID}
	s.saving = true
	s.errMsg = ""
	deps := s.deps
	ctx := s.ctx()
	id := fmt.Sprint(s.device.ID)
	return s, func() tea.Msg {
		dev, err := deps.ForgeKey.UpdateDevice(ctx, id, body)
		return fkDeviceFormSavedMsg{device: dev, err: err}
	}
}

func (s *ForgeKeyDeviceFormScreen) cancelCmd() tea.Cmd {
	id := ""
	if s.device != nil {
		id = fmt.Sprint(s.device.ID)
	}
	return SwitchTo(WSForgeKey, NewForgeKeyDeviceDetailScreen(s.deps, id))
}

func (s *ForgeKeyDeviceFormScreen) View() string {
	if s.phase == fkDeviceFormLoading {
		if s.loadErr != "" {
			return StyleStatusError.Render("Error: ") + s.loadErr + "\n\n" + StyleMuted.Render("esc to go back")
		}
		return StyleMuted.Render("Loading…")
	}
	if s.phase == fkDeviceFormPick {
		return s.viewPick()
	}
	return s.viewForm()
}

func (s *ForgeKeyDeviceFormScreen) viewForm() string {
	var b strings.Builder
	b.WriteString(StyleMuted.Render("Edit device metadata — the web edits only the default location.") + "\n\n")
	b.WriteString("▸ " + StyleTitle.Render("Location: ") + s.locationLabel() + "\n")
	if s.loadErr != "" {
		b.WriteString("\n" + StyleStatusError.Render("locations failed to load: "+s.loadErr) + "\n")
	}
	b.WriteString("\n")
	switch {
	case s.saving:
		b.WriteString(StyleMuted.Render("Saving…"))
	case s.errMsg != "":
		b.WriteString(StyleStatusError.Render("✗ " + s.errMsg))
	default:
		b.WriteString(StyleMuted.Render("space pick location · enter save · esc cancel"))
	}
	return b.String()
}

func (s *ForgeKeyDeviceFormScreen) locationLabel() string {
	if s.locationID == nil {
		return StyleMuted.Render("(unassigned)")
	}
	if name := s.locationName(*s.locationID); name != "" {
		return name
	}
	return fmt.Sprintf("#%d", *s.locationID)
}

func (s *ForgeKeyDeviceFormScreen) locationName(id int) string {
	for _, l := range s.locations {
		if l.ID == id {
			if l.Code != "" {
				return fmt.Sprintf("%s (%s)", l.Name, l.Code)
			}
			return l.Name
		}
	}
	return ""
}

func (s *ForgeKeyDeviceFormScreen) viewPick() string {
	var b strings.Builder
	b.WriteString(StyleMuted.Render("Pick location — j/k move · / filter · enter select · esc back") + "\n\n")
	if s.pickTyping || s.pickSearch.Value() != "" {
		b.WriteString(StyleMuted.Render("filter: ") + s.pickSearch.View() + "\n\n")
	}
	if len(s.pickOptions) == 0 {
		b.WriteString(StyleMuted.Render("(no matches)"))
		return b.String()
	}
	const window = 12
	start, end := fieldWindow(s.pickCursor, len(s.pickOptions), window)
	if start > 0 {
		b.WriteString(StyleMuted.Render(fmt.Sprintf("  ↑ %d more above", start)) + "\n")
	}
	for i := start; i < end; i++ {
		caret := "    "
		if i == s.pickCursor {
			caret = "  ▸ "
		}
		opt := s.pickOptions[i]
		switch {
		case i == s.pickCursor:
			b.WriteString(StyleSidebarItemActive.Render(caret+opt.label) + "\n")
		case opt.clear:
			b.WriteString(caret + StyleMuted.Render(opt.label) + "\n")
		default:
			b.WriteString(caret + opt.label + "\n")
		}
	}
	if end < len(s.pickOptions) {
		b.WriteString(StyleMuted.Render(fmt.Sprintf("  ↓ %d more below", len(s.pickOptions)-end)) + "\n")
	}
	return b.String()
}
