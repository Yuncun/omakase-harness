# Releasing

How omakase ships. This is the single release runbook (CONTRIBUTING.md links
here). The scaffolding is fully wired but nothing is outward-facing until a
maintainer pushes a version tag AND publishes the draft — two deliberate steps,
both manual.

## What is wired

- `omakase --version` prints the build metadata. A plain `go build` reports
  `dev`; release builds get the real version, commit, and date injected via
  ldflags.
- `.goreleaser.yaml` cross-compiles `linux`/`darwin` × `amd64`/`arm64`,
  archives each as `tar.gz`, and writes `checksums.txt`.
- `.github/workflows/release.yml` runs on a semver tag push (`vX.Y.Z`),
  re-proves the tagged commit with the same checks as CI (Go vet/test/build +
  every sh suite), then uploads everything to a **draft** GitHub Release.

## Cutting a release

Any change adopters should pick up needs a version bump — the plugin manager
keys off `.claude-plugin/plugin.json`, and the banner / `omakase status` read
`payload/.omakase/VERSION` (see CONTRIBUTING.md for how the two update
channels differ).

1. Bump the version in **both** `.claude-plugin/plugin.json` and
   `payload/.omakase/VERSION` — they must match the tag. Pre-1.0, a breaking
   change bumps the minor (`0.17.0` → `0.18.0`), a backward-compatible one
   bumps the patch.
2. In `CHANGELOG.md`, rename the `## [Unreleased]` block to
   `## [x.y.z] — YYYY-MM-DD` and leave a fresh empty `## [Unreleased]` above it.
3. Merge to `main` and make sure it is green, then tag the merge commit and
   push the tag:

       git tag v0.18.0
       git push origin v0.18.0

4. The `release` workflow re-runs the full test suite against the tagged
   commit, builds, and uploads a **draft** release.
5. Review the draft on GitHub (artifacts, checksums, changelog), then click
   **Publish**. Nothing is public before this click. If the draft is wrong,
   fix, delete the tag, re-tag — the new run replaces the stale draft
   (`replace_existing_draft`).

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

## Enabling the Homebrew tap (not yet done)

1. Create the `Yuncun/homebrew-tap` repo (can be empty).
2. Create a fine-grained PAT with write access to `homebrew-tap` only, store
   it as a repo secret named `TAP_GITHUB_TOKEN`, and pass it to the GoReleaser
   step in `release.yml`:

       env:
         GITHUB_TOKEN: ${{ secrets.GITHUB_TOKEN }}
         TAP_GITHUB_TOKEN: ${{ secrets.TAP_GITHUB_TOKEN }}

3. In `.goreleaser.yaml`, uncomment the `homebrew_casks:` block (it already
   references that token) and set `release.draft: false`. The cask pushes in
   the same unattended run that creates the release, so the draft gate cannot
   stay: a cask pointing at a draft release 404s for users. From then on the
   publish line moves back one step — pushing the tag IS publishing.

After that, each release updates the cask and users run:

    brew install yuncun/tap/omakase
