#!/usr/bin/env bash
# Proof for bin/xray.sh (PROTOTYPE): the agentic x-ray finds every agent-facing file in a
# repo, groups it by lifecycle stage, tags provenance, and raises the cruft/risk findings —
# committed executable config, context bloat, AGENTS/CLAUDE drift, dead references,
# deprecated forms, unwired gates — without false-positives on clean shapes.
set -u
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
XRAY="$HERE/../bin/xray.sh"
TMP="${TMPDIR:-/tmp}/omakase-xray-test.$$"
REPO="$TMP/repo"
FAILED=0
pass(){ echo "  PASS: $1"; }
fail(){ echo "  FAIL: $1"; FAILED=1; }
trap 'rm -rf "$TMP"' EXIT

# ---------- a deliberately crufty repo ----------
mkdir -p "$REPO"
git -C "$REPO" init -q
mkdir -p "$REPO/.github/workflows" "$REPO/.claude/skills/demo" "$REPO/docs" "$REPO/sub"

# session-start files: AGENTS.md (with one dead ref + one live ref), a DIFFERING CLAUDE.md,
# a deprecated .cursorrules, and a bloated copilot-instructions (400 lines).
{ echo "# agents"; echo "see \`docs/real.md\` and \`docs/gone.md\`"; } > "$REPO/AGENTS.md"
echo "different content" > "$REPO/CLAUDE.md"
echo "old cursor rules" > "$REPO/.cursorrules"
touch "$REPO/docs/real.md"
i=0; while [ "$i" -lt 400 ]; do echo "instruction line $i padding padding padding"; i=$((i+1)); done > "$REPO/.github/copilot-instructions.md"
echo "# nested" > "$REPO/sub/AGENTS.md"

# on-demand + committed executable config: a skill, a git-hook config, an MCP config.
printf -- '---\nname: demo\n---\nhi\n' > "$REPO/.claude/skills/demo/SKILL.md"
printf 'pre-commit:\n  jobs:\n    - run: echo hi\n' > "$REPO/lefthook.yml"
printf '{"mcpServers":{"x":{"command":"echo"}}}\n' > "$REPO/.mcp.json"

# CI: one agent-aware workflow (marker) and one plain workflow (no marker).
printf 'jobs:\n  a:\n    steps:\n      - uses: anthropics/claude-code-action@v1\n' > "$REPO/.github/workflows/agent.yml"
printf 'jobs:\n  b:\n    steps:\n      - run: make test\n' > "$REPO/.github/workflows/plain.yml"

git -C "$REPO" add -A && git -C "$REPO" commit -qm fixture

echo "== rows: stages, provenance, collapsing =="
OUT="$(cd "$REPO" && bash "$XRAY")" || fail "xray exited non-zero"
printf '%s\n' "$OUT" | grep -q "SESSION START" && pass "session-start stage rendered" || fail "no session-start stage"
printf '%s\n' "$OUT" | grep -q "AGENTS.md   (all, committed" && pass "AGENTS.md row with provenance" || fail "AGENTS.md row missing"
printf '%s\n' "$OUT" | grep -q "sub/AGENTS.md.*nested" && pass "nested AGENTS.md annotated" || fail "nested annotation missing"
printf '%s\n' "$OUT" | grep -q ".claude/skills/demo/" && pass "skill collapsed to one dir row" || fail "skill row missing"
printf '%s\n' "$OUT" | grep -q "ON COMMIT/PUSH" && pass "git-hook stage rendered" || fail "git-hook stage missing"
printf '%s\n' "$OUT" | grep -q "agent.yml" && pass "agent-aware workflow listed" || fail "agent workflow missing"
printf '%s\n' "$OUT" | grep -q "plain.yml" && fail "plain workflow leaked into CI stage" || pass "plain workflow excluded"

echo "== findings =="
printf '%s\n' "$OUT" | grep -q "committed executable agent config" && pass "committed-execution risk raised" || fail "committed-execution missing"
printf '%s\n' "$OUT" | grep -q "context bloat: .github/copilot-instructions.md" && pass "bloat flagged" || fail "bloat missing"
printf '%s\n' "$OUT" | grep -q "AGENTS.md and CLAUDE.md DIFFER" && pass "instruction drift flagged" || fail "drift missing"
printf '%s\n' "$OUT" | grep -q "dead references in AGENTS.md: docs/gone.md" && pass "dead reference found" || fail "dead reference missing"
printf '%s\n' "$OUT" | grep -q "docs/real.md" && fail "live reference misreported as dead" || pass "live reference not flagged"
printf '%s\n' "$OUT" | grep -q ".cursorrules is the deprecated" && pass "deprecated form flagged" || fail "deprecated form missing"
printf '%s\n' "$OUT" | grep -q "NO git hook is installed" && pass "unwired gate flagged" || fail "unwired gate missing"

echo "== markdown mode =="
MD="$(cd "$REPO" && bash "$XRAY" --markdown)" || fail "markdown mode exited non-zero"
printf '%s\n' "$MD" | grep -q "^## " && pass "md heading" || fail "no md heading"
printf '%s\n' "$MD" | grep -q "### Findings" && pass "md findings section" || fail "no md findings section"

echo "== clean shapes stay clean =="
CLEAN="$TMP/clean"; mkdir -p "$CLEAN"
git -C "$CLEAN" init -q
echo "# short" > "$CLEAN/AGENTS.md"
ln -s AGENTS.md "$CLEAN/CLAUDE.md"
git -C "$CLEAN" add -A && git -C "$CLEAN" commit -qm fixture
COUT="$(cd "$CLEAN" && bash "$XRAY")" || fail "xray on clean repo exited non-zero"
printf '%s\n' "$COUT" | grep -q "symlink to AGENTS.md" && pass "linked CLAUDE.md reported as info" || fail "symlink info missing"
printf '%s\n' "$COUT" | grep -qE '^  [!~] ' && fail "clean repo raised cruft/risk" || pass "no cruft/risk on clean repo"

exit "$FAILED"
