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
	ID        int    `json:"id"`
	Username  string `json:"username"`
	Email     string `json:"email,omitempty"`
	FirstName string `json:"first_name,omitempty"`
	LastName  string `json:"last_name,omitempty"`
	// UserDirectorySerializer emits full_name (a SerializerMethodField), not
	// display_name — the old tag left the user picker showing only usernames.
	DisplayName string `json:"full_name,omitempty"`
	IsActive    bool   `json:"is_active,omitempty"`
	// BadgeNumber is the member's access-badge UID (nil = no badge), emitted by
	// the staff-only UserDirectorySerializer. It backs the ForgeKey badge-
	// enrollment screen; other user-picker screens ignore it. It is credential
	// material — display it only where the web does (the enrollment admin) and
	// never log it.
	BadgeNumber *string `json:"badge_number,omitempty"`
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

// ListSIGMembers lists a SIG's members. SIGMemberViewSet is a plain
// viewsets.ViewSet whose list() returns Response(serializer.data) — a BARE
// JSON array with no pagination — so GetPage's envelope-only decode crashed
// the members screen ("cannot unmarshal array into ... Page"). MaybeList
// tolerates both shapes, mirroring listUsersAt.
func (c *Client) ListSIGMembers(ctx context.Context, sigID int, q url.Values) (*Page[SIGMember], error) {
	var out MaybeList[SIGMember]
	if err := c.Get(ctx, fmt.Sprintf("/api/membership/sigs/%d/members/", sigID), q, &out); err != nil {
		return nil, err
	}
	return &Page[SIGMember]{Count: out.Count, Results: out.Items}, nil
}

// SIGWrite is the writable field set for a SIG. A SIG *is* a Django auth Group;
// the backend SIGCreateSerializer (used for create/update/partial_update)
// exposes exactly two writable fields:
//
//   - name         required, unique, non-blank (server-validated)
//   - group_email  optional contact email, persisted on the SIGProfile
//
// There is deliberately no description / color / lead / is_active field — the
// serializer defines none, so sending them would be silently dropped. Both keys
// carry NO omitempty: name is always required, and an always-present group_email
// lets a PATCH clear a previously-set address (the serializer treats a
// present-but-blank value as "clear the profile email"), matching the web form
// which always submits the email input.
type SIGWrite struct {
	Name       string `json:"name"`
	GroupEmail string `json:"group_email"`
}

// GetSIG retrieves a single SIG. SIGViewSet is a ModelViewSet, so the plain
// detail route returns the read SIGSerializer (id, name, group_email, the three
// counts, admins and is_user_admin).
func (c *Client) GetSIG(ctx context.Context, id int) (*SIG, error) {
	var out SIG
	if err := c.Get(ctx, fmt.Sprintf("/api/membership/sigs/%d/", id), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// CreateSIG creates a SIG (Group). Staff/superuser only server-side; a
// non-staff caller gets a 403 which the caller surfaces. The create response is
// the SIGCreateSerializer representation (id, name, group_email).
func (c *Client) CreateSIG(ctx context.Context, body SIGWrite) (*SIG, error) {
	var out SIG
	if err := c.Post(ctx, "/api/membership/sigs/", body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// UpdateSIG edits a SIG via PATCH (partial_update → SIGCreateSerializer).
// Staff/superuser only server-side.
func (c *Client) UpdateSIG(ctx context.Context, id int, body SIGWrite) (*SIG, error) {
	var out SIG
	if err := c.Patch(ctx, fmt.Sprintf("/api/membership/sigs/%d/", id), body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteSIG deletes a SIG. Staff/superuser only server-side. Deletion never
// FK-409s: both owning_group FKs (Asset, InventoryItem) are SET_NULL, the
// user_set M2M simply unlinks, and SIGProfile/SIGAdmin cascade — so a SIG that
// still owns assets or has members deletes cleanly, orphaning those resources to
// space-owned rather than blocking.
func (c *Client) DeleteSIG(ctx context.Context, id int) error {
	return c.Delete(ctx, fmt.Sprintf("/api/membership/sigs/%d/", id))
}

// AddSIGMember adds a user to a SIG. SIGMemberViewSet.create expects a
// {"user_id": <id>} body on the nested collection and returns the added user
// (201). Server-side it is an idempotent Group.user_set.add, so re-adding an
// existing member is a harmless no-op. Permitted for staff/superuser or an admin
// of that SIG.
func (c *Client) AddSIGMember(ctx context.Context, sigID, userID int) error {
	body := map[string]int{"user_id": userID}
	return c.Post(ctx, fmt.Sprintf("/api/membership/sigs/%d/members/", sigID), body, nil)
}

// RemoveSIGMember removes a user from a SIG. The nested detail route's pk is the
// USER id (SIGMemberViewSet.destroy(sig_pk, pk) → Group.user_set.remove); the
// backend replies 204. Permitted for staff/superuser or an admin of that SIG.
func (c *Client) RemoveSIGMember(ctx context.Context, sigID, userID int) error {
	return c.Delete(ctx, fmt.Sprintf("/api/membership/sigs/%d/members/%d/", sigID, userID))
}

// ListAllUsers walks every page of the user directory and returns the full set.
// The member-add picker must be able to reach a user on ANY page, not just the
// first — the same rule the paginated-FK pickers follow — so this pages through
// rather than returning ListUsers' first page. It mirrors ListUsers' endpoint
// fallback (/api/membership/users/ → /api/users/) for older backends. The
// directory is staff-gated (IsAdminUser); a non-staff caller gets a 403 the
// caller surfaces.
func (c *Client) ListAllUsers(ctx context.Context, q url.Values) ([]User, error) {
	all, err := c.iterAllUsersAt(ctx, "/api/membership/users/", q)
	if err == nil {
		return all, nil
	}
	var apiErr *APIError
	if errors.As(err, &apiErr) && apiErr.IsNotFound() {
		return c.iterAllUsersAt(ctx, "/api/users/", q)
	}
	return nil, err
}

func (c *Client) iterAllUsersAt(ctx context.Context, path string, q url.Values) ([]User, error) {
	var all []User
	if err := IterPages[User](ctx, c, path, q, func(batch []User) error {
		all = append(all, batch...)
		return nil
	}); err != nil {
		return nil, err
	}
	return all, nil
}
