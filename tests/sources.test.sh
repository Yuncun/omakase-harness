#!/usr/bin/env bash
# Proof of the sources mechanism (spec §1): init.sh --source <git-url-or-path>
# clones a SOURCE (a git repo carrying payload/ + omakase.manifest) into a local
# cache, validates it, and injects its payload through the normal flow.
#   S1. install from a local source repo — cache under XDG_CACHE_HOME, files
#       placed, ledger source column = the user's source string, remembered
#       source written, the gate verify passes, a real commit fires the gate
#   S2. show renders the source string on the Injected rows
#   S3. update flow — commit a payload change in the source; a bare init.sh
#       re-uses the remembered source, refreshes the cache, places new content
#   S4. refusals — missing payload/ or missing omakase.manifest: nonzero exit,
#       clear error, NOTHING placed
#   S5. remove tears everything down, the remembered source file included
# HOME and XDG_CACHE_HOME point at fixture dirs so nothing touches the real machine.
set -u
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
INIT="$HERE/../bin/init.sh"
OMAKASE="$( cd "$HERE/.." && HERE="$PWD/bin" && . bin/lib-omakase-bin.sh && resolve_omakase 2>/dev/null && echo "$OMAKASE_BIN_RESOLVED" )"
[ -n "$OMAKASE" ] || { echo "FATAL: no omakase binary resolvable"; exit 1; }
verify(){ ( cd "$1" && LEFTHOOK=0 "$OMAKASE" hook pre-commit ); }   # verify-only gate run
REMOVE="$HERE/../bin/remove.sh"
SHOW="$HERE/../bin/status.sh"
LEFTHOOK="${LEFTHOOK_BIN:-$(command -v lefthook || true)}"
TMP="${TMPDIR:-/tmp}/omakase-sources-test.$$"
FAILED=0
pass(){ echo "  PASS: $1"; }
fail(){ echo "  FAIL: $1"; FAILED=1; }

export PATH="$(dirname "$LEFTHOOK"):$PATH"

FAKEHOME="$TMP/home"; CACHEHOME="$TMP/cache"
mkdir -p "$FAKEHOME" "$CACHEHOME"

# Build a SOURCE repo at $1: payload/ (gate + rule + wiring) + omakase.manifest, committed.
mksource(){
  local r="$1"; rm -rf "$r"; mkdir -p "$r"
  ( cd "$r" && git init -q && git config user.email t@t && git config user.name t && git config commit.gpgsign false )
  mkdir -p "$r/payload/.omakase/gates" "$r/payload/.claude/rules"
  cat > "$r/payload/.omakase/gates/example.sh" <<'SH'
#!/usr/bin/env bash
echo "omakase-example-gate-ran"
exit 0
SH
  cat > "$r/payload/lefthook-local.yml" <<'YML'
pre-commit:
  jobs:
    - name: omakase-example
      run: bash .omakase/gates/example.sh
YML
  printf 'a rule\n' > "$r/payload/.claude/rules/style.md"
  cat > "$r/omakase.manifest" <<'MAN'
name: test-harness
version: 0.1.0
MAN
  ( cd "$r" && git add -A && git commit -q -m harness )
}

newrepo(){ rm -rf "$1"; mkdir -p "$1"; ( cd "$1" && git init -q && git config user.email t@t && git config user.name t && git config commit.gpgsign false && git commit -q --allow-empty -m init ); }

# ---------- Scenario S1: install from a local source repo ----------
echo "== Scenario S1: --source <abs-path> clones, validates, injects =="
SRC="$TMP/src-harness"; REPO="$TMP/repoS1"
mksource "$SRC"; newrepo "$REPO"
SRC="$(cd "$SRC" && pwd)"   # normalized, as init absolutizes local dir sources (macOS TMPDIR carries a trailing slash)
( cd "$REPO" && HOME="$FAKEHOME" XDG_CACHE_HOME="$CACHEHOME" bash "$INIT" --source "$SRC" ) >/dev/null 2>&1
COMMON="$(cd "$REPO" && cd "$(git rev-parse --git-common-dir)" && pwd)"
LEDGER="$COMMON/omakase/placed.tsv"

