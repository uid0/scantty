# Project agent memory

This file is the project's committed home for project-intrinsic agent knowledge: build, test, release, architecture, and sharp-edge notes that should travel with the code.

## Working against OpenMakerSuite

ScanTTY is a client of the OpenMakerSuite HTTP API and of ForgeKey; `README.md`
has the env vars and the package map.

**"FORGEKEY" NAMES THREE DIFFERENT THINGS, AND THE COLLISION IS WHY A WHOLE
CLASS OF BUG STAYED OPEN IN `internal/forgekeyapi` FOR A RELEASE.** Say which
one you mean:

1. **ForgeKey the SYSTEM** — the device-access half of the makerspace: badge
   readers, locks, e-paper panels. This is `README.md`'s sense when it calls
   ScanTTY a TUI "for OpenMakerSuite and ForgeKey", and it is correct.
2. **The ForgeKey HTTP API** — the `/api/forgekey/...` routes `internal/forgekeyapi`
   drives, reached through `SCANTTY_FORGEKEY_URL`, which has its own base URL and
   mTLS story (`README.md`). ScanTTY very much talks to this.
3. **uid0/ForgeKey the REPO** — a C++ operating system that runs ON the ESP32
   devices. ScanTTY does not talk to THAT: it is firmware, not a server.

**WHAT IS ESTABLISHED, AND WHAT IS NOT.** VERIFIED: OpenMakerSuite's own
`backend/forgekey` Django app serves those exact routes with those exact models,
and OMS runs `forgekey.tasks.*` (firmware builds, rollout advancement,
stale-device sweeps) — so the ForgeKey server side lives in the OMS codebase and
its wire types are decided by OMS serializers. NOT VERIFIED: whether the
production `SCANTTY_FORGEKEY_URL` host is that same deployment or a separate
one. Nothing on this branch establishes that, and the pk derivation was done
against OMS's app. Both halves are stated because the sentence this replaced
over-claimed in the other direction ("ScanTTY does not talk to it", true only of
sense 3 and written as though it were true of all three), and swapping one
over-claim for another is the same defect wearing a new coat.

The operative consequence is the part that IS established: everything below —
the OMS source being authoritative, the local-backend recipe, the wire-type
rule — applies to `forgekeyapi` verbatim, and "ForgeKey is a different server"
is not a reason to scope it out of a sweep. It has been used as one.

- **No SHARED OMS is reachable from a task worktree.** `SCANTTY_OMS_URL` is
  unset and nothing answers on the usual host, so ordinary behaviour is verified
  by driving the real screens through `Root.Update` against a stateful
  `httptest` fake. See `internal/tui/wo_materials_drive_test.go` (the `pump` /
  `key` helpers, reused across drive tests) and
  `internal/tui/po_line_price_test.go`.
- **A LOCAL one can be brought up, and on a ScanTTY/OMS SEAM bug it is worth
  it.** A fake only ever proves ScanTTY's half of the contract, and a fake built
  from ScanTTY's own structs cannot disagree with them at all — which is exactly
  how the `POLineLookupOrder.ID` mismatch below survived three fixtures. See
  "Verifying against a REAL OMS" below.
- **The OMS source is the contract.** A read-only checkout normally sits beside
  this one at `../openmakersuite`; `backend/<app>/serializers.py` and `views.py`
  are authoritative for wire shapes, and `frontend/src/pages/*.tsx` is
  authoritative for what parity means. Read it before guessing at a payload —
  and never modify it from a ScanTTY task. **Check that checkout is at the
  REMOTE default branch before trusting it**: `git rev-parse HEAD^{tree}` there
  against the remote commit's tree sha (`gh-axi api repos/uid0/openmakersuite/commits/main`)
  settles it in one step, and a match means reading the local files IS reading
  remote `main`.
- **Money comes over as strings OR numbers**, hence `omsapi.DecimalString`.
  `Empty()` means "null/unset" and NOT "zero": several OMS money properties
  return a real `0.00` for "no price recorded", so treat zero as an absence
  wherever a price is being carried forward (`internal/tui/po_line_price.go`).
- **`vendor_data_withheld: true` and `null` are different facts.** Omitted vendor
  keys mean the reader was not shown the fact; `null` means no figure was
  recorded. Read the marker rather than inferring either state from a nil. The
  owner is `vendorMoney` in `internal/tui/reorder_reports.go`.
- **A transparency ORDER row has no supplier or estimate of its own.** Do not
  present item-scoped replacements as order facts. `ReorderTransparencyOrder`'s
  doc owns the wire rationale; `internal/tui/reorder_transparency_test.go` owns
  the derived set of transparency surfaces.

### Purchase-order line money has two denominators

A sharp edge worth knowing before touching PO pricing anywhere
(`internal/tui/po_edit.go`, `internal/omsapi/reorders.go`):

    estimated_cost = quantity_ORDERED  × unit_cost_ordered   (derived; 0.00, never null)
    actual_cost    = quantity_RECEIVED × unit_cost_actual    (derived; null until both exist)
    PATCH …/items/{id}/ {"line_cost": X}  ->  unit_cost_actual = X / quantity_ORDERED

So `actual_cost` is not a `line_cost`: feeding one back to the other multiplies a
partially received line's price by `quantity_received / quantity_ordered` on
every trip. `unit_cost_actual` is written ONLY through that endpoint — receiving
never sets it. `internal/tui/po_line_price.go` carries the full note.

### A PO line is ENTERED in cases and STORED in base units

`internal/tui/po_case_entry.go` carries the full note and is the authority; it
is the one conversion every screen that takes a PO line's quantity or price runs.
What is worth knowing before touching any of them:

- **The wire is base units at a per-base-unit price.** `quantity_ordered` is
  always base units "whichever pack size shaped it" and `unit_cost_ordered` is
  per base unit; `order_in_packages` is DERIVED server-side by
  `order_packages_for_line`. Nothing on this side sends it — `cases × qpp`
  ceil-divided by `qpp` is `cases`, so posting it would be a second copy of a
  number the server already gets right; `omsapi`'s
  `TestCreatePurchaseOrder_InventoryLineCaseDerivedCost` pins its absence.
- **The case is the SUPPLIER's**, `ItemSupplier.quantity_per_package`, not the
  item's own packaging chain (`packaging.go`, which is how the shop COUNTS what
  is on the shelf). The column DEFAULTS to 1, so `> 1` means "this vendor ships
  by the case" and 1 leaves a singles line untouched — the same gate the web PO
  form uses.
- **One basis per line, not one row per denominator.** The reported defect was a
  row labelled `Unit cost` taking a case price, and relabelling it alone would
  have left `Quantity` asking for base units beside it. `caseBasis` moves both
  typed rows and both labels together; Ctrl-T flips it, converting both values,
  and base-unit entry stays reachable because a broken case is a legitimate
  order.
- **A prefill is not always a whole number of cases.** OMS rounds a suggestion
  up to a whole supplier package only for items counted in BASE units
  (`views.reorder_data` and `line_entry.default_quantity` both gate on
  `counts_in_packs`), so a pack-counted item can suggest 30 against a case of
  12. `poOpensAtCaseBasis` answers that once: whole cases (or no prefill) opens
  in cases, anything else opens in units. Ctrl-T is then a BAR GATE
  (`poFlipsToCases`) rather than a refusal — the key is not named while it could
  only decline, and the derivation row says so standing, naming the two
  quantities that bring it back (`poUnwholeCaseNote`).
- **The derived SET is the four write endpoints, not the screen that was
  reported.** `CreatePurchaseOrder` (`po_create.go`'s line form, reached from
  the items picker, the reorder queue's single-row enter and Ctrl-E on the
  cart), `AddPurchaseOrderLine` (`po_add_line.go`'s price phase — the
  scan / supplier-SKU path), and the reorder queue's BULK add
  (`reorderCartLine`), which stages a cart line without the form and must still
  carry the case size or the review row and Ctrl-E describe a different order.
  `UpdatePurchaseOrderLineItem`'s cost row is deliberately outside it: it takes
  a TOTAL for the line, which no pack size changes, and its label already says
  so. So is `ReceivePOItems`: receiving records what ARRIVED against a
  server-stated balance that is base units throughout, and a case-only box could
  not express a broken case.
- **`ReorderDataItem` carries `quantity_per_package` and `package_cost`.**
  `po_create_pickers.go` used to say in as many words that it carried neither
  and the struct decoding it had no field for either, so every line staged from
  the reorder queue reached the form as a singles line. A comment asserting what
  the wire does NOT carry is worth checking against
  `backend/reorder_queue/views.py` before trusting it.
- **`package_cost` is the authoritative price and `unit_cost` its rounded
  derivative**, so every entry point prices a case-packed line FROM the case
  price. OMS derives `unit_cost = package_cost / quantity_per_package` rounded
  to two decimals (`backend/inventory/models/core.py`), so sourcing the per-unit
  figure from `unit_cost` feeds that rounding back in: a 10.00 case of 3 comes
  back as 3.33 and three of them are 9.99. The reorder queue's BULK add
  (`space`+`a`) was the last path still on `unit_cost` — it now stages up to
  0.005 more or less per base unit than it used to, scaling with the case size.
- **Money is decimal; do the arithmetic in `big.Rat`, not `float64`.**
  `poUnitCostFrom` / `poCaseCostFrom` / `poUnitCostValue`
  (`internal/tui/po_case_entry.go`) are the ONLY conversions allowed to reach a
  typed box or a payload, quantised at `poUnitCostPlaces` — the four-place
  column `unit_cost_ordered` really stores, so a screen cannot show a price the
  record cannot hold. `poUnitFromCase` / `poCaseFromUnit` are float and are for
  derived PROSE only. A float divide put 0.049999999999999996 in the add-line
  cost box for an item priced 0.05, the row `CharLimit` of 14 cut it to
  `0.049999999999`, and Enter posted that.

### A WIRE TYPE IS THE BUILDER'S DECISION, NEVER THE MODEL'S

`internal/omsapi/po_line_entry.go` carries the worked example and
`internal/omsapi/testdata/README.md` the fixture rule. What is worth knowing
before declaring or changing any field that crosses this boundary:

- **The reported failure.** `POLineLookupOrder.ID` was `string`; `serialize_lookup`
  writes `"id": purchase_order.pk` and `PurchaseOrder` declares no primary key, so
  it is `settings.DEFAULT_AUTO_FIELD` (`BigAutoField`) — a number, always. Every
  reply was refused by the decoder, so adding a PO line by SKU was IMPOSSIBLE and
  the operator read `json: cannot unmarshal number into…`.
- **THE SAME PAYLOAD MIXES BOTH, WHICH IS THE WHOLE LESSON.** In
  `serialize_lookup` the item ids ARE `str()`-wrapped and so is
  `already_on_order.line_item` — whose model pk is an INTEGER — while the order's
  own id is bare. So "what pk does the model have?" does not answer "what type is
  on the wire"; only the line of Python that builds the dict does. `POLineSupplierRef.ID`
  beside it was right for the same reason it could have been wrong: nobody checked
  the builder, it just happened to match.
- **drf-spectacular's schema is authoritative for a serializer field and NOT for a
  `SerializerMethodField`** — an un-annotated one is typed `string` by default, so
  the schema calls `can_receive` a string when it is a bool. 41 of the 50 candidates
  the schema net produced were that artefact. And spectacular cannot see a
  hand-built dict AT ALL, which is where this defect lived and where the next one
  will.
- **AN `any` ID IS NOT A FREE PASS.** It always decodes, and then a caller renders
  it. `encoding/json` puts a JSON number into an `any` as a `float64` and `%v`
  formats that with `%g`, so a seven-digit id becomes `"1e+06"` — spent on the wire
  as a path segment. `omsapi.jsonDecoder` sets `UseNumber` so an `any` keeps the
  server's own digits; that is the ONE place it is decided, because the id sites
  are not a list anyone maintains. `asset_parts.go`'s `IDString` had already
  written that reasoning down and applied it to exactly one struct.
  **A CUSTOM `UnmarshalJSON` IS A HOLE IN THAT "ONE PLACE" UNLESS IT ASKS FOR THE
  DECODER TOO**, which is why the option lives in a decoder FACTORY rather than
  in `decodeBody` alone. `encoding/json` hands a `json.Unmarshaler` the RAW BYTES
  and steps out of the way, so an outer decoder's settings do not reach inside
  one, and `json.Unmarshal` cannot be configured at all. `MaybeList[T]` is such a
  type and is on live read paths: `ListPendingReorders` decodes
  `MaybeList[ReorderRequest]`, whose `ID` is `any`, and
  `internal/tui/reorder_queue.go` spends it with `%v` as the path segment of
  `/api/reorders/requests/<id>/approve/` — so with the option set only on
  `decodeBody` a seven-digit reorder pk approved `1e+06`, the same 404 the
  item-lookup fix had just been measured against. THE RULE IS ABOUT UNTYPED
  VALUES AND NOT ABOUT DECODERS, because `UseNumber` changes exactly one thing —
  the Go type a JSON NUMBER takes where nothing declares one: **nothing that can
  produce an untyped value (`any`, `map[string]any`) may be decoded by a decoder
  that is not `jsonDecoder`.** Two flatter wordings were tried first and both
  were falsified by one grep, which is why it is stated this way: "every decoder
  in the package" is false of five `json.Unmarshal` sites (`DecodeJWTClaims`,
  `parseError`, `AsLineEntryError`, `AsReceivingRefusal`,
  `StorageSlotErrorDetail`), and "every decoder of a RESPONSE PAYLOAD" is still
  false of `DecimalString` and `DateOnly`, which ARE `json.Unmarshaler`s on
  payload types. All seven are safe for one reason: each parses into a FULLY
  TYPED target with no `any` in it — the five decode error envelopes,
  `DecimalString` and `DateOnly` parse a SCALAR into a `string` and a `time.Time`
  and decide their own representation from the raw bytes — so there is no
  untyped landing spot for a number and nothing they produce is spent as a path
  segment. Give any of them an `any` and it joins the rule.
  **THE SAME FIX WAS OWED NEXT DOOR, AND "A DIFFERENT SERVER" IS WHY IT WAS
  NOT.** `internal/forgekeyapi` decoded every response with a bare
  `json.NewDecoder` and had the identical `MaybeList` hole, because ForgeKey was
  recorded as out of scope on the strength of being another system. It is not:
  `/api/forgekey/...` is served by **OpenMakerSuite's own `backend/forgekey`
  Django app** — uid0/ForgeKey is the C++ device operating system, not the HTTP
  API — so it crosses this boundary and is checkable against a real backend like
  everything else. It has its own `jsonDecoder` now.
  **WHAT BITES IS A CONJUNCTION — an INTEGER pk AND a `fmt.Sprint` over an
  `any`** — and writing it down as a list of models is what made every earlier
  version of this roster wrong. Either half alone is harmless: `DeviceType` is a
  `BigAutoField` that IS spent as a path segment and was never affected, because
  it goes through `IntID()` and the paths format with `%d`, and an int through
  `%d` is its digits at any magnitude. The mirror case is a model reached by
  `fmt.Sprint` over an `any` whose pk is an explicit `UUIDField`: the id is a
  STRING on the wire, so there was never anything to mangle, and asserting a
  numeric id for one would be testing a payload the server cannot send.
  Both halves together is `internal/tui/op_modes.go` (`OperationalMode`)
  and `internal/tui/auth_lockout.go` (`AssetAuthorization`) — the ids are
  DECODED in `forgekeyapi` and SPENT in the TUI — the two live sites, measured
  `.../operational-modes/1000000/…` 200 against `.../1e+06/…` 404 with the revoke
  path the same. THE MODEL-BY-MODEL ROSTER LIVES IN ONE PLACE — `jsonDecoder`'s
  doc comment in `internal/forgekeyapi/client.go`, which is where the decision it
  justifies is made — and is deliberately not copied here or into the tests. It
  was carried in four places on this branch, each round corrected only the copy
  it was pointed at, and the last stale copy outlived the others by a round; a
  list repeated is a list that drifts, so read it where it is derived.
  **THAT ROSTER WAS GOT WRONG FOUR TIMES IN ONE BRANCH, ALWAYS BY MATCHING ONE
  SPELLING** — the same lesson `list_nav.go`'s retired chords teach about a
  keystroke having two spellings, arrived at independently here. Grepping
  `fmt.Sprintf("%v")` missed the `fmt.Sprint(` form; grepping `fmt.Sprint(` then
  missed `IntID()`, so `DeviceType` was left out entirely and a reader would have
  concluded it was a UUID; and grepping `^class \w+` for the pk scan swept in
  `IndicatorStatus` (a plain class) and `LockoutLevel` (a `models.TextChoices`
  enum) as though they were tables, when neither has a pk at all. Derive this by
  asking what a value IS and how it is SPENT, never by grepping for the spelling
  you happen to have in mind.
  **DERIVE A PK TYPE BY READING THE WHOLE MODEL CLASS** — `FirmwareRollout`'s
  `id =` line sits 26 lines into its class, so a fixed grep window reads it as an
  integer — and never from ScanTTY's own comments: `device_types.go` said the pk
  "decodes as a float64", which records what an author believed about a decoder
  and was then cited as a fact about the wire.
  **AND A COERCER WRITTEN BEFORE `UseNumber` CAN HAVE A DEAD ARM.** A numeric pk
  is a `json.Number` now and never a `float64`, so a type switch offering only
  `string` and `float64` falls through to its zero answer. There are three `any`
  coercers in the package — `loto.go`'s `anyToInt`, `asset_parts.go`'s `IDString`
  and `inventory.go`'s `Asset.InventoryItemID` — and the last was the one that
  had not been brought along, where the fall-through reads as "this asset has no
  linked inventory item" in the middle of hydrating an edit form. Derive that set
  by grepping for `.(type)` over `any` fields rather than trusting this list.
- **A FIXTURE WRITTEN FROM THE STRUCT CANNOT CONTRADICT THE STRUCT.** All three
  fixtures for this endpoint said `{"id": "po-1"}` and all three passed. Fixtures
  for a wire shape are RECORDED from a real backend (`internal/omsapi/testdata/`,
  with provenance) and guarded by a check that the recorded body still carries the
  server's types, so a later "fix" to the fixture cannot quietly restore the
  defect.

### Verifying against a REAL OMS

Worth ~15 minutes whenever a bug sits on the ScanTTY/OMS seam. No Docker daemon
here; Homebrew PostgreSQL listens on `127.0.0.1:5432`. **sqlite will not work** —
`backend/accounting/checks.py` needs PostgreSQL for hordak, so `quick-start.sh`'s
sqlite default is a dead end.

