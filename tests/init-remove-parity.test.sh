#!/usr/bin/env bash
# tests/init-remove-parity.test.sh — the Phase-2 RELEASE GATE for the Go port of
# omakase init + omakase remove (the shim cutover).
#
# The v1 bash bodies are preserved verbatim at bin/legacy/init.sh and
# bin/legacy/remove.sh (the parity oracles). The new bin/init.sh / bin/remove.sh are
# thin shims that rebuild and exec the Go binary (dist/omakase), falling back to the
# legacy bash only when the binary can't be resolved. Unlike status (read-only), init
# and remove MUTATE the repo, so we cannot run both impls in the same repo: we build
# TWIN fixtures (repo A + repo B, constructed identically), run the LEGACY body in A
# (`bash bin/legacy/init.sh`) and the SHIM => Go binary in B, and compare.
#
# The one sanctioned divergence is WALK ORDER: find(1) and Go's directory walk visit
# a payload's files in different orders, so any per-file list (init's placed-file
# stdout, placed.tsv rows, the exclude/wtinc entries) is compared LINE-SORTED; scalar
# and refusal outputs are compared RAW. All output is PATH-NORMALIZED first: the only
# absolute path that legitimately differs between twins is each repo's own root (it
# appears in lefthook's "Added config:" banner and in omakase's clobbered/ backup
# lines), so we rewrite it to @R@ before diffing — any OTHER byte difference still
# shows. State is compared structurally: payload-snapshot/ and the hooks dir via
# `diff -r` (their contents carry no repo-absolute paths — the hook stubs resolve the
# git dir at RUNTIME). Exit codes must always match. The Go side's NOTICE-absent guard
# (the shim's fallback line must NOT appear) proves the Go binary actually ran on every
# Go-side invocation; F1 is the one scenario that asserts the notice IS present.
#
# Skip-with-notice (exit 0) ONLY when dist/omakase is absent AND go is not on PATH.
# Scenarios that install hooks SKIP as a group when lefthook is unresolvable; the two
# pure-validation scenarios that refuse BEFORE lefthook resolution (I8 wiring guard,
# I10 source errors) run regardless. bash 3.2-safe throughout.
set -u

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="$HERE/.."
LEG_INIT="$ROOT/bin/legacy/init.sh"
LEG_REMOVE="$ROOT/bin/legacy/remove.sh"
SHIM_INIT="$ROOT/bin/init.sh"
SHIM_REMOVE="$ROOT/bin/remove.sh"
PAY="$ROOT/payload"
BIN="$ROOT/dist/omakase"
LEFTHOOK="${LEFTHOOK_BIN:-$(command -v lefthook || true)}"
TMP="${TMPDIR:-/tmp}/init-remove-parity.$$"
NOTICE_INIT='omakase: Go binary not present — running the bundled v1 init script'
NOTICE_REMOVE='omakase: Go binary not present — running the bundled v1 remove script'
BEGIN='# >>> omakase-harness >>>'
END='# <<< omakase-harness <<<'
FAILED=0
pass(){ echo "  PASS: $1"; }
fail(){ echo "  FAIL: $1"; FAILED=1; }
skip(){ echo "  SKIP: $1"; }

[ -n "$LEFTHOOK" ] && export PATH="$(dirname "$LEFTHOOK"):$PATH"
HAVE_LH=0; { [ -n "$LEFTHOOK" ] && [ -x "$LEFTHOOK" ]; } && HAVE_LH=1

# --- build/skip gate: only skip when there is NO binary AND NO go to build one ---
if [ ! -x "$BIN" ] && ! command -v go >/dev/null 2>&1; then
  echo "SKIP: dist/omakase absent and go not on PATH — the parity gate cannot run"
  exit 0
fi
if command -v go >/dev/null 2>&1; then
  ( cd "$ROOT" && CGO_ENABLED=0 go build -o dist/omakase ./cmd/omakase ) \
    || { echo "  FAIL: go build failed — cannot run the parity gate"; exit 1; }
fi

mkdir -p "$TMP"
FAKEHOME="$TMP/home"; mkdir -p "$FAKEHOME"
newrepo(){ rm -rf "$1"; mkdir -p "$1"; ( cd "$1" && git init -q && git config user.email t@t && git config user.name t && git config commit.gpgsign false && git commit -q --allow-empty -m init ); }
resolvep(){ ( cd "$1" && pwd -P ); }

# --- run helpers -------------------------------------------------------------
# EXTRA_ENV (reset per scenario) carries scenario-specific NAME=VALUE pairs
# (OMAKASE_PAYLOAD, XDG_CACHE_HOME, OMAKASE_CUTOVER_CONFIRM). Paths under $TMP hold
# no spaces, so the unquoted expansion is safe and each token reaches env as one
# NAME=VALUE. The legacy side runs the frozen bash body; the Go side pins
# OMAKASE_BIN to the prebuilt binary (the shim's sanctioned test override) so it
# execs Go directly instead of rebuilding per call under a fake HOME.
EXTRA_ENV=""
leg_init(){ local cwd="$1" out="$2" err="$3"; shift 3
  ( cd "$cwd" && env -u OMAKASE_BIN -u OMAKASE_ICON -u NO_COLOR HOME="$FAKEHOME" LEFTHOOK_BIN="$LEFTHOOK" $EXTRA_ENV bash "$LEG_INIT" "$@" ) >"$out" 2>"$err"; }
go_init(){ local cwd="$1" out="$2" err="$3"; shift 3
  ( cd "$cwd" && env -u OMAKASE_ICON -u NO_COLOR HOME="$FAKEHOME" LEFTHOOK_BIN="$LEFTHOOK" OMAKASE_BIN="$BIN" $EXTRA_ENV bash "$SHIM_INIT" "$@" ) >"$out" 2>"$err"; }
