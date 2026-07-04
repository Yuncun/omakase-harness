#!/usr/bin/env bash
# omakase-harness xray — the agentic x-ray. PROTOTYPE (branch proto/agentic-xray).
#
# One read-only view of EVERY agent-facing file in this repo — instruction docs, rules,
# skills, agent hooks, git-hook gates, MCP config, agent-aware CI — organized by WHEN it
# affects development:
#
#   SESSION START   always in the agent's context (costs tokens every session)
#   ON DEMAND       loaded only when invoked (skills, commands, subagents, prompts)
#   DURING SESSION  agent lifecycle hooks + the config that defines them
#   ON COMMIT/PUSH  git-hook gates
#   TOOLS           MCP servers the agent can launch
#   CI              agent-aware workflows (server-side)
#
# followed by FINDINGS — cruft + risk heuristics: committed executable config (runs on
# every contributor's machine), context bloat, duplicated instruction files, dead
# references, deprecated forms, gate config that is not wired to any hook, and agent
# tooling delivered as submodules. Works on ANY repo, with or without an omakase install.
#
# Known gap (prototype): gitignored agent files are only found via the omakase ledger or
# the explicit probe list below — a user's own gitignored files elsewhere are not scanned
# (listing every ignored path would drag in node_modules and friends).
#
# Two output modes, mirroring status.sh:
#   (default)    terminal
#   --markdown   Markdown — relay VERBATIM into chat so the script owns the formatting.
set -euo pipefail

FORMAT=term
case "${1:-}" in --markdown|-m|md) FORMAT=md;; esac
ICON="${OMAKASE_ICON:-🥡}"

ROOT="$(git rev-parse --show-toplevel 2>/dev/null)" || { echo "omakase: not inside a git repo" >&2; exit 1; }
COMMON="$(cd "$ROOT" && cd "$(git rev-parse --git-common-dir)" && pwd)"
PLACED="$COMMON/omakase/placed.tsv"

TMPD="$(mktemp -d "${TMPDIR:-/tmp}/omakase-xray.XXXXXX")"
trap 'rm -rf "$TMPD"' EXIT
ROWS="$TMPD/rows.tsv"          # order \t stage \t path \t tool \t prov \t note
FIND="$TMPD/findings.tsv"      # sev(1 risk / 2 cruft / 3 info) \t text
: > "$ROWS"; : > "$FIND"

# ---------------- candidate paths ----------------
# tracked + untracked-unignored (one git call), plus injected paths from the omakase
# provenance ledger (they are gitignored, so ls-files misses them), plus a short probe
# list of well-known gitignored-by-convention files.
{
  git -C "$ROOT" -c core.quotePath=false ls-files -co --exclude-standard 2>/dev/null || true
  [ -f "$PLACED" ] && cut -f1 "$PLACED"
  for f in CLAUDE.local.md .claude/settings.local.json .mcp.json lefthook-local.yml \
           .cursor/mcp.json .vscode/mcp.json; do
    [ -e "$ROOT/$f" ] && printf '%s\n' "$f"
  done
  true   # the last probe may test-fail; the block's exit code must stay 0 under pipefail
} | sort -u > "$TMPD/paths"

# Pre-filter to plausible agent paths so the classify loop never walks a whole monorepo.
grep -E '(^|/)AGENTS\.md$|(^|/)CLAUDE(\.local)?\.md$|^GEMINI\.md$|^\.github/(copilot-instructions\.md$|instructions/|prompts/|chatmodes/|skills/|hooks/|workflows/[^/]+\.ya?ml$)|^\.cursorrules$|^\.cursor/(rules/|mcp\.json$)|^\.claude/|^\.kiro/(steering/|settings/mcp\.json$)|^\.windsurfrules$|^\.windsurf/rules/|^lefthook(-local)?\.ya?ml$|^\.lefthook/|^\.husky/|^\.githooks/|^\.pre-commit-config\.ya?ml$|^\.omakase/gates/|^\.mcp\.json$|^\.vscode/mcp\.json$' \
  "$TMPD/paths" > "$TMPD/cands" || true

prov_of() {  # committed (tracked) / injected (omakase ledger) / local (loose)
  if git -C "$ROOT" ls-files --error-unmatch -- "$1" >/dev/null 2>&1; then echo committed
  elif [ -f "$PLACED" ] && cut -f1 "$PLACED" | grep -Fxq -- "$1"; then echo injected
  else echo local; fi
}

add_row() { printf '%s\t%s\t%s\t%s\t%s\t%s\n' "$1" "$2" "$3" "$4" "$5" "$6" >> "$ROWS"; }

