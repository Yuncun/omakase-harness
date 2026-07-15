#!/usr/bin/env bash
# Behavioral spec for the fail-closed hook chain under the #98 dispatcher
# scheme: .git/hooks holds permanent ~5-line dispatchers that exec the
# machine-wide binary copy with `omakase hook <name>`; the binary verifies the
# harness and runs the wired gates through the pinned lefthook. Every leg must
# fail CLOSED (block the commit with an instruction) instead of silently
# skipping gates:
#   D1. BINARY MISSING: the stable copy is absent -> pre-commit BLOCKS with a
#       one-line fix; no commit is created.
#   D2. post-checkout with the binary missing exits 0 — a checkout never fails
#       because omakase is absent (heal is best-effort by contract).
#   D3. HEAL: lefthook only in the omakase cache (the pinned version dir) ->
#       the commit succeeds AND the gate runner demonstrably ran.
#   D4. BLOCK: no lefthook anywhere -> the commit is REFUSED ("omakase:
#       BLOCKING" on stderr, an escape hatch named, no commit created).
#   D5. LEFTHOOK=0 is honored: same empty machine, LEFTHOOK=0 -> commit
#       succeeds (gates skipped by explicit choice; nothing SILENTLY lost).
#   D6. LEFTHOOK=0 does NOT bypass the harness verify: a wiped overlay still
#       blocks (the only escape is git's own --no-verify).
#   D7. LEFTHOOK_BIN override resolves at tier 1: gates run through it.
#   D8. TORN STATE: dispatchers present but no harness state -> blocked with
#       the 'omakase init' instruction.
# Hooks are exercised through REAL `git commit` runs under env -i with a
# fixture HOME/XDG_CACHE_HOME, so the dispatcher's own env interpolation and
# exec are on the tested path, not simulated.
set -u
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
INIT="$HERE/../bin/init.sh"
TMP="${TMPDIR:-/tmp}/omakase-hook-failclosed.$$"
FAILED=0
pass(){ echo "  PASS: $1"; }
fail(){ echo "  FAIL: $1"; FAILED=1; }

OMAKASE="$( cd "$HERE/.." && HERE="$PWD/bin" && . bin/lib-omakase-bin.sh && resolve_omakase 2>/dev/null && echo "$OMAKASE_BIN_RESOLVED" )"
[ -n "$OMAKASE" ] || { echo "FATAL: no omakase binary resolvable"; exit 1; }
# The pinned lefthook version, read from the lib so this suite never drifts.
LH_VER="$(. "$HERE/../bin/lib-lefthook.sh"; echo "$LEFTHOOK_VERSION")"

# A lefthook-free PATH: the real PATH + system dirs, skipping any dir that
# carries a lefthook — a distro package at /usr/bin/lefthook must not satisfy
# the PATH tier and turn the block scenarios vacuous, while git/sh stay
# reachable.
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
CLEANPATH="$(lhfree_path)"
mkdir -p "$TMP"

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

# A fake lefthook that logs every invocation and exits 0. $1 = path to place it.
write_fake_lefthook(){
  mkdir -p "$(dirname "$1")"
  cat > "$1" <<FAKE
#!/bin/sh
echo "\$@" >> "$TMP/lefthook-invocations.log"
exit 0
FAKE
  chmod +x "$1"
}

# try_commit <repo> <fixture-home> [extra env kv...]: one real commit attempt
# under env -i; returns git's exit code, captures stderr.
try_commit(){
  local repo="$1" home="$2"; shift 2
  ( cd "$repo" \
    && echo "line $(date +%s%N 2>/dev/null || echo x)$RANDOM" >> f.txt \
    && git add f.txt \
    && env -i PATH="$CLEANPATH" HOME="$home" "$@" git commit -q -m tick ) >"$TMP/out" 2>"$TMP/err"
}

commits_in(){ ( cd "$1" && git rev-list --count HEAD ); }

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

