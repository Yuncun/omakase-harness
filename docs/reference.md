# Reference

## Commands

All verbs live on the one `omakase` Go binary (`brew install yuncun/tap/omakase`).
The agent-facing skills call it on PATH; the `.git/hooks` dispatchers exec the
stable machine copy at `~/.cache/omakase/bin/current/omakase` (`XDG_CACHE_HOME`
respected — the path every real `omakase init` self-installs). Nothing ever
downloads a binary at run time (#182).

### `omakase init [<owner/repo[/subpath][#ref]> | --source <git-url|path>] [--cut-over] [--help]`

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

Healing has a second trigger besides `post-checkout`: init wires a `SessionStart`
hook into Claude Code's user-level settings (`~/.claude/settings.json`) that runs
`omakase hook session-start` when an agent session opens — the same best-effort
repair, covering the one gap git events can't see (overlay wiped, then a new
session starts). It is silent unless it restored something, exits 0 always, and
the wired command derives the stable binary path from the environment at session
time and self-guards on it existing — a machine that loses the install never
sees session-start errors. On Copilot CLI the heal rides git events only
(Copilot keeps hooks in its own `~/.copilot/hooks/*.json` mechanism, which
omakase does not write yet).

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

### `omakase status [--markdown | --plain | --global | --all | --show <path> | --disable <name> | --enable <name> | --keep <path> | --restore <path>]`

`status` prints the minimalist page: the steering stack (one band per layer
of steering — yours, the overlaid harness, the project's own — solid cells
for bytes read every turn, hollow cells for bytes that wait until needed),
the guards chart (each gate's hook, name, and last verdict, with a dim
trailing detail only for a declared purpose or non-default scheduling), and
the LOADED EVERY TURN table — one line per always-loaded layer, its
estimated token cost drawn as a bar, and the file's first words. Files the
detected host never reads collapse to one closing line. Injected files
appear individually only when something is wrong (missing, drifted, or
toggled off — a NEEDS ATTENTION group); a healthy file earns no line. The
identity line (source, base version, install path), the footprint sentence,
and the full per-file inventory live behind `--all`.

- `--plain` — accepted for script compatibility; same as no flags. Read-only.
- `--markdown` — the same page as formatted Markdown. Read-only.
- `--all` — the audit page: identity, footprint, guards, and the full file
  inventory — every placed row (healthy or not) grouped by origin
  (committed, injected, unmanaged, global). Read-only.
- `--show <path>` — print one layer in full; `<path>` is a repo-relative path,
  a `~/` personal path, or any unique fragment of one. Read-only.
- `--global` — list the personal config that applies to every repo. Read-only;
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

### `omakase remove`

Uninstalls hooks, deletes exactly the untracked files `init` placed, and strips the
omakase block from `.git/info/exclude`. Tracked files are never touched. Takes no
arguments — any argument is ignored. There is no per-source removal; a repo holds one
installed harness, and `remove` always tears it down completely.

`remove` is repo-scoped. The machine-level pieces init set up are shared by every
repo and deliberately survive it: the stable binary copy under
`~/.cache/omakase/`, the user-level skills (`~/.claude/skills/omakase-*/`,
`~/.copilot/skills/omakase-*/` — each carries a `.omakase` marker file, safe to
delete by hand), and the statusLine / SessionStart entries in the hosts'
settings.json (delete the blocks by hand; a `.omakase-bak` sits next to the file
from the last wiring write). Removing the binary itself is your package
manager's job (`brew uninstall omakase`).

## Environment

| Variable | Effect |
|---|---|
| `OMAKASE_SKIP_<NAME>=1` | skip one gate for one git command — name upper-cased, `.`/`-`→`_`. Audited and printed on every hook run; a bypassed gate is never silent |
| `OMAKASE_SKIP_GATES=1` | skip every gate for one git command — the explicit skip-all escape. Audited and printed. The overlay integrity check still runs; bypass it with git's own `--no-verify` |
| `OMAKASE_CUTOVER_CONFIRM=1` | required to apply `omakase init --cut-over` |
| `OMAKASE_PAYLOAD` | path to a payload tree to install, overriding the remembered source and the embedded base payload. Lower precedence than `--source` |
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
(`harness.SharedTopdirs` in the Go source, currently `.github`) is excluded
file-by-file, so the project's own files there stay visible to git. Every other top
directory is excluded wholesale. See
[Concepts](concepts.md#owned-and-shared-directories).
