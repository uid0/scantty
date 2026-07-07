package omsapi

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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
