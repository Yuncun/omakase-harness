package overlay

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Yuncun/omakase-harness/internal/state"
)

// The --source integration and unit tests. Path-bearing expectations are
// constructed from known inputs (XDG_CACHE_HOME + the computed cache slug)
// rather than hardcoded temp paths. Placement asserts filepath.WalkDir's lexical
// order via a controlled base payload (basePayloadOverride).

// ---------------------------------------------------------------- helpers

// srcTestEnv isolates a --source run from the real machine: XDG_CACHE_HOME + HOME
// point at fresh temp dirs (so no cache ever lands in ~/.cache), and
// OMAKASE_PAYLOAD is neutralized (the source arm must not read it; a stray
// ambient value would skip a remembered-source read). OMAKASE_BASE_PAYLOAD is
// neutralized too, so a test that clears basePayloadOverride without setting it
// does not absorb an ambient value from the shell. Tests that need a base value
// re-Setenv it after this helper, so their value still wins.
func srcTestEnv(t *testing.T) {
	t.Helper()
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	t.Setenv("OMAKASE_PAYLOAD", "")
	t.Setenv("OMAKASE_BASE_PAYLOAD", "")
	// The fresh XDG_CACHE_HOME has no stable binary copy; init verifies the
	// dispatchers' exec target, so plant one to keep runs warning-free.
	plantStableBin(t)
}

// useBasePayloadDir creates the base-harness payload fixture and points the base
// resolver at it for this test, restoring basePayloadOverride on cleanup.
func useBasePayloadDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	prev := basePayloadOverride
	basePayloadOverride = dir
	t.Cleanup(func() { basePayloadOverride = prev })
	return dir
}

// clearBasePayloadOverride empties basePayloadOverride for the duration of a test
// (save/restore, like useBasePayloadDir), so defaultPayload falls through to the
// OMAKASE_BASE_PAYLOAD env tier the shims export — the override would otherwise
// short-circuit that tier.
func clearBasePayloadOverride(t *testing.T) {
	t.Helper()
	prev := basePayloadOverride
	basePayloadOverride = ""
	t.Cleanup(func() { basePayloadOverride = prev })
}

// newSourceRepo makes an empty committed-config git repo to build a source in,
// returning its absolute path (expandSource absolutizes the same way).
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

