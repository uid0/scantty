package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/uid0/scantty/internal/omsapi"
)

// Drives of the vendor work-order stepper, pumped through Root.Update against a
// stateful fake of the `maintenance_orders` app.
//
// ROOT-LEVEL on purpose. The sheet is deliberately NOT raw-input — so tab still
// reaches the sidebar and esc is still the global back step — which means every
// action letter it binds travels through the global key layer on its way in. A
// screen-level test would pass while the real app did something else with the
// keystroke, which is exactly the failure wo_materials_drive_test.go records.
//
// WHAT THE FAKE IS AND IS NOT. It mirrors transitions.py's gates well enough
// that a drive has to satisfy them in order — no advance without an NTE, no
// scheduling without quotes or a waiver, no sign-off without a photo, no
// closure without an invoice and an FSR — so the sequence these tests walk is
// the sequence the server would demand. It cannot pin the WIRE SHAPE, because
// it answers by marshalling ScanTTY's own struct and so agrees with it by
// construction; that half is pinned in internal/omsapi/maintenance_orders_test.go
// against a payload transcribed from the serializer.

// vwoFake is a third-party work order that remembers what was done to it.
type vwoFake struct {
	mu  sync.Mutex
	wo  omsapi.MaintenanceOrder
	log []vwoCall
	// refuse, when set, answers the named action with a 400 carrying the
	// backend's hand-written {"detail": ...} body.
	refuse map[string]string
}

// vwoCall is one request the screen made: what it asked for and what it sent.
type vwoCall struct {
	method string
	path   string
	body   map[string]any
	// fields are the multipart text parts, for the attachment upload.
	fields map[string]string
	file   string
}

const vwoTestID = "c0ffee00-1111-2222-3333-444455556666"

// vwoTestOrder is the fixture order. Its names are at the length OMS really
// serves — a full vendor business name and a real asset description — because
// every fixture in this package used to read "Acme" and "Bolt", and a row that
// was never rendered at the length the server sends has never been tested at
// the width that bites.
func vwoTestOrder() omsapi.MaintenanceOrder {
	loc := 17
	asset := "a1b2c3d4-0000-4444-8888-abcdef012345"
	return omsapi.MaintenanceOrder{
		ID: vwoTestID, ShortID: "TPWO-C0FFEE00",
		Title:     "Replace compressor belt and re-tension the drive",
		Asset:     &asset,
		AssetName: "Bridgeport Series I Vertical Mill",
		Location:  &loc, LocationName: "Machine Shop",
		Vendor:     "3f4c8b2a-1e2d-4a5b-8c9d-0a1b2c3d4e5f",
		VendorName: "Northern Industrial Service & Repair Co",
		WorkType:   omsapi.ThirdPartyWorkTypeMajorRepair, WorkTypeDisplay: "Major Repair",
		Status: omsapi.MaintenanceOrderRequested, StatusDisplay: "Requested",
		ParCostBuffer: "50.00",
		OpenedAt:      time.Date(2026, 3, 1, 8, 0, 0, 0, time.UTC),
		Notes:         "Belt slipping under load; shop cannot source the matched pair.",
	}
}

func newVWOFake() *vwoFake {
	return &vwoFake{wo: vwoTestOrder(), refuse: map[string]string{}}
}

// workflow recomputes the serializer's gate block the way
// ThirdPartyWorkOrderSerializer.get_workflow does, so the screen is driven
// against the same facts the server would send.
func (f *vwoFake) workflow() omsapi.MaintenanceOrderWorkflow {
	kinds := map[string]bool{}
	for _, a := range f.wo.Attachments {
		kinds[a.Kind] = true
	}
	emergency := f.wo.EmergencyAuthorizedAt != nil
	return omsapi.MaintenanceOrderWorkflow{
		HasNTE:                          !f.wo.NTEAmount.Empty(),
		HasActiveEmergencyAuthorization: emergency,
		HasRequiredQuotes: f.wo.IsEmergency || emergency ||
			f.wo.QuoteWaiverSignedAt != nil || len(f.wo.Quotes) >= 3,
		QuoteCount:        len(f.wo.Quotes),
		HasPhotoEvidence:  kinds[omsapi.MaintenanceOrderAttachmentPhoto],
		HasInvoiceAndFSR:  kinds[omsapi.MaintenanceOrderAttachmentInvoice] && kinds[omsapi.MaintenanceOrderAttachmentFSR],
		VarianceStatus:    f.wo.VarianceStatus,
		KeyfobOutstanding: f.wo.KeyfobID != "" && f.wo.KeyfobReturnedAt == nil,
	}
}

var vwoStatusLabels = map[string]string{
	omsapi.MaintenanceOrderRequested:       "Requested",
	omsapi.MaintenanceOrderSourcing:        "Sourcing",
	omsapi.MaintenanceOrderScheduled:       "Scheduled",
	omsapi.MaintenanceOrderInProgress:      "In Progress",
	omsapi.MaintenanceOrderValidated:       "Validated",
	omsapi.MaintenanceOrderFinancialReview: "Financial Review",
	omsapi.MaintenanceOrderClosed:          "Closed",
	omsapi.MaintenanceOrderCancelled:       "Cancelled",
}

func (f *vwoFake) answer(w http.ResponseWriter, status int) {
	f.wo.Workflow = f.workflow()
	f.wo.StatusDisplay = vwoStatusLabels[f.wo.Status]
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(f.wo)
}

// refusal writes the backend's own hand-rolled refusal: a plain
// Response({"detail": ...}) that never reaches DRF's exception handler.
func vwoRefusal(w http.ResponseWriter, sentence string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadRequest)
	_ = json.NewEncoder(w).Encode(map[string]string{"detail": sentence})
}

func (f *vwoFake) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()

		call := vwoCall{method: r.Method, path: r.URL.Path}
		if r.Method == http.MethodPost {
			if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/") {
				_ = r.ParseMultipartForm(1 << 20)
				call.fields = map[string]string{}
				for k, v := range r.MultipartForm.Value {
					call.fields[k] = v[0]
				}
				if fs := r.MultipartForm.File["file"]; len(fs) > 0 {
					call.file = fs[0].Filename
				}
			} else if raw, _ := io.ReadAll(r.Body); len(raw) > 0 {
				call.body = map[string]any{}
				_ = json.Unmarshal(raw, &call.body)
			}
		}
		f.log = append(f.log, call)

		base := "/api/maintenance-orders/work-orders/" + vwoTestID + "/"
		switch {
		case r.Method == http.MethodGet && r.URL.Path == base:
			f.answer(w, http.StatusOK)
		case r.Method == http.MethodGet && r.URL.Path == "/api/maintenance-orders/work-orders/":
			f.wo.Workflow = f.workflow()
			f.wo.StatusDisplay = vwoStatusLabels[f.wo.Status]
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"count": 1, "results": []omsapi.MaintenanceOrder{f.wo},
			})
		case r.Method == http.MethodPost && r.URL.Path == "/api/maintenance-orders/quotes/":
			f.wo.Quotes = append(f.wo.Quotes, omsapi.MaintenanceOrderQuote{
				ID:     fmt.Sprintf("quote-%d", len(f.wo.Quotes)+1),
				Vendor: fmt.Sprint(call.body["vendor"]), VendorName: f.wo.VendorName,
				Amount: omsapi.DecimalString(fmt.Sprint(call.body["amount"])),
			})
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(f.wo.Quotes[len(f.wo.Quotes)-1])
		case r.Method == http.MethodPost && r.URL.Path == "/api/maintenance-orders/attachments/":
			kind := call.fields["kind"]
			if kind == "" {
				kind = omsapi.MaintenanceOrderAttachmentOther
			}
			f.wo.Attachments = append(f.wo.Attachments, omsapi.MaintenanceOrderAttachment{
				ID:   fmt.Sprintf("att-%d", len(f.wo.Attachments)+1),
				Kind: kind, KindDisplay: kind, Caption: call.fields["caption"],
				File: call.file,
			})
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(f.wo.Attachments[len(f.wo.Attachments)-1])
		case r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, base):
			action := strings.Trim(strings.TrimPrefix(r.URL.Path, base), "/")
			if sentence, ok := f.refuse[action]; ok {
				vwoRefusal(w, sentence)
				return
			}
			f.apply(w, action, call.body)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}
}

