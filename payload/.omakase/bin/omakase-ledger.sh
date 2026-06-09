#!/usr/bin/env bash
# omakase-ledger — wrap a gate command, append a run record to the harness ledger,
# and pass the command's exit code through UNCHANGED. Best-effort: a ledger write
# failure never blocks the gate. Usage:
#   bash .omakase/bin/omakase-ledger.sh <gate-name> -- <command> [args...]
# The trigger label comes from $OMAKASE_HOOK (lefthook exposes no hook name to jobs;
# set it per hook in lefthook-local.yml). The ledger lives in the shared git dir
# (.git/omakase/ledger.tsv) so the main checkout and every worktree share one run
# history. Tab-separated columns: epoch <tab> hook <tab> gate <tab> verdict <tab> ms <tab> sha.
# Test hook: OMAKASE_NOW pins "now".
set -uo pipefail   # NOT -e: we must capture the gate's exit code, not die on it.

gate="${1:-gate}"; shift || true
[ "${1:-}" = "--" ] && shift
[ "$#" -eq 0 ] && exit 0     # no command to run -> nothing to record

# Keep ledger columns intact even if a gate/hook name contains a tab or newline.
gate="${gate//$'\t'/ }"; gate="${gate//$'\n'/ }"
hook="${OMAKASE_HOOK:--}"; hook="${hook//$'\t'/ }"; hook="${hook//$'\n'/ }"

# Resolve the ledger path BEFORE running the gate: a gate that changes the working
# directory must not be able to misdirect (or silently drop) its own record. An
# empty rev-parse result must NOT become `cd ""` (a no-op that would point at cwd
# and litter a stray omakase/ dir outside any repo).
gitdir="$(git rev-parse --git-common-dir 2>/dev/null)" || gitdir=""
common=""; [ -n "$gitdir" ] && common="$(cd "$gitdir" 2>/dev/null && pwd)"

# Tag each run with the commit it ran on, so a reader can tell which checks have run
# for the CURRENT code. At pre-push, HEAD is the commit being pushed.
sha="$(git rev-parse HEAD 2>/dev/null)" || sha=""
sha="${sha//$'\t'/ }"; sha="${sha//$'\n'/ }"

now() { echo "${OMAKASE_NOW:-$(date +%s)}"; }
start="$(now)"
"$@"
rc=$?
end="$(now)"

if [ -n "$common" ]; then
  {
    mkdir -p "$common/omakase"
    verdict=pass; [ "$rc" -ne 0 ] && verdict=fail
    printf '%s\t%s\t%s\t%s\t%s\t%s\n' \
      "$end" "$hook" "$gate" "$verdict" "$(( (end - start) * 1000 ))" "$sha" \
      >> "$common/omakase/ledger.tsv"
  } 2>/dev/null || true
fi

exit "$rc"
