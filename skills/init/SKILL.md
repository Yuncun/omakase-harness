---
name: init
description: Overlay an omakase harness onto the current repo (agent instructions, lint config, git-hook gates) with zero committed footprint — placed files run from the working tree but are registered in .git/info/exclude, never entering git history. Use when asked to "init omakase", "set up / install / arm the harness", "overlay a harness onto this repo", or to adopt a published harness ("omakase init owner/repo"). A bare init refreshes the remembered harness.
allowed-tools: Bash(*/run.sh*) Bash(*/bin/init.sh*)
---

# /omakase:init — overlay a harness (zero committed footprint)

Install a harness onto the current git repo: copy a payload tree onto real paths, record every
placed path in `.git/info/exclude` (nothing committed, `.gitignore` untouched), and install one
git-hook dispatcher per hook so omakase runs the harness's manifest-declared gates itself (no
third-party runner). `/omakase:remove` reverses it.

Run this skill's self-locating `run.sh` — it finds the base harness's `bin/` and operates on the
current repo. The argument selects the mode:

```bash
bash "${CLAUDE_PLUGIN_ROOT}/skills/init/run.sh"                      # bare: refresh / re-overlay
bash "${CLAUDE_PLUGIN_ROOT}/skills/init/run.sh" alice/harness        # adopt a published harness (GitHub shorthand)
bash "${CLAUDE_PLUGIN_ROOT}/skills/init/run.sh" alice/harness#v1     # ...pinned to a branch or tag
bash "${CLAUDE_PLUGIN_ROOT}/skills/init/run.sh" --source <url|path>  # ...any git URL or local clone
```

(On Copilot CLI or a plain shell, run this skill directory's `run.sh` with the same args.)

## What each mode does

**Bare init** overlays the payload (the remembered source, or the base payload). It **skips any
path the repo already tracks** (never overwrites a committed file), **overwrites an injected file
that differs from payload** (warning that a local edit was replaced), and **removes a previously
injected file the payload no longer ships** (only when untouched). Tell the user which files were
placed / overwritten / skipped / removed, and that `/omakase:remove` undoes it.

**Adopting `owner/repo`** (or `--source`) pulls a **custom harness** (a git repo with a `payload/`
tree whose `payload/omakase.manifest` is its one manifest) into a local cache, then overlays base machinery underneath with
the custom harness's payload winning on overlap. The source is remembered; a later bare init
refreshes it. If the manifest declares `recommends:`, init prints it once — relay it.

## Guardrails (do not override)

- **Refusals — relay verbatim and STOP.** init refuses (placing nothing) on a bad source (no
  `payload/`, no `payload/omakase.manifest`, a leftover source-root `omakase.manifest` (the
  pre-consolidation two-file layout — move its keys into `payload/omakase.manifest`), a payload that
  still ships `lefthook-local.yml`, or a gate whose `run:` names a payload script neither side
  ships) and on an incumbent hook manager (husky, pre-commit, a foreign `core.hooksPath`, or any
  existing git hooks — including a project's own **native lefthook**, which omakase no longer
  cooperates with). Do not delete the incumbent's files or force config — that is the user's call.
- **Committed files (skipped).** NEVER run `git rm --cached` or set `OMAKASE_CUTOVER_CONFIRM=1`
  yourself; cutting over stages deletions of shared files that the next commit applies for everyone.
  Surface the skip report and run the guarded `init.sh --cut-over` only if the user explicitly asks.
- **Upstream collision.** If init prints an upstream-collision WARNING (an injected path is now
  tracked by the repo), relay it verbatim — the named preserved-copy path holds the user's version.
