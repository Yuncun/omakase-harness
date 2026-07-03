package state

import (
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
)

// AGENTS.md's sha256, hardcoded per the brief: printf '%s' "AGENTS.md" | shasum -a 256
const sha256OfAgentsMDString = "a54ff182c7e8acf56acfd6e4b9c3ff41e2c41a31c9b211b2deb9df75d9a478f9"

// newTestRepo builds a real temp git repo, mirroring the newrepo() fixture
// pattern of tests/placed.test.sh: t.TempDir() + `git init` + the
// user.email/user.name/commit.gpgsign config every repo-scoped test in this
// suite relies on so a commit never blocks on signing/identity.
func newTestRepo(t *testing.T) string {
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

func writeFile(t *testing.T, dir, rel, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, rel), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// ---------------------------------------------------------------- Discover

func TestDiscover(t *testing.T) {
	dir := newTestRepo(t)

	repo, err := Discover(dir)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}

	wantRoot, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	gotRoot, err := filepath.EvalSymlinks(repo.Root)
	if err != nil {
		t.Fatal(err)
	}
	if gotRoot != wantRoot {
		t.Errorf("Root = %q, want %q", repo.Root, dir)
	}
	if repo.CommonDir != filepath.Join(gotRoot, ".git") {
		t.Errorf("CommonDir = %q, want %q", repo.CommonDir, filepath.Join(gotRoot, ".git"))
	}
	if repo.OMK != filepath.Join(repo.CommonDir, "omakase") {
		t.Errorf("OMK = %q, want %q", repo.OMK, filepath.Join(repo.CommonDir, "omakase"))
	}
}

func TestDiscoverNotARepo(t *testing.T) {
	dir := t.TempDir()
	if _, err := Discover(dir); err == nil {
		t.Error("Discover outside a git repo: want error, got nil")
	}
}

// ---------------------------------------------------------------- HashOf

func TestHashOfSymlink(t *testing.T) {
	dir := t.TempDir()
	link := filepath.Join(dir, "CLAUDE.md")
	if err := os.Symlink("AGENTS.md", link); err != nil {
		t.Fatal(err)
	}
	if got := HashOf(link); got != sha256OfAgentsMDString {
		t.Errorf("HashOf(symlink) = %q, want %q", got, sha256OfAgentsMDString)
	}
}

func TestHashOfRegularFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "AGENTS.md")
	writeFile(t, dir, "AGENTS.md", "AGENTS.md")
	if got := HashOf(p); got != sha256OfAgentsMDString {
		t.Errorf("HashOf(file) = %q, want %q", got, sha256OfAgentsMDString)
	}
}

func TestHashOfMissing(t *testing.T) {
	dir := t.TempDir()
	if got := HashOf(filepath.Join(dir, "nope")); got != "" {
		t.Errorf("HashOf(missing) = %q, want \"\"", got)
	}
}

// ---------------------------------------------------------------- IsDrifted