Clone OMS into a scratch dir (never the shared checkout — `backend/.env` and the
DB would land in someone else's clone), `uv venv` + `uv pip install -r
backend/requirements.txt`, create a database, and write `backend/.env` with
`DEBUG=0`, a `DATABASE_URL`, an empty `SENTRY_DSN`, and **`SECURE_SSL_REDIRECT=0`
plus `SECURE_HSTS_SECONDS=0`** — `DEBUG=0` turns the redirect on and it 301s every
plain-HTTP request. Then `manage.py migrate` and `runserver`. Auth is JWT:
`POST /api/auth/login/` → `access` → `omsapi.New(url, omsapi.WithToken(access, ""))`.

Two endpoints need Redis (`GetResilienceStatus`, `ListProjectStoragePrintQueue`)
and 500 without it; nothing else does.

`internal/omsapi/lab_sweep_test.go` (build tag `omslab`) is the harness: it
reflects over `*Client`, drives every read method taking only a context, and
reports which calls carried ROWS and which came back EMPTY — because an empty
list decodes into any element type and proves nothing about it. Use
`go test -count=1`; the cache keys on env vars and will replay a stale PASS.

### A line goes onto a draft order by scanning an identifier

`internal/omsapi/po_line_entry.go` carries the API note and
`internal/tui/po_add_line.go` is the flow; both are the authority. What is worth
knowing before touching either:

- Two endpoints, both DRIVEN and neither re-implemented:
  `GET …/purchase-orders/{id}/item-lookup/?q=` is a pure read that resolves one
  identifier in the context of THAT order's supplier, and
  `POST …/purchase-orders/{id}/items/` is the write. The match ladder, the
  ambiguity set and every refusal (not a draft, supplier does not carry it,
  discontinued) come off the wire.
- **`resolves` is the server's answer, not a count.** It means "the strongest
  tier that matched holds exactly one candidate", which is neither
  `len(candidates) == 1` nor `candidates[0].is_exact`: an exact barcode
  routinely comes back beside partial-name matches, so recomputing it
  client-side disagrees with the server on the ordinary case and would send a
  scan through a choice list it does not need.
- **The add posts `item_supplier`, never `identifier`.** Re-posting the
  identifier RE-RESOLVES it, and the catalogue can change between the lookup and
  the operator's confirm — they would have approved one item and added another.
- **The refusal body is NOT the standard envelope.** `add_item` writes
  `{"error": "<prose>", "code": "<code>"}` by hand, so it never reaches OMS's
  DRF exception handler and `omsapi.parseError` puts the ENTIRE raw body into
  `APIError.Message`. `omsapi.AsLineEntryError` recovers the sentence and the
  code — without it the operator reads the JSON — and it is deliberately narrow,
  so a gateway page and a DRF validation envelope keep the shape they arrived in.
- **The quantity and price defaults differ between a fresh line and a repeat,
  because the SERVER's do.** A fresh line lands on `suggested_quantity` /
  `suggested_unit_cost`; a repeat GROWS the line already there by
  `repeat_increment` and leaves its price alone unless a price is sent.
  Prefilling the fresh-line price on a repeat and posting it reprices the whole
  line. Blank means "the server decides" on both rows, which is why the prompt
  can offer the defaults and still be safe.
- `suggested_unit_cost` of `0.00` is a real answer — no price on the supplier
  relationship AND no purchase history — rather than an absence. It is still
  what the prompt shows, because it is still what the server would apply, with
  the fact named beside it: a zero accepted by reflex is a zero-priced line.

### A line comes OFF an order two ways, and the SERVER says which

`internal/tui/po_edit.go` (`poRemovalFor`, `poEditPhaseDeleteLine`) is the flow
and `internal/omsapi/reorders.go` (`DeletePurchaseOrderLineItem`) is the client;
OMS's `services/line_entry.assert_deletable` and
`PurchaseOrderSerializer.get_can_delete_items` are the contract. What is worth
knowing before touching any of it:

- **DELETE while the order is still the shop's own, VOID once the supplier has
  it**, and that boundary is the captain's decision rather than an
  implementation detail: while the document is private a line put on by mistake
  is a typo and the honest record of a typo is no line at all, so deleting takes
  NO reason; once the supplier holds a copy the line is part of a record someone
  else also has, so it can only be struck off with one.
- **`can_delete_items` is READ, never re-derived.** It is served from
  `PurchaseOrder.PRE_SUPPLIER_STATUSES` and its serializer docstring says in as
  many words that a client must never keep its own copy of which statuses those
  are — guess wrong and you offer an irreversible destroy on an order the
  supplier already holds, or hide it on one where voiding leaves a meaningless
  ghost. Nothing on this side reads `po.Status` to answer it, so OMS adding a
  second pre-send state costs ScanTTY nothing.
  `TestPOLineRemove_TheOfferFollowsTheFlagAndNotTheStatus` is the guard and it
  is the only test in the file a `status == "draft"` rule fails: its two rows
  are the ones where the flag and the status DISAGREE.
- **The flag is a `*bool`, because absent and false are different facts.** An
  OMS too old to serve the key has not said the supplier holds the order — it
  has said nothing — so a plain `bool` would decode a silence into a confident
  "you may not delete" about a draft the operator is still editing. `nil` offers
  the reversible half and the frame NAMES the silence (`removalNote`); the
  irreversible action is never offered on a guess.
- **`Ctrl-X` confirms the destroy, not `Enter`.** `Ctrl-E` OPENS the confirm and
  enter is the key a hand reaches for next, so binding the write to it would
  make a reflex enough to destroy a line — the same choice the New PO
  supplier-switch confirm makes. Every other key on that frame either ACTS — the
  movement keys scroll the caveat where it outruns the pane, and the bar names
  them exactly there — or ANSWERS (`deleteNote`), because a press that did
  neither redraws a pane that is a pure function of unchanged state.
- **Both refusal shapes are recovered.** `_destroy_item` writes
  `{"error", "code"}` and `void_item` writes `{"error"}` alone; neither reaches
  DRF's exception handler, so `parseError` hands the whole raw body over.
  `asLineRefusal` tries the two narrow recognisers that already exist
  (`AsLineEntryError`, then `AsReceivingRefusal`), so a gateway page and a DRF
  envelope still arrive as the `APIError` they are.
- **THE SITE IS THE EDIT SCREEN, and the set was derived.** Every screen that
  displays persisted PO lines was checked: `po_edit.go` has the per-line cursor
  and the per-line affordance rows, so removal is one more row-action there;
  `po_detail.go` is a scrolled READ-ONLY sheet with an offset and no line
  cursor, so it has no "the line you are on" for a removal to name and `E` is
  one key away to the screen that does; `receive_form.go` is past the boundary
  by construction (`PRE_SUPPLIER_STATUSES` and `RECEIVABLE_STATUSES` are
  disjoint) and receiving records what ARRIVED; `po_add_line.go` reads
  `po.Items` only for a price hint.
  **The New PO staged cart is NOT order lines**, and conflating the two would be
  a defect: those rows exist before the order does, carry no line id and no
  flag, and `ctrl+x` already drops one with no server round trip.
  (`po_create_pickers.go`'s `.Items` is a SUPPLIER's catalogue, a different
  `Items` entirely.)
- **THE ORDER LIST HIDES AN ORDER EMPTIED BY VOIDING AFTER IT LEFT THE SHOP,
  and the filter is the SERVER's.** `PurchaseOrderViewSet.get_queryset` hides an
  order on the `list` action when ALL THREE hold, and only then (oms-a8o): it
  HAS line items, none of them survives unvoided, and it is OUTSIDE
  `PurchaseOrder.PRE_SUPPLIER_STATUSES`. ScanTTY's list is a straight
  pass-through of that endpoint (`list.go`'s `purchaseOrderRows`, no local
  filter), and it cannot lift the filter without changing the OMS API, so the
  answer is non-silence rather than a refusal — but only on the path where the
  loss is reachable, which is the half that keeps being got wrong.
  **THE DELETE CONFIRM IS NOT THAT PATH, AND THE FROZENSET IS WHY.** Deletion is
  offered only where `can_delete_items` is true, and that flag is served from
  `PRE_SUPPLIER_STATUSES` itself, so the third conjunct is FALSE on every order
  a delete can reach: whatever a delete leaves behind — no line at all, or
  voided ghosts — the order stays listed, the `draft` filter included. The
  confirm carried the warning until oms-a8o narrowed the filter and it became a
  documented claim the code could not honour, which is a defect in exactly the
  same way silence about a real loss is. It is gone from `deleteCaveats`; do not
  reinstate it, and do not read PR #1033's own body as licence to (it names a
  residual case — "a draft carrying voided ghosts" — that the shipped filter's
  pre-supplier disjunct already lists).
  **THE VOID PROMPT IS.** `voidCaveats` (`po_edit.go`) warns there, gated on
  `poLastActiveLine`, and the wording differs from the retired one in the way
  that matters: past the pre-supplier boundary OMS refuses to add a line
  (`assert_addable`) and has no unvoid endpoint at all (`is_voided` is only ever
  written true), so search is not one way back among several — it is the only
  one, and "until another line is added" would have been a remedy the operator
  cannot reach. `poRemovalUnknown` lands on the same prompt and states the
  consequence CONDITIONALLY, because there the client does not know which side
  of the boundary the order is on.
  **THE HEADLINE NAMES THE REMEDY AND NOT THE KEY, BECAUSE THE KEY DOES NOT
  WORK ON THAT FRAME.** `PurchaseOrderEditScreen.WantsRawInput` is true, so the
  root never sees a keystroke and `ctrl+k` does not open the search palette
  here — it reaches the focused Reason box, where bubbles binds it to "delete to
  end of line", so a sentence reading "ctrl+k finds it" would name a key that
  eats what the operator typed where it is named. The key is spelled only in the
  prose, BEHIND the clause that qualifies it (`voidSearchSentence`): the
  qualifier precedes the key so a trim can only ever take the key, never leave
  it standing bare. Both orders were tried and the wrong one was reported at
  80x18 by the height sweep. That sentence also NAMES THE ORDER, because "search
  by number" is not a remedy on a frame that withholds the number — the
  essential row names the LINE and the order's number is two screens back on the
  detail sheet. THE NUMBER PRECEDES "BY NUMBER" for the same reason the
  qualifier precedes the key: appended at the TAIL it was the first thing the
  trim took, and at 80x18 the poRemovalUnknown wording drew "…reaches it by
  number:" with the number gone. Ahead of the phrase the bound holds by
  CONSTRUCTION — a trim keeping "by number" keeps everything before it — rather
  than by where the words happen to break, which is all that had ever kept the
  poRemovalVoid wording right. Its sweep was single-branch and therefore green
  over the defect; it walks BOTH answers that reach the prompt now, derived from
  `poRemovalFor` over the flag's whole space, and the leading caveat's one-row
  property is asserted against the layer's own fold
  (`TestPOLineRemove_TheVanishingWarningIsOneRowWhereverItIsDrawn`) because
  `poRemoveFlatPane` collapses whitespace and cannot see a headline lose its
  second row.
  **ONE STRUCTURAL RULE, THREE INSTANCES: WHATEVER MUST SURVIVE MUST LEAD.**
  `jdeCaveatLines` folds from the tail and `jdeFitHeader` trims from the tail,
  so ordering is the only bound on this frame that holds by CONSTRUCTION. The
  qualifier leads `ctrl+k`; the NUMBER leads "by number"; and the REMEDY leads
  the LOSS in both headlines, so every prefix of a folded headline either makes
  no loss claim or carries the way back with it. Read them as one rule — written
  three times as three tricks, the next wording keeps two of them and loses the
  third, which is exactly how each of these arrived.
  WHAT IS PERMANENT AND WHAT IS NOT ARE DIFFERENT FACTS, and the wording keeps
  them apart. Permanently lost: the order's place on every purchase-order list,
  `all` included, since it can never regain an active line. Not lost: the ORDER,
  which is not deleted — `get_queryset` filters only `if self.action == "list"`,
  so detail retrieval still resolves, and `backend/search/views.py` carries no
  emptiness filter, so ctrl+k reaches it by name from anywhere without a saved
  link. Reachable by name, absent from every browsable list, for good.
  **A WARNING CUT BEFORE ITS WAY BACK IS A DEAD END, SO THE CLAIM IS ONE ROW.**
  The prompt's caveats ride the PINNED HEADER and not the body — the body holds
  the operator's Reason box, and `jdeLines` keeps a block's START, so a caveat
  written ahead of that row pushes the row off a short pane while one written
  after it is simply the tail a short window drops. `jdeFitHeader` then gives
  ground BY RANK and, within a rank, from the END, so a multi-row caveat loses
  its tail — which is where a remedy naturally falls. Two wordings had that dead
  end and were caught by the height sweep rather than by reading (80x14, then
  80x13 after shortening); shortening only moves the height, because the budget
  reaches zero one row at a time. So the first caveat is a SINGLE row wherever it
  is drawn and carries the loss AND the remedy, and the prose explaining it is a
  second caveat behind it — the headline-then-detail split `setErr` makes on the
  status row, for the same reason.
  **THAT ONE-ROW CLAIM IS CONDITIONAL, AND THE CONDITION IS ENFORCED RATHER
  THAN ASSUMED.** It used to be stated flat, and it was only ever true at 80
  columns and up: Root draws from a terminal width of 45 (`app.go`'s
  `contentWidth` gate), and at 60 `screenBodyWidth` is 31, which leaves
  `jdeCaveatLines` 29 cells — the loss-first headline broke at exactly 29 and a
  trim keeping the first row alone stated the loss with the remedy gone. So
  `voidCaveats` now takes the pane width and withholds BOTH caveats — headline
  and prose as a UNIT, since the prose states the loss too — wherever the
  headline would not fold to one row. Refuse rather than mutilate, the stance
  `jdeTooShort` takes one level up; it is NOT the silence rule 1 forbids, which
  is about a keypress changing nothing visible. The gate reads `bodyWidth()` and
  no named width ON PURPOSE: this file says 80 must HOLD while `app.go` draws
  down to 45, and a gate computed from the real pane needs no answer to that.
  **THE GATE ANSWERS FOR THE WHOLE HEADLINE SET, NOT FOR THE BRANCH BEING
  DRAWN** (`voidCaveatsFit`), and the property is that both answers are withheld
  together or drawn together. Asked of one branch it gave each answer the
  threshold its own sentence earned, and the CERTAIN-loss wording is two cells
  longer than the hedged one — so at 75 and 76 columns the prompt went SILENT
  about a loss the server had confirmed while still warning about one it had
  only left possible, severities inverted by two cells of prose. The maximum
  over the set makes the threshold single by CONSTRUCTION, so rewording one
  branch moves both. Measured with the layer's own functions, it currently
  evaluates to 77 columns and up (`screenBodyWidth(77)` = 48, which is the
  46-cell longer headline plus `jdeIndent`); that number is an OUTPUT of the
  wordings, and `TestPOLineRemove_BothVoidAnswersAreWithheldOrDrawnTogether`
  asserts the symmetry rather than the number, reporting the width it comes to.
  No wording carrying both facts fits the 18 cells the narrowest drawable pane
  leaves.
  `TestPOLineRemove_AShortVoidPaneKeepsTheVanishingOrderWarning` sweeps every
  drawable height at every drawable WIDTH — derived from Root's own gate
  (`jdeDrawableWidths`), because three hand-picked widths is exactly how the
  60-column hole survived — and fails a pane stating the loss without its
  remedy. That check keys on the loss FRAGMENT, not on the headline: keyed on
  the headline it could not fail, since the remedy leads and is therefore a
  substring of it, and a SPLIT headline reads as absent once `poRemoveFlatPane`
  has collapsed the pane. Verified by reverting both halves: it reports from
  45x14 through 79x12, 60x12 among them, and at no width from 80 up.
  **THE ESSENTIAL ROW IS BOUNDED AS ASSEMBLED, NOT PART BY PART.**
  `removalHeadline` (shared by both confirms) clipped the line's NAME to what
  the lead and the ` · N ordered` facts left, FLOORED AT 1, and then appended
  the facts anyway — a bound applied to one part of a row that is afterwards
  added to, which is the `assetScopeRows` rule broken on the one row that says
  what an irreversible action is about to destroy. At the 20-cell pane
  `screenBodyWidth` floors at, the delete row assembled to 25 cells and
  `clampToBox` cut it with no ellipsis: ` Delete: M · 250 orde`. Reported at
  every terminal width from 45 to 53 (delete) and 45 to 51 (void), by a sweep
  reading the screen's OWN `View` — measured off the CLIPPED pane the check
  cannot fail, because the truncation has already happened
  (`TestPOLineRemove_TheIdentityRowFitsThePaneOnBothConfirms`).
  THE GIVE-ORDER IS A DECISION: where the pane cannot hold both, the FACTS give
  and the NAME keeps the room, leaving `poRowDropMark`. That qualifies
  `deleteHeadline`'s standing "the ordered quantity never gives" — true of every
  pane this interface is modelled on, and not of the extreme, because the
  quantity DISAMBIGUATES a name and so presupposes one: beside a name cut to a
  character it separates nothing. Both comments say so; do not let them drift
  apart again.
  Pinning a header COSTS the prompt a row, so the layer now refuses to draw it
  one terminal row earlier than it used to (80x11 rather than 80x10). That is
  the layer's designed answer — a bounded notice naming the height it needs,
  with `Esc` still leaving — and it is the same trade the delete confirm already
  made, which is why it is accepted rather than worked around.
  Two OMS behaviours are REPORTED there and deliberately unfixed, so do not
  read either as a ScanTTY defect: `void_item` carries no status gate (voiding a
  draft line leaves the meaningless ghost `get_can_delete_items` warns about),
  and voiding a whole ORDER cascades to its lines, so a voided order that had
  lines is hidden from every list including `all`. The latter is the operator's
  own explicit act on the whole document rather than a side effect of a line
  edit, which is why the terminal's order-void modal (`po_detail.go`) says
  nothing about it.
- **A PER-LINE INDEX IS CARRIED ACROSS A RELOAD BY IDENTITY, NEVER BY
  POSITION.** `editLineIdx` / `assocLineIdx` address `po.Items` positionally and
  every line action fires `load()`, so a reload landing under an OPEN sub-phase
  is ordinary rather than a corner. The clamp that used to hold the index in
  range (`if editLineIdx >= lineCount() { editLineIdx = 0 }`) re-pointed it at
  whatever now sat there: with two lines cut to one, the delete confirm went on
  NAMING the line the operator had read and confirmed while `Ctrl-X` would have
  destroyed the other one — and an EMPTIED `Items` list, which only DELETE makes
  reachable, got a valid-looking index 0 into nothing that every reader then
  indexed. `reseatLineIndexes` follows each index to its own line by
  `poLineID` and CLOSES the sub-phase standing on one whose line is gone,
  saying so on the status row; `addressedLine` is the one guarded read every
  `po.Items[editLineIdx]` site goes through.
- **THE FLAG IS READ AGAIN ON EVERY REFRESH, INCLUDING UNDER AN OPEN CONFIRM.**
  Carrying a sub-phase across a reload by identity means it follows its own
  LINE, and the line surviving says nothing whatever about the ORDER: a delete
  confirm opened on a draft outlived the order being sent from the web, with
  the bar still reading `Ctrl-X=Delete line` — the flag read once and CACHED
  ACROSS A REFRESH, the one thing its serializer docstring forbids.
  `removalPhaseHolds` is the single predicate `deleteBar` / `voidBar`, the
  destructive arms and both frames read; when it fails the frame closes back to
  the LINE EDITOR (whose status row now offers whatever the refreshed flag
  calls for) and says what changed. The mirror is held too — a void prompt open
  when the order becomes the shop's own again offers the instrument that leaves
  a ghost where the server now allows the typo to be erased.
- **A BODY SENTENCE MAY NAME A KEY ONLY WHERE THE BAR DOES, AND THE CLAUSE IS
  WHAT GIVES — NOT THE SENTENCE.** `removalNote`'s standing text carries a FACT
  about the row (a line already voided, a server that did not answer) which is
  true whether or not a key is on offer, so gating the whole note on
  `removalOffered` would have taken the row's own explanation away with the
  claim. Only the clause naming `Ctrl-E` follows the legend.
- **A REFUSAL IS WORDED FOR A ROW THAT CANNOT FOLD.** `fitStatus` gives an
  error `bodyWidth-2` cells — 49 at 80 columns — so every one of this screen's
  local refusals leads with the load-bearing clause (`nothing written: …`,
  `nothing deleted: …`) and lets the circumstance be what the cut takes. They
  are constants (`poEditLineGoneNote` and its siblings) so the tests assert the
  wording the screen draws, and the `poEditLoadedMsg` arm returns a `Status`
  behind them: the row is one surface and a sentence needs two.
- **A REMOVAL IS NOT OFFERED OVER A WRITE ALREADY IN FLIGHT.** `removalOffered`
  is the single predicate the bar and the arm both read, so the gate goes there
  and the legend loses `Ctrl-E` in the same breath the key stops acting.
  Without it, Enter on the line editor followed by `Ctrl-E` on the status row
  opened a delete confirm whose status row read `Deleting…` for a delete nobody
  asked for, whose own `Ctrl-X` was dropped while the OTHER write was out, and
  which then vanished by itself when that write answered.

### Kits are inventory items the item API refuses to admit exist

Before touching anything kit-shaped (`internal/omsapi/kits.go` carries the full
note, and is the authority):

- A kit IS an `InventoryItem` with `is_kit=True` — no table of its own — but
  **neither item serializer exposes `is_kit`, `components` or
  `component_count`**. Those live only on `KitSerializer` (`/api/inventory/kits/`).
  So "is this item a kit?" is answered by fetching the id from `/kits/` and
  reading the status: a **404 is the answer "no"**, not a failure
  (`omsapi.IsNotKit`). Anything else left the question unanswered and must say so.
- `/api/inventory/items/` **excludes kits by default**, and the filter lives in
  `get_queryset`, so it applies to the DETAIL route too: GET or PATCH of a kit's
  id under `/items/` is a flat 404 without `?include_kits=true`, and that
  includes every ACTION and SUB-RESOURCE on the viewset. Every detail route a
  kit can legitimately reach sends it (`omsapi.includeKitsQuery` /
  `includeKitsValues`: `GetItem`, `GetItemMetrics`, `GetPurchaseHistory`,
  `SetItemRetired`, `DeleteInventoryItem`, `SetItemCountMode` — which the kit
  save fires AFTER the `/kits/` PATCH). The deliberate exceptions: cycle-count,
  log-usage and pack-container, because a kit carries no stock (and a pack is a
  way of counting stock) and the backend writes stock without `full_clean()`, so
  a count against one would persist as a number nothing can draw down — the item
  detail hides all three keys for a kit instead; and `ListItemKits`, because a
  kit is never a component, so its 404 there is the right answer. A kit is saved
  through `PATCH /api/inventory/kits/{id}/` — that is also the only write path
  its `components` have (nested-writable; there is no `/kit-components/`
  endpoint) — and the item form sends `current_stock: 0` on that PATCH, with the
  Current stock row read-only and a warning under it whenever the stored figure
  is not already zero.
- Receiving is where a kit stops being a catalogue curiosity: a kit line is
  ordered as one SKU and **credits its component items on receipt, never its
  own stock**. ScanTTY's receive flow (`POST …/purchase-orders/{id}/receive/`)
  explodes kits correctly server-side; the barcode-receive endpoint refuses kit
  lines outright. PO lines carry `is_kit_line` + `kit_components`, whose
  `quantity_per_kit` — not the pre-multiplied `quantity` — is what a PARTIAL
  receipt multiplies. `internal/tui/po_kit_lines.go` carries that note.
- **Serialized items ARE allowed as kit components now**, and that is a
  different fact from where a serial goes. The OMS ban was lifted deliberately
  (`docs/PO_RECEIVING_API.md`, and `KitComponent.clean()` carries the argument in
  full): it blocked a legitimate configuration while the hazard it named — stock
  credited with no serial recorded — was never unique to kits, since
  `mark-delivered` has always done it to an ordinary serialized line. What guards
  the identity rule now is the receipt itself (naming the kit is a 400) plus
  **`serials_outstanding`**, which every receive path reports and a client must
  surface. Receiving a kit WITH serial capture is a live path.
  **THE TWO HALVES OF THAT SENTENCE SHIPPED A RELEASE APART, WHICH IS THE LESSON
  WORTH KEEPING.** The receiving half honoured the lift and said so in three
  places; the kit-components EDITOR
  (`internal/tui/inventory_item_form_kit.go`) went on dimming every serialized
  item and refusing the pick, quoting the reason OMS had abandoned. A screen
  refusing what the server accepts is a documented claim the code does not
  honour in exactly the same way as a screen permitting what the server refuses,
  and it fails SILENTLY: nothing errors, an operator simply cannot build a
  configuration and is told why by a sentence that is no longer true. When a
  server-side rule is lifted, sweep for the CLIENT-side copies of it — the set is
  derived by grepping the retired wording, not by remembering which screens
  enforced it.
  **LIFTING IT IS NOT LOOSENING WHERE A SERIAL GOES**, and the guard is in three
  places that must stay in step. RECEIVING reads `serial_targets` and nothing
  else, so a kit's own id is never a capture target
  (`TestReceiveKit_ASerialNeverNamesTheKitItself`). The ITEM FORM freezes a kit's
  `Track serial numbers` row (`fieldReadOnly`) and ASSERTS `is_serialized:false`
  on every kit save (`buildPayload`) — asserting rather than omitting, because
  `KitSerializer.validate` falls back to the STORED value for an absent key, so
  omission is no guard at all against the stray flag `InventoryItem.save()`
  leaves reachable. And the component PICKER still drops the kit's own id, which
  used to be excluded twice for a stray-serialized kit and is now excluded once.
  `TestItemFormKit_ASerializedComponentNeverSerializesTheKit` holds all three and
  was watched failing against each.
  **NOTHING IN THAT PICKER IS SHOWN-AND-REFUSED ANY MORE.** Every exclusion left
  is a DROP (the kit itself, an item already listed) or an absence (`/items/`
  excludes kits, so a nested kit is never offered), so `kitPickOption` carries no
  reason, the list dims nothing, and every option commits
  (`TestItemFormKit_EveryOptionOnThePickerCommits`). What that leaves is the one
  refusal a picker cannot avoid — having nothing to pick — which used to return a
  byte-identical pane and now ANSWERS in four wordings, because "could not tell"
  (loading, load failed) and "found nothing" (filtered out, nothing left) are
  never the same answer and their remedies differ. While a load is in flight that
  answer LEADS the working line through `poLeadOnto` rather than replacing it:
  `statusRow`'s `saving` branch wins outright, so an answer handed to its
  `errMsg` argument is drawn by nothing at all.
  **WHICH ITEMS ARE SERIALIZED IS A READING THE OPERATOR HAD, AND IT HAS TO
  SURVIVE THE REFUSAL THAT CARRIED IT.** Lifting the ban first removed the fact
  along with it, because the fact had only ever been visible AS the refusal's
  reason. It is a two-cell FLAG COLUMN now (`kitPickSerialFlag`), and three things
  about it are decisions rather than taste: it is TWO CELLS because at the
  80-column floor a picker row has 45 and a realistic MRO name spends all of them,
  so anything competing with the item's IDENTITY at that width is the wrong trade;
  it LEADS, because `fitCell` clips from the right and a marker after the name is
  eaten at exactly the width the fact matters most; and the blank gutter is the
  SAME two cells, so names line up down the list. The legend rides the picker's
  note and is drawn only where a flag is (`kitPickNote`, asked of the DRAWN
  options rather than the catalogue), because that note is 49 cells against a
  51-cell pane and the legend is paid for out of the kits sentence.
  **ITS FIRST TEST WAS VACUOUS AND PASSED WITH THE FLAG COLUMN DELETED**: the
  fixture was named `Serialized widget`, so the row began with `S` because the
  ITEM did. A fixture for a check about a MARK must not begin with that mark, and
  one for a check about a CLIP must be clipped at the WIDEST pane in the table —
  the long-name fixture fitted at 100 and 120, so two widths of three proved
  nothing until the assertion was made to FATAL on an unclipped row rather than
  pass over it. Both are the vacuous-fixture rule, and both were found by deleting
  the code the test names and watching it stay green.

### A lateness or variance figure names what it is measured against

`internal/omsapi/lead_time_yardstick.go` carries the contract note and
`internal/tui/report_table.go` is the layer that draws it; both are the
authority, and the DERIVED SET with its deliberate exclusions is recorded at the
top of `internal/tui/report_yardstick_test.go`. What is worth knowing before
touching any report that shows a rate, a variance or a lateness:

- **An OMS `LeadTimeLog` row holds TWO promises and scores only one.**
  `variance_days` — and every rate counted off it — measures the supplier link's
  STANDING QUOTE (`ItemSupplier.average_lead_time`, read at receipt time), never
  the `expected_delivery_date` the operator confirmed on the order. A vendor that
  hit the date it agreed can still sit in "Late %". That is DELIBERATE
  (`inventory.services.supplier_selection` scores the quote and discounts it by
  how often the vendor broke it; scoring a per-order date would let a vendor
  quote three, confirm ten, deliver ten and win on both axes) and is not ours to
  reopen. What was wrong was the SCREEN saying "Late %" and nothing else.
- **The yardstick is READ, never assumed.** OMS serves `variance_measured_against`
  on `supplier_performance`, `lead_time_trends` and `lead_time_analysis`
  (PR #1046); `omsapi.LeadTimeYardstick` is the one embedded decode for all
  three. `""` means the server said NOTHING, and the legend says so rather than
  naming the quote — an OMS too old to serve the key has not told us these are
  quote-scored, and filling that silence in asserts a promise nobody made.
  AGREEMENT across rows is required (`reportYardstickOf`): one silent row
  silences the whole legend, because a legend is a claim about every figure
  under it.
- **The declaration is the HEADER.** A column whose figure is scored against a
  promise ends its header in `reportYardstickMark` (`*`), and the legend is
  DERIVED from the marked columns the pane actually DREW. One string, so there
  is no roster to keep in step: a marked column the fit dropped cannot be
  explained by a legend that mentions it, and a new marked column cannot be
  missed. Nothing else in these reports ends a header in `*`.
- **The legend LEADS the table.** `clampToBox` drops from the BOTTOM, so a
  legend under the rows is the first thing a short pane takes — and a figure
  whose yardstick has been trimmed off the pane is exactly the bare lateness
  this exists to stop. ROWS are what give instead, which is the safe direction:
  fewer figures, each still explained.
- **`on_time_rate` is a PERCENTAGE 0..100 on every one of the three payloads.**
  The struct comment used to call it "a fraction 0..1" while the render appends
  `%` without scaling; the render was always right and the comment was the
  defect, and its only possible effect was to invite somebody to "fix" the
  render into 7500%. What is HELD is that nothing on this side scales the
  value: `TestPurchasingLeadTime_OnTimeRateIsAPercentage` asserts a served 75.0
  arrives as 75, and `TestPurchasingLeadTime_OnTimeRateNotDoubled` that the
  screen draws it without multiplying. Neither pins OMS's own
  `on_time_count/total*100` and no client-side test can — that direction is read
  from `reorder_queue/views.py` and nowhere else.
- OMS PR #1046 also RENAMED six keys on `GET /api/inventory/suppliers/<id>/`
  and its `analytics` action, and three CSV headers. ScanTTY drives neither, so
  nothing here decodes them; the keys ScanTTY does decode were deliberately left
  alone and only GAINED the sibling above.

## The receiving flow is driven off ONE fetch, and the server decides

`internal/omsapi/po_receiving.go` carries the contract note and
`internal/tui/receive_form.go` is the flow; OMS's `docs/PO_RECEIVING_API.md` is
the specification and outranks both. What is worth knowing before touching
either:

- **`GET …/purchase-orders/{id}/receiving/` is the whole input.** It answers, in
  one round trip, whether the order may be received against and why not, which
  lines are outstanding and which settled, what a scanner will read off each
  line (`scan_codes`) and which identities each line's serials may name
  (`serial_targets`). There is NO fall-back to `po.Items`: a failed fetch is
  COULD NOT TELL, and receiving off the order's own items would mean guessing at
  exactly the two things that must not be guessed. `phaseBlocked` draws both
  facts and keeps them apart.
- **`can_receive` and `unavailable_reason` are a pair.** "You may not receive
  against this, and here is why" is a different fact from "there is nothing left
  to receive", and an operator at the bench with a box acts differently on each.
- **A mismatch is recorded and flagged, never rounded.** `quantity_received`
  goes as typed even when it exceeds the outstanding balance; the server flags
  the line `over_received` with a positive `quantity_variance`. The review phase
  raises it BEFORE the post because the contract asks a client to. SHORT is not
  the same fact and is not flagged as one — receiving 8 of 10 leaves 2
  outstanding, which may be a backorder; only an explicit close-short says the
  balance is not coming.
- **`receipt_state` / `is_settled` are the server's and are never re-derived.**
  Asking "is received < ordered?" calls a line closed short a partial one, and
  closed short is the one state that means somebody DECIDED. `is_settled` is not
  `is_fully_received`: a line closed two short is settled and not fully
  received, and both facts stay on the record.
- **Serials ride INSIDE the receipt** (one transaction: a refused receipt writes
  nothing), so capture is local until Enter on the review — which is what lets
  the operator walk back to a unit and fix a typo. Every arm that moves the
  cursor stores the boxes first. Fewer serials than units is allowed on purpose
  and comes back as `serials_outstanding`.
- **`serial_targets` is the ONLY thing consulted for "may this carry a serial".**
  Never `item_details.is_serialized`, which on a kit line describes the KIT.
  `TestReceiveKit_ASerialNeverNamesTheKitItself` asserts it ON THE WIRE rather
  than through a predicate: the previous guard was a predicate call, a fix round
  replaced its assertion with a substring over a frame the caveat is
  structurally absent from, and the check could then fail in neither direction.
- **The RECEIVED transition is the server's and it is conditional.** Closing the
  last outstanding balance does not on its own make the order `received` — an
  order nothing was ever received against does not advance — and that rule has
  already changed once since this client was written. So nothing on this side
  predicts it: the confirm says what the write DOES, and the summary reports the
  `status_label` that came back. `is_settled` is an INPUT to the order's status,
  never a synonym for it.
- **Every receiving endpoint is authenticated, the worksheet included** — GET
  though it is. `PurchaseOrderViewSet.get_permissions` returns `AllowAny` for
  `list`/`retrieve` and `IsAuthenticated` for everything else, and all of these
  are `@action`s. Do NOT read that off
  `backend/config/api_permission_matrix.yaml`, which records `receiving` as
  `IsAuthenticatedOrReadOnly`: the YAML is generated from the declared
  `permission_classes` and cannot see a `get_permissions` override, so on this
  viewset it is the default rather than what is enforced. Reading it as the
  effective permission is a mistake this work already made once.
  `Client.do` sends the bearer token and refreshes once, so the happy path needs
  nothing; what matters is the FAILURE, which DRF renders as `{"detail": ...}` —
  neither shape below. `receiveReason` names it ("no longer signed in") rather
  than relaying it, and reaching it means the refresh failed too.
- **An order can be receivable with nothing left to receive.** `can_receive:
  true` alongside `outstanding_line_count: 0` is a real state, not a
  contradiction: every line closed short or struck off without a single delivery
  settles the order without it ever reaching `received`. The contract instructs
  a client to say so AND point at voiding or cancelling the ORDER — `qtyBody`'s
  empty branch and Ctrl+R's decline both do, because refusing without it is a
  dead end.
- **"Never silently discard" demands NON-SILENCE, not refusal — and a refusal
  is only legitimate where the operator can satisfy it from the frame it is
  drawn on.** If input is about to be dropped, SAY SO FIRST; blocking the key is
  one way to be non-silent and it is the right one only when the operator,
  standing on that frame, can clear what is in the way. Irreversibility argues
  for making the consequence UNMISSABLE, never for blocking a key nobody can
  unblock: a refusal that cannot be satisfied is a dead end, and a dead end is
  its own defect. The failure that motivated it: the write-off gate
  (`receive_form.go`, `writeOffDiscards` / `writeOffCaptureLoss`) counted
  CAPTURED SERIALS, and `s.captures` is written only by `beginReceipt`,
  `storeUnit` and `resetEntry` — none reachable from the quantity form once the
  boxes are empty. Capture a serial, walk back, clear the quantity that opened
  it, and both destructive keys went permanently unnamed answering "receive or
  clear first", with receiving impossible (Enter refuses an empty form),
  clearing impossible (no key on the phase touches `s.captures`) and the serial
  not even sendable, since `buildReceipt` drops a line whose box is blank. So
  the gate SPLITS by what the frame can act on: the quantity boxes and the
  delivery/notes block REFUSE (a backspace away), and the captures are NAMED
  BY COUNT on the confirm — the frame Ctrl+X is pressed on, rather than a note
  the next keypress retires — and the write proceeds. **The bar follows the
  gate in both directions**: a key that will proceed, warning and all, is named;
  only a key that would refuse is unnamed, or the dead end moves onto the bar.
- **`reopen-short/` exists and this client does not drive it.** A close-short
  recorded in error is corrected there; the correction is stamped BESIDE the
  write-off rather than erasing it, so a reopened line comes back outstanding
  with `was_reopened` set and its `closed_short_reason` intact. Both are decoded
  and the line's readings say `reopened`, because receiving against a line
  somebody already got wrong once is worth knowing.
- **The refusal body is NOT the standard envelope**, again. All four endpoints
  write `{"error": "<prose>"}` by hand with no `code`, so `parseError` hands the
  whole raw body over. `omsapi.AsReceivingRefusal` recovers the sentence and is
  narrower than `AsLineEntryError` (which requires a code this shape has not):
  it accepts only an object whose `error` is a non-blank JSON STRING, so a
  gateway page, the DRF envelope and a field-validation body all keep the shape
  they arrived in.
- **A block taller than the window loses its TAIL and no key can fetch it.**
  `jdeLines.Window` keeps a block's START and nothing scrolls inside one, so a
  line block's contents are in a stated SACRIFICE ORDER (`addLineBlock`): what
  the typed number means, then what a kit receipt credits, then the readings,
  then the serial story. Measured at 80x30 with a kit, the window is eleven rows
  — with the readings ahead of the credit the second component was off the pane,
  on the block whose whole point is what a kit puts into stock.
  KNOWN AND ROUTED, on the axis that order does NOT cover: the block's own
  quantity BOX can be the thing off the pane. The block opens with the line's
  label and `Window` keeps a block's START, so at 80x12, 80x13 and 80x15 the
  pane draws `2  Box of M3 bolts` and not the field under it — while that field
  has the focus. Every rune after the first then redraws a byte-identical pane
  (the first only moves because the bar changes shape), which is rule 1 broken
  by geometry, the same shape as the New PO pickers' box and a DIFFERENT
  mechanism: the body window rather than the header budget, so the answer
  surface above does not reach it. Fixing it means reopening `addLineBlock`'s
  sacrifice order — a decision, not a patch — which is why it is written down
  rather than done in passing.

## Conventions

- **JD Edwards World interface.** The standing goal is parity with the OMS web
  app in a fixed columnar green-screen style: keyboard only (a barcode scanner
  is a keyboard delivering a burst plus Enter), one shared label column, a
  persistent action bar naming exactly the keys that work where the cursor is.
  `internal/tui/jde_form.go` is the shared layer; `internal/tui/po_edit.go` is
  the pilot and `po_edit_jde_test.go` is where the layout and key scheme are
  pinned. Extend that layer — do not hand-roll a second style beside it.
  **Purchasing is fully converted** (sc-jde-poc closed the last of it): the VIEW
  half (`po_detail.go`), the edit sheet, the add-line flow (`po_add_line.go`),
  RECEIVING (`receive_form.go`) and the New PO ENTRY flow (`po_create.go` +
  `po_create_pickers.go`). A flow with PHASES of its own brings its own derived
  sweep rather than joining a shared one — `po_add_line_sweep_test.go`,
  `receive_form_sweep_test.go`, `po_create_phase_sweep_test.go` and
  `po_edit_sweep_test.go` are four
  copies of one file with the nouns changed: phases from the iota's sentinel,
  keys from `poKeySpace()`, state and focus from `reflect` over the screen
  struct.
  The edit screen's copy is the newest and carries the one lesson the other
  three did not have to learn: **PROBES ARE PER CASE ON A FIELD FORM.** The
  shared `{nil, {"down"}, {"end"}}` exists so a clamped cursor at an edge is not
  reported dead, and on a LIST that is free — but on a field form a probe moves
  the cursor onto a row with DIFFERENT rules, and probing `down` off the line
  editor's status row wraps onto the COST row, where every printable rune goes
  into a focused box. The sweep then reported 96 keys as "acting" on a rule it
  had never actually tested. Where the cursor WRAPS, the resting position
  already reaches every named key; where it CLAMPS (a list, and the form's
  paging pair) a second position is what proves the key is not dead.
  And a state fingerprint MUST NOT RANGE A MAP: `poEditState` ranged `selects`,
  and Go's randomised iteration made every key compare unequal to itself, so the
  sweep went red for a reason unrelated to the property it names — the
  vacuous-fixture rule with the sign flipped.
  **A bar entry's `Key` is the LITERAL keystroke.** `{"a", "Assets"}` is the
  letter `a`; `{"A", "Attach"}` is shift+A. `poBarKeyNames`
  (`po_view_jde_test.go`) is the ONE table the columnar purchasing sweeps read
  (the view, New PO phase and edit phase sweeps; `po_add_line_sweep_test.go`
  keeps its own), and a
  `Key` it does not know FAILS rather than being skipped — which is also what
  keeps the convention true, because a screen displaying `A` for a key that is
  really `a` would be spelling a keystroke nobody presses.
- **A key the bar does not name must do nothing**, and a key it names must do
  something. Tests assert both — and a key absent from a sweep's VOCABULARY is
  pressed in neither direction, so it is untested rather than passing. That is
  how `N` on the purchase-order list survived `poAllBarKeys`, and how `tab`
  silently committing the New PO screen's supplier survived `poPickerVocabulary`
  one round after that sweep was written to replace the first. Two sweeps hold
  the rule and they cover different halves of the app: `po_view_jde_test.go`'s
  `TestPOView_BarNamesExactlyTheKeysThatWork` walks the columnar screens, whose
  bar is a `[]actionBarItem`; `list_bar_honesty_test.go` walks every
  `ListScreen`, whose bar is still a hand-built STRING (`ListScreen.footerHint`)
  and is therefore invisible to the first. Adding a screen means adding it to
  whichever sweep matches its bar — the header comment in
  `list_bar_honesty_test.go` records how eight advertised keys survived across
  four lists by falling between them.
- **A sweep DERIVES its members; it never restates them.** Three keys reached an
  operator's terminal doing nothing while the bar named them, and every time the
  sweep that existed to stop it was reading a hand-kept roster the key was not
  in: `poAllBarKeys` held none of the list letters, `poPickerVocabulary` held no
  `tab`, and the picker table held no `poPhaseSupplierSwitch` — where `enter`,
  the key that OPENS the confirm, was answered with `nil`. A list that has to be
  edited in step with the code is that omission waiting to happen again, and it
  fails SILENTLY, which is why it keeps being made.
  So each roster comes from its authority and a missing member FAILS:
  PHASES from the `poPhase` iota walked to the `poPhaseCount` sentinel
  (`TestPOCreate_EveryPhaseIsSwept`; a phase where genuinely no key acts is
  recorded in `poPhasesWithoutKeys`, so absent and empty are different states);
  LIST screens from `Workspaces()` (`listBarSurfaces`, with
  `listBarSurfacesOffTree` for the ones the nav tree cannot reach and
  `TestList_EveryWorkspaceListIsSwept` to prove the derivation reaches them);
  the screen's STATE from `reflect.TypeOf(PurchaseOrderCreateScreen{})`, every
  field either in `poStateFingerprinted` or in `poStateDeclined` WITH A REASON
  (`pending` and `errMsg` had fallen out of the fingerprint, so no review-phase
  arm could be judged at all).
  KEYS are the exception that proves it: no authority can be derived for them,
  so the answer is not to curate a vocabulary but to press the whole SPACE —
  every printable ASCII rune plus the named specials. There are two such spaces
  because the two halves of the app read their bars differently, and each
  catches the defect on ITS half: `poKeySpace`
  (`TestPOCreate_EveryPhaseNamesExactlyTheKeysThatWork`), which fails on `tab`
  the moment the supplier picker's alias is put back; and `listKeySpace`
  (`TestList_FooterNamesExactlyTheKeysThatWork`), which reports all eight dead
  claims — `N` on purchasing among them — the moment the `listShortcuts` handler
  is reverted. Both were verified by reverting, not asserted: the phase sweep
  builds only a `PurchaseOrderCreateScreen`, so it never could have caught `N`,
  and saying otherwise was itself a false invariant.
  A bar-token TABLE is the other half of a key space, and it must transcribe
  rather than interpret: `poBarKeyNames` (every columnar purchasing bar) and
  `listBarKeyNames` map each token to the keys it SPELLS and to no synonyms.
  Credit for a synonym is the sweep making the claim on the bar's behalf — the
  defect it exists to report, sitting inside the check. `↑↓` used to be read as
  naming `ctrl+p`/`ctrl+n`, `j/k` as naming the arrows, `pgup/pgdn` as naming
  `ctrl+u`/`ctrl+d` and `g/G` as naming `home`/`end`, so four bound-but-unnamed
  keys passed and any arm added behind them would have passed too. The bars now
  say what they bind (`j/k ↑↓ move`, `g/G home/end top/bottom`, and
  `listBarAliasKeys` for the second token in a footer segment), and the four emacs chords
  went the way the supplier picker's `tab` alias went: unbound, because a chord
  costs cells a 51-column bar does not have and names nothing an operator reads.
  `listKeySpace` replaced the last curated roster (`listAllBarKeys`), which was
  safe in one direction only — `listNamedKeys` still fails on a footer token it
  does not know, so a NAMED key could not be skipped, but a key bound in
  `ListScreen.Update` and absent from the roster was pressed in NEITHER
  direction, which is verbatim how `N` survived.
  `TestPOPickers_PaneNamesExactlyTheKeysThatWork` is the ONE roster still
  curated (`poPickerVocabulary`), and the guard that makes that safe is the
  phase sweep beside it: it earns its keep on the other axis — it walks the
  picker STATES (empty, failed, mid-flight) the phase sweep does not reach —
  while every PHASE it covers meets the whole key space there, so a key missing
  from its vocabulary is still pressed, in both directions, one file over.
  A KEY-NAME TRANSLATOR is a third place the same omission hides, and it is the
  one nobody thinks of: `poPickerKeyMsg` used to fall through to `KeyRunes` for
  a name it had not been taught, so pressing `"pgup"` typed p-g-u-p into a
  focused search box and the sweep reported the SCREEN as acting on a key its
  bar does not name. `poNamedKeyTypes` is complete over `poKeySpace()` and
  `TestPOCreate_EveryKeyNameTranslates` derives that from the space, so a key
  added to the space fails until the translator knows it. Making the bars
  `[]actionBarItem` retired the prose parser that used to be the fourth place —
  it read single-letter keys only at a segment START, because scanning prose for
  a bare `a` finds the article, and that reshaping is how `b` (bound on all
  three association pickers exactly as `esc` is, named by none of them) was
  finally caught.
  A DERIVED ROSTER IS ONE AXIS, AND A SWEEP HAS TWO. Walking an iota to its
  sentinel makes a PHASE impossible to forget and says nothing whatever about
  the STATES inside one, and it is the states a bar changes shape in — so a
  sweep can be rigorous along the axis it derives while being silent along the
  axis that carries the defect, which reads as coverage and is not. The cases a
  phase is reached through are therefore still a judgement (`receivePhaseCases`
  is the authority for receiving) and the bar is what says which ones are
  needed: a phase's cases must span every state its bar changes shape in,
  because that is exactly where the honesty rule can break. `Enter` on the
  receiving form is the instance, beside `N`, `tab` and `poPhaseSupplierSwitch`
  above: an empty quantity box and one holding `0` are different states of one
  phase, the bar named `Enter` in both, and in the second submit skipped the
  zero and could only refuse — no case had typed a zero, so nothing pressed it
  there.
- **A bar sweep presses the KEY SPACE, not the bar's own vocabulary.**
  `po_view_jde_test.go`'s `TestPOView_BarNamesExactlyTheKeysThatWork` used to
  walk `poAllBarKeys`, a roster of the tokens its bars happened to spell, so a
  key bound in a handler and absent from the roster was pressed in NEITHER
  direction — untested rather than passing, which is verbatim how `N` survived
  on the purchasing list. It walks `poKeySpace()` now, as the New PO phase sweep
  does, and `po_add_line_sweep_test.go` walks the same space over the add-line
  flow's phases (derived from the `poAddPhase` iota) with its state fingerprint
  derived by `reflect` over the screen struct.
  Two exceptions are RECORDED rather than omitted, which is the whole
  difference: `poFormNavAliases` (Tab / Shift-Tab ride alongside Up/Down on a
  sheet with fields, and roughly twenty columnar forms name that pair as
  `UP/DN=Fields` — naming the alias on three purchasing modals alone would make
  them disagree with every other form in the program), and `poAddSilentKeys` (a
  NAMED key resting against an edge it cannot move past, where the highlight or
  the absent `↑ more above` marker has already answered the press).
- **A FIELD form wraps; a LIST clamps, and the two are different keys reaching
  different handlers.** A short form has no edge worth defending: clamped, Down
  or Tab on the last of four fields blurs and re-focuses the same field — no
  state change, no note, and the bar naming `UP/DN` at that moment, which is
  rule 1. A LIST is the opposite: running off the bottom and reappearing at the
  top would land the cursor on a row that CLEARS a field, so `jdeClampPick` and
  `jdePageCursor` clamp on purpose. The shared cursor (`po_create.go`'s
  `setCursorRow` / `moveCursor`) is a LIST cursor, so the line form answers
  `up`/`down`/`tab`/`shift+tab` BEFORE `moveCursor` sees them and wraps modulo
  `lineFields()` (`focusNextLine`); paging stays clamped. `po_add_line.go`'s
  price rows wrap for the same reason, and the conversion routing the line form
  through the shared clamp is what briefly made the two purchasing field forms
  disagree — `TestPOLineForm_FieldNavigationWrapsAtBothEnds` is where that
  fails now. The wrap must not leak into the other phases: a fix applied inside
  `setCursorRow` would unclamp every list on the screen.
  It follows that a field form SHORT ENOUGH for its wrapping cursor to reach
  every row in a few presses is not OFFERED `PgUp`/`PgDn` at all: it has nothing
  to page, and `jdePageCursor` clamps on purpose (a page that jumped from the
  last row to the first would lose the operator's place), so leaving the pair on
  the shared path named a key that blurred and re-focused the SAME field
  wherever the fields outran the pane.
  LENGTH is the whole of that test, and the boundary is written down here rather
  than left to be inferred, because the over-general version of the sentence — "a
  field form is not offered PgUp/PgDn" — sounds right and would march the next
  reader, by rule 10, into STRIPPING a key a long form genuinely needs.
  `inventory_item_form.go` is the case on the other side of the line: its cursor
  WRAPS in exactly this sense (`moveCursor`: `(cursor + delta + n) % n`), and it
  offers `PgUp`/`PgDn` gated on `bodyScrolls` and says so in its own header
  comment, because it is roughly twenty rows and a wrapping cursor is no way to
  cross that. `category_form.go`, `maintenance_item_form.go` and
  `storage_slot_form.go` are the same shape. The New PO line form is three or
  four rows, which is the only reason the pair buys nothing there. `bodyPagesFor` is the ONE predicate that answers
  for both the bar and the arm, so the gate goes there and nowhere else;
  `TestPOLineForm_TheBarDoesNotOfferPagingItCannotDo` sweeps every drawable
  height, because the state only exists below 20 rows and `poPaneSizes` is
  {24, 30}.
  So the pair's reach on this screen is a QUALIFIED claim rather than a flat
  one, and it is worth stating that way: `PgUp`/`PgDn` page a scrolling body on
  the phases that draw a LIST, and are offered on the line form at NO height.
  Nothing was taken from the operator by that — before the conversion this
  screen bound neither key anywhere — so the pair is new on the phases that have
  a list to page and simply never offered on the one that does not.
- **One navigation vocabulary, three spellings of it, and only two of them are
  PROVEN.** `internal/tui/list_nav.go` is the vocabulary and carries the full
  note; read it before binding or naming a movement key anywhere.
  The set is `j/k ↑↓` move, `pgup/pgdn` page, `g/G home/end` top/bottom. The
  emacs chords are RETIRED — `ctrl+u`/`ctrl+d`/`ctrl+p`/`ctrl+n` move nothing
  anywhere — because no bar in the program ever SPELLED one and a 51-column
  footer cannot afford to teach a chord, which is the trade sc-po-create-hangs
  already made on `ListScreen`'s pager and the purchasing surfaces. It stayed
  unmade on twenty-one sibling files (twenty-four `case "ctrl+d", "pgdown":`
  pairs, forty-eight arms), so the same key paged the supplier list and did
  nothing on the inventory list the operator reached it from —
  `TestListNav_NoSurfaceBindsARetiredChord` PRESSES each chord on every fixture
  its swept sets can build (`jdePaneCases`, `listBarSurfaces` × every row count,
  `TextScroller`, and the cursor pickers of `listNavPickerCases`) and fails on
  one that moves. Its subtests are the authority on which sets there are; do not
  write the number down here, which is where it has drifted every time.
  A KEYSTROKE HAS TWO SPELLINGS IN THIS PACKAGE AND A DERIVATION OVER ONE OF THEM
  IS NOT A DERIVATION. `case "ctrl+n":` and `case tea.KeyCtrlN:` in a switch over
  `m.Type` bind the same key, and the first retirement, this sweep and the
  surface classifier all read STRING LITERALS ONLY — so `ctrl+n`/`ctrl+p` went on
  moving a cursor for two more rounds on the universal search palette
  (`search.go`), the e-paper bind picker (`epaper_panels.go`) and the
  location check-in lookup (`location_checkins.go`), while both this file and
  `listNavRetiredChords` said the chords moved nothing anywhere. All three are
  unbound now — the arrow each clause already bound is what their footers name,
  so nothing was taken from the operator — and `listNavCaseKey` reads both
  spellings, asking bubbletea itself what a `tea.Key*` constant spells rather than
  transcribing a table (`listNavSpellingIndex`, with `KeySpace` the one recorded
  exception, since its `String()` is the character and not the word).
  **A COMMIT THAT UNBINDS A LIVE KEY LISTS IT, FILE BY FILE AND KEY BY KEY** —
  the `BINDINGS CHANGED` record e1c1047 set the precedent for — because an
  operator's hands are the only place a retired chord is recorded, and a captain
  reading a subject line about a sweep cannot tell that a key they press every
  day stopped working. The three that went, in the order they matter to somebody
  who uses this program: `search.go` — the universal search palette (`ctrl+k`),
  `ctrl+n` and `ctrl+p` off the result cursor, which is the surface the captain
  actually drives and where those chords were muscle memory; `epaper_panels.go` —
  the e-paper bind picker, `ctrl+n` and `ctrl+p`; `location_checkins.go` — the
  location check-in lookup, `ctrl+n` and `ctrl+p`. Every one of those three
  `case` clauses ALREADY bound — and its footer already NAMED — the arrow that
  spells the same move (`↑/↓ move`), so nothing an operator was told about was
  taken away; what went is a chord no bar in the program ever spelled. The lesson
  is the one this area keeps teaching: a roster is only as complete as the
  alphabet it is derived over. It used to prove that from source SHAPE — a
  regex over `case "ctrl+d":` literals — which failed on a commented-out arm and
  passed a chord bound through a helper or a key-name map; behaviour answers both
  directions. It is POSITIVELY CONTROLLED (`listNavChordControls`): each case
  first presses the NAMED key spelling the same affordance and the sweep fails if
  no fixture in a set could be moved by it, because "ctrl+d changed nothing" is
  equally true of an empty list, a one-row list and a refused pane. What it
  asserts differs by set ON PURPOSE, and that is a fact about the surfaces:
  bubbles binds all four chords for LINE EDITING on a focused textinput, so on a
  columnar sheet with the caret in a box `ctrl+u` legitimately empties the box
  and the claim there is over `jdePlaceOf` alone; a `ListScreen` in browse mode
  holds no caret, so the clipped PANE is asserted too — which is what catches a
  window that scrolled without the cursor leaving its row, since `windowStart` is
  not in `jdePlaceOf`'s vocabulary. THE SCROLLER IS THE THIRD SET AND WAS THE
  HOLE the behavioural conversion opened: `scroll.go` is where two of the four
  chords were actually unbound, and neither of the other sets can reach it — no
  `TextScroller` holder embeds `jdeScreen` or is a `*ListScreen`, and
  `jdePlaceOf` walks the int fields of the SCREEN, so an offset nested inside a
  scroller value is invisible to it even if one did. Restoring
  `case "ctrl+d", "pgdown":` in `Handle` failed nothing at all, which made the
  conversion WEAKER than the regex it replaced on the one file the retirement
  touched. It asserts the OFFSET and the bool `Handle` returns, since a chord
  answered `true` is a keystroke every sheet holding one swallows on behalf of a
  binding that is gone. A prose-bar surface is PRESSED wherever a test can build
  one — `TextScroller` and the cursor pickers are values the sweep constructs
  directly — and a surface no press reaches is classified rather than claimed about
  (`TestListNav_EverySurfaceThatBindsNavigationIsSweptOrExcused`, which is a
  COVERAGE guard over the source, asserts nothing about any key, and cannot see
  a retired chord at all since it collects only what `listNavBinds` accepts).
  A TEST THAT DRIVES A RETIRED CHORD STOPS TESTING ANYTHING, and the retirement
  left three behind: `TestStorageSlots_PagedownClampsOnEmpty`,
  `TestAssetProblems_EmptyFilterCursor` and
  `TestLocationProblems_EmptyFilterCursor` each pressed `ctrl+d` at a `pgdown`
  arm, so after the retirement the arm they exist to enter was never entered and
  `cursor >= 0` passed for the reason it would have passed with the arm deleted.
  They press `pgdown` now and assert the cursor's exact resting place rather than
  its sign, because "not negative" is equally true of a screen on which nothing
  ran. Whenever a key is retired, grep the tests for it in BOTH spellings
  (`"ctrl+d"` and `tea.KeyCtrlD`) — the vacuity is silent in exactly the way the
  retirement is.
  THE COLUMNAR LAYER SPELLS THE SAME AFFORDANCES AS TOKENS (`UP/DN`,
  `PgUp/PgDn`, `Home/End`) AND BINDS NO LETTER, and that is a fact about the
  surface rather than drift: a columnar picker's filter box is always live, so a
  bare `j` is a character in the query. One vocabulary, two spellings, each bar
  honest about its own.
  WHERE THE RULE IS PROVEN is the part to keep straight, because the sentence
  is easy to over-claim. Two behavioural sweeps hold it, each over the half of
  the app whose bar is a machine-readable RECORD:
  `TestJDEForm_EveryMovementTokenIsNamedExactlyWhereItMoves` over every type
  embedding `jdeScreen`, at every width and drawable height; and
  `TestList_FooterNamesExactlyTheKeysThatWork` plus
  `TestList_TheSearchOverlayNamesExactlyTheKeysThatWork` over every `*ListScreen`
  the nav tree reaches, at every count in `listRowCases` — EMPTY, ONE ROW and
  MANY. The row counts are the axis those sweeps were blind on: every fixture
  carried eight rows, so a footer that named `j/k ↑↓ move · pgup/pgdn page ·
  g/G home/end top/bottom` as an unconditional literal was only ever pressed
  where it was true.
  WHERE IT IS NOT PROVEN, said plainly because a claim no check delivers is
  worse than no claim: every receiver `listNavUnsweptReceivers` records — that
  map is the roster and the authority on how many there are, and a count
  restated here is the one part of the derivation that cannot be derived —
  writes its bar as a muted literal
  straight into a `strings.Builder` inside `View`. `TextScroller` is the shape of
  it at its clearest — one handler shared by every detail sheet
  `listNavDelegatingReceivers` finds, whose footers
  disagree about which of its keys to name. There is no record to read,
  so no sweep can press keys against it, and a typical one reads `j/k move · n
  new · E/enter edit · x delete · r refresh · esc back` while binding the arrows,
  `g/G`, `home/end` and `pgup/pgdn` too — and it is ALREADY past the 51 cells the
  pane gives, so naming the rest would make it less readable, not more ("a bar
  the operator cannot read is not honest, it is absent"). Closing it means giving
  each of those screens the folded footer and row budget `ListScreen` already has
  (`footerRows`), a conversion of the same shape as sc-jde-lift.
  WHAT IS GUARANTEED FOR THEM INSTEAD is that the SET cannot grow in silence:
  `TestListNav_EverySurfaceThatBindsNavigationIsSweptOrExcused` parses the
  package, classifies EVERY receiver that binds a navigation keystroke as swept
  columnar, swept `ListScreen`, or recorded in `listNavUnsweptReceivers` WITH A
  REASON, and fails in all three directions — unclassified, stale, and excusing
  something that binds nothing any more. Per RECEIVER and not per file, because
  `category_form.go` holds `CategoryFormScreen` (columnar, swept) beside
  `CategoryListScreen` (prose, not), and a file-level answer excuses the second
  on the strength of the first.
  A `case "j", "down":` IS NOT THE ONLY WAY TO BIND ONE, and reading only for
  those was a hole in the DERIVATION rather than in the app: a screen that holds
  a `TextScroller` gets j/k, the arrows, pgup/pgdn and g/G/home/end from
  `Handle` without spelling a key, so seven of them were classified only
  transitively through the `TextScroller` entry and a new one could have joined
  the app appearing in no class at all. `listNavDelegatingReceivers` reads the
  STRUCT FIELDS for that — a field type is what `go/parser` can answer without
  `go/types`, and there is no way to hold a scroller and not hand it the
  keyboard. Whenever a shared handler grows that owns movement keys, the
  derivation needs the same treatment or it goes quietly blind to its callers.
- **An empty list is a STATE, and on `ListScreen` it used to be the one state
  that drew no bar at all.** `bodyView` returned `"No rows."` and nothing else
  while `s`, `r`, `n`, `f`, `/` and every sibling-surface letter worked — the
  bar's contract inverted on the state where "there is nothing here, now what?"
  is the operator's actual question and the answer is a key, which is standing
  rule 11's dead end on top of the omission. It draws `footerHint()` now, the
  same method the loaded list draws, so what it names there is what is true
  there. The empty FILTERED view keeps its fact ("no rows in the \"draft\"
  view") and lost its `press f to cycle the filter` clause, because the footer
  under it names `f filter` and ONE surface names a key.
  TWO THRESHOLDS, not one, and `footerHint` keeps them apart: the movement
  segments need a SECOND row (`listNavMoves`), `enter open` needs ONE — opening
  the row you are on is not moving to another one. The search overlay's bar
  (`searchBarHint`) keeps the same pair; its ceiling `listSearchBarHint` is what
  `listBodyLines` budgets against, because a body budget that moved with the
  live result count would make the list jump under the operator's hands while
  they type.
  DRAWING THE FOOTER THERE MOVED ONE VIOLATION RATHER THAN REMOVING IT, and
  that is the half worth remembering: `s sort` re-orders locally and its ONLY
  visible product is the `Sort: … · N rows` header, which the empty branch did
  not draw — so the key went from "acts and is not named" to "is named and
  cannot be seen to act", which is the same rule broken from the other side.
  `headerLine` is drawn in BOTH branches now (`listBodyLines` had always
  reserved the row), so an empty list states how it is sorted and how many rows
  that comes to, and `s` has somewhere to show.
  THE SWEEP MEASURES THE CLIPPED PANE, NOT A STATE FINGERPRINT, which is what
  found it: `listKeyEffectAt` compared `listBarState` — which carries `s.sort` —
  so a key that moved a number nothing draws read as working. Standing rule 1 is
  about a change the OPERATOR can distinguish, and only the rendered pane can
  answer that.
  A FOOTER DRAWN OUTSIDE THE ROW BUDGET IS A FOOTER `clampToBox` TAKES, and both
  branches were doing it. They budgeted against `screenBodyHeight`, which floors
  at four and is therefore a LIE below a terminal height of ten (`layout.go` says
  so in as many words), and `listBodyLines` then floored its own answer at two
  rows the pane did not have — so the assembled pane ran over and the drop is
  from the BOTTOM, where the bar is. At 80 columns the purchase-order list lost
  `· N new PO · Q pending reorders` at height 11 empty and 14 loaded, and by
  height 10 the whole footer was gone: the bar-less pane the empty-list work
  above exists to remove, restored by geometry. `paneRows` reads `screenBodyRows`
  now, the marker rows are reserved only where the rows really outrun the body
  (`listOverflows`), and the body floors at the TALLEST ROW rather than at one
  line — `rowsFittingFrom` will not return an empty window, so a one-line floor
  hands back a two-line row and the overflow is exactly its extra line.
  THE ↑/↓ MARKERS COST A PAIR OF ROWS OR ONE SHARED ROW, and `markerRows` is the
  single expression the budget, the refusal and the renderer all read. Where the
  pane can afford one apiece they are drawn in their places as they always were;
  where it can only afford one, `listMarkerLine` puts both facts on that row and
  the refusal comes down a terminal row with it. The height that happens at is an
  OUTPUT of the footer's fold and the tallest row, so derive it rather than
  looking for it written down.
  ALL THREE MARKER ROWS ARE BOUNDED BY THAT ONE BUILDER, and it gives ground in
  a stated order rather than being clipped: the PROSE shortens, the arrows and
  the COUNT never do, and where even the shortest form will not fit the count is
  DROPPED and the row marked. Assembled at full length and clipped, the row read
  `  ↑ more above · ↓ 1` at width 49 over twelve rows below — a cut number does
  not read as a shortened fact but as a different one, the price column's
  `@ 3.50` drawn as `@ 3.` on the row whose whole job is to say how much of the
  list is out of sight. The two per-marker sites used to write their own
  unbounded literals beside the bounded shared one, so
  `TestList_EveryMarkerRowOnThePaneFitsIt` asserts every drawn marker row is one
  `listMarkerLine` could have produced at that pane.
  Reserving one WITHOUT the shared row is the version to not reach for, and its
  reasoning sounds right: `↑ more above` needs `windowStart > 0`, which the pane
  does not open in. It is true of the opening state and false of the next
  keypress — at the boundary the body is exactly one row, so the first `j`
  scrolls, both markers apply and there is nothing left for the second to come
  out of, the pane overruns and `clampToBox` takes the bar off the bottom.
  Re-asking the refusal after the scroll is worse: the frame flips to the notice
  mid-scroll with the movement keys held, which is a dead end.
  AN EMPTY LIST NEEDS FEWER ROWS THAN THE SAME LIST ONCE ROWS ARRIVE — no
  markers, a shorter footer, a one-line floor — so a list can be drawn while
  empty and refuse when the rows land at the same size. That asymmetry is
  inherent (an empty pane genuinely is smaller) and is not papered over.
  WHERE EVEN THAT WILL NOT FIT THE PANE IS REFUSED, not mutilated: `paneDrawn` is
  the one predicate every reader asks, so the notice's claims and the keys behind
  them are one expression; grep it rather than trusting a list of readers written
  here, which has gone stale every time one has been written — including in the
  commit that added this warning. It is asked in `View` and not in `bodyView` so the
  notice REPLACES the frame rather than being drawn beneath part of it. And
  `listTooShort` draws a bounded notice naming the height needed in TERMINAL rows
  — a height that ACTUALLY DRAWS when the operator resizes to it. That holds at
  the FIXED POINT and NOT by `needRows` being monotone, which it is not once
  `markerRows` can grow from one row to two: a refused pane has a marker slack of
  zero or less, so it reserves one, and the height that buys is a slack of
  exactly one, which is what it still reserves there.
  THE SEARCH OVERLAY IS EXEMPT, AND THAT IS A DECISION PAID FOR FOUR TIMES OVER.
  Its first exemption rested on a false premise — that the overlay "pins its bar
  to the TOP of the pane, where clampToBox cannot reach it", when clampToBox
  drops from the BOTTOM, so at 80x7 the pane really did keep the input line
  alone: no bar, no rows, no notice, every key it names still live. The premise
  was false and the conclusion was right, and the reason is the one thing to
  carry forward: **THE OVERLAY OWNS THE KEYBOARD** (`WantsRawInput` is true
  whenever it is open) **AND THIS REFUSAL IS BUILT FOR A FRAME THAT OWNS
  NOTHING** — it draws over the screen, holds the keys whose product it would
  hide, and lets Root's global layer answer the rest. Bringing a keyboard-owning
  surface inside it means re-deriving EVERY predicate about who owns which key,
  at once, and the attempt produced FOUR separate defects from that one root,
  in the order they surfaced:
  (a) KEYS ACTING UNSEEN. `Update` dispatches to `updateSearch` BEFORE the
  refusal's key gate, so the notice drew over the overlay while `up`/`down`
  walked the cursor, `enter` opened an invisible row and every rune fired a
  backend search — the pane saying moving does nothing while it did.
  (b) THE BACK-STACK MISLABEL. Narrowing `WantsRawInput` to exclude a refused
  pane moved `Root.recordHistory` with it, because ONE PREDICATE WAS ANSWERING
  TWO QUESTIONS; a searching list got recorded, and `esc` brought it back with
  its query and `N match(es)` drawn over a reloaded whole catalogue — rule 3
  broken by a number rather than by a silence.
  (c) THE EXEMPTION REASON FALSIFIED. What was left was justified as "the states
  with no bar to cut", which stopped being true the moment a failed search left
  `loadErr` set with `searching` still true: at height 7 the pane kept the input
  line alone, bar-less, reached through the exemption rather than the budget.
  (d) `ctrl+k`'S SECOND MEANING. With the screen no longer owning the keyboard on
  a refused pane, `ctrl+k` stopped being bubbles' delete-to-end-of-line and
  became "leave for the search palette" — and since a searching screen skips the
  back-stack, the operator's typed query was gone unrecoverably. One keystroke
  whose meaning depended on terminal height.
  THE RULE, not four anecdotes: a surface that OWNS THE KEYBOARD cannot be
  brought inside a refusal that assumes the frame owns nothing, because every
  predicate about key ownership then has to be re-derived at once. And the reason
  to REVERT rather than narrow a fifth time: four separate collisions from one
  root is the signal that the root is wrong.
  `TestList_TheSearchOverlayBehavesTheSameAtEveryDrawablePane` is the invariant
  now — at every pane Root draws, the overlay takes every keystroke, stays off
  the back-stack, draws no refusal, and answers each key exactly as it does at
  the largest pane — so a fifth attempt fails rather than shipping.
  WHAT THE REVERT RESTORED is KEY ROUTING and the refusal EXEMPTION, and that is
  the claim the check above delivers — not a general "the overlay is untouched",
  which would be a maintained list of differences against a base commit and is
  the shape this section exists to remove. `git diff` against the base is the
  authority on the rest; two things are worth knowing because a reader will
  otherwise mistake them for oversights. `searchBarHint` names the overlay's keys
  conditionally, which changes the LEGEND and routes no key differently. And the
  overlay's BODY BUDGET moved with the shared helpers rather than with the
  refusal — it reads `screenBodyRows` and `markerRows` now, where base read the
  floored `screenBodyHeight` and reserved the marker PAIR unconditionally — so a
  short-rowed result set can show more rows than base did, and where the pane can
  afford only one marker row it draws the shared one. That is deliberate: base
  budgeted against a height `layout.go` documents as a lie below a terminal
  height of 10. Both bars fold at `listPaneCells` rather than the fixed 51: a
  fold is safe at 51 only while the pane HAS 51 cells, and at width 45 it has 16.
  THE HELD SET IS DERIVED FROM WHAT EACH KEY'S PRODUCT IS, and there is exactly
  one because there is exactly one refused pane. Read `listRefusedHoldsKey` for
  the members rather than a roster restated here — restating it is what has
  drifted every time — and what is worth knowing is the RULE that chooses them:
  a key is held when its whole product is INVISIBLE on that pane, so declining
  destroys nothing. A movement key's product is the POSITION, so `end` on
  a refused pane would walk the cursor to the bottom of a list nobody can see
  and declining it destroys nothing. On BROWSE `s` qualifies too: it re-orders
  locally, its only visible product is `headerLine`, which the refusal does not
  draw, and `needRows` is invariant under re-ordering — so the pane came back
  byte for byte, standing rule 1 broken by the refusal itself. `r` and `f` do
  not: both set `loading` and redraw as `Loading…`. Nor do `n` and the uppercase
  shortcuts, which LEAVE — the operator's way out of a pane too short to work
  in, and the reason not to widen either gate to them.
  `enter` IS HELD, and the reason is the same one: the refusal draws no rows and
  no highlight, and the row under the cursor MOVES while the pane is refused —
  a filter cycle reloads and reseats it — so enter opens a row nobody chose,
  which is worse than opening none, and the row is still there when the terminal
  grows back. Enter was never the way out; `esc` is what the notice names, and
  `n` and the uppercase shortcuts still leave.
  THE BACK-STACK IS A DIFFERENT QUESTION FROM THE KEYBOARD, and asking one
  predicate both is how a wrong label reached an operator. `Root.recordHistory`
  read `WantsRawInput` as a proxy for "is this screen transient"; narrowing the
  key-routing half to exclude a refused pane moved the history half with it, so
  a SEARCHING list navigated away from at a short height was pushed — and `esc`
  popped it, `popHistory` re-Init'd, the plain loader replaced the rows with the
  whole catalogue, and the overlay went on drawing the query and an `N match(es)`
  count over them. A filtered label on unfiltered rows is rule 3 broken by a
  number rather than by a silence. `BackStackScreen` (`route.go`) is the history
  question asked directly and `ListScreen.SkipsBackStack` reads `searching`
  alone; the raw-input test survives as the FALLBACK for the forms and confirms
  whose transience and whose keyboard ownership really are one state.
  THE NOTICE'S PROMISE IS SCOPED TO WHILE THE NOTICE IS UP, and it has to be:
  `paneDrawn` answers TRUE while a load is out, so `r` on a refused pane replaces
  the notice with the working line and `G` then walks the cursor against rows
  nobody can see. The first wording said the keys were held "until it fits",
  which that sequence falsifies. The keys-act-invisibly-during-a-load half is
  pre-existing and belongs to every list state rather than to the refusal — what
  was new was a sentence claiming otherwise, and the sentence is what gave.
  THE NOTICE IS BOUNDED AGAINST THE LIVE PANE IN BOTH AXES, and the width half
  was got wrong first: it folded and marked against a fixed `pickerPaneWidth` on
  a screen that recorded only the terminal HEIGHT, so at 60 columns it drew
  `Too short: needs 16 rows, has 1` — the operator asked to act on a number that
  is not the one the code computed, unmarked, with `StyleMuted`'s closing reset
  clipped off the end. `ListScreen` keeps `terminalWidth` now and `listPaneCells`
  reads `screenBodyCells` — the UNFLOORED width, added to `layout.go` for the
  reason `screenBodyRows` was: `screenBodyWidth`'s floor of 20 is four cells more
  than Root draws at width 45, and a bound that spends cells the pane does not
  have is not a bound.
  WHATEVER MUST SURVIVE MUST LEAD, once more, and here it decides the WORDING.
  On a REFUSAL the load-bearing clause is the WAY OUT, so `listTooShortWayOut`
  leads in its own fold segment, then the height to RESIZE TO, then the height
  the operator already HAS — a trim on either axis takes the tail and can never
  leave a WRONG number standing. The single-segment version folded on spaces
  into `Too short:` and scattered the figure across lines a short pane drops.
  THAT CLAUSE CARRIES THE RULE AND ITS EXCEPTION TOGETHER, and it is a claim
  about the LEGEND rather than about what acts — the distinction `jdeTooShort`
  already makes. Split across sentences, rule-first puts the denial on the line
  that leads and the way out on the line a one-row pane drops; way-out-first
  leaves a pane naming a key above a sentence denying that any key is named. In
  one clause the order is rule then exception AND the way out still leads. A
  wording asserting that no key WORKS would be false of every key
  `listRefusedHoldsKey` does NOT hold, which is why the claim is about the
  LEGEND: what is true whatever stays bound is that the action bar is not drawn.
  It also has to fit the 16
  cells width 45 gives, or it folds into a first line that denies without
  naming — check any rewording against that budget, which the sweeps do.
  ESC IS NAMED BECAUSE ESC WORKS THERE, and it is PRESSED rather than read off
  Root's switch (`TestList_ARefusedPaneNamesAKeyThatReallyLeaves`, through a real
  Root, with the back-stack both empty and loaded): a refused list is never
  `searching` — the overlay is exempt — so `WantsRawInput` is false and
  `HandlesKey` never claims `esc`, and the key reaches the global back step.
  It is the one key a frame that names
  none may name, the trade `jdeTooShort` already makes — the way out of a pane
  too short to work in must stay open or the refusal is one nobody can act on —
  and it does not soften the held-keys sentence beside it, because esc does not
  act ON the list, it leaves it.
  WHERE BOTH WILL NOT FIT, THE HEIGHT IS WHAT GIVES, and the state is narrow: a
  ONE-ROW pane (terminal height 7, since `screenBodyRows` is height − 6) keeps
  only the first folded line, and whether that line still holds the figure
  depends on the width — 51 cells keeps it, the 16 that width 45 gives does not,
  so only the way-out clause is drawn there, marked. From two rows up both are on the
  pane at every drawable width, which is why the sweep asks the two claims at
  different scopes: the way out at every drawable pane, the figure wherever
  `listPaneRows` is more than one.
  THE HEIGHTS ARE DERIVED FROM ROOT'S OWN GATE, and that is why nothing reported
  any of this: the legibility loops in `list_bar_honesty_test.go` walked the
  hand-picked pair {24, 30}, and every failing height was below both — two
  hand-picked heights being the same mistake on the vertical axis that three
  hand-picked widths was on the horizontal one. THE RULE THAT REPLACED THEM,
  stated as a rule because a sentence claiming EVERY loop has been converted is
  a universal over a set that grows whenever a loop is added, and no behavioural
  check can deliver it: a legibility loop walks `jdePaneHeights()`, and it
  measures through `listRootLines` rather than `screenBodyHeight`, which floors
  at four rows and is therefore a LIE below a terminal height of 10. Where a
  claim is a PRESENCE that a short pane genuinely defeats, the loop is scoped by
  a boundary DERIVED from what the frame really draws — never by a height set
  that avoids the state — and it counts BOTH sides of that boundary and fails if
  either was never reached, or the scoping is a way of asserting nothing. The
  search OVERLAY is the worked example and it carries both shapes: its BAR is
  scoped, since the overlay is exempt from the refusal and a short pane keeps
  part of it or none (`listOverlayBarFits`), while its BOX is not, since the box
  is the head's FIRST row and `clampToBox` drops from the BOTTOM. A claim of
  ABSENCE — the browse footer being gone while the box owns the keyboard — needs
  no boundary at any height.
  `TestList_AShortPaneRefusesRatherThanCuttingTheFooter` /
  `TestList_ARefusedPaneKeepsTheOperatorsPlace` /
  `TestList_ARefusedPaneHoldsTheKeysThatCouldNotBeSeenToAct` hold the refusal's
  own honesty — bounded in both axes, naming a height that works, movement and
  sort held, with the control asserted so a fixture that could not move for
  unrelated reasons fails instead of passing. The WIDTH axis has its own sweep
  over Root's drawable widths for the same reason
  (`TestList_ARefusedPaneNamesAHeightTheTerminalCannotClip`), and it asserts
  three things because the lead cannot speak for all of them: that the WAY OUT
  reaches the clipped pane whole at every drawable pane, that the height figure
  does wherever the pane has more than one row (the threshold asked of
  `listPaneRows`, not written down), and that no line of the notice overruns the
  pane at all — a styled line `clampToBox` truncates loses its closing SGR reset
  into everything drawn after it.
- **A list's uppercase keys come from `listShortcuts` (`list.go`), never from a
  hint literal.** The footer and the handler read that one table; the previous
  shape appended the words to a hint string and left the key to a global
  accelerator in `app.go` that phase 3 had deleted. Lowercase acts on the list,
  uppercase opens a sibling surface of the same workspace (which is also a
  `workspaceSurfaces` row in `route.go`).
- **A REPORT TABLE fits its pane, and the give-order is written down.**
  `internal/tui/report_table.go` (`fitReportTable`, `layoutRows`) is the layer
  behind every tabbed report — inventory, purchasing, assets, reorders
  analytics, ForgeKey fleet — and it is NOT on the columnar `jde_form.go` layer,
  which is why its pane accessors are `reportPaneRows` / `reportPaneCells`:
  `paneRows` is the columnar layer's own and marked `jde:layer-only`, exactly as
  `ListScreen` names its pair `listPaneRows` / `listPaneCells`.
  Horizontally, every part of a row is one of two things — an IDENTIFIER (a
  left-aligned column) that abbreviates down to `reportNameFloor` with an
  ellipsis, or a FACT (a right-aligned column) that NEVER gives, header
  included, so no figure is ever cut. Where even that will not fit, whole
  columns are DROPPED from the right: the header carries `reportDropMark` and
  the note under the table NAMES them, the mark leading and the names following
  because a short pane takes the note first.
  Vertically, `layoutRows` gives ground in a stated order: the yardstick legend
  and the action bar never give, the body floors at one row, and the block under
  the table gives from the END. THE ACTION BAR HALF OF THAT HOLDS ON EVERY
  BRANCH `View` DRAWS, and it is `frameRows` that spends it: only the TABLE
  branch used to consult a budget at all, so the loading, failed and empty
  frames were laid out against nothing — the failed one against a flat six-row
  constant, which at 80 columns needed a sixteen-row terminal where the frame it
  replaced needed eleven. An operator whose load had just FAILED, on an
  11-to-15-row terminal, read six lines of gateway HTML with no named way off
  the screen. Those frames give up their OWN BLOCK now, from the end; on the
  failed one what a cut leaves is always the first line of the error AND the row
  saying the rest went (`reportErrMinRows`), because the mark is what tells an
  operator they are not reading the whole failure.
  THE MARKER ROW IS RESERVED WHERE THE BLOCK BELOW
  IS CLAMPED (`rowBudget`), not taken out of the body afterwards: taken after,
  the body's floor handed back a row already spent and the frame assembled one
  row more than the pane had whenever the block below squeezed the body to one —
  at 80x20 on the reorders Supplier perf tab what `clampToBox` then took was the
  footer's last fold, `r refresh · esc back`, leaving no named way off the
  screen. Where even the floor will not fit the frame still runs over; that
  band is `frameFits`'s own answer, asked per branch, and is left as it is
  rather than half-converted into a refusal, safe because the legend LEADS, so a
  figure is never drawn without it at any height. Do NOT write the band down as
  a height:
  it moves with every wording on the frame and it grows TALLER as the terminal
  gets NARROWER, because the legend, the notes and the footer then fold onto
  more rows. `TestReportTable_TheScreenAssemblesNoMoreRowsThanThePaneHas` walks
  both sides of it on EVERY branch — loading, failed, empty and loaded — at
  every pane Root draws, and
  `TestReportTable_TheScreenAssemblesNothingThePaneCannotHold` is its width
  counterpart; both measure what the screen HANDS OVER, because after
  `clampToBox` no frame can be too big — the truncation has already happened.
  What this replaced, measured at 80 columns: a 75% on-time rate drawn as `75`,
  a 33.3% late rate as `33.`, `$12,345.67` as `$12,3`, every numeric column off
  the pane under a header line reading `Or`, notes and the action bar cut
  mid-word, and a tab bar that showed the first three tabs and no highlight at
  all while the operator stood on the sixth.
  `internal/tui/report_yardstick_test.go` sweeps every report screen — the
  roster DERIVED from the package source — at every width and height Root draws.
  Its fixtures carry a full-length OMS supplier name and a distinct wide figure
  per column ON PURPOSE: every report fixture in this package used to write
  `Acme` and `Bolt`, so no test had ever rendered a report row at the length OMS
  really serves, which is how the whole class survived.
- **80 columns leaves the pane 51.** `screenBodyWidth(80)` is
  `80 - navColumnWidth(24) - 1 - padding(4)` = **51**, and the action bar gets 49
  of them. That is the number every columnar layout has to be checked against,
  and it is small enough that a hint, a six-column grid or a long value will not
  fit without help — `jdeFitRow`, `poFitLineGrid` and `jdeCaveatLines` in
  `jde_form.go` / `po_detail.go` are the three folds that exist for it.
  51 is the width that must HOLD, not the width to render as though we had, and
  which of the two a bound is depends on what it does when it bites. FOLDING
  narrow costs an extra line and loses nothing, so the folders (`pickerWrap` and
  everything through it, `pane_text.go`) stay on `pickerPaneWidth`. CLIPPING
  narrow DESTROYS the tail, so every clip is measured against the pane the
  terminal really gives: `jdeScreen.bodyWidth()`, or a screen's own `paneWidth()`
  wrapper where an UNSIZED screen still has to clip against something
  (`po_create.go`, `po_add_line.go`). Clipped to a fixed 51, a 120-column
  terminal drew every picker row abbreviated with forty columns of pane left
  blank, on the rows an operator picks FROM.
  `TestPOPickers_AWideTerminalDrawsTheWholeRow` holds both directions at 80, 100
  and 120: nothing overflows at the narrowest, and the same row draws WIDER at
  the widest. A test helper that hard-codes 80 cannot see the second half, which
  is why `poAssertFits` and `poPaneLinesAt` clip against `poFrameWidth(screen)`.
  **A columnar fixture needs BOTH dimensions.** The frame pins its bar to the
  bottom of the pane and draws the rule at the pane's WIDTH, so a fixture given
  a height and no width draws a 72-column bar (the layer's unsized fallback)
  into a 51-column pane. `s.Update(tea.WindowSizeMsg{...})`, never
  `s.terminalHeight = h`.
- **Check the CLIPPED render.** `clampToBox` truncates in `Root.View()`, not in
  the screen, so a test that reads `screen.View()` passes while the terminal
  shows a cut line. Assert against `Root.View()` at 80/100/120 —
  `internal/tui/po_view_jde_test.go` is the pattern (and `poSeenWhileScrolling`
  for a body taller than the pane).
- **A typed row is handed to the layer as a BOX, never as a string.** Build it
  with `jdeField{Kind: jdeText, Input: &box}` and pass the pane to
  `renderJDEField` / `AddFields`; `jdeFitInputValue` then bounds the box, keeps
  the caret inside the field and leaves the fill for the reverse-video
  highlight. A row that renders its own `textinput.View()` is unbounded, and
  bubbles gives a box no scrolling window at all at `Width 0`, so the value walks
  off the pane and the caret goes with it. `jde_form.go` carries the full note;
  `TestJDEForm_EveryTextRowIsSizedByTheLayer` enforces it over the package source
  so a new sheet cannot opt out.
- **Colour and reverse video ARE testable — force the profile.** lipgloss strips
  every sequence when stdout is not a TTY, which is always in a test binary, so a
  lost highlight and a present one are byte-identical and a width assertion sees
  nothing. `withColorProfile(t, termenv.TrueColor)` turns them back on and
  `jdeCells` (`internal/tui/jde_cells_test.go`) decodes a frame into cells with
  their attributes. Any claim about what an operator SEES belongs there, not in a
  comment saying no test can catch it.
- **Viewing screens** use the read-only half of the columnar layer:
  `jdeScreen.frameScrolled` (scroll offset, not a cursor) and
  the wrapping bar every frame draws (a bar of a dozen order-level keys folds
  onto several rows rather than losing its tail). `internal/tui/po_detail.go` is
  the pilot for those, as `po_edit.go` is for forms.
- **A sheet may not answer "how many rows?", "does this scroll?", "is this
  drawn at all?" or "what goes on the status row?" itself.** All four are
  `jde_form.go`'s (`bodyAvailForBar`, `bodyScrollsForBar`, `frameDrawn`,
  `statusRow` / `fitStatus`), and the frames read the SAME functions, so a bar's
  claim that UP/DN or PgUp/PgDn move something cannot part company with the
  window that decides whether they do. Two of them lived as per-sheet copies
  until sc-jde-lift: roughly fifty copies of the scroll arithmetic in two shapes
  that disagreed at the edges — one had already been rewritten once for asking
  `ClampScroll`, which reserves the two indicator rows and so says "scrollable"
  two lines early — and a status bound applied on the three purchasing screens
  and on none of the other thirty-odd, which handed an unbounded OMS body to a
  row that cannot fold. `clampToBox` then cut it and took the closing SGR reset
  with it, colouring everything drawn afterwards.
  Two sweeps in `jde_lift_sweep_test.go` keep them there and BOTH were verified
  by reverting: `TestJDEForm_NoSheetAnswersTheScrollQuestionItself` (the
  forbidden set is derived from a `jde:layer-only` line in the layer's own doc
  comments, plus a second net on any `jdeLines.Len()` in a comparison) and
  `TestJDEForm_EveryStatusRowComesFromTheLayer` (the frame set AND the position
  of the `status` argument are read out of `jde_form.go`, so a frame variant
  added later is swept without anyone remembering it). Mark a new budget helper
  `jde:layer-only` at its declaration; a roster kept in the test is the
  hand-maintained list this project keeps being bitten by.
  Zero rows is not a licence to DISCARD the operator's place: `frameScrolled`
  hands its clamped offset back and both callers store it, so clamping against
  no rows answered 0 and a terminal briefly dragged short came back at the top
  of a long order pad. Nothing to clamp against means nothing to clamp, so the
  clamp is skipped and `ClampScroll` no longer carries a branch for a state it
  cannot be called in. That state is now reached only through the refusal below,
  and `TestJDEScroll_APaneTooShortToDrawTheBodyKeepsTheOperatorsPlace` DERIVES
  the height it happens at rather than naming one — it named 80x12, and 80x12
  stopped being that state the moment the budget stopped being floored, so the
  test would have gone on passing over a state it was no longer in.
  The status bound is measured in BOTH axes and in what is really drawn.
  `fitStatus` takes the MARK it is about to sit behind and reserves that — two
  cells for the error's `✗ ` and the storage warning's `! `, and nothing at all
  for the muted working line and the standing note, which used to be cut at 49
  on a pane with 51 to give. And it FLATTENS the message before measuring it: a
  multi-line body is inside the width on every line (nginx's 502 page is seven
  lines of at most 42 columns) and overflows the HEIGHT instead, so the frame
  ran six rows over and `clampToBox`, which drops from the bottom, took the
  whole action bar — every key on the screen unnamed at once. It is bounded by
  a forward pass (`cellPrefix`) before `fitCell` sees it, because `fitCell`
  falls back on `truncateVisible` and a flattened 20 KB gateway page through an
  O(n²) bound is the hang recorded further down this file.
- **What the layer assembles FITS THE PANE, and the order it gives ground in is
  written down.** `jdeBodyAvail` / `jdeFitHeader` / `bodyRowsForBar` are that
  order: the body gives rows first, down to ONE and no further; the pinned
  header gives up whatever that costs; the status row and the action bar never
  give. `bodyRowsForBar` used to FLOOR its budget at three rows, which does not
  create rows — it only makes the assembled frame claim rows the pane does not
  have — so whenever `screenBodyRows(H) < barRows+4` the frame ran over and
  `clampToBox`, which drops from the BOTTOM, took the action bar off it. On the
  purchase-order detail at 80 columns (a four-key-line bar) that is one key line
  gone at 80x14, three at 80x12 and the whole bar, rule and all, at 80x10 — with
  every one of those keys still working and nothing on the screen saying the
  legend was a fragment. `screenBodyHeight` floors at four for the same reason
  and so lies below a terminal height of 10, which is why the layer reads
  `screenBodyRows` (layout.go, the unfloored answer) and nothing else.
  When even that will not fit, the frame is REFUSED rather than mutilated:
  `jdeTooShort` draws a bounded two-sentence notice naming how tall a terminal
  the screen needs — a height DERIVED at the fixed point, so resizing to it
  really does draw the frame — and saying that the moving keys are HELD until it
  fits, which is true because they are (see the movement rule below). It is
  bounded in both axes, and where the HEIGHT bound bites the last row it keeps
  carries the ellipsis that says so: cut clean, "Moving keys are held until it
  fits, so you come" reads as finished advice.
  That sentence has now been wrong twice in opposite directions and both
  wordings are recorded in `jdeTooShort`: it first REASSURED ("they still
  work"), then said the keys act invisibly, which was honest and described a
  loss that was still happening. A bar with rows cut off it
  names some keys and hides the rest SILENTLY and the operator cannot tell
  which; naming none is the only honest alternative. Both floors are rule 1 — a
  keypress must change something the operator can see — and each covers a press
  the other does not: the BODY row carries the cursor and the focused box, the
  HEADER row carries the screen's answer to a press that DECLINED (the receiving
  form's note is the whole of "enter needs a quantity first"). At a budget of
  one they cannot both be had, so a budget of one with a header pinned is
  refused rather than resolved in favour of whichever defect.
  `jde_pane_fit_test.go` holds all of it over every screen at once, at 80/100/120
  and at every height Root will draw (derived from Root's own "terminal too
  short" gate, not written down). The SET of screens is derived too — every type
  embedding `jdeScreen`, read out of the package source — and
  `jdeScreenFixtures` fails on an omission, on a stale entry, and on a fixture
  left in its loading state, which renders one line of "Loading…", fits every
  pane and proves nothing.
- **EVERY frame WRAPS its bar, and movement is gated on DRAWABILITY.** These
  were one conversion (sc-refused-pane) because they are one signature change
  across some thirty sheets, and doing them separately would have touched all
  thirty twice.
  `frame` / `frameWithHeader` ARE `frameWrapped` now. The one-line
  `renderActionBar` they used to draw tightens its gutter and then lets the line
  run past the pane, so eleven form screens (MaintenanceItemFormScreen's bar is
  63 cells against 51) lost the tail of their legend at 80 columns — 95 (screen,
  width, height, row) violations, which is what
  `TestJDEForm_TheActionBarSurvivesEveryHeight` reports on both axes now.
  `bodyAvail` / `bodyScrolls` / `windowRows` are GONE: every sheet asks the
  `…ForBar` variant with the bar it is about to draw, and where that answer
  feeds the bar's own contents it passes the bar WITH the scroll keys on it,
  because the tallest bar is the fixed point.
  The gate is `jdeScreen.frameDrawn`, and the three primitives that carry it for
  a CURSOR are `moveRow` (a wrapping field cursor), `pickRow` (a clamping list
  cursor) and `pageRow` (a page). A scroll OFFSET has no fourth GATED primitive
  on purpose: its two questions are asked of different bars — the scroll answer
  of the CEILING bar, drawability of the bar really DRAWN — so a sheet that
  scrolls one spells the conjunction itself and names it (`po_detail`'s
  `sheetMoves` / `padMoves`). What IS shared is the key-to-offset MAPPING,
  `jdeScrollStep` — see the pane-that-says-there-is-more section below.
  **THE RULE IS ONE STATEMENT, NOT TWO: a movement key acts when the frame is
  DRAWN and — for a PAGE — when the body MOVES.** `pageRow` asks both, and it is
  the DEFAULT: a screen with no further condition on its pager reaches for it and
  gets the whole rule. A screen carrying a condition `pageRow` CANNOT EXPRESS
  asks the two directly and SAYS WHY AT THAT SITE, so the exception is
  self-evident where it occurs rather than tracked in a roster somewhere else —
  and the next such screen documents itself by following the rule instead of by
  being added to a list. The condition that recurs is a THIRD fact `pageRow`'s
  single bool cannot carry: "refused" and "nothing to page" come back as the same
  false, and several screens must answer them differently — silence on a refused
  pane, a decline note when the body simply does not move on a frame the operator
  can see. `po_create`'s `bodyPagesFor` and `receive_form`'s `qtyPagesFor` add a
  second on top: a page moves the CURSOR, so there must be another row to LAND
  on, which comes apart from "does the body overflow" in states they have by
  design.
  **THAT SECOND QUESTION IS THE LAYER'S TOO** — `bodyPagesForBar`, which is
  `count > 1 && bodyScrollsForBar`, where `count` is the NAVIGABLE ROW COUNT and
  not the body's line count. A `…Bar` wrapper that appends `PgUp/PgDn` asks it,
  or spells the same two conditions where it already had the count in hand
  (`service_status_screen` nests the entry inside `len(services) > 1`), and so
  does `pageRow` — so the bar's claim and the key behind it are one expression. It was three sheets' private knowledge and thirty sheets' blind
  spot, and the state it is about is the one a list SPENDS MOST OF ITS LIFE IN:
  an unopened kit list WAS a heading, its guidance and the trailing "(add a
  component)" row (the chrome rides the pinned header now — see the
  pane-that-says-there-is-more section), so at 80x11–17 the body outran the
  window while the add row was the only row a cursor could stand on — **the bar named the pair and a page
  moved nothing, on the default state of a new inventory item**. The packaging
  chain and the storage level list did the same at their own heights.
  A SWEEP THAT DERIVES ITS SCREENS STILL HAND-PICKS ITS STATES, and that is the
  axis this hid on: every list state in `jdeScreenStates` had been given several
  rows to stop the movement sweeps being vacuous — correct, and it put the
  one-row case out of reach of the sweep written to catch exactly this. The
  minimal variants (empty kit list, empty chain, no levels, one attachment) are
  there now, and they are INERT for the refused-pane sweep by construction, so
  they are recorded in `jdeInertCases` with that reason.
  `pageRow` takes TWO bars: drawability of the bar really
  DRAWN (`tooShort` is monotone in bar height, so a taller bar would decline a
  key at a height the frame IS drawn at), scrolling of the CEILING bar (naming
  the keys costs cells, cells fold the bar onto another row, and a folded bar
  leaves the body one row fewer, so the tallest bar is the fixed point). The
  ceiling is THREADED from the sheet, never synthesised by appending a generic
  `PgUp/PgDn` inside the layer: the label differs per screen — `Page`, `Unit`,
  `Last unit` — so the measured fold would differ from the real ceiling, and an
  approximate fixed point is not a fixed point. An UNSIZED terminal skips the
  scroll half rather than failing it: there is no window to overflow, and the
  layer's standing answer for no pane is "draw whole and let `clampToBox`
  decide", which is the same reason `frameDrawn` answers true there.
  The scroll half was the SHEETS' until it was the layer's, and the shape of
  that is worth keeping: every columnar sheet bound `pgup`/`pgdown`
  unconditionally in its key switch while naming the pair only when the body
  overflowed, so on any pane tall enough to hold the whole body the bar rightly
  said nothing and PgDn still walked the cursor to the last row — **3254 of 7102
  drawn (screen, width, height) triples**, with not one violation the other way.
  Two sheets then spelled the conjunction for themselves, which closed the class
  at two of thirty-two sites and is exactly how the ~50 per-sheet scroll copies
  sc-jde-lift had to unpick began: one that looked too small to be worth a shared
  function, with the same argument available to the next forty-nine. Both were
  deleted. `TestJDEForm_EveryMovementTokenIsNamedExactlyWhereItMoves` holds the
  BICONDITIONAL over `jdePaneCases` at every drawable pane — derived, so site
  thirty-three cannot reopen it — and it fails a bar that names the pair where a
  page moves nothing just as readily. It used to be a narrower sweep of its own
  over the paging pair alone; that run is gone because every claim it made this
  one makes, per token and therefore more strictly.
  THAT BICONDITIONAL NOW COVERS THE WHOLE MOVEMENT VOCABULARY, not the paging
  pair alone: `TestJDEForm_EveryMovementTokenIsNamedExactlyWhereItMoves` is the
  same sweep over every token in `jdeMoveTokens`, and the paging one is a call
  into it with the vocabulary narrowed. It is stated at TWO granularities on
  purpose, and getting that wrong makes it report screens that are honest — a
  token names a PAIR, so FORWARD it is asked per TOKEN (press both keys in
  sequence; the claim is that SOME key it spells moves, which is why a list EDGE
  stays silent), while REVERSE it is asked per KEY from the rest state, because
  "a key that acts must be named" cannot be answered about a pair. Two tokens
  can also spell one key — `PgUp/PgDn` and receiving's `PgUp` — so "is this key
  named" is asked of the UNION of the drawn tokens.
  BOTH SIDES ARE PROVED REACHABLE, and the `unnamed` counter is the one the
  generalisation dropped while its own comment went on claiming it — a vacuity
  guard that had itself gone vacuous, which is rule 8 in the most embarrassing
  place available. `named` says the sweep found bars spelling the token, `moves`
  that some key it spells moved, and `unnamed` that it found panes whose bar does
  NOT spell it, which is the only state the REVERSE implication can fire in.
  Without that last one a bar builder that started appending every movement token
  unconditionally would leave `movedKey && !namedKeys` unreachable, the forward
  half would still pass, and the sweep would report a biconditional it had only
  ever tested one side of.
  ALL THREE ARE COUNTED PER TOKEN, and that is what retired the separate paging
  sweep rather than a judgement that it was redundant. Aggregated, one popular
  token vouches for every other one — which is precisely why the paging pair used
  to need a SECOND walk of every case at every width and every drawable height to
  make the same claim about itself, in a package that has already hit `go test`'s
  600s per-package timeout once. Per token, this walk makes it for all of them
  and more strictly. Everything else the paging run asserted was already
  identical: its forward half ran over a subset of these tokens, and its reverse
  half was the same expression, since `PgUp/PgDn` and `PgUp` are the only tokens
  in `jdeMoveTokens` that spell `pgup` or `pgdown`.
  `moves` IS PER TOKEN AND NOT PER KEY, which is a fact about the vocabulary
  rather than a weakening: `home` never moves a cursor already resting at the
  top, so a per-key floor would fail on correct behaviour. A token names a PAIR
  and the forward half already claims only that SOME key it spells moves, so the
  counter is asked at the same granularity the assertion is.
  `UP/DN` IS CONDITIONAL NOW and `jdeRowMoves` (`count > 1`) is the one
  predicate: the BAR asks it through `jdePickBarWith` / `jdeMoveItem`, and the
  ARMS ask it in `pickRow` and `moveRow`, so the claim and the key behind it are
  one expression. It used to be unconditional in `jdePickBarWith` — the bar
  EVERY columnar picker draws — so a picker filtered to one option, or to the
  synthetic "(none)" row that survives a query nothing matches, named the pair
  while `jdeClampPick` handed the cursor straight back. A FIELD form was never
  an instance while it has two or more fields, because its cursor WRAPS; the
  same predicate covers the one-field case for nothing.
  Read `jde_form.go`'s "Movement" block before touching any of them; what is
  worth knowing here:
  - **The BAR and the HANDLER ask different questions and must go on asking
    different questions.** `bodyScrollsForBar` is "is there more than fits",
    which stays TRUE on a refused pane; the handler also needs "is anything
    drawn". Making `bodyScrollsForBar` answer false when refused looks like the
    same fix and is not: the bar is not drawn there and its only remaining job
    is to be MEASURED, so dropping its scroll keys shrinks it and
    `jdeTooShortRows` then names a height one row short of one that works. That
    defect has shipped once already (`jdeBodyAvail` carries it).
  - **A GATED MOVEMENT ARM answers with NOTHING — not even a decline note.**
    Its whole product WAS the position, so once the move is refused there is
    nothing left to report; and a note written there is not drawn now and IS
    drawn when the terminal grows back, answering a press the operator has
    moved on from. THE BOUNDARY IS WHAT THE KEY DID, NOT WHAT THE PANE IS: an
    arm that DECLINES AND ANSWERS — an empty picker list, a filter that matched
    nothing, `up` with one row to move through — is NOT gated and must not be.
    Its answer is the visible change rule 1 requires and it rides the surface
    #155 gave it, which the layer cannot trim; read after the pane grows back
    it is later than ideal and far better than a key that never speaks. Gating
    those would silence them at exactly the heights that work made them speak,
    so the silence is the rule for arms whose only product is a POSITION and
    for no others.
  - **TYPING is deliberately not gated**, and nor is `esc`. A movement key's
    whole effect is the position, so declining it PRESERVES what the operator
    had; a typed rune's effect is the value, and declining that would DISCARD
    input — including a scanner burst. A focused text box therefore still owns
    its own keys, its caret included, which is why the sweep reads the movement
    keys off the BAR rather than off a fixed list.
  - `TestJDEForm_ARefusedPaneKeepsTheOperatorsPlace` is the check and it is
    stated the way an operator would: drag it short, press things, drag it back,
    find what you left. It walks `jdePaneCases` (every columnar screen, derived),
    presses the movement keys THAT SCREEN'S BAR NAMES at any height, and asserts
    both the clipped frame and a position fingerprint reflected off the screen's
    own int fields (`cursor` / `scroll` / `focus` / `offset` in the name).
    `jdeInertCases` records, with a reason, every case where nothing moves at
    any height — absent and empty are different states.
  - **A reach loop in a test must be BOUNDED.** `for s.focused != row { down }`
    was the ordinary idiom and it turns a declined key into a HANG: the package
    then fails by timing out with whichever test was running named in the panic
    rather than the one that broke. `receiveWalkTo`, `receiveReachRow`,
    `poReachLineField` and `assignWalkTo` are the bounded replacements, and the
    two that run at swept heights walk at a tall pane and put the size back —
    which is the sequence an operator goes through anyway.
- **A body line that belongs to no navigable ROW is a line no key can reach.**
  `jdeLines.Window` anchors the window on the CURSOR's block, and a columnar
  sheet's cursor cannot go above its first row — up WRAPS to the last row, which
  moves the window further down, and `jdePageCursor` clamps at 0 — so anything
  added with `l.Add` (that is, tagged `jdeNoRow`) AHEAD of the first block is
  stranded the moment the body overflows, while the layer goes on drawing
  `↑ N more above` and counting it. The frame says there is content up there and
  every key the bar names refuses to fetch it. At the canonical 80x24 the
  receiving form lost its heading and the whole kit caveat that way — the
  sentence that stops "received 2" being read as two of the thing named on the
  line — and the conversion is what inverted which end is lost, since the
  pre-conversion form drew every line and `clampToBox` cut from the BOTTOM.
  Re-tagging the lead onto row 0 is NOT the fix and the arithmetic says why:
  Window keeps a block's START when the block will not fit, so lines placed
  ahead of the field push the FIELD off the pane instead (measured at 80x22 with
  one kit line, and `TestReceive_AShortPaneStillDrawsTheForm` fails on it). So
  what a row needs is drawn ON that row and AFTER its field
  (`receive_form.go`'s `addLineBlock`, whose sacrifice order is above), a
  separator travels with the block ABOVE
  it so a block never opens on a blank, and anything left over hangs off the
  last row. `TestReceive_NoBodyLineSitsWhereNoKeyCanReach` holds both halves —
  structurally, that no line falls outside a row, and behaviourally, that the
  body's first line is drawn at rest and comes back after the cursor has walked
  away and returned.
  A separator is the same rule at one row's scale and is the half that was got
  BACKWARDS first: it closes the block above it, never opens the one below,
  because Window keeps a block's START and a blank at the front is then the one
  line a short window draws — at 80x17 pressing Down drew a pane of two markers
  and a blank, naming nothing about the row the cursor had just reached.
  Where NO key moves a cursor the block has only one end to protect and the
  order inverts: `serialBody` draws the FIELD first and what identifies it
  after, and `qtyBody`'s nothing-receivable branch does the same, since a
  scanner firing into a box the operator cannot see is worse than a label they
  have to press nothing to lose. It is a RULE and not two cases: whichever body
  has one navigable row is in it, so applying it to the one that was reported
  leaves the other stranding its field a round later. A PINNED HEADER is the
  same rule with a DIFFERENT LEVER, and getting the lever wrong cost a round:
  nothing an operator presses brings back a row `jdeFitHeader` has trimmed, so a
  header does have one end to protect — but the end that must SURVIVE and the
  end that READS first are not the same end. Inverting the display (drawing the
  picker's filter box above its own title, the way `serialBody` draws a field
  above what identifies it) bought one row at one height by relaying out
  nineteen screens at every height, and it fixed nothing for the ORDER PAD,
  whose ⚠ omitted-lines warning had the identical problem one file over. So the
  two orders are decoupled: `jdeHeadRank` (`jdeHeadEssential` / `jdeHeadContext`
  / `jdeHeadDecorative`) travels with each header row, builders keep their
  natural layout, and `jdeFitHeader` gives ground BY RANK — most expendable
  first, within a rank from the END, output still in display order. Blank
  separators are forced decorative, which is the separator rule read from the
  other side.
  **A RANK DOES NOT REMOVE THE SIGNIFICANCE OF ORDER WITHIN A RANK**: ground is
  given from the END within each one, so two rows sharing a rank are still
  separated by POSITION and whichever is emitted LAST goes first. Merging the
  New PO chooser's attribution block and its cart-total block into one, to save
  a separator row, quietly made position the tiebreak again — and at 80x20 the
  review frame dropped `Total: at least $84.00` while `Agreement ..... (none)`
  and `Committee ..... (none)` stayed: the money floor going off the surface an
  order is COMMITTED from so two empty optionals could stay. The total is
  emitted first now (`po_create.go`'s `headerLines`), and the position is
  written down as a decision so it is not tidied back.
  `TestPOReview_TheCartTotalOutlivesTheOptionalRows` sweeps every drawable
  height rather than the two in `poPaneSizes`, because the defect lived below
  both of them, which is why nothing caught it. A builder may mark at most as many rows essential as the smallest
  drawable budget keeps (one, on any screen with a header), because an
  "essential" row the geometry drops anyway is the same false claim in a new
  place. `TestJDEForm_EveryEssentialHeaderRowIsOnThePane` holds it over a roster
  DERIVED from the `header jdeHeader` parameter of the layer's own frames, so
  every pinned header in the app is swept — the previous roster was derived from
  `jdePickList` literals, which is exactly why the order pad's warning went
  missing at 80x12 and 80x13 with nothing to report it. A site that marks
  nothing essential must say so in `jdeHeadersWithoutEssentials` WITH A REASON,
  so absent and empty are different states. On such a body NEITHER marker can
  be acted on, and only one of them is the sheet's to prevent: `↑ more above`
  appears when the window starts past line 0, which is a consequence of where
  the sheet puts its lines, so
  `TestReceive_ABodyWithOneRowNeverHidesLinesAboveTheWindow` sweeps the pane
  height and fails on any of it, with the set of one-row states read off the
  built body rather than listed. `↓ more below` appears when the block outruns
  the pane, which no arrangement of ONE block can avoid — it is drawn from
  80x14 to 80x18 on the receiving screen — so the TAIL is the accepted loss and
  the block is ordered so that what a short pane keeps is what the operator
  cannot do without. Do not read the sweep as holding both directions.
  Apply it to every body of a screen at once and DERIVE the check, or it is
  applied to the one that was reported: the receiving conversion fixed its
  quantity form and left the other two stranding their leads for a round.
  `TestReceive_EveryBodyLineBelongsToANavigableRow` walks the phase cases
  through `ReceiveFormScreen.body()` — the same expression `View` draws — so a
  phase added to the iota brings its body with it.
  `po_edit.go` and `po_add_line.go` still open their bodies
  with `l.Add` headings; they are safe only while their cursor blocks stay short
  of the pane. The New PO conversion took the other route and it is the one to
  copy: everything that would have been a lead-in — the supplier row, the
  optional attribution values, the review phase's PO-notes box — is a PINNED
  HEADER row with a rank, and the body is nothing but navigable rows
  (`po_create.go`'s `headerLines` / `body`). The screen's ANSWER to the last
  keypress is the one thing that is NOT a header row of its own: its head rides
  the layer's status row, which no budget can trim, and only the folded
  remainder reaches the header — see the answer-surface rule below.
- Comments in this codebase explain WHY, at length, including the failure that
  motivated the rule. Match that density.

### A screen that is working must say so, and a key that declines must say why

`po_create_pickers.go` carries the full note; the rule is worth knowing before
touching any screen an operator drives:

- Any action that goes off the terminal reports **working** (naming the work and
  the subject — "Looking up the items Acme Supply sells…", not "Loading…"),
  **succeeded**, and **failed**, and the bar still names a key that works. An
  error string must be CLEARED on the next success: a body that draws its
  "the lookup failed" line INSTEAD of the list will hide a load that worked.
  On a columnar screen the working line and the failure HEADLINE both go through
  the layer's status row — `jdeScreen.statusRow`, or `statusAnswer` where the
  headline is an order-level `errMsg` — both bounded by the same `fitStatus`,
  which flattens a multi-line OMS body in one forward pass; the failure's
  unbounded DETAIL rides in the pinned header,
  cut to a fixed row count before it is folded. `po_create.go`'s `workingLine`
  and `failure` answer for the PHASE being drawn, not for the screen: a failed
  agreement load is not a fact about the item picker, and reporting it there
  puts a sentence nobody can act on over the one they came for.
- Any key arm that declines to act — an empty list, a search that matched
  nothing, either edge of a pager — must say why. `return s, nil` there redraws
  a byte-for-byte identical screen, which reads as a wedged program; that was
  the whole of the "the item picker hangs after I press enter" report.
- Notes go on the PANE as well as the status bar: `StatusBar.Flash` expires
  after four seconds and the operator who saw nothing is still looking. A phase
  with nothing to ANSWER fills its header row with a standing FACT about the
  phase (`standingNote`) rather than leaving it blank, so "nothing to say" and
  "the row scrolled away" are different states.
- **AN ANSWER NEEDS TWO SURFACES, AND WHICH ONE IT IS ON IS NOT A RANK
  DECISION.** A pinned header row is trimmed by `jdeFitHeader` and a header may
  mark exactly ONE row essential (`jdeMinBudget`), so a phase pinning a typed
  BOX and holding something to SAY can keep only one of them — and this project
  has now shipped both choices as defects, each fixing the other:
  box-essential left a declining key answering into a row a short pane trimmed
  (byte-identical panes at 80x11/12 on the item filter, 80x11–13 on the asset
  search); note-essential, the fix for that, took the BOX off the pane at
  exactly those heights, so every rune typed into the asset search redrew a
  byte-identical frame. (The item filter survived on an accident —
  `itemFilterOrVerdict` rewrites its note per rune, so the row it got instead of
  the box happened to move. The asset search is SERVER-side and runs on enter,
  so nothing else on its frame moves at all.) Trading which row disappears
  cannot fix it in either direction.
  So the answer uses BOTH surfaces and each does what only it can:
  the layer's STATUS ROW is the one the frames append unconditionally and
  `jdeFitHeader` cannot reach, so the clause naming the KEY is reachable there
  at every height; the pinned HEADER is the one that FOLDS, so the clauses
  saying WHY are there whenever the row could not hold them. `po_create.go`'s
  `statusPlan` is the single decision — it assembles the row AND reports what
  that leaves for the header (`answerRows`, `failLines`) — and it reads those
  flags off the row it just BUILT rather than predicting them, because a
  prediction is a second implementation of the bound it predicts.
  `jdeScreen.statusAnswer` (`jde_form.go`) is the layer half, level-marked the
  same four ways `pickerNote.renderLines` marks a body note.
  The answer LEADS the WORKING sentence, and only where a box has taken the
  essential row: leading unconditionally cost the working sentence its tail on
  frames that had a header row going spare
  (`nothing to pick · Looking up the items Acme Supply…`). An ORDER-LEVEL error
  is never led at all — see below.
  **THE ANSWER MAY NOT DISPLACE THE WORK IN FLIGHT**, which is the guard #152
  put on this row and which had to be re-proved once a SECOND writer could reach
  it. It holds, and by the CAP rather than by any wording: `poLeadOnto` reserves
  the lead's opening clause at no more than HALF the row, so at 80 columns the
  subject keeps at least `51 - 25 - 3 = 23` cells whatever the lead says, and
  `poSubmitWords` is 20 for exactly that reason. A subject shares the row only
  where a phase pins a BOX, which DERIVES the set rather than listing it: on
  this screen the submit's, the item picker's and the asset picker's, and each
  leads with its FACT ("Creating the PO for", "Reloading the items",
  "Finding") so what the clip takes is the identifier at the tail.
  `poLeadOnto` is shared beyond this screen — the item form's kit-component
  picker (`kitPickStatus`) pins a filter box for the same reason and reserves
  its subject through the same function rather than copying the arithmetic — so
  a change to the cap is a change to every such row, not to this one.
  ORDER WITHIN A SUBJECT IS THE OTHER HALF OF THAT, and the asset picker is the
  worked example: it read `Searching ` + supplier + `'s assets for "zzz"…`, so
  the one variable part that is NOT the identity of the work — the supplier, the
  same on every phase and already pinned on a header row — sat AHEAD of the
  query the search is actually running on, and the 23-cell floor took the query
  and kept the supplier. Reordered to fixed words, then QUERY, then supplier
  (`workingSubject`). Choosing which SUBJECT survives is not enough; a subject
  whose parts are in the wrong order inverts rule 6 inside one sentence.
  WHAT A REORDER BUYS IS AN ORDER OF DEGRADATION, NEVER A FIT, and saying
  otherwise is the same defect one level down. The query is operator-supplied,
  so no arrangement of fixed words makes it fit: an MRO part description runs to
  forty cells against a floor of 23. The claim is only that the FIXED WORDS
  survive whole, then as much of the QUERY as is left, then the query's tail
  gives, then the supplier. The fixed words are what buys the query's head, so
  they are cut to the bone the way `poSubmitWords` was — `Searching assets ` was
  17 of the 23 and left FOUR characters of query, which reads as the reorder
  having worked while the fact it was made to protect was still gone;
  `poAssetSearchWords` is 8. Cut to the bone is not cut past it: `Search ` saved
  three more cells by going IMPERATIVE, and on a muted row whose neighbours are
  instructions that read as one more hint rather than as work in flight, beside
  a label of the same word one row up. A working sentence stays PROGRESSIVE.
  And the QUOTED string is bounded, never a bounded string then quoted (the
  `assetScopeRows` rule: Quote escapes, and the expansion lands inside the
  tightest budget on the screen) — but the CLOSING QUOTE is POSITIONAL, so a
  value in the MIDDLE of a sentence re-appends it out of the room the clip was
  given (`poQuotedClip`), or the operator cannot see where what they typed stops
  and the fixed words start; at the END of a row the ellipsis is the boundary
  already and that cell buys another character of the term instead. WHICH a site
  is, is a question about the ROW and not about the function: `assetScopeRows`
  appends a page suffix after its value, so the same query is end-of-row on page
  1 of 1 and mid-sentence the moment there is a next page — it drew
  `Showing ..... "hydraulic pump seal k… · page 1` until it routed the paged
  case through `poQuotedClip`. Because the
  cap makes every lead past it produce the identical reservation, an OVER-LONG
  lead is the provable worst case rather than a sample of one, which is what
  `TestPOStatus_AnAnswerNeverDisplacesTheWorkInFlight` drives — the
  vacuous-fixture rule pointed the other way: reach PAST the bound once rather
  than hope the longest sentence in the file reaches it. Its `poBoxInFlight`
  table is keyed by phase and checked against the discovered box set, so a phase
  that grows a search box later fails until somebody says what "a request is
  out" means on it.
  Two derived sweeps hold the rest, both watched to fail first
  (`po_create_answer_surface_test.go`): the phases come from `poPhaseCases()`
  and which of them pin a box is DISCOVERED by asking `essentialBoxRow`, never
  listed. And a pane change is necessary and not sufficient — the second sweep
  asserts the BOX and the ANSWER are both on the clipped pane at every drawable
  height, because "something moved" is exactly what the previous arrangement
  could say while the operator's box was gone.
  **AN ORDER-LEVEL ERROR IS NEVER LED, AND THE RESIDUAL THAT LEAVES IS STATED
  RATHER THAN IMPLIED.** The lead was applied to the error branch too, so a
  picker hint reserved up to half the row and the failure came back
  `✗ type to narrow the catalo… · creating the PO f…` — rule 6 inverted on the
  surface an order is committed from, where the error IS the fact and the hint
  is the thing an operator can rediscover by pressing the key again. The error
  takes the row alone now (`statusPlan`); the LEAD is what gives, entirely.
  ORDER-level is the whole of that scope: a failure belonging to the ONE line a
  frame is about is ranked the other way round, below the answer to the key just
  pressed, and hands the header the standing fact instead — `po_edit.go`'s
  `deleteStatusCarriesFailure` carries the ranking and the reason. The
  wordings were cut with it — `setErr`'s headlines the way `poSubmitWords` went
  from 32 cells to 20, `poSubmitFailWords` being 37 → 22 — but that is NOT the
  guarantee and the comment says so: the detail beside them is an OMS body of
  any length, and a bound expressed in an unbounded value is not a bound.
  WHAT THAT WOULD HAVE COST, AND WHY NO KEY CAN SPEND IT. Taking the row alone
  means the answer falls back to `answerRows`, a CONTEXT row of the pinned
  header, which at 80x11–13 is off the pane — the very state this work removed.
  It is NOT REACHABLE, and the closing argument is worth keeping because it is
  what a later change could break: it needs a box-pinning phase holding BOTH a
  standing `errMsg` and a non-empty answer, and `errMsg` is retired at every
  phase change (`Update`'s key dispatch, beside `pendingLead` and
  `sourceNote`). No `setErr` writer fires on a picker phase; the pending freeze
  on the chooser's `r`/`i`/`a`/`f` stops a picker being ENTERED with a POST
  out; and on review `phaseNote()` is empty while `poCreatedMsg` clears
  `pendingLead` before it calls `setErr`, so the answer there is always "".
  The MECHANISM is still in `statusPlan` and would reopen the moment a `setErr`
  writer becomes reachable from a box-pinning phase, or the phase-change clear
  is removed — which is why the clear is a rule and not a tidy-up. Before it,
  ONE failed submit put the chooser, both pickers and the line form permanently
  into that state: `setErr`'s other writers are `enterLinePhase`,
  `removeLineAt` and `addReorderLines`, and none of them is on the way out of a
  failed submit.
  `TestPOCreate_AnOrderLevelFailureDoesNotOutliveThePhaseItHappenedOn` drives
  the clear through the real submit and reports the answer going off the pane,
  not just the field staying set. `TestPOStatus_AnOrderLevelErrorIsNeverLedOffTheStatusRow`
  is the MECHANISM guard — it reaches the state by writing `setErr` directly,
  because no key sequence produces it, and it says so in its own doc so a later
  reader does not reason from a state the keys cannot reach. Its fixture is the
  vacuous-fixture rule caught in the act: driven on `poSubmitFailWords` alone it
  went GREEN with the lead still composed, because 22 cells is exactly what the
  reservation leaves at 80 columns (`poOrderErrorFixtures` carries a second
  error that reaches past it). The chooser drawn UNDER a failure — which IS
  reachable, since `poCreatedMsg` can land there — is swept at every height by
  `jde_pane_fit_test.go`.
  **A HEADLINE THE STATUS ROW IS DRAWING IS NOT REPEATED IN THE HEADER, and
  where the header does carry one the cut is MARKED.** `failLines` asked
  "is the head a substring of the row?", which also answered no when the row was
  drawing the head and merely SHORTENED it, so a narrow pane spent a body row on
  a second identically shortened copy. `poStatusPlan.drawsHead` asks which
  content the row CHOSE — a fact about the assembly, not a re-implemented bound,
  which is why it is the one flag not read back off the drawn row. The header's
  own copy goes through `pickerClip` rather than `cellPrefix`: at 60 columns the
  pane is 31 and a headline cut clean reads as a finished sentence.
- **Do not hand-count a hint against 51 columns — fold it.** Every note and
  fixed hint goes through `pickerWrap` / `pickerHint` / `jdeCaveatLines`
  (`pane_text.go`, `jde_form.go`), which fold at the `·` joints and indent
  continuations. EVERY one: the last sweep found seven still written straight to
  the pane with `StyleMuted.Render` — the association caveat (98 cells, cut
  mid-negation, and the negation is the whole point of the sentence), the
  `ctrl+t` cost-basis hint (110), the two line-form notes, the cart's
  catalog-pricing caveat, `Line source:` (a UUID puts it over), and an
  agreement's OMS-supplied notes. A styled literal on these screens that does
  not go through the folder is the defect, not a style choice. The SUBMIT
  failure and the supplier load error were the last two written straight to the
  pane, and both carry an OMS response body, which `omsapi.parseError` fills
  with the ENTIRE raw payload whenever the JSON envelope has no code — so at
  submit the operator read `✗ oms: http 502: <!DOCTYPE html><htm` and nothing
  else, on the one step where losing the reason costs the whole order. Both go
  through the LAYER now: the headline on `statusRow`, the body in the pinned
  header (`failLines`), cut to `poFailDetailRows` BEFORE it is folded.
  **THE FOLD-CUT-AND-MARK IS `pane_text.go`'s `failDetailLines` AND THERE IS ONE
  OF IT.** Four screens fold a failure body into a fixed row budget — the New PO
  submit, the report table's failed-load frame, the add-line price phase and the
  RECEIVING form — and each hand-written copy of that shape has lost the MARK,
  which is the only thing telling an operator that the sentence naming what
  failed is one they never got to read. The add-line copy shipped the reported
  decode error cut one word short of the field name; the receiving copy survived
  the round that fixed the other three, while the helper's own comment claimed
  there were three of them. Callers pass the width, the rows and the raw detail
  and then indent and style what comes back, and nothing else. The MARK spends
  the LAST of the rows the block already had, so converting a site cannot move a
  pinned header by a row or change what the bar under it names; at a one-row
  budget — which `receive_form.go`'s `headerSplit` really pays on a short pane —
  the content is kept and the cut is marked with the ellipsis instead, because a
  mark with no content beneath it is the rule inverted rather than obeyed.
  FIELD rows are the shape that does not FOLD, and they are bounded rather than
  exempt — the CART row included, which gives ground in its own STATED order
  because clipping its label alone was not enough: the LABEL first, then the
  expected DATE (`poCartDateMark`, and the line form still holds the value in
  full), and only then may the BADGE abbreviate (`poCartBadge`). The index, the
  quantity and the price never give — they are what review exists to confirm —
  and whatever is shortened carries the ellipsis that says so. An item-supplier
  line is the ONLY shape that can carry an expected date (`lineTakesDate`) and
  it is also the shape carrying the 14-cell `Inventory item` badge, so on an
  ordinary line the fixed parts alone came to 52 cells and the pane took the
  badge while the clipped label bought nothing. The highlighted row reserves
  two cells more, asked of `StyleSidebarItemActive.GetHorizontalPadding()`
  rather than counted.
  A PICKER row is the same shape and was the last one left unbounded — the rows
  an operator picks FROM, on the screen the report is about. Every one goes
  through `poFitRow` against `windowedListRoom()`: the NAME abbreviates, the
  FACTS never give, and the decorations behind them are dropped from the RIGHT
  so the columns that stay keep their places — reordering a columnar row costs
  more than an ellipsis. Every part of such a row is one of exactly two things:
  a BOUNDED IDENTIFIER or a FACT THAT NEVER GIVES, because a bound expressed in
  terms of an unbounded value is not a bound. The unit price on an item, the
  suggested quantity on a reorder row and the `(#id)` on a supplier are facts;
  the item's SKU and the asset's TAG are identifiers that happen to sit in the
  facts column, so each is clipped to what the price and the name's floor leave
  BEFORE `poFitRow` sees it — a 32-cell manufacturer part number is ordinary MRO
  data and used to push the price off the pane as `@ 3.`. What a row gives up it
  marks (`poRowDropMark`), and only when something was really given up: a row
  that marked an empty trailer would be claiming a cut nobody made and spending
  three cells of the budget it is short of to claim it. Callers reserve those
  cells too.
  A cut number is worse than an absent one: `@ 3.50` used to be drawn as `@ 3.`,
  which reads as a price. That room reserves the highlight's padding on EVERY
  row, not just the highlighted one, because a row that fits until it is
  selected is cut on exactly the press that stages it
  (`TestPOPickers_ALongNameKeepsTheFactsOnEveryPickerRow`, both states, both
  pane heights). Its fixtures are half the check: every picker fixture drew
  `Widget 1` / `Lathe 1` / `Bolt 1`, seven cells, so no test had ever rendered a
  picker row at the length OMS actually carries — and the long name is on the
  FIRST row only, so a fixture list is mixed the way a real one is.
  **A FIXTURE THAT CANNOT REACH THE BOUND UNDER TEST MAKES THE ASSERTION VACUOUS
  however precisely it is worded**, and that is rule 9 in its subtler form: not a
  check that cannot fail, but one that passes for reasons unrelated to the
  property it names. `TestPOSubmit_TheFrozenChooserHeaderDropsTheKeyColumn` is
  the second instance — it asserted the attribution VALUE was byte-identical
  across the freeze while the keyed and unkeyed rows are clipped to 30 and 32
  cells, so the claim was only true of the `Annual 1` / `Shop 1` names the fake
  generated. Whenever a check is about a bound, the fixture has to carry a value
  that reaches it.
  AN ASSERTION CHOSEN BECAUSE IT PASSES IS THE SAME FAILURE WITH THE FIXTURE
  LEFT ALONE, and it is the third instance:
  `TestPOSubmit_ADeclineDoesNotPushTheSubmitOffTheStatusRow` asserted the
  21-cell `Creating the purchase` — a prefix the 25-cell clip happened to spare
  — so it went green over `Creating the purchase or…` with the supplier gone,
  certifying rule 6 on a row that inverted it. Assert the substring the RULE
  requires, not one the truncation leaves.
  The supplier, agreement, work-order and committee rows are columnar VALUE
  rows carrying OMS-supplied names (`renderJDEField` with `jdeValue`), each
  clipped to what the shared label column leaves (`poFieldValueRoom`, ellipsis
  included) and kept to one row — folding them would spend rows the 24-row
  chooser does not have. They used to be one row: `Supplier: Acme (#1) ·
  agreement: Annual 2026 Steel Contract` is 67 cells, so a long supplier name
  took the agreement and the `(#id)` with it, on the row drawn on every phase
  including review, where it is the last thing seen before submit. A row of its
  own per value is what removed the priority ordering that patched it.
  A bound applied to one PART of a row and then appended to is not a bound: the
  asset picker's `Showing` row clipped its query and then added `· page 2`,
  which put it six cells past the pane. The page is a fact and never gives; the
  query abbreviates; the fact is reserved BEFORE the identifier is clipped.
  The folder (`pane_text.go`) is deliberately outside the JD Edwards layer, so
  the list screens can be legible at 80 columns without joining the columnar
  layout — and because `jde_form.go`'s own status bound calls into it
  (`cellPrefix`). Hand-counting is what broke: each
  line read fine at the width its author had in mind and then grew a
  `search closed · ` prefix, a supplier name or an unbounded OMS error string,
  and `clampToBox` took the TAIL — which
  is exactly where these lines name the key that gets the operator out. A hint
  the operator cannot finish reading is worse than none, because they believe
  they read it. Assert it too: check `clampToBox(screen.View(), screenBodyWidth(80), n)`,
  never `strings.Contains(Root.View(), …)` — the 80-column status bar satisfies
  that substring while the body line is cut in half.
- **A frame may only name keys that work in the state it is drawing.** With a
  search box open, `b` is a letter going into the query and `esc` only closes
  the box; with it shut, `esc` cancels the whole order. The picker frames used
  to print "b picks another line source · esc cancels the order" while the box
  was open, so one frame carried two claims about `esc` with the costly reading
  being the wrong one. That is structural now: `barItems` returns early on the
  typing branch, so the keys the box swallows cannot be named at all, and
  `TestPOSearchBoxes_TheBarNamesExactlyTheKeysThatWork` presses the whole key
  space against every box state to prove it.
- **Every width is CELLS, never runes — measured in ONE forward pass.**
  `lipgloss.Width` measures what the terminal draws; `len` over a string or a
  `[]rune` measures something else. A bound enforced in one unit while its
  caller budgets in the other is the same off-the-edge defect with a different
  alphabet: a CJK or emoji value clipped to `room` RUNES renders up to twice
  `room` cells, and clampToBox takes the tail the clip existed to protect.
  `pickerClip` and `pickerWords` both counted runes while every caller budgeted
  cells, and the ellipsis costs one cell of the budget.
  The second half of the rule is what the first attempt at it cost: both were
  fixed by delegating to `truncateVisible` (layout.go), which drops ONE rune off
  the end and re-measures the whole remaining string, so a bound became O(n²) —
  and these bounds are handed OMS response bodies, which `omsapi.parseError`
  fills with the entire raw payload whenever the JSON envelope carries no code.
  Measured on a 20 KB gateway page: 711ms for one clip, 1.5s to fold a fifth of
  it (unspaced, the shape DRF and a minified error page arrive in), and the
  source chooser rebuilds the attribution rows carrying one about ten times a
  frame — 45 seconds for five renders, nine seconds of dead terminal per
  keystroke, which is the reported hang restored by its own fix. So every bound
  on these screens goes through `cellPrefix` (pane_text.go), which walks
  FORWARD and stops when the budget is spent: its cost is the budget, not the
  length of what it was handed. `truncateVisible` is untouched — it belongs to
  the shared columnar layer — so a value that could be multi-KB should be
  bounded before it is handed to that layer rather than measured by it.
  And the input is bounded BEFORE a folder ever sees it, because at most `rows`
  lines of it can be drawn and folding the rest is work that is thrown away:
  `po_create.go`'s `failLines` cuts the detail with `cellPrefix` to
  `poFailDetailRows × width` before `pickerWrap` ever sees it, and
  `poAssocOptions.handle` bounds the two association load errors where they are
  RECORDED — one string, two renderers,
  the second of which is the edit screen's columnar field whose fitter is the
  shared one. `TestPOCreate_AHugeErrorBodyDoesNotFreezeTheFrame` drives a 20 KB
  whitespace-free body through both surfaces and fails on wall-clock;
  `TestPOReview_ALongCatalogNameKeepsTheFactsOnTheRow` walks a catalog name in
  both alphabets and `TestPOItemPicker_AWideRuneFailureBodyStillFitsThePane`
  folds an unspaced wide-rune error body.
- **A bar the operator cannot READ is not honest, it is absent.** The list
  sweep (`list_bar_honesty_test.go`) therefore checks each footer segment
  survives `clampToBox` as a whole line at 80 columns AND at a real pane height
  (`screenBodyHeight(24)` and `(30)`, scrolled and unscrolled), not just that
  `footerHint()` returns it — asserting the method's return value is what let
  every list ship with all eight sibling-surface keys past the 51-column cut.
  Two positions are not enough either, and the FIXTURE is half the check: a
  list row renders a title plus a line for a `Subtitle` and another for a
  `MetricsLine`, so `listFixtureRows` shapes rows the way the loaders do
  (plain, then subtitled, then metric'd — `purchaseOrderRows` leaves the
  subtitle empty on a PO with no supplier and no total, `loadInventoryItems`
  writes a metrics line only where the item has metrics) and
  `TestList_TheFooterSurvivesEveryScrollPosition` walks the cursor down the
  whole list. Rows of `listRow{ID, Title}` are ONE line each, which closed the
  arithmetic on a body half the real height and passed every legibility
  assertion over the defect below.
- **A list window is measured in LINES and re-derived on every move.**
  `rowsFittingFrom(start)` packs rows into `listBodyLines()` by what each one
  will actually draw, so the answer depends on WHICH rows the window starts at
  — and `scrollIntoView` therefore chooses the start and the size together,
  every time, rather than keeping a size taken at load. Sizing once over the
  title-only rows at the top of a list and holding it through the scroll put
  twenty lines into an eighteen-line pane the moment the cursor reached rows
  carrying a subtitle or a metrics line, and what `clampToBox` dropped was the
  folded footer with `N new PO` in it: the same claim, off the same edge, for
  the third time — horizontally, then vertically, then by counting rows where
  the renderer counts lines.
- **A textinput with no `Width` grows past its row, and `clampToBox` takes the
  caret.** bubbles' `handleOverflow` returns early when `Width` is zero, so
  `View()` emits the whole value: past the column where the row fills the pane
  every further keystroke redrew it byte for byte — the reported hang, reached
  by typing, in the PO-notes field. On a COLUMNAR sheet this is the layer's:
  hand it the BOX (`jdeField{Kind: jdeText, Input: &box}`) and `jdeFitRow` +
  `jdeFitInputValue` size the row against the pane the terminal really gave and
  keep the caret inside it. The New PO screen used to measure a hand-drawn
  prefix against a fixed 51 columns instead (`poInputWidth`), which is a bound
  computed against a width the terminal may not have — too narrow at 120
  columns, and unbounded before that function existed at all. Off the columnar
  layer the rule still has to be kept by hand: `listSearchInputWidth` against
  `listSearchPrompt`, so the value SCROLLS and the caret is always the last
  thing on the row. The list box also RESERVES its
  `  N match(es)` suffix, because a width makes bubbles pad a short value out
  to it and an unreserved suffix would then be pushed off the pane on every
  query rather than only on long ones. `TestPOTypedRows_EveryKeystrokeMovesTheRow`
  and `TestList_SearchBoxCaretStaysOnThePane` type DISTINCT runes past the cut:
  a viewport full of one repeated character looks the same however far it has
  scrolled, so a test that holds one key down passes without scrolling at all.
- **Folding a bar spends ROWS: move the row budget with it.** `clampToBox`
  truncates on both axes, so a hint folded onto three lines to survive the
  51-column cut then falls off the BOTTOM instead — the same claim, a different
  edge. Any row reservation must be DERIVED from what will be drawn
  (`ListScreen.footerRows` is `1 + len(pickerWrap(footerHint(), …))`), never a
  constant. On the columnar layer that derivation is `actionBarRowsFor`, and the
  budget that follows from it is `bodyAvailForBar` — one pair, and the reason
  the bar handed to those functions must be the bar that is about to be DRAWN.
  Where the answer feeds the bar's own contents (naming the scroll keys costs
  cells, which can fold the bar onto another row, which costs a body row, which
  can change the answer) the caller passes the bar WITH those keys on it:
  measuring against the tallest bar is the fixed point, so the answer cannot
  oscillate between frames. `receive_form.go`'s `qtyPagesFor` / `qtyBarItems`
  pair and `po_create.go`'s `bodyPagesFor` / `barItems` pair are the two worked
  examples, and both take the pinned header's height as an ARGUMENT measured
  before the arms ran, so a press is judged against the frame it was made on.
  The failure line is a HEADLINE PLUS A DETAIL and the two are written together
  (`setErr`, the only writer of either field): a picker's decline that set the
  headline alone left the previous failure's detail standing, so
  `nothing to add` was drawn with a 502's HTML folded underneath it, reading as
  the gateway explaining a validation message. It also belongs to the SUBMIT —
  a picker's decline goes in that picker's own note (`reorderEmptyNote` and its
  siblings), never in this line.
