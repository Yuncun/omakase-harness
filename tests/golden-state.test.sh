#!/usr/bin/env bash
# Phase 0 compat contract: the exact BYTES of the .git/info/exclude block and the
# $OMK layout ($OMK = $(git rev-parse --git-common-dir)/omakase) that any future
# writer (the Go rewrite) must reproduce (docs/v2-design.md §5, §10).
#
# The exclude block is delimited by the byte-exact markers
#   # >>> omakase-harness >>>   /   # <<< omakase-harness <<<
# and contains DERIVED PREFIXES, not raw placed paths (contract capture of current
# bash behavior): an omakase-OWNED top dir (.omakase, .claude, ...) is one wholesale
# entry with a trailing '/'; a top dir SHARED with the project (.github) is excluded
# file-by-file (full placed path per entry); entries appear deduped in placed.tsv row
# order; one wiring entry follows that is excluded but never in placed.tsv —
# .worktreeinclude, only when the repo does not track it.
# Every entry carries a leading '/' (root-anchored — an unanchored gitignore
# pattern matches at any depth and would hide same-named nested paths).
# Golden bytes are DERIVED in-run from the seeded inputs + placed.tsv (no committed
# golden files) and compared with cmp/diff.
#
# NOT re-asserted here (behavioral coverage elsewhere):
#   block written/stripped, zero footprint, worktree self-heal — tests/inject.test.sh
#   .github file-by-file vs whole-dir behavior              — tests/copilot-exclude-scope.test.sh
#   placed.tsv / ledger.tsv column FORMAT                   — tests/state-format.test.sh
#   source install mechanics (cache, refresh, refusals)     — tests/sources.test.sh
set -u
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
INIT="$HERE/../bin/init.sh"
TMP="${TMPDIR:-/tmp}/omakase-golden-state-test.$$"
FAILED=0
pass(){ echo "  PASS: $1"; }
fail(){ echo "  FAIL: $1"; FAILED=1; }
indent(){ printf '%s\n' "$1" | sed 's/^/      /'; }

BEGIN_MARK='# >>> omakase-harness >>>'
END_MARK='# <<< omakase-harness <<<'

newrepo(){ rm -rf "$1"; mkdir -p "$1"; ( cd "$1" && git init -q && git config user.email t@t && git config user.name t && git config commit.gpgsign false && git commit -q --allow-empty -m init ); }
common_of(){ echo "$(cd "$1" && cd "$(git rev-parse --git-common-dir)" && pwd)"; }

# Derive the expected exclude-block bytes from placed.tsv — the frozen derivation
# rule (see header). The shared-topdir list is hardcoded to .github on purpose:
# it freezes HARNESS_SHARED_TOPDIRS as of v1 without sourcing bin internals.
derive_block(){ # $1=repo root, $2=placed.tsv, $3=out file
  local root="$1" placed="$2" out="$3" ent="$3.entries" rel _rest top p
  : > "$ent"
  while IFS=$'\t' read -r rel _rest; do
    [ -n "$rel" ] || continue
    case "${rel%%/*}" in .github) top="$rel";; *) top="${rel%%/*}";; esac
    grep -qxF -- "$top" "$ent" || printf '%s\n' "$top" >> "$ent"
  done < "$placed"
  git -C "$root" ls-files --error-unmatch -- .worktreeinclude >/dev/null 2>&1 || printf '%s\n' ".worktreeinclude" >> "$ent"
  {
    printf '%s\n' "$BEGIN_MARK"
    # Every entry root-anchored with a leading "/" (unanchored gitignore
    # patterns match at any depth and would hide same-named nested paths).
    while IFS= read -r p; do
      if [ -d "$root/$p" ]; then printf '/%s/\n' "$p"; else printf '/%s\n' "$p"; fi
    done < "$ent"
    printf '%s\n' "$END_MARK"
  } > "$out"
}