leg_remove(){ local cwd="$1" out="$2" err="$3"; shift 3
  ( cd "$cwd" && env -u OMAKASE_BIN -u OMAKASE_ICON -u NO_COLOR HOME="$FAKEHOME" LEFTHOOK_BIN="$LEFTHOOK" $EXTRA_ENV bash "$LEG_REMOVE" "$@" ) >"$out" 2>"$err"; }
go_remove(){ local cwd="$1" out="$2" err="$3"; shift 3
  ( cd "$cwd" && env -u OMAKASE_ICON -u NO_COLOR HOME="$FAKEHOME" LEFTHOOK_BIN="$LEFTHOOK" OMAKASE_BIN="$BIN" $EXTRA_ENV bash "$SHIM_REMOVE" "$@" ) >"$out" 2>"$err"; }

# --- comparison helpers ------------------------------------------------------
# norm <file> <repo-given-path>: rewrite BOTH the given and the physically-resolved
# repo path (macOS /var vs /private/var) to @R@ — the sole absolute path that varies
# between twins. Any other differing byte survives the rewrite and fails the diff.
norm(){ local rp; rp="$(resolvep "$2" 2>/dev/null || echo "$2")"; sed -e "s|$rp|@R@|g" -e "s|$2|@R@|g" "$1"; }

# guard: the Go side must not have fallen back to legacy bash (else the diff would be
# bash-vs-bash — a false green). Called on every Go-side stderr except F1.
ran_go(){ local label="$1" errf="$2" notice="$3"
  if grep -qF "$notice" "$errf"; then fail "$label: shim fell back to legacy bash (the Go binary did not run)"; sed 's/^/      /' "$errf"; return 1; fi
  return 0; }

# cmp_raw / cmp_sorted <label> <what> <legf> <gof> <repoLeg> <repoGo>: normalized
# byte compare, RAW or as a line-sorted set.
cmp_raw(){ local l="$1" w="$2"
  if diff <(norm "$3" "$5") <(norm "$4" "$6") >"$TMP/d" 2>&1; then pass "$l: $w byte-identical (path-normalized)"
  else fail "$l: $w DIFFERS"; sed 's/^/      /' "$TMP/d"; fi; }
cmp_sorted(){ local l="$1" w="$2"
  if diff <(norm "$3" "$5" | sort) <(norm "$4" "$6" | sort) >"$TMP/d" 2>&1; then pass "$l: $w identical as a sorted set"
  else fail "$l: $w DIFFERS (sorted)"; sed 's/^/      /' "$TMP/d"; fi; }

eq_exit(){ [ "$2" -eq "$3" ] && pass "$1: exit codes equal ($2)" || fail "$1: exit codes differ (legacy=$2 go=$3)"; }

# want <label> <file> <marker...>: fixed-string containment against the LEGACY capture
# (the reference), pinning that the fixture exercised the intended state — an
# anti-fixture-rot guard modelled on status-parity's expect_marker. No markers => no-op.
want(){ local l="$1" f="$2"; shift 2; [ "$#" -eq 0 ] && return 0
  local p miss=""
  for p in "$@"; do grep -qF -- "$p" "$f" 2>/dev/null || miss="${miss:+$miss, }'$p'"; done
  [ -z "$miss" ] && pass "$l: legacy output carries the expected marker(s)" \
                 || fail "$l: legacy output MISSING marker(s) $miss — fixture may not exercise the intended state"; }

# state comparisons between twin repos A and B (relative, path-free contents).
common_of(){ ( cd "$1" && cd "$(git rev-parse --git-common-dir)" && pwd ); }
cmp_placed_sorted(){ local l="$1" a="$2" b="$3"
  if diff <(sort "$a") <(sort "$b") >"$TMP/d" 2>&1; then pass "$l: placed.tsv identical as a sorted set"
  else fail "$l: placed.tsv DIFFERS (sorted)"; sed 's/^/      /' "$TMP/d"; fi; }
cmp_tree(){ local l="$1" a="$2" b="$3"   # diff -r of two dirs
  if diff -r "$a" "$b" >"$TMP/d" 2>&1; then pass "$l: $a mirrors $b (diff -r)"
  else fail "$l: state differs (diff -r)"; sed 's/^/      /' "$TMP/d"; fi; }
block_entries(){ awk -v b="$BEGIN" -v e="$END" '$0==b{s=1;next} $0==e{s=0} s' "$1" 2>/dev/null; }
cmp_block(){ local l="$1" fa="$2" fb="$3"
  if [ "$(grep -cxF "$BEGIN" "$fa" 2>/dev/null)" = 1 ] && [ "$(grep -cxF "$END" "$fa" 2>/dev/null)" = 1 ] \
  && [ "$(grep -cxF "$BEGIN" "$fb" 2>/dev/null)" = 1 ] && [ "$(grep -cxF "$END" "$fb" 2>/dev/null)" = 1 ]; then
    pass "$l: both sides carry exactly one byte-exact marker pair"
  else fail "$l: marker pair count wrong (want exactly one BEGIN + one END on both sides)"; fi
  if diff <(block_entries "$fa" | sort) <(block_entries "$fb" | sort) >"$TMP/d" 2>&1; then pass "$l: block entries identical as a sorted set"
  else fail "$l: block entries DIFFER"; sed 's/^/      /' "$TMP/d"; fi; }
snap_tree(){ ( cd "$1" && find . -mindepth 1 -name .git -prune -o -print | sed 's|^\./||' | sort ); }

# Build the rich I1 payload: nested dirs, a CLAUDE.md -> AGENTS.md symlink, a .sh
# (chmod-worthy), and a .github/ file (the shared-topdir, file-by-file exclude path).
build_rich_payload(){ local p="$1"; rm -rf "$p"
  mkdir -p "$p/.claude/rules" "$p/.omakase/bin" "$p/.github/workflows"
  printf 'agent doctrine\n'   > "$p/AGENTS.md"
  ( cd "$p" && ln -s AGENTS.md CLAUDE.md )
  printf 'rule a\n'           > "$p/.claude/rules/a.md"
  printf 'rule b\n'           > "$p/.claude/rules/b.md"
  printf '#!/bin/sh\necho hi\n' > "$p/.omakase/bin/tool.sh"
  printf 'name: CI\n'         > "$p/.github/workflows/ci.yml"; }

