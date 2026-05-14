package omsapi

import (
	"context"
	"fmt"
	"net/url"
	"time"
)

type Notification struct {
	ID        any            `json:"id"`
	Type      string         `json:"type"`
	Title     string         `json:"title"`
	Message   string         `json:"message"`
	Read      bool           `json:"read"`
	CreatedAt time.Time      `json:"created_at,omitempty"`
	ActionURL string         `json:"action_url,omitempty"`
	Metadata  map[string]any `json:"metadata,omitempty"`
}

func (c *Client) ListNotifications(ctx context.Context, q url.Values) (*Page[Notification], error) {
	return GetPage[Notification](ctx, c, "/api/notifications/", q)
}

func (c *Client) ListUnreadNotifications(ctx context.Context) (*Page[Notification], error) {
	q := url.Values{"read": []string{"false"}}
	return c.ListNotifications(ctx, q)
}

func (c *Client) MarkNotificationRead(ctx context.Context, id any) error {
	return c.Post(ctx, fmt.Sprintf("/api/notifications/%v/mark-read/", id), nil, nil)
}

func (c *Client) MarkAllNotificationsRead(ctx context.Context) error {
	return c.Post(ctx, "/api/notifications/mark-all-read/", nil, nil)
}
