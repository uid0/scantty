# Project agent memory

This file is the project's committed home for project-intrinsic agent knowledge: build, test, release, architecture, and sharp-edge notes that should travel with the code.

## Working against OpenMakerSuite

ScanTTY is a client of the OpenMakerSuite HTTP API and of ForgeKey; `README.md`
has the env vars and the package map.

- **No live OMS is reachable from a task worktree.** `SCANTTY_OMS_URL` is unset
  and nothing answers locally, so behaviour is verified by driving the real
  screens through `Root.Update` against a stateful `httptest` fake. See
  `internal/tui/wo_materials_drive_test.go` (the `pump` / `key` helpers, reused
  across drive tests) and `internal/tui/po_line_price_test.go`.
- **The OMS source is the contract.** A read-only checkout normally sits beside
  this one at `../openmakersuite`; `backend/<app>/serializers.py` and `views.py`
  are authoritative for wire shapes, and `frontend/src/pages/*.tsx` is
  authoritative for what parity means. Read it before guessing at a payload —
  and never modify it from a ScanTTY task.
- **Money comes over as strings OR numbers**, hence `omsapi.DecimalString`.
  `Empty()` means "null/unset" and NOT "zero": several OMS money properties
  return a real `0.00` for "no price recorded", so treat zero as an absence
  wherever a price is being carried forward (`internal/tui/po_line_price.go`).

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

## Conventions

- **JD Edwards World interface.** The standing goal is parity with the OMS web
  app in a fixed columnar green-screen style: keyboard only (a barcode scanner
  is a keyboard delivering a burst plus Enter), one shared label column, a
  persistent action bar naming exactly the keys that work where the cursor is.
  `internal/tui/jde_form.go` is the shared layer; `internal/tui/po_edit.go` is
  the pilot and `po_edit_jde_test.go` is where the layout and key scheme are
  pinned. Extend that layer — do not hand-roll a second style beside it.
- **A key the bar does not name must do nothing**, and a key it names must do
  something. Tests assert both. Two sweeps hold the rule and they cover
  different halves of the app: `po_view_jde_test.go`'s
  `TestPOView_BarNamesExactlyTheKeysThatWork` walks the columnar screens, whose
  bar is a `[]actionBarItem`; `list_bar_honesty_test.go` walks every
  `ListScreen`, whose bar is still a hand-built STRING (`ListScreen.footerHint`)
  and is therefore invisible to the first. Adding a screen means adding it to
  whichever sweep matches its bar — the header comment in
  `list_bar_honesty_test.go` records how eight advertised keys survived across
  four lists by falling between them.
- **A list's uppercase keys come from `listShortcuts` (`list.go`), never from a
  hint literal.** The footer and the handler read that one table; the previous
  shape appended the words to a hint string and left the key to a global
  accelerator in `app.go` that phase 3 had deleted. Lowercase acts on the list,
  uppercase opens a sibling surface of the same workspace (which is also a
  `workspaceSurfaces` row in `route.go`).
- **80 columns leaves the pane 51.** `screenBodyWidth(80)` is
  `80 - navColumnWidth(24) - 1 - padding(4)` = **51**, and the action bar gets 49
  of them. That is the number every columnar layout has to be checked against,
  and it is small enough that a hint, a six-column grid or a long value will not
  fit without help — `jdeFitRow`, `poFitLineGrid` and `jdeCaveatLines` in
  `jde_form.go` / `po_detail.go` are the three folds that exist for it.
- **Check the CLIPPED render.** `clampToBox` truncates in `Root.View()`, not in
  the screen, so a test that reads `screen.View()` passes while the terminal
  shows a cut line. Assert against `Root.View()` at 80/100/120 —
  `internal/tui/po_view_jde_test.go` is the pattern (and `poSeenWhileScrolling`
  for a body taller than the pane).
- **Viewing screens** use the read-only half of the columnar layer:
  `jdeScreen.frameScrolled` (scroll offset, not a cursor) and
  `renderActionBarWrapped` (a bar of a dozen order-level keys folds onto several
  rows rather than losing its tail). `internal/tui/po_detail.go` is the pilot for
  those, as `po_edit.go` is for forms.
- Comments in this codebase explain WHY, at length, including the failure that
  motivated the rule. Match that density.

### A screen that is working must say so, and a key that declines must say why

`po_create_pickers.go` carries the full note; the rule is worth knowing before
touching any screen an operator drives:

