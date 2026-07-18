#!/usr/bin/env bash
# Proof of omakase binary self-provisioning (task 1 of the binary-bootstrap
# feature): resolve_omakase gains a fetch tier — a pinned, checksum-verified
# omakase release binary fetched into a per-machine cache when nothing else
# resolves.
#   O1. platform -> asset-name mapping (uname tokens -> goreleaser's OS/ARCH)
#   O2. fetch happy path — download (from a fixture base URL, no network) ->
#       verify archive sha256 -> extract only `omakase` -> verify binary
#       sha256 -> chmod +x -> atomic move into the cache; binary reused
#   O3. archive checksum mismatch is REJECTED — nothing cached, no residue
#   O4. binary checksum mismatch (valid archive, bad extracted-binary hash) is
#       REJECTED — nothing cached, no residue
#   O5. shim wiring through a simulated plugin clone (bin/+payload/, no
#       go.mod, no dist/): offline -> status.sh and init.sh fail closed
#       (guidance on stderr, exit 1 — no bash fallback exists); cache
#       pre-seeded -> the cached stub is exec'd with the right verb
#   O6. remove.sh never fetches (offline -> fail closed, nothing cached)
#       but DOES use an already-cached binary when one is present
#   O7. mcp.sh: resolution failure is stderr guidance + exit 1, with stdout
#       left completely clean for the MCP stdio transport
#   O8. (opt-in, OMAKASE_TEST_LIVE_FETCH=1) one real download from GitHub
#   O9. tier 2's go build failure aborts the shim (exit nonzero, stale
#       dist/omakase never runs) instead of falling through under the
#       if-condition's set -e suppression; a succeeding build still execs
#   O10. issue #70 regression: the binary sits ALONE in the cache (a fetch /
#        PATH install, no payload/ sibling); shim -> cached binary -> init
#        --source finds the base merge payload only via the shim-exported
#        OMAKASE_BASE_PAYLOAD. Its negative control runs the cached binary
#        DIRECTLY with no such export and proves it fails fast, naming the path.
# HOME and XDG_CACHE_HOME point at fixture dirs so nothing touches the real machine.
set -u
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
LIB="$HERE/../bin/lib-omakase-bin.sh"
TMP="${TMPDIR:-/tmp}/omakase-bin-fetch-test.$$"
FAILED=0
pass(){ echo "  PASS: $1"; }
fail(){ echo "  FAIL: $1"; FAILED=1; }

mkdir -p "$TMP"

# A minimal PATH with no omakase/go on it. The suite/CI exports things onto
# PATH that would win at tiers 1-4 before the fetch tier — so every subshell
# that must exercise the FETCH path runs under `env -i` with this PATH,
# guaranteeing resolution falls through to tier 6.
CLEANPATH="/usr/bin:/bin:/usr/sbin:/sbin"

# sha256 of a file, matching the lib's tool detection.
sha_of(){ if command -v shasum >/dev/null 2>&1; then shasum -a 256 "$1" | awk '{print $1}'; else sha256sum "$1" | awk '{print $1}'; fi; }

# The version the lib pins — read it from the lib so the test never drifts from it.
VER="$(. "$LIB"; echo "$OMAKASE_PIN_VERSION")"

# A bin/ dir with no go.mod / dist/omakase nearby, so resolve_omakase's tiers
# 2-3 (dev rebuild, prebuilt dist) never fire and it falls through to fetch.
FAKEBIN="$TMP/norepo/bin"; mkdir -p "$FAKEBIN"

