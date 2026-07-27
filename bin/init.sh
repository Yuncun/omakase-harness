#!/usr/bin/env bash
# omakase init — thin shim onto the omakase Go binary (v2 design §10: the entry
# point is frozen; the binary owns the behavior). Resolution lives in
# lib-omakase-bin.sh: OMAKASE_BIN override -> dev rebuild (a FAILING build aborts
# loudly on purpose — falling back to a stale binary would mask Go breakage) ->
# dist/omakase -> `omakase` on PATH -> the stable machine copy an earlier init
# self-installed. No network tier: when NOTHING resolves the shim fails closed
# with the one-line brew instruction (issue #182 — a silent fallback would mask
# a broken install; a silent download would hide the install story).
set -euo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
. "$HERE/lib-omakase-bin.sh"
if resolve_omakase; then
  exec "$OMAKASE_BIN_RESOLVED" init "$@"
fi
omakase_refuse_missing_binary init
exit 1
