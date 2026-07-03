#!/usr/bin/env bash
# tests/layers.test.sh — the Phase-3 BLACK-BOX oracle for omakase's NEW layered
# behavior (design §4/§5/§7/§9). Where init-remove-parity.test.sh proves the Go
# init/remove match the frozen v1 bash byte-for-byte, THIS suite drives the shipped
# entry points (`bash bin/init.sh` / `bash bin/status.sh` / `bash bin/remove.sh` /
# `dist/omakase personal`) exactly as an adopter would and pins the behavior v1 never
# had: the personal layer, the CLAUDE.local.md reroute, the §7 bridge, --no-personal
# persistence, `personal off` unlayering, the GC8 refusal, lazy v1→v2 migration, and
# mixed-era reheal.
#
# There is no v1 oracle for any of this — the expected bytes are pinned as EXACT
# strings COPIED from the Go source that emits them (grep init.go for the two summary
# lines, personal.go for the verb's lines, migrate.go for the warnings). Every fixture
# carries a want() anti-rot marker (modelled on init-remove-parity's want / status-
# parity's expect_marker) so a fixture edit that stops exercising the intended state
# fails loudly instead of going vacuously green.
#
# Isolation (never touches the real machine): HOME + XDG_CONFIG_HOME + XDG_CACHE_HOME
# all point INSIDE $TMP, so the per-user personal-config slot
# (${XDG_CONFIG_HOME:-~/.config}/omakase/personal) and the source cache are private.
# Sources are local git repos built in-test — no network. The Go binary is pinned via
# OMAKASE_BIN so the shims exec it directly (never rebuild under the fake HOME, never
# fall back to legacy bash); a ran_go guard fails on the fallback notice.
#
# Skip-with-notice (exit 0) when dist/omakase is absent AND go is not on PATH. Every
# scenario installs hooks (or migrates a hook-installed repo), so the WHOLE suite SKIPs
# as a group when lefthook is unresolvable — the same gate init-remove-parity uses.
# bash 3.2-safe throughout.
set -u

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="$HERE/.."
BIN="$ROOT/dist/omakase"
SHIM_INIT="$ROOT/bin/init.sh"
SHIM_STATUS="$ROOT/bin/status.sh"
SHIM_REMOVE="$ROOT/bin/remove.sh"
LEG_INIT="$ROOT/bin/legacy/init.sh"
PAY="$ROOT/payload"
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

# ---- EXACT expected bytes, COPIED from the Go source (never retyped) ----
# init.go:887 / :889 (the two new summary lines; %s is the personal label, filled per
# scenario). personal.go:137/:155/:161/:170/:455 (the verb's lines). migrate.go:71/:138.
SKIPPED_LINE="omakase: personal harness skipped in this repo (init --no-personal was set; re-init after 'omakase personal' changes to reconsider)."
CLEARED_LINE="omakase: personal harness cleared."
APPLY_OFF_LINE="omakase: this repo has personal layering off (init --no-personal); not applied here."
GC8_LINE="omakase: this repo predates layered state — run omakase init once first"
MIXED_AXIS1='($OMK/source disagrees with sources.tsv)'   # single-quoted: literal $OMK

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
XDGC="$TMP/xdg-config"        # the per-user personal-config slot lives HERE, never ~/.config
XDGCACHE="$TMP/xdg-cache"     # the source clone cache lives HERE, never ~/.cache
mkdir -p "$XDGC" "$XDGCACHE"

# --- repo + source builders (local git only; no network) ---
newrepo(){ rm -rf "$1"; mkdir -p "$1"; ( cd "$1" && git init -q && git config user.email t@t && git config user.name t && git config commit.gpgsign false && git commit -q --allow-empty -m init ); }
resolvep(){ ( cd "$1" && pwd -P ); }
common_of(){ ( cd "$1" && cd "$(git rev-parse --git-common-dir)" && pwd ); }
omk_of(){ echo "$(common_of "$1")/omakase"; }
src_init(){ rm -rf "$1"; mkdir -p "$1"; ( cd "$1" && git init -q && git config user.email s@s && git config user.name s && git config commit.gpgsign false ); }
src_commit(){ ( cd "$1" && git add -A && git commit -q -m "${2:-src}" ); }
w(){ mkdir -p "$(dirname "$1")"; printf '%s\n' "$2" > "$1"; }   # write a file, creating parent dirs
digest(){ if command -v shasum >/dev/null 2>&1; then printf '%s' "$1" | shasum -a 256 | awk '{print $1}'; else printf '%s' "$1" | sha256sum | awk '{print $1}'; fi; }
digest_file(){ if command -v shasum >/dev/null 2>&1; then shasum -a 256 "$1" | awk '{print $1}'; else sha256sum "$1" | awk '{print $1}'; fi; }
set_personal(){ mkdir -p "$XDGC/omakase"; printf '%s\n' "$1" > "$XDGC/omakase/personal"; }
clear_personal(){ rm -f "$XDGC/omakase/personal"; }
snap_tree(){ ( cd "$1" && find . -mindepth 1 -name .git -prune -o -print | sed 's|^\./||' | sort ); }

