#!/usr/bin/env bash
# Thin front door for /omakase:status — render the installed harness as Markdown. Read-only.
set -euo pipefail
SKILL_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"   # <plugin>/skills/status
BIN="$(cd "$SKILL_DIR/../../bin" && pwd)"                    # <plugin>/bin
exec bash "$BIN/show.sh" --markdown
