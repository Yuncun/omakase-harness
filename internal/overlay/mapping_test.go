package overlay

import "testing"

// TestMapInstruction is the table test over the role-free slot-fallback
// routing (Phase 3.5 §7 rewrite; task-2-brief.md): payload rel x rootSlotFree
// -> (dest, fellBack). The outer table is the brief's four ROOT STATES a
// caller (init.go, Task 3) can observe — root free, a committed root
// CLAUDE.md, a committed root AGENTS.md, or a lower layer already owning the
// root instruction slot — each documenting WHY a caller passes what it
// passes, even though the pure function itself only ever sees the collapsed
// rootSlotFree bool (three of the four states collapse to false: any one of
// them already means "no free root slot", so MapInstruction cannot and need
// not distinguish among them). The inner table is every rel shape: the
// canonical "AGENTS.md" (the only rel this function ever reroutes), an
// explicit "CLAUDE.md" (passes through regardless of slot state — v1
// semantics, downstream committed-target skip unaffected), a nested
// docs/AGENTS.md (exact-path match, not basename — proves the reroute never
// fires here even when the root slot is taken), .github/copilot-instructions.md,
// and an arbitrary other file (the catch-all).
func TestMapInstruction(t *testing.T) {
	rootStates := []struct {
		name         string
		rootSlotFree bool
	}{
		{"root free (nothing at root AGENTS.md/CLAUDE.md, no lower layer owns the slot)", true},
		{"repo commits a root CLAUDE.md", false},
		{"repo commits a root AGENTS.md", false},
		{"a lower layer already owns the root instruction slot", false},
	}

	tests := []struct {
		name string
		rel  string
		// wantDestFree/wantDestTaken: dest at rootSlotFree=true / false.
		// fellBackTaken: fellBack at rootSlotFree=false (always false when free).
		wantDestFree  string
		wantDestTaken string
		fellBackTaken bool
	}{
		{"canonical AGENTS.md", "AGENTS.md", "AGENTS.md", "CLAUDE.local.md", true},
		{"explicit CLAUDE.md passes through regardless of slot state", "CLAUDE.md", "CLAUDE.md", "CLAUDE.md", false},
		{"nested docs/AGENTS.md is not the canonical root path", "docs/AGENTS.md", "docs/AGENTS.md", "docs/AGENTS.md", false},
		{".github/copilot-instructions.md passes through", ".github/copilot-instructions.md", ".github/copilot-instructions.md", ".github/copilot-instructions.md", false},
		{"an arbitrary other file passes through (catch-all)", "lefthook.yml", "lefthook.yml", "lefthook.yml", false},
	}

	for _, rs := range rootStates {
		for _, tt := range tests {
			t.Run(rs.name+" / "+tt.name, func(t *testing.T) {
				wantDest, wantFellBack := tt.wantDestFree, false
				if !rs.rootSlotFree {
					wantDest, wantFellBack = tt.wantDestTaken, tt.fellBackTaken
				}
				dest, fellBack := MapInstruction(tt.rel, rs.rootSlotFree)
				if dest != wantDest || fellBack != wantFellBack {
					t.Errorf("MapInstruction(%q, %v) = (%q, %v), want (%q, %v)",
						tt.rel, rs.rootSlotFree, dest, fellBack, wantDest, wantFellBack)
				}
			})
		}
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

// TestBridgeWanted_NonOwningLayerNeverBridges pins the negative arm the brief
// names explicitly: a stacked (non-root-slot-owning) layer's AGENTS.md never
// bridges. Even holding every OTHER condition in the qualifying shape (a
// project-keyed set that itself has AGENTS.md, no CLAUDE.md anywhere,
// untracked), asking BridgeWanted for an ORDINAL (non-project) layer key must
// return false — the bridge belongs to the root-slot owner only.
func TestBridgeWanted_NonOwningLayerNeverBridges(t *testing.T) {
	sets := map[LayerName][]string{
		LayerProject: {"AGENTS.md"},
	}
	if BridgeWanted(LayerName("2"), sets, false) {
		t.Error("BridgeWanted(\"2\", ...) = true, want false — bridge belongs to the root-slot owner only")
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
			"CLAUDE.md in a stacked layer's set",
			map[LayerName][]string{
				LayerProject:   {"AGENTS.md"},
				LayerName("2"): {"CLAUDE.md"},
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

// TestBridgeWanted_CLAUDElocalDoesNotSuppress is the regression catcher for
// the "CLAUDE.md anywhere suppresses" check in BridgeWanted: a stacked
// layer's CLAUDE.local.md is a DIFFERENT filename than CLAUDE.md and must NOT
// trip that check. If the exact-match comparison in contains ever degrades to
// a substring/prefix check, CLAUDE.local.md would wrongly match "CLAUDE.md"
// and this test would catch it.
func TestBridgeWanted_CLAUDElocalDoesNotSuppress(t *testing.T) {
	sets := map[LayerName][]string{
		LayerProject:   {"AGENTS.md"},
		LayerName("2"): {"CLAUDE.local.md"},
	}
	if !BridgeWanted(LayerProject, sets, false) {
		t.Error("BridgeWanted = false, want true — CLAUDE.local.md in a stacked layer's set must not suppress the bridge")
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
		{"project layer missing from the map entirely", map[LayerName][]string{LayerName("2"): {"CLAUDE.local.md"}}},
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
