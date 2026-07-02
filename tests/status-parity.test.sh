#!/usr/bin/env bash
# tests/status-parity.test.sh — the Phase-1 RELEASE GATE for the Go port of omakase status.
#
# The v1 bash body is preserved verbatim at bin/legacy/status.sh (the parity oracle). The
# new bin/status.sh is a thin shim that rebuilds and execs the Go binary (dist/omakase),
# falling back to the legacy bash only when the binary can't be resolved. This test proves
# BYTE PARITY: for every scenario below, in BOTH terminal and --markdown modes, the legacy
# bash and the shim (=> Go binary) must produce byte-identical stdout, byte-identical stderr,
# and equal exit codes — under identical pinned env and identical cwd. The shim's one-line
# fallback notice must be ABSENT from stderr in every run: its presence means the binary did
# not run and the comparison would be bash-vs-bash (a false green), so we fail on it.
#
# Skip-with-notice (exit 0) ONLY when dist/omakase is absent AND go is not on PATH. Otherwise
# we build the binary and run. Scenarios that need a real `lefthook dump` (installs + the
# guards chart) SKIP gracefully when lefthook is unresolvable; bash 3.2-safe throughout.
set -u

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="$HERE/.."
LEGACY="$ROOT/bin/legacy/status.sh"
SHIM="$ROOT/bin/status.sh"
INIT="$ROOT/bin/init.sh"
PAY="$ROOT/payload"
BIN="$ROOT/dist/omakase"
LEFTHOOK="${LEFTHOOK_BIN:-$(command -v lefthook || true)}"
NOW=1700000000
TMP="${TMPDIR:-/tmp}/status-parity.$$"
NOTICE='omakase: Go binary not present — running the bundled v1 status script'
FAILED=0
pass(){ echo "  PASS: $1"; }
fail(){ echo "  FAIL: $1"; FAILED=1; }
skip(){ echo "  SKIP: $1"; }

# lefthook on PATH so init can install hooks (idiom of tests/omakase-gate.test.sh:12-20).
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
newrepo(){ rm -rf "$1"; mkdir -p "$1"; ( cd "$1" && git init -q && git config user.email t@t && git config user.name t && git config commit.gpgsign false && git commit -q --allow-empty -m init ); }

# Per-invocation env overrides (defaults resolve lefthook via LEFTHOOK_BIN so the guards
# chart is deterministic regardless of PATH; P6 overrides both to make lefthook unresolvable).
P_PATH="$PATH"
P_LH="$LEFTHOOK"

# run_impl <impl> <cwd> <home> <outfile> <errfile> <flag-or-empty>
# Pins HOME + OMAKASE_NOW, unsets OMAKASE_ICON + NO_COLOR + OMAKASE_BIN (a stray exported
# OMAKASE_BIN would make bin/status.sh exec a foreign binary instead of dist/omakase, silently
# defeating the whole comparison), pins PATH + LEFTHOOK_BIN. Only $1 is inspected by status.sh,
# so a single optional flag ("" or --markdown) covers both modes.
run_impl(){
  if [ -n "$6" ]; then
    ( cd "$2" && env -u OMAKASE_ICON -u NO_COLOR -u OMAKASE_BIN PATH="$P_PATH" LEFTHOOK_BIN="$P_LH" HOME="$3" OMAKASE_NOW="$NOW" bash "$1" "$6" ) >"$4" 2>"$5"
  else
    ( cd "$2" && env -u OMAKASE_ICON -u NO_COLOR -u OMAKASE_BIN PATH="$P_PATH" LEFTHOOK_BIN="$P_LH" HOME="$3" OMAKASE_NOW="$NOW" bash "$1" ) >"$4" 2>"$5"
  fi
}

