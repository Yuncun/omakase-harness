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
