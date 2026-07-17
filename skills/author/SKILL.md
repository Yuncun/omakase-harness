---
name: author
description: Build a custom harness — or turn a repo's existing agent files (CLAUDE.md, rules, skills) into one others can install. Use when asked to "make/author a harness", "turn my setup into a harness", "package/publish my agent rules", or "share my setup without committing it to the project". Covers where the harness lives, laying out payload/ and its one manifest, judging what is portable, testing the install, and publishing. Gate wiring hands off to the add-gate skill.
---

# /omakase:author — build a custom harness

A **custom harness** is a git-hosted `payload/` tree whose `payload/omakase.manifest` is its
one manifest — identity (`name`, optional `version` / `recommends`) plus any `gate:` blocks.
`init` copies `payload/` onto a target repo at verbatim paths, keeps every placed file out of
git, and layers the omakase base machinery underneath — so a harness ships only its delta.
You are creating or editing that source repo, NOT an installed overlay (edits to injected
copies are overwritten on the next `init`).

## Step 1 — decide where it lives (ask)

Two placements install identically; ask which the user wants:

1. **A subfolder of a repo they already have** — one hub repo, many harnesses
   (`you/hub/tools-harness` installs the harness at `hub`'s `tools-harness/`). Good default
   for a personal collection; no dedicated repo per harness.
2. **Its own repo** — `omakase init you/your-harness`. Right when the harness is the repo's
   whole point or needs its own issues/releases.

## Step 2 — lay out the skeleton

```
<harness>/
  payload/
    omakase.manifest          # the ONE manifest: identity + gate blocks
    .claude/rules/<name>.md   # ...whatever the harness places
  README.md                   # describes the harness; not placed
```

Manifest stub (flat `key: value` text, no YAML; `name` required):

```
name: <harness-name>
version: 0.1.0
# recommends: <one-line companion-tool hint, printed once at install>
```

Do NOT also create an `omakase.manifest` at the source root — the pre-consolidation
two-file layout is refused at install.

## Step 3 — move content in, judging portability

Paths mirror verbatim: `payload/X` lands at the target's `X`. What belongs:

- **Agent guidance** — `payload/.claude/rules/<name>.md` for Claude Code,
  `payload/.github/instructions/<name>.instructions.md` (with `applyTo:`) for Copilot.
  Root instruction files (`AGENTS.md`, `CLAUDE.md`) are placed verbatim too — ship your own
  file or symlink; omakase never synthesizes one.
- **Skills, editor/lint config, gate scripts** (scripts under `payload/.omakase/gates/`,
  executable).

Review every line you move for portability, with the user: repo-specific commands
(`pnpm test`), local paths, and product names either generalize or stay behind. A harness is
read-to-trust — small and legible beats complete.

Three refusals to honor while collecting:

- **Never copy a third-party tool or skill into `payload/`** — depend on it and invoke it
  (same rule as add-gate).
- **Never bulk-translate an existing hook config** (husky, lefthook, pre-commit) into
  `gate:` blocks. omakase wires only `pre-commit`/`pre-push`, and `run:` lines are judgment
  calls — a silently dropped or mistranslated check is worse than none. Take each check
  through the add-gate skill deliberately.
- **Never lift executable config from a repo that didn't publish itself as a harness**
  (e.g. scraping another project's `.claude/settings.json` hooks). The manifest is the
  "deliberately published to run" marker; only the owner adds it.

## Step 4 — gates

One at a time, via the **add-gate** skill: it picks the keys (`hook:`, `run:`, `glob:`,
`cacheable:`), pre-flights third-party tools, and wires the block into
`payload/omakase.manifest`.

## Step 5 — prove it installs

Commit first — `--source` clones the harness's **committed** state; uncommitted edits are
invisible to init. Then, in a throwaway repo:

```bash
cd "$(mktemp -d)" && git init -q && git commit -q --allow-empty -m init
omakase init --source /path/to/your-harness-clone   # //subpath suffix for a hub subfolder
omakase status                                       # placed files + gates all green
omakase remove                                       # reverses cleanly
```

If `init` refuses (a gate's `run:` names a script the payload doesn't ship, a bad manifest
key, a source-root manifest), fix the payload and re-run — the refusals are the validation,
never work around them. For a harness with gates, also trip one on purpose (stage a
violating change, watch the commit block) before calling it done.

## Step 6 — publish

Push, then hand out the install line:

    omakase init you/your-harness            # own repo
    omakase init you/hub/path/to/harness     # subfolder of a hub repo

Adopters re-run a bare `omakase init` to pick up your pushed changes; bump `version:` when
the payload changes so installs report what they got.

## See also

- [authoring.md](../../docs/authoring.md) — the conceptual reference behind this skill.
- [reference.md](../../docs/reference.md) — manifest schema, placement and exclusion rules.
- add-gate skill — everything about wiring checks to hooks.
