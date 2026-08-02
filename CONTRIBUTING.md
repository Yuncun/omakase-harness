# Contributing

## Layout

The tool is shell entry points in `bin/` and a `payload/` tree copied into adopters.
Three verbs — `init`, `remove`, and `status` — are implemented by a Go binary (module
at the repo root) behind unchanged `bin/{init,remove,status}.sh` entry points: thin
shims that resolve a runnable `omakase` in order — an `OMAKASE_BIN` override; a dev rebuild
(`CGO_ENABLED=0 go build -o dist/omakase ./cmd/omakase`, when `go.mod` and `go` are both
present); `dist/omakase`; `omakase` on `PATH`; then the stable machine copy every real
`omakase init` self-installs at `~/.cache/omakase/bin/current/omakase`.
`bin/lib-omakase-bin.sh` implements every tier. Resolution is local-only — the shims
never download a binary (#182). When none of that resolves, every shim fails closed —
one line on stderr naming the fix (`brew install yuncun/tap/omakase`) and exit 1. There
is no bash fallback body: a silent one would mask binary-distribution failures.

- `bin/` — the installer (`init`), uninstaller (`remove`), and inspector (`status`),
  plus shared libraries.
- `payload/` — the harness content copied into every target. Keep it minimal: anything
  added here ships to all adopters.
- `tests/` — one `*.test.sh` per area.

## Tests

Run the suite:

    for t in tests/*.test.sh; do bash "$t" || break; done

With Go present, the suite exercises the `status`, `init`, and `remove` binary paths
through the shims. Without Go, the shims resolve a real binary — `omakase` on `PATH`, or
the stable machine copy an earlier init self-installed — and fail closed (the brew
instruction + exit 1) when nothing resolves.

A change to the installer or the path model needs a matching test. The path classification
in `bin/lib-harness-paths.sh` is the single source of truth for what is excluded and how;
changes there must keep `tests/harness-paths.test.sh` and
`tests/copilot-exclude-scope.test.sh` passing.

## Scope

omakase optimizes for the fewest controls and the least code. Weigh every new flag,
command, or file against that. Prefer extending the existing `omakase.manifest` gate
schema over adding a new format.

## Releasing

omakase reaches users two ways, and they update differently:

- **`--source` / `owner/repo` installs.** `init` fetches the source repo and hard-resets
  to its default branch, so these users get the latest `main` on their next `init` —
  unless they pinned a branch or tag with `owner/repo#ref`, which keeps them on that ref.
  No version bump is needed for unpinned installs.
- **brew / binary installs.** `brew upgrade` delivers the new binary, and the next
  `omakase init` refreshes the machine-wide copy the git hooks run **and** the
  user-level agent skills (both are version-guarded, so an older binary never rolls
  either backwards). **A shipped change is not live for these users until a release
  is tagged and published.**

So any change adopters should pick up needs a version bump. The step-by-step
runbook — version bumps, changelog, tagging, and the publish line — is
[docs/releasing.md](docs/releasing.md).

## Pull requests

- Do not include AI generated docs
