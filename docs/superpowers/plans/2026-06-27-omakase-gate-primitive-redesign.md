# omakase-gate Primitive Redesign Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the three-script gate machinery (`omakase-ledger.sh`, `omakase-record.sh`, `deferred-check.sh`) with one primitive `omakase-gate.sh`, and clean every piece of staleness that touches it.

**Architecture:** One script, `payload/.omakase/bin/omakase-gate.sh`, parameterized by flags. A "deferred gate" becomes `--cacheable` + a blocking step (no special code). One append-only store, `.git/omakase/ledger.tsv` (4 columns: `epoch name verdict sha`), is BOTH the run scorecard and the per-commit cache. The change is sequenced so the test suite stays green after every task: build the new primitive additively first, flip the ledger schema and its readers atomically, then delete the old scripts once nothing references them.

**Tech Stack:** Bash (the primitive and all machinery), POSIX sh (hook-time generated scripts), lefthook (hook runner), awk/sed/grep (TSV parsing), git. Tests are plain bash scripts under `tests/` run on macos-latest and ubuntu-latest.

## Global Constraints

- **Clean break, no shims.** omakase is 0.x; a breaking change is fair. Do not add back-compat readers for the old 6-column schema except the ONE-TIME upgrade rotation in Task 5.
- **Waiver removed entirely.** No `--verdict`, no `--reason`, no `--original-verdict`, no `original_verdict`, no WAIVED banner. `--record` writes only a PASS. The single audited bypass is `OMAKASE_SKIP_<NAME>=1` (name upper-cased, `-`→`_`).
- **Ledger schema (new):** exactly four tab-separated columns `epoch <tab> name <tab> verdict <tab> sha`. The `ms` and `hook` columns are gone, and `OMAKASE_HOOK` is removed.
- **Removed env:** `OMAKASE_CHECK`, `OMAKASE_GLOB`, `OMAKASE_BASE`, `OMAKASE_HOOK`. **Kept env:** `OMAKASE_SKIP_<NAME>` (audited bypass), `OMAKASE_NOW` (test hook only, pins the epoch).
- **The primitive lives at** `payload/.omakase/bin/omakase-gate.sh` and is invoked in wiring as `bash .omakase/bin/omakase-gate.sh <name> ...`.
- **Carried-over safety that must NOT regress:** resolve the shared git-common-dir BEFORE running the step; an empty `git rev-parse` must never become `cd ""`; strip tab/newline from `name` and `sha`; use `set -uo pipefail` (NOT `-e`) so the step's exit code is captured, not fatal; run-recording is best-effort but `--record` fails loud.
- **Store location:** the SHARED git dir (`$(git rev-parse --git-common-dir)/omakase/ledger.tsv`), so all worktrees share one history and one cache (correct because the cache key is the commit sha).
- **No em-dashes in prose** in any file you write or edit. Plain words. Match the surrounding comment density and idiom of each file.
- **Acceptance for the whole change:** the full suite green on both OSes; a `grep -rE 'omakase-ledger\.sh|omakase-record\.sh|deferred-check\.sh'` across `payload/`, `examples/`, `tests/`, `bin/`, `skills/`, `docs/` (excluding `docs/superpowers/`) returns nothing; a fresh install→commit→push end-to-end works; re-init over an old install rotates the old ledger and leaves no broken hook.

---

## File Structure

**Created:**
- `payload/.omakase/bin/omakase-gate.sh` — the one primitive (Task 1).
- `tests/omakase-gate.test.sh` — behavioral spec for the primitive (Task 1).
- `tests/placed.test.sh` — renamed from `tests/ledger.test.sh` (Task 7).

**Modified:**
- `payload/lefthook-local.yml` — base wiring rewritten to `omakase-gate.sh` form (Task 4).
- `bin/show.sh` — 4-column reader, `omakase-gate.sh` name regex, flags-not-step ENFORCES cell, `render_guards_fallback` cut (Task 4).
- `payload/.omakase/bin/omakase-stop-notice.sh` — 4-column reader, group last run by sha (Task 4).
- `tests/scorecard.test.sh` — recorder scenarios removed, status-surface scenarios updated to 4-column (Task 4).
- `bin/init.sh` — ledger rotation, `--step`-aware wiring guard that runs on the plain path too, stale comments fixed (Task 5).
- `examples/sample-harness/payload/lefthook-local.yml`, `examples/sample-harness/README.md` — rewired to the primitive (Task 6).
- `tests/inject.test.sh`, `tests/sources.test.sh`, `tests/build.test.sh`, `tests/sample-harness.test.sh`, `tests/superpowers-harness.test.sh` — embedded lefthook fixtures and base-machinery presence assertions updated (Task 6, finalized Task 7).
- `skills/add-gate/SKILL.md`, `docs/authoring.md`, `docs/concepts.md`, `docs/harness-surface.md`, `CHANGELOG.md` — rewritten around the one primitive (Task 8).

**Deleted (Task 7):**
- `payload/.omakase/gates/deferred-check.sh`
- `payload/.omakase/bin/omakase-ledger.sh`
- `payload/.omakase/bin/omakase-record.sh`
- `tests/deferred-gate.test.sh`

**Kept unchanged:** `payload/.omakase/gates/example.sh` (the one shipped gate body, now the always-run case).

**Deviation from spec §5/§8 (deliberate, noted):** the spec says fold `tests/scorecard.test.sh` wholesale into `tests/omakase-gate.test.sh`. But `scorecard.test.sh` tests four surfaces unrelated to the gate (the statusline canary, the stop-notice, the show inventory, and branding) on top of the run-recorder. Folding all of that under a file named "omakase-gate" produces a misnamed ~500-line file, against the "simple and readable" mandate. Instead: the run-recorder and hardening cases move into `tests/omakase-gate.test.sh` (Task 1), and `scorecard.test.sh` is TRIMMED to its genuine status-surface scenarios (canary, stop-notice, show, inventory, branding) updated to 4 columns (Task 4). "scorecard" stays an accurate name because `ledger.tsv` is the scorecard. The de-collision still holds: `ledger.test.sh`→`placed.test.sh` (Task 7), and no test file named "ledger" remains.

---

## Task 1: The `omakase-gate.sh` primitive + its behavioral test

**Files:**
- Create: `payload/.omakase/bin/omakase-gate.sh`
- Test: `tests/omakase-gate.test.sh`

**Interfaces:**
- Consumes: nothing (foundational).
- Produces: the executable `omakase-gate.sh` with this contract, relied on by every later task:
  - `omakase-gate.sh <name> --step '<cmd>' [--cacheable] [--glob '<pats>']`
  - `omakase-gate.sh <name> --record`
  - Exit codes: `0` = pass or skipped; `N` = the step's own non-zero code (passed through); `2` = misuse.
  - Appends rows `epoch<TAB>name<TAB>verdict<TAB>sha` to `$(git rev-parse --git-common-dir)/omakase/ledger.tsv`.

This task builds the complete primitive in three TDD cycles (one commit each): the always-run core, then `--record` + `--cacheable`, then `--glob`. All cases live in one test file because they exercise one script with interdependent flag logic.

### Cycle A: always-run core (run, record, exit pass-through, skip var, misuse, hardening)

- [ ] **Step 1: Write the failing test file `tests/omakase-gate.test.sh`** (Cycle A section). Model the harness on the existing `tests/scorecard.test.sh` (same `pass`/`fail`/`newrepo` helpers, BSD-safe awk, `OMAKASE_NOW` pin). Write:

