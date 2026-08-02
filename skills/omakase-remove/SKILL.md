---
name: omakase-remove
description: Remove the omakase harness from the current repo — uninstall the git hooks, delete exactly the untracked files init placed (never a tracked file), and strip the omakase block from .git/info/exclude, restoring the repo to its pre-init state. Use when asked to "remove / uninstall omakase", "take the harness off", or "undo init".
allowed-tools: Bash(omakase:*)
---

# /omakase-remove — reverse init

Run the `omakase` binary directly — it is on PATH (installing it is what placed this skill). If
it is somehow missing, stop and tell the user to install it: `brew install yuncun/tap/omakase`.

```bash
omakase remove
```

Uninstalls the git hooks, deletes exactly the untracked files init placed (never a tracked file),
and strips the omakase block from `.git/info/exclude`. Confirm to the user that the working tree
is back to its pre-init state.
