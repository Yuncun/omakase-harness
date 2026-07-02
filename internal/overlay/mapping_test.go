package overlay

import "testing"

// TestMapLayerPath is the table test over every §7 row (docs/v2-design.md:130-146):
// payload file x layer -> dest rel path. Covers the one reroute (personal
// AGENTS.md), the two "as-is regardless of layer" rows (CLAUDE.md,
// .github/copilot-instructions.md), the base/project AGENTS.md as-is rows, the
// catch-all row, and — per the brief — a nested docs/AGENTS.md mapping as-is
// for EVERY layer (the table matches "AGENTS.md" as an exact repo-root-relative
// path, not a basename).
func TestMapLayerPath(t *testing.T) {
	tests := []struct {
		name  string
		layer LayerName
		rel   string
		want  string
	}{
		// AGENTS.md, per layer.
		{"project AGENTS.md placed as-is", LayerProject, "AGENTS.md", "AGENTS.md"},
		{"personal AGENTS.md rerouted to CLAUDE.local.md", LayerPersonal, "AGENTS.md", "CLAUDE.local.md"},
		{"base AGENTS.md placed as-is (no bridge — MapLayerPath doesn't know about bridging)", LayerBase, "AGENTS.md", "AGENTS.md"},

		// CLAUDE.md (shipped explicitly), any layer: as-is.
		{"project CLAUDE.md as-is", LayerProject, "CLAUDE.md", "CLAUDE.md"},
		{"personal CLAUDE.md as-is", LayerPersonal, "CLAUDE.md", "CLAUDE.md"},
		{"base CLAUDE.md as-is", LayerBase, "CLAUDE.md", "CLAUDE.md"},

		// .github/copilot-instructions.md, any layer: as-is.
		{"project copilot-instructions.md as-is", LayerProject, ".github/copilot-instructions.md", ".github/copilot-instructions.md"},
		{"personal copilot-instructions.md as-is", LayerPersonal, ".github/copilot-instructions.md", ".github/copilot-instructions.md"},
		{"base copilot-instructions.md as-is", LayerBase, ".github/copilot-instructions.md", ".github/copilot-instructions.md"},

		// Nested docs/AGENTS.md is NOT the root AGENTS.md row — as-is for every layer,
		// including personal (the reroute is an exact-path match, not a basename match).
		{"project nested docs/AGENTS.md as-is", LayerProject, "docs/AGENTS.md", "docs/AGENTS.md"},
		{"personal nested docs/AGENTS.md as-is (NOT rerouted)", LayerPersonal, "docs/AGENTS.md", "docs/AGENTS.md"},
		{"base nested docs/AGENTS.md as-is", LayerBase, "docs/AGENTS.md", "docs/AGENTS.md"},

		// Everything else, any layer: as-is (the catch-all row).
		{"project other file as-is", LayerProject, "lefthook.yml", "lefthook.yml"},
		{"personal other file as-is", LayerPersonal, "lefthook.yml", "lefthook.yml"},
		{"base other file as-is", LayerBase, ".claude/settings.json", ".claude/settings.json"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := MapLayerPath(tt.layer, tt.rel)
			if got != tt.want {
				t.Errorf("MapLayerPath(%q, %q) = %q, want %q", tt.layer, tt.rel, got, tt.want)
			}
		})
	}
}

// TestBridgeWanted_Positive is the one arm where every condition is satisfied:
// the project layer places a root AGENTS.md, no layer's post-mapping set
// contains CLAUDE.md, and the repo doesn't track a committed CLAUDE.md.
func TestBridgeWanted_Positive(t *testing.T) {
	sets := map[LayerName][]string{
		LayerProject: {"AGENTS.md", "lefthook.yml"},
	}
	if !BridgeWanted(LayerProject, sets, false) {
		t.Error("BridgeWanted = false, want true (all conditions satisfied)")
	}
}

// TestBridgeWanted_PersonalNeverBridges pins the negative arm the brief names
// explicitly: personal AGENTS.md never bridges. Even holding every OTHER
// condition in the qualifying shape (a project set that itself has AGENTS.md,
// no CLAUDE.md anywhere, untracked), asking BridgeWanted for the PERSONAL layer
// must return false — the bridge is a project-layer feature only.
func TestBridgeWanted_PersonalNeverBridges(t *testing.T) {
	sets := map[LayerName][]string{
		LayerProject: {"AGENTS.md"},
	}
	if BridgeWanted(LayerPersonal, sets, false) {
		t.Error("BridgeWanted(LayerPersonal, ...) = true, want false — bridge is project-layer only")
	}
}

