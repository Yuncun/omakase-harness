#!/usr/bin/env bash
# omakase-harness menu — the on/off switchboard for the installed harness.
# PROTOTYPE (branch proto/agentic-xray).
#
# Two toggle domains:
#   GATES — omakase-gate.sh names wired via lefthook. Disable appends the name to
#           .git/omakase/disabled-gates; the gate primitive checks that list and skips
#           VISIBLY (same audited-bypass contract as OMAKASE_SKIP_<NAME>, but persistent).
#   FILES — placed artifacts from the provenance ledger (path,kind,source,sha256,enabled).
#           Disable sets enabled=0 and deletes the placed copy — but ONLY if it still
#           matches what init placed (a local edit is never destroyed; reconcile first).
#           Enable sets enabled=1 and restores the file from the shared snapshot.
#           Disabled state survives re-init (init merges prior enabled values) and is
#           honored by ensure-present (no resurrection) and verify-overlay (no blocking).
#           omakase's own machinery (.omakase/*) is not listed: gates toggle by NAME above;
#           deleting a gate SCRIPT while its lefthook job remains would fail closed.
#
# usage: menu.sh                      interactive (needs a TTY)
#        menu.sh --list               print every toggle + state (agent/CI-friendly)
#        menu.sh --disable <path>   | --enable <path>
#        menu.sh --disable-gate <n> | --enable-gate <n>
set -euo pipefail

ROOT="$(git rev-parse --show-toplevel 2>/dev/null)" || { echo "omakase: not inside a git repo" >&2; exit 1; }
COMMON="$(cd "$ROOT" && cd "$(git rev-parse --git-common-dir)" && pwd)"
OMK="$COMMON/omakase"
LEDGER="$OMK/placed.tsv"
SNAP="$OMK/payload-snapshot"
DGATES="$OMK/disabled-gates"

[ -f "$LEDGER" ] || { echo "omakase: no harness installed here (no provenance ledger) — run omakase init first" >&2; exit 1; }

# sha256 — mirrors init.sh/ensure-present.sh exactly (symlink hashes its readlink target).
if command -v shasum >/dev/null 2>&1; then _omk_sha() { shasum -a 256; }
elif command -v sha256sum >/dev/null 2>&1; then _omk_sha() { sha256sum; }
else _omk_sha() { return 1; }; fi
hash_of() {
  command -v shasum >/dev/null 2>&1 || command -v sha256sum >/dev/null 2>&1 || return 0
  if [ -L "$1" ]; then printf '%s' "$(readlink "$1" 2>/dev/null)" | _omk_sha | awk '{print $1}'
  else [ -r "$1" ] && _omk_sha < "$1" | awk '{print $1}'; fi
}

