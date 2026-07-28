// This file is the blocking mechanism: non-cone sparse-checkout. A blocked
// item is subtracted from the working tree ("/*" keeps everything, "!/<rel>"
// hides one item) — hosts discover steering files by their presence on disk,
// so absent-from-tree means not-loaded, on every host and for every item
// kind including the ones no host offers a switch for (instruction files,
// hooks). git keeps tracking the file: the index is untouched, so commits,
// pulls, and pushes behave normally and reversal is byte-perfect.
//
// The overlay and the mask cannot collide: sparse-checkout only subtracts
// TRACKED files, the overlay only adds UNTRACKED ones — disjoint sets.
package block

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/Yuncun/omakase-harness/internal/state"
)

// maskApply makes every reachable worktree's working tree match the blocked
// set: `git sparse-checkout set --no-cone` with the derived patterns, or
// `git sparse-checkout disable` when the set is empty. An unreachable
// worktree gets a warning and is skipped — the next block/unblock, or
// omakase remove, sweeps it again.
func maskApply(repo *state.Repo, blocked map[string]bool, stderr io.Writer) error {
	patterns := maskPatterns(blocked)
	var firstErr error
	for _, wt := range state.WorktreeRoots(repo.Root) {
		if !dirExists(wt) {
			fmt.Fprintf(stderr, "omakase: worktree %s is unreachable; its block state was not updated.\n", wt)
			continue
		}
		var err error
		if len(patterns) == 0 {
			if sparseEnabled(wt) {
				err = runGitQuiet(wt, "sparse-checkout", "disable")
			}
		} else {
			args := append([]string{"sparse-checkout", "set", "--no-cone"}, patterns...)
			err = runGitQuiet(wt, args...)
		}
		if err != nil && firstErr == nil {
			firstErr = fmt.Errorf("worktree %s: %w", wt, err)
		}
	}
	return firstErr
}

// RestoreAll undoes every block: the ledger is emptied (sidecar deleted) and
// sparse-checkout disabled in every reachable worktree. It is remove's hook —
// blocked state joins the every-trace-reversed promise (issue #193) — and is
// a no-op when nothing is blocked.
func RestoreAll(repo *state.Repo, stderr io.Writer) (n int, err error) {
	blocked := state.ReadBlocked(repo.OMK)
	if len(blocked) == 0 {
		return 0, nil
	}
	if err := state.WriteBlocked(repo.OMK, nil); err != nil {
		return 0, err
	}
	return len(blocked), maskApply(repo, nil, stderr)
}

// maskPatterns derives the sparse-checkout pattern list from the blocked
// set: keep everything, then subtract each blocked rel (sorted, so the
// on-disk pattern file is deterministic). Empty set -> nil (disable).
func maskPatterns(blocked map[string]bool) []string {
	if len(blocked) == 0 {
		return nil
	}
	patterns := []string{"/*"}
	for _, rel := range sortedBlocked(blocked) {
		patterns = append(patterns, "!/"+rel)
	}
	return patterns
}

// foreignSparse reports whether sparse-checkout is enabled by something other
// than omakase. With a non-empty ledger the sparse state is omakase's own;
// with an empty one, any worktree already running sparse-checkout belongs to
// the user, and v1 refuses to merge with it rather than risk clobbering
// their patterns (issue #193).
func foreignSparse(repo *state.Repo, blocked map[string]bool) (bool, string) {
	if len(blocked) > 0 {
		return false, ""
	}
	for _, wt := range state.WorktreeRoots(repo.Root) {
		if dirExists(wt) && sparseEnabled(wt) {
			return true, fmt.Sprintf("this repo already uses git sparse-checkout (%s) — omakase won't touch existing sparse patterns; disable it first or manage this file with git directly", wt)
		}
	}
	return false, ""
}

// sparseEnabled reports whether root's checkout has sparse-checkout on
// (core.sparseCheckout resolves true — per-worktree config included).
func sparseEnabled(root string) bool {
	out, err := exec.Command("git", "-C", root, "config", "--get", "core.sparseCheckout").Output()
	return err == nil && strings.TrimSpace(string(out)) == "true"
}

// runGitQuiet runs git in dir, surfacing git's own stderr in the error.
func runGitQuiet(dir string, args ...string) error {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}
