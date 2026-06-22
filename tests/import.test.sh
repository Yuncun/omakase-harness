#!/usr/bin/env bash
# Proof that import.sh deterministically captures an existing scattered harness into
# payload/ — including a harness whose own gates are GITIGNORED (the #1 regression:
# never enumerate from `git ls-files`), while skipping personal/noise files, carrying
# symlinks, leaving committed files committed by default, and producing an init-able payload.
set -u
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
IMPORT="$HERE/../bin/import.sh"
INIT="$HERE/../bin/init.sh"
LEFTHOOK="${LEFTHOOK_BIN:-/Users/ericshen/Claude/pixterm-engine/node_modules/.bin/lefthook}"
TMP="${TMPDIR:-/tmp}/omakase-import-test.$$"
FAILED=0
pass(){ echo "  PASS: $1"; }
fail(){ echo "  FAIL: $1"; FAILED=1; }
export PATH="$(dirname "$LEFTHOOK"):$PATH"

# Build a realistic scattered-harness SOURCE repo at $1.
mksource(){
  local r="$1"; rm -rf "$r"; mkdir -p "$r"
  ( cd "$r" && git init -q && git config user.email t@t && git config user.name t && git config commit.gpgsign false )
  # committed doctrine
  printf 'real doctrine\n' > "$r/AGENTS.md"
  ( cd "$r" && ln -s AGENTS.md CLAUDE.md )
  mkdir -p "$r/.claude/rules" "$r/.claude/skills/demo" "$r/.claude/hooks"
  printf 'a rule\n' > "$r/.claude/rules/style.md"
  printf 'a skill\n' > "$r/.claude/skills/demo/SKILL.md"
  printf '#!/usr/bin/env bash\necho hook\n' > "$r/.claude/hooks/sess.sh"
  printf '{ "hooks": {} }\n' > "$r/.claude/settings.json"
  # a wired gate that is GITIGNORED (the harness's own injected gate) — the regression case
  mkdir -p "$r/.omakase/gates"
  printf '#!/usr/bin/env bash\necho GATE-G-RAN\nexit 0\n' > "$r/.omakase/gates/g.sh"; chmod +x "$r/.omakase/gates/g.sh"
  # a loose wired gate OUTSIDE a captured location
  mkdir -p "$r/scripts"; printf '#!/usr/bin/env bash\nexit 0\n' > "$r/scripts/loose.sh"; chmod +x "$r/scripts/loose.sh"
  cat > "$r/lefthook-local.yml" <<'YML'
pre-commit:
  jobs:
    - name: gate-g
      run: bash .omakase/gates/g.sh
    - name: loose
      run: bash scripts/loose.sh
    - name: fmt
      run: pnpm exec prettier --write .
YML
  # personal / noise that MUST NOT be captured
  printf 'SECRET=abc123\n' > "$r/.claude/rules/personal.md"   # personal secret INSIDE a declared harness dir
  printf '{ "outputStyle": "x" }\n' > "$r/.claude/settings.local.json"
  mkdir -p "$r/.claude/worktrees/decoy"; printf 'DECOY\n' > "$r/.claude/worktrees/decoy/AGENTS.md"
  # gitignore personal state (incl. the secret inside .claude/rules); make .omakase + lefthook-local.yml
  # gitignored via the OMAKASE OVERLAY (.git/info/exclude) like a real injected harness.
  printf '.claude/settings.local.json\n.claude/worktrees/\n.claude/rules/personal.md\n' > "$r/.gitignore"
  printf '.omakase/\nlefthook-local.yml\n' >> "$r/.git/info/exclude"
  ( cd "$r" && git add AGENTS.md CLAUDE.md .claude/rules .claude/skills .claude/hooks .claude/settings.json scripts .gitignore && git commit -q -m harness )
}

