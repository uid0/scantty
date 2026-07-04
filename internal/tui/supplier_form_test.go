package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/uid0/scantty/internal/omsapi"
)

// TestSupplierForm_BuildPayload walks the happy path: name, the type select
// (default local), website/account/notes and the tax-free toggle map onto a
// SupplierWrite.
func TestSupplierForm_BuildPayload(t *testing.T) {
	s := NewSupplierFormScreen(Deps{}, "")
	s.inputs[sfName].SetValue("Acme")
	s.inputs[sfWebsite].SetValue("https://acme.test")
	s.inputs[sfAccountNumber].SetValue("ACC-1")
	s.inputs[sfNotes].SetValue("net-30")
	s.taxFree = true

	w, err := s.buildPayload()
	if err != nil {
		t.Fatalf("buildPayload: %v", err)
	}
	if w.Name != "Acme" {
		t.Errorf("name = %q", w.Name)
	}
	// default type is the first option, "local"
	if w.SupplierType != "local" {
		t.Errorf("supplier_type = %q, want local default", w.SupplierType)
	}
	if w.Website != "https://acme.test" {
		t.Errorf("website = %q", w.Website)
	}
	if w.AccountNumber != "ACC-1" {
		t.Errorf("account_number = %q", w.AccountNumber)
	}
	if !w.TaxFreePaperworkFiled {
		t.Errorf("tax_free_paperwork_filed should be true")
	}
	if w.Notes != "net-30" {
		t.Errorf("notes = %q", w.Notes)
	}
}

// TestSupplierForm_TypeCycle confirms the type select cycles through all three
// backend choices.
func TestSupplierForm_TypeCycle(t *testing.T) {
	s := NewSupplierFormScreen(Deps{}, "")
	s.inputs[sfName].SetValue("Acme")
	s.cursor = 1 // sfType
	if id, _ := s.currentFieldID(); id != sfType {
		t.Fatalf("cursor not on type field")
	}
	s.updateFormPhase(tea.KeyMsg{Type: tea.KeySpace}) // local -> online
	if w, _ := s.buildPayload(); w.SupplierType != "online" {
		t.Errorf("after 1 cycle supplier_type = %q, want online", w.SupplierType)
	}
	s.updateFormPhase(tea.KeyMsg{Type: tea.KeySpace}) // online -> national
	if w, _ := s.buildPayload(); w.SupplierType != "national" {
		t.Errorf("after 2 cycles supplier_type = %q, want national", w.SupplierType)
	}
	s.updateFormPhase(tea.KeyMsg{Type: tea.KeySpace}) // national -> local (wrap)
	if w, _ := s.buildPayload(); w.SupplierType != "local" {
		t.Errorf("after 3 cycles supplier_type = %q, want local (wrap)", w.SupplierType)
	}
}

// TestSupplierForm_Validation covers name-required.
func TestSupplierForm_Validation(t *testing.T) {
	s := NewSupplierFormScreen(Deps{}, "")
	if _, err := s.buildPayload(); err == nil {
		t.Errorf("expected error for empty name")
	}
}

// TestSupplierForm_Hydrate fills every field from a fetched supplier, including
// the type index and tax-free toggle.
func TestSupplierForm_Hydrate(t *testing.T) {
	s := NewSupplierFormScreen(Deps{}, "11")
	s.sup = &omsapi.Supplier{
		ID:                    11,
		Name:                  "Acme",
		SupplierType:          "national",
		Website:               "https://acme.test",
		AccountNumber:         "ACC-9",
		TaxFreePaperworkFiled: true,
		Notes:                 "notes",
	}
	s.hydrate()
	if s.inputs[sfName].Value() != "Acme" {
		t.Errorf("name = %q", s.inputs[sfName].Value())
	}
	if supplierTypeOptions[s.typeIdx].value != "national" {
		t.Errorf("type = %q, want national", supplierTypeOptions[s.typeIdx].value)
	}
	if !s.taxFree {
		t.Errorf("taxFree should hydrate to true")
	}
	if s.inputs[sfAccountNumber].Value() != "ACC-9" {
		t.Errorf("account = %q", s.inputs[sfAccountNumber].Value())
	}
}

// TestSupplierForm_RenderSmoke guards the form render path.
func TestSupplierForm_RenderSmoke(t *testing.T) {
	s := NewSupplierFormScreen(Deps{}, "")
	s.terminalHeight = 30
	out := s.View()
	if !strings.Contains(out, "Type") || !strings.Contains(out, "Local") {
		t.Errorf("form view missing type select: %q", out)
	}
}

// TestSupplierList_DeleteConfirm confirms x arms + n cancels the confirmation.
func TestSupplierList_DeleteConfirm(t *testing.T) {
	s := NewSupplierListScreen(Deps{})
	s.loading = false
	s.rows = []omsapi.Supplier{{ID: 1, Name: "A"}}
	s.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	if !s.confirmingDelete || !s.WantsRawInput() {
		t.Errorf("x should arm delete confirmation")
	}
	s.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")})
	if s.confirmingDelete {
		t.Errorf("n should cancel the confirmation")
	}
}
