package gate

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// RunHook runs every gate declared for the given hook stage, from repo root,
// reading the gate list from the init-written snapshot manifest under omk. It
// is the port of one omakase-gate.sh invocation per declared gate, in manifest
// order — every gate runs (as lefthook's non-piped default did), and the stage
// returns the FIRST failing gate's exit code, so a single gate's code still
// passes through unchanged while a multi-gate stage surfaces every failure. A
// load error fails closed (returns 1) — a corrupt snapshot must not silently
// run nothing.
//
// The caller (omakase hook) has already scrubbed GIT_DIR/GIT_WORK_TREE/
// GIT_COMMON_DIR and forwarded any stock git-lfs hook, so this concerns itself
// only with gates.
func RunHook(hook, root, omk string, stdin io.Reader, stdout, stderr io.Writer) int {
	gates, err := Load(omk)
	if err != nil {
		fmt.Fprintf(stderr, "omakase: BLOCKING — %s: the harness manifest is unreadable (%v); its gates cannot run.\n", hook, err)
		return 1
	}
	stage := ForHook(gates, hook)
	if len(stage) == 0 {
		return 0
	}

	// OMAKASE_SKIP_GATES=1 skips the whole stage the same visible, audited way a
	// per-gate skip does — the replacement for lefthook's LEFTHOOK=0.
	if os.Getenv("OMAKASE_SKIP_GATES") == "1" {
		fmt.Fprintf(stdout, "omakase[%s]: all gates skipped via OMAKASE_SKIP_GATES (audited)\n", hook)
		return 0
	}

	disabled := disabledSet(omk)
	sha := headSHA(root)
	firstFail := 0
	for _, g := range stage {
		if code := runOne(g, root, omk, sha, disabled, stdin, stdout, stderr); code != 0 && firstFail == 0 {
			firstFail = code
		}
	}
	return firstFail
}

// runOne runs a single gate, mirroring omakase-gate.sh's five-step order:
// audited skip, menu skip, glob scope, cache, then the check. It records a
// verdict row whenever the check actually runs, and returns the check's exit
// code unchanged (0 for every skip path).
func runOne(g Gate, root, omk, sha string, disabled map[string]bool, stdin io.Reader, stdout, stderr io.Writer) int {
	// (1) audited per-gate bypass.
	if os.Getenv(skipVar(g.Name)) == "1" {
		fmt.Fprintf(stdout, "omakase[%s]: skipped via %s (audited)\n", g.Name, skipVar(g.Name))
		return 0
	}

	// (2) menu bypass — a name in the shared-zone disabled-gates file, written
	// by `omakase status`, skips visibly and persistently.
	if disabled[g.Name] {
		fmt.Fprintf(stdout, "omakase[%s]: disabled via omakase - skipping (re-enable: omakase status)\n", g.Name)
		return 0
	}

	// (3) glob scope: run only when a changed file in the range matches. With
	// no resolvable base the gate cannot tell what changed, so it RUNS unscoped
	// rather than skipping — the threat model is omission (#92).
	if len(g.Glob) > 0 {
		base, ok := resolveBase(root)
		if !ok {
			fmt.Fprintf(stdout, "omakase[%s]: no resolvable base ref - cannot scope, running the check\n", g.Name)
		} else if !changedMatches(root, base, g.Glob) {
			fmt.Fprintf(stdout, "omakase[%s]: no changed file matches the glob - skipping\n", g.Name)
			return 0
		}
	}

	// (4) cacheable: a fresh PASS for this exact commit short-circuits.
	if g.Cacheable && sha != "" && hasFreshPass(omk, g.Name, sha) {
		fmt.Fprintf(stdout, "omakase[%s]: fresh PASS for %s - skipping (cached)\n", g.Name, short8(sha))
		return 0
	}

	// (5) run the check in a child shell from the repo root, record the run
	// best-effort, and pass the exit code through unchanged.
	cmd := exec.Command("sh", "-c", g.Run)
	cmd.Dir = root
	cmd.Stdin = stdin
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	rc := 0
	if err := cmd.Run(); err != nil {
		rc = exitCode(err)
	}
	verdict := "pass"
	if rc != 0 {
		verdict = "fail"
	}
	_ = appendRow(omk, g.Name, verdict, sha) // best-effort: a dropped row re-runs next time
	return rc
}

// Record appends a PASS row for HEAD for one gate name, with no check run — the
// out-of-band signal that a deferred gate's real check passed (the port of
// omakase-gate.sh --record). It is the only signal an out-of-band check
// passed, so it fails LOUD: a write error returns a non-nil error.
func Record(root, omk, name string) error {
	sha := headSHA(root)
	if err := appendRow(omk, name, "pass", sha); err != nil {
		return fmt.Errorf("could not write %s: %w", filepath.Join(omk, "ledger.tsv"), err)
	}
	return nil
}

// skipVar is the audited-bypass env var name for a gate: OMAKASE_SKIP_<NAME>,
// upper-cased with '.' and '-' folded to '_' (the sh `tr '[:lower:].-'
// '[:upper:]__'`).
func skipVar(name string) string {
	var b strings.Builder
	b.WriteString("OMAKASE_SKIP_")
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r - 32)
		case r == '.' || r == '-':
			b.WriteByte('_')
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}
