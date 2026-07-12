// Self-install of the machine-wide binary copy. The statusline / stop-notice
// wiring printed by init must survive plugin updates and cache eviction, so
// it points at ONE stable path instead of a version-numbered cache dir or a
// per-repo script; every real `omakase init` refreshes the copy there.
package overlay

import (
	"io"
	"os"
	"path/filepath"
	"strconv"

	"github.com/Yuncun/omakase-harness/internal/state"
)

// StableBinPath is the machine-wide binary location the host wiring points
// at: ${XDG_CACHE_HOME:-$HOME/.cache}/omakase/bin/current/omakase. "" when
// no home directory can be resolved.
func StableBinPath() string {
	cache := os.Getenv("XDG_CACHE_HOME")
	if cache == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		cache = filepath.Join(home, ".cache")
	}
	return filepath.Join(cache, "omakase", "bin", "current", "omakase")
}

// SelfInstallCurrent copies the running executable to StableBinPath when the
// two differ (hash-compared, atomic rename). Strictly best-effort: the copy
// only feeds the cosmetic status surfaces, so no failure here may ever fail
// the verb that triggered it. Called from main() and never from RunInit, so
// unit tests exercising RunInit cannot overwrite a developer's real cached
// binary with a test binary.
func SelfInstallCurrent() {
	dest := StableBinPath()
	if dest == "" {
		return
	}
	exe, err := os.Executable()
	if err != nil {
		return
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	if h := state.HashOf(exe); h == "" || h == state.HashOf(dest) {
		return
	}
	if os.MkdirAll(filepath.Dir(dest), 0o755) != nil {
		return
	}
	src, err := os.Open(exe)
	if err != nil {
		return
	}
	defer src.Close()
	tmp := dest + ".tmp." + strconv.Itoa(os.Getpid())
	out, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o755)
	if err != nil {
		return
	}
	if _, err := io.Copy(out, src); err != nil {
		out.Close()
		os.Remove(tmp)
		return
	}
	if out.Close() != nil || os.Rename(tmp, dest) != nil {
		os.Remove(tmp)
	}
}
