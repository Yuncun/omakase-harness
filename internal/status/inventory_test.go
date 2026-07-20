package status

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
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
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

// writeFile writes rel (which may contain slashes) under dir, creating
// parent directories as needed.
func writeFile(t *testing.T, dir, rel, content string) {
	t.Helper()
	full := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// buildHomeFixture builds the fake HOME used across the golden tests: a
// Claude Code personal layout (CLAUDE.md, settings.json, one rule/command/
// agent file each, one skill dir) plus a Copilot CLI personal skill dir, with
// commands/agents/copilot rows so every PersonalList branch is exercised. File
// names are lowercase ASCII so bytewise sort.Strings gives a stable order.
func buildHomeFixture(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	writeFile(t, home, ".claude/CLAUDE.md", "global doctrine\n")
	writeFile(t, home, ".claude/settings.json", "{}\n")
	writeFile(t, home, ".claude/rules/alpha.md", "alpha rule\n")
	writeFile(t, home, ".claude/rules/beta.md", "beta rule\n")
	writeFile(t, home, ".claude/commands/cmd1.md", "cmd body\n")
	writeFile(t, home, ".claude/agents/agent1.md", "agent body\n")
	writeFile(t, home, ".claude/skills/myskill/SKILL.md", "skill body\n")
	writeFile(t, home, ".copilot/skills/coskill/SKILL.md", "coskill body\n")
	return home
}

// buildInstalledFixture builds the "installed" repo fixture used by the
// goldens: two committed harness files (+ one non-harness tracked file to
// prove exclusion), and a placed.tsv covering every render branch (normal,
// disabled, missing, drifted, symlink, the three .omakase/ machinery health
// states, 6-tab row).
func buildInstalledFixture(t *testing.T) (*state.Repo, string) {
	t.Helper()
	dir := newGitRepo(t)

	writeFile(t, dir, ".claude/rules/team.md", "team rule\n")
	writeFile(t, dir, "CLAUDE.md", "doctrine\n")
	writeFile(t, dir, "src/app.js", "app\n") // non-harness: must not appear in Committed
	runGitT(t, dir, "add", ".claude/rules/team.md", "CLAUDE.md", "src/app.js")
	runGitT(t, dir, "commit", "-q", "-m", "files")

	// Untracked agent config -> the "yours, unmanaged" group; Claude Code's
	// own worktree area must never surface there.
	writeFile(t, dir, ".claude/rules/local-tweak.md", "local tweak\n")
	writeFile(t, dir, ".claude/worktrees/wt/junk.md", "checkout litter\n")

	repo, err := state.Discover(dir)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if err := os.MkdirAll(repo.OMK, 0o755); err != nil {
		t.Fatal(err)
	}

	normalContent := "normal-body\n"
	writeFile(t, dir, "normal.txt", normalContent)
	normalHash := sha256Hex(normalContent)

	writeFile(t, dir, "disabled.txt", "disabled-body\n")

	// missing.txt: intentionally never created.

	writeFile(t, dir, "drifted.txt", "original\n")
	driftedLedgerHash := sha256Hex("original\n")
	writeFile(t, dir, "drifted.txt", "changed\n") // content changed after the ledger hash was recorded -> drift

	target := "nonexistent-target.txt" // dangling on purpose
	if err := os.Symlink(target, filepath.Join(dir, "linked.txt")); err != nil {
		t.Fatal(err)
	}
	linkedHash := sha256Hex(target) // matches: not drifted

	writeFile(t, dir, "sixtab.txt", "sixtab-body\n")

	// .omakase/ machinery rows, one per health state: healthy stays hidden,
	// enabled-but-missing and drifted must surface (issue #84 gap 3).
	// .omakase/internal.sh is the missing one — never created on disk.
	healthyContent := "healthy-gate\n"
	writeFile(t, dir, ".omakase/healthy.sh", healthyContent)
	healthyHash := sha256Hex(healthyContent)

	writeFile(t, dir, ".omakase/stale-gate.sh", "new-gate-body\n")
	staleLedgerHash := sha256Hex("old-gate-body\n")

	placedTSV := "" +
		"normal.txt\tdoc\tsome/src\t" + normalHash + "\t1\n" +
		"disabled.txt\tdoc\tsome/src\tdeadbeef\t0\n" +
		"missing.txt\tdoc\tsome/src\tdeadbeef\t1\n" +
		"drifted.txt\tdoc\tsome/src\t" + driftedLedgerHash + "\t1\n" +
		"linked.txt\tdoc\tsome/src\t" + linkedHash + "\t1\n" +
		".omakase/internal.sh\tgate\tsome/src\tdeadbeef\t1\n" + // enabled but missing -> renders
		".omakase/healthy.sh\tgate\tsome/src\t" + healthyHash + "\t1\n" + // healthy machinery -> hidden
		".omakase/stale-gate.sh\tgate\tsome/src\t" + staleLedgerHash + "\t1\n" + // drifted -> renders
		"sixtab.txt\tdoc\tsome/src\tanyhash\t1\textra\n" // 6th tab absorbed into Enabled

	if err := os.WriteFile(filepath.Join(repo.OMK, "placed.tsv"), []byte(placedTSV), 0o644); err != nil {
		t.Fatal(err)
	}

	return repo, buildHomeFixture(t)
}

// buildNotInstalledFixture builds a repo with the same committed surface as
// the installed fixture but no $OMK at all.
func buildNotInstalledFixture(t *testing.T) (*state.Repo, string) {
	t.Helper()
	dir := newGitRepo(t)
	writeFile(t, dir, ".claude/rules/team.md", "team rule\n")
	writeFile(t, dir, "src/app.js", "app\n")
	runGitT(t, dir, "add", ".claude/rules/team.md", "src/app.js")
	runGitT(t, dir, "commit", "-q", "-m", "files")

	// Untracked agent config for the audit's "yours, unmanaged" group.
	writeFile(t, dir, ".claude/rules/local-tweak.md", "local tweak\n")
	writeFile(t, dir, "CLAUDE.local.md", "personal doctrine\n")
	writeFile(t, dir, ".claude/worktrees/wt/junk.md", "checkout litter\n")

	repo, err := state.Discover(dir)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	return repo, buildHomeFixture(t)
}

// ---------------------------------------------------------------- golden tests
//
// The expected strings below are the exact output for the fixtures built above,
// with HOME set to the fixture home and cwd inside the fixture repo.

// Terminal-mode inventory for the installed fixture: the RenderInventory slice,
// everything from "COMMITTED..." through the last GLOBAL row, no leading or
// trailing blank line.
const wantInventoryTermInstalled = `THE PROJECT'S HARNESS (committed — managed by git, not omakase)
    + .claude/rules/team.md   (rule)
    + CLAUDE.md   (doc)
INJECTED (omakase) — placed by omakase init, gitignored
    + normal.txt   (doc, from some/src)
    - disabled.txt   (doc, from some/src; disabled — not restored, not verified)
    ! missing.txt   (doc, from some/src; MISSING — run omakase init to restore)
    ~ drifted.txt   (doc, from some/src; DRIFTED — differs from canonical, run omakase init to re-sync)
    + linked.txt -> nonexistent-target.txt   (doc, from some/src)
    ! .omakase/internal.sh   (gate, from some/src; MISSING — run omakase init to restore)
    ~ .omakase/stale-gate.sh   (gate, from some/src; DRIFTED — differs from canonical, run omakase init to re-sync)
    + sixtab.txt   (doc, from some/src)
    edit any of these directly — status offers keep/restore; to own the harness: /omakase:author
YOURS, UNMANAGED — untracked agent config, only in this clone (not committed, not placed by omakase)
    + .claude/rules/local-tweak.md   (rule)
    To keep or share one beyond this clone, add it to a harness — the author skill: /omakase:author
GLOBAL — 8 files in ~/.claude + ~/.copilot steer every repo (list: omakase status --global)
`

// Markdown-mode inventory for the installed fixture: the RenderInventory slice.
const wantInventoryMDInstalled = "### The project's harness (committed — managed by git, not omakase)\n" +
	"- `.claude/rules/team.md` — rule\n" +
	"- `CLAUDE.md` — doc\n" +
	"\n" +
	"### Injected (omakase) — placed by `omakase init`, gitignored\n" +
	"- `normal.txt` — doc, from some/src\n" +
	"- `disabled.txt` — doc, from some/src — disabled (not restored, not verified)\n" +
	"- `missing.txt` — doc, from some/src — **MISSING** (run `omakase init` to restore)\n" +
	"- `drifted.txt` — doc, from some/src — **DRIFTED** (differs from canonical; `omakase init` to re-sync, or it may be an intentional local edit)\n" +
	"- `linked.txt` → `nonexistent-target.txt` — doc, from some/src\n" +
	"- `.omakase/internal.sh` — gate, from some/src — **MISSING** (run `omakase init` to restore)\n" +
	"- `.omakase/stale-gate.sh` — gate, from some/src — **DRIFTED** (differs from canonical; `omakase init` to re-sync, or it may be an intentional local edit)\n" +
	"- `sixtab.txt` — doc, from some/src\n" +
	"\n" +
	"_Edit any of these directly — status offers keep/restore; to own the harness: `/omakase:author`._\n" +
	"\n" +
	"### Yours, unmanaged — untracked agent config, only in this clone (not committed, not placed by omakase)\n" +
	"- `.claude/rules/local-tweak.md` — rule\n" +
	"\n" +
	"_To keep or share one beyond this clone, add it to a harness — the author skill (`/omakase:author`)._\n" +
	"\n" +
	"### Global — 8 files in ~/.claude + ~/.copilot steer every repo (list: omakase status --global)\n"

func TestRenderInventoryTermInstalled(t *testing.T) {
	repo, home := buildInstalledFixture(t)
	var buf bytes.Buffer
	RenderInventory(&buf, repo, home, false)
	if got := buf.String(); got != wantInventoryTermInstalled {
		t.Errorf("RenderInventory (term) mismatch\n--- got ---\n%s\n--- want ---\n%s", got, wantInventoryTermInstalled)
	}
}

func TestRenderInventoryMDInstalled(t *testing.T) {
	repo, home := buildInstalledFixture(t)
	var buf bytes.Buffer
	RenderInventory(&buf, repo, home, true)
	if got := buf.String(); got != wantInventoryMDInstalled {
		t.Errorf("RenderInventory (md) mismatch\n--- got ---\n%s\n--- want ---\n%s", got, wantInventoryMDInstalled)
	}
}

// Terminal-mode output for the not-installed fixture: the presence-only audit
// (#119) — committed + global groups only (no empty Injected group), the scan
// boundary stated, and the install pointer naming the owner/repo form (a bare
// init with nothing remembered installs nothing).
const wantNotInstalledTerm = `No omakase harness is installed in this repo.

AGENT CONFIG COMMITTED IN THIS REPO (managed by git, not omakase)
    + .claude/rules/team.md   (rule)
YOURS, UNMANAGED — untracked agent config, only in this clone (not committed, not placed by omakase)
    + .claude/rules/local-tweak.md   (rule)
    + CLAUDE.local.md   (doc)
    To keep or share one beyond this clone, add it to a harness — the author skill: /omakase:author
GLOBAL — 8 files in ~/.claude + ~/.copilot steer every repo (list: omakase status --global)

A presence check of known paths for known tools — not exhaustive; a file can be present and never read.
Install a harness:  omakase init <owner/repo>
`

// Markdown-mode output for the not-installed fixture.
const wantNotInstalledMD = "**No omakase harness is installed in this repo.**\n" +
	"\n" +
	"### Agent config committed in this repo (managed by git, not omakase)\n" +
	"- `.claude/rules/team.md` — rule\n" +
	"\n" +
	"### Yours, unmanaged — untracked agent config, only in this clone (not committed, not placed by omakase)\n" +
	"- `.claude/rules/local-tweak.md` — rule\n" +
	"- `CLAUDE.local.md` — doc\n" +
	"\n" +
	"_To keep or share one beyond this clone, add it to a harness — the author skill (`/omakase:author`)._\n" +
	"\n" +
	"### Global — 8 files in ~/.claude + ~/.copilot steer every repo (list: omakase status --global)\n" +
	"\n" +
	"_A presence check of known paths for known tools — not exhaustive; a file can be present and never read._\n" +
	"\n" +
	"_Install a harness:_ `omakase init <owner/repo>`\n"

func TestRenderNotInstalledTerm(t *testing.T) {
	repo, home := buildNotInstalledFixture(t)
	var buf bytes.Buffer
	RenderNotInstalled(&buf, repo, home, false)
	if got := buf.String(); got != wantNotInstalledTerm {
		t.Errorf("RenderNotInstalled (term) mismatch\n--- got ---\n%s\n--- want ---\n%s", got, wantNotInstalledTerm)
	}
}

func TestRenderNotInstalledMD(t *testing.T) {
	repo, home := buildNotInstalledFixture(t)
	var buf bytes.Buffer
	RenderNotInstalled(&buf, repo, home, true)
	if got := buf.String(); got != wantNotInstalledMD {
		t.Errorf("RenderNotInstalled (md) mismatch\n--- got ---\n%s\n--- want ---\n%s", got, wantNotInstalledMD)
	}
}

// ---------------------------------------------------------------- unmanaged

// UnmanagedList: an untracked agent file at a known path is listed; a
// git-tracked file, a placed (ledgered) file — any enabled state — and
// Claude Code's own .claude/worktrees/ area are not; a gitignored personal
// file still is (ignored ≠ managed).
func TestUnmanagedList(t *testing.T) {
	dir := newGitRepo(t)
	writeFile(t, dir, "CLAUDE.md", "committed\n")
	runGitT(t, dir, "add", "CLAUDE.md")
	runGitT(t, dir, "commit", "-q", "-m", "c")

	writeFile(t, dir, ".claude/rules/mine.md", "mine\n")
	writeFile(t, dir, ".claude/rules/placed.md", "placed\n")
	writeFile(t, dir, ".claude/rules/off.md", "toggled\n")
	writeFile(t, dir, ".claude/worktrees/wt/junk.md", "litter\n")
	writeFile(t, dir, ".claude/settings.local.json", "{}\n")
	writeFile(t, dir, ".gitignore", ".claude/settings.local.json\n")
	writeFile(t, dir, ".omakase/stray.sh", "machinery residue\n") // unledgered machinery: torn state, never "yours"

	placedPath := filepath.Join(dir, "placed.tsv")
	if err := os.WriteFile(placedPath, []byte(
		".claude/rules/placed.md\trule\tsome/src\tdeadbeef\t1\n"+
			".claude/rules/off.md\trule\tsome/src\tdeadbeef\t0\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	got := UnmanagedList(dir, placedPath)
	want := [][2]string{
		{".claude/rules/mine.md", "rule"},
		{".claude/settings.local.json", "config"},
	}
	if len(got) != len(want) {
		t.Fatalf("UnmanagedList = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("row %d = %v, want %v", i, got[i], want[i])
		}
	}
}

// renderUnmanaged: an empty list renders nothing (the group is a flag, not a
// fixture); past the cap the elision is stated, never silent.
func TestRenderUnmanagedCap(t *testing.T) {
	var empty bytes.Buffer
	renderUnmanaged(&empty, nil, false)
	if empty.Len() != 0 {
		t.Errorf("empty list rendered %q, want nothing", empty.String())
	}

	var rows [][2]string
	for i := 0; i < maxUnmanagedRows+2; i++ {
		rows = append(rows, [2]string{fmt.Sprintf(".claude/rules/r%02d.md", i), "rule"})
	}
	// The cap keeps the FIRST maxUnmanagedRows rows: both boundary rows are
	// pinned by identity, so a wrong-slice regression (an offset or a last-N
	// window) fails even though it would still render 20 rows and "2 more".
	last := fmt.Sprintf(".claude/rules/r%02d.md", maxUnmanagedRows-1)
	cut := fmt.Sprintf(".claude/rules/r%02d.md", maxUnmanagedRows)
	for _, mode := range []struct {
		name string
		md   bool
		row  string
	}{
		{"terminal", false, "+ .claude/rules/"},
		{"markdown", true, "- `.claude/rules/"},
	} {
		var buf bytes.Buffer
		renderUnmanaged(&buf, rows, mode.md)
		out := buf.String()
		if !strings.Contains(out, "… and 2 more") {
			t.Errorf("%s: cap not stated:\n%s", mode.name, out)
		}
		if strings.Count(out, mode.row) != maxUnmanagedRows {
			t.Errorf("%s: rendered %d rows, want %d:\n%s", mode.name, strings.Count(out, mode.row), maxUnmanagedRows, out)
		}
		if !strings.Contains(out, "r00.md") || !strings.Contains(out, last) {
			t.Errorf("%s: first-20 boundary rows missing (want r00 and %s):\n%s", mode.name, last, out)
		}
		if strings.Contains(out, cut) {
			t.Errorf("%s: elided row %s rendered:\n%s", mode.name, cut, out)
		}
	}
}

// Terminal-mode output for the pre-0.10 fixture ($OMK/placed.list present,
// placed.tsv absent).
const wantPre010Term = `Pre-0.10 omakase install detected (record: placed.list).
Run  omakase init  to migrate to the provenance ledger. Placed files:
  old-file-one.md
  old-file-two.sh
`

// Markdown-mode output for the pre-0.10 fixture.
const wantPre010MD = "**Pre-0.10 omakase install detected** (record: `placed.list`). Run `omakase init` to migrate to the provenance ledger. Placed files:\n" +
	"- `old-file-one.md`\n" +
	"- `old-file-two.sh`\n"

func buildPre010Fixture(t *testing.T) string {
	t.Helper()
	omk := filepath.Join(t.TempDir(), "omakase")
	if err := os.MkdirAll(omk, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, omk, "placed.list", "old-file-one.md\nold-file-two.sh\n")
	return omk
}

func TestRenderPre010Term(t *testing.T) {
	omk := buildPre010Fixture(t)
	var buf bytes.Buffer
	RenderPre010(&buf, omk, false)
	if got := buf.String(); got != wantPre010Term {
		t.Errorf("RenderPre010 (term) mismatch\n--- got ---\n%s\n--- want ---\n%s", got, wantPre010Term)
	}
}

func TestRenderPre010MD(t *testing.T) {
	omk := buildPre010Fixture(t)
	var buf bytes.Buffer
	RenderPre010(&buf, omk, true)
	if got := buf.String(); got != wantPre010MD {
		t.Errorf("RenderPre010 (md) mismatch\n--- got ---\n%s\n--- want ---\n%s", got, wantPre010MD)
	}
}

// ---------------------------------------------------------------- CommittedList

func TestCommittedList(t *testing.T) {
	repo, _ := buildInstalledFixture(t)
	got := CommittedList(repo.Root)
	want := []string{".claude/rules/team.md", "CLAUDE.md"} // src/app.js excluded: not a harness glob
	if len(got) != len(want) {
		t.Fatalf("CommittedList = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("CommittedList[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestCommittedListError(t *testing.T) {
	dir := t.TempDir() // not a git repo
	if got := CommittedList(dir); got != nil {
		t.Errorf("CommittedList(non-repo) = %v, want nil", got)
	}
}

// ---------------------------------------------------------------- PersonalList

func TestPersonalList(t *testing.T) {
	home := buildHomeFixture(t)
	got := PersonalList(home)
	want := [][2]string{
		{"~/.claude/CLAUDE.md", "doc"},
		{"~/.claude/settings.json", "config"},
		{"~/.claude/rules/alpha.md", "rule"},
		{"~/.claude/rules/beta.md", "rule"},
		{"~/.claude/commands/cmd1.md", "command"},
		{"~/.claude/agents/agent1.md", "agent"},
		{"~/.claude/skills/myskill/", "skill"},
		{"~/.copilot/skills/coskill/", "skill"},
	}
	if len(got) != len(want) {
		t.Fatalf("PersonalList = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("PersonalList[%d] = %v, want %v", i, got[i], want[i])
		}
	}
}

func TestPersonalListNoHome(t *testing.T) {
	home := t.TempDir() // no .claude, no .copilot
	if got := PersonalList(home); len(got) != 0 {
		t.Errorf("PersonalList(empty home) = %v, want empty", got)
	}
}

// TestPersonalListUnsetHome pins the unset-HOME contract: with home == "" the
// roots must be the absolute "/.claude" and "/.copilot", never the cwd-relative
// ".claude". The cwd is set to a directory that does carry .claude/rules/, so a
// regression to filepath.Join (which drops the empty element and goes relative)
// would surface those files and fail here.
func TestPersonalListUnsetHome(t *testing.T) {
	if isDir("/.claude") || isDir("/.copilot") {
		t.Skip("machine has a root-level /.claude or /.copilot — the unset-HOME contrast is not testable here")
	}
	trap := t.TempDir()
	if err := os.MkdirAll(filepath.Join(trap, ".claude", "rules"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(trap, ".claude", "rules", "team.md"), []byte("rule\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(trap)
	if got := PersonalList(""); len(got) != 0 {
		t.Errorf("PersonalList(\"\") = %v, want empty (roots must be /.claude and /.copilot, not cwd-relative)", got)
	}
}

// TestPersonalListGlobSemantics locks two glob edge-case rules: "*/" matches
// dirs and symlinks-to-dirs (checked via os.Stat, not DirEntry.IsDir()), and
// "*.md" matches any dirent ending .md (even a directory) gated by os.Stat — a
// dangling symlink named *.md must be excluded, a directory named *.md must be
// included.
func TestPersonalListGlobSemantics(t *testing.T) {
	home := t.TempDir()
	writeFile(t, home, ".claude/rules/real.md", "x\n")
	// a directory named *.md: matches the *.md glob, passes the Stat gate.
	if err := os.MkdirAll(filepath.Join(home, ".claude/rules/dirlooksmd.md"), 0o755); err != nil {
		t.Fatal(err)
	}
	// a dangling symlink named *.md: matches the glob, fails the Stat gate.
	if err := os.Symlink(filepath.Join(home, "nope"), filepath.Join(home, ".claude/rules/dangling.md")); err != nil {
		t.Fatal(err)
	}
	// a real dir, and a symlink-to-dir, under skills/: both are "*/" rows.
	if err := os.MkdirAll(filepath.Join(home, ".claude/skills/realskill"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(home, ".claude/skills/realskill"), filepath.Join(home, ".claude/skills/linkedskill")); err != nil {
		t.Fatal(err)
	}
	// The glob never matches a leading-dot name, so a dotfile *.md and a
	// dot-directory under skills/ must both be excluded.
	writeFile(t, home, ".claude/rules/.hidden.md", "x\n")
	if err := os.MkdirAll(filepath.Join(home, ".claude/skills/.hiddenskill"), 0o755); err != nil {
		t.Fatal(err)
	}

	got := PersonalList(home)
	byPath := map[string]string{}
	for _, row := range got {
		byPath[row[0]] = row[1]
	}

	if kind, ok := byPath["~/.claude/rules/dirlooksmd.md"]; !ok || kind != "rule" {
		t.Errorf("directory named *.md: want row kind %q, got ok=%v kind=%q", "rule", ok, kind)
	}
	if _, ok := byPath["~/.claude/rules/dangling.md"]; ok {
		t.Errorf("dangling symlink named *.md: want no row, got one")
	}
	if kind, ok := byPath["~/.claude/skills/realskill/"]; !ok || kind != "skill" {
		t.Errorf("real skill dir: want row kind %q, got ok=%v kind=%q", "skill", ok, kind)
	}
	if kind, ok := byPath["~/.claude/skills/linkedskill/"]; !ok || kind != "skill" {
		t.Errorf("symlink-to-dir skill: want row kind %q, got ok=%v kind=%q", "skill", ok, kind)
	}
	if _, ok := byPath["~/.claude/rules/.hidden.md"]; ok {
		t.Errorf("dotfile *.md: want no row (bash glob without dotglob never matches it), got one")
	}
	if _, ok := byPath["~/.claude/skills/.hiddenskill/"]; ok {
		t.Errorf("dot-directory under skills/: want no row (bash glob without dotglob never matches it), got one")
	}
}

// A kept row renders its own state — kept (yours) — in both output modes;
// an edit made after the keep adds the DRIFTED suffix measured against the
// accepted version (pointing at omakase diff, not init).
func TestRenderInjectedKeptRow(t *testing.T) {
	dir := newGitRepo(t)
	repo, err := state.Discover(dir)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(repo.OMK, "kept"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, dir, "AGENTS.md", "accepted\n")
	writeFile(t, filepath.Join(repo.OMK, "kept"), "AGENTS.md", "accepted\n")
	rows := "AGENTS.md\tdoc\tacme\t" + sha256Hex("accepted\n") + "\t1\n"
	if err := os.WriteFile(filepath.Join(repo.OMK, "placed.tsv"), []byte(rows), 0o644); err != nil {
		t.Fatal(err)
	}

	var md, term bytes.Buffer
	RenderInventory(&md, repo, t.TempDir(), true)
	RenderInventory(&term, repo, t.TempDir(), false)
	if !strings.Contains(md.String(), "`AGENTS.md` — doc, from acme — kept (yours)") {
		t.Errorf("md kept row wrong:\n%s", md.String())
	}
	if !strings.Contains(term.String(), "= AGENTS.md   (doc, from acme; kept (yours))") {
		t.Errorf("term kept row wrong:\n%s", term.String())
	}
	if strings.Contains(md.String(), "DRIFTED") {
		t.Errorf("healthy kept row rendered as drifted:\n%s", md.String())
	}

	// Edit after keep: drift returns, measured against the accepted hash.
	writeFile(t, dir, "AGENTS.md", "accepted\nsecond edit\n")
	var md2 bytes.Buffer
	RenderInventory(&md2, repo, t.TempDir(), true)
	if !strings.Contains(md2.String(), "kept (yours)") || !strings.Contains(md2.String(), "differs from your accepted version") {
		t.Errorf("post-keep edit row wrong:\n%s", md2.String())
	}
}

// A missing kept file and a disabled row with a saved kept copy both keep
// their consent visible (review finding, PR #100): the MISSING row says the
// kept version is saved; the disabled row says --enable brings it back.
func TestRenderInjectedKeptMissingAndDisabled(t *testing.T) {
	dir := newGitRepo(t)
	repo, err := state.Discover(dir)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(repo.OMK, "kept"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(repo.OMK, "kept"), "gone.md", "accepted\n")
	writeFile(t, filepath.Join(repo.OMK, "kept"), "off.md", "accepted\n")
	rows := "gone.md\tdoc\tacme\t" + sha256Hex("accepted\n") + "\t1\n" +
		"off.md\tdoc\tacme\t" + sha256Hex("accepted\n") + "\t0\n"
	if err := os.WriteFile(filepath.Join(repo.OMK, "placed.tsv"), []byte(rows), 0o644); err != nil {
		t.Fatal(err)
	}

	var md bytes.Buffer
	RenderInventory(&md, repo, t.TempDir(), true)
	out := md.String()
	if !strings.Contains(out, "`gone.md`") || !strings.Contains(out, "MISSING** (your kept version is saved") {
		t.Errorf("missing kept row hides the saved copy:\n%s", out)
	}
	if !strings.Contains(out, "`off.md`") || !strings.Contains(out, "a kept version of yours is saved") {
		t.Errorf("disabled kept row hides the saved copy:\n%s", out)
	}
}
