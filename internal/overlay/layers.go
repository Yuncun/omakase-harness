// This file (layers.go) ports the design §4/§5 layer store: building each
// stack layer's own on-disk copy under $OMK/layers/<layer>/ (the "shadow
// restore" record removing one layer needs later) and the pure
// higher-layer-wins merge computation §4 describes as the whole-file overlap
// rule. No git, no hook wiring, no host process here — this is the
// building-block layer Task 4 (init) and Task 5 (personal off) wire into the
// live verbs.
package overlay

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/Yuncun/omakase-harness/internal/harness"
	"github.com/Yuncun/omakase-harness/internal/state"
)

// LayerSet is the in-memory result of building (or re-describing) one
// layer's on-disk store: the post-mapping destination-relative paths it owns
// (Rels, lexical) and, for each, the absolute path of that entry's file
// under $OMK/layers/<layer>/files/ (Src). Label is the placed.tsv column-3
// value every entry in this layer's store was written with (design §5:
// "base", or the layer's source string).
type LayerSet struct {
	Layer LayerName
	Label string
	Rels  []string          // post-mapping dest rels, lexical
	Src   map[string]string // destRel -> absolute path under $OMK/layers/<layer>/files/
}

// BuildLayerStore rebuilds ONE layer's on-disk store at
// $OMK/layers/<layer>/ from payloadDir (design §5's new
// "layers/<layer>/files/ + layers/<layer>/placed.tsv" artifact) and returns
// the in-memory LayerSet describing what it just wrote.
//
// Steps:
//
//  1. Walk payloadDir lexically (files + symlinks; walkPayload, the same
//     GC6-lexical helper init.go's payload walk uses).
//
//  2. Map each source rel through MapInstruction(rel, rootSlotFree) to its
//     dest rel, and CopyEntry it (cp -P semantics: a symlink source is
//     recreated as a symlink with the identical target string, never
//     dereferenced; a regular file is byte-copied) to
//     "<omk>/layers/<layer>.tmp-<pid>/files/<destRel>". rootSlotFree is the
//     caller's slot-fallback decision (init.go, Phase 3.5): when false, this
//     layer's canonical root AGENTS.md is rerouted to CLAUDE.local.md.
//
//  3. If two source rels map to the SAME dest rel, refuse: return an error
//     naming both source rels and the colliding dest, and clean up the tmp
//     dir. This can only happen with the CURRENT §7 mapping table when a
//     personal payload ships BOTH a root AGENTS.md (rerouted to
//     CLAUDE.local.md) and an explicit CLAUDE.local.md of its own — the two
//     sources would silently fight over one dest. Last-walked-wins would be
//     a silent, order-dependent data loss; this fails closed instead.
//
//  4. When bridge is true, additionally place the §7 bridge artifact: a
//     symlink at dest "CLAUDE.md" whose target string is exactly
//     "AGENTS.md" (never dereferenced) — subject to the same collision
//     check as any other dest (a payload that itself ships a root
//     CLAUDE.md alongside a bridge request is a caller error, per
//     BridgeWanted's own "no CLAUDE.md anywhere" guard; this function
//     enforces it defensively too, rather than trusting the caller).
//
//  5. Write "<tmpdir>/placed.tsv" via state.WritePlaced: one row per dest
//     entry, {Rel: destRel, Kind: harness.KindOf(destRel), Src: label,
//     Hash: state.HashOf(<file under files/>), Enabled: "1"}, in LEXICAL
//     destRel order (not source-walk order — MapInstruction's one reroute can
//     move an entry out of the source walk's lexical position).
//
//     state.WritePlaced truncate-writes rather than tmp+rename internally —
//     that is fine here, deliberately: placed.tsv is written INSIDE the tmp
//     store dir, which is never the live $OMK/layers/<layer>/ path until
//     the rename in step 6 succeeds, so there is no live reader for
//     WritePlaced's truncate window to race.
//
//  6. Build-fully-then-rename (design §4's rebuild ordering, restated at
//     §5's layers/ entry): os.RemoveAll the FINAL "<omk>/layers/<layer>"
//     and os.Rename the fully-built tmp dir over it. A reader only ever
//     observes the complete prior store or the complete new one, never a
//     partial one.
//
// On ANY error (including the collision refusal), the tmp dir is removed
// and the function returns before step 6 ever runs — the prior store at
// "<omk>/layers/<layer>" is left completely untouched byte-for-byte.
func BuildLayerStore(omk string, layer LayerName, label string, payloadDir string, rootSlotFree, bridge bool) (*LayerSet, error) {
	rels, err := walkPayload(payloadDir)
	if err != nil {
		return nil, fmt.Errorf("overlay: BuildLayerStore: walking payload %q: %w", payloadDir, err)
	}

	layersDir := filepath.Join(omk, "layers")
	finalDir := filepath.Join(layersDir, string(layer))
	tmpDir := filepath.Join(layersDir, fmt.Sprintf("%s.tmp-%d", layer, os.Getpid()))
	tmpFiles := filepath.Join(tmpDir, "files")

	// Any error from here on removes tmpDir and returns — finalDir (the
	// prior store, if any) is never touched until the rename at the very
	// end succeeds.
	fail := func(err error) (*LayerSet, error) {
		os.RemoveAll(tmpDir)
		return nil, err
	}
	failErr := func(err error) error {
		_, err = fail(err)
		return err
	}

	if err := os.MkdirAll(tmpFiles, 0o755); err != nil {
		return fail(fmt.Errorf("overlay: BuildLayerStore: creating %q: %w", tmpFiles, err))
	}

	// origin tracks, per dest rel, what produced it — either a source rel
	// from the walk, or the literal bridge marker below — purely so a
	// collision error can name both contributors.
	origin := make(map[string]string, len(rels)+1)
	set := &LayerSet{Layer: layer, Label: label, Src: make(map[string]string, len(rels)+1)}

	place := func(sourceDescr, destRel string, copy func(dst string) error) error {
		if prev, exists := origin[destRel]; exists {
			return failErr(fmt.Errorf(
				"overlay: BuildLayerStore: layer %q: %q and %q both map to %q — refusing (fail-closed collision, no silent last-writer-wins)",
				layer, prev, sourceDescr, destRel))
		}
		origin[destRel] = sourceDescr

		dst := filepath.Join(tmpFiles, destRel)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return failErr(fmt.Errorf("overlay: BuildLayerStore: creating dir for %q: %w", destRel, err))
		}
		if err := copy(dst); err != nil {
			return failErr(fmt.Errorf("overlay: BuildLayerStore: placing %q (from %q): %w", destRel, sourceDescr, err))
		}
		set.Rels = append(set.Rels, destRel)
		return nil
	}

	for _, rel := range rels {
		// Phase 3.5: the caller passes the real slot-fallback decision. When
		// rootSlotFree is false, this layer's canonical root AGENTS.md is
		// rerouted to CLAUDE.local.md; every other rel passes through.
		destRel, _ := MapInstruction(rel, rootSlotFree)
		if err := place(rel, destRel, func(dst string) error {
			return CopyEntry(filepath.Join(payloadDir, rel), dst)
		}); err != nil {
			return nil, err
		}
	}

	if bridge {
		if err := place("<bridge CLAUDE.md -> AGENTS.md>", "CLAUDE.md", func(dst string) error {
			return os.Symlink("AGENTS.md", dst)
		}); err != nil {
			return nil, err
		}
	}

	// Lexical order by DEST rel (Global Constraint 6's discipline, applied
	// here to the post-mapping set): the source walk is lexical in SOURCE
	// rels, but MapInstruction's one slot-fallback reroute (a canonical root
	// AGENTS.md -> CLAUDE.local.md) can move an entry out of that order.
	sort.Strings(set.Rels)

	rows := make([]state.PlacedRow, 0, len(set.Rels))
	for _, destRel := range set.Rels {
		rows = append(rows, state.PlacedRow{
			Rel:     destRel,
			Kind:    harness.KindOf(destRel),
			Src:     label,
			Hash:    state.HashOf(filepath.Join(tmpFiles, destRel)),
			Enabled: "1",
		})
	}
	if err := state.WritePlaced(filepath.Join(tmpDir, "placed.tsv"), rows); err != nil {
		return fail(fmt.Errorf("overlay: BuildLayerStore: writing placed.tsv: %w", err))
	}

	if err := os.RemoveAll(finalDir); err != nil {
		return fail(fmt.Errorf("overlay: BuildLayerStore: removing prior store %q: %w", finalDir, err))
	}
	if err := os.Rename(tmpDir, finalDir); err != nil {
		return fail(fmt.Errorf("overlay: BuildLayerStore: renaming %q -> %q: %w", tmpDir, finalDir, err))
	}

	finalFiles := filepath.Join(finalDir, "files")
	for _, destRel := range set.Rels {
		set.Src[destRel] = filepath.Join(finalFiles, destRel)
	}

	return set, nil
}

