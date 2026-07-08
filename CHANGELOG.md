# Changelog

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the
project uses semantic versioning. Versions before 0.9.0 are in the git history.

## [Unreleased]

## [0.18.0] — 2026-07-08

### Added — 2026-07-07 the consent menu
- **`omakase status` is now the menu on a real terminal**: every steering file and
  gate is a row a human can toggle (arrows + Enter/Space). Pipes, `--markdown`,
  and `--plain` keep the static page byte-for-byte. `--disable`/`--enable <name>`
  are the scriptable twins; machinery and unknown names refuse. A file toggled
  off stays off across re-init; a disabled gate is recorded in the git dir's
  `omakase/disabled-gates` and skipped VISIBLY at hook time until re-enabled.
  A locally edited file refuses either toggle rather than lose the edits.
- **`omakase mcp`** — a stdio MCP server (binary-only verb) serving the same
  consent menu inside Claude Code / Copilot CLI as native form dialogs (MCP
  elicitation), plus a read-only `status` tool. The menu is one nested
  cascade form: a header row per dev-loop stage (keep as-is / all on / all
  off, cascading over rows left unchanged) with a row per file and gate
  beneath it; `expand` gives every file its own row instead of one row per
  directory. Three disposable list-layout experiments (`variant`: triage /
  preset / sections) ran for live A/B testing and were deleted after the
  form above won.
- First external Go dependencies: bubbletea/lipgloss for the interactive screen
  (vendored with a one-file patch that stops an import-time terminal query —
  provenance and upgrade path in `third_party/bubbletea/PATCH.md`) and the
  official MCP go-sdk.

