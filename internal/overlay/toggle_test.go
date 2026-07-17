package overlay

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Yuncun/omakase-harness/internal/state"
)

// placeSingleGate runs a fresh init against singleGatePayload's fixture so each
// toggle test starts from a real placed .omakase/gates/example.sh row (ledger +
// snapshot + on-disk file).
func placeSingleGate(t *testing.T) (string, *state.Repo) {
	t.Helper()
	dir, repo := initRepo(t)
	singleGatePayload(t)
	var stdout, stderr strings.Builder
	if code := RunInit(nil, &stdout, &stderr); code != 0 {
		t.Fatalf("init exit = %d, want 0; stderr=%q", code, stderr.String())
	}
	return dir, repo
}

// ---------------------------------------------------------------- gates

func TestGateOffOnIdempotent(t *testing.T) {
	_, repo := placeSingleGate(t)

	if err := GateOff(repo, "example"); err != nil {
		t.Fatalf("GateOff: %v", err)
	}
	if err := GateOff(repo, "example"); err != nil {
		t.Fatalf("GateOff (second call): %v", err)
	}
	content := readFileT(t, filepath.Join(repo.OMK, "disabled-gates"))
	eq(t, "disabled-gates", content, "example\n")
	if !DisabledGates(repo.OMK)["example"] {
		t.Errorf("DisabledGates does not report 'example' disabled")
	}

	if err := GateOn(repo, "example"); err != nil {
		t.Fatalf("GateOn: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(repo.OMK, "disabled-gates"))
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("read disabled-gates: %v", err)
	}
	if len(b) != 0 {
		t.Errorf("disabled-gates not empty after GateOn: %q", b)
	}
	if DisabledGates(repo.OMK)["example"] {
		t.Errorf("DisabledGates still reports 'example' disabled after GateOn")
	}

	// GateOn when already absent is a no-op nil.
	if err := GateOn(repo, "example"); err != nil {
		t.Fatalf("GateOn on absent entry: %v", err)
	}
}

// ---------------------------------------------------------------- files

func TestFileOffRemovesAndMarksDisabled(t *testing.T) {
	dir, repo := placeSingleGate(t)
	rel := ".omakase/gates/example.sh"

	if err := FileOff(repo, rel); err != nil {
		t.Fatalf("FileOff: %v", err)
	}
	if lexists(filepath.Join(dir, rel)) {
		t.Errorf("file still present after FileOff")
	}
	rows := state.ReadPlaced(filepath.Join(repo.OMK, "placed.tsv"))
	idx := placedIndex(rows, rel)
	if idx < 0 {
		t.Fatalf("ledger row missing after FileOff")
	}
	eq(t, "Enabled", rows[idx].Enabled, "0")
	snap := filepath.Join(repo.OMK, "payload-snapshot", rel)
	if !lexists(snap) {
		t.Errorf("snapshot missing after FileOff")
	}
}

func TestFileOnRestoresFromSnapshot(t *testing.T) {
	dir, repo := placeSingleGate(t)
	rel := ".omakase/gates/example.sh"

	if err := FileOff(repo, rel); err != nil {
		t.Fatalf("FileOff: %v", err)
	}
	if err := FileOn(repo, rel); err != nil {
		t.Fatalf("FileOn: %v", err)
	}
	full := filepath.Join(dir, rel)
	eq(t, "restored content", readFileT(t, full), gateContent)
	info, err := os.Stat(full)
	if err != nil || info.Mode().Perm()&0o100 == 0 {
		t.Errorf("restored file not executable: %v", err)
	}
	rows := state.ReadPlaced(filepath.Join(repo.OMK, "placed.tsv"))
	idx := placedIndex(rows, rel)
	if idx < 0 {
		t.Fatalf("ledger row missing after FileOn")
	}
	eq(t, "Enabled", rows[idx].Enabled, "1")
	eq(t, "Hash", rows[idx].Hash, state.HashOf(full))
}

func TestFileOffRefusesTracked(t *testing.T) {
	dir, repo := placeSingleGate(t)
	rel := ".omakase/gates/example.sh"
	runGitT(t, dir, "add", "-f", rel)
	runGitT(t, dir, "commit", "-q", "-m", "track it")

	err := FileOff(repo, rel)
	if !errors.Is(err, ErrTracked) {
		t.Fatalf("FileOff on tracked path: got %v, want ErrTracked", err)
	}
	if !lexists(filepath.Join(dir, rel)) {
		t.Errorf("tracked file removed despite ErrTracked refusal")
	}
}

func TestFileOffRefusesEdited(t *testing.T) {
	dir, repo := placeSingleGate(t)
	rel := ".omakase/gates/example.sh"
	full := filepath.Join(dir, rel)
	f, err := os.OpenFile(full, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("x"); err != nil {
		t.Fatal(err)
	}
	f.Close()

	err = FileOff(repo, rel)
	if !errors.Is(err, ErrEdited) {
		t.Fatalf("FileOff on edited path: got %v, want ErrEdited", err)
	}
	if !lexists(full) {
		t.Errorf("edited file removed despite ErrEdited refusal")
	}
}

func TestFileOffNotPlaced(t *testing.T) {
	_, repo := placeSingleGate(t)
	err := FileOff(repo, "nope.md")
	if !errors.Is(err, ErrNotPlaced) {
		t.Fatalf("FileOff on unplaced path: got %v, want ErrNotPlaced", err)
	}
}

// FileOn must refuse a locally edited, still-enabled file rather than silently
// restoring the snapshot over it — the symmetric guard to FileOff's ErrEdited.
// Without it, a group/bulk "all on" (which calls FileOn on every child,
// already-on ones included) destroys an edited on-child with a success message.
func TestFileOnRefusesEdited(t *testing.T) {
	dir, repo := placeSingleGate(t)
	rel := ".omakase/gates/example.sh"
	full := filepath.Join(dir, rel)

	f, err := os.OpenFile(full, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("# local edit\n"); err != nil {
		t.Fatal(err)
	}
	f.Close()
	edited := readFileT(t, full)

	err = FileOn(repo, rel)
	if !errors.Is(err, ErrEditedKeep) {
		t.Fatalf("FileOn on edited path: got %v, want ErrEditedKeep", err)
	}
	if got := readFileT(t, full); got != edited {
		t.Errorf("edited file was overwritten by FileOn:\n got: %q\nwant: %q", got, edited)
	}
}

// twoRulePayload ships two files under .claude/rules/, forming a real group,
// and points OMAKASE_PAYLOAD at it.
func twoRulePayload(t *testing.T) string {
	t.Helper()
	p := t.TempDir()
	writeFile(t, filepath.Join(p, ".claude", "rules", "a.md"), "rule a\n")
	writeFile(t, filepath.Join(p, ".claude", "rules", "b.md"), "rule b\n")
	t.Setenv("OMAKASE_PAYLOAD", p)
	return p
}

func placeTwoRules(t *testing.T) (string, *state.Repo) {
	t.Helper()
	dir, repo := initRepo(t)
	twoRulePayload(t)
	var stdout, stderr strings.Builder
	if code := RunInit(nil, &stdout, &stderr); code != 0 {
		t.Fatalf("init exit = %d, want 0; stderr=%q", code, stderr.String())
	}
	return dir, repo
}

// Group turn-on must not clobber an edited, still-enabled sibling: with b.md
// toggled off (the group is now mixed) and a.md locally edited, restoring the
// group calls FileOn on both children. FileOn(a.md) must refuse (ErrEdited, edit
// intact) while FileOn(b.md) restores the toggled-off sibling.
func TestFileOnGroupSkipsEditedSibling(t *testing.T) {
	dir, repo := placeTwoRules(t)
	a := ".claude/rules/a.md"
	b := ".claude/rules/b.md"

	if err := FileOff(repo, b); err != nil {
		t.Fatalf("FileOff(b): %v", err)
	}
	fullA := filepath.Join(dir, a)
	f, err := os.OpenFile(fullA, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("edited\n"); err != nil {
		t.Fatal(err)
	}
	f.Close()
	editedA := readFileT(t, fullA)

	if err := FileOn(repo, a); !errors.Is(err, ErrEditedKeep) {
		t.Fatalf("FileOn(edited a): got %v, want ErrEditedKeep", err)
	}
	if got := readFileT(t, fullA); got != editedA {
		t.Errorf("edited sibling clobbered by group turn-on:\n got: %q\nwant: %q", got, editedA)
	}
	if err := FileOn(repo, b); err != nil {
		t.Fatalf("FileOn(b): %v", err)
	}
	if !lexists(filepath.Join(dir, b)) {
		t.Errorf("b.md not restored by group turn-on")
	}
}

// ------------------------------------------------------------ consent merge

// Re-init must not resurrect a file the developer toggled off (consent merge),
// and must refresh its snapshot so a later FileOn restores the new payload copy.
func TestReinitPreservesDeclined(t *testing.T) {
	dir, repo := initRepo(t)
	const ruleContent = "steer gently\n"
	p := t.TempDir()
	writeFile(t, filepath.Join(p, ".omakase", "gates", "example.sh"), gateContent)
	writeFile(t, filepath.Join(p, ".claude", "rules", "steer.md"), ruleContent)
	t.Setenv("OMAKASE_PAYLOAD", p)
	var stdout, stderr strings.Builder
	if code := RunInit(nil, &stdout, &stderr); code != 0 {
		t.Fatalf("init exit = %d, want 0; stderr=%q", code, stderr.String())
	}

	rel := ".claude/rules/steer.md"
	mach := ".omakase/gates/example.sh"
	if err := FileOff(repo, rel); err != nil {
		t.Fatalf("FileOff(%s): %v", rel, err)
	}
	// A machinery row can only end up enabled=0 via a pre-guard binary (the
	// CLI refuses machinery now); simulate that leftover at the overlay layer.
	if err := FileOff(repo, mach); err != nil {
		t.Fatalf("FileOff(%s): %v", mach, err)
	}

	stdout.Reset()
	stderr.Reset()
	if code := RunInit(nil, &stdout, &stderr); code != 0 {
		t.Fatalf("re-init exit = %d, want 0; stderr=%q", code, stderr.String())
	}

	full := filepath.Join(dir, rel)
	if lexists(full) {
		t.Errorf("declined steering file resurrected by re-init: %s", full)
	}

	rows := state.ReadPlaced(filepath.Join(repo.OMK, "placed.tsv"))
	idx := placedIndex(rows, rel)
	if idx < 0 {
		t.Fatalf("ledger row missing after re-init")
	}
	eq(t, "Enabled", rows[idx].Enabled, "0")

	// snapshot is refreshed from the current payload copy even though the file
	// itself was never re-placed on disk.
	snap := filepath.Join(repo.OMK, "payload-snapshot", rel)
	eq(t, "snapshot content", readFileT(t, snap), ruleContent)

	// A later FileOn restores that refreshed snapshot.
	if err := FileOn(repo, rel); err != nil {
		t.Fatalf("FileOn: %v", err)
	}
	eq(t, "restored content", readFileT(t, full), ruleContent)

	// The machinery decline is ignored: machinery is never a consent item, so
	// re-init re-places the gate primitive and flips its row back to enabled=1
	// (recovers repos broken by a pre-guard binary's --disable .omakase).
	if !lexists(filepath.Join(dir, mach)) {
		t.Errorf("machinery not re-placed by re-init: %s", mach)
	}
	midx := placedIndex(rows, mach)
	if midx < 0 {
		t.Fatalf("machinery ledger row missing after re-init")
	}
	eq(t, "machinery Enabled", rows[midx].Enabled, "1")
}

// TestReinitAllDeclinedStillWritesWorktreeinclude: when the only placed file is
// toggled off before re-init, placed ends up empty but declinedKept holds that
// row — the .worktreeinclude block must still be (re)written from the same
// prefixes used for .git/info/exclude, not silently skipped.
//
// .worktreeinclude is removed right after the first init (simulating it being
// absent — e.g. a fresh checkout of this untracked, per-worktree file) so the
// assertion actually distinguishes the buggy gate (skip => file stays absent)
// from the fix (gate fires on declinedKept => file is recreated). Leaving the
// file in place from the first init would pass either way, since nothing else
// touches it once written.
func TestReinitAllDeclinedStillWritesWorktreeinclude(t *testing.T) {
	dir, repo := initRepo(t)
	pay := t.TempDir()
	rel := ".claude/rules/steer.md"
	writeFile(t, filepath.Join(pay, rel), "steer gently\n")
	t.Setenv("OMAKASE_PAYLOAD", pay)
	var initOut, initErr strings.Builder
	if code := RunInit(nil, &initOut, &initErr); code != 0 {
		t.Fatalf("init exit = %d, want 0; stderr=%q", code, initErr.String())
	}
	wtinc := filepath.Join(dir, ".worktreeinclude")

	if err := os.Remove(wtinc); err != nil {
		t.Fatalf("remove %s: %v", wtinc, err)
	}

	if err := FileOff(repo, rel); err != nil {
		t.Fatalf("FileOff: %v", err)
	}

	var stdout, stderr strings.Builder
	if code := RunInit(nil, &stdout, &stderr); code != 0 {
		t.Fatalf("re-init exit = %d, want 0; stderr=%q", code, stderr.String())
	}

	content, err := os.ReadFile(wtinc)
	if err != nil {
		t.Fatalf("read %s: %v", wtinc, err)
	}
	if !strings.Contains(string(content), ".claude") {
		t.Errorf(".worktreeinclude missing .claude entry: %q", string(content))
	}
}

// ------------------------------------------------------------ kept edits

// editFile appends a line to a placed file, returning the new content — the
// canonical "user edited a placed file" fixture (issue #98 Part 2).
func editFile(t *testing.T, full string) string {
	t.Helper()
	f, err := os.OpenFile(full, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("# my edit\n"); err != nil {
		t.Fatal(err)
	}
	f.Close()
	return readFileT(t, full)
}

// FileKeep accepts the on-disk edit: the accepted copy lands in $OMK/kept,
// the ledger hash moves to the disk hash (drift reads green again), and the
// payload snapshot keeps the untouched harness version so restore stays
// possible offline.
func TestFileKeepAcceptsEdit(t *testing.T) {
	dir, repo := placeTwoRules(t)
	rel := ".claude/rules/a.md"
	full := filepath.Join(dir, rel)
	edited := editFile(t, full)

	// sanity: the edit must register as drift before the keep
	pre := state.ReadPlaced(filepath.Join(repo.OMK, "placed.tsv"))
	if !state.IsDrifted(dir, rel, pre[placedIndex(pre, rel)].Hash, "1") {
		t.Fatalf("fixture: edit not drifted")
	}

	if err := FileKeep(repo, rel); err != nil {
		t.Fatalf("FileKeep: %v", err)
	}

	eq(t, "kept copy", readFileT(t, filepath.Join(repo.OMK, "kept", rel)), edited)
	eq(t, "snapshot untouched", readFileT(t, filepath.Join(repo.OMK, "payload-snapshot", rel)), "rule a\n")
	rows := state.ReadPlaced(filepath.Join(repo.OMK, "placed.tsv"))
	idx := placedIndex(rows, rel)
	eq(t, "ledger Hash", rows[idx].Hash, state.HashOf(full))
	eq(t, "Enabled", rows[idx].Enabled, "1")
	if state.IsDrifted(dir, rel, rows[idx].Hash, rows[idx].Enabled) {
		t.Errorf("kept file still reads as drifted")
	}

	// Edit again after keep: drift returns, now measured against the
	// accepted hash — the lifecycle is self-similar.
	editFile(t, full)
	if !state.IsDrifted(dir, rel, rows[idx].Hash, rows[idx].Enabled) {
		t.Errorf("second edit after keep does not read as drifted")
	}
}

func TestFileKeepRefusals(t *testing.T) {
	dir, repo := placeTwoRules(t)
	rel := ".claude/rules/a.md"

	if err := FileKeep(repo, "nope.md"); !errors.Is(err, ErrNotPlaced) {
		t.Errorf("FileKeep(unplaced): got %v, want ErrNotPlaced", err)
	}

	if err := FileOff(repo, rel); err != nil {
		t.Fatalf("FileOff: %v", err)
	}
	if err := FileKeep(repo, rel); !errors.Is(err, ErrNothingToKeep) {
		t.Errorf("FileKeep(missing): got %v, want ErrNothingToKeep", err)
	}

	b := ".claude/rules/b.md"
	runGitT(t, dir, "add", "-f", b)
	runGitT(t, dir, "commit", "-q", "-m", "track b")
	if err := FileKeep(repo, b); !errors.Is(err, ErrTracked) {
		t.Errorf("FileKeep(tracked): got %v, want ErrTracked", err)
	}
}

// FileRestore clears a keep: harness version back on disk, kept mark gone,
// ledger hash reset — and works on plain (un-kept) drift the same way.
func TestFileRestoreClearsKeptAndPlainDrift(t *testing.T) {
	dir, repo := placeTwoRules(t)
	a, b := ".claude/rules/a.md", ".claude/rules/b.md"
	fullA, fullB := filepath.Join(dir, a), filepath.Join(dir, b)

	editFile(t, fullA)
	if err := FileKeep(repo, a); err != nil {
		t.Fatalf("FileKeep: %v", err)
	}
	editFile(t, fullB) // plain drift, never kept

	for _, rel := range []string{a, b} {
		if err := FileRestore(repo, rel); err != nil {
			t.Fatalf("FileRestore(%s): %v", rel, err)
		}
	}
	eq(t, "a restored", readFileT(t, fullA), "rule a\n")
	eq(t, "b restored", readFileT(t, fullB), "rule b\n")
	if lexists(filepath.Join(repo.OMK, "kept", a)) {
		t.Errorf("kept mark survived FileRestore")
	}
	rows := state.ReadPlaced(filepath.Join(repo.OMK, "placed.tsv"))
	for _, rel := range []string{a, b} {
		idx := placedIndex(rows, rel)
		eq(t, rel+" Hash", rows[idx].Hash, state.HashOf(filepath.Join(dir, rel)))
	}
}

// A disable/enable cycle must round-trip the ACCEPTED version, not silently
// swap back to the harness version the user already replaced: FileOn prefers
// the kept copy.
func TestFileOnPrefersKeptCopy(t *testing.T) {
	dir, repo := placeTwoRules(t)
	rel := ".claude/rules/a.md"
	full := filepath.Join(dir, rel)
	edited := editFile(t, full)

	if err := FileKeep(repo, rel); err != nil {
		t.Fatalf("FileKeep: %v", err)
	}
	if err := FileOff(repo, rel); err != nil {
		t.Fatalf("FileOff after keep: %v", err) // accepted hash matches disk, so the delete guard passes
	}
	if err := FileOn(repo, rel); err != nil {
		t.Fatalf("FileOn: %v", err)
	}
	eq(t, "re-enabled content", readFileT(t, full), edited)
	rows := state.ReadPlaced(filepath.Join(repo.OMK, "placed.tsv"))
	idx := placedIndex(rows, rel)
	eq(t, "Hash", rows[idx].Hash, state.HashOf(full))
}

// A kept-then-disabled file must never be a dead end (review finding, PR
// #100): --restore on the disabled row restores the harness version,
// re-enables it, and clears the kept mark — while --enable keeps preferring
// the accepted copy (TestFileOnPrefersKeptCopy).
func TestFileRestoreReenablesDisabledKeptRow(t *testing.T) {
	dir, repo := placeTwoRules(t)
	rel := ".claude/rules/a.md"
	full := filepath.Join(dir, rel)
	editFile(t, full)
	if err := FileKeep(repo, rel); err != nil {
		t.Fatalf("FileKeep: %v", err)
	}
	if err := FileOff(repo, rel); err != nil {
		t.Fatalf("FileOff: %v", err)
	}

	if err := FileRestore(repo, rel); err != nil {
		t.Fatalf("FileRestore on the disabled row: %v", err)
	}
	eq(t, "restored content", readFileT(t, full), "rule a\n")
	if lexists(filepath.Join(repo.OMK, "kept", rel)) {
		t.Errorf("kept mark survived the restore")
	}
	rows := state.ReadPlaced(filepath.Join(repo.OMK, "placed.tsv"))
	idx := placedIndex(rows, rel)
	eq(t, "Enabled", rows[idx].Enabled, "1")
	eq(t, "Hash", rows[idx].Hash, state.HashOf(full))
}
