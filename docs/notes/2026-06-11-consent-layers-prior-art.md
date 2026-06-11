# Repo-supplied agent config: prior art, current controls, and the open lane

Research notes, 2026-06-11. Compiled from three doc-verification passes (≈17 web-research agents total; every claim below carries a source link; quotes are verbatim). Context: the omakase reframe discussion — what history says about repos shipping developer-environment config, what controls Claude Code and GitHub Copilot actually offer today, and where the gap is.

---

## 1. The claim this document supports

Repos ship two kinds of config. Settings that *describe* — "indent with two spaces" — get applied silently by every editor, and that's fine: the worst case is a badly indented file. Things that *run* — git hooks, install scripts — are different, and every mature ecosystem learned it the hard way: git refuses to run a cloned repo's hooks at all; direnv makes you type `allow` and asks again whenever the file changes; npm will stop running dependencies' install scripts next month. Agent instruction files look like the first kind — plain text — so today every agent loads them silently. But they behave like the second kind: that text steers an agent that edits files and runs commands, and researchers have shown a poisoned rules file can make an agent quietly insert backdoors. Text that drives an executor deserves the code treatment, not the settings treatment: show me what this repo feeds my agent, ask me once, ask again when it changes, and let me decline pieces — openly. The vendors half-agree already — Claude Code has per-file off switches buried in settings (§3); VS Code's untrusted-folder mode now disables "AI agents" first (§5) — but nobody has built the front door (§6).

---

## 2. Prior art: how ecosystems handled repo-supplied config

The pattern across thirty years of tooling: **approval prompts appear only where repo content executes; purely descriptive content got scope limits instead of prompts.** Agent instruction files are the first artifact class that is descriptive in form and executable in effect — which is why neither inherited norm fits cleanly.

### Git hooks — the original refusal