# ---------------- gate inventory ----------------
# Gate names = every `omakase-gate.sh <name>` referenced from lefthook config in the
# worktree, plus anything already in the disabled list (so an orphaned entry can still
# be re-enabled). No lefthook binary needed — this reads the config files.
gate_names() {
  # Only non-comment `run:` lines count, and only the FIRST omakase-gate.sh match per
  # line — a step's own error text may mention the gate command (status.sh parses the
  # lefthook dump the same way).
  { for c in "$ROOT"/lefthook.yml "$ROOT"/lefthook.yaml "$ROOT"/lefthook-local.yml \
             "$ROOT"/.lefthook/*.yml "$ROOT"/.lefthook/*.yaml; do
      [ -f "$c" ] && cat "$c"   # BSD awk dies on a missing file arg — feed only real ones
    done | awk '/^[[:space:]]*#/ {next}
         /^[[:space:]]*run:/ && match($0, /omakase-gate\.sh +[A-Za-z0-9._-]+/) {
           s=substr($0,RSTART,RLENGTH); sub(/^omakase-gate\.sh +/,"",s); print s }'
    [ -f "$DGATES" ] && grep -v '^[[:space:]]*$' "$DGATES"
    true   # the probes above may test-fail; the block must stay 0 under pipefail
  } | sort -u
}
gate_disabled() { [ -f "$DGATES" ] && grep -Fxq -- "$1" "$DGATES"; }

enable_gate() {
  gate_disabled "$1" || { echo "omakase: gate '$1' is already enabled"; return 0; }
  grep -Fxv -- "$1" "$DGATES" > "$DGATES.tmp" || true
  mv "$DGATES.tmp" "$DGATES"
  [ -s "$DGATES" ] || rm -f "$DGATES"
  echo "omakase: gate '$1' ENABLED (runs on its hook again)"
}
disable_gate() {
  gate_names | grep -Fxq -- "$1" || { echo "omakase: unknown gate '$1' (known: $(gate_names | tr '\n' ' '))" >&2; return 1; }
  gate_disabled "$1" && { echo "omakase: gate '$1' is already disabled"; return 0; }
  printf '%s\n' "$1" >> "$DGATES"
  echo "omakase: gate '$1' DISABLED — omakase-gate.sh will skip it visibly (re-enable: omakase menu)"
}

# ---------------- file toggles ----------------
# Ledger rows minus omakase machinery. Emits: rel \t kind \t src \t hash \t enabled
file_rows() {
  while IFS=$'\t' read -r rel kind src hash enabled || [ -n "$rel" ]; do
    [ -z "$rel" ] && continue
    case "$rel" in .omakase/*) continue;; esac
    printf '%s\t%s\t%s\t%s\t%s\n' "$rel" "$kind" "$src" "$hash" "$enabled"
  done < "$LEDGER"
}

set_enabled() {  # $1 rel, $2 0|1 — rewrite the ledger row in place (temp + rename)
  awk -v p="$1" -v v="$2" 'BEGIN{FS=OFS="\t"} $1==p{$5=v} {print}' "$LEDGER" > "$LEDGER.tmp" \
    && mv "$LEDGER.tmp" "$LEDGER"
}

ledger_row() { awk -v p="$1" 'BEGIN{FS="\t"} $1==p{print; exit}' "$LEDGER"; }

disable_file() {
  row="$(ledger_row "$1")"
  [ -n "$row" ] || { echo "omakase: '$1' is not a placed artifact (see: omakase menu --list)" >&2; return 1; }
  enabled="$(printf '%s' "$row" | cut -f5)"; hash="$(printf '%s' "$row" | cut -f4)"
  case "$1" in .omakase/*) echo "omakase: '$1' is omakase machinery — disable gates by NAME instead (--disable-gate)" >&2; return 1;; esac
  [ "$enabled" = "0" ] && { echo "omakase: '$1' is already disabled"; return 0; }
  if git -C "$ROOT" ls-files --error-unmatch -- "$1" >/dev/null 2>&1; then
    echo "omakase: '$1' is TRACKED by the repo now — upstream owns it; nothing to disable here" >&2; return 1
  fi
  if [ -e "$ROOT/$1" ] || [ -L "$ROOT/$1" ]; then
    actual="$(hash_of "$ROOT/$1")" || actual=""
    if [ -n "$hash" ] && [ -n "$actual" ] && [ "$actual" != "$hash" ]; then
      echo "omakase: REFUSING to disable '$1' — it differs from what init placed (a local edit?). Reconcile first: omakase init to restore canonical, or delete it yourself." >&2
      return 1
    fi
    rm -f "$ROOT/$1"
    d="$(dirname "$1")"
    while [ "$d" != "." ] && [ -d "$ROOT/$d" ] && [ -z "$(ls -A "$ROOT/$d")" ]; do rmdir "$ROOT/$d"; d="$(dirname "$d")"; done
  fi
  set_enabled "$1" 0
  echo "omakase: '$1' DISABLED — removed from the worktree; won't self-heal back; survives re-init (re-enable: omakase menu)"
}

enable_file() {
  row="$(ledger_row "$1")"
  [ -n "$row" ] || { echo "omakase: '$1' is not a placed artifact (see: omakase menu --list)" >&2; return 1; }
  enabled="$(printf '%s' "$row" | cut -f5)"
  [ "$enabled" = "1" ] && { echo "omakase: '$1' is already enabled"; return 0; }
  set_enabled "$1" 1
  if [ ! -e "$ROOT/$1" ] && [ ! -L "$ROOT/$1" ]; then
    if [ -e "$SNAP/$1" ] || [ -L "$SNAP/$1" ]; then
      mkdir -p "$ROOT/$(dirname "$1")"
      cp -P "$SNAP/$1" "$ROOT/$1"
      case "$1" in *.sh) [ -L "$ROOT/$1" ] || chmod +x "$ROOT/$1";; esac
      echo "omakase: '$1' ENABLED — restored from the snapshot"
    else
      echo "omakase: '$1' ENABLED in the ledger, but the snapshot has no copy — run omakase init to restore it"
    fi
  else
    echo "omakase: '$1' ENABLED (a copy already exists in the worktree; left as-is)"
  fi
}

# ---------------- list / interactive ----------------
print_list() {  # numbered when $1=numbered; also fills MENU_KIND/MENU_KEY arrays
  i=0
  MENU_KIND=(); MENU_KEY=()
  gates="$(gate_names)"
  if [ -n "$gates" ]; then
    echo "GATES — run on commit/push (toggle by name)"
    while IFS= read -r g; do
      [ -n "$g" ] || continue
      i=$((i+1)); MENU_KIND[$i]=gate; MENU_KEY[$i]="$g"
      st=on; gate_disabled "$g" && st=off
      if [ "${1:-}" = numbered ]; then printf '  %2d) [%-3s] gate  %s\n' "$i" "$st" "$g"
      else printf '  %-3s  gate  %s\n' "$st" "$g"; fi
    done <<< "$gates"
  fi
  rows="$(file_rows)"
  if [ -n "$rows" ]; then
    echo "FILES — placed artifacts (toggle by path)"
    while IFS=$'\t' read -r rel kind src hash enabled; do
      [ -n "$rel" ] || continue
      i=$((i+1)); MENU_KIND[$i]=file; MENU_KEY[$i]="$rel"
      st=on; [ "$enabled" = "0" ] && st=off
      if [ "${1:-}" = numbered ]; then printf '  %2d) [%-3s] %-7s %s  (from %s)\n' "$i" "$st" "$kind" "$rel" "$src"
      else printf '  %-3s  %-7s %s  (from %s)\n' "$st" "$kind" "$rel" "$src"; fi
    done <<< "$rows"
  fi
  MENU_N=$i
  [ "$i" -gt 0 ] || echo "  (nothing to toggle — no gates wired, no placed files)"
}

interactive() {
  [ -t 0 ] || { echo "omakase: no TTY — use --list / --disable / --enable" >&2; exit 2; }
  while :; do
    echo
    echo "🥡 omakase menu — toggle a number, q to quit"
    print_list numbered
    [ "$MENU_N" -gt 0 ] || exit 0
    printf '> '
    IFS= read -r ans < /dev/tty || break
    case "$ans" in
      q|Q|'') break;;
      *[!0-9]*) echo "  (enter a number 1-$MENU_N, or q)";;
      *)
        if [ "$ans" -ge 1 ] && [ "$ans" -le "$MENU_N" ]; then
          k="${MENU_KIND[$ans]}"; key="${MENU_KEY[$ans]}"
          if [ "$k" = gate ]; then
            if gate_disabled "$key"; then enable_gate "$key"; else disable_gate "$key"; fi
          else
            en="$(ledger_row "$key" | cut -f5)"
            if [ "$en" = "0" ]; then enable_file "$key"; else disable_file "$key" || true; fi
          fi
        else echo "  (enter a number 1-$MENU_N, or q)"; fi;;
    esac
  done
}

case "${1:-}" in
  --list)         print_list;;
  --disable)      shift; [ $# -gt 0 ] || { echo "omakase: --disable needs a path" >&2; exit 2; }; disable_file "$1";;
  --enable)       shift; [ $# -gt 0 ] || { echo "omakase: --enable needs a path" >&2; exit 2; }; enable_file "$1";;
  --disable-gate) shift; [ $# -gt 0 ] || { echo "omakase: --disable-gate needs a name" >&2; exit 2; }; disable_gate "$1";;
  --enable-gate)  shift; [ $# -gt 0 ] || { echo "omakase: --enable-gate needs a name" >&2; exit 2; }; enable_gate "$1";;
  -h|--help)      sed -n '2,22p' "$0" | sed 's/^# \{0,1\}//';;
  "")             interactive;;
  *)              echo "omakase: unknown option '$1'" >&2; exit 2;;
esac