# expect_marker <label> <file> [marker...]: fixed-string (grep -F), CASE-SENSITIVE
# containment check against the LEGACY capture — the reference side. Case matters: some
# fixed phrases overlap in lowercase with text that is ALWAYS present (e.g. the guards
# chart's self-heal row reads "restore any missing injected files", so only the all-caps
# "MISSING" row-marker is actually specific to the states-matrix scenario). No markers =>
# no-op (a scenario like P7's "not a repo" error has nothing state-specific to pin).
#
# This exists because byte-parity alone proves legacy and shim AGREE, not that they agree
# on the RIGHT thing: if a future fixture edit silently stopped exercising the state a
# scenario claims to cover, both sides could still land on the same wrong branch and this
# gate would go green vacuously. Pinning marker text in the reference output closes that gap.
expect_marker(){
  local label="$1" file="$2"; shift 2
  [ "$#" -eq 0 ] && return 0
  local pat missing=""
  for pat in "$@"; do
    grep -qF -- "$pat" "$file" 2>/dev/null || missing="${missing:+$missing, }'$pat'"
  done
  if [ -z "$missing" ]; then pass "$label: legacy output carries the expected marker(s)"
  else fail "$label: legacy output MISSING marker(s) $missing — fixture may not exercise the intended state"; fi
}

# expect_global_empty <label> <file> <mode>: pins the Global-group-is-empty state to the
# Global section specifically. A bare substring check for "(none)" is not enough — the
# fixture repo's Committed section is ALSO "(none)" whenever empty, so that marker passes
# even when the Global group is NOT empty (this replaced exactly that false assurance).
# Instead: find the Global section header line and require the very NEXT line to be that
# mode's empty-group marker ("    (none)" term / "- _(none)_" md), per bin/legacy/status.sh.
expect_global_empty(){
  local label="$1" file="$2" mode="$3" hdr empty
  if [ "$mode" = md ]; then hdr='^### Global '; empty='- _(none)_'
  else                       hdr='^GLOBAL ';     empty='    (none)'; fi
  if awk -v hdr="$hdr" -v empty="$empty" \
      '$0 ~ hdr { g = NR } g && NR == g + 1 && $0 == empty { ok = 1 } END { exit ok ? 0 : 1 }' \
      "$file"
  then pass "$label: Global group renders empty (line after the header is '$empty')"
  else fail "$label: Global group is NOT empty right after its header — fixture may not exercise empty-HOME"; fi
}

# parity <label> <cwd> <home> <flag-or-empty> [marker...]: run both impls, compare
# stdout/stderr/exit, assert non-empty legacy stdout on an exit-0 run, and (when markers
# are given) assert the legacy capture contains them — see expect_marker above.
parity(){
  local label="$1" cwd="$2" home="$3" flag="$4"; shift 4
  local lo="$TMP/leg.out" le="$TMP/leg.err" so="$TMP/shim.out" se="$TMP/shim.err"
  local lrc srrc
  run_impl "$LEGACY" "$cwd" "$home" "$lo" "$le" "$flag"; lrc=$?
  run_impl "$SHIM"   "$cwd" "$home" "$so" "$se" "$flag"; srrc=$?
  if grep -qF "$NOTICE" "$se"; then
    fail "$label: shim fell back to legacy bash (the Go binary did not run)"
    sed 's/^/      /' "$se"
    return
  fi
  if diff "$lo" "$so" >"$TMP/dout" 2>&1; then pass "$label: stdout byte-identical"
  else fail "$label: stdout DIFFERS"; sed 's/^/      /' "$TMP/dout"; fi
  if diff "$le" "$se" >"$TMP/derr" 2>&1; then pass "$label: stderr byte-identical"
  else fail "$label: stderr DIFFERS"; sed 's/^/      /' "$TMP/derr"; fi
  if [ "$lrc" -eq "$srrc" ]; then pass "$label: exit codes equal ($lrc)"
  else fail "$label: exit codes differ (legacy=$lrc shim=$srrc)"; fi
  if [ "$lrc" -eq 0 ]; then
    if [ -s "$lo" ]; then pass "$label: legacy stdout non-empty"
    else fail "$label: legacy stdout EMPTY (exit 0 with nothing rendered)"; fi
  fi
  expect_marker "$label" "$lo" "$@"
}

# parity2 <label> <cwd> <home> [marker...]: exercise BOTH terminal and --markdown modes,
# forwarding the same marker(s) to both. Only use this when the marker text is identical
# in both modes; a scenario whose marker text differs by mode (P2, P4) calls parity()
# directly for each mode instead — see those call sites below.
parity2(){
  local label="$1" cwd="$2" home="$3"; shift 3
  parity "$label [term]" "$cwd" "$home" ""           "$@"
  parity "$label [md]"   "$cwd" "$home" "--markdown" "$@"
}

