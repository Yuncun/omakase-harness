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
# Later scenarios (O5-O8: shim wiring through init/status/remove/mcp) belong
# to a later task and are not covered here.
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

rm -rf "$TMP"
echo ""
[ "$FAILED" -eq 0 ] && echo "ALL PASS" || { echo "FAILURES PRESENT"; exit 1; }
