#!/usr/bin/env bash
# omakase-worktree-guard — worktree discipline BEFORE the edit happens (issue #86).
# Opt-in Claude Code PreToolUse hook (matcher "Edit|Write"; init prints how to wire it).
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
# menu's persistent, visible disable).
#
# This layer fails OPEN: anything it cannot parse or resolve is allowed silently. It is
# a pre-layer for the developer's attention; the commit-time gate is the layer that
# fails closed. Copilot CLI does not run PreToolUse hooks — this is Claude-Code-only
# hardening, not the portable layer (that is the statusline warning + the gate).
set -uo pipefail
# A leaked GIT_DIR/GIT_WORK_TREE/GIT_COMMON_DIR (exported for ANOTHER repo) would judge
# the wrong repo's worktrees. Resolve from the hook's cwd only.
unset GIT_DIR GIT_WORK_TREE GIT_COMMON_DIR

[ "${OMAKASE_SKIP_WORKTREE_DISCIPLINE:-0}" = "1" ] && exit 0

input="$(cat)"
field() { printf '%s' "$input" | sed -n 's/.*"'"$1"'"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' | head -1; }
cwd="$(field cwd)"; [ -n "$cwd" ] || cwd="$PWD"
fp="$(field file_path)"; [ -n "$fp" ] || exit 0
case "$fp" in /*) : ;; *) fp="$cwd/$fp" ;; esac

root="$(git -C "$cwd" rev-parse --show-toplevel 2>/dev/null)" || exit 0
[ -n "$root" ] || exit 0

# Fire only in the MAIN checkout while other worktrees exist. The main checkout is the
# first `worktree` record; rev-parse and worktree-list both report physical paths, so
# string equality is the same test the commit gate uses.
wt="$(git -C "$cwd" worktree list --porcelain 2>/dev/null)" || exit 0
n="$(printf '%s\n' "$wt" | grep -c '^worktree ' 2>/dev/null || true)"
case "${n:-0}" in ''|*[!0-9]*) exit 0;; esac
[ "$n" -le 1 ] && exit 0
main="$(printf '%s\n' "$wt" | awk '/^worktree /{sub(/^worktree /,""); print; exit}')"
[ "$root" != "$main" ] && exit 0

# The menu's persistent disable stands the guard down with the gate.
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
esc="$(printf '%s' "$reason" | sed 's/\\/\\\\/g; s/"/\\"/g')"
printf '{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"%s"}}\n' "$esc"
exit 0
