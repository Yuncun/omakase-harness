package state

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// AGENTS.md's sha256, hardcoded per the brief: printf '%s' "AGENTS.md" | shasum -a 256
const sha256OfAgentsMDString = "a54ff182c7e8acf56acfd6e4b9c3ff41e2c41a31c9b211b2deb9df75d9a478f9"

// newTestRepo builds a real temp git repo, mirroring the newrepo() fixture
// pattern of tests/placed.test.sh: t.TempDir() + `git init` + the
// user.email/user.name/commit.gpgsign config every repo-scoped test in this
// suite relies on so a commit never blocks on signing/identity.
func newTestRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	runGitT(t, dir, "init", "-q")
	runGitT(t, dir, "config", "user.email", "t@t")
	runGitT(t, dir, "config", "user.name", "t")
	runGitT(t, dir, "config", "commit.gpgsign", "false")
	return dir
}

func runGitT(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

func writeFile(t *testing.T, dir, rel, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, rel), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// ---------------------------------------------------------------- Discover

func TestDiscover(t *testing.T) {
	dir := newTestRepo(t)

	repo, err := Discover(dir)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}

	wantRoot, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	gotRoot, err := filepath.EvalSymlinks(repo.Root)
	if err != nil {
		t.Fatal(err)
	}
	if gotRoot != wantRoot {
		t.Errorf("Root = %q, want %q", repo.Root, dir)
	}
	if repo.CommonDir != filepath.Join(gotRoot, ".git") {
		t.Errorf("CommonDir = %q, want %q", repo.CommonDir, filepath.Join(gotRoot, ".git"))
	}
	if repo.OMK != filepath.Join(repo.CommonDir, "omakase") {
		t.Errorf("OMK = %q, want %q", repo.OMK, filepath.Join(repo.CommonDir, "omakase"))
	}
}

func TestDiscoverNotARepo(t *testing.T) {
	dir := t.TempDir()
	if _, err := Discover(dir); err == nil {
		t.Error("Discover outside a git repo: want error, got nil")
	}
}

// ---------------------------------------------------------------- HashOf

func TestHashOfSymlink(t *testing.T) {
	dir := t.TempDir()
	link := filepath.Join(dir, "CLAUDE.md")
	if err := os.Symlink("AGENTS.md", link); err != nil {
		t.Fatal(err)
	}
	if got := HashOf(link); got != sha256OfAgentsMDString {
		t.Errorf("HashOf(symlink) = %q, want %q", got, sha256OfAgentsMDString)
	}
}

func TestHashOfRegularFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "AGENTS.md")
	writeFile(t, dir, "AGENTS.md", "AGENTS.md")
	if got := HashOf(p); got != sha256OfAgentsMDString {
		t.Errorf("HashOf(file) = %q, want %q", got, sha256OfAgentsMDString)
	}
}

func TestHashOfMissing(t *testing.T) {
	dir := t.TempDir()
	if got := HashOf(filepath.Join(dir, "nope")); got != "" {
		t.Errorf("HashOf(missing) = %q, want \"\"", got)
	}
}

// ---------------------------------------------------------------- IsDrifted

