// This file (source.go) ports the `--source` arm of bin/init.sh — the harness
// SOURCE mechanism (spec §1): shorthand/ref rewrites (bin/init.sh:155-171),
// fetch_source (bin/init.sh:104-151: the disposable cache slug, the
// refresh-or-reclone, the #ref pin, and the fail-closed manifest validation),
// and the base+delta merge staging (bin/init.sh:181-197). RunInit calls
// expandSource + runSource in place of Task 3's stub; the source-conditional
// tails (placed.tsv column 3, $OMK/source, the summary `recommends:` line) are
// wired through the existing engine in init.go.
//
// bin/init.sh STAYS bash, untouched: the frozen v1 sources.test.sh /
// roundtrip.test.sh / recommends.test.sh still run through it. This Go arm goes
// live only at Task 6's shim cutover; source_test.go + init_test.go are this
// task's safety net.
//
// Sanctioned divergences (documented at each site): (a) the base+delta merge
// traversal walks with filepath.WalkDir (lexical) rather than find's readdir
// order — Global Constraint 6, consistent with the place loop; (b) the merge
// staging dir's random suffix differs from v1's mktemp XXXXXX (Global Constraint
// 6, sanctioned) and never surfaces in output. The merge BASE payload resolves
// exactly as v1's $SCRIPT_DIR/../payload does (binary-relative, NOT honoring
// OMAKASE_PAYLOAD — see defaultPayload); the basePayloadOverride package var is
// a production-inert TEST SEAM, not a behavioral change.
package overlay

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// reOwnerRepo is the owner/repo shorthand test (bin/init.sh:164): two path
// segments, each starting alnum then [A-Za-z0-9._-]. The `-` sits last in the
// class so it is a literal, matching the bash bracket expression byte-for-byte.
var reOwnerRepo = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*/[A-Za-z0-9][A-Za-z0-9._-]*$`)

// basePayloadOverride is a TEST SEAM for the binary-relative base payload
// (bin/init.sh:181/199's $SCRIPT_DIR/../payload). It is "" in production, so
// defaultPayload falls back to the os.Executable-relative resolution; tests set
// it because os.Executable points at the test binary, not a deployed omakase.
// Guarded like the rest of the engine by the tests' non-parallel discipline
// (they share cwd + env via t.Setenv already).
var basePayloadOverride string

// defaultPayload resolves the base harness payload the way bin/init.sh:181,199
// resolve $SCRIPT_DIR/../payload: from the running binary's own location
// (dist/omakase => repo root => payload/, matching the shim's bin/../payload).
// This is NOT the OMAKASE_PAYLOAD override — that is layered on by the plain
// install path (bin/init.sh:199) and deliberately NOT by the merge base
// (bin/init.sh:181); see this file's package comment, divergence (b).
func defaultPayload() string {
	if basePayloadOverride != "" {
		return basePayloadOverride
	}
	if exe, err := os.Executable(); err == nil {
		return filepath.Join(filepath.Dir(filepath.Dir(exe)), "payload")
	}
	return ""
}

// expandSource ports the shorthand / ref / local-dir-absolutize rewrites
// (bin/init.sh:160-171). It returns the resolved source string and the pinned
// ref ("" if none). Applied to BOTH a freshly given source and a remembered
// one, so a bare re-run round-trips a pinned ref. Skipped when source is empty
// or already names an EXISTING local path (a real path wins over the shorthand;
// `-e` dereferences, so a dangling symlink counts as absent). The #ref split can
// leave source empty (a pathological "#ref"), which the caller's branch honors.
func expandSource(source string) (string, string) {
	ref := ""
	// Shorthand + #ref only when source is non-empty and not an existing path.
	if source != "" && !pathExists(source) {
		if i := strings.IndexByte(source, '#'); i >= 0 { // *#* : first '#' splits
			ref = source[i+1:]  // ${SOURCE#*#}
			source = source[:i] // ${SOURCE%%#*}
		}
		// owner/repo -> a GitHub URL, unless it already looks like a URL/scp path.
		if !isURLish(source) && reOwnerRepo.MatchString(source) {
			source = "https://github.com/" + source
		}
	}
	// A local directory source becomes ABSOLUTE before it is cached, ledgered, or
	// remembered (a relative remembered path breaks bare re-runs from another cwd).
	// bin/init.sh uses `cd "$SOURCE" && pwd` (logical); filepath.Abs cleans without
	// resolving symlinks, matching that for absolute inputs (all tests pass one).
	if source != "" && isDir(source) {
		if abs, err := filepath.Abs(source); err == nil {
			source = abs
		}
	}
	return source, ref
}

// isURLish is the bash `case ... in *://*|git@*) false;; *) true;;` guard
// (bin/init.sh:163): the owner/repo shorthand is NOT applied when the string
// already contains a scheme (`://`) or is an scp-style `git@host:...`.
func isURLish(s string) bool {
	return strings.Contains(s, "://") || strings.HasPrefix(s, "git@")
}

