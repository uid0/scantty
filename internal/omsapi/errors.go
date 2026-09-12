package omsapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
)

type APIError struct {
	Status  int             `json:"-"`
	Code    string          `json:"code"`
	Message string          `json:"message"`
	Details json.RawMessage `json:"details,omitempty"`
}

func (e *APIError) Error() string {
	if e.Code != "" {
		return fmt.Sprintf("oms: %s: %s", e.Code, e.Message)
	}
	return fmt.Sprintf("oms: http %d: %s", e.Status, e.Message)
}

func (e *APIError) IsAuth() bool {
	return e.Code == "authentication_failed" || e.Status == http.StatusUnauthorized
}

func (e *APIError) IsNotFound() bool {
	return e.Code == "not_found" || e.Status == http.StatusNotFound
}

// IsForbidden reports a 403 — the caller is authenticated but lacks permission
// (e.g. the analytics pulse requires IsAnalyticsViewer: staff / SIG-admin).
func (e *APIError) IsForbidden() bool {
	return e.Code == "permission_denied" || e.Status == http.StatusForbidden
}

func parseError(resp *http.Response) error {
	body, _ := io.ReadAll(resp.Body)
	var envelope struct {
		Error APIError `json:"error"`
	}
	if err := json.Unmarshal(body, &envelope); err == nil && envelope.Error.Code != "" {
		envelope.Error.Status = resp.StatusCode
		return &envelope.Error
	}
	return &APIError{
		Status:  resp.StatusCode,
		Message: string(body),
	}
}

// AsDetailRefusal recovers the sentence from a refusal written as DRF's
// {"detail": "<prose>"} body, and reports false for anything that is not one.
//
// It exists for the same reason AsReceivingRefusal does, one shape over. OMS
// routes real EXCEPTIONS through config.api_errors.standardized_exception_handler,
// which produces the coded envelope parseError already understands — but a view
// that RETURNS `Response({"detail": ...}, status=400)` from its own body never
// reaches that handler, so parseError finds no `code`, falls through, and hands
// the caller the whole raw JSON as APIError.Message. Without this the operator
// reads `oms: http 400: {"detail": "Cannot receive a cancelled reorder
// request."}` on the one line that is supposed to tell them what went wrong.
//
// Deliberately as NARROW as its sibling: a body is a refusal here only when it
// parses as an object whose `detail` member is a non-blank JSON STRING. That
// leaves alone exactly the shapes that must keep arriving as they are —
//
//   - a gateway's HTML page, which does not start with `{`;
//   - the hand-built `{"error": ...}` shape, which carries no `detail`;
//   - a DRF field-validation body such as `{"quantity": ["..."]}`, likewise;
//   - `{"detail": {...}}` or `{"detail": []}`, whose detail is not prose.
//
// DRF's own 401/403/404 renderings are this shape too, so a permission refusal
// reaches the operator as its sentence rather than as JSON. That is a widening
// of what is legible and not of what is BELIEVED: the sentence is still only
// ever the server's own words.
func AsDetailRefusal(err error) (string, bool) {
	var api *APIError
	if !errors.As(err, &api) {
		return "", false
	}
	body := strings.TrimSpace(api.Message)
	if !strings.HasPrefix(body, "{") {
		return "", false
	}
	var envelope struct {
		Detail json.RawMessage `json:"detail"`
	}
	if json.Unmarshal([]byte(body), &envelope) != nil || len(envelope.Detail) == 0 {
		return "", false
	}
	var prose string
	if json.Unmarshal(envelope.Detail, &prose) != nil {
		return "", false
	}
	if strings.TrimSpace(prose) == "" {
		return "", false
	}
	return prose, true
}

