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
