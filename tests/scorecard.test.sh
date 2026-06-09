#!/usr/bin/env bash
# TDD spec for the harness STATUS SURFACES:
#   - omakase-ledger.sh      : run-ledger recorder; stamps epoch/hook/gate/verdict/ms/SHA
#   - omakase-statusline.sh  : the CANARY — "<name> is running" where the harness is
#                              active, dark elsewhere. No verdict, only the 🍣 icon.
#   - omakase-stop-notice.sh : the Stop-hook CHECKLIST — the pre-push checks for the
#                              CURRENT commit in gate order, ✓ passed / ✗ not-completed.
#   - bin/show.sh            : /omakase show RECENT RUNS (+ --markdown)
# Ledger lines are TAB-separated (epoch, hook, gate, verdict, ms, sha); assertions use
# awk, not grep -P (BSD).
set -u
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
RECORD="$HERE/../payload/.omakase/bin/omakase-ledger.sh"
CANARY="$HERE/../payload/.omakase/bin/omakase-statusline.sh"
NOTICE="$HERE/../payload/.omakase/bin/omakase-stop-notice.sh"
BANNER_REL=".omakase/bin/omakase-banner.sh"
SHOW="$HERE/../bin/show.sh"
INIT="$HERE/../bin/init.sh"
REMOVE="$HERE/../bin/remove.sh"
PAY="$HERE/../payload"
LEFTHOOK="${LEFTHOOK_BIN:-/Users/ericshen/Claude/pixterm-engine/node_modules/.bin/lefthook}"
TMP="${TMPDIR:-/tmp}/omakase-status-test.$$"
NOW=1700000000
FAILED=0
pass(){ echo "  PASS: $1"; }
fail(){ echo "  FAIL: $1"; FAILED=1; }
newrepo(){ rm -rf "$1"; mkdir -p "$1"; ( cd "$1" && git init -q && git config user.email t@t && git config user.name t && git config commit.gpgsign false && git commit -q --allow-empty -m init ); }
ledger_of(){ echo "$(cd "$1" && cd "$(git rev-parse --git-common-dir)" && pwd)/omakase/ledger.tsv"; }
has_run(){ awk -F'\t' -v g="$2" -v v="$3" '$3==g && $4==v{f=1} END{exit f?0:1}' "$1"; }
export PATH="$(dirname "$LEFTHOOK"):$PATH"

# ---------- Scenario R: recorder writes a 6-col line (incl. the commit SHA) ----------
echo "== Scenario R: omakase-ledger records epoch,hook,gate,verdict,ms,SHA =="
REPO="$TMP/repoR"; newrepo "$REPO"; LEDGER="$(ledger_of "$REPO")"
( cd "$REPO" && bash "$RECORD" mygate -- bash -c 'exit 0' ); rc=$?
[ "$rc" -eq 0 ] && pass "record passes exit 0 through" || fail "record lost exit 0 ($rc)"
{ [ -f "$LEDGER" ] && has_run "$LEDGER" mygate pass; } && pass "pass run recorded" || fail "pass run not recorded"
( cd "$REPO" && bash "$RECORD" failgate -- bash -c 'exit 7' ); rc=$?
[ "$rc" -eq 7 ] && pass "record preserves exit 7" || fail "record lost exit 7 ($rc)"
has_run "$LEDGER" failgate fail && pass "fail run recorded" || fail "fail run not recorded"
line="$(awk -F'\t' '$3=="mygate"{print; exit}' "$LEDGER")"
nf=$(printf '%s' "$line" | awk -F'\t' '{print NF}')
[ "$nf" -eq 6 ] && pass "ledger line has 6 fields" || fail "ledger line has $nf fields, want 6"
sha="$(printf '%s' "$line" | awk -F'\t' '{print $6}')"
head="$(cd "$REPO" && git rev-parse HEAD)"
[ "$sha" = "$head" ] && pass "6th field is the commit sha" || fail "sha mismatch ($sha vs $head)"
rm -rf "$(dirname "$LEDGER")"
( cd "$REPO" && bash "$RECORD" g2 -- true ); { [ -f "$LEDGER" ]; } && pass "recreates a missing ledger dir" || fail "did not recreate ledger dir"

