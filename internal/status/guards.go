// This file renders the guards chart — the "run when" table. The chart is
// derived from `lefthook dump` (the normalized hook wiring) joined to the run
// ledger. The dump is walked line by line rather than parsed as YAML.
package status

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Yuncun/omakase-harness/internal/lefthook"
	"github.com/Yuncun/omakase-harness/internal/state"
)

// Load-bearing output glyphs (U+2713 ✓ pass, U+2717 ✗ fail, U+2014 —
// non-gate); never approximated.
const (
	glyphCheck  = "✓"
	glyphCross  = "✗"
	glyphEmDash = "—"
)

// Scan regexes. The three line-anchored ones (hook header, job name, run
// line) gate a rule and, when they carry a trailing capture region, strip
// their own match off the front of the line.
var (
	reHookHeader = regexp.MustCompile(`^[A-Za-z0-9_-]+:[[:space:]]*$`)
	reJobName    = regexp.MustCompile(`^[[:space:]]*-[[:space:]]+name:[[:space:]]*`)
	reRun        = regexp.MustCompile(`^[[:space:]]*run:[[:space:]]*`)
	reGate       = regexp.MustCompile(`omakase-gate\.sh [A-Za-z0-9._-]+`)
	reGlob       = regexp.MustCompile(`--glob '[^']*'`)
	// A YAML block-scalar indicator after `run:` — "|" or ">" with optional
	// chomp/indent suffix. `lefthook dump` re-emits a block-scalar run this
	// way, with the command on the following deeper-indented line(s).
	reBlockScalar = regexp.MustCompile(`^[|>][0-9+-]*[[:space:]]*$`)
)

// RenderGuards resolves lefthook, runs `<lefthook> dump` with cwd=root
// (stderr discarded), and renders the chart — or, if lefthook can't be
// resolved or the dump is empty, the one-line not-resolved note. Verdicts are
// read from omk/ledger.tsv and the age reference from $OMAKASE_NOW.
func RenderGuards(w io.Writer, root, omk string, md bool) {
	dump := ""
	if lh := resolveLefthook(root); lh != "" {
		dump = dumpLefthook(lh, root)
	}

	if dump == "" {
		if md {
			fmt.Fprintln(w, "_lefthook not resolved - gates are not running._")
		} else {
			fmt.Fprintln(w, "  (lefthook not resolved - gates are not running)")
		}
		return
	}

	// A missing ledger yields an empty verdict map.
	verds := state.LatestVerdicts(filepath.Join(omk, "ledger.tsv"))
	renderGuardsChart(w, dump, verds, nowFromEnv(), md)
}

// resolveLefthook resolves lefthook for the guards chart through the shared
// tier walk (lefthook.ResolveForStatus): LEFTHOOK_BIN, `lefthook` on PATH,
// $root/node_modules/.bin/lefthook, then the omakase-managed cache — the same
// order init and remove use, never fetching (status is read-only). Returns ""
// if nothing resolves.
func resolveLefthook(root string) string {
	if lh, ok := lefthook.ResolveForStatus(root); ok {
		return lh
	}
	return ""
}

// dumpLefthook runs `<lh> dump` with cwd=root and returns its stdout with
// trailing newlines stripped. stderr is discarded and the exit code ignored,
// so a non-zero dump that still printed keeps its stdout.
func dumpLefthook(lh, root string) string {
	cmd := exec.Command(lh, "dump")
	cmd.Dir = root
	out, _ := cmd.Output() // stderr left unset -> discarded; exit code ignored
	return strings.TrimRight(string(out), "\n")
}

// nowFromEnv is the age reference: OMAKASE_NOW if set, else the current
// epoch. A set-but-non-numeric value coerces to its leading integer.
func nowFromEnv() int64 {
	s := os.Getenv("OMAKASE_NOW")
	if s == "" {
		return time.Now().Unix()
	}
	return awkNumeric(s)
}

// awkNumeric returns the leading integer of s (optional sign, digits), or 0.
// It covers integer coercion only, not float or exponent forms.
func awkNumeric(s string) int64 {
	s = strings.TrimLeft(s, " \t")
	i, neg := 0, false
	if i < len(s) && (s[i] == '+' || s[i] == '-') {
		neg = s[i] == '-'
		i++
	}
	start := i
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	if start == i {
		return 0
	}
	n, err := strconv.ParseInt(s[start:i], 10, 64)
	if err != nil {
		return 0
	}
	if neg {
		return -n
	}
	return n
}

type guardRow struct{ hook, guard, enf, verdict string }

