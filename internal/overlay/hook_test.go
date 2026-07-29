package overlay

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Yuncun/omakase-harness/internal/block"
	"github.com/Yuncun/omakase-harness/internal/hook"
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

// session-start (the plugin's SessionStart hook) shares post-checkout's
// best-effort contract: exit 0 and stay silent wherever there is nothing
// to do — a session start must never be blocked or narrated.
func TestHookSessionStartSilentZeroWhenNotInstalled(t *testing.T) {
	initRepo(t)
	var out, errb strings.Builder
	if code := RunHook([]string{"session-start"}, strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%q", code, errb.String())
	}
	eq(t, "stdout", out.String(), "")
	eq(t, "stderr", errb.String(), "")
}

func TestHookSessionStartSilentWhenIntact(t *testing.T) {
	hookRepo(t)
	var out, errb strings.Builder
	if code := RunHook([]string{"session-start"}, strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%q", code, errb.String())
	}
	eq(t, "stdout", out.String(), "")
	eq(t, "stderr", errb.String(), "")
}

// The one case session-start speaks: files were missing and got restored —
// one stdout line (host session context), so the wiped-overlay session is
// visibly repaired instead of silently unguided (#164 C5).
func TestHookSessionStartHealsAndReports(t *testing.T) {
	repo := hookRepo(t)
	rel := filepath.Join(".omakase", "gates", "example.sh")
	if err := os.Remove(filepath.Join(repo.Root, rel)); err != nil {
		t.Fatal(err)
	}
	var out, errb strings.Builder
	if code := RunHook([]string{"session-start"}, strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%q", code, errb.String())
	}
	eq(t, "healed content", readFileT(t, filepath.Join(repo.Root, rel)), gateContent)
	if !strings.Contains(out.String(), "restored 1 missing harness file") {
		t.Errorf("stdout = %q, want the one-line restore report", out.String())
	}
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

// A masked path is tracked with skip-worktree set: someone is deliberately
// running this harness over the repo's own copy, without staging a deletion
// their whole team would see. That is the opposite of the upstream-collision
// this warning exists for, and firing it once per masked path on every
// checkout and every commit buries the output the user actually ran.
func TestHookPostCheckoutSilentOnMaskedPath(t *testing.T) {
	repo := hookRepo(t)
	rel := ".omakase/gates/example.sh"
	writeFile(t, filepath.Join(repo.Root, rel), "upstream version\n")
	runGitT(t, repo.Root, "add", "-f", rel)
	runGitT(t, repo.Root, "commit", "-q", "-m", "upstream lands the path")
	runGitT(t, repo.Root, "update-index", "--skip-worktree", rel)
	writeFile(t, filepath.Join(repo.Root, rel), "harness version\n")

	var out, errb strings.Builder
	if code := RunHook([]string{"post-checkout"}, strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if strings.Contains(errb.String(), "now TRACKED") {
		t.Errorf("stderr = %q, want no collision warning for a masked path", errb.String())
	}
	eq(t, "harness copy untouched", readFileT(t, filepath.Join(repo.Root, rel)), "harness version\n")
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

// TestHookTimeNeverWritesGitHooks is the issue #96 regression test: a hook
// firing must never modify anything under .git/hooks — not even to "repair"
// a stale or foreign hook file. The original incident was a hook file
// rewritten in place while bash was executing it (bash resumed at a stale
// byte offset — spurious syntax error, non-zero checkout). The #98
// architecture kills the class by contract: dispatchers are write-once, and
// only init/remove are writers. This pins that contract.
func TestHookTimeNeverWritesGitHooks(t *testing.T) {
	repo := hookRepo(t)
	hooksDir := filepath.Join(repo.Root, ".git", "hooks")
	for _, name := range hook.Names() {
		if err := hook.Write(hooksDir, name); err != nil {
			t.Fatal(err)
		}
	}
	// A deliberately stale/foreign hook file: hook-time code must leave even
	// this untouched (repairing it is init's job, not a fire-time side effect).
	stale := []byte("#!/bin/sh\n# stale wiring from an older omakase\nexit 0\n")
	writeFile(t, filepath.Join(hooksDir, "post-merge"), string(stale))
	if err := os.Chmod(filepath.Join(hooksDir, "post-merge"), 0o755); err != nil {
		t.Fatal(err)
	}

	before := map[string]string{}
	beforeMtime := map[string]time.Time{}
	entries, err := os.ReadDir(hooksDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		p := filepath.Join(hooksDir, e.Name())
		before[e.Name()] = readFileT(t, p)
		info, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		beforeMtime[e.Name()] = info.ModTime()
	}

	// Fire every hook kind: a passing gate, the heal with real work to do
	// (a deleted placed file), and the session heal.
	var out, errb strings.Builder
	if code := RunHook([]string{"pre-commit"}, strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("pre-commit exit = %d; stderr=%q", code, errb.String())
	}
	if err := os.Remove(filepath.Join(repo.Root, ".omakase", "gates", "example.sh")); err != nil {
		t.Fatal(err)
	}
	if code := RunHook([]string{"post-checkout", "0000", "1111", "1"}, strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("post-checkout exit = %d; stderr=%q", code, errb.String())
	}
	if code := RunHook([]string{"session-start"}, strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("session-start exit = %d; stderr=%q", code, errb.String())
	}

	after, err := os.ReadDir(hooksDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(entries) {
		t.Errorf("hook-time run changed the .git/hooks entry count: %d -> %d", len(entries), len(after))
	}
	for _, e := range after {
		p := filepath.Join(hooksDir, e.Name())
		if got := readFileT(t, p); got != before[e.Name()] {
			t.Errorf("hook-time run rewrote .git/hooks/%s", e.Name())
		}
		info, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		if !info.ModTime().Equal(beforeMtime[e.Name()]) {
			t.Errorf("hook-time run touched .git/hooks/%s (mtime changed)", e.Name())
		}
	}
}

// A blocked-only repo (no harness ever installed) still self-heals: the
// session-start hook reasserts a blocked item a git operation brought back,
// even though there is no placed.tsv. Without this, the heal's
// not-installed early return left leaked files steering until the user
// happened to run status.
func TestSessionStartReassertsBlockedOnlyRepo(t *testing.T) {
	dir, repo := initRepo(t)
	writeFile(t, filepath.Join(dir, "CLAUDE.md"), "doctrine\n")
	runGitT(t, dir, "add", "CLAUDE.md")
	runGitT(t, dir, "commit", "-q", "-m", "steering")

	var out, errOut strings.Builder
	if code := block.Run(false, []string{"CLAUDE.md", "--yes"}, &out, &errOut); code != 0 {
		t.Fatalf("block: %s", errOut.String())
	}
	runGitT(t, dir, "sparse-checkout", "disable") // the silent-leak end state
	if _, err := os.Lstat(filepath.Join(dir, "CLAUDE.md")); err != nil {
		t.Fatal("precondition: leak should have restored the file")
	}
	if fileRegular(filepath.Join(repo.OMK, "placed.tsv")) {
		t.Fatal("precondition: repo must be blocked-only (no install)")
	}

	var stdout, stderr strings.Builder
	if code := RunHook([]string{"session-start"}, strings.NewReader(""), &stdout, &stderr); code != 0 {
		t.Fatalf("session-start: exit %d, stderr %s", code, stderr.String())
	}
	if _, err := os.Lstat(filepath.Join(dir, "CLAUDE.md")); err == nil {
		t.Error("leaked blocked file not re-hidden in blocked-only repo")
	}
	if !strings.Contains(stdout.String(), "re-hid 1 blocked item(s)") {
		t.Errorf("stdout = %q, want the re-hid note", stdout.String())
	}
}

// The pre-push ref lines have two readers — the git-lfs forward and the gate
// runner (glob scoper + gate children). A real git-lfs DRAINS stdin, so
// without buffering the gates after it see nothing: globs print "cannot
// scope" and a stdin-reading check fails (#186 regression, found live).
func TestHookPrePushStdinSurvivesLFSDrain(t *testing.T) {
	repo := hookRepo(t)
	setManifest(t, repo, "name: t\nversion: 1\n\ngate: reads\n  hook: pre-push\n  run: grep -q refs/heads/main\n")

	// A git-lfs that swallows all of stdin, like the real one.
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "git-lfs"), "#!/bin/sh\ncat >/dev/null\nexit 0\n")
	if err := os.Chmod(filepath.Join(dir, "git-lfs"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	refs := "refs/heads/main aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa refs/heads/main bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb\n"
	var out, errb strings.Builder
	if code := RunHook([]string{"pre-push"}, strings.NewReader(refs), &out, &errb); code != 0 {
		t.Fatalf("gate did not see the ref lines after the LFS forward drained stdin: exit %d\n%s%s", code, out.String(), errb.String())
	}
}

// The git-lfs forward must receive the COMPLETE ref list, even past the old
// 1MB cap: git tolerates a hook that stops reading, and git-lfs silently
// skips uploading objects for refs it never saw — dangling pointers on the
// remote with a green push. Pins both the no-cap and the
// forward-gets-the-refs wiring (mutating either broke nothing before).
func TestHookPrePushLFSReceivesFullStdin(t *testing.T) {
	repo := hookRepo(t)
	setManifest(t, repo, "name: t\nversion: 1\n") // no gates: isolate the LFS forward

	dir := t.TempDir()
	countFile := filepath.Join(dir, "count")
	writeFile(t, filepath.Join(dir, "git-lfs"), "#!/bin/sh\nwc -c > \""+countFile+"\"\nexit 0\n")
	if err := os.Chmod(filepath.Join(dir, "git-lfs"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	line := "refs/heads/main aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa refs/heads/main bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb\n"
	var refs strings.Builder
	for refs.Len() < 2*1024*1024 {
		refs.WriteString(line)
	}
	var out, errb strings.Builder
	if code := RunHook([]string{"pre-push"}, strings.NewReader(refs.String()), &out, &errb); code != 0 {
		t.Fatalf("exit %d\n%s%s", code, out.String(), errb.String())
	}
	got := strings.TrimSpace(readFileT(t, countFile))
	if want := strconv.Itoa(refs.Len()); got != want {
		t.Fatalf("git-lfs received %s bytes of ref lines, want %s (truncated or rewired)", got, want)
	}
}

// A blocked path must survive the heals: it carries the same skip-worktree
// tag as a deliberately masked (adopted) path, and before the blockedCovers
// exemption healWorktree copied the harness snapshot over the tracked,
// blocked file — block defeated, index dirtied, the agent reading the one
// file the user hid. verifyPresent must likewise not count the deliberate
// absence as a hole.
func TestHealNeverRestoresBlockedPath(t *testing.T) {
	repo := hookRepo(t)
	root := repo.Root
	rel := ".claude/skills/x/SKILL.md"

	// Upstream commits the path the harness also places.
	writeFile(t, filepath.Join(root, rel), "UPSTREAM\n")
	runGitT(t, root, "add", rel)
	runGitT(t, root, "commit", "-q", "-m", "upstream skill")

	// The harness placed it too: snapshot + enabled ledger row.
	writeFile(t, filepath.Join(repo.OMK, "payload-snapshot", rel), "HARNESS\n")
	f, err := os.OpenFile(filepath.Join(repo.OMK, "placed.tsv"), os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Fprintf(f, "%s\tskill\tpayload\t%s\t1\n", rel, sha256hex([]byte("HARNESS\n")))
	f.Close()

	// The user blocks the skill directory (the real mechanism: non-cone
	// sparse-checkout + the blocked ledger).
	runGitT(t, root, "sparse-checkout", "set", "--no-cone", "/*", "!/.claude/skills/x")
	if err := state.WriteBlocked(repo.OMK, map[string]bool{".claude/skills/x": true}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(root, rel)); !os.IsNotExist(err) {
		t.Fatal("precondition: block should hide the file")
	}

	var out, errb strings.Builder
	if code := RunHook([]string{"session-start"}, strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("session-start exit %d\n%s", code, errb.String())
	}
	if _, err := os.Lstat(filepath.Join(root, rel)); !os.IsNotExist(err) {
		t.Fatalf("heal rematerialized a blocked path\nstdout: %s\nstderr: %s", out.String(), errb.String())
	}
	stCmd := exec.Command("git", "-C", root, "status", "--porcelain")
	stOut, err := stCmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	// The hand-built fixture has no exclude block, so untracked ?? rows are
	// noise; what must never appear is a MODIFICATION — the tracked blocked
	// file overwritten with harness content.
	for _, l := range strings.Split(string(stOut), "\n") {
		if strings.Contains(l, "skills/x") || strings.HasPrefix(l, " M") || strings.HasPrefix(l, "M ") {
			t.Fatalf("heal dirtied the index on a blocked path: %q", stOut)
		}
	}

	// And the gate hook still runs: the blocked absence is not a hole.
	out.Reset()
	errb.Reset()
	if code := RunHook([]string{"pre-commit"}, strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("pre-commit blocked by a deliberately blocked path: exit %d\n%s", code, errb.String())
	}
}