while IFS= read -r p; do
  [ -n "$p" ] || continue
  order=""; stage=""; tool=""; note=""; rowpath="$p"
  case "$p" in
    # --- SESSION START: always in context ---
    AGENTS.md)                             order=1; stage=start; tool=all;;
    */AGENTS.md)                           order=1; stage=start; tool=all;    note="nested: loads only in its subtree";;
    CLAUDE.local.md)                       order=1; stage=start; tool=claude; note="personal, local-only";;
    CLAUDE.md)                             order=1; stage=start; tool=claude;;
    */CLAUDE.md)                           order=1; stage=start; tool=claude; note="nested: loads only in its subtree";;
    GEMINI.md)                             order=1; stage=start; tool=gemini;;
    .github/copilot-instructions.md)       order=1; stage=start; tool=copilot;;
    .github/instructions/*)                order=1; stage=start; tool=copilot;;
    .cursorrules)                          order=1; stage=start; tool=cursor; note="deprecated form";;
    .cursor/rules/*)                       order=1; stage=start; tool=cursor;;
    .claude/rules/*)                       order=1; stage=start; tool=claude;;
    .kiro/steering/*)                      order=1; stage=start; tool=kiro;;
    .windsurfrules|.windsurf/rules/*)      order=1; stage=start; tool=windsurf;;
    # --- ON DEMAND: loaded when invoked ---
    .claude/skills/*)   order=2; stage=demand; tool=claude;  r="${p#.claude/skills/}";  rowpath=".claude/skills/${r%%/*}/";;
    .github/skills/*)   order=2; stage=demand; tool=copilot; r="${p#.github/skills/}";  rowpath=".github/skills/${r%%/*}/";;
    .claude/commands/*)                    order=2; stage=demand; tool=claude;;
    .claude/agents/*)                      order=2; stage=demand; tool=claude;;
    .github/prompts/*|.github/chatmodes/*) order=2; stage=demand; tool=copilot;;
    # --- DURING SESSION: agent lifecycle hooks + config ---
    .claude/hooks/*)                       order=3; stage=session; tool=claude;;
    .github/hooks/*)                       order=3; stage=session; tool=copilot;;
    .claude/settings.json|.claude/settings.*.json)
      order=3; stage=session; tool=claude
      if grep -q '"hooks"' "$ROOT/$p" 2>/dev/null; then note="defines hooks"; else note="config, no hooks"; fi;;
    # --- ON COMMIT/PUSH: git-hook gates ---
    .husky/_/*) continue;;   # husky's own runner internals, not user gates
    lefthook.yml|lefthook.yaml|lefthook-local.yml|.lefthook/*) order=4; stage=githook; tool=any;;
    .husky/*|.githooks/*)                  order=4; stage=githook; tool=any;;
    .pre-commit-config.yaml|.pre-commit-config.yml) order=4; stage=githook; tool=any;;
    .omakase/gates/*)                      order=4; stage=githook; tool=omakase;;
    # --- TOOLS: MCP servers ---
    .mcp.json)                             order=5; stage=tools; tool=claude;;
    .cursor/mcp.json)                      order=5; stage=tools; tool=cursor;;
    .vscode/mcp.json)                      order=5; stage=tools; tool=vscode;;
    .kiro/settings/mcp.json)               order=5; stage=tools; tool=kiro;;
    # --- CI: agent-aware workflows only ---
    .github/workflows/*)
      # agent markers only — actions/keys that mean an agent RUNS here, never a bare
      # repo self-reference (owner/repo strings appear in ordinary CI too)
      grep -qiE 'claude-code-action|copilot-swe-agent|codex-action|githubnext/gh-aw|ANTHROPIC_API_KEY|OPENAI_API_KEY|CLAUDE_CODE_OAUTH_TOKEN' "$ROOT/$p" 2>/dev/null || continue
      order=6; stage=ci; tool=ci;;
    *) continue;;
  esac
  add_row "$order" "$stage" "$rowpath" "$tool" "$(prov_of "$p")" "$note"
done < "$TMPD/cands"

sort -u "$ROWS" -o "$ROWS"

# ---------------- findings ----------------
add_finding() { printf '%s\t%s\n' "$1" "$2" >> "$FIND"; }   # 1=risk 2=cruft 3=info

# F1: committed executable agent config — runs on every contributor's machine with no
# opt-in beyond the vendor trust dialog. Git hooks fire on commit, MCP entries launch
# processes, agent hooks run in-session. (Config rows without hooks are excluded.)
exec_committed="$(awk -F'\t' '($2=="githook"||$2=="session"||$2=="tools") && $5=="committed" && $6!="config, no hooks" {print $3}' "$ROWS" | sort -u | tr '\n' ' ')"
if [ -n "${exec_committed% }" ]; then
  add_finding 1 "committed executable agent config: ${exec_committed% } — this runs code on every contributor's machine (git hooks on commit, MCP servers as processes, agent hooks in-session) with no opt-in beyond the tool's trust dialog. Consider opt-in delivery (an omakase overlay) or moving enforcement to CI."
fi

# F2: context bloat — a session-start file this large is paid for in every session.
while IFS=$'\t' read -r _ stage rel _ _ _; do
  [ "$stage" = start ] || continue
  f="$ROOT/$rel"; [ -f "$f" ] || continue
  lines="$(wc -l < "$f" | tr -d ' ')"; chars="$(wc -c < "$f" | tr -d ' ')"; tok=$((chars / 4))
  if [ "$lines" -gt 300 ] || [ "$tok" -gt 2500 ]; then
    add_finding 2 "context bloat: $rel is $lines lines (~$tok tokens, rough) and is loaded into EVERY session — move detail into on-demand skills or linked docs and keep the always-on file a short map."
  fi
done < "$ROWS"

# F3: AGENTS.md + CLAUDE.md drift — two always-on sources of truth.
if [ -e "$ROOT/AGENTS.md" ] && { [ -e "$ROOT/CLAUDE.md" ] || [ -L "$ROOT/CLAUDE.md" ]; }; then
  if [ -L "$ROOT/CLAUDE.md" ] && [ "$(readlink "$ROOT/CLAUDE.md")" = "AGENTS.md" ]; then
    add_finding 3 "CLAUDE.md is a symlink to AGENTS.md — one source of truth, correctly linked."
  elif cmp -s "$ROOT/AGENTS.md" "$ROOT/CLAUDE.md" 2>/dev/null; then
    add_finding 2 "AGENTS.md and CLAUDE.md are identical copies — symlink one to the other so they cannot drift apart."
  else
    add_finding 2 "AGENTS.md and CLAUDE.md DIFFER — two sources of truth; a tool that reads only one misses the other. Merge them (or symlink) and keep tool-specific notes in tool-specific rules files."
  fi
fi

# F4: dead references — backticked repo paths in root instruction files that don't exist.
# Conservative: token must contain '/', no URL/glob/var characters, and its first path
# segment must exist in the repo (filters out owner/repo slugs and prose).
for rel in AGENTS.md CLAUDE.md GEMINI.md .github/copilot-instructions.md; do
  f="$ROOT/$rel"; [ -f "$f" ] || continue
  dead=""
  while IFS= read -r tok; do
    case "$tok" in */*) ;; *) continue;; esac                                  # must look like a path
    case "$tok" in /*) continue;; esac                                         # absolute path or /slash-command
    case "$tok" in http*|*'$'*|*'*'*|*'{'*|*'<'*|*'>'*|*' '*) continue;; esac  # URL/glob/var/placeholder
    tok="${tok#./}"; tok="${tok%%:*}"
    [ -e "$ROOT/$tok" ] && continue
    [ -e "$ROOT/${tok%%/*}" ] || continue
    dead="$dead $tok"
    n=0; for _t in $dead; do n=$((n+1)); done; [ "$n" -ge 5 ] && break
  done < <(grep -oE '`[^` ]+`' "$f" 2>/dev/null | tr -d '\`' | sort -u)
  [ -n "$dead" ] && add_finding 2 "dead references in $rel:$dead — the agent is being pointed at paths that no longer exist."
done

# F5: deprecated form.
[ -e "$ROOT/.cursorrules" ] && add_finding 2 ".cursorrules is the deprecated Cursor format — migrate to .cursor/rules/."

# F6: gate config that never runs — config without wiring is documentation.
if awk -F'\t' '$2=="githook"{f=1} END{exit f?0:1}' "$ROWS"; then
  hp="$(git -C "$ROOT" config core.hooksPath 2>/dev/null || true)"
  case "$hp" in "") hp="$COMMON/hooks";; /*) ;; *) hp="$ROOT/$hp";; esac
  if [ ! -f "$hp/pre-commit" ] && [ ! -f "$hp/pre-push" ]; then
    add_finding 2 "gate config is present but NO git hook is installed ($hp has no pre-commit/pre-push) — these gates never actually run. Wire them (lefthook install / pre-commit install / omakase init) or delete the config."
  fi
fi

# F7: agent tooling delivered as a submodule (referenced from an instruction file).
if [ -f "$ROOT/.gitmodules" ]; then
  while IFS= read -r sub; do
    [ -n "$sub" ] || continue
    for rel in AGENTS.md CLAUDE.md GEMINI.md .github/copilot-instructions.md; do
      [ -f "$ROOT/$rel" ] || continue
      if grep -qF -- "$sub" "$ROOT/$rel" 2>/dev/null; then
        add_finding 3 "agent tooling via submodule: $sub is referenced from $rel — every adopter must understand that tooling to benefit from it; it is often cheaper for them to have their own agent build the equivalent. Consider serving it as an opt-in harness instead."
        break
      fi
    done
  done < <(git -C "$ROOT" config -f .gitmodules --get-regexp 'submodule\..*\.path' 2>/dev/null | awk '{print $2}')
fi

sort -n "$FIND" -o "$FIND"

# ---------------- render ----------------
stage_title() {
  case "$1" in
    start)   echo "SESSION START — always in the agent's context";;
    demand)  echo "ON DEMAND — loaded when invoked (skills, commands, subagents, prompts)";;
    session) echo "DURING SESSION — agent lifecycle hooks + config";;
    githook) echo "ON COMMIT/PUSH — git-hook gates";;
    tools)   echo "TOOLS — MCP servers the agent can launch";;
    ci)      echo "CI — agent-aware workflows (server-side)";;
  esac
}

size_note() {  # only for regular files in the start stage
  f="$ROOT/$1"; [ -f "$f" ] || { echo ""; return; }
  lines="$(wc -l < "$f" | tr -d ' ')"; chars="$(wc -c < "$f" | tr -d ' ')"
  echo "$lines lines, ~$((chars / 4)) tok"
}

total_start_tok() {  # root-scope only: nested files load per-subtree, not every session
  t=0
  while IFS=$'\t' read -r _ stage rel _ _ note; do
    [ "$stage" = start ] || continue
    case "$note" in nested:*) continue;; esac
    f="$ROOT/$rel"; [ -f "$f" ] || continue
    t=$((t + $(wc -c < "$f" | tr -d ' ') / 4))
  done < "$ROWS"
  echo "$t"
}

nrows="$(grep -c . "$ROWS" 2>/dev/null || true)"; [ -n "$nrows" ] || nrows=0
tok="$(total_start_tok)"

if [ "$FORMAT" = md ]; then
  echo "## $ICON agentic x-ray — $(basename "$ROOT")"
  echo
  echo "$nrows agent-facing artifact(s); session-start context ≈ **$tok tokens** (rough, bytes/4). Provenance: committed = tracked by git · injected = omakase overlay (gitignored) · local = loose/untracked."
  for s in start demand session githook tools ci; do
    body="$(awk -F'\t' -v s="$s" '$2==s' "$ROWS")"
    [ -n "$body" ] || continue
    echo
    echo "### $(stage_title "$s")"
    printf '%s\n' "$body" | while IFS=$'\t' read -r _ _ rel tool prov note; do
      extra=""
      [ "$s" = start ] && { sz="$(size_note "$rel")"; [ -n "$sz" ] && extra=" — $sz"; }
      [ -n "$note" ] && extra="$extra — $note"
      echo "- \`$rel\` — $tool, **$prov**$extra"
    done
  done
  echo
  echo "### Findings"
  if [ -s "$FIND" ]; then
    while IFS=$'\t' read -r sev text; do
      case "$sev" in 1) tag="**RISK**";; 2) tag="**CRUFT**";; *) tag="_info_";; esac
      echo "- $tag — $text"
    done < "$FIND"
  else
    echo "- _(none — clean)_"
  fi
  echo
  echo "_Read-only; running xray changes nothing. Prototype: output shape is not contract._"
else
  echo "$ICON agentic x-ray — $(basename "$ROOT")"
  echo "$nrows agent-facing artifact(s); session-start context ~ $tok tokens (rough)"
  echo "provenance: committed = tracked by git; injected = omakase overlay; local = loose"
  for s in start demand session githook tools ci; do
    body="$(awk -F'\t' -v s="$s" '$2==s' "$ROWS")"
    [ -n "$body" ] || continue
    echo
    echo "$(stage_title "$s")"
    printf '%s\n' "$body" | while IFS=$'\t' read -r _ _ rel tool prov note; do
      extra=""
      [ "$s" = start ] && { sz="$(size_note "$rel")"; [ -n "$sz" ] && extra="; $sz"; }
      [ -n "$note" ] && extra="$extra; $note"
      printf '    + %s   (%s, %s%s)\n' "$rel" "$tool" "$prov" "$extra"
    done
  done
  echo
  echo "FINDINGS"
  if [ -s "$FIND" ]; then
    while IFS=$'\t' read -r sev text; do
      case "$sev" in 1) mk='!';; 2) mk='~';; *) mk='.';; esac
      printf '  %s %s\n' "$mk" "$text"
    done < "$FIND"
  else
    echo "  (none — clean)"
  fi
fi
