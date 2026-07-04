#!/usr/bin/env bash
# Proof for bin/menu.sh (PROTOTYPE) + the init.sh enabled-merge: files toggle off (deleted,
# not resurrected, not blocking) and back on (restored from snapshot); the OFF choice
# survives a re-init; a locally edited file is never destroyed by a disable; gates toggle
# by name via .git/omakase/disabled-gates and the primitive skips them VISIBLY.
set -u
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SRC="$(cd "$HERE/.." && pwd)"
TMP="${TMPDIR:-/tmp}/omakase-menu-test.$$"
PAY="$TMP/payload"; REPO="$TMP/repo"
FAILED=0
pass(){ echo "  PASS: $1"; }
fail(){ echo "  FAIL: $1"; FAILED=1; }
trap 'rm -rf "$TMP"' EXIT

mkdir -p "$PAY" "$REPO"
cp -R "$SRC/payload/". "$PAY/"
mkdir -p "$PAY/.claude/rules"
printf '# team rule\n' > "$PAY/.claude/rules/team.md"
git -C "$REPO" init -q && git -C "$REPO" commit -q --allow-empty -m init
( cd "$REPO" && OMAKASE_PAYLOAD="$PAY" bash "$SRC/bin/init.sh" >/dev/null 2>&1 ) || fail "init failed"
MENU="$SRC/bin/menu.sh"

echo "== list =="
LIST="$(cd "$REPO" && bash "$MENU" --list)" || fail "--list exited non-zero"
printf '%s\n' "$LIST" | grep -q "gate  markers" && pass "gate listed by name" || fail "gate not listed"
printf '%s\n' "$LIST" | grep -q ".claude/rules/team.md" && pass "placed file listed" || fail "placed file not listed"
printf '%s\n' "$LIST" | grep -q ".omakase/" && fail "machinery leaked into the file list" || pass "machinery hidden"

echo "== file off =="
( cd "$REPO" && bash "$MENU" --disable .claude/rules/team.md >/dev/null ) || fail "--disable failed"
[ ! -e "$REPO/.claude/rules/team.md" ] && pass "file removed from worktree" || fail "file still present"
awk -F'\t' '$1==".claude/rules/team.md"{print $5}' "$REPO/.git/omakase/placed.tsv" | grep -qx 0 && pass "ledger enabled=0" || fail "ledger not updated"
( cd "$REPO" && bash "$SRC/bin/verify-overlay.sh" ) && pass "verify-overlay does not block on a disabled file" || fail "verify-overlay blocked"
( cd "$REPO" && bash .git/omakase/ensure-present.sh >/dev/null 2>&1 )
[ ! -e "$REPO/.claude/rules/team.md" ] && pass "ensure-present does not resurrect" || fail "self-heal resurrected a disabled file"

echo "== off survives re-init (the enabled-merge) =="
( cd "$REPO" && OMAKASE_PAYLOAD="$PAY" bash "$SRC/bin/init.sh" >/dev/null 2>&1 ) || fail "re-init failed"
[ ! -e "$REPO/.claude/rules/team.md" ] && pass "re-init did not re-place a disabled file" || fail "re-init re-placed it"
awk -F'\t' '$1==".claude/rules/team.md"{print $5}' "$REPO/.git/omakase/placed.tsv" | grep -qx 0 && pass "re-init kept enabled=0" || fail "re-init reset enabled"
[ -e "$REPO/.git/omakase/payload-snapshot/.claude/rules/team.md" ] && pass "snapshot still carries the payload copy" || fail "snapshot lost the copy"

echo "== file on =="
( cd "$REPO" && bash "$MENU" --enable .claude/rules/team.md >/dev/null ) || fail "--enable failed"
[ -f "$REPO/.claude/rules/team.md" ] && pass "file restored from snapshot" || fail "file not restored"
awk -F'\t' '$1==".claude/rules/team.md"{print $5}' "$REPO/.git/omakase/placed.tsv" | grep -qx 1 && pass "ledger enabled=1" || fail "ledger not re-enabled"

echo "== local edit is never destroyed =="
echo tampered >> "$REPO/.claude/rules/team.md"
( cd "$REPO" && bash "$MENU" --disable .claude/rules/team.md >/dev/null 2>&1 ) && fail "disable succeeded on a drifted file" || pass "disable refused on a drifted file"
grep -q tampered "$REPO/.claude/rules/team.md" && pass "edited file kept" || fail "edited file lost"
( cd "$REPO" && OMAKASE_PAYLOAD="$PAY" bash "$SRC/bin/init.sh" >/dev/null 2>&1 )   # restore canonical

echo "== gate off/on (primitive skips visibly) =="
( cd "$REPO" && bash "$MENU" --disable-gate markers >/dev/null ) || fail "--disable-gate failed"
OUT="$(cd "$REPO" && bash .omakase/bin/omakase-gate.sh markers --step 'exit 1')"; rc=$?
[ "$rc" -eq 0 ] && pass "disabled gate skips (exit 0)" || fail "disabled gate still ran (exit $rc)"
printf '%s\n' "$OUT" | grep -q "disabled via omakase menu" && pass "skip is visible, not silent" || fail "skip was silent"
( cd "$REPO" && bash "$MENU" --enable-gate markers >/dev/null ) || fail "--enable-gate failed"
( cd "$REPO" && bash .omakase/bin/omakase-gate.sh markers --step 'exit 1' >/dev/null ); rc=$?
[ "$rc" -eq 1 ] && pass "re-enabled gate runs (exit code passes through)" || fail "re-enabled gate did not run"

echo "== guardrails =="
( cd "$REPO" && bash "$MENU" --disable .omakase/bin/omakase-gate.sh >/dev/null 2>&1 ) && fail "machinery was disable-able" || pass "machinery refuses to disable"
( cd "$REPO" && bash "$MENU" --disable-gate nonexistent >/dev/null 2>&1 ) && fail "unknown gate accepted" || pass "unknown gate rejected"

exit "$FAILED"