// pathExists is bash `[ -e p ]`: exists, following symlinks (a dangling symlink
// is `false`, so it is treated as absent and the shorthand still applies).
func pathExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// sourceResult carries what a --source install contributes back to RunInit.
type sourceResult struct {
	payload    string // the merged base+delta staging dir (becomes PAYLOAD)
	merged     string // the staging dir to clean up on every exit path (== payload)
	label      string // SOURCE_LABEL: placed.tsv column 3 (bin/init.sh:174)
	remembered string // written to $OMK/source (bin/init.sh:600) — same string
	recommends string // manifest recommends:, surfaced in the summary (bin/init.sh:662)
}

// runSource fetches the source into the disposable cache, validates it, and
// stages the base+source merge (bin/init.sh:172-197). On any failure it has
// already written the byte-exact message, cleaned up any staging dir it created,
// and returns a non-zero code; the caller returns that code. On success the
// caller MUST `defer os.RemoveAll(result.merged)` — v1's EXIT trap
// (bin/init.sh:63-68) removes the staging dir on EVERY subsequent exit path.
func runSource(source, sourceRef, basePayload string, stdout, stderr io.Writer) (sourceResult, int) {
	payloadDir, recommends, code := fetchSource(source, sourceRef, stdout, stderr)
	if code != 0 {
		return sourceResult{}, code // nothing staged yet — matches v1's empty MERGED
	}

	label := source
	if sourceRef != "" {
		label = source + "#" + sourceRef // bin/init.sh:174 ${SOURCE_REF:+#$SOURCE_REF}
	}

	// Merge staging: os.MkdirTemp honors TMPDIR the way v1's
	// `mktemp -d "${TMPDIR:-/tmp}/omakase-merge.XXXXXX"` does (os.TempDir == that
	// same ${TMPDIR:-/tmp}). The random suffix differs (GC6-sanctioned) and never
	// surfaces — placement derives its rel paths against this dir.
	merged, err := os.MkdirTemp(os.TempDir(), "omakase-merge.")
	if err != nil {
		fmt.Fprintln(stderr, "omakase: could not create a temp dir to merge the base + source payload")
		return sourceResult{}, 1
	}

	// Copy the BASE payload tree first (cp -RP "$base/." "$MERGED/", bin/init.sh:184).
	if err := copyTree(basePayload, merged); err != nil {
		os.RemoveAll(merged)
		fmt.Fprintln(stderr, "omakase: failed to copy the base payload into the merge staging dir")
		return sourceResult{}, 1
	}

	// Overlay the source delta with REPLACE semantics — mkdir -p parent, rm -rf
	// dest, cp -P — one file at a time so a base symlink at the same path is
	// removed and the SOURCE wins (never written THROUGH), and a source symlink is
	// carried across as a symlink (bin/init.sh:186-196). Walk order is
	// filepath.WalkDir's lexical order (Global Constraint 6), consistent with the
	// place loop; the per-entry effect is order-independent (each rel is disjoint).
	rels, err := walkPayload(payloadDir)
	if err != nil {
		os.RemoveAll(merged)
		return sourceResult{}, 1
	}
	overlayOne := func(rel string) error {
		dst := filepath.Join(merged, rel)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		if err := os.RemoveAll(dst); err != nil { // rm -rf: also clears a base DIR at this path
			return err
		}
		return CopyEntry(filepath.Join(payloadDir, rel), dst) // cp -P
	}
	for _, rel := range rels {
		if err := overlayOne(rel); err != nil {
			os.RemoveAll(merged)
			fmt.Fprintf(stderr, "omakase: failed to overlay source payload file '%s' onto the base payload\n", rel)
			return sourceResult{}, 1
		}
	}

	return sourceResult{
		payload:    merged,
		merged:     merged,
		label:      label,
		remembered: label,
		recommends: recommends,
	}, 0
}

