// Location CRUD — list + create/edit form.
//
// TUI counterpart to the web LocationFormPage.tsx (+ the /inventory/locations
// list). Mirrors the FULL writable field set of the web form + serializer:
// name, description, parent and is_active (the QR image + access_code are
// server-managed and read-only, surfaced on the detail screen). The parent
// picker reuses the item form's searchable sub-phase idiom and excludes the
// location itself in edit mode.
//
// LocationListScreen is reached with the global `L` hotkey (app.go). enter opens
// the location detail screen; c/E/x create/edit/delete. Like the category list
// it is a plain screen that flips to raw input only during the delete confirm.
//
// NOTE: the backend gates location create/update/delete behind IsAdminUser — a
// non-staff operator gets a 403 on save/delete (surfaced as a status error).
package tui

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/uid0/scantty/internal/omsapi"
)

// ===========================================================================
// LocationFormScreen
// ===========================================================================

const (
	lfName = iota
	lfDescription
	lfParent
	lfIsActive
	lfFieldMax
)

type locationFormPhase int

const (
	locationPhaseForm locationFormPhase = iota
	locationPhaseParentPick
)

var locationFieldLabel = map[int]string{
	lfName:        "Name",
	lfDescription: "Description",
	lfParent:      "Parent location",
	lfIsActive:    "Active",
}

type LocationFormScreen struct {
	deps  Deps
	edit  bool
	locID string

	loading bool
	loadErr string
	saving  bool
	errMsg  string

	locations  []omsapi.Location
	loc        *omsapi.Location
	refArrived bool
	locArrived bool

	terminalHeight int

	inputs   []textinput.Model
	parentID *int
	isActive bool

	fields []int
	cursor int

	phase       locationFormPhase
	pickCursor  int
	pickSearch  textinput.Model
	pickTyping  bool
	pickOptions []itemPickOption
}

type locationRefLoadedMsg struct {
	locations []omsapi.Location
	err       error
}

type locationFormLoadedMsg struct {
	loc *omsapi.Location
	err error
}

type locationSavedMsg struct {
	loc *omsapi.Location
	err error
}

func NewLocationFormScreen(deps Deps, locID string) *LocationFormScreen {
	edit := strings.TrimSpace(locID) != ""
	s := &LocationFormScreen{
		deps:     deps,
		edit:     edit,
		locID:    strings.TrimSpace(locID),
		loading:  true,
		isActive: true, // web default
	}
	s.inputs = make([]textinput.Model, lfFieldMax)
	for _, id := range []int{lfName, lfDescription} {
		ti := textinput.New()
		ti.Prompt = ""
		if id == lfName {
			ti.CharLimit = 100
			ti.Placeholder = "location name"
		} else {
			ti.CharLimit = 500
			ti.Placeholder = "optional"
		}
		s.inputs[id] = ti
	}
	s.pickSearch = textinput.New()
	s.pickSearch.Prompt = ""
	s.pickSearch.Placeholder = "filter"
	s.pickSearch.CharLimit = 60

	s.fields = []int{lfName, lfDescription, lfParent, lfIsActive}
	s.syncFocus()
	return s
}

func (s *LocationFormScreen) Title() string {
	if s.edit {
		if s.loc != nil && s.loc.Name != "" {
			return "Edit location: " + s.loc.Name
		}
		return "Edit location"
	}
	return "New location"
}

func (s *LocationFormScreen) WantsRawInput() bool { return true }

func (s *LocationFormScreen) Init() tea.Cmd {
	cmds := []tea.Cmd{s.loadRefData(), textinput.Blink}
	if s.edit {
		cmds = append(cmds, s.loadLocation())
	}
	return tea.Batch(cmds...)
}

func (s *LocationFormScreen) ctx() context.Context {
	if s.deps.Ctx != nil {
		return s.deps.Ctx
	}
	return context.Background()
}

func (s *LocationFormScreen) loadRefData() tea.Cmd {
	deps := s.deps
	ctx := s.ctx()
	return func() tea.Msg {
		locs, err := deps.OMS.ListLocations(ctx, nil)
		if err != nil {
			return locationRefLoadedMsg{err: err}
		}
		return locationRefLoadedMsg{locations: locs.Results}
	}
}

func (s *LocationFormScreen) loadLocation() tea.Cmd {
	deps := s.deps
	ctx := s.ctx()
	id := s.locID
	return func() tea.Msg {
		loc, err := deps.OMS.GetLocation(ctx, id)
		return locationFormLoadedMsg{loc: loc, err: err}
	}
}

