// This file renders the context-layers page: what the detected host loads
// every turn (one line per layer, cost drawn as a bar so the expensive
// layers are visible before the numbers are read), what sits idle until
// something asks for it (aggregated — twenty path-scoped rules are one fact,
// not twenty rows), and one closing line for files the running host never
// reads. Ordering is by cost, not alphabet: the page exists to show
// significance, and repetition is the enemy — anything identical across a
// whole group of rows is said once in the group header or not at all.
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

// tierLabel names a tier for the --help text and the markdown table.
func tierLabel(t Tier) string {
	switch t {
	case TierAlways:
		return "LOADED"
	case TierIndexed:
		return "INDEXED"
	case TierOnDemand:
		return "ON DEMAND"
	case TierOnTrigger:
		return "ON TRIGGER"
	case TierInert:
		return "INERT"
	}
	return ""
}

// page is the assembled view: entries regrouped the way the reader needs
// them rather than the way the scanner found them.
type page struct {
	everyTurn []Entry   // loaded tiers, sorted by cost descending
	idle      []idleRow // reachable-but-idle aggregates, sorted by cost
	inert     []string  // display names unread by the detected host
	loadedTok int
	idleTok   int
}

// idleRow is one aggregated line in the idle section.
type idleRow struct {
	tokens int
	label  string
	when   string
}

// assemble regroups the scan into the page. Path-scoped rules collapse to
// one row per source directory: each rule's scope is a detail for --show,
// while their combined weight and trigger are the page-level facts.
func assemble(entries []Entry) page {
	var p page
	scoped := map[string][]Entry{} // source dir -> scoped rules
	var scopedDirs []string

	for _, e := range entries {
		switch {
		case e.Tier.Loaded():
			p.everyTurn = append(p.everyTurn, e)
			p.loadedTok += e.Tokens()
		case e.Tier == TierInert:
			p.inert = append(p.inert, e.Display)
		case len(e.Scope) > 0:
			dir := path.Dir(e.Display) + "/"
			if _, seen := scoped[dir]; !seen {
				scopedDirs = append(scopedDirs, dir)
			}
			scoped[dir] = append(scoped[dir], e)
			p.idleTok += e.Tokens()
		default:
			when := e.Note
			if when == "" {
				when = "loads on request"
			}
			p.idle = append(p.idle, idleRow{e.Tokens(), e.Display, when})
			p.idleTok += e.Tokens()
		}
	}

	for _, dir := range scopedDirs {
		rules := scoped[dir]
		tok := 0
		for _, e := range rules {
			tok += e.Tokens()
		}
		p.idle = append(p.idle, idleRow{
			tok,
			plural(len(rules), "path-scoped rule", "path-scoped rules") + " (" + dir + ")",
			"load when you edit files they govern (globs: --show <rule>)",
		})
	}

	sort.SliceStable(p.everyTurn, func(i, j int) bool {
		return p.everyTurn[i].Tokens() > p.everyTurn[j].Tokens()
	})
	sort.SliceStable(p.idle, func(i, j int) bool { return p.idle[i].tokens > p.idle[j].tokens })
	return p
}

// TotalsFor sums the per-turn token cost a specific host would pay, for the
// undetected-host header where each host's number is shown side by side.
func TotalsFor(entries []Entry, hostKey string) int {
	tok := 0
	for _, e := range entries {
		if e.Tier.Loaded() && contains(e.Hosts, hostKey) {
			tok += e.Tokens()
		}
	}
	return tok
}

// Render writes the layers page. host is the detected host key or "" when
// unknown; repoName appears in the header so a pasted page says which repo
// it describes. cmdPrefix is the verb the drill-in hints name (an
// integration detail: the same page renders under more than one verb).
func Render(w io.Writer, entries []Entry, repoName, host, cmdPrefix string, md bool) {
	p := assemble(entries)
	if md {
		renderMarkdown(w, entries, p, repoName, host, cmdPrefix)
		return
	}
	renderTerminal(w, entries, p, repoName, host, cmdPrefix)
}

