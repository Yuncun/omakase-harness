# omakase-gate: one gate primitive — design

- Date: 2026-06-27
- Status: Proposed (awaiting review)
- Supersedes: the three-script `realistic-harness` example spec (folded in here as a showcase section).

## 1. Goal

Replace the three-script gate machinery with **one primitive**, `omakase-gate.sh`, and clean every
piece of staleness that touches it. The product goal: keep an agent running without stopping the
session for bookkeeping, behind a single mental model an author can hold at once.

Today there are three scripts and two stores for one idea ("did check X pass for this commit?"):

- `gates/example.sh` — a gate that runs a check inline.
- `gates/deferred-check.sh` — a gate that does NOT run a check; it reads a receipt.
- `bin/omakase-ledger.sh` — wraps a gate, records a run row in `ledger.tsv` (the scorecard).
- `bin/omakase-record.sh` — writes the receipt (`deferred/<name>.json`) the deferred gate reads.

A "deferred gate" is just a memoized step whose cache-miss action is "block" instead of "run". So one
primitive, parameterized by flags, covers every case.

### Locked decisions (from brainstorming)

- **Clean break**, no shims. omakase is 0.x; a breaking change is fair.
- **Waiver removed entirely.** `--record` writes only a pass. No `--verdict`, `--reason`, or
  `original_verdict`, and no WAIVED banner. The single audited bypass is `OMAKASE_SKIP_<NAME>=1`.
- **Scope: unify + clean adjacent staleness** — also drop the unused `ms` column, drop the never-set
  `--base`/`OMAKASE_BASE` knob, cut `show.sh`'s degraded duplicate renderer, and de-collide the
  "ledger" naming.

## 2. The primitive (contract)

`bin/omakase-gate.sh`. Two modes, three options.

```
omakase-gate.sh <name> --step '<cmd>' [--cacheable] [--glob '<pats>']
omakase-gate.sh <name> --record          # out-of-band: write a pass for HEAD, no step
```

- `<name>` (required) — the gate's scorecard name; with the HEAD sha it is the cache key.
- `--step '<cmd>'` — the check, run via the shell. exit 0 = pass, non-zero = block. Required unless `--record`.
- `--cacheable` — a fresh pass for the exact HEAD short-circuits and skips the step.
- `--glob '<pats>'` — space-separated case-globs (a single `*` spans directories). If set and no changed
  file in the range matches, skip. **Absent `--glob` = always in scope (always run).**
- `--record` — append a pass row for HEAD and exit 0; no step runs. **Fails loudly** (see §4).

Behavior, in order:
1. `--record`: append a pass row for HEAD; on write failure, print an error and exit non-zero; else exit 0.
2. Escape hatch: if `OMAKASE_SKIP_<NAME>=1` (name upper-cased, `-`→`_`), print an audited note, exit 0.
3. `--glob` set: resolve the range base (fail-OPEN — unresolvable base → skip, never a raw git error);
   diff `BASE...HEAD` with a two-dot fallback for unrelated histories; if no changed file matches a
   pattern (word-split under `set -f`), skip.
4. `--cacheable`: if a fresh pass row exists for HEAD, skip.
5. Run `--step`. Record one run row (best-effort). exit 0 → pass; non-zero → exit that same code (block).
   For `--cacheable`, a passing step's row IS the receipt.

Exit codes: `0` = pass or skipped; `N` = the step's own non-zero code (passed through unchanged); `2` = misuse.

Env: `OMAKASE_SKIP_<NAME>` (audited bypass, uniform for every gate), `OMAKASE_NOW` (test hook only).
**Removed:** `OMAKASE_CHECK`, `OMAKASE_GLOB`, `OMAKASE_BASE`, `OMAKASE_HOOK`.

Carried-over safety that must not regress: resolve the git-common-dir BEFORE running the step (a step
that `cd`s cannot misdirect its row; an empty rev-parse never becomes `cd ""`); strip tab/newline from
`name` and `sha` so a hostile name cannot shift TSV columns; `set -uo pipefail` (NOT `-e`) so the step's
exit code is captured, not fatal.

## 3. The three cases (all from flags)

