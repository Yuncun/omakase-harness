// Command omakase is the v2 install-time binary: one static executable that
// replaces the bin/*.sh install-time machinery. Verbs are registered
// incrementally as their Go ports land; "status", "init", and "remove" are
// registered so far.
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

// Build metadata, injected at release time via -ldflags (GoReleaser sets
// main.version/commit/date; see .goreleaser.yaml). A plain `go build` leaves
// these defaults; resolveVersion then backfills what Go itself stamps into
// the binary (module version for `go install …@vX.Y.Z`, VCS revision for a
// build from a checkout), so a dev build still identifies itself.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

// resolveVersion returns the version/commit/date to print. ldflags-injected
// values win untouched; only the "dev" default consults bi. From bi: the main
// module's version when Go stamped a real one (`go install module@version` —
// "(devel)" is the unstamped placeholder), and the vcs.revision (short,
// "+dirty" when vcs.modified) / vcs.time settings a checkout build carries.
// Pure so tests can feed fake build info; bi may be nil (stripped binary).
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

// verbs maps a command name to its handler. The handler receives the FULL argv
// (argv[0]="omakase", argv[1]=the verb); each entry forwards the args AFTER the
// verb (argv[2:]) to its implementation.
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
	"mcp": func(argv []string, stdout, stderr io.Writer) int {
		return mcpserver.Run(argv[2:], stdout, stderr)
	},
}

// run is the pure dispatch function: no I/O beyond the given writers, no
// process exit. main() wraps it with os.Exit so it stays testable.
func run(argv []string, stdout, stderr io.Writer) int {
	if len(argv) < 2 {
		fmt.Fprintln(stderr, "usage: omakase <command>")
		return 2
	}

	// Exactly one spelling on purpose: "-v" stays free for a future verbose
	// flag, and a bare "version" word would shadow any future verb of that name.
	if argv[1] == "--version" {
		bi, _ := debug.ReadBuildInfo()
		v, c, d := resolveVersion(version, commit, date, bi)
		fmt.Fprintf(stdout, "omakase %s (commit %s, built %s)\n", v, c, d)
		return 0
	}

	cmd, ok := verbs[argv[1]]
	if !ok {
		fmt.Fprintf(stderr, "omakase: unknown command %q\n", argv[1])
		return 2
	}

	return cmd(argv, stdout, stderr)
}

func main() {
	os.Exit(run(os.Args, os.Stdout, os.Stderr))
}
