#!/usr/bin/env bash
# omakase-harness init — overlay payload/ into this repo additively, exclude every
# placed path via .git/info/exclude (zero committed footprint), install lefthook,
# and set up new worktrees to receive the (gitignored) harness automatically too.
# Idempotent: re-running re-overlays, rewrites the exclude block, and refreshes
# the worktree snapshot. Re-run NEVER eats a file you edited (see the overlay loop);
# pass --force to take the new payload version over your edits.
set -euo pipefail

FORCE=0
for a in "$@"; do case "$a" in --force|-f) FORCE=1;; esac; done

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PAYLOAD="${OMAKASE_PAYLOAD:-$(cd "$SCRIPT_DIR/../payload" && pwd)}"
ROOT="$(git rev-parse --show-toplevel 2>/dev/null)" || { echo "omakase: not inside a git repo" >&2; exit 1; }
[ -d "$PAYLOAD" ] || { echo "omakase: payload dir not found at $PAYLOAD" >&2; exit 1; }
# Resolve a lefthook invocation WITHOUT mutating the user's global environment.
# Order: an explicit override; lefthook already on PATH (a global brew/mise install);
# then the repo's own node_modules/.bin (a JS devDependency — the common case). We do
# NOT auto-install: a global install is irreversible (/omakase-remove can't undo it)
# and a hook script has no interactive user to ask. When lefthook is genuinely absent
# we exit with guidance; the /omakase-init command layer is where an interactive
# "install it for you?" belongs (that's where a user exists to answer). Sets $LEFTHOOK.
resolve_lefthook() {
  if [ -n "${LEFTHOOK_BIN:-}" ];                  then LEFTHOOK="$LEFTHOOK_BIN"; return 0; fi
  if command -v lefthook >/dev/null 2>&1;          then LEFTHOOK="lefthook"; return 0; fi
  if [ -x "$ROOT/node_modules/.bin/lefthook" ];    then LEFTHOOK="$ROOT/node_modules/.bin/lefthook"; return 0; fi
  return 1
}
resolve_lefthook || { echo "omakase: lefthook not found. Install it (e.g. 'brew install lefthook', 'mise use lefthook', or add it as a devDependency and run your package manager's install), or set LEFTHOOK_BIN=/path/to/lefthook, then re-run." >&2; exit 1; }

BEGIN="# >>> omakase-harness >>>"
END="# <<< omakase-harness <<<"
EXCLUDE="$ROOT/.git/info/exclude"
# The shared git dir — identical for the main checkout and every linked worktree,
# so artifacts placed here are reachable from any worktree (info/exclude and the
# common dir are shared). This is where the worktree harness snapshot lives.
COMMON="$(cd "$ROOT" && cd "$(git rev-parse --git-common-dir)" && pwd)"
OMK="$COMMON/omakase"
SNAP="$OMK/payload-snapshot"   # the PREVIOUS run's snapshot — the three-way-merge base.

# Identical?  Compares symlink targets for symlinks, byte content otherwise.
same_file() {
  if [ -L "$1" ] || [ -L "$2" ]; then
    [ "$(readlink "$1" 2>/dev/null)" = "$(readlink "$2" 2>/dev/null)" ]
  else
    cmp -s "$1" "$2"
  fi
}
place_file() {  # $1 = source payload path, $2 = relative dest
  mkdir -p "$ROOT/$(dirname "$2")"
  cp -P "$1" "$ROOT/$2"   # -P: carry symlinks as symlinks (e.g. CLAUDE.md -> AGENTS.md)
  case "$2" in *.sh) [ -L "$ROOT/$2" ] || chmod +x "$ROOT/$2";; esac
}

