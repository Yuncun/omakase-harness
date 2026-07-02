package status

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/Yuncun/omakase-harness/internal/state"
)

// ---------------------------------------------------------------- fixtures

// newGitRepo mirrors the newrepo() fixture pattern of tests/placed.test.sh
// (and internal/state's newTestRepo): a real temp git repo with an identity
// that never blocks a commit on signing.
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
// agent file each, one skill dir) plus a Copilot CLI personal skill dir —
// the same shape as tests/scorecard.test.sh:166-173, extended with
// commands/agents/copilot rows so every personal_list branch
// (bin/status.sh:72-88) is exercised. File names are lowercase ASCII so
// bytewise sort.Strings cannot diverge from bash's glob order (brief note).
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
// contract-capture goldens: two committed harness files (+ one non-harness
// tracked file to prove exclusion), and a placed.tsv covering every render
// branch (brief: normal, disabled, missing, drifted, symlink, .omakase/,
// 6-tab row).
func buildInstalledFixture(t *testing.T) (*state.Repo, string) {
	t.Helper()
	dir := newGitRepo(t)

	writeFile(t, dir, ".claude/rules/team.md", "team rule\n")
	writeFile(t, dir, "CLAUDE.md", "doctrine\n")
	writeFile(t, dir, "src/app.js", "app\n") // non-harness: must not appear in Committed
	runGitT(t, dir, "add", ".claude/rules/team.md", "CLAUDE.md", "src/app.js")
	runGitT(t, dir, "commit", "-q", "-m", "files")

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

	target := "nonexistent-target.txt" // dangling on purpose (brief: "applies to dangling links too")
	if err := os.Symlink(target, filepath.Join(dir, "linked.txt")); err != nil {
		t.Fatal(err)
	}
	linkedHash := sha256Hex(target) // matches: not drifted

	writeFile(t, dir, "sixtab.txt", "sixtab-body\n")

	placedTSV := "" +
		"normal.txt\tdoc\tsome/src\t" + normalHash + "\t1\n" +
		"disabled.txt\tdoc\tsome/src\tdeadbeef\t0\n" +
		"missing.txt\tdoc\tsome/src\tdeadbeef\t1\n" +
		"drifted.txt\tdoc\tsome/src\t" + driftedLedgerHash + "\t1\n" +
		"linked.txt\tdoc\tsome/src\t" + linkedHash + "\t1\n" +
		".omakase/internal.sh\tgate\tsome/src\tdeadbeef\t1\n" + // must not render
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

	repo, err := state.Discover(dir)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	return repo, buildHomeFixture(t)
}

// ---------------------------------------------------------------- golden tests
//
// Expected strings below are transcribed VERBATIM from a run of the current
// bash bin/status.sh (commit d5f1757) against the fixtures built above (same
// file names/contents/hashes), with HOME set to the fixture home and cwd
// inside the fixture repo. Each block is marked "contract capture from
// bin/status.sh @ d5f1757". Do not paraphrase or retype these strings.

// contract capture from bin/status.sh @ d5f1757
// (bash bin/status.sh, installed fixture, terminal mode: full-output lines
// 7-25 — the render_inventory() slice, i.e. everything from "COMMITTED..."
// through the last GLOBAL row, no leading/trailing blank line.)
const wantInventoryTermInstalled = `COMMITTED (this repo) — tracked harness files
    + .claude/rules/team.md   (rule)
    + CLAUDE.md   (doc)
INJECTED (omakase) — placed by omakase init, gitignored
    + normal.txt   (doc, from some/src)
    - disabled.txt   (doc, from some/src; disabled — not restored, not verified)
    ! missing.txt   (doc, from some/src; MISSING — run omakase init to restore)
    ~ drifted.txt   (doc, from some/src; DRIFTED — differs from canonical, run omakase init to re-sync)
    + linked.txt -> nonexistent-target.txt   (doc, from some/src)
    + sixtab.txt   (doc, from some/src)
GLOBAL — not installed by omakase (Claude ~/.claude + Copilot ~/.copilot, applies to every repo)
    + ~/.claude/CLAUDE.md   (doc)
    + ~/.claude/settings.json   (config)
    + ~/.claude/rules/alpha.md   (rule)
    + ~/.claude/rules/beta.md   (rule)
    + ~/.claude/commands/cmd1.md   (command)
    + ~/.claude/agents/agent1.md   (agent)
    + ~/.claude/skills/myskill/   (skill)
    + ~/.copilot/skills/coskill/   (skill)
`

// contract capture from bin/status.sh @ d5f1757
// (bash bin/status.sh --markdown, installed fixture: full-output lines
// 11-31 — the render_inventory() slice in md mode.)
const wantInventoryMDInstalled = "### Committed (this repo) — tracked harness files\n" +
	"- `.claude/rules/team.md` — rule\n" +
	"- `CLAUDE.md` — doc\n" +
	"\n" +
	"### Injected (omakase) — placed by `omakase init`, gitignored\n" +
	"- `normal.txt` — doc, from some/src\n" +
	"- `disabled.txt` — doc, from some/src — disabled (not restored, not verified)\n" +
	"- `missing.txt` — doc, from some/src — **MISSING** (run `omakase init` to restore)\n" +
	"- `drifted.txt` — doc, from some/src — **DRIFTED** (differs from canonical; `omakase init` to re-sync, or it may be an intentional local edit)\n" +
	"- `linked.txt` → `nonexistent-target.txt` — doc, from some/src\n" +
	"- `sixtab.txt` — doc, from some/src\n" +
	"\n" +
	"### Global — not installed by omakase (Claude ~/.claude + Copilot ~/.copilot, applies to every repo)\n" +
	"- `~/.claude/CLAUDE.md` — doc\n" +
	"- `~/.claude/settings.json` — config\n" +
	"- `~/.claude/rules/alpha.md` — rule\n" +
	"- `~/.claude/rules/beta.md` — rule\n" +
	"- `~/.claude/commands/cmd1.md` — command\n" +
	"- `~/.claude/agents/agent1.md` — agent\n" +
	"- `~/.claude/skills/myskill/` — skill\n" +
	"- `~/.copilot/skills/coskill/` — skill\n"

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

