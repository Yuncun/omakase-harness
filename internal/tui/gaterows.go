// gaterows.go resolves lefthook's wiring into GateRow values: one row per
// job, split by pre-commit/pre-push/other and by whether the job is a
// consent-tracked gate (wired through omakase-gate.sh) or a plain lefthook
// job. It is a data-layer sibling of internal/status/guards.go, not a
// replacement — guards.go is parity-frozen (it feeds the shipped `status
// guards` chart, and downstream tests depend on its exact
// output), so this file deliberately re-declares its own copies of the four
// scanning regexes and the resolve/dump helpers rather than import or
// refactor guards.go. The duplication keeps guards.go's output stable; keep
// the two in lockstep by hand if lefthook's dump format ever changes.
package tui

import (
	"bufio"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// Regexes duplicated verbatim from internal/status/guards.go:42-45.
var (
	reHookHeader = regexp.MustCompile(`^[A-Za-z0-9_-]+:[[:space:]]*$`)               // guards.go:42
	reJobName    = regexp.MustCompile(`^[[:space:]]*-[[:space:]]+name:[[:space:]]*`) // guards.go:43
	reRun        = regexp.MustCompile(`^[[:space:]]*run:[[:space:]]*`)               // guards.go:44
	reGate       = regexp.MustCompile(`omakase-gate\.sh [A-Za-z0-9._-]+`)            // guards.go:45
)

// gateRowsMaxLineBuf matches internal/status/inventory.go's maxLineBuf
// (1MiB) so a long run: line can't overflow bufio.Scanner's 64KiB default.
const gateRowsMaxLineBuf = 1 << 20

// GateRow is one lefthook job, resolved to whether it is a consent-tracked
// gate (Gate==true, Name is the gate's canonical omakase-gate.sh name) or a
// plain job (Gate==false, Name is the job's `- name:` as written).
type GateRow struct {
	Hook string
	Name string
	Gate bool
}

// ParseGateRows walks a `lefthook dump` string line by line, replicating the
// scanning rules of internal/status/guards.go:158-260 (hook header sets the
// current hook; `- name:` sets the pending job and resets have-run; only the
// FIRST `run:` line after a name counts; a run: line with no pending name is
// ignored). Two rules beyond guards.go's own (Task 6 brief): the cosmetic
// `omakase-banner` job is skipped, and so is any run line mentioning
// `ensure-present.sh` — self-heal machinery, not a consent item.
func ParseGateRows(dump string) []GateRow {
	var rows []GateRow
	var curhook, jobname string
	haverun := false

	sc := bufio.NewScanner(strings.NewReader(dump))
	sc.Buffer(make([]byte, 0, 64*1024), gateRowsMaxLineBuf)
	for sc.Scan() {
		line := sc.Text()

		// Hook header (col 0) -> curhook = text before the first ':' (guards.go:163-168).
		if reHookHeader.MatchString(line) {
			if i := strings.IndexByte(line, ':'); i >= 0 {
				curhook = line[:i]
			}
			continue
		}

		// `- name: <job>` -> remainder is jobname; resets haverun (guards.go:172-176).
		if loc := reJobName.FindStringIndex(line); loc != nil {
			jobname = line[loc[1]:]
			haverun = false
			continue
		}

		// `run: <cmd>` (guards.go:179-259).
		loc := reRun.FindStringIndex(line)
		if loc == nil {
			continue
		}
		// Only the FIRST run: after a name: counts; a run: with no pending name
		// is ignored (guards.go:183-187).
		if jobname == "" || haverun {
			continue
		}
		haverun = true
		runcmd := line[loc[1]:]

		// omakase-banner: cosmetic header box, not a guard (guards.go:191-195).
		if jobname == "omakase-banner" {
			jobname = ""
			continue
		}

		// ensure-present.sh: self-heal machinery, not a consent item — the one
		// skip rule guards.go itself does NOT have (it annotates this run as
		// "self-heal" instead of dropping it; here we drop it, per the Task 6
		// brief).
		if strings.Contains(runcmd, "ensure-present.sh") {
			jobname = ""
			continue
		}

		// A ledgered gate -> its canonical (omakase-gate.sh) name (guards.go:198-203).
		name := jobname
		gate := false
		if m := reGate.FindString(runcmd); m != "" {
			name = strings.TrimPrefix(m, "omakase-gate.sh ")
			gate = true
		}

		rows = append(rows, GateRow{Hook: curhook, Name: name, Gate: gate})
		jobname = ""
	}
	return rows
}

// resolveLefthook is duplicated verbatim from internal/status/guards.go:77-89
// (LEFTHOOK_BIN, else `lefthook` on PATH, else $root/node_modules/.bin/lefthook
// if executable; "" if none) — see the package comment above for why this is
// a deliberate copy rather than a shared helper.
func resolveLefthook(root string) string {
	if b := os.Getenv("LEFTHOOK_BIN"); b != "" {
		return b
	}
	if p, err := exec.LookPath("lefthook"); err == nil {
		return p
	}
	cand := filepath.Join(root, "node_modules", ".bin", "lefthook")
	if info, err := os.Stat(cand); err == nil && !info.IsDir() && info.Mode()&0o111 != 0 {
		return cand
	}
	return ""
}

// dumpLefthook is duplicated verbatim from internal/status/guards.go:96-101:
// runs `<lh> dump` with cwd=root, stderr discarded and exit code ignored,
// and returns stdout with trailing newlines stripped.
func dumpLefthook(lh, root string) string {
	cmd := exec.Command(lh, "dump")
	cmd.Dir = root
	out, _ := cmd.Output() // stderr left unset -> discarded; exit code ignored
	return strings.TrimRight(string(out), "\n")
}

// GateRows resolves lefthook, dumps its wiring (cwd=root), and parses it into
// GateRows — nil when lefthook can't be resolved or the dump is empty,
// mirroring status.RenderGuards's own not-resolved short-circuit
// (guards.go:56-67).
func GateRows(root string) []GateRow {
	lh := resolveLefthook(root)
	if lh == "" {
		return nil
	}
	dump := dumpLefthook(lh, root)
	if dump == "" {
		return nil
	}
	return ParseGateRows(dump)
}
