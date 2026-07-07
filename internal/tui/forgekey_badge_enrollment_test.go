package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/uid0/scantty/internal/forgekeyapi"
	"github.com/uid0/scantty/internal/omsapi"
)

func badgeUser(id int, name, username string, badge *string) omsapi.User {
	return omsapi.User{ID: id, Username: username, DisplayName: name, BadgeNumber: badge}
}

// TestBadgeEnrollment_FacilitiesEOpensScreen is the wiring guard: pressing E on the
// Facilities menu must reach the badge-enrollment screen, not be shadowed by a
// global/nav hotkey (there is no global E, so it falls through to the screen).
func TestBadgeEnrollment_FacilitiesEOpensScreen(t *testing.T) {
	r := newTestRoot(NewFacilitiesScreen(Deps{}))
	next, cmd := r.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("E")})
	r = next.(Root)
	if cmd == nil {
		t.Fatal("E produced no command — a global/nav hotkey shadowed the Facilities item")
	}
	msg := cmd()
	sm, ok := msg.(SwitchScreenMsg)
	if !ok {
		t.Fatalf("E cmd yielded %T, want SwitchScreenMsg (the global layer likely stole E)", msg)
	}
	next2, _ := r.Update(sm)
	r = next2.(Root)
	if _, ok := r.screen.(*BadgeEnrollmentScreen); !ok {
		t.Fatalf("after E the active screen is %T, want *BadgeEnrollmentScreen", r.screen)
	}
}

// TestBadgeEnrollment_ClaimsCollidingKeys confirms the screen claims e/s// (which
// collide with global e=e-Paper, nav s=scan, global /=palette) and nothing else.
func TestBadgeEnrollment_ClaimsCollidingKeys(t *testing.T) {
	s := NewBadgeEnrollmentScreen(Deps{})
	for _, k := range []string{"e", "s", "/"} {
		if !s.HandlesKey(k) {
			t.Errorf("HandlesKey(%q) = false, want true (collides with a global)", k)
		}
	}
	for _, k := range []string{"x", "r", "j", "k", "n"} {
		if s.HandlesKey(k) {
			t.Errorf("HandlesKey(%q) = true, want false (globally free — should reach via fallthrough)", k)
		}
	}
}

// TestBadgeEnrollment_WantsRawInputOnlyInSubmodes confirms the list mode leaves
// global hotkeys live, and every interactive sub-mode swallows keys.
func TestBadgeEnrollment_WantsRawInputOnlyInSubmodes(t *testing.T) {
	s := NewBadgeEnrollmentScreen(Deps{})
	if s.WantsRawInput() {
		t.Error("list mode should not want raw input")
	}
	for _, mode := range []badgeMode{badgeModeSearch, badgeModeSetInput, badgeModeClearConfirm, badgeModeEnroll} {
		s.mode = mode
		if !s.WantsRawInput() {
			t.Errorf("mode %d should want raw input", mode)
		}
	}
}

