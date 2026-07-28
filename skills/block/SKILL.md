---
name: block
description: Block or unblock one piece of THIS repo's own committed agent config — an instruction file (CLAUDE.md, AGENTS.md, .github/copilot-instructions.md), a committed skill/agent/prompt, or a committed hook script — so it stops steering agents in this clone. Hidden from the working tree via git sparse-checkout; nothing is deleted and unblock restores it exactly. Use when asked to "block that skill", "stop X from loading", "hide the repo's CLAUDE.md from my agent", or "unblock / bring back X".
allowed-tools: Bash(*/run.sh*)
---

# /omakase:block — turn off a piece of the repo's own harness

Run this skill's self-locating `run.sh`. `<skill-dir>` is THIS skill's own directory — the path
this SKILL.md was loaded from, which the host shows you. (Do not use `${CLAUDE_PLUGIN_ROOT}`:
Claude Code sets it but Copilot CLI does not, and an unset variable resolves to a broken path.)

```bash
bash <skill-dir>/run.sh <item>            # step 1: explains what blocking does; changes nothing
bash <skill-dir>/run.sh <item> --yes      # step 2: applies — ONLY after the human confirms
bash <skill-dir>/run.sh unblock <item>    # restore a blocked item
```

`<item>` is a committed path from `omakase status` (e.g. `CLAUDE.md`,
`.agents/skills/od-worktree`) or a bare skill/agent name when unambiguous.

**Consent is two-step and belongs to the human.** Run WITHOUT `--yes` first and relay the
explanation verbatim; add `--yes` only after the human explicitly says to proceed. Never block —
and never pass `--yes` — on your own judgment, and never block an item the human didn't name.
Unblock needs no `--yes`.

After a block or unblock, relay the closing line, then remind the session: a file hidden this
way is out of the working tree until unblocked, but git still tracks it — commits, pulls, and
pushes are unaffected. `omakase remove` also restores every blocked item.