func TestIsDrifted(t *testing.T) {
	dir := newTestRepo(t)

	// tracked.txt: committed, then modified in the worktree. Tracked files
	// are never drifted — upstream (git) owns them.
	writeFile(t, dir, "tracked.txt", "original\n")
	trackedLedgerHash := HashOf(filepath.Join(dir, "tracked.txt")) // hash at "placement" time
	runGitT(t, dir, "add", "tracked.txt")
	runGitT(t, dir, "commit", "-q", "-m", "init")
	writeFile(t, dir, "tracked.txt", "modified\n")

	// untracked.txt: never committed, modified after its ledger hash was recorded.
	writeFile(t, dir, "untracked.txt", "original\n")
	untrackedLedgerHash := HashOf(filepath.Join(dir, "untracked.txt"))
	writeFile(t, dir, "untracked.txt", "modified\n")

	cases := []struct {
		name       string
		rel        string
		ledgerHash string
		enabled    string
		want       bool
	}{
		{"tracked file modified in worktree: not drifted, git owns it", "tracked.txt", trackedLedgerHash, "1", false},
		{"untracked file modified: drifted", "untracked.txt", untrackedLedgerHash, "1", true},
		{"disabled (0): never drifted", "untracked.txt", untrackedLedgerHash, "0", false},
		{"disabled (empty): never drifted", "untracked.txt", untrackedLedgerHash, "", false},
		{"missing file: not drifted (its own state)", "missing.txt", "deadbeef", "1", false},
		{"empty ledger hash: not drifted", "untracked.txt", "", "1", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsDrifted(dir, tc.rel, tc.ledgerHash, tc.enabled); got != tc.want {
				t.Errorf("IsDrifted(%q, ledgerHash=%q, enabled=%q) = %v, want %v", tc.rel, tc.ledgerHash, tc.enabled, got, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------- ReadPlaced

func TestReadPlaced(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "placed.tsv")
	content := "rel1\tkind1\tsrc1\thash1\t1\n" +
		"rel2\tkind2\tsrc2\thash2\t1\textra\n" + // 6th tab absorbed into Enabled
		"rel3\tkind3\n" + // short row: Src/Hash/Enabled all empty
		// empty Rel: dropped. Malformed (no real writer emits an empty field) --
		// exercises ReadPlaced's own drop-empty-Rel rule in isolation, not a
		// bash-parity claim: per ReadPlaced's doc comment, bash's real `read`
		// strips a leading tab and shifts fields left, so it would never see an
		// empty Rel here at all.
		"\tkind4\tsrc4\thash4\t1\n"
	writeFile(t, dir, "placed.tsv", content)

	got := ReadPlaced(p)
	want := []PlacedRow{
		{Rel: "rel1", Kind: "kind1", Src: "src1", Hash: "hash1", Enabled: "1"},
		{Rel: "rel2", Kind: "kind2", Src: "src2", Hash: "hash2", Enabled: "1\textra"},
		{Rel: "rel3", Kind: "kind3", Src: "", Hash: "", Enabled: ""},
	}

	if len(got) != len(want) {
		t.Fatalf("ReadPlaced: got %d rows, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("row %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestReadPlacedMissing(t *testing.T) {
	dir := t.TempDir()
	if got := ReadPlaced(filepath.Join(dir, "nope.tsv")); got != nil {
		t.Errorf("ReadPlaced(missing) = %+v, want nil", got)
	}
}

// ---------------------------------------------------------------- CountNonEmptyLines

func TestCountNonEmptyLines(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "f.txt")
	// two non-empty lines, one empty line, final line has no trailing newline.
	writeFile(t, dir, "f.txt", "a\n\nb")
	if got := CountNonEmptyLines(p); got != 2 {
		t.Errorf("CountNonEmptyLines = %d, want 2", got)
	}
}

func TestCountNonEmptyLinesMissing(t *testing.T) {
	dir := t.TempDir()
	if got := CountNonEmptyLines(filepath.Join(dir, "nope")); got != 0 {
		t.Errorf("CountNonEmptyLines(missing) = %d, want 0", got)
	}
}

// ---------------------------------------------------------------- LatestVerdicts

func TestLatestVerdicts(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "ledger.tsv")
	content := "100\tgateA\tpass\tsha1\n" +
		"100\tgateA\tfail\tsha2\n" + // equal epoch: later row still wins (ts>=seen)
		"50\tgateA\tpass\tsha3\n" + // earlier epoch: must not override
		"abc\tgateB\tpass\tsha4\n" + // non-numeric epoch: dropped
		"200\tgateB\tpass\tsha5\n" +
		"10\tgateC\n" // NF<4: dropped
	writeFile(t, dir, "ledger.tsv", content)

	got := LatestVerdicts(p)

	want := map[string]Verdict{
		"gateA": {Epoch: 100, Verdict: "fail"},
		"gateB": {Epoch: 200, Verdict: "pass"},
	}
	if len(got) != len(want) {
		t.Fatalf("LatestVerdicts: got %d gates, want %d: %+v", len(got), len(want), got)
	}
	for gate, wantV := range want {
		if gotV, ok := got[gate]; !ok || gotV != wantV {
			t.Errorf("LatestVerdicts[%q] = %+v (ok=%v), want %+v", gate, gotV, ok, wantV)
		}
	}
}

func TestLatestVerdictsMissing(t *testing.T) {
	dir := t.TempDir()
	got := LatestVerdicts(filepath.Join(dir, "nope.tsv"))
	if len(got) != 0 {
		t.Errorf("LatestVerdicts(missing) = %+v, want empty map", got)
	}
}

// ---------------------------------------------------------------- FirstLine

func TestFirstLine(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "source")
	writeFile(t, dir, "source", "owner/repo\nsecond line\n")
	if got := FirstLine(p); got != "owner/repo" {
		t.Errorf("FirstLine = %q, want %q", got, "owner/repo")
	}
}

func TestFirstLineEmptyFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "empty")
	writeFile(t, dir, "empty", "")
	if got := FirstLine(p); got != "" {
		t.Errorf("FirstLine(empty) = %q, want \"\"", got)
	}
}

func TestFirstLineMissing(t *testing.T) {
	dir := t.TempDir()
	if got := FirstLine(filepath.Join(dir, "nope")); got != "" {
		t.Errorf("FirstLine(missing) = %q, want \"\"", got)
	}
}

// ---------------------------------------------------------------- WritePlaced

func TestWritePlacedHappyPathExactBytes(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "placed.tsv")
	rows := []PlacedRow{
		{Rel: ".claude/rules/a.md", Kind: "rule", Src: "payload", Hash: "abc123", Enabled: "1"},
		{Rel: "AGENTS.md", Kind: "doc", Src: "payload", Hash: "def456", Enabled: "1"},
	}

	if err := WritePlaced(p, rows); err != nil {
		t.Fatalf("WritePlaced: %v", err)
	}

	got, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	// Hand-computed literal: printf '%s\t%s\t%s\t%s\t%s\n' per row (init.sh:608).
	want := ".claude/rules/a.md\trule\tpayload\tabc123\t1\n" +
		"AGENTS.md\tdoc\tpayload\tdef456\t1\n"
	if string(got) != want {
		t.Errorf("WritePlaced bytes = %q, want %q", got, want)
	}
}

func TestWritePlacedEmptyRowsTruncates(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "placed.tsv")
	writeFile(t, dir, "placed.tsv", "stale\trow\tfrom\tprior\trun\n")

	if err := WritePlaced(p, nil); err != nil {
		t.Fatalf("WritePlaced(nil): %v", err)
	}

	got, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("WritePlaced(nil) left %d stale bytes, want a truncated (empty) file: %q", len(got), got)
	}
}

