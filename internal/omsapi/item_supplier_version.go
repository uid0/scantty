package omsapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// A supplier link (ItemSupplier) carries an optimistic-concurrency token, and
// this file is the client's half of it. OpenMakerSuite's
// `backend/inventory/services/link_version.py` is the authority and
// `docs/API_ERROR_CONTRACT.md` lists the refusal; both arrived in OMS #1091.
//
// THE DEFECT IT CLOSES. Two people open the same link. One saves a new lead
// time of 12; the other saves a copy still holding the 7 it loaded, and the 12
// is gone with no word to either of them. Every field of the row behaves that
// way, not only the lead time, and a terminal edit is one of the two people.
//
// THE CONTRACT, measured against a running OMS (testdata/README.md records the
// session):
//
//   - Every representation carries `version`, an INTEGER that every write to the
//     row moves on by one — including writes that change nothing a client draws:
//     the measuring task, and a promotion DEMOTING a sibling primary.
//     `supplier_link_stale_version_set_primary.json` is that second case: the
//     copy went stale because a different link was made primary.
//   - A PATCH/PUT that sends `"version": <loaded>` is refused if the row has moved
//     on; so is `DELETE …/?version=<loaded>` (a query value, because a DELETE
//     carries no body). The refusal is the standardized envelope at 409 with
//     `error.code == "stale_version"`, a sentence for the person in
//     `error.message`, and `details` carrying `id`, `sent_version` and
//     `current_version` — which is JSON null once the link has been DELETED, and
//     the sentence changes with it. NOTHING from a refused write is saved.
//   - A write WITHOUT `version` is never refused this way — it is still
//     last-write-wins — and an OMS before #1091 ignores the key. So sending it is
//     harmless everywhere and checked only where the server knows it.
//
// WHAT A CLIENT MUST NEVER DO WITH A REFUSAL is retry, and above all never
// re-send with `current_version`: that is precisely the silent overwrite the
// token exists to stop, laundered through a second request. The refusal is for
// a PERSON, who reloads, sees what changed, and makes their change again. So
// StaleSupplierLink deliberately exposes CurrentVersion only as a fact ("was the
// link deleted?") and nothing on the write path accepts one read back from it.
//
// Recognised by CODE, never by wording (the server's own instruction), and only
// on a 409: the envelope's `code` alone would also match a future endpoint that
// reused the word at another status.

// StaleSupplierCode is OMS's `error.code` for a supplier-link write made from a
// stale copy. Stable: clients switch on it.
const StaleSupplierCode = "stale_version"

// StaleSupplierLink is a refused supplier-link write, decoded.
type StaleSupplierLink struct {
	// Message is the server's sentence for the person, verbatim. It names what
	// happened and the remedy, and differs between a link that was CHANGED and
	// one that was DELETED.
	Message string
	// ID is the link the refusal is about. A pointer because the contract
	// allows null — on a kit's supplier terms, whose deleted link id is
	// unknowable; never on the item-suppliers routes this client drives.
	ID *int
	// SentVersion is the token the refused write carried.
	SentVersion int
	// CurrentVersion is where the row stands now, or nil once it is DELETED.
	// It is a FACT for the screen to word its remedy on, never a token to send.
	CurrentVersion *int
}

// Deleted reports whether the link no longer exists, which changes the remedy:
// there is no current copy of it to reload into a form, only the item's list.
func (r *StaleSupplierLink) Deleted() bool { return r.CurrentVersion == nil }

// staleSupplierFallback is the sentence used if a refusal ever arrives with a
// blank message. The contract promises one, so this is a floor under a server
// bug rather than a second copy of OMS's wording: it says the two things an
// operator cannot act without — nothing was saved, and a reload is the way on.
const staleSupplierFallback = "Someone else changed this supplier link after you loaded it, " +
	"so your changes were not saved. Reload to see the current values."

// AsStaleSupplierLink recovers a stale-version refusal from an error returned
// by UpdateItemSupplier, SetItemSupplierPrimary or DeleteItemSupplier, and
// reports false for anything else — including a coded envelope at another
// status, and a 409 of any other code.
func AsStaleSupplierLink(err error) (*StaleSupplierLink, bool) {
	var api *APIError
	if !errors.As(err, &api) {
		return nil, false
	}
	if api.Status != http.StatusConflict || api.Code != StaleSupplierCode {
		return nil, false
	}
	out := &StaleSupplierLink{Message: strings.TrimSpace(api.Message)}
	if out.Message == "" {
		out.Message = staleSupplierFallback
	}
	// json.Unmarshal rather than jsonDecoder is safe here for the reason
	// client.go's jsonDecoder doc gives for the other envelope decoders: the
	// target is FULLY TYPED with no `any` in it, so a number has no untyped
	// landing spot and nothing decoded is spent as a path segment. A details
	// payload of another shape leaves the fields zero, which reads as "a
	// refusal whose link state the server did not say" — CurrentVersion nil,
	// so the remedy offered is the list reload, which is right either way.
	if len(api.Details) > 0 {
		var details struct {
			ID             *int `json:"id"`
			SentVersion    int  `json:"sent_version"`
			CurrentVersion *int `json:"current_version"`
		}
		if json.Unmarshal(api.Details, &details) == nil {
			out.ID = details.ID
			out.SentVersion = details.SentVersion
			out.CurrentVersion = details.CurrentVersion
		}
	}
	return out, true
}

// GetItemSupplier reads one link as it stands now (GET …/item-suppliers/{id}/),
// version included. It is the RELOAD a stale refusal offers: the form re-reads
// the link rather than guessing at what changed. A link deleted since answers
// 404, which APIError.IsNotFound reports.
func (c *Client) GetItemSupplier(ctx context.Context, id int) (*ItemSupplier, error) {
	var out ItemSupplier
	if err := c.Get(ctx, fmt.Sprintf("/api/inventory/item-suppliers/%d/", id), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
