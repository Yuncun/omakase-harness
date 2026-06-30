#!/usr/bin/env bash
# Behavioral spec for the ONE gate primitive (omakase-gate.sh). Exercises the real shipped
# script: the always-run case, --cacheable caching, --record, deferment, --glob scoping,
# the audited skip var, concurrency, run-recording, and an end-to-end git push. The store
# is one append-only TSV (epoch<tab>name<tab>verdict<tab>sha) in the shared git dir.
set -u
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
GATE="$HERE/../payload/.omakase/bin/omakase-gate.sh"
INIT="$HERE/../bin/init.sh"
SHOW="$HERE/../bin/status.sh"
PAY="$HERE/../payload"
LEFTHOOK="${LEFTHOOK_BIN:-$(command -v lefthook || true)}"
TMP="${TMPDIR:-/tmp}/omakase-gate-test.$$"
FAILED=0
pass(){ echo "  PASS: $1"; }
fail(){ echo "  FAIL: $1"; FAILED=1; }
newrepo(){ rm -rf "$1"; mkdir -p "$1"; ( cd "$1" && git init -q && git config user.email t@t && git config user.name t && git config commit.gpgsign false && git commit -q --allow-empty -m init ); }
ledger_of(){ echo "$(cd "$1" && cd "$(git rev-parse --git-common-dir)" && pwd)/omakase/ledger.tsv"; }
has_row(){ awk -F'\t' -v n="$2" -v v="$3" '$2==n && $3==v{f=1} END{exit f?0:1}' "$1"; }
export PATH="$(dirname "$LEFTHOOK"):$PATH"

echo "== Cycle A: always-run core =="
REPO="$TMP/repoA"; newrepo "$REPO"; LEDGER="$(ledger_of "$REPO")"

# misuse: no args -> exit 2
OUT="$( cd "$REPO" && bash "$GATE" 2>&1 )"; RC=$?
[ "$RC" -eq 2 ] && pass "no args -> misuse exit 2" || fail "no-args exit $RC ($OUT)"
# misuse: name but neither --step nor --record -> exit 2
OUT="$( cd "$REPO" && bash "$GATE" g 2>&1 )"; RC=$?
[ "$RC" -eq 2 ] && pass "name without --step/--record -> exit 2" || fail "bare name exit $RC ($OUT)"

# always-run pass: step exits 0 -> exit 0 + a pass row
OUT="$( cd "$REPO" && bash "$GATE" mygate --step 'true' 2>&1 )"; RC=$?
[ "$RC" -eq 0 ] && pass "passing step -> exit 0" || fail "passing step exit $RC ($OUT)"
{ [ -f "$LEDGER" ] && has_row "$LEDGER" mygate pass; } && pass "passing step records a pass row" || fail "no pass row recorded"

# always-run block: step exits 7 -> exit 7 (code passed through unchanged) + a fail row
OUT="$( cd "$REPO" && bash "$GATE" failgate --step 'exit 7' 2>&1 )"; RC=$?
[ "$RC" -eq 7 ] && pass "failing step passes its exit code through (7)" || fail "exit code not preserved ($RC)"
has_row "$LEDGER" failgate fail && pass "failing step records a fail row" || fail "no fail row recorded"

# row schema: exactly 4 columns; the 4th is the commit sha
line="$(awk -F'\t' '$2=="mygate"{print; exit}' "$LEDGER")"
nf=$(printf '%s' "$line" | awk -F'\t' '{print NF}')
[ "$nf" -eq 4 ] && pass "ledger row has 4 fields" || fail "row has $nf fields, want 4"
sha="$(printf '%s' "$line" | awk -F'\t' '{print $4}')"
head="$(cd "$REPO" && git rev-parse HEAD)"
[ "$sha" = "$head" ] && pass "4th field is the commit sha" || fail "sha mismatch ($sha vs $head)"