# placed.tsv helpers (frozen 5-col path<TAB>kind<TAB>source<TAB>sha256<TAB>enabled).
placed_col(){ awk -F'\t' -v r="$2" -v c="$3" '$1==r{print $c; exit}' "$1"; }  # $1 file $2 rel $3 colnum
placed_has(){ awk -F'\t' -v r="$2" '$1==r{f=1} END{exit f?0:1}' "$1"; }        # $1 file $2 rel
# sources.tsv helpers (layer<TAB>source<TAB>ref<TAB>commit<TAB>installed_epoch).
src_field(){ awk -F'\t' -v n="$2" -v c="$3" 'NR==n{print $c; exit}' "$1"; }    # $1 file $2 rownum $3 col
src_epoch_of(){ awk -F'\t' -v l="$2" -v s="$3" '$1==l && $2==s{print $5; exit}' "$1"; }

# --- run helpers ---
# EXTRA_ENV (reset per scenario) carries scenario-specific NAME=VALUE pairs (only
# OMAKASE_PAYLOAD, for the base-only L9). Paths under $TMP hold no spaces, so the
# unquoted expansion is safe. The Go side pins OMAKASE_BIN to the prebuilt binary so
# the shim execs Go directly instead of rebuilding per call under the fake HOME.
EXTRA_ENV=""
gi(){ local cwd="$1" out="$2" err="$3"; shift 3   # go init via the shim
  ( cd "$cwd" && env -u OMAKASE_ICON -u NO_COLOR HOME="$FAKEHOME" XDG_CONFIG_HOME="$XDGC" XDG_CACHE_HOME="$XDGCACHE" LEFTHOOK_BIN="$LEFTHOOK" OMAKASE_BIN="$BIN" $EXTRA_ENV bash "$SHIM_INIT" "$@" ) >"$out" 2>"$err"; }
st(){ local cwd="$1" out="$2" err="$3"; shift 3   # status via the shim (age math pinned)
  ( cd "$cwd" && env -u OMAKASE_ICON -u NO_COLOR HOME="$FAKEHOME" XDG_CONFIG_HOME="$XDGC" XDG_CACHE_HOME="$XDGCACHE" LEFTHOOK_BIN="$LEFTHOOK" OMAKASE_BIN="$BIN" OMAKASE_NOW="$NOW" $EXTRA_ENV bash "$SHIM_STATUS" "$@" ) >"$out" 2>"$err"; }
rmv(){ local cwd="$1" out="$2" err="$3"; shift 3  # remove via the shim
  ( cd "$cwd" && env -u OMAKASE_ICON -u NO_COLOR HOME="$FAKEHOME" XDG_CONFIG_HOME="$XDGC" XDG_CACHE_HOME="$XDGCACHE" LEFTHOOK_BIN="$LEFTHOOK" OMAKASE_BIN="$BIN" $EXTRA_ENV bash "$SHIM_REMOVE" "$@" ) >"$out" 2>"$err"; }
pers(){ local cwd="$1" out="$2" err="$3"; shift 3 # the personal verb — the binary directly (no shim)
  ( cd "$cwd" && env -u OMAKASE_ICON -u NO_COLOR HOME="$FAKEHOME" XDG_CONFIG_HOME="$XDGC" XDG_CACHE_HOME="$XDGCACHE" LEFTHOOK_BIN="$LEFTHOOK" $EXTRA_ENV "$BIN" personal "$@" ) >"$out" 2>"$err"; }
leg_init(){ local cwd="$1" out="$2" err="$3"; shift 3   # the frozen v1 body → a genuine v1-era $OMK
  ( cd "$cwd" && env -u OMAKASE_BIN -u OMAKASE_ICON -u NO_COLOR HOME="$FAKEHOME" XDG_CONFIG_HOME="$XDGC" XDG_CACHE_HOME="$XDGCACHE" LEFTHOOK_BIN="$LEFTHOOK" $EXTRA_ENV bash "$LEG_INIT" "$@" ) >"$out" 2>"$err"; }

# guard: a shim invocation must NOT have fallen back to legacy bash (else we'd be
# testing bash, not the Go layering engine). Called on every shim-side stderr.
ran_go(){ local label="$1" errf="$2" notice="$3"
  if grep -qF "$notice" "$errf"; then fail "$label: shim fell back to legacy bash (the Go binary did not run)"; sed 's/^/      /' "$errf"; return 1; fi
  return 0; }

# want <label> <file> <marker...>: fixed-string containment — pins the fixture
# exercised the intended state (anti-rot). No markers => no-op.
want(){ local l="$1" f="$2"; shift 2; [ "$#" -eq 0 ] && return 0
  local p miss=""
  for p in "$@"; do grep -qF -- "$p" "$f" 2>/dev/null || miss="${miss:+$miss, }'$p'"; done
  [ -z "$miss" ] && pass "$l: output carries the expected marker(s)" \
                 || fail "$l: output MISSING marker(s) $miss — fixture may not exercise the intended state"; }

ok(){ [ "$1" = "$2" ] && pass "$3" || { fail "$3 (got '$1' want '$2')"; }; }
have(){ grep -qF -- "$2" "$1" 2>/dev/null && pass "$3" || { fail "$3 — missing: $2"; sed 's/^/      /' "$1"; }; }
hasnt(){ grep -qF -- "$2" "$1" 2>/dev/null && { fail "$3 — unexpected: $2"; } || pass "$3"; }
is_exit(){ [ "$1" -eq "$2" ] && pass "$3 (exit $2)" || fail "$3 (exit $1, want $2)"; }

