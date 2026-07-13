#!/usr/bin/env bash
# Proof of lefthook self-provisioning (spec 2026-06-15-lefthook-self-fetch):
# resolution gains a 4th tier — a pinned, checksum-verified lefthook binary
# fetched into a per-machine cache when lefthook is absent everywhere else.
#   L1. platform -> asset-name mapping (uname tokens -> lefthook's OS/ARCH)
#   L2. fetch happy path — download (from a fixture base URL, no network) ->
#       verify sha256 -> chmod +x -> atomic move into the cache; binary reused
#   L3. checksum mismatch is REJECTED — nothing cached, resolve_lefthook fails
#   L4. graceful fallback through init.sh — fetch fails, init exits non-zero
#       with the install guidance and leaves NOTHING half-installed
#   L5. remove.sh never fetches but DOES use an already-cached binary
#   L6. (opt-in, OMAKASE_TEST_LIVE_FETCH=1) one real download from GitHub
# HOME and XDG_CACHE_HOME point at fixture dirs so nothing touches the real machine.
set -u
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
LIB="$HERE/../bin/lib-lefthook.sh"
INIT="$HERE/../bin/init.sh"
REMOVE="$HERE/../bin/remove.sh"
TMP="${TMPDIR:-/tmp}/omakase-lefthook-test.$$"
FAILED=0
pass(){ echo "  PASS: $1"; }
fail(){ echo "  FAIL: $1"; FAILED=1; }

FAKEHOME="$TMP/home"; CACHEHOME="$TMP/cache"
mkdir -p "$FAKEHOME" "$CACHEHOME"

# A minimal PATH with no lefthook on it. The suite/CI exports a lefthook onto PATH
# and may set LEFTHOOK_BIN, both of which win at tiers 1/2 before the fetch tier —
# so every subshell that must exercise the FETCH path runs under `env -i` with this
# PATH and no LEFTHOOK_BIN, guaranteeing resolution falls through to tier 4.
CLEANPATH="/usr/bin:/bin:/usr/sbin:/sbin"

# sha256 of a file, matching the lib's tool detection.
sha_of(){ if command -v shasum >/dev/null 2>&1; then shasum -a 256 "$1" | awk '{print $1}'; else sha256sum "$1" | awk '{print $1}'; fi; }

# The version the lib pins — read it from the lib so the test never drifts from it.
VER="$(. "$LIB"; echo "$LEFTHOOK_VERSION")"

# ---------- Scenario L1: platform -> asset-name mapping ----------
echo "== Scenario L1: uname tokens map to lefthook's OS/ARCH asset name =="
# Drive lefthook_platform with a stubbed uname so the mapping is exercised
# deterministically on whatever host runs the suite. A function named `uname`
# shadows the real binary inside the subshell.
map(){  # $1 = uname -s, $2 = uname -m -> echoes "OS ARCH" or "FAIL"
  ( . "$LIB"
    uname(){ case "$1" in -s) echo "$U_S";; -m) echo "$U_M";; esac; }
    U_S="$1"; U_M="$2"
    if lefthook_platform; then echo "$LH_OS $LH_ARCH"; else echo FAIL; fi )
}
[ "$(map Darwin arm64)"   = "MacOS arm64" ]  && pass "Darwin/arm64 -> MacOS arm64"   || fail "Darwin/arm64 mapping ($(map Darwin arm64))"
[ "$(map Darwin x86_64)"  = "MacOS x86_64" ] && pass "Darwin/x86_64 -> MacOS x86_64" || fail "Darwin/x86_64 mapping ($(map Darwin x86_64))"
[ "$(map Linux aarch64)"  = "Linux arm64" ]  && pass "Linux/aarch64 -> Linux arm64"  || fail "Linux/aarch64 mapping ($(map Linux aarch64))"
[ "$(map Linux amd64)"    = "Linux x86_64" ] && pass "Linux/amd64 -> Linux x86_64"   || fail "Linux/amd64 mapping ($(map Linux amd64))"
[ "$(map FreeBSD amd64)"  = "FAIL" ]         && pass "unknown OS fails gracefully"   || fail "FreeBSD accepted ($(map FreeBSD amd64))"
[ "$(map Linux riscv64)"  = "FAIL" ]         && pass "unknown ARCH fails gracefully" || fail "riscv64 accepted ($(map Linux riscv64))"
# every mapped asset has a baked-in checksum (no platform resolves to an empty hash)
miss=""
for pair in "MacOS arm64" "MacOS x86_64" "Linux arm64" "Linux x86_64"; do
  set -- $pair
  h="$( . "$LIB"; lefthook_sha256_for "lefthook_${VER}_$1_$2" )"
  [ -n "$h" ] || miss="$miss lefthook_${VER}_$1_$2"
