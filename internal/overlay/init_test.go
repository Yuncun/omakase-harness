package overlay

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Yuncun/omakase-harness/internal/state"
)

// RunInit integration tests. Path-bearing expectations are built from repo.OMK /
// repo.Root at test time rather than hardcoded, so assertions do not embed temp
// paths.
//
// Placement walk order is filepath.WalkDir's lexical order. Single-file (and
// single-top-dir) fixtures are order-invariant; the multi-file fixture
// (TestMultiFilePlacedTsv) asserts lexical order.
//
// Shared helpers writeFile / runGitT live in overlay_test.go.

// summaryTail is the fixed stdout block every successful init prints after its
// +/^/~/- lines.
const summaryTail = "omakase: ignores -> .git/info/exclude; hooks installed; new worktrees auto-install the harness. Nothing to commit.\n" +
	"omakase: see the whole harness any time with  omakase status\n" +
	"omakase: to customize, fork the harness source (clone -> edit -> publish) and\n" +
	"         init from your copy; do not edit injected files in place (overwritten on re-init).\n"

const gateContent = "#!/usr/bin/env bash\necho hi\n"

func sha256hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// chdir switches into dir for the duration of the test, restoring the original
// working directory on cleanup. Tests must not run in parallel (they share
// process cwd + env via t.Setenv).
func chdir(t *testing.T, dir string) {
	t.Helper()
	orig, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(orig) })
}

// initRepo builds an empty-committed git repo in a temp dir, chdirs into it,
// and returns the dir plus the discovered Repo (root/common/OMK) — the same
// discovery RunInit performs, so on-disk assertions use matching paths.
func initRepo(t *testing.T) (string, *state.Repo) {
	t.Helper()
	dir := t.TempDir()
	runGitT(t, dir, "init", "-q")
	runGitT(t, dir, "config", "user.email", "t@t")
	runGitT(t, dir, "config", "user.name", "t")
	runGitT(t, dir, "config", "commit.gpgsign", "false")
	runGitT(t, dir, "commit", "-q", "--allow-empty", "-m", "init")
	chdir(t, dir)
	repo, err := state.Discover(dir)
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	return dir, repo
}

// stubLefthook writes a lefthook stub that appends its argv to a log file and
// exits 0, points LEFTHOOK_BIN at it, and returns the log path. Lets tests
// assert the `install` invocation without a real lefthook, and confirm a
// refusal never reached `lefthook install` (empty log).
func stubLefthook(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	stub := filepath.Join(dir, "lefthook")
	log := filepath.Join(dir, "argv.log")
	writeFile(t, stub, "#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"$LEFTHOOK_STUB_LOG\"\nexit 0\n")
	if err := os.Chmod(stub, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LEFTHOOK_BIN", stub)
	t.Setenv("LEFTHOOK_STUB_LOG", log)
	return log
}

// singleGatePayload returns a payload dir shipping exactly one file,
// .omakase/gates/example.sh, and points OMAKASE_PAYLOAD at it.
func singleGatePayload(t *testing.T) string {
	t.Helper()
	p := t.TempDir()
	writeFile(t, filepath.Join(p, ".omakase", "gates", "example.sh"), gateContent)
	t.Setenv("OMAKASE_PAYLOAD", p)
	return p
}

func readFileT(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

func eq(t *testing.T, label, got, want string) {
	t.Helper()
	if got != want {
		t.Errorf("%s mismatch:\n got: %q\nwant: %q", label, got, want)
	}
}

// ---------------------------------------------------------------- fresh init

func TestFreshInit(t *testing.T) {
	dir, repo := initRepo(t)
	stubLefthook(t)
	singleGatePayload(t)
	// Seed a known exclude so the whole-file assertion does not depend on git's
	// version-specific default template.
	writeFile(t, filepath.Join(repo.CommonDir, "info", "exclude"), "scratch/\n*.tmp\n")

	var stdout, stderr strings.Builder
	code := RunInit(nil, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%q", code, stderr.String())
	}

	wantOut := "omakase: placed 1 file(s), overwrote 0 to match payload, skipped 0 committed path(s).\n" +
		"  + .omakase/gates/example.sh\n" + summaryTail
	eq(t, "stdout", stdout.String(), wantOut)
	eq(t, "stderr", stderr.String(), "")

	// exclude = seeded lines + the derived block (single owned top dir + wiring),
	// every entry root-anchored with a leading "/" — an unanchored ".omakase/"
	// is a gitignore pattern that matches at any depth and would hide a
	// project's own "payload/.omakase" too.
	wantExclude := "scratch/\n*.tmp\n" +
		"# >>> omakase-harness >>>\n/.omakase/\n/lefthook.yml\n/.worktreeinclude\n# <<< omakase-harness <<<\n"
	eq(t, "exclude", readFileT(t, filepath.Join(repo.CommonDir, "info", "exclude")), wantExclude)

	// .worktreeinclude = same block minus .worktreeinclude itself, fresh file.
	wantWtinc := "# >>> omakase-harness >>>\n.omakase/\nlefthook.yml\n# <<< omakase-harness <<<\n"
	eq(t, "wtinc", readFileT(t, filepath.Join(dir, ".worktreeinclude")), wantWtinc)

	// placed.tsv: one row (rel, kind, source label, sha256, 1).
	wantPlaced := ".omakase/gates/example.sh\tgate\tpayload\t" + sha256hex([]byte(gateContent)) + "\t1\n"
	eq(t, "placed.tsv", readFileT(t, filepath.Join(repo.OMK, "placed.tsv")), wantPlaced)

	// snapshot is a byte-equal, executable copy of the placed file.
	snap := filepath.Join(repo.OMK, "payload-snapshot", ".omakase", "gates", "example.sh")
	eq(t, "snapshot content", readFileT(t, snap), gateContent)
	if info, err := os.Stat(filepath.Join(dir, ".omakase", "gates", "example.sh")); err != nil || info.Mode().Perm()&0o100 == 0 {
		t.Errorf("placed .sh not executable: %v", err)
	}

	// the three hook-time scripts installed + executable; ledger set clean.
	for _, name := range []string{"ensure-present.sh", "verify-overlay.sh", "install-guards.sh"} {
		if info, err := os.Stat(filepath.Join(repo.OMK, name)); err != nil || info.Mode().Perm()&0o100 == 0 {
			t.Errorf("%s not installed/executable: %v", name, err)
		}
	}
	// zero committed footprint.
	if out := gitStdout(repo.Root, "status", "--porcelain"); out != "" {
		t.Errorf("git status not clean: %q", out)
	}
}

func TestFreshInitInvokesLefthookInstall(t *testing.T) {
	initRepo(t)
	log := stubLefthook(t)
	singleGatePayload(t)

	var out, errb strings.Builder
	if code := RunInit(nil, &out, &errb); code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%q", code, errb.String())
	}
	eq(t, "lefthook argv", readFileT(t, log), "install\n")
}

// ---------------------------------------------------------------- idempotency

func TestIdempotentRerun(t *testing.T) {
	_, repo := initRepo(t)
	stubLefthook(t)
	singleGatePayload(t)

	var o1, e1 strings.Builder
	if code := RunInit(nil, &o1, &e1); code != 0 {
		t.Fatalf("first init exit = %d", code)
	}
	excludeAfter1 := readFileT(t, filepath.Join(repo.CommonDir, "info", "exclude"))
	placedAfter1 := readFileT(t, filepath.Join(repo.OMK, "placed.tsv"))

	var o2, e2 strings.Builder
	if code := RunInit(nil, &o2, &e2); code != 0 {
		t.Fatalf("second init exit = %d", code)
	}
	// Second run: identical stdout, no stderr, and state unchanged (one block).
	eq(t, "rerun stdout", o2.String(), o1.String())
	eq(t, "rerun stderr", e2.String(), "")
	eq(t, "exclude unchanged", readFileT(t, filepath.Join(repo.CommonDir, "info", "exclude")), excludeAfter1)
	eq(t, "placed.tsv unchanged", readFileT(t, filepath.Join(repo.OMK, "placed.tsv")), placedAfter1)
}

// ---------------------------------------------------------------- tracked skip

func TestTrackedSkip(t *testing.T) {
	dir, _ := initRepo(t)
	stubLefthook(t)
	p := singleGatePayload(t)
	writeFile(t, filepath.Join(p, "AGENTS.md"), "payload agents\n")
	// repo commits its own AGENTS.md (a payload path).
	writeFile(t, filepath.Join(dir, "AGENTS.md"), "COMMITTED agents\n")
	runGitT(t, dir, "add", "AGENTS.md")
	runGitT(t, dir, "commit", "-q", "-m", "team")

	var stdout, stderr strings.Builder
	if code := RunInit(nil, &stdout, &stderr); code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%q", code, stderr.String())
	}
	// WalkDir lexical order: .omakase before AGENTS.md, so example.sh is placed
	// (the sole + line) and AGENTS.md is skipped (the sole ~ line).
	wantOut := "omakase: placed 1 file(s), overwrote 0 to match payload, skipped 1 committed path(s).\n" +
		"  + .omakase/gates/example.sh\n" +
		"  ~ skipped (committed — re-run with --cut-over to let the harness copy take over; guarded, see init.sh --help): AGENTS.md\n" +
		summaryTail
	eq(t, "stdout", stdout.String(), wantOut)
	eq(t, "stderr", stderr.String(), "omakase: SKIP (already tracked) AGENTS.md\n")
	// committed file left untouched.
	eq(t, "committed AGENTS.md", readFileT(t, filepath.Join(dir, "AGENTS.md")), "COMMITTED agents\n")
}

