#!/usr/bin/env bash
# omakase status — thin shim onto the omakase Go binary (v2 design §10: the entry
# point is frozen; the binary owns the behavior). In the dev repo the shim rebuilds
# the binary (go build is incremental and near-instant when clean) so it never runs
# stale — a FAILING build aborts loudly on purpose (set -e), because falling back to
# a stale dist/omakase or the v1 body would mask Go breakage. Only where the binary
# cannot be RESOLVED at all (a vendored dist without Go on PATH) does it fall back
# to the preserved v1 bash body, so downstream status keeps working until the
# Phase 6 bootstrap ships. OMAKASE_BIN overrides resolution (tests, CI).
set -euo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BIN="${OMAKASE_BIN:-$HERE/../dist/omakase}"
if [ -z "${OMAKASE_BIN:-}" ] && [ -f "$HERE/../go.mod" ] && command -v go >/dev/null 2>&1; then
  ( cd "$HERE/.." && go build -o dist/omakase ./cmd/omakase )
fi
if [ -x "$BIN" ]; then
  exec "$BIN" status "$@"
fi
echo "omakase: Go binary not present — running the bundled v1 status script" >&2
exec bash "$HERE/legacy/status.sh" "$@"
