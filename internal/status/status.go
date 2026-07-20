// This file is the `omakase status` verb entry point: flag parsing, repo
// discovery, the placed.tsv-absent routing (pre-0.10 / not installed), the
// installed-harness identity line, and the markdown / terminal page order
// (identity -> footprint -> Guards -> inventory -> footer). The guards chart
// lives in guards.go; the inventory renderers in inventory.go.
package status

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/Yuncun/omakase-harness/internal/gate"
	"github.com/Yuncun/omakase-harness/internal/state"
	"github.com/Yuncun/omakase-harness/internal/tui"
)

// schemeRe strips a leading lowercase URL scheme for srcDisplay.
var schemeRe = regexp.MustCompile(`^[a-z][a-z]*://`)

// Run is the `omakase status` verb. argv is the arguments after the verb;
// only argv[0] selects markdown mode. It discovers the repo from the current
// directory and writes the report to stdout. Outside a git repo it writes one
// stderr line and exits 1; otherwise it exits 0.
func Run(argv []string, stdout, stderr io.Writer) int {
	// --help/-h short-circuits to usage — scanned first so `status --help` on
	// a real terminal prints help rather than launching the interactive
	// screen. Any other unrecognized dash-flag is an error: a typo like
	// `--enabel smoke` must not exit 0 with the page (an automation would read
	// that as "re-enabled, green"). Bare words keep falling through; `md` is a
	// real alias.
	known := map[string]bool{
		"--markdown": true, "-m": true, "--plain": true,
		"--disable": true, "--enable": true,
		"--keep": true, "--restore": true,
		"--global": true,
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
	// twins of the interactive screen's Enter (agents cannot drive a TUI);
	// --keep/--restore are their edit-lifecycle siblings (issue #98 Part 2).
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
		case "--keep", "--restore":
			if i+1 >= len(argv) {
				fmt.Fprintf(stderr, "omakase: %s needs a placed path\n", argv[i])
				return 2
			}
			return runKeepRestore(argv[i] == "--keep", argv[i+1], stdout, stderr)
		case "--global":
			// The expansion of the page's collapsed GLOBAL line. Personal
			// config lives under $HOME, not the repo, so this needs no repo
			// discovery and works uninstalled too.
			RenderGlobal(stdout, os.Getenv("HOME"), md)
			return 0
		}
	}
	// OMAKASE_ICON: default 🥡, used only in the md installed header.
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
	if err != nil {
		fmt.Fprintln(stderr, "omakase: not inside a git repo")
		return 1
	}

	home := os.Getenv("HOME")
	placed := filepath.Join(repo.OMK, "placed.tsv")

	// Step 1: placed.tsv absent -> pre-0.10 if placed.list exists, else
	// not-installed. Both render and exit 0.
	if _, e := os.Stat(placed); e != nil {
		if _, e2 := os.Stat(filepath.Join(repo.OMK, "placed.list")); e2 == nil {
			RenderPre010(stdout, repo.OMK, md)
		} else {
			RenderNotInstalled(stdout, repo, home, md)
		}
		return 0
	}

	// Step 2 — identity. The manifest's name: is the harness's declared
	// identity; the source's last folder is only the fallback when the
	// manifest declares none (#131 gripe 5). A bare base install (no source)
	// keeps the plain header — the base manifest's own name: adds nothing.
	src := state.FirstLine(filepath.Join(repo.OMK, "source"))
	hname := harnessName(src)
	if n := gate.LoadName(repo.OMK); n != "" && src != "" {
		hname = n
	}
	srcdisp := srcDisplay(src)
	basever := state.FirstLine(filepath.Join(repo.Root, ".omakase", "VERSION"))
	if basever == "" {
		basever = "?"
	}
	// Count injected files by consent state: a declined file keeps its ledger
	// row but has no file on disk, so counting all rows would overstate the
	// footprint. Count enabled rows for the headline and note declined rows
	// separately.
	nInjected, nToggledOff := 0, 0
	for _, r := range state.ReadPlaced(placed) {
		if r.Enabled == "0" {
			nToggledOff++
		} else {
			nInjected++
		}
	}

	// Interactive dispatch: on a real terminal, and only for the default page
	// (not --markdown, not the scriptable --plain), hand off to the live
	// interactive screen. It sits after the placed.tsv-absent early returns
	// above, so not-installed / pre-0.10 repos keep the plain page.
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

