#!/usr/bin/env bash
# Behavioral spec for the ONE gate primitive (omakase-gate.sh). Exercises the real shipped
# script: the always-run case, --cacheable caching, --record, deferment, --glob scoping,
# the audited skip var, concurrency, run-recording, and an end-to-end git push. The store
# is one append-only TSV (epoch<tab>name<tab>verdict<tab>sha) in the shared git dir.
set -u
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
GATE="$HERE/../payload/.omakase/bin/omakase-gate.sh"
INIT="$HERE/../bin/init.sh"
SHOW="$HERE/../bin/show.sh"
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
