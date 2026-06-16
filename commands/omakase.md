---
description: Show, install, or remove the personal omakase harness (zero committed footprint)
argument-hint: "[show | init [<source-url-or-path>] | remove]"
---

Dispatch on the argument `$ARGUMENTS` — empty / `show` / `status` → SHOW, `init` (optionally followed by a git URL or local path) → INIT, `remove` → REMOVE. For INIT and REMOVE, run the matching script and report its output. For SHOW, run the script (it emits Markdown) and relay that output verbatim (see below). (Authoring a harness — `import` — is a creator script run from a clone of the harness repo, not an adopter command; see the repo's `bin/import.sh`.)

## SHOW — the default (empty argument, `show`, or `status`)

```bash
bash "${CLAUDE_PLUGIN_ROOT}/bin/show.sh" --markdown
```

`--markdown` makes the script emit the harness map as finished Markdown: a heading, the three-group inventory (committed / injected / personal), the hook wiring as a YAML block, the recent-runs scorecard table, and the hidden paths. **Relay it verbatim** — output exactly what the script printed, unchanged. Do NOT reformat, re-order, summarize, annotate, or add commentary; the script owns the format so the render stays deterministic and faithful (the previous "you reformat it" design let editorial drift creep in). Read-only — running this never changes anything. If no harness is installed the script says so; relay that.

## INIT — argument `init`

```bash
bash "${CLAUDE_PLUGIN_ROOT}/bin/init.sh"
```

Overlays the plugin's `payload/` onto the repo root. Rule: **the injected harness matches payload.** It **skips any path the repo already tracks** (never overwrites a committed file), **overwrites an injected file that differs from payload, warning that any local edit was replaced**, and **removes a previously injected file the payload no longer ships** (only when untouched; a locally edited one is kept, with a warning). It records every placed path in `.git/info/exclude` and runs `lefthook install`. Nothing is committed. Tell the user which files were placed, overwritten, skipped, or removed, and that `/omakase remove` undoes everything.

With a source — `/omakase init <git-url-or-local-path>`:

```bash
bash "${CLAUDE_PLUGIN_ROOT}/bin/init.sh" --source "<git-url-or-local-path>"
```

`--source` pulls a harness SOURCE (a git repo with a `payload/` tree plus an `omakase.manifest`) into a local cache and injects its payload instead of the plugin's. The source is remembered: a later bare `/omakase init` refreshes and re-injects it. If the script refuses the source (no `payload/`, no `omakase.manifest`), relay the error verbatim — nothing was changed.

lefthook is self-provisioned: if it isn't already on PATH / `LEFTHOOK_BIN` / the repo's `node_modules`, init downloads a pinned, checksum-verified lefthook binary into a per-machine cache (`${XDG_CACHE_HOME:-~/.cache}/omakase/lefthook/`) and uses it. The cache is disposable and the repo is untouched; `/omakase remove` does not need to undo a global install. Only if the fetch itself fails (unknown platform, no network, no `curl`/`wget`, checksum mismatch) does init exit with "lefthook not found and could not be fetched" — then, as a fallback, ask the user how they want lefthook installed (`brew install lefthook`, `mise use lefthook`, or as a project devDependency), run their choice, then re-run. If they already have a lefthook binary elsewhere, re-run with `LEFTHOOK_BIN=/path/to/lefthook`.

If the injector REFUSES because an incumbent hook manager is present (husky, pre-commit, a foreign `core.hooksPath`, or existing non-lefthook hook files), relay the refusal verbatim and STOP. Do not delete the incumbent's files or set config to force the install — that decision belongs to the user.

For files reported as **skipped (committed)**: the harness copy can take over only via the guarded cut-over, `init.sh --cut-over`. NEVER run `git rm --cached` directly and NEVER set `OMAKASE_CUTOVER_CONFIRM=1` on your own — cutting over stages deletions of shared files that the next commit applies for everyone. Surface the skip report to the user; run the confirmed cut-over only when the user explicitly asks for it.

If init prints an **upstream-collision WARNING** (an injected path is now tracked by the repo), relay it verbatim — the user's personal copy was likely overwritten by an upstream commit and a preserved copy path is named in the warning.

The fail-closed overlay check sits above lefthook by design: `LEFTHOOK=0` skips the gates but not the integrity check; the only bypass is git's own `--no-verify`.

## REMOVE — argument `remove`

```bash
bash "${CLAUDE_PLUGIN_ROOT}/bin/remove.sh"
```

Uninstalls the git hooks, deletes exactly the untracked files init placed (never a tracked file), and strips the omakase block from `.git/info/exclude`. Confirm the working tree is back to its pre-init state.
