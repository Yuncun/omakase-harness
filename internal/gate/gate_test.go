package gate

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"

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
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
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
	t.Helper()
	writeSnapshotManifest(t, omk, manifest)
	for k, v := range env {
		t.Setenv(k, v)
	}
	var out bytes.Buffer
	code := RunHook(hook, root, omk, strings.NewReader(""), &out, &out)
	led, _ := os.ReadFile(filepath.Join(omk, "ledger.tsv"))
	return code, out.String(), string(led)
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
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Parse([]byte(tc.manifest))
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
			err := ValidateRunnable([]Gate{{Name: "g", Hook: "pre-commit", Run: tc.run}}, payload)
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
	man := "gate: a\n  hook: pre-commit\n  run: exit 3\n" +
		"gate: b\n  hook: pre-commit\n  run: touch " + marker + "\n"
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
	code, _, led := run(t, root, omk, "pre-push", "gate: g1\n  hook: pre-push\n  run: true\n  glob: src/*\n", nil)
	if code != 0 || !hasRow(led, "g1", "pass") {
		t.Fatalf("glob match must run (code=%d led=%q)", code, led)
	}
}

func TestRunHook_GlobMissSkips(t *testing.T) {
	root, omk := newRepo(t)
	withRemote(t, root)
	commitFile(t, root, "src/app.txt", "a\n")
	code, out, led := run(t, root, omk, "pre-push", "gate: g2\n  hook: pre-push\n  run: false\n  glob: docs/*\n", nil)
	if code != 0 {
		t.Fatalf("glob miss must skip (exit 0), got %d", code)
	}
	if hasRow(led, "g2", "fail") {
		t.Fatalf("a skipped gate records nothing: %q", led)
	}
	if !strings.Contains(out, "no changed file matches") {
		t.Fatalf("glob miss must say so: %q", out)
	}
}

func TestRunHook_GlobSpansDirectories(t *testing.T) {
	root, omk := newRepo(t)
	withRemote(t, root)
	commitFile(t, root, "internal/gate/gate.go", "package gate\n")
	// A single `*` must span `/` (the sh case dialect): *.go matches internal/gate/gate.go.
	code, _, led := run(t, root, omk, "pre-push", "gate: gt\n  hook: pre-push\n  run: false\n  glob: *.go go.mod go.sum\n", nil)
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
	code, _, _ := run(t, root, omk, "pre-push", "gate: mg\n  hook: pre-push\n  run: false\n  glob: src/* lib/*\n", nil)
	if code == 0 {
		t.Fatalf("a change under the second pattern (lib/*) must trigger the gate")
	}
}

func TestRunHook_NoBaseRunsUnscoped(t *testing.T) {
	root, omk := newRepo(t)
	commitFile(t, root, "src/app.txt", "a\n") // no remote → no resolvable base
	code, out, led := run(t, root, omk, "pre-push", "gate: fo\n  hook: pre-push\n  run: false\n  glob: src/*\n", nil)
	if code == 0 {
		t.Fatalf("no resolvable base must run unscoped and block, got exit 0")
	}
	if !hasRow(led, "fo", "fail") {
		t.Fatalf("unscoped run must record: %q", led)
	}
	if !strings.Contains(out, "no resolvable base") {
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
	code, _, _ := run(t, root, omk, "pre-push", "gate: td\n  hook: pre-push\n  run: false\n  glob: src/*\n", nil)
	if code == 0 {
		t.Fatalf("two-dot fallback must find the in-scope change on unrelated histories")
	}
}

// --- cache ----------------------------------------------------------------

func TestRunHook_CacheHitSkips(t *testing.T) {
	root, omk := newRepo(t)
	marker := filepath.Join(t.TempDir(), "ran")
	man := "gate: c\n  hook: pre-commit\n  run: printf x >> " + marker + "\n  cacheable: true\n"
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
	man := "gate: cf\n  hook: pre-commit\n  run: printf x >> " + marker + "; exit 1\n  cacheable: true\n"
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
	man := "gate: review\n  hook: pre-push\n  run: printf x >> " + marker + "; exit 1\n  cacheable: true\n"
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

// --- fail closed on a pre-gate-module (lefthook-era) snapshot --------------

// writeLefthookMarker plants payload-snapshot/lefthook-local.yml — the
// fingerprint init left in a repo initialized before the gate module.
func writeLefthookMarker(t *testing.T, omk string) {
	t.Helper()
	dir := filepath.Join(omk, "payload-snapshot")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "lefthook-local.yml"), []byte("pre-commit:\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// An upgraded binary running against a v0.19.x snapshot (lefthook-local.yml in
// the snapshot, a header-only or absent manifest) must FAIL CLOSED with the
// migration pointer, never silently run zero gates (the #72 status-lie).
func TestRunHook_StaleLefthookSnapshotBlocks(t *testing.T) {
	for _, hook := range []string{"pre-commit", "pre-push"} {
		t.Run(hook, func(t *testing.T) {
			root, omk := newRepo(t)
			writeLefthookMarker(t, omk)
			// The shipped starter snapshot: header only, zero gate blocks.
			writeSnapshotManifest(t, omk, "name: x\nversion: 0.19.1\n")
			var out bytes.Buffer
			code := RunHook(hook, root, omk, strings.NewReader(""), &out, &out)
			if code == 0 {
				t.Fatalf("%s: a lefthook-era snapshot must fail closed, got exit 0", hook)
			}
			if !strings.Contains(out.String(), "lefthook-local.yml") || !strings.Contains(out.String(), "omakase init") {
				t.Fatalf("%s: the block must point at the migration: %q", hook, out.String())
			}
		})
	}
}

// The base v0.19.x snapshot had NO omakase.manifest at all (only .omakase/ +
// lefthook-local.yml); the marker alone with a missing manifest must block too.
func TestRunHook_StaleLefthookSnapshotNoManifestBlocks(t *testing.T) {
	root, omk := newRepo(t)
	writeLefthookMarker(t, omk)
	var out bytes.Buffer
	code := RunHook("pre-commit", root, omk, strings.NewReader(""), &out, &out)
	if code == 0 {
		t.Fatalf("a lefthook-era snapshot with no manifest must fail closed, got exit 0")
	}
}

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

func TestStaleLefthookSnapshot(t *testing.T) {
	_, omk := newRepo(t)
	if stale, err := StaleLefthookSnapshot(omk); err != nil || stale {
		t.Fatalf("clean omk: want not-stale, got stale=%v err=%v", stale, err)
	}
	writeLefthookMarker(t, omk)
	writeSnapshotManifest(t, omk, "name: x\n")
	if stale, err := StaleLefthookSnapshot(omk); err != nil || !stale {
		t.Fatalf("lefthook-era snapshot: want stale, got stale=%v err=%v", stale, err)
	}
	// A marker plus a real gate block is NOT stale — the gates run; the stray
	// marker alone never disables them.
	writeSnapshotManifest(t, omk, "gate: g\n  hook: pre-commit\n  run: true\n")
	if stale, err := StaleLefthookSnapshot(omk); err != nil || stale {
		t.Fatalf("marker + gates: want not-stale, got stale=%v err=%v", stale, err)
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
