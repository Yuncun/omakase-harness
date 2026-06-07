---
description: Show, install, or remove the personal omakase harness (zero committed footprint)
---

Dispatch on the argument `$ARGUMENTS` — empty / `show` / `status` → SHOW, `init` → INIT, `remove` → REMOVE. Run the matching script and report its output verbatim.

## SHOW — the default (empty argument, `show`, or `status`)

```bash
bash "${CLAUDE_PLUGIN_ROOT}/bin/show.sh"
```

Renders the installed-but-gitignored harness as one map: every placed file, the git hooks and what each one runs, a RECENT RUNS scorecard (most recent verdict per gate, with how long ago — populated by gates wired through `omakase-record.sh`), and what is hidden via `.git/info/exclude`. Read-only — running this never changes anything. If no harness is installed it says so and points to `init`.

## INIT — argument `init` (optionally `init --force`)

```bash
bash "${CLAUDE_PLUGIN_ROOT}/bin/init.sh"
```

If the user passed `--force`, run `bash "${CLAUDE_PLUGIN_ROOT}/bin/init.sh" --force` instead.

Overlays the plugin's `payload/` onto the repo root. It **skips any path the repo already tracks** (never overwrites a committed file) and **keeps any untracked file you have edited** (re-run reports those as "kept"; `init --force` takes the new payload version over your edits). It records every placed path in `.git/info/exclude` and runs `lefthook install`. Nothing is committed. Tell the user which files were placed, updated, kept-as-edited, or skipped, and that `/omakase remove` undoes everything.

If the injector exits with "lefthook not found", do NOT install it silently — ask the user how they want lefthook installed (`brew install lefthook`, `mise use lefthook`, or as a project devDependency), run their choice, then re-run. If they already have a lefthook binary elsewhere, re-run with `LEFTHOOK_BIN=/path/to/lefthook`.

## REMOVE — argument `remove`

```bash
bash "${CLAUDE_PLUGIN_ROOT}/bin/remove.sh"
```

Uninstalls the git hooks, deletes exactly the untracked files init placed (never a tracked file), and strips the omakase block from `.git/info/exclude`. Confirm the working tree is back to its pre-init state.