func (s *LocationFormScreen) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		s.terminalHeight = m.Height
		return s, nil
	case locationRefLoadedMsg:
		s.refArrived = true
		if m.err != nil {
			s.loadErr = m.err.Error()
		} else {
			s.locations = m.locations
		}
		return s, s.maybeFinalizeLoad()
	case locationFormLoadedMsg:
		s.locArrived = true
		if m.err != nil {
			s.loadErr = m.err.Error()
		} else {
			s.loc = m.loc
		}
		return s, s.maybeFinalizeLoad()
	case locationSavedMsg:
		s.saving = false
		if m.err != nil {
			s.errMsg = m.err.Error()
			return s, Status("save failed: "+m.err.Error(), StatusError)
		}
		verb := "created"
		if s.edit {
			verb = "updated"
		}
		id := s.locID
		name := ""
		if m.loc != nil {
			id = strconv.Itoa(m.loc.ID)
			name = m.loc.Name
		}
		return s, tea.Batch(
			Status(fmt.Sprintf("location %s: %s", verb, name), StatusOK),
			SwitchTo(WSInventory, NewLocationDetailScreen(s.deps, id)),
		)
	case tea.KeyMsg:
		if s.loading {
			if m.String() == "esc" {
				return s, s.cancelCmd()
			}
			return s, nil
		}
		if s.phase == locationPhaseParentPick {
			return s.updatePickPhase(m)
		}
		return s.updateFormPhase(m)
	}

	if s.phase == locationPhaseParentPick {
		var cmd tea.Cmd
		s.pickSearch, cmd = s.pickSearch.Update(msg)
		return s, cmd
	}
	if id, ok := s.currentFieldID(); ok && (id == lfName || id == lfDescription) {
		var cmd tea.Cmd
		s.inputs[id], cmd = s.inputs[id].Update(msg)
		return s, cmd
	}
	return s, nil
}

func (s *LocationFormScreen) maybeFinalizeLoad() tea.Cmd {
	if !s.refArrived {
		return nil
	}
	if s.edit && !s.locArrived {
		return nil
	}
	s.loading = false
	if s.loadErr == "" && s.edit && s.loc != nil {
		s.hydrate()
	}
	s.syncFocus()
	return nil
}

func (s *LocationFormScreen) hydrate() {
	l := s.loc
	s.inputs[lfName].SetValue(l.Name)
	s.inputs[lfDescription].SetValue(l.Description)
	s.parentID = l.Parent
	s.isActive = l.IsActive
}

func (s *LocationFormScreen) currentFieldID() (int, bool) {
	if s.cursor < 0 || s.cursor >= len(s.fields) {
		return 0, false
	}
	return s.fields[s.cursor], true
}

func (s *LocationFormScreen) syncFocus() {
	for _, id := range []int{lfName, lfDescription} {
		s.inputs[id].Blur()
	}
	if id, ok := s.currentFieldID(); ok && (id == lfName || id == lfDescription) {
		s.inputs[id].Focus()
	}
}

func (s *LocationFormScreen) updateFormPhase(m tea.KeyMsg) (Screen, tea.Cmd) {
	switch m.String() {
	case "esc":
		return s, s.cancelCmd()
	case "tab", "down":
		s.moveCursor(+1)
		return s, textinput.Blink
	case "shift+tab", "up":
		s.moveCursor(-1)
		return s, textinput.Blink
	case "enter":
		if s.saving {
			return s, nil
		}
		return s.submit()
	}

	id, ok := s.currentFieldID()
	if !ok {
		return s, nil
	}
	switch id {
	case lfParent:
		if m.String() == " " {
			s.openParentPicker()
			return s, textinput.Blink
		}
		return s, nil
	case lfIsActive:
		if m.String() == " " {
			s.isActive = !s.isActive
		}
		return s, nil
	default:
		var cmd tea.Cmd
		s.inputs[id], cmd = s.inputs[id].Update(m)
		return s, cmd
	}
}

func (s *LocationFormScreen) moveCursor(delta int) {
	n := len(s.fields)
	if n == 0 {
		return
	}
	s.cursor = (s.cursor + delta + n) % n
	s.syncFocus()
}

