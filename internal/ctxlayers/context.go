// This file is the `omakase context` verb entry point: flag parsing, repo
// discovery, host detection, and the --show drill-in that prints one layer in
// full. The scan lives in scan.go, the page in render.go.
package ctxlayers

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/Yuncun/omakase-harness/internal/state"
)

// Run is the `omakase context` verb. argv is the arguments after the verb.
// It reads the filesystem and writes a report; it changes nothing.
func Run(argv []string, stdout, stderr io.Writer) int {
	md, show := false, ""
	for i := 0; i < len(argv); i++ {
		switch a := argv[i]; a {
		case "--help", "-h":
			printUsage(stdout)
			return 0
		case "--markdown", "-m", "md":
			md = true
		case "--show":
			if i+1 >= len(argv) {
				fmt.Fprintln(stderr, "omakase: --show needs a path")
				return 2
			}
			i++
			show = argv[i]
		default:
			fmt.Fprintf(stderr, "omakase: unknown argument %q (see omakase context --help)\n", a)
			return 2
		}
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

	home := os.Getenv("HOME")
	host := DetectHost(os.Getenv)

	if show != "" {
		return runShow(repo, home, show, stdout, stderr)
	}

	entries := Scan(repo, home, host)
	if len(entries) == 0 {
		fmt.Fprintf(stdout, "No instruction layers found in %s.\n", repo.Root)
		fmt.Fprintln(stdout, "Nothing here steers an agent — no CLAUDE.md, AGENTS.md,")
		fmt.Fprintln(stdout, ".github/copilot-instructions.md, instruction rules, or skills.")
		return 0
	}
	Render(stdout, entries, filepath.Base(repo.Root), hostLabel(host), md)
	return 0
}

// runShow prints one layer in full. The overview deliberately quotes only a
// sentence per layer, so the drill-in has to exist or the elision is a lie —
// a page that hides text without offering a way to read it is worse than one
// that never quoted anything.
func runShow(repo *state.Repo, home, arg string, stdout, stderr io.Writer) int {
	path, matches := resolveShow(repo, home, arg)
	if path == "" {
		if len(matches) > 1 {
			fmt.Fprintf(stderr, "omakase: %q matches %d layers — be more specific:\n", arg, len(matches))
			for _, m := range matches {
				fmt.Fprintf(stderr, "    %s\n", m)
			}
			return 2
		}
		fmt.Fprintf(stderr, "omakase: no layer matching %q (run omakase context to list them)\n", arg)
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

// hostLabel is the human name for a detected host key.
func hostLabel(host string) string {
	switch host {
	case "copilot":
		return "GitHub Copilot CLI"
	case "claude":
		return "Claude Code"
	}
	return "host not detected — nothing marked inert"
}

// printUsage prints the `omakase context` flag surface.
func printUsage(w io.Writer) {
	fmt.Fprint(w, `usage: omakase context [--markdown] [--show PATH]

  (no flags)      show the instruction layers an agent loads in this repo,
                  what each one costs, and which are idle
  --markdown, -m  print the layers as a markdown table
  --show PATH     print one layer in full; PATH may be a repo-relative path
                  or any unique part of one (e.g. --show pr-discipline)
  --help, -h      show this help

Reach:
  LOADED      full text in the context window on every turn
  INDEXED     name and description load; the body does not
  ON DEMAND   loads when you touch the directory it governs
  ON TRIGGER  loads when what you ask matches its description
  INERT       present on disk, unread by the host you are running
`)
}