# ============================================================================
# I8 + I10 run BEFORE any lefthook gating — they refuse in the source/wiring
# validation that precedes lefthook resolution, so they exercise the Go port even on
# a host without lefthook.
# ============================================================================

# ---------- I8: wiring-guard refusal (payload names a script it doesn't ship) ----------
echo "== I8: wiring-guard refusal =="
PAY8="$TMP/pay8"; rm -rf "$PAY8"; mkdir -p "$PAY8/.claude/rules"
printf 'x\n' > "$PAY8/.claude/rules/x.md"
printf 'pre-commit:\n  jobs:\n    - name: ghost\n      run: bash .omakase/gates/nonexistent.sh\n' > "$PAY8/lefthook-local.yml"
A8="$TMP/i8A"; B8="$TMP/i8B"; newrepo "$A8"; newrepo "$B8"
EXTRA_ENV="OMAKASE_PAYLOAD=$PAY8"
leg_init "$A8" "$TMP/i8A.out" "$TMP/i8A.err"; r8a=$?
go_init  "$B8" "$TMP/i8B.out" "$TMP/i8B.err"; r8b=$?
EXTRA_ENV=""
if ran_go "I8" "$TMP/i8B.err" "$NOTICE_INIT"; then
  want "I8" "$TMP/i8A.err" "hook wiring references script(s)" "nonexistent.sh"
  cmp_raw "I8" "stdout" "$TMP/i8A.out" "$TMP/i8B.out" "$A8" "$B8"
  cmp_raw "I8" "stderr" "$TMP/i8A.err" "$TMP/i8B.err" "$A8" "$B8"
  eq_exit "I8" "$r8a" "$r8b"
  [ "$r8a" -eq 1 ] && pass "I8: legacy refused (exit 1)" || fail "I8: legacy exit $r8a (want 1)"
  { [ ! -f "$(common_of "$A8")/omakase/placed.tsv" ] && [ ! -f "$(common_of "$B8")/omakase/placed.tsv" ]; } \
    && pass "I8: nothing placed on either side (no placed.tsv)" || fail "I8: something was placed despite the refusal"
fi

# ---------- I10: source validation errors (no manifest; no name; empty payload) ----------
echo "== I10: source validation errors =="
# Three malformed local-path sources; each must fail-closed (exit 1) in fetch_source,
# before lefthook. The error names the SOURCE path (shared by both twins), so raw
# byte parity holds without normalization of the target repo.
i10_case(){ # $1 label  $2 srcdir  $3 marker
  local label="$1" src="$2" marker="$3"
  local A="$TMP/${label}A" B="$TMP/${label}B"; newrepo "$A"; newrepo "$B"
  EXTRA_ENV="XDG_CACHE_HOME=$TMP/i10cache"
  leg_init "$A" "$TMP/${label}A.out" "$TMP/${label}A.err" --source "$src"; local ra=$?
  go_init  "$B" "$TMP/${label}B.out" "$TMP/${label}B.err" --source "$src"; local rb=$?
  EXTRA_ENV=""
  ran_go "$label" "$TMP/${label}B.err" "$NOTICE_INIT" || return
  want "$label" "$TMP/${label}A.err" "$marker"
  cmp_raw "$label" "stderr" "$TMP/${label}A.err" "$TMP/${label}B.err" "$A" "$B"
  eq_exit "$label" "$ra" "$rb"
  [ "$ra" -eq 1 ] && pass "$label: legacy refused (exit 1)" || fail "$label: legacy exit $ra (want 1)"; }
# (a) no manifest
S10a="$TMP/s10a"; rm -rf "$S10a"; mkdir -p "$S10a/payload"; printf 'x\n' > "$S10a/payload/f.md"
( cd "$S10a" && git init -q && git config user.email t@t && git config user.name t && git config commit.gpgsign false && git add -A && git commit -q -m s )
i10_case "I10a" "$(resolvep "$S10a")" "has no omakase.manifest at its root"
# (b) manifest without a name:
S10b="$TMP/s10b"; rm -rf "$S10b"; mkdir -p "$S10b/payload"; printf 'x\n' > "$S10b/payload/f.md"
printf 'version: 0.1.0\n' > "$S10b/omakase.manifest"
( cd "$S10b" && git init -q && git config user.email t@t && git config user.name t && git config commit.gpgsign false && git add -A && git commit -q -m s )
i10_case "I10b" "$(resolvep "$S10b")" "manifest is missing the required 'name:' line"
# (c) empty payload
S10c="$TMP/s10c"; rm -rf "$S10c"; mkdir -p "$S10c/payload"
printf 'name: empty-fixture\n' > "$S10c/omakase.manifest"
( cd "$S10c" && git init -q && git config user.email t@t && git config user.name t && git config commit.gpgsign false && git add -A && git commit -q -m s )
i10_case "I10c" "$(resolvep "$S10c")" "has no non-empty payload/ tree"

# ============================================================================
# Install-based scenarios need a real lefthook (init installs hooks).
# ============================================================================
if [ "$HAVE_LH" -eq 0 ]; then
  skip "I1-I7, I9, R1-R3, F1 need lefthook (install); LEFTHOOK_BIN unset and lefthook not on PATH"
  rm -rf "$TMP"; echo ""; [ "$FAILED" -eq 0 ] && echo "ALL PASS" || { echo "FAILURES PRESENT"; exit 1; }
  exit 0
fi