// ---------------------------------------------------------------- overwrite

func TestOverwriteClobbered(t *testing.T) {
	dir, repo := initRepo(t)
	stubLefthook(t)
	singleGatePayload(t)

	var o1, e1 strings.Builder
	if code := RunInit(nil, &o1, &e1); code != 0 {
		t.Fatalf("first init exit = %d", code)
	}
	// User edits the injected gate in place; re-init overwrites it back to payload.
	writeFile(t, filepath.Join(dir, ".omakase", "gates", "example.sh"), "MY EDIT\n")

	var stdout, stderr strings.Builder
	if code := RunInit(nil, &stdout, &stderr); code != 0 {
		t.Fatalf("re-init exit = %d; stderr=%q", code, stderr.String())
	}
	clob := filepath.Join(repo.OMK, "clobbered", ".omakase", "gates", "example.sh")
	wantErr := "omakase: overwrote .omakase/gates/example.sh to match payload (prior copy preserved at " + clob + ")\n"
	eq(t, "stderr", stderr.String(), wantErr)
	// summary carries the ^ line.
	if !strings.Contains(stdout.String(), "  ^ overwrote to match payload (any local edit replaced): .omakase/gates/example.sh\n") {
		t.Errorf("stdout missing ^ line:\n%s", stdout.String())
	}
	if !strings.HasPrefix(stdout.String(), "omakase: placed 1 file(s), overwrote 1 to match payload, skipped 0 committed path(s).\n") {
		t.Errorf("stdout summary count wrong:\n%s", stdout.String())
	}
	// gate restored to payload; the pre-overwrite copy preserved under clobbered/.
	eq(t, "gate restored", readFileT(t, filepath.Join(dir, ".omakase", "gates", "example.sh")), gateContent)
	eq(t, "clobbered backup", readFileT(t, clob), "MY EDIT\n")
}

// TestFirstInstallBacksUpUserFile: with no prior ledger, the place-loop backup
// is the only thing preserving a user's own untracked file at a payload path.
func TestFirstInstallBacksUpUserFile(t *testing.T) {
	dir, repo := initRepo(t)
	stubLefthook(t)
	singleGatePayload(t)
	writeFile(t, filepath.Join(dir, ".omakase", "gates", "example.sh"), "MY OWN FILE\n")

	var stdout, stderr strings.Builder
	if code := RunInit(nil, &stdout, &stderr); code != 0 {
		t.Fatalf("exit = %d; stderr=%q", code, stderr.String())
	}
	eq(t, "gate overwritten", readFileT(t, filepath.Join(dir, ".omakase", "gates", "example.sh")), gateContent)
	clob := filepath.Join(repo.OMK, "clobbered", ".omakase", "gates", "example.sh")
	eq(t, "backup", readFileT(t, clob), "MY OWN FILE\n")
	if !strings.Contains(stderr.String(), "prior copy preserved at "+clob) {
		t.Errorf("stderr missing backup path: %q", stderr.String())
	}
}

// ---------------------------------------------------------------- orphan sweep

