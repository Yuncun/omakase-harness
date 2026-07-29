// The status-bar wiring: connect the segment to the hosts' settings. Per
// host (Claude Code ~/.claude, Copilot CLI ~/.copilot — each only if its
// config dir already exists): if no status line is configured, back the
// settings file up and write the block pointing at the machine-wide
// `current` binary; if one IS configured, touch nothing — an existing bar
// always wins. Two entry points share the writes and differ only in
// chatter: `omakase statusline --wire` (explicit, teaches the occupied-slot
// case) and wireAtInit (the wired-by-default half of #123 item 5, run by
// the init verb after a real install; consent = running init, #85).
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/Yuncun/omakase-harness/internal/hook"
)

func runWire(stdout, stderr io.Writer) int {
	home := os.Getenv("HOME")
	if home == "" {
		fmt.Fprintln(stderr, "omakase: HOME is not set — cannot find the hosts' settings")
		return 1
	}
	if bin := stableWireBin(home); fileMissing(bin) {
		fmt.Fprintf(stdout, "note: %s does not exist yet — the bar stays dark until an `omakase init` installs the machine-wide binary\n", bin)
	}
	if wireHosts(stdout, stderr, home, true) == 0 {
		fmt.Fprintln(stdout, "nothing wired — no host was missing a status line (or no host config dir exists)")
	}
	return 0
}

// wireAtInit fills empty slots after a real install. Quiet where --wire
// teaches: an occupied slot and a hostless machine print nothing, and there
// is no missing-binary note (init has just refreshed the machine copy).
// Real problems (unreadable JSON, failed writes) still print to stderr;
// they never fail the init.
func wireAtInit(stdout, stderr io.Writer) {
	home := os.Getenv("HOME")
	if home == "" {
		return
	}
	wireHosts(stdout, stderr, home, false)
}

// wireHosts fills each present host's empty statusLine slot and reports how
// many it wrote. verbose adds the occupied-slot teaching lines.
func wireHosts(stdout, stderr io.Writer, home string, verbose bool) int {
	cmd := stableWireBin(home) + " statusline"
	wired := 0
	if dirExists(filepath.Join(home, ".claude")) {
		wired += wireHost(stdout, stderr, "Claude Code",
			filepath.Join(home, ".claude", "settings.json"),
			map[string]any{"type": "command", "command": cmd, "padding": 0, "refreshInterval": 10}, verbose)
	}
	if dirExists(filepath.Join(home, ".copilot")) {
		// Copilot's status line is GA (the old STATUS_LINE feature flag is
		// gone from the 1.0.75 bundle — #164 C4): the block alone is enough.
		// Refresh is per-response there (no timer), so no refreshInterval.
		wired += wireHost(stdout, stderr, "Copilot CLI",
			filepath.Join(home, ".copilot", "settings.json"),
			map[string]any{"type": "command", "command": cmd}, verbose)
	}
	return wired
}

// stableWireBin is the command target the wire block points at: the
// machine-wide copy every real init refreshes. hook.StableBinPath honors
// XDG_CACHE_HOME exactly as the dispatchers do — a hardcoded ~/.cache went
// dark on XDG machines; home/.cache is the fallback when no home resolves.
func stableWireBin(home string) string {
	if p := hook.StableBinPath(); p != "" {
		return p
	}
	return filepath.Join(home, ".cache", "omakase", "bin", "current", "omakase")
}

// wireHost wires one host's settings file and reports 1 if it wrote. A
// configured statusLine is left untouched (with manual instructions when
// verbose); an unparseable settings file is refused loudly — never
// overwrite what we cannot read.
func wireHost(stdout, stderr io.Writer, host, path string, block map[string]any, verbose bool) int {
	m := map[string]any{}
	raw, err := os.ReadFile(path)
	switch {
	case err == nil:
		if json.Unmarshal(raw, &m) != nil {
			fmt.Fprintf(stderr, "omakase: %s: not valid JSON — fix it first, nothing touched (%s)\n", path, host)
			return 0
		}
	case !os.IsNotExist(err):
		fmt.Fprintf(stderr, "omakase: cannot read %s: %v\n", path, err)
		return 0
	}

	if _, has := m["statusLine"]; has {
		if verbose {
			fmt.Fprintf(stdout, "%s already has a status line — left untouched.\n", host)
			fmt.Fprintf(stdout, "  To add the omakase segment to it, run `%s` from your bar and print its output.\n", block["command"])
		}
		return 0
	}

	if raw != nil {
		if err := os.WriteFile(path+".omakase-bak", raw, 0o600); err != nil {
			fmt.Fprintf(stderr, "omakase: could not back up %s: %v — nothing touched\n", path, err)
			return 0
		}
	}
	m["statusLine"] = block
	out, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		fmt.Fprintf(stderr, "omakase: could not encode %s: %v\n", path, err)
		return 0
	}
	if err := os.WriteFile(path, append(out, '\n'), 0o600); err != nil {
		fmt.Fprintf(stderr, "omakase: could not write %s: %v\n", path, err)
		return 0
	}
	if raw != nil {
		fmt.Fprintf(stdout, "%s wired: statusLine written to %s (backup: %s.omakase-bak)\n", host, path, path)
	} else {
		fmt.Fprintf(stdout, "%s wired: statusLine written to %s\n", host, path)
	}
	return 1
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func fileMissing(path string) bool {
	_, err := os.Stat(path)
	return err != nil
}
