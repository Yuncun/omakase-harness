#!/usr/bin/env bash
# Proof that the shipped harness installs and works end-to-end.
# The example is the CONTENTS of a harness source, so the test does what an adopter does:
# copy it into a git repo, then `init --source` that repo. It checks:
#   - the overlay is placed and gitignored (both rules files, the gates, the manifest)
#   - the base machinery the harness relies on is layered in (omakase-banner.sh)
#   - block-marker: a clean commit passes; a commit staging the scratch marker is BLOCKED
#   - go-checks: passes instantly with no staged .go; blocks a misformatted .go; passes
#     once formatted (gofmt + go vet) [skipped when no Go toolchain]
#   - go-test: the wired pre-push gate blocks a push whose tests fail and its --cacheable
#     PASS is reused at the same commit [needs Go]
#   - remove tears it all down, exclude block included
# HOME and XDG_CACHE_HOME point at fixture dirs so the commit-time dispatcher execs the
# freshly self-installed binary and nothing touches the real machine.
set -u
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
INIT="$HERE/../bin/init.sh"
REMOVE="$HERE/../bin/remove.sh"
STARTER="$(cd "$HERE/../harness" && pwd)"
TMP="${TMPDIR:-/tmp}/omakase-starter-test.$$"
FAILED=0
pass(){ echo "  PASS: $1"; }
fail(){ echo "  FAIL: $1"; FAILED=1; }

# Self-contained HOME + cache: init self-installs the resolved binary into
# $XDG_CACHE_HOME, and every commit/push below fires that same copy.
export HOME="$TMP/home"; export XDG_CACHE_HOME="$TMP/cache"
mkdir -p "$HOME" "$XDG_CACHE_HOME"

# Pin Go's caches to their real locations so a shim-triggered build under the fake HOME
# doesn't strand a read-only module cache there (rm -rf noise at cleanup).
if command -v go >/dev/null 2>&1; then
  export GOMODCACHE="$(go env GOMODCACHE)"
  export GOCACHE="$(go env GOCACHE)"
fi

newrepo(){ rm -rf "$1"; mkdir -p "$1"; ( cd "$1" && git init -q && git config user.email t@t && git config user.name t && git config commit.gpgsign false && git commit -q --allow-empty -m init && git branch -M main ); }

echo "== harness: copy into a repo, install via --source, gates fire =="

# 1) The example is the contents of a harness source — put it in a git repo (what an adopter does).
SRC="$TMP/starter-harness"
rm -rf "$SRC"; mkdir -p "$SRC"
cp -R "$STARTER/." "$SRC/"
( cd "$SRC" && git init -q && git config user.email t@t && git config user.name t && git config commit.gpgsign false && git add -A && git commit -q -m "starter harness" )
SRC="$(cd "$SRC" && pwd)"

# 2) Install into a fresh project.
REPO="$TMP/repo"; newrepo "$REPO"
( cd "$REPO" && bash "$INIT" --source "$SRC" ) >/dev/null 2>&1 \
  && pass "init --source <harness> exits 0" || fail "init --source <harness> failed"

# 3) Overlay placed and gitignored.
[ -f "$REPO/.claude/rules/omakase-dev.md" ] && pass "Claude rules placed" || fail "Claude rules missing"
[ -f "$REPO/.github/instructions/omakase-dev.instructions.md" ] && pass "Copilot instructions placed" || fail "Copilot instructions missing"
[ -f "$REPO/.omakase/gates/block-marker.sh" ] && pass "block-marker gate placed" || fail "block-marker gate missing"
[ -f "$REPO/.omakase/gates/go-checks.sh" ] && pass "go-checks gate placed" || fail "go-checks gate missing"
[ -f "$REPO/omakase.manifest" ] && pass "manifest placed" || fail "manifest missing"
grep -q 'go-test' "$REPO/omakase.manifest" 2>/dev/null && pass "pre-push go-test declared" || fail "go-test not in manifest"
grep -q 'omakase-harness' "$REPO/.git/info/exclude" 2>/dev/null && pass "exclude block written" || fail "no exclude block"
( cd "$REPO" && git ls-files --error-unmatch .claude/rules/omakase-dev.md ) >/dev/null 2>&1 \
  && fail "rules file is tracked (must be gitignored)" || pass "rules file not tracked"

# 4) Base layering: machinery the harness does NOT ship is present from the base layer.
[ -f "$REPO/.omakase/bin/omakase-banner.sh" ] && pass "base machinery layered in (omakase-banner.sh)" || fail "base machinery missing"

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

# 6b) The marker is caught in a non-ASCII filename too (git octal-quotes such names in
#     porcelain output; the gate reads them NUL-delimited so it must not fail open).
OUT=$(cd "$REPO" && printf '%s\n' "$MARK" > "café.txt" && git add "café.txt" && git commit -m bad 2>&1); rc=$?
[ "$rc" -ne 0 ] && pass "marker in non-ASCII filename blocked (rc=$rc)" || { fail "marker in non-ASCII filename NOT blocked"; echo "$OUT" | sed 's/^/      /'; }
( cd "$REPO" && git reset -q -- "café.txt" && rm -f "café.txt" )