// TestBridgeWanted_BaseNeverBridges pins the base-layer negative arm the brief
// names explicitly: base AGENTS.md maps as-is with no bridge.
func TestBridgeWanted_BaseNeverBridges(t *testing.T) {
	sets := map[LayerName][]string{
		LayerBase: {"AGENTS.md"},
	}
	if BridgeWanted(LayerBase, sets, false) {
		t.Error("BridgeWanted(LayerBase, ...) = true, want false — bridge is project-layer only")
	}
}

// TestBridgeWanted_ExplicitCLAUDEmdSuppresses covers the brief's "explicit
// CLAUDE.md in ANY layer suppresses the bridge" arm: a shipped CLAUDE.md
// anywhere in the stack already gives Claude Code root instructions, so the
// bridge (which would also land at CLAUDE.md) must not be wanted — regardless
// of which layer's post-mapping set holds it.
func TestBridgeWanted_ExplicitCLAUDEmdSuppresses(t *testing.T) {
	tests := []struct {
		name string
		sets map[LayerName][]string
	}{
		{
			"CLAUDE.md in the project set itself",
			map[LayerName][]string{LayerProject: {"AGENTS.md", "CLAUDE.md"}},
		},
		{
			"CLAUDE.md in the personal set",
			map[LayerName][]string{
				LayerProject:  {"AGENTS.md"},
				LayerPersonal: {"CLAUDE.md"},
			},
		},
		{
			"CLAUDE.md in the base set",
			map[LayerName][]string{
				LayerProject: {"AGENTS.md"},
				LayerBase:    {"CLAUDE.md"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if BridgeWanted(LayerProject, tt.sets, false) {
				t.Error("BridgeWanted = true, want false — a CLAUDE.md anywhere in the stack suppresses the bridge")
			}
		})
	}
}

// TestBridgeWanted_PersonalCLAUDElocalDoesNotSuppress is the regression
// catcher for the "CLAUDE.md anywhere suppresses" check in BridgeWanted: a
// personal-layer CLAUDE.local.md is a DIFFERENT filename than CLAUDE.md and
// must NOT trip that check. If the exact-match comparison in contains ever
// degrades to a substring/prefix check, CLAUDE.local.md would wrongly match
// "CLAUDE.md" and this test would catch it.
func TestBridgeWanted_PersonalCLAUDElocalDoesNotSuppress(t *testing.T) {
	sets := map[LayerName][]string{
		LayerProject:  {"AGENTS.md"},
		LayerPersonal: {"CLAUDE.local.md"},
	}
	if !BridgeWanted(LayerProject, sets, false) {
		t.Error("BridgeWanted = false, want true — CLAUDE.local.md in the personal set must not suppress the bridge")
	}
}

// TestBridgeWanted_TrackedCLAUDEmdSuppresses covers the brief's "tracked
// CLAUDE.md suppresses" arm: a team-committed CLAUDE.md the harness doesn't
// own also suppresses the bridge, even though no layer's post-mapping set
// mentions CLAUDE.md at all.
func TestBridgeWanted_TrackedCLAUDEmdSuppresses(t *testing.T) {
	sets := map[LayerName][]string{
		LayerProject: {"AGENTS.md"},
	}
	if BridgeWanted(LayerProject, sets, true) {
		t.Error("BridgeWanted = true, want false — a git-tracked CLAUDE.md suppresses the bridge")
	}
}

// TestBridgeWanted_NoRootAGENTSmd covers the case where the project layer
// doesn't place a root AGENTS.md at all (only a nested docs/AGENTS.md, or no
// AGENTS.md whatsoever) — the bridge has nothing to point at, so it must not
// be wanted.
func TestBridgeWanted_NoRootAGENTSmd(t *testing.T) {
	tests := []struct {
		name string
		sets map[LayerName][]string
	}{
		{"project set has no AGENTS.md at all", map[LayerName][]string{LayerProject: {"lefthook.yml"}}},
		{"project set has only a nested docs/AGENTS.md", map[LayerName][]string{LayerProject: {"docs/AGENTS.md"}}},
		{"project layer missing from the map entirely", map[LayerName][]string{LayerPersonal: {"CLAUDE.local.md"}}},
		{"nil map", nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if BridgeWanted(LayerProject, tt.sets, false) {
				t.Error("BridgeWanted = true, want false — no root AGENTS.md in the project set")
			}
		})
	}
}
