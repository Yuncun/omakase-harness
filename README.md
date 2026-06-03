# omakase-harness

Inject a personal harness into any existing repo. The harness — your rules, docs,
conventions, and gates — is overlaid onto your working copy and kept out of git, so it
affects only you, never the committed repo or your teammates. Gates run on your git
hooks via lefthook.

## Usage

1. Install the plugin (Claude Code): `/plugin install omakase-harness@<marketplace>`.
2. In any git repo: `/omakase-init`. The payload tree is placed at real paths, every
   placed path is added to `.git/info/exclude`, and lefthook hooks are installed.
   Nothing is committed.
3. Undo anytime: `/omakase-remove`.

Adoption changes no tracked file. A path the repo already tracks is skipped, never
overwritten — the harness is additive by construction. To layer instructions over a
committed `AGENTS.md`/`CLAUDE.md`, ship them as `CLAUDE.local.md` (Claude Code reads and
concatenates it); to add rules, drop new files into `.claude/rules/`.

Requires the `lefthook` binary on PATH (`brew install lefthook`, `mise use lefthook`, or
a devDependency).

## Project structure

- `bin/init.sh` — overlays `payload/` additively, writes `.git/info/exclude`, installs lefthook.
- `bin/remove.sh` — reverses init.
- `commands/` — the `/omakase-init` and `/omakase-remove` slash commands.
- `payload/` — the harness content you ship: `lefthook-local.yml` wiring and `.omakase/gates/`.
  Replace the example gate with your own. To customize, fork the plugin and edit `payload/`.

## Contributing

The base (`bin/`, `commands/`) is mechanism and stays content-free — it hardcodes no
rule, doc, or gate. Harness content belongs in `payload/`. Run `bash tests/inject.test.sh`
before sending changes; it must print `ALL PASS`.

## License

MIT. See `LICENSE`.
