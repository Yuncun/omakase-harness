// Install of the user-level agent skills — the plugin fold (#211). The
// binary carries the skill files (basepayload.SkillsFS) and every
// `omakase init` refreshes them into the skill folders the hosts read:
// ~/.claude/skills (Claude Code) and ~/.copilot/skills (Copilot CLI),
// each only if its host config dir already exists. Best-effort like
// SelfInstallCurrent, and called from main() for the same reason: unit
// tests exercising RunInit must never write into a developer's real home.
package overlay

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	basepayload "github.com/Yuncun/omakase-harness"
)

// skillMarkerName identifies a skill directory as omakase-owned: only
// directories carrying it are ever overwritten, so a user's own skill that
// happens to share a name is never touched. Its content is the installing
// binary's version.
const skillMarkerName = ".omakase"

// InstallUserSkills refreshes the embedded skills under each present
// host's user-level skill folder. Version-aware like SelfInstallCurrent:
// when the marker of an installed skill and the running binary both parse
// as x.y.z and the running binary is OLDER, that skill is left alone — a
// stale entry point must not roll the front doors backwards (#189 at
// machine scope). A first-time install prints one stdout line (new slash
// commands appearing is a user-visible change; a refresh is silent).
// Nothing here may fail the verb that triggered it; problems print one
// stderr line and move on.
func InstallUserSkills(version string, stdout, stderr io.Writer) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return
	}
	var freshHosts []string
	for host, label := range map[string]string{".claude": "Claude Code", ".copilot": "Copilot CLI"} {
		if !isDir(filepath.Join(home, host)) {
			continue
		}
		if installSkillsInto(filepath.Join(home, host, "skills"), version, stderr) > 0 {
			freshHosts = append(freshHosts, label)
		}
	}
	if len(freshHosts) > 0 {
		sort.Strings(freshHosts)
		fmt.Fprintf(stdout, "omakase: agent skills installed for %s — /omakase-init, /omakase-status, /omakase-remove … (new sessions pick them up; every init keeps them current)\n",
			strings.Join(freshHosts, " and "))
	}
}

// installSkillsInto reports how many skills it installed FRESH (no prior
// marker) — refreshes of an existing install don't count.
func installSkillsInto(root, version string, stderr io.Writer) int {
	entries, err := fs.ReadDir(basepayload.SkillsFS, "skills")
	if err != nil {
		return 0
	}
	fresh := 0
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dest := filepath.Join(root, e.Name())
		hadMarker := fileRegular(filepath.Join(dest, skillMarkerName))
		if isDir(dest) && !hadMarker {
			fmt.Fprintf(stderr, "omakase: %s exists but is not omakase's — left untouched\n", dest)
			continue
		}
		if v, ok := parseVersion(strings.TrimPrefix(version, "v")); ok {
			if raw, err := os.ReadFile(filepath.Join(dest, skillMarkerName)); err == nil {
				if iv, ok := parseVersion(strings.TrimSpace(string(raw))); ok && versionLess(v, iv) {
					continue
				}
			}
		}
		if err := writeSkill(dest, e.Name(), version); err != nil {
			fmt.Fprintf(stderr, "omakase: could not install skill %s: %v\n", dest, err)
		} else if !hadMarker {
			fresh++
		}
	}
	return fresh
}

// writeSkill materializes one embedded skill directory, marker first: a
// directory without the marker reads as foreign and is never touched
// again, so a write torn after mkdir but before the marker would wedge
// that skill forever — marker-first keeps a torn write repairable by the
// next init.
func writeSkill(dest, name, version string) error {
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return err
	}
	if err := writeFileAtomic(filepath.Join(dest, skillMarkerName), []byte(version+"\n"), 0o644); err != nil {
		return err
	}
	src := "skills/" + name
	return fs.WalkDir(basepayload.SkillsFS, src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel := strings.TrimPrefix(path, src)
		rel = strings.TrimPrefix(rel, "/")
		if d.IsDir() {
			if rel == "" {
				return nil
			}
			return os.MkdirAll(filepath.Join(dest, rel), 0o755)
		}
		data, err := fs.ReadFile(basepayload.SkillsFS, path)
		if err != nil {
			return err
		}
		return writeFileAtomic(filepath.Join(dest, rel), data, 0o644)
	})
}

func writeFileAtomic(path string, data []byte, mode os.FileMode) error {
	tmp := path + ".tmp." + strconv.Itoa(os.Getpid())
	if err := os.WriteFile(tmp, data, mode); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}
