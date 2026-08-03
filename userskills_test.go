package basepayload

import (
	"io/fs"
	"regexp"
	"strings"
	"testing"
)

// TestEmbeddedSkillsComplete pins the embedded skill set: every skill the
// fold installs user-level (#211), each a directory holding a SKILL.md.
// A rename here is a user-visible change — the flat machine-wide skill
// namespace is why every name carries the omakase- prefix (bare init /
// status collide with Claude Code built-ins).
func TestEmbeddedSkillsComplete(t *testing.T) {
	want := map[string]bool{
		"skills/omakase-init/SKILL.md":     false,
		"skills/omakase-status/SKILL.md":   false,
		"skills/omakase-remove/SKILL.md":   false,
		"skills/omakase-author/SKILL.md":   false,
		"skills/omakase-add-gate/SKILL.md": false,
	}
	err := fs.WalkDir(SkillsFS, "skills", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if _, known := want[path]; known {
			want[path] = true
		}
		data, err := fs.ReadFile(SkillsFS, path)
		if err != nil {
			return err
		}
		if len(data) == 0 {
			t.Errorf("embedded %s is empty", path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk embedded skills: %v", err)
	}
	for path, seen := range want {
		if !seen {
			t.Errorf("embedded skills missing %s", path)
		}
	}
}

// Each SKILL.md's frontmatter name must equal its directory name — the
// hosts key invocation and discovery on the directory, and a mismatch
// would ship a skill answering to two different names. And nothing may
// reference the dead plugin machinery: installed copies live in the user's
// home, where ${CLAUDE_PLUGIN_ROOT}, run.sh shims, and repo-relative links
// all dangle.
func TestEmbeddedSkillsSelfConsistent(t *testing.T) {
	entries, err := fs.ReadDir(SkillsFS, "skills")
	if err != nil {
		t.Fatal(err)
	}
	nameRe := regexp.MustCompile(`(?m)^name: (.+)$`)
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		b, err := fs.ReadFile(SkillsFS, "skills/"+e.Name()+"/SKILL.md")
		if err != nil {
			t.Errorf("%s: no SKILL.md: %v", e.Name(), err)
			continue
		}
		s := string(b)
		m := nameRe.FindStringSubmatch(s)
		if m == nil || strings.TrimSpace(m[1]) != e.Name() {
			t.Errorf("%s: frontmatter name %v != directory name", e.Name(), m)
		}
		for _, dead := range []string{"CLAUDE_PLUGIN_ROOT", "run.sh", "bin/init.sh", "bin/remove.sh", "bin/status.sh", "](../"} {
			if strings.Contains(s, dead) {
				t.Errorf("%s: references dead plugin machinery %q", e.Name(), dead)
			}
		}
	}
}
