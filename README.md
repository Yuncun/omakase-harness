# omakase-harness-framework

Omakase is a framework for packaging a project's [outer harness](https://codagent.beehiiv.com/p/harnesses-explained) in a distributable plugin. This allows a project's harness to be decoupled from its harness files, and for the harness to be selectively enabled/disabled by contributors. 

The harness plugin deploys all your harness files (scripts, rules, hooks) into the project, then gitignores them so that they are not checked back into the project. 

## Usage (creating a plugin from existing repo with harness files)

To automatically adopt an existing repository's harness into `payload/`. 

Run `import.sh` 

    bash bin/import.sh /path/to/source-repo

todo: Add a skill wrapper to sprinkle more LLM magic on importing harnesses since there may be harness patterns that I haven't captured in the import script.


## Distribute

- Create plugins: https://code.claude.com/docs/en/plugins
- Create and distribute a marketplace: https://code.claude.com/docs/en/plugin-marketplaces

## Using the plugin

    cd /path/to/target-repo
    bash /path/to/harness/bin/init.sh     # inject the harness (gitignored) + install hooks
    bash /path/to/harness/bin/show.sh     # display the installed harness
    bash /path/to/harness/bin/remove.sh   # reverse init

In Claude Code, install the plugin and use the wrapper command instead:

    /plugin marketplace add owner/repo
    /plugin install omakase-harness@your-marketplace
    /omakase init
    /omakase init https://github.com/you/your-harness   # install from a harness source repo

Adoption requires lefthook (`brew install lefthook`, `mise use lefthook`, or a project devDependency); `/omakase init` prompts to install it if absent.

## Repository layout

- `bin/import.sh` — capture a repository's harness into `payload/`
- `bin/init.sh` — inject `payload/` and install hooks
- `bin/remove.sh` — reverse `init`
- `bin/show.sh` — render the installed harness
- `commands/omakase.md` — the `/omakase` command (Claude Code wrapper)
- `payload/` — the harness content (the only part that varies per harness)

## License

MIT. See `LICENSE`.
