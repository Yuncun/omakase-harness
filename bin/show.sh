#!/usr/bin/env bash
# omakase-harness show — render the installed (gitignored, invisible) harness as ONE
# readable map: an inventory of every harness artifact grouped by origin (committed /
# injected / personal), which git hooks run what, and what is hidden via
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

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
. "$SCRIPT_DIR/lib-harness-paths.sh"   # kind_of() + committed-scan globs (shared with init/import)

# Drift detection (read-only) — does a placed file still match the hash recorded at init?
# Mirrors init.sh hash_of() + ensure-present.sh EXACTLY (symlink -> readlink target string;
# file -> bytes verbatim; same digest tool) so the audit view never disagrees with the
# post-checkout warning. No digest tool -> never reports drift (degrades to silence).
if command -v shasum >/dev/null 2>&1; then _omk_sha() { shasum -a 256; }
elif command -v sha256sum >/dev/null 2>&1; then _omk_sha() { sha256sum; }
else _omk_sha() { return 1; }; fi
omakase_hash_of() {  # $1 = path; echoes the hex digest, or nothing if no digest tool
  command -v shasum >/dev/null 2>&1 || command -v sha256sum >/dev/null 2>&1 || return 0
  if [ -L "$1" ]; then printf '%s' "$(readlink "$1" 2>/dev/null)" | _omk_sha | awk '{print $1}'
  else [ -r "$1" ] && _omk_sha < "$1" | awk '{print $1}'; fi   # unreadable -> empty -> no drift, no stderr leak
}
is_drifted() {  # $1 rel, $2 ledger-hash, $3 enabled -> 0 (true) if present & content-changed
  [ "$3" = "1" ] || return 1                                            # disabled: not managed, never "drifted"
  { [ -e "$ROOT/$1" ] || [ -L "$ROOT/$1" ]; } || return 1              # missing is its own state, not drift
  git -C "$ROOT" ls-files --error-unmatch "$1" >/dev/null 2>&1 && return 1   # tracked: upstream owns it
  local a; a="$(omakase_hash_of "$ROOT/$1")" || a=""
  [ -n "$2" ] && [ -n "$a" ] && [ "$a" != "$2" ]
}

# ============================ Inventory (spec §3) ============================
# Every harness artifact in this repo, grouped by origin: committed by the
# project, injected from a source (the provenance ledger), personal (~/.claude + ~/.copilot).
# No token counts — the host owns context-cost ground truth.

# kind_of() comes from lib-harness-paths.sh (sourced above) — shared with init.sh + import.sh.

# git-TRACKED harness artifacts: the project's own committed harness surface.
# A placed (injected) file is by definition untracked, so no path can appear
# in both the Committed and Injected groups.
committed_list() {
  # core.quotePath=false: git's default quotes non-ASCII paths, and a leading
  # quote would defeat the kind_of patterns and render the path escaped.
  git -C "$ROOT" -c core.quotePath=false ls-files -- \
    "${HARNESS_COMMITTED_GLOBS[@]}" 2>/dev/null || true
}

