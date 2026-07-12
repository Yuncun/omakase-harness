#!/usr/bin/env bash
# TDD spec for the EARLIER-THAN-COMMIT worktree-discipline layers (issue #86). The
# commit-time gate (a custom harness's allowlist gate over omakase-gate.sh) stays the
# fail-closed last line; these two layers fire before it:
#   - `omakase statusline` (binary): appends a persistent "main checkout · use a worktree"
#                                   segment when this is the main checkout AND other
#                                   worktrees are active — covered by the Go tests in
#                                   internal/probe + internal/render, not here.
#   - omakase-worktree-guard.sh   : opt-in Claude Code PreToolUse hook (matcher Edit|Write).
#                                   Denies edits to product files in the MAIN checkout while
#                                   other worktrees are active; the allowlist mirrors the
#                                   commit gate's (AGENTS.md, CLAUDE.md, .claude/**, root
#                                   *.md) plus paths that CANNOT leak into a commit
#                                   (.omakase/** is force-excluded; .git/** isn't content).
# Both honor OMAKASE_SKIP_WORKTREE_DISCIPLINE=1 and a "worktree-discipline" line in the
# shared disabled-gates file (the menu's persistent, visible disable). The guard fails
# OPEN on anything it cannot parse or resolve — it is a pre-layer; the commit gate is
# the layer that must fail closed.
set -u
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
GUARD="$HERE/../payload/.omakase/bin/omakase-worktree-guard.sh"
TMP="${TMPDIR:-/tmp}/omakase-wtdisc-test.$$"
FAILED=0
pass(){ echo "  PASS: $1"; }
fail(){ echo "  FAIL: $1"; FAILED=1; }
newrepo(){ rm -rf "$1"; mkdir -p "$1"; ( cd "$1" && git init -q && git config user.email t@t && git config user.name t && git config commit.gpgsign false && git commit -q --allow-empty -m init ); }
common_of(){ ( cd "$1" && cd "$(git rev-parse --git-common-dir)" && pwd ); }

# ---------- Scenario B: PreToolUse worktree guard ----------
echo "== Scenario B: omakase-worktree-guard PreToolUse hook =="
REPO="$TMP/repoB"; newrepo "$REPO"; mkdir -p "$REPO/.omakase"
ROOT="$(cd "$REPO" && git rev-parse --show-toplevel)"   # physical path, matches worktree list
guard(){ # $1=cwd $2=file_path -> guard stdout
  printf '{"tool_name":"Edit","tool_input":{"file_path":"%s","old_string":"a","new_string":"b"},"cwd":"%s"}' "$2" "$1" | bash "$GUARD"
}

# Before any other worktree exists: everything is allowed.
OUT="$(guard "$ROOT" "$ROOT/src/app.go")"; RC=$?
{ [ "$RC" -eq 0 ] && [ -z "$OUT" ]; } && pass "no other worktrees -> allow" || fail "denied without worktrees ($RC: $OUT)"

( cd "$REPO" && git worktree add -q "$TMP/wtB" -b wt-b )
WTROOT="$(cd "$TMP/wtB" && git rev-parse --show-toplevel)"

# Product file in the main checkout -> deny, with the teaching + bypass in the reason.
OUT="$(guard "$ROOT" "$ROOT/src/app.go")"; RC=$?
[ "$RC" -eq 0 ] && pass "deny exits 0 (decision travels in JSON)" || fail "deny exited $RC"
echo "$OUT" | grep -q '"permissionDecision":"deny"' && pass "product file in main checkout -> deny" || fail "no deny for product file ($OUT)"
echo "$OUT" | grep -q '"hookEventName":"PreToolUse"' && pass "deny names the PreToolUse event" || fail "malformed hook output ($OUT)"
echo "$OUT" | grep -q 'src/app.go' && pass "reason names the file" || fail "reason missing the file ($OUT)"
echo "$OUT" | grep -q 'worktree' && pass "reason teaches the worktree rule" || fail "reason missing the rule ($OUT)"
echo "$OUT" | grep -q 'OMAKASE_SKIP_WORKTREE_DISCIPLINE=1' && pass "reason names the bypass" || fail "reason missing the bypass ($OUT)"

