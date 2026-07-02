// Command omakase is the v2 install-time binary: one static executable that
// replaces the bin/*.sh install-time machinery. This task stands up the
// module and verb dispatch only; no verb is registered yet.
package main

import (
	"fmt"
	"io"
	"os"
)

// verbs maps a command name to its handler. Empty in this task; later tasks
// register verbs here (Task 4 adds "status").
var verbs = map[string]func(argv []string, stdout, stderr io.Writer) int{}

// run is the pure dispatch function: no I/O beyond the given writers, no
// process exit. main() wraps it with os.Exit so it stays testable.
func run(argv []string, stdout, stderr io.Writer) int {
	if len(argv) < 2 {
		fmt.Fprintln(stderr, "usage: omakase <command>")
		return 2
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
