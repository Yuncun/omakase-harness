// Package render turns a probe.State into the status-bar segment and the
// stop-notice message. It is the ONLY place a user-facing string or color
// for those surfaces exists (issue #85's one-render-layer rule): rewording
// the bar, changing the palette, or adding a host-specific flavor touches
// this package and nothing else. Probes hand in facts; this package decides
// how they read.
//
// The one invariant renderers must keep: the affirmative (green / ✓) form
// appears only when every proof is affirmatively OK. A Problem renders the
// problem facts; an Unknown renders as unverified — never as working.
package render

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/Yuncun/omakase-harness/internal/probe"
)

// Opts are the presentation knobs the verb layer resolves from the
// environment (color from NO_COLOR, icon from OMAKASE_ICON).
type Opts struct {
	Color bool
	Icon  string // "" falls back to the omakase glyph
}

// Truecolor segments. Green is the proven pill (same palette the v1 bar
// used), amber the problem pill, dim the unverified tone.
const (
	greenOn = "\033[48;2;15;61;34m\033[38;2;126;226;160m"
	amberOn = "\033[48;2;92;54;10m\033[38;2;255;204;128m"
	dimOn   = "\033[38;2;150;150;150m"
	colOff  = "\033[0m"
)

var schemeRe = regexp.MustCompile(`^[a-z][a-z]*://`)

// Statusline renders the one-line status-bar segment, or "" (a dark
// segment) when st is nil or the harness is not installed here.
//
// Shape: `<icon> <project> ⎇<branch> · <harness>` followed by the state —
// ✓ on a green pill when all three proofs pass, the problem facts on an
// amber pill, or a dim "unverified" when a proof could not run. ⎇ always
// carries the branch (its conventional meaning): a worktree's FOLDER name
// is frozen at creation and goes stale as branches change inside it.
//
// The bar answers exactly one question — is the harness verifiably running
// here — and carries no workflow advice: worktree discipline (don't edit
// the main checkout while agent sessions run in worktrees) is harness
// POLICY, enforced by a custom harness's commit gate and the opt-in
// pre-edit guard, not by the base bar (#85 discussion; retired the #86
// soft layer).
func Statusline(st *probe.State, o Opts) string {
	if st == nil || !st.Installed {
		return ""
	}
	icon := o.Icon
	if icon == "" {
		icon = "🥡"
	}

	identity := icon + " " + st.Project
	if st.Branch != "" {
		identity += " ⎇" + st.Branch
	}
	if h := HarnessSlot(st); h != "" {
		identity += " · " + h
	}

	problems := problemFacts(st)
	unknown := st.Armed == probe.Unknown || st.FilesPresent == probe.Unknown || st.HashesMatch == probe.Unknown

	switch {
	case len(problems) > 0:
		if unknown {
			problems = append(problems, "unverified")
		}
		return pill(identity+" ", dimOn, o.Color) + " " + pill("⚠ "+strings.Join(problems, " · ")+" ", amberOn, o.Color)
	case unknown:
		return pill(identity+" · unverified ", dimOn, o.Color)
	default:
		return pill(identity+" ✓ ", greenOn, o.Color)
	}
}

// StopNotice renders the end-of-turn message body ("" = say nothing).
// ranThisTurn adds the last-run summary line; the caller owns the
// speak-on-change decision and the JSON envelope.
func StopNotice(st *probe.State, ranThisTurn bool) string {
	if st == nil || !st.Installed {
		return ""
	}
	name := HarnessSlot(st)
	if name == "" {
		name = "omakase"
	}

	if st.Armed == probe.Problem {
		return name + " is not active — hooks are not armed · omakase init to arm"
	}

	var nudges []string
	if st.FilesPresent == probe.Problem {
		nudges = append(nudges, name+" — "+countNoun(st.Missing, "file")+" missing · omakase init to update")
	}
	if st.HashesMatch == probe.Problem {
		nudges = append(nudges, name+" — files differ from canonical · omakase status to review")
	}
	if len(nudges) > 0 {
		return strings.Join(nudges, "\n")
	}

	if st.Armed == probe.Unknown || st.FilesPresent == probe.Unknown || st.HashesMatch == probe.Unknown {
		return name + " — state could not be verified"
	}

	msg := name + " is active ✓"
	if ranThisTurn && st.LastRun != nil && st.LastRun.Checks > 0 {
		at := clock(st.LastRun.Epoch)
		if st.LastRun.Failed > 0 {
			msg += fmt.Sprintf("\nLast run: %s failed at %s", countNoun(st.LastRun.Failed, "check"), at)
		} else {
			msg += fmt.Sprintf("\nLast run: %d/%d checks at %s", st.LastRun.Checks, st.LastRun.Checks, at)
		}
	}
	return msg
}

// HarnessSlot is the harness identity shown after the repo facts: the NAME
// override, else the source's short name, else "" for a bare base install
// (the icon already says omakase; repeating it is noise).
func HarnessSlot(st *probe.State) string {
	if st.NameOverride != "" {
		return st.NameOverride
	}
	if st.Source == "" {
		return ""
	}
	n := schemeRe.ReplaceAllString(st.Source, "")
	if i := strings.IndexByte(n, '#'); i >= 0 {
		n = n[:i]
	}
	n = strings.TrimSuffix(n, ".git")
	n = strings.TrimSuffix(n, "/")
	if i := strings.LastIndexByte(n, '/'); i >= 0 {
		n = n[i+1:]
	}
	return n
}

// problemFacts lists the affirmatively-failing proofs as short facts, in a
// fixed order so the bar is stable frame to frame.
func problemFacts(st *probe.State) []string {
	var facts []string
	if st.Armed == probe.Problem {
		facts = append(facts, "hooks not armed")
	}
	if st.FilesPresent == probe.Problem {
		facts = append(facts, countNoun(st.Missing, "file")+" missing")
	}
	if st.HashesMatch == probe.Problem {
		facts = append(facts, countNoun(st.Drifted, "file")+" drifted")
	}
	return facts
}

// pill wraps s in one color segment, or returns it bare without color.
// The trailing space inside s keeps the colored block visually closed.
func pill(s, color string, on bool) string {
	if !on {
		return strings.TrimRight(s, " ")
	}
	return color + s + colOff
}

// countNoun is "1 file" / "3 files".
func countNoun(n int, noun string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s", noun)
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

// clock formats an epoch as local wall-clock time ("3:42PM") — frozen text,
// so a relative "Nm ago" would go stale in a printed notice.
func clock(epoch int64) string {
	return time.Unix(epoch, 0).Format("3:04PM")
}
