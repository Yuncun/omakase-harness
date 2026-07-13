// Package-file toggle.go: the per-item consent backend behind `omakase status`
// (interactive Enter and the --disable/--enable/--keep/--restore flags). Gates
// toggle via $OMK/disabled-gates (read by payload/.omakase/bin/omakase-gate.sh
// step 2b); files toggle via the placed.tsv enabled column + payload-snapshot
// restore; edits are kept/restored via $OMK/kept (issue #98 Part 2).
package overlay

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Yuncun/omakase-harness/internal/state"
	"github.com/Yuncun/omakase-harness/internal/templates"
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

// GateOff lists name in disabled-gates (idempotent) after healing the placed
// gate script (healGateScript below) so an old, pre-Task-1 placed copy — one
// that has never checked disabled-gates — gets rewritten first. warn receives
// the one-line notice when the gate script is committed and cannot be healed.
func GateOff(repo *state.Repo, name string, warn io.Writer) error {
	if name == "" || strings.ContainsAny(name, " \t\n") {
		return fmt.Errorf("invalid gate name %q", name)
	}
	if err := healGateScript(repo, warn, true); err != nil {
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

// healGateScript self-updates a placed gate script that predates the
// disabled-gates check (step 2b): without it, a disabled gate would keep
// blocking. Snapshot + ledger hash move in the same step — a healed file
// with a stale recorded hash would read as drift everywhere the ledger
// hash is compared (status's drift flag, ensure-present's warn), phantom
// noise pinned on omakase's own edit.
func healGateScript(repo *state.Repo, warn io.Writer, disableInFlight bool) error {
	const rel = ".omakase/bin/omakase-gate.sh"
	dest := filepath.Join(repo.Root, rel)
	b, err := os.ReadFile(dest)
	if err != nil {
		return nil // no placed gate script — nothing depends on healing
	}
	if strings.Contains(string(b), "disabled-gates") {
		return nil // already 2b-capable (tracked or not) — nothing to heal
	}
	// omakase never rewrites a committed file; healing a tracked gate script
	// would be the one write path in this package without that guard. The
	// tracked script predates the disabled-gates check, so the disable is still
	// recorded but won't take effect until the repo updates the script itself —
	// say that plainly and skip, touching neither snapshot nor ledger.
	if gitTracked(repo.Root, rel) {
		// Warn only when a disable is in play — GateOff always is; a routine
		// bare init with an empty disabled-gates has nothing to warn about and
		// would otherwise nag on every refresh, forever.
		if disableInFlight || len(DisabledGates(repo.OMK)) > 0 {
			fmt.Fprintf(warn, "omakase: WARNING — tracked gate script %s predates disabled-gates support — disabled gates will not take effect until the repo updates it.\n", rel)
		}
		return nil
	}
	if err := templates.Install("omakase-gate.sh", dest); err != nil {
		return err
	}
	snap := filepath.Join(repo.OMK, "payload-snapshot", rel)
	if lexists(snap) {
		if err := CopyEntry(dest, snap); err != nil {
			return err
		}
	}
	ledger := filepath.Join(repo.OMK, "placed.tsv")
	rows := state.ReadPlaced(ledger)
	if idx := placedIndex(rows, rel); idx >= 0 {
		rows[idx].Hash = state.HashOf(dest)
		return state.WritePlaced(ledger, rows)
	}
	return nil
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