echo "== D3: lefthook only in the omakase cache heals — gates demonstrably run =="
R3="$TMP/d3"; newrepo "$R3"
H3="$TMP/d3-home"; rm -rf "$H3" "$TMP/lefthook-invocations.log"
write_stable_bin "$H3"
write_fake_lefthook "$H3/.cache/omakase/lefthook/$LH_VER/lefthook"
if try_commit "$R3" "$H3"; then
  pass "D3: commit succeeds with lefthook only in the omakase cache"
else
  fail "D3: commit blocked despite a usable cached lefthook (rc=$?): $(cat "$TMP/err")"
fi
grep -q "run pre-commit" "$TMP/lefthook-invocations.log" 2>/dev/null \
  && pass "D3: the cached lefthook actually RAN the hook (heal, not just unblock)" \
  || fail "D3: cached lefthook never invoked — fail-open skip still present"

echo "== D4: no lefthook anywhere blocks the commit (fail closed) =="
R4="$TMP/d4"; newrepo "$R4"
H4="$TMP/d4-home"; rm -rf "$H4"
write_stable_bin "$H4"
BEFORE4="$(commits_in "$R4")"
if try_commit "$R4" "$H4"; then
  fail "D4: commit SUCCEEDED with no lefthook anywhere — silent gate skip (fail-open)"
else
  pass "D4: commit refused with no lefthook anywhere (rc!=0)"
fi
grep -q "omakase: BLOCKING" "$TMP/err" && pass "D4: stderr carries the BLOCKING line" \
  || fail "D4: no BLOCKING line on stderr: $(cat "$TMP/err")"
grep -q "LEFTHOOK_BIN" "$TMP/err" && pass "D4: stderr names an escape hatch (LEFTHOOK_BIN)" \
  || fail "D4: stderr gives no recovery guidance: $(cat "$TMP/err")"
[ "$(commits_in "$R4")" = "$BEFORE4" ] && pass "D4: no commit was created" \
  || fail "D4: a commit landed despite the block"

echo "== D5: LEFTHOOK=0 skip is honored (no block on an empty machine) =="
if try_commit "$R4" "$H4" LEFTHOOK=0; then
  pass "D5: LEFTHOOK=0 commits cleanly with no lefthook anywhere (documented bypass)"
else
  fail "D5: LEFTHOOK=0 was blocked (rc=$?): $(cat "$TMP/err")"
fi

echo "== D6: LEFTHOOK=0 does NOT bypass the harness verify =="
REL="$(awk -F'\t' '$1 ~ /\.sh$/{print $1; exit}' "$R4/.git/omakase/placed.tsv")"
rm -f "$R4/$REL"
BEFORE6="$(commits_in "$R4")"
if try_commit "$R4" "$H4" LEFTHOOK=0; then
  fail "D6: commit passed over a wiped overlay under LEFTHOOK=0 — the verify is bypassable"
else
  pass "D6: wiped overlay still blocks under LEFTHOOK=0 (only --no-verify escapes)"
fi
grep -q "missing: $REL" "$TMP/err" && pass "D6: the block names the missing file" \
  || fail "D6: missing file not named: $(cat "$TMP/err")"
[ "$(commits_in "$R4")" = "$BEFORE6" ] && pass "D6: no commit was created" \
  || fail "D6: a commit landed despite the block"

echo "== D7: LEFTHOOK_BIN override resolves at tier 1 =="
R7="$TMP/d7"; newrepo "$R7"
H7="$TMP/d7-home"; rm -rf "$H7" "$TMP/lefthook-invocations.log"
write_stable_bin "$H7"
FAKE7="$TMP/d7-fake/lefthook"; write_fake_lefthook "$FAKE7"
if try_commit "$R7" "$H7" LEFTHOOK_BIN="$FAKE7"; then
  pass "D7: commit succeeds with LEFTHOOK_BIN set"
else
  fail "D7: LEFTHOOK_BIN run blocked (rc=$?): $(cat "$TMP/err")"
fi
grep -q "run pre-commit" "$TMP/lefthook-invocations.log" 2>/dev/null \
  && pass "D7: LEFTHOOK_BIN lefthook ran the hook" \
  || fail "D7: LEFTHOOK_BIN lefthook never invoked"

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
