#!/usr/bin/env bash
# Proof of the provenance store placed.tsv (spec §2 + safety fix 5): init writes
# .git/omakase/placed.tsv, one row per placed artifact, TAB-separated columns
#   path  kind  source  sha256  enabled
# and every consumer (ensure-present, verify-overlay, remove, show, the
# upstream-collision guard) reads placed.tsv instead of the old placed.list.
# (This is the provenance store, NOT ledger.tsv, which is the gate run + cache store.)
#   M. placed.tsv written with correct columns / kinds / hashes; placed.list gone
#   N. symlink row: hash is the link TARGET STRING; round-trips (CLAUDE.md case)
#   O. space-in-path row round-trips (restore, verify, remove)
#   P. enabled=0 honored - fix 5: not restored, not blocking, still removed
#   Q. upstream-collision warning keys off placed.tsv paths
#   T. pre-0.10 migration - placed.list regenerates into placed.tsv and is deleted
#   U. pre-0.10 remove - no placed.tsv: payload-enumeration fallback tears down
set -u
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
INIT="$HERE/../bin/init.sh"
REMOVE="$HERE/../bin/remove.sh"
SHOW="$HERE/../bin/show.sh"
LEFTHOOK="${LEFTHOOK_BIN:-$(command -v lefthook || true)}"
TMP="${TMPDIR:-/tmp}/omakase-ledger-test.$$"
FAILED=0
pass(){ echo "  PASS: $1"; }
fail(){ echo "  FAIL: $1"; FAILED=1; }

# Same digest detection the implementation uses (shasum on macOS, sha256sum on Linux).
if command -v shasum >/dev/null 2>&1; then sha_file(){ shasum -a 256 < "$1" | awk '{print $1}'; }; sha_str(){ printf '%s' "$1" | shasum -a 256 | awk '{print $1}'; }
else sha_file(){ sha256sum < "$1" | awk '{print $1}'; }; sha_str(){ printf '%s' "$1" | sha256sum | awk '{print $1}'; }; fi

# A payload exercising every kind: gate (wiring + script), rule (incl. one with a
# space in the path), skill, command, doc (root .md + AGENTS.md + CLAUDE.md symlink),
# config, and an "other" helper script.
mkpayload(){ # $1 = payload dir
  local p="$1"
  mkdir -p "$p/.omakase/gates" "$p/.omakase/bin" "$p/.claude/rules" "$p/.claude/skills/demo" "$p/.claude/commands"
  cat > "$p/.omakase/gates/example.sh" <<'SH'
#!/usr/bin/env bash
echo "omakase-example-gate-ran"
exit 0
SH
  cat > "$p/lefthook-local.yml" <<'YML'
pre-commit:
  jobs:
    - name: omakase-example
      run: bash .omakase/gates/example.sh
post-checkout:
  jobs:
    - name: omakase-ensure-present
      run: bash "$(git rev-parse --git-common-dir)/omakase/ensure-present.sh"
YML
  printf 'a rule\n' > "$p/.claude/rules/style.md"
  printf 'spaced rule\n' > "$p/.claude/rules/my rule.md"
  printf 'a skill\n' > "$p/.claude/skills/demo/SKILL.md"
  printf 'a command\n' > "$p/.claude/commands/go.md"
  printf 'doctrine\n' > "$p/AGENTS.md"
  ( cd "$p" && ln -s AGENTS.md CLAUDE.md )
  printf '{ "hooks": {} }\n' > "$p/.claude/settings.json"
  printf 'notes\n' > "$p/NOTES.md"
  printf '#!/usr/bin/env bash\ntrue\n' > "$p/.omakase/bin/helper.sh"
}

newrepo(){ rm -rf "$1"; mkdir -p "$1"; ( cd "$1" && git init -q && git config user.email t@t && git config user.name t && git config commit.gpgsign false && git commit -q --allow-empty -m init ); }
common_of(){ echo "$(cd "$1" && cd "$(git rev-parse --git-common-dir)" && pwd)"; }
col(){ awk -F'\t' -v p="$2" -v c="$3" '$1==p{print $c; exit}' "$1"; }   # $1=ledger $2=path $3=column

