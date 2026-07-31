// Package ctxlayers implements `omakase context`: the harness seen as the
// layers of instruction text an agent host assembles before your first word,
// rather than as a list of files on disk.
//
// It answers the question `omakase status` structurally cannot. status is an
// inventory — what exists, and who owns it. This verb is about reach: which
// layers are read on every turn, which are only indexed (a skill's
// description is loaded so the agent can decide to open it; the body is not),
// which sit idle until you touch a module or say a trigger phrase, and which
// the running host never reads at all.
//
// The load order is not invented here. Copilot CLI publishes its instruction
// precedence in `copilot --help`, and Claude Code its own; hostsFor and the
// group order below follow those published lists. Where a host is ambiguous
// the entry claims both, because omakase is host-agnostic by design (see
// internal/harness/paths.go) and must not assert a host it cannot observe.
package ctxlayers

import (
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/Yuncun/omakase-harness/internal/state"
)

// Tier is how much of an entry reaches the model, in descending order of
// reach. The zero value is TierAlways so a mis-tiered entry errs toward
// being shown rather than hidden.
type Tier int

const (
	// TierAlways is full text in the context window every turn.
	TierAlways Tier = iota
	// TierIndexed is metadata-only: the name and description are loaded so
	// the agent can decide whether to open it; the body is not.
	TierIndexed
	// TierOnDemand loads when you touch the directory it governs.
	TierOnDemand
	// TierOnTrigger loads when what you asked matches its description.
	TierOnTrigger
	// TierInert is present on disk but unread by the detected host.
	TierInert
)

// Loaded reports whether the tier contributes text to every turn. Used by the
// renderer to split the page at the "not loaded" rule.
func (t Tier) Loaded() bool { return t == TierAlways || t == TierIndexed }

// Entry is one row: a single file, or an aggregate of Count files that share
// a tier and a purpose (25 nested CLAUDE.md, 22 skill bodies).
type Entry struct {
	Tier    Tier
	Group   string   // GLOBAL, PROJECT, INJECTED, SKILLS, MODULES
	Display string   // what the row names — a repo-relative path or a phrase
	Bytes   int      // total bytes across Count files
	Count   int      // 1 for a single file; N for an aggregate
	Prov    string   // committed | injected | yours | global | mixed
	Hosts   []string // hosts that read it
	Excerpt string   // a representative sentence, empty when not applicable
	Points  string   // include target, when the file is only a pointer
	Note    string   // a warning worth interrupting for
}

// Tokens is the rough token cost of the entry. Four bytes per token is the
// usual English-prose approximation; it is an estimate and the renderer
// labels it as one. Only the host knows real numbers (Copilot CLI's
// /context, Claude Code's /context) — this verb deliberately does not
// pretend otherwise.
func (e Entry) Tokens() int { return e.Bytes / 4 }

// Scan builds the ordered layer list for repo, reading the filesystem but
// writing nothing. home is $HOME; host is the detected host key ("copilot",
// "claude") or "" when unknown, in which case nothing is reported inert.
func Scan(repo *state.Repo, home, host string) []Entry {
	var out []Entry
	injected := injectedSet(repo)

	out = append(out, globalLayer(home, host)...)
	out = append(out, projectLayer(repo, host)...)
	out = append(out, injectedRuleLayer(repo, injected, host)...)
	out = append(out, skillLayer(repo, injected)...)
	out = append(out, moduleLayer(repo)...)

	sort.SliceStable(out, func(i, j int) bool { return out[i].Tier < out[j].Tier })
	return out
}

// injectedSet is the set of repo-relative paths omakase placed, used to label
// provenance. An install-free repo yields an empty set and every path falls
// through to committed/yours.
func injectedSet(repo *state.Repo) map[string]bool {
	set := map[string]bool{}
	for _, r := range state.ReadPlaced(filepath.Join(repo.OMK, "placed.tsv")) {
		if r.Enabled != "0" {
			set[r.Rel] = true
		}
	}
	return set
}

// provenanceOf labels where a repo-relative path came from: injected by
// omakase, committed by the project, or the user's own untracked file.
func provenanceOf(rel string, injected, tracked map[string]bool) string {
	switch {
	case injected[rel]:
		return "injected"
	case tracked[rel]:
		return "committed"
	default:
		return "yours"
	}
}

