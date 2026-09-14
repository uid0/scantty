package scanner

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

type ScanMsg struct {
	Code      string
	Kind      Kind
	Timestamp time.Time
}

// Kind is how a scanned code should be ROUTED, and it is deliberately a
// smaller vocabulary than "what the code might BE".
//
// There is no KindForgeKeyBadge, and its absence is the whole of sc-classify-hex.
// A badge used to be claimed here on a SHAPE — 8-20 hex characters — and routed
// to a branch that could only fail, which is two separate mistakes:
//
//  1. The shape was never a badge's. OMS stores one in a free
//     `CharField(max_length=50)` with NO format validator
//     (`membership.models.User.badge_number`; `from_badge` trims and matches
//     exactly), and the real-world format its own resolver documents is
//     "8-10 digits" DECIMAL (`maker_boxes.services.identity_resolver
//     ._looks_like_badge`). So the hex rule both admitted 11-to-20-character
//     strings no badge is and excluded real values like `BADGE001`, which is
//     what OMS's own fixtures carry.
//  2. Even the CORRECT shape cannot route, because it is not the badge's alone.
//     A badge is 8-10 digits and a UPC-E/EAN-8 is 8 digits — the same string.
//     No classifier can separate them, so whichever branch claims the shape
//     takes the other's codes with it. The hex rule claimed the whole of
//     `scanner/resolvers._UPC_LENGTHS` = {8, 12, 13, 14}: every barcode format
//     OMS's dispatcher knows how to resolve.
//
// Only one side of that collision has a resolver at all. ScanTTY has never
// looked a badge up — the branch returned "badge lookup not yet wired" from the
// initial scaffold onward — and OMS still exposes no member-by-badge READ
// endpoint. Its one badge surface, `/api/forgekey/badge-enrollment/`, is keyed
// by USER rather than by badge, so nothing there answers "whose badge is this?".
// So claiming a code for the badge path could only ever turn a code the
// dispatcher would have resolved into a guaranteed error.
//
// The badge FACT is not lost, only demoted out of routing: MightBeForgeKeyBadge
// reports the shape so a code nothing resolved can say why it might not have,
// which is a sentence rather than a destination.
type Kind int

const (
	KindUnknown Kind = iota
	KindOMSCode
	KindOMSURL
)

type URLTarget struct {
	Kind       string
	ResourceID string
	RawPath    string
}

func Listen(ctx context.Context, src io.Reader, idleFlush time.Duration) tea.Cmd {
	return func() tea.Msg {
		return startedMsg{ctx: ctx, src: src, idleFlush: idleFlush}
	}
}

type startedMsg struct {
	ctx       context.Context
	src       io.Reader
	idleFlush time.Duration
}

func StdinReader() io.Reader { return os.Stdin }

// Classify says where a scanned code should be SENT, and it answers with a
// destination only where the code's shape really names one.
//
// KindUnknown is not a refusal: the scan screen sends it to OMS's dispatcher
// like a KindOMSCode, because the dispatcher resolves far more shapes than this
// function can name — UPC-A, UPC-E, EAN-13, ITF-14, location access codes,
// asset tags, project-storage stints — and it is the side that holds the data
// to settle them. Naming a local destination is therefore worth doing only for
// a code that needs NO round trip (an OMS URL carries its own target), and
// everything else is the server's question to answer. See Kind's doc for the
// badge branch this used to have and why it could only subtract.
func Classify(code string) Kind {
	switch {
	case len(code) == 0:
		return KindUnknown
	case isURL(code):
		return KindOMSURL
	case isOMSCode(code):
		return KindOMSCode
	default:
		return KindUnknown
	}
}

func isURL(code string) bool {
	return strings.HasPrefix(code, "http://") || strings.HasPrefix(code, "https://")
}

func ParseOMSURL(raw string) (*URLTarget, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("scanner: parse url: %w", err)
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) == 0 {
		return nil, fmt.Errorf("scanner: empty url path")
	}
	t := &URLTarget{RawPath: u.Path}
	switch {
	case len(parts) >= 3 && parts[0] == "inventory" && parts[1] == "items":
		t.Kind = "item"
		t.ResourceID = parts[2]
	case len(parts) >= 2 && parts[0] == "scan":
		// The short form every OMS QR generator PRINTS. See scanPathTarget.
		t.Kind, t.ResourceID = scanPathTarget(parts[1:])
	case len(parts) >= 3 && parts[0] == "inventory" && parts[1] == "scan":
		// /inventory/scan/<itemId> or /inventory/scan/asset/<id> etc. — the
		// long form the web redirects the short one to, and the one OMS's own
		// dispatcher parses.
		t.Kind, t.ResourceID = scanPathTarget(parts[2:])
	case len(parts) >= 2 && parts[0] == "assets":
		t.Kind = "asset"
		t.ResourceID = parts[1]
	case len(parts) >= 3 && parts[0] == "inventory" && parts[1] == "locations":
		t.Kind = "location"
		t.ResourceID = parts[2]
	case len(parts) >= 3 && parts[0] == "inventory" && parts[1] == "suppliers":
		t.Kind = "supplier"
		t.ResourceID = parts[2]
	case len(parts) >= 3 && parts[0] == "purchasing" && parts[1] == "orders":
		t.Kind = "purchase_order"
		t.ResourceID = parts[2]
	case len(parts) >= 3 && parts[0] == "maintenance" && parts[1] == "work-orders":
		t.Kind = "work_order"
		t.ResourceID = parts[2]
	case len(parts) >= 3 && parts[0] == "maintenance" && parts[1] == "third-party":
		t.Kind = "third_party_work_order"
		t.ResourceID = parts[2]
	case len(parts) >= 2 && parts[0] == "sigs":
		t.Kind = "sig"
		t.ResourceID = parts[1]
	case len(parts) >= 3 && parts[0] == "facilities" && parts[1] == "forgekey-devices":
		t.Kind = "forgekey_device"
		t.ResourceID = parts[2]
	default:
		t.Kind = "unknown"
		t.ResourceID = strings.Join(parts, "/")
	}
	return t, nil
}

