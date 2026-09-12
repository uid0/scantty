package omsapi

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"path/filepath"
	"sort"
	"strconv"
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

// PostBytes POSTs a JSON body and returns a NON-JSON response body in full,
// plus the filename the server suggested in Content-Disposition (empty when it
// sent none). The mirror of GetBytes for endpoints that take a request document
// and answer with a file — the storage-slot card sheets return the PDF itself
// rather than a stored URL, so do() (which always JSON-decodes) can't serve
// them. Unlike GetBytes this repeats do()'s 401-refresh-retry, because it is a
// write-shaped call an operator triggers by hand and a silent auth failure
// mid-session would look like a broken printer.
func (c *Client) PostBytes(ctx context.Context, path string, body any) ([]byte, string, error) {
	return c.postBytes(ctx, path, body, true)
}

func (c *Client) postBytes(ctx context.Context, path string, body any, retryAuth bool) ([]byte, string, error) {
	var bodyReader io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return nil, "", fmt.Errorf("oms: marshal body: %w", err)
		}
		bodyReader = bytes.NewReader(buf)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bodyReader)
	if err != nil {
		return nil, "", fmt.Errorf("oms: build request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if tok := c.AccessToken(); tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("oms: POST %s: %w", path, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized && retryAuth && c.RefreshToken() != "" {
		if rerr := c.Refresh(ctx); rerr == nil {
			return c.postBytes(ctx, path, body, false)
		}
	}
	if resp.StatusCode >= 400 {
		return nil, "", parseError(resp)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", fmt.Errorf("oms: read response: %w", err)
	}
	return data, filenameFromContentDisposition(resp.Header.Get("Content-Disposition")), nil
}

// filenameFromContentDisposition pulls the filename out of an
// `attachment; filename="x.pdf"` header. Returns "" when the header is absent
// or carries no filename, so callers fall back to a name of their own; any
// directory part is dropped so a hostile header can't steer a local write.
func filenameFromContentDisposition(header string) string {
	if header == "" {
		return ""
	}
	_, params, err := mime.ParseMediaType(header)
	if err != nil {
		return ""
	}
	name := strings.TrimSpace(params["filename"])
	if name == "" {
		return ""
	}
	// filepath.Base(".") and Base("/") are "." and "/" — neither is a usable
	// filename, so fold them back to "empty" rather than writing to them.
	if base := filepath.Base(name); base != "." && base != string(filepath.Separator) {
		return base
	}
	return ""
}

func (c *Client) Patch(ctx context.Context, path string, body, out any) error {
	return c.do(ctx, http.MethodPatch, path, nil, body, out, true)
}

func (c *Client) Delete(ctx context.Context, path string) error {
	return c.do(ctx, http.MethodDelete, path, nil, nil, nil, true)
}

// DeleteInto is Delete for an endpoint that ANSWERS. Most of OMS's destroy
// routes return 204 with nothing in them, which is what Delete above is for;
// the PO line-delete action returns 200 with the account of what it destroyed
// plus the refreshed order (OMS's docs/REACTIVE_MUTATIONS.md, in the
// openmakersuite checkout — there is no docs/ tree on this side).
//
// What the caller wants out of that body is the SERVER's own account of what it
// destroyed — by the time it is read, the row that named the line is gone and
// the flash is the only record of it left on screen, so the words in it should
// be the words the audit trail carries rather than the label this side happened
// to be showing. The refreshed order riding beside it is deliberately NOT used
// as the new view: the screen reloads instead, because a purchase order is
// edited from more places than this one action and the reload is what picks up
// everything else that moved. Decoding is what makes the first fact reachable
// at all; discarding the body would leave nothing but a guess.
func (c *Client) DeleteInto(ctx context.Context, path string, out any) error {
	return c.do(ctx, http.MethodDelete, path, nil, nil, out, true)
}

// MultipartFile is one file part in a multipart/form-data upload. Data holds
// the whole file in memory — fine for the photo/PDF uploads this serves, which
// an operator picks one at a time from a local path.
type MultipartFile struct {
	Field    string // form field name, e.g. "image" or "pdf"
	Filename string // filename reported to the server
	Data     []byte // file contents
}

// PostMultipart / PatchMultipart send a multipart/form-data request. fields are
// text form values keyed by field name; a key may carry multiple values (an M2M
// list serializes as a repeated field). files are in-memory file parts. The
// JSON response is decoded into out (may be nil). Both mirror do()'s Bearer-auth
// and 401-refresh-retry so photo / PDF / attachment uploads survive an expired
// access token the same way JSON calls do.
func (c *Client) PostMultipart(ctx context.Context, path string, fields map[string][]string, files []MultipartFile, out any) error {
	return c.doMultipart(ctx, http.MethodPost, path, fields, files, out, true)
}

