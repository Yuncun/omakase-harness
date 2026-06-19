# Changelog

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the
project uses semantic versioning. Versions before 0.9.0 are in the git history.

## [Unreleased]

### Added
- `add-gate` skill: an agent-facing walkthrough for wiring a tool, skill, or check to a git
  hook as a gate — picks the gate shape (live / deferred must-pass / deferred must-run),
  pre-flights whether a third-party tool can be gated at all, and shows the wiring.

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
