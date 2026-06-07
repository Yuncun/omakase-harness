#!/usr/bin/env bash
# omakase-harness import — the mirror of init.sh. init reads payload/ and writes it
# into a repo; import reads an existing repo's scattered harness and writes it INTO
# payload/, so a creator can capture a setup they already have. Run it from INSIDE the
# project you want to capture; it writes to your harness clone's payload/ (the same
# OMAKASE_PAYLOAD init uses, here as the DESTINATION).
#
#   cd ~/my-project && bash ~/my-harness/bin/import.sh        # -> ~/my-harness/payload
#
# It is fully deterministic — a declared signal (file location, git state, hook config)
# decides every step; nothing is inferred. The six rules:
#   1. Mirror DECLARED harness locations (.claude/{rules,skills,commands,hooks},
#      .claude/settings.json, .omakase/, AGENTS.md/CLAUDE.md, lefthook*.yml, .husky/,
#      .pre-commit-config.yaml, .githooks/) to the identical path in payload/.
#      Walk locations ON DISK — NOT `git ls-files`: a harness's own gates are gitignored
#      by design, so a tracked-file scan would silently drop them.
#   2. Gates are whatever a hook config names — read it, don't guess. A wired script that
#      lives outside a captured location is reported, never auto-grabbed.
#   3. Skip noise: node_modules/, worktrees, .git/, and the personal settings.local.json.
#   4. Carry symlinks as symlinks (cp -P), e.g. CLAUDE.md -> AGENTS.md. Never dereference.
#   5. Files you already COMMIT are left committed and listed; --adopt-tracked is the
#      explicit opt-in that `git rm --cached`es them (you commit the removal).
#   6. Anything unresolved goes to a leftover list; import never infers.
set -euo pipefail

ADOPT_TRACKED=0
for a in "$@"; do case "$a" in --adopt-tracked) ADOPT_TRACKED=1;; esac; done

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# DESTINATION payload (where we WRITE). Same env var init uses, opposite role.
PAYLOAD="${OMAKASE_PAYLOAD:-$(cd "$SCRIPT_DIR/../payload" 2>/dev/null && pwd || echo "$SCRIPT_DIR/../payload")}"
# SOURCE repo (where we READ from) — the project you run this inside.
ROOT="$(git rev-parse --show-toplevel 2>/dev/null)" || { echo "omakase: not inside a git repo" >&2; exit 1; }
mkdir -p "$PAYLOAD"
PAYLOAD="$(cd "$PAYLOAD" && pwd)"
[ "$PAYLOAD" = "$ROOT" ] && { echo "omakase: payload destination is the source repo itself — point OMAKASE_PAYLOAD at your harness clone's payload/." >&2; exit 1; }

# Declared harness locations (the contract: the path IS the classification).
LOC_FILES=(AGENTS.md CLAUDE.md lefthook-local.yml lefthook.yml .pre-commit-config.yaml .claude/settings.json)
LOC_DIRS=(.claude/rules .claude/skills .claude/commands .claude/hooks .omakase .husky .githooks)

copy_into_payload() {  # $1 = relative path under ROOT
  local rel="$1" src="$ROOT/$1" dst="$PAYLOAD/$1"
  mkdir -p "$(dirname "$dst")"
  cp -P "$src" "$dst"                                   # -P: carry symlinks as symlinks
  case "$rel" in *.sh) [ -L "$dst" ] || chmod +x "$dst";; esac
}

is_noise() {  # personal/noise path that lives inside a declared location
  case "$1" in
    */node_modules/*|*/.git/*|*/worktrees/*|.claude/worktrees/*) return 0;;
    */settings.local.json|settings.local.json)                   return 0;;
  esac
  return 1
}

imported=(); tracked=()
consider() {  # $1 = relative path of a real file/symlink under ROOT
  local rel="$1"
  is_noise "$rel" && return 0
  copy_into_payload "$rel"
  imported+=("$rel")
  if git -C "$ROOT" ls-files --error-unmatch "$rel" >/dev/null 2>&1; then tracked+=("$rel"); fi
  return 0   # never let an untracked file (git rc=1) abort the walk under set -e
}

# Rule 1 + 3 + 4: walk declared locations on disk, copy survivors by identical path.
for f in "${LOC_FILES[@]}"; do
  [ -e "$ROOT/$f" ] || [ -L "$ROOT/$f" ] || continue
  consider "$f"
