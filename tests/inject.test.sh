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
post-checkout:
  jobs:
    - name: omakase-ensure-present
      run: bash "$(git rev-parse --git-common-dir)/omakase/ensure-present.sh"
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

# ---------- Scenario C: worktree auto-install ----------
# A fresh worktree has none of the gitignored harness files. init.sh snapshots the
# placed files into the shared git dir; the post-checkout job copies the MISSING
# ones into each worktree, never overwriting a local edit. (.worktreeinclude — the
# Claude-Code-native copy — can't be exercised from bash; tested live in pixterm.)
echo "== Scenario C: worktree auto-install =="
PAY="$TMP/payloadC"; REPO="$TMP/repoC"
mkpayload "$PAY"; newrepo "$REPO"
( cd "$REPO" && OMAKASE_PAYLOAD="$PAY" bash "$INIT" ) >/dev/null 2>&1
COMMON="$(cd "$REPO" && cd "$(git rev-parse --git-common-dir)" && pwd)"

# C1: init wrote the harness snapshot artifacts + a .worktreeinclude block, all out of git.
[ -x "$COMMON/omakase/ensure-present.sh" ] && pass "ensure-present.sh written (executable)" || fail "ensure-present.sh missing"
grep -q '.omakase/gates/example.sh' "$COMMON/omakase/placed.list" 2>/dev/null && pass "placed.list manifest written" || fail "placed.list missing/empty"
[ -f "$COMMON/omakase/payload-snapshot/.omakase/gates/example.sh" ] && pass "payload snapshot captured the gate" || fail "snapshot missing the gate"
grep -q "omakase-harness" "$REPO/.worktreeinclude" 2>/dev/null && pass ".worktreeinclude block written" || fail ".worktreeinclude block missing"
[ -z "$(cd "$REPO" && git status --porcelain)" ] && pass "git status still clean (harness artifacts out of git)" || { fail "status not clean after harness wiring"; (cd "$REPO" && git status --porcelain | sed 's/^/      /'); }

# C2: mechanism — a fresh linked worktree, run ensure-present.sh directly -> gate appears.
WT="$TMP/repoC-wt"
( cd "$REPO" && git worktree add -q "$WT" -b wtprobe ) 2>/dev/null
[ ! -e "$WT/.omakase/gates/example.sh" ] && pass "fresh worktree starts WITHOUT the gitignored harness" || fail "harness unexpectedly present in fresh worktree"
( cd "$WT" && bash "$COMMON/omakase/ensure-present.sh" )
[ -x "$WT/.omakase/gates/example.sh" ] && pass "ensure-present copied the missing gate into the worktree (executable)" || fail "ensure-present did not install the harness into the worktree"

# C3: never-overwrite — a local edit in the worktree survives a re-run.
echo 'LOCAL EDIT' > "$WT/.omakase/gates/example.sh"
( cd "$WT" && bash "$COMMON/omakase/ensure-present.sh" )
grep -q 'LOCAL EDIT' "$WT/.omakase/gates/example.sh" && pass "ensure-present never overwrites a local edit" || fail "ensure-present clobbered a local edit"

# C4: end-to-end self-heal — in a worktree that already has the harness (lefthook-local.yml present, as
# .worktreeinclude would copy it), deleting a gate then checking out restores it
# via the real lefthook post-checkout job.
cp "$PAY/lefthook-local.yml" "$WT/lefthook-local.yml"
rm -f "$WT/.omakase/gates/example.sh"
( cd "$WT" && git checkout -q -b wtprobe2 ) 2>/dev/null
[ -f "$WT/.omakase/gates/example.sh" ] && pass "post-checkout self-heal restored a deleted gate in a worktree that already has the harness" || fail "post-checkout did not self-heal"

( cd "$REPO" && git worktree remove --force "$WT" ) 2>/dev/null; ( cd "$REPO" && git worktree prune ) 2>/dev/null

# C5: remove tears the harness snapshot down too.
( cd "$REPO" && OMAKASE_PAYLOAD="$PAY" bash "$REMOVE" ) >/dev/null 2>&1
[ ! -e "$COMMON/omakase" ] && pass "remove deleted the shared snapshot" || fail "remove left the snapshot"
[ ! -e "$REPO/.worktreeinclude" ] && pass "remove deleted the .worktreeinclude block" || fail "remove left .worktreeinclude"

rm -rf "$TMP"
echo ""
[ "$FAILED" -eq 0 ] && echo "ALL PASS" || { echo "FAILURES PRESENT"; exit 1; }
