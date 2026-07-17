# Authoring a custom harness

A custom harness is a `payload/` tree whose `payload/omakase.manifest` is the harness's one
manifest — its identity (`name`, `version`; see [Reference](reference.md#manifest)) and its
gate wiring — kept in a git repository, at the repo root or in a subfolder of a repo that holds
other things too. `payload/` is copied onto a target on install; everything else (README, tests,
`bin/`) stays in the custom harness.

A `--source` install layers the omakase **base harness's payload** under your `payload/` (your
delta wins on overlap), so you ship only your delta and **rely on base machinery without keeping
your own copy**: the banner and other optional UX come from the base harness. Declare your gates
as `gate:` blocks in `payload/omakase.manifest` — the one manifest, placed and snapshotted (see
[Reference](reference.md#manifest)) — and ship only your own gate scripts. If a gate's `run:` names a payload script (`.omakase/…` or `gates/…`)
neither you nor the base harness ships, `init` refuses and places nothing — so a typo surfaces
at install, not as an exit-127 on commit.

Start from the base harness repo or an existing custom harness, edit `payload/`, and publish. The
worked example is [`examples/starter-harness/`](../examples/starter-harness/) — the harness
omakase's own development runs: placed agent rules, two pre-commit gates, a cached pre-push
test gate, and the `omakase.manifest` that declares them. Try it with
`omakase init Yuncun/omakase-harness/examples/starter-harness`, then copy it and swap in your
own rules and gates. There is no capture
tool: build `payload/` and its one `omakase.manifest` (identity + gate wiring) by hand, moving
in whatever files a project already has in place.

## Public surface (the stability contract)

The stable surface a custom harness authors against is the **`omakase.manifest` schema** —
the `gate:` block and its keys (`hook:`, `run:`, `glob:`, `cacheable:`; see
[Reference](reference.md#manifest)). Those key names and their meanings will not be renamed
or repurposed out from under your manifest; anything else is an internal refactor you never
see.

The base scripts are **optional UX, opt-in, and not part of the contract**:
`omakase-banner.sh` (the branded box) and `omakase-worktree-guard.sh` (a Claude Code
PreToolUse hook that denies edits to product files in the main checkout while other
worktrees are active — the pre-edit half of worktree discipline; a commit-time allowlist
gate is the fail-closed half). Wire them only if you want them — skip them and your
harness still works. Do not build wiring that depends on their names being stable.
The status-bar segment and the Stop-hook notice are **binary subcommands**, not placed
scripts: `omakase statusline` and `omakase stop-notice` (init prints the wiring). They
probe the shared ledger and hooks, so a custom harness gets them for free.

A gate whose `run:` names a payload script (`.omakase/…` or `gates/…`) is validated at
install: `init` refuses any harness that references a script it does not ship, so a drift
between a gate and the scripts on disk **fails closed at install, not silently at commit
time**.

## Adding a gate

The `add-gate` skill walks an agent through this end-to-end: picking the keys, pre-flighting
whether a third-party tool can even be gated, and wiring it. This section is the conceptual
reference behind it.

A gate is one `gate:` block in `payload/omakase.manifest` (see [Concepts](concepts.md#gates)).
A block opens with `gate: <name>` and carries these keys:

- `hook:` — `pre-commit` or `pre-push` (required): the stage the gate runs at.
- `run:` — the check (required): a command line run via `sh` from the repo root. Exit 0 =
  pass; non-zero blocks the commit or push.
- `cacheable: true` — reuse a passing result for the same commit (run it once, then skip).
  Use for expensive steps or for a check that runs out of band: a blocking `run:` refuses
  the push until the check records its own pass via `omakase record <name>`.
- `glob:` — space-separated path globs; skip the gate when no changed file matches.

## Wrapping a third-party check

To gate on a review or test skill you do not own: install it as a dependency, then write
a thin gate script that runs it, maps its output to success or failure, and records the result.
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
repo, edit `payload/`, and install from the clone. `placed.tsv` and `status.sh` record the
source of each installed file, so the active source is always inspectable. Do not install
from both the plugin and a clone into one repo.

**Owned directories are gitignored wholesale.** A file a gate (or the command it runs) writes
under `.omakase/` or `.claude/` is invisible to git and never reaches a teammate. That is
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

It does not need a repository of its own — a subfolder of a repo you already have works:

    init.sh you/your-repo/path/to/harness
    init.sh --source https://github.com/you/your-repo//path/to/harness

The `//` marks where the repo ends and the subfolder begins; `payload/omakase.manifest` sits
inside that subfolder's `payload/`.

The manifest needs a `name`; `version` is optional. Distributing as a Claude Code plugin
adds the `omakase` skills over the same scripts; the install commands are in the
[README's Install section](../README.md#install).
