package overlay

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// ---------------------------------------------------------------- CopyEntry
//
// Copying a regular file to a destination that does not yet exist carries the
// source's permission bits, masked by umask on creation (src mode 777 + umask
// 022 -> dest mode 755). Copying a symlink recreates the same link (the readlink
// target string, not the dereferenced content), dangling or not. CopyEntry
// unlinks any pre-existing destination before copying.

func TestCopyEntryRegularFilePreservesMode(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.sh")
	writeFile(t, src, "#!/bin/sh\necho hi\n")
	if err := os.Chmod(src, 0o755); err != nil {
		t.Fatal(err)
	}

	// CopyEntry does not create the destination directory; the caller mkdir -p's
	// it first, and so does this test.
	dst := filepath.Join(dir, "sub", "dst.sh")
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := CopyEntry(src, dst); err != nil {
		t.Fatalf("CopyEntry: %v", err)
	}

	gotContent, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if string(gotContent) != "#!/bin/sh\necho hi\n" {
		t.Errorf("content = %q", gotContent)
	}

	info, err := os.Stat(dst)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Errorf("dst mode = %o, want 0755", info.Mode().Perm())
	}
}

func TestCopyEntryOverwritesExistingRegularFile(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.txt")
	writeFile(t, src, "new content")

	dst := filepath.Join(dir, "dst.txt")
	writeFile(t, dst, "stale content, should be replaced entirely")

	if err := CopyEntry(src, dst); err != nil {
		t.Fatalf("CopyEntry: %v", err)
	}
	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "new content" {
		t.Errorf("dst content = %q, want %q (must not retain trailing stale bytes)", got, "new content")
	}
}

func TestCopyEntrySymlinkRecreatesTarget(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "CLAUDE.md")
	if err := os.Symlink("AGENTS.md", src); err != nil {
		t.Fatal(err)
	}

	dst := filepath.Join(dir, "out", "CLAUDE.md")
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := CopyEntry(src, dst); err != nil {
		t.Fatalf("CopyEntry: %v", err)
	}

	target, err := os.Readlink(dst)
	if err != nil {
		t.Fatalf("dst is not a symlink: %v", err)
	}
	if target != "AGENTS.md" {
		t.Errorf("dst symlink target = %q, want %q", target, "AGENTS.md")
	}
}

func TestCopyEntrySymlinkOverwritesExistingSymlink(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "link-a")
	if err := os.Symlink("target-a", src); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(dir, "link-b")
	if err := os.Symlink("target-b", dst); err != nil {
		t.Fatal(err)
	}

	if err := CopyEntry(src, dst); err != nil {
		t.Fatalf("CopyEntry: %v", err)
	}
	target, err := os.Readlink(dst)
	if err != nil {
		t.Fatal(err)
	}
	if target != "target-a" {
		t.Errorf("dst symlink target = %q, want %q", target, "target-a")
	}
}

func TestCopyEntryMissingSource(t *testing.T) {
	dir := t.TempDir()
	if err := CopyEntry(filepath.Join(dir, "nope"), filepath.Join(dir, "dst")); err == nil {
		t.Error("CopyEntry(missing source): want error, got nil")
	}
}

// ---------------------------------------------------------------- SameFile

