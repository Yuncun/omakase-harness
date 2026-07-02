package overlay

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Yuncun/omakase-harness/internal/state"
)

// These are the --source integration + unit tests (Task 4). Discipline per
// scenario is noted in the test doc-comment: "bash-format" = the byte structure
// was pinned against a live `bash bin/init.sh --source ...` run in a twin
// fixture (see below) and the path-bearing lines are CONSTRUCTED from known
// inputs (XDG_CACHE_HOME + the computed slug), the same way init_test.go builds
// path expectations; "broken-variant" = a deliberately malformed source drives
// the fail-closed arm; "red-first" = the behavior is asserted directly.
//
// The bash-format reference run (stdout structure, recommends placement,
// placed.tsv column 3 = the source string on EVERY row, $OMK/source, and the
// slug = <sanitized-basename>-<first8 sha256>) was captured with:
//
//	SRC=<a local source repo with name/version/recommends + a payload delta>
//	HOME=$fake XDG_CACHE_HOME=$cache LEFTHOOK_BIN=$stub \
//	  bash bin/init.sh --source "$SRC"
//
// which produced:
//	omakase: source '<SRC>' (name: demo, version: 1.2.3) cached at <CACHE>/omakase/sources/demo-src-1903dc30
//	... placement summary ...
//	omakase: see the whole harness any time with  omakase status
//	omakase: this harness recommends — install the widget plugin
//	omakase: to customize, fork the harness source ...
// with placed.tsv column 3 = <SRC> on every row and $OMK/source = "<SRC>\n".
// (The base tree in that run was the real payload/, so its file COUNT/order is
// find-order; the Go tests use a controlled base via basePayloadOverride and
// assert lexical WalkDir order — the GC6-sanctioned divergence.)

// ---------------------------------------------------------------- helpers

// srcTestEnv isolates a --source run from the real machine: XDG_CACHE_HOME + HOME
// point at fresh temp dirs (so no cache ever lands in ~/.cache), and
// OMAKASE_PAYLOAD is neutralized (the source arm must not read it; a stray
// ambient value would skip a remembered-source read).
func srcTestEnv(t *testing.T) {
	t.Helper()
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	t.Setenv("OMAKASE_PAYLOAD", "")
}

// useBasePayloadDir creates the base-harness payload fixture and points the base
// resolver (bin/init.sh:181's $SCRIPT_DIR/../payload) at it for this test,
// restoring the seam on cleanup.
func useBasePayloadDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	prev := basePayloadOverride
	basePayloadOverride = dir
	t.Cleanup(func() { basePayloadOverride = prev })
	return dir
}

// newSourceRepo makes an empty committed-config git repo to build a SOURCE in,
// returning its absolute path (as expandSource would absolutize it).
func newSourceRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	runGitT(t, dir, "init", "-q")
	runGitT(t, dir, "config", "user.email", "s@s")
	runGitT(t, dir, "config", "user.name", "s")
	runGitT(t, dir, "config", "commit.gpgsign", "false")
	return dir
}

func commitAll(t *testing.T, dir, msg string) {
	t.Helper()
	runGitT(t, dir, "add", "-A")
	runGitT(t, dir, "commit", "-q", "-m", msg)
}

// ---------------------------------------------------------------- basic merge