// trackedSet is the set of git-tracked paths among globs.
func trackedSet(root string, globs []string) map[string]bool {
	set := map[string]bool{}
	for _, p := range state.TrackedUnder(root, globs) {
		set[p] = true
	}
	return set
}

// globalLayer is the personal config that applies to every repo you open —
// the widest-reaching and least visible layer, which is why it leads.
//
// The per-host global files are frequently the same file behind two names: a
// symlink from ~/.claude/CLAUDE.md to ~/.copilot/copilot-instructions.md is a
// common way to keep one set of personal instructions for both agents.
// Listing it twice would double-count its tokens and, worse, report one copy
// as inert when the bytes are in fact loaded. Entries are therefore grouped
// by resolved path.
func globalLayer(home, host string) []Entry {
	if home == "" {
		return nil
	}
	cands := []struct{ rel, hostKey string }{
		{".copilot/copilot-instructions.md", "copilot"},
		{".claude/CLAUDE.md", "claude"},
	}

	type group struct {
		names []string
		hosts []string
		abs   string
	}
	var order []string
	groups := map[string]*group{}
	for _, c := range cands {
		abs := filepath.Join(home, c.rel)
		if fileBytes(abs) == 0 {
			continue
		}
		key := realPath(abs)
		g, seen := groups[key]
		if !seen {
			g = &group{abs: abs}
			groups[key] = g
			order = append(order, key)
		}
		g.names = append(g.names, "~/"+c.rel)
		g.hosts = append(g.hosts, c.hostKey)
	}

	var out []Entry
	for _, key := range order {
		g := groups[key]
		note := "applies to every repo you open, not just this one"
		if len(g.names) > 1 {
			note = "one file, both hosts: " + strings.Join(g.names[1:], ", ") +
				" resolves here — counted once"
		}
		e := Entry{
			Group: "GLOBAL", Display: g.names[0], Bytes: fileBytes(g.abs), Count: 1,
			Prov: "global", Hosts: g.hosts, Excerpt: excerptOf(g.abs),
			Points: IncludeTargetOf(g.abs), Note: note,
		}
		e.Tier = tierFor(e.Hosts, host, TierAlways)
		out = append(out, e)
	}

	// $HOME/.copilot/instructions/**.instructions.md is a documented source
	// too; it is usually small, so it folds into one aggregate row.
	if n, c := dirBytes(filepath.Join(home, ".copilot", "instructions")); c > 0 {
		e := Entry{
			Group: "GLOBAL", Display: "~/.copilot/instructions/", Bytes: n, Count: c,
			Prov: "global", Hosts: []string{"copilot"},
		}
		e.Tier = tierFor(e.Hosts, host, TierAlways)
		out = append(out, e)
	}
	return out
}

// realPath resolves symlinks so two names for one file group together. It
// falls back to the input when the path cannot be resolved, which at worst
// restores the old double-counted behaviour rather than dropping the entry.
func realPath(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return p
}

// projectLayer is the repo's own root-level instruction files: the layer a
// contributor expects to be steered by, and the one most likely to duplicate
// itself across host-specific filenames.
func projectLayer(repo *state.Repo, host string) []Entry {
	rels := []string{"CLAUDE.md", "AGENTS.md", "GEMINI.md", ".github/copilot-instructions.md"}
	tracked := trackedSet(repo.Root, rels)
	injected := injectedSet(repo)
	var out []Entry
	for _, rel := range rels {
		abs := filepath.Join(repo.Root, rel)
		n := fileBytes(abs)
		if n == 0 {
			continue
		}
		e := Entry{
			Group: "PROJECT", Display: rel, Bytes: n, Count: 1,
			Prov: provenanceOf(rel, injected, tracked), Hosts: hostsFor(rel),
			Excerpt: excerptOf(abs), Points: IncludeTargetOf(abs),
		}
		e.Tier = tierFor(e.Hosts, host, TierAlways)
		out = append(out, e)
	}
	return annotateOverlap(repo, out)
}