# ============================================================================
# L1 — personal set → init reroutes AGENTS.md to CLAUDE.local.md, per-row col-3
# labels, sources.tsv bottom-to-top, the layered-on-top summary line.
# ============================================================================
echo "== L1: project+personal stack, reroute + labels + sources + summary =="
P1="$TMP/l1proj"; src_init "$P1"
printf 'name: proj\n' > "$P1/omakase.manifest"
w "$P1/payload/.claude/rules/r.md" 'proj rule'
w "$P1/payload/.omakase/gates/shared.sh" 'PROJECT'
src_commit "$P1" proj; P1A="$(resolvep "$P1")"
S1="$TMP/l1pers"; src_init "$S1"
printf 'name: personal-harness\n' > "$S1/omakase.manifest"
w "$S1/payload/AGENTS.md" 'personal doctrine'
w "$S1/payload/.omakase/gates/shared.sh" 'PERSONAL'
src_commit "$S1" pers; S1A="$(resolvep "$S1")"
set_personal "$S1A"
R1="$TMP/l1repo"; newrepo "$R1"; OMK1="$(omk_of "$R1")"
gi "$R1" "$TMP/l1.out" "$TMP/l1.err" --source "$P1A"; r1=$?
if ran_go "L1" "$TMP/l1.err" "$NOTICE_INIT"; then
  is_exit "$r1" 0 "L1: init succeeded"
  want "L1" "$TMP/l1.out" "omakase: personal harness layered on top (${S1A}) — omakase personal off to remove it everywhere."
  ok "$(cat "$R1/CLAUDE.local.md" 2>/dev/null)" "personal doctrine" "L1: personal AGENTS.md rerouted to CLAUDE.local.md"
  [ ! -e "$R1/AGENTS.md" ] && pass "L1: no root AGENTS.md (personal never placed as-is)" || fail "L1: personal AGENTS.md placed at root"
  ok "$(cat "$R1/.omakase/gates/shared.sh" 2>/dev/null)" "PERSONAL" "L1: personal wins the overlap in the working tree"
  ok "$(cat "$R1/.claude/rules/r.md" 2>/dev/null)" "proj rule" "L1: project-only file placed"
  # placed.tsv col 3 = the winning layer's label
  ok "$(placed_col "$OMK1/placed.tsv" "CLAUDE.local.md" 3)" "$S1A" "L1: CLAUDE.local.md col3 = personal label"
  ok "$(placed_col "$OMK1/placed.tsv" ".omakase/gates/shared.sh" 3)" "$S1A" "L1: shared.sh col3 = personal label (personal won)"
  ok "$(placed_col "$OMK1/placed.tsv" ".claude/rules/r.md" 3)" "$P1A" "L1: project file col3 = project label"
  # sources.tsv: project (bottom) then personal (top)
  ok "$(src_field "$OMK1/sources.tsv" 1 1)" "project"  "L1: sources.tsv row1 layer = project (bottom)"
  ok "$(src_field "$OMK1/sources.tsv" 1 2)" "$P1A"     "L1: sources.tsv row1 source = project"
  ok "$(src_field "$OMK1/sources.tsv" 2 1)" "personal" "L1: sources.tsv row2 layer = personal (top)"
  ok "$(src_field "$OMK1/sources.tsv" 2 2)" "$S1A"     "L1: sources.tsv row2 source = personal"
  c1="$(src_field "$OMK1/sources.tsv" 1 4)"; echo "$c1" | grep -qE '^[0-9a-f]{40}$' && pass "L1: project row commit is a resolved 40-hex sha" || fail "L1: project commit = '$c1'"
  # both layer stores built (shadow-restore groundwork)
  ok "$(cat "$OMK1/layers/project/files/.omakase/gates/shared.sh" 2>/dev/null)" "PROJECT" "L1: project store keeps its own shared.sh copy"
  ok "$(cat "$OMK1/layers/personal/files/CLAUDE.local.md" 2>/dev/null)" "personal doctrine" "L1: personal store keeps CLAUDE.local.md"
fi

# ============================================================================
# L2 — stack override: same path from both layers → personal bytes + label win.
# ============================================================================
echo "== L2: overlap → personal wins bytes AND placed.tsv label =="
P2="$TMP/l2proj"; src_init "$P2"
printf 'name: proj2\n' > "$P2/omakase.manifest"
w "$P2/payload/.omakase/gates/dup.sh" 'PROJECT'
src_commit "$P2" p2; P2A="$(resolvep "$P2")"
S2="$TMP/l2pers"; src_init "$S2"
printf 'name: personal2\n' > "$S2/omakase.manifest"
w "$S2/payload/.omakase/gates/dup.sh" 'PERSONAL'
src_commit "$S2" s2; S2A="$(resolvep "$S2")"
set_personal "$S2A"
R2="$TMP/l2repo"; newrepo "$R2"; OMK2="$(omk_of "$R2")"
gi "$R2" "$TMP/l2.out" "$TMP/l2.err" --source "$P2A"; r2=$?
if ran_go "L2" "$TMP/l2.err" "$NOTICE_INIT"; then
  is_exit "$r2" 0 "L2: init succeeded"
  ok "$(cat "$R2/.omakase/gates/dup.sh" 2>/dev/null)" "PERSONAL" "L2: personal bytes win in the working tree"
  ok "$(placed_col "$OMK2/placed.tsv" ".omakase/gates/dup.sh" 3)" "$S2A" "L2: placed.tsv label = personal (higher layer)"
  ok "$(cat "$OMK2/layers/project/files/.omakase/gates/dup.sh" 2>/dev/null)" "PROJECT" "L2: project store still holds its shadowed copy"
fi