- **One budget, and it is the LAYER's.** `bodyAvailForBar` is the one answer to
  "how many rows does the body get", `bodyScrollsForBar` the one answer to
  whether it moves, and `windowRowsForBar` the one answer to what a page is
  worth, and `frameDrawn` the one answer to whether the frame is on the pane at
  all — each asked of the bar that is about to be DRAWN. The short forms
  (`bodyAvail`, `bodyScrolls`, `windowRows`) are gone: they assumed a two-row
  bar, which stopped being true when every frame started wrapping. A sheet that
  computes any of them itself will eventually compute it differently from
  the frame; `TestJDEForm_NoSheetAnswersTheScrollQuestionItself` holds the door
  shut by reading the package's own source, and it catches a bare
  `jdeLines.Len()` in a comparison as well as the named helpers.
  The New PO screen is the cautionary tale and the reason that sweep exists:
  it carried SIX answers to that one question (`frameRows`, `frameRowsWith`,
  `frameChromeRows`, `bodyRowBudget`, `cartRowBudget`, plus `sourceCartSpace`
  and `reviewCartSpace` doing the sum by hand because the shared one floored at
  three rows it might not have). All of them are gone.
- **A key that acts on a row must ask whether the row is DRAWN.** All three
  pickers keep the rows they were showing while a reload is out and after one
  fails — a failed refresh should not also destroy what the operator was looking
  at — but neither body draws them. `itemListOnScreen` / `assetListOnScreen` /
  `reorderListOnScreen` gate the cursor keys and `enter` (and the reorder marks)
  so an invisible cursor cannot be moved and an invisible row cannot be staged:
  an item going onto a purchase order the operator cannot see is a wrong
  purchase order. `rowCount()` reads the same three predicates, so the bar stops
  naming `UP/DN` for exactly as long as the gate holds.
  A list that IS drawn and empty is the same question with a different answer,
  and it is the state the report was filed about: past those gates the cursor
  arms compared against `len-1`, did nothing and said nothing, which is the hang
  exactly. Every such arm answers — `reportItemFilterState`, `assetEmptyNote`,
  `reorderEmptyNote` — with a lead saying what the key did, and that lead NAMES
  the key (`m.String() + " moves nothing"`). Naming it is not decoration: two
  keys sharing one lead answer with the same sentence, and on a frame drawing no
  rows, no highlight and no focused textinput the second press then redraws a
  byte-for-byte identical pane — the reported hang, reached by pressing Down
  then Up. Test it IN SEQUENCE with no state reset between presses; resetting
  the lead before every key is what made the sweep structurally unable to see
  it.
  A frame that binds only a HANDFUL of keys is the same rule, not an exemption:
  the supplier-switch confirm binds `ctrl+x` and `esc` and answered every other
  press with `nil`, so a reflexive double-tap of the `enter` that OPENED it
  landed on a renderer that is a pure function of unchanged state and redrew the
  pane byte for byte. It declines through `supplierSwitchNote`, which says what
  the KEY DID and names no key at all — the bar makes that claim, on every
  frame, where it cannot be trimmed. `enter` is still NOT bound to the
  destructive answer: `ctrl+x` is that key precisely so a double-tap cannot
  empty a half-built cart, and declining to bind a key is not licence to leave
  the press silent.
  **The CART IS FROZEN for as long as the create POST is out.** `finalize`
  copies the lines and the notes into the payload, so a removal, an edit, a
  typed note or a supplier commit after it is work the 201's navigation
  discards — and until then the pane is describing a cart that is not the one
  being created. Dropping the LAST line was the worst of it, because
  `removeLineAt` sends the phase back to the source chooser: the operator told
  to add a line while their two-line order was already going in. So while
  `pending` every arm that would touch the payload declines through
  `pendingDecline` and the bar stops naming it, on all THREE phases the operator
  can be on — review, the source chooser (`r`/`i`/`a`/`f`, `ctrl+x`, `ctrl+e`,
  `g`/`w`/`c`) and the supplier picker (`enter` onto a DIFFERENT supplier, which
  would re-target the request).
  Those two are reachable because `esc` is deliberately NOT gated — a frame with
  no way out while a slow gateway thinks is the worse defect — so freezing only
  the review arms would have left the identical defect one phase over. (Leaving
  the screen with `esc` does not CANCEL the request: the order may still be
  created with nobody watching. That gap is open and known; gating the last way
  out to close it would trade it for a dead end.) What stays live is what only
  READS or MOVES: `UP/DN` move a highlight through a windowed cart, `d` reviews
  it, `esc` leaves — and on the supplier picker `enter` on the row the order
  ALREADY carries, which commits nothing (`commitSupplier` returns early on the
  same id) and only sets the phase back to the source chooser. The line is what
  a key would CHANGE, not which frame it sits on: freezing that enter outright
  cornered the one frame that binds no way back and whose `esc` leaves the
  SCREEN, so the operator who wandered there mid-flight could only wait or throw
  the answer away. `supplierHighlightIsCommitted` is the one predicate the arm
  and the bar both read, and the sweep walks the rule rather than trusting it: a
  frozen frame with no key that returns to another frozen frame FAILS
  `TestPOSubmit_TheFrozenPhasesNameExactlyTheKeysThatWork`. The notes input is
  BLURRED by `finalize` and focused again by `poCreatedMsg` when the submit
  comes back failed, so a caret is never left blinking in a field whose contents
  have already gone — and `d`, which moves ONTO the review frame, leaves it
  blurred for the same reason.
  The freeze is an ALLOW-LIST, not a list of frozen keys, and that is the whole
  lesson of it: written the other way round it froze the nine keys somebody
  thought of, and `d` — added to the chooser bar in the same round — was free by
  default and put the caret back into the field the submit had just blurred.
  Every frozen phase names what may act and declines everything else through
  `pendingDecline`, including keys the phase does not bind at all. An arm added
  later is frozen until somebody says otherwise.
  **The frozen chooser says so on ONE surface: the bar.** It used to say it on
  two — the four line-source rows each read `Reorder queue — off while
  submitting`, and a hint above the cart rows repeated the cart chords the bar
  had already dropped, so one pane advertised and refused the same two keys.
  The rows spelled the same keys the bar spells, six rows of a twelve-row budget
  spent on a second copy of it, and the conversion deleted them rather than
  re-synchronising them.
  The KEY COLUMN on the optional agreement / work-order / committee rows is the
  same claim in two cells and it survived the deletion for a round, because the
  check that replaced the old rows-say-so test only read the BAR. Those rows are
  drawn `attributionRows(!s.pending)`: the letters go when the bar drops them,
  the VALUES stay, because they are part of the order being created and only the
  affordance is false. `TestPOSubmit_TheFrozenChooserHeaderDropsTheKeyColumn`
  asserts both directions on the clipped PANE — the at-rest half is not
  optional, or the absence check passes on a frame that never drew a letter.
  Two derivations hold the freeze: `poPhasesUnreachableWhilePending` classifies
  EVERY phase of the iota as swept-frozen or unreachable-with-a-reason, and the
  sweep fails when a key actually reaches a phase outside the frozen set, so the
  reason is checked by walking rather than trusted. `pendingLead` is cleared
  where the phase changes — once, in `Update`'s key dispatch, not in the arms
  that navigate — because a lead NAMES a key and "ctrl+x removes nothing" on the
  source chooser advertises a key that frame does not bind.
  FOCUS is in the state fingerprint (`poPickerState`) for every input on the
  screen, and `poFocusFingerprinted` is derived by reflecting for
  `textinput.Model` fields, because a caret lives INSIDE the value rather than
  beside it: the field-name check could not see focus at all, which is why `d`
  re-focusing the notes was invisible to every sweep. So is the caret's BLINK,
  from the other side — bubbles falls through to `Cursor.Update` for any key its
  own switch does not handle and that returns a tick unconditionally, so
  `poCmdActs` filters it (`poIsBlink`) or every key pressed inside a search box
  reads as an act.
  `TestPOSubmit_TheCartIsFrozenUntilItAnswers` walks all three phases in
  sequence; `TestPOSubmit_AFailedSubmitHandsTheCartBack` is the other half,
  because a freeze that outlived a 502 would hold the order hostage to a
  gateway.
  The GATED frames (loading, failed) are the same rule: `a`/`enter` on the
  reorder gate, `]`/`[` on the asset gate and the cursor keys on the supplier
  gate each shared one sentence between two keys, and those frames draw no rows,
  no highlight and no focused input, so the second press redrew the pane the
  first one left. `TestPOPickers_NoTwoGatedKeysShareASentence` presses each such
  pair together, in sequence, at both pane heights.
  List EDGES stay silent on purpose: the highlight is on the pane and visibly at
  the end, so the press has answered itself.
