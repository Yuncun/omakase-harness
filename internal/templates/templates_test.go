package templates

import (
	"os"
	"path/filepath"
	"testing"
)

var embeddedNames = []string{"ensure-present.sh", "verify-overlay.sh", "install-guards.sh"}

// TestEmbeddedMatchesBin checks that internal/templates/files/*.sh stay
// duplicates of bin/*.sh: go:embed cannot reach a path outside its own package
// directory, so they cannot be the same file on disk. This reads both at test
// time and asserts byte equality; keep the two copies in lockstep by hand
// whenever a bin/ original changes.
func TestEmbeddedMatchesBin(t *testing.T) {
	for _, name := range embeddedNames {
		t.Run(name, func(t *testing.T) {
			got, err := files.ReadFile("files/" + name)
			if err != nil {
				t.Fatal(err)
			}
			want, err := os.ReadFile(filepath.Join("..", "..", "bin", name))
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != string(want) {
				t.Errorf("internal/templates/files/%s has drifted from bin/%s -- keep them in lockstep", name, name)
			}
		})
	}
}

// TestEmbeddedGateMatchesPayload checks that
// internal/templates/files/omakase-gate.sh stays a duplicate of
// payload/.omakase/bin/omakase-gate.sh, not the bin/ trio (go:embed still
// cannot reach outside its own package directory). Keep the two copies in
// lockstep by hand whenever the payload original changes.
func TestEmbeddedGateMatchesPayload(t *testing.T) {
	got, err := files.ReadFile("files/omakase-gate.sh")
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile(filepath.Join("..", "..", "payload", ".omakase", "bin", "omakase-gate.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Errorf("internal/templates/files/omakase-gate.sh has drifted from payload/.omakase/bin/omakase-gate.sh -- keep them in lockstep")
	}
}

// TestInstallAtomic covers the temp+rename+chmod install path: the destination
// ends up with the embedded bytes, mode 0755, and no ".tmp" left behind.
func TestInstallAtomic(t *testing.T) {
	for _, name := range embeddedNames {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			dest := filepath.Join(dir, "sub", name)
			if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := Install(name, dest); err != nil {
				t.Fatalf("Install: %v", err)
			}

			info, err := os.Stat(dest)
			if err != nil {
				t.Fatalf("dest not written: %v", err)
			}
			if info.Mode().Perm() != 0o755 {
				t.Errorf("dest mode = %v, want 0755", info.Mode().Perm())
			}

			got, err := os.ReadFile(dest)
			if err != nil {
				t.Fatal(err)
			}
			want, err := files.ReadFile("files/" + name)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != string(want) {
				t.Errorf("installed content differs from the embedded bytes")
			}

			if _, err := os.Stat(dest + ".tmp"); !os.IsNotExist(err) {
				t.Errorf("temp file left behind (stat err = %v)", err)
			}
		})
	}
}

// TestInstallOverwritesExisting exercises re-install over an already-placed
// script (the re-run case hit on every `omakase init`).
func TestInstallOverwritesExisting(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, "ensure-present.sh")
	if err := os.WriteFile(dest, []byte("stale content"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Install("ensure-present.sh", dest); err != nil {
		t.Fatalf("Install: %v", err)
	}
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	want, err := files.ReadFile("files/ensure-present.sh")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Errorf("re-install did not overwrite stale content")
	}
}

// TestInstallUnknownName covers the "failed to install" message for a name with
// no matching embedded asset.
func TestInstallUnknownName(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, "dest.sh")
	err := Install("does-not-exist.sh", dest)
	want := "omakase: failed to install does-not-exist.sh -> " + dest
	if err == nil || err.Error() != want {
		t.Errorf("Install(unknown name) error = %v, want %q", err, want)
	}
	if _, statErr := os.Stat(dest); !os.IsNotExist(statErr) {
		t.Errorf("Install(unknown name) left a file behind at %s", dest)
	}
}

// TestInstallFailureLeavesNothingBehind: a destination directory that can't
// be written into fails cleanly -- no dest, no ".tmp" residue.
func TestInstallFailureLeavesNothingBehind(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: permission checks don't apply")
	}
	dir := t.TempDir()
	roDir := filepath.Join(dir, "ro")
	if err := os.MkdirAll(roDir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(roDir, 0o755) })
	dest := filepath.Join(roDir, "ensure-present.sh")

	err := Install("ensure-present.sh", dest)
	if err == nil {
		t.Fatal("Install into a read-only dir succeeded, want an error")
	}
	want := "omakase: failed to install ensure-present.sh -> " + dest
	if err.Error() != want {
		t.Errorf("Install error = %q, want %q", err.Error(), want)
	}
	if _, statErr := os.Stat(dest); !os.IsNotExist(statErr) {
		t.Errorf("Install left a file behind despite failing")
	}
	if _, statErr := os.Stat(dest + ".tmp"); !os.IsNotExist(statErr) {
		t.Errorf("Install left a temp file behind despite failing")
	}
}
