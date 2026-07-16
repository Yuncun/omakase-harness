# Gate module: manifest-declared gates replace lefthook

**Status: design for review, 2026-07-15.** Locked decisions below; implementation has not
started. Supersedes the gate-wiring half of `docs/v2-design.md` where they disagree.

## Goal

Make "a gate" omakase's own concept, declared in `omakase.manifest` and run by the omakase
binary, so no part of the product knows any third-party runner's file format. Today six
places know lefthook's config format (init validation, hook runtime, `status/guards.go`,
`tui/gaterows.go`, `internal/lefthook`, `bin/lib-lefthook.sh`) — roughly 1,200 source lines
plus ~500 test lines that exist only because that knowledge leaked.

**Non-goals:** no change to what a gate can do, to the ledger format, to the dispatcher
hooks (#98), or to any consent/toggle behavior. No compatibility layer for old harnesses
(hard cut, see Migration). No Windows support change (gates were always shell).

## Locked decisions (2026-07-15)

1. **Schema** — gates are declared in `omakase.manifest`, not a separate file and not a
   directory convention.
2. **Authoring** — a gate is a plain script or command. The binary provides filtering,
   caching, audited skips, and ledger recording around it. `omakase-gate.sh` is deleted.
3. **Coexistence** — generic hook chaining: at init, a pre-existing hook file for a hook
   omakase owns is preserved and runs after omakase's gates. No lefthook-specific (or any
   manager-specific) cooperation code survives. Managers that own hooks via
   `core.hooksPath` (husky v9) still refuse — there is no hook file to chain.
4. **Migration** — hard cut. The release that ships this reads manifest gates only; init on
   a harness that still ships `lefthook-local.yml` refuses with migration instructions.

## What the user sees change

| Surface | Before | After |
|---|---|---|
| a commit in a gated repo | gates run, fail blocks | identical (same scripts, same block) |
| authoring a harness | `lefthook-local.yml` of `run: omakase-gate.sh …` lines | `gate:` blocks in `omakase.manifest`; gates are plain scripts |
| skip everything once | `LEFTHOOK=0 git commit` | `OMAKASE_SKIP_GATES=1 git commit` (audited, printed) |
| skip one gate once | `OMAKASE_SKIP_<NAME>=1` | unchanged |
| init in a repo with existing hooks | refused | file-based hooks preserved and chained; `core.hooksPath` managers still refused |
| init duration / trust | fetches + sha256-verifies a 3.4 MB lefthook binary per machine | nothing fetched beyond omakase itself |
| status / menu gate list | runs `lefthook dump`, pattern-matches output | reads the declaration directly |
| `git commit --no-verify` | bypasses everything | unchanged |

## Manifest schema

`omakase.manifest` stays a flat hand-parsed text file (no YAML library). Today it carries
`name:` and `version:`. A `gate:` line opens a block; indented `key: value` lines belong to
it until the next non-indented line.

```
name: starter-harness
version: 0.2.0

gate: block-marker
  hook: pre-commit
  run: gates/block-marker.sh

gate: go-test
  hook: pre-push
  run: gates/go-test.sh
  glob: *.go go.mod go.sum
  cacheable: true
```

| Key | Required | Meaning |
|---|---|---|
| `gate:` | yes | the gate's name: `[A-Za-z0-9._-]+`, unique within the manifest. The ledger/scorecard name and the `OMAKASE_SKIP_<NAME>` name (upper-cased, `.`/`-` → `_`) |
| `hook:` | yes | `pre-commit` or `pre-push` — the only stages omakase wires |
| `run:` | yes | a command line, executed via `sh -c` from the repo root |
| `glob:` | no | space-separated case patterns (a single `*` spans directories — same dialect as today). Gate runs only when a changed file in the range matches; absent = always in scope |
| `cacheable:` | no | `true`: a recorded PASS for the exact HEAD sha short-circuits the run |

**Validation at init (refuse = place nothing, unchanged invariant):** unknown keys, a
missing required key, a duplicate name, or a bad hook stage refuse the whole harness. If
`run:`'s first token is a path inside the harness (`gates/…`, `.omakase/…`), that file must
exist in the payload and be executable — the "nothing runs undeclared" check, moved from
the yml scan to the manifest. A first token that is not a payload path (e.g. `go`) is the
author's command and is accepted as-is.

**Which copy is read at hook time:** the manifest is read from the snapshot in the shared
zone (`.git/omakase/payload-snapshot`), written only by init. Editing the placed manifest in
the clone changes nothing until a bare `omakase init` re-consents to it. Gate *scripts* run
from the working copy, as they do today. This keeps the one-writer invariant on wiring: the
set of things that can run is fixed at init time.

## The gate module

One new package, `internal/gate`, owning the concept end to end. Everything below is a
direct port of `omakase-gate.sh`'s verified semantics (163 lines) into Go; the script is
then deleted.

