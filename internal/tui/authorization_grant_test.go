package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/uid0/scantty/internal/forgekeyapi"
	"github.com/uid0/scantty/internal/omsapi"
)

// TestAuthGrant_BuildPayload confirms the grant sends exactly {asset, user, notes}
// and requires both FKs.
func TestAuthGrant_BuildPayload(t *testing.T) {
	s := NewAuthorizationGrantScreen(Deps{})
	if _, err := s.buildPayload(); err == nil {
		t.Error("expected error when asset/user unset")
	}
	s.assetID = strptr("asset-uuid-1")
	if _, err := s.buildPayload(); err == nil {
		t.Error("expected error when user unset")
	}
	uid := 7
	s.userID = &uid
	s.notesInput.SetValue("  completed training  ")
	w, err := s.buildPayload()
	if err != nil {
		t.Fatalf("buildPayload: %v", err)
	}
	if w.Asset != "asset-uuid-1" || w.User != 7 {
		t.Errorf("payload = %+v, want asset-uuid-1 / 7", w)
	}
	if w.Notes != "completed training" {
		t.Errorf("notes = %q, want trimmed", w.Notes)
	}
}

// TestAuthGrant_PickCommits walks the asset + member pickers and confirms each
// commit records the FK (UUID string for asset, int for user).
func TestAuthGrant_PickCommits(t *testing.T) {
	s := NewAuthorizationGrantScreen(Deps{})
	s.loading = false
	s.assets = []omsapi.Asset{{ID: "a-1", Name: "Laser cutter"}}
	s.users = []omsapi.User{badgeUser(7, "Grace", "grace", nil)}

	s.openPicker(agAsset)
	s.pickCursor = 0
	s.commitPick()
	if s.assetID == nil || *s.assetID != "a-1" {
		t.Fatalf("asset pick = %v, want a-1", s.assetID)
	}

	s.openPicker(agUser)
	s.pickCursor = 0
	s.commitPick()
	if s.userID == nil || *s.userID != 7 {
		t.Fatalf("user pick = %v, want 7", s.userID)
	}
}

// TestAuthGrant_PickersHaveNoClearRow: both FKs are required, so neither picker
// offers a "(none)" row that could silently clear the selection.
func TestAuthGrant_PickersHaveNoClearRow(t *testing.T) {
	s := NewAuthorizationGrantScreen(Deps{})
	s.assets = []omsapi.Asset{{ID: "a-1", Name: "Laser"}}
	s.users = []omsapi.User{badgeUser(7, "Grace", "grace", nil)}
	for _, field := range []int{agAsset, agUser} {
		s.openPicker(field)
		for _, o := range s.pickOptions {
			if o.clear {
				t.Errorf("field %d picker offered a clear row — a required FK must not be clearable", field)
			}
		}
	}
}

func authRow(id int, active bool) forgekeyapi.Authorization {
	return forgekeyapi.Authorization{ID: id, IsActive: active, UserName: "ada", AssetName: "laser"}
}

// TestAuthorizations_XOpensRevokeConfirm: x on an active grant opens a y/n confirm
// (no more instant revoke), and the confirm swallows keys.
func TestAuthorizations_XOpensRevokeConfirm(t *testing.T) {
	s := NewAuthorizationsScreen(Deps{})
	s.loading = false
	s.rows = []forgekeyapi.Authorization{authRow(5, true)}
	s.cursor = 0
	s.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	if !s.confirmingRevoke {
		t.Fatal("x on an active grant did not open the revoke confirm")
	}
	if !s.WantsRawInput() {
		t.Error("the revoke confirm should want raw input so y/n land here")
	}
	// n cancels.
	s.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")})
	if s.confirmingRevoke {
		t.Error("n did not cancel the revoke confirm")
	}
}

// TestAuthorizations_RevokedRowSkipsConfirm: an already-revoked grant can't be
// re-revoked (no confirm opens).
func TestAuthorizations_RevokedRowSkipsConfirm(t *testing.T) {
	s := NewAuthorizationsScreen(Deps{})
	s.loading = false
	s.rows = []forgekeyapi.Authorization{authRow(5, false)}
	s.cursor = 0
	s.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	if s.confirmingRevoke {
		t.Error("x on an already-revoked grant opened a confirm")
	}
}

// TestAuthorizations_GrantRequiresStaff mirrors the web card's isStaff gate: a
// non-staff n is a warn (no navigation); a staff n opens the grant screen.
func TestAuthorizations_GrantRequiresStaff(t *testing.T) {
	nonStaff := NewAuthorizationsScreen(Deps{InitialStaff: false})
	nonStaff.loading = false
	if _, cmd := nonStaff.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")}); cmd == nil {
		t.Fatal("non-staff n produced no command (expected a warn status)")
	} else if _, ok := cmd().(SwitchScreenMsg); ok {
		t.Error("non-staff n navigated to the grant screen — must be staff-gated")
	}

	staff := NewAuthorizationsScreen(Deps{InitialStaff: true})
	staff.loading = false
	_, cmd := staff.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")})
	if cmd == nil {
		t.Fatal("staff n produced no command")
	}
	sm, ok := cmd().(SwitchScreenMsg)
	if !ok {
		t.Fatalf("staff n yielded %T, want SwitchScreenMsg", cmd())
	}
	if _, ok := sm.Screen.(*AuthorizationGrantScreen); !ok {
		t.Errorf("staff n opened %T, want *AuthorizationGrantScreen", sm.Screen)
	}
}

// TestAuthorizations_NReachesScreenNotNotifications is the wiring guard: HandlesKey
// must claim n so it doesn't open the global notifications screen.
func TestAuthorizations_NReachesScreenNotNotifications(t *testing.T) {
	r := newTestRoot(NewAuthorizationsScreen(Deps{}))
	after := press(t, r, "n")
	if _, ok := after.screen.(*NotificationsScreen); ok {
		t.Fatal("global n=notifications shadowed the grant key (HandlesKey not claimed)")
	}
	if _, ok := after.screen.(*AuthorizationsScreen); !ok {
		t.Fatalf("after n active screen is %T, want *AuthorizationsScreen", after.screen)
	}
}
