#!/usr/bin/env bash
# FROZEN v1 REFERENCE (moved from bin/status.sh by the v2 port): parity oracle for the Go binary + the shim's fallback body; kept byte-identical except this note and the lib source path, until Phase 7 retires it.
# omakase-harness status — render the installed (gitignored, invisible) harness as ONE
# readable map: the harness files grouped by origin (committed / injected / personal;
# omakase's own .omakase/ machinery is disclosed under Hidden, not listed), which git
# hooks run what, and what is hidden via .git/info/exclude. Read-only. This is the cure
# for "the install is invisible" — it lets you SEE the whole harness at a glance without
# committing anything.
#
# Two output modes:
#   (default)    terminal — ANSI banner box + indented columns, for a real terminal.
#   --markdown   Markdown — for `omakase status` to relay VERBATIM into the chat,
#                so the script owns the formatting and Claude never reformats (no drift,
#                no editorializing). Renders as a real heading/list/table in the reply.
set -euo pipefail

FORMAT=term
case "${1:-}" in --markdown|-m|md) FORMAT=md;; esac
ICON="${OMAKASE_ICON:-🥡}"

ROOT="$(git rev-parse --show-toplevel 2>/dev/null)" || { echo "omakase: not inside a git repo" >&2; exit 1; }
COMMON="$(cd "$ROOT" && cd "$(git rev-parse --git-common-dir)" && pwd)"
OMK="$COMMON/omakase"
RUNS="$OMK/ledger.tsv"      # gate run + cache store (omakase-gate.sh): epoch,name,verdict,sha
PLACED="$OMK/placed.tsv"    # provenance ledger (init.sh): path,kind,source,sha256,enabled

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
. "$SCRIPT_DIR/../lib-harness-paths.sh"   # kind_of() + committed-scan globs (shared with init/import)

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
  git -C "$ROOT" ls-files --error-unmatch -- "$1" >/dev/null 2>&1 && return 1   # tracked: upstream owns it
  local a; a="$(omakase_hash_of "$ROOT/$1")" || a=""
  [ -n "$2" ] && [ -n "$a" ] && [ "$a" != "$2" ]
}

