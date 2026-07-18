#!/usr/bin/env bash
# Phase 0 compat contract: the byte-level FORMAT of the two TSV state files any
# future writer (the Go rewrite) must reproduce exactly (docs/v2-design.md §5).
#   placed.tsv — EXACTLY 5 tab-separated columns: path kind source sha256 enabled.
#     A 6th column is a defect: the sh readers parse `read -r rel kind src hash enabled`,
#     so an extra column is absorbed into $enabled and flips verification fail-open.
#   ledger.tsv — exactly 4 tab-separated columns: epoch name verdict sha.
# Scenarios (each in a fresh fixture repo):
#   S1 plain init (plugin-style: payload from the repo checkout)
#   S2 --source install whose payload ships a relative symlink; the symlink's
#      recorded sha256 is the digest of its readlink TARGET STRING, not content
#   S3 re-init — format still holds, no duplicate path rows
#   S4 gate runs — every ledger.tsv row is 4 fields, field 1 numeric (epoch)
# Behavioral coverage (kind classification, hash-matches-content, enabled=0
# semantics, gate caching/verdicts) lives in tests/placed.test.sh; the ledger's
# concurrent-append atomicity is proven in internal/gate's Go unit tests
# (TestLedgerConcurrentAppendsDoNotTear) — NOT re-asserted here.
set -u
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
INIT="$HERE/../bin/init.sh"
TMP="${TMPDIR:-/tmp}/omakase-state-format-test.$$"
FAILED=0
pass(){ echo "  PASS: $1"; }
fail(){ echo "  FAIL: $1"; FAILED=1; }
indent(){ printf '%s\n' "$1" | sed 's/^/      /'; }

# Same digest detection the implementation uses (shasum on macOS, sha256sum on Linux).
if command -v shasum >/dev/null 2>&1; then sha_str(){ printf '%s' "$1" | shasum -a 256 | awk '{print $1}'; }
else sha_str(){ printf '%s' "$1" | sha256sum | awk '{print $1}'; }; fi

newrepo(){ rm -rf "$1"; mkdir -p "$1"; ( cd "$1" && git init -q && git config user.email t@t && git config user.name t && git config commit.gpgsign false && git commit -q --allow-empty -m init ); }
common_of(){ echo "$(cd "$1" && cd "$(git rev-parse --git-common-dir)" && pwd)"; }

# Self-contained HOME + cache so init self-installs the binary the S4 commit hook
# execs (${XDG_CACHE_HOME:-$HOME/.cache}/omakase/bin/current/omakase), and nothing
# touches the real machine. S2 re-sets HOME/XDG inline to these same dirs.
export HOME="$TMP/home"; export XDG_CACHE_HOME="$TMP/cache"
mkdir -p "$HOME" "$XDG_CACHE_HOME"
if command -v go >/dev/null 2>&1; then
  export GOMODCACHE="$(go env GOMODCACHE)"
  export GOCACHE="$(go env GOCACHE)"
fi

# The placed.tsv format contract, asserted per scenario. Failure output names the
# offending row(s). $1 = placed.tsv path, $2 = scenario label.
check_placed_format(){
  local f="$1" label="$2" bad
  if [ ! -s "$f" ]; then fail "$label: placed.tsv missing or empty ($f)"; return 1; fi
  bad="$(awk -F'\t' 'NF!=5{printf "row %d has %d fields: %s\n", NR, NF, $0}' "$f")"
  [ -z "$bad" ] && pass "$label: every row has exactly 5 tab-separated fields" || { fail "$label: row(s) without exactly 5 fields"; indent "$bad"; }
  bad="$(awk -F'\t' '$1==""||$2==""||$3==""||$4==""{printf "row %d has an empty field: %s\n", NR, $0}' "$f")"
  [ -z "$bad" ] && pass "$label: no empty path/kind/source/sha256 field" || { fail "$label: row(s) with an empty path/kind/source/sha256 field"; indent "$bad"; }
  bad="$(awk -F'\t' '$5!="1"{printf "row %d enabled=%s: %s\n", NR, $5, $0}' "$f")"
  [ -z "$bad" ] && pass "$label: enabled column is 1 on every row" || { fail "$label: row(s) with enabled != 1"; indent "$bad"; }
  # length()+class check, not /{64}/: interval expressions are not portable across awks
  bad="$(awk -F'\t' 'length($4)!=64 || $4 !~ /^[0-9a-f]+$/{printf "row %d sha256=%s: %s\n", NR, $4, $0}' "$f")"
  [ -z "$bad" ] && pass "$label: sha256 is 64 lowercase hex chars on every row" || { fail "$label: row(s) with a malformed sha256"; indent "$bad"; }
}

# ---------- S1: plain init (plugin-style payload from the repo checkout) ----------
echo "== S1: plain init writes a well-formed placed.tsv =="
REPO="$TMP/repoS1"; newrepo "$REPO"
( cd "$REPO" && OMAKASE_PAYLOAD="$HERE/../payload" bash "$INIT" ) >/dev/null 2>&1 || fail "S1: plain init exited non-zero"
check_placed_format "$(common_of "$REPO")/omakase/placed.tsv" "S1"

