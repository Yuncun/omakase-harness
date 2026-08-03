# Changelog

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the
project uses semantic versioning. Versions before 0.9.0 are in the git history.

## [Unreleased]

### Added
- **Advisory checks at session start** (#218): a harness can declare
  `advisory:` blocks in `omakase.manifest` — named checks (`run:`, optional
  `purpose:`) that run when a session starts, speak when something needs
  attention, and never block. The exit code is ignored; stdout is relayed
  behind an `omakase[<name>]:` prefix (capped, the cut announced) and
  stderr is discarded; each check is killed after 10 seconds so a hung
  check costs its own budget, not the session. A missing or corrupt
  manifest means silence (fail-open, the opposite of gates, on purpose).
  Validation at init matches gates — a malformed block or a `run:` naming
  an unshipped payload script refuses the whole harness — and init names
  the declared advisories at consent time. Fires on Claude Code's
  SessionStart wiring; Copilot CLI has no session-start wiring yet (same
  limitation as the session-start heal).

### Changed
- **One readable home for init's voice** (#179 D4): every user-facing
  sentence `internal/overlay` can print (init / remove / diff / source /
  record / hook) now lives in `messages.go` as a named constant, and an
  AST-walking test fails the build on any stray string literal in a print
  call — the one-file rule is a deterministic check, not a convention.
  Output is unchanged.
- **The gate-wiring refusal no longer mis-blames the wiring** (#49): when a
  gate references a script the payload does not ship, the message now names
  the likelier cause for an adopter — an omakase install older than the
  harness expects — with the update command, before the fix-the-wiring
  instruction. The original incident sent a user off to edit wiring that
  was correct.
- **The pre-v2 ledger rotation is silent** (#49): an internal store-format
  migration the user cannot act on no longer prints a notice that read as
  if something went wrong. The rotation itself is unchanged.
- **Solution-first wording pass over init/heal messages**: the long
  warnings (drift, kept-file, tracked-collision, downgrade, incumbent,
  linked-worktree) now state the fact in one clause and go straight to the
  command that fixes it; mechanism explanations ("git overwrites ignored
  files on checkout") are cut. The stale-edited-file notice drops its
  WARNING shout — it has a calm action, so it reads as one.

## [0.30.0] — 2026-08-02

### Changed
- **One install story — the plugin is folded into the brew install (#211).**
  `brew install yuncun/tap/omakase` is now the whole onboarding: every
  `omakase init` that leaves a real install behind also installs and
  refreshes the agent-facing skills into the user-level skill folders
  both hosts read (`~/.claude/skills/`, `~/.copilot/skills/`, each only
  if that host's config dir exists — and `init --help` or a refused init
  writes nothing). The
  refresh is version-guarded — an older binary never rolls newer skill
  files backwards — and a same-named skill directory omakase does not own
  is never touched. There is no plugin to install or update anymore.
- **Skills renamed with the `omakase-` prefix**: `/omakase-init`,
  `/omakase-status`, `/omakase-remove`, `/omakase-author`,
  `/omakase-add-gate` (formerly `/omakase:init` etc. on Claude Code and
  flat `/init` etc. on Copilot). User-level skills live in one flat
  machine-wide namespace, and bare `init` / `status` collide with Claude
  Code built-ins. The skills now run `omakase` from PATH directly.
- **The session-start heal moved off the plugin**: init wires a
  `SessionStart` hook into Claude Code's user settings
  (`~/.claude/settings.json`) alongside the statusline — same consent
  (running init: only a run that left a real install behind writes
  anything), same merge discipline (backup before write, an unreadable
  file refused loudly, existing hooks appended to, never replaced; a
  second wiring write replaces the previous `.omakase-bak`, whose
  contract is "undo the write just made", not "pre-omakase archive").
  The wired command derives the stable binary path from the environment
  at session time and self-guards on it existing. On Copilot CLI the
  plugin-era session hook is gone with the plugin — the heal rides git
  events there; Copilot's own hooks mechanism (`~/.copilot/hooks/*.json`)
  is a candidate once verified (#211).
- **The status page is minimalist — a program of respite against
  overinformation**: the default page is now banner → facts line ("N files
  injected · 0 committed · invisible to git") → the STEERING stack → guards
  → the LOADED EVERY TURN table → one dim footer hint. The steering stack is
  new: one band per layer of steering — you / harness / project — sized
  against each other, solid cells for bytes read every turn and hollow cells
  for bytes that wait until needed; an empty band prints "— none", and the
  stack renders on the not-installed page too (the empty harness band is the
  install prompt). Cut from the default page, all reachable via `--all`: the
  identity line (source · base version · install path), the labeled
  zero-footprint sentence, the LOADS ON DEMAND breakdown, per-host totals,
  host tags, the token-estimate caveat, the unmanaged count line, and the
  restore/undo footer. The guards chart lost its column-header row and its
  repeated defaults — a gate row is hook, name, verdict (`✓ 15d` / `✗ 2h` /
  `—`), with a dim trailing detail only for a declared purpose or
  non-default scheduling (cached / glob scope).
- **The status page now shows what an agent reads, not a wall of files**
  (#179 decision 1; absorbs draft PR #206): below the guards chart, the
  harness renders as the instruction layers the detected host loads — one
  line per layer with an estimated token cost drawn as a bar, cost-first,
  the file's opening words quoted, per-directory CLAUDE.md walk-up included,
  CLAUDE.md/AGENTS.md symlink pairs counted once (rendered as one
  `CLAUDE.md → AGENTS.md` row). Rules scoped by frontmatter globs
  (`paths:`/`applyTo:`) and skill bodies aggregate under a LOADS ON DEMAND
  section instead of inflating the every-turn total; files the current host
  never reads collapse to one closing line; with no host detected the
  header shows each host's per-turn total. Rows are data only — bar,
  number, path, quoted excerpt — with no annotation prose. Injected files
  get individual lines only when something is wrong (missing / drifted /
  toggled off — NEEDS ATTENTION); untracked local config is one count line.
  The full per-file inventory moved behind `omakase status --all` and is
  now a width-aligned PATH/KIND/STATE table with the install source stated
  once in the group header, never per row. The banner no longer glues the
  base payload version to the harness name (it read as the harness's own
  version); the identity line still states it as `base omakase X`.
  `omakase status --show <path-or-fragment>` prints any layer in full.

### Removed
- **The plugin machinery** (#211): `.claude-plugin/` (manifest +
  marketplace), the plugin `hooks/hooks.json` + `hooks/session-start.sh`,
  and the `bin/` call-through shims (a copy survives as test plumbing under
  `tests/bin/`). An installed omakase plugin becomes inert after updating —
  uninstall it with `/plugin`; the skills arrive user-level at the next
  `omakase init`. The enterprise plugin-push channel goes with it (it could
  no longer deliver a binary since #183 anyway); re-opened on demand.
- **`omakase block` / `omakase unblock`** (#207): the per-item hiding of the
  repo's own committed agent config is gone — the verb, the `/omakase:block`
  skill, the sparse-checkout machinery, the blocked-row annotations on the
  status page, and the re-hide heals. The mechanism fought git (both criticals
  in the 0.29.1 hardening round were block's skip-worktree collisions, and
  substituting a blocked file was structurally impossible on sparse-checkout,
  #197), and the product sticks to carrying and loading a harness. A repo with
  an active block from 0.29.x: run `git sparse-checkout disable` and delete
  `.git/omakase/blocked` to get the hidden files back — and do it BEFORE
  running `omakase remove` there, which no longer knows the sidecar and may
  misread it as an old install.

## [0.29.1] — 2026-07-29

### Fixed
- **A stale entry point can no longer downgrade the machine-wide binary**:
  the self-install that runs on every `omakase init` now asks the existing
  machine copy its version and never replaces a newer one — before, even a
  REFUSED init from an old plugin rolled back the one binary every repo's
  hooks execute, then printed "Nothing was changed".
- **Blocked items show correctly on the status page again**: 0.29.0's
  masked-path filter used the same git marker blocking uses, so every
  blocked item was dropped from the committed list and mislabeled "no
  longer committed here". Blocked rows now stay, with their real state.
- **The heals can no longer defeat a block**: a blocked path that omakase
  itself had once placed was treated as a deliberately adopted file, and the
  session-start/checkout heal copied harness content over it — un-hiding the
  one file the user said no to and dirtying the index. Blocked paths are now
  exempt everywhere the adopted-path logic applies.
- **A bare re-init keeps an adopted (skip-worktree) file working**: init now
  treats an adopted path as an ordinary injected file — no false collision
  warning per file, no tracked skip, ledger row kept — so the heal and the
  missing-file check keep protecting it after the next init instead of only
  until then.
- **The full pushed ref list reaches git-lfs and the gate scoper**: an
  internal 1MB cap could truncate very large pushes (e.g. thousands of
  tags), which would have made git-lfs silently skip uploading objects for
  the refs past the cut. No cap; one buffered copy feeds both readers, and a
  test pins that byte-for-byte.
- **init-then-remove no longer half-uninstalls git-lfs**: init saves the
  stock git-lfs hooks it displaces and remove restores them byte-perfect.
- **Worktree re-point guard covers the payload override and bare-repo
  layouts**: the `OMAKASE_PAYLOAD` door now refuses from a linked worktree
  like an explicit different source does; in a bare-repo-plus-worktrees
  layout the first worktree counts as the main checkout so the harness can
  still be re-pointed from somewhere; and spelling differences (`.git`
  suffix, trailing slash) of the same source no longer refuse falsely.
- **The downgrade refusal's instructions are now safe to follow**: the
  message names the remembered source to re-init from (the old wording's
  remove-then-init deleted the only copy of it), the guard no longer fires
  on repos that merely commit an `.omakase/VERSION` with no omakase
  installed, and a refused init no longer rotates the run ledger while
  claiming nothing changed.

## [0.29.0] — 2026-07-29

### Removed
- **The shim fetch tier** (#182): a plugin-only install no longer downloads
  the pinned release binary behind the scenes. The shims resolve locally —
  override, dev build, `dist/`, PATH, the stable machine copy — and when
  nothing resolves they print one line:
  `install it with: brew install yuncun/tap/omakase`. This is the field's
  plugin pattern (a plugin assumes the CLI, never fetches it), and it removes
  the standing machinery the fetch required: the pinned version + 8 baked
  hashes in `bin/lib-omakase-bin.sh`, and the re-pin PR after every release —
  releases are now one PR and a tag. Uninstall was already offline; now
  everything is.

### Added
- **`omakase block <item>` / `omakase unblock <item>`** (#193): per-item
  consent over the repo's OWN committed agent config. Blocking hides the
  item — an instruction file, a skill or agent directory, a prompts file, a
  hooks script — from this clone's working tree via non-cone git
  sparse-checkout, so no host loads it: agents discover steering files by
  presence on disk, and this works identically for Claude Code and Copilot
  CLI, including the surfaces neither host can switch off natively
  (instruction files, hooks). git still tracks the file: nothing is deleted,
  commits/pulls/pushes are unaffected, and unblock restores it byte-perfect.
  A run without `--yes` only explains what would happen. Applies across all
  worktrees; `omakase status` shows blocked rows and names the unblock
  command; `omakase remove` restores everything blocked. Works with or
  without a harness installed. v1 refuses repos already running their own
  sparse-checkout.

- **Statusline wired by default** (#123 item 5): a real `omakase init` now
  fills each host's empty `statusLine` slot (Claude Code `~/.claude`,
  Copilot CLI `~/.copilot` — only where the config dir already exists) with
  the segment block, announcing the write and backing up any prior settings
  file. An existing status bar is never touched, and a wired machine stays
  silent — the steady state prints nothing. `init --help` and the
  nothing-remembered first run install nothing and wire nothing.
  `omakase statusline --wire` stays for explicit re-wiring and for the
  occupied-slot instructions.
- **Resolved commit in the source line** (#38): every `--source` install and
  bare re-run now prints the commit the payload came from —
  `omakase: source '…' (name: …, commit a1b2c3d) cached at …` — so a bare
  re-run's silent refresh-to-remote-HEAD names exactly what was just
  adopted.

### Fixed
- **Scoped gates now check what you are actually committing or pushing**
  (#196, #186): a pre-commit gate's `glob:` matches the staged files (it
  used to diff branch history, so a fresh clone's first commit skipped its
  gates entirely and any old change kept firing them forever), and a
  pre-push gate's `glob:` matches the exact ranges git hands the hook —
  including when git-lfs is installed, whose forward used to swallow those
  ranges before the gates saw them. When a push cannot be scoped the gate
  runs rather than skips.
- **A stale plugin or binary can no longer silently roll a repo's omakase
  files backwards** (#189): `init` refuses when its payload is older than
  what the repo already runs, naming both versions and the fix.
- **A deliberately overlaid copy of a committed file is no longer reported
  as a collision** (#195): repos where a harness copy serves in place of a
  committed file (via git's skip-worktree) stop printing a false warning
  per file on every checkout and commit, and `status` no longer lists those
  files twice in contradicting sections.
- **A harness file inside a directory the project already uses no longer
  hides the project's own new files there** (#195): the ignore entries go
  in file-by-file for any top-level directory with tracked content, instead
  of excluding the whole directory from `git status`.
- **git-lfs forwarding is now visible** (#190): the pre-push and
  post-checkout hook files carry a line saying they also run
  `git lfs <hook>` (the stock git-lfs stub was displaced, not disabled), and
  `init` prints one line when it displaces a stock git-lfs hook. Hooks
  written by an older omakase are still recognized as omakase's own, so the
  upgrade never reads as "foreign hooks".
- **A linked worktree can no longer silently re-point the repository's
  harness** (#184): hooks, the exclude block, and the remembered source live
  in the shared git common dir, so `init` with a source that differs from
  the one the repo already runs now refuses from a linked worktree — exit 1,
  nothing changed, message naming the main checkout to run it from. The bare
  refresh and the same-source re-run keep working in any worktree (the heal
  flow), and a first install from a worktree is still allowed (nothing to
  clobber). Previously the second harness overwrote the exclude block,
  hooks, and remembered source for the main checkout and every sibling
  worktree, unprompted.

- **Wire target honors `XDG_CACHE_HOME`**: the statusline block now points
  at `hook.StableBinPath()` — the same path the dispatchers exec and every
  init refreshes — instead of a hardcoded `~/.cache`, which left the bar
  dark on machines that set `XDG_CACHE_HOME`.
- **Payload symlinks that escape the repo are refused** (#30): a harness
  payload carrying a symlink with an absolute target, or a relative target
  that climbs out of the repo, refuses the whole install before anything is
  placed. Installed verbatim, such a link would read or write outside the
  repo while hidden from `git status` and re-materialized into every
  worktree by the heal. In-tree relative links (`CLAUDE.md -> AGENTS.md`)
  still round-trip. A source symlink shadowing a base-payload directory in
  the merge is refused the same way.
- **Editor/OS cruft is never placed** (#31): `.DS_Store` and `*.bak` files
  in a payload are skipped — never placed, ledgered, or snapshotted.
- **Untrusted manifest fields are sanitized** (#32): control bytes (ANSI
  escapes, BEL, backspace) embedded in a source's `omakase.manifest`
  name/version/recommends are stripped before those values reach the
  terminal, closing an output-spoofing hole.

## [0.28.0] — 2026-07-27

### Added
- **Session-start heal** (#164 C5, narrow scope): the plugin now ships
  `hooks/hooks.json` with a `SessionStart` hook — both hosts read it — that
  runs `omakase hook session-start` when an agent session opens. It repairs
  a wiped/partial overlay the way `post-checkout` does, closing the one gap
  git events can't see (overlay deleted, then a new session starts, which
  previously ran unguided until someone noticed). Contract: silent and
  instant when there is nothing to do, one stdout line when files were
  restored, exit 0 always, and no binary fetch at session start.

### Changed
- **The base payload shrinks to content the product owns** (#172):
  `omakase.manifest` + `.omakase/VERSION`, nothing else. The branded status
  banner now renders inside `omakase status` (no script exec of repo
  content); the worktree guard is reclassified as **harness policy** and
  moves to the dogfood harness (`omakase-harness-harness/payload/.omakase/bin/`), which
  recommends its wiring at install — the omakase binary itself no longer
  mentions worktree discipline anywhere.
- The shims no longer export `OMAKASE_BASE_PAYLOAD` — every pinned binary
  since 0.27.0 embeds the base payload. The env var remains honored as a
  dev/test override.
- The dogfood harness directory `harness/` is renamed
  `omakase-harness-harness/` — the long name is deliberate teaching: the
  omakase repo carries a harness for omakase development, and the folder
  says so. Install path is now
  `omakase init Yuncun/omakase-harness/omakase-harness-harness`.

### Removed
- From the base payload: `omakase-worktree-guard.sh` (now in the dogfood
  harness), `omakase-banner.sh` (now in the binary), and the example
  `markers` gate (`example.sh` + its `gate:` block). The example gate had
  been unreachable in production since #123 — bare init places nothing, and
  a source install's manifest shadows the base manifest; its conflict-marker
  script survives as a worked example in `docs/authoring.md`. Existing
  installs: re-run `omakase init` (the orphan sweep clears retired scripts).

## [0.27.0] — 2026-07-27

### Fixed
- **A standalone binary can now install harnesses.** brew, release-tarball,
  and `go install` binaries refused `omakase init <owner/repo>` because the
  base harness payload only shipped inside the plugin (#168). The base
  payload is now embedded in the binary at build time and extracted to the
  machine cache when no on-disk copy exists; the plugin's shim handoff and
  the dev loop (edit `payload/`, re-init) are unchanged and still win.
- **Copilot CLI host-audit fixes** (#164 C1–C4, C6): each skill's primary
  command no longer relies on `${CLAUDE_PLUGIN_ROOT}` (Copilot doesn't set
  it — the command was broken there); the worktree guard's deny now carries
  both the top-level and nested output shapes so Copilot can't silently
  drop it (fail open); `statusline --wire` stops writing Copilot's retired
  `STATUS_LINE` feature flag; the dogfooding harness gives Copilot agents
  the same rules as Claude agents (two had been lost — parity is now
  tested); the superpowers example no longer ships an inert
  `.github/copilot/settings.json` (Copilot has no project-scoped settings
  file — per-repo plugin enablement is Claude-only, now said plainly).
- **`status --global` sees Copilot's user-global config** (#164 C7):
  `~/.copilot/copilot-instructions.md` (the peer of `~/.claude/CLAUDE.md`),
  `~/.copilot/settings.json`, and `~/.agents/skills/` are now listed; the
  page's GLOBAL count line names all three roots.
- **`omakase status <path>` no longer silently ignores the argument** and
  reports on the current directory anyway; a stray argument is a loud
  error now.
- **`status` now sees the whole committed agent surface.** The committed
  scan missed nested instruction files (`app/CLAUDE.md` at any depth) and
  the `.agents/skills/` project skill root — on a repo with a deep
  committed surface it under-reported by ~99%. Nested `CLAUDE.md`/
  `AGENTS.md` now classify as docs and `.agents/skills/*` as skills;
  near-miss names like `NOTCLAUDE.md` stay excluded. (#165)

## [0.26.0] — 2026-07-26

### Changed
- **State stores only what can't be derived.** `placed.tsv` shrinks to two
  columns (path, sha256): kind is derived from the path, the source label
  from the remembered source, and file disables move to a `disabled-files`
  marker file — the same shape as `disabled-gates` and `kept/`, so files
  and gates now share one disable mechanism. Old 5-column ledgers are
  still read; the first init or toggle rewrites them.
- **`omakase init` refuses instead of overwriting.** A file in the way —
  one omakase never placed, or a placed file you edited — now refuses the
  whole init and names each path (edits point at `omakase diff` /
  `--keep` / `--restore`) before anything is touched. Unedited files
  still update in place and `.omakase/` machinery still heals to
  canonical, so init remains the update verb. Nothing is ever overwritten
  with content loss, so the `clobbered/` backup tree is gone.

### Removed
- The lefthook-era migration machinery (pre-gate-module snapshot
  detection, the hook's stale-snapshot block, init's pre-#98 artifact
  cleanup, remove's skeleton `lefthook.yml` sweep). The probe's
  "needs migration" fact narrows to "manifest unreadable" — the one thing
  it still proves. Detection of a project's own lefthook/husky config is
  unchanged.

## [0.25.0] — 2026-07-25

### Removed
- **The interactive menu is gone — both surfaces** (#156): the terminal screen
  (`internal/tui` and the vendored, patched bubbletea it required) and the MCP
  menu server (`omakase mcp`, `internal/mcpserver`, `bin/mcp.sh`). A bare
  `omakase status` on a terminal now prints the same static page as everywhere
  else; `--plain` is kept as an accepted no-op for scripts. Toggles,
  keep/restore, and diff were already plain flags and are unchanged. If a
  future interactive surface is wanted, it will be designed fresh rather than
  grown from this one. If you registered the MCP server, unregister it
  (`claude mcp remove omakase`).

## [0.24.0] — 2026-07-24

### Changed
- **Gates are opt-in per harness — zero gates means zero enforcement hooks**
  (#149): a manifest that declares no gate blocks no longer installs the
  `pre-commit`/`pre-push` dispatchers, and init no longer refuses a repo that
  already has its own hooks (husky, pre-commit, …) when there is nothing to
  enforce. The `post-checkout` heal hook is still installed when the slot is
  free; if an incumbent hook occupies it, init skips it and prints the manual
  fallback (bare `omakase init` after a checkout). Re-initing a harness that
  dropped its gates removes omakase's own dispatchers — never a foreign hook.
  `omakase status` and the init verdict read "no gates declared" instead of
  reporting missing hooks as a problem. A missing or unparseable manifest keeps
  the full wiring (fail closed).

### Fixed
- **Symlinked directories broke the git exclude** (#148): the exclude derivation
  appended a trailing slash to any path that resolved to a directory, but git
  matches trailing-slash patterns against real directories only — a symlink is
  a blob to git, so symlinked payload directories were never excluded. Symlinks
  no longer get the slash.
- **Steering-only installs no longer warn "commits will be blocked"** when the
  stable binary is missing: the warning now fires only when an enforcement hook
  was actually installed.

## [0.23.2] — 2026-07-22

### Fixed
- **Brew-installed binary was killed by Gatekeeper** on macOS: Homebrew
  quarantines cask downloads, and the unsigned binary got SIGKILLed on first
  run. The cask now strips the quarantine attribute post-install (GoReleaser's
  documented pattern for unsigned binaries); real signing/notarization stays
  the eventual fix. 0.23.1's cask has the bug — this ships as 0.23.2.

## [0.23.1] — 2026-07-22

### Added
- **Homebrew install** (#132): each release now updates a cask in
  `Yuncun/homebrew-tap`, so `brew install yuncun/tap/omakase` is the leading
  install line in the README.

### Changed
- **Publishing is now the tag push.** The draft-review step is gone: the cask
  must point at a published release, so the release publishes in the same
  unattended run. Review happens before tagging, not after.

## [0.23.0] — 2026-07-21

### Removed
- **The end-of-turn notice is gone, both hosts** — the `stop-notice` verb, the
  placed Copilot `agentStop` hook (`.github/hooks/omakase.json`, new in 0.22.0),
  and init's Claude Stop-hook pointer. A line printed at every turn's end was
  too much surface for what it said; the status bar and `omakase status` carry
  the same facts. On the next `init`, repos that got the 0.22.0 hook file have
  it removed like any dropped payload file.

## [0.22.0] — 2026-07-20

### Added
- **Live status bar** (#85): the statusline segment now shows the location as
  `repo:worktree` in linked worktrees and appends `· <gate> 12s…` while a gate
  runs — a heartbeat file the gate runner writes, shown only while the gate's
  process is alive, so a killed gate can never stick. New `omakase statusline
  --wire` writes the statusLine block into `~/.claude/settings.json` and (with
  the STATUS_LINE feature flag) `~/.copilot/settings.json` — per host, only
  where the host's config dir exists, with a backup, and never replacing an
  existing bar. `init` now points at `--wire` in one line only while a host is
  missing a bar (replacing the six-line manual-wiring stanza).
- **Copilot CLI end-of-turn notice** (#85): the base payload places
  `.github/hooks/omakase.json` — an `agentStop` hook running the new
  `omakase stop-notice --plain` (bare text; the default output stays Claude
  Code's Stop-hook JSON envelope). Documented caveats: the bar reads cached
  facts (a wiped cache = dark segment until the next init; `omakase status`
  is the truth surface), and Copilot refreshes its bar per response, so the
  live gate counter is coarser there.
- **Gate blocks take an optional `purpose:` key** (#131): what the gate enforces, in
  the author's words (≤6 words, concrete). The status guards table renders it as the
  ENFORCES column; when any gate declares one, the scheduling mechanics move to their
  own RUNS column. A manifest with no `purpose:` lines renders exactly as before. The
  repo's own harness and the base example gate now declare purposes.
- **`status --global`**: the full personal-config listing (`~/.claude` + `~/.copilot`).
  The status page itself now collapses the GLOBAL group to one count line — the list
  repeats identically in every repo and drowned the page on machines with a large
  personal setup (#131).

### Changed
- **Status legibility batch** (#131): the harness header prefers the manifest's
  declared `name:` over the source path's last folder; github.com sources with a
  subpath display as the browsable `…/tree/<ref>/<subpath>` form (clicking the old
  `//` form 404'd; the canonical `//` string is unchanged everywhere internal); and
  the INJECTED group ends with an edit-affordance line (keep/restore, `/omakase:author`).
- **`examples/starter-harness` is now `harness/`, named `omakase-harness-harness`** —
  it was never a starter template (there is no base template; the base machinery layers
  in automatically) but this repo's own development harness, and its old name and
  `examples/` location said otherwise. Install line is now
  `omakase init Yuncun/omakase-harness/harness`; existing installs remembering the old
  subpath re-point with one init at the new path.
- **author skill: forking prose steering files from any repo is in scope** (#133) — the
  third-party refusal now names what it always meant: executable content. Instruction
  and rules files from a public repo can be taken into a harness with attribution and a
  portability review; found when a fresh-session dogfood run over-refused
  `microsoft/vscode`'s instruction files.

## [0.21.0] — 2026-07-18

### Added
- **`status` classifies "yours, unmanaged"** (#123): untracked agent config at
  the known paths that is neither committed nor placed by omakase — files that
  exist only in this clone — shown as its own group in both the installed page
  and the no-overlay audit, with the natural offer to add them to a harness
  (the author skill). Harness machinery and Claude Code's own
  `.claude/worktrees/` area never surface there; past 20 rows the elision is
  stated explicitly.

### Changed
- **`status` in a repo with no overlay is now a deliberate presence-only
  audit** (#119, #123). It lists the agent config that exists — committed in
  the repo plus the user's global config — states its boundary ("known paths
  for known tools — not exhaustive; a file can be present and never read"),
  drops the empty Injected section, and points the install line at
  `omakase init <owner/repo>`. Presence only: it never claims to know what a
  host actually reads.
- **Bare `init` with nothing remembered now installs nothing** (#123). It prints
  one line pointing at `omakase status` and exits 0; the wording keys on the
  placed ledger, so a harness installed without a remembered source (an
  `OMAKASE_PAYLOAD` install) is told it has no source to refresh from, never
  that nothing is installed. Previously the plugin path silently installed the
  base machinery and a cache-resident binary errored with an internal path
  ("payload dir not found"). A remembered source still refreshes, and the
  `OMAKASE_PAYLOAD` override still installs, exactly as before.

## [0.20.0] — 2026-07-16

### Changed
- **Gates are declared in the manifest and run by omakase itself; lefthook is
  gone** (#114, #115). A harness declares each gate as a `gate:` block in
  `payload/omakase.manifest` — keys `hook:` (`pre-commit` or `pre-push`),
  `run:` (a command line, `sh -c` from the repo root; non-zero blocks),
  `glob:` (skip when no changed file matches), `cacheable: true` (reuse a
  PASS for the same HEAD) — and the omakase binary runs them at hook time.
  No third-party hook runner is fetched, provisioned, or configured;
  `omakase-gate.sh` and the lefthook config files leave the authoring
  surface; git-lfs hook forwarding stays. The run ledger format and the
  skip switches (`OMAKASE_SKIP_<NAME>=1` per gate, audited) are unchanged.
  **Fail-closed migration**: hooks written by a pre-0.20 install BLOCK
  commits and pushes until a bare `omakase init` re-consents to the
  harness under the new format — an upgrade can never silently disable
  gates — and `init` refuses a harness that still ships
  `lefthook-local.yml`, and any repo using native lefthook (cooperation
  ended with the runner).
- **One manifest** (#116, #117): `payload/omakase.manifest` now carries the
  harness's identity (`name` required; `version`, `recommends` optional)
  as well as its gate blocks. A leftover source-root `omakase.manifest`
  (the old two-file layout) is refused fail-closed at install with
  instructions to move its keys into the payload manifest.
- **`examples/sample-harness` replaced by `examples/starter-harness`** — the worked
  example is no longer a toy: it is the harness omakase development itself uses
  (self-hosted via the subfolder-source install). It places agent rules for
  Claude Code and Copilot and wires three real gates: `block-marker` (refuse a
  staged scratch marker) and `go-checks` (gofmt + go vet on staged Go files) on
  pre-commit, and a cached `go-test` on pre-push.

### Added
- **`omakase record <name>`** — record an out-of-band PASS for a deferred
  gate at the current HEAD. The pattern for a check that cannot run inside
  a hook (an agent review, a visual verification): a blocking `run:` plus
  `cacheable: true`, cleared by the out-of-band job via `record`.
- **`OMAKASE_SKIP_GATES=1`** — skip every gate for one run (audited),
  alongside the existing per-gate `OMAKASE_SKIP_<NAME>=1`.
- **`/omakase:author` skill** (#120) — walks an agent through building a
  custom harness (or converting a repo's existing agent files into one):
  layout, the one manifest, portability review, gate wiring via
  `/omakase:add-gate`, a test cycle, publishing.

## [0.19.1] — 2026-07-14

### Added
- **Adopt a harness from a subfolder of a repo** (#103):
  `omakase init owner/repo/subpath[#ref]` (GitHub Actions' `uses:` shape) and a
  `//subpath` suffix on any `--source` url or local path
  (`--source https://host/x/hub//tools`, `--source /clones/hub//tools`). The
  fail-closed manifest/payload validation runs at the subfolder, never the repo
  root; the canonical `root//subpath` string is remembered so a bare `init`
  refreshes the hub and re-injects the same subfolder; a subpath can never
  point outside the clone (`..`/absolute refused up front); distinct subfolders
  of one hub get distinct source-cache clones. One hub repo can now publish
  several harnesses — no dedicated repo per harness.

## [0.19.0] — 2026-07-13

### Added
- **The edit lifecycle: `omakase diff` + keep/restore** (#98 Part 2). Editing
  a placed file is the expected lifecycle, not misuse: modified →
  `omakase diff` → keep or restore. `omakase diff [path…]` is a new, strictly
  read-only human verb showing what YOU changed, in the forward direction
  (your edit renders as an addition), against the harness version — or
  against your accepted version once you've kept a file. The plumbing
  actions live on status, siblings of `--disable`:
  `omakase status --keep <path>` accepts the on-disk version as yours (the
  accepted copy is stored under `$OMK/kept/`, the ledger hash moves to it,
  and everything — status, statusline, stop-notice — reads green again:
  green means "matches what you've consented to");
  `omakase status --restore <path>` puts the harness's version back and
  clears the mark, and works on plain drift too. Both resolve names exactly
  like `--disable` and refuse machinery and tracked paths with exit 2.
- **Kept files survive every lifecycle verb**: repair `init` and
  `init <new-source>` leave them untouched (a missing kept file is refilled
  with the ACCEPTED copy; the harness version of a kept path the new source
  no longer ships is carried across the snapshot rebuild so `--restore`
  keeps working offline); the checkout heal refills from the accepted copy;
  a disable/enable cycle round-trips the accepted version; `remove` leaves
  kept files on disk (they are yours) and reports them. Kept rows render as
  their own state — `kept (yours)` — on the status page, and the verified
  init verdict counts them.
- **Two-tier help**: `omakase --help` lists the four human verbs (init,
  status, diff, remove) first and groups hook/statusline/stop-notice/mcp
  under "commands used by your tools, not by you".

### Changed
- **The status page's committed section is retitled** "The project's harness
  (committed — managed by git, not omakase)" — the two-layer naming: the
  project's harness (committed, git-managed, omakase lists but never
  touches) vs your harness (injected, omakase-managed).
- **omakase owns `.git/hooks`: permanent dispatcher hooks, lefthook demoted to
  gate-runner** (#98). Each hook file (pre-commit, pre-push, post-checkout) is
  now a permanent ~5-line dispatcher that execs the machine-wide binary copy
  with `omakase hook <name>`; only `init` and `remove` ever write `.git/hooks`,
  atomically, and nothing at hook time rewrites them — the entire #96 class
  (hook files corrupted mid-run, worktree sessions racing each other's hooks)
  is gone. At commit/push time the binary verifies the harness is complete
  (fail closed, `LEFTHOOK=0` does not bypass it) and runs the wired gates
  through the pinned lefthook with explicit config (`LEFTHOOK_CONFIG` +
  `--no-auto-install`); a repo shipping its own `lefthook.yml` keeps lefthook's
  default merge, so its jobs still run alongside the harness's. A missing
  binary or lefthook blocks with a one-line fix; a checkout never fails.
  `git lfs <hook>` is still forwarded where a displaced stock LFS hook would
  have run it. The worktree self-heal is native Go inside
  `omakase hook post-checkout` (same contract: fill missing enabled files,
  never overwrite, never touch tracked paths, warn on drift and collisions).
  The machine-wide copy at `~/.cache/omakase/bin/current/omakase` (#97) is now
  load-bearing: `init` verifies it after writing dispatchers, and the status
  probe checks it on every run.
- **The hooks proof is byte-equality, with the cause pinned** — the status
  probe accepts only a hook file byte-equal to the dispatcher (a substring
  match would call a clobbered hook healthy) and distinguishes absent vs
  clobbered-by-another-tool vs binary-missing as separate facts. An
  `lefthook install -f` from an npm postinstall is detected, never silently
  re-armed; the next explicit `omakase init` repairs it.
- Migration is one `omakase init`: it replaces the old lefthook stubs + guard
  blocks with dispatchers and deletes the retired per-repo machinery
  ($OMK scripts, the lefthook.yml heal snapshot, `.git/info/lefthook.checksum`,
  the untracked skeleton `lefthook.yml` in every worktree). Hooks live once in
  the shared git dir, so one init converts all worktrees.
- **`init`/`status`/`remove` shims fail closed when no omakase binary can be
  resolved** — recovery guidance on stderr (naming the `OMAKASE_BIN` escape
  hatch; `remove` never downloads, so it asks for a local or already-cached
  binary) and exit 1, matching `mcp.sh`. A silent bash fallback would mask
  binary-distribution failures.

### Removed
- **The hook-time script trio and the lefthook install machinery** (#98):
  `bin/ensure-present.sh`, `bin/install-guards.sh`, `bin/verify-overlay.sh`
  (and their embedded template copies), every `lefthook install` / stub-sync /
  `.git/info/lefthook.checksum` reliance, the `lefthook.yml` skeleton and its
  heal, and the payload's post-checkout heal job (heal is native now). The
  `/lefthook.yml` exclude entry is no longer written. Gate scripts
  (`omakase-gate.sh`) stay sh, unchanged.
- **The v1 bash fallback bodies (`bin/legacy/`)** — the Go binary has been the
  engine for every verb since the shim cutover, and 0.18.1's self-provisioning
  shims fetch the pinned, checksum-verified release binary when nothing
  resolves locally (Phase 7 of the v2 design).
- **The bash-vs-Go parity suites** (`tests/status-parity.test.sh`,
  `tests/init-remove-parity.test.sh`) — their oracle is gone; the two behaviors
  they alone covered now live in `tests/scorecard.test.sh` (I2/I3) as golden
  expectations against the Go binary's output.

## [0.18.1] — 2026-07-10

### Added
- **Plugin/clone installs without Go now self-provision the omakase binary.** `init`,
  `status`, and the new `mcp` shim fetch the pinned, checksum-verified release binary
  once per machine — cached at `~/.cache/omakase/bin/<version>/` (`XDG_CACHE_HOME`
  respected, mirror overridable via `OMAKASE_RELEASE_BASE_URL`) — instead of dropping
  straight to the v1 bash fallback. `remove` never fetches but reuses an
  already-cached binary.
- **`bin/mcp.sh`** — a shim for `omakase mcp` with a stable path, so `claude mcp add
  omakase -- /path/to/omakase-harness/bin/mcp.sh` works in plugin installs where no
  binary is on PATH.
- **`omakase --version` identifies plain `go build`s** — without release ldflags it
  now backfills the module version Go stamps on `go install …@vX.Y.Z` and the VCS
  revision/time (with `+dirty`) a checkout build carries, instead of reporting
  `dev (commit none, built unknown)`.
- **`omakase status` reads YAML block-scalar wiring** — a gate wired with
  `run: |` / `run: >` now resolves its gate name, cached/scope description, and
  ledger verdict exactly like a single-line `run:` (previously the chart showed a
  bare `|` and lost the verdict join).

### Fixed
- **`status` sees a self-provisioned lefthook.** The guards chart resolved lefthook
  through a 3-tier walk that predated binary self-provisioning, so on machines whose
  lefthook lives only in the omakase cache — exactly the zero-setup adopter — it
  rendered the false `lefthook not resolved - gates are not running` note while the
  gates ran fine (#72). status (Go and the legacy bash oracle, in lockstep) now
  resolves through the same shared tier walk init and remove use, cache tier included.
- **Hooks fail closed when lefthook goes missing.** lefthook's generated hook stub
  fails OPEN when no binary is findable ("Can't find lefthook", exit 0) — the wired
  gates silently skipped. The fail-closed block omakase splices above the stub now
  resolves lefthook by omakase's own tiers first: a cache-only lefthook is healed by
  exporting it as `LEFTHOOK_BIN` (the gates then actually run); nothing found anywhere
  BLOCKS the commit/push with restore guidance instead of skipping. `LEFTHOOK=0`
  still skips — that's an explicit choice, not a silent one (#72). Re-arm existing
  repos with a bare `omakase init`.
- **A fetched / PATH-installed release binary can locate the base payload again.** v0.18.0's
  fetched/PATH-installed release binary could not locate the plugin's base payload — `init
  --source` (and bare init) failed with `failed to copy the base payload into the merge staging
  dir` (#70). The bin/ shims now export `OMAKASE_BASE_PAYLOAD` and the binary honors it before its
  binary-relative default; a missing base payload now fails fast, naming the path it tried.
- **`.git/info/exclude` entries are root-anchored** (`/.omakase/`, not `.omakase/`).
  Unanchored gitignore patterns match at any depth, so the overlay was also hiding a
  project's own same-named nested paths (e.g. `payload/.omakase` in a harness repo).
  A bare re-`init` rewrites the block with anchored entries.
- **Offline first runs fail fast.** Both binary fetch helpers (omakase, lefthook)
  bound the connection phase — `curl --connect-timeout 5` / `wget -T 15` — so a
  machine that can't reach GitHub falls back in seconds instead of hanging on the
  OS connect timeout.

### Removed
- **`tools/build.sh`** (and its test): the dist-bundle build it performed has had no
  consumer since custom harnesses moved to source installs (`omakase init owner/repo`).

## [0.18.0] — 2026-07-08

### Added — 2026-07-07 the consent menu
- **`omakase status` is now the menu on a real terminal**: every steering file and
  gate is a row a human can toggle (arrows + Enter/Space). Pipes, `--markdown`,
  and `--plain` keep the static page byte-for-byte. `--disable`/`--enable <name>`
  are the scriptable twins; machinery and unknown names refuse. A file toggled
  off stays off across re-init; a disabled gate is recorded in the git dir's
  `omakase/disabled-gates` and skipped VISIBLY at hook time until re-enabled.
  A locally edited file refuses either toggle rather than lose the edits.
- **`omakase mcp`** — a stdio MCP server (binary-only verb) serving the same
  consent menu inside Claude Code / Copilot CLI as native form dialogs (MCP
  elicitation), plus a read-only `status` tool. The menu is one nested
  cascade form: a header row per dev-loop stage (keep as-is / all on / all
  off, cascading over rows left unchanged) with a row per file and gate
  beneath it; `expand` gives every file its own row instead of one row per
  directory. Three disposable list-layout experiments (`variant`: triage /
  preset / sections) ran for live A/B testing and were deleted after the
  form above won.
- First external Go dependencies: bubbletea/lipgloss for the interactive screen
  (vendored with a one-file patch that stops an import-time terminal query —
  provenance and upgrade path in `third_party/bubbletea/PATCH.md`) and the
  official MCP go-sdk.

### Removed — 2026-07-03 slim-cut
- **Reverted to a 3-verb, single-harness overlay** (`init` / `remove` / `status`). A
  YAGNI audit found the layered design below (Phases 3-3.5) had no real user demand
  behind it and cut the whole surface before any of it reached a release: two-harness
  stacking (a second `init <source>` stacking on top instead of replacing, `remove
  <source>` unlayering one harness), the `AGENTS.md` → `CLAUDE.local.md` instruction
  reroute and its `CLAUDE.md` bridge symlink, the v1→v2 migration and mixed-era
  detection, pins (`sources.tsv`, per-source resolved-commit recording), the `update`
  verb, and `enable`/`disable` gate toggles are all gone. `init` is back to plain v1
  semantics: install, repair the same source, or replace a different one (sweeping the
  old source's orphaned files) — it never stacks. `remove` takes no arguments — argv is
  ignored — and is always a bare, total teardown; there is no `remove <source>`.
  Instruction files are placed VERBATIM, exactly as a harness ships them: no reroute, no
  synthesized bridge, no root-slot fallback. `sources.tsv` and the `$OMK/layers/` store
  are deleted; `$OMK/source` (one line, frozen v1 format) is the only remembered-source
  record. `share`/`import` stay removed (cut earlier in this same effort, before this
  entry). `docs/v2-design.md` is marked superseded and kept only as a historical record;
  `docs/reference.md` describes the current contract.
- **Fixed:** `init --source` repairing an already-installed harness while offline used to
  brick the repair when the source's cache refresh failed (no network, no fallback). It
  now falls back to reusing the last good cached copy instead of failing the repair.
- **Deferred at the slim-cut, since rebuilt:** persistent gate toggles were cut
  outright here; the consent-menu stack (see Added above, 2026-07-07) rebuilt
  them in a new shape — per-item human consent, visible skips — rather than the
  original enable/disable verbs.

### Added
- **Layers + the `personal` verb (v2 design §4/§5/§7/§9).** A repo can now hold a *project*
  harness and your *personal* harness at once, stacked highest-layer-wins (whole-file, never
  merged). `init` records the stack in `$OMK/sources.tsv` (bottom-to-top) and each layer's
  full file set under `$OMK/layers/<layer>/`; `placed.tsv` keeps its frozen 5 columns, with
  the winning layer's label in the existing column 3. A personal harness is one global
  setting (`${XDG_CONFIG_HOME:-~/.config}/omakase/personal`), auto-layered on every future
  `init`. A personal `AGENTS.md` is rerouted to Claude Code's additive `CLAUDE.local.md`
  slot (it adds to the project's instructions, never shadows them); a project `AGENTS.md`
  still gains the `CLAUDE.md → AGENTS.md` bridge symlink unless a layer or the repo already
  provides `CLAUDE.md`. New verb `omakase personal [<source> | off]` prints/sets/clears the
  setting and, in an initialized repo, applies or unlayers it immediately. `init
  --no-personal` persistently opts a repo out. Migration is lazy and read-only: the first v2
  verb in a v1 repo synthesizes `sources.tsv` from `$OMK/source` (commit `-`, never guessed);
  a v1 tool that later disagrees with the recorded stack is detected and rehealed on the next
  `init`. Covered black-box end-to-end by `tests/layers.test.sh`. (Superseded below —
  Phase 3.5 replaced the `personal` verb and the global setting with source-stacking
  through `init`/`remove` before any of this reached a release.)

### Changed
- Renamed the inventory script `bin/show.sh` → `bin/status.sh` so it matches the `status`
  verb it has served since the command-surface redesign (the 0.16.0 entry below noted the
  verb still called `bin/show.sh`). Plugin-internal only: `bin/` is never injected into an
  adopter repo, the `status` skill behaves identically, and no payload behavior changes.
- `status` is now implemented by the omakase Go binary, behind the unchanged
  `bin/status.sh` entry point: byte-identical output, with the frozen v1 bash preserved at
  `bin/legacy/status.sh` as the no-Go fallback. New differential parity suite,
  `tests/status-parity.test.sh`.
- `init` and `remove` are now implemented by the omakase Go binary too, behind the
  unchanged `bin/init.sh` / `bin/remove.sh` entry points (thin shims that rebuild and exec
  the binary, falling back to the frozen v1 bash preserved at `bin/legacy/init.sh` /
  `bin/legacy/remove.sh`). Output is byte-identical except per-file list ORDER: Go's
  directory walk is lexical where find(1) was filesystem-order, so the placed-file listing,
  the `placed.tsv` rows, and the `.git/info/exclude` + `.worktreeinclude` entries can appear
  in a different (still complete, still correct) order. New differential parity suite,
  `tests/init-remove-parity.test.sh`.
- **Init now stacks; `personal` is gone (Phase 3.5, v2 design §3/§4/§5/§7).** A second
  `init <source>` on a different source no longer replaces the installed harness — it
  stacks on top instead: both harnesses' files stay live, and the newer `init` wins
  only where both ship the same path (temporal precedence, always narrated on stdout,
  capped at two sources — a third, different source errors and changes nothing).
  `omakase remove <source>` unlayers just that one harness, restoring whatever it had
  shadowed from the other; bare `remove` keeps its v1 total-teardown meaning. The
  `personal` verb, the global `${XDG_CONFIG_HOME:-~/.config}/omakase/personal`
  setting, and `init --no-personal` are deleted: every harness on the stack is one you
  installed explicitly with `init`, nothing layers in automatically. `sources.tsv`'s
  layer column is now an ordinal (`1` bottom, `2` top) instead of a `project`/
  `personal` role name, with no back-compat for the Phase-3-era role labels — that
  surface never reached a release, so zero users are affected. Instruction routing is
  role-free: whichever harness first places a root `AGENTS.md` owns the root slot (and
  the `CLAUDE.md` bridge, if nothing else provides `CLAUDE.md`); a later or
  slot-blocked harness's `AGENTS.md` reroutes to `CLAUDE.local.md` instead, narrated on
  stdout. Covered by the rewritten `tests/layers.test.sh` (142 assertions).

## [0.17.0] — 2026-06-29

### Breaking - gate primitive

One primitive (`omakase-gate.sh`) replaces the three scripts it supersedes
(`omakase-ledger.sh`, `omakase-record.sh`, `deferred-check.sh`). These three files are
removed from the base payload; `omakase init` sweeps orphaned copies from adopter repos.

**Run ledger**: columns drop from 6 to 4 (`epoch name verdict sha`). A pre-v2 ledger with
6 columns is renamed aside on `omakase init`; the new ledger starts fresh.

**Removed environment variables**: `OMAKASE_HOOK`, `OMAKASE_CHECK`, `OMAKASE_GLOB`,
`OMAKASE_BASE`. The waiver mechanism (`--verdict`, `--reason`, `WAIVED` rows) is gone.
The single audited bypass is `OMAKASE_SKIP_<NAME>=1` (name upper-cased, `-`→`_`).

**Migration for adopters**: run `omakase init` once. The orphan sweep removes the three old
scripts, re-injects the wiring, and rotates the old ledger.

**Migration for harness authors**: rewire `lefthook-local.yml` jobs to call
`bash .omakase/bin/omakase-gate.sh <name> --step '<cmd>' [--cacheable] [--glob '<pats>']`;
replace `omakase-record.sh` calls with `omakase-gate.sh <name> --record`.

### Added
- `examples/sample-harness/` — a minimal worked custom harness (one rule, one gate, the wiring)
  to read, try, and copy. It ships only its delta and relies on the base harness machinery layered
  in at install, so it doubles as a live demonstration of the base+source merge. Covered end-to-end
  by `tests/sample-harness.test.sh` (copy into a repo → `init --source` → gate fires → remove).
- A `.claude-plugin/marketplace.json` so the repo is itself an installable marketplace: the
  documented `plugin marketplace add yuncun/omakase-harness` + `plugin install
  omakase@omakase` now resolves (the plugin's `source` is the repo root, `"./"`).
  Without it those install lines had nothing to fetch.
- A **one-skill-per-verb command surface** (`skills/{init,status,remove,share,add-gate}/`), each a
  thin self-locating `run.sh` over the base harness's `bin/`. It works the same on Claude Code
  (typed as `/omakase:init` or model-invoked), Copilot CLI, and a plain shell. Replaces the single
  dispatch-on-argument command/skill; `commands/` is dropped (Claude Code merges commands into
  skills, so one set of skills serves both hosts).
- `omakase share` — the inverse of `init`: capture the current repo's harness into a new,
  publishable harness repo created as a sibling directory (`payload/` + `omakase.manifest` + a
  README carrying the install line), git-initialized and committed ready to push. Prints the
  one-line install others run, `omakase init you/harness`. Wraps `import.sh`. Covered by
  `tests/share.test.sh`.
- `init` accepts an `owner/repo[#ref]` shorthand (e.g. `omakase init alice/harness`) that expands
  to `https://github.com/owner/repo`, optionally pinned to a branch or tag — the shareable install
  line `share` prints. An existing local path of the same shape still wins.
- A `--source` install now layers the **base harness's payload under the custom harness's delta**
  (base machinery underneath, the custom harness winning on overlap), so a custom harness ships
  only its own payload and relies on base machinery — the banner, `omakase-ledger.sh`,
  `omakase-record.sh`, `deferred-check.sh`, the status-line and stop-notice scripts — without
  keeping its own copy. This mirrors the base+delta merge `tools/build.sh` bakes into a plugin
  bundle, performed at install time instead; for a symlink-free custom harness a `--source`
  install and a built bundle place a byte-identical file set (verified against a real harness).
  They diverge only on symlinks: `--source` preserves them, a built bundle dereferences them into
  real files. Covered by `tests/sources.test.sh` (S6).
- `--source` fails closed if the merged hook wiring references a `.omakase/*.sh` script neither
  the custom harness nor the base harness ships — refusing at install (placing nothing) instead of
  dying with a cryptic exit-127 at commit time (the same wiring guard `tools/build.sh` applies to a
  bundle). Covered by `tests/sources.test.sh` (S7).

### Changed
- **Plugin renamed `omakase-harness` → `omakase`** (the plugin identity only): install is now
  `plugin install omakase@omakase`, and the skills read `/omakase:<verb>` on Claude Code. The
  repo name, the `.git/info/exclude` markers, and the harness banner stay `omakase-harness`
  (on-disk contracts).
- User-facing nudges now use host-neutral phrasing — `omakase init` / `omakase status` /
  `omakase remove` (was the slash form `/omakase init`, `/omakase show`); the inspect verb is now
  `status` (it still calls `bin/show.sh`).
- Mascot: the default status icon is now 🥡 (was 🍣); still overridable with `OMAKASE_ICON`.
- Docs terminology: the tool you install once is now the **omakase base harness** (was "the
  engine"), and a personal harness you point `--source` at is a **custom harness** (was
  "a source"). This mirrors the base/custom layering the install actually performs. Wording
  only — the `--source` flag and all behaviour are unchanged.
- The end-of-turn **Stop-hook notice is now opt-in** (was wired on by every install). It does no
  enforcement — it only prints a one-line "harness active / last run" status — and is Claude
  Code-only, so the base payload no longer ships `.claude/settings.json`; `init` prints how to
  enable it, and `omakase status` shows the same detail on demand. Leaner default install.
- The cosmetic commit **banner is no longer auto-wired** into the shipped hook configs; lefthook's
  own run header stands by default. The `omakase-banner.sh` script still ships (terminal `omakase
  status` uses it) and the base `lefthook-local.yml` documents how to re-enable the branded box.

### Fixed
- The base+source merge runs through a temp staging dir cleaned on any exit; its cleanup trap
  returns 0 so a bare (non-`--source`) `init` can never inherit a non-zero exit from it.

## [0.16.0] — 2026-06-22

### Changed
- `/omakase show` no longer lists omakase's own machinery under `.omakase/` in the Injected
  group, and the "Inventory" umbrella heading is dropped — Committed / Injected / Personal are
  now peer sections. Active gates still appear under Guards; `.omakase/` is still disclosed in
  the Hidden-via-exclude section.
- The end-of-turn Stop-hook notice tracks deployment ("<name> is active" / "is not active")
  rather than the last run's result; a failed run keeps the active header and reports the
  failure in words, with no X glyph.

### Fixed
- **Data loss (high):** `remove` no longer deletes the user's own untracked files in a repo
  that never installed omakase — the no-ledger fallback is now gated on a proof-of-install
  sentinel. `init`/`import` no longer write payload content *through* an existing destination
  symlink (clobbering an out-of-tree file); a dangling dest symlink no longer aborts the install.
- The generated fail-closed `verify-overlay` guard no longer fails open on a truncated ledger.
- Deferred gates fail closed (not silently skip) when `OMAKASE_GLOB` is unset or when the diff
  range has no merge base (two-dot fallback). The example gate no longer false-blocks a lone
  `=======` Markdown heading underline.
- `/omakase show`'s Markdown Guards table survives a `|` in a gate command. `build` no longer
  ships `.gitignore`'d junk (`.DS_Store`, `*.bak`) into the dist. Plus BSD/GNU portability and
  ledger exit-code fixes, and broader test coverage.

## [0.15.0] — 2026-06-21

### Added
- Base payload ships the deferred-gate machinery: `omakase-record.sh` (a job records a
  per-commit result) and `deferred-check.sh` (the push gate that blocks unless a fresh
  passing record exists for the commit). Wired as a commented example in `lefthook-local.yml`
  and surfaced in `show`'s GUARDS chart + scorecard; covered end-to-end by
  `tests/deferred-gate.test.sh`. A fork inherits it instead of copying from another harness.

### Changed
- Gate model collapses to two terms: a **gate** (runs in the hook) and a **deferred gate**
  (checks a job ran for the commit). The earlier `live` / `deferred must-pass` /
  `deferred must-run` split and the `producer` term are retired — a deferred gate's
  block-on-failure vs proof-it-ran behavior is now the job's recording policy, not a gate
  type. Reconciled `concepts.md`, `authoring.md`, `README.md`, and the `add-gate` skill,
  which now interviews the user one question at a time to settle the shape.

## [0.14.0] — 2026-06-19

### Added
- `add-gate` skill: an agent-facing walkthrough for wiring a tool, skill, or check to a git
  hook as a gate — picks the gate shape (live / deferred must-pass / deferred must-run),
  pre-flights whether a third-party tool can be gated at all, and shows the wiring (#24).

### Changed
- `show` renders one GUARDS chart with a "run when" column, replacing the separate
  per-hook listings (#23).
- Path classification recognizes Copilot lifecycle hooks (`.github/hooks/`), reusable
  prompt and persona assets (`.github/prompts/`, `.github/chatmodes/`), and Claude agents
  and hooks (`.claude/agents/`, `.claude/hooks/`); an invariant test asserts every known
  harness directory classifies to a concrete kind.

## [0.13.1] — 2026-06-18

### Fixed
- The harness self-heals on a bare `git worktree add`: a new linked worktree re-arms its
  injected files instead of running without them.

## [0.13.0] — 2026-06-17

### Added
- `init` self-provisions lefthook: with no binary on PATH, `LEFTHOOK_BIN`, or
  `node_modules`, it fetches a pinned, checksum-verified binary into a per-machine cache
  instead of exiting (#17).
- Path classification recognizes GitHub Copilot CLI artifacts: `.github/skills/`,
  `.github/instructions/`, `.github/copilot-instructions.md`, and `~/.copilot/` (#18).

## [0.12.0] — 2026-06-12

### Added
- Sources: install a harness from a git source repo with `init --source`, backed by a
  local cache, a manifest, a remembered source, and an orphan sweep on re-install (#16).

## [0.11.0] — 2026-06-12

### Added
- `show` groups the installed inventory by origin: committed, injected, personal.

## [0.10.0] — 2026-06-12

### Added
- Provenance ledger (`placed.tsv`): records the source and content hash of each placed
  file.

## [0.9.0] — 2026-06-11

### Added
- v1 safety fixes and the v1 specification.
