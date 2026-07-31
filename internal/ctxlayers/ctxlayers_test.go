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

// A long path in an instruction file must not be able to punch through the
// box frame.
func TestWrapHardBreaksLongWords(t *testing.T) {
	long := strings.Repeat("x", 50)
	for _, line := range wrap(long, 20) {
		if runeLen(line) > 20 {
			t.Fatalf("line %q is %d wide, want <= 20", line, runeLen(line))
		}
	}
}

// pad counts runes, not bytes: a multi-byte character counted as its byte
// length silently shortens the row and misaligns the frame.
func TestPadIsRuneAware(t *testing.T) {
	got := pad("a·b", 10) // "·" is two bytes, one column
	if runeLen(got) != 10 {
		t.Fatalf("pad width = %d runes, want 10", runeLen(got))
	}
}

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
