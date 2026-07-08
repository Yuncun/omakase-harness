#!/usr/bin/env bash
# omakase init — thin shim onto the omakase Go binary (v2 design §10: the entry
# point is frozen; the binary owns the behavior). Resolution lives in
# lib-omakase-bin.sh: OMAKASE_BIN override -> dev rebuild (a FAILING build aborts
# loudly on purpose — falling back to a stale binary would mask Go breakage) ->
# dist/omakase -> `omakase` on PATH -> the pinned, checksum-verified release
# binary fetched once per machine into the cache. Only when NOTHING resolves
# does it fall back to the preserved v1 bash body.
set -euo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
. "$HERE/lib-omakase-bin.sh"
if resolve_omakase fetch; then
  exec "$OMAKASE_BIN_RESOLVED" init "$@"
fi
echo "omakase: Go binary not present — running the bundled v1 init script" >&2
exec bash "$HERE/legacy/init.sh" "$@"
