#!/usr/bin/env bash
# tests/layers.test.sh — the Phase-3.5 BLACK-BOX oracle for omakase's STACK/UNLAYER
# behavior (design §4/§5/§7/§9). Where init-remove-parity.test.sh proves the Go
# init/remove match the frozen v1 bash byte-for-byte, THIS suite drives the shipped
# entry points (`bash bin/init.sh` / `bash bin/status.sh` / `bash bin/remove.sh` and
# the `omakase` binary directly) exactly as an adopter would, and pins the behavior
# v1 never had and Phase 3.5 built: a SECOND source stacks on top (cap 2, narrated),
# a re-init of a known source REPAIRS in place (no reorder), `remove <source>`
# UNLAYERS one harness (top or bottom, twin-diff to a fresh single-source install),
# the §7 slot-fallback reroute + sidecar marker, lazy v1→v2 sources.tsv synthesis,
# and the deleted `personal` verb (now an unknown command, exit 2).
#
# There is no v1 oracle for any of this — the expected bytes are pinned as EXACT
# strings COPIED from the Go source that emits them and CONFIRMED against live
# `bin/*.sh` shim runs of the built binary (init.go's stacked/overrides/fallback +
# cap lines, layers.go's RemoveLayer summary + per-file bullets, remove.go's
# not-installed line, migrate.go's RequireLayers refusal). Each GC5 byte assertion
# is either a full-stdout/stderr `cmp` (remove/cap/unknown — those streams carry
# nothing else) or an exact-line `have` (init's summary carries many other lines);
# a handful of `hasnt` mutation spot-checks (hyphen-for-em-dash, dropped caret
# space) prove the compares are real. The — dashes below are U+2014 EM DASH.
#
# Isolation (never touches the real machine): HOME + XDG_CONFIG_HOME + XDG_CACHE_HOME
# all point INSIDE $TMP, so the source clone cache and any per-user config are
# private. Sources are local git repos built in-test — no network (GC10). The Go
# binary is pinned via OMAKASE_BIN so the shims exec it directly (never rebuild
# under the fake HOME, never fall back to legacy bash); a ran_go guard fails on the
# fallback notice. The base payload folded under every source is the REAL
# $ROOT/payload (the binary resolves it binary-relative, NOT via OMAKASE_PAYLOAD) —
# identical for every install, so the twin-diffs hold.
#
# Skip-with-notice (exit 0) when dist/omakase is absent AND go is not on PATH. Every
# scenario installs hooks, so the WHOLE suite SKIPs as a group when lefthook is
# unresolvable — the same gate init-remove-parity uses. bash 3.2-safe throughout.
set -u

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="$HERE/.."
BIN="$ROOT/dist/omakase"
SHIM_INIT="$ROOT/bin/init.sh"
SHIM_STATUS="$ROOT/bin/status.sh"
SHIM_REMOVE="$ROOT/bin/remove.sh"
LEG_INIT="$ROOT/bin/legacy/init.sh"
LEFTHOOK="${LEFTHOOK_BIN:-$(command -v lefthook || true)}"
TMP="${TMPDIR:-/tmp}/layers.$$"
NOTICE_INIT='omakase: Go binary not present — running the bundled v1 init script'
NOTICE_STATUS='omakase: Go binary not present — running the bundled v1 status script'
NOTICE_REMOVE='omakase: Go binary not present — running the bundled v1 remove script'
NOW=1750000000   # pins status age math so two back-to-back status runs are byte-identical
FAILED=0
pass(){ echo "  PASS: $1"; }
fail(){ echo "  FAIL: $1"; FAILED=1; }
skip(){ echo "  SKIP: $1"; }

[ -n "$LEFTHOOK" ] && export PATH="$(dirname "$LEFTHOOK"):$PATH"
HAVE_LH=0; { [ -n "$LEFTHOOK" ] && [ -x "$LEFTHOOK" ]; } && HAVE_LH=1

# --- build/skip gate (copied from init-remove-parity): skip ONLY when there is no
# binary AND no go to build one; otherwise (re)build so the shim never runs stale ---
if [ ! -x "$BIN" ] && ! command -v go >/dev/null 2>&1; then
  echo "SKIP: dist/omakase absent and go not on PATH — the layers suite cannot run"
  exit 0
fi
if command -v go >/dev/null 2>&1; then
  ( cd "$ROOT" && CGO_ENABLED=0 go build -o dist/omakase ./cmd/omakase ) \
    || { echo "  FAIL: go build failed — cannot run the layers suite"; exit 1; }
fi

if [ "$HAVE_LH" -eq 0 ]; then
  echo "SKIP: the layers suite needs lefthook (every scenario installs hooks); LEFTHOOK_BIN unset and lefthook not on PATH"
  exit 0
fi

mkdir -p "$TMP"
FAKEHOME="$TMP/home"; mkdir -p "$FAKEHOME"
XDGC="$TMP/xdg-config"; XDGCACHE="$TMP/xdg-cache"; mkdir -p "$XDGC" "$XDGCACHE"

# --- repo + source builders (local git only; no network) ---
newrepo(){ rm -rf "$1"; mkdir -p "$1"; ( cd "$1" && git init -q && git config user.email t@t && git config user.name t && git config commit.gpgsign false && git commit -q --allow-empty -m init ); }
resolvep(){ ( cd "$1" && pwd -P ); }
common_of(){ ( cd "$1" && cd "$(git rev-parse --git-common-dir)" && pwd ); }
omk_of(){ echo "$(common_of "$1")/omakase"; }
src_init(){ rm -rf "$1"; mkdir -p "$1"; ( cd "$1" && git init -q && git config user.email s@s && git config user.name s && git config commit.gpgsign false ); }
src_commit(){ ( cd "$1" && git add -A && git commit -q -m "${2:-src}" ); }
w(){ mkdir -p "$(dirname "$1")"; printf '%s\n' "$2" > "$1"; }   # write a file, creating parent dirs
digest_file(){ if command -v shasum >/dev/null 2>&1; then shasum -a 256 "$1" | awk '{print $1}'; else sha256sum "$1" | awk '{print $1}'; fi; }

# build a valid harness source at $1 named $2; remaining args are rel=content pairs.
mksrc(){ local dir="$1" name="$2"; shift 2; src_init "$dir"; printf 'name: %s\n' "$name" > "$dir/omakase.manifest"
  local kv; for kv in "$@"; do w "$dir/payload/${kv%%=*}" "${kv#*=}"; done; src_commit "$dir" "$name"; }

