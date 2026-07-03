// This file (layers.go) ports the design §4/§5 layer store: building each
// stack layer's own on-disk copy under $OMK/layers/<layer>/ (the "shadow
// restore" record removing one layer needs later), the pure higher-layer-wins
// merge computation §4 describes as the whole-file overlap rule, and — the
// live verb built on those primitives — RemoveLayer, `omakase remove
// <source>`'s single-layer unlayering (design §4 layer removal / GC7). The
// pure primitives (BuildLayerStore/MergeLayers/RemoveLayerDir) do no git or
// host I/O; RemoveLayer does, mutating the working tree and the marked blocks
// exactly as init's reverse.
package overlay

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Yuncun/omakase-harness/internal/harness"
	"github.com/Yuncun/omakase-harness/internal/state"
	"github.com/Yuncun/omakase-harness/internal/textblock"
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
//     5a. When any rel fell back through MapInstruction (step 2), write the
//     reroute sidecar marker "<tmpdir>/rerouted" (see rerouteMarker) — next
//     to files/, never inside it. Recomputed from this build's own fellBack
//     results on EVERY rebuild (repair, bare-init refetch): a build with no
//     fallback writes no marker, and the rename in step 6 replaces the whole
//     store dir, dropping any stale marker. The store-REUSE path (init's
//     preMapped branch) never rebuilds the store, so an existing marker is
//     preserved there by construction.
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

	var reroutes []string
	for _, rel := range rels {
		// Phase 3.5: the caller passes the real slot-fallback decision. When
		// rootSlotFree is false, this layer's canonical root AGENTS.md is
		// rerouted to CLAUDE.local.md; every other rel passes through. Each
		// reroute is recorded in the store's sidecar marker (step 5a) so a
		// later bottom-removal re-fold can reverse it (RemoveLayer).
		destRel, fellBack := MapInstruction(rel, rootSlotFree)
		if fellBack {
			reroutes = append(reroutes, destRel+"\t"+rel)
		}
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

	// Step 5a: the reroute sidecar marker (see rerouteMarker's doc for the
	// format). Written inside the tmp store — NEVER inside files/ (anything
	// there would leak into the merged payload, placed.tsv, the exclude
	// derivation, and the snapshot) — so the rename below publishes it
	// atomically with the store. A rebuild without any fallback writes no
	// marker, and the wholesale RemoveAll+Rename drops a stale one.
	if len(reroutes) > 0 {
		if err := os.WriteFile(filepath.Join(tmpDir, rerouteMarker), []byte(strings.Join(reroutes, "\n")+"\n"), 0o644); err != nil {
			return fail(fmt.Errorf("overlay: BuildLayerStore: writing %s: %w", rerouteMarker, err))
		}
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

// rerouteMarker is the name of the §7 slot-fallback sidecar file inside a
// layer store dir — "$OMK/layers/<ord>/rerouted", a SIBLING of files/ and
// placed.tsv, never inside files/ (anything under files/ leaks into the
// merged payload, placed.tsv, the exclude derivation, and the snapshot).
//
// Frozen format (Task 6 records it in design §5): one line per rerouted
// entry, "<destRel>\t<origRel>\n" — the rel as stored under files/ first,
// then the canonical payload rel it came from. With the current
// MapInstruction table it carries at most one line:
// "CLAUDE.local.md\tAGENTS.md". Written by BuildLayerStore (step 5a) when a
// rel fell back; read by RemoveLayer's bottom-removal re-fold to reverse the
// reroute before re-mapping. A store built before this marker existed simply
// has none and degrades to the stored (post-mapping) shape unchanged —
// acceptable, zero users of that era's state.
const rerouteMarker = "rerouted"

// rerouteEntry is one parsed line of a store's reroute marker.
type rerouteEntry struct {
	dest, orig string
}

// readRerouted parses a layer store's reroute marker. A missing or unreadable
// marker (including every pre-marker store) yields nil — no un-reroute.
// Malformed lines (wrong field count, empty field) are skipped.
func readRerouted(storeDir string) []rerouteEntry {
	content, err := os.ReadFile(filepath.Join(storeDir, rerouteMarker))
	if err != nil {
		return nil
	}
	var out []rerouteEntry
	for _, line := range strings.Split(strings.TrimRight(string(content), "\n"), "\n") {
		f := strings.SplitN(line, "\t", 2)
		if len(f) == 2 && f[0] != "" && f[1] != "" {
			out = append(out, rerouteEntry{dest: f[0], orig: f[1]})
		}
	}
	return out
}

// RemoveLayerDir deletes a layer's entire on-disk store,
// "<omk>/layers/<layer>/" (files/ + placed.tsv), used by RemoveLayer once its
// shadow-restore pass has finished consulting it. A missing store is not an
// error — os.RemoveAll on an absent path already succeeds silently, matching
// "removing something that's already gone is fine."
func RemoveLayerDir(omk string, layer LayerName) error {
	if err := os.RemoveAll(filepath.Join(omk, "layers", string(layer))); err != nil {
		return fmt.Errorf("overlay: RemoveLayerDir: removing layer %q: %w", layer, err)
	}
	return nil
}

// RemoveLayer unlayers ONE source from a two-layer stack (design §4 layer
// removal), leaving the other as the sole bottom layer (ordinal "1") and
// rebuilding the live merged view to equal a fresh single-source install of the
// survivor (the GC7 twin-diff invariant). recorded is the current stack,
// bottom-to-top ("1" then "2"); removeIdx is 0 (bottom) or 1 (top).
//
// Two directions, differing only in how the survivor's FINAL store at layers/1
// is prepared:
//
//   - TOP removal (removeIdx == 1): the survivor is already the base-folded
//     bottom store at layers/1 — carried through untouched (a stacking init
//     never rebuilt it, so it is byte-identical to what a fresh install of the
//     survivor alone produced, bridge and all).
//   - BOTTOM removal (removeIdx == 0): the survivor is layers/2, a DELTA with no
//     base folded under it. The embedded base is re-folded under it into a fresh
//     layers/1 store — offline, from base + layers/2/files only (GC10) — exactly
//     the fold a fresh init of the survivor performs, with the slot-fallback and
//     §7 bridge re-derived for the now-single layer.
//
// Bottom removal also reverses the survivor's §7 slot-fallback reroute when its
// store carries the sidecar marker (rerouteMarker): the stored CLAUDE.local.md is
// moved back to its original AGENTS.md rel in the re-fold staging, and
// MapInstruction then RE-decides with the recomputed rootSlotFree — never an
// unconditional rename. Under a committed root instruction file the re-map falls
// back again (the rebuilt store re-records its own marker); with the slot free
// the restored root AGENTS.md gets its bridge through the normal BridgeWanted
// path. A pre-marker store has no marker and keeps its stored shape.
//
// After the survivor's final store is at layers/1, both directions share one
// tail: rebuild the snapshot + placed.tsv + exclude/wtinc to the survivor-only
// view BEFORE any working-tree deletion (design §4 rebuild ordering), then
// reconcile the working tree against the OLD live rows: a removed-won path the
// survivor also ships is restored byte-exact (a locally-EDITED file at that
// destination is first preserved to $OMK/clobbered/<rel> under init's exact
// overwrite discipline — the plan's tryClobberBackup mandate); a sole-removed
// path is deleted (untracked + hash-match; a local edit is warned + kept); a
// SURVIVOR-won path no longer in the survivor's rebuilt set (the old
// CLAUDE.local.md after an un-reroute) is swept under init's orphan discipline.
// Then renumber sources.tsv to the single survivor row, point $OMK/source at
// the new bottom (bottom removal only — the file a bare init re-derives the
// bottom layer from; leaving the removed source there would resurrect it),
// drop the stale layers/2, and print the GC5 summary + per-file narration.
func RemoveLayer(root, common, omk string, recorded []state.SourceRow, removeIdx int, stdout, stderr io.Writer) int {
	survivorIdx := 1 - removeIdx
	removedLabel := reassembleSource(recorded[removeIdx])
	survivorRow := recorded[survivorIdx]
	survivorLabel := reassembleSource(survivorRow)
	isTracked := func(rel string) bool { return gitTracked(root, rel) }

	// The survivor's FINAL store always lands at layers/1 (the sole bottom layer).
	survivorDir := filepath.Join(omk, "layers", "1")

	if removeIdx == 0 {
		// ---- BOTTOM removal: re-fold the embedded base under the survivor's delta
		// (layers/2/files) into a fresh layers/1 store. ----
		folded, code := foldBaseUnder(filepath.Join(omk, "layers", "2", "files"), stderr)
		if code != 0 {
			return code
		}
		defer os.RemoveAll(folded)

		// Un-reroute: the survivor's store recorded a §7 slot-fallback reroute
		// (its canonical AGENTS.md was stored as CLAUDE.local.md because the
		// root slot was taken when the stack was built). Restore the ORIGINAL
		// rel in the staging so MapInstruction re-decides against the
		// RECOMPUTED rootSlotFree below — never an unconditional un-reroute:
		// under a committed root instruction file the re-map falls back again
		// (and BuildLayerStore re-records the marker). The rename may clobber
		// the base's own copy of the original rel — correct, the survivor's
		// delta wins that overlap exactly as a fresh init's fold would.
		for _, m := range readRerouted(filepath.Join(omk, "layers", "2")) {
			from := filepath.Join(folded, m.dest)
			if !lexists(from) {
				continue
			}
			to := filepath.Join(folded, m.orig)
			if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
				return 1
			}
			if err := os.Rename(from, to); err != nil {
				fmt.Fprintf(stderr, "omakase: failed to restore rerouted payload file '%s' to '%s'\n", m.dest, m.orig)
				return 1
			}
		}

		// Re-derive the slot-fallback + §7 bridge for the now-single survivor layer,
		// exactly as a fresh init of the survivor would: a committed root instruction
		// file takes the slot, else the survivor owns it (and may bridge).
		rootSlotFree := !(isTracked("AGENTS.md") || isTracked("CLAUDE.md"))
		foldedRels, werr := walkPayload(folded)
		if werr != nil {
			fmt.Fprintln(stderr, werr.Error())
			return 1
		}
		mapped := make([]string, 0, len(foldedRels))
		for _, rel := range foldedRels {
			dest, _ := MapInstruction(rel, rootSlotFree)
			mapped = append(mapped, dest)
		}
		bridge := BridgeWanted(LayerProject, map[LayerName][]string{LayerProject: mapped}, isTracked("CLAUDE.md"))

		// BuildLayerStore RemoveAll's + replaces layers/1 (the removed bottom store)
		// with the re-folded survivor store; the stale layers/2 is dropped in the
		// shared tail below.
		if _, blErr := BuildLayerStore(omk, LayerName("1"), survivorLabel, folded, rootSlotFree, bridge); blErr != nil {
			fmt.Fprintln(stderr, blErr.Error())
			return 1
		}
	}

	survivorFilesDir := filepath.Join(survivorDir, "files")

	// The survivor store's placed.tsv is its FULL post-mapping set. A fresh init of
	// just the survivor places only the UNTRACKED subset (it SKIPs every dest the
	// repo already commits), so the post-unlayer live view is the store minus every
	// path the repo tracks — filter through the same gitTracked check init uses, so
	// a committed harness path is never resurrected into placed.tsv / the snapshot /
	// the exclude block.
	survivorRows := state.ReadPlaced(filepath.Join(survivorDir, "placed.tsv"))
	survivorHas := make(map[string]bool, len(survivorRows)) // restore-source lookup (FULL set)
	var liveSurvivorRows []state.PlacedRow                  // UNTRACKED subset (post-unlayer live view)
	for _, r := range survivorRows {
		survivorHas[r.Rel] = true
		if !isTracked(r.Rel) {
			liveSurvivorRows = append(liveSurvivorRows, r)
		}
	}

	// Read the LIVE placed.tsv BEFORE rebuilding it — the removed-won set is every
	// live row whose column-3 label is the removed layer's label.
	liveRows := state.ReadPlaced(filepath.Join(omk, "placed.tsv"))

	// ---- rebuild snapshot + placed.tsv to the survivor-only view, BEFORE any
	// working-tree deletion (design §4 rebuild ordering: a racing post-checkout heal
	// never observes a stale mix). ----
	snap := filepath.Join(omk, "payload-snapshot")
	if err := os.RemoveAll(snap); err != nil {
		return 1
	}
	if err := os.MkdirAll(snap, 0o755); err != nil {
		return 1
	}
	for _, r := range liveSurvivorRows {
		dst := filepath.Join(snap, r.Rel)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return 1
		}
		if err := CopyEntry(filepath.Join(survivorFilesDir, r.Rel), dst); err != nil {
			return 1
		}
	}
	// Lexical by dest rel (Global Constraint 6 discipline) — the untracked subset
	// already arrives lexical from the store, but sort defensively so placed.tsv /
	// the exclude block match a fresh init's lexical walk order.
	sort.Slice(liveSurvivorRows, func(i, j int) bool { return liveSurvivorRows[i].Rel < liveSurvivorRows[j].Rel })
	if err := state.WritePlaced(filepath.Join(omk, "placed.tsv"), liveSurvivorRows); err != nil {
		fmt.Fprintln(stderr, err.Error())
		return 1
	}
	if code := rewriteExcludeWtinc(root, common, placedRels(liveSurvivorRows), stderr); code != 0 {
		return code
	}

	// ---- working-tree unlayer ----
	umask := currentUmask()
	restored, deleted := 0, 0
	var bullets []string
	for _, row := range liveRows {
		rel := row.Rel
		if isTracked(rel) {
			continue // never touch a tracked path (upstream owns it)
		}
		full := filepath.Join(root, rel)
		if row.Src != removedLabel {
			// Survivor-won live row: normally still in the survivor's rebuilt set
			// and left untouched (its bytes already are the winner's). A
			// bottom-removal un-reroute MOVES the survivor's instruction rel
			// (CLAUDE.local.md -> AGENTS.md), orphaning the old dest — sweep it
			// under init's exact orphan discipline (hash-match delete + empty-dir
			// prune; a local edit is warned and kept, init's own wording).
			if survivorHas[rel] {
				continue
			}
			if !lexists(full) {
				continue
			}
			if state.HashOf(full) == row.Hash {
				if err := DeletePlaced(root, rel, isTracked); err != nil {
					return 1
				}
				deleted++
				bullets = append(bullets, "  - deleted: "+rel+"\n")
			} else {
				fmt.Fprintf(stderr, "omakase: WARNING — '%s' was placed by a prior init, is no longer in the payload, and differs from what init placed (a local edit?). Leaving it; delete it yourself if unwanted.\n", rel)
			}
			continue
		}
		if survivorHas[rel] {
			// The survivor ships this dest: re-place its copy (byte-exact restore).
			// A LOCALLY-EDITED file here (bytes differ from what omakase placed,
			// per the live row's hash) is preserved to $OMK/clobbered/<rel> first,
			// under init's exact overwrite discipline (tryClobberBackup + the
			// overwrote line) — the plan's mandate, generalizing personal off's
			// bridge arm. Unedited removed-layer bytes are replaced silently: the
			// restore IS the operation the user asked for, nothing is lost.
			src := filepath.Join(survivorFilesDir, rel)
			edited := lexists(full) && state.HashOf(full) != row.Hash && !SameFile(full, src)
			saved := ""
			if edited && (!isDir(full) || isSymlink(full)) {
				if tryClobberBackup(full, rel, omk) {
					saved = filepath.Join(omk, "clobbered", rel)
				} else {
					fmt.Fprintf(stderr, "omakase: WARNING — could not back up pre-existing '%s' before overwriting it\n", rel)
				}
			}
			if code := placeFile(src, rel, root, umask, stderr); code != 0 {
				return code
			}
			if edited {
				if saved != "" {
					fmt.Fprintf(stderr, "omakase: overwrote %s to match payload (prior copy preserved at %s)\n", rel, saved)
				} else {
					fmt.Fprintf(stderr, "omakase: overwrote %s to match payload (any local edit was replaced)\n", rel)
				}
			}
			restored++
			bullets = append(bullets, "  ^ restored: "+rel+"\n")
			continue
		}
		// Sole to the removed layer: delete under the untracked + hash-match rule (the
		// orphan-sweep discipline); a local edit is warned and kept.
		if !lexists(full) {
			continue // already gone
		}
		if state.HashOf(full) == row.Hash {
			if err := DeletePlaced(root, rel, isTracked); err != nil {
				return 1
			}
			deleted++
			bullets = append(bullets, "  - deleted: "+rel+"\n")
		} else {
			fmt.Fprintf(stderr, "omakase: WARNING — '%s' was placed by the removed harness, has no lower-layer copy to restore, and differs from what omakase placed (a local edit?). Leaving it; delete it yourself if unwanted.\n", rel)
		}
	}

	// ---- sources.tsv: the sole survivor, renumbered to the bottom ("1"). ----
	survivorRow.Layer = "1"
	if err := state.WriteSources(filepath.Join(omk, "sources.tsv"), []state.SourceRow{survivorRow}); err != nil {
		fmt.Fprintln(stderr, err.Error())
		return 1
	}
	// ---- $OMK/source: the v1 remembered-bottom-source file must name the NEW
	// bottom after a bottom removal — a bare init re-derives the bottom layer
	// from this file (init.go's bottomRemembered), so leaving the REMOVED
	// source here would resurrect it and drop the survivor on the next bare
	// init, and detectMixedEra would warn on every verb. Written exactly as a
	// fresh init of the survivor writes it (label + "\n", init.go's
	// rememberedSource write). Top removal leaves it untouched — the bottom
	// source did not change. ----
	if removeIdx == 0 {
		if err := os.WriteFile(filepath.Join(omk, "source"), []byte(survivorLabel+"\n"), 0o644); err != nil {
			return 1
		}
	}
	// Drop the removed layer's store (TOP: the removed top; BOTTOM: the stale delta,
	// whose content was re-folded into layers/1 above) — always layers/2.
	if err := RemoveLayerDir(omk, LayerName("2")); err != nil {
		fmt.Fprintln(stderr, err.Error())
		return 1
	}

	// ---- GC5 summary + per-file narration (bullets in live-row lexical order). ----
	fmt.Fprintf(stdout, "omakase: removed %s — %d file(s) deleted, %d restored from %s\n", removedLabel, deleted, restored, survivorLabel)
	for _, b := range bullets {
		fmt.Fprint(stdout, b)
	}
	return 0
}

