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
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/Yuncun/omakase-harness/internal/state"
)

// Tri is the outcome of one proof.
type Tri int

const (
	Unknown Tri = iota // could not verify — never treat as OK
	OK
	Problem
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
	Worktree     string // linked-worktree directory name; "" in the main checkout
	Branch       string // current branch, or the short sha when detached
	Source       string // $OMK/source first line; "" = bare base install
	NameOverride string // $OMAKASE_NAME, else .omakase/NAME; "" = none
	BaseVersion  string // .omakase/VERSION first line

	// Proofs.
	Armed        Tri // git's effective hooks dir holds a lefthook-managed stub
	FilesPresent Tri // every enabled placed row exists in this worktree
	HashesMatch  Tri // no enabled row drifted from its ledger hash
	Missing      int // enabled rows absent (FilesPresent detail)
	Drifted      int // enabled rows whose content diverged (HashesMatch detail)

	// Worktree-discipline facts (issue #86).
	MainCheckout  bool
	WorktreeCount int
	DisciplineOff bool // the audited skip env, or the menu's persistent disable

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

	// Identity.
	roots := state.WorktreeRoots(repo.Root)
	st.WorktreeCount = len(roots)
	st.MainCheckout = repo.Root == roots[0]
	st.Project = filepath.Base(roots[0])
	if !st.MainCheckout {
		st.Worktree = filepath.Base(repo.Root)
	}
	st.Branch = branch(repo.Root)
	st.Source = state.FirstLine(filepath.Join(repo.OMK, "source"))
	st.BaseVersion = state.FirstLine(filepath.Join(repo.Root, ".omakase", "VERSION"))
	st.NameOverride = os.Getenv("OMAKASE_NAME")
	if st.NameOverride == "" {
		n := state.FirstLine(filepath.Join(repo.Root, ".omakase", "NAME"))
		st.NameOverride = strings.TrimSpace(n)
	}

	// Proofs.
	st.Armed = armed(repo.Root)
	st.FilesPresent, st.HashesMatch, st.Missing, st.Drifted = files(repo.Root, repo.OMK)

	// Discipline standdowns, same pair the commit gate honors.
	if os.Getenv("OMAKASE_SKIP_WORKTREE_DISCIPLINE") == "1" {
		st.DisciplineOff = true
	} else if hasLine(filepath.Join(repo.OMK, "disabled-gates"), "worktree-discipline") {
		st.DisciplineOff = true
	}

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

// armed proves whether a commit/push in this repo would actually run the
// harness: git's effective hooks dir (rev-parse --git-path hooks honors
// core.hooksPath) must hold a lefthook-managed pre-commit or pre-push stub.
// No stub, or a foreign manager's stub, is an affirmative Problem; a git
// failure or an unreadable existing stub is Unknown.
func armed(root string) Tri {
	dir, err := gitOut(root, "rev-parse", "--git-path", "hooks")
	if err != nil {
		return Unknown
	}
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(root, dir)
	}
	unreadable := false
	for _, h := range []string{"pre-commit", "pre-push"} {
		b, err := os.ReadFile(filepath.Join(dir, h))
		if err != nil {
			if !os.IsNotExist(err) {
				unreadable = true
			}
			continue
		}
		if strings.Contains(strings.ToLower(string(b)), "lefthook") {
			return OK
		}
	}
	if unreadable {
		return Unknown
	}
	return Problem
}

// files walks the enabled placed.tsv rows of this worktree and proves
// presence and hash fidelity. A tracked path never drifts (upstream owns
// it), a row without a ledger hash cannot be judged and is skipped, and an
// unreadable path degrades that proof to Unknown rather than guessing.
func files(root, omk string) (present, hashes Tri, missing, drifted int) {
	rows := state.ReadPlaced(filepath.Join(omk, "placed.tsv"))
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
	return present, hashes, missing, drifted
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

// hasLine reports whether path contains line exactly (the disabled-gates
// membership test, matching the gate's `grep -Fxq`).
func hasLine(path, line string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		if sc.Text() == line {
			return true
		}
	}
	return false
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
