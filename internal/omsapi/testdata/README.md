# Captured OMS responses

Every file here is a **real response body**, recorded verbatim off a running
OpenMakerSuite, and it is the fixture the decode tests are built from.

They exist because a hand-written one does not fail. `POLineLookupOrder.ID` was
declared `string` while `serialize_lookup` writes `"id": purchase_order.pk` —
an integer — so `LookupPurchaseOrderLine` could not decode a single reply and
adding a line by SKU was impossible. Three separate fixtures encoded
`{"id": "po-1"}`, the shape the Go struct assumed rather than the shape the
server sends, so every test agreed with the bug:

  * `TestLookupPurchaseOrderLine_DecodesTheWholeAnswer` (this package)
  * `poAddFake.handler` (internal/tui/po_add_line_test.go) — the fake behind the
    whole add-line drive suite
  * `poAddLookupFixture` (internal/tui/jde_pane_fit_test.go)

A fixture written from the struct can only ever confirm the struct. A fixture
recorded from the server can contradict it, which is the entire point.

## Provenance

| file | endpoint | OMS builder |
|---|---|---|
| `po_item_lookup.json` | `GET /api/reorders/purchase-orders/{id}/item-lookup/?q=Hex%20bolt` | `reorder_queue.services.line_entry.serialize_lookup` |
| `po_item_lookup_resolving.json` | `GET /api/reorders/purchase-orders/{id}/item-lookup/?q=HEX-M8-40` | same |

Recorded 2026-09-07 against OpenMakerSuite `main` at commit
`c04613146773376844b7efff798a5c4e615657fc` (tree `ec91e923…`), a clean clone of
the remote default branch, running on PostgreSQL.

`po_item_lookup.json` was seeded to reach the branches a happy path does not:
two candidates (so `resolves` is false and the operator must choose), one
`already_on_order` (the repeat-add numbers), and one `unavailable` entry
carrying the server's own discontinued sentence.

## What these pin that a hand-written map does not

* `purchase_order.id` is a **number** — `PurchaseOrder` declares no primary key,
  so it is `settings.DEFAULT_AUTO_FIELD` (`BigAutoField`).
* `supplier.id` is a **number**, for the same reason.
* `candidates[].item.id` and `unavailable[].item.id` are **strings** — the
  builder wraps them in `str()`, and `InventoryItem` has a `UUIDField` pk.
* `already_on_order.line_item` is a **string** even though `PurchaseOrderItem`
  has an integer pk, because that one **is** `str()`-wrapped at the builder.
  The rule is the builder's, not the model's — which is exactly why reading the
  model is not enough and these files are recorded rather than reasoned out.
* `match_kind` values are the real tiers (`partial_item_name`), not invented
  ones.

## Refreshing

Re-record against a real backend; do not hand-edit. Editing a value to make a
test pass puts the fixture back under the control of the code it is meant to
check.
