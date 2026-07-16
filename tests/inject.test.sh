#!/usr/bin/env bash
# Proof that init.sh is a zero-footprint additive overlay and remove.sh reverses it.
set -u
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
INIT="$HERE/../bin/init.sh"
OMAKASE="$( cd "$HERE/.." && HERE="$PWD/bin" && . bin/lib-omakase-bin.sh && resolve_omakase 2>/dev/null && echo "$OMAKASE_BIN_RESOLVED" )"
[ -n "$OMAKASE" ] || { echo "FATAL: no omakase binary resolvable"; exit 1; }
heal(){ ( cd "$1" && "$OMAKASE" hook post-checkout ); }
REMOVE="$HERE/../bin/remove.sh"
TMP="${TMPDIR:-/tmp}/omakase-inject-test.$$"
# Self-contained HOME + cache: init self-installs the resolved binary into
# $XDG_CACHE_HOME, and every real commit/checkout below fires that same copy
# through the permanent hook dispatchers (nothing touches the real machine).
export HOME="$TMP/home"; export XDG_CACHE_HOME="$TMP/cache"
mkdir -p "$HOME" "$XDG_CACHE_HOME"
FAILED=0
pass(){ echo "  PASS: $1"; }
fail(){ echo "  FAIL: $1"; FAILED=1; }

mkpayload(){ # $1 = payload dir
  local p="$1"
  mkdir -p "$p/.omakase/gates" "$p/.claude/rules"
  cat > "$p/.omakase/gates/example.sh" <<'SH'
#!/usr/bin/env bash
echo "omakase-example-gate-ran"
exit 0
SH
  # A non-machinery placed file so `omakase status` has a visible Injected row
  # (the .omakase/ tree and omakase.manifest are machinery — filtered as noise).
  printf 'a rule\n' > "$p/.claude/rules/style.md"
  # Gates are declared in omakase.manifest now (lefthook / omakase-gate.sh gone).
  cat > "$p/omakase.manifest" <<'MAN'
name: test
version: 1

gate: omakase-example
  hook: pre-commit
  run: bash .omakase/gates/example.sh
MAN
}

newrepo(){ rm -rf "$1"; mkdir -p "$1"; ( cd "$1" && git init -q && git config user.email t@t && git config user.name t && git config commit.gpgsign false && git commit -q --allow-empty -m init ); }

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

# ---------- Scenario B: repo commits its own lefthook.yml -> incumbent refusal ----------
# A repo that commits its own lefthook.yml is using lefthook natively. omakase no
# longer runs lefthook, so installing its dispatchers would displace the project's
# own hooks — init REFUSES (the gate module's one intended regression) and places
# nothing, leaving the committed config exactly in place.
echo "== Scenario B: a committed project lefthook.yml is an incumbent hook manager — init refuses =="
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
OUT=$( cd "$REPO" && OMAKASE_PAYLOAD="$PAY" bash "$INIT" 2>&1 ); rc=$?

[ "$rc" -ne 0 ] && pass "init REFUSES to install over a committed project lefthook.yml (exit 1)" || fail "init did not refuse the incumbent lefthook.yml (rc=$rc)"
echo "$OUT" | grep -q "lefthook.yml is git-tracked (the project's own lefthook config)" && pass "refusal names the git-tracked lefthook.yml incumbent" || { fail "refusal wording wrong"; echo "$OUT" | sed 's/^/      /'; }
grep -q "COMMITTED team agents" "$REPO/AGENTS.md" && pass "committed AGENTS.md NOT overwritten" || fail "AGENTS.md was overwritten"
( cd "$REPO" && git diff --quiet HEAD -- AGENTS.md lefthook.yml ) && pass "committed AGENTS.md + lefthook.yml diff clean" || fail "committed files changed"
[ ! -e "$REPO/omakase.manifest" ] && pass "refusal placed nothing (no omakase.manifest)" || fail "init placed omakase.manifest despite refusing"
[ ! -e "$REPO/.omakase" ] && pass "refusal placed nothing (no .omakase tree)" || fail "init placed the .omakase tree despite refusing"
[ -z "$(cd "$REPO" && git status --porcelain)" ] && pass "git status clean (refusal changed nothing)" || { fail "status not clean after refusal"; (cd "$REPO" && git status --porcelain | sed 's/^/      /'); }
grep -q "omakase-harness" "$REPO/.git/info/exclude" 2>/dev/null && fail "refusal wrote an exclude block" || pass "refusal wrote no exclude block"

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
grep -q 'omakase dispatcher' "$COMMON/hooks/post-checkout" 2>/dev/null && pass "post-checkout dispatcher written (the heal lives in the binary)" || fail "post-checkout dispatcher missing"
grep -q '.omakase/gates/example.sh' "$COMMON/omakase/placed.tsv" 2>/dev/null && pass "placed.tsv provenance ledger written" || fail "placed.tsv missing/empty"
[ -f "$COMMON/omakase/payload-snapshot/.omakase/gates/example.sh" ] && pass "payload snapshot captured the gate" || fail "snapshot missing the gate"
grep -q "omakase-harness" "$REPO/.worktreeinclude" 2>/dev/null && pass ".worktreeinclude block written" || fail ".worktreeinclude block missing"
[ -z "$(cd "$REPO" && git status --porcelain)" ] && pass "git status still clean (harness artifacts out of git)" || { fail "status not clean after harness wiring"; (cd "$REPO" && git status --porcelain | sed 's/^/      /'); }

