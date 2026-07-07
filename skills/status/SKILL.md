---
name: status
description: Show what omakase harness is installed in the current repo and what runs on which git hook — the inventory (committed / injected / global), the hook wiring, the recent-runs scorecard, and the hidden paths. The default page is read-only; per-item toggles are separate explicit flags. Use when asked "omakase status", "what harness is installed", "show the harness", or "what gates run here".
allowed-tools: Bash(*/run.sh*) Bash(*/bin/status.sh*)
---

# /omakase:status — what's installed

```bash
bash "${CLAUDE_PLUGIN_ROOT}/skills/status/run.sh"
```

(On Copilot CLI or a plain shell, run this skill directory's `run.sh`.)

Runs the base harness's `status.sh --markdown`, which emits the harness map as finished Markdown:
the inventory grouped by origin (committed / injected / global), the hook wiring as a YAML
block, the recent-runs scorecard table, and the paths hidden via `.git/info/exclude`. **Relay it
verbatim** — output exactly what the script printed; do not reformat, re-order, summarize, or
annotate. The script owns the format so the render stays deterministic. Run as above
(`--markdown`), this changes nothing. If no harness is installed it says so; relay that.

The write flags — `status.sh --disable <name>` / `--enable <name>` — toggle a placed
file or a gate off/on. Use them only when the human explicitly asks for a specific
toggle; the consent surfaces for choosing are the interactive screen (bare
`omakase status` in the human's own terminal) and the MCP menu (`omakase mcp`).
