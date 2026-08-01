---
name: status
description: Show what omakase harness is installed in the current repo and what steers the agent here — the steering stack (yours vs the harness vs the project's own, sized by token cost), the guards chart (what runs on which git hook, with last verdicts), and the layers loaded every turn. The default page is read-only; per-item toggles are separate explicit flags. Use when asked "omakase status", "what harness is installed", "what has omakase injected", or "what gates run here".
allowed-tools: Bash(*/run.sh*)
---

# /omakase:status — what's installed

Run this skill's self-locating `run.sh`. `<skill-dir>` is THIS skill's own directory — the path
this SKILL.md was loaded from, which the host shows you. (Do not use `${CLAUDE_PLUGIN_ROOT}`:
Claude Code sets it but Copilot CLI does not, and an unset variable resolves to a broken path.)

```bash
bash <skill-dir>/run.sh
```

Runs the base harness's `status.sh --markdown`, which emits the harness map as finished Markdown:
the steering stack (who steers here — you, the harness, the project — sized by token cost), the
guards chart (what runs when you commit / push, with last verdicts), and the layers loaded every
turn, plus anything needing attention (missing / drifted / toggled-off files). **Relay it verbatim** — output exactly what the script printed; do not reformat, re-order, summarize, or
annotate. The script owns the format so the render stays deterministic. Run as above
(`--markdown`), this changes nothing. If no harness is installed it says so; relay that.

The write flags — `status.sh --disable <name>` / `--enable <name>` — toggle a placed
file or a gate off/on. Use them only when the human explicitly asks for a specific
toggle; never toggle on your own judgment.