# placed.tsv helpers (frozen 5-col rel<TAB>kind<TAB>source<TAB>sha256<TAB>enabled).
placed_col(){ awk -F'\t' -v r="$2" -v c="$3" '$1==r{print $c; exit}' "$1"; }  # $1 file $2 rel $3 colnum
placed_has(){ awk -F'\t' -v r="$2" '$1==r{f=1} END{exit f?0:1}' "$1"; }        # $1 file $2 rel
# sources.tsv helpers (layer<TAB>source<TAB>ref<TAB>commit<TAB>installed_epoch).
src_field(){ awk -F'\t' -v n="$2" -v c="$3" 'NR==n{print $c; exit}' "$1"; }    # $1 file $2 rownum $3 col

# --- run helpers: the shipped shims exec the pinned binary directly (OMAKASE_BIN) ---
gi(){ local cwd="$1" out="$2" err="$3"; shift 3   # init via the shim
  ( cd "$cwd" && env -u OMAKASE_ICON -u NO_COLOR -u OMAKASE_PAYLOAD HOME="$FAKEHOME" XDG_CONFIG_HOME="$XDGC" XDG_CACHE_HOME="$XDGCACHE" LEFTHOOK_BIN="$LEFTHOOK" OMAKASE_BIN="$BIN" bash "$SHIM_INIT" "$@" ) >"$out" 2>"$err"; }
st(){ local cwd="$1" out="$2" err="$3"; shift 3   # status via the shim (age math pinned)
  ( cd "$cwd" && env -u OMAKASE_ICON -u NO_COLOR -u OMAKASE_PAYLOAD HOME="$FAKEHOME" XDG_CONFIG_HOME="$XDGC" XDG_CACHE_HOME="$XDGCACHE" LEFTHOOK_BIN="$LEFTHOOK" OMAKASE_BIN="$BIN" OMAKASE_NOW="$NOW" bash "$SHIM_STATUS" "$@" ) >"$out" 2>"$err"; }
rmv(){ local cwd="$1" out="$2" err="$3"; shift 3  # remove via the shim
  ( cd "$cwd" && env -u OMAKASE_ICON -u NO_COLOR -u OMAKASE_PAYLOAD HOME="$FAKEHOME" XDG_CONFIG_HOME="$XDGC" XDG_CACHE_HOME="$XDGCACHE" LEFTHOOK_BIN="$LEFTHOOK" OMAKASE_BIN="$BIN" bash "$SHIM_REMOVE" "$@" ) >"$out" 2>"$err"; }
verb(){ local cwd="$1" out="$2" err="$3"; shift 3 # the binary directly (for the deleted `personal` verb + help)
  ( cd "$cwd" && env -u OMAKASE_ICON -u NO_COLOR -u OMAKASE_PAYLOAD HOME="$FAKEHOME" XDG_CONFIG_HOME="$XDGC" XDG_CACHE_HOME="$XDGCACHE" LEFTHOOK_BIN="$LEFTHOOK" "$BIN" "$@" ) >"$out" 2>"$err"; }
leg_init(){ local cwd="$1" out="$2" err="$3"; shift 3   # the frozen v1 body → a genuine v1-era $OMK (no sources.tsv/layers)
  ( cd "$cwd" && env -u OMAKASE_BIN -u OMAKASE_ICON -u NO_COLOR -u OMAKASE_PAYLOAD HOME="$FAKEHOME" XDG_CONFIG_HOME="$XDGC" XDG_CACHE_HOME="$XDGCACHE" LEFTHOOK_BIN="$LEFTHOOK" bash "$LEG_INIT" "$@" ) >"$out" 2>"$err"; }

# guard: a shim invocation must NOT have fallen back to legacy bash (else we'd be
# testing bash, not the Go layering engine). Called on every shim-side stderr.
ran_go(){ local label="$1" errf="$2" notice="$3"
  if grep -qF "$notice" "$errf"; then fail "$label: shim fell back to legacy bash (the Go binary did not run)"; sed 's/^/      /' "$errf"; return 1; fi
  return 0; }

# --- assertions ---
ok(){ [ "$1" = "$2" ] && pass "$3" || fail "$3 (got '$1' want '$2')"; }
have(){ grep -qF -- "$2" "$1" 2>/dev/null && pass "$3" || { fail "$3 — missing exact line: $2"; sed 's/^/      /' "$1"; }; }
hasnt(){ grep -qF -- "$2" "$1" 2>/dev/null && fail "$3 — unexpectedly present: $2" || pass "$3"; }
is_exit(){ [ "$1" -eq "$2" ] && pass "$3 (exit $2)" || fail "$3 (exit $1, want $2)"; }
gone(){ [ ! -e "$1" ] && [ ! -L "$1" ] && pass "$2" || fail "$2 — still present: $1"; }
present(){ { [ -e "$1" ] || [ -L "$1" ]; } && pass "$2" || fail "$2 — missing: $1"; }
cmp_exact(){ local lbl="$1" got="$2" exp="$3"; cmp -s "$got" "$exp" && pass "$lbl" || { fail "$lbl — bytes differ"; diff "$exp" "$got" | sed 's/^/      /'; }; }
same(){ local lbl="$1" a="$2" b="$3"; cmp -s "$a" "$b" && pass "$lbl" || { fail "$lbl — differ"; diff "$a" "$b" | sed 's/^/      /'; }; }
is40(){ echo "$1" | grep -qE '^[0-9a-f]{40}$' && pass "$2" || fail "$2 (got '$1', want 40-hex)"; }
clean_tree(){ [ -z "$(cd "$1" && git status --porcelain)" ] && pass "$2" || { fail "$2 — git status not clean"; (cd "$1" && git status --porcelain | sed 's/^/      /'); }; }

# snap_full: a content fingerprint of EVERY file+symlink under a dir (for
# before/after no-mutation checks; includes clobbered/, no normalization).
snap_full(){ ( cd "$1" 2>/dev/null && find . -mindepth 1 \( -type f -o -type l \) 2>/dev/null | LC_ALL=C sort | while IFS= read -r p; do
    if [ -L "$p" ]; then printf '%s -> %s\n' "$p" "$(readlink "$p")"; else printf '%s : %s\n' "$p" "$(digest_file "$p")"; fi
  done ) > "$2"; }

