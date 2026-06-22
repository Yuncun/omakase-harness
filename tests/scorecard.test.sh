#!/usr/bin/env bash
# TDD spec for the harness STATUS SURFACES:
#   - omakase-ledger.sh      : run-ledger recorder; stamps epoch/hook/gate/verdict/ms/SHA
#   - omakase-statusline.sh  : the CANARY — "<name> is running" where the harness is
#                              active, dark elsewhere. No verdict, only the 🍣 icon.
#   - omakase-stop-notice.sh : the Stop-hook status — "<name> is active ✓" (light ✓, no colour)
#                              when gates are armed, plus a "Last run: <Hook> N/N checks at <clk>"
#                              line after a run (a failure shows there, in words; the header keeps
#                              "is active ✓"), "<name> is not active" when gates aren't armed, and
#                              a "files missing · /omakase init" nudge. Detail -> /omakase show.
#   - bin/show.sh            : /omakase show GUARDS chart (+ --markdown)
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
LEFTHOOK="${LEFTHOOK_BIN:-$(command -v lefthook || true)}"
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

# ---------- Scenario K: the Stop-hook status notice ----------
echo "== Scenario K: omakase-stop-notice status notice =="
REPO="$TMP/repoK"; newrepo "$REPO"; LEDGER="$(ledger_of "$REPO")"; mkdir -p "$(dirname "$LEDGER")"
mkdir -p "$REPO/.omakase"                                   # active (overlay present)
arm(){ mkdir -p "$REPO/.git/hooks"; printf '#!/bin/sh\nlefthook run %s\n' "$1" > "$REPO/.git/hooks/$1"; chmod +x "$REPO/.git/hooks/$1"; }
arm pre-commit                                             # gates armed (a lefthook stub)
HEAD="$(cd "$REPO" && git rev-parse HEAD)"
SA=sess-aaa; SB=sess-bbb
notice(){ printf '{"cwd":"%s","session_id":"%s"}' "$REPO" "$1" | bash "$NOTICE"; }

OUT="$(notice "$SA")"
echo "$OUT" | grep -q 'omakase is active ✓' && pass "armed + no runs -> 'is active ✓'" || fail "no active baseline ($OUT)"
OUT="$(notice "$SA")"; [ -z "$OUT" ] && pass "same session, no change -> silent" || fail "fired with no change ($OUT)"
OUT="$(notice "$SB")"; echo "$OUT" | grep -q 'is active ✓' && pass "a new session re-announces the resting state" || fail "no reshow on new session ($OUT)"

# a pre-push run, all three gates pass on HEAD
T=$(date +%s)
for g in gamma alpha beta; do printf '%s\tpre-push\t%s\tpass\t1000\t%s\n' "$T" "$g" "$HEAD" >> "$LEDGER"; done
OUT="$(notice "$SB")"
echo "$OUT" | grep -q 'Last run: Pre-push gate' && pass "a run names the hook (Pre-push gate)" || fail "no hook name ($OUT)"
echo "$OUT" | grep -q '3/3 checks at' && pass "all-pass -> 'N/N checks at <time>'" || fail "no N/N run summary ($OUT)"
echo "$OUT" | grep -q 'is active ✓' && pass "a clean run keeps the 'is active ✓' header" || fail "no active header on a clean run ($OUT)"
echo "$OUT" | grep -qE '[0-9]+:[0-9][0-9][AP]M' && pass "shows a clock time" || fail "no clock time ($OUT)"
echo "$OUT" | grep -q '✓' && pass "the header carries the light ✓" || fail "no ✓ on a clean run ($OUT)"
OUT="$(notice "$SB")"; [ -z "$OUT" ] && pass "after a run, no new run -> silent" || fail "re-fired after a run ($OUT)"

# a later run with a failure: beta fails (gamma/alpha still pass)
T2=$((T + 5))
for g in gamma alpha; do printf '%s\tpre-push\t%s\tpass\t1000\t%s\n' "$T2" "$g" "$HEAD" >> "$LEDGER"; done
printf '%s\tpre-push\tbeta\tfail\t1000\t%s\n' "$T2" "$HEAD" >> "$LEDGER"
OUT="$(notice "$SB")"
echo "$OUT" | grep -q 'omakase is active ✓' && pass "a failed run keeps the 'is active ✓' header" || fail "failed run changed the header ($OUT)"
echo "$OUT" | grep -qE '✗|❌|✖' && fail "a failed run must not show an X glyph" || pass "no X on a failed run"
echo "$OUT" | grep -q '1 check failed' && pass "failure -> count failed (singular, not a fraction)" || fail "no failure count ($OUT)"
echo "$OUT" | grep -qE '[0-9]+/[0-9]+' && fail "failure line should not show a pass fraction" || pass "failure line drops the run fraction"

