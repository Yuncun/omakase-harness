#!/usr/bin/env bash
# omakase-harness remove — reverse of init: uninstall hooks, delete exactly the
# untracked paths init placed, strip the exclude block, and tear down the worktree
# harness snapshot. "Personal" demands easy-off.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PAYLOAD="${OMAKASE_PAYLOAD:-$(cd "$SCRIPT_DIR/../payload" && pwd)}"
ROOT="$(git rev-parse --show-toplevel 2>/dev/null)" || { echo "omakase: not inside a git repo" >&2; exit 1; }
BEGIN="# >>> omakase-harness >>>"
END="# <<< omakase-harness <<<"
EXCLUDE="$ROOT/.git/info/exclude"
COMMON="$(cd "$ROOT" && cd "$(git rev-parse --git-common-dir)" && pwd)"

if command -v lefthook >/dev/null 2>&1; then ( cd "$ROOT" && lefthook uninstall ) || true; fi

# Delete only paths init would have placed AND that are NOT tracked (never touch tracked files).
while IFS= read -r -d '' f; do
  rel="${f#"$PAYLOAD"/}"
  git -C "$ROOT" ls-files --error-unmatch "$rel" >/dev/null 2>&1 && continue
  rm -f "$ROOT/$rel"
  d="$(dirname "$rel")"
  while [ "$d" != "." ] && [ -d "$ROOT/$d" ] && [ -z "$(ls -A "$ROOT/$d")" ]; do
    rmdir "$ROOT/$d"; d="$(dirname "$d")"
  done
done < <(find "$PAYLOAD" \( -type f -o -type l \) -print0)  # -type l: also enumerate symlinks init.sh placed

# Remove the auto-created skeleton lefthook.yml if it is untracked and is lefthook's default banner.
if [ -f "$ROOT/lefthook.yml" ] && ! git -C "$ROOT" ls-files --error-unmatch lefthook.yml >/dev/null 2>&1; then
  grep -q "EXAMPLE USAGE" "$ROOT/lefthook.yml" 2>/dev/null && rm -f "$ROOT/lefthook.yml"
fi

# Strip our .worktreeinclude block; delete the file if it is now empty and untracked.
WTINC="$ROOT/.worktreeinclude"
if [ -f "$WTINC" ] && ! git -C "$ROOT" ls-files --error-unmatch .worktreeinclude >/dev/null 2>&1; then
  awk -v b="$BEGIN" -v e="$END" '$0==b{s=1} !s{print} $0==e{s=0}' "$WTINC" > "$WTINC.tmp" && mv "$WTINC.tmp" "$WTINC"
  [ -s "$WTINC" ] || rm -f "$WTINC"
fi

# Tear down the worktree harness snapshot in the shared git dir.
rm -rf "$COMMON/omakase"

# Strip our exclude block.
if [ -f "$EXCLUDE" ]; then
  awk -v b="$BEGIN" -v e="$END" '$0==b{s=1} !s{print} $0==e{s=0}' "$EXCLUDE" > "$EXCLUDE.tmp" && mv "$EXCLUDE.tmp" "$EXCLUDE"
fi
echo "omakase: removed. Hooks uninstalled, placed files deleted, worktree snapshot + exclude block stripped."