# omk_manifest: the twin-diff fingerprint of the FULL $OMK tree — every regular
# file's content and every symlink's target — with EXACTLY TWO normalizations,
# mirroring internal/overlay/remove_test.go's omkTreeForTwin: (1) sources.tsv field
# 5 (epoch) -> EPOCH; (2) clobbered/ excluded. \037 terminates each file body so a
# body can never be confused with the next header.
omk_manifest(){ local omk="$1" out="$2"; : > "$out"; local rel full
  ( cd "$omk" && find . -mindepth 1 \( -path './clobbered' -o -path './clobbered/*' \) -prune -o \( -type f -o -type l \) -print ) \
    | sed 's|^\./||' | LC_ALL=C sort | while IFS= read -r rel; do
      [ -z "$rel" ] && continue; full="$omk/$rel"
      if [ -L "$full" ]; then printf 'SYMLINK\t%s\t%s\n' "$rel" "$(readlink "$full")" >> "$out"
      elif [ -f "$full" ]; then
        printf 'FILE\t%s\n' "$rel" >> "$out"
        if [ "$rel" = "sources.tsv" ]; then awk -F'\t' 'BEGIN{OFS="\t"}{if(NF==5)$5="EPOCH"; print}' "$full" >> "$out"
        else cat "$full" >> "$out"; fi
        printf '\037\n' >> "$out"
      fi
    done; }

# wt_manifest: the working-tree bytes of every placed path (placed.tsv col1) —
# proves the two twins' PLACED files agree, not just their $OMK state.
wt_manifest(){ local root="$1" omk="$2" out="$3"; : > "$out"; local rel full
  cut -f1 "$omk/placed.tsv" | LC_ALL=C sort | while IFS= read -r rel; do
    [ -z "$rel" ] && continue; full="$root/$rel"
    if [ -L "$full" ]; then printf 'SYMLINK\t%s\t%s\n' "$rel" "$(readlink "$full")" >> "$out"
    elif [ -f "$full" ]; then printf 'FILE\t%s\n' "$rel" >> "$out"; cat "$full" >> "$out"; printf '\037\n' >> "$out"
    else printf 'MISSING\t%s\n' "$rel" >> "$out"; fi
  done; }

# excl_block: the omakase marked block of an exclude file (markers inclusive),
# path-free by construction (every entry is a repo-relative prefix).
excl_block(){ awk '/^# >>> omakase-harness >>>$/{p=1} p{print} /^# <<< omakase-harness <<<$/{p=0}' "$1" 2>/dev/null; }

# twin_equal: the GC7 twin-diff — an unlayered repo (A) and a fresh single-source
# install (B) agree byte-for-byte on the FULL $OMK tree + the exclude block + every
# placed working-tree file, and both working trees are git-clean.
twin_equal(){ local lbl="$1" ra="$2" rb="$3"; local oa ob; oa="$(omk_of "$ra")"; ob="$(omk_of "$rb")"
  omk_manifest "$oa" "$TMP/$lbl.omkA"; omk_manifest "$ob" "$TMP/$lbl.omkB"
  same "$lbl: \$OMK tree byte-equal (epoch-normalized, clobbered excluded)" "$TMP/$lbl.omkA" "$TMP/$lbl.omkB"
  same "$lbl: placed.tsv byte-identical" "$oa/placed.tsv" "$ob/placed.tsv"
  excl_block "$(common_of "$ra")/info/exclude" > "$TMP/$lbl.exA"; excl_block "$(common_of "$rb")/info/exclude" > "$TMP/$lbl.exB"
  same "$lbl: exclude block byte-identical" "$TMP/$lbl.exA" "$TMP/$lbl.exB"
  wt_manifest "$ra" "$oa" "$TMP/$lbl.wtA"; wt_manifest "$rb" "$ob" "$TMP/$lbl.wtB"
  same "$lbl: placed working-tree files byte-identical" "$TMP/$lbl.wtA" "$TMP/$lbl.wtB"
  clean_tree "$ra" "$lbl: unlayered repo git-clean"; clean_tree "$rb" "$lbl: fresh repo git-clean"; }

