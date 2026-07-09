package main

import (
	"bytes"
	"runtime/debug"
	"testing"
)

// run(argv, stdout, stderr) is the pure dispatch function main() wraps with
// os.Exit. These tests pin the two behaviors dispatch must produce: the
// bare-invocation usage message and the unknown-command error — both with
// exit code 2. ("status" is now registered — Task 4 — so it no longer takes
// the unknown-command path; an unregistered verb name is used here instead.)

func TestRunNoArgs(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := run([]string{"omakase"}, &stdout, &stderr)

	if code != 2 {
		t.Errorf("exit code = %d, want 2", code)
	}
	if got := stdout.String(); got != "" {
		t.Errorf("stdout = %q, want empty", got)
	}
	if got, want := stderr.String(), "usage: omakase <command>\n"; got != want {
		t.Errorf("stderr = %q, want %q", got, want)
	}
}

// TestRunInitDispatch proves the registry wires "init" to overlay.RunInit:
// `omakase init --help` reaches RunInit's arg parser and returns its usage on
// stdout with exit 0 (never the unknown-command path).
func TestRunInitDispatch(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := run([]string{"omakase", "init", "--help"}, &stdout, &stderr)

	if code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}
	if got := stdout.String(); got == "" || got[:6] != "usage:" {
		t.Errorf("stdout = %q, want the init usage text", got)
	}
	if got := stderr.String(); got != "" {
		t.Errorf("stderr = %q, want empty", got)
	}
}

// TestRunRemoveDispatch proves the registry wires "remove" to
// overlay.RunRemove: run from OUTSIDE any git repo (a fresh t.TempDir), it
// must reach RunRemove's own repo-discovery failure ("not inside a git
// repo", exit 1) rather than the dispatcher's unknown-command path.
// remove.sh has no --help/usage text to probe (unlike init), so this is the
// simplest argv-independent proof that dispatch reaches the verb.
func TestRunRemoveDispatch(t *testing.T) {
	t.Chdir(t.TempDir())

	var stdout, stderr bytes.Buffer
	code := run([]string{"omakase", "remove"}, &stdout, &stderr)

	if code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	if got := stdout.String(); got != "" {
		t.Errorf("stdout = %q, want empty", got)
	}
	if got, want := stderr.String(), "omakase: not inside a git repo\n"; got != want {
		t.Errorf("stderr = %q, want %q", got, want)
	}
}

// TestRunMcpDispatch proves the registry wires "mcp" to mcpserver.Run: run from
// OUTSIDE any git repo (a fresh t.TempDir), it must reach mcpserver.Run's own
// repo-discovery failure ("not inside a git repo", exit 1) rather than the
// dispatcher's unknown-command path — mirrors TestRunRemoveDispatch, since mcp
// has no --help/usage text to probe either.
func TestRunMcpDispatch(t *testing.T) {
	t.Chdir(t.TempDir())

	var stdout, stderr bytes.Buffer
	code := run([]string{"omakase", "mcp"}, &stdout, &stderr)

	if code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	if got := stdout.String(); got != "" {
		t.Errorf("stdout = %q, want empty", got)
	}
	if got, want := stderr.String(), "omakase: not inside a git repo\n"; got != want {
		t.Errorf("stderr = %q, want %q", got, want)
	}
}

// TestPersonalVerbDeregistered proves Phase 3.5 removed the `personal` verb: it
// is no longer in the registry, so `omakase personal` falls through to the
// dispatcher's unknown-command path (exit 2), never a handler.
func TestPersonalVerbDeregistered(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"omakase", "personal"}, &stdout, &stderr)

	if code != 2 {
		t.Errorf("exit code = %d, want 2", code)
	}
	if got := stdout.String(); got != "" {
		t.Errorf("stdout = %q, want empty", got)
	}
	if got, want := stderr.String(), "omakase: unknown command \"personal\"\n"; got != want {
		t.Errorf("stderr = %q, want %q", got, want)
	}
}

