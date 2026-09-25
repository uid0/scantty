// Badge enrollment — assign / clear a member's access badge (RFID UID).
//
// TUI counterpart to the web ForgeKeyBadgeEnrollmentPage ("Facilities · ForgeKey
// → Badge enrollment"). Lists the staff-only member directory with each member's
// current badge and lets a staffer:
//   - Enroll a badge by arming "enroll next scan" and having the member tap a
//     reader; the screen polls until the interlock captures the UID (e / enter).
//   - Set a badge by hand (s) — the manual-entry fallback when no reader is handy.
//   - Clear a badge (x, with a y/n confirm).
//
// This mirrors the web page 1:1 — same three actions, same field (the member's
// badge_number), nothing more. The badge UID is credential material; like the web
// enrollment admin it is shown here (and only here) but never logged. Certificate
// / PKI material is a separate surface and out of scope.
//
// Reached from the Facilities menu (hotkey E). Facilities is staff-gated, matching
// the backend BadgeEnrollmentViewSet + UserDirectoryViewSet (both IsAdminUser); a
// non-staff caller that somehow reaches here sees a clean 403 rather than a crash.
package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/uid0/scantty/internal/forgekeyapi"
	"github.com/uid0/scantty/internal/omsapi"
)

type badgeMode int

const (
	badgeModeList badgeMode = iota
	badgeModeSearch
	badgeModeSetInput
	badgeModeClearConfirm
	badgeModeEnroll
)

// badgeEnrollPollInterval / badgeEnrollMaxPolls bound the arm→scan poll. The web
// polls every 2s until the interlock captures a tap or the armed record expires;
// the max is a safety net a little past the arm TTL so a stuck poll can't loop
// forever.
const (
	badgeEnrollPollInterval = 2 * time.Second
	badgeEnrollMaxPolls     = 40
)

type badgeEnrollStatus int

const (
	badgeWaiting badgeEnrollStatus = iota
	badgeCaptured
	badgeExpired
)

type BadgeEnrollmentScreen struct {
	deps Deps

	all    []omsapi.User // full staff directory (badge_number included)
	rows   []omsapi.User // current (filtered) view
	cursor int

	windowStart    int
	windowSize     int
	terminalHeight int
	terminalWidth  int

	loading bool
	loadErr string

	mode badgeMode

	filter      string
	searchInput textinput.Model
	badgeInput  textinput.Model

	busy bool // a set/clear/arm request is in flight

	// enroll (arm → poll) sub-state
	enrollUserID int
	enrollName   string
	enrollStatus badgeEnrollStatus
	enrollBadge  string
	enrollPolls  int
}

type badgeUsersLoadedMsg struct {
	users []omsapi.User
	err   error
}

type badgeArmedMsg struct {
	userID int
	name   string
	err    error
}

type badgePolledMsg struct {
	forUser int
	state   *forgekeyapi.BadgeEnrollmentState
	err     error
}

type badgeSetMsg struct {
	userID  int
	badge   *string
	cleared bool
	err     error
}

func NewBadgeEnrollmentScreen(deps Deps) *BadgeEnrollmentScreen {
	si := textinput.New()
	si.Prompt = "search: "
	si.Placeholder = "name, username, or email"
	si.CharLimit = 100
	bi := textinput.New()
	bi.Prompt = ""
	bi.Placeholder = "badge UID (blank clears)"
	bi.CharLimit = 128
	return &BadgeEnrollmentScreen{
		deps:        deps,
		loading:     true,
		windowSize:  18,
		searchInput: si,
		badgeInput:  bi,
	}
}

func (s *BadgeEnrollmentScreen) Title() string { return "Badge enrollment" }

// WantsRawInput claims every key while a sub-flow (search input, manual-set input,
// clear confirm, or an active enrollment) is up, so keystrokes land here instead of
// the root's global hotkeys.
func (s *BadgeEnrollmentScreen) WantsRawInput() bool { return s.mode != badgeModeList }

