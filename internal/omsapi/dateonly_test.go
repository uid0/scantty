package omsapi

import (
	"encoding/json"
	"testing"
)

func TestDateOnlyUnmarshal(t *testing.T) {
	type wrap struct {
		D DateOnly `json:"d"`
	}
	cases := []struct {
		name     string
		in       string
		wantZero bool
		wantStr  string
	}{
		{"date only", `{"d":"2026-09-09"}`, false, "2026-09-09"},
		{"null", `{"d":null}`, true, ""},
		{"empty string", `{"d":""}`, true, ""},
		{"rfc3339 fallback", `{"d":"2026-09-09T12:34:56Z"}`, false, "2026-09-09"},
		{"missing key", `{}`, true, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var w wrap
			if err := json.Unmarshal([]byte(tc.in), &w); err != nil {
				t.Fatalf("unmarshal %q: %v", tc.in, err)
			}
			if w.D.IsZero() != tc.wantZero {
				t.Fatalf("IsZero()=%v want %v (%q)", w.D.IsZero(), tc.wantZero, tc.in)
			}
			if got := w.D.String(); got != tc.wantStr {
				t.Fatalf("String()=%q want %q", got, tc.wantStr)
			}
		})
	}
}

func TestDateOnlyRejectsGarbage(t *testing.T) {
	var d DateOnly
	if err := d.UnmarshalJSON([]byte(`"not-a-date"`)); err == nil {
		t.Fatal("expected error for non-date string, got nil")
	}
}
