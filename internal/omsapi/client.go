package omsapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

type Client struct {
	baseURL    string
	httpClient *http.Client

	mu      sync.RWMutex
	access  string
	refresh string

	loginFn func(context.Context) (access, refresh string, err error)
}

type ClientOption func(*Client)

func WithHTTPClient(h *http.Client) ClientOption {
	return func(c *Client) { c.httpClient = h }
}

func WithToken(access, refresh string) ClientOption {
	return func(c *Client) {
		c.access = access
		c.refresh = refresh
	}
}

func New(baseURL string, opts ...ClientOption) *Client {
	c := &Client{
		baseURL:    strings.TrimRight(baseURL, "/"),
		httpClient: &http.Client{Timeout: 15 * time.Second},
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

func (c *Client) AccessToken() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.access
}

func (c *Client) SetTokens(access, refresh string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.access = access
	c.refresh = refresh
}

type LoginResponse struct {
	Access      string `json:"access"`
	Refresh     string `json:"refresh"`
	Username    string `json:"username"`
	Email       string `json:"email"`
	IsStaff     bool   `json:"is_staff"`
	IsSuperuser bool   `json:"is_superuser"`
}

func (c *Client) Login(ctx context.Context, username, password string) (*LoginResponse, error) {
	body := map[string]string{"username": username, "password": password}
	var out LoginResponse
	if err := c.do(ctx, http.MethodPost, "/api/auth/login/", nil, body, &out, false); err != nil {
		return nil, err
	}
	c.SetTokens(out.Access, out.Refresh)
	return &out, nil
}

func (c *Client) Refresh(ctx context.Context) error {
	c.mu.RLock()
	refresh := c.refresh
	c.mu.RUnlock()
	if refresh == "" {
		return &APIError{Code: "no_refresh_token", Message: "no refresh token available"}
	}
	body := map[string]string{"refresh": refresh}
	var out struct {
		Access  string `json:"access"`
		Refresh string `json:"refresh"`
	}
	if err := c.do(ctx, http.MethodPost, "/api/auth/refresh/", nil, body, &out, false); err != nil {
		return err
	}
	c.mu.Lock()
	c.access = out.Access
	if out.Refresh != "" {
		c.refresh = out.Refresh
	}
	c.mu.Unlock()
	return nil
}

func (c *Client) Get(ctx context.Context, path string, query url.Values, out any) error {
	return c.do(ctx, http.MethodGet, path, query, nil, out, true)
}

func (c *Client) Post(ctx context.Context, path string, body, out any) error {
	return c.do(ctx, http.MethodPost, path, nil, body, out, true)
}

func (c *Client) Patch(ctx context.Context, path string, body, out any) error {
	return c.do(ctx, http.MethodPatch, path, nil, body, out, true)
}

func (c *Client) Delete(ctx context.Context, path string) error {
	return c.do(ctx, http.MethodDelete, path, nil, nil, nil, true)
}

func (c *Client) do(ctx context.Context, method, path string, query url.Values, body, out any, retryAuth bool) error {
	u := c.baseURL + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}

	var bodyReader io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("oms: marshal body: %w", err)
		}
		bodyReader = bytes.NewReader(buf)
	}

	req, err := http.NewRequestWithContext(ctx, method, u, bodyReader)
	if err != nil {
		return fmt.Errorf("oms: build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if tok := c.AccessToken(); tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("oms: %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized && retryAuth && c.refresh != "" {
		if rerr := c.Refresh(ctx); rerr == nil {
			return c.do(ctx, method, path, query, body, out, false)
		}
	}

	if resp.StatusCode >= 400 {
		return parseError(resp)
	}

	if out == nil || resp.StatusCode == http.StatusNoContent {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil && err != io.EOF {
		return fmt.Errorf("oms: decode response: %w", err)
	}
	return nil
}

type Page[T any] struct {
	Count    int     `json:"count"`
	Next     *string `json:"next"`
	Previous *string `json:"previous"`
	Results  []T     `json:"results"`
}

func GetPage[T any](ctx context.Context, c *Client, path string, q url.Values) (*Page[T], error) {
	var page Page[T]
	if err := c.Get(ctx, path, q, &page); err != nil {
		return nil, err
	}
	return &page, nil
}

func IterPages[T any](ctx context.Context, c *Client, path string, q url.Values, fn func([]T) error) error {
	if q == nil {
		q = url.Values{}
	}
	page := 1
	for {
		q.Set("page", fmt.Sprintf("%d", page))
		p, err := GetPage[T](ctx, c, path, q)
		if err != nil {
			return err
		}
		if err := fn(p.Results); err != nil {
			return err
		}
		if p.Next == nil || *p.Next == "" {
			return nil
		}
		page++
	}
}