// injectedRuleLayer is the per-host rule files: .github/instructions/* for
// Copilot, .claude/rules/* for Claude Code. These are the files a harness
// most often places, and the ones most likely to be inert on the host you
// happen to be running.
func injectedRuleLayer(repo *state.Repo, injected map[string]bool, host string) []Entry {
	tracked := trackedSet(repo.Root, []string{".github/instructions", ".claude"})
	var out []Entry
	for _, dir := range []string{".github/instructions", ".claude/rules"} {
		for _, rel := range mdFilesUnder(repo.Root, dir) {
			abs := filepath.Join(repo.Root, rel)
			e := Entry{
				Group: "INJECTED", Display: rel, Bytes: fileBytes(abs), Count: 1,
				Prov: provenanceOf(rel, injected, tracked), Hosts: hostsFor(rel),
				Excerpt: excerptOf(abs), Points: IncludeTargetOf(abs),
			}
			e.Tier = tierFor(e.Hosts, host, TierAlways)
			out = append(out, e)
		}
	}
	// .claude/settings.json carries no instruction text but is still placed,
	// and being unread by a non-Claude host is exactly what people miss.
	rel := ".claude/settings.json"
	if n := fileBytes(filepath.Join(repo.Root, rel)); n > 0 {
		e := Entry{
			Group: "INJECTED", Display: rel, Bytes: n, Count: 1,
			Prov: provenanceOf(rel, injected, tracked), Hosts: hostsFor(rel),
			Tier: TierOnDemand, // settings are read as config, not as prose
		}
		if tierFor(e.Hosts, host, TierOnDemand) == TierInert {
			e.Tier = TierInert
		}
		out = append(out, e)
	}
	return out
}

// skillLayer splits skills into the two rows that matter: the always-loaded
// index of descriptions, and the bodies that stay on disk until a trigger
// matches. Collapsing them into one "27 skills" line is what hides the fact
// that a verbose description is a permanent tax while a long body is free.
func skillLayer(repo *state.Repo, injected map[string]bool) []Entry {
	roots := []string{".agents/skills", ".github/skills", ".claude/skills"}
	var descBytes, bodyBytes, n, nInjected int
	for _, root := range roots {
		for _, dir := range skillDirs(filepath.Join(repo.Root, root)) {
			sk := filepath.Join(dir, "SKILL.md")
			b := fileBytes(sk)
			if b == 0 {
				continue
			}
			n++
			d := len(descriptionOf(sk))
			descBytes += d
			if b > d {
				bodyBytes += b - d
			}
			if rel, err := filepath.Rel(repo.Root, sk); err == nil && injected[rel] {
				nInjected++
			}
		}
	}
	if n == 0 {
		return nil
	}
	prov := "committed"
	if nInjected == n {
		prov = "injected"
	} else if nInjected > 0 {
		prov = "mixed"
	}
	return []Entry{
		{
			Tier: TierIndexed, Group: "SKILLS",
			Display: plural(n, "skill description", "skill descriptions"),
			Bytes:   descBytes, Count: n, Prov: prov,
			Hosts: []string{"claude", "copilot"},
			Note:  "descriptions load every turn so the agent can choose; bodies do not",
		},
		{
			Tier: TierOnTrigger, Group: "SKILLS",
			Display: plural(n, "skill body", "skill bodies"),
			Bytes:   bodyBytes, Count: n, Prov: prov,
			Hosts: []string{"claude", "copilot"},
			Note:  "loads when what you ask matches a description",
		},
	}
}

// moduleLayer is the nested per-directory instruction files. They are the
// largest body of text in most repos and cost nothing until you open a file
// beneath them — worth saying out loud, because their file count makes them
// look expensive in an inventory.
func moduleLayer(repo *state.Repo) []Entry {
	rels := state.TrackedUnder(repo.Root, []string{"*/CLAUDE.md", "*/AGENTS.md"})
	if len(rels) == 0 {
		return nil
	}
	total := 0
	for _, rel := range rels {
		total += fileBytes(filepath.Join(repo.Root, rel))
	}
	return []Entry{{
		Tier: TierOnDemand, Group: "MODULES",
		Display: plural(len(rels), "nested instruction file", "nested instruction files"),
		Bytes:   total, Count: len(rels), Prov: "committed",
		Hosts: []string{"claude", "copilot"},
		Note:  "loads when you touch the module it sits in",
	}}
}

