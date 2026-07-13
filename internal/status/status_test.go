package status

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Yuncun/omakase-harness/internal/state"
)

// ---------------------------------------------------------------- fixtures

// buildStatusFixture builds an installed repo for the full-output goldens: two
// committed harness files (+ a non-harness tracked file), one present,
// non-drifted injected file (normal.txt) plus a HEALTHY .omakase machinery
// row (on disk, hash matching — healthy machinery stays out of inventory), a
// remembered source (acme/harness), a base VERSION, and a ledger. It returns
// the repo, the fixture HOME (shared with the inventory goldens), and a fake
// lefthook that emits fixtureDump. The $OMK layout is hand-built.
// fixtureGateContent is the healthy machinery file's bytes; the fixture rows
// carry its real hash so the row is neither drifted nor missing.
const fixtureGateContent = "#!/usr/bin/env bash\nfixture gate\n"

func buildStatusFixture(t *testing.T) (*state.Repo, string, string) {
	t.Helper()
	dir := newGitRepo(t)

	writeFile(t, dir, ".claude/rules/team.md", "team rule\n")
	writeFile(t, dir, "CLAUDE.md", "doctrine\n")
	writeFile(t, dir, "src/app.js", "app\n") // non-harness: excluded from Committed
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

	writeFile(t, dir, ".omakase/bin/omakase-gate.sh", fixtureGateContent)
	placedTSV := "normal.txt\tdoc\tacme/harness\t" + normalHash + "\t1\n" +
		".omakase/bin/omakase-gate.sh\tgate\tacme/harness\t" + sha256Hex(fixtureGateContent) + "\t1\n"
	writeOMK(t, repo.OMK, "placed.tsv", placedTSV)
	writeOMK(t, repo.OMK, "source", "acme/harness\n")
	writeOMK(t, repo.OMK, "ledger.tsv", fixtureLedger)
	writeFile(t, dir, ".omakase/VERSION", "0.11.3\n")

	return repo, buildHomeFixture(t), writeFakeLefthook(t, fixtureDump)
}

