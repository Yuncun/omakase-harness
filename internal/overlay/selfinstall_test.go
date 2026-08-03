package overlay

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/Yuncun/omakase-harness/internal/hook"
	"github.com/Yuncun/omakase-harness/internal/state"
)

func TestStableBinPathHonorsXDG(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", "/xdg-test")
	want := filepath.Join("/xdg-test", "omakase", "bin", "current", "omakase")
	if runtime.GOOS == "windows" {
		want += ".exe" // exec.Command cannot run an extensionless binary there
	}
	if got := hook.StableBinPath(); got != want {
		t.Fatalf("StableBinPath = %q, want %q", got, want)
	}
}

func TestSelfInstallCurrent(t *testing.T) {
	cache := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cache)
	dest := hook.StableBinPath()

	SelfInstallCurrent("dev")
	info, err := os.Stat(dest)
	if err != nil {
		t.Fatalf("no binary installed at %s: %v", dest, err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o111 == 0 {
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
	SelfInstallCurrent("dev")
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
	SelfInstallCurrent("dev")
	if state.HashOf(dest) != state.HashOf(exe) {
		t.Fatal("stale copy was not replaced")
	}
}

// A stale entry point must never replace a NEWER stable copy: every repo's
// dispatchers exec that one path, so the overwrite would downgrade every
// repo's hooks at once (#189 at machine scope). A dev build or an unaskable
// copy keeps the old always-install behavior.
func TestSelfInstallRefusesToDowngradeStableCopy(t *testing.T) {
	if runtime.GOOS == "windows" {
		// The fake stable copy is an sh script answering --version;
		// Windows cannot exec it, so the version ask degrades to
		// "unaskable" and the guard (platform-independent code,
		// exercised on Unix) never engages here.
		t.Skip("fixture needs an exec'able version-printing stable copy")
	}
	cache := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cache)
	dest := hook.StableBinPath()
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		t.Fatal(err)
	}

	newer := "#!/bin/sh\necho 'omakase 9.9.9 (commit x, built y)'\n"
	if err := os.WriteFile(dest, []byte(newer), 0o755); err != nil {
		t.Fatal(err)
	}

	SelfInstallCurrent("0.1.0") // older release build: must leave the copy alone
	if got, _ := os.ReadFile(dest); string(got) != newer {
		t.Fatal("an older release build overwrote a newer stable copy")
	}

	SelfInstallCurrent("10.0.0") // newer release build: replaces
	if got, _ := os.ReadFile(dest); string(got) == newer {
		t.Fatal("a newer release build did not replace the stable copy")
	}

	if err := os.WriteFile(dest, []byte(newer), 0o755); err != nil {
		t.Fatal(err)
	}
	SelfInstallCurrent("dev") // dev build: always installs (developer flow)
	if got, _ := os.ReadFile(dest); string(got) == newer {
		t.Fatal("a dev build did not refresh the stable copy")
	}

	// An unaskable copy (no --version output) installs as before.
	if err := os.WriteFile(dest, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	SelfInstallCurrent("0.1.0")
	if got, _ := os.ReadFile(dest); string(got) == "#!/bin/sh\nexit 0\n" {
		t.Fatal("an unaskable stable copy blocked the install")
	}
}
