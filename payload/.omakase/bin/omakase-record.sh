#!/usr/bin/env bash
# omakase-record — wrap a gate command, append a run record to the harness ledger,
# and pass the command's exit code through UNCHANGED. Best-effort: a ledger write
# failure never blocks the gate. Usage:
#   bash .omakase/bin/omakase-record.sh <gate-name> [--hook <name>] -- <command> [args...]
# The trigger label may also come from $OMAKASE_HOOK (lefthook exposes no hook name
# to jobs). The ledger lives in the shared git dir (.git/omakase/ledger.tsv) so the
# main checkout and every worktree share one run history. Tab-separated columns:
#   epoch <tab> hook <tab> gate <tab> verdict <tab> duration_ms
# Test hook: OMAKASE_NOW pins "now".
set -uo pipefail   # NOT -e: we must capture the gate's exit code, not die on it.

gate="${1:-gate}"; shift || true
hook="${OMAKASE_HOOK:--}"
if [ "${1:-}" = "--hook" ]; then hook="${2:--}"; shift 2 || true; fi
[ "${1:-}" = "--" ] && shift

now() { echo "${OMAKASE_NOW:-$(date +%s)}"; }
start="$(now)"
"$@"
rc=$?
end="$(now)"

# Record. Wrapped so nothing here can change the gate's outcome.
{
  common="$(cd "$(git rev-parse --git-common-dir 2>/dev/null)" 2>/dev/null && pwd)" || common=""
  if [ -n "$common" ]; then
    mkdir -p "$common/omakase"
    verdict=pass; [ "$rc" -ne 0 ] && verdict=fail
    printf '%s\t%s\t%s\t%s\t%s\n' \
      "$end" "$hook" "$gate" "$verdict" "$(( (end - start) * 1000 ))" \
      >> "$common/omakase/ledger.tsv"
  fi
} 2>/dev/null || true

exit "$rc"
