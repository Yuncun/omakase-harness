#!/usr/bin/env bash
# Phase 0 compat contract: the HOOK-TIME READERS — the two scripts init.sh generates
# into $OMK ($(git rev-parse --git-common-dir)/omakase): ensure-present.sh (self-heal;
# bash, 3.2 floor) and verify-overlay.sh (fail-closed guard; POSIX sh). These stay
# shell forever (never the Go binary) and READ the state the future Go writer will
# WRITE, so this file pins their reading/healing contract — exercised today with
# bash-written state (docs/v2-design.md §1, §5, §10, §13).
# Every "contract capture" note below freezes OBSERVED v1 behavior deliberately: it
# records what the readers do today, not new policy.
# Scenarios:
#   V1/H heal + intact — verify-overlay exits 0 on the intact overlay; a DELETED
#      placed file heals back (content = the ledgered sha256, *.sh executable again);
#      a MODIFIED placed file is left as-is with a warn-only DRIFT line naming it
#   V2/V3 fail-closed — a drifted-but-present file never blocks (presence-only);
#      a missing unhealed file blocks with a non-zero exit naming the path
#   S  symlink heal — --source install: the deleted CLAUDE.md -> AGENTS.md placed
#      symlink returns AS a symlink with the same readlink target, never a
#      dereferenced regular file
#   D  digest parity — where BOTH shasum and sha256sum exist they must agree on a
#      regular file and on a symlink-target string (Global Constraint 5); else SKIP
# NOT re-asserted here (behavioral coverage elsewhere):
#   enabled=0 semantics, spaced paths, symlink round-trip via OMAKASE_PAYLOAD
#                                            — tests/placed.test.sh
#   tracked-collision warning, commit unblocks after restore — tests/safety.test.sh
#   worktree self-heal, local-edit content survives — tests/inject.test.sh
#   placed.tsv / ledger.tsv column FORMAT     — tests/state-format.test.sh
#   exclude-block bytes + $OMK layout         — tests/golden-state.test.sh
set -u
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
INIT="$HERE/../bin/init.sh"
LEFTHOOK="${LEFTHOOK_BIN:-$(command -v lefthook || true)}"
TMP="${TMPDIR:-/tmp}/omakase-state-readers-test.$$"
FAILED=0
pass(){ echo "  PASS: $1"; }
fail(){ echo "  FAIL: $1"; FAILED=1; }

# Same digest detection the implementation uses (shasum on macOS, sha256sum on Linux).
if command -v shasum >/dev/null 2>&1; then sha_file(){ shasum -a 256 < "$1" | awk '{print $1}'; }
else sha_file(){ sha256sum < "$1" | awk '{print $1}'; }; fi

newrepo(){ rm -rf "$1"; mkdir -p "$1"; ( cd "$1" && git init -q && git config user.email t@t && git config user.name t && git config commit.gpgsign false && git commit -q --allow-empty -m init ); }
common_of(){ echo "$(cd "$1" && cd "$(git rev-parse --git-common-dir)" && pwd)"; }
col(){ awk -F'\t' -v p="$2" -v c="$3" '$1==p{print $c; exit}' "$1"; }   # $1=placed.tsv $2=path $3=column

export PATH="$(dirname "$LEFTHOOK"):$PATH"
mkdir -p "$TMP"

# ---------- shared plain-init fixture for V (fail-closed) + H (heal) ----------
REPO="$TMP/repoHV"; newrepo "$REPO"
( cd "$REPO" && bash "$INIT" ) >/dev/null 2>&1 || fail "setup: plain init exited non-zero"
OMK="$(common_of "$REPO")/omakase"
PLACED="$OMK/placed.tsv"
# One *.sh row to heal (it also pins the exec-bit contract) and a DIFFERENT row to
# later leave missing for the fail-closed check.
REL="$(awk -F'\t' '$1 ~ /\.sh$/{print $1; exit}' "$PLACED" 2>/dev/null)"
REL2="$(awk -F'\t' -v skip="$REL" '$1!=skip{print $1; exit}' "$PLACED" 2>/dev/null)"
{ [ -n "$REL" ] && [ -n "$REL2" ]; } || fail "setup: could not pick two placed rows from $PLACED"

