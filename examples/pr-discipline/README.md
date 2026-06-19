# Example: a behavioral payload (pr-discipline)

Most omakase payloads ship **gates** — checks wired into git hooks. A payload can
just as well ship **agent guidance**: an opt-in rule your AI assistant reads, with no
hook and no enforcement. omakase injects whatever a payload contains.

This example ships one advisory rule — *prefer consolidated PRs over a tower of small
stacked ones* — to both hosts:

- `payload/.claude/rules/pr-discipline.md` — Claude Code reads it as a rule.
- `payload/.github/instructions/pr-discipline.instructions.md` — Copilot reads it as a
  path-scoped instruction (`applyTo: "**"`).

No gate, no lefthook entry, nothing committed in the target repo.

## Install it

`--source` clones its argument as a git repo, so it cannot point at a subdirectory
of this repo. Two honest paths:

**Try it locally** — from inside the target repo, point `OMAKASE_PAYLOAD` at your clone:

    OMAKASE_PAYLOAD=/path/to/omakase-harness/examples/pr-discipline/payload \
      bash /path/to/omakase-harness/bin/init.sh

**Share it (one-line install for anyone)** — copy this directory out into its own git
repo (it already carries `omakase.manifest` + `payload/`), publish it, then:

    /omakase init https://github.com/<you>/omakase-pr-discipline      # Claude Code
    bash bin/init.sh --source https://github.com/<you>/omakase-pr-discipline

Remove either with `/omakase remove`. Edit the rule to taste in `payload/` and re-init.

## Why this is opt-in, not global config

A personal preference could live in `~/.claude/CLAUDE.md` — but that only exists on
your machine, so no one else can adopt it. Shipping it as an omakase payload makes it
**shareable and opt-in**: anyone who wants the same discipline installs it; everyone
else is untouched. That is the difference a harness buys you over global config.
