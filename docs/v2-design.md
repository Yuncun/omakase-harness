# omakase v2 — design

> **SUPERSEDED 2026-07-03.** A YAGNI audit cut this design's stacking (two harnesses
> at once), instruction-reroute (`CLAUDE.local.md`), migration, pins, and gate-toggle
> (`enable`/`disable`) surface before any of it reached a release. **The shipped
> product is a SINGLE-harness overlay — verbs `init` / `remove` / `status`, joined
> 2026-07 by `mcp` (the consent menu; see the note below)** —
> `init` installs or repairs one harness; a different source REPLACES it (v1
> orphan-sweep), it does not stack; `remove` is a bare, argument-free total teardown.
> See [`docs/reference.md`](reference.md) for the current contract and
> [`CHANGELOG.md`](../CHANGELOG.md) for what was removed and why. Everything below
> this note — most of it now historical — describes the CUT design as it stood before
> the revert; do not read any of it as shipped behavior. (One piece has since
> shipped in a NEW shape this doc does not describe: the 2026-07 consent-menu stack
> rebuilt gate/file toggles as `omakase status`'s interactive screen plus
> `omakase mcp` — [`docs/reference.md`](reference.md) is the contract for those.)
> §1-§3 and §5 carry inline
> corrections; §4 and §6-§13 are kept verbatim as a design record of what was built,
> then reverted, and why.

Status: LOCKED 2026-07-02 (superseded 2026-07-03, see above). This document was the
contract for the v2 implementation; when it and the code disagreed during the build,
this document won until amended. §8 is provisional — it is deliberately the cheapest
decision in the design to reverse (one mapping-table row, no state impact). Amended
2026-07-03 (Phase 3.5): §1, §3, §4, §5, §7, §8, §12, §13 rewritten for the init-stack
surface that replaced the original `personal` verb/global-setting design — see the
CHANGELOG for why. That init-stack surface was itself reverted later the same day; see
the supersession note above.

## 1. What v2 is [CUT — see supersession note above]

This section described two changes that were built, then reverted; neither shipped.
**A second `init` replaces the first source wholesale — same as v1 — today**, exactly
as it always did:

1. **Layers (CUT, never shipped).** The design called for a repo to hold two harnesses
   at once: run `init` again with a DIFFERENT source and it would STACK on top of the
   first instead of replacing it — the newer one winning where both ship the same
   path, capped at two, narrated on stdout. `omakase remove <source>` would drop back
   to one. This was built (Phase 3.5) and then reverted by the 2026-07-03 slim-cut:
   there is no stacking, no roles, and no `remove <source>` in the shipped binary — a
   different source replaces the installed harness (sweeping its orphaned files), and
   `remove` is bare and total.
2. **Toggles + pins (CUT, never shipped).** The design called for gates to be
   persistently disabled per-repo (`omakase disable <gate>`) and for every source to
   record the commit it was installed at (`update` as the sole verb that moves pins).
   None of this was built; there is no `enable`/`disable`/`update` verb.

The install-time machinery (today `bin/*.sh`, ~1,400 lines of bash) becomes **one Go
static binary** with subcommands. *[Superseded in part by issue #98, 2026-07: the
hook-time trio (`ensure-present.sh`, `verify-overlay.sh`, `install-guards.sh`) and the
lefthook hook stubs are gone. `.git/hooks` now holds permanent ~5-line sh dispatchers,
written only by `omakase init`, that exec the machine-wide binary with
`omakase hook <name>`; the heal and the fail-closed verify run natively in the binary,
and lefthook is a gate runner invoked with explicit config, never a hook installer.
What stays sh at hook time: the dispatchers themselves and `omakase-gate.sh` + gates in
the payload (bash, 3.2 floor). The Phase 0 compat suite
(`tests/state-readers.test.sh`, `tests/omakase-gate.test.sh`) still pins the
reading/healing contract, now against the binary.]*

`share` and `import` are **removed** (owner decision 2026-07-02): they are harness-author
tools in an adopter's command list. Authors write a repo with `payload/` +
`omakase.manifest` by hand, per docs/authoring.md.

## 2. The pitch (README first paragraph) [CUT — see supersession note above]

The design called for this pitch, which sells the stacking behavior that was later
reverted; it never shipped and is not the current README wording:

> ~~omakase installs a project's quality gates, git hooks, and agent instructions into
> any repo as an invisible overlay — run `init` again with a different source to stack
> your own harness on top — with zero committed footprint: nothing ever reaches a PR,
> and `omakase remove` puts everything back exactly.~~

