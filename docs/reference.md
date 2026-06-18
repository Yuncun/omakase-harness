# Reference

## Commands

### `init.sh [--source <git-url|path>] [--cut-over] [--help]`

Overlays `payload/` onto the current repo, records placed paths in `.git/info/exclude`,
and installs hooks through lefthook. Skips paths the repo tracks. Overwrites a divergent
installed (untracked) file to match payload and warns. Removes a previously placed file
the payload no longer ships, unless it was edited locally.

- `--source <git-url|path>` — install from a harness source (a `payload/` tree plus an
  `omakase.manifest`) instead of the local or plugin payload. The source is remembered; a
  later bare `init` refreshes and reinstalls it.
- `--cut-over` — also untrack (`git rm --cached`) every payload path the repo currently
  tracks, so the installed copy takes over. Guarded: refuses without
  `OMAKASE_CUTOVER_CONFIRM=1`.

### `show.sh [--markdown]`

Prints the installed harness: the inventory grouped by origin (committed, injected,
personal), the hook wiring, the run ledger, and the paths hidden via `.git/info/exclude`.
`--markdown` emits formatted Markdown. Read-only.

### `remove.sh`

Uninstalls hooks, deletes exactly the untracked files `init` placed, and strips the
omakase block from `.git/info/exclude`. Tracked files are never touched.

### `import.sh <source-repo>`

Creator tool. Reads a project's harness files into `payload/`. Read-only on the source.
Not part of the adopter surface.

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

## Path classification

`init` decides how to exclude a placed file by its top directory. A shared top directory
(`HARNESS_SHARED_TOPDIRS` in `bin/lib-harness-paths.sh`, currently `.github`) is excluded
file-by-file, so the project's own files there stay visible to git. Every other top
directory is excluded wholesale. See
[Concepts](concepts.md#owned-and-shared-directories).
