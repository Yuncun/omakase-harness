// This file is the `omakase block` / `omakase unblock` verb entry point:
// flag parsing, repo discovery, resolution against the committed surface, the
// two-step consent (a run without --yes states the consequence and applies
// nothing — the binary never prompts), and the human-readable outcome lines.
// The working-tree mechanism itself lives in mask.go.
package block

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Yuncun/omakase-harness/internal/harness"
	"github.com/Yuncun/omakase-harness/internal/state"
)

// Run is both verbs; unblock selects the direction. argv is the arguments
// after the verb. Exit codes follow the toggle conventions: 0 success or
// no-op, 1 environment/apply failure, 2 refusal (unknown item, missing
// consent, foreign sparse-checkout).
func Run(unblock bool, argv []string, stdout, stderr io.Writer) int {
	verb := "block"
	if unblock {
		verb = "unblock"
	}

	yes := false
	item := ""
	for _, a := range argv {
		switch {
		case a == "--help" || a == "-h":
			printUsage(stdout)
			return 0
		case a == "--yes":
			yes = true
		case strings.HasPrefix(a, "-"):
			fmt.Fprintf(stderr, "omakase: unknown flag %s (see omakase %s --help)\n", a, verb)
			return 2
		case item == "":
			item = a
		default:
			fmt.Fprintf(stderr, "omakase: %s takes one item (see omakase %s --help)\n", verb, verb)
			return 2
		}
	}
	if item == "" {
		fmt.Fprintf(stderr, "omakase: %s needs an item — a committed path or a skill/agent name (the list: omakase status)\n", verb)
		return 2
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

	committed := state.TrackedUnder(repo.Root, harness.CommittedGlobs)
	blocked := state.ReadBlocked(repo.OMK)
	item = normalizeItemArg(repo.Root, wd, committed, blocked, item)

	if unblock {
		return runUnblock(repo, committed, blocked, item, stdout, stderr)
	}
	return runBlock(repo, committed, blocked, item, yes, stdout, stderr)
}

// normalizeItemArg maps the spellings a shell hands out — an absolute path
// (tab completion), or a path relative to a subdirectory cwd — onto the
// repo-root-relative form everything downstream speaks. The raw argument
// wins when it already resolves; otherwise the first rewritten candidate
// that resolves (against the committed surface or the blocked ledger) is
// used, and if none do, the raw argument passes through so error messages
// name what the user typed.
func normalizeItemArg(root, wd string, committed []string, blocked map[string]bool, arg string) string {
	resolves := func(c string) bool {
		if _, _, err := Resolve(committed, c); err == nil {
			return true
		}
		return blocked[strings.TrimSuffix(strings.TrimPrefix(c, "./"), "/")]
	}
	if resolves(arg) {
		return arg
	}
	full := arg
	if !filepath.IsAbs(full) {
		if wd == root {
			return arg
		}
		full = filepath.Join(wd, arg)
	}
	if rel, err := filepath.Rel(root, full); err == nil && rel != ".." && !strings.HasPrefix(rel, "../") && resolves(rel) {
		return rel
	}
	return arg
}

func runBlock(repo *state.Repo, committed []string, blocked map[string]bool, arg string, yes bool, stdout, stderr io.Writer) int {
	rel, covered, err := Resolve(committed, arg)
	if err != nil {
		fmt.Fprintf(stderr, "omakase: %v\n", err)
		placedHint(repo, arg, stderr)
		return 2
	}
	// The ledger is a line-oriented file: a rel containing a newline (or a
	// bare CR, which the reader strips) cannot round-trip it — the block
	// would read back as a different path and mask the wrong thing.
	if strings.ContainsAny(rel, "\n\r") {
		fmt.Fprintf(stderr, "omakase: %q contains a newline and cannot be blocked\n", rel)
		return 2
	}
	if code := preflight(repo, blocked, stderr); code != 0 {
		return code
	}
	if blocked[rel] {
		// Not a pure no-op: several git operations rematerialize a masked
		// file silently (git am, merge --abort), so re-blocking re-applies
		// and re-verifies — the manual heal path status points at.
		if err := writeAndApply(repo, blocked, stderr); err != nil {
			fmt.Fprintf(stderr, "omakase: %v\n", err)
			return 1
		}
		fmt.Fprintf(stdout, "omakase: %s is already blocked — mask re-applied and verified\n", rel)
		return 0
	}
	if !yes {
		fmt.Fprintf(stdout, "%s steers agents in this repo (%s).\n", rel, describeCovered(rel, covered))
		fmt.Fprintf(stdout, "Blocking hides it from this clone's working tree — agents, editors, and builds\n")
		fmt.Fprintf(stdout, "stop seeing it. git still tracks it: nothing is deleted, commits and pulls are\n")
		fmt.Fprintf(stdout, "unaffected, and  omakase unblock %s  puts it back exactly.\n", arg)
		fmt.Fprintf(stdout, "\nTo proceed:  omakase block %s --yes\n", arg)
		return 2
	}

	blocked[rel] = true
	if err := writeAndApply(repo, blocked, stderr); err != nil {
		fmt.Fprintf(stderr, "omakase: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "omakase: blocked — %s is hidden from the working tree (restore:  omakase unblock %s)\n", rel, arg)
	return 0
}

func runUnblock(repo *state.Repo, committed []string, blocked map[string]bool, arg string, stdout, stderr io.Writer) int {
	// Unblock resolves against the blocked set itself first, so a stale block
	// (the file no longer committed upstream) is always reversible; the
	// committed-surface resolution is the fallback for shorthand names.
	rel := strings.TrimSuffix(strings.TrimPrefix(arg, "./"), "/")
	if !blocked[rel] {
		r, _, err := Resolve(committed, arg)
		if err != nil || !blocked[r] {
			fmt.Fprintf(stderr, "omakase: %s is not blocked (blocked items:  omakase status)\n", arg)
			return 2
		}
		rel = r
	}
	if code := preflight(repo, blocked, stderr); code != 0 {
		return code
	}

	delete(blocked, rel)
	if err := writeAndApply(repo, blocked, stderr); err != nil {
		fmt.Fprintf(stderr, "omakase: %v\n", err)
		return 1
	}
	if len(blocked) == 0 {
		// The ledger was $OMK's only blocked-state content; drop the dir if
		// that leaves it empty, so a never-installed repo reads as such
		// everywhere (remove's install sentinel included).
		os.Remove(repo.OMK)
	}
	fmt.Fprintf(stdout, "omakase: unblocked — %s is back in the working tree\n", rel)
	return 0
}

// preflight is the shared refuse-before-write gate: a git below the
// non-cone floor, a merge/rebase/cherry-pick underway in any reachable
// worktree (masking mid-operation deadlocks the conflicted path), or
// sparse-checkout state omakase does not own.
func preflight(repo *state.Repo, blocked map[string]bool, stderr io.Writer) int {
	if ok, v := gitMaskSupported(repo.Root); !ok {
		fmt.Fprintf(stderr, "omakase: block needs git 2.35 or newer (this is git %s)\n", v)
		return 1
	}
	for _, wt := range state.WorktreeRoots(repo.Root) {
		if dirExists(wt) && opInProgress(wt) {
			fmt.Fprintf(stderr, "omakase: a merge, rebase, or cherry-pick is in progress in %s — finish or abort it first\n", wt)
			return 2
		}
	}
	if foreign, msg := foreignSparse(repo, blocked); foreign {
		fmt.Fprintf(stderr, "omakase: %s\n", msg)
		return 2
	}
	if wt, diverged := divergedPatterns(repo, blocked); diverged {
		fmt.Fprintf(stderr, "omakase: %s: the sparse-checkout patterns were changed outside omakase — a rewrite would drop those changes; restore them (review: git -C %s sparse-checkout list) or clear your additions first\n", wt, wt)
		return 2
	}
	return 0
}

// writeAndApply persists the blocked set and makes the working tree match.
// Ledger-first on purpose: a crash between the two writes leaves a blocked
// mark whose masking has not applied yet — visible in status and re-applied
// by the next block/unblock — rather than a masked tree with no record.
func writeAndApply(repo *state.Repo, blocked map[string]bool, stderr io.Writer) error {
	if err := os.MkdirAll(repo.OMK, 0o755); err != nil {
		return err
	}
	if err := state.WriteBlocked(repo.OMK, blocked); err != nil {
		return err
	}
	return maskApply(repo, blocked, stderr)
}

// describeCovered summarizes what a block covers: the file itself, or the n
// committed files under a directory item.
func describeCovered(rel string, covered []string) string {
	if len(covered) == 1 && covered[0] == rel {
		return harness.KindOf(rel)
	}
	if len(covered) == 1 {
		return fmt.Sprintf("%s, 1 committed file", harness.KindOf(rel+"/x"))
	}
	return fmt.Sprintf("%s, %d committed files", harness.KindOf(rel+"/x"), len(covered))
}

// placedHint adds one line when the failed argument names a file omakase
// itself placed: that file has its own switch.
func placedHint(repo *state.Repo, arg string, stderr io.Writer) {
	norm := strings.TrimSuffix(strings.TrimPrefix(arg, "./"), "/")
	for _, row := range state.ReadPlaced(filepath.Join(repo.OMK, "placed.tsv")) {
		if row.Rel == norm {
			fmt.Fprintf(stderr, "omakase: (omakase placed that file — its switch is:  omakase status --disable %s)\n", norm)
			return
		}
	}
}

// sortedBlocked is the blocked set as a sorted slice.
func sortedBlocked(blocked map[string]bool) []string {
	rels := make([]string, 0, len(blocked))
	for r := range blocked {
		rels = append(rels, r)
	}
	sort.Strings(rels)
	return rels
}

func printUsage(w io.Writer) {
	fmt.Fprint(w, `usage: omakase block <item> [--yes]
       omakase unblock <item>

Per-item consent over the repo's OWN committed agent config (instruction
files, skills, agents, prompts, hooks). Blocking hides the item from this
clone's working tree so no agent or tool loads it; git still tracks it —
nothing is deleted, commits and pulls are unaffected, and unblock restores
it exactly. Items omakase itself placed have their own switch
(omakase status --disable).

  <item>   a committed path (from omakase status), a directory of them,
           or a bare skill/agent name when unambiguous
  --yes    apply; without it, block only explains what would happen
`)
}
