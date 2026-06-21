#!/usr/bin/env bash
# Proof that tools/build.sh assembles a self-contained plugin bundle from ONE source:
# real files only (no symlinks), base machinery byte-identical (no drift), a stack's
# payload delta overlaid on the base payload, and source symlinks materialized real.
set -u
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BUILD="$HERE/../tools/build.sh"
SRC="$(cd "$HERE/.." && pwd)"
TMP="${TMPDIR:-/tmp}/omakase-build-test.$$"
FAILED=0
pass(){ echo "  PASS: $1"; }
fail(){ echo "  FAIL: $1"; FAILED=1; }
trap 'rm -rf "$TMP"' EXIT
mkdir -p "$TMP"

# ---------- generic bundle ----------
echo "== generic bundle =="
GEN="$TMP/generic"
bash "$BUILD" --out "$GEN" >/dev/null 2>&1 && pass "build generic exits 0" || fail "build generic failed"
[ -x "$GEN/bin/init.sh" ] && pass "machinery present + executable" || fail "no bin/init.sh"
[ -f "$GEN/commands/omakase.md" ] && pass "claude front door present" || fail "no commands/omakase.md"
[ -f "$GEN/.claude-plugin/plugin.json" ] && pass "plugin.json present" || fail "no plugin.json"
grep -q '"name": "omakase-harness"' "$GEN/.claude-plugin/plugin.json" && pass "generic plugin name" || fail "wrong plugin name"
[ -f "$GEN/payload/lefthook-local.yml" ] && pass "base payload wiring present" || fail "no payload wiring"
[ -f "$GEN/payload/.omakase/gates/deferred-check.sh" ] && pass "base gate present" || fail "no base gate"
[ -z "$(find "$GEN" -type l)" ] && pass "zero symlinks (real files only)" || fail "bundle has symlinks"
diff -r "$SRC/bin" "$GEN/bin" >/dev/null 2>&1 && pass "machinery byte-identical to source (no drift)" || fail "machinery differs from source"

# ---------- stack overlay ----------
echo "== stack overlay =="
STK="$TMP/stack-foo"
mkdir -p "$STK/payload/.omakase/gates"
printf '#!/usr/bin/env bash\nexit 0\n' > "$STK/payload/.omakase/gates/foo-gate.sh"
printf '# foo stack wiring\npre-commit:\n  jobs: []\n' > "$STK/payload/lefthook-local.yml"
printf '{ "name": "foo-harness", "version": "0.0.1", "commands": "./commands" }\n' > "$STK/plugin.json"
FOO="$TMP/foo"
bash "$BUILD" --out "$FOO" --stack "$STK" >/dev/null 2>&1 && pass "build stack exits 0" || fail "build stack failed"
[ -f "$FOO/payload/.omakase/gates/foo-gate.sh" ] && pass "stack delta gate added" || fail "stack gate missing"
[ -f "$FOO/payload/.omakase/gates/deferred-check.sh" ] && pass "base scaffold retained under stack" || fail "base scaffold lost"
grep -q 'foo stack wiring' "$FOO/payload/lefthook-local.yml" && pass "stack overrides base wiring" || fail "wiring not overridden"
grep -q '"name": "foo-harness"' "$FOO/.claude-plugin/plugin.json" && pass "stack plugin.json used" || fail "stack plugin.json not used"
[ -x "$FOO/bin/init.sh" ] && pass "machinery present in stack bundle" || fail "no machinery in stack bundle"

# ---------- source symlinks materialized to real files ----------
echo "== symlinks dereferenced to real files =="
SLK="$TMP/stack-link"
mkdir -p "$SLK/payload/.claude"
printf 'real advisory content\n' > "$SLK/payload/AGENTS.md"
( cd "$SLK/payload/.claude" && ln -s ../AGENTS.md CLAUDE.md )   # a symlink in the source
LNK="$TMP/linkout"
bash "$BUILD" --out "$LNK" --stack "$SLK" >/dev/null 2>&1 && pass "build with a symlinked source exits 0" || fail "build with symlink failed"
{ [ -f "$LNK/payload/.claude/CLAUDE.md" ] && [ ! -L "$LNK/payload/.claude/CLAUDE.md" ]; } && pass "source symlink materialized as a real file" || fail "symlink not materialized"
grep -q 'real advisory content' "$LNK/payload/.claude/CLAUDE.md" && pass "materialized file has the target content" || fail "materialized content wrong"
[ -z "$(find "$LNK" -type l)" ] && pass "no symlinks survive into the bundle" || fail "symlink survived"

# ---------- wiring guard: a missing referenced script fails the build ----------
echo "== wiring guard =="
BAD="$TMP/stack-bad"
mkdir -p "$BAD/payload"
printf 'pre-commit:\n  jobs:\n    - name: x\n      run: bash .omakase/gates/nonexistent.sh\n' > "$BAD/payload/lefthook-local.yml"
BADOUT="$TMP/badout"
bash "$BUILD" --out "$BADOUT" --stack "$BAD" >/dev/null 2>&1 && fail "build should reject missing wiring ref" || pass "build fails when wiring references a missing script"
[ ! -e "$BADOUT" ] && pass "no partial bundle left at --out on failure" || fail "partial bundle left behind"

# ---------- atomic: a dangling symlink fails without clobbering --out ----------
echo "== atomic build on failure =="
GOOD="$TMP/keep"; bash "$BUILD" --out "$GOOD" >/dev/null 2>&1   # a prior good bundle
DANG="$TMP/stack-dangling"
mkdir -p "$DANG/payload"
( cd "$DANG/payload" && ln -s does-not-exist.md AGENTS.md )     # dangling symlink in source
bash "$BUILD" --out "$GOOD" --stack "$DANG" >/dev/null 2>&1 && fail "build should fail on a dangling symlink" || pass "build fails on a dangling symlink"
[ -x "$GOOD/bin/init.sh" ] && pass "existing --out left intact when a rebuild fails" || fail "failed rebuild clobbered --out"

echo
[ "$FAILED" -eq 0 ] && echo "build.test.sh: ALL PASS" || echo "build.test.sh: FAILURES"
exit "$FAILED"
