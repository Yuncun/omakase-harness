# superpowers-harness — a harness that enables a plugin, cross-tool

A worked example of a harness whose job is to bring in a companion plugin
([superpowers](https://github.com/obra/superpowers)) using only the native plugin mechanism,
on **both** Claude Code and GitHub Copilot CLI. No workarounds.

It carries only its own delta:

    omakase.manifest                       name + version + a recommends: fallback line
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
settings file at startup and installs the plugin itself. `omakase init --source <this>` places
both files as a gitignored overlay; the rest of an install (banner, ledger, status-line) comes
from the omakase base harness, layered in underneath.

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

These files are the contents of a harness repo, so put them in one first:

    cp -R <path-to>/omakase-harness/examples/superpowers-harness /tmp/superpowers-harness
    cd /tmp/superpowers-harness && git init -q && git add -A && git commit -qm "superpowers harness"

Then, from any project:

    omakase init --source /tmp/superpowers-harness         # Claude Code or Copilot CLI

`omakase status` lists what it placed; `omakase remove` deletes it all and restores the repo.

## Make your own

Copy this directory, point both settings files + the manifest's `recommends:` at the plugin you
pair with, push it to a git repo, and share the URL. People adopt it with
`omakase init --source <your-repo>`.
