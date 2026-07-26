---
name: remove
description: Remove the omakase harness from the current repo — uninstall the git hooks, delete exactly the untracked files init placed (never a tracked file), and strip the omakase block from .git/info/exclude, restoring the repo to its pre-init state. Use when asked to "remove / uninstall omakase", "take the harness off", or "undo init".
allowed-tools: Bash(*/run.sh*) Bash(*/bin/remove.sh*)
---

# /omakase:remove — reverse init

Run this skill's self-locating `run.sh`. `<skill-dir>` is THIS skill's own directory — the path
this SKILL.md was loaded from, which the host shows you. (Do not use `${CLAUDE_PLUGIN_ROOT}`:
Claude Code sets it but Copilot CLI does not, and an unset variable resolves to a broken path.)

```bash
bash <skill-dir>/run.sh
```

Uninstalls the git hooks, deletes exactly the untracked files init placed (never a tracked file),
and strips the omakase block from `.git/info/exclude`. Confirm to the user that the working tree
is back to its pre-init state.
