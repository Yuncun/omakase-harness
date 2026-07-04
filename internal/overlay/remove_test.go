package overlay

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// These are the RunRemove integration tests. Every byte-exact expectation
// that isn't a pure derivation of an earlier RunInit call (the "nothing
// installed" line, the final summary line, the hook-stub strip/chmod
// behavior including its substring-gate quirk, the pre-0.10 fallback) was
// validated against a live `bash bin/remove.sh` run of the same fixture
// before being frozen — the same "bash-byte-capture" discipline
// init_test.go's header documents. Shared helpers (initRepo, stubLefthook,
// singleGatePayload, writeFile, readFileT, eq, chdir, runGitT, gitStdout,
// gateContent, summaryTail) live in init_test.go / overlay_test.go, same
// package.
//
// Global Constraint 6 (walk order) applies here only to the pre-0.10
// payload-enumeration fallback; every other loop in remove.go walks
// existing state (a ledger, a hooks dir listing) in FILE/LEXICAL order,
// matching bash exactly — no divergence to account for in these fixtures.

// mustInit runs a fresh RunInit and fails the test immediately if it does
// not exit 0 -- the "a real install already happened" precondition several
// scenarios below need. Callers already set up stubLefthook + a payload
// (singleGatePayload or equivalent).
func mustInit(t *testing.T) {
	t.Helper()
	var stdout, stderr strings.Builder
	if code := RunInit(nil, &stdout, &stderr); code != 0 {
		t.Fatalf("RunInit precondition failed: exit=%d stderr=%q", code, stderr.String())
	}
}

const removedLine = "omakase: removed. Hooks uninstalled, placed files deleted, worktree snapshot + exclude block stripped.\n"

// ---------------------------------------------------------------- full teardown

