package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// wireHome builds a fake HOME with the given host dirs and optional
// pre-existing settings content, and points HOME at it.
func wireHome(t *testing.T, hosts map[string]string) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	for host, settings := range hosts {
		dir := filepath.Join(home, host)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if settings != "" {
			if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(settings), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	return home
}

func readJSON(t *testing.T, path string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("parse %s: %v\n%s", path, err, b)
	}
	return m
}

// A host dir with no settings file gets one written: the statusLine block
// pointing at the machine-wide binary; no backup (nothing existed).
func TestWireFreshClaude(t *testing.T) {
	home := wireHome(t, map[string]string{".claude": ""})
	var out, errB bytes.Buffer
	if code := runWire(&out, &errB); code != 0 {
		t.Fatalf("exit = %d (stderr: %s)", code, errB.String())
	}
	path := filepath.Join(home, ".claude", "settings.json")
	m := readJSON(t, path)
	sl, ok := m["statusLine"].(map[string]any)
	if !ok {
		t.Fatalf("statusLine missing: %v", m)
	}
	wantCmd := filepath.Join(home, ".cache", "omakase", "bin", "current", "omakase") + " statusline"
	if sl["command"] != wantCmd || sl["type"] != "command" {
		t.Fatalf("statusLine block = %v, want command %q", sl, wantCmd)
	}
	if sl["refreshInterval"] != float64(10) {
		t.Fatalf("refreshInterval = %v, want 10", sl["refreshInterval"])
	}
	if _, err := os.Stat(path + ".omakase-bak"); !os.IsNotExist(err) {
		t.Fatal("no backup expected when no settings file existed")
	}
}

// Existing settings without a statusLine: wired, other keys preserved,
// backup written.
func TestWirePreservesExistingKeysAndBacksUp(t *testing.T) {
	prior := `{"model":"opus","hooks":{"Stop":[]}}`
	home := wireHome(t, map[string]string{".claude": prior})
	var out, errB bytes.Buffer
	runWire(&out, &errB)
	path := filepath.Join(home, ".claude", "settings.json")
	m := readJSON(t, path)
	if m["model"] != "opus" {
		t.Fatalf("existing key lost: %v", m)
	}
	if _, ok := m["statusLine"]; !ok {
		t.Fatalf("statusLine missing: %v", m)
	}
	bak, err := os.ReadFile(path + ".omakase-bak")
	if err != nil || string(bak) != prior {
		t.Fatalf("backup = %q, %v; want the prior bytes", bak, err)
	}
}

// A configured statusLine is never replaced — instructions instead.
func TestWireNeverClobbersAnExistingBar(t *testing.T) {
	prior := `{"statusLine":{"type":"command","command":"npx -y ccstatusline@latest"}}`
	home := wireHome(t, map[string]string{".claude": prior})
	var out, errB bytes.Buffer
	runWire(&out, &errB)
	path := filepath.Join(home, ".claude", "settings.json")
	b, _ := os.ReadFile(path)
	if string(b) != prior {
		t.Fatalf("existing bar was touched:\n%s", b)
	}
	if !strings.Contains(out.String(), "already has a status line") {
		t.Fatalf("no manual instructions printed: %q", out.String())
	}
}

// Unparseable settings are refused loudly and left byte-identical.
func TestWireRefusesInvalidJSON(t *testing.T) {
	prior := `{broken`
	home := wireHome(t, map[string]string{".claude": prior})
	var out, errB bytes.Buffer
	runWire(&out, &errB)
	b, _ := os.ReadFile(filepath.Join(home, ".claude", "settings.json"))
	if string(b) != prior {
		t.Fatalf("broken settings were touched: %q", b)
	}
	if !strings.Contains(errB.String(), "not valid JSON") {
		t.Fatalf("no loud refusal: %q", errB.String())
	}
}

// The Copilot arm writes the block AND turns on the STATUS_LINE feature
// flag (the block alone is inert while the feature is experimental),
// without duplicating a flag that is already on.
func TestWireCopilotArm(t *testing.T) {
	home := wireHome(t, map[string]string{".copilot": `{"feature_flags":{"enabled":["SOMETHING"]}}`})
	var out, errB bytes.Buffer
	runWire(&out, &errB)
	m := readJSON(t, filepath.Join(home, ".copilot", "settings.json"))
	if _, ok := m["statusLine"]; !ok {
		t.Fatalf("Copilot statusLine missing: %v", m)
	}
	sl := m["statusLine"].(map[string]any)
	if _, has := sl["refreshInterval"]; has {
		t.Fatalf("Copilot refreshes per-response; no refreshInterval expected: %v", sl)
	}
	enabled := m["feature_flags"].(map[string]any)["enabled"].([]any)
	if len(enabled) != 2 || enabled[0] != "SOMETHING" || enabled[1] != "STATUS_LINE" {
		t.Fatalf("feature_flags.enabled = %v", enabled)
	}
}

// A host whose config dir does not exist is skipped entirely — --wire
// creates no footprint for a host that is not on the machine.
func TestWireSkipsAbsentHosts(t *testing.T) {
	home := wireHome(t, map[string]string{".claude": ""})
	var out, errB bytes.Buffer
	runWire(&out, &errB)
	if _, err := os.Stat(filepath.Join(home, ".copilot")); !os.IsNotExist(err) {
		t.Fatal("--wire must not create ~/.copilot")
	}
}
