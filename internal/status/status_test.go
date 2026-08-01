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
// non-drifted injected file (normal.txt) plus a HEALTHY omakase.manifest
// machinery row (on disk, hash matching — healthy machinery stays out of
// inventory), a remembered source (acme/harness), a base VERSION, a ledger, and
// a snapshot manifest declaring the fixture gates (what the Guards chart reads).
// It returns the repo and the fixture HOME (shared with the inventory goldens).
// The $OMK layout is hand-built.

// fixtureManifest declares the gates the Guards chart renders (markers, tests,
// review), matching fixtureGates in guards_test.go.
// The name: differs from the source's last folder ("harness") on purpose: the
// goldens prove the header prefers the manifest's declared identity (#131
// gripe 5).
const fixtureManifest = "name: acme-dev-harness\nversion: 0.11.3\n\n" +
	"gate: markers\n  hook: pre-commit\n  run: .omakase/gates/example.sh\n\n" +
	"gate: tests\n  hook: pre-push\n  run: make check\n  cacheable: true\n  glob: a/*|b/*\n\n" +
	"gate: review\n  hook: pre-push\n  run: echo BLOCKED; exit 1\n  cacheable: true\n  glob: src/*\n"

func buildStatusFixture(t *testing.T) (*state.Repo, string) {
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

	writeFile(t, dir, "omakase.manifest", fixtureManifest)
	placedTSV := "normal.txt\t" + normalHash + "\n" +
		"omakase.manifest\t" + sha256Hex(fixtureManifest) + "\n"
	writeOMK(t, repo.OMK, "placed.tsv", placedTSV)
	writeOMK(t, repo.OMK, "source", "acme/harness\n")
	writeOMK(t, repo.OMK, "ledger.tsv", fixtureLedger)
	writeFile(t, dir, ".omakase/VERSION", "0.11.3\n")
	// The Guards chart reads the gate list from the snapshot manifest.
	if err := os.MkdirAll(filepath.Join(repo.OMK, "payload-snapshot"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeOMK(t, filepath.Join(repo.OMK, "payload-snapshot"), "omakase.manifest", fixtureManifest)

	return repo, buildHomeFixture(t)
}

func writeOMK(t *testing.T, omk, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(omk, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// pinStatusEnv sets the env the goldens expect and chdirs into the repo, so
// Run's os.Getwd/os.Getenv see the fixture.
func pinStatusEnv(t *testing.T, repo *state.Repo, home string) {
	t.Helper()
	t.Chdir(repo.Root)
	t.Setenv("HOME", home)
	t.Setenv("OMAKASE_NOW", "2000000000")
	t.Setenv("NO_COLOR", "1")
	// The layers section tiers by detected host; the suite itself runs inside
	// a host, so pin detection to "unknown" or the goldens change with the
	// developer's terminal.
	for _, v := range []string{"CLAUDECODE", "CLAUDE_CODE", "COPILOT_CLI", "COPILOT_AGENT_SESSION_ID"} {
		t.Setenv(v, "")
	}
}

func withRoot(golden, root string) string {
	return strings.ReplaceAll(golden, "{{ROOT}}", root)
}

// ---------------------------------------------------------------- full-output goldens
//
// {{ROOT}} templates the per-run temp path; each golden is the exact output for
// buildStatusFixture.

// Markdown output for the installed fixture.
const wantFullMD = "## 🥡 acme-dev-harness\n\n`acme/harness` · base omakase 0.11.3 · installed in `{{ROOT}}`\n\n**Zero footprint** — 2 file(s) injected, 0 committed; all gitignored via `.git/info/exclude` (invisible to git).\n\n### Guards — what runs when you commit / push\n\n| Run when | Guard | Enforces | Last verdict |\n| --- | --- | --- | --- |\n| `pre-commit` | markers | runs every fire | ✓ pass - 5m ago |\n| `pre-push` | tests | cached; scope: a/*\\|b/* | ✗ fail - 2h ago |\n| `pre-push` | review | cached; scope: src/* | - not yet run |\n\n### Loaded every turn — 001 · ~8 tok (Claude Code) · ~6 tok (Copilot CLI)\n\n| | ~tok | layer | says |\n| --- | ---: | --- | --- |\n| ████████████ | 4 | `~/.copilot/copilot-instructions.md`  · copilot only | copilot doctrine |\n| ████████████ | 4 | `~/.claude/CLAUDE.md`  · claude only | global doctrine |\n| ██████ | 2 | `CLAUDE.md` | doctrine |\n| ██████ | 2 | `.claude/rules/team.md`  · claude only | team rule |\n\n_Estimated at 4 bytes/token — your host's `/context` has the real numbers. `omakase status --show <path>` prints any layer in full._\n\n_Refresh:_ `omakase init`  ·  _Remove:_ `omakase remove`  ·  _read-only; running status changes nothing._\n"

// Terminal output for the installed fixture; the page opens with the
// built-in banner box (#172), plain under the goldens' NO_COLOR=1.
const wantFullTerm = "╭──────────────────────────────────────────────────────╮\n│ 🥡 acme-dev-harness                                  │\n╰──────────────────────────────────────────────────────╯\nacme-dev-harness — acme/harness · base omakase 0.11.3 · installed in {{ROOT}}\nzero footprint: 2 injected, 0 committed, all gitignored (.git/info/exclude)\n\nGUARDS — what runs when you commit / push\n  RUN WHEN     GUARD     ENFORCES                 LAST VERDICT\n  pre-commit   markers   runs every fire          ✓ pass - 5m ago\n  pre-push     tests     cached; scope: a/*|b/*   ✗ fail - 2h ago\n  pre-push     review    cached; scope: src/*     - not yet run\n\nLOADED EVERY TURN — 001 · ~8 tok (Claude Code) · ~6 tok (Copilot CLI)\n  ████████████       ~4  ~/.copilot/copilot-instructions.md  \"copilot doctrine\"  · copilot only\n  ████████████       ~4  ~/.claude/CLAUDE.md                 \"global doctrine\"  · claude only\n  ██████             ~2  CLAUDE.md                           \"doctrine\"\n  ██████             ~2  .claude/rules/team.md               \"team rule\"  · claude only\n\nestimated at 4 bytes/token — your host's /context has the real numbers\nomakase status --show <path>   print any layer in full\n\nRestore the harness (replaces missing or changed files; removes dropped ones):   omakase init\nUndo everything:                                                                 omakase remove\n"

func TestStatusRunMD(t *testing.T) {
	repo, home := buildStatusFixture(t)
	pinStatusEnv(t, repo, home)

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
	repo, home := buildStatusFixture(t)
	pinStatusEnv(t, repo, home)

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

// TestPipedStatusPlainPage checks that a default (flagless) status.Run renders
// the plain terminal page into the given writers — the only page since the
// interactive screen was removed (#156).
func TestPipedStatusPlainPage(t *testing.T) {
	repo, home := buildStatusFixture(t)
	pinStatusEnv(t, repo, home)

	var stdout, stderr bytes.Buffer
	code := Run(nil, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr: %s)", code, stderr.String())
	}
	if stderr.Len() != 0 {
		t.Errorf("stderr = %q, want empty", stderr.String())
	}
	// The plain banner + identity line — proof the static page rendered into
	// the buffer rather than an alt-screen program taking over the tty.
	if want := "╭──────────────────────────────────────────────────────╮"; !strings.HasPrefix(stdout.String(), want) {
		t.Errorf("piped status did not render the plain page; first line = %q, want prefix %q", firstLine(stdout.String()), want)
	}
	if want := "acme-dev-harness — acme/harness"; !strings.Contains(stdout.String(), want) {
		t.Errorf("piped status missing the identity line %q", want)
	}
}

// Once a file is toggled off (enabled=0), the zero-footprint count must reflect
// consent state: N counts enabled rows only, with a "(k toggled off)" note, so
// the page whose whole point is showing consent state no longer overstates what
// is on disk.
func TestStatusFootprintCountsConsentState(t *testing.T) {
	repo, home := buildStatusFixture(t)
	// Mark normal.txt disabled (as FileOff would), leaving the machinery
	// manifest row enabled -> 1 injected, 1 toggled off.
	placedTSV := "normal.txt\t" + sha256Hex("normal-body\n") + "\n" +
		"omakase.manifest\t" + sha256Hex(fixtureManifest) + "\n"
	writeOMK(t, repo.OMK, "placed.tsv", placedTSV)
	writeOMK(t, repo.OMK, "disabled-files", "normal.txt\n")
	pinStatusEnv(t, repo, home)

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
	repo, home := buildStatusFixture(t)
	pinStatusEnv(t, repo, home)

	mdHead := "## 🥡 acme-dev-harness"
	// The terminal page opens with the built-in banner box (#172).
	termHead := "╭──────────────────────────────────────────────────────╮"
	cases := []struct {
		argv   []string
		wantMD bool
	}{
		{[]string{"--markdown"}, true},
		{[]string{"-m"}, true},
		{[]string{"md"}, true},
		{nil, false},
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
	// A stray bare word is an error too: `omakase status <path>` used to be
	// silently dropped, reporting on the CWD while looking like it answered
	// for the path (#164's host-agnostic finding).
	for _, argv := range [][]string{
		{"markdown"}, // not one of the three md literals
		{"/some/other/repo"},
		{"--markdown", "extra"},
	} {
		var stdout, stderr bytes.Buffer
		if code := Run(argv, &stdout, &stderr); code != 2 {
			t.Fatalf("argv=%v exit=%d, want 2 (bare word must not be dropped)", argv, code)
		}
		if !strings.Contains(stderr.String(), "unexpected argument") {
			t.Errorf("argv=%v stderr = %q, want unexpected-argument message", argv, stderr.String())
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
	// srcDisplay strips a leading scheme:// and a trailing slash, and renders
	// github.com `//`-subpath sources as the browsable web path (#131 gripe
	// 2); everything else prints as before.
	cases := map[string]string{
		"":                                       "",
		"acme/harness":                           "acme/harness",
		"https://github.com/acme/harness.git":    "github.com/acme/harness.git",
		"git@github.com:acme/harness.git#subdir": "git@github.com:acme/harness.git#subdir",
		"https://example.com/foo/":               "example.com/foo",
		"ssh://host/path/repo.git/":              "host/path/repo.git",
		// The browsable transform: `//<sub>` → `/tree/HEAD/<sub>`; a #ref pin
		// browses at that ref; non-GitHub `//` sources are untouched.
		"https://github.com/Yuncun/omakase-harness//omakase-harness-harness":     "github.com/Yuncun/omakase-harness/tree/HEAD/omakase-harness-harness",
		"https://github.com/Yuncun/omakase-harness.git//omakase-harness-harness": "github.com/Yuncun/omakase-harness/tree/HEAD/omakase-harness-harness",
		"github.com/Yuncun/omakase-harness//omakase-harness-harness#v2":          "github.com/Yuncun/omakase-harness/tree/v2/omakase-harness-harness",
		"https://gitlab.com/acme/harness//sub":                                   "gitlab.com/acme/harness//sub",
		"/Users/me/local-harness":                                                "/Users/me/local-harness",
	}
	for src, want := range cases {
		if got := srcDisplay(src); got != want {
			t.Errorf("srcDisplay(%q) = %q, want %q", src, got, want)
		}
	}
}

// `status --global` expands the page's collapsed GLOBAL line: the full
// personal-config listing, from $HOME only (#131 gripe 4).
func TestStatusRunGlobal(t *testing.T) {
	repo, home := buildStatusFixture(t)
	pinStatusEnv(t, repo, home)

	var stdout, stderr bytes.Buffer
	if code := Run([]string{"--global"}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr: %s)", code, stderr.String())
	}
	out := stdout.String()
	if !strings.HasPrefix(out, "GLOBAL — not installed by omakase") {
		t.Errorf("--global missing header; got:\n%s", out)
	}
	for _, row := range []string{"~/.claude/CLAUDE.md", "~/.copilot/skills/coskill/"} {
		if !strings.Contains(out, row) {
			t.Errorf("--global missing row %q; got:\n%s", row, out)
		}
	}
}

// ------------------------------------------------------------ default page extras

// --all is the full file inventory — the pre-layers page, kept for auditing
// what is on disk. The default page must NOT carry the per-file walls.
func TestStatusAllShowsFullInventory(t *testing.T) {
	repo, home := buildStatusFixture(t)
	pinStatusEnv(t, repo, home)

	var all, def bytes.Buffer
	if code := Run([]string{"--all"}, &all, &all); code != 0 {
		t.Fatalf("--all exit = %d", code)
	}
	if code := Run(nil, &def, &def); code != 0 {
		t.Fatalf("default exit = %d", code)
	}
	for _, want := range []string{"THE PROJECT'S HARNESS", "INJECTED — placed by omakase init from", "normal.txt"} {
		if !strings.Contains(all.String(), want) {
			t.Errorf("--all page missing %q:\n%s", want, all.String())
		}
	}
	// The shared "from" fact appears once, in the group header — never per row.
	if n := strings.Count(all.String(), "acme/harness"); n > 2 {
		t.Errorf("source repeated %d times on --all, want once in the header (plus the identity line):\n%s", n, all.String())
	}
	for _, gone := range []string{"THE PROJECT'S HARNESS", "INJECTED — placed by"} {
		if strings.Contains(def.String(), gone) {
			t.Errorf("default page still carries the inventory wall %q", gone)
		}
	}
}

// A placed file missing from the checkout earns a NEEDS ATTENTION row on the
// default page; a healthy overlay earns none.
func TestStatusDefaultPageNeedsAttention(t *testing.T) {
	repo, home := buildStatusFixture(t)
	pinStatusEnv(t, repo, home)

	var healthy bytes.Buffer
	Run(nil, &healthy, &healthy)
	if strings.Contains(healthy.String(), "NEEDS ATTENTION") {
		t.Errorf("healthy overlay shows NEEDS ATTENTION:\n%s", healthy.String())
	}

	if err := os.Remove(filepath.Join(repo.Root, "normal.txt")); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	Run(nil, &out, &out)
	if !strings.Contains(out.String(), "NEEDS ATTENTION") ||
		!strings.Contains(out.String(), "MISSING — omakase init restores") {
		t.Errorf("missing placed file not surfaced:\n%s", out.String())
	}
}

// Untracked agent config gets one count line pointing at --all, not a wall.
func TestStatusDefaultPageUnmanagedLine(t *testing.T) {
	repo, home := buildStatusFixture(t)
	pinStatusEnv(t, repo, home)
	writeFile(t, repo.Root, ".claude/rules/local-tweak.md", "mine\n")

	var out bytes.Buffer
	Run(nil, &out, &out)
	if !strings.Contains(out.String(), "yours, unmanaged: 1 untracked agent-config file(s) only in this clone (list: omakase status --all)") {
		t.Errorf("unmanaged count line missing:\n%s", out.String())
	}
	// The rule still shows in the LAYERS table (it loads — that is the
	// point); what must be gone is the old inventory-style enumeration group.
	if strings.Contains(out.String(), "YOURS, UNMANAGED") {
		t.Errorf("default page enumerates unmanaged files inventory-style:\n%s", out.String())
	}
}

// --show prints a layer in full by unique fragment, and refuses ambiguity.
func TestStatusShowLayer(t *testing.T) {
	repo, home := buildStatusFixture(t)
	pinStatusEnv(t, repo, home)

	var out, errOut bytes.Buffer
	if code := Run([]string{"--show", "team"}, &out, &errOut); code != 0 {
		t.Fatalf("--show exit = %d, stderr %s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "team rule") || !strings.Contains(out.String(), ".claude/rules/team.md") {
		t.Errorf("--show output = %q", out.String())
	}
	out.Reset()
	errOut.Reset()
	if code := Run([]string{"--show", "no-such-layer-xyz"}, &out, &errOut); code != 2 {
		t.Fatalf("--show miss exit = %d, want 2", code)
	}
}