func TestWritePlacedRefusesEmptyField(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "placed.tsv")
	rows := []PlacedRow{
		{Rel: "AGENTS.md", Kind: "", Src: "payload", Hash: "abc", Enabled: "1"},
	}
	if err := WritePlaced(p, rows); err == nil {
		t.Error("WritePlaced with an empty field: want error, got nil")
	}
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Error("WritePlaced refused an invalid row but still wrote a file")
	}
}

func TestWritePlacedRefusesTabInField(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "placed.tsv")
	rows := []PlacedRow{
		{Rel: "AGENTS.md", Kind: "doc", Src: "pay\tload", Hash: "abc", Enabled: "1"},
	}
	if err := WritePlaced(p, rows); err == nil {
		t.Error("WritePlaced with a tab embedded in a field: want error, got nil")
	}
}

func TestWritePlacedRefusesNewlineInField(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "placed.tsv")
	rows := []PlacedRow{
		{Rel: "AGENTS.md", Kind: "doc", Src: "payload", Hash: "abc\ndef", Enabled: "1"},
	}
	if err := WritePlaced(p, rows); err == nil {
		t.Error("WritePlaced with a newline embedded in a field: want error, got nil")
	}
}

func TestWritePlacedRefusalLeavesNoPartialWriteAcrossMultipleRows(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "placed.tsv")
	rows := []PlacedRow{
		{Rel: "AGENTS.md", Kind: "doc", Src: "payload", Hash: "abc", Enabled: "1"}, // valid
		{Rel: "CLAUDE.md", Kind: "doc", Src: "payload", Hash: "", Enabled: "1"},    // invalid: empty Hash
	}
	if err := WritePlaced(p, rows); err == nil {
		t.Fatal("WritePlaced: want error for the malformed second row, got nil")
	}
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Error("a later invalid row must not leave an earlier valid row written (validate before writing)")
	}
}

