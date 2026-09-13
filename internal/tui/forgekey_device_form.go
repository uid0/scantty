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
	pickStart   int
	pickSearch  textinput.Model
	pickTyping  bool
	pickOptions []assetPickRow

	terminalHeight int
	terminalWidth  int
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
		s.terminalWidth = m.Width
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
	s.pickStart = 0
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

func (s *ForgeKeyDeviceFormScreen) paneCells() int { return proseBarCells(s.terminalWidth) }

// pickListBar is the location picker's bar with its filter box shut, and with
// both answers true it is the bar at its TALLEST — the ceiling the window is
// budgeted against, for the reason proseListWindow gives — so the ceiling and
// the drawn bar are one expression.
//
// It used to be a legend written ABOVE the rows, "Pick location — j/k move · /
// filter · enter select · esc back", naming `j/k` alone while the arrows moved
// the cursor too, and naming all four while the filter box was open and every
// one of those letters was a character in the query.
func (s *ForgeKeyDeviceFormScreen) pickListBar(moves, options bool) proseBar {
	out := append(proseNavStep(moves), proseBarItem{Keys: []string{"/"}, Hint: "/ filter"})
	if options {
		out = append(out, proseBarItem{Keys: []string{"enter"}, Hint: "enter select"})
	}
	return append(out, proseBarItem{Keys: []string{"esc"}, Hint: "esc back"})
}

// proseBar is the bar this screen is DRAWING, for whichever surface is up — the
// one-field form, the location picker drawn in its place, or that picker's
// filter box, or the load frame, which answers `esc` alone — and nil while a
// save is out, whose working line takes the bar's place.
//
// ONE RECORD PER SURFACE is what converting a screen with a second cursor
// surface comes to: the picker is the reason this screen is on the navigation
// roster at all, so converting the form alone would have left it behind a
// receiver the classifier counts as swept.
func (s *ForgeKeyDeviceFormScreen) proseBar() proseBar {
	switch {
	case s.phase == fkDeviceFormLoading:
		// The loading phase answers `esc` and nothing else — the switch returns
		// before any other arm — so that is the whole bar (prose_bar.go's
		// load-state note).
		return proseBar{{Keys: []string{"esc"}, Hint: "esc cancel"}}
	case s.phase == fkDeviceFormPick && s.pickTyping:
		return proseBar{{Keys: []string{"enter", "esc"}, Hint: "enter/esc close filter"}}
	case s.phase == fkDeviceFormPick:
		n := len(s.pickOptions)
		return s.pickListBar(listNavMoves(n), n > 0)
	case s.saving:
		return nil
	}
	return proseBar{
		{Keys: []string{" "}, Hint: "space pick location"},
		{Keys: []string{"enter"}, Hint: "enter save"},
		{Keys: []string{"esc"}, Hint: "esc cancel"},
	}
}

func (s *ForgeKeyDeviceFormScreen) View() string {
	if s.phase == fkDeviceFormLoading {
		return proseLoadingFrame("Loading…", s.paneCells(), s.proseBar())
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
		// ONE ROW, cut marked (proseFormLine): the failure is an OMS body, and a
		// gateway's 502 page written out whole pushed this form's bar off the
		// bottom of the pane on the frame a failed load lands on.
		b.WriteString("\n" + StyleStatusError.Render(proseFormLine("locations failed to load: "+s.loadErr, s.paneCells())) + "\n")
	}
	b.WriteString("\n")
	if s.saving {
		b.WriteString(StyleMuted.Render("Saving…"))
		return b.String()
	}
	// A failed save used to take the BAR's place, so the frame that most needs
	// a way forward named none; the failure is a line of its own above it now.
	if s.errMsg != "" {
		b.WriteString(StyleStatusError.Render("✗ "+s.errMsg) + "\n\n")
	}
	b.WriteString(s.proseBar().render(s.paneCells()))
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
	cells := s.paneCells()
	b.WriteString(StyleTitle.Render("Pick location") + "\n\n")
	if s.pickTyping || s.pickSearch.Value() != "" {
		b.WriteString(StyleMuted.Render("filter: ") + woBoxView(s.pickSearch, cells, "filter: ") + "\n\n")
	}
	if len(s.pickOptions) == 0 {
		b.WriteString(StyleMuted.Render("(no matches)") + "\n\n")
		return b.String() + s.proseBar().render(cells)
	}
	rows := make([]string, len(s.pickOptions))
	for i, opt := range s.pickOptions {
		caret := "    "
		if i == s.pickCursor {
			caret = "  ▸ "
		}
		switch {
		case i == s.pickCursor:
			rows[i] = StyleSidebarItemActive.Render(caret + opt.label)
		case opt.clear:
			rows[i] = caret + StyleMuted.Render(opt.label)
		default:
			rows[i] = caret + opt.label
		}
	}
	// Every location the shop has, so the window is DERIVED from the pane and
	// the folded bar rather than a flat twelve rows, which ran past any terminal
	// shorter than about twenty rows and took the bar with it.
	return proseFlatListFrame(b.String(), rows, s.pickCursor, &s.pickStart, s.terminalHeight,
		cells, s.pickListBar(true, true), s.proseBar())
}
