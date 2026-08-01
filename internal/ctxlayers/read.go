// This file holds ctxlayers' filesystem readers: the excerpt and
// frontmatter-description extractors that let a layer show what it SAYS
// rather than only where it lives, plus the small directory walkers the
// scanner aggregates over.
package ctxlayers

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
	"unicode"
)

// excerptMaxRunes caps a quoted excerpt. Two terminal lines' worth: enough to
// carry a rule's actual instruction, short enough that a rambling file cannot
// take over the page.
const excerptMaxRunes = 150

// excerptOf returns a representative sentence from a markdown instruction
// file — the first real prose, skipping YAML frontmatter, headings, HTML
// comments, list bullets, code fences, and badge/link noise. It returns ""
// when the file has no prose worth quoting, which the renderer treats as
// "show the path alone" rather than printing an empty quote.
//
// A file whose whole content is an @-include is reported as the pointer it
// is. Quoting the include path as if it were an instruction is misleading:
// the text the agent sees lives somewhere else, and saying so is the useful
// fact.
func excerptOf(path string) string {
	if includeTargetOf(path) != "" {
		return "" // a pointer has no prose of its own; see IncludeTargetOf
	}
	return prosePreviewOf(path)
}

// IncludeTargetOf is includeTargetOf's exported form, used by the scanner to
// fill Entry.Points.
func IncludeTargetOf(path string) string { return includeTargetOf(path) }

// includeTargetOf returns the referenced path when a file's only meaningful
// content is one @-include directive, else "". Harnesses use these pointer
// files to keep a single copy of a rule while satisfying two hosts' layouts.
// A pointer normally still carries YAML frontmatter (an applyTo/paths scope),
// so the frontmatter block is skipped before the body is judged.
func includeTargetOf(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 256*1024)

	target := ""
	first, inFrontmatter := true, false
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())

		if first && line == "---" {
			first, inFrontmatter = false, true
			continue
		}
		first = false
		if inFrontmatter {
			if line == "---" {
				inFrontmatter = false
			}
			continue
		}
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "@") && !strings.Contains(line, " ") {
			if target != "" {
				return "" // more than one include: not a simple pointer
			}
			target = strings.TrimPrefix(line, "@")
			continue
		}
		return "" // real content alongside the include
	}
	if target == "" {
		return ""
	}
	// The "../.." prefixes are an artefact of where the pointer sits; the
	// destination is what the reader needs.
	return path4Display(target)
}

// path4Display strips leading parent-directory hops from an include target so
// the row names the destination rather than the route to it.
func path4Display(p string) string {
	for strings.HasPrefix(p, "../") {
		p = strings.TrimPrefix(p, "../")
	}
	return strings.TrimPrefix(p, "./")
}

// prosePreviewOf is excerptOf's ordinary path: the first real sentences of a
// markdown document.
func prosePreviewOf(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	inFrontmatter, inFence, first := false, false, true
	var b strings.Builder

	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())

		// YAML frontmatter: only when it opens the file.
		if first && line == "---" {
			inFrontmatter, first = true, false
			continue
		}
		first = false
		if inFrontmatter {
			if line == "---" {
				inFrontmatter = false
			}
			continue
		}
		if strings.HasPrefix(line, "```") || strings.HasPrefix(line, "~~~") {
			inFence = !inFence
			continue
		}
		if inFence || !isProse(line) {
			// A blank line ends an excerpt already in progress.
			if b.Len() > 0 && line == "" {
				break
			}
			continue
		}

		if b.Len() > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(stripInlineMarkup(line))
		if b.Len() >= excerptMaxRunes || strings.HasSuffix(line, ".") {
			break
		}
	}
	return truncate(collapseSpaces(b.String()), excerptMaxRunes)
}

// isProse reports whether a trimmed markdown line is a sentence rather than
// structure. Headings are titles, not instructions; bullets are usually
// fragments that read badly out of context; tables and HTML are markup.
func isProse(line string) bool {
	if line == "" {
		return false
	}
	switch line[0] {
	case '#', '-', '*', '+', '>', '|', '<', '[', '!', '=':
		return false
	}
	// "1. " style ordered-list items.
	if len(line) > 2 && unicode.IsDigit(rune(line[0])) &&
		(strings.HasPrefix(line[1:], ". ") || strings.HasPrefix(line[1:], ") ")) {
		return false
	}
	// A line that is entirely a key: value pair reads as config, not prose.
	if i := strings.IndexByte(line, ':'); i > 0 && i < 20 && !strings.Contains(line[:i], " ") {
		return false
	}
	return true
}

// stripInlineMarkup removes the markdown emphasis and code markers that make
// a quoted line look like source instead of a sentence.
func stripInlineMarkup(s string) string {
	r := strings.NewReplacer("**", "", "`", "", "__", "", "*", "")
	return r.Replace(s)
}