// TestSourceFlagBasicMerge (bash-format): a --source flag clones a local source,
// merges the base payload UNDER the source delta, places the merged set, records
// the source string in placed.tsv column 3 and $OMK/source, and surfaces the
// manifest's recommends line — all byte-exact. Base ships one file; the source
// delta ships two; the merged placement is lexical (GC6).
func TestSourceFlagBasicMerge(t *testing.T) {
	dir, repo := initRepo(t)
	srcTestEnv(t)
	stubLefthook(t)
	base := useBasePayloadDir(t)
	writeFile(t, filepath.Join(base, ".omakase", "bin", "base.sh"), "base\n")

	src := newSourceRepo(t)
	writeFile(t, filepath.Join(src, "omakase.manifest"), "name: demo\nversion: 1.2.3\nrecommends: install the widget plugin\n")
	writeFile(t, filepath.Join(src, "payload", ".omakase", "gates", "src.sh"), "src gate\n")
	writeFile(t, filepath.Join(src, "payload", ".claude", "rules", "r.md"), "rule\n")
	commitAll(t, src, "src")

	var stdout, stderr strings.Builder
	if code := RunInit([]string{"--source", src}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%q", code, stderr.String())
	}

	cache := sourceCacheDir(src)
	// merged lexical order: .claude/rules/r.md, .omakase/bin/base.sh, .omakase/gates/src.sh
	wantOut := "omakase: source '" + src + "' (name: demo, version: 1.2.3) cached at " + cache + "\n" +
		"omakase: placed 3 file(s), overwrote 0 to match payload, skipped 0 committed path(s).\n" +
		"  + .claude/rules/r.md\n" +
		"  + .omakase/bin/base.sh\n" +
		"  + .omakase/gates/src.sh\n" +
		"omakase: ignores -> .git/info/exclude; hooks installed; new worktrees auto-install the harness. Nothing to commit.\n" +
		"omakase: see the whole harness any time with  omakase status\n" +
		"omakase: this harness recommends — install the widget plugin\n" +
		"omakase: to customize, fork the harness source (clone -> edit -> publish) and\n" +
		"         init from your copy; do not edit injected files in place (overwritten on re-init).\n"
	eq(t, "stdout", stdout.String(), wantOut)
	eq(t, "stderr", stderr.String(), "")

	// Both trees placed; base machinery layered in under the source delta.
	eq(t, "base file", readFileT(t, filepath.Join(dir, ".omakase", "bin", "base.sh")), "base\n")
	eq(t, "delta gate", readFileT(t, filepath.Join(dir, ".omakase", "gates", "src.sh")), "src gate\n")
	eq(t, "delta rule", readFileT(t, filepath.Join(dir, ".claude", "rules", "r.md")), "rule\n")

	// placed.tsv column 3 = the source string on EVERY row (base + delta).
	for _, row := range state.ReadPlaced(filepath.Join(repo.OMK, "placed.tsv")) {
		if row.Src != src {
			t.Errorf("placed.tsv col3 = %q for %q, want %q", row.Src, row.Rel, src)
		}
	}
	// remembered source written verbatim + newline.
	eq(t, "OMK/source", readFileT(t, filepath.Join(repo.OMK, "source")), src+"\n")
	// cache slug carries the source basename.
	if !strings.Contains(filepath.Base(cache), "src-") && !strings.HasPrefix(filepath.Base(cache), filepath.Base(src)) {
		t.Errorf("cache slug %q does not carry the source basename %q", filepath.Base(cache), filepath.Base(src))
	}
	if out := gitStdout(dir, "status", "--porcelain"); out != "" {
		t.Errorf("git status not clean: %q", out)
	}
}

// TestSourceNoRecommendsNoLine (red-first): a manifest without recommends emits
// NO "this harness recommends" line (the summary stays the plain tail).
func TestSourceNoRecommendsNoLine(t *testing.T) {
	_, repo := initRepo(t)
	srcTestEnv(t)
	stubLefthook(t)
	useBasePayloadDir(t) // empty base

	src := newSourceRepo(t)
	writeFile(t, filepath.Join(src, "omakase.manifest"), "name: quiet\n")
	writeFile(t, filepath.Join(src, "payload", ".omakase", "gates", "g.sh"), "g\n")
	commitAll(t, src, "src")

	var stdout, stderr strings.Builder
	if code := RunInit([]string{"--source", src}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%q", code, stderr.String())
	}
	if strings.Contains(stdout.String(), "this harness recommends") {
		t.Errorf("recommends line printed with no recommends: manifest:\n%s", stdout.String())
	}
	// "cached at" line carries name only (no ", version:").
	if !strings.Contains(stdout.String(), "omakase: source '"+src+"' (name: quiet) cached at "+sourceCacheDir(src)+"\n") {
		t.Errorf("cached-at line wrong:\n%s", stdout.String())
	}
	_ = repo
}

// ---------------------------------------------------------------- manifest arms

// assertSourceRefusal runs a --source install expected to fail closed: exit 1,
// exact stderr, and NOTHING placed (no .omakase, clean git status, no exclude
// block).
func assertSourceRefusal(t *testing.T, argvSource, wantErr string) {
	t.Helper()
	dir, repo := initRepo(t)
	srcTestEnv(t)
	base := useBasePayloadDir(t)
	writeFile(t, filepath.Join(base, ".omakase", "bin", "base.sh"), "base\n") // a real base, to prove it is never reached

	var stdout, stderr strings.Builder
	code := RunInit([]string{"--source", argvSource}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("exit = %d, want 1; stderr=%q", code, stderr.String())
	}
	eq(t, "refusal stderr", stderr.String(), wantErr)
	eq(t, "refusal stdout", stdout.String(), "")
	if _, err := os.Stat(filepath.Join(dir, ".omakase")); err == nil {
		t.Error("placed files despite a source refusal")
	}
	if _, err := os.Stat(filepath.Join(repo.OMK, "source")); err == nil {
		t.Error("wrote $OMK/source despite a refusal")
	}
	if out := gitStdout(dir, "status", "--porcelain"); out != "" {
		t.Errorf("refusal left changes: %q", out)
	}
}

