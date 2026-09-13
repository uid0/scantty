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

| file | endpoint | OMS builder |
|---|---|---|
| `asset_meters_list.json` | `GET /api/inventory/asset-meters/?asset=<id>` | `AssetMeterSerializer` |
| `asset_meter_readings.json` | `GET /api/inventory/asset-meter-readings/?meter=<id>` | `AssetMeterReadingSerializer` |
| `asset_meter_record_reading.json` | `POST /api/inventory/asset-meters/<id>/record-reading/` | `AssetMeterViewSet._apply_and_respond` |
| `asset_meter_adjust.json` | `POST /api/inventory/asset-meters/<id>/adjust/` | the same |
| `asset_documents_list.json` | `GET /api/inventory/asset-documents/?asset=<id>` | `AssetDocumentSerializer` |
| `asset_document_upload.json` | `POST /api/inventory/asset-documents/` (multipart) | the same |
| `asset_document_supersede.json` | `POST /api/inventory/asset-documents/<id>/supersede/` (multipart) | `AssetDocumentViewSet.supersede` |

Recorded 2026-09-11 against OpenMakerSuite `main` at commit
`1f6897d8d31e406afb015963f3e0a6c94189c4a1` (tree `2d9c8f9c…`), a clean clone of
the remote default branch, running on PostgreSQL and authenticated as a staff
user (`AssetMeterViewSet` is `IsStaffOrSigAdmin` for LIST as well as its writes).

The rows were seeded to reach the states the terminal has to keep apart, because
a library of one happy meter proves nothing about the markers: one meter whose
current value is an ESTIMATE, one that is INACTIVE, one on the `auto_session`
rollup with a watermark set, and one manual runtime meter whose ledger holds a
measurement, a second measurement, and a `manual_adjust` CORRECTION that moved
the meter DOWN — the negative delta is the row the whole record-reading /
adjust distinction exists for. The document list holds a superseded v1 beside
its current v2, so `supersedes` is reached both as `null` and as a string.

What they pin:

* Every `id` is a **string** — `AssetMeter`, `AssetMeterReading`, `Asset` and
  `AssetDocument` each declare `id = models.UUIDField(primary_key=True, …)`, read
  off the whole model class rather than a grep window, and each payload is a
  plain `ModelSerializer` rather than a hand-built dict. Nothing here is decoded
  into an `any` or spent through `anyIDString`.
* `current_value`, `delta` and `value_after` are **strings** with four decimal
  places (`numeric(14,4)`), so a meter reading never goes through a float. A
  NEGATIVE delta is present, with its sign.
* `record-reading` and `adjust` return the same `{meter, reading}` envelope with
  a **201**, and the readings differ only in `source` (`manual` vs
  `manual_adjust`) and in `notes` carrying the adjustment's reason.
* `recorded_by` is **null** on an automatic rollup, which is a different fact
  from a named recorder — hence the pointer.
* A document's `file` comes back as an **absolute URL**, not the relative storage
  path the `FileField` holds, because the serializer has a request in context.

### What the server does NOT refuse, measured on the same backend

`apply_reading` stores whatever arrives. A reading that goes BACKWARDS
(absolute 120 against a meter reading 1250.5), one off by an ORDER OF MAGNITUDE
(12505000), and a NEGATIVE cumulative total (-5) are each accepted with a 201.
And for a `runtime_hours` meter the damage is one-way: a positive advance is
added to `Asset.hours_used` and a downward correction deliberately does not
decrement it, so the sequence above left `hours_used` at 12506130 with nothing in
this API able to bring it back. `internal/tui/asset_meters.go` confirms such an
entry on the terminal side for that reason; `internal/omsapi/asset_meters.go`
carries the note, and `internal/omsapi/asset_meters_lab_test.go` (build tag
`omslab`) RE-MEASURES it against a live backend, so if OMS ever starts refusing
these the confirm's premise fails loudly rather than quietly becoming
decoration.

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

## Asset interlock — lock / unlock / disable / enable

| file | request | status | OMS builder |
|---|---|---|---|
| `asset_lock.json` | `POST /api/inventory/assets/{id}/lock/` `{"reason": "spindle bearing seized — do not run until the bearing is replaced"}` as a **Maintainer** | **201** | `inventory.views.AssetViewSet.lock` → `AssetSerializer` |
| `asset_lock_no_reason.json` | the same with **no body** — what the web's `lockAsset` posts | **400** | `config.api_errors.error_response(VALIDATION_FAILED)` |
| `asset_unlock_still_locked.json` | `POST …/unlock/` as a Maintainer with **two lockouts stacked** | **200** | `AssetViewSet.unlock` |
| `asset_unlock.json` | `POST …/unlock/` clearing the **last** lockout, on an asset that had been disabled meanwhile | **200** | same |
| `asset_unlock_not_locked.json` | `POST …/unlock/` on an asset with no active lockout | **400** | `error_response(VALIDATION_FAILED)` |
| `asset_unlock_forbidden.json` | `POST …/unlock/` as a **plain user** against a **Maintainer** lockout | **403** | a hand-built `Response({"error": "<prose>"})` in the view body |
| `asset_disable.json` | `POST …/disable/` on an asset that was **locked** at the time | **200** | `AssetViewSet.disable` |
| `asset_enable.json` | `POST …/enable/` | **200** | `AssetViewSet.enable` |

