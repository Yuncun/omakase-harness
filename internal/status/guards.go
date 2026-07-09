// This file (guards.go) ports the guards chart — the "run when" table — of
// bin/status.sh (render_guards, bin/status.sh:188-274). The chart is derived
// from `lefthook dump` (the normalized hook wiring) joined to the run ledger.
// The heart of it is a dense awk program (bin/status.sh:208-273); it is ported
// here RULE BY RULE, each Go branch carrying a comment citing the awk line it
// mirrors. Stdlib only — the dump is walked line by line exactly as the awk
// does (Global Constraint 3: replicate the awk's semantics, never "parse YAML").
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

	"github.com/Yuncun/omakase-harness/internal/state"
)

// Load-bearing output glyphs, transcribed from bin/status.sh (never approximated,
// Global Constraint 10). The awk emits them as octal UTF-8:
//
//	✓ = \342\234\223 = U+2713 (bin/status.sh:247, the pass verdict)
//	✗ = \342\234\227 = U+2717 (bin/status.sh:247, the fail verdict)
//	— = \342\200\224 = U+2014 (bin/status.sh:249, the non-gate verdict cell)
const (
	glyphCheck  = "✓" // ✓
	glyphCross  = "✗" // ✗
	glyphEmDash = "—" // —
)

// Regexes ported from the awk. The three line-anchored ones (hook header, job
// name, run line) gate a rule AND, when they carry a trailing capture region,
// strip their own match off the front of the line (the awk's sub(/^…/,"",line)).
var (
	reHookHeader = regexp.MustCompile(`^[A-Za-z0-9_-]+:[[:space:]]*$`)               // bin/status.sh:215
	reJobName    = regexp.MustCompile(`^[[:space:]]*-[[:space:]]+name:[[:space:]]*`) // bin/status.sh:216
	reRun        = regexp.MustCompile(`^[[:space:]]*run:[[:space:]]*`)               // bin/status.sh:219
	reGate       = regexp.MustCompile(`omakase-gate\.sh [A-Za-z0-9._-]+`)            // bin/status.sh:225
	reGlob       = regexp.MustCompile(`--glob '[^']*'`)                              // bin/status.sh:235 (single-quoted --glob 'PATS')
	// A YAML block-scalar indicator after `run:` — "|" or ">" with optional
	// chomp/indent suffix. `lefthook dump` re-emits a block-scalar run: this
	// way, with the command on the following deeper-indented line(s). No awk
	// twin: the oracle rendered the bare indicator as the command and lost
	// the ledger join (sanctioned forward divergence, see renderGuardsChart).
	reBlockScalar = regexp.MustCompile(`^[|>][0-9+-]*[[:space:]]*$`)
)

// RenderGuards resolves lefthook, runs `<lefthook> dump` with cwd=root (stderr
// discarded), and renders the chart — or, if lefthook can't be resolved or the
// dump is empty, the one-line not-resolved note (bin/status.sh:193-199). The
// join input (verdicts) is read from omk/ledger.tsv and the age reference from
// $OMAKASE_NOW, exactly as the reference does (bin/status.sh:201-202).
func RenderGuards(w io.Writer, root, omk string, md bool) {
	dump := ""
	if lh := resolveLefthook(root); lh != "" {
		dump = dumpLefthook(lh, root)
	}

	if dump == "" { // bin/status.sh:195-199
		if md {
			fmt.Fprintln(w, "_lefthook not resolved - gates are not running._")
		} else {
			fmt.Fprintln(w, "  (lefthook not resolved - gates are not running)")
		}
		return
	}

	// bin/status.sh:202 — a missing ledger is /dev/null there; LatestVerdicts
	// returns an empty map for a missing file, the same net effect.
	verds := state.LatestVerdicts(filepath.Join(omk, "ledger.tsv"))
	renderGuardsChart(w, dump, verds, nowFromEnv(), md)
}

// resolveLefthook mirrors bin/status.sh:190-192: LEFTHOOK_BIN, else `lefthook`
// on PATH, else $root/node_modules/.bin/lefthook if executable. "" if none.
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

