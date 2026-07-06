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
