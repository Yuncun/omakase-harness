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
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

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
//
// version is the running build's resolved version. When it and the stable
// copy's version (asked via `--version`) both parse as x.y.z and the running
// binary is OLDER, the copy is left alone: every repo's dispatchers exec
// this one machine-wide path, so letting a stale entry point overwrite it
// would downgrade every repo's hooks at once — the #189 harm at machine
// scope. A dev build ("dev") or an unaskable stable copy installs as
// before.
func SelfInstallCurrent(version string) {
	dest := hook.StableBinPath()
	if dest == "" {
		return
	}
	if v, ok := parseVersion(strings.TrimPrefix(version, "v")); ok {
		if sv, ok := stableVersion(dest); ok && versionLess(v, sv) {
			return
		}
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

// stableVersion asks the stable copy for its version (`--version` prints
// "omakase X.Y.Z (commit …, built …)"). ok is false when the copy is
// missing, not runnable, or prints anything else — the caller then installs
// as before.
func stableVersion(dest string) ([3]int, bool) {
	var zero [3]int
	out, err := exec.Command(dest, "--version").Output()
	if err != nil {
		return zero, false
	}
	f := strings.Fields(string(out))
	if len(f) < 2 || f[0] != "omakase" {
		return zero, false
	}
	return parseVersion(strings.TrimPrefix(f[1], "v"))
}
