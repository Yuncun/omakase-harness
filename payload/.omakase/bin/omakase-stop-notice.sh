#!/usr/bin/env bash
# omakase-stop-notice — Claude Stop-hook progress checklist for the developer driving
# the session: which of the harness's PUSH checks have run for the CURRENT commit, and
# which still remain. Reads stdin (the Stop hook JSON). Stays SILENT unless something
# changed this turn (HEAD moved or a check ran), so it doesn't repeat on chat turns.
# Only the 🍣 icon; never blocks the turn.
#
# "Run" = a ledger row for this commit (column 6 = sha) with hook=pre-push. The full
# set of push checks is learned from ledger history. Pre-commit checks auto-run and are
# excluded. Marker (per worktree) remembers the last HEAD + last ledger position.
set -uo pipefail

input="$(cat)"
cwd="$(printf '%s' "$input" | sed -n 's/.*"cwd"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' | head -1)"
[ -n "$cwd" ] || cwd="$PWD"

head="$(git -C "$cwd" rev-parse HEAD 2>/dev/null)" || exit 0
[ -n "$head" ] || exit 0
wt="$(git -C "$cwd" rev-parse --show-toplevel 2>/dev/null)" || exit 0
gcd="$(git -C "$cwd" rev-parse --git-common-dir 2>/dev/null)" || exit 0
common="$(cd "$cwd" 2>/dev/null && cd "$gcd" 2>/dev/null && pwd)" || exit 0
ledger="$common/omakase/ledger.tsv"
[ -s "$ledger" ] || exit 0

maxepoch="$(awk -F'\t' '$1 ~ /^[0-9]+$/ && ($1+0)>m{m=$1+0} END{print m+0}' "$ledger")"
key="$(printf '%s' "$wt" | cksum | awk '{print $1}')"
marker="$common/omakase/checklist-$key.marker"

# First time in this worktree this session: remember where we are, say nothing.
if [ ! -f "$marker" ]; then
  mkdir -p "$common/omakase" 2>/dev/null
  printf '%s %s\n' "$head" "$maxepoch" > "$marker" 2>/dev/null || true
  exit 0
fi
prevhead=""; prevmax=0
read -r prevhead prevmax < "$marker" 2>/dev/null || true
case "${prevmax:-}" in ''|*[!0-9]*) prevmax=0;; esac

# Speak only when HEAD moved or a check ran since last time. This is the guard.
{ [ "$head" != "$prevhead" ] || [ "${maxepoch:-0}" -gt "$prevmax" ]; } || exit 0
printf '%s %s\n' "$head" "$maxepoch" > "$marker" 2>/dev/null || true

# The push checks IN GATE ORDER. ✓ = completed (passed for THIS commit); ✗ = not yet
# completed (not run, or failed). Order AND the full set come from the lefthook config
# (the authoritative execution order); fall back to ledger history if it's missing.
# \342\234\223 is ✓ and \342\234\227 is ✗ (octal, awk-safe).
gates="$(awk '
  /^pre-push:/{p=1; next} /^[A-Za-z]/{p=0}
  p && /omakase-ledger\.sh/ { s=$0; sub(/.*omakase-ledger\.sh[ \t]+/,"",s); sub(/[ \t].*/,"",s); if(s!="") print s }
' "$wt/lefthook-local.yml" 2>/dev/null)"
[ -n "$gates" ] || gates="$(awk -F'\t' '$2=="pre-push" && !seen[$3]++{print $3}' "$ledger")"

pass_set="$(awk -F'\t' -v h="$head" 'NF>=6 && $2=="pre-push" && $6==h && $4=="pass"{print $3}' "$ledger" | sort -u | tr '\n' ' ')"
checks="$(printf '%s\n' "$gates" | awk -v pass="$pass_set" '
  BEGIN{ n=split(pass, a, " "); for(i=1;i<=n;i++) if(a[i]!="") P[a[i]]=1 }
  $0!="" { m=($0 in P)?" \342\234\223":" \342\234\227"; l=l (l?"  ":"") $0 m }
  END{ print l }')"
[ -n "$checks" ] || exit 0

name="omakase"
[ -f "$wt/.omakase/NAME" ] && name="$(tr -d ' \n' < "$wt/.omakase/NAME" 2>/dev/null)"
name="${OMAKASE_NAME:-$name}"; [ -n "$name" ] || name="omakase"
icon="${OMAKASE_ICON:-🍣}"

msg="$icon $name · pre-push — $checks"
printf '{"systemMessage":"%s"}\n' "$msg"
exit 0