The shipped pitch (see `README.md`) is single-harness: `init` installs one harness as
an invisible overlay with zero committed footprint; a different source replaces it
rather than stacking; `omakase remove` puts everything back exactly.

## 3. Verb surface [CUT — see supersession note above]

The 6-verb table below was the design's target and was never fully built; only
`init` / `status` / `remove` shipped, and `init`/`remove` shipped in their v1,
single-harness shape (no stacking, no `remove <source>`). Kept as a historical record
of the wider design that was cut:

| Verb | Args (design intent, NOT shipped as described) | One meaning |
|---|---|---|
| `init` | `[owner/repo[#ref] \| --source <url\|path>] [--cut-over]` | Install, stack, or repair. No source recorded yet: installs it (v1 parity). Same source recorded again: repairs that harness. A DIFFERENT source, one already recorded: **stacks** it on top — the new harness's files win where both ship the same path, narrated (`stacked <B> on top of <A>` + one `^ overrides <A>: <path>` line per shadowed path). Two sources already recorded and a third, different one given: refused (exit 1, nothing touched) — "remove one first". Bare `init` (no source): re-places every recorded harness. **Pins are RECORDED now** (`sources.tsv` captures each layer's resolved commit on every install/repair) **but not yet ENFORCED**: today both a same-source repair and a bare `init` re-fetch each layer's ref and re-record whatever commit currently resolves — the same "refresh to latest" v1 always did. Repair-at-recorded-pin (offline, no fetch) and `update` becoming the sole pin-mover are Phase 4 work (§13). `--cut-over` unchanged from v1 (guarded by `OMAKASE_CUTOVER_CONFIRM=1`). |
| `update` | `[<source>] [--check]` | **Not built; cut.** Design intent: the ONLY pin-mover. Fetch the named source's latest ref (default: every recorded source), resolve to a new commit, record it, re-overlay. Prints `old → new` per source. `--check` = read-only dry run ("9f3c2ab → 4d21e77, 12 commits behind"). |
| `status` | `[--markdown]` | Read-only, question-first (identity / footprint / guards / inventory). Design intent described a live stack-order identity line and a shadow-with-consequence flag for a second, stacked harness — moot, since stacking was cut. |
| `enable` | `<gate>` | **Not built; cut.** Would have removed the gate's row from `$OMK/disabled-gates` (atomic rewrite). |
| `disable` | `<gate> [--reason <text>]` | **Not built; cut.** Would have appended `name<TAB>epoch<TAB>reason` to `$OMK/disabled-gates`, persistent per-clone. |
| `remove` | `[<source>]` | Design intent: bare = total teardown (v1 semantics); `<source>` = remove just that one harness. **What shipped: `remove` takes no `<source>` argument at all — argv is ignored — and is always the bare, total teardown.** There is no per-source removal. |

**What actually shipped (3 verbs, single harness — see `docs/reference.md`):** `init
[<owner/repo[#ref]> | --source <url|path>] [--cut-over]` installs or repairs ONE
harness; a different source REPLACES the installed one (v1 orphan-sweep), it does not
stack. `remove` (no arguments) is a bare total teardown. `status [--markdown]` is
read-only. `update`, `enable`, `disable` do not exist as verbs. *[2026-07
correction: the consent-menu stack later rebuilt toggling as `status
--disable/--enable` and the interactive/MCP menu — see `docs/reference.md`;
`update` still does not exist. Issue #98 Part 2 (2026-07) added the fourth
human verb, `omakase diff` (read-only), and the edit-lifecycle plumbing
`status --keep/--restore` backed by `$OMK/kept/` accepted copies — editing a
placed file is the expected lifecycle now, not drift to be repaired.]*

The two "deliberate v1 behavior change" paragraphs that followed this table in the
locked design (a pin-based bare `init`, and a stacking second `init`) describe changes
that were never shipped or were shipped then reverted; `init` behaves exactly as v1
did — bare `init` re-fetches and re-records the latest commit (no offline pin-repair),
and a second `init` with a different source replaces rather than stacks.

## 4. Layering model [CUT — never shipped in this form; see supersession note above]

A TEMPORAL stack, capped at two — this is the entire mental model:

    base machinery   (bottom — ships embedded in the omakase binary; folds into layer 1)
    layer 1          (the first `init <source>` this repo ran)
    layer 2          (a second, DIFFERENT `init <source>` — optional, stacked on top)

