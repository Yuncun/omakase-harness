#!/usr/bin/env bash
# Example gate (a "scoped checker": fast, runs per-commit on staged files).
# It blocks a commit that stages an unresolved merge-conflict marker. It is fully
# generic — depends on nothing but git, passes on a clean repo, and shows you a real
# gate actually firing. Replace it, or add your own gates in .omakase/gates/ and wire
# them in lefthook-local.yml. Exit non-zero to block; exit 0 to allow.
set -euo pipefail

fail=0
while IFS= read -r f; do
  [ -f "$f" ] || continue
  if grep -nE '^(<<<<<<<|=======|>>>>>>>)([[:space:]]|$)' "$f" >/dev/null 2>&1; then
    echo "omakase: unresolved merge-conflict marker in $f" >&2
    grep -nE '^(<<<<<<<|=======|>>>>>>>)([[:space:]]|$)' "$f" | sed 's/^/    /' >&2
    fail=1
  fi
done < <(git diff --cached --name-only --diff-filter=ACM)

if [ "$fail" -ne 0 ]; then
  echo "omakase: example gate BLOCKED the commit — resolve the markers above (or edit .omakase/gates/example.sh)." >&2
  exit 1
fi
echo "omakase: example gate passed (no merge-conflict markers in staged files)."
