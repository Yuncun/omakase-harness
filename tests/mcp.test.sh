#!/usr/bin/env bash
# tests/mcp.test.sh — stdio smoke test for the `omakase mcp` verb (internal/mcpserver):
# raw newline-delimited JSON-RPC over stdio, no MCP client library, proving what a
# minimal host actually sees on the wire. Two cases: (1) inside a git repo, a real
# initialize / notifications-initialized / tools-list handshake lists both tools and
# carries the `anthropic/requiresUserInteraction` marker on "menu" (so a host never
# auto-answers the consent form on the human's behalf); (2) outside a git repo, the
# verb refuses immediately, before touching stdio at all.
#
# On case 1: closing stdin the INSTANT the last byte is written (a plain
# `printf ... | "$BIN" mcp`) races the server — confirmed by reading
# go-sdk@v1.6.1's internal/jsonrpc2/conn.go. The read side runs on its own goroutine
# and can observe stdin EOF before the handler goroutine (running the tools/list
# request) has written its response; write() then checks shuttingDown() FIRST, sees
# the (unrelated) read-side EOF, and fails the response write with "server is closing:
# EOF" — the process exits 1 with NO stdout, 100% reproducible under a bare
# `printf | binary`. That is a real gap upstream of internal/mcpserver (the SDK
# conflates the read half and write half of what is, for a stdio transport, two
# independent pipes) — a client that fires requests and closes its stdin write end
# promptly, without waiting to read the responses, loses them. It is also out of this
# task's scope (test file only, no source changes). The fix below is not a workaround
# for a hang: it matches how any real client actually behaves — keep the write end of
# the child's stdin open a beat after sending the last message, so the in-flight
# handler has time to respond before it can observe EOF. A bounded background wait
# (hand-rolled — no dependency on `timeout`/`gtimeout`, neither of which ships on stock
# macOS) still guards the whole call, so a future REAL hang fails the suite fast
# instead of hanging CI; the content assertions run against whatever came back either
# way, per house instruction, rather than being skipped.
#
# The settle window above only has margin once the binary is warm: the FIRST exec of
# a just-rebuilt dist/omakase on macOS is measurably slower (first-launch overhead —
# codesigning/AMFI checks, cold page cache), which alone can burn past a 0.3s settle
# and reintroduce the exact race above with no fault in the server at all. Case 2 (the
# no-stdio refusal path) runs FIRST for this reason: it execs the same binary once as
# a side effect of being a real assertion in its own right, which absorbs that
# first-exec tax before case 1's timing-sensitive handshake runs.
set -u
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="$HERE/.."
BIN="$ROOT/dist/omakase"
TMP="${TMPDIR:-/tmp}/omakase-mcp-test.$$"
SETTLE=0.3   # seconds stdin stays open after the last message (see header note above)
DEADLINE=10  # seconds to let the handshake pipeline finish before force-killing it
FAILED=0
pass(){ echo "  PASS: $1"; }
fail(){ echo "  FAIL: $1"; FAILED=1; }

# Freeze the Go module + build caches to their real locations (idiom of
# tests/scorecard.test.sh). This suite never overrides HOME, so it's a no-op in
# practice, but it matches the house's hermetic-build pattern.
if command -v go >/dev/null 2>&1; then
  export GOMODCACHE="$(go env GOMODCACHE)"
  export GOCACHE="$(go env GOCACHE)"
fi

# --- build/skip gate: only skip when there is NO binary AND NO go to build one ---
if [ ! -x "$BIN" ] && ! command -v go >/dev/null 2>&1; then
  echo "SKIP: dist/omakase absent and go not on PATH — the mcp smoke suite cannot run"
  exit 0
fi
if command -v go >/dev/null 2>&1; then
  ( cd "$ROOT" && CGO_ENABLED=0 go build -o dist/omakase ./cmd/omakase ) \
    || { echo "  FAIL: go build failed — cannot run the mcp smoke suite"; exit 1; }
fi

mkdir -p "$TMP"
newrepo(){ rm -rf "$1"; mkdir -p "$1"; ( cd "$1" && git init -q && git config user.email t@t && git config user.name t && git config commit.gpgsign false && git commit -q --allow-empty -m init ); }

echo "== mcp: refuses outside a git repo =="
R2="$TMP/notrepo"; mkdir -p "$R2"
OUT2="$( cd "$R2" && "$BIN" mcp </dev/null 2>"$TMP/err2" )"; RC2=$?
ERR2="$(cat "$TMP/err2" 2>/dev/null)"
[ "$RC2" -eq 1 ] && pass "case2: exit 1 outside a git repo" || fail "case2: exit $RC2 (want 1)"
[ -z "$OUT2" ] && pass "case2: stdout is empty" || fail "case2: stdout not empty: $OUT2"
echo "$ERR2" | grep -qF "not inside a git repo" \
  && pass "case2: stderr names the refusal" \
  || fail "case2: stderr missing 'not inside a git repo': $ERR2"

echo "== mcp: handshake + tool list (inside a git repo) =="
R1="$TMP/repo"; newrepo "$R1"

RPC='{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"smoke","version":"0"}}}
{"jsonrpc":"2.0","method":"notifications/initialized"}
{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}'

# run_handshake: the pipeline under test, isolated in a function so a single PID
# ($!) names the whole thing for the bounded wait below.
run_handshake(){
  cd "$R1" && { printf '%s\n' "$RPC"; sleep "$SETTLE"; } | "$BIN" mcp
}

run_handshake >"$TMP/out1" 2>"$TMP/err1" &
PID=$!
i=0; MAXI=$(( DEADLINE * 10 ))
while kill -0 "$PID" 2>/dev/null; do
  i=$((i+1))
  if [ "$i" -ge "$MAXI" ]; then kill -9 "$PID" 2>/dev/null; break; fi
  sleep 0.1
done
wait "$PID" 2>/dev/null
RC=$?
OUT="$(cat "$TMP/out1" 2>/dev/null)"
ERR="$(cat "$TMP/err1" 2>/dev/null)"

if [ "$i" -lt "$MAXI" ]; then pass "case1: server exited on its own (stdin close -> exit) before the ${DEADLINE}s deadline"
else fail "case1: server did not exit within ${DEADLINE}s of stdin closing — force-killed (rc=$RC, stderr: $ERR)"; fi
[ "$RC" -eq 0 ] && pass "case1: exit code 0" || fail "case1: exit code $RC (stderr: $ERR)"
case "$OUT" in
  *'"name":"status"'*) pass "case1: tools/list carries the status tool" ;;
  *) fail "case1: tools/list missing the status tool: $OUT" ;;
esac
case "$OUT" in
  *'"name":"menu"'*) pass "case1: tools/list carries the menu tool" ;;
  *) fail "case1: tools/list missing the menu tool: $OUT" ;;
esac
case "$OUT" in
  *requiresUserInteraction*) pass "case1: menu tool's _meta carries anthropic/requiresUserInteraction" ;;
  *) fail "case1: tools/list missing requiresUserInteraction: $OUT" ;;
esac

rm -rf "$TMP"
echo ""
[ "$FAILED" -eq 0 ] && echo "ALL PASS" || { echo "FAILURES PRESENT"; exit 1; }