// toggledOffSuffix is " (k toggled off)" when k>0, else "" — appended after
// the injected count so a page whose consent state has diverged from the
// ledger says so.
func toggledOffSuffix(nToggledOff int) string {
	if nToggledOff > 0 {
		return fmt.Sprintf(" (%d toggled off)", nToggledOff)
	}
	return ""
}

// printStatusUsage prints the `omakase status` flag surface.
func printStatusUsage(w io.Writer) {
	fmt.Fprint(w, `usage: omakase status [--markdown|--plain|--global] [--disable NAME | --enable NAME]
                      [--keep PATH | --restore PATH]

  (no flags)      on a real terminal, open the interactive consent screen;
                  otherwise print the status page
  --markdown, -m  print the status page as markdown
  --plain         force the printed status page (never the interactive screen)
  --global        list the personal config the page's GLOBAL line counts
                  (~/.claude + ~/.copilot, applies to every repo)
  --disable NAME  turn a gate off, or remove a placed file/dir; NAME is a wired
                  gate name or a placed path. Recorded so commits/pushes skip it
                  until re-enabled.
  --enable NAME   undo a --disable: restore the file/dir or turn the gate back on
  --keep PATH     you edited a placed file/dir: accept the on-disk version as
                  yours (status goes green; see the change first: omakase diff)
  --restore PATH  put the harness's version of a placed file/dir back — undoes
                  a --keep or a local edit
  --help, -h      show this help
`)
}

// renderMarkdown prints the markdown status page in question-first order.
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
	RenderGuards(w, repo.OMK, true)
	fmt.Fprintln(w)
	RenderInventory(w, repo, home, true)
	fmt.Fprintln(w)
	fmt.Fprintln(w, "_Refresh:_ `omakase init`  ·  _Remove:_ `omakase remove`  ·  _read-only; running status changes nothing._")
}

// renderTerminal prints the default status page: an optional branded banner,
// then the same question-first order as markdown.
func renderTerminal(w io.Writer, repo *state.Repo, home, hname, srcdisp, basever string, nInjected, nToggledOff int) {
	// Banner: if present, run it at the invocation cwd, pass its stdout
	// through, discard stderr, and ignore failure.
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
	RenderGuards(w, repo.OMK, false)
	fmt.Fprintln(w)
	RenderInventory(w, repo, home, false)
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Restore the harness (replaces missing or changed files; removes dropped ones):   omakase init")
	fmt.Fprintln(w, "Undo everything:                                                                 omakase remove")
}

// harnessName derives the harness display name from the remembered source:
// "omakase-harness" unless a source is set, in which case strip from the
// first '#', then one trailing ".git", then one trailing "/", then take the
// last '/'-segment.
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

// srcDisplay is the source shown in the identity line and the injected rows'
// "from" annotations: the source minus a leading lowercase URL scheme and one
// trailing "/". For a github.com source with a `//` subpath split, the
// display form is the browsable web path (`/tree/HEAD/<subpath>` — HEAD
// resolves to the default branch) because terminals auto-linkify it and the
// canonical `//` form 404s on click (#131 gripe 2). The `//` string stays the
// identity everywhere internal — remembered source, cache keys, ledger rows.
func srcDisplay(src string) string {
	d := schemeRe.ReplaceAllString(src, "")
	d = strings.TrimSuffix(d, "/")
	if repo, sub, ok := strings.Cut(d, "//"); ok && strings.HasPrefix(repo, "github.com/") {
		ref := "HEAD"
		if s, r, pinned := strings.Cut(sub, "#"); pinned && r != "" {
			sub, ref = s, r
		}
		if sub != "" {
			d = strings.TrimSuffix(repo, ".git") + "/tree/" + ref + "/" + sub
		}
	}
	return d
}
