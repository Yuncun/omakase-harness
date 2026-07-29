// Package hook owns the permanent dispatcher files omakase writes into the
// shared .git/hooks dir. Each dispatcher is a fixed sh file that execs the
// machine-wide binary copy (overlay.StableBinPath) with `hook <name>`;
// nothing at hook time ever rewrites these files (issue #98: write-once
// hooks, one writer — only `omakase init` and `omakase remove` touch them).
// Content is identical for every repo, branch, and harness — only the hook
// name varies — so a byte comparison identifies a dispatcher exactly; the
// probe's hook proof and remove's delete guard both key on Dispatcher's
// bytes.
package hook

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
)

// StableBinPath is the machine-wide binary location the dispatchers (and
// the statusline wiring) exec:
// ${XDG_CACHE_HOME:-$HOME/.cache}/omakase/bin/current/omakase. "" when no
// home directory can be resolved. It must mirror the sh interpolation baked
// into Dispatcher — the dispatcher evaluates its env at fire time, this
// function at probe/init time.
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

// Names lists the hooks omakase dispatches — the fixed set init writes,
// whatever the harness wires. pre-commit and pre-push are gate hooks (fail
// closed); post-checkout is the heal hook (best-effort by contract).
func Names() []string { return []string{"pre-commit", "pre-push", "post-checkout"} }

// IsGate reports whether name is a gate hook: one whose dispatcher and
// runner fail closed instead of best-effort.
func IsGate(name string) bool { return name == "pre-commit" || name == "pre-push" }

// Known reports whether name is one of the hooks omakase dispatches.
func Known(name string) bool {
	for _, n := range Names() {
		if n == name {
			return true
		}
	}
	return false
}

// Dispatcher returns the exact bytes of the hook file init writes for name.
// The text is deliberately version-free and repo-free so it never needs a
// rewrite: binary upgrades refresh the machine-wide copy it points at, not
// the hook file. `exec` preserves stdin (pre-push ref lines) and the exit
// code. A missing binary blocks a gate hook with a one-line fix and never
// fails a checkout.
func Dispatcher(name string) []byte {
	guard := `[ -x "$OMK" ] || exit 0`
	if IsGate(name) {
		guard = `[ -x "$OMK" ] || { echo "omakase: ` + name + ` blocked — the omakase binary is missing at $OMK. Fix: install omakase (github.com/Yuncun/omakase-harness) and run 'omakase init' in this repo. Bypass once: git ` +
			bypassVerb(name) + ` --no-verify." >&2; exit 1; }`
	}
	// The git-lfs note (#190): someone debugging LFS reads this file first,
	// and without the line concludes the stock git-lfs hook was clobbered.
	lfsNote := ""
	if name == "pre-push" || name == "post-checkout" {
		lfsNote = `# Also runs 'git lfs ` + name + `' when git-lfs is installed — a stock git-lfs hook here was displaced, not disabled.
`
	}
	return []byte(`#!/bin/sh
# omakase dispatcher — permanent. Written only by 'omakase init'; removed by 'omakase remove'.
` + lfsNote + `OMK="${XDG_CACHE_HOME:-$HOME/.cache}/omakase/bin/current/omakase"
` + guard + `
exec "$OMK" hook ` + name + ` "$@"
`)
}

// legacyDispatcher is the previous-generation dispatcher text for name (the
// pre-#190 form, without the git-lfs note). Hooks written by an older init
// keep this exact content until the next init rewrites them, and both the
// probe's hook proof and remove's delete guard must keep recognizing them as
// omakase's own — otherwise every binary upgrade reads as "foreign hooks"
// until a re-init.
func legacyDispatcher(name string) []byte {
	guard := `[ -x "$OMK" ] || exit 0`
	if IsGate(name) {
		guard = `[ -x "$OMK" ] || { echo "omakase: ` + name + ` blocked — the omakase binary is missing at $OMK. Fix: install omakase (github.com/Yuncun/omakase-harness) and run 'omakase init' in this repo. Bypass once: git ` +
			bypassVerb(name) + ` --no-verify." >&2; exit 1; }`
	}
	return []byte(`#!/bin/sh
# omakase dispatcher — permanent. Written only by 'omakase init'; removed by 'omakase remove'.
OMK="${XDG_CACHE_HOME:-$HOME/.cache}/omakase/bin/current/omakase"
` + guard + `
exec "$OMK" hook ` + name + ` "$@"
`)
}

// IsDispatcherBytes reports whether content is an omakase dispatcher for
// name — the current text or the previous generation's.
func IsDispatcherBytes(content []byte, name string) bool {
	return bytes.Equal(content, Dispatcher(name)) || bytes.Equal(content, legacyDispatcher(name))
}

// bypassVerb names the git verb whose --no-verify skips the hook, so the
// fix line reads as a runnable command.
func bypassVerb(name string) string {
	if name == "pre-push" {
		return "push"
	}
	return "commit"
}

// Write installs the dispatcher for name into hooksDir atomically: temp
// sibling, chmod 0755, rename over. On failure nothing is left behind.
func Write(hooksDir, name string) error {
	if err := os.MkdirAll(hooksDir, 0o755); err != nil {
		return err
	}
	dest := filepath.Join(hooksDir, name)
	tmp := dest + ".tmp." + strconv.Itoa(os.Getpid())
	if err := os.WriteFile(tmp, Dispatcher(name), 0o644); err != nil {
		return err
	}
	if err := os.Chmod(tmp, 0o755); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, dest); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// Matches reports whether the file at path is an omakase dispatcher for
// name (current or previous-generation text). A missing or unreadable file
// does not match.
func Matches(path, name string) bool {
	content, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	return IsDispatcherBytes(content, name)
}
