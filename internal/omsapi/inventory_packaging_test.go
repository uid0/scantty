package omsapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// Unit-of-measure / packaging-matrix wire contract (OMS #979/#980/#981).

// TestItem_DecodesPackagingMatrix pins the read shape: the new item fields, the
// nested chain (including a null per_parent on the base rung), and the by_level
// flavour of on_hand_display.
func TestItem_DecodesPackagingMatrix(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"abc","name":"Copy paper","sku":"P-1","current_stock":2500,
			"minimum_stock":2,"reorder_quantity":4,
			"base_unit":"sheet","count_mode":"by_level","count_level":9,
			"open_container_count":0,
			"packaging_levels":[
				{"id":8,"name":"case","sort_order":0,"base_units":5000,"per_parent":10},
				{"id":9,"name":"ream","sort_order":1,"base_units":500,"per_parent":500},
				{"id":10,"name":"sheet","sort_order":2,"base_units":1,"per_parent":null}
			],
			"on_hand_display":{"mode":"by_level","level":"ream","level_count":5,
				"remainder_base":0,"text":"5 ream(s)"}}`))
	}))
	defer srv.Close()

	it, err := New(srv.URL).GetItem(context.Background(), "abc")
	if err != nil {
		t.Fatalf("GetItem: %v", err)
	}
	if it.BaseUnit != "sheet" || it.CountMode != CountModeByLevel {
		t.Errorf("base_unit/count_mode = %q/%q", it.BaseUnit, it.CountMode)
	}
	if it.CountLevel == nil || *it.CountLevel != 9 {
		t.Fatalf("count_level = %v, want 9", it.CountLevel)
	}
	if len(it.PackagingLevels) != 3 {
		t.Fatalf("packaging_levels = %d rungs, want 3", len(it.PackagingLevels))
	}
	// Rungs arrive outermost-first; base_units is always in BASE units.
	if got := it.PackagingLevels[0]; got.Name != "case" || got.SortOrder != 0 || got.BaseUnits != 5000 {
		t.Errorf("outermost rung = %+v", got)
	}
	if pp := it.PackagingLevels[0].PerParent; pp == nil || *pp != 10 {
		t.Errorf("case per_parent = %v, want 10", pp)
	}
	// The base rung has nothing below it, so per_parent is null — a *float64 so
	// it stays distinguishable from a real 0.
	if pp := it.PackagingLevels[2].PerParent; pp != nil {
		t.Errorf("base rung per_parent = %v, want nil", *pp)
	}
	if it.OnHandDisplay == nil {
		t.Fatal("on_hand_display did not decode")
	}
	if it.OnHandDisplay.Mode != CountModeByLevel || it.OnHandDisplay.LevelCount != 5 ||
		it.OnHandDisplay.Level != "ream" || it.OnHandDisplay.Text != "5 ream(s)" {
		t.Errorf("on_hand_display = %+v", *it.OnHandDisplay)
	}
	// current_stock stays the canonical BASE-unit quantity.
	if it.Stock != 2500 {
		t.Errorf("current_stock = %d, want the base-unit 2500", it.Stock)
	}
}

// TestItem_DecodesOpenClosedDisplay pins the sealed/open flavour, whose fields
// are disjoint from the by_level one.
func TestItem_DecodesOpenClosedDisplay(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"abc","name":"Trash bags","current_stock":300,
			"base_unit":"bag","count_mode":"open_closed","count_level":3,
			"open_container_count":1,
			"packaging_levels":[{"id":3,"name":"case","sort_order":0,"base_units":100},
				{"id":4,"name":"bag","sort_order":1,"base_units":1}],
			"on_hand_display":{"mode":"open_closed","level":"case","sealed":3,"open":1,
				"text":"3 sealed + 1 open"}}`))
	}))
	defer srv.Close()

	it, err := New(srv.URL).GetItem(context.Background(), "abc")
	if err != nil {
		t.Fatalf("GetItem: %v", err)
	}
	if it.OpenContainerCount != 1 {
		t.Errorf("open_container_count = %d, want 1", it.OpenContainerCount)
	}
	d := it.OnHandDisplay
	if d == nil || d.Mode != CountModeOpenClosed || d.Sealed != 3 || d.Open != 1 {
		t.Fatalf("on_hand_display = %+v", d)
	}
	if d.Text != "3 sealed + 1 open" {
		t.Errorf("text = %q", d.Text)
	}
}

