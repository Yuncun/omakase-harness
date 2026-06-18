# Concepts

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

## Gates and producers

A gate is a check wired into a git hook. omakase splits a gate into two parts.

A producer runs when the work is done. It performs the check — a test run, a render, a
review — and records a verdict for the current commit. The developer or agent runs it.

The gate runs at push. It reads the recorded verdict for the commit being pushed and
blocks if the verdict is missing or failing. It does not perform the check.

The split exists for slow or non-deterministic checks, such as an emulator render or an
LLM review: running them inside the push hook would be slow and unrepeatable, so the
verdict is recorded once when the work is done and only read at push.

A pure check, such as a linter or a type check, needs no producer. It runs directly in
the hook against files already in the tree.

## State

A harness writes state as it runs: the installed version, the record of what `init`
placed (`.omakase/placed.tsv`), recorded verdicts, a run ledger. This lives under
`.omakase/` and is gitignored by design. It describes one machine's installation, not the
project, so it is never committed.