# ============================================================================
# L1 — STACK: init A → init B stacks B on top. Both file sets live, B wins the
# overlap, the §7 reroute + bridge, the two ordinal sources.tsv rows w/ resolved
# commits, and the exact GC5 stacked/overrides/fallback narration bytes.
# ============================================================================
echo "== L1: init A then init B stacks B on top (narration + winners + sources) =="
L1A="$TMP/l1-a"; mksrc "$L1A" a "AGENTS.md=A doctrine" ".claude/rules/a.md=A rule" ".omakase/gates/shared.sh=A"; L1AA="$(resolvep "$L1A")"
L1B="$TMP/l1-b"; mksrc "$L1B" b "AGENTS.md=B doctrine" ".omakase/gates/shared.sh=B" ".omakase/gates/bonly.sh=B only"; L1BB="$(resolvep "$L1B")"
R1="$TMP/l1-repo"; newrepo "$R1"; OMK1="$(omk_of "$R1")"
gi "$R1" "$TMP/L1a.out" "$TMP/L1a.err" --source "$L1AA"; r1a=$?
gi "$R1" "$TMP/L1b.out" "$TMP/L1b.err" --source "$L1BB"; r1b=$?
if ran_go "L1-A" "$TMP/L1a.err" "$NOTICE_INIT" && ran_go "L1-B" "$TMP/L1b.err" "$NOTICE_INIT"; then
  is_exit "$r1a" 0 "L1: init A"
  is_exit "$r1b" 0 "L1: init B stacks"
  # winners on disk
  ok "$(cat "$R1/AGENTS.md" 2>/dev/null)" "A doctrine" "L1: A owns the root AGENTS.md"
  ok "$(cat "$R1/CLAUDE.local.md" 2>/dev/null)" "B doctrine" "L1: B rerouted to CLAUDE.local.md"
  ok "$(cat "$R1/.omakase/gates/shared.sh" 2>/dev/null)" "B" "L1: B wins the overlapping gate"
  ok "$(cat "$R1/.claude/rules/a.md" 2>/dev/null)" "A rule" "L1: A-only file placed"
  ok "$(cat "$R1/.omakase/gates/bonly.sh" 2>/dev/null)" "B only" "L1: B-only file placed"
  ok "$(cat "$R1/.omakase/gates/example.sh" 2>/dev/null | head -1)" "#!/usr/bin/env bash" "L1: base machinery folded in under the stack"
  ok "$(readlink "$R1/CLAUDE.md" 2>/dev/null)" "AGENTS.md" "L1: §7 bridge CLAUDE.md -> AGENTS.md"
  # GC5 stacking narration (exact lines on init B's stdout)
  have "$TMP/L1b.out" "omakase: stacked ${L1BB} on top of ${L1AA}" "L1: stacked line exact"
  have "$TMP/L1b.out" "  ^ overrides ${L1AA}: .omakase/gates/shared.sh" "L1: override line exact (two-space caret-space)"
  have "$TMP/L1b.out" "omakase: instructions from ${L1BB} -> CLAUDE.local.md (root slot taken)" "L1: slot-fallback line exact"
  # mutation spot-checks: the em-dash-free / dropped-space variants must be ABSENT
  hasnt "$TMP/L1b.out" "  ^overrides ${L1AA}: .omakase/gates/shared.sh" "L1: dropped caret-space variant absent (compare is real)"
  hasnt "$TMP/L1b.out" "omakase: instructions from ${L1BB} -> CLAUDE.local.md (root slot free)" "L1: wrong-parenthetical variant absent (compare is real)"
  # only the PRIOR path B now wins is narrated (bonly is new, not an override)
  hasnt "$TMP/L1b.out" "  ^ overrides ${L1AA}: .omakase/gates/bonly.sh" "L1: a brand-new B path is not narrated as an override"
  # placed.tsv col3 = winning layer's SOURCE LABEL
  ok "$(placed_col "$OMK1/placed.tsv" ".omakase/gates/shared.sh" 3)" "$L1BB" "L1: shared.sh col3 = B (winner)"
  ok "$(placed_col "$OMK1/placed.tsv" "CLAUDE.local.md" 3)" "$L1BB" "L1: CLAUDE.local.md col3 = B"
  ok "$(placed_col "$OMK1/placed.tsv" ".claude/rules/a.md" 3)" "$L1AA" "L1: a.md col3 = A"
  ok "$(placed_col "$OMK1/placed.tsv" "AGENTS.md" 3)" "$L1AA" "L1: AGENTS.md col3 = A"
  ok "$(placed_col "$OMK1/placed.tsv" "CLAUDE.md" 3)" "$L1AA" "L1: bridge CLAUDE.md col3 = A"
  # sources.tsv: two ordinal rows, bottom-to-top, both commits resolved
  ok "$(src_field "$OMK1/sources.tsv" 1 1)" "1" "L1: row1 layer ordinal = 1 (bottom)"
  ok "$(src_field "$OMK1/sources.tsv" 1 2)" "$L1AA" "L1: row1 source = A"
  ok "$(src_field "$OMK1/sources.tsv" 1 3)" "-" "L1: row1 ref placeholder = -"
  is40 "$(src_field "$OMK1/sources.tsv" 1 4)" "L1: row1 commit is a resolved 40-hex sha"
  ok "$(src_field "$OMK1/sources.tsv" 2 1)" "2" "L1: row2 layer ordinal = 2 (top)"
  ok "$(src_field "$OMK1/sources.tsv" 2 2)" "$L1BB" "L1: row2 source = B"
  is40 "$(src_field "$OMK1/sources.tsv" 2 4)" "L1: row2 commit is a resolved 40-hex sha"
  # both layer stores built at ordinal dirs; the reroute sidecar on layers/2
  present "$OMK1/layers/1/placed.tsv" "L1: layers/1 store built"
  present "$OMK1/layers/2/placed.tsv" "L1: layers/2 store built"
  printf 'CLAUDE.local.md\tAGENTS.md\n' > "$TMP/L1.reroute.exp"
  cmp_exact "L1: layers/2 reroute sidecar = CLAUDE.local.md<TAB>AGENTS.md" "$OMK1/layers/2/rerouted" "$TMP/L1.reroute.exp"
  gone "$OMK1/layers/1/rerouted" "L1: bottom layer (root owner) has NO reroute sidecar"
  clean_tree "$R1" "L1: git status clean after the stack"
fi

# ============================================================================
# L2 — REPAIR (no reorder): with A+B installed, re-init A repairs layer 1 in place
# (sources.tsv + placed.tsv byte-identical, no stacked line, no third layer, B still
# wins); re-init B is likewise idempotent. Reuses the L1 repo.
# ============================================================================
echo "== L2: re-init a known source REPAIRS in place (no reorder, no stack) =="
cp "$OMK1/sources.tsv" "$TMP/L2.sources.pre"; cp "$OMK1/placed.tsv" "$TMP/L2.placed.pre"
gi "$R1" "$TMP/L2a.out" "$TMP/L2a.err" --source "$L1AA"; r2a=$?
if ran_go "L2-A" "$TMP/L2a.err" "$NOTICE_INIT"; then
  is_exit "$r2a" 0 "L2: re-init A"
  same "L2: sources.tsv byte-identical after re-init A (no reorder)" "$OMK1/sources.tsv" "$TMP/L2.sources.pre"
  same "L2: placed.tsv byte-identical after re-init A" "$OMK1/placed.tsv" "$TMP/L2.placed.pre"
  hasnt "$TMP/L2a.out" "stacked " "L2: re-init A narrates NO stack"
  gone "$OMK1/layers/3" "L2: re-init A created no third layer"
  ok "$(cat "$R1/.omakase/gates/shared.sh" 2>/dev/null)" "B" "L2: B still wins the overlap after re-init A"
fi
gi "$R1" "$TMP/L2b.out" "$TMP/L2b.err" --source "$L1BB"; r2b=$?
if ran_go "L2-B" "$TMP/L2b.err" "$NOTICE_INIT"; then
  is_exit "$r2b" 0 "L2: re-init B"
  same "L2: sources.tsv byte-identical after re-init B" "$OMK1/sources.tsv" "$TMP/L2.sources.pre"
  same "L2: placed.tsv byte-identical after re-init B" "$OMK1/placed.tsv" "$TMP/L2.placed.pre"
  hasnt "$TMP/L2b.out" "stacked " "L2: re-init B narrates NO stack"
fi

# ============================================================================
# L3 — CAP: with two sources installed, a third distinct source errors (exit 1) with
# the exact GC5 cap line and mutates NOTHING ($OMK + working tree byte-identical
# before/after, the third gate never placed).
# ============================================================================
echo "== L3: a third distinct source hits the 2-harness cap, exit 1, zero mutation =="
L3A="$TMP/l3-a"; mksrc "$L3A" a ".omakase/gates/a.sh=a"; L3AA="$(resolvep "$L3A")"
L3B="$TMP/l3-b"; mksrc "$L3B" b ".omakase/gates/b.sh=b"; L3BB="$(resolvep "$L3B")"
L3C="$TMP/l3-c"; mksrc "$L3C" c ".omakase/gates/c.sh=c"; L3CC="$(resolvep "$L3C")"
R3="$TMP/l3-repo"; newrepo "$R3"; OMK3="$(omk_of "$R3")"
gi "$R3" "$TMP/L3a.out" "$TMP/L3a.err" --source "$L3AA" >/dev/null 2>&1
gi "$R3" "$TMP/L3b.out" "$TMP/L3b.err" --source "$L3BB" >/dev/null 2>&1
snap_full "$OMK3" "$TMP/L3.omk.pre"
gi "$R3" "$TMP/L3c.out" "$TMP/L3c.err" --source "$L3CC"; r3c=$?
if ran_go "L3-cap" "$TMP/L3c.err" "$NOTICE_INIT"; then
  is_exit "$r3c" 1 "L3: third source refused"
  printf 'omakase: this repo already has 2 harnesses (%s, %s) — remove one first: omakase remove <source>\n' "$L3AA" "$L3BB" > "$TMP/L3.cap.exp"
  cmp_exact "L3: cap stderr is exactly the GC5 cap line (em-dash)" "$TMP/L3c.err" "$TMP/L3.cap.exp"
  [ ! -s "$TMP/L3c.out" ] && pass "L3: cap stdout empty" || fail "L3: cap wrote stdout"
  hasnt "$TMP/L3.cap.exp" "already has 2 harnesses ($L3AA, $L3BB) - remove one first" "L3: hyphen-for-em-dash variant is not what the binary emitted"
  snap_full "$OMK3" "$TMP/L3.omk.post"
  same "L3: \$OMK byte-identical before/after the refused cap" "$TMP/L3.omk.pre" "$TMP/L3.omk.post"
  gone "$R3/.omakase/gates/c.sh" "L3: third source's gate never placed"
  clean_tree "$R3" "L3: working tree clean after the refused cap"
