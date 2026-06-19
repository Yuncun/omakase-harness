# Authoring a harness

A harness is a git repository with a `payload/` tree. `payload/` is copied verbatim into
a target on install; everything else in the repo (README, tests, `bin/`) stays in the
harness. To install a harness from a URL or path with `--source`, it also needs an
`omakase.manifest` at the root (see [Reference](reference.md#manifest)).

Start from this repo or an existing harness, edit `payload/`, and publish. To capture the
harness already living inside a project, run `bin/import.sh /path/to/project`, which reads
that project's harness files into `payload/` and leaves the project untouched.

## Adding a gate

A gate has two parts (see [Concepts](concepts.md#gates-and-producers)):

1. A producer that runs the check and records a verdict for the commit. For a pure check
   such as a linter, the hook command is the producer.
2. A hook entry in `payload/lefthook-local.yml` that runs the producer on commit, and the
   gate that reads the recorded verdict on push.

The deferred-gate scripts under `.omakase/` (the verdict recorder and the push gate) are
reusable. A new gate supplies its own producer and points the gate at its verdict by name.

## Wrapping a third-party check

To gate on a review or test skill you do not own: install it as a dependency, then write
a thin producer that runs it, maps its output to pass or fail, and records the verdict.
You own the threshold for what counts as failing; the upstream skill stays unmodified. Do
not copy it into `payload/`. Depend on it and invoke it.

## A behavioral payload (no gate)

A payload need not enforce anything. It can ship **agent guidance** — a rule or
instruction the AI assistant reads at session start, with no hook behind it. Place it
where the agent looks: `payload/.claude/rules/<name>.md` for Claude Code, or
`payload/.github/instructions/<name>.instructions.md` (with `applyTo:`) for Copilot.
omakase injects it like any other file; nothing is committed, and `remove` deletes it.

This is the opt-in alternative to a personal `~/.claude/CLAUDE.md` rule: a harness
payload is **shareable**, so anyone who wants the same guidance installs it and everyone
else is untouched. See [`examples/pr-discipline`](../examples/pr-discipline) for a
worked example.

## Pitfalls

**Edit the source, not the installed copy.** An edit to an installed file in a target
repo is overwritten the next time `init` runs, because `init` makes the target match
`payload/`. Durable changes go in the harness repo's `payload/`, followed by a
re-install.

**A plugin's files are read-only.** A harness distributed as a Claude Code plugin lives in
a cache that is replaced on every update, so it cannot be edited there. Clone the harness
repo, edit `payload/`, and install from the clone. `placed.tsv` and `show.sh` record the
source of each installed file, so the active source is always inspectable. Do not install
from both the plugin and a clone into one repo.

**Owned directories are gitignored wholesale.** A file a gate or producer writes under
`.omakase/` or `.claude/` is invisible to git and never reaches a teammate. That is
correct for machinery and per-machine state. Content the team must share — test specs,
fixtures, recorded flows — belongs in the project's own committed tree, with the gate's
config pointing at it. A test that lives only in an ignored directory runs only on the
machine that wrote it.

**`init` skips tracked files.** It never overwrites a file the project commits. To replace
a committed file with the harness copy, use `init.sh --cut-over`, which is guarded and
requires explicit confirmation. Do not run `git rm --cached` by hand: it stages a deletion
that the next commit applies for everyone.

**`.github/` is excluded file-by-file.** Files placed there are ignored individually, so
the project's own `.github` contents stay visible. It is the one shared directory;
everything else omakase places is owned.

## Publishing

A harness installs from any git URL:

    init.sh --source https://github.com/you/your-harness

The manifest needs a `name`; `version` is optional. Distributing as a Claude Code plugin
adds the `/omakase` wrapper over the same scripts; see the plugin and marketplace docs
linked from the README.