# ---------- Scenario O1: platform -> asset-name mapping ----------
echo "== Scenario O1: uname tokens map to goreleaser's OS/ARCH asset name =="
# Drive omakase_platform with a stubbed uname so the mapping is exercised
# deterministically on whatever host runs the suite. A function named `uname`
# shadows the real binary inside the subshell.
map(){  # $1 = uname -s, $2 = uname -m -> echoes "OS ARCH" or "FAIL"
  ( . "$LIB"
    uname(){ case "$1" in -s) echo "$U_S";; -m) echo "$U_M";; esac; }
    U_S="$1"; U_M="$2"
    if omakase_platform; then echo "$OMK_OS $OMK_ARCH"; else echo FAIL; fi )
}
[ "$(map Darwin arm64)"   = "darwin arm64" ]  && pass "Darwin/arm64 -> darwin arm64"   || fail "Darwin/arm64 mapping ($(map Darwin arm64))"
[ "$(map Darwin x86_64)"  = "darwin amd64" ]  && pass "Darwin/x86_64 -> darwin amd64"  || fail "Darwin/x86_64 mapping ($(map Darwin x86_64))"
[ "$(map Linux aarch64)"  = "linux arm64" ]   && pass "Linux/aarch64 -> linux arm64"   || fail "Linux/aarch64 mapping ($(map Linux aarch64))"
[ "$(map Linux amd64)"    = "linux amd64" ]   && pass "Linux/amd64 -> linux amd64"     || fail "Linux/amd64 mapping ($(map Linux amd64))"
[ "$(map FreeBSD amd64)"  = "FAIL" ]          && pass "unknown OS fails gracefully"    || fail "FreeBSD accepted ($(map FreeBSD amd64))"
[ "$(map Linux riscv64)"  = "FAIL" ]          && pass "unknown ARCH fails gracefully"  || fail "riscv64 accepted ($(map Linux riscv64))"
# every mapped stem has baked-in ARCHIVE and BINARY checksums
miss=""
for pair in "darwin arm64" "darwin amd64" "linux arm64" "linux amd64"; do
  set -- $pair
  stem="omakase_${VER}_$1_$2"
  ah="$( . "$LIB"; omakase_archive_sha256_for "$stem.tar.gz" )"
  bh="$( . "$LIB"; omakase_bin_sha256_for "$stem" )"
  [ -n "$ah" ] || miss="$miss $stem(archive)"
  [ -n "$bh" ] || miss="$miss $stem(binary)"
done
[ -z "$miss" ] && pass "every supported asset has baked-in archive+binary checksums" || fail "missing checksums:$miss"

# ---------- Scenario O2: fetch happy path (download->verify->extract->verify->chmod->cache) ----------
echo "== Scenario O2: fetch downloads, verifies, extracts, verifies, chmods, atomically caches =="
# Build a fixture release archive containing a fake `omakase` binary plus a
# decoy README.md (real releases also carry a README/LICENSE — the archive
# extraction must pull out only the `omakase` member). Its real sha256 won't
# equal the baked-in values, so we override both hash fns in the subshell to
# return the fixture's actual digests — exercising the verify path against
# known-good digests with NO network and NO real binary.
BASE="$TMP/base/v$VER"; mkdir -p "$BASE"
# Determine THIS host's asset stem from the lib's own platform detection.
ASSET_STEM="$( . "$LIB"; omakase_platform && echo "omakase_${VER}_${OMK_OS}_${OMK_ARCH}" || echo UNSUPPORTED )"
if [ "$ASSET_STEM" = "UNSUPPORTED" ]; then
  echo "  SKIP: host platform unsupported by the fetcher — O2/O3/O4 need a host asset name"
else
  ASSET="$BASE/$ASSET_STEM.tar.gz"
  FIXDIR="$TMP/fixture"; rm -rf "$FIXDIR"; mkdir -p "$FIXDIR"
  ( cd "$FIXDIR" && printf '#!/bin/sh\necho fixture-omakase "$@"\n' > omakase && printf 'decoy\n' > README.md && tar czf "$ASSET" omakase README.md )
  GOOD_ARCHIVE_HASH="$(sha_of "$ASSET")"
  GOOD_BIN_HASH="$(sha_of "$FIXDIR/omakase")"

  FAKEHOME="$TMP/home-o2"; CACHEHOME="$TMP/cache-o2"; mkdir -p "$FAKEHOME" "$CACHEHOME"
  OUT="$( env -i HOME="$FAKEHOME" XDG_CACHE_HOME="$CACHEHOME" PATH="$CLEANPATH" \
    OMAKASE_RELEASE_BASE_URL="$BASE" \
    bash -c '
      HERE="'"$FAKEBIN"'"
      . "'"$LIB"'"
      omakase_archive_sha256_for(){ echo "'"$GOOD_ARCHIVE_HASH"'"; }
      omakase_bin_sha256_for(){ echo "'"$GOOD_BIN_HASH"'"; }
      if resolve_omakase fetch; then echo "RESOLVED:$OMAKASE_BIN_RESOLVED"; else echo FAILED; fi
    ' 2>&1 )"
  CACHED="$CACHEHOME/omakase/bin/$VER/omakase"
  echo "$OUT" | grep -q "RESOLVED:$CACHED" && pass "resolve_omakase fetched and pointed at the cache" || fail "fetch did not resolve to the cache ($OUT)"
  [ -f "$CACHED" ] && pass "binary cached at the per-machine path" || fail "no cached binary at $CACHED"
  [ -x "$CACHED" ] && pass "cached binary is executable (chmod +x ran)" || fail "cached binary not executable"
  [ "$(sha_of "$CACHED")" = "$GOOD_BIN_HASH" ] && pass "cached bytes match the verified, extracted binary" || fail "cached bytes differ from the fixture binary"
  [ ! -e "$CACHEHOME/omakase/bin/$VER/README.md" ] && pass "no README.md leaked into the cache" || fail "README.md leaked into the cache"
  find "$CACHEHOME" -name '.omakase.download.*' -o -name '.omakase.extract.*' 2>/dev/null | grep -q . && fail "temp download/extract residue left behind" || pass "no temp download/extract residue anywhere under the cache"

  # reuse: a second resolve with NO fetch and UNMODIFIED (real, baked-in) hash
  # fns still finds the cached binary directly (tier-5 cache hit precedes any
  # fetch, and tier 5 doesn't re-verify), proving one download per machine.
  OUT2="$( env -i HOME="$FAKEHOME" XDG_CACHE_HOME="$CACHEHOME" PATH="$CLEANPATH" bash -c '
      HERE="'"$FAKEBIN"'"
      . "'"$LIB"'"
      if resolve_omakase; then echo "RESOLVED:$OMAKASE_BIN_RESOLVED"; else echo FAILED; fi' 2>&1 )"
  echo "$OUT2" | grep -q "RESOLVED:$CACHED" && pass "cached binary reused with no fetch (one download per machine)" || fail "cache not reused ($OUT2)"