fi

# ============================================================================
# L4 — REMOVE TOP (GC7a): init A; init B; remove B  ≡  a fresh init-A-only repo.
# Exact GC5 removed summary + per-file bullets; B's reroute swept; twin-diff.
# ============================================================================
echo "== L4: remove the TOP source; twin-diff to a fresh init-A-only repo =="
L4A="$TMP/l4-a"; mksrc "$L4A" a "AGENTS.md=A doctrine" ".claude/rules/a.md=A rule" ".omakase/gates/shared.sh=A" ".omakase/gates/aonly.sh=A only"; L4AA="$(resolvep "$L4A")"
L4B="$TMP/l4-b"; mksrc "$L4B" b "AGENTS.md=B doctrine" ".omakase/gates/shared.sh=B" ".omakase/gates/bonly.sh=B only"; L4BB="$(resolvep "$L4B")"
R4="$TMP/l4-repo"; newrepo "$R4"; OMK4="$(omk_of "$R4")"
gi "$R4" "$TMP/L4a.out" "$TMP/L4a.err" --source "$L4AA" >/dev/null 2>&1
gi "$R4" "$TMP/L4b.out" "$TMP/L4b.err" --source "$L4BB" >/dev/null 2>&1
rmv "$R4" "$TMP/L4r.out" "$TMP/L4r.err" "$L4BB"; r4r=$?
if ran_go "L4-remove" "$TMP/L4r.err" "$NOTICE_REMOVE"; then
  is_exit "$r4r" 0 "L4: remove B (top)"
  # stdout is EXACTLY the summary + three bullets in lexical rel order (nothing else)
  { printf 'omakase: removed %s — 2 file(s) deleted, 1 restored from %s\n' "$L4BB" "$L4AA"
    printf '  - deleted: .omakase/gates/bonly.sh\n'
    printf '  ^ restored: .omakase/gates/shared.sh\n'
    printf '  - deleted: CLAUDE.local.md\n'; } > "$TMP/L4.rm.exp"
  cmp_exact "L4: remove stdout is exactly the GC5 summary + bullets (em-dash, lexical bullets)" "$TMP/L4r.out" "$TMP/L4.rm.exp"
  hasnt "$TMP/L4.rm.exp" "omakase: removed $L4BB - 2 file(s) deleted" "L4: hyphen-for-em-dash variant is not what the binary emitted"
  # B's traces gone; A's survive
  gone "$R4/CLAUDE.local.md" "L4: B's rerouted CLAUDE.local.md swept"
  gone "$R4/.omakase/gates/bonly.sh" "L4: sole-B gate deleted"
  ok "$(cat "$R4/.omakase/gates/shared.sh" 2>/dev/null)" "A" "L4: shared.sh restored to A's copy"
  ok "$(cat "$R4/.omakase/gates/aonly.sh" 2>/dev/null)" "A only" "L4: A-only gate untouched"
  ok "$(cat "$R4/AGENTS.md" 2>/dev/null)" "A doctrine" "L4: A still owns the root AGENTS.md"
  ok "$(readlink "$R4/CLAUDE.md" 2>/dev/null)" "AGENTS.md" "L4: A's bridge intact"
  ok "$(src_field "$OMK4/sources.tsv" 1 2)" "$L4AA" "L4: sources.tsv left with the sole A row"
  gone "$OMK4/layers/2" "L4: layers/2 removed after top removal"
  # twin: a repo that only ever installed A
  R4T="$TMP/l4-twin"; newrepo "$R4T"
  gi "$R4T" "$TMP/L4t.out" "$TMP/L4t.err" --source "$L4AA"; r4t=$?
  ran_go "L4-twin" "$TMP/L4t.err" "$NOTICE_INIT" && is_exit "$r4t" 0 "L4: fresh init-A-only twin"
  twin_equal "L4-twin" "$R4" "$R4T"
fi