// apply is the fake's half of transitions.py: the gates, in the same order, so
// a drive that satisfies it has satisfied the shape of the real thing.
func (f *vwoFake) apply(w http.ResponseWriter, action string, body map[string]any) {
	now := time.Now().UTC()
	str := func(k string) string {
		if v, ok := body[k]; ok {
			return fmt.Sprint(v)
		}
		return ""
	}
	wf := f.workflow()
	switch action {
	case "set-nte":
		if f.wo.Status != omsapi.MaintenanceOrderRequested &&
			f.wo.Status != omsapi.MaintenanceOrderSourcing {
			vwoRefusal(w, "NTE can only be set during intake or sourcing.")
			return
		}
		f.wo.NTEAmount = omsapi.DecimalString(str("nte_amount"))
		f.wo.NTESetAt = &now
	case "authorize-emergency":
		f.wo.IsEmergency = true
		f.wo.EmergencyAuthorizedAt = &now
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "auth-1", "work_order": vwoTestID, "reason": str("reason"),
			"authorized_at": now, "expires_at": now.Add(24 * time.Hour),
			"is_currently_valid": true,
		})
		return
	case "advance-to-sourcing":
		if f.wo.Status != omsapi.MaintenanceOrderRequested {
			vwoRefusal(w, fmt.Sprintf("Work order is in '%s' state; expected 'requested'.", f.wo.Status))
			return
		}
		if !wf.HasNTE && !f.wo.IsEmergency {
			vwoRefusal(w, "Cannot advance to sourcing without an NTE or emergency authorization.")
			return
		}
		f.wo.Status = omsapi.MaintenanceOrderSourcing
	case "waive-quote-requirement":
		if strings.TrimSpace(str("reason")) == "" {
			vwoRefusal(w, "Waiver requires a written reason for audit.")
			return
		}
		f.wo.QuoteWaiverSignedAt = &now
		f.wo.QuoteWaiverReason = str("reason")
	case "advance-to-scheduled":
		if f.wo.Status != omsapi.MaintenanceOrderSourcing {
			vwoRefusal(w, fmt.Sprintf("Work order is in '%s' state; expected 'sourcing'.", f.wo.Status))
			return
		}
		if !wf.HasRequiredQuotes {
			vwoRefusal(w, "Standard work orders require 3 quotes OR a signed waiver OR an "+
				"active emergency authorization before scheduling.")
			return
		}
		f.wo.Status = omsapi.MaintenanceOrderScheduled
	case "vendor-arrived":
		if f.wo.Status != omsapi.MaintenanceOrderScheduled {
			vwoRefusal(w, fmt.Sprintf("Work order is in '%s' state; expected 'scheduled'.", f.wo.Status))
			return
		}
		f.wo.Status = omsapi.MaintenanceOrderInProgress
		f.wo.DowntimeStart = &now
		if k := str("keyfob_id"); k != "" {
			f.wo.KeyfobID = k
		}
	case "record-keyfob-return":
		if f.wo.KeyfobID == "" {
			vwoRefusal(w, "No keyfob is checked out for this work order.")
			return
		}
		f.wo.KeyfobReturnedAt = &now
	case "sign-off":
		if f.wo.Status != omsapi.MaintenanceOrderInProgress {
			vwoRefusal(w, fmt.Sprintf("Work order is in '%s' state; expected 'in_progress'.", f.wo.Status))
			return
		}
		if !wf.HasPhotoEvidence {
			vwoRefusal(w, "At least one photo attachment is required before validation.")
			return
		}
		f.wo.Status = omsapi.MaintenanceOrderValidated
		f.wo.DowntimeEnd = &now
		f.wo.TotalDowntime = "04:30:00"
	case "advance-to-financial-review":
		if f.wo.Status != omsapi.MaintenanceOrderValidated {
			vwoRefusal(w, fmt.Sprintf("Work order is in '%s' state; expected 'validated'.", f.wo.Status))
			return
		}
		f.wo.ActualInvoiceTotal = omsapi.DecimalString(str("actual_invoice_total"))
		if fee := str("dispatch_fee"); fee != "" {
			f.wo.DispatchFee = omsapi.DecimalString(fee)
		}
		f.wo.Status = omsapi.MaintenanceOrderFinancialReview
		f.wo.VarianceStatus = "auto_approved"
	case "override-variance":
		if f.wo.VarianceStatus != omsapi.MaintenanceOrderVarianceBlocked {
			vwoRefusal(w, "Variance is not currently blocked; nothing to override.")
			return
		}
		f.wo.VarianceStatus = "auto_approved"
	case "close":
		if f.wo.Status != omsapi.MaintenanceOrderFinancialReview {
			vwoRefusal(w, fmt.Sprintf("Work order is in '%s' state; expected 'financial_review'.", f.wo.Status))
			return
		}
		if f.wo.VarianceStatus == omsapi.MaintenanceOrderVarianceBlocked {
			vwoRefusal(w, "Variance exceeds par buffer and 15% NTE — closure blocked.")
			return
		}
		if !wf.HasInvoiceAndFSR {
			vwoRefusal(w, "Closure requires both invoice and FSR (Field Service Report) attachments.")
			return
		}
		if wf.KeyfobOutstanding {
			vwoRefusal(w, "Keyfob "+f.wo.KeyfobID+" is still checked out to the vendor.")
			return
		}
		f.wo.Status = omsapi.MaintenanceOrderClosed
		f.wo.ClosedAt = &now
	default:
		w.WriteHeader(http.StatusNotFound)
		return
	}
	f.answer(w, http.StatusOK)
}

// posts returns the action segment of every POST the screen made, in order.
func (f *vwoFake) posts() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, c := range f.log {
		if c.method != http.MethodPost {
			continue
		}
		out = append(out, strings.TrimSuffix(
			strings.TrimPrefix(c.path, "/api/maintenance-orders/"), "/"))
	}
	return out
}

