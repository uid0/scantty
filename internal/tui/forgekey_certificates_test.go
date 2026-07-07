package tui

import (
	"errors"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/uid0/scantty/internal/forgekeyapi"
)

func fkTestCAs() []forgekeyapi.CertificateAuthority {
	active, revoked := 7, 2
	return []forgekeyapi.CertificateAuthority{
		{
			Name:              "forgekey-root",
			CommonName:        "ForgeKey Internal Root CA",
			FingerprintSHA256: "aa11bb22ccddeeff00",
			NotBefore:         time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
			NotAfter:          time.Date(2036, 1, 1, 0, 0, 0, 0, time.UTC),
			IsActive:          true,
			ActiveCertCount:   &active,
			RevokedCertCount:  &revoked,
		},
		{Name: "forgekey-root", IsActive: false}, // retired
	}
}

func fkTestCerts() []forgekeyapi.DeviceCertificate {
	revokedAt := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
	return []forgekeyapi.DeviceCertificate{
		{
			DeviceChipID:      "ESP-CHIP-001",
			Serial:            "0A1B2C",
			Subject:           "CN=ESP-CHIP-001",
			FingerprintSHA256: "deadbeefcafe",
			NotAfter:          time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC),
			IssuedBy:          "forgekey-root",
			Status:            "active",
		},
		{
			DeviceChipID:      "ESP-CHIP-002",
			Serial:            "0A1B2D",
			Subject:           "CN=ESP-CHIP-002",
			FingerprintSHA256: "0badf00d",
			NotAfter:          time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
			IssuedBy:          "forgekey-root",
			RevokedAt:         &revokedAt,
			Status:            "revoked",
		},
	}
}

// HandlesKey claims only G (which collides with the global Category surface);
// R / r / j fall through to the screen via the normal dispatch and must NOT be
// claimed here.
func TestFKCerts_HandlesKey(t *testing.T) {
	s := NewForgeKeyCertificatesScreen(Deps{})
	if !s.HandlesKey("G") {
		t.Error("HandlesKey(G) = false, want true (scroll-to-bottom vs global categories)")
	}
	for _, k := range []string{"R", "r", "j", "k", "n"} {
		if s.HandlesKey(k) {
			t.Errorf("HandlesKey(%q) = true, want false", k)
		}
	}
}

// R opens the rotate confirm (and flips on raw input so y/n/esc land here);
// n cancels it back to the normal view.
func TestFKCerts_RotateConfirmCancel(t *testing.T) {
	s := NewForgeKeyCertificatesScreen(Deps{})
	s.Update(fkCertsLoadedMsg{cas: fkTestCAs(), certs: fkTestCerts()})
	if s.WantsRawInput() {
		t.Fatal("WantsRawInput should be false before confirming")
	}
	s.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("R")})
	if !s.confirmingRotate || !s.WantsRawInput() {
		t.Fatalf("after R: confirmingRotate=%v rawInput=%v, want both true", s.confirmingRotate, s.WantsRawInput())
	}
	if !strings.Contains(s.View(), "Rotate the root CA?") {
		t.Error("confirm view should ask to rotate the root CA")
	}
	s.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")})
	if s.confirmingRotate || s.WantsRawInput() {
		t.Errorf("after n: confirmingRotate=%v rawInput=%v, want both false", s.confirmingRotate, s.WantsRawInput())
	}
}

// A successful rotation clears the confirm, triggers a reload, and flashes the
// web's verbatim success message; a failure clears the confirm without reloading.
func TestFKCerts_RotateResultHandling(t *testing.T) {
	s := NewForgeKeyCertificatesScreen(Deps{})
	s.loading = false // simulate the initial load already completed
	s.confirmingRotate = true
	s.rotating = true
	_, cmd := s.Update(fkCaRotatedMsg{})
	if s.confirmingRotate || s.rotating {
		t.Errorf("after success: confirmingRotate=%v rotating=%v, want both false", s.confirmingRotate, s.rotating)
	}
	if !s.loading {
		t.Error("success should set loading=true to reload the lists")
	}
	if cmd == nil {
		t.Error("success should return a cmd (status + reload)")
	}

	s2 := NewForgeKeyCertificatesScreen(Deps{})
	s2.loading = false // simulate the initial load already completed
	s2.confirmingRotate = true
	s2.rotating = true
	_, cmd2 := s2.Update(fkCaRotatedMsg{err: errors.New("boom")})
	if s2.confirmingRotate || s2.rotating {
		t.Errorf("after error: confirmingRotate=%v rotating=%v, want both false", s2.confirmingRotate, s2.rotating)
	}
	if s2.loading {
		t.Error("error should NOT set loading (no reload)")
	}
	if cmd2 == nil {
		t.Error("error should still return a status cmd")
	}
}

