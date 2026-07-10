# shellcheck shell=bash
# omakase-harness — resolution + self-provisioning of the omakase Go binary.
# Sourced by bin/init.sh, bin/status.sh, bin/remove.sh, bin/mcp.sh. NOT executed
# directly: it defines functions and runs nothing at source time. The sourcing
# scripts own `set -euo pipefail`; everything here is safe under `set -u`.
# Callers must set $HERE (their bin/ directory) before calling resolve_omakase.
#
# resolve_omakase sets $OMAKASE_BIN_RESOLVED to a runnable omakase in this order:
#   1. $OMAKASE_BIN override (tests, CI) — must be executable, or resolution
#      fails immediately (no fallthrough to tiers 2-6).
#   2. Dev rebuild: go.mod + go on PATH -> go build (a FAILING build aborts loudly
#      via an explicit exit — set -e alone would be suppressed here, since this
#      runs inside an if-condition's call chain — because a stale binary would
#      mask Go breakage).
#   3. dist/omakase — a prebuilt/vendored copy.
#   4. `omakase` on PATH (brew or manual install).
#   5. The omakase-managed cached binary — fetched (tier 6, opt-in via $1=fetch)
#      if absent. remove.sh never passes fetch: uninstall stays offline.
# The fetch mirrors bin/lib-lefthook.sh: pinned version, baked sha256s, one
# download per machine into ${XDG_CACHE_HOME:-$HOME/.cache}/omakase/bin/<ver>/.
#
# resolve_omakase also exports OMAKASE_BASE_PAYLOAD (the plugin's own
# bin/../payload) so the binary can find the --source merge base even when it
# runs from a cache dir / PATH with no payload/ sibling (since v0.18.0). A
# pre-set OMAKASE_BASE_PAYLOAD always wins.

# Pinned omakase release. Re-pinning: bump this, replace the four archive hashes
# from that release's checksums.txt, and regenerate the four binary hashes
# (docs/releasing.md has the loop).
OMAKASE_PIN_VERSION="0.18.1"

# Baked SHA256 of each release ARCHIVE (verbatim from the published checksums.txt).
omakase_archive_sha256_for() {  # $1 = asset file name; echoes expected sha256, empty if unknown
  case "$1" in
    omakase_0.18.1_darwin_amd64.tar.gz) echo "9a691e89585288b61462d7df6fe772e2d4a7b0cdaa8e0ce68740af63367386d3";;
    omakase_0.18.1_darwin_arm64.tar.gz) echo "a3b6fd7536555b4ed2276ca065ae36a6ccf86d3fe4e328632add49ef9f6c8a0b";;
    omakase_0.18.1_linux_amd64.tar.gz)  echo "4995201f398960ec76bd98242e74e773a61735c3b7faa45e5f82a03dd8adda45";;
    omakase_0.18.1_linux_arm64.tar.gz)  echo "c9fc282c5bcd16fcf3e63b9e96d379dfba05c0e7b3fabc0d9fdc409e49d74bcc";;
    *) echo "";;
  esac
}

# Baked SHA256 of the EXTRACTED `omakase` binary per asset stem. Verified after
# extraction (validates the unpack) and lets an already-cached binary be
# re-verified against a repo-held digest before reuse in fetch_omakase.
omakase_bin_sha256_for() {  # $1 = asset stem; echoes expected sha256, empty if unknown
  case "$1" in
    omakase_0.18.1_darwin_amd64) echo "67b7256eac7bd171b1505bb9fd4f41cab19aa87ba8fbe5867054441ad2497b65";;
    omakase_0.18.1_darwin_arm64) echo "720684c4670efc74ad8a8ed229055628260db8062773abdb2b9b5a0f562be697";;
    omakase_0.18.1_linux_amd64)  echo "4164ab469d769778a68be8f2bccfde80e43ea62a73c995b2775af5bed2bf83c4";;
    omakase_0.18.1_linux_arm64)  echo "2e48820ba9f18f25033bffb9962ceb3fda85f7d1ad9e15913dc3289236d172c3";;
    *) echo "";;
  esac
}

