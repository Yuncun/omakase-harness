package overlay

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Yuncun/omakase-harness/internal/state"
)

// placeSingleGate runs a fresh init against singleGatePayload's fixture so
// each toggle test starts from a real placed .omakase/gates/example.sh row
// (ledger + snapshot + on-disk file), matching TestFreshInit's arrangement
// (init_test.go:124).
func placeSingleGate(t *testing.T) (string, *state.Repo) {
	t.Helper()
	dir, repo := initRepo(t)
	stubLefthook(t)
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

// A placed gate script that predates the disabled-gates check is healed by the
// first gate toggle — file rewritten from the embedded copy, snapshot + ledger
// hash updated so the fail-closed drift guards stay quiet.
func TestGateOffHealsOldGateScript(t *testing.T) {
	dir, repo := initRepo(t)
	stubLefthook(t)
	p := singleGatePayload(t)
	rel := ".omakase/bin/omakase-gate.sh"
	const staleStub = "#!/usr/bin/env bash\n# stale gate script predating the menu-bypass check\nexit 0\n"
	writeFile(t, filepath.Join(p, rel), staleStub)

	var stdout, stderr strings.Builder
	if code := RunInit(nil, &stdout, &stderr); code != 0 {
		t.Fatalf("init exit = %d, want 0; stderr=%q", code, stderr.String())
	}

	placed := filepath.Join(dir, rel)
	if strings.Contains(readFileT(t, placed), "disabled-gates") {
		t.Fatalf("fixture invalid: placed gate script already contains 'disabled-gates' before healing")
	}

	if err := GateOff(repo, "anything"); err != nil {
		t.Fatalf("GateOff: %v", err)
	}

	placedContent := readFileT(t, placed)
	if !strings.Contains(placedContent, "disabled-gates") {
		t.Errorf("placed gate script not healed: missing 'disabled-gates'\n%s", placedContent)
	}

	snap := filepath.Join(repo.OMK, "payload-snapshot", rel)
	eq(t, "snapshot content", readFileT(t, snap), placedContent)

	rows := state.ReadPlaced(filepath.Join(repo.OMK, "placed.tsv"))
	idx := placedIndex(rows, rel)
	if idx < 0 {
		t.Fatalf("ledger row missing for %s", rel)
	}
	eq(t, "ledger Hash", rows[idx].Hash, state.HashOf(placed))
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

// ------------------------------------------------------------ consent merge

// Re-init must not resurrect a file the developer toggled off (consent merge),
// and must refresh its snapshot so a later FileOn restores the NEW payload copy.
func TestReinitPreservesDeclined(t *testing.T) {
	dir, repo := placeSingleGate(t)
	rel := ".omakase/gates/example.sh"

	if err := FileOff(repo, rel); err != nil {
		t.Fatalf("FileOff: %v", err)
	}

	var stdout, stderr strings.Builder
	if code := RunInit(nil, &stdout, &stderr); code != 0 {
		t.Fatalf("re-init exit = %d, want 0; stderr=%q", code, stderr.String())
	}

	full := filepath.Join(dir, rel)
	if lexists(full) {
		t.Errorf("declined file resurrected by re-init: %s", full)
	}

	rows := state.ReadPlaced(filepath.Join(repo.OMK, "placed.tsv"))
	idx := placedIndex(rows, rel)
	if idx < 0 {
		t.Fatalf("ledger row missing after re-init")
	}
	eq(t, "Enabled", rows[idx].Enabled, "0")

	// snapshot is refreshed from the CURRENT payload copy even though the file
	// itself was never re-placed on disk.
	snap := filepath.Join(repo.OMK, "payload-snapshot", rel)
	eq(t, "snapshot content", readFileT(t, snap), gateContent)

	// A later FileOn restores that refreshed snapshot.
	if err := FileOn(repo, rel); err != nil {
		t.Fatalf("FileOn: %v", err)
	}
	eq(t, "restored content", readFileT(t, full), gateContent)
}

// TestReinitAllDeclinedStillWritesWorktreeinclude: when the ONLY placed file
// is toggled off before re-init, placed ends up empty but declinedKept holds
// that row — the .worktreeinclude block must still be (re)written from the
// same prefixes used for .git/info/exclude, not silently skipped.
//
// .worktreeinclude is removed right after the first init (simulating it being
// absent — e.g. a fresh checkout of this untracked, per-worktree file) so the
// assertion actually distinguishes the buggy gate (skip => file stays absent)
// from the fix (gate fires on declinedKept => file is recreated). Leaving the
// file in place from the first init would pass either way, since nothing else
// touches it once written.
func TestReinitAllDeclinedStillWritesWorktreeinclude(t *testing.T) {
	dir, repo := placeSingleGate(t)
	rel := ".omakase/gates/example.sh"
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
	if !strings.Contains(string(content), ".omakase") {
		t.Errorf(".worktreeinclude missing .omakase entry: %q", string(content))
	}
}
