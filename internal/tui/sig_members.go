// SIGMembersScreen — the member-management drill-in for one SIG.
//
// Reached with enter from the SIGListScreen. It mirrors the electrical
// parent→children list idiom (n add, x remove with a y/n confirm, j/k/g/G nav,
// r refresh) with one twist: "add" is not a form but a searchable user picker,
// reusing the category parent-picker's itemPickOption + '/' filter idiom. The
// picker loads the FULL user directory (ListAllUsers pages through, so a member
// on any page is reachable) and excludes users already in the SIG so the list
// only offers new members.
//
// Member add/remove map to the nested SIGMemberViewSet: POST {"user_id": id}
// adds, DELETE .../members/{userId}/ removes. Both are permitted for staff /
// superusers or an admin of the SIG; a caller without rights 403s, surfaced as a
// clear status message. n and G collide with global hotkeys so the screen claims
// them via LocalKeyScreen; WantsRawInput is asserted while the add picker or the
// remove confirm is up so their keys land here.
package tui

import (
	"context"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/uid0/scantty/internal/omsapi"
)

type sigMembersPhase int

const (
	sigMembersList sigMembersPhase = iota
	sigMembersAddPick
)

type SIGMembersScreen struct {
	deps    Deps
	sigID   int
	sigName string

	members        []omsapi.SIGMember
	cursor         int
	windowStart    int
	windowSize     int
	loading        bool
	loadErr        string
	terminalHeight int
	terminalWidth  int

	confirmingRemove bool
	removing         bool

	phase sigMembersPhase

	// add-member picker
	users        []omsapi.User
	usersLoaded  bool
	usersLoading bool
	usersErr     string
	adding       bool
	pendingAdd   *omsapi.User // the user an in-flight add is for (optimistic exclude)
	pickCursor   int
	pickStart    int
	pickSearch   textinput.Model
	pickTyping   bool
	pickOptions  []itemPickOption
}

type sigMembersLoadedMsg struct {
	members []omsapi.SIGMember
	err     error
}

type sigUsersLoadedMsg struct {
	users []omsapi.User
	err   error
}

type sigMemberAddedMsg struct {
	err error
}

type sigMemberRemovedMsg struct {
	err error
}

func NewSIGMembersScreen(deps Deps, sigID int, sigName string) *SIGMembersScreen {
	s := &SIGMembersScreen{
		deps:       deps,
		sigID:      sigID,
		sigName:    sigName,
		loading:    true,
		windowSize: 18,
	}
	s.pickSearch = textinput.New()
	s.pickSearch.Prompt = ""
	s.pickSearch.Placeholder = "filter"
	s.pickSearch.CharLimit = 80
	return s
}

func (s *SIGMembersScreen) Title() string {
	if s.sigName != "" {
		return "Members: " + s.sigName
	}
	return fmt.Sprintf("Members: SIG #%d", s.sigID)
}

// WantsRawInput claims every key while the add picker or the remove confirm is
// up so typing / j/k / y/n land here instead of the global hotkey layer.
func (s *SIGMembersScreen) WantsRawInput() bool {
	return s.confirmingRemove || s.phase == sigMembersAddPick
}

// HandlesKey claims the two list-phase action keys that collide with global
// hotkeys (n add, G bottom). x / r / j / k are not globals and fall through.
func (s *SIGMembersScreen) HandlesKey(key string) bool {
	if s.phase != sigMembersList || s.confirmingRemove {
		return false
	}
	return key == "n" || key == "G"
}

func (s *SIGMembersScreen) Init() tea.Cmd { return s.loadMembers() }

func (s *SIGMembersScreen) ctx() context.Context {
	if s.deps.Ctx != nil {
		return s.deps.Ctx
	}
	return context.Background()
}

func (s *SIGMembersScreen) loadMembers() tea.Cmd {
	deps := s.deps
	ctx := s.ctx()
	sigID := s.sigID
	return func() tea.Msg {
		page, err := deps.OMS.ListSIGMembers(ctx, sigID, nil)
		if err != nil {
			return sigMembersLoadedMsg{err: err}
		}
		return sigMembersLoadedMsg{members: page.Results}
	}
}

