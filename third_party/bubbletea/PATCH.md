# Patched copy of github.com/charmbracelet/bubbletea v1.3.10

Wired via a `replace` directive in the repo's `go.mod`. Diff vs the upstream
v1.3.10 tag: **`tea_init.go` deleted** (plus this file added). Nothing else.

## Why

Upstream's `tea_init.go` runs at package init — before `main()`, for EVERY
verb of the binary — and queries the terminal (OSC 11 background color +
cursor position) whenever stdin/stdout are TTYs. Measured on a pty that does
not answer OSC (script(1), expect, CI pty wrappers): a 5-second stall per
invocation, and the query's escape bytes pollute captured output — breaking
the contract that only the interactive screen changed and `--markdown` /
`--plain` / `init` / `remove` stay byte-identical.

The query exists to pre-resolve `lipgloss.AdaptiveColor` before a Program
owns the terminal (a mid-run query can hang). We keep that workaround, but in
the right place: `internal/tui/run.go` calls `lipgloss.HasDarkBackground()`
immediately before starting the Program — the one path that actually draws.

Upstream says the init will be removed in bubbletea v2 ("This workaround will
be removed in v2"). When v2 is stable, prefer upgrading to it and deleting
this vendored copy over rebasing the patch.

## Upgrading

1. `cp -R "$(go env GOMODCACHE)/github.com/charmbracelet/bubbletea@vX.Y.Z" third_party/bubbletea`
2. `chmod -R u+w third_party/bubbletea && rm third_party/bubbletea/tea_init.go` (if still present)
3. Keep this PATCH.md; update the version above; `go mod tidy`.
4. Re-run the pty proof: `script -q /dev/null dist/omakase nosuchverb` must
   finish instantly with no `ESC]11;` bytes in the capture.
