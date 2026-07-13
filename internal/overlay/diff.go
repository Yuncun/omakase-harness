// Package-file diff.go: the `omakase diff` verb (issue #98 Part 2) — the
// read-only teaching interface of the edit lifecycle. It answers the user's
// question, "what did I change vs the harness": each changed enabled placed
// file is rendered disk-relative-to-baseline (the opposite of chezmoi's
// confusing default, where your own edit renders as a deletion), where the
// baseline is the accepted copy for a kept file and the payload snapshot
// otherwise. Zero writes, no mutating flags, ever; exit 0 whether or not
// differences exist.
package overlay

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/Yuncun/omakase-harness/internal/state"
)

// RunDiff is the `omakase diff [path…]` verb. No paths = every changed
// enabled placed file. A path resolves like the status toggles: an exact
// placed rel, else a group directory holding placed rels; anything else is
// refused with exit 2 before any output. Rendering shells to
// `git diff --no-index` (git is guaranteed present; zero new dependencies);
// git's "differences found" exit 1 is expected and swallowed.
func RunDiff(argv []string, stdout, stderr io.Writer) int {
	var names []string
	for _, a := range argv {
		if a == "--help" || a == "-h" {
			printDiffUsage(stdout)
			return 0
		}
		if strings.HasPrefix(a, "-") {
			fmt.Fprintf(stderr, "omakase: unknown flag %s (omakase diff is read-only and takes only paths; see omakase diff --help)\n", a)
			return 2
		}
		names = append(names, a)
	}

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
	ledger := filepath.Join(repo.OMK, "placed.tsv")
	if _, err := os.Stat(ledger); err != nil {
		fmt.Fprintln(stderr, "omakase: no harness installed here — nothing to diff (install one:  omakase init)")
		return 1
	}
	rows := state.ReadPlaced(ledger)

	// Resolve the requested paths to ledger rows up front, so a typo refuses
	// cleanly instead of printing half a report first.
	targets := rows
	if len(names) > 0 {
		seen := map[string]bool{}
		targets = nil
		for _, name := range names {
			matched := false
			prefix := strings.TrimSuffix(name, "/") + "/"
			for _, r := range rows {
				if r.Rel != name && !strings.HasPrefix(r.Rel, prefix) {
					continue
				}
				matched = true
				if !seen[r.Rel] {
					seen[r.Rel] = true
					targets = append(targets, r)
				}
			}
			if !matched {
				fmt.Fprintf(stderr, "omakase: unknown placed path: %s\n", name)
				return 2
			}
		}
	}

	changed := 0
	for _, row := range targets {
		if row.Enabled != "1" {
			continue
		}
		full := filepath.Join(repo.Root, row.Rel)
		if !lexists(full) {
			fmt.Fprintf(stdout, "%s — missing from this worktree (restored on the next checkout, or:  omakase init)\n", row.Rel)
			changed++
			continue
		}
		// The baseline is what the user last consented to: the accepted copy
		// for a kept file, the harness version otherwise.
		base := filepath.Join(repo.OMK, "payload-snapshot", row.Rel)
		vs := "the harness version"
		if k := keptEntry(repo.OMK, row.Rel); lexists(k) {
			base, vs = k, "your accepted (kept) version"
		}
		if !lexists(base) {
			continue // nothing recorded to compare against (no snapshot)
		}
		if h := state.HashOf(full); h != "" && h == state.HashOf(base) {
			continue
		}
		fmt.Fprintf(stdout, "%s — your changes vs %s:\n", row.Rel, vs)
		var raw bytes.Buffer
		cmd := exec.Command("git", "diff", "--no-index", "--", base, full)
		cmd.Stdout = &raw
		cmd.Stderr = stderr
		if err := cmd.Run(); err != nil {
			// exit 1 = differences found (expected); anything else is git
			// failing to render — say so, keep going, still exit 0.
			if ee, ok := err.(*exec.ExitError); !ok || ee.ExitCode() != 1 {
				fmt.Fprintf(stderr, "omakase: could not diff %s: %v\n", row.Rel, err)
			}
		}
		writeScrubbedDiff(stdout, raw.String(), row.Rel, vs)
		changed++
	}
	if changed == 0 {
		fmt.Fprintln(stdout, "omakase: no changes — every placed file matches what you've consented to.")
	}
	return 0
}

// writeScrubbedDiff prints git's unified diff with the mechanism paths
// hidden: the operands are files under the git dir's omakase/ state
// (payload-snapshot, kept), and `git diff --no-index` prints those paths
// verbatim in its header — exactly the internals the diff verb exists to
// keep out of sight. The `diff --git`/`index` lines are dropped (our own
// per-file header already names the file), the ---/+++ lines are relabeled
// in the user's vocabulary, and a binary-file notice loses its paths. Hunks
// and content lines pass through untouched.
func writeScrubbedDiff(w io.Writer, raw, rel, vs string) {
	for _, line := range strings.Split(strings.TrimSuffix(raw, "\n"), "\n") {
		switch {
		case line == "":
			fmt.Fprintln(w)
		case strings.HasPrefix(line, "diff --git "), strings.HasPrefix(line, "index "):
			// dropped: both carry the internal operand paths / blob hashes
		case strings.HasPrefix(line, "--- "):
			fmt.Fprintf(w, "--- %s  (%s)\n", rel, vs)
		case strings.HasPrefix(line, "+++ "):
			fmt.Fprintf(w, "+++ %s  (yours, on disk)\n", rel)
		case strings.HasPrefix(line, "Binary files "):
			fmt.Fprintln(w, "Binary files differ")
		default:
			fmt.Fprintln(w, line)
		}
	}
}

func printDiffUsage(w io.Writer) {
	fmt.Fprint(w, `usage: omakase diff [path…]

Shows what you changed in the files omakase placed, vs the harness version
(or vs the version you accepted with  omakase status --keep). Read-only.

  (no paths)   every changed placed file
  path…        a placed file, or a directory of placed files

After reviewing:  omakase status --keep <path>     make your version the accepted one
                  omakase status --restore <path>  put the harness's version back
`)
}
