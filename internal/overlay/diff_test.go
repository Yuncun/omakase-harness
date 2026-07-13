package overlay

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// diff renders in the FORWARD direction — the user's question is "what did
// I change", so their edit must appear as an addition, never a deletion.
func TestDiffForwardDirection(t *testing.T) {
	dir, _ := placeTwoRules(t)
	editFile(t, filepath.Join(dir, ".claude/rules/a.md"))

	var stdout, stderr strings.Builder
	if code := RunDiff(nil, &stdout, &stderr); code != 0 {
		t.Fatalf("diff exit = %d, want 0; stderr=%q", code, stderr.String())
	}
	out := stdout.String()
	if !strings.Contains(out, ".claude/rules/a.md — your changes vs the harness version:") {
		t.Errorf("missing the per-file header:\n%s", out)
	}
	if !strings.Contains(out, "+# my edit") {
		t.Errorf("edit not shown as an addition (forward direction):\n%s", out)
	}
	if strings.Contains(out, "-# my edit") {
		t.Errorf("edit shown as a deletion (reverse direction):\n%s", out)
	}
	if strings.Contains(out, "b.md") {
		t.Errorf("unchanged file rendered:\n%s", out)
	}
}

// diff is strictly read-only: repo file, ledger, and snapshot are untouched
// — content and mtimes prove it wrote nothing.
func TestDiffWritesNothing(t *testing.T) {
	dir, repo := placeTwoRules(t)
	full := filepath.Join(dir, ".claude/rules/a.md")
	editFile(t, full)

	watched := []string{
		full,
		filepath.Join(repo.OMK, "placed.tsv"),
		filepath.Join(repo.OMK, "payload-snapshot", ".claude/rules/a.md"),
	}
	before := map[string]time.Time{}
	for _, p := range watched {
		info, err := os.Stat(p)
		if err != nil {
			t.Fatalf("stat %s: %v", p, err)
		}
		before[p] = info.ModTime()
	}
	content := readFileT(t, full)

	var stdout, stderr strings.Builder
	if code := RunDiff(nil, &stdout, &stderr); code != 0 {
		t.Fatalf("diff exit = %d; stderr=%q", code, stderr.String())
	}

	for _, p := range watched {
		info, err := os.Stat(p)
		if err != nil {
			t.Fatalf("stat %s after diff: %v", p, err)
		}
		if !info.ModTime().Equal(before[p]) {
			t.Errorf("diff modified %s (mtime changed)", p)
		}
	}
	eq(t, "edited content untouched", readFileT(t, full), content)
	if lexists(filepath.Join(repo.OMK, "kept")) {
		t.Errorf("diff created a kept/ directory")
	}
}

