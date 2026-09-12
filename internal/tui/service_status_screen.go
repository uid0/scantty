package tui

import (
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

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
		switch m.String() {
		case "enter":
			return s, RefreshServiceStatus(s.deps)
		case "down":
			s.move(+1)
			return s, nil
		case "up":
			s.move(-1)
			return s, nil
		case "pgdown":
			s.page(+1)
			return s, nil
		case "pgup":
			s.page(-1)
			return s, nil
		}
	}
	return s, nil
}

// move and page walk the service cursor. It is a LIST cursor, so it clamps
// rather than wrapping, and both DECLINE on a pane the frame is not drawn
// into — nothing on the pane would change, and growing the terminal back would
// find the highlight somewhere the operator never put it.
func (s *ServiceStatusScreen) move(delta int) {
	header, body := s.render()
	next, ok := s.pickRow(s.cursor, len(s.services()), delta, serviceStatusHeaderRows, s.bar(header, body))
	if !ok {
		return
	}
	s.cursor = next
}

func (s *ServiceStatusScreen) page(dir int) {
	header, body := s.render()
	next, ok := s.pageRow(body, s.cursor, len(s.services()), dir, serviceStatusHeaderRows,
		s.bar(header, body), s.barItems(true))
	if !ok {
		return
	}
	s.cursor = next
}

// bar names the keys that work here, with PgUp/PgDn on it exactly when the list
// moves under the bar about to be drawn — measured against the bar WITH the
// pair on it, because the tallest bar is the fixed point.
func (s *ServiceStatusScreen) bar(header jdeHeader, body *jdeLines) []actionBarItem {
	return s.barItems(s.bodyScrollsForBar(body, serviceStatusHeaderRows, s.barItems(true)))
}

// barItems is bar for a given paging state, so the bar that is MEASURED against
// the pane is the bar that is drawn on it.
func (s *ServiceStatusScreen) barItems(paging bool) []actionBarItem {
	items := []actionBarItem{{"Enter", "Refresh"}, {"Esc", "Back"}}
	if len(s.services()) > 1 {
		items = append(items, actionBarItem{"UP/DN", "Move"})
		// Paging only earns a slot on the bar when there is something off
		// screen to page to — the bar's contract is that every key on it does
		// something here.
		if paging {
			items = append(items, actionBarItem{"PgUp/PgDn", "Page"})
		}
	}
	return items
}

// serviceStatusHeaderRows is what the pinned header costs the body: the summary
// line and the blank under it, drawn on every frame so the count and the
// checked-at time cannot scroll away under a long list.
const serviceStatusHeaderRows = 2

func (s *ServiceStatusScreen) View() string {
	header, body := s.render()
	return s.frameWithHeader(header, body, s.cursor, "", s.bar(header, body))
}

// render builds the pinned header and the scrollable body. Split out of View so
// paging can measure the same lines the operator is looking at.
func (s *ServiceStatusScreen) render() (jdeHeader, *jdeLines) {
	snapshot := s.deps.Health.Snapshot()
	body := &jdeLines{}

	if snapshot == nil || len(snapshot.Services) == 0 {
		// Unknown is not an outage, and this line is deliberately muted rather
		// than an error: the status check being unreachable tells us nothing
		// about the services themselves, and nothing is gated on it.
		header := jdeHeader(nil).add(jdeHeadContext, jdeIndent+StyleMuted.Render("Service status unavailable")).
			add(jdeHeadDecorative, "")
		// FOLDED against the live pane, not hand-broken. Both sentences were
		// split across two Adds at the width their author had in mind, and the
		// first line of each was 60 and 66 cells against the 51 an 80-column
		// terminal leaves — so clampToBox cut "so nothing is known" and "treated
		// as healthy," and the operator read a reassurance that stopped mid-
		// clause. A fold costs a row and loses nothing.
		for _, line := range jdeCaveatLinesStyled("The status check could not be reached, so "+
			"nothing is known about the external services right now.", s.bodyWidth(), 0,
			lipgloss.NewStyle()) {
			body.Add(line)
		}
		body.Add("")
		for _, line := range jdeCaveatLines("Nothing is being gated: an unknown status is treated "+
			"as healthy, so every control stays available.", s.bodyWidth()) {
			body.Add(line)
		}
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
	// CONTEXT and not essential, deliberately: the body lists every service
	// and its state, so an operator who loses this row loses the roll-up and the
	// checked-at time and still reads the same facts one row down. Nothing here
	// is a row they would act differently without, which is why this screen is
	// recorded in jdeHeadersWithoutEssentials rather than promoting a row to
	// make a sweep happy.
	header := jdeHeader(nil).add(jdeHeadContext, jdeIndent+summary).add(jdeHeadDecorative, "")

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
			body.AddFittedField(i, f, labelWidth, s.bodyWidth())
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
