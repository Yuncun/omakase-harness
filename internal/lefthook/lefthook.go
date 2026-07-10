// Package lefthook ports bin/lib-lefthook.sh's lefthook resolution and
// pinned self-fetch (lib-lefthook.sh:1-168) -- the shared library
// bin/init.sh and bin/remove.sh source to obtain a runnable lefthook
// invocation without requiring a global install.
//
// bin/lib-lefthook.sh STAYS bash, unchanged: tests/lefthook-fetch.test.sh
// sources it directly and drives its functions by name. This package's
// pinned version and four checksums are DUPLICATES of that file's, kept
// honest by TestVersionAndChecksumsMatchBash (lefthook_test.go), which
// parses bin/lib-lefthook.sh with regexps and asserts equality against the
// Go constants below -- keep in lockstep; re-pinning means bumping
// lefthookVersion AND the four checksums here AND their bash twins in
// bin/lib-lefthook.sh together, from the new tag's lefthook_checksums.txt.
package lefthook

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// lefthookVersion is the pinned lefthook release (lib-lefthook.sh:21).
const lefthookVersion = "2.1.9"

// Baked-in SHA256 for each lefthook v2.1.9 asset (lib-lefthook.sh:35-38),
// duplicated verbatim -- see the package doc comment.
const (
	checksumLinuxARM64  = "304321997336c450af6b5c0cc641c59141168866fca0b1fc3767e067812600a9"
	checksumLinuxAMD64  = "0d60b0d350c923963729574f6431171f0277788884ad0c6284fa0160c36e3877"
	checksumDarwinARM64 = "fd506e05954af2062ce320d59ac1f5bf13fad8d694694a72bc6ef91e8c284e3d"
	checksumDarwinAMD64 = "0868b9b5b9cd807b0f9e0135fadaff1bd99fa026cccc15cbfd4510f0ee3b5431"
)

// checksumFor mirrors lefthook_sha256_for (lib-lefthook.sh:33-41): the
// expected sha256 for a mapped (osTok, archTok) pair, or "" if unknown. In
// practice every pair platformTokenFor can produce has an entry here (the
// bash suite's own L1 scenario asserts the same invariant) -- the "" arm
// stays reachable only through a future re-pin that updates one table but
// not the other, exactly as the bash `case` block's `*) echo "";;` catch-all
// does.
func checksumFor(osTok, archTok string) string {
	switch osTok + "_" + archTok {
	case "Linux_arm64":
		return checksumLinuxARM64
	case "Linux_x86_64":
		return checksumLinuxAMD64
	case "MacOS_arm64":
		return checksumDarwinARM64
	case "MacOS_x86_64":
		return checksumDarwinAMD64
	default:
		return ""
	}
}

// platformTokenFor maps a GOOS/GOARCH pair to lefthook's asset OS/ARCH
// tokens -- the Go twin of lefthook_platform (lib-lefthook.sh:45-60), using
// runtime.GOOS/GOARCH instead of `uname -s`/`uname -m` (Global Constraint 9:
// identical result on every platform this suite or its CI runners exercise).
// ok is false for any platform with no baked-in checksum -- the caller's
// fail path. Takes goos/goarch as parameters (rather than reading
// runtime.GOOS/GOARCH itself) purely so tests can exercise every branch,
// including the unsupported ones, without needing to actually run there.
func platformTokenFor(goos, goarch string) (osTok, archTok string, ok bool) {
	switch goos {
	case "darwin":
		osTok = "MacOS"
	case "linux":
		osTok = "Linux"
	default:
		return "", "", false
	}
	switch goarch {
	case "arm64":
		archTok = "arm64"
	case "amd64":
		archTok = "x86_64"
	default:
		return "", "", false
	}
	return osTok, archTok, true
}