func (s *SIGMembersScreen) loadUsers() tea.Cmd {
	deps := s.deps
	ctx := s.ctx()
	return func() tea.Msg {
		users, err := deps.OMS.ListAllUsers(ctx, nil)
		return sigUsersLoadedMsg{users: users, err: err}
	}
}

func (s *SIGMembersScreen) computeWindowSize() int {
	return proseListWindow(s.terminalHeight, s.paneCells(), s.listBar(true, true))
}

// paneCells is the width this screen folds and budgets against: the pane the
// terminal really gave, never the 51 an 80-column one happens to leave.
func (s *SIGMembersScreen) paneCells() int { return proseBarCells(s.terminalWidth) }

// listBar names every key that acts on the member list, as a RECORD rather than
// a literal — prose_bar.go carries the conversion, proseNavCursor why the
// movement half is gated on one threshold, and proseListWindow what it costs the
// body.
//
// It used to be "j/k move · n add member · x remove · r refresh · esc back",
// naming two of the ten movement keystrokes the list's switch binds; and the
// empty list drew "n add member · esc back" while `r` reloaded it under no word
// at all. `x` needs a member to remove and comes off without one.
func (s *SIGMembersScreen) listBar(moves, rows bool) proseBar {
	out := append(proseNavCursor(moves), proseBarItem{Keys: []string{"n"}, Hint: "n add member"})
	if rows {
		out = append(out, proseBarItem{Keys: []string{"x"}, Hint: "x remove"})
	}
	return append(out, proseBarRefresh, proseBarEsc)
}

// pickBar names the keys the add-member picker answers, as a record.
//
// It used to be a legend written ABOVE the rows — "j/k move · / filter · enter
// add · esc back" — which named `j/k` alone while the arrows moved the cursor
// too, and went on naming all four while the filter box was open and every one
// of those letters was a character in the query. The typing state has a bar of
// its own now, naming the two keys the box does not eat.
func (s *SIGMembersScreen) pickBar(moves, options bool) proseBar {
	if s.pickTyping {
		return proseBar{{Keys: []string{"enter", "esc"}, Hint: "enter/esc close filter"}}
	}
	if s.usersLoading || s.usersErr != "" {
		return proseBar{{Keys: []string{"esc"}, Hint: "esc back"}}
	}
	return s.pickListBar(moves, options)
}

// pickListBar is the picker's bar over a loaded list, and with both answers true
// it is the bar at its TALLEST — the ceiling its window is budgeted against, for
// the reason proseListWindow gives — so the ceiling and the drawn bar are one
// expression.
func (s *SIGMembersScreen) pickListBar(moves, options bool) proseBar {
	out := append(proseNavStep(moves), proseBarItem{Keys: []string{"/"}, Hint: "/ filter"})
	if options {
		out = append(out, proseBarItem{Keys: []string{"enter"}, Hint: "enter add"})
	}
	return append(out, proseBarItem{Keys: []string{"esc"}, Hint: "esc back"})
}

// proseBar is the bar this screen is DRAWING, for whichever surface is up — the
// member list or the add-member picker drawn in its place — and nil in the
// states that draw something else instead: the remove confirm (a one-line y/n
// prompt naming its own keys), and an add while it is out, whose frame is a
// working line with every key held. A load in flight or failed draws loadBar's.
//
// ONE RECORD PER SURFACE is what converting a screen with a second cursor
// surface comes to: converting the list alone would have left the picker's
// literal behind a receiver the classifier counts as swept.
func (s *SIGMembersScreen) proseBar() proseBar {
	if s.phase == sigMembersAddPick {
		if s.adding {
			return nil
		}
		n := len(s.pickOptions)
		return s.pickBar(listNavMoves(n), n > 0)
	}
	if s.loading || s.loadErr != "" {
		return s.loadBar()
	}
	if s.confirmingRemove {
		return nil
	}
	n := len(s.members)
	return s.listBar(listNavMoves(n), n > 0)
}

// loadBar is the member list's bar while its load is out or has failed — what
// its key switch still answers with no rows drawn (prose_bar.go carries the
// defect and the decision). `n` opens the add picker whatever the list holds.
// `x` is not named: on a member a refresh kept, all it does is arm a confirm
// the frame does not draw.
func (s *SIGMembersScreen) loadBar() proseBar {
	return proseBar{
		{Keys: []string{"n"}, Hint: "n add member"},
		proseBarReloadFor(s.loadErr != ""),
		proseBarEsc,
	}
}

