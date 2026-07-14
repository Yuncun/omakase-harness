#!/usr/bin/env bash
# Proof that the shipped examples/starter-harness installs and works end-to-end.
# The example is the CONTENTS of a harness source, so the test does what an adopter does:
# copy it into a git repo, then `init --source` that repo. It checks:
#   - the overlay is placed and gitignored (both rules files, the gates, the wiring)
#   - the base machinery the wiring relies on is layered in (omakase-gate.sh)
#   - block-marker: a clean commit passes; a commit staging the scratch marker is BLOCKED
#   - go-checks: passes instantly with no staged .go; blocks a misformatted .go; passes
#     once formatted (gofmt + go vet) [skipped when no Go toolchain]
#   - go-test: the wired gate command passes and its --cacheable pass is reused [needs Go]
#   - remove tears it all down, exclude block included
# HOME and XDG_CACHE_HOME point at fixture dirs so nothing touches the real machine.
set -u
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
INIT="$HERE/../bin/init.sh"
REMOVE="$HERE/../bin/remove.sh"
STARTER="$(cd "$HERE/../examples/starter-harness" && pwd)"
LEFTHOOK="${LEFTHOOK_BIN:-$(command -v lefthook || true)}"
TMP="${TMPDIR:-/tmp}/omakase-starter-test.$$"
FAILED=0
pass(){ echo "  PASS: $1"; }
fail(){ echo "  FAIL: $1"; FAILED=1; }

export PATH="$(dirname "$LEFTHOOK"):$PATH"
FAKEHOME="$TMP/home"; CACHEHOME="$TMP/cache"
mkdir -p "$FAKEHOME" "$CACHEHOME"

# Pin Go's caches to their real locations so a shim-triggered build under the fake HOME
# doesn't strand a read-only module cache there (rm -rf noise at cleanup). Idiom shared
# with scorecard.test.sh.
if command -v go >/dev/null 2>&1; then
  export GOMODCACHE="$(go env GOMODCACHE)"
  export GOCACHE="$(go env GOCACHE)"
fi

newrepo(){ rm -rf "$1"; mkdir -p "$1"; ( cd "$1" && git init -q && git config user.email t@t && git config user.name t && git config commit.gpgsign false && git commit -q --allow-empty -m init ); }

echo "== starter-harness: copy into a repo, install via --source, gates fire =="

# 1) The example is the contents of a harness source — put it in a git repo (what an adopter does).
SRC="$TMP/starter-harness"
rm -rf "$SRC"; mkdir -p "$SRC"
cp -R "$STARTER/." "$SRC/"
( cd "$SRC" && git init -q && git config user.email t@t && git config user.name t && git config commit.gpgsign false && git add -A && git commit -q -m "starter harness" )
SRC="$(cd "$SRC" && pwd)"

# 2) Install into a fresh project.
REPO="$TMP/repo"; newrepo "$REPO"
( cd "$REPO" && HOME="$FAKEHOME" XDG_CACHE_HOME="$CACHEHOME" bash "$INIT" --source "$SRC" ) >/dev/null 2>&1 \
  && pass "init --source <starter> exits 0" || fail "init --source <starter> failed"

# 3) Overlay placed and gitignored.
[ -f "$REPO/.claude/rules/omakase-dev.md" ] && pass "Claude rules placed" || fail "Claude rules missing"
[ -f "$REPO/.github/instructions/omakase-dev.instructions.md" ] && pass "Copilot instructions placed" || fail "Copilot instructions missing"
[ -f "$REPO/.omakase/gates/block-marker.sh" ] && pass "block-marker gate placed" || fail "block-marker gate missing"
[ -f "$REPO/.omakase/gates/go-checks.sh" ] && pass "go-checks gate placed" || fail "go-checks gate missing"
[ -f "$REPO/lefthook-local.yml" ] && pass "wiring placed" || fail "wiring missing"
grep -q 'go-test' "$REPO/lefthook-local.yml" 2>/dev/null && pass "pre-push go-test wired" || fail "go-test not in wiring"
grep -q 'omakase-harness' "$REPO/.git/info/exclude" 2>/dev/null && pass "exclude block written" || fail "no exclude block"
( cd "$REPO" && git ls-files --error-unmatch .claude/rules/omakase-dev.md ) >/dev/null 2>&1 \
  && fail "rules file is tracked (must be gitignored)" || pass "rules file not tracked"

# 4) Base layering: machinery the starter does NOT ship is present from the base layer.
[ -f "$REPO/.omakase/bin/omakase-gate.sh" ] && pass "base machinery layered in (omakase-gate.sh)" || fail "base machinery missing"

