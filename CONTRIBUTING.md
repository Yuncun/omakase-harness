# Contributing

## Layout

The tool is shell entry points in `bin/` and a `payload/` tree copied into adopters.
`status`, `init`, and `remove` are implemented by a Go binary (module at the repo root)
behind their unchanged `bin/{status,init,remove}.sh` entry points — thin shims that
rebuild and exec the binary, falling back to the frozen v1 bash body only when it cannot be
resolved. CI and the shims build it with `CGO_ENABLED=0 go build -o dist/omakase
./cmd/omakase`; the frozen v1 bodies stay at `bin/legacy/{status,init,remove}.sh` as the
no-Go fallback until the rewrite completes. The new v2 `personal` verb (harness layers) is
binary-only — invoked as `dist/omakase personal`, with no `bin/` shim of its own. (`import`
and `share` are still bash; both retire in Phase 6.)

- `bin/` — the installer (`init`), uninstaller (`remove`), inspector (`status`), and
  capture tool (`import`), plus shared libraries.
- `payload/` — the harness content copied into every target. Keep it minimal: anything
  added here ships to all adopters.
- `tests/` — one `*.test.sh` per area.

## Tests

Run the suite:

    for t in tests/*.test.sh; do bash "$t" || break; done

With Go present, the suite exercises the `status`, `init`, and `remove` binary paths
through the shims. Two differential suites diff the Go output byte-for-byte against the
frozen v1 bash: `tests/status-parity.test.sh` for `status`, and
`tests/init-remove-parity.test.sh` for `init`/`remove` (the latter compares per-file lists
line-SORTED, since find(1) and Go's directory walk emit the same set of files in different
orders). Without Go, both suites skip and the shims fall back to the bundled v1 scripts,
printing a one-line notice. `tests/layers.test.sh` is the black-box oracle for the v2
layered behavior (personal layer, `personal off`, migration) — the new-behavior counterpart
to the differential suites; it too skips as a group without lefthook and without Go.

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

So any change adopters should pick up needs a version bump. The step-by-step
runbook — version bumps, changelog, tagging, and the draft-release gate — is
[docs/releasing.md](docs/releasing.md).

## Pull requests

- Do not include AI generated docs
