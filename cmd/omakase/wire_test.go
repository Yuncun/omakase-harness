package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// wireHome builds a fake HOME with the given host dirs and optional
// pre-existing settings content, and points HOME at it. XDG_CACHE_HOME is
// cleared so the wire target resolves under this HOME, not the developer's
// real cache dir.
func wireHome(t *testing.T, hosts map[string]string) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CACHE_HOME", "")
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

// The Copilot arm writes the statusLine block and nothing else — the old
// STATUS_LINE feature flag is gone from Copilot (GA since before 1.0.75,
// #164 C4), so --wire must not write dead config, and existing
// feature_flags content passes through untouched.
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
	if len(enabled) != 1 || enabled[0] != "SOMETHING" {
		t.Fatalf("feature_flags.enabled = %v, want the prior [SOMETHING] untouched (no STATUS_LINE write)", enabled)
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

// The wire block must point where the binary actually self-installs:
// hook.StableBinPath honors XDG_CACHE_HOME exactly as the dispatchers do.
// The old hardcoded ~/.cache went dark on XDG machines.
func TestWireHonorsXDGCacheHome(t *testing.T) {
	home := wireHome(t, map[string]string{".claude": ""})
	xdg := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", xdg)
	var out, errB bytes.Buffer
	runWire(&out, &errB)
	sl, ok := readJSON(t, filepath.Join(home, ".claude", "settings.json"))["statusLine"].(map[string]any)
	if !ok {
		t.Fatal("statusLine missing")
	}
	want := filepath.Join(xdg, "omakase", "bin", "current", "omakase") + " statusline"
	if sl["command"] != want {
		t.Fatalf("command = %v, want %q", sl["command"], want)
	}
}

// ---- wireAtInit: the wired-by-default half (#123 item 5) ----

// An empty slot is filled with the same block --wire writes, announced with
// the one wired line.
func TestInitWireFillsEmptySlot(t *testing.T) {
	home := wireHome(t, map[string]string{".claude": ""})
	var out, errB bytes.Buffer
	wireAtInit(&out, &errB)
	if _, ok := readJSON(t, filepath.Join(home, ".claude", "settings.json"))["statusLine"]; !ok {
		t.Fatal("statusLine missing after wireAtInit")
	}
	if !strings.Contains(out.String(), "statusLine written to") {
		t.Fatalf("wiring not announced: %q", out.String())
	}
	if errB.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", errB.String())
	}
}

// An occupied slot is the machine steady state: byte-identical file and
// total silence — the add-it-by-hand teaching stays on the --wire verb.
func TestInitWireSilentWhenBarExists(t *testing.T) {
	prior := `{"statusLine":{"type":"command","command":"npx -y ccstatusline@latest"}}`
	home := wireHome(t, map[string]string{".claude": prior})
	var out, errB bytes.Buffer
	wireAtInit(&out, &errB)
	b, _ := os.ReadFile(filepath.Join(home, ".claude", "settings.json"))
	if string(b) != prior {
		t.Fatalf("existing bar was touched:\n%s", b)
	}
	if out.Len() != 0 || errB.Len() != 0 {
		t.Fatalf("want total silence, got stdout=%q stderr=%q", out.String(), errB.String())
	}
}

// A machine with no host config dirs: silence, no writes.
func TestInitWireSilentWithNoHosts(t *testing.T) {
	wireHome(t, nil)
	var out, errB bytes.Buffer
	wireAtInit(&out, &errB)
	if out.Len() != 0 || errB.Len() != 0 {
		t.Fatalf("want total silence, got stdout=%q stderr=%q", out.String(), errB.String())
	}
}

// Broken settings JSON keeps the loud stderr refusal (never write what we
// cannot read) but stays a warning — the init itself is not failed.
func TestInitWireWarnsOnInvalidJSON(t *testing.T) {
	prior := `{broken`
	home := wireHome(t, map[string]string{".claude": prior})
	var out, errB bytes.Buffer
	wireAtInit(&out, &errB)
	b, _ := os.ReadFile(filepath.Join(home, ".claude", "settings.json"))
	if string(b) != prior {
		t.Fatalf("broken settings were touched: %q", b)
	}
	if !strings.Contains(errB.String(), "not valid JSON") {
		t.Fatalf("no loud refusal: %q", errB.String())
	}
}

