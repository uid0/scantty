package omsapi

import (
	"context"
	"fmt"
	"net/url"
	"time"
)

// Webhook mirrors WebHookSerializer (backend/reorder_queue) — the outbound
// event-notification registry served at /api/reorders/webhooks/. An operator
// registers a target URL + one event_type and OMS POSTs a signed payload there
// when that event fires.
//
// DECODE-DRIFT NOTE ([[scantty-api-field-drift]]): the webhook pk is an int
// (Django autoid). `secret` is serializer write_only, so it is NEVER present in
// a read response — it is deliberately omitted from this struct rather than
// carried as an always-empty field. `event_type_display` is the human label for
// the event_type code. `success_rate` is a SerializerMethodField that returns
// null (not 0) until the webhook has fired at least once, so it is a *float64.
// `last_triggered_at` is a nullable DateTimeField (*time.Time); created_at /
// updated_at are non-null auto timestamps. `headers` is a JSON object of string
// header values (may arrive as null → nil map).
type Webhook struct {
	ID               int               `json:"id"`
	Name             string            `json:"name"`
	Description      string            `json:"description"`
	URL              string            `json:"url"`
	EventType        string            `json:"event_type"`
	EventTypeDisplay string            `json:"event_type_display"`
	IsActive         bool              `json:"is_active"`
	Headers          map[string]string `json:"headers"`
	LastTriggeredAt  *time.Time        `json:"last_triggered_at"`
	SuccessCount     int               `json:"success_count"`
	FailureCount     int               `json:"failure_count"`
	SuccessRate      *float64          `json:"success_rate"`
	TotalTriggers    int               `json:"total_triggers"`
	LastError        string            `json:"last_error"`
	CreatedAt        time.Time         `json:"created_at,omitempty"`
	UpdatedAt        time.Time         `json:"updated_at,omitempty"`
}

// WebhookWrite is the create/edit payload. The form owns the full writable field
// set (WebHookCreateSerializer: name, description, url, event_type, is_active,
// secret, headers), so the whole representation is sent each save (create = POST,
// edit = PATCH).
//
// name / url / event_type are required by the serializer. is_active always
// serializes (bool, default true) so an edit can flip it off. headers ALWAYS
// serializes as a JSON object — the form sends an empty {} rather than null so
// the model's JSONField (null=False) accepts it and an edit can clear headers by
// sending {}; buildPayload keeps the map non-nil for this reason.
//
// secret is `omitempty`: an EMPTY secret is dropped from the body so an edit
// leaves the stored secret untouched ("leave empty to keep current"), exactly as
// the web form does (it deletes an empty secret before submit). A non-empty
// secret is sent and rotates the stored value (the backend records a
// webhook_secret_rotate audit event on change). There is NO regenerate-secret
// @action on the viewset — rotation is a plain field write.
type WebhookWrite struct {
	Name        string            `json:"name"`
	Description string            `json:"description"`
	URL         string            `json:"url"`
	EventType   string            `json:"event_type"`
	IsActive    bool              `json:"is_active"`
	Secret      string            `json:"secret,omitempty"`
	Headers     map[string]string `json:"headers"`
}

// WebhookTestResult mirrors WebHookTestResultSerializer — the response to the
// test-delivery @action (and the test-status poll). In eager mode (the backend's
// synchronous path) task_id is null and task_status is already "SUCCESS" /
// "FAILURE" with success + status_code + response_time_ms filled in. In async
// (Celery) mode the initial response is task_status "PENDING" with a task_id and
// null success — the caller polls GetWebhookTestStatus until it resolves. All
// fields are optional/nullable per the serializer, so the ones that distinguish
// pending-from-done (success, status_code, response_time_ms, tested_at) are
// pointers.
type WebhookTestResult struct {
	WebhookID      *int       `json:"webhook_id"`
	WebhookName    string     `json:"webhook_name"`
	TaskID         string     `json:"task_id"`
	TaskStatus     string     `json:"task_status"`
	Success        *bool      `json:"success"`
	StatusCode     *int       `json:"status_code"`
	ResponseTimeMs *float64   `json:"response_time_ms"`
	ErrorMessage   string     `json:"error_message"`
	ResponseBody   string     `json:"response_body"`
	TestedAt       *time.Time `json:"tested_at"`
}

// ListWebhooks returns every configured webhook (GET /api/reorders/webhooks/),
// paging through the DRF envelope. Webhook counts are tiny (one per subscribed
// event per integration), so eager full-paging is cheap.
func (c *Client) ListWebhooks(ctx context.Context) ([]Webhook, error) {
	var all []Webhook
	if err := IterPages[Webhook](ctx, c, "/api/reorders/webhooks/", nil, func(batch []Webhook) error {
		all = append(all, batch...)
		return nil
	}); err != nil {
		return nil, err
	}
	return all, nil
}

// GetWebhook fetches one webhook by id (for edit-mode hydration + detail).
func (c *Client) GetWebhook(ctx context.Context, id int) (*Webhook, error) {
	var out Webhook
	if err := c.Get(ctx, fmt.Sprintf("/api/reorders/webhooks/%d/", id), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// CreateWebhook POSTs a new webhook. The create response echoes the full read
// representation (minus the write-only secret).
func (c *Client) CreateWebhook(ctx context.Context, body WebhookWrite) (*Webhook, error) {
	var out Webhook
	if err := c.Post(ctx, "/api/reorders/webhooks/", body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// UpdateWebhook PATCHes an existing webhook (partial-update mixin; the form sends
// the full representation, minus an empty secret, so this behaves like a replace
// that preserves the stored secret when the operator leaves it blank).
func (c *Client) UpdateWebhook(ctx context.Context, id int, body WebhookWrite) (*Webhook, error) {
	var out Webhook
	if err := c.Patch(ctx, fmt.Sprintf("/api/reorders/webhooks/%d/", id), body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteWebhook removes a webhook (the backend records a webhook_delete audit
// event first). A 204 with no body is treated as success by the client.
func (c *Client) DeleteWebhook(ctx context.Context, id int) error {
	return c.Delete(ctx, fmt.Sprintf("/api/reorders/webhooks/%d/", id))
}

// TestWebhook fires a test delivery (POST /api/reorders/webhooks/{id}/test/,
// bodyless) and returns the initial result. The endpoint returns 202 Accepted;
// in eager mode the WebhookTestResult is already terminal, in async mode it
// carries a task_id + "PENDING" status the caller polls via GetWebhookTestStatus.
func (c *Client) TestWebhook(ctx context.Context, id int) (*WebhookTestResult, error) {
	var out WebhookTestResult
	if err := c.Post(ctx, fmt.Sprintf("/api/reorders/webhooks/%d/test/", id), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetWebhookTestStatus polls a queued test delivery
// (GET /api/reorders/webhooks/test-status/?task_id=...). Returns the resolved
// WebhookTestResult once the Celery task finishes; while it is still running the
// result's TaskStatus stays "PENDING".
func (c *Client) GetWebhookTestStatus(ctx context.Context, taskID string) (*WebhookTestResult, error) {
	q := url.Values{}
	q.Set("task_id", taskID)
	var out WebhookTestResult
	if err := c.Get(ctx, "/api/reorders/webhooks/test-status/", q, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