// While a rotation is in flight, keypresses are ignored so a double-y can't
// fire two rotations.
func TestFKCerts_IgnoresKeysWhileRotating(t *testing.T) {
	s := NewForgeKeyCertificatesScreen(Deps{})
	s.confirmingRotate = true
	s.rotating = true
	_, cmd := s.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	if cmd != nil {
		t.Error("a keypress while rotating should be a no-op")
	}
}

func TestFKCerts_ActiveAndRetired(t *testing.T) {
	s := NewForgeKeyCertificatesScreen(Deps{})
	s.Update(fkCertsLoadedMsg{cas: fkTestCAs(), certs: nil})
	ca := s.activeCA()
	if ca == nil || ca.CommonName != "ForgeKey Internal Root CA" {
		t.Fatalf("activeCA() = %+v, want the active root", ca)
	}
	if n := s.retiredCount(); n != 1 {
		t.Errorf("retiredCount() = %d, want 1", n)
	}

	// No active CA → the bootstrap hint, no active-CA card fields.
	s2 := NewForgeKeyCertificatesScreen(Deps{})
	s2.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	s2.Update(fkCertsLoadedMsg{cas: nil, certs: nil})
	if s2.activeCA() != nil {
		t.Error("activeCA() should be nil with no CAs")
	}
	if !strings.Contains(s2.View(), "No active CA") {
		t.Error("view should show the no-active-CA bootstrap hint")
	}
}

// The render shows every PUBLIC field the web table shows — and NONE of the
// key material a PKI surface must never leak. The structs carry no key bytes,
// so this is a guard against a future field being added and rendered.
func TestFKCerts_RenderPublicFieldsNoKeyMaterial(t *testing.T) {
	s := NewForgeKeyCertificatesScreen(Deps{})
	s.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	s.Update(fkCertsLoadedMsg{cas: fkTestCAs(), certs: fkTestCerts()})
	view := s.View()

	for _, want := range []string{
		"ForgeKey Internal Root CA", // CA common name
		"aa11bb22ccddeeff00",        // CA fingerprint
		"7 active · 2 revoked",      // cert counts
		"1 retired CA",              // retired history
		"ESP-CHIP-001",              // device chip id
		"0A1B2C",                    // serial
		"deadbeefcafe",              // device cert fingerprint
		"active",                    // status label (valid cert)
		"revoked",                   // status label (revoked cert)
		"R rotate root CA",          // footer action hint
	} {
		if !strings.Contains(view, want) {
			t.Errorf("view missing expected public field %q", want)
		}
	}

	lower := strings.ToLower(view)
	for _, forbidden := range []string{
		"private key",
		"begin certificate",
		"begin ",
		"-----",
		"cert_pem",
		"encrypted_private_key",
		"key_kid",
	} {
		if strings.Contains(lower, forbidden) {
			t.Errorf("view leaked forbidden key material marker %q", forbidden)
		}
	}
}

// A long cert list must window so the CA card and the footer hotkeys never
// clip off the bottom of the body budget.
func TestFKCerts_WindowFitsBudgetAndKeepsFooter(t *testing.T) {
	s := NewForgeKeyCertificatesScreen(Deps{})
	const height = 24
	s.Update(tea.WindowSizeMsg{Width: 100, Height: height})

	certs := make([]forgekeyapi.DeviceCertificate, 50)
	for i := range certs {
		certs[i] = forgekeyapi.DeviceCertificate{
			DeviceChipID:      "ESP-CHIP",
			Serial:            "SER",
			FingerprintSHA256: "fp",
			NotAfter:          time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC),
			Status:            "active",
		}
	}
	s.Update(fkCertsLoadedMsg{cas: fkTestCAs(), certs: certs})

	view := s.View()
	lines := strings.Count(view, "\n") + 1
	if budget := screenBodyHeight(height); lines > budget {
		t.Errorf("view is %d lines, exceeds body budget %d — would clip", lines, budget)
	}
	if !strings.Contains(view, "R rotate root CA") {
		t.Error("footer hotkeys clipped by an over-long cert table")
	}
	if !strings.Contains(view, "more below") {
		t.Error("expected an overflow hint when the cert table is windowed")
	}

	// Scrolling to the bottom keeps it within budget and surfaces an 'above' hint.
	s.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("G")})
	bottom := s.View()
	if l := strings.Count(bottom, "\n") + 1; l > screenBodyHeight(height) {
		t.Errorf("bottom view is %d lines, exceeds budget", l)
	}
	if !strings.Contains(bottom, "above") {
		t.Error("expected an 'above' hint after scrolling to the bottom")
	}
}
