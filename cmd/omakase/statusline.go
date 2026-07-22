// The `omakase statusline` verb: probe the repo the host is sitting in and
// print the one-line status-bar segment. Hosts wire their status line to run
// this one machine-wide binary; the segment goes dark in repos the harness
// does not guard.
//
// Contract with the hosts: whatever happens, exit 0 — Claude Code blanks the
// segment on empty stdout, and a non-zero exit or a hang would degrade the
// whole bar. Stdin carries the host's session JSON (Claude Code and
// ccstatusline put the working directory at workspace.current_dir; other
// hosts may use cwd); any shape we can't read falls back to the process cwd,
// and a terminal stdin (a human running it by hand) is skipped entirely so
// the command never sits waiting for EOF.
package main

import (
	"encoding/json"
	"io"
	"os"

	"github.com/Yuncun/omakase-harness/internal/probe"
	"github.com/Yuncun/omakase-harness/internal/render"
)

func runStatusline(stdin io.Reader, stdout io.Writer) int {
	scrubGitEnv()
	cwd := cwdFromHostJSON(readHostJSON(stdin))
	if cwd == "" {
		var err error
		if cwd, err = os.Getwd(); err != nil {
			return 0
		}
	}
	st, err := probe.Collect(cwd)
	if err != nil {
		return 0 // not a repo: dark segment
	}
	line := render.Statusline(st, render.Opts{
		Color: os.Getenv("NO_COLOR") == "",
		Icon:  os.Getenv("OMAKASE_ICON"),
	})
	if line != "" {
		io.WriteString(stdout, line+"\n")
	}
	return 0
}

// scrubGitEnv drops repo-pinning git env vars a hosting session may have
// leaked (exported for ANOTHER repo, they would make every git call below
// judge the wrong repository) — same hygiene as the hook-time scripts.
func scrubGitEnv() {
	for _, v := range []string{"GIT_DIR", "GIT_WORK_TREE", "GIT_COMMON_DIR", "GIT_INDEX_FILE"} {
		os.Unsetenv(v)
	}
}

// readHostJSON reads the host's session JSON from stdin. A terminal stdin
// is not read at all; malformed or absent JSON degrades to nil.
func readHostJSON(stdin io.Reader) map[string]any {
	if f, ok := stdin.(*os.File); ok {
		if info, err := f.Stat(); err != nil || info.Mode()&os.ModeCharDevice != 0 {
			return nil
		}
	}
	b, err := io.ReadAll(io.LimitReader(stdin, 1<<20))
	if err != nil {
		return nil
	}
	var m map[string]any
	if json.Unmarshal(b, &m) != nil {
		return nil
	}
	return m
}

// cwdFromHostJSON digs the working directory out of the shapes the hosts
// send: workspace.current_dir / workspace.project_dir (Claude Code,
// ccstatusline), then a top-level cwd (Copilot CLI, other hosts).
func cwdFromHostJSON(m map[string]any) string {
	if m == nil {
		return ""
	}
	if ws, ok := m["workspace"].(map[string]any); ok {
		for _, k := range []string{"current_dir", "project_dir"} {
			if s, ok := ws[k].(string); ok && s != "" {
				return s
			}
		}
	}
	if s, ok := m["cwd"].(string); ok && s != "" {
		return s
	}
	return ""
}