# ---------- I1: fresh init, multi-entry payload (nested, symlink, .sh, .github) ----------
echo "== I1: fresh init, multi-entry payload =="
PAY1="$TMP/pay1"; build_rich_payload "$PAY1"
A1="$TMP/i1A"; B1="$TMP/i1B"; newrepo "$A1"; newrepo "$B1"
printf 'my untracked notes\n' > "$A1/notes-user.txt"   # a user file the round trip (R1) must preserve
printf 'my untracked notes\n' > "$B1/notes-user.txt"
PRE1A="$TMP/i1A.pretree"; PRE1B="$TMP/i1B.pretree"
snap_tree "$A1" > "$PRE1A"; snap_tree "$B1" > "$PRE1B"
EXTRA_ENV="OMAKASE_PAYLOAD=$PAY1"
leg_init "$A1" "$TMP/i1A.out" "$TMP/i1A.err"; r1a=$?
go_init  "$B1" "$TMP/i1B.out" "$TMP/i1B.err"; r1b=$?
EXTRA_ENV=""
if ran_go "I1" "$TMP/i1B.err" "$NOTICE_INIT"; then
  CA1="$(common_of "$A1")"; CB1="$(common_of "$B1")"
  want "I1" "$TMP/i1A.out" "placed" "+ CLAUDE.md" "+ .github/workflows/ci.yml"
  cmp_sorted "I1" "stdout" "$TMP/i1A.out" "$TMP/i1B.out" "$A1" "$B1"
  cmp_sorted "I1" "stderr" "$TMP/i1A.err" "$TMP/i1B.err" "$A1" "$B1"
  eq_exit "I1" "$r1a" "$r1b"
  [ -L "$A1/CLAUDE.md" ] && [ -L "$B1/CLAUDE.md" ] && pass "I1: symlink placed as a symlink on both sides" || fail "I1: CLAUDE.md not a symlink on both sides"
  cmp_placed_sorted "I1" "$CA1/omakase/placed.tsv" "$CB1/omakase/placed.tsv"
  cmp_tree "I1 snapshot" "$CA1/omakase/payload-snapshot" "$CB1/omakase/payload-snapshot"
  cmp_tree "I1 hooks" "$CA1/hooks" "$CB1/hooks"
  cmp_block "I1 exclude" "$CA1/info/exclude" "$CB1/info/exclude"
  cmp_block "I1 wtinc" "$A1/.worktreeinclude" "$B1/.worktreeinclude"
fi

# ---------- I2: re-init over I1 (idempotent) — same twins, compare the SECOND init ----------
echo "== I2: re-init (idempotent) =="
EXTRA_ENV="OMAKASE_PAYLOAD=$PAY1"
leg_init "$A1" "$TMP/i2A.out" "$TMP/i2A.err"; r2a=$?
go_init  "$B1" "$TMP/i2B.out" "$TMP/i2B.err"; r2b=$?
EXTRA_ENV=""
if ran_go "I2" "$TMP/i2B.err" "$NOTICE_INIT"; then
  CA1="$(common_of "$A1")"; CB1="$(common_of "$B1")"
  cmp_sorted "I2" "stdout" "$TMP/i2A.out" "$TMP/i2B.out" "$A1" "$B1"
  cmp_sorted "I2" "stderr" "$TMP/i2A.err" "$TMP/i2B.err" "$A1" "$B1"
  eq_exit "I2" "$r2a" "$r2b"
  cmp_placed_sorted "I2" "$CA1/omakase/placed.tsv" "$CB1/omakase/placed.tsv"
  cmp_tree "I2 snapshot" "$CA1/omakase/payload-snapshot" "$CB1/omakase/payload-snapshot"
  cmp_block "I2 exclude" "$CA1/info/exclude" "$CB1/info/exclude"
fi

# ---------- R1: remove after I1 — roundtrip to the pre-init tree ----------
echo "== R1: remove after I1 (roundtrip) =="
leg_remove "$A1" "$TMP/r1A.out" "$TMP/r1A.err"; rr1a=$?
go_remove  "$B1" "$TMP/r1B.out" "$TMP/r1B.err"; rr1b=$?
if ran_go "R1" "$TMP/r1B.err" "$NOTICE_REMOVE"; then
  want "R1" "$TMP/r1A.out" "omakase: removed."
  cmp_raw "R1" "stdout" "$TMP/r1A.out" "$TMP/r1B.out" "$A1" "$B1"
  cmp_raw "R1" "stderr" "$TMP/r1A.err" "$TMP/r1B.err" "$A1" "$B1"
  eq_exit "R1" "$rr1a" "$rr1b"
  snap_tree "$A1" > "$TMP/r1A.tree"; snap_tree "$B1" > "$TMP/r1B.tree"
  cmp -s "$PRE1A" "$TMP/r1A.tree" && pass "R1: legacy repo restored to its pre-init tree" || { fail "R1: legacy tree not restored"; diff "$PRE1A" "$TMP/r1A.tree" | sed 's/^/      /'; }
  cmp -s "$PRE1B" "$TMP/r1B.tree" && pass "R1: Go repo restored to its pre-init tree" || { fail "R1: Go tree not restored"; diff "$PRE1B" "$TMP/r1B.tree" | sed 's/^/      /'; }
  cmp -s "$TMP/r1A.tree" "$TMP/r1B.tree" && pass "R1: legacy and Go post-remove trees identical" || fail "R1: post-remove trees differ"
  { [ ! -e "$(common_of "$A1")/omakase" ] && [ ! -e "$(common_of "$B1")/omakase" ]; } && pass "R1: \$OMK gone on both sides" || fail "R1: \$OMK survived remove"
fi

# ---------- R2: a second remove on the clean repo — no-op, exit 0 ----------
echo "== R2: second remove (no-op) =="
leg_remove "$A1" "$TMP/r2A.out" "$TMP/r2A.err"; rr2a=$?
go_remove  "$B1" "$TMP/r2B.out" "$TMP/r2B.err"; rr2b=$?
if ran_go "R2" "$TMP/r2B.err" "$NOTICE_REMOVE"; then
  want "R2" "$TMP/r2A.err" "nothing installed here; nothing to remove."
  cmp_raw "R2" "stdout" "$TMP/r2A.out" "$TMP/r2B.out" "$A1" "$B1"
  cmp_raw "R2" "stderr" "$TMP/r2A.err" "$TMP/r2B.err" "$A1" "$B1"
  eq_exit "R2" "$rr2a" "$rr2b"
  [ "$rr2a" -eq 0 ] && [ "$rr2b" -eq 0 ] && pass "R2: both sides exit 0 on the clean repo" || fail "R2: non-zero exit (legacy=$rr2a go=$rr2b)"