func TestSweepDeletesUnchangedOrphan(t *testing.T) {
	dir, _ := initRepo(t)
	stubLefthook(t)
	p := singleGatePayload(t)
	extra := filepath.Join(p, ".omakase", "gates", "extra.sh")
	writeFile(t, extra, "#!/bin/sh\necho extra\n")

	var o1, e1 strings.Builder
	if code := RunInit(nil, &o1, &e1); code != 0 {
		t.Fatalf("first init exit = %d", code)
	}
	// Payload shrinks: extra.sh dropped between versions.
	if err := os.Remove(extra); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr strings.Builder
	if code := RunInit(nil, &stdout, &stderr); code != 0 {
		t.Fatalf("re-init exit = %d; stderr=%q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "  - removed (placed by a prior init, no longer in the payload): .omakase/gates/extra.sh\n") {
		t.Errorf("stdout missing sweep - line:\n%s", stdout.String())
	}
	eq(t, "sweep stderr", stderr.String(), "")
	if _, err := os.Lstat(filepath.Join(dir, ".omakase", "gates", "extra.sh")); !os.IsNotExist(err) {
		t.Errorf("orphan not swept")
	}
}

func TestSweepWarnsEditedOrphan(t *testing.T) {
	dir, _ := initRepo(t)
	stubLefthook(t)
	p := singleGatePayload(t)
	extra := filepath.Join(p, ".omakase", "gates", "extra.sh")
	writeFile(t, extra, "#!/bin/sh\necho extra\n")

	var o1, e1 strings.Builder
	if code := RunInit(nil, &o1, &e1); code != 0 {
		t.Fatalf("first init exit = %d", code)
	}
	if err := os.Remove(extra); err != nil {
		t.Fatal(err)
	}
	// User edited the orphan: init must not destroy it — warn and keep.
	writeFile(t, filepath.Join(dir, ".omakase", "gates", "extra.sh"), "LOCAL EDIT\n")

	var stdout, stderr strings.Builder
	if code := RunInit(nil, &stdout, &stderr); code != 0 {
		t.Fatalf("re-init exit = %d", code)
	}
	wantWarn := "omakase: WARNING — '.omakase/gates/extra.sh' was placed by a prior init, is no longer in the payload, and differs from what init placed (a local edit?). Leaving it; delete it yourself if unwanted.\n"
	eq(t, "sweep warn stderr", stderr.String(), wantWarn)
	// The edited orphan survives untouched, and is not reported as swept.
	eq(t, "orphan kept", readFileT(t, filepath.Join(dir, ".omakase", "gates", "extra.sh")), "LOCAL EDIT\n")
	if strings.Contains(stdout.String(), "extra.sh") {
		t.Errorf("edited orphan wrongly listed in summary:\n%s", stdout.String())
	}
}

// ---------------------------------------------------------------- cut-over

func TestCutoverRefusalWithoutConfirm(t *testing.T) {
	dir, _ := initRepo(t)
	stubLefthook(t)
	log := os.Getenv("LEFTHOOK_STUB_LOG")
	p := singleGatePayload(t)
	writeFile(t, filepath.Join(p, "AGENTS.md"), "payload agents\n")
	writeFile(t, filepath.Join(dir, "AGENTS.md"), "COMMITTED\n")
	runGitT(t, dir, "add", "AGENTS.md")
	runGitT(t, dir, "commit", "-q", "-m", "team")
	t.Setenv("OMAKASE_CUTOVER_CONFIRM", "")

	var stdout, stderr strings.Builder
	code := RunInit([]string{"--cut-over"}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	wantOut := "omakase: cut-over will run  git rm --cached  on 1 tracked file(s):\n" +
		"    AGENTS.md\n" +
		"  This STAGES A DELETION of each shared file. The next commit — including an agent\n" +
		"  auto-commit — applies that deletion FOR EVERYONE who pulls it, and upstream changes\n" +
		"  to these files will then produce modify/delete conflicts. The files stay on disk;\n" +
		"  the injected (gitignored) copies take over locally. Undo before committing with\n" +
		"  'git restore --staged <file>'; 'git add <file>' re-tracks later.\n"
	eq(t, "cutover plan stdout", stdout.String(), wantOut)
	eq(t, "cutover refusal stderr", stderr.String(),
		"omakase: REFUSING cut-over without confirmation. Re-run with OMAKASE_CUTOVER_CONFIRM=1 to proceed. Nothing was changed.\n")
	// nothing mutated: file still tracked, nothing staged, lefthook never ran.
	if !gitTracked(dir, "AGENTS.md") {
		t.Error("AGENTS.md untracked after a refusal")
	}
	if out := gitStdout(dir, "status", "--porcelain"); out != "" {
		t.Errorf("refusal left changes: %q", out)
	}
	if _, err := os.Stat(log); err == nil {
		t.Error("lefthook install ran despite the cut-over refusal")
	}
}

func TestCutoverConfirmed(t *testing.T) {
	dir, _ := initRepo(t)
	stubLefthook(t)
	p := singleGatePayload(t)
	writeFile(t, filepath.Join(p, "AGENTS.md"), "payload agents\n")
	writeFile(t, filepath.Join(dir, "AGENTS.md"), "COMMITTED\n")
	runGitT(t, dir, "add", "AGENTS.md")
	runGitT(t, dir, "commit", "-q", "-m", "team")
	t.Setenv("OMAKASE_CUTOVER_CONFIRM", "1")

	var stdout, stderr strings.Builder
	if code := RunInit([]string{"--cut-over"}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "omakase: cut-over staged 1 deletion(s) — review with 'git status' and commit them yourself.\n") {
		t.Errorf("stdout missing staged confirmation:\n%s", stdout.String())
	}
	// AGENTS.md untracked, deletion staged, injected copy took over on disk.
	if gitTracked(dir, "AGENTS.md") {
		t.Error("AGENTS.md still tracked after confirmed cut-over")
	}
	if out := gitStdout(dir, "status", "--porcelain"); !strings.Contains(out, "D  AGENTS.md") {
		t.Errorf("no staged deletion: %q", out)
	}
	eq(t, "injected copy on disk", readFileT(t, filepath.Join(dir, "AGENTS.md")), "payload agents\n")
}

func TestCutoverNothingTracked(t *testing.T) {
	initRepo(t)
	stubLefthook(t)
	singleGatePayload(t)

	var stdout, stderr strings.Builder
	if code := RunInit([]string{"--cut-over"}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if !strings.HasPrefix(stdout.String(), "omakase: --cut-over: no payload path is tracked by this repo — nothing to cut over.\n") {
		t.Errorf("stdout missing nothing-to-cut-over line:\n%s", stdout.String())
	}
}

// ---------------------------------------------------------- collision guard

func TestCollisionWarning(t *testing.T) {
	dir, repo := initRepo(t)
	stubLefthook(t)
	singleGatePayload(t)

	var o1, e1 strings.Builder
	if code := RunInit(nil, &o1, &e1); code != 0 {
		t.Fatalf("first init exit = %d", code)
	}
	// Simulate an upstream commit landing a tracked file at the placed path.
	writeFile(t, filepath.Join(dir, ".omakase", "gates", "example.sh"), "UPSTREAM\n")
	runGitT(t, dir, "add", "-f", ".omakase/gates/example.sh")
	runGitT(t, dir, "commit", "-q", "-m", "upstream")

	var stdout, stderr strings.Builder
	if code := RunInit(nil, &stdout, &stderr); code != 0 {
		t.Fatalf("re-init exit = %d; stderr=%q", code, stderr.String())
	}
	clob := filepath.Join(repo.OMK, "clobbered", ".omakase", "gates", "example.sh")
	wantWarn := "omakase: WARNING — '.omakase/gates/example.sh' was injected (personal, gitignored) but is NOW TRACKED by the repo.\n" +
		"  An upstream commit likely landed a file at this path; git silently overwrites ignored\n" +
		"  files on checkout/pull, so your personal copy was likely clobbered. Last-injected copy\n" +
		"  preserved at:\n" +
		"    " + clob + "\n" +
		"  Diff it against the tracked file and reconcile: drop '.omakase/gates/example.sh' from your payload, or run\n" +
		"  init --cut-over (guarded) to untrack the file and let the injected copy take over.\n"
	if !strings.Contains(stderr.String(), wantWarn) {
		t.Errorf("collision warning bytes mismatch:\n got: %q\nwant substr: %q", stderr.String(), wantWarn)
	}
	// last-injected copy preserved; tracked file left untouched.
	eq(t, "preserved copy", readFileT(t, clob), gateContent)
	eq(t, "tracked file untouched", readFileT(t, filepath.Join(dir, ".omakase", "gates", "example.sh")), "UPSTREAM\n")
}

// ---------------------------------------------------------- incumbent guards

// assertIncumbentRefusal runs init and asserts exit 1 with a stderr containing
// the incumbent header + a substring, and that nothing was placed.
func assertIncumbentRefusal(t *testing.T, dir string, stderr string, code int, mustContain string) {
	t.Helper()
	if code != 1 {
		t.Fatalf("exit = %d, want 1; stderr=%q", code, stderr)
	}
	if !strings.HasPrefix(stderr, "omakase: REFUSING to install — an incumbent hook manager is present:\n") {
		t.Errorf("stderr missing refusal header:\n%s", stderr)
	}
	if !strings.Contains(stderr, mustContain) {
		t.Errorf("stderr missing %q:\n%s", mustContain, stderr)
	}
	if _, err := os.Stat(filepath.Join(dir, ".omakase")); err == nil {
		t.Error("placed files despite refusal")
	}
}

func TestIncumbentHuskyDir(t *testing.T) {
	dir, _ := initRepo(t)
	stubLefthook(t)
	singleGatePayload(t)
	if err := os.MkdirAll(filepath.Join(dir, ".husky"), 0o755); err != nil {
		t.Fatal(err)
	}
	var out, errb strings.Builder
	code := RunInit(nil, &out, &errb)
	assertIncumbentRefusal(t, dir, errb.String(), code, "  - .husky/ directory (husky)\n")
}

func TestIncumbentTrackedHusky(t *testing.T) {
	dir, _ := initRepo(t)
	stubLefthook(t)
	// payload also ships .husky: a git-tracked .husky still refuses (the exemption
	// is for untracked payload-matching content only).
	p := singleGatePayload(t)
	writeFile(t, filepath.Join(p, ".husky", "pre-commit"), "#!/bin/sh\ntrue\n")
	writeFile(t, filepath.Join(dir, ".husky", "pre-commit"), "#!/bin/sh\nnpx jest\n")
	runGitT(t, dir, "add", ".husky")
	runGitT(t, dir, "commit", "-q", "-m", "husky")

	var out, errb strings.Builder
	code := RunInit(nil, &out, &errb)
	assertIncumbentRefusal(t, dir, errb.String(), code, "  - .husky/ content is git-tracked (the project's own husky setup)\n")
}

func TestIncumbentPrepareScript(t *testing.T) {
	dir, _ := initRepo(t)
	stubLefthook(t)
	singleGatePayload(t)
	writeFile(t, filepath.Join(dir, "package.json"), "{ \"scripts\": { \"prepare\": \"husky\" } }\n")

	var out, errb strings.Builder
	code := RunInit(nil, &out, &errb)
	assertIncumbentRefusal(t, dir, errb.String(), code,
		"  - package.json \"prepare\" script wires a hook manager (husky / simple-git-hooks) — npm install would overwrite lefthook's hooks\n")
}

func TestIncumbentForeignHook(t *testing.T) {
	dir, repo := initRepo(t)
	stubLefthook(t)
	singleGatePayload(t)
	hook := filepath.Join(repo.CommonDir, "hooks", "pre-push")
	writeFile(t, hook, "#!/bin/sh\necho team-gate\nexit 1\n")
	if err := os.Chmod(hook, 0o755); err != nil {
		t.Fatal(err)
	}
	var out, errb strings.Builder
	code := RunInit(nil, &out, &errb)
	assertIncumbentRefusal(t, dir, errb.String(), code,
		"  - pre-push: existing non-lefthook hook in "+filepath.Join(repo.CommonDir, "hooks")+"\n")
	// the foreign hook is left exactly in place (not displaced).
	eq(t, "foreign hook untouched", readFileT(t, hook), "#!/bin/sh\necho team-gate\nexit 1\n")
}

func TestIncumbentPrecommitStub(t *testing.T) {
	dir, repo := initRepo(t)
	stubLefthook(t)
	singleGatePayload(t)
	writeFile(t, filepath.Join(dir, ".pre-commit-config.yaml"), "repos: []\n")
	hook := filepath.Join(repo.CommonDir, "hooks", "pre-commit")
	writeFile(t, hook, "#!/usr/bin/env bash\n# File generated by pre-commit: https://pre-commit.com\nexit 0\n")
	if err := os.Chmod(hook, 0o755); err != nil {
		t.Fatal(err)
	}
	var out, errb strings.Builder
	code := RunInit(nil, &out, &errb)
	assertIncumbentRefusal(t, dir, errb.String(), code,
		"  - pre-commit: installed pre-commit-framework stub (plus .pre-commit-config.yaml)\n")
}

func TestIncumbentForeignHooksPath(t *testing.T) {
	dir, _ := initRepo(t)
	stubLefthook(t)
	singleGatePayload(t)
	runGitT(t, dir, "config", "core.hooksPath", ".husky/_")

	var out, errb strings.Builder
	code := RunInit(nil, &out, &errb)
	assertIncumbentRefusal(t, dir, errb.String(), code,
		"  - core.hooksPath = '.husky/_' (a foreign hook manager owns the hooks dir; husky v9 sets .husky/_)\n")
}

// TestRedundantHooksPathCleared: core.hooksPath pointing at the repo's own hooks
// dir is redundant, not foreign — init clears it (with a stdout notice) and
// succeeds.
func TestRedundantHooksPathCleared(t *testing.T) {
	dir, _ := initRepo(t)
	stubLefthook(t)
	singleGatePayload(t)
	runGitT(t, dir, "config", "core.hooksPath", ".git/hooks")

	var stdout, stderr strings.Builder
	if code := RunInit(nil, &stdout, &stderr); code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "omakase: cleared redundant core.hooksPath (it named the repo's own hooks dir; lefthook refuses to install while it is set — the effective hooks dir is unchanged).\n") {
		t.Errorf("stdout missing cleared-hooksPath notice:\n%s", stdout.String())
	}
	if v := gitOutTrim(dir, "config", "--get", "core.hooksPath"); v != "" {
		t.Errorf("core.hooksPath still set: %q", v)
	}
}

// TestStockLFSAccepted: the four stock `git lfs install` hooks are absorbed by
// lefthook, not treated as a rival manager — init installs cleanly.
func TestStockLFSAccepted(t *testing.T) {
	dir, repo := initRepo(t)
	stubLefthook(t)
	singleGatePayload(t)
	for _, h := range []string{"post-checkout", "post-commit", "post-merge", "pre-push"} {
		hf := filepath.Join(repo.CommonDir, "hooks", h)
		writeFile(t, hf, "#!/bin/sh\ncommand -v git-lfs >/dev/null 2>&1 || { printf >&2 \"%s\" \"no git-lfs\"; exit 2; }\ngit lfs "+h+" \"$@\"\n")
		if err := os.Chmod(hf, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	var out, errb strings.Builder
	if code := RunInit(nil, &out, &errb); code != 0 {
		t.Fatalf("exit = %d, want 0 (LFS repo should install); stderr=%q", code, errb.String())
	}
	if _, err := os.Stat(filepath.Join(dir, ".omakase", "gates", "example.sh")); err != nil {
		t.Errorf("gate not placed in an LFS repo: %v", err)
	}
}

// TestForeignHookAlongsideLFS: a genuine foreign hook next to LFS hooks refuses
// and names only it, never an exempt LFS event.
func TestForeignHookAlongsideLFS(t *testing.T) {
	dir, repo := initRepo(t)
	stubLefthook(t)
	singleGatePayload(t)
	for _, h := range []string{"post-checkout", "post-commit", "post-merge", "pre-push"} {
		hf := filepath.Join(repo.CommonDir, "hooks", h)
		writeFile(t, hf, "#!/bin/sh\ncommand -v git-lfs >/dev/null 2>&1 || exit 2\ngit lfs "+h+" \"$@\"\n")
		if err := os.Chmod(hf, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	foreign := filepath.Join(repo.CommonDir, "hooks", "pre-commit")
	writeFile(t, foreign, "#!/bin/sh\necho team-precommit\nexit 1\n")
	if err := os.Chmod(foreign, 0o755); err != nil {
		t.Fatal(err)
	}
	var out, errb strings.Builder
	code := RunInit(nil, &out, &errb)
	assertIncumbentRefusal(t, dir, errb.String(), code, "  - pre-commit: existing non-lefthook hook in ")
	for _, exempt := range []string{"post-checkout", "post-commit", "post-merge", "pre-push"} {
		if strings.Contains(errb.String(), "  - "+exempt+":") {
			t.Errorf("refusal wrongly named exempt LFS hook %q:\n%s", exempt, errb.String())
		}
	}
}

// ---------------------------------------------------------------- wiring guard

func TestWiringRefusal(t *testing.T) {
	dir, _ := initRepo(t)
	stubLefthook(t)
	log := os.Getenv("LEFTHOOK_STUB_LOG")
	p := singleGatePayload(t)
	// A lefthook-local.yml that wires a script the payload does not ship.
	writeFile(t, filepath.Join(p, "lefthook-local.yml"),
		"pre-commit:\n  jobs:\n    - name: x\n      run: bash .omakase/bin/missing-script.sh\n")

	var stdout, stderr strings.Builder
	code := RunInit(nil, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	wantErr := "omakase: hook wiring references script(s) the payload does not ship: .omakase/bin/missing-script.sh\n" +
		"  These would fail at commit time (exit 127). Fix lefthook-local.yml or ship the script(s). Nothing was placed.\n"
	eq(t, "wiring refusal stderr", stderr.String(), wantErr)
	eq(t, "wiring refusal stdout", stdout.String(), "")
	// Nothing placed; lefthook never resolved/ran (wiring guard precedes it).
	if _, err := os.Stat(filepath.Join(dir, ".omakase")); err == nil {
		t.Error("placed files despite wiring refusal")
	}
	if _, err := os.Stat(log); err == nil {
		t.Error("lefthook install ran despite wiring refusal")
	}
}

// ---------------------------------------------------------------- rotation

func TestLedgerRotation(t *testing.T) {
	_, repo := initRepo(t)
	stubLefthook(t)
	singleGatePayload(t)
	// Seed a pre-v2 6-column ledger row.
	if err := os.MkdirAll(repo.OMK, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(repo.OMK, "ledger.tsv"), "111\thook\tgate\tpass\t5\tdeadbeef\n")

	var stdout, stderr strings.Builder
	if code := RunInit(nil, &stdout, &stderr); code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%q", code, stderr.String())
	}
	if !strings.HasPrefix(stdout.String(), "omakase: rotated a pre-v2 (6-column) run ledger aside to ledger.tsv.pre-v2.bak (the new store starts clean).\n") {
		t.Errorf("stdout missing rotation notice as its first line:\n%s", stdout.String())
	}
	if _, err := os.Stat(filepath.Join(repo.OMK, "ledger.tsv.pre-v2.bak")); err != nil {
		t.Errorf("ledger not rotated aside: %v", err)
	}
	if _, err := os.Stat(filepath.Join(repo.OMK, "ledger.tsv")); !os.IsNotExist(err) {
		t.Errorf("original ledger.tsv still present after rotation")
	}
}

// A 4-column (post-v2) ledger must not rotate. Rotation triggers only at 6+
// columns, so the boundary is pinned on both the common post-v2 case (4 columns,
// this test) and the one-short-of-trigger case (5 columns,
// TestLedgerNoRotationFor5Columns).
func TestLedgerNoRotationFor4Columns(t *testing.T) {
	_, repo := initRepo(t)
	stubLefthook(t)
	singleGatePayload(t)
	if err := os.MkdirAll(repo.OMK, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(repo.OMK, "ledger.tsv"), "111\tname\tpass\tdeadbeefdeadbeef\n")

	var stdout, stderr strings.Builder
	if code := RunInit(nil, &stdout, &stderr); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if strings.Contains(stdout.String(), "rotated a pre-v2") {
		t.Errorf("rotated a non-6-column ledger:\n%s", stdout.String())
	}
	if _, err := os.Stat(filepath.Join(repo.OMK, "ledger.tsv.pre-v2.bak")); err == nil {
		t.Error("created a .pre-v2.bak for a 4-column ledger")
	}
}

// A genuine 5-column ledger row (one column short of the 6-column rotation
// trigger) must also not rotate — pins the boundary from the other side.
func TestLedgerNoRotationFor5Columns(t *testing.T) {
	_, repo := initRepo(t)
	stubLefthook(t)
	singleGatePayload(t)
	if err := os.MkdirAll(repo.OMK, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(repo.OMK, "ledger.tsv"), "111\tname\tpass\tdeadbeefdeadbeef\textra\n")

	var stdout, stderr strings.Builder
	if code := RunInit(nil, &stdout, &stderr); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if strings.Contains(stdout.String(), "rotated a pre-v2") {
		t.Errorf("rotated a non-6-column ledger:\n%s", stdout.String())
	}
	if _, err := os.Stat(filepath.Join(repo.OMK, "ledger.tsv.pre-v2.bak")); err == nil {
		t.Error("created a .pre-v2.bak for a 5-column ledger")
	}
}

// TestLedgerRotationFailureContinues: when the rotate-aside rename fails (here,
// constructed portably by pre-creating ledger.tsv.pre-v2.bak as a directory, so
// os.Rename onto it fails on both macOS and Linux), the failure prints no notice
// and does not abort the run: placement completes, and the pre-v2 ledger is left
// exactly where it was because the rename never happened.
func TestLedgerRotationFailureContinues(t *testing.T) {
	dir, repo := initRepo(t)
	stubLefthook(t)
	singleGatePayload(t)
	if err := os.MkdirAll(repo.OMK, 0o755); err != nil {
		t.Fatal(err)
	}
	const ledgerContent = "111\thook\tgate\tpass\t5\tdeadbeef\n" // 6 columns: triggers rotation
	writeFile(t, filepath.Join(repo.OMK, "ledger.tsv"), ledgerContent)
	// The rotation destination already exists as a directory: os.Rename onto it fails.
	if err := os.MkdirAll(filepath.Join(repo.OMK, "ledger.tsv.pre-v2.bak"), 0o755); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr strings.Builder
	code := RunInit(nil, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (a failed rotate must not abort the run); stderr=%q", code, stderr.String())
	}
	if strings.Contains(stdout.String(), "rotated a pre-v2") {
		t.Errorf("printed the rotation notice despite a failed rename:\n%s", stdout.String())
	}
	// The original ledger is untouched (the rename never happened) ...
	eq(t, "ledger.tsv left in place", readFileT(t, filepath.Join(repo.OMK, "ledger.tsv")), ledgerContent)
	// ... and the pre-existing directory at the rotation destination is untouched.
	if info, err := os.Stat(filepath.Join(repo.OMK, "ledger.tsv.pre-v2.bak")); err != nil || !info.IsDir() {
		t.Errorf(".pre-v2.bak directory disturbed: %v", err)
	}
	// The rest of the run still completed: placement, hooks, clean git status.
	if _, err := os.Stat(filepath.Join(dir, ".omakase", "gates", "example.sh")); err != nil {
		t.Errorf("run did not continue past the failed rotation: %v", err)
	}
	if out := gitStdout(dir, "status", "--porcelain"); out != "" {
		t.Errorf("git status not clean: %q", out)
	}
}

// ---------------------------------------------------- multi-file walk order

// TestMultiFilePlacedTsv asserts lexical walk order across a multi-top-dir
// payload — placed.tsv rows, per-path kind, and the .github file-by-file vs
// owned-wholesale exclude derivation.
func TestMultiFilePlacedTsv(t *testing.T) {
	dir, repo := initRepo(t)
	stubLefthook(t)
	p := t.TempDir()
	t.Setenv("OMAKASE_PAYLOAD", p)
	writeFile(t, filepath.Join(p, ".claude", "rules", "a.md"), "rule a\n")
	writeFile(t, filepath.Join(p, ".claude", "skills", "b", "SKILL.md"), "skill b\n")
	writeFile(t, filepath.Join(p, ".omakase", "gates", "example.sh"), gateContent)
	writeFile(t, filepath.Join(p, ".github", "skills", "foo", "SKILL.md"), "gh skill\n")
	writeFile(t, filepath.Join(p, "AGENTS.md"), "agents\n")

	var stdout, stderr strings.Builder
	if code := RunInit(nil, &stdout, &stderr); code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%q", code, stderr.String())
	}

	// WalkDir lexical order: .claude/* , .github/* , .omakase/* , AGENTS.md.
	h := func(s string) string { return sha256hex([]byte(s)) }
	wantPlaced := "" +
		".claude/rules/a.md\trule\tpayload\t" + h("rule a\n") + "\t1\n" +
		".claude/skills/b/SKILL.md\tskill\tpayload\t" + h("skill b\n") + "\t1\n" +
		".github/skills/foo/SKILL.md\tskill\tpayload\t" + h("gh skill\n") + "\t1\n" +
		".omakase/gates/example.sh\tgate\tpayload\t" + h(gateContent) + "\t1\n" +
		"AGENTS.md\tdoc\tpayload\t" + h("agents\n") + "\t1\n"
	eq(t, "multi placed.tsv", readFileT(t, filepath.Join(repo.OMK, "placed.tsv")), wantPlaced)

	// exclude block: .claude owned (wholesale), .github shared (file-by-file),
	// .omakase owned, AGENTS.md file, then wiring entries — all in walk order,
	// every entry root-anchored with a leading "/".
	wantBlock := "# >>> omakase-harness >>>\n" +
		"/.claude/\n" +
		"/.github/skills/foo/SKILL.md\n" +
		"/.omakase/\n" +
		"/AGENTS.md\n" +
		"/lefthook.yml\n" +
		"/.worktreeinclude\n" +
		"# <<< omakase-harness <<<\n"
	excl := readFileT(t, filepath.Join(repo.CommonDir, "info", "exclude"))
	if !strings.Contains(excl, wantBlock) {
		t.Errorf("exclude block mismatch:\n got:\n%s\nwant block:\n%s", excl, wantBlock)
	}
	if out := gitStdout(dir, "status", "--porcelain"); out != "" {
		t.Errorf("git status not clean: %q", out)
	}
}

// TestSymlinkPayloadCarried: a payload symlink (CLAUDE.md -> AGENTS.md) lands as
// a symlink, snapshots as a symlink, and its ledger digest is the readlink
// target string's sha256 (not the dereferenced content).
func TestSymlinkPayloadCarried(t *testing.T) {
	dir, repo := initRepo(t)
	stubLefthook(t)
	p := t.TempDir()
	t.Setenv("OMAKASE_PAYLOAD", p)
	writeFile(t, filepath.Join(p, "AGENTS.md"), "doctrine\n")
	if err := os.Symlink("AGENTS.md", filepath.Join(p, "CLAUDE.md")); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr strings.Builder
	if code := RunInit(nil, &stdout, &stderr); code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%q", code, stderr.String())
	}
	target, err := os.Readlink(filepath.Join(dir, "CLAUDE.md"))
	if err != nil || target != "AGENTS.md" {
		t.Errorf("CLAUDE.md not a symlink to AGENTS.md: target=%q err=%v", target, err)
	}
	snapTarget, err := os.Readlink(filepath.Join(repo.OMK, "payload-snapshot", "CLAUDE.md"))
	if err != nil || snapTarget != "AGENTS.md" {
		t.Errorf("snapshot dereferenced the symlink: target=%q err=%v", snapTarget, err)
	}
	// ledger row for the symlink uses the target-string digest.
	wantRow := "CLAUDE.md\tdoc\tpayload\t" + sha256hex([]byte("AGENTS.md")) + "\t1\n"
	if !strings.Contains(readFileT(t, filepath.Join(repo.OMK, "placed.tsv")), wantRow) {
		t.Errorf("placed.tsv missing symlink-digest row %q:\n%s", wantRow, readFileT(t, filepath.Join(repo.OMK, "placed.tsv")))
	}
	if out := gitStdout(dir, "status", "--porcelain"); out != "" {
		t.Errorf("git status not clean (symlink gitignored): %q", out)
	}
}

// ---------------------------------------------------- usage / arg errors

func TestUsageAndArgErrors(t *testing.T) {
	// These exit before repo discovery, so no fixture repo is needed for the
	// error arms; --help exits 0 with usage on stdout.
	cases := []struct {
		name     string
		argv     []string
		wantCode int
		wantOut  string // stdout
		errCheck func(string) bool
	}{
		{
			name: "help", argv: []string{"--help"}, wantCode: 0, wantOut: usageText,
			errCheck: func(s string) bool { return s == "" },
		},
		{
			name: "unknown option", argv: []string{"--bogus"}, wantCode: 2, wantOut: "",
			errCheck: func(s string) bool { return s == "omakase: unknown option '--bogus'\n"+usageText },
		},
		{
			name: "source needs arg", argv: []string{"--source"}, wantCode: 2, wantOut: "",
			errCheck: func(s string) bool { return s == "omakase: --source needs a git URL or local path\n" },
		},
		{
			name: "extra positional", argv: []string{"a/b", "c/d"}, wantCode: 2, wantOut: "",
			errCheck: func(s string) bool {
				return s == "omakase: unexpected extra argument 'c/d' (source already set)\n"+usageText
			},
		},
		{
			name: "tab in source", argv: []string{"a\tb"}, wantCode: 2, wantOut: "",
			errCheck: func(s string) bool { return s == "omakase: --source must not contain a tab or newline\n" },
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr strings.Builder
			code := RunInit(tc.argv, &stdout, &stderr)
			if code != tc.wantCode {
				t.Errorf("exit = %d, want %d", code, tc.wantCode)
			}
			eq(t, "stdout", stdout.String(), tc.wantOut)
			if !tc.errCheck(stderr.String()) {
				t.Errorf("stderr unexpected:\n%q", stderr.String())
			}
		})
	}
}

func TestNotAGitRepo(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	var stdout, stderr strings.Builder
	code := RunInit(nil, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	eq(t, "stderr", stderr.String(), "omakase: not inside a git repo\n")
	eq(t, "stdout", stdout.String(), "")
}

func TestPayloadNotFound(t *testing.T) {
	initRepo(t)
	stubLefthook(t)
	missing := filepath.Join(t.TempDir(), "no-such-payload")
	t.Setenv("OMAKASE_PAYLOAD", missing)

	var stdout, stderr strings.Builder
	code := RunInit(nil, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	eq(t, "stderr", stderr.String(), "omakase: payload dir not found at "+missing+"\n")
}

// ---------------------------------------------------- source precedence
// The full --source flow (cache, manifest, ref pin, base+delta merge, source
// memory) is covered in source_test.go. These two pin the precedence edges the
// engine owns.

// OMAKASE_PAYLOAD set alongside a remembered $OMK/source: the env override wins,
// so the source is not taken and a plain payload install proceeds (precedence:
// env > remembered).
func TestOmakasePayloadOverridesRememberedSource(t *testing.T) {
	dir, repo := initRepo(t)
	stubLefthook(t)
	singleGatePayload(t) // sets OMAKASE_PAYLOAD
	if err := os.MkdirAll(repo.OMK, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(repo.OMK, "source"), "you/harness\n")

	var stdout, stderr strings.Builder
	if code := RunInit(nil, &stdout, &stderr); code != 0 {
		t.Fatalf("exit = %d, want 0 (env override should install); stderr=%q", code, stderr.String())
	}
	if _, err := os.Stat(filepath.Join(dir, ".omakase", "gates", "example.sh")); err != nil {
		t.Errorf("payload not installed under an env override: %v", err)
	}
}

// TestPlainInstallPayloadEnvPrecedence: on a plain (no --source) install,
// OMAKASE_PAYLOAD wins over OMAKASE_BASE_PAYLOAD — the base-payload env is the
// merge base only, never the plain payload, so it must not leak into the plain
// path even when set. Two distinct fixtures prove which one is placed.
func TestPlainInstallPayloadEnvPrecedence(t *testing.T) {
	dir, _ := initRepo(t)
	stubLefthook(t)
	clearBasePayloadOverride(t)

	payloadDir := t.TempDir()
	writeFile(t, filepath.Join(payloadDir, ".omakase", "gates", "chosen.sh"), "chosen\n")
	t.Setenv("OMAKASE_PAYLOAD", payloadDir)

	baseDir := t.TempDir()
	writeFile(t, filepath.Join(baseDir, ".omakase", "gates", "ignored.sh"), "ignored\n")
	t.Setenv("OMAKASE_BASE_PAYLOAD", baseDir)

	var stdout, stderr strings.Builder
	if code := RunInit(nil, &stdout, &stderr); code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%q", code, stderr.String())
	}
	eq(t, "OMAKASE_PAYLOAD file placed", readFileT(t, filepath.Join(dir, ".omakase", "gates", "chosen.sh")), "chosen\n")
	if _, err := os.Stat(filepath.Join(dir, ".omakase", "gates", "ignored.sh")); err == nil {
		t.Error("OMAKASE_BASE_PAYLOAD file placed on a plain install — the base-payload env leaked into the plain path")
	}
}

// TestUntrackedHuskyExemptWhenPayloadShips: an untracked .husky matching a
// payload that ships one is omakase's own — exempt, so init proceeds. A .husky is
// only flagged when the payload does not ship one.
func TestUntrackedHuskyExemptWhenPayloadShips(t *testing.T) {
	dir, _ := initRepo(t)
	stubLefthook(t)
	p := singleGatePayload(t)
	writeFile(t, filepath.Join(p, ".husky", "pre-commit"), "#!/bin/sh\ntrue\n")
	writeFile(t, filepath.Join(dir, ".husky", "pre-commit"), "#!/bin/sh\ntrue\n") // untracked

	var out, errb strings.Builder
	if code := RunInit(nil, &out, &errb); code != 0 {
		t.Fatalf("exit = %d, want 0 (untracked payload-matching .husky is exempt); stderr=%q", code, errb.String())
	}
}

// TestCustomizedGitLfsHookRefuses: a hook that forwards to git-lfs and does extra
// work is not the pristine stub — it still refuses.
func TestCustomizedGitLfsHookRefuses(t *testing.T) {
	dir, repo := initRepo(t)
	stubLefthook(t)
	singleGatePayload(t)
	hf := filepath.Join(repo.CommonDir, "hooks", "pre-push")
	writeFile(t, hf, "#!/bin/sh\nnpm run lint || exit 1\ncommand -v git-lfs >/dev/null 2>&1 || exit 2\ngit lfs pre-push \"$@\"\n")
	if err := os.Chmod(hf, 0o755); err != nil {
		t.Fatal(err)
	}
	var out, errb strings.Builder
	code := RunInit(nil, &out, &errb)
	assertIncumbentRefusal(t, dir, errb.String(), code, "  - pre-push: existing non-lefthook hook in ")
	eq(t, "customized hook untouched", readFileT(t, hf), "#!/bin/sh\nnpm run lint || exit 1\ncommand -v git-lfs >/dev/null 2>&1 || exit 2\ngit lfs pre-push \"$@\"\n")
}

// TestPlaceFileRefusesRealDir: an untracked real directory where the payload
// ships a regular file makes placement refuse — exit 1, leaving the directory
// untouched.
func TestPlaceFileRefusesRealDir(t *testing.T) {
	dir, _ := initRepo(t)
	stubLefthook(t)
	singleGatePayload(t)
	// A real directory sitting exactly where the payload's example.sh must land.
	if err := os.MkdirAll(filepath.Join(dir, ".omakase", "gates", "example.sh"), 0o755); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr strings.Builder
	code := RunInit(nil, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	eq(t, "real-dir refusal", stderr.String(),
		"omakase: refusing to overlay file '.omakase/gates/example.sh' — an untracked directory exists there; remove it and re-run\n")
	if info, err := os.Stat(filepath.Join(dir, ".omakase", "gates", "example.sh")); err != nil || !info.IsDir() {
		t.Errorf("the untracked directory was disturbed: %v", err)
	}
}

// ---------------------------------------------------------------- symlink escape (security)
//
// safeMkdirAll refuses to create a directory whose parent chain passes through
// an existing symlink, so a payload write can never land outside the repo
// through a symlinked parent. TestSafeMkdirAllRefusesSymlinkedParent
// (overlay_test.go) exercises the primitive directly; this test drives the guard
// end to end through RunInit's placeFile call site, so a refactor that drops
// safeMkdirAll there is caught by the wiring, not just the primitive.
//
// Setup: the target repo has an untracked directory symlink "evil" pointing
// outside the repo. The payload ships a plain file "evil/pwned" under that same
// name. Following the symlink would write pwned outside the repo; safeMkdirAll
// must refuse before that write happens.
func TestPlaceFileRefusesSymlinkedParentInRepo(t *testing.T) {
	dir, _ := initRepo(t)
	stubLefthook(t)

	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(dir, "evil")); err != nil {
		t.Fatal(err)
	}

	p := t.TempDir()
	t.Setenv("OMAKASE_PAYLOAD", p)
	writeFile(t, filepath.Join(p, "evil", "pwned"), "malicious content\n")

	var stdout, stderr strings.Builder
	code := RunInit(nil, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("exit = %d, want 1 (a symlinked parent in the repo must be refused); stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "safeMkdirAll: refusing to create") {
		t.Errorf("stderr missing the safeMkdirAll refusal:\n%s", stderr.String())
	}
	eq(t, "stdout (nothing placed)", stdout.String(), "")
	if _, err := os.Stat(filepath.Join(outside, "pwned")); !os.IsNotExist(err) {
		t.Errorf("write escaped through the symlink: %s exists (statErr=%v)", filepath.Join(outside, "pwned"), err)
	}
}

// TestTrackedWorktreeincludeNotice: a git-tracked .worktreeinclude is left
// untouched (appending would be a committed footprint) — init prints the notice
// to stderr and writes no wtinc block.
func TestTrackedWorktreeincludeNotice(t *testing.T) {
	dir, _ := initRepo(t)
	stubLefthook(t)
	singleGatePayload(t)
	writeFile(t, filepath.Join(dir, ".worktreeinclude"), "manual\n")
	runGitT(t, dir, "add", ".worktreeinclude")
	runGitT(t, dir, "commit", "-q", "-m", "wtinc")

	var stdout, stderr strings.Builder
	if code := RunInit(nil, &stdout, &stderr); code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%q", code, stderr.String())
	}
	eq(t, "tracked wtinc notice", stderr.String(),
		"omakase: .worktreeinclude is tracked — leaving it untouched (re-run omakase init inside a new manual worktree to install it there).\n")
	// the tracked file is left untouched (no block appended).
	eq(t, "tracked .worktreeinclude untouched", readFileT(t, filepath.Join(dir, ".worktreeinclude")), "manual\n")
}

// TestWtincBlockOmitsPlacedWorktreeinclude: a payload shipping a top-level
// .worktreeinclude file (not just the wiring entry) still must not appear in the
// generated .worktreeinclude block — the block skips every prefix equal to
// ".worktreeinclude", whether the prefix came from the wiring append or from a
// placed path. The exclude block, by contrast, does list it.
func TestWtincBlockOmitsPlacedWorktreeinclude(t *testing.T) {
	dir, repo := initRepo(t)
	stubLefthook(t)
	p := singleGatePayload(t)
	writeFile(t, filepath.Join(p, ".worktreeinclude"), "custom-pattern\n")

	var stdout, stderr strings.Builder
	if code := RunInit(nil, &stdout, &stderr); code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%q", code, stderr.String())
	}
	eq(t, "stderr", stderr.String(), "")

	wantOut := "omakase: placed 2 file(s), overwrote 0 to match payload, skipped 0 committed path(s).\n" +
		"  + .omakase/gates/example.sh\n" +
		"  + .worktreeinclude\n" + summaryTail
	eq(t, "stdout", stdout.String(), wantOut)

	// exclude block: DOES list .worktreeinclude (v1 has no skip there).
	// Entries are root-anchored (leading "/") in the exclude block only.
	wantExcludeBlock := "# >>> omakase-harness >>>\n" +
		"/.omakase/\n" +
		"/.worktreeinclude\n" +
		"/lefthook.yml\n" +
		"# <<< omakase-harness <<<\n"
	excl := readFileT(t, filepath.Join(repo.CommonDir, "info", "exclude"))
	if !strings.Contains(excl, wantExcludeBlock) {
		t.Errorf("exclude block mismatch:\n got:\n%s\nwant block:\n%s", excl, wantExcludeBlock)
	}

	// .worktreeinclude: the payload's own content (unstripped — no marker
	// block present yet), THEN the generated block with NO .worktreeinclude
	// entry (neither the wiring append nor the placed-path prefix survives).
	wantWtinc := "custom-pattern\n" +
		"# >>> omakase-harness >>>\n" +
		".omakase/\n" +
		"lefthook.yml\n" +
		"# <<< omakase-harness <<<\n"
	eq(t, "wtinc", readFileT(t, filepath.Join(dir, ".worktreeinclude")), wantWtinc)

	if out := gitStdout(dir, "status", "--porcelain"); out != "" {
		t.Errorf("git status not clean: %q", out)
	}
}

// TestStatuslineAndStopNoticeStanzas: the closing summary appends the statusline
// + stop-notice wire-up stanzas iff those files exist in the repo after
// placement, including the repo-root path line.
func TestStatuslineAndStopNoticeStanzas(t *testing.T) {
	_, repo := initRepo(t)
	stubLefthook(t)
	p := t.TempDir()
	t.Setenv("OMAKASE_PAYLOAD", p)
	writeFile(t, filepath.Join(p, ".omakase", "bin", "omakase-statusline.sh"), "#!/bin/sh\n")
	writeFile(t, filepath.Join(p, ".omakase", "bin", "omakase-stop-notice.sh"), "#!/bin/sh\n")

	var stdout, stderr strings.Builder
	if code := RunInit(nil, &stdout, &stderr); code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%q", code, stderr.String())
	}
	// WalkDir lexical: omakase-statusline.sh before omakase-stop-notice.sh. The
	// path line prints repo.Root (git's normalized toplevel), not the temp dir.
	wantOut := "omakase: placed 2 file(s), overwrote 0 to match payload, skipped 0 committed path(s).\n" +
		"  + .omakase/bin/omakase-statusline.sh\n" +
		"  + .omakase/bin/omakase-stop-notice.sh\n" +
		summaryTail +
		"omakase: status line — compose the scorecard into your existing bar (it never\n" +
		"         takes over the bar). Add this command to your status-line script:\n" +
		"           bash " + repo.Root + "/.omakase/bin/omakase-statusline.sh\n" +
		"         Claude Code: your ~/.claude statusLine script. Copilot CLI: ~/.copilot. tmux: status-right.\n" +
		"omakase: end-of-turn notice (Claude Code only, opt-in) — a one-line 'harness active'\n" +
		"         status when a turn ends. Enable by adding a Stop hook to .claude/settings.json:\n" +
		"           bash $CLAUDE_PROJECT_DIR/.omakase/bin/omakase-stop-notice.sh\n"
	eq(t, "summary with stanzas", stdout.String(), wantOut)
}

// ------------------------------------------------------------ exclude/wtinc write mode
//
// init rewrites the exclude and .worktreeinclude files so their final mode is
// `0666 &^ umask` regardless of the pre-existing mode. Each test below seeds a
// pre-existing file at 0600 (deliberately not `0666 &^ umask`) so a port that
// silently preserves the original mode (os.WriteFile over an existing path only
// applies its mode argument at creation) is caught.

// TestExcludeWriteModeMatchesBashFreshInode: exclude pre-seeded at 0600 must
// end up at `0666 &^ umask` after init's strip+append.
func TestExcludeWriteModeMatchesBashFreshInode(t *testing.T) {
	_, repo := initRepo(t)
	stubLefthook(t)
	singleGatePayload(t)

	exclude := filepath.Join(repo.CommonDir, "info", "exclude")
	writeFile(t, exclude, "scratch/\n")
	if err := os.Chmod(exclude, 0o600); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr strings.Builder
	if code := RunInit(nil, &stdout, &stderr); code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%q", code, stderr.String())
	}

	info, err := os.Stat(exclude)
	if err != nil {
		t.Fatal(err)
	}
	want := os.FileMode(0o666) &^ currentUmask()
	if info.Mode().Perm() != want {
		t.Errorf("exclude mode after init = %o, want %o (0666 &^ umask -- the original seeded 0600 must NOT survive)", info.Mode().Perm(), want)
	}
}

// TestWtincWriteModeMatchesBashFreshInode: .worktreeinclude pre-seeded at 0600
// must end up at `0666 &^ umask` after init's strip+append. singleGatePayload
// guarantees len(placed) > 0, which is required for this block to be written at
// all.
func TestWtincWriteModeMatchesBashFreshInode(t *testing.T) {
	dir, _ := initRepo(t)
	stubLefthook(t)
	singleGatePayload(t)

	wtinc := filepath.Join(dir, ".worktreeinclude")
	writeFile(t, wtinc, "my-own-ignore/\n")
	if err := os.Chmod(wtinc, 0o600); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr strings.Builder
	if code := RunInit(nil, &stdout, &stderr); code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%q", code, stderr.String())
	}

	info, err := os.Stat(wtinc)
	if err != nil {
		t.Fatal(err)
	}
	want := os.FileMode(0o666) &^ currentUmask()
	if info.Mode().Perm() != want {
		t.Errorf("wtinc mode after init = %o, want %o (0666 &^ umask -- the original seeded 0600 must NOT survive)", info.Mode().Perm(), want)
	}
}