# Map uname output to goreleaser's OS/ARCH tokens; sets $OMK_OS and $OMK_ARCH.
# NOTE: these are goreleaser tokens (darwin/linux, amd64/arm64) — NOT the
# MacOS/x86_64 tokens lefthook uses. Returns non-zero for unsupported platforms.
omakase_platform() {
  local s m
  s="$(uname -s 2>/dev/null || echo)"
  m="$(uname -m 2>/dev/null || echo)"
  case "$s" in
    Darwin) OMK_OS="darwin";;
    Linux)  OMK_OS="linux";;
    *)      return 1;;
  esac
  case "$m" in
    arm64|aarch64) OMK_ARCH="arm64";;
    x86_64|amd64)  OMK_ARCH="amd64";;
    *)             return 1;;
  esac
  return 0
}

# sha256 of a file via whichever digest tool exists (shasum on macOS, sha256sum
# elsewhere); echoes the bare hex digest, or nothing if neither tool is present
# (caller treats an empty actual as a mismatch and rejects). Self-contained on
# purpose, same as lib-lefthook.sh — do NOT consolidate the two libs' copies.
omakase_sha256_file() {  # $1 = file
  if command -v shasum >/dev/null 2>&1; then shasum -a 256 "$1" | awk '{print $1}'
  elif command -v sha256sum >/dev/null 2>&1; then sha256sum "$1" | awk '{print $1}'
  else echo; fi
}

