package lefthook

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

// ---------------------------------------------------------------- lockstep
//
// bin/lib-lefthook.sh STAYS bash unchanged (tests/lefthook-fetch.test.sh
// sources it directly and drives its functions by name). This package's
// pinned version and four checksums are DUPLICATES of that file's, kept
// honest by parsing the bash file itself here and asserting equality
// against the Go constants -- re-pinning means bumping both together.

func TestVersionAndChecksumsMatchBash(t *testing.T) {
	data, err := os.ReadFile("../../bin/lib-lefthook.sh")
	if err != nil {
		t.Fatal(err)
	}
	src := string(data)

	verRe := regexp.MustCompile(`(?m)^LEFTHOOK_VERSION="([^"]+)"$`)
	m := verRe.FindStringSubmatch(src)
	if m == nil {
		t.Fatal(`LEFTHOOK_VERSION="..." not found in bin/lib-lefthook.sh`)
	}
	if m[1] != lefthookVersion {
		t.Errorf("Go lefthookVersion = %q, bash LEFTHOOK_VERSION = %q", lefthookVersion, m[1])
	}

	sumRe := regexp.MustCompile(`lefthook_[0-9.]+_(Linux|MacOS)_(arm64|x86_64)\)\s+echo "([0-9a-f]{64})";;`)
	matches := sumRe.FindAllStringSubmatch(src, -1)
	if len(matches) != 4 {
		t.Fatalf("expected 4 lefthook_sha256_for case lines in bin/lib-lefthook.sh, found %d", len(matches))
	}
	got := make(map[string]string, 4)
	for _, mm := range matches {
		got[mm[1]+"_"+mm[2]] = mm[3]
	}
	want := map[string]string{
		"Linux_arm64":  checksumLinuxARM64,
		"Linux_x86_64": checksumLinuxAMD64,
		"MacOS_arm64":  checksumDarwinARM64,
		"MacOS_x86_64": checksumDarwinAMD64,
	}
	for k, wantHash := range want {
		if got[k] != wantHash {
			t.Errorf("checksum for %s: Go constant = %q, bash = %q", k, wantHash, got[k])
		}
	}
}

// ---------------------------------------------------------------- platform mapping

func TestPlatformTokenFor(t *testing.T) {
	cases := []struct {
		goos, goarch     string
		wantOS, wantArch string
		wantOK           bool
	}{
		{"darwin", "arm64", "MacOS", "arm64", true},
		{"darwin", "amd64", "MacOS", "x86_64", true},
		{"linux", "arm64", "Linux", "arm64", true},
		{"linux", "amd64", "Linux", "x86_64", true},
		{"freebsd", "amd64", "", "", false},
		{"linux", "riscv64", "", "", false},
		{"windows", "amd64", "", "", false},
		{"darwin", "386", "", "", false},
	}
	for _, c := range cases {
		gotOS, gotArch, gotOK := platformTokenFor(c.goos, c.goarch)
		if gotOK != c.wantOK || gotOS != c.wantOS || gotArch != c.wantArch {
			t.Errorf("platformTokenFor(%q,%q) = (%q,%q,%v), want (%q,%q,%v)",
				c.goos, c.goarch, gotOS, gotArch, gotOK, c.wantOS, c.wantArch, c.wantOK)
		}
	}
}

func TestChecksumFor(t *testing.T) {
	cases := []struct{ osTok, archTok, want string }{
		{"Linux", "arm64", checksumLinuxARM64},
		{"Linux", "x86_64", checksumLinuxAMD64},
		{"MacOS", "arm64", checksumDarwinARM64},
		{"MacOS", "x86_64", checksumDarwinAMD64},
		{"Windows", "arm64", ""},
		{"Linux", "riscv64", ""},
	}
	for _, c := range cases {
		if got := checksumFor(c.osTok, c.archTok); got != c.want {
			t.Errorf("checksumFor(%q,%q) = %q, want %q", c.osTok, c.archTok, got, c.want)
		}
	}
}

// ---------------------------------------------------------------- Guidance

func TestGuidance(t *testing.T) {
	var buf bytes.Buffer
	Guidance(&buf)
	want := "omakase: lefthook not found and could not be fetched. Install it (e.g. 'brew install lefthook', 'mise use lefthook', or add it as a devDependency and run your package manager's install), or set LEFTHOOK_BIN=/path/to/lefthook, then re-run.\n"
	if buf.String() != want {
		t.Errorf("Guidance() wrote %q, want %q", buf.String(), want)
	}
}

