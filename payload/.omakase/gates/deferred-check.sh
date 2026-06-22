#!/usr/bin/env bash
# Deferred gate: confirm a job recorded a fresh PASS for the code being pushed.
# The hook does NOT run the check; it reads a record the job wrote in-session
# (see the sibling bin/omakase-record.sh). For checks that cannot run inside a
# hook - an LLM review, a slow render, a human sign-off.
#
# GENERIC + DORMANT BY DEFAULT. Reads its parameters from env, so a repo that
# wires no deferred gate never sees it. Wire it as a pre-push job, wrapped in
# omakase-ledger.sh so the run lands in the scorecard:
#
#   pre-push:
#     jobs:
#       - name: deferred-check-<name>
#         run: bash .omakase/bin/omakase-ledger.sh <name> -- bash .omakase/gates/deferred-check.sh
#         env:
#           OMAKASE_CHECK: <name>          # matches the record + the job. UNSET = dormant.
#           OMAKASE_GLOB: 'src/* lib/*'    # fires only when a pushed file matches
#           OMAKASE_HOOK: pre-push
#
# Env:
#   OMAKASE_CHECK - check name; matches the record + the job. UNSET = dormant.
#   OMAKASE_GLOB  - space-separated path globs; the gate applies only when a file
#                   in the pushed range matches one. Patterns are shell `case`
#                   globs (a single * spans directories).
#   OMAKASE_BASE  - optional range base. Default: the remote's default branch.
#
# Scope is a HEURISTIC against the local remote-tracking ref, not git's exact
# pushed-ref protocol; it can over- or under-scope on multi-ref / non-origin /
# stale-ref pushes.
set -euo pipefail

CHECK="${OMAKASE_CHECK:-}"
[[ -z "$CHECK" ]] && exit 0

# Per-invocation escape hatch (audited - document the reason in the PR).
SKIP_VAR="OMAKASE_SKIP_$(printf '%s' "$CHECK" | tr '[:lower:]-' '[:upper:]_')"
if [[ "${!SKIP_VAR:-0}" == "1" ]]; then
  echo "deferred-check[$CHECK]: skipped via $SKIP_VAR"
  exit 0
fi

# Resolve a base ref defensively. If none resolves (fresh clone before first
# fetch, no origin remote, or a default branch that is neither master nor main),
# fail OPEN: a missing base must never hard-block a push with a raw git error.
# The threat model is the agent's omission, not forgery, so fail-open here is safe.
resolve_base() {
  local c
  if [[ -n "${OMAKASE_BASE:-}" ]] \
     && git rev-parse --verify --quiet "${OMAKASE_BASE}^{commit}" >/dev/null 2>&1; then
    printf '%s\n' "$OMAKASE_BASE"; return 0
  fi
  for c in "$(git rev-parse --abbrev-ref --symbolic-full-name origin/HEAD 2>/dev/null)" \
           origin/master origin/main; do
    [[ -n "$c" ]] || continue
    if git rev-parse --verify --quiet "${c}^{commit}" >/dev/null 2>&1; then
      printf '%s\n' "$c"; return 0
    fi
  done
  return 1
}

if ! BASE="$(resolve_base)"; then
  echo "deferred-check[$CHECK]: no resolvable base ref - skipping scope check (fail-open)"
  exit 0
fi

# Files changed on this branch, merge-base bounded (three-dot) so a file changed
# only on the base since branch-point does not false-trigger the gate. If the three-dot
# range is UNRESOLVABLE (unrelated histories / no merge base -> git fatals instead of
# returning empty), fall back to a two-dot diff so a range ERROR can't masquerade as
# "no changes" and silently skip an in-scope push.
if ! CHANGED="$(git diff --name-only "${BASE}...HEAD" 2>/dev/null)"; then
  CHANGED="$(git diff --name-only "${BASE}..HEAD" 2>/dev/null || true)"
fi

# A wired gate (CHECK is set) with no trigger globs is a misconfiguration. An empty
# OMAKASE_GLOB used to leave `matched` at 0 and silently SKIP (fail-open). Refuse instead.
if [[ -z "${OMAKASE_GLOB:-}" ]]; then
  echo "deferred-check[$CHECK]: OMAKASE_GLOB is not set - cannot tell which pushes this gate guards." >&2
  echo "  Fix: set OMAKASE_GLOB to the trigger paths (e.g. 'src/* lib/*'), or '*' to gate every push." >&2
  exit 1
fi

matched=0
if [[ -n "$CHANGED" ]]; then
  # noglob: $OMAKASE_GLOB must word-split into literal case patterns (src/*),
  # NOT pathname-expand against the working tree. Without this, a pattern that
  # matches real files (the common case) expands to those filenames and the
  # literal pattern is lost, so nested paths silently fail to match.
  set -f
  while IFS= read -r file; do
    [[ -z "$file" ]] && continue
    for g in $OMAKASE_GLOB; do
      # shellcheck disable=SC2254
      case "$file" in
        $g) matched=1; break;;
      esac
    done
    [[ $matched -eq 1 ]] && break
  done <<< "$CHANGED"
  set +f
fi

if [[ $matched -eq 0 ]]; then
  echo "deferred-check[$CHECK]: no files matching trigger globs in range - skipping"
  exit 0
fi

# In scope: a fresh PASS record must exist for the exact commit being pushed.
KEY="$(git rev-parse HEAD)"
REC="$(git rev-parse --git-path omakase)/deferred/$CHECK.json"

block() {
  {
    echo ""
    echo "BLOCKED: deferred gate '$CHECK' - $1"
    echo "  Fix: run the '$CHECK' job (it records the result), then push again."
    echo "  Escape (audited - document in the PR): ${SKIP_VAR}=1 git push ..."
    echo ""
  } >&2
  exit 1
}

[[ -f "$REC" ]] || block "no record found (the check has not run on this code)"

# Parse with sed (no jq dependency). Positive tests only - never `!= fail`.
rec_field() { sed -n "s/.*\"$1\"[[:space:]]*:[[:space:]]*\"\([^\"]*\)\".*/\1/p" "$REC"; }
REC_KEY="$(rec_field key)"
REC_VERDICT="$(rec_field verdict)"
REC_REASON="$(rec_field reason)"
REC_ORIGINAL="$(rec_field original_verdict)"   # "fail" only for a true waiver; null/empty otherwise

[[ -n "$REC_KEY" && -n "$REC_VERDICT" ]] || block "record is corrupt or incomplete - re-run"
[[ "$REC_KEY" == "$KEY" ]] || \
  block "record is stale (covers ${REC_KEY:0:8}, pushing ${KEY:0:8}) - re-run after your latest commit"
[[ "$REC_VERDICT" == "pass" ]] || block "last run verdict was '$REC_VERDICT'"

# A waiver is a PASS recorded OVER a judged FAIL (original_verdict=fail) - omakase-record
# requires a reason for exactly that case. Surface it loudly so the human always sees what
# was overridden. A plain PASS that merely carries an informational --reason is NOT a waiver
# and must not be branded as an override.
if [[ "$REC_ORIGINAL" == "fail" ]]; then
  {
    echo ""
    echo "WAIVED: deferred gate '$CHECK' passed over a recorded FAIL -"
    echo "  reason: $REC_REASON"
    echo ""
  } >&2
fi

echo "deferred-check[$CHECK]: fresh PASS for ${KEY:0:8} - ok"
exit 0
