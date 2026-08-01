// This file is the --show drill-in: resolving a user-typed layer name and
// printing that layer in full. The scan lives in scan.go, the page in
// render.go; the verb surface belongs to `omakase status`, which calls in.
package ctxlayers

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/Yuncun/omakase-harness/internal/state"
)

// RunShow prints one layer in full. The overview deliberately quotes only a
// sentence per layer, so the drill-in has to exist or the elision is a lie —
// a page that hides text without offering a way to read it is worse than one
// that never quoted anything.
func RunShow(repo *state.Repo, home, arg string, stdout, stderr io.Writer) int {
	path, matches := resolveShow(repo, home, arg)
	if path == "" {
		if len(matches) > 1 {
			fmt.Fprintf(stderr, "omakase: %q matches %d layers — be more specific:\n", arg, len(matches))
			for _, m := range matches {
				fmt.Fprintf(stderr, "    %s\n", m)
			}
			return 2
		}
		fmt.Fprintf(stderr, "omakase: no layer matching %q (run omakase status to list them)\n", arg)
		return 2
	}
	data, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintf(stderr, "omakase: cannot read %s: %v\n", path, err)
		return 1
	}
	fmt.Fprintf(stdout, "--- %s (%s tokens, estimated) ---\n\n", path, commas(len(data)/4))
	stdout.Write(data)
	if len(data) > 0 && data[len(data)-1] != '\n' {
		fmt.Fprintln(stdout)
	}
	return 0
}

// resolveShow turns a user-typed layer name into a readable path. It accepts
// an exact repo-relative path, a "~/" personal path, and a substring match
// against the scanned layers so `--show pr-discipline` works without typing
// the whole path.
//
// When a substring matches several layers it returns them all with an empty
// path, so the caller can say which ones rather than reporting "not found"
// for something that was found twice. Host detection is deliberately skipped
// here: you must still be able to read a layer that is inert on this host.
func resolveShow(repo *state.Repo, home, arg string) (string, []string) {
	if strings.HasPrefix(arg, "~/") && home != "" {
		if p := filepath.Join(home, arg[2:]); fileBytes(p) > 0 {
			return p, nil
		}
	}
	if p := filepath.Join(repo.Root, arg); fileBytes(p) > 0 {
		return p, nil
	}
	if fileBytes(arg) > 0 {
		return arg, nil
	}

	var paths, shown []string
	for _, e := range Scan(repo, home, "") {
		if e.Count != 1 || !strings.Contains(e.Display, arg) {
			continue
		}
		p := e.Display
		if strings.HasPrefix(p, "~/") {
			p = filepath.Join(home, p[2:])
		} else {
			p = filepath.Join(repo.Root, p)
		}
		paths = append(paths, p)
		shown = append(shown, e.Display)
	}
	if len(paths) == 1 {
		return paths[0], nil
	}
	return "", shown
}
