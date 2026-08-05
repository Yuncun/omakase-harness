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
authenticated by the `TAP_GITHUB_TOKEN` repo secret — a token with
Contents read/write on BOTH repos (currently the maintainer's `gh` login
token; a fine-grained PAT scoped to the two repos also works). When the
token expires (or is missing a repo), the release run fails at the
corresponding push step: refresh the secret and re-run. Note the ordering:
the GitHub release publishes (`draft: false`) BEFORE the tap/bucket
pushes, so a token failure there leaves the release live with a stale (or
missing) install manifest — fix the token and re-run the workflow. Users
install with:

    brew install yuncun/tap/omakase

    scoop bucket add yuncun https://github.com/Yuncun/scoop-bucket
    scoop install omakase

## winget

The release run also generates winget manifests, pushes them to the
`Yuncun/winget-pkgs` fork, and opens a pull request against
`microsoft/winget-pkgs` (package id `Yuncun.omakase`). Two differences
from the tap/bucket channels:

- **Publishing is moderated.** A Microsoft validation pipeline plus a human
  moderator gate the merge — `winget install Yuncun.omakase` serves the new
  version only after that PR merges, typically days after the tag push. If
  the validation bot asks for changes, fix them on the PR branch in the
  fork.
- **The token needs Microsoft SSO.** The maintainer's account is a
  Microsoft org member, so the token in `TAP_GITHUB_TOKEN` must be
  SSO-authorized for the Microsoft enterprise
  (github.com/enterprises/microsoftopensource/sso) or the PR step fails.
  Everything earlier in the run — release, cask, scoop, the fork push —
  still lands; authorize the token and re-run, or open the PR by hand from
  the already-pushed fork branch.
