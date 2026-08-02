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
	// One normalization for BOTH the marker content and the comparisons: a
	// v-prefixed build stamp written raw into the marker would fail to
	// parse later and silently disable the downgrade guard.
	version = strings.TrimPrefix(version, "v")
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
		if !hadMarker && !skillDestClaimable(dest) {
			fmt.Fprintf(stderr, "omakase: %s exists but is not omakase's — left untouched\n", dest)
			continue
		}
		if v, ok := parseVersion(version); ok {
			if raw, err := os.ReadFile(filepath.Join(dest, skillMarkerName)); err == nil {
				if iv, ok := parseVersion(strings.TrimSpace(string(raw))); ok && versionLess(v, iv) {
					continue
				}
			}
		}
		if err := writeSkill(dest, e.Name(), version); err != nil {
			// One line and stop for this whole folder: a root-level cause
			// (read-only dir, missing permissions) would otherwise repeat
			// per skill on every init.
			fmt.Fprintf(stderr, "omakase: could not install skill %s: %v\n", dest, err)
			return fresh
		}
		if !hadMarker {
			fresh++
		}
	}
	return fresh
}

// skillDestClaimable reports whether a marker-less dest may be written: it
// doesn't exist, or it is a real directory holding nothing but our own
// torn-write residue (the marker's or a file's .tmp.<pid> leftovers — the
// only states our own writer can abandon, since the marker lands before
// any content). Anything else — a user's file, symlink, or a directory
// with real content — is foreign and never touched. Without the residue
// tolerance, an install killed between mkdir and the marker write would
// read as foreign forever and permanently wedge that skill.
func skillDestClaimable(dest string) bool {
	fi, err := os.Lstat(dest)
	if err != nil {
		return os.IsNotExist(err)
	}
	if !fi.IsDir() {
		return false
	}
	entries, err := os.ReadDir(dest)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if !strings.Contains(e.Name(), ".tmp.") {
			return false
		}
	}
	return true
}

// writeSkill materializes one embedded skill directory, marker first.
// The ordering is load-bearing for skillDestClaimable's reasoning: with
// the marker landing before any content, the only marker-less states our
// own writer can abandon are an empty dir or .tmp leftovers — exactly
// what skillDestClaimable declares repairable. Real content without a
// marker therefore always means a foreign dir.
func writeSkill(dest, name, version string) error {
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return err
	}
	// Sweep our own .tmp.<pid> leftovers from a killed earlier write —
	// nothing else re-examines a dir once it carries the marker.
	if entries, err := os.ReadDir(dest); err == nil {
		for _, e := range entries {
			if strings.Contains(e.Name(), ".tmp.") {
				os.Remove(filepath.Join(dest, e.Name()))
			}
		}
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
