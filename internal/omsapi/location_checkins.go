package omsapi

import (
	"context"
	"net/url"
	"time"
)

// LocationCheckIn mirrors backend LocationCheckInSerializer. Per
// [[scantty-api-field-drift]] note: Location keeps Django's int
// autoid; the LocationCheckIn id itself is a UUID (string).
//
// `user` is nullable because anonymous check-ins are a first-class
// shape on this endpoint — volunteers and contractors can drop a
// pin without an OMS account. `user_username` mirrors it as a
// pre-joined display string for the same reason; we don't try to
// look up users for anonymous rows.
type LocationCheckIn struct {
	ID           string    `json:"id"`
	Location     int       `json:"location"`
	LocationName string    `json:"location_name,omitempty"`
	CheckinType  string    `json:"checkin_type"`
	User         *int      `json:"user"`
	UserUsername *string   `json:"user_username"`
	CheckedInAt  time.Time `json:"checked_in_at"`
	Notes        string    `json:"notes,omitempty"`
}

// ListLocationCheckIns returns the paginated check-in list. Common
// filters: `?location=<id>` to scope to a single space, `?ordering=
// -checked_in_at` for newest-first.
func (c *Client) ListLocationCheckIns(ctx context.Context, q url.Values) (*Page[LocationCheckIn], error) {
	return GetPage[LocationCheckIn](ctx, c, "/api/location-checkins/checkins/", q)
}

// Checkin posts a single check-in via the AllowAny action endpoint.
// `checkinType` is one of "volunteer" / "contractor" / "anonymous";
// the backend coerces unknown values to "anonymous" and upgrades an
// authenticated "anonymous" call to "volunteer", so passing an empty
// string is safe — the endpoint will pick a sensible default based on
// whether scantty has a JWT loaded.
func (c *Client) Checkin(ctx context.Context, locationID int, checkinType, notes string) (*LocationCheckIn, error) {
	body := map[string]any{
		"location_id": locationID,
	}
	if checkinType != "" {
		body["checkin_type"] = checkinType
	}
	if notes != "" {
		body["notes"] = notes
	}
	var out LocationCheckIn
	if err := c.Post(ctx, "/api/location-checkins/checkins/checkin/", body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