export PATH="$(dirname "$LEFTHOOK"):$PATH"

# ---------- Scenario M: ledger columns, kinds, hashes ----------
echo "== Scenario M: init writes the provenance ledger (placed.tsv) =="
PAY="$TMP/payM"; REPO="$TMP/repoM"
mkpayload "$PAY"; newrepo "$REPO"
( cd "$REPO" && OMAKASE_PAYLOAD="$PAY" bash "$INIT" ) >/dev/null 2>&1
COMMON="$(common_of "$REPO")"; LEDGER="$COMMON/omakase/placed.tsv"

[ -f "$LEDGER" ] && pass "placed.tsv written" || fail "placed.tsv missing"
[ ! -e "$COMMON/omakase/placed.list" ] && pass "old placed.list NOT written" || fail "placed.list still written"
awk -F'\t' 'NF!=5{bad=1} END{exit bad?1:0}' "$LEDGER" && pass "every row has exactly 5 tab-separated fields" || fail "row with wrong field count"
n_rows=$(grep -c . "$LEDGER"); n_payload=$(find "$PAY" \( -type f -o -type l \) | wc -l | tr -d ' ')
[ "$n_rows" -eq "$n_payload" ] && pass "one row per placed artifact ($n_rows)" || fail "row count $n_rows != payload file count $n_payload"

[ "$(col "$LEDGER" .omakase/gates/example.sh 2)" = gate ]    && pass "kind: gate script -> gate" || fail "gate script kind wrong"
[ "$(col "$LEDGER" lefthook-local.yml 2)" = gate ]           && pass "kind: lefthook-local.yml -> gate" || fail "lefthook-local.yml kind wrong"
[ "$(col "$LEDGER" .claude/rules/style.md 2)" = rule ]       && pass "kind: .claude/rules -> rule" || fail "rule kind wrong"
[ "$(col "$LEDGER" .claude/skills/demo/SKILL.md 2)" = skill ] && pass "kind: .claude/skills -> skill" || fail "skill kind wrong"
[ "$(col "$LEDGER" .claude/commands/go.md 2)" = command ]    && pass "kind: .claude/commands -> command" || fail "command kind wrong"
[ "$(col "$LEDGER" AGENTS.md 2)" = doc ]                     && pass "kind: AGENTS.md -> doc" || fail "AGENTS.md kind wrong"
[ "$(col "$LEDGER" NOTES.md 2)" = doc ]                      && pass "kind: root *.md -> doc" || fail "root .md kind wrong"
[ "$(col "$LEDGER" .claude/settings.json 2)" = config ]      && pass "kind: settings -> config" || fail "settings kind wrong"
[ "$(col "$LEDGER" .omakase/bin/helper.sh 2)" = other ]      && pass "kind: unclassified helper -> other" || fail "helper kind wrong"

awk -F'\t' '$3!="payload"{bad=1} END{exit bad?1:0}' "$LEDGER" && pass "source column is 'payload' on every row" || fail "non-payload source value"
awk -F'\t' '$5!="1"{bad=1} END{exit bad?1:0}' "$LEDGER" && pass "enabled column is 1 on every row" || fail "row not enabled=1 at placement"
[ "$(col "$LEDGER" .claude/rules/style.md 4)" = "$(sha_file "$REPO/.claude/rules/style.md")" ] && pass "sha256 matches the placed file content" || fail "sha256 mismatch"

grep -q 'placed\.list' "$COMMON/omakase/ensure-present.sh" "$COMMON/omakase/verify-overlay.sh" "$COMMON/omakase/install-guards.sh" 2>/dev/null \
  && fail "a generated script still references placed.list" || pass "generated scripts carry no placed.list reference"

