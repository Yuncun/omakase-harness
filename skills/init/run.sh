#!/usr/bin/env bash
# Thin front door for /omakase:init — self-locate the base harness and run the injector on
# the current git repo. Host-agnostic: the path is resolved from THIS file's location, so it
# works in Claude Code, Copilot CLI, or a plain shell with no host env var.
set -euo pipefail
SKILL_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"   # <plugin>/skills/init
BIN="$(cd "$SKILL_DIR/../../bin" && pwd)"                    # <plugin>/bin
exec bash "$BIN/init.sh" "$@"
