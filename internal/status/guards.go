// This file renders the guards chart — the "run when" table. It reads the
// declared gates straight from the manifest (internal/gate) and joins them to
// the run ledger; declaration IS wiring now, so there is no runner to dump and
// no declared/wired distinction to reconcile.
package status

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Yuncun/omakase-harness/internal/gate"
	"github.com/Yuncun/omakase-harness/internal/state"
)

// Load-bearing output glyphs (U+2713 ✓ pass, U+2717 ✗ fail, U+2014 —
// non-gate); never approximated.
const (
	glyphCheck  = "✓"
	glyphCross  = "✗"
	glyphEmDash = "—"
)

// RenderGuards loads the declared gates from the snapshot manifest under omk
// and renders the chart — or, if none are declared, the one-line note.
// Verdicts are read from omk/ledger.tsv and the age reference from
// $OMAKASE_NOW.
func RenderGuards(w io.Writer, omk string, md bool) {
	gates, err := gate.Load(omk)
	if err != nil || len(gates) == 0 {
		if md {
			fmt.Fprintln(w, "_no gates declared — this harness gates nothing._")
		} else {
			fmt.Fprintln(w, "  (no gates declared — this harness gates nothing)")
		}
		return
	}

	// A missing ledger yields an empty verdict map.
	verds := state.LatestVerdicts(filepath.Join(omk, "ledger.tsv"))
	renderGuardsChart(w, gates, verds, nowFromEnv(), md)
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

type guardRow struct{ hook, guard, verdict, detail string }

// renderGuardsChart builds one row per declared gate (in manifest order),
// joins each to its latest ledger verdict, then emits a markdown table (md)
// or a width-aligned terminal table. now is the age reference.
//
// Minimalist rules: no column-header row, and silence is the default — a
// gate that just runs on every fire says nothing beyond its name and
// verdict. The dim trailing detail exists only for the exceptions: a
// declared purpose:, or non-default scheduling (cached / glob scope).
// Verdicts are ✓ 15d / ✗ 2h / – (never run).
func renderGuardsChart(w io.Writer, gates []gate.Gate, verds map[string]state.Verdict, now int64, md bool) {
	wH, wG, wV := 0, 0, 0
	anyDetail := false

	var rows []guardRow
	for _, g := range gates {
		detail := g.Purpose
		if detail == "" && (g.Cacheable || len(g.Glob) > 0) {
			var parts []string
			if g.Cacheable {
				parts = append(parts, "cached")
			}
			if len(g.Glob) > 0 {
				parts = append(parts, strings.Join(g.Glob, " "))
			}
			detail = strings.Join(parts, " · ")
		}
		if detail != "" {
			anyDetail = true
		}

		vc := glyphEmDash
		if v, ok := verds[g.Name]; ok { // has a ledger row
			d := now - v.Epoch
			if d < 0 { // clamp >= 0
				d = 0
			}
			vc = verdictGlyph(v.Verdict) + " " + age(d)
		}

		rows = append(rows, guardRow{g.Hook, g.Name, vc, detail})
		if l := utf8.RuneCountInString(g.Hook); l > wH {
			wH = l
		}
		if l := utf8.RuneCountInString(g.Name); l > wG {
			wG = l
		}
		if l := utf8.RuneCountInString(vc); l > wV {
			wV = l
		}
	}

	if md {
		fmt.Fprintln(w, "| Run when | Guard | Verdict | |")
		fmt.Fprintln(w, "| --- | --- | --- | --- |")
		for _, r := range rows { // escape only Guard + detail
			fmt.Fprintf(w, "| `%s` | %s | %s | %s |\n", r.hook, mdcell(r.guard), r.verdict, mdcell(r.detail))
		}
		return
	}
	for _, r := range rows {
		if !anyDetail {
			fmt.Fprintf(w, "  %-*s   %-*s   %s\n", wH, r.hook, wG, r.guard, r.verdict)
			continue
		}
		line := fmt.Sprintf("  %-*s   %-*s   %-*s    %s", wH, r.hook, wG, r.guard, wV, r.verdict, r.detail)
		fmt.Fprintln(w, strings.TrimRight(line, " "))
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
		return glyphCross
	}
	return glyphCheck
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
