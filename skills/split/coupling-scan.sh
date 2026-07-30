#!/usr/bin/env bash
# Three-tier coupling scan. Prints: total-lines  repo-hits  stack-hits  path
#
#   repo  = names this repo's proper nouns   -> must be sanitized before it moves at all
#   stack = names the toolchain              -> moves to same-stack repos only
#   both zero = generic prose                -> moves anywhere
#
# Usage:  coupling-scan.sh -r '<repo regex>' -s '<stack regex>' <file>...
#         coupling-scan.sh -r 'acme|@acme|widgetkit' $(git ls-files '.claude/skills/**')
#
# A high score means "this file will mislead somewhere else", not "this file is bad".
# A ZERO score does NOT mean portable: semantic coupling ("PRs are <=10 files",
# "trunk-based", "run the Storybook") scores 0 and only a test vessel finds it.
set -euo pipefail

REPO=''
STACK='pnpm|npm |yarn|turbo|vitest|jest|playwright|storybook|eslint|oxlint|React|\.tsx|TypeScript|javascript|CSS|DOM|browser|node_modules|package\.json|gradle|kotlin|xcodebuild|cargo|pytest'

while getopts 'r:s:' o; do case $o in r) REPO=$OPTARG;; s) STACK=$OPTARG;; esac; done
shift $((OPTIND - 1))

[ -n "$REPO" ] || { echo "coupling-scan: -r <repo regex> is required" >&2; exit 2; }

printf 'lines\trepo\tstack\tpath\n'
for f in "$@"; do
  [ -f "$f" ] || continue
  tot=$(wc -l < "$f" | tr -d ' ')
  [ "$tot" -eq 0 ] && continue
  printf '%s\t%s\t%s\t%s\n' \
    "$tot" \
    "$(grep -cEi "$REPO" "$f" || true)" \
    "$(grep -cEi "$STACK" "$f" || true)" \
    "$f"
done
