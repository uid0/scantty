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
  Purchasing is converted screen by screen: the VIEW half (`po_detail.go`), the
  edit sheet, the add-line flow (`po_add_line.go`) and RECEIVING
  (`receive_form.go`, sc-jde-recv). The ENTRY half of New PO
  (`po_create.go` + `po_create_pickers.go`) is still on its own pane-local
  dialect and is queued. A flow with PHASES of its own brings its own derived
  sweep rather than joining a shared one — `po_add_line_sweep_test.go` and
  `receive_form_sweep_test.go` are the two examples, and they are the same
  file with the nouns changed: phases from the iota's sentinel, keys from
  `poKeySpace()`, state and focus from `reflect` over the screen struct.
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
  rather than interpret: `poPickerBarKeys`, the phase sweep's `buried` list and
  `listBarKeyNames` map each token to the keys it SPELLS and to no synonyms.
  Credit for a synonym is the sweep making the claim on the bar's behalf — the
  defect it exists to report, sitting inside the check. `↑↓` used to be read as
  naming `ctrl+p`/`ctrl+n`, `j/k` as naming the arrows, `pgup/pgdn` as naming
  `ctrl+u`/`ctrl+d` and `g/G` as naming `home`/`end`, so four bound-but-unnamed
  keys passed and any arm added behind them would have passed too. The bars now
  say what they bind (`j/k ↑↓ move`, `g/G home/end top/bottom`, and
  `poBarAliasKeys` for the second token in a segment), and the four emacs chords
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
  from its vocabulary is still pressed, in both directions, one file over. Where a bar is prose rather than `key claim · key claim`, reshape the
  bar: `poBarNamedKeys` reads single-letter keys only at a segment START,
  because scanning prose for a bare `a` finds the article. That reshaping is how
  `b` — bound on all three association pickers exactly as `esc` is, named by
  none of them — was finally caught.
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
  51 is the width that must HOLD, not the width to render as though we had, and
  which of the two a bound is depends on what it does when it bites. FOLDING
  narrow costs an extra line and loses nothing, so the folders (`pickerWrap` and
  everything through it) stay on `pickerPaneWidth`. CLIPPING narrow DESTROYS the
  tail, so every clip is measured against the pane the terminal really gives:
  `PurchaseOrderCreateScreen.paneWidth()` — `screenBodyWidth` of the width
  recorded from `WindowSizeMsg`, falling back to 51 unsized — and the picker
  rows, the cart row, the supplier header and the association rows all read it.
  Clipped to a fixed 51, a 120-column terminal drew every picker row abbreviated
  with forty columns of pane left blank, on the rows an operator picks FROM.
  `TestPOPickers_AWideTerminalDrawsTheWholeRow` holds both directions at 80, 100
  and 120: nothing overflows at the narrowest, and the same row draws WIDER at
  the widest. A test helper that hard-codes 80 cannot see the second half, which
  is why `poAssertFits` and `poPaneLinesAt` clip against `poFrameWidth(screen)`.
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
  `renderActionBarWrapped` (a bar of a dozen order-level keys folds onto several
  rows rather than losing its tail). `internal/tui/po_detail.go` is the pilot for
  those, as `po_edit.go` is for forms.
