// Package templates embeds the payload's gate primitive (omakase-gate.sh)
// and installs it atomically (the gate-script heal in overlay/toggle.go).
// It lives under internal/templates/files/ as a byte-identical duplicate of
// the payload original — go:embed cannot reference a path outside its own
// package directory, so they cannot be the same file on disk.
// TestEmbeddedGateMatchesPayload reads the original at test time and asserts
// equality; keep the two copies in lockstep by hand whenever the payload
// gate original changes.
package templates

import (
	"embed"
	"fmt"
	"os"
)

//go:embed files/omakase-gate.sh
var files embed.FS

// Install writes the embedded script `name` (e.g. "omakase-gate.sh") to
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
