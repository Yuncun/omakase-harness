#!/usr/bin/env bash
# Refuse a commit that stages a file carrying the scratch marker - the words
# "DO NOT" + "COMMIT", in capitals, together. Mark scratch code or a
# secret-in-progress with it and this gate keeps it out of history until the
# marker is removed. Depends on nothing but git; passes on a clean repo.
# Exit non-zero to block the commit; exit 0 to allow.
set -euo pipefail

# Built from two pieces so THIS file never contains the contiguous marker, and so
# the gate never trips on its own source when a harness dogfoods it on itself.
marker="DO NOT ""COMMIT"

# -z: NUL-delimited raw filenames, so a non-ASCII or quoted name can never be
# octal-escaped into a string that silently fails the -f test (fail-open).
fail=0
while IFS= read -r -d '' f; do
  [ -f "$f" ] || continue
  if grep -nF "$marker" -- "$f" >/dev/null 2>&1; then
    echo "omakase: '$marker' marker found in $f" >&2
    grep -nF "$marker" -- "$f" | sed 's/^/    /' >&2
    fail=1
  fi
done < <(git diff --cached --name-only --diff-filter=ACM -z)

if [ "$fail" -ne 0 ]; then
  echo "omakase: block-marker BLOCKED the commit - remove the marker above first." >&2
  exit 1
fi
echo "omakase: block-marker passed (no '$marker' marker in staged files)."