fi

# ---------- I3: payload path already tracked by the repo (single skip line) ----------
echo "== I3: tracked payload path — single skip =="
PAY3="$TMP/pay3"; rm -rf "$PAY3"; mkdir -p "$PAY3/.claude/rules"; printf 'team\n' > "$PAY3/.claude/rules/team.md"
A3="$TMP/i3A"; B3="$TMP/i3B"
for R in "$A3" "$B3"; do newrepo "$R"; mkdir -p "$R/.claude/rules"; printf 'team\n' > "$R/.claude/rules/team.md"; ( cd "$R" && git add .claude/rules/team.md && git commit -qm tracked ); done
EXTRA_ENV="OMAKASE_PAYLOAD=$PAY3"
leg_init "$A3" "$TMP/i3A.out" "$TMP/i3A.err"; r3a=$?
go_init  "$B3" "$TMP/i3B.out" "$TMP/i3B.err"; r3b=$?
EXTRA_ENV=""
if ran_go "I3" "$TMP/i3B.err" "$NOTICE_INIT"; then
  want "I3" "$TMP/i3A.err" "SKIP (already tracked) .claude/rules/team.md"
  cmp_raw "I3" "stdout" "$TMP/i3A.out" "$TMP/i3B.out" "$A3" "$B3"
  cmp_raw "I3" "stderr" "$TMP/i3A.err" "$TMP/i3B.err" "$A3" "$B3"
  eq_exit "I3" "$r3a" "$r3b"
fi

# ---------- I4: untracked DRIFTED file at a payload path (overwrite + backup) ----------
echo "== I4: untracked drifted file — overwrite, backup to clobbered/ =="
PAY4="$TMP/pay4"; rm -rf "$PAY4"; mkdir -p "$PAY4/.claude/rules"; printf 'canonical\n' > "$PAY4/.claude/rules/note.md"
A4="$TMP/i4A"; B4="$TMP/i4B"
for R in "$A4" "$B4"; do newrepo "$R"; mkdir -p "$R/.claude/rules"; printf 'my own edit\n' > "$R/.claude/rules/note.md"; done  # untracked, differs from payload
EXTRA_ENV="OMAKASE_PAYLOAD=$PAY4"
leg_init "$A4" "$TMP/i4A.out" "$TMP/i4A.err"; r4a=$?
go_init  "$B4" "$TMP/i4B.out" "$TMP/i4B.err"; r4b=$?
EXTRA_ENV=""
if ran_go "I4" "$TMP/i4B.err" "$NOTICE_INIT"; then
  want "I4" "$TMP/i4A.err" "prior copy preserved at"
  cmp_raw "I4" "stdout" "$TMP/i4A.out" "$TMP/i4B.out" "$A4" "$B4"
  cmp_raw "I4" "stderr" "$TMP/i4A.err" "$TMP/i4B.err" "$A4" "$B4"
  eq_exit "I4" "$r4a" "$r4b"
  CB4="$(common_of "$B4")"; CA4="$(common_of "$A4")"
  cmp -s "$CA4/omakase/clobbered/.claude/rules/note.md" "$CB4/omakase/clobbered/.claude/rules/note.md" \
    && pass "I4: clobbered/ backup byte-identical on both sides" || fail "I4: clobbered/ backup differs"
  printf 'my own edit\n' | cmp -s - "$CB4/omakase/clobbered/.claude/rules/note.md" \
    && pass "I4: backup holds the pre-overwrite bytes ('my own edit')" || fail "I4: backup does not hold the pre-overwrite bytes"
fi

# ---------- I5: re-init after the payload shrinks (orphan sweep + edited-orphan keep) ----------
echo "== I5: payload shrink — sweep clean orphan, keep edited orphan =="
PAY5A="$TMP/pay5-big"; rm -rf "$PAY5A"; mkdir -p "$PAY5A/.claude/rules"
for f in a b c; do printf 'rule %s\n' "$f" > "$PAY5A/.claude/rules/$f.md"; done
PAY5B="$TMP/pay5-small"; rm -rf "$PAY5B"; mkdir -p "$PAY5B/.claude/rules"; printf 'rule a\n' > "$PAY5B/.claude/rules/a.md"
A5="$TMP/i5A"; B5="$TMP/i5B"; newrepo "$A5"; newrepo "$B5"
EXTRA_ENV="OMAKASE_PAYLOAD=$PAY5A"
leg_init "$A5" /dev/null /dev/null; go_init "$B5" /dev/null /dev/null
EXTRA_ENV=""
for R in "$A5" "$B5"; do printf 'local edit\n' >> "$R/.claude/rules/b.md"; done   # b.md edited => must be KEPT; c.md untouched => swept
EXTRA_ENV="OMAKASE_PAYLOAD=$PAY5B"
leg_init "$A5" "$TMP/i5A.out" "$TMP/i5A.err"; r5a=$?
go_init  "$B5" "$TMP/i5B.out" "$TMP/i5B.err"; r5b=$?
EXTRA_ENV=""
if ran_go "I5" "$TMP/i5B.err" "$NOTICE_INIT"; then
  want "I5" "$TMP/i5A.out" "removed (placed by a prior init, no longer in the payload): .claude/rules/c.md"
  want "I5" "$TMP/i5A.err" "differs from what init placed"
  cmp_sorted "I5" "stdout" "$TMP/i5A.out" "$TMP/i5B.out" "$A5" "$B5"
  cmp_sorted "I5" "stderr" "$TMP/i5A.err" "$TMP/i5B.err" "$A5" "$B5"
  eq_exit "I5" "$r5a" "$r5b"
  { [ ! -e "$A5/.claude/rules/c.md" ] && [ ! -e "$B5/.claude/rules/c.md" ]; } && pass "I5: untouched orphan (c.md) swept on both sides" || fail "I5: c.md not swept"
  { [ -e "$A5/.claude/rules/b.md" ] && [ -e "$B5/.claude/rules/b.md" ]; } && pass "I5: edited orphan (b.md) kept on both sides" || fail "I5: edited b.md was destroyed"
