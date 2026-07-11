package textblock

import "testing"

const (
	begin = "# >>> omakase-harness >>>"
	end   = "# <<< omakase-harness <<<"
)

// ---------------------------------------------------------------- Strip
//
// Strip removes the block delimited by the begin and end markers (inclusive)
// and emits every surviving line newline-terminated — so a final source line
// with no trailing newline comes back "\n"-terminated.
func TestStrip(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "block in the middle, trailing newline preserved outside",
			input: "scratch/\n*.tmp\n" + begin + "\nfoo\nbar\n" + end + "\n",
			want:  "scratch/\n*.tmp\n",
		},
		{
			// Final line "*.tmp" has no trailing newline and there is no block
			// present; Strip still adds one.
			name:  "final unterminated line outside the block gains a trailing newline",
			input: "scratch/\n*.tmp",
			want:  "scratch/\n*.tmp\n",
		},
		{
			// Missing end marker: every line from begin to EOF (including a
			// final unterminated one) is dropped.
			name:  "missing end marker drops everything from begin to EOF",
			input: "keep1\n" + begin + "\ndropped1\ndropped2",
			want:  "keep1\n",
		},
		{
			name:  "empty content produces empty output",
			input: "",
			want:  "",
		},
		{
			// No block at all: passthrough, still gains the trailing newline
			// on an unterminated final line.
			name:  "no block present: passthrough with awk's added trailing newline",
			input: "just one line no block",
			want:  "just one line no block\n",
		},
		{
			name:  "begin line itself is swallowed even with an empty body",
			input: begin + "\n" + end + "\n",
			want:  "",
		},
		{
			name:  "two block occurrences both stripped",
			input: "a\n" + begin + "\nx\n" + end + "\nb\n" + begin + "\ny\n" + end + "\nc\n",
			want:  "a\nb\nc\n",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Strip([]byte(tc.input), begin, end)
			if string(got) != tc.want {
				t.Errorf("Strip(%q) = %q, want %q", tc.input, string(got), tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------- AppendBlock

func TestAppendBlock(t *testing.T) {
	cases := []struct {
		name    string
		content string
		entries []string
		want    string
	}{
		{
			name:    "appends begin, each entry, end onto stripped (newline-terminated) content",
			content: "scratch/\n*.tmp\n",
			entries: []string{".claude/", ".omakase/", "lefthook.yml"},
			want:    "scratch/\n*.tmp\n" + begin + "\n.claude/\n.omakase/\nlefthook.yml\n" + end + "\n",
		},
		{
			name:    "empty content (fresh file): block is the whole output",
			content: "",
			entries: []string{".claude/"},
			want:    begin + "\n.claude/\n" + end + "\n",
		},
		{
			name:    "zero entries: begin immediately followed by end",
			content: "",
			entries: nil,
			want:    begin + "\n" + end + "\n",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := AppendBlock([]byte(tc.content), begin, tc.entries, end)
			if string(got) != tc.want {
				t.Errorf("AppendBlock(%q, entries=%v) = %q, want %q", tc.content, tc.entries, string(got), tc.want)
			}
		})
	}
}

// Round-trip: Strip(AppendBlock(x)) reproduces x when x itself has no
// pre-existing block.
func TestStripAppendBlockRoundTrip(t *testing.T) {
	base := "scratch/\n*.tmp\n"
	entries := []string{".claude/", "lefthook.yml", ".worktreeinclude"}

	appended := AppendBlock([]byte(base), begin, entries, end)
	stripped := Strip(appended, begin, end)

	if string(stripped) != base {
		t.Errorf("Strip(AppendBlock(base)) = %q, want %q", string(stripped), base)
	}

	// Re-running strip+append on the already-blocked content is idempotent.
	reAppended := AppendBlock(stripped, begin, entries, end)
	if string(reAppended) != string(appended) {
		t.Errorf("second AppendBlock pass = %q, want byte-identical %q", string(reAppended), string(appended))
	}
}