# ---------- S2: --source install with a payload symlink ----------
echo "== S2: --source install — 5-column rule + symlink target-string digest =="
FAKEHOME="$TMP/home"; CACHEHOME="$TMP/cache"; mkdir -p "$FAKEHOME" "$CACHEHOME"
SRC="$TMP/src-harness"; rm -rf "$SRC"; mkdir -p "$SRC/payload"
( cd "$SRC" && git init -q && git config user.email t@t && git config user.name t && git config commit.gpgsign false )
printf 'shared agent instructions\n' > "$SRC/payload/AGENTS.md"
( cd "$SRC/payload" && ln -s AGENTS.md CLAUDE.md )
printf 'name: state-format-fixture\n' > "$SRC/payload/omakase.manifest"
( cd "$SRC" && git add -A && git commit -q -m harness )
SRC="$(cd "$SRC" && pwd)"   # init absolutizes local dir sources (macOS TMPDIR carries a trailing slash)
REPO="$TMP/repoS2"; newrepo "$REPO"
( cd "$REPO" && HOME="$FAKEHOME" XDG_CACHE_HOME="$CACHEHOME" bash "$INIT" --source "$SRC" ) >/dev/null 2>&1 || fail "S2: --source init exited non-zero"
PLACED="$(common_of "$REPO")/omakase/placed.tsv"
check_placed_format "$PLACED" "S2"
row_sha="$(awk -F'\t' '$1=="CLAUDE.md"{print $4; exit}' "$PLACED" 2>/dev/null)"
want="$(sha_str AGENTS.md)"
[ -n "$row_sha" ] && pass "S2: symlink row present (CLAUDE.md)" || fail "S2: no CLAUDE.md row in placed.tsv"
[ "$row_sha" = "$want" ] && pass "S2: symlink sha256 = digest of the readlink target STRING (AGENTS.md)" || fail "S2: symlink sha256 is '$row_sha', want '$want' (digest of the target string, not the dereferenced content)"

# ---------- S3: re-init — format holds, no duplicate path rows ----------
echo "== S3: re-init keeps the format and never duplicates a path row =="
REPO="$TMP/repoS3"; newrepo "$REPO"
( cd "$REPO" && OMAKASE_PAYLOAD="$HERE/../payload" bash "$INIT" ) >/dev/null 2>&1
( cd "$REPO" && OMAKASE_PAYLOAD="$HERE/../payload" bash "$INIT" ) >/dev/null 2>&1 || fail "S3: re-init exited non-zero"
PLACED="$(common_of "$REPO")/omakase/placed.tsv"
check_placed_format "$PLACED" "S3"
dups="$(cut -f1 "$PLACED" 2>/dev/null | sort | uniq -d)"
[ -z "$dups" ] && pass "S3: no duplicate path rows after re-init" || { fail "S3: duplicate path row(s) after re-init"; indent "$dups"; }

# ---------- S4: gate runs write 4-column ledger.tsv rows ----------
echo "== S4: gate runs write 4-column ledger rows (epoch name verdict sha) =="
REPO="$TMP/repoS4"; newrepo "$REPO"
( cd "$REPO" && OMAKASE_PAYLOAD="$HERE/../payload" bash "$INIT" ) >/dev/null 2>&1
[ -f "$REPO/omakase.manifest" ] || fail "S4: init did not place omakase.manifest"
# A real commit fires the wired pre-commit gate, which appends a ledger row. The
# commit hook execs the binary init self-installed into $XDG_CACHE_HOME (set above).
( cd "$REPO" && echo hi > note.txt && git add note.txt && git commit -q -m c1 ) >/dev/null 2>&1
LEDGER="$(common_of "$REPO")/omakase/ledger.tsv"
if [ -s "$LEDGER" ]; then
  pass "S4: gate runs produced ledger.tsv rows"
  bad="$(awk -F'\t' 'NF!=4{printf "row %d has %d fields: %s\n", NR, NF, $0}' "$LEDGER")"
  [ -z "$bad" ] && pass "S4: every ledger row has exactly 4 tab-separated fields" || { fail "S4: ledger row(s) without exactly 4 fields"; indent "$bad"; }
  bad="$(awk -F'\t' '$1 !~ /^[0-9]+$/{printf "row %d field 1 = %s: %s\n", NR, $1, $0}' "$LEDGER")"
  [ -z "$bad" ] && pass "S4: ledger field 1 is numeric (epoch) on every row" || { fail "S4: ledger row(s) with a non-numeric field 1"; indent "$bad"; }
else
  fail "S4: ledger.tsv missing or empty ($LEDGER)"
fi

rm -rf "$TMP"
echo ""
[ "$FAILED" -eq 0 ] && echo "ALL PASS" || { echo "FAILURES PRESENT"; exit 1; }
