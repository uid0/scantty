package tui

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/uid0/scantty/internal/omsapi"
)

// Optional supplier purchase/pricing agreement on PO create (op-yoos).
//
// The whole feature turns on one asymmetry: nearly every purchase order cites
// NO agreement, so the picker must cost the common path nothing — no phase to
// dismiss, no request to wait on — while still being there, and re-editable,
// for the supplier who does have contract pricing on file.

const poTwoAgreements = `
	{"id":4,"supplier":7,"supplier_name":"Acme Supply","name":"2026 nonprofit pricing",
	 "notes":"15% off list, net 30","is_active":true},
	{"id":9,"supplier":7,"supplier_name":"Acme Supply","name":"Standing quote Q3",
	 "notes":"","is_active":true}`

// poAgreementSrv serves the agreement list (from the raw `results` rows given)
// plus the PO create endpoint, capturing the POSTed body so a test can assert
// what the order actually carried.
func poAgreementSrv(t *testing.T, results string) (*httptest.Server, *map[string]any) {
	t.Helper()
	body := map[string]any{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(r.URL.Path, "supplier-agreements") {
			_, _ = w.Write([]byte(`{"count":0,"next":null,"previous":null,"results":[` + results + `]}`))
			return
		}
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		_, _ = w.Write([]byte(`{"id":1,"po_number":"PO-2026-0009"}`))
	}))
	t.Cleanup(srv.Close)
	return srv, &body
}

// poAgreementScreen drives the real entry point — enter on the supplier
// picker — and settles the agreement load it kicks off, so every test below
// starts from the state an operator actually reaches.
func poAgreementScreen(t *testing.T, srv *httptest.Server) *PurchaseOrderCreateScreen {
	t.Helper()
	s := NewPurchaseOrderCreateScreen(Deps{OMS: omsapi.New(srv.URL)})
	s.supplierLoading = false
	s.suppliers = []omsapi.Supplier{{ID: 7, Name: "Acme Supply"}, {ID: 8, Name: "Other Co"}}
	s.supplierCursor = 0

	_, cmd := s.updateSupplierPhase(tea.KeyMsg{Type: tea.KeyEnter}, 0)
	if cmd == nil {
		t.Fatal("committing a supplier must kick off the agreement load")
	}
	s.Update(cmd())
	return s
}

// stageOneLine gives the cart the line finalize() requires, so agreement tests
// can submit without re-testing line entry.
func stageOneLine(s *PurchaseOrderCreateScreen) {
	cost := 2.50
	s.lines = append(s.lines, poCartLineFor(
		omsapi.PurchaseOrderCreateItem{Description: "Bolts", Quantity: 4, UnitCost: &cost},
		"Bolts",
	))
}

// TestPOAgreement_LoadIsBackgroundAndOffersG: committing a supplier lands the
// operator on the source chooser immediately — the agreement request must not
// gate a phase — and the affordance appears once the list says there is
// something to pick.
func TestPOAgreement_LoadIsBackgroundAndOffersG(t *testing.T) {
	srv, _ := poAgreementSrv(t, poTwoAgreements)
	s := poAgreementScreen(t, srv)

	if s.phase != poPhaseSource {
		t.Fatalf("phase = %v, want poPhaseSource — the agreement load must not block the flow", s.phase)
	}
	if len(s.agreements) != 2 {
		t.Fatalf("loaded %d agreement(s), want 2", len(s.agreements))
	}
	if !s.agreementOffered() {
		t.Error("a supplier with agreements on file should offer the picker")
	}

	out := s.View()
	// The short label, not "Purchase / pricing agreement (optional)": that put
	// the row at 52 cells with an EMPTY value, so the 51-column pane cut the
	// value — the one thing on the row the operator has not already read off
	// the key — off every render.
	// The columnar value row: the label right-aligned into the shared column,
	// the leader, then what the order carries. "(optional)" went with the
	// affordance — the bar names the key and says what it opens, so the row is
	// a label and a VALUE and nothing else.
	if !strings.Contains(out, "Agreement") || !strings.Contains(out, "(none)") {
		t.Errorf("source chooser should advertise the unset agreement:\n%s", out)
	}
	if !strings.Contains(poBarText(s.bar()), "g=Agreement") {
		t.Errorf("the bar should name g: %q", poBarText(s.bar()))
	}
}

