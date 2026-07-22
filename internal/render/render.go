// Package render turns a probe.State into the status-bar segment. It is
// the ONLY place a user-facing string or color for that surface exists (issue #85's one-render-layer rule): rewording
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
// Shape: `<icon> <project>[:<worktree>] ⎇<branch> · <harness>` followed by
// the state — ✓ on a green pill (with a live `· <gate> 12s…` suffix while a
// gate runs), one problem fact and its fix on an amber pill, or a dim
// "unverified" when a proof could not run. ⎇ always carries the branch (its
// conventional meaning): a worktree's FOLDER name is frozen at creation and
// goes stale as branches change inside it — which is exactly why it appears
// in the location slot, not the branch slot.
//
// The amber state is one fact + one fix, never counts or a fact list —
// counts and per-file detail belong to `omakase status` (#85 field audit:
// no comparable tool renders counted health facts on an ambient surface).
// Hooks-not-installed outranks files-changed because a dead hook runs
// nothing, and a proven problem outranks an unverified proof because it is
// actionable now; neither ordering can paint green.
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

	// The location slot is repo[:worktree] — the worktree name marks it
	// unmistakably as "where this bar is looking", since the segment tracks
	// the session's live cwd by design (#85).
	identity := icon + " " + st.Project
	if st.Worktree != "" {
		identity += ":" + st.Worktree
	}
	if st.Branch != "" {
		identity += " ⎇" + st.Branch
	}
	if h := HarnessSlot(st); h != "" {
		identity += " · " + h
	}

	// The live heartbeat: shown only on the healthy pill — while a gate
	// runs, the last proven state is still the truth of the bar, and a
	// problem fact outranks progress detail.
	running := ""
	if st.Running != nil {
		running = fmt.Sprintf(" · %s %ds…", st.Running.Name, st.Running.Seconds)
	}

	problem := problemFact(st)
	unknown := st.HooksInstalled == probe.Unknown || st.GatesMigrated == probe.Unknown || st.FilesPresent == probe.Unknown || st.HashesMatch == probe.Unknown

	switch {
	case problem != "":
		return pill(identity+" ", dimOn, o.Color) + " " + pill("⚠ "+problem+" — omakase init ", amberOn, o.Color)
	case unknown:
		return pill(identity+" · unverified ", dimOn, o.Color)
	default:
		return pill(identity+" ✓"+running+" ", greenOn, o.Color)
	}
}

// InitVerdict is the closing line of `omakase init`: the same three proofs
// the status bar renders, run fresh after the install, so init ends with
// evidence instead of an assertion (#85 — the asserted "hooks installed"
// shipped the green-while-broken counter-example, #72). The fix verb cannot
// be `omakase init` here — it just ran — so failures point at status.
func InitVerdict(st *probe.State) string {
	if st == nil {
		return "omakase: could not verify the install — run omakase status"
	}
	switch {
	case st.HooksInstalled == probe.Problem:
		return "omakase: NOT verified — hooks not installed — run omakase status"
	case st.GatesMigrated == probe.Problem:
		return "omakase: NOT verified — harness needs migration — run omakase status"
	case st.FilesPresent == probe.Problem || st.HashesMatch == probe.Problem:
		return "omakase: NOT verified — harness files changed — run omakase status"
	case st.HooksInstalled == probe.OK && st.GatesMigrated == probe.OK && st.FilesPresent == probe.OK && st.HashesMatch == probe.OK:
		s := "omakase: verified — hooks installed ✓ · files present ✓ · files match ✓"
		// Kept files read green by design (the ledger hash is the accepted
		// hash); the verdict still names them so consent is visible at the
		// moment init reports (#98 Part 2).
		if st.Kept > 0 {
			s += fmt.Sprintf(" · %d kept (yours)", st.Kept)
		}
		return s
	}
	return "omakase: could not verify the install — run omakase status"
}

// HarnessSlot is the harness identity shown after the repo facts: the NAME
// override, else the manifest's declared name: (the harness's identity —
// #131 gripe 5), else the source's short name, else "" for a bare base
// install (the icon already says omakase; repeating it is noise). The base
// payload's own name: ("omakase-base") is skipped like the bare install it
// is — with no custom harness the icon already carries the identity.
func HarnessSlot(st *probe.State) string {
	if st.NameOverride != "" {
		return st.NameOverride
	}
	if st.ManifestName != "" && st.Source != "" {
		return st.ManifestName
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

// problemFact collapses the affirmatively-failing proofs to the single most
// severe fact, or "" when none fail. Missing and drifted files share one
// fact — the fix is the same either way, and the post-checkout heal already
// restores missing files silently on the next checkout.
func problemFact(st *probe.State) string {
	switch {
	case st.HooksInstalled == probe.Problem:
		return "hooks not installed"
	case st.GatesMigrated == probe.Problem:
		return "harness needs migration"
	case st.FilesPresent == probe.Problem || st.HashesMatch == probe.Problem:
		return "harness files changed"
	}
	return ""
}

// pill wraps s in one color segment, or returns it bare without color.
// The trailing space inside s keeps the colored block visually closed.
func pill(s, color string, on bool) string {
	if !on {
		return strings.TrimRight(s, " ")
	}
	return color + s + colOff
}
