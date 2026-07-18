#!/usr/bin/env bash
# Phase 0 compat contract: init -> remove is an EXACT round trip (docs/v2-design.md
# §10 gate 2 — "remove reverses everything exactly"). In a repo seeded with user
# state (two exclude lines, a committed file, an untracked file omakase does not
# place), remove must restore byte-identical pre-init state, captured BEFORE any
# omakase invocation and compared after:
#   .git/info/exclude byte-identical (cmp), working-tree listing identical (the
#   untracked user file survives with its bytes, every placed file gone), the
#   .git/hooks state identical (omakase dispatchers gone, pre-existing hook files
#   untouched byte-for-byte), and $OMK ($(git rev-parse --git-common-dir)/omakase)
#   deleted.
# Scenarios:
#   R1 plain init -> remove (plugin-style: payload from the repo checkout)
#   R2 --source install -> remove (payload ships the CLAUDE.md -> AGENTS.md
#      symlink; after remove the symlink is GONE, everything else restored)
#   R3 a second remove on the already-clean repo exits 0 and changes nothing
# NOT re-asserted here (behavioral coverage elsewhere):
#   remove deletes placed tree / strips block, .worktreeinclude,
#   worktree-bootstrap                                  — tests/inject.test.sh
#   exclude-block bytes + $OMK layout while installed   — tests/golden-state.test.sh
#   placed.tsv / ledger.tsv column FORMAT               — tests/state-format.test.sh
set -u
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
INIT="$HERE/../bin/init.sh"
REMOVE="$HERE/../bin/remove.sh"
TMP="${TMPDIR:-/tmp}/omakase-roundtrip-test.$$"
FAILED=0
pass(){ echo "  PASS: $1"; }
fail(){ echo "  FAIL: $1"; FAILED=1; }

# File digest via whichever tool exists (shasum on macOS, sha256sum on Linux).
if command -v shasum >/dev/null 2>&1; then sha_file(){ shasum -a 256 "$1" | awk '{print $1}'; }
else sha_file(){ sha256sum "$1" | awk '{print $1}'; }; fi

newrepo(){ rm -rf "$1"; mkdir -p "$1"; ( cd "$1" && git init -q && git config user.email t@t && git config user.name t && git config commit.gpgsign false && git commit -q --allow-empty -m init ); }
common_of(){ echo "$(cd "$1" && cd "$(git rev-parse --git-common-dir)" && pwd)"; }

mkdir -p "$TMP"

# Sorted listing of every working-tree path (files, dirs, symlinks), .git excluded.
snap_tree(){ ( cd "$1" && find . -mindepth 1 -name .git -prune -o -print | sed 's|^\./||' | sort ); }

# Full .git/hooks state: one line per entry — name + content digest (regular
# file), readlink target (symlink), or "dir" — so the diff on failure names both
# leftover stub files AND byte changes to pre-existing hooks.
snap_hooks(){
  ( cd "$1" 2>/dev/null || exit 0
    find . -mindepth 1 -print | sed 's|^\./||' | sort | while IFS= read -r f; do
      if   [ -L "$f" ]; then printf '%s  link:%s\n' "$f" "$(readlink "$f")"
      elif [ -f "$f" ]; then printf '%s  %s\n' "$f" "$(sha_file "$f")"
      else printf '%s  dir\n' "$f"; fi
    done )
}

# Seed the user state the round trip must preserve, then capture the BEFORE
# state under prefix $2: byte copies of .git/info/exclude + the two user files,
# the tree listing, and the hooks state. Called before ANY omakase invocation.
seed_and_capture(){ # $1 = repo root, $2 = capture prefix
  local repo="$1" pre="$2" common
  ( cd "$repo" && printf 'hello committed\n' > README-user.md && git add README-user.md && git commit -q -m user )
  printf 'my untracked notes\n' > "$repo/notes-user.txt"   # untracked; omakase never places it
  common="$(common_of "$repo")"; mkdir -p "$common/info"
  printf 'scratch/\n*.tmp\n' > "$common/info/exclude"      # two pre-existing user lines
  cp "$common/info/exclude" "$pre.exclude"
  cp "$repo/README-user.md" "$pre.readme"
  cp "$repo/notes-user.txt" "$pre.notes"
  snap_tree "$repo" > "$pre.tree"
  snap_hooks "$common/hooks" > "$pre.hooks"
}