// fetchSource ports fetch_source (bin/init.sh:104-151): resolve the disposable
// cache dir from the source slug, refresh an existing clone (or discard + reclone
// a stale/corrupt one), pin an optional #ref, then validate the manifest
// FAIL-CLOSED before anything is placed. On success it prints the "cached at"
// line and returns the cache's payload/ dir + the manifest's recommends value.
// On any failure it writes the byte-exact message and returns a non-zero code.
func fetchSource(src, sourceRef string, stdout, stderr io.Writer) (payloadDir, recommends string, code int) {
	cache := sourceCacheDir(src)

	// An existing clone is refreshed to the remote default branch (never merged —
	// cache state has no standing). Any failure discards it and falls through to a
	// fresh clone (bin/init.sh:113-127).
	//
	// This refresh (a reset --hard to the remote default, inside refreshCache)
	// runs BEFORE the #ref checkout further below — so a BRANCH pin does NOT
	// survive a bare re-run: the hard-reset lands the cache's checked-out branch
	// on default-branch content, and re-checking out that same branch name then
	// yields the DEFAULT content, not the pinned one (a TAG pin does survive,
	// since a tag's ref is immutable — see TestBranchPinNotPreserved vs.
	// TestRememberedSourceRoundTrip in source_test.go). This is a v1 quirk
	// (bin/init.sh:117-138), reproduced here deliberately, not a Go bug. Phase
	// 4's `update` verb is expected to redesign exactly this refresh-then-
	// checkout sequence.
	if isDir(filepath.Join(cache, ".git")) {
		if !refreshCache(cache) {
			fmt.Fprintf(stderr, "omakase: source cache at %s is stale or corrupt — discarding and re-cloning (a cache is disposable)\n", cache)
			os.RemoveAll(cache)
		}
	}
	if !isDir(filepath.Join(cache, ".git")) {
		os.RemoveAll(cache)
		os.MkdirAll(filepath.Dir(cache), 0o755) // mkdir -p "${cache%/*}"
		clone := exec.Command("git", "clone", "-q", "--", src, cache)
		clone.Stdout = stdout // inherited, matching v1's un-redirected clone (-q => silent on success)
		clone.Stderr = stderr
		if err := clone.Run(); err != nil {
			fmt.Fprintf(stderr, "omakase: could not clone source '%s' into the cache (%s)\n", src, cache)
			return "", "", 1
		}
	}

	// Pin to a requested #ref (branch or tag). Fetch tags so a tag ref resolves;
	// tag-fetch failure is ignored (bin/init.sh:134-138). Both git calls redirect
	// to /dev/null in v1 (nil Stdout/Stderr here does the same).
	if sourceRef != "" {
		gitCacheQuiet(cache, "fetch", "-q", "--tags", "origin") // failure ignored
		co := exec.Command("git", "-C", cache, "-c", "advice.detachedHead=false", "checkout", "-q", sourceRef)
		if err := co.Run(); err != nil {
			fmt.Fprintf(stderr, "omakase: source '%s' has no ref '%s' (no such branch or tag)\n", src, sourceRef)
			return "", "", 1
		}
	}

	// Fail-closed manifest validation BEFORE anything is placed (bin/init.sh:141-148).
	manifestPath := filepath.Join(cache, "omakase.manifest")
	if !fileRegular(manifestPath) {
		fmt.Fprintf(stderr, "omakase: source '%s' has no omakase.manifest at its root — not an omakase source\n", src)
		return "", "", 1
	}
	manifest, _ := os.ReadFile(manifestPath)
	name := manifestField(manifest, "name")
	if name == "" {
		fmt.Fprintf(stderr, "omakase: source '%s' manifest is missing the required 'name:' line\n", src)
		return "", "", 1
	}
	payloadDir = filepath.Join(cache, "payload")
	if !isDir(payloadDir) || !dirNonEmpty(payloadDir) {
		fmt.Fprintf(stderr, "omakase: source '%s' has no non-empty payload/ tree — nothing to inject\n", src)
		return "", "", 1
	}
	ver := manifestField(manifest, "version")
	recommends = manifestField(manifest, "recommends")

	verPart := ""
	if ver != "" {
		verPart = ", version: " + ver // ${ver:+, version: $ver}
	}
	fmt.Fprintf(stdout, "omakase: source '%s' (name: %s%s) cached at %s\n", src, name, verPart, cache)
	return payloadDir, recommends, 0
}

