#!/usr/bin/env bash
# tests/toggles.test.sh — e2e for `omakase status --plain | --disable <name> | --enable
# <name>`: the scriptable twin of the interactive screen's per-item Enter (an agent
# cannot drive a TUI). Proves the whole chain end to end against a REAL installed
# harness: a wired gate that blocks a real `git commit`, toggled off via the CLI
# (the commit then succeeds, and the gate itself says so), toggled back on (blocks
# again); a real placed FILE toggled off (deleted on disk, enabled=0 in placed.tsv,
# STAYS gone across a re-init per Task 3's consent merge) and back on (restored,
# enabled=1); the REFUSING guards (a tracked path, a locally-edited path) leave the
# file untouched; and --plain still reaches the normal (non-interactive) render.
set -u
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
INIT="$HERE/../bin/init.sh"
SHOW="$HERE/../bin/status.sh"
LEFTHOOK="${LEFTHOOK_BIN:-$(command -v lefthook || true)}"
TMP="${TMPDIR:-/tmp}/omakase-toggles-test.$$"
FAILED=0
pass(){ echo "  PASS: $1"; }
fail(){ echo "  FAIL: $1"; FAILED=1; }
newrepo(){ rm -rf "$1"; mkdir -p "$1"; ( cd "$1" && git init -q && git config user.email t@t && git config user.name t && git config commit.gpgsign false && git commit -q --allow-empty -m init ); }
omk_of(){ echo "$(cd "$1" && cd "$(git rev-parse --git-common-dir)" && pwd)/omakase"; }
export PATH="$(dirname "$LEFTHOOK"):$PATH"
HAVE_LH=0; { [ -n "$LEFTHOOK" ] && [ -x "$LEFTHOOK" ]; } && HAVE_LH=1

if [ "$HAVE_LH" -eq 0 ]; then
  echo "SKIP: toggles e2e needs a real lefthook install to prove a commit actually blocks/allows; LEFTHOOK_BIN unset and lefthook not on PATH"
  echo ""
  echo "ALL PASS"
  exit 0
fi

mkdir -p "$TMP"

# ---- fixture payload: one wired gate ("smoke", always exits 9) + one file (AGENTS.md) ----
FIX="$TMP/payload"
mkdir -p "$FIX/.omakase/bin" "$FIX/.omakase/gates"
cp "$HERE/../payload/.omakase/bin/omakase-gate.sh" "$FIX/.omakase/bin/omakase-gate.sh"
cat > "$FIX/.omakase/gates/smoke.sh" <<'SH'
#!/usr/bin/env bash
# Fixture gate body (unused directly by the wired job below, which inlines its
# own --step; shipped so the payload carries a realistic .omakase/gates/ entry).
exit 0
SH
chmod +x "$FIX/.omakase/gates/smoke.sh"
printf 'Fixture agent doctrine.\n' > "$FIX/AGENTS.md"
mkdir -p "$FIX/.claude/rules"
printf 'Fixture style rule.\n' > "$FIX/.claude/rules/style.md"   # non-machinery placed file for the tracked-path test
cat > "$FIX/lefthook-local.yml" <<'YML'
pre-commit:
  jobs:
    - name: smoke
      run: bash .omakase/bin/omakase-gate.sh smoke --step 'exit 9'
YML

echo "== toggles: gate lifecycle (--disable/--enable a wired gate) =="
REPO="$TMP/repo"; newrepo "$REPO"
( cd "$REPO" && OMAKASE_PAYLOAD="$FIX" bash "$INIT" ) >/dev/null 2>&1 || fail "install failed"
OMK="$(omk_of "$REPO")"

# 1. a real commit is blocked by the wired gate (it always exits 9)
OUT="$( cd "$REPO" && echo one >> f.txt && git add f.txt && git commit -m c1 2>&1 )"; RC=$?
[ "$RC" -ne 0 ] && pass "case1: the wired gate blocks a real commit" || fail "case1: commit was not blocked ($RC: $OUT)"

# 2. --disable smoke -> rc 0, names the gate; the SAME commit now succeeds and the
#    gate itself says it was skipped via omakase
OUT="$( cd "$REPO" && "$SHOW" --disable smoke 2>&1 )"; RC=$?
{ [ "$RC" -eq 0 ] && echo "$OUT" | grep -q "gate 'smoke' off"; } \
  && pass "case2: --disable smoke exits 0 and names the gate" \
  || fail "case2: --disable smoke ($RC: $OUT)"
