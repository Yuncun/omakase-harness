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
if [ "$RECORD" -eq 1 ] && { [ "$HAVE_STEP" -eq 1 ] || [ "$CACHEABLE" -eq 1 ] || [ -n "$GLOB" ]; }; then
  die_misuse "--record takes no other flags (it writes a pass for HEAD without running anything)"
fi
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
skipvar="OMAKASE_SKIP_$(printf '%s' "$NAME" | tr '[:lower:].-' '[:upper:]__')"
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
