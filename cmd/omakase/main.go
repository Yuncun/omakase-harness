// Command omakase is the install-time binary: one static executable that
// dispatches the human verbs (init, status, diff, remove) and the plumbing
// verbs run by wired-up tools (hook, statusline, stop-notice, mcp).
package main

import (
	"fmt"
	"io"
	"os"
	"runtime/debug"
	"strings"

	"github.com/Yuncun/omakase-harness/internal/mcpserver"
	"github.com/Yuncun/omakase-harness/internal/overlay"
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
		return overlay.RunInit(argv[2:], stdout, stderr)
	},
	"remove": func(argv []string, stdout, stderr io.Writer) int {
		return overlay.RunRemove(argv[2:], stdout, stderr)
	},
	"diff": func(argv []string, stdout, stderr io.Writer) int {
		return overlay.RunDiff(argv[2:], stdout, stderr)
	},
	"mcp": func(argv []string, stdout, stderr io.Writer) int {
		return mcpserver.Run(argv[2:], stdout, stderr)
	},
	"hook": func(argv []string, stdout, stderr io.Writer) int {
		return overlay.RunHook(argv[2:], os.Stdin, stdout, stderr)
	},
	"statusline": func(argv []string, stdout, stderr io.Writer) int {
		return runStatusline(os.Stdin, stdout)
	},
	"stop-notice": func(argv []string, stdout, stderr io.Writer) int {
		return runStopNotice(argv[2:], os.Stdin, stdout, stderr)
	},
}

// usage is the two-tier command list (issue #98 Part 2; chezmoi's grouped
// --help is the precedent): the four human verbs first, then the plumbing
// verbs your tools call — grouped so a human scanning for what to type never
// wades through them.
const usage = `usage: omakase <command>

  init [source]    install or repair the harness in this repo (idempotent)
  status           what's installed here; per-item switches (--help for flags)
  diff [path…]     what you changed vs the harness (read-only)
  remove           undo everything omakase placed

commands used by your tools, not by you:
  hook <name>      run the git-hook logic (called by the hooks init installs)
  statusline       one-line status segment (wire into your status bar)
  stop-notice      end-of-turn notice (wire as a Claude or Copilot Stop hook)
  mcp              menu server (wire into your agent's MCP config)
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
		overlay.SelfInstallCurrent()
	}
	os.Exit(run(os.Args, os.Stdout, os.Stderr))
}