OUT="$( cd "$REPO" && git commit -m c1 2>&1 )"; RC=$?
[ "$RC" -eq 0 ] && pass "case2: the same commit now succeeds" || fail "case2: commit still blocked ($RC: $OUT)"
echo "$OUT" | grep -q "disabled via omakase" \
  && pass "case2: the commit's own output names the disable" \
  || fail "case2: commit output missing 'disabled via omakase' ($OUT)"

# 3. --enable smoke -> the next commit blocks again
OUT="$( cd "$REPO" && "$SHOW" --enable smoke 2>&1 )"; RC=$?
{ [ "$RC" -eq 0 ] && echo "$OUT" | grep -q "gate 'smoke' back on"; } \
  && pass "case3: --enable smoke exits 0 and names the gate" \
  || fail "case3: --enable smoke ($RC: $OUT)"
OUT="$( cd "$REPO" && echo two >> f.txt && git add f.txt && git commit -m c2 2>&1 )"; RC=$?
[ "$RC" -ne 0 ] && pass "case3: the gate blocks again once re-enabled" || fail "case3: commit not blocked ($RC: $OUT)"

echo "== toggles: file lifecycle (--disable/--enable a placed file, survives re-init) =="
col5(){ awk -F'\t' -v n="$1" '$1==n{print $5}' "$OMK/placed.tsv"; }

# 4. --disable AGENTS.md -> the file is deleted, placed.tsv records enabled=0; a
#    re-init leaves it gone and still 0 (Task 3's consent merge).
OUT="$( cd "$REPO" && "$SHOW" --disable AGENTS.md 2>&1 )"; RC=$?
[ "$RC" -eq 0 ] && pass "case4: --disable AGENTS.md exits 0" || fail "case4: --disable AGENTS.md ($RC: $OUT)"
[ ! -e "$REPO/AGENTS.md" ] && pass "case4: AGENTS.md removed from disk" || fail "case4: AGENTS.md still present"
[ "$(col5 AGENTS.md)" = "0" ] && pass "case4: placed.tsv column 5 is 0" || fail "case4: placed.tsv col5 = '$(col5 AGENTS.md)'"
( cd "$REPO" && OMAKASE_PAYLOAD="$FIX" bash "$INIT" ) >/dev/null 2>&1
[ ! -e "$REPO/AGENTS.md" ] && pass "case4: still gone after a re-init" || fail "case4: re-init resurrected AGENTS.md"
[ "$(col5 AGENTS.md)" = "0" ] && pass "case4: still 0 after a re-init" || fail "case4: post-reinit col5 = '$(col5 AGENTS.md)'"

# 5. --enable AGENTS.md -> the file comes back, enabled=1
OUT="$( cd "$REPO" && "$SHOW" --enable AGENTS.md 2>&1 )"; RC=$?
[ "$RC" -eq 0 ] && pass "case5: --enable AGENTS.md exits 0" || fail "case5: --enable AGENTS.md ($RC: $OUT)"
[ -f "$REPO/AGENTS.md" ] && pass "case5: AGENTS.md restored to disk" || fail "case5: AGENTS.md missing after --enable"
[ "$(col5 AGENTS.md)" = "1" ] && pass "case5: placed.tsv column 5 is back to 1" || fail "case5: placed.tsv col5 = '$(col5 AGENTS.md)'"

echo "== toggles: REFUSING guards (tracked path, local edit) =="
# 6. --disable on a NON-MACHINERY path the repo TRACKS -> rc 1, REFUSING, file
#    survives untouched. Uses .claude/rules/style.md (a placed rule file);
#    AGENTS.md is kept untracked for case 7's edit test, and .omakase/* is now
#    machinery (case 9, exit 2), so a plain placed file is what proves the
#    tracked-path guard. `git ls-files` (what gitTracked checks) counts a STAGED
#    path as tracked, so a plain `git add -f` is enough — no commit needed.
( cd "$REPO" && git add -f .claude/rules/style.md ) >/dev/null 2>&1
OUT="$( cd "$REPO" && "$SHOW" --disable .claude/rules/style.md 2>&1 )"; RC=$?
{ [ "$RC" -eq 1 ] && echo "$OUT" | grep -q REFUSING; } \
  && pass "case6: --disable refuses a tracked path" \
  || fail "case6: tracked-path refusal ($RC: $OUT)"