// TestFullTeardownAfterInit is the round-trip proof: everything a real
// RunInit placed or wrote is gone or restored byte-exact afterward. The two
// hook-stub fixtures are hand-seeded in EXACTLY the shape
// internal/templates/files/install-guards.sh writes (verified by reading
// that file), at mode 644 (non-executable) so the chmod-restoration step is
// meaningfully exercised rather than a silent no-op — real lefthook-managed
// stubs are always executable already, which is why bash's own awk>tmp&&mv
// dance (losing the bit) plus the trailing chmod +x (restoring it) is
// usually invisible; forcing the "before" state to non-executable here
// proves the chmod actually ran instead of the file merely staying as it was.
func TestFullTeardownAfterInit(t *testing.T) {
	dir, repo := initRepo(t)
	log := stubLefthook(t)
	singleGatePayload(t)
	writeFile(t, filepath.Join(repo.CommonDir, "info", "exclude"), "scratch/\n*.tmp\n")
	mustInit(t)

	preCommit := filepath.Join(repo.CommonDir, "hooks", "pre-commit")
	preCommitBefore := "#!/bin/sh\n" +
		"# >>> omakase-harness fail-closed >>>\n" +
		"omakase_verify=\"$(git rev-parse --git-common-dir)/omakase/verify-overlay.sh\"\n" +
		"if [ -f \"$omakase_verify\" ]; then\n" +
		"  sh \"$omakase_verify\" || exit 1\n" +
		"fi\n" +
		"# <<< omakase-harness fail-closed <<<\n" +
		"call_lefthook run \"pre-commit\" \"$@\"\n"
	writeFile(t, preCommit, preCommitBefore)
	if err := os.Chmod(preCommit, 0o644); err != nil {
		t.Fatal(err)
	}

	postCheckout := filepath.Join(repo.CommonDir, "hooks", "post-checkout")
	postCheckoutBefore := "#!/bin/sh\n" +
		"# >>> omakase-harness worktree-bootstrap >>>\n" +
		"omakase_ensure=\"$(git rev-parse --git-common-dir)/omakase/ensure-present.sh\"\n" +
		"if [ -f \"$omakase_ensure\" ]; then\n" +
		"  bash \"$omakase_ensure\" || true\n" +
		"fi\n" +
		"# <<< omakase-harness worktree-bootstrap <<<\n" +
		"call_lefthook run \"post-checkout\" \"$@\"\n"
	writeFile(t, postCheckout, postCheckoutBefore)
	if err := os.Chmod(postCheckout, 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr strings.Builder
	code := RunRemove(nil, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%q", code, stderr.String())
	}
	eq(t, "stdout", stdout.String(), removedLine)
	eq(t, "stderr", stderr.String(), "")
	eq(t, "lefthook argv (install then uninstall)", readFileT(t, log), "install\nuninstall\n")

	// Placed file gone, empty parent dirs pruned all the way to repo root.
	if _, err := os.Lstat(filepath.Join(dir, ".omakase")); !os.IsNotExist(err) {
		t.Errorf(".omakase not pruned away: %v", err)
	}

	// exclude restored BYTE-EXACT to the pre-init seeded bytes.
	eq(t, "exclude restored", readFileT(t, filepath.Join(repo.CommonDir, "info", "exclude")), "scratch/\n*.tmp\n")

	// wtinc: init's only content was the derived block, so stripping it
	// leaves zero bytes -- the file must be DELETED ([ -s ] false).
	if _, err := os.Lstat(filepath.Join(dir, ".worktreeinclude")); !os.IsNotExist(err) {
		t.Errorf(".worktreeinclude not deleted (should be empty after strip): %v", err)
	}

	// $OMK gone.
	if _, err := os.Lstat(repo.OMK); !os.IsNotExist(err) {
		t.Errorf("$OMK still exists: %v", err)
	}

	// hook stubs: block stripped (only the non-block lines survive), file
	// restored executable.
	eq(t, "pre-commit stripped", readFileT(t, preCommit), "#!/bin/sh\ncall_lefthook run \"pre-commit\" \"$@\"\n")
	eq(t, "post-checkout stripped", readFileT(t, postCheckout), "#!/bin/sh\ncall_lefthook run \"post-checkout\" \"$@\"\n")
	for _, hf := range []string{preCommit, postCheckout} {
		info, err := os.Stat(hf)
		if err != nil || info.Mode().Perm()&0o100 == 0 {
			t.Errorf("%s not executable after remove: %v", hf, err)
		}
	}
}

// TestHookStubSubstringGateQuirk pins the deliberate mismatch between
// remove.sh's strip GATE (grep -qF -- a plain substring test anywhere in
// the file) and the strip itself (textblock.Strip -- only drops lines EQUAL
// to the marker): a begin marker that appears only as a mid-line substring
// still fires the gate (so chmod +x still runs) even though the strip is a
// complete no-op on content (no line equals the marker). Verified against a
// live `bash bin/remove.sh` run of this exact fixture.
func TestHookStubSubstringGateQuirk(t *testing.T) {
	_, repo := initRepo(t)
	stubLefthook(t)
	singleGatePayload(t)
	mustInit(t)

	hf := filepath.Join(repo.CommonDir, "hooks", "pre-commit")
	content := "#!/bin/sh\n" +
		"echo \"look: # >>> omakase-harness fail-closed >>> embedded mid-line\"\n" +
		"call_lefthook run \"pre-commit\" \"$@\"\n"
	writeFile(t, hf, content)
	if err := os.Chmod(hf, 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr strings.Builder
	if code := RunRemove(nil, &stdout, &stderr); code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%q", code, stderr.String())
	}
	eq(t, "content unchanged (no line equals the marker)", readFileT(t, hf), content)
	info, err := os.Stat(hf)
	if err != nil || info.Mode().Perm()&0o100 == 0 {
		t.Errorf("hook not made executable despite the gate firing: %v", err)
	}
}

// ---------------------------------------------------------------- no-op paths

// TestNeverInstalledNoSentinelIsANoOp: a repo omakase never touched has no
// ledger, no exclude block, no $OMK -- RunRemove must report the "nothing
// installed" line on stderr and exit 0 without touching anything. A stray
// argv is also passed here to pin that RunRemove ignores its argv entirely
// (bin/remove.sh has no arg-parsing at all).
func TestNeverInstalledNoSentinelIsANoOp(t *testing.T) {
	initRepo(t)
	stubLefthook(t)

	var stdout, stderr strings.Builder
	code := RunRemove([]string{"stray", "--args", "ignored"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	eq(t, "stdout", stdout.String(), "")
	eq(t, "stderr", stderr.String(), "omakase: nothing installed here; nothing to remove.\n")
}

// TestDoubleRemoveIsANoOp: after a full remove already ran, the ledger is
// gone, the exclude block is already stripped, and $OMK no longer exists --
// a second remove must land on the same "nothing installed" path and change
// nothing further.
func TestDoubleRemoveIsANoOp(t *testing.T) {
	_, repo := initRepo(t)
	stubLefthook(t)
	singleGatePayload(t)
	mustInit(t)

	var o1, e1 strings.Builder
	if code := RunRemove(nil, &o1, &e1); code != 0 {
		t.Fatalf("first remove exit = %d; stderr=%q", code, e1.String())
	}
	excludeAfter1 := readFileT(t, filepath.Join(repo.CommonDir, "info", "exclude"))

	var stdout, stderr strings.Builder
	code := RunRemove(nil, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("second remove exit = %d, want 0", code)
	}
	eq(t, "stdout", stdout.String(), "")
	eq(t, "stderr", stderr.String(), "omakase: nothing installed here; nothing to remove.\n")
	if _, err := os.Lstat(repo.OMK); !os.IsNotExist(err) {
		t.Errorf("$OMK reappeared: %v", err)
	}
	eq(t, "exclude unchanged by the second remove", readFileT(t, filepath.Join(repo.CommonDir, "info", "exclude")), excludeAfter1)
}

// ---------------------------------------------------------------- pre-0.10 fallback

// TestPre010FallbackPayloadEnumeration: no placed.tsv ever existed, but the
// exclude block IS present -- the sentinel a pre-0.10 install always left.
// remove must fall back to enumerating the payload and delete every file it
// finds there, byte-capture-verified against a live `bash bin/remove.sh`
// run of this exact fixture (hand-placed files mirroring what a pre-0.10
// init would have copied, since there is no ledger to drive deletion from).
func TestPre010FallbackPayloadEnumeration(t *testing.T) {
	dir, repo := initRepo(t)
	stubLefthook(t)
	p := t.TempDir()
	writeFile(t, filepath.Join(p, "foo", "a.txt"), "a\n")
	writeFile(t, filepath.Join(p, "b.txt"), "b\n")
	t.Setenv("OMAKASE_PAYLOAD", p)

	writeFile(t, filepath.Join(repo.CommonDir, "info", "exclude"),
		"scratch/\n*.tmp\n# >>> omakase-harness >>>\nfoo/\nb.txt\n# <<< omakase-harness <<<\n")
	writeFile(t, filepath.Join(dir, "foo", "a.txt"), "a\n")
	writeFile(t, filepath.Join(dir, "b.txt"), "b\n")

	var stdout, stderr strings.Builder
	code := RunRemove(nil, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%q", code, stderr.String())
	}
	eq(t, "stdout", stdout.String(), removedLine)
	eq(t, "stderr", stderr.String(), "")
	if _, err := os.Lstat(filepath.Join(dir, "foo")); !os.IsNotExist(err) {
		t.Errorf("foo/ not pruned away: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(dir, "b.txt")); !os.IsNotExist(err) {
		t.Errorf("b.txt not deleted: %v", err)
	}
	eq(t, "exclude stripped", readFileT(t, filepath.Join(repo.CommonDir, "info", "exclude")), "scratch/\n*.tmp\n")
}

// TestSentinelViaOmkDirWithoutExcludeBlock covers the OTHER half of the
// sentinel OR (remove.sh:59): no ledger, no exclude block at all, but a
// leftover $OMK dir (e.g. from an interrupted prior remove) is enough on
// its own to authorize the payload-enumeration fallback.
func TestSentinelViaOmkDirWithoutExcludeBlock(t *testing.T) {
	dir, repo := initRepo(t)
	stubLefthook(t)
	p := t.TempDir()
	writeFile(t, filepath.Join(p, "c.txt"), "c\n")
	t.Setenv("OMAKASE_PAYLOAD", p)

	if err := os.MkdirAll(repo.OMK, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "c.txt"), "c\n")

	var stdout, stderr strings.Builder
	code := RunRemove(nil, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%q", code, stderr.String())
	}
	if _, err := os.Lstat(filepath.Join(dir, "c.txt")); !os.IsNotExist(err) {
		t.Errorf("c.txt not deleted: %v", err)
	}
	if _, err := os.Lstat(repo.OMK); !os.IsNotExist(err) {
		t.Errorf("$OMK not removed: %v", err)
	}
}

// ---------------------------------------------------------------- skeleton lefthook.yml

// TestTrackedLefthookYmlPreserved: a lefthook.yml the repo COMMITS is never
// touched, even though its content matches the skeleton's "EXAMPLE USAGE"
// marker -- trackedness is checked first (remove.sh:73).
func TestTrackedLefthookYmlPreserved(t *testing.T) {
	dir, _ := initRepo(t)
	stubLefthook(t)
	singleGatePayload(t)
	mustInit(t)

	content := "# EXAMPLE USAGE:\n# tracked by the repo\n"
	writeFile(t, filepath.Join(dir, "lefthook.yml"), content)
	// -f: lefthook.yml is listed in the exclude block init just wrote, so a
	// plain `git add` would refuse it as ignored.
	runGitT(t, dir, "add", "-f", "lefthook.yml")
	runGitT(t, dir, "commit", "-q", "-m", "track lefthook.yml")

	var stdout, stderr strings.Builder
	if code := RunRemove(nil, &stdout, &stderr); code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%q", code, stderr.String())
	}
	eq(t, "tracked lefthook.yml preserved", readFileT(t, filepath.Join(dir, "lefthook.yml")), content)
}

// TestUntrackedLefthookYmlWithoutMarkerPreserved: an untracked lefthook.yml
// that is the project's OWN real config (no "EXAMPLE USAGE" skeleton
// banner) must survive -- remove only deletes the auto-created skeleton.
func TestUntrackedLefthookYmlWithoutMarkerPreserved(t *testing.T) {
	dir, _ := initRepo(t)
	stubLefthook(t)
	singleGatePayload(t)
	mustInit(t)

	content := "pre-commit:\n  jobs:\n    - run: echo hi\n"
	writeFile(t, filepath.Join(dir, "lefthook.yml"), content)

	var stdout, stderr strings.Builder
	if code := RunRemove(nil, &stdout, &stderr); code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%q", code, stderr.String())
	}
	eq(t, "custom lefthook.yml preserved", readFileT(t, filepath.Join(dir, "lefthook.yml")), content)
}

// TestUntrackedSkeletonLefthookYmlDeleted: an untracked lefthook.yml
// carrying the "EXAMPLE USAGE" banner (lefthook's own default, auto-created
// by `lefthook install` when no config existed) is exactly what remove.sh
// is willing to delete.
func TestUntrackedSkeletonLefthookYmlDeleted(t *testing.T) {
	dir, _ := initRepo(t)
	stubLefthook(t)
	singleGatePayload(t)
	mustInit(t)

	writeFile(t, filepath.Join(dir, "lefthook.yml"), "# EXAMPLE USAGE:\n#\n#   see https://lefthook.dev\n")

	var stdout, stderr strings.Builder
	if code := RunRemove(nil, &stdout, &stderr); code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%q", code, stderr.String())
	}
	if _, err := os.Lstat(filepath.Join(dir, "lefthook.yml")); !os.IsNotExist(err) {
		t.Errorf("skeleton lefthook.yml not deleted: %v", err)
	}
}

