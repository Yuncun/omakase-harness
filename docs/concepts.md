# Concepts

## Base harness and custom harness

**omakase base harness** — the tool you install once. It holds the install/remove logic
(`bin/`), the base machinery every harness can rely on (the gate runner and its scorecard
ledger, the status banner, the worktree guard — all inside the `omakase` binary), and the
`omakase` commands. This repo is the base harness.

**custom harness** — a personal harness you make and share: a git repo with a `payload/`
tree whose `payload/omakase.manifest` is its one manifest. You install it with
`omakase init owner/repo[/subpath]` (or `--source <git-url|path>`), and the base harness
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

### Why not just copy the files in — or ship a plugin?

Three things get compared to the overlay, and only two of them are the same kind
of alternative.

**`cp -R` + exclude.** The placement half is reproducible by hand: copy the
payload onto the repo, append its paths to `.git/info/exclude`, `git status`
comes back clean.

**A minimal placer.** A few hundred lines of shell doing the same with a hash
ledger on top, so it can also skip already-tracked paths, retract what a later
version dropped, and uninstall exactly what it placed. These deliberately scope
out enforcement rather than fight a hook manager for `.git/hooks`.

**A plugin.** A different axis: it places nothing in the repo. Skills and agent
files live under the host's plugin directory. Plugin hooks are *agent-lifecycle*
hooks (`SessionStart`, `PreToolUse`) — they fire when the agent acts, not when a
commit happens.

|  | `cp -R` | placer | plugin | omakase |
| --- | :---: | :---: | :---: | :---: |
| Place files, zero committed footprint | ✅ | ✅ | — | ✅ |
| Leaves a path the project already committed alone | ❌ | ✅ | ✅ | ✅ |
| Removes what a later version dropped | ❌ | ✅ | ✅ | ✅ |
| Uninstall removes only what it added | ❌ | ✅ | ✅ | ✅ |
| Reaches new clones and worktrees with no per-repo step | ❌ | ❌ | ✅ | ❌ |
| Updates everywhere at once | ❌ | ❌ | ✅ | ❌ |
| Files land where non-agent tools read them | ✅ | ✅ | ❌ | ✅ |
| Applies to plain `git`, an IDE, or someone else's agent | ❌ | ❌ | ❌ | ✅ |
| Works with no agent host installed | ✅ | ✅ | ❌ | ✅ |
| Runs checks on commit and push | ❌ | ❌ | ❌ | ✅ |
| Per-gate bypass, disable, and a pass/fail record | ❌ | ❌ | ❌ | ✅ |

**If a harness is only agent files, prefer a plugin.** On a host with per-project
plugin scopes it is strictly the better tool: it updates everywhere at once and
costs nothing per clone, where an overlay needs an `init` in every checkout.
Host support for scoping varies — Claude Code has project, per-user-per-repo,
and user scopes; other hosts resolve plugin settings from the user directory
only, so a plugin there loads in every repo you open. Check your host before
assuming a plugin can be confined to one project.

The two are not rivals. A harness can place a project-scoped settings file that
*enables* a plugin — the overlay becomes the delivery mechanism, and the plugin
still does the loading.

**The overlay earns its place on the bottom five rows**, and they are all one
idea: the file has to exist on disk, at a real path, for something that is not
your agent. A `lefthook.yml` or a lint config is read by a tool. A gate script is
run by git. Those cannot live in a plugin directory, and a commit made from an
IDE or by a teammate who runs no agent still has to be governed.

One more row is worth expanding, because it is the only case where the cheap
option damages the project rather than the overlay: if a harness ships
`CLAUDE.md` and the project has a committed `CLAUDE.md`, `cp -R` overwrites it
and nothing warns you. It surfaces later as a modification to tracked code that
nobody made.

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
picks the stage (`pre-commit` or `pre-push`). Three optional keys extend the behavior:

- `cacheable: true`: once the `run:` passes for a given commit, subsequent runs at that
  commit skip it. Use for expensive checks, or when a check runs out of band: the `run:`
  blocks the push, the check runs separately (by an agent or developer), and when it passes
  it calls `omakase record <name>` to record the result. The re-push at the same commit is
  then allowed.
- `glob: <pats>`: space-separated path globs; the gate is skipped when no changed file
  matches.
- `purpose: <words>`: what the gate enforces, in the author's words (≤6 words, concrete).
  `omakase status` shows it as the guards table's ENFORCES column.

Gates are optional. A manifest that declares none makes a steering-only harness:
`init` installs no enforcement hooks at all, so it coexists with a repo's existing hook
manager (husky, pre-commit) instead of refusing — there is nothing to conflict over.
Declaring the first gate brings the hooks, and the incumbent-hook refusal, back.

Every run appends to the scorecard, visible in `omakase status`. Audited bypasses exist:
`OMAKASE_SKIP_<NAME>=1` (name upper-cased, `.`/`-`→`_`) skips one gate for one git command,
`OMAKASE_SKIP_GATES=1` skips every gate for one git command, and a persistent per-gate
toggle (`omakase status --disable <gate>`) records
the gate in the git dir's `omakase/disabled-gates` until re-enabled. All announce the skip
on every hook run — a bypassed gate is never silent.

## State

A harness writes state as it runs: the installed version, the record of what `init`
placed (`.omakase/placed.tsv`), recorded results, a run ledger. This lives under
`.omakase/` and is gitignored by design. It describes one machine's installation, not the
project, so it is never committed.