- **Two loads for one picker is a wrong list, so guard EVERY load site.** A
  picker reply echoes the `supplierID` it was asked about (`pickerReplySupplier`)
  and nothing else, so two lookups for the same supplier cannot be told apart by
  it and whichever lands last wins.
  `b` is named on the working frame and has to keep working, so leaving
  mid-lookup and pressing the source key again is an ordinary sequence — and
  (`b` there means the picker's "back to the line sources"; `esc` on the SOURCE
  chooser is the one that goes back to the supplier picker, and the chooser's
  own `b` was retired as a duplicate of it) —
  gating only the search box left `a` → `/`+search → `b` → `a` firing an
  unfiltered page 1 over a search still out, painting a FILTERED SUBSET as the
  supplier's whole asset list with an empty box and a green tick. Every site
  that fires one declines while one is out: `updateSourcePhase`'s `r`/`i`/`a`
  arms, the item picker's `r`, and the asset picker's search and `]`/`[`. The
  guard goes AFTER the phase change and BEFORE the query/page reset — resetting
  the fields the in-flight request owns is what made the mismatch legible as a
  success — so the key still acts (it opens the picker on the lookup that is
  really out) and the bar can go on naming it.
  Those guards are NOT the whole answer, and believing they were is what left
  the hole open for two rounds: `resetSupplierScopedPickers` clears the
  in-flight flags (rightly — a picker stranded on a "looking up…" frame for an
  answer nobody will use is its own defect), so `A` → `B` → `A` through the
  supplier picker makes A current again with A's lookup still out and every
  guard reset. Only the ASSET reply varies by more than the supplier — it
  answers a query and a page — so it carries a monotonic `assetsSeq` stamped in
  `loadAssetsForSupplier` (the one place an asset request is built) and checked
  in `handlePickerLoaded`, the same shape as `ListScreen.searchSeq` in
  `list.go`. The item and reorder replies carry supplier-wide data, so a second
  one is redundant rather than wrong, and they keep the `supplierID` echo alone.
