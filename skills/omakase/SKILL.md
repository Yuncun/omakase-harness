---
name: omakase
description: Show, install, or remove a personal omakase harness in the current repo, with zero committed footprint. Use when asked to install/set up/arm an omakase harness, inject a harness into the repo, show harness status, or remove/uninstall it. Triggers on "/omakase", "omakase init", "omakase show", "omakase remove", "install the harness", "arm the harness".
allowed-tools: Bash(*/run.sh *) Bash(*/bin/init.sh *) Bash(*/bin/remove.sh *) Bash(*/bin/show.sh *)
---

# /omakase — manage a harness (zero committed footprint)

This is the omakase base harness's host-agnostic management front door for **Copilot CLI**
(Claude Code uses the `/omakase` *command* with the same behaviour). It overlays a harness
**payload** onto the current repo at real paths, records every placed path in
`.git/info/exclude` (so nothing is committed and `.gitignore` is untouched), and installs
lefthook to run the gates. `/omakase remove` reverses it.

The payload comes from either the base harness's own payload (a bare `init`), or — the usual
case — a **custom harness** you point it at: `/omakase init --source <git-url-or-path>` pulls a
repo carrying a `payload/` tree plus an `omakase.manifest` and injects the base harness's payload
with **that custom harness's payload layered on top** (base machinery underneath, the custom
harness winning on overlap). So a custom harness ships only its own delta and relies on base
machinery without keeping its own copy. It is remembered, so a later bare `/omakase init`
refreshes and re-injects it. (Example: install the omakase base harness once, then
`init --source` a custom-harness repo.)

All work goes through the self-locating dispatcher `run.sh` in this skill's directory (it
finds the base harness's injector in `bin/` and operates on the current git repo).

## Dispatch on the user's request

Resolve `run.sh`'s absolute path from this skill's base directory, then:

### SHOW (default — "status", "show", or no argument)

```bash
bash <this-skill-dir>/run.sh show
```

`run.sh show` calls the injector's `show.sh --markdown`, which emits the harness map as
finished Markdown (inventory, hook wiring, recent-runs scorecard, hidden paths). **Relay its
output verbatim** — do not reformat, re-order, summarize, or annotate. Read-only. If no
harness is installed it says so; relay that.

### INIT ("init", "install", "arm")

```bash
bash <this-skill-dir>/run.sh init                              # re-inject the remembered/base payload
bash <this-skill-dir>/run.sh init --source <git-url-or-path>   # pull + inject a custom harness
```

Overlays the payload onto the repo root. It **skips any path the repo already tracks** (never
overwrites a committed file), **overwrites an injected file that differs from payload**
(warning that any local edit was replaced), and **removes a previously injected file the
payload no longer ships** (only when untouched). It records placed paths in
`.git/info/exclude` and runs `lefthook install`. Nothing is committed. Tell the user which
files were placed/overwritten/skipped/removed, and that `/omakase remove` undoes everything.

If the source declares `recommends:` in its manifest, init prints it once — relay it. If init
**refuses** the source (no `payload/`, no `omakase.manifest`, or merged hook wiring that
references a `.omakase/*.sh` script neither the custom harness nor the base harness ships),
relay the refusal verbatim and STOP — nothing was placed. If init refuses because an incumbent hook
manager is present (husky, pre-commit, a foreign `core.hooksPath`, non-lefthook hooks), relay
that refusal verbatim and STOP — do not force it. For paths reported **skipped (committed)**,
never run `git rm --cached` or set `OMAKASE_CUTOVER_CONFIRM=1` yourself; surface the skip and
run the guarded `init.sh --cut-over` only if the user explicitly asks.

### REMOVE ("remove", "uninstall")

```bash
bash <this-skill-dir>/run.sh remove
```

Uninstalls the git hooks, deletes exactly the untracked files init placed (never a tracked
file), and strips the omakase block from `.git/info/exclude`. Confirm the working tree is
back to its pre-init state.

## Notes

- After `init`, run `/skills reload` so Copilot picks up any injected project skill in the
  target repo (a custom harness may ship `.github/skills/*`).
- omakase is host-agnostic: the same `bin/` runs from Copilot CLI, Claude Code, or a
  plain shell (`bash <base-harness>/bin/init.sh`).
