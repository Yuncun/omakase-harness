#!/usr/bin/env bash
# omakase-harness init — overlay payload/ into this repo additively, exclude every
# placed path via .git/info/exclude (zero committed footprint), install lefthook,
# and set up new worktrees to receive the (gitignored) harness automatically too.
# Idempotent: re-running re-overlays, rewrites the exclude block, and refreshes the
# worktree snapshot. Rule: the injected harness always matches payload — a re-run
# overwrites an injected file that differs (and warns that any local edit was replaced),
# but never touches a COMMITTED file (those are reported; the GUARDED `--cut-over` flag
# untracks them to let the harness copy take over — see usage).
set -euo pipefail

usage() {
  cat <<'USAGE'
usage: init.sh [--source <git-url|path>] [--cut-over] [--help]

Overlay payload/ into the current repo additively (zero committed footprint) and
install lefthook hooks. A payload path the repo already COMMITS is never touched:
it is skipped and reported.

  --source <git-url|path>
               pull a harness SOURCE — a git repo carrying a payload/ tree plus an
               omakase.manifest (flat key: value; name required, version + recommends optional) —
               into a local cache (${XDG_CACHE_HOME:-~/.cache}/omakase/sources) and inject
               the base harness's payload with the source's payload layered ON TOP (base
               machinery underneath, source wins on overlap), so a source ships only its
               delta and relies on base machinery without keeping its own copy. The source is
               remembered; a later bare init.sh refreshes and re-injects the same source.
  --cut-over   also untrack (git rm --cached) every payload path the repo currently
               commits, so the injected copies take over. With --source this is the MERGED
               base+source set, not only the source delta (a --source install equals a
               built bundle). This STAGES DELETIONS of
               shared files; the next commit applies them for everyone. It prints
               exactly what it will untrack and the consequences, then REFUSES
               unless OMAKASE_CUTOVER_CONFIRM=1 is set. You review and commit the
               staged deletions yourself.
  -h, --help   show this help.
USAGE
}

CUTOVER=0
SOURCE=""
while [ $# -gt 0 ]; do
  case "$1" in
    --cut-over) CUTOVER=1;;
    --source)   shift; [ $# -gt 0 ] || { echo "omakase: --source needs a git URL or local path" >&2; exit 2; }; SOURCE="$1";;
    -h|--help)  usage; exit 0;;
    *) echo "omakase: unknown argument '$1'" >&2; usage >&2; exit 2;;
  esac
  shift
done
# TSV column safety: the source string is recorded verbatim in the TAB-separated ledger.
case "$SOURCE" in *$'\t'*|*$'\n'*) echo "omakase: --source must not contain a tab or newline" >&2; exit 2;; esac

# A --source install merges the base harness's payload under the source delta into a temp
# staging dir (built below). Clean it on ANY exit so a failure never leaks scratch.
MERGED=""
# return 0 unconditionally: an EXIT trap's last status becomes the script's exit code,
# so a bare `[ -n "$MERGED" ]` failing (MERGED empty on a non-source install) must not
# turn a clean run into a non-zero exit.
cleanup() { [ -n "$MERGED" ] && rm -rf "$MERGED"; return 0; }
trap cleanup EXIT

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="$(git rev-parse --show-toplevel 2>/dev/null)" || { echo "omakase: not inside a git repo" >&2; exit 1; }
# The shared git dir — identical for the main checkout and every linked worktree,
# so artifacts placed here are reachable from any worktree (info/exclude and the
# common dir are shared). This is where the worktree harness snapshot lives.
COMMON="$(cd "$ROOT" && cd "$(git rev-parse --git-common-dir)" && pwd)"
OMK="$COMMON/omakase"

# sha256 — detect the digest tool once (shasum on macOS, sha256sum elsewhere).
# Used for the provenance ledger below and the source-cache slug here.
if command -v shasum >/dev/null 2>&1; then SHA256=(shasum -a 256)
elif command -v sha256sum >/dev/null 2>&1; then SHA256=(sha256sum)
else echo "omakase: need shasum or sha256sum for the provenance ledger" >&2; exit 1; fi

