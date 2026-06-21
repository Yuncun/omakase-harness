#!/usr/bin/env bash
# Proof of the deferred gate: a job records a result keyed to the commit
# (omakase-record.sh), and the push gate (deferred-check.sh) blocks unless a
# fresh PASS exists for the commit being pushed. Exercises the REAL shipped
# scripts in payload/, every branch of the gate, and one end-to-end git push.
set -u
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
INIT="$HERE/../bin/init.sh"
SHOW="$HERE/../bin/show.sh"
RECORD="$HERE/../payload/.omakase/bin/omakase-record.sh"
DEFERRED="$HERE/../payload/.omakase/gates/deferred-check.sh"
LEFTHOOK="${LEFTHOOK_BIN:-/Users/ericshen/Claude/pixterm-engine/node_modules/.bin/lefthook}"
TMP="${TMPDIR:-/tmp}/omakase-deferred-gate-test.$$"
FAILED=0
pass(){ echo "  PASS: $1"; }
fail(){ echo "  FAIL: $1"; FAILED=1; }

newrepo(){ rm -rf "$1"; mkdir -p "$1"; ( cd "$1" && git init -q && git config user.email t@t && git config user.name t && git config commit.gpgsign false && git commit -q --allow-empty -m init ); }

export PATH="$(dirname "$LEFTHOOK"):$PATH"

# ============================================================================
# Direct-invocation coverage. A repo with a known base commit ($C0) and a change
# under src/, so OMAKASE_BASE makes scope deterministic with no remote needed.
# This drives deferred-check.sh exactly as lefthook does at pre-push.
# ============================================================================
echo "== Direct: deferred-check.sh branches =="
REPO="$TMP/repo"; newrepo "$REPO"
C0="$(cd "$REPO" && git rev-parse HEAD)"
( cd "$REPO" && mkdir -p src && printf 'a\n' > src/app.txt && git add src/app.txt && git commit -q -m c1 )
DEFDIR="$(cd "$REPO" && d="$(git rev-parse --git-path omakase)" && mkdir -p "$d/deferred" && cd "$d/deferred" && pwd)"

# Drive the gate exactly as lefthook does. RC must be read from the command
# substitution itself (its last command is the gate), NOT from a helper run in a
# subshell — a function can't export RC back across a $(...) boundary.
dcheck(){ cd "$REPO" && env OMAKASE_BASE="$C0" OMAKASE_GLOB='src/*' "$@" bash "$DEFERRED" 2>&1; }

# 1. dormant: no OMAKASE_CHECK -> exits 0, does nothing.
OUT="$( cd "$REPO" && bash "$DEFERRED" 2>&1 )"; RC=$?
[ "$RC" -eq 0 ] && pass "dormant when OMAKASE_CHECK unset" || fail "not dormant ($RC: $OUT)"

# 2. in scope, no record -> BLOCK.
OUT="$( dcheck OMAKASE_CHECK=nr )"; RC=$?
{ [ "$RC" -ne 0 ] && echo "$OUT" | grep -q 'no record'; } && pass "blocks when no record exists" || fail "did not block on missing record ($RC: $OUT)"

# 3. glob does not match the changed file -> SKIP (exit 0).
OUT="$( cd "$REPO" && env OMAKASE_BASE="$C0" OMAKASE_GLOB='docs/*' OMAKASE_CHECK=nm bash "$DEFERRED" 2>&1 )"; RC=$?
{ [ "$RC" -eq 0 ] && echo "$OUT" | grep -q 'no files matching'; } && pass "skips when no pushed file matches the glob" || fail "did not skip on glob miss ($RC: $OUT)"

# 4. escape hatch OMAKASE_SKIP_<NAME>=1 -> SKIP even when it would block.
OUT="$( dcheck OMAKASE_CHECK=nr OMAKASE_SKIP_NR=1 )"; RC=$?
{ [ "$RC" -eq 0 ] && echo "$OUT" | grep -q 'skipped via OMAKASE_SKIP_NR'; } && pass "per-check escape hatch skips the gate" || fail "escape hatch did not skip ($RC: $OUT)"