# audited skip var: OMAKASE_SKIP_<NAME>=1 skips even a blocking step
OUT="$( cd "$REPO" && OMAKASE_SKIP_FAILGATE=1 bash "$GATE" failgate --step 'exit 1' 2>&1 )"; RC=$?
{ [ "$RC" -eq 0 ] && echo "$OUT" | grep -q 'OMAKASE_SKIP_FAILGATE'; } && pass "skip var bypasses a blocking gate" || fail "skip var did not bypass ($RC: $OUT)"

# hardening: resolve common-dir BEFORE the step (a step that cd's still records its row)
OUT="$( cd "$REPO" && bash "$GATE" cdgate --step 'cd /tmp' 2>&1 )"; RC=$?
has_row "$LEDGER" cdgate pass && pass "records even when the step changes directory" || fail "cd-in-step dropped the row"
# hardening: outside any git repo -> pass the step's code through, write no stray omakase/
OUTSIDE="$TMP/notarepo"; rm -rf "$OUTSIDE"; mkdir -p "$OUTSIDE"
OUT="$( cd "$OUTSIDE" && bash "$GATE" g --step 'true' 2>&1 )"; RC=$?
[ "$RC" -eq 0 ] && pass "outside a repo: passes the step exit through" || fail "outside-repo exit $RC"
[ ! -e "$OUTSIDE/omakase" ] && pass "outside a repo: writes no stray omakase/ dir" || fail "littered outside a repo"
# hardening: a tab in the name must not shift columns
( cd "$REPO" && bash "$GATE" "$(printf 'tab\tname')" --step 'true' ) >/dev/null 2>&1
nf=$(tail -1 "$LEDGER" | awk -F'\t' '{print NF}')
[ "$nf" -eq 4 ] && pass "tab in name sanitized (row stays 4 fields)" || fail "tab in name shifted columns ($nf)"

# dotted gate name bypass: '.' must map to '_' in the skip var
OUT="$( cd "$REPO" && OMAKASE_SKIP_LINT_FAST=1 bash "$GATE" lint.fast --step 'exit 1' 2>&1 )"; RC=$?
[ "$RC" -eq 0 ] && pass "dotted gate name: OMAKASE_SKIP_LINT_FAST bypasses lint.fast" || fail "dotted gate name bypass did not work ($RC: $OUT)"

# --record rejects extra flags
OUT="$( cd "$REPO" && bash "$GATE" x --record --cacheable 2>&1 )"; RC=$?
[ "$RC" -eq 2 ] && pass "--record --cacheable -> misuse exit 2" || fail "--record --cacheable exit $RC ($OUT)"

echo "== Cycle B: --cacheable, --record, deferment =="
REPO="$TMP/repoB"; newrepo "$REPO"; LEDGER="$(ledger_of "$REPO")"
( cd "$REPO" && mkdir -p src && printf 'a\n' > src/app.txt && git add src/app.txt && git commit -q -m c1 )

# --cacheable freshness: no row -> runs; after a pass -> next run skips (cached)
runs="$TMP/ran.B"; : > "$runs"
step="printf x >> $runs"
OUT="$( cd "$REPO" && bash "$GATE" cached --cacheable --step "$step" 2>&1 )"
[ "$(wc -c < "$runs" | tr -d ' ')" = "1" ] && pass "cacheable: first run executes the step" || fail "cacheable first run did not execute ($OUT)"
OUT="$( cd "$REPO" && bash "$GATE" cached --cacheable --step "$step" 2>&1 )"
{ [ "$(wc -c < "$runs" | tr -d ' ')" = "1" ] && echo "$OUT" | grep -q 'cached'; } && pass "cacheable: a fresh pass skips the step" || fail "cacheable did not skip on a fresh pass ($OUT)"
# HEAD moves -> the pass is stale -> the step runs again
( cd "$REPO" && printf 'b\n' > src/more.txt && git add src/more.txt && git commit -q -m c2 )
OUT="$( cd "$REPO" && bash "$GATE" cached --cacheable --step "$step" 2>&1 )"
[ "$(wc -c < "$runs" | tr -d ' ')" = "2" ] && pass "cacheable: a new commit busts the cache (re-runs)" || fail "cacheable did not re-run after HEAD moved ($OUT)"