# ---- source mechanism (spec §1) ----
# A SOURCE is a git repo carrying the harness: a payload/ tree plus an
# omakase.manifest at its root (flat "key: value" lines — a YAML subset read with
# sed, NOT a YAML parser; name required, version + recommends optional). It is cloned into a
# disposable local cache and injected through the normal flow below. Payload
# precedence: --source flag > OMAKASE_PAYLOAD env > remembered source
# ($OMK/source, written on every source install so a bare re-run refreshes the
# same source) > the repo-relative ../payload default.
SOURCE_LABEL=payload   # ledger source column: the user's source string verbatim, else 'payload'
fetch_source() {  # $1 = git URL or local path; sets PAYLOAD to the cached payload/
  local src="$1" urlhash base slug cache def name ver
  # Cache slug: filesystem-safe basename + a short content hash of the URL string,
  # so distinct sources never collide and the same source always maps to one dir.
  urlhash="$(printf '%s' "$src" | "${SHA256[@]}" | awk '{print $1}')"
  base="$(printf '%s' "$src" | sed 's,/*$,,; s,.*/,,; s,\.git$,,' | tr -c 'A-Za-z0-9._-' '-')"
  [ -n "$base" ] || base=source
  slug="$(printf '%.50s' "$base")-$(printf '%.8s' "$urlhash")"   # %.50s: a pathological URL can't exceed filename limits
  cache="${XDG_CACHE_HOME:-$HOME/.cache}/omakase/sources/$slug"
  if [ -d "$cache/.git" ]; then
    # The cache is disposable: refresh = fetch + hard reset to the remote default
    # branch (never merge — local state in the cache has no standing). Any failure
    # here discards the cache and falls through to the fresh clone below.
    if git -C "$cache" fetch -q origin >/dev/null 2>&1 \
       && { git -C "$cache" remote set-head origin -a >/dev/null 2>&1 || true
            def="$(git -C "$cache" symbolic-ref --short refs/remotes/origin/HEAD 2>/dev/null || true)"
            [ -n "$def" ]; } \
       && git -C "$cache" reset -q --hard "$def" >/dev/null 2>&1; then
      :
    else
      echo "omakase: source cache at $cache is stale or corrupt — discarding and re-cloning (a cache is disposable)" >&2
      rm -rf "$cache"
    fi
  fi
  if [ ! -d "$cache/.git" ]; then
    rm -rf "$cache"; mkdir -p "${cache%/*}"
    git clone -q "$src" "$cache" || { echo "omakase: could not clone source '$src' into the cache ($cache)" >&2; exit 1; }
  fi
  # Validate fail-closed BEFORE anything is placed. Manifest values are stripped of
  # trailing whitespace incl. CR, so a CRLF manifest does not leak ^M downstream.
  [ -f "$cache/omakase.manifest" ] || { echo "omakase: source '$src' has no omakase.manifest at its root — not an omakase source" >&2; exit 1; }
  name="$(sed -n 's/^name:[[:space:]]*//p' "$cache/omakase.manifest" | head -n1 | sed 's/[[:space:]]*$//')"
  [ -n "$name" ] || { echo "omakase: source '$src' manifest is missing the required 'name:' line" >&2; exit 1; }
  { [ -d "$cache/payload" ] && [ -n "$(ls -A "$cache/payload" 2>/dev/null)" ]; } || { echo "omakase: source '$src' has no non-empty payload/ tree — nothing to inject" >&2; exit 1; }
  ver="$(sed -n 's/^version:[[:space:]]*//p' "$cache/omakase.manifest" | head -n1 | sed 's/[[:space:]]*$//')"
  # Optional 'recommends:' — free text printed once at install (e.g. companion
  # plugins the harness pairs with). Global so the end-of-run summary can surface it.
  recommends="$(sed -n 's/^recommends:[[:space:]]*//p' "$cache/omakase.manifest" | head -n1 | sed 's/[[:space:]]*$//')"
  echo "omakase: source '$src' (name: $name${ver:+, version: $ver}) cached at $cache"
  PAYLOAD="$cache/payload"
}
if [ -z "$SOURCE" ] && [ -z "${OMAKASE_PAYLOAD:-}" ] && [ -s "$OMK/source" ]; then
  SOURCE="$(head -n1 "$OMK/source")"
fi
# A local directory source becomes an ABSOLUTE path before it is cached, ledgered,
# or remembered — a remembered relative path breaks bare re-runs from another cwd
# or worktree.
if [ -n "$SOURCE" ] && [ -d "$SOURCE" ]; then SOURCE="$(cd "$SOURCE" && pwd)"; fi
if [ -n "$SOURCE" ]; then
  fetch_source "$SOURCE"   # sets PAYLOAD to the cached source payload/
  SOURCE_LABEL="$SOURCE"
  # Layer the base harness's payload UNDER the source delta, so a source can RELY on base
  # machinery (banner / ledger / record / deferred-check / status-line / stop-notice)
  # without keeping its own copy. This mirrors tools/build.sh (base payload, then the
  # stack delta on top — stack wins): a --source install equals a built bundle, merged at
  # inject time instead of build time. cp -RP PRESERVES symlinks (a payload may ship e.g.
  # CLAUDE.md -> AGENTS.md), which the symlink-aware place loop below carries through.
  base_payload="$(cd "$SCRIPT_DIR/../payload" && pwd)"
  MERGED="$(mktemp -d "${TMPDIR:-/tmp}/omakase-merge.XXXXXX")" \
    || { echo "omakase: could not create a temp dir to merge the base + source payload" >&2; exit 1; }
  cp -RP "$base_payload/." "$MERGED/" \
    || { echo "omakase: failed to copy the base payload into the merge staging dir" >&2; exit 1; }
  # Overlay the source delta with REPLACE semantics — rm the staging dest, THEN copy — one
  # file at a time, NOT a bulk `cp -RP "$PAYLOAD/." "$MERGED/"`. A bulk copy writes THROUGH a
  # base symlink sitting at the same path (following the link and clobbering its target,
  # leaving base's symlink in place) instead of letting the source win. The rm-first mirrors
  # place_file() below; cp -P carries a source symlink across as a symlink (e.g. CLAUDE.md ->
  # AGENTS.md). The per-file error names the path that collided (e.g. a dir-vs-file clash).
  while IFS= read -r -d '' f; do
    rel="${f#"$PAYLOAD"/}"
    mkdir -p "$MERGED/$(dirname "$rel")" && rm -rf "$MERGED/$rel" && cp -P "$f" "$MERGED/$rel" \
      || { echo "omakase: failed to overlay source payload file '$rel' onto the base payload" >&2; exit 1; }
  done < <(find "$PAYLOAD" \( -type f -o -type l \) -print0)
  PAYLOAD="$MERGED"
  # Fail-closed wiring guard (mirrors tools/build.sh): every .omakase/*.sh the MERGED hook
  # wiring references must exist in the merged payload. A source that wires a script neither
  # it nor the base harness ships would otherwise die at commit time with a cryptic exit 127 —
  # refuse here, before anything is placed.
  wiring="$MERGED/lefthook-local.yml"
  if [ -f "$wiring" ]; then
    missing=""
    # Strip YAML '#' comments first: a commented-out wiring breadcrumb — a pattern the base
    # payload's own lefthook-local.yml uses for templates — must not trip a fail-closed
    # refusal for a script it deliberately does not ship.
    for ref in $(sed 's/#.*//' "$wiring" | grep -oE '\.omakase/[A-Za-z0-9._/-]+\.sh' | sort -u); do
      [ -f "$MERGED/$ref" ] || missing="$missing $ref"
    done
    if [ -n "$missing" ]; then
      echo "omakase: source '$SOURCE' hook wiring references script(s) neither it nor the base harness ships:$missing" >&2
      echo "  These would fail at commit time (exit 127). Fix the source's lefthook-local.yml or ship the script(s). Nothing was placed." >&2
      exit 1
    fi
  fi
else
  PAYLOAD="${OMAKASE_PAYLOAD:-$(cd "$SCRIPT_DIR/../payload" && pwd)}"
