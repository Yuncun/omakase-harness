package status

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Yuncun/omakase-harness/internal/state"
)

// installOne builds a minimal installed repo: an $OMK with a placed.tsv holding
// the given rows and each row's file on disk, then chdirs into it so runToggle's
// os.Getwd/state.Discover see the fixture. rows is the raw placed.tsv body.
func installOne(t *testing.T, rows string, files map[string]string) (string, *state.Repo) {
	t.Helper()
	dir := newGitRepo(t)
	repo, err := state.Discover(dir)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if err := os.MkdirAll(repo.OMK, 0o755); err != nil {
		t.Fatal(err)
	}
	writeOMK(t, repo.OMK, "placed.tsv", rows)
	for rel, content := range files {
		writeFile(t, dir, rel, content)
	}
	t.Chdir(dir)
	return dir, repo
}

// --disable/--enable on harness machinery (.omakase/, lefthook wiring) must
// refuse with exit 2 and delete nothing — the CLI surface classifies machinery
// the same way the TUI and MCP menu do, so `--disable .omakase` can no longer
// wipe the gate primitive and brick every commit.
func TestRunToggleRefusesMachinery(t *testing.T) {
	gateRel := ".omakase/bin/omakase-gate.sh"
	rows := gateRel + "\tgate\tacme\tdeadbeef\t1\n"
	dir, repo := installOne(t, rows, map[string]string{gateRel: "#!/bin/sh\nexit 0\n"})

	for _, name := range []string{".omakase", gateRel} {
		var stdout, stderr bytes.Buffer
		code := runToggle(true, name, &stdout, &stderr)
		if code != 2 {
			t.Errorf("--disable %s: exit = %d, want 2 (stderr=%q)", name, code, stderr.String())
		}
		if !strings.Contains(stderr.String(), "machinery") {
			t.Errorf("--disable %s: stderr = %q, want a 'machinery' explanation", name, stderr.String())
		}
	}
	// The gate primitive is still on disk, and no disabled-gates was written.
	if _, err := os.Stat(filepath.Join(dir, gateRel)); err != nil {
		t.Errorf("machinery file deleted despite refusal: %v", err)
	}
	if _, err := os.Stat(filepath.Join(repo.OMK, "disabled-gates")); !os.IsNotExist(err) {
		t.Errorf("disabled-gates written for a machinery target (err=%v)", err)
	}
}

// A --disable/--enable target that matches no placed path and no wired gate name
// (a typo) must refuse with exit 2 instead of writing a phantom gate entry with
// a false success.
func TestRunToggleRejectsUnknownName(t *testing.T) {
	rows := "AGENTS.md\tdoc\tacme\t" + sha256Hex("body\n") + "\t1\n"
	_, repo := installOne(t, rows, map[string]string{"AGENTS.md": "body\n"})
	// No lefthook wiring in this repo -> no wired gates -> a typo is unknown.
	lh := writeFakeLefthook(t, "")
	t.Setenv("LEFTHOOK_BIN", lh)

	var stdout, stderr bytes.Buffer
	code := runToggle(true, "AGENTS.mddd", &stdout, &stderr)
	if code != 2 {
		t.Fatalf("exit = %d, want 2 (stdout=%q stderr=%q)", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "unknown gate or placed path") {
		t.Errorf("stderr = %q, want the unknown-target message", stderr.String())
	}
	if _, err := os.Stat(filepath.Join(repo.OMK, "disabled-gates")); !os.IsNotExist(err) {
		t.Errorf("disabled-gates written for an unknown target (err=%v)", err)
	}
}

// A --disable target that is a lefthook-wired gate name is accepted and
// recorded; real gates must not be over-refused.
func TestRunToggleAcceptsWiredGate(t *testing.T) {
	_, repo := installOne(t, "AGENTS.md\tdoc\tacme\t"+sha256Hex("b\n")+"\t1\n", map[string]string{"AGENTS.md": "b\n"})
	lh := writeFakeLefthook(t, "pre-commit:\n  jobs:\n    - name: smoke\n      run: bash .omakase/bin/omakase-gate.sh smoke --step 'exit 9'\n")
	t.Setenv("LEFTHOOK_BIN", lh)

	var stdout, stderr bytes.Buffer
	code := runToggle(true, "smoke", &stdout, &stderr)
	if code != 0 {
		t.Fatalf("--disable smoke: exit = %d, want 0 (stderr=%q)", code, stderr.String())
	}
	b, err := os.ReadFile(filepath.Join(repo.OMK, "disabled-gates"))
	if err != nil || !strings.Contains(string(b), "smoke") {
		t.Errorf("disabled-gates missing 'smoke': content=%q err=%v", string(b), err)
	}
}

// status --help / -h prints a usage block on stdout and exits 0, instead of
// falling through to the page (or launching the TUI on a real terminal).
func TestStatusHelp(t *testing.T) {
	for _, flag := range []string{"--help", "-h"} {
		var stdout, stderr bytes.Buffer
		code := Run([]string{flag}, &stdout, &stderr)
		if code != 0 {
			t.Errorf("status %s: exit = %d, want 0", flag, code)
		}
		out := stdout.String()
		if !strings.HasPrefix(out, "usage: omakase status") {
			t.Errorf("status %s: stdout = %q, want a usage block", flag, out)
		}
		for _, want := range []string{"--markdown", "--plain", "--disable NAME", "--enable NAME", "interactive"} {
			if !strings.Contains(out, want) {
				t.Errorf("status %s usage missing %q:\n%s", flag, want, out)
			}
		}
	}
}
