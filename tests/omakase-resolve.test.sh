#!/usr/bin/env bash
# Behavioral spec for omakase binary resolution (issue #182: the network fetch
# tier is gone — resolution is local-only, and a plugin-only install refuses
# with the brew line instead of downloading a binary):
#   R1. OMAKASE_BIN override: an executable value wins outright; a
#       non-executable value fails resolution with NO fallthrough.
#   R2. Nothing resolvable (simulated plugin clone: bin/+payload/, no go.mod,
#       no dist/, clean PATH, empty cache): init.sh / status.sh / remove.sh
#       fail closed — exit 1, the one-line brew instruction on stderr, stdout
#       untouched, and nothing appears in the cache (no download is ever
#       attempted).
#   R3. The stable machine copy (<cache>/omakase/bin/current/omakase — what
#       `omakase init` self-installs and the .git/hooks dispatchers exec)
#       resolves at tier 5: shims exec it with the right verb, offline.
#   R4. Tier 2's go build failure aborts the shim (exit nonzero, stale
#       dist/omakase never runs) instead of falling through under the
#       if-condition's set -e suppression; a succeeding build still execs.
#   R5. A cache-resident release-shaped binary drives init end to end: shim ->
#       stable copy -> init --source offline (issue #70), the standalone
#       binary with no shim inits via its embedded base payload (#168), and
#       bare init with nothing remembered places nothing (#123 item 1).
# HOME and XDG_CACHE_HOME point at fixture dirs so nothing touches the real machine.
set -u
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
LIB="$HERE/../bin/lib-omakase-bin.sh"
TMP="${TMPDIR:-/tmp}/omakase-bin-resolve-test.$$"
FAILED=0
pass(){ echo "  PASS: $1"; }
fail(){ echo "  FAIL: $1"; FAILED=1; }

mkdir -p "$TMP"

# A minimal PATH with no omakase/go on it, so subshells that must exercise a
# specific tier are not short-circuited by whatever the suite/CI has on PATH.
CLEANPATH="/usr/bin:/bin:/usr/sbin:/sbin"

# A bin/ dir with no go.mod / dist/omakase nearby, so tiers 2-3 never fire.
FAKEBIN="$TMP/norepo/bin"; mkdir -p "$FAKEBIN"

# A scratch git repo to run the shims from (init/status/remove expect a repo).
scratch_repo(){  # $1 = dir to create
  mkdir -p "$1"
  ( cd "$1" && git init -q && git config user.email t@t && git config user.name t && git config commit.gpgsign false && git commit -q --allow-empty -m init )
}

# ---------- Scenario R1: OMAKASE_BIN override ----------
echo "== Scenario R1: OMAKASE_BIN override wins when executable, fails hard when not =="
STUB1="$TMP/stub1"; printf '#!/bin/sh\necho stub-one "$@"\n' > "$STUB1"; chmod +x "$STUB1"
OUT="$( env -i PATH="$CLEANPATH" OMAKASE_BIN="$STUB1" bash -c '
    HERE="'"$FAKEBIN"'"
    . "'"$LIB"'"
    if resolve_omakase; then echo "RESOLVED:$OMAKASE_BIN_RESOLVED"; else echo FAILED; fi' 2>&1 )"
[ "$OUT" = "RESOLVED:$STUB1" ] && pass "an executable OMAKASE_BIN resolves verbatim" || fail "override not used ($OUT)"
OUT="$( env -i PATH="$CLEANPATH" OMAKASE_BIN="$TMP/nonexistent" bash -c '
    HERE="'"$FAKEBIN"'"
    . "'"$LIB"'"
    if resolve_omakase; then echo "RESOLVED:$OMAKASE_BIN_RESOLVED"; else echo FAILED; fi' 2>&1 )"
[ "$OUT" = "FAILED" ] && pass "a non-executable OMAKASE_BIN fails with no fallthrough" || fail "invalid override fell through ($OUT)"

# ---------- Simulated plugin clone for R2/R3/R5: bin/+payload/, no go.mod, no dist/ ----------
echo "== Building a simulated plugin clone (bin/ + payload/, no go.mod, no dist/) =="
CLONE="$TMP/clone"; mkdir -p "$CLONE"
cp -R "$HERE/../bin" "$CLONE/bin"
cp -R "$HERE/../payload" "$CLONE/payload"
[ ! -e "$CLONE/go.mod" ] && [ ! -e "$CLONE/dist" ] && pass "clone has no go.mod/dist (tiers 2-3 are structurally unreachable)" || fail "clone contaminated with go.mod/dist"