fi

# ---------- I6: --cut-over unconfirmed (refuse) then confirmed (stage deletion) ----------
echo "== I6: --cut-over unconfirmed then confirmed =="
PAY6="$TMP/pay6"; rm -rf "$PAY6"; mkdir -p "$PAY6/.claude/rules"; printf 'shared\n' > "$PAY6/.claude/rules/shared.md"
A6="$TMP/i6A"; B6="$TMP/i6B"
for R in "$A6" "$B6"; do newrepo "$R"; mkdir -p "$R/.claude/rules"; printf 'shared\n' > "$R/.claude/rules/shared.md"; ( cd "$R" && git add .claude/rules/shared.md && git commit -qm shared ); done
# unconfirmed => refuse, exit 1
EXTRA_ENV="OMAKASE_PAYLOAD=$PAY6"
leg_init "$A6" "$TMP/i6uA.out" "$TMP/i6uA.err" --cut-over; r6ua=$?
go_init  "$B6" "$TMP/i6uB.out" "$TMP/i6uB.err" --cut-over; r6ub=$?
EXTRA_ENV=""
if ran_go "I6-unconfirmed" "$TMP/i6uB.err" "$NOTICE_INIT"; then
  want "I6-unconfirmed" "$TMP/i6uA.err" "REFUSING cut-over without confirmation"
  cmp_raw "I6-unconfirmed" "stdout" "$TMP/i6uA.out" "$TMP/i6uB.out" "$A6" "$B6"
  cmp_raw "I6-unconfirmed" "stderr" "$TMP/i6uA.err" "$TMP/i6uB.err" "$A6" "$B6"
  eq_exit "I6-unconfirmed" "$r6ua" "$r6ub"
  [ "$r6ua" -eq 1 ] && pass "I6: unconfirmed cut-over refused (exit 1)" || fail "I6: unconfirmed exit $r6ua (want 1)"
fi
# confirmed => stage the deletion, exit 0
EXTRA_ENV="OMAKASE_PAYLOAD=$PAY6 OMAKASE_CUTOVER_CONFIRM=1"
leg_init "$A6" "$TMP/i6cA.out" "$TMP/i6cA.err" --cut-over; r6ca=$?
go_init  "$B6" "$TMP/i6cB.out" "$TMP/i6cB.err" --cut-over; r6cb=$?
EXTRA_ENV=""
if ran_go "I6-confirmed" "$TMP/i6cB.err" "$NOTICE_INIT"; then
  want "I6-confirmed" "$TMP/i6cA.out" "cut-over staged"
  cmp_raw "I6-confirmed" "stdout" "$TMP/i6cA.out" "$TMP/i6cB.out" "$A6" "$B6"
  cmp_raw "I6-confirmed" "stderr" "$TMP/i6cA.err" "$TMP/i6cB.err" "$A6" "$B6"
  eq_exit "I6-confirmed" "$r6ca" "$r6cb"
  ( cd "$A6" && git diff --cached --name-status ) > "$TMP/i6A.staged"
  ( cd "$B6" && git diff --cached --name-status ) > "$TMP/i6B.staged"
  cmp -s "$TMP/i6A.staged" "$TMP/i6B.staged" && pass "I6: staged index (git diff --cached --name-status) identical" || { fail "I6: staged index differs"; diff "$TMP/i6A.staged" "$TMP/i6B.staged" | sed 's/^/      /'; }
fi

# ---------- I7: incumbent-manager refusals + redundant hooksPath cleared ----------
echo "== I7: incumbent refusals + redundant hooksPath =="
PAY7="$TMP/pay7"; rm -rf "$PAY7"; mkdir -p "$PAY7/.claude/rules"; printf 'r\n' > "$PAY7/.claude/rules/r.md"
i7_refuse(){ # $1 label  $2 setup-fn  $3 marker
  local label="$1" setup="$2" marker="$3"
  local A="$TMP/${label}A" B="$TMP/${label}B"; newrepo "$A"; newrepo "$B"
  "$setup" "$A"; "$setup" "$B"
  EXTRA_ENV="OMAKASE_PAYLOAD=$PAY7"
  leg_init "$A" "$TMP/${label}A.out" "$TMP/${label}A.err"; local ra=$?
  go_init  "$B" "$TMP/${label}B.out" "$TMP/${label}B.err"; local rb=$?
  EXTRA_ENV=""
  ran_go "$label" "$TMP/${label}B.err" "$NOTICE_INIT" || return
  want "$label" "$TMP/${label}A.err" "REFUSING to install" "$marker"
  cmp_raw "$label" "stdout" "$TMP/${label}A.out" "$TMP/${label}B.out" "$A" "$B"
  cmp_raw "$label" "stderr" "$TMP/${label}A.err" "$TMP/${label}B.err" "$A" "$B"
  eq_exit "$label" "$ra" "$rb"
  [ "$ra" -eq 1 ] && pass "$label: legacy refused (exit 1)" || fail "$label: legacy exit $ra (want 1)"; }
