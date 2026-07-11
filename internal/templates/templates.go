// Package templates embeds the three hook-time sh scripts (ensure-present.sh,
// verify-overlay.sh, install-guards.sh) plus the payload's gate primitive
// (omakase-gate.sh) and installs them atomically. They live under
// internal/templates/files/ as byte-identical duplicates of their bin/ (or
// payload/) originals — go:embed cannot reference a path outside its own
// package directory, so they cannot be the same file on disk.
// TestEmbeddedMatchesBin and TestEmbeddedGateMatchesPayload read each original
// at test time and assert equality; keep every copy in lockstep by hand
// whenever a bin/ or payload gate original changes.
package templates

import (
	"embed"
	"fmt"
	"os"
)

//go:embed files/ensure-present.sh files/verify-overlay.sh files/install-guards.sh files/omakase-gate.sh
var files embed.FS

// Install writes the embedded script `name` (e.g. "ensure-present.sh") to
// dest atomically: embedded bytes -> dest+".tmp" -> chmod 0755 -> rename. On
// any failure the ".tmp" is removed and an error is returned; the caller owns
// the stderr stream and exit code.
func Install(name, dest string) error {
	failure := fmt.Errorf("omakase: failed to install %s -> %s", name, dest)

	data, err := files.ReadFile("files/" + name)
	if err != nil {
		return failure
	}

	tmp := dest + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return failure
	}
	if err := os.Chmod(tmp, 0o755); err != nil {
		os.Remove(tmp)
		return failure
	}
	if err := os.Rename(tmp, dest); err != nil {
		os.Remove(tmp)
		return failure
	}
	return nil
}