# 6c) OMAKASE_SKIP_GATES=1 skips every gate once (audited); the marker commit goes through.
OUT=$(cd "$REPO" && printf '%s\n' "$MARK" > skip.txt && git add skip.txt && OMAKASE_SKIP_GATES=1 git commit -m skip 2>&1); rc=$?
[ "$rc" -eq 0 ] && pass "OMAKASE_SKIP_GATES=1 lets the marker commit through" || { fail "OMAKASE_SKIP_GATES did not skip (rc=$rc)"; echo "$OUT" | sed 's/^/      /'; }
( cd "$REPO" && git rm -q skip.txt && git commit -q -m "drop skip" )

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

  # The pre-push go-test gate, against a bare remote. newrepo put the repo on
  # `main` and the push below creates origin/main, so the gate resolves a base ref
  # and its glob (*.go go.mod go.sum) is exercised IN SCOPE, not via the unscoped
  # fallback. origin/main stays behind the commits below, so base...HEAD carries a
  # .go change and a re-fired gate at the same HEAD can actually reach the cache.
  OMK_BIN="$XDG_CACHE_HOME/omakase/bin/current/omakase"
  REMOTE="$TMP/remote.git"; rm -rf "$REMOTE"; git init -q --bare "$REMOTE"
  ( cd "$REPO" && git remote add origin "$REMOTE" 2>/dev/null; git push -q -u origin main 2>/dev/null )

  # A failing test blocks the push (gofmt-clean so the pre-commit go-checks gate
  # lets it through; the pre-push go-test gate is what must fail on it).
  printf 'package main\n\nimport "testing"\n\nfunc TestFails(t *testing.T) {\n\tt.Fatal("boom")\n}\n' > "$REPO/main_test.go"
  ( cd "$REPO" && git add main_test.go && git commit -q -m "failing test" )
  OUT=$(cd "$REPO" && git push origin HEAD 2>&1); rc=$?
  [ "$rc" -ne 0 ] && pass "push BLOCKED when go test fails" || { fail "push not blocked on failing test (rc=$rc)"; echo "$OUT" | sed 's/^/      /'; }

  # A passing test at a new HEAD — committed but NOT pushed, so origin/main still
  # trails it and the glob range stays non-empty.
  ( cd "$REPO" && printf 'package main\n\nimport "testing"\n\nfunc TestOK(t *testing.T) {}\n' > main_test.go && git add main_test.go && git commit -q -m "passing test" )

  # Fire the pre-push gate directly (a git push would advance origin/main and empty
  # the glob range): the first fire runs go test in scope and records the PASS.
  OUT=$(cd "$REPO" && printf '' | "$OMK_BIN" hook pre-push origin "$REMOTE" 2>&1); rc=$?
  [ "$rc" -eq 0 ] && pass "go-test gate ran in glob scope and passed (recorded)" || { fail "go-test gate failed on a passing test (rc=$rc)"; echo "$OUT" | sed 's/^/      /'; }

  # Second fire at the SAME HEAD MUST short-circuit on the cached PASS. This is a
  # HARD assertion (the old both-branches-pass check could never fail): if caching
  # regressed, the 'cached' note is absent and this fails.
  OUT=$(cd "$REPO" && printf '' | "$OMK_BIN" hook pre-push origin "$REMOTE" 2>&1); rc=$?
  { [ "$rc" -eq 0 ] && echo "$OUT" | grep -q 'cached'; } && pass "go-test PASS reused (cached) at the same HEAD" || { fail "cached PASS not reused on a re-fired pre-push (rc=$rc)"; echo "$OUT" | sed 's/^/      /'; }

  # The real push at that HEAD is allowed — the cached PASS short-circuits the gate.
  OUT=$(cd "$REPO" && git push origin HEAD 2>&1); rc=$?
  [ "$rc" -eq 0 ] && pass "push ALLOWED when go test passes (cached)" || { fail "push blocked on passing test (rc=$rc)"; echo "$OUT" | sed 's/^/      /'; }
else
  echo "  SKIP: no Go toolchain — go-checks/go-test scenarios not run"
fi

# 8) remove tears everything down.
( cd "$REPO" && bash "$REMOVE" ) >/dev/null 2>&1
[ -f "$REPO/.claude/rules/omakase-dev.md" ] && fail "rules file survived remove" || pass "rules file removed"
[ -d "$REPO/.omakase" ] && fail ".omakase survived remove" || pass ".omakase removed"
[ -f "$REPO/omakase.manifest" ] && fail "manifest survived remove" || pass "manifest removed"
grep -q 'omakase-harness' "$REPO/.git/info/exclude" 2>/dev/null && fail "exclude block survived remove" || pass "exclude block stripped"

rm -rf "$TMP"
[ "$FAILED" -eq 0 ] && echo "harness.test.sh: ALL PASS" || { echo "harness.test.sh: FAILURES"; exit 1; }