echo "== Scenario IMPORT: capture a scattered harness (gitignored gates included) =="
SRC="$TMP/src"; PAY="$TMP/payload"
mksource "$SRC"
# Invoked from OUTSIDE the source, naming the repo as a path argument (the creator runs
# import from their harness clone, not from inside the project being captured).
OUT=$( OMAKASE_PAYLOAD="$PAY" bash "$IMPORT" "$SRC" 2>&1 )
echo "$OUT" | grep -qi 'captured' && pass "import ran from OUTSIDE the source, taking the repo as a path argument" || { fail "positional-source import did not run"; echo "$OUT" | sed 's/^/      /'; }

# #1 REGRESSION: a GITIGNORED gate must still be captured (no git-ls-files enumeration).
[ -f "$PAY/.omakase/gates/g.sh" ] && pass "gitignored gate captured into payload (the #1 regression)" || { fail "gitignored gate DROPPED"; echo "$OUT" | sed 's/^/      /'; }
[ -x "$PAY/.omakase/gates/g.sh" ] && pass "captured gate stays executable" || fail "gate lost +x"
# committed doctrine captured by identical path
[ -f "$PAY/AGENTS.md" ] && pass "committed AGENTS.md captured" || fail "AGENTS.md missing"
[ -f "$PAY/.claude/rules/style.md" ] && pass "committed rule captured" || fail "rule missing"
[ -f "$PAY/.claude/skills/demo/SKILL.md" ] && pass "committed skill captured" || fail "skill missing"
[ -f "$PAY/.claude/settings.json" ] && pass "settings.json captured" || fail "settings.json missing"
[ -f "$PAY/lefthook-local.yml" ] && pass "gitignored lefthook-local.yml captured" || fail "lefthook-local.yml missing"
# symlink carried AS a symlink
[ -L "$PAY/CLAUDE.md" ] && pass "CLAUDE.md captured as a symlink (cp -P)" || fail "CLAUDE.md not a symlink"
[ "$(readlink "$PAY/CLAUDE.md")" = "AGENTS.md" ] && pass "symlink target preserved" || fail "symlink target wrong"
# noise / personal NOT captured
[ ! -e "$PAY/.claude/settings.local.json" ] && pass "personal settings.local.json NOT captured" || fail "leaked settings.local.json"
[ ! -e "$PAY/.claude/worktrees" ] && pass "worktree decoy NOT captured" || fail "captured a worktree decoy"
# BLOCKER regression: a personal secret gitignored INSIDE a declared dir must NOT leak into payload
[ ! -e "$PAY/.claude/rules/personal.md" ] && pass "gitignored personal file inside .claude/rules NOT captured (no secret leak)" || fail "LEAKED a gitignored personal file into payload"
grep -rq 'SECRET=abc123' "$PAY" 2>/dev/null && fail "secret value present somewhere in payload" || pass "secret value absent from the entire payload"
echo "$OUT" | grep -qi 'skipped (personal' && pass "skipped personal file surfaced in the report" || fail "skipped personal file not surfaced"
# cut-over: tracked files left committed by default, reported (not un-tracked)
( cd "$SRC" && git ls-files --error-unmatch AGENTS.md >/dev/null 2>&1 ) && pass "default: committed AGENTS.md left tracked (no surprise un-track)" || fail "default import un-tracked a file"
echo "$OUT" | grep -qi 'still committed' && pass "report lists the still-committed cut-over set" || fail "report missing cut-over list"
[ -z "$(cd "$SRC" && git status --porcelain)" ] && pass "import did NOT mutate the source repo (working tree unchanged)" || { fail "import changed the source repo"; (cd "$SRC" && git status --porcelain | sed 's/^/      /'); }
# the printed cut-over command is GUARDED: no pre-baked confirmation; run VERBATIM it
# refuses and stages nothing. Import is agent-driven — an unattended copy/paste of the
# report's command must hit the review checkpoint, not stage shared-file deletions.
CUTCMD="$(echo "$OUT" | grep -- '--cut-over' | grep 'init.sh' | head -1 | sed 's/^[[:space:]]*//')"
[ -n "$CUTCMD" ] && pass "report prints a runnable cut-over command" || fail "no cut-over command printed"
echo "$CUTCMD" | grep -q 'OMAKASE_CUTOVER_CONFIRM' && fail "printed command pre-bakes OMAKASE_CUTOVER_CONFIRM (unattended hazard)" || pass "printed command does NOT pre-confirm"
CUTERR="$( eval "$CUTCMD" 2>&1 )" && fail "printed command run verbatim PROCEEDED (unattended cut-over)" || pass "printed command run verbatim refused (the review checkpoint)"
echo "$CUTERR" | grep -q 'OMAKASE_CUTOVER_CONFIRM' && pass "refusal names the confirmation guard (not an unrelated failure)" || fail "refusal did not name the confirmation guard ($CUTERR)"
[ -z "$(cd "$SRC" && git status --porcelain)" ] && pass "verbatim run staged nothing in the source repo" || { fail "verbatim run mutated the source repo"; (cd "$SRC" && git status --porcelain | sed 's/^/      /'); }
# leftover detection
echo "$OUT" | grep -q 'scripts/loose.sh' && pass "loose wired gate reported (not auto-grabbed)" || fail "loose gate not reported"
[ ! -e "$PAY/scripts/loose.sh" ] && pass "loose gate NOT captured (outside a declared location)" || fail "loose gate wrongly captured"
echo "$OUT" | grep -qi 'prettier' && pass "stack-coupled hook job flagged for review" || fail "stack job not flagged"

