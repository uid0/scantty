// Taking a machine out of service, and putting it back.
//
// Four POST actions on the asset viewset — `lock`, `unlock`, `disable`,
// `enable` — and they are TWO INDEPENDENT AXES rather than four points on one
// scale. Getting that wrong at a bench is the difference between a machine
// somebody cannot start and a machine that merely stopped appearing in a list.
//
// LOCK IS THE ONE THAT STOPS THE MACHINE. It creates a `forgekey.DeviceLockout`
// row (actor, reason, hierarchical level) and sets the asset's `OperationalMode`
// to `locked_out`. `forgekey.services.access_control.is_authorized` — whose own
// docstring calls itself "the single source of truth for 'can this user use this
// asset right now'" — returns False while any active lockout exists and while
// the mode is `locked_out`, so the badge reader denies and the relay stays cut.
// A `post_save` on DeviceLockout also re-syncs the asset's indicator bindings,
// so the light or panel at the machine shows locked-out.
//
// DISABLE DOES NOT STOP ANYTHING. It flips `Asset.is_active`, whose help_text is
// "Inactive assets are hidden from most views", and `is_authorized` DOES NOT
// READ IT — verified by reading the function, which consults the
// AssetAuthorization, the lockouts and the mode and nothing else. So a disabled
// asset with a valid authorization and no lockout is still usable by a badge
// holder. Disable is the catalogue switch; lock is the interlock.
//
// The two do not touch each other, measured rather than assumed (below):
// locking left `is_active` true, disabling left `is_locked` true, and unlocking
// left a disabled asset disabled.
//
// MEASURED CONTRACT — against a real OMS on PostgreSQL, 2026-09-12, remote
// `main` commit a1f8c6e8 (tree 855f6f5a). The recorded bodies are in
// testdata/asset_{lock,unlock,disable,enable}*.json with their provenance in
// testdata/README.md.
//
//	POST …/assets/{id}/lock/    {"reason": "…"}  -> 201 + the whole asset
//	POST …/assets/{id}/unlock/  no body          -> 200 + the whole asset
//	POST …/assets/{id}/disable/ no body          -> 200 + the whole asset
//	POST …/assets/{id}/enable/  no body          -> 200 + the whole asset
//
// EVERY ONE ANSWERS WITH THE FULL ASSET SERIALIZER, which is why each method
// here returns `*Asset` rather than an error alone: the reply IS the state after
// the write, and on this domain a caller must report that state rather than
// predict it. Three measured reasons it cannot be predicted:
//
//   - LOCK STACKS. It is not idempotent and does not refuse a second lockout:
//     two locks on one asset returned 201 twice and left two active rows. That
//     is deliberate (DeviceLockout's own docstring: "Lockouts can be stacked —
//     higher level lockouts can only be unlocked by higher level users"), and it
//     is how physical lockout/tagout is supposed to work, so nothing on this
//     side may treat an already-locked asset as un-lockable.
//   - AN UNLOCK THAT RETURNS 200 CAN LEAVE THE ASSET LOCKED. The view clears ONE
//     lockout — the first the caller is entitled to clear — and only drops the
//     mode back to `available` when none remain. Measured with two stacked
//     lockouts: 200, and `is_locked` still true; a second unlock cleared it.
//     testdata/asset_unlock_still_locked.json is that reply.
//   - THE ASSET PAYLOAD CANNOT SAY HOW MANY THERE ARE. `lockout_info` is built
//     from `DeviceLockout.objects.filter(asset=…, is_active=True).first()`, so it
//     names ONE of possibly several and carries no count. The stack is listable
//     at `GET /api/forgekey/lockouts/?asset=<id>&is_active=true` (verified: it
//     filters and returns both rows), which is a different service in ScanTTY's
//     wiring — so nothing here reads it, and the honest answer is to report the
//     state the write came back with.
//
// `can_unlock` AND `can_enable` ARE ADVISORY AND ARE NOT WHAT THESE ENDPOINTS
// ENFORCE. Both serializer methods return False for a `report_only` asset, and
// measured on one: lock returned 201 and disable returned 200 anyway. So a
// client that gated a key on either flag would refuse what the server accepts —
// the defect class AGENTS.md records under the kit serialized-component ban. The
// flags are worth SHOWING (they are the server's own read of the caller's
// standing) and must never be a gate; `internal/tui/asset_interlock.go` shows
// them as facts and sends every write.
//
// This is the opposite of `can_delete_items` on a purchase order, which IS
// served from the frozenset the server enforces on and therefore MUST be read.
// The difference is whether the flag and the enforcement come from the same
// expression, and here they do not.
//
// THE REFUSALS COME IN TWO SHAPES and both are already recognised in this
// package, so nothing new parses anything:
//
//	{"error": {"code": "validation_failed", "message": "reason is required"}}
//	{"error": {"code": "validation_failed", "message": "Asset is not locked"}}
//	{"error": "You do not have permission to unlock this asset"}     (403)
//	{"error": "You do not have permission to enable this asset"}      (403)
//
// The first two are OMS's standardized envelope, which `parseError` understands,
// so they arrive with `APIError.Code` set and `AsLineEntryError` recovers them.
// The last two are hand-built `Response({"error": "<prose>"}, …)` bodies in the
// view, which defeat `parseError` (its `error` is a string where the envelope has
// an object), so the whole payload sits in `.Message` and `AsReceivingRefusal` is
// the narrow recogniser for it. `internal/tui/asset_interlock.go`'s
// `interlockRefusal` is the one combiner.
//
// THE PERMISSION GATES ARE ASYMMETRIC, and the asymmetry is the server's:
// `enable` checks `groups_can_enable` (403, measured) and `disable` does not, so
// any authenticated user may disable an active asset. Nothing here evens that
// out.
//
// A NOTE ON THE WEB, because it explains why this is not a transcription of it:
// `assetsAPI.lockAsset` (frontend/src/services/api.ts) posts NO BODY, and the
// server requires `reason` — so the web's Lock button answers 400 "reason is
// required" every time, measured. Parity here is with the API's contract rather
// than with that caller.
//
// EVERY ID IS A UUID AND THEREFORE A STRING ON THE WIRE. `Asset` declares
// `id = models.UUIDField(primary_key=True, …)`, read from the whole model class,
// and these replies are the ordinary `AssetSerializer` rather than a hand-built
// dict — so there is no untyped landing spot for a number here and nothing is
// spent through `anyIDString` (client.go's jsonDecoder doc owns that rule).
package omsapi

