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
	"path/filepath"
	"regexp"
	"strings"

	"github.com/Yuncun/omakase-harness/internal/ctxlayers"
	"github.com/Yuncun/omakase-harness/internal/gate"
	"github.com/Yuncun/omakase-harness/internal/state"
)

// schemeRe strips a leading lowercase URL scheme for srcDisplay.
var schemeRe = regexp.MustCompile(`^[a-z][a-z]*://`)

// Run is the `omakase status` verb. argv is the arguments after the verb;
// only argv[0] selects markdown mode. It discovers the repo from the current
// directory and writes the report to stdout. Outside a git repo it writes one
// stderr line and exits 1; otherwise it exits 0.
func Run(argv []string, stdout, stderr io.Writer) int {
	// --help/-h short-circuits to usage — scanned first.
	// Any other unrecognized dash-flag is an error: a typo like
	// `--enabel smoke` must not exit 0 with the page (an automation would read
	// that as "re-enabled, green"). Bare words keep falling through; `md` is a
	// real alias.
	known := map[string]bool{
		"--markdown": true, "-m": true, "--plain": true,
		"--disable": true, "--enable": true,
		"--keep": true, "--restore": true,
		"--global": true, "--all": true, "--show": true,
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

	// A stray bare word (usually a path: `omakase status ~/proj`) must not be
	// silently dropped — status would report on the CWD while looking like it
	// answered for the argument (#164's host-agnostic finding). Value-taking
	// flags consume the word after them; `md` is the one bare-word alias.
	valueFlags := map[string]bool{"--disable": true, "--enable": true, "--keep": true, "--restore": true, "--show": true}
	for i := 0; i < len(argv); i++ {
		if valueFlags[argv[i]] {
			i++ // the flag's name/path value, validated by its handler
			continue
		}
		if a := argv[i]; !strings.HasPrefix(a, "-") && a != "md" {
			fmt.Fprintf(stderr, "omakase: unexpected argument %q — status reports on the current directory; cd there first (see omakase status --help)\n", a)
			return 2
		}
	}

	md, all := false, false
	for _, a := range argv {
		switch a {
		case "--markdown", "-m", "md":
			md = true
		case "--all":
			all = true
		}
	}

	// --plain is kept as an accepted no-op for script compatibility: the plain
	// page has been the only page since the interactive screen was removed
	// (#156). --disable/--enable toggle consent; --keep/--restore are their
	// edit-lifecycle siblings (issue #98 Part 2).
	for i := 0; i < len(argv); i++ {
		switch argv[i] {
		case "--plain":
			// accepted, no effect — the plain page is the default
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
			// config lives under the home dir, not the repo, so this needs no
			// repo discovery and works uninstalled too. os.UserHomeDir (not
			// $HOME) so native Windows — where HOME is unset — resolves too.
			home, _ := os.UserHomeDir()
			RenderGlobal(stdout, home, md)
			return 0
		case "--show":
			// The drill-in behind the layers section's one-sentence excerpts:
			// print any layer in full, by path or unique fragment.
			if i+1 >= len(argv) {
				fmt.Fprintln(stderr, "omakase: --show needs a path (or a unique part of one)")
				return 2
			}
			return runShowLayer(argv[i+1], stdout, stderr)
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

	home, _ := os.UserHomeDir()
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

	if md {
		renderMarkdown(stdout, repo, home, icon, hname, srcdisp, basever, nInjected, nToggledOff, all)
		return 0
	}
	renderTerminal(stdout, repo, home, hname, srcdisp, basever, nInjected, nToggledOff, all)
	return 0
}

// runShowLayer resolves and prints one layer in full (`omakase status --show`).
func runShowLayer(arg string, stdout, stderr io.Writer) int {
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
	home, _ := os.UserHomeDir()
	return ctxlayers.RunShow(repo, home, arg, stdout, stderr)
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
	fmt.Fprint(w, `usage: omakase status [--markdown|--plain|--global|--all] [--show PATH]
                      [--disable NAME | --enable NAME] [--keep PATH | --restore PATH]

  (no flags)      print the status page: the guards chart, then what an agent
                  loads here (each layer's reach, cost, and first words)
  --markdown, -m  print the status page as markdown
  --plain         same as no flags (kept for scripts)
  --all           the full file inventory — every placed row, healthy or not
  --show PATH     print one layer in full; PATH may be a repo-relative path or
                  any unique part of one (e.g. --show team)
  --global        list the personal config that applies to every repo
                  (~/.claude + ~/.copilot + ~/.agents)
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

// renderMarkdown prints the markdown status page. all=false is the default
// minimalist page: facts line, steering stack, guards, the loaded-every-turn
// table, then only the injected files that need attention. all=true is the
// full audit page — identity, footprint, guards, and the per-file inventory.
func renderMarkdown(w io.Writer, repo *state.Repo, home, icon, hname, srcdisp, basever string, nInjected, nToggledOff int, all bool) {
	fmt.Fprintf(w, "## %s %s\n", icon, hname)
	fmt.Fprintln(w)
	if all {
		if srcdisp != "" {
			fmt.Fprintf(w, "`%s` · base omakase %s · installed in `%s`\n", srcdisp, basever, repo.Root)
		} else {
			fmt.Fprintf(w, "base omakase %s · installed in `%s`\n", basever, repo.Root)
		}
		fmt.Fprintln(w)
		fmt.Fprintf(w, "**Zero footprint** — %d file(s) injected%s, 0 committed; all gitignored via `.git/info/exclude` (invisible to git).\n", nInjected, toggledOffSuffix(nToggledOff))
		fmt.Fprintln(w)
		fmt.Fprintln(w, "### Guards")
		fmt.Fprintln(w)
		RenderGuards(w, repo.OMK, true)
		fmt.Fprintln(w)
		RenderInventory(w, repo, home, true)
		fmt.Fprintln(w)
		fmt.Fprintln(w, "_Refresh:_ `omakase init`  ·  _Remove:_ `omakase remove`  ·  _read-only; running status changes nothing._")
		return
	}
	fmt.Fprintln(w, factsLine(nInjected, nToggledOff))
	fmt.Fprintln(w)
	entries := ctxlayers.Scan(repo, home, ctxlayers.DetectHost(os.Getenv))
	if len(entries) > 0 {
		ctxlayers.RenderStack(w, entries, true)
		fmt.Fprintln(w)
	}
	fmt.Fprintln(w, "### Guards")
	fmt.Fprintln(w)
	RenderGuards(w, repo.OMK, true)
	if len(entries) > 0 {
		fmt.Fprintln(w)
		ctxlayers.Render(w, entries, ctxlayers.DetectHost(os.Getenv), true)
	}
	RenderProblems(w, repo, true)
	fmt.Fprintln(w)
	fmt.Fprintln(w, "_`omakase status --all` · `--show <path>`_")
}

// renderTerminal prints the status page: the branded banner box (built in
// since #172; see banner.go), then the same order as markdown.
func renderTerminal(w io.Writer, repo *state.Repo, home, hname, srcdisp, basever string, nInjected, nToggledOff int, all bool) {
	fmt.Fprint(w, statusBanner(hname))

	if all {
		if srcdisp != "" {
			fmt.Fprintf(w, "%s — %s · base omakase %s · installed in %s\n", hname, srcdisp, basever, repo.Root)
		} else {
			fmt.Fprintf(w, "installed in %s (base omakase %s)\n", repo.Root, basever)
		}
		fmt.Fprintf(w, "zero footprint: %d injected%s, 0 committed, all gitignored (.git/info/exclude)\n", nInjected, toggledOffSuffix(nToggledOff))
		fmt.Fprintln(w)
		fmt.Fprintln(w, "GUARDS")
		RenderGuards(w, repo.OMK, false)
		fmt.Fprintln(w)
		RenderInventory(w, repo, home, false)
		fmt.Fprintln(w)
		fmt.Fprintln(w, "Restore the harness (replaces missing or changed files; removes dropped ones):   omakase init")
		fmt.Fprintln(w, "Undo everything:                                                                 omakase remove")
		return
	}

	fmt.Fprintln(w, factsLine(nInjected, nToggledOff))
	fmt.Fprintln(w)
	entries := ctxlayers.Scan(repo, home, ctxlayers.DetectHost(os.Getenv))
	if len(entries) > 0 {
		ctxlayers.RenderStack(w, entries, false)
		fmt.Fprintln(w)
	}
	fmt.Fprintln(w, "GUARDS")
	RenderGuards(w, repo.OMK, false)
	if len(entries) > 0 {
		fmt.Fprintln(w)
		ctxlayers.Render(w, entries, ctxlayers.DetectHost(os.Getenv), false)
	}
	RenderProblems(w, repo, false)
	fmt.Fprintln(w)
	fmt.Fprintln(w, "omakase status --all · --show <path>")
}

// factsLine is the default page's one line of numbers under the banner: the
// footprint facts, no label. The words "zero footprint" and the exclusion
// mechanism belong to the --all audit page.
func factsLine(nInjected, nToggledOff int) string {
	files := "files"
	if nInjected == 1 {
		files = "file"
	}
	return fmt.Sprintf("%d %s injected%s · 0 committed · invisible to git", nInjected, files, toggledOffSuffix(nToggledOff))
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
