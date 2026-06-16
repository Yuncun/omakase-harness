#!/usr/bin/env bash
# Regression for the Copilot port: injecting into a SHARED top-level dir (.github) must
# exclude the exact placed files, NEVER the whole directory — otherwise omakase would hide
# the project's own untracked .github/ files (workflows, instructions a dev is authoring, …).
# Uses lefthook from PATH (as CI does); no hardcoded paths.
set -u
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
INIT="$HERE/../bin/init.sh"
TMP="${TMPDIR:-/tmp}/omakase-copilot-exclude.$$"
PAY="$TMP/payload"; REPO="$TMP/repo"
FAILED=0
pass(){ echo "  PASS: $1"; }
fail(){ echo "  FAIL: $1"; FAILED=1; }

mkdir -p "$PAY/.omakase/gates" "$PAY/.github/skills/demo"
printf '#!/usr/bin/env bash\nexit 0\n' > "$PAY/.omakase/gates/example.sh"
printf -- '---\nname: demo\ndescription: d\n---\nhi\n' > "$PAY/.github/skills/demo/SKILL.md"
cat > "$PAY/lefthook-local.yml" <<'YML'
pre-commit:
  jobs:
    - name: omakase-example
      run: bash .omakase/gates/example.sh
post-checkout:
  jobs:
    - name: omakase-ensure-present
      run: bash "$(git rev-parse --git-common-dir)/omakase/ensure-present.sh"
YML

rm -rf "$REPO"; mkdir -p "$REPO"
( cd "$REPO" \
  && git init -q && git config user.email t@t && git config user.name t && git config commit.gpgsign false \
  && mkdir -p .github/workflows && printf 'name: ci\n' > .github/workflows/ci.yml \
  && git add .github/workflows/ci.yml && git commit -q -m "repo has its own committed .github/" )

if ! ( cd "$REPO" && OMAKASE_PAYLOAD="$PAY" bash "$INIT" >/dev/null 2>&1 ); then
  echo "  FAIL: init errored (is lefthook on PATH?)"; rm -rf "$TMP"; exit 1
fi

# 1. the injected skill IS ignored (zero committed footprint still holds)
if ( cd "$REPO" && git check-ignore -q .github/skills/demo/SKILL.md ); then
  pass "injected .github/skills file is ignored"; else fail "injected skill is not ignored"; fi

# 2. an UNRELATED untracked .github/ file is NOT ignored — the footgun this fixes
( cd "$REPO" && printf 'x\n' > .github/scratch.txt )
if ( cd "$REPO" && git check-ignore -q .github/scratch.txt ); then
  fail "unrelated .github/scratch.txt was hidden (whole .github/ excluded)"
else pass "unrelated .github/ file stays visible to git"; fi

# 3. the exclude block names the exact file, not the bare dir
if ( cd "$REPO" && grep -qx '\.github/skills/demo/SKILL\.md' .git/info/exclude ); then
  pass "exclude lists the exact file"; else fail "exclude does not list the exact file"; fi
if ( cd "$REPO" && grep -qx '\.github/' .git/info/exclude ); then
  fail "exclude over-broadly lists bare .github/"; else pass "exclude has no bare .github/"; fi

[ "$FAILED" -eq 0 ] && echo "copilot-exclude-scope.test.sh: ALL PASS" || echo "copilot-exclude-scope.test.sh: FAILURES"
rm -rf "$TMP"
exit "$FAILED"
