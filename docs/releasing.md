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

Any change adopters should pick up needs a version bump — `omakase status`
(its banner line) reads `payload/.omakase/VERSION`, and the downgrade guards
and the user-skill refresh compare against it.

1. Bump the version in **both** stamps — `payload/.omakase/VERSION` and the
   `version:` line in `payload/omakase.manifest` — they must match the tag.
   Pre-1.0, a breaking change bumps the minor (`0.17.0` → `0.18.0`), a
   backward-compatible one bumps the patch.
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
5. Verify: the release page shows the four tarballs, the two Windows zips,
   and `checksums.txt`; the tap and bucket repos each have a fresh commit;
   and `brew install yuncun/tap/omakase` (or `brew upgrade omakase`) serves
   the new version.

To test the build locally without touching GitHub:

    goreleaser release --snapshot --skip=publish --clean

That is the whole release: one PR, one tag. (The shims carry no pinned
version or fetch tier since #182 — there is nothing to re-pin after
publishing.)

## The Homebrew tap and the Scoop bucket

`Yuncun/homebrew-tap` holds the cask and `Yuncun/scoop-bucket` holds the
Windows Scoop manifest (#212); GoReleaser rewrites both on every release,
authenticated by the `TAP_GITHUB_TOKEN` repo secret — a fine-grained PAT
that needs Contents read/write on BOTH repos. When the PAT expires (or is
missing the bucket repo), the release run fails at the corresponding push
step: mint a replacement scoped the same way and update the secret. Note
the ordering: the GitHub release publishes (`draft: false`) BEFORE the
tap/bucket pushes, so a token failure there leaves the release live with a
stale (or missing) install manifest — fix the token and re-run the
workflow. Users install with:

    brew install yuncun/tap/omakase

    scoop bucket add yuncun https://github.com/Yuncun/scoop-bucket
    scoop install omakase
