package ctxlayers

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Yuncun/omakase-harness/internal/state"
)

// ---------------------------------------------------------------- fixtures

// newGitRepo builds a real temp git repo with an identity that never blocks a
// commit on signing.
func newGitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	runGitT(t, dir, "init", "-q")
	runGitT(t, dir, "config", "user.email", "t@t")
	runGitT(t, dir, "config", "user.name", "t")
	runGitT(t, dir, "config", "commit.gpgsign", "false")
	return dir
}

func runGitT(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "gc.auto=0", "-c", "maintenance.auto=false"}, args...)...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

func writeFile(t *testing.T, dir, rel, body string) string {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// ------------------------------------------------------------ descriptionOf

// A skill body that contains a "description:" line must not be counted as
// part of the frontmatter description. Getting this wrong reports a skill
// index an order of magnitude larger than it is.
func TestDescriptionOfStopsAtFrontmatter(t *testing.T) {
	dir := t.TempDir()
	p := writeFile(t, dir, "SKILL.md", `---
name: demo
description: Short and true.
---

# Demo

description: this line is body prose and must be ignored
`+strings.Repeat("filler filler filler\n", 200))

	got := descriptionOf(p)
	if got != "Short and true." {
		t.Fatalf("description = %q, want %q", got, "Short and true.")
	}
}

func TestDescriptionOfFoldsContinuationLines(t *testing.T) {
	dir := t.TempDir()
	p := writeFile(t, dir, "SKILL.md", `---
description: first part
  second part
name: demo
---
body
`)
	if got, want := descriptionOf(p), "first part second part"; got != want {
		t.Fatalf("description = %q, want %q", got, want)
	}
}

func TestDescriptionOfNoFrontmatter(t *testing.T) {
	dir := t.TempDir()
	p := writeFile(t, dir, "SKILL.md", "# Just a heading\n\ndescription: nope\n")
	if got := descriptionOf(p); got != "" {
		t.Fatalf("description = %q, want empty", got)
	}
}

// -------------------------------------------------------------- excerptOf

func TestExcerptOfSkipsStructure(t *testing.T) {
	dir := t.TempDir()
	p := writeFile(t, dir, "r.md", `---
applyTo: "**"
---
# A Heading

- a bullet
> a quote

Real prose starts here and should be quoted.
`)
	got := excerptOf(p)
	if !strings.HasPrefix(got, "Real prose starts here") {
		t.Fatalf("excerpt = %q, want it to start with the prose line", got)
	}
}

func TestExcerptOfIgnoresCodeFences(t *testing.T) {
	dir := t.TempDir()
	p := writeFile(t, dir, "r.md", "```bash\nnot prose at all\n```\n\nThe actual sentence.\n")
	if got := excerptOf(p); got != "The actual sentence." {
		t.Fatalf("excerpt = %q", got)
	}
}

func TestExcerptOfEmptyForPointerFile(t *testing.T) {
	dir := t.TempDir()
	p := writeFile(t, dir, "r.md", "---\npaths:\n  - \"**\"\n---\n\n@../../.github/instructions/x.instructions.md\n")
	if got := excerptOf(p); got != "" {
		t.Fatalf("excerpt = %q, want empty so the pointer is not quoted as prose", got)
	}
}

// ---------------------------------------------------------- includeTargetOf

// The pointer files a harness places carry frontmatter above the @-include;
// skipping it is what makes them recognizable as pointers at all.
func TestIncludeTargetOfSkipsFrontmatter(t *testing.T) {
	dir := t.TempDir()
	p := writeFile(t, dir, "r.md", "---\npaths:\n  - \"**\"\n---\n\n@../../.github/instructions/pr.instructions.md\n")
	if got, want := includeTargetOf(p), ".github/instructions/pr.instructions.md"; got != want {
		t.Fatalf("target = %q, want %q", got, want)
	}
}

func TestIncludeTargetOfRejectsMixedContent(t *testing.T) {
	dir := t.TempDir()
	p := writeFile(t, dir, "r.md", "@../other.md\n\nBut also real prose.\n")
	if got := includeTargetOf(p); got != "" {
		t.Fatalf("target = %q, want empty when the file has its own content", got)
	}
}

func TestIncludeTargetOfRejectsMultipleIncludes(t *testing.T) {
	dir := t.TempDir()
	p := writeFile(t, dir, "r.md", "@../a.md\n@../b.md\n")
	if got := includeTargetOf(p); got != "" {
		t.Fatalf("target = %q, want empty for a multi-include file", got)
	}
}

// ---------------------------------------------------------------- hostsFor

func TestHostsFor(t *testing.T) {
	cases := []struct {
		rel  string
		want []string
	}{
		// Copilot CLI's published list includes CLAUDE.md, so it is not a
		// Claude-only file even though the name suggests otherwise.
		{"CLAUDE.md", []string{"claude", "copilot"}},
		{"AGENTS.md", []string{"copilot"}},
		{".github/copilot-instructions.md", []string{"copilot"}},
		{".github/instructions/x.instructions.md", []string{"copilot"}},
		{".claude/rules/x.md", []string{"claude"}},
		{".claude/settings.json", []string{"claude"}},
		{".claude/skills/x/SKILL.md", []string{"claude", "copilot"}},
		{"something/unknown.md", []string{"claude", "copilot"}},
	}
	for _, c := range cases {
		got := hostsFor(c.rel)
		if strings.Join(got, ",") != strings.Join(c.want, ",") {
			t.Errorf("hostsFor(%q) = %v, want %v", c.rel, got, c.want)
		}
	}
}

// ----------------------------------------------------------------- tierFor

// Claiming a file is inert requires knowing the host. When detection fails,
// staying quiet is the only honest option.
func TestTierForUnknownHostNeverDemotes(t *testing.T) {
	if got := tierFor([]string{"claude"}, "", TierAlways); got != TierAlways {
		t.Fatalf("tier = %v, want TierAlways when the host is unknown", got)
	}
}

func TestTierForDemotesUnreadFile(t *testing.T) {
	if got := tierFor([]string{"claude"}, "copilot", TierAlways); got != TierInert {
		t.Fatalf("tier = %v, want TierInert", got)
	}
	if got := tierFor([]string{"claude", "copilot"}, "copilot", TierAlways); got != TierAlways {
		t.Fatalf("tier = %v, want TierAlways", got)
	}
}

// -------------------------------------------------------------- DetectHost

func TestDetectHost(t *testing.T) {
	env := func(m map[string]string) func(string) string {
		return func(k string) string { return m[k] }
	}
	if got := DetectHost(env(map[string]string{"COPILOT_CLI": "1"})); got != "copilot" {
		t.Errorf("got %q, want copilot", got)
	}
	if got := DetectHost(env(map[string]string{"CLAUDECODE": "1"})); got != "claude" {
		t.Errorf("got %q, want claude", got)
	}
	if got := DetectHost(env(map[string]string{})); got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

// ------------------------------------------------------------- text layout

func TestElideLeftKeepsFilename(t *testing.T) {
	got := elideLeft(".github/instructions/pr-discipline.instructions.md", 30)
	if runeLen(got) > 30 {
		t.Fatalf("elided to %d runes, want <= 30", runeLen(got))
	}
	if !strings.HasSuffix(got, "pr-discipline.instructions.md") {
		t.Fatalf("elided = %q, want the filename preserved", got)
	}
	if short := elideLeft("a.md", 30); short != "a.md" {
		t.Fatalf("short path was altered: %q", short)
	}
}

// --------------------------------------------------------------- overlap

func TestOverlapPct(t *testing.T) {
	a := map[string]bool{"alpha": true, "beta": true, "gamma": true, "delta": true}
	b := map[string]bool{"alpha": true, "beta": true, "zeta": true, "eta": true}
	if got := overlapPct(a, b); got != 50 {
		t.Fatalf("overlap = %d, want 50", got)
	}
	if got := overlapPct(a, map[string]bool{}); got != 0 {
		t.Fatalf("overlap with empty = %d, want 0", got)
	}
}

// ------------------------------------------------------------------- Scan

func TestScanTiersAndCounts(t *testing.T) {
	root := newGitRepo(t)
	writeFile(t, root, "CLAUDE.md", "# Root\n\nThe project rule.\n")
	writeFile(t, root, ".github/instructions/pr.instructions.md", "---\napplyTo: \"**\"\n---\n\nPrefer one PR.\n")
	writeFile(t, root, "core/auth/CLAUDE.md", "# Auth\n\nNested guidance.\n")
	writeFile(t, root, ".agents/skills/demo/SKILL.md", "---\nname: demo\ndescription: Does a demo thing.\n---\n\nBody body body.\n")
	runGitT(t, root, "add", "-A")
	runGitT(t, root, "commit", "-qm", "init")

	repo, err := state.Discover(root)
	if err != nil {
		t.Fatal(err)
	}
	entries := Scan(repo, "", "copilot")

	byDisplay := map[string]Entry{}
	for _, e := range entries {
		byDisplay[e.Display] = e
	}

	if e, ok := byDisplay["CLAUDE.md"]; !ok || e.Tier != TierAlways {
		t.Fatalf("CLAUDE.md tier = %v (present=%v), want TierAlways", e.Tier, ok)
	}
	if e, ok := byDisplay["CLAUDE.md"]; ok && e.Prov != "committed" {
		t.Errorf("CLAUDE.md prov = %q, want committed", e.Prov)
	}

	// The nested file is aggregated and must not be loaded up front.
	nested, ok := byDisplay["1 nested instruction file"]
	if !ok {
		t.Fatalf("no nested-instruction aggregate in %v", displays(entries))
	}
	if nested.Tier != TierOnDemand {
		t.Errorf("nested tier = %v, want TierOnDemand", nested.Tier)
	}

	// Skills split into an always-loaded index and an idle body.
	idx, ok := byDisplay["1 skill description"]
	if !ok || idx.Tier != TierIndexed {
		t.Fatalf("skill index tier = %v (present=%v), want TierIndexed", idx.Tier, ok)
	}
	body, ok := byDisplay["1 skill body"]
	if !ok || body.Tier != TierOnTrigger {
		t.Fatalf("skill body tier = %v (present=%v), want TierOnTrigger", body.Tier, ok)
	}
	// The description is a small fraction of the file; the body carries the
	// rest. If these were equal the split would be measuring nothing.
	if idx.Bytes == 0 || body.Bytes == 0 || idx.Bytes >= body.Bytes {
		t.Errorf("index %d bytes vs body %d bytes: want a small index and a larger body", idx.Bytes, body.Bytes)
	}
}

// A repo with no instruction files anywhere must produce no rows, so the verb
// can say "nothing steers an agent here" instead of drawing an empty box.
func TestScanEmptyRepo(t *testing.T) {
	root := newGitRepo(t)
	repo, err := state.Discover(root)
	if err != nil {
		t.Fatal(err)
	}
	if got := Scan(repo, "", "copilot"); len(got) != 0 {
		t.Fatalf("Scan returned %d entries for an empty repo: %v", len(got), displays(got))
	}
}

func TestTotalsOfExcludesInert(t *testing.T) {
	entries := []Entry{
		{Tier: TierAlways, Bytes: 400},
		{Tier: TierIndexed, Bytes: 200},
		{Tier: TierOnTrigger, Bytes: 800},
		{Tier: TierInert, Bytes: 9999},
	}
	loaded, idle := TotalsOf(entries)
	if loaded != 600 {
		t.Errorf("loaded = %d, want 600", loaded)
	}
	if idle != 800 {
		t.Errorf("idle = %d, want 800 (inert is neither loaded nor reachable)", idle)
	}
}

func displays(entries []Entry) []string {
	var out []string
	for _, e := range entries {
		out = append(out, e.Display)
	}
	return out
}

// ------------------------------------------------------------ scope parsing

// A rule scoped by frontmatter globs loads only when a matching file is
// touched. Reporting it as always-loaded overstates the per-turn cost by the
// whole file — on a rule-heavy harness, most of the page.
func TestScopeGlobsOfClaudePaths(t *testing.T) {
	dir := t.TempDir()
	p := writeFile(t, dir, "r.md", "---\npaths:\n  - 'packages/data/**'\n  - \"docs/adr/**/*.md\"\n---\n\nBody.\n")
	got := scopeGlobsOf(p)
	if len(got) != 2 || got[0] != "packages/data/**" || got[1] != "docs/adr/**/*.md" {
		t.Errorf("scopeGlobsOf = %v", got)
	}
}

func TestScopeGlobsOfCopilotApplyTo(t *testing.T) {
	dir := t.TempDir()
	p := writeFile(t, dir, "r.instructions.md", "---\napplyTo: \"src/**, lib/**\"\n---\n\nBody.\n")
	got := scopeGlobsOf(p)
	if len(got) != 2 || got[0] != "src/**" || got[1] != "lib/**" {
		t.Errorf("scopeGlobsOf = %v", got)
	}
}

// A match-everything glob is not a scope, and a file without frontmatter has
// none — both must read as unscoped (always loaded).
func TestScopeGlobsOfUnscoped(t *testing.T) {
	dir := t.TempDir()
	if got := scopeGlobsOf(writeFile(t, dir, "a.md", "---\napplyTo: \"**\"\n---\nBody.\n")); got != nil {
		t.Errorf("match-everything glob read as a scope: %v", got)
	}
	if got := scopeGlobsOf(writeFile(t, dir, "b.md", "Plain body, no frontmatter.\n")); got != nil {
		t.Errorf("frontmatter-free file read as scoped: %v", got)
	}
}

// The scan itself must tier a scoped rule ON DEMAND and an unscoped one
// always-loaded.
func TestScanTiersScopedRules(t *testing.T) {
	dir := newGitRepo(t)
	writeFile(t, dir, ".claude/rules/scoped.md", "---\npaths:\n  - 'pkg/**'\n---\n\nScoped body.\n")
	writeFile(t, dir, ".claude/rules/always.md", "Unscoped body.\n")
	repo, err := state.Discover(dir)
	if err != nil {
		t.Fatal(err)
	}
	tiers := map[string]Tier{}
	for _, e := range Scan(repo, "", "claude") {
		tiers[e.Display] = e.Tier
	}
	if tiers[".claude/rules/scoped.md"] != TierOnDemand {
		t.Errorf("scoped rule tier = %v, want ON DEMAND", tiers[".claude/rules/scoped.md"])
	}
	if tiers[".claude/rules/always.md"] != TierAlways {
		t.Errorf("unscoped rule tier = %v, want LOADED", tiers[".claude/rules/always.md"])
	}
}

// ------------------------------------------------------------ parent layer

// Claude Code loads every CLAUDE.md between the working directory and $HOME;
// a file two directories above the repo steers every session inside it and
// must appear on the page. Copilot does not walk up, so under a copilot host
// the same file is inert.
func TestParentLayerWalksUpToHome(t *testing.T) {
	home := t.TempDir()
	writeFile(t, home, "projects/CLAUDE.md", "Parent steering.\n")
	writeFile(t, home, "CLAUDE.md", "Home-level steering.\n")
	dir := filepath.Join(home, "projects", "repo")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	runGitT(t, dir, "init", "-q")
	repo, err := state.Discover(dir)
	if err != nil {
		t.Fatal(err)
	}
	var got []Entry
	for _, e := range Scan(repo, home, "claude") {
		if e.Group == "PARENT" {
			got = append(got, e)
		}
	}
	if len(got) != 2 {
		t.Fatalf("parent entries = %d, want 2 (%v)", len(got), got)
	}
	if got[0].Display != "~/projects/CLAUDE.md" || got[0].Tier != TierAlways {
		t.Errorf("nearest parent = %+v", got[0])
	}
	if got[1].Display != "~/CLAUDE.md" {
		t.Errorf("home-level parent = %+v", got[1])
	}
	for _, e := range Scan(repo, home, "copilot") {
		if e.Group == "PARENT" && e.Tier != TierInert {
			t.Errorf("parent CLAUDE.md not inert under copilot: %+v", e)
		}
	}
}

// ------------------------------------------------------------ symlink dedup

// CLAUDE.md as a symlink of AGENTS.md is one file with two names: one entry,
// bytes counted once, hosts unioned, and no self-overlap warning.
func TestProjectLayerDedupsSymlink(t *testing.T) {
	dir := newGitRepo(t)
	writeFile(t, dir, "AGENTS.md", "Shared steering for both hosts, written once.\n")
	if err := os.Symlink("AGENTS.md", filepath.Join(dir, "CLAUDE.md")); err != nil {
		t.Skip("symlinks unavailable:", err)
	}
	repo, err := state.Discover(dir)
	if err != nil {
		t.Fatal(err)
	}
	var rows []Entry
	for _, e := range Scan(repo, "", "") {
		if e.Group == "PROJECT" {
			rows = append(rows, e)
		}
	}
	if len(rows) != 1 {
		t.Fatalf("project rows = %d, want 1 (%v)", len(rows), rows)
	}
	e := rows[0]
	if !strings.Contains(e.Note, "symlink") || !strings.Contains(e.Note, "counted once") {
		t.Errorf("note = %q, want the symlink note", e.Note)
	}
	if strings.Contains(e.Note, "overlap") {
		t.Errorf("self-overlap reported for a symlink pair: %q", e.Note)
	}
	if !contains(e.Hosts, "claude") || !contains(e.Hosts, "copilot") {
		t.Errorf("hosts = %v, want both", e.Hosts)
	}
}

// ------------------------------------------------------------ settings.json

// Settings are config, not steering — they never enter the context window as
// prose and must not appear as a token-costed layer.
func TestScanOmitsSettingsJSON(t *testing.T) {
	dir := newGitRepo(t)
	writeFile(t, dir, ".claude/settings.json", `{"hooks":{}}`)
	repo, err := state.Discover(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range Scan(repo, "", "") {
		if strings.Contains(e.Display, "settings.json") {
			t.Errorf("settings.json listed as a layer: %+v", e)
		}
	}
}

// ------------------------------------------------------------- render page

// renderFixture is a stable entry set exercising every section: two loaded
// rows (one with a note), an indexed aggregate, a scoped rule, a trigger
// aggregate, and an inert file.
func renderFixture() []Entry {
	return []Entry{
		{Tier: TierAlways, Display: "CLAUDE.md", Bytes: 4000, Count: 1,
			Hosts: []string{"claude", "copilot"}, Excerpt: "Ship no change without tests."},
		{Tier: TierAlways, Display: "~/.claude/CLAUDE.md", Bytes: 2000, Count: 1,
			Hosts: []string{"claude"}, Excerpt: "Personal doctrine.",
			Note: "applies to every repo you open, not just this one"},
		{Tier: TierIndexed, Display: "2 skill descriptions", Bytes: 800, Count: 2,
			Hosts: []string{"claude", "copilot"}, Note: "(the menu the agent picks from; bodies wait)"},
		{Tier: TierOnDemand, Display: ".claude/rules/data.md", Bytes: 1200, Count: 1,
			Hosts: []string{"claude"}, Scope: []string{"pkg/**"}},
		{Tier: TierOnTrigger, Display: "2 skill bodies", Bytes: 6000, Count: 2,
			Hosts: []string{"claude", "copilot"}, Note: "load when your ask matches a description"},
		{Tier: TierInert, Display: ".github/instructions/a.instructions.md", Bytes: 900, Count: 1,
			Hosts: []string{"copilot"}},
		{Tier: TierInert, Display: ".github/instructions/b.instructions.md", Bytes: 900, Count: 1,
			Hosts: []string{"copilot"}},
	}
}

// The detected-host terminal page: cost-descending bars, aggregated scoped
// rules under IDLE, and the inert files collapsed to one closing line.
func TestRenderTerminalDetectedHost(t *testing.T) {
	var b strings.Builder
	Render(&b, renderFixture(), "fixture", "claude", "omakase status", false)
	got := b.String()

	if !strings.Contains(got, "WHAT YOUR AGENT READS — fixture · Claude Code · every turn: ~1,700 tok") {
		t.Errorf("header missing or wrong total:\n%s", got)
	}
	// Largest row carries the full-width bar; the loaded rows are sorted by
	// cost, so CLAUDE.md must precede the personal file.
	if !strings.Contains(got, strings.Repeat("█", barWidth)+"   ~1,000  CLAUDE.md") {
		t.Errorf("full bar row missing:\n%s", got)
	}
	if strings.Index(got, "CLAUDE.md") > strings.Index(got, "~/.claude/CLAUDE.md") {
		t.Errorf("loaded rows not cost-descending:\n%s", got)
	}
	if !strings.Contains(got, "IDLE UNTIL NEEDED — ~1,800 tok") {
		t.Errorf("idle total wrong:\n%s", got)
	}
	if !strings.Contains(got, "1 path-scoped rule (.claude/rules/)") {
		t.Errorf("scoped-rule aggregate missing:\n%s", got)
	}
	if !strings.Contains(got, "unread by Claude Code: .github/instructions/ (2 files)") {
		t.Errorf("inert line missing or uncollapsed:\n%s", got)
	}
	if strings.Contains(got, "claude only") {
		t.Errorf("host tag printed under a detected host:\n%s", got)
	}
}

// The undetected-host page: both hosts' per-turn totals in the header,
// single-host rows tagged, and no inert line (nothing can be called unread
// when the reader is unknown).
func TestRenderTerminalUndetectedHost(t *testing.T) {
	entries := renderFixture()
	// Undetected scans never demote to inert; re-tier the fixture the way
	// Scan(host="") would deliver it.
	for i := range entries {
		if entries[i].Tier == TierInert {
			entries[i].Tier = TierAlways
		}
	}
	var b strings.Builder
	Render(&b, entries, "fixture", "", "omakase status", false)
	got := b.String()

	if !strings.Contains(got, "~1,700 tok (Claude Code)") || !strings.Contains(got, "~1,650 tok (Copilot CLI)") {
		t.Errorf("per-host totals missing:\n%s", got)
	}
	if !strings.Contains(got, "· claude only") || !strings.Contains(got, "· copilot only") {
		t.Errorf("single-host tags missing:\n%s", got)
	}
	if strings.Contains(got, "unread by") {
		t.Errorf("inert line printed with no host detected:\n%s", got)
	}
}

// The markdown page mirrors the sections for the skill relay: bar column,
// idle bullets, italic inert line.
func TestRenderMarkdown(t *testing.T) {
	var b strings.Builder
	Render(&b, renderFixture(), "fixture", "claude", "omakase status", true)
	got := b.String()
	for _, want := range []string{
		"| | ~tok | layer | says |",
		"| " + strings.Repeat("█", barWidth) + " | 1,000 | `CLAUDE.md` | Ship no change without tests. |",
		"**Idle until needed — ~1,800 tok**",
		"_unread by Claude Code: .github/instructions/ (2 files)_",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("markdown page missing %q:\n%s", want, got)
		}
	}
}