// TestSourceMissingManifest (broken-variant): a payload-only source (no
// omakase.manifest) is refused, byte-exact.
func TestSourceMissingManifest(t *testing.T) {
	src := newSourceRepo(t)
	writeFile(t, filepath.Join(src, "payload", "rule.md"), "a rule\n")
	commitAll(t, src, "no-manifest")
	assertSourceRefusal(t, src,
		"omakase: source '"+src+"' has no omakase.manifest at its root — not an omakase source\n")
}

// TestSourceMissingName (broken-variant): a manifest with no name: line is refused.
func TestSourceMissingName(t *testing.T) {
	src := newSourceRepo(t)
	writeFile(t, filepath.Join(src, "omakase.manifest"), "version: 1.0\nrecommends: x\n")
	writeFile(t, filepath.Join(src, "payload", "rule.md"), "a rule\n")
	commitAll(t, src, "no-name")
	assertSourceRefusal(t, src,
		"omakase: source '"+src+"' manifest is missing the required 'name:' line\n")
}

// TestSourceEmptyPayload (broken-variant): a manifest but no non-empty payload/
// tree is refused. (git cannot track an empty dir, so the payload/ is absent in
// the clone — the same `[ -d payload ] && [ -n "$(ls -A payload)" ]` failure.)
func TestSourceEmptyPayload(t *testing.T) {
	src := newSourceRepo(t)
	writeFile(t, filepath.Join(src, "omakase.manifest"), "name: empty\n")
	commitAll(t, src, "no-payload")
	assertSourceRefusal(t, src,
		"omakase: source '"+src+"' has no non-empty payload/ tree — nothing to inject\n")
}

// TestSourceCRLFManifest (broken-variant): a CRLF manifest with trailing spaces
// yields name/version stripped of the ^M and surrounding whitespace — the
// "cached at" line carries the clean values, no ^M leaks downstream.
func TestSourceCRLFManifest(t *testing.T) {
	_, repo := initRepo(t)
	srcTestEnv(t)
	stubLefthook(t)
	useBasePayloadDir(t)

	src := newSourceRepo(t)
	// CRLF line endings + trailing spaces on name and version.
	writeFile(t, filepath.Join(src, "omakase.manifest"), "name:  crlf-harness  \r\nversion: 2.3 \r\n")
	writeFile(t, filepath.Join(src, "payload", ".omakase", "gates", "g.sh"), "g\n")
	commitAll(t, src, "crlf")

	var stdout, stderr strings.Builder
	if code := RunInit([]string{"--source", src}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%q", code, stderr.String())
	}
	wantCached := "omakase: source '" + src + "' (name: crlf-harness, version: 2.3) cached at " + sourceCacheDir(src) + "\n"
	if !strings.HasPrefix(stdout.String(), wantCached) {
		t.Errorf("cached-at line did not strip CR/whitespace:\n got: %q\nwant prefix: %q", stdout.String(), wantCached)
	}
	if strings.Contains(stdout.String(), "\r") {
		t.Errorf("a ^M leaked into stdout:\n%q", stdout.String())
	}
	_ = repo
}

// ---------------------------------------------------------------- ref pin

// TestSourceRefPinBranch (red-first): --source repo#branch checks the cache out
// to that branch; the branch-specific delta is installed, and the pinned label
// round-trips into placed.tsv col3 + $OMK/source.
func TestSourceRefPinBranch(t *testing.T) {
	dir, repo := initRepo(t)
	srcTestEnv(t)
	stubLefthook(t)
	useBasePayloadDir(t)

	src := newSourceRepo(t)
	writeFile(t, filepath.Join(src, "omakase.manifest"), "name: pinned\n")
	writeFile(t, filepath.Join(src, "payload", ".omakase", "gates", "g.sh"), "DEFAULT\n")
	commitAll(t, src, "default")
	runGitT(t, src, "branch", "-M", "main")
	runGitT(t, src, "checkout", "-q", "-b", "feature")
	writeFile(t, filepath.Join(src, "payload", ".omakase", "gates", "g.sh"), "FEATURE\n")
	commitAll(t, src, "feature")
	runGitT(t, src, "checkout", "-q", "main") // source HEAD back on the default (DEFAULT content)

	var stdout, stderr strings.Builder
	if code := RunInit([]string{"--source", src + "#feature"}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%q", code, stderr.String())
	}
	eq(t, "branch-pinned content", readFileT(t, filepath.Join(dir, ".omakase", "gates", "g.sh")), "FEATURE\n")
	eq(t, "remembered label pinned", readFileT(t, filepath.Join(repo.OMK, "source")), src+"#feature\n")
	for _, row := range state.ReadPlaced(filepath.Join(repo.OMK, "placed.tsv")) {
		if row.Src != src+"#feature" {
			t.Errorf("placed.tsv col3 = %q, want %q", row.Src, src+"#feature")
		}
	}
}

