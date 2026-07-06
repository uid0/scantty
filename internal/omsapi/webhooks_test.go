package omsapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestCreateWebhook_Contract pins the create wire contract: POST to the
// collection, the required name/url/event_type present, is_active always sent,
// headers serialized as a JSON object (not null), and the write-only secret
// carried when set.
func TestCreateWebhook_Contract(t *testing.T) {
	var captured struct {
		method string
		path   string
		body   map[string]any
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured.method = r.Method
		captured.path = r.URL.Path
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &captured.body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":7,"name":"Slack","url":"https://hooks.example.com/x","event_type":"item_low_stock","event_type_display":"Item Low Stock","is_active":true}`))
	}))
	defer srv.Close()

	c := New(srv.URL)
	hook, err := c.CreateWebhook(context.Background(), WebhookWrite{
		Name:      "Slack",
		URL:       "https://hooks.example.com/x",
		EventType: "item_low_stock",
		IsActive:  true,
		Secret:    "s3cr3t",
		Headers:   map[string]string{"X-Env": "prod"},
	})
	if err != nil {
		t.Fatalf("CreateWebhook: %v", err)
	}
	if hook == nil || hook.ID != 7 {
		t.Fatalf("unexpected webhook: %+v", hook)
	}
	if captured.method != http.MethodPost || captured.path != "/api/reorders/webhooks/" {
		t.Fatalf("method/path = %q %q", captured.method, captured.path)
	}
	if captured.body["name"] != "Slack" {
		t.Errorf("name = %v", captured.body["name"])
	}
	if captured.body["url"] != "https://hooks.example.com/x" {
		t.Errorf("url = %v", captured.body["url"])
	}
	if captured.body["event_type"] != "item_low_stock" {
		t.Errorf("event_type = %v", captured.body["event_type"])
	}
	// is_active bool always present.
	if v, ok := captured.body["is_active"]; !ok || v != true {
		t.Errorf("is_active = %v (present=%v), want true present", v, ok)
	}
	if captured.body["secret"] != "s3cr3t" {
		t.Errorf("secret = %v", captured.body["secret"])
	}
	// headers is a JSON object, not null.
	hdr, ok := captured.body["headers"].(map[string]any)
	if !ok {
		t.Fatalf("headers = %v (%T), want object", captured.body["headers"], captured.body["headers"])
	}
	if hdr["X-Env"] != "prod" {
		t.Errorf("headers[X-Env] = %v", hdr["X-Env"])
	}
}

// TestCreateWebhook_EmptySecretOmitted confirms an empty secret is dropped from
// the body (edit keeps the stored secret) while headers still serialize as {}.
func TestCreateWebhook_EmptySecretOmitted(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":1,"name":"n"}`))
	}))
	defer srv.Close()

	c := New(srv.URL)
	if _, err := c.CreateWebhook(context.Background(), WebhookWrite{
		Name:      "n",
		URL:       "https://e.example.com",
		EventType: "delivery_received",
		IsActive:  false,
		Headers:   map[string]string{},
	}); err != nil {
		t.Fatalf("CreateWebhook: %v", err)
	}
	if _, present := body["secret"]; present {
		t.Errorf("empty secret should be omitted, got %v", body["secret"])
	}
	hdr, ok := body["headers"].(map[string]any)
	if !ok || len(hdr) != 0 {
		t.Errorf("headers = %v (%T), want empty object", body["headers"], body["headers"])
	}
	// is_active present even when false.
	if v, ok := body["is_active"]; !ok || v != false {
		t.Errorf("is_active = %v (present=%v), want false present", v, ok)
	}
}

// TestUpdateWebhook_Contract pins the edit path + method (PATCH to the detail URL).
func TestUpdateWebhook_Contract(t *testing.T) {
	var captured struct {
		method string
		path   string
		body   map[string]any
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured.method = r.Method
		captured.path = r.URL.Path
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &captured.body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":7,"name":"Renamed"}`))
	}))
	defer srv.Close()

	c := New(srv.URL)
	if _, err := c.UpdateWebhook(context.Background(), 7, WebhookWrite{
		Name:      "Renamed",
		URL:       "https://e.example.com",
		EventType: "security_report",
		IsActive:  true,
		Headers:   map[string]string{},
	}); err != nil {
		t.Fatalf("UpdateWebhook: %v", err)
	}
	if captured.method != http.MethodPatch || captured.path != "/api/reorders/webhooks/7/" {
		t.Fatalf("method/path = %q %q", captured.method, captured.path)
	}
	if captured.body["name"] != "Renamed" {
		t.Errorf("name = %v", captured.body["name"])
	}
	if captured.body["event_type"] != "security_report" {
		t.Errorf("event_type = %v", captured.body["event_type"])
	}
}