- **ONE surface names a key: the action bar.** The pickers used to state their
  live keys TWICE — a prose bar at the top of the pane and a way-out line in the
  frame beside the note — and keeping the two in sync by hand put "enter picks
  the match" four rows above a note saying enter closes the search, with enter
  doing neither. Three gaps between those surfaces were recorded here as
  deferred; the conversion closed all three the same way, by deleting the second
  surface. The NOTES name no keys at all now. What a note carries is the LEAD —
  what the key just pressed DID — which is a statement about a press rather than
  a claim about what works, and it is what stops two keys redrawing one pane.
  `TestPOPickers_PaneNamesExactlyTheKeysThatWork` presses the picker vocabulary
  against every non-typing picker state, and
  `TestPOSearchBoxes_TheBarNamesExactlyTheKeysThatWork` presses the whole key
  space against the TYPING states the first one excludes; both fail a key the
  bar names that does nothing AND a key it does not name that acts — "acts"
  meaning CHANGES something, since a key that declines and says why has not
  acted. The search-box sweep also fails a BODY line that names a key, which is
  how the second surface is kept from growing back.
  THE VOCABULARY IS WRITTEN DOWN ONCE in `internal/tui/list_nav.go`, and NO
  UNIVERSAL IS ASSERTED OVER IT HERE. Four have been, and all four were false —
  the last claimed no list surface binds a movement keystroke outside the
  vocabulary, which `jdePickKey` falsifies by moving a columnar picker's cursor
  on `tab`/`shift+tab` (recorded below as `poFormNavAliases`). The FORM was the
  defect rather than the words: each version quantified over a set nobody
  enumerates, so each rewrite bought one round. A claim here now states what a
  NAMED check presses and stops there, or it is not written.
  `TestListNav_NoSurfaceBindsARetiredChord` presses each keystroke in
  `listNavRetiredChords`, and nothing else, over the fixtures its swept sets
  build, and fails on one that moves the operator's place — controlled by first
  showing the NAMED key moves in that same fixture, so a fixture nothing could
  move fails too (`listNavChordControls`, whose coverage of the retired set is
  itself checked). It says nothing about any other keystroke.
  `TestList_TheSearchOverlayNamesExactlyTheKeysThatWork` asserts that on
  `ListScreen`'s search overlay, at every row count in `listRowCases`, the bar
  names exactly the keys that act. It is about that overlay and not about a
  class of surfaces.
  WHY A LIVE QUERY BOX ROUTES LETTERS TO ITSELF is a fact about the MECHANISM
  and survives as prose because it is a reason rather than a census: every
  keystroke such a surface's switch does not name falls through to the box, so a
  bare `j` or `g` is a character the operator typed and binding it would eat
  what they typed, which is standing rule 4. Do NOT "close" that by binding
  letters into a search box, and do not restate it as a claim about which
  surfaces bind what.
  THE NAMING HALF is proven on the surfaces whose bar is a machine-readable
  record (every type embedding `jdeScreen`, plus `ListScreen`), and on those
  alone — see the navigation entry below, because every receiver in
  `listNavUnsweptReceivers` still names less than it binds. The New PO flow was never part of it after its
  conversion: it is on the columnar set (`UP/DN`, `PgUp/PgDn` when the body
  moves) and `j`/`k` are unbound on it.
