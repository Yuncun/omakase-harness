---
name: add-gate
description: Wire a tool, skill, or check to run on a git hook as an omakase gate. Use when asked to "add a gate", "attach/wire a tool or skill to a hook", "run X on pre-commit/pre-push", "gate on a linter/test/reviewer", or "make sure X runs before commit/push". Covers picking the gate shape, the pre-flight checks that decide whether a third-party tool can even be gated, and the wiring. Run from a harness clone (it edits payload/), not an adopter repo.
---

# /add-gate — attach a tool to a git hook

You are editing a **custom harness** (a clone of omakase-harness or your own harness
repo), adding a gate to its `payload/`. You are NOT editing an installed
overlay — edits to an injected copy are overwritten on the next `init`. Confirm you are in
the harness repo (it has `payload/` and `omakase.manifest`); if you are in an adopter repo,
stop and switch to the harness clone first.

A **gate** is a check wired into a git hook (pre-commit or pre-push). omakase has two
shapes — a **gate** that runs in the hook, and a **deferred gate** that checks a job ran
earlier. Settle the shape **before** writing anything; most mistakes are a wrong-shape choice.

## Step 1 — find the shape by asking (don't guess)

Work the shape out *with the user*, the way brainstorming does: **one question at a time**,
multiple-choice where you can, and don't move on until the answer is clear. Don't paste the
decision tree at them and don't silently pick. Three questions settle it:

1. **What are you gating, and on which event?** The tool / skill / command, and whether it
   fires on pre-commit or pre-push. (Slow checks belong on pre-push.)
2. **Can it run while you wait?** *"Fast and deterministic with a real exit code — a linter,
   compiler, test, script — so the hook can run it inline? Or slow / non-deterministic / a
   judgment call (a render, an LLM review) that can't run in a hook?"*
   - fast + deterministic + exit code → **gate**
   - slow / non-deterministic / judgment → **deferred gate**
3. **(deferred only) What should block the push?** *"Should a failing result block — or do
   you only need proof it ran, with a human or agent reading the findings?"*
   - block on failure → the job records real pass/fail
   - proof-it-ran → the job records success whenever it ran

Read the plan back in one line and get a yes before wiring — e.g. *"Deferred gate on
pre-push: a job runs `<tool>`, records pass/fail, the push blocks on fail. Wiring it?"*

**The two shapes:**

- **Gate** — runs inside the hook, while you wait. Good for `detekt`, `ktlint`, a compile,
  a unit subset — anything quick and deterministic. The hook runs it; a non-zero exit blocks.
- **Deferred gate** — for checks too slow or non-deterministic to run inside a hook. A *job*
  runs in-session and records a result keyed to the commit; the hook only READS that record
  at push and blocks unless the job recorded success for the commit. What counts as success
  is the job's call:
  - **block on failure** — the job records real pass/fail and the push blocks on a fail
    (waiver path included). `visual-verify` is the worked example: a blank or crashed screen
    is an objective fail.
  - **proof-it-ran** — the job **always records success**, so the only thing the hook
    enforces is "you ran it for this commit," trusting the human or agent to act on the
    findings. `review-verify` is the worked example.

## Step 2 — pre-flight a third-party tool (the part people skip)

