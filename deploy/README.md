# ScanTTY deploy — keep the running binary fresh

ScanTTY has no long-running server: it's an interactive TUI you launch as
`./scantty`. The failure mode this directory fixes is that a merged source
change (say #95) sits in `git` on `main` while the binary you actually run
stays stale until someone rebuilds by hand.

Two halves solve it:

1. **CI (`.github/workflows/ci.yml`, already active on merge).** Every push to
   `main` builds prebuilt `linux/amd64` binaries and attaches them to a GitHub
   Release. Any host can then pull a build without a Go toolchain.
2. **Host auto-build (this directory).** A user systemd timer runs
   [`scantty-autobuild.sh`](./scantty-autobuild.sh) every 5 minutes. It fast-
   forwards the local checkout to `origin/main` and rebuilds `./scantty`, so the
   next time you launch the TUI you're running current code.

The script keeps the **binary** fresh — it does **not** kill your interactive
session. If (and only if) a `scantty.service` unit is ever running, it restarts
that instead.

## What the script does (and refuses to do)

[`scantty-autobuild.sh`](./scantty-autobuild.sh) is idempotent and fail-safe:

- Fetches `origin/main`; rebuilds only when it advanced past the last build
  (tracked by a stamp file) or the binary is missing.
- **Never clobbers local WIP.** If there are uncommitted changes outside the
  agent scratch dirs (`.agents/`, `.codex/`, `.gc/`), it logs a warning and
  makes no changes.
- **Never switches your branch.** If `HEAD` isn't on `main`, it skips.
- Only after those guards pass does it `git reset --hard origin/main` and
  `go build -o scantty ./cmd/scantty`.
- Restarts `scantty.service` only if such a unit is already active (normally a
  no-op — ScanTTY is a TUI, not a service).

State lives **outside** the repo so the working tree stays clean:

| Path | What |
| --- | --- |
| `${XDG_STATE_HOME:-~/.local/state}/scantty-autobuild/last-built` | SHA of the last successful build |
| `${XDG_STATE_HOME:-~/.local/state}/scantty-autobuild/autobuild.log` | rolling log (auto-trimmed at ~1 MiB) |

## Requirements

- A **Go** toolchain matching `go.mod` (currently `go 1.26.4`). `go` on `PATH`
  is used if present; otherwise the script falls back to
  `/opt/goenv/versions/1.25.7/bin/go`, and sets `GOTOOLCHAIN=auto` so `go` can
  fetch the pinned toolchain. If neither is available, adjust the `PATH` block
  near the top of the script.
- The checkout at `~/gascity/scantty` (or edit `ExecStart`/`WorkingDirectory`
  in the `.service` unit to point at your clone).

## One-time host install (run once, as the `ian` user)

> This is the manual step the mayor/Ian runs on the host to activate auto-build.
> It is intentionally **not** automated by the merge.

```bash
cd ~/gascity/scantty

# 1. Make sure the script is executable (it ships +x, but belt-and-suspenders).
chmod +x deploy/scantty-autobuild.sh

# 2. Install the user units.
mkdir -p ~/.config/systemd/user
cp deploy/scantty-autobuild.service ~/.config/systemd/user/
cp deploy/scantty-autobuild.timer   ~/.config/systemd/user/
systemctl --user daemon-reload

# 3. Enable + start the timer.
systemctl --user enable --now scantty-autobuild.timer

# 4. Let user units run without an active login session (so it keeps building
#    even when you're not logged in). Needs sudo once.
sudo loginctl enable-linger "$USER"
```

Verify it's live:

```bash
systemctl --user list-timers scantty-autobuild.timer
systemctl --user status  scantty-autobuild.service   # last oneshot run
tail -f ~/.local/state/scantty-autobuild/autobuild.log
```

Run a build check immediately instead of waiting for the timer:

```bash
systemctl --user start scantty-autobuild.service
# ...or just run the script directly:
deploy/scantty-autobuild.sh
```

### Simpler alternative: cron

If you'd rather not use systemd units, a single crontab line does the same job
(the script self-logs, so no redirection is required):

```cron
*/5 * * * * /home/ian/gascity/scantty/deploy/scantty-autobuild.sh >/dev/null 2>&1
```

## Pulling a prebuilt binary instead of building locally

Every merge to `main` publishes a Release. On any `linux/amd64` host with the
`gh` CLI:

```bash
gh release download --repo uid0/scantty --pattern 'scantty' --clobber
chmod +x scantty
./scantty
```

## Uninstall

```bash
systemctl --user disable --now scantty-autobuild.timer
rm ~/.config/systemd/user/scantty-autobuild.{service,timer}
systemctl --user daemon-reload
```