# ============================================================================
# L5 — REMOVE BOTTOM (GC7b): init A; init B; remove A  ≡  a fresh init-B-only repo.
# The survivor B is re-folded with the base and its instructions UN-reroute back to
# the root slot (+ bridge); twin-diff. (Counts vary with the base fold, so the
# summary is pinned by its stable prefix/suffix, not its interior counts.)
# ============================================================================
echo "== L5: remove the BOTTOM source; survivor un-reroutes; twin-diff to fresh init-B =="
L5A="$TMP/l5-a"; mksrc "$L5A" a "AGENTS.md=A doctrine" ".omakase/gates/a.sh=a gate"; L5AA="$(resolvep "$L5A")"
L5B="$TMP/l5-b"; mksrc "$L5B" b "AGENTS.md=B doctrine" ".omakase/gates/b.sh=b gate"; L5BB="$(resolvep "$L5B")"
R5="$TMP/l5-repo"; newrepo "$R5"; OMK5="$(omk_of "$R5")"
gi "$R5" "$TMP/L5a.out" "$TMP/L5a.err" --source "$L5AA" >/dev/null 2>&1
gi "$R5" "$TMP/L5b.out" "$TMP/L5b.err" --source "$L5BB" >/dev/null 2>&1
ok "$(cat "$R5/CLAUDE.local.md" 2>/dev/null)" "B doctrine" "L5: precheck — B rerouted to CLAUDE.local.md at stack time"
rmv "$R5" "$TMP/L5r.out" "$TMP/L5r.err" "$L5AA"; r5r=$?
if ran_go "L5-remove" "$TMP/L5r.err" "$NOTICE_REMOVE"; then
  is_exit "$r5r" 0 "L5: remove A (bottom)"
  head -1 "$TMP/L5r.out" > "$TMP/L5.rm.head"
  have "$TMP/L5.rm.head" "omakase: removed ${L5AA} — " "L5: removed-summary names A with the em-dash separator"
  have "$TMP/L5.rm.head" " restored from ${L5BB}" "L5: removed-summary names B as the survivor"
  hasnt "$TMP/L5.rm.head" "omakase: removed ${L5AA} - " "L5: hyphen-for-em-dash variant absent (compare is real)"
  # survivor's instructions moved back to the root slot; the old reroute swept
  ok "$(cat "$R5/AGENTS.md" 2>/dev/null)" "B doctrine" "L5: B's AGENTS.md un-rerouted to the root"
  ok "$(readlink "$R5/CLAUDE.md" 2>/dev/null)" "AGENTS.md" "L5: survivor bridge placed after un-reroute"
  gone "$R5/CLAUDE.local.md" "L5: stale CLAUDE.local.md swept"
  gone "$OMK5/layers/1/rerouted" "L5: rebuilt survivor store carries no stale reroute marker"
  ok "$(cat "$R5/.omakase/gates/b.sh" 2>/dev/null)" "b gate" "L5: survivor B's gate remains"
  ok "$(src_field "$OMK5/sources.tsv" 1 2)" "$L5BB" "L5: sources.tsv renumbered to the sole survivor B (bottom)"
  gone "$OMK5/layers/2" "L5: stale layers/2 dropped"
  # twin: a repo that only ever installed B
  R5T="$TMP/l5-twin"; newrepo "$R5T"
  gi "$R5T" "$TMP/L5t.out" "$TMP/L5t.err" --source "$L5BB"; r5t=$?
  ran_go "L5-twin" "$TMP/L5t.err" "$NOTICE_INIT" && is_exit "$r5t" 0 "L5: fresh init-B-only twin"
  ok "$(cat "$R5T/AGENTS.md" 2>/dev/null)" "B doctrine" "L5: fresh init B places AGENTS.md at the free root"
  twin_equal "L5-twin" "$R5" "$R5T"
fi

# ============================================================================
# L6 — REMOVE UNKNOWN: `remove <bogus>` in a 2-stack refuses with the exact GC5
# not-installed line naming the installed harnesses, exit 1, mutating NOTHING.
# ============================================================================
echo "== L6: remove an un-installed source name refuses, exit 1, zero mutation =="
L6A="$TMP/l6-a"; mksrc "$L6A" a ".omakase/gates/a.sh=a"; L6AA="$(resolvep "$L6A")"
L6B="$TMP/l6-b"; mksrc "$L6B" b ".omakase/gates/b.sh=b"; L6BB="$(resolvep "$L6B")"
R6="$TMP/l6-repo"; newrepo "$R6"; OMK6="$(omk_of "$R6")"
gi "$R6" "$TMP/L6a.out" "$TMP/L6a.err" --source "$L6AA" >/dev/null 2>&1
gi "$R6" "$TMP/L6b.out" "$TMP/L6b.err" --source "$L6BB" >/dev/null 2>&1
snap_full "$OMK6" "$TMP/L6.omk.pre"
rmv "$R6" "$TMP/L6u.out" "$TMP/L6u.err" "nope/nope"; r6u=$?
if ran_go "L6-unknown" "$TMP/L6u.err" "$NOTICE_REMOVE"; then
  is_exit "$r6u" 1 "L6: unknown source refused"
  printf "omakase: no harness 'nope/nope' installed here (installed: %s, %s)\n" "$L6AA" "$L6BB" > "$TMP/L6.exp"
  cmp_exact "L6: stderr is exactly the GC5 not-installed line + installed list" "$TMP/L6u.err" "$TMP/L6.exp"
  [ ! -s "$TMP/L6u.out" ] && pass "L6: unknown-source stdout empty" || fail "L6: unknown-source wrote stdout"
  snap_full "$OMK6" "$TMP/L6.omk.post"
  same "L6: \$OMK byte-identical after the refused unknown-source remove" "$TMP/L6.omk.pre" "$TMP/L6.omk.post"
  clean_tree "$R6" "L6: working tree clean after the refused remove"
fi

# ============================================================================
# L7 — INSTRUCTION STACKING: A ships AGENTS.md (root + bridge); init B shipping
# AGENTS.md reroutes to CLAUDE.local.md + prints the fallback line; the reroute
# sidecar is on layers/2; remove B restores the root ownership cleanly.
# ============================================================================
echo "== L7: §7 instruction stacking (root/bridge, reroute + sidecar, clean restore) =="
L7A="$TMP/l7-a"; mksrc "$L7A" a "AGENTS.md=A doctrine"; L7AA="$(resolvep "$L7A")"
L7B="$TMP/l7-b"; mksrc "$L7B" b "AGENTS.md=B doctrine"; L7BB="$(resolvep "$L7B")"
R7="$TMP/l7-repo"; newrepo "$R7"; OMK7="$(omk_of "$R7")"
gi "$R7" "$TMP/L7a.out" "$TMP/L7a.err" --source "$L7AA"; r7a=$?
gi "$R7" "$TMP/L7b.out" "$TMP/L7b.err" --source "$L7BB"; r7b=$?
if ran_go "L7-A" "$TMP/L7a.err" "$NOTICE_INIT" && ran_go "L7-B" "$TMP/L7b.err" "$NOTICE_INIT"; then
  is_exit "$r7a" 0 "L7: init A"; is_exit "$r7b" 0 "L7: init B"
  ok "$(cat "$R7/AGENTS.md" 2>/dev/null)" "A doctrine" "L7: A owns the root AGENTS.md"
  ok "$(readlink "$R7/CLAUDE.md" 2>/dev/null)" "AGENTS.md" "L7: A gets the §7 bridge"
  ok "$(cat "$R7/CLAUDE.local.md" 2>/dev/null)" "B doctrine" "L7: B's AGENTS.md rerouted to CLAUDE.local.md"
  have "$TMP/L7b.out" "omakase: instructions from ${L7BB} -> CLAUDE.local.md (root slot taken)" "L7: B's slot-fallback narration exact"
  printf 'CLAUDE.local.md\tAGENTS.md\n' > "$TMP/L7.reroute.exp"
  cmp_exact "L7: layers/2 reroute sidecar = CLAUDE.local.md<TAB>AGENTS.md<LF>" "$OMK7/layers/2/rerouted" "$TMP/L7.reroute.exp"
  gone "$OMK7/layers/1/rerouted" "L7: root-owner layer 1 has no reroute sidecar"
  # remove B restores root ownership cleanly (CLAUDE.local.md gone; root still A's)
  rmv "$R7" "$TMP/L7r.out" "$TMP/L7r.err" "$L7BB"; r7r=$?
  if ran_go "L7-remove" "$TMP/L7r.err" "$NOTICE_REMOVE"; then
    is_exit "$r7r" 0 "L7: remove B"
    gone "$R7/CLAUDE.local.md" "L7: CLAUDE.local.md gone after remove B (root ownership restored cleanly)"
    ok "$(cat "$R7/AGENTS.md" 2>/dev/null)" "A doctrine" "L7: A still owns the root AGENTS.md"
    ok "$(readlink "$R7/CLAUDE.md" 2>/dev/null)" "AGENTS.md" "L7: A's bridge intact after remove B"
    clean_tree "$R7" "L7: git status clean after remove B"
  fi
