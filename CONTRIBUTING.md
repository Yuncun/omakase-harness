# Contributing

## Layout

The tool is shell entry points in `bin/` and a `payload/` tree copied into adopters.
Four verbs — `init`, `remove`, `status`, and `mcp` — are implemented by a Go binary (module
at the repo root) behind unchanged `bin/{init,remove,status,mcp}.sh` entry points: thin
shims that resolve a runnable `omakase` in order — an `OMAKASE_BIN` override; a dev rebuild
(`CGO_ENABLED=0 go build -o dist/omakase ./cmd/omakase`, when `go.mod` and `go` are both
present); `dist/omakase`; `omakase` on `PATH`; then the pinned, checksum-verified release
binary, fetched once per machine into a local cache (`init`, `status`, and `mcp` allow this
fetch; `remove` does not, so uninstall stays offline). `bin/lib-omakase-bin.sh` implements
every tier. When none of that resolves, every shim fails closed — recovery guidance on
stderr (naming the `OMAKASE_BIN` override) and exit 1. There is no bash fallback body: a
silent one would mask binary-distribution failures.

- `bin/` — the installer (`init`), uninstaller (`remove`), inspector (`status`), and
  MCP-server entry point (`mcp`), plus shared libraries.
- `payload/` — the harness content copied into every target. Keep it minimal: anything
  added here ships to all adopters.
- `tests/` — one `*.test.sh` per area.

## Tests

Run the suite:

    for t in tests/*.test.sh; do bash "$t" || break; done

With Go present, the suite exercises the `status`, `init`, and `remove` binary paths
through the shims. Without Go, the shims resolve a real binary — `omakase` on `PATH`, or
the pinned, checksum-verified release fetched once per machine — and fail closed (error +
exit 1) when nothing resolves.

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
