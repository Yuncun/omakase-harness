#!/usr/bin/env bash
# TDD spec for the harness STATUS SURFACES:
#   - bin/status.sh : omakase status GUARDS chart (+ --markdown) and the inventory.
# (The statusline segment and the Stop-hook notice are binary subcommands now —
#  `omakase statusline` / `omakase stop-notice` — covered by the Go tests in
#  internal/probe, internal/render and cmd/omakase.)
# Ledger lines are TAB-separated (epoch, name, verdict, sha); assertions use
# awk, not grep -P (BSD).
set -u
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BANNER_REL=".omakase/bin/omakase-banner.sh"
SHOW="$HERE/../bin/status.sh"
INIT="$HERE/../bin/init.sh"
REMOVE="$HERE/../bin/remove.sh"
PAY="$HERE/../payload"
TMP="${TMPDIR:-/tmp}/omakase-status-test.$$"
NOW=1700000000
FAILED=0
pass(){ echo "  PASS: $1"; }
fail(){ echo "  FAIL: $1"; FAILED=1; }
newrepo(){ rm -rf "$1"; mkdir -p "$1"; ( cd "$1" && git init -q && git config user.email t@t && git config user.name t && git config commit.gpgsign false && git commit -q --allow-empty -m init ); }
ledger_of(){ echo "$(cd "$1" && cd "$(git rev-parse --git-common-dir)" && pwd)/omakase/ledger.tsv"; }
has_run(){ awk -F'\t' -v g="$2" -v v="$3" '$2==g && $3==v{f=1} END{exit f?0:1}' "$1"; }
# digest helpers (shasum on macOS, sha256sum on Linux); "nodigest" when neither exists —
# callers SKIP, matching the scripts' own degrade-to-silence rule.
if command -v shasum >/dev/null 2>&1; then shastr(){ printf '%s' "$1" | shasum -a 256 | awk '{print $1}'; }; shafile(){ shasum -a 256 < "$1" | awk '{print $1}'; }
elif command -v sha256sum >/dev/null 2>&1; then shastr(){ printf '%s' "$1" | sha256sum | awk '{print $1}'; }; shafile(){ sha256sum < "$1" | awk '{print $1}'; }
else shastr(){ echo nodigest; }; shafile(){ echo nodigest; }; fi

# Self-contained HOME + cache: init self-installs the resolved binary into
# $XDG_CACHE_HOME, so the real commit in Scenario U fires that copy through the
# permanent .git/hooks dispatchers. Scenarios pin their own $HOME below to
# control the personal inventory; $XDG_CACHE_HOME stays this fixture cache.
export HOME="$TMP/home"; export XDG_CACHE_HOME="$TMP/cache"
mkdir -p "$HOME" "$XDG_CACHE_HOME"

# Freeze the Go module + build caches to their real locations: scenarios pin $HOME
# to fixture dirs to control the personal inventory, which also relocates the
# default $HOME/go caches — without this the shim's `go build` re-downloads the
# whole module tree under each fixture HOME (cold cache, "go: downloading …" noise,
# and read-only module-cache residue rm -rf can't clear). Idiom inherited from the
# retired status-parity suite.
if command -v go >/dev/null 2>&1; then
  export GOMODCACHE="$(go env GOMODCACHE)"
  export GOCACHE="$(go env GOCACHE)"
fi

# ---------- Scenario S: omakase status surfaces a 4-col ledger verdict on the guards chart ----------
# Since #23 `show` lists gates from the manifest WIRING, joined to the latest ledger verdict.
# A 4-col row for the base payload's WIRED gate (markers) must surface with its verdict in both
# modes. Asserts on gate-name + verdict, not the exact header.
echo "== Scenario S: show surfaces a 4-col verdict on the guards chart =="
REPO="$TMP/repoS"; newrepo "$REPO"
( cd "$REPO" && OMAKASE_PAYLOAD="$PAY" bash "$INIT" ) >/dev/null 2>&1
LEDGER="$(ledger_of "$REPO")"; mkdir -p "$(dirname "$LEDGER")"
HEAD="$(cd "$REPO" && git rev-parse HEAD)"
printf '%s\tmarkers\tfail\t%s\n' $((NOW-60)) "$HEAD" >> "$LEDGER"
OUT="$( cd "$REPO" && OMAKASE_NOW=$NOW bash "$SHOW" 2>&1 )"
echo "$OUT" | grep -q 'markers' && pass "show lists the wired gate (4-col)" || fail "show missed 4-col gate"
echo "$OUT" | grep 'markers' | grep -q 'fail' && pass "show shows a fail verdict on the gate row" || fail "show missing fail verdict on the gate row"
OUT="$( cd "$REPO" && OMAKASE_NOW=$NOW bash "$SHOW" --markdown 2>&1 )"
echo "$OUT" | grep -qE '^\| *-+ *\|' && pass "markdown table renders" || fail "no markdown table"
echo "$OUT" | grep -E 'markers' | grep -q 'fail' && pass "markdown fail row (4-col)" || fail "no fail row in markdown"
( cd "$REPO" && OMAKASE_PAYLOAD="$PAY" bash "$REMOVE" ) >/dev/null 2>&1