Recorded 2026-09-12 against OpenMakerSuite remote `main` at commit
`a1f8c6e8bc3eb709a049ec21dd68e8cabc220060` (tree `855f6f5a…`), a clean clone of
the remote default branch, running on PostgreSQL. One database throughout, with
three accounts at three different lockout levels (`coo` via superuser,
`maintainer` via the `Maintainer` group, `user`) so the hierarchy in
`DeviceLockout.can_be_unlocked_by` is what produced the 403 rather than a
contrived body.

What they pin that a hand-written map does not:

* **The two axes are independent, in all three directions.** `asset_lock.json`
  is `is_locked: true` with `is_active: **true**`; `asset_disable.json` is
  `is_active: false` with `is_locked: **true**`; `asset_unlock.json` is
  `is_locked: false` with `is_active: **false**`. So neither action touches the
  other's flag, and a screen presenting them as one "out of service" state would
  be describing an asset the server cannot be in.
* **A 200 on unlock can still be locked.** `asset_unlock_still_locked.json` is a
  success reply whose `is_locked` is `true` and whose `operational_mode.mode` is
  still `locked_out`, because the view clears ONE lockout of a stack. Nothing on
  the client side can predict that, which is why all four methods return the
  asset and `internal/tui/asset_interlock.go` reports the state that came back.
* **Two refusal SHAPES from one pair of endpoints.** The validation refusals are
  OMS's standardized envelope (`error.code` + `error.message`, which `parseError`
  understands), and the permission refusals are the flat
  `{"error": "<prose>"}` written by hand in the view, which defeats `parseError`
  entirely. One recogniser could not read both, so `interlockRefusal` uses
  `AsLineEntryError` for the first and `AsReceivingRefusal` for the second.
* **`lockout_info` names ONE lockout and carries no count.** It is
  `…filter(asset=…, is_active=True).first()`, so `asset_unlock_still_locked.json`
  names the *remaining* lockout and says nothing about how many are left. The
  stack is only countable at `GET /api/forgekey/lockouts/?asset=<id>&is_active=true`.
* **`lock` answers 201, not 200** — it creates a row — while the other three
  answer 200.

## Reorder create — the three outcomes of `POST /api/reorders/requests/`

| file | request | status | OMS commit |
|---|---|---|---|
| `reorder_create_pre_rule.json` | the SECOND anonymous scan of one item | **201** | `9b3a5f09`, remote `main` (tree `77790f74…`) |
| `reorder_create_filed.json` | the FIRST anonymous scan of one item | **201** | `fc56bcb8`, `fm/oms-anonymous-reorder-duplicate-filing` (tree `e19730ad…`) |
| `reorder_create_already_requested.json` | the SECOND anonymous scan of the same item | **200** | same |
| `reorder_create_validation_failed.json` | a POST with **no `item`** | **400** | same |

All four recorded 2026-09-12 against a clean clone on PostgreSQL, with NO
`Authorization` header — the duplicate rule applies to anonymous submissions
only, so a recording made with a token could not reach the state at all. One
database throughout, one item (`Blue nitrile gloves (M)`), and the request pk
sequence set to seven digits before recording, deliberately: a `BigAutoField`
decoded the default way renders through `%v` as `1.200041e+06`, and a fixture
whose id is `3` cannot report that.

`reorder_create_pre_rule.json` is served by the commit BEFORE the rule, which is
the file the tolerance rests on. ScanTTY lands ahead of the server change, so
what has to be pinned is not the new shape but the OLD one: a body with no
marker at all, off a server where the second scan really did file a second row.
A fixture written from the Go struct would carry whichever key the struct
declares, so only a recording from before the key existed can prove the client
still reports that filing as a filing.

What they pin that a hand-written map does not:

* **Two of the three outcomes are 2xx.** The duplicate is a **200** with a body,
  not an error, so `Client.do` (which fails only at `>= 400`) hands it to the
  caller as a success. A client reading only the transport reports a request as
  filed that the server did not file — which is exactly what
  `internal/tui/reorder_form.go` did.
* **`already_requested` is a JSON bool on BOTH success bodies**, `false` on the
  201 as well as `true` on the 200, so one field answers "filed or already
  recorded" without a client having to notice 201 against 200.
* **The duplicate body echoes the EXISTING request** — the same `id` as the
  filed one, `status: "pending"` — which is what lets a client name the request
  already on file rather than just deny that one was made.
* **`id` is a NUMBER** (`ReorderRequest` declares no pk, so it is
  `settings.DEFAULT_AUTO_FIELD`), while `item` is a **string** UUID. Both come
  off the same hand-widened dict in `ReorderRequestViewSet.create`, which is the
  builder — the serializer's field list has neither of the two extra keys.
