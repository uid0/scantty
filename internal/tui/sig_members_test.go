package tui

import (
	"strings"
	"testing"

	"github.com/uid0/scantty/internal/omsapi"
)

// TestSIGMembers_HandlesKey confirms n / G are claimed only in the plain list
// phase — during the add picker or a remove confirm the screen is raw-input, so
// HandlesKey must yield to the WantsRawInput path.
func TestSIGMembers_HandlesKey(t *testing.T) {
	s := NewSIGMembersScreen(Deps{}, 5, "Woodshop")
	if !s.HandlesKey("n") || !s.HandlesKey("G") {
		t.Errorf("list phase should claim n and G")
	}
	if s.HandlesKey("x") || s.HandlesKey("enter") {
		t.Errorf("x / enter must not be claimed")
	}
	s.phase = sigMembersAddPick
	if s.HandlesKey("n") || s.HandlesKey("G") {
		t.Errorf("add-pick phase must not claim keys (raw input owns them)")
	}
	s.phase = sigMembersList
	s.confirmingRemove = true
	if s.HandlesKey("n") {
		t.Errorf("remove-confirm must not claim keys (raw input owns them)")
	}
}

// TestSIGMembers_WantsRawInput asserts raw input is on only while the add picker
// or the remove confirm is up.
func TestSIGMembers_WantsRawInput(t *testing.T) {
	s := NewSIGMembersScreen(Deps{}, 5, "W")
	if s.WantsRawInput() {
		t.Errorf("plain list should not want raw input")
	}
	s.phase = sigMembersAddPick
	if !s.WantsRawInput() {
		t.Errorf("add pick should want raw input")
	}
	s.phase = sigMembersList
	s.confirmingRemove = true
	if !s.WantsRawInput() {
		t.Errorf("remove confirm should want raw input")
	}
}

// TestSIGMembers_RemoveConfirm confirms x arms the remove confirm and n cancels.
func TestSIGMembers_RemoveConfirm(t *testing.T) {
	s := NewSIGMembersScreen(Deps{}, 5, "W")
	s.loading = false
	s.members = []omsapi.SIGMember{{ID: 7, Username: "ada"}}
	s.cursor = 0
	s.Update(sigKey("x"))
	if !s.confirmingRemove || !s.WantsRawInput() {
		t.Errorf("x should arm the remove confirm + raw input")
	}
	if out := s.View(); !strings.Contains(out, "Remove ada") {
		t.Errorf("confirm view should name the member: %q", out)
	}
	s.Update(sigKey("n"))
	if s.confirmingRemove {
		t.Errorf("n should cancel the confirm")
	}
}

// TestSIGMembers_AddPickExcludesMembers confirms the user picker drops users who
// already belong to the SIG so only new members are offered.
func TestSIGMembers_AddPickExcludesMembers(t *testing.T) {
	s := NewSIGMembersScreen(Deps{}, 5, "W")
	s.members = []omsapi.SIGMember{{ID: 7, Username: "ada"}}
	s.users = []omsapi.User{
		{ID: 7, Username: "ada"},
		{ID: 8, Username: "grace"},
	}
	s.usersLoaded = true
	s.applyUserFilter()
	if len(s.pickOptions) != 1 {
		t.Fatalf("pickOptions = %d, want 1 (member excluded)", len(s.pickOptions))
	}
	if s.pickOptions[0].id != 8 {
		t.Errorf("offered user id = %d, want 8 (grace)", s.pickOptions[0].id)
	}
}

// TestSIGMembers_AddPickFilter confirms the '/' filter narrows the picker by
// name / username / email.
func TestSIGMembers_AddPickFilter(t *testing.T) {
	s := NewSIGMembersScreen(Deps{}, 5, "W")
	s.users = []omsapi.User{
		{ID: 8, Username: "grace", Email: "grace@ex.org"},
		{ID: 9, Username: "linus", Email: "linus@ex.org"},
	}
	s.usersLoaded = true
	s.pickSearch.SetValue("grace")
	s.applyUserFilter()
	if len(s.pickOptions) != 1 || s.pickOptions[0].id != 8 {
		t.Fatalf("filter grace → %+v, want just id 8", s.pickOptions)
	}
	// Email substring also matches.
	s.pickSearch.SetValue("linus@ex")
	s.applyUserFilter()
	if len(s.pickOptions) != 1 || s.pickOptions[0].id != 9 {
		t.Fatalf("filter by email → %+v, want just id 9", s.pickOptions)
	}
}

// TestSIGMembers_AddKeyEntersPickAndLoads confirms n switches into the add-pick
// phase and (with no cached directory) fires the user load.
func TestSIGMembers_AddKeyEntersPickAndLoads(t *testing.T) {
	s := NewSIGMembersScreen(Deps{}, 5, "W")
	s.loading = false
	_, cmd := s.Update(sigKey("n"))
	if s.phase != sigMembersAddPick {
		t.Fatalf("n should enter the add-pick phase")
	}
	if !s.usersLoading || cmd == nil {
		t.Errorf("first add-pick open should trigger a user load")
	}
}

