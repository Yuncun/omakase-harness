---
name: init
description: Overlay an omakase harness onto the current repo — agent instructions, lint config, and git-hook gates — with zero committed footprint (files run from the working tree but are registered in .git/info/exclude, never entering git history). Use when asked to "init omakase", "set up / install / arm the harness", "overlay a harness onto this repo", or to adopt a published harness ("omakase init owner/repo"). A bare init refreshes the remembered harness.
allowed-tools: Bash(*/run.sh*) Bash(*/bin/init.sh*)
---

# /omakase:init — overlay a harness (zero committed footprint)

Install a harness onto the current git repo: copy a payload tree onto real paths, record
every placed path in `.git/info/exclude` (nothing is committed, `.gitignore` untouched), and
install lefthook to run the gates. `/omakase:remove` reverses it.

Run this skill's self-locating `run.sh` (it finds the base harness's `bin/` and operates on
the current repo). On Claude Code:

```bash
bash "${CLAUDE_PLUGIN_ROOT}/skills/init/run.sh" [args]
```

On Copilot CLI or a plain shell, run this skill directory's `run.sh` with the same args.

## Bare init — refresh / re-overlay

```bash
bash "${CLAUDE_PLUGIN_ROOT}/skills/init/run.sh"
```

Overlays the payload (the remembered source, or the base payload). It **skips any path the
repo already tracks** (never overwrites a committed file), **overwrites an injected file that
differs from payload** (warning that a local edit was replaced), and **removes a previously
injected file the payload no longer ships** (only when untouched). Records placed paths in
`.git/info/exclude` and runs `lefthook install`. Nothing is committed. Tell the user which
files were placed / overwritten / skipped / removed, and that `/omakase:remove` undoes it.

## Adopt a published harness — `owner/repo`

```bash
bash "${CLAUDE_PLUGIN_ROOT}/skills/init/run.sh" alice/harness        # GitHub shorthand
bash "${CLAUDE_PLUGIN_ROOT}/skills/init/run.sh" alice/harness#v1     # pin a branch or tag
bash "${CLAUDE_PLUGIN_ROOT}/skills/init/run.sh" --source <url|path>  # any git URL or clone
```

Pulls a **custom harness** (a git repo with a `payload/` tree plus an `omakase.manifest`)
into a local cache and overlays the base harness's payload with the custom harness's payload
on top — base machinery underneath, the custom harness's delta winning on overlap. The custom
harness is remembered: a later bare init refreshes and re-injects it.

If the harness declares `recommends:` in its manifest, init prints it once — relay it. If init
**refuses** the source (no `payload/`, no `omakase.manifest`, or merged hook wiring that
references a `.omakase/*.sh` script neither side ships), relay the refusal verbatim and STOP —
nothing was placed.

## Guardrails (do not override)

- **Incumbent hook manager.** If init refuses because husky, pre-commit, a foreign
  `core.hooksPath`, or non-lefthook hooks are present, relay the refusal verbatim and STOP.
  Do not delete the incumbent's files or force config — that is the user's call.
- **Committed files (skipped).** For paths reported **skipped (committed)**, NEVER run
  `git rm --cached` or set `OMAKASE_CUTOVER_CONFIRM=1` yourself; cutting over stages deletions
  of shared files that the next commit applies for everyone. Surface the skip report and run
  the guarded `init.sh --cut-over` only if the user explicitly asks.
- **Upstream collision.** If init prints an upstream-collision WARNING (an injected path is
  now tracked by the repo), relay it verbatim — the named preserved-copy path holds the user's
  version.
- **lefthook fetch.** init self-provisions a pinned, checksum-verified lefthook into a
  per-machine cache if none is on PATH. Only if that fetch fails does init stop with "lefthook
  not found and could not be fetched" — then ask how the user wants lefthook (`brew install
  lefthook`, `mise use lefthook`, or a project devDependency), run their choice, and re-run.
  The repo is never touched by the fetch, so `/omakase:remove` need not undo it.