# 5) A clean non-Go commit passes (block-marker ran; go-checks self-skips with no staged .go).
OUT=$(cd "$REPO" && echo clean > ok.txt && git add ok.txt && git commit -m ok 2>&1); rc=$?
[ "$rc" -eq 0 ] && pass "clean commit passes" || { fail "clean commit was blocked (rc=$rc)"; echo "$OUT" | sed 's/^/      /'; }
echo "$OUT" | grep -q 'go-checks skipped' && pass "go-checks self-skips with no staged .go" || fail "go-checks did not self-skip"

# 6) A commit staging the scratch marker is blocked. (Concatenated so this suite's own
#    source never contains the contiguous marker once the harness guards this repo.)
MARK="DO NOT ""COMMIT"
OUT=$(cd "$REPO" && printf '%s\n' "$MARK" > bad.txt && git add bad.txt && git commit -m bad 2>&1); rc=$?
[ "$rc" -ne 0 ] && pass "marker commit blocked (rc=$rc)" || { fail "marker commit was NOT blocked"; echo "$OUT" | sed 's/^/      /'; }
echo "$OUT" | grep -qi 'marker found\|BLOCKED' && pass "block message shown" || { fail "no block message"; echo "$OUT" | sed 's/^/      /'; }
( cd "$REPO" && git reset -q bad.txt && rm -f bad.txt )

# 7) Go gates, only where a toolchain exists (CI's main matrix has one; tests-no-go doesn't
#    run this suite).
if command -v go >/dev/null 2>&1; then
  ( cd "$REPO" && go mod init omakase-starter-fixture >/dev/null 2>&1 )
  printf 'package main\n\nfunc   main(){println("hi")}\n' > "$REPO/main.go"
  OUT=$(cd "$REPO" && git add go.mod main.go && git commit -m go 2>&1); rc=$?
  [ "$rc" -ne 0 ] && pass "misformatted .go commit blocked (rc=$rc)" || { fail "misformatted .go commit was NOT blocked"; echo "$OUT" | sed 's/^/      /'; }
  echo "$OUT" | grep -q 'gofmt wants to rewrite' && pass "gofmt message shown" || { fail "no gofmt message"; echo "$OUT" | sed 's/^/      /'; }

  ( cd "$REPO" && gofmt -w main.go && git add main.go )
  OUT=$(cd "$REPO" && git commit -m go 2>&1); rc=$?
  [ "$rc" -eq 0 ] && pass "formatted .go commit passes (gofmt + go vet)" || { fail "formatted .go commit blocked (rc=$rc)"; echo "$OUT" | sed 's/^/      /'; }

  # The wired pre-push command, invoked directly: first run executes go test, the second
  # reuses the --cacheable PASS for the same HEAD.
  GOTEST="bash .omakase/bin/omakase-gate.sh go-test --cacheable --glob '*.go go.mod go.sum' --step 'go test ./...'"
  OUT=$(cd "$REPO" && eval "$GOTEST" 2>&1); rc=$?
  [ "$rc" -eq 0 ] && pass "go-test gate passes" || { fail "go-test gate failed (rc=$rc)"; echo "$OUT" | sed 's/^/      /'; }
  OUT=$(cd "$REPO" && eval "$GOTEST" 2>&1); rc=$?
  { [ "$rc" -eq 0 ] && echo "$OUT" | grep -q 'cached'; } && pass "go-test pass reused (cached)" || { fail "go-test cache not reused"; echo "$OUT" | sed 's/^/      /'; }
else
  echo "  SKIP: no Go toolchain — go-checks/go-test scenarios not run"
fi

# 8) remove tears everything down.
( cd "$REPO" && HOME="$FAKEHOME" XDG_CACHE_HOME="$CACHEHOME" bash "$REMOVE" ) >/dev/null 2>&1
[ -f "$REPO/.claude/rules/omakase-dev.md" ] && fail "rules file survived remove" || pass "rules file removed"
[ -d "$REPO/.omakase" ] && fail ".omakase survived remove" || pass ".omakase removed"
grep -q 'omakase-harness' "$REPO/.git/info/exclude" 2>/dev/null && fail "exclude block survived remove" || pass "exclude block stripped"

rm -rf "$TMP"
[ "$FAILED" -eq 0 ] && echo "starter-harness.test.sh: ALL PASS" || { echo "starter-harness.test.sh: FAILURES"; exit 1; }
