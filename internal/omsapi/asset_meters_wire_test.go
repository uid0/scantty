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

// Decode tests for the asset-meter and asset-document endpoints, built from
// RECORDED OMS responses rather than from hand-written maps.
//
// The rule and the failure that motivated it are in testdata/README.md: a
// fixture written by whoever wrote the struct agrees with the struct by
// construction and cannot report a disagreement with the server. Every body
// below came off a real backend; the guards beside each decode read the RAW
// bytes, so a later edit "fixing" a fixture to match a struct fails rather than
// quietly restoring the defect.

// serveWirePaged answers any request with the same recorded body. The meter and
// document clients walk `next`, and each recorded page carries `"next": null`,
// so one body is one complete walk.
func assetWireClient(t *testing.T, body []byte) (*Client, *[]*http.Request) {
	t.Helper()
	var seen []*http.Request
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Body = io.NopCloser(strings.NewReader(readAll(t, r)))
		seen = append(seen, r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return New(srv.URL), &seen
}

func readAll(t *testing.T, r *http.Request) string {
	t.Helper()
	if r.Body == nil {
		return ""
	}
	b, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatalf("read request body: %v", err)
	}
	return string(b)
}

// ---------------------------------------------------------------------------
// Meters
// ---------------------------------------------------------------------------

// THE DECODE. Served the bytes OMS really sends, the meter list must decode and
// carry the server's own digits through.
func TestListAssetMeters_DecodesTheBytesOMSReallySends(t *testing.T) {
	body := wireBody(t, "asset_meters_list.json")
	c, _ := assetWireClient(t, body)
	meters, err := c.ListAssetMeters(context.Background(), "0289121e-7524-46f8-8dc6-8c3c5337e28a")
	if err != nil {
		t.Fatalf("a recorded OMS reply did not decode: %v", err)
	}

	var raw struct {
		Count   int              `json:"count"`
		Results []map[string]any `json:"results"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		t.Fatalf("fixture is not JSON: %v", err)
	}
	if len(meters) != raw.Count {
		t.Fatalf("decoded %d meters, want %d", len(meters), raw.Count)
	}
	for i, want := range raw.Results {
		got := meters[i]
		if id, _ := want["id"].(string); got.ID != id {
			t.Errorf("meter %d id = %q, want %q", i, got.ID, id)
		}
		// The VALUE is the point of the whole screen: it must arrive with the
		// server's own digits, unrounded and unreformatted, or the number an
		// operator reads back is not the number that was stored.
		if v, _ := want["current_value"].(string); got.CurrentValue.String() != v {
			t.Errorf("meter %d current_value = %q, want %q verbatim", i, got.CurrentValue, v)
		}
		if u, _ := want["unit"].(string); got.Unit != u {
			t.Errorf("meter %d unit = %q, want %q", i, got.Unit, u)
		}
		if b, _ := want["current_is_estimated"].(bool); got.CurrentIsEstimated != b {
			t.Errorf("meter %d current_is_estimated = %v, want %v", i, got.CurrentIsEstimated, b)
		}
		if b, _ := want["is_active"].(bool); got.IsActive != b {
			t.Errorf("meter %d is_active = %v, want %v", i, got.IsActive, b)
		}
	}
}

// The recorded body really does carry UUID STRINGS for ids and decimal STRINGS
// for values. Without this, a later edit turning `"id"` into a number or
// `current_value` into a bare number would change what the struct is being
// tested against and the decode test above would still pass.
func TestAssetMeterFixture_CarriesTheServersOwnTypes(t *testing.T) {
	var raw struct {
		Results []map[string]any `json:"results"`
	}
	if err := json.Unmarshal(wireBody(t, "asset_meters_list.json"), &raw); err != nil {
		t.Fatalf("fixture is not JSON: %v", err)
	}
	if len(raw.Results) == 0 {
		t.Fatal("fixture has no meters — it would prove nothing about a meter's types")
	}
	for i, m := range raw.Results {
		if _, isString := m["id"].(string); !isString {
			t.Errorf("results[%d].id is %T, want a JSON string — AssetMeter declares "+
				"`id = models.UUIDField(primary_key=True)`, so a number here means the "+
				"fixture was hand-edited", i, m["id"])
		}
		if _, isString := m["asset"].(string); !isString {
			t.Errorf("results[%d].asset is %T, want a JSON string (Asset's pk is a UUIDField too)", i, m["asset"])
		}
		if _, isString := m["current_value"].(string); !isString {
			t.Errorf("results[%d].current_value is %T, want a JSON string — DRF serializes a "+
				"DecimalField as a string, and DecimalString exists so the digits survive "+
				"rather than going through a float", i, m["current_value"])
		}
	}

	// The fixture reaches the states the grid has to keep apart, or the
	// assertions above are true of one uninteresting row repeated.
	var estimated, inactive, auto bool
	for _, m := range raw.Results {
		b, _ := m["current_is_estimated"].(bool)
		estimated = estimated || b
		act, _ := m["is_active"].(bool)
		inactive = inactive || !act
		s, _ := m["source"].(string)
		auto = auto || s == MeterSourceAutoSession
	}
	if !estimated || !inactive || !auto {
		t.Errorf("the recorded meters reach estimated=%v inactive=%v auto_session=%v; "+
			"all three must be present or the grid's markers are unexercised", estimated, inactive, auto)
	}
}

// record-reading and adjust return the SAME envelope, and both halves of it have
// to decode: the meter as it now stands, and the ledger row that moved it.
func TestRecordMeterReading_DecodesTheEnvelope(t *testing.T) {
	body := wireBody(t, "asset_meter_record_reading.json")
	c, seen := assetWireClient(t, body)
	res, err := c.RecordMeterReading(context.Background(), "bceb18f6-288e-4789-b596-a98493833bb6",
		RecordMeterReadingRequest{Value: "1294.0", IsAbsolute: true})
	if err != nil {
		t.Fatalf("a recorded OMS reply did not decode: %v", err)
	}

	var raw struct {
		Meter   map[string]any `json:"meter"`
		Reading map[string]any `json:"reading"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		t.Fatalf("fixture is not JSON: %v", err)
	}
	if v, _ := raw.Meter["current_value"].(string); res.Meter.CurrentValue.String() != v {
		t.Errorf("meter.current_value = %q, want %q", res.Meter.CurrentValue, v)
	}
	if v, _ := raw.Reading["value_after"].(string); res.Reading.ValueAfter.String() != v {
		t.Errorf("reading.value_after = %q, want %q", res.Reading.ValueAfter, v)
	}
	if v, _ := raw.Reading["delta"].(string); res.Reading.Delta.String() != v {
		t.Errorf("reading.delta = %q, want %q", res.Reading.Delta, v)
	}
	if v, _ := raw.Reading["source"].(string); res.Reading.Source != v {
		t.Errorf("reading.source = %q, want %q", res.Reading.Source, v)
	}
	if res.Reading.Source != MeterSourceManual {
		t.Errorf("a record-reading wrote source %q, want %q — the ledger is where a "+
			"measurement is told apart from a correction", res.Reading.Source, MeterSourceManual)
	}
	if len(*seen) != 1 {
		t.Fatalf("record-reading made %d requests, want 1", len(*seen))
	}
}

