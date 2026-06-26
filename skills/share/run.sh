#!/usr/bin/env bash
# Thin front door for /omakase:share — capture this repo's harness into a new, publishable
# harness repo (the inverse of init).
set -euo pipefail
SKILL_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"   # <plugin>/skills/share
BIN="$(cd "$SKILL_DIR/../../bin" && pwd)"                    # <plugin>/bin
exec bash "$BIN/share.sh" "$@"