// TestSourceRefPinTag (red-first): a tag ref resolves via fetch --tags; the
// tagged (older) content installs, not HEAD.
func TestSourceRefPinTag(t *testing.T) {
	dir, _ := initRepo(t)
	srcTestEnv(t)
	stubLefthook(t)
	useBasePayloadDir(t)

	src := newSourceRepo(t)
	writeFile(t, filepath.Join(src, "omakase.manifest"), "name: tagged\n")
	writeFile(t, filepath.Join(src, "payload", ".omakase", "gates", "g.sh"), "TAGGED\n")
	commitAll(t, src, "v1")
	runGitT(t, src, "tag", "v1")
	writeFile(t, filepath.Join(src, "payload", ".omakase", "gates", "g.sh"), "NEWER\n")
	commitAll(t, src, "newer") // HEAD moves past the tag

	var stdout, stderr strings.Builder
	if code := RunInit([]string{"--source", src + "#v1"}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%q", code, stderr.String())
	}
	eq(t, "tag-pinned content", readFileT(t, filepath.Join(dir, ".omakase", "gates", "g.sh")), "TAGGED\n")
}

// TestSourceRefNotFound (broken-variant): an unknown ref is refused, byte-exact,
// with nothing placed.
func TestSourceRefNotFound(t *testing.T) {
	dir, repo := initRepo(t)
	srcTestEnv(t)
	useBasePayloadDir(t)

	src := newSourceRepo(t)
	writeFile(t, filepath.Join(src, "omakase.manifest"), "name: x\n")
	writeFile(t, filepath.Join(src, "payload", "g.sh"), "g\n")
	commitAll(t, src, "src")

	var stdout, stderr strings.Builder
	code := RunInit([]string{"--source", src + "#nope"}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("exit = %d, want 1; stderr=%q", code, stderr.String())
	}
	eq(t, "ref-not-found stderr", stderr.String(),
		"omakase: source '"+src+"' has no ref 'nope' (no such branch or tag)\n")
	if _, err := os.Stat(filepath.Join(dir, ".omakase")); err == nil {
		t.Error("placed files despite a bad ref")
	}
	if _, err := os.Stat(filepath.Join(repo.OMK, "source")); err == nil {
		t.Error("wrote $OMK/source despite a bad ref")
	}
}

// ---------------------------------------------------------------- round-trip

// TestRememberedSourceRoundTrip (red-first): the INIT-twice contract. Init 1
// pins a TAG and writes $OMK/source; a BARE init 2 (no flag, OMAKASE_PAYLOAD
// unset) reads it back, re-splits the pinned ref, refreshes the cache, and
// re-installs the SAME pinned content. A tag is used because its ref is
// immutable: the refresh's hard-reset lands on a DETACHED HEAD, which the tag
// re-checkout overrides, so the pinned content survives (verified against
// bin/init.sh; a BRANCH pin does NOT survive — see TestBranchPinNotPreserved).
func TestRememberedSourceRoundTrip(t *testing.T) {
	dir, repo := initRepo(t)
	srcTestEnv(t)
	stubLefthook(t)
	useBasePayloadDir(t)

	src := newSourceRepo(t)
	writeFile(t, filepath.Join(src, "omakase.manifest"), "name: rt\n")
	writeFile(t, filepath.Join(src, "payload", ".omakase", "gates", "g.sh"), "PINNED\n")
	commitAll(t, src, "tagged")
	runGitT(t, src, "branch", "-M", "main")
	runGitT(t, src, "tag", "v1") // v1 = PINNED (immutable)
	writeFile(t, filepath.Join(src, "payload", ".omakase", "gates", "g.sh"), "NEWER\n")
	commitAll(t, src, "newer") // the default branch advances past the tag

	var o1, e1 strings.Builder
	if code := RunInit([]string{"--source", src + "#v1"}, &o1, &e1); code != 0 {
		t.Fatalf("init 1 exit = %d; stderr=%q", code, e1.String())
	}
	eq(t, "init1 content", readFileT(t, filepath.Join(dir, ".omakase", "gates", "g.sh")), "PINNED\n")

	// Bare re-run: no argv, no OMAKASE_PAYLOAD — the remembered #v1 must win.
	var o2, e2 strings.Builder
	if code := RunInit(nil, &o2, &e2); code != 0 {
		t.Fatalf("bare re-run exit = %d; stderr=%q", code, e2.String())
	}
	eq(t, "roundtrip content", readFileT(t, filepath.Join(dir, ".omakase", "gates", "g.sh")), "PINNED\n")
	eq(t, "roundtrip remembered", readFileT(t, filepath.Join(repo.OMK, "source")), src+"#v1\n")
	// the bare run re-emitted the pinned "cached at" line (proves it re-fetched the remembered source).
	if !strings.Contains(o2.String(), "omakase: source '"+src+"' (name: rt) cached at ") {
		t.Errorf("bare re-run did not re-fetch the remembered source:\n%s", o2.String())
	}
}