func writeOMK(t *testing.T, omk, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(omk, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// pinStatusEnv sets the env the goldens expect and chdirs into the repo, so
// Run's os.Getwd/os.Getenv see the fixture.
func pinStatusEnv(t *testing.T, repo *state.Repo, home, lefthook string) {
	t.Helper()
	t.Chdir(repo.Root)
	t.Setenv("HOME", home)
	t.Setenv("LEFTHOOK_BIN", lefthook)
	t.Setenv("OMAKASE_NOW", "2000000000")
	t.Setenv("NO_COLOR", "1")
}

func withRoot(golden, root string) string {
	return strings.ReplaceAll(golden, "{{ROOT}}", root)
}

// ---------------------------------------------------------------- full-output goldens
//
// {{ROOT}} templates the per-run temp path; each golden is the exact output for
// buildStatusFixture.

// Markdown output for the installed fixture.
const wantFullMD = "## 🥡 harness\n\n`acme/harness` · base omakase 0.11.3 · installed in `{{ROOT}}`\n\n**Zero footprint** — 2 file(s) injected, 0 committed; all gitignored via `.git/info/exclude` (invisible to git).\n\n### Guards — what runs when you commit / push\n\n| Run when | Guard | Enforces | Last verdict |\n| --- | --- | --- | --- |\n| `pre-commit` | markers | runs every commit | ✓ pass - 5m ago |\n| `pre-commit` | lint | sh -c 'echo a \\| grep a' | — |\n| `pre-push` | tests | cached; scope: a/*\\|b/* | ✗ fail - 2h ago |\n| `pre-push` | review | cached; scope: src/* | - not yet run |\n| `post-checkout` | omakase-ensure-present | self-heal: restore any missing injected files | — |\n\n### The project's harness (committed — managed by git, not omakase)\n- `.claude/rules/team.md` — rule\n- `CLAUDE.md` — doc\n\n### Injected (omakase) — placed by `omakase init`, gitignored\n- `normal.txt` — doc, from acme/harness\n\n### Global — not installed by omakase (Claude ~/.claude + Copilot ~/.copilot, applies to every repo)\n- `~/.claude/CLAUDE.md` — doc\n- `~/.claude/settings.json` — config\n- `~/.claude/rules/alpha.md` — rule\n- `~/.claude/rules/beta.md` — rule\n- `~/.claude/commands/cmd1.md` — command\n- `~/.claude/agents/agent1.md` — agent\n- `~/.claude/skills/myskill/` — skill\n- `~/.copilot/skills/coskill/` — skill\n\n_Refresh:_ `omakase init`  ·  _Remove:_ `omakase remove`  ·  _read-only; running status changes nothing._\n"

// Terminal output for the installed fixture, no banner.
const wantFullTerm = "harness — acme/harness · base omakase 0.11.3 · installed in {{ROOT}}\nzero footprint: 2 injected, 0 committed, all gitignored (.git/info/exclude)\n\nGUARDS — what runs when you commit / push\n  RUN WHEN        GUARD                    ENFORCES                                        LAST VERDICT\n  pre-commit      markers                  runs every commit                               ✓ pass - 5m ago\n  pre-commit      lint                     sh -c 'echo a | grep a'                         —\n  pre-push        tests                    cached; scope: a/*|b/*                          ✗ fail - 2h ago\n  pre-push        review                   cached; scope: src/*                            - not yet run\n  post-checkout   omakase-ensure-present   self-heal: restore any missing injected files   —\n\nTHE PROJECT'S HARNESS (committed — managed by git, not omakase)\n    + .claude/rules/team.md   (rule)\n    + CLAUDE.md   (doc)\nINJECTED (omakase) — placed by omakase init, gitignored\n    + normal.txt   (doc, from acme/harness)\nGLOBAL — not installed by omakase (Claude ~/.claude + Copilot ~/.copilot, applies to every repo)\n    + ~/.claude/CLAUDE.md   (doc)\n    + ~/.claude/settings.json   (config)\n    + ~/.claude/rules/alpha.md   (rule)\n    + ~/.claude/rules/beta.md   (rule)\n    + ~/.claude/commands/cmd1.md   (command)\n    + ~/.claude/agents/agent1.md   (agent)\n    + ~/.claude/skills/myskill/   (skill)\n    + ~/.copilot/skills/coskill/   (skill)\n\nRestore the harness (replaces missing or changed files; removes dropped ones):   omakase init\nUndo everything:                                                                 omakase remove\n"

// Terminal output for the installed fixture with a deterministic banner script
// at .omakase/bin/omakase-banner.sh printing two lines — proves the banner exec
// and multi-line stdout passthrough.
const bannerScript = "#!/usr/bin/env bash\necho \"== omakase ==\"\necho \"banner line two\"\n"
const wantFullTermBanner = "== omakase ==\nbanner line two\nharness — acme/harness · base omakase 0.11.3 · installed in {{ROOT}}\nzero footprint: 2 injected, 0 committed, all gitignored (.git/info/exclude)\n\nGUARDS — what runs when you commit / push\n  RUN WHEN        GUARD                    ENFORCES                                        LAST VERDICT\n  pre-commit      markers                  runs every commit                               ✓ pass - 5m ago\n  pre-commit      lint                     sh -c 'echo a | grep a'                         —\n  pre-push        tests                    cached; scope: a/*|b/*                          ✗ fail - 2h ago\n  pre-push        review                   cached; scope: src/*                            - not yet run\n  post-checkout   omakase-ensure-present   self-heal: restore any missing injected files   —\n\nTHE PROJECT'S HARNESS (committed — managed by git, not omakase)\n    + .claude/rules/team.md   (rule)\n    + CLAUDE.md   (doc)\nINJECTED (omakase) — placed by omakase init, gitignored\n    + normal.txt   (doc, from acme/harness)\nGLOBAL — not installed by omakase (Claude ~/.claude + Copilot ~/.copilot, applies to every repo)\n    + ~/.claude/CLAUDE.md   (doc)\n    + ~/.claude/settings.json   (config)\n    + ~/.claude/rules/alpha.md   (rule)\n    + ~/.claude/rules/beta.md   (rule)\n    + ~/.claude/commands/cmd1.md   (command)\n    + ~/.claude/agents/agent1.md   (agent)\n    + ~/.claude/skills/myskill/   (skill)\n    + ~/.copilot/skills/coskill/   (skill)\n\nRestore the harness (replaces missing or changed files; removes dropped ones):   omakase init\nUndo everything:                                                                 omakase remove\n"

func TestStatusRunMD(t *testing.T) {
	repo, home, lh := buildStatusFixture(t)
	pinStatusEnv(t, repo, home, lh)

	var stdout, stderr bytes.Buffer
	code := Run([]string{"--markdown"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr: %s)", code, stderr.String())
	}
	if stderr.Len() != 0 {
		t.Errorf("stderr = %q, want empty", stderr.String())
	}
	want := withRoot(wantFullMD, repo.Root)
	if got := stdout.String(); got != want {
		t.Errorf("Run --markdown mismatch\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestStatusRunTerm(t *testing.T) {
	repo, home, lh := buildStatusFixture(t)
	pinStatusEnv(t, repo, home, lh)

	var stdout, stderr bytes.Buffer
	code := Run(nil, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr: %s)", code, stderr.String())
	}
	if stderr.Len() != 0 {
		t.Errorf("stderr = %q, want empty", stderr.String())
	}
	want := withRoot(wantFullTerm, repo.Root)
	if got := stdout.String(); got != want {
		t.Errorf("Run (term) mismatch\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestStatusRunTermBanner(t *testing.T) {
	repo, home, lh := buildStatusFixture(t)
	writeFile(t, repo.Root, ".omakase/bin/omakase-banner.sh", bannerScript)
	pinStatusEnv(t, repo, home, lh)

	var stdout, stderr bytes.Buffer
	code := Run(nil, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr: %s)", code, stderr.String())
	}
	want := withRoot(wantFullTermBanner, repo.Root)
	if got := stdout.String(); got != want {
		t.Errorf("Run (term, banner) mismatch\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

// TestStatusRunTermBannerCwd pins the banner-exec contract: the banner script
// inherits the invocation cwd, not repo.Root. A cwd-sensitive fake banner
// (prints `pwd`) run from a subdirectory of the repo must see that
// subdirectory, proving Run does not force cmd.Dir = repo.Root.
func TestStatusRunTermBannerCwd(t *testing.T) {
	repo, home, lh := buildStatusFixture(t)
	writeFile(t, repo.Root, ".omakase/bin/omakase-banner.sh", "#!/usr/bin/env bash\npwd\n")
	pinStatusEnv(t, repo, home, lh)

	// buildStatusFixture already created src/ (tracked non-harness file); reuse it
	// as an invocation cwd that is inside the repo but distinct from repo.Root.
	sub := filepath.Join(repo.Root, "src")
	t.Chdir(sub)

	var stdout, stderr bytes.Buffer
	code := Run(nil, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr: %s)", code, stderr.String())
	}

	wantCwd, err := filepath.EvalSymlinks(sub)
	if err != nil {
		t.Fatal(err)
	}
	gotLine := strings.SplitN(stdout.String(), "\n", 2)[0]
	gotCwd, err := filepath.EvalSymlinks(gotLine)
	if err != nil {
		t.Fatalf("banner printed %q, not a real path: %v", gotLine, err)
	}
	if gotCwd != wantCwd {
		t.Errorf("banner ran with cwd = %q, want invocation cwd %q (not repo.Root %q)", gotCwd, wantCwd, repo.Root)
	}
}

// TestPipedStatusNeverInteractive checks that status.Run given bytes.Buffer
// writers (never *os.File) still emits the plain terminal page, never the TUI.
// interactiveTerminal gates on the process's os.Stdin/os.Stdout, which under
// `go test` is a pipe, not a terminal.
func TestPipedStatusNeverInteractive(t *testing.T) {
	repo, home, lh := buildStatusFixture(t)
	pinStatusEnv(t, repo, home, lh)

	var stdout, stderr bytes.Buffer
	code := Run(nil, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr: %s)", code, stderr.String())
	}
	if stderr.Len() != 0 {
		t.Errorf("stderr = %q, want empty", stderr.String())
	}
	// The plain terminal identity line — proof the static page rendered into
	// the buffer rather than an alt-screen program taking over the tty.
	if want := "harness — acme/harness"; !strings.HasPrefix(stdout.String(), want) {
		t.Errorf("piped status did not render the plain identity line; first line = %q, want prefix %q", firstLine(stdout.String()), want)
	}
}

// Once a file is toggled off (enabled=0), the zero-footprint count must reflect
// consent state: N counts enabled rows only, with a "(k toggled off)" note, so
// the page whose whole point is showing consent state no longer overstates what
// is on disk.
func TestStatusFootprintCountsConsentState(t *testing.T) {
	repo, home, lh := buildStatusFixture(t)
	// Mark normal.txt disabled (as FileOff would), leaving the machinery gate
	// row enabled -> 1 injected, 1 toggled off.
	placedTSV := "normal.txt\tdoc\tacme/harness\t" + sha256Hex("normal-body\n") + "\t0\n" +
		".omakase/bin/omakase-gate.sh\tgate\tacme/harness\t" + sha256Hex(fixtureGateContent) + "\t1\n"
	writeOMK(t, repo.OMK, "placed.tsv", placedTSV)
	pinStatusEnv(t, repo, home, lh)

	var md, mdErr bytes.Buffer
	if code := Run([]string{"--markdown"}, &md, &mdErr); code != 0 {
		t.Fatalf("md exit = %d (stderr=%q)", code, mdErr.String())
	}
	if !strings.Contains(md.String(), "1 file(s) injected (1 toggled off)") {
		t.Errorf("markdown footprint missing consent count:\n%s", md.String())
	}

	var term, termErr bytes.Buffer
	if code := Run(nil, &term, &termErr); code != 0 {
		t.Fatalf("term exit = %d (stderr=%q)", code, termErr.String())
	}
	if !strings.Contains(term.String(), "1 injected (1 toggled off)") {
		t.Errorf("terminal footprint missing consent count:\n%s", term.String())
	}
}

func TestStatusNotARepo(t *testing.T) {
	t.Chdir(t.TempDir()) // not a git repo

	var stdout, stderr bytes.Buffer
	code := Run(nil, &stdout, &stderr)
	if code != 1 {
		t.Errorf("exit = %d, want 1", code)
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout = %q, want empty", stdout.String())
	}
	if got, want := stderr.String(), "omakase: not inside a git repo\n"; got != want {
		t.Errorf("stderr = %q, want %q", got, want)
	}
}

// TestStatusFormatSelection pins the flag rule: only argv[0] is inspected, and only the
// three literal flags select md; anything else (or nothing) is terminal mode.
func TestStatusFormatSelection(t *testing.T) {
	repo, home, lh := buildStatusFixture(t)
	pinStatusEnv(t, repo, home, lh)

	mdHead := "## 🥡 harness"
	termHead := "harness — acme/harness"
	cases := []struct {
		argv   []string
		wantMD bool
	}{
		{[]string{"--markdown"}, true},
		{[]string{"-m"}, true},
		{[]string{"md"}, true},
		{[]string{"markdown"}, false}, // bare word, not one of the three literals
		{nil, false},
		{[]string{"--markdown", "extra"}, true},  // only argv[0] inspected
		{[]string{"extra", "--markdown"}, false}, // flag not in argv[0] -> term
	}
	// An unrecognized dash-flag is an error, never a silent page: a typo like
	// --enabel must not exit 0 (automation would read that as success).
	{
		var stdout, stderr bytes.Buffer
		if code := Run([]string{"--md"}, &stdout, &stderr); code != 2 {
			t.Fatalf("argv=[--md] exit=%d, want 2", code)
		}
		if !strings.Contains(stderr.String(), "unknown flag --md") {
			t.Errorf("stderr = %q, want unknown-flag message", stderr.String())
		}
	}
	for _, tc := range cases {
		var stdout, stderr bytes.Buffer
		if code := Run(tc.argv, &stdout, &stderr); code != 0 {
			t.Fatalf("argv=%v exit=%d", tc.argv, code)
		}
		got := stdout.String()
		head := termHead
		if tc.wantMD {
			head = mdHead
		}
		if !strings.HasPrefix(got, head) {
			t.Errorf("argv=%v: output should start %q, got first line %q", tc.argv, head, firstLine(got))
		}
	}
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// ---------------------------------------------------------------- not-installed / pre-0.10 routing

func TestStatusRunNotInstalled(t *testing.T) {
	dir := newGitRepo(t)
	writeFile(t, dir, ".claude/rules/team.md", "team rule\n")
	runGitT(t, dir, "add", ".claude/rules/team.md")
	runGitT(t, dir, "commit", "-q", "-m", "files")
	home := buildHomeFixture(t)
	t.Chdir(dir)
	t.Setenv("HOME", home)

	var stdout, stderr bytes.Buffer
	if code := Run([]string{"--markdown"}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if !strings.HasPrefix(stdout.String(), "**No omakase harness is installed in this repo.**") {
		t.Errorf("not-installed routing failed; got:\n%s", stdout.String())
	}
}

func TestStatusRunPre010(t *testing.T) {
	dir := newGitRepo(t)
	runGitT(t, dir, "commit", "-q", "--allow-empty", "-m", "init")
	repo, err := state.Discover(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(repo.OMK, 0o755); err != nil {
		t.Fatal(err)
	}
	writeOMK(t, repo.OMK, "placed.list", "old-file-one.md\nold-file-two.sh\n")
	t.Chdir(dir)
	t.Setenv("HOME", t.TempDir())

	var stdout, stderr bytes.Buffer
	if code := Run(nil, &stdout, &stderr); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if !strings.HasPrefix(stdout.String(), "Pre-0.10 omakase install detected (record: placed.list).") {
		t.Errorf("pre-0.10 routing failed; got:\n%s", stdout.String())
	}
}

// ---------------------------------------------------------------- identity derivation

func TestHarnessName(t *testing.T) {
	// harnessName strips the #fragment, a trailing .git, and a trailing /, then
	// takes the last path segment.
	cases := map[string]string{
		"":                                       "omakase-harness",
		"acme/harness":                           "harness",
		"https://github.com/acme/harness.git":    "harness",
		"git@github.com:acme/harness.git#subdir": "harness",
		"https://example.com/foo/":               "foo",
		"ssh://host/path/repo.git/":              "repo.git",
	}
	for src, want := range cases {
		if got := harnessName(src); got != want {
			t.Errorf("harnessName(%q) = %q, want %q", src, got, want)
		}
	}
}

func TestSrcDisplay(t *testing.T) {
	// srcDisplay strips a leading scheme:// and a trailing slash.
	cases := map[string]string{
		"":                                       "",
		"acme/harness":                           "acme/harness",
		"https://github.com/acme/harness.git":    "github.com/acme/harness.git",
		"git@github.com:acme/harness.git#subdir": "git@github.com:acme/harness.git#subdir",
		"https://example.com/foo/":               "example.com/foo",
		"ssh://host/path/repo.git/":              "host/path/repo.git",
	}
	for src, want := range cases {
		if got := srcDisplay(src); got != want {
			t.Errorf("srcDisplay(%q) = %q, want %q", src, got, want)
		}
	}
}