// AsFieldRefusal flattens OMS's STANDARD validation envelope into the one
// sentence an operator can act on, and reports false for anything that is not
// one.
//
// It exists because parseError already understands that envelope and that is
// exactly the problem: a 400 from a serializer arrives as
//
//	{"error": {"code": "validation_failed",
//	           "message": "One or more fields failed validation.",
//	           "details": {"components": ["A kit must contain at least one component."]}}}
//
// so APIError.Error() renders `oms: validation_failed: One or more fields
// failed validation.` — true, useless, and identical for every refusal the
// server can make. The sentence the operator needs is in `details`, keyed by
// the field it is about, and nothing on this side read it. What that cost is
// worst on a CREATE: a kit refused for a missing description, an empty bill of
// materials and a bad supplier id all said the same eleven words.
//
// WHAT IT PRODUCES is `field: sentence`, joined with " · " when the server
// refused more than one, in SORTED field order — sorted because a Go map has no
// order and a refusal that reshuffles itself between two renders of the same
// failure reads as two different failures. Nested blocks (the kit's
// `supplier_terms` is one: a serializer inside a serializer) come through as
// `supplier_terms.supplier: …`, so the operator is told which box, not just
// which block. DRF's own `non_field_errors` key is dropped from the prefix,
// because "non_field_errors: A kit cannot contain itself." names a field that
// does not exist.
//
// It is as NARROW as AsDetailRefusal and AsReceivingRefusal beside it: only a
// coded envelope carrying a non-empty `details` OBJECT whose members resolve to
// prose. A `details` that is a list, a scalar, or an object of some other shape
// (the `candidates` hint AsLineEntryError reads is one) leaves the error exactly
// as it arrived, so no caller loses a shape it already understands.
//
// NOTHING HERE DECODES INTO AN UNTYPED VALUE, which is what keeps it off
// jsonDecoder's rule (client.go): every target is json.RawMessage, []string or
// string, so a JSON number has no untyped landing spot to become a float64 in,
// and nothing recovered here is ever spent as a path segment. A number sitting
// where prose was expected simply fails its unmarshal and is skipped.
func AsFieldRefusal(err error) (string, bool) {
	var api *APIError
	if !errors.As(err, &api) || len(api.Details) == 0 {
		return "", false
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(api.Details, &fields) != nil || len(fields) == 0 {
		return "", false
	}
	names := make([]string, 0, len(fields))
	for name := range fields {
		names = append(names, name)
	}
	sort.Strings(names)

	var out []string
	for _, name := range names {
		out = append(out, fieldRefusalParts(name, fields[name])...)
	}
	if len(out) == 0 {
		return "", false
	}
	return strings.Join(out, " · "), true
}

// fieldRefusalParts renders one member of a `details` object as zero or more
// "field: sentence" clauses.
//
// Three shapes reach it, and all three are real: DRF writes a field's errors as
// a LIST of strings, a hand-raised ValidationError with a bare message arrives
// as a STRING, and a nested serializer — KitSupplierTermsSerializer is the live
// one — arrives as an OBJECT keyed by its own field names. The object case
// recurses exactly one level deeper than it has to be written for, which is
// enough: OMS nests a serializer inside a serializer and nothing nests one
// inside that.
//
// Anything else yields nothing rather than a guess, so AsFieldRefusal's caller
// keeps the raw error when the shape is one this cannot read.
func fieldRefusalParts(name string, raw json.RawMessage) []string {
	label := name + ": "
	// The key DRF uses for an error about the object rather than about one of
	// its fields. Prefixing with it would name a box the operator cannot find.
	if name == "non_field_errors" {
		label = ""
	}

	var msgs []string
	if json.Unmarshal(raw, &msgs) == nil {
		out := make([]string, 0, len(msgs))
		for _, msg := range msgs {
			if strings.TrimSpace(msg) != "" {
				out = append(out, label+strings.TrimSpace(msg))
			}
		}
		return out
	}

	var msg string
	if json.Unmarshal(raw, &msg) == nil {
		if strings.TrimSpace(msg) == "" {
			return nil
		}
		return []string{label + strings.TrimSpace(msg)}
	}

	var nested map[string]json.RawMessage
	if json.Unmarshal(raw, &nested) != nil || len(nested) == 0 {
		return nil
	}
	inner := make([]string, 0, len(nested))
	for key := range nested {
		inner = append(inner, key)
	}
	sort.Strings(inner)
	var out []string
	for _, key := range inner {
		out = append(out, fieldRefusalParts(name+"."+key, nested[key])...)
	}
	return out
}
