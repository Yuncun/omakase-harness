---
applyTo: "**"
---

# omakase development conventions (placed by the starter harness)

This file is a gitignored overlay — placed by `omakase init`, never committed, deleted by
`omakase remove`. Durable edits go in the harness source at
`examples/starter-harness/payload/`, followed by a re-init; an edit made here in place is
overwritten on the next init.

- Write conventional commit messages: `feat(scope): …`, `fix: …`, `docs: …`, `chore: …`.
- Go code must pass `gofmt` and `go vet` before commit — the `go-checks` gate enforces it.
  Fix formatting with `gofmt -w <file>` and re-stage.
- `go test ./...` must pass before push — the `go-test` gate enforces it, cached per commit.
- Run the shell test suites non-interactively: `bash tests/<name>.test.sh </dev/null`.
- Mark scratch or secret-in-progress code with the scratch marker — the words `DO NOT` +
  `COMMIT`, in capitals, together — and the `block-marker` gate refuses to commit it until
  the marker is removed.