// TestItem_PackagingFieldsAbsentOnOlderBackend confirms an item payload with
// none of the new keys decodes as an opted-out item rather than failing — the
// back-compat half of the invariant.
func TestItem_PackagingFieldsAbsentOnOlderBackend(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"abc","name":"Widget","current_stock":12}`))
	}))
	defer srv.Close()

	it, err := New(srv.URL).GetItem(context.Background(), "abc")
	if err != nil {
		t.Fatalf("GetItem: %v", err)
	}
	if it.BaseUnit != "" || it.CountMode != "" || it.CountLevel != nil ||
		it.PackagingLevels != nil || it.OnHandDisplay != nil {
		t.Errorf("absent packaging fields should stay zero, got %+v", it)
	}
}

// TestItemWrite_OmitsPackagingWhenUntouched is the safety property: a payload
// that sets no packaging must carry NEITHER key, so an each-mode item's write is
// byte-identical to before the matrix — and a nil chain can never wipe a stored
// one.
func TestItemWrite_OmitsPackagingWhenUntouched(t *testing.T) {
	raw, err := json.Marshal(ItemWrite{Name: "Widget", CurrentStock: 3})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, key := range []string{"base_unit", "packaging_levels", "count_mode", "count_level"} {
		if _, present := body[key]; present {
			t.Errorf("%s must be omitted when untouched, got %v", key, body[key])
		}
	}
}

// TestItemWrite_SendsPackagingChain pins the nested write: sort_order is the
// rung's position, no pk is sent (the serializer upserts on (item, sort_order)),
// and an EMPTY chain still serializes as [] so it can clear a stored one.
func TestItemWrite_SendsPackagingChain(t *testing.T) {
	unit := "sheet"
	levels := []PackagingLevelWrite{
		{Name: "case", SortOrder: 0, BaseUnits: 5000},
		{Name: "ream", SortOrder: 1, BaseUnits: 500},
		{Name: "sheet", SortOrder: 2, BaseUnits: 1},
	}
	raw, err := json.Marshal(ItemWrite{Name: "Paper", BaseUnit: &unit, PackagingLevels: &levels})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var body struct {
		BaseUnit        string `json:"base_unit"`
		PackagingLevels []struct {
			Name      string `json:"name"`
			SortOrder int    `json:"sort_order"`
			BaseUnits int    `json:"base_units"`
			ID        *int   `json:"id"`
		} `json:"packaging_levels"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body.BaseUnit != "sheet" {
		t.Errorf("base_unit = %q", body.BaseUnit)
	}
	if len(body.PackagingLevels) != 3 {
		t.Fatalf("rungs = %d", len(body.PackagingLevels))
	}
	for i, rung := range body.PackagingLevels {
		if rung.SortOrder != i {
			t.Errorf("rung %d sort_order = %d, want the row index", i, rung.SortOrder)
		}
		if rung.ID != nil {
			t.Errorf("rung %d must not send a pk (upsert is positional), got %v", i, *rung.ID)
		}
	}

	// A pointer to an empty slice is how the chain is CLEARED — it must reach the
	// wire as [], not be dropped by omitempty.
	empty := []PackagingLevelWrite{}
	raw, err = json.Marshal(ItemWrite{Name: "Paper", PackagingLevels: &empty})
	if err != nil {
		t.Fatalf("marshal empty: %v", err)
	}
	var cleared map[string]any
	if err := json.Unmarshal(raw, &cleared); err != nil {
		t.Fatalf("unmarshal empty: %v", err)
	}
	got, present := cleared["packaging_levels"]
	if !present {
		t.Fatal("an explicitly empty chain must still be sent, so it can clear the stored one")
	}
	if rows, ok := got.([]any); !ok || len(rows) != 0 {
		t.Errorf("packaging_levels = %v, want []", got)
	}
}

