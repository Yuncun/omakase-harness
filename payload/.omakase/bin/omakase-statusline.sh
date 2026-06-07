#!/usr/bin/env bash
# omakase-statusline — print the harness scorecard SEGMENT for a status line:
#   🍣 ✓ pre-commit · 3m     green = every gate's most recent run passed
#   🍣 ✗ pre-commit · 10m    red   = a gate's most recent run failed (label/age are
#                                    that failing gate's, so the ✗ points at the cause)
#   🍣 ready                 dim   = nothing recorded yet
# COMPOSE this into your existing status line (Claude Code statusLine / Copilot CLI
# statusLine / tmux status); it never seizes the bar. Reads the shared-git-dir
# ledger, ignores malformed lines, honors NO_COLOR, costs no API tokens. Test hook:
# OMAKASE_NOW pins "now".
set -uo pipefail

# Resolve the shared git dir without ever doing `cd ""` (which would point at cwd).
gitdir="$(git rev-parse --git-common-dir 2>/dev/null)" || gitdir=""
common=""; [ -n "$gitdir" ] && common="$(cd "$gitdir" 2>/dev/null && pwd)"
ledger="${common:+$common/omakase/ledger.tsv}"

esc=$'\033'
green="${esc}[32m"; red="${esc}[31m"; dim="${esc}[2m"; reset="${esc}[0m"
[ -n "${NO_COLOR:-}" ] && { green=""; red=""; dim=""; reset=""; }
ready() { printf '🍣 %sready%s\n' "$dim" "$reset"; exit 0; }

[ -z "$ledger" ] || [ ! -s "$ledger" ] && ready

# Latest valid run per gate; overall is red if ANY gate's most recent run failed.
# When red, report the most-recent FAILING gate's timestamp+hook (so the label/age
# match the ✗); when green, the most-recent gate overall. Only well-formed rows count.
overall=none; latest_ts=0; latest_hook=-
read -r overall latest_ts latest_hook < <(awk -F'\t' '
  NF==5 && $1 ~ /^[0-9]+$/ {
    ts=$1+0
    if (ts >= seen[$3]) { seen[$3]=ts; verd[$3]=$4; hk[$3]=$2 }
  }
  END {
    bad=0; failts=-1; failhook="-"; allts=-1; allhook="-"
    for (g in seen) {
      if (seen[g] >= allts) { allts=seen[g]; allhook=hk[g] }
      if (verd[g]=="fail") { bad=1; if (seen[g] >= failts) { failts=seen[g]; failhook=hk[g] } }
    }
    if (allts < 0)   print "none", 0, "-"
    else if (bad)    print "fail", failts, failhook
    else             print "pass", allts, allhook
  }' "$ledger")

{ [ "$overall" = "none" ] || [ "${latest_ts:-0}" -le 0 ]; } && ready

now="${OMAKASE_NOW:-$(date +%s)}"
diff=$(( now - latest_ts )); [ "$diff" -lt 0 ] && diff=0
if   [ "$diff" -lt 60 ];    then ago="<1m"
elif [ "$diff" -lt 3600 ];  then ago="$(( diff / 60 ))m"
elif [ "$diff" -lt 86400 ]; then ago="$(( diff / 3600 ))h"
else                              ago="$(( diff / 86400 ))d"
fi

if [ "$overall" = "fail" ]; then mark="${red}✗${reset}"; else mark="${green}✓${reset}"; fi
trigger=""; { [ -n "$latest_hook" ] && [ "$latest_hook" != "-" ]; } && trigger=" $latest_hook"
printf '🍣 %s%s · %s\n' "$mark" "$trigger" "$ago"
