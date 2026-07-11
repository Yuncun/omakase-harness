// This file implements the --source arm of `omakase init`: shorthand/ref
// rewrites, the disposable source cache (refresh-or-reclone, the optional
// #ref pin, fail-closed manifest validation), and the base+delta merge
// staging. RunInit calls expandSource + runSource; the source-conditional
// tails (placed.tsv column 3, $OMK/source, the summary recommends: line)
// are wired through init.go.
//
// The merge base payload's location is handed over by the shims in
// OMAKASE_BASE_PAYLOAD and resolves binary-relative only as a last resort
// (see defaultPayload). OMAKASE_PAYLOAD is never honored here: that is the
// plain-install override, not the merge base.
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

// reOwnerRepo is the owner/repo shorthand test: two path segments, each
// starting alnum then [A-Za-z0-9._-].
var reOwnerRepo = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*/[A-Za-z0-9][A-Za-z0-9._-]*$`)

// basePayloadOverride is a test seam for the binary-relative base payload.
// It is "" in production; tests set it because os.Executable points at the
// test binary, not a deployed omakase.
var basePayloadOverride string

// defaultPayload resolves the base harness payload. Precedence:
// basePayloadOverride (test seam, "" in production) > OMAKASE_BASE_PAYLOAD
// (the shim handoff) > the binary-relative ../payload. A fetched or PATH
// binary has no payload/ sibling, so the shims export the plugin's own
// bin/../payload in OMAKASE_BASE_PAYLOAD. This is not the OMAKASE_PAYLOAD
// override, which applies only to a plain install, never the merge base.
func defaultPayload() string {
	if basePayloadOverride != "" {
		return basePayloadOverride
	}
	if v := os.Getenv("OMAKASE_BASE_PAYLOAD"); v != "" {
		return v
	}
	if exe, err := os.Executable(); err == nil {
		return filepath.Join(filepath.Dir(filepath.Dir(exe)), "payload")
	}
	return ""
}

// expandSource applies the shorthand / ref / local-dir-absolutize rewrites,
// returning the resolved source string and the pinned ref ("" if none).
// Applied to both a freshly given source and a remembered one, so a bare
// re-run round-trips a pinned ref; skipped when source is empty or already
// names an existing local path. The #ref split can leave source empty (a
// pathological "#ref"), which the caller's branch honors.
func expandSource(source string) (string, string) {
	ref := ""
	// Shorthand + #ref only when source is non-empty and not an existing path.
	if source != "" && !pathExists(source) {
		if i := strings.IndexByte(source, '#'); i >= 0 { // the first '#' splits
			ref = source[i+1:]
			source = source[:i]
		}
		// owner/repo -> a GitHub URL, unless it already looks like a URL/scp path.
		if !isURLish(source) && reOwnerRepo.MatchString(source) {
			source = "https://github.com/" + source
		}
	}
	// A local directory source becomes absolute before it is cached,
	// ledgered, or remembered: a relative remembered path breaks bare
	// re-runs from another cwd. filepath.Abs cleans without resolving
	// symlinks.
	if source != "" && isDir(source) {
		if abs, err := filepath.Abs(source); err == nil {
			source = abs
		}
	}
	return source, ref
}

// isURLish reports whether s already contains a scheme ("://") or is an
// scp-style `git@host:...` path; the owner/repo shorthand is not applied
// then.
func isURLish(s string) bool {
	return strings.Contains(s, "://") || strings.HasPrefix(s, "git@")
}

// pathExists reports whether p exists, following symlinks; a dangling
// symlink counts as absent, so the shorthand still applies.
func pathExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// sourceResult carries what a --source install contributes back to RunInit.
type sourceResult struct {
	payload    string // the merged base+delta staging dir (becomes the payload)
	merged     string // the staging dir to clean up on every exit path (== payload)
	label      string // placed.tsv column 3
	remembered string // written to $OMK/source
	recommends string // manifest recommends:, surfaced in the summary
}

// runSource fetches the source into the disposable cache, validates it, and
// stages the base+source merge. On any failure it has already written the
// message and cleaned up any staging dir it created; the caller returns the
// code. On success the caller must `defer os.RemoveAll(result.merged)` so
// the staging dir is removed on every subsequent exit path.
func runSource(source, sourceRef, basePayload string, stdout, stderr io.Writer) (sourceResult, int) {
	// Fail before any clone/fetch when the merge base is absent: a missing
	// base means the shim handoff (OMAKASE_BASE_PAYLOAD, see defaultPayload)
	// never happened. The message names the path so a bad handoff is
	// diagnosable.
	if info, err := os.Stat(basePayload); err != nil || !info.IsDir() {
		fmt.Fprintf(stderr, "omakase: base payload not found at %s — set OMAKASE_BASE_PAYLOAD or run omakase via the plugin's bin/ shims\n", basePayload)
		return sourceResult{}, 1
	}

	payloadDir, recommends, code := fetchSource(source, sourceRef, stdout, stderr)
	if code != 0 {
		return sourceResult{}, code // nothing staged yet
	}

	label := source
	if sourceRef != "" {
		label = source + "#" + sourceRef
	}

	// The staging dir lands under ${TMPDIR:-/tmp}; its random suffix never
	// surfaces — placement derives its rel paths against this dir.
	merged, err := os.MkdirTemp(os.TempDir(), "omakase-merge.")
	if err != nil {
		fmt.Fprintln(stderr, "omakase: could not create a temp dir to merge the base + source payload")
		return sourceResult{}, 1
	}

	// Copy the base payload tree first. Its existence was checked up top;
	// the message still covers a genuine copy error (permissions, a base
	// that disappeared mid-run).
	if err := copyTree(basePayload, merged); err != nil {
		os.RemoveAll(merged)
		fmt.Fprintf(stderr, "omakase: failed to copy the base payload (%s) into the merge staging dir\n", basePayload)
		return sourceResult{}, 1
	}

	// Overlay the source delta with replace semantics, one file at a time: a
	// base symlink at the same path is removed and the source wins (never
	// written through), and a source symlink is carried across as a symlink.
	// The per-entry effect is order-independent (each rel is disjoint).
	rels, err := walkPayload(payloadDir)
	if err != nil {
		os.RemoveAll(merged)
		return sourceResult{}, 1
	}
	overlayOne := func(rel string) error {
		dst := filepath.Join(merged, rel)
		// safeMkdirAll: the base (or an earlier delta entry) must never
		// leave a directory symlink that a later file is written through,
		// out of the staging dir.
		if err := safeMkdirAll(merged, filepath.Dir(dst)); err != nil {
			return err
		}
		if err := os.RemoveAll(dst); err != nil { // also clears a base dir at this path
			return err
		}
		return CopyEntry(filepath.Join(payloadDir, rel), dst)
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

// fetchSource resolves the disposable cache dir from the source slug,
// refreshes an existing clone (or discards and reclones a stale/corrupt
// one), pins an optional #ref, then validates the manifest fail-closed
// before anything is placed. On success it prints the "cached at" line and
// returns the cache's payload/ dir and the manifest's recommends value; on
// failure it writes the message and returns a non-zero code.
func fetchSource(src, sourceRef string, stdout, stderr io.Writer) (payloadDir, recommends string, code int) {
	cache := sourceCacheDir(src)

	// An existing clone is refreshed to the remote default branch (never
	// merged — cache state has no standing). The refresh runs before the
	// #ref checkout below, so a branch pin does not survive a bare re-run:
	// the hard reset lands the checked-out branch on default-branch content,
	// and re-checking out that branch name yields the default content. A tag
	// pin does survive, since a tag's ref is immutable (see
	// TestBranchPinNotPreserved vs. TestRememberedSourceRoundTrip).
	if isDir(filepath.Join(cache, ".git")) {
		if !refreshCache(cache) {
			// A refresh failure can mean the cache is genuinely corrupt
			// (discard and reclone) or the fetch merely could not reach the
			// remote (offline/transient), in which case the on-disk checkout
			// is still usable — a bare-init repair must survive with no
			// network. Discard only when the cache does not look like a
			// healthy, reusable checkout: HEAD resolves locally and it still
			// carries a manifest + payload/.
			if cacheGitHealthy(cache) &&
				fileRegular(filepath.Join(cache, "omakase.manifest")) &&
				isDir(filepath.Join(cache, "payload")) {
				fmt.Fprintf(stderr, "omakase: could not refresh source cache at %s — reusing the cached copy (offline?)\n", cache)
			} else {
				fmt.Fprintf(stderr, "omakase: source cache at %s is stale or corrupt — discarding and re-cloning (a cache is disposable)\n", cache)
				os.RemoveAll(cache)
			}
		}
	}
	if !isDir(filepath.Join(cache, ".git")) {
		os.RemoveAll(cache)
		os.MkdirAll(filepath.Dir(cache), 0o755)
		clone := exec.Command("git", "clone", "-q", "--", src, cache)
		clone.Stdout = stdout // -q: silent on success
		clone.Stderr = stderr
		if err := clone.Run(); err != nil {
			fmt.Fprintf(stderr, "omakase: could not clone source '%s' into the cache (%s)\n", src, cache)
			return "", "", 1
		}
	}

	// Pin to a requested #ref (branch or tag). Tags are fetched first so a
	// tag ref resolves; a tag-fetch failure is ignored.
	if sourceRef != "" {
		gitCacheQuiet(cache, "fetch", "-q", "--tags", "origin") // failure ignored
		co := exec.Command("git", "-C", cache, "-c", "advice.detachedHead=false", "checkout", "-q", sourceRef)
		if err := co.Run(); err != nil {
			fmt.Fprintf(stderr, "omakase: source '%s' has no ref '%s' (no such branch or tag)\n", src, sourceRef)
			return "", "", 1
		}
	}

	// Fail-closed manifest validation, before anything is placed.
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
		verPart = ", version: " + ver
	}
	fmt.Fprintf(stdout, "omakase: source '%s' (name: %s%s) cached at %s\n", src, name, verPart, cache)
	return payloadDir, recommends, 0
}

// refreshCache fetches origin, sets the remote HEAD (failure ignored),
// reads the default branch, and hard-resets to it. Returns true iff the
// whole chain succeeds; false tells the caller to discard and reclone. All
// child output is discarded.
func refreshCache(cache string) bool {
	if !gitCacheQuiet(cache, "fetch", "-q", "origin") {
		return false
	}
	gitCacheQuiet(cache, "remote", "set-head", "origin", "-a") // failure ignored
	def := gitCacheOut(cache, "symbolic-ref", "--short", "refs/remotes/origin/HEAD")
	if def == "" {
		return false
	}
	return gitCacheQuiet(cache, "reset", "-q", "--hard", def)
}

// sourceCacheDir is "${XDG_CACHE_HOME:-$HOME/.cache}/omakase/sources/<slug>",
// built by string concatenation; the path is printed verbatim in the
// "cached at" and "stale or corrupt" messages.
func sourceCacheDir(src string) string {
	root := os.Getenv("XDG_CACHE_HOME")
	if root == "" {
		root = os.Getenv("HOME") + "/.cache"
	}
	return root + "/omakase/sources/" + sourceSlug(src)
}

// sourceSlug is a filesystem-safe basename (first 50 bytes) plus "-" plus
// the first 8 hex of sha256(src), so distinct sources never collide and one
// source always maps to one dir.
func sourceSlug(src string) string {
	sum := sha256.Sum256([]byte(src))
	urlhash := hex.EncodeToString(sum[:])
	base := sanitizeBase(src)
	if len(base) > 50 { // first 50 bytes; base is ASCII after sanitizeBase
		base = base[:50]
	}
	return base + "-" + urlhash[:8]
}

// sanitizeBase reduces src to its basename with trailing "/" runs and a
// ".git" suffix stripped, every byte outside [A-Za-z0-9._-] replaced with
// '-', and "" mapped to "source".
func sanitizeBase(src string) string {
	s := strings.TrimRight(src, "/")
	if i := strings.LastIndexByte(s, '/'); i >= 0 {
		s = s[i+1:] // basename
	}
	s = strings.TrimSuffix(s, ".git")
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

// manifestField returns the value of the first line beginning "<key>:",
// with whitespace after the colon and all trailing whitespace (including
// CR) stripped, so a CRLF manifest never leaks a ^M downstream; "" when no
// such line exists.
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
		v = strings.TrimLeft(v, ws)
		v = strings.TrimRight(v, ws)
		return v
	}
	return ""
}

// dirNonEmpty reports whether the directory has at least one entry,
// including dotfiles.
func dirNonEmpty(dir string) bool {
	entries, err := os.ReadDir(dir)
	return err == nil && len(entries) > 0
}

// copyTree copies the contents of src into dst, recursing real directories
// and preserving symlinks (never following them). Fresh directories are
// created 0o755 masked by umask; regular files and symlinks go through
// CopyEntry. dst must already exist.
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

// cacheGitHealthy reports whether cache/.git is a structurally sound git
// checkout — HEAD resolves to a real object — using a purely local git
// operation. A mangled ref store fails this; a healthy checkout whose remote
// has gone unreachable passes it.
func cacheGitHealthy(cache string) bool {
	return gitCacheQuiet(cache, "rev-parse", "--verify", "-q", "HEAD")
}

// gitCacheQuiet runs `git -C cache <args...>` with all child output
// discarded, returning whether it exited 0.
func gitCacheQuiet(cache string, args ...string) bool {
	return exec.Command("git", append([]string{"-C", cache}, args...)...).Run() == nil
}

// gitCacheOut runs `git -C cache <args...>` and returns its stdout with
// trailing newlines stripped, "" on any error, with stderr suppressed.
func gitCacheOut(cache string, args ...string) string {
	out, err := exec.Command("git", append([]string{"-C", cache}, args...)...).Output()
	if err != nil {
		return ""
	}
	return strings.TrimRight(string(out), "\n")
}
