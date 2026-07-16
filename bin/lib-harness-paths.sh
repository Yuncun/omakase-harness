# shellcheck shell=bash
# omakase-harness — the single source of truth for "which repo paths are agent-harness
# artifacts, and what kind each is." Sourced by init.sh (records the kind in the provenance
# ledger) and status.sh (inventory + committed-surface scan).
# NOT executed directly: defines one function + three path lists and runs nothing at source
# time. Safe under the callers' `set -euo pipefail` — the arrays are never empty, so there
# is no unbound-array expansion under bash 3.2 + set -u.
#
# omakase itself is host-agnostic: it injects whatever a payload contains and never branches
# on "which agent." This table is the ONLY place in bash that encodes a specific agent's
# on-disk layout, and it encodes them all at once — there is no per-host mode.
#
# It also has a Go twin: internal/harness/paths.go, a verbatim port used by the Go `status`
# binary. A row added or changed here MUST be mirrored there — and in its test vectors,
# internal/harness/paths_test.go and tests/harness-paths.test.sh — or the two readers of
# the same on-disk layout drift apart.
#
#   Claude Code        : .claude/{rules,skills,commands,agents,hooks}, .claude/settings*.json, CLAUDE.md, AGENTS.md
#   GitHub Copilot CLI : .github/{skills,instructions,prompts,chatmodes,hooks},
#                        .github/copilot-instructions.md, .github/copilot/settings*.json
#       (Copilot CLI loads project skills from .github/skills live from disk — see
#        https://docs.github.com/en/copilot/how-tos/copilot-cli/customize-copilot/add-skills.
#        .github/hooks holds Copilot's lifecycle hooks — preToolUse/sessionStart gates — and
#        .github/{prompts,chatmodes} hold reusable prompt + persona assets.)
#
# bash-3.2 safe (macOS ships 3.2): plain indexed arrays only — no associative arrays.

# kind_of: classify a harness path by its location — the path IS the classification.
# Order matters only between the specific patterns and the catch-alls (*/*, *.md, *): every
# specific pattern below is mutually disjoint, so their order RELATIVE TO EACH OTHER is free.
# Valid kinds: rule skill command agent prompt config doc gate other.
# INVARIANT: every dir named in HARNESS_LOC_DIRS (except .omakase, omakase's own mixed
# plumbing dir) must classify to a kind other than 'other' — harness-paths.test.sh enforces it.
kind_of() {
  case "$1" in
    # --- Claude Code ---
    .claude/rules/*)                                  echo rule;;
    .claude/skills/*)                                 echo skill;;
    .claude/commands/*)                               echo command;;
    .claude/agents/*)                                 echo agent;;
    .claude/hooks/*)                                  echo gate;;
    .claude/settings.json|.claude/settings.*.json)    echo config;;
    # --- GitHub Copilot ---
    .github/skills/*)                                 echo skill;;
    .github/instructions/*)                           echo rule;;
    .github/prompts/*|.github/chatmodes/*)            echo prompt;;
    .github/hooks/*)                                  echo gate;;
    .github/copilot-instructions.md)                  echo doc;;
    .github/copilot/settings.json|.github/copilot/settings.*.json) echo config;;
    # --- host-agnostic ---
    lefthook-local.yml|lefthook.yml|.omakase/gates/*) echo gate;;
    .husky/*|.githooks/*)                             echo gate;;
    AGENTS.md|CLAUDE.md)                              echo doc;;
    */*)                                              echo other;;  # nested, none of the above
    *.md)                                             echo doc;;    # remaining root-level *.md
    *)                                                echo other;;
  esac
}

# Harness-owned paths — the anti-drift lock's source list; tests/harness-paths.test.sh asserts
# every HARNESS_LOC_DIRS entry classifies to a real kind above. No capture tool reads these
# arrays today — a harness author places files by hand, see docs/harness-surface.md. Keep in
# step with kind_of above.
HARNESS_LOC_FILES=(AGENTS.md CLAUDE.md .github/copilot-instructions.md .github/copilot/settings.json .github/copilot/settings.local.json lefthook-local.yml lefthook.yml .pre-commit-config.yaml .claude/settings.json)
HARNESS_LOC_DIRS=(.claude/rules .claude/skills .claude/commands .claude/agents .claude/hooks .github/skills .github/instructions .github/prompts .github/chatmodes .github/hooks .omakase .husky .githooks)

# status.sh committed-surface scan — the tracked pathspecs it audits as the project's OWN
# committed harness:  git ls-files -- "${HARNESS_COMMITTED_GLOBS[@]}"
HARNESS_COMMITTED_GLOBS=(AGENTS.md CLAUDE.md CLAUDE.local.md .claude lefthook.yml lefthook-local.yml .lefthook .omakase .husky .githooks .github/copilot-instructions.md .github/copilot/settings.json .github/copilot/settings.local.json .github/instructions .github/skills .github/prompts .github/chatmodes .github/hooks)

# Top-level dirs omakase SHARES with the project rather than owning outright. An injected
# path under one of these is excluded from git file-by-file in .git/info/exclude — never the
# whole dir — so omakase never hides the project's OWN untracked files there. (.github holds
# Copilot skills/instructions but also workflows, issue templates, dependabot config, ….)
# Dirs NOT listed here (.omakase, .claude) are omakase-owned and excluded wholesale.
HARNESS_SHARED_TOPDIRS=(.github)