# ---------- Scenario R2: nothing resolvable -> the one-line brew refusal ----------
echo "== Scenario R2: plugin-only shape refuses with the brew line — no download is attempted =="
REPO2="$TMP/repo-r2"; scratch_repo "$REPO2"
R2HOME="$TMP/home-r2"; R2CACHE="$TMP/cache-r2"; mkdir -p "$R2HOME" "$R2CACHE"
for verb in status init remove; do
  R2OUT="$TMP/r2-$verb.out"; R2ERR="$TMP/r2-$verb.err"
  ( cd "$REPO2" && env -i HOME="$R2HOME" XDG_CACHE_HOME="$R2CACHE" PATH="$CLEANPATH" \
    bash "$CLONE/bin/$verb.sh" >"$R2OUT" 2>"$R2ERR" )
  rc=$?
  [ "$rc" -eq 1 ] && pass "$verb.sh exits 1 when nothing resolves" || fail "$verb.sh exited $rc, expected 1"
  grep -q "omakase: $verb needs the omakase binary — install it with: brew install yuncun/tap/omakase" "$R2ERR" \
    && pass "$verb.sh prints the one-line brew instruction" || fail "$verb.sh guidance wrong ($(cat "$R2ERR"))"
  [ ! -s "$R2OUT" ] && pass "$verb.sh stdout stays empty on the fail-closed path" || fail "$verb.sh wrote to stdout: $(cat "$R2OUT")"
done
[ -z "$(find "$R2CACHE" -type f 2>/dev/null)" ] && pass "cache stayed empty (no download was ever attempted)" || fail "something appeared in the cache: $(find "$R2CACHE" -type f)"

# ---------- Scenario R3: the stable machine copy resolves at tier 5 ----------
echo "== Scenario R3: the stable machine copy (bin/current) resolves offline =="
REPO3="$TMP/repo-r3"; scratch_repo "$REPO3"
R3HOME="$TMP/home-r3"; R3CACHE="$TMP/cache-r3"; mkdir -p "$R3HOME"
STABLE3="$R3CACHE/omakase/bin/current"; mkdir -p "$STABLE3"
printf '#!/bin/sh\necho fixture-omakase "$@"\n' > "$STABLE3/omakase"; chmod +x "$STABLE3/omakase"
OUT="$( cd "$REPO3" && env -i HOME="$R3HOME" XDG_CACHE_HOME="$R3CACHE" PATH="$CLEANPATH" \
  bash "$CLONE/bin/status.sh" 2>&1 )"
echo "$OUT" | grep -q 'fixture-omakase status' && pass "status.sh execs the stable machine copy" || fail "stable copy not used for status ($OUT)"
OUT="$( cd "$REPO3" && env -i HOME="$R3HOME" XDG_CACHE_HOME="$R3CACHE" PATH="$CLEANPATH" \
  bash "$CLONE/bin/remove.sh" 2>&1 )"
echo "$OUT" | grep -q 'fixture-omakase remove' && pass "remove.sh execs the stable machine copy" || fail "stable copy not used for remove ($OUT)"

# ---------- Scenario R4: tier 2's go build failure aborts the shim ----------
echo "== Scenario R4: a FAILING go build at tier 2 aborts the shim instead of exec'ing a stale dist/omakase =="
# resolve_omakase is always called as `if resolve_omakase; then ...` from every
# shim, and bash suppresses `set -e` throughout an if-condition's call chain —
# so a plain failing `go build` would not abort under the caller's set -e and
# could silently fall through to whatever dist/omakase happens to sit on disk.
# This pins the fix (an explicit `exit 1` on the build subshell, immune to that
# suppression) against exactly that regression.
DEVREPO="$TMP/devrepo"; mkdir -p "$DEVREPO/dist"
cp -R "$HERE/../bin" "$DEVREPO/bin"
echo "module fake" > "$DEVREPO/go.mod"
printf '#!/bin/sh\necho STALE-BINARY-RAN "$@"\n' > "$DEVREPO/dist/omakase"; chmod +x "$DEVREPO/dist/omakase"
REPO4="$TMP/repo-r4"; scratch_repo "$REPO4"

