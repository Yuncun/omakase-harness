<!-- Maintainers: this README is sell-first but lean — intro, demo, install, commands,
     what a harness is, how it works, why not commit, sharing. Deeper detail belongs in
     docs/ (concepts, authoring, reference), not here. -->

<h1 align="center">omakase</h1>

<p align="center">
  <a href="https://github.com/Yuncun/omakase-harness/actions/workflows/tests.yml"><img src="https://github.com/Yuncun/omakase-harness/actions/workflows/tests.yml/badge.svg" alt="tests"></a>
  <a href="https://github.com/Yuncun/omakase-harness/releases"><img src="https://img.shields.io/github/v/release/Yuncun/omakase-harness" alt="release"></a>
  <a href="LICENSE"><img src="https://img.shields.io/github/license/Yuncun/omakase-harness" alt="license"></a>
</p>

<p align="center"><b>A harness manager for coding agents.</b><br>
Organize, share, and deploy agent instructions, skills, and git-hook gates.
They are placed in the working tree and never committed.</p>

omakase installs a harness from one repo into any number of others. Instruction
files go where your agent reads them. Gates are wired to git hooks. Every placed
file is registered in `.git/info/exclude`, so nothing enters git history or
shows up in a pull request. `omakase status` lists each item and lets you turn
it off. `omakase remove` deletes everything it placed.

<!-- demo.gif slot — VHS tape to live at docs/tapes/demo.tape.
     Storyboard: init → status menu opens, toggle one gate → a commit trips the lint gate
     → git status: clean. The transcript below is a SKETCH with invented output; the tape
     replaces it. -->

```
$ omakase init acme/webapp-harness
placed 14 files, wired 3 gates (lint, test, secrets) — nothing committed

$ git status
nothing to commit, working tree clean
```

## Install

```
brew install yuncun/tap/omakase
```

<!-- tap not published yet — ship with whichever methods are live at publish time -->

Or grab a binary from [releases](https://github.com/Yuncun/omakase-harness/releases)
(checksums published), or build from source:

```
go install github.com/Yuncun/omakase-harness/cmd/omakase@latest
```

Inside Claude Code or GitHub Copilot CLI, the plugin wraps the same commands:

```
/plugin marketplace add yuncun/omakase-harness
/plugin install omakase@omakase
/omakase:init
```

## Use

```
omakase init owner/repo   place that repo's harness here: files in, gates wired, all of it excluded
omakase status            the menu — see every item, toggle any of them off (plain text when piped)
omakase init              bare: repair or refresh from the remembered source
omakase remove            delete everything placed, unwire the hooks
omakase mcp               the same menu, served inside Claude Code and Copilot CLI
```

## What's a harness?

Everything a team places in a repo to shape how agents (and people) work there,
without being part of the product itself. It has two halves:

- **steering**, before the agent acts: instructions, rules, skills
- **checking**, after it produces: lint, test, and secret gates on commit and push

A rule of thumb for what belongs in a harness: two contributors could disagree
about it and still build the identical product. A 25-minute test gate or a
coding convention passes that test. Source code and the CI that defines
correctness do not.

## How it works

Placing works differently for each half. Steering files are copied to where the
agent reads them and excluded from git. Gates are also excluded, and wired to
git hooks through [lefthook](https://github.com/evilmartians/lefthook), fetched
as a pinned, checksum-verified binary if absent. Hooks fire on commit and push
regardless of what made the change: an agent, an IDE, or plain `git`. The source
repo is remembered, so a bare `omakase init` repairs or refreshes the overlay,
and anything you turned off stays off. A skipped gate prints that it was
skipped.

For scripts and agents, `omakase status --plain` prints a stable text page, and
`--disable` / `--enable` do what the menu does.

## Why not just commit these files?

The short version — the manifesto has the full argument with sources:
<!-- manifesto link goes here once the gh-pages essay exists -->

- Instruction files rot. They are reviewed like documentation but consumed like
  configuration. OpenAI's codex repo shipped a 322-line AGENTS.md that pointed
  at a file that no longer existed.
- Committed config activates on every clone behind one folder-trust click.
  Committed hook config is a known attack class with CVEs, and enterprise push
  rulesets now block those file paths.
- Attention is per-person. Every committed skill and rule spends every
  contributor's instruction budget, whether or not it helps them.
- These files are working preferences, not the product. Contributors can
  disagree about them while shipping identical code.

## Share your harness

A harness is a repo with a `payload/` directory and an `omakase.manifest`.
Publish it and anyone can install it with `omakase init you/your-harness`.
See [authoring](docs/authoring.md).

## Documentation

- [Concepts](docs/concepts.md) — the overlay model, gates and deferred gates, owned and shared paths
- [Authoring](docs/authoring.md) — build or customize a harness, and the common pitfalls
- [Reference](docs/reference.md) — commands, flags, environment variables, path classification

## License

MIT. See [LICENSE](LICENSE).
