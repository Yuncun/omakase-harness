#!/usr/bin/env bash
# Hook-time hardening (issue #84 gaps 4+5). Two failure classes, both shown
# empirically by the 2026-07 worktree audit:
#   E. ENV LEAK — GIT_DIR/GIT_WORK_TREE/GIT_COMMON_DIR exported for ANOTHER
#      repo (a wrapper script, a parent hook, a stale shell — in a linked
#      worktree git exports GIT_DIR and GIT_COMMON_DIR as a pair) misdirect
#      every rev-parse in the hook-time scripts: the fail-closed guard resolves
#      the other repo, finds no ledger there, and exits 0 — fail OPEN — while
#      heal targets the wrong tree. GIT_COMMON_DIR alone is enough: it feeds
#      --git-common-dir directly, so the ledger path lands in the other repo
#      even when --show-toplevel is still correct. The scripts' repo is always
#      the one they run IN, so each unsets all three and resolves from cwd only.
#      E1 verify-overlay still BLOCKS a gutted overlay under the leaked env
#      E2 ensure-present heals the cwd repo under the leaked env
#      E3 omakase-gate's verdict row lands in the cwd repo's ledger, and the
#         leaked repo's git dir stays untouched
#   A. ATOMIC RE-ARM — install-guards.sh rewrote live hook files with a
#      truncating `>` while a hook may be mid-execution (one transient syntax
#      error observed). The rewrite must be compose-then-rename: a fresh inode
#      replaces the old, so a running interpreter keeps reading its original
#      bytes to the end.
#      A1 the hook file's inode CHANGES on re-arm; content still carries
#         exactly one guard block and the file stays executable
#   M. MIRRORS — each script ships as two byte-identical copies (bin/ + the Go
#      binary's embedded template; the gate as template + payload). Pin the
#      pairs so a fix to one copy cannot silently miss the other.
#      (install-guards' pair is already pinned by hook-failclosed H0.)
set -u
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
INIT="$HERE/../bin/init.sh"
LEFTHOOK="${LEFTHOOK_BIN:-$(command -v lefthook || true)}"
TMP="${TMPDIR:-/tmp}/omakase-hooktime-hardening.$$"
FAILED=0
pass(){ echo "  PASS: $1"; }
fail(){ echo "  FAIL: $1"; FAILED=1; }

newrepo(){ rm -rf "$1"; mkdir -p "$1"; ( cd "$1" && git init -q && git config user.email t@t && git config user.name t && git config commit.gpgsign false && git commit -q --allow-empty -m init ); }
common_of(){ echo "$(cd "$1" && cd "$(git rev-parse --git-common-dir)" && pwd)"; }

[ -n "$LEFTHOOK" ] && export PATH="$(dirname "$LEFTHOOK"):$PATH"
mkdir -p "$TMP"

echo "== M: the shipped copies of each hook-time script are byte-identical =="
for f in ensure-present verify-overlay; do
  cmp -s "$HERE/../bin/$f.sh" "$HERE/../internal/templates/files/$f.sh" \
    && pass "M: bin/$f.sh == internal/templates/files/$f.sh" \
    || fail "M: bin/$f.sh and internal/templates/files/$f.sh differ"
done
cmp -s "$HERE/../payload/.omakase/bin/omakase-gate.sh" "$HERE/../internal/templates/files/omakase-gate.sh" \
  && pass "M: payload omakase-gate.sh == internal/templates/files/omakase-gate.sh" \
  || fail "M: payload and embedded omakase-gate.sh differ"

# ---------- fixture: repoA carries a real install; repoB is the leak target ----------
REPOA="$TMP/repoA"; newrepo "$REPOA"
( cd "$REPOA" && bash "$INIT" ) >/dev/null 2>&1 || fail "setup: init exited non-zero in repoA"
OMKA="$(common_of "$REPOA")/omakase"
REPOB="$TMP/repoB"; newrepo "$REPOB"
REL="$(awk -F'\t' '{print $1; exit}' "$OMKA/placed.tsv" 2>/dev/null)"
[ -n "$REL" ] || fail "setup: no placed rows in $OMKA/placed.tsv"

