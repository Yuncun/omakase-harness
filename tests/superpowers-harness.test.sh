#!/usr/bin/env bash
# Proof that the shipped examples/superpowers-harness installs and works end-to-end.
# The example is the CONTENTS of a harness repo, so the test does what an adopter does:
# copy it into a git repo, then `init --source` that repo. It checks:
#   - the overlay places .claude/settings.json (Claude Code reads this; per-repo plugin
#     enablement is Claude-only — Copilot has no project-scoped settings file, #164 C2)
#     and gitignores it, and does NOT place the retired .github/copilot/settings.json
#   - the file registers the superpowers-marketplace and enables the plugin
#   - the base harness machinery is layered in (omakase-banner.sh)
#   - init prints the manifest's `recommends:` fallback line
#   - remove tears it all down, exclude block included
# HOME and XDG_CACHE_HOME point at fixture dirs so nothing touches the real machine.
set -u
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
INIT="$HERE/../bin/init.sh"
REMOVE="$HERE/../bin/remove.sh"
EXAMPLE="$(cd "$HERE/../examples/superpowers-harness" && pwd)"
TMP="${TMPDIR:-/tmp}/omakase-superpowers-test.$$"
FAILED=0
pass(){ echo "  PASS: $1"; }
fail(){ echo "  FAIL: $1"; FAILED=1; }

FAKEHOME="$TMP/home"; CACHEHOME="$TMP/cache"
mkdir -p "$FAKEHOME" "$CACHEHOME"

newrepo(){ rm -rf "$1"; mkdir -p "$1"; ( cd "$1" && git init -q && git config user.email t@t && git config user.name t && git config commit.gpgsign false && git commit -q --allow-empty -m init ); }

echo "== superpowers-harness: copy into a repo, install via --source, verify both tools =="

# 1) The example is the contents of a harness repo — put it in one (what an adopter does).
SRC="$TMP/superpowers-harness"
rm -rf "$SRC"; mkdir -p "$SRC"
cp -R "$EXAMPLE/." "$SRC/"
( cd "$SRC" && git init -q && git config user.email t@t && git config user.name t && git config commit.gpgsign false && git add -A && git commit -q -m "superpowers harness" )
SRC="$(cd "$SRC" && pwd)"

# 2) Install into a fresh project; capture output to check the recommends line.
REPO="$TMP/repo"; newrepo "$REPO"
OUT=$( cd "$REPO" && HOME="$FAKEHOME" XDG_CACHE_HOME="$CACHEHOME" bash "$INIT" --source "$SRC" 2>&1 ); rc=$?
[ "$rc" -eq 0 ] && pass "init --source <superpowers> exits 0" || { fail "init --source failed (rc=$rc)"; echo "$OUT" | sed 's/^/      /'; }

# 3) The Claude settings file placed and gitignored; the retired Copilot path absent.
CC="$REPO/.claude/settings.json"
[ -f "$CC" ] && pass ".claude/settings.json placed (Claude)" || fail ".claude/settings.json missing"
# Copilot has no project-scoped settings file; .github/copilot/ is not a Copilot path.
# An inert file here would silently promise Copilot users a plugin they don't get (#164 C2).
[ -f "$REPO/.github/copilot/settings.json" ] && fail "retired .github/copilot/settings.json reappeared" || pass "no inert Copilot settings file placed"
grep -q 'omakase-harness' "$REPO/.git/info/exclude" 2>/dev/null && pass "exclude block written" || fail "no exclude block"

# 4) The file registers the marketplace and enables the plugin.
grep -q 'obra/superpowers-marketplace' "$CC" 2>/dev/null && pass ".claude/settings.json: registers superpowers-marketplace" || fail ".claude/settings.json: marketplace missing"
grep -q 'superpowers@superpowers-marketplace' "$CC" 2>/dev/null && pass ".claude/settings.json: enables the plugin" || fail ".claude/settings.json: enabledPlugins entry missing"

# 5) Base layering: machinery the example does NOT ship is present from the base layer.
[ -f "$REPO/.omakase/bin/omakase-banner.sh" ] && pass "base machinery layered in (omakase-banner.sh)" || fail "base machinery missing"

# 6) init surfaced the manifest's recommends fallback line.
echo "$OUT" | grep -q 'this harness recommends' && pass "recommends line printed" || { fail "no recommends line"; echo "$OUT" | sed 's/^/      /'; }
echo "$OUT" | grep -q 'plugin install superpowers@superpowers-marketplace' && pass "recommends names the install command" || fail "recommends missing install command"

# 7) remove tears everything down.
( cd "$REPO" && HOME="$FAKEHOME" XDG_CACHE_HOME="$CACHEHOME" bash "$REMOVE" ) >/dev/null 2>&1
[ -f "$CC" ] && fail ".claude/settings.json survived remove" || pass ".claude/settings.json removed"
[ -d "$REPO/.omakase" ] && fail ".omakase survived remove" || pass ".omakase removed"
grep -q 'omakase-harness' "$REPO/.git/info/exclude" 2>/dev/null && fail "exclude block survived remove" || pass "exclude block stripped"

rm -rf "$TMP"
[ "$FAILED" -eq 0 ] && echo "superpowers-harness.test.sh: ALL PASS" || { echo "superpowers-harness.test.sh: FAILURES"; exit 1; }
