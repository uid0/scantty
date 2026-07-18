package tui

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/uid0/scantty/internal/omsapi"
)

// TestConsumeModal_Flow drives the three-step state machine with no OMS:
// open (u) → digit gating → qty → committee move/clamp → notes → esc-cancel,
// plus positive-qty validation. Reuses runeKey/ccEnterKey/ccEscKey from the
// sibling detail tests.
func TestConsumeModal_Flow(t *testing.T) {
	s := NewInventoryDetailScreen(Deps{}, "abc")
	s.item = &omsapi.Item{ID: "abc", Name: "Widget", SKU: "W-1", UnitCost: "2.50"}
	s.loading = false

	// 'u' collides with the global ForgeKey-Usage hotkey, so the screen must
	// claim it to open the consume modal instead.
	if !s.HandlesKey("u") {
		t.Fatalf("screen should claim 'u' so it beats the global Usage hotkey")
	}

	s.Update(runeKey('u'))
	if s.cnStep != consumeStepQty {
		t.Fatalf("u should open the qty step, got %d", s.cnStep)
	}
	if !s.WantsRawInput() {
		t.Errorf("open modal should claim raw input")
	}

	// Non-digit is ignored in the qty step; digits accumulate.
	s.Update(runeKey('x'))
	if s.cnQty.Value() != "" {
		t.Errorf("non-digit should be ignored, got %q", s.cnQty.Value())
	}
	s.Update(runeKey('3'))
	if s.cnQty.Value() != "3" {
		t.Errorf("qty = %q, want 3", s.cnQty.Value())
	}

	s.Update(ccEnterKey())
	if s.cnStep != consumeStepSIG {
		t.Fatalf("enter should advance to the committee step, got %d", s.cnStep)
	}
	if s.cnSIGIx != 0 {
		t.Errorf("committee should default to the none row (0), got %d", s.cnSIGIx)
	}

	// Populate the pick-list as the async load would, then navigate it.
	s.Update(consumeSIGsLoadedMsg{sigs: []omsapi.SIG{{ID: 7, Name: "Metal SIG"}, {ID: 9, Name: "Wood SIG"}}})
	if s.cnLoadingSIGs {
		t.Errorf("loaded message should clear the loading flag")
	}
	s.Update(runeKey('j'))
	if s.cnSIGIx != 1 {
		t.Errorf("j should move to the first committee, got %d", s.cnSIGIx)
	}
	if sig, ok := s.selectedConsumeSIG(); !ok || sig.ID != 7 {
		t.Errorf("selected committee = %+v ok=%v, want ID 7", sig, ok)
	}
	// Down clamps at the last committee (index len(cnSIGs)).
	s.Update(runeKey('j'))
	s.Update(runeKey('j'))
	if s.cnSIGIx != 2 {
		t.Errorf("j should clamp at the last committee, got %d", s.cnSIGIx)
	}
	// Up clamps at the none row (index 0).
	s.Update(runeKey('k'))
	s.Update(runeKey('k'))
	s.Update(runeKey('k'))
	if s.cnSIGIx != 0 {
		t.Errorf("k should clamp at the none row, got %d", s.cnSIGIx)
	}
	if _, ok := s.selectedConsumeSIG(); ok {
		t.Errorf("none row should select no committee")
	}

	s.Update(ccEnterKey())
	if s.cnStep != consumeStepNotes {
		t.Fatalf("enter should advance to notes, got %d", s.cnStep)
	}

	s.Update(ccEscKey())
	if s.cnStep != consumeStepNone {
		t.Errorf("esc should cancel, step = %d", s.cnStep)
	}
	if s.WantsRawInput() {
		t.Errorf("raw input should be released after cancel")
	}

	// Zero qty does not advance (must be a positive whole number).
	s.Update(runeKey('u'))
	s.Update(runeKey('0'))
	s.Update(ccEnterKey())
	if s.cnStep != consumeStepQty {
		t.Errorf("zero qty should stay on the qty step, got %d", s.cnStep)
	}
	if s.cnErr == "" {
		t.Errorf("zero qty should set a validation error")
	}
}

// TestConsumeModal_Submits drives the modal to submission against a stub server
// and verifies the request carries the collected quantity/charged_group/notes,
// the success status line reflects the posted charge, and success closes the
// modal and triggers a re-fetch.
func TestConsumeModal_Submits(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && strings.Contains(r.URL.Path, "log_usage") {
			raw, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(raw, &body)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":1,"charged_group":7,"unit_cost":"2.50",
				"total_cost":"5.00","ledger_transaction":42}`))
			return
		}
		// Re-fetch (GET item / metrics) after success — answer minimally.
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"abc","name":"Widget","current_stock":8}`))
	}))
	defer srv.Close()

	s := NewInventoryDetailScreen(Deps{OMS: omsapi.New(srv.URL)}, "abc")
	s.item = &omsapi.Item{ID: "abc", Name: "Widget", SKU: "W-1", Stock: 10, UnitCost: "2.50"}
	s.loading = false

	s.openConsume()
	s.Update(runeKey('2'))
	s.Update(ccEnterKey()) // → committee
	s.Update(consumeSIGsLoadedMsg{sigs: []omsapi.SIG{{ID: 7, Name: "Metal SIG"}}})
	s.Update(runeKey('j')) // pick Metal SIG
	s.Update(ccEnterKey()) // → notes
	s.Update(runeKey('u')) // notes = "up"
	s.Update(runeKey('p'))
	_, cmd := s.Update(ccEnterKey()) // submit
	if cmd == nil {
		t.Fatal("submit should return a command")
	}
	if !s.cnPending {
		t.Errorf("submit should mark the modal pending")
	}
	msg := cmd()
	done, ok := msg.(consumeDoneMsg)
	if !ok {
		t.Fatalf("expected consumeDoneMsg, got %T", msg)
	}
	if done.err != nil {
		t.Fatalf("submit error: %v", done.err)
	}
	if body["quantity"].(float64) != 2 {
		t.Errorf("quantity = %v, want 2", body["quantity"])
	}
	if body["charged_group"].(float64) != 7 {
		t.Errorf("charged_group = %v, want 7", body["charged_group"])
	}
	if body["notes"] != "up" {
		t.Errorf("notes = %v, want up", body["notes"])
	}

	// Status line reflects the posted charge (amount + committee).
	text, level := consumeStatusLine(done)
	if level != StatusOK {
		t.Errorf("charged usage should be StatusOK, got %d (text=%q)", level, text)
	}
	if !strings.Contains(text, "$5.00") || !strings.Contains(text, "Metal SIG") {
		t.Errorf("status = %q, want charge amount + committee", text)
	}

	// Success closes the modal and triggers a reload command.
	_, reload := s.Update(done)
	if s.cnStep != consumeStepNone {
		t.Errorf("success should close the modal, step = %d", s.cnStep)
	}
	if reload == nil {
		t.Fatal("success should trigger a reload command")
	}
}

