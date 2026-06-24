#!/usr/bin/env bash
# omakase build — assemble a self-contained Claude Code plugin bundle from the
# single source: this repo's machinery (bin/, commands/, skills/) + the base
# payload, optionally overlaid with a stack's payload delta.
#
# Every file in the output is a REAL file: symlinks in the source are dereferenced
# (cp -RL), and the build refuses to emit a bundle that still contains a symlink.
# This is the copy step that keeps ONE source of truth with zero version drift,
# without depending on any host's symlink-dereference behavior (Claude Code does
# dereference marketplace symlinks; Copilot CLI's behavior is unproven, so no
# installed artifact is allowed to rely on it).
#
# The bundle is assembled in a temp dir and moved into place atomically, so a
# failed build never leaves a partial bundle at --out.
#
# Internal release tooling. It lives in tools/ so it is never copied into a bundle,
# and it is not adopter-facing.
set -euo pipefail

usage() {
  cat <<'USAGE'
usage: build.sh --out <dir> [--stack <dir>]

Assemble a self-contained plugin bundle into <dir> (created fresh).

  --out <dir>     output bundle directory; WIPED and recreated.
  --stack <dir>   a stack source: a directory with a payload/ delta overlaid on the
                  base payload, and optionally a plugin.json (its name/description).
                  Omitted = the generic stack (base payload only).
  -h, --help      show this help.

Bundle layout (the installable plugin): bin/, commands/, skills/ (if any),
.claude-plugin/plugin.json, payload/. Every file is materialized real (no symlinks).
USAGE
}

OUT=""
STACK=""
while [ $# -gt 0 ]; do
  case "$1" in
    --out)   shift; [ $# -gt 0 ] || { echo "build: --out needs a dir" >&2; exit 2; }; OUT="$1";;
    --stack) shift; [ $# -gt 0 ] || { echo "build: --stack needs a dir" >&2; exit 2; }; STACK="$1";;
    -h|--help) usage; exit 0;;
    *) echo "build: unknown argument '$1'" >&2; usage >&2; exit 2;;
  esac
  shift
done
[ -n "$OUT" ] || { echo "build: --out is required" >&2; usage >&2; exit 2; }

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SRC="$(cd "$SCRIPT_DIR/.." && pwd)"   # the single source of truth (this repo)

[ -d "$SRC/bin" ] && [ -d "$SRC/payload" ] && [ -d "$SRC/commands" ] && [ -f "$SRC/.claude-plugin/plugin.json" ] \
  || { echo "build: source missing bin/, payload/, commands/, or .claude-plugin/plugin.json ($SRC)" >&2; exit 1; }
if [ -n "$STACK" ]; then
  [ -d "$STACK" ]         || { echo "build: --stack dir not found: $STACK" >&2; exit 1; }
  [ -d "$STACK/payload" ] || { echo "build: stack has no payload/: $STACK" >&2; exit 1; }
fi

# Assemble in a temp dir; move into place atomically at the end. The trap removes
# the temp on any failure, so --out is never left as a partial bundle.
WORK="${OUT%/}.build.$$"
trap 'rm -rf "$WORK"' EXIT
rm -rf "$WORK"
mkdir -p "$WORK/.claude-plugin"

# 1) Machinery — the one copy, dereferenced to real files.
cp -RL "$SRC/bin"      "$WORK/bin"
cp -RL "$SRC/commands" "$WORK/commands"
[ -d "$SRC/skills" ] && cp -RL "$SRC/skills" "$WORK/skills"

# 2) Base payload, then the stack's payload delta on top (stack wins).
cp -RL "$SRC/payload" "$WORK/payload"
[ -n "$STACK" ] && cp -RL "$STACK/payload/." "$WORK/payload/"

# Prune the junk .gitignore declares non-shippable: cp copies the working tree verbatim,
# so OS/editor cruft (.DS_Store on macOS, *.bak) would otherwise ride into the dist and
# then into every adopter repo. Keep the bundle to what the repo considers real source.
find "$WORK" \( -name .DS_Store -o -name '*.bak' \) -delete

# 3) plugin.json — the stack's own, else the base one.
if [ -n "$STACK" ] && [ -f "$STACK/plugin.json" ]; then
  cp -L "$STACK/plugin.json" "$WORK/.claude-plugin/plugin.json"
else
  cp -L "$SRC/.claude-plugin/plugin.json" "$WORK/.claude-plugin/plugin.json"
fi

# 4) Wiring guard — every .omakase script the merged hook wiring references must
#    exist in the bundle. Catches a stack whose lefthook-local.yml points at a
#    script it forgot to ship (the harness would otherwise fail silently on commit).
WIRING="$WORK/payload/lefthook-local.yml"
if [ -f "$WIRING" ]; then
  MISSING=""
  # Strip YAML '#' comments first (mirror bin/init.sh): a commented-out wiring breadcrumb
  # must not be treated as a live requirement.
  for ref in $(sed 's/#.*//' "$WIRING" | grep -oE '\.omakase/[A-Za-z0-9._/-]+\.sh' | sort -u); do
    [ -f "$WORK/payload/$ref" ] || MISSING="$MISSING $ref"
  done
  if [ -n "$MISSING" ]; then
    echo "build: FAILED — hook wiring references scripts not in the bundle:$MISSING" >&2
    exit 1
  fi
fi

# 5) Invariant — a bundle must contain ZERO symlinks. The whole design rests on the
#    installed artifact being real files, independent of any host's symlink handling.
#    (cp -RL already dereferences; this is a cheap backstop.)
if find "$WORK" -type l | grep -q .; then
  echo "build: FAILED — bundle contains symlinks (must be real files):" >&2
  find "$WORK" -type l >&2
  exit 1
fi

# Atomic publish into place.
rm -rf "$OUT"
mv "$WORK" "$OUT"

LABEL="generic"; [ -n "$STACK" ] && LABEL="$(basename "$STACK")"
FILES="$(find "$OUT" -type f | wc -l | tr -d ' ')"
echo "omakase build: stack '$LABEL' -> $OUT ($FILES files, 0 symlinks)"
