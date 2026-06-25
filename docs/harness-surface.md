# Harness surface — what omakase recognizes

The paths omakase treats as agent-harness, and the `kind` it records for each. This is the
human view of [`bin/lib-harness-paths.sh`](../bin/lib-harness-paths.sh) (the source of truth).
Anything not listed falls to `other` and is left to the project — that boundary is deliberate
(omakase must never mistake `.github/workflows`, dependabot config, etc. for harness).

| Path glob | Host | kind |
|---|---|---|
| `CLAUDE.md`, `AGENTS.md` | both | `doc` |
| `.claude/settings*.json` | Claude | `config` |
| `.claude/rules/*` | Claude | `rule` |
| `.claude/skills/*` | Claude | `skill` |
| `.claude/commands/*` | Claude | `command` |
| `.claude/agents/*` | Claude | `agent` ⬅ |
| `.claude/hooks/*` | Claude | `gate` ⬅ |
| `.github/copilot-instructions.md` | Copilot | `doc` |
| `.github/instructions/*` | Copilot | `rule` |
| `.github/skills/*` | Copilot | `skill` |
| `.github/prompts/*` | Copilot | `prompt` ⬅ |
| `.github/chatmodes/*` | Copilot | `prompt` ⬅ |
| `.github/hooks/*` | Copilot | `gate` ⬅ |
| `lefthook*.yml`, `.omakase/gates/*` | agnostic | `gate` |
| `.husky/*`, `.githooks/*` | agnostic | `gate` ⬅ |
| everything else | — | `other` (not harness) |

⬅ = added/fixed by the 2026-06-18 pressure test (capturing a real Claude+Copilot
repo). Before it, omakase was blind to Copilot's `.github/hooks` gate layer, and three dirs it
already imported (`.claude/hooks`, `.husky`, `.githooks`) were recorded as `other`.

## Why it drifts, and the lock

One table feeds four things that must agree: `kind_of()` (the ledger's label), `HARNESS_LOC_*`
(what `import.sh` captures), and `HARNESS_COMMITTED_GLOBS` (what `show.sh` audits). Add a path to
one, forget the others, and a captured file lands in the ledger as `other` — silently. The lock
is in [`tests/harness-paths.test.sh`](../tests/harness-paths.test.sh): an anti-drift loop asserts
every `HARNESS_LOC_DIRS` entry classifies to a real kind, so a new capture-dir without a matching
`kind_of` case fails the suite.

## Adding another agent (Cursor, Gemini, …)

Add its rows to the table in `lib-harness-paths.sh` (`kind_of` case + `HARNESS_LOC_*` +
`HARNESS_COMMITTED_GLOBS`) and an assertion here. Nothing else in the base harness branches on host —
omakase injects whatever a payload contains; this table is the only host-aware part.
