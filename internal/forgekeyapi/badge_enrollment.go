package forgekeyapi

import (
	"context"
	"fmt"
	"net/url"
)

// Badge enrollment (op-vj9) — assign an access badge (RFID UID) to a member.
//
// This mirrors the web ForgeKeyBadgeEnrollmentPage / forgekeyAPI badge-enrollment
// calls 1:1. The staff-only BadgeEnrollmentViewSet at /api/forgekey/badge-enrollment/
// offers three ways to bind a UID to a user:
//
//   - arm "enroll next scan" (ArmBadgeEnrollment) + poll (GetBadgeEnrollmentState)
//     until the access-control interlock captures the next physical tap;
//   - set/clear the UID directly (SetBadge) as a manual-entry fallback;
//
// The badge UID is credential material. Callers MUST NOT log the UID argument or
// the captured/returned value; the web shows it in the enrollment admin but never
// elsewhere, and neither should ScanTTY.

// BadgeCapture is a UID bound to a user by the interlock during an armed
// "enroll next scan" handshake.
type BadgeCapture struct {
	UserID      int    `json:"user_id"`
	BadgeNumber string `json:"badge_number"`
}

// BadgeEnrollmentState is the poll target for the arm flow: whether a scope is
// armed, and whether a scan has been captured for the polled user. It mirrors the
// web ForgeKeyBadgeEnrollmentState shape.
type BadgeEnrollmentState struct {
	Armed       bool          `json:"armed"`
	ArmedUserID *int          `json:"armed_user_id"`
	ReaderID    *string       `json:"reader_id"`
	Captured    *BadgeCapture `json:"captured"`
}

// GetBadgeEnrollmentState reports the armed/captured state. Reading it with a
// userID (>0) consumes a captured result for that user, exactly like the web
// poll (getBadgeEnrollmentState({userId})).
func (c *Client) GetBadgeEnrollmentState(ctx context.Context, userID int, readerID string) (*BadgeEnrollmentState, error) {
	q := url.Values{}
	if userID > 0 {
		q.Set("user_id", fmt.Sprintf("%d", userID))
	}
	if readerID != "" {
		q.Set("reader_id", readerID)
	}
	var out BadgeEnrollmentState
	if err := c.Get(ctx, "/api/forgekey/badge-enrollment/", q, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// BadgeArmResult is the response to arming an enrollment.
type BadgeArmResult struct {
	Armed      bool    `json:"armed"`
	UserID     int     `json:"user_id"`
	ReaderID   *string `json:"reader_id"`
	TTLSeconds int     `json:"ttl_seconds"`
}

type badgeArmRequest struct {
	UserID   int    `json:"user_id"`
	ReaderID string `json:"reader_id,omitempty"`
}

// ArmBadgeEnrollment arms "enroll next scan" for a user (optionally scoped to one
// reader). The backend requires user_id; a matching physical tap within
// ttl_seconds binds the UID, surfaced by the next GetBadgeEnrollmentState poll.
func (c *Client) ArmBadgeEnrollment(ctx context.Context, userID int, readerID string) (*BadgeArmResult, error) {
	var out BadgeArmResult
	// Router @action → trailing slash (DefaultRouter contract, matching the web).
	if err := c.Post(ctx, "/api/forgekey/badge-enrollment/arm/", badgeArmRequest{UserID: userID, ReaderID: readerID}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

type badgeCancelRequest struct {
	ReaderID string `json:"reader_id,omitempty"`
}

// CancelBadgeEnrollment disarms a pending enrollment for a scope (or the global
// one). Best-effort: the armed record TTL-expires on its own regardless.
func (c *Client) CancelBadgeEnrollment(ctx context.Context, readerID string) error {
	return c.Post(ctx, "/api/forgekey/badge-enrollment/cancel/", badgeCancelRequest{ReaderID: readerID}, nil)
}

// BadgeSetResult is the response to a manual set/clear. BadgeNumber is nil after a
// clear.
type BadgeSetResult struct {
	UserID      int     `json:"user_id"`
	BadgeNumber *string `json:"badge_number"`
}

type badgeSetRequest struct {
	UserID int `json:"user_id"`
	// No omitempty: a nil pointer must serialize as JSON null so the backend
	// clears the badge; "" would also clear, but null mirrors the web's
	// setBadge({badgeNumber: null}) exactly.
	BadgeNumber *string `json:"badge_number"`
}

// SetBadge sets a user's badge number directly (manual-entry fallback), or clears
// it when badge is nil. The backend returns 409 if the UID already belongs to
// another user. The UID is credential material — do NOT log badge or the result.
func (c *Client) SetBadge(ctx context.Context, userID int, badge *string) (*BadgeSetResult, error) {
	var out BadgeSetResult
	if err := c.Post(ctx, "/api/forgekey/badge-enrollment/set-badge/", badgeSetRequest{UserID: userID, BadgeNumber: badge}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
