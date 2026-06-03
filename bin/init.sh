#!/usr/bin/env bash
# omakase-harness init — overlay payload/ into this repo additively, exclude every
# placed path via .git/info/exclude (zero committed footprint), install lefthook.
# Idempotent: re-running re-overlays and rewrites the exclude block.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PAYLOAD="${OMAKASE_PAYLOAD:-$(cd "$SCRIPT_DIR/../payload" && pwd)}"
ROOT="$(git rev-parse --show-toplevel 2>/dev/null)" || { echo "omakase: not inside a git repo" >&2; exit 1; }
[ -d "$PAYLOAD" ] || { echo "omakase: payload dir not found at $PAYLOAD" >&2; exit 1; }
command -v lefthook >/dev/null 2>&1 || { echo "omakase: lefthook not found on PATH — install it first (brew install lefthook, mise use lefthook, or add it as a devDependency)." >&2; exit 1; }

BEGIN="# >>> omakase-harness >>>"
END="# <<< omakase-harness <<<"
EXCLUDE="$ROOT/.git/info/exclude"

placed=(); skipped=()
while IFS= read -r -d '' f; do
  rel="${f#"$PAYLOAD"/}"
  if git -C "$ROOT" ls-files --error-unmatch "$rel" >/dev/null 2>&1; then
    skipped+=("$rel"); echo "omakase: SKIP (already tracked) $rel" >&2; continue
  fi
  mkdir -p "$ROOT/$(dirname "$rel")"
  cp "$f" "$ROOT/$rel"
  case "$rel" in *.sh) chmod +x "$ROOT/$rel";; esac
  placed+=("$rel")
done < <(find "$PAYLOAD" -type f -print0)

# Top-level prefixes for the exclude block (small + stable), plus lefthook's
# auto-created lefthook.yml if the repo does not track one.
prefixes=()
add_prefix(){ case " ${prefixes[*]:-} " in *" $1 "*) ;; *) prefixes+=("$1");; esac; }
for rel in "${placed[@]:-}"; do [ -n "$rel" ] && add_prefix "${rel%%/*}"; done
git -C "$ROOT" ls-files --error-unmatch lefthook.yml >/dev/null 2>&1 || add_prefix "lefthook.yml"

mkdir -p "$(dirname "$EXCLUDE")"; touch "$EXCLUDE"
# strip any prior block (portable, no sed -i)
awk -v b="$BEGIN" -v e="$END" '$0==b{s=1} !s{print} $0==e{s=0}' "$EXCLUDE" > "$EXCLUDE.tmp" && mv "$EXCLUDE.tmp" "$EXCLUDE"
{
  echo "$BEGIN"
  for p in "${prefixes[@]:-}"; do
    [ -z "$p" ] && continue
    if [ -d "$ROOT/$p" ]; then echo "$p/"; else echo "$p"; fi
  done
  echo "$END"
} >> "$EXCLUDE"

( cd "$ROOT" && lefthook install )

echo "omakase: placed ${#placed[@]} file(s), skipped ${#skipped[@]} tracked path(s)."
for p in "${placed[@]:-}"; do [ -n "$p" ] && echo "  + $p"; done
for s in "${skipped[@]:-}"; do [ -n "$s" ] && echo "  ~ skipped (tracked): $s"; done
echo "omakase: ignores -> .git/info/exclude; hooks installed. Nothing to commit."
