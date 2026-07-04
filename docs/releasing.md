# Releasing

How the omakase binary ships. The scaffolding is fully wired but nothing is
outward-facing until a maintainer pushes a version tag AND publishes the draft —
two deliberate steps, both manual.

## What is wired

- `omakase --version` (also `-v`, `version`) prints the build metadata.
  A plain `go build` reports `dev`; release builds get the real version,
  commit, and date injected via ldflags.
- `.goreleaser.yaml` cross-compiles `linux`/`darwin` × `amd64`/`arm64`,
  archives each as `tar.gz`, and writes `checksums.txt`.
- `.github/workflows/release.yml` runs GoReleaser on any `v*` tag push and
  uploads everything to a **draft** GitHub Release.

## Cutting a release

1. Make sure `main` is green.
2. Tag and push (continue the existing line — `v0.17.0` was the last
   plugin-era release):

       git tag v0.18.0
       git push origin v0.18.0

3. The `release` workflow builds and uploads a **draft** release.
4. Review the draft on GitHub (artifacts, checksums, changelog), then click
   **Publish**. Nothing is public before this click.

To test the build locally without touching GitHub:

    goreleaser release --snapshot --skip=publish --clean

## Enabling the Homebrew tap (not yet done)

1. Create the `Yuncun/homebrew-tap` repo (can be empty).
2. Uncomment the `brews:` block at the bottom of `.goreleaser.yaml`.
3. Give the release workflow a token that can push to the tap repo
   (the default `GITHUB_TOKEN` only reaches this repo): create a
   fine-grained PAT with write access to `homebrew-tap`, store it as a
   repo secret, and pass it to the GoReleaser step. Also set
   `release.draft: false` or publish before the formula updates, since a
   formula pointing at a draft release 404s for users.

After that, each published release updates the formula and users run:

    brew install yuncun/tap/omakase