func (s *LocationFormScreen) openParentPicker() {
	s.phase = locationPhaseParentPick
	s.pickTyping = false
	s.pickSearch.SetValue("")
	s.pickSearch.Blur()
	s.applyParentFilter()
	s.pickCursor = 0
	if s.parentID != nil {
		for i, o := range s.pickOptions {
			if !o.clear && o.id == *s.parentID {
				s.pickCursor = i
				break
			}
		}
	}
}

func (s *LocationFormScreen) applyParentFilter() {
	q := strings.ToLower(strings.TrimSpace(s.pickSearch.Value()))
	selfID := -1
	if s.edit {
		if v, err := strconv.Atoi(s.locID); err == nil {
			selfID = v
		}
	}
	opts := []itemPickOption{{clear: true, label: "(none — top level)"}}
	for _, l := range s.locations {
		if l.ID == selfID {
			continue
		}
		label := l.Name
		if l.ParentName != "" {
			label = fmt.Sprintf("%s (in %s)", l.Name, l.ParentName)
		}
		if q == "" || strings.Contains(strings.ToLower(label), q) {
			opts = append(opts, itemPickOption{id: l.ID, label: label})
		}
	}
	s.pickOptions = opts
	if s.pickCursor >= len(s.pickOptions) {
		s.pickCursor = 0
	}
}

func (s *LocationFormScreen) updatePickPhase(m tea.KeyMsg) (Screen, tea.Cmd) {
	if s.pickTyping {
		switch m.Type {
		case tea.KeyEsc:
			s.pickTyping = false
			s.pickSearch.Blur()
			return s, nil
		case tea.KeyEnter:
			s.pickTyping = false
			s.pickSearch.Blur()
			s.applyParentFilter()
			s.pickCursor = 0
			return s, nil
		}
		var cmd tea.Cmd
		s.pickSearch, cmd = s.pickSearch.Update(m)
		s.applyParentFilter()
		return s, cmd
	}

	switch m.String() {
	case "esc":
		s.phase = locationPhaseForm
		s.syncFocus()
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
		s.commitParent()
	}
	return s, nil
}

func (s *LocationFormScreen) commitParent() {
	if s.pickCursor >= 0 && s.pickCursor < len(s.pickOptions) {
		opt := s.pickOptions[s.pickCursor]
		if opt.clear {
			s.parentID = nil
		} else {
			id := opt.id
			s.parentID = &id
		}
	}
	s.phase = locationPhaseForm
	s.pickTyping = false
	s.pickSearch.SetValue("")
	s.pickSearch.Blur()
	s.syncFocus()
}

func (s *LocationFormScreen) submit() (Screen, tea.Cmd) {
	body, err := s.buildPayload()
	if err != nil {
		s.errMsg = err.Error()
		return s, Status(err.Error(), StatusError)
	}
	s.saving = true
	s.errMsg = ""
	deps := s.deps
	ctx := s.ctx()
	edit := s.edit
	id := s.locID
	return s, func() tea.Msg {
		var loc *omsapi.Location
		var e error
		if edit {
			loc, e = deps.OMS.UpdateLocation(ctx, id, body)
		} else {
			loc, e = deps.OMS.CreateLocation(ctx, body)
		}
		return locationSavedMsg{loc: loc, err: e}
	}
}

func (s *LocationFormScreen) buildPayload() (omsapi.LocationWrite, error) {
	var w omsapi.LocationWrite
	name := strings.TrimSpace(s.inputs[lfName].Value())
	if name == "" {
		return w, errors.New("name is required")
	}
	w = omsapi.LocationWrite{
		Name:        name,
		Description: strings.TrimSpace(s.inputs[lfDescription].Value()),
		Parent:      s.parentID,
		IsActive:    s.isActive,
	}
	return w, nil
}

func (s *LocationFormScreen) cancelCmd() tea.Cmd {
	if s.edit && s.locID != "" {
		return SwitchTo(WSInventory, NewLocationDetailScreen(s.deps, s.locID))
	}
	return SwitchTo(WSInventory, NewLocationListScreen(s.deps))
}

func (s *LocationFormScreen) View() string {
	if s.loading {
		return StyleMuted.Render("Loading…")
	}
	if s.loadErr != "" {
		return StyleStatusError.Render("Error: ") + s.loadErr + "\n\n" + StyleMuted.Render("esc to go back")
	}
	if s.phase == locationPhaseParentPick {
		return s.viewPick()
	}
	return s.viewForm()
}