func TestWritePlacedRefusalLeavesPreexistingFileUntouched(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "placed.tsv")
	original := "AGENTS.md\tdoc\tpayload\tabc\t1\n"
	writeFile(t, dir, "placed.tsv", original)

	rows := []PlacedRow{{Rel: "CLAUDE.md", Kind: "doc", Src: "payload", Hash: "", Enabled: "1"}} // invalid: empty Hash
	if err := WritePlaced(p, rows); err == nil {
		t.Fatal("WritePlaced: want error for the malformed row, got nil")
	}

	got, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != original {
		t.Errorf("a failed WritePlaced call must leave a pre-existing file byte-identical; got %q, want %q", got, original)
	}
}

func TestWritePlacedOverwritesExistingFileWholesale(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "placed.tsv")
	writeFile(t, dir, "placed.tsv", "old1\told2\told3\told4\told5\nsecondstale\tk\ts\th\t1\n")

	rows := []PlacedRow{{Rel: "new.md", Kind: "doc", Src: "payload", Hash: "xyz", Enabled: "1"}}
	if err := WritePlaced(p, rows); err != nil {
		t.Fatalf("WritePlaced: %v", err)
	}

	got, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	want := "new.md\tdoc\tpayload\txyz\t1\n"
	if string(got) != want {
		t.Errorf("WritePlaced bytes = %q, want %q (must regenerate wholesale, not append)", got, want)
	}
}

// ---------------------------------------------------------------- ReadSources

func TestReadSources(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "sources.tsv")
	content := "project\thttps://github.com/owner/repo\tmain\tabc123\t1700000000\n" +
		"badrow\tonly\tthree\n" + // wrong field count (3, not 5): skipped
		"personal\t\t-\t-\t1700000001\n" + // empty Source field: skipped
		"personal\thttps://github.com/me/dotfiles\t-\tdef456\t1700000002\n"
	writeFile(t, dir, "sources.tsv", content)

	got := ReadSources(p)
	want := []SourceRow{
		{Layer: "project", Source: "https://github.com/owner/repo", Ref: "main", Commit: "abc123", Epoch: "1700000000"},
		{Layer: "personal", Source: "https://github.com/me/dotfiles", Ref: "-", Commit: "def456", Epoch: "1700000002"},
	}
	if len(got) != len(want) {
		t.Fatalf("ReadSources: got %d rows, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("row %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestReadSourcesMissing(t *testing.T) {
	dir := t.TempDir()
	if got := ReadSources(filepath.Join(dir, "nope.tsv")); got != nil {
		t.Errorf("ReadSources(missing) = %+v, want nil", got)
	}
}

// TestReadSourcesSkipsAnyEmptyField extends TestReadSources's single
// empty-Source case to every field: ReadSources's any-empty-field skip
// (state.go's `fields[0] == "" || fields[1] == "" || ...` check) must drop
// a row whose Layer, Ref, Commit, or Epoch is empty, not just its Source.
func TestReadSourcesSkipsAnyEmptyField(t *testing.T) {
	cases := []struct {
		name string
		row  string
	}{
		{"empty Layer", "\thttps://github.com/owner/repo\tmain\tabc123\t1700000000\n"},
		{"empty Source", "project\t\tmain\tabc123\t1700000000\n"},
		{"empty Ref", "project\thttps://github.com/owner/repo\t\tabc123\t1700000000\n"},
		{"empty Commit", "project\thttps://github.com/owner/repo\tmain\t\t1700000000\n"},
		{"empty Epoch", "project\thttps://github.com/owner/repo\tmain\tabc123\t\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			p := filepath.Join(dir, "sources.tsv")
			writeFile(t, dir, "sources.tsv", tc.row)
			if got := ReadSources(p); got != nil {
				t.Errorf("ReadSources(%s) = %+v, want nil (skipped)", tc.name, got)
			}
		})
	}
}

func TestReadSourcesSkipsRowWithTooManyFields(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "sources.tsv")
	// A 6th field is NOT absorbed the way ReadPlaced absorbs a 6th tab into
	// Enabled -- ReadSources requires EXACTLY 5 fields and drops anything else.
	writeFile(t, dir, "sources.tsv", "project\towner/repo\t-\t-\t1\textra\n")

	if got := ReadSources(p); got != nil {
		t.Errorf("ReadSources(6 fields) = %+v, want nil (dropped)", got)
	}
}