# Download $1 to $2 with curl (fallback wget). Supports a plain local path or a
# file:// URL (the test fixture path) by copying. Returns non-zero if no fetcher
# is available or the transfer fails.
omakase_download() {  # $1 = url-or-path, $2 = dest
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

# Fetch the pinned omakase release into the per-machine cache and set
# $OMAKASE_BIN_RESOLVED. Verify the archive hash BEFORE extraction (never run tar
# on unverified bytes), extract only the `omakase` member, verify the binary
# hash, chmod +x, atomic mv. Any failure returns non-zero, prints one line to
# stderr, and leaves no temp or cache residue.
# Base URL: OMAKASE_RELEASE_BASE_URL overrides GitHub releases (test fixtures).
fetch_omakase() {
  local stem asset cache_dir cache_bin base url tmp_archive tmp_dir expected actual
  if ! omakase_platform; then
    echo "omakase: binary self-fetch unsupported on this platform ($(uname -s 2>/dev/null)/$(uname -m 2>/dev/null))." >&2
    return 1
  fi
  stem="omakase_${OMAKASE_PIN_VERSION}_${OMK_OS}_${OMK_ARCH}"
  asset="$stem.tar.gz"
  if [ -z "$(omakase_archive_sha256_for "$asset")" ] || [ -z "$(omakase_bin_sha256_for "$stem")" ]; then
    echo "omakase: no baked-in checksum for $asset — refusing to fetch." >&2
    return 1
  fi
  cache_dir="${XDG_CACHE_HOME:-$HOME/.cache}/omakase/bin/${OMAKASE_PIN_VERSION}"
  cache_bin="$cache_dir/omakase"
  # Already cached? Re-verify against the baked binary digest before trusting it.
  if [ -x "$cache_bin" ]; then
    actual="$(omakase_sha256_file "$cache_bin")"
    if [ -n "$actual" ] && [ "$actual" = "$(omakase_bin_sha256_for "$stem")" ]; then
      OMAKASE_BIN_RESOLVED="$cache_bin"; return 0
    fi
    rm -f "$cache_bin"   # corrupt — drop it and re-fetch
  fi
  base="${OMAKASE_RELEASE_BASE_URL:-https://github.com/Yuncun/omakase-harness/releases/download/v${OMAKASE_PIN_VERSION}}"
  url="$base/$asset"
  mkdir -p "$cache_dir" || return 1
  tmp_archive="$cache_dir/.omakase.download.$$"
  tmp_dir="$cache_dir/.omakase.extract.$$"
  rm -rf "$tmp_archive" "$tmp_dir"
  if ! omakase_download "$url" "$tmp_archive"; then
    echo "omakase: could not download omakase from $url" >&2
    rm -f "$tmp_archive"
    return 1
  fi
  actual="$(omakase_sha256_file "$tmp_archive")"
  expected="$(omakase_archive_sha256_for "$asset")"
  if [ -z "$actual" ]; then
    echo "omakase: no shasum/sha256sum available to verify the omakase download — refusing it." >&2
    rm -f "$tmp_archive"
    return 1
  fi
  if [ "$actual" != "$expected" ]; then
    echo "omakase: archive checksum mismatch for $asset (expected $expected, got $actual) — refusing it." >&2
    rm -f "$tmp_archive"
    return 1
  fi
  mkdir -p "$tmp_dir" || { rm -f "$tmp_archive"; return 1; }
  if ! tar -xzf "$tmp_archive" -C "$tmp_dir" omakase 2>/dev/null || [ ! -f "$tmp_dir/omakase" ]; then
    echo "omakase: could not extract the omakase binary from $asset — refusing it." >&2
    rm -rf "$tmp_archive" "$tmp_dir"
    return 1
  fi
  rm -f "$tmp_archive"
  actual="$(omakase_sha256_file "$tmp_dir/omakase")"
  expected="$(omakase_bin_sha256_for "$stem")"
  if [ -z "$actual" ] || [ "$actual" != "$expected" ]; then
    echo "omakase: binary checksum mismatch for $stem (expected $expected, got ${actual:-none}) — refusing it." >&2
    rm -rf "$tmp_dir"
    return 1
  fi
  chmod +x "$tmp_dir/omakase" || { rm -rf "$tmp_dir"; return 1; }
  mv -f "$tmp_dir/omakase" "$cache_bin" || { rm -rf "$tmp_dir"; return 1; }   # atomic within the cache dir
  rm -rf "$tmp_dir"
  OMAKASE_BIN_RESOLVED="$cache_bin"
  return 0
}

# Resolve the omakase binary, setting $OMAKASE_BIN_RESOLVED. $1 = "fetch" enables
# tier 6's network fetch (init/status/mcp pass it; remove does not — uninstall
# stays offline but still uses an already-cached binary). Returns non-zero when
# nothing resolves. Requires $HERE = the caller's bin/ directory.
resolve_omakase() {
  local allow_fetch="${1:-}"
  # Hand the base harness payload's location to the binary before resolving it:
  # since v0.18.0 the binary can run from a cache dir / PATH with no payload/
  # sibling, so the --source merge base ($SCRIPT_DIR/../payload in v1) is no
  # longer discoverable binary-relative. Export the plugin's own bin/../payload
  # as a normalized absolute path (it can surface in the binary's error
  # messages). A pre-set value always wins; safe under set -u.
  if [ -z "${OMAKASE_BASE_PAYLOAD:-}" ] && [ -d "$HERE/../payload" ]; then
    export OMAKASE_BASE_PAYLOAD="$(cd "$HERE/.." && pwd)/payload"
  fi
  # An OMAKASE_BIN override short-circuits resolution entirely, same as the
  # pre-bootstrap shims: valid (executable) -> use it; invalid -> fail now
  # rather than falling through to tiers 2-6 (tests rely on this to force a
  # resolution failure deterministically, e.g. OMAKASE_BIN=/nonexistent/omakase).
  if [ -n "${OMAKASE_BIN:-}" ]; then
    if [ -x "${OMAKASE_BIN}" ]; then OMAKASE_BIN_RESOLVED="$OMAKASE_BIN"; return 0; fi
    return 1
  fi
  if [ -f "$HERE/../go.mod" ] && command -v go >/dev/null 2>&1; then
    # resolve_omakase is always called as `if resolve_omakase; then ...` from
    # the shims, and bash suppresses `set -e` throughout an if-condition's call
    # chain — so a failing `go build` here would NOT abort under the caller's
    # set -e and could fall through to a stale dist/omakase. The explicit
    # `exit 1` is immune to that suppression (this file is sourced, so it exits
    # the shim process) — do not simplify this back to a bare command.
    ( cd "$HERE/.." && go build -o dist/omakase ./cmd/omakase ) || exit 1
  fi
  if [ -x "$HERE/../dist/omakase" ]; then OMAKASE_BIN_RESOLVED="$HERE/../dist/omakase"; return 0; fi
  if command -v omakase >/dev/null 2>&1; then OMAKASE_BIN_RESOLVED="omakase"; return 0; fi
  local cache_bin="${XDG_CACHE_HOME:-$HOME/.cache}/omakase/bin/${OMAKASE_PIN_VERSION}/omakase"
  if [ -x "$cache_bin" ]; then OMAKASE_BIN_RESOLVED="$cache_bin"; return 0; fi
  if [ "$allow_fetch" = "fetch" ]; then
    fetch_omakase && return 0
  fi
  return 1
}