```bash
#!/usr/bin/env bash
# Behavioral spec for the ONE gate primitive (omakase-gate.sh). Exercises the real shipped
# script: the always-run case, --cacheable caching, --record, deferment, --glob scoping,
# the audited skip var, concurrency, run-recording, and an end-to-end git push. The store
# is one append-only TSV (epoch<tab>name<tab>verdict<tab>sha) in the shared git dir.
set -u
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
GATE="$HERE/../payload/.omakase/bin/omakase-gate.sh"
INIT="$HERE/../bin/init.sh"
SHOW="$HERE/../bin/show.sh"
PAY="$HERE/../payload"
LEFTHOOK="${LEFTHOOK_BIN:-$(command -v lefthook || true)}"
TMP="${TMPDIR:-/tmp}/omakase-gate-test.$$"
FAILED=0
pass(){ echo "  PASS: $1"; }
fail(){ echo "  FAIL: $1"; FAILED=1; }
newrepo(){ rm -rf "$1"; mkdir -p "$1"; ( cd "$1" && git init -q && git config user.email t@t && git config user.name t && git config commit.gpgsign false && git commit -q --allow-empty -m init ); }
ledger_of(){ echo "$(cd "$1" && cd "$(git rev-parse --git-common-dir)" && pwd)/omakase/ledger.tsv"; }
has_row(){ awk -F'\t' -v n="$2" -v v="$3" '$2==n && $3==v{f=1} END{exit f?0:1}' "$1"; }
export PATH="$(dirname "$LEFTHOOK"):$PATH"

echo "== Cycle A: always-run core =="
REPO="$TMP/repoA"; newrepo "$REPO"; LEDGER="$(ledger_of "$REPO")"

# misuse: no args -> exit 2
OUT="$( cd "$REPO" && bash "$GATE" 2>&1 )"; RC=$?
[ "$RC" -eq 2 ] && pass "no args -> misuse exit 2" || fail "no-args exit $RC ($OUT)"
# misuse: name but neither --step nor --record -> exit 2
OUT="$( cd "$REPO" && bash "$GATE" g 2>&1 )"; RC=$?
[ "$RC" -eq 2 ] && pass "name without --step/--record -> exit 2" || fail "bare name exit $RC ($OUT)"

# always-run pass: step exits 0 -> exit 0 + a pass row
OUT="$( cd "$REPO" && bash "$GATE" mygate --step 'true' 2>&1 )"; RC=$?
[ "$RC" -eq 0 ] && pass "passing step -> exit 0" || fail "passing step exit $RC ($OUT)"
{ [ -f "$LEDGER" ] && has_row "$LEDGER" mygate pass; } && pass "passing step records a pass row" || fail "no pass row recorded"

# always-run block: step exits 7 -> exit 7 (code passed through unchanged) + a fail row
OUT="$( cd "$REPO" && bash "$GATE" failgate --step 'exit 7' 2>&1 )"; RC=$?
[ "$RC" -eq 7 ] && pass "failing step passes its exit code through (7)" || fail "exit code not preserved ($RC)"
has_row "$LEDGER" failgate fail && pass "failing step records a fail row" || fail "no fail row recorded"

# row schema: exactly 4 columns; the 4th is the commit sha
line="$(awk -F'\t' '$2=="mygate"{print; exit}' "$LEDGER")"
nf=$(printf '%s' "$line" | awk -F'\t' '{print NF}')
[ "$nf" -eq 4 ] && pass "ledger row has 4 fields" || fail "row has $nf fields, want 4"
sha="$(printf '%s' "$line" | awk -F'\t' '{print $4}')"
head="$(cd "$REPO" && git rev-parse HEAD)"
[ "$sha" = "$head" ] && pass "4th field is the commit sha" || fail "sha mismatch ($sha vs $head)"

# audited skip var: OMAKASE_SKIP_<NAME>=1 skips even a blocking step
OUT="$( cd "$REPO" && OMAKASE_SKIP_FAILGATE=1 bash "$GATE" failgate --step 'exit 1' 2>&1 )"; RC=$?
{ [ "$RC" -eq 0 ] && echo "$OUT" | grep -q 'OMAKASE_SKIP_FAILGATE'; } && pass "skip var bypasses a blocking gate" || fail "skip var did not bypass ($RC: $OUT)"

# hardening: resolve common-dir BEFORE the step (a step that cd's still records its row)
OUT="$( cd "$REPO" && bash "$GATE" cdgate --step 'cd /tmp' 2>&1 )"; RC=$?
has_row "$LEDGER" cdgate pass && pass "records even when the step changes directory" || fail "cd-in-step dropped the row"
# hardening: outside any git repo -> pass the step's code through, write no stray omakase/
OUTSIDE="$TMP/notarepo"; rm -rf "$OUTSIDE"; mkdir -p "$OUTSIDE"
OUT="$( cd "$OUTSIDE" && bash "$GATE" g --step 'true' 2>&1 )"; RC=$?
[ "$RC" -eq 0 ] && pass "outside a repo: passes the step exit through" || fail "outside-repo exit $RC"
[ ! -e "$OUTSIDE/omakase" ] && pass "outside a repo: writes no stray omakase/ dir" || fail "littered outside a repo"
# hardening: a tab in the name must not shift columns
( cd "$REPO" && bash "$GATE" "$(printf 'tab\tname')" --step 'true' ) >/dev/null 2>&1
nf=$(tail -1 "$LEDGER" | awk -F'\t' '{print NF}')
[ "$nf" -eq 4 ] && pass "tab in name sanitized (row stays 4 fields)" || fail "tab in name shifted columns ($nf)"
```

- [ ] **Step 2: Run the test, verify Cycle A cases fail.** Run: `bash tests/omakase-gate.test.sh`. Expected: FAIL (script `omakase-gate.sh` does not exist yet).

- [ ] **Step 3: Create `payload/.omakase/bin/omakase-gate.sh`** with the complete primitive (all flags; later cycles only add tests, not code):

```bash
#!/usr/bin/env bash
# omakase-gate - ONE gate primitive. Run a check at a git hook, record the result in the
# shared run ledger (the scorecard), and pass the check's exit code through UNCHANGED so a
# non-zero result blocks the commit/push. Flags turn one primitive into every gate shape:
#
#   omakase-gate.sh <name> --step '<cmd>' [--cacheable] [--glob '<pats>']
#   omakase-gate.sh <name> --record        # out-of-band: write a PASS for HEAD, no step
#
#   <name>        the scorecard name; with the HEAD sha it is the cache key.
#   --step CMD    the check, run via the shell (a child). exit 0 = pass, non-zero = block.
#   --cacheable   a fresh PASS for the exact HEAD short-circuits and skips the step.
#   --glob PATS   space-separated case-globs (a single * spans directories). If set and no
#                 changed file in the range matches, skip. ABSENT = always in scope.
#   --record      append a PASS row for HEAD and exit 0; no step runs. Fails LOUD.
#
# A "deferred gate" is just --cacheable + a step that blocks: the step refuses, an
# out-of-band `--record` writes the PASS, the re-push at the same commit is allowed.
#
# Store: one append-only TSV in the SHARED git dir (.git/omakase/ledger.tsv), so every
# worktree shares one run history and one cache (the cache key is the commit sha):
#   epoch <tab> name <tab> verdict <tab> sha
# Run-recording is best-effort (a dropped row just re-runs next time); --record is the only
# signal an out-of-band check passed, so it fails LOUD.
#
# Env: OMAKASE_SKIP_<NAME>=1 (audited bypass; name upper-cased, '-'->'_'),
#      OMAKASE_NOW (test hook: pins the epoch).
set -uo pipefail   # NOT -e: we must capture the step's exit code, not die on it.

die_misuse() { echo "omakase-gate: $1" >&2; exit 2; }

[ $# -gt 0 ] || die_misuse "usage: omakase-gate.sh <name> --step '<cmd>' [--cacheable] [--glob '<pats>'] | <name> --record"
NAME="$1"; shift
case "$NAME" in --*) die_misuse "first argument must be the gate name, got '$NAME'";; esac

STEP="" CACHEABLE=0 GLOB="" RECORD=0 HAVE_STEP=0
while [ $# -gt 0 ]; do
  case "$1" in
    --step)      shift; [ $# -gt 0 ] || die_misuse "--step needs a command"; STEP="$1"; HAVE_STEP=1;;
    --cacheable) CACHEABLE=1;;
    --glob)      shift; [ $# -gt 0 ] || die_misuse "--glob needs a pattern"; GLOB="$1";;
    --record)    RECORD=1;;
    *) die_misuse "unknown argument '$1'";;
  esac
  shift
done
[ "$RECORD" -eq 1 ] && [ "$HAVE_STEP" -eq 1 ] && die_misuse "--record takes no --step (it writes a pass without running anything)"
[ "$RECORD" -eq 0 ] && [ "$HAVE_STEP" -eq 0 ] && die_misuse "need --step '<cmd>' (or --record)"

# Resolve the SHARED git dir BEFORE running the step: a step that cd's must not be able to
# misdirect (or drop) its own row, and an empty rev-parse must never become `cd ""`.
gitdir="$(git rev-parse --git-common-dir 2>/dev/null)" || gitdir=""
common=""; [ -n "$gitdir" ] && common="$(cd "$gitdir" 2>/dev/null && pwd)"
LEDGER=""; [ -n "$common" ] && LEDGER="$common/omakase/ledger.tsv"

# Tag every row with the commit it ran on (HEAD = the commit being committed/pushed).
sha="$(git rev-parse HEAD 2>/dev/null)" || sha=""
# Keep TSV columns intact even if a hostile name or sha carries a tab/newline.
NAME="${NAME//$'\t'/ }"; NAME="${NAME//$'\n'/ }"
sha="${sha//$'\t'/ }";   sha="${sha//$'\n'/ }"

now() { echo "${OMAKASE_NOW:-$(date +%s)}"; }

# append_row <verdict> - build the whole row in one variable and append it with a single
# printf (one write, O_APPEND) so concurrent appends under `parallel: true` do not tear.
append_row() {
  [ -n "$LEDGER" ] || return 1
  mkdir -p "$common/omakase" 2>/dev/null || return 1
  printf '%s\t%s\t%s\t%s\n' "$(now)" "$NAME" "$1" "$sha" >> "$LEDGER"
}

# (1) --record: the ONLY signal an out-of-band check passed -> fail LOUD on a write error.
if [ "$RECORD" -eq 1 ]; then
  if append_row pass; then
    echo "omakase-gate: recorded PASS for '$NAME' at ${sha:0:8}"
    exit 0
  fi
  echo "omakase-gate: FAILED to record a PASS for '$NAME' (could not write ${LEDGER:-<no git dir>})" >&2
  exit 1
fi

# (2) audited bypass, uniform for every gate.
skipvar="OMAKASE_SKIP_$(printf '%s' "$NAME" | tr '[:lower:]-' '[:upper:]_')"
if [ "${!skipvar:-0}" = "1" ]; then
  echo "omakase-gate[$NAME]: skipped via $skipvar (audited)"
  exit 0
fi

# (3) --glob scope: run only when a changed file in the range matches. Base resolves
# fail-OPEN (unresolvable -> skip, never a raw git error); the threat model is omission.
if [ -n "$GLOB" ]; then
  resolve_base() {
    local c
    for c in "$(git rev-parse --abbrev-ref --symbolic-full-name origin/HEAD 2>/dev/null)" origin/master origin/main; do
      [ -n "$c" ] || continue
      git rev-parse --verify --quiet "${c}^{commit}" >/dev/null 2>&1 && { printf '%s\n' "$c"; return 0; }
    done
    return 1
  }
  if ! BASE="$(resolve_base)"; then
    echo "omakase-gate[$NAME]: no resolvable base ref - skipping scope check (fail-open)"
    exit 0
  fi
  # merge-base bounded (three-dot); two-dot fallback if the range is unresolvable
  # (unrelated histories) so a range error cannot masquerade as "no changes".
  if ! CHANGED="$(git diff --name-only "${BASE}...HEAD" 2>/dev/null)"; then
    CHANGED="$(git diff --name-only "${BASE}..HEAD" 2>/dev/null || true)"
  fi
  matched=0
  if [ -n "$CHANGED" ]; then
    set -f   # noglob: $GLOB must word-split into literal case patterns, not expand here
    while IFS= read -r file; do
      [ -n "$file" ] || continue
      for g in $GLOB; do
        # shellcheck disable=SC2254
        case "$file" in $g) matched=1; break;; esac
      done
      [ "$matched" -eq 1 ] && break
    done <<< "$CHANGED"
    set +f
  fi
  if [ "$matched" -eq 0 ]; then
    echo "omakase-gate[$NAME]: no changed file matches the glob - skipping"
    exit 0
  fi
fi

# (4) --cacheable: a fresh PASS for this exact commit short-circuits the step.
if [ "$CACHEABLE" -eq 1 ] && [ -n "$LEDGER" ] && [ -f "$LEDGER" ] && [ -n "$sha" ]; then
  if awk -F'\t' -v n="$NAME" -v s="$sha" '$2==n && $4==s && $3=="pass"{f=1} END{exit f?0:1}' "$LEDGER"; then
    echo "omakase-gate[$NAME]: fresh PASS for ${sha:0:8} - skipping (cached)"
    exit 0
  fi
fi

# (5) run the step in a CHILD shell (so a step that calls `exit` cannot kill the gate
# before its row is recorded); record the run best-effort; pass the exit code through.
sh -c "$STEP"
rc=$?
verdict=pass; [ "$rc" -ne 0 ] && verdict=fail
append_row "$verdict" 2>/dev/null || true
exit "$rc"
```

