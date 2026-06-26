#!/usr/bin/env bash
# Proof that each verb skill's run.sh — the shared Claude Code / Copilot CLI / plain-shell entry
# layer — self-locates ../../bin and execs the RIGHT base-harness script, forwarding "$@" with
# argument boundaries intact. Stub bin/ scripts in a temp plugin layout isolate the contract from
# the real bin behavior (these 6-8 line files are the exact cross-tool seam; a broken relative
# path or a dropped/collapsed "$@" would otherwise ship green). add-gate has no run.sh (it is a
# pure instruction skill), so only the four exec front doors are exercised.
set -u
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SKILLS="$(cd "$HERE/../skills" && pwd)"
TMP="${TMPDIR:-/tmp}/omakase-frontdoor-test.$$"
FAILED=0
pass(){ echo "  PASS: $1"; }
fail(){ echo "  FAIL: $1"; FAILED=1; }
trap 'rm -rf "$TMP"' EXIT
mkdir -p "$TMP/plugin/bin" "$TMP/plugin/skills"

# Stub every bin script a run.sh might exec: echo a marker, then each arg on its own line so a
# collapsed "$*" (which would join 'be ta' into the prior arg) is caught.
for s in init show remove share; do
  printf '#!/usr/bin/env bash\necho "STUB:%s"\nfor a in "$@"; do echo "ARG[$a]"; done\n' "$s" > "$TMP/plugin/bin/$s.sh"
  chmod +x "$TMP/plugin/bin/$s.sh"
done

# Copy each real run.sh into the temp plugin tree; it self-locates ../../bin -> our stubs.
for v in init status remove share; do
  mkdir -p "$TMP/plugin/skills/$v"
  cp "$SKILLS/$v/run.sh" "$TMP/plugin/skills/$v/run.sh"
done
run(){ bash "$TMP/plugin/skills/$1/run.sh" "${@:2}"; }

echo "== run.sh front doors reach the right bin script and forward args =="
# init -> bin/init.sh, forwards "$@" verbatim (incl. a spaced arg)
OUT=$(run init alpha "be ta")
echo "$OUT" | grep -qxF 'STUB:init'  && pass "init/run.sh execs bin/init.sh"   || fail "init reached wrong script ($OUT)"
{ echo "$OUT" | grep -qxF 'ARG[alpha]' && echo "$OUT" | grep -qxF 'ARG[be ta]'; } && pass "init forwards args with boundaries intact" || fail "init dropped or split args ($OUT)"
# remove -> bin/remove.sh, forwards "$@"
OUT=$(run remove --keep "x y")
echo "$OUT" | grep -qxF 'STUB:remove' && pass "remove/run.sh execs bin/remove.sh" || fail "remove reached wrong script ($OUT)"
{ echo "$OUT" | grep -qxF 'ARG[--keep]' && echo "$OUT" | grep -qxF 'ARG[x y]'; } && pass "remove forwards args" || fail "remove dropped args ($OUT)"
# share -> bin/share.sh, forwards "$@"
OUT=$(run share team-rig)
echo "$OUT" | grep -qxF 'STUB:share'  && pass "share/run.sh execs bin/share.sh"  || fail "share reached wrong script ($OUT)"
echo "$OUT" | grep -qxF 'ARG[team-rig]' && pass "share forwards its name arg"     || fail "share dropped its arg ($OUT)"
# status -> bin/show.sh --markdown (FIXED args; read-only inventory — must NOT forward caller args).
# Pass a sentinel to prove BOTH halves: --markdown IS sent, and a caller arg is NOT leaked into the
# read-only render (a regression that began forwarding "$@" here would otherwise ship green).
OUT=$(run status SHOULD_NOT_APPEAR)
echo "$OUT" | grep -qxF 'STUB:show'       && pass "status/run.sh execs bin/show.sh"    || fail "status reached wrong script ($OUT)"
echo "$OUT" | grep -qxF 'ARG[--markdown]' && pass "status passes the fixed --markdown" || fail "status did not pass --markdown ($OUT)"
echo "$OUT" | grep -qxF 'ARG[SHOULD_NOT_APPEAR]' && fail "status forwarded a caller arg into the read-only render" || pass "status does not forward caller args (fixed --markdown only)"

echo ""
[ "$FAILED" -eq 0 ] && echo "skill-frontdoor.test.sh: ALL PASS" || { echo "skill-frontdoor.test.sh: FAILURES"; exit 1; }
