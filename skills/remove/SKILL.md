---
name: remove
description: Remove the omakase harness from the current repo — uninstall the git hooks, delete exactly the untracked files init placed (never a tracked file), and strip the omakase block from .git/info/exclude, restoring the repo to its pre-init state. Use when asked to "remove / uninstall omakase", "take the harness off", or "undo init".
allowed-tools: Bash(*/run.sh*) Bash(*/bin/remove.sh*)
---

# /omakase:remove — reverse init

```bash
bash "${CLAUDE_PLUGIN_ROOT}/skills/remove/run.sh"
```

(On Copilot CLI or a plain shell, run this skill directory's `run.sh`.)

Uninstalls the git hooks, deletes exactly the untracked files init placed (never a tracked file),
and strips the omakase block from `.git/info/exclude`. Confirm to the user that the working tree
is back to its pre-init state.