// TestSetItemCountMode_Contract pins the counting-mode PATCH: the item path, and
// that count_level is sent as an explicit NULL for "each" (omitting it would
// leave a level set, which the backend rejects).
func TestSetItemCountMode_Contract(t *testing.T) {
	var captured struct {
		method string
		path   string
		body   map[string]any
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured.method, captured.path = r.Method, r.URL.Path
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &captured.body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"abc","name":"Paper","count_mode":"each"}`))
	}))
	defer srv.Close()

	c := New(srv.URL)
	if _, err := c.SetItemCountMode(context.Background(), "abc", CountModeEach, nil); err != nil {
		t.Fatalf("SetItemCountMode: %v", err)
	}
	if captured.method != http.MethodPatch || captured.path != "/api/inventory/items/abc/" {
		t.Fatalf("method/path = %q %q", captured.method, captured.path)
	}
	if captured.body["count_mode"] != CountModeEach {
		t.Errorf("count_mode = %v", captured.body["count_mode"])
	}
	level, present := captured.body["count_level"]
	if !present {
		t.Fatal("count_level must be sent explicitly so 'each' clears it")
	}
	if level != nil {
		t.Errorf("count_level = %v, want null", level)
	}

	// A pack mode sends the resolved rung pk.
	pk := 9
	if _, err := c.SetItemCountMode(context.Background(), "abc", CountModeByLevel, &pk); err != nil {
		t.Fatalf("SetItemCountMode(by_level): %v", err)
	}
	if captured.body["count_mode"] != CountModeByLevel {
		t.Errorf("count_mode = %v", captured.body["count_mode"])
	}
	if got, ok := captured.body["count_level"].(float64); !ok || int(got) != 9 {
		t.Errorf("count_level = %v, want 9", captured.body["count_level"])
	}
}

// TestPackContainer_Contract pins the action path — HYPHENATED url_path plus the
// load-bearing trailing slash — and the transition body.
func TestPackContainer_Contract(t *testing.T) {
	var captured struct {
		method string
		path   string
		body   map[string]any
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured.method, captured.path = r.Method, r.URL.Path
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &captured.body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"transition":"open","id":"abc","current_stock":200,
			"open_container_count":1,
			"on_hand_display":{"mode":"open_closed","level":"case","sealed":2,"open":1,
				"text":"2 sealed + 1 open"},
			"usage_log":{"id":41,"quantity_used":100}}`))
	}))
	defer srv.Close()

	res, err := New(srv.URL).PackContainer(context.Background(), "abc", PackTransitionOpen, "")
	if err != nil {
		t.Fatalf("PackContainer: %v", err)
	}
	if captured.method != http.MethodPost || captured.path != "/api/inventory/items/abc/pack-container/" {
		t.Fatalf("method/path = %q %q (hyphen + trailing slash are the contract)", captured.method, captured.path)
	}
	if captured.body["transition"] != PackTransitionOpen {
		t.Errorf("transition = %v", captured.body["transition"])
	}
	if _, present := captured.body["notes"]; present {
		t.Errorf("empty notes should be omitted, got %v", captured.body["notes"])
	}
	if res.CurrentStock != 200 || res.OpenContainerCount != 1 {
		t.Errorf("stock/open = %d/%d", res.CurrentStock, res.OpenContainerCount)
	}
	if res.OnHandDisplay == nil || res.OnHandDisplay.Text != "2 sealed + 1 open" {
		t.Errorf("on_hand_display = %+v", res.OnHandDisplay)
	}
}

// TestCycleCountBody_AtLevelIsOptIn is the invariant in payload form: a count
// that does not opt in carries neither at_level nor open_count, so the backend
// reads it in base units exactly as it always has.
func TestCycleCountBody_AtLevelIsOptIn(t *testing.T) {
	raw, err := json.Marshal(CycleCountBody{CountedQty: 8, Reason: "miscounted"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, key := range []string{"at_level", "open_count"} {
		if _, present := body[key]; present {
			t.Errorf("%s must be omitted unless opted in, got %v", key, body[key])
		}
	}

	open := 1
	raw, err = json.Marshal(CycleCountBody{CountedQty: 3, Reason: "found", AtLevel: true, OpenCount: &open})
	if err != nil {
		t.Fatalf("marshal opted-in: %v", err)
	}
	body = nil
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("unmarshal opted-in: %v", err)
	}
	if body["at_level"] != true {
		t.Errorf("at_level = %v, want true", body["at_level"])
	}
	if got, ok := body["open_count"].(float64); !ok || int(got) != 1 {
		t.Errorf("open_count = %v, want 1", body["open_count"])
	}
	// A ZERO open count is meaningful ("nothing is open now"), so a pointer to 0
	// must still be sent.
	zero := 0
	raw, _ = json.Marshal(CycleCountBody{CountedQty: 3, Reason: "found", AtLevel: true, OpenCount: &zero})
	body = nil
	_ = json.Unmarshal(raw, &body)
	got, present := body["open_count"]
	if !present || got.(float64) != 0 {
		t.Errorf("open_count=0 must be sent, got %v (present=%v)", got, present)
	}
}

// TestLogUsageBody_AtLevelIsOptIn is the same invariant for consumption.
func TestLogUsageBody_AtLevelIsOptIn(t *testing.T) {
	raw, _ := json.Marshal(LogUsageBody{Quantity: 2})
	var body map[string]any
	_ = json.Unmarshal(raw, &body)
	if _, present := body["at_level"]; present {
		t.Errorf("at_level must be omitted unless opted in, got %v", body["at_level"])
	}

	raw, _ = json.Marshal(LogUsageBody{Quantity: 2, AtLevel: true})
	body = nil
	_ = json.Unmarshal(raw, &body)
	if body["at_level"] != true {
		t.Errorf("at_level = %v, want true", body["at_level"])
	}
}
