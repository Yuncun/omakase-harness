#!/usr/bin/env bash
# Proof that the shipped examples/sample-harness installs and works end-to-end.
# The example is the CONTENTS of a harness repo, so the test does what an adopter does:
# copy it into a git repo, then `init --source` that repo. It checks:
#   - the overlay is placed and gitignored (CLAUDE.md, the gate, the wiring)
#   - the base harness machinery the wiring relies on is layered in (omakase-ledger.sh) —
#     this is the PR-#28 base+source merge, exercised through the real sample
#   - a clean commit passes; a commit staging a DO-NOT-COMMIT marker is BLOCKED
#   - remove tears it all down, exclude block included
# HOME and XDG_CACHE_HOME point at fixture dirs so nothing touches the real machine.
set -u
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
INIT="$HERE/../bin/init.sh"
REMOVE="$HERE/../bin/remove.sh"
SAMPLE="$(cd "$HERE/../examples/sample-harness" && pwd)"
LEFTHOOK="${LEFTHOOK_BIN:-$(command -v lefthook || true)}"
TMP="${TMPDIR:-/tmp}/omakase-sample-test.$$"
FAILED=0
pass(){ echo "  PASS: $1"; }
fail(){ echo "  FAIL: $1"; FAILED=1; }

export PATH="$(dirname "$LEFTHOOK"):$PATH"
FAKEHOME="$TMP/home"; CACHEHOME="$TMP/cache"
mkdir -p "$FAKEHOME" "$CACHEHOME"

newrepo(){ rm -rf "$1"; mkdir -p "$1"; ( cd "$1" && git init -q && git config user.email t@t && git config user.name t && git config commit.gpgsign false && git commit -q --allow-empty -m init ); }

echo "== sample-harness: copy into a repo, install via --source, gate fires =="

# 1) The example is the contents of a harness repo — put it in one (what an adopter does).
SRC="$TMP/sample-harness"
rm -rf "$SRC"; mkdir -p "$SRC"
cp -R "$SAMPLE/." "$SRC/"
( cd "$SRC" && git init -q && git config user.email t@t && git config user.name t && git config commit.gpgsign false && git add -A && git commit -q -m "sample harness" )
SRC="$(cd "$SRC" && pwd)"

# 2) Install into a fresh project.
REPO="$TMP/repo"; newrepo "$REPO"
( cd "$REPO" && HOME="$FAKEHOME" XDG_CACHE_HOME="$CACHEHOME" bash "$INIT" --source "$SRC" ) >/dev/null 2>&1 \
  && pass "init --source <sample> exits 0" || fail "init --source <sample> failed"

# 3) Overlay placed and gitignored.
[ -f "$REPO/CLAUDE.md" ] && pass "CLAUDE.md placed" || fail "CLAUDE.md missing"
[ -f "$REPO/.omakase/gates/block-marker.sh" ] && pass "gate script placed" || fail "gate script missing"
[ -f "$REPO/lefthook-local.yml" ] && pass "wiring placed" || fail "wiring missing"
grep -q 'omakase-harness' "$REPO/.git/info/exclude" 2>/dev/null && pass "exclude block written" || fail "no exclude block"
( cd "$REPO" && git ls-files --error-unmatch CLAUDE.md ) >/dev/null 2>&1 \
  && fail "CLAUDE.md is tracked (must be gitignored)" || pass "CLAUDE.md not tracked"

# 4) Base layering: machinery the sample does NOT ship is present from the base layer.
[ -f "$REPO/.omakase/bin/omakase-ledger.sh" ] && pass "base machinery layered in (omakase-ledger.sh)" || fail "base machinery missing"
[ -f "$REPO/.omakase/bin/omakase-banner.sh" ] && pass "base machinery layered in (omakase-banner.sh)" || fail "base banner missing"

# 5) A clean commit passes (and the gate ran).
OUT=$(cd "$REPO" && echo clean > ok.txt && git add ok.txt && git commit -m ok 2>&1); rc=$?
[ "$rc" -eq 0 ] && pass "clean commit passes" || { fail "clean commit was blocked (rc=$rc)"; echo "$OUT" | sed 's/^/      /'; }

# 6) A commit staging a DO-NOT-COMMIT marker is blocked.
MARK="DO NOT COMMIT"
OUT=$(cd "$REPO" && printf '%s\n' "$MARK" > bad.txt && git add bad.txt && git commit -m bad 2>&1); rc=$?
[ "$rc" -ne 0 ] && pass "marker commit blocked (rc=$rc)" || { fail "marker commit was NOT blocked"; echo "$OUT" | sed 's/^/      /'; }
echo "$OUT" | grep -qi 'marker found\|BLOCKED' && pass "block message shown" || { fail "no block message"; echo "$OUT" | sed 's/^/      /'; }

# 7) remove tears everything down.
( cd "$REPO" && HOME="$FAKEHOME" XDG_CACHE_HOME="$CACHEHOME" bash "$REMOVE" ) >/dev/null 2>&1
[ -f "$REPO/CLAUDE.md" ] && fail "CLAUDE.md survived remove" || pass "CLAUDE.md removed"
[ -d "$REPO/.omakase" ] && fail ".omakase survived remove" || pass ".omakase removed"
grep -q 'omakase-harness' "$REPO/.git/info/exclude" 2>/dev/null && fail "exclude block survived remove" || pass "exclude block stripped"

rm -rf "$TMP"
[ "$FAILED" -eq 0 ] && echo "sample-harness.test.sh: ALL PASS" || { echo "sample-harness.test.sh: FAILURES"; exit 1; }