func (f *vwoFake) lastPost() (vwoCall, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := len(f.log) - 1; i >= 0; i-- {
		if f.log[i].method == http.MethodPost {
			return f.log[i], true
		}
	}
	return vwoCall{}, false
}

// vwoDrive stands a screen up against the fake, loaded and sized at 80x30.
func vwoDrive(t *testing.T, fake *vwoFake) (Root, *VendorWorkOrderDetailScreen) {
	t.Helper()
	srv := httptest.NewServer(fake.handler())
	t.Cleanup(srv.Close)
	deps := Deps{OMS: omsapi.New(srv.URL), Ctx: context.Background()}
	screen := NewVendorWorkOrderDetailScreen(deps, vwoTestID)
	r := newTestRoot(screen)
	r.deps = deps
	next, _ := r.Update(tea.WindowSizeMsg{Width: 80, Height: 30})
	r = next.(Root)
	r = pump(t, r, screen.Init(), 0)
	return r, screen
}

func vwoKey(t *testing.T, r Root, k string) Root {
	t.Helper()
	return key(t, r, poPickerKeyMsg(k))
}

// vwoType sends a string one rune at a time — which is what a keyboard and a
// scanner both do — and does NOT settle what each press returns.
//
// Typing is synchronous: the screen updates its own textinput inside Update, so
// the value and the frame are already correct when this returns. The only
// command a typed rune produces is the cursor's own blink TICK — not the
// immediate initialBlinkMsg the shared pump recognises, but a real 530ms
// tea.Tick — so settling each press pays the pump's flat 200ms budget per
// keystroke. Driven through `key`, this file's typing alone cost about 90
// seconds; it costs nothing measurable now. A key that starts real work still
// goes through vwoKey.
func vwoType(t *testing.T, r Root, s string) Root {
	t.Helper()
	for _, ch := range s {
		next, _ := r.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{ch}})
		after, ok := next.(Root)
		if !ok {
			t.Fatalf("Root.Update returned %T, want Root", next)
		}
		r = after
	}
	return r
}

// vwoPane is what the operator is actually looking at: Root's CLIPPED render,
// stripped of styling, at 80 columns.
func vwoPane(t *testing.T, r Root) string {
	t.Helper()
	return stripANSI(r.View())
}

// ---------------------------------------------------------------------------
// The dead end, closed end to end
// ---------------------------------------------------------------------------

