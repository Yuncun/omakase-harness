# Contributing

## Layout

The tool is shell scripts in `bin/` and a `payload/` tree copied into adopters. There is
no build step.

- `bin/` — the installer (`init`), uninstaller (`remove`), inspector (`show`), and capture
  tool (`import`), plus shared libraries.
- `payload/` — the harness content copied into every target. Keep it minimal: anything
  added here ships to all adopters.
- `tests/` — one `*.test.sh` per area.

## Tests

Run the suite:

    for t in tests/*.test.sh; do bash "$t" || break; done

A change to the installer or the path model needs a matching test. The path classification
in `bin/lib-harness-paths.sh` is the single source of truth for what is excluded and how;
changes there must keep `tests/harness-paths.test.sh` and
`tests/copilot-exclude-scope.test.sh` passing.

## Scope

omakase optimizes for the fewest controls and the least code. Weigh every new flag,
command, or file against that. Prefer reusing lefthook's native behavior over adding a
format.

## Releasing

omakase reaches users two ways, and they update differently:

- **`--source` / `owner/repo` installs.** `init` fetches the source repo and hard-resets
  to its default branch, so these users get the latest `main` on their next `init` —
  unless they pinned a branch or tag with `owner/repo#ref`, which keeps them on that ref.
  No version bump is needed for unpinned installs.
- **Claude Code plugin installs.** The plugin is cached and refreshed by the plugin
  manager, which keys off the version in `.claude-plugin/plugin.json`. If you do not bump
  it, plugin users keep running the old code. **A shipped change is not live for plugin
  users until the version is bumped and published.**

So any change adopters should pick up needs a version bump. To cut a release:

1. Bump the version in **both** `.claude-plugin/plugin.json` and `payload/.omakase/VERSION`
   — they must match. The first is what the plugin manager reads; the second is what the
   banner and `omakase status` show. Pre-1.0, a breaking change bumps the minor
   (`0.16.0` → `0.17.0`), a backward-compatible one bumps the patch.
2. In `CHANGELOG.md`, rename the `## [Unreleased]` block to `## [x.y.z] — YYYY-MM-DD` and
   leave a fresh empty `## [Unreleased]` above it.
3. Merge to `main`, then tag the merge commit `vx.y.z` and push the tag.

## Pull requests

- Do not include AI generated docs
