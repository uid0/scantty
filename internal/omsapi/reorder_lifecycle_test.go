package omsapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The two actions that CLOSE a reorder request, measured on the wire.
//
// The bodies are the contract and not a detail. ReorderRequestViewSet
// .mark_ordered reads its three optional fields with `if "<key>" in
// request.data`, so an ABSENT key leaves the stored value alone while a key
// sent EMPTY overwrites it — and `order_number` is carried onto the request by
// the Purchase Order domain rather than typed by an operator. A client that
// helpfully sent `{"order_number": ""}` would erase a PO's own number on every
// mark-ordered, silently.

func TestMarkReorderRequestOrdered_SendsAnEmptyBody(t *testing.T) {
	var gotPath string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		_ = json.NewEncoder(w).Encode(map[string]any{"id": 41, "status": "ordered"})
	}))
	defer srv.Close()

	out, err := New(srv.URL).MarkReorderRequestOrdered(context.Background(), "41")
	if err != nil {
		t.Fatalf("MarkReorderRequestOrdered: %v", err)
	}
	if gotPath != "/api/reorders/requests/41/mark_ordered/" {
		t.Errorf("posted to %q", gotPath)
	}
	if len(gotBody) != 0 {
		t.Errorf("body carried %v — every key here overwrites a stored value", gotBody)
	}
	if out.Status != ReorderStatusOrdered {
		t.Errorf("decoded status %q, want ordered", out.Status)
	}
}

func TestMarkReorderRequestReceived_OmitsTheDateItWasNotGiven(t *testing.T) {
	var bodies []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		body := map[string]any{}
		_ = json.Unmarshal(raw, &body)
		bodies = append(bodies, body)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": 77, "status": "received", "actual_delivery": "2026-09-11",
		})
	}))
	defer srv.Close()

	c := New(srv.URL)
	// "" means "the server decides", which for this action is today. Sending an
	// empty string instead would be a DIFFERENT claim about the delivery date.
	if _, err := c.MarkReorderRequestReceived(context.Background(), "77", ""); err != nil {
		t.Fatalf("MarkReorderRequestReceived: %v", err)
	}
	out, err := c.MarkReorderRequestReceived(context.Background(), "77", "2026-09-01")
	if err != nil {
		t.Fatalf("MarkReorderRequestReceived with a date: %v", err)
	}
	if _, present := bodies[0]["actual_delivery"]; present {
		t.Errorf("a blank date still sent the key: %v", bodies[0])
	}
	if bodies[1]["actual_delivery"] != "2026-09-01" {
		t.Errorf("a supplied date did not reach the wire: %v", bodies[1])
	}
	if out.ActualDelivery.Format("2006-01-02") != "2026-09-11" {
		t.Errorf("actual_delivery decoded as %v", out.ActualDelivery)
	}
}

// ListReorderRequests walks EVERY page, because it is the only route to an
// approved or ordered row and the viewset serves no status filter. A client
// that read page one would hide exactly the rows an operator opens this list to
// close, with nothing on screen saying so.
func TestListReorderRequests_WalksEveryPage(t *testing.T) {
	pages := map[string]string{
		"":  `{"count":3,"next":"http://x/api/reorders/requests/?page=2","results":[{"id":1,"status":"pending"}]}`,
		"2": `{"count":3,"next":"http://x/api/reorders/requests/?page=3","results":[{"id":2,"status":"approved"}]}`,
		"3": `{"count":3,"next":null,"results":[{"id":3,"status":"ordered"}]}`,
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		page := r.URL.Query().Get("page")
		if page == "1" {
			page = ""
		}
		body, ok := pages[page]
		if !ok {
			t.Errorf("asked for an unexpected page %q", page)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = io.WriteString(w, body)
	}))
	defer srv.Close()

	rows, err := New(srv.URL).ListReorderRequests(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListReorderRequests: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("got %d rows, want every page's worth", len(rows))
	}
	if rows[2].Status != ReorderStatusOrdered {
		t.Errorf("the last page's row is %q", rows[2].Status)
	}
}

// A seven-digit pk decodes into an `any`. jsonDecoder keeps it as a
// json.Number, and IDString renders its digits — a bare %v over a float64 would
// make "1e+06" and the action URL would 404.
func TestReorderRequest_IDStringKeepsTheServersDigits(t *testing.T) {
	var r ReorderRequest
	if err := jsonDecoder(strings.NewReader(`{"id": 1000000}`)).Decode(&r); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got := r.IDString(); got != "1000000" {
		t.Fatalf("IDString() = %q, want the server's own digits", got)
	}
	var missing ReorderRequest
	if got := missing.IDString(); got != "" {
		t.Errorf("a row with no id rendered %q, want the empty string", got)
	}
}

// AsDetailRefusal is as NARROW as its sibling AsReceivingRefusal, and the
// negative cases are the point: coercing any of them into a refusal would be
// inventing a sentence the server never said, on the line whose whole job is to
// relay what it did say.
func TestAsDetailRefusal_RecoversOnlyTheShapeItNames(t *testing.T) {
	refusal := func(body string) error { return &APIError{Status: 400, Message: body} }

	prose, ok := AsDetailRefusal(refusal(`{"detail": "Cannot receive a cancelled reorder request."}`))
	if !ok || prose != "Cannot receive a cancelled reorder request." {
		t.Fatalf("AsDetailRefusal = (%q, %v), want the server's sentence", prose, ok)
	}

	for name, body := range map[string]string{
		"a gateway's HTML page":      `<!DOCTYPE html><html><body>502</body></html>`,
		"the hand-built error shape": `{"error": "not a draft", "code": "not_draft"}`,
		"a field-validation body":    `{"quantity": ["This field is required."]}`,
		"a detail that is an object": `{"detail": {"code": "x"}}`,
		"a detail that is a list":    `{"detail": ["x"]}`,
		"a blank detail":             `{"detail": "   "}`,
		"not JSON at all":            `upstream connect error`,
	} {
		if prose, ok := AsDetailRefusal(refusal(body)); ok {
			t.Errorf("%s was read as a refusal saying %q", name, prose)
		}
	}

	if _, ok := AsDetailRefusal(context.Canceled); ok {
		t.Error("a non-API error was read as a refusal")
	}
}
