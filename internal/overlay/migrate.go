// This file (migrate.go) implements the design §9 v1→v2 migration surface every
// layer-aware verb shares: lazy sources.tsv synthesis (EnsureSources), mixed-era
// detection (a v1 tool ran after a v2 layered install), and the GC8
// refuse-don't-guess layer-store guard (RequireLayers). init/status/remove all
// route their sources.tsv read through EnsureSources, so the first v2 run of
// ANY verb against a still-v1 repo migrates it once — silently, without ever
// touching a working-tree file.
//
// The synthesis math (the $OMK/source → sources.tsv row, commit "-" never
// guessed) lives in state.SynthesizeSources, which is READ-ONLY by contract; this
// file owns the WRITE-back and the wiring policy: when to write, when to stay
// silent, and the best-effort discipline that keeps a read-only verb (status)
// from ever failing on a synthesis write it could not complete.
package overlay

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/Yuncun/omakase-harness/internal/state"
)

// EnsureSources returns the repo's layer stack (sources.tsv rows), migrating a
// still-v1 repo along the way (design §9 lazy synthesis). It is the single read
// point every Phase 3 verb uses in place of a raw state.ReadSources:
//
//   - $OMK absent          → nil, write nothing (a not-installed repo has nothing
//     to migrate; status and remove must NEVER create $OMK).
//   - sources.tsv present  → read it (the single source of truth once it exists),
//     run mixed-era detection against $OMK/source, and return the rows unchanged.
//   - sources.tsv absent   → state.SynthesizeSources from $OMK/source (commit "-",
//     never guessed); if a row was synthesized, WRITE it back (state.WriteSources,
//     GC3 tmp+rename) and return the rows. No $OMK/source to synthesize from →
//     nil, write nothing.
//
// WRITE-FAILURE POLICY — the plan's status write-vs-read-only subtlety, decided
// here uniformly for ALL verbs: a synthesis write is BEST-EFFORT and its failure
// is silently ignored — EnsureSources returns the freshly-synthesized rows either
// way. A read-only filesystem, a lost worktree race, or a $OMK that vanished
// mid-run must never turn a `status` (read-only in spirit) or a `remove` (about
// to rm -rf $OMK moments later) into an error; the next MUTATING verb simply
// retries the synthesis. `init`, which re-records sources.tsv with resolved
// commits at the end of its own run, never depends on this early write landing.
func EnsureSources(omk string, stderr io.Writer) []state.SourceRow {
	if _, err := os.Stat(omk); err != nil {
		return nil // $OMK absent: nothing installed, nothing to migrate
	}
	sourcesPath := filepath.Join(omk, "sources.tsv")
	if fileRegular(sourcesPath) {
		rows := state.ReadSources(sourcesPath)
		detectMixedEra(omk, rows, stderr)
		return rows
	}
	epoch := strconv.FormatInt(time.Now().Unix(), 10)
	rows, ok := state.SynthesizeSources(omk, epoch)
	if !ok {
		return nil // no $OMK/source line to synthesize from
	}
	_ = state.WriteSources(sourcesPath, rows) // best-effort, silent (see policy above)
	return rows
}

// mixedEraWarn builds the one-line design §9 warning; detail is the parenthetical
// naming which disagreement fired. Byte-frozen (migrate_test.go pins both forms),
// shared verbatim across every verb.
func mixedEraWarn(detail string) string {
	return "omakase: WARNING — a pre-layers omakase run changed this repo's source (" + detail + "); run omakase init to reheal."
}

// detectMixedEra emits the design §9 mixed-era warning — a v1 tool ran AFTER a v2
// layered install and left $OMK/source / placed.tsv disagreeing with sources.tsv.
// Gated on $OMK/source EXISTING: a v2 sources.tsv coexisting with a $OMK/source is
// the signal a v1 tool touched the repo (§9 "sources.tsv disagreeing with
// $OMK/source + placed.tsv"). At most ONE line is printed (axis 1 takes
// precedence) — either symptom is cured by the SAME `omakase init` reheal.
// WARNING ONLY: no reheal here. init reheals through its normal remembered-source
// flow; status/remove warn and move on.
//
// Layers are ordinal strings (Phase 3.5): "1" is the bottom layer — the one
// $OMK/source remembers — and "2" is the stacked layer on top.
func detectMixedEra(omk string, rows []state.SourceRow, stderr io.Writer) {
	srcLine := state.FirstLine(filepath.Join(omk, "source"))
	if srcLine == "" {
		return // no $OMK/source → not the mixed-era shape (§9 gates on BOTH files)
	}

	// Axis 1: the bottom row (layer "1"), reassembled as "source[#ref]" exactly as
	// $OMK/source stores it, disagrees with $OMK/source — a v1 tool rewrote the
	// remembered source out from under sources.tsv.
	for _, r := range rows {
		if r.Layer != "1" {
			continue
		}
		if reassembleSource(r) != srcLine {
			fmt.Fprintln(stderr, mixedEraWarn("$OMK/source disagrees with sources.tsv"))
			return
		}
		break // exactly one bottom row ever
	}

	// Axis 2: a stacked layer (any row above the bottom) is recorded but NO
	// placed.tsv row carries its label (col 3) — a v1 orphan sweep ate that
	// layer's files.
	//
	// Known caveat, accepted (triaged, not fixed): a false-positive if EVERY
	// path a stacked layer owns is git-tracked — placement skips a tracked dest,
	// so no placed.tsv row ever carries its label and this fires on a repo no v1
	// tool touched; init doesn't cure it either (contrived in practice: a
	// stacked layer's instructions land at CLAUDE.local.md, essentially never
	// tracked).
	for _, r := range rows {
		if r.Layer == "1" {
			continue
		}
		label := reassembleSource(r)
		for _, p := range state.ReadPlaced(filepath.Join(omk, "placed.tsv")) {
			if p.Src == label {
				return // the stacked layer is still placed — no mismatch
			}
		}
		fmt.Fprintln(stderr, mixedEraWarn("a stacked layer is missing from placed.tsv"))
		return // at most one stacked layer ever (cap 2)
	}
}

// reassembleSource rebuilds a source row's "source[#ref]" string exactly as
// $OMK/source and placed.tsv column 3 store it: the source, plus "#ref" only when
// a ref was actually pinned ("-"/"" is the no-ref placeholder, never appended).
func reassembleSource(r state.SourceRow) string {
	if r.Ref != "-" && r.Ref != "" {
		return r.Source + "#" + r.Ref
	}
	return r.Source
}

// RequireLayers is the design §9 GC8 refuse-don't-guess guard: an operation that
// needs the $OMK/layers/ store before it exists (an unmigrated / pre-layers repo)
// must refuse rather than guess. Returns true when $OMK/layers/ is a directory;
// otherwise prints the one-line refusal to stderr and returns false. The message
// is byte-frozen (remove_test.go's GC8 refusal test + migrate_test.go's
// TestRequireLayers pin it; tests/layers.test.sh L10 exercises the same
// refusal end-to-end).
func RequireLayers(omk string, stderr io.Writer) bool {
	if isDir(filepath.Join(omk, "layers")) {
		return true
	}
	fmt.Fprintln(stderr, "omakase: this repo predates layered state — run omakase init once first")
	return false
}