# ============================ Inventory (spec §3) ============================
# The harness files grouped by origin: committed by the project, injected from a
# source (the provenance ledger; omakase's own .omakase/ machinery is disclosed under
# Hidden, not listed), personal (~/.claude + ~/.copilot).
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
    echo "### Committed (this repo) — tracked harness files"
    if [ -n "$comm" ]; then
      printf '%s\n' "$comm" | while IFS= read -r rel; do
        [ -z "$rel" ] && continue
        echo "- \`$rel\` — $(kind_of "$rel")"
      done
    else
      echo "- _(none)_"
    fi
    echo
    echo "### Injected (omakase) — placed by \`omakase init\`, gitignored"
    if [ -f "$PLACED" ] && [ -s "$PLACED" ]; then
      shown=0
      while IFS=$'\t' read -r rel kind src hash enabled; do
        [ -z "$rel" ] && continue
        case "$rel" in .omakase/*) continue;; esac   # omakase's own machinery/gate scripts live here; not listed (active gates show under Guards; .omakase/ is disclosed under Hidden)
        shown=1
        dz=""; if is_drifted "$rel" "$hash" "$enabled"; then dz=" — **DRIFTED** (differs from canonical; \`omakase init\` to re-sync, or it may be an intentional local edit)"; fi
        if [ "$enabled" = "0" ]; then
          echo "- \`$rel\` — $kind, from $src — disabled (not restored, not verified)"
        elif [ -L "$ROOT/$rel" ]; then
          echo "- \`$rel\` → \`$(readlink "$ROOT/$rel")\` — $kind, from $src$dz"
        elif [ -e "$ROOT/$rel" ]; then
          echo "- \`$rel\` — $kind, from $src$dz"
        else
          echo "- \`$rel\` — $kind, from $src — **MISSING** (run \`omakase init\` to restore)"
        fi
      done < "$PLACED"
      [ "$shown" = "0" ] && echo "- _(none)_"
    else
      echo "- _(none)_"
    fi
    echo
    echo "### Global — not installed by omakase (Claude ~/.claude + Copilot ~/.copilot, applies to every repo)"
    if [ -n "$pers" ]; then
      printf '%s\n' "$pers" | while IFS=$'\t' read -r rel kind; do
        [ -z "$rel" ] && continue
        echo "- \`$rel\` — $kind"
      done
    else
      echo "- _(none)_"
    fi
  else
    echo "COMMITTED (this repo) — tracked harness files"
    if [ -n "$comm" ]; then
      printf '%s\n' "$comm" | while IFS= read -r rel; do
        [ -z "$rel" ] && continue
        echo "    + $rel   ($(kind_of "$rel"))"
      done
    else
      echo "    (none)"
    fi
    echo "INJECTED (omakase) — placed by omakase init, gitignored"
    if [ -f "$PLACED" ] && [ -s "$PLACED" ]; then
      shown=0
      while IFS=$'\t' read -r rel kind src hash enabled; do
        [ -z "$rel" ] && continue
        case "$rel" in .omakase/*) continue;; esac   # omakase's own machinery/gate scripts live here; not listed (active gates show under Guards; .omakase/ is disclosed under Hidden)
        shown=1
        dz=""; mk="+"; if is_drifted "$rel" "$hash" "$enabled"; then dz="; DRIFTED — differs from canonical, run omakase init to re-sync"; mk="~"; fi
        if [ "$enabled" = "0" ]; then
          echo "    - $rel   ($kind, from $src; disabled — not restored, not verified)"
        elif [ -L "$ROOT/$rel" ]; then
          echo "    $mk $rel -> $(readlink "$ROOT/$rel")   ($kind, from $src$dz)"
        elif [ -e "$ROOT/$rel" ]; then
          echo "    $mk $rel   ($kind, from $src$dz)"
        else
          echo "    ! $rel   ($kind, from $src; MISSING — run omakase init to restore)"
        fi
      done < "$PLACED"
      [ "$shown" = "0" ] && echo "    (none)"
    else
      echo "    (none)"
    fi
    echo "GLOBAL — not installed by omakase (Claude ~/.claude + Copilot ~/.copilot, applies to every repo)"
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
# ledger. Derived from `lefthook dump` (the normalized wiring) joined to ledger.tsv. This
# replaces the old raw-YAML "git hooks" dump + the separate "recent runs" table with a
# single readable chart. The cosmetic banner job is omitted. ENFORCES describes a gate by
# its safe flags only (--cacheable, and --glob's value), never the quoted --step body. If
# lefthook can't be resolved no gates run, so the chart degrades to a one-line note.
render_guards() {
  local LH="" DUMP="" now RUNS_FILE
  if [ -n "${LEFTHOOK_BIN:-}" ]; then LH="$LEFTHOOK_BIN"
  elif command -v lefthook >/dev/null 2>&1; then LH="lefthook"
  elif [ -x "$ROOT/node_modules/.bin/lefthook" ]; then LH="$ROOT/node_modules/.bin/lefthook"; fi
  [ -n "$LH" ] && DUMP="$( cd "$ROOT" && "$LH" dump 2>/dev/null || true )"

  if [ -z "$DUMP" ]; then
    if [ "$FORMAT" = md ]; then echo "_lefthook not resolved - gates are not running._"
    else echo "  (lefthook not resolved - gates are not running)"; fi
    return
  fi

  now="${OMAKASE_NOW:-$(date +%s)}"
  RUNS_FILE="$RUNS"; [ -f "$RUNS_FILE" ] || RUNS_FILE=/dev/null
  # Pass 1 (FILENAME==runsfile): latest verdict+ts per gate. Pass 2 (the dump): walk
  # hook -> job -> run, emit one buffered row per non-cosmetic job, join the verdict by
  # the ledgered gate name. Buffer + END so the header prints only when rows exist and
  # term columns can be width-aligned (only the ASCII columns are padded; the verdict
  # cell carries the multibyte check/cross and is always last, so alignment stays exact).
  awk -v runsfile="$RUNS_FILE" -v now="$now" -v fmt="$FORMAT" '
    BEGIN { FS="\t"; wH=length("RUN WHEN"); wG=length("GUARD"); wE=length("ENFORCES") }
    function mdcell(s){ gsub(/\|/,"\\|",s); gsub(/\n/," ",s); return s }   # a literal | in a cell breaks the md table
    FILENAME==runsfile {
      if (NF>=4 && $1 ~ /^[0-9]+$/) { ts=$1+0; if (ts>=seen[$2]) { seen[$2]=ts; verd[$2]=$3 } }
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
      if (match(runcmd, /omakase-gate\.sh [A-Za-z0-9._-]+/)) {     # a gate -> its canonical name
        s=substr(runcmd,RSTART,RLENGTH); sub(/^omakase-gate\.sh /,"",s); gate=s; ledgered=1
      }
      if (runcmd ~ /ensure-present\.sh/) {
        enf="self-heal: restore any missing injected files"
      } else if (ledgered) {
        # Describe the gate by its SAFE flags only - never the quoted --step body (it can
        # carry spaces, quotes, ; and even a literal --record).
        cached=(runcmd ~ /--cacheable/)
        scope="runs every commit"
        if (match(runcmd, /--glob '"'"'[^'"'"']*'"'"'/)) {         # --glob 'PATS' (single-quoted)
          g=substr(runcmd,RSTART,RLENGTH); sub(/^--glob '"'"'/,"",g); sub(/'"'"'$/,"",g); scope="scope: "g
        }
        enf=(cached ? "cached; " : "") scope
      } else enf=runcmd
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
          for (i=1;i<=n;i++) printf "| `%s` | %s | %s | %s |\n", H[i], mdcell(G[i]), mdcell(E[i]), V[i]
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

if [ ! -f "$PLACED" ]; then
  # pre-0.10 installs recorded placements in placed.list; the harness IS installed —
  # never report a false negative about an enforcement system.
  if [ -f "$OMK/placed.list" ]; then
    if [ "$FORMAT" = md ]; then
      echo "**Pre-0.10 omakase install detected** (record: \`placed.list\`). Run \`omakase init\` to migrate to the provenance ledger. Placed files:"
      sed 's/^/- `/; s/$/`/' "$OMK/placed.list"
    else
      echo "Pre-0.10 omakase install detected (record: placed.list)."
      echo "Run  omakase init  to migrate to the provenance ledger. Placed files:"
      sed 's/^/  /' "$OMK/placed.list"
    fi
    exit 0
  fi
  # Not installed — say so, then still render the inventory: the audit view
  # (what does this repo feed your agent?) works on an uninstalled repo.
  if [ "$FORMAT" = md ]; then
    echo "**No omakase harness is installed in this repo.** Run \`omakase init\` to inject one."
    echo
  else
    echo "No omakase harness is installed in this repo."
    echo "Run  omakase init  to inject one."
    echo
  fi
  render_inventory
  exit 0
fi

# ============================ Identity (installed harness) ============================
# Lead with WHAT this repo tracks. Repo-local only: the remembered source ($OMK/source) gives
# the harness name + origin; the base machinery version is the injected .omakase/VERSION. The
# custom harness's OWN manifest version is NOT persisted here (it lives in the source cache),
# so it is intentionally not shown — surfacing it would need an init-time persist.
src=""; [ -s "$OMK/source" ] && src="$(head -n1 "$OMK/source" 2>/dev/null || true)"
hname="omakase-harness"
if [ -n "$src" ]; then n="${src%%#*}"; n="${n%.git}"; n="${n%/}"; hname="${n##*/}"; fi
srcdisp="$(printf '%s' "$src" | sed -e 's,^[a-z][a-z]*://,,' -e 's,/$,,')"
basever=""; [ -s "$ROOT/.omakase/VERSION" ] && basever="$(head -n1 "$ROOT/.omakase/VERSION" 2>/dev/null || true)"
# Injected count by consent state: enabled=0 rows (a file toggled off — deleted
# on disk, row kept) are the first-ever writers of a 0 in column 5, so they are
# reported separately rather than inflating the injected count. Kept in lockstep
# with internal/status/status.go (the parity gate proves both agree); an
# all-enabled ledger has no 0 rows, so the output is byte-identical to before.
ninjected=0; ntoggled=0
if [ -f "$PLACED" ]; then
  ninjected="$(awk -F'\t' '$1!=""&&$5!="0"{c++} END{print c+0}' "$PLACED" 2>/dev/null || echo 0)"
  ntoggled="$(awk -F'\t' '$1!=""&&$5=="0"{c++} END{print c+0}' "$PLACED" 2>/dev/null || echo 0)"