echo "== V1: verify-overlay exits 0 on the intact overlay =="
OUT="$( cd "$REPO" && "$OMK/verify-overlay.sh" 2>&1 )"; RC=$?
[ "$RC" -eq 0 ] && pass "V1: intact overlay -> exit 0" || fail "V1: intact overlay exited $RC ($OUT)"

echo "== H1: a DELETED placed file heals back from the snapshot =="
rm "$REPO/$REL"
OUT="$( cd "$REPO" && "$OMK/ensure-present.sh" 2>&1 )"; RC=$?
[ "$RC" -eq 0 ] && pass "H1: heal run exits 0" || fail "H1: heal run exited $RC ($OUT)"
if [ -f "$REPO/$REL" ]; then
  pass "H1: deleted file restored ($REL)"
  # Content contract: the healed copy hashes back to the LEDGERED sha256 — the heal
  # restores canonical content, not merely "a" file (catches a corrupt snapshot too).
  [ "$(sha_file "$REPO/$REL")" = "$(col "$PLACED" "$REL" 4)" ] \
    && pass "H1: healed content matches the ledgered sha256" \
    || fail "H1: healed content does not hash to the ledger row's sha256"
  # contract capture (v1 bash): the heal re-applies the exec bit to a healed *.sh
  # (cp from the snapshot then chmod +x), so a healed gate is still runnable.
  [ -x "$REPO/$REL" ] && pass "H1: healed *.sh is executable again" || fail "H1: healed $REL lost its exec bit"
else
  fail "H1: $REL still missing after ensure-present"
fi

echo "== H2: a MODIFIED placed file is left as-is, drift is warn-only (contract capture) =="
printf 'extra local edit\n' >> "$REPO/$REL"
cp "$REPO/$REL" "$TMP/H2.before"
OUT="$( cd "$REPO" && "$OMK/ensure-present.sh" 2>&1 )"; RC=$?
# contract capture (observed on v1 bash, 2026-07): a present-but-MODIFIED placed file
# is NEVER overwritten — the heal fills MISSING files only (never-clobber). The run
# still exits 0, and the drift is SURFACED as exactly one stderr WARNING line per
# drifted path, containing the word DRIFTED and the quoted path. The warning repeats
# on EVERY run while the drift persists (it is not a one-shot notice).
[ "$RC" -eq 0 ] && pass "H2: heal run over a drifted file still exits 0" || fail "H2: exited $RC on a drifted file ($OUT)"
cmp -s "$TMP/H2.before" "$REPO/$REL" \
  && pass "H2: drifted copy left byte-identical (never overwritten)" \
  || fail "H2: ensure-present changed the drifted copy"
printf '%s\n' "$OUT" | grep 'DRIFTED' | grep -qF "'$REL'" \
  && pass "H2: one WARNING line says DRIFTED and names the path" \
  || fail "H2: no DRIFTED warning naming $REL (output: $OUT)"
[ "$(printf '%s\n' "$OUT" | grep -c 'DRIFTED')" -eq 1 ] \
  && pass "H2: exactly one DRIFTED line for the one drifted path" \
  || fail "H2: expected exactly 1 DRIFTED line ($OUT)"
OUT="$( cd "$REPO" && "$OMK/ensure-present.sh" 2>&1 )"
printf '%s\n' "$OUT" | grep -q 'DRIFTED' \
  && pass "H2: the warning repeats on the next run (not once-only)" \
  || fail "H2: second run stayed silent about the persisting drift"

echo "== V2: a drifted-but-PRESENT file never blocks (contract capture) =="
# contract capture (v1 bash): verify-overlay is PRESENCE-only — it blocks on a
# missing enabled path, never on content drift. Drift surfaces exclusively through
# ensure-present's warning above (H2); the fail-closed gate does not consume sha256.
OUT="$( cd "$REPO" && "$OMK/verify-overlay.sh" 2>&1 )"; RC=$?
[ "$RC" -eq 0 ] && pass "V2: exit 0 with $REL still drifted on disk" || fail "V2: drift blocked ($RC: $OUT)"

echo "== V3: a missing unhealed file fails closed, naming the path =="
rm "$REPO/$REL2"
OUT="$( cd "$REPO" && "$OMK/verify-overlay.sh" 2>&1 )"; RC=$?
# Observed exit code is 1; the frozen contract is non-zero = block.
[ "$RC" -ne 0 ] && pass "V3: missing placed file -> non-zero exit (fail-closed)" || fail "V3: verify-overlay passed with $REL2 missing"
printf '%s\n' "$OUT" | grep -qF "missing: $REL2" \
  && pass "V3: message names the missing path ('missing: $REL2')" \
  || fail "V3: message does not name the path (output: $OUT)"