// foldBaseUnder stages the embedded base payload with deltaDir overlaid on top
// (REPLACE semantics — the delta wins any overlap), the same base+delta merge
// runSource performs for a fresh install, minus the fetch. deltaDir is a
// surviving layer store's post-mapping files/ tree; the returned staging dir is
// the caller's to os.RemoveAll on every exit path. On failure it has written the
// byte-appropriate message and returns a non-zero code.
func foldBaseUnder(deltaDir string, stderr io.Writer) (string, int) {
	base := defaultPayload()
	if info, err := os.Stat(base); err != nil || !info.IsDir() {
		fmt.Fprintf(stderr, "omakase: payload dir not found at %s\n", base)
		return "", 1
	}
	merged, err := os.MkdirTemp(os.TempDir(), "omakase-refold.")
	if err != nil {
		fmt.Fprintln(stderr, "omakase: could not create a temp dir to re-fold the base + surviving payload")
		return "", 1
	}
	if err := copyTree(base, merged); err != nil {
		os.RemoveAll(merged)
		fmt.Fprintln(stderr, "omakase: failed to copy the base payload into the re-fold staging dir")
		return "", 1
	}
	rels, err := walkPayload(deltaDir)
	if err != nil {
		os.RemoveAll(merged)
		fmt.Fprintln(stderr, err.Error())
		return "", 1
	}
	for _, rel := range rels {
		dst := filepath.Join(merged, rel)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			os.RemoveAll(merged)
			return "", 1
		}
		if err := os.RemoveAll(dst); err != nil { // rm -rf: also clears a base DIR at this path
			os.RemoveAll(merged)
			return "", 1
		}
		if err := CopyEntry(filepath.Join(deltaDir, rel), dst); err != nil {
			os.RemoveAll(merged)
			fmt.Fprintf(stderr, "omakase: failed to overlay surviving payload file '%s' onto the base payload\n", rel)
			return "", 1
		}
	}
	return merged, 0
}

