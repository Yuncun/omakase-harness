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

Claude Code and GitHub Copilot CLI (same commands — Copilot reads the same plugin
manifest; its skill names are not `omakase:`-prefixed):

    /plugin marketplace add yuncun/omakase-harness
    /plugin install omakase@omakase
    /omakase:init

Any other environment, or a plain shell:

    cd /path/to/target-repo
    bash /path/to/omakase/bin/init.sh

The plugin wraps the same `bin/` scripts behind skills: `/omakase:init`,
`/omakase:status`, `/omakase:remove`, plus two authoring skills — `/omakase:author`
(build a custom harness, or turn a repo's existing agent files into one) and
`/omakase:add-gate` (wire a check into a git hook).

## Commands

    init.sh [<owner/repo[/subpath][#ref]> | --source <git-url|path>] [--cut-over]
                              overlay the harness, exclude it from git, install hooks
    status.sh                 the menu: see and toggle every steering file and gate
                              (interactive on a terminal; static page when piped)
    remove.sh                 delete the placed files, uninstall hooks, restore the repo

`init` self-provisions the omakase binary itself when a clone has no Go — a pinned,
checksum-verified binary fetched once into a per-machine cache. `omakase diff` shows what
you changed in any placed file (read-only);
keep your version with `omakase status --keep <path>` or put the harness's back with
`--restore`. `omakase mcp` serves the same status + consent menu inside Claude Code
and Copilot CLI. Flags and environment variables are in the
[reference](docs/reference.md).

## How it works

Gates run through git hooks, so they fire on commit and push whatever produced the
change: an agent, an IDE, or a plain `git` command. `init` writes one permanent
dispatcher per hook; at commit time it verifies the harness is complete (fail closed)
and runs the gates the harness declares in `omakase.manifest`. Nothing rewrites a hook
file after init. Installed files are never staged or committed, and `remove` reverses
every step.

A harness declares its gates in `omakase.manifest` — one `gate:` block per check:

```
gate: go-test
  hook: pre-push
  run: go test ./...
  glob: *.go go.mod go.sum
  cacheable: true
```

`hook:` is `pre-commit` or `pre-push`; `run:` is any command (run via `sh` from the repo
root; non-zero blocks the commit or push); `glob:` scopes the gate to matching changed
files; `cacheable:` reuses a PASS until HEAD moves. Skip one gate once with
`OMAKASE_SKIP_<NAME>=1`, or every gate once with `OMAKASE_SKIP_GATES=1` (both audited).

Editing a placed file is expected, not an error: the status surfaces turn amber,
`omakase diff` shows exactly what you changed, and you either keep your version or
restore the harness's. To customize a whole harness, work at the source: fork the
harness repo, edit there, and point `init` at your fork.

## Documentation

- [Concepts](docs/concepts.md) — the overlay model, gates and deferred gates, owned and shared paths
- [Authoring](docs/authoring.md) — build or customize a harness, and the common pitfalls
- [Reference](docs/reference.md) — commands, flags, environment variables, path classification

## License

MIT. See [LICENSE](LICENSE).