// TestBranchPinNotPreserved (parity): a BRANCH pin does NOT survive a bare
// re-run — the refresh hard-resets the checked-out local branch to the remote
// default, and re-checking-out that same branch yields the DEFAULT content. This
// is a fetch_source quirk (bin/init.sh:117-138) the Go port reproduces exactly
// (verified against bin/init.sh: init1 => FEATURE, bare re-run => MAIN).
func TestBranchPinNotPreserved(t *testing.T) {
	dir, _ := initRepo(t)
	srcTestEnv(t)
	stubLefthook(t)
	useBasePayloadDir(t)

	src := newSourceRepo(t)
	writeFile(t, filepath.Join(src, "omakase.manifest"), "name: br\n")
	writeFile(t, filepath.Join(src, "payload", ".omakase", "gates", "g.sh"), "MAIN\n")
	commitAll(t, src, "main")
	runGitT(t, src, "branch", "-M", "main")
	runGitT(t, src, "checkout", "-q", "-b", "feature")
	writeFile(t, filepath.Join(src, "payload", ".omakase", "gates", "g.sh"), "FEATURE\n")
	commitAll(t, src, "feature")
	runGitT(t, src, "checkout", "-q", "main")

	var o1, e1 strings.Builder
	if code := RunInit([]string{"--source", src + "#feature"}, &o1, &e1); code != 0 {
		t.Fatalf("init 1 exit = %d; stderr=%q", code, e1.String())
	}
	eq(t, "init1 feature", readFileT(t, filepath.Join(dir, ".omakase", "gates", "g.sh")), "FEATURE\n")

	var o2, e2 strings.Builder
	if code := RunInit(nil, &o2, &e2); code != 0 {
		t.Fatalf("bare re-run exit = %d; stderr=%q", code, e2.String())
	}
	eq(t, "bare re-run clobbered branch to default", readFileT(t, filepath.Join(dir, ".omakase", "gates", "g.sh")), "MAIN\n")
}

// ---------------------------------------------------------------- cache refresh

// TestSourceCacheRefreshPicksUpNewCommit (red-first): a second --source install
// against a source whose default branch advanced refreshes the cache (fetch +
// hard reset) and places the new tip.
func TestSourceCacheRefreshPicksUpNewCommit(t *testing.T) {
	dir, _ := initRepo(t)
	srcTestEnv(t)
	stubLefthook(t)
	useBasePayloadDir(t)

	src := newSourceRepo(t)
	writeFile(t, filepath.Join(src, "omakase.manifest"), "name: refresh\n")
	writeFile(t, filepath.Join(src, "payload", ".omakase", "gates", "g.sh"), "V1\n")
	commitAll(t, src, "v1")

	var o1, e1 strings.Builder
	if code := RunInit([]string{"--source", src}, &o1, &e1); code != 0 {
		t.Fatalf("init 1 exit = %d; stderr=%q", code, e1.String())
	}
	eq(t, "v1 content", readFileT(t, filepath.Join(dir, ".omakase", "gates", "g.sh")), "V1\n")

	// Source advances on its default branch.
	writeFile(t, filepath.Join(src, "payload", ".omakase", "gates", "g.sh"), "V2\n")
	commitAll(t, src, "v2")

	var o2, e2 strings.Builder
	if code := RunInit([]string{"--source", src}, &o2, &e2); code != 0 {
		t.Fatalf("init 2 exit = %d; stderr=%q", code, e2.String())
	}
	eq(t, "refreshed v2 content", readFileT(t, filepath.Join(dir, ".omakase", "gates", "g.sh")), "V2\n")
	// The placed gate changed V1 -> V2, so the engine reports the overwrite on
	// stderr (the injected copy is brought back to match the refreshed payload).
	if !strings.Contains(e2.String(), "omakase: overwrote .omakase/gates/g.sh to match payload") {
		t.Errorf("refresh did not report the overwrite:\n%s", e2.String())
	}
}