# ---------- Scenario N: symlink row (CLAUDE.md -> AGENTS.md) ----------
echo "== Scenario N: symlink row hashes the link target string and round-trips =="
[ "$(col "$LEDGER" CLAUDE.md 2)" = doc ] && pass "kind: CLAUDE.md symlink -> doc" || fail "CLAUDE.md kind wrong"
[ "$(col "$LEDGER" CLAUDE.md 4)" = "$(sha_str AGENTS.md)" ] && pass "symlink hash = sha256 of the target path string" || fail "symlink hash is not the target-string hash"
rm -f "$REPO/CLAUDE.md"
( cd "$REPO" && bash "$COMMON/omakase/ensure-present.sh" )
{ [ -L "$REPO/CLAUDE.md" ] && [ "$(readlink "$REPO/CLAUDE.md")" = AGENTS.md ]; } && pass "ensure-present restored the symlink AS a symlink" || fail "symlink did not round-trip"

# ---------- Scenario O: space-in-path row round-trips ----------
echo "== Scenario O: a path containing a space survives every consumer =="
nf=$(awk -F'\t' '$1==".claude/rules/my rule.md"{print NF; exit}' "$LEDGER")
[ "${nf:-0}" -eq 5 ] && pass "space-in-path row intact (5 fields)" || fail "space-in-path row missing/split ($nf fields)"
rm -f "$REPO/.claude/rules/my rule.md"
ERR=$( cd "$REPO" && sh "$COMMON/omakase/verify-overlay.sh" 2>&1 ); rc=$?
{ [ "$rc" -ne 0 ] && echo "$ERR" | grep -q 'my rule.md'; } && pass "verify-overlay blocks on the missing spaced path, names it" || fail "verify-overlay missed the spaced path ($ERR)"
( cd "$REPO" && bash "$COMMON/omakase/ensure-present.sh" )
grep -q 'spaced rule' "$REPO/.claude/rules/my rule.md" 2>/dev/null && pass "ensure-present restored the spaced path with content intact" || fail "spaced path not restored"
( cd "$REPO" && sh "$COMMON/omakase/verify-overlay.sh" ) >/dev/null 2>&1 && pass "verify-overlay passes after restore" || fail "verify-overlay still blocking"
( cd "$REPO" && OMAKASE_PAYLOAD="$PAY" bash "$REMOVE" ) >/dev/null 2>&1
[ ! -e "$REPO/.claude/rules/my rule.md" ] && pass "remove deleted the spaced path" || fail "remove left the spaced path"
[ ! -e "$REPO/.omakase" ] && pass "remove deleted the placed tree (ledger-driven)" || fail "remove left placed files"

# ---------- Scenario P: enabled=0 honored (safety fix 5) ----------
echo "== Scenario P: a disabled artifact is not 'missing' — fix 5 =="
PAY="$TMP/payP"; REPO="$TMP/repoP"
mkpayload "$PAY"; newrepo "$REPO"
( cd "$REPO" && OMAKASE_PAYLOAD="$PAY" bash "$INIT" ) >/dev/null 2>&1
COMMON="$(common_of "$REPO")"; LEDGER="$COMMON/omakase/placed.tsv"
# hand-disable the rule (and delete it: the user switched it off) + the skill (still on disk)
awk -F'\t' -v OFS='\t' '$1==".claude/rules/style.md" || $1==".claude/skills/demo/SKILL.md" {$5=0} 1' "$LEDGER" > "$LEDGER.tmp" && mv "$LEDGER.tmp" "$LEDGER"
rm -f "$REPO/.claude/rules/style.md"

