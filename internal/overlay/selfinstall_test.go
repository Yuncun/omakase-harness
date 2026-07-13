package overlay

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Yuncun/omakase-harness/internal/hook"
	"github.com/Yuncun/omakase-harness/internal/state"
)

func TestStableBinPathHonorsXDG(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", "/xdg-test")
	if got, want := hook.StableBinPath(), filepath.Join("/xdg-test", "omakase", "bin", "current", "omakase"); got != want {
		t.Fatalf("StableBinPath = %q, want %q", got, want)
	}
}

func TestSelfInstallCurrent(t *testing.T) {
	cache := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cache)
	dest := hook.StableBinPath()

	SelfInstallCurrent()
	info, err := os.Stat(dest)
	if err != nil {
		t.Fatalf("no binary installed at %s: %v", dest, err)
	}
	if info.Mode().Perm()&0o111 == 0 {
		t.Fatalf("installed binary is not executable: %o", info.Mode().Perm())
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if state.HashOf(dest) != state.HashOf(exe) {
		t.Fatal("installed copy differs from the running executable")
	}

	// Idempotent: an identical copy is left alone (same inode contents; the
	// mtime not advancing proves the skip).
	before := info.ModTime()
	SelfInstallCurrent()
	after, err := os.Stat(dest)
	if err != nil {
		t.Fatal(err)
	}
	if !after.ModTime().Equal(before) {
		t.Fatal("identical copy was rewritten, want skip")
	}

	// A stale copy is replaced.
	if err := os.WriteFile(dest, []byte("old version"), 0o755); err != nil {
		t.Fatal(err)
	}
	SelfInstallCurrent()
	if state.HashOf(dest) != state.HashOf(exe) {
		t.Fatal("stale copy was not replaced")
	}
}