fi
[ -d "$PAYLOAD" ] || { echo "omakase: payload dir not found at $PAYLOAD" >&2; exit 1; }
# Resolve a lefthook invocation WITHOUT mutating the user's global environment.
# Order (shared with remove.sh via lib-lefthook.sh): an explicit override; lefthook
# already on PATH (a global brew/mise install); then the repo's own node_modules/.bin
# (a JS devDependency); then a pinned, checksum-verified binary in a per-machine cache,
# FETCHED here if absent (the 'fetch' argument). We still do NOT touch the user's global
# environment: the cache is per-machine and disposable, so it is reversible in a way a
# global brew install is not. On any fetch failure (unknown platform, no curl/wget, no
# network, checksum mismatch) we fall back to the original guidance + non-zero — never
# worse than before. Resolution runs BEFORE any placement, so a failure exits clean.
# Sets $LEFTHOOK.
. "$SCRIPT_DIR/lib-lefthook.sh"
resolve_lefthook fetch || { lefthook_install_guidance; exit 1; }

# Shared harness-path table — kind_of() + capture/scan lists, the single source of truth
# for which paths are agent artifacts (shared with show.sh and import.sh).
. "$SCRIPT_DIR/lib-harness-paths.sh"

BEGIN="# >>> omakase-harness >>>"
END="# <<< omakase-harness <<<"
# Exclude file via the shared git dir, NOT "$ROOT/.git/info/exclude": in a linked
# worktree $ROOT/.git is a FILE, so the literal path crashes mkdir; this resolves
# to the same place in a main checkout and the right place in a worktree.
EXCLUDE="$COMMON/info/exclude"
HOOKS_DIR="$COMMON/hooks"   # hooks live in the shared git dir (we refuse a foreign core.hooksPath below)

