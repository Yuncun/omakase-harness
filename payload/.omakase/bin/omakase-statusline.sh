#!/usr/bin/env bash
# omakase-statusline — the harness canary for the status bar. Its presence is the
# whole signal: "<name> is running" means the harness is active and watching THIS
# repo; it goes dark in a repo the harness doesn't guard. No verdicts, no jargon,
# only the 🍣 icon. Honors NO_COLOR, costs no API tokens. Name comes from
# .omakase/NAME (or $OMAKASE_NAME), default "omakase".
set -uo pipefail

top="$(git rev-parse --show-toplevel 2>/dev/null)" || exit 0
[ -n "$top" ] || exit 0

# Light up only where the harness is actually installed. Works from a linked
# worktree too: the hooks/ledger live in the shared (common) git dir.
active=0
[ -d "$top/.omakase" ] && active=1
if [ "$active" -eq 0 ]; then
  gcd="$(git rev-parse --git-common-dir 2>/dev/null)" \
    && cg="$(cd "$gcd" 2>/dev/null && pwd)" \
    && [ -d "$cg/omakase" ] && active=1
fi
[ "$active" -eq 1 ] || exit 0

name="omakase"
[ -f "$top/.omakase/NAME" ] && name="$(tr -d ' \n' < "$top/.omakase/NAME" 2>/dev/null)"
name="${OMAKASE_NAME:-$name}"
[ -n "$name" ] || name="omakase"
icon="${OMAKASE_ICON:-🍣}"

if [ -n "${NO_COLOR:-}" ]; then
  printf '%s %s is running\n' "$icon" "$name"
else
  esc=$'\033'
  printf '%s%s %s is running %s\n' \
    "${esc}[48;2;15;61;34m${esc}[38;2;126;226;160m" "$icon" "$name" "${esc}[0m"
fi