Precedence, one line: **committed file > latest `init` > earlier `init` > base
machinery.** There are no roles — no "project" layer, no "personal" layer — only
*installed first* and *installed second*. Which harness plays which part is whatever
was typed, in order; nothing is ever layered in automatically.

On the FIRST `init <source>` in a repo, the base payload folds INTO that source's
layer (base+delta = layer 1's store; there is no separate base store), so
`$OMK/layers/` holds layer 1 (+layer 2) — the diagram above is the MENTAL model, not
always three physical stores.

- Overlap = **whole-file replacement, higher layer wins**. Never content merging —
  not for instructions, not for `lefthook-local.yml`.
- One exception, owned by the instruction mapping rule (§7): whichever layer FIRST
  places a root `AGENTS.md` owns the root instruction slot for as long as it stays
  installed. A layer that ships `AGENTS.md` after the slot is taken (or under a
  committed root `AGENTS.md`/`CLAUDE.md`) is **rerouted** to `CLAUDE.local.md`, the
  host's additive gitignored slot, instead of shadowing the slot-owner's
  instructions — narrated on stdout.
- Capped at 2. A third, different source errors ("remove one first") and mutates
  nothing.
- Arbitrary N-layer stacks are out of scope. Two team harnesses compose in a harness
  repo, not in omakase.
- Removing one layer (`omakase remove <source>`): for each path it won, re-place the
  copy the OTHER layer ships (from `$OMK/layers/<n>/files/`), rewriting the
  placed.tsv row (source label + sha256); delete the path if the other layer doesn't
  ship it (untracked + hash-match rule; local edits warned and kept). Deterministic,
  offline (GC10 — no network). Removing the BOTTOM layer additionally: re-folds the
  embedded base payload under the survivor's delta (the same fold a fresh
  `init <survivor>` would build), repoints `$OMK/source` at the survivor (so a later
  bare `init` can't resurrect the removed harness), and un-reroutes the survivor's
  instructions back to the root slot if nothing else still claims it.
- Layer rebuild ordering (worktree race): only `$OMK/layers/<n>/` is rebuilt tmp +
  rename; `payload-snapshot/` is RemoveAll'd and rebuilt in place (no tmp dir). The
  safety property lives in the READER instead: the heal skips a placed row
  whose snapshot copy is missing, so a hook racing a mid-rebuild snapshot can only
  skip a heal, never heal from wrong bytes. The rebuilt-before-deletions ordering
  itself holds for `remove <source>` (snapshot rebuilt before any working-tree
  deletion, on both the top-removal and the bottom-removal path).
- The fail-closed wiring guard runs against the MERGED tree.

## 5. State under `$GIT_COMMON_DIR/omakase` (`$OMK`)

**FROZEN — never change (sh hook-time readers depend on the bytes):**

| Artifact | Frozen contract |
|---|---|
| `placed.tsv` | Exactly 5 columns `path<TAB>kind<TAB>source<TAB>sha256<TAB>enabled`, one row per placed file. **No 6th column, ever**: `state.ReadPlaced` splits into at most 5 fields (a 6th tab is absorbed into `enabled` and would flip verification fail-open), matching the retired sh readers' `read -r rel kind src hash enabled`. A writer-side format test must enforce ≤5 columns. Col 3 is `payload` for a base-only install (the v1 label, pinned by placed.test.sh), otherwise the one installed source's label (`source` or `source#ref`) — there is only ever one source, never a layer stack, so this is not a "winning layer" label. |
| exclude block | `# >>> omakase-harness >>>` / `# <<< omakase-harness <<<` markers, verbatim. |
| sha256 semantics | A symlink hashes its readlink TARGET STRING; digest tool = shasum (macOS) / sha256sum (elsewhere), identical output required. |
| `payload-snapshot/` | The effective placed tree the post-checkout heal restores from. |
| `ledger.tsv` | `epoch<TAB>name<TAB>verdict<TAB>sha` gate-run records. |
| `$OMK/source` | One line = the one recorded source (v1 semantics, unchanged). There is no "bottom layer" — a repo holds exactly one installed harness — so there is no survivor to rewrite this to; a different `init <source>` simply overwrites this file with the new source, same as v1. |

**NEW additions from the cut design — none of this shipped as designed (CUT, see
supersession note above). `sources.tsv` and `$OMK/layers/` do not exist in the
codebase; there is no layer store.** *[2026-07 correction: a `disabled-gates` file
DOES now exist — the consent-menu stack rebuilt it in a simpler shape (one gate
name per line, no epoch/reason columns) — see `docs/reference.md`.]* Kept for the
historical record of what the locked design called for:

| File | Format | Purpose |
|---|---|---|
| `sources.tsv` | `layer<TAB>source<TAB>ref<TAB>commit<TAB>installed_epoch`, bottom-to-top. `layer` is an OPAQUE ORDINAL string — `"1"` for the bottom row, `"2"` for the top row — assigned by the caller from each row's position in the stack, never a role name (Phase 3.5 deleted the `project`/`personal` labels and the `personal<TAB>off<TAB>-<TAB>-<TAB><epoch>` sentinel row entirely; no back-compat, that state never reached a user — see §9). `ref` = requested `#ref` or `-`; `commit` = full resolved sha at install/update time (`-` for a non-git local path — never guessed). | The layer stack + lockfile groundwork (diff/verify UX deferred; only the format ships now). |
| `disabled-gates` | `name<TAB>epoch<TAB>reason` (reason optional, tabs/newlines stripped), atomic tmp+mv writes. | Persistent gate toggles; lets status render "OFF · 6 weeks · flaky on CI". |
| `layers/<n>/files/` + `layers/<n>/placed.tsv` | Each layer's FULL post-mapping file set (incl. currently-shadowed paths), same 5-col layout. | Shadow-restore source for `remove <source>` / bottom-layer re-fold. |
| `layers/<n>/rerouted` | One line per rerouted path, `<dest><TAB><original>` (in practice at most one: `CLAUDE.local.md<TAB>AGENTS.md`). Lives OUTSIDE `layers/<n>/files/` — never leaks into `placed.tsv`, the exclude block, or the snapshot. | Sidecar marker recording that this layer's canonical `AGENTS.md` fell back to `CLAUDE.local.md` (§7). Read only by a BOTTOM-layer `remove <source>`, to un-reroute the survivor's instructions back to the root slot (suppressed if a committed root instruction file still blocks it). Preserved when a store is reused untouched; recomputed from scratch on every store rebuild. |

## 6. Gate toggles — mechanics [CUT — not built; see supersession note above]

The gate primitive (`payload/.omakase/bin/omakase-gate.sh`, bash — see §1) gains ONE
check right after the existing `OMAKASE_SKIP_<NAME>` env bypass (the check itself is
POSIX-safe):

```sh
DIS="$common/omakase/disabled-gates"
if [ -n "$common" ] && [ -f "$DIS" ] && cut -f1 -- "$DIS" | grep -qxF -- "$NAME"; then
  echo "omakase-gate[$NAME]: OFF (persisted; omakase enable $NAME to re-arm)"
  exit 0
fi
```

Trivial POSIX (`cut` + `grep -qxF`), missing file = everything enabled, no ledger row on
skip. `OMAKASE_SKIP_<NAME>=1` remains the one-command ephemeral bypass; the file is the
remembered one. Orthogonal to placed.tsv's per-FILE `enabled` column (files vs gates are
different axes; disabling a gate must not delete its files).