// refreshCache is the && chain at bin/init.sh:117-121: fetch origin, then set the
// remote HEAD (failure ignored) and read the default branch, then hard-reset to
// it. Returns true iff the whole chain succeeds; a false return tells the caller
// to discard and reclone. All child output is discarded (v1's >/dev/null 2>&1).
func refreshCache(cache string) bool {
	if !gitCacheQuiet(cache, "fetch", "-q", "origin") {
		return false
	}
	gitCacheQuiet(cache, "remote", "set-head", "origin", "-a") // || true
	def := gitCacheOut(cache, "symbolic-ref", "--short", "refs/remotes/origin/HEAD")
	if def == "" { // [ -n "$def" ]
		return false
	}
	return gitCacheQuiet(cache, "reset", "-q", "--hard", def)
}

// sourceCacheDir is bin/init.sh:112:
// "${XDG_CACHE_HOME:-$HOME/.cache}/omakase/sources/<slug>". Built by string
// concatenation, NOT filepath.Join, so it is byte-identical to v1 (the path is
// printed verbatim in the "cached at" and "stale or corrupt" messages).
func sourceCacheDir(src string) string {
	root := os.Getenv("XDG_CACHE_HOME")
	if root == "" {
		root = os.Getenv("HOME") + "/.cache"
	}
	return root + "/omakase/sources/" + sourceSlug(src)
}

// sourceSlug is bin/init.sh:108-111: a filesystem-safe basename (first 50 BYTES)
// plus "-" plus the first 8 hex of sha256(src), so distinct sources never
// collide and one source always maps to one dir.
func sourceSlug(src string) string {
	sum := sha256.Sum256([]byte(src))
	urlhash := hex.EncodeToString(sum[:])
	base := sanitizeBase(src)
	if len(base) > 50 { // printf '%.50s' — first 50 BYTES (base is ASCII after the tr)
		base = base[:50]
	}
	return base + "-" + urlhash[:8] // printf '%.8s' of the hex digest
}

// sanitizeBase is bin/init.sh:109-110:
// `sed 's,/*$,,; s,.*/,,; s,\.git$,,' | tr -c 'A-Za-z0-9._-' '-'`, empty => "source".
func sanitizeBase(src string) string {
	s := strings.TrimRight(src, "/") // s,/*$,,
	if i := strings.LastIndexByte(s, '/'); i >= 0 {
		s = s[i+1:] // s,.*/,, (basename)
	}
	s = strings.TrimSuffix(s, ".git") // s,\.git$,,
	// tr -c 'A-Za-z0-9._-' '-' : replace every byte NOT in the set with '-'.
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '.' || c == '_' || c == '-' {
			b.WriteByte(c)
		} else {
			b.WriteByte('-')
		}
	}
	out := b.String()
	if out == "" {
		return "source"
	}
	return out
}

