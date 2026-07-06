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
)

// schemeRe strips a leading URL scheme (lowercase only) for srcDisplay
// (bin/status.sh:312, `sed -e 's,^[a-z][a-z]*://,,'`).
var schemeRe = regexp.MustCompile(`^[a-z][a-z]*://`)

// Run is the `omakase status` verb. argv is the arguments AFTER the verb; only
// argv[0] selects markdown mode (bin/status.sh:17). It discovers the repo from
// the current directory and writes the report to stdout. Not inside a git repo:
// the one stderr line + exit 1 (bin/status.sh:20). Exit 0 otherwise.
func Run(argv []string, stdout, stderr io.Writer) int {
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
	_ = plain // consumed by the interactive dispatch (Task 8)

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
	nplaced := state.CountNonEmptyLines(placed)

	if md {
		renderMarkdown(stdout, repo, home, icon, hname, srcdisp, basever, nplaced)
		return 0
	}
	renderTerminal(stdout, repo, home, hname, srcdisp, basever, nplaced)
	return 0
}

// renderMarkdown is the md page (bin/status.sh:320-335): the script owns the
// formatting so `omakase status` relays it verbatim. Question-first order.
func renderMarkdown(w io.Writer, repo *state.Repo, home, icon, hname, srcdisp, basever string, nplaced int) {
	fmt.Fprintf(w, "## %s %s\n", icon, hname)
	fmt.Fprintln(w)
	if srcdisp != "" {
		fmt.Fprintf(w, "`%s` · base omakase %s · installed in `%s`\n", srcdisp, basever, repo.Root)
	} else {
		fmt.Fprintf(w, "base omakase %s · installed in `%s`\n", basever, repo.Root)
	}
	fmt.Fprintln(w)
	fmt.Fprintf(w, "**Zero footprint** — %d file(s) injected, 0 committed; all gitignored via `.git/info/exclude` (invisible to git).\n", nplaced)
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
func renderTerminal(w io.Writer, repo *state.Repo, home, hname, srcdisp, basever string, nplaced int) {
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
	fmt.Fprintf(w, "zero footprint: %d injected, 0 committed, all gitignored (.git/info/exclude)\n", nplaced)
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