setup_husky(){ mkdir -p "$1/.husky/_"; printf 'echo hi\n' > "$1/.husky/pre-commit"; }   # untracked .husky
setup_prepare(){ printf '{\n  "scripts": { "prepare": "husky install" }\n}\n' > "$1/package.json"; }
setup_foreign_hook(){ local c; c="$(common_of "$1")"; mkdir -p "$c/hooks"; printf '#!/bin/sh\necho mine\n' > "$c/hooks/pre-commit"; chmod +x "$c/hooks/pre-commit"; }
i7_refuse "I7a-husky"   setup_husky        ".husky"
i7_refuse "I7b-prepare" setup_prepare      "prepare"
i7_refuse "I7c-hook"    setup_foreign_hook "existing non-lefthook hook"
# (d) redundant core.hooksPath naming the repo's OWN hooks dir => cleared, init proceeds (exit 0)
echo "-- I7d: redundant core.hooksPath cleared --"
A7d="$TMP/I7dA"; B7d="$TMP/I7dB"; newrepo "$A7d"; newrepo "$B7d"
( cd "$A7d" && git config core.hooksPath "$(common_of "$A7d")/hooks" )
( cd "$B7d" && git config core.hooksPath "$(common_of "$B7d")/hooks" )
EXTRA_ENV="OMAKASE_PAYLOAD=$PAY7"
leg_init "$A7d" "$TMP/I7dA.out" "$TMP/I7dA.err"; r7da=$?
go_init  "$B7d" "$TMP/I7dB.out" "$TMP/I7dB.err"; r7db=$?
EXTRA_ENV=""
if ran_go "I7d" "$TMP/I7dB.err" "$NOTICE_INIT"; then
  want "I7d" "$TMP/I7dA.out" "cleared redundant core.hooksPath"
  cmp_sorted "I7d" "stdout" "$TMP/I7dA.out" "$TMP/I7dB.out" "$A7d" "$B7d"
  cmp_sorted "I7d" "stderr" "$TMP/I7dA.err" "$TMP/I7dB.err" "$A7d" "$B7d"
  eq_exit "I7d" "$r7da" "$r7db"
  [ "$r7da" -eq 0 ] && pass "I7d: legacy proceeded (exit 0)" || fail "I7d: legacy exit $r7da (want 0)"
  a_hp="$(cd "$A7d" && git config --get core.hooksPath 2>/dev/null || echo UNSET)"
  b_hp="$(cd "$B7d" && git config --get core.hooksPath 2>/dev/null || echo UNSET)"
  { [ "$a_hp" = "UNSET" ] && [ "$b_hp" = "UNSET" ]; } && pass "I7d: core.hooksPath unset on both sides after init" || fail "I7d: core.hooksPath still set (legacy='$a_hp' go='$b_hp')"
fi

# ---------- I9: --source local path (manifest + recommends + #TAG pin) then bare re-init ----------
echo "== I9: --source #tag pin then bare re-init =="
SRC9="$TMP/src9"; rm -rf "$SRC9"; mkdir -p "$SRC9/payload/.claude/rules"
printf 'shared agents\n' > "$SRC9/payload/AGENTS.md"
( cd "$SRC9/payload" && ln -s AGENTS.md CLAUDE.md )
printf 'src rule\n' > "$SRC9/payload/.claude/rules/style.md"
printf 'name: i9-harness\nversion: 0.3.0\nrecommends: pair with the widget plugin\n' > "$SRC9/omakase.manifest"
( cd "$SRC9" && git init -q && git config user.email t@t && git config user.name t && git config commit.gpgsign false && git add -A && git commit -q -m harness && git tag v1 )
SRC9ABS="$(resolvep "$SRC9")"
CACHE9="$TMP/i9cache"; mkdir -p "$CACHE9"   # shared cache: keyed by source string, identical for both twins
A9="$TMP/i9A"; B9="$TMP/i9B"; newrepo "$A9"; newrepo "$B9"
# step 1: --source SRC#v1 (tag pin — a branch pin would be clobbered by the refresh, Task-4 quirk)
EXTRA_ENV="XDG_CACHE_HOME=$CACHE9"
leg_init "$A9" "$TMP/i9A1.out" "$TMP/i9A1.err" --source "$SRC9ABS#v1"; r9a1=$?
go_init  "$B9" "$TMP/i9B1.out" "$TMP/i9B1.err" --source "$SRC9ABS#v1"; r9b1=$?
# step 2: bare re-init (refreshes + re-injects the remembered source)
leg_init "$A9" "$TMP/i9A2.out" "$TMP/i9A2.err"; r9a2=$?
go_init  "$B9" "$TMP/i9B2.out" "$TMP/i9B2.err"; r9b2=$?
EXTRA_ENV=""
if ran_go "I9-src" "$TMP/i9B1.err" "$NOTICE_INIT" && ran_go "I9-bare" "$TMP/i9B2.err" "$NOTICE_INIT"; then
  CA9="$(common_of "$A9")"; CB9="$(common_of "$B9")"
  want "I9-src" "$TMP/i9A1.out" "this harness recommends — pair with the widget plugin"
  cmp_sorted "I9-src" "step1 stdout" "$TMP/i9A1.out" "$TMP/i9B1.out" "$A9" "$B9"
  cmp_sorted "I9-bare" "step2 stdout" "$TMP/i9A2.out" "$TMP/i9B2.out" "$A9" "$B9"
  [ "$r9a1" -eq "$r9b1" ] && [ "$r9a2" -eq "$r9b2" ] && pass "I9: exit codes equal across both steps" || fail "I9: exit codes differ (s1 $r9a1/$r9b1  s2 $r9a2/$r9b2)"
  cmp -s "$CA9/omakase/source" "$CB9/omakase/source" && pass "I9: \$OMK/source byte-identical" || { fail "I9: \$OMK/source differs"; diff "$CA9/omakase/source" "$CB9/omakase/source" | sed 's/^/      /'; }
  printf '%s\n' "$SRC9ABS#v1" | cmp -s - "$CA9/omakase/source" && pass "I9: \$OMK/source is the pinned source string" || fail "I9: \$OMK/source is not '$SRC9ABS#v1'"
  cmp_placed_sorted "I9" "$CA9/omakase/placed.tsv" "$CB9/omakase/placed.tsv"
  # ledger col 3 (source label) must be the pinned source string for a source install
  col3A="$(cut -f3 "$CA9/omakase/placed.tsv" | sort -u)"; col3B="$(cut -f3 "$CB9/omakase/placed.tsv" | sort -u)"
  { [ "$col3A" = "$SRC9ABS#v1" ] && [ "$col3B" = "$SRC9ABS#v1" ]; } && pass "I9: placed.tsv col 3 = the pinned source on both sides" || fail "I9: placed.tsv col3 wrong (legacy='$col3A' go='$col3B')"
  cmp_tree "I9 snapshot" "$CA9/omakase/payload-snapshot" "$CB9/omakase/payload-snapshot"