func (c *Client) PatchMultipart(ctx context.Context, path string, fields map[string][]string, files []MultipartFile, out any) error {
	return c.doMultipart(ctx, http.MethodPatch, path, fields, files, out, true)
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
	if err := decodeBody(resp.Body, out); err != nil {
		return err
	}
	return nil
}

// jsonDecoder is the ONE place this package chooses a JSON decoder's options,
// so what an `any` holds is decided here rather than per endpoint.
//
// UseNumber is the whole point of it. Several ids on these payloads are typed
// `any` because the client carries whatever the serializer echoed — a UUID
// string on some models, a BigAutoField NUMBER on others — and the callers turn
// that back into a path segment with fmt. Decoded the default way a JSON number
// lands in an `any` as a float64, and `%v` formats a float64 with `%g`: a
// seven-digit purchase-order id renders "1e+06". That string is spent on the
// wire — internal/tui/po_add_line.go builds `/purchase-orders/<id>/item-lookup/`
// from it and internal/tui/po_edit.go builds the line paths the same way — so
// against a real OMS the order that answers 200 at `.../1000000/` answers 404
// at `.../1e+06/`.
//
// json.Number keeps the server's own digits, so `%v` gives them back exactly.
// It is set at the decoder rather than fixed at each fmt call because the id
// sites are not a list anyone can keep: `any` fields are decoded all over this
// package and stringified all over internal/tui.
//
// Nothing in the package reads a decoded `any` as a float64 — a json.Number is
// a string underneath and arithmetic on one has to go through Float64()/Int64()
// — so this narrows what an `any` can hold without changing any other reader.
// TestAnyID_ANumericIDKeepsItsDigits is the guard.
//
// WHY THIS IS A DECODER FACTORY AND NOT JUST decodeBody, which is the correction
// a review had to make to the first version of the sentence above: a custom
// json.Unmarshaler is handed the RAW BYTES of its value and decodes them itself,
// so nothing an outer decoder was configured with reaches inside one.
// MaybeList[T].UnmarshalJSON is such a type and it is on the read path of real
// endpoints — ListPendingReorders decodes MaybeList[ReorderRequest], whose ID is
// `any`, and internal/tui/reorder_queue.go spends that id with %v as the path
// segment of `/api/reorders/requests/<id>/approve/`. Written as one function
// setting UseNumber on its own decoder, the option stopped at the envelope and a
// seven-digit reorder pk approved "1e+06", exactly the 404 this comment already
// described for item-lookup.
//
// WHAT THAT MAKES TRUE is a claim about UNTYPED values, not about decoders, and
// the difference is what two earlier wordings here got wrong. UseNumber changes
// exactly one thing: the Go type a JSON NUMBER takes when it lands somewhere
// with no declared type — an `any`, or a map[string]any value. So the rule is
//
//	NOTHING THAT CAN PRODUCE AN UNTYPED VALUE MAY BE DECODED BY A DECODER THAT
//	IS NOT jsonDecoder.
//
// A new json.Unmarshaler on a payload type that yields an `any` or a
// map[string]any must therefore call jsonDecoder rather than reach for
// json.Unmarshal, which cannot be configured at all. MaybeList is exactly that
// case and is why this function exists.
//
// TWO OTHER json.Unmarshalers ON PAYLOAD TYPES DO NOT CALL IT, and they are not
// holes — a reader grepping UnmarshalJSON to check this sentence will find them,
// so they are named here rather than left to look like counterexamples.
// DecimalString (decimal.go) and DateOnly (dateonly.go) each parse a SCALAR
// straight into a fully typed Go value — a string and a time.Time — deciding
// their own representation from the raw bytes. There is no untyped landing spot
// in either, so UseNumber has nothing to change about them; DecimalString in
// particular keeps the server's literal digits verbatim precisely because it
// never goes through a float.
//
// The plain json.Unmarshal sites are safe for the same reason, one level up:
// DecodeJWTClaims above, parseError's envelope (errors.go), AsLineEntryError
// (po_line_entry.go), AsReceivingRefusal (po_receiving.go) and
// StorageSlotErrorDetail (storage_slots.go) each decode into a FULLY TYPED
// struct with no `any` anywhere in it — POLineEntryError's Candidates are
// POLineCandidate, which has none either — so there is no field for a number to
// land in as a float64 and nothing they decode is ever spent as a path segment.
// Give any one of them an `any` and it joins the rule.
func jsonDecoder(r io.Reader) *json.Decoder {
	dec := json.NewDecoder(r)
	dec.UseNumber()
	return dec
}

