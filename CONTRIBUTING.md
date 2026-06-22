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

## Pull requests

- Do not include AI generated docs
