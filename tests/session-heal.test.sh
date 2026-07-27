#!/usr/bin/env bash
# Behavioral spec for the plugin's SessionStart heal (#164 C5, narrow scope):
# hooks/hooks.json runs hooks/session-start.sh when a host session opens, and
# the script must NEVER fail or narrate a session start except to report a
# repair:
#   H1. hooks/hooks.json is valid JSON, declares exactly the SessionStart
#       event, and its command points at the script that exists (and is
#       executable) in this repo.
#   H2. Outside any git repo: exit 0, no output.
#   H3. No omakase binary resolvable (OMAKASE_BIN pointed at a nonexistent
#       path): exit 0, no output — session start never waits on a fetch.
#   H4. Installed repo with a placed file deleted: the file is restored and
#       stdout carries the one-line report.
#   H5. Installed repo, overlay intact: exit 0, no output (silence is the
#       contract — the line must EARN its place).
set -u
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
HOOK="$HERE/../hooks/session-start.sh"
HOOKS_JSON="$HERE/../hooks/hooks.json"
TMP="${TMPDIR:-/tmp}/omakase-session-heal.$$"
FAILED=0
pass(){ echo "  PASS: $1"; }
fail(){ echo "  FAIL: $1"; FAILED=1; }
mkdir -p "$TMP"
trap 'rm -rf "$TMP"' EXIT

# The binary: CI exports OMAKASE_BIN; locally, build the dev binary.
if [ -z "${OMAKASE_BIN:-}" ]; then
  ( cd "$HERE/.." && go build -o dist/omakase ./cmd/omakase ) || { echo "FAIL: go build"; exit 1; }
  OMAKASE_BIN="$HERE/../dist/omakase"
fi

echo "H1: hooks.json wiring is sound"
if python3 -c 'import json,sys; d=json.load(open(sys.argv[1])); assert list(d["hooks"].keys())==["SessionStart"], d' "$HOOKS_JSON" 2>/dev/null; then
  pass "valid JSON, SessionStart only"
else
  fail "hooks.json invalid or declares more than SessionStart"
fi
if grep -q 'CLAUDE_PLUGIN_ROOT}/hooks/session-start.sh' "$HOOKS_JSON" && [ -x "$HOOK" ]; then
  pass "command targets the executable script"
else
  fail "command/script mismatch (or script not executable)"
fi

echo "H2: outside a git repo — silent exit 0"
OUT=$(cd "$TMP" && OMAKASE_BIN="$OMAKASE_BIN" bash "$HOOK" 2>&1); RC=$?
if [ "$RC" -eq 0 ] && [ -z "$OUT" ]; then pass "silent 0"; else fail "rc=$RC out='$OUT'"; fi

echo "H3: binary unresolvable — silent exit 0, no fetch"
OUT=$(cd "$TMP" && OMAKASE_BIN="$TMP/nonexistent-omakase" bash "$HOOK" 2>&1); RC=$?
if [ "$RC" -eq 0 ] && [ -z "$OUT" ]; then pass "silent 0"; else fail "rc=$RC out='$OUT'"; fi

# An installed repo: init a fixture harness with one placed rule file.
export HOME="$TMP/home" XDG_CACHE_HOME="$TMP/cache"
mkdir -p "$HOME" "$XDG_CACHE_HOME"
git config --global user.email t@t; git config --global user.name t
git config --global init.defaultBranch main
REPO="$TMP/repo"; mkdir -p "$REPO"; git -C "$REPO" init -q
FIXPAY="$TMP/fixpay"; mkdir -p "$FIXPAY/.claude/rules"
printf 'the rule\n' > "$FIXPAY/.claude/rules/fix.md"
printf 'name: fixture-harness\nversion: 0.1.0\n' > "$FIXPAY/omakase.manifest"
( cd "$REPO" && OMAKASE_PAYLOAD="$FIXPAY" "$OMAKASE_BIN" init >/dev/null 2>&1 ) || { echo "FAIL: fixture init"; exit 1; }

echo "H4: placed file deleted — restored + one-line report"
rm "$REPO/.claude/rules/fix.md"
OUT=$(cd "$REPO" && OMAKASE_BIN="$OMAKASE_BIN" bash "$HOOK" 2>/dev/null); RC=$?
if [ "$RC" -eq 0 ] && [ -f "$REPO/.claude/rules/fix.md" ] && echo "$OUT" | grep -q "restored 1 missing harness file"; then
  pass "restored and reported"
else
  fail "rc=$RC out='$OUT' file-present=$([ -f "$REPO/.claude/rules/fix.md" ] && echo yes || echo no)"
fi

echo "H5: intact overlay — silent exit 0"
OUT=$(cd "$REPO" && OMAKASE_BIN="$OMAKASE_BIN" bash "$HOOK" 2>&1); RC=$?
if [ "$RC" -eq 0 ] && [ -z "$OUT" ]; then pass "silent 0"; else fail "rc=$RC out='$OUT'"; fi

[ "$FAILED" -eq 0 ] && echo "ALL PASS" || echo "FAILURES"
exit "$FAILED"
