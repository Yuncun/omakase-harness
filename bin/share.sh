#!/usr/bin/env bash
# omakase-harness share — the inverse of init. init reads a harness's payload/ and overlays
# it onto a repo; share reads THIS repo's harness files and writes them into a NEW, publishable
# harness repo (payload/ + omakase.manifest + README), so others can adopt it with one line:
#   omakase init you/<name>
#
# It delegates the capture to import.sh (the deterministic "read a repo's scattered harness
# into payload/" engine) and adds the scaffolding + a ready-to-push git repo on top.
#
#   cd ~/my-project && bash <base-harness>/bin/share.sh            # -> ../my-project-harness
#   cd ~/my-project && bash <base-harness>/bin/share.sh team-rig   # -> ../team-rig
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# SOURCE repo to capture FROM = the current git repo.
ROOT="$(git rev-parse --show-toplevel 2>/dev/null)" || { echo "omakase share: not inside a git repo" >&2; exit 1; }
ROOT="$(cd "$ROOT" && pwd -P)"

# Name of the harness repo to create (a bare name, no path). Default: <reponame>-harness.
NAME="${1:-$(basename "$ROOT")-harness}"
case "$NAME" in
  */*|"")             echo "omakase share: name must be a bare directory name, not a path: '$NAME'" >&2; exit 2;;
  .|..|.git)          echo "omakase share: invalid name: '$NAME'" >&2; exit 2;;
esac

# DEST is a SIBLING of the repo — never inside it. import.sh refuses a payload that overlaps the
# source, and a harness nested in the project would get captured into itself / committed there.
DEST="$(dirname "$ROOT")/$NAME"
if [ -e "$DEST" ] && [ -n "$(ls -A "$DEST" 2>/dev/null)" ]; then
  echo "omakase share: destination already exists and is not empty: $DEST" >&2
  echo "  Remove it, or pass a different name: share <other-name>" >&2
  exit 1
fi
mkdir -p "$DEST/payload"
DEST="$(cd "$DEST" && pwd -P)"

echo "omakase share: capturing $ROOT -> $DEST/payload"
echo ""

# Capture this repo's harness into the new repo's payload/ (delegates to import.sh).
OMAKASE_PAYLOAD="$DEST/payload" bash "$SCRIPT_DIR/import.sh" "$ROOT"

# Scaffold the manifest if the capture didn't already carry one.
MANIFEST="$DEST/omakase.manifest"
[ -f "$MANIFEST" ] || printf 'name: %s\nversion: 0.1.0\n' "$NAME" > "$MANIFEST"

# Best-effort GitHub owner for the printed install line: the current repo's origin, else the
# user's github.user, else a placeholder the author fills in.
OWNER=""
# Raw configured URL (not `git remote get-url`, which expands url.*.insteadOf — a user with an
# SSH/mirror rewrite would otherwise yield the wrong owner).
URL="$(git -C "$ROOT" config --get remote.origin.url 2>/dev/null || true)"
case "$URL" in
  *github.com[:/]*) OWNER="$(printf '%s' "$URL" | sed -E 's#^.*github\.com[:/]+([^/]+)/.*$#\1#')";;
esac
[ -n "$OWNER" ] || OWNER="$(git config --get github.user 2>/dev/null || true)"
[ -n "$OWNER" ] || OWNER="<you>"

# Scaffold a README carrying the one-line install command (the README doubles as docs).
README="$DEST/README.md"
if [ ! -f "$README" ]; then
  cat > "$README" <<EOF
# $NAME

A personal [omakase](https://github.com/yuncun/omakase-harness) harness — agent
instructions, lint config, and git-hook gates, overlaid onto any repo with zero
committed footprint.

## Install

    omakase init $OWNER/$NAME

Adopters need omakase first: https://github.com/yuncun/omakase-harness
EOF
fi

# Make it a git repo so it is ready to publish.
if [ ! -d "$DEST/.git" ]; then
  git -C "$DEST" init -q
  git -C "$DEST" add -A
  git -C "$DEST" -c user.name='omakase' -c user.email='omakase@localhost' \
    commit -q -m "Initial harness: $NAME" >/dev/null 2>&1 || true
fi

# ---- next steps ----
echo ""
echo "omakase share: created harness repo -> $DEST"
echo "  scaffolded payload/, omakase.manifest, README.md; git initialized + committed."
if [ -z "$(ls -A "$DEST/payload" 2>/dev/null)" ]; then
  echo "  note: no harness files were found to capture — payload/ is empty. Add gates with"
  echo "        omakase add-gate, or drop rules/config under payload/, then re-run share."
fi
echo ""
echo "Publish it, then share the one-line install:"
echo "  cd \"$DEST\""
echo "  gh repo create $OWNER/$NAME --public --source . --push   # or push to any git host"
echo "  # others then run:  omakase init $OWNER/$NAME"