# ============================================================================
# L3 — §7 bridge: CLAUDE.md -> AGENTS.md placed for a project AGENTS.md; suppressed
# by a tracked CLAUDE.md; suppressed by a personal-shipped CLAUDE.md.
# ============================================================================
echo "== L3: CLAUDE.md -> AGENTS.md bridge (placed / two suppressions) =="
AGENTS_HASH="$(digest 'AGENTS.md')"
# (a) placed
P3="$TMP/l3proj"; src_init "$P3"
printf 'name: bridge\n' > "$P3/omakase.manifest"
w "$P3/payload/AGENTS.md" 'doctrine'
src_commit "$P3" p3; P3A="$(resolvep "$P3")"
clear_personal
R3="$TMP/l3repo"; newrepo "$R3"; OMK3="$(omk_of "$R3")"
gi "$R3" "$TMP/l3.out" "$TMP/l3.err" --source "$P3A"; r3=$?
if ran_go "L3a" "$TMP/l3.err" "$NOTICE_INIT"; then
  is_exit "$r3" 0 "L3a: init succeeded"
  tgt="$(readlink "$R3/CLAUDE.md" 2>/dev/null)"
  ok "$tgt" "AGENTS.md" "L3a: CLAUDE.md is a symlink -> AGENTS.md"
  ok "$(placed_col "$OMK3/placed.tsv" "CLAUDE.md" 3)" "$P3A" "L3a: bridge row col3 = project label"
  ok "$(placed_col "$OMK3/placed.tsv" "CLAUDE.md" 4)" "$AGENTS_HASH" "L3a: bridge row hash = sha256('AGENTS.md')"
fi
# (b) suppressed by a tracked CLAUDE.md
R3b="$TMP/l3repo-b"; newrepo "$R3b"
printf 'TEAM CLAUDE\n' > "$R3b/CLAUDE.md"; ( cd "$R3b" && git add CLAUDE.md && git commit -q -m team )
gi "$R3b" "$TMP/l3b.out" "$TMP/l3b.err" --source "$P3A"; r3b=$?
if ran_go "L3b" "$TMP/l3b.err" "$NOTICE_INIT"; then
  is_exit "$r3b" 0 "L3b: init succeeded"
  [ ! -L "$R3b/CLAUDE.md" ] && pass "L3b: committed CLAUDE.md suppresses the bridge (not a symlink)" || fail "L3b: bridge overwrote a committed CLAUDE.md"
  ok "$(cat "$R3b/CLAUDE.md")" "TEAM CLAUDE" "L3b: committed CLAUDE.md left byte-untouched"
fi
# (c) suppressed when the personal layer ships CLAUDE.md
S3="$TMP/l3pers"; src_init "$S3"
printf 'name: personal3\n' > "$S3/omakase.manifest"
w "$S3/payload/CLAUDE.md" 'PERSONAL CLAUDE'
src_commit "$S3" s3; S3A="$(resolvep "$S3")"
set_personal "$S3A"
R3c="$TMP/l3repo-c"; newrepo "$R3c"
gi "$R3c" "$TMP/l3c.out" "$TMP/l3c.err" --source "$P3A"; r3c=$?
if ran_go "L3c" "$TMP/l3c.err" "$NOTICE_INIT"; then
  is_exit "$r3c" 0 "L3c: init succeeded"
  [ ! -L "$R3c/CLAUDE.md" ] && pass "L3c: personal CLAUDE.md suppresses the bridge (not a symlink)" || fail "L3c: bridge placed despite a personal CLAUDE.md"
  ok "$(cat "$R3c/CLAUDE.md" 2>/dev/null)" "PERSONAL CLAUDE" "L3c: personal CLAUDE.md placed as-is (whole-file wins)"
fi
clear_personal

# ============================================================================
# L4 — --no-personal: flag skips + persists (off-row), bare re-init keeps skipping
# and prints the skipped line, personal-verb apply respects the off-row.
# ============================================================================
echo "== L4: --no-personal skip + persistence + verb-apply respect =="
P4="$TMP/l4proj"; src_init "$P4"
printf 'name: proj4\n' > "$P4/omakase.manifest"
w "$P4/payload/.omakase/gates/g.sh" 'g4'
src_commit "$P4" p4; P4A="$(resolvep "$P4")"
S4="$TMP/l4pers"; src_init "$S4"
printf 'name: personal4\n' > "$S4/omakase.manifest"
w "$S4/payload/AGENTS.md" 'personal doctrine'
src_commit "$S4" s4; S4A="$(resolvep "$S4")"
set_personal "$S4A"
R4="$TMP/l4repo"; newrepo "$R4"; OMK4="$(omk_of "$R4")"
# (1) init --source --no-personal → silent skip, off-row recorded
gi "$R4" "$TMP/l4a.out" "$TMP/l4a.err" --source "$P4A" --no-personal; r4a=$?
if ran_go "L4-1" "$TMP/l4a.err" "$NOTICE_INIT"; then
  is_exit "$r4a" 0 "L4-1: init --no-personal succeeded"
  [ ! -e "$R4/CLAUDE.local.md" ] && pass "L4-1: no personal layer placed" || fail "L4-1: --no-personal still placed CLAUDE.local.md"
  hasnt "$TMP/l4a.out" "personal harness" "L4-1: freshly-given flag prints NO personal line"
  ok "$(src_field "$OMK4/sources.tsv" 1 1)" "project"  "L4-1: sources row1 = project"
  ok "$(src_field "$OMK4/sources.tsv" 2 1)" "personal" "L4-1: sources row2 = personal (the off row)"
  ok "$(src_field "$OMK4/sources.tsv" 2 2)" "off"      "L4-1: personal row source = off"
  e4="$(src_epoch_of "$OMK4/sources.tsv" personal off)"
  echo "$e4" | grep -qE '^[0-9]+$' && pass "L4-1: off-row epoch is a unix decimal" || fail "L4-1: off-row epoch = '$e4'"
