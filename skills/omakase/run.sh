#!/usr/bin/env bash
# Self-locating dispatcher for the /omakase management skill (Copilot CLI).
#
# Copilot exposes this script from the installed engine-plugin copy of the skill, so we
# never hardcode a path: resolve the plugin root (the installed engine) from THIS file's
# location and call the injector in bin/. The injector operates on the CURRENT git repo
# (the one the user is working in) via `git rev-parse --show-toplevel` inside bin/.
set -euo pipefail

SKILL_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"   # <engine-plugin>/skills/omakase
PLUGIN_ROOT="$(cd "$SKILL_DIR/../.." && pwd)"               # <engine-plugin>
BIN="$PLUGIN_ROOT/bin"

cmd="${1:-show}"; [ "$#" -gt 0 ] && shift || true
case "$cmd" in
  init)            bash "$BIN/init.sh" "$@" ;;
  remove)          bash "$BIN/remove.sh" "$@" ;;
  show|status|"")  bash "$BIN/show.sh" --markdown ;;
  *) echo "usage: run.sh [show | init [--source <git-url-or-path>] | remove]" >&2; exit 2 ;;
esac