- [ ] **Step 4: Make it executable and run the test.** Run: `chmod +x payload/.omakase/bin/omakase-gate.sh && bash tests/omakase-gate.test.sh`. Expected: all Cycle A cases PASS.

- [ ] **Step 5: Commit.**

```bash
git add payload/.omakase/bin/omakase-gate.sh tests/omakase-gate.test.sh
git commit -m "feat(gate): add the omakase-gate primitive (always-run core + scorecard)"
```

### Cycle B: `--record` + `--cacheable` + deferment

- [ ] **Step 6: Append the Cycle B cases to `tests/omakase-gate.test.sh`** (before the final `rm -rf "$TMP"` / summary block):

```bash
echo "== Cycle B: --cacheable, --record, deferment =="
REPO="$TMP/repoB"; newrepo "$REPO"; LEDGER="$(ledger_of "$REPO")"
( cd "$REPO" && mkdir -p src && printf 'a\n' > src/app.txt && git add src/app.txt && git commit -q -m c1 )

# --cacheable freshness: no row -> runs; after a pass -> next run skips (cached)
runs="$TMP/ran.B"; : > "$runs"
step="printf x >> $runs"
OUT="$( cd "$REPO" && bash "$GATE" cached --cacheable --step "$step" 2>&1 )"
[ "$(wc -c < "$runs" | tr -d ' ')" = "1" ] && pass "cacheable: first run executes the step" || fail "cacheable first run did not execute ($OUT)"
OUT="$( cd "$REPO" && bash "$GATE" cached --cacheable --step "$step" 2>&1 )"
{ [ "$(wc -c < "$runs" | tr -d ' ')" = "1" ] && echo "$OUT" | grep -q 'cached'; } && pass "cacheable: a fresh pass skips the step" || fail "cacheable did not skip on a fresh pass ($OUT)"
# HEAD moves -> the pass is stale -> the step runs again
( cd "$REPO" && printf 'b\n' > src/more.txt && git add src/more.txt && git commit -q -m c2 )
OUT="$( cd "$REPO" && bash "$GATE" cached --cacheable --step "$step" 2>&1 )"
[ "$(wc -c < "$runs" | tr -d ' ')" = "2" ] && pass "cacheable: a new commit busts the cache (re-runs)" || fail "cacheable did not re-run after HEAD moved ($OUT)"

# --record: writes a PASS for HEAD with no step; a subsequent --cacheable run skips
REPO="$TMP/repoR"; newrepo "$REPO"; LEDGER="$(ledger_of "$REPO")"
OUT="$( cd "$REPO" && bash "$GATE" review --record 2>&1 )"; RC=$?
{ [ "$RC" -eq 0 ] && has_row "$LEDGER" review pass; } && pass "--record writes a pass row, exit 0" || fail "--record did not write a pass ($RC: $OUT)"
ran="$TMP/ran.R"; : > "$ran"
OUT="$( cd "$REPO" && bash "$GATE" review --cacheable --step "printf x >> $ran" 2>&1 )"
[ ! -s "$ran" ] && pass "--record then --cacheable run skips the step" || fail "cacheable ran despite a recorded pass ($OUT)"

# --record fail-loud: an unwritable ledger dir -> exit non-zero and say so
REPO="$TMP/repoRL"; newrepo "$REPO"
COMMON="$(cd "$REPO" && cd "$(git rev-parse --git-common-dir)" && pwd)"
# make the omakase dir un-creatable by planting a FILE where the dir must go
rm -rf "$COMMON/omakase"; : > "$COMMON/omakase"
OUT="$( cd "$REPO" && bash "$GATE" review --record 2>&1 )"; RC=$?
{ [ "$RC" -ne 0 ] && echo "$OUT" | grep -qi 'FAILED to record'; } && pass "--record fails loud on a write error" || fail "--record did not fail loud ($RC: $OUT)"
rm -f "$COMMON/omakase"

# deferment (case 3): a blocking step blocks; after --record the same HEAD is allowed
REPO="$TMP/repoD"; newrepo "$REPO"; LEDGER="$(ledger_of "$REPO")"
blocker='echo "BLOCKED: run review then: omakase-gate.sh review --record" >&2; exit 1'
OUT="$( cd "$REPO" && bash "$GATE" review --cacheable --step "$blocker" 2>&1 )"; RC=$?
{ [ "$RC" -ne 0 ] && echo "$OUT" | grep -q 'BLOCKED'; } && pass "deferment: the blocking step blocks first" || fail "deferment did not block ($RC: $OUT)"
( cd "$REPO" && bash "$GATE" review --record ) >/dev/null
OUT="$( cd "$REPO" && bash "$GATE" review --cacheable --step "$blocker" 2>&1 )"; RC=$?
[ "$RC" -eq 0 ] && pass "deferment: after --record the same HEAD is allowed" || fail "deferment still blocked after --record ($RC: $OUT)"
```

- [ ] **Step 7: Run the test.** Run: `bash tests/omakase-gate.test.sh`. Expected: Cycle B cases PASS (the primitive already implements `--record`/`--cacheable`). If any fail, fix `omakase-gate.sh`, not the test.

- [ ] **Step 8: Commit.**

```bash
git add tests/omakase-gate.test.sh
git commit -m "test(gate): cover --cacheable, --record (fail-loud), and deferment"
```

### Cycle C: `--glob` scope + concurrency + end-to-end push

- [ ] **Step 9: Append the Cycle C cases to `tests/omakase-gate.test.sh`:**

