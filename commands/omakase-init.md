---
description: Inject the harness payload into this repo (personal, zero committed footprint) and install git hooks
---

Run the injector, then report its output verbatim:

```bash
bash "${CLAUDE_PLUGIN_ROOT}/bin/init.sh"
```

This overlays the plugin's `payload/` tree onto the repo root, skipping any path the
repo already tracks (it never overwrites a committed file), records every placed path
in `.git/info/exclude`, and runs `lefthook install`. Nothing is committed. Tell the
user which files were placed, which were skipped as already-tracked, and that they can
undo everything with `/omakase-remove`. To add real gates, edit `.omakase/gates/` and
`lefthook-local.yml`.
