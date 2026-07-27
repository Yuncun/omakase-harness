package status

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// bannerCells counts terminal cells in one plain banner line, treating any
// non-ASCII rune the way the renderer budgets the icon (2 cells) — good
// enough for the box glyphs (1 cell each) plus one emoji icon.
func bannerCells(line string) int {
	n := 0
	for _, r := range line {
		if r > 0xFF { // box-drawing + emoji; ╭─│ are 1 cell, emoji 2
			if r >= 0x1F000 {
				n += 2
				continue
			}
		}
		n++
	}
	return n
}

func TestBannerPlainGeometry(t *testing.T) {
	out := renderBanner("🥡", "acme-dev-harness v0.11.3", false)
	lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("banner is %d lines, want 3: %q", len(lines), out)
	}
	w := bannerCells(lines[0])
	for i, l := range lines {
		if got := bannerCells(l); got != w {
			t.Errorf("line %d is %d cells, want %d (misaligned right edge)\n%s", i, got, w, out)
		}
	}
	if !strings.Contains(lines[1], "🥡 acme-dev-harness v0.11.3") {
		t.Errorf("content row missing the label: %q", lines[1])
	}
	if strings.Contains(out, "\033") {
		t.Errorf("plain banner contains escapes: %q", out)
	}
}

func TestBannerAsciiIconAligned(t *testing.T) {
	emoji := renderBanner("🥡", "same label", false)
	ascii := renderBanner("*", "same label", false)
	// An ASCII icon is 1 cell where the emoji is 2 — the renderer pads one
	// extra space so the right edge stays put.
	el := strings.Split(emoji, "\n")[1]
	al := strings.Split(ascii, "\n")[1]
	if utf8.RuneCountInString(al) != utf8.RuneCountInString(el)+1 {
		t.Errorf("ascii-icon row not repadded: emoji=%q ascii=%q", el, al)
	}
}

func TestBannerColorGradient(t *testing.T) {
	out := renderBanner("🥡", "x", true)
	if !strings.Contains(out, "\033[38;2;") || !strings.Contains(out, "\033[0m") {
		t.Errorf("color banner missing truecolor escapes: %q", out)
	}
}

func TestStatusBannerComposition(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	t.Setenv("OMAKASE_ICON", "")
	if out := statusBanner("acme", "1.2.3"); !strings.Contains(out, "🥡 acme v1.2.3") {
		t.Errorf("default icon + version missing: %q", out)
	}
	// Unknown base version: no dangling "v?".
	if out := statusBanner("acme", "?"); strings.Contains(out, "v?") || !strings.Contains(out, "🥡 acme ") {
		t.Errorf("unknown version handled wrong: %q", out)
	}
	t.Setenv("OMAKASE_ICON", "⚙️")
	if out := statusBanner("acme", "?"); !strings.Contains(out, "⚙️ acme") {
		t.Errorf("OMAKASE_ICON ignored: %q", out)
	}
}
