package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/uid0/scantty/internal/omsapi"
)

// sigKey builds a KeyMsg for the SIG-screen tests (shared with
// sig_members_test.go).
func sigKey(s string) tea.KeyMsg {
	switch s {
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

// TestSIGForm_BuildPayload walks the happy path: name + group_email map onto a
// SIGWrite (the complete writable set — a SIG has no other writable fields).
func TestSIGForm_BuildPayload(t *testing.T) {
	s := NewSIGFormScreen(Deps{}, "")
	s.inputs[sigfName].SetValue("Woodshop")
	s.inputs[sigfGroupEmail].SetValue("wood@ex.org")

	w, err := s.buildPayload()
	if err != nil {
		t.Fatalf("buildPayload: %v", err)
	}
	if w.Name != "Woodshop" {
		t.Errorf("name = %q", w.Name)
	}
	if w.GroupEmail != "wood@ex.org" {
		t.Errorf("group_email = %q", w.GroupEmail)
	}
}

// TestSIGForm_BuildPayloadTrimsAndAllowsBlankEmail confirms the name is trimmed
// and an empty email is allowed (the field is optional).
func TestSIGForm_BuildPayloadTrimsAndAllowsBlankEmail(t *testing.T) {
	s := NewSIGFormScreen(Deps{}, "")
	s.inputs[sigfName].SetValue("  Metalshop  ")
	// group_email left blank
	w, err := s.buildPayload()
	if err != nil {
		t.Fatalf("buildPayload: %v", err)
	}
	if w.Name != "Metalshop" {
		t.Errorf("name not trimmed: %q", w.Name)
	}
	if w.GroupEmail != "" {
		t.Errorf("group_email = %q, want empty", w.GroupEmail)
	}
}

// TestSIGForm_Validation covers name-required and the lightweight optional-email
// check (looser than Django's so it never rejects a server-valid address).
func TestSIGForm_Validation(t *testing.T) {
	s := NewSIGFormScreen(Deps{}, "")

	if _, err := s.buildPayload(); err == nil {
		t.Errorf("expected error for empty name")
	}
	s.inputs[sigfName].SetValue("SIG")

	// empty email validates
	if _, err := s.buildPayload(); err != nil {
		t.Errorf("empty email should validate: %v", err)
	}
	// gross-typo emails rejected
	for _, bad := range []string{"notanemail", "@ex.org", "ada@", "a b@ex.org"} {
		s.inputs[sigfGroupEmail].SetValue(bad)
		if _, err := s.buildPayload(); err == nil {
			t.Errorf("expected error for bad email %q", bad)
		}
	}
	// plausible emails accepted
	for _, good := range []string{"a@b", "sig@example.org", "team.wood@ex.co.uk"} {
		s.inputs[sigfGroupEmail].SetValue(good)
		if _, err := s.buildPayload(); err != nil {
			t.Errorf("email %q should validate: %v", good, err)
		}
	}
}

// TestSIGForm_Hydrate fills name + email from a fetched SIG.
func TestSIGForm_Hydrate(t *testing.T) {
	s := NewSIGFormScreen(Deps{}, "9")
	s.sig = &omsapi.SIG{ID: 9, Name: "Woodshop", GroupEmail: "wood@ex.org"}
	s.hydrate()
	if s.inputs[sigfName].Value() != "Woodshop" {
		t.Errorf("name = %q", s.inputs[sigfName].Value())
	}
	if s.inputs[sigfGroupEmail].Value() != "wood@ex.org" {
		t.Errorf("email = %q", s.inputs[sigfGroupEmail].Value())
	}
}

// TestSIGForm_RenderSmoke guards the create-mode form render path.
func TestSIGForm_RenderSmoke(t *testing.T) {
	s := NewSIGFormScreen(Deps{}, "")
	s.terminalHeight = 30
	out := s.View()
	if !strings.Contains(out, "Name") || !strings.Contains(out, "Group email") {
		t.Errorf("form view missing fields: %q", out)
	}
}

// TestSIGList_HandlesKey confirms only the two colliding action keys (n, G) are
// claimed; E / x / v / enter / j fall through to the root's normal dispatch.
func TestSIGList_HandlesKey(t *testing.T) {
	s := NewSIGListScreen(Deps{})
	for _, k := range []string{"n", "G"} {
		if !s.HandlesKey(k) {
			t.Errorf("HandlesKey(%q) = false, want true", k)
		}
	}
	for _, k := range []string{"x", "E", "v", "enter", "j", "s"} {
		if s.HandlesKey(k) {
			t.Errorf("HandlesKey(%q) = true, want false", k)
		}
	}
}

// TestSIGList_DeleteConfirm confirms x arms the confirmation (raw input on) and n
// cancels it (raw input off).
func TestSIGList_DeleteConfirm(t *testing.T) {
	s := NewSIGListScreen(Deps{})
	s.loading = false
	s.rows = []omsapi.SIG{{ID: 1, Name: "A", MemberCount: 3, AssetCount: 2}}
	if s.WantsRawInput() {
		t.Fatalf("should not want raw input before confirming")
	}
	s.Update(sigKey("x"))
	if !s.confirmingDelete || !s.WantsRawInput() {
		t.Errorf("x should arm delete confirmation + raw input")
	}
	// The confirm prompt surfaces the unlink impact.
	if out := s.View(); !strings.Contains(out, "unlinked") {
		t.Errorf("confirm view should note the unlink impact: %q", out)
	}
	s.Update(sigKey("n"))
	if s.confirmingDelete || s.WantsRawInput() {
		t.Errorf("n should cancel the confirmation")
	}
}

// TestSIGList_EnterOpensMembers confirms enter drills into member management for
// the selected SIG.
func TestSIGList_EnterOpensMembers(t *testing.T) {
	s := NewSIGListScreen(Deps{})
	s.loading = false
	s.rows = []omsapi.SIG{{ID: 5, Name: "Woodshop"}}
	s.cursor = 0
	_, cmd := s.Update(sigKey("enter"))
	if cmd == nil {
		t.Fatalf("enter should return a switch command")
	}
	sw, ok := cmd().(SwitchScreenMsg)
	if !ok {
		t.Fatalf("expected SwitchScreenMsg, got %T", cmd())
	}
	if sw.Workspace != WSSIGs {
		t.Errorf("workspace = %v", sw.Workspace)
	}
	ms, ok := sw.Screen.(*SIGMembersScreen)
	if !ok {
		t.Fatalf("expected *SIGMembersScreen, got %T", sw.Screen)
	}
	if ms.sigID != 5 || ms.sigName != "Woodshop" {
		t.Errorf("members screen scoped wrong: id=%d name=%q", ms.sigID, ms.sigName)
	}
}

// TestSIGList_NewEditView confirms n opens a create form, E an edit form (scoped
// to the row), and v the read-only detail.
func TestSIGList_NewEditView(t *testing.T) {
	s := NewSIGListScreen(Deps{})
	s.loading = false
	s.rows = []omsapi.SIG{{ID: 7, Name: "Metal"}}
	s.cursor = 0

	// n → create form
	_, cmd := s.Update(sigKey("n"))
	sw := cmd().(SwitchScreenMsg)
	fs, ok := sw.Screen.(*SIGFormScreen)
	if !ok || fs.edit {
		t.Fatalf("n should open a create SIGFormScreen, got %T edit=%v", sw.Screen, ok && fs.edit)
	}

	// E → edit form scoped to id 7
	_, cmd = s.Update(sigKey("E"))
	sw = cmd().(SwitchScreenMsg)
	fs, ok = sw.Screen.(*SIGFormScreen)
	if !ok || !fs.edit || fs.sigID != 7 {
		t.Fatalf("E should open an edit SIGFormScreen for id 7, got %T", sw.Screen)
	}

	// v → read-only detail
	_, cmd = s.Update(sigKey("v"))
	sw = cmd().(SwitchScreenMsg)
	if _, ok := sw.Screen.(*SIGDetailScreen); !ok {
		t.Fatalf("v should open a SIGDetailScreen, got %T", sw.Screen)
	}
}

// TestSIGList_RenderSmoke covers the loaded / empty / error render paths.
func TestSIGList_RenderSmoke(t *testing.T) {
	s := NewSIGListScreen(Deps{})
	s.loading = false
	s.terminalHeight = 30
	s.rows = []omsapi.SIG{{ID: 1, Name: "Woodshop", GroupEmail: "w@ex.org", MemberCount: 4, IsUserAdmin: true}}
	s.windowSize = s.computeWindowSize()
	out := s.View()
	if !strings.Contains(out, "Woodshop") || !strings.Contains(out, "you admin") {
		t.Errorf("list view missing name/admin badge: %q", out)
	}

	s.rows = nil
	if out := s.View(); !strings.Contains(out, "No SIGs") {
		t.Errorf("empty view = %q", out)
	}
	s.loadErr = "boom"
	if out := s.View(); !strings.Contains(out, "boom") {
		t.Errorf("error view = %q", out)
	}
}