// sha256File is the Go twin of lefthook_sha256_file (lib-lefthook.sh:68-72):
// the lowercase hex sha256 of a file's bytes, or "" if it cannot be read.
// Accepted divergence (Global Constraint 9): bash re-detects shasum/
// sha256sum per call and, absent either tool, echoes nothing -- the caller
// treats that empty actual as a mismatch (lib-lefthook.sh:135-139, "no
// shasum/sha256sum available"). Go always has crypto/sha256, so that branch
// is unreachable here; an empty actual from sha256File can only mean the
// file could not be opened (e.g. the earlier download step already failed),
// and still flows into the same "mismatch" handling as bash's fallback.
func sha256File(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return ""
	}
	return hex.EncodeToString(h.Sum(nil))
}

// download is the Go twin of lefthook_download (lib-lefthook.sh:74-91): src
// may be a "file://" URL or a bare absolute path (both copied locally, no
// network -- the test fixture path bash's own tests use), or else an
// http(s) URL fetched with net/http (bash's curl/wget). Any transport error
// or a non-200 status both count as failure -- the caller (fetch) collapses
// every failure from download to one message, so this error's own text is
// never observed.
func download(src, dest string) error {
	switch {
	case strings.HasPrefix(src, "file://"):
		return copyLocal(strings.TrimPrefix(src, "file://"), dest)
	case strings.HasPrefix(src, "/"):
		return copyLocal(src, dest)
	}

	// src is the pinned GitHub releases base or a caller-controlled override
	// (OMAKASE_LEFTHOOK_BASE_URL) -- the same variable bash's curl/wget fetch.
	resp, err := http.Get(src)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("lefthook: download %s: status %s", src, resp.Status)
	}
	out, err := os.Create(dest)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, resp.Body); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