```bash
echo "== Cycle C: --glob scope, concurrency, end-to-end =="
# A bare repo as origin so origin/HEAD resolves a base for the --glob range.
REMOTE="$TMP/remoteC.git"; git init -q --bare "$REMOTE"
REPO="$TMP/repoC"; newrepo "$REPO"
( cd "$REPO" && git branch -M main && git remote add origin "$REMOTE" && git push -q -u origin main )
( cd "$REPO" && mkdir -p src docs && printf 'a\n' > src/app.txt && git add src/app.txt && git commit -q -m feat )
LEDGER="$(ledger_of "$REPO")"

# glob match -> the step runs (records a row)
OUT="$( cd "$REPO" && bash "$GATE" g1 --glob 'src/*' --step 'true' 2>&1 )"
has_row "$LEDGER" g1 pass && pass "glob match: the step runs" || fail "glob match did not run ($OUT)"
# glob miss -> skip (no row, exit 0)
OUT="$( cd "$REPO" && bash "$GATE" g2 --glob 'docs/*' --step 'false' 2>&1 )"; RC=$?
{ [ "$RC" -eq 0 ] && ! has_row "$LEDGER" g2 fail; } && pass "glob miss: skips (no run)" || fail "glob miss did not skip ($RC: $OUT)"
# no --glob -> always runs even when nothing in range would match
OUT="$( cd "$REPO" && bash "$GATE" g3 --step 'false' 2>&1 )"; RC=$?
{ [ "$RC" -ne 0 ] && has_row "$LEDGER" g3 fail; } && pass "no --glob: always in scope (runs every time)" || fail "no-glob gate did not run ($RC: $OUT)"

# base fail-open: a repo with no remote and no resolvable base -> skip, never a git error
REPONB="$TMP/repoNB"; newrepo "$REPONB"
( cd "$REPONB" && mkdir -p src && printf 'a\n' > src/app.txt && git add src/app.txt && git commit -q -m c1 )
OUT="$( cd "$REPONB" && bash "$GATE" fo --glob 'src/*' --step 'false' 2>&1 )"; RC=$?
{ [ "$RC" -eq 0 ] && echo "$OUT" | grep -q 'no resolvable base'; } && pass "glob: fails open when no base resolves" || fail "did not fail open without a base ($RC: $OUT)"

# concurrency: N parallel appends yield N complete (untorn) 4-field rows
REPOC="$TMP/repoCC"; newrepo "$REPOC"; LEDGERC="$(ledger_of "$REPOC")"
( cd "$REPOC" && for i in 1 2 3 4 5 6 7 8; do bash "$GATE" "cc$i" --step 'true' & done; wait ) >/dev/null 2>&1
rows=$(grep -c . "$LEDGERC"); torn=$(awk -F'\t' 'NF!=4{n++} END{print n+0}' "$LEDGERC")
{ [ "$rows" -eq 8 ] && [ "$torn" -eq 0 ]; } && pass "concurrency: 8 parallel appends -> 8 untorn rows" || fail "concurrency: $rows rows, $torn torn"

# end-to-end: a real git push through an installed pre-push hook wired to the primitive.
echo "== Cycle C: end-to-end git push =="
PAYE="$TMP/payE"; REPOE="$TMP/repoE"; REMOTEE="$TMP/remoteE.git"
mkdir -p "$PAYE"; cp -R "$PAY/." "$PAYE/"
cat > "$PAYE/lefthook-local.yml" <<'YML'
pre-push:
  jobs:
    - name: review
      run: bash .omakase/bin/omakase-gate.sh review --cacheable --glob 'src/*' --step 'echo "BLOCKED: record review then push" >&2; exit 1'
post-checkout:
  jobs:
    - name: omakase-ensure-present
      run: bash "$(git rev-parse --git-common-dir)/omakase/ensure-present.sh"
YML
newrepo "$REPOE"; git init -q --bare "$REMOTEE"
( cd "$REPOE" && git branch -M main && git remote add origin "$REMOTEE" && git push -q -u origin main )
( cd "$REPOE" && OMAKASE_PAYLOAD="$PAYE" bash "$INIT" ) >/dev/null 2>&1
LEDGERE="$(ledger_of "$REPOE")"
( cd "$REPOE" && mkdir -p src && printf 'x\n' > src/app.txt && git add src/app.txt && git commit -q -m feat )
OUT="$( cd "$REPOE" && git push origin main 2>&1 )"; RC=$?
{ [ "$RC" -ne 0 ] && echo "$OUT" | grep -q 'BLOCKED'; } && pass "e2e: push BLOCKED when review never recorded for the commit" || fail "e2e push not blocked ($RC: $OUT)"
has_row "$LEDGERE" review fail && pass "e2e: the blocked run recorded a fail row" || fail "e2e no fail row in the ledger"
( cd "$REPOE" && bash "$GATE" review --record ) >/dev/null
OUT="$( cd "$REPOE" && git push origin main 2>&1 )"; RC=$?
[ "$RC" -eq 0 ] && pass "e2e: push ALLOWED after --record for the same commit" || fail "e2e push still blocked after --record ($RC: $OUT)"
OUT="$( cd "$REPOE" && bash "$SHOW" --markdown 2>&1 )"
echo "$OUT" | grep -q 'review' && pass "e2e: omakase status renders the review gate" || fail "show did not render the gate"

rm -rf "$TMP"
echo ""
[ "$FAILED" -eq 0 ] && echo "ALL PASS" || { echo "FAILURES PRESENT"; exit 1; }
```

- [ ] **Step 10: Run the test.** Run: `bash tests/omakase-gate.test.sh`. Expected: `ALL PASS`. Note: the end-to-end `show` case depends on `show.sh` reading the 4-column ledger and matching the `omakase-gate.sh` name regex. `show.sh` is not rewritten until Task 4, so until then the final two e2e assertions may fail on the `show` line. If so, comment the two `$SHOW` lines with a `# TODO(Task 4): show.sh reads 4-col` note and uncomment them in Task 4. Every non-`show` case must pass now.

- [ ] **Step 11: Commit.**

```bash
git add tests/omakase-gate.test.sh
git commit -m "test(gate): cover --glob scope, concurrency, and an end-to-end push"
```

---

## Task 2: (reserved — folded into Task 1)

The primitive is one cohesive file; its three TDD cycles are Task 1. No separate Task 2. Numbering continues at Task 3 to keep later cross-references stable in review.

---

## Task 3: (reserved — folded into Task 1)

See Task 1. Proceed to Task 4.

---

## Task 4: Flip the ledger schema and its readers (atomic)

The ledger schema changes from 6 columns to 4. The producer (now `omakase-gate.sh`, wired into the base payload) and both readers (`show.sh`, `omakase-stop-notice.sh`) must flip together, with their tests. This is one task because the schema change is atomic: a half-flip leaves a reader misparsing the other half's rows.

**Files:**
- Modify: `payload/lefthook-local.yml`
- Modify: `bin/show.sh`
- Modify: `payload/.omakase/bin/omakase-stop-notice.sh`
- Modify: `tests/scorecard.test.sh`

**Interfaces:**
- Consumes: `omakase-gate.sh` (Task 1) and its 4-column ledger schema.
- Produces: a base payload wired to the primitive; `show.sh` and `omakase-stop-notice.sh` that read 4 columns; `scorecard.test.sh` covering the status surfaces at 4 columns.

### 4a: base payload wiring

- [ ] **Step 1: Rewrite `payload/lefthook-local.yml`.** Replace the whole file with the primitive form. The always-run example gate, a commented cacheable/deferred pre-push template that references ONLY shipping scripts, and the unchanged post-checkout job:

```yaml
# Personal hook wiring (gitignored via .git/info/exclude - never committed).
# This one file IS your harness - read it top to bottom to see everything that runs.
# Every gate is one omakase-gate.sh call. Its flags pick the shape:
#   (default)     run the --step every time; non-zero blocks. Good for fast checks.
#   --cacheable   run once per commit, then reuse the pass (skip) until HEAD moves.
#   --glob 'PATS' only run when a changed file in the range matches a pattern.
#   --record      (out of band) write a pass for the current commit without running a step.
# A "deferred" check is just --cacheable + a step that blocks: the step refuses, an agent or
# human runs the real check and then `omakase-gate.sh <name> --record`, and the re-push at the
# same commit is allowed. Every run lands in the scorecard (omakase status, the takeout
# status-line). The per-gate audited bypass is OMAKASE_SKIP_<NAME>=1.
#
# By default lefthook's own run header shows. The branded omakase box is opt-in (it enforces
# nothing): to use it, add an omakase-banner job as the first pre-commit job -
# `run: bash .omakase/bin/omakase-banner.sh pre-commit` - and add a top-level
# `output: [summary, success, failure, execution_out]` to drop lefthook's own header.

pre-commit:
  jobs:
    # The shipped example: block a staged merge-conflict marker. Runs every commit.
    - name: markers
      run: bash .omakase/bin/omakase-gate.sh markers --step 'bash .omakase/gates/example.sh'

# --- pre-push: complete checkers + deferred gates. Left commented so this example harness
# --- installs cleanly on any repo. Uncomment and adapt to your stack.
# pre-push:
#   parallel: true
#   jobs:
#     # complete checker, cached per commit: your whole-project check runs once per HEAD.
#     - name: tests
#       run: bash .omakase/bin/omakase-gate.sh tests --cacheable --glob 'src/* lib/*' --step '<your check, e.g. make check | npm test | pytest>'
#     # deferred gate: a slow / non-deterministic check (a render, an LLM review) that can't
#     # run in a hook. The step blocks; an agent or human runs the real check and records:
#     #   bash .omakase/bin/omakase-gate.sh review --record
#     - name: review
#       run: bash .omakase/bin/omakase-gate.sh review --cacheable --glob 'src/*' --step 'echo "BLOCKED: run review, then: omakase-gate.sh review --record" >&2; exit 1'

# Worktree auto-install: on every checkout (including `git worktree add`), copy any
# MISSING injected file into this worktree from the shared snapshot. Never overwrites a
# local edit, never touches a tracked path. The script lives in the shared git dir, so it
# is reachable from any worktree. Written by init.sh.
post-checkout:
  jobs:
    - name: omakase-ensure-present
      run: bash "$(git rev-parse --git-common-dir)/omakase/ensure-present.sh"
```

### 4b: `bin/show.sh`

- [ ] **Step 2: Update the RUNS comment.** In `bin/show.sh`, change line ~24 from:

```bash
RUNS="$OMK/ledger.tsv"      # gate-RUN ledger (omakase-ledger.sh): epoch,hook,gate,verdict,ms,sha
```
to:
```bash
RUNS="$OMK/ledger.tsv"      # gate run + cache store (omakase-gate.sh): epoch,name,verdict,sha
```