# AFTER == BEFORE, byte for byte. Failure output is the actual diff — which
# path leaked or vanished, which byte changed.
check_restored(){ # $1 = repo root, $2 = capture prefix, $3 = label
  local repo="$1" pre="$2" label="$3" common excl
  common="$(common_of "$repo")"; excl="$common/info/exclude"
  if cmp -s "$pre.exclude" "$excl"; then
    pass "$label: .git/info/exclude byte-identical to the BEFORE capture"
  else
    fail "$label: .git/info/exclude differs from the BEFORE bytes (< expected  > actual)"
    diff "$pre.exclude" "$excl" | sed 's/^/      /'
  fi
  snap_tree "$repo" > "$pre.tree.after"
  if cmp -s "$pre.tree" "$pre.tree.after"; then
    pass "$label: working-tree listing identical (every placed file gone, user files intact)"
  else
    fail "$label: working-tree listing differs (< missing after remove  > leaked by remove)"
    diff "$pre.tree" "$pre.tree.after" | sed 's/^/      /'
  fi
  snap_hooks "$common/hooks" > "$pre.hooks.after"
  if cmp -s "$pre.hooks" "$pre.hooks.after"; then
    pass "$label: .git/hooks state identical — no omakase hook wiring remains"
  else
    fail "$label: .git/hooks state differs (< expected  > actual; name + digest per hook)"
    diff "$pre.hooks" "$pre.hooks.after" | sed 's/^/      /'
  fi
  [ ! -e "$common/omakase" ] && pass "$label: \$OMK directory gone" \
    || fail "$label: \$OMK still exists ($common/omakase)"
  if cmp -s "$pre.notes" "$repo/notes-user.txt" 2>/dev/null; then
    pass "$label: untracked user file survived, bytes intact (notes-user.txt)"
  else
    fail "$label: untracked user file missing or changed (notes-user.txt)"
  fi
  if cmp -s "$pre.readme" "$repo/README-user.md" 2>/dev/null; then
    pass "$label: committed file intact, bytes identical (README-user.md)"
  else
    fail "$label: committed file missing or changed (README-user.md)"
  fi
}

# ---------- R1: plain init -> remove restores the pre-init state exactly ----------
echo "== R1: plain init -> remove — byte-identical restoration =="
REPO1="$TMP/repoR1"; newrepo "$REPO1"
seed_and_capture "$REPO1" "$TMP/R1"
( cd "$REPO1" && OMAKASE_PAYLOAD="$HERE/../payload" bash "$INIT" ) >/dev/null 2>&1 || fail "R1: plain init exited non-zero"
[ -s "$(common_of "$REPO1")/omakase/placed.tsv" ] \
  && pass "R1: init installed an overlay (placed.tsv non-empty) — the round trip is meaningful" \
  || fail "R1: init placed nothing (placed.tsv missing/empty) — restoration would be vacuous"
( cd "$REPO1" && bash "$REMOVE" ) >/dev/null 2>&1 || fail "R1: remove exited non-zero"
check_restored "$REPO1" "$TMP/R1" "R1"

# ---------- R2: --source install -> remove (payload carries a symlink) ----------
echo "== R2: --source init -> remove — symlink gone, state restored =="
FAKEHOME="$TMP/home"; CACHEHOME="$TMP/cache"; mkdir -p "$FAKEHOME" "$CACHEHOME"
SRC="$TMP/src-harness"; rm -rf "$SRC"; mkdir -p "$SRC/payload"
( cd "$SRC" && git init -q && git config user.email t@t && git config user.name t && git config commit.gpgsign false )
printf 'shared agent instructions\n' > "$SRC/payload/AGENTS.md"
( cd "$SRC/payload" && ln -s AGENTS.md CLAUDE.md )
printf 'name: roundtrip-fixture\n' > "$SRC/payload/omakase.manifest"
( cd "$SRC" && git add -A && git commit -q -m harness )
SRC="$(cd "$SRC" && pwd)"   # init absolutizes local dir sources (macOS TMPDIR carries a trailing slash)
REPO2="$TMP/repoR2"; newrepo "$REPO2"
seed_and_capture "$REPO2" "$TMP/R2"
( cd "$REPO2" && HOME="$FAKEHOME" XDG_CACHE_HOME="$CACHEHOME" bash "$INIT" --source "$SRC" ) >/dev/null 2>&1 \
  || fail "R2: --source init exited non-zero"
[ -L "$REPO2/CLAUDE.md" ] \
  && pass "R2: init placed the payload symlink (CLAUDE.md -> AGENTS.md) — its removal is testable" \
  || fail "R2: init did not place the CLAUDE.md symlink"
( cd "$REPO2" && HOME="$FAKEHOME" XDG_CACHE_HOME="$CACHEHOME" bash "$REMOVE" ) >/dev/null 2>&1 \
  || fail "R2: remove exited non-zero"
{ [ ! -e "$REPO2/CLAUDE.md" ] && [ ! -L "$REPO2/CLAUDE.md" ]; } \
  && pass "R2: placed symlink gone after remove" \
  || fail "R2: CLAUDE.md still present after remove"
check_restored "$REPO2" "$TMP/R2" "R2"

# ---------- R3: a second remove on the clean repo is a no-op that exits 0 ----------
echo "== R3: second remove — exits 0, changes nothing =="
cp "$(common_of "$REPO1")/info/exclude" "$TMP/R3.exclude"
cp "$REPO1/README-user.md" "$TMP/R3.readme"
cp "$REPO1/notes-user.txt" "$TMP/R3.notes"
snap_tree "$REPO1" > "$TMP/R3.tree"
snap_hooks "$(common_of "$REPO1")/hooks" > "$TMP/R3.hooks"
( cd "$REPO1" && bash "$REMOVE" ) >/dev/null 2>&1; RC=$?
[ "$RC" -eq 0 ] && pass "R3: second remove on a clean repo exits 0" \
  || fail "R3: second remove exited $RC"
check_restored "$REPO1" "$TMP/R3" "R3"

rm -rf "$TMP"
echo ""
[ "$FAILED" -eq 0 ] && echo "ALL PASS" || { echo "FAILURES PRESENT"; exit 1; }