// ---------------------------------------------------------------- WriteSources

func TestWriteSourcesHappyPathExactBytes(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "sources.tsv")
	rows := []SourceRow{
		{Layer: "1", Source: "https://github.com/owner/repo", Ref: "main", Commit: "abc123", Epoch: "1700000000"},
		{Layer: "2", Source: "https://github.com/me/dotfiles", Ref: "-", Commit: "-", Epoch: "1700000002"},
	}
	if err := WriteSources(p, rows); err != nil {
		t.Fatalf("WriteSources: %v", err)
	}

	got, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	// Hand-computed literal per the frozen format (GC6): one row bottom-to-top,
	// layer = the ordinal string ("1" bottom, "2" top).
	want := "1\thttps://github.com/owner/repo\tmain\tabc123\t1700000000\n" +
		"2\thttps://github.com/me/dotfiles\t-\t-\t1700000002\n"
	if string(got) != want {
		t.Errorf("WriteSources bytes = %q, want %q", got, want)
	}
}

func TestWriteSourcesEmptyRowsTruncates(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "sources.tsv")
	writeFile(t, dir, "sources.tsv", "stale\trow\tfrom\tprior\trun\n")

	if err := WriteSources(p, nil); err != nil {
		t.Fatalf("WriteSources(nil): %v", err)
	}

	got, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("WriteSources(nil) left %d stale bytes, want a truncated (empty) file: %q", len(got), got)
	}
}

func TestWriteSourcesRefusesEmptyField(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "sources.tsv")
	rows := []SourceRow{{Layer: "1", Source: "", Ref: "-", Commit: "-", Epoch: "1700000000"}}
	if err := WriteSources(p, rows); err == nil {
		t.Error("WriteSources with an empty field: want error, got nil")
	}
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Error("WriteSources refused an invalid row but still wrote a file")
	}
}

func TestWriteSourcesRefusesTabInField(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "sources.tsv")
	rows := []SourceRow{{Layer: "1", Source: "owner/re\tpo", Ref: "-", Commit: "-", Epoch: "1700000000"}}
	if err := WriteSources(p, rows); err == nil {
		t.Error("WriteSources with a tab embedded in a field: want error, got nil")
	}
}

func TestWriteSourcesRefusesNewlineInField(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "sources.tsv")
	rows := []SourceRow{{Layer: "1", Source: "owner/repo", Ref: "-", Commit: "abc\ndef", Epoch: "1700000000"}}
	if err := WriteSources(p, rows); err == nil {
		t.Error("WriteSources with a newline embedded in a field: want error, got nil")
	}
}

