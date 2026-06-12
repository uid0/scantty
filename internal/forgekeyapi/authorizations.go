package forgekeyapi

import (
	"context"
	"fmt"
	"net/url"
	"time"
)

type Authorization struct {
	ID            any        `json:"id"`
	Asset         any        `json:"asset"`
	AssetName     string     `json:"asset_name,omitempty"`
	User          any        `json:"user"`
	UserName      string     `json:"user_name,omitempty"`
	AuthorizedBy  any        `json:"authorized_by,omitempty"`
	IsActive      bool       `json:"is_active"`
	Notes         string     `json:"notes,omitempty"`
	AuthorizedAt  time.Time  `json:"authorized_at,omitempty"`
	RevokedAt     *time.Time `json:"revoked_at,omitempty"`
}

func (c *Client) ListAuthorizations(ctx context.Context, q url.Values) ([]Authorization, error) {
	var out MaybeList[Authorization]
	if err := c.Get(ctx, "/api/forgekey/authorizations/", q, &out); err != nil {
		return nil, err
	}
	return out.Items, nil
}

func (c *Client) FindAuthorization(ctx context.Context, userID, assetID int) (*Authorization, error) {
	q := url.Values{}
	q.Set("user", fmt.Sprintf("%d", userID))
	q.Set("asset", fmt.Sprintf("%d", assetID))
	list, err := c.ListAuthorizations(ctx, q)
	if err != nil {
		return nil, err
	}
	for i := range list {
		if list[i].IsActive {
			return &list[i], nil
		}
	}
	if len(list) > 0 {
		return &list[0], nil
	}
	return nil, nil
}

func (c *Client) CreateAuthorization(ctx context.Context, a Authorization) (*Authorization, error) {
	var out Authorization
	if err := c.Post(ctx, "/api/forgekey/authorizations/", a, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) RevokeAuthorization(ctx context.Context, id string) error {
	return c.Post(ctx, fmt.Sprintf("/api/forgekey/authorizations/%s/revoke", id), nil, nil)
}

type ClassroomEnrollRequest struct {
	AssetID int `json:"asset_id"`
}

func (c *Client) ClassroomEnroll(ctx context.Context, assetID int) (*Authorization, error) {
	var out Authorization
	req := ClassroomEnrollRequest{AssetID: assetID}
	if err := c.Post(ctx, "/api/forgekey/authorizations/add_via_classroom_mode/", req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

type Lockout struct {
	ID           any       `json:"id"`
	Asset        any       `json:"asset"`
	AssetName    string    `json:"asset_name,omitempty"`
	LockedBy     any       `json:"locked_by"`
	LockoutLevel string    `json:"lockout_level"`
	Reason       string    `json:"reason,omitempty"`
	IsActive     bool      `json:"is_active"`
	CreatedAt    time.Time `json:"created_at,omitempty"`
	UnlockedAt   *time.Time `json:"unlocked_at,omitempty"`
}

func (c *Client) ListLockouts(ctx context.Context, q url.Values) ([]Lockout, error) {
	var out MaybeList[Lockout]
	if err := c.Get(ctx, "/api/forgekey/lockouts/", q, &out); err != nil {
		return nil, err
	}
	return out.Items, nil
}

func (c *Client) CreateLockout(ctx context.Context, l Lockout) (*Lockout, error) {
	var out Lockout
	if err := c.Post(ctx, "/api/forgekey/lockouts/", l, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) Unlock(ctx context.Context, id string) error {
	return c.Post(ctx, fmt.Sprintf("/api/forgekey/lockouts/%s/unlock", id), nil, nil)
}

type OperationalMode struct {
	ID                     any        `json:"id"`
	Asset                  any        `json:"asset"`
	AssetName              string     `json:"asset_name,omitempty"`
	Mode                   string     `json:"mode"`
	ClassroomModeEnabled   bool       `json:"classroom_mode_enabled"`
	ClassroomModeEnabledBy *any       `json:"classroom_mode_enabled_by,omitempty"`
	ClassroomModeEnabledAt *time.Time `json:"classroom_mode_enabled_at,omitempty"`
}

func (c *Client) ListOperationalModes(ctx context.Context, q url.Values) ([]OperationalMode, error) {
	var out MaybeList[OperationalMode]
	if err := c.Get(ctx, "/api/forgekey/operational-modes/", q, &out); err != nil {
		return nil, err
	}
	return out.Items, nil
}

func (c *Client) EnableClassroomMode(ctx context.Context, id string) error {
	return c.Post(ctx, fmt.Sprintf("/api/forgekey/operational-modes/%s/enable_classroom_mode", id), nil, nil)
}

func (c *Client) DisableClassroomMode(ctx context.Context, id string) error {
	return c.Post(ctx, fmt.Sprintf("/api/forgekey/operational-modes/%s/disable_classroom_mode", id), nil, nil)
}

type Usage struct {
	ID        any        `json:"id"`
	Asset     any        `json:"asset"`
	AssetName string     `json:"asset_name,omitempty"`
	User      any        `json:"user"`
	UserName  string     `json:"user_name,omitempty"`
	StartedAt time.Time  `json:"started_at"`
	EndedAt   *time.Time `json:"ended_at,omitempty"`
}

func (c *Client) ListUsage(ctx context.Context, q url.Values) ([]Usage, error) {
	var out MaybeList[Usage]
	if err := c.Get(ctx, "/api/forgekey/usage/", q, &out); err != nil {
		return nil, err
	}
	return out.Items, nil
}

func (c *Client) EndSession(ctx context.Context, id string) error {
	return c.Post(ctx, fmt.Sprintf("/api/forgekey/usage/%s/end_session", id), nil, nil)
}