**Self-heal graft (from design B, both judges):** if `disable` runs where the placed
`omakase-gate.sh` predates this check (a v1 repo), the binary re-places that ONE file
from its embedded base payload (offline) and fixes its placed.tsv row — a toggle is never
a silent no-op.

## 7. Instruction files — thin mapping, no merging [CUT — reroute/bridge never shipped; omakase places instruction files verbatim, see docs/reference.md]

Canonical authoring rule: a harness ships ONE instruction file, `payload/AGENTS.md`.
A literal data table in the binary (mirrored in docs/reference.md; swap rows when the
AGENTS.md standard converges — data, not a subsystem) fans it out. Routing is
role-free: it depends only on whether the ROOT INSTRUCTION SLOT is free, not on which
layer (first or second) is asking. Whichever installed layer FIRST places a root
`AGENTS.md` owns the slot for as long as it stays installed (§4); this is decided by
the caller and handed to the mapping rule as one boolean, `rootSlotFree`:

| Payload file | Root slot | Claude Code | Copilot CLI |
|---|---|---|---|
| `AGENTS.md` | free (no committed `AGENTS.md`/`CLAUDE.md` at root, and no already-installed layer owns the slot) | placed as-is at root + bridge `CLAUDE.md → AGENTS.md` (symlink; **only if** nothing already provides `CLAUDE.md`) | reads root AGENTS.md natively — nothing extra placed |
| `AGENTS.md` | taken | **rerouted to `CLAUDE.local.md`** — Claude Code's additive, gitignored slot; these instructions ADD to whatever owns the root slot, never replace it. Narrated: `instructions from <label> -> CLAUDE.local.md (root slot taken)` | **honest gap — DECIDED, §8** |
| `CLAUDE.md` (shipped explicitly) | n/a | as-is; whole-file, later layer wins; committed copy skipped as always (v1 semantics, unaffected by slot-fallback) | reads root CLAUDE.md natively |
| `.github/copilot-instructions.md` | n/a | — | as-is (file-level exclude under the shared `.github` topdir) |

