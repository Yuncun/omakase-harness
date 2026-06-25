# Project conventions (placed by the sample omakase harness)

This file was placed as a **gitignored overlay** — it runs from the working tree but is
never committed to this repo. It is here to show what a harness's agent-instructions file
looks like; replace it with your own conventions.

- Write commit messages in the imperative mood ("add X", not "added X").
- Prefer existing, reputable libraries over hand-rolled code.
- Resolve every merge-conflict marker before committing.
- Never commit scratch or secret-in-progress code — mark it `DO NOT COMMIT` and the
  harness's gate will block the commit until you remove it.

A real harness can place an `AGENTS.md` or `.github/copilot-instructions.md` the same way,
so other tools (Copilot CLI, etc.) read the same conventions.