fi
# (2) bare re-init → still off, prints the skipped line, off-row epoch preserved
gi "$R4" "$TMP/l4b.out" "$TMP/l4b.err"; r4b=$?
if ran_go "L4-2" "$TMP/l4b.err" "$NOTICE_INIT"; then
  is_exit "$r4b" 0 "L4-2: bare re-init succeeded"
  [ ! -e "$R4/CLAUDE.local.md" ] && pass "L4-2: bare re-init keeps skipping the personal layer" || fail "L4-2: bare re-init placed a personal layer"
  have "$TMP/l4b.out" "$SKIPPED_LINE" "L4-2: bare re-init prints the remembered-skip line"
  ok "$(src_epoch_of "$OMK4/sources.tsv" personal off)" "$e4" "L4-2: off-row epoch preserved (no flag = no refresh)"
fi
# (3) personal-verb apply respects the off-row (announces, does not apply)
S4b="$TMP/l4pers-b"; src_init "$S4b"
printf 'name: personal4b\n' > "$S4b/omakase.manifest"
w "$S4b/payload/AGENTS.md" 'later doctrine'
src_commit "$S4b" s4b; S4bA="$(resolvep "$S4b")"
pers "$R4" "$TMP/l4c.out" "$TMP/l4c.err" "$S4bA"; r4c=$?
is_exit "$r4c" 0 "L4-3: personal set exit 0"
have "$TMP/l4c.out" "omakase: personal harness set to ${S4bA} — layered on every omakase init from now on." "L4-3: set-line printed"
have "$TMP/l4c.out" "$APPLY_OFF_LINE" "L4-3: apply respects the off-row (announces, no apply)"
[ ! -e "$R4/CLAUDE.local.md" ] && pass "L4-3: no personal layer applied over the off-row" || fail "L4-3: verb applied a personal layer despite --no-personal"
clear_personal

# ============================================================================
# L5 — `personal off` unlayers: overlap restored byte-exact, sole-personal clean
# deleted (incl. CLAUDE.local.md), sole-personal edited kept + warned, placed.tsv +
# sources.tsv + layers/ + exclude healed to the post-unlayer view.
# ============================================================================
echo "== L5: personal off (restore / delete / keep-warn / heal) =="
P5="$TMP/l5proj"; src_init "$P5"
printf 'name: proj5\n' > "$P5/omakase.manifest"
w "$P5/payload/.claude/rules/r.md" 'proj rule'
w "$P5/payload/.omakase/gates/shared.sh" 'PROJECT'
src_commit "$P5" p5; P5A="$(resolvep "$P5")"
S5="$TMP/l5pers"; src_init "$S5"
printf 'name: personal5\n' > "$S5/omakase.manifest"
w "$S5/payload/AGENTS.md" 'personal doctrine'          # -> CLAUDE.local.md, sole-personal clean (delete)
w "$S5/payload/.omakase/gates/shared.sh" 'PERSONAL'    # overlaps project (restore)
w "$S5/payload/.omakase/gates/ponly.sh" 'P ONLY'       # sole-personal clean (delete)
w "$S5/payload/.omakase/gates/pedit.sh" 'P EDIT'       # sole-personal, will be edited (keep)
src_commit "$S5" s5; S5A="$(resolvep "$S5")"
set_personal "$S5A"
R5="$TMP/l5repo"; newrepo "$R5"; OMK5="$(omk_of "$R5")"
gi "$R5" "$TMP/l5i.out" "$TMP/l5i.err" --source "$P5A"; r5i=$?
if ran_go "L5-init" "$TMP/l5i.err" "$NOTICE_INIT" && [ "$r5i" -eq 0 ]; then
  want "L5" "$TMP/l5i.out" "omakase: personal harness layered on top (${S5A}) — omakase personal off to remove it everywhere."
  ok "$(cat "$R5/.omakase/gates/shared.sh")" "PERSONAL" "L5: precheck personal won shared.sh"
  ok "$(cat "$R5/CLAUDE.local.md")" "personal doctrine" "L5: precheck CLAUDE.local.md present"
  printf 'MY EDIT\n' > "$R5/.omakase/gates/pedit.sh"   # user edits a sole-personal file before unlayering
  pers "$R5" "$TMP/l5o.out" "$TMP/l5o.err" off; r5o=$?
  is_exit "$r5o" 0 "L5: personal off exit 0"
  # stdout EXACTLY the two lines; stderr EXACTLY the keep-warn line
  { printf '%s\n' "$CLEARED_LINE"; printf '%s\n' "omakase: personal layer removed from this repo (restored 1 file(s), deleted 2)."; } > "$TMP/l5.exp.out"
  cmp -s "$TMP/l5o.out" "$TMP/l5.exp.out" && pass "L5: off stdout is exactly the cleared+removed lines" || { fail "L5: off stdout differs"; diff "$TMP/l5.exp.out" "$TMP/l5o.out" | sed 's/^/      /'; }
  printf '%s\n' "omakase: WARNING — '.omakase/gates/pedit.sh' was placed by your personal layer, has no lower-layer copy to restore, and differs from what omakase placed (a local edit?). Leaving it; delete it yourself if unwanted." > "$TMP/l5.exp.err"
  cmp -s "$TMP/l5o.err" "$TMP/l5.exp.err" && pass "L5: off stderr is exactly the edited-orphan keep-warn line" || { fail "L5: off stderr differs"; diff "$TMP/l5.exp.err" "$TMP/l5o.err" | sed 's/^/      /'; }
  # working tree
  ok "$(cat "$R5/.omakase/gates/shared.sh")" "PROJECT" "L5: overlap restored to the project copy byte-exact"
  ok "$(cat "$R5/.omakase/gates/pedit.sh")" "MY EDIT" "L5: edited sole-personal file kept"
  [ ! -e "$R5/.omakase/gates/ponly.sh" ] && pass "L5: clean sole-personal file deleted" || fail "L5: ponly.sh survived"
  [ ! -e "$R5/CLAUDE.local.md" ] && pass "L5: rerouted CLAUDE.local.md deleted" || fail "L5: CLAUDE.local.md survived"
  ok "$(cat "$R5/.claude/rules/r.md")" "proj rule" "L5: project-only file untouched"
  # placed.tsv rewritten to the project view (label + hash)
  ok "$(placed_col "$OMK5/placed.tsv" ".omakase/gates/shared.sh" 3)" "$P5A" "L5: shared.sh row rewritten to project label"
  ok "$(placed_col "$OMK5/placed.tsv" ".omakase/gates/shared.sh" 4)" "$(digest_file "$P5/payload/.omakase/gates/shared.sh")" "L5: shared.sh row hash = project bytes"
  for gone in CLAUDE.local.md .omakase/gates/ponly.sh .omakase/gates/pedit.sh; do
    placed_has "$OMK5/placed.tsv" "$gone" && fail "L5: placed.tsv still lists personal path $gone" || pass "L5: placed.tsv dropped $gone"
  done
  # state: personal row gone, layers/personal gone, exclude drops CLAUDE.local.md
  ok "$(src_field "$OMK5/sources.tsv" 1 1)" "project" "L5: sources.tsv left with one project row"
  awk -F'\t' 'END{exit (NR==1)?0:1}' "$OMK5/sources.tsv" && pass "L5: sources.tsv has exactly one row" || fail "L5: sources.tsv row count wrong"
  [ ! -e "$OMK5/layers/personal" ] && pass "L5: layers/personal removed" || fail "L5: layers/personal survived"
  hasnt "$(common_of "$R5")/info/exclude" "CLAUDE.local.md" "L5: exclude block dropped the CLAUDE.local.md entry"
  [ ! -e "$XDGC/omakase/personal" ] && pass "L5: global personal config cleared" || fail "L5: global config survived off"
