#!/usr/bin/env bash
# Proof of the sources mechanism (spec §1): init.sh --source <git-url-or-path>
# clones a SOURCE (a git repo carrying payload/ + omakase.manifest) into a local
# cache, validates it, and injects its payload through the normal flow.
#   S1. install from a local source repo — cache under XDG_CACHE_HOME, files
#       placed, ledger source column = the user's source string, remembered
#       source written, verify-overlay passes, a real commit fires the gate
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
REMOVE="$HERE/../bin/remove.sh"
SHOW="$HERE/../bin/show.sh"
LEFTHOOK="${LEFTHOOK_BIN:-/Users/ericshen/Claude/pixterm-engine/node_modules/.bin/lefthook}"
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
post-checkout:
  jobs:
    - name: omakase-ensure-present
      run: bash "$(git rev-parse --git-common-dir)/omakase/ensure-present.sh"
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
( cd "$REPO" && sh "$COMMON/omakase/verify-overlay.sh" ) >/dev/null 2>&1 && pass "verify-overlay exits 0" || fail "verify-overlay blocked a complete overlay"
[ -z "$(cd "$REPO" && git status --porcelain)" ] && pass "git status clean (zero footprint)" || { fail "git status NOT clean"; (cd "$REPO" && git status --porcelain | sed 's/^/      /'); }
OUT=$(cd "$REPO" && echo x > f.txt && git add f.txt 2>/dev/null; git commit -m t 2>&1); echo "$OUT" | grep -q "omakase-example-gate-ran" && pass "gate fired on a real commit" || { fail "gate did not fire"; echo "$OUT" | sed 's/^/      /'; }

# ---------- Scenario S2: show renders the source string ----------
echo "== Scenario S2: show's Injected group carries the source string =="
OUT=$( cd "$REPO" && HOME="$FAKEHOME" bash "$SHOW" 2>&1 )
echo "$OUT" | grep '.omakase/gates/example.sh' | grep -qF "from $SRC" && pass "show renders 'from <source>' on an injected row" || fail "show row missing the source string"

# ---------- Scenario S3: bare re-run refreshes the remembered source ----------
echo "== Scenario S3: source commits an update; bare init refreshes it =="
printf '#!/usr/bin/env bash\necho NEW-PAYLOAD-V2\nexit 0\n' > "$SRC/payload/.omakase/gates/example.sh"
( cd "$SRC" && git add -A && git commit -q -m v2 )
( cd "$REPO" && HOME="$FAKEHOME" XDG_CACHE_HOME="$CACHEHOME" bash "$INIT" ) >/dev/null 2>&1
grep -q 'NEW-PAYLOAD-V2' "$REPO/.omakase/gates/example.sh" && pass "bare init pulled the new payload version from the remembered source" || fail "update did not apply"
awk -F'\t' -v s="$SRC" '$3!=s{bad=1} END{exit bad?1:0}' "$LEDGER" 2>/dev/null && pass "ledger still records the source string after refresh" || fail "ledger source column lost on refresh"

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

rm -rf "$TMP"
echo ""
[ "$FAILED" -eq 0 ] && echo "ALL PASS" || { echo "FAILURES PRESENT"; exit 1; }