placed=(); skipped=(); updated=(); kept=()
while IFS= read -r -d '' f; do
  rel="${f#"$PAYLOAD"/}"
  dest="$ROOT/$rel"
  # Never touch a path the repo tracks (committed file wins).
  if git -C "$ROOT" ls-files --error-unmatch "$rel" >/dev/null 2>&1; then
    skipped+=("$rel"); echo "omakase: SKIP (already tracked) $rel" >&2; continue
  fi
  # Fresh placement: nothing there yet.
  if [ ! -e "$dest" ] && [ ! -L "$dest" ]; then
    place_file "$f" "$rel"; placed+=("$rel"); continue
  fi
  # Already current: an untracked copy identical to the payload — leave it.
  if same_file "$dest" "$f"; then placed+=("$rel"); continue; fi
  # Differs from the payload. Three-way: if it still matches the last snapshot the
  # user has NOT edited it since the previous init, so a payload change is a clean
  # update — take it. Otherwise it is the user's edit (or a pre-existing untracked
  # file) — keep it unless --force, so re-init never eats a hand-tuned gate.
  if [ -e "$SNAP/$rel" ] || [ -L "$SNAP/$rel" ]; then
    if same_file "$dest" "$SNAP/$rel"; then
      place_file "$f" "$rel"; placed+=("$rel"); updated+=("$rel")
      echo "omakase: updated $rel (you had not edited it; took the new payload version)" >&2
      continue
    fi
  fi
  if [ "$FORCE" -eq 1 ]; then
    place_file "$f" "$rel"; placed+=("$rel"); updated+=("$rel")
    echo "omakase: --force overwrote your edited $rel" >&2
  else
    placed+=("$rel"); kept+=("$rel")   # still managed (excluded + snapshotted), just not overwritten
    echo "omakase: KEPT your edited $rel (differs from payload; re-run with --force to take the new version)" >&2
  fi
done < <(find "$PAYLOAD" \( -type f -o -type l \) -print0)

# Top-level prefixes for the exclude block (small + stable), plus lefthook's
# auto-created lefthook.yml if the repo does not track one.
prefixes=()
add_prefix(){ case " ${prefixes[*]:-} " in *" $1 "*) ;; *) prefixes+=("$1");; esac; }
for rel in "${placed[@]:-}"; do [ -n "$rel" ] && add_prefix "${rel%%/*}"; done
git -C "$ROOT" ls-files --error-unmatch lefthook.yml >/dev/null 2>&1 || add_prefix "lefthook.yml"

# Worktree auto-install wiring (.worktreeinclude). Only when the repo does not TRACK
# .worktreeinclude — appending to a tracked file would be a committed footprint,
# which the additive rule forbids. When skipped, manual `git worktree add` won't
# get it automatically; new Claude-created worktrees still receive it via the snapshot + post-checkout.
WTINC_TRACKED=0
if git -C "$ROOT" ls-files --error-unmatch .worktreeinclude >/dev/null 2>&1; then
  WTINC_TRACKED=1
  echo "omakase: .worktreeinclude is tracked — leaving it untouched (re-run /omakase-init inside a new manual worktree to install it there)." >&2
else
  add_prefix ".worktreeinclude"
fi

mkdir -p "$(dirname "$EXCLUDE")"; touch "$EXCLUDE"
# strip any prior block (portable, no sed -i)
awk -v b="$BEGIN" -v e="$END" '$0==b{s=1} !s{print} $0==e{s=0}' "$EXCLUDE" > "$EXCLUDE.tmp" && mv "$EXCLUDE.tmp" "$EXCLUDE"
{
  echo "$BEGIN"
  for p in "${prefixes[@]:-}"; do
    [ -z "$p" ] && continue
    if [ -d "$ROOT/$p" ]; then echo "$p/"; else echo "$p"; fi
  done
  echo "$END"
} >> "$EXCLUDE"

# Write the .worktreeinclude block (Claude Code copies gitignored files matching
# these patterns into worktrees it creates). Marked block so re-runs stay idempotent
# and /omakase-remove can strip exactly what we added.
if [ "$WTINC_TRACKED" -eq 0 ] && [ "${#placed[@]:-0}" -gt 0 ]; then
  WTINC="$ROOT/.worktreeinclude"
  touch "$WTINC"
  awk -v b="$BEGIN" -v e="$END" '$0==b{s=1} !s{print} $0==e{s=0}' "$WTINC" > "$WTINC.tmp" && mv "$WTINC.tmp" "$WTINC"
  {
    echo "$BEGIN"
    for p in "${prefixes[@]:-}"; do
      [ -z "$p" ] && continue
      [ "$p" = ".worktreeinclude" ] && continue
      if [ -d "$ROOT/$p" ]; then echo "$p/"; else echo "$p"; fi
    done
    echo "$END"
  } >> "$WTINC"