// placedRels projects a placed-row slice to its rel column (order preserved),
// the input DerivePrefixes and the exclude/wtinc rewrite expect.
func placedRels(rows []state.PlacedRow) []string {
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.Rel)
	}
	return out
}

// rewriteExcludeWtinc re-derives and rewrites the exclude + .worktreeinclude
// marked blocks for the given placed set — the exact derivation init.go performs
// (DerivePrefixes over harness.SharedTopdirs, strip-then-append via textblock,
// the .worktreeinclude-entry skip). Kept in lockstep with init's two blocks; a
// small, documented duplication so RemoveLayer's edits stay off the byte-frozen
// init engine.
func rewriteExcludeWtinc(root, common string, placed []string, stderr io.Writer) int {
	const begin = "# >>> omakase-harness >>>"
	const end = "# <<< omakase-harness <<<"
	exclude := filepath.Join(common, "info", "exclude")

	lefthookTracked := gitTracked(root, "lefthook.yml")
	wtincTracked := gitTracked(root, ".worktreeinclude")
	if wtincTracked {
		fmt.Fprintln(stderr, "omakase: .worktreeinclude is tracked — leaving it untouched (re-run omakase init inside a new manual worktree to install it there).")
	}
	isDirRoot := func(p string) bool { return isDir(filepath.Join(root, p)) }
	prefixes := DerivePrefixes(placed, harness.SharedTopdirs, isDirRoot, lefthookTracked, wtincTracked)

	if err := os.MkdirAll(filepath.Dir(exclude), 0o755); err != nil {
		return 1
	}
	if err := touch(exclude); err != nil {
		return 1
	}
	excludeContent, _ := os.ReadFile(exclude)
	excludeOut := textblock.AppendBlock(textblock.Strip(excludeContent, begin, end), begin, prefixes, end)
	if err := rewriteFile(exclude, excludeOut); err != nil {
		return 1
	}

	if !wtincTracked && len(placed) > 0 {
		wtinc := filepath.Join(root, ".worktreeinclude")
		if err := touch(wtinc); err != nil {
			return 1
		}
		var wtEntries []string
		for _, p := range prefixes {
			if strings.TrimSuffix(p, "/") == ".worktreeinclude" {
				continue
			}
			wtEntries = append(wtEntries, p)
		}
		wtContent, _ := os.ReadFile(wtinc)
		wtOut := textblock.AppendBlock(textblock.Strip(wtContent, begin, end), begin, wtEntries, end)
		if err := rewriteFile(wtinc, wtOut); err != nil {
			return 1
		}
	}
	return 0
}