- **A sheet may not answer "how many rows?", "does this scroll?" or "what goes
  on the status row?" itself.** All three are `jde_form.go`'s
  (`bodyAvail` / `bodyAvailForBar`, `bodyScrolls` / `bodyScrollsForBar`,
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
  really does draw the frame — and warning that the keys still ACT on a screen
  nobody is drawing, so their effect cannot be seen. That second sentence used
  to reassure ("They still work"), which is what made the offset drift routed
  below a surprise. A bar with rows cut off it
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
  KNOWN AND ROUTED, on the other axis: `renderActionBar` — the one-line bar the
  NON-wrapping frames draw — tightens its gutter and then lets the line run past
  the pane, so eleven form screens (MaintenanceItemFormScreen's bar is 63 cells
  against 51) lose the tail of their legend at 80 columns. It predates the floor
  fix and the floor fix reduces it. The fix is to give those frames
  `renderActionBarWrapped`, which means giving `bodyAvail` and `bodyScrolls` the
  `items` they do not take, on some thirty sheets — a conversion, not a patch.
  A SECOND routed item rides on the same signature change, so treat the two as
  ONE conversion rather than two: movement is gated on SCROLLABILITY where it
  should be gated on DRAWABILITY. On a pane the frame is REFUSED on the body
  still gets its floor of one row (`jdeBodyAvail`, which is what makes the
  refusal notice's height honest), so `bodyScrolls` answers true and the
  movement arms act over a pane showing nothing but the notice — `end` on the
  order pad sets `padScroll` to the pad's length and growing the terminal back
  lands the operator at the bottom of a forty-line pad. `frameScrolled` cannot
  fix it: the sheet's own handler destroys the offset before the frame is ever
  called. Nor is a layer-side stash of the last DRAWN offset enough — the same
  thing happens to the CURSOR on every `frame` / `frameWrapped` screen, so
  fixing the two `frameScrolled` sheets would be the apply-it-where-it-was-
  reported failure again. That is why its priority is not cosmetic: until it
  lands, the too-short notice says so in as many words rather than reassuring.
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
  (`receive_form.go`'s `lineCaveats`), a separator travels with the block ABOVE
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
  other side. A builder may mark at most as many rows essential as the smallest
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
  of the pane, and the queued New PO conversion should not copy the shape.
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
  All FOUR pickers carry a `pickerNote` for this and every frame of each of them
  draws it, the loaded LIST frames included — the reorder and supplier frames
  answered into the flash alone for several rounds, two of their frames drew no
  body at all, and the supplier list frame then went one more round returning
  its rows with the note dropped, so the rule kept being documented wider than
  the code honoured it.
- **Do not hand-count a hint against 51 columns — fold it.** Every note, fixed
  hint and prose ACTION BAR goes through `pickerWrap` / `pickerHint` /
  `pickerFail` (`po_create_pickers.go`), which fold at the `·` joints and indent
  continuations. EVERY one: the last sweep found seven still written straight to
  the pane with `StyleMuted.Render` — the association caveat (98 cells, cut
  mid-negation, and the negation is the whole point of the sentence), the
  `ctrl+t` cost-basis hint (110), the two line-form notes, the cart's
  catalog-pricing caveat, `Line source:` (a UUID puts it over), and an
  agreement's OMS-supplied notes. A styled literal on these screens that does
  not go through the folder is the defect, not a style choice. That now
  includes the SUBMIT-failure line and the supplier header's load error
  (`renderFailLine`, `renderSupplierHeader`), which were the last two written
  straight to the pane — and both carry an OMS response body, which
  `omsapi.parseError` fills with the ENTIRE raw payload whenever the JSON
  envelope has no code, so at submit the operator read
  `✗ oms: http 502: <!DOCTYPE html><htm` and nothing else, on the one step where
  losing the reason costs the whole order.
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
  an operator picks FROM, on the screen the report is about. Every one now goes
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
  The source chooser's `g` / `w` / `c` rows and the `Supplier: … ·
  agreement: …` header are `label: value` rows carrying OMS-supplied names, so
  `renderAssocValue` and `renderSupplierHeader` clip each value to what the
  labels leave (`pickerClip`, ellipsis included) and keep them one row each —
  folding them would spend rows the 24-row chooser does not have, and the g/w/c
  rows are the first ones it drops when it runs out. The header is bounded in
  PRIORITY order, the agreement's label and floor reserved before the supplier
  name takes the rest, because at 51 columns a long supplier name used to remove
  the agreement and the `(#id)` with it — on the row that is drawn on every
  phase, review included, where it is the last thing seen before submit.
  The agreement row also lost the long form of its label:
  `  g  Purchase / pricing agreement (optional): (none)` is 52 cells with an
  EMPTY value, so the pane cut the value on every render.
  That folder is deliberately pane-local, outside the JD Edwards layer, so the
  list screens and the New PO help line can be legible at 80 columns without
  joining the columnar layout. Hand-counting is what broke: each
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
- **Every width is CELLS, never runes — measured in ONE forward pass.**
  `lipgloss.Width` measures what the terminal draws; `len` over a string or a
  `[]rune` measures something else. A bound enforced in one unit while its
  caller budgets in the other is the same off-the-edge defect with a different
  alphabet: a CJK or emoji value clipped to `room` RUNES renders up to twice
  `room` cells, and clampToBox takes the tail the clip existed to protect.
  `pickerClip` and `pickerWords` both counted runes while every caller budgeted
  cells (`renderCart`, `renderSupplierHeader`, `renderAssocValue`,
  `pickerWrap`), and the ellipsis costs one cell of the budget.
  The second half of the rule is what the first attempt at it cost: both were
  fixed by delegating to `truncateVisible` (layout.go), which drops ONE rune off
  the end and re-measures the whole remaining string, so a bound became O(n²) —
  and these bounds are handed OMS response bodies, which `omsapi.parseError`
  fills with the entire raw payload whenever the JSON envelope carries no code.
  Measured on a 20 KB gateway page: 711ms for one clip, 1.5s to fold a fifth of
  it (unspaced, the shape DRF and a minified error page arrive in), and the
  source chooser rebuilds the attribution row carrying one about ten times a
  frame — 45 seconds for five renders, nine seconds of dead terminal per
  keystroke, which is the reported hang restored by its own fix. So every bound
  on these screens goes through `cellPrefix` (po_create_pickers.go), which walks
  FORWARD and stops when the budget is spent: its cost is the budget, not the
  length of what it was handed. `truncateVisible` is untouched — it belongs to
  the shared columnar layer — so a value that could be multi-KB should be
  bounded before it is handed to that layer rather than measured by it.
  And the input is bounded BEFORE a folder ever sees it, because at most `rows`
  lines of it can be drawn and folding the rest is work that is thrown away:
  `pickerFail` cuts the detail to `rows × pickerPaneWidth` first (and stops
  claiming an exact hidden-line count when it does, since that count would only
  be true of the part it folded), and `poAssocOptions.handle` bounds the two
  association load errors where they are RECORDED — one string, two renderers,
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
  by typing, in the PO-notes field the sacrifice order below spends rows
  keeping on the pane vertically. Every typed row on these screens is measured
  from the prefix it is drawn with (`poInputWidth` against `poNotesLabel`,
  `poItemFilterLabel`, `poAssetSearchLabel` and `poLineRowPrefix`;
  `listSearchInputWidth` against `listSearchPrompt`), so the value SCROLLS and
  the caret is always the last thing on the row. The list box also RESERVES its
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
  (`ListScreen.footerRows` is `1 + len(pickerWrap(footerHint(), …))`;
  `PurchaseOrderCreateScreen.frameRows` measures the folded help line AND the
  failure line under the body), never a constant, and a growing top chunk must
  window whatever the frame draws LAST —
  on the PO review phase that is the focused notes input, and an operator typing
  into a field that is off the pane is the worst form of this defect.
  `frameRowsWith` reserved the failure line as ONE row for four rounds while an
  OMS body carrying newlines rendered as a block of them, so every budget on the
  screen was computed against a count that was wrong; it measures
  `renderFailLine` now, and that line trims its own DETAIL to
  `failRowBudget` — the pane, less the chrome, less the smallest body the phase
  can honestly draw — so what a short terminal loses is the tail of the
  gateway's HTML and never the sentence naming what failed.
  That line is a HEADLINE PLUS A DETAIL and the two are written together
  (`setErr`, the only writer of either field): a picker's decline that set the
  headline alone left the previous failure's detail standing, so
  `nothing to add` was drawn with the 502's HTML folded underneath it, reading
  as the gateway explaining a validation message. It also belongs to the
  SUBMIT — a picker's decline goes in that picker's own note
  (`reorderEmptyNote` and its siblings), never in this line.
- **One budget, not one per block.** Nearly every scrolling block on the New PO
  screen takes its height from `bodyRowBudget(otherRows)`, which measures the frame
  chrome and whatever the phase draws around the block; `renderWindowedList`
  takes that budget and counts its own `↑`/`↓` markers INSIDE it. The first pass
  gave the list footer and the review cart their own answers and left the
  pickers on a fixed ten rows, so a matched row could be highlighted and STAGED
  while off the pane — a wrong purchase order, on the screen the report was
  about. A block that cannot fit says how many rows it hid.
  The one exception is measured, not assumed: `bodyRowBudget` FLOORS at three
  rows, which is a lie when the phase chrome alone fills the pane, and on the
  source chooser at 80x24 it is — the folded bar, the supplier header, the four
  source rows and the `d` row leave the cart fewer rows than its own header and
  total. `sourceCartSpace` therefore does that one sum itself (and both the
  collapse decision and the render read it, because measuring with one budget
  and drawing with another is how a block passes its own fit check and then
  overflows). `reviewCartSpace` is the same sum for the review phase, where the
  row that floor spends is the focused notes input. Do not "fix" the floor in
  `bodyRowBudget`: it is shared with the queued columnar conversion.
  The cart's own chrome is derived too (`cartChromeRows`), and was the last
  hand-kept row count here: `poCartChromeRows = 5` counted one row per item
  while the catalog-pricing caveat, once it went through the folder, took TWO
  at 63 cells. `poCartCaveat` is the one wording both the renderer and the
  reservation read, so they cannot disagree about how tall it is.
- **When the pane runs out, sacrifice in a stated order — do not shave words.**
  The New PO screen gives ground in this order, last named being last to go:
  the optional ATTRIBUTION rows (`g` agreement, `w` work order, `c` committee,
  and the review tail's repeat of them), then prose and caveats, then the TITLE,
  then the `r`/`i`/`a`/`f` rows the screen is for, and never the cart's
  existence — the `d` that opens it, its count and its total, in that order of
  protection — or the focused PO-notes input. `sourceAttributionShown`,
  `sourceTitleShown` and `reviewAttributionShown` are that order in code: three
  header rows plus the bar they lengthen were enough on their own to
  push the whole collapsed-cart sentence off an 18-row pane, so the frame drew
  no cart at all while `j`/`k`/`x`/`ctrl+e` answered into a four-second flash;
  on review the same rows plus a caveat folded onto two left the cart no line
  and took the notes field with them.
  Ask BOTH halves before dropping anything: does hiding actually free rows, and
  does the frame overflow with them shown. Asking only the second is what the
  first version did, and a supplier offering exactly ONE optional row spends the
  same rows either way — the substitute notice is one row in the slot the row
  occupied and the bar folds to the same height — so at 80x24 the frame replaced
  a real committee row with "optional rows need more height", which was FALSE,
  stopped naming `c`, and made `c` decline. Dropping a row that costs nothing to
  keep is worse than the overflow it avoids.
  The TITLE step is what makes the total half of that order true rather than
  accidental, and it is the one place the order runs INSIDE a sentence.
  `cartHiddenSentence` is ORDERED by sacrifice — the key, then the count, then
  "not listed here", and the total LAST — so the total
  is what a one-row overflow takes, and with one optional row offered the
  chooser sits exactly on an 18-row pane with nothing spare. `sourceTitleShown`
  gives up "Where should this line come from?" and its blank line first: two
  rows that name no key and carry no value, with the four `r`/`i`/`a`/`f` rows
  right under them still saying what the screen is. It is measured against the
  same conservative reservation the attribution step uses (`sourceCartMinRows`, one
  function so the two consecutive steps cannot reserve different carts), which
  includes a row of slack for a declining key's lead, so the title goes while
  the pane still has a row spare. That is the trade: the title costs nothing to
  lose, and the spare row is what stops a keypress pushing the total off.
  Two rules ride along. **The bar follows the cut**: `sourceHelpText` stops
  naming `g`/`w`/`c` for exactly as long as their rows are off the pane and the
  three arms decline (`attributionHiddenNote`) — a key naming a row the frame
  has dropped is the same defect as a key acting on one. And **what is dropped
  says so**, in one row that cannot fold: both notices are FIXED strings sized
  so that even led by a decline (`g is off here · …`) they stay one rendered
  row, since a second row appearing on a keypress would take back the row it was
  dropped to free. The TITLE is the one exception to that second rule and says
  why in its own decision: nothing stops working when it goes, so spending a row
  to announce a row that cost the operator nothing would be the overflow it was
  dropped to avoid.
- **Measure a tail before you draw the list above it.** The asset pager and the
  reorder summary are written after their list; a list sized without counting
  them pushes exactly them off the bottom, taking the `]`/`[` keys with it. The
  three picker FAILURE frames do the same with their way-out bar and verdict
  note: both are built first and the unbounded error DETAIL is what `pickerFail`
  trims to `bodyRowBudget` (saying how many rows it hid). An OMS error string is
  the whole raw response body whenever the JSON envelope carries no code, so
  roughly 380 characters used to push the verdict note off an 80x24 pane and
  roughly 470 took `esc cancels the order` with it — folding had traded the
  horizontal cut for a vertical one, for the third time in this file.
- **A key that acts on a row must ask whether the row is DRAWN.** All three
  pickers keep the rows they were showing while a reload is out and after one
  fails — a failed refresh should not also destroy what was on screen — but
  neither frame draws them. `itemListOnScreen` / `assetListOnScreen` /
  `reorderListOnScreen` gate `j`/`k`/`enter` (and the reorder marks) so an
  invisible cursor cannot be moved and an invisible row cannot be staged: an
  item going onto a purchase order the operator cannot see is a wrong purchase
  order, and the failure frame names `r`/`b`/`esc` and nothing else.
  A list that IS drawn and empty is the same question with a different answer,
  and it is the state the report was filed about: past those gates, `j`/`k`
  compared a cursor against `len-1`, did nothing and said nothing, which is the
  hang exactly. Every such arm now answers — `reportItemFilterState`,
  `assetEmptyNote`, `reorderEmptyNote` (which also takes the reorder mark key) —
  with a lead saying what the key did, and that lead NAMES the key
  (`m.String() + " moves nothing"`). Naming it is not decoration: two keys
  sharing one lead answer with the same sentence, and on a frame drawing no
  rows, no highlight and no focused textinput the second press then redraws a
  byte-for-byte identical pane — the reported hang, reached by pressing `j` then
  `k`. Test it IN SEQUENCE with no state reset between presses; resetting the
  lead before every key is what made the sweep structurally unable to see it.
  A frame that binds only a HANDFUL of keys is the same rule, not an exemption:
  the supplier-switch confirm binds `ctrl+x` and `esc` and answered every other
  press with `nil`, so a reflexive double-tap of the `enter` that OPENED it
  landed on a renderer that is a pure function of unchanged state and redrew the
  pane byte for byte. It declines through `supplierSwitchNote` now, which reads
  the two live keys off `supplierSwitchBar` — the sentence the frame already
  prints — so a decline cannot name a key the confirm does not honour. `enter`
  is still NOT bound to the destructive answer: `ctrl+x` is that key precisely
  so a double-tap cannot empty a half-built cart, and declining to bind a key is
  not licence to leave the press silent.
  A key the SUBMIT has made inert is the same shape once more: while the POST is
  out `helpText` drops `enter submit` from the review bar (the drop
  `itemPickBar` and `assetPickBar` already make for a gated key) and the press
  answers on the "Submitting…" line through `pendingLead`, because an operator
  watching a slow gateway is exactly the operator who will have missed a
  four-second flash.
  **And the CART IS FROZEN for as long as the request is out**, which is the
  same rule with something at stake: `finalize` copies the lines and the notes
  into the payload, so a removal, an edit, a typed note or a supplier commit
  after it is work the 201's navigation discards — and until then the pane is
  describing a cart that is not the one being created. Dropping the LAST line
  was the worst of it, because `removeLineAt` sends the phase back to the source
  chooser: the operator told to add a line while their two-line order was
  already going in. So while `pending` every arm that would touch the payload
  declines through `pendingLead` and the bar stops naming it, on all THREE
  phases the operator can be on — review, the source chooser (`r`/`i`/`a`/`f`,
  `x`, `ctrl+e`, `g`/`w`/`c`) and the supplier picker (`enter` onto a DIFFERENT
  supplier, which would re-target the request).
  Those two are reachable because `esc` is deliberately NOT gated — a frame with
  no way out while a slow gateway thinks is the worse defect — so freezing only
  the review arms would have left the identical defect one phase over. (Leaving
  the screen with `esc` does not CANCEL the request: the order may still be
  created with nobody watching. That gap is open and known; gating the last way
  out to close it would trade it for a dead end.) What
  stays live is what only READS or MOVES: `↑↓` / `j`/`k` move a highlight
  through a windowed cart, `d` reviews it, `b` and `esc` leave — and on the
  supplier picker `enter` on the row the order ALREADY carries, which commits
  nothing (`commitSupplier` returns early on the same id) and only sets the
  phase back to the source chooser. The line is what a key would CHANGE, not
  which frame it sits on: freezing that enter outright cornered the one frame
  that binds no `b` and no `d` and whose `esc` leaves the SCREEN, so the
  operator who wandered there mid-flight could only wait or throw the answer
  away. `supplierHighlightIsCommitted` is the one predicate the arm and
  `supplierPickBar` both read, and the sweep now walks the rule rather than
  trusting it: a frozen frame with no key that returns to another frozen frame
  FAILS `TestPOSubmit_TheFrozenPhasesNameExactlyTheKeysThatWork`. The notes input is BLURRED
  by `finalize` and focused again by `poCreatedMsg` when the submit comes back
  failed, so a caret is never left blinking in a field whose contents have
  already gone — and `d`, which moves ONTO the notes frame, leaves it blurred
  for the same reason.
  The freeze is an ALLOW-LIST, not a list of frozen keys, and that is the whole
  lesson of it: written the other way round it froze the nine keys somebody
  thought of, and `d` — added to the chooser bar in the same round — was free by
  default and put the caret back into the field the submit had just blurred.
  Every frozen phase now names what may act and declines everything else through
  `pendingDecline`, including keys the phase does not bind at all. An arm added
  later is frozen until somebody says otherwise.
  The frozen chooser SAYS which keys are off rather than merely dimming them:
  the four line-source rows keep their letters and their places — that block is
  the map of the screen — and each reads `Reorder queue — off while submitting`
  (shorter wording, so a marked row still fits 51 columns), while the `g`/`w`/`c`
  rows drop their key COLUMN and become the value-only lines the review phase
  already draws. Colour alone could not carry it: lipgloss renders plain with no
  terminal attached, so a dimmed row is a claim no test can check.
  The cart-row keys are stated ONCE (`sourceCartKeyClaim`) and read by both
  surfaces that make the claim — the action bar and the hint above the rows.
  They were two sentences until the freeze dropped `ctrl+e` and `x` from the bar
  and left the hint three rows below still naming them, so one pane advertised
  and refused the same two keys.
  Two derivations hold it: `poPhasesUnreachableWhilePending` classifies EVERY
  phase of the iota as swept-frozen or unreachable-with-a-reason, and the sweep
  fails when a key actually reaches a phase outside the frozen set, so the
  reason is checked by walking rather than trusted. `pendingLead` is cleared
  where the phase changes — once, in `Update`'s key dispatch, not in the three
  arms that navigate — because a lead NAMES a key and "ctrl+x removes nothing"
  on the source chooser advertises a key that frame does not bind.
  FOCUS is in the state fingerprint (`poPickerState`) for every input on the
  screen, and `poFocusFingerprinted` is derived by reflecting for
  `textinput.Model` fields, because a caret lives INSIDE the value rather than
  beside it: the field-name check could not see focus at all, which is why `d`
  re-focusing the notes was invisible to every sweep. `TestPOSubmit_TheCartIsFrozenUntilItAnswers` walks all three
  phases in sequence; `TestPOSubmit_AFailedSubmitHandsTheCartBack` is the other
  half, because a freeze that outlived a 502 would hold the order hostage to a
  gateway.
  The GATED frames (loading, failed) are the same rule and were the last
  instance of it: `a`/`enter` on the reorder gate, `]`/`[` on the asset gate and
  `j`/`k` on the supplier gate each shared one sentence between two keys, and
  those frames draw no rows, no highlight and no focused input, so the second
  press redrew the pane the first one left.
  `TestPOPickers_NoTwoGatedKeysShareASentence` presses each such pair together,
  in sequence, at both pane heights.
  List EDGES stay silent on purpose: the
  highlight is on the pane and visibly at the end, so the press has answered
  itself. The CART on the source chooser is the same rule off the pickers:
  `cartListedOnScreen` measures whether its rows fit, and when they do not the
  block collapses to one sentence carrying the count, the total, that the lines
  are not listed and the key that opens them, while `j`/`k`/`x`/`ctrl+e` decline
  and `sourceHelpText` stops naming them.
  That sentence is ORDERED by what may be sacrificed rather than shaved to fit,
  because its length is data: `at least $…` (any line priced from the catalog at
  save time) or a five-figure order takes it past one row whatever the wording.
  So the key, the count and `not listed here` lead it and the TOTAL is what
  folds onto a second row — and the row budget counts that fold instead of
  assuming one row, which the first version claimed and was not. The declining
  leads are short for the same reason — they fold onto the first line AHEAD of
  the key rather than pushing it onto a second — and each one names the key it
  answers (`j moves nothing`, `x removes nothing`, `ctrl+e edits nothing`), so
  no two of them can redraw the same pane. `x` removing, and `ctrl+e` editing, a
  line clampToBox had dropped is the worst instance of this rule this screen
  has had. The way out has to be real: the review phase lists and highlights the
  same cart at 80x24, which is why `d` is what the sentence names.
  The SEARCH BOX is not an exception: enter inside it is gated too — on
  `itemListOnScreen` for items (the filter is client-side, so a pick out of an
  unanswered catalog is a pick out of nothing) and on `assetsLoading` for assets
  (that search goes off the terminal). Gate a key and the bar must stop naming
  it for exactly as long as the gate holds, in the typing arm as well as the
  browsing one, and the note the box OPENS with must not promise it either.
- **Two loads for one picker is a wrong list, so guard EVERY load site.** A
  picker reply echoes the `supplierID` it was asked about (`pickerReplySupplier`)
  and nothing else, so two lookups for the same supplier cannot be told apart by
  it and whichever lands last wins.
  `b` is named on the working frame and has to keep working, so leaving
  mid-lookup and pressing the source key again is an ordinary sequence — and
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
- **One WAY-OUT line, both surfaces.** A picker's way out is stated once — by
  `itemPickBar` / `assetPickBar` / `reorderPickBar` / `supplierPickBar` /
  `supplierSwitchBar` — and BOTH the screen's action bar (`helpText`) and the
  frame's own hint read it. Keeping the two in sync by hand is what put "enter
  picks the match" in the bar four rows above a note saying enter closes the
  search, with enter doing neither. Those bars are NOT the only place a picker
  names a key: a note answers a specific press, so it can be narrower than the
  bar, and the rule is the weaker one — whatever a bar names must act in the
  state being drawn, and bar and note must not CONTRADICT each other about the
  same key. Two gaps are known and DEFERRED to the queued columnar conversion,
  with the reasoning at the "One statement of what works here" comment in
  `po_create_pickers.go`: `itemPickBar`'s empty-list arm cannot tell "matched
  nothing" from "sells nothing" by row count, so it says only `r reloads` while
  the note says `/ edits the search`; and the two failure frames print the
  way-out line twice, once as the bar and once as the tail of the verdict note
  drawn under it. `po_create_picker_status_test.go`'s
  `TestPOPickers_PaneNamesExactlyTheKeysThatWork` presses the whole vocabulary
  against every non-typing picker state and fails a key the bar names that does
  nothing AND a key it does not name that acts — "acts" meaning CHANGES
  something, since a key that declines and says why has not acted.
  A THIRD gap is deferred to the same conversion, and as a RULE rather than a
  list: EVERY list surface should name and bind the same navigation set. The
  bar-honesty work unbound four alias chords on the surfaces it swept —
  `ctrl+u`/`ctrl+d` on `ListScreen`'s pager, `ctrl+p`/`ctrl+n` on its search
  overlay and on the review cart — while the list-SHAPED screens outside those
  sweeps (category, location, supplier, asset parts, storage slots, device
  types, thermostats, e-paper panels and the rest) still bind them, so `ctrl+d`
  pages the supplier list and does nothing on the inventory list the operator
  reached it from. The asymmetry is deliberate for now: the ARROWS were kept and
  NAMED (`j/k ↑↓ move`, `g/G home/end top/bottom`) because they cost three cells
  and are what a non-vim operator reaches for, and the chords were dropped
  because naming them costs cells a 51-column bar does not have. Aligning the
  rest is a change to roughly twenty screens nobody reported, which is why it
  waits — and why it is written here as one rule, since an enumeration of the
  twenty is how the drift started.
- **On a destructive confirm the KEYS go above the prose.** `clampToBox` drops
  from the bottom, so whatever is last is what a short terminal eats; on
  `poPhaseSupplierSwitch` that was the decline hint, which is the safe answer.
  The keys carry no supplier name (a 20-cell `pickerClip` name is what pushed
  the fold over), and the prose below them is trimmed to `bodyRowBudget`.
- **A note is rendered in two states, so word BOTH.** `itemFilterNote` takes
  `typing` and every arm whose keys DIFFER between the two consults it: with the
  box open `j`/`k` are characters and `enter` only picks a lone match. The
  one-match arm is the exception and says so in place — "enter picks it" holds
  with the box open and shut alike. Gating one arm and leaving the rest is
  how the ordinary search path — type three letters, see eleven matches — went
  on naming three keys of which two did something else. Same gate on
  `assetLoadedNote`, whose reply can land with the box still open.
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
  pager, AND the `search:` LABEL above the list — that label is what an operator
  reads first to know what a list IS, and leaving it on the live textinput for
  one round drew `search: hovercraft` over an unfiltered page with a green tick.
  A box holding something else says so instead of concluding — `showing: …`
  above the box in EVERY state, open or shut, plus `search (not run): …` once
  it is shut. Drawing the box alone while it was being typed into was the same
  mislabel one state over, and it survived a round because every label test
  asserted with the box closed.
  The wording is for the state it is DRAWN in, twice over: the uncommitted-esc
  note only ever appears with the box SHUT, where enter stages the highlighted
  row, so it names `/ reopens the search` and never "enter runs it"; and it
  asks whether any rows came BACK before saying what they answer, because on a
  search that found nothing that note IS the body of the frame and "the rows
  still answer X" would assert rows that are not there. "enter runs the search
  AGAIN" is likewise conditional on a search having run.

## Gotchas

- **`XDG_CONFIG_HOME` does not isolate anything on macOS.** `defaultPrefsPath()`
  and `defaultCachePath()` use `os.UserConfigDir()`/`os.UserCacheDir()`, which on
  darwin resolve under `$HOME/Library` and ignore the XDG variables entirely. A
  test that only sets `XDG_CONFIG_HOME` therefore writes the developer's real
  prefs file and fails on its second run; set `HOME` as well. `internal/config/prefs_test.go`'s
  `setRequiredConfigEnv` does this and is what any new config test should call.
- **A drive that settles a keystroke pays 200ms for the cursor blink.** `pump`
  (`wo_materials_drive_test.go`) abandons the textinput blink tick by WAITING IT
  OUT — 200ms of dead wall-clock per `key()` — and bubbles' tick is 530ms, so a
  drive that runs the tick itself pays even more. It is invisible on a handful of
  presses and ruinous on a sweep: the receiving key-space sweep took 292s through
  `pump` and 242s through a settler that ran the tick, against 1s once neither
  did. Two facts get you out, both in `receive_form_sweep_test.go`:
  `textinput.Blink` returns its message IMMEDIATELY and it is only FEEDING that
  message back to `Update` that starts the tick (so recognise it and stop —
  `receiveIsBlink`, checked against `textinput.Blink()` by a test so a bubbles
  rename cannot turn it into a drive that skips nothing); and TYPING is
  synchronous, so a typed rune needs no settling at all (`receiveType`). Do not
  shorten `pump`'s own budget to fix this — it is shared with ~30 drive tests and
  would make all of them racier on a loaded machine.
- `gofmt -l` flags a few pre-existing files (doc-comment backtick rewrites).
  Format only what you touch.

## Maintaining this file

Keep this file for knowledge useful to almost every future agent session in this project.
Do not repeat what the codebase already shows; point to the authoritative file or command instead.
Prefer rewriting or pruning existing entries over appending new ones.
When updating this file, preserve this bar for all agents and keep entries concise.
