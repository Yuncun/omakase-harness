package block

import (
	"strings"
	"testing"
)

// The committed surface used across the Resolve tests: a root instruction
// file, a nested one, a rules file, a Claude skill, a Copilot .agents skill,
// an agent, and a hooks dir entry.
var committed = []string{
	"CLAUDE.md",
	"core/CLAUDE.md",
	".claude/rules/style.md",
	".claude/skills/deploy/SKILL.md",
	".claude/skills/deploy/scripts/run.sh",
	".agents/skills/od-worktree/SKILL.md",
	".claude/agents/reviewer.md",
	".github/hooks/pre-commit.sh",
}

func TestResolveExactPath(t *testing.T) {
	item, covered, err := Resolve(committed, "CLAUDE.md")
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if item != "CLAUDE.md" || len(covered) != 1 || covered[0] != "CLAUDE.md" {
		t.Errorf("item = %q covered = %v", item, covered)
	}
}

func TestResolveNormalization(t *testing.T) {
	for _, arg := range []string{"./CLAUDE.md", ".claude/skills/deploy/", "./.claude/skills/deploy/"} {
		if _, _, err := Resolve(committed, arg); err != nil {
			t.Errorf("Resolve(%q): %v", arg, err)
		}
	}
}

func TestResolveDirectory(t *testing.T) {
	item, covered, err := Resolve(committed, ".claude/skills/deploy")
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if item != ".claude/skills/deploy" {
		t.Errorf("item = %q", item)
	}
	if len(covered) != 2 {
		t.Errorf("covered = %v, want the 2 deploy files", covered)
	}
}

func TestResolveShorthandSkill(t *testing.T) {
	item, covered, err := Resolve(committed, "od-worktree")
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if item != ".agents/skills/od-worktree" {
		t.Errorf("item = %q", item)
	}
	if len(covered) != 1 || covered[0] != ".agents/skills/od-worktree/SKILL.md" {
		t.Errorf("covered = %v", covered)
	}
}

func TestResolveShorthandAgent(t *testing.T) {
	item, _, err := Resolve(committed, "reviewer")
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if item != ".claude/agents/reviewer.md" {
		t.Errorf("item = %q", item)
	}
}

func TestResolveShorthandAmbiguous(t *testing.T) {
	both := append([]string{".github/skills/deploy/SKILL.md"}, committed...)
	_, _, err := Resolve(both, "deploy")
	if err == nil {
		t.Fatal("want ambiguity error")
	}
	if !strings.Contains(err.Error(), ".claude/skills/deploy") || !strings.Contains(err.Error(), ".github/skills/deploy") {
		t.Errorf("ambiguity error should list both candidates: %v", err)
	}
}

func TestResolveUnknown(t *testing.T) {
	for _, arg := range []string{"nope", "docs/README.md", "", "-", "--yes", "./"} {
		if _, _, err := Resolve(committed, arg); err == nil {
			t.Errorf("Resolve(%q): want error", arg)
		}
	}
}

// A directory argument must not swallow a sibling file sharing the prefix:
// blocking ".claude/skills/dep" when only "deploy" exists is unknown, not a
// prefix match.
func TestResolvePrefixIsNotSubstring(t *testing.T) {
	if _, _, err := Resolve(committed, ".claude/skills/dep"); err == nil {
		t.Error("partial dir name resolved; want error")
	}
}