// HandlesKey claims, in list mode, the action keys that collide with global
// hotkeys — e (global e = e-Paper), s (nav s = scan), / (global / = palette) — so
// enroll / set / search reach this screen. All other list keys (j/k/x/r/enter) are
// globally free and arrive via the app.go fallthrough.
func (s *BadgeEnrollmentScreen) HandlesKey(key string) bool {
	return key == "e" || key == "s" || key == "/"
}

func (s *BadgeEnrollmentScreen) Init() tea.Cmd { return s.loadUsers() }

func (s *BadgeEnrollmentScreen) ctx() context.Context {
	if s.deps.Ctx != nil {
		return s.deps.Ctx
	}
	return context.Background()
}

func (s *BadgeEnrollmentScreen) loadUsers() tea.Cmd {
	deps := s.deps
	ctx := s.ctx()
	return func() tea.Msg {
		users, err := deps.OMS.ListAllUsers(ctx, nil)
		return badgeUsersLoadedMsg{users: users, err: err}
	}
}

func (s *BadgeEnrollmentScreen) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		s.terminalHeight = m.Height
		s.terminalWidth = m.Width
		s.windowSize = s.computeWindowSize()
		s.scrollIntoView()
		return s, nil
	case badgeUsersLoadedMsg:
		s.loading = false
		if m.err != nil {
			s.loadErr = m.err.Error()
		} else {
			s.loadErr = ""
			s.all = m.users
			s.applyFilter()
		}
		s.windowSize = s.computeWindowSize()
		s.scrollIntoView()
		return s, nil
	case badgeArmedMsg:
		s.busy = false
		if m.err != nil {
			s.mode = badgeModeList
			return s, Status("arm enrollment failed: "+m.err.Error(), StatusError)
		}
		s.mode = badgeModeEnroll
		s.enrollUserID = m.userID
		s.enrollName = m.name
		s.enrollStatus = badgeWaiting
		s.enrollBadge = ""
		s.enrollPolls = 0
		return s, s.pollCmd()
	case badgePolledMsg:
		return s.onPolled(m)
	case badgeSetMsg:
		return s.onSet(m)
	case tea.KeyMsg:
		if proseLoadKeyHidden(s.loading, s.loadErr, s.loadBar(), m.String()) {
			return s, nil
		}
		switch s.mode {
		case badgeModeSearch:
			return s.updateSearch(m)
		case badgeModeSetInput:
			return s.updateSetInput(m)
		case badgeModeClearConfirm:
			return s.updateClearConfirm(m)
		case badgeModeEnroll:
			return s.updateEnroll(m)
		default:
			return s.updateList(m)
		}
	}
	return s, nil
}

func (s *BadgeEnrollmentScreen) updateList(m tea.KeyMsg) (Screen, tea.Cmd) {
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
	case "pgdown":
		s.cursor += s.windowSize
		if s.cursor >= len(s.rows) {
			s.cursor = len(s.rows) - 1
		}
		s.scrollIntoView()
	case "pgup":
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
		return s, s.loadUsers()
	case "/":
		s.mode = badgeModeSearch
		s.searchInput.SetValue(s.filter)
		s.searchInput.CursorEnd()
		s.searchInput.Focus()
		return s, textinput.Blink
	case "e", "enter":
		return s.beginEnroll()
	case "s":
		return s.beginSet()
	case "x":
		u, ok := s.selected()
		if !ok {
			return s, nil
		}
		if !badgeHasValue(u) {
			return s, Status("no badge to clear", StatusWarn)
		}
		s.mode = badgeModeClearConfirm
	}
	return s, nil
}

func (s *BadgeEnrollmentScreen) beginEnroll() (Screen, tea.Cmd) {
	u, ok := s.selected()
	if !ok || s.busy {
		return s, nil
	}
	s.busy = true
	deps := s.deps
	ctx := s.ctx()
	id := u.ID
	name := assetUserLabel(u)
	return s, func() tea.Msg {
		_, err := deps.ForgeKey.ArmBadgeEnrollment(ctx, id, "")
		return badgeArmedMsg{userID: id, name: name, err: err}
	}
}

