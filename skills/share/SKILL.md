---
name: share
description: Turn the current repo's harness setup into a new, publishable harness repo so others can adopt it with one line (omakase init you/harness). Captures the agent instructions, lint config, gates, and hook wiring into a sibling repo's payload/ plus an omakase.manifest and a README, and git-inits it ready to push. The inverse of init. Use when asked to "share my harness", "publish my harness", "package this setup", or "make a harness others can install".
allowed-tools: Bash(*/run.sh*) Bash(*/bin/share.sh*)
---

# /omakase:share — publish your harness

The inverse of init: init overlays a harness's payload onto a repo; share reads THIS repo's
harness files and writes them into a NEW harness repo you can push.

```bash
bash "${CLAUDE_PLUGIN_ROOT}/skills/share/run.sh"            # -> ../<reponame>-harness
bash "${CLAUDE_PLUGIN_ROOT}/skills/share/run.sh" team-rig   # -> ../team-rig (custom name)
```

(On Copilot CLI or a plain shell, run this skill directory's `run.sh` with the same args.)

It creates a SIBLING directory (never inside this repo), captures the harness into its `payload/`,
scaffolds `omakase.manifest` + `README.md` (carrying the install line), and git-inits + commits it.
**Relay the script's output**, especially the printed next steps: the publish command
(`gh repo create … --push`, or push to any git host) and the one-line install others run,
`omakase init you/<name>`.

Notes:
- If the capture finds no harness files, `payload/` is empty — a valid starting skeleton; add
  gates with `/omakase:add-gate`.
- It captures from the CURRENT repo. To package a different project, run share from there.
- A file the current repo still COMMITS is captured into payload but left committed in place
  (share never changes the source repo); the script lists those.
