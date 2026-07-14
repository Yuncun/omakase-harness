# sample-harness — a minimal omakase harness

A worked example of a harness: the smallest thing that places a real file and runs a real
gate. Read it to see how a harness is shaped, then copy it to make your own.

It carries only its own **delta**:

    omakase.manifest                        name + version (required beside payload/)
    payload/CLAUDE.md                       agent instructions placed into the repo
    payload/.omakase/gates/block-marker.sh  the gate
    payload/lefthook-local.yml              wires the gate onto pre-commit

Everything else the wiring uses comes from the **omakase base harness**, layered in
underneath at install: `omakase-gate.sh` (the one gate primitive: it runs the check, records
the run, and passes the exit code through) and the worktree auto-install. That is why this
directory is so small.

## Try it

This directory is a complete harness, and a harness can live in a **subfolder** of a git
repo — so it is adoptable straight from GitHub. From any project:

    omakase init Yuncun/omakase-harness/examples/sample-harness    # Claude Code or Copilot CLI
    # or, in a plain shell:
    bash <path-to>/omakase-harness/bin/init.sh Yuncun/omakase-harness/examples/sample-harness

From a local clone of this repo, the same install is

    omakase init --source <path-to-clone>//examples/sample-harness

where the `//` marks where the repo ends and the subfolder begins.

Now:

- a commit that stages a file containing the `DO NOT COMMIT` marker is **blocked**;
- a clean commit **passes**;
- `omakase status` lists the `block-marker` gate;
- `omakase remove` deletes it all and restores the repo.

## Make your own

Copy this directory into a git repo of your own — its own repo, or a subfolder of one you
already have — edit the three files under `payload/`, push, and share. People adopt it with
`omakase init you/your-repo` (add `/path/to/harness` when it lives in a subfolder).
