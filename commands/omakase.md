---
description: Show, install, or remove the personal omakase harness (zero committed footprint)
---

Dispatch on the argument `$ARGUMENTS` — empty / `show` / `status` → SHOW, `init` → INIT, `remove` → REMOVE. For INIT and REMOVE, run the matching script and report its output. For SHOW, run the script but **re-render** its output as Markdown (see below). (Authoring a harness — `import` — is a creator script run from a clone of the harness repo, not an adopter command; see the repo's `bin/import.sh`.)

## SHOW — the default (empty argument, `show`, or `status`)

```bash
NO_COLOR=1 bash "${CLAUDE_PLUGIN_ROOT}/bin/show.sh"
```

The script prints the installed-but-gitignored harness as one terminal-formatted map. **Do not relay it verbatim** — raw script output lands in a collapsed tool box that the user must expand, and it is not formatted. Instead, read the script's output and re-present it to the user as clean Markdown in your reply:

- A short heading naming the harness and the repo it is installed in.
- **Placed files** — a list (flag any line marked `MISSING`).
- **Git hooks** — what runs at each hook (pre-commit / pre-push / post-checkout), grouped by hook.
- **Recent runs** — the scorecard as a small table (gate · verdict · how long ago); omit the section if nothing is recorded yet.
- **Hidden via `.git/info/exclude`** — a one-line note of the excluded path prefixes.

Render faithfully — reformat, don't editorialize, drop nothing material. The scorecard is populated by gates wired through `omakase-ledger.sh`. Read-only — running this never changes anything. If no harness is installed the script says so; relay that and point to `/omakase init`.

## INIT — argument `init`

```bash
bash "${CLAUDE_PLUGIN_ROOT}/bin/init.sh"
```

Overlays the plugin's `payload/` onto the repo root. Rule: **the injected harness matches payload.** It **skips any path the repo already tracks** (never overwrites a committed file — those are reported so the user can `git rm --cached` them to let the harness copy take over) and **overwrites an injected file that differs from payload, warning that any local edit was replaced**. It records every placed path in `.git/info/exclude` and runs `lefthook install`. Nothing is committed. Tell the user which files were placed, overwritten, or skipped, and that `/omakase remove` undoes everything.

If the injector exits with "lefthook not found", do NOT install it silently — ask the user how they want lefthook installed (`brew install lefthook`, `mise use lefthook`, or as a project devDependency), run their choice, then re-run. If they already have a lefthook binary elsewhere, re-run with `LEFTHOOK_BIN=/path/to/lefthook`.

## REMOVE — argument `remove`

```bash
bash "${CLAUDE_PLUGIN_ROOT}/bin/remove.sh"
```

Uninstalls the git hooks, deletes exactly the untracked files init placed (never a tracked file), and strips the omakase block from `.git/info/exclude`. Confirm the working tree is back to its pre-init state.
