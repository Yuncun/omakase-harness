package overlay

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Yuncun/omakase-harness/internal/state"
)

// RunHook integration tests, against real temp git repos. Installed state
// (placed.tsv + snapshot + snapshot manifest) is assembled by hand rather than
// via RunInit so the runner's contract is pinned independently of init's.

// hookRepo builds an empty-committed repo with an installed-looking harness:
// an enabled placed gate script (.omakase/gates/example.sh, executable) plus an
// enabled placed omakase.manifest declaring a pre-commit gate that runs it, all
// snapshotted. Returns the repo.
func hookRepo(t *testing.T) *state.Repo {
	t.Helper()
	_, repo := initRepo(t)
	installState(t, repo, gateContent)
	return repo
}

// defaultManifest declares one pre-commit gate that runs the placed example
// script — the gate whose pass/fail the runner tests drive.
const defaultManifest = "name: t\nversion: 1\n\ngate: example\n  hook: pre-commit\n  run: .omakase/gates/example.sh\n"

// installState writes the minimal installed harness: the example gate script
// (executable) + its snapshot, the omakase.manifest + its snapshot, and a
// placed.tsv with an enabled row for each.
func installState(t *testing.T, repo *state.Repo, content string) {
	t.Helper()
	rel := filepath.Join(".omakase", "gates", "example.sh")
	writeFile(t, filepath.Join(repo.Root, rel), content)
	if err := os.Chmod(filepath.Join(repo.Root, rel), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(repo.OMK, "payload-snapshot", rel), content)
	setManifest(t, repo, defaultManifest)
	row := ".omakase/gates/example.sh\tgate\tpayload\t" + sha256hex([]byte(content)) + "\t1\n" +
		"omakase.manifest\tgate\tpayload\t" + sha256hex([]byte(defaultManifest)) + "\t1\n"
	writeFile(t, filepath.Join(repo.OMK, "placed.tsv"), row)
	writeFile(t, filepath.Join(repo.Root, "omakase.manifest"), defaultManifest)
}

// setManifest (re)writes the snapshot manifest gate.RunHook reads.
func setManifest(t *testing.T, repo *state.Repo, content string) {
	t.Helper()
	writeFile(t, filepath.Join(repo.OMK, "payload-snapshot", "omakase.manifest"), content)
}