# C2: mechanism — a fresh linked worktree AUTO-self-heals on `git worktree add`
# (the post-checkout dispatcher runs the binary's native heal), so the gate is
# present immediately; a manual re-run of the heal is idempotent.
WT="$TMP/repoC-wt"
( cd "$REPO" && git worktree add -q "$WT" -b wtprobe ) >/dev/null 2>&1
[ -x "$WT/.omakase/gates/example.sh" ] && pass "fresh worktree auto-self-healed the gate on add (post-checkout dispatcher)" || fail "fresh worktree did not auto-self-heal the gate"
heal "$WT" >/dev/null 2>&1
[ -x "$WT/.omakase/gates/example.sh" ] && pass "heal re-run is idempotent (gate still present, executable)" || fail "the heal disturbed an already-present gate"

# C3: never-overwrite — a local edit in the worktree survives a re-run.
echo 'LOCAL EDIT' > "$WT/.omakase/gates/example.sh"
heal "$WT" >/dev/null 2>&1
grep -q 'LOCAL EDIT' "$WT/.omakase/gates/example.sh" && pass "the heal never overwrites a local edit" || fail "the heal clobbered a local edit"

# C4: end-to-end self-heal — deleting a gate then checking out restores it via
# the real post-checkout dispatcher chain.
rm -f "$WT/.omakase/gates/example.sh"
( cd "$WT" && git checkout -q -b wtprobe2 ) 2>/dev/null
[ -f "$WT/.omakase/gates/example.sh" ] && pass "post-checkout self-heal restored a deleted gate in a worktree" || fail "post-checkout did not self-heal"

( cd "$REPO" && git worktree remove --force "$WT" ) 2>/dev/null; ( cd "$REPO" && git worktree prune ) 2>/dev/null

# C4b: REGRESSION — a bare `git worktree add` self-heals with NO hand-holding: the
# SHARED post-checkout dispatcher runs the binary's native heal, which needs no
# per-worktree lefthook config at all. Pre-fix this gate would be ABSENT after the
# add (the "harness incomplete" bug).
PCHOOK="$COMMON/hooks/post-checkout"
grep -q 'omakase dispatcher' "$PCHOOK" 2>/dev/null && pass "shared post-checkout hook is the omakase dispatcher" || fail "post-checkout is not the dispatcher"
WTB="$TMP/repoC-wtbare"
( cd "$REPO" && git worktree add -q "$WTB" -b wtbare ) >/dev/null 2>&1
[ -x "$WTB/.omakase/gates/example.sh" ] && pass "bare 'git worktree add' self-healed the gate (no manual heal)" || fail "bare worktree did NOT self-heal — harness incomplete"
[ -f "$WTB/omakase.manifest" ] && pass "bare worktree self-healed the manifest wiring too" || fail "bare worktree missing omakase.manifest after self-heal"
( cd "$REPO" && git worktree remove --force "$WTB" ) 2>/dev/null; ( cd "$REPO" && git worktree prune ) 2>/dev/null