### Removed — 2026-07-03 slim-cut
- **Reverted to a 3-verb, single-harness overlay** (`init` / `remove` / `status`). A
  YAGNI audit found the layered design below (Phases 3-3.5) had no real user demand
  behind it and cut the whole surface before any of it reached a release: two-harness
  stacking (a second `init <source>` stacking on top instead of replacing, `remove
  <source>` unlayering one harness), the `AGENTS.md` → `CLAUDE.local.md` instruction
  reroute and its `CLAUDE.md` bridge symlink, the v1→v2 migration and mixed-era
  detection, pins (`sources.tsv`, per-source resolved-commit recording), the `update`
  verb, and `enable`/`disable` gate toggles are all gone. `init` is back to plain v1
  semantics: install, repair the same source, or replace a different one (sweeping the
  old source's orphaned files) — it never stacks. `remove` takes no arguments — argv is
  ignored — and is always a bare, total teardown; there is no `remove <source>`.
  Instruction files are placed VERBATIM, exactly as a harness ships them: no reroute, no
  synthesized bridge, no root-slot fallback. `sources.tsv` and the `$OMK/layers/` store
  are deleted; `$OMK/source` (one line, frozen v1 format) is the only remembered-source
  record. `share`/`import` stay removed (cut earlier in this same effort, before this
  entry). `docs/v2-design.md` is marked superseded and kept only as a historical record;
  `docs/reference.md` describes the current contract.
- **Fixed:** `init --source` repairing an already-installed harness while offline used to
  brick the repair when the source's cache refresh failed (no network, no fallback). It
  now falls back to reusing the last good cached copy instead of failing the repair.
- **Deferred at the slim-cut, since rebuilt:** persistent gate toggles were cut
  outright here; the consent-menu stack (see Added above, 2026-07-07) rebuilt
  them in a new shape — per-item human consent, visible skips — rather than the
  original enable/disable verbs.

### Added
- **Layers + the `personal` verb (v2 design §4/§5/§7/§9).** A repo can now hold a *project*
  harness and your *personal* harness at once, stacked highest-layer-wins (whole-file, never
  merged). `init` records the stack in `$OMK/sources.tsv` (bottom-to-top) and each layer's
  full file set under `$OMK/layers/<layer>/`; `placed.tsv` keeps its frozen 5 columns, with
  the winning layer's label in the existing column 3. A personal harness is one global
  setting (`${XDG_CONFIG_HOME:-~/.config}/omakase/personal`), auto-layered on every future
  `init`. A personal `AGENTS.md` is rerouted to Claude Code's additive `CLAUDE.local.md`
  slot (it adds to the project's instructions, never shadows them); a project `AGENTS.md`
  still gains the `CLAUDE.md → AGENTS.md` bridge symlink unless a layer or the repo already
  provides `CLAUDE.md`. New verb `omakase personal [<source> | off]` prints/sets/clears the
  setting and, in an initialized repo, applies or unlayers it immediately. `init
  --no-personal` persistently opts a repo out. Migration is lazy and read-only: the first v2
  verb in a v1 repo synthesizes `sources.tsv` from `$OMK/source` (commit `-`, never guessed);
  a v1 tool that later disagrees with the recorded stack is detected and rehealed on the next
  `init`. Covered black-box end-to-end by `tests/layers.test.sh`. (Superseded below —
  Phase 3.5 replaced the `personal` verb and the global setting with source-stacking
  through `init`/`remove` before any of this reached a release.)

### Changed
- Renamed the inventory script `bin/show.sh` → `bin/status.sh` so it matches the `status`
  verb it has served since the command-surface redesign (the 0.16.0 entry below noted the
  verb still called `bin/show.sh`). Plugin-internal only: `bin/` is never injected into an
  adopter repo, the `status` skill behaves identically, and no payload behavior changes.
- `status` is now implemented by the omakase Go binary, behind the unchanged
  `bin/status.sh` entry point: byte-identical output, with the frozen v1 bash preserved at
  `bin/legacy/status.sh` as the no-Go fallback. New differential parity suite,
  `tests/status-parity.test.sh`.
- `init` and `remove` are now implemented by the omakase Go binary too, behind the
  unchanged `bin/init.sh` / `bin/remove.sh` entry points (thin shims that rebuild and exec
  the binary, falling back to the frozen v1 bash preserved at `bin/legacy/init.sh` /
  `bin/legacy/remove.sh`). Output is byte-identical except per-file list ORDER: Go's
  directory walk is lexical where find(1) was filesystem-order, so the placed-file listing,
  the `placed.tsv` rows, and the `.git/info/exclude` + `.worktreeinclude` entries can appear
  in a different (still complete, still correct) order. New differential parity suite,
  `tests/init-remove-parity.test.sh`.
- **Init now stacks; `personal` is gone (Phase 3.5, v2 design §3/§4/§5/§7).** A second
  `init <source>` on a different source no longer replaces the installed harness — it
  stacks on top instead: both harnesses' files stay live, and the newer `init` wins
  only where both ship the same path (temporal precedence, always narrated on stdout,
  capped at two sources — a third, different source errors and changes nothing).
  `omakase remove <source>` unlayers just that one harness, restoring whatever it had
  shadowed from the other; bare `remove` keeps its v1 total-teardown meaning. The
  `personal` verb, the global `${XDG_CONFIG_HOME:-~/.config}/omakase/personal`
  setting, and `init --no-personal` are deleted: every harness on the stack is one you
  installed explicitly with `init`, nothing layers in automatically. `sources.tsv`'s
  layer column is now an ordinal (`1` bottom, `2` top) instead of a `project`/
  `personal` role name, with no back-compat for the Phase-3-era role labels — that
  surface never reached a release, so zero users are affected. Instruction routing is
  role-free: whichever harness first places a root `AGENTS.md` owns the root slot (and
  the `CLAUDE.md` bridge, if nothing else provides `CLAUDE.md`); a later or
  slot-blocked harness's `AGENTS.md` reroutes to `CLAUDE.local.md` instead, narrated on
  stdout. Covered by the rewritten `tests/layers.test.sh` (142 assertions).

## [0.17.0] — 2026-06-29

### Breaking - gate primitive

One primitive (`omakase-gate.sh`) replaces the three scripts it supersedes
(`omakase-ledger.sh`, `omakase-record.sh`, `deferred-check.sh`). These three files are
removed from the base payload; `omakase init` sweeps orphaned copies from adopter repos.

**Run ledger**: columns drop from 6 to 4 (`epoch name verdict sha`). A pre-v2 ledger with
6 columns is renamed aside on `omakase init`; the new ledger starts fresh.

**Removed environment variables**: `OMAKASE_HOOK`, `OMAKASE_CHECK`, `OMAKASE_GLOB`,
`OMAKASE_BASE`. The waiver mechanism (`--verdict`, `--reason`, `WAIVED` rows) is gone.
The single audited bypass is `OMAKASE_SKIP_<NAME>=1` (name upper-cased, `-`→`_`).

**Migration for adopters**: run `omakase init` once. The orphan sweep removes the three old
scripts, re-injects the wiring, and rotates the old ledger.

**Migration for harness authors**: rewire `lefthook-local.yml` jobs to call
`bash .omakase/bin/omakase-gate.sh <name> --step '<cmd>' [--cacheable] [--glob '<pats>']`;
replace `omakase-record.sh` calls with `omakase-gate.sh <name> --record`.

### Added
- `examples/sample-harness/` — a minimal worked custom harness (one rule, one gate, the wiring)
  to read, try, and copy. It ships only its delta and relies on the base harness machinery layered
  in at install, so it doubles as a live demonstration of the base+source merge. Covered end-to-end
  by `tests/sample-harness.test.sh` (copy into a repo → `init --source` → gate fires → remove).
- A `.claude-plugin/marketplace.json` so the repo is itself an installable marketplace: the
  documented `plugin marketplace add yuncun/omakase-harness` + `plugin install
  omakase@omakase` now resolves (the plugin's `source` is the repo root, `"./"`).
  Without it those install lines had nothing to fetch.
- A **one-skill-per-verb command surface** (`skills/{init,status,remove,share,add-gate}/`), each a
  thin self-locating `run.sh` over the base harness's `bin/`. It works the same on Claude Code
  (typed as `/omakase:init` or model-invoked), Copilot CLI, and a plain shell. Replaces the single
  dispatch-on-argument command/skill; `commands/` is dropped (Claude Code merges commands into
  skills, so one set of skills serves both hosts).
- `omakase share` — the inverse of `init`: capture the current repo's harness into a new,
  publishable harness repo created as a sibling directory (`payload/` + `omakase.manifest` + a
  README carrying the install line), git-initialized and committed ready to push. Prints the
  one-line install others run, `omakase init you/harness`. Wraps `import.sh`. Covered by
  `tests/share.test.sh`.
- `init` accepts an `owner/repo[#ref]` shorthand (e.g. `omakase init alice/harness`) that expands
  to `https://github.com/owner/repo`, optionally pinned to a branch or tag — the shareable install
  line `share` prints. An existing local path of the same shape still wins.
- A `--source` install now layers the **base harness's payload under the custom harness's delta**
  (base machinery underneath, the custom harness winning on overlap), so a custom harness ships
  only its own payload and relies on base machinery — the banner, `omakase-ledger.sh`,
  `omakase-record.sh`, `deferred-check.sh`, the status-line and stop-notice scripts — without
  keeping its own copy. This mirrors the base+delta merge `tools/build.sh` bakes into a plugin
  bundle, performed at install time instead; for a symlink-free custom harness a `--source`
  install and a built bundle place a byte-identical file set (verified against a real harness).
  They diverge only on symlinks: `--source` preserves them, a built bundle dereferences them into
  real files. Covered by `tests/sources.test.sh` (S6).
- `--source` fails closed if the merged hook wiring references a `.omakase/*.sh` script neither
  the custom harness nor the base harness ships — refusing at install (placing nothing) instead of
  dying with a cryptic exit-127 at commit time (the same wiring guard `tools/build.sh` applies to a
  bundle). Covered by `tests/sources.test.sh` (S7).

### Changed
- **Plugin renamed `omakase-harness` → `omakase`** (the plugin identity only): install is now
  `plugin install omakase@omakase`, and the skills read `/omakase:<verb>` on Claude Code. The
  repo name, the `.git/info/exclude` markers, and the harness banner stay `omakase-harness`
  (on-disk contracts).
- User-facing nudges now use host-neutral phrasing — `omakase init` / `omakase status` /
  `omakase remove` (was the slash form `/omakase init`, `/omakase show`); the inspect verb is now
  `status` (it still calls `bin/show.sh`).
- Mascot: the default status icon is now 🥡 (was 🍣); still overridable with `OMAKASE_ICON`.
- Docs terminology: the tool you install once is now the **omakase base harness** (was "the
  engine"), and a personal harness you point `--source` at is a **custom harness** (was
  "a source"). This mirrors the base/custom layering the install actually performs. Wording
  only — the `--source` flag and all behaviour are unchanged.
- The end-of-turn **Stop-hook notice is now opt-in** (was wired on by every install). It does no
  enforcement — it only prints a one-line "harness active / last run" status — and is Claude
  Code-only, so the base payload no longer ships `.claude/settings.json`; `init` prints how to
  enable it, and `omakase status` shows the same detail on demand. Leaner default install.
- The cosmetic commit **banner is no longer auto-wired** into the shipped hook configs; lefthook's
  own run header stands by default. The `omakase-banner.sh` script still ships (terminal `omakase
  status` uses it) and the base `lefthook-local.yml` documents how to re-enable the branded box.

### Fixed
- The base+source merge runs through a temp staging dir cleaned on any exit; its cleanup trap
  returns 0 so a bare (non-`--source`) `init` can never inherit a non-zero exit from it.

## [0.16.0] — 2026-06-22

### Changed
- `/omakase show` no longer lists omakase's own machinery under `.omakase/` in the Injected
  group, and the "Inventory" umbrella heading is dropped — Committed / Injected / Personal are
  now peer sections. Active gates still appear under Guards; `.omakase/` is still disclosed in
  the Hidden-via-exclude section.
- The end-of-turn Stop-hook notice tracks deployment ("<name> is active" / "is not active")
  rather than the last run's result; a failed run keeps the active header and reports the
  failure in words, with no X glyph.

### Fixed
- **Data loss (high):** `remove` no longer deletes the user's own untracked files in a repo
  that never installed omakase — the no-ledger fallback is now gated on a proof-of-install
  sentinel. `init`/`import` no longer write payload content *through* an existing destination
  symlink (clobbering an out-of-tree file); a dangling dest symlink no longer aborts the install.
- The generated fail-closed `verify-overlay` guard no longer fails open on a truncated ledger.
- Deferred gates fail closed (not silently skip) when `OMAKASE_GLOB` is unset or when the diff
  range has no merge base (two-dot fallback). The example gate no longer false-blocks a lone
  `=======` Markdown heading underline.
- `/omakase show`'s Markdown Guards table survives a `|` in a gate command. `build` no longer
  ships `.gitignore`'d junk (`.DS_Store`, `*.bak`) into the dist. Plus BSD/GNU portability and
  ledger exit-code fixes, and broader test coverage.

## [0.15.0] — 2026-06-21

### Added
- Base payload ships the deferred-gate machinery: `omakase-record.sh` (a job records a
  per-commit result) and `deferred-check.sh` (the push gate that blocks unless a fresh
  passing record exists for the commit). Wired as a commented example in `lefthook-local.yml`
  and surfaced in `show`'s GUARDS chart + scorecard; covered end-to-end by
  `tests/deferred-gate.test.sh`. A fork inherits it instead of copying from another harness.

### Changed
- Gate model collapses to two terms: a **gate** (runs in the hook) and a **deferred gate**
  (checks a job ran for the commit). The earlier `live` / `deferred must-pass` /
  `deferred must-run` split and the `producer` term are retired — a deferred gate's
  block-on-failure vs proof-it-ran behavior is now the job's recording policy, not a gate
  type. Reconciled `concepts.md`, `authoring.md`, `README.md`, and the `add-gate` skill,
  which now interviews the user one question at a time to settle the shape.

## [0.14.0] — 2026-06-19

### Added
- `add-gate` skill: an agent-facing walkthrough for wiring a tool, skill, or check to a git
  hook as a gate — picks the gate shape (live / deferred must-pass / deferred must-run),
  pre-flights whether a third-party tool can be gated at all, and shows the wiring (#24).

### Changed
- `show` renders one GUARDS chart with a "run when" column, replacing the separate
  per-hook listings (#23).
- Path classification recognizes Copilot lifecycle hooks (`.github/hooks/`), reusable
  prompt and persona assets (`.github/prompts/`, `.github/chatmodes/`), and Claude agents
  and hooks (`.claude/agents/`, `.claude/hooks/`); an invariant test asserts every known
  harness directory classifies to a concrete kind.

## [0.13.1] — 2026-06-18

### Fixed
- The harness self-heals on a bare `git worktree add`: a new linked worktree re-arms its
  injected files instead of running without them.

## [0.13.0] — 2026-06-17

### Added
- `init` self-provisions lefthook: with no binary on PATH, `LEFTHOOK_BIN`, or
  `node_modules`, it fetches a pinned, checksum-verified binary into a per-machine cache
  instead of exiting (#17).
- Path classification recognizes GitHub Copilot CLI artifacts: `.github/skills/`,
  `.github/instructions/`, `.github/copilot-instructions.md`, and `~/.copilot/` (#18).

## [0.12.0] — 2026-06-12

### Added
- Sources: install a harness from a git source repo with `init --source`, backed by a
  local cache, a manifest, a remembered source, and an orphan sweep on re-install (#16).

## [0.11.0] — 2026-06-12

### Added
- `show` groups the installed inventory by origin: committed, injected, personal.

## [0.10.0] — 2026-06-12

### Added
- Provenance ledger (`placed.tsv`): records the source and content hash of each placed
  file.

## [0.9.0] — 2026-06-11

### Added
- v1 safety fixes and the v1 specification.
