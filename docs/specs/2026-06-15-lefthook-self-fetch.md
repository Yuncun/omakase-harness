# Spec: lefthook self-provisioning (2026-06-15)

## Problem

`init.sh` hard-exits when lefthook is absent (`resolve_lefthook`, the `|| { … exit 1; }`
near line 138), so a harness cannot place its checks unless the adopter first installs
lefthook. That breaks the "one command in, nothing else installed" goal.

## Change

Add a fetch tier to lefthook resolution: when lefthook is not found on PATH /
`LEFTHOOK_BIN` / `node_modules`, download a **pinned, checksum-verified** lefthook binary
into a per-machine cache and use it. On any failure, fall back to today's guidance
message — never worse than the current behavior.

## Resolution order (shared by `init.sh` and `remove.sh`)

1. `LEFTHOOK_BIN` override.
2. `lefthook` on PATH.
3. `$ROOT/node_modules/.bin/lefthook`.
4. The omakase-managed cached binary — **fetch if absent (init only; remove never fetches).**

## Fetch

- Pinned `LEFTHOOK_VERSION=2.1.9`.
- Cache: `${XDG_CACHE_HOME:-$HOME/.cache}/omakase/lefthook/<version>/lefthook` (one
  download per machine; mirrors the existing sources cache root).
- Platform detection: `uname -s` → `Darwin`=MacOS, `Linux`=Linux; `uname -m` →
  `arm64|aarch64`→arm64, `x86_64|amd64`→x86_64. Anything else → graceful fail.
- Asset name `lefthook_${VERSION}_${OS}_${ARCH}`; URL
  `https://github.com/evilmartians/lefthook/releases/download/v${VERSION}/${ASSET}`.
- Download with `curl` (fallback `wget`) to a temp file; verify SHA256 against the
  baked-in expected value for that platform; reject on mismatch; `chmod +x`; atomically
  move into the cache path.
- **Any failure** (no fetcher, no network, unknown platform, checksum mismatch) → print
  the existing install guidance and return non-zero. `init` then exits without a partial
  install (resolution already runs before any mutation).

### Baked-in SHA256 (lefthook v2.1.9)

| asset | sha256 |
|-------|--------|
| `lefthook_2.1.9_Linux_arm64`  | `304321997336c450af6b5c0cc641c59141168866fca0b1fc3767e067812600a9` |
| `lefthook_2.1.9_Linux_x86_64` | `0d60b0d350c923963729574f6431171f0277788884ad0c6284fa0160c36e3877` |
| `lefthook_2.1.9_MacOS_arm64`  | `fd506e05954af2062ce320d59ac1f5bf13fad8d694694a72bc6ef91e8c284e3d` |
| `lefthook_2.1.9_MacOS_x86_64` | `0868b9b5b9cd807b0f9e0135fadaff1bd99fa026cccc15cbfd4510f0ee3b5431` |

(Windows is omitted — git hooks run under bash.)

## Testability

- `OMAKASE_LEFTHOOK_BASE_URL` overrides the GitHub base so tests serve a fixture binary
  from a local path and exercise download→verify→cache→chmod deterministically, no
  network.
- `tests/lefthook-fetch.test.sh`: platform→asset mapping; checksum-mismatch rejected;
  graceful fallback returns guidance + non-zero and leaves nothing half-installed; one
  live fetch gated behind `OMAKASE_TEST_LIVE_FETCH=1`.
- CI (ubuntu + macos) runs the deterministic suite; the live test is opt-in.
- bash-3.2 safe (macOS ships 3.2; CI runs both) — no `${arr[@]:-}` length-with-default,
  no associative arrays.

## `remove.sh`

Resolve lefthook via the same order (no fetch) so `lefthook uninstall` works when
lefthook lives only in the cache. The existing `$COMMON/omakase` teardown already
neutralizes the fail-closed guard; ensure no orphan hook stub is left behind.

## Code shape

`resolve_lefthook` + the fetch live in one place shared by `init.sh` and `remove.sh`
(a sourced `bin/lib-lefthook.sh`, vendored alongside the other scripts), rather than
duplicated. If a sourced lib is rejected for the standalone-script constraint, duplicate
with a sync comment (the `kind_of` precedent).

## Docs

Update `commands/omakase.md` and `README`: init now provisions lefthook automatically;
the "ask the user how to install" flow becomes the fallback when the fetch fails.

## Reversibility / maintenance

The cache is per-machine and disposable (not system-wide; the repo is untouched), so it
is far cleaner than a global `brew install`. Re-pinning = bump `LEFTHOOK_VERSION` and
replace the four hashes from that tag's `lefthook_checksums.txt`.

## Out of scope

Writing our own git hooks to drop lefthook entirely — a future option if a zero-fetch
path is ever wanted.
