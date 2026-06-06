#!/usr/bin/env bash
# omakase-harness show — render the installed (gitignored, invisible) harness as ONE
# readable map: every placed file, which git hooks run what, and what is hidden via
# .git/info/exclude. Read-only. This is the cure for "the install is invisible" — it
# lets you SEE the whole harness at a glance without committing anything.
set -euo pipefail

ROOT="$(git rev-parse --show-toplevel 2>/dev/null)" || { echo "omakase: not inside a git repo" >&2; exit 1; }
COMMON="$(cd "$ROOT" && cd "$(git rev-parse --git-common-dir)" && pwd)"
OMK="$COMMON/omakase"
EXCLUDE="$ROOT/.git/info/exclude"
BEGIN="# >>> omakase-harness >>>"
END="# <<< omakase-harness <<<"

if [ ! -f "$OMK/placed.list" ]; then
  echo "No omakase harness is installed in this repo."
  echo "Run  /omakase init  to inject one."
  exit 0
fi

echo "omakase harness — installed in $ROOT"
echo "(every file below is gitignored via .git/info/exclude: invisible to git, never committed)"
echo
echo "PLACED FILES"
while IFS= read -r rel; do
  [ -z "$rel" ] && continue
  if [ -e "$ROOT/$rel" ] || [ -L "$ROOT/$rel" ]; then
    if [ -L "$ROOT/$rel" ]; then echo "  + $rel -> $(readlink "$ROOT/$rel")"; else echo "  + $rel"; fi
  else
    echo "  ! $rel   (MISSING — run /omakase init to restore)"
  fi
done < "$OMK/placed.list"
echo

echo "GIT HOOKS — what runs, and when"
LH=""
if [ -n "${LEFTHOOK_BIN:-}" ]; then LH="$LEFTHOOK_BIN"
elif command -v lefthook >/dev/null 2>&1; then LH="lefthook"
elif [ -x "$ROOT/node_modules/.bin/lefthook" ]; then LH="$ROOT/node_modules/.bin/lefthook"; fi
DUMP=""
[ -n "$LH" ] && DUMP="$( cd "$ROOT" && "$LH" dump 2>/dev/null || true )"
if [ -n "$DUMP" ]; then
  printf '%s\n' "$DUMP" | sed 's/^/  /'
elif [ -f "$ROOT/lefthook-local.yml" ]; then
  echo "  (lefthook not resolved — showing the raw wiring file)"
  sed 's/^/  /' "$ROOT/lefthook-local.yml"
else
  echo "  (no hook wiring found)"
fi
echo

echo "HIDDEN VIA .git/info/exclude"
if [ -f "$EXCLUDE" ]; then
  awk -v b="$BEGIN" -v e="$END" '$0==b{s=1;next} $0==e{s=0} s&&NF{print "  "$0}' "$EXCLUDE"
fi
echo
echo "Update (take new payload over your edits):  /omakase init --force"
echo "Undo everything:                            /omakase remove"