- **On a destructive confirm the keys are on the BAR and the prose is the
  body.** `clampToBox` drops from the bottom, so whatever a screen draws last is
  what a short terminal eats; on `poPhaseSupplierSwitch` that used to be the
  decline hint, which is the safe answer on a destructive confirm. The layer
  settles it: the bar is pinned to the bottom of the pane and is drawn before
  the body gets any rows, so the two keys cannot be cut. The prose is a
  read-only body (`frameScrolled` + `switchScroll`), and `UP/DN` are named there
  exactly when the layer says it moves.
- **A BAR is rendered in two states, so word BOTH.** With the item picker's box
  open every letter is a character in the query, so the bar names `Esc` and —
  conditionally — `Enter`, whose LABEL says which of two things it is about to
  do: `Pick it` on a lone match, `Close & choose` on several, and nothing at all
  over a query that matched nothing, where it can only decline. A single label
  for all three is what the prose bar had, promising "picks the match" over
  eleven matches and over none. The notes no longer word this at all; the
  `typing` argument that survives on `itemFilterNote` chooses between "N item(s)
  · type to narrow" and a bare count, which is a fact about the state and not a
  claim about a key.
- **A keypress that changes nothing visible IS the reported bug.** Enter over an
  ambiguous search re-emitted the note already on screen, so only the caret
  moved — the original "it just kinda hangs there", surviving inside its own
  fix, and then again in the zero-match arm beside it, which keeps the box OPEN
  so not even the caret moves. EVERY arm that declines in answer to a key leads
  with what the key DID, and the GATES do too, not just the filter arms: all
  four verdict helpers (`catalogVerdictNote`, `assetVerdictNote`,
  `reorderVerdictNote`, `supplierVerdictNote`) take a prefix, and every call
  site that answers a key IN PLACE passes one. The four that pass `""` are the
  picker-ENTRY arms (`itemPickEntryNote`, and `updateSourcePhase`'s `r`/`i`/`a`
  guards): those change the phase, so the whole pane is the answer and a lead
  naming a key that did something else would be noise. That last step is what finally closed the reported hang on the
  search box it was reported about — the typing branch words the note with
  `catalogVerdict("")` on every rune, so a gate answering with the same wording
  answered with the note the keystroke before it had already drawn, and the
  enter arm never touches the textinput, so not even the caret moved. Without
  the lead the note comes back character for character, and on the browse path
  there is not even a caret in a blurred textinput to move.
  `TestPOPickers_EveryGatedKeyMovesTheBody` is the by-construction check: it
  seeds each off-screen picker state with that state's OWN un-led verdict and
  requires every gated key to change the clipped pane, so a lead dropped
  anywhere fails it. Targeted tests compare the rendered NOTE line before and
  after, because a blinking cursor satisfies a comparison of the pane; where the
  change IS the note arriving — the frames that draw a working or failure line
  and used to return before drawing the note under it — the comparison is the
  other way round, on the clipped pane, because the note object changed in both
  worlds.