- Any action that goes off the terminal reports **working** (naming the work and
  the subject — "Looking up the items Acme Supply sells…", not "Loading…"),
  **succeeded**, and **failed**, and a failure frame names a key that still
  works. An error string must be CLEARED on the next success: several renderers
  show the error *instead of* the list, so a stale one hides a load that worked.
- Any key arm that declines to act — an empty list, a search that matched
  nothing, either edge of a pager — must say why. `return s, nil` there redraws
  a byte-for-byte identical screen, which reads as a wedged program; that was
  the whole of the "the item picker hangs after I press enter" report.
- Notes go in the screen BODY as well as the status bar: `StatusBar.Flash`
  expires after four seconds and the operator who saw nothing is still looking.
- **Do not hand-count a hint against 51 columns — fold it.** Every note, fixed
  hint and prose ACTION BAR goes through `pickerWrap` / `pickerHint` /
  `pickerFail` (`po_create_pickers.go`), which fold at the `·` joints and indent
  continuations. That folder is deliberately pane-local, outside the JD Edwards
  layer, so the list screens and the New PO help line can be legible at 80
  columns without joining the columnar layout. Hand-counting is what broke: each
  line read fine at the width its author had in mind and then grew a
  `search closed · ` prefix, a supplier name or an unbounded OMS error string,
  and `clampToBox` took the TAIL — which
  is exactly where these lines name the key that gets the operator out. A hint
  the operator cannot finish reading is worse than none, because they believe
  they read it. Assert it too: check `clampToBox(screen.View(), screenBodyWidth(80), n)`,
  never `strings.Contains(Root.View(), …)` — the 80-column status bar satisfies
  that substring while the body line is cut in half.
- **A frame may only name keys that work in the state it is drawing** — and
  that includes the WORDING of a note rendered in two states. With a search box
  open, `b` is a letter going into the query and `esc` only closes the box; with
  it shut, `esc` cancels the whole order. The picker frames used to print "b
  picks another line source · esc cancels the order" while the box was open, and
  the zero-match note used to keep its open-box tail after the box shut, so one
  frame carried two claims about `esc` with the costly reading being the wrong
  one. A note rendered in both states takes the state as an argument
  (`itemFilterNote`).
- **A bar the operator cannot READ is not honest, it is absent.** The list
  sweep (`list_bar_honesty_test.go`) therefore checks each footer segment
  survives `clampToBox` as a whole line at 80 columns AND at a real pane height
  (`screenBodyHeight(24)` and `(30)`, scrolled and unscrolled), not just that
  `footerHint()` returns it — asserting the method's return value is what let
  every list ship with all eight sibling-surface keys past the 51-column cut.
- **Folding a bar spends ROWS: move the row budget with it.** `clampToBox`
  truncates on both axes, so a hint folded onto three lines to survive the
  51-column cut then falls off the BOTTOM instead — the same claim, a different
  edge. Any row reservation must be DERIVED from what will be drawn
  (`ListScreen.footerRows` is `1 + len(pickerWrap(footerHint(), …))`;
  `PurchaseOrderCreateScreen.frameRows` measures the folded help line), never a
  constant, and a growing top chunk must window whatever the frame draws LAST —
  on the PO review phase that is the focused notes input, and an operator typing
  into a field that is off the pane is the worst form of this defect.
- **One budget, not one per block.** Every scrolling block on the New PO screen
  takes its height from `bodyRowBudget(otherRows)`, which measures the frame
  chrome and whatever the phase draws around the block; `renderWindowedList`
  takes that budget and counts its own `↑`/`↓` markers INSIDE it. The first pass
  gave the list footer and the review cart their own answers and left the
  pickers on a fixed ten rows, so a matched row could be highlighted and STAGED
  while off the pane — a wrong purchase order, on the screen the report was
  about. A block that cannot fit says how many rows it hid.
- **Measure a tail before you draw the list above it.** The asset pager and the
  reorder summary are written after their list; a list sized without counting
  them pushes exactly them off the bottom, taking the `]`/`[` keys with it.
