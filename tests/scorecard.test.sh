#!/usr/bin/env bash
# TDD spec for the harness scorecard: the run-ledger recorder, the status-line
# segment, and the /omakase show "RECENT RUNS" section. The scorecard answers
# "is the harness green, and how long ago did it run?" at a glance — a persistent
# at-rest view for someone who stepped away mid-task. ledger lines are TAB-separated
# (epoch, hook, gate, verdict, duration_ms); assertions use awk, not grep -P (BSD).
set -u
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
RECORD="$HERE/../payload/.omakase/bin/omakase-record.sh"
SEG="$HERE/../payload/.omakase/bin/omakase-statusline.sh"
SHOW="$HERE/../bin/show.sh"
INIT="$HERE/../bin/init.sh"
REMOVE="$HERE/../bin/remove.sh"
LEFTHOOK="${LEFTHOOK_BIN:-/Users/ericshen/Claude/pixterm-engine/node_modules/.bin/lefthook}"
TMP="${TMPDIR:-/tmp}/omakase-scorecard-test.$$"
NOW=1700000000
FAILED=0
pass(){ echo "  PASS: $1"; }
fail(){ echo "  FAIL: $1"; FAILED=1; }

newrepo(){ rm -rf "$1"; mkdir -p "$1"; ( cd "$1" && git init -q && git config user.email t@t && git config user.name t && git config commit.gpgsign false && git commit -q --allow-empty -m init ); }
ledger_of(){ echo "$(cd "$1" && cd "$(git rev-parse --git-common-dir)" && pwd)/omakase/ledger.tsv"; }
has_run(){ awk -F'\t' -v g="$2" -v v="$3" '$3==g && $4==v{f=1} END{exit f?0:1}' "$1"; }

export PATH="$(dirname "$LEFTHOOK"):$PATH"

# ---------- Scenario R: recorder writes a ledger line and passes exit through ----------
echo "== Scenario R: omakase-record =="
REPO="$TMP/repoR"; newrepo "$REPO"
LEDGER="$(ledger_of "$REPO")"

( cd "$REPO" && bash "$RECORD" mygate -- bash -c 'exit 0' ); rc=$?
[ "$rc" -eq 0 ] && pass "record passes through exit 0" || fail "record did not pass through exit 0 (got $rc)"
[ -f "$LEDGER" ] && pass "ledger file created in shared git dir" || fail "ledger file not created"
has_run "$LEDGER" mygate pass && pass "pass run recorded (gate + verdict)" || { fail "pass run not recorded"; sed 's/^/      /' "$LEDGER" 2>/dev/null; }

( cd "$REPO" && bash "$RECORD" failgate -- bash -c 'exit 7' ); rc=$?
[ "$rc" -eq 7 ] && pass "record preserves a non-zero exit code" || fail "record lost the exit code (got $rc, want 7)"
has_run "$LEDGER" failgate fail && pass "fail run recorded with verdict=fail" || fail "fail verdict not recorded"

line="$(awk -F'\t' '$3=="mygate"{print; exit}' "$LEDGER")"
nf=$(printf '%s' "$line" | awk -F'\t' '{print NF}')
[ "$nf" -eq 5 ] && pass "ledger line has 5 tab-separated fields" || fail "ledger line has $nf fields, want 5"
printf '%s' "$line" | awk -F'\t' '$1 ~ /^[0-9]+$/ && $5 ~ /^[0-9]+$/{ok=1} END{exit ok?0:1}' && pass "epoch and duration are numeric" || fail "epoch/duration not numeric"

rm -rf "$(dirname "$LEDGER")"
( cd "$REPO" && bash "$RECORD" g2 -- true ); rc=$?
{ [ "$rc" -eq 0 ] && [ -f "$LEDGER" ]; } && pass "recorder recreates a missing ledger dir (best-effort)" || fail "recorder did not recreate the ledger dir"

# ---------- Scenario S: status-line segment ----------
echo "== Scenario S: omakase-statusline segment =="
REPO="$TMP/repoS"; newrepo "$REPO"
LEDGER="$(ledger_of "$REPO")"; mkdir -p "$(dirname "$LEDGER")"

OUT="$( cd "$REPO" && bash "$SEG" )"
echo "$OUT" | grep -q '🍣' && pass "segment shows the sushi icon" || fail "no sushi icon"
echo "$OUT" | grep -qi 'ready' && pass "empty ledger -> ready" || fail "empty ledger not 'ready' ($OUT)"

: > "$LEDGER"
printf '%s\t%s\t%s\t%s\t%s\n' $((NOW-180)) pre-commit typecheck pass 12 >> "$LEDGER"
OUT="$( cd "$REPO" && OMAKASE_NOW=$NOW NO_COLOR=1 bash "$SEG" )"
echo "$OUT" | grep -q '✓' && pass "all-pass -> green check" || fail "no check for all-pass ($OUT)"
echo "$OUT" | grep -q '3m' && pass "renders time-ago (3m)" || fail "missing 3m time-ago ($OUT)"
printf '%s' "$OUT" | grep -q "$(printf '\033')" && fail "NO_COLOR not honored (ANSI present)" || pass "NO_COLOR strips ANSI"

