package tui

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/uid0/scantty/internal/omsapi"
)

// LocationCheckinsScreen lists recent check-ins and lets a logged-in
// user log a new one against a location id. The "checkin" backend
// action is AllowAny — anonymous + volunteer + contractor — so the
// log form works regardless of whether scantty has a JWT loaded; the
// backend coerces the `checkin_type` based on the request auth.
//
// The log form is a small terminal-JDE prompt flow: type a location
// id → the id is RESOLVED to its name (GetLocation) and shown for
// confirmation → enter checks in. An operator who doesn't know the id
// opens a searchable lookup (l / ?) that server-side-filters locations
// by name; picking a row fills the id and jumps straight to confirm.
// This stops a check-in ever landing against an unconfirmed/invalid id.
//
// Scanner integration is a v2 follow-up: when a location code is
// scanned, the scan workspace currently routes to the location
// detail screen — adding "check in here" as a hotkey from there is
// the natural next step but a bigger lift than this v1 needs.
type LocationCheckinsScreen struct {
	deps    Deps
	rows    []omsapi.LocationCheckIn
	cursor  int
	loading bool
	loadErr string

	// Log-a-check-in sub-flow. logging gates the whole form (and drives
	// WantsRawInput); step selects which panel of the form is showing.
	logging    bool
	step       checkinStep
	locInput   textinput.Model
	notesInput textinput.Model
	focusNotes bool
	submitErr  string

	// resolving is true while a GetLocation(id) confirm lookup is in flight;
	// resolveID tags it so a stale reply for an id the operator has since
	// changed (or one landing on a reopened screen) is dropped. resolved holds
	// the confirmed location shown on the confirm step.
	resolving  bool
	resolveID  int
	resolved   *omsapi.Location
	submitting bool

	// Lookup picker (step == checkinLookup). pickSeq guards the server-side
	// search the same way list.go's search does: only the freshest response is
	// applied, so a slow reply can't clobber newer results.
	pickInput   textinput.Model
	pickRows    []omsapi.Location
	pickCursor  int
	pickSeq     int
	pickPending bool
	pickErr     string
}

// checkinStep is the panel of the log form currently showing.
type checkinStep int

const (
	checkinEntry   checkinStep = iota // type a location id (+ notes)
	checkinConfirm                    // resolved name shown; enter to check in
	checkinLookup                     // searchable location picker
)

// checkinPickWindow caps how many lookup rows render at once so a large
// location set can't overflow the panel; the window follows the cursor.
const checkinPickWindow = 12

type locationCheckinsLoadedMsg struct {
	rows []omsapi.LocationCheckIn
	err  error
}

type locationCheckinSubmittedMsg struct {
	row *omsapi.LocationCheckIn
	err error
}

// locationResolvedMsg carries a GetLocation confirm lookup tagged with the id
// it was issued for, so the handler can drop a stale reply.
type locationResolvedMsg struct {
	id  int
	loc *omsapi.Location
	err error
}

// locationLookupMsg carries a picker search result tagged with its seq.
type locationLookupMsg struct {
	seq  int
	rows []omsapi.Location
	err  error
}

func NewLocationCheckinsScreen(deps Deps) *LocationCheckinsScreen {
	return &LocationCheckinsScreen{deps: deps, loading: true}
}

func (s *LocationCheckinsScreen) Title() string { return "Location check-ins" }

func (s *LocationCheckinsScreen) WantsRawInput() bool { return s.logging }

// HandlesKey claims the list-view shortcuts so they beat the colliding global
// nav keys — most importantly 'n' (start a new check-in) over the global
// n=notifications. While the log form is open WantsRawInput already routes
// every key here, so the claim only matters in the list view.
func (s *LocationCheckinsScreen) HandlesKey(key string) bool {
	switch key {
	case "n", "r", "j", "k":
		return true
	}
	return false
}

func (s *LocationCheckinsScreen) Init() tea.Cmd { return s.load() }

func (s *LocationCheckinsScreen) ctx() context.Context {
	if s.deps.Ctx != nil {
		return s.deps.Ctx
	}
	return context.Background()
}

