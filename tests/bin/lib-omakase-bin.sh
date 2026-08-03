# shellcheck shell=bash
# TEST PLUMBING ONLY (since the plugin fold, #211): resolution of the omakase
# Go binary for the shell test suites — nothing user-facing sources this. NOT
# executed directly: it defines functions and runs nothing at source time. The
# sourcing scripts own `set -euo pipefail`; everything here is safe under
# `set -u`. Callers must set $HERE (a directory exactly one level below the
# repo root, e.g. tests/bin) before calling resolve_omakase.
#
# resolve_omakase sets $OMAKASE_BIN_RESOLVED to a runnable omakase in this order:
#   1. $OMAKASE_BIN override (tests, CI) — must be executable, or resolution
#      fails immediately (no fallthrough to tiers 2-5).
#   2. Dev rebuild: go.mod + go on PATH -> go build (a FAILING build aborts loudly
#      via an explicit exit — set -e alone would be suppressed here, since this
#      runs inside an if-condition's call chain — because a stale binary would
#      mask Go breakage).
#   3. dist/omakase — a prebuilt/vendored copy.
#   4. `omakase` on PATH (brew install yuncun/tap/omakase, or a manual install).
#   5. The stable machine copy at ${XDG_CACHE_HOME:-$HOME/.cache}/omakase/bin/
#      current/omakase — the path every real `omakase init` self-installs to
#      and the .git/hooks dispatchers exec.
# There is no network tier (issue #182). Resolution is local and offline,
# always.

# Print the one-line refusal for a verb whose binary could not be resolved.
# Agent-legible on purpose: in an agent host the agent reads this and offers
# to run the brew command — one consented step instead of a hidden download.
omakase_refuse_missing_binary() {  # $1 = verb name for the message
  echo "omakase: $1 needs the omakase binary — install it with: brew install yuncun/tap/omakase" >&2
}

# Resolve the omakase binary, setting $OMAKASE_BIN_RESOLVED. Returns non-zero
# when nothing resolves. Requires $HERE = the caller's bin/ directory.
resolve_omakase() {
  # The binary carries its own embedded base payload (since 0.27.0) and
  # resolves an on-disk one binary-relative for the dev loop, so the shims no
  # longer export OMAKASE_BASE_PAYLOAD (#172). A pre-set value still wins as
  # the documented dev/test override.
  # An OMAKASE_BIN override short-circuits resolution entirely: valid
  # (executable) -> use it; invalid -> fail now rather than falling through to
  # tiers 2-5 (tests rely on this to force a resolution failure
  # deterministically, e.g. OMAKASE_BIN=/nonexistent/omakase).
  if [ -n "${OMAKASE_BIN:-}" ]; then
    if [ -x "${OMAKASE_BIN}" ]; then OMAKASE_BIN_RESOLVED="$OMAKASE_BIN"; return 0; fi
    return 1
  fi
  if [ -f "$HERE/../../go.mod" ] && command -v go >/dev/null 2>&1; then
    # resolve_omakase is always called as `if resolve_omakase; then ...` from
    # the shims, and bash suppresses `set -e` throughout an if-condition's call
    # chain — so a failing `go build` here would NOT abort under the caller's
    # set -e and could fall through to a stale dist/omakase. The explicit
    # `exit 1` is immune to that suppression (this file is sourced, so it exits
    # the shim process) — do not simplify this back to a bare command.
    ( cd "$HERE/../.." && go build -o dist/omakase ./cmd/omakase ) || exit 1
  fi
  if [ -x "$HERE/../../dist/omakase" ]; then OMAKASE_BIN_RESOLVED="$HERE/../../dist/omakase"; return 0; fi
  if command -v omakase >/dev/null 2>&1; then OMAKASE_BIN_RESOLVED="omakase"; return 0; fi
  # The stable machine copy (hook.StableBinPath in Go): refreshed by every
  # real `omakase init` on this machine, and what the permanent .git/hooks
  # dispatchers exec — so if hooks work here, this resolves.
  local stable_bin="${XDG_CACHE_HOME:-$HOME/.cache}/omakase/bin/current/omakase"
  if [ -x "$stable_bin" ]; then OMAKASE_BIN_RESOLVED="$stable_bin"; return 0; fi
  return 1
}