// ---------------------------------------------------------------- .worktreeinclude

// TestWtincUserContentSurvivesBlockStripped: content OUTSIDE the marked
// block is never touched -- only the block itself is removed, and the file
// stays because it is not empty afterward.
func TestWtincUserContentSurvivesBlockStripped(t *testing.T) {
	dir, _ := initRepo(t)
	stubLefthook(t)
	singleGatePayload(t)
	mustInit(t)

	wtinc := filepath.Join(dir, ".worktreeinclude")
	before := readFileT(t, wtinc) // init's fresh block, nothing else
	writeFile(t, wtinc, "my-own-ignore/\n"+before)

	var stdout, stderr strings.Builder
	if code := RunRemove(nil, &stdout, &stderr); code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%q", code, stderr.String())
	}
	eq(t, "wtinc survives, block stripped", readFileT(t, wtinc), "my-own-ignore/\n")
}

// ---------------------------------------------------------------- ledger semantics

// TestDisabledPlacedRowStillDeleted: the ledger's "enabled" column is an
// off-switch for drift/guard machinery, not an uninstall flag -- remove
// deletes every row regardless (remove.sh:40-43's comment).
func TestDisabledPlacedRowStillDeleted(t *testing.T) {
	dir, repo := initRepo(t)
	stubLefthook(t)
	singleGatePayload(t)
	mustInit(t)

	ledger := filepath.Join(repo.OMK, "placed.tsv")
	before := readFileT(t, ledger)
	disabled := strings.TrimSuffix(before, "1\n") + "0\n"
	if disabled == before {
		t.Fatalf("fixture setup did not find the expected enabled column in %q", before)
	}
	writeFile(t, ledger, disabled)

	var stdout, stderr strings.Builder
	if code := RunRemove(nil, &stdout, &stderr); code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%q", code, stderr.String())
	}
	if _, err := os.Lstat(filepath.Join(dir, ".omakase", "gates", "example.sh")); !os.IsNotExist(err) {
		t.Errorf("disabled placed file was NOT deleted (enabled flag must not gate removal): %v", err)
	}
}

