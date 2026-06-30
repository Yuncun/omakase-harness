#!/usr/bin/env bash
# omakase-stop-notice — a short, end-of-turn status for the developer driving the session.
# Opt-in Claude Code Stop hook: add it to .claude/settings.json (init prints how). Reads the Stop-hook
# JSON on stdin. Deterministic — no LLM, no API tokens. Never blocks the turn.
#
# The states it can show (always the harness's name, no 🥡; detail lives in omakase status):
#   <name> is active ✓                                        harness deployed and gates armed
#   <name> is active ✓ / Last run: 8/8 checks at <clk>       a run just finished, all passed
#   <name> is active ✓ / Last run: 2 checks failed ...        a run failed - header stays "active"
#                                                             (it tracks the harness, not the run)
#   <name> is not active                                      overlay present but gates not armed
#   <name> — files missing · omakase init to update          overlay incomplete in this worktree
#
# "Last run" = the most recent hook run (pre-commit OR pre-push), summarised from the
# shared ledger via latest-verdict-per-gate (so a check that failed then passed on the
# same commit counts as passed). Clock time, not "Nm ago": the line is frozen once
# printed, so a relative time would go stale.
#
# Stays SILENT unless the state changed: a run finished this turn, the enabled/missing
# state changed, or it's a new Claude session (so the resting "is active ✓" shows once per
# session, not every turn). A per-worktree marker (keyed by worktree path) remembers it.
set -uo pipefail

input="$(cat)"
field() { printf '%s' "$input" | sed -n 's/.*"'"$1"'"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' | head -1; }
cwd="$(field cwd)"; [ -n "$cwd" ] || cwd="$PWD"
session="$(field session_id)"

root="$(git -C "$cwd" rev-parse --show-toplevel 2>/dev/null)" || exit 0
[ -n "$root" ] || exit 0
gcd="$(git -C "$cwd" rev-parse --git-common-dir 2>/dev/null)" || exit 0
common="$(cd "$cwd" 2>/dev/null && cd "$gcd" 2>/dev/null && pwd)" || exit 0

# Active only where omakase is installed (same rule as the statusline): the overlay dir
# in this worktree, or the shared omakase dir. In any other repo -> say nothing.
{ [ -d "$root/.omakase" ] || [ -d "$common/omakase" ]; } || exit 0

name="${OMAKASE_NAME:-}"
[ -z "$name" ] && [ -f "$root/.omakase/NAME" ] && name="$(tr -d '[:cntrl:] ' < "$root/.omakase/NAME" 2>/dev/null)"
[ -n "$name" ] || name="omakase"

# enabled? — gates are "armed" when git's effective hooks dir holds a lefthook-managed
# stub. --git-path honors core.hooksPath, so this is true wherever a commit/push would
# actually run the harness, and false when a foreign manager (or removal) took the hooks.
# --git-path returns a path relative to the working dir, so read it FROM $cwd and, if it
# came back relative, anchor it there.
hooksdir="$(cd "$cwd" 2>/dev/null && git rev-parse --git-path hooks 2>/dev/null || true)"
case "${hooksdir:-}" in ''|/*) : ;; *) hooksdir="$cwd/$hooksdir" ;; esac
armed=0
for h in pre-commit pre-push; do
  [ -f "$hooksdir/$h" ] && grep -qi lefthook "$hooksdir/$h" 2>/dev/null && { armed=1; break; }
done

# install nudge — any ENABLED placed file missing from this worktree means the overlay is
# incomplete here (e.g. a fresh `git worktree add` that hasn't self-healed). One fix for
# all of it: omakase init. (An EDITED file is not flagged — that may be intentional and
# init would clobber it; that case stays in omakase status.)
nudge=""
placed="$common/omakase/placed.tsv"
if [ -f "$placed" ]; then
  # `|| [ -n "$rel" ]` processes a final line with no trailing newline. Only ENABLED
  # rows count (enabled=1, matching status.sh); a malformed/blank row is skipped, not nudged.
  while IFS=$'\t' read -r rel kind src hash enabled || [ -n "$rel" ]; do
    [ "$enabled" = "1" ] && [ -n "$rel" ] || continue
    [ -e "$root/$rel" ] || [ -L "$root/$rel" ] || { nudge="files missing · omakase init to update"; break; }
  done < "$placed"
fi

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

clock() { # epoch -> "3:42PM" (BSD `date -r`, GNU `date -d @`); drop a leading zero hour
  local e="$1" t; t="$(date -r "$e" '+%I:%M%p' 2>/dev/null)"
  [ -n "$t" ] || t="$(date -d "@$e" '+%I:%M%p' 2>/dev/null)"; printf '%s' "${t#0}"
}

# marker (per worktree): session, last-seen max epoch, and a status signature that captures
# the non-run state (disabled / nudge) so a change to those re-announces too.
key="$(printf '%s' "$root" | cksum | awk '{print $1}')"
marker="$common/omakase/notice-$key.marker"
mkdir -p "$common/omakase" 2>/dev/null
prev_session=""; prev_maxepoch=0; prev_statusig=""
[ -f "$marker" ] && IFS=$'\t' read -r prev_session prev_maxepoch prev_statusig < "$marker" 2>/dev/null || true
case "${prev_maxepoch:-}" in ''|*[!0-9]*) prev_maxepoch=0;; esac

if [ "$armed" -eq 0 ]; then statusig="disabled"; else statusig="enabled|$nudge"; fi
# "a run finished this turn" = the newest run epoch advanced. Epoch is 1-second granularity,
# so two runs in the same second can't be told apart here — the rare second one waits for the
# next state change. Acceptable; the alternative (tracking a full run signature) is not worth it.
ran_this_turn=0; [ "$maxepoch" -gt "$prev_maxepoch" ] && ran_this_turn=1

speak=0
[ -f "$marker" ] || speak=1
[ "$session" != "$prev_session" ] && speak=1
[ "$ran_this_turn" -eq 1 ] && speak=1
[ "$statusig" != "$prev_statusig" ] && speak=1

printf '%s\t%s\t%s\n' "$session" "$maxepoch" "$statusig" > "$marker" 2>/dev/null || true
[ "$speak" -eq 1 ] || exit 0

# render — line 1 is the harness's own status: "is active ✓" when gates are armed (deployed),
# "is not active" when they aren't. It does NOT change on a failed run — a run's result lives
# only on the "Last run:" line below. The check is the light text ✓ (no colour: a Stop
# systemMessage can't carry colour codes; there is no X — a failure reads from the words).
if [ "$armed" -eq 0 ]; then
  msg="$name is not active"
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
else
  msg="$name is active ✓"
fi
[ "$armed" -eq 1 ] && [ -n "$nudge" ] && msg="$msg
$name — $nudge"

# JSON-escape (backslash, quote) and fold newlines to \n.
esc="$(printf '%s' "$msg" | sed 's/\\/\\\\/g; s/"/\\"/g' | awk 'NR>1{printf "\\n"} {printf "%s", $0}')"
printf '{"systemMessage":"%s"}\n' "$esc"
exit 0