# Relative file_path resolves against cwd.
OUT="$(guard "$ROOT" "src/app.go")"
echo "$OUT" | grep -q '"permissionDecision":"deny"' && pass "relative file_path -> deny" || fail "relative path escaped the guard ($OUT)"

# The allowlist (the commit gate's, verbatim).
for f in AGENTS.md CLAUDE.md .claude/settings.json README.md; do
  OUT="$(guard "$ROOT" "$ROOT/$f")"
  [ -z "$OUT" ] && pass "allowlist: $f -> allow" || fail "allowlist file denied: $f ($OUT)"
done
OUT="$(guard "$ROOT" "$ROOT/docs/notes.md")"
echo "$OUT" | grep -q '"permissionDecision":"deny"' && pass "nested .md is NOT allowlisted" || fail "nested .md escaped ($OUT)"

# Paths that cannot leak into a commit: the overlay (force-excluded) and the git dir.
OUT="$(guard "$ROOT" "$ROOT/.omakase/gates/example.sh")"
[ -z "$OUT" ] && pass ".omakase/** -> allow (cannot leak into a commit)" || fail "overlay edit denied ($OUT)"
OUT="$(guard "$ROOT" "$ROOT/.git/info/exclude")"
[ -z "$OUT" ] && pass ".git/** -> allow (not committable content)" || fail "git-dir edit denied ($OUT)"

# Editing a file in ANOTHER worktree from a main-checkout session is the desired flow.
OUT="$(guard "$ROOT" "$WTROOT/src/app.go")"
[ -z "$OUT" ] && pass "file outside this checkout -> allow" || fail "other-worktree file denied ($OUT)"

# From a linked worktree everything is allowed.
OUT="$(guard "$WTROOT" "$WTROOT/src/app.go")"
[ -z "$OUT" ] && pass "linked worktree -> allow" || fail "denied inside a worktree ($OUT)"

# Bypasses: the audited skip env and the menu's persistent disable.
OUT="$(printf '{"tool_name":"Edit","tool_input":{"file_path":"%s/src/app.go"},"cwd":"%s"}' "$ROOT" "$ROOT" | OMAKASE_SKIP_WORKTREE_DISCIPLINE=1 bash "$GUARD")"
[ -z "$OUT" ] && pass "OMAKASE_SKIP_WORKTREE_DISCIPLINE=1 -> allow" || fail "skip env ignored ($OUT)"
COMMONB="$(common_of "$REPO")"; mkdir -p "$COMMONB/omakase"
printf 'worktree-discipline\n' > "$COMMONB/omakase/disabled-gates"
OUT="$(guard "$ROOT" "$ROOT/src/app.go")"
[ -z "$OUT" ] && pass "disabled-gates 'worktree-discipline' -> allow" || fail "disabled-gates ignored ($OUT)"
rm -f "$COMMONB/omakase/disabled-gates"

# Fail OPEN on anything unresolvable: no repo, no file_path in the payload.
NOREPO="$TMP/norepo"; rm -rf "$NOREPO"; mkdir -p "$NOREPO"
OUT="$(printf '{"tool_name":"Edit","tool_input":{"file_path":"/x/y.go"},"cwd":"%s"}' "$NOREPO" | bash "$GUARD")"; RC=$?
{ [ "$RC" -eq 0 ] && [ -z "$OUT" ]; } && pass "outside any repo -> allow" || fail "guard fired outside a repo ($RC: $OUT)"
OUT="$(printf '{"tool_name":"Edit","tool_input":{},"cwd":"%s"}' "$ROOT" | bash "$GUARD")"; RC=$?
{ [ "$RC" -eq 0 ] && [ -z "$OUT" ]; } && pass "no file_path -> allow (fail open)" || fail "guard fired with no file_path ($RC: $OUT)"

rm -rf "$TMP"
echo ""
[ "$FAILED" -eq 0 ] && echo "ALL PASS" || { echo "FAILURES PRESENT"; exit 1; }