Every slot is a normal omakase placement: excluded via `.git/info/exclude`, healed on
checkout, reversed by remove; a committed target is skipped and reported — the
universal rule, no special case. Un-reroute is wired to **BOTTOM-layer removal only**
(§4/§5): `omakase remove <source>` on the BOTTOM layer frees the slot and moves the
survivor's own `AGENTS.md`, if it had been rerouted, back to the root. Removing a TOP
layer never triggers an un-reroute, even in the narrow case where the TOP layer is the
one that ended up owning the slot (see the known limitation in §8).

## 8. Copilot additive-slot gap — DECIDED: honest gap (provisional)

Copilot CLI has no per-repo gitignored additive slot (no `CLAUDE.local.md`
equivalent). **Decision: a rerouted instruction file (§7) is Claude-only for now.**
This is design intent, not yet surfaced in `status` — today `status` carries no
Copilot-visibility note at all; the reroute is visible only as the per-file `from
<label>` origin row `status` prints for the placed `CLAUDE.local.md` path (§3, §12).
Revisit when the AGENTS.md standard grows a local slot — the mapping table (§7) exists
for exactly this, and this decision costs one table row to change.

Rejected for now: placing `.github/copilot-instructions.md` when absent (occupies a
conventionally *committed team* file, and silently fails where teams commit it) and
routing to user-global `~/.copilot/copilot-instructions.md` (machine-wide, outside the
per-repo overlay/remove model — a special case in an otherwise uniform design).

**Known limitation (deferred):** un-reroute only runs on a BOTTOM-layer removal
(§4/§7). In the narrow case where the TOP layer ends up owning the root slot — the
bottom layer's payload didn't ship `AGENTS.md` at first install (or was blocked by a
since-removed committed instruction file) while the top layer claimed the free slot,
and the bottom layer's `AGENTS.md` was the one rerouted to `CLAUDE.local.md` —
`omakase remove <top-source>` does NOT hand the slot back: the survivor's instructions
stay stuck in `CLAUDE.local.md` (invisible to Copilot, per above) with no root
`AGENTS.md` or bridge restored. The fix (top-removal un-reroute) is deferred to a
later phase; it is not built in this batch.

Verified against live Copilot docs 2026-07-02: root `CLAUDE.md` IS read by Copilot
(so the slot-owning layer needs no Copilot-specific placement), `CLAUDE.local.md` is
NOT. One more future option recorded: `COPILOT_CUSTOM_INSTRUCTIONS_DIRS` (env var
naming directories whose `AGENTS.md` Copilot reads) could carry a per-user additive
slot — rejected for now because it requires mutating the user's shell profile, which
omakase never does.

## 9. Migration from v1 (grafts from design C, both judges) [CUT — moot, since no stacking/pins shipped; see supersession note above]

- **Lazy, read-only synthesis:** first v2 run of ANY verb in a v1 repo synthesizes
  `sources.tsv` from `$OMK/source` with `commit = '-'` (never guessed), touching no
  working-tree file. `status`/`remove` work immediately on v1 state.
