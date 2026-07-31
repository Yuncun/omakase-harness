// This file renders the `omakase context` page: the layer box, the rule that
// separates loaded text from idle text, and the footer totals. The layout
// follows the prior art the design leaned on — a fixed status vocabulary in a
// left column (chezmoi doctor), significance-first ordering rather than
// alphabetical, and roomy rows rather than a dense table, because the page is
// meant to be read once and understood, not scanned for a known key.
//
// Deliberately absent: color as the sole carrier of meaning. Every tier is
// named in words as well as marked with a glyph, so the page survives
// NO_COLOR, a pipe into less, and a CI log.
package ctxlayers

import (
	"fmt"
	"io"
	"strings"
	"unicode/utf8"
)

// boxWidth is the inner width of the layer box. 72 keeps the whole page
// inside an 80-column terminal with room for the frame and indent.
const boxWidth = 72

// tierGlyph and tierLabel are the fixed vocabulary. Three states, not two:
// the middle one — loaded as an index but not as text — is the distinction
// no existing tool shows, and the reason a repo can carry 60k tokens of
// skills for 1.4k tokens of rent.
func tierGlyph(t Tier) string {
	switch t {
	case TierAlways:
		return "*"
	case TierIndexed:
		return "~"
	case TierInert:
		return "x"
	default:
		return "."
	}
}

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

// Render writes the context page for entries. host is the detected host key
// or "" when unknown; repoName and hostLabel are shown in the header so a
// pasted page says which repo and which agent it describes.
func Render(w io.Writer, entries []Entry, repoName, hostLabel string, md bool) {
	if md {
		renderMarkdown(w, entries, repoName, hostLabel)
		return
	}
	renderTerminal(w, entries, repoName, hostLabel)
}

// renderTerminal prints the boxed layer cake.
func renderTerminal(w io.Writer, entries []Entry, repoName, hostLabel string) {
	fmt.Fprintf(w, "CONTEXT LAYERS — what an agent reads before your first word\n")
	fmt.Fprintf(w, "%s · %s\n\n", repoName, hostLabel)

	fmt.Fprintln(w, "  "+"+"+strings.Repeat("-", boxWidth)+"+")
	printed := false
	splitDone := false

	for _, e := range entries {
		// One rule between what is loaded and what is merely reachable. It is
		// the single most important boundary on the page, so it gets a line of
		// its own rather than a column.
		if !splitDone && !e.Tier.Loaded() {
			if printed {
				fmt.Fprintln(w, "  |"+strings.Repeat(" ", boxWidth)+"|")
			}
			fmt.Fprintln(w, "  "+centerRule("not loaded until something asks for it", boxWidth))
			splitDone = true
		} else if printed {
			fmt.Fprintln(w, "  |"+strings.Repeat(" ", boxWidth)+"|")
		}
		writeEntry(w, e)
		printed = true
	}

	fmt.Fprintln(w, "  "+"+"+strings.Repeat("-", boxWidth)+"+")
	fmt.Fprintln(w)

	loaded, idle := TotalsOf(entries)
	fmt.Fprintf(w, "  ~%s tokens loaded every turn   ·   ~%s tokens reachable but idle\n",
		commas(loaded/4), commas(idle/4))
	fmt.Fprintln(w, "  estimated at 4 bytes/token — your host's /context has the real numbers")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "  omakase context --show <path>   print a layer in full")
	fmt.Fprintln(w, "  omakase status                  the same harness as files and gates")
}

// writeEntry prints one row inside the box: a header line carrying the tier,
// group, path and cost, then the optional excerpt and note indented beneath
// it. The excerpt is the point of the whole verb — a layer that says what it
// tells the agent is worth more than a layer that only says where it lives.
func writeEntry(w io.Writer, e Entry) {
	cost := ""
	if e.Bytes > 0 {
		cost = "~" + commas(e.Tokens()) + " tok"
	}
	// The glyph, label and cost are fixed-width commitments; the path is the
	// only elastic field, so it is the one that gives way. Eliding from the
	// left keeps the filename, which is what identifies the layer.
	prefix := fmt.Sprintf(" %s %-10s ", tierGlyph(e.Tier), tierLabel(e.Tier))
	room := boxWidth - runeLen(prefix) - runeLen(cost) - 2
	fmt.Fprintln(w, "  |"+padBetween(prefix+elideLeft(e.Display, room), cost, boxWidth)+"|")

	meta := e.Prov
	if len(e.Hosts) == 1 {
		meta += " - " + e.Hosts[0] + " only"
	}
	if e.Count > 1 {
		meta = fmt.Sprintf("%s - %d files", meta, e.Count)
	}
	fmt.Fprintln(w, "  |"+pad(indent+meta, boxWidth)+"|")

	writeQuoted(w, e.Excerpt)
	if e.Points != "" {
		for _, line := range wrap("-> includes "+e.Points, boxWidth-runeLen(indent)) {
			fmt.Fprintln(w, "  |"+pad(indent+line, boxWidth)+"|")
		}
	}
	for _, line := range wrap(e.Note, boxWidth-runeLen(indent)) {
		fmt.Fprintln(w, "  |"+pad(indent+line, boxWidth)+"|")
	}
}

