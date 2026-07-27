package overlay

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// guardRepo creates a committed git repo and returns its physical root (the
// path git itself reports, so string comparisons against `git worktree list`
// hold on macOS's symlinked TMPDIR).
func guardRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q"},
		{"config", "user.email", "t@t"},
		{"config", "user.name", "t"},
		{"config", "commit.gpgsign", "false"},
		{"commit", "-q", "--allow-empty", "-m", "init"},
	} {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	root := gitOutTrim(dir, "rev-parse", "--show-toplevel")
	if root == "" {
		t.Fatal("no toplevel for fixture repo")
	}
	return root
}

// runGuard feeds one PreToolUse JSON payload to RunGuard and returns its
// stdout and exit code.
func runGuard(t *testing.T, cwd, filePath string) (string, int) {
	t.Helper()
	in := map[string]any{
		"tool_name":  "Edit",
		"tool_input": map[string]any{"file_path": filePath, "old_string": "a", "new_string": "b"},
		"cwd":        cwd,
	}
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	rc := RunGuard(nil, bytes.NewReader(raw), &stdout, &stderr)
	return stdout.String(), rc
}

func TestGuardAllowsWithoutOtherWorktrees(t *testing.T) {
	root := guardRepo(t)
	out, rc := runGuard(t, root, filepath.Join(root, "src", "app.go"))
	if rc != 0 || out != "" {
		t.Fatalf("want silent allow, got rc=%d out=%q", rc, out)
	}
}

func TestGuardDeniesProductFileInMainCheckout(t *testing.T) {
	root := guardRepo(t)
	addWorktree(t, root, "guard-wt")
	out, rc := runGuard(t, root, filepath.Join(root, "src", "app.go"))
	if rc != 0 {
		t.Fatalf("deny must exit 0 (decision travels in JSON), got %d", rc)
	}
	// Both output shapes (#164 C3): top-level for Copilot, nested for Claude.
	if !strings.HasPrefix(out, `{"permissionDecision":"deny","permissionDecisionReason":`) {
		t.Errorf("top-level shape missing (Copilot would fail open): %q", out)
	}
	if !strings.Contains(out, `"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny"`) {
		t.Errorf("nested shape missing (Claude): %q", out)
	}
	for _, want := range []string{"src/app.go", "worktree", "OMAKASE_SKIP_WORKTREE_DISCIPLINE=1"} {
		if !strings.Contains(out, want) {
			t.Errorf("reason missing %q: %q", want, out)
		}
	}
	var decoded struct {
		PermissionDecision string `json:"permissionDecision"`
	}
	if err := json.Unmarshal([]byte(out), &decoded); err != nil || decoded.PermissionDecision != "deny" {
		t.Errorf("output is not valid deny JSON (%v): %q", err, out)
	}
}

func TestGuardRelativePathResolvesAgainstCwd(t *testing.T) {
	root := guardRepo(t)
	addWorktree(t, root, "guard-wt")
	out, _ := runGuard(t, root, "src/app.go")
	if !strings.Contains(out, `"permissionDecision":"deny"`) {
		t.Fatalf("relative file_path escaped the guard: %q", out)
	}
}

func TestGuardAllowlist(t *testing.T) {
	root := guardRepo(t)
	addWorktree(t, root, "guard-wt")
	for _, rel := range []string{
		"AGENTS.md", "CLAUDE.md", ".claude/settings.json", "README.md",
		".omakase/gates/mine.sh", ".git/info/exclude",
	} {
		out, rc := runGuard(t, root, filepath.Join(root, rel))
		if rc != 0 || out != "" {
			t.Errorf("allowlist %s: want silent allow, got rc=%d out=%q", rel, rc, out)
		}
	}
	// Nested markdown is NOT allowlisted — only root-level *.md is coordination.
	out, _ := runGuard(t, root, filepath.Join(root, "docs", "notes.md"))
	if !strings.Contains(out, `"permissionDecision":"deny"`) {
		t.Errorf("nested .md escaped: %q", out)
	}
}

func TestGuardAllowsOtherWorktreeAndLinkedCheckout(t *testing.T) {
	root := guardRepo(t)
	wt := addWorktree(t, root, "guard-wt")
	// Editing another worktree's file from the main checkout is the desired flow.
	if out, _ := runGuard(t, root, filepath.Join(wt, "src", "app.go")); out != "" {
		t.Errorf("other-worktree file denied: %q", out)
	}
	// From a linked worktree everything is allowed.
	if out, _ := runGuard(t, wt, filepath.Join(wt, "src", "app.go")); out != "" {
		t.Errorf("denied inside a linked worktree: %q", out)
	}
}

func TestGuardStanddowns(t *testing.T) {
	root := guardRepo(t)
	addWorktree(t, root, "guard-wt")
	target := filepath.Join(root, "src", "app.go")

	t.Setenv("OMAKASE_SKIP_WORKTREE_DISCIPLINE", "1")
	if out, _ := runGuard(t, root, target); out != "" {
		t.Errorf("skip env ignored: %q", out)
	}
	t.Setenv("OMAKASE_SKIP_WORKTREE_DISCIPLINE", "")

	// Persistent disable: a "worktree-discipline" line in the shared
	// disabled-gates file (omakase status --disable).
	gcd := gitOutTrim(root, "rev-parse", "--git-common-dir")
	if !filepath.IsAbs(gcd) {
		gcd = filepath.Join(root, gcd)
	}
	if err := os.MkdirAll(filepath.Join(gcd, "omakase"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gcd, "omakase", "disabled-gates"), []byte("worktree-discipline\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, _ := runGuard(t, root, target); out != "" {
		t.Errorf("disabled-gates ignored: %q", out)
	}
}

func TestGuardFailsOpen(t *testing.T) {
	// Outside any repo.
	if out, rc := runGuard(t, t.TempDir(), "/x/y.go"); rc != 0 || out != "" {
		t.Errorf("fired outside a repo: rc=%d out=%q", rc, out)
	}
	// No file_path in the payload.
	root := guardRepo(t)
	addWorktree(t, root, "guard-wt")
	var stdout bytes.Buffer
	rc := RunGuard(nil, strings.NewReader(`{"tool_name":"Edit","tool_input":{},"cwd":"`+root+`"}`), &stdout, &stdout)
	if rc != 0 || stdout.Len() != 0 {
		t.Errorf("fired with no file_path: rc=%d out=%q", rc, stdout.String())
	}
	// Unparseable stdin.
	stdout.Reset()
	rc = RunGuard(nil, strings.NewReader("not json"), &stdout, &stdout)
	if rc != 0 || stdout.Len() != 0 {
		t.Errorf("fired on garbage stdin: rc=%d out=%q", rc, stdout.String())
	}
}
