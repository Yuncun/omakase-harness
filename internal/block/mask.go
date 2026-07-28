// This file is the blocking mechanism: non-cone sparse-checkout. A blocked
// item is subtracted from the working tree ("/*" keeps everything, "!/<rel>"
// hides one item) — hosts discover steering files by their presence on disk,
// so absent-from-tree means not-loaded, on every host and for every item
// kind including the ones no host offers a switch for (instruction files,
// hooks). git keeps tracking the file: the index is untouched, so commits,
// pulls, and pushes behave normally and reversal is byte-perfect.
//
// The overlay and the mask cannot collide: sparse-checkout only subtracts
// TRACKED files, the overlay only adds UNTRACKED ones — disjoint sets. And
// non-cone mode is load-bearing for that: CONE mode silently deletes ignored
// files outside the cone, which would eat the overlay.
//
// The mask is advisory, not a guarantee (2026-07-28 hardening pass): git am,
// merge --abort, `checkout <ref> -- <path>`, and a conflict touching the
// masked file all rematerialize it silently with a clean status. So this
// file never trusts the mask — every apply verifies the tree afterward, and
// Reassert (run by the session-start/post-checkout heal) re-hides what a git
// operation brought back.
package block

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/Yuncun/omakase-harness/internal/state"
)

// maskApply makes every reachable worktree's working tree match the blocked
// set: `git sparse-checkout set --no-cone` with the derived patterns
// (argv, never --stdin — git 2.43.0, Ubuntu 24.04's git, mis-handles stdin),
// or `git sparse-checkout disable` + residue cleanup when the set is empty.
// Every non-empty apply is verified (verifyHidden) — a pattern git accepted
// but did not honor must fail loudly, not mask nothing at exit 0. An
// unreachable worktree gets a warning and is skipped — the next
// block/unblock, or omakase remove, sweeps it again.
func maskApply(repo *state.Repo, blocked map[string]bool, stderr io.Writer) error {
	var firstErr error
	for _, wt := range state.WorktreeRoots(repo.Root) {
		if !dirExists(wt) {
			fmt.Fprintf(stderr, "omakase: worktree %s is unreachable; its block state was not updated.\n", wt)
			continue
		}
		if err := applyWorktree(wt, blocked); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("worktree %s: %w", wt, err)
		}
	}
	return firstErr
}

// applyWorktree applies (or, for an empty set, reverses) the mask in one
// worktree and verifies the result.
func applyWorktree(wt string, blocked map[string]bool) error {
	if len(blocked) == 0 {
		if !sparseActive(wt) {
			return nil
		}
		if err := runGitQuiet(wt, "sparse-checkout", "disable"); err != nil {
			return err
		}
		// disable restores every file but leaves the pattern file behind, and
		// a later `git sparse-checkout init` would silently reuse it. The
		// patterns were omakase's own (foreign sparse is refused before the
		// first block), so delete the residue. extensions.worktreeConfig is
		// left alone: other worktrees may rely on it for config resolution.
		if p, err := gitPath(wt, "info/sparse-checkout"); err == nil {
			os.Remove(p)
		}
		return nil
	}
	args := append([]string{"sparse-checkout", "set", "--no-cone"}, maskPatterns(blocked)...)
	if err := runGitQuiet(wt, args...); err != nil {
		return err
	}
	return verifyHidden(wt, blocked)
}

// verifyHidden fails when any blocked item is still present in wt's working
// tree. git accepts patterns it then does not honor — and several operations
// rematerialize masked files — so presence on disk, not git's exit code, is
// the property that matters (it is exactly what hosts probe).
func verifyHidden(wt string, blocked map[string]bool) error {
	for _, rel := range sortedBlocked(blocked) {
		if _, err := os.Lstat(wt + "/" + rel); err == nil {
			return fmt.Errorf("%s is still present after masking — if a merge or rebase involves it, finish or abort that first, then re-run", rel)
		}
	}
	return nil
}

// Reassert re-hides blocked items a git operation brought back into THIS
// worktree (git am, merge --abort, a resolved conflict — the hardening
// pass's silent-rematerialization list). Run by the session-start and
// post-checkout heals. It returns how many items were re-hidden; 0 when
// nothing is blocked, everything is already hidden, or a merge/rebase is in
// progress (never fight an operation that materialized the file to do its
// work). Failures are reported, never fatal — heal contexts must not block.
func Reassert(repo *state.Repo, stderr io.Writer) int {
	blocked := state.ReadBlocked(repo.OMK)
	if len(blocked) == 0 || opInProgress(repo.Root) {
		return 0
	}
	visible := 0
	for rel := range blocked {
		if _, err := os.Lstat(repo.Root + "/" + rel); err == nil {
			visible++
		}
	}
	if visible == 0 {
		return 0
	}
	if err := applyWorktree(repo.Root, blocked); err != nil {
		fmt.Fprintf(stderr, "omakase: could not re-hide blocked item(s): %v\n", err)
		return 0
	}
	return visible
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
		patterns = append(patterns, "!/"+escapePattern(rel))
	}
	return patterns
}

