#!/usr/bin/env bash
# omakase init — thin shim onto the omakase Go binary (v2 design §10: the entry
# point is frozen; the binary owns the behavior). Resolution lives in
# lib-omakase-bin.sh: OMAKASE_BIN override -> dev rebuild (a FAILING build aborts
# loudly on purpose — falling back to a stale binary would mask Go breakage) ->
# dist/omakase -> `omakase` on PATH -> the pinned, checksum-verified release
# binary fetched once per machine into the cache. When NOTHING resolves the shim
# fails closed: error + exit 1 (a silent fallback would mask a broken install).
set -euo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
. "$HERE/lib-omakase-bin.sh"
if resolve_omakase fetch; then
  exec "$OMAKASE_BIN_RESOLVED" init "$@"
fi
echo "omakase: init needs the omakase binary and none could be resolved or fetched. Install one (brew, or a release tarball from github.com/Yuncun/omakase-harness/releases), or set OMAKASE_BIN=/path/to/omakase, then re-run." >&2
exit 1