# --record: writes a PASS for HEAD with no step; a subsequent --cacheable run skips
REPO="$TMP/repoR"; newrepo "$REPO"; LEDGER="$(ledger_of "$REPO")"
OUT="$( cd "$REPO" && bash "$GATE" review --record 2>&1 )"; RC=$?
{ [ "$RC" -eq 0 ] && has_row "$LEDGER" review pass; } && pass "--record writes a pass row, exit 0" || fail "--record did not write a pass ($RC: $OUT)"
ran="$TMP/ran.R"; : > "$ran"
OUT="$( cd "$REPO" && bash "$GATE" review --cacheable --step "printf x >> $ran" 2>&1 )"
[ ! -s "$ran" ] && pass "--record then --cacheable run skips the step" || fail "cacheable ran despite a recorded pass ($OUT)"

# --record fail-loud: an unwritable ledger dir -> exit non-zero and say so
REPO="$TMP/repoRL"; newrepo "$REPO"
COMMON="$(cd "$REPO" && cd "$(git rev-parse --git-common-dir)" && pwd)"
# make the omakase dir un-creatable by planting a FILE where the dir must go
rm -rf "$COMMON/omakase"; : > "$COMMON/omakase"
OUT="$( cd "$REPO" && bash "$GATE" review --record 2>&1 )"; RC=$?
{ [ "$RC" -ne 0 ] && echo "$OUT" | grep -qi 'FAILED to record'; } && pass "--record fails loud on a write error" || fail "--record did not fail loud ($RC: $OUT)"
rm -f "$COMMON/omakase"

# deferment (case 3): a blocking step blocks; after --record the same HEAD is allowed
REPO="$TMP/repoD"; newrepo "$REPO"; LEDGER="$(ledger_of "$REPO")"
blocker='echo "BLOCKED: run review then: omakase-gate.sh review --record" >&2; exit 1'
OUT="$( cd "$REPO" && bash "$GATE" review --cacheable --step "$blocker" 2>&1 )"; RC=$?
{ [ "$RC" -ne 0 ] && echo "$OUT" | grep -q 'BLOCKED'; } && pass "deferment: the blocking step blocks first" || fail "deferment did not block ($RC: $OUT)"
( cd "$REPO" && bash "$GATE" review --record ) >/dev/null
OUT="$( cd "$REPO" && bash "$GATE" review --cacheable --step "$blocker" 2>&1 )"; RC=$?
[ "$RC" -eq 0 ] && pass "deferment: after --record the same HEAD is allowed" || fail "deferment still blocked after --record ($RC: $OUT)"

# fail row must NOT satisfy the cache: a blocked gate must re-run, not skip on a fail row
REPOCF="$TMP/repoCF"; newrepo "$REPOCF"
m="$TMP/cf.mark"; : > "$m"
( cd "$REPOCF" && bash "$GATE" cf --cacheable --step "printf x >> $m; exit 1" ) >/dev/null 2>&1
( cd "$REPOCF" && bash "$GATE" cf --cacheable --step "printf x >> $m; exit 1" ) >/dev/null 2>&1
[ "$(wc -c < "$m" | tr -d ' ')" = "2" ] && pass "a fail row does not satisfy the cache (re-runs, not skips)" || fail "fail row wrongly cached (step did not re-run)"

echo "== Cycle C: --glob scope, concurrency, end-to-end =="
# A bare repo as origin so origin/HEAD resolves a base for the --glob range.
REMOTE="$TMP/remoteC.git"; git init -q --bare "$REMOTE"
REPO="$TMP/repoC"; newrepo "$REPO"
( cd "$REPO" && git branch -M main && git remote add origin "$REMOTE" && git push -q -u origin main )
( cd "$REPO" && mkdir -p src docs && printf 'a\n' > src/app.txt && git add src/app.txt && git commit -q -m feat )
LEDGER="$(ledger_of "$REPO")"

