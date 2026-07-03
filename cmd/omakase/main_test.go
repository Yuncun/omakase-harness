package main

import (
	"bytes"
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