// renderTerminal prints the page for a terminal.
func renderTerminal(w io.Writer, entries []Entry, p page, repoName, host, cmdPrefix string) {
	fmt.Fprintln(w, headerLine(entries, p, repoName, host, false))

	maxTok := 0
	for _, e := range p.everyTurn {
		if e.Tokens() > maxTok {
			maxTok = e.Tokens()
		}
	}
	pathW := 0
	for _, e := range p.everyTurn {
		if n := runeLen(e.Display); n > pathW {
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
		} else if e.Note != "" && e.Count > 1 {
			says = e.Note // aggregate rows explain themselves in the note
		}
		tag := hostTag(e, host)
		line := fmt.Sprintf("  %-*s  %7s  %-*s  %s%s",
			barWidth, bar(e.Tokens(), maxTok),
			"~"+commas(e.Tokens()),
			pathW, elideLeft(e.Display, pathW), says, tag)
		fmt.Fprintln(w, strings.TrimRight(line, " "))
		if note := rowNote(e); note != "" {
			fmt.Fprintf(w, "  %*s  %s\n", barWidth+9+pathW+1, "", note)
		}
	}

	if len(p.idle) > 0 {
		fmt.Fprintln(w)
		fmt.Fprintf(w, "IDLE UNTIL NEEDED — ~%s tok reachable, costing nothing now\n", commas(p.idleTok))
		for _, r := range p.idle {
			fmt.Fprintf(w, "  %7s  %s — %s\n", "~"+commas(r.tokens), r.label, r.when)
		}
	}

	if line := inertLine(p.inert, host); line != "" {
		fmt.Fprintln(w)
		fmt.Fprintln(w, line)
	}

	fmt.Fprintln(w)
	fmt.Fprintf(w, "estimated at 4 bytes/token — your host's /context has the real numbers\n")
	fmt.Fprintf(w, "%s --show <path>   print any layer in full\n", cmdPrefix)
}

// renderMarkdown prints the same page for --markdown consumers (the status
// skill relays this form). The bars ride along in a table column: GitHub
// renders block characters fine, and relative length survives any font.
func renderMarkdown(w io.Writer, entries []Entry, p page, repoName, host, cmdPrefix string) {
	fmt.Fprintf(w, "### %s\n\n", headerLine(entries, p, repoName, host, true))
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
		if says == "" {
			says = e.Note
		} else if n := rowNote(e); n != "" {
			says += " _(" + n + ")_"
		}
		fmt.Fprintf(w, "| %s | %s | `%s`%s | %s |\n",
			bar(e.Tokens(), maxTok), commas(e.Tokens()), e.Display,
			mdEscape(hostTag(e, host)), mdEscape(truncate(says, 120)))
	}
	if len(p.idle) > 0 {
		fmt.Fprintln(w)
		fmt.Fprintf(w, "**Idle until needed — ~%s tok** reachable, costing nothing now:\n", commas(p.idleTok))
		for _, r := range p.idle {
			fmt.Fprintf(w, "- ~%s tok — %s — %s\n", commas(r.tokens), r.label, r.when)
		}
	}
	if line := inertLine(p.inert, host); line != "" {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "_"+line+"_")
	}
	fmt.Fprintln(w)
	fmt.Fprintf(w, "_Estimated at 4 bytes/token — your host's `/context` has the real numbers. `%s --show <path>` prints any layer in full._\n", cmdPrefix)
}

// headerLine is the page's first line: the per-turn total for the detected
// host, or both hosts' totals when no host was detected — the union view
// would otherwise present one host's inert files as everyone's cost.
func headerLine(entries []Entry, p page, repoName, host string, md bool) string {
	sep := " — "
	switch host {
	case "claude":
		return "WHAT YOUR AGENT READS" + sep + repoName + " · Claude Code · every turn: ~" + commas(p.loadedTok) + " tok"
	case "copilot":
		return "WHAT YOUR AGENT READS" + sep + repoName + " · Copilot CLI · every turn: ~" + commas(p.loadedTok) + " tok"
	}
	return "WHAT AN AGENT READS HERE" + sep + repoName +
		" · every turn: ~" + commas(TotalsFor(entries, "claude")) + " tok (Claude Code) · ~" +
		commas(TotalsFor(entries, "copilot")) + " tok (Copilot CLI)"
}

// hostTag marks a row read by only one host — but only when no host was
// detected. Under a detected host the single-host rows that remain are by
// definition this host's, and the other host's files are on the inert line.
func hostTag(e Entry, host string) string {
	if host != "" || len(e.Hosts) != 1 {
		return ""
	}
	switch e.Hosts[0] {
	case "claude":
		return "  · claude only"
	case "copilot":
		return "  · copilot only"
	}
	return ""
}

// rowNote is the one-line continuation worth printing under a row: the
// symlink/overlap/parent-dir facts. Aggregate rows carry their note inline.
func rowNote(e Entry) string {
	if e.Count > 1 {
		return ""
	}
	return e.Note
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
