package gate

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Yuncun/omakase-harness/internal/state"
)

// --- fixtures -------------------------------------------------------------

// newRepo makes a temp git repo with one empty commit and returns its root and
// its shared-zone omk dir (.git/omakase). OMAKASE_NOW is pinned for the whole
// test run so ledger epochs are deterministic.
func newRepo(t *testing.T) (root, omk string) {
	t.Helper()
	root = t.TempDir()
	runGit(t, root, "init", "-q")
	runGit(t, root, "config", "user.email", "t@t")
	runGit(t, root, "config", "user.name", "t")
	runGit(t, root, "config", "commit.gpgsign", "false")
	runGit(t, root, "commit", "-q", "--allow-empty", "-m", "init")
	omk = filepath.Join(root, ".git", "omakase")
	if err := os.MkdirAll(omk, 0o755); err != nil {
		t.Fatal(err)
	}
	return root, omk
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	// Background maintenance off: a detached auto-gc can still be writing
	// .git/objects when TempDir cleanup deletes the tree (flaked in CI).
	cmd := exec.Command("git", append(append([]string{"-c", "gc.auto=0", "-c", "maintenance.auto=false"}, "-C", dir), args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

func writeSnapshotManifest(t *testing.T, omk, content string) {
	t.Helper()
	dir := filepath.Join(omk, "payload-snapshot")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "omakase.manifest"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// run drives RunHook with the given snapshot manifest and returns the exit
// code, combined stdout, and the ledger contents.
func run(t *testing.T, root, omk, hook, manifest string, env map[string]string) (int, string, string) {
	return runIn(t, root, omk, hook, manifest, "", env)
}

// runIn is run with hook stdin content — on pre-push, the ref lines git
// feeds the hook.
func runIn(t *testing.T, root, omk, hook, manifest, stdin string, env map[string]string) (int, string, string) {
	t.Helper()
	writeSnapshotManifest(t, omk, manifest)
	for k, v := range env {
		t.Setenv(k, v)
	}
	var out bytes.Buffer
	code := RunHook(hook, root, omk, strings.NewReader(stdin), &out, &out)
	led, _ := os.ReadFile(filepath.Join(omk, "ledger.tsv"))
	return code, out.String(), string(led)
}

// pushStdin builds the pre-push ref line git would feed for pushing the
// repo's HEAD over the given remote-side sha ("0000…" for a new ref).
func pushStdin(t *testing.T, root, remoteSHA string) string {
	t.Helper()
	return "refs/heads/main " + revParse(t, root, "HEAD") + " refs/heads/main " + remoteSHA + "\n"
}

func revParse(t *testing.T, root, ref string) string {
	t.Helper()
	out, err := exec.Command("git", "-C", root, "rev-parse", ref).Output()
	if err != nil {
		t.Fatalf("rev-parse %s: %v", ref, err)
	}
	return strings.TrimRight(string(out), "\n")
}

func ledgerRows(led string) [][]string {
	var rows [][]string
	for _, line := range strings.Split(strings.TrimRight(led, "\n"), "\n") {
		if line == "" {
			continue
		}
		rows = append(rows, strings.Split(line, "\t"))
	}
	return rows
}

func hasRow(led, name, verdict string) bool {
	for _, r := range ledgerRows(led) {
		if len(r) >= 3 && r[1] == name && r[2] == verdict {
			return true
		}
	}
	return false
}

// --- Parse / validation ---------------------------------------------------

func TestParse(t *testing.T) {
	cases := []struct {
		name     string
		manifest string
		want     []Gate
		wantAdv  []Advisory
		wantErr  string
	}{
		{
			name: "full block set with header",
			manifest: "name: starter\nversion: 0.2.0\n\n" +
				"gate: block-marker\n  hook: pre-commit\n  run: .omakase/gates/block-marker.sh\n\n" +
				"gate: go-test\n  hook: pre-push\n  run: go test ./...\n  glob: *.go go.mod go.sum\n  cacheable: true\n",
			want: []Gate{
				{Name: "block-marker", Hook: "pre-commit", Run: ".omakase/gates/block-marker.sh"},
				{Name: "go-test", Hook: "pre-push", Run: "go test ./...", Glob: []string{"*.go", "go.mod", "go.sum"}, Cacheable: true},
			},
		},
		{name: "unknown key in block", manifest: "gate: g\n  hook: pre-commit\n  run: x\n  bogus: 1\n", wantErr: "unknown key"},
		{name: "duplicate name", manifest: "gate: g\n  hook: pre-commit\n  run: x\ngate: g\n  hook: pre-push\n  run: y\n", wantErr: "duplicate"},
		{name: "bad hook stage", manifest: "gate: g\n  hook: post-merge\n  run: x\n", wantErr: "must be pre-commit or pre-push"},
		{name: "missing hook", manifest: "gate: g\n  run: x\n", wantErr: "missing required key hook"},
		{name: "missing run", manifest: "gate: g\n  hook: pre-commit\n", wantErr: "missing required key run"},
		{name: "bad gate name", manifest: "gate: bad name!\n  hook: pre-commit\n  run: x\n", wantErr: "not [A-Za-z0-9._-]+"},
		{name: "bad cacheable value", manifest: "gate: g\n  hook: pre-commit\n  run: x\n  cacheable: yes\n", wantErr: "cacheable must be true or false"},
		{name: "header only, no gates", manifest: "name: x\nversion: 1\n", want: nil},
		{
			name:     "purpose key",
			manifest: "gate: go-test\n  hook: pre-push\n  run: go test ./...\n  purpose: tests green before push\n",
			want:     []Gate{{Name: "go-test", Hook: "pre-push", Run: "go test ./...", Purpose: "tests green before push"}},
		},
		{
			name: "advisory blocks alongside gates",
			manifest: "name: h\nversion: 1\n\n" +
				"gate: g\n  hook: pre-commit\n  run: true\n\n" +
				"advisory: branch-freshness\n  run: .omakase/advisories/branch-freshness.sh\n  purpose: warn when the branch is behind\n" +
				"advisory: detached-head\n  run: .omakase/advisories/detached-head.sh\n",
			want: []Gate{{Name: "g", Hook: "pre-commit", Run: "true"}},
			wantAdv: []Advisory{
				{Name: "branch-freshness", Run: ".omakase/advisories/branch-freshness.sh", Purpose: "warn when the branch is behind"},
				{Name: "detached-head", Run: ".omakase/advisories/detached-head.sh"},
			},
		},
		{name: "advisory missing run", manifest: "advisory: a\n  purpose: x\n", wantErr: "missing required key run"},
		{name: "advisory rejects hook key", manifest: "advisory: a\n  hook: session-start\n  run: true\n", wantErr: "unknown key"},
		{name: "advisory rejects glob key", manifest: "advisory: a\n  run: true\n  glob: *.go\n", wantErr: "unknown key"},
		{name: "advisory bad name", manifest: "advisory: bad name!\n  run: true\n", wantErr: "not [A-Za-z0-9._-]+"},
		{name: "advisory duplicate name", manifest: "advisory: a\n  run: true\nadvisory: a\n  run: false\n", wantErr: "duplicate"},
		{name: "gate and advisory share the namespace", manifest: "gate: a\n  hook: pre-commit\n  run: true\nadvisory: a\n  run: true\n", wantErr: "duplicate"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, adv, err := Parse([]byte(tc.manifest))
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("want error containing %q, got %v", tc.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("gates mismatch\n got: %+v\nwant: %+v", got, tc.want)
			}
			if !reflect.DeepEqual(adv, tc.wantAdv) {
				t.Fatalf("advisories mismatch\n got: %+v\nwant: %+v", adv, tc.wantAdv)
			}
		})
	}
}

func TestValidateRunnable(t *testing.T) {
	payload := t.TempDir()
	if err := os.MkdirAll(filepath.Join(payload, ".omakase", "gates"), 0o755); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(payload, ".omakase", "gates", "ok.sh")
	if err := os.WriteFile(exe, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	nonexe := filepath.Join(payload, ".omakase", "gates", "noexec.sh")
	if err := os.WriteFile(nonexe, []byte("#!/bin/sh\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name    string
		run     string
		wantErr string
	}{
		{name: "payload path present + executable", run: ".omakase/gates/ok.sh"},
		{name: "non-payload command accepted", run: "go test ./..."},
		{name: "payload path missing", run: ".omakase/gates/missing.sh", wantErr: "does not ship"},
		{name: "payload path not executable", run: ".omakase/gates/noexec.sh", wantErr: "not executable"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.wantErr == "not executable" && runtime.GOOS == "windows" {
				t.Skip("the exec-bit refusal is Unix-only — NTFS has no exec bits")
			}
			err := ValidateRunnable([]Gate{{Name: "g", Hook: "pre-commit", Run: tc.run}}, nil, payload)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("want nil, got %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("want error containing %q, got %v", tc.wantErr, err)
			}
		})
	}

	// An advisory's run: is held to the same "nothing runs undeclared" check.
	err := ValidateRunnable(nil, []Advisory{{Name: "a", Run: ".omakase/advisories/missing.sh"}}, payload)
	if err == nil || !strings.Contains(err.Error(), "does not ship") || !strings.Contains(err.Error(), `advisory "a"`) {
		t.Fatalf("advisory missing script: want does-not-ship error naming the advisory, got %v", err)
	}
	if err := ValidateRunnable(nil, []Advisory{{Name: "a", Run: "git fetch --dry-run"}}, payload); err != nil {
		t.Fatalf("advisory non-payload command: want nil, got %v", err)
	}
}

// --- RunAdvisories ---------------------------------------------------------

// Advisories run in manifest order from the repo root; stdout lines are
// relayed behind an omakase[<name>]: prefix (session-start text is always
// attributed), stderr is discarded (a broken check must not splash a raw
// shell error into every session), and a non-zero exit changes nothing.
func TestRunAdvisoriesRelayAndOrder(t *testing.T) {
	root, omk := newRepo(t)
	writeSnapshotManifest(t, omk,
		"name: t\n\nadvisory: first\n  run: echo behind by 3\nadvisory: fails\n  run: echo trouble >&2; exit 7\nadvisory: last\n  run: echo still-ran\n")
	var out bytes.Buffer
	RunAdvisories(root, omk, &out)
	if !strings.Contains(out.String(), "omakase[first]: behind by 3") ||
		!strings.Contains(out.String(), "omakase[last]: still-ran") {
		t.Fatalf("stdout = %q, want both advisory lines with name prefixes", out.String())
	}
	if strings.Index(out.String(), "behind by 3") > strings.Index(out.String(), "still-ran") {
		t.Errorf("stdout = %q, want manifest order", out.String())
	}
	if strings.Contains(out.String(), "trouble") {
		t.Errorf("stdout = %q, a failing advisory's stderr must be discarded", out.String())
	}
}

// The child shell starts from the repo root, whatever directory the hook fired
// from.
func TestRunAdvisoriesRunFromRoot(t *testing.T) {
	root, omk := newRepo(t)
	writeSnapshotManifest(t, omk, "advisory: mark\n  run: touch ran-here\n")
	var out bytes.Buffer
	RunAdvisories(root, omk, &out)
	if _, err := os.Stat(filepath.Join(root, "ran-here")); err != nil {
		t.Fatalf("advisory did not run from the repo root: %v", err)
	}
}

// Advisories are fail-open where gates are fail-closed: a missing or corrupt
// snapshot manifest means silence, never noise or a block at session start.
func TestRunAdvisoriesFailOpen(t *testing.T) {
	root, omk := newRepo(t)
	var out bytes.Buffer
	RunAdvisories(root, omk, &out) // no manifest at all
	writeSnapshotManifest(t, omk, "advisory: broken\n")
	RunAdvisories(root, omk, &out) // corrupt manifest
	if out.String() != "" {
		t.Fatalf("want silence, got stdout=%q", out.String())
	}
}

// A hung advisory is killed at advisoryTimeout, and a backgrounded grandchild
// holding the output pipe is cut loose after advisoryGrace — a session start
// pays one check's budget at most, never "until the network comes back".
func TestRunAdvisoriesTimeoutAndGrace(t *testing.T) {
	root, omk := newRepo(t)
	oldT, oldG := advisoryTimeout, advisoryGrace
	advisoryTimeout, advisoryGrace = 300*time.Millisecond, 300*time.Millisecond
	defer func() { advisoryTimeout, advisoryGrace = oldT, oldG }()

	writeSnapshotManifest(t, omk,
		"advisory: hangs\n  run: echo before-hang; sleep 30\nadvisory: bg\n  run: sleep 30 & echo spoke\nadvisory: after\n  run: echo still-reached\n")
	start := time.Now()
	var out bytes.Buffer
	RunAdvisories(root, omk, &out)
	if el := time.Since(start); el > 5*time.Second {
		t.Fatalf("RunAdvisories took %v — a hung advisory blocked the session start", el)
	}
	if !strings.Contains(out.String(), "omakase[hangs]: before-hang") {
		t.Errorf("stdout = %q, want the hung advisory's output before the kill", out.String())
	}
	if !strings.Contains(out.String(), "omakase[bg]: spoke") ||
		!strings.Contains(out.String(), "omakase[after]: still-reached") {
		t.Errorf("stdout = %q, want later advisories to still run", out.String())
	}
}

// One advisory's output is capped: the head is kept, the excess dropped, and
// the drop is announced rather than silent.
func TestRunAdvisoriesOutputCap(t *testing.T) {
	root, omk := newRepo(t)
	oldC := advisoryOutputCap
	advisoryOutputCap = 64
	defer func() { advisoryOutputCap = oldC }()

	writeSnapshotManifest(t, omk, "advisory: chatty\n  run: yes flood | head -c 100000\nadvisory: next\n  run: echo unaffected\n")
	var out bytes.Buffer
	RunAdvisories(root, omk, &out)
	if !strings.Contains(out.String(), "omakase[chatty]: (output capped)") {
		t.Errorf("stdout = %q, want the cap announced", out.String())
	}
	if len(out.String()) > 4096 {
		t.Errorf("relayed %d bytes — the cap did not hold", len(out.String()))
	}
	if !strings.Contains(out.String(), "omakase[next]: unaffected") {
		t.Errorf("stdout = %q, want the next advisory unaffected", out.String())
	}
}

// --- RunHook: the core primitive semantics --------------------------------

func TestRunHook_PassAndFailRows(t *testing.T) {
	t.Setenv("OMAKASE_NOW", "1700000000")
	root, omk := newRepo(t)

	code, _, led := run(t, root, omk, "pre-commit", "gate: p\n  hook: pre-commit\n  run: true\n", nil)
	if code != 0 {
		t.Fatalf("passing check: want exit 0, got %d", code)
	}
	if !hasRow(led, "p", "pass") {
		t.Fatalf("no pass row: %q", led)
	}
	rows := ledgerRows(led)
	if len(rows[0]) != 4 {
		t.Fatalf("row must have 4 fields, got %d: %q", len(rows[0]), rows[0])
	}
	head := headSHA(root)
	if rows[0][3] != head {
		t.Fatalf("4th field must be HEAD sha %q, got %q", head, rows[0][3])
	}
	if rows[0][0] != "1700000000" {
		t.Fatalf("epoch must honor OMAKASE_NOW, got %q", rows[0][0])
	}
}

func TestRunHook_ExitCodePassthrough(t *testing.T) {
	root, omk := newRepo(t)
	code, _, led := run(t, root, omk, "pre-commit", "gate: f\n  hook: pre-commit\n  run: exit 7\n", nil)
	if code != 7 {
		t.Fatalf("want the check's exit code 7 passed through, got %d", code)
	}
	if !hasRow(led, "f", "fail") {
		t.Fatalf("failing check must record a fail row: %q", led)
	}
}

func TestRunHook_RunsEveryGateReturnsFirstFailure(t *testing.T) {
	root, omk := newRepo(t)
	// The first gate fails; every gate still runs (the second writes a marker),
	// and the stage returns the first failure's code.
	marker := filepath.Join(t.TempDir(), "ran")
	// Slash form inside the sh command: sh treats backslashes as escapes,
	// so a Windows-form path never reaches the gate intact.
	shMarker := filepath.ToSlash(marker)
	man := "gate: a\n  hook: pre-commit\n  run: exit 3\n" +
		"gate: b\n  hook: pre-commit\n  run: touch " + shMarker + "\n"
	code, _, _ := run(t, root, omk, "pre-commit", man, nil)
	if code != 3 {
		t.Fatalf("want the first failure's code 3, got %d", code)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("the second gate did not run — every declared gate must run")
	}
}

func TestRunHook_AuditedSkipVar(t *testing.T) {
	root, omk := newRepo(t)
	code, out, led := run(t, root, omk, "pre-commit",
		"gate: failgate\n  hook: pre-commit\n  run: exit 1\n",
		map[string]string{"OMAKASE_SKIP_FAILGATE": "1"})
	if code != 0 {
		t.Fatalf("skip var must bypass a blocking gate, got %d", code)
	}
	if !strings.Contains(out, "OMAKASE_SKIP_FAILGATE") {
		t.Fatalf("skip must be audited on stdout: %q", out)
	}
	if strings.Contains(led, "failgate") {
		t.Fatalf("a skipped gate records no row: %q", led)
	}
}

func TestRunHook_DottedNameSkipVar(t *testing.T) {
	root, omk := newRepo(t)
	code, _, _ := run(t, root, omk, "pre-commit",
		"gate: lint.fast\n  hook: pre-commit\n  run: exit 1\n",
		map[string]string{"OMAKASE_SKIP_LINT_FAST": "1"})
	if code != 0 {
		t.Fatalf("dotted name: OMAKASE_SKIP_LINT_FAST must bypass lint.fast, got %d", code)
	}
}

func TestRunHook_SkipAllGates(t *testing.T) {
	root, omk := newRepo(t)
	code, out, led := run(t, root, omk, "pre-commit",
		"gate: a\n  hook: pre-commit\n  run: exit 1\ngate: b\n  hook: pre-commit\n  run: exit 1\n",
		map[string]string{"OMAKASE_SKIP_GATES": "1"})
	if code != 0 {
		t.Fatalf("OMAKASE_SKIP_GATES must skip the whole stage, got %d", code)
	}
	if !strings.Contains(out, "OMAKASE_SKIP_GATES") {
		t.Fatalf("skip-all must be audited on stdout: %q", out)
	}
	if led != "" {
		t.Fatalf("skip-all records nothing: %q", led)
	}
}

func TestRunHook_DisabledGatesFile(t *testing.T) {
	root, omk := newRepo(t)
	if err := os.WriteFile(filepath.Join(omk, "disabled-gates"), []byte("noisy\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out, _ := run(t, root, omk, "pre-commit", "gate: noisy\n  hook: pre-commit\n  run: exit 7\n", nil)
	if code != 0 {
		t.Fatalf("a disabled gate must skip visibly, got %d", code)
	}
	if !strings.Contains(out, "disabled via omakase") {
		t.Fatalf("disabled skip must say so: %q", out)
	}
	// An unlisted gate still runs.
	code, _, _ = run(t, root, omk, "pre-push", "gate: other\n  hook: pre-push\n  run: exit 7\n", nil)
	if code != 7 {
		t.Fatalf("an unlisted gate still runs, got %d", code)
	}
}

func TestRunHook_OnlyForStage(t *testing.T) {
	root, omk := newRepo(t)
	// A pre-push gate must not run at pre-commit.
	code, _, led := run(t, root, omk, "pre-commit", "gate: pp\n  hook: pre-push\n  run: exit 1\n", nil)
	if code != 0 || led != "" {
		t.Fatalf("a pre-push gate must not run at pre-commit (code=%d led=%q)", code, led)
	}
}

// --- glob scope -----------------------------------------------------------

// withRemote gives root an origin remote whose main branch is pushed, so
// origin/HEAD resolves a base ref for glob ranges.
func withRemote(t *testing.T, root string) {
	t.Helper()
	remote := t.TempDir()
	runGit(t, remote, "init", "-q", "--bare")
	runGit(t, root, "branch", "-M", "main")
	runGit(t, root, "remote", "add", "origin", remote)
	runGit(t, root, "push", "-q", "-u", "origin", "main")
}

func commitFile(t *testing.T, root, rel, content string) {
	t.Helper()
	full := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, root, "add", rel)
	runGit(t, root, "commit", "-q", "-m", "add "+rel)
}

func TestRunHook_GlobMatchRuns(t *testing.T) {
	root, omk := newRepo(t)
	withRemote(t, root)
	commitFile(t, root, "src/app.txt", "a\n")
	code, _, led := runIn(t, root, omk, "pre-push", "gate: g1\n  hook: pre-push\n  run: true\n  glob: src/*\n", pushStdin(t, root, revParse(t, root, "origin/main")), nil)
	if code != 0 || !hasRow(led, "g1", "pass") {
		t.Fatalf("glob match must run (code=%d led=%q)", code, led)
	}
}

func TestRunHook_GlobMissSkips(t *testing.T) {
	root, omk := newRepo(t)
	withRemote(t, root)
	commitFile(t, root, "src/app.txt", "a\n")
	code, out, led := runIn(t, root, omk, "pre-push", "gate: g2\n  hook: pre-push\n  run: false\n  glob: docs/*\n", pushStdin(t, root, revParse(t, root, "origin/main")), nil)
	if code != 0 {
		t.Fatalf("glob miss must skip (exit 0), got %d", code)
	}
	if hasRow(led, "g2", "fail") {
		t.Fatalf("a skipped gate records nothing: %q", led)
	}
	if !strings.Contains(out, "no pushed file matches") {
		t.Fatalf("glob miss must say so: %q", out)
	}
}

func TestRunHook_GlobSpansDirectories(t *testing.T) {
	root, omk := newRepo(t)
	withRemote(t, root)
	commitFile(t, root, "internal/gate/gate.go", "package gate\n")
	// A single `*` must span `/` (the sh case dialect): *.go matches internal/gate/gate.go.
	code, _, led := runIn(t, root, omk, "pre-push", "gate: gt\n  hook: pre-push\n  run: false\n  glob: *.go go.mod go.sum\n", pushStdin(t, root, revParse(t, root, "origin/main")), nil)
	if code == 0 {
		t.Fatalf("*.go must match a nested .go file (glob should span directories)")
	}
	if !hasRow(led, "gt", "fail") {
		t.Fatalf("the matched gate must have run: %q", led)
	}
}

func TestRunHook_MultiPatternSecond(t *testing.T) {
	root, omk := newRepo(t)
	withRemote(t, root)
	commitFile(t, root, "lib/util.txt", "y\n")
	code, _, _ := runIn(t, root, omk, "pre-push", "gate: mg\n  hook: pre-push\n  run: false\n  glob: src/* lib/*\n", pushStdin(t, root, revParse(t, root, "origin/main")), nil)
	if code == 0 {
		t.Fatalf("a change under the second pattern (lib/*) must trigger the gate")
	}
}

func TestRunHook_NoBaseRunsUnscoped(t *testing.T) {
	root, omk := newRepo(t)
	commitFile(t, root, "src/app.txt", "a\n") // no remote → no resolvable base
	// A brand-new ref (zero remote sha) with no resolvable base ref: the
	// push cannot be scoped, so the gate must run.
	code, out, led := runIn(t, root, omk, "pre-push", "gate: fo\n  hook: pre-push\n  run: false\n  glob: src/*\n", pushStdin(t, root, strings.Repeat("0", 40)), nil)
	if code == 0 {
		t.Fatalf("no resolvable base must run unscoped and block, got exit 0")
	}
	if !hasRow(led, "fo", "fail") {
		t.Fatalf("unscoped run must record: %q", led)
	}
	if !strings.Contains(out, "cannot scope this push") {
		t.Fatalf("must explain the unscoped run: %q", out)
	}
}

func TestRunHook_TwoDotFallbackUnrelatedHistory(t *testing.T) {
	root, omk := newRepo(t)
	remote := t.TempDir()
	runGit(t, remote, "init", "-q", "--bare")
	runGit(t, root, "branch", "-M", "main")
	runGit(t, root, "remote", "add", "origin", remote)
	commitFile(t, root, "base.txt", "b\n")
	runGit(t, root, "push", "-q", "-u", "origin", "main")
	// An orphan branch: three-dot (merge-base) is fatal on unrelated histories,
	// so the two-dot fallback must still find the in-scope change.
	runGit(t, root, "checkout", "-q", "--orphan", "orphanwork")
	runGit(t, root, "rm", "-rfq", "--cached", ".")
	os.Remove(filepath.Join(root, "base.txt"))
	commitFile(t, root, "src/app.txt", "x\n")
	// Force-pushing the orphan over main: three-dot (merge-base) is fatal on
	// unrelated histories, so the two-dot fallback must still find the change.
	code, out, _ := runIn(t, root, omk, "pre-push", "gate: td\n  hook: pre-push\n  run: false\n  glob: src/*\n", "refs/heads/orphanwork "+revParse(t, root, "HEAD")+" refs/heads/main "+revParse(t, root, "origin/main")+"\n", nil)
	if code == 0 {
		t.Fatalf("two-dot fallback must find the in-scope change on unrelated histories")
	}
	// The gate must have run because the fallback SCOPED it in — an
	// unscopable "cannot scope" run also exits non-zero and would hide a
	// deleted fallback (mutation-testing finding).
	if strings.Contains(out, "cannot scope") {
		t.Fatalf("gate ran unscoped, not via the two-dot fallback: %q", out)
	}
}

// --- staged / pushed scope (#196, #186) -----------------------------------

// The #196 repro, direction 1: a fresh clone sitting at origin/main stages an
// in-scope file. The old branch-range scope saw an empty range and skipped —
// the gate's first commit after every clone was unguarded.
func TestRunHook_PreCommitScopesByStagedSet(t *testing.T) {
	root, omk := newRepo(t)
	withRemote(t, root) // HEAD == origin/main, the fresh-clone shape
	if err := os.WriteFile(filepath.Join(root, "config.txt"), []byte("v\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, root, "add", "config.txt")
	code, out, led := run(t, root, omk, "pre-commit", "gate: sc\n  hook: pre-commit\n  run: false\n  glob: config.txt\n", nil)
	if code == 0 {
		t.Fatalf("a staged in-scope file must run the gate (and block), got exit 0")
	}
	// It must have run because the staged set MATCHED — a broken scoper
	// falls back to an unscoped run and also blocks (mutation finding).
	if strings.Contains(out, "cannot read the staged files") {
		t.Fatalf("gate ran via the unscoped fallback, not the staged set: %q", out)
	}
	if !hasRow(led, "sc", "fail") {
		t.Fatalf("the gate must have run: %q", led)
	}
}

// Direction 2: an in-scope path in the BRANCH's history but not in the staged
// set must not fire the gate — the old scope kept matching it on every later
// commit.
func TestRunHook_PreCommitIgnoresBranchHistory(t *testing.T) {
	root, omk := newRepo(t)
	withRemote(t, root)
	commitFile(t, root, "config.txt", "v\n") // ahead of origin/main now
	if err := os.WriteFile(filepath.Join(root, "unrelated.txt"), []byte("u\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, root, "add", "unrelated.txt")
	code, out, _ := run(t, root, omk, "pre-commit", "gate: sh2\n  hook: pre-commit\n  run: false\n  glob: config.txt\n", nil)
	if code != 0 {
		t.Fatalf("nothing staged matches - must skip, got exit %d (%s)", code, out)
	}
	if !strings.Contains(out, "no staged file matches") {
		t.Fatalf("skip must say so: %q", out)
	}
}

// A push of a NEW ref (zero remote sha) has no range of its own; it falls
// back to the resolved base ref and scopes against that.
func TestRunHook_PrePushNewRefFallsBackToBaseRef(t *testing.T) {
	root, omk := newRepo(t)
	withRemote(t, root)
	runGit(t, root, "checkout", "-q", "-b", "feature")
	commitFile(t, root, "docs/readme.txt", "d\n")
	stdin := "refs/heads/feature " + revParse(t, root, "HEAD") + " refs/heads/feature " + strings.Repeat("0", 40) + "\n"
	code, out, _ := runIn(t, root, omk, "pre-push", "gate: nr\n  hook: pre-push\n  run: false\n  glob: src/*\n", stdin, nil)
	if code != 0 {
		t.Fatalf("new-ref push touching only docs/ must skip a src/* gate, got exit %d (%s)", code, out)
	}
	if !strings.Contains(out, "no pushed file matches") {
		t.Fatalf("skip must say so: %q", out)
	}
}

// A deletion-only push has no range to certify a skip against: run.
func TestRunHook_PrePushDeletionRunsUnscoped(t *testing.T) {
	root, omk := newRepo(t)
	withRemote(t, root)
	stdin := "(delete) " + strings.Repeat("0", 40) + " refs/heads/gone " + revParse(t, root, "origin/main") + "\n"
	code, out, _ := runIn(t, root, omk, "pre-push", "gate: dl\n  hook: pre-push\n  run: false\n  glob: src/*\n", stdin, nil)
	if code == 0 {
		t.Fatalf("deletion-only push cannot be scoped - the gate must run, got exit 0")
	}
	if !strings.Contains(out, "cannot scope this push") {
		t.Fatalf("must explain the unscoped run: %q", out)
	}
}

// The ref lines are both the scope source and input the checks may read:
// every gate in the stage must see the FULL stdin, not whatever the previous
// child left behind.
func TestRunHook_PrePushStdinSharedAcrossGates(t *testing.T) {
	root, omk := newRepo(t)
	withRemote(t, root)
	commitFile(t, root, "src/app.txt", "a\n")
	manifest := "gate: r1\n  hook: pre-push\n  run: grep -q refs/heads/main\n  glob: src/*\n" +
		"gate: r2\n  hook: pre-push\n  run: grep -q refs/heads/main\n  glob: src/*\n"
	code, _, led := runIn(t, root, omk, "pre-push", manifest, pushStdin(t, root, revParse(t, root, "origin/main")), nil)
	if code != 0 {
		t.Fatalf("both gates must see the ref lines on stdin, got exit %d (ledger %q)", code, led)
	}
	if !hasRow(led, "r1", "pass") || !hasRow(led, "r2", "pass") {
		t.Fatalf("both gates must have run and passed: %q", led)
	}
}

// --- cache ----------------------------------------------------------------

func TestRunHook_CacheHitSkips(t *testing.T) {
	root, omk := newRepo(t)
	marker := filepath.Join(t.TempDir(), "ran")
	// Slash form inside the sh command: sh treats backslashes as escapes,
	// so a Windows-form path never reaches the gate intact.
	shMarker := filepath.ToSlash(marker)
	man := "gate: c\n  hook: pre-commit\n  run: printf x >> " + shMarker + "\n  cacheable: true\n"
	// First run executes.
	run(t, root, omk, "pre-commit", man, nil)
	b, _ := os.ReadFile(marker)
	if string(b) != "x" {
		t.Fatalf("first cacheable run must execute, marker=%q", b)
	}
	// Second run at the same HEAD is cached: the check does not run again.
	_, out, _ := run(t, root, omk, "pre-commit", man, nil)
	b, _ = os.ReadFile(marker)
	if string(b) != "x" {
		t.Fatalf("a fresh pass must skip the check, marker=%q", b)
	}
	if !strings.Contains(out, "cached") {
		t.Fatalf("a cache hit must say (cached): %q", out)
	}
	// HEAD moves → the cache is stale → the check runs again.
	commitFile(t, root, "more.txt", "b\n")
	run(t, root, omk, "pre-commit", man, nil)
	b, _ = os.ReadFile(marker)
	if string(b) != "xx" {
		t.Fatalf("a new commit must bust the cache, marker=%q", b)
	}
}

func TestRunHook_FailRowDoesNotCache(t *testing.T) {
	root, omk := newRepo(t)
	marker := filepath.Join(t.TempDir(), "ran")
	// Slash form inside the sh command: sh treats backslashes as escapes,
	// so a Windows-form path never reaches the gate intact.
	shMarker := filepath.ToSlash(marker)
	man := "gate: cf\n  hook: pre-commit\n  run: printf x >> " + shMarker + "; exit 1\n  cacheable: true\n"
	run(t, root, omk, "pre-commit", man, nil)
	run(t, root, omk, "pre-commit", man, nil)
	b, _ := os.ReadFile(marker)
	if string(b) != "xx" {
		t.Fatalf("a fail row must not satisfy the cache (want re-run), marker=%q", b)
	}
}

// --- record ---------------------------------------------------------------

func TestRecord(t *testing.T) {
	t.Setenv("OMAKASE_NOW", "1700000001")
	root, omk := newRepo(t)
	if err := Record(root, omk, "review"); err != nil {
		t.Fatalf("Record: %v", err)
	}
	led, _ := os.ReadFile(filepath.Join(omk, "ledger.tsv"))
	if !hasRow(string(led), "review", "pass") {
		t.Fatalf("Record must write a pass row: %q", led)
	}
	// A subsequent cacheable run at the same HEAD skips (deferment).
	marker := filepath.Join(t.TempDir(), "ran")
	// Slash form inside the sh command: sh treats backslashes as escapes,
	// so a Windows-form path never reaches the gate intact.
	shMarker := filepath.ToSlash(marker)
	man := "gate: review\n  hook: pre-push\n  run: printf x >> " + shMarker + "; exit 1\n  cacheable: true\n"
	code, _, _ := run(t, root, omk, "pre-push", man, nil)
	if code != 0 {
		t.Fatalf("after Record the same HEAD must be allowed, got %d", code)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatalf("the deferred check must not have run after Record")
	}
}

func TestRecordFailsLoud(t *testing.T) {
	root, omk := newRepo(t)
	// Plant a FILE where the omakase dir must be, so the ledger cannot be written.
	if err := os.RemoveAll(omk); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(omk, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Record(root, omk, "review"); err == nil {
		t.Fatalf("Record must fail loud on a write error")
	}
}

// --- hostile names --------------------------------------------------------

func TestLedgerSanitizesHostileFields(t *testing.T) {
	root, omk := newRepo(t)
	// A tab/newline in the recorded name (e.g. via `omakase record`) must not
	// shift the TSV columns.
	if err := Record(root, omk, "tab\tname\nsecond"); err != nil {
		t.Fatalf("Record: %v", err)
	}
	led, _ := os.ReadFile(filepath.Join(omk, "ledger.tsv"))
	rows := ledgerRows(string(led))
	if len(rows) != 1 || len(rows[0]) != 4 {
		t.Fatalf("a hostile name must keep the row at 4 fields, got %v", rows)
	}
}

// --- ledger byte-compatibility with the readers ---------------------------

func TestLedgerShapeMatchesReaders(t *testing.T) {
	t.Setenv("OMAKASE_NOW", "1700000000")
	root, omk := newRepo(t)
	run(t, root, omk, "pre-commit", "gate: a\n  hook: pre-commit\n  run: true\n", nil)
	run(t, root, omk, "pre-commit", "gate: b\n  hook: pre-commit\n  run: exit 1\n", nil)

	led, _ := os.ReadFile(filepath.Join(omk, "ledger.tsv"))
	for _, r := range ledgerRows(string(led)) {
		if len(r) != 4 {
			t.Fatalf("every module-written row must be 4 fields: %v", r)
		}
		if r[2] != "pass" && r[2] != "fail" {
			t.Fatalf("verdict must be pass|fail: %q", r[2])
		}
	}
	// The bytes must be exactly epoch\tname\tverdict\tsha\n per row.
	head := headSHA(root)
	want := "1700000000\ta\tpass\t" + head + "\n1700000000\tb\tfail\t" + head + "\n"
	if string(led) != want {
		t.Fatalf("ledger bytes not in canonical shape\n got: %q\nwant: %q", led, want)
	}

	// The existing ledger reader (shared with probe.RunSummary and the
	// statusline) must parse the module-written rows unchanged.
	verds := state.LatestVerdicts(filepath.Join(omk, "ledger.tsv"))
	if v, ok := verds["a"]; !ok || v.Verdict != "pass" {
		t.Fatalf("state.LatestVerdicts must read gate a as pass, got %+v (ok=%v)", v, ok)
	}
	if v, ok := verds["b"]; !ok || v.Verdict != "fail" {
		t.Fatalf("state.LatestVerdicts must read gate b as fail, got %+v (ok=%v)", v, ok)
	}
}

// --- gate-less current harness ---------------------------------------------

// A migrated harness that genuinely declares zero gates — a manifest present,
// no gate blocks, and NO lefthook marker — is not stale and still passes.
func TestRunHook_GatelessCurrentHarnessPasses(t *testing.T) {
	root, omk := newRepo(t)
	writeSnapshotManifest(t, omk, "name: x\nversion: 1\n")
	var out bytes.Buffer
	code := RunHook("pre-commit", root, omk, strings.NewReader(""), &out, &out)
	if code != 0 {
		t.Fatalf("a gate-less current harness must pass, got exit %d (%q)", code, out.String())
	}
}

// --- skip-var name folding ------------------------------------------------

// Every shipped gate uses a hyphenated name (block-marker, go-test, go-checks),
// so the '-'→'_' fold in skipVar must be exercised, not just the '.' case.
func TestRunHook_HyphenatedNameSkipVar(t *testing.T) {
	root, omk := newRepo(t)
	code, _, _ := run(t, root, omk, "pre-commit",
		"gate: block-marker\n  hook: pre-commit\n  run: exit 1\n",
		map[string]string{"OMAKASE_SKIP_BLOCK_MARKER": "1"})
	if code != 0 {
		t.Fatalf("hyphenated name: OMAKASE_SKIP_BLOCK_MARKER must bypass block-marker, got %d", code)
	}
}

// --- non-ASCII glob -------------------------------------------------------

// A UTF-8 glob must match a UTF-8 filename exactly as the deleted sh `case`
// did: byte-wise translation re-encoded the lead byte and silently skipped the
// gate (fail-open). café/* must match café/foo.go and run the gate.
func TestRunHook_GlobMatchesNonASCII(t *testing.T) {
	root, omk := newRepo(t)
	withRemote(t, root)
	commitFile(t, root, "café/foo.go", "package x\n")
	code, _, led := run(t, root, omk, "pre-push", "gate: intl\n  hook: pre-push\n  run: false\n  glob: café/*\n", nil)
	if code == 0 {
		t.Fatalf("a UTF-8 glob (café/*) must match a UTF-8 path (café/foo.go) and run the gate")
	}
	if !hasRow(led, "intl", "fail") {
		t.Fatalf("the matched gate must have run: %q", led)
	}
}

// --- signal-killed step ---------------------------------------------------

// A step killed by a signal surfaces 128+signal (the sh convention), not a
// flattened 1: SIGTERM -> 143.
func TestRunHook_SignalKilledStepIs128Plus(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("128+signal is a POSIX convention; Windows has no process signals")
	}
	root, omk := newRepo(t)
	code, _, _ := run(t, root, omk, "pre-commit", "gate: sig\n  hook: pre-commit\n  run: kill -TERM $$\n", nil)
	if code != 143 {
		t.Fatalf("a SIGTERM-killed step must surface 128+15=143, got %d", code)
	}
}

// --- concurrent ledger appends --------------------------------------------

// The ledger's single-write O_APPEND is the invariant two worktrees committing
// at the same shared ledger rely on: N concurrent appends must land N untorn
// 4-field rows, never an interleaved (<4-field) row a fail-open reader would
// trip on. The deleted omakase-gate.test.sh proved this in sh; here in Go.
func TestLedgerConcurrentAppendsDoNotTear(t *testing.T) {
	omk := t.TempDir()
	const n = 24
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			// Distinct name/sha per goroutine so an interleave is detectable.
			_ = appendRow(omk, "gate"+strconv.Itoa(i), "pass", "sha"+strconv.Itoa(i))
		}(i)
	}
	wg.Wait()
	led, _ := os.ReadFile(filepath.Join(omk, "ledger.tsv"))
	rows := ledgerRows(string(led))
	if len(rows) != n {
		t.Fatalf("want %d rows from %d concurrent appends, got %d", n, n, len(rows))
	}
	for _, r := range rows {
		if len(r) != 4 {
			t.Fatalf("a concurrent append tore a row (not 4 fields): %v", r)
		}
	}
}

// --- LoadName -------------------------------------------------------------

func TestLoadName(t *testing.T) {
	cases := []struct {
		name, manifest, want string
	}{
		{"declared", "name: omakase-harness-harness\nversion: 0.3.0\n\ngate: g\n  hook: pre-commit\n  run: true\n", "omakase-harness-harness"},
		{"no name header", "version: 1\n\ngate: g\n  hook: pre-commit\n  run: true\n", ""},
		{"comment and blank lines first", "# a comment\n\nname: h\n", "h"},
		{"name only after gate blocks is not the header", "gate: g\n  hook: pre-commit\n  run: true\nname: late\n", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			omk := t.TempDir()
			writeSnapshotManifest(t, omk, tc.manifest)
			if got := LoadName(omk); got != tc.want {
				t.Fatalf("LoadName = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestLoadNameMissingManifest(t *testing.T) {
	if got := LoadName(t.TempDir()); got != "" {
		t.Fatalf("LoadName on empty omk = %q, want \"\"", got)
	}
}

// --- heartbeat (#85) ------------------------------------------------------

// While a check runs, $OMK/running carries `name \t pid \t epoch`; when the
// check ends the heartbeat is gone. The check itself copies the file so the
// test can read what existed mid-run.
func TestRunHook_HeartbeatDuringCheck(t *testing.T) {
	t.Setenv("OMAKASE_NOW", "1700000000")
	root, omk := newRepo(t)

	code, _, _ := run(t, root, omk, "pre-commit",
		"gate: hb\n  hook: pre-commit\n  run: cp .git/omakase/running hb-copy\n", nil)
	if code != 0 {
		t.Fatalf("gate failed: %d", code)
	}
	b, err := os.ReadFile(filepath.Join(root, "hb-copy"))
	if err != nil {
		t.Fatalf("heartbeat not present during the check: %v", err)
	}
	f := strings.Split(strings.TrimRight(string(b), "\n"), "\t")
	if len(f) != 3 || f[0] != "hb" || f[2] != "1700000000" {
		t.Fatalf("heartbeat row = %q, want hb \\t <pid> \\t 1700000000", string(b))
	}
	if _, err := strconv.Atoi(f[1]); err != nil {
		t.Fatalf("heartbeat pid %q is not a number", f[1])
	}
	if _, err := os.Stat(filepath.Join(omk, "running")); !os.IsNotExist(err) {
		t.Fatal("heartbeat must be removed after the check ends")
	}
}

// A failing check still removes its heartbeat.
func TestRunHook_HeartbeatRemovedOnFailure(t *testing.T) {
	root, omk := newRepo(t)
	code, _, _ := run(t, root, omk, "pre-commit", "gate: f\n  hook: pre-commit\n  run: exit 3\n", nil)
	if code != 3 {
		t.Fatalf("want exit 3, got %d", code)
	}
	if _, err := os.Stat(filepath.Join(omk, "running")); !os.IsNotExist(err) {
		t.Fatal("heartbeat must be removed after a failing check")
	}
}
