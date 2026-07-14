#!/usr/bin/env bash
# gofmt + go vet for the Go files this commit touches. Exits 0 instantly when no
# .go file is staged (so the gate is free on docs-only commits and harmless in a
# repo with no Go at all). gofmt checks only the staged files; go vet runs on the
# staged files' packages - not the whole module, so unrelated work-in-progress
# elsewhere in the tree cannot block this commit.
# Exit non-zero to block the commit; exit 0 to allow.
set -euo pipefail

# -z: NUL-delimited raw filenames, so a non-ASCII or quoted name can never be
# octal-escaped into a string that silently fails the -f test below.
staged=()
while IFS= read -r -d '' f; do staged+=("$f"); done \
  < <(git diff --cached --name-only --diff-filter=ACM -z -- '*.go')
[ "${#staged[@]}" -gt 0 ] || { echo "omakase: go-checks skipped (no staged .go files)."; exit 0; }

command -v go >/dev/null 2>&1 \
  || { echo "omakase: go-checks BLOCKED - .go files are staged but no Go toolchain is on PATH." >&2; exit 1; }

badfmt=""
pkgs=()
for f in "${staged[@]}"; do
  [ -f "$f" ] || continue
  if [ -n "$(gofmt -l -- "$f")" ]; then badfmt="$badfmt $f"; fi
  d="./$(dirname "$f")"
  case " ${pkgs[*]-} " in *" $d "*) ;; *) pkgs+=("$d");; esac
done

if [ -n "$badfmt" ]; then
  echo "omakase: gofmt wants to rewrite:$badfmt" >&2
  echo "omakase: run 'gofmt -w' on the file(s) above and re-stage." >&2
  exit 1
fi

if [ "${#pkgs[@]}" -gt 0 ]; then
  go vet "${pkgs[@]}"
fi
echo "omakase: go-checks passed (gofmt clean, go vet clean)."
