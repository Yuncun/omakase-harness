---
name: status
description: Show what omakase harness is installed in the current repo and what runs on which git hook — the inventory (committed / injected / personal), the hook wiring, the recent-runs scorecard, and the hidden paths. Read-only. Use when asked "omakase status", "what harness is installed", "show the harness", or "what gates run here".
allowed-tools: Bash(*/run.sh*) Bash(*/bin/status.sh*)
---

# /omakase:status — what's installed (read-only)

```bash
bash "${CLAUDE_PLUGIN_ROOT}/skills/status/run.sh"
```

(On Copilot CLI or a plain shell, run this skill directory's `run.sh`.)

Runs the base harness's `status.sh --markdown`, which emits the harness map as finished Markdown:
the inventory grouped by origin (committed / injected / personal), the hook wiring as a YAML
block, the recent-runs scorecard table, and the paths hidden via `.git/info/exclude`. **Relay it
verbatim** — output exactly what the script printed; do not reformat, re-order, summarize, or
annotate. The script owns the format so the render stays deterministic. This never changes
anything. If no harness is installed it says so; relay that.