fi

# ---------- R3: pre-0.10 fallback remove (exclude sentinel, no ledger) ----------
echo "== R3: pre-0.10 fallback remove (no ledger) =="
A3r="$TMP/r3A"; B3r="$TMP/r3B"; newrepo "$A3r"; newrepo "$B3r"
seed_pre010(){ # $1 repo: place the BASE payload's files + an exclude block, but NO placed.tsv ledger
  local R="$1" c; c="$(common_of "$R")"
  cp -RP "$PAY/." "$R/"                                   # place the real base payload (remove enumerates payload/ in the no-ledger fallback)
  mkdir -p "$c/info" "$c/omakase"                         # $OMK dir present but ledger-less = the pre-0.10 sentinel
  printf '%s\n.omakase/\n.claude/\n%s\n' "$BEGIN" "$END" > "$c/info/exclude"; }
seed_pre010 "$A3r"; seed_pre010 "$B3r"
PRE3A="$TMP/r3A.pretree"; PRE3B="$TMP/r3B.pretree"
# capture what a clean repo looks like (pre-seed) for the roundtrip target
( cd "$A3r" && git rev-parse HEAD >/dev/null )   # sanity: still a repo
leg_remove "$A3r" "$TMP/r3A.out" "$TMP/r3A.err"; rr3a=$?
go_remove  "$B3r" "$TMP/r3B.out" "$TMP/r3B.err"; rr3b=$?
if ran_go "R3" "$TMP/r3B.err" "$NOTICE_REMOVE"; then
  want "R3" "$TMP/r3A.out" "omakase: removed."
  cmp_raw "R3" "stdout" "$TMP/r3A.out" "$TMP/r3B.out" "$A3r" "$B3r"
  cmp_raw "R3" "stderr" "$TMP/r3A.err" "$TMP/r3B.err" "$A3r" "$B3r"
  eq_exit "R3" "$rr3a" "$rr3b"
  snap_tree "$A3r" > "$TMP/r3A.tree"; snap_tree "$B3r" > "$TMP/r3B.tree"
  cmp -s "$TMP/r3A.tree" "$TMP/r3B.tree" && pass "R3: legacy and Go post-remove trees identical" || { fail "R3: post-remove trees differ"; diff "$TMP/r3A.tree" "$TMP/r3B.tree" | sed 's/^/      /'; }
  # the base payload's files enumerated + deleted; the exclude block stripped on both
  { [ ! -e "$A3r/.omakase" ] && [ ! -e "$B3r/.omakase" ]; } && pass "R3: enumerated payload files deleted (.omakase gone)" || fail "R3: payload files not deleted"
  { ! grep -qF "$BEGIN" "$(common_of "$A3r")/info/exclude" && ! grep -qF "$BEGIN" "$(common_of "$B3r")/info/exclude"; } && pass "R3: exclude block stripped on both sides" || fail "R3: exclude block survived"
fi

# ---------- F1: shim fallback — Go binary unresolvable => legacy body + one-line notice ----------
# Mirrors status-parity P9: OMAKASE_BIN pointed at a nonexistent path so the shim's
# `[ -x "$BIN" ]` check fails without hiding a real toolchain, invoked DIRECTLY (not
# via go_init, which pins OMAKASE_BIN to the real binary). The fallback must exec the
# SAME legacy body a direct legacy run does — byte-equal outcome, equal exit.
echo "== F1: shim fallback (Go binary unresolvable) =="
PAYF="$TMP/payF"; build_rich_payload "$PAYF"
AF="$TMP/f1-legacy"; BF="$TMP/f1-shim"; newrepo "$AF"; newrepo "$BF"
( cd "$AF" && env -u OMAKASE_BIN -u OMAKASE_ICON -u NO_COLOR HOME="$FAKEHOME" LEFTHOOK_BIN="$LEFTHOOK" OMAKASE_PAYLOAD="$PAYF" bash "$LEG_INIT" ) >"$TMP/f1A.out" 2>"$TMP/f1A.err"; rfa=$?
( cd "$BF" && env -u OMAKASE_ICON -u NO_COLOR HOME="$FAKEHOME" LEFTHOOK_BIN="$LEFTHOOK" OMAKASE_PAYLOAD="$PAYF" OMAKASE_BIN=/nonexistent/omakase bash "$SHIM_INIT" ) >"$TMP/f1B.out" 2>"$TMP/f1B.err"; rfb=$?
if grep -qF "$NOTICE_INIT" "$TMP/f1B.err"; then pass "F1: shim stderr carries the fallback notice (legacy body ran)"; else fail "F1: shim stderr MISSING the fallback notice"; fi
# the notice is EXTRA stderr the direct legacy run does not emit — strip it before comparing streams
grep -vF "$NOTICE_INIT" "$TMP/f1B.err" > "$TMP/f1B.err.clean"
cmp_sorted "F1" "stdout" "$TMP/f1A.out" "$TMP/f1B.out" "$AF" "$BF"
cmp_sorted "F1" "stderr (notice stripped)" "$TMP/f1A.err" "$TMP/f1B.err.clean" "$AF" "$BF"
eq_exit "F1" "$rfa" "$rfb"
CAF="$(common_of "$AF")"; CBF="$(common_of "$BF")"
cmp_placed_sorted "F1" "$CAF/omakase/placed.tsv" "$CBF/omakase/placed.tsv"
cmp_tree "F1 snapshot" "$CAF/omakase/payload-snapshot" "$CBF/omakase/payload-snapshot"

rm -rf "$TMP"
echo ""
[ "$FAILED" -eq 0 ] && echo "ALL PASS" || { echo "FAILURES PRESENT"; exit 1; }