```
# (1) always-run check — no --cacheable, runs every time
omakase-gate.sh markers --step 'bash .omakase/gates/example.sh'

# (2) expensive but runnable, cached per commit — runs once per HEAD, reuses the pass
omakase-gate.sh tests --cacheable --glob 'src/* lib/*' --step '<your check, e.g. make check>'

# (3) out-of-band / human or agent sign-off — the step blocks; an out-of-band --record unblocks
omakase-gate.sh review --cacheable --glob 'src/*' \
  --step 'echo "BLOCKED: run your review, then: omakase-gate.sh review --record" >&2; exit 1'
```

Case 3 flow: push → in scope, no fresh pass → the blocker step runs, records a fail row, blocks. The
human or agent runs the real check out of band, then `omakase-gate.sh review --record` appends a pass for
HEAD. Re-push (same HEAD) → fresh pass found → step skipped → allowed. A new commit moves HEAD, the pass
is stale, the gate re-blocks. Deferment needs **zero** special code: it is `--cacheable` + a blocker step.

**Known semantics (documented, not a bug):** `--cacheable` means "trust the last pass for this exact
commit." Editing the check (in the gitignored `lefthook-local.yml`, which does not change HEAD) does NOT
bust the cache, and a pass is reused across worktrees at the same sha. `--cacheable` is therefore for
checks whose result depends only on the committed code. Checks that depend on the working tree or the
environment should be left non-cacheable (they run every time).

## 4. One store: `ledger.tsv`

The per-commit receipt and the scorecard collapse into one append-only file in the **shared**
git-common-dir (so all worktrees share one run history and one cache; correct because the cache key is
the commit sha).

- Row schema: `epoch <tab> name <tab> verdict <tab> sha` (4 columns; `ms` and `hook` are removed).
- Scorecard = the ledger: latest verdict per name.
- Receipt query = the ledger: a fresh pass for HEAD is "a row where name==N && sha==HEAD && verdict==pass"
  (latest-wins; `--cacheable` short-circuits before a later fail can be appended for the same sha).
- **Load-bearing, fail-closed.** Run-recording (step path) stays best-effort: a dropped run row just
  means the next push re-runs. But `--record` is the ONLY signal an out-of-band check passed, so it
  **fails loudly** — a failed `--record` write exits non-zero and says so, never a silent exit 0.
- **Atomic append.** Build the full row in a variable and append it in a single `printf >> file`
  (one write, O_APPEND). Rows are short (< PIPE_BUF), so concurrent appends under `parallel: true` do not
  tear. (Networked filesystems are out of scope for v1; the store is a local per-clone git dir.)
- **Upgrade.** `init.sh` currently preserves `ledger.tsv` across re-init; old 6-column rows misparse under
  the 4-column reader. On init, detect a pre-v2 ledger (row column count) and rotate it aside
  (`ledger.tsv.pre-v2.bak`) so the new store starts clean. It is disposable per-clone run history. The
  cache itself is already safe across the change (an old `$4` verdict string is never a 40-hex sha, so a
  freshness query fails closed), but the scorecard display needs the rotation.

## 5. Deletions and renames (the cut list)

Delete: `gates/deferred-check.sh`, `bin/omakase-ledger.sh`, `bin/omakase-record.sh`; the
`deferred/<name>.json` receipt store, its JSON format, the sed parser, the `deferred/` dir, and the
mktemp+mv dance; the entire waiver mechanism (`--original-verdict`, `original_verdict`, the
require-reason validation, the WAIVED banner); `--reason`/`reason`; `--verdict`; the `ms` column and its
timing; the `hook` column + `OMAKASE_HOOK`; `--base`/`OMAKASE_BASE`; dormancy-via-unset-`OMAKASE_CHECK`;
fail-closed-on-unset-glob (the new contract makes no `--glob` a valid always-run config).

Keep: `gates/example.sh` as the one shipped gate body (a real, generic merge-conflict check whose
no-false-block-on-bare-`=======` subtlety deserves a commented file), wired via the primitive;
`OMAKASE_SKIP_<NAME>` as the single uniform audited bypass; `gates/` as the OPTIONAL home for multi-line
gate bodies (simple checks become inline `--step`).

