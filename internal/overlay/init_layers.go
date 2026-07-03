// This file (init_layers.go) holds the layering additions RunInit (init.go)
// wires in on top of the byte-parity Phase 2 engine: the sources.tsv ref-field
// placeholder and buildMergedStaging — the TRANSIENT higher-layer-wins merge
// (design §4) the engine places from and the wiring guard validates against.
//
// buildMergedStaging is deliberately SEPARATE from BuildLayerStore (layers.go): it
// assembles the merged tree the engine mutates the working tree from WITHOUT
// persisting any $OMK/layers/ store, so it can run BEFORE the refusal-capable guards
// (the wiring guard needs the merged tree) while the persistent stores are built
// LATER, after those guards pass (init.go's rebuild-ordering slot). The two share
// the same §7 slot-fallback mapping (MapInstruction) and the same fail-closed
// collision rule, applied to their two different outputs.
//
// A spec is either FRESH (payloadDir is a raw payload whose rels are mapped through
// MapInstruction with spec.rootSlotFree) or PRE-MAPPED (payloadDir is an already-
// built layer store's files/ dir whose rels are already dest rels — copied as-is,
// no re-mapping). Pre-mapped specs let a stacking init reuse a lower layer's persisted
// store without re-fetching its source (Phase 3.5).
package overlay

import (
	"fmt"
	"os"
	"path/filepath"
)

// layerSpec is one entry of the resolved layer stack RunInit assembles
// (bottom-to-top): its ordinal identity (LayerName "1"/"2", also the
// $OMK/layers/<layer>/ store dir name), the placed.tsv column-3 label every
// file it wins is recorded with, and the payload dir its files come from.
//
// payloadDir is EITHER a raw payload (rootSlotFree feeds MapInstruction; the
// bottom layer's runSource base+delta merge, or a top layer's fetched payload)
// or, when preMapped is true, an already-built store's files/ dir whose entries
// are already dest rels (a lower layer reused across a stacking init). bridge
// requests the §7 CLAUDE.md -> AGENTS.md symlink (a fresh root-slot-owning layer
// only; a pre-mapped store already carries its own bridge, if any).
type layerSpec struct {
	layer        LayerName
	label        string
	payloadDir   string
	rootSlotFree bool
	preMapped    bool
	bridge       bool
}

// layerPlan is one resolved entry of the target stack RunInit builds from the
// recorded sources.tsv + the CLI source arg (Phase 3.5 decision table),
// bottom-to-top. source/ref are the expanded source string and its pin ("" =
// none). refetch true rebuilds this layer's store from a fresh fetch (a new,
// repaired, or bare-init layer); false reuses the persisted store as-is (a
// lower layer left untouched under a stacking init). epoch/commit carry the
// prior sources.tsv row's values, preserved for a recorded layer (epoch "" =
// stamp now; commit "" = re-resolved after a fetch).
type layerPlan struct {
	source  string
	ref     string
	refetch bool
	epoch   string
	commit  string
}

// displayLabel is a source's status/placed.tsv label: the source string, plus
// "#ref" only when a ref was pinned (the same grammar runSource/fetchSource and
// status render).
func displayLabel(source, ref string) string {
	if ref != "" {
		return source + "#" + ref
	}
	return source
}

// unRefField is the inverse of refField: a sources.tsv ref column ("-" for no
// pin) back to the bare ref ("" for no pin).
func unRefField(ref string) string {
	if ref == "-" {
		return ""
	}
	return ref
}

// refField is the sources.tsv `ref` column value: the requested #ref, or "-" when
// none was given (design §5 — "-" is a present placeholder, never an empty field,
// which state.WriteSources would refuse).
func refField(ref string) string {
	if ref == "" {
		return "-"
	}
	return ref
}

