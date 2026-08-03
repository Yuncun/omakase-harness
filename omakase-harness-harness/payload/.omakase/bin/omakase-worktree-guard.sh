#!/usr/bin/env bash
# omakase-worktree-guard — worktree discipline BEFORE the edit happens (issue #86).
# HARNESS POLICY, not omakase machinery (#172): this script ships in the
# omakase-harness-harness payload — a harness that wants the discipline carries its
# own copy. The omakase binary knows nothing about it.
# Opt-in Claude Code PreToolUse hook (matcher "Edit|Write"); wire it in
# .claude/settings.json as:  bash "$CLAUDE_PROJECT_DIR/.omakase/bin/omakase-worktree-guard.sh"
# While other worktrees are active, an Edit/Write to a product file in the MAIN checkout
# is denied with a teaching message: branches cut in the main checkout inherit concurrent
# sessions' uncommitted work, which then leaks into a PR. Implementation goes in a
# worktree; the main checkout is for harness/coordination files.
#
# The allowlist mirrors the commit-time gate's (AGENTS.md, CLAUDE.md, .claude/**, root
# *.md) plus two classes an EDIT-time layer must not block because they cannot leak into
# a commit: .omakase/** (the placed overlay, force-excluded from git) and .git/**.
#
# Standdowns, same as the gate: OMAKASE_SKIP_WORKTREE_DISCIPLINE=1 (audited, per
# invocation) or a "worktree-discipline" line in the shared disabled-gates file (the
# persistent, visible disable via `omakase status --disable`).
#
# This layer fails OPEN: anything it cannot parse or resolve is allowed silently. It is
# a pre-layer for the developer's attention; the commit-time gate is the layer that
# fails closed. Copilot CLI (1.0.75+) DOES run PreToolUse hooks via a plugin's
# hooks/hooks.json — but it only reads the nested hookSpecificOutput shape when the hook
# entry declares _vsCodeCompat; otherwise it looks for a top-level permissionDecision.
# The deny below therefore emits BOTH shapes so a dropped deny can't fail open there
# (#164 C3). Wiring it into Copilot is a separate step (#164 C5); Claude Code wiring is
# unchanged.
set -uo pipefail
# A leaked GIT_DIR/GIT_WORK_TREE/GIT_COMMON_DIR (exported for ANOTHER repo) would judge
# the wrong repo's worktrees. Resolve from the hook's cwd only.
unset GIT_DIR GIT_WORK_TREE GIT_COMMON_DIR

[ "${OMAKASE_SKIP_WORKTREE_DISCIPLINE:-0}" = "1" ] && exit 0

input="$(cat)"
field() { printf '%s' "$input" | sed -n 's/.*"'"$1"'"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' | head -1; }
# One canonical path form (Windows/git-bash): the host sends C:/-style paths
# while $PWD and git may use /c/-style — mixed forms break every prefix test
# below, and the failure direction would be a wrong DENY on allowlisted
# files. cygpath exists wherever this runs under Git for Windows; elsewhere
# norm is the identity.
if command -v cygpath >/dev/null 2>&1; then
  norm() { cygpath -u -- "$1" 2>/dev/null || printf '%s' "$1"; }
else
  norm() { printf '%s' "$1"; }
fi
cwd="$(norm "$(field cwd)")"; [ -n "$cwd" ] || cwd="$(norm "$PWD")"
fp="$(field file_path)"; [ -n "$fp" ] || exit 0
fp="$(norm "$fp")"
# Drive-letter paths count as absolute even when cygpath is unavailable
# (a bash without cygpath can still receive C:/-form host paths) — treating
# one as relative would mangle it into a wrong DENY on allowlisted files.
case "$fp" in /*|[A-Za-z]:/*|[A-Za-z]:\\*) : ;; *) fp="$cwd/$fp" ;; esac

root="$(git -C "$cwd" rev-parse --show-toplevel 2>/dev/null)" || exit 0
[ -n "$root" ] || exit 0
root="$(norm "$root")"

# Fire only in the MAIN checkout while other worktrees exist. The main checkout is the
# first `worktree` record; rev-parse and worktree-list both report physical paths, so
# string equality is the same test the commit gate uses.
wt="$(git -C "$cwd" worktree list --porcelain 2>/dev/null)" || exit 0
n="$(printf '%s\n' "$wt" | grep -c '^worktree ' 2>/dev/null || true)"
case "${n:-0}" in ''|*[!0-9]*) exit 0;; esac
[ "$n" -le 1 ] && exit 0
main="$(norm "$(printf '%s\n' "$wt" | awk '/^worktree /{sub(/^worktree /,""); print; exit}')")"
[ "$root" != "$main" ] && exit 0

# A persistent disable (status --disable) stands the guard down with the gate.
gcd="$(git -C "$cwd" rev-parse --git-common-dir 2>/dev/null)" || exit 0
common="$(cd "$cwd" 2>/dev/null && cd "$gcd" 2>/dev/null && pwd)" || exit 0
grep -Fxq -- "worktree-discipline" "$common/omakase/disabled-gates" 2>/dev/null && exit 0

# Only files INSIDE this checkout are its contamination; editing another worktree's
# files from here is the desired flow.
case "$fp" in
  "$root"/*) rel="${fp#"$root"/}" ;;
  *) exit 0 ;;
esac

# The allowlist. `*/*` catches every nested non-allowlisted path first, so the final
# `*.md` arm can only match ROOT-level markdown (in a `case` pattern, * spans slashes).
case "$rel" in
  AGENTS.md|CLAUDE.md|.claude/*|.omakase/*|.git/*) exit 0 ;;
  */*) : ;;
  *.md) exit 0 ;;
esac

others=$((n - 1))
reason="omakase worktree discipline: '$rel' is a product file and this is the MAIN checkout while $others other worktree(s) are active. Branches cut here inherit concurrent sessions' uncommitted work. Edit it in a worktree instead (the main checkout is for coordination: AGENTS.md, CLAUDE.md, .claude/**, root *.md). Bypass (audited): OMAKASE_SKIP_WORKTREE_DISCIPLINE=1."
# JSON-escape (backslash, quote); rel is the only interpolated data.
# Both output shapes on purpose: Claude Code reads the nested hookSpecificOutput block;
# Copilot CLI without _vsCodeCompat reads only the top-level keys and would silently
# drop a nested-only deny — the one failure mode a guard must not have (#164 C3).
esc="$(printf '%s' "$reason" | sed 's/\\/\\\\/g; s/"/\\"/g')"
printf '{"permissionDecision":"deny","permissionDecisionReason":"%s","hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"%s"}}\n' "$esc" "$esc"
exit 0