# 5. fresh PASS recorded for HEAD -> ALLOW.
( cd "$REPO" && bash "$RECORD" --check ok --verdict pass ) >/dev/null
OUT="$( dcheck OMAKASE_CHECK=ok )"; RC=$?
{ [ "$RC" -eq 0 ] && echo "$OUT" | grep -q 'fresh PASS'; } && pass "allows when a fresh PASS exists for the commit" || fail "blocked despite a fresh PASS ($RC: $OUT)"

# 6. recorded FAIL -> BLOCK.
( cd "$REPO" && bash "$RECORD" --check bad --verdict fail ) >/dev/null
OUT="$( dcheck OMAKASE_CHECK=bad )"; RC=$?
{ [ "$RC" -ne 0 ] && echo "$OUT" | grep -q "verdict was 'fail'"; } && pass "blocks when the recorded verdict is fail" || fail "did not block on a fail verdict ($RC: $OUT)"

# 7. waiver (pass over a judged fail, with a reason) -> ALLOW and surface the reason loudly.
( cd "$REPO" && bash "$RECORD" --check wv --verdict pass --original-verdict fail --reason "ship for the demo" ) >/dev/null
OUT="$( dcheck OMAKASE_CHECK=wv )"; RC=$?
{ [ "$RC" -eq 0 ] && echo "$OUT" | grep -q 'WAIVED' && echo "$OUT" | grep -q 'ship for the demo'; } && pass "waiver allows the push and surfaces the override reason" || fail "waiver not honored/surfaced ($RC: $OUT)"

# 8. corrupt record -> BLOCK (fail closed on an unparseable record).
printf 'not json at all\n' > "$DEFDIR/cr.json"
OUT="$( dcheck OMAKASE_CHECK=cr )"; RC=$?
{ [ "$RC" -ne 0 ] && echo "$OUT" | grep -q 'corrupt or incomplete'; } && pass "blocks on a corrupt record" || fail "did not block on a corrupt record ($RC: $OUT)"

# 9. stale record: recorded for C1, then a new commit moves HEAD -> BLOCK.
( cd "$REPO" && bash "$RECORD" --check st --verdict pass ) >/dev/null
( cd "$REPO" && printf 'b\n' > src/more.txt && git add src/more.txt && git commit -q -m c2 )
OUT="$( dcheck OMAKASE_CHECK=st )"; RC=$?
{ [ "$RC" -ne 0 ] && echo "$OUT" | grep -q 'stale'; } && pass "blocks when the record is for an earlier commit (stale)" || fail "did not block on a stale record ($RC: $OUT)"

# 10. fail-open: no remote and no OMAKASE_BASE -> base unresolvable -> SKIP (never a raw git error).
OUT="$( cd "$REPO" && env OMAKASE_GLOB='src/*' OMAKASE_CHECK=fo bash "$DEFERRED" 2>&1 )"; RC=$?
{ [ "$RC" -eq 0 ] && echo "$OUT" | grep -q 'no resolvable base'; } && pass "fails open when no base ref resolves" || fail "did not fail open without a base ($RC: $OUT)"

# ============================================================================
# Recorder argument validation (omakase-record.sh).
# ============================================================================
echo "== Direct: omakase-record.sh validation =="
OUT="$( cd "$REPO" && bash "$RECORD" --verdict pass 2>&1 )"; RC=$?
{ [ "$RC" -eq 2 ] && echo "$OUT" | grep -q 'check required'; } && pass "record rejects a missing --check" || fail "record accepted a missing --check ($RC: $OUT)"
OUT="$( cd "$REPO" && bash "$RECORD" --check x --verdict maybe 2>&1 )"; RC=$?
{ [ "$RC" -eq 2 ] && echo "$OUT" | grep -q 'must be pass|fail'; } && pass "record rejects a bad --verdict" || fail "record accepted a bad --verdict ($RC: $OUT)"
OUT="$( cd "$REPO" && bash "$RECORD" --check x --verdict pass --original-verdict fail 2>&1 )"; RC=$?
{ [ "$RC" -eq 2 ] && echo "$OUT" | grep -q 'requires --reason'; } && pass "record requires a reason to waive a fail" || fail "record waived a fail with no reason ($RC: $OUT)"