// TestWriteSourcesAcceptsLayerOutsideOldClosedSet pins the Phase 3.5 behavior
// change: WriteSources no longer refuses a Layer value outside the deleted
// {"project","personal"} closed set (see the Phase 3 TestWriteSourcesRefusesUnknownLayer
// this replaces). Layer is now an opaque ordinal string assigned by the caller
// from the row's stack position ("1" bottom, "2" top, and onward) — WriteSources
// only guards the SHARED empty/tab/newline invariant every field gets, same as
// Source/Ref/Commit/Epoch.
func TestWriteSourcesAcceptsLayerOutsideOldClosedSet(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "sources.tsv")
	rows := []SourceRow{{Layer: "3", Source: "owner/repo", Ref: "-", Commit: "-", Epoch: "1700000000"}}
	if err := WriteSources(p, rows); err != nil {
		t.Errorf(`WriteSources with Layer="3" (outside the old {project,personal} set): want nil, got %v`, err)
	}
	want := "3\towner/repo\t-\t-\t1700000000\n"
	got, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Errorf("WriteSources bytes = %q, want %q", got, want)
	}
}

func TestWriteSourcesRefusalLeavesNoPartialWriteAcrossMultipleRows(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "sources.tsv")
	rows := []SourceRow{
		{Layer: "project", Source: "owner/repo", Ref: "-", Commit: "-", Epoch: "1700000000"}, // valid
		{Layer: "personal", Source: "", Ref: "-", Commit: "-", Epoch: "1700000001"},          // invalid: empty Source
	}
	if err := WriteSources(p, rows); err == nil {
		t.Fatal("WriteSources: want error for the malformed second row, got nil")
	}
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Error("a later invalid row must not leave an earlier valid row written (validate before writing)")
	}
}

func TestWriteSourcesRefusalLeavesPreexistingFileUntouched(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "sources.tsv")
	original := "project\towner/repo\t-\t-\t1700000000\n"
	writeFile(t, dir, "sources.tsv", original)

	rows := []SourceRow{{Layer: "personal", Source: "", Ref: "-", Commit: "-", Epoch: "1700000001"}} // invalid
	if err := WriteSources(p, rows); err == nil {
		t.Fatal("WriteSources: want error for the malformed row, got nil")
	}

	got, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != original {
		t.Errorf("a failed WriteSources call must leave a pre-existing file byte-identical; got %q, want %q", got, original)
	}
}

func TestWriteSourcesOverwritesExistingFileWholesale(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "sources.tsv")
	writeFile(t, dir, "sources.tsv", "old1\told2\told3\told4\told5\nsecondstale\tk\ts\th\t1\n")

	rows := []SourceRow{{Layer: "project", Source: "owner/new", Ref: "-", Commit: "-", Epoch: "1700000099"}}
	if err := WriteSources(p, rows); err != nil {
		t.Fatalf("WriteSources: %v", err)
	}

	got, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	want := "project\towner/new\t-\t-\t1700000099\n"
	if string(got) != want {
		t.Errorf("WriteSources bytes = %q, want %q (must regenerate wholesale, not append)", got, want)
	}
}

// TestWriteSourcesModeMatchesFreshInodeViaRename proves WriteSources goes
// through the tmp+rename discipline (Global Constraint 3), not os.WriteFile
// over an existing path -- which only applies its mode argument at file
// CREATION, silently preserving whatever mode the file already had. Mirrors
// overlay's TestExcludeWriteModeMatchesBashFreshInode: seed the file at 0600
// (deliberately not 0666&^umask) and require the post-write mode to be the
// fresh-inode value regardless.
func TestWriteSourcesModeMatchesFreshInodeViaRename(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "sources.tsv")
	writeFile(t, dir, "sources.tsv", "stale\n")
	if err := os.Chmod(p, 0o600); err != nil {
		t.Fatal(err)
	}

	rows := []SourceRow{{Layer: "project", Source: "owner/repo", Ref: "-", Commit: "-", Epoch: "1700000000"}}
	if err := WriteSources(p, rows); err != nil {
		t.Fatalf("WriteSources: %v", err)
	}

	info, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	want := os.FileMode(0o666) &^ currentUmaskT(t)
	if info.Mode().Perm() != want {
		t.Errorf("mode after WriteSources = %o, want %o (0666 &^ umask -- the original seeded 0600 must NOT survive)", info.Mode().Perm(), want)
	}
}

