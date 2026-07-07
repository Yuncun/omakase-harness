// Command omakase is the v2 install-time binary: one static executable that
// replaces the bin/*.sh install-time machinery. Verbs are registered
// incrementally as their Go ports land; "status", "init", and "remove" are
// registered so far.
package main

import (
	"fmt"
	"io"
	"os"

	"github.com/Yuncun/omakase-harness/internal/mcpserver"
	"github.com/Yuncun/omakase-harness/internal/overlay"
	"github.com/Yuncun/omakase-harness/internal/status"
)

// Build metadata, injected at release time via -ldflags (GoReleaser sets
// main.version/commit/date; see .goreleaser.yaml). A plain `go build` leaves
// these defaults, so `omakase --version` on a dev build reports "dev".
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

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
		fmt.Fprintf(stdout, "omakase %s (commit %s, built %s)\n", version, commit, date)
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