# glob match -> the step runs (records a row)
OUT="$( cd "$REPO" && bash "$GATE" g1 --glob 'src/*' --step 'true' 2>&1 )"
has_row "$LEDGER" g1 pass && pass "glob match: the step runs" || fail "glob match did not run ($OUT)"
# glob miss -> skip (no row, exit 0)
OUT="$( cd "$REPO" && bash "$GATE" g2 --glob 'docs/*' --step 'false' 2>&1 )"; RC=$?
{ [ "$RC" -eq 0 ] && ! has_row "$LEDGER" g2 fail; } && pass "glob miss: skips (no run)" || fail "glob miss did not skip ($RC: $OUT)"
# no --glob -> always runs even when nothing in range would match
OUT="$( cd "$REPO" && bash "$GATE" g3 --step 'false' 2>&1 )"; RC=$?
{ [ "$RC" -ne 0 ] && has_row "$LEDGER" g3 fail; } && pass "no --glob: always in scope (runs every time)" || fail "no-glob gate did not run ($RC: $OUT)"

# multi-pattern --glob: a change under the SECOND pattern must trigger the gate
( cd "$REPO" && mkdir -p lib && printf 'y\n' > lib/util.txt && git add lib/util.txt && git commit -q -m libchange )
OUT="$( cd "$REPO" && bash "$GATE" mg --glob 'src/* lib/*' --step 'false' 2>&1 )"; RC=$?
[ "$RC" -ne 0 ] && pass "multi-pattern --glob matches the second pattern (lib/*)" || fail "multi-pattern glob missed the second pattern ($RC: $OUT)"

# base fail-open: a repo with no remote and no resolvable base -> skip, never a git error
REPONB="$TMP/repoNB"; newrepo "$REPONB"
( cd "$REPONB" && mkdir -p src && printf 'a\n' > src/app.txt && git add src/app.txt && git commit -q -m c1 )
OUT="$( cd "$REPONB" && bash "$GATE" fo --glob 'src/*' --step 'false' 2>&1 )"; RC=$?
{ [ "$RC" -eq 0 ] && echo "$OUT" | grep -q 'no resolvable base'; } && pass "glob: fails open when no base resolves" || fail "did not fail open without a base ($RC: $OUT)"

# two-dot fallback: an orphan HEAD with unrelated history (three-dot fatal) must still find
# an in-scope change via the two-dot fallback, so "no changes" cannot masquerade as skip.
REMOTE2="$TMP/remote2.git"; git init -q --bare "$REMOTE2"
REPO2="$TMP/repo2dot"; newrepo "$REPO2"
( cd "$REPO2" && git branch -M main && git remote add origin "$REMOTE2" && printf 'b\n' > base.txt && git add base.txt && git commit -q -m base && git push -q -u origin main )
( cd "$REPO2" && git checkout -q --orphan orphanwork && git rm -rfq --cached . 2>/dev/null; rm -f base.txt && mkdir -p src && printf 'x\n' > src/app.txt && git add src/app.txt && git commit -q -m orphan )
m2="$TMP/td.mark"; : > "$m2"
OUT="$( cd "$REPO2" && bash "$GATE" td --glob 'src/*' --step "printf x >> $m2; exit 1" 2>&1 )"; RC=$?
{ [ "$RC" -ne 0 ] && [ -s "$m2" ]; } && pass "two-dot fallback finds the in-scope change on unrelated histories (step ran)" || fail "two-dot fallback did not find the change ($RC: $OUT)"

# concurrency: N parallel appends yield N complete (untorn) 4-field rows
REPOC="$TMP/repoCC"; newrepo "$REPOC"; LEDGERC="$(ledger_of "$REPOC")"
( cd "$REPOC" && for i in 1 2 3 4 5 6 7 8; do bash "$GATE" "cc$i" --step 'true' & done; wait ) >/dev/null 2>&1
rows=$(grep -c . "$LEDGERC"); torn=$(awk -F'\t' 'NF!=4{n++} END{print n+0}' "$LEDGERC")
{ [ "$rows" -eq 8 ] && [ "$torn" -eq 0 ]; } && pass "concurrency: 8 parallel appends -> 8 untorn rows" || fail "concurrency: $rows rows, $torn torn"