fi

# ============================================================================
# L8 — COMMITTED INSTRUCTIONS: a repo commits its own CLAUDE.md, taking the root
# slot. (a) a source shipping the canonical AGENTS.md reroutes to CLAUDE.local.md +
# prints the fallback line (v2 slot-fallback); (b) a source shipping an EXPLICIT
# CLAUDE.md is SKIPPED (v1 committed-file-wins). The committed file is untouched
# in both.
# ============================================================================
echo "== L8: a committed CLAUDE.md — AGENTS.md reroutes (v2); an explicit CLAUDE.md is skipped (v1) =="
# (a) canonical AGENTS.md payload -> reroute
L8A="$TMP/l8-a"; mksrc "$L8A" a "AGENTS.md=A doctrine"; L8AA="$(resolvep "$L8A")"
R8="$TMP/l8-repo"; newrepo "$R8"
printf 'TEAM CLAUDE\n' > "$R8/CLAUDE.md"; ( cd "$R8" && git add CLAUDE.md && git commit -q -m team )
gi "$R8" "$TMP/L8a.out" "$TMP/L8a.err" --source "$L8AA"; r8a=$?
if ran_go "L8a" "$TMP/L8a.err" "$NOTICE_INIT"; then
  is_exit "$r8a" 0 "L8a: init A over a committed CLAUDE.md"
  ok "$(cat "$R8/CLAUDE.local.md" 2>/dev/null)" "A doctrine" "L8a: canonical AGENTS.md rerouted to CLAUDE.local.md (root slot taken)"
  gone "$R8/AGENTS.md" "L8a: no root AGENTS.md placed (slot taken by committed CLAUDE.md)"
  ok "$(cat "$R8/CLAUDE.md" 2>/dev/null)" "TEAM CLAUDE" "L8a: committed CLAUDE.md left byte-untouched"
  [ ! -L "$R8/CLAUDE.md" ] && pass "L8a: committed CLAUDE.md is not a bridge symlink" || fail "L8a: bridge overwrote the committed CLAUDE.md"
  have "$TMP/L8a.out" "omakase: instructions from ${L8AA} -> CLAUDE.local.md (root slot taken)" "L8a: slot-fallback line exact"
  clean_tree "$R8" "L8a: git status clean"
fi
# (b) explicit CLAUDE.md payload -> v1 skip (committed file wins)
L8B="$TMP/l8-b"; mksrc "$L8B" b "CLAUDE.md=SOURCE CLAUDE" ".omakase/gates/g.sh=g"; L8BB="$(resolvep "$L8B")"
R8b="$TMP/l8-repo-b"; newrepo "$R8b"
printf 'TEAM CLAUDE\n' > "$R8b/CLAUDE.md"; ( cd "$R8b" && git add CLAUDE.md && git commit -q -m team )
gi "$R8b" "$TMP/L8b.out" "$TMP/L8b.err" --source "$L8BB"; r8b=$?
if ran_go "L8b" "$TMP/L8b.err" "$NOTICE_INIT"; then
  is_exit "$r8b" 0 "L8b: init an explicit-CLAUDE.md source over a committed CLAUDE.md"
  ok "$(cat "$R8b/CLAUDE.md" 2>/dev/null)" "TEAM CLAUDE" "L8b: committed CLAUDE.md wins (source CLAUDE.md skipped, v1 semantics)"
  [ ! -L "$R8b/CLAUDE.md" ] && pass "L8b: CLAUDE.md is the committed regular file, not a symlink" || fail "L8b: source symlinked over the committed CLAUDE.md"
  gone "$R8b/CLAUDE.local.md" "L8b: nothing rerouted (source shipped no canonical AGENTS.md)"
  have "$TMP/L8b.err" "omakase: SKIP (already tracked) CLAUDE.md" "L8b: the committed CLAUDE.md is reported skipped"
  ok "$(cat "$R8b/.omakase/gates/g.sh" 2>/dev/null)" "g" "L8b: the source's non-instruction file still placed"
  clean_tree "$R8b" "L8b: git status clean"
fi

# ============================================================================
# L9 — HEAL: with A+B installed, deleting a B-won file and an A-only file, a bare
# init re-places BOTH from the merged view and leaves sources.tsv untouched.
# ============================================================================
echo "== L9: bare init heals a stacked repo from the merged view (sources.tsv untouched) =="
L9A="$TMP/l9-a"; mksrc "$L9A" a ".omakase/gates/shared.sh=A" ".claude/rules/a.md=A rule"; L9AA="$(resolvep "$L9A")"
L9B="$TMP/l9-b"; mksrc "$L9B" b ".omakase/gates/shared.sh=B"; L9BB="$(resolvep "$L9B")"
R9="$TMP/l9-repo"; newrepo "$R9"; OMK9="$(omk_of "$R9")"
gi "$R9" "$TMP/L9a.out" "$TMP/L9a.err" --source "$L9AA" >/dev/null 2>&1
gi "$R9" "$TMP/L9b.out" "$TMP/L9b.err" --source "$L9BB" >/dev/null 2>&1
cp "$OMK9/sources.tsv" "$TMP/L9.sources.pre"
rm -f "$R9/.omakase/gates/shared.sh" "$R9/.claude/rules/a.md"   # delete a B-won file + an A-only file
gi "$R9" "$TMP/L9h.out" "$TMP/L9h.err"; r9h=$?
if ran_go "L9-heal" "$TMP/L9h.err" "$NOTICE_INIT"; then
  is_exit "$r9h" 0 "L9: bare init heals"
  ok "$(cat "$R9/.omakase/gates/shared.sh" 2>/dev/null)" "B" "L9: B-won file re-placed from the merged view"
  ok "$(cat "$R9/.claude/rules/a.md" 2>/dev/null)" "A rule" "L9: A-only file re-placed from the merged view"
  same "L9: sources.tsv untouched by the heal" "$OMK9/sources.tsv" "$TMP/L9.sources.pre"
  clean_tree "$R9" "L9: git status clean after the heal"