// ---------------------------------------------------------------- mode parity (review finding 1)
//
// bash's marked-block rewrites go through `awk ... > "$f.tmp" && mv "$f.tmp"
// "$f"` -- a NEW inode, mode `0666 &^ umask`, regardless of what mode $f had
// going in. Confirmed against a live shell run (umask 022):
//
//	$ printf orig > f; chmod 0640 f; stat -f '%Lp' f   # 640
//	$ awk '{print}' f > f.tmp && mv f.tmp f; stat -f '%Lp' f   # 644 = 0666 &^ 022
//
// Each test below seeds a PRE-EXISTING file at a mode that deliberately
// differs from `0666 &^ umask`, so a port that merely preserves the original
// mode (os.WriteFile over an existing path, which only applies its mode
// argument at creation) would leave the seeded mode in place instead of
// normalizing to bash's fresh-inode mode -- exactly the divergence rewriteFile
// (internal/overlay/overlay.go) fixes. Verified red (fails) against the
// pre-fix os.WriteFile call sites before this fix landed; see the task-5
// report addendum for the stash-based red/green run.

// TestHookStubModeMatchesBashFreshInode: a hook stub seeded at 0640 (not
// executable, and not 0666&^umask either) must end up at `0777 &^ umask`
// after remove -- the strip's fresh 0666&^umask base, then chmod +x's
// `0111&^umask` on top (remove.sh:24-37).
func TestHookStubModeMatchesBashFreshInode(t *testing.T) {
	_, repo := initRepo(t)
	stubLefthook(t)
	singleGatePayload(t)
	mustInit(t)

	hf := filepath.Join(repo.CommonDir, "hooks", "pre-commit")
	content := "#!/bin/sh\n" +
		"# >>> omakase-harness fail-closed >>>\n" +
		"marker body\n" +
		"# <<< omakase-harness fail-closed <<<\n" +
		"call_lefthook run \"pre-commit\" \"$@\"\n"
	writeFile(t, hf, content)
	if err := os.Chmod(hf, 0o640); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr strings.Builder
	if code := RunRemove(nil, &stdout, &stderr); code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%q", code, stderr.String())
	}

	info, err := os.Stat(hf)
	if err != nil {
		t.Fatal(err)
	}
	want := os.FileMode(0o777) &^ currentUmask()
	if info.Mode().Perm() != want {
		t.Errorf("hook stub mode after remove = %o, want %o (0777 &^ umask -- the original seeded 0640 must NOT survive)", info.Mode().Perm(), want)
	}
}