# ============================================================================
# End-to-end: a real `git push` through the installed pre-push hook, wired the
# Style-1 way (omakase-ledger wraps deferred-check), proving the wiring fires,
# the run lands in the scorecard ledger, and `show` renders the deferred gate.
# ============================================================================
echo "== End-to-end: git push through the installed hook =="
PAY="$TMP/payE"; REPOE="$TMP/repoE"; REMOTE="$TMP/remoteE.git"
mkdir -p "$PAY"; cp -R "$HERE/../payload/." "$PAY/"
cat > "$PAY/lefthook-local.yml" <<'YML'
pre-push:
  jobs:
    - name: deferred-check-review
      run: bash .omakase/bin/omakase-ledger.sh review -- bash .omakase/gates/deferred-check.sh
      env:
        OMAKASE_CHECK: review
        OMAKASE_GLOB: 'src/*'
        OMAKASE_HOOK: pre-push
post-checkout:
  jobs:
    - name: omakase-ensure-present
      run: bash "$(git rev-parse --git-common-dir)/omakase/ensure-present.sh"
YML
newrepo "$REPOE"
git init -q --bare "$REMOTE"
( cd "$REPOE" && git branch -M main && git remote add origin "$REMOTE" && git push -q -u origin main )
( cd "$REPOE" && OMAKASE_PAYLOAD="$PAY" bash "$INIT" ) >/dev/null 2>&1
COMMON="$(cd "$REPOE" && cd "$(git rev-parse --git-common-dir)" && pwd)"
LEDGER_FILE="$COMMON/omakase/ledger.tsv"

# A change under src/ with no recorded review -> push BLOCKED.
( cd "$REPOE" && mkdir -p src && printf 'x\n' > src/app.txt && git add src/app.txt && git commit -q -m feat )
OUT="$( cd "$REPOE" && git push origin main 2>&1 )"; RC=$?
{ [ "$RC" -ne 0 ] && echo "$OUT" | grep -qi 'deferred gate'; } && pass "push BLOCKED when review never ran for the commit" || fail "push not blocked ($RC: $OUT)"
awk -F'\t' '$3=="review" && $4=="fail"{f=1} END{exit f?0:1}' "$LEDGER_FILE" 2>/dev/null && pass "blocked deferred run recorded in the ledger (verdict=fail)" || fail "no fail row in the ledger for the deferred gate"

# Record a PASS for the commit -> push ALLOWED.
( cd "$REPOE" && bash "$RECORD" --check review --verdict pass ) >/dev/null
OUT="$( cd "$REPOE" && git push origin main 2>&1 )"; RC=$?
[ "$RC" -eq 0 ] && pass "push ALLOWED after the job records a fresh PASS" || fail "push still blocked after recording pass ($RC: $OUT)"
awk -F'\t' '$3=="review" && $4=="pass"{p=1} END{exit p?0:1}' "$LEDGER_FILE" 2>/dev/null && pass "passing deferred run recorded in the ledger (verdict=pass)" || fail "no pass row in the ledger for the deferred gate"

# show renders the deferred gate as a guard with its deferred ENFORCES phrase.
OUT="$( cd "$REPOE" && bash "$SHOW" --markdown 2>&1 )"
echo "$OUT" | grep -q 'review' && pass "show lists the deferred gate guard" || fail "show did not list the deferred gate"
echo "$OUT" | grep -qi 'deferred gate' && pass "show labels it as a deferred gate" || fail "show did not label the deferred gate"

rm -rf "$TMP"
echo ""
[ "$FAILED" -eq 0 ] && echo "ALL PASS" || { echo "FAILURES PRESENT"; exit 1; }