# Build a PATH with EVERY lefthook executable removed (system dirs appended as candidates,
# and any dir — including a system one — that carries lefthook is skipped). Portable way to
# make lefthook unresolvable for P6 while keeping git/shasum/awk resolvable.
lhfree_path(){
  local out="" d oldifs="$IFS"
  IFS=':'
  for d in $PATH /usr/bin /bin /usr/sbin /sbin; do
    IFS="$oldifs"
    [ -n "$d" ] || { IFS=':'; continue; }
    [ -x "$d/lefthook" ] && { IFS=':'; continue; }
    case ":$out:" in *":$d:"*) IFS=':'; continue;; esac
    out="${out:+$out:}$d"
    IFS=':'
  done
  IFS="$oldifs"
  printf '%s' "$out"
}

# ================= Shared personal $HOME (claude rule + skill + CLAUDE.md + copilot skill) =================
H1="$TMP/home1"
mkdir -p "$H1/.claude/rules" "$H1/.claude/skills/myskill" "$H1/.copilot/skills/copskill"
printf 'global doctrine\n' > "$H1/.claude/CLAUDE.md"
printf 'personal rule\n'   > "$H1/.claude/rules/personal.md"
printf 'skill body\n'      > "$H1/.claude/skills/myskill/SKILL.md"
printf 'cop skill\n'       > "$H1/.copilot/skills/copskill/SKILL.md"

# ---------- P1: uninstalled repo (audit view + personal inventory) ----------
echo "== P1: uninstalled repo =="
R1="$TMP/p1"; newrepo "$R1"
mkdir -p "$R1/.claude/rules" "$R1/src"
printf 'team rule\n' > "$R1/.claude/rules/team.md"    # committed HARNESS file
printf 'app\n'       > "$R1/src/app.js"               # committed NON-harness file
( cd "$R1" && git add .claude/rules/team.md src/app.js && git commit -qm files )
parity2 "P1 uninstalled" "$R1" "$H1" "No omakase harness"

# Install-based scenarios (P2-P4, P6, P8) need a real lefthook: init installs hooks and the
# guards chart joins a `lefthook dump`. Skip them as one group when lefthook is unresolvable.
if [ "$HAVE_LH" -eq 0 ]; then
  skip "P2-P4, P6, P8 need lefthook (install + guards dump); LEFTHOOK_BIN unset and lefthook not on PATH"
fi