// scanTypeKinds maps the <type> segment of an OMS scan URL to the kind a
// target is routed by. The kinds are the DISPATCHER's `target_type` spellings
// (`scanner/resolvers._parse_scan_url`'s type_map, plus `project_storage_stint`),
// so a printed label and a dispatcher-resolved code for the same record land on
// the same arm of the scan screen rather than on two spellings of one fact —
// `donation-item` in a path is `donation_item` on the wire.
//
// The set is what OMS PRINTS and ROUTES, read at uid0/openmakersuite main:
//
//   - `inventory/utils/qr_generator.py` and `inventory/services/qr_code_service.py`
//     emit `/scan/<item_id>`, `/scan/asset/<id>`, `/scan/location/<id>` and
//     `/scan/project-storage/<stint_id>`;
//   - `donations/services/qr_code_service.py` emits `/scan/donation-item/<id>`;
//   - `maker_boxes/services/label_service.py` emits
//     `/scan/makerbox/<bin_id>/<username>/`;
//   - `frontend/src/App.tsx` routes all of those plus `/scan/fixture/<id>`,
//     and redirects each short form to its `/inventory/scan/...` long form
//     (project storage to `/facilities/project-storage/<stint_id>`).
//
// OMS's own `_parse_scan_url` recognises only the long forms and the
// project-storage short form, so a printed asset or location label sent to the
// dispatcher comes back unknown. That is why these are parsed HERE: the label
// already says what it is, and no round trip is needed to read it back.
var scanTypeKinds = map[string]string{
	"asset":           "asset",
	"location":        "location",
	"fixture":         "fixture",
	"donation-item":   "donation_item",
	"project-storage": "project_storage_stint",
	"makerbox":        "maker_box",
}

// scanPathTarget reads the segments AFTER `scan` in either scan URL form. A
// single segment is a bare inventory item (kind "code", the same as it always
// was for `/inventory/scan/<id>`); two or more are `<type>/<id>`, where a maker
// box label's trailing username is ignored because the bin is what it names. A
// type OMS does not print is "unknown" rather than its raw segment, so nothing
// downstream can mistake a guess for a route.
func scanPathTarget(rest []string) (kind, id string) {
	if len(rest) == 1 {
		return "code", rest[0]
	}
	if k, ok := scanTypeKinds[rest[0]]; ok {
		return k, rest[1]
	}
	return "unknown", strings.Join(rest, "/")
}

func isOMSCode(code string) bool {
	if len(code) != 6 {
		return false
	}
	for _, r := range code {
		switch {
		case r >= '0' && r <= '9':
		case r >= 'A' && r <= 'Z':
		case r >= 'a' && r <= 'z':
		default:
			return false
		}
	}
	return true
}

// badgeMinDigits / badgeMaxDigits are the real-world ForgeKey badge format, and
// they are OMS's own figures rather than ours: `maker_boxes.services
// .identity_resolver._looks_like_badge` says "Real RFID badge numbers in the DMS
// deploy are 8-10 digits", and the Common API that resolves them is called with
// exactly that (`lookup_by_rfid("12345678")`).
//
// The stored column constrains nothing — `User.badge_number` is a
// `CharField(max_length=50)` with no validator, and OMS's own fixtures put
// `BADGE001` and `MQTT-CARD` in it — so this is the shape a READER emits, not
// the set of values the database will hold. That is the right bound for a hint
// and the wrong one for a rule, which is why nothing routes on it.
const (
	badgeMinDigits = 8
	badgeMaxDigits = 10
)

// MightBeForgeKeyBadge reports whether code has the SHAPE of a ForgeKey access
// badge. It is ADVISORY and must never decide where a code is sent.
//
// The shape is not exclusive: a UPC-E and an EAN-8 are eight digits too, so at
// that length a badge and a barcode are indistinguishable strings and only the
// side holding the data can tell them apart. What this is for is the sentence
// after a lookup has already come back with nothing — a code nobody resolved
// that looks like a badge probably IS one, and saying so is better than a bare
// "no match" for an operator holding a card ScanTTY cannot yet look up.
//
// Use it to EXPLAIN a miss, never to pre-empt one.
func MightBeForgeKeyBadge(code string) bool {
	if len(code) < badgeMinDigits || len(code) > badgeMaxDigits {
		return false
	}
	for _, r := range code {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func ReadOne(r io.Reader) (string, error) {
	br := bufio.NewReader(r)
	line, err := br.ReadString('\n')
	if err != nil && err != io.EOF {
		return "", err
	}
	for len(line) > 0 && (line[len(line)-1] == '\n' || line[len(line)-1] == '\r') {
		line = line[:len(line)-1]
	}
	return line, nil
}