CACHE_DIR=""
for d in "$CACHEHOME"/omakase/sources/*/; do [ -d "$d" ] && CACHE_DIR="${d%/}"; done
{ [ -n "$CACHE_DIR" ] && [ -d "$CACHE_DIR/.git" ]; } && pass "cache clone created under the fake XDG_CACHE_HOME" || fail "no cache clone under $CACHEHOME/omakase/sources"
echo "$CACHE_DIR" | grep -q 'src-harness' && pass "cache slug carries the source basename" || fail "cache slug missing the source basename ($CACHE_DIR)"
[ -x "$REPO/.omakase/gates/example.sh" ] && pass "payload gate placed (executable)" || fail "gate not placed"
[ -f "$REPO/.claude/rules/style.md" ] && pass "payload rule placed" || fail "rule not placed"
awk -F'\t' -v s="$SRC" '$3!=s{bad=1} END{exit bad?1:0}' "$LEDGER" 2>/dev/null && pass "ledger source column is the user's source string on every row" || fail "ledger source column wrong"
[ "$(head -n1 "$COMMON/omakase/source" 2>/dev/null)" = "$SRC" ] && pass "remembered source written to \$COMMON/omakase/source" || fail "remembered source missing/wrong"
verify "$REPO" >/dev/null 2>&1 && pass "the gate verify exits 0" || fail "the gate verify blocked a complete overlay"
[ -z "$(cd "$REPO" && git status --porcelain)" ] && pass "git status clean (zero footprint)" || { fail "git status NOT clean"; (cd "$REPO" && git status --porcelain | sed 's/^/      /'); }
OUT=$(cd "$REPO" && echo x > f.txt && git add f.txt 2>/dev/null; git commit -m t 2>&1); echo "$OUT" | grep -q "omakase-example-gate-ran" && pass "gate fired on a real commit" || { fail "gate did not fire"; echo "$OUT" | sed 's/^/      /'; }

# ---------- Scenario S2: show renders the source string ----------
echo "== Scenario S2: show's Injected group carries the source string =="
OUT=$( cd "$REPO" && HOME="$FAKEHOME" bash "$SHOW" 2>&1 )
echo "$OUT" | grep 'rules/style.md' | grep -qF "from $SRC" && pass "show renders 'from <source>' on an injected row" || fail "show row missing the source string"
echo "$OUT" | grep -q "^$(basename "$SRC") —" && pass "show header leads with the harness name from the remembered source" || fail "header does not name the source harness ($OUT)"

# ---------- Scenario S3: bare re-run refreshes the remembered source ----------
echo "== Scenario S3: source commits an update; bare init refreshes it =="
printf '#!/usr/bin/env bash\necho NEW-PAYLOAD-V2\nexit 0\n' > "$SRC/payload/.omakase/gates/example.sh"
( cd "$SRC" && git add -A && git commit -q -m v2 )
( cd "$REPO" && HOME="$FAKEHOME" XDG_CACHE_HOME="$CACHEHOME" bash "$INIT" ) >/dev/null 2>&1
grep -q 'NEW-PAYLOAD-V2' "$REPO/.omakase/gates/example.sh" && pass "bare init pulled the new payload version from the remembered source" || fail "update did not apply"
awk -F'\t' -v s="$SRC" '$3!=s{bad=1} END{exit bad?1:0}' "$LEDGER" 2>/dev/null && pass "ledger still records the source string after refresh" || fail "ledger source column lost on refresh"

# ---------- Scenario S3b: orphan sweep — a dropped payload file is cleaned up ----------
echo "== Scenario S3b: a file the source drops between versions is swept =="
( cd "$SRC" && git rm -q payload/.claude/rules/style.md && git commit -q -m v3 )
( cd "$REPO" && HOME="$FAKEHOME" XDG_CACHE_HOME="$CACHEHOME" bash "$INIT" ) >/dev/null 2>&1
[ ! -e "$REPO/.claude/rules/style.md" ] && pass "dropped payload file deleted from the repo" || fail "dropped file left behind (silent residue)"
# The base harness ships nothing under .claude/ (the Stop-hook end-of-turn notice is opt-in), so
# once the source's only .claude/ file is dropped, .claude/ is genuinely empty and is pruned.
# Base machinery lives under .omakase/, which never empties — the prune clearing the emptied
# .claude/ while leaving .omakase/ intact proves it removes orphaned dirs without over-pruning.
[ ! -d "$REPO/.claude/rules" ] && pass "emptied source dir (.claude/rules) pruned" || fail ".claude/rules left behind"
[ ! -e "$REPO/.claude" ] && pass "fully-emptied .claude pruned (base ships nothing there)" || fail ".claude left behind after its last file was dropped"
[ -d "$REPO/.omakase" ] && pass "base machinery (.omakase/) survives the prune (no over-reach)" || fail "prune over-reached into base machinery"
grep -q 'style.md' "$LEDGER" && fail "ledger still lists the dropped file" || pass "ledger no longer lists the dropped file"
[ -z "$(cd "$REPO" && git status --porcelain)" ] && pass "git status clean after the sweep" || { fail "status not clean after sweep"; (cd "$REPO" && git status --porcelain | sed 's/^/      /'); }
# a LOCALLY EDITED dropped file is kept, with a warning
mkdir -p "$SRC/payload/.claude/rules"
printf 'extra rule\n' > "$SRC/payload/.claude/rules/extra.md"
( cd "$SRC" && git add payload/.claude/rules/extra.md && git commit -q -m v4 )
( cd "$REPO" && HOME="$FAKEHOME" XDG_CACHE_HOME="$CACHEHOME" bash "$INIT" ) >/dev/null 2>&1
[ -f "$REPO/.claude/rules/extra.md" ] && pass "v4 extra rule placed" || fail "v4 extra rule not placed"
echo 'LOCAL EDIT' >> "$REPO/.claude/rules/extra.md"
( cd "$SRC" && git rm -q payload/.claude/rules/extra.md && git commit -q -m v5 )
OUT=$( cd "$REPO" && HOME="$FAKEHOME" XDG_CACHE_HOME="$CACHEHOME" bash "$INIT" 2>&1 )
{ [ -f "$REPO/.claude/rules/extra.md" ] && grep -q 'LOCAL EDIT' "$REPO/.claude/rules/extra.md"; } && pass "locally edited dropped file kept" || fail "edited dropped file destroyed"
echo "$OUT" | grep -i 'WARNING' | grep -q 'extra.md' && pass "kept file warned about, named" || fail "no warning for the kept file ($OUT)"
rm -rf "$REPO/.claude"   # the user disposes of the kept file; keep later scenarios tidy

# ---------- Scenario S3c: OMAKASE_PAYLOAD env beats the remembered source ----------
echo "== Scenario S3c: precedence — env payload over remembered source =="
PAYENV="$TMP/payload-env"; mkdir -p "$PAYENV"
printf 'env marker\n' > "$PAYENV/ENVMARK.md"
( cd "$REPO" && HOME="$FAKEHOME" XDG_CACHE_HOME="$CACHEHOME" OMAKASE_PAYLOAD="$PAYENV" bash "$INIT" ) >/dev/null 2>&1
[ -f "$REPO/ENVMARK.md" ] && pass "env payload placed (env beat the remembered source)" || fail "env payload not placed"
awk -F'\t' '$3!="payload"{bad=1} END{exit bad?1:0}' "$LEDGER" 2>/dev/null && pass "env install records 'payload' in the source column" || fail "env install source column wrong"
[ "$(head -n1 "$COMMON/omakase/source" 2>/dev/null)" = "$SRC" ] && pass "remembered source untouched by the env install" || fail "remembered source clobbered"
( cd "$REPO" && HOME="$FAKEHOME" XDG_CACHE_HOME="$CACHEHOME" bash "$INIT" ) >/dev/null 2>&1
awk -F'\t' -v s="$SRC" '$3!=s{bad=1} END{exit bad?1:0}' "$LEDGER" 2>/dev/null && pass "bare re-run returned to the remembered source" || fail "bare re-run ignored the remembered source"
[ ! -e "$REPO/ENVMARK.md" ] && pass "pristine env marker swept on the return to the source payload" || fail "env marker left behind"

# ---------- Scenario S3d: corrupt cache self-recovers via a fresh clone ----------
echo "== Scenario S3d: corrupt cache is discarded and re-cloned =="
printf '#!/usr/bin/env bash\necho PAYLOAD-V6\nexit 0\n' > "$SRC/payload/.omakase/gates/example.sh"
( cd "$SRC" && git add -A && git commit -q -m v6 )
echo garbage > "$CACHE_DIR/.git/HEAD"
OUT=$( cd "$REPO" && HOME="$FAKEHOME" XDG_CACHE_HOME="$CACHEHOME" bash "$INIT" 2>&1 ); rc=$?
[ "$rc" -eq 0 ] && pass "init recovered from a corrupt cache" || fail "init failed on a corrupt cache ($OUT)"
echo "$OUT" | grep -qi 're-cloning' && pass "recovery announced (discard + re-clone)" || fail "no recovery notice in output"
grep -q 'PAYLOAD-V6' "$REPO/.omakase/gates/example.sh" && pass "fresh clone delivered the latest payload" || fail "stale payload after recovery"
( cd "$CACHE_DIR" && git rev-parse --git-dir ) >/dev/null 2>&1 && pass "cache healthy again" || fail "cache still corrupt"

# ---------- Scenario S4: refusals — fail closed, place nothing ----------
echo "== Scenario S4: invalid sources are refused with nothing placed =="
SRCNP="$TMP/src-no-payload"; rm -rf "$SRCNP"; mkdir -p "$SRCNP"
( cd "$SRCNP" && git init -q && git config user.email t@t && git config user.name t && git config commit.gpgsign false )
printf 'name: broken\n' > "$SRCNP/omakase.manifest"
( cd "$SRCNP" && git add -A && git commit -q -m m )
REPO2="$TMP/repoS4a"; newrepo "$REPO2"
ERR=$( cd "$REPO2" && HOME="$FAKEHOME" XDG_CACHE_HOME="$CACHEHOME" bash "$INIT" --source "$SRCNP" 2>&1 ); rc=$?
[ "$rc" -ne 0 ] && pass "source without payload/ refused (nonzero exit)" || fail "missing payload accepted"
echo "$ERR" | grep -qi 'payload' && pass "error names the missing payload" || fail "error unclear ($ERR)"
{ [ ! -e "$REPO2/.omakase" ] && [ ! -e "$REPO2/.git/omakase" ] && [ -z "$(cd "$REPO2" && git status --porcelain)" ]; } && pass "nothing placed on payload refusal" || fail "refusal left artifacts behind"
grep -q 'omakase-harness' "$REPO2/.git/info/exclude" 2>/dev/null && fail "refusal wrote the exclude block" || pass "no exclude block on refusal"

SRCNM="$TMP/src-no-manifest"; rm -rf "$SRCNM"; mkdir -p "$SRCNM/payload"
( cd "$SRCNM" && git init -q && git config user.email t@t && git config user.name t && git config commit.gpgsign false )
printf 'a rule\n' > "$SRCNM/payload/rule.md"
( cd "$SRCNM" && git add -A && git commit -q -m m )
REPO3="$TMP/repoS4b"; newrepo "$REPO3"
ERR=$( cd "$REPO3" && HOME="$FAKEHOME" XDG_CACHE_HOME="$CACHEHOME" bash "$INIT" --source "$SRCNM" 2>&1 ); rc=$?
[ "$rc" -ne 0 ] && pass "source without omakase.manifest refused (nonzero exit)" || fail "missing manifest accepted"
echo "$ERR" | grep -qi 'manifest' && pass "error names the missing manifest" || fail "error unclear ($ERR)"
{ [ ! -e "$REPO3/.git/omakase" ] && [ -z "$(cd "$REPO3" && git status --porcelain)" ]; } && pass "nothing placed on manifest refusal" || fail "refusal left artifacts behind"

# ---------- Scenario S5: remove tears down the remembered source too ----------
echo "== Scenario S5: remove deletes placed files + the remembered source =="
( cd "$REPO" && bash "$REMOVE" ) >/dev/null 2>&1
[ ! -e "$REPO/.omakase" ] && pass "remove deleted the placed tree" || fail "remove left placed files"
[ ! -e "$COMMON/omakase/source" ] && pass "remembered source file gone" || fail "remembered source survived remove"
[ ! -e "$COMMON/omakase" ] && pass "shared omakase dir torn down" || fail "remove left \$COMMON/omakase"
grep -q 'omakase-harness' "$REPO/.git/info/exclude" 2>/dev/null && fail "remove left the exclude block" || pass "exclude block stripped"

# ---------- Scenario S6: base machinery is layered UNDER the source delta ----------
# A real harness WIRES base machinery — the banner, the ledger
# wrapper, the deferred-check gate — but ships only its OWN delta. --source must layer
# the base harness's payload under the source so that wiring resolves at hook time;
# otherwise the hook dies on commit with exit 127 (No such file: .omakase/bin/omakase-banner.sh).
echo "== Scenario S6: --source layers base machinery under the source delta =="
SRC6="$TMP/src-needs-base"; REPO6="$TMP/repoS6"
rm -rf "$SRC6"; mkdir -p "$SRC6/payload/.omakase/gates"
( cd "$SRC6" && git init -q && git config user.email t@t && git config user.name t && git config commit.gpgsign false )
# the source's OWN gate — the only .omakase script it ships
cat > "$SRC6/payload/.omakase/gates/discipline.sh" <<'SH'
#!/usr/bin/env bash
echo "source-discipline-gate-ran"
exit 0
SH
# the source OVERRIDES a base file (the base harness ships .omakase/gates/example.sh) — proves
# the merge lets the SOURCE win on overlap via rm-before-copy, never writing through a base file
cat > "$SRC6/payload/.omakase/gates/example.sh" <<'SH'
#!/usr/bin/env bash
echo "SOURCE-OVERRODE-EXAMPLE"
exit 0
SH
# a source SYMLINK must survive the merge loop's cp -P (the advertised CLAUDE.md -> AGENTS.md)
printf 'shared agent instructions\n' > "$SRC6/payload/AGENTS.md"
( cd "$SRC6/payload" && ln -s AGENTS.md CLAUDE.md )
# wiring that DEPENDS on base machinery the source does NOT ship (banner + gate primitive)
cat > "$SRC6/payload/lefthook-local.yml" <<'YML'
output: [summary, success, failure, execution_out]
pre-commit:
  jobs:
    - name: omakase-banner
      run: bash .omakase/bin/omakase-banner.sh pre-commit
    - name: source-discipline
      run: bash .omakase/bin/omakase-gate.sh source-discipline --step 'bash .omakase/gates/discipline.sh'
YML
printf 'name: needs-base\nversion: 0.1.0\n' > "$SRC6/omakase.manifest"
( cd "$SRC6" && git add -A && git commit -q -m harness )
SRC6="$(cd "$SRC6" && pwd)"
newrepo "$REPO6"
# Scope TMPDIR to this run so the merge-staging leak check below can't false-fail on a stale
# or concurrent omakase-merge.* dir in the shared system /tmp. init.sh's mktemp honors TMPDIR,
# so this exercises the same staging + EXIT-cleanup path, just inside our own scratch.
export TMPDIR="$TMP/merge-tmp"; mkdir -p "$TMPDIR"
( cd "$REPO6" && HOME="$FAKEHOME" XDG_CACHE_HOME="$CACHEHOME" bash "$INIT" --source "$SRC6" ) >/dev/null 2>&1
# base machinery the source did NOT ship is present (layered from the base harness's payload)
[ -x "$REPO6/.omakase/bin/omakase-banner.sh" ] && pass "base banner layered in (source did not ship it)" || fail "base banner missing — base payload not layered under source"
[ -x "$REPO6/.omakase/bin/omakase-gate.sh" ] && pass "base gate primitive layered in" || fail "base gate primitive missing"
# the source's own gate is placed too
[ -x "$REPO6/.omakase/gates/discipline.sh" ] && pass "source's own gate placed" || fail "source gate missing"
grep -q 'SOURCE-OVERRODE-EXAMPLE' "$REPO6/.omakase/gates/example.sh" 2>/dev/null && pass "source wins over a base file at the same path (replace semantics, no write-through)" || fail "base file won over the source on overlap (merge write-through?)"
{ [ -L "$REPO6/CLAUDE.md" ] && [ "$(readlink "$REPO6/CLAUDE.md")" = "AGENTS.md" ]; } && pass "source symlink preserved through the merge (CLAUDE.md -> AGENTS.md)" || fail "source symlink not preserved by the merge loop"
[ -z "$(find "$TMPDIR" -maxdepth 1 -name 'omakase-merge.*' 2>/dev/null)" ] && pass "merge staging dir cleaned on exit (no scratch leak)" || fail "merge staging dir leaked in $TMPDIR"
# the source's lefthook WINS over the base's (it is the overlay)
grep -q 'source-discipline' "$REPO6/lefthook-local.yml" 2>/dev/null && pass "source lefthook-local.yml overlays the base one" || fail "source wiring did not win"
COMMON6="$(cd "$REPO6" && cd "$(git rev-parse --git-common-dir)" && pwd)"
verify "$REPO6" >/dev/null 2>&1 && pass "the gate verify passes over the merged overlay" || fail "the gate verify blocked the merged overlay"
# the real bite: a commit must FIRE the wired gate with no exit-127 from missing machinery
OUT=$(cd "$REPO6" && echo x > f.txt && git add f.txt 2>/dev/null; git commit -m t 2>&1); rc=$?
echo "$OUT" | grep -q "source-discipline-gate-ran" && pass "wired gate fired on a real commit (base machinery resolved)" || { fail "gate did not fire — base machinery unresolved"; echo "$OUT" | sed 's/^/      /'; }
echo "$OUT" | grep -qiE 'No such file|not found|: 127' && { fail "commit hit a missing-machinery error"; echo "$OUT" | sed 's/^/      /'; } || pass "no missing-machinery error on commit"
[ "$rc" -eq 0 ] && pass "commit succeeded" || { fail "commit failed (rc=$rc)"; echo "$OUT" | sed 's/^/      /'; }
[ -z "$(cd "$REPO6" && git status --porcelain | grep -v '^?? f.txt$')" ] && pass "no stray tracked/ignored residue from the merge" || { fail "merge left residue in git status"; (cd "$REPO6" && git status --porcelain | sed 's/^/      /'); }
( cd "$REPO6" && bash "$REMOVE" ) >/dev/null 2>&1

# ---------- Scenario S7: wiring guard — a source referencing an unshipped script is refused ----------
# The wiring guard: after the base+source merge, every
# .omakase/*.sh the merged wiring references must exist, else the harness would die at
# commit with exit 127. Refuse at init, fail-closed, place nothing.
echo "== Scenario S7: a source wiring a script neither it nor the base harness ships is refused =="
SRC7="$TMP/src-bad-wiring"; REPO7="$TMP/repoS7"
rm -rf "$SRC7"; mkdir -p "$SRC7/payload/.omakase/gates"
( cd "$SRC7" && git init -q && git config user.email t@t && git config user.name t && git config commit.gpgsign false )
cat > "$SRC7/payload/lefthook-local.yml" <<'YML'
pre-commit:
  jobs:
    - name: ghost
      run: bash .omakase/gates/this-script-does-not-exist.sh
YML
printf 'name: bad-wiring\n' > "$SRC7/omakase.manifest"
( cd "$SRC7" && git add -A && git commit -q -m m )
SRC7="$(cd "$SRC7" && pwd)"
newrepo "$REPO7"
ERR=$( cd "$REPO7" && HOME="$FAKEHOME" XDG_CACHE_HOME="$CACHEHOME" bash "$INIT" --source "$SRC7" 2>&1 ); rc=$?
[ "$rc" -ne 0 ] && pass "source with dangling wiring refused (nonzero exit)" || fail "dangling wiring accepted"
echo "$ERR" | grep -q 'this-script-does-not-exist.sh' && pass "refusal names the missing script" || fail "refusal does not name the script ($ERR)"
{ [ ! -e "$REPO7/.omakase" ] && [ -z "$(cd "$REPO7" && git status --porcelain)" ]; } && pass "nothing placed on wiring refusal" || fail "wiring refusal left artifacts behind"
grep -q 'omakase-harness' "$REPO7/.git/info/exclude" 2>/dev/null && fail "wiring refusal wrote the exclude block" || pass "no exclude block on wiring refusal"

# ---------- Scenario S8: a COMMENTED-OUT wiring reference is ignored, not a false refusal ----------
# The wiring guard greps the merged lefthook-local.yml; it must strip YAML '#' comments first, or a
# commented-out breadcrumb referencing a script the source doesn't ship (the pattern the base
# payload's own wiring uses for its templates) would trip a fail-closed refusal for a dead line.
echo "== Scenario S8: a commented-out wiring reference is ignored, not refused =="
SRC8="$TMP/src-commented-wiring"; REPO8="$TMP/repoS8"
rm -rf "$SRC8"; mkdir -p "$SRC8/payload/.omakase/gates"
( cd "$SRC8" && git init -q && git config user.email t@t && git config user.name t && git config commit.gpgsign false )
cat > "$SRC8/payload/.omakase/gates/live.sh" <<'SH'
#!/usr/bin/env bash
echo live; exit 0
SH
# the LIVE gate is shipped; the gate referenced in the COMMENT (legacy-removed.sh) is NOT — and must
# not be treated as a live requirement.
cat > "$SRC8/payload/lefthook-local.yml" <<'YML'
pre-commit:
  jobs:
    # Old gate, replaced by 'live' below — left as a breadcrumb:
    # - run: bash .omakase/gates/legacy-removed.sh
    - name: live
      run: bash .omakase/gates/live.sh
YML
printf 'name: commented-wiring\n' > "$SRC8/omakase.manifest"
( cd "$SRC8" && git add -A && git commit -q -m m )
SRC8="$(cd "$SRC8" && pwd)"
newrepo "$REPO8"
OUT8=$( cd "$REPO8" && HOME="$FAKEHOME" XDG_CACHE_HOME="$CACHEHOME" bash "$INIT" --source "$SRC8" 2>&1 ); rc=$?
[ "$rc" -eq 0 ] && pass "commented-out wiring reference did not cause a refusal (install succeeded)" || { fail "commented-out reference tripped the wiring guard (rc=$rc)"; echo "$OUT8" | sed 's/^/      /'; }
echo "$OUT8" | grep -q 'legacy-removed.sh' && fail "guard named a commented-out script" || pass "guard ignored the commented-out script"
[ -x "$REPO8/.omakase/gates/live.sh" ] && pass "source's live gate placed" || fail "live gate missing"
( cd "$REPO8" && bash "$REMOVE" ) >/dev/null 2>&1

# ---------- Scenario S9: a non-shipping script named AFTER a '#' inside a --step is still caught ----------
# The '#'-truncation hazard the awk guard closes: a '#' INSIDE a quoted --step value precedes the
# script reference. A line-comment-stripping guard (sed 's/#.*//') would cut the line at the '#' and
# miss does-not-ship.sh, passing the install. The guard skips only FULL-LINE comments, so it keeps
# this line whole, finds the reference, and refuses. (This is the differential vs a sed-based guard.)
echo "== Scenario S9: wiring guard catches a script named after a # inside a --step =="
REPOWG="$TMP/repoWG"; PAYWG="$TMP/payWG"
rm -rf "$PAYWG"; cp -R "$HERE/../payload/." "$PAYWG/"
cat > "$PAYWG/lefthook-local.yml" <<'YML'
pre-commit:
  jobs:
    - name: ghost
      run: bash .omakase/bin/omakase-gate.sh ghost --step 'echo "#skip"; bash .omakase/gates/does-not-ship.sh'
