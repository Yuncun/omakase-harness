#!/usr/bin/env bash
# TEST PLUMBING ONLY (since the plugin fold, #211): a thin entry point the
# shell test suites drive; users and agents run `omakase init` directly.
set -euo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
. "$HERE/lib-omakase-bin.sh"
if resolve_omakase; then
  exec "$OMAKASE_BIN_RESOLVED" init "$@"
fi
omakase_refuse_missing_binary init
exit 1
