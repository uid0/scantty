# scantty

Scanner-driven curses TUI for [OpenMakerSuite](https://github.com/uid0/openmakersuite) and [ForgeKey](https://github.com/uid0/forgekey). Designed for shop-floor terminals where a barcode/HID scanner is the primary input device and bandwidth is unreliable.

## What it does

- **Scan a 6-character OMS code** → look up the item/asset/location/fixture → open its detail screen → start a reorder.
- **Scan an 8–20 char hex badge** → resolve to a ForgeKey-authorized member (planned; needs an OMS member-by-badge endpoint).
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
| `SENTRY_DSN` | Override the baked-in Sentry DSN (e.g. point at a personal sandbox). The default reports panics + `run()` errors to the `scantty` project on the self-hosted Sentry — public DSN, safe to commit. | baked-in |
| `SENTRY_DISABLED` | Set to `1` (or `true`) to disable Sentry entirely. | unset |
| `SENTRY_ENVIRONMENT` | Sentry environment tag (`dev`, `staging`, `prod`). | `dev` |
| `SENTRY_RELEASE` | Override the release identifier. Defaults to `scantty@<vcs.revision[:12]>` from the build's debug info when available. | derived |

If `SCANTTY_OMS_TOKEN` is unset, scantty still works for the `AllowAny` endpoints — barcode lookup, scanning items/assets/fixtures, and creating reorder requests in kiosk mode all function unauthenticated. Receiving deliveries (`/api/reorders/receipts/`) and most other writes require a token.

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
| `Ctrl+K` | Search palette (items, assets, orders, people) — works from anywhere |
| `Ctrl+C` / `Ctrl+Q` | Quit, from anywhere |

On a **list** screen the letters in that foot carry case: lowercase acts on the
list you are looking at (`s` sort, `f` filter, `r` refresh, `n` new), and
uppercase leaves it for a sibling surface of the same workspace — `N` for a new
purchase order from Purchasing, for example. Each list names its own along its
foot; they are shortcuts to surfaces the sidebar tree already carries, never the
only way there.

The purchasing **viewing** screens — a purchase order's detail sheet and its
attachments, associations and terms — are drawn in the fixed columnar JD Edwards
World style and follow its reduced key scheme instead: no `j`/`k` or `g`/`G`
aliases (arrows, `PgUp`/`PgDn` and `Home`/`End` only), and `Enter` fires the
screen's own action rather than opening a row. Each of those screens carries a
persistent action bar naming every key that works there, and a key the bar does
not name does nothing. The rest of the app is being converted screen by screen.

The sidebar lists the eleven workspaces; the workspace you are in also shows its
own surfaces indented beneath it (Inventory › New item / Categories / Locations
/ Suppliers, ForgeKey › Firmware / Lockouts / …). Facilities and Reports open a
cursor menu of their surfaces instead.

## Scanner input

Most barcode scanners present themselves as USB HID keyboards. With `SCANTTY_SCANNER_SOURCE=stdin` (the default) the scan workspace simply reads typed characters until Enter — the scanner appends `\n` after the code by default, so a single trigger pull populates and submits the field.

For non-keyboard scanners (raw HID, serial, etc.), point `SCANTTY_SCANNER_SOURCE` at a character device path. (The raw-HID path isn't wired yet — see Roadmap.)

The classifier in `internal/scanner` distinguishes two scan kinds:

- **OMS 6-char code** — exactly 6 alphanumeric characters. Routes to `GET /api/inventory/lookup-code/`.
- **ForgeKey badge** — 8–20 hex characters. Routes to member-resolution (still stubbed — see Roadmap).

## Project layout

```
.
├── cmd/
│   ├── scantty/main.go           # TUI entrypoint: load config, build clients, hand off to bubbletea
│   └── oms-claim-print/main.go   # Pi-side claim-tag print daemon (Epson TM via ESC/POS to USB)
├── systemd/
│   └── oms-claim-print.service   # drop-in unit for the Pi daemon
├── internal/
│   ├── config/                   # env-var loader, validation
│   ├── scanner/                  # scan-code classifier + stdin reader
│   ├── cache/                    # pure-Go SQLite (modernc.org/sqlite); resources, lookups, pending actions
│   ├── omsapi/                   # OMS HTTP client (JWT bearer, refresh on 401, stable error envelope, generic page iterator)
│   │   ├── client.go             # base + auth
│   │   ├── errors.go             # APIError + envelope parsing
│   │   ├── inventory.go          # items, assets, locations, categories, suppliers, item-suppliers, fixtures, lookup-code
│   │   ├── reorders.go           # reorder requests, purchase orders, receipts
│   │   ├── membership.go         # profile, SIGs, certifications
│   │   ├── workorders.go         # internal WOs + third-party (maintenance-orders)
│   │   ├── donations.go          # donations + tax receipts
│   │   └── search.go             # /api/search/, dashboard summary
│   ├── forgekeyapi/              # ForgeKey HTTP client (mTLS-capable transport)
│   │   ├── client.go             # base + mTLS + bearer
│   │   ├── devices.go            # devices, command actions (enable/disable/identify/blink/firmware), occupancy
│   │   └── authorizations.go     # authorizations, lockouts, operational modes, usage sessions
│   └── tui/                      # bubbletea root, screens, styles
│       ├── app.go                # root model + workspace router
│       ├── route.go              # workspace types, screen interface, switch/status messages
│       ├── styles.go             # lipgloss palette + status rendering
│       ├── nav.go                # sidebar menu tree — workspaces + their surfaces
│       ├── status.go             # bottom status bar (OMS/FK conn dots, scanner state, flash messages)
│       ├── welcome.go            # default landing screen
│       ├── scan.go               # scan input + recent-scan history + auto-navigate on item match
│       ├── list.go               # generic paginated list with loaders for items/assets/POs/WOs/SIGs/FK devices
│       ├── inventory_detail.go   # item detail + supplier list
│       ├── reorder_form.go       # reorder request form
│       ├── po_detail.go          # purchase order detail + line items
│       ├── receive_form.go       # per-line receipt entry
│       └── settings.go           # env-var inventory display
└── go.mod
```

## Architecture notes

**Why pure-Go SQLite (`modernc.org/sqlite`)?** No CGo means a static binary across the shop's mix of x86 laptops and ARM kiosks, with no `libsqlite3` system dependency. Throughput is fine for the cache workload (small, low-frequency writes).

**Why bubbletea over tview/termbox?** Better composition story (every screen is a `Screen` interface implementer), cleaner message-passing for async API calls, and the `bubbles/textinput` widget covers our form needs without a giant widget toolkit. The downside is more boilerplate per screen, but the per-screen code stays readable.

**Why mirror the web UI's 8 workspaces?** Members already have a mental map of where things live in the browser. Scantty's nav uses the same names and groupings so muscle memory transfers. Scanner-priority surfaces (the scan workspace itself) sit at the top of the sidebar menu so they're never more than a keypress away.

**Error envelope handling.** OMS returns a stable `{error: {code, message, details}}` shape on failure. `omsapi.APIError` parses this and exposes `IsAuth()`/`IsNotFound()` helpers. The client switches on `code`, not HTTP status, because OMS sometimes returns 400 with informative codes and sometimes 422 — the code is authoritative.

**JWT refresh.** A 401 with a refresh token in scope triggers one transparent retry against `/api/auth/refresh/`. There's no automatic logout — if refresh fails too, the call surfaces a `not_authenticated` error and the next request will fail the same way until the user re-supplies a token.

**ForgeKey trust refactor.** ForgeKey is mid-migration from MAC + JWT-password to device_id + mTLS. `forgekeyapi.New(Options{ClientCert, ClientKey, CACert})` already loads an mTLS keypair when supplied; bearer-token auth still works during the transition. Once the refactor lands, scantty will need to be enrolled as a trusted device and provisioned with a cert at `SCANTTY_FORGEKEY_CLIENT_CERT`/`KEY`.

## Current scope

Landed:
- Foundation: clients, cache, scanner classifier, config, TUI shell with 11 workspaces.
- End-to-end scanner flow: scan → lookup → inventory detail → reorder form → submit.
- Receive deliveries: PO list → PO detail → line-by-line qty entry → submit.
- Add a purchase-order line by scanning or typing an identifier: `n` on a draft order's detail sheet takes the supplier's SKU, the item's own SKU, a package or unit barcode, or a name; OMS resolves it against that order's supplier; the item is shown to confirm — naming the other vendor when the code came off a rival's box — and then quantity and unit cost are prompted with the OMS defaults prefilled and overtypable (on a repeat add, which grows the line already on the order, the price row starts blank so accepting it cannot reprice that line). Genuine ambiguity offers the candidates to pick from; a refusal (the supplier does not carry it, the order is not a draft) is shown as the server's own sentence. A successful add returns to the identifier box with a running tally, so a stack of boxes is one scan each.
- Serialized components: per-unit instance tracking off the item detail (`i`) with inline install/remove/consume/retire/dispose + usage history, an asset's installed-components view, per-unit serial capture during receiving, and the consumption forecast (Reports workspace — days-until-stockout / reorder point / low-stock).
- Kits: a kit is tagged as one on the item detail and shows its components with per-kit quantities, its bill of materials is editable from the item form (saved with the kit), and a kit PO line says which component items receiving it will credit instead of the kit's own stock. A kit's detail screen opens from its id the same way any other item's does; kits are absent from the inventory list and cannot be created from scantty (the item API excludes kits and exposes no `is_kit` flag, so a listed kit would be indistinguishable from an ordinary item — see `AGENTS.md`).

Not yet landed (the long tail):
- Auth/login screen and persistent token storage. Today, tokens come from `SCANTTY_OMS_TOKEN`.
- Cache *reads* — the cache layer is wired but every list/detail still hits the network on every refresh. The plan is to read-through on network failure, then refresh asynchronously when connectivity returns.
- Detail screens for assets, work orders, SIGs, donations.
- ForgeKey device detail + command actions (enable/disable/identify/blink/firmware).
- Authorization create/revoke + classroom-mode QR enroll.
- Lockout flows with hierarchical unlock.
- ForgeKey occupancy sparkline.
- Global search palette (Cmd-K equivalent).
- Badge-scan path — needs an OMS member-by-badge endpoint or a documented convention for resolving badge → user.
- Raw-HID and serial scanner sources (only stdin/keyboard-emulation today).

## Building and running

```sh
go build ./cmd/scantty           # produces ./scantty (TUI)
go build ./cmd/oms-claim-print   # produces ./oms-claim-print (Pi daemon)
go vet ./...                     # static checks
go run ./cmd/scantty             # build + run in one step (good for iteration)
```

`go test ./...` runs the suite; CI (`.github/workflows/ci.yml`) runs build,
vet and test on every pull request. No live OMS is reachable from a checkout,
so screen behaviour is verified by driving the real screens against an
`httptest` fake — see `AGENTS.md`.

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
