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

## Gates and deferred gates

A gate is a check wired into a git hook. It either runs in the hook or defers to a job
that ran earlier.

A gate runs the check inside the hook and blocks on the result — a linter, a type check,
or a fast test, anything deterministic and quick enough to run while you wait. The hook
runs it; a non-zero exit blocks the commit or push.

A deferred gate is for checks too slow or non-deterministic to run inside a hook, such as
an emulator render or an LLM review. The job runs earlier, when the work is done (the
developer or agent runs it), and records a result keyed to the commit. The deferred gate
runs at push, reads that record, and blocks unless the job recorded success for the exact
commit being pushed. It never runs the check itself.

What counts as success is the job's call: a render-diff records success only when the
output matches, so its gate blocks a broken render; a review records success whenever it
ran, so its gate only enforces that the review happened and leaves the findings for a
human. Same gate either way — the policy lives in the job, not the gate.

## State

A harness writes state as it runs: the installed version, the record of what `init`
placed (`.omakase/placed.tsv`), recorded results, a run ledger. This lives under
`.omakase/` and is gitignored by design. It describes one machine's installation, not the
project, so it is never committed.
