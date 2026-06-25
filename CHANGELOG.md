# Changelog

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the
project uses semantic versioning. Versions before 0.9.0 are in the git history.

## [Unreleased]

### Added
- `examples/sample-harness/` — a minimal worked custom harness (one rule, one gate, the wiring)
  to read, try, and copy. It ships only its delta and relies on the base harness machinery layered
  in at install, so it doubles as a live demonstration of the base+source merge. Covered end-to-end
  by `tests/sample-harness.test.sh` (copy into a repo → `init --source` → gate fires → remove).
- A `.claude-plugin/marketplace.json` so the repo is itself an installable marketplace: the
  documented `plugin marketplace add yuncun/omakase-harness` + `plugin install
  omakase-harness@omakase` now resolves (the plugin's `source` is the repo root, `"./"`).
  Without it those install lines had nothing to fetch.
- A generic Copilot CLI `/omakase` management skill (`skills/omakase/`) — the host-agnostic
  front door (show / init / remove, including `--source`) that mirrors the Claude `/omakase`
  command, via a self-locating `run.sh` dispatcher that finds the base harness's `bin/` from its
  own install location. Ships in the plugin bundle, so a custom harness no longer keeps its own
  copy of the management skill — the base harness carries it once.
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
- Docs terminology: the tool you install once is now the **omakase base harness** (was "the
  engine"), and a personal harness you point `--source` at is a **custom harness** (was
  "a source"). This mirrors the base/custom layering the install actually performs. Wording
  only — the `--source` flag and all behaviour are unchanged.

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
