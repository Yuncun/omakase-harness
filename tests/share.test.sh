#!/usr/bin/env bash
# Proof that share.sh turns the CURRENT repo's harness into a publishable harness repo
# (the inverse of init) — sibling dir, captured payload, scaffolded manifest + README with
# the install line, a ready-to-push git repo, source left untouched — and that init's
# owner/repo[#ref] shorthand adopts a published harness with one line. Fully offline: a git
# `insteadOf` rewrite stands in for GitHub and a stub lefthook stands in for the binary.
set -u
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SHARE="$HERE/../bin/share.sh"
INIT="$HERE/../bin/init.sh"
TMP="${TMPDIR:-/tmp}/omakase-share-test.$$"
FAILED=0
pass(){ echo "  PASS: $1"; }
fail(){ echo "  FAIL: $1"; FAILED=1; }
trap 'rm -rf "$TMP"' EXIT
mkdir -p "$TMP"

# Isolated HOME (so the global git config below never touches the real one) + a private cache
# + a stub lefthook, so init needs no network.
export HOME="$TMP/home"; mkdir -p "$HOME"
export XDG_CACHE_HOME="$TMP/cache"
git config --global user.email t@t
git config --global user.name t
git config --global commit.gpgsign false
# Any unmapped github.com URL routes to a nonexistent local path -> clone fails instantly
# (no network, no hang). A more specific insteadOf added later wins by longest-match.
git config --global url."$TMP/nogithub/".insteadOf "https://github.com/"
printf '#!/usr/bin/env bash\nexit 0\n' > "$TMP/lefthook"; chmod +x "$TMP/lefthook"
export LEFTHOOK_BIN="$TMP/lefthook"

# Build a tuned project repo with a scattered harness at $1, origin owner $2.
mkproj(){
  local r="$1" owner="$2"; rm -rf "$r"; mkdir -p "$r"
  ( cd "$r" && git init -q && git config user.email t@t && git config user.name t && git config commit.gpgsign false )
  printf 'my doctrine\n' > "$r/AGENTS.md"
  mkdir -p "$r/.omakase/gates"
  printf '#!/usr/bin/env bash\nexit 0\n' > "$r/.omakase/gates/lint.sh"; chmod +x "$r/.omakase/gates/lint.sh"
  printf 'pre-commit:\n  jobs:\n    - name: lint\n      run: bash .omakase/gates/lint.sh\n' > "$r/lefthook-local.yml"
  printf '.omakase/\nlefthook-local.yml\n' >> "$r/.git/info/exclude"
  ( cd "$r" && git add AGENTS.md && git commit -q -m init && git remote add origin "https://github.com/$owner/$(basename "$r").git" )
}

echo "== Scenario SHARE: capture the current repo into a publishable harness repo =="
PROJ="$TMP/work/proj"; mkproj "$PROJ" acme
OUT=$( cd "$PROJ" && bash "$SHARE" 2>&1 ); rc=$?
DEST="$TMP/work/proj-harness"
[ "$rc" -eq 0 ] && pass "share exits 0" || { fail "share rc=$rc"; echo "$OUT" | sed 's/^/      /'; }
[ -d "$DEST" ] && pass "harness repo created as a sibling (../proj-harness)" || fail "no sibling harness repo"
[ -f "$DEST/payload/AGENTS.md" ] && pass "committed doctrine captured into payload/" || fail "AGENTS.md not captured"
[ -x "$DEST/payload/.omakase/gates/lint.sh" ] && pass "gitignored gate captured (executable)" || fail "gate not captured"
[ -f "$DEST/payload/lefthook-local.yml" ] && pass "hook wiring captured" || fail "wiring not captured"
grep -q '^name: proj-harness$' "$DEST/omakase.manifest" && pass "manifest written with name" || fail "manifest name wrong/missing"
grep -q 'omakase init acme/proj-harness' "$DEST/README.md" && pass "README carries the install line (owner from origin)" || fail "README install line wrong"
[ -d "$DEST/.git" ] && pass "harness repo git-initialized" || fail "not a git repo"
[ -n "$(git -C "$DEST" log --oneline 2>/dev/null)" ] && pass "initial commit made" || fail "no commit"
[ -z "$(cd "$PROJ" && git status --porcelain)" ] && pass "source repo left unchanged" || fail "share mutated the source"

echo "== Scenario NAME + GUARD: custom name; refuse a path or a non-empty destination =="
PROJ2="$TMP/work2/app"; mkproj "$PROJ2" bob
OUT=$( cd "$PROJ2" && bash "$SHARE" team-rig 2>&1 )
[ -d "$TMP/work2/team-rig" ] && pass "custom name -> ../team-rig" || fail "custom-name dest missing"
( cd "$PROJ2" && bash "$SHARE" team-rig ) >/dev/null 2>&1 && fail "did not refuse a non-empty existing dest" || pass "refuses a non-empty existing destination"
( cd "$PROJ2" && bash "$SHARE" "../escape" ) >/dev/null 2>&1 && fail "accepted a path as a name" || pass "rejects a name containing a path separator"

echo "== Scenario ROUNDTRIP: publish + adopt via the owner/repo shorthand (offline) =="
# 'publish' acme/proj-harness to the local harness repo from Scenario SHARE (longest-match wins).
git config --global url."$DEST".insteadOf "https://github.com/acme/proj-harness"
ADOPT="$TMP/adopter"; mkdir -p "$ADOPT"
( cd "$ADOPT" && git init -q && git commit -q --allow-empty -m init )
OUT=$( cd "$ADOPT" && bash "$INIT" acme/proj-harness 2>&1 ); rc=$?
[ "$rc" -eq 0 ] && pass "init <owner/repo> adopts the shared harness (exit 0)" || { fail "slug init rc=$rc"; echo "$OUT" | sed 's/^/      /'; }
[ -f "$ADOPT/AGENTS.md" ] && pass "shared harness delta (AGENTS.md) placed in the adopter" || fail "delta not placed"
[ -d "$ADOPT/.omakase" ] && pass "base machinery present (base+delta merge)" || fail "base machinery missing"
grep -q 'omakase-harness' "$ADOPT/.git/info/exclude" 2>/dev/null && pass "zero committed footprint (exclude block written)" || fail "no exclude block"
[ "$(head -n1 "$ADOPT/.git/omakase/source" 2>/dev/null)" = "https://github.com/acme/proj-harness" ] && pass "expanded source remembered (bare re-run refreshes it)" || fail "remembered source wrong"

echo "== Scenario SHORTHAND: expand a slug; leave an existing local path alone =="
S="$TMP/short"; mkdir -p "$S"; ( cd "$S" && git init -q && git commit -q --allow-empty -m init )
OUT=$( cd "$S" && bash "$INIT" zzz-nobody/zzz-nothing 2>&1 )
echo "$OUT" | grep -q 'https://github.com/zzz-nobody/zzz-nothing' && pass "owner/repo expands to a GitHub URL" || { fail "slug not expanded"; echo "$OUT" | sed 's/^/      /'; }
mkdir -p "$TMP/plainlocal"
OUT=$( cd "$S" && bash "$INIT" "$TMP/plainlocal" 2>&1 )
echo "$OUT" | grep -q 'github.com' && fail "an existing local path was github-expanded" || pass "an existing local path is not expanded"

echo ""
[ "$FAILED" -eq 0 ] && echo "share.test.sh: ALL PASS" || { echo "share.test.sh: FAILURES"; exit 1; }
