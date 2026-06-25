#!/usr/bin/env bash
# Sample gate (a "scoped checker": fast, runs per-commit on staged files).
# Blocks a commit that stages a file containing a DO-NOT-COMMIT marker — a common guard
# against committing scratch code or a secret-in-progress. Fully generic: depends on
# nothing but git, passes on a clean repo, and shows a real gate firing. Replace it, or
# add your own gates in .omakase/gates/ and wire them in lefthook-local.yml.
# Exit non-zero to block the commit; exit 0 to allow.
set -euo pipefail

# Built from two pieces so THIS file never contains the contiguous marker, and so the gate
# never trips on its own source if a harness dogfoods this gate on itself.
marker="DO NOT ""COMMIT"

fail=0
while IFS= read -r f; do
  [ -f "$f" ] || continue
  if grep -nF "$marker" "$f" >/dev/null 2>&1; then
    echo "omakase: '$marker' marker found in $f" >&2
    grep -nF "$marker" "$f" | sed 's/^/    /' >&2
    fail=1
  fi
done < <(git diff --cached --name-only --diff-filter=ACM)

if [ "$fail" -ne 0 ]; then
  echo "omakase: sample gate BLOCKED the commit — remove the marker above (or edit .omakase/gates/block-marker.sh)." >&2
  exit 1
fi
echo "omakase: sample gate passed (no '$marker' marker in staged files)."
