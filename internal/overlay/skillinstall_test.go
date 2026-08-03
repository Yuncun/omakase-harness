package overlay

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// skillHome isolates HOME with the given host config dirs present.
func skillHome(t *testing.T, hosts ...string) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	for _, h := range hosts {
		if err := os.MkdirAll(filepath.Join(home, h), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return home
}

// Both present hosts get the full renamed skill set, marker included; the
// SKILL.md content is the embedded one.
func TestInstallUserSkillsWritesBothHosts(t *testing.T) {
	home := skillHome(t, ".claude", ".copilot")
	InstallUserSkills("1.2.3", io.Discard, io.Discard)
	for _, host := range []string{".claude", ".copilot"} {
		for _, name := range []string{"omakase-init", "omakase-status", "omakase-remove", "omakase-author", "omakase-add-gate"} {
			dir := filepath.Join(home, host, "skills", name)
			b, err := os.ReadFile(filepath.Join(dir, "SKILL.md"))
			if err != nil {
				t.Fatalf("%s: %v", dir, err)
			}
			if !strings.Contains(string(b), "name: "+name) {
				t.Errorf("%s/SKILL.md: frontmatter name missing", dir)
			}
			marker, err := os.ReadFile(filepath.Join(dir, ".omakase"))
			if err != nil || strings.TrimSpace(string(marker)) != "1.2.3" {
				t.Errorf("%s/.omakase = %q, %v; want the installing version", dir, marker, err)
			}
		}
	}
}

// A host whose config dir does not exist is skipped entirely — no
// footprint for a host that is not on the machine.
func TestInstallUserSkillsSkipsAbsentHost(t *testing.T) {
	home := skillHome(t, ".claude")
	InstallUserSkills("1.2.3", io.Discard, io.Discard)
	if _, err := os.Stat(filepath.Join(home, ".copilot")); !os.IsNotExist(err) {
		t.Fatal("install must not create ~/.copilot")
	}
}

// A same-named directory without the omakase marker belongs to the user
// and is never touched — one stderr line says so.
func TestInstallUserSkillsLeavesForeignDirAlone(t *testing.T) {
	home := skillHome(t, ".claude")
	foreign := filepath.Join(home, ".claude", "skills", "omakase-init")
	if err := os.MkdirAll(foreign, 0o755); err != nil {
		t.Fatal(err)
	}
	prior := []byte("the user's own skill\n")
	if err := os.WriteFile(filepath.Join(foreign, "SKILL.md"), prior, 0o644); err != nil {
		t.Fatal(err)
	}
	var errB strings.Builder
	InstallUserSkills("1.2.3", io.Discard, &errB)
	b, _ := os.ReadFile(filepath.Join(foreign, "SKILL.md"))
	if string(b) != string(prior) {
		t.Fatalf("foreign skill dir was overwritten:\n%s", b)
	}
	if !strings.Contains(errB.String(), "not omakase's") {
		t.Fatalf("no foreign-dir note: %q", errB.String())
	}
	// The other skills still install — one squatter doesn't block the set.
	if _, err := os.Stat(filepath.Join(home, ".claude", "skills", "omakase-status", "SKILL.md")); err != nil {
		t.Fatalf("sibling skill not installed: %v", err)
	}
}

// An older binary must not roll newer installed skills backwards (#189 at
// machine scope); a newer one refreshes them.
func TestInstallUserSkillsNeverDowngrades(t *testing.T) {
	home := skillHome(t, ".claude")
	InstallUserSkills("9.9.9", io.Discard, io.Discard)
	path := filepath.Join(home, ".claude", "skills", "omakase-init", "SKILL.md")
	newer := []byte("---\nname: omakase-init\n---\nfrom the future\n")
	if err := os.WriteFile(path, newer, 0o644); err != nil {
		t.Fatal(err)
	}

	InstallUserSkills("1.0.0", io.Discard, io.Discard)
	if b, _ := os.ReadFile(path); string(b) != string(newer) {
		t.Fatalf("older binary downgraded a newer skill:\n%s", b)
	}

	InstallUserSkills("10.0.0", io.Discard, io.Discard)
	b, _ := os.ReadFile(path)
	if string(b) == string(newer) {
		t.Fatal("newer binary did not refresh the skill")
	}
	if marker, _ := os.ReadFile(filepath.Join(home, ".claude", "skills", "omakase-init", ".omakase")); strings.TrimSpace(string(marker)) != "10.0.0" {
		t.Fatalf("marker not updated: %q", marker)
	}
}

// A dev build installs unconditionally — the developer loop must never be
// blocked by a version comparison (SelfInstallCurrent's rule).
func TestInstallUserSkillsDevAlwaysInstalls(t *testing.T) {
	home := skillHome(t, ".claude")
	InstallUserSkills("9.9.9", io.Discard, io.Discard)
	path := filepath.Join(home, ".claude", "skills", "omakase-init", "SKILL.md")
	if err := os.WriteFile(path, []byte("stale\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	InstallUserSkills("dev", io.Discard, io.Discard)
	if b, _ := os.ReadFile(path); string(b) == "stale\n" {
		t.Fatal("dev build did not refresh the skill")
	}
}

// The first install announces itself once (new slash commands appearing is
// user-visible); the steady-state refresh is silent.
func TestInstallUserSkillsAnnouncesOnlyFirstInstall(t *testing.T) {
	skillHome(t, ".claude", ".copilot")
	var out strings.Builder
	InstallUserSkills("1.0.0", &out, io.Discard)
	if !strings.Contains(out.String(), "agent skills installed for Claude Code and Copilot CLI") ||
		!strings.Contains(out.String(), "/omakase-init") {
		t.Fatalf("first install not announced: %q", out.String())
	}
	var again strings.Builder
	InstallUserSkills("1.0.1", &again, io.Discard)
	if again.Len() != 0 {
		t.Fatalf("refresh must be silent, got %q", again.String())
	}
}

// The wedge case (review finding): an install killed between mkdir and the
// marker write leaves an empty marker-less dir. That dir must read as OUR
// torn write and get repaired, not refused as foreign forever.
func TestInstallUserSkillsRepairsTornWrite(t *testing.T) {
	home := skillHome(t, ".claude")
	torn := filepath.Join(home, ".claude", "skills", "omakase-init")
	if err := os.MkdirAll(torn, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(torn, ".omakase.tmp.123"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	var errB strings.Builder
	InstallUserSkills("1.0.0", io.Discard, &errB)
	if _, err := os.Stat(filepath.Join(torn, "SKILL.md")); err != nil {
		t.Fatalf("torn write not repaired: %v (stderr: %q)", err, errB.String())
	}
	if _, err := os.Stat(filepath.Join(torn, ".omakase.tmp.123")); !os.IsNotExist(err) {
		t.Fatal("tmp residue not swept")
	}
	if strings.Contains(errB.String(), "not omakase's") {
		t.Fatalf("torn write misread as foreign: %q", errB.String())
	}
}

// A dest that is a regular FILE (or any non-directory) is foreign: left
// untouched with the friendly refusal, not a raw mkdir error.
func TestInstallUserSkillsRefusesNonDirDest(t *testing.T) {
	home := skillHome(t, ".claude")
	if err := os.MkdirAll(filepath.Join(home, ".claude", "skills"), 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(home, ".claude", "skills", "omakase-init")
	if err := os.WriteFile(file, []byte("mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var errB strings.Builder
	InstallUserSkills("1.0.0", io.Discard, &errB)
	if b, _ := os.ReadFile(file); string(b) != "mine\n" {
		t.Fatalf("user's file was touched: %q", b)
	}
	if !strings.Contains(errB.String(), "not omakase's") {
		t.Fatalf("no friendly refusal for non-dir dest: %q", errB.String())
	}
}

// A v-prefixed build stamp must not disable the downgrade guard (review
// finding): the marker is written normalized, so a later older binary
// still refuses to roll it back.
func TestInstallUserSkillsNormalizesVPrefix(t *testing.T) {
	home := skillHome(t, ".claude")
	InstallUserSkills("v9.9.9", io.Discard, io.Discard)
	marker, _ := os.ReadFile(filepath.Join(home, ".claude", "skills", "omakase-init", ".omakase"))
	if strings.TrimSpace(string(marker)) != "9.9.9" {
		t.Fatalf("marker = %q, want the v-stripped version", marker)
	}
	path := filepath.Join(home, ".claude", "skills", "omakase-init", "SKILL.md")
	newer := []byte("from the future\n")
	if err := os.WriteFile(path, newer, 0o644); err != nil {
		t.Fatal(err)
	}
	InstallUserSkills("1.0.0", io.Discard, io.Discard)
	if b, _ := os.ReadFile(path); string(b) != string(newer) {
		t.Fatal("v-prefixed marker let an older binary downgrade the skill")
	}
}
