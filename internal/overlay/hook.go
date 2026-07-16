// This file implements the `omakase hook <name>` plumbing verb — what the
// permanent .git/hooks dispatchers exec (issue #98). In order: env scrub,
// repo discovery from cwd, the not-installed refusal, then per hook kind:
// gate hooks (pre-commit, pre-push) verify the harness is complete, forward
// any stock git-lfs hook, and run the manifest-declared gates through
// internal/gate, fail-closed at every step; post-checkout heals missing
// placed files natively (the ensure-present.sh port) and forwards git-lfs
// best-effort, always exit 0.
//
// Write rules (the #98 boundary): this code never writes anything under the
// shared git dir — hooks and dispatcher config stay init/remove-only. The
// gate ledger under the shared git dir is the one exception (a hook run
// records verdicts, exactly as omakase-gate.sh did); heal's writes land in
// the working tree.
package overlay

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/Yuncun/omakase-harness/internal/gate"
	"github.com/Yuncun/omakase-harness/internal/hook"
	"github.com/Yuncun/omakase-harness/internal/state"
)

// lfsHooks are the hooks git-lfs installs stubs for. `omakase hook` forwards
// `git lfs <hook>` itself for these (with our forwarded args and stdin), so
// displacing a stock git-lfs hook with a dispatcher loses nothing.
var lfsHooks = map[string]bool{
	"pre-push": true, "post-checkout": true, "post-commit": true, "post-merge": true,
}

// RunHook is the `omakase hook <name>` verb. argv is the arguments after
// the verb: the hook name first, then whatever git passed the hook. stdin
// is forwarded to the gate runner (pre-push ref lines). Gate hooks return
// non-zero to block the commit/push; post-checkout always returns 0.
func RunHook(argv []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(argv) < 1 || !hook.Known(argv[0]) {
		fmt.Fprintln(stderr, "usage: omakase hook <pre-commit|pre-push|post-checkout> [hook args...]")
		return 2
	}
	name := argv[0]
	hookArgs := argv[1:]
	isGate := hook.IsGate(name)

	// A leaked GIT_DIR/GIT_WORK_TREE/GIT_COMMON_DIR (exported for ANOTHER
	// repo by a wrapper or a parent hook) would misdirect every git call
	// below — and the lefthook child. This hook's repo is always the one it
	// fires in: resolve from cwd only (the PR #91 lesson). GIT_INDEX_FILE is
	// deliberately kept: git points it at the temporary index during partial
	// commits, and the gates must see that staged set.
	for _, v := range []string{"GIT_DIR", "GIT_WORK_TREE", "GIT_COMMON_DIR"} {
		os.Unsetenv(v)
	}

	wd, err := os.Getwd()
	var repo *state.Repo
	if err == nil {
		repo, err = state.Discover(wd)
	}
	if err != nil {
		if !isGate {
			return 0
		}
		fmt.Fprintf(stderr, "omakase: BLOCKING — %s: not inside a git repository; the harness cannot be verified.\n", name)
		return 1
	}

	// Not installed: no harness state to run. A dispatcher only exists where
	// init wrote it, so this is a torn state (state wiped without `omakase
	// remove`) — gate hooks refuse rather than silently running nothing.
	if !fileRegular(filepath.Join(repo.OMK, "placed.tsv")) {
		if !isGate {
			return 0
		}
		fmt.Fprintf(stderr, "omakase: BLOCKING — %s: omakase hooks are installed but no harness state exists in this repo.\n", name)
		fmt.Fprintln(stderr, "omakase: restore it with  omakase init  — or take the hooks out with  omakase remove.")
		return 1
	}

	if isGate {
		return runGateHook(name, hookArgs, repo, stdin, stdout, stderr)
	}

	// post-checkout: heal, then forward git-lfs best-effort. Never fails the
	// checkout.
	healWorktree(repo, stderr)
	runGitLFS(name, hookArgs, repo.Root, stdin, stdout, stderr, false)
	return 0
}

// runGateHook verifies the harness, forwards any stock git-lfs hook, and runs
// the manifest-declared gates for a pre-commit/pre-push fire, fail-closed at
// every step.
func runGateHook(name string, hookArgs []string, repo *state.Repo, stdin io.Reader, stdout, stderr io.Writer) int {
	root := repo.Root

	// Fail-closed verify (the verify-overlay.sh port): a wiped or partial
	// harness must block, not silently skip its gates. OMAKASE_SKIP_GATES does
	// NOT bypass this — the only escape is git's own --no-verify.
	if code := verifyPresent(root, repo.OMK, stderr); code != 0 {
		return code
	}

	// A displaced stock git-lfs hook still owes its LFS run (pre-push): forward
	// it, fail closed on its failure like the stock stub did. This is not a
	// gate, so OMAKASE_SKIP_GATES never reaches it. No-op for pre-commit
	// (which git-lfs does not stub).
	if code := runGitLFS(name, hookArgs, root, stdin, stdout, stderr, true); code != 0 {
		return code
	}

	// Run the manifest-declared gates. gate.RunHook reads the gate list from
	// the init-written snapshot manifest only (the one-writer invariant on
	// wiring), records verdicts, and passes a blocking gate's exit code through.
	return gate.RunHook(name, root, repo.OMK, stdin, stdout, stderr)
}