OUT="$( cd "$REPO" && OMAKASE_NOW=$NOW bash "$SEG" )"
printf '%s' "$OUT" | grep -q "$(printf '\033')" && pass "ANSI color present by default" || fail "no ANSI color by default"

printf '%s\t%s\t%s\t%s\t%s\n' $((NOW-60)) pre-push test fail 30 >> "$LEDGER"
OUT="$( cd "$REPO" && OMAKASE_NOW=$NOW NO_COLOR=1 bash "$SEG" )"
echo "$OUT" | grep -q '✗' && pass "a failing gate -> red cross" || fail "no cross when a gate failed ($OUT)"

: > "$LEDGER"
printf '%s\t%s\t%s\t%s\t%s\n' $((NOW-600)) pre-commit lint fail 5 >> "$LEDGER"
printf '%s\t%s\t%s\t%s\t%s\n' $((NOW-120)) pre-commit lint pass 5 >> "$LEDGER"
OUT="$( cd "$REPO" && OMAKASE_NOW=$NOW NO_COLOR=1 bash "$SEG" )"
echo "$OUT" | grep -q '✓' && pass "latest-per-gate: a fixed gate shows green again" || fail "stale failure stuck red ($OUT)"

: > "$LEDGER"; printf '%s\t-\tg\tpass\t0\n' $((NOW-30)) >> "$LEDGER"
echo "$( cd "$REPO" && OMAKASE_NOW=$NOW NO_COLOR=1 bash "$SEG" )" | grep -q '<1m' && pass "ago bucket <1m" || fail "ago <1m wrong"
: > "$LEDGER"; printf '%s\t-\tg\tpass\t0\n' $((NOW-7200)) >> "$LEDGER"
echo "$( cd "$REPO" && OMAKASE_NOW=$NOW NO_COLOR=1 bash "$SEG" )" | grep -q '2h' && pass "ago bucket 2h" || fail "ago 2h wrong"
: > "$LEDGER"; printf '%s\t-\tg\tpass\t0\n' $((NOW-172800)) >> "$LEDGER"
echo "$( cd "$REPO" && OMAKASE_NOW=$NOW NO_COLOR=1 bash "$SEG" )" | grep -q '2d' && pass "ago bucket 2d" || fail "ago 2d wrong"

: > "$LEDGER"; printf '%s\tpre-push\tg\tpass\t0\n' $((NOW-60)) >> "$LEDGER"
echo "$( cd "$REPO" && OMAKASE_NOW=$NOW NO_COLOR=1 bash "$SEG" )" | grep -q 'pre-push' && pass "shows the trigger label when recorded" || fail "trigger label missing"

# ---------- Scenario T: /omakase show RECENT RUNS ----------
echo "== Scenario T: show RECENT RUNS section =="
PAY="$HERE/../payload"; REPO="$TMP/repoT"; newrepo "$REPO"
( cd "$REPO" && OMAKASE_PAYLOAD="$PAY" bash "$INIT" ) >/dev/null 2>&1
OUT="$( cd "$REPO" && bash "$SHOW" 2>&1 )"
echo "$OUT" | grep -qi 'RECENT RUNS' && pass "show has a RECENT RUNS section" || fail "show missing RECENT RUNS"
echo "$OUT" | grep -qi 'no gate runs' && pass "empty-state line before any run" || fail "no empty-state line"
LEDGER="$(ledger_of "$REPO")"; mkdir -p "$(dirname "$LEDGER")"
printf '%s\tpre-commit\ttypecheck\tpass\t11\n' $((NOW-120)) >> "$LEDGER"
printf '%s\tpre-push\ttest\tfail\t40\n' $((NOW-60)) >> "$LEDGER"
OUT="$( cd "$REPO" && OMAKASE_NOW=$NOW bash "$SHOW" 2>&1 )"
echo "$OUT" | grep -q 'typecheck' && pass "show lists a recorded gate" || fail "show missing gate row"
echo "$OUT" | grep -q 'fail' && pass "show shows a fail verdict" || fail "show missing fail verdict"
( cd "$REPO" && OMAKASE_PAYLOAD="$PAY" bash "$REMOVE" ) >/dev/null 2>&1

# ---------- Scenario U: end-to-end — a real commit records through the shipped wiring ----------
echo "== Scenario U: a real lefthook commit writes the ledger =="
PAY="$HERE/../payload"; REPO="$TMP/repoU"; newrepo "$REPO"
( cd "$REPO" && OMAKASE_PAYLOAD="$PAY" bash "$INIT" ) >/dev/null 2>&1
LEDGER="$(ledger_of "$REPO")"
( cd "$REPO" && echo hi > f.txt && git add f.txt && git commit -m t ) >/dev/null 2>&1
{ [ -f "$LEDGER" ] && has_run "$LEDGER" omakase-example pass; } && pass "a real commit recorded the example gate (verdict=pass)" || { fail "no pass ledger entry after a real commit"; sed 's/^/      /' "$LEDGER" 2>/dev/null; }
( cd "$REPO" && OMAKASE_PAYLOAD="$PAY" bash "$REMOVE" ) >/dev/null 2>&1

rm -rf "$TMP"
echo ""
[ "$FAILED" -eq 0 ] && echo "ALL PASS" || { echo "FAILURES PRESENT"; exit 1; }