func (s *BadgeEnrollmentScreen) beginSet() (Screen, tea.Cmd) {
	u, ok := s.selected()
	if !ok {
		return s, nil
	}
	s.mode = badgeModeSetInput
	// Preload the current badge so an edit starts from it (mirrors the web
	// openManual, which seeds the input with user.badge_number).
	s.badgeInput.SetValue(badgeValue(u))
	s.badgeInput.CursorEnd()
	s.badgeInput.Focus()
	return s, textinput.Blink
}

func (s *BadgeEnrollmentScreen) updateSearch(m tea.KeyMsg) (Screen, tea.Cmd) {
	switch m.String() {
	case "enter", "esc":
		s.mode = badgeModeList
		s.searchInput.Blur()
		return s, nil
	}
	var cmd tea.Cmd
	s.searchInput, cmd = s.searchInput.Update(m)
	s.filter = s.searchInput.Value()
	s.applyFilter()
	return s, cmd
}

func (s *BadgeEnrollmentScreen) updateSetInput(m tea.KeyMsg) (Screen, tea.Cmd) {
	switch m.String() {
	case "esc":
		s.mode = badgeModeList
		s.badgeInput.Blur()
		return s, nil
	case "enter":
		if s.busy {
			return s, nil
		}
		u, ok := s.selected()
		if !ok {
			s.mode = badgeModeList
			return s, nil
		}
		badge := badgeInputToPtr(s.badgeInput.Value())
		s.busy = true
		deps := s.deps
		ctx := s.ctx()
		id := u.ID
		cleared := badge == nil
		return s, func() tea.Msg {
			res, err := deps.ForgeKey.SetBadge(ctx, id, badge)
			var nb *string
			if err == nil && res != nil {
				nb = res.BadgeNumber
			}
			return badgeSetMsg{userID: id, badge: nb, cleared: cleared, err: err}
		}
	}
	var cmd tea.Cmd
	s.badgeInput, cmd = s.badgeInput.Update(m)
	return s, cmd
}

func (s *BadgeEnrollmentScreen) updateClearConfirm(m tea.KeyMsg) (Screen, tea.Cmd) {
	if s.busy {
		return s, nil
	}
	switch m.String() {
	case "y", "Y":
		u, ok := s.selected()
		if !ok {
			s.mode = badgeModeList
			return s, nil
		}
		s.busy = true
		deps := s.deps
		ctx := s.ctx()
		id := u.ID
		return s, func() tea.Msg {
			_, err := deps.ForgeKey.SetBadge(ctx, id, nil)
			return badgeSetMsg{userID: id, badge: nil, cleared: true, err: err}
		}
	case "n", "N", "esc":
		s.mode = badgeModeList
	}
	return s, nil
}

func (s *BadgeEnrollmentScreen) updateEnroll(m tea.KeyMsg) (Screen, tea.Cmd) {
	// Any key leaves the enrollment panel. While still waiting for a tap, disarm
	// best-effort (the armed record TTL-expires on its own regardless).
	waiting := s.enrollStatus == badgeWaiting
	s.mode = badgeModeList
	if waiting {
		deps := s.deps
		ctx := s.ctx()
		return s, func() tea.Msg {
			_ = deps.ForgeKey.CancelBadgeEnrollment(ctx, "")
			return nil
		}
	}
	return s, nil
}

func (s *BadgeEnrollmentScreen) pollCmd() tea.Cmd {
	deps := s.deps
	ctx := s.ctx()
	uid := s.enrollUserID
	return tea.Tick(badgeEnrollPollInterval, func(time.Time) tea.Msg {
		st, err := deps.ForgeKey.GetBadgeEnrollmentState(ctx, uid, "")
		return badgePolledMsg{forUser: uid, state: st, err: err}
	})
}

