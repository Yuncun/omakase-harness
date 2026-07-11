// tty.go decides whether `omakase status` may take over the terminal with the
// interactive screen (internal/tui) or must fall back to the plain static page.
package status

import (
	"os"

	"github.com/charmbracelet/x/term"
)

// interactiveTerminal gates the TUI: both stdin and stdout must be real
// terminals (agents/CI pipe at least one), TERM must not be dumb, and the
// window must be wide enough (narrower falls back to the plain page). It checks
// the process's os.Stdin/os.Stdout — never status.Run's writers, which are
// buffers under test — so piped and captured runs always stay on the plain
// page. When the width check fails it returns false silently: the plain page is
// the fallback, and a stderr note would corrupt a piped capture.
//
// term is github.com/charmbracelet/x/term — the golang.org/x/term fork already
// pulled in by bubbletea, so no new dependency (IsTerminal/GetSize take a
// uintptr fd, which os.File.Fd() already returns).
func interactiveTerminal() bool {
	if os.Getenv("TERM") == "dumb" {
		return false
	}
	if !term.IsTerminal(os.Stdin.Fd()) || !term.IsTerminal(os.Stdout.Fd()) {
		return false
	}
	w, _, err := term.GetSize(os.Stdout.Fd())
	if err != nil || w < 60 {
		return false
	}
	return true
}