echo "== E1: verify-overlay fails CLOSED under a leaked GIT_DIR/GIT_WORK_TREE/GIT_COMMON_DIR =="
rm -f "$REPOA/$REL"
OUT="$( cd "$REPOA" && GIT_DIR="$REPOB/.git" GIT_WORK_TREE="$REPOB" GIT_COMMON_DIR="$REPOB/.git" sh "$OMKA/verify-overlay.sh" 2>&1 )"; RC=$?
if [ "$RC" -ne 0 ] && printf '%s' "$OUT" | grep -q "missing: $REL"; then
  pass "E1: gutted overlay still blocks (exit $RC, names $REL)"
else
  fail "E1: fell OPEN under the leaked env (exit $RC, out: $OUT)"
fi

echo "== E2: ensure-present heals the cwd repo under the leaked env =="
OUT="$( cd "$REPOA" && GIT_DIR="$REPOB/.git" GIT_WORK_TREE="$REPOB" GIT_COMMON_DIR="$REPOB/.git" bash "$OMKA/ensure-present.sh" 2>&1 )"; RC=$?
[ "$RC" -eq 0 ] || fail "E2: heal exited $RC ($OUT)"
if [ -f "$REPOA/$REL" ]; then
  pass "E2: $REL healed back into the repo the script ran in"
else
  fail "E2: $REL still missing — heal was misdirected by the leaked env"
fi
if ( cd "$REPOA" && sh "$OMKA/verify-overlay.sh" ) >/dev/null 2>&1; then
  pass "E2: overlay verifies clean after the heal"
else
  fail "E2: overlay still failing verification after the heal"
fi

echo "== E3: omakase-gate's ledger row lands in the cwd repo under the leaked env =="
GATE="$HERE/../payload/.omakase/bin/omakase-gate.sh"
( cd "$REPOA" && GIT_DIR="$REPOB/.git" GIT_WORK_TREE="$REPOB" GIT_COMMON_DIR="$REPOB/.git" bash "$GATE" leaktest --step 'true' ) >/dev/null 2>&1 \
  || fail "E3: gate run exited non-zero"
if awk -F'\t' '$2=="leaktest" && $3=="pass"{found=1} END{exit !found}' "$OMKA/ledger.tsv" 2>/dev/null; then
  pass "E3: pass row recorded in the cwd repo's ledger"
else
  fail "E3: no leaktest row in $OMKA/ledger.tsv — the verdict went elsewhere"
fi
if [ -e "$REPOB/.git/omakase" ]; then
  fail "E3: the leaked repo's git dir grew an omakase/ dir — cross-repo write"
else
  pass "E3: the leaked repo's git dir stays untouched"
fi

echo "== A1: install-guards re-arms hook files by compose-then-rename =="
HF="$(common_of "$REPOA")/hooks/pre-commit"
if [ -f "$HF" ]; then
  INODE_BEFORE="$(ls -i "$HF" | awk '{print $1}')"
  ( cd "$REPOA" && sh "$OMKA/install-guards.sh" ) || fail "A1: install-guards exited non-zero"
  INODE_AFTER="$(ls -i "$HF" | awk '{print $1}')"
  if [ "$INODE_BEFORE" != "$INODE_AFTER" ]; then
    pass "A1: fresh inode after re-arm — a running hook keeps its original bytes"
  else
    fail "A1: same inode — the rewrite truncates the file a hook may be executing"
  fi
  N="$(grep -c '>>> omakase-harness fail-closed >>>' "$HF")"
  [ "$N" -eq 1 ] && pass "A1: exactly one fail-closed block after re-arm" \
    || fail "A1: $N fail-closed blocks after re-arm, want 1"
  [ -x "$HF" ] && pass "A1: hook still executable" || fail "A1: hook lost its exec bit"
else
  fail "A1 setup: no pre-commit stub after init (lefthook install missing?)"
fi

rm -rf "$TMP"
[ "$FAILED" -eq 0 ] && echo "hooktime-hardening: ALL PASS" || echo "hooktime-hardening: FAILURES"
exit "$FAILED"