func (s *BadgeEnrollmentScreen) onPolled(m badgePolledMsg) (Screen, tea.Cmd) {
	// The user may have dismissed the panel (or started a different enroll) while
	// a tick was in flight; ignore stale results.
	if s.mode != badgeModeEnroll || m.forUser != s.enrollUserID {
		return s, nil
	}
	if m.err != nil {
		s.mode = badgeModeList
		return s, Status("enrollment check failed: "+m.err.Error(), StatusError)
	}
	st := m.state
	if st != nil && st.Captured != nil {
		s.enrollStatus = badgeCaptured
		s.enrollBadge = st.Captured.BadgeNumber
		badge := st.Captured.BadgeNumber
		s.setBadgeOnRow(s.enrollUserID, &badge)
		return s, Status("badge enrolled", StatusOK)
	}
	if st == nil || !st.Armed {
		s.enrollStatus = badgeExpired
		return s, nil
	}
	s.enrollPolls++
	if s.enrollPolls >= badgeEnrollMaxPolls {
		s.enrollStatus = badgeExpired
		return s, nil
	}
	return s, s.pollCmd()
}

func (s *BadgeEnrollmentScreen) onSet(m badgeSetMsg) (Screen, tea.Cmd) {
	s.busy = false
	if m.err != nil {
		s.mode = badgeModeList
		return s, Status("set badge failed: "+m.err.Error(), StatusError)
	}
	s.setBadgeOnRow(m.userID, m.badge)
	s.mode = badgeModeList
	s.badgeInput.Blur()
	if m.cleared {
		return s, Status("badge cleared", StatusOK)
	}
	return s, Status("badge set", StatusOK)
}

func (s *BadgeEnrollmentScreen) selected() (omsapi.User, bool) {
	if s.cursor < 0 || s.cursor >= len(s.rows) {
		return omsapi.User{}, false
	}
	return s.rows[s.cursor], true
}

// setBadgeOnRow updates a member's badge in both the full directory and the
// filtered view so the change shows immediately (optimistic; the next refresh
// re-authoritative-loads).
func (s *BadgeEnrollmentScreen) setBadgeOnRow(userID int, badge *string) {
	for i := range s.all {
		if s.all[i].ID == userID {
			s.all[i].BadgeNumber = badge
		}
	}
	for i := range s.rows {
		if s.rows[i].ID == userID {
			s.rows[i].BadgeNumber = badge
		}
	}
}

func (s *BadgeEnrollmentScreen) applyFilter() {
	q := strings.ToLower(strings.TrimSpace(s.filter))
	if q == "" {
		s.rows = s.all
	} else {
		out := make([]omsapi.User, 0, len(s.all))
		for _, u := range s.all {
			if badgeUserMatches(u, q) {
				out = append(out, u)
			}
		}
		s.rows = out
	}
	if s.cursor >= len(s.rows) {
		s.cursor = 0
	}
	s.scrollIntoView()
}

// badgeUserMatches filters on name / username / email — the same fields the web
// directory search covers. Deliberately NOT the badge UID.
func badgeUserMatches(u omsapi.User, q string) bool {
	for _, f := range []string{u.Username, u.DisplayName, u.FirstName, u.LastName, u.Email} {
		if strings.Contains(strings.ToLower(f), q) {
			return true
		}
	}
	return false
}

func (s *BadgeEnrollmentScreen) computeWindowSize() int {
	return proseListWindow(s.terminalHeight, s.paneCells(), s.listBar(true, true))
}

// paneCells is the width this screen folds and budgets against: the pane the
// terminal really gave, never the 51 an 80-column one happens to leave.
func (s *BadgeEnrollmentScreen) paneCells() int { return proseBarCells(s.terminalWidth) }

// listBar names every key that acts on the member directory, as a RECORD rather
// than a literal — prose_bar.go carries the conversion, proseNavCursor why the
// movement half is gated on one threshold, and proseListWindow what it costs the
// body.
//
// It used to be "j/k move · e/enter enroll · s set · x clear · / search · r
// refresh · esc back": 76 cells against the 51 an 80-column pane gives, naming
// two of the ten movement keystrokes the list's switch binds — and drawn
// unchanged under a filter that matched nobody, where the three row actions have
// no member to act on. Enroll comes off while an arm is out, because the arm
// declines a second one without a word.
func (s *BadgeEnrollmentScreen) listBar(moves, rows bool) proseBar {
	out := proseNavCursor(moves)
	if rows {
		if !s.busy {
			out = append(out, proseBarItem{Keys: []string{"e", "enter"}, Hint: "e/enter enroll"})
		}
		out = append(out,
			proseBarItem{Keys: []string{"s"}, Hint: "s set"},
			proseBarItem{Keys: []string{"x"}, Hint: "x clear"},
		)
	}
	return append(out,
		proseBarItem{Keys: []string{"/"}, Hint: "/ search"},
		proseBarRefresh,
		proseBarEsc,
	)
}