// fakeGitLFS puts a logging git-lfs first on PATH and returns its log path.
func fakeGitLFS(t *testing.T, exit string) string {
	t.Helper()
	dir := t.TempDir()
	log := filepath.Join(dir, "lfs.log")
	writeFile(t, filepath.Join(dir, "git-lfs"), "#!/bin/sh\necho \"$@\" >> \""+log+"\"\nexit "+exit+"\n")
	if err := os.Chmod(filepath.Join(dir, "git-lfs"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return log
}

func TestHookUnknownNameUsage(t *testing.T) {
	var out, errb strings.Builder
	if code := RunHook([]string{"commit-msg"}, strings.NewReader(""), &out, &errb); code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if !strings.Contains(errb.String(), "usage: omakase hook") {
		t.Errorf("stderr = %q, want usage line", errb.String())
	}
	if code := RunHook(nil, strings.NewReader(""), &out, &errb); code != 2 {
		t.Fatalf("no-arg exit = %d, want 2", code)
	}
}

func TestHookGateBlocksWhenNotInstalled(t *testing.T) {
	initRepo(t)
	var out, errb strings.Builder
	if code := RunHook([]string{"pre-commit"}, strings.NewReader(""), &out, &errb); code != 1 {
		t.Fatalf("exit = %d, want 1; stderr=%q", code, errb.String())
	}
	if !strings.Contains(errb.String(), "BLOCKING") || !strings.Contains(errb.String(), "omakase init") {
		t.Errorf("stderr = %q, want BLOCKING + fix line", errb.String())
	}
}

func TestHookPostCheckoutExitsZeroWhenNotInstalled(t *testing.T) {
	initRepo(t)
	var out, errb strings.Builder
	if code := RunHook([]string{"post-checkout"}, strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%q", code, errb.String())
	}
	eq(t, "stderr", errb.String(), "")
}

// A clean pre-commit runs the declared gate and records a pass row.
func TestHookGateRunsAndRecords(t *testing.T) {
	repo := hookRepo(t)
	var out, errb strings.Builder
	if code := RunHook([]string{"pre-commit"}, strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%q", code, errb.String())
	}
	led := readFileT(t, filepath.Join(repo.OMK, "ledger.tsv"))
	if !strings.Contains(led, "\texample\tpass\t") {
		t.Errorf("ledger missing the example pass row: %q", led)
	}
}

func TestHookGateBlocksOnMissingPlacedFile(t *testing.T) {
	repo := hookRepo(t)
	if err := os.Remove(filepath.Join(repo.Root, ".omakase", "gates", "example.sh")); err != nil {
		t.Fatal(err)
	}
	var out, errb strings.Builder
	if code := RunHook([]string{"pre-commit"}, strings.NewReader(""), &out, &errb); code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	if !strings.Contains(errb.String(), "missing: .omakase/gates/example.sh") {
		t.Errorf("stderr = %q, want the missing path named", errb.String())
	}
	if !strings.Contains(errb.String(), "omakase init") {
		t.Errorf("stderr = %q, want the restore instruction", errb.String())
	}
}

// A missing DISABLED row is deliberately absent — never a block; the enabled
// gates still run.
func TestHookGateIgnoresDisabledRows(t *testing.T) {
	repo := hookRepo(t)
	// Disable + remove the example script; declare a separate passing gate.
	rows := readFileT(t, filepath.Join(repo.OMK, "placed.tsv"))
	writeFile(t, filepath.Join(repo.OMK, "placed.tsv"), strings.Replace(rows, ".omakase/gates/example.sh\tgate\tpayload\t"+sha256hex([]byte(gateContent))+"\t1\n", ".omakase/gates/example.sh\tgate\tpayload\t"+sha256hex([]byte(gateContent))+"\t0\n", 1))
	if err := os.Remove(filepath.Join(repo.Root, ".omakase", "gates", "example.sh")); err != nil {
		t.Fatal(err)
	}
	setManifest(t, repo, "gate: ok\n  hook: pre-commit\n  run: true\n")
	var out, errb strings.Builder
	if code := RunHook([]string{"pre-commit"}, strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%q", code, errb.String())
	}
	if !strings.Contains(readFileT(t, filepath.Join(repo.OMK, "ledger.tsv")), "\tok\tpass\t") {
		t.Error("the enabled gate did not run")
	}
}

// GIT_INDEX_FILE must SURVIVE the env scrub: git points it at the temporary
// index during partial commits, and the gate must see that staged set. Only
// GIT_DIR/GIT_WORK_TREE/GIT_COMMON_DIR are scrubbed.
func TestHookKeepsGitIndexFile(t *testing.T) {
	repo := hookRepo(t)
	out := filepath.Join(t.TempDir(), "seen")
	setManifest(t, repo, "gate: idx\n  hook: pre-commit\n  run: printf '%s' \"$GIT_INDEX_FILE\" > "+out+"\n")
	t.Setenv("GIT_INDEX_FILE", "/tmp/sentinel-index")
	var o, errb strings.Builder
	if code := RunHook([]string{"pre-commit"}, strings.NewReader(""), &o, &errb); code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%q", code, errb.String())
	}
	if got := readFileT(t, out); got != "/tmp/sentinel-index" {
		t.Errorf("GIT_INDEX_FILE did not reach the gate: %q", got)
	}
}

// A failing gate passes its exit code through and blocks.
func TestHookGatePropagatesExitCode(t *testing.T) {
	repo := hookRepo(t)
	setManifest(t, repo, "gate: boom\n  hook: pre-commit\n  run: exit 3\n")
	var out, errb strings.Builder
	if code := RunHook([]string{"pre-commit"}, strings.NewReader(""), &out, &errb); code != 3 {
		t.Fatalf("exit = %d, want the gate's 3", code)
	}
}

// git-lfs pre-push is forwarded (with args), then the gates run.
func TestHookGateForwardsLFSOnPrePush(t *testing.T) {
	hookRepo(t)
	lfsLog := fakeGitLFS(t, "0")
	var out, errb strings.Builder
	if code := RunHook([]string{"pre-push", "origin", "u"}, strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%q", code, errb.String())
	}
	if !strings.Contains(readFileT(t, lfsLog), "pre-push origin u") {
		t.Errorf("git lfs pre-push not forwarded: %q", readFileT(t, lfsLog))
	}
}

// The git-lfs forward fails closed on a gate hook, like the stock stub — and
// the gates never run past it.
func TestHookGateLFSFailureBlocks(t *testing.T) {
	repo := hookRepo(t)
	fakeGitLFS(t, "3")
	var out, errb strings.Builder
	if code := RunHook([]string{"pre-push"}, strings.NewReader(""), &out, &errb); code != 3 {
		t.Fatalf("exit = %d, want git-lfs's 3", code)
	}
	if lexists(filepath.Join(repo.OMK, "ledger.tsv")) {
		t.Error("gates ran despite the LFS failure")
	}
}

// OMAKASE_SKIP_GATES=1 skips the whole stage, audited on stdout — the
// replacement for lefthook's LEFTHOOK=0.
func TestHookGateSkipAllGates(t *testing.T) {
	repo := hookRepo(t)
	setManifest(t, repo, "gate: boom\n  hook: pre-commit\n  run: exit 1\n")
	t.Setenv("OMAKASE_SKIP_GATES", "1")
	var out, errb strings.Builder
	if code := RunHook([]string{"pre-commit"}, strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if !strings.Contains(out.String(), "OMAKASE_SKIP_GATES") {
		t.Errorf("skip-all not audited: %q", out.String())
	}
}

// OMAKASE_SKIP_GATES skips gates by explicit choice — but never the harness
// verify: a wiped overlay still blocks.
func TestHookGateSkipAllDoesNotBypassVerify(t *testing.T) {
	repo := hookRepo(t)
	t.Setenv("OMAKASE_SKIP_GATES", "1")
	if err := os.Remove(filepath.Join(repo.Root, ".omakase", "gates", "example.sh")); err != nil {
		t.Fatal(err)
	}
	var out, errb strings.Builder
	if code := RunHook([]string{"pre-commit"}, strings.NewReader(""), &out, &errb); code != 1 {
		t.Fatalf("exit = %d, want 1 (verify is not bypassable)", code)
	}
}

// A leaked GIT_DIR (exported for ANOTHER repo) must not misdirect the hook:
// cwd wins.
func TestHookScrubsLeakedGitEnv(t *testing.T) {
	repo := hookRepo(t)
	otherDir := t.TempDir()
	runGitT(t, otherDir, "init", "-q")
	t.Setenv("GIT_DIR", filepath.Join(otherDir, ".git"))
	t.Setenv("GIT_WORK_TREE", otherDir)
	var out, errb strings.Builder
	if code := RunHook([]string{"pre-commit"}, strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("exit = %d, want 0 (cwd repo is installed); stderr=%q", code, errb.String())
	}
	_ = repo
}

// ------------------------------------------------------------ post-checkout

func TestHookPostCheckoutHealsMissingFile(t *testing.T) {
	repo := hookRepo(t)
	rel := filepath.Join(".omakase", "gates", "example.sh")
	if err := os.Remove(filepath.Join(repo.Root, rel)); err != nil {
		t.Fatal(err)
	}
	var out, errb strings.Builder
	if code := RunHook([]string{"post-checkout", "abc", "def", "1"}, strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%q", code, errb.String())
	}
	got := readFileT(t, filepath.Join(repo.Root, rel))
	eq(t, "healed content", got, gateContent)
	if info, err := os.Stat(filepath.Join(repo.Root, rel)); err != nil || info.Mode().Perm()&0o100 == 0 {
		t.Errorf("healed .sh not executable: %v", err)
	}
	eq(t, "stderr (heal is silent)", errb.String(), "")
}

func TestHookPostCheckoutNeverOverwrites(t *testing.T) {
	repo := hookRepo(t)
	rel := filepath.Join(".omakase", "gates", "example.sh")
	writeFile(t, filepath.Join(repo.Root, rel), "my local edit\n")
	var out, errb strings.Builder
	if code := RunHook([]string{"post-checkout"}, strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	eq(t, "edited file untouched", readFileT(t, filepath.Join(repo.Root, rel)), "my local edit\n")
	if !strings.Contains(errb.String(), "DRIFTED") {
		t.Errorf("stderr = %q, want the drift warning", errb.String())
	}
	if !strings.Contains(errb.String(), "cp -P") {
		t.Errorf("stderr = %q, want the adopt-canonical fix", errb.String())
	}
}

func TestHookPostCheckoutNeverResurrectsDisabled(t *testing.T) {
	repo := hookRepo(t)
	rel := filepath.Join(".omakase", "gates", "example.sh")
	rows := readFileT(t, filepath.Join(repo.OMK, "placed.tsv"))
	writeFile(t, filepath.Join(repo.OMK, "placed.tsv"), strings.Replace(rows, "example.sh\tgate\tpayload\t"+sha256hex([]byte(gateContent))+"\t1\n", "example.sh\tgate\tpayload\t"+sha256hex([]byte(gateContent))+"\t0\n", 1))
	if err := os.Remove(filepath.Join(repo.Root, rel)); err != nil {
		t.Fatal(err)
	}
	var out, errb strings.Builder
	if code := RunHook([]string{"post-checkout"}, strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if lexists(filepath.Join(repo.Root, rel)) {
		t.Error("disabled row was resurrected")
	}
	eq(t, "stderr", errb.String(), "")
}

func TestHookPostCheckoutWarnsOnTrackedCollision(t *testing.T) {
	repo := hookRepo(t)
	rel := ".omakase/gates/example.sh"
	writeFile(t, filepath.Join(repo.Root, rel), "upstream version\n")
	runGitT(t, repo.Root, "add", "-f", rel)
	runGitT(t, repo.Root, "commit", "-q", "-m", "upstream lands the path")
	var out, errb strings.Builder
	if code := RunHook([]string{"post-checkout"}, strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if !strings.Contains(errb.String(), "now TRACKED") {
		t.Errorf("stderr = %q, want the upstream-collision warning", errb.String())
	}
	eq(t, "tracked file untouched", readFileT(t, filepath.Join(repo.Root, rel)), "upstream version\n")
}

// post-checkout forwards git-lfs best-effort: a git-lfs failure never fails the
// checkout.
func TestHookPostCheckoutForwardsLFSBestEffort(t *testing.T) {
	hookRepo(t)
	lfsLog := fakeGitLFS(t, "9")
	var out, errb strings.Builder
	if code := RunHook([]string{"post-checkout", "a", "b", "1"}, strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("exit = %d, want 0 (post-checkout is best-effort)", code)
	}
	if !strings.Contains(readFileT(t, lfsLog), "post-checkout a b 1") {
		t.Errorf("git lfs post-checkout not forwarded: %q", readFileT(t, lfsLog))
	}
}

// A deleted kept file heals back to the ACCEPTED version, not the harness
// snapshot: the kept copy is what the user consented to (#98 Part 2).
func TestHealRestoresKeptVersion(t *testing.T) {
	dir, repo := placeTwoRules(t)
	rel := ".claude/rules/a.md"
	full := filepath.Join(dir, rel)
	edited := editFile(t, full)
	if err := FileKeep(repo, rel); err != nil {
		t.Fatalf("FileKeep: %v", err)
	}
	if err := os.Remove(full); err != nil {
		t.Fatal(err)
	}

	var warn strings.Builder
	healWorktree(repo, &warn)

	eq(t, "healed content", readFileT(t, full), edited)
	if strings.Contains(warn.String(), "DRIFTED") {
		t.Errorf("heal warned about drift on a healthy kept file: %q", warn.String())
	}
}

// Heal's drift warning on a KEPT file must speak in kept terms and point at
// the lifecycle verbs — the plain-drift cp suggestion would silently discard
// the newest edit (review finding, PR #100).
func TestHealWarnsKeptDriftWithoutCpFix(t *testing.T) {
	dir, repo := placeTwoRules(t)
	rel := ".claude/rules/a.md"
	full := filepath.Join(dir, rel)
	editFile(t, full)
	if err := FileKeep(repo, rel); err != nil {
		t.Fatalf("FileKeep: %v", err)
	}
	editFile(t, full) // drift past the accepted version

	var warn strings.Builder
	healWorktree(repo, &warn)
	w := warn.String()
	if !strings.Contains(w, "accepted (kept) version") || !strings.Contains(w, "omakase diff") {
		t.Errorf("kept drift warning wrong: %q", w)
	}
	if strings.Contains(w, "cp -P") {
		t.Errorf("kept drift warning still suggests the cp fix: %q", w)
	}
}
