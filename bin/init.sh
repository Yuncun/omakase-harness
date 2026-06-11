#!/usr/bin/env bash
# omakase-harness init — overlay payload/ into this repo additively, exclude every
# placed path via .git/info/exclude (zero committed footprint), install lefthook,
# and set up new worktrees to receive the (gitignored) harness automatically too.
# Idempotent: re-running re-overlays, rewrites the exclude block, and refreshes the
# worktree snapshot. Rule: the injected harness always matches payload — a re-run
# overwrites an injected file that differs (and warns that any local edit was replaced),
# but never touches a COMMITTED file (those are reported; the GUARDED `--cut-over` flag
# untracks them to let the harness copy take over — see usage).
set -euo pipefail

usage() {
  cat <<'USAGE'
usage: init.sh [--cut-over] [--help]

Overlay payload/ into the current repo additively (zero committed footprint) and
install lefthook hooks. A payload path the repo already COMMITS is never touched:
it is skipped and reported.

  --cut-over   also untrack (git rm --cached) every payload path the repo currently
               commits, so the injected copies take over. This STAGES DELETIONS of
               shared files; the next commit applies them for everyone. It prints
               exactly what it will untrack and the consequences, then REFUSES
               unless OMAKASE_CUTOVER_CONFIRM=1 is set. You review and commit the
               staged deletions yourself.
  -h, --help   show this help.
USAGE
}

CUTOVER=0
while [ $# -gt 0 ]; do
  case "$1" in
    --cut-over) CUTOVER=1;;
    -h|--help)  usage; exit 0;;
    *) echo "omakase: unknown argument '$1'" >&2; usage >&2; exit 2;;
  esac
  shift
done

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
# The shared git dir — identical for the main checkout and every linked worktree,
# so artifacts placed here are reachable from any worktree (info/exclude and the
# common dir are shared). This is where the worktree harness snapshot lives.
COMMON="$(cd "$ROOT" && cd "$(git rev-parse --git-common-dir)" && pwd)"
# Exclude file via the shared git dir, NOT "$ROOT/.git/info/exclude": in a linked
# worktree $ROOT/.git is a FILE, so the literal path crashes mkdir; this resolves
# to the same place in a main checkout and the right place in a worktree.
EXCLUDE="$COMMON/info/exclude"
OMK="$COMMON/omakase"
HOOKS_DIR="$COMMON/hooks"   # hooks live in the shared git dir (we refuse a foreign core.hooksPath below)

