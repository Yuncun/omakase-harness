package main

import (
	"bytes"
	"testing"
)

// run(argv, stdout, stderr) is the pure dispatch function main() wraps with
// os.Exit. These tests pin the two behaviors this task must produce: the
// bare-invocation usage message and the unknown-command error — both with
// exit code 2. "status" is asserted to dispatch to the unknown-command path
// since it is not registered in this task (Task 4 adds it to verbs).

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
		{"status not yet registered", []string{"omakase", "status"}},
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
