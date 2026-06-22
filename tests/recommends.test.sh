#!/usr/bin/env bash
# Proof of the manifest 'recommends:' field + the fork-to-customize guidance that
# init.sh prints at install:
#   R1. a source whose manifest declares 'recommends:' → init prints it ONCE
#       ("omakase: this harness recommends — <value>"), and prints the
#       fork-to-customize guidance.
#   R2. a source with NO 'recommends:' → the recommends line is ABSENT, but the
#       fork-to-customize guidance still prints (it is generic, not per-source).
# HOME and XDG_CACHE_HOME point at fixture dirs so nothing touches the real machine.
set -u
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
INIT="$HERE/../bin/init.sh"
LEFTHOOK="${LEFTHOOK_BIN:-$(command -v lefthook || true)}"
TMP="${TMPDIR:-/tmp}/omakase-recommends-test.$$"
FAILED=0
pass(){ echo "  PASS: $1"; }
fail(){ echo "  FAIL: $1"; FAILED=1; }

export PATH="$(dirname "$LEFTHOOK"):$PATH"
FAKEHOME="$TMP/home"; CACHEHOME="$TMP/cache"
mkdir -p "$FAKEHOME" "$CACHEHOME"
trap 'rm -rf "$TMP"' EXIT

# Build a SOURCE repo at $1 with optional 'recommends:' line $2.
mksource(){
  local r="$1" rec="${2:-}"; rm -rf "$r"; mkdir -p "$r"
  ( cd "$r" && git init -q && git config user.email t@t && git config user.name t && git config commit.gpgsign false )
  mkdir -p "$r/payload/.omakase/gates"
  printf '#!/usr/bin/env bash\nexit 0\n' > "$r/payload/.omakase/gates/example.sh"
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
  { printf 'name: test-harness\nversion: 0.1.0\n'; [ -n "$rec" ] && printf 'recommends: %s\n' "$rec"; } > "$r/omakase.manifest"
  ( cd "$r" && git add -A && git commit -q -m harness )
}
newrepo(){ rm -rf "$1"; mkdir -p "$1"; ( cd "$1" && git init -q && git config user.email t@t && git config user.name t && git config commit.gpgsign false && git commit -q --allow-empty -m init ); }

REC='superpowers — claude plugin install superpowers@superpowers-marketplace'

# ---------- R1: recommends present ----------
echo "== R1: manifest 'recommends:' is surfaced once at install =="
SRC="$TMP/src-rec"; REPO="$TMP/repo-rec"
mksource "$SRC" "$REC"; newrepo "$REPO"
SRC="$(cd "$SRC" && pwd)"
OUT=$( cd "$REPO" && HOME="$FAKEHOME" XDG_CACHE_HOME="$CACHEHOME" bash "$INIT" --source "$SRC" 2>&1 )
echo "$OUT" | grep -q "this harness recommends" && echo "$OUT" | grep -qF "superpowers@superpowers-marketplace" \
  && pass "recommends line printed with the manifest value" || { fail "recommends line missing/wrong"; echo "$OUT" | sed 's/^/      /'; }
echo "$OUT" | grep -q "to customize, fork the harness source" \
  && pass "fork-to-customize guidance printed" || fail "fork guidance missing"
[ "$(echo "$OUT" | grep -c "this harness recommends")" = "1" ] \
  && pass "recommends surfaced exactly once" || fail "recommends printed more than once"

# ---------- R2: recommends absent ----------
echo "== R2: no 'recommends:' → no recommends line, fork guidance still prints =="
SRC2="$TMP/src-norec"; REPO2="$TMP/repo-norec"
mksource "$SRC2"; newrepo "$REPO2"
SRC2="$(cd "$SRC2" && pwd)"
OUT2=$( cd "$REPO2" && HOME="$FAKEHOME" XDG_CACHE_HOME="$CACHEHOME" bash "$INIT" --source "$SRC2" 2>&1 )
echo "$OUT2" | grep -q "this harness recommends" \
  && { fail "recommends line printed when manifest has none"; echo "$OUT2" | sed 's/^/      /'; } \
  || pass "no recommends line when manifest omits it"
echo "$OUT2" | grep -q "to customize, fork the harness source" \
  && pass "fork-to-customize guidance still printed (generic, not per-source)" || fail "fork guidance missing"

[ "$FAILED" = 0 ] && echo "ALL PASS" || echo "SOME FAILED"
exit "$FAILED"
