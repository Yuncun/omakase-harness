# Authoring a custom harness

A custom harness is a git repository with a `payload/` tree. `payload/` is copied onto a target
on install; everything else in the repo (README, tests, `bin/`) stays in the custom harness. To
install one from a URL or path with `--source`, it also needs an `omakase.manifest` at the root
(see [Reference](reference.md#manifest)).

A `--source` install layers the omakase **base harness's payload** under your `payload/` (your
delta wins on overlap), so you ship only your delta and **rely on base machinery without keeping
your own copy**: the banner and the `omakase-gate.sh` primitive are provided by the base harness. Wire the
primitive into `payload/lefthook-local.yml` and ship only your own gates. If your
wiring references a `.omakase/*.sh` neither you nor the base harness ships,
`init` refuses and places nothing — so a typo surfaces at install, not as an exit-127 on commit.

Start from the base harness repo or an existing custom harness, edit `payload/`, and publish. The
smallest worked example is [`examples/sample-harness/`](../examples/sample-harness/) — one rule,
one gate, and the wiring; copy it and edit the three files under `payload/`. There is no capture
tool: build `payload/` and `omakase.manifest` by hand, moving in whatever files a project already
has in place.

## Public surface (the stability contract)

The base harness exposes exactly **one stable primitive a custom harness may reference:
`.omakase/bin/omakase-gate.sh`**. Its name and its CLI grammar — `<name>` followed by
`--step` / `--cacheable` / `--glob` / `--record` — are the contract. They will not be renamed
or repurposed out from under your wiring; anything else is an internal refactor you never see.

The other base scripts are **optional UX, opt-in, and not part of the contract**:
`omakase-banner.sh` (the branded box) and `omakase-worktree-guard.sh` (a Claude Code
PreToolUse hook that denies edits to product files in the main checkout while other
worktrees are active — the pre-edit half of worktree discipline; a commit-time allowlist
gate is the fail-closed half). Wire them only if you want them — skip them and your
harness still works. Do not build wiring that depends on their names being stable.
The status-bar segment and the Stop-hook notice are **binary subcommands**, not placed
scripts: `omakase statusline` and `omakase stop-notice` (init prints the wiring). They
probe the shared ledger and hooks, so a custom harness gets them for free.

The install- and build-time wiring guard rejects any wiring that references a `.omakase/*.sh`
the surface does not ship, so a drift between your wiring and the surface **fails closed at
install, not silently at commit time**.

## Adding a gate

The `add-gate` skill walks an agent through this end-to-end: picking the flags, pre-flighting
whether a third-party tool can even be gated, and wiring it. This section is the conceptual
reference behind it.

A gate is one `omakase-gate.sh` call wired into a hook job (see [Concepts](concepts.md#gates)).
Three flags cover the common cases:

- `--step '<cmd>'`: the check. Exit 0 = pass; non-zero blocks the commit or push.
- `--cacheable`: reuse a passing result for the same commit (run it once, then skip). Use
  for expensive steps or for a check that runs out of band: a blocking step refuses the push
  until the check records its own pass via `omakase-gate.sh <name> --record`.
- `--glob '<pats>'`: space-separated path globs; skip the gate when no changed file matches.

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
repo, edit `payload/`, and install from the clone. `placed.tsv` and `status.sh` record the
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
adds the `omakase` skills over the same scripts; the install commands are in the
[README's Install section](../README.md#install).
