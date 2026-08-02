---
name: omakase-status
description: Show what omakase harness is installed in the current repo and what steers the agent here — the steering stack (yours vs the harness vs the project's own, sized by token cost), the guards chart (what runs on which git hook, with last verdicts), and the layers loaded every turn. The default page is read-only; per-item toggles are separate explicit flags. Use when asked "omakase status", "what harness is installed", "what has omakase injected", or "what gates run here".
allowed-tools: Bash(omakase:*)
---

# /omakase-status — what's installed

Run the `omakase` binary directly — it is on PATH (installing it is what placed this skill). If
it is somehow missing, stop and tell the user to install it: `brew install yuncun/tap/omakase`.

```bash
omakase status --markdown
```

This emits the harness map as finished Markdown:
the steering stack (who steers here — you, the harness, the project — sized by token cost), the
guards chart (what runs when you commit / push, with last verdicts), and the layers loaded every
turn, plus anything needing attention (missing / drifted / toggled-off files). **Relay it
verbatim** — output exactly what the command printed; do not reformat, re-order, summarize, or
annotate. The binary owns the format so the render stays deterministic. If no harness is
installed it says so; relay that.

The write flags — `omakase status --disable <name>` / `--enable <name>` — toggle a placed
file or a gate off/on. Use them only when the human explicitly asks for a specific
toggle; never toggle on your own judgment.
