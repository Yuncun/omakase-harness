# Repo-supplied agent instructions: GitHub Copilot vs Claude Code

Four scenarios. Behavior lines only; every line sourced. Verified 2026-06-11.

---

## 1. You clone an unfamiliar repo that ships agent instruction files

Expected:

- The agent discloses which instruction files it found before they take effect.
- Approval is asked once and re-asked if the files change.

GitHub Copilot:

- Instruction files load automatically: "Instructions are automatically added to requests that you submit to Copilot." ([Copilot CLI docs](https://docs.github.com/en/copilot/how-tos/copilot-cli/customize-copilot/add-custom-instructions))
- No approval prompt is documented on any surface — VS Code, CLI, or github.com. ([repo instructions](https://docs.github.com/en/copilot/how-tos/configure-custom-instructions/add-repository-instructions), [support matrix](https://docs.github.com/en/copilot/reference/custom-instructions-support))
- A workspace's AGENTS.md "is automatically picked up as context for chat requests," on by default. ([VS Code 1.104 release notes](https://code.visualstudio.com/updates/v1_104))
- Copilot CLI documents no way to turn instruction loading off; its only documented control adds more instruction directories. ([CLI docs](https://docs.github.com/en/copilot/how-tos/copilot-cli/customize-copilot/add-custom-instructions))
- No re-approval on content change is documented anywhere.

Claude Code:

- First launch in a new folder asks "Do you trust the files in this folder?" before project config takes effect. ([security docs](https://code.claude.com/docs/en/security))
- Each project-defined MCP server requires separate approval and shows as "⏸ Pending approval" until decided. ([MCP docs](https://code.claude.com/docs/en/mcp))
- External files imported by CLAUDE.md trigger a one-time approval dialog listing each file, and declining persists. ([memory docs](https://code.claude.com/docs/en/memory))
- `claude --safe-mode` starts a session with all repo-supplied customization disabled. ([changelog v2.1.169](https://github.com/anthropics/claude-code/blob/main/CHANGELOG.md))

---

## 2. You want to turn off one checked-in instruction file or skill, without editing the repo

Expected:

- A per-item off switch that stays on your machine.

GitHub Copilot:

- No per-file disable exists on any surface. ([support matrix](https://docs.github.com/en/copilot/reference/custom-instructions-support); docs describe none)
- VS Code settings disable whole file types only — every AGENTS.md (`chat.useAgentsMdFile`), every instructions file (`github.copilot.chat.codeGeneration.useInstructionFiles`). ([VS Code 1.104](https://code.visualstudio.com/updates/v1_104), [IDE instructions docs](https://docs.github.com/en/copilot/how-tos/configure-custom-instructions-in-your-ide/add-repository-instructions-in-your-ide))
- The only per-file scoping (`excludeAgent` frontmatter) is written by the repo author into the committed file; the consuming developer cannot use it. ([changelog 2025-11-12](https://github.blog/changelog/2025-11-12-copilot-code-review-and-coding-agent-now-support-agent-specific-instructions/))
- Copilot CLI: no disable mechanism documented at all. ([CLI docs](https://docs.github.com/en/copilot/how-tos/copilot-cli/customize-copilot/add-custom-instructions))
- Organization instructions have no documented member opt-out, and "all sets of relevant instructions are provided to Copilot." ([org instructions](https://docs.github.com/en/copilot/how-tos/copilot-on-github/customize-copilot/add-custom-instructions/add-organization-instructions), [repo instructions](https://docs.github.com/en/copilot/how-tos/configure-custom-instructions/add-repository-instructions))

Claude Code:

- `/skills` → highlight a skill → Space → off; saved to `.claude/settings.local.json`, a file Claude Code itself configures git to ignore. ([skills docs](https://code.claude.com/docs/en/skills), [settings docs](https://code.claude.com/docs/en/settings))
- `claudeMdExcludes` skips individual CLAUDE.md or `.claude/rules` files by path or glob. ([settings docs](https://code.claude.com/docs/en/settings), [memory docs](https://code.claude.com/docs/en/memory))
- A project-enabled plugin is declined with one line in settings.local.json: `"enabledPlugins": {"name@marketplace": false}`. ([settings docs](https://code.claude.com/docs/en/settings))

---

## 3. The repo recommends tooling — what will it add, and what does it cost?

Expected:

- Itemized disclosure and context cost, before accepting.

GitHub Copilot:

- Instruction files have no pre-load disclosure; visibility is per response, after the fact: "expand the list of references at the top of a chat response." ([repo instructions](https://docs.github.com/en/copilot/how-tos/configure-custom-instructions/add-repository-instructions))
- VS Code's Diagnostics view lists instruction files only after they are loaded. ([VS Code custom instructions](https://code.visualstudio.com/docs/agent-customization/custom-instructions))
- No token or context cost is shown anywhere for instruction files.

Claude Code:

- The `/plugin` detail pane shows a "Will install" list — commands, agents, skills, hooks, MCP and LSP servers — before installation. ([discover-plugins](https://code.claude.com/docs/en/discover-plugins), v2.1.145)
- The same pane shows a context-cost estimate in tokens before install. (v2.1.143)
- Repo-recommended plugins arrive as prompts that can be skipped, never silent installs: "Users can skip unwanted marketplaces or plugins." ([settings docs](https://code.claude.com/docs/en/settings))
- `claude plugin details <name>` prints the same component inventory and projected per-session token cost from the command line. (v2.1.139)
- `/skills` sorts loaded skills by estimated token count (press `t`). (v2.1.111)

---

## 4. A malicious repo's checked-in settings try to escalate the agent's autonomy

Expected:

- A repo cannot grant the agent more autonomy by shipping config.

GitHub Copilot (VS Code):

- A committed `.vscode/settings.json` containing `"chat.permissions.default": "autoApprove"` switches Copilot into bypass-approvals mode once the user accepts the ordinary workspace-trust prompt. ([Repello AI, 2026-06-04](https://repello.ai/blog/vscode-copilot-workspace-trust-bypass))
- Microsoft's MSRC response: the behavior "aligns with the intended design of workspace trust" and "a solution will not be released." ([Repello AI](https://repello.ai/blog/vscode-copilot-workspace-trust-bypass))
- GitHub's position on rules-file poisoning (Pillar disclosure): "users are responsible for reviewing and accepting suggestions." ([Pillar Security, 2025-03-18](https://www.pillar.security/blog/new-vulnerability-in-github-copilot-and-cursor-how-hackers-can-weaponize-code-agents))

Claude Code:

- The equivalent escalation key is ignored when set in project settings, "to prevent untrusted repositories from auto-bypassing the prompt." ([settings docs](https://code.claude.com/docs/en/settings), `skipDangerousModePermissionPrompt`)
- Untrusted project settings setting OTEL client-certificate paths was treated as a bug and patched. ([changelog v2.1.169](https://github.com/anthropics/claude-code/blob/main/CHANGELOG.md))
- The trust dialog silently enabling all .mcp.json servers was likewise treated as a bug and patched. ([changelog v2.1.69](https://github.com/anthropics/claude-code/blob/main/CHANGELOG.md))

---

## Notes

- Claude Code's advisory text (CLAUDE.md, `.claude/rules`, skills) still auto-loads after the single folder-trust click; its per-artifact controls are opt-out after the fact, not consent up front.
- Neither product shows what a repo will inject before trust is granted — that pre-trust inventory exists nowhere.
- Full link-annotated research: `2026-06-11-consent-layers-prior-art.md` (same folder).
