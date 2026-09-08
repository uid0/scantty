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
	BaseURL   string
	AuthToken string
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
			// Preserve the method + body across redirects. Without this,
			// net/http downgrades a redirected POST/PATCH/DELETE to a GET and
			// drops the body — so a missing trailing slash (DRF 301s the
			// un-slashed path to the slashed route) turns a state-changing
			// action into a silent no-op read. Mirrors omsapi's fix (PR #46)
			// as a belt-and-suspenders net beneath the slashed paths below.
			CheckRedirect: preserveMethodOnRedirect,
		},
		authToken:     opts.AuthToken,
		authTokenFunc: opts.AuthTokenFunc,
	}, nil
}

// maxRedirects bounds redirect-following, matching net/http's own default.
const maxRedirects = 10

// preserveMethodOnRedirect keeps the original method and body when following a
// redirect. net/http's default converts a 301/302/303 on a POST (or other
// non-idempotent method) into a GET with no body — browser behaviour that is
// wrong for an API client: a state-changing call arrives at the server as a
// read and silently does nothing. Re-issuing with the original method + a fresh
// body makes a DRF APPEND_SLASH upgrade transparent. The Authorization/
// Content-Type/Accept headers are re-attached only when the redirect stays on
// the same host, so the bearer token can never leak to a different origin.
func preserveMethodOnRedirect(req *http.Request, via []*http.Request) error {
	if len(via) == 0 {
		return nil
	}
	if len(via) >= maxRedirects {
		return fmt.Errorf("forgekey: stopped after %d redirects", maxRedirects)
	}
	orig := via[0]
	req.Method = orig.Method
	if orig.GetBody != nil {
		body, err := orig.GetBody()
		if err != nil {
			return fmt.Errorf("forgekey: replay body across redirect: %w", err)
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
	if err := jsonDecoder(resp.Body).Decode(out); err != nil && err != io.EOF {
		return fmt.Errorf("forgekey: decode: %w", err)
	}
	return nil
}

// jsonDecoder is the ONE place this package chooses a decoder's options, the
// mirror of omsapi.jsonDecoder and for the same reason.
//
// THESE ENDPOINTS ARE SERVED BY OPENMAKERSUITE'S OWN CODE. `/api/forgekey/...`
// is `backend/forgekey`, an OMS Django app, with OMS models behind it — so this
// client crosses exactly the boundary omsapi crosses and its wire types are
// decided by the same serializers. uid0/ForgeKey is the C++ operating system
// that runs ON the devices; it is firmware, not the HTTP API, and confusing the
// two is what put this package out of scope on the strength of it "being a
// different server" — which is why the class stayed open here for a release
// after it was closed next door.
//
// What is NOT established, and is not needed for any of the above: whether the
// deployed SCANTTY_FORGEKEY_URL host is the same process as SCANTTY_OMS_URL.
// The claim that matters is about whose SERIALIZERS decide these types, and that
// one is checkable by reading the app.
//
// UseNumber is the whole point. Several ids on these payloads are typed `any`
// because the client carries whatever the serializer echoed, and the callers
// turn that back into a URL path segment with fmt. Decoded the default way a
// JSON number lands in an `any` as a float64 and `%v` formats a float64 with
// `%g`, so a seven-digit pk renders "1e+06" and is spent on the wire. Measured
// against a live backend, with the correct id as the control:
//
//	POST /api/forgekey/operational-modes/1000000/enable_classroom_mode/  -> 200
//	POST /api/forgekey/operational-modes/1e+06/enable_classroom_mode/    -> 404
//	POST /api/forgekey/authorizations/1000001/revoke/                    -> 200
//	POST /api/forgekey/authorizations/1.000001e+06/revoke/               -> 404
//
// WHAT BITES IS A CONJUNCTION, and stating it as a list of models is what has
// made every version of this roster wrong. It takes an INTEGER pk AND a
// fmt.Sprint over an `any` to lose digits. Either half alone is harmless:
//
//	integer pk, no `any`   DeviceType is a BigAutoField and IS spent as a path
//	                       segment (device_types.go's get/update/delete), and it
//	                       was never affected — it goes through IntID(), which
//	                       returns an int, and the paths format with %d. An int
//	                       through %d is its digits at any magnitude.
//	`any`, no integer pk   ESP32Device, DeviceLockout, DeviceUsage, EPaperDisplay
//	                       and FirmwareRollout all declare
//	                       `id = models.UUIDField(primary_key=True)`, so the id is
//	                       a STRING on the wire and fmt.Sprint never had anything
//	                       to mangle.
//
// Both halves together is op_modes.go's `c` (OperationalMode) and
// auth_lockout.go's revoke (AssetAuthorization), and those are the two the live
// measurements above were taken against.
//
// THE MODEL SIDE, derived from backend/forgekey/models.py by reading each class
// WHOLE: the `models.Model` subclasses taking Django's implicit BigAutoField are
// AssetAuthorization, AssetDevice, DeviceType, OperationalMode and
// RoomOperationalMode. Everything else is an explicit UUIDField.
//
// THIS IS THE ONE PLACE THAT ROSTER IS WRITTEN DOWN, and AGENTS.md and
// any_id_test.go point here rather than repeating it. They used to repeat it,
// which is how the branch ended up with four copies and corrected them one round
// at a time as each was reported — the test's copy still said "only
// AssetAuthorization and OperationalMode" after the other three had been fixed,
// so a reader deriving scope from it would have reached the retired answer. A
// roster costs nothing to copy and cannot be kept in step by hand.
//
// AND IT HAS BEEN GOT WRONG FOUR TIMES IN ONE BRANCH, ALWAYS BY MATCHING
// ONE SPELLING OF THE THING BEING LOOKED FOR — the lesson AGENTS.md already
// records about retired key chords having two spellings, arrived at again here:
//
//   - grepping `fmt.Sprintf("%v")` missed the `fmt.Sprint(` form;
//   - grepping `fmt.Sprint(` then missed `IntID()`, so DeviceType was left out
//     of the roster entirely and a reader would have concluded it was a UUID;
//   - grepping `^class \w+` for the pk scan swept in `IndicatorStatus` (a plain
//     class) and `LockoutLevel` (a models.TextChoices enum) as though they were
//     tables with primary keys. Neither has a pk at all.
//
// So derive this by asking what a value IS and how it is SPENT, not by grepping
// for the spelling you happen to have in mind. And ScanTTY's OWN comments are
// not evidence about any of it: device_types.go said the pk "decodes as a
// float64", which records what an author believed and was then cited as a fact
// about the wire — the mistake this whole branch is about.
//
// The option is set at the DECODER rather than at each fmt call because the id
// sites are not a list anyone maintains, and because a UUID is a string either
// way: the narrow derivation above decides what the TESTS can prove, not what
// the code has to cover.
func jsonDecoder(r io.Reader) *json.Decoder {
	dec := json.NewDecoder(r)
	dec.UseNumber()
	return dec
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

// UnmarshalJSON tries the bare array first and falls back to the envelope, and
// BOTH branches decode through jsonDecoder rather than json.Unmarshal.
//
// encoding/json hands a custom Unmarshaler the RAW BYTES and steps out of the
// way, so an outer decoder's UseNumber does not reach in here and json.Unmarshal
// cannot be configured at all — this method is the hole the option escapes
// through, and it is on the path of both live sites: ListAuthorizations and
// ListOperationalModes each decode a MaybeList of a struct whose ID is `any`,
// and op_modes.go / auth_lockout.go render that id with fmt.Sprint straight into
// an enable_classroom_mode / revoke URL. Fixing only do() above would have left
// every list-fed id still arriving as a float64.
func (m *MaybeList[T]) UnmarshalJSON(data []byte) error {
	// Try bare array first — the more common shape and the cheaper parse.
	var arr []T
	if err := jsonDecoder(bytes.NewReader(data)).Decode(&arr); err == nil {
		m.Items = arr
		m.Count = len(arr)
		return nil
	}
	var env struct {
		Count   int `json:"count"`
		Results []T `json:"results"`
	}
	if err := jsonDecoder(bytes.NewReader(data)).Decode(&env); err != nil {
		return err
	}
	m.Items = env.Results
	m.Count = env.Count
	return nil
}