done
[ -z "$miss" ] && pass "every supported asset has a baked-in checksum" || fail "missing checksums:$miss"

# ---------- Scenario L2: fetch happy path (download->verify->chmod->cache) ----------
echo "== Scenario L2: fetch downloads, verifies, chmods, atomically caches =="
# Build a fixture "lefthook binary" and serve it from a local base URL. Its real
# sha256 won't equal the baked-in value, so we override lefthook_sha256_for in the
# subshell to return the fixture's actual hash — exercising the verify path against
# a known-good digest with NO network and NO real binary.
BASE="$TMP/base/v$VER"; mkdir -p "$BASE"
# Determine THIS host's asset name from the lib's own platform detection.
ASSET="$( . "$LIB"; lefthook_platform && echo "lefthook_${VER}_${LH_OS}_${LH_ARCH}" || echo UNSUPPORTED )"
if [ "$ASSET" = "UNSUPPORTED" ]; then
  echo "  SKIP: host platform unsupported by the fetcher — L2/L3/L5 need a host asset name"
else
  printf '#!/bin/sh\necho fixture-lefthook "$@"\n' > "$BASE/$ASSET"
  GOODHASH="$(sha_of "$BASE/$ASSET")"
  OUT="$( env -i HOME="$FAKEHOME" XDG_CACHE_HOME="$CACHEHOME" PATH="$CLEANPATH" \
    OMAKASE_LEFTHOOK_BASE_URL="$TMP/base/v$VER" \
    bash -c '
      . "'"$LIB"'"
      lefthook_sha256_for(){ echo "'"$GOODHASH"'"; }
      ROOT="'"$TMP"'/norepo"   # no node_modules here
      if resolve_lefthook fetch; then echo "RESOLVED:$LEFTHOOK"; else echo FAILED; fi
    ' 2>&1 )"
  CACHED="$CACHEHOME/omakase/lefthook/$VER/lefthook"
  echo "$OUT" | grep -q "RESOLVED:$CACHED" && pass "resolve_lefthook fetched and pointed at the cache" || fail "fetch did not resolve to the cache ($OUT)"
  [ -f "$CACHED" ] && pass "binary cached at the per-machine path" || fail "no cached binary at $CACHED"
  [ -x "$CACHED" ] && pass "cached binary is executable (chmod +x ran)" || fail "cached binary not executable"
  [ "$(sha_of "$CACHED")" = "$GOODHASH" ] && pass "cached bytes match the verified download" || fail "cached bytes differ from the source"
  ls "$CACHEHOME/omakase/lefthook/$VER/".lefthook.download.* >/dev/null 2>&1 && fail "temp download file left behind" || pass "no temp download residue"
  # reuse: a second resolve with the REAL (mismatching) checksum table still finds
  # the cached binary directly (tier-4 cache hit precedes any fetch), proving one
  # download per machine.
  OUT2="$( env -i HOME="$FAKEHOME" XDG_CACHE_HOME="$CACHEHOME" PATH="$CLEANPATH" bash -c '
      . "'"$LIB"'"; ROOT="'"$TMP"'/norepo"
      if resolve_lefthook; then echo "RESOLVED:$LEFTHOOK"; else echo FAILED; fi' 2>&1 )"
  echo "$OUT2" | grep -q "RESOLVED:$CACHED" && pass "cached binary reused with no fetch (one download per machine)" || fail "cache not reused ($OUT2)"
fi

