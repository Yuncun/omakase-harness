package harness

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// TestKindOf pins the classification vectors: the Claude Code layout, the
// GitHub Copilot layout, and the host-agnostic + catch-all cases.
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
		{".github/copilot/settings.json", "config"},
		{".github/copilot/settings.local.json", "config"},
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

// locDirs is the set of capture dirs omakase imports, used only as the
// probe set for the anti-drift lock below.
var locDirs = []string{
	".claude/rules", ".claude/skills", ".claude/commands", ".claude/agents", ".claude/hooks",
	".github/skills", ".github/instructions", ".github/prompts", ".github/chatmodes", ".github/hooks",
	".omakase", ".husky", ".githooks",
}

// TestKindOfAntiDrift: every capture dir omakase imports must classify to a
// real kind, not "other" — catches a new capture dir added without a
// matching KindOf case. .omakase is skipped: it is omakase's own mixed
// plumbing dir, where bin/ and VERSION legitimately fall to "other".
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

// TestSharedTopdirs pins SharedTopdirs to bin/lib-harness-paths.sh's
// HARNESS_SHARED_TOPDIRS array, parsed from the bash source so the two
// cannot drift. A drift would change whether .github is excluded wholesale
// or file-by-file (overlay.DerivePrefixes consumes this list).
func TestSharedTopdirs(t *testing.T) {
	data, err := os.ReadFile("../../bin/lib-harness-paths.sh")
	if err != nil {
		t.Fatal(err)
	}
	src := string(data)

	re := regexp.MustCompile(`(?m)^HARNESS_SHARED_TOPDIRS=\(([^)]*)\)$`)
	m := re.FindStringSubmatch(src)
	if m == nil {
		t.Fatal(`HARNESS_SHARED_TOPDIRS=(...) not found in bin/lib-harness-paths.sh`)
	}
	want := strings.Fields(m[1])
	if len(want) == 0 {
		t.Fatal("HARNESS_SHARED_TOPDIRS parsed to zero entries")
	}

	if len(SharedTopdirs) != len(want) {
		t.Fatalf("len(SharedTopdirs) = %d, want %d (parsed from bash: %v): %v", len(SharedTopdirs), len(want), want, SharedTopdirs)
	}
	for i := range want {
		if SharedTopdirs[i] != want[i] {
			t.Errorf("SharedTopdirs[%d] = %q, want %q (bash HARNESS_SHARED_TOPDIRS: %v)", i, SharedTopdirs[i], want[i], want)
		}
	}
}

// TestCommittedGlobs pins the CommittedGlobs entries and their order.
func TestCommittedGlobs(t *testing.T) {
	want := []string{
		"AGENTS.md", "CLAUDE.md", "CLAUDE.local.md", ".claude",
		"lefthook.yml", "lefthook-local.yml", ".lefthook", ".omakase",
		".husky", ".githooks", ".github/copilot-instructions.md",
		".github/copilot/settings.json", ".github/copilot/settings.local.json",
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