( cd "$REPO" && bash "$COMMON/omakase/ensure-present.sh" )
[ ! -e "$REPO/.claude/rules/style.md" ] && pass "ensure-present does NOT resurrect a disabled artifact" || fail "disabled artifact was restored"
( cd "$REPO" && sh "$COMMON/omakase/verify-overlay.sh" ) >/dev/null 2>&1 && pass "verify-overlay does NOT block on a disabled missing artifact" || fail "disabled artifact blocked the gate check"
OUT=$( cd "$REPO" && echo p > p.txt && git add p.txt && git commit -m p 2>&1 ); rc=$?
{ [ "$rc" -eq 0 ] && echo "$OUT" | grep -q 'omakase-example-gate-ran'; } && pass "real commit passes with a disabled artifact missing (gate still fires)" || fail "commit blocked or gate dead ($OUT)"
# enabled=1 paths still fail closed alongside the disabled one
rm -f "$REPO/.claude/commands/go.md"
( cd "$REPO" && sh "$COMMON/omakase/verify-overlay.sh" ) >/dev/null 2>&1 && fail "verify-overlay ignored an enabled missing artifact" || pass "enabled missing artifact still blocks (disabled row did not mask it)"
( cd "$REPO" && bash "$COMMON/omakase/ensure-present.sh" )
[ -f "$REPO/.claude/commands/go.md" ] && pass "ensure-present still restores enabled artifacts" || fail "enabled artifact not restored"
OUT=$( cd "$REPO" && bash "$SHOW" 2>&1 )
echo "$OUT" | grep '.claude/rules/style.md' | grep -qi 'disabled' && pass "show marks the disabled row as disabled" || fail "show does not surface the disabled state ($OUT)"
echo "$OUT" | grep '.claude/rules/style.md' | grep -qi 'MISSING' && fail "show calls a disabled artifact MISSING" || pass "show does not call a disabled artifact MISSING"
( cd "$REPO" && OMAKASE_PAYLOAD="$PAY" bash "$REMOVE" ) >/dev/null 2>&1
[ ! -e "$REPO/.claude/skills/demo/SKILL.md" ] && pass "remove deletes a DISABLED artifact still on disk" || fail "remove skipped a disabled artifact"
[ ! -e "$COMMON/omakase" ] && pass "remove deleted the ledger with the snapshot" || fail "remove left the ledger"

# ---------- Scenario Q: upstream-collision warning keys off the ledger ----------
echo "== Scenario Q: collision warning fires from ledger data =="
PAY="$TMP/payQ"; REPO="$TMP/repoQ"
mkpayload "$PAY"; newrepo "$REPO"
( cd "$REPO" && OMAKASE_PAYLOAD="$PAY" bash "$INIT" ) >/dev/null 2>&1
COMMON="$(common_of "$REPO")"
[ ! -e "$COMMON/omakase/placed.list" ] && pass "no placed.list exists (ledger is the only record)" || fail "placed.list present"
( cd "$REPO" && printf 'UPSTREAM CONTENT\n' > .omakase/gates/example.sh && git add -f .omakase/gates/example.sh && LEFTHOOK=0 git commit -q -m upstream )
OUT=$( cd "$REPO" && OMAKASE_PAYLOAD="$PAY" bash "$INIT" 2>&1 ); rc=$?
[ "$rc" -eq 0 ] && pass "re-init completes (warn, not block)" || fail "re-init failed ($OUT)"
{ echo "$OUT" | grep -qi 'WARNING' && echo "$OUT" | grep -q '.omakase/gates/example.sh'; } && pass "collision warning fired off the ledger, names the path" || fail "no ledger-keyed collision warning ($OUT)"
grep -q 'omakase-example-gate-ran' "$COMMON/omakase/clobbered/.omakase/gates/example.sh" 2>/dev/null && pass "last-injected copy preserved under clobbered/" || fail "preserved copy missing"