```
gate.Load(omk string) ([]Gate, error)     // parse the snapshot manifest's gate blocks
gate.RunHook(hook string, io…) int        // run every gate declared for this stage
gate.Record(omk, name string) error       // out-of-band PASS for HEAD (deferred gates)
```

Per-gate run order, identical to the script:

1. **Audited skip** — `OMAKASE_SKIP_<NAME>=1` prints "skipped (audited)" and passes.
   New: `OMAKASE_SKIP_GATES=1` skips the whole stage the same visible way (replaces
   lefthook's `LEFTHOOK=0`).
2. **Menu skip** — a name listed in `.git/omakase/disabled-gates` (written by
   `omakase status`) skips visibly, persistent until re-enabled.
3. **Glob scope** — resolve a base ref (`origin/HEAD`, then `origin/master`, `origin/main`);
   diff `base...HEAD` (two-dot fallback for unrelated histories); skip only when no changed
   file matches. **No resolvable base = run unscoped** (#92: the threat model is omission).
4. **Cache** — `cacheable` + an existing `pass` row for this exact HEAD sha in
   `ledger.tsv` skips with "(cached)".
5. **Run** — `sh -c "<run>"` from the repo root with `GIT_DIR`/`GIT_WORK_TREE`/
   `GIT_COMMON_DIR` scrubbed (already done by `omakase hook`); append the verdict row;
   pass the exit code through unchanged. Any gate failing fails the stage.

**Ledger format unchanged:** `epoch \t name \t verdict \t sha`, append-only, in
`.git/omakase/ledger.tsv`. `probe.RunSummary` and the statusline already parse exactly
this; they do not change.

**Deferred gates** keep working: the pattern is `cacheable: true` plus a step that refuses;
the out-of-band PASS moves from `omakase-gate.sh <name> --record` to a plumbing verb,
`omakase record <name>` (fails loud on a write error, exactly like `--record` today).

Consumers all ask the module and never a runner:

```mermaid
flowchart LR
  MF["omakase.manifest (snapshot)"] --> GM["internal/gate"]
  GM --> HK["omakase hook — runs them"]
  GM --> ST["status / tui / mcp — list them"]
  GM --> IN["init — validates them"]
```

`status/guards.go`'s dump-scraping and `tui/gaterows.go`'s duplicated copy of it are
deleted; both surfaces render `gate.Load` output. The declared/wired distinction disappears
because declaration *is* wiring.

## Hook time, before and after

```mermaid
flowchart TB
  subgraph B["BEFORE"]
    b1["git commit"] --> b2[".git/hooks/pre-commit (dispatcher)"]
    b2 --> b3["omakase hook pre-commit"]
    b3 --> b4["lefthook run (exec + parse yml)"]
    b4 --> b5["bash omakase-gate.sh per gate"]
    b5 --> b6["the check"]
  end
  subgraph A["AFTER"]
    a1["git commit"] --> a2[".git/hooks/pre-commit (dispatcher, unchanged)"]
    a2 --> a3["omakase hook pre-commit"]
    a3 --> a4["internal/gate: filter · cache · skip · record"]
    a4 --> a5["sh -c the check"]
    a5 --> a6["chained: pre-commit.before-omakase, if preserved"]
  end
```

The dispatcher files and their fail-closed guard (#98) are untouched. The "pinned runner
missing" blocking point no longer exists — one fewer way a commit can block.

## Generic hook chaining

Applies only to the three hook names omakase owns (`pre-commit`, `pre-push`,
`post-checkout`). Hooks omakase never touches (`commit-msg`, …) keep working directly.

**At init:** for each owned hook name, if a hook file exists that is not an omakase
dispatcher (marker comment `# omakase dispatcher`), rename it to
`<name>.before-omakase` and record the preservation in the shared zone. Then write the
dispatcher as today. Idempotent: a dispatcher is never preserved (marker check), and an
existing `.before-omakase` is never overwritten by a second preservation.

**At fire time:** after omakase's own gates pass, `omakase hook` executes
`<name>.before-omakase` if present, forwarding stdin and arguments (pre-push's ref list on
stdin reaches it intact). Its exit code merges with the stage result — either side failing
blocks, which is exactly what git did before omakase arrived. For post-checkout the chained
hook runs after the heal and stays fail-open like the heal itself.

**At remove:** each preserved hook is restored to its original name. `placed.tsv`-style
exactness applies: remove restores precisely what init preserved.

```mermaid
sequenceDiagram
  participant G as git
  participant D as dispatcher
  participant O as omakase hook
  participant P as pre-commit.before-omakase
  G->>D: fires pre-commit
  D->>O: exec
  O->>O: run declared gates (block on failure)
  O->>P: exec preserved hook, stdin/args forwarded
  P-->>O: exit code
  O-->>G: nonzero from either side blocks
```

**What this deletes:** the lefthook-config cooperation in `overlay/hook.go` (hasMain /
hasLocal / `LEFTHOOK_CONFIG`) and the stock-git-lfs stub forwarding — a git-lfs hook is now
just another preserved file. A repo whose project genuinely uses lefthook keeps working:
its lefthook-installed hook files are preserved and chained, and they invoke the lefthook
the project's own developers installed. omakase never ships or fetches lefthook again.

**What still refuses at init:** `core.hooksPath` pointing anywhere but the standard hooks
dir (husky v9, and any manager that owns the hooks *directory* — there is no file to
chain), and a `package.json` `prepare` script that wires husky / simple-git-hooks (npm
install would overwrite the dispatchers back). The refusal message drops the "omakase does
not chain hook managers" line and names only these two causes.

**Incumbent scan changes:** `.before-omakase` joins `.sample` / `.old` as a skipped suffix;
the "existing non-lefthook hook" and "pre-commit stub" refusal classes become preservation
instead.

## Deletions

| Deleted | Lines |
|---|---|
| `internal/lefthook/` (fetch, pin, resolve + tests) | 313 + 555 |
| `bin/lib-lefthook.sh` (the same fetch in shell) | 172 |
| `status/guards.go` dump-scraping + `tui/gaterows.go` duplicate | 322 + 159 (replaced by thin `gate.Load` rendering) |
| lefthook cooperation + git-lfs forwarding in `overlay/hook.go` | ~150 of 374 |
| `payload/.omakase/bin/omakase-gate.sh` | 163 sh |
| `payload/lefthook-local.yml` + starter-harness's | both |
| lefthook checksums, machine-cache dir, release re-pin chore for it | — |

Estimated: ~1,700 lines out (tests included), ~350–450 in (`internal/gate` + tests +
chaining).

## Migration (hard cut)

- Ships as **v0.20.0** with the base payload and `examples/starter-harness` migrated in the
  same release. `pixterm-harness` and the work harness (`gim-home/yuncun/…`) each get a
  small follow-up PR: delete `lefthook-local.yml`, add `gate:` blocks; gates' scripts are
  unchanged except dropping the `omakase-gate.sh` wrapper invocation.
- Init on a harness that still ships `lefthook-local.yml` (or whose manifest declares no
  gates while a yml is present) refuses:
  `omakase: this harness declares gates in lefthook-local.yml, which omakase no longer
  reads. Declare them as gate: blocks in omakase.manifest (see <repo>/docs/) and delete the
  yml. Nothing was changed.`
- `omakase-gate.sh` was documented as a stability contract; this design retires that
  contract deliberately, with the refusal message as the migration pointer. All known
  consumers are ours.
- Repos already initialized keep working until their next `omakase init` (the dispatcher +
  new binary path reads the new snapshot); a bare re-init migrates them.

## Invariants: what changes, what holds

| | Before | After |
|---|---|---|
| blocking points | 5 (binary missing, runner missing, gate fails, init refusal, toggle refusal) | 4 — "runner missing" no longer exists |
| skip-all escape | `LEFTHOOK=0` | `OMAKASE_SKIP_GATES=1`, audited + printed |
| everything else in the fail-closed table | — | unchanged (`--no-verify`, `OMAKASE_SKIP_<NAME>`, bare init repair, remove) |
| one writer | init/remove write wiring | strengthened: gate list read from the init-written snapshot only |
| nothing runs undeclared | yml scan at init | manifest validation at init |
| green needs proof | probe tri-state | unchanged; `HooksInstalled` no longer has a runner to consider |
| zero committed footprint / exact undo | — | unchanged; preserved hooks are recorded and restored by remove |

## Testing

- `internal/gate` table tests port each behavior of `omakase-gate.sh` one-for-one: skip
  env, disabled-gates, glob match/no-match, no-base-runs-unscoped (#92), unrelated-history
  fallback, cache hit/miss, verdict rows, exit-code passthrough, hostile names (tabs in
  name/sha), `record` failing loud.
- Ledger compatibility: rows written by the module are byte-identical in shape to the
  script's; `probe.RunSummary` tests run against module-written ledgers.
- Chaining: preserved-hook exec (stdin forwarding for pre-push), either-side-blocks,
  remove-restores, re-init idempotency, `.before-omakase` skipped by the incumbent scan.
- End-to-end: the repo's own starter-harness install (self-hosted since #108) proves a
  blocked commit live, as PR #108 did.

## Open review items

1. The plumbing verb name for the deferred-gate PASS: `omakase record <name>` (proposed).
2. The skip-all env name: `OMAKASE_SKIP_GATES=1` (proposed, consistent with per-gate skips).
3. Chaining order: omakase gates first, then the preserved hook (proposed — cheap
   deterministic gates before a potentially slow foreign hook).
