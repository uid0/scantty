package omsapi

// The one place this client spells where a supplier's lead time CAME FROM.
//
// `ItemSupplier.average_lead_time` is NOT NULL with a planning default of 7, so
// a link nobody quoted a wait for stores the same 7 as a supplier who really
// promised a week, and a bare "lead 7d" reads identically for both. OMS PR #1085
// added `average_lead_time_source` beside the value, decided by
// `ItemSupplier.save()` and nothing else
// (`backend/inventory/services/lead_time_source.py`, `LeadTimeSource`). It is
// served, read-only, on every `ItemSupplierSerializer` row — the item-suppliers
// list, an item's nested `suppliers`, a supplier detail's `items` — and flat on
// `InventoryItemSerializer` beside the flat `average_lead_time`, where it
// mirrors the PRIMARY link and is null when the item has none.
//
// It is VENDOR data on the item serializer: an anonymous reader gets neither
// key (the payload's `vendor_data_withheld` marker says so), exactly as for the
// value.
//
// THE KEY IS READ, NEVER ASSUMED — the same stance as LeadTimeYardstick. An OMS
// that predates #1085 serves no key at all; that has not told us the number is
// a quote or a default, it has said nothing, and the surfaces draw the value
// exactly as they did before the marker existed. So "" is "the server said
// nothing", and it is a different fact from `unknown`, which is the server
// SAYING it cannot tell (every row stored before the column existed was
// backfilled with it).
//
// The value NEVER changes meaning with the source. Nothing on this side
// computes with the source; it is information for the person reading the
// number.

// LeadTimeSource is the `average_lead_time_source` key. "" means the payload
// did not carry it.
type LeadTimeSource string

// The four values OMS's `LeadTimeSource` TextChoices define on the default
// branch. A value outside these is provenance this client has no words for,
// which callers must treat as not established rather than as any of them.
const (
	// LeadTimeSourceUnknown: stored before the source was kept. Never promoted
	// to a known source by a write that does not change the number.
	LeadTimeSourceUnknown LeadTimeSource = "unknown"
	// LeadTimeSourceDefault: nobody supplied a number; the column took the
	// planning default.
	LeadTimeSourceDefault LeadTimeSource = "default"
	// LeadTimeSourceRecorded: a caller supplied this number — an operator
	// typing a supplier's quote, or an API client sending one.
	LeadTimeSourceRecorded LeadTimeSource = "recorded"
	// LeadTimeSourceMeasured: computed from completed deliveries by
	// `inventory.tasks.update_average_lead_times`.
	LeadTimeSourceMeasured LeadTimeSource = "measured"
)