func (s *LocationCheckinsScreen) load() tea.Cmd {
	deps := s.deps
	ctx := s.ctx()
	return func() tea.Msg {
		q := url.Values{"ordering": []string{"-checked_in_at"}}
		page, err := deps.OMS.ListLocationCheckIns(ctx, q)
		if err != nil {
			return locationCheckinsLoadedMsg{err: err}
		}
		return locationCheckinsLoadedMsg{rows: page.Results}
	}
}

func (s *LocationCheckinsScreen) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch m := msg.(type) {
	case locationCheckinsLoadedMsg:
		s.loading = false
		if m.err != nil {
			s.loadErr = m.err.Error()
			return s, nil
		}
		s.loadErr = ""
		s.rows = m.rows
		if s.cursor >= len(s.rows) {
			s.cursor = 0
		}
		return s, nil
	case locationResolvedMsg:
		// Drop a reply we're no longer waiting on: the operator changed the id
		// (resolveID moved), opened the lookup (resolving cleared), or this is a
		// reopened screen. Otherwise a stale/late confirm could yank us onto a
		// wrong location.
		if !s.resolving || m.id != s.resolveID {
			return s, nil
		}
		s.resolving = false
		// The confirm must reflect what's on screen: if the operator kept typing
		// while GetLocation was in flight (so the field no longer parses to the
		// id we resolved), drop the reply and let them re-submit the new value.
		if cur, err := strconv.Atoi(strings.TrimSpace(s.locInput.Value())); err != nil || cur != m.id {
			return s, nil
		}
		if m.err != nil {
			var apiErr *omsapi.APIError
			if errors.As(m.err, &apiErr) && apiErr.IsNotFound() {
				s.submitErr = fmt.Sprintf("no location with ID %d", m.id)
			} else {
				s.submitErr = m.err.Error()
			}
			return s, nil
		}
		s.resolved = m.loc
		s.step = checkinConfirm
		s.submitErr = ""
		return s, nil
	case locationLookupMsg:
		if m.seq != s.pickSeq {
			return s, nil // a fresher query has been typed; drop this response
		}
		s.pickPending = false
		if m.err != nil {
			s.pickErr = m.err.Error()
			s.pickRows = nil
		} else {
			s.pickErr = ""
			s.pickRows = m.rows
		}
		if s.pickCursor >= len(s.pickRows) {
			s.pickCursor = 0
		}
		return s, nil
	case locationCheckinSubmittedMsg:
		s.submitting = false
		s.logging = false
		s.step = checkinEntry
		s.resolved = nil
		if m.err != nil {
			s.submitErr = m.err.Error()
			return s, Status("check-in failed: "+s.submitErr, StatusError)
		}
		s.submitErr = ""
		s.locInput.SetValue("")
		s.notesInput.SetValue("")
		// Checkin returns a non-nil row on a nil error, but guard anyway: a
		// nil-deref here would crash the whole TUI.
		label := "location"
		if m.row != nil {
			label = fmt.Sprintf("location #%d", m.row.Location)
			if m.row.LocationName != "" {
				label = m.row.LocationName
			}
		}
		return s, tea.Batch(
			Status("checked in to "+label, StatusOK),
			s.load(),
		)
	case tea.KeyMsg:
		if s.logging {
			switch s.step {
			case checkinLookup:
				return s.updateLookup(m)
			case checkinConfirm:
				return s.updateConfirm(m)
			default:
				return s.updateEntry(m)
			}
		}
		switch m.String() {
		case "j", "down":
			if s.cursor < len(s.rows)-1 {
				s.cursor++
			}
		case "k", "up":
			if s.cursor > 0 {
				s.cursor--
			}
		case "r":
			s.loading = true
			return s, s.load()
		case "n":
			s.openEntry()
			return s, textinput.Blink
		}
	}
	return s, nil
}

// openEntry starts a fresh log-a-check-in flow on the id-entry step.
func (s *LocationCheckinsScreen) openEntry() {
	loc := textinput.New()
	loc.Prompt = ""
	loc.Placeholder = "location id (integer)"
	loc.CharLimit = 12
	loc.Focus()
	notes := textinput.New()
	notes.Prompt = ""
	notes.Placeholder = "notes (optional)"
	notes.CharLimit = 500
	s.locInput = loc
	s.notesInput = notes
	s.logging = true
	s.step = checkinEntry
	s.focusNotes = false
	s.submitErr = ""
	s.resolving = false
	s.resolved = nil
	s.submitting = false
}

