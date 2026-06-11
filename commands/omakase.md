---
description: Show, install, or remove the personal omakase harness (zero committed footprint)
argument-hint: "[show | init | remove]"
---

Dispatch on the argument `$ARGUMENTS` — empty / `show` / `status` → SHOW, `init` → INIT, `remove` → REMOVE. For INIT and REMOVE, run the matching script and report its output. For SHOW, run the script (it emits Markdown) and relay that output verbatim (see below). (Authoring a harness — `import` — is a creator script run from a clone of the harness repo, not an adopter command; see the repo's `bin/import.sh`.)

## SHOW — the default (empty argument, `show`, or `status`)

```bash
bash "${CLAUDE_PLUGIN_ROOT}/bin/show.sh" --markdown
```

`--markdown` makes the script emit the harness map as finished Markdown: a heading, the placed-files list, the hook wiring as a YAML block, the recent-runs scorecard table, and the hidden paths. **Relay it verbatim** — output exactly what the script printed, unchanged. Do NOT reformat, re-order, summarize, annotate, or add commentary; the script owns the format so the render stays deterministic and faithful (the previous "you reformat it" design let editorial drift creep in). Read-only — running this never changes anything. If no harness is installed the script says so; relay that.

## INIT — argument `init`

```bash
bash "${CLAUDE_PLUGIN_ROOT}/bin/init.sh"
```

Overlays the plugin's `payload/` onto the repo root. Rule: **the injected harness matches payload.** It **skips any path the repo already tracks** (never overwrites a committed file) and **overwrites an injected file that differs from payload, warning that any local edit was replaced**. It records every placed path in `.git/info/exclude` and runs `lefthook install`. Nothing is committed. Tell the user which files were placed, overwritten, or skipped, and that `/omakase remove` undoes everything.

If the injector exits with "lefthook not found", do NOT install it silently — ask the user how they want lefthook installed (`brew install lefthook`, `mise use lefthook`, or as a project devDependency), run their choice, then re-run. If they already have a lefthook binary elsewhere, re-run with `LEFTHOOK_BIN=/path/to/lefthook`.

If the injector REFUSES because an incumbent hook manager is present (husky, pre-commit, a foreign `core.hooksPath`, or existing non-lefthook hook files), relay the refusal verbatim and STOP. Do not delete the incumbent's files or set config to force the install — that decision belongs to the user.

For files reported as **skipped (committed)**: the harness copy can take over only via the guarded cut-over, `init.sh --cut-over`. NEVER run `git rm --cached` directly and NEVER set `OMAKASE_CUTOVER_CONFIRM=1` on your own — cutting over stages deletions of shared files that the next commit applies for everyone. Surface the skip report to the user; run the confirmed cut-over only when the user explicitly asks for it.

If init prints an **upstream-collision WARNING** (an injected path is now tracked by the repo), relay it verbatim — the user's personal copy was likely overwritten by an upstream commit and a preserved copy path is named in the warning.

## REMOVE — argument `remove`

```bash
bash "${CLAUDE_PLUGIN_ROOT}/bin/remove.sh"
```

Uninstalls the git hooks, deletes exactly the untracked files init placed (never a tracked file), and strips the omakase block from `.git/info/exclude`. Confirm the working tree is back to its pre-init state.