fi

# ---------- Scenario O3: archive checksum mismatch is rejected, nothing cached ----------
echo "== Scenario O3: an archive that fails checksum verification is rejected =="
if [ "$ASSET_STEM" != "UNSUPPORTED" ]; then
  MMHOME="$TMP/home-o3"; MMCACHE="$TMP/cache-o3"; mkdir -p "$MMHOME" "$MMCACHE"
  BASE3="$TMP/base3/v$VER"; mkdir -p "$BASE3"
  # Serve bytes that do NOT match the baked-in (default, real) archive checksum.
  printf 'totally-wrong-bytes\n' > "$BASE3/$ASSET_STEM.tar.gz"
  OUT="$( env -i HOME="$MMHOME" XDG_CACHE_HOME="$MMCACHE" PATH="$CLEANPATH" \
    OMAKASE_RELEASE_BASE_URL="$BASE3" \
    bash -c '
      HERE="'"$FAKEBIN"'"
      . "'"$LIB"'"
      if resolve_omakase fetch; then echo "RESOLVED:$OMAKASE_BIN_RESOLVED"; else echo FAILED; fi
    ' 2>&1 )"
  echo "$OUT" | grep -q FAILED && pass "archive checksum mismatch -> resolve_omakase fails" || fail "mismatch was accepted ($OUT)"
  echo "$OUT" | grep -qi 'checksum mismatch' && pass "mismatch is reported" || fail "no mismatch message ($OUT)"
  [ ! -e "$MMCACHE/omakase/bin/$VER/omakase" ] && pass "nothing cached on archive mismatch" || fail "a binary was cached despite the archive mismatch"
  find "$MMCACHE" -name '.omakase.download.*' -o -name '.omakase.extract.*' 2>/dev/null | grep -q . && fail "temp residue left behind on archive mismatch" || pass "no temp residue on archive mismatch"
fi

# ---------- Scenario O4: binary checksum mismatch (valid archive) is rejected ----------
echo "== Scenario O4: a valid archive whose extracted binary fails verification is rejected =="
if [ "$ASSET_STEM" != "UNSUPPORTED" ]; then
  # Reuse O2's valid fixture archive at $BASE/$ASSET_STEM.tar.gz. Override ONLY
  # the archive hash fn (to the fixture's real digest, so archive verification
  # passes); leave omakase_bin_sha256_for at its baked-in value, which does not
  # match the fixture binary's real digest -> the post-extraction check must fail.
  BMHOME="$TMP/home-o4"; BMCACHE="$TMP/cache-o4"; mkdir -p "$BMHOME" "$BMCACHE"
  OUT="$( env -i HOME="$BMHOME" XDG_CACHE_HOME="$BMCACHE" PATH="$CLEANPATH" \
    OMAKASE_RELEASE_BASE_URL="$BASE" \
    bash -c '
      HERE="'"$FAKEBIN"'"
      . "'"$LIB"'"
      omakase_archive_sha256_for(){ echo "'"$GOOD_ARCHIVE_HASH"'"; }
      if resolve_omakase fetch; then echo "RESOLVED:$OMAKASE_BIN_RESOLVED"; else echo FAILED; fi
    ' 2>&1 )"
  echo "$OUT" | grep -q FAILED && pass "binary checksum mismatch -> resolve_omakase fails" || fail "mismatch was accepted ($OUT)"
  echo "$OUT" | grep -qi 'binary checksum mismatch' && pass "mismatch is reported" || fail "no mismatch message ($OUT)"
  [ ! -e "$BMCACHE/omakase/bin/$VER/omakase" ] && pass "nothing cached on binary mismatch" || fail "a binary was cached despite the binary mismatch"
  find "$BMCACHE" -name '.omakase.download.*' -o -name '.omakase.extract.*' 2>/dev/null | grep -q . && fail "temp residue left behind on binary mismatch" || pass "no temp residue on binary mismatch"
