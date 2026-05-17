package forgekeyapi

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

type Client struct {
	baseURL    string
	httpClient *http.Client
	authToken  string
	// authTokenFunc, when set, takes precedence over authToken. Used to
	// borrow the OMS JWT at request time so the FK client tracks token
	// refreshes without manual re-syncing.
	authTokenFunc func() string
}

type Options struct {
	BaseURL    string
	AuthToken  string
	// AuthTokenFunc is invoked on every request to obtain the current
	// bearer token. Set this when the FK API shares an auth realm with
	// another client (typically OMS) so refreshes propagate automatically.
	AuthTokenFunc func() string
	ClientCert    string
	ClientKey     string
	CACert        string
	Timeout       time.Duration
}

func New(opts Options) (*Client, error) {
	if opts.Timeout == 0 {
		opts.Timeout = 15 * time.Second
	}
	tlsCfg := &tls.Config{}
	if opts.ClientCert != "" && opts.ClientKey != "" {
		cert, err := tls.LoadX509KeyPair(opts.ClientCert, opts.ClientKey)
		if err != nil {
			return nil, fmt.Errorf("forgekey: load client keypair: %w", err)
		}
		tlsCfg.Certificates = []tls.Certificate{cert}
	}
	if opts.CACert != "" {
		ca, err := os.ReadFile(opts.CACert)
		if err != nil {
			return nil, fmt.Errorf("forgekey: read CA: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(ca) {
			return nil, fmt.Errorf("forgekey: parse CA pem")
		}
		tlsCfg.RootCAs = pool
	}
	return &Client{
		baseURL: strings.TrimRight(opts.BaseURL, "/"),
		httpClient: &http.Client{
			Timeout:   opts.Timeout,
			Transport: &http.Transport{TLSClientConfig: tlsCfg},
		},
		authToken:     opts.AuthToken,
		authTokenFunc: opts.AuthTokenFunc,
	}, nil
}

// SetAuthToken updates the static bearer token used by future requests.
// Has no effect when AuthTokenFunc is set, since that takes precedence.
func (c *Client) SetAuthToken(token string) { c.authToken = token }

type APIError struct {
	Status  int    `json:"-"`
	Message string `json:"detail,omitempty"`
	Raw     string `json:"-"`
}

func (e *APIError) Error() string {
	if e.Message != "" {
		return fmt.Sprintf("forgekey: %d: %s", e.Status, e.Message)
	}
	return fmt.Sprintf("forgekey: http %d: %s", e.Status, e.Raw)
}

func (c *Client) do(ctx context.Context, method, path string, query url.Values, body, out any) error {
	u := c.baseURL + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	var bodyReader io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("forgekey: marshal: %w", err)
		}
		bodyReader = bytes.NewReader(buf)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, bodyReader)
	if err != nil {
		return fmt.Errorf("forgekey: build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	token := c.authToken
	if c.authTokenFunc != nil {
		if t := c.authTokenFunc(); t != "" {
			token = t
		}
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("forgekey: %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		raw, _ := io.ReadAll(resp.Body)
		apiErr := &APIError{Status: resp.StatusCode, Raw: string(raw)}
		_ = json.Unmarshal(raw, apiErr)
		return apiErr
	}
	if out == nil || resp.StatusCode == http.StatusNoContent {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil && err != io.EOF {
		return fmt.Errorf("forgekey: decode: %w", err)
	}
	return nil
}

func (c *Client) Get(ctx context.Context, path string, q url.Values, out any) error {
	return c.do(ctx, http.MethodGet, path, q, nil, out)
}

func (c *Client) Post(ctx context.Context, path string, body, out any) error {
	return c.do(ctx, http.MethodPost, path, nil, body, out)
}

func (c *Client) Patch(ctx context.Context, path string, body, out any) error {
	return c.do(ctx, http.MethodPatch, path, nil, body, out)
}

func (c *Client) Delete(ctx context.Context, path string) error {
	return c.do(ctx, http.MethodDelete, path, nil, nil, nil)
}

// MaybeList[T] decodes a ForgeKey list response that may arrive either as a
// bare JSON array (DRF default with no PageNumberPagination) or as a
// `{count, next, previous, results: [...]}` envelope (DRF with pagination).
// Both shapes show up in practice depending on the endpoint and the
// deployed forgekey version, so list callers must tolerate both.
type MaybeList[T any] struct {
	Items []T
	Count int
}

func (m *MaybeList[T]) UnmarshalJSON(data []byte) error {
	// Try bare array first — the more common shape and the cheaper parse.
	var arr []T
	if err := json.Unmarshal(data, &arr); err == nil {
		m.Items = arr
		m.Count = len(arr)
		return nil
	}
	var env struct {
		Count   int `json:"count"`
		Results []T `json:"results"`
	}
	if err := json.Unmarshal(data, &env); err != nil {
		return err
	}
	m.Items = env.Results
	m.Count = env.Count
	return nil
}