// TestConsumeModal_NoChargeBody omits charged_group when the "— none —" row is
// selected — the backend then records usage without posting a charge.
func TestConsumeModal_NoChargeBody(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && strings.Contains(r.URL.Path, "log_usage") {
			raw, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(raw, &body)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":1,"unit_cost":"2.50","total_cost":"5.00"}`))
	}))
	defer srv.Close()

	s := NewInventoryDetailScreen(Deps{OMS: omsapi.New(srv.URL)}, "abc")
	s.item = &omsapi.Item{ID: "abc", Name: "Widget", UnitCost: "2.50"}
	s.loading = false

	s.openConsume()
	s.Update(runeKey('2'))
	s.Update(ccEnterKey()) // → committee (stays on the none row)
	s.Update(ccEnterKey()) // → notes
	_, cmd := s.Update(ccEnterKey())
	if _, ok := cmd().(consumeDoneMsg); !ok {
		t.Fatalf("submit should produce a consumeDoneMsg")
	}
	if _, present := body["charged_group"]; present {
		t.Errorf("no committee picked → charged_group must be omitted, got %v", body["charged_group"])
	}
}

// TestConsumeStatusLine covers the charge/warning/no-charge branches, including a
// warning downgrading the level and a nil response staying readable.
func TestConsumeStatusLine(t *testing.T) {
	cases := []struct {
		name      string
		m         consumeDoneMsg
		wantLevel StatusLevel
		wantSub   []string
	}{
		{
			name:      "charged with total",
			m:         consumeDoneMsg{qty: 2, itemName: "Widget", chargedName: "Metal SIG", res: &omsapi.LogUsageResult{TotalCost: "5.00"}},
			wantLevel: StatusOK,
			wantSub:   []string{"used 2 × Widget", "$5.00", "Metal SIG"},
		},
		{
			name:      "charged, no total echoed",
			m:         consumeDoneMsg{qty: 1, itemName: "Widget", chargedName: "Metal SIG", res: &omsapi.LogUsageResult{}},
			wantLevel: StatusOK,
			wantSub:   []string{"charged to Metal SIG"},
		},
		{
			name:      "no charge",
			m:         consumeDoneMsg{qty: 3, itemName: "Widget", res: &omsapi.LogUsageResult{}},
			wantLevel: StatusOK,
			wantSub:   []string{"no charge"},
		},
		{
			name:      "warning downgrades to warn",
			m:         consumeDoneMsg{qty: 1, itemName: "Widget", chargedName: "Metal SIG", res: &omsapi.LogUsageResult{Warning: "no unit cost; usage recorded, nothing posted"}},
			wantLevel: StatusWarn,
			wantSub:   []string{"nothing posted"},
		},
		{
			name:      "nil response stays readable",
			m:         consumeDoneMsg{qty: 1, itemName: "Widget", chargedName: "Metal SIG"},
			wantLevel: StatusOK,
			wantSub:   []string{"charged to Metal SIG"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			text, level := consumeStatusLine(c.m)
			if level != c.wantLevel {
				t.Errorf("level = %d, want %d (text=%q)", level, c.wantLevel, text)
			}
			for _, sub := range c.wantSub {
				if !strings.Contains(text, sub) {
					t.Errorf("status %q missing %q", text, sub)
				}
			}
		})
	}
}

func TestFormatMoney(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", ""},
		{"5", "$5.00"},
		{"5.5", "$5.50"},
		{"12.34", "$12.34"},
		{"bogus", ""},
	}
	for _, c := range cases {
		if got := formatMoney(omsapi.DecimalString(c.in)); got != c.want {
			t.Errorf("formatMoney(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestProjectedCharge(t *testing.T) {
	s := &InventoryDetailScreen{item: &omsapi.Item{UnitCost: "2.50"}}
	if got := s.projectedCharge(4); got != "$10.00" {
		t.Errorf("projectedCharge(4) = %q, want $10.00", got)
	}
	if got := s.projectedCharge(0); got != "" {
		t.Errorf("projectedCharge(0) = %q, want empty (non-positive qty)", got)
	}
	noCost := &InventoryDetailScreen{item: &omsapi.Item{}}
	if got := noCost.projectedCharge(3); got != "" {
		t.Errorf("projectedCharge with no unit cost = %q, want empty", got)
	}
}
