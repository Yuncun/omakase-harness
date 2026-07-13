// This file implements the `omakase hook <name>` plumbing verb — what the
// permanent .git/hooks dispatchers exec (issue #98). In order: env scrub,
// repo discovery from cwd, the not-installed refusal, then per hook kind:
// gate hooks (pre-commit, pre-push) verify the harness is complete and run
// the wired gates through the pinned lefthook, fail-closed at every step;
// post-checkout heals missing placed files natively (the ensure-present.sh
// port) and runs any wired post-checkout jobs best-effort, always exit 0.
//
// Write rules (the #98 boundary): this code never writes anything under the
// shared git dir — hooks, config, and state stay init/remove-only. The only
// writes here are heal's file placements into the working tree.
package overlay

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/Yuncun/omakase-harness/internal/hook"
	"github.com/Yuncun/omakase-harness/internal/lefthook"
	"github.com/Yuncun/omakase-harness/internal/state"
)

// lfsHooks are the hooks git-lfs installs stubs for. When lefthook runs it
// forwards `git lfs <hook>` itself (with our forwarded args and stdin); the
// direct runGitLFS path below covers only the invocations that skip
// lefthook, so displacing a stock git-lfs hook with a dispatcher loses
// nothing.
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
	gate := hook.IsGate(name)

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
		if !gate {
			return 0
		}
		fmt.Fprintf(stderr, "omakase: BLOCKING — %s: not inside a git repository; the harness cannot be verified.\n", name)
		return 1
	}

	// Not installed: no harness state to run. A dispatcher only exists where
	// init wrote it, so this is a torn state (state wiped without `omakase
	// remove`) — gate hooks refuse rather than silently running nothing.
	if !fileRegular(filepath.Join(repo.OMK, "placed.tsv")) {
		if !gate {
			return 0
		}
		fmt.Fprintf(stderr, "omakase: BLOCKING — %s: omakase hooks are installed but no harness state exists in this repo.\n", name)
		fmt.Fprintln(stderr, "omakase: restore it with  omakase init  — or take the hooks out with  omakase remove.")
		return 1
	}

	if gate {
		return runGateHook(name, hookArgs, repo, stdin, stdout, stderr)
	}

	// post-checkout: heal, then wired jobs best-effort. Never fails the
	// checkout.
	healWorktree(repo, stderr)
	runPostJobs(name, hookArgs, repo.Root, stdin, stdout, stderr)
	return 0
}

// runGateHook verifies the harness and runs the wired gates for a
// pre-commit/pre-push fire, fail-closed at every step.
func runGateHook(name string, hookArgs []string, repo *state.Repo, stdin io.Reader, stdout, stderr io.Writer) int {
	root := repo.Root

	// Fail-closed verify (the verify-overlay.sh port): a wiped or partial
	// harness must block, not silently skip its gates. LEFTHOOK=0 does NOT
	// bypass this — the only escape is git's own --no-verify.
	if code := verifyPresent(root, repo.OMK, stderr); code != 0 {
		return code
	}

	// LEFTHOOK=0/false skips the gates by explicit choice (lefthook's own
	// documented switch) — nothing is SILENTLY skipped.
	if lefthookDisabled() {
		return 0
	}

	hasLocal := fileRegular(filepath.Join(root, "lefthook-local.yml"))
	hasMain := fileRegular(filepath.Join(root, "lefthook.yml"))
	if !hasLocal && !hasMain {
		// No wiring at all (a harness that ships no lefthook-local.yml):
		// nothing gated, but a displaced stock git-lfs hook still owes its
		// LFS run — fail closed on its failure like the stock stub did.
		return runGitLFS(name, hookArgs, root, stdin, stdout, stderr, true)
	}

	// The pinned lefthook, resolved but NEVER fetched — no network at
	// commit time; init provisioned the cache.
	lh, ok := lefthook.ResolveForHook(root)
	if !ok {
		fmt.Fprintln(stderr, "omakase: BLOCKING — no lefthook found (LEFTHOOK_BIN, PATH, node_modules/.bin, or the omakase cache): the wired gates cannot run.")
		fmt.Fprintln(stderr, "omakase: restore it with a bare  omakase init  (self-fetches lefthook), or install lefthook / set LEFTHOOK_BIN. Skip once with LEFTHOOK=0; git --no-verify bypasses hooks entirely.")
		return 1
	}

	// lefthook forwards `git lfs <hook>` natively ONLY for a hook its config
	// defines jobs for (verified against the pinned 2.1.9); a gate the
	// wiring does not name — the base harness ships pre-push commented out —
	// would silently lose the displaced stock git-lfs hook's job. Forward it
	// here first, fail closed like the stock stub. (A hook defined only via
	// lefthook `extends:` escapes the key scan and gets both this forward
	// and lefthook's; git-lfs runs are idempotent, so the double forward is
	// spend, not breakage.)
	wired := (hasLocal && wiringDefinesHook(filepath.Join(root, "lefthook-local.yml"), name)) ||
		(hasMain && wiringDefinesHook(filepath.Join(root, "lefthook.yml"), name))
	if !wired {
		if code := runGitLFS(name, hookArgs, root, stdin, stdout, stderr, true); code != 0 {
			return code
		}
	}
	return runLefthook(lh, name, hookArgs, root, !hasMain, stdin, stdout, stderr)
}