// runGitLFS forwards `git lfs <name> <args>` the way a stock git-lfs hook
// stub would, for invocations where lefthook (which forwards LFS itself)
// does not run. Mirrors the stub's `command -v git-lfs` guard: no git-lfs
// on PATH means nothing to forward. failClosed propagates a non-zero exit
// (gate hooks); post-checkout callers pass false.
func runGitLFS(name string, args []string, root string, stdin io.Reader, stdout, stderr io.Writer, failClosed bool) int {
	if !lfsHooks[name] {
		return 0
	}
	if _, err := exec.LookPath("git-lfs"); err != nil {
		return 0
	}
	cmd := exec.Command("git", append([]string{"lfs", name}, args...)...)
	cmd.Dir = root
	cmd.Stdin = stdin
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil && failClosed {
		return exitCode(err)
	}
	return 0
}

// verifyPresent blocks when any enabled placed row is missing from this
// worktree (the verify-overlay.sh port): a wiped overlay means the wired
// gates would silently not run. A tracked path is upstream's (warned at
// init/checkout); disabled rows are deliberately absent.
func verifyPresent(root, omk string, stderr io.Writer) int {
	missing := 0
	for _, row := range state.ReadPlaced(filepath.Join(omk, "placed.tsv")) {
		if row.Enabled != "1" {
			continue
		}
		if lexists(filepath.Join(root, row.Rel)) {
			continue
		}
		if gitTracked(root, row.Rel) {
			continue
		}
		if missing == 0 {
			fmt.Fprintln(stderr, "omakase: BLOCKING — the injected harness is incomplete; its gates would silently not run:")
		}
		fmt.Fprintf(stderr, "  missing: %s\n", row.Rel)
		missing++
	}
	if missing == 0 {
		return 0
	}
	fmt.Fprintln(stderr, "omakase: restore it with  omakase init  and retry.")
	return 1
}

// healWorktree is the ensure-present.sh placement loop, ported (issue #98):
// copy any MISSING enabled placed file into this worktree from the shared
// snapshot. Ensure-present / never-overwrite: safe on every checkout, never
// clobbers a local edit, never writes a tracked path, warns on the
// upstream-collision and drift cases. Wholly best-effort — a failed step
// warns or skips, never fails the checkout. Unlike the sh original this
// refuses to create dest parents through a planted directory symlink
// (safeMkdirAll), matching init's placement hardening.
func healWorktree(repo *state.Repo, stderr io.Writer) {
	root := repo.Root
	snap := filepath.Join(repo.OMK, "payload-snapshot")
	umask := currentUmask()
	for _, row := range state.ReadPlaced(filepath.Join(repo.OMK, "placed.tsv")) {
		// enabled=0 is a deliberate off switch: a missing disabled artifact
		// is not "missing" — never resurrect it.
		if row.Enabled != "1" {
			continue
		}
		rel := row.Rel
		dest := filepath.Join(root, rel)
		// The accepted (kept) copy outranks the harness version: refilling a
		// kept file must restore what the user consented to, and the drift
		// warning below must speak in kept terms (issue #98 Part 2).
		snapEntry := filepath.Join(snap, rel)
		kept := false
		if k := keptEntry(repo.OMK, rel); lexists(k) {
			snapEntry, kept = k, true
		}
		// Tracked first — a tracked file exists in the working tree, so an
		// existence-first order would skip the collision warning silently.
		if gitTracked(root, rel) {
			fmt.Fprintf(stderr, "omakase: WARNING — injected path '%s' is now TRACKED by the repo; your personal copy was likely clobbered by an upstream commit (git overwrites ignored files on checkout). Last-injected copy: %s — diff it against the tracked file, then drop the path from your payload or cut over (init --cut-over).\n", rel, snapEntry)
			continue
		}
		// Present (including a dangling symlink): NEVER overwrite — but
		// surface drift, the present-but-changed state that would otherwise
		// look installed and green while a gate is weakened or stale.
		if lexists(dest) {
			if row.Hash != "" {
				if actual := state.HashOf(dest); actual != "" && actual != row.Hash {
					if kept {
						// A kept file's baseline is the ACCEPTED version; the
						// cp fix below would silently discard this newest
						// edit, so the kept path points at the lifecycle
						// verbs instead.
						fmt.Fprintf(stderr, "omakase: WARNING — '%s' differs from your accepted (kept) version. Your copy is left as-is. See the change:  omakase diff %s  — then keep it (omakase status --keep %s) or go back (omakase status --restore %s).\n", rel, rel, rel, rel)
					} else {
						fix := "omakase init"
						if lexists(snapEntry) {
							fix = fmt.Sprintf("cp -P '%s' '%s'  (or omakase init to re-sync every file)", snapEntry, dest)
						}
						fmt.Fprintf(stderr, "omakase: WARNING — injected '%s' has DRIFTED from canonical (ledger %s…, on-disk %s…); a gate may be weakened or stale. Drift only surfaces — your copy is left as-is. Adopt canonical with: %s\n", rel, first12(row.Hash), first12(actual), fix)
					}
				}
			}
			continue
		}
		if !lexists(snapEntry) {
			continue
		}
		if err := safeMkdirAll(root, filepath.Dir(dest)); err != nil {
			fmt.Fprintf(stderr, "omakase: %v\n", err)
			continue
		}
		if err := CopyEntry(snapEntry, dest); err != nil {
			continue
		}
		if strings.HasSuffix(rel, ".sh") && !isSymlink(dest) {
			if info, statErr := os.Stat(dest); statErr == nil {
				os.Chmod(dest, info.Mode().Perm()|(0o111&^umask))
			}
		}
	}
}

// first12 is the sh scripts' ${hash:0:12} — the digest prefix the drift
// warning prints.
func first12(s string) string {
	if len(s) > 12 {
		return s[:12]
	}
	return s
}
