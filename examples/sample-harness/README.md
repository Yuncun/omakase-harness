# sample-harness — a minimal omakase harness

A worked example of a harness: the smallest thing that places a real file and runs a real
gate. Read it to see how a harness is shaped, then copy it to make your own.

It carries only its own **delta**:

    omakase.manifest                        name + version (required beside payload/)
    payload/CLAUDE.md                       agent instructions placed into the repo
    payload/.omakase/gates/block-marker.sh  the gate
    payload/lefthook-local.yml              wires the gate onto pre-commit

Everything else the wiring uses — the banner, `omakase-ledger.sh`, the status-line — comes
from the **omakase base harness**, layered in underneath at install. That is why this
directory is so small.

## Try it

These files are the *contents* of a harness repo. omakase installs a harness from a git
repo, so put them in one first:

    cp -R <path-to>/omakase-harness/examples/sample-harness /tmp/sample-harness
    cd /tmp/sample-harness && git init -q && git add -A && git commit -qm "sample harness"

Then, from any project:

    /omakase init --source /tmp/sample-harness          # Claude Code or Copilot CLI
    # or, in a plain shell:
    bash <path-to>/omakase-harness/bin/init.sh --source /tmp/sample-harness

Now:

- a commit that stages a file containing the `DO NOT COMMIT` marker is **blocked**;
- a clean commit **passes**;
- `/omakase show` lists the `block-marker` gate;
- `/omakase remove` deletes it all and restores the repo.

## Make your own

Copy this directory, edit the three files under `payload/`, push it to a git repo, and
share the URL. People adopt it with `/omakase init --source <your-repo>`.
