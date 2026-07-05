package tui

import (
	"context"
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

	logging    bool
	locInput   textinput.Model
	notesInput textinput.Model
	focusNotes bool
	submitErr  string
}

type locationCheckinsLoadedMsg struct {
	rows []omsapi.LocationCheckIn
	err  error
}

type locationCheckinSubmittedMsg struct {
	row *omsapi.LocationCheckIn
	err error
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

func (s *LocationCheckinsScreen) load() tea.Cmd {
	deps := s.deps
	ctx := deps.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
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
	case locationCheckinSubmittedMsg:
		s.logging = false
		if m.err != nil {
			s.submitErr = m.err.Error()
			return s, Status("check-in failed: "+s.submitErr, StatusError)
		}
		s.submitErr = ""
		s.locInput.SetValue("")
		s.notesInput.SetValue("")
		label := fmt.Sprintf("location #%d", m.row.Location)
		if m.row.LocationName != "" {
			label = m.row.LocationName
		}
		return s, tea.Batch(
			Status("checked in to "+label, StatusOK),
			s.load(),
		)
	case tea.KeyMsg:
		if s.logging {
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
				return s.submit()
			}
			var cmd tea.Cmd
			if s.focusNotes {
				s.notesInput, cmd = s.notesInput.Update(msg)
			} else {
				s.locInput, cmd = s.locInput.Update(msg)
			}
			return s, cmd
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
			s.focusNotes = false
			s.submitErr = ""
			return s, textinput.Blink
		}
	}
	return s, nil
}

func (s *LocationCheckinsScreen) submit() (Screen, tea.Cmd) {
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
	deps := s.deps
	ctx := deps.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	notes := strings.TrimSpace(s.notesInput.Value())
	s.submitErr = ""
	return s, func() tea.Msg {
		row, err := deps.OMS.Checkin(ctx, id, "", notes)
		return locationCheckinSubmittedMsg{row: row, err: err}
	}
}

func (s *LocationCheckinsScreen) View() string {
	if s.logging {
		var b strings.Builder
		b.WriteString(StyleTitle.Render("Check in to a location") + "\n\n")
		b.WriteString(StyleMuted.Render("Location ID: ") + s.locInput.View() + "\n")
		b.WriteString(StyleMuted.Render("Notes:       ") + s.notesInput.View() + "\n")
		if s.submitErr != "" {
			b.WriteString("\n" + StyleStatusError.Render(s.submitErr) + "\n")
		}
		b.WriteString("\n" + StyleMuted.Render("tab next field · enter submit · esc cancel"))
		return b.String()
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