# fail-then-fixed on the SAME commit: beta passes again -> back to all green (latest verdict wins)
T3=$((T2 + 5))
printf '%s\tpre-push\tbeta\tpass\t1000\t%s\n' "$T3" "$HEAD" >> "$LEDGER"
OUT="$(notice "$SB")"
echo "$OUT" | grep -q '3/3 checks at' && pass "fail-then-fixed counts as passed (latest verdict per gate)" || fail "fixed gate not re-counted ($OUT)"

# an empty-sha row (omakase-ledger writes one when HEAD is unborn — e.g. the first commit's
# pre-commit) must NOT become "the last run" and mask a later real run
T4=$((T3 + 5))
printf '%s\tpre-commit\tprecommit-gate\tpass\t1000\t\n' "$T4" >> "$LEDGER"   # 6 cols, empty sha
OUT="$(notice "$SB")"; [ -z "$OUT" ] && pass "empty-sha row alone -> silent (not a run)" || fail "empty-sha row spoke ($OUT)"
T5=$((T4 + 5))
for g in gamma alpha beta; do printf '%s\tpre-push\t%s\tpass\t1000\t%s\n' "$T5" "$g" "$HEAD" >> "$LEDGER"; done
OUT="$(notice "$SB")"
echo "$OUT" | grep -q '3/3 checks at' && pass "a real run after an empty-sha row still announces" || fail "real run masked by empty-sha row ($OUT)"

# a fresh run with TWO failures -> plural "N checks failed" (the singular path is not hardcoded)
T6=$((T5 + 5))
printf '%s\tpre-push\tgamma\tpass\t1000\t%s\n' "$T6" "$HEAD" >> "$LEDGER"
for g in alpha beta; do printf '%s\tpre-push\t%s\tfail\t1000\t%s\n' "$T6" "$g" "$HEAD" >> "$LEDGER"; done
OUT="$(notice "$SB")"
echo "$OUT" | grep -q '2 checks failed' && pass "two failures -> plural 'N checks failed'" || fail "no plural failure count ($OUT)"
echo "$OUT" | grep -qE '[0-9]+/[0-9]+' && fail "plural failure line should not show a fraction" || pass "plural failure line drops the fraction"

# gates no longer armed -> 'is not active'
rm -f "$REPO/.git/hooks/pre-commit"
OUT="$(notice "$SB")"
echo "$OUT" | grep -q 'omakase is not active' && pass "no armed hook -> 'is not active'" || fail "not 'is not active' with hooks gone ($OUT)"
printf '%s' "$OUT" | grep -qE '✓|✗|✅|❌' && fail "'is not active' should carry no glyph" || pass "'is not active' has no glyph"

# re-armed, but an enabled placed file is missing -> re-init nudge
arm pre-commit
printf '.omakase/gone.sh\tgate\tpayload\tdeadbeef\t1\n' > "$(dirname "$LEDGER")/placed.tsv"
OUT="$(notice "$SB")"
echo "$OUT" | grep -q '/omakase init to update' && pass "a missing placed file -> re-init nudge" || fail "no nudge for a missing file ($OUT)"
echo "$OUT" | grep -q 'files missing' && pass "the nudge names the reason" || fail "nudge missing its reason ($OUT)"

# a repo without the overlay stays silent (the global Stop hook must not chatter elsewhere)
REPO2="$TMP/repoK2"; newrepo "$REPO2"
OUT="$(printf '{"cwd":"%s","session_id":"x"}' "$REPO2" | bash "$NOTICE")"
[ -z "$OUT" ] && pass "no overlay -> silent (not an omakase repo)" || fail "fired in a non-omakase repo ($OUT)"