// updateEntry owns the id-entry step: enter resolves the id to its location
// name (GetLocation) rather than checking in blind; l/? open the searchable
// lookup when the numeric id field is focused (where those letters can never be
// valid input — notes keeps them as literal text); tab toggles id/notes.
func (s *LocationCheckinsScreen) updateEntry(m tea.KeyMsg) (Screen, tea.Cmd) {
	switch m.Type {
	case tea.KeyEsc:
		s.logging = false
		return s, nil
	case tea.KeyTab, tea.KeyShiftTab:
		s.focusNotes = !s.focusNotes
		if s.focusNotes {
			s.locInput.Blur()
			s.notesInput.Focus()
		} else {
			s.notesInput.Blur()
			s.locInput.Focus()
		}
		return s, nil
	case tea.KeyEnter:
		return s.resolve()
	}
	if !s.focusNotes {
		switch m.String() {
		case "l", "L", "?":
			return s.openLookup()
		}
	}
	var cmd tea.Cmd
	if s.focusNotes {
		s.notesInput, cmd = s.notesInput.Update(m)
	} else {
		s.locInput, cmd = s.locInput.Update(m)
	}
	return s, cmd
}

// resolve validates the typed id and fires GetLocation to confirm its name
// before any check-in happens. An invalid id is a local error (no request); a
// 404 comes back as a friendly "no location with that ID" on the reply.
func (s *LocationCheckinsScreen) resolve() (Screen, tea.Cmd) {
	raw := strings.TrimSpace(s.locInput.Value())
	if raw == "" {
		s.submitErr = "location id required"
		return s, nil
	}
	id, err := strconv.Atoi(raw)
	if err != nil || id <= 0 {
		s.submitErr = "location id must be a positive integer"
		return s, nil
	}
	s.submitErr = ""
	s.resolving = true
	s.resolveID = id
	deps := s.deps
	ctx := s.ctx()
	return s, func() tea.Msg {
		loc, err := deps.OMS.GetLocation(ctx, strconv.Itoa(id))
		return locationResolvedMsg{id: id, loc: loc, err: err}
	}
}

// updateConfirm owns the confirm step: the resolved name is on screen, enter
// commits the check-in, esc returns to the id field to edit.
func (s *LocationCheckinsScreen) updateConfirm(m tea.KeyMsg) (Screen, tea.Cmd) {
	switch m.Type {
	case tea.KeyEsc:
		s.step = checkinEntry
		s.submitErr = ""
		if s.focusNotes {
			s.notesInput.Focus()
		} else {
			s.locInput.Focus()
		}
		return s, textinput.Blink
	case tea.KeyEnter:
		if s.submitting {
			return s, nil
		}
		return s.submit()
	}
	return s, nil
}

// openLookup switches to the searchable location picker and kicks off the
// initial (unfiltered) search.
func (s *LocationCheckinsScreen) openLookup() (Screen, tea.Cmd) {
	s.step = checkinLookup
	s.resolving = false // any in-flight confirm is abandoned; drop its reply
	in := textinput.New()
	in.Prompt = "search ▸ "
	in.Placeholder = "name / description…"
	in.CharLimit = 120
	in.Focus()
	s.pickInput = in
	s.pickRows = nil
	s.pickCursor = 0
	s.pickErr = ""
	s.pickPending = true
	return s, tea.Batch(textinput.Blink, s.runLookup())
}

// updateLookup owns the picker step: the arrows move the cursor, enter
// selects the highlighted row (filling the id and jumping to confirm), esc
// returns to the id field; every other key edits the query and fires a fresh
// server-side search.
func (s *LocationCheckinsScreen) updateLookup(m tea.KeyMsg) (Screen, tea.Cmd) {
	switch m.Type {
	case tea.KeyEsc:
		s.step = checkinEntry
		if s.focusNotes {
			s.notesInput.Focus()
		} else {
			s.locInput.Focus()
		}
		return s, textinput.Blink
	case tea.KeyUp:
		if s.pickCursor > 0 {
			s.pickCursor--
		}
		return s, nil
	case tea.KeyDown:
		if s.pickCursor < len(s.pickRows)-1 {
			s.pickCursor++
		}
		return s, nil
	case tea.KeyEnter:
		return s.selectLookup()
	}
	prev := s.pickInput.Value()
	var cmd tea.Cmd
	s.pickInput, cmd = s.pickInput.Update(m)
	if s.pickInput.Value() != prev {
		s.pickPending = true
		return s, tea.Batch(cmd, s.runLookup())
	}
	return s, cmd
}