YML
newrepo "$REPOWG"
OUT="$( cd "$REPOWG" && OMAKASE_PAYLOAD="$PAYWG" bash "$INIT" 2>&1 )"; RC=$?
{ [ "$RC" -ne 0 ] && echo "$OUT" | grep -q 'does-not-ship.sh'; } && pass "guard refuses a script named after a # inside a --step (plain path)" || fail "guard missed the non-shipping script ($RC: $OUT)"
[ ! -d "$REPOWG/.omakase" ] && pass "guard refused before placing anything" || fail "guard placed files despite refusing"

# ---------- Scenario S10: a harness adopted from a SUBFOLDER of a hub repo ----------
echo "== Scenario S10: --source <hub>//subpath adopts a harness from inside a repo =="
HUB="$TMP/hub"; REPOSUB="$TMP/repoS10"
rm -rf "$HUB"; mkdir -p "$HUB/tools/harness/payload/.claude/rules"
( cd "$HUB" && git init -q && git config user.email t@t && git config user.name t && git config commit.gpgsign false )
printf 'name: hub-harness\n' > "$HUB/tools/harness/omakase.manifest"
printf 'sub rule\n' > "$HUB/tools/harness/payload/.claude/rules/sub.md"
printf 'name: decoy\n' > "$HUB/omakase.manifest"   # root-level decoys: a subpath install must never read these
mkdir -p "$HUB/payload"; printf 'never\n' > "$HUB/payload/decoy.txt"
( cd "$HUB" && git add -A && git commit -q -m hub )
HUB="$(cd "$HUB" && pwd)"
newrepo "$REPOSUB"
( cd "$REPOSUB" && HOME="$FAKEHOME" XDG_CACHE_HOME="$CACHEHOME" bash "$INIT" --source "$HUB//tools/harness" ) >/dev/null 2>&1
COMMONSUB="$(cd "$REPOSUB" && cd "$(git rev-parse --git-common-dir)" && pwd)"
[ -f "$REPOSUB/.claude/rules/sub.md" ] && pass "subfolder harness placed" || fail "subfolder harness not placed"
[ ! -f "$REPOSUB/decoy.txt" ] && pass "hub-root decoy payload ignored (validation ran at the subfolder)" || fail "hub-root decoy placed"
[ "$(head -n1 "$COMMONSUB/omakase/source" 2>/dev/null)" = "$HUB//tools/harness" ] && pass "remembered source is the canonical root//subpath string" || fail "remembered source wrong: $(head -n1 "$COMMONSUB/omakase/source" 2>/dev/null)"
printf 'sub rule v2\n' > "$HUB/tools/harness/payload/.claude/rules/sub.md"
( cd "$HUB" && git add -A && git commit -q -m v2 )
( cd "$REPOSUB" && HOME="$FAKEHOME" XDG_CACHE_HOME="$CACHEHOME" bash "$INIT" ) >/dev/null 2>&1
grep -q 'sub rule v2' "$REPOSUB/.claude/rules/sub.md" && pass "bare init refreshed the hub and re-injected the same subfolder" || fail "subfolder refresh did not apply"
REPOSUBX="$TMP/repoS10x"; newrepo "$REPOSUBX"
OUT="$( cd "$REPOSUBX" && HOME="$FAKEHOME" XDG_CACHE_HOME="$CACHEHOME" bash "$INIT" --source "$HUB//no/such" 2>&1 )"; RC=$?
{ [ "$RC" -ne 0 ] && echo "$OUT" | grep -q "has no directory 'no/such'"; } && pass "a subpath naming no directory is refused" || fail "missing subfolder not refused ($RC: $OUT)"
[ ! -d "$REPOSUBX/.claude" ] && pass "the refusal placed nothing" || fail "refusal placed files"

rm -rf "$TMP"
echo ""
[ "$FAILED" -eq 0 ] && echo "ALL PASS" || { echo "FAILURES PRESENT"; exit 1; }
