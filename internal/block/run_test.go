package block

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Yuncun/omakase-harness/internal/state"
)

// newRepo builds a real temp git repo with a committed steering surface:
// CLAUDE.md, a rules file, a .agents skill (two files), and a plain README
// (not agent config).
func newRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	runGit(t, dir, "init", "-q")
	runGit(t, dir, "config", "user.email", "t@t")
	runGit(t, dir, "config", "user.name", "t")
	runGit(t, dir, "config", "commit.gpgsign", "false")
	write(t, dir, "CLAUDE.md", "Always answer in haiku.\n")
	write(t, dir, ".claude/rules/style.md", "tabs only\n")
	write(t, dir, ".agents/skills/od-worktree/SKILL.md", "flat worktrees\n")
	write(t, dir, ".agents/skills/od-worktree/notes.md", "notes\n")
	write(t, dir, "README.md", "hello\n")
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-q", "-m", "init")
	return dir
}

func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "gc.auto=0", "-c", "maintenance.auto=false"}, args...)...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

func write(t *testing.T, dir, rel, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, filepath.Dir(rel)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, rel), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// runVerb runs Run from inside dir, restoring the cwd after.
func runVerb(t *testing.T, dir string, unblock bool, argv ...string) (int, string, string) {
	t.Helper()
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(old)
	var stdout, stderr bytes.Buffer
	code := Run(unblock, argv, &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

func lexists(t *testing.T, dir, rel string) bool {
	t.Helper()
	_, err := os.Lstat(filepath.Join(dir, rel))
	return err == nil
}

func TestBlockWithoutYesExplainsAndAppliesNothing(t *testing.T) {
	dir := newRepo(t)
	code, out, _ := runVerb(t, dir, false, "CLAUDE.md")
	if code != 2 {
		t.Errorf("exit = %d, want 2", code)
	}
	if !strings.Contains(out, "--yes") {
		t.Errorf("consent message must name --yes; got: %s", out)
	}
	if !lexists(t, dir, "CLAUDE.md") {
		t.Error("file vanished without --yes")
	}
	repo, _ := state.Discover(dir)
	if len(state.ReadBlocked(repo.OMK)) != 0 {
		t.Error("ledger written without --yes")
	}
}

func TestBlockUnblockRoundtrip(t *testing.T) {
	dir := newRepo(t)

	code, _, errOut := runVerb(t, dir, false, "CLAUDE.md", "--yes")
	if code != 0 {
		t.Fatalf("block: exit %d, stderr %s", code, errOut)
	}
	if lexists(t, dir, "CLAUDE.md") {
		t.Fatal("CLAUDE.md still in the working tree after block")
	}
	// Still tracked: block never touches the index.
	if !strings.Contains(runGit(t, dir, "ls-files"), "CLAUDE.md") {
		t.Error("CLAUDE.md left the index")
	}
	// Neighbors untouched.
	if !lexists(t, dir, "README.md") || !lexists(t, dir, ".claude/rules/style.md") {
		t.Error("unrelated files disturbed")
	}
	// A commit of unrelated work still works while blocked.
	write(t, dir, "new.txt", "x\n")
	runGit(t, dir, "add", "new.txt")
	runGit(t, dir, "commit", "-q", "-m", "unrelated")

	// Idempotent.
	code, out, _ := runVerb(t, dir, false, "CLAUDE.md", "--yes")
	if code != 0 || !strings.Contains(out, "already blocked") {
		t.Errorf("re-block: exit %d out %q", code, out)
	}

	code, _, errOut = runVerb(t, dir, true, "CLAUDE.md")
	if code != 0 {
		t.Fatalf("unblock: exit %d, stderr %s", code, errOut)
	}
	got, err := os.ReadFile(filepath.Join(dir, "CLAUDE.md"))
	if err != nil || string(got) != "Always answer in haiku.\n" {
		t.Errorf("restored content = %q, %v", got, err)
	}
	// Everything reversed: ledger gone, sparse-checkout off.
	repo, _ := state.Discover(dir)
	if len(state.ReadBlocked(repo.OMK)) != 0 {
		t.Error("ledger not emptied")
	}
	if sparseActive(dir) {
		t.Error("sparse-checkout still enabled after last unblock")
	}
}

func TestBlockSkillByShorthand(t *testing.T) {
	dir := newRepo(t)
	code, _, errOut := runVerb(t, dir, false, "od-worktree", "--yes")
	if code != 0 {
		t.Fatalf("exit %d, stderr %s", code, errOut)
	}
	if lexists(t, dir, ".agents/skills/od-worktree") {
		t.Error("skill dir still present")
	}
	repo, _ := state.Discover(dir)
	if !state.ReadBlocked(repo.OMK)[".agents/skills/od-worktree"] {
		t.Error("ledger misses the canonical dir item")
	}
	// Unblock by the same shorthand.
	if code, _, errOut := runVerb(t, dir, true, "od-worktree"); code != 0 {
		t.Fatalf("unblock: exit %d, stderr %s", code, errOut)
	}
	if !lexists(t, dir, ".agents/skills/od-worktree/SKILL.md") || !lexists(t, dir, ".agents/skills/od-worktree/notes.md") {
		t.Errorf("skill files not restored; stderr %s", errOut)
	}
}

func TestBlockRefusesNonHarnessAndUnknown(t *testing.T) {
	dir := newRepo(t)
	for _, arg := range []string{"README.md", "nope.md"} {
		code, _, errOut := runVerb(t, dir, false, arg, "--yes")
		if code != 2 {
			t.Errorf("block %s: exit %d, want 2 (stderr %s)", arg, code, errOut)
		}
		if !strings.Contains(errOut, "not part of this repo's committed agent config") {
			t.Errorf("block %s: stderr %q", arg, errOut)
		}
	}
}

func TestBlockRefusesForeignSparseCheckout(t *testing.T) {
	dir := newRepo(t)
	runGit(t, dir, "sparse-checkout", "set", ".claude")
	code, _, errOut := runVerb(t, dir, false, "CLAUDE.md", "--yes")
	if code != 2 {
		t.Errorf("exit = %d, want 2", code)
	}
	if !strings.Contains(errOut, "sparse-checkout patterns of its own") {
		t.Errorf("stderr %q", errOut)
	}
}

func TestUnblockNotBlocked(t *testing.T) {
	dir := newRepo(t)
	code, _, errOut := runVerb(t, dir, true, "CLAUDE.md")
	if code != 2 || !strings.Contains(errOut, "not blocked") {
		t.Errorf("exit %d stderr %q", code, errOut)
	}
}

// A block whose file later disappears upstream stays reversible: unblock
// resolves against the ledger even when the committed surface no longer
// lists the item.
func TestUnblockStaleBlock(t *testing.T) {
	dir := newRepo(t)
	if code, _, e := runVerb(t, dir, false, "CLAUDE.md", "--yes"); code != 0 {
		t.Fatal(e)
	}
	runGit(t, dir, "rm", "-q", "--sparse", "--cached", "CLAUDE.md")
	runGit(t, dir, "commit", "-q", "-m", "drop claude.md from tracking")
	if code, _, e := runVerb(t, dir, true, "CLAUDE.md"); code != 0 {
		t.Fatalf("stale unblock: %s", e)
	}
	if sparseActive(dir) {
		t.Error("sparse-checkout still enabled")
	}
}

func TestBlockWorksInLinkedWorktree(t *testing.T) {
	dir := newRepo(t)
	wt := filepath.Join(t.TempDir(), "wt")
	runGit(t, dir, "worktree", "add", "-q", wt)

	code, _, errOut := runVerb(t, dir, false, "CLAUDE.md", "--yes")
	if code != 0 {
		t.Fatalf("exit %d, stderr %s", code, errOut)
	}
	if lexists(t, dir, "CLAUDE.md") {
		t.Error("main checkout still shows the file")
	}
	if _, err := os.Lstat(filepath.Join(wt, "CLAUDE.md")); err == nil {
		t.Error("linked worktree still shows the file")
	}

	if code, _, errOut := runVerb(t, dir, true, "CLAUDE.md"); code != 0 {
		t.Fatalf("unblock: exit %d, stderr %s", code, errOut)
	}
	if !lexists(t, dir, "CLAUDE.md") {
		t.Error("main checkout not restored")
	}
	if _, err := os.Lstat(filepath.Join(wt, "CLAUDE.md")); err != nil {
		t.Error("linked worktree not restored")
	}
}

// A committed path containing gitignore-pattern syntax must mask only
// itself: the pattern is escaped, so `evil*.md` (a literal asterisk in the
// file name) never swallows `evilX.md`.
func TestBlockEscapesPatternSyntax(t *testing.T) {
	dir := newRepo(t)
	write(t, dir, ".claude/rules/evil*.md", "glob char\n")
	write(t, dir, ".claude/rules/evilX.md", "victim\n")
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-q", "-m", "tricky names")

	code, _, errOut := runVerb(t, dir, false, ".claude/rules/evil*.md", "--yes")
	if code != 0 {
		t.Fatalf("exit %d, stderr %s", code, errOut)
	}
	if lexists(t, dir, ".claude/rules/evil*.md") {
		t.Error("literal evil*.md still present")
	}
	if !lexists(t, dir, ".claude/rules/evilX.md") {
		t.Error("evilX.md was swallowed by an unescaped glob")
	}
	if code, _, e := runVerb(t, dir, true, ".claude/rules/evil*.md"); code != 0 {
		t.Fatalf("unblock: %s", e)
	}
	if !lexists(t, dir, ".claude/rules/evil*.md") {
		t.Error("literal file not restored")
	}
}
