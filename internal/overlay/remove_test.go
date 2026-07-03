package overlay

import (
	"io"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Yuncun/omakase-harness/internal/state"
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
// ledger, no exclude block, no $OMK -- a bare RunRemove must report the
// "nothing installed" line on stderr and exit 0 without touching anything.
func TestNeverInstalledNoSentinelIsANoOp(t *testing.T) {
	initRepo(t)
	stubLefthook(t)

	var stdout, stderr strings.Builder
	code := RunRemove(nil, &stdout, &stderr)
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

// ---------------------------------------------------------------- remove <source> (Task 4, Phase 3.5)
//
// These pin the `remove <source>` surface: unlayer ONE harness from a 2-stack
// (top or bottom), the one-source==total-teardown equivalence, the unknown-arg
// and pre-layer refusals, the GC5 summary/narration bytes, and the GC7 twin-diff
// invariants (a removed-then state must equal a fresh single-source install).
// There is no v1 oracle for any of this — it is a NEW v2 surface — so the
// summary line's exact bytes are pinned here per the controller's GC5 spec and
// the per-file narration per this port's own house style (init's +/^/- bullets).
//
// Shared source helpers (srcTestEnv, useBasePayloadDir, newHarnessSource,
// commitAll, newSourceRepo, sha256hex, gitStdout, snapshotTree) live in
// source_test.go / init_layers_test.go / init_test.go / layers_test.go.

// excludeBlockOf returns the omakase marked block (markers inclusive) from an
// exclude file, so a twin comparison is of the frozen block bytes, not the whole
// file (which carries the caller's own pre-seeded ignores).
func excludeBlockOf(t *testing.T, path string) string {
	t.Helper()
	const begin = "# >>> omakase-harness >>>"
	const end = "# <<< omakase-harness <<<"
	content := readFileT(t, path)
	bi := strings.Index(content, begin)
	ei := strings.Index(content, end)
	if bi < 0 || ei < 0 {
		t.Fatalf("no omakase block in %s:\n%s", path, content)
	}
	return content[bi : ei+len(end)]
}

// omkTreeForTwin snapshots the FULL $OMK tree (every regular file's bytes and
// every symlink's target, via snapshotTree) for the twin comparison, with
// exactly two justified normalizations:
//
//   - sources.tsv epoch column (field 5) -> "EPOCH": a fresh init records a new
//     wall-clock epoch while remove preserves the survivor's — the one
//     controller-sanctioned normalization. Every OTHER column of every row is
//     compared exactly.
//   - clobbered/ entries are EXCLUDED: $OMK/clobbered/ is the backup area for
//     user data (a locally-edited file overwritten by a restore is preserved
//     there — the Fix-3 mandate), so it CANNOT be twin-equal by construction:
//     the fresh twin never had a pre-existing file to back up, and deleting the
//     backups to equalize the trees would destroy the only copy of a user's
//     edit. Same documented exemption personal_test.go's assertTwinsEqual
//     carried ("clobbered/ is intentionally not compared across twins").
//
// NO other normalization: any other $OMK byte difference between the twins is
// a defect the comparison must catch ($OMK/source was exactly such a defect).
func omkTreeForTwin(t *testing.T, omk string) map[string]string {
	t.Helper()
	tree := snapshotTree(t, omk)
	for k := range tree {
		if strings.HasPrefix(k, "file:clobbered/") || strings.HasPrefix(k, "symlink:clobbered/") {
			delete(tree, k)
		}
	}
	if s, ok := tree["file:sources.tsv"]; ok {
		var out []string
		for _, line := range strings.Split(strings.TrimRight(s, "\n"), "\n") {
			f := strings.Split(line, "\t")
			if len(f) == 5 {
				f[4] = "EPOCH"
			}
			out = append(out, strings.Join(f, "\t"))
		}
		tree["file:sources.tsv"] = strings.Join(out, "\n") + "\n"
	}
	return tree
}

// assertRemoveTwinEqual pins the GC7 twin-diff invariant: an unlayered repo and a
// fresh single-source install agree BYTE-FOR-BYTE on the FULL $OMK tree (see
// omkTreeForTwin for the two justified normalizations) and the exclude block,
// and both working trees are git-clean. placed.tsv is also asserted directly
// first, only for a readable failure message — the full-tree compare covers it.
func assertRemoveTwinEqual(t *testing.T, a, b *state.Repo, dirA, dirB string) {
	t.Helper()
	eq(t, "placed.tsv (unlayered vs fresh)",
		readFileT(t, filepath.Join(a.OMK, "placed.tsv")),
		readFileT(t, filepath.Join(b.OMK, "placed.tsv")))
	eq(t, "exclude block (unlayered vs fresh)",
		excludeBlockOf(t, filepath.Join(a.CommonDir, "info", "exclude")),
		excludeBlockOf(t, filepath.Join(b.CommonDir, "info", "exclude")))
	ta, tb := omkTreeForTwin(t, a.OMK), omkTreeForTwin(t, b.OMK)
	if !maps.Equal(ta, tb) {
		for k, va := range ta {
			vb, ok := tb[k]
			if !ok {
				t.Errorf("$OMK entry %q: unlayered has it, fresh does not (%q)", k, va)
			} else if va != vb {
				t.Errorf("$OMK entry %q differs:\n unlayered=%q\n fresh=%q", k, va, vb)
			}
		}
		for k, vb := range tb {
			if _, ok := ta[k]; !ok {
				t.Errorf("$OMK entry %q: fresh has it, unlayered does not (%q)", k, vb)
			}
		}
	}
	if out := gitStdout(dirA, "status", "--porcelain"); out != "" {
		t.Errorf("unlayered repo status not clean: %q", out)
	}
	if out := gitStdout(dirB, "status", "--porcelain"); out != "" {
		t.Errorf("fresh repo status not clean: %q", out)
	}
}

// ---------------------------------------------------------------- dispatch guards

// TestRemoveUnknownSource: `remove <bogus>` in a 2-stack refuses with the GC5
// no-match line naming the installed harnesses, exit 1, mutating nothing.
func TestRemoveUnknownSource(t *testing.T) {
	dir, repo := initRepo(t)
	srcTestEnv(t)
	stubLefthook(t)
	useBasePayloadDir(t)

	srcA := newHarnessSource(t, "a", map[string]string{".omakase/gates/a.sh": "a\n"})
	srcB := newHarnessSource(t, "b", map[string]string{".omakase/gates/b.sh": "b\n"})
	if code := RunInit([]string{"--source", srcA}, io.Discard, io.Discard); code != 0 {
		t.Fatal("init A failed")
	}
	if code := RunInit([]string{"--source", srcB}, io.Discard, io.Discard); code != 0 {
		t.Fatal("init B failed")
	}
	before := snapshotTree(t, repo.OMK)

	var stdout, stderr strings.Builder
	code := RunRemove([]string{"nope/nope"}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("exit = %d, want 1; stderr=%q", code, stderr.String())
	}
	eq(t, "stdout", stdout.String(), "")
	eq(t, "stderr", stderr.String(),
		"omakase: no harness 'nope/nope' installed here (installed: "+srcA+", "+srcB+")\n")
	if after := snapshotTree(t, repo.OMK); !maps.Equal(before, after) {
		t.Errorf("unknown-source refusal mutated $OMK")
	}
	if out := gitStdout(dir, "status", "--porcelain"); out != "" {
		t.Errorf("unknown-source refusal dirtied the tree: %q", out)
	}
}

// TestRemoveArgPreLayerRefusal: `remove <arg>` against a repo that predates
// layered state ($OMK with placed.tsv but no layers/) refuses with the frozen
// RequireLayers message, exit 1.
func TestRemoveArgPreLayerRefusal(t *testing.T) {
	_, repo := initRepo(t)
	stubLefthook(t)
	// Hand-build a pre-layers $OMK: a placed ledger but NO layers/ and NO sources.tsv.
	if err := os.MkdirAll(repo.OMK, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(repo.OMK, "placed.tsv"),
		".omakase/gates/example.sh\tgate\tpayload\t"+sha256hex([]byte(gateContent))+"\t1\n")

	var stdout, stderr strings.Builder
	code := RunRemove([]string{"you/harness"}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("exit = %d, want 1; stderr=%q", code, stderr.String())
	}
	eq(t, "stdout", stdout.String(), "")
	eq(t, "stderr", stderr.String(),
		"omakase: this repo predates layered state — run omakase init once first\n")
}

// TestRemoveUsageArm: an unparseable remove invocation (two positionals, an
// unknown flag) prints the usage to stderr and exits 2; -h prints it to stdout,
// exit 0.
func TestRemoveUsageArm(t *testing.T) {
	initRepo(t)
	stubLefthook(t)

	for _, argv := range [][]string{{"a/b", "c/d"}, {"--bogus"}} {
		var stdout, stderr strings.Builder
		if code := RunRemove(argv, &stdout, &stderr); code != 2 {
			t.Errorf("argv %v: exit = %d, want 2", argv, code)
		}
		eq(t, "stdout", stdout.String(), "")
		eq(t, "stderr", stderr.String(), removeUsageText)
	}
	var so, se strings.Builder
	if code := RunRemove([]string{"-h"}, &so, &se); code != 0 {
		t.Errorf("-h exit = %d, want 0", code)
	}
	eq(t, "help stdout", so.String(), removeUsageText)
	eq(t, "help stderr", se.String(), "")
}

// ---------------------------------------------------------------- one source == total teardown

// TestRemoveOneSourceIsTotalTeardown: with exactly ONE source installed, `remove
// <that source>` is byte-identical to a bare `remove` — full teardown, the bare
// removed line, and $OMK gone (the decided edge case; no third state).
func TestRemoveOneSourceIsTotalTeardown(t *testing.T) {
	dir, repo := initRepo(t)
	srcTestEnv(t)
	stubLefthook(t)
	useBasePayloadDir(t)

	src := newHarnessSource(t, "solo", map[string]string{".omakase/gates/g.sh": "g\n"})
	if code := RunInit([]string{"--source", src}, io.Discard, io.Discard); code != 0 {
		t.Fatal("init failed")
	}

	var stdout, stderr strings.Builder
	if code := RunRemove([]string{src}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%q", code, stderr.String())
	}
	eq(t, "stdout is the bare-remove line", stdout.String(), removedLine)
	eq(t, "stderr", stderr.String(), "")
	if _, err := os.Lstat(repo.OMK); !os.IsNotExist(err) {
		t.Errorf("$OMK still exists after single-source teardown: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(dir, ".omakase")); !os.IsNotExist(err) {
		t.Errorf(".omakase not pruned away: %v", err)
	}
}

// ---------------------------------------------------------------- top-layer removal (GC5 + restore/delete)

// TestRemoveTopLayerSummaryAndMatrix: A+B stack, `remove B`. A B-won-over-A path
// is restored to A's copy; a sole-B path is deleted; an A-only path is untouched.
// Pins the exact GC5 summary line + per-file narration bytes.
func TestRemoveTopLayerSummaryAndMatrix(t *testing.T) {
	dir, repo := initRepo(t)
	srcTestEnv(t)
	stubLefthook(t)
	useBasePayloadDir(t) // empty base

	srcA := newHarnessSource(t, "a", map[string]string{
		".omakase/gates/shared.sh": "A\n",
		".omakase/gates/aonly.sh":  "A only\n",
	})
	srcB := newHarnessSource(t, "b", map[string]string{
		".omakase/gates/shared.sh": "B\n",
		".omakase/gates/bonly.sh":  "B only\n",
	})
	if code := RunInit([]string{"--source", srcA}, io.Discard, io.Discard); code != 0 {
		t.Fatal("init A failed")
	}
	if code := RunInit([]string{"--source", srcB}, io.Discard, io.Discard); code != 0 {
		t.Fatal("init B failed")
	}
	eq(t, "B won shared.sh", readFileT(t, filepath.Join(dir, ".omakase", "gates", "shared.sh")), "B\n")

	var stdout, stderr strings.Builder
	if code := RunRemove([]string{srcB}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%q", code, stderr.String())
	}
	eq(t, "stdout", stdout.String(),
		"omakase: removed "+srcB+" — 1 file(s) deleted, 1 restored from "+srcA+"\n"+
			"  - deleted: .omakase/gates/bonly.sh\n"+
			"  ^ restored: .omakase/gates/shared.sh\n")
	eq(t, "stderr", stderr.String(), "")

	eq(t, "shared.sh restored to A", readFileT(t, filepath.Join(dir, ".omakase", "gates", "shared.sh")), "A\n")
	eq(t, "aonly.sh untouched", readFileT(t, filepath.Join(dir, ".omakase", "gates", "aonly.sh")), "A only\n")
	if _, err := os.Lstat(filepath.Join(dir, ".omakase", "gates", "bonly.sh")); !os.IsNotExist(err) {
		t.Errorf("sole-B bonly.sh not deleted: %v", err)
	}
	rows := state.ReadSources(filepath.Join(repo.OMK, "sources.tsv"))
	if len(rows) != 1 || rows[0].Layer != "1" || rows[0].Source != srcA {
		t.Errorf("sources.tsv = %+v, want one {1 %s} row", rows, srcA)
	}
	if _, err := os.Stat(filepath.Join(repo.OMK, "layers", "2")); !os.IsNotExist(err) {
		t.Error("layers/2 not removed after top-layer removal")
	}
	for _, r := range state.ReadPlaced(filepath.Join(repo.OMK, "placed.tsv")) {
		if r.Rel == ".omakase/gates/shared.sh" && (r.Src != srcA || r.Hash != sha256hex([]byte("A\n"))) {
			t.Errorf("shared.sh row = %+v, want {src=%s hash=sha256(A)}", r, srcA)
		}
		if r.Rel == ".omakase/gates/bonly.sh" {
			t.Error("placed.tsv still lists the deleted sole-B path")
		}
	}
	if out := gitStdout(dir, "status", "--porcelain"); out != "" {
		t.Errorf("git status not clean: %q", out)
	}
}

// TestRemoveTopLayerEditedSolePathKept: a sole-B path the user EDITED is warned
// and KEPT (never silently deleted), matching init's orphan-sweep discipline.
func TestRemoveTopLayerEditedSolePathKept(t *testing.T) {
	dir, _ := initRepo(t)
	srcTestEnv(t)
	stubLefthook(t)
	useBasePayloadDir(t)

	srcA := newHarnessSource(t, "a", map[string]string{".omakase/gates/a.sh": "a\n"})
	srcB := newHarnessSource(t, "b", map[string]string{".omakase/gates/bedit.sh": "B ORIG\n"})
	if code := RunInit([]string{"--source", srcA}, io.Discard, io.Discard); code != 0 {
		t.Fatal("init A failed")
	}
	if code := RunInit([]string{"--source", srcB}, io.Discard, io.Discard); code != 0 {
		t.Fatal("init B failed")
	}
	writeFile(t, filepath.Join(dir, ".omakase", "gates", "bedit.sh"), "MY EDIT\n")

	var stdout, stderr strings.Builder
	if code := RunRemove([]string{srcB}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit = %d; stderr=%q", code, stderr.String())
	}
	eq(t, "edited sole path kept", readFileT(t, filepath.Join(dir, ".omakase", "gates", "bedit.sh")), "MY EDIT\n")
	if !strings.Contains(stderr.String(), ".omakase/gates/bedit.sh") || !strings.Contains(stderr.String(), "Leaving it") {
		t.Errorf("expected a warn-keep line for the edited sole path:\n%s", stderr.String())
	}
	eq(t, "summary (0 deleted, 0 restored)", stdout.String(),
		"omakase: removed "+srcB+" — 0 file(s) deleted, 0 restored from "+srcA+"\n")
}

// ---------------------------------------------------------------- GC7(a): top removal ≡ fresh init A

// TestRemoveTopLayerTwinDiff: init A; init B; remove B  ≡  fresh init A only.
// A ships a root AGENTS.md (owns the slot + a bridge); B overrides a shared gate,
// ships a sole gate, and its own AGENTS.md reroutes to CLAUDE.local.md. After
// removing B every trace of B is gone and the repo is byte-identical to a repo
// that only ever installed A.
func TestRemoveTopLayerTwinDiff(t *testing.T) {
	srcTestEnv(t)
	stubLefthook(t)
	base := useBasePayloadDir(t)
	writeFile(t, filepath.Join(base, ".omakase", "bin", "base.sh"), "base\n")

	srcA := newHarnessSource(t, "a", map[string]string{
		"AGENTS.md":                "A doctrine\n",
		".claude/rules/a.md":       "A rule\n",
		".omakase/gates/shared.sh": "A\n",
	})
	srcB := newHarnessSource(t, "b", map[string]string{
		"AGENTS.md":                "B doctrine\n", // -> CLAUDE.local.md, sole-B, deleted on remove
		".omakase/gates/shared.sh": "B\n",          // overrides A, restored on remove
		".omakase/gates/bonly.sh":  "B only\n",     // sole-B, deleted on remove
	})

	// twin A: install both, remove the top.
	dirA, repoA := initRepo(t)
	if code := RunInit([]string{"--source", srcA}, io.Discard, io.Discard); code != 0 {
		t.Fatal("A: init A failed")
	}
	if code := RunInit([]string{"--source", srcB}, io.Discard, io.Discard); code != 0 {
		t.Fatal("A: init B failed")
	}
	var ro, re strings.Builder
	if code := RunRemove([]string{srcB}, &ro, &re); code != 0 {
		t.Fatalf("A: remove B exit = %d; stderr=%q", code, re.String())
	}
	// B's rerouted CLAUDE.local.md must be gone; A's root AGENTS.md + bridge stay.
	if _, err := os.Lstat(filepath.Join(dirA, "CLAUDE.local.md")); !os.IsNotExist(err) {
		t.Error("A: CLAUDE.local.md (B's reroute) survived remove B")
	}

	// twin B: only ever install A.
	dirB, repoB := initRepo(t)
	if code := RunInit([]string{"--source", srcA}, io.Discard, io.Discard); code != 0 {
		t.Fatal("B: fresh init A failed")
	}

	assertRemoveTwinEqual(t, repoA, repoB, dirA, dirB)
}

// ---------------------------------------------------------------- GC7(b): bottom removal ≡ fresh init B

// TestRemoveBottomLayerTwinDiff: init A; init B; remove A  ≡  fresh init B only.
// A (bottom, base-folded) owns the root AGENTS.md + bridge; B ships only gates.
// Removing A re-folds the embedded base into B's surviving layer (new layers/1),
// so the repo becomes byte-identical to one that only ever installed B — the
// base-folded gates, no root AGENTS.md, no bridge.
func TestRemoveBottomLayerTwinDiff(t *testing.T) {
	srcTestEnv(t)
	stubLefthook(t)
	base := useBasePayloadDir(t)
	writeFile(t, filepath.Join(base, ".omakase", "bin", "base.sh"), "base\n")

	srcA := newHarnessSource(t, "a", map[string]string{
		"AGENTS.md":                "A doctrine\n", // root + bridge, sole-A, deleted on remove
		".claude/rules/a.md":       "A rule\n",     // sole-A, deleted on remove
		".omakase/gates/shared.sh": "A\n",          // overridden by B (B wins), untouched on remove
	})
	srcB := newHarnessSource(t, "b", map[string]string{
		".omakase/gates/shared.sh": "B\n",      // B wins in the stack
		".omakase/gates/bonly.sh":  "B only\n", // sole-B
	})

	// twin A: install both, remove the bottom.
	dirA, repoA := initRepo(t)
	if code := RunInit([]string{"--source", srcA}, io.Discard, io.Discard); code != 0 {
		t.Fatal("A: init A failed")
	}
	if code := RunInit([]string{"--source", srcB}, io.Discard, io.Discard); code != 0 {
		t.Fatal("A: init B failed")
	}
	var ro, re strings.Builder
	if code := RunRemove([]string{srcA}, &ro, &re); code != 0 {
		t.Fatalf("A: remove A exit = %d; stderr=%q", code, re.String())
	}
	// A's root AGENTS.md, bridge, and A-only rule are gone; B's gates remain.
	for _, gone := range []string{"AGENTS.md", "CLAUDE.md", ".claude/rules/a.md"} {
		if _, err := os.Lstat(filepath.Join(dirA, gone)); !os.IsNotExist(err) {
			t.Errorf("A: %q survived remove A (bottom)", gone)
		}
	}
	eq(t, "A: base folded under B", readFileT(t, filepath.Join(dirA, ".omakase", "bin", "base.sh")), "base\n")
	eq(t, "A: B's gate remains", readFileT(t, filepath.Join(dirA, ".omakase", "gates", "shared.sh")), "B\n")

	rows := state.ReadSources(filepath.Join(repoA.OMK, "sources.tsv"))
	if len(rows) != 1 || rows[0].Layer != "1" || rows[0].Source != srcB {
		t.Errorf("A: sources.tsv = %+v, want one {1 %s} row (B renumbered to the bottom)", rows, srcB)
	}

	// twin B: only ever install B.
	dirB, repoB := initRepo(t)
	if code := RunInit([]string{"--source", srcB}, io.Discard, io.Discard); code != 0 {
		t.Fatal("B: fresh init B failed")
	}

	assertRemoveTwinEqual(t, repoA, repoB, dirA, dirB)
}

// ---------------------------------------------------------------- GC7(b): the un-reroute shape (fix wave 1)

// TestRemoveBottomUnreroutesSurvivorInstructions: A owns the root AGENTS.md (+
// bridge); B's canonical AGENTS.md was rerouted to CLAUDE.local.md at stack time
// (root slot taken). remove A must UN-reroute the survivor's instructions: the
// working tree ends with B's AGENTS.md at the ROOT (+ the §7 bridge), the old
// CLAUDE.local.md is swept, and the repo is byte-equal to a fresh `init B`
// (which places B's AGENTS.md at the then-free root). Driven by the reroute
// sidecar marker B's store recorded at build time.
func TestRemoveBottomUnreroutesSurvivorInstructions(t *testing.T) {
	srcTestEnv(t)
	stubLefthook(t)
	base := useBasePayloadDir(t)
	writeFile(t, filepath.Join(base, ".omakase", "bin", "base.sh"), "base\n")

	srcA := newHarnessSource(t, "a", map[string]string{
		"AGENTS.md":           "A doctrine\n",
		".omakase/gates/a.sh": "a gate\n",
	})
	srcB := newHarnessSource(t, "b", map[string]string{
		"AGENTS.md":           "B doctrine\n", // rerouted to CLAUDE.local.md in the stack
		".omakase/gates/b.sh": "b gate\n",
	})

	// twin A: stack, then remove the bottom.
	dirA, repoA := initRepo(t)
	if code := RunInit([]string{"--source", srcA}, io.Discard, io.Discard); code != 0 {
		t.Fatal("A: init A failed")
	}
	if code := RunInit([]string{"--source", srcB}, io.Discard, io.Discard); code != 0 {
		t.Fatal("A: init B failed")
	}
	eq(t, "A: B rerouted at stack time", readFileT(t, filepath.Join(dirA, "CLAUDE.local.md")), "B doctrine\n")

	var ro, re strings.Builder
	if code := RunRemove([]string{srcA}, &ro, &re); code != 0 {
		t.Fatalf("A: remove A exit = %d; stderr=%q", code, re.String())
	}
	eq(t, "A: summary", strings.SplitN(ro.String(), "\n", 2)[0]+"\n",
		"omakase: removed "+srcA+" — 2 file(s) deleted, 3 restored from "+srcB+"\n")

	// The survivor's instructions moved back to the root slot.
	eq(t, "A: B's AGENTS.md un-rerouted to the root", readFileT(t, filepath.Join(dirA, "AGENTS.md")), "B doctrine\n")
	if tgt, err := os.Readlink(filepath.Join(dirA, "CLAUDE.md")); err != nil || tgt != "AGENTS.md" {
		t.Fatalf("A: bridge missing after un-reroute: tgt=%q err=%v", tgt, err)
	}
	if _, err := os.Lstat(filepath.Join(dirA, "CLAUDE.local.md")); !os.IsNotExist(err) {
		t.Error("A: stale CLAUDE.local.md survived the un-reroute")
	}
	// The rebuilt survivor store carries NO reroute marker (nothing fell back).
	if _, err := os.Lstat(filepath.Join(repoA.OMK, "layers", "1", "rerouted")); !os.IsNotExist(err) {
		t.Error("A: rebuilt layers/1 carries a stale reroute marker")
	}

	// twin B: only ever install B (root slot free -> AGENTS.md at the root).
	dirB, repoB := initRepo(t)
	if code := RunInit([]string{"--source", srcB}, io.Discard, io.Discard); code != 0 {
		t.Fatal("B: fresh init B failed")
	}
	if got := readFileT(t, filepath.Join(dirB, "AGENTS.md")); got != "B doctrine\n" {
		t.Fatalf("B: fresh init did not place root AGENTS.md: %q", got)
	}

	assertRemoveTwinEqual(t, repoA, repoB, dirA, dirB)
}

// TestRemoveBottomUnrerouteSuppressedByCommittedCLAUDEmd: same shape but the
// repo COMMITS a root CLAUDE.md, so the root slot is taken by the repo itself —
// the un-reroute must be SUPPRESSED (MapInstruction re-falls-back with the
// recomputed rootSlotFree=false): CLAUDE.local.md survives carrying B's
// doctrine, no root AGENTS.md appears, no bridge, the rebuilt store re-records
// the reroute marker, and the twin (fresh `init B` under the same committed
// CLAUDE.md, which also falls back) is byte-equal.
func TestRemoveBottomUnrerouteSuppressedByCommittedCLAUDEmd(t *testing.T) {
	srcTestEnv(t)
	stubLefthook(t)
	base := useBasePayloadDir(t)
	writeFile(t, filepath.Join(base, ".omakase", "bin", "base.sh"), "base\n")

	srcA := newHarnessSource(t, "a", map[string]string{
		"AGENTS.md":           "A doctrine\n",
		".omakase/gates/a.sh": "a gate\n",
	})
	srcB := newHarnessSource(t, "b", map[string]string{
		"AGENTS.md":           "B doctrine\n",
		".omakase/gates/b.sh": "b gate\n",
	})

	// twin A: committed CLAUDE.md, stack, remove the bottom.
	dirA, repoA := initRepo(t)
	writeFile(t, filepath.Join(dirA, "CLAUDE.md"), "TEAM CLAUDE\n")
	runGitT(t, dirA, "add", "CLAUDE.md")
	runGitT(t, dirA, "commit", "-q", "-m", "team")
	if code := RunInit([]string{"--source", srcA}, io.Discard, io.Discard); code != 0 {
		t.Fatal("A: init A failed")
	}
	if code := RunInit([]string{"--source", srcB}, io.Discard, io.Discard); code != 0 {
		t.Fatal("A: init B failed")
	}
	eq(t, "A: B won the fallback slot at stack time", readFileT(t, filepath.Join(dirA, "CLAUDE.local.md")), "B doctrine\n")

	var ro, re strings.Builder
	if code := RunRemove([]string{srcA}, &ro, &re); code != 0 {
		t.Fatalf("A: remove A exit = %d; stderr=%q", code, re.String())
	}

	// Suppressed: instructions STAY at CLAUDE.local.md; no root AGENTS.md; the
	// committed CLAUDE.md untouched; the rebuilt store re-records the marker.
	eq(t, "A: CLAUDE.local.md survives (suppressed un-reroute)", readFileT(t, filepath.Join(dirA, "CLAUDE.local.md")), "B doctrine\n")
	if _, err := os.Lstat(filepath.Join(dirA, "AGENTS.md")); !os.IsNotExist(err) {
		t.Error("A: root AGENTS.md appeared despite a committed root CLAUDE.md")
	}
	eq(t, "A: committed CLAUDE.md untouched", readFileT(t, filepath.Join(dirA, "CLAUDE.md")), "TEAM CLAUDE\n")
	eq(t, "A: rebuilt layers/1 re-records the reroute marker",
		readFileT(t, filepath.Join(repoA.OMK, "layers", "1", "rerouted")), "CLAUDE.local.md\tAGENTS.md\n")

	// twin B: committed CLAUDE.md, fresh init B (also falls back).
	dirB, repoB := initRepo(t)
	writeFile(t, filepath.Join(dirB, "CLAUDE.md"), "TEAM CLAUDE\n")
	runGitT(t, dirB, "add", "CLAUDE.md")
	runGitT(t, dirB, "commit", "-q", "-m", "team")
	if code := RunInit([]string{"--source", srcB}, io.Discard, io.Discard); code != 0 {
		t.Fatal("B: fresh init B failed")
	}
	eq(t, "B: fresh init also falls back", readFileT(t, filepath.Join(dirB, "CLAUDE.local.md")), "B doctrine\n")

	assertRemoveTwinEqual(t, repoA, repoB, dirA, dirB)
}

// ---------------------------------------------------------------- restore arm: tryClobberBackup discipline (fix wave 1)

// TestRemoveTopBacksUpEditedRestoreTarget: B ships an explicit CLAUDE.md that
// suppressed A's §7 bridge; the user EDITS the working-tree CLAUDE.md; then
// `remove B`. CLAUDE.md is a restore target (A's store carries the bridge), so
// the edit must NOT be silently clobbered: it is backed up to
// $OMK/clobbered/CLAUDE.md byte-exact under init's overwrite discipline, the
// overwrote-warning line fires (with the preserved-at path), the bridge symlink
// is placed, and the warn-keep line never fires for CLAUDE.md. The GC7(a) twin
// still holds (clobbered/ is the documented exemption — the fresh twin has no
// pre-existing edit to back up).
func TestRemoveTopBacksUpEditedRestoreTarget(t *testing.T) {
	srcTestEnv(t)
	stubLefthook(t)
	useBasePayloadDir(t)

	srcA := newHarnessSource(t, "a", map[string]string{
		"AGENTS.md":          "project agents\n",
		".claude/rules/r.md": "a rule\n",
	})
	srcB := newHarnessSource(t, "b", map[string]string{
		"CLAUDE.md":               "B claude\n", // suppresses A's bridge in the stack
		".omakase/gates/bonly.sh": "B only\n",
	})

	// twin A: stack (B's CLAUDE.md wins over the bridge), EDIT it, remove B.
	dirA, repoA := initRepo(t)
	if code := RunInit([]string{"--source", srcA}, io.Discard, io.Discard); code != 0 {
		t.Fatal("A: init A failed")
	}
	if code := RunInit([]string{"--source", srcB}, io.Discard, io.Discard); code != 0 {
		t.Fatal("A: init B failed")
	}
	eq(t, "A: B's CLAUDE.md won at stack time", readFileT(t, filepath.Join(dirA, "CLAUDE.md")), "B claude\n")

	const editedBody = "MY EDITED CLAUDE\n"
	writeFile(t, filepath.Join(dirA, "CLAUDE.md"), editedBody)

	var ro, re strings.Builder
	if code := RunRemove([]string{srcB}, &ro, &re); code != 0 {
		t.Fatalf("A: remove B exit = %d; stderr=%q", code, re.String())
	}

	// The edit is preserved byte-exact under $OMK/clobbered/, never destroyed.
	clob := filepath.Join(repoA.OMK, "clobbered", "CLAUDE.md")
	eq(t, "edited CLAUDE.md preserved at clobbered/", readFileT(t, clob), editedBody)

	// CLAUDE.md is now the restored bridge symlink.
	if tgt, err := os.Readlink(filepath.Join(dirA, "CLAUDE.md")); err != nil || tgt != "AGENTS.md" {
		t.Fatalf("A: bridge not restored over the edit: tgt=%q err=%v", tgt, err)
	}

	// stderr: the overwrote message fired with the preserved-at path; the
	// warn-keep "Leaving it" line did NOT fire for CLAUDE.md.
	wantOverwrote := "omakase: overwrote CLAUDE.md to match payload (prior copy preserved at " + clob + ")\n"
	if !strings.Contains(re.String(), wantOverwrote) {
		t.Errorf("overwrote message missing/wrong:\n got=%q\n want substr=%q", re.String(), wantOverwrote)
	}
	if strings.Contains(re.String(), "'CLAUDE.md'") {
		t.Errorf("a warn line fired for the restore-target CLAUDE.md:\n%s", re.String())
	}
	eq(t, "summary", strings.SplitN(ro.String(), "\n", 2)[0]+"\n",
		"omakase: removed "+srcB+" — 1 file(s) deleted, 1 restored from "+srcA+"\n")

	// twin B: only ever install A — the bridge is placed fresh.
	dirB, repoB := initRepo(t)
	if code := RunInit([]string{"--source", srcA}, io.Discard, io.Discard); code != 0 {
		t.Fatal("B: fresh init A failed")
	}
	if tgt, err := os.Readlink(filepath.Join(dirB, "CLAUDE.md")); err != nil || tgt != "AGENTS.md" {
		t.Fatalf("B: fresh init did not place the bridge: tgt=%q err=%v", tgt, err)
	}

	assertRemoveTwinEqual(t, repoA, repoB, dirA, dirB)
}

// ---------------------------------------------------------------- final-review fix sweep (Fix B)

// TestRemoveArgSoleSourceMixedEraWarnsOnce: `remove <source>` naming the ONE
// recorded source in a mixed-era repo (design §9: a v1 tool rewrote $OMK/source
// out from under sources.tsv) falls through the <source> dispatch to the
// total-teardown path (remove.go:110-112), which used to call EnsureSources a
// SECOND time (remove.go's bare-path migration call) — printing the mixed-era
// WARNING line twice for the exact same on-disk state. It must print exactly
// once.
func TestRemoveArgSoleSourceMixedEraWarnsOnce(t *testing.T) {
	_, repo := initRepo(t)
	srcTestEnv(t)
	stubLefthook(t)
	useBasePayloadDir(t)

	src := newHarnessSource(t, "solo", map[string]string{".omakase/gates/g.sh": "g\n"})
	if code := RunInit([]string{"--source", src}, io.Discard, io.Discard); code != 0 {
		t.Fatal("init failed")
	}

	// The "v1 tool": repoint $OMK/source at a different string, leaving
	// sources.tsv still naming the original source — mixed-era axis 1.
	writeFile(t, filepath.Join(repo.OMK, "source"), "other/harness\n")

	var stdout, stderr strings.Builder
	if code := RunRemove([]string{src}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%q", code, stderr.String())
	}
	if n := strings.Count(stderr.String(), "a pre-layers omakase run changed this repo's source"); n != 1 {
		t.Errorf("mixed-era warning count = %d, want exactly 1; stderr=%q", n, stderr.String())
	}
	eq(t, "stdout is the bare-remove line", stdout.String(), removedLine)
}

// TestRemoveArgThreeRowSourcesRefusesRatherThanPanic: RemoveLayer's survivor
// math (layers.go: survivorIdx := 1 - removeIdx) only holds for exactly TWO
// recorded rows. A hand-edited sources.tsv carrying a third row would let a
// matched `remove <source>` reach RemoveLayer with removeIdx == 2, indexing
// recorded[-1] and panicking. `remove` must instead refuse before any
// mutation: exit 1, a named refusal, zero $OMK change, no panic.
func TestRemoveArgThreeRowSourcesRefusesRatherThanPanic(t *testing.T) {
	dir, repo := initRepo(t)
	stubLefthook(t)

	if err := os.MkdirAll(filepath.Join(repo.OMK, "layers"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(repo.OMK, "sources.tsv"),
		"1\ta/one\t-\tdeadbeef\t1700000000\n"+
			"2\ta/two\t-\tcafef00d\t1700000000\n"+
			"3\ta/three\t-\tfeedface\t1700000000\n")
	writeFile(t, filepath.Join(repo.OMK, "placed.tsv"),
		".omakase/gates/example.sh\tgate\ta/one\t"+sha256hex([]byte(gateContent))+"\t1\n")

	before := snapshotTree(t, repo.OMK)

	var stdout, stderr strings.Builder
	code := RunRemove([]string{"a/three"}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("exit = %d, want 1; stderr=%q", code, stderr.String())
	}
	eq(t, "stdout", stdout.String(), "")
	eq(t, "stderr", stderr.String(),
		"omakase: sources.tsv records 3 harnesses — expected at most 2; repair it or run omakase init\n")

	if after := snapshotTree(t, repo.OMK); !maps.Equal(before, after) {
		t.Errorf("3-row refusal mutated $OMK")
	}
	if out := gitStdout(dir, "status", "--porcelain"); out != "" {
		t.Errorf("3-row refusal dirtied the tree: %q", out)
	}
}

// ---------------------------------------------------------------- adversarial-review fix wave (FT2)

// TestRemoveEmptyStringArgIsNotInstalledNotTeardown pins Fix #2: an
// explicitly-passed empty-string positional (remove invoked with a single
// empty-string argument) is a source arg like any other and must land on the
// GC5 not-installed refusal -- never the bare total-teardown path. Before the
// fix, an empty string collapsed to the zero-args case (both compared as
// source == ""), silently wiping BOTH stacked harnesses and $OMK/clobbered
// instead of refusing.
func TestRemoveEmptyStringArgIsNotInstalledNotTeardown(t *testing.T) {
	dir, repo := initRepo(t)
	srcTestEnv(t)
	stubLefthook(t)
	useBasePayloadDir(t)

	srcA := newHarnessSource(t, "a", map[string]string{".omakase/gates/a.sh": "a\n"})
	srcB := newHarnessSource(t, "b", map[string]string{".omakase/gates/b.sh": "b\n"})
	if code := RunInit([]string{"--source", srcA}, io.Discard, io.Discard); code != 0 {
		t.Fatal("init A failed")
	}
	if code := RunInit([]string{"--source", srcB}, io.Discard, io.Discard); code != 0 {
		t.Fatal("init B failed")
	}
	before := snapshotTree(t, repo.OMK)

	var stdout, stderr strings.Builder
	code := RunRemove([]string{""}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("exit = %d, want 1; stderr=%q", code, stderr.String())
	}
	eq(t, "stdout", stdout.String(), "")
	eq(t, "stderr", stderr.String(),
		"omakase: no harness '' installed here (installed: "+srcA+", "+srcB+")\n")

	if after := snapshotTree(t, repo.OMK); !maps.Equal(before, after) {
		t.Errorf("empty-string-arg refusal mutated $OMK")
	}
	if out := gitStdout(dir, "status", "--porcelain"); out != "" {
		t.Errorf("empty-string-arg refusal dirtied the tree: %q", out)
	}
	if _, err := os.Lstat(repo.OMK); err != nil {
		t.Errorf("$OMK must still exist (no teardown happened): %v", err)
	}
}

// TestRemoveTwoPositionalsWithEmptyFirstIsUsageError: a latent twin of Fix #2
// in the positional-count check itself -- when the FIRST positional is "",
// testing `source != ""` to detect "already have one" also misfires (it never
// becomes true), so a genuine second positional slipped through as a silent
// source reassignment instead of the usage error every other two-positional
// invocation gets. Pins that argv{"", "b/c"} is a usage error like any other.
func TestRemoveTwoPositionalsWithEmptyFirstIsUsageError(t *testing.T) {
	initRepo(t)
	stubLefthook(t)

	var stdout, stderr strings.Builder
	code := RunRemove([]string{"", "b/c"}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("exit = %d, want 2; stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	eq(t, "stdout", stdout.String(), "")
	eq(t, "stderr", stderr.String(), removeUsageText)
}

// TestRemoveArgNothingInstalledIsNoOp pins Fix #7: `remove <src>` in a repo
// that was NEVER initialized ($OMK absent entirely) must land on the legacy
// "nothing installed here; nothing to remove." no-op (exit 0) -- never
// RequireLayers' "predates layered state -- run omakase init once first"
// refusal, which would falsely instruct the user to INSTALL the very harness
// they are trying to remove.
func TestRemoveArgNothingInstalledIsNoOp(t *testing.T) {
	initRepo(t)
	stubLefthook(t)

	var stdout, stderr strings.Builder
	code := RunRemove([]string{"foo/bar"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%q", code, stderr.String())
	}
	eq(t, "stdout", stdout.String(), "")
	eq(t, "stderr", stderr.String(), "omakase: nothing installed here; nothing to remove.\n")
}

// TestRemoveTopLayerMissingSurvivorStoreRefuses pins Fix #3: a TOP removal
// whose survivor store (layers/1) is missing -- e.g. a crash mid-publish
// during a prior bottom removal's BuildLayerStore RemoveAll-then-Rename
// window -- must refuse BEFORE any mutation rather than silently reading a
// missing placed.tsv as an empty survivor set and wiping the live state while
// sources.tsv still claims the survivor is installed.
func TestRemoveTopLayerMissingSurvivorStoreRefuses(t *testing.T) {
	dir, repo := initRepo(t)
	srcTestEnv(t)
	stubLefthook(t)
	useBasePayloadDir(t)

	srcA := newHarnessSource(t, "a", map[string]string{".omakase/gates/a.sh": "a\n"})
	srcB := newHarnessSource(t, "b", map[string]string{".omakase/gates/b.sh": "b\n"})
	if code := RunInit([]string{"--source", srcA}, io.Discard, io.Discard); code != 0 {
		t.Fatal("init A failed")
	}
	if code := RunInit([]string{"--source", srcB}, io.Discard, io.Discard); code != 0 {
		t.Fatal("init B failed")
	}

	// Simulate the crash-mid-publish state a prior BOTTOM removal (or repair)
	// could leave: layers/1 (the TOP removal's survivor store) is gone, while
	// sources.tsv still records both layers.
	if err := os.RemoveAll(filepath.Join(repo.OMK, "layers", "1")); err != nil {
		t.Fatal(err)
	}
	before := snapshotTree(t, repo.OMK)

	var stdout, stderr strings.Builder
	code := RunRemove([]string{srcB}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("exit = %d, want 1; stderr=%q", code, stderr.String())
	}
	eq(t, "stdout", stdout.String(), "")
	eq(t, "stderr", stderr.String(),
		"omakase: harness state is damaged (layers/1 missing) — run omakase init to reheal, or omakase remove to tear down\n")

	if after := snapshotTree(t, repo.OMK); !maps.Equal(before, after) {
		t.Errorf("damaged-survivor refusal mutated $OMK further")
	}
	if out := gitStdout(dir, "status", "--porcelain"); out != "" {
		t.Errorf("damaged-survivor refusal dirtied the tree: %q", out)
	}
	rows := state.ReadSources(filepath.Join(repo.OMK, "sources.tsv"))
	if len(rows) != 2 {
		t.Errorf("sources.tsv rewritten despite the refusal: %+v", rows)
	}
}

// TestRemoveBottomLayerMissingLayers2WrapsError pins the whole-branch-review
// Minor #2 fold-in: a BOTTOM removal whose surviving delta store (layers/2)
// is missing must refuse with an omakase:-prefixed message and a reheal hint
// -- never leak foldBaseUnder's raw `lstat ...: no such file or directory` Go
// error text straight to the user.
func TestRemoveBottomLayerMissingLayers2WrapsError(t *testing.T) {
	dir, repo := initRepo(t)
	srcTestEnv(t)
	stubLefthook(t)
	useBasePayloadDir(t)

	srcA := newHarnessSource(t, "a", map[string]string{".omakase/gates/a.sh": "a\n"})
	srcB := newHarnessSource(t, "b", map[string]string{".omakase/gates/b.sh": "b\n"})
	if code := RunInit([]string{"--source", srcA}, io.Discard, io.Discard); code != 0 {
		t.Fatal("init A failed")
	}
	if code := RunInit([]string{"--source", srcB}, io.Discard, io.Discard); code != 0 {
		t.Fatal("init B failed")
	}

	// Damage the surviving-delta store: layers/2 gone. Removing the BOTTOM (A)
	// re-folds the base under layers/2/files via foldBaseUnder.
	if err := os.RemoveAll(filepath.Join(repo.OMK, "layers", "2")); err != nil {
		t.Fatal(err)
	}
	before := snapshotTree(t, repo.OMK)

	var stdout, stderr strings.Builder
	code := RunRemove([]string{srcA}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("exit = %d, want 1; stderr=%q", code, stderr.String())
	}
	eq(t, "stdout", stdout.String(), "")
	eq(t, "stderr", stderr.String(),
		"omakase: harness state is damaged (layers/2 missing) — run omakase init to reheal, or omakase remove to tear down\n")

	if after := snapshotTree(t, repo.OMK); !maps.Equal(before, after) {
		t.Errorf("damaged-delta refusal mutated $OMK further")
	}
	if out := gitStdout(dir, "status", "--porcelain"); out != "" {
		t.Errorf("damaged-delta refusal dirtied the tree: %q", out)
	}
	rows := state.ReadSources(filepath.Join(repo.OMK, "sources.tsv"))
	if len(rows) != 2 {
		t.Errorf("sources.tsv rewritten despite the refusal: %+v", rows)
	}
}