// TestSourceCorruptCacheReclone (broken-variant): a corrupt cache (garbage
// .git/HEAD, mirroring sources.test.sh S3d) is discarded with the byte-exact
// stale notice and re-cloned; the fresh clone delivers the latest payload.
func TestSourceCorruptCacheReclone(t *testing.T) {
	dir, _ := initRepo(t)
	srcTestEnv(t)
	stubLefthook(t)
	useBasePayloadDir(t)

	src := newSourceRepo(t)
	writeFile(t, filepath.Join(src, "omakase.manifest"), "name: recover\n")
	writeFile(t, filepath.Join(src, "payload", ".omakase", "gates", "g.sh"), "V1\n")
	commitAll(t, src, "v1")

	var o1, e1 strings.Builder
	if code := RunInit([]string{"--source", src}, &o1, &e1); code != 0 {
		t.Fatalf("init 1 exit = %d; stderr=%q", code, e1.String())
	}
	cache := sourceCacheDir(src)

	// Advance the source, then corrupt the cache's HEAD.
	writeFile(t, filepath.Join(src, "payload", ".omakase", "gates", "g.sh"), "V6\n")
	commitAll(t, src, "v6")
	writeFile(t, filepath.Join(cache, ".git", "HEAD"), "garbage\n")

	var o2, e2 strings.Builder
	if code := RunInit([]string{"--source", src}, &o2, &e2); code != 0 {
		t.Fatalf("recovery exit = %d; stderr=%q", code, e2.String())
	}
	wantNotice := "omakase: source cache at " + cache + " is stale or corrupt — discarding and re-cloning (a cache is disposable)\n"
	if !strings.Contains(e2.String(), wantNotice) {
		t.Errorf("stale notice missing/mismatched:\n got: %q\nwant substr: %q", e2.String(), wantNotice)
	}
	eq(t, "recovered content", readFileT(t, filepath.Join(dir, ".omakase", "gates", "g.sh")), "V6\n")
	// cache healthy again.
	if !isDir(filepath.Join(cache, ".git")) {
		t.Error("cache/.git missing after re-clone")
	}
}

// ---------------------------------------------------------------- merge semantics

// TestMergeSourceWinsOverlap (red-first): base and source both ship a file at
// the same path; the SOURCE wins (replace semantics, rm-before-copy).
func TestMergeSourceWinsOverlap(t *testing.T) {
	dir, _ := initRepo(t)
	srcTestEnv(t)
	stubLefthook(t)
	base := useBasePayloadDir(t)
	writeFile(t, filepath.Join(base, ".omakase", "gates", "example.sh"), "BASE\n")

	src := newSourceRepo(t)
	writeFile(t, filepath.Join(src, "omakase.manifest"), "name: overlap\n")
	writeFile(t, filepath.Join(src, "payload", ".omakase", "gates", "example.sh"), "SOURCE\n")
	commitAll(t, src, "src")

	var stdout, stderr strings.Builder
	if code := RunInit([]string{"--source", src}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit = %d; stderr=%q", code, stderr.String())
	}
	eq(t, "source wins on overlap", readFileT(t, filepath.Join(dir, ".omakase", "gates", "example.sh")), "SOURCE\n")
}

// TestMergeSymlinkOverFile (red-first): base ships a regular file where the
// source ships a symlink — the merged path is the SOURCE symlink (cp -P carried).
func TestMergeSymlinkOverFile(t *testing.T) {
	dir, _ := initRepo(t)
	srcTestEnv(t)
	stubLefthook(t)
	base := useBasePayloadDir(t)
	writeFile(t, filepath.Join(base, "CLAUDE.md"), "base regular doc\n")

	src := newSourceRepo(t)
	writeFile(t, filepath.Join(src, "omakase.manifest"), "name: symlink\n")
	writeFile(t, filepath.Join(src, "payload", "AGENTS.md"), "real doctrine\n")
	if err := os.Symlink("AGENTS.md", filepath.Join(src, "payload", "CLAUDE.md")); err != nil {
		t.Fatal(err)
	}
	commitAll(t, src, "src")

	var stdout, stderr strings.Builder
	if code := RunInit([]string{"--source", src}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit = %d; stderr=%q", code, stderr.String())
	}
	target, err := os.Readlink(filepath.Join(dir, "CLAUDE.md"))
	if err != nil || target != "AGENTS.md" {
		t.Errorf("CLAUDE.md is not the source symlink -> AGENTS.md: target=%q err=%v", target, err)
	}
}

