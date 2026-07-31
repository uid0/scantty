package tui

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/uid0/scantty/internal/omsapi"
)

// Editable PO header + derived payment schedule (op-bwo9).
//
// The header is saved as one PATCH, so the thing to protect is what that PATCH
// says about fields the operator did NOT touch: the date row shows a day where
// the column holds a timestamp, and a choice row can only show a token this
// build knows, so either one sent back unchanged would rewrite a field nobody
// edited. The schedule is read-only in both directions — the backend derives it
// and the screen prints it, including the null due date that means "nothing to
// anchor to yet".

func poTermsPO() *omsapi.PurchaseOrder {
	return &omsapi.PurchaseOrder{
		ID:                   "po-1",
		Number:               "PO-2026-0042",
		Status:               "sent",
		SupplierDetails:      "Acme Supply",
		SupplierOrderNumber:  "SUP-1",
		SalesOrderNumber:     "SO-1",
		OrderDate:            time.Date(2026, 7, 15, 14, 32, 11, 0, time.UTC),
		ExpectedDeliveryDate: "2026-08-04",
		Priority:             "urgent",
		PaymentTerms:         "net_30",
		FreightTerms:         "fob_destination",
		Notes:                "handle with care",
		EstimatedTotal:       omsapi.DecimalString("1234.56"),
		PaymentSchedule: &omsapi.POPaymentSchedule{
			DueDate: "2026-08-14",
			Amount:  omsapi.DecimalString("1234.56"),
			Basis:   "Net 30 from order date",
		},
		Items: []omsapi.PurchaseOrderItem{
			{ID: "line-1", Description: "Widget", QuantityOrdered: 5},
		},
	}
}

// poTermsEditScreen wires an edit screen to a stub that captures the PATCH body.
func poTermsEditScreen(t *testing.T, po *omsapi.PurchaseOrder) (*PurchaseOrderEditScreen, *map[string]any) {
	t.Helper()
	body := map[string]any{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		raw, _ := io.ReadAll(r.Body)
		for k := range body {
			delete(body, k)
		}
		_ = json.Unmarshal(raw, &body)
		_, _ = w.Write([]byte(`{"id":"po-1","po_number":"PO-2026-0042"}`))
	}))
	t.Cleanup(srv.Close)
	return NewPurchaseOrderEditScreen(Deps{OMS: omsapi.New(srv.URL)}, po), &body
}

// ---------------------------------------------------------------------------
// Edit screen
// ---------------------------------------------------------------------------

func TestPOEditTerms_HydratesTheHeader(t *testing.T) {
	s, _ := poTermsEditScreen(t, poTermsPO())

	// The day the order was placed, not the timestamp underneath it: that is
	// what the field is for and what the detail screen prints.
	if got := s.meta[poMetaOrderDate].Value(); got != "2026-07-15" {
		t.Errorf("date ordered = %q, want 2026-07-15", got)
	}
	for row, want := range map[int]string{
		poMetaPriority:     "urgent",
		poMetaPaymentTerms: "net_30",
		poMetaFreightTerms: "fob_destination",
	} {
		sel := s.selects[row]
		if sel.picked() != want || sel.original != want {
			t.Errorf("row %d parked on %q (original %q), want %q", row, sel.picked(), sel.original, want)
		}
	}

	out := s.viewForm()
	for _, want := range []string{"Date ordered", "2026-07-15", "Priority: ", "Urgent",
		"Payment terms: ", "Net 30", "Freight terms: ", "FOB Destination"} {
		if !strings.Contains(out, want) {
			t.Errorf("edit form missing %q:\n%s", want, out)
		}
	}
}