fi
clear_personal

# ============================================================================
# L6 — GC8 refusal: a store-less v1-era $OMK that STILL records a personal row →
# `personal off` clears the global setting then refuses (exit 1, exact stderr).
# ============================================================================
echo "== L6: GC8 refuse-don't-guess (personal row, no layers/) =="
R6="$TMP/l6repo"; newrepo "$R6"; OMK6="$(omk_of "$R6")"
set_personal "you/harness"
mkdir -p "$OMK6"
printf '.omakase/gates/example.sh\tgate\tpayload\t%s\t1\n' "$(digest 'x')" > "$OMK6/placed.tsv"
printf 'personal\tyou/harness\t-\tabc123\t1\n' > "$OMK6/sources.tsv"   # a personal row, but NO layers/
pers "$R6" "$TMP/l6.out" "$TMP/l6.err" off; r6=$?
is_exit "$r6" 1 "L6: off refuses (exit 1)"
printf '%s\n' "$CLEARED_LINE" > "$TMP/l6.exp.out"
cmp -s "$TMP/l6.out" "$TMP/l6.exp.out" && pass "L6: stdout is exactly the cleared line (global clear still happened)" || { fail "L6: stdout differs"; diff "$TMP/l6.exp.out" "$TMP/l6.out" | sed 's/^/      /'; }
printf '%s\n' "$GC8_LINE" > "$TMP/l6.exp.err"
cmp -s "$TMP/l6.err" "$TMP/l6.exp.err" && pass "L6: stderr is exactly the GC8 refusal" || { fail "L6: stderr differs"; diff "$TMP/l6.exp.err" "$TMP/l6.err" | sed 's/^/      /'; }
[ ! -e "$XDGC/omakase/personal" ] && pass "L6: global config cleared before the refusal" || fail "L6: global config survived"
clear_personal

# ============================================================================
# L7 — lazy v1→v2 synthesis: a genuine v1-era $OMK (legacy init, no sources.tsv) +
# `omakase status` synthesizes sources.tsv (commit '-'); status stdout is invisible
# to the migration (two back-to-back runs byte-identical).
# ============================================================================
echo "== L7: lazy sources.tsv synthesis on first v2 status =="
P7="$TMP/l7src"; src_init "$P7"
printf 'name: proj7\n' > "$P7/omakase.manifest"
w "$P7/payload/.omakase/gates/g.sh" 'g7'
src_commit "$P7" p7; P7A="$(resolvep "$P7")"
clear_personal
R7="$TMP/l7repo"; newrepo "$R7"; OMK7="$(omk_of "$R7")"
leg_init "$R7" "$TMP/l7leg.out" "$TMP/l7leg.err" --source "$P7A"; r7leg=$?
is_exit "$r7leg" 0 "L7: legacy init built the v1-era repo"
[ ! -e "$OMK7/sources.tsv" ] && pass "L7: precondition — no sources.tsv after a v1 install" || fail "L7: v1 install already had sources.tsv"
[ ! -e "$OMK7/layers" ] && pass "L7: precondition — no layers/ after a v1 install" || fail "L7: v1 install already had layers/"
st "$R7" "$TMP/l7s1.out" "$TMP/l7s1.err"; r7s1=$?
if ran_go "L7-status1" "$TMP/l7s1.err" "$NOTICE_STATUS"; then
  is_exit "$r7s1" 0 "L7: first status exit 0"
  [ -e "$OMK7/sources.tsv" ] && pass "L7: first status synthesized sources.tsv" || fail "L7: sources.tsv not synthesized"
  ok "$(src_field "$OMK7/sources.tsv" 1 1)" "project" "L7: synthesized row layer = project"
  ok "$(src_field "$OMK7/sources.tsv" 1 2)" "$P7A"    "L7: synthesized row source = the remembered \$OMK/source"
  ok "$(src_field "$OMK7/sources.tsv" 1 4)" "-"        "L7: synthesized commit = '-' (never guessed)"
  hasnt "$TMP/l7s1.err" "WARNING —" "L7: synthesis is silent (no mixed-era warning on first run)"
