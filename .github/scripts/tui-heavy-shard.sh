#!/usr/bin/env bash
# Runs one shard of internal/tui's heavy tests and then PROVES it ran them.
#
#   .github/scripts/tui-heavy-shard.sh <shard> [go test flags...]
#
# internal/tui/test_schedule_test.go owns what a shard is; this script is CI's
# half of the proof that every heavy test runs somewhere. A shard whose pattern
# matched nothing would pass `go test` with "no tests to run", so passing is not
# enough: every name internal/tui/testdata/heavy_tests.txt marks for this shard
# must have reported `--- PASS:` at top level, or the job fails naming it.
#
# The output is run with -v, because the PASS lines are the evidence, and
# filtered as it streams: a sweep's thousands of `=== RUN` / `--- PASS` subtest
# lines are dropped from the log while failures, panics and a timeout's
# goroutine dump are not. The unfiltered log is kept for the check.
set -euo pipefail

shard="${1:?usage: tui-heavy-shard.sh <shard> [go test flags...]}"
shift
roster=internal/tui/testdata/heavy_tests.txt
log="$(mktemp)"

set +e
SCANTTY_TUI_TESTS="heavy:${shard}" go test -v "$@" ./internal/tui 2>&1 |
	tee "$log" |
	grep --line-buffered -vE '^[[:space:]]*(=== (RUN|PAUSE|CONT|NAME) |--- PASS: )'
status="${PIPESTATUS[0]}"
set -e

expected=0
missing=0
while read -r s name; do
	[[ "$s" == "$shard" ]] || continue
	expected=$((expected + 1))
	if ! grep -qE "^--- PASS: ${name} \(" "$log"; then
		echo "::error::${name} is marked for heavy shard ${shard} in ${roster} and did not report a top-level PASS"
		missing=$((missing + 1))
	fi
done < <(grep -vE '^[[:space:]]*(#|$)' "$roster")

if [[ "$expected" -eq 0 ]]; then
	echo "::error::${roster} marks no test for shard ${shard}; this job ran nothing"
	exit 1
fi
echo "heavy shard ${shard}: ${expected} listed, $((expected - missing)) reported PASS, go test exit ${status}"
if [[ "$status" -ne 0 ]]; then
	exit "$status"
fi
[[ "$missing" -eq 0 ]]
