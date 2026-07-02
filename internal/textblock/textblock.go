// Package textblock ports the marked-block strip/append idiom used
// throughout omakase-harness to keep a re-run idempotent: an owned block
// delimited by a whole-line begin/end marker pair is stripped from a file's
// content, then a fresh block is appended. Four bash call sites use the
// exact same awk one-liner for the strip half (bin/init.sh:556,
// bin/remove.sh:33,80,89) and the exact same `{ ... } >> file` shape for the
// append half (bin/init.sh:557-564,573-581). Go twin of both —
// DUPLICATED bash<->Go until Phase 2 retires the bash callers; keep in
// lockstep.
package textblock

import "bytes"

// Strip mirrors the awk program every bash marked-block strip uses:
//
//	awk -v b="$BEGIN" -v e="$END" '$0==b{s=1} !s{print} $0==e{s=0}'
//
// Per-line (per-record), in order, exactly as awk evaluates the three
// pattern-action pairs against each record in turn:
//  1. a line EQUAL to begin (whole-line match) sets s=1 — the begin line
//     itself is NOT printed, because this action runs before the print
//     test below, so s is already 1 by the time that line is tested.
//  2. while s is false, the line is printed; while s is true (inside a
//     block, including one with no closing marker), the line is dropped.
//  3. a line equal to end sets s=0 — AFTER the print test for that same
//     line, so the end marker line itself is also dropped, matching how
//     the begin line is dropped.
//
// Two behaviors worth calling out because they are easy to get wrong in a
// naive line-split port:
//   - A missing end marker is NOT an error: everything from begin to EOF is
//     dropped, s never resets.
//   - awk's `print` always appends ORS ("\n" here), even for a record read
//     from a final, unterminated line — so a printed line that had no
//     trailing "\n" in the source comes back "\n"-terminated. Confirmed
//     against the live awk and pinned by a test vector in
//     textblock_test.go (see that file's doc comment for the exact
//     generating command).
//
// Records (awk's line-splitting) are reproduced as: split content on "\n";
// if content ends with "\n", drop the resulting spurious trailing empty
// element (there is no record after the final newline); empty content
// splits to zero records, matching awk's behavior on a 0-byte input.
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

// AppendBlock mirrors the block-append heredoc used right after Strip
// (bin/init.sh:557-564, :573-581): it appends `begin\n`, each entry
// followed by `\n`, then `end\n`, directly onto content — a pure
// concatenation, no transformation of content itself. Callers pass the
// STRIPPED content (Strip's output is always "\n"-terminated unless empty,
// matching what awk's `print` left in the real file before the `>>`
// append), so the appended block always starts on its own line.
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
