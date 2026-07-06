// Package-file toggle.go: the per-item consent backend behind `omakase status`
// (interactive Enter and the --disable/--enable flags). Gates toggle via
// $OMK/disabled-gates (read by payload/.omakase/bin/omakase-gate.sh step 2b);
// files toggle via the placed.tsv enabled column + payload-snapshot restore.
package overlay

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Yuncun/omakase-harness/internal/state"
)

var (
	ErrTracked    = errors.New("tracked by git — omakase never deletes committed files")
	ErrEdited     = errors.New("differs from what init placed (local edits?) — refusing to delete")
	ErrNotPlaced  = errors.New("not in the omakase ledger")
	ErrNoSnapshot = errors.New("no snapshot to restore from — run omakase init first")
)

func disabledGatesPath(omk string) string { return filepath.Join(omk, "disabled-gates") }

// DisabledGates is the set of gate names currently toggled off. Missing file -> empty.
func DisabledGates(omk string) map[string]bool {
	m := map[string]bool{}
	f, err := os.Open(disabledGatesPath(omk))
	if err != nil {
		return m
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if l := strings.TrimSpace(sc.Text()); l != "" {
			m[l] = true
		}
	}
	return m
}

// GateOff lists name in disabled-gates (idempotent) after healing the placed
// gate script (Task 5 fills healGateScript in; until then it returns nil).
func GateOff(repo *state.Repo, name string) error {
	if name == "" || strings.ContainsAny(name, " \t\n") {
		return fmt.Errorf("invalid gate name %q", name)
	}
	if err := healGateScript(repo); err != nil {
		return err
	}
	if DisabledGates(repo.OMK)[name] {
		return nil
	}
	if err := os.MkdirAll(repo.OMK, 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(disabledGatesPath(repo.OMK), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(name + "\n")
	return err
}

// GateOn delists name (idempotent). Rewrites the file whole via rewriteFile
// so a concurrent reader never sees a torn line.
func GateOn(repo *state.Repo, name string) error {
	set := DisabledGates(repo.OMK)
	if !set[name] {
		return nil
	}
	delete(set, name)
	names := make([]string, 0, len(set))
	for n := range set {
		names = append(names, n)
	}
	sort.Strings(names)
	content := ""
	if len(names) > 0 {
		content = strings.Join(names, "\n") + "\n"
	}
	return rewriteFile(disabledGatesPath(repo.OMK), []byte(content))
}

// healGateScript is a stub until Task 5 (embedded-script self-heal).
func healGateScript(repo *state.Repo) error { return nil }

func placedIndex(rows []state.PlacedRow, rel string) int {
	for i, r := range rows {
		if r.Rel == rel {
			return i
		}
	}
	return -1
}

// FileOff deletes a placed file (refusing tracked paths and local edits) and
// records enabled=0 so repair/self-heal respect the choice.
func FileOff(repo *state.Repo, rel string) error {
	ledger := filepath.Join(repo.OMK, "placed.tsv")
	rows := state.ReadPlaced(ledger)
	idx := placedIndex(rows, rel)
	if idx < 0 {
		return fmt.Errorf("%s: %w", rel, ErrNotPlaced)
	}
	if gitTracked(repo.Root, rel) {
		return fmt.Errorf("%s: %w", rel, ErrTracked)
	}
	full := filepath.Join(repo.Root, rel)
	if lexists(full) {
		if h := state.HashOf(full); h != "" && rows[idx].Hash != "" && h != rows[idx].Hash {
			return fmt.Errorf("%s: %w", rel, ErrEdited)
		}
		if err := DeletePlaced(repo.Root, rel, func(r string) bool { return gitTracked(repo.Root, r) }); err != nil {
			return err
		}
	}
	rows[idx].Enabled = "0"
	return state.WritePlaced(ledger, rows)
}

// FileOn restores a toggled-off file from the payload snapshot and records
// enabled=1 (hash refreshed from the restored copy).
func FileOn(repo *state.Repo, rel string) error {
	ledger := filepath.Join(repo.OMK, "placed.tsv")
	rows := state.ReadPlaced(ledger)
	idx := placedIndex(rows, rel)
	if idx < 0 {
		return fmt.Errorf("%s: %w", rel, ErrNotPlaced)
	}
	if gitTracked(repo.Root, rel) {
		return fmt.Errorf("%s: %w", rel, ErrTracked)
	}
	snap := filepath.Join(repo.OMK, "payload-snapshot", rel)
	if !lexists(snap) {
		return fmt.Errorf("%s: %w", rel, ErrNoSnapshot)
	}
	if err := safeMkdirAll(repo.Root, filepath.Join(repo.Root, filepath.Dir(rel))); err != nil {
		return err
	}
	if err := CopyEntry(snap, filepath.Join(repo.Root, rel)); err != nil {
		return err
	}
	rows[idx].Enabled = "1"
	rows[idx].Hash = state.HashOf(filepath.Join(repo.Root, rel))
	return state.WritePlaced(ledger, rows)
}