// TestExcludeStripModeMatchesBashFreshInode: the exclude file seeded at 0600
// (not 0666&^umask) must end up at exactly `0666 &^ umask` after the
// unconditional strip (remove.sh:87-90 has no chmod +x -- exclude is never
// executable).
func TestExcludeStripModeMatchesBashFreshInode(t *testing.T) {
	_, repo := initRepo(t)
	stubLefthook(t)
	singleGatePayload(t)
	mustInit(t)

	exclude := filepath.Join(repo.CommonDir, "info", "exclude")
	if err := os.Chmod(exclude, 0o600); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr strings.Builder
	if code := RunRemove(nil, &stdout, &stderr); code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%q", code, stderr.String())
	}

	info, err := os.Stat(exclude)
	if err != nil {
		t.Fatal(err)
	}
	want := os.FileMode(0o666) &^ currentUmask()
	if info.Mode().Perm() != want {
		t.Errorf("exclude mode after remove = %o, want %o (0666 &^ umask -- the original seeded 0600 must NOT survive)", info.Mode().Perm(), want)
	}
}

// TestWtincStripModeMatchesBashFreshInode: same fresh-inode reasoning for the
// .worktreeinclude strip (remove.sh:77-82), seeded at 0600 with content that
// survives the strip (so the file isn't deleted for being empty).
func TestWtincStripModeMatchesBashFreshInode(t *testing.T) {
	dir, repo := initRepo(t)
	stubLefthook(t)
	singleGatePayload(t)
	mustInit(t)

	wtinc := filepath.Join(dir, ".worktreeinclude")
	before := readFileT(t, wtinc)
	writeFile(t, wtinc, "my-own-ignore/\n"+before)
	if err := os.Chmod(wtinc, 0o600); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr strings.Builder
	if code := RunRemove(nil, &stdout, &stderr); code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%q", code, stderr.String())
	}

	info, err := os.Stat(wtinc)
	if err != nil {
		t.Fatal(err)
	}
	want := os.FileMode(0o666) &^ currentUmask()
	if info.Mode().Perm() != want {
		t.Errorf("wtinc mode after remove = %o, want %o (0666 &^ umask -- the original seeded 0600 must NOT survive)", info.Mode().Perm(), want)
	}
	eq(t, repo.Root, readFileT(t, wtinc), "my-own-ignore/\n") // sanity: content still correct alongside the mode check
}

