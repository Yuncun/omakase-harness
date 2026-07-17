# superpowers-harness — a harness that enables a plugin, cross-tool

A worked example of a harness whose job is to bring in a companion plugin
([superpowers](https://github.com/obra/superpowers)) using only the native plugin mechanism,
on **both** Claude Code and GitHub Copilot CLI. No workarounds.

It carries only its own delta:

    payload/omakase.manifest               the one manifest — name + version + a recommends: fallback line (no gates)
    payload/.claude/settings.json          Claude Code reads this
    payload/.github/copilot/settings.json  Copilot CLI reads this (same JSON, different path)

Both settings files declare the same thing — register the `superpowers-marketplace` and
enable `superpowers@superpowers-marketplace`:

```json
{
  "extraKnownMarketplaces": {
    "superpowers-marketplace": {
      "source": { "source": "github", "repo": "obra/superpowers-marketplace" },
      "autoUpdate": true
    }
  },
  "enabledPlugins": { "superpowers@superpowers-marketplace": true }
}
```

## How it works

omakase only **overlays files**. The host tool (Claude Code or Copilot CLI) reads its own
settings file at startup and installs the plugin itself. `omakase init` places both files as
a gitignored overlay; the rest of an install (banner, ledger, status-line) comes from the
omakase base harness, layered in underneath.

Two tools, two files, because they read different paths — Copilot never reads `.claude/`.

## What actually happens (and the consequence)

| | Claude Code | Copilot CLI |
|---|---|---|
| Best case | installs on its own, stays latest; one-session activation lag | installs on its own, **immediately** (no lag); "latest" not guaranteed |
| Won't fire if | folder not trusted, headless run, or an old client (then it prints a hint) | folder not trusted, or a client older than v1.0.22 |
| Failure is | **silent** — superpowers just isn't there, no error | **silent** |

Because failures are quiet, the manifest's `recommends:` line is the one visible fallback —
init prints it once so you can install by hand:

    claude (or copilot) plugin marketplace add obra/superpowers-marketplace
    claude (or copilot) plugin install superpowers@superpowers-marketplace

## Try it

This directory is a complete harness, and a harness can live in a **subfolder** of a git
repo — so it is adoptable straight from GitHub. From any project:

    omakase init Yuncun/omakase-harness/examples/superpowers-harness    # Claude Code or Copilot CLI

(From a local clone of this repo, the same install is
`omakase init --source <path-to-clone>//examples/superpowers-harness` — the `//` marks where
the repo ends and the subfolder begins.)

`omakase status` lists what it placed; `omakase remove` deletes it all and restores the repo.

## Make your own

Copy this directory into a git repo of your own — its own repo, or a subfolder of one you
already have — point both settings files + the manifest's `recommends:` at the plugin you
pair with, push, and share. People adopt it with `omakase init you/your-repo` (add
`/path/to/harness` when it lives in a subfolder).
