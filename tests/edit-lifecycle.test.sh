#!/usr/bin/env bash
# The EDIT LIFECYCLE contract (issue #98 Part 2): editing a placed file is the
# expected lifecycle, not misuse — modified -> omakase diff -> keep / restore.
# End-to-end through the real binary against a controlled payload.
# Scenarios:
#   E1 edit -> the statusline goes amber; `omakase diff` shows MY change in the
#      forward direction (+ lines); the diff run writes nothing (ledger bytes +
#      edited-file mtime unchanged)
#   E2 `status --keep` -> green everywhere; deleting the kept file heals back
#      the ACCEPTED version via `omakase hook post-checkout`, not the harness's
#   E3 edit AFTER keep -> amber again; diff is measured against the accepted
#      version and says so
#   E4 `status --restore` -> harness version on disk, kept mark gone, green
#   E5 repair init leaves a kept file untouched, says "kept (yours)", and the
#      verdict counts it
#   E6 --keep/--restore refuse machinery and tracked paths with exit 2
#   E7 two-tier help: human verbs before the plumbing group; status --help
#      carries --keep/--restore
#   E8 remove leaves the kept file on disk (reported), deletes the rest
set -u
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
LEFTHOOK="${LEFTHOOK_BIN:-$(command -v lefthook || true)}"
OMAKASE="$( cd "$HERE/.." && HERE="$PWD/bin" && . bin/lib-omakase-bin.sh && resolve_omakase 2>/dev/null && echo "$OMAKASE_BIN_RESOLVED" )"
[ -n "$OMAKASE" ] || { echo "FATAL: no omakase binary resolvable"; exit 1; }
TMP="${TMPDIR:-/tmp}/omakase-edit-lifecycle-test.$$"
FAILED=0
pass(){ echo "  PASS: $1"; }
fail(){ echo "  FAIL: $1"; FAILED=1; }
newrepo(){ rm -rf "$1"; mkdir -p "$1"; ( cd "$1" && git init -q && git config user.email t@t && git config user.name t && git config commit.gpgsign false && git commit -q --allow-empty -m init ); }
common_of(){ echo "$(cd "$1" && cd "$(git rev-parse --git-common-dir)" && pwd)"; }
bar(){ ( cd "$1" && printf '{"workspace":{"current_dir":"%s"}}' "$1" | NO_COLOR=1 "$OMAKASE" statusline ); }

[ -n "$LEFTHOOK" ] && export PATH="$(dirname "$LEFTHOOK"):$PATH"
mkdir -p "$TMP"

# ---------- fixture: a controlled two-file payload ----------
PAYLOAD="$TMP/payload"
mkdir -p "$PAYLOAD/.claude/rules"
printf 'rule a\n' > "$PAYLOAD/.claude/rules/a.md"
printf 'rule b\n' > "$PAYLOAD/.claude/rules/b.md"
export OMAKASE_PAYLOAD="$PAYLOAD"

REPO="$TMP/repo"; newrepo "$REPO"
( cd "$REPO" && "$OMAKASE" init ) >/dev/null 2>&1 || fail "setup: init exited non-zero"
OMK="$(common_of "$REPO")/omakase"
A="$REPO/.claude/rules/a.md"

echo "E1: edit -> amber; diff is forward and read-only"
case "$(bar "$REPO")" in *"✓"*) pass "E1: statusline green before the edit";; *) fail "E1: statusline not green on a fresh install: $(bar "$REPO")";; esac
printf 'my edit\n' >> "$A"
BAR="$(bar "$REPO")"
case "$BAR" in *"harness files changed"*) pass "E1: statusline amber names the changed-files fact";; *) fail "E1: statusline after edit: $BAR";; esac
LEDGER_BEFORE="$(cat "$OMK/placed.tsv")"
touch -t 202001010000 "$A"   # pin an old mtime so any rewrite is visible
MTIME_BEFORE="$(ls -l "$A")"
DIFF_OUT="$(cd "$REPO" && "$OMAKASE" diff 2>&1)"; DIFF_RC=$?
[ "$DIFF_RC" -eq 0 ] || fail "E1: diff exited $DIFF_RC"
case "$DIFF_OUT" in *"+my edit"*) pass "E1: diff shows my line as an addition (forward direction)";; *) fail "E1: diff output: $DIFF_OUT";; esac
case "$DIFF_OUT" in *"vs the harness version"*) pass "E1: diff labels the harness baseline";; *) fail "E1: diff label missing";; esac
[ "$(cat "$OMK/placed.tsv")" = "$LEDGER_BEFORE" ] && pass "E1: diff left the ledger untouched" || fail "E1: diff rewrote placed.tsv"
[ "$(ls -l "$A")" = "$MTIME_BEFORE" ] && pass "E1: diff left the edited file untouched" || fail "E1: diff touched the edited file"

echo "E2: keep -> green; heal restores the ACCEPTED version"
( cd "$REPO" && "$OMAKASE" status --keep .claude/rules/a.md ) >/dev/null 2>&1 || fail "E2: --keep exited non-zero"
case "$(bar "$REPO")" in *"✓"*) pass "E2: statusline green after keep";; *) fail "E2: statusline after keep: $(bar "$REPO")";; esac
rm "$A"
( cd "$REPO" && "$OMAKASE" hook post-checkout ) >/dev/null 2>&1
if [ -f "$A" ] && grep -q "my edit" "$A"; then pass "E2: heal refilled the kept (accepted) version"
else fail "E2: heal content: $(cat "$A" 2>/dev/null || echo MISSING)"; fi

