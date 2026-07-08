#!/usr/bin/env bash
# omakase remove — thin shim onto the omakase Go binary (v2 design §10: the entry
# point is frozen; the binary owns the behavior). Resolution lives in
# lib-omakase-bin.sh: OMAKASE_BIN override -> dev rebuild (a FAILING build aborts
# loudly on purpose — falling back to a stale binary would mask Go breakage) ->
# dist/omakase -> `omakase` on PATH -> the omakase-managed cache (already-fetched
# binary only — remove never triggers a network fetch, so uninstall stays
# offline). Only when NOTHING resolves does it fall back to the preserved v1
# bash body.
set -euo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
. "$HERE/lib-omakase-bin.sh"
if resolve_omakase; then
  exec "$OMAKASE_BIN_RESOLVED" remove "$@"
fi
echo "omakase: Go binary not present — running the bundled v1 remove script" >&2
exec bash "$HERE/legacy/remove.sh" "$@"
