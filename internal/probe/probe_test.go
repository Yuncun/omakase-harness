package probe

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Yuncun/omakase-harness/internal/hook"
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
// placed files with correct ledger hashes, the gate-hook dispatchers in the
// shared hooks dir with a stable binary copy behind them, and the identity
// files. Returns the OMK dir.
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
	installDispatchers(t, filepath.Join(root, ".git", "hooks"))
	stubStableBin(t)
	return omk
}

// installDispatchers writes the two gate-hook dispatchers (what `omakase
// init` leaves behind) into hooksDir.
func installDispatchers(t *testing.T, hooksDir string) {
	t.Helper()
	for _, h := range []string{"pre-commit", "pre-push"} {
		if err := hook.Write(hooksDir, h); err != nil {
			t.Fatal(err)
		}
	}
}

// stubStableBin points XDG_CACHE_HOME at a temp dir holding an executable
// file at the stable binary path the dispatchers exec.
func stubStableBin(t *testing.T) {
	t.Helper()
	cache := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cache)
	bin := filepath.Join(cache, "omakase", "bin", "current", "omakase")
	if err := os.MkdirAll(filepath.Dir(bin), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
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
	if st.HooksInstalled != OK {
		t.Fatalf("HooksInstalled = %v, want OK", st.HooksInstalled)
	}
	if st.HookIssue != HookIssueNone {
		t.Fatalf("HookIssue = %v, want HookIssueNone", st.HookIssue)
	}
	if st.FilesPresent != OK {
		t.Fatalf("FilesPresent = %v, want OK", st.FilesPresent)
	}
	if st.HashesMatch != OK {
		t.Fatalf("HashesMatch = %v, want OK", st.HashesMatch)
	}
}

func TestCollectHooksAbsent(t *testing.T) {
	root := newTestRepo(t)
	installHarness(t, root)
	os.Remove(filepath.Join(root, ".git", "hooks", "pre-commit"))
	st := collect(t, root)
	if st.HooksInstalled != Problem {
		t.Fatalf("HooksInstalled = %v, want Problem with a missing dispatcher", st.HooksInstalled)
	}
	if st.HookIssue != HookIssueAbsent {
		t.Fatalf("HookIssue = %v, want HookIssueAbsent", st.HookIssue)
	}
}

// A hook file that is not byte-equal to the dispatcher is clobbered —
// `lefthook install -f` from an npm postinstall, a foreign manager, or a
// pre-#98 omakase stub awaiting its migration init. Substring matching
// would call these healthy; byte equality is the proof.
func TestCollectHooksForeign(t *testing.T) {
	for name, content := range map[string]string{
		"husky":              "#!/bin/sh\n# husky\n",
		"lefthook stub":      "#!/bin/sh\n# lefthook\ncall_lefthook \"$@\"\n",
		"edited dispatcher":  string(hook.Dispatcher("pre-commit")) + "# extra line\n",
		"wrong-name content": string(hook.Dispatcher("pre-push")),
	} {
		t.Run(name, func(t *testing.T) {
			root := newTestRepo(t)
			installHarness(t, root)
			writeFile(t, root, ".git/hooks/pre-commit", content)
			st := collect(t, root)
			if st.HooksInstalled != Problem {
				t.Fatalf("HooksInstalled = %v, want Problem", st.HooksInstalled)
			}
			if st.HookIssue != HookIssueForeign {
				t.Fatalf("HookIssue = %v, want HookIssueForeign", st.HookIssue)
			}
		})
	}
}

// Dispatchers intact but their binary target gone: every commit would block
// (fail closed), so status must warn before the user hits that wall.
func TestCollectHooksBinaryMissing(t *testing.T) {
	root := newTestRepo(t)
	installHarness(t, root)
	if err := os.Remove(filepath.Join(os.Getenv("XDG_CACHE_HOME"), "omakase", "bin", "current", "omakase")); err != nil {
		t.Fatal(err)
	}
	st := collect(t, root)
	if st.HooksInstalled != Problem {
		t.Fatalf("HooksInstalled = %v, want Problem with the stable binary gone", st.HooksInstalled)
	}
	if st.HookIssue != HookIssueBinary {
		t.Fatalf("HookIssue = %v, want HookIssueBinary", st.HookIssue)
	}
}