- **Assert a mid-flight state while it is still mid-flight.** Driving the fake
  to completion and then checking is how a note that concluded "0 in catalog"
  during an eight-page walk passed its own test. Split the load command out
  (`r.Update` without `pump`), assert, then pump.
- **An empty slice is three different facts.** "Sells nothing", "the walk is
  still out" and "the walk failed" all look like `len(rows) == 0`, and only the
  first is safe to act on. The flag that means an ANSWER is the one recording
  which supplier the rows came back for (`itemSuppliersFor`); `catalogAnswered`
  / `catalogVerdict` gate on it, and a working frame never SUBSTITUTES a note
  for its working line — it draws the note underneath, so the fact stays first
  and the reply to the keypress is still visible. Every wording of a filter outcome goes through the
  one gated choke point (`itemFilterOrVerdict`) — the live count typed into the
  box reached the ungated wording on its own the first time round.
- **Do not silently discard staged work.** Committing a different supplier
  invalidates every cart line carrying an `item_supplier_id` (that id IS the
  item↔supplier pair, and the backend accepts it without checking it against
  the order), so the supplier picker warns, names the count, and waits:
  `poPhaseSupplierSwitch`, `ctrl+x` to drop and switch, `esc` to keep both.
  Freeform and ASSET lines survive — an `asset_id` names equipment, not a
  (item, supplier) pair, and the asset picker's `?manufacturer=` scoping is who
  built the machine, not who is being ordered from.
