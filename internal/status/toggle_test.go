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
		for _, want := range []string{"--markdown", "--plain", "--disable NAME", "--enable NAME", "--keep PATH", "--restore PATH", "interactive"} {
			if !strings.Contains(out, want) {
				t.Errorf("status %s usage missing %q:\n%s", flag, want, out)
			}
		}
	}
}

// --keep then --restore round-trips an edited placed file: keep accepts the
// on-disk version (ledger hash moves, kept copy recorded), restore puts the
// snapshot version back and clears the kept mark.
func TestRunKeepRestoreLifecycle(t *testing.T) {
	orig := "guidance\n"
	rows := "AGENTS.md\tdoc\tacme\t" + sha256Hex(orig) + "\t1\n"
	dir, repo := installOne(t, rows, map[string]string{"AGENTS.md": "guidance\nmy edit\n"})
	if err := os.MkdirAll(filepath.Join(repo.OMK, "payload-snapshot"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeOMK(t, filepath.Join(repo.OMK, "payload-snapshot"), "AGENTS.md", orig)

	var stdout, stderr bytes.Buffer
	if code := runKeepRestore(true, "AGENTS.md", &stdout, &stderr); code != 0 {
		t.Fatalf("--keep: exit = %d, want 0 (stderr=%q)", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "kept AGENTS.md") {
		t.Errorf("--keep stdout = %q, want a 'kept' line", stdout.String())
	}
	if _, err := os.Stat(filepath.Join(repo.OMK, "kept", "AGENTS.md")); err != nil {
		t.Errorf("kept copy missing: %v", err)
	}
	got := state.ReadPlaced(filepath.Join(repo.OMK, "placed.tsv"))
	if got[0].Hash != sha256Hex("guidance\nmy edit\n") {
		t.Errorf("ledger hash = %s, want the accepted (edited) hash", got[0].Hash)
	}

	stdout.Reset()
	stderr.Reset()
	if code := runKeepRestore(false, "AGENTS.md", &stdout, &stderr); code != 0 {
		t.Fatalf("--restore: exit = %d, want 0 (stderr=%q)", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "restored AGENTS.md") {
		t.Errorf("--restore stdout = %q, want a 'restored' line", stdout.String())
	}
	b, err := os.ReadFile(filepath.Join(dir, "AGENTS.md"))
	if err != nil || string(b) != orig {
		t.Errorf("disk after restore = %q, want the harness version %q (err=%v)", b, orig, err)
	}
	if _, err := os.Stat(filepath.Join(repo.OMK, "kept", "AGENTS.md")); !os.IsNotExist(err) {
		t.Errorf("kept mark survived --restore (err=%v)", err)
	}
}

// --keep/--restore refuse machinery, tracked paths, and unknown names with
// exit 2, like --disable — and a name matching no placed path is never
// treated as a gate.
func TestRunKeepRestoreRefusals(t *testing.T) {
	gateRel := ".omakase/bin/omakase-gate.sh"
	rows := gateRel + "\tgate\tacme\tdeadbeef\t1\nAGENTS.md\tdoc\tacme\t" + sha256Hex("b\n") + "\t1\n"
	dir, _ := installOne(t, rows, map[string]string{gateRel: "#!/bin/sh\nexit 0\n", "AGENTS.md": "b\n"})

	for _, keep := range []bool{true, false} {
		var stdout, stderr bytes.Buffer
		if code := runKeepRestore(keep, ".omakase", &stdout, &stderr); code != 2 {
			t.Errorf("keep=%v machinery: exit = %d, want 2 (stderr=%q)", keep, code, stderr.String())
		}
		stderr.Reset()
		if code := runKeepRestore(keep, "nope.md", &stdout, &stderr); code != 2 {
			t.Errorf("keep=%v unknown: exit = %d, want 2", keep, code)
		} else if !strings.Contains(stderr.String(), "unknown placed path") {
			t.Errorf("keep=%v unknown: stderr = %q, want 'unknown placed path'", keep, stderr.String())
		}
	}

	runGitT(t, dir, "add", "-f", "AGENTS.md")
	runGitT(t, dir, "commit", "-q", "-m", "track it")
	for _, keep := range []bool{true, false} {
		var stdout, stderr bytes.Buffer
		if code := runKeepRestore(keep, "AGENTS.md", &stdout, &stderr); code != 2 {
			t.Errorf("keep=%v tracked: exit = %d, want 2 (stderr=%q)", keep, code, stderr.String())
		}
	}
}

// A group directory resolves to every placed child, exactly like --disable.
func TestRunKeepGroupDirectory(t *testing.T) {
	a, b := ".claude/rules/a.md", ".claude/rules/b.md"
	rows := a + "\trule\tacme\t" + sha256Hex("a\n") + "\t1\n" + b + "\trule\tacme\t" + sha256Hex("b\n") + "\t1\n"
	_, repo := installOne(t, rows, map[string]string{a: "a edited\n", b: "b edited\n"})

	var stdout, stderr bytes.Buffer
	if code := runKeepRestore(true, ".claude/rules", &stdout, &stderr); code != 0 {
		t.Fatalf("--keep group: exit = %d, want 0 (stderr=%q)", code, stderr.String())
	}
	for _, rel := range []string{a, b} {
		if _, err := os.Stat(filepath.Join(repo.OMK, "kept", rel)); err != nil {
			t.Errorf("kept copy missing for %s: %v", rel, err)
		}
	}
}

// On an uninstalled repo, --keep/--restore say "no harness installed" with
// exit 1 (the diff verb's contract), never a confusing "unknown placed path".
func TestRunKeepRestoreNotInstalled(t *testing.T) {
	dir := newGitRepo(t)
	t.Chdir(dir)
	for _, keep := range []bool{true, false} {
		var stdout, stderr bytes.Buffer
		if code := runKeepRestore(keep, "AGENTS.md", &stdout, &stderr); code != 1 {
			t.Errorf("keep=%v: exit = %d, want 1 (stderr=%q)", keep, code, stderr.String())
		}
		if !strings.Contains(stderr.String(), "no harness installed") {
			t.Errorf("keep=%v: stderr = %q, want the not-installed line", keep, stderr.String())
		}
	}
}