// TestMergeFileOverSymlink (red-first): base ships a symlink where the source
// ships a regular file — the merged path is the SOURCE regular file, and the
// base symlink's TARGET is NOT written through (proving the rm-before-copy).
func TestMergeFileOverSymlink(t *testing.T) {
	dir, _ := initRepo(t)
	srcTestEnv(t)
	stubLefthook(t)
	base := useBasePayloadDir(t)
	writeFile(t, filepath.Join(base, "target.md"), "base target untouched\n")
	if err := os.Symlink("target.md", filepath.Join(base, "link.md")); err != nil {
		t.Fatal(err)
	}

	src := newSourceRepo(t)
	writeFile(t, filepath.Join(src, "omakase.manifest"), "name: fileoversym\n")
	writeFile(t, filepath.Join(src, "payload", "link.md"), "source regular content\n")
	commitAll(t, src, "src")

	var stdout, stderr strings.Builder
	if code := RunInit([]string{"--source", src}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit = %d; stderr=%q", code, stderr.String())
	}
	// link.md is a regular file with the source content (NOT a symlink).
	if isSymlink(filepath.Join(dir, "link.md")) {
		t.Error("link.md stayed a symlink — the source regular file did not win")
	}
	eq(t, "link.md content", readFileT(t, filepath.Join(dir, "link.md")), "source regular content\n")
	// The base symlink's target was NOT clobbered through the link.
	eq(t, "base target untouched", readFileT(t, filepath.Join(dir, "target.md")), "base target untouched\n")
}

// TestMergeStagingCleaned (red-first): the merge staging dir under TMPDIR is
// removed on a successful run (v1's EXIT-trap cleanup, honoring TMPDIR).
func TestMergeStagingCleaned(t *testing.T) {
	initRepo(t)
	srcTestEnv(t)
	stubLefthook(t)
	useBasePayloadDir(t)
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)

	src := newSourceRepo(t)
	writeFile(t, filepath.Join(src, "omakase.manifest"), "name: staging\n")
	writeFile(t, filepath.Join(src, "payload", "g.sh"), "g\n")
	commitAll(t, src, "src")

	var stdout, stderr strings.Builder
	if code := RunInit([]string{"--source", src}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit = %d; stderr=%q", code, stderr.String())
	}
	entries, err := os.ReadDir(tmp)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "omakase-merge.") {
			t.Errorf("merge staging dir leaked in TMPDIR: %s", e.Name())
		}
	}
}