- [ ] **Step 3: Rewrite `render_guards` pass-1 + the gate-name match + the ENFORCES cell.** In the `awk` program inside `render_guards` (currently lines ~208-271), make these exact changes:

  Pass-1 verdict scan, change:
```awk
    FILENAME==runsfile {
      if (NF>=5 && $1 ~ /^[0-9]+$/) { ts=$1+0; if (ts>=seen[$3]) { seen[$3]=ts; verd[$3]=$4 } }
      next
    }
```
  to (4-column: name=$2, verdict=$3):
```awk
    FILENAME==runsfile {
      if (NF>=4 && $1 ~ /^[0-9]+$/) { ts=$1+0; if (ts>=seen[$2]) { seen[$2]=ts; verd[$2]=$3 } }
      next
    }
```

  Gate-name extraction, change:
```awk
      ledgered=0; gate=""
      if (match(runcmd, /omakase-ledger\.sh [A-Za-z0-9._-]+/)) {   # ledgered gate -> canonical name
        s=substr(runcmd,RSTART,RLENGTH); sub(/^omakase-ledger\.sh /,"",s); gate=s; ledgered=1
      }
      act=runcmd                                            # the action: strip the ledger wrapper
      p=index(act," -- "); if (p>0) act=substr(act,p+4)
      sub(/^bash[ \t]+/,"",act); gsub(/"/,"",act)
      base=act; sub(/[ \t].*/,"",base); sub(/.*\//,"",base) # gate script basename for the ENFORCES lookup
      # ensure-present matches on the full action (its run cmd has spaces inside $(...), which
      # would truncate `base`); the clean gate paths below match on the extracted basename.
      if      (act ~ /ensure-present\.sh/)    enf="self-heal: restore any missing injected files"
      else if (base=="worktree-discipline.sh") enf="no main-checkout commit carrying WIP from another worktree"
      else if (base=="deferred-check.sh")      enf="deferred gate - needs a fresh recorded PASS to push"
      else enf=act
      gname=(ledgered ? gate : jobname)
```
  to (match `omakase-gate.sh <name>`; ENFORCES reads only the SAFE fixed-token flags, never the quoted `--step` body):
```awk
      ledgered=0; gate=""
      if (match(runcmd, /omakase-gate\.sh [A-Za-z0-9._-]+/)) {     # a gate -> its canonical name
        s=substr(runcmd,RSTART,RLENGTH); sub(/^omakase-gate\.sh /,"",s); gate=s; ledgered=1
      }
      if (runcmd ~ /ensure-present\.sh/) {
        enf="self-heal: restore any missing injected files"
      } else if (ledgered) {
        # Describe the gate by its SAFE flags only - never the quoted --step body (it can
        # carry spaces, quotes, ; and even a literal --record).
        cached=(runcmd ~ /--cacheable/)
        scope="runs every commit"
        if (match(runcmd, /--glob '"'"'[^'"'"']*'"'"'/)) {         # --glob 'PATS' (single-quoted)
          g=substr(runcmd,RSTART,RLENGTH); sub(/^--glob '"'"'/,"",g); sub(/'"'"'$/,"",g); scope="scope: "g
        }
        enf=(cached ? "cached; " : "") scope
      } else enf=runcmd
      gname=(ledgered ? gate : jobname)
```

  Note: the awk source is inside a single-quoted shell heredoc-less string. To put a literal single quote in the awk regex, use the `'"'"'` shell escape exactly as shown.

- [ ] **Step 4: Cut `render_guards_fallback`.** Delete the entire `render_guards_fallback()` function (currently lines ~274-338). Replace its single call site inside `render_guards` (currently line ~199):

```bash
  if [ -z "$DUMP" ]; then render_guards_fallback; return; fi
```
with a one-line note in both modes:
```bash
  if [ -z "$DUMP" ]; then
    if [ "$FORMAT" = md ]; then echo "_lefthook not resolved - gates are not running._"
    else echo "  (lefthook not resolved - gates are not running)"; fi
    return
  fi
```

### 4c: `payload/.omakase/bin/omakase-stop-notice.sh`

- [ ] **Step 5: Rewrite the header state list and the "last run" parsing for 4 columns, grouped by sha.** Change the header comment block's "Last run" lines to drop the `<Hook>` label (lefthook exposes no hook name to a job), then replace the "last run" parse section (currently lines ~69-92):

```bash
# last run - summarise the most recent 6-col run (epoch hook gate verdict ms sha). Pass 1
# finds the (hook, sha) of the newest run row; pass 2 takes the latest verdict per gate for
# that run and counts passed/failed plus the run's clock epoch. Legacy 5-col rows (no sha)
# are ignored.
ledger="$common/omakase/ledger.tsv"
maxepoch=0; ran_hook=""; ran_sha=""; ran=0; passed=0; failed=0; runepoch=0
if [ -s "$ledger" ]; then
  read -r maxepoch ran_hook ran_sha <<EOF
$(awk -F'\t' 'NF>=6 && $6!="" && $1 ~ /^[0-9]+$/ && ($1+0)>m{m=$1+0; h=$2; s=$6} END{printf "%d %s %s\n", m+0, h, s}' "$ledger")
EOF
  case "${maxepoch:-}" in ''|*[!0-9]*) maxepoch=0;; esac
  if [ "$maxepoch" -gt 0 ] && [ -n "$ran_sha" ]; then
    read -r ran passed failed runepoch <<EOF
$(awk -F'\t' -v H="$ran_hook" -v S="$ran_sha" '
        NF>=6 && $1 ~ /^[0-9]+$/ && $2==H && $6==S {
          e=$1+0; g=$3
          if (!(g in te) || e>=te[g]) { te[g]=e; tv[g]=$4 }
          if (e>re) re=e
        }
        END { for (g in tv){ n++; if (tv[g]=="pass") p++ }
              printf "%d %d %d %d\n", n+0, p+0, (n-p)+0, re+0 }' "$ledger")
EOF
  fi
fi
```
to (4-col `epoch name verdict sha`; group the latest run by sha alone):
```bash
# last run - summarise the most recent run from the 4-col ledger (epoch name verdict sha).
# Pass 1 finds the sha of the newest run row. Pass 2 takes the latest verdict per gate name
# for that sha and counts passed/failed plus the run's clock epoch. Rows with an empty sha
# (a pre-commit on an unborn HEAD) are ignored so they cannot mask a later real run.
ledger="$common/omakase/ledger.tsv"
maxepoch=0; ran_sha=""; ran=0; passed=0; failed=0; runepoch=0
if [ -s "$ledger" ]; then
  read -r maxepoch ran_sha <<EOF
$(awk -F'\t' 'NF>=4 && $4!="" && $1 ~ /^[0-9]+$/ && ($1+0)>m{m=$1+0; s=$4} END{printf "%d %s\n", m+0, s}' "$ledger")
EOF
  case "${maxepoch:-}" in ''|*[!0-9]*) maxepoch=0;; esac
  if [ "$maxepoch" -gt 0 ] && [ -n "$ran_sha" ]; then
    read -r ran passed failed runepoch <<EOF
$(awk -F'\t' -v S="$ran_sha" '
        NF>=4 && $1 ~ /^[0-9]+$/ && $4==S {
          e=$1+0; g=$2
          if (!(g in te) || e>=te[g]) { te[g]=e; tv[g]=$3 }
          if (e>re) re=e
        }
        END { for (g in tv){ n++; if (tv[g]=="pass") p++ }
              printf "%d %d %d %d\n", n+0, p+0, (n-p)+0, re+0 }' "$ledger")
EOF
  fi
fi
```

- [ ] **Step 6: Drop the hook label from the rendered "Last run" line.** Delete the `hookname()` helper (currently line ~94) and its uses. Change the render block (currently lines ~130-139) from:

```bash
elif [ "$ran_this_turn" -eq 1 ] && [ "$ran" -gt 0 ]; then
  hk="$(hookname "$ran_hook")"; tm="$(clock "$runepoch")"
  if [ "$failed" -gt 0 ]; then
    u=checks; [ "$failed" -eq 1 ] && u=check
    msg="$name is active ✓
Last run: $hk $failed $u failed at $tm"
  else
    msg="$name is active ✓
Last run: $hk $ran/$ran checks at $tm"
  fi
```
to (no hook label):
```bash
elif [ "$ran_this_turn" -eq 1 ] && [ "$ran" -gt 0 ]; then
  tm="$(clock "$runepoch")"
  if [ "$failed" -gt 0 ]; then
    u=checks; [ "$failed" -eq 1 ] && u=check
    msg="$name is active ✓
Last run: $failed $u failed at $tm"
  else
    msg="$name is active ✓
Last run: $ran/$ran checks at $tm"
  fi
```

### 4d: update `tests/scorecard.test.sh`

