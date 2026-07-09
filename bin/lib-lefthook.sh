# shellcheck shell=bash
# omakase-harness — shared lefthook resolution + self-provisioning.
# Sourced by bin/init.sh and bin/remove.sh (the only two scripts that drive
# `lefthook install` / `lefthook uninstall`). NOT executed directly: it defines
# functions and runs nothing at source time. The sourcing scripts own
# `set -euo pipefail`; everything here is safe under `set -u` (no arrays, every
# expansion guarded).
#
# resolve_lefthook sets $LEFTHOOK to a runnable lefthook invocation in this order:
#   1. $LEFTHOOK_BIN override.
#   2. `lefthook` on PATH (a global brew/mise install).
#   3. $ROOT/node_modules/.bin/lefthook (a JS devDependency — the common case).
#   4. The omakase-managed cached binary — fetched (init only) if absent.
# We do NOT mutate the user's global environment: the cache is per-machine and
# disposable (see fetch_lefthook), so it is reversible in a way a global
# `brew install` is not. Tier 4 fetch is opt-in via the caller (init passes 1;
# remove passes nothing — uninstall must never reach for the network).

# Pinned lefthook release. Re-pinning: bump this and replace the four hashes in
# lefthook_sha256_for() from that tag's lefthook_checksums.txt.
LEFTHOOK_VERSION="2.1.9"

# The install guidance printed on any resolution failure (no lefthook anywhere
# and — for init — the fetch could not deliver one). Kept verbatim from the
# original init.sh message so behavior is never worse than before this change.
lefthook_install_guidance() {
  echo "omakase: lefthook not found and could not be fetched. Install it (e.g. 'brew install lefthook', 'mise use lefthook', or add it as a devDependency and run your package manager's install), or set LEFTHOOK_BIN=/path/to/lefthook, then re-run." >&2
}

# Baked-in SHA256 for each lefthook v2.1.9 asset (from the release's
# lefthook_checksums.txt). Windows is omitted — git hooks run under bash.
# A case block, not an associative array, for bash 3.2.
lefthook_sha256_for() {  # $1 = asset name; echoes the expected sha256, empty if unknown
  case "$1" in
    lefthook_2.1.9_Linux_arm64)  echo "304321997336c450af6b5c0cc641c59141168866fca0b1fc3767e067812600a9";;
    lefthook_2.1.9_Linux_x86_64) echo "0d60b0d350c923963729574f6431171f0277788884ad0c6284fa0160c36e3877";;
    lefthook_2.1.9_MacOS_arm64)  echo "fd506e05954af2062ce320d59ac1f5bf13fad8d694694a72bc6ef91e8c284e3d";;
    lefthook_2.1.9_MacOS_x86_64) echo "0868b9b5b9cd807b0f9e0135fadaff1bd99fa026cccc15cbfd4510f0ee3b5431";;
    *) echo "";;
  esac
}

# Map uname output to lefthook's asset OS/ARCH tokens; sets $LH_OS and $LH_ARCH.
# Returns non-zero for any platform we have no baked-in hash for (graceful fail).
lefthook_platform() {
  local s m
  s="$(uname -s 2>/dev/null || echo)"
  m="$(uname -m 2>/dev/null || echo)"
  case "$s" in
    Darwin) LH_OS="MacOS";;
    Linux)  LH_OS="Linux";;
    *)      return 1;;
  esac
  case "$m" in
    arm64|aarch64) LH_ARCH="arm64";;
    x86_64|amd64)  LH_ARCH="x86_64";;
    *)             return 1;;
  esac
  return 0
}

# sha256 of a file via whichever digest tool exists (shasum on macOS,
# sha256sum elsewhere); echoes the bare hex digest, or nothing if neither tool
# is present (caller treats an empty actual as a mismatch and rejects).
# The tool re-detection here is DELIBERATE, not stray duplication: this lib is
# self-contained and cannot assume init.sh's $SHA256 array crosses the source
# boundary (remove.sh never defines it). Do NOT consolidate it away.
lefthook_sha256_file() {  # $1 = file
  if command -v shasum >/dev/null 2>&1; then shasum -a 256 "$1" | awk '{print $1}'
  elif command -v sha256sum >/dev/null 2>&1; then sha256sum "$1" | awk '{print $1}'
  else echo; fi
}

