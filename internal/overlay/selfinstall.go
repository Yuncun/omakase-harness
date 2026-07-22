// Self-install of the machine-wide binary copy. The statusline
// wiring printed by init — and, since issue #98, the permanent .git/hooks
// dispatchers — must survive plugin updates and cache eviction, so they
// point at ONE stable path (hook.StableBinPath) instead of a
// version-numbered cache dir or a per-repo script; every real
// `omakase init` refreshes the copy there.
package overlay

import (
	"io"
	"os"
	"path/filepath"
	"strconv"

	"github.com/Yuncun/omakase-harness/internal/hook"
	"github.com/Yuncun/omakase-harness/internal/state"
)

// SelfInstallCurrent copies the running executable to hook.StableBinPath
// when the two differ (hash-compared, atomic rename). Best-effort in itself
// — no failure here may ever fail the verb that triggered it — but the copy
// is load-bearing once dispatchers exist: gate hooks fail closed without
// it, so RunInit verifies it after writing dispatchers and the probe's hook
// proof checks it on every status run. Called from main() and never from
// RunInit, so unit tests exercising RunInit cannot overwrite a developer's
// real cached binary with a test binary.
func SelfInstallCurrent() {
	dest := hook.StableBinPath()
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
