# Contributing

## Layout

The tool is one Go binary (module at the repo root) plus a `payload/` tree copied into
adopters. The binary embeds both the base payload and the agent-facing skills, so a
brew/release install is self-contained. Every real `omakase init` refreshes the stable
machine copy at `~/.cache/omakase/bin/current/omakase` — the path the `.git/hooks`
dispatchers exec — and the user-level skills under `~/.claude/skills/` and
`~/.copilot/skills/` (#211). Nothing ever downloads a binary at run time (#182).

- `cmd/omakase/`, `internal/` — the binary: all verbs, the gate runner, the status
  pages, the statusline, the host wiring.
- `payload/` — the harness content copied into every target. Keep it minimal: anything
  added here ships to all adopters.
- `skills/` — the agent-facing skills, embedded into the binary and installed
  user-level at init.
- `tests/` — one `*.test.sh` per area; `tests/bin/` is test-only plumbing (thin verb
  entry points plus the binary-resolution lib the suites share — an `OMAKASE_BIN`
  override; a dev rebuild `go build -o dist/omakase ./cmd/omakase`; `dist/omakase`;
  `omakase` on `PATH`; the stable machine copy).

## Tests

Run the suite:

    for t in tests/*.test.sh; do bash "$t" || break; done

A change to the installer or the path model needs a matching test. The path
classification ships in Go (`internal/harness`); `tests/bin/lib-harness-paths.sh` is the
suites' mirror of it, and the two are pinned against drift by
`tests/harness-paths.test.sh` and the Go `TestSharedTopdirs`.

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
