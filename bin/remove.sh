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
COMMON="$(cd "$ROOT" && cd "$(git rev-parse --git-common-dir)" && pwd)"
EXCLUDE="$COMMON/info/exclude"   # shared git dir — also correct inside a linked worktree, where $ROOT/.git is a file

# Resolve lefthook by the same order init.sh uses (lib-lefthook.sh), but NEVER
# fetch — remove must stay offline. This still finds a binary that lives only in
# the omakase cache, so `lefthook uninstall` works when init self-provisioned it.
# If nothing resolves, skip uninstall: the $COMMON/omakase teardown below already
# neutralizes the fail-closed guard, and the hook stubs are reversed regardless.
. "$SCRIPT_DIR/lib-lefthook.sh"
if resolve_lefthook; then ( cd "$ROOT" && "$LEFTHOOK" uninstall ) || true; fi

# Strip our injected blocks from any hook stub that survived uninstall (they are
# already inert once $COMMON/omakase is gone, but leave no residue): the fail-closed
# verify (pre-commit/pre-push) and the worktree-bootstrap self-heal (post-checkout).
for hf in "$COMMON/hooks"/*; do
  [ -f "$hf" ] || continue
  changed=0
  for pair in "omakase-harness fail-closed" "omakase-harness worktree-bootstrap"; do
    gb="# >>> $pair >>>"; ge="# <<< $pair <<<"
    grep -qF "$gb" "$hf" 2>/dev/null || continue
    awk -v b="$gb" -v e="$ge" '$0==b{s=1} !s{print} $0==e{s=0}' "$hf" > "$hf.tmp" && mv "$hf.tmp" "$hf"
    changed=1
  done
  [ "$changed" = 1 ] && chmod +x "$hf"
done

# Delete the placed paths — never a tracked file. The provenance ledger
# (placed.tsv: path,kind,source,sha256,enabled) records exactly what init placed,
# so it is the authority when present: ALL its rows are deleted, enabled or not
# (remove is a total teardown; the enabled flag is an off switch, not an uninstall).
# Without a ledger (pre-0.10 install, or no install at all) fall back to
# enumerating the payload — the old behavior.
delete_placed() {  # $1 = repo-relative path
  git -C "$ROOT" ls-files --error-unmatch "$1" >/dev/null 2>&1 && return 0
  rm -f "$ROOT/$1"
  local d; d="$(dirname "$1")"
  while [ "$d" != "." ] && [ -d "$ROOT/$d" ] && [ -z "$(ls -A "$ROOT/$d")" ]; do
    rmdir "$ROOT/$d"; d="$(dirname "$d")"
  done
}
LEDGER="$COMMON/omakase/placed.tsv"
if [ -f "$LEDGER" ]; then
  while IFS=$'\t' read -r rel kind src hash enabled || [ -n "$rel" ]; do
    [ -z "$rel" ] && continue
    delete_placed "$rel"
  done < "$LEDGER"
elif { [ -f "$EXCLUDE" ] && grep -qF "$BEGIN" "$EXCLUDE"; } || [ -d "$COMMON/omakase" ]; then
  # No ledger (a pre-0.10 install) but omakase WAS installed here — fall back to enumerating
  # the payload. The install-proof sentinel (init always writes the exclude block; a leftover
  # snapshot dir also counts) is REQUIRED: without it this fallback deletes the user's own
  # untracked files in a repo that merely shares a payload filename (e.g. .claude/settings.json).
  while IFS= read -r -d '' f; do
    delete_placed "${f#"$PAYLOAD"/}"
  done < <(find "$PAYLOAD" \( -type f -o -type l \) -print0)  # -type l: also enumerate symlinks init.sh placed
else
  echo "omakase: nothing installed here; nothing to remove." >&2
  exit 0
fi

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