# The full exclude-file golden check: exactly one byte-exact marker pair, user lines
# byte-identical and in order OUTSIDE the block, whole file == seeded bytes + derived
# block, and block <-> placed.tsv set equality in both directions.
# $1=repo root, $2=byte copy of the pre-init exclude file, $3=label
check_exclude_golden(){
  local repo="$1" before="$2" label="$3" common excl placed nb ne scratch
  local rel _rest entl cov uncovered orphans base hit
  common="$(common_of "$repo")"; excl="$common/info/exclude"; placed="$common/omakase/placed.tsv"
  scratch="$TMP/$label"
  [ -s "$placed" ] || { fail "$label: placed.tsv missing or empty ($placed)"; return 1; }
  [ -f "$excl" ] || { fail "$label: exclude file missing ($excl)"; return 1; }

  # exactly one block, byte-exact marker lines (Global Constraint 4)
  nb="$(grep -cxF -- "$BEGIN_MARK" "$excl")"; ne="$(grep -cxF -- "$END_MARK" "$excl")"
  [ "${nb:-0}" = 1 ] && pass "$label: exactly one byte-exact opening marker '$BEGIN_MARK'" \
    || fail "$label: expected exactly 1 byte-exact opening marker line, found ${nb:-0}"
  [ "${ne:-0}" = 1 ] && pass "$label: exactly one byte-exact closing marker '$END_MARK'" \
    || fail "$label: expected exactly 1 byte-exact closing marker line, found ${ne:-0}"

  # user lines survive OUTSIDE the block, byte-identical and in order
  awk -v b="$BEGIN_MARK" -v e="$END_MARK" '$0==b{s=1} !s{print} $0==e{s=0}' "$excl" > "$scratch.outside"
  if cmp -s "$before" "$scratch.outside"; then
    pass "$label: pre-existing user lines survive outside the block, byte-identical and in order"
  else
    fail "$label: user lines outside the block differ from the seeded bytes"
    diff "$before" "$scratch.outside" | sed 's/^/      /'
  fi

  # whole-file golden: seeded bytes + the block derived from placed.tsv, appended at EOF
  derive_block "$repo" "$placed" "$scratch.block.expected"
  cat "$before" "$scratch.block.expected" > "$scratch.exclude.expected"
  if cmp -s "$scratch.exclude.expected" "$excl"; then
    pass "$label: whole exclude file is byte-exact (seeded lines + derived block at EOF)"
  else
    fail "$label: exclude file bytes differ from seeded-lines + derived block (expected vs actual below)"
    diff "$scratch.exclude.expected" "$excl" | sed 's/^/      /'
  fi

  # set equality, direction 1: every placed.tsv col-1 path is covered by a block entry
  awk -v b="$BEGIN_MARK" -v e="$END_MARK" '$0==e{s=0} s{print} $0==b{s=1}' "$excl" > "$scratch.block.actual"
  uncovered=""
  while IFS=$'\t' read -r rel _rest; do
    [ -n "$rel" ] || continue
    cov=0
    while IFS= read -r entl; do
      entl="${entl#/}"   # entries are root-anchored; compare against bare rels
      case "$entl" in
        */) case "$rel" in "${entl%/}"/*) cov=1;; esac;;
        *)  [ "$rel" = "$entl" ] && cov=1;;
      esac
    done < "$scratch.block.actual"
    [ "$cov" -eq 1 ] || uncovered="$uncovered$rel
"
  done < "$placed"
  [ -z "$uncovered" ] && pass "$label: every placed.tsv path is covered by an exclude-block entry" \
    || { fail "$label: placed path(s) covered by NO exclude-block entry"; indent "$uncovered"; }

  # set equality, direction 2: nothing inside the block that placed.tsv does not
  # explain — every entry maps back to placed content, except the one wiring
  # entry (.worktreeinclude), which is excluded but never placed.
  orphans=""
  while IFS= read -r entl; do
    [ -n "$entl" ] || { orphans="$orphans(empty line)
"; continue; }
    entl="${entl#/}"   # entries are root-anchored; compare against bare rels
    case "$entl" in .worktreeinclude) continue;; esac
    base="${entl%/}"
    if [ "$base" != "$entl" ]; then
      hit="$(cut -f1 "$placed" | awk -v p="$base/" 'index($0,p)==1{f=1} END{print f+0}')"
    else
      hit="$(cut -f1 "$placed" | awk -v p="$entl" '$0==p{f=1} END{print f+0}')"
    fi
    [ "$hit" = 1 ] || orphans="$orphans$entl
"
  done < "$scratch.block.actual"
  [ -z "$orphans" ] && pass "$label: no block entry outside placed.tsv (+ the wiring entry)" \
    || { fail "$label: block entr(ies) that no placed.tsv path explains"; indent "$orphans"; }
}

# $OMK layout: placed.tsv, payload-snapshot/ mirroring every placed path (byte-equal
# content; symlinks compared by readlink TARGET STRING), executable generated scripts.
# $1=repo root, $2=label
check_omk_layout(){
  local repo="$1" label="$2" common omk rel _rest snap wt bad
  common="$(common_of "$repo")"; omk="$common/omakase"
  [ -f "$omk/placed.tsv" ] && pass "$label: \$OMK/placed.tsv exists" || fail "$label: \$OMK/placed.tsv missing"
  [ -d "$omk/payload-snapshot" ] && pass "$label: \$OMK/payload-snapshot/ exists" || fail "$label: \$OMK/payload-snapshot/ missing"
  # The hook-time scripts live in the binary since #98; $OMK must NOT carry
  # per-repo copies (init deletes any pre-#98 leftovers).
  [ ! -e "$omk/ensure-present.sh" ] && pass "$label: no per-repo ensure-present.sh (job lives in the binary)" || fail "$label: stale ensure-present.sh in \$OMK"
  [ ! -e "$omk/verify-overlay.sh" ] && pass "$label: no per-repo verify-overlay.sh (job lives in the binary)" || fail "$label: stale verify-overlay.sh in \$OMK"
  bad=""
  while IFS=$'\t' read -r rel _rest; do
    [ -n "$rel" ] || continue
    snap="$omk/payload-snapshot/$rel"; wt="$repo/$rel"
    if [ ! -e "$snap" ] && [ ! -L "$snap" ]; then bad="${bad}missing from snapshot: $rel
"; continue; fi
    if [ -L "$wt" ] || [ -L "$snap" ]; then
      { [ -L "$wt" ] && [ -L "$snap" ] && [ "$(readlink "$wt")" = "$(readlink "$snap")" ]; } \
        || bad="${bad}symlink target mismatch (working '$(readlink "$wt" 2>/dev/null)' vs snapshot '$(readlink "$snap" 2>/dev/null)'): $rel
"
    else
      cmp -s "$wt" "$snap" || bad="${bad}content differs (working tree vs snapshot): $rel
"
    fi
  done < "$omk/placed.tsv"
  [ -z "$bad" ] && pass "$label: snapshot mirrors every placed path (content / symlink target byte-equal)" \
    || { fail "$label: payload-snapshot does not mirror the working tree"; indent "$bad"; }
}

# ---------- G1: plain init appends ONE byte-exact block after the seeded user lines ----------
echo "== G1: plain init — one byte-exact block, seeded user lines intact =="
REPO="$TMP/repoG1"; newrepo "$REPO"
COMMON="$(common_of "$REPO")"
mkdir -p "$COMMON/info"
printf 'scratch/\n*.tmp\n' > "$COMMON/info/exclude"   # two pre-existing user lines
cp "$COMMON/info/exclude" "$TMP/G1.before"
( cd "$REPO" && bash "$INIT" ) >/dev/null 2>&1 || fail "G1: plain init exited non-zero"
check_exclude_golden "$REPO" "$TMP/G1.before" "G1"

# ---------- G2: re-init — still exactly one block, byte-identical file ----------
echo "== G2: re-init leaves exactly one block, byte-identical exclude file =="
cp "$COMMON/info/exclude" "$TMP/G2.after-first-init"
( cd "$REPO" && bash "$INIT" ) >/dev/null 2>&1 || fail "G2: re-init exited non-zero"
check_exclude_golden "$REPO" "$TMP/G1.before" "G2"
if cmp -s "$TMP/G2.after-first-init" "$COMMON/info/exclude"; then
  pass "G2: exclude file after re-init is byte-identical to after the first init"
else
  fail "G2: re-init changed the exclude file bytes"
  diff "$TMP/G2.after-first-init" "$COMMON/info/exclude" | sed 's/^/      /'
fi

# ---------- G3: $OMK layout after a plain init ----------
echo "== G3: \$OMK layout (plain init) =="
check_omk_layout "$REPO" "G3"
[ ! -e "$COMMON/omakase/source" ] && pass "G3: plain init writes no \$OMK/source (no remembered source)" \
  || fail "G3: plain init unexpectedly wrote \$OMK/source"

# ---------- G4: --source install — $OMK/source is exactly one line; symlink snapshot ----------
echo "== G4: --source install — remembered source bytes + symlink-carrying snapshot =="
FAKEHOME="$TMP/home"; CACHEHOME="$TMP/cache"; mkdir -p "$FAKEHOME" "$CACHEHOME"
SRC="$TMP/src-harness"; rm -rf "$SRC"; mkdir -p "$SRC/payload/.claude/rules"
( cd "$SRC" && git init -q && git config user.email t@t && git config user.name t && git config commit.gpgsign false )
printf 'shared agent instructions\n' > "$SRC/payload/AGENTS.md"
( cd "$SRC/payload" && ln -s AGENTS.md CLAUDE.md )
printf 'a rule\n' > "$SRC/payload/.claude/rules/style.md"
printf 'name: golden-state-fixture\n' > "$SRC/omakase.manifest"
( cd "$SRC" && git add -A && git commit -q -m harness )
SRC="$(cd "$SRC" && pwd)"   # init absolutizes local dir sources (macOS TMPDIR carries a trailing slash)
REPO4="$TMP/repoG4"; newrepo "$REPO4"
COMMON4="$(common_of "$REPO4")"
mkdir -p "$COMMON4/info"
printf 'scratch/\n*.tmp\n' > "$COMMON4/info/exclude"
cp "$COMMON4/info/exclude" "$TMP/G4.before"
( cd "$REPO4" && HOME="$FAKEHOME" XDG_CACHE_HOME="$CACHEHOME" bash "$INIT" --source "$SRC" ) >/dev/null 2>&1 \
  || fail "G4: --source init exited non-zero"
OMKSRC="$COMMON4/omakase/source"
if printf '%s\n' "$SRC" | cmp -s - "$OMKSRC" 2>/dev/null; then
  pass "G4: \$OMK/source is byte-exact — one line holding the source string"
else
  fail "G4: \$OMK/source is not exactly one line '$SRC' (actual below)"
  sed 's/^/      /' "$OMKSRC" 2>/dev/null || echo "      (missing: $OMKSRC)"
fi
check_exclude_golden "$REPO4" "$TMP/G4.before" "G4"
check_omk_layout "$REPO4" "G4"   # placed set includes CLAUDE.md -> AGENTS.md: pins symlink-target snapshot bytes

rm -rf "$TMP"
echo ""
[ "$FAILED" -eq 0 ] && echo "ALL PASS" || { echo "FAILURES PRESENT"; exit 1; }
