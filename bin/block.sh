#!/usr/bin/env bash
# omakase block — thin shim onto the omakase Go binary (v2 design §10: the entry
# point is frozen; the binary owns the behavior). Resolution lives in
# lib-omakase-bin.sh; no network tier — when nothing resolves the shim fails
# closed with the one-line brew instruction (issue #182).
set -euo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
. "$HERE/lib-omakase-bin.sh"
if resolve_omakase; then
  exec "$OMAKASE_BIN_RESOLVED" block "$@"
fi
omakase_refuse_missing_binary block
exit 1
