# Authoring a custom harness

A custom harness is a `payload/` tree whose `payload/omakase.manifest` is the harness's one
manifest — its identity (`name`, `version`; see [Reference](reference.md#manifest)) and its
gate wiring — kept in a git repository, at the repo root or in a subfolder of a repo that holds
other things too. `payload/` is copied onto a target on install; everything else (README, tests) stays in the custom harness.

A `--source` install layers the omakase **base harness's payload** under your `payload/` (your
delta wins on overlap), so you ship only your delta and **rely on base machinery without keeping
your own copy** — the gate runner and the status banner live in the `omakase` binary
itself. Declare your gates
as `gate:` blocks in `payload/omakase.manifest` — the one manifest, placed and snapshotted (see
[Reference](reference.md#manifest)) — and ship only your own gate scripts. If a gate's `run:` names a payload script (`.omakase/…` or `gates/…`)
neither you nor the base harness ships, `init` refuses and places nothing — so a typo surfaces
at install, not as an exit-127 on commit.

Start from the base harness repo or an existing custom harness, edit `payload/`, and publish. The
worked example is [`omakase-harness-harness/`](../omakase-harness-harness/) — the harness
omakase's own development runs: placed agent rules, two pre-commit gates, a cached pre-push
test gate, and the `omakase.manifest` that declares them. Try it with
`omakase init Yuncun/omakase-harness/omakase-harness-harness`, then copy it and swap in your
own rules and gates. There is no capture
tool: build `payload/` and its one `omakase.manifest` (identity + gate wiring) by hand, moving
in whatever files a project already has in place. The `author` skill walks an agent through
exactly that, end to end; this document is the conceptual reference behind it.

## Public surface (the stability contract)

The stable surface a custom harness authors against is the **`omakase.manifest` schema** —
the `gate:` block and its keys (`hook:`, `run:`, `glob:`, `cacheable:`, `purpose:`), and
the `advisory:` block and its keys (`run:`, `purpose:`; see
[Reference](reference.md#manifest)). Those key names and their meanings will not be renamed
or repurposed out from under your manifest; anything else is an internal refactor you never
see.

The product UX is **built into the binary, not placed scripts**: the branded box
opening `omakase status` is rendered by the binary (swap the glyph with
`OMAKASE_ICON`), and the status-bar segment is a binary subcommand,
`omakase statusline` (wired into the hosts' bars at init;
`omakase statusline --wire` re-wires by hand — see [Reference](reference.md)).
Both probe the shared ledger and hooks, so a custom harness gets them for free
and ships none of it.

**Policy**, by contrast, ships in a harness. Anything that steers how people or
agents work — editor hooks, workflow rules, discipline scripts — is payload
content you place and recommend, never an omakase feature. The worked example is
the worktree guard in [`omakase-harness-harness/`](../omakase-harness-harness/): a PreToolUse script this repo's
own harness ships (with a `recommends:` line teaching the wiring) that omakase
itself knows nothing about.

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
- `glob:` — space-separated path globs; the gate runs only when a matching file is in
  what the hook is guarding: the STAGED files on `pre-commit`, the files in the pushed
  range on `pre-push`. When the scope cannot be determined the gate runs unscoped rather
  than skipping. Note the pre-commit scope covers exactly what `git commit` stages —
  content landed by a rebase, cherry-pick, or clean merge fires no pre-commit at all, so
  a path-scoped policy that must catch those needs a `pre-push` gate too.
- `purpose:` — what the gate enforces, in your words (≤6 words, concrete — "tests green
  before push"). Shown as the ENFORCES column of the status guards table.

A worked example — block staged merge-conflict markers. Save it as
`payload/.omakase/gates/markers.sh` and declare it in `payload/omakase.manifest`:

```bash
#!/usr/bin/env bash
# Blocks a commit that stages an unresolved merge-conflict marker. Fully
# generic: depends on nothing but git. Exit non-zero to block; 0 to allow.
set -euo pipefail

# A real conflict always writes the <<<<<<< / >>>>>>> pair, each with a
# trailing ref label. Deliberately do NOT match a bare ======= line: that is
# also a Markdown/RST heading underline and would false-block.
fail=0
while IFS= read -r f; do
  [ -f "$f" ] || continue
  if grep -nE '^(<<<<<<<|>>>>>>>)([[:space:]]|$)' "$f" >/dev/null 2>&1; then
    echo "markers: unresolved merge-conflict marker in $f" >&2
    fail=1
  fi
done < <(git diff --cached --name-only --diff-filter=ACM)
[ "$fail" -eq 0 ] || exit 1
```

```
gate: markers
  hook: pre-commit
  run: .omakase/gates/markers.sh
  purpose: merge-conflict markers stay out
```

## Adding an advisory check

An advisory is a gate's opposite number: it runs when a session starts (not at a git
hook) and can never block anything. Use it for "you should know this before you start"
conditions — a branch that is behind its upstream, a detached HEAD — where the cost of
not knowing lands later as rebase conflicts or work on the wrong base.

One `advisory:` block per check in `payload/omakase.manifest`:

```
advisory: branch-freshness
  run: .omakase/advisories/branch-freshness.sh
  purpose: warn when the branch is behind
```

- `run:` (required) — a command line run via `sh` from the repo root at every session
  start. The exit code is ignored; stdout and stderr pass straight through to the
  session.
- `purpose:` (optional) — what the check watches, in your words.

There is no `hook:`, `glob:`, or `cacheable:` — the stage is fixed and nothing is scoped
or cached because nothing is enforced. The contract for the script: **silent when there
is nothing to say**, one or two lines when there is, and fast — it runs at every session
start, and the hosts' own hook timeout is the only backstop. `init` names every declared
advisory at consent time, and a `run:` pointing at a payload script the harness does not
ship refuses the whole harness, same as a gate.

One honesty rule worth copying into any freshness-style check: comparing against an
unfetched remote ref under-reports staleness. Either fetch first or say which ref you
compared against — never print a confident wrong number.

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

**An installed source's cache is read-only.** A harness installed by `owner/repo` lives in
a local cache that is replaced on every refresh, so it cannot be edited there. Clone the
harness repo, edit `payload/`, and install from the clone. The remembered source is recorded
per repo and shown by `omakase status`, so the active source is always inspectable.

**Owned directories are gitignored wholesale.** A file a gate (or the command it runs) writes
under `.omakase/` or `.claude/` is invisible to git and never reaches a teammate. That is
correct for machinery and per-machine state. Content the team must share — test specs,
fixtures, recorded flows — belongs in the project's own committed tree, with the gate's
config pointing at it. A test that lives only in an ignored directory runs only on the
machine that wrote it.

**`init` skips tracked files.** It never overwrites a file the project commits. To replace
a committed file with the harness copy, use `omakase init --cut-over`, which is guarded and
requires explicit confirmation. Do not run `git rm --cached` by hand: it stages a deletion
that the next commit applies for everyone.

**`.github/` is excluded file-by-file.** Files placed there are ignored individually, so
the project's own `.github` contents stay visible. It is the one shared directory;
everything else omakase places is owned.

## Publishing

A harness installs from any git URL:

    omakase init --source https://github.com/you/your-harness

It does not need a repository of its own — a subfolder of a repo you already have works:

    omakase init you/your-repo/path/to/harness
    omakase init --source https://github.com/you/your-repo//path/to/harness

The `//` marks where the repo ends and the subfolder begins; `payload/omakase.manifest` sits
inside that subfolder's `payload/`.

The manifest needs a `name`; `version` is optional. Adopters need nothing beyond the
omakase binary itself — the install commands are in the
[README's Install section](../README.md#install).
