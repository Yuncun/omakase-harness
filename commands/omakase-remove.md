---
description: Remove the injected harness from this repo (reverse of /omakase-init)
---

Run the remover, then report its output verbatim:

```bash
bash "${CLAUDE_PLUGIN_ROOT}/bin/remove.sh"
```

This uninstalls the git hooks, deletes exactly the untracked files the injector placed
(never a tracked file), and strips the omakase block from `.git/info/exclude`. Confirm
to the user that the working tree is back to its pre-init state.
