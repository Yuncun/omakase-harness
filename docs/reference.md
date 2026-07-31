# Reference

## Commands

`init.sh`, `status.sh`, and `remove.sh` are thin shims onto the omakase Go
binary. Each resolves, in order: an `OMAKASE_BIN` override (must be executable, or
resolution fails immediately) → a dev rebuild (`go.mod` + `go` on PATH) → a prebuilt
`dist/omakase` → `omakase` on PATH → the stable machine copy at
`~/.cache/omakase/bin/current/omakase` (`XDG_CACHE_HOME` respected — the path every
real `omakase init` self-installs and the `.git/hooks` dispatchers exec). Resolution
is local-only; the shims never download anything (#182). When nothing resolves, every
shim fails closed: one line on stderr — install the binary with
`brew install yuncun/tap/omakase` — and exit 1. There is no bash fallback.

### `init.sh [<owner/repo[/subpath][#ref]> | --source <git-url|path>] [--cut-over] [--help]`

Overlays `payload/` onto the current repo, records placed paths in `.git/info/exclude`,
and installs one dispatcher per hook (no third-party runner). Skips paths the repo tracks. Overwrites a divergent
installed (untracked) file to match payload and warns. Removes a previously placed file
the payload no longer ships, unless it was edited locally.

Hook wiring follows the manifest: a harness that declares **zero gates** gets no
`pre-commit`/`pre-push` dispatchers, and — having nothing to conflict over — installs
even where an incumbent hook manager (husky, pre-commit, an existing hook file) would
otherwise be refused; the incumbent is left untouched. The `post-checkout` heal hook
(worktree auto-install, file repair) is still written when the repo has no incumbent
hooks; with one present it is skipped and init prints the fallback (bare `omakase init`
after a checkout). A re-init after a harness drops its last gate deletes omakase's own
enforcement dispatchers; a later gate added to the manifest brings the full wiring — and
the incumbent refusal — back.

Healing has a second trigger besides `post-checkout`: the plugin ships a `SessionStart`
hook (`hooks/hooks.json`, both hosts) that runs `omakase hook session-start` when an
agent session opens — the same best-effort repair, covering the one gap git events
can't see (overlay wiped, then a new session starts). It is silent unless it restored
something, exits 0 always, and never fetches the binary at session start.

Hooks, the exclude block, and the remembered source live in the git common dir, shared by
every checkout of the repository. Switching the repo's harness is therefore repo-wide: a
source that differs from the one the repo already runs is refused from a **linked
worktree** (the message names the main checkout to run it from). The bare refresh and the
same-source re-run stay allowed in any worktree — that is the normal heal flow.

- `<owner/repo[/subpath][#ref]>` — positional shorthand for
  `--source https://github.com/owner/repo`, optionally pinned to a branch or tag with
  `#ref`. This is the install line for a custom harness a repo publishes:
  `omakase init you/harness`. Segments past `owner/repo` name a harness directory INSIDE
  the repo — `omakase init you/hub/tools` adopts the harness at the hub repo's `tools/` —
  so one hub repo can publish several harnesses without a dedicated repo each. A real
  local path with the same shape wins over the shorthand.
- `--source <git-url|path>` — install ONE harness (a `payload/` tree plus an
  `omakase.manifest`) at a time. A `//subpath` suffix on the url or path adopts a
  harness directory inside the repo (`--source https://host/x/hub//tools`,
  `--source /clones/hub//tools`); the manifest and `payload/` must live under that
  directory, the validation runs there (never at the repo root), and the subpath is
  remembered so a bare `init` refreshes the hub and re-injects the same subfolder.
  The root — the part before `//` — is what gets cloned, so it must be a git repo,
  as with every source. No harness installed yet: the omakase base harness's
  payload is layered UNDER the custom harness's payload (base machinery underneath, the
  custom harness's delta winning on overlap), so a custom harness ships only its delta
  and relies on base machinery without keeping its own copy. This source names the
  SAME harness already installed: repairs it — re-fetches the source's ref and
  re-records whatever commit currently resolves; if the fetch fails (offline) it falls
  back to the last good cached copy instead of failing the repair. This source names a
  DIFFERENT harness than the one installed: **replaces** it — every file the old source
  placed and the new one does not ship is swept, then the new source is installed fresh.
  There is no stacking; a repo holds exactly one installed harness at a time. Refuses
  (placing nothing) if a gate's `run:` names a payload script (`.omakase/…` or `gates/…`)
  neither the harness nor the base ships. The harness is remembered; a later bare `init`
  refreshes and reinstalls it.
- `--cut-over` — also untrack (`git rm --cached`) every payload path the repo currently
  tracks, so the installed copy takes over. Guarded: refuses without
  `OMAKASE_CUTOVER_CONFIRM=1`.

### `status.sh [--markdown | --plain | --global | --disable <name> | --enable <name> | --keep <path> | --restore <path>]`

`status` prints the static page: the inventory grouped by origin (committed,
injected, global), the hook
wiring, the run ledger, and the paths hidden via `.git/info/exclude`. The global
group prints as one count line — the personal config under `~/.claude` +
`~/.copilot` steers every repo identically, so the page states the fact and
keeps the enumeration behind `--global`.

- `--plain` — accepted for script compatibility; same as no flags. Read-only.
- `--markdown` — the static page as formatted Markdown. Read-only.
- `--global` — list the personal config the page's GLOBAL line counts. Read-only;
  reads only `$HOME`, so it prints the same in every repo.
- `--disable <name>` / `--enable <name>` — per-item consent toggles. `<name>`
  is a wired gate name, a placed path, or a placed top-level
  directory (a group). Disabling a FILE removes it from the working tree (the
  snapshot keeps a copy; `--enable` restores it; a locally edited file refuses
  the toggle rather than lose the edits). Disabling a GATE records it in the
  git dir's `omakase/disabled-gates`; the hook still announces the skip on
  every run — a bypassed gate is never silent — until `--enable` clears it.
  Machinery (`.omakase/`, the `omakase.manifest`) refuses to toggle. A name that
  matches nothing errors (exit 2).
- `--keep <path>` / `--restore <path>` — the edit lifecycle (#98). You edited
  a placed file (or directory of them); the status surfaces show it as
  changed. `--keep` accepts the on-disk version as yours: the accepted copy
  is stored under the git dir's `omakase/kept/`, the ledger hash moves to
  it, and everything reads green again — green means "matches what you've
  consented to". `--restore` puts the harness's version back — it also clears
  plain, un-kept drift, and on a disabled row it restores AND re-enables (the
  harness's version, full stop), so a kept-then-disabled file is never a dead
  end. `--enable` prefers the kept copy when one is saved, so a disable/enable
  cycle round-trips the version you accepted. See the change first with
  `omakase diff`. Names resolve like `--disable`; machinery and git-tracked
  paths refuse (exit 2).
- `--help` — usage.

Consent survives re-init: a file toggled off stays off across `init` (its
ledger row and snapshot refresh, so a later `--enable` restores the CURRENT
payload copy — or your accepted, kept copy when one is saved), a disabled
gate stays recorded, and a kept file is left
untouched — by repair `init`, by `init <new-source>` (even when the new
source no longer ships the path; `--restore` still works offline), by the
checkout self-heal (which refills a missing kept file with the ACCEPTED
copy), and by `remove` (a kept file is yours; it stays on disk, reported).

### `omakase context [--markdown] [--show <path>]`

Binary-only verb, strictly read-only: the harness seen as the layers of
instruction text an agent host assembles before your first word, rather than
as a list of files. Where `status` answers *what is installed and who owns
it*, `context` answers *what actually reaches the model, and what it costs*.

Every layer is placed in one of five reach tiers:

| Tier | Meaning |
| --- | --- |
| `LOADED` | full text in the context window on every turn |
| `INDEXED` | name and description load; the body does not |
| `ON DEMAND` | loads when you touch the directory it governs |
| `ON TRIGGER` | loads when what you ask matches its description |
| `INERT` | present on disk, unread by the host you are running |

The `INDEXED` tier is the distinction the file-based page cannot make: a
skill's frontmatter `description:` is loaded every turn so the agent can
decide whether to open it, while its body stays on disk until a trigger
matches. A repo can therefore carry tens of thousands of tokens of skills for
a rent of one or two thousand — and a needlessly long description is a
permanent tax that a long body is not.

Layer order follows each host's *published* instruction precedence (Copilot
CLI prints its list in `copilot --help`); it is not inferred. Host detection
reads `COPILOT_CLI`/`COPILOT_AGENT_SESSION_ID` and `CLAUDECODE` from the
environment. When the host cannot be determined **nothing is marked inert** —
asserting that a file is unread without being able to observe the host would
be worse than staying quiet.

Rows are aggregated by unit, not by file: one row per skill (not one per
script, test, and reference), one row for all nested `*/CLAUDE.md`. Two names
for one file — commonly `~/.claude/CLAUDE.md` symlinked to
`~/.copilot/copilot-instructions.md` — are grouped by resolved path and
counted once, since double-counting would both inflate the total and report
loaded bytes as inert. A file whose only content is an `@`-include is
reported as the pointer it is rather than quoted as if it were guidance.

Token costs are estimated at four bytes per token and labelled as estimates;
only the host knows real numbers (`/context` in both Copilot CLI and Claude
Code). The verb deliberately does not duplicate that — it supplies the column
the host cannot have: where a layer came from, whether it survives a clone,
and who can change it.

`--show <path>` prints one layer in full, accepting a repo-relative path, a
`~/` personal path, or any unique substring of one. A substring matching
several layers lists them and exits 2 rather than guessing. Exit 0 on the
page; unknown flags and unresolvable `--show` targets exit 2; outside a git
repo exits 1.

### `omakase diff [path…]`

Binary-only verb (no `.sh` shim), strictly read-only: shows what you changed
in the placed files, in the forward direction (your edit renders as an
addition), against the harness version — or against your accepted version
once a file is kept. No paths = every changed enabled placed file; a path is
a placed file or a directory of them (resolution as above). Exit 0 whether or
not differences exist; unknown paths and any flag other than `--help` exit 2.

### `omakase statusline [--wire]`

Binary-only verb: prints the one-line status-bar segment for the repo the host
session is sitting in — `🥡 <repo>[:<worktree>] ⎇<branch> · <harness> ✓`, with
the amber problem fact or a dim "unverified" when the proofs don't all pass,
and a live `· <gate> 12s…` suffix while a gate is running (a heartbeat the
gate runner writes; it shows only while the gate's process is alive, so a
killed gate can never stick). The segment tracks the session's live working
directory by design — it appears in omakase repos and goes dark everywhere
else. Dark is also the honest degraded state: the segment reads cached facts
only, so a wiped cache stays dark until the next `init`; `omakase status` is
the truth surface.

The segment is wired into the hosts' bars by default: after a real install,
`omakase init` fills each host's empty `statusLine` slot, per host and only
where its config dir already exists (Claude Code `~/.claude`, Copilot CLI
`~/.copilot`), backing the settings file up first
(`settings.json.omakase-bak`). An existing bar is never replaced — a host
that already has one is left untouched, silently. `init --help` and the
nothing-remembered first run install nothing and wire nothing.

`--wire` does the same connection explicitly — after an unwire, or on a
machine that has never run an init here — and it teaches the occupied-slot
case: instructions for adding the segment to an existing bar by hand. Note
Copilot refreshes its bar per response (no timer), so the live gate counter
updates more coarsely there than on Claude Code.

### `remove.sh`

Uninstalls hooks, deletes exactly the untracked files `init` placed, and strips the
omakase block from `.git/info/exclude`. Tracked files are never touched. Takes no
arguments — any argument is ignored. There is no per-source removal; a repo holds one
installed harness, and `remove` always tears it down completely.

## Environment

| Variable | Effect |
|---|---|
| `OMAKASE_SKIP_<NAME>=1` | skip one gate for one git command — name upper-cased, `.`/`-`→`_`. Audited and printed on every hook run; a bypassed gate is never silent |
| `OMAKASE_SKIP_GATES=1` | skip every gate for one git command — the explicit skip-all escape. Audited and printed. The overlay integrity check still runs; bypass it with git's own `--no-verify` |
| `OMAKASE_CUTOVER_CONFIRM=1` | required to apply `init.sh --cut-over` |
| `OMAKASE_PAYLOAD` | path to a payload tree to install, overriding the plugin payload. Lower precedence than `--source` |
| `OMAKASE_BASE_PAYLOAD` | dev/test override: path to a base payload tree to merge under a `--source` install. A location hint only — unlike `OMAKASE_PAYLOAD` it never suppresses a remembered source. Normally unset: the binary resolves a `payload/` sibling (dev loop) or extracts its own embedded copy into the machine cache |
| `OMAKASE_BIN` | path to an omakase binary to use instead of dev rebuild, `dist/omakase`, PATH, or the stable machine copy — must be executable, or resolution fails immediately |
| `OMAKASE_NOW` | test hook: pins the ledger epoch (the timestamp on each recorded gate row) to a fixed value for reproducible runs |
| `XDG_CACHE_HOME` | cache root for the stable machine binary copy and source clones (default `~/.cache`) |

## Manifest

A harness carries **one** `omakase.manifest` — flat, hand-parsed text, no YAML — at
`payload/omakase.manifest`. It carries both the harness's identity and its gate wiring:

- **Identity** — header keys (`name`, `version`, `recommends`) name the harness; `name` is
  required, read when a `--source` install fetches the source.
- **Gate wiring** — `gate:` blocks declare the harness's gates.

`init` places this file with the rest of `payload/` (it lands at the target root as
`omakase.manifest`) and snapshots it into the target's git dir; each git hook reads its gates
from that snapshot. Editing the placed copy changes nothing until a bare `init` re-consents to
it. A leftover source-root `omakase.manifest` (the pre-consolidation two-file layout) is refused
fail-closed: `init` points you to move its keys into `payload/omakase.manifest` and delete the
root file.

Header keys, one `key: value` line each:

| Key | Required | Meaning |
|---|---|---|
| `name` | for `--source` | harness name, shown on install |
| `version` | no | harness version |
| `recommends` | no | free-text companion-tool hint, printed once at install |

### Gate blocks

Gate blocks live in `payload/omakase.manifest`. A `gate: <name>` line at column 0 opens a
block; indented `key: value` lines belong to it until the next column-0 line. The omakase
binary runs each gate at its hook (see [Concepts](concepts.md#gates)).

    gate: go-test
      hook: pre-push
      run: go test ./...
      glob: *.go go.mod go.sum
      cacheable: true

| Key | Required | Meaning |
|---|---|---|
| `gate:` | yes | the gate's name: `[A-Za-z0-9._-]+`, unique in the manifest. The scorecard name and the `OMAKASE_SKIP_<NAME>` name (upper-cased, `.`/`-`→`_`) |
| `hook:` | yes | `pre-commit` or `pre-push` — the only stages omakase wires |
| `run:` | yes | a command line, run via `sh` from the repo root; exit 0 passes, non-zero blocks |
| `glob:` | no | space-separated case-glob patterns (a single `*` spans directories); the gate runs only when a changed file in the range matches. Absent = always in scope |
| `cacheable:` | no | `true` reuses a recorded PASS for the exact HEAD sha until HEAD moves |
| `purpose:` | no | what the gate enforces, in the author's words (≤6 words, concrete — "tests green before push", not a clever label). Shown as the ENFORCES column of the status guards table; when any gate declares one, the scheduling mechanics move to their own RUNS column |

At init, an unknown key, a missing required key, a duplicate name, or a bad hook stage
refuses the whole harness (places nothing). If a `run:`'s first token is a payload path
(`.omakase/…` or `gates/…`), that file must exist in the payload and be executable — the
"nothing runs undeclared" check; any other first token (`go`, `make`, `bash …`) is the
author's own command, resolved from `PATH`.

## Instruction files

omakase places an instruction file exactly as the harness ships it — VERBATIM, at the
same path. There is no reroute, no synthesized bridge symlink, and no root-slot
fallback logic: `init` treats `AGENTS.md`, `CLAUDE.md`, and
`.github/copilot-instructions.md` like any other payload file. A harness that wants
Claude Code to read the same instructions as `CLAUDE.md` ships its own `CLAUDE.md` (or
its own `CLAUDE.md → AGENTS.md` symlink) under `payload/`; omakase never creates one on
a harness's behalf. Each host then reads whatever it natively recognizes at that path —
`AGENTS.md`/`CLAUDE.md` at the repo root for Claude Code, `.github/copilot-instructions.md`
for Copilot CLI. The usual placement rules apply and nothing else: a path the repo
already commits is skipped and reported, an installed instruction file is excluded via
`.git/info/exclude`, and `remove` deletes it like any other placed file.

## Path classification

`init` decides how to exclude a placed file by its top directory. A shared top directory
(`HARNESS_SHARED_TOPDIRS` in `bin/lib-harness-paths.sh`, currently `.github`) is excluded
file-by-file, so the project's own files there stay visible to git. Every other top
directory is excluded wholesale. See
[Concepts](concepts.md#owned-and-shared-directories).