// scopeGlobsOf returns the path globs a rule file is scoped to, parsed from
// its YAML frontmatter, or nil when the file is unscoped. Claude Code rules
// scope with a `paths:` list; Copilot instruction files scope with an
// `applyTo:` value (comma-separated globs on one line). A scoped rule loads
// only when the session touches a matching file — reporting it as loaded
// every turn overstates the always-on cost by the size of the file, which on
// a rule-heavy harness is most of the page (the pixterm lesson).
//
// A glob that matches everything ("**", "**/*") is not a scope; a file
// carrying only that is reported unscoped.
func scopeGlobsOf(path string) []string {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 256*1024)

	opened, inPaths := false, false
	var globs []string
	for sc.Scan() {
		line := sc.Text()
		trimmed := strings.TrimSpace(line)

		if !opened {
			if trimmed == "---" {
				opened = true
				continue
			}
			return nil // no frontmatter at all
		}
		if trimmed == "---" {
			break
		}
		if inPaths {
			if strings.HasPrefix(trimmed, "- ") {
				if g := yamlScalar(strings.TrimPrefix(trimmed, "- ")); g != "" {
					globs = append(globs, g)
				}
				continue
			}
			inPaths = false // a new key ends the list
		}
		if trimmed == "paths:" {
			inPaths = true
			continue
		}
		if rest, ok := strings.CutPrefix(trimmed, "applyTo:"); ok {
			for _, g := range strings.Split(rest, ",") {
				if g = yamlScalar(g); g != "" {
					globs = append(globs, g)
				}
			}
		}
	}
	// Drop match-everything globs; if nothing else remains the rule is
	// effectively unscoped.
	var scoped []string
	for _, g := range globs {
		if g != "**" && g != "**/*" && g != "*" {
			scoped = append(scoped, g)
		}
	}
	return scoped
}

// yamlScalar strips the quotes and surrounding space off a one-line YAML
// scalar value.
func yamlScalar(s string) string {
	s = strings.TrimSpace(s)
	s = strings.Trim(s, `"'`)
	return strings.TrimSpace(s)
}

// descriptionOf returns a skill's frontmatter description with folded
// continuation lines joined. It parses only the frontmatter block, so a body
// line that happens to begin "description:" cannot inflate the result — the
// bug that makes a naive grep report a skill index an order of magnitude
// larger than it is.
func descriptionOf(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	opened, capturing := false, false
	var b strings.Builder

	for sc.Scan() {
		line := sc.Text()
		trimmed := strings.TrimSpace(line)

		if !opened {
			if trimmed == "---" {
				opened = true
				continue
			}
			return "" // no frontmatter at all
		}
		if trimmed == "---" {
			break // end of frontmatter
		}
		if capturing {
			// Folded YAML continuations are indented; a new top-level key ends
			// the value.
			if line != "" && (line[0] == ' ' || line[0] == '\t') {
				b.WriteByte(' ')
				b.WriteString(trimmed)
				continue
			}
			break
		}
		if rest, ok := strings.CutPrefix(trimmed, "description:"); ok {
			capturing = true
			b.WriteString(strings.TrimSpace(rest))
		}
	}
	return collapseSpaces(b.String())
}

// skillDirs lists the immediate subdirectories of a skills root — one per
// skill. A skill is a directory, so its scripts, tests, and references never
// count as separate entries.
func skillDirs(root string) []string {
	ents, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range ents {
		if e.IsDir() {
			out = append(out, filepath.Join(root, e.Name()))
		}
	}
	return out
}

// mdFilesUnder lists repo-relative markdown files directly under dir, sorted
// by ReadDir order (already lexical).
func mdFilesUnder(root, dir string) []string {
	ents, err := os.ReadDir(filepath.Join(root, dir))
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range ents {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".md") {
			out = append(out, filepath.ToSlash(filepath.Join(dir, e.Name())))
		}
	}
	return out
}

// dirBytes totals the markdown bytes directly under dir and counts the files.
func dirBytes(dir string) (bytes, count int) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return 0, 0
	}
	for _, e := range ents {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		if n := fileBytes(filepath.Join(dir, e.Name())); n > 0 {
			bytes += n
			count++
		}
	}
	return bytes, count
}

// collapseSpaces squeezes runs of whitespace to single spaces and trims.
func collapseSpaces(s string) string { return strings.Join(strings.Fields(s), " ") }

// truncate shortens s to at most n runes, ending with a single ellipsis and
// never splitting mid-word when a space is close enough to the cut.
func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	cut := string(r[:n])
	if i := strings.LastIndexByte(cut, ' '); i > n*3/4 {
		cut = cut[:i]
	}
	return strings.TrimRight(cut, " ,;:") + "…"
}

// plural returns "N one" or "N many".
func plural(n int, one, many string) string {
	word := many
	if n == 1 {
		word = one
	}
	return itoa(n) + " " + word
}

// itoa is strconv.Itoa under a short name, kept local so the render and scan
// files share one spelling for the many small counts they format.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