- **A key that acts on a row must ask whether the row is DRAWN.** All three
  pickers keep the rows they were showing while a reload is out and after one
  fails — a failed refresh should not also destroy what was on screen — but
  neither frame draws them. `itemListOnScreen` / `assetListOnScreen` /
  `reorderListOnScreen` gate `j`/`k`/`enter` (and the reorder marks) so an
  invisible cursor cannot be moved and an invisible row cannot be staged: an
  item going onto a purchase order the operator cannot see is a wrong purchase
  order, and the failure frame names `r`/`b`/`esc` and nothing else.
  The SEARCH BOX is not an exception: enter inside it is gated too — on
  `itemListOnScreen` for items (the filter is client-side, so a pick out of an
  unanswered catalog is a pick out of nothing) and on `assetsLoading` for assets
  (that search goes off the terminal, and the reply carries no request
  generation, so a second one racing the first can leave rows belonging to a
  query the box no longer holds). Gate a key and the bar must stop naming it
  for exactly as long as the gate holds, in the typing arm as well as the
  browsing one, and the note the box OPENS with must not promise it either.
- **One WAY-OUT line, both surfaces.** A picker's way out is stated once — by
  `itemPickBar` / `assetPickBar` / `reorderPickBar` / `supplierPickBar` /
  `supplierSwitchBar` — and BOTH the screen's action bar (`helpText`) and the
  frame's own hint read it. Keeping the two in sync by hand is what put "enter
  picks the match" in the bar four rows above a note saying enter closes the
  search, with enter doing neither. Those bars are NOT the only place a picker
  names a key: a note answers a specific press, so it can be narrower than the
  bar, and the rule is the weaker one — whatever a bar names must act in the
  state being drawn, and bar and note must not CONTRADICT each other about the
  same key. One gap is known and DEFERRED to the queued columnar conversion,
  with the reasoning at the "One statement of what works here" comment in
  `po_create_pickers.go`: `itemPickBar`'s empty-list arm cannot tell "matched
  nothing" from "sells nothing" by row count, so it says only `r reloads` while
  the note says `/ edits the search`. `po_create_picker_status_test.go`'s
  `TestPOPickers_PaneNamesExactlyTheKeysThatWork` presses the whole vocabulary
  against every non-typing picker state and fails a key the bar names that does
  nothing AND a key it does not name that acts — "acts" meaning CHANGES
  something, since a key that declines and says why has not acted.
- **On a destructive confirm the KEYS go above the prose.** `clampToBox` drops
  from the bottom, so whatever is last is what a short terminal eats; on
  `poPhaseSupplierSwitch` that was the decline hint, which is the safe answer.
  The keys carry no supplier name (a 20-cell `pickerClip` name is what pushed
  the fold over), and the prose below them is trimmed to `bodyRowBudget`.
- **A note is rendered in two states, so word BOTH.** `itemFilterNote` takes
  `typing` and every arm consults it: with the box open `j`/`k` are characters
  and `enter` only picks a lone match. Gating one arm and leaving the rest is
  how the ordinary search path — type three letters, see eleven matches — went
  on naming three keys of which two did something else. Same gate on
  `assetLoadedNote`, whose reply can land with the box still open.
- **A keypress that changes nothing visible IS the reported bug.** Enter over an
  ambiguous search re-emitted the note already on screen, so only the caret
  moved — the original "it just kinda hangs there", surviving inside its own
  fix, and then again in the zero-match arm beside it, which keeps the box OPEN
  so not even the caret moves. Both filter arms of `commitSearchedItem` now lead
  with what the key DID ("too many to pick · …", "searched again · …") and the
  test compares the rendered NOTE line before and after, because a blinking
  cursor satisfies a comparison of the pane. Where the change IS the note
  arriving — the frames that draw a working or failure line and used to return
  before drawing the note under it — the comparison is the other way round, on
  the clipped pane, because the note object changed in both worlds.
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

## Gotchas

- **`XDG_CONFIG_HOME` does not isolate anything on macOS.** `defaultPrefsPath()`
  and `defaultCachePath()` use `os.UserConfigDir()`/`os.UserCacheDir()`, which on
  darwin resolve under `$HOME/Library` and ignore the XDG variables entirely. A
  test that only sets `XDG_CONFIG_HOME` therefore writes the developer's real
  prefs file and fails on its second run; set `HOME` as well. `internal/config/prefs_test.go`'s
  `setRequiredConfigEnv` does this and is what any new config test should call.
- `gofmt -l` flags a few pre-existing files (doc-comment backtick rewrites).
  Format only what you touch.

## Maintaining this file

Keep this file for knowledge useful to almost every future agent session in this project.
Do not repeat what the codebase already shows; point to the authoritative file or command instead.
Prefer rewriting or pruning existing entries over appending new ones.
When updating this file, preserve this bar for all agents and keep entries concise.
