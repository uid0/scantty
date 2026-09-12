// What an operator reads when a serializer refuses a write.
//
// The shape under test is OMS's STANDARD envelope (backend/config/api_errors.py)
// — the one parseError already understands, which is exactly why this exists:
// understanding it produced `oms: validation_failed: One or more fields failed
// validation.`, the same eleven words for every refusal the catalogue can make,
// with the sentences an operator can act on left unread in `details`.
package omsapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// refusalFrom runs a body through the real transport, so what these measure is
// what a client method really returns rather than a hand-built APIError.
func refusalFrom(t *testing.T, status int, body string) error {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	var out struct{}
	err := New(srv.URL).Get(t.Context(), "/api/inventory/kits/", nil, &out)
	if err == nil {
		t.Fatal("the request succeeded; there is no refusal to read")
	}
	return err
}

// TestAsFieldRefusal_NamesTheFieldAndKeepsTheServersWords is the ordinary case:
// one field, one sentence, said in the server's own words with the box named.
func TestAsFieldRefusal_NamesTheFieldAndKeepsTheServersWords(t *testing.T) {
	err := refusalFrom(t, http.StatusBadRequest, `{"error":{"code":"validation_failed",
		"message":"One or more fields failed validation.",
		"details":{"components":["A kit must contain at least one component."]}}}`)

	got, ok := AsFieldRefusal(err)
	if !ok {
		t.Fatalf("a standard validation envelope was not read as one: %v", err)
	}
	if got != "components: A kit must contain at least one component." {
		t.Errorf("refusal = %q", got)
	}
	// The generic envelope message is what this exists to replace, so it must
	// not come along with the sentence it replaces.
	if strings.Contains(got, "One or more fields") {
		t.Errorf("the envelope's generic message survived into %q", got)
	}
}

// TestAsFieldRefusal_ReadsANestedBlockDownToTheBox. KitSupplierTermsSerializer
// is a serializer inside a serializer (OMS op-kit-terms), so its refusals arrive
// keyed by the BLOCK and then by the field inside it. "supplier_terms:
// Incorrect type" would name a block with five boxes in it; the operator has to
// be told which one.
func TestAsFieldRefusal_ReadsANestedBlockDownToTheBox(t *testing.T) {
	err := refusalFrom(t, http.StatusBadRequest, `{"error":{"code":"validation_failed",
		"message":"One or more fields failed validation.",
		"details":{"supplier_terms":{
			"supplier":["Incorrect type. Expected pk value, received str."],
			"package_cost":["This field is not part of a kit's supplier terms."]}}}}`)

	got, ok := AsFieldRefusal(err)
	if !ok {
		t.Fatalf("a nested refusal was not read: %v", err)
	}
	// SORTED, so one failure renders one way. A map has no order, and a refusal
	// that reshuffles between two draws of the same failure reads as two.
	want := "supplier_terms.package_cost: This field is not part of a kit's supplier terms. · " +
		"supplier_terms.supplier: Incorrect type. Expected pk value, received str."
	if got != want {
		t.Errorf("refusal = %q, want %q", got, want)
	}
}

// TestAsFieldRefusal_IsStableAcrossDraws pins that ordering directly rather than
// inferring it from one lucky pass: Go randomises map iteration per range, so a
// single assertion can be green over an unsorted implementation.
func TestAsFieldRefusal_IsStableAcrossDraws(t *testing.T) {
	err := refusalFrom(t, http.StatusBadRequest, `{"error":{"code":"validation_failed",
		"message":"One or more fields failed validation.",
		"details":{"sku":["a"],"name":["b"],"description":["c"],"components":["d"],
			"minimum_stock":["e"],"reorder_quantity":["f"]}}}`)

	first, ok := AsFieldRefusal(err)
	if !ok {
		t.Fatal("not read as a refusal")
	}
	for i := 0; i < 50; i++ {
		again, _ := AsFieldRefusal(err)
		if again != first {
			t.Fatalf("draw %d differed:\n\t%q\n\t%q", i, first, again)
		}
	}
	if !strings.HasPrefix(first, "components: ") {
		t.Errorf("refusal = %q, want the fields in sorted order", first)
	}
}