* **The 400 carries no `already_requested` at all**, so "could not tell" cannot
  be read as either success.
* The 200 also carries a member-facing **`detail`** sentence, recorded here and
  deliberately not decoded — `omsapi.ReorderRequestCreated` says why.

## Supplier lead-time provenance — `average_lead_time_source`

| file | request | OMS builder | OMS commit |
|---|---|---|---|
| `lead_time_source_item_suppliers.json` | `GET /api/inventory/item-suppliers/?item_id=<hex bolt>` | `inventory.serializers.ItemSupplierSerializer` | `6b150544` (#1085), remote `main`, tree `67db86bf` |
| `lead_time_source_item_detail.json` | `GET /api/inventory/items/<hex bolt>/` | `InventoryItemSerializer` (flat key + nested `suppliers`) | same |
| `lead_time_source_item_detail_primary_default.json` | the same, for an item whose PRIMARY link took the default | same | same |
| `lead_time_source_item_detail_no_supplier.json` | the same, for an item with no link | same | same |
| `lead_time_source_item_detail_anonymous.json` | the first item, with **no** `Authorization` header | same | same |
| `lead_time_source_supplier_detail.json` | `GET /api/inventory/suppliers/1/` | `SupplierDetailSerializer` (`items`) | same |
| `lead_time_source_item_suppliers_pre1085.json` | as the first row | same | `328af14f` — #1085's parent |
| `lead_time_source_item_detail_pre1085.json` | as the second row | same | same |
| `lead_time_source_supplier_detail_pre1085.json` | as the sixth row | same | same |
| `lead_time_source_sku_only_edit_default.json` | `PATCH /api/inventory/item-suppliers/3/` | `ItemSupplierSerializer.update` | `6b150544` (#1085), remote `main`, tree `67db86bf` |
| `lead_time_source_sku_only_edit_measured.json` | `PATCH /api/inventory/item-suppliers/5/` | same | same |
| `lead_time_source_sku_only_edit_unknown.json` | `PATCH /api/inventory/item-suppliers/1/` | same | same |

Recorded 2026-09-13 off ONE PostgreSQL database, as a superuser. The
`_pre1085` bodies were recorded first, with OMS at #1085's parent; the checkout
was then moved to `main` and migrated (`inventory.0114` backfills every existing
row `unknown`), and the rest recorded against the same rows plus new ones. So
each pair differs only by the server.

The rows were seeded to reach every value the key can take, BY THE PATH OMS
decides each one on, rather than by writing the column:

* `unknown` — the two links (Grainger 7, McMaster-Carr 12) created through the
  API BEFORE the migration.
* `default` — a `POST /api/inventory/item-suppliers/` that **omits**
  `average_lead_time` (Fastenal on the hex bolt; the gloves' primary link).
* `recorded` — the same POST **sending** `7` (Global Industrial) and `14`.
* `measured` — `link.average_lead_time = measured_lead_time(n)` then
  `save(update_fields=["average_lead_time"])` in `manage.py shell`, which is
  the exact assignment `inventory.tasks.update_average_lead_times` makes. The
  task itself was not run: it overwrites EVERY active link on an item from that
  item's received reorders, so it cannot leave a measured link beside the other
  three.

The hex bolt therefore carries a DEFAULTED 7 and a QUOTED 7 side by side, which
is the pair the marker exists to tell apart, and supplier 1 (Grainger) carries
one link of each of the four sources across four items.

The three `sku_only_edit` replies were recorded 2026-09-13 against the same OMS
commit and PostgreSQL database, authenticated as the same superuser. Each PATCH
sent the terminal's edit body with `average_lead_time` omitted and changed only
`supplier_sku`: Fastenal link 3 from `11101234` to `11101234-B`, Uline link 5
from `S-9912` to `S-9912-B`, and Grainger link 1 from `4NUE7` to `4NUE7-B`.
Each response was 200 and retained, respectively, 7/default, 9/measured, and
7/unknown.

The same session also sent only `{"average_lead_time": 7}` to the default and
unknown links. OMS returned 7/default and 7/unknown: its
`decide_lead_time_source` preserves the source when an echoed value equals
storage. The terminal still omits an unchanged edit value because a stale form,
or a fractional hydrated value truncated for its integer box, can differ from
storage and would then be labelled recorded.

What they pin that a hand-written map does not:

* the key is `average_lead_time_source`, a **string** from `unknown`,
  `default`, `recorded`, `measured`, on every `ItemSupplierSerializer` row and
  flat on the item beside `average_lead_time`;
* on the item it mirrors the PRIMARY link, and is JSON **null** beside a null
  lead time when there is no link;
* an anonymous reader gets **neither** key, with `vendor_data_withheld: true` —
  withheld like the value, never served as `null`;
* a server before #1085 serves **no key at all**, which is what the terminal's
  "draw exactly as before" rests on (`omsapi.LeadTimeSource`).
