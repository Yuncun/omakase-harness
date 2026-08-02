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
	"strings"

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

// wireHosts fills each present host's empty statusLine slot — and, on
// Claude Code, the SessionStart heal hook (#211: the plugin fold moved it
// off the plugin's hooks.json; #164 C5 is why it exists — git hooks only
// heal on git events, so "overlay wiped, new session opens" stayed
// silently broken). Copilot CLI keeps hooks in a separate mechanism
// (~/.copilot/hooks/*.json, different schema, unverified — #211), not in
// settings.json, so only the statusline is wired there. Reports how many
// files it wrote. verbose adds the occupied-slot teaching lines.
func wireHosts(stdout, stderr io.Writer, home string, verbose bool) int {
	cmd := stableWireBin(home) + " statusline"
	wired := 0
	if dirExists(filepath.Join(home, ".claude")) {
		wired += wireHost(stdout, stderr, "Claude Code",
			filepath.Join(home, ".claude", "settings.json"),
			map[string]any{"type": "command", "command": cmd, "padding": 0, "refreshInterval": 10}, true, verbose)
	}
	if dirExists(filepath.Join(home, ".copilot")) {
		// Copilot's status line is GA (the old STATUS_LINE feature flag is
		// gone from the 1.0.75 bundle — #164 C4): the block alone is enough.
		// Refresh is per-response there (no timer), so no refreshInterval.
		wired += wireHost(stdout, stderr, "Copilot CLI",
			filepath.Join(home, ".copilot", "settings.json"),
			map[string]any{"type": "command", "command": cmd}, false, verbose)
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

// wireHost wires one host's settings file in a single read-modify-write
// and reports 1 if it wrote: the statusLine block when the slot is empty
// (a configured bar is left untouched, with manual instructions when
// verbose), and — when heal — the SessionStart heal hook when absent. An
// unparseable settings file is refused loudly — never overwrite what we
// cannot read.
func wireHost(stdout, stderr io.Writer, host, path string, block map[string]any, heal, verbose bool) int {
	m := map[string]any{}
	raw, err := os.ReadFile(path)
	switch {
	case err == nil:
		if json.Unmarshal(raw, &m) != nil {
			fmt.Fprintf(stderr, "omakase: %s: not valid JSON — fix it first, nothing touched (%s)\n", path, host)
			return 0
		}
		// A file holding literal `null` unmarshals fine but leaves the
		// map nil — assigning into it would panic.
		if m == nil {
			m = map[string]any{}
		}
	case !os.IsNotExist(err):
		fmt.Fprintf(stderr, "omakase: cannot read %s: %v\n", path, err)
		return 0
	}

	var wrote []string
	if _, has := m["statusLine"]; has {
		if verbose {
			fmt.Fprintf(stdout, "%s already has a status line — left untouched.\n", host)
			fmt.Fprintf(stdout, "  To add the omakase segment to it, run `%s` from your bar and print its output.\n", block["command"])
		}
	} else {
		m["statusLine"] = block
		wrote = append(wrote, "statusLine")
	}
	if heal {
		switch added, err := addSessionStartHook(m, raw); {
		case err != nil:
			fmt.Fprintf(stderr, "omakase: %s: %v — heal hook not wired\n", path, err)
		case added:
			wrote = append(wrote, "session-start heal hook")
		}
	}
	if len(wrote) == 0 {
		return 0
	}

	if raw != nil {
		if err := os.WriteFile(path+".omakase-bak", raw, 0o600); err != nil {
			fmt.Fprintf(stderr, "omakase: could not back up %s: %v — nothing touched\n", path, err)
			return 0
		}
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
	what := strings.Join(wrote, " + ")
	if raw != nil {
		fmt.Fprintf(stdout, "%s wired: %s written to %s (backup: %s.omakase-bak)\n", host, what, path, path)
	} else {
		fmt.Fprintf(stdout, "%s wired: %s written to %s\n", host, what, path)
	}
	return 1
}

// healCmd is the SessionStart command init wires. It re-derives the
// stable machine copy from the environment on every session — the same
// expansion hook.StableBinPath and the dispatchers use — so it never goes
// stale when HOME or XDG_CACHE_HOME change, and it embeds no
// machine-specific text (a baked absolute path was both a staleness trap
// and an sh-quoting hazard). It self-guards on the binary existing, so a
// machine that later loses the install never surfaces host hook errors.
const healCmd = `b="${XDG_CACHE_HOME:-$HOME/.cache}/omakase/bin/current/omakase"; [ -x "$b" ] && "$b" hook session-start || true`

// addSessionStartHook merges the heal hook into m's hooks.SessionStart
// list unless the raw settings already mention it. A hooks/SessionStart
// value of JSON null is treated as absent, not refused — null must never
// wedge the wiring into a complain-forever loop. Reports whether it added
// anything; errs on a hooks shape it cannot safely extend.
func addSessionStartHook(m map[string]any, raw []byte) (bool, error) {
	if strings.Contains(string(raw), "hook session-start") {
		return false, nil
	}
	hooks, ok := m["hooks"].(map[string]any)
	if !ok {
		if v, has := m["hooks"]; has && v != nil {
			return false, fmt.Errorf("hooks is not an object")
		}
		hooks = map[string]any{}
	}
	list, ok := hooks["SessionStart"].([]any)
	if !ok && hooks["SessionStart"] != nil {
		return false, fmt.Errorf("hooks.SessionStart is not a list")
	}
	hooks["SessionStart"] = append(list, map[string]any{
		"hooks": []any{map[string]any{"type": "command", "command": healCmd}},
	})
	m["hooks"] = hooks
	return true, nil
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func fileMissing(path string) bool {
	_, err := os.Stat(path)
	return err != nil
}
