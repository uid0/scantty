package omsapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Decode tests for POST /api/reorders/requests/, built from RECORDED OMS
// responses (testdata/reorder_create_*.json, provenance in
// testdata/README.md).
//
// WHAT MAKES THIS ENDPOINT WORTH RECORDING TWICE. It has three outcomes and two
// of them are 2xx, so a client that reads only the transport reports a request
// as filed when the server filed nothing. Both success bodies are here, and so
// is one off the server BEFORE the rule existed — that third file is the whole
// reason ScanTTY can land ahead of the OMS change, and it is the one a fixture
// written from the struct could never have produced, because a struct with an
// AlreadyRequested field invites a fixture that carries the key.
//
// The guards read the RAW bytes alongside the decode, so a later edit "fixing" a
// fixture to match a struct fails rather than quietly restoring the defect
// testdata/README.md records.

// reorderCreateServer answers one POST with a recorded body at a recorded
// status, and hands back what the client SENT so the request half can be
// checked too.
func reorderCreateServer(t *testing.T, status int, body []byte) (*Client, **http.Request, *string) {
	t.Helper()
	var got *http.Request
	var sent string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sent = readAll(t, r)
		got = r
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return New(srv.URL), &got, &sent
}

func scanRequest() ReorderRequestCreate {
	return ReorderRequestCreate{
		Item:         "650681b1-f9b6-4410-be93-351e76c718d3",
		Quantity:     4,
		RequestedBy:  "Anonymous",
		RequestNotes: "Auto-submitted via QR scan",
	}
}

// A FILED REQUEST IS A 201 CARRYING already_requested: false, and the server
// puts the key on the created reply as well as on the duplicate one so a client
// reads ONE field rather than having to notice 201 against 200 — a distinction
// anything checking "did this succeed" flattens.
func TestCreateReorderRequest_AFiledRequestSaysSo(t *testing.T) {
	body := wireBody(t, "reorder_create_filed.json")
	c, got, sent := reorderCreateServer(t, http.StatusCreated, body)

	out, err := c.CreateReorderRequest(context.Background(), scanRequest())
	if err != nil {
		t.Fatalf("a recorded 201 did not decode: %v", err)
	}
	if out.AlreadyRequested {
		t.Errorf("AlreadyRequested = true on the FILED reply; nothing would ever " +
			"be reported as created")
	}
	if raw := rawAsset(t, body); raw["already_requested"] != false {
		t.Fatalf("fixture's already_requested = %#v, want the recorded JSON false; "+
			"a fixture edited to match a struct proves nothing", raw["already_requested"])
	}

	if (*got).Method != http.MethodPost {
		t.Errorf("method = %s, want POST", (*got).Method)
	}
	if (*got).URL.Path != "/api/reorders/requests/" {
		t.Errorf("path = %q, want the create endpoint", (*got).URL.Path)
	}
	var req map[string]any
	if err := json.Unmarshal([]byte(*sent), &req); err != nil {
		t.Fatalf("request body %q is not JSON: %v", *sent, err)
	}
	if req["item"] != "650681b1-f9b6-4410-be93-351e76c718d3" || req["quantity"] != float64(4) {
		t.Errorf("sent %v, want the scan's item and quantity", req)
	}
}

