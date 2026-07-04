package omsapi

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
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
	// Preserve the HTTP method across redirects unless the caller supplied
	// a client with its own policy. Without this, net/http downgrades a
	// redirected POST/PATCH/DELETE to GET (see preserveMethodOnRedirect),
	// which silently turned mark-printed into a no-op GET whenever
	// OMS_API_BASE was http:// and the server 301-upgraded to https://.
	if c.httpClient.CheckRedirect == nil {
		c.httpClient.CheckRedirect = preserveMethodOnRedirect
	}
	return c
}

// maxRedirects bounds redirect-following, matching net/http's own default.
const maxRedirects = 10

// preserveMethodOnRedirect keeps the original method and body when following
// a redirect. net/http's default converts a 301/302/303 on a POST (or other
// non-idempotent method) into a GET with no body — browser behaviour that is
// wrong for an API client: a state-changing call arrives at the server as a
// read and silently does nothing. That is exactly what bit the claim-tag
// printer — an OMS_API_BASE of "http://…" 301-upgrades to "https://…", so the
// GET list/label calls worked but the POST mark-printed reached the backend
// as a GET ("Method GET not allowed"), the stint never drained, and the label
// reprinted every poll. Re-issuing with the original method + a fresh body
// makes the upgrade transparent. The Authorization/Content-Type/Accept headers
// are re-attached only when the redirect stays on the same host, so the bearer
// token can never leak to a different origin.
func preserveMethodOnRedirect(req *http.Request, via []*http.Request) error {
	if len(via) == 0 {
		return nil
	}
	if len(via) >= maxRedirects {
		return fmt.Errorf("oms: stopped after %d redirects", maxRedirects)
	}
	orig := via[0]
	req.Method = orig.Method
	if orig.GetBody != nil {
		body, err := orig.GetBody()
		if err != nil {
			return fmt.Errorf("oms: replay body across redirect: %w", err)
		}
		req.Body = body
		req.ContentLength = orig.ContentLength
		req.GetBody = orig.GetBody
	}
	if req.URL.Host == orig.URL.Host {
		for _, h := range []string{"Authorization", "Content-Type", "Accept"} {
			if v := orig.Header.Get(h); v != "" {
				req.Header.Set(h, v)
			}
		}
	}
	return nil
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

func (c *Client) RefreshToken() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.refresh
}

func (c *Client) BaseURL() string { return c.baseURL }

type JWTClaims struct {
	Exp         int64  `json:"exp"`
	UserID      int    `json:"user_id"`
	Username    string `json:"username,omitempty"`
	IsStaff     bool   `json:"is_staff,omitempty"`
	IsSuperuser bool   `json:"is_superuser,omitempty"`
}

func DecodeJWTClaims(token string) (*JWTClaims, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, fmt.Errorf("oms: jwt: expected 3 parts, got %d", len(parts))
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		payload, err = base64.URLEncoding.DecodeString(parts[1])
		if err != nil {
			return nil, fmt.Errorf("oms: jwt: decode payload: %w", err)
		}
	}
	var claims JWTClaims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return nil, fmt.Errorf("oms: jwt: unmarshal: %w", err)
	}
	return &claims, nil
}

func (c JWTClaims) ExpiresAt() time.Time {
	if c.Exp == 0 {
		return time.Time{}
	}
	return time.Unix(c.Exp, 0)
}

func (c JWTClaims) IsExpired(skew time.Duration) bool {
	exp := c.ExpiresAt()
	if exp.IsZero() {
		return false
	}
	return time.Now().Add(skew).After(exp)
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

// GetBytes fetches a non-JSON response body in full and returns the raw
// bytes. Used for image/binary endpoints like the project-storage label
// PNG, where the standard do() path can't help because it always tries to
// JSON-decode. `urlOrPath` may be an absolute URL (e.g. a backend-supplied
// label_url) or a baseURL-relative path starting with "/".
func (c *Client) GetBytes(ctx context.Context, urlOrPath string) ([]byte, error) {
	u := urlOrPath
	if strings.HasPrefix(urlOrPath, "/") {
		u = c.baseURL + urlOrPath
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("oms: build request: %w", err)
	}
	if tok := c.AccessToken(); tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("oms: GET %s: %w", urlOrPath, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, parseError(resp)
	}
	return io.ReadAll(resp.Body)
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

func (c *Client) PostMultipart(ctx context.Context, path string, fields map[string][]string, fileField, filePath string, out any) error {
	return c.doMultipart(ctx, http.MethodPost, path, fields, fileField, filePath, out, true)
}

func (c *Client) PatchMultipart(ctx context.Context, path string, fields map[string][]string, fileField, filePath string, out any) error {
	return c.doMultipart(ctx, http.MethodPatch, path, fields, fileField, filePath, out, true)
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

func (c *Client) doMultipart(ctx context.Context, method, path string, fields map[string][]string, fileField, filePath string, out any, retryAuth bool) error {
	u := c.baseURL + path

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for k, vals := range fields {
		for _, v := range vals {
			if err := mw.WriteField(k, v); err != nil {
				return fmt.Errorf("oms: multipart field %s: %w", k, err)
			}
		}
	}
	if strings.TrimSpace(filePath) != "" {
		f, err := os.Open(filePath)
		if err != nil {
			return fmt.Errorf("oms: open %s: %w", filePath, err)
		}
		part, err := mw.CreateFormFile(fileField, filepath.Base(filePath))
		if err != nil {
			_ = f.Close()
			return fmt.Errorf("oms: multipart file %s: %w", fileField, err)
		}
		if _, err := io.Copy(part, f); err != nil {
			_ = f.Close()
			return fmt.Errorf("oms: read %s: %w", filePath, err)
		}
		if err := f.Close(); err != nil {
			return fmt.Errorf("oms: close %s: %w", filePath, err)
		}
	}
	if err := mw.Close(); err != nil {
		return fmt.Errorf("oms: close multipart: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, method, u, bytes.NewReader(buf.Bytes()))
	if err != nil {
		return fmt.Errorf("oms: build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", mw.FormDataContentType())
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
			return c.doMultipart(ctx, method, path, fields, fileField, filePath, out, false)
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

// MaybeList[T] decodes an OMS list response that may arrive either as a
// bare JSON array (DRF view returning `Response(serializer.data)` without
// pagination — typically @action endpoints like /pending/) or as a
// `{count, next, previous, results}` envelope. Both shapes show up so
// any caller that doesn't know which it'll get must tolerate both.
type MaybeList[T any] struct {
	Items []T
	Count int
}

func (m *MaybeList[T]) UnmarshalJSON(data []byte) error {
	var arr []T
	if err := json.Unmarshal(data, &arr); err == nil {
		m.Items = arr
		m.Count = len(arr)
		return nil
	}
	var env Page[T]
	if err := json.Unmarshal(data, &env); err != nil {
		return err
	}
	m.Items = env.Results
	m.Count = env.Count
	return nil
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
