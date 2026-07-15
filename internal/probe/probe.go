// Package probe collects the verifiable facts about the harness installed
// in a repository — the data behind `omakase statusline` and `omakase
// stop-notice`. It returns facts only: no display strings, no colors, no
// verdict words; internal/render owns every user-facing string (issue #85's
// one-render-layer rule, so a wording change never touches a probe).
//
// Every proof is tri-state. OK and Problem are affirmative findings backed
// by evidence; Unknown means the probe could not verify (a git failure, an
// unreadable file). Unknown is the zero value on purpose: a proof nobody
// ran must never read as good, and a renderer must never paint Unknown as
// working (issue #85's green-requires-proof invariant).
package probe

import (
	"bufio"
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/Yuncun/omakase-harness/internal/hook"
	"github.com/Yuncun/omakase-harness/internal/state"
)

// Tri is the outcome of one proof.
type Tri int

const (
	Unknown Tri = iota // could not verify — never treat as OK
	OK
	Problem
)

// HookIssue pins a HooksInstalled Problem to its cause — a fact, not a
// message; render owns the wording (the amber-string decision is issue #98
// PR C).
type HookIssue int

const (
	HookIssueNone    HookIssue = iota
	HookIssueAbsent            // a dispatcher file is missing
	HookIssueForeign           // a hook file exists but is not omakase's dispatcher (e.g. `lefthook install -f` rewrote it, or a pre-#98 install awaiting its migration init)
	HookIssueBinary            // dispatchers intact but the machine-wide binary copy they exec is gone: commits would block, not silently skip
)

// RunSummary is the most recent hook run recorded in $OMK/ledger.tsv: the
// newest run identified by commit sha, summarised latest-verdict-per-gate
// (a gate that failed then passed on the same sha counts as passed). Rows
// with an empty sha (a pre-commit on an unborn HEAD) are ignored so they
// cannot mask a later real run.
type RunSummary struct {
	Checks int   // gates in the run
	Failed int   // gates whose latest verdict is not "pass"
	Epoch  int64 // newest row epoch in the run
}

// State is the fact sheet for one repository. Identity fields are plain
// facts; the three Tri fields are the proofs whose conjunction means "the
// harness is verifiably working".
type State struct {
	Installed bool // $OMK/placed.tsv exists; when false only Root/OMK are meaningful

	// Identity facts.
	Project      string // basename of the main worktree's root
	Branch       string // current branch, or the short sha when detached
	Source       string // $OMK/source first line; "" = bare base install
	NameOverride string // $OMAKASE_NAME, else .omakase/NAME; "" = none
	BaseVersion  string // .omakase/VERSION first line

	// Proofs. "Installed" follows the field's vocabulary — no coined words
	// like "armed" on any surface.
	HooksInstalled Tri       // gate hooks are omakase dispatchers AND their binary target exists
	HookIssue      HookIssue // why HooksInstalled is Problem (HookIssueNone otherwise)
	FilesPresent   Tri       // every enabled placed row exists in this worktree
	HashesMatch    Tri       // no enabled row drifted from its ledger hash

	// Kept counts the enabled rows whose $OMK/kept/<rel> accepted copy
	// exists — files the user edited and consented to keep (#98 Part 2).
	// A fact, not a problem: keep moved the ledger hash to the accepted
	// version, so kept rows read green through the proofs above.
	Kept int

	LastRun *RunSummary // nil when the ledger records no sha-bearing run

	// Paths, for callers that persist per-repo state (the stop-notice marker).
	Root string
	OMK  string
}

// Collect probes the repository containing cwd. It errors only when cwd is
// not inside a git repository; every failure past discovery degrades to
// Unknown on the affected proof instead, so one broken probe cannot take
// down the rest of the fact sheet.
func Collect(cwd string) (*State, error) {
	repo, err := state.Discover(cwd)
	if err != nil {
		return nil, err
	}
	st := &State{Root: repo.Root, OMK: repo.OMK}

	if _, err := os.Stat(filepath.Join(repo.OMK, "placed.tsv")); err != nil {
		return st, nil // not installed: identity and proofs stay zero
	}
	st.Installed = true

	// Identity. The project is named by the MAIN worktree's root (first
	// WorktreeRoots entry), so a linked worktree still reports the project
	// it belongs to, not its own folder name.
	st.Project = filepath.Base(state.WorktreeRoots(repo.Root)[0])
	st.Branch = branch(repo.Root)
	st.Source = state.FirstLine(filepath.Join(repo.OMK, "source"))
	st.BaseVersion = state.FirstLine(filepath.Join(repo.Root, ".omakase", "VERSION"))
	st.NameOverride = os.Getenv("OMAKASE_NAME")
	if st.NameOverride == "" {
		n := state.FirstLine(filepath.Join(repo.Root, ".omakase", "NAME"))
		st.NameOverride = strings.TrimSpace(n)
	}

	// Proofs.
	st.HooksInstalled, st.HookIssue = hooksInstalled(repo.Root)
	st.FilesPresent, st.HashesMatch = files(repo.Root, repo.OMK)
	st.Kept = keptCount(repo.OMK)

	st.LastRun = lastRun(filepath.Join(repo.OMK, "ledger.tsv"))
	return st, nil
}

// branch is the current branch name, or the short sha when detached, or ""
// when git cannot answer. symbolic-ref (not rev-parse --abbrev-ref) so an
// unborn branch — a fresh repo before its first commit — still names itself.
func branch(root string) string {
	if out, err := gitOut(root, "symbolic-ref", "--short", "-q", "HEAD"); err == nil && out != "" {
		return out
	}
	sha, err := gitOut(root, "rev-parse", "--short", "HEAD")
	if err != nil {
		return ""
	}
	return sha
}

