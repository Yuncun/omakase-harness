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
eq ".claude/settings.json"                   config
eq ".claude/settings.local.json"             config
eq "CLAUDE.md"                               doc

echo "== kind_of: GitHub Copilot =="
eq ".github/skills/foo/SKILL.md"             skill
eq ".github/skills/a/b/c.md"                 skill   # deep skill subtree
eq ".github/instructions/x.instructions.md"  rule
eq ".github/copilot-instructions.md"         doc
# Boundary that protects the project's OWN .github content: a non-harness .github file
# must fall through to 'other', never be mistaken for an injected harness artifact.
eq ".github/workflows/ci.yml"                other
eq ".github/dependabot.yml"                  other

echo "== kind_of: host-agnostic + catch-alls =="
eq "lefthook-local.yml"                      gate
eq ".omakase/gates/example.sh"               gate
eq "README.md"                               doc
eq "some/nested/file.txt"                    other

echo "== shared capture/scan lists carry the Copilot paths =="
has ".github/skills"                  "${HARNESS_LOC_DIRS[@]}"
has ".github/instructions"            "${HARNESS_LOC_DIRS[@]}"
has ".github/copilot-instructions.md" "${HARNESS_LOC_FILES[@]}"
has ".github/skills"                  "${HARNESS_COMMITTED_GLOBS[@]}"
has ".github/instructions"            "${HARNESS_COMMITTED_GLOBS[@]}"
has ".github/copilot-instructions.md" "${HARNESS_COMMITTED_GLOBS[@]}"

if [ "$FAILED" -eq 0 ]; then echo "harness-paths.test.sh: ALL PASS"; else echo "harness-paths.test.sh: FAILURES"; fi
exit "$FAILED"
