package overlay

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Yuncun/omakase-harness/internal/state"
)

// newSymlinkHarnessSource builds a valid harness source repo whose payload ships
// ONE entry: a symlink at payload/<rel> pointing at target (an absolute path).
// This is the malformed lower layer of the stacked path-traversal attack — a
// directory symlink that, if followed, escapes the repo.
func newSymlinkHarnessSource(t *testing.T, name, rel, target string) string {
	t.Helper()
	src := newSourceRepo(t)
	writeFile(t, filepath.Join(src, "omakase.manifest"), "name: "+name+"\n")
	link := filepath.Join(src, "payload", rel)
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	commitAll(t, src, name)
	return src
}

// TestStackedSymlinkParentTraversalRefused is the security regression test for the
// Phase 3.5 stacked-parent path traversal. A lower harness ships `data` as a
// symlink to an attacker-chosen directory OUTSIDE the repo; a higher harness ships
// a regular file `data/loot`. When the two are stacked, the merge must NOT create
// the directory-symlink and then write the child straight through it to the outside
// target. The run must be refused (non-zero exit), NO file may appear outside the
// repo, and $OMK / the working tree must be left unmutated.
func TestStackedSymlinkParentTraversalRefused(t *testing.T) {
	dir, repo := initRepo(t)
	srcTestEnv(t)
	stubLefthook(t)
	base := useBasePayloadDir(t)
	writeFile(t, filepath.Join(base, ".omakase", "gates", "base.sh"), "base\n")

	// $OUT is an attacker-chosen directory OUTSIDE the victim repo.
	outside := t.TempDir()
	loot := filepath.Join(outside, "loot")

	lowA := newSymlinkHarnessSource(t, "a", "data", outside)
	upB := newHarnessSource(t, "b", map[string]string{"data/loot": "PWNED\n"})

	// Bottom layer: the dir-symlink `data` -> $OUT. Placing a symlink LEAF is a
	// legitimate single-harness install (like the CLAUDE.md -> AGENTS.md bridge) —
	// no escape happens until a child is written THROUGH it.
	if code := RunInit([]string{"--source", lowA}, io.Discard, io.Discard); code != 0 {
		t.Fatalf("init lowA (bottom) failed, exit %d", code)
	}
	// Baseline: the tree/exclude state a successful bottom install leaves behind.
	treeBefore := gitStdout(dir, "status", "--porcelain")

	// Top layer: the file `data/loot`. This is the exploit trigger.
	var stdout, stderr strings.Builder
	code := RunInit([]string{"--source", upB}, &stdout, &stderr)

	// SECURITY: nothing may be written outside the repo, period.
	if _, err := os.Lstat(loot); err == nil {
		content, _ := os.ReadFile(loot)
		t.Fatalf("SECURITY BREACH: stacked harness wrote OUTSIDE the repo at %s (content=%q); the symlinked-parent traversal was NOT blocked (exit=%d, stderr=%q)",
			loot, content, code, stderr.String())
	}

	// The malformed stack must be refused fail-closed, with a message that names
	// the two conflicting paths (the labelled deeper-fix refusal, not a silent
	// backstop).
	if code == 0 {
		t.Errorf("stacking a symlink-vs-dir harness pair exited 0, want non-zero refusal; stderr=%q", stderr.String())
	}
	for _, want := range []string{"refusing to install", "data", "data/loot"} {
		if !strings.Contains(stderr.String(), want) {
			t.Errorf("refusal message must name %q; got %q", want, stderr.String())
		}
	}

	// Zero mutation: the recorded stack stays [1=lowA], no layers/2, clean tree.
	rows := state.ReadSources(filepath.Join(repo.OMK, "sources.tsv"))
	if len(rows) != 1 || rows[0].Source != lowA {
		t.Errorf("recorded stack changed by a refused traversal: %+v", rows)
	}
	if _, err := os.Stat(filepath.Join(repo.OMK, "layers", "2")); err == nil {
		t.Error("layers/2 built despite a refused traversal")
	}
	// The refused stacking init must not FURTHER mutate the tree beyond what the
	// successful bottom install already established.
	if out := gitStdout(dir, "status", "--porcelain"); out != treeBefore {
		t.Errorf("refused traversal changed the working tree:\n before=%q\n after =%q", treeBefore, out)
	}
}

// TestMergeLayers_SymlinkVsDirConflict is the deeper (merge-level) fix: even
// before any copy, a merged view that pits a leaf entry `data` (a symlink or
// file) against another entry beneath `data/` is a malformed harness and must be
// refused fail-closed, naming both conflicting paths and both source labels.
func TestMergeLayers_SymlinkVsDirConflict(t *testing.T) {
	bottom := &LayerSet{Layer: LayerName("1"), Label: "acme/lower", Rels: []string{"data"}}
	top := &LayerSet{Layer: LayerName("2"), Label: "you/upper", Rels: []string{"data/loot"}}

	_, err := MergeLayers([]*LayerSet{bottom, top})
	if err == nil {
		t.Fatal("MergeLayers accepted a symlink-vs-dir conflict; want a fail-closed error")
	}
	msg := err.Error()
	for _, want := range []string{"data", "data/loot", "acme/lower", "you/upper"} {
		if !strings.Contains(msg, want) {
			t.Errorf("conflict error must name %q; got %q", want, msg)
		}
	}
}

// TestMergeLayers_NoFalsePositivePrefix guards against a naive string-prefix
// check: `data` is a string-prefix of `database.md` but NOT a path-prefix, and
// two files under a shared REAL directory `a/` are perfectly valid. Neither is a
// conflict.
func TestMergeLayers_NoFalsePositivePrefix(t *testing.T) {
	sets := []*LayerSet{
		{Layer: LayerName("1"), Label: "x", Rels: []string{"data.md", "database.md", "a/b.md"}},
		{Layer: LayerName("2"), Label: "y", Rels: []string{"a/c.md", "datax"}},
	}
	if _, err := MergeLayers(sets); err != nil {
		t.Errorf("MergeLayers falsely flagged non-conflicting siblings: %v", err)
	}
}
