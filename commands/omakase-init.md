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

If the injector exits with "lefthook not found", it could not resolve lefthook on PATH
or in the repo's `node_modules/.bin`. Do NOT install it silently — ask the user how
they'd like to install lefthook (e.g. `brew install lefthook`, `mise use lefthook`, or
as a project devDependency via their package manager), run the one they choose, then
re-run the injector. If they already have a lefthook binary elsewhere, they can instead
re-run with `LEFTHOOK_BIN=/path/to/lefthook`.