import (
	"context"
	"fmt"
)

// assetLockRequest is lock's only field, and it is sent ALWAYS rather than
// omitted when blank.
//
// The server reads `request.data.get("reason", "")` and refuses a falsy one, so
// an absent key and an empty one are the same refusal — there is no "leave the
// stored value alone" reading to protect here, unlike mark_ordered's optional
// keys on a reorder request. Sending it unconditionally keeps the request one
// shape, and the caller is what declines a blank before it gets here.
type assetLockRequest struct {
	Reason string `json:"reason"`
}

// LockAsset puts an active lockout on the asset, which is what actually stops it
// being used. The reason is REQUIRED by the server and is stored on the lockout
// row, where it is the sentence the next person to walk up reads.
//
// It APPENDS: locking an already-locked asset adds a second lockout rather than
// refusing, and both have to be cleared before the machine runs again.
func (c *Client) LockAsset(ctx context.Context, id, reason string) (*Asset, error) {
	var out Asset
	path := fmt.Sprintf("/api/inventory/assets/%s/lock/", id)
	if err := c.Post(ctx, path, assetLockRequest{Reason: reason}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// UnlockAsset clears ONE active lockout — the first the caller is entitled to
// clear by the hierarchy in DeviceLockout.can_be_unlocked_by — and drops the
// operational mode back to available only when none remain.
//
// So the returned asset's IsLocked is the answer to "is the machine usable
// again", and a 200 with IsLocked still true is an ordinary outcome rather than
// a contradiction. Callers must read it.
func (c *Client) UnlockAsset(ctx context.Context, id string) (*Asset, error) {
	var out Asset
	path := fmt.Sprintf("/api/inventory/assets/%s/unlock/", id)
	if err := c.Post(ctx, path, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DisableAsset flips is_active false: the asset is hidden from most views. It
// does NOT stop the machine being used — see this file's header.
//
// The server applies no group gate here, so any authenticated caller may do it.
func (c *Client) DisableAsset(ctx context.Context, id string) (*Asset, error) {
	var out Asset
	path := fmt.Sprintf("/api/inventory/assets/%s/disable/", id)
	if err := c.Post(ctx, path, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// EnableAsset flips is_active true. It leaves any lockout in place, so an
// enabled asset can still be locked.
//
// Unlike disable, this one IS gated: the server checks `groups_can_enable` and
// answers 403 with its own sentence when the caller is in none of them.
//
// It is a separate method from DisableAsset rather than one call with a flag
// precisely because of that asymmetry — two endpoints with two permission
// stories, which a single SetAssetActive(bool) would hide at the call site.
func (c *Client) EnableAsset(ctx context.Context, id string) (*Asset, error) {
	var out Asset
	path := fmt.Sprintf("/api/inventory/assets/%s/enable/", id)
	if err := c.Post(ctx, path, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
