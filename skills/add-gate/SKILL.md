---
name: add-gate
description: Wire a tool, skill, or check to run on a git hook as an omakase gate. Use when asked to "add a gate", "attach/wire a tool or skill to a hook", "run X on pre-commit/pre-push", "gate on a linter/test/reviewer", or "make sure X runs before commit/push". Covers picking the gate type, the pre-flight checks that decide whether a third-party tool can even be gated, and the wiring. Run from a harness clone (it edits payload/), not an adopter repo.
---

# /add-gate — attach a tool to a git hook

You are editing a **harness source** (a clone of omakase-harness or a fork like
omakase-android), adding a gate to its `payload/`. You are NOT editing an installed
overlay — edits to an injected copy are overwritten on the next `init`. Confirm you are in
the harness repo (it has `payload/` and `omakase.manifest`); if you are in an adopter repo,
stop and switch to the harness clone first.

A "gate" is a check wired into a git hook (pre-commit or pre-push). omakase has three
shapes. Pick the shape **before** writing anything — most mistakes are a wrong-shape choice.

## Step 1 — pick the shape

```
Is the check deterministic and fast, with a real exit code (linter, compiler, test, script)?
  └─ YES → PURE GATE. The hook runs it directly; exit non-zero blocks. Done. (see "Pure gate")
  └─ NO (slow / non-deterministic / LLM / needs human or agent judgment):
        Do you need it to PASS, or only to have RUN?
          ├─ must PASS (a render check, a security scan) → DEFERRED GATE (pass/fail + waiver)
          └─ only to have RUN, agent decides on the result → RAN GATE (deferred, always-pass)
```

- **Pure gate** runs inside the hook. Good for `detekt`, `ktlint`, a compile, a unit subset —
  anything quick and deterministic. The hook is the producer.
- **Deferred gate** is for checks too slow or non-deterministic to run inside a hook. A
  *producer* runs in-session and records a verdict keyed to the commit; the hook only READS
  the verdict at push. `visual-verify` is the worked example.
- **Ran gate** is a deferred gate whose producer **always records pass** — so the only thing
  it enforces is "you ran it for this commit." Use it for back-pressure ("make sure the
  agent runs the reviewer") while trusting the agent to act on what it found. `review-verify`
  is the worked example.

## Step 2 — pre-flight a third-party tool (the part people skip)

Before wiring a tool you do not own (a marketplace skill, a creator's skill, a CLI), check
all five. A "no" on 1–3 usually means the tool **cannot** be a gate as-is — change the shape
or the tool, do not force it.

1. **Agent-invocable non-interactively?** Some skills are interactive-only or set
   `disable-model-invocation: true`. If a producer can't drive it headlessly, it can't be a
   gate. (This is why the Anthropic code-review plugin can't be a gate here.)
2. **Emits a machine verdict, or only a human report?** A pure or pass/fail gate needs an
   exit code or a parseable result. If the tool only writes prose, either (a) make it a RAN
   gate (no verdict needed), or (b) have the producer apply *its own* thin pass/fail rule —
   don't pretend the tool emits one it doesn't.
3. **Does its output path work in THIS repo?** A reviewer that posts to GitHub is inert in an
   Azure-DevOps repo; a check that needs a service you don't run is dead. Confirm the result
   actually lands somewhere usable here.
4. **Deterministic?** Decides Step 1: deterministic → pure gate; not → deferred/ran.
5. **Safe to depend on, with an off-switch?** You will DEPEND on it, not copy it (see Step 3).
   Make sure it has an escape hatch and won't wedge a commit.

## Step 3 — wire it

**Depend, don't copy.** Install/keep the tool as a dependency and invoke it. Never paste a
third-party tool's files into `payload/` — you own the threshold, not the tool.

### Pure gate
Add a script under `payload/.omakase/gates/<name>.sh` (or call the tool directly) and a job
in `payload/lefthook-local.yml`:

```yaml
pre-commit:
  jobs:
    - name: <name>
      run: bash .omakase/bin/omakase-ledger.sh <name> -- <your command>   # ledger = scorecard
      env: { OMAKASE_HOOK: pre-commit }
```

### Deferred or ran gate
Two pieces:

1. **A producer** — a skill (or script) the agent runs at done-time. It runs the tool, then
   records a verdict with the reusable recorder:
   ```bash
   .omakase/bin/omakase-record.sh --check <name> --verdict pass    # ran gate: always pass
   # deferred (pass/fail) gate: --verdict pass|fail, plus --reason on a waiver
   ```
   Model the producer on `payload/.github/skills/visual-verify` (pass/fail) or
   `review-verify` (ran-only). Keep it thin — its job is run-tool-then-record.
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
   `deferred-check.sh` blocks a push when the record is missing/stale (and, for a pass/fail
   gate, when the verdict is fail without a waiver). For a ran gate the producer always
   records pass, so the only block is "never ran for this commit." The per-check escape hatch
   is `OMAKASE_SKIP_<NAME>=1` (name upper-cased, `-`→`_`).

> The reusable `deferred-check.sh` (push-gate) and `omakase-record.sh` (recorder) ship in a
> harness's payload. **omakase-android is the reference** — copy both from
> `omakase/payload/.omakase/{gates/deferred-check.sh,bin/omakase-record.sh}` if your harness
> doesn't ship them yet. (The base omakase-harness payload currently ships only a pure-gate
> example; see authoring.md.)

## Step 4 — prove it fires

Test before you publish. In a throwaway repo, inject the payload, then make a change that
should trip the gate and one that shouldn't:

```bash
cd "$(mktemp -d)" && git init -q && git commit -q --allow-empty -m init
OMAKASE_PAYLOAD=<your>/payload bash <engine>/bin/init.sh
# pure gate: stage a violating file, attempt commit, see it block, fix, see it pass.
# deferred/ran gate: touch a file matching OMAKASE_GLOB, attempt push -> blocked (no record);
#   run the producer (records the verdict); attempt push -> allowed.
OMAKASE_PAYLOAD=<your>/payload bash <engine>/bin/remove.sh    # reset
```

Then update the harness's guard table (README / docs) and, for `--source` harnesses, leave
`omakase.manifest` alone unless the gate needs a new `recommends:`.

## See also

- [authoring.md](../../docs/authoring.md) — "Adding a gate", "Wrapping a third-party check".
- [concepts.md](../../docs/concepts.md) — gates and producers, owned vs shared dirs.
- Worked examples in omakase-android: `visual-verify` (pass/fail) and `review-verify` (ran).