// TestSourceWiringGuardPostMerge (broken-variant): the fail-closed wiring guard
// runs on the MERGED payload — a source wiring a .omakase/*.sh that neither it
// nor the base ships is refused after the merge, with nothing placed (the
// merge->guard seam; mirrors sources.test.sh S7).
func TestSourceWiringGuardPostMerge(t *testing.T) {
	dir, repo := initRepo(t)
	srcTestEnv(t)
	useBasePayloadDir(t) // base ships no such script either

	src := newSourceRepo(t)
	writeFile(t, filepath.Join(src, "omakase.manifest"), "name: bad-wiring\n")
	writeFile(t, filepath.Join(src, "payload", "lefthook-local.yml"),
		"pre-commit:\n  jobs:\n    - name: ghost\n      run: bash .omakase/gates/this-script-does-not-exist.sh\n")
	commitAll(t, src, "src")

	var stdout, stderr strings.Builder
	code := RunInit([]string{"--source", src}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("exit = %d, want 1; stderr=%q", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "this-script-does-not-exist.sh") {
		t.Errorf("wiring refusal did not name the missing script:\n%s", stderr.String())
	}
	if _, err := os.Stat(filepath.Join(dir, ".omakase")); err == nil {
		t.Error("placed files despite the wiring refusal")
	}
	// The cached-at line DID print (fetch/validate precede the guard), but nothing
	// was placed and no source was remembered.
	if _, err := os.Stat(filepath.Join(repo.OMK, "source")); err == nil {
		t.Error("remembered a source despite the wiring refusal")
	}
}

// ---------------------------------------------------------------- clone failure

// TestSourceCloneFailure (broken-variant): a local path that exists but is not a
// git repo fails the clone; the byte-exact message names the source + cache, and
// nothing is placed. No network.
func TestSourceCloneFailure(t *testing.T) {
	dir, _ := initRepo(t)
	srcTestEnv(t)
	useBasePayloadDir(t)
	notRepo := t.TempDir() // exists, empty, not a git repo

	var stdout, stderr strings.Builder
	code := RunInit([]string{"--source", notRepo}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("exit = %d, want 1; stderr=%q", code, stderr.String())
	}
	wantMsg := "omakase: could not clone source '" + notRepo + "' into the cache (" + sourceCacheDir(notRepo) + ")\n"
	if !strings.Contains(stderr.String(), wantMsg) {
		t.Errorf("clone-failure message missing:\n got: %q\nwant substr: %q", stderr.String(), wantMsg)
	}
	if _, err := os.Stat(filepath.Join(dir, ".omakase")); err == nil {
		t.Error("placed files despite a clone failure")
	}
}

// ---------------------------------------------------------------- unit: slug / expand / manifest

// TestSourceSlugCollisionResistance (property): two sources sharing a basename
// but at different URLs get DIFFERENT cache slugs; one source always maps to one.
func TestSourceSlugCollisionResistance(t *testing.T) {
	a := sourceSlug("/x/harness")
	b := sourceSlug("/y/harness")
	if a == b {
		t.Errorf("same-basename different-path sources collided: both %q", a)
	}
	if !strings.HasPrefix(a, "harness-") || !strings.HasPrefix(b, "harness-") {
		t.Errorf("slugs do not carry the basename: %q %q", a, b)
	}
	if sourceSlug("/x/harness") != a {
		t.Error("slug not stable for the same source")
	}
	// URL vs shorthand-expanded URL are distinct strings -> distinct slugs.
	if sourceSlug("https://github.com/a/b") == sourceSlug("https://github.com/a/c") {
		t.Error("distinct GitHub URLs collided")
	}
}

// TestSanitizeBase (unit): the sed + tr basename sanitizer.
func TestSanitizeBase(t *testing.T) {
	cases := map[string]string{
		"https://github.com/you/harness.git": "harness",
		"https://github.com/you/harness":     "harness",
		"/path/to/My_Repo":                   "My_Repo",
		"git@github.com:you/harness.git":     "harness",
		"/path/to/weird name!":               "weird-name-",
		"foo.git/":                           "foo",
		"/":                                  "source",
		".git":                               "source",
		"":                                   "source",
	}
	for in, want := range cases {
		if got := sanitizeBase(in); got != want {
			t.Errorf("sanitizeBase(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestExpandSource (unit): shorthand -> GitHub URL, #ref split, URL/scp
// passthrough, and local-dir absolutize.
func TestExpandSource(t *testing.T) {
	type sr struct{ src, ref string }
	cases := map[string]sr{
		"you/harness":             {"https://github.com/you/harness", ""},
		"you/harness#v1":          {"https://github.com/you/harness", "v1"},
		"https://x.com/a/b":       {"https://x.com/a/b", ""},
		"https://x.com/a/b#dev":   {"https://x.com/a/b", "dev"},
		"git@github.com:a/b.git":  {"git@github.com:a/b.git", ""},
		"single-segment-no-slash": {"single-segment-no-slash", ""},
	}
	for in, want := range cases {
		gs, gr := expandSource(in)
		if gs != want.src || gr != want.ref {
			t.Errorf("expandSource(%q) = (%q,%q), want (%q,%q)", in, gs, gr, want.src, want.ref)
		}
	}
	// A local directory is absolutized; no ref is split from an existing path.
	d := t.TempDir()
	if gs, gr := expandSource(d); gs != d || gr != "" {
		t.Errorf("expandSource(localdir %q) = (%q,%q), want (%q,\"\")", d, gs, gr, d)
	}
}

// TestManifestField (unit): first-match value, CRLF + whitespace stripping,
// no-value and missing-key -> "".
func TestManifestField(t *testing.T) {
	m := []byte("name:  demo-harness  \r\nversion:1.0\r\nrecommends: a b c\n")
	eqField := func(key, want string) {
		if got := manifestField(m, key); got != want {
			t.Errorf("manifestField(%q) = %q, want %q", key, got, want)
		}
	}
	eqField("name", "demo-harness")
	eqField("version", "1.0")
	eqField("recommends", "a b c")
	eqField("missing", "")
	if got := manifestField([]byte("name: first\nname: second\n"), "name"); got != "first" {
		t.Errorf("first-match: got %q, want first", got)
	}
	if got := manifestField([]byte("name:\n"), "name"); got != "" {
		t.Errorf("empty value: got %q, want \"\"", got)
	}
}