func (s *LocationFormScreen) viewForm() string {
	var b strings.Builder
	b.WriteString(StyleMuted.Render(s.helpText()) + "\n\n")
	for i := range s.fields {
		b.WriteString(s.renderField(i) + "\n")
	}
	b.WriteString("\n")
	if s.saving {
		b.WriteString(StyleMuted.Render("Saving…"))
	} else if s.errMsg != "" {
		b.WriteString(StyleStatusError.Render("✗ " + s.errMsg))
	}
	return b.String()
}

func (s *LocationFormScreen) renderField(i int) string {
	id := s.fields[i]
	caret := "  "
	if i == s.cursor {
		caret = "▸ "
	}
	label := locationFieldLabel[id]
	var value string
	switch id {
	case lfParent:
		value = s.parentLabel()
	case lfIsActive:
		if s.isActive {
			value = StyleStatusOK.Render("[x] yes")
		} else {
			value = StyleMuted.Render("[ ] no")
		}
	default:
		value = s.inputs[id].View()
	}
	return caret + StyleTitle.Render(label+": ") + value
}

func (s *LocationFormScreen) parentLabel() string {
	if s.parentID == nil {
		return StyleMuted.Render("(none — top level)")
	}
	for _, l := range s.locations {
		if l.ID == *s.parentID {
			return l.Name
		}
	}
	return fmt.Sprintf("#%d", *s.parentID)
}

func (s *LocationFormScreen) helpText() string {
	kindHelp := "type to edit"
	if id, ok := s.currentFieldID(); ok {
		switch id {
		case lfParent:
			kindHelp = "space to pick parent"
		case lfIsActive:
			kindHelp = "space toggle"
		}
	}
	return kindHelp + " · tab/↑↓ move · enter save · esc cancel"
}

func (s *LocationFormScreen) viewPick() string {
	var b strings.Builder
	b.WriteString(StyleMuted.Render("Pick parent location — j/k move · / filter · enter select · esc back") + "\n\n")
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
		if i == s.pickCursor {
			b.WriteString(StyleSidebarItemActive.Render(caret+opt.label) + "\n")
		} else if opt.clear {
			b.WriteString(caret + StyleMuted.Render(opt.label) + "\n")
		} else {
			b.WriteString(caret + opt.label + "\n")
		}
	}
	if end < len(s.pickOptions) {
		b.WriteString(StyleMuted.Render(fmt.Sprintf("  ↓ %d more below", len(s.pickOptions)-end)) + "\n")
	}
	return b.String()
}

// ===========================================================================
// LocationListScreen
// ===========================================================================

type LocationListScreen struct {
	deps           Deps
	rows           []omsapi.Location
	cursor         int
	windowStart    int
	windowSize     int
	loading        bool
	loadErr        string
	terminalHeight int

	confirmingDelete bool
	deleting         bool
}

type locationListLoadedMsg struct {
	rows []omsapi.Location
	err  error
}

type locationDeletedMsg struct {
	err error
}

func NewLocationListScreen(deps Deps) *LocationListScreen {
	return &LocationListScreen{deps: deps, loading: true, windowSize: 20}
}

func (s *LocationListScreen) Title() string { return "Locations" }

func (s *LocationListScreen) WantsRawInput() bool { return s.confirmingDelete }

func (s *LocationListScreen) Init() tea.Cmd {
	deps := s.deps
	ctx := deps.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return func() tea.Msg {
		page, err := deps.OMS.ListLocations(ctx, nil)
		if err != nil {
			return locationListLoadedMsg{err: err}
		}
		return locationListLoadedMsg{rows: page.Results}
	}
}

func (s *LocationListScreen) computeWindowSize() int {
	const chrome = 4
	avail := screenBodyHeight(s.terminalHeight) - chrome
	if avail < 3 {
		avail = 3
	}
	return avail
}

