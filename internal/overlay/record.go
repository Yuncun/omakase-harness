// record.go implements the `omakase record <name>` plumbing verb: the
// out-of-band PASS for a deferred gate (the port of omakase-gate.sh --record).
// A deferred gate is `cacheable: true` plus a check that refuses; the real
// check runs elsewhere (an agent, a human, CI), and `omakase record` writes
// the PASS row for the current HEAD so the re-run at the same commit is
// allowed. It is the only signal an out-of-band check passed, so it fails LOUD
// on a write error.
package overlay

import (
	"fmt"
	"io"
	"os"

	"github.com/Yuncun/omakase-harness/internal/gate"
	"github.com/Yuncun/omakase-harness/internal/state"
)

// RunRecord is the `omakase record <name>` verb. argv is the arguments after
// the verb: exactly the gate name. It returns 2 for a usage error, 1 for a
// not-a-repo or a write failure, 0 on success.
func RunRecord(argv []string, stdout, stderr io.Writer) int {
	if len(argv) != 1 || argv[0] == "" {
		fmt.Fprintln(stderr, "usage: omakase record <gate-name>")
		return 2
	}
	name := argv[0]

	// A leaked GIT_DIR/GIT_WORK_TREE/GIT_COMMON_DIR would send the PASS row to
	// another repo — the same scrub the gate runner does. This gate's repo is
	// the one it runs in: resolve from cwd only.
	for _, v := range []string{"GIT_DIR", "GIT_WORK_TREE", "GIT_COMMON_DIR"} {
		os.Unsetenv(v)
	}

	wd, err := os.Getwd()
	var repo *state.Repo
	if err == nil {
		repo, err = state.Discover(wd)
	}
	if err != nil {
		fmt.Fprintln(stderr, "omakase: not inside a git repo")
		return 1
	}

	if err := gate.Record(repo.Root, repo.OMK, name); err != nil {
		fmt.Fprintf(stderr, "omakase: FAILED to record a PASS for '%s' (%v)\n", name, err)
		return 1
	}
	fmt.Fprintf(stdout, "omakase: recorded PASS for '%s' at HEAD\n", name)
	return 0
}
