#!/usr/bin/env bash
# gofmt + go vet for the Go files this commit touches. Exits 0 instantly when no
# .go file is staged (so the gate is free on docs-only commits and harmless in a
# repo with no Go at all). gofmt checks only the staged files; go vet runs across
# the module because it needs whole-package context anyway.
# Exit non-zero to block the commit; exit 0 to allow.
set -euo pipefail

staged="$(git diff --cached --name-only --diff-filter=ACM -- '*.go')"
[ -n "$staged" ] || { echo "omakase: go-checks skipped (no staged .go files)."; exit 0; }

badfmt=""
while IFS= read -r f; do
  [ -f "$f" ] || continue
  if [ -n "$(gofmt -l "$f")" ]; then badfmt="$badfmt $f"; fi
done <<< "$staged"

if [ -n "$badfmt" ]; then
  echo "omakase: gofmt wants to rewrite:$badfmt" >&2
  echo "omakase: run 'gofmt -w' on the file(s) above and re-stage." >&2
  exit 1
fi

go vet ./...
echo "omakase: go-checks passed (gofmt clean, go vet clean)."