func TestIsDrifted(t *testing.T) {
	dir := newTestRepo(t)

	// tracked.txt: committed, then modified in the worktree. Tracked files
	// are never drifted — upstream (git) owns them.
	writeFile(t, dir, "tracked.txt", "original\n")
	trackedLedgerHash := HashOf(filepath.Join(dir, "tracked.txt")) // hash at "placement" time
	runGitT(t, dir, "add", "tracked.txt")
	runGitT(t, dir, "commit", "-q", "-m", "init")
	writeFile(t, dir, "tracked.txt", "modified\n")

	// untracked.txt: never committed, modified after its ledger hash was recorded.
	writeFile(t, dir, "untracked.txt", "original\n")
	untrackedLedgerHash := HashOf(filepath.Join(dir, "untracked.txt"))
	writeFile(t, dir, "untracked.txt", "modified\n")

	cases := []struct {
		name       string
		rel        string
		ledgerHash string
		enabled    string
		want       bool
	}{
		{"tracked file modified in worktree: not drifted, git owns it", "tracked.txt", trackedLedgerHash, "1", false},
		{"untracked file modified: drifted", "untracked.txt", untrackedLedgerHash, "1", true},
		{"disabled (0): never drifted", "untracked.txt", untrackedLedgerHash, "0", false},
		{"disabled (empty): never drifted", "untracked.txt", untrackedLedgerHash, "", false},
		{"missing file: not drifted (its own state)", "missing.txt", "deadbeef", "1", false},
		{"empty ledger hash: not drifted", "untracked.txt", "", "1", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsDrifted(dir, tc.rel, tc.ledgerHash, tc.enabled); got != tc.want {
				t.Errorf("IsDrifted(%q, ledgerHash=%q, enabled=%q) = %v, want %v", tc.rel, tc.ledgerHash, tc.enabled, got, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------- ReadPlaced

func TestReadPlaced(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "placed.tsv")
	content := "rel1\tkind1\tsrc1\thash1\t1\n" +
		"rel2\tkind2\tsrc2\thash2\t1\textra\n" + // 6th tab absorbed into Enabled
		"rel3\tkind3\n" + // short row: Src/Hash/Enabled all empty
		"\tkind4\tsrc4\thash4\t1\n" // empty Rel: dropped
	writeFile(t, dir, "placed.tsv", content)

	got := ReadPlaced(p)
	want := []PlacedRow{
		{Rel: "rel1", Kind: "kind1", Src: "src1", Hash: "hash1", Enabled: "1"},
		{Rel: "rel2", Kind: "kind2", Src: "src2", Hash: "hash2", Enabled: "1\textra"},
		{Rel: "rel3", Kind: "kind3", Src: "", Hash: "", Enabled: ""},
	}

	if len(got) != len(want) {
		t.Fatalf("ReadPlaced: got %d rows, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("row %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestReadPlacedMissing(t *testing.T) {
	dir := t.TempDir()
	if got := ReadPlaced(filepath.Join(dir, "nope.tsv")); got != nil {
		t.Errorf("ReadPlaced(missing) = %+v, want nil", got)
	}
}

// ---------------------------------------------------------------- CountNonEmptyLines

func TestCountNonEmptyLines(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "f.txt")
	// two non-empty lines, one empty line, final line has no trailing newline.
	writeFile(t, dir, "f.txt", "a\n\nb")
	if got := CountNonEmptyLines(p); got != 2 {
		t.Errorf("CountNonEmptyLines = %d, want 2", got)
	}
}

func TestCountNonEmptyLinesMissing(t *testing.T) {
	dir := t.TempDir()
	if got := CountNonEmptyLines(filepath.Join(dir, "nope")); got != 0 {
		t.Errorf("CountNonEmptyLines(missing) = %d, want 0", got)
	}
}

// ---------------------------------------------------------------- LatestVerdicts

func TestLatestVerdicts(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "ledger.tsv")
	content := "100\tgateA\tpass\tsha1\n" +
		"100\tgateA\tfail\tsha2\n" + // equal epoch: later row still wins (ts>=seen)
		"50\tgateA\tpass\tsha3\n" + // earlier epoch: must not override
		"abc\tgateB\tpass\tsha4\n" + // non-numeric epoch: dropped
		"200\tgateB\tpass\tsha5\n" +
		"10\tgateC\n" // NF<4: dropped
	writeFile(t, dir, "ledger.tsv", content)

	got := LatestVerdicts(p)

	want := map[string]Verdict{
		"gateA": {Epoch: 100, Verdict: "fail"},
		"gateB": {Epoch: 200, Verdict: "pass"},
	}
	if len(got) != len(want) {
		t.Fatalf("LatestVerdicts: got %d gates, want %d: %+v", len(got), len(want), got)
	}
	for gate, wantV := range want {
		if gotV, ok := got[gate]; !ok || gotV != wantV {
			t.Errorf("LatestVerdicts[%q] = %+v (ok=%v), want %+v", gate, gotV, ok, wantV)
		}
	}
}

func TestLatestVerdictsMissing(t *testing.T) {
	dir := t.TempDir()
	got := LatestVerdicts(filepath.Join(dir, "nope.tsv"))
	if len(got) != 0 {
		t.Errorf("LatestVerdicts(missing) = %+v, want empty map", got)
	}
}

// ---------------------------------------------------------------- FirstLine

func TestFirstLine(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "source")
	writeFile(t, dir, "source", "owner/repo\nsecond line\n")
	if got := FirstLine(p); got != "owner/repo" {
		t.Errorf("FirstLine = %q, want %q", got, "owner/repo")
	}
}

func TestFirstLineEmptyFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "empty")
	writeFile(t, dir, "empty", "")
	if got := FirstLine(p); got != "" {
		t.Errorf("FirstLine(empty) = %q, want \"\"", got)
	}
}

func TestFirstLineMissing(t *testing.T) {
	dir := t.TempDir()
	if got := FirstLine(filepath.Join(dir, "nope")); got != "" {
		t.Errorf("FirstLine(missing) = %q, want \"\"", got)
	}
}