# ---- incumbent hook-manager guard (runs BEFORE any mutation) ----
# `lefthook install` DISPLACES an existing hook stub (renames it to .old), silently
# disabling the project's own gates; a hook-manager "prepare" script (husky,
# simple-git-hooks) then reinstalls its own hooks on the next npm install, so the
# active gate set flip-flops. Detect an incumbent manager and refuse with guidance —
# omakase does not chain hook managers (v1). Exempt: lefthook-managed stubs (incl.
# our own re-init): lefthook.yml + lefthook-local.yml merging is the supported
# coexistence path. Exemption principle: omakase's own injected artifacts are always
# UNTRACKED — so an untracked .husky/ matching a payload that ships one is ours;
# git-TRACKED .husky content is always the project's own and always refuses.
incumbent=()
RESET_HOOKSPATH=0
hookspath="$(git -C "$ROOT" config --get core.hooksPath 2>/dev/null || true)"
if [ -n "$hookspath" ]; then
  # core.hooksPath pointing at the repo's OWN standard hooks dir is harmless (some real
  # repos do exactly this); only a path that resolves
  # elsewhere means a foreign manager owns the hooks. Resolve relative values
  # against $ROOT and compare physically (symlinks resolved).
  case "$hookspath" in /*) hp_abs="$hookspath";; *) hp_abs="$ROOT/$hookspath";; esac
  hp_abs="$(cd "$hp_abs" 2>/dev/null && pwd -P || echo "$hp_abs")"
  std_abs="$(cd "$HOOKS_DIR" 2>/dev/null && pwd -P || echo "$HOOKS_DIR")"
  if [ "$hp_abs" != "$std_abs" ]; then
    incumbent+=("core.hooksPath = '$hookspath' (a foreign hook manager owns the hooks dir; husky v9 sets .husky/_)")
  else
    # Redundant config: it names the default location explicitly, but lefthook
    # refuses to install while ANY core.hooksPath is set. Clear it just before
    # 'lefthook install' (the effective hooks dir is unchanged) — flagged here,
    # acted on later, so a refusal elsewhere in this guard mutates nothing.
    RESET_HOOKSPATH=1
  fi
fi
if [ -n "$(git -C "$ROOT" ls-files -- .husky 2>/dev/null)" ]; then
  incumbent+=(".husky/ content is git-tracked (the project's own husky setup)")
elif [ -d "$ROOT/.husky" ] && [ ! -d "$PAYLOAD/.husky" ]; then
  incumbent+=(".husky/ directory (husky)")
fi
if [ -f "$ROOT/package.json" ] && grep -Eq '"prepare"[[:space:]]*:[[:space:]]*"[^"]*(husky|simple-git-hooks)' "$ROOT/package.json"; then
  incumbent+=("package.json \"prepare\" script wires a hook manager (husky / simple-git-hooks) — npm install would overwrite lefthook's hooks")
fi
for hf in "$HOOKS_DIR"/*; do
  [ -f "$hf" ] || continue
  case "$hf" in *.sample|*.old) continue;; esac
  if grep -qi 'lefthook' "$hf" 2>/dev/null; then continue; fi
  if [ -f "$ROOT/.pre-commit-config.yaml" ] && grep -q 'pre-commit\.com\|generated by pre-commit' "$hf" 2>/dev/null; then
    incumbent+=("$(basename "$hf"): installed pre-commit-framework stub (plus .pre-commit-config.yaml)")
  else
    incumbent+=("$(basename "$hf"): existing non-lefthook hook in $HOOKS_DIR")
  fi
done
if [ "${#incumbent[@]}" -gt 0 ]; then
  echo "omakase: REFUSING to install — an incumbent hook manager is present:" >&2
  for i in "${incumbent[@]}"; do echo "  - $i" >&2; done
  echo "  'lefthook install' would displace the project's own hooks (renaming them to .old)," >&2
  echo "  silently disabling its gates — and a husky prepare script would overwrite lefthook" >&2
  echo "  back on the next npm install. omakase does not chain hook managers (v1)." >&2
  echo "  If these are stale leftovers, remove them and re-run. If the project really uses" >&2
  echo "  them, do not install omakase here. Nothing was changed." >&2
  exit 1
fi

# ---- guarded cut-over (--cut-over) ----
# The old advice was a raw `git rm --cached` for the user to run by hand; an agent
# reading that output runs it and auto-commits, deleting shared files from the repo
# for everyone. The guarded form states the consequences and refuses without an
# explicit confirmation env.
if [ "$CUTOVER" -eq 1 ]; then
  cutover=()
  while IFS= read -r -d '' f; do
    rel="${f#"$PAYLOAD"/}"
    git -C "$ROOT" ls-files --error-unmatch "$rel" >/dev/null 2>&1 && cutover+=("$rel")
  done < <(find "$PAYLOAD" \( -type f -o -type l \) -print0)
  if [ "${#cutover[@]}" -eq 0 ]; then
    echo "omakase: --cut-over: no payload path is tracked by this repo — nothing to cut over."
  else
    echo "omakase: cut-over will run  git rm --cached  on ${#cutover[@]} tracked file(s):"
    for c in "${cutover[@]}"; do echo "    $c"; done
    echo "  This STAGES A DELETION of each shared file. The next commit — including an agent"
    echo "  auto-commit — applies that deletion FOR EVERYONE who pulls it, and upstream changes"
    echo "  to these files will then produce modify/delete conflicts. The files stay on disk;"
    echo "  the injected (gitignored) copies take over locally. Undo before committing with"
    echo "  'git restore --staged <file>'; 'git add <file>' re-tracks later."
    if [ "${OMAKASE_CUTOVER_CONFIRM:-}" != "1" ]; then
      echo "omakase: REFUSING cut-over without confirmation. Re-run with OMAKASE_CUTOVER_CONFIRM=1 to proceed. Nothing was changed." >&2
      exit 1
    fi
    ( cd "$ROOT" && git rm --cached -q -- "${cutover[@]}" )
    echo "omakase: cut-over staged ${#cutover[@]} deletion(s) — review with 'git status' and commit them yourself."
  fi
fi

# ---- upstream-collision guard ----
# git's default --overwrite-ignore behavior SILENTLY overwrites ignored files on
# checkout/pull. If upstream commits a tracked file at a path the overlay occupies,
# the personal copy is destroyed without warning and init thereafter skips the path
# as tracked. Detect the transition: a previously PLACED path (prior run's
# provenance ledger) that the index now tracks. The last-injected copy is preserved
# under $OMK/clobbered/ because the snapshot rebuild below would delete it.
# Pre-0.10 fallback: a stale placed.list (paths only) feeds the guard one last time;
# the ledger rebuild below deletes it.
prior_paths=""
if [ -f "$OMK/placed.tsv" ]; then prior_paths="$(cut -f1 "$OMK/placed.tsv")"
elif [ -f "$OMK/placed.list" ]; then prior_paths="$(cat "$OMK/placed.list")"
fi
if [ -n "$prior_paths" ]; then
  while IFS= read -r rel; do
    [ -z "$rel" ] && continue
    if git -C "$ROOT" ls-files --error-unmatch "$rel" >/dev/null 2>&1; then
      if [ -e "$OMK/payload-snapshot/$rel" ] || [ -L "$OMK/payload-snapshot/$rel" ]; then
        mkdir -p "$OMK/clobbered/$(dirname "$rel")"
        cp -P "$OMK/payload-snapshot/$rel" "$OMK/clobbered/$rel"
      fi
      echo "omakase: WARNING — '$rel' was injected (personal, gitignored) but is NOW TRACKED by the repo." >&2
      echo "  An upstream commit likely landed a file at this path; git silently overwrites ignored" >&2
      echo "  files on checkout/pull, so your personal copy was likely clobbered. Last-injected copy" >&2
      echo "  preserved at:" >&2
      echo "    $OMK/clobbered/$rel" >&2
      echo "  Diff it against the tracked file and reconcile: drop '$rel' from your payload, or run" >&2
      echo "  init --cut-over (guarded) to untrack the file and let the injected copy take over." >&2
    fi
  done <<< "$prior_paths"
fi

# ---- provenance-ledger helpers ----
# kind_of() (classify a placed path by location) is provided by lib-harness-paths.sh,
# sourced above — the one table shared with show.sh and import.sh.
# sha256 of placed content (the SHA256 tool was detected once, up top). For a
# symlink, hash the link TARGET STRING, not the dereferenced content, so a
# payload symlink (CLAUDE.md -> AGENTS.md) round-trips.
hash_of() {
  if [ -L "$1" ]; then printf '%s' "$(readlink "$1")" | "${SHA256[@]}" | awk '{print $1}'
  else "${SHA256[@]}" < "$1" | awk '{print $1}'; fi
}

# Identical?  Compares symlink targets for symlinks, byte content otherwise.
same_file() {
  if [ -L "$1" ] || [ -L "$2" ]; then
    [ "$(readlink "$1" 2>/dev/null)" = "$(readlink "$2" 2>/dev/null)" ]
  else
    cmp -s "$1" "$2"
  fi
}
place_file() {  # $1 = source payload path, $2 = relative dest
  mkdir -p "$ROOT/$(dirname "$2")"
  # Unlink a non-directory dest first. Bare `cp -P` FOLLOWS an existing dest symlink and
  # writes through it to the link's TARGET — clobbering an out-of-tree file and leaving the
  # placed path a stale symlink — and a DANGLING dest symlink makes `cp -P` fail outright
  # (aborting the whole install under set -e). The place loop has already decided this dest
  # should be (re)written, so removing it first is safe. `[ -d ]` guard: don't rm a dir.
  [ -d "$ROOT/$2" ] || rm -f "$ROOT/$2"
  cp -P "$1" "$ROOT/$2"   # -P: carry symlinks as symlinks (e.g. CLAUDE.md -> AGENTS.md)
  case "$2" in *.sh) [ -L "$ROOT/$2" ] || chmod +x "$ROOT/$2";; esac
}

placed=(); skipped=(); overwrote=()
while IFS= read -r -d '' f; do
  rel="${f#"$PAYLOAD"/}"
  dest="$ROOT/$rel"
  # Never touch a path the repo tracks (committed file wins). Report it so the user can
  # cut over deliberately (init --cut-over, guarded) to let the injected copy take over.
  if git -C "$ROOT" ls-files --error-unmatch "$rel" >/dev/null 2>&1; then
    skipped+=("$rel"); echo "omakase: SKIP (already tracked) $rel" >&2; continue
  fi
  # Fresh placement: nothing there yet.
  if [ ! -e "$dest" ] && [ ! -L "$dest" ]; then
    place_file "$f" "$rel"; placed+=("$rel"); continue
  fi
  # Already current: an untracked copy identical to the payload — leave it.
  if same_file "$dest" "$f"; then placed+=("$rel"); continue; fi
  # Differs from payload and is NOT committed: the injected harness always matches payload,
  # so overwrite — and warn, since this replaces whatever was there (an upstream update, or
  # a local in-place edit; init cannot tell which and does not try).
  place_file "$f" "$rel"; placed+=("$rel"); overwrote+=("$rel")
  echo "omakase: overwrote $rel to match payload (any local edit was replaced)" >&2
done < <(find "$PAYLOAD" \( -type f -o -type l \) -print0)

# ---- orphan sweep ----
# A re-init whose payload no longer contains a previously placed path (a source
# dropped the file between versions, or the payload shrank) would otherwise leave
# silent residue: the regenerated ledger forgets the path, so /omakase remove never
# deletes it and it leaks untracked noise into git status. For every prior ledger
# row absent from this placement: delete the file when it is untracked AND still
# hashes to what init placed (untouched harness residue; prune emptied dirs like
# remove.sh does); otherwise keep it and WARN — a local edit is not ours to destroy.
swept=()
if [ -f "$OMK/placed.tsv" ]; then
  while IFS=$'\t' read -r rel kind src hash enabled; do
    [ -z "$rel" ] && continue
    still=0
    for p in "${placed[@]:-}"; do [ "$p" = "$rel" ] && { still=1; break; }; done
    [ "$still" -eq 1 ] && continue
    git -C "$ROOT" ls-files --error-unmatch "$rel" >/dev/null 2>&1 && continue   # tracked: upstream owns it (collision guard warned above)
    { [ -e "$ROOT/$rel" ] || [ -L "$ROOT/$rel" ]; } || continue                  # already gone
    if [ "$(hash_of "$ROOT/$rel")" = "$hash" ]; then
      rm -f "$ROOT/$rel"
      d="$(dirname "$rel")"
      while [ "$d" != "." ] && [ -d "$ROOT/$d" ] && [ -z "$(ls -A "$ROOT/$d")" ]; do rmdir "$ROOT/$d"; d="$(dirname "$d")"; done
      swept+=("$rel")
    else
      echo "omakase: WARNING — '$rel' was placed by a prior init, is no longer in the payload, and differs from what init placed (a local edit?). Leaving it; delete it yourself if unwanted." >&2
    fi
  done < "$OMK/placed.tsv"
fi

# Top-level prefixes for the exclude block (small + stable), plus lefthook's
# auto-created lefthook.yml if the repo does not track one.
prefixes=()
add_prefix(){ case " ${prefixes[*]:-} " in *" $1 "*) ;; *) prefixes+=("$1");; esac; }
# Exclude granularity: an omakase-OWNED top dir (.omakase, .claude, …) is excluded wholesale
# (small + stable). A top dir omakase SHARES with the project (.github — see
# HARNESS_SHARED_TOPDIRS in lib-harness-paths.sh) is excluded file-by-file instead, so we
# never hide the project's OWN untracked files under it.
is_shared_topdir(){ local d; for d in "${HARNESS_SHARED_TOPDIRS[@]}"; do [ "$1" = "$d" ] && return 0; done; return 1; }
for rel in "${placed[@]:-}"; do
  [ -n "$rel" ] || continue
  if is_shared_topdir "${rel%%/*}"; then add_prefix "$rel"; else add_prefix "${rel%%/*}"; fi
done
git -C "$ROOT" ls-files --error-unmatch lefthook.yml >/dev/null 2>&1 || add_prefix "lefthook.yml"

# Worktree auto-install wiring (.worktreeinclude). Only when the repo does not TRACK
# .worktreeinclude — appending to a tracked file would be a committed footprint,
# which the additive rule forbids. When skipped, manual `git worktree add` won't
# get it automatically; new Claude-created worktrees still receive it via the snapshot + post-checkout.
WTINC_TRACKED=0
if git -C "$ROOT" ls-files --error-unmatch .worktreeinclude >/dev/null 2>&1; then
  WTINC_TRACKED=1
  echo "omakase: .worktreeinclude is tracked — leaving it untouched (re-run /omakase-init inside a new manual worktree to install it there)." >&2
else
  add_prefix ".worktreeinclude"
fi

mkdir -p "$(dirname "$EXCLUDE")"; touch "$EXCLUDE"
# strip any prior block (portable, no sed -i)
awk -v b="$BEGIN" -v e="$END" '$0==b{s=1} !s{print} $0==e{s=0}' "$EXCLUDE" > "$EXCLUDE.tmp" && mv "$EXCLUDE.tmp" "$EXCLUDE"
{
  echo "$BEGIN"
  for p in "${prefixes[@]:-}"; do
    [ -z "$p" ] && continue
    if [ -d "$ROOT/$p" ]; then echo "$p/"; else echo "$p"; fi
  done
  echo "$END"
} >> "$EXCLUDE"

# Write the .worktreeinclude block (Claude Code copies gitignored files matching
# these patterns into worktrees it creates). Marked block so re-runs stay idempotent
# and /omakase-remove can strip exactly what we added.
if [ "$WTINC_TRACKED" -eq 0 ] && [ "${#placed[@]}" -gt 0 ]; then
  WTINC="$ROOT/.worktreeinclude"
  touch "$WTINC"
  awk -v b="$BEGIN" -v e="$END" '$0==b{s=1} !s{print} $0==e{s=0}' "$WTINC" > "$WTINC.tmp" && mv "$WTINC.tmp" "$WTINC"
  {
    echo "$BEGIN"
    for p in "${prefixes[@]:-}"; do
      [ -z "$p" ] && continue
      [ "$p" = ".worktreeinclude" ] && continue
      if [ -d "$ROOT/$p" ]; then echo "$p/"; else echo "$p"; fi
    done
    echo "$END"
  } >> "$WTINC"
fi

# Snapshot the placed files into the shared git dir, plus the provenance ledger and
# a self-heal script. A fresh worktree has none of the (gitignored) harness files;
# the post-checkout job runs ensure-present.sh, which copies only the MISSING ones
# from this snapshot — never overwriting a local edit, never touching a tracked path.
# The ledger (placed.tsv) is one row per placed artifact, TAB-separated:
#   path  kind  source  sha256  enabled
# Plain TSV on purpose: the hook-time readers (ensure-present, verify-overlay) are
# dependency-free POSIX sh. Regenerated wholesale each init; paths may not contain
# tabs. enabled is written 1 here — nothing writes 0 yet, but every reader honors it
# (spec §2 + safety fix 5). NOT $OMK/ledger.tsv: that is the gate-RUN ledger
# (omakase-ledger.sh), which must survive re-init.
rm -rf "$OMK/payload-snapshot"
mkdir -p "$OMK/payload-snapshot"
# Remember a source install ($OMK/source, one line) so a bare re-run refreshes the
# same source. A plain payload install leaves any remembered source in place — the
# precedence above (flag > env > remembered) already decides who wins.
if [ -n "$SOURCE" ]; then printf '%s\n' "$SOURCE" > "$OMK/source"; fi
# TODO(when: anything writes enabled=0): merge prior enabled values instead of
# hardcoding 1 — wholesale regeneration silently re-enables declined artifacts.
: > "$OMK/placed.tsv"
for rel in "${placed[@]:-}"; do
  [ -z "$rel" ] && continue
  mkdir -p "$OMK/payload-snapshot/$(dirname "$rel")"
  cp -P "$ROOT/$rel" "$OMK/payload-snapshot/$rel"
  printf '%s\t%s\t%s\t%s\t%s\n' "$rel" "$(kind_of "$rel")" "$SOURCE_LABEL" "$(hash_of "$ROOT/$rel")" 1 >> "$OMK/placed.tsv"
done
rm -f "$OMK/placed.list"   # pre-0.10 record — superseded by the ledger

cat > "$OMK/ensure-present.sh" <<'ENSURE'
#!/usr/bin/env bash
# omakase-harness self-heal — copy any MISSING injected file into this worktree
# from the shared snapshot. Ensure-present / never-overwrite: safe on every
# checkout, self-heals deleted files, never clobbers a local edit, never writes a
# tracked path. Generated by init.sh; reversed by remove.sh.
set -euo pipefail
ROOT="$(git rev-parse --show-toplevel 2>/dev/null)" || exit 0
COMMON="$(cd "$(git rev-parse --git-common-dir)" && pwd)"
SNAP="$COMMON/omakase/payload-snapshot"
LEDGER="$COMMON/omakase/placed.tsv"   # provenance ledger: path,kind,source,sha256,enabled
[ -f "$LEDGER" ] || exit 0
TAB="$(printf '\t')"
# sha256 of placed content — mirrors init.sh hash_of()/SHA256 detection EXACTLY so a
# drift compare can never false-positive on a different digest method. A symlink hashes
# its readlink TARGET STRING (so a CLAUDE.md -> AGENTS.md payload round-trips), a regular
# file hashes its bytes verbatim. No shasum/sha256sum -> empty digest -> every compare is
# a no-op: drift detection degrades to silence, never to a false warning or a hard error.
if command -v shasum >/dev/null 2>&1; then _omk_sha() { shasum -a 256; }
elif command -v sha256sum >/dev/null 2>&1; then _omk_sha() { sha256sum; }
else _omk_sha() { return 1; }; fi
omakase_hash_of() {  # $1 = path; echoes the hex digest, or nothing if no digest tool
  command -v shasum >/dev/null 2>&1 || command -v sha256sum >/dev/null 2>&1 || return 0
  if [ -L "$1" ]; then printf '%s' "$(readlink "$1" 2>/dev/null)" | _omk_sha | awk '{print $1}'
  else [ -r "$1" ] && _omk_sha < "$1" | awk '{print $1}'; fi   # unreadable -> empty -> compare skipped, no stderr leak
}
# `|| [ -n "$rel" ]`: still process a final ledger row that lacks a trailing newline
# (a corrupted/truncated ledger must not silently drop its LAST gate from heal+drift).
while IFS="$TAB" read -r rel kind src hash enabled || [ -n "$rel" ]; do
  [ -z "$rel" ] && continue
  # Self-heal respects intent (safety fix 5): enabled=0 is a deliberate off switch,
  # so a missing disabled artifact is not "missing" — never resurrect it.
  [ "$enabled" = "1" ] || continue
  # Never touch tracked — and warn: a placed path turning TRACKED means an upstream
  # commit landed a file here, and git silently overwrites ignored files on checkout,
  # so the personal copy was likely clobbered (the upstream-collision guard). This
  # check must run BEFORE the existence check: a tracked file exists in the working
  # tree, so existence-first would skip it silently.
  if git -C "$ROOT" ls-files --error-unmatch "$rel" >/dev/null 2>&1; then
    echo "omakase: WARNING — injected path '$rel' is now TRACKED by the repo; your personal copy was likely clobbered by an upstream commit (git overwrites ignored files on checkout). Last-injected copy: $SNAP/$rel — diff it against the tracked file, then drop the path from your payload or cut over (init --cut-over)." >&2
    continue
  fi
  # Already present — NEVER overwrite (the heal only fills MISSING files; an auto-reset
  # could nuke an in-progress edit and would break omakase's never-clobber contract).
  # But the canonical fingerprint $hash was, until now, captured and thrown away: a
  # present-but-CHANGED file was indistinguishable from a present-and-correct one. Detect
  # that DRIFT and SURFACE it (warn only). Two silent failures this catches:
  #   1. a gate edited/weakened in place — still looks installed and green, no longer protects;
  #   2. (the common bite) a STALE gate in a linked worktree — a main-checkout re-init
  #      updated the shared snapshot+ledger, but this worktree's copy already exists, so
  #      the heal below skips it and the worktree silently keeps running the old gate.
  if [ -e "$ROOT/$rel" ] || [ -L "$ROOT/$rel" ]; then
    actual="$(omakase_hash_of "$ROOT/$rel")" || actual=""
    if [ -n "$hash" ] && [ -n "$actual" ] && [ "$actual" != "$hash" ]; then
      if [ -e "$SNAP/$rel" ] || [ -L "$SNAP/$rel" ]; then fix="cp -P '$SNAP/$rel' '$ROOT/$rel'  (or /omakase init to re-sync every file)"
      else fix="/omakase init"; fi
      echo "omakase: WARNING — injected '$rel' has DRIFTED from canonical (ledger ${hash:0:12}…, on-disk ${actual:0:12}…); a gate may be weakened or stale. Drift only surfaces — your copy is left as-is. Adopt canonical with: $fix" >&2
    fi
    continue                                                                  # never overwrite (also catches dangling symlinks)
  fi
  [ -e "$SNAP/$rel" ] || [ -L "$SNAP/$rel" ] || continue
  mkdir -p "$ROOT/$(dirname "$rel")"
  cp -P "$SNAP/$rel" "$ROOT/$rel"
  case "$rel" in *.sh) [ -L "$ROOT/$rel" ] || chmod +x "$ROOT/$rel";; esac
done < "$LEDGER"
# Re-arm the fail-closed guard blocks: lefthook's npm postinstall runs
# `lefthook install -f` on every npm/yarn install, regenerating the hook stubs and
# stripping the guard; this post-checkout hook still runs afterwards, so the guard
# self-heals on the next checkout/pull.
if [ -f "$COMMON/omakase/install-guards.sh" ]; then sh "$COMMON/omakase/install-guards.sh" || true; fi
exit 0   # post-checkout self-heal is best-effort: never let a guard-install hiccup fail the checkout job
ENSURE
chmod +x "$OMK/ensure-present.sh"

# Fail-closed verifier — run by the enforcement hook stubs BEFORE lefthook. If the
# (gitignored) overlay was wiped (e.g. `git clean -fdx`), the stubs survive in .git
# but lefthook finds no config and passes silently — gates would fail OPEN. This
# checks every ledgered path still exists and hard-fails with a restore instruction.
# Fast (one existence test per placed file, every commit) and dependency-free
# (POSIX sh + git only). Generated by init.sh; removed with the snapshot by remove.sh.
cat > "$OMK/verify-overlay.sh" <<'VERIFY'
#!/bin/sh
# omakase-harness fail-closed guard. Generated by init.sh.
ROOT="$(git rev-parse --show-toplevel 2>/dev/null)" || exit 0
COMMON="$(cd "$(git rev-parse --git-common-dir)" && pwd)"
LEDGER="$COMMON/omakase/placed.tsv"   # provenance ledger: path,kind,source,sha256,enabled
[ -f "$LEDGER" ] || exit 0   # harness not installed -> nothing to verify
TAB="$(printf '\t')"
missing=0
# `|| [ -n "$rel" ]`: process a final row lacking a trailing newline (matches ensure-present).
# A truncated/corrupt ledger is exactly the damaged-overlay case this guard exists to catch, so
# it must not silently drop its LAST gate and fail OPEN for it.
while IFS="$TAB" read -r rel kind src hash enabled || [ -n "$rel" ]; do
  [ -z "$rel" ] && continue
  [ "$enabled" = "1" ] || continue   # disabled artifacts are deliberately absent — never block on them
  [ -e "$ROOT/$rel" ] || [ -L "$ROOT/$rel" ] && continue
  git -C "$ROOT" ls-files --error-unmatch "$rel" >/dev/null 2>&1 && continue  # tracked: upstream owns it (warned at init/checkout)
  [ "$missing" -eq 0 ] && echo "omakase: BLOCKING — the injected harness is incomplete; its gates would silently not run:" >&2
  echo "  missing: $rel" >&2
  missing=1
done < "$LEDGER"
[ "$missing" -eq 0 ] && exit 0
echo "omakase: restore it with  bash $COMMON/omakase/ensure-present.sh  (or /omakase init), then retry." >&2
exit 1
VERIFY
chmod +x "$OMK/verify-overlay.sh"

# ---- hook stub blocks (install-guards.sh) ----
# install-guards.sh inserts our blocks into the shared hook stubs ABOVE lefthook's own
# call, so none of them depends on a worktree-local lefthook config (gitignored, hence
# absent in a fresh worktree). It is a SCRIPT in the shared git dir, not inline code,
# because lefthook's npm package runs `lefthook install -f` in its postinstall on EVERY
# npm/yarn install, regenerating the stubs and stripping our blocks; the generated
# ensure-present.sh calls this on post-checkout, so they self-heal on the next
# checkout/pull. Two block kinds, each idempotent (marked, strip-then-insert):
#   fail-closed (pre-commit, pre-push) — runs verify-overlay.sh and blocks when the
#     overlay is gone. ABOVE lefthook on purpose: overlay integrity is not a lefthook
#     check, so LEFTHOOK=0 does not bypass it — the only escape is git's own --no-verify.
#   worktree-bootstrap (post-checkout) — runs ensure-present.sh directly so a fresh
#     `git worktree add` self-heals the harness on ANY host. lefthook no-ops in a fresh
#     worktree (the gitignored config is not there yet), so this self-heal cannot rely on
#     a lefthook job; it lives in the SHARED stub, reachable from every worktree. This is
#     the host-agnostic backstop to the Claude-native .worktreeinclude copy: Claude keeps
#     its eager copy, every other host (Copilot CLI, bare git) gets the same result here.
#     Best-effort — it never blocks a checkout (the pre-push fail-closed guard backstops).
#     The lefthook post-checkout job is kept too: it is the re-arm trigger in the main
#     checkout after `lefthook install -f`.
# All blocks are inert once the harness is removed ($COMMON/omakase deleted).
cat > "$OMK/install-guards.sh" <<'GUARDS'
#!/bin/sh
# omakase-harness hook-stub installer. Generated by init.sh; called by init.sh after
# `lefthook install` and by ensure-present.sh on every checkout (lefthook's npm
# postinstall regenerates the stubs, stripping our blocks — this re-arms them).
# Idempotent (strip-then-insert). Installs two block kinds, both ABOVE lefthook's call:
# fail-closed verify (pre-commit/pre-push) and worktree-bootstrap self-heal (post-checkout).
COMMON="$(cd "$(git rev-parse --git-common-dir 2>/dev/null)" && pwd)" || exit 0
HOOKS_DIR="$COMMON/hooks"

# --- fail-closed verify: block a commit/push when the overlay was wiped. ---
GBEGIN="# >>> omakase-harness fail-closed >>>"
GEND="# <<< omakase-harness fail-closed <<<"
for h in pre-commit pre-push; do
  hf="$HOOKS_DIR/$h"
  [ -f "$hf" ] || continue
  grep -qi 'lefthook' "$hf" 2>/dev/null || continue   # only instrument lefthook-managed stubs
  awk -v b="$GBEGIN" -v e="$GEND" '$0==b{s=1} !s{print} $0==e{s=0}' "$hf" > "$hf.tmp"
  {
    head -n1 "$hf.tmp"   # the shebang
    printf '%s\n' "$GBEGIN"
    cat <<'GUARD'
# Fail-closed: this runs ABOVE lefthook, so LEFTHOOK=0 does not bypass it;
# the only escape is git's own --no-verify.
omakase_verify="$(git rev-parse --git-common-dir)/omakase/verify-overlay.sh"
if [ -f "$omakase_verify" ]; then
  sh "$omakase_verify" || exit 1
fi
GUARD
    printf '%s\n' "$GEND"
    tail -n +2 "$hf.tmp"
  } > "$hf"
  rm -f "$hf.tmp"
  chmod +x "$hf"
done

# --- worktree-bootstrap: self-heal the harness into a fresh `git worktree add`. ---
# A fresh worktree has no (gitignored) lefthook config, so lefthook's post-checkout job
# no-ops and the harness never materializes. This block runs ensure-present.sh straight
# from the SHARED git dir (reachable from every worktree), so a plain `git worktree add`
# — any host: Copilot CLI, bare git, or Claude (alongside its .worktreeinclude copy) —
# bootstraps the harness on creation. Never blocks the checkout (self-heal is best-effort;
# the pre-push fail-closed guard backs it).
WBEGIN="# >>> omakase-harness worktree-bootstrap >>>"
WEND="# <<< omakase-harness worktree-bootstrap <<<"
hf="$HOOKS_DIR/post-checkout"
if [ -f "$hf" ] && grep -qi 'lefthook' "$hf" 2>/dev/null; then
  awk -v b="$WBEGIN" -v e="$WEND" '$0==b{s=1} !s{print} $0==e{s=0}' "$hf" > "$hf.tmp"
  {
    head -n1 "$hf.tmp"   # the shebang
    printf '%s\n' "$WBEGIN"
    cat <<'BOOT'
omakase_ensure="$(git rev-parse --git-common-dir)/omakase/ensure-present.sh"
if [ -f "$omakase_ensure" ]; then
  bash "$omakase_ensure" || true
fi
BOOT
    printf '%s\n' "$WEND"
    tail -n +2 "$hf.tmp"
  } > "$hf"
  rm -f "$hf.tmp"
  chmod +x "$hf"
fi
GUARDS
chmod +x "$OMK/install-guards.sh"

if [ "$RESET_HOOKSPATH" -eq 1 ]; then
  git -C "$ROOT" config --unset core.hooksPath 2>/dev/null || true
  echo "omakase: cleared redundant core.hooksPath (it named the repo's own hooks dir; lefthook refuses to install while it is set — the effective hooks dir is unchanged)."
fi
( cd "$ROOT" && $LEFTHOOK install )
sh "$OMK/install-guards.sh"

echo "omakase: placed ${#placed[@]} file(s), overwrote ${#overwrote[@]} to match payload, skipped ${#skipped[@]} committed path(s)."
for p in "${placed[@]:-}"; do [ -n "$p" ] && echo "  + $p"; done
for o in "${overwrote[@]:-}"; do [ -n "$o" ] && echo "  ^ overwrote to match payload (any local edit replaced): $o"; done
for w in "${swept[@]:-}"; do [ -n "$w" ] && echo "  - removed (placed by a prior init, no longer in the payload): $w"; done
for s in "${skipped[@]:-}"; do [ -n "$s" ] && echo "  ~ skipped (committed — re-run with --cut-over to let the harness copy take over; guarded, see init.sh --help): $s"; done
echo "omakase: ignores -> .git/info/exclude; hooks installed; new worktrees auto-install the harness. Nothing to commit."
echo "omakase: see the whole harness any time with  /omakase show"
# A harness may name companion tools (e.g. plugins it pairs with) in its manifest's
# 'recommends:' line. Surfaced once here, at install — installing a companion is a
# one-time setup action, not a per-session instruction.
if [ -n "${recommends:-}" ]; then
  echo "omakase: this harness recommends — $recommends"
fi
# How to customize, surfaced where the agent acts: editing injected files in place is
# overwritten on the next init. The supported path is forking the source.
echo "omakase: to customize, fork the harness source (clone -> edit -> publish) and"
echo "         init from your copy; do not edit injected files in place (overwritten on re-init)."
# Only advertise the scorecard status line when the payload actually ships it.
# A payload may ship gates but no status-line segment (it forgoes the scorecard
# surface), and a dangling wire-up instruction is worse than none.
if [ -f "$ROOT/.omakase/bin/omakase-statusline.sh" ]; then
  echo "omakase: status line — compose the scorecard into your existing bar (it never"
  echo "         takes over the bar). Add this command to your status-line script:"
  echo "           bash $ROOT/.omakase/bin/omakase-statusline.sh"
  echo "         Claude Code: your ~/.claude statusLine script. Copilot CLI: ~/.copilot. tmux: status-right."
fi