// TestVendorWO_TheWholeStepperRunsFromTheTerminal is the bead, driven: a vendor
// work order created by the terminal is taken from `requested` to `closed`
// without a browser, through the REAL key handlers, and the requests the fake
// received are the eleven the web stepper makes.
//
// It walks the WAIVER branch of sourcing rather than three quotes because that
// is the branch with a money decision in it — and adds a quote on the way so
// the quote endpoint is driven too.
func TestVendorWO_TheWholeStepperRunsFromTheTerminal(t *testing.T) {
	fake := newVWOFake()
	r, s := vwoDrive(t, fake)

	photo := vwoTempFile(t, "site-photo.jpg")
	invoice := vwoTempFile(t, "invoice-8812.pdf")
	fsr := vwoTempFile(t, "field-service-report.pdf")

	// Step 1 — the NTE. n opens the form, Enter goes to the CONFIRM (not the
	// wire), Ctrl-X commits.
	r = vwoKey(t, r, "n")
	r = vwoType(t, r, "1200.00")
	r = vwoKey(t, r, "enter")
	if len(fake.posts()) != 0 {
		t.Fatalf("Enter on the NTE form already wrote: %v", fake.posts())
	}
	r = vwoKey(t, r, "ctrl+x")
	if s.wo.NTEAmount != "1200.00" {
		t.Fatalf("NTE = %q after the commit", s.wo.NTEAmount)
	}

	// Step 1 → 2.
	r = vwoKey(t, r, "enter")
	r = vwoKey(t, r, "ctrl+x")
	if s.wo.Status != omsapi.MaintenanceOrderSourcing {
		t.Fatalf("status = %q, want sourcing", s.wo.Status)
	}

	// Step 2 — one quote on file, then the waiver that satisfies the gate.
	r = vwoKey(t, r, "q")
	r = vwoType(t, r, "1180.00")
	r = vwoKey(t, r, "enter")
	r = vwoKey(t, r, "ctrl+x")
	if len(s.wo.Quotes) != 1 {
		t.Fatalf("quotes = %d, want 1", len(s.wo.Quotes))
	}
	r = vwoKey(t, r, "w")
	r = vwoType(t, r, "Sole authorized service provider for this model")
	r = vwoKey(t, r, "enter")
	r = vwoKey(t, r, "ctrl+x")
	if s.wo.QuoteWaiverSignedAt == nil {
		t.Fatal("the waiver did not land")
	}

	// Step 2 → 3.
	r = vwoKey(t, r, "enter")
	r = vwoKey(t, r, "ctrl+x")
	if s.wo.Status != omsapi.MaintenanceOrderScheduled {
		t.Fatalf("status = %q, want scheduled", s.wo.Status)
	}

	// Step 3 → 4, with a keyfob, which arms the closure gate.
	r = vwoKey(t, r, "enter")
	r = vwoType(t, r, "KF-0042")
	r = vwoKey(t, r, "enter")
	r = vwoKey(t, r, "ctrl+x")
	if s.wo.Status != omsapi.MaintenanceOrderInProgress || s.wo.KeyfobID != "KF-0042" {
		t.Fatalf("status = %q keyfob = %q", s.wo.Status, s.wo.KeyfobID)
	}

	// Step 4 — sign-off needs a photo, so the attachment path is part of the
	// workflow rather than an extra.
	r = vwoAttachFile(t, r, photo, omsapi.MaintenanceOrderAttachmentPhoto)
	r = vwoKey(t, r, "enter")
	r = vwoKey(t, r, "ctrl+x")
	if s.wo.Status != omsapi.MaintenanceOrderValidated {
		t.Fatalf("status = %q, want validated", s.wo.Status)
	}

	// Step 5 → 6, the invoice.
	r = vwoKey(t, r, "enter")
	r = vwoType(t, r, "1425.50")
	r = vwoKey(t, r, "down")
	r = vwoType(t, r, "95.00")
	r = vwoKey(t, r, "enter")
	r = vwoKey(t, r, "ctrl+x")
	if s.wo.Status != omsapi.MaintenanceOrderFinancialReview ||
		s.wo.ActualInvoiceTotal != "1425.50" || s.wo.DispatchFee != "95.00" {
		t.Fatalf("financial review = %q / %q / %q",
			s.wo.Status, s.wo.ActualInvoiceTotal, s.wo.DispatchFee)
	}

	// Step 6 → 7: the invoice and FSR the server gates closure on, the keyfob
	// back, then the close.
	r = vwoAttachFile(t, r, invoice, omsapi.MaintenanceOrderAttachmentInvoice)
	r = vwoAttachFile(t, r, fsr, omsapi.MaintenanceOrderAttachmentFSR)
	r = vwoKey(t, r, "k")
	r = vwoKey(t, r, "ctrl+x")
	if s.wo.Workflow.KeyfobOutstanding {
		t.Fatal("the keyfob is still outstanding after the return was recorded")
	}
	r = vwoKey(t, r, "enter")
	r = vwoKey(t, r, "ctrl+x")
	if s.wo.Status != omsapi.MaintenanceOrderClosed {
		t.Fatalf("status = %q, want closed", s.wo.Status)
	}

	// THE REQUESTS, in order. This is the claim: every one of the web
	// stepper's actions was driven from the terminal, and nothing else was.
	want := []string{
		"work-orders/" + vwoTestID + "/set-nte",
		"work-orders/" + vwoTestID + "/advance-to-sourcing",
		"quotes",
		"work-orders/" + vwoTestID + "/waive-quote-requirement",
		"work-orders/" + vwoTestID + "/advance-to-scheduled",
		"work-orders/" + vwoTestID + "/vendor-arrived",
		"attachments",
		"work-orders/" + vwoTestID + "/sign-off",
		"work-orders/" + vwoTestID + "/advance-to-financial-review",
		"attachments",
		"attachments",
		"work-orders/" + vwoTestID + "/record-keyfob-return",
		"work-orders/" + vwoTestID + "/close",
	}
	if got := fake.posts(); !equalStrings(got, want) {
		t.Errorf("requests sent:\n got %v\nwant %v", got, want)
	}

	// And the closed order offers nothing to press but the way out and a
	// refresh: no key on a closed order can be refused, because none is named.
	for _, item := range s.sheetBar() {
		if item.Key != "Esc" && item.Key != "r" &&
			item.Key != "UP/DN" && item.Key != "PgUp/PgDn" && item.Key != "Home/End" {
			t.Errorf("a closed order still names %q=%q", item.Key, item.Label)
		}
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func vwoTempFile(t *testing.T, name string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte("fixture bytes"), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return path
}

// vwoAttachFile drives the attach form: path, then the kind CHOICE row cycled
// with →, then the confirm.
func vwoAttachFile(t *testing.T, r Root, path, kind string) Root {
	t.Helper()
	r = vwoKey(t, r, "a")
	r = vwoType(t, r, path)
	r = vwoKey(t, r, "down")
	for i := 0; i < len(omsapi.MaintenanceOrderAttachmentKinds); i++ {
		if omsapi.MaintenanceOrderAttachmentKinds[i] == kind {
			for j := 0; j < i; j++ {
				r = vwoKey(t, r, "right")
			}
			break
		}
	}
	r = vwoKey(t, r, "enter")
	return vwoKey(t, r, "ctrl+x")
}

// ---------------------------------------------------------------------------
// What is committed is on the pane before it is sent
// ---------------------------------------------------------------------------

// TestVendorWO_TheConfirmShowsTheMoneyBeforeItIsSent is the money-shaped half
// of the brief: a mis-keyed amount costs real money, so the figure and what it
// is measured against are on the CLIPPED pane before Ctrl-X can reach the wire.
//
// Asserted against Root.View() rather than the screen's own, because the pane
// is 51 columns at 80 and this test's whole job is what the operator is looking
// at (AGENTS.md's clipped-render rule).
func TestVendorWO_TheConfirmShowsTheMoneyBeforeItIsSent(t *testing.T) {
	fake := newVWOFake()
	fake.wo.Status = omsapi.MaintenanceOrderValidated
	fake.wo.NTEAmount = "1200.00"
	r, s := vwoDrive(t, fake)

	r = vwoKey(t, r, "enter") // the validated order's next step: capture the invoice
	r = vwoType(t, r, "1425.50")
	r = vwoKey(t, r, "enter")
	if s.phase != vwoPhaseConfirm {
		t.Fatalf("phase = %v, want the confirm", s.phase)
	}
	pane := vwoPane(t, r)
	for _, want := range []string{
		"$1425.50", // what is about to be sent
		"$1200.00", // what it is measured against
		"+$225.50", // the difference, computed exactly
		"TPWO-C0FFEE00",
		"Ctrl-X",
	} {
		if !strings.Contains(pane, want) {
			t.Errorf("the confirm does not carry %q:\n%s", want, pane)
		}
	}
	// And nothing has been sent yet.
	if got := fake.posts(); len(got) != 0 {
		t.Errorf("the confirm had already written: %v", got)
	}
}

// TestVendorWO_ADifferenceIsExactAndIsNotAVerdict.
//
// The difference is arithmetic on two facts the server gave us, done in big.Rat
// because it is money — 0.1 + 0.2 in float64 is how a confirm comes to show
// $225.49999999999997 on the frame an order is committed from. It is NOT a
// verdict: whether the overage is approved is scored against the par buffer and
// a 15%-of-NTE cap in transitions.evaluate_variance, and a client that guessed
// would eventually guess differently from the server it is reporting.
func TestVendorWO_ADifferenceIsExactAndIsNotAVerdict(t *testing.T) {
	got, ok := vwoDelta("0.10", "0.30")
	if !ok || got != "+$0.20" {
		t.Errorf("0.30 - 0.10 = %q (ok=%v), want +$0.20 exactly", got, ok)
	}
	if got, ok := vwoDelta("1200.00", "1100.00"); !ok || got != "$-100.00" {
		t.Errorf("under the NTE = %q (ok=%v)", got, ok)
	}
	// A missing figure is not a zero: no difference is drawn at all rather than
	// one measured against a number this side invented.
	if _, ok := vwoDelta("", "1425.50"); ok {
		t.Error("a difference was computed against an absent NTE")
	}
	// And the three variance states stay three. "" is not approval.
	if vwoVarianceText("") == vwoVarianceText("auto_approved") {
		t.Error("an unscored variance reads the same as an approved one")
	}
}

// ---------------------------------------------------------------------------
// Refusals
// ---------------------------------------------------------------------------

// TestVendorWO_AServerRefusalReachesTheOperatorAsOneMarkedLine.
//
// views._err writes `{"detail": ...}` by hand and never reaches DRF's exception
// handler, so omsapi.parseError hands the WHOLE RAW BODY over. Unrecovered, the
// bench reads the JSON. The sentence has to arrive whole, marked, and on ONE
// line — the status row cannot fold, and a refusal spread over rows loses
// whichever ones the pane drops.
func TestVendorWO_AServerRefusalReachesTheOperatorAsOneMarkedLine(t *testing.T) {
	const sentence = "Waiver requires a written reason for audit."
	fake := newVWOFake()
	fake.wo.Status = omsapi.MaintenanceOrderSourcing
	fake.wo.NTEAmount = "1200.00"
	fake.refuse["waive-quote-requirement"] = sentence
	r, s := vwoDrive(t, fake)

	r = vwoKey(t, r, "w")
	r = vwoType(t, r, "because")
	r = vwoKey(t, r, "enter")
	r = vwoKey(t, r, "ctrl+x")

	if s.errMsg != sentence {
		t.Fatalf("errMsg = %q, want the server's own sentence", s.errMsg)
	}
	if strings.Contains(s.errMsg, "{") {
		t.Errorf("the raw JSON body reached the operator: %q", s.errMsg)
	}
	pane := vwoPane(t, r)
	var marked string
	for _, line := range strings.Split(pane, "\n") {
		if strings.Contains(line, jdeStatusErrMark) {
			marked = line
		}
	}
	if marked == "" {
		t.Fatalf("no marked refusal line on the pane:\n%s", pane)
	}
	// Left-justified after the mark, and carrying the load-bearing head of the
	// sentence. The TAIL is what a narrow pane takes, which is why the head is
	// what is asserted.
	if !strings.Contains(marked, "Waiver requires a written reason") {
		t.Errorf("the marked line does not carry the refusal: %q", marked)
	}
	// The prompt STAYS UP, so the reason can be fixed in place rather than
	// retyped from the sheet.
	if s.phase != vwoPhaseConfirm {
		t.Errorf("phase = %v after a refusal, want the confirm still up", s.phase)
	}

	// It outlives an Esc back to the sheet — StatusBar.Flash expires after four
	// seconds and the operator who looked away is still owed the reason — and
	// it goes when the sheet is re-read, because a failure standing over a
	// freshly loaded order is a claim about a state that no longer exists.
	r = vwoKey(t, r, "esc")
	r = vwoKey(t, r, "esc")
	if s.phase != vwoPhaseSheet {
		t.Fatalf("phase = %v, want back on the sheet", s.phase)
	}
	if !strings.Contains(vwoPane(t, r), "Waiver requires a written reason") {
		t.Errorf("the refusal did not survive the walk back to the sheet:\n%s", vwoPane(t, r))
	}
	r = vwoKey(t, r, "r")
	if s.errMsg != "" {
		t.Errorf("errMsg = %q after a refresh — a stale failure over a re-read order", s.errMsg)
	}
}

// TestVendorWO_AMultiLineFailureBodyIsFlattenedToOneMarkedLine.
//
// Not every refusal is the hand-written {"detail": ...} sentence: a gateway
// between ScanTTY and OMS answers with an HTML page, and omsapi.parseError puts
// the ENTIRE raw body into APIError.Message. The status row cannot fold, so a
// multi-line body handed to it straight runs the frame over its own height and
// clampToBox — which drops from the BOTTOM — takes the whole action bar with
// it: every key on the screen unnamed at once.
func TestVendorWO_AMultiLineFailureBodyIsFlattenedToOneMarkedLine(t *testing.T) {
	fake := newVWOFake()
	fake.wo.Status = omsapi.MaintenanceOrderRequested
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			w.Header().Set("Content-Type", "text/html")
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte("<html>\r\n<head><title>502 Bad Gateway</title></head>\r\n" +
				"<body>\r\n<center><h1>502 Bad Gateway</h1></center>\r\n" +
				"<hr><center>nginx</center>\r\n</body>\r\n</html>\r\n"))
			return
		}
		fake.handler()(w, r)
	}))
	defer srv.Close()

	deps := Deps{OMS: omsapi.New(srv.URL), Ctx: context.Background()}
	screen := NewVendorWorkOrderDetailScreen(deps, vwoTestID)
	r := newTestRoot(screen)
	r.deps = deps
	next, _ := r.Update(tea.WindowSizeMsg{Width: 80, Height: 30})
	r = next.(Root)
	r = pump(t, r, screen.Init(), 0)

	r = vwoKey(t, r, "n")
	r = vwoType(t, r, "1200.00")
	r = vwoKey(t, r, "enter")
	r = vwoKey(t, r, "ctrl+x")

	pane := vwoPane(t, r)
	marked := 0
	for _, line := range strings.Split(pane, "\n") {
		if strings.Contains(line, jdeStatusErrMark) {
			marked++
		}
	}
	if marked != 1 {
		t.Errorf("the gateway page reached the pane on %d marked lines, want exactly 1:\n%s",
			marked, pane)
	}
	// The bar is still there, whole: that is what a frame run over its own
	// height loses first.
	if !strings.Contains(pane, "Esc=Cancel") || !strings.Contains(pane, "Ctrl-X=") {
		t.Errorf("the action bar did not survive the failure:\n%s", pane)
	}
}