# ---------- Scenario U: a real commit records a 4-col row through the wiring ----------
echo "== Scenario U: a real commit writes a 4-col ledger row through the wiring =="
REPO="$TMP/repoU"; newrepo "$REPO"
( cd "$REPO" && OMAKASE_PAYLOAD="$PAY" bash "$INIT" ) >/dev/null 2>&1
LEDGER="$(ledger_of "$REPO")"
( cd "$REPO" && echo hi > f.txt && git add f.txt && git commit -m t ) >/dev/null 2>&1
{ [ -f "$LEDGER" ] && has_run "$LEDGER" markers pass; } && pass "real commit recorded the example gate" || { fail "no pass row after a real commit"; sed 's/^/      /' "$LEDGER" 2>/dev/null; }
nf=$(awk -F'\t' '$2=="markers"{print NF; exit}' "$LEDGER")
[ "$nf" -eq 4 ] && pass "real commit row has 4 fields" || fail "real commit row has $nf fields"
( cd "$REPO" && OMAKASE_PAYLOAD="$PAY" bash "$REMOVE" ) >/dev/null 2>&1

# ---------- Scenario I: the inventory — every harness artifact, grouped by origin ----------
# spec §3: show gains an inventory (Committed / Injected / Personal), both modes,
# no token counts, audit view works even on an uninstalled repo.
echo "== Scenario I: show inventory groups harness artifacts by origin =="
REPO="$TMP/repoI"; newrepo "$REPO"
HOMEI="$TMP/homeI"; mkdir -p "$HOMEI/.claude/rules" "$HOMEI/.claude/skills/myskill"
printf 'global doctrine\n' > "$HOMEI/.claude/CLAUDE.md"
printf 'personal rule\n'   > "$HOMEI/.claude/rules/personal.md"
printf 'skill body\n'      > "$HOMEI/.claude/skills/myskill/SKILL.md"
mkdir -p "$REPO/.claude/rules" "$REPO/src"
printf 'team rule\n' > "$REPO/.claude/rules/team.md"
printf 'app\n'       > "$REPO/src/app.js"
( cd "$REPO" && git add .claude/rules/team.md src/app.js && git commit -qm files )

# not installed yet — the audit view still works
OUT="$( cd "$REPO" && HOME="$HOMEI" bash "$SHOW" 2>&1 )"
echo "$OUT" | grep -qi 'No omakase harness' && pass "not-installed message kept" || fail "not-installed message gone ($OUT)"
echo "$OUT" | grep -qiF "The project's harness" && pass "Committed group prints on an uninstalled repo" || fail "no Committed group when not installed"
echo "$OUT" | grep '\.claude/rules/team\.md' | grep -q 'rule' && pass "tracked harness file listed with kind rule" || fail "tracked rule missing or unkinded ($OUT)"
echo "$OUT" | grep -q 'src/app.js' && fail "non-harness tracked file leaked into the inventory" || pass "non-harness tracked file excluded"
echo "$OUT" | grep -qiF 'not installed by omakase' && pass "Global group prints on an uninstalled repo" || fail "no Global group when not installed"
echo "$OUT" | grep 'rules/personal\.md' | grep -q 'rule' && pass "personal rule listed from \$HOME" || fail "personal rule missing ($OUT)"
echo "$OUT" | grep 'CLAUDE\.md' | grep -q 'doc' && pass "personal CLAUDE.md listed as doc" || fail "personal CLAUDE.md missing"
[ "$(echo "$OUT" | grep -c 'skills/myskill')" -eq 1 ] && pass "personal skill dir is ONE row (not its files)" || fail "skill dir rows != 1"
echo "$OUT" | grep 'skills/myskill' | grep -q 'skill' && pass "personal skill dir carries kind skill" || fail "skill dir unkinded"
OUT="$( cd "$REPO" && HOME="$HOMEI" bash "$SHOW" --markdown 2>&1 )"
{ echo "$OUT" | grep -qi 'No omakase harness' && echo "$OUT" | grep -qiF "The project's harness"; } \
  && pass "markdown not-installed keeps the message and the Committed group" || fail "markdown not-installed inventory wrong ($OUT)"