# Presence-only listing of the user's GLOBAL harness — agent config in $HOME that applies to
# every repo. Claude Code keeps it under ~/.claude; Copilot CLI keeps personal skills under
# ~/.copilot/skills (https://docs.github.com/en/copilot/how-tos/copilot-cli/customize-copilot/add-skills).
# Rows are root-qualified (~/.claude/…, ~/.copilot/…) so origin is unambiguous; never reads
# file contents. A skill directory is ONE row. Add a host = add its block here.
personal_list() {
  ch="${HOME:-}/.claude"
  if [ -d "$ch" ]; then
    [ -e "$ch/CLAUDE.md" ]     && printf '~/.claude/CLAUDE.md\t%s\n'     "$(kind_of CLAUDE.md)"
    [ -e "$ch/settings.json" ] && printf '~/.claude/settings.json\t%s\n' "$(kind_of .claude/settings.json)"
    for f in "$ch"/rules/*.md;    do [ -e "$f" ] || continue; b="${f##*/}"; printf '~/.claude/rules/%s\t%s\n'    "$b" "$(kind_of ".claude/rules/$b")"; done
    for f in "$ch"/commands/*.md; do [ -e "$f" ] || continue; b="${f##*/}"; printf '~/.claude/commands/%s\t%s\n' "$b" "$(kind_of ".claude/commands/$b")"; done
    for f in "$ch"/agents/*.md;   do [ -e "$f" ] || continue; b="${f##*/}"; printf '~/.claude/agents/%s\t%s\n'   "$b" "$(kind_of ".claude/agents/$b")"; done
    for d in "$ch"/skills/*/;     do [ -d "$d" ] || continue; d="${d%/}"; b="${d##*/}"; printf '~/.claude/skills/%s/\t%s\n' "$b" "$(kind_of ".claude/skills/$b/")"; done
  fi
  # Copilot CLI personal skills: ~/.copilot/skills/<name>/SKILL.md (classified like a .github skill).
  co="${HOME:-}/.copilot"
  if [ -d "$co" ]; then
    for d in "$co"/skills/*/; do [ -d "$d" ] || continue; d="${d%/}"; b="${d##*/}"; printf '~/.copilot/skills/%s/\t%s\n' "$b" "$(kind_of ".github/skills/$b/")"; done
  fi
  return 0
}

render_inventory() {
  comm="$(committed_list)"
  pers="$(personal_list)"
  if [ "$FORMAT" = md ]; then
    echo "### Inventory"
    echo
    echo "**Committed (this repo)** — tracked harness files"
    if [ -n "$comm" ]; then
      printf '%s\n' "$comm" | while IFS= read -r rel; do
        [ -z "$rel" ] && continue
        echo "- \`$rel\` — $(kind_of "$rel")"
      done
    else
      echo "- _(none)_"
    fi
    echo
    echo "**Injected (omakase)** — placed by \`/omakase init\`, gitignored"
    if [ -f "$PLACED" ] && [ -s "$PLACED" ]; then
      while IFS=$'\t' read -r rel kind src hash enabled; do
        [ -z "$rel" ] && continue
        dz=""; if is_drifted "$rel" "$hash" "$enabled"; then dz=" — **DRIFTED** (differs from canonical; \`/omakase init\` to re-sync, or it may be an intentional local edit)"; fi
        if [ "$enabled" = "0" ]; then
          echo "- \`$rel\` — $kind, from $src — disabled (not restored, not verified)"
        elif [ -L "$ROOT/$rel" ]; then
          echo "- \`$rel\` → \`$(readlink "$ROOT/$rel")\` — $kind, from $src$dz"
        elif [ -e "$ROOT/$rel" ]; then
          echo "- \`$rel\` — $kind, from $src$dz"
        else
          echo "- \`$rel\` — $kind, from $src — **MISSING** (run \`/omakase init\` to restore)"
        fi
      done < "$PLACED"
    else
      echo "- _(none)_"
    fi
    echo
    echo "**Personal (global)** — Claude ~/.claude + Copilot ~/.copilot, applies to every repo"
    if [ -n "$pers" ]; then
      printf '%s\n' "$pers" | while IFS=$'\t' read -r rel kind; do
        [ -z "$rel" ] && continue
        echo "- \`$rel\` — $kind"
      done
    else
      echo "- _(none)_"
    fi
  else
    echo "INVENTORY — every harness artifact in this repo, by origin"
    echo "  COMMITTED (this repo) — tracked harness files"
    if [ -n "$comm" ]; then
      printf '%s\n' "$comm" | while IFS= read -r rel; do
        [ -z "$rel" ] && continue
        echo "    + $rel   ($(kind_of "$rel"))"
      done
    else
      echo "    (none)"
    fi
    echo "  INJECTED (omakase) — placed by /omakase init, gitignored"
    if [ -f "$PLACED" ] && [ -s "$PLACED" ]; then
      while IFS=$'\t' read -r rel kind src hash enabled; do
        [ -z "$rel" ] && continue
        dz=""; mk="+"; if is_drifted "$rel" "$hash" "$enabled"; then dz="; DRIFTED — differs from canonical, run /omakase init to re-sync"; mk="~"; fi
        if [ "$enabled" = "0" ]; then
          echo "    - $rel   ($kind, from $src; disabled — not restored, not verified)"
        elif [ -L "$ROOT/$rel" ]; then
          echo "    $mk $rel -> $(readlink "$ROOT/$rel")   ($kind, from $src$dz)"
        elif [ -e "$ROOT/$rel" ]; then
          echo "    $mk $rel   ($kind, from $src$dz)"
        else
          echo "    ! $rel   ($kind, from $src; MISSING — run /omakase init to restore)"
        fi
      done < "$PLACED"
    else
      echo "    (none)"
    fi
    echo "  PERSONAL (global) — Claude ~/.claude + Copilot ~/.copilot, applies to every repo"
    if [ -n "$pers" ]; then
      printf '%s\n' "$pers" | while IFS=$'\t' read -r rel kind; do
        [ -z "$rel" ] && continue
        echo "    + $rel   ($kind)"
      done
    else
      echo "    (none)"
    fi
  fi
}