// TestPOEditTerms_UntouchedHeaderStaysOffTheWire is the load-bearing one: an
// operator who opens the form to fix the notes must not silently flatten the
// order date to midnight or re-assert terms they never looked at.
func TestPOEditTerms_UntouchedHeaderStaysOffTheWire(t *testing.T) {
	s, body := poTermsEditScreen(t, poTermsPO())
	s.meta[poMetaNotes].SetValue("now with tracking")

	if msg, ok := s.saveMetadata()().(poEditSavedMsg); !ok || msg.err != nil {
		t.Fatalf("save failed: %#v", msg)
	}
	for _, key := range []string{"order_date", "priority", "payment_terms", "freight_terms"} {
		if v, present := (*body)[key]; present {
			t.Errorf("untouched %s reached the wire (%v): %v", key, v, *body)
		}
	}
	if (*body)["notes"] != "now with tracking" {
		t.Errorf("notes = %v", (*body)["notes"])
	}
}

// TestPOEditTerms_CyclingAChoiceSavesItWithTheHeader: the choice rows stage like
// the text fields around them, so one enter writes the header as one PATCH —
// and still says nothing about the rows the operator left alone.
func TestPOEditTerms_CyclingAChoiceSavesItWithTheHeader(t *testing.T) {
	s, body := poTermsEditScreen(t, poTermsPO())

	s.cursor = poMetaFreightTerms
	s.syncFocus()
	s.updateForm(tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}})
	if got := s.selects[poMetaFreightTerms].picked(); got != "prepaid" {
		t.Fatalf("space should advance the freight row to prepaid, got %q", got)
	}
	s.updateForm(tea.KeyMsg{Type: tea.KeyLeft})
	if got := s.selects[poMetaFreightTerms].picked(); got != "fob_destination" {
		t.Fatalf("← should walk back to fob_destination, got %q", got)
	}
	s.updateForm(tea.KeyMsg{Type: tea.KeyRight})

	// Enter on the choice row itself saves the header — every row in this band
	// saves it, which is why the terms live here rather than writing alone.
	scr, cmd := s.updateForm(tea.KeyMsg{Type: tea.KeyEnter})
	if _, isEdit := scr.(*PurchaseOrderEditScreen); !isEdit {
		t.Fatalf("enter should stay on the edit screen, got %T", scr)
	}
	if cmd == nil {
		t.Fatal("enter on a choice row should save the header")
	}
	if msg, ok := cmd().(poEditSavedMsg); !ok || msg.err != nil {
		t.Fatalf("save failed: %#v", msg)
	}

	if got, _ := (*body)["freight_terms"].(string); got != "prepaid" {
		t.Errorf("freight_terms = %v, want prepaid", (*body)["freight_terms"])
	}
	for _, key := range []string{"priority", "payment_terms", "order_date"} {
		if v, present := (*body)[key]; present {
			t.Errorf("changing freight must not touch %s (got %v)", key, v)
		}
	}
	// The text fields still ride along — the terms joined the header save, they
	// did not replace it.
	if (*body)["supplier_order_number"] != "SUP-1" || (*body)["notes"] != "handle with care" {
		t.Errorf("the rest of the header should still be sent: %v", *body)
	}
}

// TestPOEditTerms_ClearingATermSendsAnEmptyString: "not agreed" is a value this
// column stores, so undoing a mis-set term sends "" — not the null the
// association fields use to detach, which this CharField would reject.
func TestPOEditTerms_ClearingATermSendsAnEmptyString(t *testing.T) {
	s, body := poTermsEditScreen(t, poTermsPO())

	sel := s.selects[poMetaPaymentTerms]
	sel.idx = selectIndexOf(sel.opts, "")
	if sel.picked() != "" {
		t.Fatalf("the payment-terms row should offer a blank option, got %q", sel.picked())
	}
	if msg, ok := s.saveMetadata()().(poEditSavedMsg); !ok || msg.err != nil {
		t.Fatalf("save failed: %#v", msg)
	}

	v, present := (*body)["payment_terms"]
	if !present {
		t.Fatalf("clearing a term must send it: %v", *body)
	}
	if v != "" {
		t.Errorf("payment_terms = %#v, want an empty string (not null)", v)
	}
}