fi
st "$R7" "$TMP/l7s2.out" "$TMP/l7s2.err"; r7s2=$?
if ran_go "L7-status2" "$TMP/l7s2.err" "$NOTICE_STATUS"; then
  cmp -s "$TMP/l7s1.out" "$TMP/l7s2.out" && pass "L7: status stdout byte-identical pre/post synthesis" || { fail "L7: status stdout changed after synthesis"; diff "$TMP/l7s1.out" "$TMP/l7s2.out" | sed 's/^/      /'; }
  cmp -s "$TMP/l7s1.err" "$TMP/l7s2.err" && pass "L7: status stderr byte-identical pre/post synthesis" || { fail "L7: status stderr changed"; diff "$TMP/l7s1.err" "$TMP/l7s2.err" | sed 's/^/      /'; }
fi

# ============================================================================
# L8 — mixed-era: a v2 install, then a "v1 tool" repoints $OMK/source out from under
# sources.tsv → status warns (axis 1); a bare init reheals (sources.tsv re-recorded).
# ============================================================================
echo "== L8: mixed-era detection on status + reheal on init =="
P8a="$TMP/l8src1"; src_init "$P8a"
printf 'name: proj8a\n' > "$P8a/omakase.manifest"
w "$P8a/payload/.omakase/gates/g.sh" 'g8a'
src_commit "$P8a" p8a; P8aA="$(resolvep "$P8a")"
P8b="$TMP/l8src2"; src_init "$P8b"
printf 'name: proj8b\n' > "$P8b/omakase.manifest"
w "$P8b/payload/.omakase/gates/g.sh" 'g8b'
src_commit "$P8b" p8b; P8bA="$(resolvep "$P8b")"
clear_personal
R8="$TMP/l8repo"; newrepo "$R8"; OMK8="$(omk_of "$R8")"
gi "$R8" "$TMP/l8i.out" "$TMP/l8i.err" --source "$P8aA"; r8i=$?
if ran_go "L8-init" "$TMP/l8i.err" "$NOTICE_INIT" && [ "$r8i" -eq 0 ]; then
  ok "$(src_field "$OMK8/sources.tsv" 1 2)" "$P8aA" "L8: install recorded src1 in sources.tsv"
  printf '%s\n' "$P8bA" > "$OMK8/source"   # the "v1 tool": repoint $OMK/source out from under sources.tsv
  st "$R8" "$TMP/l8s.out" "$TMP/l8s.err"; r8s=$?
  if ran_go "L8-status" "$TMP/l8s.err" "$NOTICE_STATUS"; then
    have "$TMP/l8s.err" "$MIXED_AXIS1" "L8: status warns mixed-era (axis 1 parenthetical)"
    have "$TMP/l8s.err" "run omakase init to reheal" "L8: status warning names the reheal path"
  fi
  gi "$R8" "$TMP/l8r.out" "$TMP/l8r.err"; r8r=$?
  if ran_go "L8-reheal" "$TMP/l8r.err" "$NOTICE_INIT"; then
    is_exit "$r8r" 0 "L8: reheal init exit 0"
    n="$(grep -cF "a pre-layers omakase run changed this repo's source" "$TMP/l8r.err")"
    ok "$n" "1" "L8: reheal init prints the mixed-era warning exactly once"
    ok "$(src_field "$OMK8/sources.tsv" 1 2)" "$P8bA" "L8: sources.tsv re-recorded to src2 (reheal)"
    c8="$(src_field "$OMK8/sources.tsv" 1 4)"; echo "$c8" | grep -qE '^[0-9a-f]{40}$' && pass "L8: reheal recorded a fresh resolved commit" || fail "L8: reheal commit = '$c8'"
  fi
fi