# (a) a failing `go` must abort the shim non-zero, never running the stale binary.
FAILGO="$TMP/fakebin-r4-fail"; mkdir -p "$FAILGO"
printf '#!/bin/sh\necho "fake go: build failed" >&2\nexit 1\n' > "$FAILGO/go"; chmod +x "$FAILGO/go"
R4AHOME="$TMP/home-r4a"; R4ACACHE="$TMP/cache-r4a"; mkdir -p "$R4AHOME" "$R4ACACHE"
OUTFILE="$TMP/r4a.out"; ERRFILE="$TMP/r4a.err"
( cd "$REPO4" && env -i HOME="$R4AHOME" XDG_CACHE_HOME="$R4ACACHE" PATH="$FAILGO:$CLEANPATH" \
  bash "$DEVREPO/bin/status.sh" >"$OUTFILE" 2>"$ERRFILE" )
rc=$?
[ "$rc" -ne 0 ] && pass "a failing go build aborts the shim (exit $rc)" || fail "shim exited 0 despite a failing go build"
grep -q 'STALE-BINARY-RAN' "$OUTFILE" && fail "stale dist/omakase ran despite the failing build ($(cat "$OUTFILE"))" || pass "stale dist/omakase never ran"
grep -q 'fake go: build failed' "$ERRFILE" && pass "the go build's own failure reached stderr" || fail "go build failure missing from stderr ($(cat "$ERRFILE"))"

# (b) inverse: a SUCCEEDING go (simulated by rewriting dist/omakase in place,
# standing in for a real rebuild) still lets the shim exec dist/omakase —
# proving the fix didn't turn tier 2 into an unconditional abort.
OKGO="$TMP/fakebin-r4-ok"; mkdir -p "$OKGO"
cat > "$OKGO/go" <<GOEOF
#!/bin/sh
printf '#!/bin/sh\necho STALE-BINARY-RAN "\$@"\n' > "$DEVREPO/dist/omakase"
chmod +x "$DEVREPO/dist/omakase"
exit 0
GOEOF
chmod +x "$OKGO/go"
R4BHOME="$TMP/home-r4b"; R4BCACHE="$TMP/cache-r4b"; mkdir -p "$R4BHOME" "$R4BCACHE"
OUTFILE2="$TMP/r4b.out"
( cd "$REPO4" && env -i HOME="$R4BHOME" XDG_CACHE_HOME="$R4BCACHE" PATH="$OKGO:$CLEANPATH" \
  bash "$DEVREPO/bin/status.sh" >"$OUTFILE2" 2>&1 )
rc2=$?
[ "$rc2" -eq 0 ] && grep -q 'STALE-BINARY-RAN status' "$OUTFILE2" && pass "a succeeding go build still lets the shim exec dist/omakase" || fail "shim did not run dist/omakase after a succeeding build (rc=$rc2, out=$(cat "$OUTFILE2"))"