printf '%s\n' "$OUT" | grep -q 'ensure-present.sh' \
  && pass "V3: message gives the restore instruction (ensure-present.sh)" \
  || fail "V3: no restore instruction in the block message (output: $OUT)"

# ---------- S: symlink heal through a --source install ----------
echo "== S: a deleted placed symlink heals back AS a symlink, same target =="
FAKEHOME="$TMP/home"; CACHEHOME="$TMP/cache"; mkdir -p "$FAKEHOME" "$CACHEHOME"
SRC="$TMP/src-harness"; rm -rf "$SRC"; mkdir -p "$SRC/payload"
( cd "$SRC" && git init -q && git config user.email t@t && git config user.name t && git config commit.gpgsign false )
printf 'shared agent instructions\n' > "$SRC/payload/AGENTS.md"
( cd "$SRC/payload" && ln -s AGENTS.md CLAUDE.md )
printf 'name: state-readers-fixture\n' > "$SRC/omakase.manifest"
( cd "$SRC" && git add -A && git commit -q -m harness )
SRC="$(cd "$SRC" && pwd)"   # init absolutizes local dir sources (macOS TMPDIR carries a trailing slash)
REPOS="$TMP/repoS"; newrepo "$REPOS"
( cd "$REPOS" && HOME="$FAKEHOME" XDG_CACHE_HOME="$CACHEHOME" bash "$INIT" --source "$SRC" ) >/dev/null 2>&1 \
  || fail "S: --source init exited non-zero"
OMKS="$(common_of "$REPOS")/omakase"
[ -L "$REPOS/CLAUDE.md" ] && pass "S: init placed the payload symlink — the heal below is meaningful" \
  || fail "S: init did not place CLAUDE.md as a symlink"
TARGET="$(readlink "$REPOS/CLAUDE.md" 2>/dev/null)"
rm -f "$REPOS/CLAUDE.md"
OUT="$( cd "$REPOS" && "$OMKS/ensure-present.sh" 2>&1 )"; RC=$?
[ "$RC" -eq 0 ] && pass "S: heal run exits 0" || fail "S: heal run exited $RC ($OUT)"
[ -L "$REPOS/CLAUDE.md" ] \
  && pass "S: healed back AS a symlink (never a dereferenced regular file)" \
  || fail "S: CLAUDE.md came back as $(ls -l "$REPOS/CLAUDE.md" 2>&1 || echo 'nothing')"
[ "$(readlink "$REPOS/CLAUDE.md" 2>/dev/null)" = "$TARGET" ] \
  && pass "S: same readlink target ('$TARGET')" \
  || fail "S: readlink target changed ('$(readlink "$REPOS/CLAUDE.md" 2>/dev/null)' vs '$TARGET')"

# ---------- D: digest-tool parity (Global Constraint 5) ----------
echo "== D: shasum -a 256 and sha256sum agree (where both exist) =="
if command -v shasum >/dev/null 2>&1 && command -v sha256sum >/dev/null 2>&1; then
  PF="$TMP/parity.txt"; printf 'digest parity fixture\n' > "$PF"
  A="$(shasum -a 256 < "$PF" | awk '{print $1}')"
  B="$(sha256sum < "$PF" | awk '{print $1}')"
  [ "$A" = "$B" ] && pass "D: both tools agree on a regular file's content digest" \
    || fail "D: file digest differs (shasum $A vs sha256sum $B)"
  A="$(printf '%s' "AGENTS.md" | shasum -a 256 | awk '{print $1}')"
  B="$(printf '%s' "AGENTS.md" | sha256sum | awk '{print $1}')"
  [ "$A" = "$B" ] && pass "D: both tools agree on a symlink-target STRING digest ('AGENTS.md')" \
    || fail "D: target-string digest differs (shasum $A vs sha256sum $B)"
else
  echo "  SKIP: only one of shasum/sha256sum exists on this machine — CI's other OS covers digest parity"
fi

rm -rf "$TMP"
echo ""
[ "$FAILED" -eq 0 ] && echo "ALL PASS" || { echo "FAILURES PRESENT"; exit 1; }
