#!/usr/bin/env bash
# omakase-banner — print a rounded, gray-gradient box header branded to the harness,
# in place of lefthook's own box (which is suppressed via `output:` in lefthook-local.yml).
# Usage: omakase-banner.sh [hook-name]   (hook omitted -> just name + version, e.g. for /omakase show)
# Icon is swappable: $OMAKASE_ICON (default 🍣). Version read from .omakase/VERSION if present.
# Honors NO_COLOR. Never fails a hook.
set -uo pipefail

icon="${OMAKASE_ICON:-🍣}"
name="omakase-harness"
hook="${1:-}"
ver=""
common="$(cd "$(git rev-parse --show-toplevel 2>/dev/null)" 2>/dev/null && pwd)" || common=""
[ -n "$common" ] && [ -f "$common/.omakase/VERSION" ] && ver="v$(tr -d ' \n' < "$common/.omakase/VERSION")"

label="$icon $name"
[ -n "$ver" ]  && label="$label $ver"
[ -n "$hook" ] && label="$label   ·   $hook"

# Fixed inner width keeps the box aligned regardless of which emoji is chosen.
W=54
use_color=1; [ -n "${NO_COLOR:-}" ] && use_color=0

# Emit one border row of $1 chars (len $W), gray gradient bright in the middle.
border() {
  local ch="$1" i g out=""
  for (( i=0; i<W; i++ )); do
    if [ "$use_color" -eq 1 ]; then
      g=$(( 30 + (90 * ( i < W/2 ? i : W-1-i )) / (W/2) ))
      out+=$'\033'"[38;2;${g};${g};${g}m${ch}"
    else
      out+="$ch"
    fi
  done
  [ "$use_color" -eq 1 ] && out+=$'\033'"[0m"
  printf '%s\n' "$out"
}

top="╭$(border ─)╮"; bot="╰$(border ─)╯"
# content row: pad label out to the inner width (W). Emoji are ~2 cells; trim one space.
pad=$(( W - ${#label} - 2 )); [ "$pad" -lt 0 ] && pad=0
spaces=""; for (( i=0; i<pad; i++ )); do spaces+=" "; done
if [ "$use_color" -eq 1 ]; then
  edge=$'\033'"[38;2;75;75;75m│"$'\033'"[0m"
  printf '%s\n%s %s%s %s\n%s\n' "$top" "$edge" "$label" "$spaces" "$edge" "$bot"
else
  printf '%s\n│ %s%s │\n%s\n' "$top" "$label" "$spaces" "$bot"
fi
