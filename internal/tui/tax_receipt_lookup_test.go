package tui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/uid0/scantty/internal/omsapi"
)

const taxReceiptBody = `{
	"id":"11111111-2222-3333-4444-555555555555",
	"serial_number":"66666666-7777-8888-9999-000000000000",
	"donation":"a1b2c3d4-0000-4444-8888-abcdef012345",
	"donation_number":"D-0001","donor_name":"Ada Lovelace",
	"donor_email":"ada@example.com",
	"issued_date":"2026-06-02","issued_by":7,"issued_by_username":"clerk",
	"pdf_file":"/media/receipts/1.pdf","is_copy":false,
	"created_at":"2026-01-02T03:04:05Z","updated_at":"2026-01-02T03:04:05Z"
}`

func taxReceiptTestServer(t *testing.T) (*httptest.Server, *string) {
	t.Helper()
	var gotSerial string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotSerial = r.URL.Query().Get("serial_number")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(taxReceiptBody))
	}))
	t.Cleanup(srv.Close)
	return srv, &gotSerial
}

// TestTaxReceiptLookup_SubmitRendersReceipt is the load-bearing flow: type a
// serial, press enter, and the fetched receipt's data (the fields the web page
// shows plus the serializer's other read fields) renders — along with the
// web-only PDF caveat instead of a fake download.
func TestTaxReceiptLookup_SubmitRendersReceipt(t *testing.T) {
	srv, gotSerial := taxReceiptTestServer(t)
	deps := Deps{OMS: omsapi.New(srv.URL), Ctx: context.Background()}
	s := NewTaxReceiptLookupScreen(deps)

	s.input.SetValue("66666666-7777-8888-9999-000000000000")
	next, cmd := s.Update(tea.KeyMsg{Type: tea.KeyEnter})
	s = next.(*TaxReceiptLookupScreen)
	if !s.pending || cmd == nil {
		t.Fatal("enter with a serial should start a pending lookup")
	}
	next, _ = s.Update(cmd())
	s = next.(*TaxReceiptLookupScreen)

	if *gotSerial != "66666666-7777-8888-9999-000000000000" {
		t.Errorf("backend serial_number = %q, want the typed serial", *gotSerial)
	}
	if s.pending || s.receipt == nil {
		t.Fatalf("after the response, pending=%v receipt=%v, want done+populated", s.pending, s.receipt)
	}
	view := s.View()
	for _, want := range []string{
		"66666666-7777-8888-9999-000000000000", // serial
		"Ada Lovelace",                          // donor
		"ada@example.com",                       // donor email
		"D-0001",                                // donation number
		"2026-06-02",                            // issued date
		"clerk",                                 // issuing clerk
		"web app only",                          // PDF caveat
	} {
		if !strings.Contains(view, want) {
			t.Errorf("view missing %q\n---\n%s", want, view)
		}
	}
}

// TestTaxReceiptLookup_NotFound shows a clean "not found" (not a raw error) when
// the serial matches nothing, driven off the backend's 404.
func TestTaxReceiptLookup_NotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"Tax receipt not found"}`))
	}))
	defer srv.Close()
	deps := Deps{OMS: omsapi.New(srv.URL), Ctx: context.Background()}
	s := NewTaxReceiptLookupScreen(deps)

	s.input.SetValue("nope")
	next, cmd := s.Update(tea.KeyMsg{Type: tea.KeyEnter})
	s = next.(*TaxReceiptLookupScreen)
	next, _ = s.Update(cmd())
	s = next.(*TaxReceiptLookupScreen)

	if !s.notFound || s.receipt != nil {
		t.Fatalf("notFound=%v receipt=%v, want notFound with no receipt", s.notFound, s.receipt)
	}
	if s.lastErr != "" {
		t.Errorf("lastErr = %q, want empty (a 404 is not-found, not an error)", s.lastErr)
	}
	if !strings.Contains(s.View(), "Tax receipt not found") {
		t.Errorf("view should say the receipt was not found:\n%s", s.View())
	}
}

// TestTaxReceiptLookup_BlankSerialNoRequest guards against firing an empty
// lookup: a blank submit is a no-op with a hint (mirrors the web's client-side
// "Please enter a serial number").
func TestTaxReceiptLookup_BlankSerialNoRequest(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		_, _ = w.Write([]byte(taxReceiptBody))
	}))
	defer srv.Close()
	deps := Deps{OMS: omsapi.New(srv.URL), Ctx: context.Background()}
	s := NewTaxReceiptLookupScreen(deps)

	s.input.SetValue("   ") // whitespace only
	next, cmd := s.Update(tea.KeyMsg{Type: tea.KeyEnter})
	s = next.(*TaxReceiptLookupScreen)
	if cmd != nil {
		t.Fatal("a blank serial should not fire a lookup command")
	}
	if s.pending {
		t.Error("a blank serial should not enter the pending state")
	}
	if called {
		t.Error("a blank serial must not hit the backend")
	}
	if !strings.Contains(s.View(), "enter a serial number") {
		t.Errorf("view should hint to enter a serial number:\n%s", s.View())
	}
}

// TestTaxReceiptLookup_StaleResponseIgnored proves the serial guard: a slow
// response from a superseded lookup (including one that could land after the
// screen was left and reopened) must not overwrite the current query's state.
// The server echoes the requested serial into donor_name so the test can prove
// exactly which response populated the screen.
func TestTaxReceiptLookup_StaleResponseIgnored(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		serial := r.URL.Query().Get("serial_number")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"id-` + serial + `","serial_number":"` + serial +
			`","donation":"d","donation_number":"D-1","donor_name":"donor-for-` + serial +
			`","issued_date":"2026-06-02"}`))
	}))
	defer srv.Close()
	deps := Deps{OMS: omsapi.New(srv.URL), Ctx: context.Background()}
	s := NewTaxReceiptLookupScreen(deps)

	// Submit "first", capturing its in-flight command without resolving it.
	s.input.SetValue("first")
	next, cmd1 := s.Update(tea.KeyMsg{Type: tea.KeyEnter})
	s = next.(*TaxReceiptLookupScreen)

	// A newer submit for "second" supersedes it before "first"'s response lands.
	s.input.SetValue("second")
	next, cmd2 := s.Update(tea.KeyMsg{Type: tea.KeyEnter})
	s = next.(*TaxReceiptLookupScreen)

	// Feeding "first"'s (now stale) response must be dropped — the input reads
	// "second", so it doesn't match.
	next, _ = s.Update(cmd1())
	s = next.(*TaxReceiptLookupScreen)
	if s.receipt != nil {
		t.Fatalf("stale response leaked through: receipt=%+v", s.receipt)
	}

	// "second"'s response is the current one and populates the screen.
	next, _ = s.Update(cmd2())
	s = next.(*TaxReceiptLookupScreen)
	if s.receipt == nil || s.receipt.DonorName != "donor-for-second" {
		t.Fatalf("current response should populate with the second lookup: %+v", s.receipt)
	}
}

