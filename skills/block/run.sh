#!/usr/bin/env bash
# Thin front door for /omakase:block — per-item consent over the repo's own
# committed agent config. A leading "unblock" routes to the unblock verb;
# everything else forwards to block verbatim.
set -euo pipefail
SKILL_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"   # <plugin>/skills/block
BIN="$(cd "$SKILL_DIR/../../bin" && pwd)"                    # <plugin>/bin
if [ "${1:-}" = "unblock" ]; then
  shift
  exec bash "$BIN/unblock.sh" "$@"
fi
exec bash "$BIN/block.sh" "$@"