// ---------------------------------------------------------------- fetch

func writeFixtureAsset(t *testing.T, dir, name, content string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func sha256Hex(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// hostAsset returns this test host's platform tokens and asset name, or
// skips the test outright -- the fetch happy-path/mismatch/cache tests need
// a real (mapped) host asset name to build a fixture and base URL around;
// every darwin/linux x arm64/amd64 runner this suite targets maps.
func hostAsset(t *testing.T) (osTok, archTok, asset string) {
	t.Helper()
	osTok, archTok, ok := platformTokenFor(runtime.GOOS, runtime.GOARCH)
	if !ok {
		t.Skipf("host platform %s/%s unsupported by the fetcher", runtime.GOOS, runtime.GOARCH)
	}
	return osTok, archTok, fmt.Sprintf("lefthook_%s_%s_%s", lefthookVersion, osTok, archTok)
}

func TestFetchUnsupportedPlatformMessage(t *testing.T) {
	var buf bytes.Buffer
	path, ok := fetch(&buf, "linux", "riscv64", checksumFor)
	if ok || path != "" {
		t.Fatalf("fetch(unsupported) = (%q,%v), want (\"\",false)", path, ok)
	}
	// Divergence, documented: bash interpolates `uname -s`/`uname -m`
	// (e.g. "Linux/riscv64"); Go has no uname call here and prints the raw
	// goos/goarch instead (e.g. "linux/riscv64") -- cosmetic only, and
	// unreachable on every platform this suite or its CI runners exercise.
	want := "omakase: lefthook self-fetch unsupported on this platform (linux/riscv64).\n"
	if buf.String() != want {
		t.Errorf("fetch(unsupported) stderr = %q, want %q", buf.String(), want)
	}
}

func TestFetchHappyPathFileURL(t *testing.T) {
	osTok, archTok, asset := hostAsset(t)
	base := t.TempDir()
	content := "#!/bin/sh\necho fixture-lefthook \"$@\"\n"
	assetPath := writeFixtureAsset(t, base, asset, content)
	goodHash := sha256Hex(t, assetPath)
	lookup := func(o, a string) string {
		if o == osTok && a == archTok {
			return goodHash
		}
		return ""
	}

	cacheHome := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cacheHome)
	t.Setenv("OMAKASE_LEFTHOOK_BASE_URL", "file://"+base)

	var buf bytes.Buffer
	path, ok := fetch(&buf, runtime.GOOS, runtime.GOARCH, lookup)
	if !ok {
		t.Fatalf("fetch failed: stderr=%q", buf.String())
	}
	wantCache := filepath.Join(cacheHome, "omakase", "lefthook", lefthookVersion, "lefthook")
	if path != wantCache {
		t.Errorf("fetch returned %q, want %q", path, wantCache)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("cached binary missing: %v", err)
	}
	if info.Mode()&0o111 == 0 {
		t.Errorf("cached binary not executable: mode=%v", info.Mode())
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != content {
		t.Errorf("cached content = %q, %v, want %q", got, err, content)
	}
	leftovers, _ := filepath.Glob(filepath.Join(cacheHome, "omakase", "lefthook", lefthookVersion, ".lefthook.download.*"))
	if len(leftovers) != 0 {
		t.Errorf("temp download residue left behind: %v", leftovers)
	}
}

func TestFetchHappyPathBarePath(t *testing.T) {
	osTok, archTok, asset := hostAsset(t)
	base := t.TempDir()
	content := "#!/bin/sh\necho fixture-lefthook \"$@\"\n"
	assetPath := writeFixtureAsset(t, base, asset, content)
	goodHash := sha256Hex(t, assetPath)
	lookup := func(o, a string) string {
		if o == osTok && a == archTok {
			return goodHash
		}
		return ""
	}

	cacheHome := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cacheHome)
	t.Setenv("OMAKASE_LEFTHOOK_BASE_URL", base) // bare absolute path, no file:// scheme

	var buf bytes.Buffer
	path, ok := fetch(&buf, runtime.GOOS, runtime.GOARCH, lookup)
	if !ok {
		t.Fatalf("fetch failed: stderr=%q", buf.String())
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != content {
		t.Errorf("cached content = %q, %v, want %q", got, err, content)
	}
}

func TestFetchChecksumMismatchRejected(t *testing.T) {
	osTok, archTok, asset := hostAsset(t)
	base := t.TempDir()
	assetPath := writeFixtureAsset(t, base, asset, "totally-wrong-bytes\n")
	wrongExpected := strings.Repeat("0", 64)
	lookup := func(o, a string) string {
		if o == osTok && a == archTok {
			return wrongExpected
		}
		return ""
	}
	cacheHome := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cacheHome)
	t.Setenv("OMAKASE_LEFTHOOK_BASE_URL", "file://"+base)

	var buf bytes.Buffer
	path, ok := fetch(&buf, runtime.GOOS, runtime.GOARCH, lookup)
	if ok || path != "" {
		t.Fatalf("fetch(mismatch) = (%q,%v), want (\"\",false)", path, ok)
	}
	actual := sha256Hex(t, assetPath)
	want := fmt.Sprintf("omakase: lefthook checksum mismatch for %s (expected %s, got %s) — refusing it.\n", asset, wrongExpected, actual)
	if buf.String() != want {
		t.Errorf("stderr = %q, want %q", buf.String(), want)
	}
	cacheBin := filepath.Join(cacheHome, "omakase", "lefthook", lefthookVersion, "lefthook")
	if _, err := os.Stat(cacheBin); err == nil {
		t.Errorf("a binary was cached despite the mismatch")
	}
	leftovers, _ := filepath.Glob(filepath.Join(cacheHome, "omakase", "lefthook", lefthookVersion, ".lefthook.download.*"))
	if len(leftovers) != 0 {
		t.Errorf("temp download residue left behind on mismatch: %v", leftovers)
	}
}

func TestFetchCachedBinaryReuse(t *testing.T) {
	osTok, archTok, _ := hostAsset(t)
	content := "#!/bin/sh\necho cached\n"
	cacheHome := t.TempDir()
	cacheDir := filepath.Join(cacheHome, "omakase", "lefthook", lefthookVersion)
	cacheBin := writeFixtureAsset(t, cacheDir, "lefthook", content)
	if err := os.Chmod(cacheBin, 0o755); err != nil {
		t.Fatal(err)
	}
	goodHash := sha256Hex(t, cacheBin)
	lookup := func(o, a string) string {
		if o == osTok && a == archTok {
			return goodHash
		}
		return ""
	}
	t.Setenv("XDG_CACHE_HOME", cacheHome)
	// A base URL to nowhere: if fetch tried to download instead of reusing
	// the cache, this would fail.
	t.Setenv("OMAKASE_LEFTHOOK_BASE_URL", filepath.Join(t.TempDir(), "does-not-exist"))

	var buf bytes.Buffer
	path, ok := fetch(&buf, runtime.GOOS, runtime.GOARCH, lookup)
	if !ok {
		t.Fatalf("fetch failed to reuse cache: stderr=%q", buf.String())
	}
	if path != cacheBin {
		t.Errorf("fetch returned %q, want reused cache path %q", path, cacheBin)
	}
	if buf.Len() != 0 {
		t.Errorf("fetch wrote to stderr on a cache hit: %q", buf.String())
	}
}

func TestFetchCorruptCacheRefetched(t *testing.T) {
	osTok, archTok, asset := hostAsset(t)
	base := t.TempDir()
	goodContent := "#!/bin/sh\necho good\n"
	assetPath := writeFixtureAsset(t, base, asset, goodContent)
	goodHash := sha256Hex(t, assetPath)
	lookup := func(o, a string) string {
		if o == osTok && a == archTok {
			return goodHash
		}
		return ""
	}

	cacheHome := t.TempDir()
	cacheDir := filepath.Join(cacheHome, "omakase", "lefthook", lefthookVersion)
	cacheBin := writeFixtureAsset(t, cacheDir, "lefthook", "corrupt-stale-bytes")
	if err := os.Chmod(cacheBin, 0o755); err != nil {
		t.Fatal(err)
	}

	t.Setenv("XDG_CACHE_HOME", cacheHome)
	t.Setenv("OMAKASE_LEFTHOOK_BASE_URL", "file://"+base)

	var buf bytes.Buffer
	path, ok := fetch(&buf, runtime.GOOS, runtime.GOARCH, lookup)
	if !ok {
		t.Fatalf("fetch failed to re-fetch past a corrupt cache: stderr=%q", buf.String())
	}
	if path != cacheBin {
		t.Errorf("fetch returned %q, want %q", path, cacheBin)
	}
	got, err := os.ReadFile(cacheBin)
	if err != nil || string(got) != goodContent {
		t.Errorf("re-fetched content = %q, %v, want %q", got, err, goodContent)
	}
}

// ---------------------------------------------------------------- resolve tiers

// isolateEnv resets every environment input Resolve/Fetch reads (LEFTHOOK_BIN,
// PATH, HOME, XDG_CACHE_HOME, OMAKASE_LEFTHOOK_BASE_URL) to values the test
// controls, so no tier accidentally resolves against whatever happens to be
// installed on the machine running the suite.
func isolateEnv(t *testing.T) {
	t.Helper()
	t.Setenv("LEFTHOOK_BIN", "")
	t.Setenv("PATH", t.TempDir()) // an empty dir: no `lefthook` reachable via PATH
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
	t.Setenv("OMAKASE_LEFTHOOK_BASE_URL", "")
}

func TestResolveForInitLefthookBinSplitsFields(t *testing.T) {
	isolateEnv(t)
	t.Setenv("LEFTHOOK_BIN", "node ./scripts/lefthook.js")
	inv, ok := ResolveForInit(t.TempDir(), io.Discard)
	if !ok {
		t.Fatal("ResolveForInit failed")
	}
	want := []string{"node", "./scripts/lefthook.js"}
	if !reflect.DeepEqual(inv, want) {
		t.Errorf("ResolveForInit = %v, want %v", inv, want)
	}
}

func TestResolveForRemoveLefthookBinSingleToken(t *testing.T) {
	isolateEnv(t)
	t.Setenv("LEFTHOOK_BIN", "node ./scripts/lefthook.js")
	inv, ok := ResolveForRemove(t.TempDir())
	if !ok {
		t.Fatal("ResolveForRemove failed")
	}
	want := []string{"node ./scripts/lefthook.js"}
	if !reflect.DeepEqual(inv, want) {
		t.Errorf("ResolveForRemove = %v, want %v (ONE token: remove uses \"$LEFTHOOK\" quoted)", inv, want)
	}
}

func TestResolveForRemoveWhitespaceLefthookBinResolvesAsOneToken(t *testing.T) {
	isolateEnv(t)
	t.Setenv("LEFTHOOK_BIN", "   ")
	inv, ok := ResolveForRemove(t.TempDir())
	if !ok || !reflect.DeepEqual(inv, []string{"   "}) {
		t.Errorf("ResolveForRemove(whitespace LEFTHOOK_BIN) = (%v,%v), want ([\"   \"],true)", inv, ok)
	}
}

func TestResolveForInitEmptyLefthookBinWhitespace(t *testing.T) {
	isolateEnv(t)
	t.Setenv("LEFTHOOK_BIN", "   ")
	inv, ok := ResolveForInit(t.TempDir(), io.Discard)
	if ok || inv != nil {
		t.Errorf("ResolveForInit(whitespace-only LEFTHOOK_BIN) = (%v,%v), want (nil,false) -- unquoted word-split leaves nothing", inv, ok)
	}
}

func TestResolveForInitPathTierReturnsBareToken(t *testing.T) {
	isolateEnv(t)
	dir := t.TempDir()
	stub := filepath.Join(dir, "lefthook")
	if err := os.WriteFile(stub, []byte("#!/bin/sh\necho stub\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	inv, ok := ResolveForInit(t.TempDir(), io.Discard)
	if !ok {
		t.Fatal("ResolveForInit failed")
	}
	want := []string{"lefthook"}
	if !reflect.DeepEqual(inv, want) {
		t.Errorf("ResolveForInit = %v, want %v (bare token, not the resolved absolute path)", inv, want)
	}
}

func TestResolveNodeModulesTier(t *testing.T) {
	isolateEnv(t)
	root := t.TempDir()
	binDir := filepath.Join(root, "node_modules", ".bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cand := filepath.Join(binDir, "lefthook")
	if err := os.WriteFile(cand, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	inv, ok := ResolveForInit(root, io.Discard)
	if !ok || !reflect.DeepEqual(inv, []string{cand}) {
		t.Errorf("ResolveForInit = (%v,%v), want ([%s],true)", inv, ok, cand)
	}
	inv2, ok2 := ResolveForRemove(root)
	if !ok2 || !reflect.DeepEqual(inv2, []string{cand}) {
		t.Errorf("ResolveForRemove = (%v,%v), want ([%s],true)", inv2, ok2, cand)
	}
}

func TestResolveNodeModulesTierNotExecutableSkipped(t *testing.T) {
	isolateEnv(t)
	root := t.TempDir()
	binDir := filepath.Join(root, "node_modules", ".bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cand := filepath.Join(binDir, "lefthook")
	if err := os.WriteFile(cand, []byte("not executable"), 0o644); err != nil {
		t.Fatal(err)
	}

	if inv, ok := ResolveForRemove(root); ok {
		t.Errorf("ResolveForRemove resolved a non-executable node_modules candidate: %v", inv)
	}
}

func TestResolveCacheTierNoFetchNeeded(t *testing.T) {
	isolateEnv(t)
	home := os.Getenv("HOME")
	cacheDir := filepath.Join(home, ".cache", "omakase", "lefthook", lefthookVersion)
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cacheBin := filepath.Join(cacheDir, "lefthook")
	if err := os.WriteFile(cacheBin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	// A base URL to nowhere: if either resolver fell through to a fetch
	// instead of reusing the cache, this would fail.
	t.Setenv("OMAKASE_LEFTHOOK_BASE_URL", filepath.Join(t.TempDir(), "nowhere"))

	root := t.TempDir()
	inv, ok := ResolveForInit(root, io.Discard)
	if !ok || !reflect.DeepEqual(inv, []string{cacheBin}) {
		t.Errorf("ResolveForInit = (%v,%v), want ([%s],true)", inv, ok, cacheBin)
	}
	inv2, ok2 := ResolveForRemove(root)
	if !ok2 || !reflect.DeepEqual(inv2, []string{cacheBin}) {
		t.Errorf("ResolveForRemove = (%v,%v), want ([%s],true)", inv2, ok2, cacheBin)
	}
}

func TestResolveForRemoveNeverFetchesNothingResolvesSilent(t *testing.T) {
	isolateEnv(t)
	root := t.TempDir()

	// Capture the REAL process stderr too, guarding against a future
	// regression writing directly to os.Stderr instead of staying silent:
	// ResolveForRemove's signature takes no io.Writer at all -- remove.sh's
	// resolve_lefthook call site has no lefthook_install_guidance on
	// failure, unlike init.sh's.
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	orig := os.Stderr
	os.Stderr = w
	inv, ok := ResolveForRemove(root)
	os.Stderr = orig
	w.Close()
	var buf bytes.Buffer
	buf.ReadFrom(r)

	if ok || inv != nil {
		t.Fatalf("ResolveForRemove = (%v,%v), want (nil,false)", inv, ok)
	}
	if buf.Len() != 0 {
		t.Errorf("ResolveForRemove wrote to stderr: %q", buf.String())
	}
}

func TestResolveForInitFetchFailureReachesTier5(t *testing.T) {
	isolateEnv(t)
	if _, _, ok := platformTokenFor(runtime.GOOS, runtime.GOARCH); !ok {
		t.Skipf("host platform %s/%s unsupported by the fetcher", runtime.GOOS, runtime.GOARCH)
	}
	// A base URL that resolves nothing real -- fetch will attempt (and fail)
	// a download rather than silently giving up, proving allowFetch=true
	// actually reaches tier 5.
	t.Setenv("OMAKASE_LEFTHOOK_BASE_URL", filepath.Join(t.TempDir(), "nowhere"))

	var buf bytes.Buffer
	inv, ok := ResolveForInit(t.TempDir(), &buf)
	if ok || inv != nil {
		t.Fatalf("ResolveForInit = (%v,%v), want (nil,false)", inv, ok)
	}
	if !strings.Contains(buf.String(), "omakase: could not download lefthook from") {
		t.Errorf("ResolveForInit stderr = %q, want it to contain the download-failure line", buf.String())
	}
}
