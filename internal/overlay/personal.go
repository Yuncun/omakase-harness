// This file (personal.go) implements the `omakase personal` verb (design §3
// personal row / §4 layer-removal / §5 the per-user global setting). It is a
// NEW v2 verb — there is no bin/*.sh original and no v1 byte oracle; every
// message is specified verbatim by the Phase 3 plan (Task 5) and pinned by
// personal_test.go.
//
// Three arms plus a usage arm:
//
//   - no arg      : print the current setting (stdout, exit 0).
//   - <source>    : fail-closed-validate (expandSource + fetchSource, the SAME
//     machinery init's project/personal arms use, so a broken
//     source prints init's byte-identical refusal), then write the
//     resolved source to ${XDG_CONFIG_HOME:-$HOME/.config}/omakase/
//     personal and — if cwd is an initialized repo — apply it now
//     by re-running the layering engine (bare RunInit).
//   - off         : clear the global setting, and (in an initialized layered
//     repo carrying a personal row) UNLAYER it: restore each
//     personal-won path's next-lower copy from $OMK/layers/, delete
//     the sole-personal ones (untracked + hash-match, edits kept),
//     rebuild the snapshot + placed.tsv + exclude/wtinc to the
//     post-unlayer merged view, drop the personal row, and remove
//     the personal store.
//
// The unlayer half deliberately reuses the Phase 2 primitives (placeFile,
// DeletePlaced, CopyEntry, copyTree, DerivePrefixes, textblock, state.*) rather
// than re-deriving them; the exclude/.worktreeinclude rewrite mirrors init.go's
// two blocks (a small, documented duplication kept in lockstep with init).
package overlay

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/Yuncun/omakase-harness/internal/harness"
	"github.com/Yuncun/omakase-harness/internal/state"
	"github.com/Yuncun/omakase-harness/internal/textblock"
)

// personalUsageText is the byte-exact 6-line usage heredoc for the verb (the
// "anything else" arm, exit 2). Written once here and pinned by
// personal_test.go — this verb has no legacy bytes to inherit.
const personalUsageText = "usage: omakase personal [<owner/repo[#ref]> | --source <git-url|path> | off]\n" +
	"\n" +
	"  (no argument)  print the current personal harness setting.\n" +
	"  <owner/repo[#ref]>            set the personal harness from a shorthand source.\n" +
	"  --source <git-url|path>       set the personal harness from a git URL or local path.\n" +
	"  off            clear the personal harness globally and unlayer it from the current repo.\n"

// RunPersonal is the `omakase personal` verb. argv is the arguments AFTER the
// verb, the same shape every other Run function receives. It returns the process
// exit code: 2 for usage/arg errors, 1 for fail-closed refusals / GC8, 0 on
// success.
func RunPersonal(argv []string, stdout, stderr io.Writer) int {
	switch {
	case len(argv) == 0:
		return personalShow(stdout)
	case argv[0] == "off":
		if len(argv) != 1 { // `off` takes no further arguments
			fmt.Fprint(stderr, personalUsageText)
			return 2
		}
		return personalOff(stdout, stderr)
	case argv[0] == "--source":
		if len(argv) != 2 { // exactly one value, nothing after it
			fmt.Fprint(stderr, personalUsageText)
			return 2
		}
		return personalSet(argv[1], stdout, stderr)
	case strings.HasPrefix(argv[0], "-"):
		// any other flag (incl. -h/--help): not a recognized form.
		fmt.Fprint(stderr, personalUsageText)
		return 2
	default:
		if len(argv) != 1 { // a single positional source and nothing after it
			fmt.Fprint(stderr, personalUsageText)
			return 2
		}
		return personalSet(argv[0], stdout, stderr)
	}
}

// personalShow prints the current setting (design §5 / plan Task 5 no-arg arm):
// the stored first line, or "(none)" when the config file is absent/empty.
func personalShow(stdout io.Writer) int {
	if line := state.FirstLine(personalConfigPath()); line != "" {
		fmt.Fprintln(stdout, "personal harness: "+line)
	} else {
		fmt.Fprintln(stdout, "personal harness: (none)")
	}
	return 0
}