done
for d in "${LOC_DIRS[@]}"; do
  [ -d "$ROOT/$d" ] || continue
  while IFS= read -r -d '' abs; do
    consider "${abs#"$ROOT"/}"
  done < <(find "$ROOT/$d" \( -type f -o -type l \) \
             -not -path '*/node_modules/*' -not -path '*/.git/*' -not -path '*/worktrees/*' -print0)
done

# Rule 2 + 6: leftover detection from the hook configs we captured. A gate is a command
# WIRED into a hook, so the hook config is the gate manifest — read it.
loose_gates=(); stack_jobs=()
in_imported() { local p; for p in "${imported[@]:-}"; do [ "$p" = "$1" ] && return 0; done; return 1; }
for cfg in lefthook-local.yml lefthook.yml .pre-commit-config.yaml; do
  [ -f "$ROOT/$cfg" ] || continue
  # wired scripts named directly (e.g. `bash path/to/foo.sh`) that we did NOT capture
  while IFS= read -r sh; do
    [ -n "$sh" ] || continue
    [ -e "$ROOT/$sh" ] || continue
    in_imported "$sh" || { case " ${loose_gates[*]:-} " in *" $sh "*) ;; *) loose_gates+=("$sh");; esac; }
  done < <(grep -oE '[A-Za-z0-9._/-]+\.sh' "$ROOT/$cfg" 2>/dev/null | sort -u)
  # stack-coupled run: bodies (won't run off this project's toolchain)
  while IFS= read -r line; do
    stack_jobs+=("${cfg}: ${line}")
  done < <(grep -E '^\s*run:' "$ROOT/$cfg" 2>/dev/null | grep -E '(pnpm|npm |npx|yarn|turbo|make |cargo|go run|pytest|ruff|vue-tsc)' | sed -E 's/^\s*run:\s*//' | sort -u)
done

# Rule 5: the cut-over for files the source still COMMITS.
adopted=0
if [ "$ADOPT_TRACKED" -eq 1 ] && [ "${#tracked[@]:-0}" -gt 0 ]; then
  git -C "$ROOT" rm --cached --quiet -- "${tracked[@]}"
  adopted=1
fi

# ---- report ----
echo "omakase import: captured ${#imported[@]} harness file(s) into $PAYLOAD"
for p in "${imported[@]:-}"; do [ -n "$p" ] && echo "  + $p"; done

if [ "${#tracked[@]:-0}" -gt 0 ]; then
  echo ""
  if [ "$adopted" -eq 1 ]; then
    echo "omakase import: --adopt-tracked → git rm --cached staged for ${#tracked[@]} committed file(s) in $ROOT."
    echo "  Commit the removal so the harness (injected) becomes the single source. Files stay on disk."
    for t in "${tracked[@]:-}"; do [ -n "$t" ] && echo "  - untracked: $t"; done
  else
    echo "omakase import: ${#tracked[@]} captured file(s) are still COMMITTED in this repo — left in place."
    echo "  They were copied into payload/, but git still tracks them here, so injection would skip them."
    echo "  Re-run with --adopt-tracked to 'git rm --cached' them (reversible: git add undoes it; files stay on disk)."
    for t in "${tracked[@]:-}"; do [ -n "$t" ] && echo "  = still committed: $t"; done
  fi
fi

if [ "${#loose_gates[@]:-0}" -gt 0 ]; then
  echo ""
  echo "omakase import: these scripts are wired into a hook but live OUTSIDE a captured location:"
  for g in "${loose_gates[@]:-}"; do [ -n "$g" ] && echo "  ? wired gate not captured: $g  (move it under .omakase/gates/ and re-import to ship it)"; done
fi

if [ "${#stack_jobs[@]:-0}" -gt 0 ]; then
  echo ""
  echo "omakase import: these hook jobs are coupled to this project's toolchain — review them for the repos you'll inject into:"
  for j in "${stack_jobs[@]:-}"; do [ -n "$j" ] && echo "  ~ $j"; done
fi

echo ""
echo "omakase import: test the captured harness without publishing —"
echo "    cd \"\$(mktemp -d)\" && git init -q && git commit -q --allow-empty -m init \\"
echo "      && OMAKASE_PAYLOAD=\"$PAYLOAD\" bash \"$SCRIPT_DIR/init.sh\""
echo "  then make a commit to watch a gate fire; OMAKASE_PAYLOAD=\"$PAYLOAD\" bash \"$SCRIPT_DIR/remove.sh\" to reset."