func TestCollectHooksInstalledHonorsCoreHooksPath(t *testing.T) {
	root := newTestRepo(t)
	installHarness(t, root)
	// Move the effective hooks dir away: the default-dir dispatchers no
	// longer count, and dispatchers in the configured dir do.
	hooks := filepath.Join(root, "custom-hooks")
	if err := os.MkdirAll(hooks, 0o755); err != nil {
		t.Fatal(err)
	}
	runGit(t, root, "config", "core.hooksPath", hooks)
	if st := collect(t, root); st.HooksInstalled != Problem {
		t.Fatalf("HooksInstalled = %v, want Problem (dispatchers live outside the effective dir)", st.HooksInstalled)
	}
	installDispatchers(t, hooks)
	if st := collect(t, root); st.HooksInstalled != OK {
		t.Fatalf("HooksInstalled = %v, want OK via core.hooksPath", st.HooksInstalled)
	}
}

func TestCollectMissingFile(t *testing.T) {
	root := newTestRepo(t)
	installHarness(t, root)
	os.Remove(filepath.Join(root, ".omakase", "bin", "omakase-gate.sh"))
	st := collect(t, root)
	if st.FilesPresent != Problem {
		t.Fatalf("FilesPresent = %v, want Problem", st.FilesPresent)
	}
}

func TestCollectDriftedFile(t *testing.T) {
	root := newTestRepo(t)
	installHarness(t, root)
	writeFile(t, root, ".omakase/bin/omakase-gate.sh", "#!/bin/sh\n# edited\n")
	st := collect(t, root)
	if st.HashesMatch != Problem {
		t.Fatalf("HashesMatch = %v, want Problem", st.HashesMatch)
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
	if st.FilesPresent != OK {
		t.Fatalf("FilesPresent = %v, want OK (row disabled)", st.FilesPresent)
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

// A linked worktree reports the project it belongs to (the main root's
// basename, not its own folder), its own branch, and the shared hooks.
func TestCollectFromALinkedWorktree(t *testing.T) {
	root := newTestRepo(t)
	installHarness(t, root)
	wt := filepath.Join(t.TempDir(), "feature-x")
	runGit(t, root, "worktree", "add", "-q", "-b", "feature-x", wt)
	// The overlay files exist per worktree; mirror them so presence holds.
	writeFile(t, wt, ".omakase/VERSION", "0.18.1\n")
	writeFile(t, wt, ".omakase/bin/omakase-gate.sh", "#!/bin/sh\n")

	main := collect(t, root)
	linked := collect(t, wt)
	if linked.Branch != "feature-x" {
		t.Fatalf("Branch = %q, want feature-x", linked.Branch)
	}
	if linked.HooksInstalled != OK {
		t.Fatalf("HooksInstalled = %v in linked worktree, want OK (hooks are shared)", linked.HooksInstalled)
	}
	if got, want := linked.Project, filepath.Base(main.Root); got != want {
		t.Fatalf("Project in worktree = %q, want main-root basename %q", got, want)
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

// A kept row (an $OMK/kept accepted copy behind an enabled row) is a fact,
// not a problem: Kept counts it, and because keep moved the ledger hash to
// the accepted version, the proofs stay green. Disabled rows and rows
// without a kept copy don't count.
func TestCollectKeptCount(t *testing.T) {
	dir := newTestRepo(t)
	omk := installHarness(t, dir)

	// Edit + keep .omakase/VERSION: accepted copy recorded, ledger hash moved.
	writeFile(t, dir, ".omakase/VERSION", "0.18.1\nedited\n")
	writeFile(t, filepath.Join(omk, "kept"), ".omakase/VERSION", "0.18.1\nedited\n")
	ledger := filepath.Join(omk, "placed.tsv")
	rows := state.ReadPlaced(ledger)
	rows[0].Hash = state.HashOf(filepath.Join(dir, ".omakase/VERSION"))
	if err := state.WritePlaced(ledger, rows); err != nil {
		t.Fatal(err)
	}

	st := collect(t, dir)
	if st.Kept != 1 {
		t.Errorf("Kept = %d, want 1", st.Kept)
	}
	if st.FilesPresent != OK || st.HashesMatch != OK {
		t.Errorf("kept row broke the proofs: present=%v hashes=%v", st.FilesPresent, st.HashesMatch)
	}

	// A kept copy behind a DISABLED row does not count.
	rows = state.ReadPlaced(ledger)
	rows[0].Enabled = "0"
	if err := state.WritePlaced(ledger, rows); err != nil {
		t.Fatal(err)
	}
	if st := collect(t, dir); st.Kept != 0 {
		t.Errorf("disabled row counted as kept: Kept = %d, want 0", st.Kept)
	}
}
