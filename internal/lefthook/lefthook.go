// Package lefthook resolves a runnable lefthook invocation for the init,
// remove, and status verbs, self-fetching the pinned release into the
// per-machine cache when allowed.
//
// The pinned version and checksums are duplicated in bin/lib-lefthook.sh;
// TestVersionAndChecksumsMatchBash enforces the lockstep. Re-pin both files
// together from the new tag's lefthook_checksums.txt.
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

// lefthookVersion is the pinned lefthook release.
const lefthookVersion = "2.1.9"

// Baked-in SHA256 for each lefthook v2.1.9 asset, duplicated in
// bin/lib-lefthook.sh.
const (
	checksumLinuxARM64  = "304321997336c450af6b5c0cc641c59141168866fca0b1fc3767e067812600a9"
	checksumLinuxAMD64  = "0d60b0d350c923963729574f6431171f0277788884ad0c6284fa0160c36e3877"
	checksumDarwinARM64 = "fd506e05954af2062ce320d59ac1f5bf13fad8d694694a72bc6ef91e8c284e3d"
	checksumDarwinAMD64 = "0868b9b5b9cd807b0f9e0135fadaff1bd99fa026cccc15cbfd4510f0ee3b5431"
)

// checksumFor returns the expected sha256 for a mapped (osTok, archTok)
// pair, or "" if unknown. Every pair platformTokenFor produces has an entry;
// "" can only mean a re-pin updated one table and not the other.
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
// tokens. ok is false for platforms with no baked-in checksum. goos/goarch
// are parameters so tests can cover unsupported platforms.
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

// sha256File returns the lowercase hex sha256 of the file's bytes, or "" if
// it cannot be read; callers treat "" as a digest mismatch.
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

// download fetches src into dest. src may be a "file://" URL or a bare
// absolute path (copied locally, no network), or an http(s) URL; any
// transport error or non-200 status is a failure. The caller collapses all
// failures to one message, so the error text is never shown.
func download(src, dest string) error {
	switch {
	case strings.HasPrefix(src, "file://"):
		return copyLocal(strings.TrimPrefix(src, "file://"), dest)
	case strings.HasPrefix(src, "/"):
		return copyLocal(src, dest)
	}

	// src is the pinned GitHub releases base or a caller-controlled override
	// (OMAKASE_LEFTHOOK_BASE_URL).
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

// cacheRoot is `${XDG_CACHE_HOME:-$HOME/.cache}`.
func cacheRoot() string {
	if x := os.Getenv("XDG_CACHE_HOME"); x != "" {
		return x
	}
	return filepath.Join(os.Getenv("HOME"), ".cache")
}

// isExecutable reports whether the path exists, is not a directory, and has
// at least one executable bit set.
func isExecutable(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir() && info.Mode()&0o111 != 0
}

// fetch downloads the pinned lefthook release into the per-machine cache
// (${XDG_CACHE_HOME:-$HOME/.cache}/omakase/lefthook/<version>/lefthook) and
// returns its path. A cached binary is re-verified and reused. Any failure
// (unsupported platform, missing checksum, download error, checksum
// mismatch) writes one message to stderr, leaves nothing new in the cache,
// and returns ("", false).
//
// OMAKASE_LEFTHOOK_BASE_URL overrides the GitHub releases base so tests can
// serve a fixture without network. goos/goarch and checksumLookup are
// parameters for testability — a fixture cannot hash to a pinned production
// checksum, so tests supply their own lookup; resolve is the production
// caller.
func fetch(stderr io.Writer, goos, goarch string, checksumLookup func(osTok, archTok string) string) (string, bool) {
	osTok, archTok, ok := platformTokenFor(goos, goarch)
	if !ok {
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

	// Re-verify a cached binary before trusting it: a truncated or corrupt
	// cache must not win.
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

// Guidance prints the lefthook-not-found message. Callers print it once
// resolution fails entirely; fetch reports its own failures inline.
func Guidance(stderr io.Writer) {
	fmt.Fprintln(stderr, "omakase: lefthook not found and could not be fetched. Install it (e.g. 'brew install lefthook', 'mise use lefthook', or add it as a devDependency and run your package manager's install), or set LEFTHOOK_BIN=/path/to/lefthook, then re-run.")
}

// resolve is the tier walk shared by the ResolveFor* functions: LEFTHOOK_BIN
// override, `lefthook` on PATH, $root/node_modules/.bin/lefthook, the
// omakase-managed cache, then a fetch iff allowFetch. Only fetch writes to
// stderr; no-fetch callers pass io.Discard.
func resolve(root string, allowFetch bool, stderr io.Writer) (string, bool) {
	if bin := os.Getenv("LEFTHOOK_BIN"); bin != "" {
		return bin, true
	}
	if _, err := exec.LookPath("lefthook"); err == nil {
		// The bare token, not exec.LookPath's resolved absolute path.
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

// ResolveForInit resolves a lefthook invocation for init, with self-fetch
// enabled. The resolved value is word-split into argv tokens, so a
// LEFTHOOK_BIN override may carry arguments; a value that is entirely
// whitespace counts as resolution failure. stderr receives only what a
// failed fetch prints; callers print Guidance once on failure.
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

// ResolveForRemove resolves a lefthook invocation for remove: no fetch —
// uninstall never reaches for the network — and silent on failure. The
// resolved value is one argv token, never word-split (contrast
// ResolveForInit).
func ResolveForRemove(root string) ([]string, bool) {
	v, ok := resolve(root, false, io.Discard)
	if !ok {
		return nil, false
	}
	return []string{v}, true
}

// ResolveForStatus resolves a lefthook binary for the status verb: no fetch
// (status is read-only reporting) and silent on failure. The value is one
// token, never word-split. Status must walk every tier init can provision,
// including the cache — a resolver that misses one misreports
// self-provisioned machines as "gates are not running".
func ResolveForStatus(root string) (string, bool) {
	return resolve(root, false, io.Discard)
}

// ResolveForHook resolves a lefthook binary for `omakase hook` gate runs:
// no fetch — no network at commit time; init provisioned the cache — and
// silent on failure (the caller prints the blocking lines). One token,
// never word-split. Walks the same tiers as init, so a lefthook init could
// resolve is never reported missing at commit time (the #72 lesson).
func ResolveForHook(root string) (string, bool) {
	return resolve(root, false, io.Discard)
}

// PinnedVersion returns the pinned lefthook release version.
func PinnedVersion() string { return lefthookVersion }