fi

# ---------- Simulated plugin clone for O5-O8: bin/+payload/, no go.mod, no dist/ ----------
# Models a payload-only dist clone / the no-go.mod shape (a real plugin clone
# does carry a go.mod — tier 2 is skipped there instead at `command -v go`,
# since a plugin install has no Go on PATH). Either way tiers 1-4 miss, leaving
# only the cache (tier 5) or a fetch (tier 6) to resolve from inside it.
echo "== Building a simulated plugin clone (bin/ + payload/, no go.mod, no dist/) =="
CLONE="$TMP/clone"; mkdir -p "$CLONE"
cp -R "$HERE/../bin" "$CLONE/bin"
cp -R "$HERE/../payload" "$CLONE/payload"
[ ! -e "$CLONE/go.mod" ] && [ ! -e "$CLONE/dist" ] && pass "clone has no go.mod/dist (tiers 2-3 are structurally unreachable)" || fail "clone contaminated with go.mod/dist"

# A scratch git repo to run the shims from (init/status/remove expect a repo).
scratch_repo(){  # $1 = dir to create
  mkdir -p "$1"
  ( cd "$1" && git init -q && git config user.email t@t && git config user.name t && git config commit.gpgsign false && git commit -q --allow-empty -m init )
}

# ---------- Scenario O5: shim resolution through the clone ----------
echo "== Scenario O5: clone status.sh/init.sh fail closed offline and resolve via a seeded cache =="
REPO5="$TMP/repo-o5"; scratch_repo "$REPO5"

# (a) Completely offline: empty cache, empty base URL -> fetch fails -> the shim
# fails CLOSED, mirroring O7: guidance on stderr, exit 1, stdout untouched. There
# is no bash fallback body — a silent one would mask binary-distribution failures.
O5AHOME="$TMP/home-o5a"; O5ACACHE="$TMP/cache-o5a"; mkdir -p "$O5AHOME" "$O5ACACHE"
O5AOUT="$TMP/o5a.out"; O5AERR="$TMP/o5a.err"
( cd "$REPO5" && env -i HOME="$O5AHOME" XDG_CACHE_HOME="$O5ACACHE" PATH="$CLEANPATH" \
  OMAKASE_RELEASE_BASE_URL="$TMP/empty-base-o5a" \
  bash "$CLONE/bin/status.sh" >"$O5AOUT" 2>"$O5AERR" )
rc=$?
[ "$rc" -eq 1 ] && pass "clone status.sh exits 1 when nothing resolves offline" || fail "status.sh exited $rc, expected 1"
grep -q 'status needs the omakase binary' "$O5AERR" && pass "status.sh prints recovery guidance to stderr" || fail "no guidance on stderr ($(cat "$O5AERR"))"
[ ! -s "$O5AOUT" ] && pass "status.sh stdout stays empty on the fail-closed path" || fail "status.sh wrote to stdout: $(cat "$O5AOUT")"

# init.sh takes the same fail-closed path (it was the other verb with a v1
# fallback body; the third, remove.sh, is O6's subject).
( cd "$REPO5" && env -i HOME="$O5AHOME" XDG_CACHE_HOME="$O5ACACHE" PATH="$CLEANPATH" \
  OMAKASE_RELEASE_BASE_URL="$TMP/empty-base-o5a" \
  bash "$CLONE/bin/init.sh" >"$O5AOUT" 2>"$O5AERR" )
rc=$?
[ "$rc" -eq 1 ] && pass "clone init.sh exits 1 when nothing resolves offline" || fail "init.sh exited $rc, expected 1"
grep -q 'init needs the omakase binary' "$O5AERR" && pass "init.sh prints recovery guidance to stderr" || fail "no guidance on stderr ($(cat "$O5AERR"))"
[ ! -s "$O5AOUT" ] && pass "init.sh stdout stays empty on the fail-closed path" || fail "init.sh wrote to stdout: $(cat "$O5AOUT")"

