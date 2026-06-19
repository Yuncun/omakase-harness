# Spec: engine-as-dependency (2026-06-18)

## Problem

A downstream harness (e.g. `omakase-android`) **vendors a hand-copy of the engine** —
`omakase/bin/*.sh`, including `lib-harness-paths.sh`. When the base engine changes, the
copy silently rots until someone mirrors it by hand. Concrete: the 2026-06-18 Copilot-hook
recognition fix had to be committed *twice* — once to base `bin/lib-harness-paths.sh`, once
to the android copy — or the two would disagree about what a `.github/hooks` file even is.

This is the same smell the lefthook spec already conceded ("duplicate with a sync comment —
the `kind_of` precedent"). The vendored-`bin/` copy is that workaround scaled to the whole
engine. We want base engine updates to be **adopted by a version bump, not a re-copy.**

## Goal

An implementer depends on a **pinned** engine and adopts base updates by changing one line —
without giving up omakase's "nothing system-wide installed, repo stays clean" contract.

## The split this rests on

omakase is two layers; only one is shared:

| Layer | Files | Owner |
|-------|-------|-------|
| **Engine** | `bin/` — `init`, `import`, `show`, `remove`, `lib-harness-paths.sh`, `lib-lefthook.sh` | base — identical for everyone → **the dependency** |
| **Payload** | the implementer's `payload/`, `skills/`, `commands/` | the implementer — stays committed |

Drift only ever occurs in the engine. The payload is legitimately per-implementer, so it is
never fetched — only the engine is. The folder/contract the payload follows is *defined by*
the engine you pin, which is why `lib-harness-paths.sh` (100% engine) should adopt for free.

## Design: a wrapper, mirroring the Gradle wrapper + our own lefthook self-fetch

Replace the vendored `omakase/bin/` with two committed artifacts:

1. **`omakasew`** — a ~40-line bootstrap (the only committed engine code). It resolves the
   pinned engine from cache, fetching if absent, then `exec`s the requested real script
   (`init`/`import`/`show`/`remove`) from the cache. Same role as `gradlew`.
2. **`omakase-engine.lock`** — pins `ENGINE_VERSION` + the tarball SHA256. Same role as
   `gradle-wrapper.properties`' `distributionUrl`.

### Resolution order (in `omakasew`)
1. `OMAKASE_ENGINE_DIR` override (local dev against a working tree).
2. The cached engine for the locked version.
3. Fetch it (see below), then use it.

### Fetch — reuse the lefthook-fetch machinery verbatim
- Cache root already exists: `${XDG_CACHE_HOME:-$HOME/.cache}/omakase/` (the lefthook spec's
  root). Engine lands at `…/omakase/engine/<version>/bin/`.
- Asset: `omakase-engine-<version>.tar.gz`, attached to the base GitHub release for that tag;
  URL `https://github.com/Yuncun/omakase-harness/releases/download/v<version>/<asset>`.
- `curl` (fallback `wget`) to a temp file → verify SHA256 against `omakase-engine.lock` →
  extract → atomically move into the cache. **Any failure** (no fetcher, no net, checksum
  mismatch) → print guidance and exit non-zero before any repo mutation, exactly like the
  lefthook fallback. `OMAKASE_ENGINE_BASE_URL` overrides the host for deterministic tests.
- Two-level fetch is consistent: `omakasew` fetches the engine; the engine then fetches
  lefthook — both pinned, checksum-verified, under the same cache root.

## Adoption flow (the whole point)

```
# implementer adopts a base update:
edit omakase-engine.lock   # ENGINE_VERSION=0.12.1 -> 0.13.0  (+ new sha256)
./omakasew init            # re-resolves, fetches the new engine, re-injects
```

Today's recognition fix would have arrived on that `lock` bump — no android mirror commit.

## Release side (base)

Tagging a release builds `omakase-engine-<version>.tar.gz` (just `bin/`) + emits its SHA256
into the release notes, so an implementer can copy the two values into their `lock`. A
`bin/cut-release.sh` (or a CI job on tag) does the tar + hash; no manual checksum bookkeeping.

## Alternatives considered

- **git submodule** — real pinning, but submodules are a chronic chore (detached HEAD,
  `--init` forgotten) and nesting one *inside an injected payload* compounds it.
- **git subtree** — files are really present (no clone step), but `subtree pull` is arcane
  and still a manual mirror, not a version bump.
- **wrapper + pinned fetch (this spec)** — wins because it is the pattern the repo *already*
  runs for lefthook and the one every contributor already knows from Gradle.

## Testability

- `OMAKASE_ENGINE_BASE_URL` serves a fixture tarball from a local path → exercise
  resolve→fetch→verify→extract→exec with no network.
- `tests/engine-fetch.test.sh`: lock parsing; checksum-mismatch rejected; graceful fallback
  leaves the repo untouched; `OMAKASE_ENGINE_DIR` override short-circuits the fetch; one live
  fetch gated behind `OMAKASE_TEST_LIVE_FETCH=1`.
- bash-3.2 safe (macOS ships 3.2), same constraint as the rest of the engine.

## Reversibility / maintenance

Cache is per-machine and disposable; the repo only ever holds `omakasew` + the lock + the
payload. Re-pinning is one edit. Deleting the cache forces a clean re-fetch. An implementer
who wants to freeze simply never bumps the lock — identical to today, minus the silent rot.

## Out of scope

- Auto-bumping the lock (a renovate-style PR bot) — a later convenience once releases exist.
- Distributing the engine via the Claude/Copilot plugin marketplace — different axis (that
  ships skills/commands, not the engine runtime).