# installed — injected rows come from the provenance ledger with source + kind.
# Install a copy of the base payload PLUS a .claude/settings.json, so there is an injected
# CONFIG row to hand-disable below. The base payload no longer ships one (the Stop-hook
# end-of-turn notice is opt-in), so the fixture adds it here.
PAYI="$TMP/payI"; rm -rf "$PAYI"; cp -R "$PAY/." "$PAYI/"; mkdir -p "$PAYI/.claude"
printf '{ "hooks": {} }\n' > "$PAYI/.claude/settings.json"
( cd "$REPO" && OMAKASE_PAYLOAD="$PAYI" bash "$INIT" ) >/dev/null 2>&1
PLACEDTSV="$(cd "$REPO" && cd "$(git rev-parse --git-common-dir)" && pwd)/omakase/placed.tsv"
awk -F'\t' -v OFS='\t' '$1==".claude/settings.json"{$5=0} 1' "$PLACEDTSV" > "$PLACEDTSV.tmp" && mv "$PLACEDTSV.tmp" "$PLACEDTSV"
OUT="$( cd "$REPO" && HOME="$HOMEI" bash "$SHOW" 2>&1 )"
echo "$OUT" | grep -qiF 'Injected (omakase)' && pass "Injected group prints when installed" || fail "no Injected group ($OUT)"
# The manifest gate wiring (omakase.manifest) is machinery — hidden while healthy —
# so the visible injected row to check for kind + source is the .claude/settings.json
# config the fixture added.
echo "$OUT" | grep '\.claude/settings\.json' | grep 'config' | grep -q 'payload' && pass "injected row carries kind + source value" || fail "injected row missing kind/source ($OUT)"
echo "$OUT" | grep '\.claude/settings\.json' | grep -qi 'disabled' && pass "hand-disabled row carries the disabled marker" || fail "disabled marker missing ($OUT)"
# omakase's own machinery (.omakase/) is not itemised in Injected; scope the absence check
# to that section (Guards may legitimately name an .omakase/ gate path in the ENFORCES cell).
INJ="$(echo "$OUT" | awk '/^INJECTED \(omakase\)/{f=1;next} /^GLOBAL /{f=0} f')"
echo "$INJ" | grep -q '\.omakase/' && fail "machinery files under .omakase/ leaked into the Injected list" || pass ".omakase/ machinery files excluded from the Injected list"
echo "$OUT" | grep '\.claude/rules/team\.md' | grep -qi 'payload' && fail "committed file leaked into the Injected group" || pass "committed file stays out of Injected"
echo "$OUT" | grep -qi 'token' && fail "output mentions tokens (explicitly cut from the spec)" || pass "no token counts anywhere (terminal)"

# markdown mode carries the same three groups
OUT="$( cd "$REPO" && HOME="$HOMEI" bash "$SHOW" --markdown 2>&1 )"
echo "$OUT" | grep -qiF "The project's harness" && pass "markdown: Committed group" || fail "markdown missing Committed group"
echo "$OUT" | grep -qiF 'Injected (omakase)' && pass "markdown: Injected group" || fail "markdown missing Injected group"
echo "$OUT" | grep -qiF 'not installed by omakase' && pass "markdown: Global group" || fail "markdown missing Global group"
echo "$OUT" | grep '\.claude/settings\.json' | grep -qi 'disabled' && pass "markdown: disabled marker carried" || fail "markdown lost the disabled marker"
INJ="$(echo "$OUT" | awk '/^### Injected/{f=1;next} /^### /{f=0} f')"
echo "$INJ" | grep -q '\.omakase/' && fail "markdown: machinery files under .omakase/ leaked into the Injected list" || pass "markdown: .omakase/ machinery files excluded from the Injected list"
echo "$OUT" | grep -qi 'token' && fail "markdown mentions tokens" || pass "no token counts anywhere (markdown)"

# status-UX: lead with footprint + identity, and promote Guards above the file inventory
echo "$OUT" | grep -qiF 'Zero footprint' && pass "markdown shows the zero-footprint line" || fail "markdown missing footprint line ($OUT)"
echo "$OUT" | grep -q 'base omakase' && pass "identity line names the base version" || fail "identity missing base-version ($OUT)"
gln=$(echo "$OUT" | grep -n '^### Guards' | head -1 | cut -d: -f1)
iln=$(echo "$OUT" | grep -n '^### Injected' | head -1 | cut -d: -f1)
{ [ -n "$gln" ] && [ -n "$iln" ] && [ "$gln" -lt "$iln" ]; } && pass "Guards renders above the file inventory" || fail "Guards not promoted above Injected (g=$gln i=$iln)"

