---
name: add-gate
description: Wire a tool, skill, or check to run on a git hook as an omakase gate. Use when asked to "add a gate", "attach/wire a tool or skill to a hook", "run X on pre-commit/pre-push", "gate on a linter/test/reviewer", or "make sure X runs before commit/push". Covers picking the keys, the pre-flight checks that decide whether a third-party tool can even be gated, and the wiring. Run from a harness clone (it edits payload/), not an adopter repo.
---

# /add-gate — attach a tool to a git hook

You are editing a **custom harness** (a clone of omakase-harness or your own harness
repo), adding a gate to its `payload/`. You are NOT editing an installed
overlay — edits to an injected copy are overwritten on the next `init`. Confirm you are in
the harness repo (it has a `payload/` tree and an `omakase.manifest`); if you are in an
adopter repo, stop and switch to the harness clone first.

A **gate** is a check omakase runs at a git hook (pre-commit or pre-push), declared as a
`gate:` block in `payload/omakase.manifest`. The check itself is a plain script or command:
exit non-zero blocks, exit 0 allows. Pick the keys **before** writing anything; most mistakes
come from picking the wrong combination.

## Step 1 — pick the keys by asking (don't guess)

Work out the keys with the user, one question at a time, multiple-choice where you can. Do
not move on until the answer is clear. Three questions settle the block:

1. **What are you gating, and on which event?** The tool / skill / command, and whether it
   fires on pre-commit or pre-push (`hook:`). (Slow checks belong on pre-push.)
2. **Can it run inline every time, or is it expensive or out-of-band?**
   - Runs fast and deterministically inline, exits with a real code (a linter, a compiler,
     a test suite): just `run:`, no extra key.
   - Runs inline but is slow enough that re-running on every commit wastes time: add
     `cacheable: true` (a recorded PASS is reused for the same commit until HEAD moves).
   - Cannot run inside a hook at all (slow, non-deterministic, or requires human/agent
     judgment): add `cacheable: true` plus a blocking `run:` that refuses the push; the check
     runs out of band, then records its result with `omakase record <name>`.
3. **Does it only apply to some paths?** If yes, add `glob: <space-separated globs>`; the
   gate is skipped when no changed file matches.

Read the key set back in one line and confirm before wiring. Example: *"Pre-push, a blocking
`run:` + `cacheable: true` (out-of-band judgment), `glob: src/*`. Wiring it?"*

## Step 2 — pre-flight a third-party tool (the part people skip)

Before wiring a tool you do not own (a marketplace skill, a creator's skill, a CLI), check
all five. A "no" on 1–3 usually means the tool **cannot** be a gate as-is: change the
approach or the tool, do not force it.

1. **Agent-invocable non-interactively?** Some skills are interactive-only or set
   `disable-model-invocation: true`. If a job can't drive it headlessly, it can't be a
   gate. (This is why the Anthropic code-review plugin can't be a gate here.)
2. **Emits a machine verdict, or only a human report?** A gate blocks on a non-zero exit,
   so it needs an exit code or a parseable result. If the tool only writes prose,
   either (a) use the out-of-band pattern (a blocking `run:` an agent or human clears with
   `omakase record` after reading the findings), or (b) have the `run:` apply *its own* thin
   pass/fail rule, and don't pretend the tool emits one it doesn't.
3. **Does its output path work in THIS repo?** A reviewer that posts to GitHub is inert in an
   Azure-DevOps repo; a check that needs a service you don't run is dead. Confirm the result
   actually lands somewhere usable here.
4. **Deterministic and fast enough to run inline every time?** Decides the keys: yes → just
   `run:`; slow but runnable → add `cacheable: true`; can't run in a hook → `cacheable: true`
   + a blocking `run:` cleared out of band with `omakase record`.
5. **Safe to depend on, with an off-switch?** You will DEPEND on it, not copy it (see Step 3).
   Make sure it has an escape hatch and won't wedge a commit.

## Step 3 — wire it

**Depend, don't copy.** Install/keep the tool as a dependency and invoke it. Never paste a
third-party tool's files into `payload/` — you own the threshold, not the tool.

Add a `gate:` block to `payload/omakase.manifest`. Put any gate script under
`payload/.omakase/gates/` (a plain script — exit non-zero blocks); a `run:` whose first token
is a payload path (`.omakase/…` or `gates/…`) must be an executable the harness ships, or
`init` refuses. A first token that is not a payload path (`go`, `npm`, `bash …`) is your own
command, resolved from `PATH`.

```
gate: <name>                      # pre-commit or pre-push
  hook: pre-commit
  run: .omakase/gates/<name>.sh   # or a bare command, e.g. `go test ./...`
  # cacheable: true               # add if the check is expensive (reuses a PASS for the same commit)
  # glob: src/* docs/*            # add if the gate applies only to some paths
```

For a check that cannot run inside a hook (out-of-band review, LLM judgment): use a blocking
`run:` that always refuses, add `cacheable: true` so a recorded pass unblocks the re-push, and
run `omakase record <name>` out of band (from the job or skill that ran the actual check) once
it passes.

```
gate: <name>
  hook: pre-push
  cacheable: true
  run: echo "run the <name> check, then: omakase record <name>" && exit 1
  # glob: src/*                   # add if the gate applies only to some paths
```

The per-gate escape hatch is `OMAKASE_SKIP_<NAME>=1` (name upper-cased, `.`/`-`→`_`), audited
in the run ledger; `OMAKASE_SKIP_GATES=1` skips every gate once.

## Step 4 — prove it fires

Test before you publish. In a throwaway repo, inject the payload, then make a change that
should trip the gate and one that shouldn't:

```bash
cd "$(mktemp -d)" && git init -q && git commit -q --allow-empty -m init
OMAKASE_PAYLOAD=<your>/payload bash <base-harness>/bin/init.sh
# inline gate: stage a violating file, attempt commit, see it block, fix, see it pass.
# out-of-band gate: touch a matching file, attempt push -> blocked (run: exits non-zero);
#   run the check and `omakase record <name>`; attempt push -> allowed (cached PASS).
OMAKASE_PAYLOAD=<your>/payload bash <base-harness>/bin/remove.sh    # reset
```

Then, if the harness lists its gates in a guard table (README / docs), add the new one there;
and for `--source` harnesses, leave the root `omakase.manifest` alone unless the gate needs a
new `recommends:`.

## See also

- [authoring.md](../../docs/authoring.md) — "Adding a gate", "Wrapping a third-party check".
- [concepts.md](../../docs/concepts.md) — gates, owned vs shared dirs.
