# Omakase Harness — Roadmap

Status snapshot and ordered plan. Top constraint: **fewest controls, least code, no slop.**
Every new flag, command, or file is a cost paid against simplicity.

## Where we are (2026-06-07)

Four scripts in `bin/`: `init`, `import`, `remove`, `show`. The plugin (`commands/omakase.md`)
dispatches `init` / `show` / `remove`; `import` is a creator-only script, off the adopter
surface.

The flagless surface shipped in PR #4: `init` dropped `--force` and the three-way merge (it now
matches payload — overwrite-a-divergent-uncommitted-file-and-warn, never touch a committed one),
and `import` dropped `--adopt-tracked` and no longer mutates the source. The scripts and command
doc now match the decisions below. Phase 1 is done; Phase 2 (interactive onboarding `init`) is next.

## Decisions locked this round (the simplification)

1. **`import` never touches the source repo.** It reads declared harness locations on disk and
   writes them into a `payload/` you point at. Read-only on the donor. (Cut `--adopt-tracked`.)
2. **`import` is a creator script, not an adopter command.** It runs from a clone/fork of the
   harness repo, where the payload you're building lives. The plugin surface is adoption only.
3. **The cut-over is the user's own `git rm --cached`,** not a tool flag. To stop committing a
   file so the injected copy wins, the user runs that one native command, then `init` injects it.
4. **`init` drops `--force` and the three-way merge.** Rule: *the injected harness matches payload.*
   No keep-vs-update logic, no snapshot-as-merge-base. (The snapshot itself stays — worktree
   self-heal still needs it.)
5. **`init` overwrites only a divergent, uncommitted file, and warns when it does** — generic
   wording ("updated N file(s) to the published version; local edits to these were replaced").
   It cannot, and does not try to, distinguish "your edit" from "upstream update."
6. **Take-it-or-leave-it on gate _content_.** Adopters can't change what a gate does; a content
   change means forking the harness (adopter → creator, via `import`). The one exception is turning
   a gate **on/off** — a bounded local toggle, not a content edit (see Phase 4 / decision C).

The model in one line: the harness repo and the plugin are the **same artifact, two faces** —
clone it to author, install it as a plugin to adopt.

## Phase 1 — Make the code match the decisions (DONE — PR #4)

- `import.sh`: remove `ADOPT_TRACKED`, the `git rm --cached` block, and the tracked/adopted
  reporting. Keep a plain "these N files are still committed; `git rm --cached` them to inject"
  note.
- `init.sh`: remove `--force`, the snapshot-as-merge-base compare, and the keep/updated branches.
  Collapse the placement loop to four buckets: **committed → skip**, **absent → place**,
  **identical → no-op**, **divergent → overwrite + warn**. Keep snapshot writing (self-heal).
- `commands/omakase.md`: drop `import` from the dispatch; drop `--force` / `--adopt-tracked`
  mentions; rewrite INIT/REMOVE text to the new behavior.
- Update `tests/`.

## Phase 2 — Interactive onboarding `init` (the welcome + preview + confirm)

Make `init` an onboarding flow, reusing `show` as the renderer:

1. **Welcome** — "Welcome to <name>'s omakase harness."
2. **Step : Tool(s) grid** — reuse `show`'s `GIT HOOKS — what runs, and when` section
   (`lefthook dump`), rendered in order. ~80% built already.
3. **Explain** — the harness injects files, hides them from git via `.git/info/exclude` (so they
   never upstream), and is removed with `/omakase remove`.
4. **Conflict preview (plan/apply pattern)** — dry-run the placement, show the buckets:
   *new*, *already current*, *committed (skipped)*, *would-overwrite*. This is the surfacing.
   Precedents: Terraform `plan`→`apply`, `kubectl --dry-run`, `apt` confirm, GNU Stow conflicts.
5. **One confirm**, then apply. No per-file options (take-it-or-leave-it).

Required refactor: **`show` gains a preview mode** — render what *would* be installed from a
payload, not only what *is* installed (today it requires `placed.list` and bails pre-install).
Factor `show` into a renderer callable in both modes; `init` calls it for step 2 and step 4.

Interactivity = **Claude conducts the Q&A** (the command is a skill that renders `show`, asks,
runs the script). Not a TTY menu — that needs a live terminal, breaks non-interactively, and is
more code.

Naming: keep **`remove`** (or `uninstall`). Not `eject` — create-react-app's `eject` means the
inverse (dump hidden config permanently *into* the project), and it collides with disk-eject.

## Phase 3 — Consolidate to one `omakase` command (nice-to-have)

`/omakase` with no argument → render `show`, detect installed-or-not, and offer the next action
(inject if absent, remove if present) interactively. Collapses three commands to one entry.
Fits the minimal-controls principle; bigger build. `init` / `remove` can remain as explicit
aliases or be folded entirely into the one command.

## Phase 4 — Long-term: interactive `show` that enables/disables gates

The override an adopter owns is **one local file mapping gate → on/off** — nothing more. It does
not edit gate content (content stays take-it-or-leave-it, decision #6), so it does not reopen that
call; it only declines or re-enables a whole gate.

Mechanism is lefthook's own layering — near-zero new format:

- `lefthook-local.yml` is lefthook's native per-developer, gitignored override that can `skip`
  hooks. That is the toggle file. Phase 4's interactive `show` just edits it (flip a gate → write
  the `skip`).
- **Wrinkle to resolve first:** omakase currently ships the gate *wiring* in `lefthook-local.yml`,
  so it can't also be the adopter's skip layer. Separate the layers — move shipped wiring to
  `lefthook.yml` (overwritten on update) and leave `lefthook-local.yml` as the adopter-owned skip
  file: `init` **seeds it once and never overwrites it.**
- `init` therefore treats the toggle file as seed-once/never-overwrite, and `show` becomes
  writable for on/off only (still read-only for everything else).

## Open decisions

| # | Decision | Lean |
|---|----------|------|
| A | Consolidate to one `omakase` command (Phase 3) | yes — most minimal |
| B | Uninstall verb: `remove` vs `uninstall` | `remove`; **not** `eject` |
| C | Give adopters a gate on/off toggle file (gates Phase 4) | **yes** — scoped to on/off only via `lefthook-local.yml` `skip`; never arbitrary content edits |
