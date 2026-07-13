// This file (toggle.go) is the scriptable side of per-item consent:
// `omakase status --disable <name>` / `--enable <name>`. <name> resolves in
// order: placed path -> placed-path group directory -> wired gate name (or a
// gate already listed in disabled-gates, so a stale disable is always
// reversible). Harness machinery (.omakase/, lefthook wiring, .worktreeinclude)
// is refused, and a name matching none of the above is refused too — both with
// exit 2 — rather than silently written to disabled-gates as a phantom gate.
package status

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/Yuncun/omakase-harness/internal/overlay"
	"github.com/Yuncun/omakase-harness/internal/state"
	"github.com/Yuncun/omakase-harness/internal/tui"
)

// discoverRepo is the shared cwd->repo resolution of the scriptable consent
// flags; a failure prints the one-liner and returns nil.
func discoverRepo(stderr io.Writer) *state.Repo {
	wd, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(stderr, "omakase: not inside a git repo")
		return nil
	}
	repo, err := state.Discover(wd)
	if err != nil {
		fmt.Fprintln(stderr, "omakase: not inside a git repo")
		return nil
	}
	return repo
}

// placedTargets resolves name against the ledger the way every consent flag
// does: an exact placed rel, else every placed rel under name as a group
// directory. Empty means name matched no placed path.
func placedTargets(rows []state.PlacedRow, name string) []string {
	for _, r := range rows {
		if r.Rel == name {
			return []string{name}
		}
	}
	var targets []string
	p := strings.TrimSuffix(name, "/") + "/"
	for _, r := range rows {
		if strings.HasPrefix(r.Rel, p) {
			targets = append(targets, r.Rel)
		}
	}
	return targets
}

// refuseMachinery rejects any target that is harness machinery. Machinery
// keeps the harness running and is never a consent item — the TUI and MCP
// menu filter it out (tui.IsMachinery), so the scriptable surface must too.
// Deleting the .omakase/ tree or lefthook wiring via --disable would brick
// every commit with a raw hook error; --keep/--restore on it would bless or
// revert gate plumbing behind the same one-word name. Refuse before any write.
func refuseMachinery(targets []string, name string, stderr io.Writer) bool {
	for _, rel := range targets {
		if tui.IsMachinery(rel) {
			fmt.Fprintf(stderr, "omakase: %s is harness machinery — it keeps the harness running; remove the harness with omakase remove\n", name)
			return true
		}
	}
	return false
}

func runToggle(off bool, name string, stdout, stderr io.Writer) int {
	repo := discoverRepo(stderr)
	if repo == nil {
		return 1
	}

	rows := state.ReadPlaced(filepath.Join(repo.OMK, "placed.tsv"))
	targets := placedTargets(rows, name) // empty -> treat name as a gate
	if refuseMachinery(targets, name, stderr) {
		return 2
	}

	if len(targets) == 0 { // gate
		// A gate name is valid only if lefthook actually wires it (the same
		// source the TUI's gate rows use), or it is already disabled (so a
		// stale disable stays reversible even if the wiring later changed).
		// Anything else — a typo'd file path, a gate-script filename — would
		// otherwise become a phantom disabled-gates entry with a false success.
		if !wiredGateNames(repo.Root)[name] && !overlay.DisabledGates(repo.OMK)[name] {
			fmt.Fprintf(stderr, "omakase: unknown gate or placed path: %s\n", name)
			return 2
		}
		if off {
			if err := overlay.GateOff(repo, name, stderr); err != nil {
				fmt.Fprintf(stderr, "omakase: %v\n", err)
				return 1
			}
			fmt.Fprintf(stdout, "omakase: gate '%s' off — skipped visibly at commit/push until re-enabled (omakase status --enable %s)\n", name, name)
		} else {
			if err := overlay.GateOn(repo, name); err != nil {
				fmt.Fprintf(stderr, "omakase: %v\n", err)
				return 1
			}
			fmt.Fprintf(stdout, "omakase: gate '%s' back on\n", name)
		}
		return 0
	}

	code := 0
	for _, rel := range targets {
		var terr error
		if off {
			terr = overlay.FileOff(repo, rel)
		} else {
			terr = overlay.FileOn(repo, rel)
		}
		switch {
		case terr == nil && off:
			fmt.Fprintf(stdout, "omakase: %s removed (restorable — omakase status --enable %s)\n", rel, rel)
		case terr == nil:
			fmt.Fprintf(stdout, "omakase: %s restored\n", rel)
		case errors.Is(terr, overlay.ErrTracked), errors.Is(terr, overlay.ErrEdited),
			errors.Is(terr, overlay.ErrEditedKeep),
			errors.Is(terr, overlay.ErrNotPlaced), errors.Is(terr, overlay.ErrNoSnapshot):
			fmt.Fprintf(stderr, "omakase: REFUSING: %v\n", terr)
			code = 1
		default:
			fmt.Fprintf(stderr, "omakase: %v\n", terr)
			code = 1
		}
	}
	return code
}

// runKeepRestore is `omakase status --keep <path>` / `--restore <path>` —
// the plumbing half of the edit lifecycle (issue #98 Part 2: modified ->
// omakase diff -> keep / restore). Names resolve exactly like --disable
// (placed path or group directory); machinery and tracked paths are refused
// with exit 2. Gates are files-only territory: a name matching no placed
// path is unknown here, never a gate.
func runKeepRestore(keep bool, name string, stdout, stderr io.Writer) int {
	repo := discoverRepo(stderr)
	if repo == nil {
		return 1
	}

	rows := state.ReadPlaced(filepath.Join(repo.OMK, "placed.tsv"))
	targets := placedTargets(rows, name)
	if refuseMachinery(targets, name, stderr) {
		return 2
	}
	if len(targets) == 0 {
		fmt.Fprintf(stderr, "omakase: unknown placed path: %s\n", name)
		return 2
	}

	code := 0
	for _, rel := range targets {
		var err error
		if keep {
			err = overlay.FileKeep(repo, rel)
		} else {
			err = overlay.FileRestore(repo, rel)
		}
		switch {
		case err == nil && keep:
			fmt.Fprintf(stdout, "omakase: kept %s — your version is the accepted one now (put the harness version back any time: omakase status --restore %s)\n", rel, rel)
		case err == nil:
			fmt.Fprintf(stdout, "omakase: restored %s to the harness version\n", rel)
		case errors.Is(err, overlay.ErrTracked):
			fmt.Fprintf(stderr, "omakase: REFUSING: %v\n", err)
			code = 2
		default:
			fmt.Fprintf(stderr, "omakase: %v\n", err)
			if code == 0 {
				code = 1
			}
		}
	}
	return code
}

// wiredGateNames is the set of consent-tracked gate names lefthook currently
// wires, resolved from the same source the interactive screen's gate rows use
// (tui.GateRows). It is the authority for whether a --disable/--enable target
// that matched no placed path is a real gate. lefthook unresolved -> empty set.
func wiredGateNames(root string) map[string]bool {
	set := map[string]bool{}
	for _, g := range tui.GateRows(root) {
		if g.Gate {
			set[g.Name] = true
		}
	}
	return set
}