// TestSIGMembers_UsersLoadedRebuildsOptions confirms a users-loaded message,
// while in the picker, populates the options.
func TestSIGMembers_UsersLoadedRebuildsOptions(t *testing.T) {
	s := NewSIGMembersScreen(Deps{}, 5, "W")
	s.phase = sigMembersAddPick
	s.usersLoading = true
	s.Update(sigUsersLoadedMsg{users: []omsapi.User{{ID: 8, Username: "grace"}}})
	if s.usersLoading || !s.usersLoaded {
		t.Errorf("users-loaded should clear loading + set loaded")
	}
	if len(s.pickOptions) != 1 || s.pickOptions[0].id != 8 {
		t.Errorf("options not rebuilt from loaded users: %+v", s.pickOptions)
	}
}

// TestSIGMembers_CommitAddArms confirms selecting a user in the picker arms the
// add (adding flag + a command) without needing a live client.
func TestSIGMembers_CommitAddArms(t *testing.T) {
	s := NewSIGMembersScreen(Deps{}, 5, "W")
	s.phase = sigMembersAddPick
	s.usersLoaded = true
	s.users = []omsapi.User{{ID: 8, Username: "grace"}}
	s.applyUserFilter()
	s.pickCursor = 0
	_, cmd := s.updateAddPick(sigKey("enter"))
	if !s.adding || cmd == nil {
		t.Errorf("enter on a selected user should arm the add (adding=%v cmd=%v)", s.adding, cmd != nil)
	}
}

// TestSIGMembers_AddOptimisticallyExcludes is the regression for the codex-found
// stale-exclude window: after a successful add, the just-added user must be
// folded into `members` immediately so reopening the picker before the async
// members reload lands cannot offer (and re-add) them.
func TestSIGMembers_AddOptimisticallyExcludes(t *testing.T) {
	s := NewSIGMembersScreen(Deps{}, 5, "W")
	s.users = []omsapi.User{{ID: 8, Username: "grace"}, {ID: 9, Username: "linus"}}
	s.usersLoaded = true
	s.phase = sigMembersAddPick
	s.applyUserFilter()
	s.pickCursor = 0 // grace (id 8)

	// Commit the add: stashes pendingAdd + arms the request.
	s.updateAddPick(sigKey("enter"))
	if s.pendingAdd == nil || s.pendingAdd.ID != 8 {
		t.Fatalf("commitAdd should stash pendingAdd for the picked user")
	}

	// Simulate the add succeeding (without a live client).
	s.Update(sigMemberAddedMsg{})
	if !s.isMember(8) {
		t.Fatalf("added user should be optimistically present in members")
	}
	if s.pendingAdd != nil {
		t.Errorf("pendingAdd should be cleared after success")
	}

	// Reopening the picker must exclude the just-added user but still offer others.
	s.phase = sigMembersAddPick
	s.applyUserFilter()
	for _, o := range s.pickOptions {
		if o.id == 8 {
			t.Errorf("just-added user must not be offered again")
		}
	}
	sawOther := false
	for _, o := range s.pickOptions {
		if o.id == 9 {
			sawOther = true
		}
	}
	if !sawOther {
		t.Errorf("other non-members should still be offered")
	}
}

// TestSIGMembers_RenderSmoke covers the list, empty, add-pick and error paths.
func TestSIGMembers_RenderSmoke(t *testing.T) {
	s := NewSIGMembersScreen(Deps{}, 5, "Woodshop")
	s.loading = false
	s.terminalHeight = 30
	s.members = []omsapi.SIGMember{{ID: 7, Username: "ada", Handle: "ada_l", Email: "a@ex.org", IsSIGAdmin: true}}
	s.windowSize = s.computeWindowSize()
	if out := s.View(); !strings.Contains(out, "ada") || !strings.Contains(out, "admin") {
		t.Errorf("list view missing member/admin badge: %q", out)
	}

	// empty
	s.members = nil
	if out := s.View(); !strings.Contains(out, "No members") {
		t.Errorf("empty view = %q", out)
	}

	// add-pick, loading
	s.phase = sigMembersAddPick
	s.usersLoading = true
	if out := s.View(); !strings.Contains(out, "Loading users") {
		t.Errorf("add-pick loading view = %q", out)
	}

	// add-pick, all users already members
	s.usersLoading = false
	s.usersLoaded = true
	s.users = nil
	s.applyUserFilter()
	if out := s.View(); !strings.Contains(out, "already members") {
		t.Errorf("add-pick empty view = %q", out)
	}
}
