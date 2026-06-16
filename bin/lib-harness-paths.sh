# shellcheck shell=bash
# omakase-harness — the single source of truth for "which repo paths are agent-harness
# artifacts, and what kind each is." Sourced by init.sh (records the kind in the provenance
# ledger), show.sh (inventory + committed-surface scan), and import.sh (capture locations).
# NOT executed directly: defines one function + three path lists and runs nothing at source
# time. Safe under the callers' `set -euo pipefail` — the arrays are never empty, so there
# is no unbound-array expansion under bash 3.2 + set -u.
#
# omakase itself is host-agnostic: it injects whatever a payload contains and never branches
# on "which agent." This table is the ONLY place that encodes a specific agent's on-disk
# layout, and it encodes them all at once — there is no per-host mode. Supporting another
# agent (Cursor, Gemini, …) = add its rows below; nothing else in the engine changes.
#
#   Claude Code        : .claude/{rules,skills,commands,hooks}, .claude/settings*.json, CLAUDE.md, AGENTS.md
#   GitHub Copilot CLI : .github/skills/<name>/SKILL.md, .github/instructions/, .github/copilot-instructions.md
#       (Copilot CLI loads project skills from .github/skills live from disk — see
#        https://docs.github.com/en/copilot/how-tos/copilot-cli/customize-copilot/add-skills)
#
# bash-3.2 safe (macOS ships 3.2): plain indexed arrays only — no associative arrays.

# kind_of: classify a harness path by its location — the path IS the classification.
# Order matters only between the specific patterns and the catch-alls (*/*, *.md, *): every
# specific pattern below is mutually disjoint, so their order RELATIVE TO EACH OTHER is free.
kind_of() {
  case "$1" in
    # --- Claude Code ---
    .claude/rules/*)                                  echo rule;;
    .claude/skills/*)                                 echo skill;;
    .claude/commands/*)                               echo command;;
    .claude/settings.json|.claude/settings.*.json)    echo config;;
    # --- GitHub Copilot ---
    .github/skills/*)                                 echo skill;;
    .github/instructions/*)                           echo rule;;
    .github/copilot-instructions.md)                  echo doc;;
    # --- host-agnostic ---
    lefthook-local.yml|lefthook.yml|.omakase/gates/*) echo gate;;
    AGENTS.md|CLAUDE.md)                              echo doc;;
    */*)                                              echo other;;  # nested, none of the above
    *.md)                                             echo doc;;    # remaining root-level *.md
    *)                                                echo other;;
  esac
}

# import.sh capture locations — single files and whole dirs it walks ON DISK to mirror an
# existing harness into payload/ (see import.sh rule 1). Keep in step with kind_of above.
HARNESS_LOC_FILES=(AGENTS.md CLAUDE.md .github/copilot-instructions.md lefthook-local.yml lefthook.yml .pre-commit-config.yaml .claude/settings.json)
HARNESS_LOC_DIRS=(.claude/rules .claude/skills .claude/commands .claude/hooks .github/skills .github/instructions .omakase .husky .githooks)

# show.sh committed-surface scan — the tracked pathspecs it audits as the project's OWN
# committed harness:  git ls-files -- "${HARNESS_COMMITTED_GLOBS[@]}"
HARNESS_COMMITTED_GLOBS=(AGENTS.md CLAUDE.md CLAUDE.local.md .claude lefthook.yml lefthook-local.yml .lefthook .omakase .github/copilot-instructions.md .github/instructions .github/skills)

# Top-level dirs omakase SHARES with the project rather than owning outright. An injected
# path under one of these is excluded from git file-by-file in .git/info/exclude — never the
# whole dir — so omakase never hides the project's OWN untracked files there. (.github holds
# Copilot skills/instructions but also workflows, issue templates, dependabot config, ….)
# Dirs NOT listed here (.omakase, .claude) are omakase-owned and excluded wholesale.
HARNESS_SHARED_TOPDIRS=(.github)
