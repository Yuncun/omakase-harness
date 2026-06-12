# omakase v1 — harness manager

Status: draft for review, 2026-06-11. Reframes the product surface; the existing engine (injection, ledger, import, lefthook spine, status panel) is kept and extended, not replaced. Evidence base: `docs/notes/2026-06-11-consent-layers-prior-art.md` and the 2026-06-10 four-lens design review.

## Product statement

Omakase installs, shows, and manages coding-agent harnesses: ready-made harness content pulled from sources, a full inventory of every harness artifact in a repo, and per-piece control — all with zero committed footprint.

Two audiences, one engine: people who want a working harness in one command, and people who want to see and control what a repo feeds their agent. Both are served by the same source → ledger → materialize → show pipeline.

## v1 scope

### 1. Sources

A source is a git repo containing a payload tree (gate definitions, gate scripts, skills, rules, commands) plus a manifest. Omakase pulls a source into a local cache and materializes its payload through the existing injection mechanism. `pixterm-harness` converts to the first source once the mechanism is proven; until then it continues as-is. The Claude Code plugin remains in the marketplace as the discovery and bootstrap channel; harness content moves to sources (dual distribution).

Amendment (2026-06-12, as shipped in 0.12.0): the manifest is a flat `omakase.manifest` (`name:` required, `version:` optional). The surface is `init.sh --source <git-url-or-path>`; payload precedence is `--source` > `OMAKASE_PAYLOAD` > the remembered source (`$COMMON/omakase/source`) > the plugin payload. The cache self-recovers (a failed refresh discards and re-clones). Re-init also gained an orphan sweep — a change to the injection mechanism itself: prior-ledger paths absent from the new payload are deleted when untracked and hash-pristine, kept with a warning when locally edited.

### 2. Provenance ledger

`placed.list` becomes a per-artifact record: source, kind (gate / script / skill / rule / command), path, content hash, enabled state. Consumed by init, import, remove, self-heal, and show. This is the load-bearing change; inventory and per-source operations key off it.

### 3. Inventory

One verb: `/omakase show` gains an inventory section alongside the existing gate checklist. It lists every harness artifact in the repo grouped by origin — committed by the project, injected from a source, personal — with kind and enabled state from the ledger. No token estimates: the host owns context-cost ground truth (`/context`, `/skills` token sort, the plugin cost pane); re-deriving it duplicates vendor surfaces and risks disagreeing with them. Cut 2026-06-12. No new commands; the minimal-controls principle holds.

### 4. Safety fixes (preconditions for running on arbitrary repos)

1. **Incumbent hook-manager guard** — init detects husky, pre-commit, or a foreign `core.hooksPath` before touching hooks and refuses with guidance; installing over an incumbent silently disables the project's own gates.
2. **Guarded cut-over** — the raw `git rm --cached` step is replaced with a guarded command that states its consequences and refuses without explicit confirmation; an agent auto-commit after the raw form deletes shared files from the repo.
3. **Upstream-collision guard** — warn loudly when an upstream commit lands a tracked file at a path the overlay occupies; git's default is to silently overwrite ignored files on checkout.
4. **Fail-closed gates** — the hook stub verifies the overlay manifest before running and hard-fails with a re-init instruction when overlay files are missing; today `git clean -fdx` wipes the overlay and the remaining hooks pass silently.
5. **Self-heal respects intent** — ensure-present consults the ledger's enabled state; a disabled artifact is not "missing" and must not be restored.

### 5. Import widens

Import scrapes skills, commands, and rules (file-level) in addition to gates, and parses hook configs structurally (real YAML parsing) instead of pattern-matching script names.

## Non-goals (v1)

- No git-layer masking of committed files (sparse-checkout / skip-worktree rejected; git's own documentation warns both off).
- No cross-host adapters (Cursor/Copilot config translation) until a second host has demonstrated users.
- No standalone npx packaging of the inventory; plugin command first.
- Decline management (writing `skillOverrides` / `claudeMdExcludes` / `enabledPlugins:false` into `settings.local.json` from the inventory) is phase 2.
- The essay is a separate track.

## Engine and language

New engine components (ledger, inventory, import parsing) are written in Go — single static binary, no runtime dependency, the same choice lefthook and direnv made. Distribution via GitHub release binaries fetched at init; checksums recorded. Bash remains only in the thin hook-stub layer that must execute inside git hooks with zero dependencies. Existing bash scripts migrate onto the Go engine incrementally as each is touched; no big-bang rewrite.

Amendment (2026-06-11): the provenance ledger is plain TSV (`.git/omakase/placed.tsv`: path, kind, source, sha256, enabled) written by bash at init, so the hook-time readers (ensure-present, verify-overlay) parse it under POSIX sh without the binary; Go debuts at the inventory step. It is a separate file from the gate-run ledger (`.git/omakase/ledger.tsv`), which records run history and must survive re-init.

Amendment (2026-06-12): with token estimates cut from the inventory, the inventory is ledger rendering plus a committed-path scan and may remain bash; Go's trigger moves to the step that genuinely needs it — structural YAML parsing for import widening and the sources mechanism.

Amendment (2026-06-12, later): the inventory AND sources both shipped in bash (the manifest is deliberately flat key:value, read with sed — no YAML parser). Go's remaining trigger is import widening (§5) alone; if that also proves tractable without structural YAML parsing, Go has no trigger left in this spec.

## Phase 2 (recorded, not built)

- Decline surface: per-artifact off switches driven from the inventory, written to the host's own override keys in `settings.local.json`; declined items are reported loudly in show, never hidden.
- Change re-approval: ledger content hashes flag source and project artifacts that changed since last review (direnv model).

## Sequencing

1. Safety fixes 1–4 (ledger-independent, bash, testable now).
2. Go engine: provenance ledger + fix 5.
3. Inventory in show.
4. Sources mechanism; pixterm-harness converts last.
