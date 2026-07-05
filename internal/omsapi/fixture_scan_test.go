package omsapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestScanFixtureDecodesStringID feeds the REAL fixture-scan wire shape: a UUID
// string id. Fixture.ID was typed `int` before the decode field-drift sweep,
// which crashed with `cannot unmarshal string into ... id of type int`; it is
// now `any`, so a UUID string (or a legacy int) decodes cleanly. Regression
// guard for the sc-8gy Fixture fix.
func TestScanFixtureDecodesStringID(t *testing.T) {
	const uuid = "f1e2d3c4-5566-7788-99aa-bbccddeeff00"
	body := `{"id":"` + uuid + `","name":"Shelf A","location":5,"status":"active"}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	c := New(srv.URL)
	fix, err := c.ScanFixture(context.Background(), uuid)
	if err != nil {
		t.Fatalf("ScanFixture: %v", err)
	}
	if s, ok := fix.ID.(string); !ok || s != uuid {
		t.Errorf("id = %#v, want the UUID string %q", fix.ID, uuid)
	}
	if fix.Name != "Shelf A" || fix.Location == nil || *fix.Location != 5 || fix.Status != "active" {
		t.Errorf("decoded fixture = %+v", fix)
	}
}
