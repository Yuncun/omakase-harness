// This file renders the steering stack: one band per layer of steering —
// yours (global + parent-dir + untracked local), the overlaid harness
// (injected), and the project's own (committed) — sized against each other,
// with solid cells for bytes read every turn and hollow cells for bytes
// that wait until needed. Three lines answer "how much steering is here,
// whose is it, and what does it cost right now"; the shape is the answer
// and the numbers are the caption. An empty band prints "— none": in a repo
// with no harness the empty middle band is the install prompt.
package ctxlayers

import (
	"fmt"
	"io"
	"strings"
)

// stackWidth is the widest band bar — wider than the per-file bars because
// one band must be able to dwarf another visibly.
const stackWidth = 28

// Band is one steering layer's byte totals.
type Band struct {
	Name   string
	Loaded int // bytes read every turn
	Idle   int // bytes reachable on demand
}

// StackOf folds the scan into the three bands, in page order. Inert entries
// (files the detected host never reads) belong to no band.
func StackOf(entries []Entry) []Band {
	bands := []Band{{Name: "you"}, {Name: "harness"}, {Name: "project"}}
	idx := map[string]int{"you": 0, "harness": 1, "project": 2}
	for _, e := range entries {
		if e.Tier == TierInert {
			continue
		}
		b := &bands[idx[bandOf(e.Prov)]]
		if e.Tier.Loaded() {
			b.Loaded += e.Bytes
		} else {
			b.Idle += e.Bytes
		}
	}
	return bands
}

// bandOf maps an entry's provenance to its band. Untracked local files are
// yours, not the project's — the project only owns what it commits.
func bandOf(prov string) string {
	switch prov {
	case "global", "parent dir", "yours":
		return "you"
	case "injected":
		return "harness"
	}
	return "project"
}

// kfmt renders a token count for a band: always in k with one decimal — the
// stack is shape first, so one coarse precision for all bands. A nonzero
// count that would round to 0.0k prints <0.1k rather than lying either way.
func kfmt(tokens int) string {
	if tokens > 0 && tokens < 50 {
		return "<0.1k"
	}
	return fmt.Sprintf("~%.1fk", float64(tokens)/1000)
}

// bandBar draws loaded bytes as solid cells and idle bytes as hollow cells,
// scaled to the largest band. Nonzero parts draw at least one cell; the
// idle part yields first when both are capped into the width.
func bandBar(b Band, max int) string {
	if max <= 0 {
		return ""
	}
	cells := func(bytes int) int {
		if bytes <= 0 {
			return 0
		}
		n := (bytes*stackWidth + max - 1) / max
		if n < 1 {
			n = 1
		}
		return n
	}
	solid, hollow := cells(b.Loaded), cells(b.Idle)
	if solid+hollow > stackWidth {
		hollow = stackWidth - solid
		if hollow < 0 {
			solid, hollow = stackWidth, 0
		}
	}
	return strings.Repeat("█", solid) + strings.Repeat("░", hollow)
}

// stackLegend explains the two textures; it is the only caption the stack
// gets.
const stackLegend = "█ every turn · ░ on demand"

// RenderStack writes the steering stack. Terminal: a STEERING header with
// the legend floated right, then three fixed rows. Markdown: a table with
// the legend as the bar column's header.
func RenderStack(w io.Writer, entries []Entry, md bool) {
	bands := StackOf(entries)
	max := 0
	for _, b := range bands {
		if t := b.Loaded + b.Idle; t > max {
			max = t
		}
	}

	if md {
		fmt.Fprintln(w, "### Steering")
		fmt.Fprintln(w)
		fmt.Fprintf(w, "| | %s | ~tok |\n", stackLegend)
		fmt.Fprintln(w, "| --- | --- | ---: |")
		for _, b := range bands {
			if b.Loaded+b.Idle == 0 {
				fmt.Fprintf(w, "| %s | — none | |\n", b.Name)
				continue
			}
			fmt.Fprintf(w, "| %s | %s | %s |\n", b.Name, bandBar(b, max), kfmt((b.Loaded+b.Idle)/4))
		}
		return
	}

	nums := make([]string, len(bands))
	wN := 0
	for i, b := range bands {
		if t := b.Loaded + b.Idle; t > 0 {
			nums[i] = kfmt(t / 4)
		}
		if l := runeLen(nums[i]); l > wN {
			wN = l
		}
	}
	rowLen := 2 + 7 + 3 + stackWidth + 2 + wN
	pad := rowLen - runeLen("STEERING") - runeLen(stackLegend)
	if pad < 2 {
		pad = 2
	}
	fmt.Fprintln(w, "STEERING"+strings.Repeat(" ", pad)+stackLegend)
	for i, b := range bands {
		if b.Loaded+b.Idle == 0 {
			fmt.Fprintf(w, "  %-7s   — none\n", b.Name)
			continue
		}
		fmt.Fprintf(w, "  %-7s   %-*s  %*s\n", b.Name, stackWidth, bandBar(b, max), wN, nums[i])
	}
}