# (b) Hash-fn overrides can't cross the exec into a separate script file, so
# instead of faking a fetch, pre-seed the cache directly (tier 5) and confirm
# the shim execs the cached binary with the right verb.
O5BHOME="$TMP/home-o5b"; O5BCACHE="$TMP/cache-o5b"; mkdir -p "$O5BHOME"
STUB5B="$O5BCACHE/omakase/bin/$VER"; mkdir -p "$STUB5B"
printf '#!/bin/sh\necho fixture-omakase "$@"\n' > "$STUB5B/omakase"; chmod +x "$STUB5B/omakase"
OUT="$( cd "$REPO5" && env -i HOME="$O5BHOME" XDG_CACHE_HOME="$O5BCACHE" PATH="$CLEANPATH" \
  bash "$CLONE/bin/status.sh" 2>&1 )"
echo "$OUT" | grep -q 'fixture-omakase status' && pass "clone status.sh execs the cached stub (tier 5 cache hit, no network)" || fail "cached stub not used ($OUT)"

# ---------- Scenario O6: remove never fetches but DOES use an already-cached binary ----------
echo "== Scenario O6: clone remove.sh never fetches (offline -> fail closed), but uses a seeded cache =="
REPO6="$TMP/repo-o6"; scratch_repo "$REPO6"

O6AHOME="$TMP/home-o6a"; O6ACACHE="$TMP/cache-o6a"; mkdir -p "$O6AHOME" "$O6ACACHE"
O6AOUT="$TMP/o6a.out"; O6AERR="$TMP/o6a.err"
( cd "$REPO6" && env -i HOME="$O6AHOME" XDG_CACHE_HOME="$O6ACACHE" PATH="$CLEANPATH" \
  OMAKASE_RELEASE_BASE_URL="$TMP/empty-base-o6" \
  bash "$CLONE/bin/remove.sh" >"$O6AOUT" 2>"$O6AERR" )
rc=$?
[ "$rc" -eq 1 ] && pass "clone remove.sh exits 1 when nothing resolves (and it never fetches)" || fail "remove.sh exited $rc, expected 1"
grep -q 'remove needs the omakase binary' "$O6AERR" && pass "remove.sh prints recovery guidance to stderr" || fail "no guidance on stderr ($(cat "$O6AERR"))"
grep -q 'OMAKASE_BIN' "$O6AERR" && pass "remove.sh guidance names the OMAKASE_BIN escape hatch" || fail "guidance missing OMAKASE_BIN ($(cat "$O6AERR"))"
[ ! -e "$O6ACACHE/omakase" ] && pass "nothing appeared in the cache (remove never attempts a fetch)" || fail "cache dir was populated despite remove.sh never fetching"

O6BHOME="$TMP/home-o6b"; O6BCACHE="$TMP/cache-o6b"; mkdir -p "$O6BHOME"
STUB6B="$O6BCACHE/omakase/bin/$VER"; mkdir -p "$STUB6B"
printf '#!/bin/sh\necho fixture-omakase "$@"\n' > "$STUB6B/omakase"; chmod +x "$STUB6B/omakase"
OUT="$( cd "$REPO6" && env -i HOME="$O6BHOME" XDG_CACHE_HOME="$O6BCACHE" PATH="$CLEANPATH" \
  bash "$CLONE/bin/remove.sh" 2>&1 )"
echo "$OUT" | grep -q 'fixture-omakase remove' && pass "clone remove.sh uses the cached binary without network" || fail "cached stub not used for remove ($OUT)"

# ---------- Scenario O7: mcp.sh — guidance on stderr, exit 1, clean stdout ----------
echo "== Scenario O7: clone mcp.sh reports guidance on stderr only and exits 1 =="
O7HOME="$TMP/home-o7"; O7CACHE="$TMP/cache-o7"; mkdir -p "$O7HOME"
OUTFILE="$TMP/o7.out"; ERRFILE="$TMP/o7.err"
env -i HOME="$O7HOME" XDG_CACHE_HOME="$O7CACHE" PATH="$CLEANPATH" \
  OMAKASE_RELEASE_BASE_URL="$TMP/empty-base-o7" \
  bash "$CLONE/bin/mcp.sh" >"$OUTFILE" 2>"$ERRFILE"