// tierFor demotes an entry to TierInert when the detected host is not among
// the hosts that read it. An unknown host never demotes: asserting a file is
// inert when we cannot observe the host would be worse than staying quiet.
func tierFor(hosts []string, host string, want Tier) Tier {
	if host == "" {
		return want
	}
	for _, h := range hosts {
		if h == host {
			return want
		}
	}
	return TierInert
}

// hostsFor maps a repo-relative path to the hosts documented to read it.
// Copilot CLI's list comes from `copilot --help`; Claude Code reads CLAUDE.md
// and its own .claude tree. Anything unrecognized claims both hosts rather
// than risk a false "inert".
func hostsFor(rel string) []string {
	switch {
	case rel == "CLAUDE.md":
		return []string{"claude", "copilot"} // both — per Copilot's published list
	case rel == "AGENTS.md", rel == "GEMINI.md":
		return []string{"copilot"}
	case rel == ".github/copilot-instructions.md",
		strings.HasPrefix(rel, ".github/instructions/"),
		strings.HasPrefix(rel, ".github/skills/"),
		strings.HasPrefix(rel, ".agents/skills/"):
		return []string{"copilot"}
	case strings.HasPrefix(rel, ".claude/skills/"):
		return []string{"claude", "copilot"}
	case strings.HasPrefix(rel, ".claude/"):
		return []string{"claude"}
	}
	return []string{"claude", "copilot"}
}

// DetectHost returns the running agent host key from the environment, or ""
// when it cannot be determined. Copilot CLI exports COPILOT_CLI; Claude Code
// exports CLAUDECODE.
func DetectHost(getenv func(string) string) string {
	if getenv("COPILOT_CLI") != "" || getenv("COPILOT_AGENT_SESSION_ID") != "" {
		return "copilot"
	}
	if getenv("CLAUDECODE") != "" || getenv("CLAUDE_CODE") != "" {
		return "claude"
	}
	return ""
}

// annotateOverlap flags root instruction files that substantially repeat each
// other. Two host-specific copies of the same guidance both load in full, and
// nothing else in the toolchain reports that.
func annotateOverlap(repo *state.Repo, rows []Entry) []Entry {
	if len(rows) < 2 {
		return rows
	}
	words := make([]map[string]bool, len(rows))
	for i, r := range rows {
		words[i] = wordSet(filepath.Join(repo.Root, r.Display))
	}
	for i := range rows {
		if rows[i].Note != "" {
			continue
		}
		for j := range rows {
			if i == j || len(words[i]) == 0 || len(words[j]) == 0 {
				continue
			}
			if pct := overlapPct(words[i], words[j]); pct >= overlapWarnPct {
				rows[i].Note = "~" + strconv.Itoa(pct) + "% vocabulary overlap with " +
					rows[j].Display + " — both load in full"
				break
			}
		}
	}
	return rows
}

// overlapWarnPct is the share of the smaller file's vocabulary that must be
// shared before the duplication is worth interrupting for. Two unrelated
// documents about the same codebase share a surprising amount of vocabulary,
// so the bar sits well above incidental overlap.
const overlapWarnPct = 30

// overlapPct is the share of the smaller vocabulary that both sets contain.
func overlapPct(a, b map[string]bool) int {
	small, large := a, b
	if len(b) < len(a) {
		small, large = b, a
	}
	if len(small) == 0 {
		return 0
	}
	n := 0
	for w := range small {
		if large[w] {
			n++
		}
	}
	return n * 100 / len(small)
}

// TotalsOf sums the loaded and idle byte counts for the footer.
func TotalsOf(entries []Entry) (loaded, idle int) {
	for _, e := range entries {
		switch {
		case e.Tier.Loaded():
			loaded += e.Bytes
		case e.Tier != TierInert:
			idle += e.Bytes
		}
	}
	return loaded, idle
}

// fileBytes is the size of path, or 0 when absent, unreadable, or a dir.
func fileBytes(path string) int {
	fi, err := os.Stat(path)
	if err != nil || fi.IsDir() {
		return 0
	}
	return int(fi.Size())
}
