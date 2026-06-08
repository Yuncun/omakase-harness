#!/usr/bin/env bash
# omakase-harness import — the mirror of init.sh. init reads payload/ and writes it
# into a repo; import reads an existing repo's scattered harness and writes it INTO
# payload/, so a creator can capture a setup they already have. Run it from your harness
# clone and name the repo to capture as the argument; it writes to the clone's payload/
# (override the destination with OMAKASE_PAYLOAD).
#
#   cd ~/my-harness && bash bin/import.sh ~/my-project        # capture ~/my-project -> ./payload
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
#   5. import NEVER mutates the source repo. A file you already COMMIT is captured into
#      payload but left committed in place, and listed — to let the injected copy take
#      over, you run `git rm --cached` yourself (then re-init). The cut-over is your call.
#   6. Anything unresolved goes to a leftover list; import never infers.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# SOURCE repo to capture FROM — the first argument, or the current directory if omitted.
SRC_ARG="${1:-.}"
[ -d "$SRC_ARG" ] || { echo "omakase: source '$SRC_ARG' is not a directory" >&2; exit 1; }
ROOT="$(git -C "$SRC_ARG" rev-parse --show-toplevel 2>/dev/null)" || { echo "omakase: '$SRC_ARG' is not inside a git repo" >&2; exit 1; }
# DESTINATION payload (where we WRITE) — defaults to this harness clone's own payload/.
PAYLOAD="${OMAKASE_PAYLOAD:-$(cd "$SCRIPT_DIR/../payload" 2>/dev/null && pwd || echo "$SCRIPT_DIR/../payload")}"
mkdir -p "$PAYLOAD"
# Resolve BOTH physically (-P): git returns ROOT symlink-resolved, so the payload must be too, or the
# overlap guard below silently misses when paths differ only by a symlink (e.g. /tmp -> /private/tmp on macOS).
PAYLOAD="$(cd "$PAYLOAD" && pwd -P)"
ROOT="$(cd "$ROOT" && pwd -P)"
# import must write to a SEPARATE harness clone — never into the project it is reading. Refuse a
# destination that equals, contains, or sits inside the source repo (exact-equality alone missed nesting).
overlaps() { case "$1/" in "$2"/*) return 0;; esac; return 1; }
if [ "$PAYLOAD" = "$ROOT" ] || overlaps "$PAYLOAD" "$ROOT" || overlaps "$ROOT" "$PAYLOAD"; then
  echo "omakase: payload destination ($PAYLOAD) overlaps the source repo ($ROOT)." >&2
  echo "  Point OMAKASE_PAYLOAD at a SEPARATE harness clone's payload/, not inside the project you're capturing." >&2
  exit 1
fi

# Declared harness locations (the contract: the path IS the classification).
LOC_FILES=(AGENTS.md CLAUDE.md lefthook-local.yml lefthook.yml .pre-commit-config.yaml .claude/settings.json)
LOC_DIRS=(.claude/rules .claude/skills .claude/commands .claude/hooks .omakase .husky .githooks)

copy_into_payload() {  # $1 = relative path under ROOT
  local rel="$1" src="$ROOT/$1" dst="$PAYLOAD/$1"
  mkdir -p "$(dirname "$dst")"
  cp -P "$src" "$dst"                                   # -P: carry symlinks as symlinks
  case "$rel" in *.sh) [ -L "$dst" ] || chmod +x "$dst";; esac
}

is_noise() {  # structural noise — never harness, regardless of ignore state
  case "$1" in
    */node_modules/*|*/.git/*|*/worktrees/*|.claude/worktrees/*) return 0;;
    */settings.local.json|settings.local.json)                   return 0;;
  esac
  return 1
}

# A path is PERSONAL (drop + surface, never publish into payload) when git ignores it via a source
# OTHER than the omakase overlay. The harness's OWN injected files are ignored via .git/info/exclude
# (the `>>> omakase-harness >>>` block) — those are real harness and MUST be kept (the #1 regression).
# A file ignored via .gitignore or a global excludesFile is the user's personal state (a secret, scratch
# notes) that merely lives inside a declared dir — dropping it stops a credential leak into every adopter.
ignored_personal() {  # $1 = relative path under ROOT
  local v
  v="$(git -C "$ROOT" check-ignore -v -- "$1" 2>/dev/null)" || return 1   # not ignored -> not personal
  case "$v" in
    .git/info/exclude:*|*/info/exclude:*) return 1;;   # hidden by the omakase overlay -> real harness, keep
    *) return 0;;                                       # .gitignore / global excludes -> personal, drop
  esac
}

imported=(); tracked=(); skipped_personal=()
consider() {  # $1 = relative path of a real file/symlink under ROOT
  local rel="$1"
  is_noise "$rel" && return 0
  if ignored_personal "$rel"; then skipped_personal+=("$rel"); return 0; fi
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

# ---- report ----
echo "omakase import: captured ${#imported[@]} harness file(s) into $PAYLOAD"
for p in "${imported[@]:-}"; do [ -n "$p" ] && echo "  + $p"; done

if [ "${#skipped_personal[@]:-0}" -gt 0 ]; then
  echo ""
  echo "omakase import: SKIPPED ${#skipped_personal[@]} gitignored personal file(s) sitting inside harness dirs — NOT published into payload (they are ignored via .gitignore/global, not the omakase overlay):"
  for s in "${skipped_personal[@]:-}"; do [ -n "$s" ] && echo "  · skipped (personal/gitignored): $s"; done
fi

if [ "${#tracked[@]:-0}" -gt 0 ]; then
  echo ""
  echo "omakase import: ${#tracked[@]} captured file(s) are still COMMITTED in the source repo — left in place (import never changes the source)."
  echo "  They were copied into payload/, but git still tracks them here, so injection would skip them."
  echo "  To let the injected copies take over, untrack them yourself, then re-init:"
  echo "    git rm --cached -- <the files listed below>"
  echo "  (reversible: git add undoes it; the files stay on disk)."
  for t in "${tracked[@]:-}"; do [ -n "$t" ] && echo "  = still committed: $t"; done
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
