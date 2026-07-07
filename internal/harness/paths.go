// Package harness classifies harness-artifact repo paths into their "kind"
// and lists the git pathspecs status.sh scans for a project's own committed
// harness surface. Go twin of bin/lib-harness-paths.sh (kind_of() +
// HARNESS_COMMITTED_GLOBS) — DUPLICATED bash<->Go until Phase 2 retires the
// bash callers; keep in lockstep.
package harness

import "strings"

// SharedTopdirs mirrors bin/lib-harness-paths.sh's HARNESS_SHARED_TOPDIRS
// verbatim (currently just ".github"): the top-level dirs omakase SHARES with
// the project rather than owning outright. An injected path under one of these
// is excluded from git file-by-file in .git/info/exclude — never the whole dir
// — so omakase never hides the project's OWN untracked files there. Dirs NOT
// listed here (.omakase, .claude) are omakase-owned and excluded wholesale.
// Go twin of the bash array — DUPLICATED bash<->Go until Phase 2 retires the
// bash callers; keep in lockstep with bin/lib-harness-paths.sh:72.
var SharedTopdirs = []string{".github"}

// CommittedGlobs mirrors bin/lib-harness-paths.sh's HARNESS_COMMITTED_GLOBS
// verbatim (order matters: it is handed to `git ls-files -- globs...`).
var CommittedGlobs = []string{
	"AGENTS.md", "CLAUDE.md", "CLAUDE.local.md", ".claude",
	"lefthook.yml", "lefthook-local.yml", ".lefthook", ".omakase",
	".husky", ".githooks", ".github/copilot-instructions.md",
	".github/instructions", ".github/skills", ".github/prompts",
	".github/chatmodes", ".github/hooks",
}

// KindOf classifies a harness path by its location — the path IS the
// classification. Verbatim port of the kind_of() case table in
// bin/lib-harness-paths.sh, including case ORDER: specific patterns first
// (mutually disjoint, so their order relative to each other is free), then
// the catch-alls */* (nested path), *.md (remaining root-level .md), *
// (everything else). Valid kinds: rule skill command agent prompt config
// doc gate other.
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
	case bashGlobMatch("lefthook-local.yml", path), bashGlobMatch("lefthook.yml", path), bashGlobMatch(".omakase/gates/*", path):
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

// bashGlobMatch reproduces bash `case` glob semantics for one pattern
// against a full string (anchored match). The only wildcard used in the
// kind_of table is `*`, and unlike path.Match / filepath.Match, bash's `*`
// matches any sequence of characters INCLUDING `/` — ".claude/skills/*"
// matches "a/b/c", not just a single path segment. There are no `?` or
// `[...]` classes in this table, so those are not implemented.
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

// IsMachinery reports whether rel is harness machinery — the paths that keep
// the harness itself running (.omakase/ tree, lefthook wiring, hook dirs,
// .worktreeinclude). Machinery is never a consent item: the TUI and MCP menu
// filter it out, the scriptable toggles refuse it, and init ignores a stale
// enabled=0 ledger row for it (a pre-guard binary could record one).
func IsMachinery(rel string) bool {
	switch {
	case strings.HasPrefix(rel, ".omakase/"),
		rel == "lefthook.yml", rel == "lefthook-local.yml", rel == ".worktreeinclude",
		strings.HasPrefix(rel, ".lefthook/"), strings.HasPrefix(rel, ".husky/"),
		strings.HasPrefix(rel, ".githooks/"):
		return true
	}
	return false
}
