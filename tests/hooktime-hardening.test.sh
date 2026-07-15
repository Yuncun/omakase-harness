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
#      the one they run IN, so `omakase hook` unsets all three and resolves
#      from cwd only (verify-only run: LEFTHOOK=0 omakase hook pre-commit —
#      LEFTHOOK=0 skips the gates, never the verify; heal: hook post-checkout).
#      E1 the gate verify still BLOCKS a gutted overlay under the leaked env
#      E2 the heal restores the cwd repo under the leaked env
#      E3 omakase-gate's verdict row lands in the cwd repo's ledger, and the
#         leaked repo's git dir stays untouched
#   A. WRITE-ONCE HOOKS (#98) — hook files are permanent dispatchers written
#      only by `omakase init`; NOTHING at hook time may rewrite them (the #96
#      corruption class: lefthook's run-time sync + omakase's own re-arm both
#      edited live hook files). A1 pins the invariant end-to-end: bytes, inode,
#      and mtime of every dispatcher unchanged across a real commit, a branch
#      checkout, and a gate-config edit.
#      A2 branch state is read at fire time: two worktrees with DIFFERENT gate
#      wiring each run their OWN branch's gates on commit — the pre-#98 scheme
#      raced here because both worktrees kept rewriting the one shared hook.
#   M. MIRRORS — the gate primitive ships as two byte-identical copies
#      (payload + the Go binary's embedded template). Pin the pair so a fix to
#      one copy cannot silently miss the other.
set -u
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
INIT="$HERE/../bin/init.sh"
LEFTHOOK="${LEFTHOOK_BIN:-$(command -v lefthook || true)}"
OMAKASE="$( cd "$HERE/.." && HERE="$PWD/bin" && . bin/lib-omakase-bin.sh && resolve_omakase 2>/dev/null && echo "$OMAKASE_BIN_RESOLVED" )"
[ -n "$OMAKASE" ] || { echo "FATAL: no omakase binary resolvable"; exit 1; }
TMP="${TMPDIR:-/tmp}/omakase-hooktime-hardening.$$"
FAILED=0
pass(){ echo "  PASS: $1"; }
fail(){ echo "  FAIL: $1"; FAILED=1; }

newrepo(){ rm -rf "$1"; mkdir -p "$1"; ( cd "$1" && git init -q && git config user.email t@t && git config user.name t && git config commit.gpgsign false && git commit -q --allow-empty -m init ); }
common_of(){ echo "$(cd "$1" && cd "$(git rev-parse --git-common-dir)" && pwd)"; }

[ -n "$LEFTHOOK" ] && export PATH="$(dirname "$LEFTHOOK"):$PATH"
mkdir -p "$TMP"

echo "== M: the shipped copies of the gate primitive are byte-identical =="
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

echo "== E1: the gate verify fails CLOSED under a leaked GIT_DIR/GIT_WORK_TREE/GIT_COMMON_DIR =="
rm -f "$REPOA/$REL"
OUT="$( cd "$REPOA" && GIT_DIR="$REPOB/.git" GIT_WORK_TREE="$REPOB" GIT_COMMON_DIR="$REPOB/.git" LEFTHOOK=0 "$OMAKASE" hook pre-commit 2>&1 )"; RC=$?
if [ "$RC" -ne 0 ] && printf '%s' "$OUT" | grep -q "missing: $REL"; then
  pass "E1: gutted overlay still blocks (exit $RC, names $REL)"
else
  fail "E1: fell OPEN under the leaked env (exit $RC, out: $OUT)"
fi

echo "== E2: the heal restores the cwd repo under the leaked env =="
OUT="$( cd "$REPOA" && GIT_DIR="$REPOB/.git" GIT_WORK_TREE="$REPOB" GIT_COMMON_DIR="$REPOB/.git" "$OMAKASE" hook post-checkout 2>&1 )"; RC=$?
[ "$RC" -eq 0 ] || fail "E2: heal exited $RC ($OUT)"
if [ -f "$REPOA/$REL" ]; then
  pass "E2: $REL healed back into the repo the script ran in"