// personalSet validates a source fail-closed, records it as the global personal
// setting, and applies it to the current repo if that repo is initialized (plan
// Task 5 set arm).
func personalSet(raw string, stdout, stderr io.Writer) int {
	// TSV safety: the resolved source becomes the personal row's source field in
	// sources.tsv on the next init, so a tab/newline would corrupt that file — the
	// same guard (and byte-exact message) init applies to its own source arg.
	if strings.ContainsAny(raw, "\t\n") {
		fmt.Fprintln(stderr, "omakase: --source must not contain a tab or newline")
		return 2
	}

	source, ref := expandSource(raw)

	// Fail-closed validation FIRST — the SAME expandSource + fetchSource (clone,
	// #ref pin, manifest-name + non-empty-payload checks) init's arms use, so a
	// broken personal source prints the byte-identical refusal and writes NOTHING.
	// The validation's SUCCESS chatter ("cached at …") is suppressed to io.Discard:
	// the personal verb owns its own output; the cache line resurfaces later if the
	// apply below runs a bare init. Failure messages go to the real stderr.
	if _, _, code := fetchSource(source, ref, io.Discard, stderr); code != 0 {
		return code
	}

	resolved := source
	if ref != "" {
		resolved = source + "#" + ref // same grammar init's expandSource re-splits
	}

	// Record the setting (GC3 tmp+rename discipline via rewriteFile; mkdir -p the
	// config dir 0o755).
	cfg := personalConfigPath()
	if err := os.MkdirAll(filepath.Dir(cfg), 0o755); err != nil {
		fmt.Fprintf(stderr, "omakase: could not create the personal config dir: %v\n", err)
		return 1
	}
	if err := rewriteFile(cfg, []byte(resolved+"\n")); err != nil {
		fmt.Fprintf(stderr, "omakase: could not write the personal config: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "omakase: personal harness set to %s — layered on every omakase init from now on.\n", resolved)

	// Apply immediately IFF cwd is an initialized repo (design §5). Outside a repo
	// or in an uninitialized cwd: no apply, no extra output.
	wd, err := os.Getwd()
	if err != nil {
		return 0
	}
	repo, err := state.Discover(wd)
	if err != nil {
		return 0 // outside a git repo
	}
	if !fileRegular(filepath.Join(repo.OMK, "placed.tsv")) {
		return 0 // uninitialized cwd
	}
	if state.PersonalOff(state.ReadSources(filepath.Join(repo.OMK, "sources.tsv"))) {
		// A persisted per-repo opt-out (init --no-personal) wins over the fresh
		// global setting here — do NOT apply.
		fmt.Fprintln(stdout, "omakase: this repo has personal layering off (init --no-personal); not applied here.")
		return 0
	}
	// Bare init IS the "re-run the layering engine at the recorded pins" semantics
	// the plan specifies: it re-reads the remembered project source + the personal
	// config just written and re-overlays the full stack (incl. lefthook).
	fmt.Fprintln(stdout, "omakase: applying to this repo now (bare init).")
	return RunInit([]string{}, stdout, stderr)
}

// personalOff clears the global setting and, in an initialized layered repo
// carrying a personal row, unlayers it (plan Task 5 off arm).
func personalOff(stdout, stderr io.Writer) int {
	// Clear globally first (already-absent is fine), unconditionally.
	removeF(personalConfigPath())
	fmt.Fprintln(stdout, "omakase: personal harness cleared.")

	wd, err := os.Getwd()
	if err != nil {
		return 0
	}
	repo, err := state.Discover(wd)
	if err != nil {
		return 0 // not in a repo → global clear only
	}
	omk := repo.OMK
	if !fileRegular(filepath.Join(omk, "placed.tsv")) {
		return 0 // uninitialized cwd → global clear only
	}
	// An initialized repo IS per-repo work: it must have a layer store to unlayer
	// from. A repo that predates layers cannot be unlayered — GC8 refuse (design §9)
	// rather than guess.
	if !isDir(filepath.Join(omk, "layers")) {
		fmt.Fprintln(stderr, "omakase: this repo predates layered state — run omakase init once first")
		return 1
	}

	// Split off the personal row (a real one, NOT the personal|off opt-out sentinel,
	// which off never touches — that is init --no-personal's memory).
	sources := state.ReadSources(filepath.Join(omk, "sources.tsv"))
	var personalRow *state.SourceRow
	var remaining []state.SourceRow
	for i := range sources {
		r := sources[i]
		if r.Layer == "personal" && r.Source != "off" {
			rc := r
			personalRow = &rc
			continue
		}
		remaining = append(remaining, r)
	}
	if personalRow == nil {
		return 0 // layered repo with no personal layer → global clear only
	}

	return personalUnlayer(repo.Root, repo.CommonDir, omk, *personalRow, remaining, stdout, stderr)
}

// personalUnlayer performs the design §4 layer removal for the personal layer:
// restore each personal-won path from the next-lower store, delete the
// sole-personal ones, rebuild the snapshot + placed.tsv + exclude/wtinc to the
// post-unlayer merged view, drop the personal row, and remove the personal
// store. Gracefully tolerates the Task-4 stale seam (a personal row lingering
// after the personal FILES were already swept): no live personal-won rows means
// restored/deleted counts of zero, and RemoveLayerDir tolerates a missing store.
func personalUnlayer(root, common, omk string, personalRow state.SourceRow, remaining []state.SourceRow, stdout, stderr io.Writer) int {
	// The label the personal layer's files carry in placed.tsv column 3 — rebuilt
	// from the sources.tsv row exactly as init built it (source, plus #ref when a
	// ref was pinned).
	personalLabel := personalRow.Source
	if personalRow.Ref != "-" && personalRow.Ref != "" {
		personalLabel = personalRow.Source + "#" + personalRow.Ref
	}

	// The layer directly below personal is project when a project source was
	// installed (base was folded INTO it), else the base store (a personal-over-bare
	// stack builds layers/base too — see init's specs). Exactly one of the two
	// exists.
	lowerLayer := LayerBase
	if isDir(filepath.Join(omk, "layers", string(LayerProject))) {
		lowerLayer = LayerProject
	}
	lowerDir := filepath.Join(omk, "layers", string(lowerLayer))
	lowerFilesDir := filepath.Join(lowerDir, "files")
	// The lower store's placed.tsv IS the post-unlayer merged view: after personal
	// is gone the stack is a SINGLE layer, so its store (labels, hashes, kinds, and
	// lexical order) already equals what a fresh init of just that layer produces.
	lowerRows := state.ReadPlaced(filepath.Join(lowerDir, "placed.tsv"))
	lowerHas := make(map[string]bool, len(lowerRows))
	for _, r := range lowerRows {
		lowerHas[r.Rel] = true
	}

	// Read the LIVE placed.tsv BEFORE rebuilding it — the personal-won set is every
	// live row whose column-3 label is the personal label.
	liveRows := state.ReadPlaced(filepath.Join(omk, "placed.tsv"))

	// ---- rebuild snapshot + placed.tsv to the post-unlayer view, BEFORE any
	// working-tree deletion (design §4 rebuild ordering: a racing post-checkout heal
	// never observes a stale mix). The snapshot becomes exactly the lower store's
	// files — NO personal files remain, so ensure-present can never resurrect one. ----
	snap := filepath.Join(omk, "payload-snapshot")
	if err := os.RemoveAll(snap); err != nil {
		return 1
	}
	if err := os.MkdirAll(snap, 0o755); err != nil {
		return 1
	}
	if isDir(lowerFilesDir) {
		if err := copyTree(lowerFilesDir, snap); err != nil {
			return 1
		}
	}
	if err := state.WritePlaced(filepath.Join(omk, "placed.tsv"), lowerRows); err != nil {
		fmt.Fprintln(stderr, err.Error())
		return 1
	}

	// ---- re-derive the exclude + .worktreeinclude blocks from the post-unlayer
	// placed set, so entries a personal-only path contributed (e.g. CLAUDE.local.md)
	// are dropped. ----
	if code := rewriteExcludeWtinc(root, common, placedRels(lowerRows), stderr); code != 0 {
		return code
	}

	// ---- working-tree unlayer ----
	umask := currentUmask()
	isTracked := func(rel string) bool { return gitTracked(root, rel) }
	restored, deleted := 0, 0
	for _, row := range liveRows {
		if row.Src != personalLabel {
			continue // not personal-won — a lower layer owns it, working tree untouched
		}
		rel := row.Rel
		if isTracked(rel) {
			continue // never touch a tracked path (upstream owns it)
		}
		if lowerHas[rel] {
			// A lower layer ships this dest: re-place its copy (byte-exact restore;
			// placeFile mkdir -p's, refuses a real-dir dest, and chmod +x's a .sh).
			if code := placeFile(filepath.Join(lowerFilesDir, rel), rel, root, umask, stderr); code != 0 {
				return code
			}
			restored++
			continue
		}
		// Sole-personal path: delete under the untracked + hash-match rule (the
		// orphan-sweep discipline); a local edit is warned and kept.
		full := filepath.Join(root, rel)
		if !lexists(full) {
			continue // already gone
		}
		if state.HashOf(full) == row.Hash {
			if err := DeletePlaced(root, rel, isTracked); err != nil {
				return 1
			}
			deleted++
		} else {
			fmt.Fprintf(stderr, "omakase: WARNING — '%s' was placed by your personal layer, has no lower-layer copy to restore, and differs from what omakase placed (a local edit?). Leaving it; delete it yourself if unwanted.\n", rel)
		}
	}

	// ---- sources.tsv: drop the personal row. When nothing remains (a
	// personal-over-base stack), remove the file so the repo matches a base-only
	// install (GC2: base-only has no sources.tsv); otherwise rewrite the survivors. ----
	sourcesPath := filepath.Join(omk, "sources.tsv")
	if len(remaining) == 0 {
		removeF(sourcesPath)
	} else {
		if err := state.WriteSources(sourcesPath, remaining); err != nil {
			fmt.Fprintln(stderr, err.Error())
			return 1
		}
	}

	// ---- remove the personal store (tolerates an already-missing one, healing the
	// stale seam). ----
	if err := RemoveLayerDir(omk, LayerPersonal); err != nil {
		fmt.Fprintln(stderr, err.Error())
		return 1
	}

	fmt.Fprintf(stdout, "omakase: personal layer removed from this repo (restored %d file(s), deleted %d).\n", restored, deleted)
	return 0
}

// placedRels projects a placed-row slice to its rel column (lexical order
// preserved), the input DerivePrefixes and the exclude/wtinc rewrite expect.
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
// the .worktreeinclude-entry skip). Duplicated from init.go rather than shared
// out, to keep this new verb's edits off the byte-frozen init engine; kept in
// lockstep with init's two blocks.
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