func TestDiffNoChanges(t *testing.T) {
	placeTwoRules(t)
	var stdout, stderr strings.Builder
	if code := RunDiff(nil, &stdout, &stderr); code != 0 {
		t.Fatalf("diff exit = %d; stderr=%q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "no changes") {
		t.Errorf("stdout = %q, want the no-changes line", stdout.String())
	}
}

// After a keep, the baseline is the ACCEPTED version: only the post-keep
// edit renders, labeled as vs your accepted (kept) version.
func TestDiffAfterKeepUsesAcceptedBaseline(t *testing.T) {
	dir, repo := placeTwoRules(t)
	rel := ".claude/rules/a.md"
	full := filepath.Join(dir, rel)
	editFile(t, full)
	if err := FileKeep(repo, rel); err != nil {
		t.Fatalf("FileKeep: %v", err)
	}
	f, err := os.OpenFile(full, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("second edit\n"); err != nil {
		t.Fatal(err)
	}
	f.Close()

	var stdout, stderr strings.Builder
	if code := RunDiff([]string{rel}, &stdout, &stderr); code != 0 {
		t.Fatalf("diff exit = %d; stderr=%q", code, stderr.String())
	}
	out := stdout.String()
	if !strings.Contains(out, "your accepted (kept) version") {
		t.Errorf("baseline label wrong:\n%s", out)
	}
	if !strings.Contains(out, "+second edit") {
		t.Errorf("post-keep edit not shown:\n%s", out)
	}
	if strings.Contains(out, "+# my edit") {
		t.Errorf("already-accepted edit re-rendered (baseline is not the kept copy):\n%s", out)
	}
}

// Path arguments resolve like the status toggles — exact rel or group
// directory; a typo refuses with exit 2 before printing any diff.
func TestDiffPathResolution(t *testing.T) {
	dir, _ := placeTwoRules(t)
	editFile(t, filepath.Join(dir, ".claude/rules/a.md"))
	editFile(t, filepath.Join(dir, ".claude/rules/b.md"))

	var stdout, stderr strings.Builder
	if code := RunDiff([]string{".claude/rules/b.md"}, &stdout, &stderr); code != 0 {
		t.Fatalf("diff exit = %d; stderr=%q", code, stderr.String())
	}
	if out := stdout.String(); strings.Contains(out, "a.md —") || !strings.Contains(out, "b.md —") {
		t.Errorf("exact-path arg did not scope the report:\n%s", out)
	}

	stdout.Reset()
	if code := RunDiff([]string{".claude/rules"}, &stdout, &stderr); code != 0 {
		t.Fatalf("group diff exit = %d; stderr=%q", code, stderr.String())
	}
	if out := stdout.String(); !strings.Contains(out, "a.md —") || !strings.Contains(out, "b.md —") {
		t.Errorf("group arg did not cover both children:\n%s", out)
	}

	stdout.Reset()
	stderr.Reset()
	if code := RunDiff([]string{"nope.md"}, &stdout, &stderr); code != 2 {
		t.Fatalf("unknown path: exit = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "unknown placed path") {
		t.Errorf("stderr = %q, want the unknown-path refusal", stderr.String())
	}
	if stdout.String() != "" {
		t.Errorf("partial report printed before the refusal: %q", stdout.String())
	}
}

// diff takes no flags beyond --help: read-only means no mutating flags can
// even exist, and a typo'd flag must not silently render the full report.
func TestDiffRefusesFlags(t *testing.T) {
	placeTwoRules(t)
	var stdout, stderr strings.Builder
	if code := RunDiff([]string{"--reverse"}, &stdout, &stderr); code != 2 {
		t.Fatalf("--reverse: exit = %d, want 2 (stderr=%q)", code, stderr.String())
	}
	stdout.Reset()
	if code := RunDiff([]string{"--help"}, &stdout, &stderr); code != 0 {
		t.Fatalf("--help: exit = %d, want 0", code)
	}
	if !strings.HasPrefix(stdout.String(), "usage: omakase diff") {
		t.Errorf("--help output = %q, want the usage block", stdout.String())
	}
}

// A missing enabled file is a change worth reporting (it is what amber
// means), but a disabled row is not — its absence is deliberate.
func TestDiffMissingAndDisabled(t *testing.T) {
	dir, repo := placeTwoRules(t)
	a, b := ".claude/rules/a.md", ".claude/rules/b.md"
	if err := os.Remove(filepath.Join(dir, a)); err != nil {
		t.Fatal(err)
	}
	if err := FileOff(repo, b); err != nil {
		t.Fatalf("FileOff: %v", err)
	}

	var stdout, stderr strings.Builder
	if code := RunDiff(nil, &stdout, &stderr); code != 0 {
		t.Fatalf("diff exit = %d; stderr=%q", code, stderr.String())
	}
	out := stdout.String()
	if !strings.Contains(out, a+" — missing from this worktree") {
		t.Errorf("missing enabled file not reported:\n%s", out)
	}
	if strings.Contains(out, b) {
		t.Errorf("disabled row reported:\n%s", out)
	}
}

// Outside a harness repo, diff says so and exits 1 — never a stack of git
// errors.
func TestDiffNotInstalled(t *testing.T) {
	initRepo(t)
	var stdout, stderr strings.Builder
	if code := RunDiff(nil, &stdout, &stderr); code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "no harness installed") {
		t.Errorf("stderr = %q, want the not-installed line", stderr.String())
	}
}
