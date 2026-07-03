# Reference

## Commands

### `init.sh [<owner/repo[#ref]> | --source <git-url|path>] [--cut-over] [--help]`

Overlays `payload/` onto the current repo, records placed paths in `.git/info/exclude`,
and installs hooks through lefthook. Skips paths the repo tracks. Overwrites a divergent
installed (untracked) file to match payload and warns. Removes a previously placed file
the payload no longer ships, unless it was edited locally.

- `<owner/repo[#ref]>` — positional shorthand for `--source https://github.com/owner/repo`,
  optionally pinned to a branch or tag with `#ref`. This is the shareable install line others
  run after `share`: `omakase init you/harness`. A real local path with the same shape wins
  over the shorthand.
- `--source <git-url|path>` — install from a custom harness (a `payload/` tree plus an
  `omakase.manifest`). No harness installed yet: the omakase base harness's payload is
  layered UNDER the custom harness's payload (base machinery underneath, the custom
  harness's delta winning on overlap), so a custom harness ships only its delta and
  relies on base machinery without keeping its own copy — the same base+delta merge
  `tools/build.sh` bakes into a bundle, done at install time. One harness already
  installed and this source names the SAME one: repairs it. (Design intent, Phase 4,
  not yet shipped: repair at the recorded pin, offline, with `update` the only verb
  that fetches anything newer. Today a repair re-fetches the source's ref and
  re-records whatever commit currently resolves — `sources.tsv` records the resolved
  commit on every install/repair, but nothing reads it back to skip the fetch.) One
  harness installed and this source names a DIFFERENT one: **stacks** it on top — both
  harnesses' files stay live, the new one wins where both ship the same path (printed as `stacked <new> on
  top of <old>` plus one `^ overrides` line per shadowed path). Two harnesses already
  installed: a third, different source is refused (exit 1, nothing changed) — remove
  one first (`omakase remove <source>`). Refuses (placing nothing) if the merged hook
  wiring references a `.omakase/*.sh` script neither side ships. Each installed
  harness is remembered; a later bare `init` refreshes and reinstalls all of them —
  again, design intent is "at their recorded pins"; today it re-fetches each one's ref
  and re-records whatever commit currently resolves.
- `--cut-over` — also untrack (`git rm --cached`) every payload path the repo currently
  tracks, so the installed copy takes over. Guarded: refuses without
  `OMAKASE_CUTOVER_CONFIRM=1`.

### `status.sh [--markdown]`

Prints the installed harness: the inventory grouped by origin (committed, injected,
personal), the hook wiring, the run ledger, and the paths hidden via `.git/info/exclude`.
`--markdown` emits formatted Markdown. Read-only.

### `remove.sh [<source>]`

With no argument: uninstalls hooks, deletes exactly the untracked files `init`
placed, and strips the omakase block from `.git/info/exclude`. Tracked files are
never touched.

With `<source>`: removes just that one harness (matched by its source string or its
`source#ref` label) and restores whatever it had shadowed from the OTHER installed
harness — offline, no network fetch. Un-reroute is wired to **removing the BOTTOM
harness only**: that hands the root instruction slot back to the surviving harness
(see the instruction file mapping below), moving its own rerouted `AGENTS.md` back to
the root if nothing else claims the slot. Removing the TOP harness never un-reroutes,
even in the narrow case where the TOP harness ended up owning the slot — that survivor
stays stuck in `CLAUDE.local.md` (a known, deferred limitation; see v2-design.md §8).
An unrecognized `<source>` errors and changes nothing. With
only one harness installed, `remove <source>` for it behaves exactly like the
no-argument form.

### `share.sh [<name>]`

The inverse of `init`. Captures the current repo's harness into a NEW harness repo created
as a sibling directory (`../<name>`, default `<reponame>-harness`): writes `payload/`,
scaffolds `omakase.manifest` + a `README.md` carrying the install line, and runs `git init` +
commit so it is ready to push. Prints the publish command and the one-line install others run
(`omakase init you/<name>`). Never changes the source repo. Wraps `import.sh`.

### `import.sh <source-repo>`

The capture step behind `share`. Reads a project's harness files into `payload/`
(`OMAKASE_PAYLOAD` sets the destination). Read-only on the project it reads. Not an adopter command.

## Environment

| Variable | Effect |
|---|---|
| `LEFTHOOK_BIN` | path to a lefthook binary to use instead of PATH, `node_modules`, or the fetched cache |
| `LEFTHOOK=0` | skip gates for one git command. The overlay integrity check still runs; bypass it with git's own `--no-verify` |
| `OMAKASE_CUTOVER_CONFIRM=1` | required to apply `init.sh --cut-over` |
| `OMAKASE_PAYLOAD` | path to a payload tree to install, overriding the plugin payload. Lower precedence than `--source` |
| `OMAKASE_LEFTHOOK_BASE_URL` | mirror for the lefthook binary download |
| `XDG_CACHE_HOME` | cache root for the fetched lefthook binary (default `~/.cache`) |

A gate that defers its verdict is skipped with its own variable, by convention
`OMAKASE_SKIP_<CHECK>=1`.

## Manifest

`omakase.manifest` sits at the harness root. It is read only when installing from
`--source`; a plugin or `OMAKASE_PAYLOAD` install does not require it. Flat `key: value`
lines.

| Key | Required | Meaning |
|---|---|---|
| `name` | for `--source` | harness name, shown on install |
| `version` | no | harness version |
| `recommends` | no | free-text companion-tool hint, printed once at install |

## Instruction file mapping

A harness ships ONE instruction file, `payload/AGENTS.md`. This table is the
per-path fan-out `init` applies to it (and to an explicitly shipped `CLAUDE.md` or
`.github/copilot-instructions.md`) — the mirror of the literal data table in the binary
(v2 design §7); swap rows here and there together when the AGENTS.md standard converges.
`rel` is matched as a repo-root-relative path, never a basename (`AGENTS.md` is the ROOT
one only; `docs/AGENTS.md` falls through to "as-is"). Overlap between harnesses is
whole-file, later-installed harness wins — never a content merge. Root-slot ownership is
temporal, not role-based: whichever installed harness FIRST ships a root `AGENTS.md`
owns the slot for as long as it stays installed.

| Payload file | Root slot | Claude Code | Copilot CLI |
|---|---|---|---|
| `AGENTS.md` | free (no committed `AGENTS.md`/`CLAUDE.md` at root, and no already-installed harness owns the slot) | placed as-is at root + bridge `CLAUDE.md → AGENTS.md` (symlink; **only if** nothing already provides `CLAUDE.md`) | reads root `AGENTS.md` natively — nothing extra placed |
| `AGENTS.md` | taken | **rerouted to `CLAUDE.local.md`** — Claude Code's additive, gitignored slot; these instructions ADD to whatever owns the root slot, never replace it | **honest gap — §8** (Copilot has no per-repo gitignored additive slot; a rerouted file is Claude-only for now) |
| `CLAUDE.md` (shipped explicitly) | n/a | as-is; whole-file, later harness wins; committed copy skipped as always | reads root `CLAUDE.md` natively |
| `.github/copilot-instructions.md` | n/a | — | as-is (file-level exclude under the shared `.github` topdir) |

The bridge symlink hashes its target string (`AGENTS.md`), is a normal placed row owned by
the root-slot-owning harness, and is reversed by `remove` like any other placement.

## Path classification

`init` decides how to exclude a placed file by its top directory. A shared top directory
(`HARNESS_SHARED_TOPDIRS` in `bin/lib-harness-paths.sh`, currently `.github`) is excluded
file-by-file, so the project's own files there stay visible to git. Every other top
directory is excluded wholesale. See
[Concepts](concepts.md#owned-and-shared-directories).