// hooksInstalled proves whether a commit/push in this repo would actually
// run the harness: each gate hook in git's effective hooks dir (rev-parse
// --git-path hooks honors core.hooksPath) must be byte-equal to the
// dispatcher omakase writes for that name — a substring test would call a
// clobbered hook healthy — and the machine-wide binary copy the dispatchers
// exec must exist (a dispatcher with no binary behind it blocks every
// commit; status must say so before the user hits that wall, the #72
// lesson). Missing or foreign hooks are an affirmative Problem with the
// cause pinned; a git failure or an unreadable hook file is Unknown.
func hooksInstalled(root string) (Tri, HookIssue) {
	dir, err := gitOut(root, "rev-parse", "--git-path", "hooks")
	if err != nil {
		return Unknown, HookIssueNone
	}
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(root, dir)
	}
	unreadable, absent := false, false
	for _, h := range []string{"pre-commit", "pre-push"} {
		b, err := os.ReadFile(filepath.Join(dir, h))
		if err != nil {
			if os.IsNotExist(err) {
				absent = true
			} else {
				unreadable = true
			}
			continue
		}
		if !bytes.Equal(b, hook.Dispatcher(h)) {
			return Problem, HookIssueForeign
		}
	}
	if unreadable {
		return Unknown, HookIssueNone
	}
	if absent {
		return Problem, HookIssueAbsent
	}
	stable := hook.StableBinPath()
	if stable == "" {
		return Unknown, HookIssueNone
	}
	if info, err := os.Stat(stable); err != nil || !info.Mode().IsRegular() || info.Mode()&0o111 == 0 {
		return Problem, HookIssueBinary
	}
	return OK, HookIssueNone
}

// files walks the enabled placed.tsv rows of this worktree and proves
// presence and hash fidelity. A tracked path never drifts (upstream owns
// it), a row without a ledger hash cannot be judged and is skipped, and an
// unreadable path degrades that proof to Unknown rather than guessing.
func files(root, omk string) (present, hashes Tri) {
	rows := state.ReadPlaced(filepath.Join(omk, "placed.tsv"))
	var missing, drifted int
	presentUnknown, hashUnknown := false, false
	for _, r := range rows {
		if r.Enabled != "1" {
			continue
		}
		full := filepath.Join(root, r.Rel)
		if _, err := os.Stat(full); err != nil {
			if _, lerr := os.Lstat(full); lerr != nil {
				if os.IsNotExist(err) && os.IsNotExist(lerr) {
					missing++
				} else {
					presentUnknown = true
				}
				continue
			}
		}
		if r.Hash == "" {
			continue
		}
		actual := state.HashOf(full)
		if actual == "" {
			hashUnknown = true
			continue
		}
		if actual == r.Hash {
			continue
		}
		// Only a hash mismatch pays for a git spawn: tracked paths are the
		// upstream's business, everything else is drift.
		if err := exec.Command("git", "-C", root, "ls-files", "--error-unmatch", "--", r.Rel).Run(); err == nil {
			continue
		}
		drifted++
	}

	present = OK
	if missing > 0 {
		present = Problem
	} else if presentUnknown {
		present = Unknown
	}
	hashes = OK
	if drifted > 0 {
		hashes = Problem
	} else if hashUnknown {
		hashes = Unknown
	}
	return present, hashes
}

// keptCount counts the enabled placed rows carrying a kept mark (the
// $OMK/kept/<rel> accepted copy — a dangling symlink still counts, hence
// Lstat).
func keptCount(omk string) int {
	n := 0
	for _, r := range state.ReadPlaced(filepath.Join(omk, "placed.tsv")) {
		if r.Enabled != "1" {
			continue
		}
		if _, err := os.Lstat(filepath.Join(omk, "kept", r.Rel)); err == nil {
			n++
		}
	}
	return n
}

// lastRun summarises the newest sha-bearing run in the ledger (see
// RunSummary). Ledger rows are epoch \t gate \t verdict \t sha.
func lastRun(ledger string) *RunSummary {
	f, err := os.Open(ledger)
	if err != nil {
		return nil
	}
	defer f.Close()

	type row struct {
		epoch        int64
		gate, v, sha string
	}
	var rows []row
	var maxEpoch int64
	runSha := ""
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		fields := strings.Split(sc.Text(), "\t")
		if len(fields) < 4 || fields[3] == "" {
			continue
		}
		e, err := strconv.ParseInt(fields[0], 10, 64)
		if err != nil {
			continue
		}
		rows = append(rows, row{e, fields[1], fields[2], fields[3]})
		if e > maxEpoch {
			maxEpoch, runSha = e, fields[3]
		}
	}
	if runSha == "" {
		return nil
	}

	latest := map[string]row{}
	var epoch int64
	for _, r := range rows {
		if r.sha != runSha {
			continue
		}
		if cur, ok := latest[r.gate]; !ok || r.epoch >= cur.epoch {
			latest[r.gate] = r
		}
		if r.epoch > epoch {
			epoch = r.epoch
		}
	}
	sum := &RunSummary{Checks: len(latest), Epoch: epoch}
	for _, r := range latest {
		if r.v != "pass" {
			sum.Failed++
		}
	}
	return sum
}

func gitOut(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimRight(string(out), "\n"), nil
}