# ---------- Scenario L3: checksum mismatch is rejected, nothing cached ----------
echo "== Scenario L3: a download that fails verification is rejected =="
if [ "$ASSET" != "UNSUPPORTED" ]; then
  MMHOME="$TMP/home-mm"; MMCACHE="$TMP/cache-mm"; mkdir -p "$MMHOME" "$MMCACHE"
  # Serve a fixture whose bytes do NOT match the baked-in checksum (default table).
  printf 'totally-wrong-bytes\n' > "$BASE/$ASSET.bad"
  cp "$BASE/$ASSET.bad" "$BASE/$ASSET"   # base now serves wrong bytes for the real asset
  OUT="$( env -i HOME="$MMHOME" XDG_CACHE_HOME="$MMCACHE" PATH="$CLEANPATH" \
    OMAKASE_LEFTHOOK_BASE_URL="$TMP/base/v$VER" \
    bash -c '. "'"$LIB"'"; ROOT="'"$TMP"'/norepo"
      if resolve_lefthook fetch; then echo "RESOLVED:$LEFTHOOK"; else echo FAILED; fi' 2>&1 )"
  echo "$OUT" | grep -q FAILED && pass "checksum mismatch -> resolve_lefthook fails" || fail "mismatch was accepted ($OUT)"
  echo "$OUT" | grep -qi 'checksum mismatch' && pass "mismatch is reported" || fail "no mismatch message ($OUT)"
  [ ! -e "$MMCACHE/omakase/lefthook/$VER/lefthook" ] && pass "nothing cached on mismatch" || fail "a binary was cached despite the mismatch"
  ls "$MMCACHE/omakase/lefthook/$VER/".lefthook.download.* >/dev/null 2>&1 && fail "temp download left behind on mismatch" || pass "no temp residue on mismatch"
fi

# ---------- Scenario L4: graceful fallback through init.sh ----------
echo "== Scenario L4: init exits non-zero with guidance and places nothing on fetch failure =="
# A repo with NO lefthook reachable: empty PATH of lefthook, no node_modules, no
# LEFTHOOK_BIN, and a base URL that serves nothing -> the fetch fails and init must
# fall back to the guidance and exit before any mutation.
REPO="$TMP/repoL4"; rm -rf "$REPO"; mkdir -p "$REPO"
( cd "$REPO" && git init -q && git config user.email t@t && git config user.name t && git config commit.gpgsign false && git commit -q --allow-empty -m init )
PAYENV="$TMP/payloadL4"; mkdir -p "$PAYENV"; printf 'marker\n' > "$PAYENV/MARK.md"
# A PRISTINE cache + home so tier 4 has no already-cached binary: the only way to a
# lefthook here is the fetch, and the empty base URL makes that fail -> fall back.
L4HOME="$TMP/home-L4"; L4CACHE="$TMP/cache-L4"; mkdir -p "$L4HOME"
OUT="$( cd "$REPO" && \
  env -i HOME="$L4HOME" XDG_CACHE_HOME="$L4CACHE" PATH="$CLEANPATH" \
    OMAKASE_PAYLOAD="$PAYENV" \
    OMAKASE_LEFTHOOK_BASE_URL="$TMP/base-empty/v$VER" \
    bash "$INIT" 2>&1 )"; rc=$?
[ "$rc" -ne 0 ] && pass "init exits non-zero when lefthook can't be fetched" || fail "init exited 0 with no lefthook"
echo "$OUT" | grep -qi 'lefthook not found' && pass "the install guidance is printed" || fail "no guidance message ($OUT)"
[ ! -e "$REPO/MARK.md" ] && pass "payload NOT placed (resolution ran before any mutation)" || fail "init placed files despite the lefthook failure"
[ -z "$(cd "$REPO" && git status --porcelain)" ] && pass "git status clean (nothing half-installed)" || { fail "repo dirtied on failure"; (cd "$REPO" && git status --porcelain | sed 's/^/      /'); }
grep -q 'omakase-harness' "$REPO/.git/info/exclude" 2>/dev/null && fail "exclude block written on failure" || pass "no exclude block on failure"
[ ! -e "$REPO/.git/omakase" ] && pass "no shared omakase dir created on failure" || fail "shared omakase dir created on failure"

