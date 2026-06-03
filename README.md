# Omakase Harness

A base harness shared as a Claude Code plugin and customized per project. It solves the
adoptability question: how do you introduce a harness into an existing project without
requiring a greenfield repo? Answer: a plugin that **injects** your harness into your
project and **gitignores the injected files**, so it is personal — it affects only you,
never the committed repo or your teammates.

The base does only two things:

- Initialize the harness via a script.
- Enforce gates on your git hooks (via [lefthook](https://lefthook.dev)).

The creator drops the gates and tools they want into the base and distributes it to
adopters. The base itself ships no rule, doc, or gate — only the mechanism.

## Usage

From a Claude Code session in any git repo:

```
/omakase-init     # overlay the payload into this repo (gitignored) + install hooks
/omakase-remove   # reverse it
```

`/omakase-init` copies the plugin's `payload/` tree to real paths in your repo, skips any
path the repo already tracks (it never overwrites a committed file), records every placed
path in `.git/info/exclude`, and runs `lefthook install`. Nothing is committed — zero
footprint. The harness is additive: to layer instructions over a committed
`AGENTS.md`/`CLAUDE.md`, ship them as `CLAUDE.local.md`; to add rules, drop new files into
`.claude/rules/`.

Requires the `lefthook` binary on PATH (`brew install lefthook`, `mise use lefthook`, or a
devDependency).

### Worktrees self-arm

The injected files are gitignored, so a fresh `git worktree` would normally not have them.
`init.sh` handles this: it snapshots the placed files into the shared git dir and installs a
`post-checkout` job that copies any **missing** ones into each worktree (ensure-present /
never-overwrite — it self-heals deleted files and never clobbers a local edit). It also writes
a `.worktreeinclude` block so Claude Code copies the harness into worktrees it creates. Manual
`git worktree add` from a never-armed clone is the one gap — re-run `/omakase-init` there.

## Project structure

- `bin/init.sh` — overlays `payload/` additively, writes `.git/info/exclude`, installs lefthook.
- `bin/remove.sh` — reverses init.
- `commands/` — the `/omakase-init` and `/omakase-remove` slash commands.
- `payload/` — the harness content you ship: `lefthook-local.yml` wiring + `.omakase/gates/`.
  Replace the example gate with your own. To customize, fork the plugin and edit `payload/`.

## Example payload: a web project's harness

The base ships only the mechanism and one example gate. As a fuller example, here is the
payload I run for pixterm (a web project) — the kind of tree a creator drops into
`payload/`:

| Piece | Kind | What it does |
| ----- | ---- | ------------ |
| `worktree-discipline` | pre-commit guard | Blocks a main-checkout commit that would inherit another worktree's uncommitted work. Pure git; dormant unless more than one worktree is active. |
| `adr-required` | pre-commit guard | Requires a paired decision record when a declared architectural file changes. Dormant until you name the files. |
| `deferred-check` + `omakase-record` | deferred-gate scaffold | Enforces a verdict a hook can't compute itself — an LLM judge, a slow flow, a human sign-off. A producer records a pass/fail; the hook confirms a fresh pass for the pushed code. |
| `visual-verify` | skill | Best-effort visual check: drives the running app, judges screenshots, records a verdict for the deferred gate. Needs a per-project driver. |

The guards are stack-agnostic and dormant until relevant. Your own stack gates (typecheck,
test, lint, build) are yours to declare — the base ships none.

## Contributing

The base (`bin/`, `commands/`) is mechanism and stays content-free — it hardcodes no rule,
doc, or gate. Harness content belongs in `payload/`. Run `bash tests/inject.test.sh` before
sending changes; it must print `ALL PASS`.

## License

MIT. See `LICENSE`.
