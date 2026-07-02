package harness

import "testing"

// TestKindOf copies the exact classification vectors of
// tests/harness-paths.test.sh:17-46 (all 24 eq lines) — the bash suite's
// proof that kind_of() recognizes the Claude Code layout, the GitHub
// Copilot layout, and the host-agnostic + catch-all cases.
func TestKindOf(t *testing.T) {
	cases := []struct {
		path string
		want string
	}{
		// == kind_of: Claude Code ==
		{".claude/rules/style.md", "rule"},
		{".claude/skills/foo/SKILL.md", "skill"},
		{".claude/commands/x.md", "command"},
		{".claude/agents/reviewer.md", "agent"},
		{".claude/hooks/pre-commit.sh", "gate"},
		{".claude/settings.json", "config"},
		{".claude/settings.local.json", "config"},
		{"CLAUDE.md", "doc"},

		// == kind_of: GitHub Copilot ==
		{".github/skills/foo/SKILL.md", "skill"},
		{".github/skills/a/b/c.md", "skill"}, // deep skill subtree
		{".github/instructions/x.instructions.md", "rule"},
		{".github/prompts/triage.prompt.md", "prompt"},
		{".github/chatmodes/coach.chatmode.md", "prompt"},
		{".github/hooks/check-verify-gate.py", "gate"},
		{".github/hooks/check-verify-gate.json", "gate"},
		{".github/copilot-instructions.md", "doc"},
		// Boundary that protects the project's OWN .github content: a
		// non-harness .github file must fall through to 'other', never be
		// mistaken for an injected harness artifact.
		{".github/workflows/ci.yml", "other"},
		{".github/dependabot.yml", "other"},

		// == kind_of: host-agnostic + catch-alls ==
		{"lefthook-local.yml", "gate"},
		{".omakase/gates/example.sh", "gate"},
		{".husky/pre-commit", "gate"},
		{".githooks/pre-commit", "gate"},
		{"README.md", "doc"},
		{"some/nested/file.txt", "other"},
	}

	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			if got := KindOf(tc.path); got != tc.want {
				t.Errorf("KindOf(%q) = %q, want %q", tc.path, got, tc.want)
			}
		})
	}
}

// locDirs mirrors bin/lib-harness-paths.sh HARNESS_LOC_DIRS. It is NOT part
// of the Go API (no consumer needs the list itself yet, only the invariant
// below) but the anti-drift lock needs the same probe set the bash test
// walks.
var locDirs = []string{
	".claude/rules", ".claude/skills", ".claude/commands", ".claude/agents", ".claude/hooks",
	".github/skills", ".github/instructions", ".github/prompts", ".github/chatmodes", ".github/hooks",
	".omakase", ".husky", ".githooks",
}

// TestKindOfAntiDrift ports the anti-drift lock in
// tests/harness-paths.test.sh:67-72: every capture dir omakase imports
// (HARNESS_LOC_DIRS) must classify to a real kind, not "other" — the exact
// bug (a new capture dir added without a matching kind_of case) the bash
// test catches. .omakase is skipped: it is omakase's own mixed plumbing
// dir, where bin/ and VERSION legitimately fall to "other".
func TestKindOfAntiDrift(t *testing.T) {
	for _, d := range locDirs {
		if d == ".omakase" {
			continue
		}
		t.Run(d, func(t *testing.T) {
			if got := KindOf(d + "/probe"); got == "other" {
				t.Errorf("KindOf(%q) = other, want a real kind (LOC_DIR %s has no kind_of case)", d+"/probe", d)
			}
		})
	}
}

// TestSharedTopdirs pins SharedTopdirs to bin/lib-harness-paths.sh:72's
// HARNESS_SHARED_TOPDIRS (.github). The exclude-block derivation
// (overlay.DerivePrefixes) hands this list in, so a drift here would change
// whether .github is excluded wholesale or file-by-file.
func TestSharedTopdirs(t *testing.T) {
	want := []string{".github"}
	if len(SharedTopdirs) != len(want) {
		t.Fatalf("len(SharedTopdirs) = %d, want %d: %v", len(SharedTopdirs), len(want), SharedTopdirs)
	}
	for i := range want {
		if SharedTopdirs[i] != want[i] {
			t.Errorf("SharedTopdirs[%d] = %q, want %q", i, SharedTopdirs[i], want[i])
		}
	}
}

// TestCommittedGlobs pins the verbatim order of the 16-entry
// HARNESS_COMMITTED_GLOBS array (bin/lib-harness-paths.sh:61).
func TestCommittedGlobs(t *testing.T) {
	want := []string{
		"AGENTS.md", "CLAUDE.md", "CLAUDE.local.md", ".claude",
		"lefthook.yml", "lefthook-local.yml", ".lefthook", ".omakase",
		".husky", ".githooks", ".github/copilot-instructions.md",
		".github/instructions", ".github/skills", ".github/prompts",
		".github/chatmodes", ".github/hooks",
	}
	if len(CommittedGlobs) != len(want) {
		t.Fatalf("len(CommittedGlobs) = %d, want %d: %v", len(CommittedGlobs), len(want), CommittedGlobs)
	}
	for i := range want {
		if CommittedGlobs[i] != want[i] {
			t.Errorf("CommittedGlobs[%d] = %q, want %q", i, CommittedGlobs[i], want[i])
		}
	}
}