# Download $1 to $2 with curl (fallback wget). Supports a plain local path or a
# file:// URL (the test fixture path) by copying — curl/wget both handle file://
# but a bare local path does not, and tests pass a bare path. Returns non-zero
# if no fetcher is available or the transfer fails.
lefthook_download() {  # $1 = url-or-path, $2 = dest
  local url="$1" dest="$2" src
  case "$url" in
    file://*) src="${url#file://}"; [ -f "$src" ] && { cp "$src" "$dest"; return $?; }; return 1;;
    /*)       [ -f "$url" ] && { cp "$url" "$dest"; return $?; }; return 1;;
  esac
  # --connect-timeout bounds only the CONNECTION phase: an offline or
  # black-holed first run fails in ~5s instead of hanging on the OS connect
  # timeout, while a slow-but-connected download still runs to completion.
  # wget -T is the portable near-equivalent (per-phase timeout, busybox-safe).
  if command -v curl >/dev/null 2>&1; then
    curl -fsSL --connect-timeout 5 "$url" -o "$dest"
  elif command -v wget >/dev/null 2>&1; then
    wget -q -T 15 -O "$dest" "$url"
  else
    return 1
  fi
}

# Fetch the pinned lefthook into the per-machine cache and set $LEFTHOOK to it.
# One download per machine: the cached binary is reused on every later run. Any
# failure (unknown platform, no fetcher, transfer error, checksum mismatch)
# returns non-zero and leaves nothing in the cache, so the caller falls back to
# the install guidance without a half-installed binary.
#
# Cache: ${XDG_CACHE_HOME:-$HOME/.cache}/omakase/lefthook/<version>/lefthook —
# mirrors the sources-cache root style in init.sh's fetch_source.
# Base URL: OMAKASE_LEFTHOOK_BASE_URL overrides the GitHub releases base so a
# test can serve a fixture binary from a local path with no network.
fetch_lefthook() {
  local cache_dir cache_bin asset base url tmp expected actual
  if ! lefthook_platform; then
    echo "omakase: lefthook self-fetch unsupported on this platform ($(uname -s 2>/dev/null)/$(uname -m 2>/dev/null))." >&2
    return 1
  fi
  asset="lefthook_${LEFTHOOK_VERSION}_${LH_OS}_${LH_ARCH}"
  expected="$(lefthook_sha256_for "$asset")"
  if [ -z "$expected" ]; then
    echo "omakase: no baked-in checksum for $asset — refusing to fetch." >&2
    return 1
  fi
  cache_dir="${XDG_CACHE_HOME:-$HOME/.cache}/omakase/lefthook/${LEFTHOOK_VERSION}"
  cache_bin="$cache_dir/lefthook"
  # Already cached and verified-good earlier? Re-verify cheaply before trusting it
  # (a truncated/corrupt cache should not silently win), then reuse.
  if [ -x "$cache_bin" ]; then
    actual="$(lefthook_sha256_file "$cache_bin")"
    if [ -n "$actual" ] && [ "$actual" = "$expected" ]; then LEFTHOOK="$cache_bin"; return 0; fi
    rm -f "$cache_bin"   # corrupt — drop it and re-fetch
  fi
  base="${OMAKASE_LEFTHOOK_BASE_URL:-https://github.com/evilmartians/lefthook/releases/download/v${LEFTHOOK_VERSION}}"
  url="$base/$asset"
  mkdir -p "$cache_dir" || return 1
  tmp="$cache_dir/.lefthook.download.$$"
  rm -f "$tmp"
  if ! lefthook_download "$url" "$tmp"; then
    echo "omakase: could not download lefthook from $url" >&2
    rm -f "$tmp"
    return 1
  fi
  actual="$(lefthook_sha256_file "$tmp")"
  if [ -z "$actual" ]; then
    echo "omakase: no shasum/sha256sum available to verify the lefthook download — refusing it." >&2
    rm -f "$tmp"
    return 1
  fi
  if [ "$actual" != "$expected" ]; then
    echo "omakase: lefthook checksum mismatch for $asset (expected $expected, got $actual) — refusing it." >&2
    rm -f "$tmp"
    return 1
  fi
  chmod +x "$tmp" || { rm -f "$tmp"; return 1; }
  mv -f "$tmp" "$cache_bin" || { rm -f "$tmp"; return 1; }   # atomic within the cache dir
  LEFTHOOK="$cache_bin"
  return 0
}

# Resolve lefthook, setting $LEFTHOOK. $1 = "fetch" enables tier 4's network
# fetch (init passes it; remove does not — uninstall stays offline but still
# uses an already-cached binary). Returns non-zero when nothing resolves.
resolve_lefthook() {
  local allow_fetch="${1:-}"
  if [ -n "${LEFTHOOK_BIN:-}" ];                then LEFTHOOK="$LEFTHOOK_BIN"; return 0; fi
  if command -v lefthook >/dev/null 2>&1;        then LEFTHOOK="lefthook"; return 0; fi
  if [ -x "$ROOT/node_modules/.bin/lefthook" ];  then LEFTHOOK="$ROOT/node_modules/.bin/lefthook"; return 0; fi
  # Tier 4: the omakase-managed cache. Reuse an existing cached binary even when
  # fetch is off (remove must uninstall via the cache when it is the only copy);
  # only init (allow_fetch=fetch) may reach out to download a missing one.
  local cache_bin="${XDG_CACHE_HOME:-$HOME/.cache}/omakase/lefthook/${LEFTHOOK_VERSION}/lefthook"
  if [ -x "$cache_bin" ]; then LEFTHOOK="$cache_bin"; return 0; fi
  if [ "$allow_fetch" = "fetch" ]; then
    fetch_lefthook && return 0
  fi
  return 1
}