fi

# Snapshot the placed files into the shared git dir, plus a manifest and a
# self-heal script. A fresh worktree has none of the (gitignored) harness files;
# the post-checkout job runs ensure-present.sh, which copies only the MISSING ones
# from this snapshot — never overwriting a local edit, never touching a tracked path.
rm -rf "$OMK/payload-snapshot"
mkdir -p "$OMK/payload-snapshot"
: > "$OMK/placed.list"
for rel in "${placed[@]:-}"; do
  [ -z "$rel" ] && continue
  mkdir -p "$OMK/payload-snapshot/$(dirname "$rel")"
  cp -P "$ROOT/$rel" "$OMK/payload-snapshot/$rel"
  printf '%s\n' "$rel" >> "$OMK/placed.list"
done

cat > "$OMK/ensure-present.sh" <<'ENSURE'
#!/usr/bin/env bash
# omakase-harness self-heal — copy any MISSING injected file into this worktree
# from the shared snapshot. Ensure-present / never-overwrite: safe on every
# checkout, self-heals deleted files, never clobbers a local edit, never writes a
# tracked path. Generated by init.sh; reversed by remove.sh.
set -euo pipefail
ROOT="$(git rev-parse --show-toplevel 2>/dev/null)" || exit 0
COMMON="$(cd "$(git rev-parse --git-common-dir)" && pwd)"
SNAP="$COMMON/omakase/payload-snapshot"
LIST="$COMMON/omakase/placed.list"
[ -f "$LIST" ] || exit 0
while IFS= read -r rel; do
  [ -z "$rel" ] && continue
  [ -e "$ROOT/$rel" ] || [ -L "$ROOT/$rel" ] && continue                      # never overwrite (also catches dangling symlinks)
  git -C "$ROOT" ls-files --error-unmatch "$rel" >/dev/null 2>&1 && continue  # never touch tracked
  [ -e "$SNAP/$rel" ] || [ -L "$SNAP/$rel" ] || continue
  mkdir -p "$ROOT/$(dirname "$rel")"
  cp -P "$SNAP/$rel" "$ROOT/$rel"
  case "$rel" in *.sh) [ -L "$ROOT/$rel" ] || chmod +x "$ROOT/$rel";; esac
done < "$LIST"
ENSURE
chmod +x "$OMK/ensure-present.sh"

( cd "$ROOT" && $LEFTHOOK install )

echo "omakase: placed ${#placed[@]} file(s), updated ${#updated[@]:-0}, kept ${#kept[@]:-0} edited, skipped ${#skipped[@]} tracked path(s)."
for p in "${placed[@]:-}"; do [ -n "$p" ] && echo "  + $p"; done
for u in "${updated[@]:-}"; do [ -n "$u" ] && echo "  ^ updated to new payload: $u"; done
for k in "${kept[@]:-}"; do [ -n "$k" ] && echo "  = kept your edit (use --force to update): $k"; done
for s in "${skipped[@]:-}"; do [ -n "$s" ] && echo "  ~ skipped (tracked): $s"; done
echo "omakase: ignores -> .git/info/exclude; hooks installed; new worktrees auto-install the harness. Nothing to commit."
echo "omakase: see the whole harness any time with  /omakase show"
echo "omakase: status line — compose the scorecard into your existing bar (it never"
echo "         takes over the bar). Add this command to your status-line script:"
echo "           bash $ROOT/.omakase/bin/omakase-statusline.sh"
echo "         Claude Code: your ~/.claude statusLine script. Copilot CLI: ~/.copilot. tmux: status-right."
