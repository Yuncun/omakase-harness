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

# ---------- Scenario D: payload symlinks are carried (CLAUDE.md -> AGENTS.md) ----------
# A payload symlink must land AS a symlink (cp -P), be snapshotted, and self-heal into
# a worktree. The old `find -type f` + plain `cp` skipped it / dereferenced it.
echo "== Scenario D: payload symlink carried as a symlink =="
PAY="$TMP/payloadD"; REPO="$TMP/repoD"
mkpayload "$PAY"
printf 'real doctrine\n' > "$PAY/AGENTS.md"
( cd "$PAY" && ln -s AGENTS.md CLAUDE.md )
newrepo "$REPO"
( cd "$REPO" && OMAKASE_PAYLOAD="$PAY" bash "$INIT" ) >/dev/null 2>&1
[ -L "$REPO/CLAUDE.md" ] && pass "payload symlink placed AS a symlink" || fail "symlink not carried (skipped or dereferenced)"
[ "$(readlink "$REPO/CLAUDE.md")" = "AGENTS.md" ] && pass "symlink target preserved" || fail "symlink target wrong"
COMMON="$(cd "$REPO" && cd "$(git rev-parse --git-common-dir)" && pwd)"
[ -L "$COMMON/omakase/payload-snapshot/CLAUDE.md" ] && pass "snapshot kept it a symlink" || fail "snapshot dereferenced the symlink"
[ -z "$(cd "$REPO" && git status --porcelain)" ] && pass "git status clean (symlink gitignored)" || { fail "status not clean (symlink)"; (cd "$REPO" && git status --porcelain | sed 's/^/      /'); }
WTD="$TMP/repoD-wt"
( cd "$REPO" && git worktree add -q "$WTD" -b wtdsym ) 2>/dev/null
( cd "$WTD" && bash "$COMMON/omakase/ensure-present.sh" )
[ -L "$WTD/CLAUDE.md" ] && pass "ensure-present self-healed the symlink into a worktree" || fail "ensure-present did not carry the symlink"
( cd "$REPO" && git worktree remove --force "$WTD" ) 2>/dev/null; ( cd "$REPO" && git worktree prune ) 2>/dev/null

# ---------- Scenario E: re-init never eats your edits; --force takes the update ----------
echo "== Scenario E: re-init keep / clean-update / --force =="
# E1 — you edited a placed gate; re-init KEEPS it.
PAY="$TMP/payloadE1"; REPO="$TMP/repoE1"; mkpayload "$PAY"; newrepo "$REPO"
( cd "$REPO" && OMAKASE_PAYLOAD="$PAY" bash "$INIT" ) >/dev/null 2>&1
echo 'MY EDIT' > "$REPO/.omakase/gates/example.sh"
OUT=$( cd "$REPO" && OMAKASE_PAYLOAD="$PAY" bash "$INIT" 2>&1 )
grep -q 'MY EDIT' "$REPO/.omakase/gates/example.sh" && pass "re-init KEPT the user's edited gate" || fail "re-init clobbered the user's edit"
echo "$OUT" | grep -qi 'kept' && pass "re-init reported the kept edit" || fail "re-init did not report a kept edit"
[ -z "$(cd "$REPO" && git status --porcelain)" ] && pass "git status still clean after a keep" || { fail "status not clean after keep"; (cd "$REPO" && git status --porcelain | sed 's/^/      /'); }
# E2 — you did NOT edit; payload changed; re-init takes the new version.
PAY="$TMP/payloadE2"; REPO="$TMP/repoE2"; mkpayload "$PAY"; newrepo "$REPO"
( cd "$REPO" && OMAKASE_PAYLOAD="$PAY" bash "$INIT" ) >/dev/null 2>&1
printf '#!/usr/bin/env bash\necho NEW-PAYLOAD-V2\nexit 0\n' > "$PAY/.omakase/gates/example.sh"
( cd "$REPO" && OMAKASE_PAYLOAD="$PAY" bash "$INIT" ) >/dev/null 2>&1
grep -q 'NEW-PAYLOAD-V2' "$REPO/.omakase/gates/example.sh" && pass "re-init took the new payload version (no local edit)" || fail "clean update did not apply"
# E3 — --force overrides a real edit.
PAY="$TMP/payloadE3"; REPO="$TMP/repoE3"; mkpayload "$PAY"; newrepo "$REPO"
( cd "$REPO" && OMAKASE_PAYLOAD="$PAY" bash "$INIT" ) >/dev/null 2>&1
echo 'EDIT TO BE FORCED OUT' > "$REPO/.omakase/gates/example.sh"
( cd "$REPO" && OMAKASE_PAYLOAD="$PAY" bash "$INIT" --force ) >/dev/null 2>&1
grep -q 'omakase-example-gate-ran' "$REPO/.omakase/gates/example.sh" && pass "--force overwrote the edit with the payload" || fail "--force did not overwrite"

# ---------- Scenario F: /omakase show renders the installed harness ----------
echo "== Scenario F: show renders the installed-but-invisible harness =="
SHOW="$HERE/../bin/show.sh"
PAY="$TMP/payloadF"; REPO="$TMP/repoF"; mkpayload "$PAY"; newrepo "$REPO"
OUT=$( cd "$REPO" && bash "$SHOW" 2>&1 )
echo "$OUT" | grep -qi 'No omakase harness' && pass "show reports empty state before init" || fail "show did not report empty state"
( cd "$REPO" && OMAKASE_PAYLOAD="$PAY" bash "$INIT" ) >/dev/null 2>&1
OUT=$( cd "$REPO" && bash "$SHOW" 2>&1 )
echo "$OUT" | grep -q 'PLACED FILES' && pass "show prints the placed-files map" || fail "show missing PLACED FILES"
echo "$OUT" | grep -q '.omakase/gates/example.sh' && pass "show lists the gate file" || fail "show did not list the gate"
echo "$OUT" | grep -q 'HIDDEN VIA' && pass "show lists what is gitignored" || fail "show missing the exclude section"
( cd "$REPO" && OMAKASE_PAYLOAD="$PAY" bash "$REMOVE" ) >/dev/null 2>&1

# ---------- Scenario G: the SHIPPED example gate blocks merge-conflict markers ----------
echo "== Scenario G: shipped example gate is real and generic =="
GATE="$HERE/../payload/.omakase/gates/example.sh"
REPO="$TMP/repoG"; newrepo "$REPO"
mkdir -p "$REPO/.omakase/gates"; cp "$GATE" "$REPO/.omakase/gates/example.sh"; chmod +x "$REPO/.omakase/gates/example.sh"
( cd "$REPO" && printf 'hello\n' > a.txt && git add a.txt )
( cd "$REPO" && bash .omakase/gates/example.sh ) >/dev/null 2>&1 && pass "example gate passes on clean staged input" || fail "example gate failed on clean input"
( cd "$REPO" && printf '<<<<<<< HEAD\nx\n=======\ny\n>>>>>>> branch\n' > b.txt && git add b.txt )
( cd "$REPO" && bash .omakase/gates/example.sh ) >/dev/null 2>&1 && fail "example gate did NOT block a conflict marker" || pass "example gate blocked a staged conflict marker"

rm -rf "$TMP"
echo ""
[ "$FAILED" -eq 0 ] && echo "ALL PASS" || { echo "FAILURES PRESENT"; exit 1; }
