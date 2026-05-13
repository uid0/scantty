package omsapi

import (
	"context"
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
	ID          int        `json:"id"`
	Name        string     `json:"name"`
	GrantedAt   time.Time  `json:"granted_at,omitempty"`
	RevokedAt   *time.Time `json:"revoked_at,omitempty"`
	GrantedBy   string     `json:"granted_by,omitempty"`
	Asset       *int       `json:"asset,omitempty"`
	AssetName   string     `json:"asset_name,omitempty"`
}

func (c *Certification) Active() bool {
	return c.RevokedAt == nil
}

func (c *Client) GetProfile(ctx context.Context) (*Profile, error) {
	var out Profile
	if err := c.Get(ctx, "/api/membership/profile/", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

type SIG struct {
	ID          int    `json:"id"`
	Name        string `json:"name"`
	Slug        string `json:"slug,omitempty"`
	Description string `json:"description,omitempty"`
}

func (c *Client) ListSIGs(ctx context.Context, q url.Values) (*Page[SIG], error) {
	return GetPage[SIG](ctx, c, "/api/membership/sigs/", q)
}

type SIGMember struct {
	UserID    int    `json:"user_id"`
	Username  string `json:"username"`
	Email     string `json:"email,omitempty"`
	Role      string `json:"role,omitempty"`
	JoinedAt  time.Time `json:"joined_at,omitempty"`
}

func (c *Client) ListSIGMembers(ctx context.Context, sigID int, q url.Values) (*Page[SIGMember], error) {
	return GetPage[SIGMember](ctx, c, fmt.Sprintf("/api/membership/sigs/%d/members/", sigID), q)
}
