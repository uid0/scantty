package tui

import (
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/uid0/scantty/internal/omsapi"
)

// ServiceStatusScreen lists every external capability the backend knows about,
// what state it is in, and how long a degraded one has been that way.
//
// The web has no equivalent page — a browser can afford a banner that names
// every affected service at once, and a terminal status bar cannot. So the bar
// carries a chip ("! 2 services degraded") and this screen carries the detail
// the chip had to drop.
//
// It is a pure VIEW over Deps.Health: Root owns the snapshot and applies every
// poll reply, so this screen keeps no copy that could go stale behind it. That
// is also why Refresh does not show a spinner — the screen never sees the reply
// (Root intercepts it), and "Checked HH:MM:SS" moving is the honest signal that
// one landed.
//
// Built on the columnar layer (jde_form.go): labels right-aligned into one
// column, a dotted leader, and a persistent action bar naming exactly the keys
// that work here. No letter accelerators — enter, esc and the arrows are the
// whole keymap, which is also why the screen needs no HandlesKey claim: none of
// them collide with a global.
type ServiceStatusScreen struct {
	jdeScreen
	deps   Deps
	cursor int
	// now is the clock the "degraded for" duration is measured against.
	// Injectable so a test can assert a duration instead of racing one.
	now func() time.Time
}

func NewServiceStatusScreen(deps Deps) *ServiceStatusScreen {
	return &ServiceStatusScreen{deps: deps}
}

func (s *ServiceStatusScreen) Title() string { return "Service status" }

// Init asks for a fresh snapshot rather than showing whatever the last poll
// left: an operator opening this screen is asking "is it still down?", and the
// background poll may be 59 seconds from answering. Marked manual so it feeds
// the snapshot without forking the poll loop.
func (s *ServiceStatusScreen) Init() tea.Cmd { return RefreshServiceStatus(s.deps) }

func (s *ServiceStatusScreen) clock() time.Time {
	if s.now != nil {
		return s.now()
	}
	return time.Now()
}

func (s *ServiceStatusScreen) services() []omsapi.ServiceStatus {
	snapshot := s.deps.Health.Snapshot()
	if snapshot == nil {
		return nil
	}
	return snapshot.Services
}

func (s *ServiceStatusScreen) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		s.setSize(m)
		return s, nil
	case tea.KeyMsg:
		count := len(s.services())
		switch m.String() {
		case "enter":
			return s, RefreshServiceStatus(s.deps)
		case "down":
			if count > 0 && s.cursor < count-1 {
				s.cursor++
			}
			return s, nil
		case "up":
			if s.cursor > 0 {
				s.cursor--
			}
			return s, nil
		case "pgdown":
			s.cursor = jdePageCursor(s.cursor, count, s.pageStep(), +1)
			return s, nil
		case "pgup":
			s.cursor = jdePageCursor(s.cursor, count, s.pageStep(), -1)
			return s, nil
		}
	}
	return s, nil
}

// pageStep is how far a page moves: the rows the pane is actually showing, so
// paging covers exactly what the operator can see.
func (s *ServiceStatusScreen) pageStep() int {
	_, body := s.render()
	return s.windowRows(body, s.cursor, serviceStatusHeaderRows)
}

// serviceStatusHeaderRows is what the pinned header costs the body: the summary
// line and the blank under it, drawn on every frame so the count and the
// checked-at time cannot scroll away under a long list.
const serviceStatusHeaderRows = 2

func (s *ServiceStatusScreen) View() string {
	header, body := s.render()
	items := []actionBarItem{{"Enter", "Refresh"}, {"Esc", "Back"}}
	if count := len(s.services()); count > 1 {
		items = append(items, actionBarItem{"UP/DN", "Move"})
		// Paging only earns a slot on the bar when there is something off
		// screen to page to — the bar's contract is that every key on it does
		// something here.
		if budget := s.bodyRows(); budget > 0 && body.Len() > budget-serviceStatusHeaderRows {
			items = append(items, actionBarItem{"PgUp/PgDn", "Page"})
		}
	}
	return s.frameWithHeader(header, body, s.cursor, "", items)
}

