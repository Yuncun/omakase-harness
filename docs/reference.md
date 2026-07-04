# Reference

## Commands

### `init.sh [<owner/repo[#ref]> | --source <git-url|path>] [--cut-over] [--help]`

Overlays `payload/` onto the current repo, records placed paths in `.git/info/exclude`,
and installs hooks through lefthook. Skips paths the repo tracks. Overwrites a divergent
installed (untracked) file to match payload and warns. Removes a previously placed file
the payload no longer ships, unless it was edited locally.

- `<owner/repo[#ref]>` — positional shorthand for `--source https://github.com/owner/repo`,
  optionally pinned to a branch or tag with `#ref`. This is the install line for a custom
  harness a repo publishes: `omakase init you/harness`. A real local path with the same
  shape wins over the shorthand.
- `--source <git-url|path>` — install ONE harness (a `payload/` tree plus an
  `omakase.manifest`) at a time. No harness installed yet: the omakase base harness's
  payload is layered UNDER the custom harness's payload (base machinery underneath, the
  custom harness's delta winning on overlap), so a custom harness ships only its delta
  and relies on base machinery without keeping its own copy — the same base+delta merge
  `tools/build.sh` bakes into a bundle, done at install time. This source names the
  SAME harness already installed: repairs it — re-fetches the source's ref and
  re-records whatever commit currently resolves; if the fetch fails (offline) it falls
  back to the last good cached copy instead of failing the repair. This source names a
  DIFFERENT harness than the one installed: **replaces** it — every file the old source
  placed and the new one does not ship is swept, then the new source is installed fresh.
  There is no stacking; a repo holds exactly one installed harness at a time. Refuses
  (placing nothing) if the hook wiring references a `.omakase/*.sh` script neither the
  harness nor the base ships. The harness is remembered; a later bare `init` refreshes
  and reinstalls it.
- `--cut-over` — also untrack (`git rm --cached`) every payload path the repo currently
  tracks, so the installed copy takes over. Guarded: refuses without
  `OMAKASE_CUTOVER_CONFIRM=1`.

### `status.sh [--markdown]`

Prints the installed harness: the inventory grouped by origin (committed, injected,
global), the hook wiring, the run ledger, and the paths hidden via `.git/info/exclude`.
`--markdown` emits formatted Markdown. Read-only.

### `remove.sh`

Uninstalls hooks, deletes exactly the untracked files `init` placed, and strips the
omakase block from `.git/info/exclude`. Tracked files are never touched. Takes no
arguments — any argument is ignored. There is no per-source removal; a repo holds one
installed harness, and `remove` always tears it down completely.

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

## Instruction files

omakase places an instruction file exactly as the harness ships it — VERBATIM, at the
same path. There is no reroute, no synthesized bridge symlink, and no root-slot
fallback logic: `init` treats `AGENTS.md`, `CLAUDE.md`, and
`.github/copilot-instructions.md` like any other payload file. A harness that wants
Claude Code to read the same instructions as `CLAUDE.md` ships its own `CLAUDE.md` (or
its own `CLAUDE.md → AGENTS.md` symlink) under `payload/`; omakase never creates one on
a harness's behalf. Each host then reads whatever it natively recognizes at that path —
`AGENTS.md`/`CLAUDE.md` at the repo root for Claude Code, `.github/copilot-instructions.md`
for Copilot CLI. The usual placement rules apply and nothing else: a path the repo
already commits is skipped and reported, an installed instruction file is excluded via
`.git/info/exclude`, and `remove` deletes it like any other placed file.

## Path classification

`init` decides how to exclude a placed file by its top directory. A shared top directory
(`HARNESS_SHARED_TOPDIRS` in `bin/lib-harness-paths.sh`, currently `.github`) is excluded
file-by-file, so the project's own files there stay visible to git. Every other top
directory is excluded wholesale. See
[Concepts](concepts.md#owned-and-shared-directories).