// TestSourceFlagBasicMerge: a --source flag clones a local source, merges the
// base payload under the source delta, places the merged set, records the source
// string in placed.tsv column 3 and $OMK/source, and surfaces the manifest's
// recommends line. Base ships one file; the source delta ships two; the merged
// placement is lexical.
func TestSourceFlagBasicMerge(t *testing.T) {
	dir, repo := initRepo(t)
	srcTestEnv(t)
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
		"omakase: ignores -> .git/info/exclude; new worktrees auto-install the harness. Nothing to commit.\n" +
		"omakase: see the whole harness any time with  omakase status\n" +
		"omakase: this harness recommends — install the widget plugin\n" +
		"omakase: to customize, fork the harness source (clone -> edit -> publish) and\n" +
		"         init from your copy; do not edit injected files in place (overwritten on re-init).\n" +
		uxStanzas() + verifiedLine
	eq(t, "stdout", stdout.String(), wantOut)
	eq(t, "stderr", stderr.String(), "")

	// Both trees placed; base machinery layered in under the source delta.
	eq(t, "base file", readFileT(t, filepath.Join(dir, ".omakase", "bin", "base.sh")), "base\n")
	eq(t, "delta gate", readFileT(t, filepath.Join(dir, ".omakase", "gates", "src.sh")), "src gate\n")
	eq(t, "delta rule", readFileT(t, filepath.Join(dir, ".claude", "rules", "r.md")), "rule\n")

	// placed.tsv column 3 = the source string on every row (base + delta).
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

// TestSourceNoRecommendsNoLine: a manifest without recommends emits no "this
// harness recommends" line (the summary stays the plain tail).
func TestSourceNoRecommendsNoLine(t *testing.T) {
	_, repo := initRepo(t)
	srcTestEnv(t)
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
// exact stderr, and nothing placed (no .omakase, clean git status, no exclude
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

// TestSourceMissingManifest: a payload-only source (no omakase.manifest) is
// refused.
func TestSourceMissingManifest(t *testing.T) {
	src := newSourceRepo(t)
	writeFile(t, filepath.Join(src, "payload", "rule.md"), "a rule\n")
	commitAll(t, src, "no-manifest")
	assertSourceRefusal(t, src,
		"omakase: source '"+src+"' has no omakase.manifest at its root — not an omakase source\n")
}

// TestSourceMissingName: a manifest with no name: line is refused.
func TestSourceMissingName(t *testing.T) {
	src := newSourceRepo(t)
	writeFile(t, filepath.Join(src, "omakase.manifest"), "version: 1.0\nrecommends: x\n")
	writeFile(t, filepath.Join(src, "payload", "rule.md"), "a rule\n")
	commitAll(t, src, "no-name")
	assertSourceRefusal(t, src,
		"omakase: source '"+src+"' manifest is missing the required 'name:' line\n")
}

// TestSourceGatesInRootManifest: gates declared in the harness-ROOT
// omakase.manifest never run (gates live in payload/omakase.manifest), so the
// install is refused with a pointer there — a doc-following author's gates must
// not be silently substituted by the base's.
func TestSourceGatesInRootManifest(t *testing.T) {
	src := newSourceRepo(t)
	writeFile(t, filepath.Join(src, "omakase.manifest"),
		"name: rooter\n\ngate: block-marker\n  hook: pre-commit\n  run: .omakase/gates/x.sh\n")
	writeFile(t, filepath.Join(src, "payload", "rule.md"), "a rule\n")
	commitAll(t, src, "gates-in-root")
	assertSourceRefusal(t, src,
		"omakase: source '"+src+"' declares gate: blocks in its root omakase.manifest, which omakase never runs — gates belong in payload/omakase.manifest (placed and snapshotted at init). Move the gate: blocks there and re-run. Nothing was changed.\n")
}

// TestSourceEmptyPayload: a manifest but no non-empty payload/ tree is refused.
// git cannot track an empty dir, so payload/ is absent in the clone.
func TestSourceEmptyPayload(t *testing.T) {
	src := newSourceRepo(t)
	writeFile(t, filepath.Join(src, "omakase.manifest"), "name: empty\n")
	commitAll(t, src, "no-payload")
	assertSourceRefusal(t, src,
		"omakase: source '"+src+"' has no non-empty payload/ tree — nothing to inject\n")
}

// TestSourceCRLFManifest: a CRLF manifest with trailing spaces yields
// name/version stripped of the ^M and surrounding whitespace — the "cached at"
// line carries the clean values, no ^M leaks downstream.
func TestSourceCRLFManifest(t *testing.T) {
	_, repo := initRepo(t)
	srcTestEnv(t)
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

// TestSourceRefPinBranch: --source repo#branch checks the cache out to that
// branch; the branch-specific delta is installed, and the pinned label
// round-trips into placed.tsv col3 + $OMK/source.
func TestSourceRefPinBranch(t *testing.T) {
	dir, repo := initRepo(t)
	srcTestEnv(t)
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

// TestSourceRefPinTag: a tag ref resolves via fetch --tags; the tagged (older)
// content installs, not HEAD.
func TestSourceRefPinTag(t *testing.T) {
	dir, _ := initRepo(t)
	srcTestEnv(t)
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

// TestSourceRefNotFound: an unknown ref is refused, with nothing placed.
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

// TestRememberedSourceRoundTrip: the init-twice contract. Init 1 pins a tag and
// writes $OMK/source; a bare init 2 (no flag, OMAKASE_PAYLOAD unset) reads it
// back, re-splits the pinned ref, refreshes the cache, and re-installs the same
// pinned content. A tag is used because its ref is immutable: the refresh's
// hard-reset lands on a detached HEAD, which the tag re-checkout overrides, so
// the pinned content survives. A branch pin does not survive — see
// TestBranchPinNotPreserved.
func TestRememberedSourceRoundTrip(t *testing.T) {
	dir, repo := initRepo(t)
	srcTestEnv(t)
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

// TestBranchPinNotPreserved: a branch pin does not survive a bare re-run — the
// refresh hard-resets the checked-out local branch to the remote default, and
// re-checking-out that same branch yields the default branch's content.
func TestBranchPinNotPreserved(t *testing.T) {
	dir, _ := initRepo(t)
	srcTestEnv(t)
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

// TestSourceCacheRefreshPicksUpNewCommit: a second --source install against a
// source whose default branch advanced refreshes the cache (fetch + hard reset)
// and places the new tip.
func TestSourceCacheRefreshPicksUpNewCommit(t *testing.T) {
	dir, _ := initRepo(t)
	srcTestEnv(t)
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

// TestSourceCorruptCacheReclone: a corrupt cache (garbage .git/HEAD) is discarded
// with the stale notice and re-cloned; the fresh clone delivers the latest
// payload.
func TestSourceCorruptCacheReclone(t *testing.T) {
	dir, _ := initRepo(t)
	srcTestEnv(t)
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

// ---------------------------------------------------------------- offline repair

// TestFetchSourceReusesCacheWhenRefreshFails: a prior fetch leaves a healthy,
// valid cache (.git + omakase.manifest + payload/). The cache's remote then goes
// dark (repointed at a nonexistent local path, no network involved) and the
// source itself disappears too, so both refreshCache's `git fetch` and any
// reclone attempt (which would clone from `src` verbatim) are impossible — the
// genuinely offline case a bare-init repair must survive. fetchSource must reuse
// the retained cache and succeed, instead of deleting the only usable copy and
// then failing to reclone with nothing to fall back to.
func TestFetchSourceReusesCacheWhenRefreshFails(t *testing.T) {
	srcTestEnv(t)

	src := newSourceRepo(t)
	writeFile(t, filepath.Join(src, "omakase.manifest"), "name: reuse\nversion: 9.9\n")
	writeFile(t, filepath.Join(src, "payload", ".omakase", "gates", "g.sh"), "ORIGINAL\n")
	commitAll(t, src, "v1")

	// Prime a real, healthy cache via the normal fetch path.
	var o1, e1 strings.Builder
	payloadDir1, _, code1 := fetchSource(src, "", "", &o1, &e1)
	if code1 != 0 {
		t.Fatalf("priming fetch failed: code=%d stderr=%q", code1, e1.String())
	}
	cache := sourceCacheDir(src)
	if !isDir(filepath.Join(cache, ".git")) {
		t.Fatalf("priming fetch did not leave a cache at %s", cache)
	}

	// Go dark: repoint the cache's own remote at a nonexistent local path (a
	// missing local path fails `git fetch` immediately, no network needed), and
	// remove the source directory itself so a reclone attempt — which uses
	// `src` verbatim, not the cache's remote — has nothing to clone from either.
	deadRemote := filepath.Join(t.TempDir(), "gone", "nowhere.git")
	runGitT(t, cache, "remote", "set-url", "origin", deadRemote)
	if err := os.RemoveAll(src); err != nil {
		t.Fatal(err)
	}

	var o2, e2 strings.Builder
	payloadDir2, _, code2 := fetchSource(src, "", "", &o2, &e2)
	if code2 != 0 {
		t.Fatalf("fetchSource with a dead remote must still succeed from the retained cache; code=%d stdout=%q stderr=%q", code2, o2.String(), e2.String())
	}
	eq(t, "reused payload dir", payloadDir2, payloadDir1)
	if !isDir(filepath.Join(cache, ".git")) {
		t.Error("cache dir was deleted despite being a healthy, usable, retained copy")
	}
	if !isDir(filepath.Join(cache, "payload")) {
		t.Error("cache payload/ was deleted despite being a healthy, usable, retained copy")
	}
	eq(t, "retained gate content", readFileT(t, filepath.Join(cache, "payload", ".omakase", "gates", "g.sh")), "ORIGINAL\n")
	if !strings.Contains(e2.String(), "reusing the cached copy") {
		t.Errorf("no offline-reuse notice on stderr:\n%s", e2.String())
	}
	if strings.Contains(e2.String(), "stale or corrupt") {
		t.Errorf("wrongly reported the retained cache as corrupt:\n%s", e2.String())
	}
}

// ---------------------------------------------------------------- merge semantics

// TestMergeSourceWinsOverlap: base and source both ship a file at the same path;
// the source wins (replace semantics, rm-before-copy).
func TestMergeSourceWinsOverlap(t *testing.T) {
	dir, _ := initRepo(t)
	srcTestEnv(t)
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

// TestMergeSymlinkOverFile: base ships a regular file where the source ships a
// symlink — the merged path is the source symlink.
func TestMergeSymlinkOverFile(t *testing.T) {
	dir, _ := initRepo(t)
	srcTestEnv(t)
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

// TestMergeFileOverSymlink: base ships a symlink where the source ships a regular
// file — the merged path is the source regular file, and the base symlink's
// target is not written through (proving the rm-before-copy).
func TestMergeFileOverSymlink(t *testing.T) {
	dir, _ := initRepo(t)
	srcTestEnv(t)
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
	// link.md is a regular file with the source content (not a symlink).
	if isSymlink(filepath.Join(dir, "link.md")) {
		t.Error("link.md stayed a symlink — the source regular file did not win")
	}
	eq(t, "link.md content", readFileT(t, filepath.Join(dir, "link.md")), "source regular content\n")
	// The base symlink's target was not clobbered through the link.
	eq(t, "base target untouched", readFileT(t, filepath.Join(dir, "target.md")), "base target untouched\n")
}

// TestMergeStagingCleaned: the merge staging dir under TMPDIR is removed on a
// successful run.
func TestMergeStagingCleaned(t *testing.T) {
	initRepo(t)
	srcTestEnv(t)
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

// TestSourceLefthookLocalRefusalPostMerge: the manifest guard runs on the merged
// payload — a source that still ships lefthook-local.yml is refused after the
// merge with the migration message, and nothing is placed.
func TestSourceLefthookLocalRefusalPostMerge(t *testing.T) {
	dir, repo := initRepo(t)
	srcTestEnv(t)
	useBasePayloadDir(t)

	src := newSourceRepo(t)
	writeFile(t, filepath.Join(src, "omakase.manifest"), "name: legacy-wiring\n")
	writeFile(t, filepath.Join(src, "payload", "lefthook-local.yml"),
		"pre-commit:\n  jobs:\n    - name: ghost\n      run: bash .omakase/gates/example.sh\n")
	commitAll(t, src, "src")

	var stdout, stderr strings.Builder
	code := RunInit([]string{"--source", src}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("exit = %d, want 1; stderr=%q", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "lefthook-local.yml, which omakase no longer reads") {
		t.Errorf("refusal did not carry the migration message:\n%s", stderr.String())
	}
	if _, err := os.Stat(filepath.Join(dir, ".omakase")); err == nil {
		t.Error("placed files despite the wiring refusal")
	}
	// The cached-at line did print (fetch/validate precede the guard), but nothing
	// was placed and no source was remembered.
	if _, err := os.Stat(filepath.Join(repo.OMK, "source")); err == nil {
		t.Error("remembered a source despite the wiring refusal")
	}
}

// ---------------------------------------------------------------- clone failure

// TestSourceCloneFailure: a local path that exists but is not a git repo fails
// the clone; the message names the source + cache, and nothing is placed. No
// network.
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

// TestSourceSlugCollisionResistance: two sources sharing a basename but at
// different URLs get different cache slugs; one source always maps to one.
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

// TestSanitizeBase: the basename sanitizer.
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

// TestExpandSource: shorthand -> GitHub URL, #ref split, URL/scp passthrough,
// subpath split (the "//" marker and the owner/repo/subpath shorthand), and
// local-dir absolutize.
func TestExpandSource(t *testing.T) {
	type sr struct{ src, ref, sub string }
	cases := map[string]sr{
		"you/harness":             {"https://github.com/you/harness", "", ""},
		"you/harness#v1":          {"https://github.com/you/harness", "v1", ""},
		"https://x.com/a/b":       {"https://x.com/a/b", "", ""},
		"https://x.com/a/b#dev":   {"https://x.com/a/b", "dev", ""},
		"git@github.com:a/b.git":  {"git@github.com:a/b.git", "", ""},
		"single-segment-no-slash": {"single-segment-no-slash", "", ""},
		// The extended shorthand: segments past owner/repo are the subpath.
		"you/hub/tools":          {"https://github.com/you/hub", "", "tools"},
		"you/hub/tools/gates#v2": {"https://github.com/you/hub", "v2", "tools/gates"},
		// The explicit "//" marker: after the scheme, on scp paths, with a ref,
		// on the shorthand root, and degenerate (trailing, empty) forms.
		"https://x.com/a/b//sub":      {"https://x.com/a/b", "", "sub"},
		"https://x.com/a/b//sub#main": {"https://x.com/a/b", "main", "sub"},
		"git@github.com:a/b//s/t":     {"git@github.com:a/b", "", "s/t"},
		"you/hub//tools":              {"https://github.com/you/hub", "", "tools"},
		"https://x.com/a/b//":         {"https://x.com/a/b", "", ""},
		"https://x.com/a/b///sub/":    {"https://x.com/a/b", "", "sub"},
		// The canonical remembered shape round-trips through expandSource.
		"https://x.com/a/b//sub#v1": {"https://x.com/a/b", "v1", "sub"},
	}
	for in, want := range cases {
		gs, gr, gp := expandSource(in)
		if gs != want.src || gr != want.ref || gp != want.sub {
			t.Errorf("expandSource(%q) = (%q,%q,%q), want (%q,%q,%q)", in, gs, gr, gp, want.src, want.ref, want.sub)
		}
	}
	// A local directory is absolutized; no ref is split from an existing path.
	d := t.TempDir()
	if gs, gr, gp := expandSource(d); gs != d || gr != "" || gp != "" {
		t.Errorf("expandSource(localdir %q) = (%q,%q,%q), want (%q,\"\",\"\")", d, gs, gr, gp, d)
	}
	// A local hub with the "//" marker: the marker survives even though the OS
	// would collapse "//" in a stat (an existing d/sub must not swallow it),
	// and the ROOT is what gets absolutized.
	if err := os.MkdirAll(filepath.Join(d, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if gs, gr, gp := expandSource(d + "//sub"); gs != d || gr != "" || gp != "sub" {
		t.Errorf("expandSource(%q) = (%q,%q,%q), want (%q,\"\",\"sub\")", d+"//sub", gs, gr, gp, d)
	}
}

// TestManifestField: first-match value, CRLF + whitespace stripping, no-value and
// missing-key -> "".
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

// ------------------------------------------- base payload env handoff

// Since v0.18.0 the binary may run apart from the plugin (a fetched release cache
// or a PATH install), so the shims hand the merge base's location over in
// OMAKASE_BASE_PAYLOAD; the binary-relative $SCRIPT_DIR/../payload is a last
// resort only. These tests run with basePayloadOverride cleared so defaultPayload
// actually consults the env tier.

// TestDefaultPayloadHonorsBasePayloadEnv: with basePayloadOverride cleared,
// OMAKASE_BASE_PAYLOAD is the resolved base.
func TestDefaultPayloadHonorsBasePayloadEnv(t *testing.T) {
	clearBasePayloadOverride(t)
	dir := t.TempDir()
	t.Setenv("OMAKASE_BASE_PAYLOAD", dir)
	if got := defaultPayload(); got != dir {
		t.Errorf("defaultPayload() = %q, want %q (OMAKASE_BASE_PAYLOAD)", got, dir)
	}
}

// TestSourceMergeBaseFromEnv: a full --source install with basePayloadOverride
// cleared, OMAKASE_PAYLOAD empty, and OMAKASE_BASE_PAYLOAD pointing at a base
// fixture merges that env-pointed base under the source delta — both a base-only
// file and the source delta file land on disk.
func TestSourceMergeBaseFromEnv(t *testing.T) {
	dir, _ := initRepo(t)
	srcTestEnv(t) // OMAKASE_PAYLOAD="" among others
	clearBasePayloadOverride(t)

	base := t.TempDir()
	writeFile(t, filepath.Join(base, ".omakase", "bin", "base.sh"), "base\n")
	t.Setenv("OMAKASE_BASE_PAYLOAD", base)

	src := newSourceRepo(t)
	writeFile(t, filepath.Join(src, "omakase.manifest"), "name: envbase\n")
	writeFile(t, filepath.Join(src, "payload", ".omakase", "gates", "src.sh"), "src\n")
	commitAll(t, src, "src")

	var stdout, stderr strings.Builder
	if code := RunInit([]string{"--source", src}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%q", code, stderr.String())
	}
	// One of each: the env-pointed base merged in under the source delta.
	eq(t, "base-only file", readFileT(t, filepath.Join(dir, ".omakase", "bin", "base.sh")), "base\n")
	eq(t, "source delta file", readFileT(t, filepath.Join(dir, ".omakase", "gates", "src.sh")), "src\n")
}

// TestSourceMergeBaseMissing: with basePayloadOverride cleared and
// OMAKASE_BASE_PAYLOAD set to a nonexistent path, the missing merge base is
// caught before any clone/fetch — exit 1 with the guidance and no "cached at"
// line (proving the check runs first).
func TestSourceMergeBaseMissing(t *testing.T) {
	initRepo(t)
	srcTestEnv(t)
	clearBasePayloadOverride(t)

	missing := filepath.Join(t.TempDir(), "missing")
	t.Setenv("OMAKASE_BASE_PAYLOAD", missing)

	src := newSourceRepo(t)
	writeFile(t, filepath.Join(src, "omakase.manifest"), "name: wouldwork\n")
	writeFile(t, filepath.Join(src, "payload", ".omakase", "gates", "src.sh"), "src\n")
	commitAll(t, src, "src")

	var stdout, stderr strings.Builder
	code := RunInit([]string{"--source", src}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("exit = %d, want 1; stderr=%q", code, stderr.String())
	}
	eq(t, "base-missing stderr", stderr.String(),
		"omakase: base payload not found at "+missing+" — set OMAKASE_BASE_PAYLOAD or run omakase via the plugin's bin/ shims\n")
	if strings.Contains(stdout.String(), "cached at") || strings.Contains(stderr.String(), "cached at") {
		t.Errorf("the base check must run before any clone/fetch (no 'cached at'):\n stdout=%q\n stderr=%q", stdout.String(), stderr.String())
	}
}

// TestBareRunRememberedSourceSurvivesBasePayloadEnv: after a --source install
// remembered in $OMK/source, a bare re-run with OMAKASE_BASE_PAYLOAD exported
// (OMAKASE_PAYLOAD empty) must still take the remembered-source merge path, not a
// plain install — the remembered-source suppression keys on OMAKASE_PAYLOAD only,
// so the shim-exported base var must never suppress it (that would reintroduce
// the bug: bare re-runs silently downgrading to plain installs). The source delta
// being re-placed proves the merge path ran; a plain install (base only) would
// instead sweep it as an orphan.
func TestBareRunRememberedSourceSurvivesBasePayloadEnv(t *testing.T) {
	dir, repo := initRepo(t)
	srcTestEnv(t)
	clearBasePayloadOverride(t)

	base := t.TempDir()
	writeFile(t, filepath.Join(base, ".omakase", "bin", "base.sh"), "base\n")
	t.Setenv("OMAKASE_BASE_PAYLOAD", base)

	src := newSourceRepo(t)
	writeFile(t, filepath.Join(src, "omakase.manifest"), "name: remembered\n")
	writeFile(t, filepath.Join(src, "payload", ".omakase", "gates", "src.sh"), "src delta\n")
	commitAll(t, src, "src")

	var o1, e1 strings.Builder
	if code := RunInit([]string{"--source", src}, &o1, &e1); code != 0 {
		t.Fatalf("init 1 exit = %d; stderr=%q", code, e1.String())
	}
	eq(t, "remembered source", readFileT(t, filepath.Join(repo.OMK, "source")), src+"\n")

	// Bare re-run: no argv, OMAKASE_PAYLOAD still empty, OMAKASE_BASE_PAYLOAD set.
	var o2, e2 strings.Builder
	if code := RunInit(nil, &o2, &e2); code != 0 {
		t.Fatalf("bare re-run exit = %d; stderr=%q", code, e2.String())
	}
	eq(t, "source delta re-placed via the remembered merge path",
		readFileT(t, filepath.Join(dir, ".omakase", "gates", "src.sh")), "src delta\n")
	// the bare run re-emitted the "cached at" line — it re-fetched the remembered
	// source rather than doing a plain install off OMAKASE_BASE_PAYLOAD.
	if !strings.Contains(o2.String(), "omakase: source '"+src+"' (name: remembered) cached at ") {
		t.Errorf("bare re-run did not take the remembered-source merge path:\n%s", o2.String())
	}
}

// ---------------------------------------------------------------- subpath sources

// TestSourceSubpathMerge: a `root//subpath` source adopts the harness at that
// directory inside the clone — validation and payload/ resolve at the
// subpath, decoy manifest/payload at the repo ROOT are ignored, and the
// canonical string lands in placed.tsv column 3, $OMK/source, the "cached
// at" line, and the cache slug.
func TestSourceSubpathMerge(t *testing.T) {
	dir, repo := initRepo(t)
	srcTestEnv(t)
	base := useBasePayloadDir(t)
	writeFile(t, filepath.Join(base, ".omakase", "bin", "base.sh"), "base\n")

	src := newSourceRepo(t)
	// Decoys at the clone root: a subpath install must never read these.
	writeFile(t, filepath.Join(src, "omakase.manifest"), "name: decoy\n")
	writeFile(t, filepath.Join(src, "payload", "decoy.txt"), "never placed\n")
	// The real harness lives two levels down, next to other hub content.
	writeFile(t, filepath.Join(src, "tools", "harness", "omakase.manifest"), "name: hubbed\nversion: 0.1\n")
	writeFile(t, filepath.Join(src, "tools", "harness", "payload", ".omakase", "gates", "src.sh"), "src gate\n")
	writeFile(t, filepath.Join(src, "tools", "harness", "payload", ".claude", "rules", "r.md"), "rule\n")
	commitAll(t, src, "hub")

	canonical := src + "//tools/harness"
	var stdout, stderr strings.Builder
	if code := RunInit([]string{"--source", canonical}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%q", code, stderr.String())
	}

	cache := sourceCacheDir(canonical)
	wantOut := "omakase: source '" + canonical + "' (name: hubbed, version: 0.1) cached at " + cache + "\n" +
		"omakase: placed 3 file(s), overwrote 0 to match payload, skipped 0 committed path(s).\n" +
		"  + .claude/rules/r.md\n" +
		"  + .omakase/bin/base.sh\n" +
		"  + .omakase/gates/src.sh\n" +
		"omakase: ignores -> .git/info/exclude; new worktrees auto-install the harness. Nothing to commit.\n" +
		"omakase: see the whole harness any time with  omakase status\n" +
		"omakase: to customize, fork the harness source (clone -> edit -> publish) and\n" +
		"         init from your copy; do not edit injected files in place (overwritten on re-init).\n" +
		uxStanzas() + verifiedLine
	eq(t, "stdout", stdout.String(), wantOut)
	eq(t, "stderr", stderr.String(), "")

	eq(t, "delta gate", readFileT(t, filepath.Join(dir, ".omakase", "gates", "src.sh")), "src gate\n")
	eq(t, "base file", readFileT(t, filepath.Join(dir, ".omakase", "bin", "base.sh")), "base\n")
	if pathExists(filepath.Join(dir, "decoy.txt")) {
		t.Error("root-level decoy payload was placed; subpath validation leaked to the clone root")
	}
	for _, row := range state.ReadPlaced(filepath.Join(repo.OMK, "placed.tsv")) {
		if row.Src != canonical {
			t.Errorf("placed.tsv col3 = %q for %q, want %q", row.Src, row.Rel, canonical)
		}
	}
	eq(t, "OMK/source", readFileT(t, filepath.Join(repo.OMK, "source")), canonical+"\n")
	// The slug carries the harness directory's basename, not the hub repo's.
	if !strings.HasPrefix(filepath.Base(cache), "harness-") {
		t.Errorf("cache slug %q does not carry the subpath basename", filepath.Base(cache))
	}
}

// TestSourceSubpathMissingDir: a subpath that names no directory in the clone
// fails closed (exit 1) before any manifest check, placing nothing.
func TestSourceSubpathMissingDir(t *testing.T) {
	dir, _ := initRepo(t)
	srcTestEnv(t)
	useBasePayloadDir(t)

	src := newSourceRepo(t)
	writeFile(t, filepath.Join(src, "omakase.manifest"), "name: hub\n")
	writeFile(t, filepath.Join(src, "payload", "x.txt"), "x\n")
	commitAll(t, src, "hub")

	var stdout, stderr strings.Builder
	code := RunInit([]string{"--source", src + "//no/such/dir"}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("exit = %d, want 1; stderr=%q", code, stderr.String())
	}
	eq(t, "stderr", stderr.String(), "omakase: source '"+src+"' has no directory 'no/such/dir' — nothing to adopt\n")
	if pathExists(filepath.Join(dir, "x.txt")) {
		t.Error("payload placed despite the missing subpath")
	}
}

// TestSourceSubpathManifestValidatedAtSubroot: the fail-closed manifest check
// runs at the SUBPATH root — a valid manifest at the clone root must not
// stand in for a missing one under the subpath.
func TestSourceSubpathManifestValidatedAtSubroot(t *testing.T) {
	_, _ = initRepo(t)
	srcTestEnv(t)
	useBasePayloadDir(t)

	src := newSourceRepo(t)
	writeFile(t, filepath.Join(src, "omakase.manifest"), "name: root-ok\n")
	writeFile(t, filepath.Join(src, "payload", "x.txt"), "x\n")
	writeFile(t, filepath.Join(src, "sub", "payload", "y.txt"), "y\n") // no manifest here
	commitAll(t, src, "hub")

	var stdout, stderr strings.Builder
	code := RunInit([]string{"--source", src + "//sub"}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("exit = %d, want 1; stderr=%q", code, stderr.String())
	}
	eq(t, "stderr", stderr.String(), "omakase: source '"+src+"//sub' has no omakase.manifest at its root — not an omakase source\n")
}

// TestSourceSubpathTraversalRefused: a subpath that escapes (or degenerates
// to) the clone root is refused up front, exit 2, before any fetch.
func TestSourceSubpathTraversalRefused(t *testing.T) {
	_, _ = initRepo(t)
	srcTestEnv(t)
	useBasePayloadDir(t)

	src := newSourceRepo(t)
	writeFile(t, filepath.Join(src, "omakase.manifest"), "name: hub\n")
	writeFile(t, filepath.Join(src, "payload", "x.txt"), "x\n")
	commitAll(t, src, "hub")

	for _, sub := range []string{"../evil", "a/../..", ".."} {
		var stdout, stderr strings.Builder
		code := RunInit([]string{"--source", src + "//" + sub}, &stdout, &stderr)
		if code != 2 {
			t.Fatalf("subpath %q: exit = %d, want 2; stderr=%q", sub, code, stderr.String())
		}
		if !strings.Contains(stderr.String(), "must stay inside the source repo") {
			t.Errorf("subpath %q: refusal message missing; stderr=%q", sub, stderr.String())
		}
	}

	// A subpath with no repo before the marker is explicit intent that must
	// never collapse silently into a plain install.
	for _, in := range []string{"//sub", "//sub#ref"} {
		var stdout, stderr strings.Builder
		code := RunInit([]string{"--source", in}, &stdout, &stderr)
		if code != 2 {
			t.Fatalf("source %q: exit = %d, want 2; stderr=%q", in, code, stderr.String())
		}
		if !strings.Contains(stderr.String(), "missing the repo part before the '//' subpath marker") {
			t.Errorf("source %q: refusal message missing; stderr=%q", in, stderr.String())
		}
	}
}

// TestSourceSubpathRememberedRoundTrip: the canonical root//subpath string is
// remembered, and a bare re-run re-fetches the hub and re-injects the same
// subfolder — including content the hub's default branch gained since.
func TestSourceSubpathRememberedRoundTrip(t *testing.T) {
	dir, repo := initRepo(t)
	srcTestEnv(t)
	useBasePayloadDir(t)

	src := newSourceRepo(t)
	writeFile(t, filepath.Join(src, "sub", "omakase.manifest"), "name: rt-sub\n")
	writeFile(t, filepath.Join(src, "sub", "payload", ".omakase", "gates", "g.sh"), "V1\n")
	commitAll(t, src, "v1")

	canonical := src + "//sub"
	var o1, e1 strings.Builder
	if code := RunInit([]string{"--source", canonical}, &o1, &e1); code != 0 {
		t.Fatalf("init 1 exit = %d; stderr=%q", code, e1.String())
	}
	eq(t, "init1 content", readFileT(t, filepath.Join(dir, ".omakase", "gates", "g.sh")), "V1\n")

	// The hub advances; a bare re-run must re-fetch and re-inject the subfolder.
	writeFile(t, filepath.Join(src, "sub", "payload", ".omakase", "gates", "g.sh"), "V2\n")
	commitAll(t, src, "v2")

	var o2, e2 strings.Builder
	if code := RunInit(nil, &o2, &e2); code != 0 {
		t.Fatalf("bare re-run exit = %d; stderr=%q", code, e2.String())
	}
	eq(t, "roundtrip content", readFileT(t, filepath.Join(dir, ".omakase", "gates", "g.sh")), "V2\n")
	eq(t, "roundtrip remembered", readFileT(t, filepath.Join(repo.OMK, "source")), canonical+"\n")
	if !strings.Contains(o2.String(), "omakase: source '"+canonical+"' (name: rt-sub) cached at ") {
		t.Errorf("bare re-run did not re-fetch the remembered subpath source:\n%s", o2.String())
	}
}
