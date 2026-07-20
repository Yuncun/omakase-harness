# Reference

## Commands

`init.sh`, `status.sh`, `remove.sh`, and `mcp.sh` are thin shims onto the omakase Go
binary. Each resolves, in order: an `OMAKASE_BIN` override (must be executable, or
resolution fails immediately) → a dev rebuild (`go.mod` + `go` on PATH) → a prebuilt
`dist/omakase` → `omakase` on PATH → the pinned release binary, downloaded once per
machine, sha256-verified against digests baked into the repo, and cached at
`~/.cache/omakase/bin/<version>/` (`XDG_CACHE_HOME` respected). `init.sh`, `status.sh`,
and `mcp.sh` may trigger that download on first run; `remove.sh` never fetches but
reuses an already-cached binary, keeping uninstall offline. When nothing resolves, every
shim fails closed: recovery guidance on stderr (install a binary, or point
`OMAKASE_BIN=/path/to/omakase` at one) and exit 1 — there is no bash fallback.

### `init.sh [<owner/repo[/subpath][#ref]> | --source <git-url|path>] [--cut-over] [--help]`

Overlays `payload/` onto the current repo, records placed paths in `.git/info/exclude`,
and installs one dispatcher per hook (no third-party runner). Skips paths the repo tracks. Overwrites a divergent
installed (untracked) file to match payload and warns. Removes a previously placed file
the payload no longer ships, unless it was edited locally.

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

On a real terminal, `status` opens the interactive consent screen: every steering
file and gate as a row (arrows to move, Enter/Space to toggle, q to quit).
Everywhere else — pipes, scripts, agents — it prints the same static page as
always: the inventory grouped by origin (committed, injected, global), the hook
wiring, the run ledger, and the paths hidden via `.git/info/exclude`. The global
group prints as one count line — the personal config under `~/.claude` +
`~/.copilot` steers every repo identically, so the page states the fact and
keeps the enumeration behind `--global`.

- `--plain` — force the static page on a terminal too. Read-only.
- `--markdown` — the static page as formatted Markdown. Read-only.
- `--global` — list the personal config the page's GLOBAL line counts. Read-only;
  reads only `$HOME`, so it prints the same in every repo.
- `--disable <name>` / `--enable <name>` — the scriptable twins of the screen's
  toggle. `<name>` is a wired gate name, a placed path, or a placed top-level
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

### `omakase mcp`

Binary-only verb (no `.sh` shim): a stdio MCP server that serves the same
consent surface inside agent hosts. Tools: `status` (the read-only page) and
`menu` (one nested form: a header row per dev-loop stage — set to keep as-is,
all on, or all off, which applies to every row under it left unchanged —
with a row per file and gate beneath; Space toggles a row). `expand=true`
gives every file its own row instead of one row per directory. Rendered
natively by hosts that support MCP elicitation — Claude Code and Copilot CLI
both do; plain text elsewhere. Nothing applies until the human submits the
form. Register it from the target repo, e.g.:
`claude mcp add omakase -- /path/to/omakase mcp`. In a plugin install where no
binary is on PATH, register the shim's stable path instead:
`claude mcp add omakase -- /path/to/omakase-harness/bin/mcp.sh`.

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
| `OMAKASE_BASE_PAYLOAD` | path to the base (plugin) payload tree, exported automatically by the bin/ shims. Needed when the binary resolves from the per-machine cache or PATH, away from a `payload/` sibling. A location hint only — unlike `OMAKASE_PAYLOAD` it never suppresses a remembered source |
| `OMAKASE_RELEASE_BASE_URL` | mirror for the omakase binary download, overriding the GitHub releases base URL |
| `OMAKASE_BIN` | path to an omakase binary to use instead of dev rebuild, `dist/omakase`, PATH, or the fetched cache — must be executable, or resolution fails immediately |
| `OMAKASE_NOW` | test hook: pins the ledger epoch (the timestamp on each recorded gate row) to a fixed value for reproducible runs |
| `XDG_CACHE_HOME` | cache root for the fetched omakase binary (default `~/.cache`) |

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
