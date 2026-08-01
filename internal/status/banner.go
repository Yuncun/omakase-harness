package status

import (
	"fmt"
	"os"
	"strings"
	"unicode/utf8"
)

// The banner is the branded box header the terminal status page opens with —
// built into the binary since #172 (the payload script omakase-banner.sh it
// replaces was optional UX nothing depended on). The icon is swappable via
// $OMAKASE_ICON; NO_COLOR strips the gray gradient.

// bannerW is the fixed inner width; it keeps the box aligned regardless of
// which icon is chosen.
const bannerW = 54

// bannerBorder emits one border row of bannerW ch glyphs, gray gradient
// bright in the middle when color is on.
func bannerBorder(ch string, color bool) string {
	var b strings.Builder
	for i := 0; i < bannerW; i++ {
		if !color {
			b.WriteString(ch)
			continue
		}
		d := i
		if i >= bannerW/2 {
			d = bannerW - 1 - i
		}
		g := 30 + (90*d)/(bannerW/2)
		fmt.Fprintf(&b, "\033[38;2;%d;%d;%dm%s", g, g, g, ch)
	}
	if color {
		b.WriteString("\033[0m")
	}
	return b.String()
}

// renderBanner returns the three-line box. The icon renders as ~2 terminal
// cells when it is an emoji but as its rune count when plain ASCII;
// measuring it explicitly keeps the right edge aligned for any icon.
func renderBanner(icon, text string, color bool) string {
	iw := utf8.RuneCountInString(icon)
	for _, r := range icon {
		if r > 0x7f {
			iw = 2
			break
		}
	}
	rest := " " + text
	pad := bannerW - 2 - iw - utf8.RuneCountInString(rest)
	if pad < 0 {
		pad = 0
	}
	spaces := strings.Repeat(" ", pad)
	top := "╭" + bannerBorder("─", color) + "╮"
	bot := "╰" + bannerBorder("─", color) + "╯"
	if color {
		edge := "\033[38;2;75;75;75m│\033[0m"
		return fmt.Sprintf("%s\n%s %s%s %s\n%s\n", top, edge, icon+rest, spaces, edge, bot)
	}
	return fmt.Sprintf("%s\n│ %s%s │\n%s\n", top, icon+rest, spaces, bot)
}

// statusBanner composes the page's banner from the harness identity: the
// icon (OMAKASE_ICON, default 🥡) and the harness display name. No version:
// the only version to hand is the BASE payload's (.omakase/VERSION), and
// gluing it to the harness name mislabels it as the harness's own — the
// identity line below states it as "base omakase X" instead.
func statusBanner(hname string) string {
	icon := os.Getenv("OMAKASE_ICON")
	if icon == "" {
		icon = "🥡"
	}
	return renderBanner(icon, hname, os.Getenv("NO_COLOR") == "")
}
