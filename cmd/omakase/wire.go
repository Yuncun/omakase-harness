// The `omakase statusline --wire` verb: connect the status-bar segment to
// the hosts' settings, with consent = running the command (#85). Per host
// (Claude Code ~/.claude, Copilot CLI ~/.copilot — each only if its config
// dir already exists): if no status line is configured, back the settings
// file up and write the block pointing at the machine-wide `current`
// binary; if one IS configured, print how to add the segment by hand and
// touch nothing. It never replaces an existing bar.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

func runWire(stdout, stderr io.Writer) int {
	home := os.Getenv("HOME")
	if home == "" {
		fmt.Fprintln(stderr, "omakase: HOME is not set — cannot find the hosts' settings")
		return 1
	}
	bin := filepath.Join(home, ".cache", "omakase", "bin", "current", "omakase")
	if _, err := os.Stat(bin); err != nil {
		fmt.Fprintf(stdout, "note: %s does not exist yet — the bar stays dark until an `omakase init` installs the machine-wide binary\n", bin)
	}
	cmd := bin + " statusline"

	wired := 0
	if dirExists(filepath.Join(home, ".claude")) {
		wired += wireHost(stdout, stderr, "Claude Code",
			filepath.Join(home, ".claude", "settings.json"),
			map[string]any{"type": "command", "command": cmd, "padding": 0, "refreshInterval": 10},
			nil)
	}
	if dirExists(filepath.Join(home, ".copilot")) {
		// Copilot's status line is experimental: the block alone is inert
		// until the STATUS_LINE feature flag is on, so --wire sets both.
		// Refresh is per-response there (no timer), so no refreshInterval.
		wired += wireHost(stdout, stderr, "Copilot CLI",
			filepath.Join(home, ".copilot", "settings.json"),
			map[string]any{"type": "command", "command": cmd},
			func(m map[string]any) { enableFeatureFlag(m, "STATUS_LINE") })
	}
	if wired == 0 {
		fmt.Fprintln(stdout, "nothing wired — no host was missing a status line (or no host config dir exists)")
	}
	return 0
}

// wireHost wires one host's settings file and reports 1 if it wrote. A
// configured statusLine is left untouched (manual instructions instead); an
// unparseable settings file is refused loudly — never overwrite what we
// cannot read. extra, when non-nil, mutates the settings map after the
// statusLine is set (the Copilot feature flag).
func wireHost(stdout, stderr io.Writer, host, path string, block map[string]any, extra func(map[string]any)) int {
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
		fmt.Fprintf(stdout, "%s already has a status line — left untouched.\n", host)
		fmt.Fprintf(stdout, "  To add the omakase segment to it, run `%s` from your bar and print its output.\n", block["command"])
		return 0
	}

	if raw != nil {
		if err := os.WriteFile(path+".omakase-bak", raw, 0o600); err != nil {
			fmt.Fprintf(stderr, "omakase: could not back up %s: %v — nothing touched\n", path, err)
			return 0
		}
	}
	m["statusLine"] = block
	if extra != nil {
		extra(m)
	}
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

// enableFeatureFlag appends name to feature_flags.enabled, creating the
// structure as needed and never duplicating.
func enableFeatureFlag(m map[string]any, name string) {
	ff, _ := m["feature_flags"].(map[string]any)
	if ff == nil {
		ff = map[string]any{}
	}
	enabled, _ := ff["enabled"].([]any)
	for _, v := range enabled {
		if v == name {
			m["feature_flags"] = ff
			return
		}
	}
	ff["enabled"] = append(enabled, name)
	m["feature_flags"] = ff
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}