fi

# ============================================================================
# L10 — MIGRATION + VOCABULARY: a genuine v1-era $OMK (legacy init, no sources.tsv /
# no layers/) → `status` synthesizes the ordinal sources.tsv silently (commit '-',
# two runs byte-identical); `remove <src>` before any v2 init hits RequireLayers;
# the deleted `personal` verb is an unknown command (exit 2). Then the GC4 grep.
# ============================================================================
echo "== L10: lazy v1->v2 synthesis, RequireLayers refusal, deleted personal verb, GC4 vocab =="
L10S="$TMP/l10-src"; mksrc "$L10S" proj ".omakase/gates/g.sh=g10"; L10SS="$(resolvep "$L10S")"
R10="$TMP/l10-repo"; newrepo "$R10"; OMK10="$(omk_of "$R10")"
leg_init "$R10" "$TMP/L10leg.out" "$TMP/L10leg.err" --source "$L10SS"; r10leg=$?
is_exit "$r10leg" 0 "L10: legacy (v1) init built the v1-era repo"
gone "$OMK10/sources.tsv" "L10: precondition — no sources.tsv after a v1 install"
gone "$OMK10/layers" "L10: precondition — no layers/ after a v1 install"
st "$R10" "$TMP/L10s1.out" "$TMP/L10s1.err"; r10s1=$?
if ran_go "L10-status1" "$TMP/L10s1.err" "$NOTICE_STATUS"; then
  is_exit "$r10s1" 0 "L10: first status"
  present "$OMK10/sources.tsv" "L10: first status synthesized sources.tsv"
  ok "$(src_field "$OMK10/sources.tsv" 1 1)" "1" "L10: synthesized row layer ordinal = 1"
  ok "$(src_field "$OMK10/sources.tsv" 1 2)" "$L10SS" "L10: synthesized row source = the remembered \$OMK/source"
  ok "$(src_field "$OMK10/sources.tsv" 1 4)" "-" "L10: synthesized commit = '-' (never guessed for a v1-era repo)"
  hasnt "$TMP/L10s1.err" "WARNING —" "L10: synthesis is silent (no mixed-era warning)"
fi
st "$R10" "$TMP/L10s2.out" "$TMP/L10s2.err"; r10s2=$?
if ran_go "L10-status2" "$TMP/L10s2.err" "$NOTICE_STATUS"; then
  same "L10: status stdout byte-identical pre/post synthesis" "$TMP/L10s1.out" "$TMP/L10s2.out"
fi
# remove <src> before any v2 init → RequireLayers refusal (no layers/ store)
rmv "$R10" "$TMP/L10r.out" "$TMP/L10r.err" "$L10SS"; r10r=$?
if ran_go "L10-remove" "$TMP/L10r.err" "$NOTICE_REMOVE"; then
  is_exit "$r10r" 1 "L10: remove <src> on a pre-layers repo refuses"
  printf 'omakase: this repo predates layered state — run omakase init once first\n' > "$TMP/L10.reqlayers.exp"
  cmp_exact "L10: stderr is exactly the RequireLayers refusal (em-dash)" "$TMP/L10r.err" "$TMP/L10.reqlayers.exp"
  [ ! -s "$TMP/L10r.out" ] && pass "L10: RequireLayers refusal wrote no stdout" || fail "L10: RequireLayers refusal wrote stdout"
fi
# the deleted `personal` verb is an unknown command (exit 2)
verb "$R10" "$TMP/L10p.out" "$TMP/L10p.err" personal off; r10p=$?
is_exit "$r10p" 2 "L10: 'omakase personal' is now an unknown command"
printf 'omakase: unknown command "personal"\n' > "$TMP/L10.personal.exp"
cmp_exact "L10: unknown-command stderr names personal" "$TMP/L10p.err" "$TMP/L10.personal.exp"
[ ! -s "$TMP/L10p.out" ] && pass "L10: personal verb wrote no stdout" || fail "L10: personal verb wrote stdout"

# ---- GC4 vocabulary grep (CONTROLLER RULING) ----
# Scope: (a) the binary's usage/help output (main usage + each verb's -h) and (b)
# the captured stdout/stderr of the L1-L9 scenario runs. Assert neither carries the
# token `personal` nor `add` as a STANDALONE VERB TOKEN. Grep is case-SENSITIVE
# whole-word lowercase: verbs are lowercase, so prose "Add this command"/lefthook's
# "Added config" (both current, non-frozen strings) are correctly NOT verb tokens.
# Explicitly NOT grepped: the binary itself, legacy bash bodies, or v1-frozen
# collision/cut-over strings — none of which the L1-L9 scenarios exercise.
verb "$R10" "$TMP/help-main.out" "$TMP/help-main.err"                # main usage (no verb) -> exit 2
verb "$R10" "$TMP/help-init.out" "$TMP/help-init.err" init -h
verb "$R10" "$TMP/help-remove.out" "$TMP/help-remove.err" remove -h
RCLEAN="$TMP/l10-clean"; newrepo "$RCLEAN"
verb "$RCLEAN" "$TMP/help-status.out" "$TMP/help-status.err" status -h
GC4_FILES="$TMP/help-main.out $TMP/help-main.err $TMP/help-init.out $TMP/help-init.err $TMP/help-remove.out $TMP/help-remove.err $TMP/help-status.out $TMP/help-status.err"
# L1-L9 captures only: exclude every L10* file (L10 deliberately carries the
# 'personal' unknown-command string and the RequireLayers refusal — not verb vocab).
GC4_FILES="$GC4_FILES $(ls "$TMP"/L*.out "$TMP"/L*.err 2>/dev/null | grep -vE '/L10')"
if grep -wln 'personal' $GC4_FILES 2>/dev/null | grep -q .; then
  fail "L10/GC4: a 'personal' verb token leaked into help or an L1-L9 capture"; grep -wln 'personal' $GC4_FILES | sed 's/^/      /'
else pass "L10/GC4: no 'personal' token in usage/help or any L1-L9 output"; fi
if grep -wln 'add' $GC4_FILES 2>/dev/null | grep -q .; then
  fail "L10/GC4: an 'add' standalone verb token leaked into help or an L1-L9 capture"; grep -wln 'add' $GC4_FILES | sed 's/^/      /'
else pass "L10/GC4: no 'add' standalone verb token in usage/help or any L1-L9 output"; fi

rm -rf "$TMP"
echo ""
[ "$FAILED" -eq 0 ] && echo "ALL PASS" || { echo "FAILURES PRESENT"; exit 1; }