// TestAsFieldRefusal_ANonFieldErrorNamesNoField. DRF puts an error about the
// OBJECT under `non_field_errors`, and prefixing with that key would send an
// operator hunting for a box that does not exist.
func TestAsFieldRefusal_ANonFieldErrorNamesNoField(t *testing.T) {
	err := refusalFrom(t, http.StatusBadRequest, `{"error":{"code":"validation_failed",
		"message":"One or more fields failed validation.",
		"details":{"non_field_errors":["A kit cannot contain itself."]}}}`)

	got, ok := AsFieldRefusal(err)
	if !ok {
		t.Fatalf("not read as a refusal: %v", err)
	}
	if got != "A kit cannot contain itself." {
		t.Errorf("refusal = %q, want the sentence alone", got)
	}
}

// TestAsFieldRefusal_AScalarDetailIsProseToo. A hand-raised ValidationError with
// a bare message arrives as a string rather than a list, and it is the same
// fact wearing a different shape.
func TestAsFieldRefusal_AScalarDetailIsProseToo(t *testing.T) {
	err := refusalFrom(t, http.StatusBadRequest, `{"error":{"code":"validation_failed",
		"message":"One or more fields failed validation.",
		"details":{"sku":"Already taken."}}}`)

	got, ok := AsFieldRefusal(err)
	if !ok || got != "sku: Already taken." {
		t.Errorf("refusal = %q, ok = %v", got, ok)
	}
}

// TestAsFieldRefusal_LeavesEveryOtherShapeAlone. As narrow as AsDetailRefusal
// and AsReceivingRefusal beside it: a shape this cannot read must reach the
// caller exactly as it arrived, so nothing that already understands a body
// loses it — the `candidates` hint AsLineEntryError reads off `details` being
// the live one.
func TestAsFieldRefusal_LeavesEveryOtherShapeAlone(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"a gateway page", `<!DOCTYPE html><html><body>502</body></html>`},
		{"DRF's own detail prose", `{"detail":"Authentication credentials were not provided."}`},
		{"a hand-built receiving refusal", `{"error":"Nothing left to receive on this order."}`},
		{"an envelope with no details at all", `{"error":{"code":"conflict","message":"Already sent."}}`},
		{"a details LIST rather than a field map",
			`{"error":{"code":"validation_failed","message":"m","details":["nope"]}}`},
		{"a details SCALAR", `{"error":{"code":"validation_failed","message":"m","details":"nope"}}`},
		{"an empty field map", `{"error":{"code":"validation_failed","message":"m","details":{}}}`},
		{"a details map whose values are numbers",
			`{"error":{"code":"validation_failed","message":"m","details":{"retry_after":30}}}`},
		{"the ambiguity hint a line-entry caller already reads",
			`{"error":{"code":"ambiguous","message":"m","details":{"candidates":[{"item_supplier":4}]}}}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := refusalFrom(t, http.StatusBadRequest, tc.body)
			if got, ok := AsFieldRefusal(err); ok {
				t.Errorf("read %q as a field refusal: %q", tc.body, got)
			}
		})
	}
}

// TestAsFieldRefusal_ACandidateHintStillReachesTheLineEntryReader is the other
// half of that last row, and the one that matters: it is not enough that
// AsFieldRefusal declines the ambiguity envelope — the reader that DOES
// understand it has to go on understanding it.
func TestAsFieldRefusal_ACandidateHintStillReachesTheLineEntryReader(t *testing.T) {
	err := refusalFrom(t, http.StatusConflict, `{"error":{"code":"ambiguous",
		"message":"Several items match that code.",
		"details":{"candidates":[{"item_supplier":4,"match_label":"supplier SKU"}]}}}`)

	entry, ok := AsLineEntryError(err)
	if !ok {
		t.Fatalf("the line-entry reader lost the envelope: %v", err)
	}
	if len(entry.Candidates) != 1 || entry.Candidates[0].ItemSupplier != 4 {
		t.Errorf("candidates = %+v", entry.Candidates)
	}
}

// TestAsFieldRefusal_ABlankSentenceIsNotOne. An empty string in the list is not
// something an operator can read, and "sku: " would be a label with nothing
// behind it on the one row a refusal gets.
func TestAsFieldRefusal_ABlankSentenceIsNotOne(t *testing.T) {
	err := refusalFrom(t, http.StatusBadRequest, `{"error":{"code":"validation_failed",
		"message":"m","details":{"sku":["   ",""]}}}`)
	if got, ok := AsFieldRefusal(err); ok {
		t.Errorf("read a blank detail as a refusal: %q", got)
	}
}
