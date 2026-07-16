#!/usr/bin/env bash
# Behavioral spec for the fail-closed hook chain under the #98 dispatcher
# scheme: .git/hooks holds permanent ~5-line dispatchers that exec the
# machine-wide binary copy with `omakase hook <name>`; the binary verifies the
# harness and runs the manifest-declared gates itself (no third-party runner).
# Every leg must fail CLOSED (block the commit with an instruction) instead of
# silently skipping gates:
#   D1. BINARY MISSING: the stable copy is absent -> pre-commit BLOCKS with a
#       one-line fix; no commit is created.
#   D2. post-checkout with the binary missing exits 0 — a checkout never fails
#       because omakase is absent (heal is best-effort by contract).
#   D3. BINARY PRESENT: a complete install commits AND the manifest gate
#       demonstrably runs (heal, not just unblock).
#   D5. OMAKASE_SKIP_GATES=1 is honored: the commit succeeds and the skip is
#       audited on stdout (nothing SILENTLY lost).
#   D6. OMAKASE_SKIP_GATES=1 does NOT bypass the harness verify: a wiped overlay
#       still blocks (the only escape is git's own --no-verify).
#   D8. TORN STATE: dispatchers present but no harness state -> blocked with
#       the 'omakase init' instruction.
# Hooks are exercised through REAL `git commit` runs under env -i with a
# fixture HOME, so the dispatcher's own env interpolation and exec are on the
# tested path, not simulated.
set -u
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
INIT="$HERE/../bin/init.sh"
TMP="${TMPDIR:-/tmp}/omakase-hook-failclosed.$$"
FAILED=0
pass(){ echo "  PASS: $1"; }
fail(){ echo "  FAIL: $1"; FAILED=1; }

OMAKASE="$( cd "$HERE/.." && HERE="$PWD/bin" && . bin/lib-omakase-bin.sh && resolve_omakase 2>/dev/null && echo "$OMAKASE_BIN_RESOLVED" )"
[ -n "$OMAKASE" ] || { echo "FATAL: no omakase binary resolvable"; exit 1; }

# A clean PATH for the env -i commits: the real PATH plus the system dirs, so
# git/sh/env/bash and the coreutils the base gate uses stay reachable while the
# rest of the ambient environment is cleared.
CLEANPATH="$PATH:/usr/bin:/bin:/usr/sbin:/sbin"
mkdir -p "$TMP"

# Self-contained HOME + cache for the setup inits, so a `bash init` self-install
# never touches the real machine. The commit-time HOME is set per scenario below
# (env -i), which is what actually drives the dispatcher's ${…:-$HOME/.cache}.
export HOME="$TMP/home"; export XDG_CACHE_HOME="$TMP/cache"
mkdir -p "$HOME" "$XDG_CACHE_HOME"
if command -v go >/dev/null 2>&1; then
  export GOMODCACHE="$(go env GOMODCACHE)"
  export GOCACHE="$(go env GOCACHE)"
fi

# A repo with a REAL install (dispatchers + placed harness + wiring).
newrepo(){
  rm -rf "$1"; mkdir -p "$1"
  ( cd "$1" && git init -q && git config user.email t@t && git config user.name t && git config commit.gpgsign false && git commit -q --allow-empty -m init )
  ( cd "$1" && bash "$INIT" ) >/dev/null 2>&1 || fail "setup: init failed in $1"
}

# A fixture HOME whose stable path carries the real omakase binary. $1 = home dir.
write_stable_bin(){
  mkdir -p "$1/.cache/omakase/bin/current"
  cp "$OMAKASE" "$1/.cache/omakase/bin/current/omakase"
  chmod +x "$1/.cache/omakase/bin/current/omakase"
}

# try_commit <repo> <fixture-home> [extra env kv...]: one real commit attempt
# under env -i; returns git's exit code, captures stdout/stderr.
try_commit(){
  local repo="$1" home="$2"; shift 2
  ( cd "$repo" \
    && echo "line $(date +%s%N 2>/dev/null || echo x)$RANDOM" >> f.txt \
    && git add f.txt \
    && env -i PATH="$CLEANPATH" HOME="$home" "$@" git commit -q -m tick ) >"$TMP/out" 2>"$TMP/err"
}

commits_in(){ ( cd "$1" && git rev-list --count HEAD ); }
ledger_of(){ echo "$1/.git/omakase/ledger.tsv"; }

echo "== D1: binary missing — pre-commit blocks with the fix line =="
R1="$TMP/d1"; newrepo "$R1"
H1="$TMP/d1-home"; rm -rf "$H1"; mkdir -p "$H1"   # empty: no stable binary
BEFORE="$(commits_in "$R1")"
if try_commit "$R1" "$H1"; then
  fail "D1: commit SUCCEEDED with no omakase binary — dispatcher fell open"
