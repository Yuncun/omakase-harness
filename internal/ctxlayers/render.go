// This file renders the LOADED EVERY TURN table: one line per layer the
// detected host reads on every turn, cost drawn as a bar so the expensive
// layers are visible before the numbers are read, plus one closing line for
// files the running host never reads. On-demand totals are the steering
// stack's job (stack.go); the per-item breakdown lives behind --all.
//
// A row is data only — bar, number, path, quoted excerpt. No annotation
// prose: a structural fact either goes into the data (a symlink renders as
// `CLAUDE.md → AGENTS.md` in the path column) or is not on the page. The
// section header is the bare section name: rows carry the numbers.
//
// Deliberately absent: color as the sole carrier of meaning. The bars,
// labels and numbers survive NO_COLOR, a pipe into less, and a CI log.
package ctxlayers

import (
	"fmt"
	"io"
	"path"
	"sort"
	"strings"
	"unicode/utf8"
)

// barWidth is the widest cost bar. The bar exists to rank rows at a glance;
// past a dozen cells more resolution adds noise, not information.
const barWidth = 12

// pathColWidth caps the layer-name column so one deep path cannot push every
// excerpt off the page.
const pathColWidth = 36

// excerptColWidth caps the quoted excerpt: enough for the opening clause of
// a rule, short enough to keep rows on one line in a ~105-column terminal.
const excerptColWidth = 48

// page is the assembled view: entries regrouped the way the reader needs
// them rather than the way the scanner found them.
type page struct {
	everyTurn []Entry  // loaded tiers, sorted by cost descending
	inert     []string // display names unread by the detected host
}

// assemble regroups the scan into the page: the loaded rows (cost
// descending) and the inert names. On-demand entries do not appear — their
// mass is the steering stack's hollow cells, and their breakdown is --all's.
func assemble(entries []Entry) page {
	var p page
	for _, e := range entries {
		switch {
		case e.Tier.Loaded():
			p.everyTurn = append(p.everyTurn, e)
		case e.Tier == TierInert:
			p.inert = append(p.inert, e.Display)
		}
	}
	sort.SliceStable(p.everyTurn, func(i, j int) bool {
		return p.everyTurn[i].Tokens() > p.everyTurn[j].Tokens()
	})
	return p
}

// Render writes the LOADED EVERY TURN table. host is the detected host key
// or "" when unknown (nothing is inert then).
func Render(w io.Writer, entries []Entry, host string, md bool) {
	p := assemble(entries)
	if md {
		renderMarkdown(w, p, host)
		return
	}
	renderTerminal(w, p, host)
}

// renderTerminal prints the table for a terminal.
func renderTerminal(w io.Writer, p page, host string) {
	fmt.Fprintln(w, "LOADED EVERY TURN")

	maxTok := 0
	for _, e := range p.everyTurn {
		if e.Tokens() > maxTok {
			maxTok = e.Tokens()
		}
	}
	pathW := 0
	for _, e := range p.everyTurn {
		if n := runeLen(displayName(e)); n > pathW {
			pathW = n
		}
	}
	if pathW > pathColWidth {
		pathW = pathColWidth
	}

	for _, e := range p.everyTurn {
		says := e.Excerpt
		if says != "" {
			says = `"` + truncate(says, excerptColWidth) + `"`
		} else if e.Points != "" {
			says = "includes " + e.Points
		}
		line := fmt.Sprintf("  %-*s  %7s  %-*s  %s",
			barWidth, bar(e.Tokens(), maxTok),
			"~"+commas(e.Tokens()),
			pathW, elideLeft(displayName(e), pathW), says)
		fmt.Fprintln(w, strings.TrimRight(line, " "))
	}

	if line := inertLine(p.inert, host); line != "" {
		fmt.Fprintln(w)
		fmt.Fprintln(w, line)
	}
}