rc=$?
[ "$rc" -eq 1 ] && pass "clone mcp.sh exits 1 when nothing resolves" || fail "mcp.sh exited $rc, expected 1"
grep -q 'mcp needs the omakase binary' "$ERRFILE" && pass "mcp.sh prints guidance to stderr" || fail "no guidance on stderr ($(cat "$ERRFILE"))"
[ ! -s "$OUTFILE" ] && pass "mcp.sh stdout stays empty (the stdio transport is never polluted)" || fail "mcp.sh wrote to stdout: $(cat "$OUTFILE")"

# ---------- Scenario O8: opt-in live fetch of the real pinned release ----------
echo "== Scenario O8: live fetch from GitHub (opt-in: OMAKASE_TEST_LIVE_FETCH=1) =="
if [ "${OMAKASE_TEST_LIVE_FETCH:-}" = "1" ]; then
  O8HOME="$TMP/home-o8"; O8CACHE="$TMP/cache-o8"; mkdir -p "$O8HOME"
  OUT="$( env -i HOME="$O8HOME" XDG_CACHE_HOME="$O8CACHE" PATH="$CLEANPATH" bash -c '
      HERE="'"$FAKEBIN"'"
      . "'"$LIB"'"
      if resolve_omakase fetch; then echo "RESOLVED:$OMAKASE_BIN_RESOLVED"; else echo FAILED; fi' 2>&1 )"
  O8CACHED="$O8CACHE/omakase/bin/$VER/omakase"
  echo "$OUT" | grep -q "RESOLVED:$O8CACHED" && pass "live: real omakase binary fetched + checksum-verified into the cache" || fail "live fetch failed ($OUT)"
  [ -x "$O8CACHED" ] && "$O8CACHED" --version 2>&1 | grep -q "$VER" && pass "live: fetched binary runs and reports the pinned version ($VER)" || fail "live: fetched binary missing or wrong version"
else
  echo "  SKIP: set OMAKASE_TEST_LIVE_FETCH=1 to exercise a real download"
fi

# ---------- Scenario O9: tier 2's go build failure aborts the shim, never falls through to a stale dist/omakase ----------
echo "== Scenario O9: a FAILING go build at tier 2 aborts the shim instead of exec'ing a stale dist/omakase =="
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
REPO9="$TMP/repo-o9"; scratch_repo "$REPO9"

# (a) a failing `go` must abort the shim non-zero, never running the stale binary.
FAILGO="$TMP/fakebin-o9-fail"; mkdir -p "$FAILGO"
printf '#!/bin/sh\necho "fake go: build failed" >&2\nexit 1\n' > "$FAILGO/go"; chmod +x "$FAILGO/go"
O9AHOME="$TMP/home-o9a"; O9ACACHE="$TMP/cache-o9a"; mkdir -p "$O9AHOME" "$O9ACACHE"
OUTFILE="$TMP/o9a.out"; ERRFILE="$TMP/o9a.err"
( cd "$REPO9" && env -i HOME="$O9AHOME" XDG_CACHE_HOME="$O9ACACHE" PATH="$FAILGO:$CLEANPATH" \
  bash "$DEVREPO/bin/status.sh" >"$OUTFILE" 2>"$ERRFILE" )
rc=$?
[ "$rc" -ne 0 ] && pass "a failing go build aborts the shim (exit $rc)" || fail "shim exited 0 despite a failing go build"
grep -q 'STALE-BINARY-RAN' "$OUTFILE" && fail "stale dist/omakase ran despite the failing build ($(cat "$OUTFILE"))" || pass "stale dist/omakase never ran"
grep -q 'fake go: build failed' "$ERRFILE" && pass "the go build's own failure reached stderr" || fail "go build failure missing from stderr ($(cat "$ERRFILE"))"

# (b) inverse: a SUCCEEDING go (simulated by rewriting dist/omakase in place,
# standing in for a real rebuild) still lets the shim exec dist/omakase —
# proving the fix didn't turn tier 2 into an unconditional abort.
OKGO="$TMP/fakebin-o9-ok"; mkdir -p "$OKGO"
cat > "$OKGO/go" <<GOEOF
#!/bin/sh
printf '#!/bin/sh\necho STALE-BINARY-RAN "\$@"\n' > "$DEVREPO/dist/omakase"
chmod +x "$DEVREPO/dist/omakase"
exit 0
GOEOF
chmod +x "$OKGO/go"
O9BHOME="$TMP/home-o9b"; O9BCACHE="$TMP/cache-o9b"; mkdir -p "$O9BHOME" "$O9BCACHE"
OUTFILE2="$TMP/o9b.out"
( cd "$REPO9" && env -i HOME="$O9BHOME" XDG_CACHE_HOME="$O9BCACHE" PATH="$OKGO:$CLEANPATH" \
  bash "$DEVREPO/bin/status.sh" >"$OUTFILE2" 2>&1 )