# ============================ Guards chart (the "run when" table) ============================
# ONE chart for every wired guard: which git hook fires it (RUN WHEN), the guard's
# canonical (ledgered) name, what it ENFORCES, and the most-recent verdict from the run
# ledger. Derived from `lefthook dump` (the normalized wiring) joined to ledger.tsv — this
# replaces the old raw-YAML "git hooks" dump + the separate "recent runs" table with a
# single readable chart. The cosmetic banner job is omitted. ENFORCES is a short built-in
# phrase keyed by the gate script's basename (falls back to the script path for custom/
# unknown gates, so the base harness and any consumer gate still render). If lefthook can't
# be resolved, degrades to the raw wiring file + the plain run ledger (render_guards_fallback).
render_guards() {
  local LH="" DUMP="" now RUNS_FILE
  if [ -n "${LEFTHOOK_BIN:-}" ]; then LH="$LEFTHOOK_BIN"
  elif command -v lefthook >/dev/null 2>&1; then LH="lefthook"
  elif [ -x "$ROOT/node_modules/.bin/lefthook" ]; then LH="$ROOT/node_modules/.bin/lefthook"; fi
  [ -n "$LH" ] && DUMP="$( cd "$ROOT" && "$LH" dump 2>/dev/null || true )"

  if [ -z "$DUMP" ]; then render_guards_fallback; return; fi

  now="${OMAKASE_NOW:-$(date +%s)}"
  RUNS_FILE="$RUNS"; [ -f "$RUNS_FILE" ] || RUNS_FILE=/dev/null
  # Pass 1 (FILENAME==runsfile): latest verdict+ts per gate. Pass 2 (the dump): walk
  # hook -> job -> run, emit one buffered row per non-cosmetic job, join the verdict by
  # the ledgered gate name. Buffer + END so the header prints only when rows exist and
  # term columns can be width-aligned (only the ASCII columns are padded; the verdict
  # cell carries the multibyte check/cross and is always last, so alignment stays exact).
  awk -v runsfile="$RUNS_FILE" -v now="$now" -v fmt="$FORMAT" '
    BEGIN { FS="\t"; wH=length("RUN WHEN"); wG=length("GUARD"); wE=length("ENFORCES") }
    FILENAME==runsfile {
      if (NF>=5 && $1 ~ /^[0-9]+$/) { ts=$1+0; if (ts>=seen[$3]) { seen[$3]=ts; verd[$3]=$4 } }
      next
    }
    /^[A-Za-z0-9_-]+:[[:space:]]*$/ { curhook=$0; sub(/:.*/,"",curhook); next }   # hook header (col 0)
    /^[[:space:]]*-[[:space:]]+name:[[:space:]]*/ {
      line=$0; sub(/^[[:space:]]*-[[:space:]]+name:[[:space:]]*/,"",line); jobname=line; haverun=0; next
    }
    /^[[:space:]]*run:[[:space:]]*/ {
      if (jobname=="" || haverun) next                      # only the first run: after a name:
      haverun=1
      line=$0; sub(/^[[:space:]]*run:[[:space:]]*/,"",line); runcmd=line
      if (jobname=="omakase-banner") { jobname=""; next }   # cosmetic header box, not a guard
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
      if (gate!="" && (gate in seen)) {
        d=now-seen[gate]; if (d<0) d=0
        if      (d<60)    a="<1m"
        else if (d<3600)  a=int(d/60)"m"
        else if (d<86400) a=int(d/3600)"h"
        else              a=int(d/86400)"d"
        vc=(verd[gate]=="fail" ? "\342\234\227 fail" : "\342\234\223 pass") " - " a " ago"
      } else if (ledgered) vc="- not yet run"
      else vc="\342\200\224"                                # em dash: not a pass/fail gate
      n++; H[n]=curhook; G[n]=gname; E[n]=enf; V[n]=vc
      if (length(curhook)>wH) wH=length(curhook)
      if (length(gname)>wG)   wG=length(gname)
      if (length(enf)>wE)     wE=length(enf)
      jobname=""
      next
    }
    END {
      if (fmt=="md") {
        if (n==0) { print "_(no guards wired)_"; }
        else {
          print "| Run when | Guard | Enforces | Last verdict |"
          print "| --- | --- | --- | --- |"
          for (i=1;i<=n;i++) printf "| `%s` | %s | %s | %s |\n", H[i], G[i], E[i], V[i]
        }
      } else {
        if (n==0) { print "  (no guards wired)"; }
        else {
          printf "  %-*s   %-*s   %-*s   %s\n", wH,"RUN WHEN", wG,"GUARD", wE,"ENFORCES", "LAST VERDICT"
          for (i=1;i<=n;i++) printf "  %-*s   %-*s   %-*s   %s\n", wH,H[i], wG,G[i], wE,E[i], V[i]
        }
      }
    }
  ' "$RUNS_FILE" <(printf '%s\n' "$DUMP")
}