// ---- the init verb end-to-end (dispatch-level gating) ----

// initFixtureRepo builds a committed git repo and chdirs in, points
// OMAKASE_PAYLOAD at a one-file payload, isolates HOME (with the given host
// dirs) and XDG_CACHE_HOME, and plants an executable stable-bin stub there
// so init's dispatcher-target check and closing verdict see a live exec
// target. Returns the fake HOME.
func initFixtureRepo(t *testing.T, hosts map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q"}, {"config", "user.email", "t@t"}, {"config", "user.name", "t"},
		{"config", "commit.gpgsign", "false"}, {"commit", "-q", "--allow-empty", "-m", "x"},
	} {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	t.Chdir(dir)
	payload := t.TempDir()
	if err := os.WriteFile(filepath.Join(payload, "STEER.md"), []byte("steer\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("OMAKASE_PAYLOAD", payload)
	home := wireHome(t, hosts)
	xdg := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", xdg)
	stub := filepath.Join(xdg, "omakase", "bin", "current", "omakase")
	if err := os.MkdirAll(filepath.Dir(stub), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stub, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return home
}

// A real `omakase init` wires the empty slot by default, and a re-init is
// the steady state: settings byte-identical, no second announcement.
func TestRunInitWiresStatuslineByDefault(t *testing.T) {
	home := initFixtureRepo(t, map[string]string{".claude": ""})
	var out, errB bytes.Buffer
	if code := run([]string{"omakase", "init"}, &out, &errB); code != 0 {
		t.Fatalf("init exit=%d stderr=%s", code, errB.String())
	}
	path := filepath.Join(home, ".claude", "settings.json")
	if _, ok := readJSON(t, path)["statusLine"]; !ok {
		t.Fatalf("init did not wire the statusline; stdout:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "statusLine written to") {
		t.Fatalf("wiring not announced in init output:\n%s", out.String())
	}
	first, _ := os.ReadFile(path)

	out.Reset()
	errB.Reset()
	if code := run([]string{"omakase", "init"}, &out, &errB); code != 0 {
		t.Fatalf("re-init exit=%d stderr=%s", code, errB.String())
	}
	second, _ := os.ReadFile(path)
	if !bytes.Equal(first, second) {
		t.Fatalf("re-init churned the settings:\n%s", second)
	}
	if strings.Contains(out.String(), "statusLine written to") {
		t.Fatalf("re-init announced wiring again:\n%s", out.String())
	}
}

// `init --help` has zero side effects: even in a wireable repo it must not
// touch host settings.
func TestRunInitHelpNeverWires(t *testing.T) {
	home := initFixtureRepo(t, map[string]string{".claude": ""})
	var out, errB bytes.Buffer
	if code := run([]string{"omakase", "init", "--help"}, &out, &errB); code != 0 {
		t.Fatalf("exit=%d stderr=%s", code, errB.String())
	}
	if _, err := os.Stat(filepath.Join(home, ".claude", "settings.json")); !os.IsNotExist(err) {
		t.Fatal("init --help wired the statusline")
	}
}

// The nothing-remembered bare init (the newcomer first-run) installs
// nothing and must wire nothing.
func TestRunInitNothingRememberedNeverWires(t *testing.T) {
	home := initFixtureRepo(t, map[string]string{".claude": ""})
	t.Setenv("OMAKASE_PAYLOAD", "") // empty = unset for RunInit's precedence
	var out, errB bytes.Buffer
	if code := run([]string{"omakase", "init"}, &out, &errB); code != 0 {
		t.Fatalf("exit=%d stderr=%s", code, errB.String())
	}
	if !strings.Contains(out.String(), "nothing to refresh") {
		t.Fatalf("expected the first-run pointer, got:\n%s", out.String())
	}
	if _, err := os.Stat(filepath.Join(home, ".claude", "settings.json")); !os.IsNotExist(err) {
		t.Fatal("a nothing-remembered init wired the statusline")
	}
}
