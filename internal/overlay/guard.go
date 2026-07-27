// This file implements the `omakase guard` plumbing verb — worktree
// discipline BEFORE the edit happens (issue #86), the Go port of the retired
// payload script omakase-worktree-guard.sh (#172). It is an opt-in Claude
// Code PreToolUse hook (matcher "Edit|Write"; init prints how to wire it,
// pointing at the stable machine-wide binary like the git dispatchers do).
// While other worktrees are active, an Edit/Write to a product file in the
// MAIN checkout is denied with a teaching message: branches cut in the main
// checkout inherit concurrent sessions' uncommitted work, which then leaks
// into a PR. Implementation goes in a worktree; the main checkout is for
// harness/coordination files.
//
// The allowlist mirrors the commit-time gate's (AGENTS.md, CLAUDE.md,
// .claude/**, root *.md) plus two classes an EDIT-time layer must not block
// because they cannot leak into a commit: .omakase/** (the placed overlay,
// force-excluded from git) and .git/**.
//
// Standdowns, same as the gate: OMAKASE_SKIP_WORKTREE_DISCIPLINE=1 (audited,
// per invocation) or a "worktree-discipline" line in the shared
// disabled-gates file (the persistent, visible disable via
// `omakase status --disable`).
//
// This layer fails OPEN: anything it cannot parse or resolve is allowed
// silently (exit 0, no output). It is a pre-layer for the developer's
// attention; the commit-time gate is the layer that fails closed.
package overlay

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// guardDeny is the deny decision, emitted in BOTH hook-output shapes on
// purpose (#164 C3): Claude Code reads the nested hookSpecificOutput block;
// Copilot CLI without _vsCodeCompat reads only the top-level keys and would
// silently drop a nested-only deny — the one failure mode a guard must not
// have. Field order matters to consumers grepping the top-level shape first.
type guardDeny struct {
	PermissionDecision       string         `json:"permissionDecision"`
	PermissionDecisionReason string         `json:"permissionDecisionReason"`
	HookSpecificOutput       guardDenyInner `json:"hookSpecificOutput"`
}

type guardDenyInner struct {
	HookEventName            string `json:"hookEventName"`
	PermissionDecision       string `json:"permissionDecision"`
	PermissionDecisionReason string `json:"permissionDecisionReason"`
}

// RunGuard is the `omakase guard` verb. It reads one PreToolUse hook JSON
// payload from stdin and either stays silent (allow) or prints a deny
// decision; the exit code is always 0 — the decision travels in the JSON.
func RunGuard(_ []string, stdin io.Reader, stdout, _ io.Writer) int {
	// A leaked GIT_DIR/GIT_WORK_TREE/GIT_COMMON_DIR (exported for ANOTHER
	// repo) would judge the wrong repo's worktrees. Resolve from the hook's
	// cwd only.
	for _, v := range []string{"GIT_DIR", "GIT_WORK_TREE", "GIT_COMMON_DIR"} {
		os.Unsetenv(v)
	}

	if os.Getenv("OMAKASE_SKIP_WORKTREE_DISCIPLINE") == "1" {
		return 0
	}

	var in struct {
		Cwd       string `json:"cwd"`
		FilePath  string `json:"file_path"`
		ToolInput struct {
			FilePath string `json:"file_path"`
		} `json:"tool_input"`
	}
	if err := json.NewDecoder(stdin).Decode(&in); err != nil {
		return 0
	}
	cwd := in.Cwd
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	fp := in.ToolInput.FilePath
	if fp == "" {
		fp = in.FilePath
	}
	if fp == "" {
		return 0
	}
	if !filepath.IsAbs(fp) {
		fp = filepath.Join(cwd, fp)
	}

	root := gitOutTrim(cwd, "rev-parse", "--show-toplevel")
	if root == "" {
		return 0
	}

	// Fire only in the MAIN checkout while other worktrees exist. The main
	// checkout is the first `worktree` record; rev-parse and worktree-list
	// both report physical paths, so string equality is the same test the
	// commit gate uses.
	var worktrees []string
	for _, line := range strings.Split(gitStdout(cwd, "worktree", "list", "--porcelain"), "\n") {
		if p, ok := strings.CutPrefix(line, "worktree "); ok {
			worktrees = append(worktrees, p)
		}
	}
	if len(worktrees) <= 1 || root != worktrees[0] {
		return 0
	}

	// A persistent disable (status --disable) stands the guard down with the
	// gate.
	gcd := gitOutTrim(cwd, "rev-parse", "--git-common-dir")
	if gcd == "" {
		return 0
	}
	if !filepath.IsAbs(gcd) {
		gcd = filepath.Join(cwd, gcd)
	}
	if disabled, err := os.ReadFile(filepath.Join(gcd, "omakase", "disabled-gates")); err == nil {
		for _, line := range strings.Split(string(disabled), "\n") {
			if line == "worktree-discipline" {
				return 0
			}
		}
	}

	// Only files INSIDE this checkout are its contamination; editing another
	// worktree's files from here is the desired flow.
	rel, ok := strings.CutPrefix(fp, root+"/")
	if !ok {
		return 0
	}

	// The allowlist. Nested paths outside the allowed trees are the deny
	// class; a bare *.md can only be ROOT-level markdown here.
	switch {
	case rel == "AGENTS.md" || rel == "CLAUDE.md":
		return 0
	case strings.HasPrefix(rel, ".claude/") || strings.HasPrefix(rel, ".omakase/") || strings.HasPrefix(rel, ".git/"):
		return 0
	case !strings.Contains(rel, "/") && strings.HasSuffix(rel, ".md"):
		return 0
	}

	reason := fmt.Sprintf("omakase worktree discipline: '%s' is a product file and this is the MAIN checkout while %d other worktree(s) are active. Branches cut here inherit concurrent sessions' uncommitted work. Edit it in a worktree instead (the main checkout is for coordination: AGENTS.md, CLAUDE.md, .claude/**, root *.md). Bypass (audited): OMAKASE_SKIP_WORKTREE_DISCIPLINE=1.", rel, len(worktrees)-1)
	enc := json.NewEncoder(stdout)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(guardDeny{
		PermissionDecision:       "deny",
		PermissionDecisionReason: reason,
		HookSpecificOutput: guardDenyInner{
			HookEventName:            "PreToolUse",
			PermissionDecision:       "deny",
			PermissionDecisionReason: reason,
		},
	})
	return 0
}