rc2=$?
[ "$rc2" -eq 0 ] && grep -q 'STALE-BINARY-RAN status' "$OUTFILE2" && pass "a succeeding go build still lets the shim exec dist/omakase" || fail "shim did not run dist/omakase after a succeeding build (rc=$rc2, out=$(cat "$OUTFILE2"))"

# ---------- Scenario O10: fetched-binary init works end-to-end (issue #70 regression) ----------
echo "== Scenario O10: a cache-resident release binary drives init end-to-end (issue #70) =="
# The issue's exact shape: the omakase binary sits ALONE in the per-machine cache
# (the fetch / PATH-install case, no payload/ sibling), so the --source merge base
# is NOT discoverable binary-relative. resolve_omakase exports the plugin's own
# bin/../payload in OMAKASE_BASE_PAYLOAD (fix #70) and the binary honors it. O10
# itself touches no network: a real binary drives a LOCAL source clone.
#
# Binary source, in order: go on PATH builds from source; else the pinned release
# binary O8 fetched this run (OMAKASE_TEST_LIVE_FETCH=1) stands in, so a host
# without Go still reaches the #70 path; else skip. Legs 7-8 are identical either
# way — they need only a real omakase binary seeded into the cache. Leg 9 asserts
# HEAD (#123) bare-init semantics and runs only on the HEAD build.
O10="$TMP/o10"; mkdir -p "$O10"
O10BUILT=""
O10HEADBUILD=""   # set when O10BUILT is compiled from this checkout
if command -v go >/dev/null 2>&1; then
  # Build the real binary ONCE for the whole scenario — every leg shares it.
  if ( cd "$HERE/.." && go build -o "$O10/omakase-built" ./cmd/omakase ) 2>"$O10/build.err"; then
    O10BUILT="$O10/omakase-built"
    O10HEADBUILD=1
  else
    fail "O10 could not build the omakase binary ($(cat "$O10/build.err"))"
  fi
elif [ "${OMAKASE_TEST_LIVE_FETCH:-}" = "1" ] && [ -x "${O8CACHED:-}" ]; then
  O10BUILT="$O8CACHED"
else
  echo "  SKIP: no omakase binary available — put go on PATH to build one, or set OMAKASE_TEST_LIVE_FETCH=1 to reuse O8's fetched release binary"