fi
toggled_suffix=""; [ "$ntoggled" -gt 0 ] && toggled_suffix=" ($ntoggled toggled off)"

# ============================ Markdown mode ============================
# The script emits the final Markdown; `omakase status` relays it verbatim. Order is
# question-first: identity (what is this) -> footprint (did it stay clean) -> Guards (what
# gates me) -> inventory (the files; global config last and clearly NOT installed by omakase).
if [ "$FORMAT" = md ]; then
  echo "## $ICON $hname"
  echo
  if [ -n "$srcdisp" ]; then echo "\`$srcdisp\` · base omakase ${basever:-?} · installed in \`$ROOT\`"
  else echo "base omakase ${basever:-?} · installed in \`$ROOT\`"; fi
  echo
  echo "**Zero footprint** — $ninjected file(s) injected$toggled_suffix, 0 committed; all gitignored via \`.git/info/exclude\` (invisible to git)."
  echo
  echo "### Guards — what runs when you commit / push"
  echo
  render_guards
  echo
  render_inventory
  echo
  echo "_Refresh:_ \`omakase init\`  ·  _Remove:_ \`omakase remove\`  ·  _read-only; running status changes nothing._"
  exit 0
fi

# ============================ Terminal mode (default) ============================
# Same question-first order as markdown: banner -> identity + footprint -> Guards -> inventory.
BANNER="$ROOT/.omakase/bin/omakase-banner.sh"
if [ -f "$BANNER" ]; then bash "$BANNER" 2>/dev/null || true; fi
if [ -n "$srcdisp" ]; then echo "$hname — $srcdisp · base omakase ${basever:-?} · installed in $ROOT"
else echo "installed in $ROOT (base omakase ${basever:-?})"; fi
echo "zero footprint: $ninjected injected$toggled_suffix, 0 committed, all gitignored (.git/info/exclude)"
echo

echo "GUARDS — what runs when you commit / push"
render_guards
echo

render_inventory
echo
echo "Update to the latest harness (syncs files; removes dropped ones):   omakase init"
echo "Undo everything:                                                    omakase remove"