// runPostJobs runs a post-checkout's wired jobs and LFS forward,
// best-effort: every failure is swallowed (heal already warned about
// anything actionable). lefthook is spawned only when the wiring names this
// hook — a jobless spawn would print its run header on every checkout. The
// line-anchored key scan cannot see a hook defined only through lefthook's
// `extends:`, an accepted miss on this best-effort path (gate hooks always
// spawn lefthook, so no gate can be skipped that way).
func runPostJobs(name string, hookArgs []string, root string, stdin io.Reader, stdout, stderr io.Writer) {
	if lefthookDisabled() {
		return
	}
	hasLocal := fileRegular(filepath.Join(root, "lefthook-local.yml"))
	hasMain := fileRegular(filepath.Join(root, "lefthook.yml"))
	wired := (hasLocal && wiringDefinesHook(filepath.Join(root, "lefthook-local.yml"), name)) ||
		(hasMain && wiringDefinesHook(filepath.Join(root, "lefthook.yml"), name))
	if wired {
		if lh, ok := lefthook.ResolveForHook(root); ok {
			runLefthook(lh, name, hookArgs, root, !hasMain, stdin, stdout, stderr)
			return // lefthook forwarded `git lfs <hook>` itself
		}
	}
	runGitLFS(name, hookArgs, root, stdin, stdout, stderr, false)
}

// runLefthook spawns `lefthook run <name> <hook args> --no-auto-install`
// from the worktree root and returns its exit code. --no-auto-install is
// load-bearing: without it lefthook's run-time hook sync rewrites
// .git/hooks mid-run — the #96 corruption — and would clobber the
// dispatchers. When the repo has no lefthook.yml of its own, LEFTHOOK_CONFIG
// points lefthook straight at the placed wiring, so no skeleton main config
// ever needs to exist; a repo that ships its own lefthook.yml keeps
// lefthook's default resolution (main config + lefthook-local.yml merged),
// so the project's own jobs still run alongside the harness's.
func runLefthook(lh, name string, hookArgs []string, root string, useLocalConfig bool, stdin io.Reader, stdout, stderr io.Writer) int {
	args := append([]string{"run", name}, hookArgs...)
	args = append(args, "--no-auto-install")
	cmd := exec.Command(lh, args...)
	cmd.Dir = root
	env := make([]string, 0, len(os.Environ())+1)
	for _, kv := range os.Environ() {
		// Drop any inherited LEFTHOOK_CONFIG: a value leaked for another
		// repo would run that repo's gates here (same class as the GIT_DIR
		// scrub above). The harness wiring below is the only config.
		if useLocalConfig && strings.HasPrefix(kv, "LEFTHOOK_CONFIG=") {
			continue
		}
		env = append(env, kv)
	}
	if useLocalConfig {
		env = append(env, "LEFTHOOK_CONFIG="+filepath.Join(root, "lefthook-local.yml"))
	}
	cmd.Env = env
	cmd.Stdin = stdin
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		return exitCode(err)
	}
	return 0
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
		snapEntry := filepath.Join(snap, rel)
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
					fix := "omakase init"
					if lexists(snapEntry) {
						fix = fmt.Sprintf("cp -P '%s' '%s'  (or omakase init to re-sync every file)", snapEntry, dest)
					}
					fmt.Fprintf(stderr, "omakase: WARNING — injected '%s' has DRIFTED from canonical (ledger %s…, on-disk %s…); a gate may be weakened or stale. Drift only surfaces — your copy is left as-is. Adopt canonical with: %s\n", rel, first12(row.Hash), first12(actual), fix)
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

// lefthookDisabled reports lefthook's own documented off switch: LEFTHOOK
// set to "0" or "false".
func lefthookDisabled() bool {
	v := os.Getenv("LEFTHOOK")
	return v == "0" || v == "false"
}

// wiringDefinesHook reports whether the config file at path has a top-level
// `<name>:` key — a cheap line-anchored scan, not a YAML parse (a top-level
// key starts at column 0, so a comment or nested key can never match).
func wiringDefinesHook(path, name string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		if strings.HasPrefix(sc.Text(), name+":") {
			return true
		}
	}
	return false
}

// first12 is the sh scripts' ${hash:0:12} — the digest prefix the drift
// warning prints.
func first12(s string) string {
	if len(s) > 12 {
		return s[:12]
	}
	return s
}