// currentUmaskT reads the process umask without permanently changing it --
// local twin of overlay's currentUmask (unexported there too; duplicated
// here rather than imported, to keep this test self-contained).
func currentUmaskT(t *testing.T) os.FileMode {
	t.Helper()
	u := syscall.Umask(0)
	syscall.Umask(u)
	return os.FileMode(u)
}

// TestWriteSourcesReadSourcesRoundTripOrdinalLayers is the core Phase 3.5
// table test (GC6): a write of two rows -- ordinal layer "1" at the bottom,
// "2" at the top -- read back through ReadSources returns the same rows in
// the same order, byte for byte. Neither WriteSources nor ReadSources
// interprets the Layer string itself; it round-trips as an opaque field.
func TestWriteSourcesReadSourcesRoundTripOrdinalLayers(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "sources.tsv")
	rows := []SourceRow{
		{Layer: "1", Source: "owner/repo", Ref: "main", Commit: "abc123", Epoch: "1700000000"},
		{Layer: "2", Source: "owner/dotfiles", Ref: "-", Commit: "def456", Epoch: "1700000002"},
	}
	if err := WriteSources(p, rows); err != nil {
		t.Fatalf("WriteSources: %v", err)
	}

	got := ReadSources(p)
	if len(got) != len(rows) {
		t.Fatalf("round trip: got %d rows, want %d: %+v", len(got), len(rows), got)
	}
	for i := range rows {
		if got[i] != rows[i] {
			t.Errorf("round trip row %d = %+v, want %+v (bottom-to-top order must be preserved)", i, got[i], rows[i])
		}
	}
}

// ---------------------------------------------------------------- SynthesizeSources

func TestSynthesizeSourcesFromV1Source(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "source", "owner/repo\n")

	rows, ok := SynthesizeSources(dir, "1700000000")
	if !ok {
		t.Fatal("SynthesizeSources = false, want true")
	}
	want := SourceRow{Layer: "1", Source: "owner/repo", Ref: "-", Commit: "-", Epoch: "1700000000"}
	if len(rows) != 1 || rows[0] != want {
		t.Errorf("SynthesizeSources = %+v, want [%+v]", rows, want)
	}
}

func TestSynthesizeSourcesSplitsRef(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "source", "https://github.com/owner/repo#v1.2.3\n")

	rows, ok := SynthesizeSources(dir, "1700000000")
	if !ok {
		t.Fatal("SynthesizeSources = false, want true")
	}
	want := SourceRow{Layer: "1", Source: "https://github.com/owner/repo", Ref: "v1.2.3", Commit: "-", Epoch: "1700000000"}
	if len(rows) != 1 || rows[0] != want {
		t.Errorf("SynthesizeSources = %+v, want [%+v]", rows, want)
	}
}

func TestSynthesizeSourcesFirstHashOnlySplits(t *testing.T) {
	// expandSource's split rule is the FIRST '#': a source string with more
	// than one '#' (pathological, but the split rule must match exactly)
	// keeps every subsequent '#' inside Ref.
	dir := t.TempDir()
	writeFile(t, dir, "source", "owner/repo#v1#extra\n")

	rows, ok := SynthesizeSources(dir, "1700000000")
	if !ok {
		t.Fatal("SynthesizeSources = false, want true")
	}
	want := SourceRow{Layer: "1", Source: "owner/repo", Ref: "v1#extra", Commit: "-", Epoch: "1700000000"}
	if len(rows) != 1 || rows[0] != want {
		t.Errorf("SynthesizeSources = %+v, want [%+v]", rows, want)
	}
}