echo "== Scenario ROUNDTRIP: the captured payload is init-able =="
SCRATCH="$TMP/scratch"; rm -rf "$SCRATCH"; mkdir -p "$SCRATCH"
( cd "$SCRATCH" && git init -q && git config user.email t@t && git config user.name t && git config commit.gpgsign false && git commit -q --allow-empty -m init )
( cd "$SCRATCH" && OMAKASE_PAYLOAD="$PAY" bash "$INIT" ) >/dev/null 2>&1
[ -x "$SCRATCH/.omakase/gates/g.sh" ] && pass "captured payload injects: gate lands in a fresh repo (executable)" || fail "captured payload did not inject the gate"
[ -L "$SCRATCH/CLAUDE.md" ] && pass "captured payload injects the symlink" || fail "symlink did not inject"
[ -z "$(cd "$SCRATCH" && git status --porcelain)" ] && pass "injected captured payload is zero-footprint (git clean)" || { fail "captured payload injection not clean"; (cd "$SCRATCH" && git status --porcelain | sed 's/^/      /'); }

echo "== Scenario GUARD: refuses a payload that overlaps the source =="
SRCG="$TMP/srcg"; mksource "$SRCG"
( OMAKASE_PAYLOAD="$SRCG" bash "$IMPORT" "$SRCG" ) >/dev/null 2>&1 && fail "did NOT refuse payload == source" || pass "refused payload == source repo"
( OMAKASE_PAYLOAD="$SRCG/inside" bash "$IMPORT" "$SRCG" ) >/dev/null 2>&1 && fail "did NOT refuse payload nested in source" || pass "refused payload nested inside source"
[ ! -e "$SRCG/inside/AGENTS.md" ] && pass "nested-refusal wrote nothing into the source tree" || fail "nested payload contaminated the source tree"
ln -s "$SRCG" "$TMP/srcg-link"
( OMAKASE_PAYLOAD="$TMP/srcg-link" bash "$IMPORT" "$SRCG" ) >/dev/null 2>&1 && fail "guard bypassed via a symlinked payload path" || pass "guard resolves symlinks (payload symlink to source refused)"

rm -rf "$TMP"
echo ""
[ "$FAILED" -eq 0 ] && echo "ALL PASS" || { echo "FAILURES PRESENT"; exit 1; }