# an empty Global group prints (none)
HOMEE="$TMP/homeEmpty"; mkdir -p "$HOMEE"
OUT="$( cd "$REPO" && HOME="$HOMEE" bash "$SHOW" 2>&1 )"
echo "$OUT" | grep -i -A1 'not installed by omakase' | grep -q '(none)' && pass "empty Global group shows (none)" || fail "empty Global group not (none) ($OUT)"
( cd "$REPO" && OMAKASE_PAYLOAD="$PAY" bash "$REMOVE" ) >/dev/null 2>&1

# ---------- Scenario I2: machinery visibility — healthy hidden, unhealthy surfaces ----------
# Reworked from the retired status-parity P11, which pinned .omakase/* machinery rows as
# ALWAYS hidden. Healthy machinery is still noise — with every machinery file present and
# matching its ledger hash the INJECTED inventory renders (none) — but an enabled
# machinery row MISSING from the checkout must surface (issue #84 gap 3: a gutted gate
# was invisible at rest; DRIFT surfacing is pinned in the Go goldens). The (none) check
# anchors to the line right AFTER the Injected header, per mode ("    (none)" term /
# "- _(none)_" md): a bare "(none)" would be satisfied by the empty Committed or Global
# sections too. The hand-built install declares no gates, so no real gate run is
# needed.
echo "== Scenario I2: healthy machinery hidden; missing machinery surfaces =="
inj_empty(){ # $1 = captured output, $2 = mode (term|md), $3 = label
  local hdr empty
  if [ "$2" = md ]; then hdr='^### Injected '; empty='- _(none)_'
  else                    hdr='^INJECTED ';     empty='    (none)'; fi
  if echo "$1" | awk -v hdr="$hdr" -v empty="$empty" \
      '$0 ~ hdr { g = NR } g && NR == g + 1 && $0 == empty { ok = 1 } END { exit ok ? 0 : 1 }'
  then pass "$3: Injected group renders empty (line after the header is '$empty')"
  else fail "$3: Injected group is NOT empty right after its header"; fi
}
if [ "$(shastr x)" = nodigest ]; then
  echo "  SKIP: no shasum/sha256sum — machinery health hashes cannot be built"
else
  REPOI2="$TMP/repoI2"; newrepo "$REPOI2"
  HOMEI2="$TMP/homeI2"; rm -rf "$HOMEI2"; mkdir -p "$HOMEI2"
  COMMONI2="$(cd "$REPOI2" && cd "$(git rev-parse --git-common-dir)" && pwd)"; mkdir -p "$COMMONI2/omakase"
  mkdir -p "$REPOI2/.omakase/bin" "$REPOI2/.omakase/gates"
  printf 'banner\n' > "$REPOI2/.omakase/bin/omakase-banner.sh"
  printf 'example gate\n' > "$REPOI2/.omakase/gates/example.sh"
  { printf '%s\t%s\t%s\t%s\t%s\n' '.omakase/bin/omakase-banner.sh' guard payload "$(shafile "$REPOI2/.omakase/bin/omakase-banner.sh")" 1
    printf '%s\t%s\t%s\t%s\t%s\n' '.omakase/gates/example.sh'      guard payload "$(shafile "$REPOI2/.omakase/gates/example.sh")" 1
  } > "$COMMONI2/omakase/placed.tsv"
  OUT="$( cd "$REPOI2" && HOME="$HOMEI2" bash "$SHOW" 2>&1 )"; RC=$?
  [ "$RC" -eq 0 ] && pass "I2: status exits 0 on the all-machinery install" || fail "I2: status exited $RC"
  inj_empty "$OUT" term "I2 [term, all healthy]"
  OUT="$( cd "$REPOI2" && HOME="$HOMEI2" bash "$SHOW" --markdown 2>&1 )"; RC=$?
  [ "$RC" -eq 0 ] && pass "I2: --markdown exits 0 on the all-machinery install" || fail "I2: --markdown exited $RC"
  inj_empty "$OUT" md "I2 [md, all healthy]"
  # gut one machinery file -> its row surfaces with the MISSING marker
  rm "$REPOI2/.omakase/gates/example.sh"
  OUT="$( cd "$REPOI2" && HOME="$HOMEI2" bash "$SHOW" 2>&1 )"
  echo "$OUT" | grep '\.omakase/gates/example\.sh' | grep -q 'MISSING' \
    && pass "I2 [term]: a missing machinery row surfaces with the MISSING marker" \
    || fail "I2 [term]: gutted machinery is still invisible ($OUT)"
  echo "$OUT" | grep '\.omakase/bin/omakase-banner\.sh' >/dev/null \
    && fail "I2 [term]: the still-healthy machinery row leaked into the listing" \
    || pass "I2 [term]: the still-healthy machinery row stays hidden"
