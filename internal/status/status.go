// This file (status.go) is the `omakase status` verb entry point and the
// identity + full-output assembly of bin/status.sh (bin/status.sh:14-354): flag
// parsing, repo discovery, the placed.tsv-absent routing (pre-0.10 / not
// installed), the installed-harness identity line, and the markdown / terminal
// page order (identity -> footprint -> Guards -> inventory -> footer). The
// guards chart lives in guards.go; the inventory renderers in inventory.go.
package status

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/Yuncun/omakase-harness/internal/state"
	"github.com/Yuncun/omakase-harness/internal/tui"
)

// schemeRe strips a leading URL scheme (lowercase only) for srcDisplay
// (bin/status.sh:312, `sed -e 's,^[a-z][a-z]*://,,'`).
var schemeRe = regexp.MustCompile(`^[a-z][a-z]*://`)

// Run is the `omakase status` verb. argv is the arguments AFTER the verb; only
// argv[0] selects markdown mode (bin/status.sh:17). It discovers the repo from
// the current directory and writes the report to stdout. Not inside a git repo:
// the one stderr line + exit 1 (bin/status.sh:20). Exit 0 otherwise.
func Run(argv []string, stdout, stderr io.Writer) int {
	// --help/-h short-circuits to usage — scanned first so `status --help` on
	// a real terminal prints help rather than launching the interactive
	// screen. Any OTHER unrecognized dash-flag is an error: a typo like
	// `--enabel smoke` must not exit 0 with the page (an automation would read
	// that as "re-enabled, green") — the legacy fallback (bin/legacy/status.sh)
	// enforces the same rule in lockstep. Bare words keep falling through
	// (parity; `md` is a real alias).
	known := map[string]bool{
		"--markdown": true, "-m": true, "--plain": true,
		"--disable": true, "--enable": true,
	}
	for _, a := range argv {
		if a == "--help" || a == "-h" {
			printStatusUsage(stdout)
			return 0
		}
		if strings.HasPrefix(a, "-") && !known[a] {
			fmt.Fprintf(stderr, "omakase: unknown flag %s (see omakase status --help)\n", a)
			return 2
		}
	}

	md := len(argv) > 0 && (argv[0] == "--markdown" || argv[0] == "-m" || argv[0] == "md")

	// --plain forces the static page; --disable/--enable are the scriptable
	// twins of the interactive screen's Enter (agents cannot drive a TUI).
	plain := false
	for i := 0; i < len(argv); i++ {
		switch argv[i] {
		case "--plain":
			plain = true
		case "--disable", "--enable":
			if i+1 >= len(argv) {
				fmt.Fprintf(stderr, "omakase: %s needs a gate name or placed path\n", argv[i])
				return 2
			}
			return runToggle(argv[i] == "--disable", argv[i+1], stdout, stderr)
		}
	}
	// OMAKASE_ICON: default 🥡, used only in the md installed header
	// (bin/status.sh:18, Global Constraint 6).
	icon := os.Getenv("OMAKASE_ICON")
	if icon == "" {
		icon = "🥡"
	}

	wd, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(stderr, "omakase: not inside a git repo")
		return 1
	}
	repo, err := state.Discover(wd)
	if err != nil { // bin/status.sh:20
		fmt.Fprintln(stderr, "omakase: not inside a git repo")
		return 1
	}

	home := os.Getenv("HOME")
	placed := filepath.Join(repo.OMK, "placed.tsv")

	// Step 1 (bin/status.sh:276-302): placed.tsv absent -> pre-0.10 if
	// placed.list exists, else not-installed. Both render + exit 0.
	if _, e := os.Stat(placed); e != nil {
		if _, e2 := os.Stat(filepath.Join(repo.OMK, "placed.list")); e2 == nil {
			RenderPre010(stdout, repo.OMK, md)
		} else {
			RenderNotInstalled(stdout, repo, home, md)
		}
		return 0
	}

	// Step 2 — identity (bin/status.sh:309-314).
	src := state.FirstLine(filepath.Join(repo.OMK, "source"))
	hname := harnessName(src)
	srcdisp := srcDisplay(src)
	basever := state.FirstLine(filepath.Join(repo.Root, ".omakase", "VERSION"))
	if basever == "" { // ${basever:-?} renders "?" when empty (bin/status.sh:323)
		basever = "?"
	}
	// Count injected files by consent state: legacy counted every ledger row
	// (`grep -c .`), but this stack's toggles are the first writers of
	// enabled=0 rows — a declined file keeps its row but has no file on disk, so
	// the all-rows count would overstate the footprint. Count enabled rows for
	// the headline and note declined rows separately. An all-enabled repo has
	// no enabled=0 rows, so the output stays byte-identical to legacy.
	nInjected, nToggledOff := 0, 0
	for _, r := range state.ReadPlaced(placed) {
		if r.Enabled == "0" {
			nToggledOff++
		} else {
			nInjected++
		}
	}

	// Interactive dispatch (Task 8): on a real terminal, and only for the
	// default page (not --markdown, not the scriptable --plain), hand off to
	// the live interactive screen. It sits AFTER the placed.tsv-absent early
	// returns above, so not-installed / pre-0.10 repos keep the plain page.
	if !md && !plain && interactiveTerminal() {
		hdr := hname
		if srcdisp != "" {
			hdr = hname + " · " + srcdisp
		}
		foot := fmt.Sprintf("%d injected%s · 0 committed · all gitignored (.git/info/exclude)", nInjected, toggledOffSuffix(nToggledOff))
		return tui.Run(repo, hdr, foot)
	}

	if md {
		renderMarkdown(stdout, repo, home, icon, hname, srcdisp, basever, nInjected, nToggledOff)
		return 0
	}
	renderTerminal(stdout, repo, home, hname, srcdisp, basever, nInjected, nToggledOff)
	return 0
}