# Degraded path for render_guards: lefthook couldn't be resolved, so we can't build the
# join. Fall back to exactly the pre-chart behavior — raw wiring file + plain run ledger.
render_guards_fallback() {
  local now
  if [ "$FORMAT" = md ]; then
    if [ -f "$ROOT/lefthook-local.yml" ]; then
      echo "_lefthook not resolved — raw wiring file:_"
      echo '```yaml'
      cat "$ROOT/lefthook-local.yml"
      echo '```'
    else
      echo "_(no hook wiring found)_"
    fi
    echo
    echo "**Recent runs**"
    echo
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
      echo "_No gate runs recorded yet._"
    fi
  else
    if [ -f "$ROOT/lefthook-local.yml" ]; then
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
            printf "%s\t  %s  %-4s  %s%s  (%s ago)\n", g, (verd[g]=="fail" ? "\342\234\227" : "\342\234\223"), verd[g], h, g, a
          }
        }' "$RUNS" | sort | cut -f2-
    else
      echo "  (no gate runs recorded yet)"
    fi
  fi
}

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
  # Not installed — say so, then still render the inventory: the audit view
  # (what does this repo feed your agent?) works on an uninstalled repo.
  if [ "$FORMAT" = md ]; then
    echo "**No omakase harness is installed in this repo.** Run \`/omakase init\` to inject one."
    echo
  else
    echo "No omakase harness is installed in this repo."
    echo "Run  /omakase init  to inject one."
    echo
  fi
  render_inventory
  exit 0
fi

# ============================ Markdown mode ============================
# The script emits the final Markdown; the /omakase command relays it verbatim.
if [ "$FORMAT" = md ]; then
  echo "## $ICON omakase-harness"
  echo
  echo "Installed in \`$ROOT\`. Injected files are gitignored via \`.git/info/exclude\` — invisible to git, never committed."
  echo
  render_inventory
  echo
  echo "### Guards — what runs, when, and the last verdict"
  echo
  render_guards
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
echo "(injected files are gitignored via .git/info/exclude: invisible to git, never committed)"
echo
render_inventory
echo

echo "GUARDS — what runs, when, and the last verdict"
render_guards
echo

echo "HIDDEN VIA .git/info/exclude"
if [ -f "$EXCLUDE" ]; then
  awk -v b="$BEGIN" -v e="$END" '$0==b{s=1;next} $0==e{s=0} s&&NF{print "  "$0}' "$EXCLUDE"
fi
echo
echo "Update to the latest harness (syncs files; removes dropped ones):   /omakase init"
echo "Undo everything:                                                    /omakase remove"