// runLookup forwards the current query to ListLocations as ?search=, which the
// backend LocationViewSet matches (icontains) against name / description. The
// bumped seq is stamped on the reply so a stale response is dropped on arrival.
func (s *LocationCheckinsScreen) runLookup() tea.Cmd {
	s.pickSeq++
	seq := s.pickSeq
	query := strings.TrimSpace(s.pickInput.Value())
	deps := s.deps
	ctx := s.ctx()
	return func() tea.Msg {
		q := url.Values{}
		if query != "" {
			q.Set("search", query)
		}
		page, err := deps.OMS.ListLocations(ctx, q)
		if err != nil {
			return locationLookupMsg{seq: seq, err: err}
		}
		return locationLookupMsg{seq: seq, rows: page.Results}
	}
}

// selectLookup fills the id field from the highlighted row and jumps straight
// to confirm — the name is already known from the picker, so no extra
// round-trip is needed.
func (s *LocationCheckinsScreen) selectLookup() (Screen, tea.Cmd) {
	if s.pickCursor < 0 || s.pickCursor >= len(s.pickRows) {
		return s, nil
	}
	loc := s.pickRows[s.pickCursor]
	s.locInput.SetValue(strconv.Itoa(loc.ID))
	picked := loc
	s.resolved = &picked
	s.step = checkinConfirm
	s.submitErr = ""
	return s, nil
}

// submit posts the confirmed check-in. It is only reachable from the confirm
// step, so resolved is set; the id comes off the resolved location rather than
// re-parsing the field.
func (s *LocationCheckinsScreen) submit() (Screen, tea.Cmd) {
	if s.resolved == nil {
		s.step = checkinEntry
		return s, nil
	}
	id := s.resolved.ID
	notes := strings.TrimSpace(s.notesInput.Value())
	s.submitErr = ""
	s.submitting = true
	deps := s.deps
	ctx := s.ctx()
	return s, func() tea.Msg {
		row, err := deps.OMS.Checkin(ctx, id, "", notes)
		return locationCheckinSubmittedMsg{row: row, err: err}
	}
}

func (s *LocationCheckinsScreen) View() string {
	if s.logging {
		switch s.step {
		case checkinLookup:
			return s.viewLookup()
		case checkinConfirm:
			return s.viewConfirm()
		default:
			return s.viewEntry()
		}
	}
	if s.loading {
		return StyleMuted.Render("Loading recent check-ins…")
	}
	if s.loadErr != "" {
		return StyleStatusError.Render("Error: ") + s.loadErr + "\n\n" + StyleMuted.Render("r retry · n new check-in · esc back")
	}

	var b strings.Builder
	if len(s.rows) == 0 {
		b.WriteString(StyleMuted.Render("No recent check-ins.") + "\n\n")
	} else {
		for i, r := range s.rows {
			caret := "  "
			if i == s.cursor {
				caret = "▸ "
			}
			loc := r.LocationName
			if loc == "" {
				loc = fmt.Sprintf("location #%d", r.Location)
			}
			ts := r.CheckedInAt.Format("01-02 15:04")
			who := "anonymous"
			if r.UserUsername != nil && *r.UserUsername != "" {
				who = *r.UserUsername
			}
			typeBadge := r.CheckinType
			switch r.CheckinType {
			case "volunteer":
				typeBadge = StyleStatusOK.Render(r.CheckinType)
			case "contractor":
				typeBadge = StyleStatusWarn.Render(r.CheckinType)
			}
			line := fmt.Sprintf("%s%s · %s · %s · %s", caret, ts, loc, who, typeBadge)
			if i == s.cursor {
				line = StyleSidebarItemActive.Render(line)
			}
			b.WriteString(line + "\n")
			if r.Notes != "" {
				note := r.Notes
				if len(note) > 80 {
					note = note[:77] + "…"
				}
				b.WriteString("    " + StyleMuted.Render(note) + "\n")
			}
		}
		b.WriteString("\n")
	}
	b.WriteString(StyleMuted.Render("j/k move · n new check-in · r refresh · esc back"))
	return b.String()
}

