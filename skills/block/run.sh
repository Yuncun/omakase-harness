#!/usr/bin/env bash
# Thin front door for /omakase:block — per-item consent over the repo's own
# committed agent config. This pre-approved entry point only EXPLAINS a block
# (and routes unblock, which is restorative); it refuses --yes so an agent
# cannot apply a block in one pre-approved call. Applying goes through
# confirm.sh, which is NOT in the skill's allowed-tools — the host's own
# permission prompt is the human confirmation.
set -euo pipefail
SKILL_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"   # <plugin>/skills/block
BIN="$(cd "$SKILL_DIR/../../bin" && pwd)"                    # <plugin>/bin
for a in "$@"; do
  if [ "$a" = "--yes" ]; then
    echo "omakase: this entry point only explains a block; to apply it run confirm.sh (the host will ask for permission)" >&2
    exit 2
  fi
done
if [ "${1:-}" = "unblock" ]; then
  shift
  exec bash "$BIN/unblock.sh" "$@"
fi
exec bash "$BIN/block.sh" "$@"
