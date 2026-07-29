#!/usr/bin/env bash
# The apply half of /omakase:block. Deliberately NOT in the skill's
# allowed-tools: invoking it triggers the host's permission prompt, which is
# the human confirmation the two-step consent design requires.
set -euo pipefail
SKILL_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BIN="$(cd "$SKILL_DIR/../../bin" && pwd)"
exec bash "$BIN/block.sh" "$@" --yes
