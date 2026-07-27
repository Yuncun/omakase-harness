#!/usr/bin/env bash
# Plugin SessionStart hook: heal the overlay when a session opens (#164 C5,
# narrow scope). The git hooks only heal on git events, so "overlay wiped,
# new session opens" stayed silently broken until now.
#
# Contract — this must NEVER fail or noticeably delay a session start:
#   - not a git repo / no omakase state here -> the binary exits 0 silently
#   - no binary on this machine yet          -> exit 0 silently; NO network
#     fetch at session start (the first skill use fetches it)
#   - overlay intact                         -> the binary exits 0 silently
#   - placed files missing                   -> restored from the snapshot,
#     one line on stdout saying so
# Both hosts interpolate ${CLAUDE_PLUGIN_ROOT} in hooks.json commands, so this
# script runs on Claude Code and Copilot CLI alike.
set -u
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")/../bin" 2>/dev/null && pwd)" || exit 0
[ -f "$HERE/lib-omakase-bin.sh" ] || exit 0
. "$HERE/lib-omakase-bin.sh"
# Local tiers only — deliberately NOT `resolve_omakase fetch`.
resolve_omakase || exit 0
"$OMAKASE_BIN_RESOLVED" hook session-start || true
exit 0
