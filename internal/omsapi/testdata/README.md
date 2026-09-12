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

| file | endpoint | OMS builder | OMS commit |
|---|---|---|---|
| `transparency_signed_in.json` | `GET /api/reorders/analytics/transparency/` with a Bearer token | `reorder_queue.views.AnalyticsViewSet.transparency` | `ff3508b6` (#1057), remote `main`, tree `8c619074` |
| `transparency_anonymous.json` | the same, no `Authorization` header | same | same |
| `transparency_pre1057_signed_in.json` | the same as the first | same | `ca2474e` — #1057's parent |

Recorded 2026-09-10, all three off ONE PostgreSQL database seeded with the same
rows, so the pre- and post-#1057 bodies differ only by the server. The token is
an ordinary non-staff account's (`may_see_vendor_data` is `is_authenticated`).
The rows were chosen to reach the states the transparency tabs have to keep
apart: a paid order (`actual_cost` 1243.7), a donated one (a recorded 0.00 —
published as `0.0` by `ff3508b6` and as `null` by `ca2474e`), and a placed order
with paperwork and NO cost recorded (`null` on both); an item with two
suppliers, one with one, and one with none (`item_supplier_choice.reason`
`no_suppliers`); and one sent purchase order with an estimated total and no
actual. The extra `supplier-N` alternatives are the item factory's own links.

What they pin:

* `ff3508b6`'s order rows carry **no** `supplier_name`, `estimated_cost` or
  `cost_variance` and carry `item_supplier_choice` / `item_estimated_cost_today`
  instead — item-scoped, as of the response.
* The anonymous body **omits** every vendor key and marks each row, the
  purchase orders and the summary `vendor_data_withheld: true`; `null` still
  means "no figure recorded" in the signed-in one.
* `ca2474e` still sends the substituted `supplier_name` and `estimated_cost` on
  each order row: that recording is what proves the screen keeps them off the
  pane against a deployment that has not taken #1057.

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

| file | endpoint | OMS builder | OMS commit |
|---|---|---|---|
| `wo_detail_pending_review.json` | `GET /api/inventory/work-orders/{id}/` | `inventory.serializers.WorkOrderSerializer` (+ `WorkOrderSubmissionSerializer`) | `ff3508b6` (#1057), remote `main`, tree `8c619074` |
| `wo_list_pending_review.json` | `GET /api/inventory/work-orders/?asset={id}` | `inventory.serializers.WorkOrderListSerializer` | same |
| `wo_apply_pending_selective.json` | `POST .../work-orders/{id}/submissions/{sid}/apply-pending/` with `{"target_ids": ["task_…"]}` | `inventory.views.WorkOrderViewSet.apply_pending_changes` | same |
| `wo_apply_pending_confirm_complete.json` | the same, `{"confirm_complete": true}` | same | same |
| `wo_apply_pending_empty_queue.json` | the same, `{}`, on a submission whose queue is already empty | same | same |
| `wo_apply_pending_not_found.json` | the same, against a submission id the work order does not have (**404**) | DRF's own handler | same |
| `wo_discard_pending_all.json` | `POST .../discard-pending/` with `{}` | `WorkOrderViewSet.discard_pending_changes` | same |

Recorded 2026-09-11 against a clean clone of remote `main` on PostgreSQL, one
request per branch the terminal's scan-review surface has to tell apart.

The work order was seeded to reach all of them at once: two REQUIRED task
completions, and TWO parked submissions — one carrying a change of every kind
the two ingest paths produce (an `auto_applied` checkbox at 0.9993, a
low-confidence one at 0.61 whose `value` is **false**, a `signature` and a
`handwritten` note), and one DEGRADED read, parked `pending_review` with an
EMPTY queue and a `parse_error` instead. `pending_review_count` is 2 across
them, which is what makes it visibly a count of SUBMISSIONS rather than of
changes.

What they pin that a hand-written map does not:

* `has_pending_review` is a JSON **bool** and `pending_review_count` a **number**
  on BOTH serializers. drf-spectacular types an un-annotated
  `SerializerMethodField` as `string`, so the schema is not evidence here — the
  builder is (`_pending_review_count`), and AGENTS.md records that artefact
  costing 41 of 50 false candidates once already.
* `pending_changes[].target_id` is **null** on a signature and a handwritten
  note. That is the whole constraint on selective apply: the server filters with
  `change.get("target_id") in target_ids`, so a change with none can be reached
  only by a whole-queue write, and there is no spelling of `target_ids` that
  names it.
* `pending_changes[].value` is a **bool** on a mark and a **string** on a
  handwritten note — one field, two shapes, because the server's is typed
  `object`.
* the work-order LIST body carries **no `title` key at all**;
  `WorkOrderListSerializer` has only `display_title`.
* `apply-pending`'s nothing-to-do reply carries **no counts** — `detail` and
  `submission_status` alone — so a client that inferred a refusal from a zero
  `applied_count` would report one the server never made.
* the 404 is DRF's bare `{"detail": …}` with **no `code`**, which `parseError`
  cannot recognise; `AsSubmissionRefusal` is what keeps that off the operator's
  status row as raw JSON.