else
  pass "D1: commit refused with the binary missing (rc!=0)"
fi
grep -q "omakase: pre-commit blocked" "$TMP/err" && pass "D1: stderr carries the blocked line" \
  || fail "D1: no blocked line on stderr: $(cat "$TMP/err")"
grep -q "omakase init" "$TMP/err" && pass "D1: the fix line names omakase init" \
  || fail "D1: no fix instruction on stderr: $(cat "$TMP/err")"
[ "$(commits_in "$R1")" = "$BEFORE" ] && pass "D1: no commit was created" \
  || fail "D1: a commit landed despite the block"

echo "== D2: binary missing — post-checkout exits 0 (checkout never fails) =="
if ( cd "$R1" && env -i PATH="$CLEANPATH" HOME="$H1" git checkout -q -b d2branch ) 2>"$TMP/err"; then
  pass "D2: checkout succeeds with the binary missing"
else
  fail "D2: checkout FAILED because omakase is absent: $(cat "$TMP/err")"
fi

echo "== D3: binary present — the commit passes and the gate demonstrably runs =="
R3="$TMP/d3"; newrepo "$R3"
H3="$TMP/d3-home"; rm -rf "$H3"
write_stable_bin "$H3"
if try_commit "$R3" "$H3"; then
  pass "D3: commit succeeds with the binary present and the harness complete"
else
  fail "D3: commit blocked despite a present binary and complete overlay (rc=$?): $(cat "$TMP/err")"
fi
awk -F'\t' '$2=="markers" && $3=="pass"{f=1} END{exit !f}' "$(ledger_of "$R3")" 2>/dev/null \
  && pass "D3: the manifest gate actually RAN and recorded a verdict (heal, not just unblock)" \
  || fail "D3: no markers PASS row — the gate never ran ($(cat "$(ledger_of "$R3")" 2>/dev/null))"

echo "== D5: OMAKASE_SKIP_GATES=1 skip is honored (audited, commit clean) =="
if try_commit "$R3" "$H3" OMAKASE_SKIP_GATES=1; then
  pass "D5: OMAKASE_SKIP_GATES=1 commits cleanly (documented bypass)"
else
  fail "D5: OMAKASE_SKIP_GATES=1 was blocked (rc=$?): $(cat "$TMP/err")"
fi
grep -q "all gates skipped via OMAKASE_SKIP_GATES (audited)" "$TMP/out" "$TMP/err" \
  && pass "D5: the skip is audited (not silent)" \
  || fail "D5: no audited skip line: $(cat "$TMP/out" "$TMP/err")"

echo "== D6: OMAKASE_SKIP_GATES=1 does NOT bypass the harness verify =="
REL="$(awk -F'\t' '$1 ~ /\.sh$/{print $1; exit}' "$R3/.git/omakase/placed.tsv")"
rm -f "$R3/$REL"
BEFORE6="$(commits_in "$R3")"
if try_commit "$R3" "$H3" OMAKASE_SKIP_GATES=1; then
  fail "D6: commit passed over a wiped overlay under OMAKASE_SKIP_GATES=1 — the verify is bypassable"
else
  pass "D6: wiped overlay still blocks under OMAKASE_SKIP_GATES=1 (only --no-verify escapes)"
fi
grep -q "missing: $REL" "$TMP/err" && pass "D6: the block names the missing file" \
  || fail "D6: missing file not named: $(cat "$TMP/err")"
[ "$(commits_in "$R3")" = "$BEFORE6" ] && pass "D6: no commit was created" \
  || fail "D6: a commit landed despite the block"

echo "== D8: dispatchers present but no harness state — blocked with the init instruction =="
R8="$TMP/d8"; newrepo "$R8"
H8="$TMP/d8-home"; rm -rf "$H8"
write_stable_bin "$H8"
rm -rf "$R8/.git/omakase"   # torn state: state wiped without `omakase remove`
BEFORE8="$(commits_in "$R8")"
if try_commit "$R8" "$H8"; then
  fail "D8: commit passed with no harness state behind the hooks"
else
  pass "D8: torn state blocks the commit"
fi
grep -q "omakase init" "$TMP/err" && pass "D8: the block points at omakase init" \
  || fail "D8: no init instruction: $(cat "$TMP/err")"
[ "$(commits_in "$R8")" = "$BEFORE8" ] && pass "D8: no commit was created" \
  || fail "D8: a commit landed despite the block"

rm -rf "$TMP"
echo ""
[ "$FAILED" -eq 0 ] && echo "ALL PASS" || { echo "FAILURES PRESENT"; exit 1; }
