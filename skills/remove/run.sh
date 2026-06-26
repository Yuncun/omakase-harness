#!/usr/bin/env bash
# Thin front door for /omakase:remove — reverse init on the current git repo.
set -euo pipefail
SKILL_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"   # <plugin>/skills/remove
BIN="$(cd "$SKILL_DIR/../../bin" && pwd)"                    # <plugin>/bin
exec bash "$BIN/remove.sh" "$@"
