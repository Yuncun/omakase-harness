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

// A path git would C-quote in newline output (a backslash in the name) must
// round-trip raw: -z listing, real masking, verified absence. Before the -z
// fix, blocking the QUOTED name "succeeded" while the real file kept
// steering.
func TestBlockCQuotablePath(t *testing.T) {
	dir := newRepo(t)
	name := `.claude/rules/back\slash.md`
	write(t, dir, name, "tricky\n")
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-q", "-m", "quoted name")

	code, _, errOut := runVerb(t, dir, false, name, "--yes")
	if code != 0 {
		t.Fatalf("exit %d, stderr %s", code, errOut)
	}
	if lexists(t, dir, name) {
		t.Error("file still present — blocked a quoted alias instead of the real path")
	}
	if code, _, e := runVerb(t, dir, true, name); code != 0 {
		t.Fatalf("unblock: %s", e)
	}
	if !lexists(t, dir, name) {
		t.Error("file not restored")
	}
}

// A committed path with a trailing space must round-trip the ledger intact:
// the reader may strip line endings only, or unblock refuses and the mask
// hides the wrong file.
func TestBlockTrailingSpacePath(t *testing.T) {
	dir := newRepo(t)
	name := ".claude/rules/trail .md"
	write(t, dir, name, "spaced\n")
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-q", "-m", "spaced name")

	if code, _, e := runVerb(t, dir, false, name, "--yes"); code != 0 {
		t.Fatalf("block: %s", e)
	}
	if lexists(t, dir, name) {
		t.Error("spaced file still present")
	}
	repo, _ := state.Discover(dir)
	if !state.ReadBlocked(repo.OMK)[name] {
		t.Errorf("ledger lost the exact name; has %v", state.ReadBlocked(repo.OMK))
	}
	if code, _, e := runVerb(t, dir, true, name); code != 0 {
		t.Fatalf("unblock refused the exact name: %s", e)
	}
	if !lexists(t, dir, name) {
		t.Error("spaced file not restored")
	}
}

// Patterns edited outside omakase (a user `git sparse-checkout add`) refuse
// the next block/unblock instead of being silently rewritten away.
func TestBlockRefusesDivergedPatterns(t *testing.T) {
	dir := newRepo(t)
	if code, _, e := runVerb(t, dir, false, "CLAUDE.md", "--yes"); code != 0 {
		t.Fatal(e)
	}
	runGit(t, dir, "sparse-checkout", "add", "!/vendor")

	code, _, errOut := runVerb(t, dir, false, ".claude/rules/style.md", "--yes")
	if code != 2 || !strings.Contains(errOut, "changed outside omakase") {
		t.Errorf("block after foreign add: exit %d stderr %q", code, errOut)
	}
	code, _, errOut = runVerb(t, dir, true, "CLAUDE.md")
	if code != 2 || !strings.Contains(errOut, "changed outside omakase") {
		t.Errorf("unblock after foreign add: exit %d stderr %q", code, errOut)
	}
	// The user's line survived both refusals.
	found := false
	for _, l := range currentPatterns(dir) {
		if l == "!/vendor" {
			found = true
		}
	}
	if !found {
		t.Errorf("user pattern gone: %v", currentPatterns(dir))
	}
}

// Stale residue from the user's OWN past sparse-checkout (file survives a
// disable) blocks the first omakase block — a plain set would destroy it.
func TestBlockRefusesStalePatternResidue(t *testing.T) {
	dir := newRepo(t)
	runGit(t, dir, "sparse-checkout", "set", ".claude")
	runGit(t, dir, "sparse-checkout", "disable") // pattern file survives
	code, _, errOut := runVerb(t, dir, false, "CLAUDE.md", "--yes")
	if code != 2 || !strings.Contains(errOut, "sparse-checkout patterns of its own") {
		t.Errorf("exit %d stderr %q", code, errOut)
	}
}

// After the last unblock, extensions.worktreeConfig and config.worktree are
// reverted too (single-worktree repos) — the every-trace-reversed promise
// extends to config.
func TestUnblockCleansWorktreeConfig(t *testing.T) {
	dir := newRepo(t)
	if code, _, e := runVerb(t, dir, false, "CLAUDE.md", "--yes"); code != 0 {
		t.Fatal(e)
	}
	if code, _, e := runVerb(t, dir, true, "CLAUDE.md"); code != 0 {
		t.Fatal(e)
	}
	repo, _ := state.Discover(dir)
	if _, err := os.Lstat(filepath.Join(repo.CommonDir, "config.worktree")); !os.IsNotExist(err) {
		t.Error("config.worktree left behind")
	}
	out := runGit(t, dir, "config", "--list")
	if strings.Contains(out, "extensions.worktreeconfig=true") {
		t.Error("extensions.worktreeConfig left set")
	}
	// And $OMK is gone: a blocked-then-unblocked repo reads never-installed.
	if _, err := os.Lstat(repo.OMK); !os.IsNotExist(err) {
		t.Error("$OMK dir left behind after last unblock")
	}
}

// A global core.sparseCheckout=true (user dotfiles) must not read as
// foreign sparse state — the probe is the repo's pattern file, not sparse
// mode (review finding: the mode probe made block permanently unusable
// under that global with a false explanation).
func TestBlockWorksUnderGlobalSparseConfig(t *testing.T) {
	g := filepath.Join(t.TempDir(), "gitconfig")
	if err := os.WriteFile(g, []byte("[core]\n\tsparseCheckout = true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", g)
	dir := newRepo(t)
	code, _, errOut := runVerb(t, dir, false, "CLAUDE.md", "--yes")
	if code != 0 {
		t.Fatalf("exit %d, stderr %s", code, errOut)
	}
	if lexists(t, dir, "CLAUDE.md") {
		t.Error("not masked")
	}
	if code, _, e := runVerb(t, dir, true, "CLAUDE.md"); code != 0 {
		t.Fatalf("unblock: %s", e)
	}
}
