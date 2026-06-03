# Omakase Base Harness

Omakase is a base structure for harnesses that custom harness plugins can be built on. These harnesses are intended to be shared as plugins that injects harness files into the project and enforces gates using git hooks, without committing files to the project. 

This is my proposal to the adoptability question: how do you introduce a harness to a project with existing contributors?


The base does only two things:

- Initialize the harness via a script, to inject claude rules, agent.mds, etc. into the project.
- Enforce gates on your git hooks (via [lefthook](https://lefthook.dev)).

What is possible:
* Precommit hooks to run static analyzers
* Pre-push hooks to run visual verification
* I've used this to trigger documentation enforcement using claude rules


So far I haven't found an "outer harness" concept that cant be covered by these two steps - gate enforcement and injecting files. Lmk if you find any.

## Usage

For creators:

//todo


For adopters of your harness:

* Install plugin 

From claude code (probably also gh copilot, but i havent tried yet)
```
/omakase-init     # overlay the payload into this repo (gitignored) + install hooks
/omakase-remove   # reverse it
```



## Project structure

- `bin/init.sh` — overlays `payload/` additively, writes `.git/info/exclude`, installs lefthook.
- `bin/remove.sh` — reverses init.
- `commands/` — the `/omakase-init` and `/omakase-remove` slash commands.
- `payload/` — the harness content you ship: `lefthook-local.yml` wiring + `.omakase/gates/`.
  Replace the example gate with your own. To customize, fork the plugin and edit `payload/`.

## Example payload: a web project's harness

The base ships only the mechanism and one example gate. As a fuller example, here is the
payload I run for web (See [pixterm harness](https://github.com/Yuncun/pixterm-harness)).


| Piece | Kind | What it does |
| ----- | ---- | ------------ |
| `worktree-discipline` | pre-commit guard | Blocks a main-checkout commit that would inherit another worktree's uncommitted work. Pure git; dormant unless more than one worktree is active. |
| `adr-required` | pre-commit guard | Requires a paired decision record when a declared architectural file changes. Dormant until you name the files. |
| `deferred-check` + `omakase-record` | deferred-gate scaffold | Enforces a verdict a hook can't compute itself — an LLM judge, a slow flow, a human sign-off. A producer records a pass/fail; the hook confirms a fresh pass for the pushed code. |
| `visual-verify` | skill | Best-effort visual check: drives the running app, judges screenshots, records a verdict for the deferred gate. Needs a per-project driver. |



## License

MIT. See `LICENSE`.
