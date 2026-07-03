package overlay

// LayerName identifies which of the three fixed stack roles (design §4) a
// placement belongs to: base (embedded in the binary), project (the one
// remembered source), or personal (the one global per-user setting, on top).
// state.SourceRow (internal/state/state.go) carries the same two non-base
// layer names as plain strings ("project" / "personal") in its Layer field —
// these constants are overlay-side only, deliberately not a shared type: they
// are string-assignable and string-comparable (e.g. `string(LayerProject) ==
// row.Layer`), so no conversion helper or shared type is needed to bridge the
// two packages.
type LayerName string

const (
	LayerBase     LayerName = "base"
	LayerProject  LayerName = "project"
	LayerPersonal LayerName = "personal"
)

// MapInstruction is the pure, role-free §7 instruction-routing rule (Phase
// 3.5: docs/v2-design.md §7 is being rewritten in Task 6 to describe this
// slot-fallback model in place of the old per-layer-role table). A harness
// ships exactly ONE instruction file, payload/AGENTS.md, at the canonical
// repo-root-relative path "AGENTS.md" (matched EXACTLY, never as a basename —
// a nested path such as "docs/AGENTS.md" does not match and always passes
// through unchanged). This function decides where THAT one path lands:
//
//   - canonical "AGENTS.md", rootSlotFree == true  -> ("AGENTS.md", false)
//   - canonical "AGENTS.md", rootSlotFree == false -> ("CLAUDE.local.md", true)
//   - any other rel (including an explicitly shipped "CLAUDE.md",
//     ".github/copilot-instructions.md", or anything else) -> (rel, false),
//     always, regardless of rootSlotFree
//
// rootSlotFree is computed entirely by the CALLER (init.go, Task 3) — this
// function does no filesystem or state lookup of its own. Free means: no
// committed AGENTS.md or CLAUDE.md at repo root, AND no already-installed
// lower layer already owns the root instruction slot. Whichever of those
// conditions fails, the caller passes false; MapInstruction does not need to
// (and cannot) tell them apart, since the routing decision is identical
// either way — CLAUDE.local.md, Claude Code's additive, gitignored-by-
// convention slot, is the fallback: instructions placed there ADD to
// whatever already occupies the root slot, they never shadow or replace it.
//
// fellBack reports whether THIS call actually rerouted (true only for the
// canonical-AGENTS.md/rootSlotFree-false case). The caller uses it to emit
// the one-line narration (design contract, GC5):
// "omakase: instructions from <label> -> CLAUDE.local.md (root slot taken)".
//
// An explicit CLAUDE.md or .github/copilot-instructions.md passes through
// unchanged no matter what rootSlotFree is — CLAUDE.md is read natively by
// both Claude Code and Copilot CLI and never needs rerouting the way the
// canonical AGENTS.md does; a committed copy of it is skipped downstream by
// the normal place-loop rule (v1 semantics, unaffected by this function).
//
// The §7 bridge symlink (CLAUDE.md -> AGENTS.md) remains a SEPARATE,
// conditional placement decided by BridgeWanted below, not by this function
// — MapInstruction only ever maps one path to one dest, never adds paths.
//
// Copilot CLI's personal-instruction slot is a deliberate, honest gap (§8):
// Copilot has no per-repo gitignored slot equivalent to CLAUDE.local.md, so a
// fallen-back instruction file is invisible to it — `status` says so.
func MapInstruction(rel string, rootSlotFree bool) (dest string, fellBack bool) {
	if rel != "AGENTS.md" {
		return rel, false
	}
	if rootSlotFree {
		return "AGENTS.md", false
	}
	return "CLAUDE.local.md", true
}

// BridgeWanted reports whether the project layer's placement of a root
// AGENTS.md should ALSO place the §7 bridge artifact: a symlink at
// repo-root CLAUDE.md whose target string is exactly "AGENTS.md" (never
// dereferenced — a relative symlink, root-to-root). The bridge exists so
// Claude Code, which does not read AGENTS.md natively, still gets root
// instructions from a project harness that ships only AGENTS.md.
//
// The bridge is a normal placed file OWNED BY THE PROJECT LAYER's
// post-mapping set (not a fourth layer, not base) — its placed.tsv row
// records source = the project layer's label and sha256 = state.HashOf of
// the symlink, which (per HashOf's existing symlink rule) hashes the target
// STRING "AGENTS.md", not any file's contents. Removing the project layer
// removes the bridge along with everything else it owns; re-running init
// re-derives whether the bridge is still wanted from scratch.
//
// True iff ALL of:
//   - layer == LayerProject — the bridge is a project-layer-only feature.
//     A personal layer's AGENTS.md can fall back to CLAUDE.local.md (see
//     MapInstruction) and never bridges; base's AGENTS.md ships as-is with
//     no bridge at all.
//   - postMappingSets[LayerProject] contains the literal string "AGENTS.md"
//     — the project layer must actually be placing a ROOT AGENTS.md (a
//     project harness shipping only a nested docs/AGENTS.md has nothing for
//     the bridge to point at).
//   - no layer's post-mapping set (any key in postMappingSets) contains
//     "CLAUDE.md" — an explicitly shipped CLAUDE.md at ANY layer already
//     gives Claude Code root instructions; bridging on top would collide
//     with it (two things placed at the same dest).
//   - !repoTracksCLAUDEmd — a team-committed CLAUDE.md the harness doesn't
//     own also suppresses the bridge, the same "a committed file is skipped,
//     never overwritten" rule every other omakase placement follows.
func BridgeWanted(layer LayerName, postMappingSets map[LayerName][]string, repoTracksCLAUDEmd bool) bool {
	if layer != LayerProject {
		return false
	}
	if repoTracksCLAUDEmd {
		return false
	}
	if !contains(postMappingSets[LayerProject], "AGENTS.md") {
		return false
	}
	for _, set := range postMappingSets {
		if contains(set, "CLAUDE.md") {
			return false
		}
	}
	return true
}