func TestPOEditTerms_ChangedOrderDateGoesOutAsATimestamp(t *testing.T) {
	s, body := poTermsEditScreen(t, poTermsPO())
	s.meta[poMetaOrderDate].SetValue("2026-06-30")

	if msg, ok := s.saveMetadata()().(poEditSavedMsg); !ok || msg.err != nil {
		t.Fatalf("save failed: %#v", msg)
	}
	// DRF's DateTimeField does not take a bare date; the backend runs on UTC, so
	// midnight UTC is the day the operator typed.
	if got, _ := (*body)["order_date"].(string); got != "2026-06-30T00:00:00Z" {
		t.Errorf("order_date = %v, want 2026-06-30T00:00:00Z", (*body)["order_date"])
	}
}

func TestPOEditTerms_OrderDateValidation(t *testing.T) {
	for _, tc := range []struct{ name, value string }{
		{"not a date", "last tuesday"},
		{"cleared", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, body := poTermsEditScreen(t, poTermsPO())
			s.meta[poMetaOrderDate].SetValue(tc.value)
			s.saveMetadata()
			if s.errMsg == "" {
				t.Fatalf("%q should be rejected", tc.value)
			}
			if len(*body) != 0 {
				t.Errorf("a rejected date must not send anything: %v", *body)
			}
		})
	}
	// A full timestamp is legitimate — an operator recording the hour an order
	// actually went out — and passes through untouched.
	s, body := poTermsEditScreen(t, poTermsPO())
	s.meta[poMetaOrderDate].SetValue("2026-06-30T09:15:00Z")
	if msg, ok := s.saveMetadata()().(poEditSavedMsg); !ok || msg.err != nil {
		t.Fatalf("save failed: %#v", msg)
	}
	if got, _ := (*body)["order_date"].(string); got != "2026-06-30T09:15:00Z" {
		t.Errorf("order_date = %v, want the timestamp verbatim", (*body)["order_date"])
	}
}

// TestPOEditTerms_UnrecognizedTokenIsGraftedNotFlattened: if the backend grows a
// fifth priority, an order carrying it must say so rather than reading as "Low"
// — and an unrelated save must not write that misreading back.
func TestPOEditTerms_UnrecognizedTokenIsGraftedNotFlattened(t *testing.T) {
	po := poTermsPO()
	po.Priority = "critical"
	s, body := poTermsEditScreen(t, po)

	sel := s.selects[poMetaPriority]
	if sel.picked() != "critical" {
		t.Fatalf("priority row = %q, want the stored token grafted in", sel.picked())
	}
	if len(sel.opts) != len(poPriorityOptions)+1 {
		t.Errorf("the unknown token should be grafted into the options: %+v", sel.opts)
	}
	if out := s.viewForm(); !strings.Contains(out, "critical") {
		t.Errorf("the form should show the stored token:\n%s", out)
	}
	if msg, ok := s.saveMetadata()().(poEditSavedMsg); !ok || msg.err != nil {
		t.Fatalf("save failed: %#v", msg)
	}
	if v, present := (*body)["priority"]; present {
		t.Errorf("an untouched unknown priority must not be written back (got %v)", v)
	}
}

// TestPOEditTerms_ChoiceRowTakesNoTyping: a choice row has no input to focus, so
// a stray letter must not land in the field above or below it.
func TestPOEditTerms_ChoiceRowTakesNoTyping(t *testing.T) {
	s, _ := poTermsEditScreen(t, poTermsPO())
	s.cursor = poMetaPriority
	s.syncFocus()

	if s.meta[poMetaPriority].Focused() {
		t.Error("a choice row must not focus a text input")
	}
	s.updateForm(runeKey('x'))
	for i := 0; i < poEditMetaCount; i++ {
		if strings.Contains(s.meta[i].Value(), "x") && i != poMetaNotes {
			t.Errorf("typing on a choice row leaked into row %d: %q", i, s.meta[i].Value())
		}
	}
	if s.selects[poMetaPriority].picked() != "urgent" {
		t.Errorf("a letter should not move the choice: %q", s.selects[poMetaPriority].picked())
	}
}