[ -f "$REPO/.claude/rules/style.md" ] && pass "case6: the tracked file survives" || fail "case6: tracked file was deleted"

# 7. a LOCAL EDIT to an untracked placed file -> rc 1, REFUSING, file survives.
OUT="$( cd "$REPO" && echo extra >> AGENTS.md && "$SHOW" --disable AGENTS.md 2>&1 )"; RC=$?
{ [ "$RC" -eq 1 ] && echo "$OUT" | grep -q REFUSING; } \
  && pass "case7: --disable refuses a locally-edited file" \
  || fail "case7: edited-file refusal ($RC: $OUT)"
[ -f "$REPO/AGENTS.md" ] && pass "case7: the edited file survives" || fail "case7: edited file was deleted"

echo "== toggles: machinery + unknown names are refused (exit 2) =="
# 9. --disable .omakase (harness machinery) -> exit 2, deletes nothing, no raw
#    bash-127 commit wedge. The gate primitive must survive.
OUT="$( cd "$REPO" && "$SHOW" --disable .omakase 2>&1 )"; RC=$?
{ [ "$RC" -eq 2 ] && echo "$OUT" | grep -q machinery; } \
  && pass "case9: --disable .omakase refuses machinery (exit 2)" \
  || fail "case9: machinery refusal ($RC: $OUT)"
[ -f "$REPO/.omakase/bin/omakase-gate.sh" ] && pass "case9: the gate primitive survives" || fail "case9: gate primitive deleted"

# 10. --disable CLAUDE.mdd (typo: no placed path, no wired gate) -> exit 2, and
#     the junk name never reaches disabled-gates as a phantom entry.
OUT="$( cd "$REPO" && "$SHOW" --disable CLAUDE.mdd 2>&1 )"; RC=$?
{ [ "$RC" -eq 2 ] && echo "$OUT" | grep -q "unknown gate or placed path"; } \
  && pass "case10: --disable of an unknown name exits 2" \
  || fail "case10: unknown-name refusal ($RC: $OUT)"
if grep -q "CLAUDE.mdd" "$OMK/disabled-gates" 2>/dev/null; then
  fail "case10: junk name leaked into disabled-gates"
else
  pass "case10: no phantom disabled-gates entry"
fi

# 12. A typo'd FLAG (not target) errors — it must never exit 0 with the page
#     (an automation reading exit 0 as "toggled, green" is the hazard).
OUT="$( cd "$REPO" && "$SHOW" --enabel smoke 2>&1 )"; RC=$?
{ [ "$RC" -eq 2 ] && echo "$OUT" | grep -q "unknown flag --enabel"; } \
  && pass "case12: unknown flag exits 2, no page" \
  || fail "case12: unknown flag ($RC: $OUT)"

# 13. Machinery refuses both toggle directions (it keeps the harness running).
OUT="$( cd "$REPO" && "$SHOW" --disable .omakase 2>&1 )"; RC=$?
{ [ "$RC" -eq 2 ] && echo "$OUT" | grep -q "machinery"; } \
  && pass "case13: machinery --disable refused" \
  || fail "case13: machinery refusal ($RC: $OUT)"

# 11. --help prints usage and exits 0 (never the page, never the TUI).
OUT="$( cd "$REPO" && "$SHOW" --help 2>&1 )"; RC=$?
{ [ "$RC" -eq 0 ] && echo "$OUT" | grep -q "usage: omakase status"; } \
  && pass "case11: --help prints usage and exits 0" \
  || fail "case11: --help ($RC: $OUT)"

echo "== toggles: --plain still reaches the normal render =="
# 8. --plain is a reachable no-op today (Task 8 wires the interactive dispatch); the
#    normal identity line still renders as the first line of output.
OUT="$( cd "$REPO" && "$SHOW" --plain 2>&1 | head -1 )"
echo "$OUT" | grep -q "installed in" \
  && pass "case8: --plain still prints the identity line" \
  || fail "case8: --plain first line unexpected: '$OUT'"

rm -rf "$TMP"
echo ""
[ "$FAILED" -eq 0 ] && echo "ALL PASS" || { echo "FAILURES PRESENT"; exit 1; }