# ---------- Scenario C: the canary ----------
echo "== Scenario C: omakase-statusline canary =="
REPO="$TMP/repoC"; newrepo "$REPO"
OUT="$( cd "$REPO" && NO_COLOR=1 bash "$CANARY" )"
[ -z "$OUT" ] && pass "dark where the harness is not installed" || fail "canary lit without harness ($OUT)"
mkdir -p "$REPO/.omakase"
OUT="$( cd "$REPO" && NO_COLOR=1 bash "$CANARY" )"
echo "$OUT" | grep -q '🍣' && pass "shows the sushi icon" || fail "no icon ($OUT)"
echo "$OUT" | grep -q 'omakase is running' && pass "says 'omakase is running' (default name)" || fail "wrong text ($OUT)"
printf '%s' "$OUT" | grep -q "$(printf '\033')" && fail "NO_COLOR not honored" || pass "NO_COLOR strips ANSI"
OUT="$( cd "$REPO" && bash "$CANARY" )"
printf '%s' "$OUT" | grep -q "$(printf '\033')" && pass "ANSI color by default" || fail "no color by default"
OUT="$( cd "$REPO" && OMAKASE_NAME=widget NO_COLOR=1 bash "$CANARY" )"
echo "$OUT" | grep -q 'widget is running' && pass "OMAKASE_NAME overrides the name" || fail "name override failed ($OUT)"
printf 'gizmo\n' > "$REPO/.omakase/NAME"
OUT="$( cd "$REPO" && NO_COLOR=1 bash "$CANARY" )"
echo "$OUT" | grep -q 'gizmo is running' && pass ".omakase/NAME sets the name" || fail "NAME file ignored ($OUT)"
OUTSIDE="$TMP/notarepo"; rm -rf "$OUTSIDE"; mkdir -p "$OUTSIDE"
OUT="$( cd "$OUTSIDE" && bash "$CANARY" )"
[ -z "$OUT" ] && pass "dark outside any git repo" || fail "canary lit outside a repo ($OUT)"

# ---------- Scenario K: the Stop-hook checklist ----------
echo "== Scenario K: omakase-stop-notice checklist =="
REPO="$TMP/repoK"; newrepo "$REPO"; LEDGER="$(ledger_of "$REPO")"; mkdir -p "$(dirname "$LEDGER")"
HEAD="$(cd "$REPO" && git rev-parse HEAD)"
# config: pre-push order gamma, alpha, beta + a pre-commit gate that MUST be excluded
cat > "$REPO/lefthook-local.yml" <<YML
pre-commit:
  jobs:
    - run: bash .omakase/bin/omakase-ledger.sh precommit-gate -- true
pre-push:
  jobs:
    - name: checks
      group:
        jobs:
          - run: bash .omakase/bin/omakase-ledger.sh gamma -- true
          - run: bash .omakase/bin/omakase-ledger.sh alpha -- true
          - run: bash .omakase/bin/omakase-ledger.sh beta -- true
post-checkout:
  jobs:
    - run: true
YML
SIN='{"cwd":"'"$REPO"'"}'
notice(){ printf '%s' "$SIN" | bash "$NOTICE"; }
# baseline row (old sha, not HEAD) so the ledger exists and history order != config order
printf '%s\tpre-push\tbeta\tpass\t0\tdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef\n' 1700000000 >> "$LEDGER"
OUT="$(notice)"; [ -z "$OUT" ] && pass "first run is silent (inits marker)" || fail "first run not silent ($OUT)"
OUT="$(notice)"; [ -z "$OUT" ] && pass "no change -> silent (the guard)" || fail "fired with no change ($OUT)"
# gamma passes, beta fails, alpha never runs — for THIS commit
printf '%s\tpre-push\tgamma\tpass\t0\t%s\n' "$(date +%s)" "$HEAD" >> "$LEDGER"
printf '%s\tpre-push\tbeta\tfail\t0\t%s\n'  "$(date +%s)" "$HEAD" >> "$LEDGER"
OUT="$(notice)"
echo "$OUT" | grep -q 'gamma ✓' && pass "passed check -> ✓" || fail "no ✓ for passed ($OUT)"
echo "$OUT" | grep -q 'alpha ✗' && pass "not-run check -> ✗" || fail "no ✗ for not-run ($OUT)"
echo "$OUT" | grep -q 'beta ✗'  && pass "failed check -> ✗ (not ✓)" || fail "failed check not ✗ ($OUT)"
echo "$OUT" | grep -q 'gamma.*alpha.*beta' && pass "config (gate) order respected over ledger history" || fail "wrong order ($OUT)"
echo "$OUT" | grep -q 'precommit-gate' && fail "pre-commit gate leaked into the checklist" || pass "pre-commit gate excluded"
OUT="$(notice)"; [ -z "$OUT" ] && pass "after firing, no change -> silent" || fail "re-fired with no change ($OUT)"
( cd "$REPO" && git commit -q --allow-empty -m c2 )
OUT="$(notice)"
echo "$OUT" | grep -q 'gamma ✗' && pass "new commit resets the checklist (all ✗)" || fail "checklist did not reset on new HEAD ($OUT)"

# ---------- Scenario S: /omakase show tolerates 6-col rows ----------
echo "== Scenario S: show reads the 6-col ledger =="
REPO="$TMP/repoS"; newrepo "$REPO"
( cd "$REPO" && OMAKASE_PAYLOAD="$PAY" bash "$INIT" ) >/dev/null 2>&1
LEDGER="$(ledger_of "$REPO")"; mkdir -p "$(dirname "$LEDGER")"
HEAD="$(cd "$REPO" && git rev-parse HEAD)"
printf '%s\tpre-commit\ttypecheck\tpass\t11\t%s\n' $((NOW-120)) "$HEAD" >> "$LEDGER"
printf '%s\tpre-push\ttest\tfail\t40\t%s\n' $((NOW-60)) "$HEAD" >> "$LEDGER"
OUT="$( cd "$REPO" && OMAKASE_NOW=$NOW bash "$SHOW" 2>&1 )"
echo "$OUT" | grep -q 'typecheck' && pass "show lists a recorded gate (6-col)" || fail "show missed 6-col gate"
echo "$OUT" | grep -q 'fail' && pass "show shows a fail verdict" || fail "show missing fail verdict"
OUT="$( cd "$REPO" && OMAKASE_NOW=$NOW bash "$SHOW" --markdown 2>&1 )"
echo "$OUT" | grep -q '| Gate | Verdict | When |' && pass "markdown table renders" || fail "no markdown table"
echo "$OUT" | grep -qE '\| test \| .* fail \|' && pass "markdown fail row (6-col)" || fail "no fail row in markdown"
( cd "$REPO" && OMAKASE_PAYLOAD="$PAY" bash "$REMOVE" ) >/dev/null 2>&1

