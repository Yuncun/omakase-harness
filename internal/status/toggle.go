// This file (toggle.go) is the scriptable side of per-item consent:
// `omakase status --disable <name>` / `--enable <name>`. <name> resolves in
// order: placed path -> placed-path group directory -> WIRED gate name (or a
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

func runToggle(off bool, name string, stdout, stderr io.Writer) int {
	wd, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(stderr, "omakase: not inside a git repo")
		return 1
	}
	repo, err := state.Discover(wd)
	if err != nil {
		fmt.Fprintln(stderr, "omakase: not inside a git repo")
		return 1
	}

	rows := state.ReadPlaced(filepath.Join(repo.OMK, "placed.tsv"))
	var targets []string // placed rels; empty -> treat name as a gate
	for _, r := range rows {
		if r.Rel == name {
			targets = []string{name}
			break
		}
	}
	if len(targets) == 0 { // group directory: every placed rel under it
		p := strings.TrimSuffix(name, "/") + "/"
		for _, r := range rows {
			if strings.HasPrefix(r.Rel, p) {
				targets = append(targets, r.Rel)
			}
		}
	}

	// Machinery keeps the harness running and is never a consent item — the TUI
	// and MCP menu filter it out (tui.IsMachinery), so the scriptable surface
	// must too. Deleting the .omakase/ tree or lefthook wiring via --disable
	// would brick every commit with a raw hook error. Refuse before any write.
	for _, rel := range targets {
		if tui.IsMachinery(rel) {
			fmt.Fprintf(stderr, "omakase: %s is harness machinery — it keeps the harness running; remove the harness with omakase remove\n", name)
			return 2
		}
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