// TestRunVersion pins the top-level version flag: `omakase --version` prints
// the build-metadata line to stdout and exits 0. Test binaries carry neither
// ldflags nor VCS/module stamping, so resolveVersion passes the defaults
// through and the line is exactly the dev string; release builds overwrite
// version/commit/date via .goreleaser.yaml's ldflags, and plain builds
// backfill from build info (TestResolveVersion covers both). `--version` is
// deliberately the ONLY spelling:
// "-v" and a bare "version" must keep taking the unknown-command path, so "-v"
// stays free for a future verbose flag and "version" never shadows a future
// verb — pinned below alongside the flag itself.
func TestRunVersion(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := run([]string{"omakase", "--version"}, &stdout, &stderr)

	if code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}
	if got, want := stdout.String(), "omakase dev (commit none, built unknown)\n"; got != want {
		t.Errorf("stdout = %q, want %q", got, want)
	}
	if got := stderr.String(); got != "" {
		t.Errorf("stderr = %q, want empty", got)
	}

	for _, arg := range []string{"-v", "version"} {
		t.Run(arg+" is not a version alias", func(t *testing.T) {
			var stdout, stderr bytes.Buffer

			code := run([]string{"omakase", arg}, &stdout, &stderr)

			if code != 2 {
				t.Errorf("exit code = %d, want 2 (unknown command)", code)
			}
			if got, want := stderr.String(), "omakase: unknown command \""+arg+"\"\n"; got != want {
				t.Errorf("stderr = %q, want %q", got, want)
			}
		})
	}
}

// TestResolveVersion pins the --version fallback for builds without ldflags:
// ldflags-injected values pass through untouched; a "dev" build backfills the
// go-install module version (leading "v" stripped, "(devel)" ignored) and the
// VCS revision (truncated to 12, "+dirty" on a modified tree) / time.
func TestResolveVersion(t *testing.T) {
	vcsBI := &debug.BuildInfo{
		Main: debug.Module{Version: "(devel)"},
		Settings: []debug.BuildSetting{
			{Key: "vcs.revision", Value: "0123456789abcdef0123"},
			{Key: "vcs.time", Value: "2026-07-08T00:00:00Z"},
			{Key: "vcs.modified", Value: "true"},
		},
	}
	cases := []struct {
		name       string
		v, c, d    string
		bi         *debug.BuildInfo
		wv, wc, wd string
	}{
		{"ldflags win untouched", "0.18.0", "abc1234", "2026-07-08", vcsBI,
			"0.18.0", "abc1234", "2026-07-08"},
		{"nil build info keeps defaults", "dev", "none", "unknown", nil,
			"dev", "none", "unknown"},
		{"go-install stamps module version", "dev", "none", "unknown",
			&debug.BuildInfo{Main: debug.Module{Version: "v0.18.0"}},
			"0.18.0", "none", "unknown"},
		{"checkout build backfills vcs revision+time, dirty marked", "dev", "none", "unknown", vcsBI,
			"dev", "0123456789ab+dirty", "2026-07-08T00:00:00Z"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gv, gc, gd := resolveVersion(tc.v, tc.c, tc.d, tc.bi)
			if gv != tc.wv || gc != tc.wc || gd != tc.wd {
				t.Errorf("resolveVersion = (%q, %q, %q), want (%q, %q, %q)", gv, gc, gd, tc.wv, tc.wc, tc.wd)
			}
		})
	}
}

func TestRunUnknownCommand(t *testing.T) {
	cases := []struct {
		name string
		argv []string
	}{
		{"nope", []string{"omakase", "nope"}},
		{"unregistered verb", []string{"omakase", "bogus"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer

			code := run(tc.argv, &stdout, &stderr)

			if code != 2 {
				t.Errorf("exit code = %d, want 2", code)
			}
			if got := stdout.String(); got != "" {
				t.Errorf("stdout = %q, want empty", got)
			}
			want := "omakase: unknown command \"" + tc.argv[1] + "\"\n"
			if got := stderr.String(); got != want {
				t.Errorf("stderr = %q, want %q", got, want)
			}
		})
	}
}