// TestPOAgreement_NoneOnFileOffersNothing is the skip-when-none half: a
// supplier with no agreements gets no row, no help entry and an inert g — an
// empty picker would be a dead end, and the web form likewise renders its
// select only when agreements exist.
func TestPOAgreement_NoneOnFileOffersNothing(t *testing.T) {
	srv, _ := poAgreementSrv(t, "")
	s := poAgreementScreen(t, srv)

	if s.agreementOffered() {
		t.Error("a supplier with no agreements must not offer the picker")
	}
	if out := s.View(); strings.Contains(out, "greement") {
		t.Errorf("source chooser should stay silent about agreements:\n%s", out)
	}
	if strings.Contains(poBarText(s.bar()), "Agreement") {
		t.Errorf("the bar should not advertise g: %q", poBarText(s.bar()))
	}

	s.updateSourcePhase(runeKey('g'), 0)
	if s.phase != poPhaseSource {
		t.Errorf("g opened a picker with nothing in it; phase = %v", s.phase)
	}
}

// TestPOAgreement_PickRoundTripsToSubmit walks the whole path the bead
// describes: open the picker, choose an agreement, see it before submitting,
// and find it on the wire as a bare id.
func TestPOAgreement_PickRoundTripsToSubmit(t *testing.T) {
	srv, body := poAgreementSrv(t, poTwoAgreements)
	s := poAgreementScreen(t, srv)

	s.updateSourcePhase(runeKey('g'), 0)
	if s.phase != poPhaseAgreement {
		t.Fatalf("g should open the agreement picker; phase = %v", s.phase)
	}
	// Row 0 is "no agreement", so the first real agreement is one Down away.
	if out := s.View(); !strings.Contains(out, "— no agreement —") {
		t.Errorf("picker must offer an explicit skip row:\n%s", out)
	}
	s.updateAgreementPhase(tea.KeyMsg{Type: tea.KeyDown}, 0)
	s.updateAgreementPhase(tea.KeyMsg{Type: tea.KeyEnter}, 0)

	if s.phase != poPhaseSource {
		t.Errorf("committing an agreement should return to the source chooser; phase = %v", s.phase)
	}
	if s.agreementID == nil || *s.agreementID != 4 {
		t.Fatalf("agreementID = %v, want 4", s.agreementID)
	}

	// Visible in the pinned header (every phase the chooser and review draw)
	// and again on the review surface.
	//
	// Asserted against what the 51-column pane can actually SHOW, not against
	// the unclipped string: the agreement used to ride on the supplier row and
	// "Supplier: Acme Supply (#7)  · agreement: 2026 nonprofit pricing" is 63
	// cells, so the full name was never on the pane and this assertion passed
	// on a row clampToBox had already cut. It is a columnar row of its own now
	// — one label, one value, bounded to what the shared label column leaves.
	for _, line := range s.headerLines().lines() {
		if w := lipgloss.Width(line); w > pickerPaneWidth {
			t.Errorf("a header row is %d cells and the pane cuts at %d: %q", w, pickerPaneWidth, line)
		}
	}
	header := strings.Join(s.headerLines().lines(), "\n")
	if !strings.Contains(header, "Agreement") || !strings.Contains(header, "2026 nonp") {
		t.Errorf("header should name the committed agreement:\n%s", header)
	}
	stageOneLine(s)
	if got := s.View(); !strings.Contains(got, "2026 nonprofit pricing") {
		t.Errorf("review should name the agreement before submit:\n%s", got)
	}

	msg := s.finalize()()
	created, ok := msg.(poCreatedMsg)
	if !ok || created.err != nil {
		t.Fatalf("create failed: %#v", msg)
	}
	got, present := (*body)["supplier_agreement"]
	if !present {
		t.Fatalf("supplier_agreement missing from the payload: %v", *body)
	}
	if n, ok := got.(float64); !ok || n != 4 {
		t.Errorf("supplier_agreement = %v, want 4", got)
	}
}