func TestSameFile(t *testing.T) {
	dir := t.TempDir()

	fileA := filepath.Join(dir, "a.txt")
	fileAcopy := filepath.Join(dir, "a-copy.txt")
	fileB := filepath.Join(dir, "b.txt")
	writeFile(t, fileA, "identical content")
	writeFile(t, fileAcopy, "identical content")
	writeFile(t, fileB, "different content")

	linkToA := filepath.Join(dir, "link-to-a")
	if err := os.Symlink("a.txt", linkToA); err != nil {
		t.Fatal(err)
	}
	linkToAagain := filepath.Join(dir, "link-to-a-again")
	if err := os.Symlink("a.txt", linkToAagain); err != nil {
		t.Fatal(err)
	}
	linkToB := filepath.Join(dir, "link-to-b")
	if err := os.Symlink("b.txt", linkToB); err != nil {
		t.Fatal(err)
	}

	subdir := filepath.Join(dir, "subdir")
	if err := os.Mkdir(subdir, 0o755); err != nil {
		t.Fatal(err)
	}

	missing := filepath.Join(dir, "does-not-exist")

	cases := []struct {
		name string
		a, b string
		want bool
	}{
		{"identical byte content, neither a symlink", fileA, fileAcopy, true},
		{"differing byte content", fileA, fileB, false},
		{"two symlinks with the same target string", linkToA, linkToAagain, true},
		{"two symlinks with different targets", linkToA, linkToB, false},
		{"symlink vs the file it points to: compares target string, not content", linkToA, fileA, false},
		{"symlink vs a missing path: missing readlink -> empty string, not equal", linkToA, missing, false},
		{"neither exists: byte compare fails closed", missing, filepath.Join(dir, "also-missing"), false},
		{"directory operand (non-symlink) is never same", subdir, fileA, false},
		{"directory operand on the other side", fileA, subdir, false},
		{"same path both sides (regular file)", fileA, fileA, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := SameFile(tc.a, tc.b); got != tc.want {
				t.Errorf("SameFile(%q, %q) = %v, want %v", tc.a, tc.b, got, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------- DerivePrefixes
//
// DerivePrefixes dedups owned top dirs to their bare first segment, keeps .github
// (the one HARNESS_SHARED_TOPDIRS entry) as full placed paths, appends the
// .worktreeinclude wiring entry unless tracked, then gives every entry a
// trailing "/" iff it names a directory on the live repo.

func TestDerivePrefixes(t *testing.T) {
	sharedTopdirs := []string{".github"}

	dirSet := map[string]bool{
		".claude":                 true,
		".omakase":                true,
		".github/workflows":       false, // it's a file in this fixture
		".worktreeinclude":        false,
		"AGENTS.md":               false,
		"top-level-dir-no-nested": true,
	}
	isDir := func(p string) bool { return dirSet[p] }

	cases := []struct {
		name         string
		placed       []string
		wtincTracked bool
		want         []string
	}{
		{
			name: "owned topdirs deduped to bare segment; .github kept as full path; wiring entry appended with trailing slash logic",
			placed: []string{
				".claude/rules/a.md",
				".claude/skills/b/SKILL.md", // second .claude/* entry: deduped to the same bare "claude" prefix, not re-added
				".github/workflows/ci.yml",
				"AGENTS.md",
			},
			wtincTracked: false,
			want: []string{
				".claude/",                 // owned topdir, isDir(".claude")=true
				".github/workflows/ci.yml", // shared topdir: full placed path, not a dir here
				"AGENTS.md",                // root file, no "/", not a dir
				".worktreeinclude",         // wiring entry
			},
		},
		{
			name:         ".worktreeinclude tracked: not appended",
			placed:       []string{".omakase/foo.sh"},
			wtincTracked: true,
			want:         []string{".omakase/"},
		},
		{
			name:         "root-level single-segment path with no nested content: bare rel used verbatim, trailing slash iff isDir",
			placed:       []string{"top-level-dir-no-nested"},
			wtincTracked: true,
			want:         []string{"top-level-dir-no-nested/"},
		},
		{
			name:         "empty placed set still gets the wiring entry",
			placed:       nil,
			wtincTracked: false,
			want:         []string{".worktreeinclude"},
		},
		{
			name:         "empty rel entries in placed are skipped",
			placed:       []string{"", ".claude/x", ""},
			wtincTracked: true,
			want:         []string{".claude/"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := DerivePrefixes(tc.placed, sharedTopdirs, isDir, tc.wtincTracked)
			if !equalStrings(got, tc.want) {
				t.Errorf("DerivePrefixes(%v) = %v, want %v", tc.placed, got, tc.want)
			}
		})
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// ---------------------------------------------------------------- DeletePlaced

func TestDeletePlacedRemovesUntrackedFileAndPrunesEmptyParents(t *testing.T) {
	dir := t.TempDir()
	rel := ".claude/rules/deep/leaf.md"
	writeFile(t, filepath.Join(dir, rel), "content")

	isTracked := func(string) bool { return false }
	if err := DeletePlaced(dir, rel, isTracked); err != nil {
		t.Fatalf("DeletePlaced: %v", err)
	}

	if _, err := os.Lstat(filepath.Join(dir, rel)); !os.IsNotExist(err) {
		t.Errorf("file still exists after DeletePlaced")
	}
	if _, err := os.Lstat(filepath.Join(dir, ".claude", "rules", "deep")); !os.IsNotExist(err) {
		t.Errorf("emptied dir 'deep' was not pruned")
	}
	if _, err := os.Lstat(filepath.Join(dir, ".claude", "rules")); !os.IsNotExist(err) {
		t.Errorf("emptied dir 'rules' was not pruned")
	}
	if _, err := os.Lstat(filepath.Join(dir, ".claude")); !os.IsNotExist(err) {
		t.Errorf("emptied dir '.claude' was not pruned")
	}
	// The repo root itself must never be pruned.
	if _, err := os.Lstat(dir); err != nil {
		t.Errorf("repo root was removed: %v", err)
	}
}

func TestDeletePlacedStopsPruningAtNonEmptyDir(t *testing.T) {
	dir := t.TempDir()
	rel := ".claude/rules/leaf.md"
	writeFile(t, filepath.Join(dir, rel), "content")
	writeFile(t, filepath.Join(dir, ".claude", "rules", "sibling.md"), "kept")

	isTracked := func(string) bool { return false }
	if err := DeletePlaced(dir, rel, isTracked); err != nil {
		t.Fatalf("DeletePlaced: %v", err)
	}

	if _, err := os.Lstat(filepath.Join(dir, rel)); !os.IsNotExist(err) {
		t.Errorf("leaf.md still exists")
	}
	if _, err := os.Lstat(filepath.Join(dir, ".claude", "rules")); err != nil {
		t.Errorf("'rules' dir was pruned even though sibling.md remains: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(dir, ".claude", "rules", "sibling.md")); err != nil {
		t.Errorf("sibling.md was unexpectedly removed: %v", err)
	}
}

func TestDeletePlacedSkipsTrackedPath(t *testing.T) {
	dir := t.TempDir()
	rel := "AGENTS.md"
	writeFile(t, filepath.Join(dir, rel), "tracked content, git owns it")

	isTracked := func(r string) bool { return r == rel }
	if err := DeletePlaced(dir, rel, isTracked); err != nil {
		t.Fatalf("DeletePlaced: %v", err)
	}

	if _, err := os.Lstat(filepath.Join(dir, rel)); err != nil {
		t.Errorf("tracked file was deleted: %v", err)
	}
}

func TestDeletePlacedRootLevelFileNoPruning(t *testing.T) {
	dir := t.TempDir()
	rel := "AGENTS.md"
	writeFile(t, filepath.Join(dir, rel), "content")

	isTracked := func(string) bool { return false }
	if err := DeletePlaced(dir, rel, isTracked); err != nil {
		t.Fatalf("DeletePlaced: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(dir, rel)); !os.IsNotExist(err) {
		t.Errorf("file still exists")
	}
	if _, err := os.Lstat(dir); err != nil {
		t.Errorf("repo root was removed: %v", err)
	}
}

func TestDeletePlacedMissingFileIsNotAnError(t *testing.T) {
	dir := t.TempDir()
	isTracked := func(string) bool { return false }
	if err := DeletePlaced(dir, "never-existed.md", isTracked); err != nil {
		t.Errorf("DeletePlaced(missing): want nil (rm -f semantics), got %v", err)
	}
}

// realGitRepo builds a temp git repo and returns an isTracked closure backed by
// real `git ls-files`, exercising DeletePlaced against a live git
// tracked/untracked distinction rather than a stub.
func realGitRepo(t *testing.T) (dir string, isTracked func(string) bool) {
	t.Helper()
	dir = t.TempDir()
	runGitT(t, dir, "init", "-q")
	runGitT(t, dir, "config", "user.email", "t@t")
	runGitT(t, dir, "config", "user.name", "t")
	runGitT(t, dir, "config", "commit.gpgsign", "false")
	isTracked = func(rel string) bool {
		cmd := exec.Command("git", "-C", dir, "ls-files", "--error-unmatch", "--", rel)
		return cmd.Run() == nil
	}
	return dir, isTracked
}

func runGitT(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func TestDeletePlacedWithRealGitTracked(t *testing.T) {
	dir, isTracked := realGitRepo(t)
	writeFile(t, filepath.Join(dir, "AGENTS.md"), "committed\n")
	runGitT(t, dir, "add", "AGENTS.md")
	runGitT(t, dir, "commit", "-q", "-m", "init")

	if err := DeletePlaced(dir, "AGENTS.md", isTracked); err != nil {
		t.Fatalf("DeletePlaced: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(dir, "AGENTS.md")); err != nil {
		t.Errorf("tracked AGENTS.md was deleted: %v", err)
	}
}

func TestDeletePlacedWithRealGitUntracked(t *testing.T) {
	dir, isTracked := realGitRepo(t)
	writeFile(t, filepath.Join(dir, ".claude", "rules", "a.md"), "untracked\n")

	if err := DeletePlaced(dir, ".claude/rules/a.md", isTracked); err != nil {
		t.Fatalf("DeletePlaced: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(dir, ".claude")); !os.IsNotExist(err) {
		t.Errorf(".claude was not pruned away")
	}
}

// ---------------------------------------------------------------- safeMkdirAll
//
// safeMkdirAll is the security primitive both init place guards (placeFile and
// the payload-snapshot copy loop) call instead of a bare os.MkdirAll: it refuses
// to create a directory whose parent chain passes through a symlink, so a payload
// that ships a directory symlink at a parent path cannot make a later child write
// land outside the repo. This is the single-payload unit coverage of that guard;
// it must never silently degrade to a bare os.MkdirAll.

// TestSafeMkdirAllRefusesSymlinkedParent: a symlink at a parent component makes
// safeMkdirAll refuse (never following it), and no directory is created through
// the symlink at the symlink's target.
func TestSafeMkdirAllRefusesSymlinkedParent(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()

	// A payload placed `data` as a directory symlink pointing outside the repo.
	if err := os.Symlink(outside, filepath.Join(root, "data")); err != nil {
		t.Fatal(err)
	}

	err := safeMkdirAll(root, filepath.Join(root, "data", "loot"))
	if err == nil {
		t.Fatal("safeMkdirAll followed a symlinked parent (want a fail-closed refusal)")
	}
	// The refusal must not have created anything through the symlink.
	if _, statErr := os.Stat(filepath.Join(outside, "loot")); !os.IsNotExist(statErr) {
		t.Errorf("safeMkdirAll wrote through the symlink to %s (statErr=%v)", filepath.Join(outside, "loot"), statErr)
	}
}

// TestSafeMkdirAllCreatesRealNestedDir: with no symlink in the way, safeMkdirAll
// creates the full nested path as real directories (the ordinary success path).
func TestSafeMkdirAllCreatesRealNestedDir(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "a", "b", "c")
	if err := safeMkdirAll(root, dir); err != nil {
		t.Fatalf("safeMkdirAll on a clean path: %v", err)
	}
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() {
		t.Fatalf("nested dir not created as a real directory: info=%v err=%v", info, err)
	}
}

// TestSafeMkdirAllRefusesEscape: a target outside root is refused before any
// filesystem mutation.
func TestSafeMkdirAllRefusesEscape(t *testing.T) {
	root := t.TempDir()
	if err := safeMkdirAll(root, filepath.Join(filepath.Dir(root), "elsewhere")); err == nil {
		t.Fatal("safeMkdirAll accepted a path escaping root (want a refusal)")
	}
}