else
  fail "E2: $REL still missing — heal was misdirected by the leaked env"
fi
if ( cd "$REPOA" && LEFTHOOK=0 "$OMAKASE" hook pre-commit ) >/dev/null 2>&1; then
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

echo "== A1: hook files are write-once — untouched by commits, checkouts, config edits =="
HOOKSDIR="$(common_of "$REPOA")/hooks"
# mtime: GNU stat (-c) first — on Linux, BSD-style `stat -f '%m'` half-succeeds
# in filesystem mode and prints drifting free-block counts; on macOS `stat -c`
# fails cleanly and the BSD form takes over.
snap_hooks(){ for h in pre-commit pre-push post-checkout; do ls -i "$HOOKSDIR/$h"; stat -c '%Y' "$HOOKSDIR/$h" 2>/dev/null || stat -f '%m' "$HOOKSDIR/$h"; cat "$HOOKSDIR/$h"; done; }
BEFORE="$(snap_hooks)"
( cd "$REPOA" && echo a1 > a1.txt && git add a1.txt && git commit -q -m a1 ) >/dev/null 2>&1
( cd "$REPOA" && git checkout -q -b hookstill ) 2>/dev/null
printf '# an edited gate config\n' >> "$REPOA/lefthook-local.yml"
( cd "$REPOA" && echo a2 > a2.txt && git add a2.txt && git commit -q -m a2 ) >/dev/null 2>&1
AFTER="$(snap_hooks)"
[ "$BEFORE" = "$AFTER" ] && pass "A1: dispatcher bytes+inode+mtime unchanged across commit, checkout, and config edit" \
  || fail "A1: a hook file changed at hook time — the #96 writer class is back"

echo "== A2: two worktrees with different wiring each run their own gates =="
WTA="$TMP/wt-alpha"
( cd "$REPOA" && git worktree add -q "$WTA" -b alpha ) >/dev/null 2>&1
# Each side gets its own wiring with a distinct gate marker. The worktree copy
# is gitignored per checkout, so the two coexist on one shared .git.
cat > "$REPOA/lefthook-local.yml" <<'YML'
pre-commit:
  jobs:
    - name: main-gate
      run: echo GATE-MAIN-RAN
YML
cat > "$WTA/lefthook-local.yml" <<'YML'
pre-commit:
  jobs:
    - name: alpha-gate
      run: echo GATE-ALPHA-RAN
YML
HOOKS_SNAP="$(snap_hooks)"
OUTM="$( cd "$REPOA" && echo m > m.txt && git add m.txt && git commit -m m 2>&1 )"
OUTA="$( cd "$WTA"   && echo a > a.txt && git add a.txt && git commit -m a 2>&1 )"
{ printf '%s' "$OUTM" | grep -q 'GATE-MAIN-RAN' && ! printf '%s' "$OUTM" | grep -q 'GATE-ALPHA-RAN'; } \
  && pass "A2: the main checkout ran ITS wiring only" \
  || fail "A2: main-checkout commit ran the wrong gates ($OUTM)"
{ printf '%s' "$OUTA" | grep -q 'GATE-ALPHA-RAN' && ! printf '%s' "$OUTA" | grep -q 'GATE-MAIN-RAN'; } \
  && pass "A2: the linked worktree ran ITS wiring only" \
  || fail "A2: worktree commit ran the wrong gates ($OUTA)"
[ "$HOOKS_SNAP" = "$(snap_hooks)" ] && pass "A2: zero writes to .git/hooks from either worktree's commit" \
  || fail "A2: a worktree commit wrote to the shared hooks dir"
( cd "$REPOA" && git worktree remove --force "$WTA" ) 2>/dev/null; ( cd "$REPOA" && git worktree prune ) 2>/dev/null

rm -rf "$TMP"
[ "$FAILED" -eq 0 ] && echo "hooktime-hardening: ALL PASS" || echo "hooktime-hardening: FAILURES"
exit "$FAILED"