// dumpLefthook runs `<lh> dump` with cwd=root and returns its stdout with
// trailing newlines stripped — the Go twin of
// `DUMP="$( cd "$ROOT" && "$LH" dump 2>/dev/null || true )"` (bin/status.sh:193):
// stdout is captured, stderr discarded, and the exit code ignored (a non-zero
// dump that still printed keeps its stdout, matching the `|| true`).
func dumpLefthook(lh, root string) string {
	cmd := exec.Command(lh, "dump")
	cmd.Dir = root
	out, _ := cmd.Output() // stderr left unset -> discarded; exit code ignored
	return strings.TrimRight(string(out), "\n")
}

// nowFromEnv is the age reference: OMAKASE_NOW if set, else the current epoch
// (bin/status.sh:201, `now="${OMAKASE_NOW:-$(date +%s)}"`). A set-but-non-numeric
// value coerces to its leading integer, matching awk's numeric coercion.
func nowFromEnv() int64 {
	s := os.Getenv("OMAKASE_NOW")
	if s == "" {
		return time.Now().Unix()
	}
	return awkNumeric(s)
}

// awkNumeric returns the leading integer of s (optional sign, digits), or 0.
// This covers only awk's integer coercion, which is all the epoch inputs
// here ever need — not awk's full numeric (float/exponent) string coercion.
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