func copyLocal(src, dest string) error {
	info, err := os.Stat(src)
	if err != nil || info.IsDir() {
		return fmt.Errorf("lefthook: source %q not found", src)
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dest)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// cacheRoot is `${XDG_CACHE_HOME:-$HOME/.cache}` (lib-lefthook.sh:115).
func cacheRoot() string {
	if x := os.Getenv("XDG_CACHE_HOME"); x != "" {
		return x
	}
	return filepath.Join(os.Getenv("HOME"), ".cache")
}

// isExecutable is the Go twin of a bash `[ -x path ]` test: the path exists,
// is not a directory, and has at least one executable bit set. Since #72
// internal/status resolves through ResolveForStatus (below), so this is the
// ONE copy of the probe -- the pre-#72 3-tier twin in internal/status is gone.
func isExecutable(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir() && info.Mode()&0o111 != 0
}

// fetch is the Go twin of fetch_lefthook (lib-lefthook.sh:103-149): fetches
// the pinned lefthook release into the per-machine cache and returns its
// path. One download per machine -- a cached, re-verified binary is reused.
// Any failure (unsupported platform, no baked-in checksum, download error,
// checksum mismatch) writes the matching bash message to stderr, leaves
// nothing new in the cache, and returns ("", false).
//
// Cache: ${XDG_CACHE_HOME:-$HOME/.cache}/omakase/lefthook/<version>/lefthook.
// Base URL: OMAKASE_LEFTHOOK_BASE_URL overrides the GitHub releases base so
// tests can serve a fixture from a local path or file:// URL with no
// network. goos/goarch and checksumLookup are parameters (rather than
// runtime.GOOS/GOARCH and checksumFor read directly) purely for testability:
// checksumLookup lets a test supply a fixture's REAL digest in place of the
// pinned production hashes -- there is no computable preimage for a sha256
// value, so a test fixture can never hash to one of the four constants
// above. resolve (below) is the production caller, fixing these to
// runtime.GOOS/GOARCH and checksumFor; nothing outside this package needs
// fetch directly, so it stays unexported.
func fetch(stderr io.Writer, goos, goarch string, checksumLookup func(osTok, archTok string) string) (string, bool) {
	osTok, archTok, ok := platformTokenFor(goos, goarch)
	if !ok {
		// Divergence, documented: bash interpolates `uname -s`/`uname -m`
		// (e.g. "Linux/riscv64"); Go prints the raw goos/goarch instead
		// (e.g. "linux/riscv64") -- cosmetic only, and unreachable on every
		// platform this suite or its CI runners (darwin/linux x
		// arm64/amd64) exercise.
		fmt.Fprintf(stderr, "omakase: lefthook self-fetch unsupported on this platform (%s/%s).\n", goos, goarch)
		return "", false
	}
	asset := fmt.Sprintf("lefthook_%s_%s_%s", lefthookVersion, osTok, archTok)
	expected := checksumLookup(osTok, archTok)
	if expected == "" {
		fmt.Fprintf(stderr, "omakase: no baked-in checksum for %s — refusing to fetch.\n", asset)
		return "", false
	}

	cacheDir := filepath.Join(cacheRoot(), "omakase", "lefthook", lefthookVersion)
	cacheBin := filepath.Join(cacheDir, "lefthook")

	// Already cached and verified-good earlier? Re-verify cheaply before
	// trusting it (a truncated/corrupt cache should not silently win), then
	// reuse.
	if isExecutable(cacheBin) {
		if actual := sha256File(cacheBin); actual != "" && actual == expected {
			return cacheBin, true
		}
		os.Remove(cacheBin) // corrupt -- drop it and re-fetch
	}

	base := os.Getenv("OMAKASE_LEFTHOOK_BASE_URL")
	if base == "" {
		base = fmt.Sprintf("https://github.com/evilmartians/lefthook/releases/download/v%s", lefthookVersion)
	}
	url := base + "/" + asset

	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return "", false
	}
	tmp := filepath.Join(cacheDir, fmt.Sprintf(".lefthook.download.%d", os.Getpid()))
	os.Remove(tmp)

	if err := download(url, tmp); err != nil {
		fmt.Fprintf(stderr, "omakase: could not download lefthook from %s\n", url)
		os.Remove(tmp)
		return "", false
	}

	actual := sha256File(tmp)
	if actual != expected {
		fmt.Fprintf(stderr, "omakase: lefthook checksum mismatch for %s (expected %s, got %s) — refusing it.\n", asset, expected, actual)
		os.Remove(tmp)
		return "", false
	}

	if err := os.Chmod(tmp, 0o755); err != nil {
		os.Remove(tmp)
		return "", false
	}
	if err := os.Rename(tmp, cacheBin); err != nil { // atomic within the cache dir
		os.Remove(tmp)
		return "", false
	}
	return cacheBin, true
}

// Guidance prints the one lefthook-not-found message (lib-lefthook.sh:27),
// verbatim. Callers print it once resolution fails entirely -- the same way
// bin/init.sh calls lefthook_install_guidance as a separate step after
// resolve_lefthook returns non-zero (resolve/fetch failures themselves are
// reported inline by fetch, above; this is the final "give up" line).
func Guidance(stderr io.Writer) {
	fmt.Fprintln(stderr, "omakase: lefthook not found and could not be fetched. Install it (e.g. 'brew install lefthook', 'mise use lefthook', or add it as a devDependency and run your package manager's install), or set LEFTHOOK_BIN=/path/to/lefthook, then re-run.")
}