# R2 is created by P2 and reused (read-only) by P6 and P8, so keep it pristine.
R2="$TMP/p2"
if [ "$HAVE_LH" -eq 1 ]; then
  # ---------- P2: plain install + a seeded 4-column ledger ----------
  echo "== P2: plain install + seeded ledger =="
  newrepo "$R2"
  ( cd "$R2" && HOME="$H1" OMAKASE_PAYLOAD="$PAY" bash "$INIT" ) >/dev/null 2>&1 || fail "P2: init failed"
  COMMON2="$(cd "$R2" && cd "$(git rev-parse --git-common-dir)" && pwd)"; mkdir -p "$COMMON2/omakase"
  LEDGER2="$COMMON2/omakase/ledger.tsv"; HEAD2="$(cd "$R2" && git rev-parse HEAD)"
  # A pass row and a fail row, DISTINCT gates, epochs NOW-60 and NOW-7200. `markers` is the
  # base payload's wired gate, so its fail verdict surfaces on the guards chart (✗ + age).
  # Deliberately NO 6-column pre-v2 row (§5): real init rotates those aside, so a hand-built
  # one would exercise an unreachable path; 4-column rows only.
  printf '%s\ttests\tpass\t%s\n'   $((NOW-60))   "$HEAD2" >> "$LEDGER2"
  printf '%s\tmarkers\tfail\t%s\n' $((NOW-7200)) "$HEAD2" >> "$LEDGER2"
  # Markers: the guards chart carries the wired gate name ("markers"), and the footprint
  # line renders — case differs by mode ("zero footprint" term / "Zero footprint" md), so
  # each mode gets its own call rather than routing through parity2's shared marker list.
  parity "P2 plain install [term]" "$R2" "$H1" ""           "markers" "zero footprint"
  parity "P2 plain install [md]"   "$R2" "$H1" "--markdown" "markers" "Zero footprint"

  # ---------- P3: the states matrix (MISSING / DRIFTED / disabled + normal before/after) ----------
  echo "== P3: states matrix =="
  PAY3="$TMP/pay3"; rm -rf "$PAY3"; cp -R "$PAY/." "$PAY3/"
  mkdir -p "$PAY3/.claude/rules"
  for f in a b c d e; do printf 'rule %s\n' "$f" > "$PAY3/.claude/rules/$f.md"; done
  R3="$TMP/p3"; newrepo "$R3"
  ( cd "$R3" && HOME="$H1" OMAKASE_PAYLOAD="$PAY3" bash "$INIT" ) >/dev/null 2>&1 || fail "P3: init failed"
  COMMON3="$(cd "$R3" && cd "$(git rev-parse --git-common-dir)" && pwd)"
  PLACED3="$COMMON3/omakase/placed.tsv"
  rm -f "$R3/.claude/rules/b.md"                       # MISSING
  printf 'drift\n' >> "$R3/.claude/rules/c.md"         # DRIFTED (appended AFTER init hashed it)
  awk -F'\t' -v OFS='\t' '$1==".claude/rules/d.md"{$5=0} 1' "$PLACED3" > "$PLACED3.tmp" && mv "$PLACED3.tmp" "$PLACED3"   # disabled
  # a.md and e.md are left normal (before/after rows).
  # Markers pin all three fixture-driven states so a future edit that stops exercising one
  # of them (e.g. forgetting to delete b.md) fails loudly instead of passing vacuously.
  parity2 "P3 states matrix" "$R3" "$H1" "MISSING" "DRIFTED" "disabled"

  # ---------- P4: --source install with a CLAUDE.md -> AGENTS.md symlink ----------
  echo "== P4: --source install with symlink =="
  SRC4="$TMP/p4src"; rm -rf "$SRC4"; mkdir -p "$SRC4/payload/.claude"
  ( cd "$SRC4" && git init -q && git config user.email t@t && git config user.name t && git config commit.gpgsign false )
  printf 'agent doctrine\n' > "$SRC4/payload/agents.md"
  ( cd "$SRC4/payload" && ln -s agents.md claude.md )  # symlink row => the arrow
  cat > "$SRC4/omakase.manifest" <<'MAN'
name: p4-harness
version: 0.2.0
MAN
  ( cd "$SRC4" && git add -A && git commit -q -m harness )
  SRC4ABS="$(cd "$SRC4" && pwd)"
  R4="$TMP/p4"; newrepo "$R4"
  CACHE4="$TMP/cache4"; mkdir -p "$CACHE4"
  ( cd "$R4" && HOME="$H1" XDG_CACHE_HOME="$CACHE4" bash "$INIT" --source "$SRC4ABS" ) >/dev/null 2>&1 || fail "P4: --source init failed"
  [ -L "$R4/claude.md" ] && pass "P4: source symlink placed as a symlink (arrow row exercised)" || fail "P4: claude.md not placed as a symlink"
  # Marker: the injected-inventory arrow row for the symlink ("->" term / "→" md — distinct
  # glyphs per mode, so each mode gets its own call rather than parity2's shared list).
  parity "P4 source+symlink [term]" "$R4" "$H1" ""           "->"
  parity "P4 source+symlink [md]"   "$R4" "$H1" "--markdown" "→"
fi

# ---------- P5: pre-0.10 install (placed.list, no placed.tsv) ----------
echo "== P5: pre-0.10 placed.list =="
R5="$TMP/p5"; newrepo "$R5"
COMMON5="$(cd "$R5" && cd "$(git rev-parse --git-common-dir)" && pwd)"; mkdir -p "$COMMON5/omakase"
printf '%s\n%s\n' '.claude/rules/team.md' '.omakase/gates/example.sh' > "$COMMON5/omakase/placed.list"
rm -f "$COMMON5/omakase/placed.tsv"
parity2 "P5 pre-0.10" "$R5" "$H1" "Pre-0.10"

