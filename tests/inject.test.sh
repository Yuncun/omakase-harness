#!/usr/bin/env bash
# Proof that init.sh is a zero-footprint additive overlay and remove.sh reverses it.
set -u
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
INIT="$HERE/../bin/init.sh"
REMOVE="$HERE/../bin/remove.sh"
LEFTHOOK="${LEFTHOOK_BIN:-$(command -v lefthook || true)}"
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
# Claude-Code-native copy — can't be exercised from bash; tested live in a real project.)
echo "== Scenario C: worktree auto-install =="
PAY="$TMP/payloadC"; REPO="$TMP/repoC"
mkpayload "$PAY"; newrepo "$REPO"
( cd "$REPO" && OMAKASE_PAYLOAD="$PAY" bash "$INIT" ) >/dev/null 2>&1
COMMON="$(cd "$REPO" && cd "$(git rev-parse --git-common-dir)" && pwd)"

# C1: init wrote the harness snapshot artifacts + a .worktreeinclude block, all out of git.
[ -x "$COMMON/omakase/ensure-present.sh" ] && pass "ensure-present.sh written (executable)" || fail "ensure-present.sh missing"
grep -q '.omakase/gates/example.sh' "$COMMON/omakase/placed.tsv" 2>/dev/null && pass "placed.tsv provenance ledger written" || fail "placed.tsv missing/empty"
[ -f "$COMMON/omakase/payload-snapshot/.omakase/gates/example.sh" ] && pass "payload snapshot captured the gate" || fail "snapshot missing the gate"
grep -q "omakase-harness" "$REPO/.worktreeinclude" 2>/dev/null && pass ".worktreeinclude block written" || fail ".worktreeinclude block missing"
[ -z "$(cd "$REPO" && git status --porcelain)" ] && pass "git status still clean (harness artifacts out of git)" || { fail "status not clean after harness wiring"; (cd "$REPO" && git status --porcelain | sed 's/^/      /'); }

# C2: mechanism — a fresh linked worktree now AUTO-self-heals on `git worktree add` (the
# worktree-bootstrap block runs ensure-present.sh from the shared post-checkout stub), so
# the gate is present immediately; a manual re-run of ensure-present is idempotent.
WT="$TMP/repoC-wt"
( cd "$REPO" && git worktree add -q "$WT" -b wtprobe ) >/dev/null 2>&1
[ -x "$WT/.omakase/gates/example.sh" ] && pass "fresh worktree auto-self-healed the gate on add (worktree-bootstrap)" || fail "fresh worktree did not auto-self-heal the gate"
( cd "$WT" && bash "$COMMON/omakase/ensure-present.sh" )
[ -x "$WT/.omakase/gates/example.sh" ] && pass "ensure-present re-run is idempotent (gate still present, executable)" || fail "ensure-present disturbed an already-present gate"

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

# C4b: REGRESSION — a bare `git worktree add` self-heals with NO hand-holding (no manual
# ensure-present, no pre-seeded lefthook-local.yml). This is the worktree-bootstrap fix:
# a fresh worktree has no gitignored lefthook config, so lefthook's post-checkout job
# no-ops; the harness only materializes because install-guards.sh injected a
# worktree-bootstrap block into the SHARED post-checkout stub that runs ensure-present.sh
# directly. Pre-fix this gate would be ABSENT after the add (the "harness incomplete" bug).
PCHOOK="$COMMON/hooks/post-checkout"
grep -qF "omakase-harness worktree-bootstrap" "$PCHOOK" 2>/dev/null && pass "worktree-bootstrap block injected into the shared post-checkout stub" || fail "post-checkout stub missing the worktree-bootstrap block"
WTB="$TMP/repoC-wtbare"
( cd "$REPO" && git worktree add -q "$WTB" -b wtbare ) >/dev/null 2>&1
[ -x "$WTB/.omakase/gates/example.sh" ] && pass "bare 'git worktree add' self-healed the gate (no manual ensure-present)" || fail "bare worktree did NOT self-heal — harness incomplete"
[ -f "$WTB/lefthook-local.yml" ] && pass "bare worktree self-healed the lefthook config too" || fail "bare worktree missing lefthook-local.yml after self-heal"
( cd "$REPO" && git worktree remove --force "$WTB" ) 2>/dev/null; ( cd "$REPO" && git worktree prune ) 2>/dev/null

