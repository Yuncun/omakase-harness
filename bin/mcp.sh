#!/usr/bin/env bash
# omakase mcp — thin shim onto the omakase Go binary. Resolution failure is an
# error (exit 1), like every shim. Stdout belongs to the MCP stdio transport:
# everything this shim prints goes to stderr. Register with:
#   claude mcp add omakase -- /path/to/omakase-harness/bin/mcp.sh
set -euo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
. "$HERE/lib-omakase-bin.sh"
if resolve_omakase fetch; then
  exec "$OMAKASE_BIN_RESOLVED" mcp "$@"
fi
echo "omakase: mcp needs the omakase binary and none could be resolved or fetched. Install one (brew, or a release tarball from github.com/Yuncun/omakase-harness/releases), or set OMAKASE_BIN=/path/to/omakase, then re-run." >&2
exit 1