// TestDeleteWebhook_Contract pins the delete path + method and that a 204 with no
// body is treated as success.
func TestDeleteWebhook_Contract(t *testing.T) {
	var captured struct {
		method string
		path   string
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured.method = r.Method
		captured.path = r.URL.Path
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	c := New(srv.URL)
	if err := c.DeleteWebhook(context.Background(), 7); err != nil {
		t.Fatalf("DeleteWebhook: %v", err)
	}
	if captured.method != http.MethodDelete || captured.path != "/api/reorders/webhooks/7/" {
		t.Fatalf("method/path = %q %q", captured.method, captured.path)
	}
}

// TestTestWebhook_Contract pins the test-delivery @action: bodyless POST to the
// detail test/ URL, decoding the eager-mode terminal result (pointers filled).
func TestTestWebhook_Contract(t *testing.T) {
	var captured struct {
		method string
		path   string
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured.method = r.Method
		captured.path = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"webhook_id":7,"webhook_name":"Slack","task_id":null,"task_status":"SUCCESS","success":true,"status_code":200,"response_time_ms":42.5,"tested_at":"2026-07-06T09:00:00Z"}`))
	}))
	defer srv.Close()

	c := New(srv.URL)
	res, err := c.TestWebhook(context.Background(), 7)
	if err != nil {
		t.Fatalf("TestWebhook: %v", err)
	}
	if captured.method != http.MethodPost || captured.path != "/api/reorders/webhooks/7/test/" {
		t.Fatalf("method/path = %q %q", captured.method, captured.path)
	}
	if res.TaskStatus != "SUCCESS" {
		t.Errorf("task_status = %q", res.TaskStatus)
	}
	if res.Success == nil || !*res.Success {
		t.Errorf("success = %v, want true", res.Success)
	}
	if res.StatusCode == nil || *res.StatusCode != 200 {
		t.Errorf("status_code = %v", res.StatusCode)
	}
	if res.ResponseTimeMs == nil || *res.ResponseTimeMs != 42.5 {
		t.Errorf("response_time_ms = %v", res.ResponseTimeMs)
	}
	if res.TestedAt == nil {
		t.Errorf("tested_at should decode")
	}
}

// TestGetWebhookTestStatus_Query confirms the task_id query param is sent and a
// still-pending result decodes with success left nil (distinguishing pending).
func TestGetWebhookTestStatus_Query(t *testing.T) {
	var gotTask string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotTask = r.URL.Query().Get("task_id")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"task_id":"abc-123","task_status":"PENDING","success":null}`))
	}))
	defer srv.Close()

	c := New(srv.URL)
	res, err := c.GetWebhookTestStatus(context.Background(), "abc-123")
	if err != nil {
		t.Fatalf("GetWebhookTestStatus: %v", err)
	}
	if gotTask != "abc-123" {
		t.Errorf("task_id query = %q", gotTask)
	}
	if res.TaskStatus != "PENDING" {
		t.Errorf("task_status = %q", res.TaskStatus)
	}
	if res.Success != nil {
		t.Errorf("success should be nil while pending, got %v", *res.Success)
	}
}

// TestListWebhooks_Decode confirms the DRF page envelope unwraps and the read
// struct round-trips, especially the nullable success_rate / last_triggered_at
// and the write-only secret being absent.
func TestListWebhooks_Decode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"count":2,"next":null,"previous":null,"results":[
			{"id":1,"name":"Fresh","url":"https://a.example.com","event_type":"item_low_stock","event_type_display":"Item Low Stock","is_active":true,"headers":{},"last_triggered_at":null,"success_count":0,"failure_count":0,"success_rate":null,"total_triggers":0,"last_error":"","created_at":"2026-07-06T09:00:00Z","updated_at":"2026-07-06T09:00:00Z"},
			{"id":2,"name":"Live","url":"https://b.example.com","event_type":"delivery_received","event_type_display":"Delivery Received","is_active":false,"headers":{"X-Env":"prod"},"last_triggered_at":"2026-07-05T12:00:00Z","success_count":9,"failure_count":1,"success_rate":90.0,"total_triggers":10,"last_error":"timeout"}
		]}`))
	}))
	defer srv.Close()

	c := New(srv.URL)
	hooks, err := c.ListWebhooks(context.Background())
	if err != nil {
		t.Fatalf("ListWebhooks: %v", err)
	}
	if len(hooks) != 2 {
		t.Fatalf("hooks = %d, want 2", len(hooks))
	}
	fresh := hooks[0]
	if fresh.SuccessRate != nil {
		t.Errorf("fresh success_rate should be nil, got %v", *fresh.SuccessRate)
	}
	if fresh.LastTriggeredAt != nil {
		t.Errorf("fresh last_triggered_at should be nil")
	}
	if fresh.EventTypeDisplay != "Item Low Stock" {
		t.Errorf("event_type_display = %q", fresh.EventTypeDisplay)
	}
	live := hooks[1]
	if live.SuccessRate == nil || *live.SuccessRate != 90.0 {
		t.Errorf("live success_rate = %v, want 90", live.SuccessRate)
	}
	if live.LastTriggeredAt == nil {
		t.Errorf("live last_triggered_at should decode")
	}
	if live.Headers["X-Env"] != "prod" {
		t.Errorf("live headers = %v", live.Headers)
	}
	if live.TotalTriggers != 10 || live.SuccessCount != 9 || live.FailureCount != 1 {
		t.Errorf("live counts: %+v", live)
	}
	if live.LastError != "timeout" {
		t.Errorf("last_error = %q", live.LastError)
	}
}
