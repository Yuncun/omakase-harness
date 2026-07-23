# Releasing

How omakase ships. This is the single release runbook (CONTRIBUTING.md links
here). Nothing is outward-facing until a maintainer pushes a version tag — and
that one deliberate step publishes: the release, and the Homebrew cask that
points at it, ship in the same unattended run. Review comes before the tag.

## What is wired

- `omakase --version` prints the build metadata. A plain `go build` reports
  `dev`; release builds get the real version, commit, and date injected via
  ldflags.
- `.goreleaser.yaml` cross-compiles `linux`/`darwin` × `amd64`/`arm64`,
  archives each as `tar.gz`, and writes `checksums.txt`.
- `.github/workflows/release.yml` runs on a semver tag push (`vX.Y.Z`),
  re-proves the tagged commit with the same checks as CI (Go vet/test/build +
  every sh suite), then **publishes** the GitHub Release and updates the
  Homebrew cask in `Yuncun/homebrew-tap`.

## Cutting a release

Any change adopters should pick up needs a version bump — the plugin manager
keys off `.claude-plugin/plugin.json`, and the banner / `omakase status` read
`payload/.omakase/VERSION` (see CONTRIBUTING.md for how the two update
channels differ).

1. Bump the version in all **three** stamps — `.claude-plugin/plugin.json`,
   `payload/.omakase/VERSION`, and the `version:` line in
   `payload/omakase.manifest` — they must match the tag. Pre-1.0, a breaking
   change bumps the minor (`0.17.0` → `0.18.0`), a backward-compatible one
   bumps the patch.
2. In `CHANGELOG.md`, rename the `## [Unreleased]` block to
   `## [x.y.z] — YYYY-MM-DD` and leave a fresh empty `## [Unreleased]` above it.
3. Merge to `main` and make sure it is green, then tag the merge commit and
   push the tag:

       git tag v0.18.0
       git push origin v0.18.0

4. The `release` workflow re-runs the full test suite against the tagged
   commit, builds, **publishes** the release, and pushes the updated cask to
   `Yuncun/homebrew-tap`. Pushing the tag is the publish line — review the
   changelog and diff BEFORE tagging; there is no draft step to catch a
   mistake after.
5. Verify: the release page shows the four tarballs plus `checksums.txt`, the
   tap repo has a fresh cask commit, and `brew install yuncun/tap/omakase`
   (or `brew upgrade omakase`) serves the new version.

To test the build locally without touching GitHub:

    goreleaser release --snapshot --skip=publish --clean

## After publishing

Re-pin `bin/lib-omakase-bin.sh` to the release just published:

1. Bump `OMAKASE_PIN_VERSION`.
2. Replace the four archive hashes in `omakase_archive_sha256_for` with the new
   release's `checksums.txt` entries, verbatim.
3. Regenerate the four binary hashes (the checksum of the `omakase` binary
   *inside* each archive, not the archive itself) by downloading the four
   `omakase_*_*.tar.gz` assets into a scratch directory and running:

       for a in omakase_*_*.tar.gz; do d="${a%.tar.gz}"; mkdir -p "$d"; tar xzf "$a" -C "$d" omakase; \
         printf '%s  %s\n' "$(shasum -a 256 "$d/omakase" | awk '{print $1}')" "$d"; done

   and pasting each `hash  stem` pair into `omakase_bin_sha256_for`.

The pin intentionally lags one commit: the pin for a version can only land after that
version is published, since the hashes come from that release's own artifacts.

## The Homebrew tap

`Yuncun/homebrew-tap` holds the cask; GoReleaser rewrites it on every release,
authenticated by the `TAP_GITHUB_TOKEN` repo secret — a fine-grained PAT
scoped to that one repo with Contents read/write. When the PAT expires, the
release run fails at the cask-push step: mint a replacement scoped the same
way and update the secret. Users install with:

    brew install yuncun/tap/omakase
