// Package-file toggle.go: the per-item consent backend behind `omakase status`
// (interactive Enter and the --disable/--enable/--keep/--restore flags). Gates
// toggle via $OMK/disabled-gates (read by internal/gate step 2); files toggle
// via the placed.tsv enabled column + payload-snapshot restore; edits are
// kept/restored via $OMK/kept (issue #98 Part 2).
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
	ErrTracked = errors.New("tracked by git — omakase never deletes committed files")
	ErrEdited  = errors.New("differs from what init placed (local edits?) — refusing to delete")
	// ErrEditedKeep is FileOn's twin of ErrEdited: same local-edit detection,
	// but the refused operation is an overwrite (snapshot restore), not a
	// delete — the message must not claim the opposite operation. It points
	// at the edit lifecycle: an edited placed file is an uncommitted change,
	// not an error (issue #98 Part 2).
	ErrEditedKeep    = errors.New("differs from what init placed (local edits?) — refusing to overwrite. See the change:  omakase diff  — then make it yours (omakase status --keep <path>) or put the harness version back (omakase status --restore <path>)")
	ErrNotPlaced     = errors.New("not in the omakase ledger")
	ErrNoSnapshot    = errors.New("no snapshot to restore from — run omakase init first")
	ErrNothingToKeep = errors.New("missing from disk — nothing to keep")
)

// keptEntry is the accepted copy of a kept file. Its existence IS the kept
// mark (mirroring disabled-gates): no placed.tsv format change, old readers
// unaffected.
func keptEntry(omk, rel string) string { return filepath.Join(omk, "kept", rel) }

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

// GateOff lists name in disabled-gates (idempotent). internal/gate reads this
// file at hook time (step 2), so the disable takes effect immediately — no
// placed script to heal now that gates are declared in the manifest.
func GateOff(repo *state.Repo, name string) error {
	if name == "" || strings.ContainsAny(name, " \t\n") {
		return fmt.Errorf("invalid gate name %q", name)
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
	// Symmetric with FileOff's ErrEdited guard: an on-disk copy that differs
	// from the ledger hash is a local edit. A group/bulk "all on" calls FileOn
	// on every child, already-on ones included, so restoring the snapshot over
	// an edited, still-enabled file would silently destroy that edit. Refuse
	// instead. A toggled-off file has no on-disk copy (FileOff deleted it), so
	// this never blocks the real restore.
	full := filepath.Join(repo.Root, rel)
	if lexists(full) {
		if h := state.HashOf(full); h != "" && rows[idx].Hash != "" && h != rows[idx].Hash {
			return fmt.Errorf("%s: %w", rel, ErrEditedKeep)
		}
	}
	// A kept file's accepted copy outranks the snapshot: consent survives an
	// off/on cycle — re-enabling restores what the user accepted, not the
	// harness version they had already replaced (that reversal is --restore's
	// job, and it clears the kept mark first).
	snap := filepath.Join(repo.OMK, "payload-snapshot", rel)
	if k := keptEntry(repo.OMK, rel); lexists(k) {
		snap = k
	}
	if !lexists(snap) {
		return fmt.Errorf("%s: %w", rel, ErrNoSnapshot)
	}
	if err := safeMkdirAll(repo.Root, filepath.Join(repo.Root, filepath.Dir(rel))); err != nil {
		return err
	}
	if err := CopyEntry(snap, full); err != nil {
		return err
	}
	rows[idx].Enabled = "1"
	rows[idx].Hash = state.HashOf(full)
	return state.WritePlaced(ledger, rows)
}

// FileKeep accepts the current on-disk version of a placed file as the
// user's own (issue #98 Part 2: modified -> diff -> keep). The disk copy is
// stored at $OMK/kept/rel and the row's ledger hash becomes the disk hash,
// so every hash check reads green — green means "matches what you've
// consented to". The payload snapshot is untouched: the harness version
// never leaves the machine, so --restore always works offline.
func FileKeep(repo *state.Repo, rel string) error {
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
	if !lexists(full) {
		return fmt.Errorf("%s: %w", rel, ErrNothingToKeep)
	}
	h := state.HashOf(full)
	if h == "" {
		return fmt.Errorf("%s: unreadable — cannot keep", rel)
	}
	keptRoot := filepath.Join(repo.OMK, "kept")
	kept := keptEntry(repo.OMK, rel)
	if err := safeMkdirAll(keptRoot, filepath.Dir(kept)); err != nil {
		return err
	}
	if err := CopyEntry(full, kept); err != nil {
		return err
	}
	// Keeping a file the user re-created at a disabled path adopts it too:
	// "make the current on-disk version yours" implies it is managed again.
	rows[idx].Enabled = "1"
	rows[idx].Hash = h
	return state.WritePlaced(ledger, rows)
}

// FileRestore puts the harness's version back: the payload-snapshot copy is
// restored to disk, any $OMK/kept/rel accepted copy is deleted (clearing the
// kept mark), and the ledger hash resets to the restored copy — one verb for
// a kept file, plain drift, AND a disabled row (which it re-enables:
// "the harness's version, full stop" undoes both a keep and a disable, so a
// kept-then-disabled file is never a dead end — --enable brings back the
// accepted version, --restore the harness's).
func FileRestore(repo *state.Repo, rel string) error {
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
	full := filepath.Join(repo.Root, rel)
	if err := safeMkdirAll(repo.Root, filepath.Join(repo.Root, filepath.Dir(rel))); err != nil {
		return err
	}
	if err := CopyEntry(snap, full); err != nil {
		return err
	}
	if err := removeF(keptEntry(repo.OMK, rel)); err != nil {
		return err
	}
	rows[idx].Enabled = "1"
	rows[idx].Hash = state.HashOf(full)
	return state.WritePlaced(ledger, rows)
}
