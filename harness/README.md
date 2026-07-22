# omakase-harness-harness — this repo's own development harness

Not a demo: this is the real harness omakase development runs, published the same way any
custom harness is. Adopt it to see a working harness with gates, or copy it as a starting
point for your own.

It carries only its own **delta**; the base machinery it relies on (the run ledger, the
status surfaces) is layered in underneath at install:

    payload/.claude/rules/omakase-dev.md      conventions, read by Claude Code
    payload/.github/instructions/
      omakase-dev.instructions.md             the same conventions, read by Copilot
    payload/.omakase/gates/block-marker.sh    gate: refuse a staged scratch marker
    payload/.omakase/gates/go-checks.sh       gate: gofmt + go vet on staged Go files
    payload/omakase.manifest                  the one manifest — identity (name + version)
                                              and the gates (hook / run / glob / cacheable / purpose)

## What runs

| Gate | When | What |
|---|---|---|
| `block-marker` | pre-commit | refuses any staged file carrying the scratch marker — the words `DO NOT` + `COMMIT`, in capitals, together |
| `go-checks` | pre-commit | staged `.go` files must be `gofmt`-clean and their packages must pass `go vet`; exits instantly when no `.go` file is staged |
| `go-test` | pre-push | `go test ./...`, cached per commit (`--cacheable`): runs once per HEAD, then reuses the pass; skipped when the push changes no Go file |

Every run lands in the scorecard (`omakase status` and the status line). The audited
per-gate bypass is `OMAKASE_SKIP_<NAME>=1` with the name upper-cased and `-` mapped to
`_`: `OMAKASE_SKIP_BLOCK_MARKER=1`, `OMAKASE_SKIP_GO_CHECKS=1`, `OMAKASE_SKIP_GO_TEST=1`.

## Try it

A harness can live in a **subfolder** of a git repo — this one lives at `harness/` in the
omakase repo itself. From any Go project, including a clone of this repo (that is the
self-hosting use):

    omakase init Yuncun/omakase-harness/harness    # Claude Code or Copilot CLI

(From a local clone, the same install is `omakase init --source <path-to-clone>//harness`
— the `//` marks where the repo ends and the subfolder begins.)

`omakase status` lists what it placed; `omakase remove` deletes it all and restores the repo.

## Make your own

Copy this directory into a git repo of your own — its own repo, or a subfolder of one you
already have. Replace the rules files with your team's conventions, swap the Go gates for
your stack's checks (each gate is one script plus one wiring line), push, and share.
People adopt it with `omakase init you/your-repo` (add `/path/to/harness` when it lives
in a subfolder).