// renderMarkdown prints the same table for --markdown consumers (the status
// skill relays this form). The bars ride along in a table column: GitHub
// renders block characters fine, and relative length survives any font.
func renderMarkdown(w io.Writer, p page, host string) {
	fmt.Fprintln(w, "### Loaded every turn")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "| | ~tok | layer | says |")
	fmt.Fprintln(w, "| --- | ---: | --- | --- |")
	maxTok := 0
	for _, e := range p.everyTurn {
		if e.Tokens() > maxTok {
			maxTok = e.Tokens()
		}
	}
	for _, e := range p.everyTurn {
		says := e.Excerpt
		if says == "" && e.Points != "" {
			says = "includes `" + e.Points + "`"
		}
		fmt.Fprintf(w, "| %s | %s | `%s` | %s |\n",
			bar(e.Tokens(), maxTok), commas(e.Tokens()), displayName(e),
			mdEscape(truncate(says, 120)))
	}
	if line := inertLine(p.inert, host); line != "" {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "_"+line+"_")
	}
}

// displayName is the path cell: the primary name, with any other names that
// resolve to the same file joined by arrows — the symlink fact as data, not
// annotation.
func displayName(e Entry) string {
	if len(e.Also) == 0 {
		return e.Display
	}
	return e.Display + " → " + strings.Join(e.Also, " → ")
}

// inertLine is the single closing line for files the detected host never
// reads. Runs of files under one directory collapse to the directory: the
// fact is "that tree is unread here", not each filename.
func inertLine(inert []string, host string) string {
	if host == "" || len(inert) == 0 {
		return ""
	}
	byDir := map[string]int{}
	var order, loose []string
	for _, d := range inert {
		dir := path.Dir(d)
		if dir == "." || dir == "~" {
			loose = append(loose, d)
			continue
		}
		if byDir[dir] == 0 {
			order = append(order, dir)
		}
		byDir[dir]++
	}
	var parts []string
	parts = append(parts, loose...)
	for _, dir := range order {
		if n := byDir[dir]; n > 1 {
			parts = append(parts, dir+"/ ("+itoa(n)+" files)")
		} else {
			for _, d := range inert {
				if path.Dir(d) == dir {
					parts = append(parts, d)
				}
			}
		}
	}
	hostName := "Claude Code"
	if host == "copilot" {
		hostName = "Copilot CLI"
	}
	return "unread by " + hostName + ": " + strings.Join(parts, " · ")
}

// bar draws tokens as a share of the section's largest row. Zero draws
// nothing; anything nonzero draws at least one cell so a small layer is
// visibly present rather than invisibly free.
func bar(tokens, max int) string {
	if tokens <= 0 || max <= 0 {
		return ""
	}
	n := (tokens*barWidth + max - 1) / max
	if n < 1 {
		n = 1
	}
	if n > barWidth {
		n = barWidth
	}
	return strings.Repeat("█", n)
}

// runeLen is the column width of s. Every pad and truncate in this file
// counts runes, not bytes: a multi-byte character counted as its byte length
// silently misaligns the columns.
func runeLen(s string) int { return utf8.RuneCountInString(s) }

// elideLeft shortens a path from the left to at most max runes, keeping the
// filename intact because that is what names the layer. A path already short
// enough is returned untouched.
func elideLeft(s string, max int) string {
	if max <= 1 || runeLen(s) <= max {
		return s
	}
	r := []rune(s)
	tail := string(r[len(r)-(max-1):])
	// Prefer cutting at a path separator so the result reads as a path.
	if i := strings.IndexByte(tail, '/'); i >= 0 && i < max/3 {
		tail = tail[i:]
	}
	return "…" + tail
}

// commas formats n with thousands separators.
func commas(n int) string {
	s := itoa(n)
	if len(s) <= 3 {
		return s
	}
	var b strings.Builder
	pre := len(s) % 3
	if pre > 0 {
		b.WriteString(s[:pre])
	}
	for i := pre; i < len(s); i += 3 {
		if b.Len() > 0 {
			b.WriteByte(',')
		}
		b.WriteString(s[i : i+3])
	}
	return b.String()
}

// mdEscape neutralises the pipe characters that would break a markdown table
// cell.
func mdEscape(s string) string { return strings.ReplaceAll(s, "|", `\|`) }