- First real `init`/`update` records resolved commits, builds `$OMK/layers/`, rewrites
  placed.tsv (same frozen 5 columns, the winning layer's source label in col 3),
  regenerates hook scripts (same reader contracts + the disabled-gates check).
- **Refuse-don't-guess:** any operation needing `$OMK/layers/` before it exists
  (`omakase remove <source>` before any real `init` has built the layer store) errors
  with "run omakase init once first".
- **Mixed-era detection:** v2 notices `sources.tsv` disagreeing with `$OMK/source` +
  `placed.tsv` (a v1 tool ran after a v2 stacked install and orphan-swept the top
  layer) and reports/reheals on the next run. Window is real (plugin-dist one-session
  lag); keep the transition short.
- **No compat for Phase-3-era state:** an earlier v2 prototype wrote `sources.tsv`
  rows labeled with `project`/`personal` roles instead of ordinals. That surface
  never reached a user — zero back-compat for it. Only genuine v1 state
  (`$OMK/source`, no `sources.tsv` at all) is migrated, per the lazy synthesis above.
- Carried forward: pre-0.10 `placed.list` fallback, `ledger.tsv` 6-col rotation.

## 10. Compatibility gates (release blockers)

1. **v1 suite passes untouched:** `bin/*.sh` become thin shims onto the Go binary, and
   v2 must pass the existing 2,526-line black-box suite VERBATIM before any
   output-wording changes land.
2. **Round-trip tests:** bash-initialized repo → v2 `remove` reverses byte-exactly;
   v2-written state → v1 sh hook readers parse identically; exactly-5-column writer test
   on placed.tsv (fewer columns also break the readers: an empty `$enabled` skips every
   row, failing open).
3. **Downstream:** pixterm-harness chain re-verified (built dists vendor `bin/`; the
   shims keep entry points stable) before the old bash bodies are deleted.

## 11. Distribution

One unit, unchanged channels: the repo/plugin ships a thin sh bootstrap (the proven
`lib-lefthook.sh` pattern: pinned version, baked per-platform sha256, curl→wget, atomic
cache install at `~/.cache/omakase/bin/<version>/`) that fetches the omakase Go binary
from GitHub releases. Hook-time never depends on the binary; a botched fetch can never
weaken an installed repo's gates. `remove` keeps working from the previously cached
binary (no network on uninstall). No npm channel unless a concrete user appears.

Release mechanics: goreleaser (or equivalent) builds 4 targets (darwin/linux ×
arm64/x86_64); each release bumps the bootstrap's pinned version + 4 hashes — the same
motion as a lefthook re-pin.

## 12. Known risks (accepted, with mitigations)

| Risk | Mitigation |
|---|---|
| placed.tsv 6th-column trap for future contributors | header comment + writer-side format test |
| bare-init semantic change breaks muscle memory | per-run transition notice; docs/skills wording |
| stack order is state — the same two sources stacked in a different order (or in two different repos) can leave different files winning | narrated on every `init`/`remove` (`stacked`/`overrides`/`removed`/`restored` lines) + `status`'s per-file Injected rows name each file's winning layer (`from <label>`). A live stack-order identity line and an explicit shadow-with-consequence flag in `status` are design intent, not yet built (unscheduled) — see §3. |
| stale disabled-gates row after a gate rename silently re-arms nothing | status flags "disabled gate X is not wired" |
| a layer's pin only refreshes at `init`/`update`, never automatically | by design; no daemon |
| binary release blast radius | hook-time is sh; remove works offline from cache; checksum-pinned bootstrap |

## 13. Implementation plan [Superseded — phases 3.5+ were built then reverted; see supersession note above]

| Phase | Deliverable | Gate |
|---|---|---|
| 0 | Compat scaffolding: round-trip tests, exact-5-col writer test, derived-in-run golden byte assertions | tests exist and pass against v1 bash |
| 1 | Go binary skeleton + `status` (read-only, v1 state) | v1 suite status tests pass via shim |
| 2 | `init` (single-source parity) + `remove` + sh template generation | full v1 suite passes via shims |
| 3 | Layers: sources.tsv, `$OMK/layers/`, `personal` verb, migration + mixed-era detection | new layer tests + v1 suite |
| 3.5 | Init-stack surface: kill the `personal` verb/global setting/`--no-personal`; a second `init <source>` stacks instead of replacing (temporal precedence, cap 2, narrated); `remove <source>` unlayers one harness; role-free slot-fallback instruction routing | rewritten `tests/layers.test.sh` (142 assertions) + full v1 suite |
| 4 | `update` (+ `--check`), pin semantics, transition notices | new pin tests |
| 5 | `enable`/`disable` + gate-primitive check + self-heal | new toggle tests |
| 6 | Bootstrap + release pipeline; plugin skills updated (init/status/remove + update/enable/disable); share/import retired | both-host verification |
| 7 | Delete old bash bodies (shims remain); docs rewrite; pixterm chain re-verified | downstream re-init works |