# ---- incumbent hook-manager guard (runs BEFORE any mutation) ----
# `lefthook install` DISPLACES an existing hook stub (renames it to .old), silently
# disabling the project's own gates; a hook-manager "prepare" script (husky,
# simple-git-hooks) then reinstalls its own hooks on the next npm install, so the
# live gate set flip-flops. Detect an incumbent manager and refuse with guidance —
# omakase does not chain hook managers (v1). Exempt: lefthook-managed stubs (incl.
# our own re-init): lefthook.yml + lefthook-local.yml merging is the supported
# coexistence path. Exemption principle: omakase's own injected artifacts are always
# UNTRACKED — so an untracked .husky/ matching a payload that ships one is ours;
# git-TRACKED .husky content is always the project's own and always refuses.
incumbent=()
RESET_HOOKSPATH=0
hookspath="$(git -C "$ROOT" config --get core.hooksPath 2>/dev/null || true)"
if [ -n "$hookspath" ]; then
  # core.hooksPath pointing at the repo's OWN standard hooks dir is harmless (the
  # live pixterm-engine install does exactly this); only a path that resolves
  # elsewhere means a foreign manager owns the hooks. Resolve relative values
  # against $ROOT and compare physically (symlinks resolved).
  case "$hookspath" in /*) hp_abs="$hookspath";; *) hp_abs="$ROOT/$hookspath";; esac
  hp_abs="$(cd "$hp_abs" 2>/dev/null && pwd -P || echo "$hp_abs")"
  std_abs="$(cd "$HOOKS_DIR" 2>/dev/null && pwd -P || echo "$HOOKS_DIR")"
  if [ "$hp_abs" != "$std_abs" ]; then
    incumbent+=("core.hooksPath = '$hookspath' (a foreign hook manager owns the hooks dir; husky v9 sets .husky/_)")
  else
    # Redundant config: it names the default location explicitly, but lefthook
    # refuses to install while ANY core.hooksPath is set. Clear it just before
    # 'lefthook install' (the effective hooks dir is unchanged) — flagged here,
    # acted on later, so a refusal elsewhere in this guard mutates nothing.
    RESET_HOOKSPATH=1
  fi
fi
if [ -n "$(git -C "$ROOT" ls-files -- .husky 2>/dev/null)" ]; then
  incumbent+=(".husky/ content is git-tracked (the project's own husky setup)")
elif [ -d "$ROOT/.husky" ] && [ ! -d "$PAYLOAD/.husky" ]; then
  incumbent+=(".husky/ directory (husky)")
fi
if [ -f "$ROOT/package.json" ] && grep -Eq '"prepare"[[:space:]]*:[[:space:]]*"[^"]*(husky|simple-git-hooks)' "$ROOT/package.json"; then
  incumbent+=("package.json \"prepare\" script wires a hook manager (husky / simple-git-hooks) — npm install would overwrite lefthook's hooks")
fi
for hf in "$HOOKS_DIR"/*; do
  [ -f "$hf" ] || continue
  case "$hf" in *.sample|*.old) continue;; esac
  if grep -qi 'lefthook' "$hf" 2>/dev/null; then continue; fi
  if [ -f "$ROOT/.pre-commit-config.yaml" ] && grep -q 'pre-commit\.com\|generated by pre-commit' "$hf" 2>/dev/null; then
    incumbent+=("$(basename "$hf"): installed pre-commit-framework stub (plus .pre-commit-config.yaml)")
  else
    incumbent+=("$(basename "$hf"): existing non-lefthook hook in $HOOKS_DIR")
  fi
done
if [ "${#incumbent[@]:-0}" -gt 0 ]; then
  echo "omakase: REFUSING to install — an incumbent hook manager is present:" >&2
  for i in "${incumbent[@]}"; do echo "  - $i" >&2; done
  echo "  'lefthook install' would displace the project's own hooks (renaming them to .old)," >&2
  echo "  silently disabling its gates — and a husky prepare script would overwrite lefthook" >&2
  echo "  back on the next npm install. omakase does not chain hook managers (v1)." >&2
  echo "  If these are stale leftovers, remove them and re-run. If the project really uses" >&2
  echo "  them, do not install omakase here. Nothing was changed." >&2
  exit 1
fi

# ---- guarded cut-over (--cut-over) ----
# The old advice was a raw `git rm --cached` for the user to run by hand; an agent
# reading that output runs it and auto-commits, deleting shared files from the repo
# for everyone. The guarded form states the consequences and refuses without an
# explicit confirmation env.
if [ "$CUTOVER" -eq 1 ]; then
  cutover=()
  while IFS= read -r -d '' f; do
    rel="${f#"$PAYLOAD"/}"
    git -C "$ROOT" ls-files --error-unmatch "$rel" >/dev/null 2>&1 && cutover+=("$rel")
  done < <(find "$PAYLOAD" \( -type f -o -type l \) -print0)
  if [ "${#cutover[@]:-0}" -eq 0 ]; then
    echo "omakase: --cut-over: no payload path is tracked by this repo — nothing to cut over."
  else
    echo "omakase: cut-over will run  git rm --cached  on ${#cutover[@]} tracked file(s):"
    for c in "${cutover[@]}"; do echo "    $c"; done
    echo "  This STAGES A DELETION of each shared file. The next commit — including an agent"
    echo "  auto-commit — applies that deletion FOR EVERYONE who pulls it, and upstream changes"
    echo "  to these files will then produce modify/delete conflicts. The files stay on disk;"
    echo "  the injected (gitignored) copies take over locally. Undo before committing with"
    echo "  'git restore --staged <file>'; 'git add <file>' re-tracks later."
    if [ "${OMAKASE_CUTOVER_CONFIRM:-}" != "1" ]; then
      echo "omakase: REFUSING cut-over without confirmation. Re-run with OMAKASE_CUTOVER_CONFIRM=1 to proceed. Nothing was changed." >&2
      exit 1
    fi
    ( cd "$ROOT" && git rm --cached -q -- "${cutover[@]}" )
    echo "omakase: cut-over staged ${#cutover[@]} deletion(s) — review with 'git status' and commit them yourself."
  fi
fi

# ---- upstream-collision guard ----
# git's default --overwrite-ignore behavior SILENTLY overwrites ignored files on
# checkout/pull. If upstream commits a tracked file at a path the overlay occupies,
# the personal copy is destroyed without warning and init thereafter skips the path
# as tracked. Detect the transition: a previously PLACED path (prior run's ledger)
# that the index now tracks. The last-injected copy is preserved under
# $OMK/clobbered/ because the snapshot rebuild below would delete it.
if [ -f "$OMK/placed.list" ]; then
  while IFS= read -r rel; do
    [ -z "$rel" ] && continue
    if git -C "$ROOT" ls-files --error-unmatch "$rel" >/dev/null 2>&1; then
      if [ -e "$OMK/payload-snapshot/$rel" ] || [ -L "$OMK/payload-snapshot/$rel" ]; then
        mkdir -p "$OMK/clobbered/$(dirname "$rel")"
        cp -P "$OMK/payload-snapshot/$rel" "$OMK/clobbered/$rel"
      fi
      echo "omakase: WARNING — '$rel' was injected (personal, gitignored) but is NOW TRACKED by the repo." >&2
      echo "  An upstream commit likely landed a file at this path; git silently overwrites ignored" >&2
      echo "  files on checkout/pull, so your personal copy was likely clobbered. Last-injected copy" >&2
      echo "  preserved at:" >&2
      echo "    $OMK/clobbered/$rel" >&2
      echo "  Diff it against the tracked file and reconcile: drop '$rel' from your payload, or run" >&2
      echo "  init --cut-over (guarded) to untrack the file and let the injected copy take over." >&2
    fi
  done < "$OMK/placed.list"
fi

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

placed=(); skipped=(); overwrote=()
while IFS= read -r -d '' f; do
  rel="${f#"$PAYLOAD"/}"
  dest="$ROOT/$rel"
  # Never touch a path the repo tracks (committed file wins). Report it so the user can
  # cut over deliberately (init --cut-over, guarded) to let the injected copy take over.
  if git -C "$ROOT" ls-files --error-unmatch "$rel" >/dev/null 2>&1; then
    skipped+=("$rel"); echo "omakase: SKIP (already tracked) $rel" >&2; continue
  fi
  # Fresh placement: nothing there yet.
  if [ ! -e "$dest" ] && [ ! -L "$dest" ]; then
    place_file "$f" "$rel"; placed+=("$rel"); continue
  fi
  # Already current: an untracked copy identical to the payload — leave it.
  if same_file "$dest" "$f"; then placed+=("$rel"); continue; fi
  # Differs from payload and is NOT committed: the injected harness always matches payload,
  # so overwrite — and warn, since this replaces whatever was there (an upstream update, or
  # a local in-place edit; init cannot tell which and does not try).
  place_file "$f" "$rel"; placed+=("$rel"); overwrote+=("$rel")
  echo "omakase: overwrote $rel to match payload (any local edit was replaced)" >&2
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
  # Never touch tracked — and warn: a placed path turning TRACKED means an upstream
  # commit landed a file here, and git silently overwrites ignored files on checkout,
  # so the personal copy was likely clobbered (the upstream-collision guard). This
  # check must run BEFORE the existence check: a tracked file exists in the working
  # tree, so existence-first would skip it silently.
  if git -C "$ROOT" ls-files --error-unmatch "$rel" >/dev/null 2>&1; then
    echo "omakase: WARNING — injected path '$rel' is now TRACKED by the repo; your personal copy was likely clobbered by an upstream commit (git overwrites ignored files on checkout). Last-injected copy: $SNAP/$rel — diff it against the tracked file, then drop the path from your payload or cut over (init --cut-over)." >&2
    continue
  fi
  [ -e "$ROOT/$rel" ] || [ -L "$ROOT/$rel" ] && continue                      # never overwrite (also catches dangling symlinks)
  [ -e "$SNAP/$rel" ] || [ -L "$SNAP/$rel" ] || continue
  mkdir -p "$ROOT/$(dirname "$rel")"
  cp -P "$SNAP/$rel" "$ROOT/$rel"
  case "$rel" in *.sh) [ -L "$ROOT/$rel" ] || chmod +x "$ROOT/$rel";; esac
done < "$LIST"
# Re-arm the fail-closed guard blocks: lefthook's npm postinstall runs
# `lefthook install -f` on every npm/yarn install, regenerating the hook stubs and
# stripping the guard; this post-checkout hook still runs afterwards, so the guard
# self-heals on the next checkout/pull.
if [ -f "$COMMON/omakase/install-guards.sh" ]; then sh "$COMMON/omakase/install-guards.sh"; fi
ENSURE
chmod +x "$OMK/ensure-present.sh"

# Fail-closed verifier — run by the enforcement hook stubs BEFORE lefthook. If the
# (gitignored) overlay was wiped (e.g. `git clean -fdx`), the stubs survive in .git
# but lefthook finds no config and passes silently — gates would fail OPEN. This
# checks every ledgered path still exists and hard-fails with a restore instruction.
# Fast (one existence test per placed file, every commit) and dependency-free
# (POSIX sh + git only). Generated by init.sh; removed with the snapshot by remove.sh.
cat > "$OMK/verify-overlay.sh" <<'VERIFY'
#!/bin/sh
# omakase-harness fail-closed guard. Generated by init.sh.
ROOT="$(git rev-parse --show-toplevel 2>/dev/null)" || exit 0
COMMON="$(cd "$(git rev-parse --git-common-dir)" && pwd)"
LIST="$COMMON/omakase/placed.list"
[ -f "$LIST" ] || exit 0   # harness not installed -> nothing to verify
missing=0
while IFS= read -r rel; do
  [ -z "$rel" ] && continue
  [ -e "$ROOT/$rel" ] || [ -L "$ROOT/$rel" ] && continue
  git -C "$ROOT" ls-files --error-unmatch "$rel" >/dev/null 2>&1 && continue  # tracked: upstream owns it (warned at init/checkout)
  [ "$missing" -eq 0 ] && echo "omakase: BLOCKING — the injected harness is incomplete; its gates would silently not run:" >&2
  echo "  missing: $rel" >&2
  missing=1
done < "$LIST"
[ "$missing" -eq 0 ] && exit 0
echo "omakase: restore it with  bash $COMMON/omakase/ensure-present.sh  (or /omakase init), then retry." >&2
exit 1
VERIFY
chmod +x "$OMK/verify-overlay.sh"

# ---- fail-closed gate stubs ----
# install-guards.sh inserts a guard block into the enforcement stubs (pre-commit,
# pre-push) that runs verify-overlay.sh before lefthook and blocks when the overlay
# is gone. It is a SCRIPT in the shared git dir, not inline code, because lefthook's
# npm package runs `lefthook install -f` in its postinstall on EVERY npm/yarn
# install, regenerating the stubs and stripping the block; the generated
# ensure-present.sh calls this on post-checkout, so the guard self-heals on the next
# checkout/pull. The guard sits ABOVE lefthook on purpose: overlay integrity is not
# a lefthook check, so LEFTHOOK=0 does not bypass it — the only escape is git's own
# --no-verify. Inert once the harness is removed ($COMMON/omakase deleted).
# post-checkout is deliberately NOT guarded: its job (ensure-present) is the
# self-heal path and must keep running. Marked block, strip-then-insert: idempotent.
cat > "$OMK/install-guards.sh" <<'GUARDS'
#!/bin/sh
# omakase-harness fail-closed guard installer. Generated by init.sh; called by
# init.sh after `lefthook install` and by ensure-present.sh on every checkout
# (lefthook's npm postinstall regenerates the stubs, stripping the block — this
# re-arms it). Idempotent (strip-then-insert).
COMMON="$(cd "$(git rev-parse --git-common-dir 2>/dev/null)" && pwd)" || exit 0
HOOKS_DIR="$COMMON/hooks"
GBEGIN="# >>> omakase-harness fail-closed >>>"
GEND="# <<< omakase-harness fail-closed <<<"
for h in pre-commit pre-push; do
  hf="$HOOKS_DIR/$h"
  [ -f "$hf" ] || continue
  grep -qi 'lefthook' "$hf" 2>/dev/null || continue   # only instrument lefthook-managed stubs
  awk -v b="$GBEGIN" -v e="$GEND" '$0==b{s=1} !s{print} $0==e{s=0}' "$hf" > "$hf.tmp"
  {
    head -n1 "$hf.tmp"   # the shebang
    printf '%s\n' "$GBEGIN"
    cat <<'GUARD'
# Fail-closed: this runs ABOVE lefthook, so LEFTHOOK=0 does not bypass it;
# the only escape is git's own --no-verify.
omakase_verify="$(git rev-parse --git-common-dir)/omakase/verify-overlay.sh"
if [ -f "$omakase_verify" ]; then
  sh "$omakase_verify" || exit 1
fi
GUARD
    printf '%s\n' "$GEND"
    tail -n +2 "$hf.tmp"
  } > "$hf"
  rm -f "$hf.tmp"
  chmod +x "$hf"
done
GUARDS
chmod +x "$OMK/install-guards.sh"

if [ "$RESET_HOOKSPATH" -eq 1 ]; then
  git -C "$ROOT" config --unset core.hooksPath 2>/dev/null || true
  echo "omakase: cleared redundant core.hooksPath (it named the repo's own hooks dir; lefthook refuses to install while it is set — the effective hooks dir is unchanged)."
fi
( cd "$ROOT" && $LEFTHOOK install )
sh "$OMK/install-guards.sh"

echo "omakase: placed ${#placed[@]} file(s), overwrote ${#overwrote[@]:-0} to match payload, skipped ${#skipped[@]} committed path(s)."
for p in "${placed[@]:-}"; do [ -n "$p" ] && echo "  + $p"; done
for o in "${overwrote[@]:-}"; do [ -n "$o" ] && echo "  ^ overwrote to match payload (any local edit replaced): $o"; done
for s in "${skipped[@]:-}"; do [ -n "$s" ] && echo "  ~ skipped (committed — re-run with --cut-over to let the harness copy take over; guarded, see init.sh --help): $s"; done
echo "omakase: ignores -> .git/info/exclude; hooks installed; new worktrees auto-install the harness. Nothing to commit."
echo "omakase: see the whole harness any time with  /omakase show"
# Only advertise the scorecard status line when the payload actually ships it.
# A payload may ship gates but no status-line segment (it forgoes the scorecard
# surface), and a dangling wire-up instruction is worse than none.
if [ -f "$ROOT/.omakase/bin/omakase-statusline.sh" ]; then
  echo "omakase: status line — compose the scorecard into your existing bar (it never"
  echo "         takes over the bar). Add this command to your status-line script:"
  echo "           bash $ROOT/.omakase/bin/omakase-statusline.sh"
  echo "         Claude Code: your ~/.claude statusLine script. Copilot CLI: ~/.copilot. tmux: status-right."
fi