# ---------- P6: lefthook unresolved (P2's repo, LEFTHOOK_BIN= and PATH without lefthook) ----------
if [ "$HAVE_LH" -eq 1 ]; then
  echo "== P6: lefthook unresolved =="
  P_PATH="$(lhfree_path)"; P_LH=""
  parity2 "P6 lefthook unresolved" "$R2" "$H1" "lefthook not resolved"
  P_PATH="$PATH"; P_LH="$LEFTHOOK"   # restore defaults
fi

# ---------- P7: not inside a git repo (both exit 1, same stderr line) ----------
echo "== P7: not a git repo =="
R7="$TMP/p7-notrepo"; rm -rf "$R7"; mkdir -p "$R7"
parity2 "P7 not a repo" "$R7" "$H1"

# ---------- P8: empty HOME => Global group renders (none) ----------
# Marker: a bare "(none)" substring is satisfied by the Committed section alone (also
# empty in this fixture), so pin the empty state to the line right after the Global
# section header instead — see expect_global_empty above.
if [ "$HAVE_LH" -eq 1 ]; then
  echo "== P8: empty HOME =="
  HEMPTY="$TMP/home-empty"; rm -rf "$HEMPTY"; mkdir -p "$HEMPTY"
  parity "P8 empty HOME [term]" "$R2" "$HEMPTY" ""
  expect_global_empty "P8 empty HOME [term]" "$TMP/leg.out" term
  parity "P8 empty HOME [md]"   "$R2" "$HEMPTY" "--markdown"
  expect_global_empty "P8 empty HOME [md]"   "$TMP/leg.out" md
fi

# ---------- P9: shim fallback when the Go binary cannot be resolved ----------
# The path every plugin-bundle adopter without Go on PATH actually runs. OMAKASE_BIN is
# pointed at a path that doesn't exist so the shim's `[ -x "$BIN" ]` check fails without
# needing to hide a real Go toolchain; this is invoked directly (not via run_impl, which
# unsets OMAKASE_BIN — the very override this scenario needs). Runs against P1's R1/H1
# (uninstalled repo) so it isn't gated on lefthook. One mode is enough: the fallback execs
# the same legacy script regardless of --markdown.
echo "== P9: shim fallback (Go binary unresolvable) =="
P9SO="$TMP/p9-shim.out"; P9SE="$TMP/p9-shim.err"; P9LO="$TMP/p9-legacy.out"
( cd "$R1" && env -u OMAKASE_ICON -u NO_COLOR PATH="$P_PATH" LEFTHOOK_BIN="$P_LH" \
    HOME="$H1" OMAKASE_NOW="$NOW" OMAKASE_BIN=/nonexistent/omakase bash "$SHIM" ) >"$P9SO" 2>"$P9SE"
P9RC=$?
( cd "$R1" && env -u OMAKASE_ICON -u NO_COLOR -u OMAKASE_BIN PATH="$P_PATH" LEFTHOOK_BIN="$P_LH" \
    HOME="$H1" OMAKASE_NOW="$NOW" bash "$LEGACY" ) >"$P9LO" 2>/dev/null
if grep -qF "$NOTICE" "$P9SE"; then pass "P9 fallback: stderr carries the fallback notice"
else fail "P9 fallback: stderr MISSING the fallback notice"; fi
if diff "$P9LO" "$P9SO" >"$TMP/p9diff" 2>&1; then pass "P9 fallback: stdout byte-identical to a direct legacy run"
else fail "P9 fallback: stdout DIFFERS from a direct legacy run"; sed 's/^/      /' "$TMP/p9diff"; fi
if [ "$P9RC" -eq 0 ]; then pass "P9 fallback: exit 0"
else fail "P9 fallback: exit $P9RC (want 0)"; fi

rm -rf "$TMP"
echo ""
[ "$FAILED" -eq 0 ] && echo "ALL PASS" || { echo "FAILURES PRESENT"; exit 1; }
