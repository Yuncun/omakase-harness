#!/usr/bin/env bash
# omakase-statusline — print the harness scorecard SEGMENT for a status line:
#   🍣 ✓ pre-commit · 3m     green = every gate's most recent run passed
#   🍣 ✗ pre-push · 1m       red   = a gate's most recent run failed
#   🍣 ready                 dim   = nothing recorded yet
# COMPOSE this into your existing status line (Claude Code statusLine / Copilot CLI
# statusLine / tmux status); it never seizes the bar. Reads the shared-git-dir
# ledger, honors NO_COLOR, costs no API tokens. Test hook: OMAKASE_NOW pins "now".
set -uo pipefail

common="$(cd "$(git rev-parse --git-common-dir 2>/dev/null)" 2>/dev/null && pwd)" || common=""
ledger="${common:+$common/omakase/ledger.tsv}"

esc=$'\033'
green="${esc}[32m"; red="${esc}[31m"; dim="${esc}[2m"; reset="${esc}[0m"
[ -n "${NO_COLOR:-}" ] && { green=""; red=""; dim=""; reset=""; }

if [ -z "$ledger" ] || [ ! -s "$ledger" ]; then
  printf '🍣 %sready%s\n' "$dim" "$reset"
  exit 0
fi

# Latest run per gate; overall is red if ANY gate's most recent run failed.
# Also carry the most-recent entry's timestamp and hook for the "ago"/trigger label.
overall=pass; latest_ts=0; latest_hook=-
read -r overall latest_ts latest_hook < <(awk -F'\t' '
  { ts=$1+0
    if (ts >= seen[$3]) { seen[$3]=ts; verd[$3]=$4 }
    if (ts >= maxts)    { maxts=ts; maxhook=$2 } }
  END {
    bad=0; for (g in verd) if (verd[g]=="fail") bad=1
    print (bad ? "fail" : "pass"), maxts, maxhook
  }' "$ledger")

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