// TestPOAgreement_SkippedOrderOmitsTheField: declining an agreement on a
// supplier that HAS them must leave the key off the payload entirely. The
// backend validates whatever it is handed against the order's supplier, so a
// null or 0 would be a value to reject rather than the "none" that was meant.
func TestPOAgreement_SkippedOrderOmitsTheField(t *testing.T) {
	srv, body := poAgreementSrv(t, poTwoAgreements)
	s := poAgreementScreen(t, srv)

	s.updateSourcePhase(runeKey('g'), 0)
	s.updateAgreementPhase(tea.KeyMsg{Type: tea.KeyEnter}, 0) // enter on row 0 = none
	if s.agreementID != nil {
		t.Fatalf("row 0 should commit 'no agreement'; got %v", s.agreementID)
	}
	// The review surface still says so — a skipped agreement is a choice worth
	// seeing when the supplier had some to offer.
	stageOneLine(s)
	if got := s.View(); !strings.Contains(got, "(none)") {
		t.Errorf("review should show the agreement was skipped:\n%s", got)
	}

	if msg := s.finalize()(); msg.(poCreatedMsg).err != nil {
		t.Fatalf("create failed: %v", msg.(poCreatedMsg).err)
	}
	if v, present := (*body)["supplier_agreement"]; present {
		t.Errorf("supplier_agreement should be omitted when skipped, got %v", v)
	}
}

// TestPOAgreement_RowZeroClearsAPickAndEscKeepsIt separates the two ways out of
// the picker: esc means "done looking", the skip row means "clear it". An
// operator who opened the picker to check what the order carries must not lose
// the pick by leaving.
func TestPOAgreement_RowZeroClearsAPickAndEscKeepsIt(t *testing.T) {
	srv, _ := poAgreementSrv(t, poTwoAgreements)
	s := poAgreementScreen(t, srv)

	s.updateSourcePhase(runeKey('g'), 0)
	s.updateAgreementPhase(tea.KeyMsg{Type: tea.KeyDown}, 0)
	s.updateAgreementPhase(tea.KeyMsg{Type: tea.KeyEnter}, 0)
	if s.agreementID == nil {
		t.Fatal("setup: agreement should be committed")
	}

	// Re-opening parks the cursor on the current pick, so enter is a no-op
	// confirm rather than a silent reset to "none".
	s.updateSourcePhase(runeKey('g'), 0)
	if s.agreementCursor != 1 {
		t.Errorf("re-opened cursor = %d, want 1 (the committed agreement)", s.agreementCursor)
	}
	s.updateAgreementPhase(tea.KeyMsg{Type: tea.KeyEsc}, 0)
	if s.agreementID == nil || *s.agreementID != 4 {
		t.Errorf("esc cleared the pick; agreementID = %v", s.agreementID)
	}

	// The skip row is the way to clear it.
	s.updateSourcePhase(runeKey('g'), 0)
	s.updateAgreementPhase(tea.KeyMsg{Type: tea.KeyUp}, 0)
	s.updateAgreementPhase(tea.KeyMsg{Type: tea.KeyEnter}, 0)
	if s.agreementID != nil {
		t.Errorf("row 0 should clear the pick; got %v", s.agreementID)
	}
}

// TestPOAgreement_ChangingSupplierDropsThePick: an agreement belongs to exactly
// one supplier and the backend rejects a mismatch, so re-pointing the order at
// another supplier has to clear the pick — not carry it into a 400.
func TestPOAgreement_ChangingSupplierDropsThePick(t *testing.T) {
	srv, _ := poAgreementSrv(t, poTwoAgreements)
	s := poAgreementScreen(t, srv)

	s.updateSourcePhase(runeKey('g'), 0)
	s.updateAgreementPhase(tea.KeyMsg{Type: tea.KeyDown}, 0)
	s.updateAgreementPhase(tea.KeyMsg{Type: tea.KeyEnter}, 0)
	if s.agreementID == nil {
		t.Fatal("setup: agreement should be committed")
	}

	// Back to the supplier picker, move to a different supplier, commit.
	s.updateSourcePhase(tea.KeyMsg{Type: tea.KeyEsc}, 0)
	s.updateSupplierPhase(tea.KeyMsg{Type: tea.KeyDown}, 0)
	_, cmd := s.updateSupplierPhase(tea.KeyMsg{Type: tea.KeyEnter}, 0)

	if s.supplierID != 8 {
		t.Fatalf("supplierID = %d, want 8", s.supplierID)
	}
	if s.agreementID != nil {
		t.Errorf("changing supplier must clear the agreement; got %v", s.agreementID)
	}
	if len(s.agreements) != 0 {
		t.Errorf("the previous supplier's agreements are still offered: %+v", s.agreements)
	}
	if cmd == nil {
		t.Error("changing supplier should refetch that supplier's agreements")
	}

	// Stepping back and re-committing the SAME supplier is not a change: the
	// operator only went to look. No refetch, and nothing to clear.
	s.updateSourcePhase(runeKey('b'), 0)
	if _, again := s.updateSupplierPhase(tea.KeyMsg{Type: tea.KeyEnter}, 0); again != nil {
		t.Error("re-committing the same supplier should not refetch its agreements")
	}
}

