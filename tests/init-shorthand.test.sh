#!/usr/bin/env bash
# Salvages the one end-to-end test of the primary install path that Task 1's
# removal of tests/share.test.sh left uncovered: `omakase init <owner/repo[#ref]>`
# expands the shorthand to a GitHub URL, clones it, optionally checks out a
# pinned #ref, and places the delta — the install path every adopter actually
# uses (share.sh/import.sh are gone; init's owner/repo shorthand is not).
# Fully offline: a git `insteadOf` rewrite stands in for github.com (the same
# proven pattern the deleted share.test.sh used); init fetches nothing but the
# omakase binary, which the shim resolves locally.
#   H1. init <owner/repo> expands, clones, places the delta, zero committed footprint
#   H2. init <owner/repo#tag> pins the checkout to the tag's content
set -u
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
INIT="$HERE/../bin/init.sh"
TMP="${TMPDIR:-/tmp}/omakase-init-shorthand-test.$$"
FAILED=0
pass(){ echo "  PASS: $1"; }
fail(){ echo "  FAIL: $1"; FAILED=1; }
trap 'rm -rf "$TMP"' EXIT
mkdir -p "$TMP"

# Isolated HOME (the git config --global calls below never touch the real
# one) + a private XDG cache, so init needs no network.
export HOME="$TMP/home"; mkdir -p "$HOME"
export XDG_CACHE_HOME="$TMP/cache"; mkdir -p "$XDG_CACHE_HOME"
git config --global user.email t@t
git config --global user.name t
git config --global commit.gpgsign false
# Any unmapped github.com URL routes to a nonexistent local path -> a clone
# fails instantly (no network, no hang). A more specific insteadOf added below
# wins by longest-match, so it stands in for one specific owner/repo.
git config --global url."$TMP/nogithub/".insteadOf "https://github.com/"

# Build a tiny publishable harness repo at $1: payload/omakase.manifest + one
# payload delta file (payload/AGENTS.md), committed.
mkharness(){
  local r="$1"; rm -rf "$r"; mkdir -p "$r/payload"
  printf 'name: shorthand-harness\nversion: 0.1.0\n' > "$r/payload/omakase.manifest"
  printf 'shorthand doctrine\n' > "$r/payload/AGENTS.md"
  ( cd "$r" && git init -q && git config user.email t@t && git config user.name t && git config commit.gpgsign false && git add -A && git commit -q -m harness )
}

newrepo(){ rm -rf "$1"; mkdir -p "$1"; ( cd "$1" && git init -q && git config user.email t@t && git config user.name t && git config commit.gpgsign false && git commit -q --allow-empty -m init ); }

echo "== Scenario H1: init <owner/repo> expands, clones, and places the delta =="
DEST="$TMP/pub/acme-widget-harness"
mkharness "$DEST"
git config --global url."$DEST".insteadOf "https://github.com/acme/widget-harness"
ADOPT="$TMP/adopter1"; newrepo "$ADOPT"
OUT=$( cd "$ADOPT" && bash "$INIT" acme/widget-harness 2>&1 ); rc=$?
[ "$rc" -eq 0 ] && pass "init <owner/repo> exits 0" || { fail "init rc=$rc"; echo "$OUT" | sed 's/^/      /'; }
[ "$(cat "$ADOPT/AGENTS.md" 2>/dev/null)" = "shorthand doctrine" ] && pass "shorthand delta (AGENTS.md) placed" || fail "delta not placed"
[ -z "$(cd "$ADOPT" && git status --porcelain)" ] && pass "git status clean (zero committed footprint)" || { fail "git status NOT clean"; (cd "$ADOPT" && git status --porcelain | sed 's/^/      /'); }

echo "== Scenario H2: init <owner/repo#tag> pins the checkout to the tag =="
# A distinctive file lives ONLY on a tag, never on the default branch, so a
# passing assertion proves the #ref actually drove a tag checkout.
DEFBRANCH="$(git -C "$DEST" symbolic-ref --short HEAD)"
git -C "$DEST" checkout -q -b _pinned
printf 'pinned delta\n' > "$DEST/payload/PINNED.md"
git -C "$DEST" add payload/PINNED.md && git -C "$DEST" commit -q -m pinned
git -C "$DEST" tag v1
git -C "$DEST" checkout -q "$DEFBRANCH"
git -C "$DEST" branch -q -D _pinned

PIN="$TMP/adopter2"; newrepo "$PIN"
OUT=$( cd "$PIN" && bash "$INIT" 'acme/widget-harness#v1' 2>&1 ); rc=$?
[ "$rc" -eq 0 ] && pass "init <owner/repo#tag> exits 0" || { fail "ref init rc=$rc"; echo "$OUT" | sed 's/^/      /'; }
[ -f "$PIN/PINNED.md" ] && pass "the tag's payload delta landed (checkout pinned to the tag)" || fail "tagged payload not placed"

echo ""
[ "$FAILED" -eq 0 ] && echo "init-shorthand.test.sh: ALL PASS" || { echo "init-shorthand.test.sh: FAILURES"; exit 1; }