// proseBar is the bar this screen is DRAWING, for whichever surface is up — the
// directory, its search box, or the manual badge entry drawn in place of the
// list — and nil in the states that draw something else instead: the clear
// confirm (a one-line y/n prompt naming its own keys), a manual set while it is
// out, and the enrolment panel, whose every key is "any key". A load in flight
// or failed draws loadBar's.
//
// THE SEARCH BOX HAS A BAR OF ITS OWN. It used to open over the list with a
// hint line of its own above the rows and the LIST's footer still under them, so
// one frame said `j/k move` while `j` was a character going into the query.
func (s *BadgeEnrollmentScreen) proseBar() proseBar {
	if s.loading || s.loadErr != "" {
		return s.loadBar()
	}
	switch s.mode {
	case badgeModeSearch:
		return proseBar{{Keys: []string{"enter", "esc"}, Hint: "enter/esc close search"}}
	case badgeModeSetInput:
		if s.busy {
			return nil
		}
		return proseBar{
			{Keys: []string{"enter"}, Hint: "enter save"},
			{Keys: []string{"esc"}, Hint: "esc cancel"},
		}
	case badgeModeClearConfirm, badgeModeEnroll:
		return nil
	}
	return s.listBar(listNavMoves(len(s.rows)), len(s.rows) > 0)
}

// loadBar is the member list's bar while its load is out or has failed — what
// its key switch still answers with no rows drawn (prose_bar.go carries the
// defect and the decision). `e`/`enter` still ARM ENROLLMENT for the member a
// refresh kept under the cursor, which the frame no longer draws: named because
// it acts, and a candidate for gating. `/`, `s` and `x` are not named — each
// opens a box or a confirm this frame does not draw.
func (s *BadgeEnrollmentScreen) loadBar() proseBar {
	var out proseBar
	if _, ok := s.selected(); ok && !s.busy {
		out = append(out, proseBarItem{Keys: []string{"e", "enter"}, Hint: "e/enter enroll"})
	}
	return append(out, proseBarReloadFor(s.loadErr != ""), proseBarEsc)
}