// anyIDString renders a primary key decoded into an `any` as the decimal string
// a path segment needs, and it lives HERE — beside the decoder that decides what
// such a value actually is — because that is the one fact it has to stay true
// about.
//
// It exists because `fmt.Sprint` over an `any` holding a JSON number formats a
// float64 with %g, so a seven-digit pk becomes "1e+06" and the request 404s.
// AssetPart.IDString's doc comment (asset_parts.go) carries the worked example
// and is what AGENTS.md and po_numeric_id_test.go cite; this is its body, shared
// rather than copied, because AGENTS.md also records that a coercer written
// before UseNumber can keep a DEAD ARM nobody notices — and three hand-copied
// switches is exactly how one copy gets left behind.
//
// Every arm stays. The function's job is to be indifferent to which
// representation the decoder produces, which is what lets it survive a decoder
// change instead of needing a comment per representation.
func anyIDString(id any) string {
	switch v := id.(type) {
	case string:
		return v
	case float64:
		return strconv.FormatInt(int64(v), 10)
	case int:
		return strconv.Itoa(v)
	case int64:
		return strconv.FormatInt(v, 10)
	case json.Number:
		return v.String()
	case nil:
		return ""
	default:
		return fmt.Sprintf("%v", v)
	}
}

// decodeBody is the ONE place a RESPONSE body becomes Go values: it reads a
// jsonDecoder so the whole reply, nested `any` ids included, is decoded on the
// package's own terms, and it wraps the failure in the sentence the operator
// reads on the pane.
func decodeBody(r io.Reader, out any) error {
	if err := jsonDecoder(r).Decode(out); err != nil && err != io.EOF {
		return fmt.Errorf("oms: decode response: %w", err)
	}
	return nil
}

func (c *Client) doMultipart(ctx context.Context, method, path string, fields map[string][]string, files []MultipartFile, out any, retryAuth bool) error {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	// Deterministic field order so the encoded body is stable across runs
	// (keeps the multipart round-trip tests deterministic).
	keys := make([]string, 0, len(fields))
	for k := range fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		for _, v := range fields[k] {
			if err := mw.WriteField(k, v); err != nil {
				return fmt.Errorf("oms: multipart field %s: %w", k, err)
			}
		}
	}
	for _, f := range files {
		part, err := mw.CreateFormFile(f.Field, f.Filename)
		if err != nil {
			return fmt.Errorf("oms: multipart file %s: %w", f.Field, err)
		}
		if _, err := part.Write(f.Data); err != nil {
			return fmt.Errorf("oms: multipart write %s: %w", f.Field, err)
		}
	}
	if err := mw.Close(); err != nil {
		return fmt.Errorf("oms: close multipart: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, bytes.NewReader(buf.Bytes()))
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
			return c.doMultipart(ctx, method, path, fields, files, out, false)
		}
	}

	if resp.StatusCode >= 400 {
		return parseError(resp)
	}

	if out == nil || resp.StatusCode == http.StatusNoContent {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
	if err := decodeBody(resp.Body, out); err != nil {
		return err
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

// UnmarshalJSON tries the bare array first and falls back to the envelope, and
// BOTH branches decode through jsonDecoder rather than json.Unmarshal.
//
// encoding/json hands a custom Unmarshaler the raw bytes and steps out of the
// way, so an outer decoder's UseNumber does not reach in here — this method is
// the boundary the option was escaping through. json.Unmarshal cannot be
// configured at all, so an `any` id inside T landed as a float64 and %v rendered
// it with %g: ListPendingReorders returns MaybeList[ReorderRequest].Items, and a
// seven-digit request pk reached ApproveReorderRequest as "1e+06", posting to a
// request that does not exist. jsonDecoder's own comment carries the reasoning;
// what matters at this site is that a fallback chain is exactly where a decoding
// decision gets quietly re-made, so neither branch may make its own.
func (m *MaybeList[T]) UnmarshalJSON(data []byte) error {
	var arr []T
	if err := jsonDecoder(bytes.NewReader(data)).Decode(&arr); err == nil {
		m.Items = arr
		m.Count = len(arr)
		return nil
	}
	var env Page[T]
	if err := jsonDecoder(bytes.NewReader(data)).Decode(&env); err != nil {
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