// TestVendorWO_AMisKeyedAmountIsRefusedBeforeItIsSent.
//
// big.Rat.SetString is a NUMBER parser and takes "1/3", "1e9" and "-5"; this
// screen posts the row verbatim, so a mis-keyed leading minus would otherwise
// reach the server as a negative NTE or be scored as a negative invoice. The
// refusal says so and NOTHING is sent.
func TestVendorWO_AMisKeyedAmountIsRefusedBeforeItIsSent(t *testing.T) {
	for _, typed := range []string{"-5", "1e9", "1/3", "twelve", ""} {
		t.Run(fmt.Sprintf("%q", typed), func(t *testing.T) {
			fake := newVWOFake()
			r, s := vwoDrive(t, fake)
			r = vwoKey(t, r, "n")
			r = vwoType(t, r, typed)
			r = vwoKey(t, r, "enter")
			if s.phase != vwoPhaseForm {
				t.Fatalf("%q walked onto the confirm", typed)
			}
			if s.note == "" {
				t.Fatal("the form declined in silence — the pane came back unchanged")
			}
			if got := fake.posts(); len(got) != 0 {
				t.Errorf("%q was sent: %v", typed, got)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Gates
// ---------------------------------------------------------------------------

// TestVendorWO_AShutGateIsNeitherNamedNorBoundAndSaysHowToOpenIt.
//
// A key the bar does not name must do nothing, and a key it names must do
// something — so a forward transition the server would refuse is dropped from
// BOTH. That alone would be a dead end, which is its own defect, so the reason
// is drawn and it names a REMEDY that is a key on this same frame.
func TestVendorWO_AShutGateIsNeitherNamedNorBoundAndSaysHowToOpenIt(t *testing.T) {
	cases := []struct {
		name   string
		setup  func(*vwoFake)
		remedy string // the key the reason must point at
	}{
		{"requested with no NTE", func(f *vwoFake) {}, "n"},
		{"sourcing with no quotes", func(f *vwoFake) {
			f.wo.Status = omsapi.MaintenanceOrderSourcing
			f.wo.NTEAmount = "1200.00"
		}, "q"},
		{"in progress with no photo", func(f *vwoFake) {
			f.wo.Status = omsapi.MaintenanceOrderInProgress
			f.wo.NTEAmount = "1200.00"
		}, "a"},
		{"financial review with a blocked variance", func(f *vwoFake) {
			f.wo.Status = omsapi.MaintenanceOrderFinancialReview
			f.wo.NTEAmount = "1200.00"
			f.wo.ActualInvoiceTotal = "2400.00"
			f.wo.VarianceStatus = omsapi.MaintenanceOrderVarianceBlocked
		}, "o"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := newVWOFake()
			tc.setup(fake)
			r, s := vwoDrive(t, fake)

			advance := vwoAdvanceFor(s.wo.Status)
			if advance == vwoNone {
				t.Fatalf("no forward transition from %q — the fixture is wrong", s.wo.Status)
			}
			reason := s.vwoBlockedBy(advance)
			if reason == "" {
				t.Fatalf("the gate is open — this fixture proves nothing")
			}
			if !strings.Contains(reason, tc.remedy) {
				t.Errorf("the reason %q does not name the remedy key %q", reason, tc.remedy)
			}
			// Not named…
			for _, item := range s.sheetBar() {
				if item.Key == "Enter" {
					t.Errorf("the bar names Enter through a shut gate: %q", item.Label)
				}
			}
			// …and not bound: Enter opens nothing and sends nothing.
			before := vwoPane(t, r)
			r = vwoKey(t, r, "enter")
			if s.phase != vwoPhaseSheet {
				t.Errorf("Enter opened %v through a shut gate", s.phase)
			}
			if got := fake.posts(); len(got) != 0 {
				t.Errorf("Enter wrote through a shut gate: %v", got)
			}
			// But it still ANSWERS: a key that does nothing and says nothing
			// redraws a byte-identical pane, which is what reads as a wedged
			// program.
			if after := vwoPane(t, r); after == before {
				t.Error("Enter redrew the pane byte for byte")
			}
			// The reason is on the pane the operator is looking at.
			if !strings.Contains(vwoPane(t, r), strings.Fields(reason)[0]) {
				t.Errorf("the blocked reason is not on the pane:\n%s", vwoPane(t, r))
			}
		})
	}
}

// ---------------------------------------------------------------------------
// The bar names exactly the keys that work
// ---------------------------------------------------------------------------

// vwoPlace is the screen's POSITION and pending work — everything a key can
// move that is not the screen's answer to that key.
//
// The note is deliberately OUT of it: a key that declines and says why has not
// ACTED, and counting the note as an act would report every honest refusal as a
// bar violation.
func vwoPlace(s *VendorWorkOrderDetailScreen) string {
	status := ""
	if s.wo != nil {
		status = s.wo.Status
	}
	return fmt.Sprintf("phase=%d action=%d focus=%d kind=%d scroll=%d/%d loading=%v sending=%v status=%s",
		s.phase, s.action, s.focus, s.kindIx, s.sheetScroll, s.confirmScroll,
		s.loading, s.sending, status)
}

// vwoBarKeyNames maps a bar TOKEN to the keystrokes it SPELLS, and to no
// synonyms. A token this table does not know FAILS the sweep rather than being
// skipped: credit for a synonym is the sweep making a claim on the bar's
// behalf, which is the defect it exists to report sitting inside the check, and
// a token nobody taught it is a bar entry pressed in neither direction.
var vwoBarKeyNames = map[string][]string{
	"Enter":     {"enter"},
	"Esc":       {"esc"},
	"Ctrl-X":    {"ctrl+x"},
	"UP/DN":     {"up", "down"},
	"PgUp/PgDn": {"pgup", "pgdown"},
	"Home/End":  {"home", "end"},
	"←→":        {"left", "right"},
	"n":         {"n"},
	"q":         {"q"},
	"w":         {"w"},
	"e":         {"e"},
	"a":         {"a"},
	"k":         {"k"},
	"o":         {"o"},
	"r":         {"r"},
}

// vwoPress sends one key and reports whether it ACTED, WITHOUT settling what it
// returned.
//
// Two reasons it does not settle. A key whose whole product is a REQUEST has
// finished acting the moment it hands back the command — `r` reloads, and a
// drive that pumps the reload to completion finds the same state it started
// from and reports the refresh key as dead. And a mid-flight state has to be
// asserted while it is still mid-flight, which is this package's oldest
// measuring lesson.
//
// The NOTE is deliberately outside vwoPlace, so a key that declines and SAYS
// WHY has not acted: counting it would report every honest refusal as a bar
// violation.
func vwoPress(t *testing.T, r Root, s *VendorWorkOrderDetailScreen, k string) (Root, bool) {
	t.Helper()
	before := vwoPlace(s)
	next, cmd := r.Update(poPickerKeyMsg(k))
	after, ok := next.(Root)
	if !ok {
		t.Fatalf("Root.Update returned %T, want Root", next)
	}
	return after, vwoPlace(s) != before || cmd != nil
}

func vwoBarKeys(t *testing.T, items []actionBarItem) map[string][]string {
	t.Helper()
	out := map[string][]string{}
	for _, item := range items {
		keys, ok := vwoBarKeyNames[item.Key]
		if !ok {
			t.Fatalf("bar token %q is not in vwoBarKeyNames — add it, or the sweep "+
				"presses it in neither direction", item.Key)
		}
		out[item.Key] = keys
	}
	return out
}

// vwoRootKeys are the keystrokes the ROOT owns, which the sheet leaves it on
// purpose by not being raw-input: tab moves into the sidebar, ctrl+k opens the
// search palette, and esc is the global back step the bar names. None of them
// can move this screen's own state, so neither direction of the rule is about
// them here.
var vwoRootKeys = map[string]bool{"tab": true, "ctrl+k": true, "esc": true}

// TestVendorWO_TheSheetNamesExactlyTheKeysThatWork presses the whole KEY SPACE
// — every printable rune plus the named specials — against the sheet at every
// status a vendor order reaches, and fails a bar entry that moves nothing as
// readily as a key that acts unnamed.
//
// The key SPACE and not a vocabulary: a roster has to be edited in step with
// the code, and a key missing from one is pressed in NEITHER direction, which
// is exactly how three dead keys reached an operator's terminal in this package
// before (AGENTS.md records all three).
//
// THE TWO HALVES ARE ASKED AT DIFFERENT GRANULARITIES, and getting that wrong
// reports honest screens. A token names a PAIR, so FORWARD it is asked per
// TOKEN — press both keys it spells and require SOME key to move, which is why
// a body resting at the top stays silent on `up` without failing. REVERSE is
// asked per KEY from the rest state, because "a key that acts must be named"
// cannot be answered about a pair.
func TestVendorWO_TheSheetNamesExactlyTheKeysThatWork(t *testing.T) {
	statuses := []string{
		omsapi.MaintenanceOrderRequested,
		omsapi.MaintenanceOrderSourcing,
		omsapi.MaintenanceOrderScheduled,
		omsapi.MaintenanceOrderInProgress,
		omsapi.MaintenanceOrderValidated,
		omsapi.MaintenanceOrderFinancialReview,
		omsapi.MaintenanceOrderClosed,
	}
	// Every gate open and a keyfob out, so the greatest number of keys is live
	// and the sweep is not quietly measuring a bar with nothing on it.
	seed := func(f *vwoFake, status string) {
		f.wo.Status = status
		f.wo.NTEAmount = "1200.00"
		f.wo.KeyfobID = "KF-0042"
		f.wo.ActualInvoiceTotal = "1425.50"
		f.wo.QuoteWaiverSignedAt = &f.wo.OpenedAt
		f.wo.Attachments = []omsapi.MaintenanceOrderAttachment{
			{ID: "a1", Kind: omsapi.MaintenanceOrderAttachmentPhoto},
			{ID: "a2", Kind: omsapi.MaintenanceOrderAttachmentInvoice},
			{ID: "a3", Kind: omsapi.MaintenanceOrderAttachmentFSR},
		}
	}

	tokens, moved, acted := 0, 0, 0
	for _, status := range statuses {
		t.Run(status, func(t *testing.T) {
			rest := func() (Root, *VendorWorkOrderDetailScreen) {
				fake := newVWOFake()
				seed(fake, status)
				return vwoDrive(t, fake)
			}

			// FORWARD, per token.
			_, probe := rest()
			for token, keys := range vwoBarKeys(t, probe.sheetBar()) {
				tokens++
				if len(keys) == 1 && vwoRootKeys[keys[0]] {
					continue
				}
				r, s := rest()
				some := false
				for _, k := range keys {
					var did bool
					r, did = vwoPress(t, r, s, k)
					some = some || did
				}
				if !some {
					t.Errorf("%s: the bar names %q (%v) and no key it spells moved anything",
						status, token, keys)
					continue
				}
				moved++
			}

			// REVERSE, per key from the rest state.
			for _, k := range poKeySpace() {
				if vwoRootKeys[k] {
					continue
				}
				r, s := rest()
				named := false
				for _, keys := range vwoBarKeys(t, s.sheetBar()) {
					for _, spelled := range keys {
						if spelled == k {
							named = true
						}
					}
				}
				if _, did := vwoPress(t, r, s, k); !did {
					continue
				}
				acted++
				if !named {
					t.Errorf("%s: %q acted and the bar does not name it", status, k)
				}
			}
		})
	}
	// Both halves must be REACHABLE, or the biconditional above is a pair of
	// implications neither of which ever fired.
	if tokens == 0 || moved == 0 || acted == 0 {
		t.Fatalf("tokens=%d moved=%d acted=%d — the sweep never exercised both directions",
			tokens, moved, acted)
	}
}

// ---------------------------------------------------------------------------
// The identifier
// ---------------------------------------------------------------------------

// TestVendorWO_TheOrderIdIsShownWholeAtEveryWidth.
//
// A UUID is 36 cells and the pane at 80 columns is 51, so an identifier drawn
// on a labelled row does not fit beside its own label. The answer is to change
// the LAYOUT rather than the identifier — the label takes a row and the id sits
// under it — and where even that will not do the id is packed across lines at
// its hyphens. What may never happen is a clipped id that reads as a whole one.
func TestVendorWO_TheOrderIdIsShownWholeAtEveryWidth(t *testing.T) {
	for _, w := range jdeDrawableWidths() {
		room := screenBodyCells(w)
		lines := vwoIDLines(vwoTestID, room)
		if len(lines) == 0 {
			t.Fatalf("width %d: no id lines at all", w)
		}
		var rebuilt strings.Builder
		for _, line := range lines {
			rebuilt.WriteString(strings.TrimLeft(line, " "))
		}
		if rebuilt.String() != vwoTestID {
			t.Errorf("width %d: the id reassembles to %q, not the id — it was cut or padded",
				w, rebuilt.String())
		}
		if strings.Contains(rebuilt.String(), "…") {
			t.Errorf("width %d: the id carries an ellipsis", w)
		}
	}

	// And on the pane the interface is modelled on, it is one unbroken run.
	fake := newVWOFake()
	r, _ := vwoDrive(t, fake)
	if !strings.Contains(vwoPane(t, r), vwoTestID) {
		t.Errorf("the 80-column pane does not carry the whole order id:\n%s", vwoPane(t, r))
	}
}

// ---------------------------------------------------------------------------
// A prompt outliving the state it was opened on
// ---------------------------------------------------------------------------

// TestVendorWO_ARefreshClosesAPromptTheOrderNoLongerAllows.
//
// A reload can land UNDER an open prompt, and this is the sequence that does
// it: `r` on the sheet puts a GET in flight, the operator opens an action
// before it answers, and the order that comes back has moved on in the browser
// meanwhile. The flag a prompt was opened on is therefore read AGAIN on every
// refresh, never cached across one — a confirm left offering Ctrl-X for a
// transition the server has since made impossible is a frame whose only
// possible outcome is a refusal the operator did nothing to earn.
//
// The load command is held rather than pumped, which is what makes this the
// real sequence and not a hand-set flag: `r` is pressed, its command is kept,
// `n` is pressed while it is still out, and only then is the reply fed back.
func TestVendorWO_ARefreshClosesAPromptTheOrderNoLongerAllows(t *testing.T) {
	fake := newVWOFake()
	fake.wo.Status = omsapi.MaintenanceOrderRequested
	r, s := vwoDrive(t, fake)

	// r: the reload goes out and its command is HELD.
	next, reload := r.Update(poPickerKeyMsg("r"))
	r = next.(Root)
	if reload == nil {
		t.Fatal("r issued no reload")
	}

	// The order advances elsewhere, past the window set-nte is allowed in.
	fake.mu.Lock()
	fake.wo.Status = omsapi.MaintenanceOrderScheduled
	fake.mu.Unlock()

	// n: set-NTE was legal when the sheet was drawn, so the form opens.
	next, _ = r.Update(poPickerKeyMsg("n"))
	r = next.(Root)
	if s.phase != vwoPhaseForm {
		t.Fatalf("phase = %v, want the NTE form", s.phase)
	}

	// The reply lands under the open form.
	r = pump(t, r, reload, 0)

	if s.wo.Status != omsapi.MaintenanceOrderScheduled {
		t.Fatalf("the reload did not land: status = %q", s.wo.Status)
	}
	if s.phase != vwoPhaseSheet {
		t.Fatalf("phase = %v after the refresh, want the sheet — the prompt outlived its state", s.phase)
	}
	if s.note == "" {
		t.Fatal("the prompt closed in silence")
	}
	if !strings.Contains(vwoPane(t, r), "no longer available") {
		t.Errorf("the pane does not say why the prompt went:\n%s", vwoPane(t, r))
	}
}

// ---------------------------------------------------------------------------
// The list
// ---------------------------------------------------------------------------

// TestVendorWOList_RowsCarryIdentityAndTheEmergencyMark.
//
// is_emergency is not a status: it rides alongside every status and is what
// says the NTE and three-quote gates were bypassed. Two orders both reading
// "(sourcing)" are different kinds of order when one carries it, and it is the
// kind whose money nobody capped — so the row says so.
func TestVendorWOList_RowsCarryIdentityAndTheEmergencyMark(t *testing.T) {
	fake := newVWOFake()
	fake.wo.Status = omsapi.MaintenanceOrderSourcing
	fake.wo.IsEmergency = true
	srv := httptest.NewServer(fake.handler())
	defer srv.Close()

	deps := Deps{OMS: omsapi.New(srv.URL), Ctx: context.Background()}
	rows, err := vendorWorkOrderRows(context.Background(), deps, nil)
	if err != nil {
		t.Fatalf("vendorWorkOrderRows: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	row := rows[0]
	if row.ID != vwoTestID {
		t.Errorf("row id = %q, want the order's UUID whole", row.ID)
	}
	if !strings.Contains(row.Title, "compressor belt") {
		t.Errorf("row title = %q", row.Title)
	}
	if row.Tag != omsapi.MaintenanceOrderSourcing {
		t.Errorf("row tag = %q", row.Tag)
	}
	for _, want := range []string{"TPWO-C0FFEE00", "emergency", "Northern Industrial"} {
		if !strings.Contains(row.Subtitle, want) {
			t.Errorf("subtitle %q does not carry %q", row.Subtitle, want)
		}
	}
	// NO MONEY on a list row: it is drawn straight into a pane clampToBox cuts
	// from the right with no mark, and a cut figure reads as a smaller one.
	if strings.Contains(row.Subtitle, "$") {
		t.Errorf("a list row carries a money figure it cannot bound: %q", row.Subtitle)
	}
}

// TestVendorWOList_TheFilterViewsAreTheServersOwn.
//
// ThirdPartyWorkOrderViewSet.filterset_fields carries `status`, so `f` is a
// real ?status= narrowing of the whole set. A LOCAL filter could only ever
// narrow whichever rows page one happened to contain, which is how a draft
// purchase order became unreachable before the PO list was made filter-driven.
func TestVendorWOList_TheFilterViewsAreTheServersOwn(t *testing.T) {
	seen := map[string]bool{}
	for _, f := range vendorWorkOrderFilters {
		if f.query == nil {
			continue
		}
		status := f.query.Get("status")
		if status == "" {
			t.Errorf("filter %q carries a query with no status", f.label)
		}
		if seen[status] {
			t.Errorf("two filters both ask for %q", status)
		}
		seen[status] = true
		if _, ok := vwoStatusLabels[status]; !ok {
			t.Errorf("filter %q asks for %q, which is not a ThirdPartyWorkOrder status",
				f.label, status)
		}
	}
	if vendorWorkOrderFilters[0].query != nil {
		t.Error("filters[0] must be the unfiltered view, or the landing list is narrowed")
	}
	// Every live status is reachable: a status with no view is a set of orders
	// no filter can bring up.
	for status := range vwoStatusLabels {
		if !seen[status] {
			t.Errorf("no filter view reaches %q", status)
		}
	}
}

// ---------------------------------------------------------------------------
// Fixtures for the columnar sweeps
// ---------------------------------------------------------------------------

// vwoRichOrder is the order the geometry sweeps are run against: every block
// the sheet can draw, at the lengths OMS really serves.
//
// It is deliberately the LONGEST sheet this screen has — full names, three
// quotes, three attachments, a cost split, a downtime window and two note
// blocks — because the layer's promise is about a body that OUTRUNS the pane,
// and a fixture that fits proves nothing about the window over one that does
// not.
func vwoRichOrder() *omsapi.MaintenanceOrder {
	wo := vwoTestOrder()
	wo.Status = omsapi.MaintenanceOrderFinancialReview
	wo.StatusDisplay = "Financial Review"
	wo.NTEAmount, wo.ActualInvoiceTotal, wo.DispatchFee = "1200.00", "1425.50", "95.00"
	wo.VarianceStatus = omsapi.MaintenanceOrderVarianceBlocked
	wo.KeyfobID = "KF-0042"
	wo.WarrantyRecovery = true
	wo.TotalDowntime = "04:30:00"
	start := wo.OpenedAt.Add(6 * time.Hour)
	end := start.Add(4*time.Hour + 30*time.Minute)
	wo.DowntimeStart, wo.DowntimeEnd = &start, &end
	signed := wo.OpenedAt.Add(2 * time.Hour)
	wo.QuoteWaiverSignedAt = &signed
	wo.QuoteWaiverReason = "Sole authorized service provider for this spindle assembly in the region."
	wo.InternalNotes = "Third call-out this year; Logistics to review the service agreement."
	for i := 0; i < 3; i++ {
		wo.Quotes = append(wo.Quotes, omsapi.MaintenanceOrderQuote{
			ID: fmt.Sprintf("quote-%d", i+1), Vendor: wo.Vendor,
			VendorName: wo.VendorName,
			Amount:     omsapi.DecimalString(fmt.Sprintf("%d.00", 1180+i*45)),
		})
	}
	for i, kind := range []string{
		omsapi.MaintenanceOrderAttachmentPhoto,
		omsapi.MaintenanceOrderAttachmentInvoice,
		omsapi.MaintenanceOrderAttachmentFSR,
	} {
		wo.Attachments = append(wo.Attachments, omsapi.MaintenanceOrderAttachment{
			ID: fmt.Sprintf("att-%d", i+1), Kind: kind, KindDisplay: kind,
			Caption: "Northern Industrial " + kind + " for the spindle rebuild",
			File:    fmt.Sprintf("/media/third_party_work_orders/%s/%s.pdf", vwoTestID, kind),
		})
	}
	wo.AssetLinks = []omsapi.MaintenanceOrderAssetLink{{
		ID: "link-1", Asset: *wo.Asset, AssetName: wo.AssetName, AssetTag: "AST-0031",
		SharePct: "100.00", AllocatedCost: "1425.50",
	}}
	wo.Workflow = omsapi.MaintenanceOrderWorkflow{
		HasNTE: true, HasRequiredQuotes: true, QuoteCount: 3,
		HasPhotoEvidence: true, HasInvoiceAndFSR: true,
		VarianceStatus: omsapi.MaintenanceOrderVarianceBlocked, KeyfobOutstanding: true,
	}
	return &wo
}

// vwoPaneFixture builds the screen PAST its loading state, with no server
// behind it — a screen still fetching draws one muted line and no frame at all,
// and a fixture that renders nothing proves nothing.
func vwoPaneFixture(mut func(*omsapi.MaintenanceOrder)) *VendorWorkOrderDetailScreen {
	s := NewVendorWorkOrderDetailScreen(Deps{}, vwoTestID)
	s.loading = false
	s.wo = vwoRichOrder()
	if mut != nil {
		mut(s.wo)
	}
	return s
}

// vwoFormFixture opens one action's form with the rows already typed into, so
// the sweeps measure the frame an operator is actually looking at rather than
// an empty one.
func vwoFormFixture(a vwoAction, typed ...string) *VendorWorkOrderDetailScreen {
	s := vwoPaneFixture(nil)
	s.openAction(a)
	for i, v := range typed {
		if i < len(s.inputs) {
			s.inputs[i].SetValue(v)
		}
	}
	return s
}

func vwoConfirmFixture(a vwoAction, typed ...string) *VendorWorkOrderDetailScreen {
	s := vwoFormFixture(a, typed...)
	s.phase = vwoPhaseConfirm
	return s
}
