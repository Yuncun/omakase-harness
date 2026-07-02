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
// Reference behavior probed against the live macOS `cp -P` (see task-1
// report for the exact commands/outputs): copying a regular file to a
// destination that does not yet exist carries the SOURCE's permission bits
// (masked by umask on creation, same as any open(..., O_CREAT, mode) —
// confirmed: src mode 777 + umask 022 -> dest mode 755); copying a symlink
// recreates the same link (readlink target string, not the dereferenced
// content) at the destination, dangling or not. All current bash callers
// (place_file, the source-merge overlay loop, the clobbered/ backup) already
// `rm -f`/`rm -rf` the destination before calling `cp -P`, so CopyEntry
// unlinks any pre-existing destination itself rather than reproducing cp's
// quirky "write through an existing destination symlink" behavior for a
// regular-file source — that quirk is exactly why every bash caller
// pre-unlinks in the first place (bin/init.sh:433-441's comment); no current
// or planned caller relies on it. Documented divergence, not a bug.

func TestCopyEntryRegularFilePreservesMode(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.sh")
	writeFile(t, src, "#!/bin/sh\necho hi\n")
	if err := os.Chmod(src, 0o755); err != nil {
		t.Fatal(err)
	}

	// CopyEntry mirrors bare `cp -P`, which does NOT create the destination
	// directory — bash's caller (place_file, bin/init.sh:424) does that
	// itself with a separate `mkdir -p` before calling cp -P. So does this
	// test.
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
// Scenario shape lifted from tests/golden-state.test.sh:43-62 (derive_block):
// dedup owned top dirs to their bare first segment, .github (the one
// HARNESS_SHARED_TOPDIRS entry frozen as of v1) kept as full placed paths,
// then lefthook.yml / .worktreeinclude wiring entries appended unless
// tracked, then every entry gets a trailing "/" iff it names a directory on
// the live repo.

func TestDerivePrefixes(t *testing.T) {
	sharedTopdirs := []string{".github"}

	dirSet := map[string]bool{
		".claude":                 true,
		".omakase":                true,
		".github/workflows":       false, // it's a file in this fixture
		"lefthook.yml":            false,
		".worktreeinclude":        false,
		"AGENTS.md":               false,
		"top-level-dir-no-nested": true,
	}
	isDir := func(p string) bool { return dirSet[p] }

	cases := []struct {
		name            string
		placed          []string
		lefthookTracked bool
		wtincTracked    bool
		want            []string
	}{
		{
			name: "owned topdirs deduped to bare segment; .github kept as full path; both wiring entries appended with trailing slash logic",
			placed: []string{
				".claude/rules/a.md",
				".claude/skills/b/SKILL.md", // second .claude/* entry: deduped to the SAME bare "claude" prefix, not re-added
				".github/workflows/ci.yml",
				"AGENTS.md",
			},
			lefthookTracked: false,
			wtincTracked:    false,
			want: []string{
				".claude/",                 // owned topdir, isDir(".claude")=true
				".github/workflows/ci.yml", // shared topdir: full placed path, not a dir here
				"AGENTS.md",                // root file, no "/", not a dir
				"lefthook.yml",             // wiring entry 1
				".worktreeinclude",         // wiring entry 2
			},
		},
		{
			name:            "lefthook.yml tracked: no wiring entry for it; .worktreeinclude still appended",
			placed:          []string{".omakase/foo.sh"},
			lefthookTracked: true,
			wtincTracked:    false,
			want:            []string{".omakase/", ".worktreeinclude"},
		},
		{
			name:            "both wiring entries tracked: neither appended",
			placed:          []string{".omakase/foo.sh"},
			lefthookTracked: true,
			wtincTracked:    true,
			want:            []string{".omakase/"},
		},
		{
			name:            "root-level single-segment path with no nested content: bare rel used verbatim, trailing slash iff isDir",
			placed:          []string{"top-level-dir-no-nested"},
			lefthookTracked: true,
			wtincTracked:    true,
			want:            []string{"top-level-dir-no-nested/"},
		},
		{
			name:            "empty placed set still gets both wiring entries",
			placed:          nil,
			lefthookTracked: false,
			wtincTracked:    false,
			want:            []string{"lefthook.yml", ".worktreeinclude"},
		},
		{
			name:            "empty rel entries in placed are skipped",
			placed:          []string{"", ".claude/x", ""},
			lefthookTracked: true,
			wtincTracked:    true,
			want:            []string{".claude/"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := DerivePrefixes(tc.placed, sharedTopdirs, isDir, tc.lefthookTracked, tc.wtincTracked)
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

// realGitRepo builds a temp git repo and returns an isTracked closure backed
// by real `git ls-files`, exercising DeletePlaced against a live git
// tracked/untracked distinction rather than a stub — mirrors the newGitRepo
// pattern in internal/status/inventory_test.go.
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