# ---------- Scenario S: /omakase show surfaces a 6-col ledger verdict on the guards chart ----------
# Since #23 `show` lists gates from the lefthook WIRING, joined to the latest ledger verdict
# (the old ledger-only "recent runs" table now only appears in the lefthook-unresolved
# fallback). So a 6-col row for the base payload's WIRED gate (omakase-example) must surface
# with its verdict in both modes. Asserts on gate-name + verdict, not the exact header, so it
# holds whether the chart or the fallback renders.
echo "== Scenario S: show surfaces a 6-col verdict on the guards chart =="
REPO="$TMP/repoS"; newrepo "$REPO"
( cd "$REPO" && OMAKASE_PAYLOAD="$PAY" bash "$INIT" ) >/dev/null 2>&1
LEDGER="$(ledger_of "$REPO")"; mkdir -p "$(dirname "$LEDGER")"
HEAD="$(cd "$REPO" && git rev-parse HEAD)"
printf '%s\tpre-commit\tomakase-example\tfail\t40\t%s\n' $((NOW-60)) "$HEAD" >> "$LEDGER"
OUT="$( cd "$REPO" && OMAKASE_NOW=$NOW bash "$SHOW" 2>&1 )"
echo "$OUT" | grep -q 'omakase-example' && pass "show lists the wired gate (6-col)" || fail "show missed 6-col gate"
echo "$OUT" | grep 'omakase-example' | grep -q 'fail' && pass "show shows a fail verdict on the gate row" || fail "show missing fail verdict on the gate row"
OUT="$( cd "$REPO" && OMAKASE_NOW=$NOW bash "$SHOW" --markdown 2>&1 )"
echo "$OUT" | grep -qE '^\| *-+ *\|' && pass "markdown table renders" || fail "no markdown table"
echo "$OUT" | grep -E 'omakase-example' | grep -q 'fail' && pass "markdown fail row (6-col)" || fail "no fail row in markdown"
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

# ---------- Scenario I: the inventory — every harness artifact, grouped by origin ----------
# spec §3: show gains an inventory (Committed / Injected / Personal), both modes,
# no token counts, audit view works even on an uninstalled repo.
echo "== Scenario I: show inventory groups harness artifacts by origin =="
REPO="$TMP/repoI"; newrepo "$REPO"
HOMEI="$TMP/homeI"; mkdir -p "$HOMEI/.claude/rules" "$HOMEI/.claude/skills/myskill"
printf 'global doctrine\n' > "$HOMEI/.claude/CLAUDE.md"
printf 'personal rule\n'   > "$HOMEI/.claude/rules/personal.md"
printf 'skill body\n'      > "$HOMEI/.claude/skills/myskill/SKILL.md"
mkdir -p "$REPO/.claude/rules" "$REPO/src"
printf 'team rule\n' > "$REPO/.claude/rules/team.md"
printf 'app\n'       > "$REPO/src/app.js"
( cd "$REPO" && git add .claude/rules/team.md src/app.js && git commit -qm files )

# not installed yet — the audit view still works
OUT="$( cd "$REPO" && HOME="$HOMEI" bash "$SHOW" 2>&1 )"
echo "$OUT" | grep -qi 'No omakase harness' && pass "not-installed message kept" || fail "not-installed message gone ($OUT)"
echo "$OUT" | grep -qiF 'Committed (this repo)' && pass "Committed group prints on an uninstalled repo" || fail "no Committed group when not installed"
echo "$OUT" | grep '\.claude/rules/team\.md' | grep -q 'rule' && pass "tracked harness file listed with kind rule" || fail "tracked rule missing or unkinded ($OUT)"
echo "$OUT" | grep -q 'src/app.js' && fail "non-harness tracked file leaked into the inventory" || pass "non-harness tracked file excluded"
echo "$OUT" | grep -qiF 'Personal (global)' && pass "Personal group prints on an uninstalled repo" || fail "no Personal group when not installed"
echo "$OUT" | grep 'rules/personal\.md' | grep -q 'rule' && pass "personal rule listed from \$HOME" || fail "personal rule missing ($OUT)"
echo "$OUT" | grep 'CLAUDE\.md' | grep -q 'doc' && pass "personal CLAUDE.md listed as doc" || fail "personal CLAUDE.md missing"
[ "$(echo "$OUT" | grep -c 'skills/myskill')" -eq 1 ] && pass "personal skill dir is ONE row (not its files)" || fail "skill dir rows != 1"
echo "$OUT" | grep 'skills/myskill' | grep -q 'skill' && pass "personal skill dir carries kind skill" || fail "skill dir unkinded"
OUT="$( cd "$REPO" && HOME="$HOMEI" bash "$SHOW" --markdown 2>&1 )"
{ echo "$OUT" | grep -qi 'No omakase harness' && echo "$OUT" | grep -qiF 'Committed (this repo)'; } \
  && pass "markdown not-installed keeps the message and the Committed group" || fail "markdown not-installed inventory wrong ($OUT)"