// renderGuardsChart is the Go twin of the awk program (bin/status.sh:208-273),
// ported rule by rule. It walks dump line by line, buffers one row per
// non-cosmetic job (hook -> job -> first run), joins each ledgered gate to its
// verdict, then emits a markdown table (md) or a width-aligned terminal table.
// now is the age reference (already resolved from OMAKASE_NOW).
func renderGuardsChart(w io.Writer, dump string, verds map[string]state.Verdict, now int64, md bool) {
	// BEGIN: term widths start at the header label lengths (bin/status.sh:209).
	wH, wG, wE := utf8.RuneCountInString("RUN WHEN"), utf8.RuneCountInString("GUARD"), utf8.RuneCountInString("ENFORCES")

	var rows []guardRow
	var curhook, jobname string
	haverun := false

	// Indexed loop (not a Scanner): the block-scalar rule below needs to
	// consume the lines following a `run: |` as that run's continuation.
	lines := strings.Split(dump, "\n")
	for i := 0; i < len(lines); i++ {
		line := lines[i]

		// Rule: hook header (col 0) -> curhook = text before the first ':'
		// (bin/status.sh:215, `sub(/:.*/,"",curhook)`).
		if reHookHeader.MatchString(line) {
			if i := strings.IndexByte(line, ':'); i >= 0 {
				curhook = line[:i]
			}
			continue
		}

		// Rule: `- name: <job>` -> remainder is jobname; resets haverun
		// (bin/status.sh:216-218).
		if loc := reJobName.FindStringIndex(line); loc != nil {
			jobname = line[loc[1]:]
			haverun = false
			continue
		}

		// Rule: `run: <cmd>` (bin/status.sh:219-256).
		loc := reRun.FindStringIndex(line)
		if loc == nil {
			continue
		}
		// Only the FIRST run: after a name:; run lines with no pending name are
		// skipped (bin/status.sh:220).
		if jobname == "" || haverun {
			continue
		}
		haverun = true
		runcmd := line[loc[1]:]

		// Rule (Go only, no awk twin): a block-scalar run. The command lives
		// on the following line(s), each indented deeper than the `run:` line;
		// join them with single spaces into one logical command so the gate
		// name, --cacheable/--glob description, and ledger join all work
		// exactly as they do for a single-line run. Consuming the
		// continuation lines here also keeps them from misparsing as rules.
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

		// omakase-banner: cosmetic header box, not a guard (bin/status.sh:223).
		if jobname == "omakase-banner" {
			jobname = ""
			continue
		}

		// A gate -> its canonical (ledgered) name (bin/status.sh:225-227).
		ledgered := false
		gate := ""
		if m := reGate.FindString(runcmd); m != "" {
			gate = strings.TrimPrefix(m, "omakase-gate.sh ")
			ledgered = true
		}

		// ENFORCES cell precedence (bin/status.sh:228-239).
		var enf string
		switch {
		case strings.Contains(runcmd, "ensure-present.sh"): // bin/status.sh:228
			enf = "self-heal: restore any missing injected files"
		case ledgered: // bin/status.sh:230-238 — describe by SAFE flags only
			cached := strings.Contains(runcmd, "--cacheable") // bin/status.sh:232
			scope := "runs every commit"                      // bin/status.sh:233
			if m := reGlob.FindString(runcmd); m != "" {      // bin/status.sh:235-237
				g := strings.TrimPrefix(m, "--glob '")
				g = strings.TrimSuffix(g, "'")
				scope = "scope: " + g
			}
			if cached {
				enf = "cached; " + scope // bin/status.sh:238
			} else {
				enf = scope
			}
		default: // bin/status.sh:239
			enf = runcmd
		}

		// Row name = gate if ledgered, else the job name (bin/status.sh:240).
		gname := jobname
		if ledgered {
			gname = gate
		}

		// Verdict cell (bin/status.sh:241-249).
		var vc string
		if v, ok := verds[gate]; gate != "" && ok { // has a ledger row (gate in seen)
			d := now - v.Epoch
			if d < 0 { // clamp >= 0 (bin/status.sh:242)
				d = 0
			}
			vc = verdictGlyph(v.Verdict) + " - " + age(d) + " ago"
		} else if ledgered { // wired gate, never run (bin/status.sh:248)
			vc = "- not yet run"
		} else { // non-gate job (bin/status.sh:249)
			vc = glyphEmDash
		}

		rows = append(rows, guardRow{curhook, gname, enf, vc})
		// Grow term widths to the longest cell (bin/status.sh:251-253). Only the
		// ASCII columns are padded; the verdict cell is always last and unpadded.
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

	// END (bin/status.sh:257-272).
	if md {
		if len(rows) == 0 { // bin/status.sh:259
			fmt.Fprintln(w, "_(no guards wired)_")
			return
		}
		fmt.Fprintln(w, "| Run when | Guard | Enforces | Last verdict |")
		fmt.Fprintln(w, "| --- | --- | --- | --- |")
		for _, r := range rows { // bin/status.sh:263 — escape only Guard + Enforces
			fmt.Fprintf(w, "| `%s` | %s | %s | %s |\n", r.hook, mdcell(r.guard), mdcell(r.enf), r.verdict)
		}
		return
	}
	if len(rows) == 0 { // bin/status.sh:266
		fmt.Fprintln(w, "  (no guards wired)")
		return
	}
	// bin/status.sh:268-269 — header + rows, same format, verdict last & unpadded.
	fmt.Fprintf(w, "  %-*s   %-*s   %-*s   %s\n", wH, "RUN WHEN", wG, "GUARD", wE, "ENFORCES", "LAST VERDICT")
	for _, r := range rows {
		fmt.Fprintf(w, "  %-*s   %-*s   %-*s   %s\n", wH, r.hook, wG, r.guard, wE, r.enf, r.verdict)
	}
}

// mdcell escapes a literal `|` (which would break the md table) and folds
// newlines to spaces — the awk's mdcell() (bin/status.sh:210), applied ONLY to
// the Guard and Enforces cells.
func mdcell(s string) string {
	s = strings.ReplaceAll(s, "|", "\\|")
	s = strings.ReplaceAll(s, "\n", " ")
	return s
}

// verdictGlyph is the pass/fail lead of a verdict cell (bin/status.sh:247).
func verdictGlyph(verdict string) string {
	if verdict == "fail" {
		return glyphCross + " fail"
	}
	return glyphCheck + " pass"
}

// age buckets d (seconds, already clamped >= 0) exactly as bin/status.sh:243-246.
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
