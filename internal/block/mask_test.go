package block

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Yuncun/omakase-harness/internal/state"
)

// Reassert re-hides a blocked item a git operation brought back (here:
// simulated with a manual `git sparse-checkout disable`, the same end state
// as git am or merge --abort), and is a no-op when everything is hidden.
func TestReassertRehidesVivifiedItem(t *testing.T) {
	dir := newRepo(t)
	if code, _, e := runVerb(t, dir, false, "CLAUDE.md", "--yes"); code != 0 {
		t.Fatal(e)
	}
	repo, _ := state.Discover(dir)

	if n := Reassert(repo, io.Discard); n != 0 {
		t.Errorf("healthy state: Reassert = %d, want 0", n)
	}

	runGit(t, dir, "sparse-checkout", "disable") // vivifies CLAUDE.md
	if !lexists(t, dir, "CLAUDE.md") {
		t.Fatal("precondition: disable should restore the file")
	}
	if n := Reassert(repo, io.Discard); n != 1 {
		t.Errorf("Reassert = %d, want 1", n)
	}
	if lexists(t, dir, "CLAUDE.md") {
		t.Error("CLAUDE.md still present after Reassert")
	}
}

// Reassert never fights an in-progress merge — the operation materialized
// the file to do its work.
func TestReassertSkipsMidMerge(t *testing.T) {
	dir := newRepo(t)
	if code, _, e := runVerb(t, dir, false, "CLAUDE.md", "--yes"); code != 0 {
		t.Fatal(e)
	}
	repo, _ := state.Discover(dir)
	runGit(t, dir, "sparse-checkout", "disable")

	// Fake the merge marker: opInProgress only stats the git-path.
	mh, err := gitPath(dir, "MERGE_HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mh, []byte("deadbeef\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	defer os.Remove(mh)

	if n := Reassert(repo, io.Discard); n != 0 {
		t.Errorf("Reassert mid-merge = %d, want 0 (skip)", n)
	}
	if !lexists(t, dir, "CLAUDE.md") {
		t.Error("Reassert masked a file during a merge")
	}
}

// block and unblock refuse while a merge is underway, before any write.
func TestBlockRefusesMidMerge(t *testing.T) {
	dir := newRepo(t)
	mh, err := gitPath(dir, "MERGE_HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mh, []byte("deadbeef\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, _, errOut := runVerb(t, dir, false, "CLAUDE.md", "--yes")
	if code != 2 || !strings.Contains(errOut, "in progress") {
		t.Errorf("exit %d stderr %q", code, errOut)
	}
	repo, _ := state.Discover(dir)
	if len(state.ReadBlocked(repo.OMK)) != 0 {
		t.Error("ledger written despite mid-merge refusal")
	}
}

// Re-blocking an already-blocked item re-applies the mask — the manual heal
// for a silent vivification.
func TestReblockReappliesMask(t *testing.T) {
	dir := newRepo(t)
	if code, _, e := runVerb(t, dir, false, "CLAUDE.md", "--yes"); code != 0 {
		t.Fatal(e)
	}
	runGit(t, dir, "sparse-checkout", "disable")
	code, out, errOut := runVerb(t, dir, false, "CLAUDE.md", "--yes")
	if code != 0 {
		t.Fatalf("re-block: exit %d stderr %s", code, errOut)
	}
	if !strings.Contains(out, "re-applied") {
		t.Errorf("out = %q", out)
	}
	if lexists(t, dir, "CLAUDE.md") {
		t.Error("re-block did not re-hide the file")
	}
}

// escapePattern: literal glob characters stay literal; a trailing space is
// preserved through escaping.
func TestEscapePattern(t *testing.T) {
	cases := map[string]string{
		"a/plain.md":     "a/plain.md",
		"a/star*.md":     "a/star\\*.md",
		"a/brack[et].md": "a/brack\\[et\\].md",
		"a/q?.md":        "a/q\\?.md",
		"a/back\\.md":    "a/back\\\\.md",
		"a/trail ":       "a/trail\\ ",
	}
	for in, want := range cases {
		if got := escapePattern(in); got != want {
			t.Errorf("escapePattern(%q) = %q, want %q", in, got, want)
		}
	}
}

// The residue rule: after the last unblock, the pattern file is gone too, so
// a later `git sparse-checkout init` cannot silently re-mask from stale
// patterns.
func TestUnblockRemovesPatternResidue(t *testing.T) {
	dir := newRepo(t)
	if code, _, e := runVerb(t, dir, false, "CLAUDE.md", "--yes"); code != 0 {
		t.Fatal(e)
	}
	if code, _, e := runVerb(t, dir, true, "CLAUDE.md"); code != 0 {
		t.Fatal(e)
	}
	p, err := gitPath(dir, "info/sparse-checkout")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(p); !os.IsNotExist(err) {
		t.Errorf("stale pattern file left at %s", p)
	}
}

// gitPath resolves per-worktree (a linked worktree's info/ lives under
// .git/worktrees/<name>/info/).
func TestGitPathPerWorktree(t *testing.T) {
	dir := newRepo(t)
	wt := filepath.Join(t.TempDir(), "wt")
	runGit(t, dir, "worktree", "add", "-q", wt)
	p, err := gitPath(wt, "info/sparse-checkout")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(p, "worktrees") {
		t.Errorf("linked worktree git-path = %q, want a .git/worktrees/ path", p)
	}
}
