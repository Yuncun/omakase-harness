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

// MapLayerPath is the pure §7 instruction-mapping table (docs/v2-design.md:
// 130-146): a harness ships exactly ONE instruction file, payload/AGENTS.md,
// and this function is the literal per-layer, per-path fan-out rule that
// turns it (and every other payload path) into the repo-root-relative
// destination it's placed at. rel is matched EXACTLY as a repo-root-relative
// path, never as a basename: "AGENTS.md" means the ROOT AGENTS.md only — a
// nested path such as "docs/AGENTS.md" does not match that row and falls
// through to "everything else, as-is" for every layer, including personal.
//
// The table (dest column blank means "rel, unchanged"):
//
//	rel                              | layer            | dest
//	---------------------------------|------------------|------------------
//	AGENTS.md                        | project          | (as-is)
//	AGENTS.md                        | personal         | CLAUDE.local.md
//	AGENTS.md                        | base             | (as-is)
//	CLAUDE.md (shipped explicitly)   | any              | (as-is)
//	.github/copilot-instructions.md  | any              | (as-is)
//	everything else                  | any              | (as-is)
//
// The personal+AGENTS.md row is the ONLY reroute in the whole table:
// CLAUDE.local.md is Claude Code's additive, gitignored-by-convention slot —
// personal instructions there ADD to the project's CLAUDE.md, they never
// shadow/replace it, which is why personal is rerouted instead of placed at
// AGENTS.md (where "higher layer wins" whole-file replacement, design §4,
// would otherwise make it clobber the project's instructions). The project
// row's AGENTS.md may ALSO gain a bridge symlink CLAUDE.md -> AGENTS.md
// alongside it — a separate, conditional placement decided by BridgeWanted,
// not by this function (MapLayerPath only ever maps ONE path to ONE dest; it
// never adds paths). The base row has no such bridge: bridging is a
// project-layer-only feature (see BridgeWanted).
//
// An explicitly shipped CLAUDE.md or .github/copilot-instructions.md maps
// as-is regardless of layer — CLAUDE.md is read natively by both Claude Code
// and Copilot CLI, and .github/copilot-instructions.md is Copilot's own
// convention; neither needs rerouting the way AGENTS.md does.
//
// Copilot CLI's personal-instruction slot is a deliberate, honest gap (§8):
// Copilot has no per-repo gitignored personal slot equivalent to
// CLAUDE.local.md, so this function places nothing anywhere for it — personal
// instructions are Claude-only for now, and `status` says so. Revisiting that
// costs one row in this table, not a redesign.
func MapLayerPath(layer LayerName, rel string) string {
	switch {
	case layer == LayerPersonal && rel == "AGENTS.md":
		return "CLAUDE.local.md"
	default:
		return rel
	}
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
//     Personal's AGENTS.md is rerouted to CLAUDE.local.md by MapLayerPath and
//     never bridges; base's AGENTS.md ships as-is with no bridge at all.
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
	if !containsPath(postMappingSets[LayerProject], "AGENTS.md") {
		return false
	}
	for _, set := range postMappingSets {
		if containsPath(set, "CLAUDE.md") {
			return false
		}
	}
	return true
}

func containsPath(set []string, target string) bool {
	for _, p := range set {
		if p == target {
			return true
		}
	}
	return false
}