// toggledOffSuffix is " (k toggled off)" when k>0, else "" — appended after the
// injected count so a page whose consent state has diverged from the ledger
// says so, while an all-enabled page (k==0) stays byte-identical to legacy
// output (the status-parity fence).
func toggledOffSuffix(nToggledOff int) string {
	if nToggledOff > 0 {
		return fmt.Sprintf(" (%d toggled off)", nToggledOff)
	}
	return ""
}

// printStatusUsage prints the `omakase status` flag surface. The stack's new
// state-writing flags (--disable/--enable) and the interactive-on-TTY screen
// were undocumented, and unknown flags fall through silently; this handler is
// the discoverable surface for both.
func printStatusUsage(w io.Writer) {
	fmt.Fprint(w, `usage: omakase status [--markdown|--plain] [--disable NAME | --enable NAME]

  (no flags)      on a real terminal, open the interactive consent screen;
                  otherwise print the status page
  --markdown, -m  print the status page as markdown
  --plain         force the printed status page (never the interactive screen)
  --disable NAME  turn a gate off, or remove a placed file/dir; NAME is a wired
                  gate name or a placed path. Recorded so commits/pushes skip it
                  until re-enabled.
  --enable NAME   undo a --disable: restore the file/dir or re-arm the gate
  --help, -h      show this help
`)
}

// renderMarkdown is the md page (bin/status.sh:320-335): the script owns the
// formatting so `omakase status` relays it verbatim. Question-first order.
func renderMarkdown(w io.Writer, repo *state.Repo, home, icon, hname, srcdisp, basever string, nInjected, nToggledOff int) {
	fmt.Fprintf(w, "## %s %s\n", icon, hname)
	fmt.Fprintln(w)
	if srcdisp != "" {
		fmt.Fprintf(w, "`%s` · base omakase %s · installed in `%s`\n", srcdisp, basever, repo.Root)
	} else {
		fmt.Fprintf(w, "base omakase %s · installed in `%s`\n", basever, repo.Root)
	}
	fmt.Fprintln(w)
	fmt.Fprintf(w, "**Zero footprint** — %d file(s) injected%s, 0 committed; all gitignored via `.git/info/exclude` (invisible to git).\n", nInjected, toggledOffSuffix(nToggledOff))
	fmt.Fprintln(w)
	fmt.Fprintln(w, "### Guards — what runs when you commit / push")
	fmt.Fprintln(w)
	RenderGuards(w, repo.Root, repo.OMK, true)
	fmt.Fprintln(w)
	RenderInventory(w, repo, home, true)
	fmt.Fprintln(w)
	fmt.Fprintln(w, "_Refresh:_ `omakase init`  ·  _Remove:_ `omakase remove`  ·  _read-only; running status changes nothing._")
}

// renderTerminal is the default page (bin/status.sh:338-354): optional branded
// banner, then the same question-first order as markdown.
func renderTerminal(w io.Writer, repo *state.Repo, home, hname, srcdisp, basever string, nInjected, nToggledOff int) {
	// Banner (bin/status.sh:340-341, `bash "$BANNER" 2>/dev/null || true`): if
	// present, run it at the INVOCATION cwd (no `cd`), pass its stdout through,
	// discard stderr, ignore failure.
	banner := filepath.Join(repo.Root, ".omakase", "bin", "omakase-banner.sh")
	if info, e := os.Stat(banner); e == nil && !info.IsDir() {
		cmd := exec.Command("bash", banner)
		cmd.Stdout = w
		cmd.Stderr = io.Discard
		_ = cmd.Run()
	}

	if srcdisp != "" {
		fmt.Fprintf(w, "%s — %s · base omakase %s · installed in %s\n", hname, srcdisp, basever, repo.Root)
	} else {
		fmt.Fprintf(w, "installed in %s (base omakase %s)\n", repo.Root, basever)
	}
	fmt.Fprintf(w, "zero footprint: %d injected%s, 0 committed, all gitignored (.git/info/exclude)\n", nInjected, toggledOffSuffix(nToggledOff))
	fmt.Fprintln(w)
	fmt.Fprintln(w, "GUARDS — what runs when you commit / push")
	RenderGuards(w, repo.Root, repo.OMK, false)
	fmt.Fprintln(w)
	RenderInventory(w, repo, home, false)
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Update to the latest harness (syncs files; removes dropped ones):   omakase init")
	fmt.Fprintln(w, "Undo everything:                                                    omakase remove")
}

// harnessName derives the harness display name from the remembered source
// (bin/status.sh:310-311): "omakase-harness" unless a source is set, in which
// case strip from the first '#', then one trailing ".git", then one trailing
// "/", then take the last '/'-segment.
func harnessName(src string) string {
	if src == "" {
		return "omakase-harness"
	}
	n := src
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

// srcDisplay is the source shown in the identity line (bin/status.sh:312): the
// source minus a leading lowercase URL scheme and one trailing "/".
func srcDisplay(src string) string {
	d := schemeRe.ReplaceAllString(src, "")
	return strings.TrimSuffix(d, "/")
}