// resolve is the shared tier walk behind ResolveForInit/ResolveForRemove --
// the Go twin of resolve_lefthook (lib-lefthook.sh:154-168): LEFTHOOK_BIN
// override, `lefthook` on PATH, $root/node_modules/.bin/lefthook if
// executable, the omakase-managed cache, then (iff allowFetch) a fetch.
// stderr is only ever written to by tier 5 (fetch) -- pass io.Discard when
// the caller must stay silent on failure (ResolveForRemove never fetches,
// so it is never actually written to, but resolve always takes one so the
// two callers share this one tier walk).
func resolve(root string, allowFetch bool, stderr io.Writer) (string, bool) {
	if bin := os.Getenv("LEFTHOOK_BIN"); bin != "" {
		return bin, true
	}
	if _, err := exec.LookPath("lefthook"); err == nil {
		// Bare token, matching bash setting LEFTHOOK="lefthook"
		// (lib-lefthook.sh:157) -- not exec.LookPath's resolved absolute path.
		return "lefthook", true
	}
	cand := filepath.Join(root, "node_modules", ".bin", "lefthook")
	if isExecutable(cand) {
		return cand, true
	}
	cacheBin := filepath.Join(cacheRoot(), "omakase", "lefthook", lefthookVersion, "lefthook")
	if isExecutable(cacheBin) {
		return cacheBin, true
	}
	if allowFetch {
		return fetch(stderr, runtime.GOOS, runtime.GOARCH, checksumFor)
	}
	return "", false
}

// ResolveForInit resolves a runnable lefthook invocation the way
// bin/init.sh does: resolve_lefthook("fetch") (lib-lefthook.sh:154-168),
// with tier 5 (self-fetch) enabled. init.sh then runs the result UNQUOTED
// ($LEFTHOOK install, init.sh:649), so a resolved value containing
// whitespace (only possible via LEFTHOOK_BIN -- every other tier's value is
// a single bare word or path) word-splits into multiple argv tokens;
// strings.Fields is that split. A resolved value that is entirely
// whitespace splits to zero tokens, which this treats as resolution failure
// (bash would instead try, and fail, to exec a literal "install" -- an
// unreachable edge case in practice, made a clean failure here instead of a
// nonsensical one-word invocation).
//
// stderr receives any message a failed fetch attempt itself prints (fetch,
// above); "nothing resolved at all" prints nothing here -- callers print
// Guidance once on failure, matching bin/init.sh's own separate
// lefthook_install_guidance call.
func ResolveForInit(root string, stderr io.Writer) ([]string, bool) {
	v, ok := resolve(root, true, stderr)
	if !ok {
		return nil, false
	}
	fields := strings.Fields(v)
	if len(fields) == 0 {
		return nil, false
	}
	return fields, true
}

// ResolveForRemove resolves a runnable lefthook invocation the way
// bin/remove.sh does: resolve_lefthook() (lib-lefthook.sh:154-168, no
// "fetch" argument) -- tier 5 never fetches, only reuses an already-cached
// binary. remove.sh then runs the result QUOTED ("$LEFTHOOK" uninstall,
// remove.sh:22), so the resolved value is always ONE argv token even if it
// contains whitespace -- no strings.Fields split (contrast
// ResolveForInit). Resolution failure is silent in bash: remove.sh has no
// lefthook_install_guidance call on this path (unlike init.sh's), so this
// never writes to any stream -- its signature has no io.Writer at all.
func ResolveForRemove(root string) ([]string, bool) {
	v, ok := resolve(root, false, io.Discard)
	if !ok {
		return nil, false
	}
	return []string{v}, true
}

// ResolveForStatus resolves a runnable lefthook for the status verb: the
// shared no-fetch tier walk (LEFTHOOK_BIN -> `lefthook` on PATH ->
// node_modules/.bin -> the omakase-managed cache), silent on failure --
// status is read-only reporting, so it must never download anything and
// renders its "not resolved" note off a false return instead. The value is
// ONE token, never word-split: status runs `<lh> dump` the way
// bin/legacy/status.sh's render_guards does ("$LEFTHOOK" dump, quoted).
// Until #72 internal/status carried a local 3-tier copy that missed the
// cache tier, so a machine whose lefthook was self-provisioned by init
// falsely reported "gates are not running".
func ResolveForStatus(root string) (string, bool) {
	return resolve(root, false, io.Discard)
}

// PinnedVersion exposes the pinned lefthook release (lib-lefthook.sh:21's
// LEFTHOOK_VERSION twin) so callers and tests can derive the cache path
// without duplicating the constant.
func PinnedVersion() string { return lefthookVersion }