// manifestField reproduces `sed -n 's/^<key>:[[:space:]]*//p' | head -n1 |
// sed 's/[[:space:]]*$//'` (bin/init.sh:142/145/148): the value of the FIRST
// line beginning "<key>:", with leading whitespace after the colon and all
// trailing whitespace (space, tab, CR, NL, VT, FF — POSIX [[:space:]]) stripped,
// so a CRLF manifest never leaks a ^M downstream. "" when no such line exists.
func manifestField(content []byte, key string) string {
	prefix := key + ":"
	const ws = " \t\r\n\v\f" // POSIX [[:space:]]
	sc := bufio.NewScanner(bytes.NewReader(content))
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, prefix) {
			continue
		}
		v := line[len(prefix):]
		v = strings.TrimLeft(v, ws)  // ^<key>:[[:space:]]*
		v = strings.TrimRight(v, ws) // [[:space:]]*$
		return v
	}
	return ""
}

// dirNonEmpty is `[ -n "$(ls -A "$dir")" ]` (bin/init.sh:144): the directory has
// at least one entry, including dotfiles (os.ReadDir excludes only "." / "..").
func dirNonEmpty(dir string) bool {
	entries, err := os.ReadDir(dir)
	return err == nil && len(entries) > 0
}

// copyTree mirrors `cp -RP "$src/." "$dst/"` (bin/init.sh:184): the CONTENTS of
// src are copied into dst, recursing real directories and preserving symlinks
// (never following them — WalkDir does not descend into a symlinked dir, and
// CopyEntry recreates the link). Walk order is filepath.WalkDir's LEXICAL order
// (Global Constraint 6). Fresh directories are created 0755 masked by umask, as
// `cp -R` makes them; regular files and symlinks go through CopyEntry (cp -P).
// dst must already exist (the caller's MkdirTemp).
func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, rerr := filepath.Rel(src, path)
		if rerr != nil {
			return rerr
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		t := d.Type()
		if t.IsRegular() || t&os.ModeSymlink != 0 {
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			return CopyEntry(path, target)
		}
		return nil // skip other special files (fifos/sockets) — a payload never ships them
	})
}

// gitCacheQuiet runs `git -C cache <args...>` with all child output discarded
// (nil Stdout/Stderr => the null device), returning whether it exited 0 —
// v1's `git -C "$cache" ... >/dev/null 2>&1`.
func gitCacheQuiet(cache string, args ...string) bool {
	return exec.Command("git", append([]string{"-C", cache}, args...)...).Run() == nil
}

// gitCacheOut runs `git -C cache <args...>` and returns its stdout with trailing
// newlines stripped (command-substitution semantics), "" on any error and with
// stderr suppressed — v1's `$(git -C "$cache" ... 2>/dev/null || true)`.
func gitCacheOut(cache string, args ...string) string {
	out, err := exec.Command("git", append([]string{"-C", cache}, args...)...).Output()
	if err != nil {
		return ""
	}
	return strings.TrimRight(string(out), "\n")
}

// resolvedCommit is sources.tsv column 4 for a project or personal row (design
// §9: "First real init/update records resolved commits"; plan GC4: "commit =
// full sha via `git -C <cache> rev-parse HEAD` after checkout"). Call this
// AFTER a fetchSource (direct, or via runSource) for src has already
// succeeded — the cache dir is recomputed from src via sourceCacheDir rather
// than threaded back through sourceResult/fetchSource's return values: the
// slug is a pure function of src, and re-running `rev-parse HEAD` against the
// cache fetchSource just populated is cheap.
//
// fetchSource's own clone step (bin/init.sh:104-138) always runs `git clone`
// into the cache, even when src names a local directory (git clones a local
// path just as it would a remote URL, producing a full .git checkout there
// too) — so in every currently reachable success path the cache DOES have a
// .git dir. The "no .git" arm below is therefore defensive, not exercised by
// any test: GC4 also specifies "-" for "a non-git local path", so it is kept
// as the documented fallback rather than assuming rev-parse can never fail.
func resolvedCommit(src string) string {
	cache := sourceCacheDir(src)
	if !isDir(filepath.Join(cache, ".git")) {
		return "-"
	}
	if sha := gitCacheOut(cache, "rev-parse", "HEAD"); sha != "" {
		return sha
	}
	return "-"
}
