# superpowers-harness — a harness that enables a plugin

A worked example of a harness whose job is to bring in a companion plugin
([superpowers](https://github.com/obra/superpowers)) using only the native plugin mechanism.

It carries only its own delta:

    payload/omakase.manifest       the one manifest — name + version + a recommends: fallback line (no gates)
    payload/.claude/settings.json  Claude Code reads this

**Per-repo plugin enablement is Claude-only.** GitHub Copilot CLI has no project-scoped
settings file at all — `settings.json` is only ever read from `~/.copilot/` — so there is no
file a harness could place that would make Copilot install a plugin for one repo. (An earlier
version of this example shipped a `payload/.github/copilot/settings.json`; that path is not
read by Copilot and the file was inert.) On Copilot the manifest's `recommends:` line is the
mechanism: init prints the manual install commands once.

The settings file registers the `superpowers-marketplace` and enables
`superpowers@superpowers-marketplace`:

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

omakase only **overlays files**. Claude Code reads its settings file at startup and installs
the plugin itself. `omakase init` places the file as a gitignored overlay; the rest of an
install (banner, ledger, status-line) comes from the omakase base harness, layered in
underneath.

## What actually happens (and the consequence)

| | Claude Code | Copilot CLI |
|---|---|---|
| Best case | installs on its own, stays latest; one-session activation lag | no per-repo mechanism — install by hand from the `recommends:` line (or add the plugin to `~/.copilot/settings.json`, which applies to every repo) |
| Won't fire if | folder not trusted, headless run, or an old client (then it prints a hint) | — |
| Failure is | **silent** — superpowers just isn't there, no error | n/a (a manual step either ran or didn't) |

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
already have — point the settings file + the manifest's `recommends:` at the plugin you
pair with, push, and share. People adopt it with `omakase init you/your-repo` (add
`/path/to/harness` when it lives in a subfolder).