# ---------- Scenario U: a real commit records a 6-col row through the wiring ----------
echo "== Scenario U: a real lefthook commit writes a 6-col ledger row =="
REPO="$TMP/repoU"; newrepo "$REPO"
( cd "$REPO" && OMAKASE_PAYLOAD="$PAY" bash "$INIT" ) >/dev/null 2>&1
LEDGER="$(ledger_of "$REPO")"
( cd "$REPO" && echo hi > f.txt && git add f.txt && git commit -m t ) >/dev/null 2>&1
{ [ -f "$LEDGER" ] && has_run "$LEDGER" omakase-example pass; } && pass "real commit recorded the example gate" || { fail "no pass row after a real commit"; sed 's/^/      /' "$LEDGER" 2>/dev/null; }
nf=$(awk -F'\t' '$3=="omakase-example"{print NF; exit}' "$LEDGER")
[ "$nf" -eq 6 ] && pass "real commit row has 6 fields" || fail "real commit row has $nf fields"
( cd "$REPO" && OMAKASE_PAYLOAD="$PAY" bash "$REMOVE" ) >/dev/null 2>&1

# ---------- Scenario V: hardening (recorder + canary) ----------
echo "== Scenario V: hardening =="
OUTSIDE="$TMP/notarepo2"; rm -rf "$OUTSIDE"; mkdir -p "$OUTSIDE"
( cd "$OUTSIDE" && bash "$RECORD" g -- true ); rc=$?
[ "$rc" -eq 0 ] && pass "recorder outside a repo passes exit through" || fail "recorder outside repo exit $rc"
[ ! -e "$OUTSIDE/omakase" ] && pass "recorder writes no stray omakase/ outside a repo" || fail "recorder littered outside a repo"
REPO="$TMP/repoV"; newrepo "$REPO"; LEDGER="$(ledger_of "$REPO")"; mkdir -p "$(dirname "$LEDGER")"
( cd "$REPO" && bash "$RECORD" cdgate -- cd /tmp ) >/dev/null 2>&1
has_run "$LEDGER" cdgate pass && pass "records even when the gate changes directory" || fail "cd-in-gate dropped the record"
( cd "$REPO" && bash "$RECORD" emptyg -- ); rc=$?
{ [ "$rc" -eq 0 ] && ! has_run "$LEDGER" emptyg pass; } && pass "empty command records nothing, exits 0" || fail "empty command mishandled"
( cd "$REPO" && bash "$RECORD" "$(printf 'tab\tname')" -- true ) >/dev/null 2>&1
nf=$(tail -1 "$LEDGER" | awk -F'\t' '{print NF}')
[ "$nf" -eq 6 ] && pass "tab in gate name sanitized (line stays 6 fields)" || fail "tab in gate name shifted columns ($nf)"

# ---------- Scenario W: branding (banner + version, no drift) ----------
echo "== Scenario W: branding =="
REPO="$TMP/repoW"; newrepo "$REPO"
( cd "$REPO" && OMAKASE_PAYLOAD="$PAY" bash "$INIT" ) >/dev/null 2>&1
BAN="$REPO/$BANNER_REL"
VER="$(cat "$PAY/.omakase/VERSION")"
PJV="$(grep -o '"version"[^,]*' "$HERE/../.claude-plugin/plugin.json" | grep -o '[0-9][0-9.]*')"
[ "$PJV" = "$VER" ] && pass "payload VERSION matches plugin.json ($PJV)" || fail "VERSION drift: plugin.json=$PJV payload=$VER"
OUT="$( cd "$REPO" && NO_COLOR=1 bash "$BAN" pre-commit )"
echo "$OUT" | grep -q 'omakase-harness' && pass "banner shows the plugin name" || fail "banner missing name"
echo "$OUT" | grep -q "v$VER" && pass "banner shows the version" || fail "banner missing version ($OUT)"
OUT="$( cd "$REPO" && bash "$SHOW" 2>&1 )"
echo "$OUT" | grep -q 'omakase-harness' && pass "show prints a branded header" || fail "show missing header"
( cd "$REPO" && OMAKASE_PAYLOAD="$PAY" bash "$REMOVE" ) >/dev/null 2>&1

rm -rf "$TMP"
echo ""
[ "$FAILED" -eq 0 ] && echo "ALL PASS" || { echo "FAILURES PRESENT"; exit 1; }
