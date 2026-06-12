#!/usr/bin/env bash
# omakase-harness show — render the installed (gitignored, invisible) harness as ONE
# readable map: every placed file, which git hooks run what, and what is hidden via
# .git/info/exclude. Read-only. This is the cure for "the install is invisible" — it
# lets you SEE the whole harness at a glance without committing anything.
#
# Two output modes:
#   (default)    terminal — ANSI banner box + indented columns, for a real terminal.
#   --markdown   Markdown — for the /omakase command to relay VERBATIM into the chat,
#                so the script owns the formatting and Claude never reformats (no drift,
#                no editorializing). Renders as a real heading/list/table in the reply.
set -euo pipefail

FORMAT=term
case "${1:-}" in --markdown|-m|md) FORMAT=md;; esac
ICON="${OMAKASE_ICON:-🍣}"

ROOT="$(git rev-parse --show-toplevel 2>/dev/null)" || { echo "omakase: not inside a git repo" >&2; exit 1; }
COMMON="$(cd "$ROOT" && cd "$(git rev-parse --git-common-dir)" && pwd)"
OMK="$COMMON/omakase"
EXCLUDE="$COMMON/info/exclude"   # shared git dir — also correct inside a linked worktree, where $ROOT/.git is a file
RUNS="$OMK/ledger.tsv"      # gate-RUN ledger (omakase-ledger.sh): epoch,hook,gate,verdict,ms,sha
PLACED="$OMK/placed.tsv"    # provenance ledger (init.sh): path,kind,source,sha256,enabled
BEGIN="# >>> omakase-harness >>>"
END="# <<< omakase-harness <<<"

if [ ! -f "$PLACED" ]; then
  # pre-0.10 installs recorded placements in placed.list; the harness IS installed —
  # never report a false negative about an enforcement system.
  if [ -f "$OMK/placed.list" ]; then
    if [ "$FORMAT" = md ]; then
      echo "**Pre-0.10 omakase install detected** (record: \`placed.list\`). Run \`/omakase init\` to migrate to the provenance ledger. Placed files:"
      sed 's/^/- `/; s/$/`/' "$OMK/placed.list"
    else
      echo "Pre-0.10 omakase install detected (record: placed.list)."
      echo "Run  /omakase init  to migrate to the provenance ledger. Placed files:"
      sed 's/^/  /' "$OMK/placed.list"
    fi
    exit 0
  fi
  if [ "$FORMAT" = md ]; then
    echo "**No omakase harness is installed in this repo.** Run \`/omakase init\` to inject one."
  else
    echo "No omakase harness is installed in this repo."
    echo "Run  /omakase init  to inject one."
  fi
  exit 0
fi

# ============================ Markdown mode ============================
# The script emits the final Markdown; the /omakase command relays it verbatim.
if [ "$FORMAT" = md ]; then
  LH=""
  if [ -n "${LEFTHOOK_BIN:-}" ]; then LH="$LEFTHOOK_BIN"
  elif command -v lefthook >/dev/null 2>&1; then LH="lefthook"
  elif [ -x "$ROOT/node_modules/.bin/lefthook" ]; then LH="$ROOT/node_modules/.bin/lefthook"; fi
  DUMP=""
  [ -n "$LH" ] && DUMP="$( cd "$ROOT" && "$LH" dump 2>/dev/null || true )"

  echo "## $ICON omakase-harness"
  echo
  echo "Installed in \`$ROOT\`. Every file below is gitignored via \`.git/info/exclude\` — invisible to git, never committed."
  echo
  echo "### Placed files ($(grep -c . "$PLACED"))"
  while IFS=$'\t' read -r rel kind src hash enabled; do
    [ -z "$rel" ] && continue
    if [ "$enabled" = "0" ]; then
      echo "- \`$rel\` — disabled (not restored, not verified)"
    elif [ -L "$ROOT/$rel" ]; then
      echo "- \`$rel\` → \`$(readlink "$ROOT/$rel")\`"
    elif [ -e "$ROOT/$rel" ]; then
      echo "- \`$rel\`"
    else
      echo "- \`$rel\` — **MISSING** (run \`/omakase init\` to restore)"
    fi
  done < "$PLACED"
  echo
  echo "### Git hooks"
  if [ -n "$DUMP" ]; then
    echo '```yaml'
    printf '%s\n' "$DUMP"
    echo '```'
  elif [ -f "$ROOT/lefthook-local.yml" ]; then
    echo "_lefthook not resolved — raw wiring file:_"
    echo '```yaml'
    cat "$ROOT/lefthook-local.yml"
    echo '```'
  else
    echo "_(no hook wiring found)_"
  fi
  echo
  echo "### Recent runs"
  if [ -s "$RUNS" ]; then
    echo "| Gate | Verdict | When |"
    echo "| ---- | ------- | ---- |"
    now="${OMAKASE_NOW:-$(date +%s)}"
    awk -F'\t' -v now="$now" '
      NF>=5 && $1 ~ /^[0-9]+$/ { ts=$1+0; if (ts >= seen[$3]) { seen[$3]=ts; verd[$3]=$4 } }
      END {
        for (g in seen) {
          d=now-seen[g]; if (d < 0) d=0
          if      (d < 60)    a="<1m"
          else if (d < 3600)  a=int(d/60)"m"
          else if (d < 86400) a=int(d/3600)"h"
          else                a=int(d/86400)"d"
          mark=(verd[g]=="fail" ? "\342\234\227 fail" : "\342\234\223 pass")
          printf "%s\t| %s | %s | %s ago |\n", g, g, mark, a
        }
      }' "$RUNS" | sort | cut -f2-
  else
    echo "_No gate runs recorded yet — gates wired through \`omakase-ledger.sh\` log here._"
  fi
  echo
  echo "### Hidden via \`.git/info/exclude\`"
  if [ -f "$EXCLUDE" ]; then
    hidden="$(awk -v b="$BEGIN" -v e="$END" '$0==b{s=1;next} $0==e{s=0} s&&NF{printf "`%s`, ", $0}' "$EXCLUDE")"
    echo "${hidden%, }"
  fi
  echo
  echo "_Refresh:_ \`/omakase init\`  ·  _Remove:_ \`/omakase remove\`  ·  _read-only; running show changes nothing._"
  exit 0