# C5: remove tears the harness snapshot down too.
( cd "$REPO" && OMAKASE_PAYLOAD="$PAY" bash "$REMOVE" ) >/dev/null 2>&1
[ ! -e "$COMMON/omakase" ] && pass "remove deleted the shared snapshot" || fail "remove left the snapshot"
[ ! -e "$REPO/.worktreeinclude" ] && pass "remove deleted the .worktreeinclude block" || fail "remove left .worktreeinclude"
grep -qF "omakase-harness worktree-bootstrap" "$COMMON/hooks/post-checkout" 2>/dev/null && fail "remove left the worktree-bootstrap block in post-checkout" || pass "remove stripped the worktree-bootstrap block from post-checkout"

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

# ---------- Scenario E: re-init always matches payload — overwrites divergent files + warns ----------
echo "== Scenario E: re-init overwrites a changed injected file and warns =="
# E1 — you edited a placed gate in place; re-init OVERWRITES it back to payload and warns.
PAY="$TMP/payloadE1"; REPO="$TMP/repoE1"; mkpayload "$PAY"; newrepo "$REPO"
( cd "$REPO" && OMAKASE_PAYLOAD="$PAY" bash "$INIT" ) >/dev/null 2>&1
echo 'MY EDIT' > "$REPO/.omakase/gates/example.sh"
OUT=$( cd "$REPO" && OMAKASE_PAYLOAD="$PAY" bash "$INIT" 2>&1 )
grep -q 'omakase-example-gate-ran' "$REPO/.omakase/gates/example.sh" && pass "re-init overwrote the edited gate back to payload" || fail "re-init did not overwrite the edit"
grep -q 'MY EDIT' "$REPO/.omakase/gates/example.sh" && fail "re-init left the local edit in place" || pass "the local edit was replaced"
echo "$OUT" | grep -qi 'overwrote' && pass "re-init warned that it overwrote a changed file" || fail "re-init did not warn about the overwrite"
[ -z "$(cd "$REPO" && git status --porcelain)" ] && pass "git status still clean after an overwrite" || { fail "status not clean after overwrite"; (cd "$REPO" && git status --porcelain | sed 's/^/      /'); }
# E2 — payload changed upstream; re-init takes the new version (same overwrite path).
PAY="$TMP/payloadE2"; REPO="$TMP/repoE2"; mkpayload "$PAY"; newrepo "$REPO"
( cd "$REPO" && OMAKASE_PAYLOAD="$PAY" bash "$INIT" ) >/dev/null 2>&1
printf '#!/usr/bin/env bash\necho NEW-PAYLOAD-V2\nexit 0\n' > "$PAY/.omakase/gates/example.sh"
( cd "$REPO" && OMAKASE_PAYLOAD="$PAY" bash "$INIT" ) >/dev/null 2>&1
grep -q 'NEW-PAYLOAD-V2' "$REPO/.omakase/gates/example.sh" && pass "re-init took the new payload version (upstream update)" || fail "upstream update did not apply"

# ---------- Scenario F: /omakase show renders the installed harness ----------
echo "== Scenario F: show renders the installed-but-invisible harness =="
SHOW="$HERE/../bin/show.sh"
PAY="$TMP/payloadF"; REPO="$TMP/repoF"; mkpayload "$PAY"; newrepo "$REPO"
OUT=$( cd "$REPO" && bash "$SHOW" 2>&1 )
echo "$OUT" | grep -qi 'No omakase harness' && pass "show reports empty state before init" || fail "show did not report empty state"
( cd "$REPO" && OMAKASE_PAYLOAD="$PAY" bash "$INIT" ) >/dev/null 2>&1
OUT=$( cd "$REPO" && bash "$SHOW" 2>&1 )
echo "$OUT" | grep -q 'INJECTED (omakase)' && pass "show prints the placed files as the Injected group" || fail "show missing INJECTED group"
INJ="$(echo "$OUT" | awk '/^INJECTED \(omakase\)/{f=1;next} /^PERSONAL \(global\)/{f=0} f')"
echo "$INJ" | grep -q 'lefthook-local.yml' && pass "show lists an injected harness file in the Injected group" || fail "show did not list the injected file in the Injected group"
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
# a lone ======= line is a Markdown/RST heading underline, NOT a conflict — must not block
( cd "$REPO" && git rm -q --cached b.txt && rm -f b.txt && printf 'My Title\n=======\n\nbody\n' > c.md && git add c.md )
( cd "$REPO" && bash .omakase/gates/example.sh ) >/dev/null 2>&1 && pass "example gate does not false-block a ======= heading underline" || fail "example gate false-blocked a Markdown heading underline"

rm -rf "$TMP"
echo ""
[ "$FAILED" -eq 0 ] && echo "ALL PASS" || { echo "FAILURES PRESENT"; exit 1; }