// THE PATH AND THE BODY ARE THE REQUEST. A meter reading that reaches the wrong
// endpoint, or carries is_absolute silently dropped, is a wrong number stored
// without a single error on screen.
func TestRecordMeterReading_SendsWhatTheServerReads(t *testing.T) {
	var gotPath, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(wireBody(t, "asset_meter_record_reading.json"))
	}))
	defer srv.Close()

	c := New(srv.URL)
	if _, err := c.RecordMeterReading(context.Background(), "m-1",
		RecordMeterReadingRequest{Value: "8.25", IsAbsolute: false, IsEstimated: true}); err != nil {
		t.Fatalf("record: %v", err)
	}
	if want := "/api/inventory/asset-meters/m-1/record-reading/"; gotPath != want {
		t.Errorf("path = %q, want %q", gotPath, want)
	}

	var sent map[string]any
	if err := json.Unmarshal([]byte(gotBody), &sent); err != nil {
		t.Fatalf("request body is not JSON: %v (%s)", err, gotBody)
	}
	// is_absolute FALSE must be on the wire. The server defaults it to true, so
	// a `false` dropped by omitempty turns "add 8.25 hours" into "the meter now
	// reads 8.25" — accepted with 201, and the number is gone.
	basis, present := sent["is_absolute"]
	if !present {
		t.Fatalf("request omitted is_absolute: %s — the server defaults it to TRUE, so a "+
			"delta reading would be stored as an absolute one", gotBody)
	}
	if basis != false {
		t.Errorf("is_absolute = %v, want false", basis)
	}
	if sent["value"] != "8.25" {
		t.Errorf("value = %v, want the operator's own digits %q", sent["value"], "8.25")
	}
	if sent["is_estimated"] != true {
		t.Errorf("is_estimated = %v, want true", sent["is_estimated"])
	}
	// observed_at is omitted when blank so the server stamps its own now().
	if _, present := sent["observed_at"]; present {
		t.Errorf("request carried observed_at %v with nothing to observe; blank must be "+
			"OMITTED so the server stamps the time it received it", sent["observed_at"])
	}
}