// indent is the hanging indent under a row header, aligning continuation
// lines with the path above them.
const indent = "              "

// writeQuoted prints an excerpt as one quotation spanning however many lines
// it needs — opening quote on the first, closing on the last. Quoting every
// wrapped line separately reads as a list of unrelated fragments.
func writeQuoted(w io.Writer, s string) {
	lines := wrap(s, boxWidth-runeLen(indent)-2)
	for i, line := range lines {
		switch {
		case len(lines) == 1:
			line = `"` + line + `"`
		case i == 0:
			line = `"` + line
		case i == len(lines)-1:
			line = " " + line + `"`
		default:
			line = " " + line
		}
		fmt.Fprintln(w, "  |"+pad(indent+line, boxWidth)+"|")
	}
}

// renderMarkdown prints the same layers as a table, for pasting into an issue
// or a PR. Markdown has no fixed-width frame to preserve, so the excerpt
// moves into its own column rather than wrapping under the row.
func renderMarkdown(w io.Writer, entries []Entry, repoName, hostLabel string) {
	fmt.Fprintln(w, "## Context layers")
	fmt.Fprintln(w)
	fmt.Fprintf(w, "`%s` · %s — what an agent reads before your first word.\n", repoName, hostLabel)
	fmt.Fprintln(w)
	fmt.Fprintln(w, "| Reach | Layer | Cost | From | Says |")
	fmt.Fprintln(w, "| --- | --- | --- | --- | --- |")
	for _, e := range entries {
		says := e.Excerpt
		if says == "" && e.Points != "" {
			says = "includes `" + e.Points + "`"
		}
		if says == "" {
			says = e.Note
		}
		cost := ""
		if e.Bytes > 0 {
			cost = "~" + commas(e.Tokens()) + " tok"
		}
		fmt.Fprintf(w, "| %s | `%s` | %s | %s | %s |\n",
			tierLabel(e.Tier), e.Display, cost, e.Prov, mdEscape(says))
	}
	fmt.Fprintln(w)
	loaded, idle := TotalsOf(entries)
	fmt.Fprintf(w, "**~%s tokens loaded every turn** · ~%s tokens reachable but idle. ",
		commas(loaded/4), commas(idle/4))
	fmt.Fprintln(w, "Estimated at 4 bytes/token; your host's `/context` has the real numbers.")
}

// centerRule is a box rule with a label centred in it.
func centerRule(label string, width int) string {
	label = " " + label + " "
	if runeLen(label) >= width {
		return "|" + string([]rune(label)[:width]) + "|"
	}
	left := (width - runeLen(label)) / 2
	return "+" + strings.Repeat("-", left) + label +
		strings.Repeat("-", width-left-runeLen(label)) + "+"
}

// runeLen is the column width of s. Every pad and truncate in this file
// counts runes, not bytes: a single multi-byte character counted as its byte
// length silently shortens the row and breaks the box frame.
func runeLen(s string) int { return utf8.RuneCountInString(s) }

// pad right-pads s to width, truncating when it would overflow the frame.
func pad(s string, width int) string {
	n := runeLen(s)
	if n > width {
		return string([]rune(s)[:width])
	}
	return s + strings.Repeat(" ", width-n)
}

// padBetween places left and right at the two ends of a width-wide field.
func padBetween(left, right string, width int) string {
	gap := width - runeLen(left) - runeLen(right) - 1
	if gap < 1 {
		return pad(left+" "+right, width)
	}
	return left + strings.Repeat(" ", gap) + right + " "
}

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

// wrap breaks s into lines of at most width characters on word boundaries,
// returning nil for empty input so callers can range over it unconditionally.
// A single word longer than the line is hard-broken rather than allowed to
// overflow: instruction files are full of long paths, and one of them must
// not be able to punch through the box frame.
func wrap(s string, width int) []string {
	if s == "" || width <= 0 {
		return nil
	}
	var out []string
	line := ""
	flush := func() {
		if line != "" {
			out = append(out, line)
			line = ""
		}
	}
	for _, word := range strings.Fields(s) {
		for runeLen(word) > width {
			flush()
			r := []rune(word)
			out = append(out, string(r[:width]))
			word = string(r[width:])
		}
		switch {
		case line == "":
			line = word
		case runeLen(line)+1+runeLen(word) <= width:
			line += " " + word
		default:
			flush()
			line = word
		}
	}
	flush()
	return out
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