# installed — injected rows come from the provenance ledger with source + kind
( cd "$REPO" && OMAKASE_PAYLOAD="$PAY" bash "$INIT" ) >/dev/null 2>&1
PLACEDTSV="$(cd "$REPO" && cd "$(git rev-parse --git-common-dir)" && pwd)/omakase/placed.tsv"
awk -F'\t' -v OFS='\t' '$1==".claude/settings.json"{$5=0} 1' "$PLACEDTSV" > "$PLACEDTSV.tmp" && mv "$PLACEDTSV.tmp" "$PLACEDTSV"
OUT="$( cd "$REPO" && HOME="$HOMEI" bash "$SHOW" 2>&1 )"
echo "$OUT" | grep -qiF 'Injected (omakase)' && pass "Injected group prints when installed" || fail "no Injected group ($OUT)"
echo "$OUT" | grep 'lefthook-local\.yml' | grep 'gate' | grep -q 'payload' && pass "injected row carries kind + source value" || fail "injected row missing kind/source ($OUT)"
echo "$OUT" | grep '\.claude/settings\.json' | grep -qi 'disabled' && pass "hand-disabled row carries the disabled marker" || fail "disabled marker missing ($OUT)"
# omakase's own machinery (.omakase/) is not itemised in Injected; scope the absence check
# to that section (Guards may legitimately name an .omakase/ gate path in the ENFORCES cell).
INJ="$(echo "$OUT" | awk '/^INJECTED \(omakase\)/{f=1;next} /^PERSONAL \(global\)/{f=0} f')"
echo "$INJ" | grep -q '\.omakase/' && fail "engine files under .omakase/ leaked into the Injected list" || pass ".omakase/ engine files excluded from the Injected list"
echo "$OUT" | grep '\.claude/rules/team\.md' | grep -qi 'payload' && fail "committed file leaked into the Injected group" || pass "committed file stays out of Injected"
echo "$OUT" | grep -qi 'token' && fail "output mentions tokens (explicitly cut from the spec)" || pass "no token counts anywhere (terminal)"

# markdown mode carries the same three groups
OUT="$( cd "$REPO" && HOME="$HOMEI" bash "$SHOW" --markdown 2>&1 )"
echo "$OUT" | grep -qiF 'Committed (this repo)' && pass "markdown: Committed group" || fail "markdown missing Committed group"
echo "$OUT" | grep -qiF 'Injected (omakase)' && pass "markdown: Injected group" || fail "markdown missing Injected group"
echo "$OUT" | grep -qiF 'Personal (global)' && pass "markdown: Personal group" || fail "markdown missing Personal group"
echo "$OUT" | grep '\.claude/settings\.json' | grep -qi 'disabled' && pass "markdown: disabled marker carried" || fail "markdown lost the disabled marker"
INJ="$(echo "$OUT" | awk '/^### Injected/{f=1;next} /^### /{f=0} f')"
echo "$INJ" | grep -q '\.omakase/' && fail "markdown: engine files under .omakase/ leaked into the Injected list" || pass "markdown: .omakase/ engine files excluded from the Injected list"
echo "$OUT" | grep -qi 'token' && fail "markdown mentions tokens" || pass "no token counts anywhere (markdown)"

# an empty Personal group prints (none)
HOMEE="$TMP/homeEmpty"; mkdir -p "$HOMEE"
OUT="$( cd "$REPO" && HOME="$HOMEE" bash "$SHOW" 2>&1 )"
echo "$OUT" | grep -i -A1 'Personal (global)' | grep -q '(none)' && pass "empty Personal group shows (none)" || fail "empty Personal group not (none) ($OUT)"
( cd "$REPO" && OMAKASE_PAYLOAD="$PAY" bash "$REMOVE" ) >/dev/null 2>&1

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
