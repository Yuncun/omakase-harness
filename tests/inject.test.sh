#!/usr/bin/env bash
# Proof that init.sh is a zero-footprint additive overlay and remove.sh reverses it.
set -u
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
INIT="$HERE/../bin/init.sh"
REMOVE="$HERE/../bin/remove.sh"
LEFTHOOK="${LEFTHOOK_BIN:-/Users/ericshen/Claude/pixterm-engine/node_modules/.bin/lefthook}"
TMP="${TMPDIR:-/tmp}/omakase-inject-test.$$"
FAILED=0
pass(){ echo "  PASS: $1"; }
fail(){ echo "  FAIL: $1"; FAILED=1; }

mkpayload(){ # $1 = payload dir
  local p="$1"
  mkdir -p "$p/.omakase/gates"
  cat > "$p/.omakase/gates/example.sh" <<'SH'
#!/usr/bin/env bash
echo "omakase-example-gate-ran"
exit 0
SH
  cat > "$p/lefthook-local.yml" <<'YML'
pre-commit:
  jobs:
    - name: omakase-example
      run: bash .omakase/gates/example.sh
YML
}

newrepo(){ rm -rf "$1"; mkdir -p "$1"; ( cd "$1" && git init -q && git config user.email t@t && git config user.name t && git config commit.gpgsign false && git commit -q --allow-empty -m init ); }

export PATH="$(dirname "$LEFTHOOK"):$PATH"

# ---------- Scenario A: clean repo, no harness ----------
echo "== Scenario A: additive into a repo with no harness =="
PAY="$TMP/payloadA"; REPO="$TMP/repoA"
mkpayload "$PAY"; newrepo "$REPO"
( cd "$REPO" && OMAKASE_PAYLOAD="$PAY" bash "$INIT" ) >/dev/null 2>&1

[ -f "$REPO/.omakase/gates/example.sh" ] && pass "payload file placed at real path" || fail "payload file not placed"
[ -x "$REPO/.omakase/gates/example.sh" ] && pass "placed .sh is executable" || fail ".sh not executable"
grep -q "omakase-harness" "$REPO/.git/info/exclude" && pass "exclude block written" || fail "no exclude block"
[ -z "$(cd "$REPO" && git status --porcelain)" ] && pass "git status clean (zero footprint)" || { fail "git status NOT clean"; (cd "$REPO" && git status --porcelain | sed 's/^/      /'); }
OUT=$(cd "$REPO" && echo x > f.txt && git add f.txt 2>/dev/null; git commit -m t 2>&1); echo "$OUT" | grep -q "omakase-example-gate-ran" && pass "gate fired on commit" || { fail "gate did not fire"; echo "$OUT" | sed 's/^/      /'; }

( cd "$REPO" && OMAKASE_PAYLOAD="$PAY" bash "$REMOVE" ) >/dev/null 2>&1
[ ! -e "$REPO/.omakase" ] && pass "remove deleted placed tree" || fail "remove left files"
grep -q "omakase-harness" "$REPO/.git/info/exclude" && fail "remove left exclude block" || pass "remove stripped exclude block"

# ---------- Scenario B: repo already commits AGENTS.md + lefthook.yml ----------
echo "== Scenario B: collisions skipped, committed files untouched =="
PAY="$TMP/payloadB"; REPO="$TMP/repoB"
mkpayload "$PAY"
printf 'team agents\n' > "$PAY/AGENTS.md"   # colliding singleton in the payload
newrepo "$REPO"
( cd "$REPO" && printf 'COMMITTED team agents\n' > AGENTS.md && cat > lefthook.yml <<'YML'
pre-commit:
  jobs:
    - name: team-noop
      run: 'true'
YML
git add AGENTS.md lefthook.yml && git commit -q -m team )
( cd "$REPO" && OMAKASE_PAYLOAD="$PAY" bash "$INIT" ) >/dev/null 2>&1

grep -q "COMMITTED team agents" "$REPO/AGENTS.md" && pass "committed AGENTS.md NOT overwritten" || fail "AGENTS.md was overwritten"
( cd "$REPO" && git diff --quiet HEAD -- AGENTS.md lefthook.yml ) && pass "committed AGENTS.md + lefthook.yml diff clean" || fail "committed files changed"
[ -f "$REPO/lefthook-local.yml" ] && pass "lefthook-local.yml placed (additive)" || fail "lefthook-local.yml missing"
[ -z "$(cd "$REPO" && git status --porcelain)" ] && pass "git status clean with committed harness present" || { fail "status not clean"; (cd "$REPO" && git status --porcelain | sed 's/^/      /'); }
OUT=$(cd "$REPO" && echo x > g.txt && git add g.txt 2>/dev/null; git commit -m t 2>&1); echo "$OUT" | grep -q "omakase-example-gate-ran" && pass "personal gate fires alongside committed team config" || { fail "personal gate did not fire"; echo "$OUT" | sed 's/^/      /'; }

rm -rf "$TMP"
echo ""
[ "$FAILED" -eq 0 ] && echo "ALL PASS" || { echo "FAILURES PRESENT"; exit 1; }