# ---------- Scenario R5: a cache-resident real binary drives init end-to-end ----------
echo "== Scenario R5: a stable-copy real binary drives init end-to-end (issues #70/#168/#123) =="
# The standalone shapes: the binary sits ALONE in the machine cache (brew /
# tarball / self-installed copy — no payload/ sibling), so the --source merge
# base is NOT discoverable binary-relative and the binary falls back to its
# EMBEDDED base payload (#168). No network: a real binary drives a LOCAL
# source clone. Needs go to build the binary; skipped otherwise (the go CI
# jobs run it).
R5="$TMP/r5"; mkdir -p "$R5"
if command -v go >/dev/null 2>&1; then
  if ( cd "$HERE/.." && go build -o "$R5/omakase-built" ./cmd/omakase ) 2>"$R5/build.err"; then
    # Fake cache holding ONLY the binary at the stable path — no payload/ sibling.
    XDG="$R5/xdg"
    R5STABLE="$XDG/omakase/bin/current"; mkdir -p "$R5STABLE"
    cp "$R5/omakase-built" "$R5STABLE/omakase"; chmod +x "$R5STABLE/omakase"
    R5BIN="$R5STABLE/omakase"
    R5HOME="$R5/home"; mkdir -p "$R5HOME"

    # Local fixture source repo: payload/omakase.manifest + one marker file.
    R5SRC="$R5/src"; mkdir -p "$R5SRC/payload/.omakase"
    ( cd "$R5SRC" && git init -q && git config user.email t@t && git config user.name t && git config commit.gpgsign false )
    printf 'r5-source-marker\n' > "$R5SRC/payload/.omakase/R5-SOURCE-MARKER"
    printf 'name: r5-fixture\nversion: 0.1.0\n' > "$R5SRC/payload/omakase.manifest"
    ( cd "$R5SRC" && git add -A && git commit -q -m fixture )
    R5SRC="$(cd "$R5SRC" && pwd)"   # absolutize (macOS TMPDIR trails a slash), as init does

    # ---- leg 1: shim -> stable copy -> init --source, offline ----
    R5TGT="$R5/target"; scratch_repo "$R5TGT"
    R5OUT="$R5/probe.out"; R5ERR="$R5/probe.err"
    ( cd "$R5TGT" && env -i PATH="$CLEANPATH" HOME="$R5HOME" XDG_CACHE_HOME="$XDG" \
      bash "$CLONE/bin/init.sh" --source "$R5SRC" >"$R5OUT" 2>"$R5ERR" )
    rc=$?
    [ "$rc" -eq 0 ] && pass "shim -> stable copy -> init --source exits 0" || fail "probe exited $rc ($(cat "$R5ERR"))"
    grep -q 'cached at' "$R5OUT" && pass "the source was cached + injected (the full --source flow ran)" || fail "no 'cached at' in probe stdout ($(cat "$R5OUT"))"
    [ -f "$R5TGT/.omakase/VERSION" ] && pass "base payload file placed (the merge base was located)" || fail "base payload file missing — base payload not located"
    [ -f "$R5TGT/.omakase/R5-SOURCE-MARKER" ] && pass "source marker placed (source delta layered over the base)" || fail "source marker missing"

    # ---- leg 2: the binary DIRECTLY, no shim at all (issue #168) ----
    R5TGT2="$R5/target-standalone"; scratch_repo "$R5TGT2"
    R5SAOUT="$R5/standalone.out"; R5SAERR="$R5/standalone.err"
    ( cd "$R5TGT2" && env -i PATH="$CLEANPATH" HOME="$R5HOME" XDG_CACHE_HOME="$XDG" \
      "$R5BIN" init --source "$R5SRC" >"$R5SAOUT" 2>"$R5SAERR" )
    rc=$?
    [ "$rc" -eq 0 ] && pass "standalone binary (no shim) inits via the embedded base (#168)" || fail "standalone init exited $rc ($(cat "$R5SAERR"))"
    [ -f "$R5TGT2/.omakase/VERSION" ] && pass "embedded base payload placed" || fail "base payload missing from standalone init"
    [ -f "$R5TGT2/.omakase/R5-SOURCE-MARKER" ] && pass "source delta layered over the embedded base" || fail "source marker missing from standalone init"
    [ -d "$XDG/omakase/basepayload" ] && pass "embedded base extracted into the machine cache" || fail "no basepayload extraction dir — embedded fallback did not run"

    # ---- leg 3: bare init with nothing remembered places NOTHING (#123 item 1) ----
    R5TGT3="$R5/target-bare"; scratch_repo "$R5TGT3"
    R5OUT3="$R5/bare.out"; R5ERR3="$R5/bare.err"
    ( cd "$R5TGT3" && env -i PATH="$CLEANPATH" HOME="$R5HOME" XDG_CACHE_HOME="$XDG" \
      bash "$CLONE/bin/init.sh" >"$R5OUT3" 2>"$R5ERR3" )
    rc=$?
    [ "$rc" -eq 0 ] && pass "bare init (no --source, no remembered source) exits 0 via the stable copy" || fail "bare init exited $rc ($(cat "$R5ERR3"))"
    grep -q 'nothing to refresh' "$R5OUT3" && pass "bare init printed the one-line pointer at status" || fail "no 'nothing to refresh' in bare-init stdout ($(cat "$R5OUT3"))"
    [ -e "$R5TGT3/.omakase" ] && fail "bare init placed base machinery despite nothing remembered" || pass "bare init placed nothing (no silent base-machinery install)"
  else
    fail "R5 could not build the omakase binary ($(cat "$R5/build.err"))"
  fi
else
  echo "  SKIP: no go on PATH to build a real binary — the go CI jobs run this scenario"
fi

rm -rf "$TMP"
echo ""
[ "$FAILED" -eq 0 ] && echo "ALL PASS" || { echo "FAILURES PRESENT"; exit 1; }