- [ ] **Step 7: Trim the recorder scenarios and reframe for 4 columns.** Make these edits to `tests/scorecard.test.sh`:
  - **Header comment (lines 1-13):** drop the `omakase-ledger.sh` line and the "6-col" wording; describe the 4-col store `epoch name verdict sha`.
  - **Delete Scenario R entirely** (lines ~35-51): it tested `omakase-ledger.sh`'s recording, now covered by `tests/omakase-gate.test.sh` Cycle A. Remove the `RECORD="$HERE/../payload/.omakase/bin/omakase-ledger.sh"` variable (line 16).
  - **Delete Scenario V entirely** (lines ~183-196): recorder hardening, now covered by `tests/omakase-gate.test.sh` Cycle A.
  - **Scenario K** (stop-notice): change every hand-built ledger row from 6 columns to 4. Each `printf '%s\tpre-push\t%s\tpass\t1000\t%s\n' "$T" "$g" "$HEAD"` becomes `printf '%s\t%s\tpass\t%s\n' "$T" "$g" "$HEAD"`; the fail row likewise. Change the empty-sha row `printf '%s\tpre-commit\tprecommit-gate\tpass\t1000\t\n' "$T4"` to `printf '%s\tprecommit-gate\tpass\t\n' "$T4"`. Change the assertion `grep -q 'Last run: Pre-push gate'` (line 93) to `grep -q 'Last run:'` (the hook label is gone). All other K assertions (`3/3 checks at`, `is active ✓`, `1 check failed`, `2 checks failed`, the empty-sha masking, the nudge) stay.
  - **Scenario S** (lines ~158-170): change the hand-built row `printf '%s\tpre-commit\tomakase-example\tfail\t40\t%s\n'` to `printf '%s\tomakase-example\tfail\t%s\n'`, and change the gate name asserted from `omakase-example` to `markers` (the base wiring's gate name is now `markers`). Keep the fail-verdict and markdown-table assertions.
  - **Scenario U** (lines ~172-181): the real-commit row is now written by `omakase-gate.sh` through the rewired base wiring. Change `has_run "$LEDGER" omakase-example pass` to `has_run "$LEDGER" markers pass`, and the field-count assertion from `-eq 6` to `-eq 4` with name `markers`:
```bash
nf=$(awk -F'\t' '$2=="markers"{print NF; exit}' "$LEDGER")
[ "$nf" -eq 4 ] && pass "real commit row has 4 fields" || fail "real commit row has $nf fields"
```
  Also update `has_run()` (line 32) to key on the new columns: `awk -F'\t' -v g="$2" -v v="$3" '$2==g && $3==v{f=1} END{exit f?0:1}' "$1"`.
  - **Scenarios C, I, W** (canary, inventory, branding): no schema dependency; leave them unchanged.

- [ ] **Step 8: Run the affected tests.** Run:
```bash
bash tests/omakase-gate.test.sh && bash tests/scorecard.test.sh
```
Expected: both `ALL PASS`. If the Task 1 Step 10 `show` lines were commented out, uncomment them now and re-run `tests/omakase-gate.test.sh`.

- [ ] **Step 9: Commit.**
```bash
git add payload/lefthook-local.yml bin/show.sh payload/.omakase/bin/omakase-stop-notice.sh tests/scorecard.test.sh
git commit -m "refactor(ledger): flip the run store to 4 columns; rewire base wiring + readers to omakase-gate"
```

---

## Task 5: `init.sh` — ledger rotation, wiring guard, stale comments

**Files:**
- Modify: `bin/init.sh`
- Test: `tests/omakase-gate.test.sh` (append an upgrade-rotation case), `tests/sources.test.sh` (append a wiring-guard case)

**Interfaces:**
- Consumes: the 4-column ledger schema; `omakase-gate.sh` wiring.
- Produces: `init.sh` that rotates a pre-v2 (6-column) ledger aside on init, runs the `--step`-aware wiring guard on BOTH install paths, and carries no stale machinery comments.

- [ ] **Step 1: Write the failing upgrade-rotation test** in `tests/omakase-gate.test.sh` (append before the final `rm -rf "$TMP"` block):

```bash
echo "== Upgrade: a pre-v2 (6-col) ledger is rotated aside on init =="
PAYU="$TMP/payU"; REPOU="$TMP/repoU"
mkdir -p "$PAYU"; cp -R "$PAY/." "$PAYU/"
newrepo "$REPOU"
COMMONU="$(cd "$REPOU" && cd "$(git rev-parse --git-common-dir)" && pwd)"
mkdir -p "$COMMONU/omakase"
# plant an old 6-column ledger
printf '%s\tpre-commit\told-gate\tpass\t40\t%s\n' 1700000000 "$(cd "$REPOU" && git rev-parse HEAD)" > "$COMMONU/omakase/ledger.tsv"
( cd "$REPOU" && OMAKASE_PAYLOAD="$PAYU" bash "$INIT" ) >/dev/null 2>&1
[ -f "$COMMONU/omakase/ledger.tsv.pre-v2.bak" ] && pass "upgrade: pre-v2 ledger rotated to .pre-v2.bak" || fail "pre-v2 ledger not rotated"
{ [ ! -f "$COMMONU/omakase/ledger.tsv" ] || ! awk -F'\t' 'NF>=6{f=1} END{exit f?0:1}' "$COMMONU/omakase/ledger.tsv"; } && pass "upgrade: the live ledger no longer holds 6-col rows" || fail "6-col rows survived in the live ledger"
```

- [ ] **Step 2: Run it, verify it fails.** Run: `bash tests/omakase-gate.test.sh`. Expected: the rotation case FAILS (init does not rotate yet).

- [ ] **Step 3: Add ledger rotation to `bin/init.sh`.** Insert this block after `OMK="$COMMON/omakase"` is defined (after line ~76), before the source mechanism:

```bash
# ---- one-time ledger schema upgrade ----
# The run + cache store (ledger.tsv) survives re-init, but a pre-v2 ledger holds 6-column
# rows (epoch hook gate verdict ms sha) that the 4-column reader (epoch name verdict sha)
# would misparse on the scorecard. Detect any 6+-column row and rotate the whole file aside
# once; it is disposable per-clone run history. The cache itself is already safe across the
# change (an old $4 verdict string is never a 40-hex sha, so a freshness query fails closed).
if [ -f "$OMK/ledger.tsv" ] && awk -F'\t' 'NF>=6{f=1; exit} END{exit f?0:1}' "$OMK/ledger.tsv"; then
  mv -f "$OMK/ledger.tsv" "$OMK/ledger.tsv.pre-v2.bak" \
    && echo "omakase: rotated a pre-v2 (6-column) run ledger aside to ledger.tsv.pre-v2.bak (the new store starts clean)."
fi
```

- [ ] **Step 4: Fix the stale comment at `init.sh:583-584`.** Change:
```bash
# enabled is written 1 here - nothing writes 0 yet, but every reader honors it
# (spec §2 + safety fix 5). NOT $OMK/ledger.tsv: that is the gate-RUN ledger
# (omakase-ledger.sh), which must survive re-init.
```
to:
```bash
# enabled is written 1 here - nothing writes 0 yet, but every reader honors it
# (spec §2 + safety fix 5). NOT $OMK/ledger.tsv: that is the gate run + cache store
# (omakase-gate.sh), which survives re-init (a pre-v2 6-col ledger is rotated aside above).
```

- [ ] **Step 5: Fix the stale machinery list at `init.sh:165`.** Change:
```bash
  # Layer the base harness's payload UNDER the source delta, so a source can RELY on base
  # machinery (banner / ledger / record / deferred-check / status-line / stop-notice)
```
to:
```bash
  # Layer the base harness's payload UNDER the source delta, so a source can RELY on base
  # machinery (banner / gate / status-line / stop-notice)
```

- [ ] **Step 6: Make the wiring guard `--step`-aware and run it on BOTH paths.** First, REMOVE the guard from inside the `--source` block (currently lines ~187-205: the `wiring="$MERGED/lefthook-local.yml"` ... `fi` block). Then add a single guard call after `PAYLOAD` is finalized and validated (after line ~216 `[ -d "$PAYLOAD" ] || { ...; }`):

```bash
# Fail-closed wiring guard (both install paths): every .omakase/*.sh the hook wiring invokes
# must exist in the payload about to be placed. A wired script that does not ship would die at
# commit time with a cryptic exit 127 - refuse here, before anything is placed. Skip full-line
# YAML comments (commented templates may reference scripts on purpose) but scan the REST of
# each line in full, so a '#' inside a quoted --step value cannot truncate the scan and hide a
# real missing reference.
wiring="$PAYLOAD/lefthook-local.yml"
if [ -f "$wiring" ]; then
  missing=""
  for ref in $(awk '!/^[[:space:]]*#/' "$wiring" | grep -oE '\.omakase/[A-Za-z0-9._/-]+\.sh' | sort -u); do
    [ -f "$PAYLOAD/$ref" ] || missing="$missing $ref"
  done
  if [ -n "$missing" ]; then
    echo "omakase: hook wiring references script(s) the payload does not ship:$missing" >&2
    echo "  These would fail at commit time (exit 127). Fix lefthook-local.yml or ship the script(s). Nothing was placed." >&2
    exit 1
  fi
fi
```

- [ ] **Step 7: Write the failing wiring-guard test** in `tests/sources.test.sh` (append a scenario near the other source scenarios; reuse the file's existing `newrepo`/`pass`/`fail` helpers and `INIT` variable):

```bash
echo "== Scenario: wiring guard refuses a --step that names a non-shipping script =="
REPOWG="$TMP/repoWG"; PAYWG="$TMP/payWG"
rm -rf "$PAYWG"; cp -R "$HERE/../payload/." "$PAYWG/"
cat > "$PAYWG/lefthook-local.yml" <<'YML'
pre-commit:
  jobs:
    - name: ghost
      run: bash .omakase/bin/omakase-gate.sh ghost --step 'bash .omakase/gates/does-not-ship.sh'  # note: a # in this step must not hide the ref
YML
newrepo "$REPOWG"
OUT="$( cd "$REPOWG" && OMAKASE_PAYLOAD="$PAYWG" bash "$INIT" 2>&1 )"; RC=$?
{ [ "$RC" -ne 0 ] && echo "$OUT" | grep -q 'does-not-ship.sh'; } && pass "guard refuses a wired script that does not ship (plain path, # in step)" || fail "guard missed the non-shipping script ($RC: $OUT)"
[ ! -d "$REPOWG/.omakase" ] && pass "guard refused before placing anything" || fail "guard placed files despite refusing"
```

- [ ] **Step 8: Run the affected tests.** Run:
```bash
bash tests/omakase-gate.test.sh && bash tests/sources.test.sh && bash tests/inject.test.sh
```
Expected: all `ALL PASS`. `inject.test.sh` exercises the plain install path; its `mkpayload` wires only `.omakase/gates/example.sh` (which ships), so the new plain-path guard passes.

- [ ] **Step 9: Commit.**
```bash
git add bin/init.sh tests/omakase-gate.test.sh tests/sources.test.sh
git commit -m "feat(init): rotate pre-v2 ledger; make the wiring guard --step-aware and run on both paths"
```

---

## Task 6: Rewire examples and embedded test fixtures to the primitive

**Files:**
- Modify: `examples/sample-harness/payload/lefthook-local.yml`
- Modify: `examples/sample-harness/README.md`
- Modify: `tests/sources.test.sh` (the embedded fixture at lines ~209-211 and the presence assertions ~228-229)
- Modify: `tests/build.test.sh` (presence assertions at lines 26, 47)
- Modify: `tests/sample-harness.test.sh` (header comment + assertion at lines 6, 51)
- Modify: `tests/superpowers-harness.test.sh` (header comment + assertion at lines 9, 61)
- Modify: `tests/inject.test.sh` (the `mkpayload` lefthook fixture, lines ~21-25, to the primitive form)

**Interfaces:**
- Consumes: the primitive and the 4-column store.
- Produces: examples and fixtures that wire `omakase-gate.sh`, with presence assertions that check `omakase-gate.sh` is layered in (the old-script "absent" assertions come in Task 7).

- [ ] **Step 1: Rewrite `examples/sample-harness/payload/lefthook-local.yml`.** Replace the `omakase-ledger.sh ... -- ... block-marker.sh` job with the primitive form and drop the `OMAKASE_HOOK` env:

```yaml
# Sample harness hook wiring (gitignored via .git/info/exclude - never committed).
# This one file IS the harness - read it top to bottom to see everything that runs.
# The .omakase/bin/omakase-gate.sh helper below is NOT shipped by this harness: it comes
# from the omakase base harness, layered in underneath this harness's own files at install
# time. So this harness carries only its delta - its rule, its one gate, and this wiring.

pre-commit:
  jobs:
    # The sample gate: block a staged DO-NOT-COMMIT marker. One omakase-gate.sh call: the
    # run lands in `omakase status` and the takeout status-line, and the step's exit code
    # passes straight through (non-zero blocks the commit).
    - name: block-marker
      run: bash .omakase/bin/omakase-gate.sh block-marker --step 'bash .omakase/gates/block-marker.sh'

# Worktree auto-install (from the base harness): on checkout, copy any MISSING injected
# file into the worktree. Never overwrites a local edit, never touches a tracked path.
post-checkout:
  jobs:
    - name: omakase-ensure-present
      run: bash "$(git rev-parse --git-common-dir)/omakase/ensure-present.sh"
```

- [ ] **Step 2: Update `examples/sample-harness/README.md` line 13.** Change the sentence naming `omakase-ledger.sh` (the scorecard wrapper) to name the primitive:
```
Everything else the wiring uses - `omakase-gate.sh` (the one gate primitive: it runs the
```
Adjust the surrounding clause so it reads cleanly (the primitive both runs the check and records the run; there is no separate wrapper). Read the full sentence in context and rewrite it to one accurate clause.

- [ ] **Step 3: Update the `tests/sources.test.sh` embedded fixture and presence assertions.** Change the fixture job (lines ~209-211) from the wrapper+env form:
```bash
      run: bash .omakase/bin/omakase-ledger.sh source-discipline -- bash .omakase/gates/discipline.sh
    ... env: OMAKASE_HOOK: pre-commit
```
to:
```bash
      run: bash .omakase/bin/omakase-gate.sh source-discipline --step 'bash .omakase/gates/discipline.sh'
```
(remove the `env:`/`OMAKASE_HOOK:` lines for that job). Change the presence assertions (lines ~228-229):
```bash
[ -x "$REPO6/.omakase/bin/omakase-gate.sh" ] && pass "base gate primitive layered in" || fail "base gate primitive missing"
```
and delete the `deferred-check.sh` presence line (line 229) — the deferred gate is gone.

- [ ] **Step 4: Update `tests/build.test.sh` presence assertions.** Change line 26 and line 47 from checking `deferred-check.sh` to checking the primitive:
```bash
[ -f "$GEN/payload/.omakase/bin/omakase-gate.sh" ] && pass "base gate primitive present" || fail "no base gate primitive"
```
(line 26, generic bundle) and:
```bash
[ -f "$FOO/payload/.omakase/bin/omakase-gate.sh" ] && pass "base scaffold retained under stack" || fail "base scaffold lost"
```
(line 47, stack overlay).

- [ ] **Step 5: Update `tests/sample-harness.test.sh`.** Line 6 (header comment) and line 51: change `omakase-ledger.sh` to `omakase-gate.sh`:
```bash
[ -f "$REPO/.omakase/bin/omakase-gate.sh" ] && pass "base machinery layered in (omakase-gate.sh)" || fail "base machinery missing"
```

- [ ] **Step 6: Update `tests/superpowers-harness.test.sh`.** Line 9 (header comment) and line 61: same swap to `omakase-gate.sh`:
```bash
[ -f "$REPO/.omakase/bin/omakase-gate.sh" ] && pass "base machinery layered in (omakase-gate.sh)" || fail "base machinery missing"
```

- [ ] **Step 7: Update the `tests/inject.test.sh` `mkpayload` lefthook fixture (lines ~21-25)** to the primitive form, so it matches what ships:
```bash
  cat > "$p/lefthook-local.yml" <<'YML'
pre-commit:
  jobs:
    - name: omakase-example
      run: bash .omakase/bin/omakase-gate.sh omakase-example --step 'bash .omakase/gates/example.sh'
post-checkout:
  jobs:
    - name: omakase-ensure-present
      run: bash "$(git rev-parse --git-common-dir)/omakase/ensure-present.sh"
YML
```
Note: this payload still ships `.omakase/gates/example.sh` (the `mkpayload` block writes it), so the wiring guard passes.

- [ ] **Step 8: Run the affected tests.** Run:
```bash
for t in inject sources build sample-harness superpowers-harness; do echo "== $t =="; bash tests/$t.test.sh || break; done
```
Expected: each `ALL PASS`. (Old scripts still exist, so any not-yet-updated reference is harmless; this task only adds primitive wiring and primitive-presence assertions.)

- [ ] **Step 9: Commit.**
```bash
git add examples/sample-harness/payload/lefthook-local.yml examples/sample-harness/README.md tests/sources.test.sh tests/build.test.sh tests/sample-harness.test.sh tests/superpowers-harness.test.sh tests/inject.test.sh
git commit -m "refactor(examples,tests): wire fixtures to omakase-gate; assert the primitive is layered in"
```

---

## Task 7: Delete the old scripts; rename the placed-ledger test; final sweep

**Files:**
- Delete: `payload/.omakase/gates/deferred-check.sh`, `payload/.omakase/bin/omakase-ledger.sh`, `payload/.omakase/bin/omakase-record.sh`, `tests/deferred-gate.test.sh`
- Rename: `tests/ledger.test.sh` → `tests/placed.test.sh`

**Interfaces:**
- Consumes: nothing new (all references rewired in Tasks 4-6).
- Produces: a repo with one gate primitive and no dangling references to the deleted scripts.

- [ ] **Step 1: Delete the three old scripts and the old test.**
```bash
git rm payload/.omakase/gates/deferred-check.sh payload/.omakase/bin/omakase-ledger.sh payload/.omakase/bin/omakase-record.sh tests/deferred-gate.test.sh
```

- [ ] **Step 2: Rename the provenance-ledger test to its accurate name.** `tests/ledger.test.sh` tests `placed.tsv` (provenance), not `ledger.tsv`:
```bash
git mv tests/ledger.test.sh tests/placed.test.sh
```
Then update its header comment (line ~1-13) so the title says "provenance ledger (placed.tsv)" and remove any wording that implies it covers the run ledger. No logic change (it already tests `placed.tsv`).

- [ ] **Step 3: Update the "absent" presence assertions.** In `tests/build.test.sh`, `tests/sources.test.sh`, `tests/sample-harness.test.sh`, and `tests/superpowers-harness.test.sh`, the primitive-presence assertions from Task 6 stay. Add one explicit "old machinery is gone" assertion to `tests/build.test.sh` (generic bundle section, after line 26):
```bash
[ ! -e "$GEN/payload/.omakase/gates/deferred-check.sh" ] && [ ! -e "$GEN/payload/.omakase/bin/omakase-ledger.sh" ] && [ ! -e "$GEN/payload/.omakase/bin/omakase-record.sh" ] && pass "old gate machinery dropped from the bundle" || fail "a deleted script still ships in the bundle"
```

- [ ] **Step 4: Run the grep sweep as an acceptance check.** Run:
```bash
grep -rnE 'omakase-ledger\.sh|omakase-record\.sh|deferred-check\.sh|OMAKASE_HOOK|OMAKASE_CHECK|OMAKASE_GLOB|OMAKASE_BASE|original.verdict' payload/ examples/ tests/ bin/ skills/ docs/authoring.md docs/concepts.md docs/harness-surface.md docs/reference.md CHANGELOG.md
```
Expected: NO output (docs are rewritten in Task 8, but if any of the listed docs still match, that is fine to leave until Task 8 — the payload/examples/tests/bin/skills lines MUST be empty). If `skills/add-gate/SKILL.md` still matches, that is expected (rewritten in Task 8); narrow the sweep to `payload/ examples/ tests/ bin/` and require it empty here.

- [ ] **Step 5: Run the full suite.** Run:
```bash
for t in tests/*.test.sh; do echo "== $t =="; bash "$t" || { echo "FAILED: $t"; break; }; done
```
Expected: every test `ALL PASS`.

- [ ] **Step 6: Commit.**
```bash
git add -A
git commit -m "refactor(gate): delete the old gate machinery; rename ledger.test.sh -> placed.test.sh"
```

---

## Task 8: Documentation

**Files:**
- Modify: `skills/add-gate/SKILL.md`
- Modify: `docs/authoring.md`
- Modify: `docs/concepts.md`
- Modify: `docs/harness-surface.md` (review only; likely no change)
- Modify: `CHANGELOG.md`

**Interfaces:**
- Consumes: the final primitive contract and flag set.
- Produces: docs that teach one primitive and three flag-combos, with no dangling references to deleted scripts or the retired two-shape taxonomy.

- [ ] **Step 1: Rewrite `skills/add-gate/SKILL.md` around the one primitive.** Replace the "two shapes (gate / deferred gate)" framing with the one primitive and its flag-combos. Specifically:
  - Step 1 ("find the shape"): becomes "pick the flags." Three questions: (a) what + which event (pre-commit/pre-push); (b) can it run inline and cheaply every time, or is it expensive (add `--cacheable`) / out-of-band (a blocking step + `--record`); (c) does it only touch some paths (add `--glob`).
  - Step 3 ("wire it"): one `payload/lefthook-local.yml` job calling `bash .omakase/bin/omakase-gate.sh <name> --step '<cmd>' [--cacheable] [--glob '<pats>']`. The deferred case is `--cacheable` + a blocking step + an out-of-band `omakase-gate.sh <name> --record` from the job/skill. Remove the `omakase-record.sh --check ... --verdict ...` and `deferred-check.sh` wiring blocks entirely.
  - Remove the dangling `visual-verify`/`review-verify` worked-example references (those dirs do not exist in this repo).
  - Keep Step 2 (pre-flight a third-party tool) and Step 4 (prove it fires); update Step 4's commands to use the primitive and the audited bypass `OMAKASE_SKIP_<NAME>=1`.
  - Update the "See also" bullets to drop the deferred-gate worked-example line.

- [ ] **Step 2: Rewrite `docs/authoring.md` lines ~10-11 and ~29-38.** Replace "the `omakase-ledger.sh` scorecard wrapper, the `omakase-record.sh` recorder, and the `deferred-check.sh` push gate" with "the `omakase-gate.sh` primitive." Replace the "two kinds of gate" section with the one-primitive model: a gate is one `omakase-gate.sh` call; flags (`--cacheable`, `--glob`, `--record`) cover the cached and deferred cases. Remove "The deferred-gate scripts under `.omakase/` ... reusable" paragraph.

- [ ] **Step 3: Rewrite `docs/concepts.md` lines ~6-7 and the "Gates and deferred gates" section (~40-51).** In the machinery list, replace "the scorecard ledger, the deferred-gate scripts" with "the gate primitive (`omakase-gate.sh`) and its scorecard ledger." Rename the section to "Gates" and describe the one primitive: a check wired into a hook via `omakase-gate.sh`; `--cacheable` reuses a per-commit pass; a deferred check is `--cacheable` + a blocking step unblocked by an out-of-band `--record`. Remove the separate "deferred gate is two pieces" explanation.

- [ ] **Step 4: Review `docs/harness-surface.md`.** Confirm it only classifies `.omakase/gates/*` and `lefthook*.yml` as `gate` (path classification, unchanged by this redesign) and does not describe the two-shape taxonomy. The earlier grep showed no machinery references. If it mentions deferred gates anywhere, update; otherwise leave unchanged and note "reviewed, no change" in the commit body.

- [ ] **Step 5: Add a CHANGELOG entry and fix the stale references.** At the top of `CHANGELOG.md`, add a new entry describing the breaking change:
  - One primitive `omakase-gate.sh` replaces `omakase-ledger.sh` + `omakase-record.sh` + `deferred-check.sh`.
  - The run ledger drops to 4 columns (`epoch name verdict sha`); a pre-v2 6-column ledger is rotated aside on `omakase init`.
  - Removed env `OMAKASE_HOOK`/`OMAKASE_CHECK`/`OMAKASE_GLOB`/`OMAKASE_BASE`; the waiver mechanism is gone; the single audited bypass is `OMAKASE_SKIP_<NAME>=1`.
  - **Migration:** adopters run `omakase init` once (the orphan sweep removes the three old scripts, the wiring is re-injected, the old ledger is rotated). A harness author rewires `lefthook-local.yml` to `omakase-gate.sh` and replaces any `omakase-record.sh` calls with `omakase-gate.sh <name> --record`.
  Also fix the stale references at the existing lines ~32-33 and ~86-105 (the older entries that name the deleted scripts): leave historical entries as written EXCEPT where they describe current behavior as a feature list; if an entry is purely historical (describing a past release), do not rewrite it. Use judgment: only the new top entry must describe the new model.

- [ ] **Step 6: Run the full grep sweep across docs.** Run:
```bash
grep -rnE 'omakase-ledger\.sh|omakase-record\.sh|deferred-check\.sh' skills/ docs/authoring.md docs/concepts.md docs/harness-surface.md docs/reference.md
```
Expected: NO output (CHANGELOG.md may retain historical entries; the active docs and the skill must be clean).

- [ ] **Step 7: Run the full suite one final time.** Run:
```bash
for t in tests/*.test.sh; do echo "== $t =="; bash "$t" || { echo "FAILED: $t"; break; }; done
```
Expected: every test `ALL PASS`.

- [ ] **Step 8: Commit.**
```bash
git add skills/add-gate/SKILL.md docs/authoring.md docs/concepts.md docs/harness-surface.md CHANGELOG.md
git commit -m "docs(gate): teach one primitive (--cacheable/--glob/--record); drop the two-shape taxonomy"
```

---

## Self-Review (completed during planning)

**Spec coverage check (spec §1-§10):**
- §2 primitive contract (two modes, three options, behavior order, exit codes, env, carried-over safety) → Task 1.
- §3 three cases from flags → Task 1 (tests) + Task 4 (wiring template) + Task 8 (docs).
- §4 one store, 4-column schema, fail-closed `--record`, atomic append, upgrade rotation → Task 1 (schema, append, record), Task 5 (rotation).
- §5 deletions + renames + de-collide "ledger" → Task 7. Keep `example.sh` → unchanged (Global). Deviation on `scorecard.test.sh` documented in File Structure.
- §6 consumers: `show.sh` → Task 4; `omakase-stop-notice.sh` → Task 4; `init.sh`/wiring guard + stale comments → Task 5; `lefthook-local.yml` + both example payloads → Task 4 (base) + Task 6 (sample-harness); docs → Task 8.
- §7 realistic-harness example → OUT OF SCOPE (separate follow-up, per the spec and the locked decisions). Not in this plan.
- §8 test plan → Tasks 1, 4, 5 (omakase-gate.test.sh checklist + scorecard.test.sh status surfaces + fixture rewrites + grep sweep).
- §9 pixterm-harness migration → OUT OF SCOPE (separate repo, separate PR). Mentioned only in the CHANGELOG migration note.
- §10 non-goals (no size rotation, no networked-fs guarantee, `--base` dropped) → honored (no tasks add them).

**Placeholder scan:** no TBD/TODO left except one deliberate, removable `# TODO(Task 4)` marker in Task 1 Step 10 that Task 4 Step 8 removes. Every code step shows complete code.

**Type/name consistency:** the gate name in the base wiring is `markers` (Task 4) and the scorecard tests assert `markers` (Task 4 Step 7). The ledger columns are `epoch name verdict sha` everywhere (Tasks 1, 4, 5). The `has_row`/`has_run` helpers key on `$2`(name)/`$3`(verdict) consistently. The primitive name regex in `show.sh` is `omakase-gate\.sh [A-Za-z0-9._-]+` (Task 4), matching the wiring form `omakase-gate.sh <name> ...` (Tasks 4, 6).