# ============================================================================
# L9 — base-only invariance (GC2): no source, no personal → NO sources.tsv, NO
# layers/, no new summary lines, col3 stays 'payload'; twin repos self-consistent.
# ============================================================================
echo "== L9: base-only install writes no layer artifacts (GC2) =="
BP9="$TMP/l9base"; rm -rf "$BP9"; mkdir -p "$BP9/.omakase/gates"
printf '#!/bin/sh\ntrue\n' > "$BP9/.omakase/gates/ex.sh"
clear_personal
A9="$TMP/l9A"; B9="$TMP/l9B"; newrepo "$A9"; newrepo "$B9"; OMKA9="$(omk_of "$A9")"; OMKB9="$(omk_of "$B9")"
EXTRA_ENV="OMAKASE_PAYLOAD=$BP9"
gi "$A9" "$TMP/l9A.out" "$TMP/l9A.err"; r9a=$?
gi "$B9" "$TMP/l9B.out" "$TMP/l9B.err"; r9b=$?
EXTRA_ENV=""
if ran_go "L9A" "$TMP/l9A.err" "$NOTICE_INIT" && ran_go "L9B" "$TMP/l9B.err" "$NOTICE_INIT"; then
  is_exit "$r9a" 0 "L9: twin A init succeeded"
  is_exit "$r9b" 0 "L9: twin B init succeeded"
  for pair in "A9:$OMKA9" "B9:$OMKB9"; do
    lbl="${pair%%:*}"; omk="${pair#*:}"
    [ ! -e "$omk/sources.tsv" ] && pass "L9($lbl): no sources.tsv" || fail "L9($lbl): sources.tsv written for a base-only install"
    [ ! -e "$omk/layers" ] && pass "L9($lbl): no layers/" || fail "L9($lbl): layers/ created for a base-only install"
    bad="$(awk -F'\t' '$3!="payload"{print; exit}' "$omk/placed.tsv")"
    [ -z "$bad" ] && pass "L9($lbl): every placed.tsv col3 stays 'payload'" || fail "L9($lbl): a col3 diverged: $bad"
  done
  hasnt "$TMP/l9A.out" "personal harness" "L9: base-only stdout carries no personal line"
  # self-consistency across twins (path-free artifacts compared byte-for-byte)
  cmp -s <(sort "$OMKA9/placed.tsv") <(sort "$OMKB9/placed.tsv") && pass "L9: twin placed.tsv identical (sorted)" || { fail "L9: twin placed.tsv differ"; diff <(sort "$OMKA9/placed.tsv") <(sort "$OMKB9/placed.tsv") | sed 's/^/      /'; }
  bl(){ awk '/# >>> omakase-harness >>>/{s=1;next} /# <<< omakase-harness <<</{s=0} s' "$1" | sort; }
  cmp -s <(bl "$(common_of "$A9")/info/exclude") <(bl "$(common_of "$B9")/info/exclude") && pass "L9: twin exclude blocks identical (sorted)" || fail "L9: twin exclude blocks differ"
fi

# ============================================================================
# L10 — remove tears a layered repo all the way back to its pre-init tree (bridge
# symlink + CLAUDE.local.md + $OMK all gone; roundtrip discipline).
# ============================================================================
echo "== L10: remove restores a layered repo to its pre-init tree =="
P10="$TMP/l10proj"; src_init "$P10"
printf 'name: proj10\n' > "$P10/omakase.manifest"
w "$P10/payload/AGENTS.md" 'proj doctrine'           # -> bridge CLAUDE.md symlink
w "$P10/payload/.omakase/gates/g.sh" 'g10'
src_commit "$P10" p10; P10A="$(resolvep "$P10")"
S10="$TMP/l10pers"; src_init "$S10"
printf 'name: personal10\n' > "$S10/omakase.manifest"
w "$S10/payload/AGENTS.md" 'personal doctrine'       # -> CLAUDE.local.md
src_commit "$S10" s10; S10A="$(resolvep "$S10")"
set_personal "$S10A"
R10="$TMP/l10repo"; newrepo "$R10"; OMK10="$(omk_of "$R10")"
printf 'my notes\n' > "$R10/notes-user.txt"   # an untracked user file the roundtrip must preserve
snap_tree "$R10" > "$TMP/l10.pretree"
gi "$R10" "$TMP/l10i.out" "$TMP/l10i.err" --source "$P10A"; r10i=$?
if ran_go "L10-init" "$TMP/l10i.err" "$NOTICE_INIT" && [ "$r10i" -eq 0 ]; then
  [ -L "$R10/CLAUDE.md" ] && pass "L10: bridge symlink placed by the layered install" || fail "L10: no bridge symlink to tear down"
  ok "$(cat "$R10/CLAUDE.local.md" 2>/dev/null)" "personal doctrine" "L10: CLAUDE.local.md placed by the layered install"
  rmv "$R10" "$TMP/l10r.out" "$TMP/l10r.err"; r10r=$?
  if ran_go "L10-remove" "$TMP/l10r.err" "$NOTICE_REMOVE"; then
    is_exit "$r10r" 0 "L10: remove exit 0"
    have "$TMP/l10r.out" "omakase: removed." "L10: remove prints its done line"
    [ ! -L "$R10/CLAUDE.md" ] && [ ! -e "$R10/CLAUDE.md" ] && pass "L10: bridge symlink gone" || fail "L10: CLAUDE.md survived remove"
    [ ! -e "$R10/CLAUDE.local.md" ] && pass "L10: CLAUDE.local.md gone" || fail "L10: CLAUDE.local.md survived remove"
    [ ! -e "$OMK10" ] && pass "L10: \$OMK deleted" || fail "L10: \$OMK survived remove"
    snap_tree "$R10" > "$TMP/l10.posttree"
    cmp -s "$TMP/l10.pretree" "$TMP/l10.posttree" && pass "L10: repo byte-restored to its pre-init tree" || { fail "L10: tree not restored"; diff "$TMP/l10.pretree" "$TMP/l10.posttree" | sed 's/^/      /'; }
  fi
fi
clear_personal

rm -rf "$TMP"
echo ""
[ "$FAILED" -eq 0 ] && echo "ALL PASS" || { echo "FAILURES PRESENT"; exit 1; }