// MergedView is the result of MergeLayers: the union of every layer's dest
// rels (lexical) plus, per rel, which layer's LayerSet won it.
type MergedView struct {
	Rels   []string // union, lexical
	Winner map[string]*LayerSet
}

// MergeLayers computes the design §4 overlap rule — whole-file replacement,
// higher layer wins, never content merging — over an already-built stack of
// LayerSets. sets must be ordered bottom-to-top (ordinal 1 at the bottom,
// 2 on top): for any dest rel two or more layers place, the LAST set in the
// slice that places it wins, because later entries simply overwrite earlier
// ones in the winner map. A higher layer's CLAUDE.local.md never collides
// with a lower layer's AGENTS.md here — they are different dest rels
// (MapInstruction's slot-fallback reroute already made them so), so both survive
// independently with their own winners; this function does no
// filename-aware reasoning of its own, only a plain per-rel overwrite.
//
// Pure function: no I/O, no ordering requirement on the caller beyond
// "bottom-to-top", and no requirement that every LayerName appear.
func MergeLayers(sets []*LayerSet) *MergedView {
	winner := make(map[string]*LayerSet)
	for _, set := range sets {
		for _, rel := range set.Rels {
			winner[rel] = set
		}
	}

	rels := make([]string, 0, len(winner))
	for rel := range winner {
		rels = append(rels, rel)
	}
	sort.Strings(rels)

	return &MergedView{Rels: rels, Winner: winner}
}

// RemoveLayerDir deletes a layer's entire on-disk store,
// "<omk>/layers/<layer>/" (files/ + placed.tsv), used by `omakase personal
// off`'s teardown (Task 5) once its shadow-restore pass has finished
// consulting it. A missing store is not an error — os.RemoveAll on an
// absent path already succeeds silently, matching "removing something
// that's already gone is fine."
func RemoveLayerDir(omk string, layer LayerName) error {
	if err := os.RemoveAll(filepath.Join(omk, "layers", string(layer))); err != nil {
		return fmt.Errorf("overlay: RemoveLayerDir: removing layer %q: %w", layer, err)
	}
	return nil
}