// buildMergedStaging assembles the design §4 higher-layer-wins merged tree from the
// given layer specs (bottom-to-top) into a fresh MkdirTemp staging dir, WITHOUT
// persisting any $OMK/layers/ store (BuildLayerStore does that later). It returns
// the staging dir (the caller RemoveAlls it on every exit path, matching v1's
// EXIT-trap merge cleanup), a map from each placed dest rel to the WINNING layer's
// label (placed.tsv column 3), and an error.
//
// A FRESH spec's payload paths route through MapInstruction(rel, spec.rootSlotFree);
// a PRE-MAPPED spec's paths (a reused store's files/ tree) are already dest rels and
// are copied as-is. A higher layer overwrites a lower layer's file at the same dest
// (CopyEntry rm-firsts, so a symlink cleanly replaces a regular file and vice versa)
// and its label wins. Two SOURCE rels WITHIN ONE fresh layer mapping to the same
// dest is a fail-closed collision (the §7 AGENTS.md + explicit CLAUDE.local.md case)
// — never a silent last-writer-wins; the error names both contributors and the
// shared dest. A fresh layer's bridge places a CLAUDE.md -> AGENTS.md symlink
// (subject to the same within-layer collision check).
func buildMergedStaging(specs []layerSpec) (staging string, labelByRel map[string]string, err error) {
	staging, err = os.MkdirTemp(os.TempDir(), "omakase-merge.")
	if err != nil {
		return "", nil, fmt.Errorf("could not create a temp dir to merge the layer payloads")
	}
	fail := func(e error) (string, map[string]string, error) {
		os.RemoveAll(staging)
		return "", nil, e
	}

	// Pre-check the merged DEST-rel set for a file/symlink-vs-directory conflict
	// BEFORE placing a single byte: one layer shipping `data` (a file/symlink) and
	// another shipping `data/loot` (an entry beneath it) is a malformed harness —
	// the Phase 3.5 stacked-parent traversal in structural form. Refuse fail-closed
	// with the two conflicting paths and their two source labels. (The safeMkdirAll
	// guard in `place` below is the order-independent backstop; this early check
	// gives the clear, labelled message and guarantees ZERO placement.)
	if e := checkStagingPrefixConflict(specs); e != nil {
		return fail(e)
	}

	labelByRel = make(map[string]string)
	for _, spec := range specs {
		rels, werr := walkPayload(spec.payloadDir)
		if werr != nil {
			return fail(werr)
		}
		// origin tracks, PER LAYER, what produced each dest rel — only so a
		// collision error can name both contributors. Reset each layer: the same
		// dest across layers is an OVERLAP (higher wins), not a collision.
		origin := make(map[string]string, len(rels)+1)
		place := func(sourceDescr, dest string, doCopy func(dst string) error) error {
			if prev, dup := origin[dest]; dup {
				return fmt.Errorf("layer %q: %q and %q both map to %q (two payload files map to one path)", spec.layer, prev, sourceDescr, dest)
			}
			origin[dest] = sourceDescr
			dst := filepath.Join(staging, dest)
			// safeMkdirAll refuses a symlinked parent under the staging root: a
			// lower layer's directory-symlink must never become the path a higher
			// layer's child is written through (the Phase 3.5 stacked traversal).
			if e := safeMkdirAll(staging, filepath.Dir(dst)); e != nil {
				return e
			}
			if e := doCopy(dst); e != nil {
				return e
			}
			labelByRel[dest] = spec.label
			return nil
		}
		for _, rel := range rels {
			dest := rel
			if !spec.preMapped {
				// FRESH layer: route the one canonical root AGENTS.md through the
				// §7 slot-fallback rule; everything else passes through.
				dest, _ = MapInstruction(rel, spec.rootSlotFree)
			}
			src := filepath.Join(spec.payloadDir, rel)
			if e := place(rel, dest, func(dst string) error { return CopyEntry(src, dst) }); e != nil {
				return fail(e)
			}
		}
		if spec.bridge {
			if e := place("<bridge CLAUDE.md -> AGENTS.md>", "CLAUDE.md", func(dst string) error {
				if rmErr := os.Remove(dst); rmErr != nil && !os.IsNotExist(rmErr) {
					return rmErr
				}
				return os.Symlink("AGENTS.md", dst)
			}); e != nil {
				return fail(e)
			}
		}
	}
	return staging, labelByRel, nil
}

// checkStagingPrefixConflict resolves each spec's DEST rels the same way
// buildMergedStaging's place loop does (fresh specs route through MapInstruction;
// pre-mapped specs are already dest rels; a bridge adds "CLAUDE.md") and reports a
// file/symlink-vs-directory conflict across the merged view — a leaf entry P and
// another entry beneath "P/" contributed by two layers. It walks the payloads (no
// mutation) and returns the labelled refusal, or nil when the view is well-formed.
func checkStagingPrefixConflict(specs []layerSpec) error {
	labelOf := make(map[string]string)
	var union []string
	note := func(dest, label string) {
		if _, seen := labelOf[dest]; !seen {
			union = append(union, dest)
		}
		labelOf[dest] = label // higher layer wins, matching the place loop
	}
	for _, spec := range specs {
		rels, werr := walkPayload(spec.payloadDir)
		if werr != nil {
			return werr
		}
		for _, rel := range rels {
			dest := rel
			if !spec.preMapped {
				dest, _ = MapInstruction(rel, spec.rootSlotFree)
			}
			note(dest, spec.label)
		}
		if spec.bridge {
			note("CLAUDE.md", spec.label)
		}
	}
	if parent, child, found := prefixConflict(union); found {
		return fmt.Errorf("%s", symlinkDirConflictMsg(parent, labelOf[parent], child, labelOf[child]))
	}
	return nil
}
