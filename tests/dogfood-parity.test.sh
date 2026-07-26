#!/usr/bin/env bash
# The dogfooding harness must give Claude Code and Copilot CLI agents the SAME rules.
# harness/payload ships the one rule set twice — .claude/rules/omakase-dev.md (Claude) and
# .github/instructions/omakase-dev.instructions.md (Copilot, same body plus an applyTo
# frontmatter). #164 C6 caught them drifted: the Copilot copy had silently lost two rules,
# so a Copilot agent worked this repo with fewer conventions — the exact failure mode the
# project exists to prevent. This locks the two bodies byte-identical.
# No git, no temp repo — pure file comparison, so it runs anywhere.
set -euo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CLAUDE="$HERE/../harness/payload/.claude/rules/omakase-dev.md"
COPILOT="$HERE/../harness/payload/.github/instructions/omakase-dev.instructions.md"

FAILED=0
pass(){ echo "  PASS: $1"; }
fail(){ echo "  FAIL: $1"; FAILED=1; }

echo "== dogfood harness: Claude and Copilot rule files carry the same body =="
[ -f "$CLAUDE" ]  && pass "Claude rules file present"  || fail "missing $CLAUDE"
[ -f "$COPILOT" ] && pass "Copilot instructions file present" || fail "missing $COPILOT"

# The Copilot file is the Claude body behind a `---`-fenced frontmatter block. Strip the
# frontmatter (first line must open it) and the blank line after; the rest must match the
# Claude file byte-for-byte — any drift means one host's agents get different rules.
head -1 "$COPILOT" | grep -qx -- '---' && pass "Copilot file opens with frontmatter" || fail "Copilot file has no frontmatter fence"
BODY="$(awk 'NR==1 && $0=="---" {infm=1; next} infm && $0=="---" {infm=0; skipblank=1; next} infm {next} skipblank && $0=="" {skipblank=0; next} {skipblank=0; print}' "$COPILOT")"
if [ "$BODY" = "$(cat "$CLAUDE")" ]; then
  pass "bodies are byte-identical"
else
  fail "bodies differ — a rule edit landed in one host's file only"
  diff <(printf '%s\n' "$BODY") "$CLAUDE" | sed 's/^/      /' || true
fi

if [ "$FAILED" -eq 0 ]; then echo "dogfood-parity.test.sh: ALL PASS"; else echo "dogfood-parity.test.sh: FAILURES"; fi
exit "$FAILED"