func TestPOTermsOptions_GraftsOnlyRealTokens(t *testing.T) {
	if got := poTermsOptions(poPaymentTermsOptions, "net_30"); len(got) != len(poPaymentTermsOptions) {
		t.Errorf("a known token needs no graft: %+v", got)
	}
	// Blank is a row the payment/freight sets already have, and for priority — a
	// set with no blank row — it means "this order reported nothing", which is a
	// caller's fallback, not an option to offer.
	if got := poTermsOptions(poPriorityOptions, ""); len(got) != len(poPriorityOptions) {
		t.Errorf("an empty token must not be grafted: %+v", got)
	}
	if got := poTermsLabel(poFreightTermsOptions, "hyperloop"); got != "hyperloop" {
		t.Errorf("an unknown token should name itself, got %q", got)
	}
}

// ---------------------------------------------------------------------------
// Detail screen
// ---------------------------------------------------------------------------

func TestPODetailTerms_RendersTermsAndSchedule(t *testing.T) {
	s := &PurchaseOrderDetailScreen{po: poTermsPO()}
	out := s.renderBody()

	for _, want := range []string{
		"Terms",
		"Priority: ", "Urgent",
		"Payment terms: ", "Net 30",
		"Freight terms: ", "FOB Destination",
		"Payment: ", "$1234.56 due 2026-08-14 · Net 30 from order date",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("PO detail missing %q:\n%s", want, out)
		}
	}
	// A rush order says so at the top, where the status is, rather than only
	// eight rows down in the Terms section.
	head := strings.SplitN(out, "\n", 3)[1]
	if !strings.Contains(head, "Urgent priority") {
		t.Errorf("status line should badge a non-default priority, got %q", head)
	}
}

// TestPODetailTerms_UnagreedTermsSaySo: an order with no terms yet is the common
// starting state, and the schedule still carries the amount plus the reason it
// has no date. Silence would read as "there is nothing to pay".
func TestPODetailTerms_UnagreedTermsSaySo(t *testing.T) {
	po := poTermsPO()
	po.Priority = "normal"
	po.PaymentTerms = ""
	po.FreightTerms = ""
	po.PaymentSchedule = &omsapi.POPaymentSchedule{
		Amount: omsapi.DecimalString("1234.56"),
		Basis:  "No payment terms set",
	}
	out := (&PurchaseOrderDetailScreen{po: po}).renderBody()

	if !strings.Contains(out, "Payment terms: ") || !strings.Contains(out, "— not agreed —") {
		t.Errorf("unset terms should say so:\n%s", out)
	}
	if !strings.Contains(out, "$1234.56 · No payment terms set") {
		t.Errorf("a schedule with no due date should still show amount + reason:\n%s", out)
	}
	if strings.Contains(out, "due ") {
		t.Errorf("there is no due date to print:\n%s", out)
	}
	// "normal" is every order's starting priority — badging it would put a
	// meaningless chunk on every PO.
	head := strings.SplitN(out, "\n", 3)[1]
	if strings.Contains(head, "priority") {
		t.Errorf("the default priority should not be badged, got %q", head)
	}
	if !strings.Contains(out, "Priority: ") || !strings.Contains(out, "Normal") {
		t.Errorf("the Terms section should still name it:\n%s", out)
	}
}

// TestPODetailTerms_OrderWithoutTermsShowsNoSection: an order from a backend
// older than op-bwo9 carries none of these fields, and inventing three "not
// agreed" rows for it would be the client answering a question it never asked.
func TestPODetailTerms_OrderWithoutTermsShowsNoSection(t *testing.T) {
	po := poTermsPO()
	po.Priority, po.PaymentTerms, po.FreightTerms, po.PaymentSchedule = "", "", "", nil
	out := (&PurchaseOrderDetailScreen{po: po}).renderBody()

	for _, absent := range []string{"Terms", "Priority: ", "Payment terms: ", "Payment: "} {
		if strings.Contains(out, absent) {
			t.Errorf("an order with no terms should not render %q:\n%s", absent, out)
		}
	}
	// The rest of the detail is untouched.
	if !strings.Contains(out, "Totals") || !strings.Contains(out, "PO-2026-0042") {
		t.Errorf("the rest of the PO should still render:\n%s", out)
	}
}