func (s *LocationListScreen) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		s.terminalHeight = m.Height
		s.windowSize = s.computeWindowSize()
		s.scrollIntoView()
		return s, nil
	case locationListLoadedMsg:
		s.loading = false
		if m.err != nil {
			s.loadErr = m.err.Error()
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
	case locationDeletedMsg:
		s.deleting = false
		s.confirmingDelete = false
		if m.err != nil {
			return s, Status("delete failed: "+m.err.Error(), StatusError)
		}
		s.loading = true
		return s, tea.Batch(Status("location deleted", StatusOK), s.Init())
	case tea.KeyMsg:
		if s.confirmingDelete {
			return s.updateConfirmDelete(m)
		}
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
		case "ctrl+d", "pgdown":
			s.cursor += s.windowSize
			if s.cursor >= len(s.rows) {
				s.cursor = len(s.rows) - 1
			}
			s.scrollIntoView()
		case "ctrl+u", "pgup":
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
		case "c":
			return s, SwitchTo(WSInventory, NewLocationFormScreen(s.deps, ""))
		case "enter":
			if row, ok := s.selected(); ok {
				return s, SwitchTo(WSInventory, NewLocationDetailScreen(s.deps, strconv.Itoa(row.ID)))
			}
		case "E":
			if row, ok := s.selected(); ok {
				return s, SwitchTo(WSInventory, NewLocationFormScreen(s.deps, strconv.Itoa(row.ID)))
			}
		case "x":
			if _, ok := s.selected(); ok {
				s.confirmingDelete = true
			}
		}
	}
	return s, nil
}

func (s *LocationListScreen) updateConfirmDelete(m tea.KeyMsg) (Screen, tea.Cmd) {
	if s.deleting {
		return s, nil
	}
	switch m.String() {
	case "y", "Y":
		row, ok := s.selected()
		if !ok {
			s.confirmingDelete = false
			return s, nil
		}
		s.deleting = true
		deps := s.deps
		ctx := deps.Ctx
		if ctx == nil {
			ctx = context.Background()
		}
		id := strconv.Itoa(row.ID)
		return s, func() tea.Msg {
			return locationDeletedMsg{err: deps.OMS.DeleteLocation(ctx, id)}
		}
	case "n", "N", "esc":
		s.confirmingDelete = false
	}
	return s, nil
}

func (s *LocationListScreen) selected() (omsapi.Location, bool) {
	if s.cursor < 0 || s.cursor >= len(s.rows) {
		return omsapi.Location{}, false
	}
	return s.rows[s.cursor], true
}

func (s *LocationListScreen) scrollIntoView() {
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

func (s *LocationListScreen) View() string {
	if s.loading {
		return StyleMuted.Render("Loading locations…")
	}
	if s.loadErr != "" {
		return StyleStatusError.Render("Error: ") + s.loadErr + "\n\n" + StyleMuted.Render("r retry · c new · esc back")
	}
	if s.confirmingDelete {
		name := ""
		if row, ok := s.selected(); ok {
			name = row.Name
		}
		var prompt string
		if s.deleting {
			prompt = StyleMuted.Render("Deleting…")
		} else {
			prompt = StyleStatusWarn.Render(fmt.Sprintf("Delete location %q? This can't be undone.  y delete · n/esc cancel", name))
		}
		return prompt
	}
	if len(s.rows) == 0 {
		return StyleMuted.Render("No locations.") + "\n\n" + StyleMuted.Render("c new location · esc back")
	}

	var b strings.Builder
	b.WriteString(StyleMuted.Render(fmt.Sprintf("%d locations", len(s.rows))) + "\n")
	if s.windowStart > 0 {
		b.WriteString(StyleMuted.Render("  ↑ more above") + "\n")
	}
	end := s.windowStart + s.windowSize
	if end > len(s.rows) {
		end = len(s.rows)
	}
	for i := s.windowStart; i < end; i++ {
		b.WriteString(s.renderRow(i) + "\n")
	}
	if end < len(s.rows) {
		b.WriteString(StyleMuted.Render(fmt.Sprintf("  ↓ %d more below", len(s.rows)-end)) + "\n")
	}
	b.WriteString("\n")
	b.WriteString(StyleMuted.Render("j/k move · enter open · c new · E edit · x delete · r refresh · esc back"))
	return b.String()
}

func (s *LocationListScreen) renderRow(i int) string {
	l := s.rows[i]
	marker := "  "
	if i == s.cursor {
		marker = "▸ "
	}
	meta := []string{}
	if l.ParentName != "" {
		meta = append(meta, "in "+l.ParentName)
	}
	if l.FixtureCount > 0 {
		meta = append(meta, fmt.Sprintf("%d fixtures", l.FixtureCount))
	}
	if !l.IsActive {
		meta = append(meta, "inactive")
	}
	line := marker + l.Name
	if len(meta) > 0 {
		line += " " + StyleMuted.Render("("+strings.Join(meta, " · ")+")")
	}
	if i == s.cursor {
		line = StyleSidebarItemActive.Render(line)
	}
	return line
}
