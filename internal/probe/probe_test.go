package probe

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Yuncun/omakase-harness/internal/state"
)

// newTestRepo builds a real temp git repo with the identity/signing config
// every repo-scoped test relies on, plus one commit so HEAD exists.
func newTestRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	runGit(t, dir, "init", "-q", "-b", "main")
	runGit(t, dir, "config", "user.email", "t@t")
	runGit(t, dir, "config", "user.name", "t")
	runGit(t, dir, "config", "commit.gpgsign", "false")
	writeFile(t, dir, "README.md", "hi\n")
	runGit(t, dir, "add", "README.md")
	runGit(t, dir, "commit", "-q", "-m", "init")
	return dir
}

func runGit(t *testing.T, dir string, args ...string) string {
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
	full := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// installHarness gives the repo a minimal installed overlay: two enabled
// placed files with correct ledger hashes, an armed lefthook pre-commit
// stub in the shared hooks dir, and the identity files. Returns the OMK dir.
func installHarness(t *testing.T, root string) string {
	t.Helper()
	omk := filepath.Join(root, ".git", "omakase")
	if err := os.MkdirAll(omk, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, root, ".omakase/VERSION", "0.18.1\n")
	writeFile(t, root, ".omakase/bin/omakase-gate.sh", "#!/bin/sh\n")
	rows := []state.PlacedRow{
		{Rel: ".omakase/VERSION", Kind: "other", Src: "payload", Hash: state.HashOf(filepath.Join(root, ".omakase/VERSION")), Enabled: "1"},
		{Rel: ".omakase/bin/omakase-gate.sh", Kind: "other", Src: "payload", Hash: state.HashOf(filepath.Join(root, ".omakase/bin/omakase-gate.sh")), Enabled: "1"},
	}
	if err := state.WritePlaced(filepath.Join(omk, "placed.tsv"), rows); err != nil {
		t.Fatal(err)
	}
	armRepo(t, root)
	return omk
}

// armRepo writes a lefthook-managed pre-commit stub into the repo's shared
// hooks dir (what `lefthook install` leaves behind).
func armRepo(t *testing.T, root string) {
	t.Helper()
	writeFile(t, root, ".git/hooks/pre-commit", "#!/bin/sh\n# lefthook\ncall_lefthook \"$@\"\n")
}

func collect(t *testing.T, cwd string) *State {
	t.Helper()
	st, err := Collect(cwd)
	if err != nil {
		t.Fatalf("Collect(%s): %v", cwd, err)
	}
	return st
}

// ---------------------------------------------------------------- discovery

func TestCollectOutsideRepoErrors(t *testing.T) {
	if _, err := Collect(t.TempDir()); err == nil {
		t.Fatal("Collect outside a git repo: want error, got nil")
	}
}

func TestCollectNotInstalled(t *testing.T) {
	root := newTestRepo(t)
	st := collect(t, root)
	if st.Installed {
		t.Fatal("Installed: want false in a repo with no placed.tsv")
	}
	if st.Root == "" || st.OMK == "" {
		t.Fatalf("Root/OMK must still be set: %q %q", st.Root, st.OMK)
	}
}

// ---------------------------------------------------------------- identity

func TestCollectIdentityFacts(t *testing.T) {
	root := newTestRepo(t)
	omk := installHarness(t, root)
	writeFile(t, root, filepath.Join(".git", "omakase", "source"), "github.com/acme/team-harness\n")
	_ = omk
	writeFile(t, root, ".omakase/NAME", "acme\n")

	st := collect(t, root)
	if !st.Installed {
		t.Fatal("Installed: want true")
	}
	if got, want := st.Project, filepath.Base(st.Root); got != want {
		t.Fatalf("Project = %q, want %q", got, want)
	}
	if st.Branch != "main" {
		t.Fatalf("Branch = %q, want main", st.Branch)
	}
	if st.Worktree != "" {
		t.Fatalf("Worktree = %q, want empty in the main checkout", st.Worktree)
	}
	if st.Source != "github.com/acme/team-harness" {
		t.Fatalf("Source = %q", st.Source)
	}
	if st.NameOverride != "acme" {
		t.Fatalf("NameOverride = %q", st.NameOverride)
	}
	if st.BaseVersion != "0.18.1" {
		t.Fatalf("BaseVersion = %q", st.BaseVersion)
	}
}

func TestCollectNameEnvBeatsFile(t *testing.T) {
	root := newTestRepo(t)
	installHarness(t, root)
	writeFile(t, root, ".omakase/NAME", "fromfile\n")
	t.Setenv("OMAKASE_NAME", "fromenv")
	if st := collect(t, root); st.NameOverride != "fromenv" {
		t.Fatalf("NameOverride = %q, want fromenv", st.NameOverride)
	}
}

func TestCollectUnbornBranchStillNamed(t *testing.T) {
	dir := t.TempDir()
	runGit(t, dir, "init", "-q", "-b", "main") // no commit: HEAD is unborn
	installHarness(t, dir)
	if st := collect(t, dir); st.Branch != "main" {
		t.Fatalf("Branch on an unborn HEAD = %q, want main", st.Branch)
	}
}

func TestCollectDetachedHeadBranchIsShortSha(t *testing.T) {
	root := newTestRepo(t)
	installHarness(t, root)
	runGit(t, root, "checkout", "-q", "--detach")
	st := collect(t, root)
	if st.Branch == "" || st.Branch == "HEAD" {
		t.Fatalf("Branch on detached HEAD = %q, want a short sha", st.Branch)
	}
}

// ---------------------------------------------------------------- proofs

func TestCollectAllProven(t *testing.T) {
	root := newTestRepo(t)
	installHarness(t, root)
	st := collect(t, root)
	if st.Armed != OK {
		t.Fatalf("Armed = %v, want OK", st.Armed)
	}
	if st.FilesPresent != OK || st.Missing != 0 {
		t.Fatalf("FilesPresent = %v (missing %d), want OK/0", st.FilesPresent, st.Missing)
	}
	if st.HashesMatch != OK || st.Drifted != 0 {
		t.Fatalf("HashesMatch = %v (drifted %d), want OK/0", st.HashesMatch, st.Drifted)
	}
}

func TestCollectNotArmedWhenNoStub(t *testing.T) {
	root := newTestRepo(t)
	installHarness(t, root)
	os.Remove(filepath.Join(root, ".git", "hooks", "pre-commit"))
	if st := collect(t, root); st.Armed != Problem {
		t.Fatalf("Armed = %v, want Problem with no hook stub", st.Armed)
	}
}

func TestCollectNotArmedWhenStubIsForeign(t *testing.T) {
	root := newTestRepo(t)
	installHarness(t, root)
	writeFile(t, root, ".git/hooks/pre-commit", "#!/bin/sh\n# husky\n")
	if st := collect(t, root); st.Armed != Problem {
		t.Fatalf("Armed = %v, want Problem with a foreign stub", st.Armed)
	}
}

func TestCollectArmedHonorsCoreHooksPath(t *testing.T) {
	root := newTestRepo(t)
	installHarness(t, root)
	// Move the effective hooks dir away: the default-dir stub no longer
	// counts, and a lefthook stub in the configured dir does.
	hooks := filepath.Join(root, "custom-hooks")
	if err := os.MkdirAll(hooks, 0o755); err != nil {
		t.Fatal(err)
	}
	runGit(t, root, "config", "core.hooksPath", hooks)
	if st := collect(t, root); st.Armed != Problem {
		t.Fatalf("Armed = %v, want Problem (stub lives outside the effective dir)", st.Armed)
	}
	writeFile(t, root, "custom-hooks/pre-push", "#!/bin/sh\nlefthook run pre-push\n")
	if st := collect(t, root); st.Armed != OK {
		t.Fatalf("Armed = %v, want OK via core.hooksPath", st.Armed)
	}
}

func TestCollectMissingFile(t *testing.T) {
	root := newTestRepo(t)
	installHarness(t, root)
	os.Remove(filepath.Join(root, ".omakase", "bin", "omakase-gate.sh"))
	st := collect(t, root)
	if st.FilesPresent != Problem || st.Missing != 1 {
		t.Fatalf("FilesPresent = %v missing=%d, want Problem/1", st.FilesPresent, st.Missing)
	}
}

func TestCollectDriftedFile(t *testing.T) {
	root := newTestRepo(t)
	installHarness(t, root)
	writeFile(t, root, ".omakase/bin/omakase-gate.sh", "#!/bin/sh\n# edited\n")
	st := collect(t, root)
	if st.HashesMatch != Problem || st.Drifted != 1 {
		t.Fatalf("HashesMatch = %v drifted=%d, want Problem/1", st.HashesMatch, st.Drifted)
	}
}

func TestCollectTrackedFileNeverDrifts(t *testing.T) {
	root := newTestRepo(t)
	installHarness(t, root)
	writeFile(t, root, ".omakase/bin/omakase-gate.sh", "#!/bin/sh\n# edited\n")
	runGit(t, root, "add", "-f", ".omakase/bin/omakase-gate.sh")
	runGit(t, root, "commit", "-q", "-m", "track it")
	if st := collect(t, root); st.HashesMatch != OK {
		t.Fatalf("HashesMatch = %v, want OK for a tracked path", st.HashesMatch)
	}
}

func TestCollectDisabledRowIgnored(t *testing.T) {
	root := newTestRepo(t)
	omk := installHarness(t, root)
	rows := state.ReadPlaced(filepath.Join(omk, "placed.tsv"))
	rows[1].Enabled = "0"
	if err := state.WritePlaced(filepath.Join(omk, "placed.tsv"), rows); err != nil {
		t.Fatal(err)
	}
	os.Remove(filepath.Join(root, ".omakase", "bin", "omakase-gate.sh"))
	st := collect(t, root)
	if st.FilesPresent != OK || st.Missing != 0 {
		t.Fatalf("FilesPresent = %v missing=%d, want OK/0 (row disabled)", st.FilesPresent, st.Missing)
	}
}

func TestCollectEmptyLedgerHashSkipsDrift(t *testing.T) {
	root := newTestRepo(t)
	omk := installHarness(t, root)
	// A short row (no hash field) comes back from ReadPlaced with Hash "";
	// drift cannot be judged without a ledger hash, so the row is skipped.
	raw := ".omakase/VERSION\tother\tpayload\t" + state.HashOf(filepath.Join(root, ".omakase/VERSION")) + "\t1\n" +
		".omakase/bin/omakase-gate.sh\tother\tpayload\n"
	if err := os.WriteFile(filepath.Join(omk, "placed.tsv"), []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	writeFile(t, root, ".omakase/bin/omakase-gate.sh", "#!/bin/sh\n# edited\n")
	if st := collect(t, root); st.HashesMatch != OK {
		t.Fatalf("HashesMatch = %v, want OK when the ledger row has no real hash", st.HashesMatch)
	}
}

// ---------------------------------------------------------------- worktrees

func TestCollectWorktreeFacts(t *testing.T) {
	root := newTestRepo(t)
	installHarness(t, root)
	wt := filepath.Join(t.TempDir(), "feature-x")
	runGit(t, root, "worktree", "add", "-q", "-b", "feature-x", wt)
	// The overlay files exist per worktree; mirror them so presence holds.
	writeFile(t, wt, ".omakase/VERSION", "0.18.1\n")
	writeFile(t, wt, ".omakase/bin/omakase-gate.sh", "#!/bin/sh\n")

	main := collect(t, root)
	if !main.MainCheckout || main.WorktreeCount != 2 {
		t.Fatalf("main checkout: MainCheckout=%v count=%d, want true/2", main.MainCheckout, main.WorktreeCount)
	}
	if main.Worktree != "" {
		t.Fatalf("main checkout Worktree = %q, want empty", main.Worktree)
	}

	linked := collect(t, wt)
	if linked.MainCheckout {
		t.Fatal("linked worktree reported as main checkout")
	}
	if linked.Worktree != "feature-x" {
		t.Fatalf("Worktree = %q, want feature-x", linked.Worktree)
	}
	if linked.Branch != "feature-x" {
		t.Fatalf("Branch = %q, want feature-x", linked.Branch)
	}
	if linked.Armed != OK {
		t.Fatalf("Armed = %v in linked worktree, want OK (hooks are shared)", linked.Armed)
	}
	if got, want := linked.Project, filepath.Base(main.Root); got != want {
		t.Fatalf("Project in worktree = %q, want main-root basename %q", got, want)
	}
}

func TestCollectDisciplineStanddowns(t *testing.T) {
	root := newTestRepo(t)
	omk := installHarness(t, root)
	if st := collect(t, root); st.DisciplineOff {
		t.Fatal("DisciplineOff: want false by default")
	}
	writeFile(t, root, filepath.Join(".git", "omakase", "disabled-gates"), "worktree-discipline\n")
	_ = omk
	if st := collect(t, root); !st.DisciplineOff {
		t.Fatal("DisciplineOff: want true with a disabled-gates line")
	}
	os.Remove(filepath.Join(root, ".git", "omakase", "disabled-gates"))
	t.Setenv("OMAKASE_SKIP_WORKTREE_DISCIPLINE", "1")
	if st := collect(t, root); !st.DisciplineOff {
		t.Fatal("DisciplineOff: want true with the skip env set")
	}
}

// ---------------------------------------------------------------- last run

func TestCollectLastRun(t *testing.T) {
	root := newTestRepo(t)
	omk := installHarness(t, root)
	ledger := strings.Join([]string{
		"100\tsmoke\tfail\t", // empty sha (unborn HEAD): ignored entirely
		"200\tsmoke\tfail\tabc",
		"210\tsmoke\tpass\tabc", // retry: latest verdict per gate wins
		"205\tlint\tfail\tabc",
		"150\told\tpass\tdef", // older run, different sha: not the last run
	}, "\n") + "\n"
	writeFile(t, root, filepath.Join(".git", "omakase", "ledger.tsv"), ledger)
	_ = omk

	st := collect(t, root)
	if st.LastRun == nil {
		t.Fatal("LastRun: want a summary, got nil")
	}
	if st.LastRun.Checks != 2 || st.LastRun.Failed != 1 || st.LastRun.Epoch != 210 {
		t.Fatalf("LastRun = %+v, want checks=2 failed=1 epoch=210", *st.LastRun)
	}
}

func TestCollectLastRunNilWithoutRealRuns(t *testing.T) {
	root := newTestRepo(t)
	installHarness(t, root)
	writeFile(t, root, filepath.Join(".git", "omakase", "ledger.tsv"), "100\tsmoke\tpass\t\n")
	if st := collect(t, root); st.LastRun != nil {
		t.Fatalf("LastRun = %+v, want nil when every row lacks a sha", *st.LastRun)
	}
}