# C5: remove tears the harness snapshot down too.
( cd "$REPO" && OMAKASE_PAYLOAD="$PAY" bash "$REMOVE" ) >/dev/null 2>&1
[ ! -e "$COMMON/omakase" ] && pass "remove deleted the shared snapshot" || fail "remove left the snapshot"
[ ! -e "$REPO/.worktreeinclude" ] && pass "remove deleted the .worktreeinclude block" || fail "remove left .worktreeinclude"
[ ! -e "$COMMON/hooks/post-checkout" ] && pass "remove deleted the post-checkout dispatcher" || fail "remove left the post-checkout hook"

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
heal "$WTD" >/dev/null 2>&1
[ -L "$WTD/CLAUDE.md" ] && pass "the native heal self-healed the symlink into a worktree" || fail "the heal did not carry the symlink"
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
# the overwritten content is recoverable: init preserves the pre-overwrite copy under clobbered/
CMN="$(cd "$REPO" && cd "$(git rev-parse --git-common-dir)" && pwd)"
grep -q 'MY EDIT' "$CMN/omakase/clobbered/.omakase/gates/example.sh" 2>/dev/null && pass "pre-overwrite copy preserved under clobbered/ (recoverable)" || fail "overwritten content was destroyed with no backup"
echo "$OUT" | grep -q 'clobbered/' && pass "the overwrite warning names the preserved-copy path" || fail "overwrite warning does not name the backup ($OUT)"
[ -z "$(cd "$REPO" && git status --porcelain)" ] && pass "git status still clean after an overwrite" || { fail "status not clean after overwrite"; (cd "$REPO" && git status --porcelain | sed 's/^/      /'); }
# E2 — payload changed upstream; re-init takes the new version (same overwrite path).
PAY="$TMP/payloadE2"; REPO="$TMP/repoE2"; mkpayload "$PAY"; newrepo "$REPO"
( cd "$REPO" && OMAKASE_PAYLOAD="$PAY" bash "$INIT" ) >/dev/null 2>&1
printf '#!/usr/bin/env bash\necho NEW-PAYLOAD-V2\nexit 0\n' > "$PAY/.omakase/gates/example.sh"
( cd "$REPO" && OMAKASE_PAYLOAD="$PAY" bash "$INIT" ) >/dev/null 2>&1
grep -q 'NEW-PAYLOAD-V2' "$REPO/.omakase/gates/example.sh" && pass "re-init took the new payload version (upstream update)" || fail "upstream update did not apply"
# E3 — FIRST install over a user's OWN pre-existing untracked file at a payload path. No prior
# ledger exists, so the place-loop backup is the ONLY safety net (the collision guard is skipped).
PAY="$TMP/payloadE3"; REPO="$TMP/repoE3"; mkpayload "$PAY"; newrepo "$REPO"
mkdir -p "$REPO/.omakase/gates"; echo 'MY OWN FILE' > "$REPO/.omakase/gates/example.sh"
OUT=$( cd "$REPO" && OMAKASE_PAYLOAD="$PAY" bash "$INIT" 2>&1 )
grep -q 'omakase-example-gate-ran' "$REPO/.omakase/gates/example.sh" && pass "first install overwrote the user's pre-existing untracked file" || fail "first install did not overwrite"
CMN="$(cd "$REPO" && cd "$(git rev-parse --git-common-dir)" && pwd)"
grep -q 'MY OWN FILE' "$CMN/omakase/clobbered/.omakase/gates/example.sh" 2>/dev/null && pass "first-install: user's content preserved under clobbered/ (no prior ledger needed)" || fail "first-install backup missing — user file destroyed with no recovery"
echo "$OUT" | grep -q 'clobbered/' && pass "first-install overwrite warning names the backup" || fail "no backup path in the warning ($OUT)"
# E4 — an untracked SYMLINK-TO-DIRECTORY where the payload ships a regular file: init must replace
# the symlink with the payload file, NOT write the file through into the linked dir (regression
# guard for the bare-`-d`-follows-symlink bug), and back up the symlink.
PAY="$TMP/payloadE4"; REPO="$TMP/repoE4"; mkpayload "$PAY"; newrepo "$REPO"
mkdir -p "$REPO/realdir" "$REPO/.omakase/gates"
( cd "$REPO/.omakase/gates" && ln -s ../../realdir example.sh )   # example.sh -> a directory
OUT=$( cd "$REPO" && OMAKASE_PAYLOAD="$PAY" bash "$INIT" 2>&1 )
[ ! -L "$REPO/.omakase/gates/example.sh" ] && pass "symlink-to-dir dest replaced (no longer a symlink)" || fail "symlink-to-dir dest left as a symlink"
grep -q 'omakase-example-gate-ran' "$REPO/.omakase/gates/example.sh" 2>/dev/null && pass "payload file placed at the path" || fail "payload not placed at the path"
[ ! -e "$REPO/realdir/example.sh" ] && pass "payload NOT written through into the linked directory" || fail "payload leaked into the linked dir (write-through bug)"
CMN="$(cd "$REPO" && cd "$(git rev-parse --git-common-dir)" && pwd)"
[ -L "$CMN/omakase/clobbered/.omakase/gates/example.sh" ] && pass "the user's symlink was backed up under clobbered/ (as a symlink)" || fail "symlink dest not backed up"

# ---------- Scenario F: omakase status renders the installed harness ----------
echo "== Scenario F: show renders the installed-but-invisible harness =="
SHOW="$HERE/../bin/status.sh"
PAY="$TMP/payloadF"; REPO="$TMP/repoF"; mkpayload "$PAY"; newrepo "$REPO"
OUT=$( cd "$REPO" && bash "$SHOW" 2>&1 )
echo "$OUT" | grep -qi 'No omakase harness' && pass "show reports empty state before init" || fail "show did not report empty state"
( cd "$REPO" && OMAKASE_PAYLOAD="$PAY" bash "$INIT" ) >/dev/null 2>&1
OUT=$( cd "$REPO" && bash "$SHOW" 2>&1 )
echo "$OUT" | grep -q 'INJECTED (omakase)' && pass "show prints the placed files as the Injected group" || fail "show missing INJECTED group"
INJ="$(echo "$OUT" | awk '/^INJECTED \(omakase\)/{f=1;next} /^GLOBAL /{f=0} f')"
echo "$INJ" | grep -q '.claude/rules/style.md' && pass "show lists an injected harness file in the Injected group" || fail "show did not list the injected file in the Injected group"
echo "$OUT" | grep -qi 'zero footprint' && pass "show states the zero-committed footprint" || fail "show missing the footprint line"
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
