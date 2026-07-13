package overlay

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Yuncun/omakase-harness/internal/state"
)

// RunHook integration tests, against real temp git repos. Installed state
// (placed.tsv + snapshot) is assembled by hand rather than via RunInit so
// the runner's contract is pinned independently of init's.

// hookRepo builds an empty-committed repo with an installed-looking harness:
// one enabled placed row (.omakase/gates/example.sh), its snapshot copy, and
// the file on disk. Returns the repo.
func hookRepo(t *testing.T) *state.Repo {
	t.Helper()
	_, repo := initRepo(t)
	installState(t, repo, gateContent)
	return repo
}

// installState writes the minimal installed harness: ledger row + snapshot +
// placed file for .omakase/gates/example.sh with the given content.
func installState(t *testing.T, repo *state.Repo, content string) {
	t.Helper()
	rel := filepath.Join(".omakase", "gates", "example.sh")
	writeFile(t, filepath.Join(repo.Root, rel), content)
	writeFile(t, filepath.Join(repo.OMK, "payload-snapshot", rel), content)
	row := ".omakase/gates/example.sh\tgate\tpayload\t" + sha256hex([]byte(content)) + "\t1\n"
	writeFile(t, filepath.Join(repo.OMK, "placed.tsv"), row)
}

// hookStubLefthook writes a lefthook stub that appends its argv and selected
// env to a log and exits with HOOKSTUB_EXIT (default 0), pointing
// LEFTHOOK_BIN at it. Returns the log path.
func hookStubLefthook(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	stub := filepath.Join(dir, "lefthook")
	log := filepath.Join(dir, "argv.log")
	writeFile(t, stub, "#!/bin/sh\n"+
		"printf 'argv:%s\\n' \"$*\" >> \"$HOOKSTUB_LOG\"\n"+
		"printf 'config:%s\\n' \"${LEFTHOOK_CONFIG:-unset}\" >> \"$HOOKSTUB_LOG\"\n"+
		"printf 'indexfile:%s\\n' \"${GIT_INDEX_FILE:-unset}\" >> \"$HOOKSTUB_LOG\"\n"+
		"printf 'stdin:%s\\n' \"$(cat)\" >> \"$HOOKSTUB_LOG\"\n"+
		"exit \"${HOOKSTUB_EXIT:-0}\"\n")
	if err := os.Chmod(stub, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LEFTHOOK_BIN", stub)
	t.Setenv("HOOKSTUB_LOG", log)
	return log
}

// lhFreePath rebuilds PATH as one temp dir holding only a git symlink, and
// empties every other lefthook resolution tier, so ResolveForHook finds
// nothing while git keeps working.
func lhFreePath(t *testing.T) {
	t.Helper()
	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	if err := os.Symlink(gitPath, filepath.Join(bin, "git")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	t.Setenv("LEFTHOOK_BIN", "")
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
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

func TestHookGateBlocksOnMissingPlacedFile(t *testing.T) {
	repo := hookRepo(t)
	hookStubLefthook(t)
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

// A missing DISABLED row is deliberately absent — never a block.
func TestHookGateIgnoresDisabledRows(t *testing.T) {
	repo := hookRepo(t)
	log := hookStubLefthook(t)
	writeFile(t, filepath.Join(repo.Root, "lefthook-local.yml"), "pre-commit:\n  jobs:\n    - name: x\n      run: true\n")
	rows := readFileT(t, filepath.Join(repo.OMK, "placed.tsv"))
	writeFile(t, filepath.Join(repo.OMK, "placed.tsv"),
		strings.Replace(rows, "\t1\n", "\t0\n", 1)+"lefthook-local.yml\tgate\tpayload\t"+sha256hex([]byte("x"))+"\t1\n")
	if err := os.Remove(filepath.Join(repo.Root, ".omakase", "gates", "example.sh")); err != nil {
		t.Fatal(err)
	}
	var out, errb strings.Builder
	if code := RunHook([]string{"pre-commit"}, strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%q", code, errb.String())
	}
	if !strings.Contains(readFileT(t, log), "argv:run pre-commit") {
		t.Error("gates did not run for the enabled wiring")
	}
}

func TestHookGateRunsLefthookWithLocalConfig(t *testing.T) {
	repo := hookRepo(t)
	log := hookStubLefthook(t)
	wiring := filepath.Join(repo.Root, "lefthook-local.yml")
	writeFile(t, wiring, "pre-commit:\n  jobs:\n    - name: x\n      run: true\n")
	var out, errb strings.Builder
	if code := RunHook([]string{"pre-commit"}, strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%q", code, errb.String())
	}
	got := readFileT(t, log)
	if !strings.Contains(got, "argv:run pre-commit --no-auto-install\n") {
		t.Errorf("lefthook argv missing run/--no-auto-install: %q", got)
	}
	if !strings.Contains(got, "config:"+wiring+"\n") {
		t.Errorf("LEFTHOOK_CONFIG not pointed at the placed wiring: %q", got)
	}
}

// A repo shipping its own lefthook.yml keeps lefthook's default config
// resolution, so the project's own jobs still run alongside the harness's.
func TestHookGateRespectsProjectLefthookYml(t *testing.T) {
	repo := hookRepo(t)
	log := hookStubLefthook(t)
	writeFile(t, filepath.Join(repo.Root, "lefthook-local.yml"), "pre-commit:\n  jobs:\n    - name: x\n      run: true\n")
	writeFile(t, filepath.Join(repo.Root, "lefthook.yml"), "pre-commit:\n  jobs:\n    - name: own\n      run: true\n")
	var out, errb strings.Builder
	if code := RunHook([]string{"pre-commit"}, strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%q", code, errb.String())
	}
	if !strings.Contains(readFileT(t, log), "config:unset\n") {
		t.Errorf("LEFTHOOK_CONFIG must stay unset when the repo has its own lefthook.yml: %q", readFileT(t, log))
	}
}

// A LEFTHOOK_CONFIG leaked from the environment must not misdirect the gates
// (same class as the GIT_DIR scrub).
func TestHookGateDropsLeakedLefthookConfig(t *testing.T) {
	repo := hookRepo(t)
	log := hookStubLefthook(t)
	wiring := filepath.Join(repo.Root, "lefthook-local.yml")
	writeFile(t, wiring, "pre-commit:\n  jobs:\n    - name: x\n      run: true\n")
	t.Setenv("LEFTHOOK_CONFIG", "/some/other/repo/lefthook.yml")
	var out, errb strings.Builder
	if code := RunHook([]string{"pre-commit"}, strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%q", code, errb.String())
	}
	if !strings.Contains(readFileT(t, log), "config:"+wiring+"\n") {
		t.Errorf("leaked LEFTHOOK_CONFIG survived: %q", readFileT(t, log))
	}
}

// The leak must also be dropped when the repo has its OWN lefthook.yml —
// there the leak would displace the project's config entirely, silently
// skipping its committed gates and running another repo's commands here.
func TestHookGateDropsLeakedConfigWithProjectLefthookYml(t *testing.T) {
	repo := hookRepo(t)
	log := hookStubLefthook(t)
	writeFile(t, filepath.Join(repo.Root, "lefthook-local.yml"), "pre-commit:\n  jobs:\n    - name: x\n      run: true\n")
	writeFile(t, filepath.Join(repo.Root, "lefthook.yml"), "pre-commit:\n  jobs:\n    - name: own\n      run: true\n")
	t.Setenv("LEFTHOOK_CONFIG", "/some/other/repo/lefthook.yml")
	var out, errb strings.Builder
	if code := RunHook([]string{"pre-commit"}, strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%q", code, errb.String())
	}
	if !strings.Contains(readFileT(t, log), "config:unset\n") {
		t.Errorf("leaked LEFTHOOK_CONFIG survived past the project's own lefthook.yml: %q", readFileT(t, log))
	}
}

// GIT_INDEX_FILE must SURVIVE the env scrub: git points it at the temporary
// index during partial commits, and the gates must see that staged set. Only
// GIT_DIR/GIT_WORK_TREE/GIT_COMMON_DIR are scrubbed.
func TestHookKeepsGitIndexFile(t *testing.T) {
	repo := hookRepo(t)
	log := hookStubLefthook(t)
	writeFile(t, filepath.Join(repo.Root, "lefthook-local.yml"), "pre-commit:\n  jobs:\n    - name: x\n      run: true\n")
	t.Setenv("GIT_INDEX_FILE", "/tmp/sentinel-index")
	var out, errb strings.Builder
	if code := RunHook([]string{"pre-commit"}, strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%q", code, errb.String())
	}
	if !strings.Contains(readFileT(t, log), "indexfile:/tmp/sentinel-index\n") {
		t.Errorf("GIT_INDEX_FILE did not reach the gate runner: %q", readFileT(t, log))
	}
}

func TestHookGatePropagatesLefthookExitCode(t *testing.T) {
	repo := hookRepo(t)
	hookStubLefthook(t)
	t.Setenv("HOOKSTUB_EXIT", "3")
	writeFile(t, filepath.Join(repo.Root, "lefthook-local.yml"), "pre-commit:\n  jobs:\n    - name: x\n      run: false\n")
	var out, errb strings.Builder
	if code := RunHook([]string{"pre-commit"}, strings.NewReader(""), &out, &errb); code != 3 {
		t.Fatalf("exit = %d, want lefthook's 3", code)
	}
}

func TestHookGateForwardsArgsAndStdin(t *testing.T) {
	repo := hookRepo(t)
	log := hookStubLefthook(t)
	writeFile(t, filepath.Join(repo.Root, "lefthook-local.yml"), "pre-push:\n  jobs:\n    - name: x\n      run: true\n")
	var out, errb strings.Builder
	code := RunHook([]string{"pre-push", "origin", "https://example.com/r.git"},
		strings.NewReader("refs/heads/main abc refs/heads/main def"), &out, &errb)
	if code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%q", code, errb.String())
	}
	got := readFileT(t, log)
	if !strings.Contains(got, "argv:run pre-push origin https://example.com/r.git --no-auto-install\n") {
		t.Errorf("hook args not forwarded before the option: %q", got)
	}
	if !strings.Contains(got, "stdin:refs/heads/main abc refs/heads/main def\n") {
		t.Errorf("pre-push stdin not forwarded: %q", got)
	}
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

// lefthook forwards `git lfs <hook>` natively only for hooks its config
// defines (pinned 2.1.9 behavior); an LFS hook the wiring does not name gets
// omakase's own forward, so displacing a stock git-lfs hook loses nothing.
func TestHookGateForwardsLFSWhenUnwired(t *testing.T) {
	repo := hookRepo(t)
	log := hookStubLefthook(t)
	writeFile(t, filepath.Join(repo.Root, "lefthook-local.yml"), "pre-commit:\n  jobs:\n    - name: x\n      run: true\n")
	lfsLog := fakeGitLFS(t, "0")
	var out, errb strings.Builder
	if code := RunHook([]string{"pre-push", "origin", "u"}, strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%q", code, errb.String())
	}
	if !strings.Contains(readFileT(t, lfsLog), "pre-push origin u") {
		t.Errorf("git lfs pre-push not forwarded: %q", readFileT(t, lfsLog))
	}
	if !strings.Contains(readFileT(t, log), "argv:run pre-push") {
		t.Error("lefthook still owes its (jobless) run after the LFS forward")
	}
}

// The direct LFS forward fails closed on a gate hook, like the stock stub.
func TestHookGateLFSFailureBlocks(t *testing.T) {
	repo := hookRepo(t)
	log := hookStubLefthook(t)
	writeFile(t, filepath.Join(repo.Root, "lefthook-local.yml"), "pre-commit:\n  jobs:\n    - name: x\n      run: true\n")
	fakeGitLFS(t, "3")
	var out, errb strings.Builder
	if code := RunHook([]string{"pre-push"}, strings.NewReader(""), &out, &errb); code != 3 {
		t.Fatalf("exit = %d, want git-lfs's 3", code)
	}
	if _, err := os.Stat(log); !os.IsNotExist(err) {
		t.Error("gates ran despite the LFS failure")
	}
}

// A hook the wiring DOES define skips omakase's own forward — lefthook
// forwards LFS natively there, and a double run would be waste.
func TestHookGateSkipsOwnLFSWhenWired(t *testing.T) {
	repo := hookRepo(t)
	hookStubLefthook(t)
	writeFile(t, filepath.Join(repo.Root, "lefthook-local.yml"), "pre-push:\n  jobs:\n    - name: x\n      run: true\n")
	lfsLog := fakeGitLFS(t, "0")
	var out, errb strings.Builder
	if code := RunHook([]string{"pre-push", "origin", "u"}, strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if _, err := os.Stat(lfsLog); !os.IsNotExist(err) {
		t.Errorf("omakase forwarded LFS for a wired hook (lefthook owns it there): %q", readFileT(t, lfsLog))
	}
}

func TestHookGateHonorsLefthookZero(t *testing.T) {
	repo := hookRepo(t)
	log := hookStubLefthook(t)
	writeFile(t, filepath.Join(repo.Root, "lefthook-local.yml"), "pre-commit:\n  jobs:\n    - name: x\n      run: true\n")
	t.Setenv("LEFTHOOK", "0")
	var out, errb strings.Builder
	if code := RunHook([]string{"pre-commit"}, strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if _, err := os.Stat(log); !os.IsNotExist(err) {
		t.Error("lefthook spawned despite LEFTHOOK=0")
	}
}

// LEFTHOOK=0 skips gates by explicit choice — but never the harness verify:
// a wiped overlay still blocks.
func TestHookGateLefthookZeroDoesNotBypassVerify(t *testing.T) {
	repo := hookRepo(t)
	hookStubLefthook(t)
	t.Setenv("LEFTHOOK", "0")
	if err := os.Remove(filepath.Join(repo.Root, ".omakase", "gates", "example.sh")); err != nil {
		t.Fatal(err)
	}
	var out, errb strings.Builder
	if code := RunHook([]string{"pre-commit"}, strings.NewReader(""), &out, &errb); code != 1 {
		t.Fatalf("exit = %d, want 1 (verify is not bypassable)", code)
	}
}

func TestHookGateBlocksWhenNoLefthookAnywhere(t *testing.T) {
	repo := hookRepo(t)
	lhFreePath(t)
	writeFile(t, filepath.Join(repo.Root, "lefthook-local.yml"), "pre-commit:\n  jobs:\n    - name: x\n      run: true\n")
	var out, errb strings.Builder
	if code := RunHook([]string{"pre-commit"}, strings.NewReader(""), &out, &errb); code != 1 {
		t.Fatalf("exit = %d, want 1; stderr=%q", code, errb.String())
	}
	if !strings.Contains(errb.String(), "omakase: BLOCKING — no lefthook found") {
		t.Errorf("stderr = %q, want the BLOCKING line", errb.String())
	}
	if !strings.Contains(errb.String(), "LEFTHOOK_BIN") {
		t.Errorf("stderr = %q, want an escape hatch named", errb.String())
	}
}

// A leaked GIT_DIR (exported for ANOTHER repo) must not misdirect the hook:
// cwd wins.
func TestHookScrubsLeakedGitEnv(t *testing.T) {
	repo := hookRepo(t)
	hookStubLefthook(t)
	writeFile(t, filepath.Join(repo.Root, "lefthook-local.yml"), "pre-commit:\n  jobs:\n    - name: x\n      run: true\n")
	otherDir := t.TempDir()
	runGitT(t, otherDir, "init", "-q")
	t.Setenv("GIT_DIR", filepath.Join(otherDir, ".git"))
	t.Setenv("GIT_WORK_TREE", otherDir)
	var out, errb strings.Builder
	if code := RunHook([]string{"pre-commit"}, strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("exit = %d, want 0 (cwd repo is installed); stderr=%q", code, errb.String())
	}
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
	writeFile(t, filepath.Join(repo.OMK, "placed.tsv"), strings.Replace(rows, "\t1\n", "\t0\n", 1))
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

// The wiring names no post-checkout hook: lefthook must not be spawned (a
// jobless spawn would print its header on every checkout).
func TestHookPostCheckoutSkipsLefthookWhenUnwired(t *testing.T) {
	repo := hookRepo(t)
	log := hookStubLefthook(t)
	writeFile(t, filepath.Join(repo.Root, "lefthook-local.yml"), "pre-commit:\n  jobs:\n    - name: x\n      run: true\n")
	var out, errb strings.Builder
	if code := RunHook([]string{"post-checkout"}, strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if _, err := os.Stat(log); !os.IsNotExist(err) {
		t.Errorf("lefthook spawned for an unwired post-checkout: %q", readFileT(t, log))
	}
}

func TestHookPostCheckoutRunsWiredJobs(t *testing.T) {
	repo := hookRepo(t)
	log := hookStubLefthook(t)
	writeFile(t, filepath.Join(repo.Root, "lefthook-local.yml"),
		"pre-commit:\n  jobs:\n    - name: x\n      run: true\npost-checkout:\n  jobs:\n    - name: y\n      run: true\n")
	var out, errb strings.Builder
	if code := RunHook([]string{"post-checkout", "a", "b", "1"}, strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if !strings.Contains(readFileT(t, log), "argv:run post-checkout a b 1 --no-auto-install\n") {
		t.Errorf("wired post-checkout not run with forwarded args: %q", readFileT(t, log))
	}
}

// A wired post-checkout job failing must never fail the checkout.
func TestHookPostCheckoutIgnoresJobFailure(t *testing.T) {
	repo := hookRepo(t)
	hookStubLefthook(t)
	t.Setenv("HOOKSTUB_EXIT", "9")
	writeFile(t, filepath.Join(repo.Root, "lefthook-local.yml"), "post-checkout:\n  jobs:\n    - name: y\n      run: false\n")
	var out, errb strings.Builder
	if code := RunHook([]string{"post-checkout"}, strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("exit = %d, want 0 (post-checkout is best-effort)", code)
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