# end-to-end: a real git push through an installed pre-push hook wired to the primitive.
echo "== Cycle C: end-to-end git push =="
PAYE="$TMP/payE"; REPOE="$TMP/repoE"; REMOTEE="$TMP/remoteE.git"
mkdir -p "$PAYE"; cp -R "$PAY/." "$PAYE/"
cat > "$PAYE/lefthook-local.yml" <<'YML'
pre-push:
  jobs:
    - name: review
      run: bash .omakase/bin/omakase-gate.sh review --cacheable --glob 'src/*' --step 'echo "BLOCKED - record review then push" >&2; exit 1'
post-checkout:
  jobs:
    - name: omakase-ensure-present
      run: bash "$(git rev-parse --git-common-dir)/omakase/ensure-present.sh"
YML
newrepo "$REPOE"; git init -q --bare "$REMOTEE"
( cd "$REPOE" && git branch -M main && git remote add origin "$REMOTEE" && git push -q -u origin main )
( cd "$REPOE" && OMAKASE_PAYLOAD="$PAYE" bash "$INIT" ) >/dev/null 2>&1
LEDGERE="$(ledger_of "$REPOE")"
( cd "$REPOE" && mkdir -p src && printf 'x\n' > src/app.txt && git add src/app.txt && git commit -q -m feat )
OUT="$( cd "$REPOE" && git push origin main 2>&1 )"; RC=$?
{ [ "$RC" -ne 0 ] && echo "$OUT" | grep -q 'BLOCKED'; } && pass "e2e: push BLOCKED when review never recorded for the commit" || fail "e2e push not blocked ($RC: $OUT)"
has_row "$LEDGERE" review fail && pass "e2e: the blocked run recorded a fail row" || fail "e2e no fail row in the ledger"
( cd "$REPOE" && bash "$GATE" review --record ) >/dev/null
OUT="$( cd "$REPOE" && git push origin main 2>&1 )"; RC=$?
[ "$RC" -eq 0 ] && pass "e2e: push ALLOWED after --record for the same commit" || fail "e2e push still blocked after --record ($RC: $OUT)"
OUT="$( cd "$REPOE" && bash "$SHOW" --markdown 2>&1 )"
echo "$OUT" | grep -q 'review' && pass "e2e: omakase status renders the review gate" || fail "show did not render the gate"

echo "== Upgrade: a pre-v2 (6-col) ledger is rotated aside on init =="
PAYU="$TMP/payU"; REPOU="$TMP/repoU"
mkdir -p "$PAYU"; cp -R "$PAY/." "$PAYU/"
newrepo "$REPOU"
COMMONU="$(cd "$REPOU" && cd "$(git rev-parse --git-common-dir)" && pwd)"
mkdir -p "$COMMONU/omakase"
# plant an old 6-column ledger
printf '%s\tpre-commit\told-gate\tpass\t40\t%s\n' 1700000000 "$(cd "$REPOU" && git rev-parse HEAD)" > "$COMMONU/omakase/ledger.tsv"
( cd "$REPOU" && OMAKASE_PAYLOAD="$PAYU" bash "$INIT" ) >/dev/null 2>&1
[ -f "$COMMONU/omakase/ledger.tsv.pre-v2.bak" ] && pass "upgrade: pre-v2 ledger rotated to .pre-v2.bak" || fail "pre-v2 ledger not rotated"
{ [ ! -f "$COMMONU/omakase/ledger.tsv" ] || ! awk -F'\t' 'NF>=6{f=1} END{exit f?0:1}' "$COMMONU/omakase/ledger.tsv"; } && pass "upgrade: the live ledger no longer holds 6-col rows" || fail "6-col rows survived in the live ledger"

rm -rf "$TMP"
echo ""
[ "$FAILED" -eq 0 ] && echo "ALL PASS" || { echo "FAILURES PRESENT"; exit 1; }
