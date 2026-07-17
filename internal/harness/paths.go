// Package harness classifies harness-artifact repo paths into their "kind"
// and lists the git pathspecs the status verb scans for a project's own
// committed harness surface.
package harness

import "strings"

// SharedTopdirs lists the top-level dirs omakase shares with the project
// rather than owning outright. Injected paths under these are excluded
// file-by-file in .git/info/exclude, never as a whole dir, so omakase does
// not hide the project's own untracked files; unlisted dirs (.omakase,
// .claude) are omakase-owned and excluded wholesale.
var SharedTopdirs = []string{".github"}

// CommittedGlobs lists the pathspecs for a project's own committed harness
// surface (order matters: it is handed to `git ls-files -- globs...`).
var CommittedGlobs = []string{
	"AGENTS.md", "CLAUDE.md", "CLAUDE.local.md", ".claude",
	"omakase.manifest", "lefthook.yml", "lefthook-local.yml", ".lefthook", ".omakase",
	".husky", ".githooks", ".github/copilot-instructions.md",
	".github/instructions", ".github/skills", ".github/prompts",
	".github/chatmodes", ".github/hooks",
}

// KindOf classifies a harness path by its location into one of: rule skill
// command agent prompt config doc gate other. Specific patterns are tested
// first, then the catch-alls: */* (nested path), *.md (root-level doc),
// * (other).
func KindOf(path string) string {
	switch {
	// --- Claude Code ---
	case bashGlobMatch(".claude/rules/*", path):
		return "rule"
	case bashGlobMatch(".claude/skills/*", path):
		return "skill"
	case bashGlobMatch(".claude/commands/*", path):
		return "command"
	case bashGlobMatch(".claude/agents/*", path):
		return "agent"
	case bashGlobMatch(".claude/hooks/*", path):
		return "gate"
	case bashGlobMatch(".claude/settings.json", path), bashGlobMatch(".claude/settings.*.json", path):
		return "config"
	// --- GitHub Copilot ---
	case bashGlobMatch(".github/skills/*", path):
		return "skill"
	case bashGlobMatch(".github/instructions/*", path):
		return "rule"
	case bashGlobMatch(".github/prompts/*", path), bashGlobMatch(".github/chatmodes/*", path):
		return "prompt"
	case bashGlobMatch(".github/hooks/*", path):
		return "gate"
	case bashGlobMatch(".github/copilot-instructions.md", path):
		return "doc"
	// --- host-agnostic ---
	case bashGlobMatch("omakase.manifest", path), bashGlobMatch(".omakase/gates/*", path):
		return "gate"
	case bashGlobMatch(".husky/*", path), bashGlobMatch(".githooks/*", path):
		return "gate"
	case bashGlobMatch("AGENTS.md", path), bashGlobMatch("CLAUDE.md", path):
		return "doc"
	case bashGlobMatch("*/*", path):
		return "other" // nested, none of the above
	case bashGlobMatch("*.md", path):
		return "doc" // remaining root-level *.md
	default:
		return "other"
	}
}

// bashGlobMatch matches s against pattern with bash `case` glob semantics,
// anchored: unlike path.Match, `*` matches any sequence including `/`, so
// ".claude/skills/*" matches "a/b/c". `?` and `[...]` are not implemented;
// the KindOf table uses only `*`.
func bashGlobMatch(pattern, s string) bool {
	if !strings.Contains(pattern, "*") {
		return pattern == s
	}

	parts := strings.Split(pattern, "*")

	if !strings.HasPrefix(s, parts[0]) {
		return false
	}
	s = s[len(parts[0]):]

	for _, mid := range parts[1 : len(parts)-1] {
		idx := strings.Index(s, mid)
		if idx < 0 {
			return false
		}
		s = s[idx+len(mid):]
	}

	return strings.HasSuffix(s, parts[len(parts)-1])
}

// IsMachinery reports whether rel is harness machinery: the paths that keep
// the harness itself running (.omakase/ tree, the omakase.manifest gate
// declaration, hook dirs, .worktreeinclude). Machinery is never a consent
// item — the TUI and MCP menu filter it out, the scriptable toggles refuse it,
// and init ignores a stale enabled=0 ledger row for it. The lefthook wiring
// names stay listed so a re-init that migrates a pre-gate-module install still
// sweeps a placed lefthook-local.yml row as machinery.
func IsMachinery(rel string) bool {
	switch {
	case strings.HasPrefix(rel, ".omakase/"),
		rel == "omakase.manifest",
		rel == "lefthook.yml", rel == "lefthook-local.yml", rel == ".worktreeinclude",
		strings.HasPrefix(rel, ".lefthook/"), strings.HasPrefix(rel, ".husky/"),
		strings.HasPrefix(rel, ".githooks/"):
		return true
	}
	return false
}