Before wiring a tool you do not own (a marketplace skill, a creator's skill, a CLI), check
all five. A "no" on 1–3 usually means the tool **cannot** be a gate as-is — change the shape
or the tool, do not force it.

1. **Agent-invocable non-interactively?** Some skills are interactive-only or set
   `disable-model-invocation: true`. If a job can't drive it headlessly, it can't be a
   gate. (This is why the Anthropic code-review plugin can't be a gate here.)
2. **Emits a machine verdict, or only a human report?** A gate, or a deferred gate that
   blocks on failure, needs an exit code or a parseable result. If the tool only writes
   prose, either (a) make it a proof-it-ran deferred gate (no verdict needed), or (b) have
   the job apply *its own* thin pass/fail rule — don't pretend the tool emits one it doesn't.
3. **Does its output path work in THIS repo?** A reviewer that posts to GitHub is inert in an
   Azure-DevOps repo; a check that needs a service you don't run is dead. Confirm the result
   actually lands somewhere usable here.
4. **Deterministic?** Decides Step 1: deterministic → gate; not → a deferred gate.
5. **Safe to depend on, with an off-switch?** You will DEPEND on it, not copy it (see Step 3).
   Make sure it has an escape hatch and won't wedge a commit.

## Step 3 — wire it

**Depend, don't copy.** Install/keep the tool as a dependency and invoke it. Never paste a
third-party tool's files into `payload/` — you own the threshold, not the tool.

### Gate
Add a script under `payload/.omakase/gates/<name>.sh` (or call the tool directly) and a job
in `payload/lefthook-local.yml`:

```yaml
pre-commit:
  jobs:
    - name: <name>
      run: bash .omakase/bin/omakase-ledger.sh <name> -- <your command>   # ledger = scorecard
      env: { OMAKASE_HOOK: pre-commit }
```

### Deferred gate
Two pieces:

1. **A job** — a skill (or script) the agent runs at done-time. It runs the tool, then
   records a result with the reusable recorder:
   ```bash
   .omakase/bin/omakase-record.sh --check <name> --verdict pass    # proof-it-ran: always pass
   # block-on-failure: --verdict pass|fail, plus --reason on a waiver
   ```
   Model the job on `payload/.github/skills/visual-verify` (block-on-failure) or
   `review-verify` (proof-it-ran). Keep it thin — run-tool-then-record.
2. **A hook job** pointing the generic push-gate at the verdict by name:
   ```yaml
   pre-push:
     jobs:
       - name: deferred-check-<name>
         run: bash .omakase/bin/omakase-ledger.sh <name> -- bash .omakase/gates/deferred-check.sh
         env:
           OMAKASE_CHECK: <name>          # matches --check above; UNSET = gate dormant
           OMAKASE_GLOB: '<paths>'        # gate fires only when a pushed file matches
           OMAKASE_HOOK: pre-push
   ```
   `deferred-check.sh` blocks a push when the record is missing/stale (and, when the job
   records pass/fail, when the verdict is fail without a waiver). When the job always records
   pass, the only block is "never ran for this commit." The per-check escape hatch is
   `OMAKASE_SKIP_<NAME>=1` (name upper-cased, `-`→`_`).

> The reusable `deferred-check.sh` (push gate) and `omakase-record.sh` (recorder) ship in the
> base payload at `payload/.omakase/{gates/deferred-check.sh,bin/omakase-record.sh}`, with a
> commented wiring example in `payload/lefthook-local.yml`. A fork inherits them — depend on
> them, don't re-implement them.

## Step 4 — prove it fires

Test before you publish. In a throwaway repo, inject the payload, then make a change that
should trip the gate and one that shouldn't:

```bash
cd "$(mktemp -d)" && git init -q && git commit -q --allow-empty -m init
OMAKASE_PAYLOAD=<your>/payload bash <base-harness>/bin/init.sh
# gate: stage a violating file, attempt commit, see it block, fix, see it pass.
# deferred gate: touch a file matching OMAKASE_GLOB, attempt push -> blocked (no record);
#   run the job (records the result); attempt push -> allowed.
OMAKASE_PAYLOAD=<your>/payload bash <base-harness>/bin/remove.sh    # reset
```

Then, if the harness lists its gates in a guard table (README / docs), add the new one there;
and for `--source` harnesses, leave `omakase.manifest` alone unless the gate needs a new
`recommends:`.

## See also

- [authoring.md](../../docs/authoring.md) — "Adding a gate", "Wrapping a third-party check".
- [concepts.md](../../docs/concepts.md) — gates and deferred gates, owned vs shared dirs.
- Worked-example shapes: a deferred gate that blocks on failure, and a deferred gate that just records proof it ran.
