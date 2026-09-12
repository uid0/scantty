package omsapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
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