// ---------------------------------------------------------------- removeF error propagation (review finding 2)
//
// remove.sh:74 (`grep -q "EXAMPLE USAGE" ... && rm -f "$ROOT/lefthook.yml"`)
// and remove.sh:81 (`[ -s "$WTINC" ] || rm -f "$WTINC"`) both run under
// `set -e`: an `rm -f` failure there aborts the script with a nonzero exit.
// The Go port previously discarded removeF's error at both call sites --
// these tests force a removal failure (a non-empty, non-writable PARENT
// directory so the unlink itself fails, not just a missing file, which
// removeF already treats as success) and assert RunRemove now propagates it
// as exit 1 instead of silently continuing to exit 0.

// TestSkeletonLefthookYmlRemovalFailurePropagates: making the repo root
// read-only means `rm -f lefthook.yml` cannot unlink the entry (removing a
// file requires write permission on its PARENT directory, not the file
// itself) -- RunRemove must report that failure as exit 1, matching set -e.
//
// Uses an EMPTY payload (nothing placed) rather than singleGatePayload
// deliberately: a nested placed path (e.g. .omakase/gates/example.sh) would
// make the ledger-driven deletion loop, which runs BEFORE this step, prune
// ROOT/.omakase itself once its contents are gone -- rmdir-ing a direct
// child of ROOT needs write permission on ROOT, so THAT step would fail
// first instead (an error path DeletePlaced already propagated correctly
// before this fix), masking the site actually under test here. An empty
// payload keeps the ledger-driven loop a true no-op (zero rows), isolating
// the failure to the lefthook.yml removeF call.
func TestSkeletonLefthookYmlRemovalFailurePropagates(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root ignores directory write permission; this fixture needs a non-root unlink to fail")
	}
	dir, _ := initRepo(t)
	stubLefthook(t)
	t.Setenv("OMAKASE_PAYLOAD", t.TempDir()) // empty: nothing placed
	mustInit(t)

	writeFile(t, filepath.Join(dir, "lefthook.yml"), "# EXAMPLE USAGE:\n#\n#   see https://lefthook.dev\n")
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o755) }) // let t.TempDir() clean up afterward

	var stdout, stderr strings.Builder
	code := RunRemove(nil, &stdout, &stderr)
	if code != 1 {
		t.Errorf("exit = %d, want 1 (rm -f failure must abort, matching set -e); stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}