func (s *BadgeEnrollmentScreen) scrollIntoView() {
	if s.windowSize <= 0 {
		s.windowSize = 18
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

func (s *BadgeEnrollmentScreen) View() string {
	if s.loading {
		return proseLoadingFrame("Loading members…", s.paneCells(), s.proseBar())
	}
	if s.loadErr != "" {
		return proseRefusalFrame("Error: ", StyleStatusError, s.loadErr,
			"Badge enrollment is staff-only.", s.terminalHeight, s.paneCells(), s.proseBar())
	}
	switch s.mode {
	case badgeModeSetInput:
		return s.viewSetInput()
	case badgeModeClearConfirm:
		return s.viewClearConfirm()
	case badgeModeEnroll:
		return s.viewEnroll()
	}
	return s.viewList()
}

func (s *BadgeEnrollmentScreen) viewList() string {
	var b strings.Builder
	// ONE line ahead of the window in every mode, because that is the one line
	// proseListFixedRows reserves: the blank that used to follow it, and the
	// search box's own hint line, were rows the window was never budgeted for.
	if s.mode == badgeModeSearch {
		b.WriteString(woBoxView(s.searchInput, s.paneCells(), "") + "\n")
	} else if s.filter != "" {
		b.WriteString(StyleMuted.Render(fmt.Sprintf("filter: %q — %d of %d members", s.filter, len(s.rows), len(s.all))) + "\n")
	} else {
		b.WriteString(StyleMuted.Render(fmt.Sprintf("%d members", len(s.all))) + "\n")
	}

	if len(s.rows) == 0 {
		b.WriteString("\n" + StyleMuted.Render("No members found."))
		b.WriteString("\n\n" + s.proseBar().render(s.paneCells()))
		return b.String()
	}

	rows := make([]string, len(s.rows))
	for i := range s.rows {
		rows[i] = s.renderRow(i)
	}
	return proseFlatListFrame(b.String(), rows, s.cursor, &s.windowStart, s.terminalHeight,
		s.paneCells(), s.listBar(true, true), s.proseBar())
}

func (s *BadgeEnrollmentScreen) renderRow(i int) string {
	u := s.rows[i]
	marker := "  "
	if i == s.cursor {
		marker = "▸ "
	}
	label := assetUserLabel(u)
	var badge string
	if badgeHasValue(u) {
		badge = StyleTitle.Render(badgeValue(u))
	} else {
		badge = StyleMuted.Render("none")
	}
	line := marker + label
	if i == s.cursor {
		line = StyleSidebarItemActive.Render(line)
	}
	return line + "  " + StyleMuted.Render("badge: ") + badge
}

func (s *BadgeEnrollmentScreen) viewSetInput() string {
	name := s.selectedName()
	var b strings.Builder
	b.WriteString(StyleTitle.Render("Set badge — "+name) + "\n\n")
	b.WriteString("  " + woBoxView(s.badgeInput, s.paneCells(), "  ") + "\n\n")
	if s.busy {
		b.WriteString(StyleMuted.Render("Saving…"))
	} else {
		b.WriteString(StyleMuted.Render("A blank badge clears it.") + "\n\n")
		b.WriteString(s.proseBar().render(s.paneCells()))
	}
	return b.String()
}

func (s *BadgeEnrollmentScreen) viewClearConfirm() string {
	name := s.selectedName()
	if s.busy {
		return StyleMuted.Render("Clearing…")
	}
	return StyleStatusWarn.Render(fmt.Sprintf(
		"Clear %s's badge? They will no longer be able to scan in.  y clear · n/esc cancel", name))
}

func (s *BadgeEnrollmentScreen) viewEnroll() string {
	var b strings.Builder
	b.WriteString(StyleTitle.Render("Enroll badge — "+s.enrollName) + "\n\n")
	switch s.enrollStatus {
	case badgeWaiting:
		b.WriteString(StyleStatusWarn.Render("⏳ Ask them to tap their badge on the reader…") + "\n")
		b.WriteString(StyleMuted.Render("waiting for a scan · press any key to cancel"))
	case badgeCaptured:
		b.WriteString(StyleStatusOK.Render("✓ Captured badge "+s.enrollBadge) + "\n")
		b.WriteString(StyleMuted.Render("press any key to return"))
	case badgeExpired:
		b.WriteString(StyleStatusError.Render("✗ No tap detected — enrollment expired.") + "\n")
		b.WriteString(StyleMuted.Render("press any key to return, then e to try again"))
	}
	return b.String()
}

func (s *BadgeEnrollmentScreen) selectedName() string {
	if u, ok := s.selected(); ok {
		return assetUserLabel(u)
	}
	return "member"
}

// badgeInputToPtr turns the manual-entry field into the SetBadge argument: a
// trimmed non-empty value sets the badge; blank returns nil to CLEAR it, mirroring
// the web saveManual's `value || null`.
func badgeInputToPtr(raw string) *string {
	v := strings.TrimSpace(raw)
	if v == "" {
		return nil
	}
	return &v
}

// badgeValue returns a member's badge UID ("" when unset). badgeHasValue reports
// whether it is set. Both treat a nil pointer and an empty string as "no badge".
func badgeValue(u omsapi.User) string {
	if u.BadgeNumber == nil {
		return ""
	}
	return *u.BadgeNumber
}

func badgeHasValue(u omsapi.User) bool {
	return strings.TrimSpace(badgeValue(u)) != ""
}