// TestTaxReceiptLookup_ForeignSerialDropped locks the in-flight-serial guard
// directly: while a lookup for "B" is outstanding, a response tagged with a
// different serial "A" (a stale command from a superseded query or a prior
// screen instance) must be dropped without populating the screen or clearing
// the pending indicator for the request we're actually waiting on.
func TestTaxReceiptLookup_ForeignSerialDropped(t *testing.T) {
	s := NewTaxReceiptLookupScreen(Deps{})
	s.pending = true
	s.pendingSerial = "B" // a lookup for "B" is in flight

	next, _ := s.Update(taxReceiptLookupMsg{
		serial:  "A", // a response for a different (earlier) serial
		receipt: &omsapi.TaxReceipt{DonorName: "stale"},
	})
	s = next.(*TaxReceiptLookupScreen)
	if s.receipt != nil {
		t.Fatalf("a response for a non-current serial must be dropped, got %+v", s.receipt)
	}
	if !s.pending {
		t.Fatal("a dropped foreign response must not clear the pending indicator for the in-flight lookup")
	}
}

// TestTaxReceiptLookup_EscReturnsToSettings — esc leaves the lookup for the
// Settings screen it was launched from.
func TestTaxReceiptLookup_EscReturnsToSettings(t *testing.T) {
	s := NewTaxReceiptLookupScreen(Deps{})
	_, cmd := s.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if cmd == nil {
		t.Fatal("esc should switch away from the lookup")
	}
	sw, ok := cmd().(SwitchScreenMsg)
	if !ok {
		t.Fatalf("esc produced %T, want SwitchScreenMsg", cmd())
	}
	if sw.Workspace != WSSettings {
		t.Errorf("esc workspace = %v, want settings", sw.Workspace)
	}
	if _, ok := sw.Screen.(*SettingsScreen); !ok {
		t.Fatalf("esc target = %T, want *SettingsScreen", sw.Screen)
	}
}

// TestSettingsTOpensTaxReceiptLookup locks the wiring: pressing 't' on the
// Settings screen opens the tax receipt lookup (and 't' is not shadowed by a
// global hotkey).
func TestSettingsTOpensTaxReceiptLookup(t *testing.T) {
	r := newTestRoot(NewSettingsScreen(Deps{}))
	next, cmd := r.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("t")})
	r = next.(Root)
	if cmd == nil {
		t.Fatal("pressing t on Settings should open the tax receipt lookup")
	}
	sw, ok := cmd().(SwitchScreenMsg)
	if !ok {
		t.Fatalf("t on Settings produced %T, want SwitchScreenMsg", cmd())
	}
	if sw.Workspace != WSSettings {
		t.Errorf("switch workspace = %v, want settings", sw.Workspace)
	}
	if _, ok := sw.Screen.(*TaxReceiptLookupScreen); !ok {
		t.Fatalf("switch target = %T, want *TaxReceiptLookupScreen", sw.Screen)
	}
	// The root installs it as the active screen.
	next, _ = r.Update(sw)
	r = next.(Root)
	if _, ok := r.screen.(*TaxReceiptLookupScreen); !ok {
		t.Fatalf("after switch, active screen = %T, want *TaxReceiptLookupScreen", r.screen)
	}
}