Rename (de-collide "ledger"): today `ledger.tsv` is the run history and `placed.tsv` is the provenance
list, but `tests/ledger.test.sh` tests **placed.tsv** while `tests/scorecard.test.sh` tests
**ledger.tsv** — crossed. Fix: `ledger.tsv` stays the run/scorecard store (now also the cache);
`tests/ledger.test.sh` → `tests/placed.test.sh` (it tests provenance); `tests/scorecard.test.sh` folds
into the new `tests/omakase-gate.test.sh`. After this, no test file named "ledger" remains and "ledger"
means exactly one file.

## 6. Consumers to rewrite (the blast radius, all in-scope)

- **`bin/show.sh`** — switch the name regex from `omakase-ledger.sh <name>` to `omakase-gate.sh <name>`;
  read the 4-column schema; show `name` + `verdict`, and for the "what it enforces" cell read only the
  safe fixed-token flags (`--cacheable` present? `--glob`'s value?) — NOT the quoted `--step` body, which
  can contain spaces, quotes, `;`, and even a literal `--record`. Delete the basename→phrase table (the
  `worktree-discipline.sh` and `deferred-check.sh` branches; their friendly phrases go away for everyone,
  including pixterm, which is an accepted cosmetic change). **Cut**
  `render_guards_fallback` (~60 lines): when lefthook is unresolved no gates run anyway, so replace it
  with a one-line "lefthook not resolved — gates are not running" note.
- **`payload/.omakase/bin/omakase-stop-notice.sh`** — a hard consumer of the 6-column schema (reads
  `$2`/`$6`, filters `NF>=6`). Rewrite for 4 columns and group the latest run by sha alone. Accept the
  cosmetic loss of the "Pre-push" label (lefthook exposes no hook name to a job, so keeping it would cost
  the per-job `env` block this redesign is removing) and the rare `--amend`/push-parent mis-group on this
  opt-in surface.
- **`bin/init.sh` / `tools/build.sh`** — the fail-closed wiring guard does
  `sed 's/#.*//' | grep -oE '\.omakase/...\.sh'` over the whole line. Make it `--step`-aware: verify only
  a `.omakase/*.sh` path that is the gate body the `--step` actually invokes, and do not let a `#` inside
  a quoted `--step` value truncate the scan. Also run the guard on the plain (non-`--source`) install
  path, not only the merged-source path. Fix the two stale comments at `init.sh:165,584`.
- **`payload/lefthook-local.yml`** and **both example payloads** (`sample-harness`, `superpowers-harness`)
  — rewrite every job from the wrapper form to one `omakase-gate.sh` call; update the README prose that
  names the deleted scripts. A missed one ships a hook referencing a nonexistent script (cryptic exit
  127), so a `grep` sweep for `omakase-ledger.sh|omakase-record.sh|deferred-check.sh` across
  `payload/`, `examples/`, and `tests/` is part of acceptance.
- **Docs** — rewrite `skills/add-gate/SKILL.md` around the one primitive and the three flag-combos;
  delete the "two shapes / scoped-vs-complete checker" taxonomy and the dangling
  `visual-verify`/`review-verify` references (those dirs do not exist here). Update `docs/authoring.md`,
  `docs/harness-surface.md` (gate classification), `docs/concepts.md` if present, and `CHANGELOG.md`.

## 7. The realistic-harness example (showcase, rides on top)

A third `examples/` entry, kept tight, demonstrating the primitive end to end and cross-tool. README
leads with "what changes for you" (the table below), not "how it works".

| Moment | Without | With it installed |
|---|---|---|
| `git push` | always goes | a push touching a guarded path is refused unless `review` has a fresh pass for that exact commit |
| Agent | no conventions/ritual | `AGENTS.md` (both tools) says run review before pushing; the agent self-satisfies the gate |
| `git commit` | any commit | a staged merge-conflict marker is blocked by the always-run gate |
| Companion plugin | install by hand | superpowers auto-enables (trust-gated, can fail silently; `recommends:` is the fallback) |
| Visibility | nothing | `omakase status` shows the gates and the latest `review` verdict |
| Others | — | see nothing; overlay is gitignored; `omakase remove` restores the repo |

Files (delta only): `omakase.manifest` (with a single-line `recommends:`), `README.md`,
`payload/AGENTS.md` (cross-tool instructions), `payload/.claude/skills/review-verify/SKILL.md` +
`payload/.github/skills/review-verify/SKILL.md` (same skill, two paths), `payload/.claude/settings.json`
+ `payload/.github/copilot/settings.json` (enable superpowers), `payload/lefthook-local.yml`. Wiring:
an always-run pre-commit gate (`example.sh`) and a cacheable pre-push `review` gate whose step blocks and
whose skill records via `omakase-gate.sh review --record`. PII-free and project-agnostic throughout.

## 8. Test plan

One behavioral file, `tests/omakase-gate.test.sh` (absorbs `deferred-gate.test.sh` + `scorecard.test.sh`).
Irreducible checklist:
- Case 1 always-run: step 0 → pass + a pass row; step non-zero → block with that code.
- Conflict-marker check (migrated from `inject.test.sh` scenario G): blocks a `<<<<<<<`/`>>>>>>>` pair,
  does not false-block a lone `=======` heading underline, passes clean input.
- `--cacheable` freshness: no row → runs; pass recorded → next run skips; HEAD moved → re-block (stale).
- `--record`: writes a pass for HEAD with no step; a subsequent `--cacheable` run skips; **a failed write
  exits non-zero and says so** (fail-loud).
- Deferment (case 3): blocker step blocks; after `--record` the next push at the same HEAD allows.
- `--glob`: match runs; miss skips; **no `--glob` runs every time** (the positive assertion replacing the
  deleted fail-closed-on-unset-glob test).
- Base fail-open: no resolvable base → skip (never a raw git error). Two-dot fallback on unrelated histories.
- `OMAKASE_SKIP_<NAME>=1` → skip even when it would block.
- Scorecard: a run appends `epoch,name,verdict,sha`; latest-verdict-per-name is queryable.
- Concurrency: N parallel appends yield N complete (untorn) rows.
- Upgrade: a pre-v2 (6-column) ledger is rotated aside on init; the new store starts clean.
- End-to-end: a real `git push` through the installed pre-push hook — blocked unreviewed, the run lands in
  the ledger, allowed after `--record`, and `omakase status` renders the gate row.

Fixture-only tests (`inject`, `safety`, `sources`, `build`, `harness-paths`, `recommends`,
`copilot-exclude-scope`, `sample-harness`, `superpowers-harness`): rewrite the embedded lefthook fixture
to the `omakase-gate.sh <name> --step ...` form and update base-machinery presence assertions
(`omakase-gate.sh` present; `omakase-ledger.sh`/`omakase-record.sh`/`deferred-check.sh` gone). Rename
`tests/ledger.test.sh` → `tests/placed.test.sh`. Final `grep` sweep for the three deleted names.

Acceptance: full suite green on macos-latest and ubuntu-latest (CI); fresh install→commit→push e2e works;
re-init over an old install leaves no broken hook and rotates the old ledger.

## 9. pixterm-harness migration (separate PR, separate repo)

pixterm-harness is a live consumer and is NOT changed by this PR. Its own follow-up PR:
- Re-wire every `lefthook-local.yml` job to `omakase-gate.sh`; delete `OMAKASE_HOOK` env blocks; move
  `OMAKASE_CHECK`→name, `OMAKASE_GLOB`→`--glob`; add `--cacheable` to gates that used `deferred-check.sh`.
- Replace `omakase-record.sh --check X --verdict pass [--reason ...]` calls in the `review-verify` and
  `visual-verify` skills with `omakase-gate.sh X --record` (the `--verdict fail`/`--reason` path is gone;
  to push past a known failure use `OMAKASE_SKIP_<NAME>=1`, documented in the PR), and update their
  `allowed-tools` pins.
- Retire the bespoke in-script skip vars (`SKIP_WORKTREE_CHECK`, `SKIP_ADR_CHECK`) in favor of the
  uniform `OMAKASE_SKIP_<NAME>`.
- Delete old `deferred/*.json` receipts and let init rotate the old ledger.

## 10. Non-goals / open items

- No ledger rotation for size growth in v1 (only the one-time schema-upgrade rotation); the latest-by-sha
  scan is cheap.
- Networked-filesystem concurrency guarantees are out of scope.
- `--base` is dropped; re-add as a flag only when a real adopter needs a non-default range base.
