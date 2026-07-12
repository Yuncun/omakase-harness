package overlay

import (
	"errors"
	"io"
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

	if err := GateOff(repo, "example", io.Discard); err != nil {
		t.Fatalf("GateOff: %v", err)
	}
	if err := GateOff(repo, "example", io.Discard); err != nil {
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

// GateOff self-heals a stale placed gate script (one predating the menu-bypass
// check) when a gate is toggled — the file is rewritten from the embedded copy,
// and the snapshot + ledger hash are updated so the fail-closed drift guards stay
// quiet. init also heals now (TestReinitHealsStaleGateScript), so this test
// reverts the on-disk copy to a stale stub after init to isolate GateOff's own
// heal path.
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

	// init heals the stale payload script; revert the on-disk copy to stale so
	// GateOff's heal is what this test exercises.
	placed := filepath.Join(dir, rel)
	writeFile(t, placed, staleStub)
	if strings.Contains(readFileT(t, placed), "disabled-gates") {
		t.Fatalf("fixture invalid: reverted gate script still contains 'disabled-gates'")
	}

	if err := GateOff(repo, "anything", io.Discard); err != nil {
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

// A bare re-init from a stale payload must not leave the placed gate script
// stale: init re-heals it after the place loop, so a gate the human disabled
// stays honored across the documented refresh flow rather than the gate silently
// re-enabling while the consent surfaces still show it off.
func TestReinitHealsStaleGateScript(t *testing.T) {
	dir, repo := initRepo(t)
	stubLefthook(t)
	p := singleGatePayload(t)
	rel := ".omakase/bin/omakase-gate.sh"
	const staleStub = "#!/usr/bin/env bash\n# stale gate script predating the menu-bypass check\nexit 0\n"
	writeFile(t, filepath.Join(p, rel), staleStub)
	placed := filepath.Join(dir, rel)

	// First init from the stale payload: init heals the placed script.
	var out, errOut strings.Builder
	if code := RunInit(nil, &out, &errOut); code != 0 {
		t.Fatalf("init exit = %d; stderr=%q", code, errOut.String())
	}
	if !strings.Contains(readFileT(t, placed), "disabled-gates") {
		t.Fatalf("init did not heal the stale gate script")
	}

	// Disable a gate (records disabled-gates; the heal is a no-op now).
	if err := GateOff(repo, "smoke", io.Discard); err != nil {
		t.Fatalf("GateOff: %v", err)
	}

	// Bare re-init from the same stale payload: the place loop overwrites the
	// placed script with the stale payload copy, then the init heal restores
	// the menu-bypass check.
	out.Reset()
	errOut.Reset()
	if code := RunInit(nil, &out, &errOut); code != 0 {
		t.Fatalf("re-init exit = %d; stderr=%q", code, errOut.String())
	}

	healed := readFileT(t, placed)
	if n := strings.Count(healed, "disabled-gates"); n != 3 {
		t.Errorf("re-init left placed gate script pre-2b: %d 'disabled-gates' occurrences, want 3", n)
	}
	if !DisabledGates(repo.OMK)["smoke"] {
		t.Errorf("disabled-gates lost 'smoke' across re-init")
	}
	rows := state.ReadPlaced(filepath.Join(repo.OMK, "placed.tsv"))
	idx := placedIndex(rows, rel)
	if idx < 0 {
		t.Fatalf("ledger row missing for %s", rel)
	}
	eq(t, "ledger Hash", rows[idx].Hash, state.HashOf(placed))
}

// A git-tracked placed gate script must not be healed (overwritten) by a gate
// toggle — omakase never rewrites a committed file. GateOff still records the
// disable, but warns that the tracked, stale script won't honor it until the repo
// updates the script itself, and leaves the script untouched.
func TestGateOffSkipsTrackedGateScript(t *testing.T) {
	dir, repo := initRepo(t)
	rel := ".omakase/bin/omakase-gate.sh"
	const staleStub = "#!/usr/bin/env bash\n# tracked, customized gate script predating the menu-bypass check\nexit 0\n"
	writeFile(t, filepath.Join(dir, rel), staleStub)
	runGitT(t, dir, "add", "-f", rel) // staged counts as tracked (gitTracked -> ls-files)

	var warn strings.Builder
	if err := GateOff(repo, "smoke", &warn); err != nil {
		t.Fatalf("GateOff: %v", err)
	}

	if got := readFileT(t, filepath.Join(dir, rel)); got != staleStub {
		t.Errorf("tracked gate script was rewritten:\n got: %q\nwant: %q", got, staleStub)
	}
	if w := warn.String(); !strings.Contains(w, "tracked") || !strings.Contains(w, "disabled-gates") {
		t.Errorf("GateOff warning missing/weak for tracked script: %q", w)
	}
	if !DisabledGates(repo.OMK)["smoke"] {
		t.Errorf("disabled-gates did not record 'smoke' despite the tracked-script skip")
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
	stubLefthook(t)
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
	stubLefthook(t)
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
	stubLefthook(t)
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