// render builds the pinned header and the scrollable body. Split out of View so
// paging can measure the same lines the operator is looking at.
func (s *ServiceStatusScreen) render() ([]string, *jdeLines) {
	snapshot := s.deps.Health.Snapshot()
	body := &jdeLines{}

	if snapshot == nil || len(snapshot.Services) == 0 {
		// Unknown is not an outage, and this line is deliberately muted rather
		// than an error: the status check being unreachable tells us nothing
		// about the services themselves, and nothing is gated on it.
		header := []string{jdeIndent + StyleMuted.Render("Service status unavailable"), ""}
		body.Add(jdeIndent + "The status check could not be reached, so nothing is known")
		body.Add(jdeIndent + "about the external services right now.")
		body.Add("")
		body.Add(jdeIndent + StyleMuted.Render("Nothing is being gated: an unknown status is treated as healthy,"))
		body.Add(jdeIndent + StyleMuted.Render("so every control stays available."))
		return header, body
	}

	services := snapshot.Services
	degraded := snapshot.DegradedServices()
	summary := fmt.Sprintf("%d services", len(services))
	if len(degraded) == 0 {
		summary += "  ·  " + StyleStatusOK.Render("all working")
	} else {
		summary += "  ·  " + StyleStatusWarn.Render(fmt.Sprintf("%d degraded", len(degraded)))
	}
	if !snapshot.CheckedAt.IsZero() {
		summary += "  ·  " + StyleMuted.Render("checked "+snapshot.CheckedAt.Local().Format("15:04:05"))
	}
	header := []string{jdeIndent + summary, ""}

	now := s.clock()
	// One label column across every service's block, so the leaders line up
	// down the whole screen rather than per service.
	labelWidth := jdeLabelWidth(serviceStatusFields(services[0], now, s.staff()))
	for _, svc := range services {
		labelWidth = maxInt(labelWidth, jdeLabelWidth(serviceStatusFields(svc, now, s.staff())))
	}

	for i, svc := range services {
		name := svc.Label
		if name == "" {
			name = svc.Key
		}
		if i == s.cursor {
			body.AddRow(i, jdeIndent+StyleSidebarItemActive.Render("▸ "+name))
		} else {
			body.AddRow(i, jdeIndent+"  "+StyleJDEHeading.Render(name))
		}
		for _, f := range serviceStatusFields(svc, now, s.staff()) {
			body.AddRow(i, renderJDEField(f, labelWidth, s.bodyWidth()))
		}
		if i < len(services)-1 {
			body.AddRow(i, "")
		}
	}
	return header, body
}

// staff reports whether this console signed in as staff. Only the last_error
// detail is gated on it: the payload is otherwise labels and states, but that
// one field carries whatever the transition recorded — a broker hostname, a
// provider's response — which is why the web calls it staff-only and keeps it
// out of member-facing surfaces.
func (s *ServiceStatusScreen) staff() bool { return s.deps.InitialStaff }

// serviceStatusFields is one service's columnar block.
func serviceStatusFields(svc omsapi.ServiceStatus, now time.Time, staff bool) []jdeField {
	fields := []jdeField{{
		Label: "Status",
		Kind:  jdeValue,
		Value: serviceStateText(svc),
	}}
	if svc.IsDegraded() {
		fields = append(fields, jdeField{
			Label: "Degraded for",
			Kind:  jdeValue,
			Value: serviceDegradedFor(svc.Since, now),
			Dim:   svc.Since == nil,
		})
	}
	fields = append(fields, jdeField{
		Label: "Affects",
		Kind:  jdeValue,
		Value: svc.Description,
		Dim:   svc.Description == "",
	})
	// Counts only mean something for a FAMILY of breakers (the webhook
	// endpoints); every single-breaker service reports 1 of 1, which would read
	// as precision it does not have.
	if svc.TotalCount > 1 {
		fields = append(fields, jdeField{
			Label: "Endpoints",
			Kind:  jdeValue,
			Value: fmt.Sprintf("%d of %d degraded", svc.DegradedCount, svc.TotalCount),
		})
	}
	if staff && svc.LastError != "" {
		fields = append(fields, jdeField{
			Label: "Last error",
			Kind:  jdeValue,
			Value: svc.LastError,
			Dim:   true,
		})
	}
	return fields
}

// serviceStateText is the state, said in words. The wire value rides along in
// parentheses because half_open ("on trial after an outage") is a real
// distinction an operator deciding whether to retry wants to see.
func serviceStateText(svc omsapi.ServiceStatus) string {
	switch svc.State {
	case omsapi.ServiceStateClosed:
		return StyleStatusOK.Render("Working")
	case omsapi.ServiceStateOpen:
		return StyleStatusError.Render("Unavailable") + StyleMuted.Render("  (open)")
	case omsapi.ServiceStateHalfOpen:
		return StyleStatusWarn.Render("Recovering") + StyleMuted.Render("  (half-open — calls may still fail)")
	case "":
		return StyleMuted.Render("Unknown")
	default:
		// A state this build does not know about. Report it verbatim rather
		// than guessing — and note that it gates nothing (IsDegraded is false
		// for it), which is the safe direction.
		return StyleMuted.Render(svc.State + " (unrecognised — not gating)")
	}
}

// serviceDegradedFor renders how long a service has been in its current state.
//
// Two units, largest first ("3d 4h", "1h 30m"), because "how long has this been
// broken" is a question about magnitude — "91m" makes the reader do arithmetic
// the screen could have done. A nil Since is a service that has never
// transitioned: it is degraded now but the backend has no event for it, which is
// a different statement from "0 seconds".
func serviceDegradedFor(since *time.Time, now time.Time) string {
	if since == nil || since.IsZero() {
		return "unknown (no recorded transition)"
	}
	d := now.Sub(*since)
	if d < 0 {
		d = 0
	}
	stamp := "  ·  since " + since.Local().Format("2006-01-02 15:04")
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds())) + stamp
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes())) + stamp
	case d < 24*time.Hour:
		h := int(d.Hours())
		return fmt.Sprintf("%dh %dm", h, int(d.Minutes())-h*60) + stamp
	default:
		days := int(d.Hours()) / 24
		return fmt.Sprintf("%dd %dh", days, int(d.Hours())-days*24) + stamp
	}
}

// serviceStatusHint is the one-line pointer other screens use to say where the
// detail lives. Kept here so the key and the wording move together.
func serviceStatusHint() string {
	return StyleMuted.Render("S service status")
}