- **A picker that filters client-side must load every page.** The page count is
  then a correctness property, not a performance one:
  `omsapi.ListItemSuppliersForSupplier` fetched page one and dropped `next`, so
  a search for a real item on page two answered "No inventory items match" and
  no key on the screen could reach it. Paging makes the reply expensive and
  slow, so it is cached per supplier (`itemSuppliersFor`) with `r` to refetch —
  and every picker reply echoes its `supplierID` so one for a supplier the order
  has moved off is DROPPED. Found-nothing and could-not-tell are different
  facts: an empty catalog reports as a warning, never as a green "0 loaded".
- **A server-side search has a fourth fact: NOT ASKED.** The asset search runs
  only on enter, so what the box holds and what the rows answer are different
  things. Reading `assetsSearch.Value()` let esc out of an uncommitted box
  report `no asset matches "hovercraft"` against a supplier that simply has none
  — found-nothing where could-not-tell is the fact — and let `]` page with a
  query nobody submitted. `assetsQuery` records what the last load actually
  CARRIED, and every surface that describes the rows reads it: the notes, the
  pager, AND the `Showing` LABEL above the list — that label is what an operator
  reads first to know what a list IS, and leaving it on the live textinput for
  one round drew `search: hovercraft` over an unfiltered page with a green tick.
  A box holding something else says so instead of concluding: `assetScopeRows`
  pins `Showing ..... "…"` in EVERY state, open or shut, plus a
  `Not run ..... "…"` row once the box is shut and holds something nobody
  submitted. Drawing the box alone while it was being typed into was the same
  mislabel one state over, and it survived a round because every label test
  asserted with the box closed.
  The uncommitted-esc note asks whether any rows came BACK before saying what
  they answer, because on a search that found nothing that note is what the
  operator reads and "the rows still answer X" would assert rows that are not
  there.

### A pane that says there is more must name a key, and a key it names must move the PANE

`internal/tui/jde_unreachable_body_test.go` carries the rule and both sweeps and
is the authority; read it before adding a frame or wording a bar.

- **The rule is two implications COMPOSED, and each was broken somewhere
  different.** A frame drawing `↑ N more above` / `↓ N more below` must have a
  bar that NAMES a movement token, and a named token must move what the operator
  SEES. Together they get the operator to the next screenful, and by repetition
  through the body. Separately, neither says anything useful: a marker over a bar
  naming nothing is a dead end wearing an affordance, and a named key that moves
  an int nobody can see is the same rule broken from the other side.
- **A BODY THAT OWNS NO NAVIGABLE ROW IS PINNED, and a cursor-anchored frame can
  never move it.** `jdeLines.block()` answers `(0,0)` for a row that owns no
  line, so `frame` / `frameWithHeader` / `frameWrapped` window such a body at
  line 0 for as long as the frame is up and everything past `avail-2` is
  unreachable. It belongs on `frameScrolled` with an offset — the shape
  `po_add_line`'s confirm and the order pad already use. The DERIVED set was the
  four purchasing CONFIRMS: the line-DELETE confirm (`po_edit.go`, prose only —
  deleting takes no reason, so there is nothing to type into) and the
  attachment-DELETE confirm (`po_attachments.go`) now scroll; the order-VOID
  prompt (`po_detail.go`) pins its caveat in the header instead, because its body
  is a Reason box and the body's own floor keeps that; and the line-VOID prompt
  already did it that way, which is why it was the one that was right. Measured
  before the fix: the line-delete confirm hid its caveat at 80x15, 60x15 and
  45x16; the attachment confirm at 80x14 drew the heading above the fold and the
  whole "cannot be undone, staff only" warning below it, over `Enter=Delete`; the
  order-void prompt hid the half of its sentence that says the void CASCADES TO
  EVERY LINE.
- **A BODY'S LEAD-IN BELONGS IN THE PINNED HEADER.** A heading, a guidance
  sentence or a column header added with `l.Add` AHEAD of the first block is
  stranded the moment the body overflows — the rule AGENTS.md already states, and
  `jdeFitHeader` is the answer: it trims by RANK and claims nothing about what it
  dropped, where a window promises a remainder. Five frames were converted, all
  in the state where the list has exactly ONE navigable row so the bar honestly
  named nothing: the add-line identify phase, the attachments grid, and the kit,
  chain and level lists in the empty state each opens in. A per-row ERROR moved
  with them onto the layer's STATUS ROW, which no budget can trim.
  WHAT IS STILL OPEN, said plainly because a claim no check delivers is worse
  than no claim: the same strand exists on some forty other (screen, state) pairs
  whose list has TWO OR MORE rows. No sweep reports them — their bar names UP/DN
  and the key really moves — and closing them is the same header conversion on
  every columnar screen at once, the shape of sc-jde-lift rather than a patch.
- **A DESTRUCTIVE CONFIRM MID-WRITE IS A STATE OF ITS OWN, AND NO DERIVED SWEEP
  REACHED IT.** `jdeScreenStates` builds each confirm freshly OPENED, so every
  bar-honesty sweep measured the frame before the operator pressed the destroy
  key — and that is the half of its life the bar changes shape in. Both confirms
  got the rule wrong there, in opposite directions. `deleteBar` (`po_edit.go`)
  collapsed to `{Esc=Back}` while the delete was out and RETURNED, taking the
  movement tokens off the bar, while `deleteScrolls` never mirrored the branch
  and the arm went on scrolling: a key ACTING while the bar names nothing, which
  is standing rule 2 in the direction an operator cannot see to complain about.
  `updateConfirmDelete` (`po_attachments.go`) had the mirror — a blanket
  `if s.deleting { return s, nil }` over the WHOLE handler while the bar went on
  naming `Enter`, `Esc` and three movement tokens, all inert. The answer is one
  predicate per question, read by the bar AND the arm (`deleteDestroys` /
  `confirmDeleteDestroys`), and the scroll question measured against the bar that
  will really be DRAWN with the scroll keys added — measured against an
  unconditional tallest bar, a body that FITS under the shorter drawn bar answers
  "it scrolls", the arm bumps the offset, `ClampScroll` puts it back and the pane
  returns byte-identical with no note. Only the WRITE waits: `Esc` still leaves
  and the caveat still scrolls, because reading the warning while the server
  thinks is exactly what somebody does there, and withholding the scroll keys
  would leave `↓ N more below` over a bar offering nothing to press.
  `TestJDEConfirm_AWriteInFlightLeavesTheBarHonest` presses the keys with the
  write really in flight — reached by pressing the destroy key, not by setting
  the flag — and it asks its two halves with DIFFERENT instruments, which is the
  part to keep: FORWARD (a named token must move what is SEEN) on the clipped
  PANE, REVERSE (a key that acts must be named) on `jdePlaceOf`, because an arm
  that DECLINES AND ANSWERS changes the pane by design and a key that declines
  and says why has not ACTED. Measured on the pane, the reverse half reported
  both confirms at every height their caveat fits.
- **A HEADER SITE'S RANK CLAIM IS PER BRANCH, AND A FIXTURE REACHES ONE.**
  `jdeHeaderCases` built `chainHeader` on an item with NO packaging rows, so
  `TestJDEForm_EveryHeaderSiteIsSwept` measured the branch that DOES mark an
  essential row while the common one — a populated item whose chain validates —
  marked none at all, with the function's own doc saying in as many words that
  the row "goes to the heading". A claim the code did not honour, sitting inside
  the sweep's own vacuity guard. The rank is decided from what else the header
  will carry now, and `jdeHeaderCase.alsoIn` names FURTHER states of the same
  site so a builder with three branches is swept in three. The cheapest state to
  construct is the empty, freshly-opened one, which is why this is the shape the
  next site will get wrong.
- **AN ESSENTIAL ROW IS A PROMISE ABOUT THE ROW, NOT ABOUT ITS WIDTH.**
  `jdeFitHeader` trims by ROW and does no width fitting at all, so `clampToBox`
  is what cuts an over-wide header row — from the right, no ellipsis, closing SGR
  reset gone with it. `levelListHeader` (`storage_slot_generate.go`) promoted a
  63-cell sentence to the essential row against the 51 an 80-column pane gives,
  which is rules 5 and 6 broken inside the fix for rule 11. The shape that works
  is `chainHeader`'s: a short fixed FACT leads and takes the row, the unbounded
  remainder folds behind it as context through `jdeCaveatLines` against the LIVE
  pane. `TestJDEForm_EveryEssentialHeaderRowIsOnThePane` could not report ANY of
  this: it compared against the row already `truncateVisible`'d to the pane, so
  `Contains` matched the very mutilation the check exists to find, and the check
  could not fail in this direction at all. It compares the row AS THE BUILDER
  WROTE IT now, and the class it had been hiding is recorded rather than closed.
  KNOWN AND UNFIXED, with the MEASURED extent, so the next agent inherits the
  numbers instead of rediscovering them. `jdeOverWideEssentialRows` is the
  roster and it fails in both directions — an unlisted over-wide row is a new
  defect, a listed one that now fits is a stale exception — and the numbers in
  it are what the sweep measures rather than what anyone remembered. Twenty
  entries, one mechanism apiece:
  the columnar picker's `Filter .....` row on NINETEEN sites
  (`AssetFormScreen/viewPick` and its siblings), **70 cells** — cut at a
  terminal width of 80, where `screenBodyWidth` gives 51, and fitting from 100
  (pane 71) up. THE MECHANISM IS THE HINT PAST THE CAP, and the first wording of
  this entry got it wrong in a way worth recording: it blamed the layer's
  unsized fallback and named a `jdePickHeader` that does not exist, when
  `jdePickList.render` is the builder and all nineteen sites call it with the
  LIVE pane. The row is 70 cells at every width because nothing in it is derived
  from the pane at all — the field is declared at a flat `Width: 30`,
  `jdePaneFieldWidth` caps a text row at `bodyWidth - (indent + label + leader)`
  = 36 at an 80-column pane so 30 survives untouched, and `renderJDEField` then
  appends `"  "` plus the 23-cell hint AFTER that cap: 2 + 6 + 7 + 30 + 2 + 23.
  `jdePaneFieldWidth`'s own doc says so — "a hint sitting past the fill is still
  past the pane afterwards — which is exactly why the fold is `jdeFitRow`'s job
  and not this one's" — so the remedy for these nineteen is routing the filter
  row through `jdeFitRow`, which already trades the field against the hint and
  folds the hint underneath. A RECORDED REASON IS READ AS A DIAGNOSIS AND WORK
  IS FILED FROM IT, so a wrong one costs more than none. And `chainHeader`'s promoted
  validation message, **85 cells** — cut at 80 (pane 51) AND at 100 (pane 71),
  fitting only from 120 (pane 91), which makes it the widest essential row in
  the package and the only one that overruns past 80 columns. Those messages are
  composed unfolded from OMS-supplied level names, so no WORDING of them can be
  a bound.
  THE REMEDY IS THE LAYER'S, which is why neither was fixed where it was found:
  bound an essential header row where it is emitted, the way `jdeCaveatLines`
  bounds a caveat against the live pane. That is a per-screen conversion of the
  shape sc-jde-lift was, on twenty sites at once, and doing four of twenty from
  a review round is "applying the rule where it was reported" — the failure mode
  this file exists to record.
- **WHAT THE RULE DOES NOT PROMISE is a block taller than the window.**
  `jdeLines.Window` keeps a block's START and nothing scrolls inside one, so a
  single navigable row whose own block outruns a one-line body loses its tail
  whatever is pressed — the stated sacrifice `addLineBlock` records, a fact about
  the terminal's HEIGHT rather than a missing key. `jdeUnfetchableMarkerCases` is
  the recorded residue and the sweep fails on a stale entry as loudly as on a
  missing one.
- **A MOVEMENT SWEEP MEASURED ON A STATE FINGERPRINT IS NOT MEASURING RULE 1.**
  `jdePlaceOf` is the right instrument for the question it was written for — a
  position that drifted and happened to redraw the same — and it called the
  slot-generate RUN REPORT's `UP/DN=Scroll` alive at all 61 of the panes the
  report FITS at 80, 100 and 120 columns, where the key moved `resultCursor` and
  the pane came back byte for byte. `TestJDEForm_EveryMovementTokenMovesTheOperatorsPANE` is the
  pane-measured half. Two mechanical traps in writing one: the COLOUR PROFILE
  must be FORCED or a moving highlight is stripped and an honest form reads as
  dead, and `jdeBarOf` must then be fed a STRIPPED view, because it anchors on
  the bar's rule and that run of hyphens is styled — with colour on it finds no
  bar anywhere and the sweep passes over the whole package. Its width axis is
  `jdePaneWidths` and not every drawable width ON PURPOSE: at the 45-column floor
  a picker row has sixteen cells and two different catalogue items both draw as
  `▸ Hex b…  AF-`, so the key moves the cursor AND the window while the pane is
  unchanged. That is rule 5's width form, not a bar naming a dead key.
- **A FIXTURE WHOSE ROWS DIFFER ONLY PAST THE CLIP CANNOT REPORT MOVEMENT.** The
  reorder picker's nine rows were `Hex bolt M8x40 zinc #1 … #9` with identical
  quantities, and `poFitRow` clips the name from the RIGHT, so at 80 columns
  every row drew as one string and a picker whose cursor was moving perfectly
  read as dead. Rows a check tells apart must differ AT THE FRONT.
- **The run report was a LIST CURSOR over `body.Len()`, which is two facts wrong
  about a read-only body.** The count included the two lines the report opens
  with, which belong to no row, so its last two cursor positions addressed rows
  `block()` answers `(0,0)` for and pressing Down at the bottom threw the reader
  back to the top. It is an offset now (`resultScroll`).
- **`jdeScrollStep` (`jde_form.go`) is the ONE key-to-offset mapping**, the
  offset analogue of `pickRow` / `moveRow` / `pageRow`. THREE copies of the same
  six-arm switch had been written out by hand across TWO files — `po_detail`'s
  `handleSheetKey` and `handleOrderPadKey`, and `po_add_line`'s `keyConfirm` —
  and this change puts three more sites on that footing (the two removal
  confirms and the slot-generate run report, none of which had an offset
  before). Whether a key acts at all is still the
  SHEET's question — the two gates are asked of different bars — and the clamp is
  one-sided on purpose: `end` asks for the whole body and `frameScrolled` brings
  it back against the pane it is about to draw into, which is what makes
  `↓ 0 more below` impossible.

## Gotchas

- **`XDG_CONFIG_HOME` does not isolate anything on macOS.** `defaultPrefsPath()`
  and `defaultCachePath()` use `os.UserConfigDir()`/`os.UserCacheDir()`, which on
  darwin resolve under `$HOME/Library` and ignore the XDG variables entirely. A
  test that only sets `XDG_CONFIG_HOME` therefore writes the developer's real
  prefs file and fails on its second run; set `HOME` as well. `internal/config/prefs_test.go`'s
  `setRequiredConfigEnv` does this and is what any new config test should call.
- **A drive that WAITS OUT the cursor blink pays 200ms a keystroke, and this
  package has no room for it.** bubbles' tick is 530ms and `pump`'s budget is
  200, so a settler that starts the blink and gives up on it burns a flat 200ms
  per `key()` — invisible on a handful of presses and ruinous on a sweep, which
  rebuilds a screen per key per probe per pane size. The receiving key-space
  sweep took 292s that way (242s through a settler that ran the tick outright)
  against 1s once neither did, and `internal/tui` as a whole sat at 573s against
  `go test`'s **600s default per-package timeout**, which CI does not raise — so
  adding two phase cases to one sweep was enough to make the package fail by
  TIMING OUT, with a passing test named in the panic as the one that happened to
  be running.
  Two facts get you out. `textinput.Blink` returns its message IMMEDIATELY and it
  is only FEEDING that message back to `Update` that starts the tick, so
  recognise it and stop: `driveIsBlink` (`wo_materials_drive_test.go`), checked
  against `textinput.Blink()` by `TestDrive_TheBlinkIsWhatTheDriveSkips` so a
  bubbles rename cannot turn it into a drive that skips nothing. Both settlers
  read it — the shared `pump` and receiving's `receiveSettle` — which is what
  took the package to 133s. It skips no state a drive can see:
  `cursor.Update`'s `initialBlinkMsg` arm returns the next tick and touches
  nothing else. And TYPING is synchronous, so a typed rune needs no settling at
  all (`receiveType`). Do NOT shorten `pump`'s 200ms budget instead — it is the
  backstop for a genuine timer, shared with ~30 drive tests, and cutting it
  would make all of them racier on a loaded machine.
- **A DERIVED SET IS CHEAP TO WRITE AND EXPENSIVE TO ASK, so ask it once — a
  `for _, h := range jdePaneHeights()` in an inner loop is the second way this
  package has blown the 600s timeout.** `jdeDrawableWidths` / `jdePaneHeights`
  (`jde_pane_fit_test.go`) answer by BUILDING A ROOT AND RENDERING IT per
  candidate size, which is the whole point of them — Root's own gate is the
  authority on which panes exist — and it makes each call cost about what one
  sweep iteration costs. The report-table height sweep nested `jdePaneHeights()`
  inside its WIDTH loop, so it re-derived the set once per width per state per
  tab per fixture: a quarter of a million Root renders re-answering a question
  whose inputs never change, 200s against 59s for that one test. Both are
  `sync.OnceValue` now and hand out a COPY, so a nested call is free and no
  caller can reshape another sweep's axis;
  `TestJDEForm_TheDerivedPaneSetsStayTheOnesRootDraws` holds both halves against
  asking Root afresh. The rule generalises past those two: derive at the top of
  the test, not in the loop.
- `gofmt -l` flags a few pre-existing files (doc-comment backtick rewrites).
  Format only what you touch.

## Maintaining this file

Keep this file for knowledge useful to almost every future agent session in this project.
Do not repeat what the codebase already shows; point to the authoritative file or command instead.
Prefer rewriting or pruning existing entries over appending new ones.
When updating this file, preserve this bar for all agents and keep entries concise.