// TestBadgeEnrollment_ClearNeedsBadge: x on a member without a badge is a no-op
// warn (no confirm); on a member with one it opens the clear confirm.
func TestBadgeEnrollment_ClearNeedsBadge(t *testing.T) {
	s := NewBadgeEnrollmentScreen(Deps{})
	s.loading = false
	s.all = []omsapi.User{
		badgeUser(1, "No Badge", "nobadge", nil),
		badgeUser(2, "Has Badge", "hasbadge", strptr("ABC123")),
	}
	s.applyFilter()

	s.cursor = 0
	if _, _ = s.updateList(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")}); s.mode != badgeModeList {
		t.Errorf("x on a badge-less member opened mode %d, want it to stay list", s.mode)
	}

	s.cursor = 1
	s.updateList(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	if s.mode != badgeModeClearConfirm {
		t.Errorf("x on a badged member gave mode %d, want clear-confirm", s.mode)
	}
}

// TestBadgeEnrollment_BlankSetClears verifies the manual-entry decision: a blank
// value clears (nil), a value sets. This is the web's `value || null`.
func TestBadgeEnrollment_BlankSetClears(t *testing.T) {
	if p := badgeInputToPtr("   "); p != nil {
		t.Errorf("blank input = %q, want nil (clear)", *p)
	}
	if p := badgeInputToPtr("  UID-9  "); p == nil || *p != "UID-9" {
		t.Errorf("value input = %v, want trimmed UID-9", p)
	}
}

// TestBadgeEnrollment_FilterIgnoresBadgeUID confirms the member filter matches on
// name/username/email only — never the badge UID (mirrors the web search + avoids
// making the credential a lookup key).
func TestBadgeEnrollment_FilterIgnoresBadgeUID(t *testing.T) {
	s := NewBadgeEnrollmentScreen(Deps{})
	s.loading = false
	s.all = []omsapi.User{
		badgeUser(1, "Ada Lovelace", "ada", strptr("SECRETUID")),
		badgeUser(2, "Bob Stone", "bob", nil),
	}
	s.filter = "secretuid"
	s.applyFilter()
	if len(s.rows) != 0 {
		t.Errorf("filtering by the badge UID matched %d rows, want 0 (UID is not a search key)", len(s.rows))
	}
	s.filter = "ada"
	s.applyFilter()
	if len(s.rows) != 1 {
		t.Errorf("filtering by name matched %d rows, want 1", len(s.rows))
	}
}

// TestBadgeEnrollment_PollCapturedUpdatesRow drives the arm→poll state machine: a
// captured scan flips to captured, records the UID, and updates the member row.
func TestBadgeEnrollment_PollCapturedUpdatesRow(t *testing.T) {
	s := NewBadgeEnrollmentScreen(Deps{})
	s.loading = false
	s.all = []omsapi.User{badgeUser(7, "Grace", "grace", nil)}
	s.applyFilter()
	s.mode = badgeModeEnroll
	s.enrollUserID = 7
	s.enrollStatus = badgeWaiting

	s.onPolled(badgePolledMsg{
		forUser: 7,
		state: &forgekeyapi.BadgeEnrollmentState{
			Armed:    true,
			Captured: &forgekeyapi.BadgeCapture{UserID: 7, BadgeNumber: "CAFED00D"},
		},
	})
	if s.enrollStatus != badgeCaptured {
		t.Fatalf("status = %d, want captured", s.enrollStatus)
	}
	if s.enrollBadge != "CAFED00D" {
		t.Errorf("captured badge = %q, want CAFED00D", s.enrollBadge)
	}
	if s.rows[0].BadgeNumber == nil || *s.rows[0].BadgeNumber != "CAFED00D" {
		t.Errorf("member row badge = %v, want it updated to CAFED00D", s.rows[0].BadgeNumber)
	}
}

// TestBadgeEnrollment_PollUnarmedExpires: once the armed record lapses, the poll
// resolves to expired (no infinite loop).
func TestBadgeEnrollment_PollUnarmedExpires(t *testing.T) {
	s := NewBadgeEnrollmentScreen(Deps{})
	s.mode = badgeModeEnroll
	s.enrollUserID = 7
	s.enrollStatus = badgeWaiting
	s.onPolled(badgePolledMsg{forUser: 7, state: &forgekeyapi.BadgeEnrollmentState{Armed: false}})
	if s.enrollStatus != badgeExpired {
		t.Errorf("status = %d, want expired", s.enrollStatus)
	}
}

// TestBadgeEnrollment_PollIgnoresStale: a tick for a different (already-cancelled)
// user must not mutate the current state.
func TestBadgeEnrollment_PollIgnoresStale(t *testing.T) {
	s := NewBadgeEnrollmentScreen(Deps{})
	s.mode = badgeModeEnroll
	s.enrollUserID = 7
	s.enrollStatus = badgeWaiting
	s.onPolled(badgePolledMsg{forUser: 99, state: &forgekeyapi.BadgeEnrollmentState{Armed: false}})
	if s.enrollStatus != badgeWaiting {
		t.Errorf("a stale poll for another user changed status to %d", s.enrollStatus)
	}
}

// TestBadgeEnrollment_RowShowsBadgeLikeWeb documents the mirror-the-web decision:
// the enrollment screen shows the badge UID in full (the web renders it as a Badge
// chip), and "none" when unset.
func TestBadgeEnrollment_RowShowsBadgeLikeWeb(t *testing.T) {
	s := NewBadgeEnrollmentScreen(Deps{})
	s.loading = false
	s.all = []omsapi.User{
		badgeUser(1, "Ada", "ada", strptr("BADGE-42")),
		badgeUser(2, "Bob", "bob", nil),
	}
	s.applyFilter()

	if got := s.renderRow(0); !strings.Contains(got, "BADGE-42") {
		t.Errorf("row for a badged member = %q, want it to show the UID (web shows it)", got)
	}
	if got := s.renderRow(1); !strings.Contains(got, "none") {
		t.Errorf("row for a badge-less member = %q, want 'none'", got)
	}
}
