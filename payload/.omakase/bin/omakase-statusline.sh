#!/usr/bin/env bash
# omakase-statusline — the harness canary for the status bar. Its presence is the
# whole signal: "<name> is running" means the harness is active and watching THIS
# repo; it goes dark in a repo the harness doesn't guard. No verdicts, no jargon,
# only the 🥡 icon — plus ONE warning segment: "⚠ MAIN CHECKOUT · use a worktree"
# whenever this is the main checkout and other worktrees are active (a one-shot
# SessionStart reminder scrolls away and gets ignored; a status bar cannot).
# Honors NO_COLOR, costs no API tokens. Name comes from .omakase/NAME (or
# $OMAKASE_NAME), default "omakase".
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
icon="${OMAKASE_ICON:-🥡}"

# Worktree-discipline warning (issue #86): the main checkout is for coordination while
# implementation runs in worktrees — branches cut here inherit concurrent sessions'
# uncommitted work. Warn persistently when (main checkout) AND (other worktrees exist).
# Same standdowns as the commit gate: the audited skip env, or a "worktree-discipline"
# line in the menu's disabled-gates file.
warn=0
if [ "${OMAKASE_SKIP_WORKTREE_DISCIPLINE:-0}" != "1" ]; then
  wt="$(git worktree list --porcelain 2>/dev/null)" || wt=""
  n="$(printf '%s\n' "$wt" | grep -c '^worktree ' 2>/dev/null || true)"
  case "${n:-0}" in ''|*[!0-9]*) n=0;; esac
  if [ "$n" -gt 1 ]; then
    main="$(printf '%s\n' "$wt" | awk '/^worktree /{sub(/^worktree /,""); print; exit}')"
    [ "$top" = "$main" ] && warn=1
  fi
  if [ "$warn" -eq 1 ]; then
    gcd="$(git rev-parse --git-common-dir 2>/dev/null)" \
      && cg="$(cd "$gcd" 2>/dev/null && pwd)" \
      && grep -Fxq -- "worktree-discipline" "$cg/omakase/disabled-gates" 2>/dev/null \
      && warn=0
  fi
fi

if [ -n "${NO_COLOR:-}" ]; then
  if [ "$warn" -eq 1 ]; then
    printf '%s %s is running ⚠ MAIN CHECKOUT · use a worktree\n' "$icon" "$name"
  else
    printf '%s %s is running\n' "$icon" "$name"
  fi
else
  esc=$'\033'
  if [ "$warn" -eq 1 ]; then
    printf '%s%s %s is running %s%s⚠ MAIN CHECKOUT · use a worktree %s\n' \
      "${esc}[48;2;15;61;34m${esc}[38;2;126;226;160m" "$icon" "$name" "${esc}[0m" \
      "${esc}[48;2;92;54;10m${esc}[38;2;255;204;128m" "${esc}[0m"
  else
    printf '%s%s %s is running %s\n' \
      "${esc}[48;2;15;61;34m${esc}[38;2;126;226;160m" "$icon" "$name" "${esc}[0m"
  fi
fi