fi
if [ -n "$O10BUILT" ]; then
    # init writes its own git-hook dispatchers (no external hook runner), so O10
    # needs no hook-installer stub: it fires no commit and only exercises the real
    # omakase binary's WHOLE init flow — the base-payload handoff under test.
    # Simulated plugin clone: bin/ (+libs) and payload/, NO go.mod / dist/,
    # so resolution tiers 2-3 are unreachable and it falls to the cached binary
    # (tier 5) — the same trick as O5.
    mkdir -p "$O10/plugin"
    cp -R "$HERE/../bin" "$O10/plugin/bin"
    cp -R "$HERE/../payload" "$O10/plugin/payload"
    # Fake cache holding ONLY the binary at the tier-5 path — the fetched/PATH
    # install shape (no payload/ sibling), fetched without any download.
    XDG="$O10/xdg"
    O10CACHE="$XDG/omakase/bin/$VER"; mkdir -p "$O10CACHE"
    cp "$O10BUILT" "$O10CACHE/omakase"; chmod +x "$O10CACHE/omakase"
    O10BIN="$O10CACHE/omakase"
    O10HOME="$O10/home"; mkdir -p "$O10HOME"

    # Local fixture source repo: the one payload/omakase.manifest + one marker
    # payload file, committed. Both binary sources (HEAD build and the ≥ 0.20.0
    # pinned release) read the one-manifest layout.
    O10SRC="$O10/src"; mkdir -p "$O10SRC/payload/.omakase"
    ( cd "$O10SRC" && git init -q && git config user.email t@t && git config user.name t && git config commit.gpgsign false )
    printf 'o10-source-marker\n' > "$O10SRC/payload/.omakase/O10-SOURCE-MARKER"
    printf 'name: o10-fixture\nversion: 0.1.0\n' > "$O10SRC/payload/omakase.manifest"
    ( cd "$O10SRC" && git add -A && git commit -q -m fixture )
    O10SRC="$(cd "$O10SRC" && pwd)"   # absolutize (macOS TMPDIR trails a slash), as init does

    # ---- leg 7: THE PROBE — shim -> cached binary -> init --source, offline ----
    # git must be reachable through CLEANPATH (as every scenario manages it); HOME
    # gives git a config dir. init writes its own hook dispatchers, so no external
    # hook installer is involved.
    O10TGT="$O10/target"; scratch_repo "$O10TGT"
    O10OUT="$O10/probe.out"; O10ERR="$O10/probe.err"
    ( cd "$O10TGT" && env -i PATH="$CLEANPATH" HOME="$O10HOME" XDG_CACHE_HOME="$XDG" \
      bash "$O10/plugin/bin/init.sh" --source "$O10SRC" >"$O10OUT" 2>"$O10ERR" )
    rc=$?
    [ "$rc" -eq 0 ] && pass "shim -> cached binary -> init --source exits 0 (fetched-binary init works)" || fail "probe exited $rc ($(cat "$O10ERR"))"
    grep -q 'cached at' "$O10OUT" && pass "the source was cached + injected (the full --source flow ran)" || fail "no 'cached at' in probe stdout ($(cat "$O10OUT"))"
    [ -x "$O10TGT/.omakase/bin/omakase-banner.sh" ] && pass "base payload file placed (OMAKASE_BASE_PAYLOAD located the merge base)" || fail "base machinery missing — base payload not located"
    [ -f "$O10TGT/.omakase/O10-SOURCE-MARKER" ] && pass "source marker placed (source delta layered over the base)" || fail "source marker missing"

    # ---- leg 8: negative control — the cached binary DIRECTLY, no OMAKASE_BASE_PAYLOAD ----
    # Proves the leg-7 assertions bite: with no shim to export OMAKASE_BASE_PAYLOAD,
    # defaultPayload falls to the binary-relative ../payload — a non-existent
    # <cache>/bin/payload — and the merge base is not found. This is the pre-#70
    # failure mode, now a clear fail-fast BEFORE any clone. Same env as leg 7 minus
    # the shim, so the ONLY difference is the absent OMAKASE_BASE_PAYLOAD export.
    O10NCERR="$O10/negctl.err"
    ( cd "$O10TGT" && env -i PATH="$CLEANPATH" HOME="$O10HOME" XDG_CACHE_HOME="$XDG" \
      "$O10BIN" init --source "$O10SRC" >/dev/null 2>"$O10NCERR" )
    rc=$?
    [ "$rc" -eq 1 ] && pass "cached binary run WITHOUT OMAKASE_BASE_PAYLOAD exits 1 (the assertion bites)" || fail "negative control exited $rc, expected 1 ($(cat "$O10NCERR"))"
    grep -q 'base payload not found at' "$O10NCERR" && pass "negative control fails fast, naming the base payload path it tried" || fail "no 'base payload not found at' on stderr ($(cat "$O10NCERR"))"

    # ---- leg 9: bare init with nothing remembered places NOTHING (#123 item 1) ----
    # A fresh repo, no --source, no remembered source: there is no harness to
    # refresh, so init prints the one-line pointer at status and exits 0 —
    # never the old silent base-machinery install, even though the shim
    # exported OMAKASE_BASE_PAYLOAD (merge-base plumbing, not install intent).
    # HEAD-build only: the pinned release standing in on a no-Go host predates
    # #123 (it still installs the base payload) until the next release re-pin.
    if [ -n "$O10HEADBUILD" ]; then
        O10TGT2="$O10/target2"; scratch_repo "$O10TGT2"
        O10OUT2="$O10/bare.out"; O10ERR2="$O10/bare.err"
        ( cd "$O10TGT2" && env -i PATH="$CLEANPATH" HOME="$O10HOME" XDG_CACHE_HOME="$XDG" \
          bash "$O10/plugin/bin/init.sh" >"$O10OUT2" 2>"$O10ERR2" )
        rc=$?
        [ "$rc" -eq 0 ] && pass "bare init (no --source, no remembered source) exits 0 via the cached binary" || fail "bare init exited $rc ($(cat "$O10ERR2"))"
        grep -q 'nothing to refresh' "$O10OUT2" && pass "bare init printed the one-line pointer at status" || fail "no 'nothing to refresh' in bare-init stdout ($(cat "$O10OUT2"))"
        [ -e "$O10TGT2/.omakase" ] && fail "bare init placed base machinery despite nothing remembered" || pass "bare init placed nothing (no silent base-machinery install)"
    else
        echo "  SKIP: leg 9 asserts HEAD (#123) bare-init semantics — the stand-in pinned release predates them"
    fi
fi

rm -rf "$TMP"
echo ""
[ "$FAILED" -eq 0 ] && echo "ALL PASS" || { echo "FAILURES PRESENT"; exit 1; }
