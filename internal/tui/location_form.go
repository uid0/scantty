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

// locationFieldHint carries what the placeholders used to say. A placeholder
// long enough to fill the input area leaves no underscores, so an empty
// green-screen row stops reading as empty; and a columnar form marks what is
// REQUIRED rather than tagging everything else "(optional)".
var locationFieldHint = map[int]string{
	lfName: "required",
}

func locationFieldWidth(id int) int {
	switch id {
	case lfName:
		return 40
	case lfDescription:
		return 44
	}
	return 0
}

// locationLabelWidth is this sheet's label column. The location form is NOT in
// the storage family's shared column (storage_form_helpers.go): it hangs off
// inventory, not the racking, so the two are never seen side by side and
// sharing would only widen one of them.
var locationLabelWidth = jdeLabelWidth(jdeLabelFields(locationFieldLabel))

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

	jdeScreen

	inputs   []textinput.Model
	parentID *int
	isActive bool

	fields []int
	cursor int

	phase       locationFormPhase
	pickCursor  int
	pickSearch  textinput.Model
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
		// Placeholders moved to locationFieldHint — see there.
		if id == lfName {
			ti.CharLimit = 100
		} else {
			ti.CharLimit = 500
		}
		s.inputs[id] = ti
	}
	s.pickSearch = textinput.New()
	s.pickSearch.Prompt = ""
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
		s.setSize(m)
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
	// The system keys, first and everywhere: they mean the same thing on every
	// row, which is the whole point of the reduced scheme (sc-h412).
	switch m.String() {
	case "esc":
		return s, s.cancelCmd()
	case "tab", "down":
		s.moveCursor(+1)
		return s, textinput.Blink
	case "shift+tab", "up":
		s.moveCursor(-1)
		return s, textinput.Blink
	case "pgdown":
		s.pageCursor(+1)
		return s, textinput.Blink
	case "pgup":
		s.pageCursor(-1)
		return s, textinput.Blink
	case "enter":
		if s.saving {
			return s, nil
		}
		return s.submit()
	case "ctrl+e":
		// EDIT opens whatever the highlighted row IS — only the parent row opens
		// anything, which is why the bar drops the key on the others.
		if id, ok := s.currentFieldID(); ok && id == lfParent {
			s.openParentPicker()
			return s, textinput.Blink
		}
		return s, nil
	}

	id, ok := s.currentFieldID()
	if !ok {
		return s, nil
	}
	switch id {
	case lfParent:
		// A picker row has nothing to type into and no accelerators left.
		return s, nil
	case lfIsActive:
		// A toggle is a two-value choice row, so it flips on the same ←/→ every
		// other bounded set takes (space stays as the pilot's synonym).
		switch m.String() {
		case " ", "left", "right":
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
	body := s.formLines()
	next, ok := s.moveRow(s.cursor, len(s.fields), delta, 0, s.formBar(body))
	if !ok {
		return
	}
	s.cursor = next
	s.syncFocus()
}

// pageCursor moves a whole pane's worth of rows, clamping where moveCursor
// wraps — a page is for covering ground, not for losing your place.
func (s *LocationFormScreen) pageCursor(dir int) {
	body := s.formLines()
	next, ok := s.pageRow(body, s.cursor, len(s.fields), dir, 0, s.formBar(body))
	if !ok {
		return
	}
	s.cursor = next
	s.syncFocus()
}

func (s *LocationFormScreen) openParentPicker() {
	s.phase = locationPhaseParentPick
	s.pickSearch.SetValue("")
	// The filter is always live in a columnar picker, so it holds the caret for
	// as long as the picker is open.
	s.pickSearch.Focus()
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

// movePick walks the option cursor, clamping at both ends (a picker list is a
// set of choices, not a ring) and DECLINING on a pane the frame is not drawn
// into — where the highlight it would move is not on screen to be seen.
func (s *LocationFormScreen) movePick(delta int) {
	header, body := s.pickView()
	next, ok := s.pickRow(s.pickCursor, len(s.pickOptions), delta, len(header), s.pickBar(header, body))
	if !ok {
		return
	}
	s.pickCursor = next
}

func (s *LocationFormScreen) updatePickPhase(m tea.KeyMsg) (Screen, tea.Cmd) {
	switch act, delta := jdePickKey(m); act {
	case jdePickCancel:
		s.closePicker()
	case jdePickCommit:
		s.commitParent()
	case jdePickMove:
		s.movePick(delta)
	case jdePickPage:
		header, body := s.pickView()
		s.movePick(delta * s.windowRowsForBar(body, s.pickCursor, len(header), s.pickBar(header, body)))
	default:
		// Anything else is filter text: the box is always live, so there is no
		// mode to enter and no "/" to remember.
		var cmd tea.Cmd
		s.pickSearch, cmd = s.pickSearch.Update(m)
		s.applyParentFilter()
		return s, cmd
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
	s.closePicker()
}

func (s *LocationFormScreen) closePicker() {
	s.phase = locationPhaseForm
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
	body := s.formLines()
	return s.frame(body, s.cursor, s.statusRow(s.saving, "Saving…", s.errMsg), s.formBar(body))
}

// formFields describes the sheet as columnar rows: one FK row Ctrl-E opens, one
// bounded set, and two typed into. The QR image and access code are
// server-managed and read-only, so they live on the detail screen, not here.
func (s *LocationFormScreen) formFields() []jdeField {
	out := make([]jdeField, len(s.fields))
	for i, id := range s.fields {
		f := jdeField{
			Label:   locationFieldLabel[id],
			Width:   locationFieldWidth(id),
			Hint:    locationFieldHint[id],
			Focused: i == s.cursor,
		}
		switch id {
		case lfParent:
			value, dim := s.parentValue()
			f.Kind, f.Value, f.Dim = jdeValue, value, dim
			if f.Focused {
				f.Hint = "Ctrl-E picks"
			}
		case lfIsActive:
			f.Kind, f.Value = jdeChoice, jdeYesNo(s.isActive)
		default:
			f.Kind, f.Input = jdeText, &s.inputs[id]
		}
		out[i] = f
	}
	return out
}

func (s *LocationFormScreen) formLines() *jdeLines {
	l := &jdeLines{}
	l.Add(StyleJDEHeading.Render("Location"))
	l.Add("")
	l.AddFields(s.formFields(), locationLabelWidth, s.bodyWidth(), 0)
	return l
}

// formBar names the keys that work on the form, with PgUp/PgDn on it exactly
// when the body moves under the bar that is about to be drawn.
//
// The paging claim is measured against formBarItems(true) — the bar WITH the
// pair on it — because naming them costs cells, cells fold the bar onto another
// row, and a folded bar leaves the body one row fewer. The tallest bar is the
// fixed point, so the answer cannot oscillate between frames.
func (s *LocationFormScreen) formBar(body *jdeLines) []actionBarItem {
	return s.formBarItems(s.bodyScrollsForBar(body, 0, s.formBarItems(true)))
}

// formBarItems is formBar for a given paging state, so the bar that is
// MEASURED is the bar that is drawn.
//
// It names the keys that apply where the cursor is standing — and only those,
// so the bar never teaches a key that does nothing here.
func (s *LocationFormScreen) formBarItems(paging bool) []actionBarItem {
	items := []actionBarItem{{"Enter", "Save"}, {"Esc", "Cancel"}, {"UP/DN", "Fields"}}
	if id, ok := s.currentFieldID(); ok {
		switch id {
		case lfIsActive:
			items = append(items, actionBarItem{"←→", "Change"})
		case lfParent:
			items = append(items, actionBarItem{"Ctrl-E", "Pick"})
		}
	}
	if paging {
		items = append(items, actionBarItem{"PgUp/PgDn", "Page"})
	}
	return items
}

// parentValue is the parent row's text, and whether it is an empty state rather
// than a value. PLAIN text plus a flag, not pre-styled muted text: a focused row
// has to be able to reverse-video the whole field.
func (s *LocationFormScreen) parentValue() (string, bool) {
	if s.parentID == nil {
		return "(none — top level)", true
	}
	for _, l := range s.locations {
		if l.ID == *s.parentID {
			return l.Name, false
		}
	}
	return fmt.Sprintf("#%d", *s.parentID), false
}

func (s *LocationFormScreen) pickView() (jdeHeader, *jdeLines) {
	return jdePickList{
		Title:  "Parent location",
		For:    strings.TrimSpace(s.inputs[lfName].Value()),
		Note:   "Row 1 is none — the location then sits at the top level.",
		Filter: s.pickSearch,
		Count:  len(s.pickOptions),
		Label:  func(i int) string { return s.pickOptions[i].label },
		Dim:    func(i int) bool { return s.pickOptions[i].clear },
		Cursor: s.pickCursor,
		Empty:  "(no matching locations)",
	}.render(s.bodyWidth())
}

// pickBar is the picker's bar, with PgUp/PgDn on it exactly when the option
// list moves under the bar about to be drawn — measured against the bar WITH
// the pair on it, because the tallest bar is the fixed point.
func (s *LocationFormScreen) pickBar(header jdeHeader, body *jdeLines) []actionBarItem {
	return jdePickBar("Select", s.bodyScrollsForBar(body, len(header), jdePickBar("Select", true)))
}

func (s *LocationFormScreen) viewPick() string {
	header, body := s.pickView()
	return s.frameWithHeader(header, body, s.pickCursor,
		s.statusRow(false, "", ""), s.pickBar(header, body))
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