func (s *LocationCheckinsScreen) viewEntry() string {
	var b strings.Builder
	b.WriteString(StyleTitle.Render("Check in to a location") + "\n\n")
	locLine := StyleMuted.Render("Location ID: ") + s.locInput.View()
	if s.resolving {
		locLine += "  " + StyleMuted.Render("resolving…")
	}
	b.WriteString(locLine + "\n")
	b.WriteString(StyleMuted.Render("Notes:       ") + s.notesInput.View() + "\n")
	if s.submitErr != "" {
		b.WriteString("\n" + StyleStatusError.Render(s.submitErr) + "\n")
	}
	hint := "enter confirm · tab notes · esc cancel"
	if !s.focusNotes {
		hint = "enter confirm · l look up · tab notes · esc cancel"
	}
	b.WriteString("\n" + StyleMuted.Render(hint))
	return b.String()
}

func (s *LocationCheckinsScreen) viewConfirm() string {
	var b strings.Builder
	b.WriteString(StyleTitle.Render("Confirm check-in") + "\n\n")
	name := "(unnamed)"
	id := 0
	code := ""
	if s.resolved != nil {
		if s.resolved.Name != "" {
			name = s.resolved.Name
		}
		id = s.resolved.ID
		code = locationCode(s.resolved)
	}
	b.WriteString(StyleMuted.Render("Location: ") + name + StyleMuted.Render(fmt.Sprintf("  #%d", id)) + "\n")
	if code != "" {
		b.WriteString(StyleMuted.Render("Code:     ") + code + "\n")
	}
	notes := strings.TrimSpace(s.notesInput.Value())
	if notes == "" {
		notes = StyleMuted.Render("(none)")
	}
	b.WriteString(StyleMuted.Render("Notes:    ") + notes + "\n")
	if s.submitting {
		b.WriteString("\n" + StyleMuted.Render("checking in…") + "\n")
	}
	if s.submitErr != "" {
		b.WriteString("\n" + StyleStatusError.Render(s.submitErr) + "\n")
	}
	b.WriteString("\n" + StyleMuted.Render("enter check in · esc edit id"))
	return b.String()
}

func (s *LocationCheckinsScreen) viewLookup() string {
	var b strings.Builder
	b.WriteString(StyleTitle.Render("Look up a location") + "\n\n")
	head := s.pickInput.View()
	if s.pickPending {
		head += "  " + StyleMuted.Render("searching…")
	}
	b.WriteString(head + "\n\n")

	switch {
	case s.pickErr != "":
		b.WriteString(StyleStatusError.Render("Error: ") + s.pickErr + "\n")
	case len(s.pickRows) == 0 && !s.pickPending:
		b.WriteString(StyleMuted.Render("(no matching locations)") + "\n")
	default:
		start := 0
		if s.pickCursor >= checkinPickWindow {
			start = s.pickCursor - checkinPickWindow + 1
		}
		end := start + checkinPickWindow
		if end > len(s.pickRows) {
			end = len(s.pickRows)
		}
		for i := start; i < end; i++ {
			loc := s.pickRows[i]
			caret := "  "
			if i == s.pickCursor {
				caret = "▸ "
			}
			label := loc.Name
			if label == "" {
				label = "(unnamed)"
			}
			line := fmt.Sprintf("%s%s  %s", caret, label, StyleMuted.Render(fmt.Sprintf("#%d", loc.ID)))
			if code := locationCode(&loc); code != "" {
				line += StyleMuted.Render(" · " + code)
			}
			if i == s.pickCursor {
				line = StyleSidebarItemActive.Render(line)
			}
			b.WriteString(line + "\n")
		}
		if len(s.pickRows) > end-start {
			b.WriteString(StyleMuted.Render(fmt.Sprintf("  … %d more", len(s.pickRows)-(end-start))) + "\n")
		}
	}

	b.WriteString("\n" + StyleMuted.Render("type to search · ↑/↓ move · enter select · esc back"))
	return b.String()
}

// locationCode surfaces the operator-meaningful code for a location: the real
// check-in access_code when set, falling back to the (usually empty) code
// column the item/asset pickers label with.
func locationCode(loc *omsapi.Location) string {
	if loc == nil {
		return ""
	}
	if loc.AccessCode != "" {
		return loc.AccessCode
	}
	return loc.Code
}