// renderGuardsChart walks dump line by line, buffers one row per non-cosmetic
// job (hook -> job -> first run), joins each ledgered gate to its verdict,
// then emits a markdown table (md) or a width-aligned terminal table. now is
// the age reference.
func renderGuardsChart(w io.Writer, dump string, verds map[string]state.Verdict, now int64, md bool) {
	// Term widths start at the header label lengths.
	wH, wG, wE := utf8.RuneCountInString("RUN WHEN"), utf8.RuneCountInString("GUARD"), utf8.RuneCountInString("ENFORCES")

	var rows []guardRow
	var curhook, jobname string
	haverun := false

	// Indexed loop (not a Scanner): the block-scalar rule below needs to
	// consume the lines following a `run: |` as that run's continuation.
	lines := strings.Split(dump, "\n")
	for i := 0; i < len(lines); i++ {
		line := lines[i]

		// Hook header (col 0): curhook is the text before the first ':'.
		if reHookHeader.MatchString(line) {
			if i := strings.IndexByte(line, ':'); i >= 0 {
				curhook = line[:i]
			}
			continue
		}

		// `- name: <job>`: the remainder is jobname; resets haverun.
		if loc := reJobName.FindStringIndex(line); loc != nil {
			jobname = line[loc[1]:]
			haverun = false
			continue
		}

		// `run: <cmd>`.
		loc := reRun.FindStringIndex(line)
		if loc == nil {
			continue
		}
		// Only the first run after a name:; run lines with no pending name are
		// skipped.
		if jobname == "" || haverun {
			continue
		}
		haverun = true
		runcmd := line[loc[1]:]

		// A block-scalar run: the command lives on the following line(s),
		// each indented deeper than the `run:` line; join them with single
		// spaces into one logical command so the gate name,
		// --cacheable/--glob description, and ledger join all work as they
		// do for a single-line run. Consuming the continuation lines here
		// also keeps them from misparsing as rules.
		if reBlockScalar.MatchString(runcmd) {
			runIndent := len(line) - len(strings.TrimLeft(line, " "))
			var parts []string
			for i+1 < len(lines) {
				next := strings.TrimRight(lines[i+1], " \t")
				if next == "" { // blank inside/after the block: skip, keep looking
					i++
					continue
				}
				trimmed := strings.TrimLeft(next, " ")
				if len(next)-len(trimmed) <= runIndent {
					break
				}
				parts = append(parts, trimmed)
				i++
			}
			runcmd = strings.Join(parts, " ")
		}

		// omakase-banner: cosmetic header box, not a guard.
		if jobname == "omakase-banner" {
			jobname = ""
			continue
		}

		// A gate -> its canonical (ledgered) name.
		ledgered := false
		gate := ""
		if m := reGate.FindString(runcmd); m != "" {
			gate = strings.TrimPrefix(m, "omakase-gate.sh ")
			ledgered = true
		}

		// ENFORCES cell precedence.
		var enf string
		switch {
		case strings.Contains(runcmd, "ensure-present.sh"):
			enf = "self-heal: restore any missing injected files"
		case ledgered: // describe by safe flags only
			cached := strings.Contains(runcmd, "--cacheable")
			scope := "runs every commit"
			if m := reGlob.FindString(runcmd); m != "" {
				g := strings.TrimPrefix(m, "--glob '")
				g = strings.TrimSuffix(g, "'")
				scope = "scope: " + g
			}
			if cached {
				enf = "cached; " + scope
			} else {
				enf = scope
			}
		default:
			enf = runcmd
		}

		// Row name = gate if ledgered, else the job name.
		gname := jobname
		if ledgered {
			gname = gate
		}

		// Verdict cell.
		var vc string
		if v, ok := verds[gate]; gate != "" && ok { // has a ledger row
			d := now - v.Epoch
			if d < 0 { // clamp >= 0
				d = 0
			}
			vc = verdictGlyph(v.Verdict) + " - " + age(d) + " ago"
		} else if ledgered { // wired gate, never run
			vc = "- not yet run"
		} else { // non-gate job
			vc = glyphEmDash
		}

		rows = append(rows, guardRow{curhook, gname, enf, vc})
		// Grow term widths to the longest cell. Only the ASCII columns are
		// padded; the verdict cell is always last and unpadded.
		if l := utf8.RuneCountInString(curhook); l > wH {
			wH = l
		}
		if l := utf8.RuneCountInString(gname); l > wG {
			wG = l
		}
		if l := utf8.RuneCountInString(enf); l > wE {
			wE = l
		}
		jobname = ""
	}

	if md {
		if len(rows) == 0 {
			fmt.Fprintln(w, "_(no guards wired)_")
			return
		}
		fmt.Fprintln(w, "| Run when | Guard | Enforces | Last verdict |")
		fmt.Fprintln(w, "| --- | --- | --- | --- |")
		for _, r := range rows { // escape only Guard + Enforces
			fmt.Fprintf(w, "| `%s` | %s | %s | %s |\n", r.hook, mdcell(r.guard), mdcell(r.enf), r.verdict)
		}
		return
	}
	if len(rows) == 0 {
		fmt.Fprintln(w, "  (no guards wired)")
		return
	}
	// Header + rows, verdict last and unpadded.
	fmt.Fprintf(w, "  %-*s   %-*s   %-*s   %s\n", wH, "RUN WHEN", wG, "GUARD", wE, "ENFORCES", "LAST VERDICT")
	for _, r := range rows {
		fmt.Fprintf(w, "  %-*s   %-*s   %-*s   %s\n", wH, r.hook, wG, r.guard, wE, r.enf, r.verdict)
	}
}

// mdcell escapes a literal `|` (which would break the md table) and folds
// newlines to spaces, applied only to the Guard and Enforces cells.
func mdcell(s string) string {
	s = strings.ReplaceAll(s, "|", "\\|")
	s = strings.ReplaceAll(s, "\n", " ")
	return s
}

// verdictGlyph is the pass/fail lead of a verdict cell.
func verdictGlyph(verdict string) string {
	if verdict == "fail" {
		return glyphCross + " fail"
	}
	return glyphCheck + " pass"
}

// age buckets d (seconds, already clamped >= 0) into <1m / m / h / d.
func age(d int64) string {
	switch {
	case d < 60:
		return "<1m"
	case d < 3600:
		return strconv.FormatInt(d/60, 10) + "m"
	case d < 86400:
		return strconv.FormatInt(d/3600, 10) + "h"
	default:
		return strconv.FormatInt(d/86400, 10) + "d"
	}
}
