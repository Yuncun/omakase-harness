#!/usr/bin/env bash
# omakase remove — thin shim onto the omakase Go binary (v2 design §10: the entry
# point is frozen; the binary owns the behavior). Resolution lives in
# lib-omakase-bin.sh: OMAKASE_BIN override -> dev rebuild (a FAILING build aborts
# loudly on purpose — falling back to a stale binary would mask Go breakage) ->
# dist/omakase -> `omakase` on PATH -> the omakase-managed cache (already-fetched
# binary only — remove never triggers a network fetch, so uninstall stays
# offline). When NOTHING resolves the shim fails closed: error + exit 1 (a
# silent fallback would mask a broken install).
set -euo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
. "$HERE/lib-omakase-bin.sh"
if resolve_omakase; then
  exec "$OMAKASE_BIN_RESOLVED" remove "$@"
fi
echo "omakase: remove needs the omakase binary and none could be resolved. remove never downloads (uninstall stays offline), so a local or already-cached binary is required — install one (brew, or a release tarball from github.com/Yuncun/omakase-harness/releases), or set OMAKASE_BIN=/path/to/omakase, then re-run." >&2
exit 1
