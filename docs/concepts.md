# Concepts

## Base harness and custom harness

**omakase base harness** — the tool you install once. It holds the install/remove logic
(`bin/`), the base machinery every harness can rely on (the banner, the gate runner and its
scorecard ledger), and the `omakase` commands. This repo is the base harness.

**custom harness** — a personal harness you make and share: a git repo with a `payload/`
tree whose `payload/omakase.manifest` is its one manifest. You install it with `--source`, and the base harness
layers its machinery underneath your files (your files win on overlap), so a custom harness
ships only your own delta. See [Authoring](authoring.md).

The rest of these docs say just "harness" when the distinction does not matter.

## The overlay

A harness is a `payload/` directory holding the files a project needs for local
enforcement: git hooks, gate scripts, lint config, agent instructions. `init` copies
that tree onto a target repo's root and records every placed path in the target's
`.git/info/exclude`. The files exist on disk, so hooks and agents read them normally.
Git ignores them, so they never stage, commit, or appear in a diff.

A path the target already tracks is skipped, never overwritten. The harness only owns
files the project does not. Replacing a tracked file with the harness copy is a separate,
guarded step (see [Authoring](authoring.md) and `--cut-over`).

## Owned and shared directories

`init` excludes most harness files by their top directory, written once: `.omakase/`,
`.claude/`. These belong entirely to the harness, so the whole directory is excluded.

`.github/` is the exception. Projects keep their own files there, so omakase excludes only
the exact files it placed, not the directory. The set of shared top directories is
`HARNESS_SHARED_TOPDIRS` in `bin/lib-harness-paths.sh`.

The distinction matters when a gate writes files: anything created under an owned
directory is gitignored wholesale and will not reach a teammate.

## Gates

A gate is a check declared as a `gate:` block in `payload/omakase.manifest` and run by the
omakase binary at a git hook. The block names the gate and gives it a `run:` command line, executed
via `sh` from the repo root; exit 0 passes, non-zero blocks the commit or push. `hook:`
picks the stage (`pre-commit` or `pre-push`). Two optional keys extend the behavior:

- `cacheable: true`: once the `run:` passes for a given commit, subsequent runs at that
  commit skip it. Use for expensive checks, or when a check runs out of band: the `run:`
  blocks the push, the check runs separately (by an agent or developer), and when it passes
  it calls `omakase record <name>` to record the result. The re-push at the same commit is
  then allowed.
- `glob: <pats>`: space-separated path globs; the gate is skipped when no changed file
  matches.

Every run appends to the scorecard, visible in `omakase status`. Audited bypasses exist:
`OMAKASE_SKIP_<NAME>=1` (name upper-cased, `.`/`-`→`_`) skips one gate for one git command,
`OMAKASE_SKIP_GATES=1` skips every gate for one git command, and a persistent per-gate
toggle (`omakase status --disable <gate>`, the interactive screen, or the MCP menu) records
the gate in the git dir's `omakase/disabled-gates` until re-enabled. All announce the skip
on every hook run — a bypassed gate is never silent.

## State

A harness writes state as it runs: the installed version, the record of what `init`
placed (`.omakase/placed.tsv`), recorded results, a run ledger. This lives under
`.omakase/` and is gitignored by design. It describes one machine's installation, not the
project, so it is never committed.