echo "E3: edit after keep -> amber; diff vs the accepted version"
printf 'second edit\n' >> "$A"
case "$(bar "$REPO")" in *"harness files changed"*) pass "E3: statusline amber after the post-keep edit";; *) fail "E3: statusline: $(bar "$REPO")";; esac
DIFF2="$(cd "$REPO" && "$OMAKASE" diff .claude/rules/a.md 2>&1)"
case "$DIFF2" in *"your accepted (kept) version"*) pass "E3: diff baseline is the accepted version";; *) fail "E3: diff label: $DIFF2";; esac
case "$DIFF2" in *"+second edit"*) pass "E3: only the new edit renders";; *) fail "E3: diff body: $DIFF2";; esac
case "$DIFF2" in *"+my edit"*) fail "E3: already-accepted edit re-rendered";; *) pass "E3: accepted edit not re-rendered";; esac

echo "E5: repair init preserves the kept file"
( cd "$REPO" && "$OMAKASE" status --keep .claude/rules/a.md ) >/dev/null 2>&1 || fail "E5: re-keep exited non-zero"
INIT_OUT="$(cd "$REPO" && "$OMAKASE" init 2>&1)"
grep -q "my edit" "$A" && grep -q "second edit" "$A" && pass "E5: kept content untouched by repair init" || fail "E5: repair init clobbered the kept file"
case "$INIT_OUT" in *"kept (yours"*) pass "E5: init summary names the kept file";; *) fail "E5: init output: $INIT_OUT";; esac
case "$INIT_OUT" in *"1 kept (yours)"*) pass "E5: init verdict counts the kept file";; *) fail "E5: verdict line missing the kept count";; esac

echo "E4: restore -> harness version back, kept mark gone, green"
( cd "$REPO" && "$OMAKASE" status --restore .claude/rules/a.md ) >/dev/null 2>&1 || fail "E4: --restore exited non-zero"
[ "$(cat "$A")" = "rule a" ] && pass "E4: harness version back on disk" || fail "E4: disk after restore: $(cat "$A")"
[ ! -e "$OMK/kept/.claude/rules/a.md" ] && pass "E4: kept mark cleared" || fail "E4: kept mark survived restore"
case "$(bar "$REPO")" in *"✓"*) pass "E4: statusline green after restore";; *) fail "E4: statusline: $(bar "$REPO")";; esac

echo "E6: --keep/--restore refuse machinery and tracked paths (exit 2)"
( cd "$REPO" && "$OMAKASE" status --keep .omakase ) >/dev/null 2>&1; RC=$?
[ "$RC" -eq 2 ] && pass "E6: --keep .omakase refused with exit 2" || fail "E6: --keep .omakase exit $RC"
( cd "$REPO" && git add -f .claude/rules/b.md && git commit -q -m track )
( cd "$REPO" && "$OMAKASE" status --restore .claude/rules/b.md ) >/dev/null 2>&1; RC=$?
[ "$RC" -eq 2 ] && pass "E6: --restore on a tracked path refused with exit 2" || fail "E6: tracked --restore exit $RC"
( cd "$REPO" && "$OMAKASE" status --keep nope.md ) >/dev/null 2>&1; RC=$?
[ "$RC" -eq 2 ] && pass "E6: unknown path refused with exit 2" || fail "E6: unknown path exit $RC"

echo "E7: two-tier help"
HELP="$("$OMAKASE" --help 2>&1)"
case "$HELP" in *"commands used by your tools, not by you:"*) pass "E7: plumbing tier present";; *) fail "E7: help: $HELP";; esac
HUMANS="${HELP%%commands used by your tools*}"
case "$HUMANS" in *"diff"*"remove"*) pass "E7: human verbs listed before the plumbing tier";; *) fail "E7: human tier: $HUMANS";; esac
SHELP="$(cd "$REPO" && "$OMAKASE" status --help 2>&1)"
case "$SHELP" in *"--keep PATH"*"--restore PATH"*) pass "E7: status --help carries keep/restore";; *) fail "E7: status help: $SHELP";; esac

echo "E8: remove leaves the kept file, deletes the rest"
printf 'mine now\n' >> "$A"
( cd "$REPO" && "$OMAKASE" status --keep .claude/rules/a.md ) >/dev/null 2>&1 || fail "E8: keep exited non-zero"
RM_OUT="$(cd "$REPO" && "$OMAKASE" remove 2>&1)"
[ -f "$A" ] && grep -q "mine now" "$A" && pass "E8: kept file survived remove" || fail "E8: kept file gone or clobbered"
case "$RM_OUT" in *".claude/rules/a.md"*"kept"*) pass "E8: remove reported the kept file";; *) fail "E8: remove output: $RM_OUT";; esac
[ ! -d "$OMK" ] && pass "E8: \$OMK torn down" || fail "E8: \$OMK survived remove"

rm -rf "$TMP"
echo ""
[ "$FAILED" -eq 0 ] && echo "ALL PASS" || { echo "FAILURES PRESENT"; exit 1; }
