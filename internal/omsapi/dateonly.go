package omsapi

import (
	"bytes"
	"strings"
	"time"
)

// DateOnly holds a calendar date that a Django “DateField“ serializes as a
// bare "2006-01-02" string with no time component. Decoding that into a plain
// “time.Time“ fails — encoding/json only accepts RFC3339 — which is exactly
// the crash that took out the vendors screen (`parsing time "2026-09-09" as
// "2006-01-02T15:04:05Z07:00": cannot parse "" as "T"`). This type parses the
// date-only form, falls back to RFC3339 for the odd endpoint that hands back a
// full timestamp for a nominal date, and treats null / "" as the zero date so
// a missing value never errors. It embeds time.Time so callers keep the usual
// “IsZero“/“Format“ helpers.
type DateOnly struct {
	time.Time
}

const dateOnlyLayout = "2006-01-02"

func (d *DateOnly) UnmarshalJSON(data []byte) error {
	data = bytes.TrimSpace(data)
	if len(data) == 0 || bytes.Equal(data, []byte("null")) || bytes.Equal(data, []byte(`""`)) {
		d.Time = time.Time{}
		return nil
	}
	s := strings.Trim(string(data), `"`)
	if t, err := time.Parse(dateOnlyLayout, s); err == nil {
		d.Time = t
		return nil
	}
	// A field shared with a DateTimeField, or an endpoint that returns a full
	// timestamp for a nominal date, still decodes rather than crashing.
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return err
	}
	d.Time = t
	return nil
}

// MarshalJSON emits the date-only form (or null when zero) so the value round
// trips back to a Django DateField when used in a request body.
func (d DateOnly) MarshalJSON() ([]byte, error) {
	if d.Time.IsZero() {
		return []byte("null"), nil
	}
	return []byte(`"` + d.Time.Format(dateOnlyLayout) + `"`), nil
}

// String renders the date as "2006-01-02", or "" when unset.
func (d DateOnly) String() string {
	if d.Time.IsZero() {
		return ""
	}
	return d.Time.Format(dateOnlyLayout)
}