// ADJUST IS A DIFFERENT OPERATION AND THE RECORD SAYS SO. The recorded reply
// carries source `manual_adjust` and the operator's reason in notes — which is
// exactly what a later audit reads to tell a correction from a measurement.
func TestAdjustMeter_DecodesTheCorrection(t *testing.T) {
	body := wireBody(t, "asset_meter_adjust.json")
	var gotPath, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	res, err := New(srv.URL).AdjustMeter(context.Background(), "m-1",
		AdjustMeterRequest{Target: "1289.75", Reason: "reverting a mis-keyed reading"})
	if err != nil {
		t.Fatalf("a recorded OMS reply did not decode: %v", err)
	}
	if want := "/api/inventory/asset-meters/m-1/adjust/"; gotPath != want {
		t.Errorf("path = %q, want %q", gotPath, want)
	}
	var sent map[string]any
	if err := json.Unmarshal([]byte(gotBody), &sent); err != nil {
		t.Fatalf("request body is not JSON: %v", err)
	}
	if sent["target"] != "1289.75" || sent["reason"] != "reverting a mis-keyed reading" {
		t.Errorf("adjust sent %v, want the target and the reason verbatim", sent)
	}

	var raw struct {
		Reading map[string]any `json:"reading"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		t.Fatalf("fixture is not JSON: %v", err)
	}
	if res.Reading.Source != MeterReadingSourceManualAdjust {
		t.Errorf("adjust wrote source %q, want %q", res.Reading.Source, MeterReadingSourceManualAdjust)
	}
	if want, _ := raw.Reading["notes"].(string); res.Reading.Notes != want {
		t.Errorf("reading.notes = %q, want the reason %q — the reason IS the audit trail", res.Reading.Notes, want)
	}
	if res.Reading.Notes == "" {
		t.Error("the recorded adjust carries no reason, so this test proves nothing about one")
	}
}

// The ledger decodes, newest first, with a NEGATIVE delta intact — a correction
// that moved a meter DOWN is the row the whole `adjust` distinction exists for,
// and a sign lost in the decode would draw it as an advance.
func TestListMeterReadings_DecodesTheLedger(t *testing.T) {
	body := wireBody(t, "asset_meter_readings.json")
	c, _ := assetWireClient(t, body)
	readings, err := c.ListMeterReadings(context.Background(), "bceb18f6-288e-4789-b596-a98493833bb6")
	if err != nil {
		t.Fatalf("a recorded OMS reply did not decode: %v", err)
	}

	var raw struct {
		Count   int              `json:"count"`
		Results []map[string]any `json:"results"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		t.Fatalf("fixture is not JSON: %v", err)
	}
	if len(readings) != raw.Count {
		t.Fatalf("decoded %d readings, want %d", len(readings), raw.Count)
	}

	var sawNegative, sawAdjust bool
	for i, want := range raw.Results {
		got := readings[i]
		if v, _ := want["delta"].(string); got.Delta.String() != v {
			t.Errorf("reading %d delta = %q, want %q verbatim", i, got.Delta, v)
		}
		if v, _ := want["value_after"].(string); got.ValueAfter.String() != v {
			t.Errorf("reading %d value_after = %q, want %q verbatim", i, got.ValueAfter, v)
		}
		if strings.HasPrefix(got.Delta.String(), "-") {
			sawNegative = true
		}
		if got.Source == MeterReadingSourceManualAdjust {
			sawAdjust = true
		}
	}
	if !sawNegative {
		t.Error("the recorded ledger has no negative delta, so nothing here proves a " +
			"downward correction survives the decode with its sign")
	}
	if !sawAdjust {
		t.Error("the recorded ledger has no manual_adjust row, so nothing here proves a " +
			"correction is distinguishable from a measurement")
	}
}

// A rollup reading has NO recorder — nobody entered it — and that is a different
// fact from a reading whose recorder the serializer could not name. The pointer
// is what keeps them apart.
func TestAssetMeterReading_AnUnrecordedReadingIsNotRecordedByNobodyNamed(t *testing.T) {
	body := []byte(`{"count":1,"next":null,"previous":null,"results":[{
		"id":"r-1","meter":"m-1","source":"auto_session","source_display":"Auto — usage sessions",
		"delta":"6.0833","value_after":"6.0833","is_estimated":false,
		"observed_at":"2026-09-11T23:08:39.231165Z","recorded_at":"2026-09-11T23:08:39.237Z",
		"recorded_by":null,"recorded_by_name":null,"source_ref":"device_usage x12","notes":""}]}`)
	c, _ := assetWireClient(t, body)
	readings, err := c.ListMeterReadings(context.Background(), "m-1")
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(readings) != 1 {
		t.Fatalf("got %d readings, want 1", len(readings))
	}
	if readings[0].RecordedBy != nil {
		t.Errorf("recorded_by = %v, want nil — a rollup has no recorder", *readings[0].RecordedBy)
	}
	if readings[0].SourceRef == "" {
		t.Error("source_ref was dropped; it is the only provenance an automatic reading has")
	}
}