# ---------- Scenario L5: remove never fetches but uses a cached binary ----------
echo "== Scenario L5: legacy remove resolves via the cache (no fetch) =="
if [ "$ASSET" != "UNSUPPORTED" ]; then
  # Seed the cache with a stub lefthook so remove can resolve it; remove must NOT
  # need the network. The stub answers `uninstall` (and `install`) as a no-op.
  # The repo carries a pre-#98 install's shape — an $OMK dir (the install-proof
  # sentinel) plus a lefthook stub hook with omakase's guard marker — because only
  # that marker evidence triggers remove's legacy `lefthook uninstall` path (a
  # current-scheme remove never spawns lefthook at all).
  RMHOME="$TMP/home-rm"; RMCACHE="$TMP/cache-rm"; mkdir -p "$RMHOME"
  STUBDIR="$RMCACHE/omakase/lefthook/$VER"; mkdir -p "$STUBDIR"
  printf '#!/bin/sh\necho "stub-lefthook $*"\nexit 0\n' > "$STUBDIR/lefthook"; chmod +x "$STUBDIR/lefthook"
  RREPO="$TMP/repoL5"; rm -rf "$RREPO"; mkdir -p "$RREPO"
  ( cd "$RREPO" && git init -q && git config user.email t@t && git config user.name t && git config commit.gpgsign false && git commit -q --allow-empty -m init )
  mkdir -p "$RREPO/.git/omakase" "$RREPO/.git/hooks"
  printf '#!/bin/sh\n# >>> omakase-harness fail-closed >>>\n# guard\n# <<< omakase-harness fail-closed <<<\ncall_lefthook run "pre-commit" "$@"\n' > "$RREPO/.git/hooks/pre-commit"
  chmod +x "$RREPO/.git/hooks/pre-commit"
  # No lefthook on PATH, no node_modules, no LEFTHOOK_BIN, no base URL (offline):
  # resolve must still find the cached stub and run `<cache>/lefthook uninstall`.
  OUT="$( cd "$RREPO" && \
    env -i HOME="$RMHOME" XDG_CACHE_HOME="$RMCACHE" PATH="$CLEANPATH" \
      bash "$REMOVE" 2>&1 )"; rc=$?
  [ "$rc" -eq 0 ] && pass "remove succeeded using the cached binary" || fail "remove failed ($OUT)"
  echo "$OUT" | grep -q 'stub-lefthook uninstall' && pass "remove invoked '<cache>/lefthook uninstall'" || fail "cached binary not used for uninstall ($OUT)"
fi

# ---------- Scenario L6: opt-in live fetch from GitHub ----------
echo "== Scenario L6: live fetch from GitHub (opt-in: OMAKASE_TEST_LIVE_FETCH=1) =="
if [ "${OMAKASE_TEST_LIVE_FETCH:-}" = "1" ]; then
  LHOME="$TMP/home-live"; LCACHE="$TMP/cache-live"; mkdir -p "$LHOME"
  OUT="$( env -i HOME="$LHOME" XDG_CACHE_HOME="$LCACHE" PATH="$CLEANPATH" bash -c '
      . "'"$LIB"'"; ROOT="'"$TMP"'/norepo"
      if resolve_lefthook fetch; then echo "RESOLVED:$LEFTHOOK"; else echo FAILED; fi' 2>&1 )"
  LCACHED="$LCACHE/omakase/lefthook/$VER/lefthook"
  echo "$OUT" | grep -q "RESOLVED:$LCACHED" && pass "live: real binary fetched + checksum-verified into the cache" || fail "live fetch failed ($OUT)"
  [ -x "$LCACHED" ] && "$LCACHED" version >/dev/null 2>&1 && pass "live: fetched binary actually runs ('lefthook version')" || fail "live: fetched binary does not run"
else
  echo "  SKIP: set OMAKASE_TEST_LIVE_FETCH=1 to exercise a real download"
fi

rm -rf "$TMP"
echo ""
[ "$FAILED" -eq 0 ] && echo "ALL PASS" || { echo "FAILURES PRESENT"; exit 1; }
