# Authoring a custom harness

A custom harness is a git repository with a `payload/` tree. `payload/` is copied onto a target
on install; everything else in the repo (README, tests, `bin/`) stays in the custom harness. To
install one from a URL or path with `--source`, it also needs an `omakase.manifest` at the root
(see [Reference](reference.md#manifest)).

A `--source` install layers the omakase **base harness's payload** under your `payload/` (your
delta wins on overlap), so you ship only your delta and **rely on base machinery without keeping
your own copy** — the banner, the `omakase-ledger.sh` scorecard wrapper, the `omakase-record.sh`
recorder, and the `deferred-check.sh` push gate are all provided by the base harness. Wire them in
`payload/lefthook-local.yml` and ship only your own gates. (This is
the same base+delta merge `tools/build.sh` bakes into a plugin bundle, performed at install
time instead.) If your wiring references a `.omakase/*.sh` neither you nor the base harness ships,
`init` refuses and places nothing — so a typo surfaces at install, not as an exit-127 on commit.

Start from the base harness repo or an existing custom harness, edit `payload/`, and publish. To
capture the harness files already living inside a project, run `bin/import.sh /path/to/project`,
which reads that project's harness files into `payload/` and leaves the project untouched.

## Adding a gate

The `add-gate` skill walks an agent through this end-to-end — picking the gate shape,
pre-flighting whether a third-party tool can even be gated, and wiring it. This section is the
conceptual reference behind it.

omakase has two kinds of gate (see [Concepts](concepts.md#gates-and-deferred-gates)):

- A **gate** runs in the hook. The hook command is the whole gate — for a linter or a
  test, lefthook runs it and a non-zero exit blocks.
- A **deferred gate** is two pieces: a job that runs the check in-session and records a
  result keyed to the commit, and a hook entry in `payload/lefthook-local.yml` that reads
  that record on push and blocks unless the job recorded success for the commit.

The deferred-gate scripts under `.omakase/` (the recorder and the push gate) are reusable.
A new deferred gate supplies its own job and points the gate at its record by name.

## Wrapping a third-party check

To gate on a review or test skill you do not own: install it as a dependency, then write
a thin job that runs it, maps its output to success or failure, and records the result.
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
else is untouched.

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

**Owned directories are gitignored wholesale.** A file a gate or its job writes under
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