// TestPOAgreement_StaleLoadForAnotherSupplierIsDropped: a slow response for a
// supplier the operator has moved off of would otherwise offer agreements the
// backend rejects against the current supplier.
func TestPOAgreement_StaleLoadForAnotherSupplierIsDropped(t *testing.T) {
	srv, _ := poAgreementSrv(t, poTwoAgreements)
	s := poAgreementScreen(t, srv)

	s.Update(poAgreementsLoadedMsg{
		supplierID: 999,
		agreements: []omsapi.SupplierAgreement{{ID: 77, Supplier: 999, Name: "Someone else's deal"}},
	})
	for _, a := range s.agreements {
		if a.ID == 77 {
			t.Fatalf("a stale load leaked another supplier's agreement: %+v", s.agreements)
		}
	}
}

// TestPOAgreement_FailedLoadSaysUnavailableAndStillSubmits holds both halves of
// the failure contract: the operator is told the list is MISSING (silence would
// read as "this supplier has none", which may be false), and a lookup that
// could not be made never stops the purchase order from going out.
func TestPOAgreement_FailedLoadSaysUnavailableAndStillSubmits(t *testing.T) {
	body := map[string]any{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "supplier-agreements") {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"detail":"boom"}`))
			return
		}
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":1,"po_number":"PO-2026-0009"}`))
	}))
	defer srv.Close()

	s := poAgreementScreen(t, srv)
	if s.agreementLoadErr == "" {
		t.Fatal("a failed agreement load should be recorded")
	}
	if s.phase != poPhaseSource {
		t.Errorf("a failed agreement load must not strand the flow; phase = %v", s.phase)
	}
	if out := s.View(); !strings.Contains(out, "unavailable") {
		t.Errorf("source chooser should say the list is unavailable, not imply there are none:\n%s", out)
	}
	// g retries rather than opening a picker over an empty list.
	s.updateSourcePhase(runeKey('g'), 0)
	if s.phase == poPhaseAgreement {
		t.Error("g should retry a failed load, not open an empty picker")
	}

	stageOneLine(s)
	if msg := s.finalize()(); msg.(poCreatedMsg).err != nil {
		t.Fatalf("a failed agreement lookup blocked the order: %v", msg.(poCreatedMsg).err)
	}
	if v, present := body["supplier_agreement"]; present {
		t.Errorf("supplier_agreement should be omitted, got %v", v)
	}
}

// TestPOAgreement_NotesRenderUnderTheHighlightedRow: the terms are why an
// operator cites an agreement at all, so the highlighted row's notes show
// without a detour — the same paragraph the web form puts under its select.
func TestPOAgreement_NotesRenderUnderTheHighlightedRow(t *testing.T) {
	srv, _ := poAgreementSrv(t, poTwoAgreements)
	s := poAgreementScreen(t, srv)
	s.updateSourcePhase(runeKey('g'), 0)

	if out := s.View(); strings.Contains(out, "15% off list") {
		t.Errorf("the skip row has no notes to show:\n%s", out)
	}
	s.updateAgreementPhase(tea.KeyMsg{Type: tea.KeyDown}, 0)
	if out := s.View(); !strings.Contains(out, "15% off list, net 30") {
		t.Errorf("highlighted agreement should show its terms:\n%s", out)
	}
	// The second agreement has no notes — the row must simply carry none.
	s.updateAgreementPhase(tea.KeyMsg{Type: tea.KeyDown}, 0)
	out := s.View()
	if !strings.Contains(out, "Standing quote Q3") {
		t.Errorf("second agreement missing from the list:\n%s", out)
	}
	if strings.Contains(out, "15% off list") {
		t.Errorf("notes leaked from the previously highlighted row:\n%s", out)
	}
}