> "It's important to note that client-side hooks are not copied when you clone a repository." — [Pro Git, Customizing Git – Git Hooks](https://git-scm.com/book/en/v2/Customizing-Git-Git-Hooks)

The sharp detail: a repo *can* ship hook scripts as ordinary tracked files — they just never execute until a developer activates them locally. Presence ≠ activation. (Precision note: this statement lives in the Pro Git book, not the githooks man page; `core.hooksPath` can't be smuggled either, because git config is also local-only.)

### Hook managers — consent eroded, then restored by force

A hook manager fills git's one-script-per-event slots from a shared config file. The three big ones diverge on consent:

| Tool | Activation | Consent step? |
|---|---|---|
| pre-commit | `pre-commit install`, manual | Always explicit: "Every time you clone a project using pre-commit running `pre-commit install` should always be the first thing you do." ([pre-commit.com](https://pre-commit.com/)) |
| husky | npm `prepare` lifecycle script | Collapsed into `npm install` ([husky docs](https://typicode.github.io/husky/get-started.html)); escape hatches: `HUSKY=0`, `npm install --ignore-scripts` |
| lefthook | `lefthook install` — **except** the npm package, which installs hooks in a postinstall script automatically ([lefthook install docs](https://github.com/evilmartians/lefthook/blob/master/docs/usage/commands/install.md)) | Explicit for brew/gem/binary installs; auto for npm |

Then the package managers walked the erosion back, after incidents:

- **pnpm v10** (Jan 2025): "Lifecycle scripts of dependencies are not executed during installation by default! This is a breaking change aimed at increasing security." ([release notes](https://github.com/pnpm/pnpm/releases/tag/v10.0.0)) Per-package approval via [`pnpm approve-builds`](https://pnpm.io/cli/approve-builds).
- **npm v12** (est. July 2026): "npm install will no longer execute preinstall, install, or postinstall scripts from dependencies unless they are explicitly allowed in your project." ([official changelog, 2026-06-09](https://github.blog/changelog/2026-06-09-upcoming-breaking-changes-for-npm-v12/)) Motivated by worm attacks "injecting malicious post-install scripts into popular JavaScript packages" ([GitHub supply-chain plan](https://github.blog/security/supply-chain-security/our-plan-for-a-more-secure-npm-supply-chain/)).

The arc in one line: every convenience that auto-runs repo-supplied code eventually gets walked back to explicit approval after an incident.

### direnv — per-file consent with change detection

> "This is the security mechanism to avoid loading new files automatically. Otherwise any git repo that you pull, or tar archive that you unpack, would be able to wipe your hard drive once you cd into it." — [direnv man page](https://direnv.net/man/direnv.1.html)

Mechanism (verified in [source](https://github.com/direnv/direnv/blob/master/internal/cmd/rc.go)): the approval record is a file named by the SHA-256 of the `.envrc`'s absolute path + contents. Editing **or moving** the file invalidates the old approval automatically. This is the closest existing model for "re-ask when a previously-approved artifact changes." Known gap even here: files pulled in indirectly aren't covered by the hash.

### VS Code — the most complete precedent, three layers deep

1. **Recommend, never install.** "VS Code prompts a user to install the recommended extensions when a workspace is opened for the first time." ([extension marketplace docs](https://code.visualstudio.com/docs/configure/extensions/extension-marketplace)) Counter-nuance: `devcontainer.json` *does* auto-install extensions inside dev containers.
2. **Workspace Trust / Restricted Mode.** "Restricted Mode tries to prevent automatic code execution by disabling or limiting the operation of several VS Code features: **AI agents**, terminal, tasks, debugging, workspace settings, and extensions." ([workspace trust docs](https://code.visualstudio.com/docs/editing/workspaces/workspace-trust)) AI agents were added to this list around 2026-01 (§5).
3. **A register of settings the repo may not control.** "Not all user settings are available as workspace settings. … The first time you open a workspace that defines any of these settings, VS Code will warn you and then always ignore the values after that." ([settings docs](https://code.visualstudio.com/docs/configure/settings)) Every setting declares a scope; application/machine-scoped ones are simply not settable from repo files.

### EditorConfig — the declarative counterexample

EditorConfig auto-applies with **no consent prompt anywhere** — and there has never been a backlash, because its official property set is nine keys about file bytes, and the project wiki explicitly rejects domain-specific properties: "The following properties are not intended to be implemented by EditorConfig." ([spec](https://spec.editorconfig.org/index.html), [properties wiki](https://github.com/editorconfig/editorconfig/wiki/EditorConfig-Properties)) Legitimacy through scope discipline, not through consent UI. Any essay claiming "every ecosystem added a consent layer" must address this head-on: the honest claim is *consent for what runs, scope-limits for what describes* — and then argue agent instructions belong to the first family despite their format (§5).

---

## 3. Claude Code: the consent surfaces and per-artifact controls (most shipped in the last few months)

Current release at time of writing: 2.1.173. Versions below are from the [official changelog](https://github.com/anthropics/claude-code/blob/main/CHANGELOG.md) and doc min-version markers.

### The folder trust dialog — what it actually gates

First interactive launch in a folder asks "Do you trust the files in this folder?". Documented gated features (scattered across five doc pages; no single list exists): project settings permission rules, project hooks, project skills' `allowed-tools` grants, `autoMemoryDirectory`, project-scope skills-dir plugins, and the repo marketplace/plugin prompts. ([security](https://code.claude.com/docs/en/security), [skills](https://code.claude.com/docs/en/skills), [memory](https://code.claude.com/docs/en/memory), [plugins-reference](https://code.claude.com/docs/en/plugins-reference)) Caveats: "Trust verification is disabled when running non-interactively with the `-p` flag"; home-directory trust is session-only. It is folder-level all-or-nothing — per-artifact layers are separate, below.

### Per-MCP-server approval (separate from trust by design)

> "For security reasons, Claude Code prompts for approval before using project-scoped servers from .mcp.json files." — [MCP docs](https://code.claude.com/docs/en/mcp)

- Pending servers show as "⏸ Pending approval" in `claude mcp list` (hardened v2.1.154: piped output no longer auto-approves).
- Reset choices: `claude mcp reset-project-choices`.
- Settings keys to pre-decide: `enableAllProjectMcpServers`, `enabledMcpjsonServers`, `disabledMcpjsonServers` ([settings](https://code.claude.com/docs/en/settings)).
- v2.1.69 fixed the trust dialog "silently enabling all .mcp.json servers" — evidence the two consent layers are intentionally distinct.

### Repo-recommended plugins — prompt, not install

> "1. Team members are prompted to install the marketplace when they trust the folder 2. Team members are then prompted to install plugins from that marketplace 3. Users can skip unwanted marketplaces or plugins … 4. Installation respects trust boundaries and requires explicit consent" — [settings docs](https://code.claude.com/docs/en/settings)

- A project `enabledPlugins` entry on a fresh machine produces an actionable install **hint**, not a silent fetch (v2.1.144).
- Opt out of a project-enabled plugin: `"enabledPlugins": {"plugin@marketplace": false}` in `.claude/settings.local.json` (project settings beat *user* settings, so the local scope is the supported opt-out).
- **Force is org-only**: managed settings (`managed-settings.json`, deployed by an admin to the machine) can force-enable plugins; "Plugins force-enabled by managed settings cannot be disabled this way … `--plugin-dir` cannot override those." ([discover-plugins](https://code.claude.com/docs/en/discover-plugins)) No repo-scope force path is documented.
- Author-side consent affordance: `defaultEnabled: false` ships a plugin installed-but-off (v2.1.154+).

### Pre-install inventory and token cost

The `/plugin` Discover detail pane shows, **before** install: a context-cost token estimate (v2.1.143), Last updated date (v2.1.144), and a "Will install" list of commands/agents/skills/hooks/MCP/LSP (v2.1.145). CLI equivalent: `claude plugin details <name>` (v2.1.139). ([discover-plugins](https://code.claude.com/docs/en/discover-plugins))

### Per-artifact off switches for repo-committed content (the obscure ones)

| Control | What it disables | How |
|---|---|---|
| `skillOverrides` (v2.1.129) | A **single** checked-in skill or command — "Use it for skills whose SKILL.md you don't want to edit, such as ones checked into a shared project repo" | `/skills` → highlight → **Space** cycles on → name-only → user-invocable-only → off → **Enter** saves to `.claude/settings.local.json`. Plugin skills excluded ("Manage those through /plugin instead"). [skills docs](https://code.claude.com/docs/en/skills) |
| `claudeMdExcludes` | Individual CLAUDE.md **and `.claude/rules` files**, by glob or absolute path | `{"claudeMdExcludes": ["/path/other-team/.claude/rules/**"]}` in `.claude/settings.local.json`; managed policy files can't be excluded. [settings](https://code.claude.com/docs/en/settings), [memory](https://code.claude.com/docs/en/memory) |
| External-import approval | `@`-imports in CLAUDE.md pointing outside the project | Automatic one-time dialog listing the files; declining persists. [memory docs](https://code.claude.com/docs/en/memory) |
| `disableAllHooks` | All hooks — "There is no way to disable an individual hook while keeping it in the configuration." | Any settings file. **Hooks are the one artifact class with no per-item control.** [hooks docs](https://code.claude.com/docs/en/hooks) |
| `ConfigChange` hook (v2.1.49) | Audits or **blocks** mid-session config changes | Matcher on source (user/project/local/policy settings, skills); block via exit 2. [hooks docs](https://code.claude.com/docs/en/hooks) |
| `--safe-mode` (v2.1.169) | Everything repo-supplied, one flag | `claude --safe-mode` or `CLAUDE_CODE_SAFE_MODE=1` |

Post-load visibility (not pre-trust audit): `/context` (token breakdown by category), `/memory` (which CLAUDE.md/memory files loaded), `/skills` with `t` to sort by estimated token count (v2.1.111).

### Direction of travel

Consent/inventory features shipped in a ~35-release window: 2.1.139 (details CLI) → 143 (context cost) → 144 (install hint, not fetch) → 145 (pre-install component disclosure) → 154 (piped-list approval hardening, suggestion allowlists, defaultEnabled) → 169 (--safe-mode, trust-bypass patch) → 172 (background agents no longer leak another directory's trust state). One counter-entry: 2.1.152 "Auto mode no longer requires opt-in consent." Net: active, fast investment in exactly this surface — the *knobs* exist; what doesn't exist is a prompt when repo advisory text first loads, re-approval on change, per-hook control, or any unified view.

---

## 4. GitHub Copilot: the map (weakest consent surface of the major hosts)

### What a repo can ship, and which Copilot surface reads it

Per the [official support matrix](https://docs.github.com/en/copilot/reference/custom-instructions-support) and [repo-instructions how-to](https://docs.github.com/en/copilot/how-tos/configure-custom-instructions/add-repository-instructions):

| File | VS Code Chat | Copilot CLI | Coding agent (github.com) | Code review | Chat on github.com |
|---|---|---|---|---|---|
| `.github/copilot-instructions.md` | ✓ | ✓ | ✓ | ✓ | ✓ |
| `.github/instructions/*.instructions.md` (`applyTo` globs) | ✓ | ✓ | ✓ | ✓ | — |
| `AGENTS.md` (anywhere, nearest wins) | ✓ | ✓ | ✓ | — | — |
| `CLAUDE.md` / `GEMINI.md` (root) | CLAUDE.md per VS Code docs (matrices disagree) | ✓ | ✓ | — | — |
| Org instructions (server-side, set by org owners) | — | — | ✓ | ✓ | ✓ |

Notable load-location facts: code review reads instructions **server-side from the PR's base branch** ("When reviewing a pull request, Copilot uses the custom instructions in the base branch") — untouchable by any local tool; the branch the coding agent reads from is undocumented. And as of VS Code 1.109 (Jan 2026), **VS Code reads Claude config directly**: CLAUDE.md, `.claude/rules`, `.claude/agents`, `.claude/skills`, and hooks from `.claude/settings.json` — "your agents, skills, instructions, and hooks work across both tools without duplication" ([release notes](https://code.visualstudio.com/updates/v1_109)).

### Visibility

Per-response references list ("expand the list of references at the top of a chat response"), plus a VS Code Diagnostics view: "Use the chat customization diagnostics view to see all loaded instruction files and any errors. Right-click in the Chat view and select **Diagnostics**." ([VS Code custom-instructions docs](https://code.visualstudio.com/docs/agent-customization/custom-instructions)) No visibility mechanism documented for code review or coding-agent sessions.

### Controls — and what's missing

- File-**type** toggles only, in VS Code settings: `github.copilot.chat.codeGeneration.useInstructionFiles`, `chat.useAgentsMdFile`, `chat.useClaudeMdFile`, `chat.useNestedAgentsMdFiles`, `chat.instructionsFilesLocations`. **No per-individual-file disable exists on any surface.**
- The only per-file scoping is *author*-side (`excludeAgent` frontmatter) — committed content; a consuming developer can't use it.
- Copilot CLI: "Instructions are automatically added to requests that you submit to Copilot." **No flag, config, or env var to disable** — only the additive `COPILOT_CUSTOM_INSTRUCTIONS_DIRS`. ([CLI docs](https://docs.github.com/en/copilot/how-tos/copilot-cli/customize-copilot/add-custom-instructions))
- **No consent prompt anywhere, no re-approval on change** — docs-silent on every surface; the only gate is generic VS Code Workspace Trust (all-or-nothing).
- Org instructions ([GA 2026-04-02](https://github.blog/changelog/2026-04-02-copilot-organization-custom-instructions-are-generally-available/)): **no documented member opt-out**, and precedence is additive — "all sets of relevant instructions are provided to Copilot," so personal instructions cannot displace repo or org ones.

---

## 5. The security record: instruction files are already an execution surface

Chronological; this is the evidence chain for §1's claim.

- **Nov 2021** — VS Code 1.63 highlights invisible Unicode by default, in response to Trojan Source research. ([release notes](https://code.visualstudio.com/updates/v1_63))
- **2025-03-18** — Pillar Security, "Rules File Backdoor": invisible Unicode in `.cursor/rules` and Copilot instruction files steers agents to emit malicious code invisible in review. Vendor replies in the disclosure timeline: Cursor — "this risk falls under the users' responsibility"; GitHub — "users are responsible for reviewing and accepting suggestions." Pillar's mitigation: review AI config files "with the same scrutiny as executable code." ([Pillar](https://www.pillar.security/blog/new-vulnerability-in-github-copilot-and-cursor-how-hackers-can-weaponize-code-agents))
- **2025-05-01** — GitHub ships a hidden-Unicode warning on github.com, naming AI explicitly: "This can cause code to appear one way and be interpreted another way, especially by AI." ([changelog](https://github.blog/changelog/2025-05-01-github-now-provides-a-warning-about-hidden-unicode-text/)) Cursor: no first-party mitigation found as of 2026-06-11.
- **2025-08-25** — GitHub Security Lab documents Copilot agent-mode prompt-injection vulnerabilities and the consent layers shipped in response (URL-fetch confirmation, no out-of-workspace edits, confirmation on sensitive-file edits). ([GitHub blog](https://github.blog/security/vulnerability-research/safeguarding-vs-code-against-prompt-injections/))
- **2025-12-17** — Prompt Security: VS Code injects a workspace's AGENTS.md into every chat request by default; "AGENTS.MD is not documentation. It is an instruction substrate that runtimes may treat as authoritative." ([Prompt Security](https://prompt.security/blog/when-your-repo-starts-talking-agents-md-and-agent-goal-hijack-in-vs-code-chat))
- **2026-01-02** — VS Code docs add AI agents to the Workspace Trust restricted-mode list, with prompt injection as the stated rationale ([workspace trust docs](https://code.visualstudio.com/docs/editing/workspaces/workspace-trust); vscode-docs PR #9217).
- **2026-06-04** — Repello AI, "Workspace Trust Is Not AI Consent": a malicious repo's `.vscode/settings.json` with `"chat.permissions.default": "autoApprove"` silently puts Copilot in bypass-approvals mode once the user clicks the familiar trust prompt. MSRC declined to fix: the behavior "aligns with the intended design of workspace trust." Their line: "'Trust this workspace' has quietly become authorization for 'let an LLM run shell commands on my behalf'." ([Repello](https://repello.ai/blog/vscode-copilot-workspace-trust-bypass))

Key meta-finding: **"treat AI config like code" guidance comes from security researchers, not vendors** — no first-party GitHub/Microsoft/Anthropic statement frames instruction files themselves as executable-grade artifacts; vendor language stays at workspace-trust / review-the-output level. The developer-doctrine version of this argument is unwritten.

---

## 6. Landscape: who else is in this space

### Rules-sync tools (outward propagation — author once, emit to N agent formats)

[ruler](https://github.com/intellectronica/ruler) (~2.7k★, 30+ agents, per-agent enable/disable), [rulesync](https://github.com/dyoshikawa/rulesync), [agentsync](https://github.com/dallay/agentsync), [ai-rules-sync](https://github.com/lbb00/ai-rules-sync), [agent_sync](https://github.com/yelmuratoff/agent_sync), [block/ai-rules](https://github.com/block/ai-rules) (corporate-backed, ~106★). They distribute config; none audit it.

### Audit/linter tools (inward — "what is this repo feeding my agent")

A small ecosystem emerged Jan–Apr 2026; multiple independent attempts, none with traction:

| Tool | Stars | Created | Angle |
|---|---|---|---|
| [AgentLinter](https://agentlinter.com/) ([repo](https://github.com/seojoonkim/agentlinter)) | 66 | 2026-02-05 | **Token Budget Estimator** + heatmap for CLAUDE.md/AGENTS.md; `npx agentlinter` |
| [AgentLint](https://www.agentlint.app/) ([repo](https://github.com/0xmariowu/AgentLint)) | 36 | 2026-04-02 | "The linter for your agent harness" — 33 checks, 5 dimensions, scores + fixes |
| [cclint](https://github.com/carlrannaberg/cclint) | 19 | 2025-08-21 | Earliest; Claude Code project files |
| [ctxlint](https://github.com/YawLabs/ctxlint) | 7 | 2026-04-05 | Lints context files against the actual codebase; launch post claims ~74% of a typical AGENTS.md is token waste |
| [akz4ol/agentlint](https://github.com/akz4ol/agentlint) | 3 | 2026-01-11 | "Supply-chain security for AI agent configurations" — the only security-scanner-flavored one |

Read on the cluster: five independent attempts within months is a demand signal; sub-100 stars across all of them means nobody has cracked positioning or distribution. **Genuine absence confirmed: no vendor-shipped tool (Anthropic, GitHub/Microsoft, Cursor) audits or inventories repo-supplied agent configuration *before* it takes effect.** Everything vendor-side is post-load (`/context`, `/memory`, Copilot references/Diagnostics).

### The harness-engineering canon (the conversation an essay enters)

- 2026-02-05 — Mitchell Hashimoto, ["My AI Adoption Journey"](https://mitchellh.com/writing/my-ai-adoption-journey): "I've grown to calling this 'harness engineering.' … anytime you find an agent makes a mistake, you take the time to engineer a solution such that the agent never makes that mistake again."
- Feb 2026 — OpenAI, ["Harness engineering: leveraging Codex in an agent-first world"](https://openai.com/index/harness-engineering/): ~1M-line product built "without any manually written source code."
- 2026-02-17 — Birgitta Böckeler, [martinfowler.com memo](https://martinfowler.com/articles/exploring-gen-ai/harness-engineering-memo.html) (credits Hashimoto for the term).
- 2026-03-10 — Viv Trivedy, [LangChain: "The Anatomy of an Agent Harness"](https://www.langchain.com/blog/the-anatomy-of-an-agent-harness): "Agent = Model + Harness." (Osmani and HumanLayer credit Trivedy with the coinage; Böckeler credits Hashimoto — contested.)
- 2026-03-12 — HumanLayer, ["Skill Issue"](https://www.humanlayer.dev/blog/skill-issue-harness-engineering-for-coding-agents): "it's not a model problem. It's a configuration problem."
- 2026-04-19 — Addy Osmani, ["Agent Harness Engineering"](https://addyosmani.com/blog/agent-harness-engineering/) (republished on [O'Reilly Radar](https://www.oreilly.com/radar/agent-harness-engineering/) 2026-05-15): "A decent model with a great harness beats a great model with a bad harness."
- Awesome lists: [walkinglabs/awesome-harness-engineering](https://github.com/walkinglabs/awesome-harness-engineering) (~3.1k★) and two competitors, all created within days of each other (2026-03-29/30).

Nobody in this canon addresses the ownership/consent question — the conversation is entirely about making harnesses *effective*, not about who gets to install one in whose agent.

---

## 7. What this means for omakase (analysis summary)

1. **The spine is the product.** Zero-footprint personal overlay + import + central lefthook gates + status panel survived a four-lens adversarial review intact; the documented real-world demand (people hand-hacking CLAUDE.md out of shared repos) is exactly what it serves.
2. **Gates were always solvable by config.** lefthook ships the per-developer override natively: `lefthook-local.yml` with `skip: true` / `exclude_tags` ("Don't forget to add the file to `.gitignore`" — [local config docs](https://lefthook.dev/usage/features/local/)), plus `LEFTHOOK_EXCLUDE` and `LEFTHOOK=0` ([env docs](https://github.com/evilmartians/lefthook/blob/master/docs/usage/envs/LEFTHOOK.md)).
3. **Override = managing native knobs, not fighting git.** On Claude Code, per-artifact decline already exists (§3: `skillOverrides`, `claudeMdExcludes`) and writes to `settings.local.json` — a file the vendor itself auto-gitignores. Omakase's override is a management surface over those keys; where no knob exists (CC hooks; all of Copilot), flag-and-report plus upstream advocacy. Git-layer masking (sparse-checkout, skip-worktree) is dead on arrival per git's own docs.
4. **The inventory should be an audit, framed by cost.** "What does this repo feed your agent, from which source, at what token cost" — the §6 linter cluster proves demand and proves the lane is uncolonized; the vendor surfaces are post-load and fragmented; the pre-trust audit does not exist anywhere.
5. **The essay's lane is open and timely.** The security community already says "treat AI config like code" (§5) but no one has drawn the developer-tooling conclusion: agent config should inherit the consent norms of executable config (the direnv lineage), not the silent-apply norms of settings (the editorconfig lineage). The Repello piece (one week old) and npm v12 (next month) date-stamp the moment. Concessions required for the essay to survive fact-checking: credit CC's existing knobs and prompts (§3); concede gate-shadow rules, security directives, and automation-fed conventions as legitimately project-owned; scope claims to local surfaces (Copilot code review reads the base branch server-side — no local tool can touch it).
