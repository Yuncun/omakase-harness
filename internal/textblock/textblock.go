// Package textblock implements the marked-block strip/append idiom used
// throughout omakase-harness to keep a re-run idempotent: an owned block
// delimited by a whole-line begin/end marker pair is stripped from a file's
// content, then a fresh block is appended.
package textblock

import "bytes"

// Strip removes an owned block from content, processing each line in order:
//  1. a line equal to begin (whole-line match) starts the block; the begin
//     line itself is not emitted.
//  2. lines outside the block are emitted; lines inside it (including a block
//     with no closing marker) are dropped.
//  3. a line equal to end ends the block after that line's emit test, so the
//     end marker line itself is also dropped.
//
// Two behaviors that are easy to get wrong:
//   - A missing end marker is not an error: everything from begin to EOF is
//     dropped.
//   - Every emitted line is "\n"-terminated, even one read from a final
//     unterminated line, so a line that had no trailing "\n" in the source
//     comes back "\n"-terminated.
//
// Lines are content split on "\n": a trailing "\n" produces no spurious final
// empty line, and empty content produces no lines.
func Strip(content []byte, begin, end string) []byte {
	if len(content) == 0 {
		return nil
	}

	lines := bytes.Split(content, []byte("\n"))
	if bytes.HasSuffix(content, []byte("\n")) {
		lines = lines[:len(lines)-1]
	}

	var out bytes.Buffer
	inBlock := false
	beginB, endB := []byte(begin), []byte(end)
	for _, line := range lines {
		if bytes.Equal(line, beginB) {
			inBlock = true
		}
		if !inBlock {
			out.Write(line)
			out.WriteByte('\n')
		}
		if bytes.Equal(line, endB) {
			inBlock = false
		}
	}
	return out.Bytes()
}

// AppendBlock appends `begin\n`, each entry followed by `\n`, then `end\n`
// directly onto content — a pure concatenation, no transformation of content.
// Callers pass Strip's output, which is "\n"-terminated unless empty, so the
// appended block always starts on its own line.
func AppendBlock(content []byte, begin string, entries []string, end string) []byte {
	var out bytes.Buffer
	out.Write(content)
	out.WriteString(begin)
	out.WriteByte('\n')
	for _, e := range entries {
		out.WriteString(e)
		out.WriteByte('\n')
	}
	out.WriteString(end)
	out.WriteByte('\n')
	return out.Bytes()
}