// escapePattern backslash-escapes gitignore-pattern syntax inside a literal
// path. Blocked rels come from `git ls-files`, i.e. from the repo being
// blocked — untrusted input by definition — and an unescaped `*`, `?`, `[`
// or `\` turns one committed path into a glob masking MORE than itself
// (`star*` over-masks) or into a pattern git silently fails to honor
// (`brack[et]` stays present at exit 0; both verified empirically). A
// trailing space is escaped too (git trims unescaped trailing whitespace).
func escapePattern(rel string) string {
	var b strings.Builder
	for _, r := range rel {
		if r == '\\' || r == '*' || r == '?' || r == '[' || r == ']' {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	s := b.String()
	if strings.HasSuffix(s, " ") {
		s = s[:len(s)-1] + "\\ "
	}
	return s
}

// foreignSparse reports whether sparse-checkout is enabled by something other
// than omakase. With a non-empty ledger the sparse state is omakase's own;
// with an empty one, any worktree already running sparse-checkout belongs to
// the user, and v1 refuses to merge with it rather than risk clobbering
// their patterns (issue #193 — a plain `set` restores their whole tree
// silently).
func foreignSparse(repo *state.Repo, blocked map[string]bool) (bool, string) {
	if len(blocked) > 0 {
		return false, ""
	}
	for _, wt := range state.WorktreeRoots(repo.Root) {
		if dirExists(wt) && sparseActive(wt) {
			return true, fmt.Sprintf("this repo already uses git sparse-checkout (%s) — omakase won't touch existing sparse patterns; disable it first or manage this file with git directly", wt)
		}
	}
	return false, ""
}

// sparseActive reports whether root's checkout currently has sparse-checkout
// on. `git sparse-checkout list` is the robust probe (hardening area 1): it
// exits 128 on a non-sparse worktree, while the pattern file and stale
// config can both survive a disable.
func sparseActive(root string) bool {
	return exec.Command("git", "-C", root, "sparse-checkout", "list").Run() == nil
}

// opInProgress reports whether a merge, rebase, or cherry-pick is underway
// in root's checkout. Masking mid-operation deadlocks: the conflicted path
// cannot be re-added except with --sparse, and reapply cannot help.
func opInProgress(root string) bool {
	for _, marker := range []string{"MERGE_HEAD", "CHERRY_PICK_HEAD", "rebase-merge", "rebase-apply"} {
		p, err := gitPath(root, marker)
		if err != nil {
			continue
		}
		if _, err := os.Lstat(p); err == nil {
			return true
		}
	}
	return false
}

// gitMaskSupported reports whether root's git can run the one-line non-cone
// apply. Floor: 2.35.0, where `sparse-checkout set` learned --no-cone
// (Ubuntu 22.04's 2.34.1 is below it). ok is false only on a confident
// too-old parse; an unparseable version string passes — git's own error
// then names the problem.
func gitMaskSupported(root string) (ok bool, version string) {
	out, err := exec.Command("git", "-C", root, "version").Output()
	if err != nil {
		return true, ""
	}
	version = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(string(out)), "git version "))
	parts := strings.SplitN(version, ".", 3)
	if len(parts) < 2 {
		return true, version
	}
	major, err1 := strconv.Atoi(parts[0])
	minor, err2 := strconv.Atoi(strings.TrimFunc(parts[1], func(r rune) bool { return r < '0' || r > '9' }))
	if err1 != nil || err2 != nil {
		return true, version
	}
	return major > 2 || (major == 2 && minor >= 35), version
}

// gitPath resolves a $GIT_DIR-relative name for root's checkout —
// per-worktree correct (a linked worktree's info/ lives under
// .git/worktrees/<name>/).
func gitPath(root, name string) (string, error) {
	out, err := exec.Command("git", "-C", root, "rev-parse", "--git-path", name).Output()
	if err != nil {
		return "", err
	}
	p := strings.TrimSpace(string(out))
	if !strings.HasPrefix(p, "/") {
		p = root + "/" + p
	}
	return p, nil
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
