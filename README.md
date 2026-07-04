<!-- AGENTS / maintainers: keep this README sparse — intro, install, commands, a short
     "how it works", and the links to docs/. Deeper detail belongs in docs/ (concepts,
     authoring, reference), NOT here. This README is a doorway, not a manual. -->

# omakase

Omakase installs a repository's local development harness (git hooks, gates, lint
rules, agent instructions) into any target repo as a gitignored overlay. The harness
runs from the target's working tree but never enters its git history. One repo defines
a harness; any number of repos install it.

A project's enforcement layer usually lives committed inside the repo it guards. That
couples it to one project, forces it on every contributor, and copies the same checks
into every repo that wants them. Omakase keeps a harness in its own repo.
Installing registers each placed file in `.git/info/exclude`, so git never tracks it and
it never reaches a pull request. Removing deletes exactly what was placed.

## Install

Claude Code:

    /plugin marketplace add yuncun/omakase-harness
    /plugin install omakase@omakase
    /omakase:init

Any other environment, including GitHub Copilot CLI and a plain shell:

    cd /path/to/target-repo
    bash /path/to/omakase/bin/init.sh

The Claude Code plugin wraps the same `bin/` scripts behind `/omakase:*` skills
(`/omakase:init`, `/omakase:status`, `/omakase:remove`).

## Commands

    init.sh [<owner/repo> | --source <url>]   overlay the harness, exclude it from git, install hooks
    status.sh [--markdown]                       print what is installed and what runs when
    remove.sh                                  delete the placed files, uninstall hooks, restore the repo

`init` installs lefthook if absent, fetching a pinned, checksum-verified binary into a
per-machine cache. Flags and environment variables are in the [reference](docs/reference.md).

## How it works

Gates run through git hooks, installed via lefthook, so they fire on commit and push
whatever produced the change: an agent, an IDE, or a plain `git` command. Installed files
are never staged or committed, and `remove` reverses every step.

## Documentation

- [Concepts](docs/concepts.md) — the overlay model, gates and deferred gates, owned and shared paths
- [Authoring](docs/authoring.md) — build or customize a harness, and the common pitfalls
- [Reference](docs/reference.md) — commands, flags, environment variables, path classification

## License

MIT. See [LICENSE](LICENSE).