func (s *SIGMembersScreen) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		s.terminalHeight = m.Height
		s.terminalWidth = m.Width
		s.windowSize = s.computeWindowSize()
		s.scrollIntoView()
		return s, nil
	case sigMembersLoadedMsg:
		s.loading = false
		if m.err != nil {
			s.loadErr = m.err.Error()
		} else {
			s.loadErr = ""
			s.members = m.members
		}
		if s.cursor >= len(s.members) {
			s.cursor = 0
		}
		s.windowSize = s.computeWindowSize()
		s.scrollIntoView()
		return s, nil
	case sigUsersLoadedMsg:
		s.usersLoading = false
		s.usersLoaded = m.err == nil
		if m.err != nil {
			s.usersErr = m.err.Error()
		} else {
			s.usersErr = ""
			s.users = m.users
		}
		if s.phase == sigMembersAddPick {
			s.applyUserFilter()
			s.pickCursor = 0
		}
		return s, nil
	case sigMemberAddedMsg:
		s.adding = false
		if m.err != nil {
			s.pendingAdd = nil
			return s, Status("add member failed: "+m.err.Error(), StatusError)
		}
		// Optimistically fold the just-added user into `members` so the
		// picker's exclude-set is correct IMMEDIATELY — before the async
		// members reload lands. Without this, reopening the picker during the
		// reload window would still offer (and re-add) them off the stale list.
		// The reload replaces members wholesale with the authoritative rows.
		if s.pendingAdd != nil {
			if !s.isMember(s.pendingAdd.ID) {
				s.members = append(s.members, omsapi.SIGMember{
					ID:       s.pendingAdd.ID,
					Username: s.pendingAdd.Username,
					Email:    s.pendingAdd.Email,
				})
			}
			s.pendingAdd = nil
		}
		// Back to the list and reload so counts + the picker's exclude-set
		// reflect the new member authoritatively.
		s.phase = sigMembersList
		s.pickSearch.SetValue("")
		s.pickSearch.Blur()
		s.pickTyping = false
		s.loading = true
		return s, tea.Batch(Status("member added", StatusOK), s.loadMembers())
	case sigMemberRemovedMsg:
		s.removing = false
		s.confirmingRemove = false
		if m.err != nil {
			return s, Status("remove failed: "+m.err.Error(), StatusError)
		}
		s.loading = true
		return s, tea.Batch(Status("member removed", StatusOK), s.loadMembers())
	case tea.KeyMsg:
		if proseLoadKeyHidden(s.loading, s.loadErr, s.loadBar(), m.String()) {
			return s, nil
		}
		if s.confirmingRemove {
			return s.updateConfirmRemove(m)
		}
		if s.phase == sigMembersAddPick {
			return s.updateAddPick(m)
		}
		return s.updateList(m)
	}

	// Forward non-key messages (e.g. cursor blink) to the picker input while it
	// is open.
	if s.phase == sigMembersAddPick {
		var cmd tea.Cmd
		s.pickSearch, cmd = s.pickSearch.Update(msg)
		return s, cmd
	}
	return s, nil
}

