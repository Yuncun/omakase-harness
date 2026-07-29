package hook

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The dispatcher bytes are a contract: probe's hook proof and remove's
// delete guard both compare files against Dispatcher(name), so these tests
// pin the properties (not the exact prose) every dispatcher must keep.

func TestDispatcherShape(t *testing.T) {
	for _, name := range Names() {
		t.Run(name, func(t *testing.T) {
			d := string(Dispatcher(name))
			if !strings.HasPrefix(d, "#!/bin/sh\n") {
				t.Errorf("dispatcher must start with a sh shebang, got %q", d[:20])
			}
			if !strings.Contains(d, `exec "$OMK" hook `+name+` "$@"`) {
				t.Errorf("dispatcher must exec `omakase hook %s` forwarding args:\n%s", name, d)
			}
			if !strings.Contains(d, `${XDG_CACHE_HOME:-$HOME/.cache}/omakase/bin/current/omakase`) {
				t.Errorf("dispatcher must target the stable machine-wide binary copy:\n%s", d)
			}
			if !strings.HasSuffix(d, "\n") {
				t.Error("dispatcher must end with a newline")
			}
		})
	}
}

func TestDispatcherGateFailsClosed(t *testing.T) {
	for _, name := range []string{"pre-commit", "pre-push"} {
		d := string(Dispatcher(name))
		if !strings.Contains(d, "exit 1") {
			t.Errorf("%s: a gate dispatcher must fail closed when the binary is missing:\n%s", name, d)
		}
		if !strings.Contains(d, "omakase init") {
			t.Errorf("%s: the fail-closed message must carry the fix line:\n%s", name, d)
		}
	}
}

func TestDispatcherPostCheckoutFailsOpen(t *testing.T) {
	d := string(Dispatcher("post-checkout"))
	if !strings.Contains(d, `[ -x "$OMK" ] || exit 0`) {
		t.Errorf("post-checkout must exit 0 when the binary is missing (heal is best-effort):\n%s", d)
	}
	if strings.Contains(d, "exit 1") {
		t.Errorf("post-checkout must never fail the checkout:\n%s", d)
	}
}

// The dispatcher text must be identical across repos and versions — nothing
// repo-specific, nothing version-specific — so hooks stay write-once.
func TestDispatcherIsStable(t *testing.T) {
	for _, name := range Names() {
		if !bytes.Equal(Dispatcher(name), Dispatcher(name)) {
			t.Fatalf("%s: Dispatcher is not deterministic", name)
		}
	}
}

func TestIsGate(t *testing.T) {
	for name, want := range map[string]bool{
		"pre-commit": true, "pre-push": true, "post-checkout": false, "commit-msg": false,
	} {
		if got := IsGate(name); got != want {
			t.Errorf("IsGate(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestKnown(t *testing.T) {
	for _, name := range Names() {
		if !Known(name) {
			t.Errorf("Known(%q) = false, want true", name)
		}
	}
	for _, name := range []string{"", "commit-msg", "pre-commit.sample"} {
		if Known(name) {
			t.Errorf("Known(%q) = true, want false", name)
		}
	}
}

func TestWriteInstallsExecutableDispatcher(t *testing.T) {
	dir := t.TempDir()
	if err := Write(dir, "pre-commit"); err != nil {
		t.Fatalf("Write: %v", err)
	}
	dest := filepath.Join(dir, "pre-commit")
	info, err := os.Stat(dest)
	if err != nil {
		t.Fatalf("dest not written: %v", err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Errorf("mode = %v, want 0755", info.Mode().Perm())
	}
	if !Matches(dest, "pre-commit") {
		t.Error("written file does not match Dispatcher bytes")
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Errorf("temp residue left behind: %v", entries)
	}
}

func TestWriteOverwritesForeignHook(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, "pre-push")
	if err := os.WriteFile(dest, []byte("#!/bin/sh\n# lefthook stub\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := Write(dir, "pre-push"); err != nil {
		t.Fatalf("Write over existing: %v", err)
	}
	if !Matches(dest, "pre-push") {
		t.Error("existing hook not replaced by the dispatcher")
	}
}

func TestMatches(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, "pre-commit")
	if Matches(dest, "pre-commit") {
		t.Error("a missing file must not match")
	}
	if err := os.WriteFile(dest, Dispatcher("pre-commit"), 0o755); err != nil {
		t.Fatal(err)
	}
	if !Matches(dest, "pre-commit") {
		t.Error("byte-equal file must match")
	}
	if Matches(dest, "pre-push") {
		t.Error("a pre-commit dispatcher must not match the pre-push name")
	}
	if err := os.WriteFile(dest, append(Dispatcher("pre-commit"), '\n'), 0o755); err != nil {
		t.Fatal(err)
	}
	if Matches(dest, "pre-commit") {
		t.Error("a single appended byte must break the match")
	}
}

// The git-lfs note (#190) appears exactly on the hooks git-lfs stubs
// (pre-push, post-checkout among ours), so someone reading the hook file
// learns the stock stub was displaced, not disabled.
func TestDispatcherCarriesLFSNote(t *testing.T) {
	for _, name := range []string{"pre-push", "post-checkout"} {
		if !bytes.Contains(Dispatcher(name), []byte("git lfs "+name)) {
			t.Errorf("%s dispatcher missing the git-lfs note", name)
		}
	}
	if bytes.Contains(Dispatcher("pre-commit"), []byte("git lfs")) {
		t.Error("pre-commit dispatcher must not claim an LFS forward (git-lfs has no pre-commit stub)")
	}
}

// Hooks written by an older init (pre-#190 text, no LFS note) must keep
// reading as omakase's own — otherwise every binary upgrade shows "foreign
// hooks" until a re-init, and remove strands them. The legacy bytes are
// LITERALS (never derived from Dispatcher's helpers): a shared helper would
// shift both generations together on the next wording edit and orphan every
// 0.29.0-written hook with a green suite.
func TestLegacyDispatcherStillRecognized(t *testing.T) {
	for _, name := range Names() {
		old := legacyDispatchers[name]
		if len(old) == 0 {
			t.Fatalf("%s: no pinned legacy dispatcher bytes", name)
		}
		if bytes.Equal(old, Dispatcher(name)) && (name == "pre-push" || name == "post-checkout") {
			t.Errorf("%s: legacy and current dispatcher are identical — the legacy pin lost its point", name)
		}
		// The pin is exactly the 0.28.0 shape: current text minus the one
		// git-lfs line. Reconstruct that and require byte equality, so a
		// future edit to the CURRENT text cannot drag the pin along.
		var lines []string
		for _, l := range strings.Split(string(Dispatcher(name)), "\n") {
			if !strings.Contains(l, "git lfs") {
				lines = append(lines, l)
			}
		}
		if want := strings.Join(lines, "\n"); string(old) != want {
			t.Errorf("%s: legacy pin no longer matches the shipped 0.28.0 text\n got: %q\nwant: %q", name, old, want)
		}
		if !IsDispatcherBytes(old, name) {
			t.Errorf("%s: legacy dispatcher bytes not recognized", name)
		}
		dir := t.TempDir()
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, old, 0o755); err != nil {
			t.Fatal(err)
		}
		if !Matches(p, name) {
			t.Errorf("%s: Matches rejects a legacy dispatcher file", name)
		}
	}
}
