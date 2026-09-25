# scantty

Scanner-driven curses TUI for [OpenMakerSuite](https://github.com/uid0/openmakersuite) and [ForgeKey](https://github.com/uid0/forgekey). Designed for shop-floor terminals where a barcode/HID scanner is the primary input device and bandwidth is unreliable.

## What it does

- **Scan a barcode, asset tag, location code, or OMS URL** → open the matching record → start the relevant workflow.
- **Browse OMS workspaces** that mirror the web UI: Dashboard, Inventory, Purchasing, Assets, Facilities, Maintenance, SIGs, Reports, Settings.
- **Receive deliveries** into open purchase orders without touching a mouse.
- **Cache aggressively** to a local SQLite store so the terminal stays useful when the network drops (cache reads on failure are coming — the store is wired but most code paths still go to the network).

## Quick start

Requires Go 1.22+ (built and tested on 1.26).

```sh
git clone git@github.com:uid0/scantty.git
cd scantty
go build ./cmd/scantty

export SCANTTY_OMS_URL=https://oms.example.org
export SCANTTY_FORGEKEY_URL=https://forgekey.example.org
./scantty
```

The first run will fail fast with a clear message if either URL is missing. The cache database is created on demand under your OS user cache directory — `~/Library/Caches/scantty/cache.db` on macOS, `${XDG_CACHE_HOME:-~/.cache}/scantty/cache.db` on Linux (override with `SCANTTY_CACHE_PATH`).

## Configuration (environment variables)

| Variable | Purpose | Default |
|---|---|---|
| `SCANTTY_OMS_URL` | Base URL for the OMS HTTP API | *(required)* |
| `SCANTTY_OMS_TOKEN` | JWT access token for authenticated requests | unset |
| `SCANTTY_FORGEKEY_URL` | Base URL for the ForgeKey HTTP API | *(required)* |
| `SCANTTY_FORGEKEY_TOKEN` | Bearer token for ForgeKey | unset |
| `SCANTTY_FORGEKEY_CLIENT_CERT` | Path to mTLS client cert (post-trust-refactor) | unset |
| `SCANTTY_FORGEKEY_CLIENT_KEY` | Path to mTLS client key | unset |
| `SCANTTY_FORGEKEY_CA_CERT` | Path to ForgeKey CA cert for verification | unset |
| `SCANTTY_CACHE_PATH` | SQLite cache file location | `<user cache dir>/scantty/cache.db` — `~/Library/Caches` on macOS, `${XDG_CACHE_HOME:-~/.cache}` on Linux |
| `SCANTTY_SCANNER_SOURCE` | `stdin` (keyboard-emulation scanners) or a serial device path | `stdin` |
| `SCANTTY_THEME` | Colour theme by name; overrides the saved preference, and an unrecognised name falls back to the default rather than failing. `internal/theme`'s `Definitions()` is the list. | saved preference, else `purple` |
| `SENTRY_DSN` | Override the baked-in Sentry DSN (e.g. point at a personal sandbox). The default reports panics + `run()` errors to the `scantty` project on the self-hosted Sentry — public DSN, safe to commit. | baked-in |
| `SENTRY_DISABLED` | Set to `1` (or `true`) to disable Sentry entirely. | unset |
| `SENTRY_ENVIRONMENT` | Sentry environment tag (`dev`, `staging`, `prod`). | `dev` |
| `SENTRY_RELEASE` | Override the release identifier. Defaults to `scantty@<vcs.revision[:12]>` from the build's debug info when available. | derived |

If `SCANTTY_OMS_TOKEN` is unset, scantty still works for the `AllowAny` endpoints — barcode lookup, scanning items/assets/fixtures, and creating reorder requests in kiosk mode all function unauthenticated. Receiving needs a token throughout: every endpoint the receiving flow drives is authenticated, the worksheet `GET` it opens on included, so without one the form can only report that the session is not signed in. Most other writes need a token too.

## Terminal size

Scantty draws in a terminal of **at least 80 columns and 7 rows** and refuses
below either, saying which it needs and what it has:
`scantty needs 80 columns; this terminal has 63`. 80 is the width every layout
in the program is written against — the sidebar takes 24 columns and its border
one, leaving a 51-column pane and a 49-column action bar. Anything wider than 80
is used as it arrives: wide panes add detail, but never trade away what the
standard 80-column layout shows.

## Keys

Scantty reserves system keys for **scroll / exit / submit / edit** and nothing
else. There are no app-wide letter accelerators and no workspace digits:
navigation happens through the sidebar menu and each screen's own on-screen
menu, and every screen names the keys that apply to it along its foot.

| Key | What it does |
|---|---|
| `Tab` | Move the keyboard into the sidebar menu, and back out to the screen |
| `↑`/`↓` (or `j`/`k`) | Move — a menu row, a list row, a form field |
| `←`/`→` | In the menu, jump a whole workspace; on a choice field, change the value |
| `g`/`G`, `PgUp`/`PgDn` | Jump to top/bottom, page a long body |
| `Enter` | Open what is selected · submit the form you are on |
| `Esc` | Back one step · cancel the form you are on |
| `Ctrl+E` | Edit / open the highlighted row |
| `Ctrl+K` | Search palette (items, assets, orders, people) — from any screen that is not taking every key itself: a form or prompt with a focused field owns the key (in a text box it deletes to end of line), and the receiving form uses it to close a line short |
| `Ctrl+C` / `Ctrl+Q` | Quit, from anywhere |

On a **list** screen the letters in that foot carry case: lowercase acts on the
list you are looking at (`s` sort, `f` filter, `r` refresh, `n` new), and
uppercase leaves it for a sibling surface of the same workspace — `N` for a new
purchase order from Purchasing, for example. Each list names its own along its
foot; they are shortcuts to surfaces the sidebar tree already carries, never the
only way there.

That foot names only the keys that will act on the list as it stands: the moving
keys need a second row to move to and `Enter` needs a row to open, so neither is
named on a list with nothing in it and the moving keys drop off a list of one —
while `s`, `r`, `f` and the uppercase letters stay named, so a list with nothing
in it still says what to do next instead of drawing no foot at all. On a
terminal too short to hold that foot whole, a list draws a notice naming the
height it needs and the height it has instead of the rows, for the reason the
columnar screens below refuse: a foot with rows cut off it names some keys and
hides the rest with no way to tell which. `Esc` still leaves, and `r`, `f`, `n`
and the uppercase letters still act; moving, sorting and opening are held while
that notice is up, so you come back where you were.

The purchasing **viewing** screens — a purchase order's detail sheet and its
attachments, associations and terms — are drawn in the fixed columnar JD Edwards
World style and follow its reduced key scheme instead: no `j`/`k` or `g`/`G`
aliases (arrows, `PgUp`/`PgDn` and `Home`/`End` only), and `Enter` fires the
screen's own action rather than opening a row. Each of those screens carries a
persistent action bar naming every key that works there, and a key the bar does
not name does nothing. On a terminal too short to hold that bar whole, any
screen drawn in this style shows nothing but a notice saying how many rows it
needs — a bar with rows cut off it would name some keys and hide the rest with
no way to tell which — and saying that the keys that move you are held until it
fits, so you come back where you were. Typing and `Esc` are not held: the way
out of a pane too short to work in stays open. The rest of the app is being
converted screen by screen.

The purchasing **entry** sheets in that same style — starting a new order,
editing a line, adding one by scanning an identifier, and receiving a delivery
— read the same way, with field navigation on top: `↑`/`↓` — or `Tab`/`Shift+Tab`,
on a sheet that has fields — move between them, `PgUp`/`PgDn` pages a body taller
than the pane, `Enter` fires the sheet's own action from whichever row the cursor
is on — save the line, add the one just scanned — and `Esc` backs out, the bar
saying so when that discards what you typed. Each of those keys is named only
while it will actually act. Every purchasing screen now reads this way; New PO
was the last one on a scheme of its own, so `j`/`k` no longer move anything
there either.

New PO is a multi-step entry rather than a single sheet, and each step — the
supplier picker, the line-source chooser, the reorder/inventory/asset pickers,
the line form, the review cart — carries its own bar naming only what acts
there: `r`/`i`/`a`/`f` choose where a line comes from, `Ctrl-E` and `Ctrl-X` edit
and remove the highlighted cart line, and `d` reviews it. Inside a picker `b`
goes back to the line sources and `Esc` cancels the whole order — except with
that picker's search box open, where every letter goes into the query and `Esc`
only closes the box; the bar names whichever pair is live. While the order is
being submitted the bar drops every key that would change what has already been
sent, so the cart on the pane is the cart going in.

A purchase-order line is **entered at the packaging level the vendor sells it
at**. Where the supplier ships an item by the case, both the New PO line form
and the scan-to-add flow ask for **cases** and a **case cost**, say what one
case holds and what the line comes to, and convert to the base quantity and
per-unit price the order records — so a case of 24 at 48.00 is typed as one case
at 48.00 and saved as 24 at 2.00, not as 24 at 48.00. `Ctrl-T` moves both rows
to base units and back, because a broken case or an odd top-up is a legitimate
order; it is named only while it can act, so a quantity that is not a whole
number of cases stays in units until it is one, the pane saying which quantities
would flip it. Items the vendor sells as singles are unchanged.

**Receiving** is a flow of its own on that layer, driven off the server's
receiving worksheet — whether the order may be received against and why not,
which lines are outstanding, and what a scanner will read off each one. The form
opens on a `Scan` box so a scanner burst lands somewhere it means something:
`Enter` there finds the line the code names, and the tracking barcode, carrier
and delivery date follow it above the per-line quantity boxes. Type what
actually arrived — short or over, as counted. The figure is recorded and the
difference flagged, never rounded to the quantity ordered. `Enter` off the scan
box commits the quantities into a **review**, and `Enter` on the review is what
sends the receipt — while that receipt is in flight the bar drops every key but
`Esc`.

Lines whose units carry serials open a capture step on the way: a serial number
plus optional lot and expiry per unit, where `Enter` records the unit and moves
to the next (to the review on the last), `Esc` goes forward to the review,
`PgUp`/`PgDn` walk the units while `↑`/`↓` walk the three fields, and `Ctrl+E`
on the review comes back. Fewer serials than units is allowed, and the summary
then says how many units are in stock with no serial naming them. Two settling
keys sit behind a confirm of their own — `Ctrl+K` closes the focused line short,
`Ctrl+R` marks the whole order received — where `Ctrl+X` commits and `Enter` is
deliberately unbound, so a reflexive double-tap of the key that opened the
confirm cannot write a balance off. `Ctrl+O` reopens a line that was closed short
in error, preserving the close-short in history and recording the correction;
it is available even when the settled order can no longer receive, and returns
to the refreshed receiving form so the recovered balance can be booked.
Receiving ends on a summary of what the server booked, which `Enter` or `Esc`
closes and `r` re-reads so the next delivery can be worked without leaving the
screen.

The sidebar lists the eleven workspaces; the workspace you are in also shows its
own surfaces indented beneath it (Inventory › New item / Kits / Categories /
Locations / Suppliers, ForgeKey › Firmware / Lockouts / …). Facilities and
Reports open a cursor menu of their surfaces instead.

## Scanner input

Most barcode scanners present themselves as USB HID keyboards. With `SCANTTY_SCANNER_SOURCE=stdin` (the default) the scan workspace simply reads typed characters until Enter — the scanner appends `\n` after the code by default, so a single trigger pull populates and submits the field.

For non-keyboard scanners (raw HID, serial, etc.), point `SCANTTY_SCANNER_SOURCE` at a character device path. (The raw-HID path isn't wired yet — see Roadmap.)

The scan workspace sends non-URL codes to OMS's scanner dispatcher, which can
resolve barcodes, asset tags, location codes, and other server-known identifiers.
OMS URLs carry their destination directly, including the short `/scan/...` URLs
printed on item, asset, location, project-storage, donation-item, and maker-box
QR labels. Items, assets, locations, project-storage stints, work orders, and
vendor work orders open their existing detail screens. A fixture, donation-item,
or maker-box label is still identified, but the scan workspace warns that
ScanTTY has no detail screen for it instead of reporting a successful open.
Access badges can overlap barcode shapes, so they are never claimed locally; an
unmatched 8–10 digit scan explains that badge lookup is not available yet (see
Roadmap).

## Project layout

The source tree is the authoritative project inventory. Query it rather than
maintaining a file-by-file copy here:

```sh
go list ./...                    # packages in the current checkout
rg --files cmd internal deploy systemd
```

Package documentation and local code comments own the boundaries and
non-obvious constraints of each area. `AGENTS.md` records the project-wide
conventions that future changes must preserve.

## Architecture notes

**Why pure-Go SQLite (`modernc.org/sqlite`)?** No CGo means a static binary across the shop's mix of x86 laptops and ARM kiosks, with no `libsqlite3` system dependency. Throughput is fine for the cache workload (small, low-frequency writes).

**Why bubbletea over tview/termbox?** Better composition story (every screen is a `Screen` interface implementer), cleaner message-passing for async API calls, and the `bubbles/textinput` widget covers our form needs without a giant widget toolkit. The downside is more boilerplate per screen, but the per-screen code stays readable.

**Why mirror the web UI's 8 workspaces?** Members already have a mental map of where things live in the browser. Scantty's nav uses the same names and groupings so muscle memory transfers. Scanner-priority surfaces (the scan workspace itself) sit at the top of the sidebar menu so they're never more than a keypress away.

**Error envelope handling.** OMS returns a stable `{error: {code, message, details}}` shape on failure. `omsapi.APIError` parses this and exposes `IsAuth()`/`IsNotFound()` helpers. The client normally switches on `code` rather than assuming one status for every refusal, because OMS may return informative codes at 400 or 422. Endpoint-specific classifiers can narrow that further when the contract defines both values; supplier-link `stale_version`, for example, is recognized only at 409.

**JWT refresh.** A 401 with a refresh token in scope triggers one transparent retry against `/api/auth/refresh/`. There's no automatic logout — if refresh fails too, the call surfaces a `not_authenticated` error and the next request will fail the same way until the user re-supplies a token.

**ForgeKey trust refactor.** ForgeKey is mid-migration from MAC + JWT-password to device_id + mTLS. `forgekeyapi.New(Options{ClientCert, ClientKey, CACert})` already loads an mTLS keypair when supplied; bearer-token auth still works during the transition. Once the refactor lands, scantty will need to be enrolled as a trusted device and provisioned with a cert at `SCANTTY_FORGEKEY_CLIENT_CERT`/`KEY`.

## Current scope

Landed:
- Foundation: clients, cache, scanner classifier, config, TUI shell with 11 workspaces.
- End-to-end scanner flow: scan → lookup → inventory detail → reorder form → submit. From an item's detail, `s` opens its supplier links with supplier name, supplier SKU, box and unit barcodes, unit cost, and lead time.
- Review an item's stock movements and usage from its detail sheet with `h`: stock history shows dated levels and reorder events, while usage logs show when and how much was used, who recorded it, and the accompanying note.
- Count a whole inventory location from its detail screen with `c`: enter each active item's counted quantity in the unit named on its row, choose a reason and optional notes, review the room, and submit it as one all-or-nothing batch. Counts at or below minimum create reorder requests unless suppressed; a rejected batch keeps every typed value for correction and retry.
- Work the fixture refill queue from Inventory, or open a location's installed fixtures with `f`. From a fixture screen, operators can file a refill request, resolve one pending report, or resolve every report pending when OMS receives the request.
- Complete a reorder request from the Reorder Queue: `f` cycles pending, approved, ordered, and all requests; `a` approves a pending request, `o` marks an approved request ordered, and `d` confirms receipt of an ordered request and credits its item quantity to stock. `x` cancels pending or approved requests. The screen names only the actions valid for the selected request, and reorder quantities are individual items rather than supplier cases.
- Run in-house work orders: uploads remain behind OMS's human gate, pending-review counts are visible in the work-order list and detail, and the terminal can selectively apply or discard parsed readings or explicitly request completion. A scan never closes a work order on its own. From the same detail screen, technicians can add job-specific tools, update where each tool is staged, remove ad-hoc tools, and review and record each structured lockout/tagout step.
- Run vendor work orders from Maintenance: list and filter the seven workflow stages, review quotes and attachments, set the not-to-exceed amount, advance and close the order, and handle emergency authorization, quote waivers, keyfob returns, and variance overrides. Every write is confirmed, and blocked stages show the server's reason.
- Receive deliveries: PO list → PO detail → the receiving flow. The server's receiving worksheet is the whole input — whether the order may be received against and why not, which lines are outstanding and which are settled, and what a scanner reads off each one — so scanning a code finds its line. Tracking barcode, carrier and stated delivery date ride with the receipt (no transit duration is computed from them). What actually arrived is recorded as counted and any difference from the quantity ordered is flagged rather than rounded away; serialized units are captured one at a time with optional lot and expiry, and units credited to stock with no serial naming them are reported back. A line's outstanding balance can be closed short and the whole order marked received, each behind its own confirm; a mistaken close-short can be reopened without erasing its history. Whether the order then advances to `received` is the server's call, and the summary reports the status that came back.
- Purchase-order detail adapts to the terminal: at the standard 80 columns each line keeps its multi-row detail, while a sufficiently wide pane puts each non-kit line on one row with its supplier SKU, full part UUID, and supplier line cost. The fit is measured from the whole order without abbreviating values; orders containing kit lines retain the multi-row form.
- Add a purchase-order line by scanning or typing an identifier: `n` on a draft order's detail sheet takes the supplier's SKU, the item's own SKU, a package or unit barcode, or a name; OMS resolves it against that order's supplier; the item is shown to confirm — naming the other vendor when the code came off a rival's box — and then quantity and price are prompted with the OMS defaults prefilled and overtypable, in the vendor's cases where that item is case-packed (on a repeat add, which grows the line already on the order, the price row starts blank so accepting it cannot reprice that line). Genuine ambiguity offers the candidates to pick from; a refusal (the supplier does not carry it, the order is not a draft) is shown as the server's own sentence. A successful add returns to the identifier box with a running tally, so a stack of boxes is one scan each.
- Take a purchase-order line back off: on the edit sheet (`E` from the order's detail), `Ctrl-E` opens a line's editor and `Ctrl-E` on that editor's status row removes it — **deleting** it outright while the order is still the shop's own draft, **voiding** it with a reason once the supplier holds a copy. Which of the two applies is the server's answer, never a status this side guesses at, so exactly one is ever offered and a server that does not say offers only the reversible half. Deleting takes no reason (a typo's honest record is no line at all) and is confirmed with `Ctrl-X` rather than `Enter`; both confirms name the line they are about to take off. The **void** prompt is the one that warns about the order vanishing: the OMS list endpoint hides an order once it has lines, none of them unvoided, and the supplier already holds it, so voiding the last active line takes the order off every purchase-order list for good — nothing there can add a line or lift a void, and the prompt says so and names `Ctrl+K` search on the order's number as the way back, hedging the loss rather than asserting it where the server did not say which side of that boundary the order is on. A delete cannot do that, because it is offered only while the order is still the shop's own and such an order is always listed. A refusal arrives as the server's own sentence.
- Serialized components: per-unit instance tracking off the item detail (`i`) with inline install/remove/consume/retire/dispose + usage history, an asset's installed-components view, per-unit serial capture during receiving, and the consumption forecast (Reports workspace — days-until-stockout / reorder point / low-stock).
- Asset meters and documents: from an asset detail, `M` opens its meters to create one, record or adjust a reading, and review its reading history; `D` opens its document library to upload a local file, supersede an existing version, or confirm permanent deletion. Meter values retain their units, suspicious entries are confirmed before sending, and superseded documents remain visible and marked.
- Contextual checklists: `K` on an asset, inventory item, or location detail lists the runnable checklists that scan that record; choose one to start it in the existing checklist runner.
- Take a machine out of service, and put it back: `L` on an asset detail opens the interlock — lock, unlock, disable, enable. **Locking is the one that stops the machine**: it records a ForgeKey lockout, so the badge reader denies it. **Disabling only hides the asset from lists** and does not stop it being used, and both the sheet and the confirm say so, because that is the mistake the screen exists to prevent. Locking asks for the reason the server stores on the lockout; every one of the four then confirms, and `Ctrl-X` sends rather than `Enter`, so no reflex can stop or start a machine. The unlock confirm carries the lockout's own reason, actor, level and time, so an operator reads why the machine was stopped before letting it start. Lockouts stack and one unlock clears one of them, so the screen reports the state the server came back with — saying **still locked** when it is — rather than reading "unlocked" off a success. Refusals arrive as the server's own sentence. This is beside the out-of-service records (`o` / `R`), which note that a machine is broken without stopping it; neither implies the other.
- Browse past the first page: the Inventory, Assets, Maintenance and Purchasing lists fetch OMS's next page when the cursor reaches the last loaded row, and the list header says how much of the server's list is loaded (`50 of 173 rows`), which page is loading, and why one failed — the rows already loaded stay on the pane, and the next press on the last row asks again. The Inventory list also searches name, SKU and description with `/`, and `f` cycles all items, low stock, in stock, and retired-and-empty hidden, sending the same parameters as the web inventory list; category and location filters remain web-only.
- Kits: browse and search the dedicated kit list under Inventory, create a kit with its bill of materials, and edit its components with per-kit quantities from the item form. A kit's detail opens like any other item's and a kit PO line says which component items receiving it will credit instead of the kit's own stock. Kits remain absent from the ordinary inventory-item list because OMS exposes them through its dedicated kit API.
- Enforce project-storage stints from the stint sheet: send an expiry notice, move warned projects to purgatory, review a member's stint history, and generate or regenerate the stint QR alongside the existing removal and label-reprint actions. OMS remains the authority for permissions and lifecycle refusals.

Not yet landed (the long tail):
- Auth/login screen and persistent token storage. Today, tokens come from `SCANTTY_OMS_TOKEN`.
- Cache *reads* — the cache layer is wired but every list/detail still hits the network on every refresh. The plan is to read-through on network failure, then refresh asynchronously when connectivity returns.
 - Detail screens for SIGs and donations.
- ForgeKey device detail + command actions (enable/disable/identify/blink/firmware).
- Authorization create/revoke + classroom-mode QR enroll.
- ForgeKey occupancy sparkline.
- Badge-scan path — needs an OMS member-by-badge endpoint or a documented convention for resolving badge → user.
- Raw-HID and serial scanner sources (only stdin/keyboard-emulation today).

## Building and running

```sh
go build ./cmd/scantty           # produces ./scantty (TUI)
go build ./cmd/oms-claim-print   # produces ./oms-claim-print (Pi daemon)
go vet ./...                     # static checks
go run ./cmd/scantty             # build + run in one step (good for iteration)
```

`go test ./...` runs the ordinary suite and finishes inside Go's default
timeout. It deliberately leaves out `internal/tui`'s heavy tests — the
exhaustive layout sweeps that walk every screen at every drawable size, listed
in `internal/tui/testdata/heavy_tests.txt` — which on their own take several
minutes. Run those with an environment variable:

```sh
go test ./...                                                   # ordinary suite
SCANTTY_TUI_TESTS=heavy   go test -timeout 30m ./internal/tui   # only the heavy tests
SCANTTY_TUI_TESTS=heavy:2 go test ./internal/tui                # one CI shard of them
SCANTTY_TUI_TESTS=all     go test -timeout 30m ./internal/tui   # everything, as one run
go test -run TestJDEForm_NoRowRunsPastThePane ./internal/tui    # naming a test runs it
```

CI (`.github/workflows/ci.yml`) runs build, vet and the ordinary suite, and the
heavy tests in shard jobs of their own, on every pull request — every test runs
in exactly one of them (`internal/tui/test_schedule_test.go` owns that). The
suite needs no server: screen behaviour
is verified by driving the real screens against an `httptest` fake, and the two
`omslab`-tagged tests that do want a real backend are inert without one. A
local OpenMakerSuite can be brought up and is worth it on a bug that sits on the
ScanTTY/OMS seam — the recipe and its traps are in `AGENTS.md`.

## oms-claim-print — Pi-side claim-tag print daemon

A second binary that lives alongside scantty in this repo. It runs on a
Raspberry Pi attached to an Epson TM receipt printer and drains the OMS
project-storage print queue. Labels are encoded as ESC/POS raster and
written straight to the printer's USB character device — no CUPS, no
cupsd, no per-host print queue.

Configure via env (the `OMS_API_*` vars match the legacy Python daemon in
OMS; the printer target moved from CUPS to the `OMS_ESCPOS_*` vars):

| Variable | Purpose | Default |
|---|---|---|
| `OMS_API_BASE` | OMS base URL (no `/api` suffix) | *(required)* |
| `OMS_API_TOKEN` | Bearer if the print-queue endpoint is gated | unset (AllowAny) |
| `OMS_POLL_INTERVAL_S` | Seconds between queue polls | `10` |
| `OMS_ESCPOS_DEVICE` | usblp character device | `/dev/usb/lp0` |
| `OMS_ESCPOS_WIDTH_DOTS` | Printhead width in dots (80mm=576, 58mm=512) | `576` |
| `OMS_ESCPOS_CUT` | Partial-cut after each label | `true` |

> `OMS_EPSON_CUPS_QUEUE` is retired — the CUPS backend was removed. If it
> is still set, the daemon logs a warning and ignores it.

The printer's USB node (`/dev/usb/lp0`) is usually `root:lp` mode `0660`,
so the daemon's user must be in the `lp` group (the systemd unit sets
`SupplementaryGroups=lp`) or a udev rule must grant write access.

Cross-build for arm64 Pis:

```sh
GOOS=linux GOARCH=arm64 go build -o oms-claim-print-arm64 ./cmd/oms-claim-print
```

Install:

```sh
sudo install -d /opt/oms-claim-print
sudo install -m 0755 oms-claim-print-arm64 /opt/oms-claim-print/oms-claim-print
sudo install -m 0644 systemd/oms-claim-print.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now oms-claim-print.service
journalctl -u oms-claim-print.service -f
```

OMS endpoints consumed: `GET /api/project-storage/stints/print-queue/`,
`GET <queue entry's label_url>` verbatim, `POST /api/project-storage/stints/<id>/mark-printed/`.

## Contributing

This is a `uid0`-owned makerspace tool. PRs welcome via GitHub, but coordinate with the maintainer first on the OpenMakerSuite/ForgeKey side if the change implies an API addition (for example, a badge-resolution endpoint).

## License

TBD — talk to uid0.