func (s *SIGMembersScreen) updateList(m tea.KeyMsg) (Screen, tea.Cmd) {
	switch m.String() {
	case "j", "down":
		if s.cursor < len(s.members)-1 {
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
		if s.cursor >= len(s.members) {
			s.cursor = len(s.members) - 1
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
		s.cursor = len(s.members) - 1
		if s.cursor < 0 {
			s.cursor = 0
		}
		s.scrollIntoView()
	case "r":
		s.loading = true
		s.loadErr = ""
		return s, s.loadMembers()
	case "n":
		return s.enterAddPick()
	case "x":
		if _, ok := s.selected(); ok {
			s.confirmingRemove = true
		}
	}
	return s, nil
}

func (s *SIGMembersScreen) enterAddPick() (Screen, tea.Cmd) {
	s.phase = sigMembersAddPick
	s.pickTyping = false
	s.pickSearch.SetValue("")
	s.pickSearch.Blur()
	s.pickCursor = 0
	s.pickStart = 0
	if s.usersLoaded {
		s.applyUserFilter()
		return s, nil
	}
	// Lazy-load the directory the first time the picker opens; cache after.
	s.usersLoading = true
	s.usersErr = ""
	return s, s.loadUsers()
}

// applyUserFilter builds the picker rows from the loaded directory, dropping
// users who already belong to the SIG (so the list only offers new members) and
// honoring the '/' filter query.
func (s *SIGMembersScreen) applyUserFilter() {
	member := make(map[int]bool, len(s.members))
	for _, mem := range s.members {
		member[mem.ID] = true
	}
	q := strings.ToLower(strings.TrimSpace(s.pickSearch.Value()))
	opts := make([]itemPickOption, 0, len(s.users))
	for _, u := range s.users {
		if member[u.ID] {
			continue
		}
		label := assetUserLabel(u)
		if q != "" && !strings.Contains(strings.ToLower(label), q) &&
			!strings.Contains(strings.ToLower(u.Email), q) {
			continue
		}
		opts = append(opts, itemPickOption{id: u.ID, label: label})
	}
	s.pickOptions = opts
	if s.pickCursor >= len(s.pickOptions) {
		s.pickCursor = 0
	}
}

func (s *SIGMembersScreen) updateAddPick(m tea.KeyMsg) (Screen, tea.Cmd) {
	if s.adding {
		return s, nil
	}
	if s.pickTyping {
		switch m.Type {
		case tea.KeyEsc:
			s.pickTyping = false
			s.pickSearch.Blur()
			return s, nil
		case tea.KeyEnter:
			s.pickTyping = false
			s.pickSearch.Blur()
			s.applyUserFilter()
			s.pickCursor = 0
			return s, nil
		}
		var cmd tea.Cmd
		s.pickSearch, cmd = s.pickSearch.Update(m)
		s.applyUserFilter()
		return s, cmd
	}

	switch m.String() {
	case "esc":
		s.phase = sigMembersList
		s.pickSearch.SetValue("")
		s.pickSearch.Blur()
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
		return s.commitAdd()
	}
	return s, nil
}

func (s *SIGMembersScreen) commitAdd() (Screen, tea.Cmd) {
	if s.pickCursor < 0 || s.pickCursor >= len(s.pickOptions) {
		return s, nil
	}
	userID := s.pickOptions[s.pickCursor].id
	// Remember which user this add is for so the success handler can fold them
	// into `members` optimistically (see sigMemberAddedMsg).
	s.pendingAdd = nil
	for i := range s.users {
		if s.users[i].ID == userID {
			u := s.users[i]
			s.pendingAdd = &u
			break
		}
	}
	s.adding = true
	deps := s.deps
	ctx := s.ctx()
	sigID := s.sigID
	return s, func() tea.Msg {
		return sigMemberAddedMsg{err: deps.OMS.AddSIGMember(ctx, sigID, userID)}
	}
}

func (s *SIGMembersScreen) isMember(userID int) bool {
	for _, mem := range s.members {
		if mem.ID == userID {
			return true
		}
	}
	return false
}

func (s *SIGMembersScreen) updateConfirmRemove(m tea.KeyMsg) (Screen, tea.Cmd) {
	if s.removing {
		return s, nil
	}
	switch m.String() {
	case "y", "Y":
		mem, ok := s.selected()
		if !ok {
			s.confirmingRemove = false
			return s, nil
		}
		s.removing = true
		deps := s.deps
		ctx := s.ctx()
		sigID := s.sigID
		userID := mem.ID
		return s, func() tea.Msg {
			return sigMemberRemovedMsg{err: deps.OMS.RemoveSIGMember(ctx, sigID, userID)}
		}
	case "n", "N", "esc":
		s.confirmingRemove = false
	}
	return s, nil
}

func (s *SIGMembersScreen) selected() (omsapi.SIGMember, bool) {
	if s.cursor < 0 || s.cursor >= len(s.members) {
		return omsapi.SIGMember{}, false
	}
	return s.members[s.cursor], true
}

func (s *SIGMembersScreen) scrollIntoView() {
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
	if len(s.members) <= s.windowSize {
		s.windowStart = 0
	}
}

func (s *SIGMembersScreen) View() string {
	if s.phase == sigMembersAddPick {
		return s.viewAddPick()
	}
	if s.loading {
		return proseLoadingFrame("Loading members…", s.paneCells(), s.proseBar())
	}
	if s.loadErr != "" {
		return proseFailedFrame(s.loadErr, s.terminalHeight, s.paneCells(), s.proseBar())
	}
	if s.confirmingRemove {
		return s.viewConfirm()
	}
	if len(s.members) == 0 {
		var b strings.Builder
		b.WriteString(StyleMuted.Render("No members yet.") + "\n\n")
		b.WriteString(s.proseBar().render(s.paneCells()))
		return b.String()
	}
	rows := make([]string, len(s.members))
	for i := range s.members {
		rows[i] = s.renderRow(i)
	}
	head := StyleMuted.Render(fmt.Sprintf("%d members", len(s.members))) + "\n"
	return proseFlatListFrame(head, rows, s.cursor, &s.windowStart, s.terminalHeight,
		s.paneCells(), s.listBar(true, true), s.proseBar())
}

func (s *SIGMembersScreen) viewConfirm() string {
	mem, ok := s.selected()
	if !ok {
		return ""
	}
	if s.removing {
		return StyleMuted.Render("Removing…")
	}
	name := mem.Username
	if name == "" {
		name = fmt.Sprintf("#%d", mem.ID)
	}
	return StyleStatusWarn.Render(fmt.Sprintf("Remove %s from this SIG?  y remove · n/esc cancel", name))
}

func (s *SIGMembersScreen) viewAddPick() string {
	var b strings.Builder
	cells := s.paneCells()
	b.WriteString(StyleTitle.Render("Add member") + "\n\n")
	bar := "\n" + s.proseBar().render(cells)
	if s.usersLoading {
		return b.String() + StyleMuted.Render("Loading users…") + "\n" + bar
	}
	if s.usersErr != "" {
		return b.String() + StyleStatusError.Render("Error: ") + s.usersErr + "\n" + bar
	}
	if s.adding {
		b.WriteString(StyleMuted.Render("Adding…"))
		return b.String()
	}
	if s.pickTyping || s.pickSearch.Value() != "" {
		b.WriteString(StyleMuted.Render("filter: ") + woBoxView(s.pickSearch, cells, "filter: ") + "\n\n")
	}
	if len(s.pickOptions) == 0 {
		if strings.TrimSpace(s.pickSearch.Value()) != "" {
			b.WriteString(StyleMuted.Render("(no matching users)"))
		} else {
			b.WriteString(StyleMuted.Render("All users are already members."))
		}
		return b.String() + "\n" + bar
	}
	// The directory is every user, so the window is DERIVED from the pane and
	// the folded bar rather than a flat twelve rows: at twelve the picker ran
	// past any terminal shorter than about twenty rows, and what clampToBox took
	// was the bottom — which, once the legend moved under the rows, is the bar.
	rows := make([]string, len(s.pickOptions))
	for i, opt := range s.pickOptions {
		caret := "    "
		if i == s.pickCursor {
			caret = "  ▸ "
		}
		if i == s.pickCursor {
			rows[i] = StyleSidebarItemActive.Render(caret + opt.label)
		} else {
			rows[i] = caret + opt.label
		}
	}
	return proseFlatListFrame(b.String(), rows, s.pickCursor, &s.pickStart, s.terminalHeight,
		cells, s.pickListBar(true, true), s.proseBar())
}

func (s *SIGMembersScreen) renderRow(i int) string {
	mem := s.members[i]
	marker := "  "
	if i == s.cursor {
		marker = "▸ "
	}
	line := marker + mem.Username
	if mem.IsSIGAdmin {
		line += " " + StyleStatusOK.Render("(admin)")
	}
	if i == s.cursor {
		line = StyleSidebarItemActive.Render(line)
	}
	meta := []string{}
	if mem.Handle != "" && mem.Handle != mem.Username {
		meta = append(meta, "@"+mem.Handle)
	}
	if mem.Email != "" {
		meta = append(meta, mem.Email)
	}
	if len(meta) > 0 {
		line += " " + StyleMuted.Render("("+strings.Join(meta, " · ")+")")
	}
	return line
}
