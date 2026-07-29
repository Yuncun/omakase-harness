// Command omakase is the install-time binary: one static executable that
// dispatches the human verbs (init, status, diff, remove) and the plumbing
// verbs run by wired-up tools (hook, statusline).
package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"

	"github.com/Yuncun/omakase-harness/internal/block"
	"github.com/Yuncun/omakase-harness/internal/overlay"
	"github.com/Yuncun/omakase-harness/internal/state"
	"github.com/Yuncun/omakase-harness/internal/status"
)

// Build metadata, injected at release time via -ldflags (see
// .goreleaser.yaml). A plain `go build` leaves these defaults;
// resolveVersion backfills them from the build info Go stamps into the
// binary.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

// resolveVersion returns the version/commit/date to print. Injected values
// pass through; only the "dev" default consults bi, taking the main module
// version (ignoring the "(devel)" placeholder), the vcs.revision setting
// (12 chars, "+dirty" when vcs.modified), and vcs.time. bi may be nil.
func resolveVersion(v, c, d string, bi *debug.BuildInfo) (string, string, string) {
	if v != "dev" || bi == nil {
		return v, c, d
	}
	if mv := bi.Main.Version; mv != "" && mv != "(devel)" {
		v = strings.TrimPrefix(mv, "v")
	}
	var rev, t, modified string
	for _, s := range bi.Settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
		case "vcs.time":
			t = s.Value
		case "vcs.modified":
			modified = s.Value
		}
	}
	if rev != "" {
		if len(rev) > 12 {
			rev = rev[:12]
		}
		if modified == "true" {
			rev += "+dirty"
		}
		c = rev
	}
	if t != "" {
		d = t
	}
	return v, c, d
}

// verbs maps a command name to its handler. Handlers receive the full argv
// (argv[0]="omakase", argv[1]=the verb) and forward argv[2:] to their
// implementation.
var verbs = map[string]func(argv []string, stdout, stderr io.Writer) int{
	"status": func(argv []string, stdout, stderr io.Writer) int {
		return status.Run(argv[2:], stdout, stderr)
	},
	"init": func(argv []string, stdout, stderr io.Writer) int {
		code := overlay.RunInit(argv[2:], stdout, stderr)
		// Statusline wired by default (#123 item 5): after an init that
		// left a real install behind, fill each host's empty statusLine
		// slot; an existing bar always wins (wireAtInit). Lives here, not
		// in RunInit, for SelfInstallCurrent's reason: unit tests calling
		// RunInit directly must never touch a developer's real host
		// settings. --help and the nothing-remembered first run install
		// nothing and must wire nothing — the placed.tsv check is the
		// same installed-here signal RunInit's own wording keys on.
		if code == 0 && !hasHelpFlag(argv[2:]) && overlayInstalled() {
			wireAtInit(stdout, stderr)
		}
		return code
	},
	"remove": func(argv []string, stdout, stderr io.Writer) int {
		return overlay.RunRemove(argv[2:], stdout, stderr)
	},
	"diff": func(argv []string, stdout, stderr io.Writer) int {
		return overlay.RunDiff(argv[2:], stdout, stderr)
	},
	"block": func(argv []string, stdout, stderr io.Writer) int {
		return block.Run(false, argv[2:], stdout, stderr)
	},
	"unblock": func(argv []string, stdout, stderr io.Writer) int {
		return block.Run(true, argv[2:], stdout, stderr)
	},
	"hook": func(argv []string, stdout, stderr io.Writer) int {
		return overlay.RunHook(argv[2:], os.Stdin, stdout, stderr)
	},
	"record": func(argv []string, stdout, stderr io.Writer) int {
		return overlay.RunRecord(argv[2:], stdout, stderr)
	},
	"statusline": func(argv []string, stdout, stderr io.Writer) int {
		for _, a := range argv[2:] {
			if a == "--wire" {
				return runWire(stdout, stderr)
			}
		}
		return runStatusline(os.Stdin, stdout)
	},
}

// hasHelpFlag mirrors RunInit's parser: -h/--help anywhere in the args
// prints usage and installs nothing, so the wired-by-default step must
// treat it as a no-op run too.
func hasHelpFlag(args []string) bool {
	for _, a := range args {
		if a == "-h" || a == "--help" {
			return true
		}
	}
	return false
}

// overlayInstalled reports whether the working directory sits in a repo
// carrying a placed overlay (placed.tsv) — the installed-here signal the
// wired-by-default gate keys on.
func overlayInstalled() bool {
	wd, err := os.Getwd()
	if err != nil {
		return false
	}
	repo, err := state.Discover(wd)
	if err != nil {
		return false
	}
	_, err = os.Stat(filepath.Join(repo.OMK, "placed.tsv"))
	return err == nil
}

// usage is the two-tier command list (issue #98 Part 2; chezmoi's grouped
// --help is the precedent): the four human verbs first, then the plumbing
// verbs your tools call — grouped so a human scanning for what to type never
// wades through them.
const usage = `usage: omakase <command>

  init [source]    install or repair the harness in this repo (idempotent)
  status           what's installed here; per-item switches (--help for flags)
  diff [path…]     what you changed vs the harness (read-only)
  block <item>     hide one piece of the repo's OWN committed agent config
  unblock <item>   put a blocked item back
  remove           undo everything omakase placed

commands used by your tools, not by you:
  hook <name>      run the git-hook logic (called by the hooks init installs)
  record <name>    record a PASS for HEAD for a deferred gate (out-of-band)
  statusline       one-line status segment (init wires it into your hosts' bars; --wire re-wires)
`

// run dispatches argv to a verb and returns the exit code, writing only to
// the given writers.
func run(argv []string, stdout, stderr io.Writer) int {
	if len(argv) < 2 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	if argv[1] == "--help" || argv[1] == "-h" || argv[1] == "help" {
		fmt.Fprint(stdout, usage)
		return 0
	}

	// The only spelling: "-v" is reserved for a future verbose flag, and a
	// bare "version" would shadow a future verb of that name.
	if argv[1] == "--version" {
		bi, _ := debug.ReadBuildInfo()
		v, c, d := resolveVersion(version, commit, date, bi)
		fmt.Fprintf(stdout, "omakase %s (commit %s, built %s)\n", v, c, d)
		return 0
	}

	cmd, ok := verbs[argv[1]]
	if !ok {
		fmt.Fprintf(stderr, "omakase: unknown command %q (see omakase --help)\n", argv[1])
		return 2
	}

	return cmd(argv, stdout, stderr)
}

func main() {
	// Every real init refreshes the machine-wide binary copy the status-bar
	// wiring points at. Done here and not inside RunInit so unit tests that
	// call RunInit directly can never overwrite a developer's cached binary
	// with a test binary.
	if len(os.Args) > 1 && os.Args[1] == "init" {
		bi, _ := debug.ReadBuildInfo()
		v, _, _ := resolveVersion(version, commit, date, bi)
		overlay.SelfInstallCurrent(v)
	}
	os.Exit(run(os.Args, os.Stdout, os.Stderr))
}