func TestSynthesizeSourcesAbsentBoth(t *testing.T) {
	dir := t.TempDir()
	rows, ok := SynthesizeSources(dir, "1700000000")
	if ok || rows != nil {
		t.Errorf("SynthesizeSources(neither present) = (%+v,%v), want (nil,false)", rows, ok)
	}
}

func TestSynthesizeSourcesAbsentWhenSourcesTSVPresent(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "source", "owner/repo\n")
	writeFile(t, dir, "sources.tsv", "1\towner/repo\t-\t-\t1\n")

	rows, ok := SynthesizeSources(dir, "1700000000")
	if ok || rows != nil {
		t.Errorf("SynthesizeSources(sources.tsv present) = (%+v,%v), want (nil,false)", rows, ok)
	}
}

func TestSynthesizeSourcesAbsentWhenSourceFileMissing(t *testing.T) {
	dir := t.TempDir()
	rows, ok := SynthesizeSources(dir, "1700000000")
	if ok || rows != nil {
		t.Errorf("SynthesizeSources(no $OMK/source) = (%+v,%v), want (nil,false)", rows, ok)
	}
}

func TestSynthesizeSourcesAbsentWhenSourceFileEmpty(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "source", "")
	rows, ok := SynthesizeSources(dir, "1700000000")
	if ok || rows != nil {
		t.Errorf("SynthesizeSources(empty $OMK/source) = (%+v,%v), want (nil,false)", rows, ok)
	}
}

// TestSynthesizeSourcesLocalPathWithHashNotSplit mirrors expandSource's
// local-path guard (internal/overlay/source.go): a remembered $OMK/source
// value that IS itself an existing path must never be '#'-split, even if
// its name contains a literal '#' — init absolutizes a local-dir source
// before remembering it, so a path like ".../my#project" is exactly the
// kind of string this guards against corrupting into Source ".../my",
// Ref "project".
func TestSynthesizeSourcesLocalPathWithHashNotSplit(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "my#project")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, dir, "source", sub+"\n")

	rows, ok := SynthesizeSources(dir, "1700000000")
	if !ok {
		t.Fatal("SynthesizeSources = false, want true")
	}
	want := SourceRow{Layer: "1", Source: sub, Ref: "-", Commit: "-", Epoch: "1700000000"}
	if len(rows) != 1 || rows[0] != want {
		t.Errorf("SynthesizeSources(local path containing '#') = %+v, want [%+v]", rows, want)
	}
}

// TestSynthesizeSourcesNonexistentPathLookingStringStillSplits is the
// inverse of the above: a string that merely LOOKS like a path (has
// slashes, and even a literal '#') but does not name anything that exists
// on disk is not a local-path source at all — it still splits on '#',
// parity with expandSource's own behavior on an absent path.
func TestSynthesizeSourcesNonexistentPathLookingStringStillSplits(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "source", "/no/such/dir/my#project\n")

	rows, ok := SynthesizeSources(dir, "1700000000")
	if !ok {
		t.Fatal("SynthesizeSources = false, want true")
	}
	want := SourceRow{Layer: "1", Source: "/no/such/dir/my", Ref: "project", Commit: "-", Epoch: "1700000000"}
	if len(rows) != 1 || rows[0] != want {
		t.Errorf("SynthesizeSources(nonexistent path-looking string) = %+v, want [%+v]", rows, want)
	}
}

func TestSynthesizeSourcesNeverWrites(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "source", "owner/repo\n")
	if _, ok := SynthesizeSources(dir, "1700000000"); !ok {
		t.Fatal("SynthesizeSources = false, want true")
	}
	if _, err := os.Stat(filepath.Join(dir, "sources.tsv")); !os.IsNotExist(err) {
		t.Error("SynthesizeSources must never write sources.tsv itself")
	}
}