// contract capture from bin/status.sh @ d5f1757
// (bash bin/status.sh, not-installed fixture, terminal mode — entire
// output: the not-installed message + render_inventory(), whole scope.)
const wantNotInstalledTerm = `No omakase harness is installed in this repo.
Run  omakase init  to inject one.

COMMITTED (this repo) — tracked harness files
    + .claude/rules/team.md   (rule)
INJECTED (omakase) — placed by omakase init, gitignored
    (none)
GLOBAL — not installed by omakase (Claude ~/.claude + Copilot ~/.copilot, applies to every repo)
    + ~/.claude/CLAUDE.md   (doc)
    + ~/.claude/settings.json   (config)
    + ~/.claude/rules/alpha.md   (rule)
    + ~/.claude/rules/beta.md   (rule)
    + ~/.claude/commands/cmd1.md   (command)
    + ~/.claude/agents/agent1.md   (agent)
    + ~/.claude/skills/myskill/   (skill)
    + ~/.copilot/skills/coskill/   (skill)
`

// contract capture from bin/status.sh @ d5f1757
// (bash bin/status.sh --markdown, not-installed fixture — entire output.)
const wantNotInstalledMD = "**No omakase harness is installed in this repo.** Run `omakase init` to inject one.\n" +
	"\n" +
	"### Committed (this repo) — tracked harness files\n" +
	"- `.claude/rules/team.md` — rule\n" +
	"\n" +
	"### Injected (omakase) — placed by `omakase init`, gitignored\n" +
	"- _(none)_\n" +
	"\n" +
	"### Global — not installed by omakase (Claude ~/.claude + Copilot ~/.copilot, applies to every repo)\n" +
	"- `~/.claude/CLAUDE.md` — doc\n" +
	"- `~/.claude/settings.json` — config\n" +
	"- `~/.claude/rules/alpha.md` — rule\n" +
	"- `~/.claude/rules/beta.md` — rule\n" +
	"- `~/.claude/commands/cmd1.md` — command\n" +
	"- `~/.claude/agents/agent1.md` — agent\n" +
	"- `~/.claude/skills/myskill/` — skill\n" +
	"- `~/.copilot/skills/coskill/` — skill\n"

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

// contract capture from bin/status.sh @ d5f1757
// (bash bin/status.sh, pre-0.10 fixture ($OMK/placed.list present,
// placed.tsv absent), terminal mode — entire output.)
const wantPre010Term = `Pre-0.10 omakase install detected (record: placed.list).
Run  omakase init  to migrate to the provenance ledger. Placed files:
  old-file-one.md
  old-file-two.sh
`

// contract capture from bin/status.sh @ d5f1757
// (bash bin/status.sh --markdown, pre-0.10 fixture — entire output.)
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

// TestPersonalListUnsetHome pins the unset-HOME contract (ultrareview
// bug_002): with home == "" the roots must be the ABSOLUTE "/.claude" and
// "/.copilot" — bash's `${HOME:-}/.claude` — never the cwd-relative
// ".claude". The cwd is set to a directory that DOES carry .claude/rules/,
// so a regression to filepath.Join (which drops the empty element and goes
// relative) would surface those files and fail here.
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

// TestPersonalListGlobSemantics locks the two glob-edge-case rules the brief
// calls out (not bash-capturable in isolation, so this is a semantics test,
// not a contract-capture golden): "*/ " matches dirs AND symlinks-to-dirs
// (checked via os.Stat, not DirEntry.IsDir()), and "*.md" matches ANY
// dirent ending .md (even a directory) gated by [ -e ]/os.Stat — a dangling
// symlink named *.md must be excluded, a directory named *.md must be
// included.
func TestPersonalListGlobSemantics(t *testing.T) {
	home := t.TempDir()
	writeFile(t, home, ".claude/rules/real.md", "x\n")
	// a directory named *.md: matches the *.md glob, passes the -e/Stat gate.
	if err := os.MkdirAll(filepath.Join(home, ".claude/rules/dirlooksmd.md"), 0o755); err != nil {
		t.Fatal(err)
	}
	// a dangling symlink named *.md: matches the glob, fails the -e/Stat gate.
	if err := os.Symlink(filepath.Join(home, "nope"), filepath.Join(home, ".claude/rules/dangling.md")); err != nil {
		t.Fatal(err)
	}
	// a real dir, and a symlink-to-dir, under skills/: both are "*/ " rows.
	if err := os.MkdirAll(filepath.Join(home, ".claude/skills/realskill"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(home, ".claude/skills/realskill"), filepath.Join(home, ".claude/skills/linkedskill")); err != nil {
		t.Fatal(err)
	}
	// bash's default glob (dotglob unset, confirmed against bash 3.2/5.x: a
	// bare `*.md` or `*/ ` never matches a leading-dot name) must exclude a
	// dotfile *.md and a dot-directory under skills/.
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