// A DUPLICATE IS A 200 CARRYING already_requested: true AND THE EXISTING ROW.
// Nothing was created; the id, quantity and status echoed back are the pending
// request's own, which is what lets a caller name it on screen.
func TestCreateReorderRequest_ADuplicateIsASuccessThatCreatedNothing(t *testing.T) {
	body := wireBody(t, "reorder_create_already_requested.json")
	c, _, _ := reorderCreateServer(t, http.StatusOK, body)

	out, err := c.CreateReorderRequest(context.Background(), scanRequest())
	if err != nil {
		t.Fatalf("the 200 reached the caller as an error (%v); a duplicate is not a "+
			"failure and must never be reported as one", err)
	}
	if !out.AlreadyRequested {
		t.Fatal("AlreadyRequested = false on the recorded duplicate reply, so the " +
			"terminal would report a reorder that was never created")
	}
	if out.Status != ReorderStatusPending {
		t.Errorf("status = %q, want %q: the row handed back is the PENDING one this "+
			"scan duplicated", out.Status, ReorderStatusPending)
	}

	raw := rawAsset(t, body)
	if raw["already_requested"] != true {
		t.Fatalf("fixture's already_requested = %#v, want the recorded JSON true",
			raw["already_requested"])
	}
	// The id is a BigAutoField, so it is a NUMBER on the wire and lands in the
	// `any` as a json.Number (jsonDecoder's UseNumber). The recorded pk is seven
	// digits ON PURPOSE: decoded the default way it would be a float64 and print
	// as "1.200041e+06", which is the failure AGENTS.md records against every id
	// spent through fmt.
	if _, ok := raw["id"].(float64); !ok {
		t.Fatalf("fixture's id = %#v; the recorded body carries a NUMBER and a "+
			"fixture rewritten to a string would hide what the pk really is", raw["id"])
	}
	if got := out.IDString(); got != "1200041" {
		t.Errorf("IDString() = %q, want the server's own digits 1200041", got)
	}
}

// AN OMS THAT HAS NOT TAKEN THE RULE BEHAVES EXACTLY AS BEFORE, and this is the
// recording that proves it rather than the reasoning that predicts it: the
// SECOND anonymous scan against remote main answers 201 with a NEW row and no
// marker at all. The absent key decodes to false, false is "filed", and filed is
// the truth there — which is what lets this land before the server change.
func TestCreateReorderRequest_AServerWithoutTheRuleStillReportsAFiling(t *testing.T) {
	body := wireBody(t, "reorder_create_pre_rule.json")
	if raw := rawAsset(t, body); len(raw) != 7 {
		t.Fatalf("the pre-rule fixture has %d keys (%v); it must be the body the "+
			"server sent BEFORE the marker existed, or it proves nothing about "+
			"tolerating one that does not send it", len(raw), raw)
	}
	if _, present := rawAsset(t, body)["already_requested"]; present {
		t.Fatal("the pre-rule fixture carries already_requested; a recording from " +
			"before the rule cannot have it, so this file has been edited")
	}

	c, _, _ := reorderCreateServer(t, http.StatusCreated, body)
	out, err := c.CreateReorderRequest(context.Background(), scanRequest())
	if err != nil {
		t.Fatalf("a pre-rule 201 did not decode: %v", err)
	}
	if out.AlreadyRequested {
		t.Error("AlreadyRequested = true with the key absent; an older server's " +
			"filed request would be reported as a duplicate")
	}
	if got := out.IDString(); got != "1200042" {
		t.Errorf("IDString() = %q, want 1200042 — the SECOND row that server filed", got)
	}
}

// A REAL ERROR STAYS A REAL ERROR and never acquires the marker. The refusal is
// OMS's standardized envelope, recorded off the same backend, and it must reach
// the caller as an error so "could not tell" cannot be read as either of the
// two successes.
func TestCreateReorderRequest_ARefusalIsNeitherOutcome(t *testing.T) {
	body := wireBody(t, "reorder_create_validation_failed.json")
	c, _, _ := reorderCreateServer(t, http.StatusBadRequest, body)

	out, err := c.CreateReorderRequest(context.Background(), ReorderRequestCreate{Quantity: 4})
	if err == nil {
		t.Fatal("a recorded 400 came back as a success")
	}
	if out != nil {
		t.Errorf("a refusal returned a result (%+v); there is nothing for a caller "+
			"to read AlreadyRequested off", out)
	}
	// parseError recognises OMS's standardized envelope and surfaces its code and
	// message; the per-field `details` are not carried, which is the package's
	// existing behaviour and not this endpoint's business.
	if !strings.Contains(err.Error(), "One or more fields failed validation") {
		t.Errorf("error = %q, want the server's own sentence", err.Error())
	}
	if _, present := rawAsset(t, body)["already_requested"]; present {
		t.Error("the refusal fixture carries already_requested; the server does not " +
			"put it on the error envelope and a client must not be able to read one")
	}
}
