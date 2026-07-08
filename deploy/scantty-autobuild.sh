#!/usr/bin/env bash
# scantty-autobuild.sh — keep the local ScanTTY binary fresh from origin/main.
#
# Designed to run unattended on a timer (see deploy/README.md). It is
# idempotent and fail-safe: on any doubt it logs a warning and leaves the
# working tree untouched. Concretely it:
#
#   1. fetches origin/main;
#   2. rebuilds ONLY when origin/main advanced past the last built commit
#      (or the binary is missing);
#   3. refuses to move the tree if HEAD is not on main, or if there are
#      uncommitted changes outside the agent scratch dirs (.agents/.codex/.gc)
#      — it never clobbers local WIP and never switches you off a branch;
#   4. restarts scantty.service IFF such a unit is active. ScanTTY is normally
#      an interactive TUI with no service, so that step is usually a no-op and
#      this script NEVER kills an interactive session — it only refreshes the
#      binary so the next launch runs current code.
#
# Everything is logged. State (the last-built stamp + log) lives OUTSIDE the
# repo so the working tree stays clean and the WIP guard never trips on it.

set -uo pipefail

# --- Locations -------------------------------------------------------------
# Repo root = the parent of this script's dir (deploy/ -> repo root).
SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
REPO="$(cd -- "$SCRIPT_DIR/.." && pwd)"

STATE_DIR="${XDG_STATE_HOME:-$HOME/.local/state}/scantty-autobuild"
STAMP="$STATE_DIR/last-built" # commit SHA of the last successful build
LOG="$STATE_DIR/autobuild.log"
mkdir -p "$STATE_DIR"

# Keep the log from growing without bound (~1 MiB -> last 500 lines).
if [ -f "$LOG" ] && [ "$(wc -c <"$LOG" 2>/dev/null || echo 0)" -gt 1048576 ]; then
  tail -n 500 "$LOG" >"$LOG.tmp" 2>/dev/null && mv "$LOG.tmp" "$LOG"
fi

# log  -> file + stderr (journal): actions, warnings, errors.
# logf -> file only: quiet, high-frequency "nothing to do" lines.
_ts() { date -u +%Y-%m-%dT%H:%M:%SZ; }
log() { printf '%s %s\n' "$(_ts)" "$*" | tee -a "$LOG" >&2; }
logf() { printf '%s %s\n' "$(_ts)" "$*" >>"$LOG"; }

# --- Go toolchain on PATH --------------------------------------------------
# bare `go` is not on PATH on this host; prefer goenv's toolchain, else rely on
# whatever `go` resolves to. GOTOOLCHAIN=auto lets `go` fetch the go.mod-pinned
# version if the on-PATH toolchain is older.
if command -v go >/dev/null 2>&1; then
  :
elif [ -x /opt/goenv/versions/1.25.7/bin/go ]; then
  PATH="/opt/goenv/versions/1.25.7/bin:$PATH"
fi
export PATH
export GOTOOLCHAIN=auto

if ! command -v go >/dev/null 2>&1; then
  log "FATAL: no 'go' toolchain on PATH (install Go or add goenv to PATH); aborting"
  exit 1
fi

# Binary output path. `go build -o scantty ./cmd/scantty` from the repo root
# matches the README ("produces ./scantty"). In a clean checkout OUT is written
# as a file; in Ian's working tree a stray ./scantty DIRECTORY exists, so Go
# writes ./scantty/scantty instead — either way that is the binary the launcher
# runs, so we let Go resolve it and just log the path it actually produced.
OUT="$REPO/scantty"

binary_present() {
  if [ -d "$OUT" ]; then
    [ -x "$OUT/scantty" ]
  else
    [ -x "$OUT" ]
  fi
}

build_current() {
  local sha="$1" built
  log "building ${sha:0:7} -> go build -o scantty ./cmd/scantty"
  if ! go build -trimpath -o "$OUT" ./cmd/scantty 2>>"$LOG"; then
    log "FATAL: go build failed (see $LOG)"
    return 1
  fi
  if [ -d "$OUT" ]; then built="$OUT/scantty"; else built="$OUT"; fi
  log "built OK: $built"
  return 0
}

cd "$REPO" || {
  log "FATAL: cannot cd to repo $REPO"
  exit 1
}

# --- 1. Fetch --------------------------------------------------------------
if ! git fetch --quiet origin main 2>>"$LOG"; then
  log "WARN: 'git fetch origin main' failed (offline?); skipping this run"
  exit 0
fi

TARGET="$(git rev-parse origin/main 2>/dev/null || true)"
if [ -z "$TARGET" ]; then
  log "WARN: cannot resolve origin/main; skipping"
  exit 0
fi
LAST="$(cat "$STAMP" 2>/dev/null || true)"

# --- 2. Already current? ---------------------------------------------------
# Rebuild only when origin/main advanced OR the binary is missing.
if [ "$TARGET" = "$LAST" ] && binary_present; then
  logf "up-to-date at ${TARGET:0:7}; nothing to do"
  exit 0
fi

# --- 3a. Branch guard: only ever update while sitting on main --------------
CURRENT_BRANCH="$(git symbolic-ref --quiet --short HEAD 2>/dev/null || echo '(detached)')"
if [ "$CURRENT_BRANCH" != "main" ]; then
  log "WARN: HEAD is '$CURRENT_BRANCH' (not main) — skipping to avoid moving you off your branch"
  exit 0
fi

# --- 3b. WIP guard: never reset over uncommitted work ----------------------
# Any uncommitted change OUTSIDE .agents/.codex/.gc means a human or agent has
# WIP here; do NOT reset --hard over it. gitignored files (the binary, .claude/,
# *.db, ...) never appear in --porcelain, so they don't trip this.
WIP="$(git status --porcelain --untracked-files=all 2>/dev/null |
  sed -E 's/^.{3}//' |
  grep -vE '^"?\.(agents|codex|gc)(/|"?$)' ||
  true)"
if [ -n "$WIP" ]; then
  log "WARN: uncommitted changes outside .agents/.codex/.gc — skipping update to protect local WIP:"
  printf '%s\n' "$WIP" | sed 's/^/      /' | tee -a "$LOG" >&2
  exit 0
fi

# --- 4. Update to origin/main and rebuild ----------------------------------
FROM="${LAST:0:7}"
[ -n "$FROM" ] || FROM="none"
log "updating ${FROM}..${TARGET:0:7} on main"
if ! git reset --hard "$TARGET" 2>>"$LOG"; then
  log "FATAL: 'git reset --hard $TARGET' failed"
  exit 1
fi

if ! build_current "$TARGET"; then
  exit 1
fi
printf '%s\n' "$TARGET" >"$STAMP"
log "recorded built commit ${TARGET:0:7}"

# --- 5. Restart scantty.service only if one is active ----------------------
# ScanTTY is normally an interactive TUI with no service (no-op path). We check
# the user manager first (that is where these units live), then the system
# manager. We never start a service that is not already running, and never
# disturb an interactive session.
if systemctl --user is-active --quiet scantty.service 2>/dev/null; then
  log "scantty.service active (user) — restarting to pick up the new binary"
  systemctl --user restart scantty.service 2>>"$LOG" || log "WARN: user restart failed"
elif systemctl is-active --quiet scantty.service 2>/dev/null; then
  log "scantty.service active (system) — restarting to pick up the new binary"
  systemctl restart scantty.service 2>>"$LOG" || log "WARN: system restart failed (needs privilege?)"
else
  log "no active scantty.service — binary refreshed; relaunch ScanTTY to run current code"
fi

log "done at ${TARGET:0:7}"