# ---------- Scenario T: pre-0.10 placed.list migrates ----------
echo "== Scenario T: stale placed.list regenerates into the ledger and is deleted =="
PAY="$TMP/payT"; REPO="$TMP/repoT"
mkpayload "$PAY"; newrepo "$REPO"
( cd "$REPO" && OMAKASE_PAYLOAD="$PAY" bash "$INIT" ) >/dev/null 2>&1
COMMON="$(common_of "$REPO")"
# simulate the pre-0.10 on-disk state: a path-only placed.list, no placed.tsv
cut -f1 "$COMMON/omakase/placed.tsv" > "$COMMON/omakase/placed.list"
rm -f "$COMMON/omakase/placed.tsv"
# show must NOT report "not installed" against this state (the harness IS running)
OUT=$( cd "$REPO" && bash "$SHOW" 2>&1 )
echo "$OUT" | grep -qi 'No omakase harness' && fail "show false-negatives on a pre-0.10 install" || pass "show does not report 'not installed' on a pre-0.10 install"
{ echo "$OUT" | grep -qi 'pre-0.10' && echo "$OUT" | grep -q 'AGENTS.md'; } && pass "show names the pre-0.10 state and lists placed files" || fail "show pre-0.10 notice wrong ($OUT)"
OUT=$( cd "$REPO" && bash "$SHOW" --markdown 2>&1 )
echo "$OUT" | grep -qi 'pre-0.10' && pass "markdown mode carries the pre-0.10 notice" || fail "markdown mode missing pre-0.10 notice"
# an upstream collision arriving exactly across the upgrade must still warn
( cd "$REPO" && printf 'UPSTREAM CONTENT\n' > .claude/rules/style.md && git add -f .claude/rules/style.md && LEFTHOOK=0 git commit -q -m upstream )
OUT=$( cd "$REPO" && OMAKASE_PAYLOAD="$PAY" bash "$INIT" 2>&1 ); rc=$?
[ "$rc" -eq 0 ] && pass "init over a pre-0.10 record completes" || fail "upgrade init failed ($OUT)"
{ echo "$OUT" | grep -qi 'WARNING' && echo "$OUT" | grep -q '.claude/rules/style.md'; } && pass "collision warning still fires from the stale placed.list (one-time fallback)" || fail "upgrade run lost the collision warning ($OUT)"
[ -f "$COMMON/omakase/placed.tsv" ] && pass "ledger regenerated" || fail "no ledger after upgrade init"
[ ! -e "$COMMON/omakase/placed.list" ] && pass "stale placed.list deleted" || fail "placed.list left behind"

# ---------- Scenario U: pre-0.10 remove (no ledger) falls back to payload enumeration ----------
echo "== Scenario U: remove without a ledger tears down via the payload fallback =="
PAY="$TMP/payU"; REPO="$TMP/repoU"
mkpayload "$PAY"; newrepo "$REPO"
( cd "$REPO" && OMAKASE_PAYLOAD="$PAY" bash "$INIT" ) >/dev/null 2>&1
COMMON="$(common_of "$REPO")"
# simulate the pre-0.10 on-disk state: a path-only placed.list, no placed.tsv
cut -f1 "$COMMON/omakase/placed.tsv" > "$COMMON/omakase/placed.list"
rm -f "$COMMON/omakase/placed.tsv"
( cd "$REPO" && OMAKASE_PAYLOAD="$PAY" bash "$REMOVE" ) >/dev/null 2>&1
[ ! -e "$REPO/.omakase" ] && pass "fallback remove deleted the placed tree" || fail "fallback remove left placed files"
[ ! -e "$REPO/.claude/rules/my rule.md" ] && pass "fallback remove deleted the spaced path" || fail "fallback remove left the spaced path"
[ ! -e "$REPO/CLAUDE.md" ] && pass "fallback remove deleted the placed symlink" || fail "fallback remove left the symlink"
[ ! -d "$REPO/.claude" ] && pass "fallback remove pruned emptied directories" || fail "fallback remove left empty dirs"
[ ! -e "$COMMON/omakase" ] && pass "fallback remove tore down the snapshot (stale placed.list included)" || fail "fallback remove left the snapshot"
grep -q "omakase-harness" "$COMMON/info/exclude" 2>/dev/null && fail "fallback remove left the exclude block" || pass "fallback remove stripped the exclude block"

rm -rf "$TMP"
echo ""
[ "$FAILED" -eq 0 ] && echo "ALL PASS" || { echo "FAILURES PRESENT"; exit 1; }
