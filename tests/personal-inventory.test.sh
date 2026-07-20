#!/usr/bin/env bash
# status.sh's "Personal (global)" inventory must list the user's global harness for BOTH hosts:
# Claude (~/.claude) AND Copilot CLI (~/.copilot/skills), each row qualified by origin AND
# carrying its kind. Drives status.sh with an isolated $HOME so it never touches the real one.
# set -u (not -e): we deliberately capture status.sh's exit status to assert it.
set -u
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SHOW="$HERE/../bin/status.sh"
TMP="${TMPDIR:-/tmp}/omakase-personal.$$"
FAILED=0
pass(){ echo "  PASS: $1"; }
fail(){ echo "  FAIL: $1"; FAILED=1; }
newrepo(){ rm -rf "$1"; mkdir -p "$1"; ( cd "$1" && git init -q && git config user.email t@t && git config user.name t ); }
mkskill(){ mkdir -p "$1"; printf -- '---\nname: %s\n---\n' "$(basename "$1")" > "$1/SKILL.md"; }

REPO="$TMP/repo"; newrepo "$REPO"

# --- both hosts present: the PAGE carries one count line; the LIST lives behind --global
# (#131 gripe 4), each row qualified + kinded ---
H="$TMP/home-both"
mkskill "$H/.claude/skills/claude-skill"
mkskill "$H/.copilot/skills/copilot-skill"
PAGE="$( cd "$REPO" && HOME="$H" bash "$SHOW" --markdown 2>&1 )"; rcp=$?
[ "$rcp" -eq 0 ] && pass "page exits clean with both hosts" || fail "status.sh non-zero exit ($rcp): $PAGE"
printf '%s\n' "$PAGE" | grep -qF '2 files in ~/.claude + ~/.copilot steer every repo' && pass "page collapses Global to a count line" || fail "page Global count line missing ($PAGE)"
printf '%s\n' "$PAGE" | grep -q 'skills/claude-skill' && fail "page still enumerates global rows" || pass "page no longer enumerates global rows"
OUT="$( cd "$REPO" && HOME="$H" bash "$SHOW" --markdown --global 2>&1 )"; rc=$?
[ "$rc" -eq 0 ] && pass "--global exits clean with both hosts" || fail "status.sh --global non-zero exit ($rc): $OUT"
printf '%s\n' "$OUT" | grep -Eq '~/\.claude/skills/claude-skill/.*skill'   && pass "global Claude skill listed + kinded"  || fail "Claude personal skill missing/unkinded"
printf '%s\n' "$OUT" | grep -Eq '~/\.copilot/skills/copilot-skill/.*skill' && pass "global Copilot skill listed + kinded" || fail "Copilot personal skill missing/unkinded"
printf '%s\n' "$OUT" | grep -qF 'not installed by omakase' && pass "global section labeled not-installed-by-omakase" || fail "global section not relabeled"
printf '%s\n' "$OUT" | grep -qF 'Personal (~/.claude)' && fail "stale 'Personal (~/.claude)' label reappeared" || pass "no stale ~/.claude-only label"

# --- Copilot-only HOME (no ~/.claude): the asymmetric path most likely to regress ---
H2="$TMP/home-copilot-only"
mkskill "$H2/.copilot/skills/solo"
OUT2="$( cd "$REPO" && HOME="$H2" bash "$SHOW" --markdown --global 2>&1 )"; rc2=$?
[ "$rc2" -eq 0 ] && pass "show exits clean with Copilot-only HOME" || fail "Copilot-only non-zero exit ($rc2): $OUT2"
printf '%s\n' "$OUT2" | grep -q '~/.copilot/skills/solo/' && pass "Copilot skill listed when ~/.claude is absent" || fail "Copilot skill missing without ~/.claude"

# --- Claude-only HOME (no ~/.copilot): the mirror of the Copilot-only case ---
H3="$TMP/home-claude-only"
mkskill "$H3/.claude/skills/solo"
OUT3="$( cd "$REPO" && HOME="$H3" bash "$SHOW" --markdown --global 2>&1 )"; rc3=$?
[ "$rc3" -eq 0 ] && pass "show exits clean with Claude-only HOME" || fail "Claude-only non-zero exit ($rc3): $OUT3"
printf '%s\n' "$OUT3" | grep -q '~/.claude/skills/solo/' && pass "Claude skill listed when ~/.copilot is absent" || fail "Claude skill missing without ~/.copilot"

[ "$FAILED" -eq 0 ] && echo "personal-inventory.test.sh: ALL PASS" || echo "personal-inventory.test.sh: FAILURES"
rm -rf "$TMP"
exit "$FAILED"
