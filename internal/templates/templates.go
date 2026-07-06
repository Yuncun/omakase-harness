// Package templates embeds the three hook-time sh scripts
// (bin/ensure-present.sh, bin/verify-overlay.sh, bin/install-guards.sh) plus
// the payload's gate primitive (payload/.omakase/bin/omakase-gate.sh), and
// installs them atomically. These live under internal/templates/files/ as
// BYTE-IDENTICAL DUPLICATES of their bin/ (or payload/) originals -- go:embed
// cannot reference a path outside its own package directory, so they cannot
// be the SAME file on disk. TestEmbeddedMatchesBin and
// TestEmbeddedGateMatchesPayload (templates_test.go) read each original at
// test time and assert equality; keep every copy in lockstep by hand
// whenever a bin/ or payload gate original changes. bin/ensure-present.sh,
// bin/verify-overlay.sh, and bin/install-guards.sh themselves are untouched
// this phase (Global Constraint 5).
package templates

import (
	"embed"
	"fmt"
	"os"
)

//go:embed files/ensure-present.sh files/verify-overlay.sh files/install-guards.sh files/omakase-gate.sh
var files embed.FS

// Install writes the embedded script `name` (e.g. "ensure-present.sh") to
// dest, atomically: embedded bytes -> dest+".tmp" -> chmod 0755 -> rename.
// The Go twin of install_script (bin/init.sh:450-453,
// `cp "$SCRIPT_DIR/$1" "$2.tmp" && chmod +x "$2.tmp" && mv -f "$2.tmp" "$2"`).
// On any failure the ".tmp" is removed and the error's message is exactly
// bash's own failure line, `omakase: failed to install %s -> %s`
// (bin/init.sh:452) -- bash prints this to stderr and exits 1 itself; here
// that decision belongs to the caller (it owns the stdout/stderr streams
// and exit code), so Install only returns the error.
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
