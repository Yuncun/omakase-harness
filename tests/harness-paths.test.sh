#!/usr/bin/env bash
# Unit test for lib-harness-paths.sh — the single source of truth for path classification.
# Proves kind_of() recognizes both the Claude Code and GitHub Copilot layouts (and the
# host-agnostic ones), and that the shared capture/scan lists carry the Copilot paths.
# No lefthook, no git, no temp repo — pure classification, so it runs anywhere.
set -euo pipefail   # exercise the lib under the same strictness its callers use
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
. "$HERE/../bin/lib-harness-paths.sh"

FAILED=0
pass(){ echo "  PASS: $1"; }
fail(){ echo "  FAIL: $1 (got '$2', want '$3')"; FAILED=1; }
eq(){ local got; got="$(kind_of "$1")"; [ "$got" = "$2" ] && pass "kind_of $1 -> $2" || fail "kind_of $1" "$got" "$2"; }
has(){ local n="$1"; shift; local x; for x in "$@"; do [ "$x" = "$n" ] && { pass "list carries $n"; return; }; done; fail "list carries $n" "absent" "present"; }

echo "== kind_of: Claude Code =="
eq ".claude/rules/style.md"                  rule
eq ".claude/skills/foo/SKILL.md"             skill
eq ".claude/commands/x.md"                   command
eq ".claude/agents/reviewer.md"              agent
eq ".claude/hooks/pre-commit.sh"             gate
eq ".claude/settings.json"                   config
eq ".claude/settings.local.json"             config
eq "CLAUDE.md"                               doc

echo "== kind_of: GitHub Copilot =="
eq ".github/skills/foo/SKILL.md"             skill
eq ".github/skills/a/b/c.md"                 skill   # deep skill subtree
eq ".github/instructions/x.instructions.md"  rule
eq ".github/prompts/triage.prompt.md"        prompt
eq ".github/chatmodes/coach.chatmode.md"     prompt
eq ".github/hooks/check-verify-gate.py"      gate
eq ".github/hooks/check-verify-gate.json"    gate
eq ".github/copilot-instructions.md"         doc
# Boundary that protects the project's OWN .github content: a non-harness .github file
# must fall through to 'other', never be mistaken for an injected harness artifact.
eq ".github/workflows/ci.yml"                other
eq ".github/dependabot.yml"                  other

echo "== kind_of: host-agnostic + catch-alls =="
eq "lefthook-local.yml"                      gate
eq ".omakase/gates/example.sh"               gate
eq ".husky/pre-commit"                       gate
eq ".githooks/pre-commit"                    gate
eq "README.md"                               doc
eq "some/nested/file.txt"                    other

echo "== shared capture/scan lists carry the Copilot paths =="
has ".github/skills"                  "${HARNESS_LOC_DIRS[@]}"
has ".github/instructions"            "${HARNESS_LOC_DIRS[@]}"
has ".github/hooks"                   "${HARNESS_LOC_DIRS[@]}"
has ".github/prompts"                 "${HARNESS_LOC_DIRS[@]}"
has ".github/chatmodes"               "${HARNESS_LOC_DIRS[@]}"
has ".github/copilot-instructions.md" "${HARNESS_LOC_FILES[@]}"
has ".github/skills"                  "${HARNESS_COMMITTED_GLOBS[@]}"
has ".github/instructions"            "${HARNESS_COMMITTED_GLOBS[@]}"
has ".github/hooks"                   "${HARNESS_COMMITTED_GLOBS[@]}"
has ".github/prompts"                 "${HARNESS_COMMITTED_GLOBS[@]}"
has ".github/chatmodes"               "${HARNESS_COMMITTED_GLOBS[@]}"
has ".github/copilot-instructions.md" "${HARNESS_COMMITTED_GLOBS[@]}"

# Anti-drift lock: every dir omakase IMPORTS (HARNESS_LOC_DIRS) must classify to a real kind.
# This catches a new capture-dir added without a matching kind_of case — the exact bug where
# .claude/hooks / .husky / .githooks were imported but recorded in the ledger as 'other'.
# kind_of matches on the "$dir/*" prefix, so any probe path under the dir exercises its case.
# .omakase is omakase's own mixed plumbing dir (bin/ + VERSION legitimately fall to 'other').
echo "== anti-drift: every capture-dir primitive has a kind_of case =="
for d in "${HARNESS_LOC_DIRS[@]}"; do
  case "$d" in .omakase) continue;; esac
  k="$(kind_of "$d/probe")"
  [ "$k" != other ] && pass "LOC_DIR $d classifies ($k)" || fail "LOC_DIR $d has NO kind_of case" other "a kind"
done

if [ "$FAILED" -eq 0 ]; then echo "harness-paths.test.sh: ALL PASS"; else echo "harness-paths.test.sh: FAILURES"; fi
exit "$FAILED"
