// run.go is the live wiring of the interactive status screen: it builds the
// Item list from the real repo (placed ledger + resolved gate rows + the
// disabled-gates set + the repo's tracked-but-unplaced harness files), adapts
// the four overlay consent operations into the model's Toggler, and runs the
// Bubble Tea program. status.Run calls Run only after interactiveTerminal() has
// confirmed stdout is a real terminal.
package tui

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Yuncun/omakase-harness/internal/harness"
	"github.com/Yuncun/omakase-harness/internal/overlay"
	"github.com/Yuncun/omakase-harness/internal/state"
)

// Run builds live items from the repo and runs the interactive program. Returns
// the process exit code: 0 on a clean quit, 1 if Bubble Tea fails to start or
// run (e.g. it cannot open the tty). Callers must have verified stdout is a real
// terminal (status.interactiveTerminal).
func Run(repo *state.Repo, header, footprint string) int {
	items, machinery := LiveItems(repo)
	m := NewModel(header, footprint, items, machinery, &repoToggler{repo}, func() ([]Item, int) { return LiveItems(repo) })
	if _, err := tea.NewProgram(m, tea.WithAltScreen()).Run(); err != nil {
		fmt.Fprintf(os.Stderr, "omakase: %v\n", err)
		return 1
	}
	return 0
}

// LiveItems re-derives the Item list and machinery count from the repo on disk:
// the placed ledger, the resolved lefthook gate rows, the disabled-gates set,
// and the repo's tracked harness files (so a committed harness file shows as a
// view-only row). It is called both to seed the model and, via the reload
// closure, after every successful toggle so the screen reflects the new state.
func LiveItems(repo *state.Repo) ([]Item, int) {
	rows := state.ReadPlaced(filepath.Join(repo.OMK, "placed.tsv"))
	gates := GateRows(repo.Root)
	disabled := overlay.DisabledGates(repo.OMK)
	tracked := trackedHarness(repo.Root)
	return BuildItems(rows, gates, disabled, tracked)
}

// trackedHarness lists the repo's git-tracked harness files (the CommittedGlobs
// pathspecs), NUL-delimited so paths with odd characters survive. Empty entries
// (from the trailing NUL, or an empty result) are dropped. A non-repo or a
// git failure yields nil — BuildItems simply itemizes no tracked rows.
func trackedHarness(root string) []string {
	args := append([]string{"-C", root, "ls-files", "-z", "--"}, harness.CommittedGlobs...)
	out, err := exec.Command("git", args...).Output()
	if err != nil {
		return nil
	}
	var rels []string
	for _, p := range splitNUL(out) {
		if p != "" {
			rels = append(rels, p)
		}
	}
	return rels
}

// splitNUL splits git's -z output on NUL bytes.
func splitNUL(b []byte) []string {
	var out []string
	start := 0
	for i, c := range b {
		if c == 0 {
			out = append(out, string(b[start:i]))
			start = i + 1
		}
	}
	if start < len(b) {
		out = append(out, string(b[start:]))
	}
	return out
}

// repoToggler adapts the model's Toggler interface to the live overlay consent
// operations against one repo.
type repoToggler struct{ repo *state.Repo }

func (t *repoToggler) GateOff(name string) error { return overlay.GateOff(t.repo, name) }
func (t *repoToggler) GateOn(name string) error  { return overlay.GateOn(t.repo, name) }
func (t *repoToggler) FileOff(rel string) error  { return overlay.FileOff(t.repo, rel) }

// FileOn restores a placed file from the payload snapshot. It is idempotent for
// an already-on file — overlay.FileOn restores over the existing copy and
// returns nil — which the model's group-on path relies on: turning a partially
// off group on calls FileOn for every child, including the ones already on.
func (t *repoToggler) FileOn(rel string) error { return overlay.FileOn(t.repo, rel) }

var _ Toggler = (*repoToggler)(nil)