fi

# ---------- Scenario I3: an INJECTED symlink row that is ALSO drifted ----------
# Ported from the retired status-parity suite (its P12). Drift for a symlink is a change
# to the LINK TARGET STRING, not the target's content: the ledger records the sha256 of
# the ORIGINAL readlink string; repointing the symlink at a DIFFERENT path must render
# the row as an arrow row ("->" term / "→" md) carrying the DRIFTED marker. The symlink
# is left untracked (drift returns false for a tracked path) and dangles on purpose
# (drift never dereferences it).
echo "== Scenario I3: drifted symlink row renders arrow + DRIFTED =="
if [ "$(shastr x)" = nodigest ]; then
  echo "  SKIP: no shasum/sha256sum on PATH — symlink drift cannot be computed"
else
  REPOI3="$TMP/repoI3"; newrepo "$REPOI3"
  COMMONI3="$(cd "$REPOI3" && cd "$(git rev-parse --git-common-dir)" && pwd)"; mkdir -p "$COMMONI3/omakase"
  printf '%s\t%s\t%s\t%s\t%s\n' '.claude/rules/link.md' rule payload "$(shastr 'orig-target.md')" 1 > "$COMMONI3/omakase/placed.tsv"
  mkdir -p "$REPOI3/.claude/rules"
  ( cd "$REPOI3/.claude/rules" && ln -s changed-target.md link.md )   # readlink 'changed-target.md' != ledger's 'orig-target.md' => DRIFTED
  OUT="$( cd "$REPOI3" && HOME="$HOMEI2" bash "$SHOW" 2>&1 )"
  echo "$OUT" | grep 'link\.md' | grep -F -- '-> changed-target.md' | grep -q 'DRIFTED' \
    && pass "I3 [term]: drifted symlink renders as an arrow row with the DRIFTED marker" \
    || fail "I3 [term]: no 'link.md -> changed-target.md' row carrying DRIFTED ($OUT)"
  OUT="$( cd "$REPOI3" && HOME="$HOMEI2" bash "$SHOW" --markdown 2>&1 )"
  echo "$OUT" | grep 'link\.md' | grep -F -- '→' | grep -q 'DRIFTED' \
    && pass "I3 [md]: drifted symlink renders as an arrow row with the DRIFTED marker" \
    || fail "I3 [md]: no 'link.md → …' row carrying DRIFTED ($OUT)"
fi

# ---------- Scenario W: branding (banner + version, no drift) ----------
echo "== Scenario W: branding =="
REPO="$TMP/repoW"; newrepo "$REPO"
( cd "$REPO" && OMAKASE_PAYLOAD="$PAY" bash "$INIT" ) >/dev/null 2>&1
BAN="$REPO/$BANNER_REL"
VER="$(cat "$PAY/.omakase/VERSION")"
PJV="$(grep -o '"version"[^,]*' "$HERE/../.claude-plugin/plugin.json" | grep -o '[0-9][0-9.]*')"
[ "$PJV" = "$VER" ] && pass "payload VERSION matches plugin.json ($PJV)" || fail "VERSION drift: plugin.json=$PJV payload=$VER"
OUT="$( cd "$REPO" && NO_COLOR=1 bash "$BAN" pre-commit )"
echo "$OUT" | grep -q 'omakase-harness' && pass "banner shows the plugin name" || fail "banner missing name"
echo "$OUT" | grep -q "v$VER" && pass "banner shows the version" || fail "banner missing version ($OUT)"
OUT="$( cd "$REPO" && bash "$SHOW" 2>&1 )"
echo "$OUT" | grep -q 'omakase-harness' && pass "show prints a branded header" || fail "show missing header"
( cd "$REPO" && OMAKASE_PAYLOAD="$PAY" bash "$REMOVE" ) >/dev/null 2>&1

rm -rf "$TMP"
echo ""
[ "$FAILED" -eq 0 ] && echo "ALL PASS" || { echo "FAILURES PRESENT"; exit 1; }