fi

# ============================ Terminal mode (default) ============================
BANNER="$ROOT/.omakase/bin/omakase-banner.sh"
if [ -f "$BANNER" ]; then bash "$BANNER" 2>/dev/null || true; fi
echo "installed in $ROOT"
echo "(every file below is gitignored via .git/info/exclude: invisible to git, never committed)"
echo
echo "PLACED FILES"
while IFS=$'\t' read -r rel kind src hash enabled; do
  [ -z "$rel" ] && continue
  if [ "$enabled" = "0" ]; then
    echo "  - $rel   (disabled — not restored, not verified)"
  elif [ -e "$ROOT/$rel" ] || [ -L "$ROOT/$rel" ]; then
    if [ -L "$ROOT/$rel" ]; then echo "  + $rel -> $(readlink "$ROOT/$rel")"; else echo "  + $rel"; fi
  else
    echo "  ! $rel   (MISSING — run /omakase init to restore)"
  fi
done < "$PLACED"
echo

echo "GIT HOOKS — what runs, and when"
LH=""
if [ -n "${LEFTHOOK_BIN:-}" ]; then LH="$LEFTHOOK_BIN"
elif command -v lefthook >/dev/null 2>&1; then LH="lefthook"
elif [ -x "$ROOT/node_modules/.bin/lefthook" ]; then LH="$ROOT/node_modules/.bin/lefthook"; fi
DUMP=""
[ -n "$LH" ] && DUMP="$( cd "$ROOT" && "$LH" dump 2>/dev/null || true )"
if [ -n "$DUMP" ]; then
  printf '%s\n' "$DUMP" | sed 's/^/  /'
elif [ -f "$ROOT/lefthook-local.yml" ]; then
  echo "  (lefthook not resolved — showing the raw wiring file)"
  sed 's/^/  /' "$ROOT/lefthook-local.yml"
else
  echo "  (no hook wiring found)"
fi
echo

echo "RECENT RUNS — most recent verdict per gate"
if [ -s "$RUNS" ]; then
  now="${OMAKASE_NOW:-$(date +%s)}"
  awk -F'\t' -v now="$now" '
    NF>=5 && $1 ~ /^[0-9]+$/ { ts=$1+0; if (ts >= seen[$3]) { seen[$3]=ts; verd[$3]=$4; hook[$3]=$2 } }
    END {
      for (g in seen) {
        d=now-seen[g]; if (d < 0) d=0
        if      (d < 60)    a="<1m"
        else if (d < 3600)  a=int(d/60)"m"
        else if (d < 86400) a=int(d/3600)"h"
        else                a=int(d/86400)"d"
        h=(hook[g]=="-" ? "" : hook[g]" ")
        # leading "<gate><tab>" is a sort key, stripped by cut below
        printf "%s\t  %s  %-4s  %s%s  (%s ago)\n", g, (verd[g]=="fail" ? "\342\234\227" : "\342\234\223"), verd[g], h, g, a
      }
    }' "$RUNS" | sort | cut -f2-
else
  echo "  (no gate runs recorded yet — gates wired through omakase-ledger.sh log here)"
fi
echo

echo "HIDDEN VIA .git/info/exclude"
if [ -f "$EXCLUDE" ]; then
  awk -v b="$BEGIN" -v e="$END" '$0==b{s=1;next} $0==e{s=0} s&&NF{print "  "$0}' "$EXCLUDE"
fi
echo
echo "Update to the latest harness (overwrites injected files to match):  /omakase init"
echo "Undo everything:                                                    /omakase remove"
