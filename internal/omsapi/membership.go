package omsapi

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"time"
)

type Profile struct {
	ID             int             `json:"id"`
	Username       string          `json:"username"`
	Email          string          `json:"email,omitempty"`
	FirstName      string          `json:"first_name,omitempty"`
	LastName       string          `json:"last_name,omitempty"`
	IsStaff        bool            `json:"is_staff"`
	IsSuperuser    bool            `json:"is_superuser"`
	Groups         []string        `json:"groups,omitempty"`
	Certifications []Certification `json:"certifications,omitempty"`
	BadgeIDs       []string        `json:"badge_ids,omitempty"`
}

type Certification struct {
	ID        int        `json:"id"`
	Name      string     `json:"name"`
	GrantedAt time.Time  `json:"granted_at,omitempty"`
	RevokedAt *time.Time `json:"revoked_at,omitempty"`
	GrantedBy string     `json:"granted_by,omitempty"`
	Asset     *int       `json:"asset,omitempty"`
	AssetName string     `json:"asset_name,omitempty"`
}

func (c *Certification) Active() bool {
	return c.RevokedAt == nil
}

// GetProfile fetches the current user's membership profile.
//
// The OMS endpoint is the @action `me` on UserProfileViewSet, mounted at
// /api/membership/profile/me/ — the bare /api/membership/profile/ URL is
// the router root for a ViewSet with no list action, so it returns 404.
func (c *Client) GetProfile(ctx context.Context) (*Profile, error) {
	var out Profile
	if err := c.Get(ctx, "/api/membership/profile/me/", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

type User struct {
	ID          int    `json:"id"`
	Username    string `json:"username"`
	Email       string `json:"email,omitempty"`
	FirstName   string `json:"first_name,omitempty"`
	LastName    string `json:"last_name,omitempty"`
	DisplayName string `json:"display_name,omitempty"`
	IsActive    bool   `json:"is_active,omitempty"`
}

func (c *Client) ListUsers(ctx context.Context, q url.Values) (*Page[User], error) {
	page, err := c.listUsersAt(ctx, "/api/membership/users/", q)
	if err == nil {
		return page, nil
	}
	var apiErr *APIError
	if errors.As(err, &apiErr) && apiErr.IsNotFound() {
		return c.listUsersAt(ctx, "/api/users/", q)
	}
	return nil, err
}

func (c *Client) listUsersAt(ctx context.Context, path string, q url.Values) (*Page[User], error) {
	var out MaybeList[User]
	if err := c.Get(ctx, path, q, &out); err != nil {
		return nil, err
	}
	return &Page[User]{
		Count:   out.Count,
		Results: out.Items,
	}, nil
}

type SIG struct {
	ID             int        `json:"id"`
	Name           string     `json:"name"`
	GroupEmail     string     `json:"group_email,omitempty"`
	MemberCount    int        `json:"member_count,omitempty"`
	AssetCount     int        `json:"asset_count,omitempty"`
	InventoryCount int        `json:"inventory_count,omitempty"`
	IsUserAdmin    bool       `json:"is_user_admin,omitempty"`
	Admins         []SIGAdmin `json:"admins,omitempty"`
}

type SIGAdmin struct {
	ID       int    `json:"id"`
	Username string `json:"username"`
	Email    string `json:"email,omitempty"`
	Handle   string `json:"handle,omitempty"`
}

func (c *Client) ListSIGs(ctx context.Context, q url.Values) (*Page[SIG], error) {
	return GetPage[SIG](ctx, c, "/api/membership/sigs/", q)
}

type SIGMember struct {
	ID         int    `json:"id"`
	Username   string `json:"username"`
	Email      string `json:"email,omitempty"`
	Handle     string `json:"handle,omitempty"`
	IsSIGAdmin bool   `json:"is_sig_admin,omitempty"`
}

func (c *Client) ListSIGMembers(ctx context.Context, sigID int, q url.Values) (*Page[SIGMember], error) {
	return GetPage[SIGMember](ctx, c, fmt.Sprintf("/api/membership/sigs/%d/members/", sigID), q)
}
