// This file implements the `omakase remove` verb, the reverse of init. In
// order: payload default resolution, repo discovery, the hook teardown — a
// dispatcher is deleted only when byte-equal to what init writes, a foreign
// hook is reported and never deleted, and a pre-#98 install additionally
// gets its guard-block strip — the per-worktree sweep — placed-path deletion
// (ledger-driven, or the pre-0.10 payload-enumeration fallback behind an
// install-proof sentinel) plus the skeleton lefthook.yml and .worktreeinclude
// teardown, applied to every worktree git lists — the $OMK wipe, the
// exclude-block strip, and the closing summary line.
package overlay

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/Yuncun/omakase-harness/internal/hook"
	"github.com/Yuncun/omakase-harness/internal/state"
	"github.com/Yuncun/omakase-harness/internal/textblock"
)

// RunRemove is the `omakase remove` verb. It takes no flags; argv is
// accepted only to match the other verbs' Run shape and is ignored. It
// returns 1 for a not-a-repo environment error, 0 on success including the
// "nothing installed" no-op.
func RunRemove(argv []string, stdout, stderr io.Writer) int {
	// ---- payload default/normalize ----
	// The same rule as init's plain-install default: OMAKASE_PAYLOAD
	// overrides; otherwise defaultPayload. Unlike init, remove does not
	// validate that the payload dir exists — it is read only in the
	// pre-0.10 enumeration fallback below, where a missing dir enumerates
	// nothing.
	payload := os.Getenv("OMAKASE_PAYLOAD")
	if payload == "" {
		payload = defaultPayload()
	}
	payload = strings.TrimSuffix(payload, "/")

	// ---- repo discovery ----
	wd, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(stderr, "omakase: not inside a git repo")
		return 1
	}
	repo, err := state.Discover(wd)
	if err != nil {
		fmt.Fprintln(stderr, "omakase: not inside a git repo")
		return 1
	}
	root := repo.Root
	common := repo.CommonDir
	omk := repo.OMK

	const begin = "# >>> omakase-harness >>>"
	const end = "# <<< omakase-harness <<<"
	// The exclude file and hooks dir live in the shared git dir, so a linked
	// worktree (where $ROOT/.git is a file) resolves correctly.
	exclude := filepath.Join(common, "info", "exclude")
	hooksDir := filepath.Join(common, "hooks")

	// ---- pre-#98 hook teardown ----
	// A repo last initialized under the old scheme carries hook stubs with
	// omakase's guard blocks spliced in. The guard-block strip below
	// de-omakases any such stub; omakase no longer bundles a runner, so there
	// is nothing to `uninstall`. A project's own hooks carry no omakase markers
	// and are left untouched.
	umask := currentUmask()
	for _, hf := range sortedHookFiles(hooksDir) {
		if !fileRegular(hf) {
			continue
		}
		changed := false
		for _, pair := range []string{"omakase-harness fail-closed", "omakase-harness worktree-bootstrap"} {
			gb := "# >>> " + pair + " >>>"
			ge := "# <<< " + pair + " <<<"
			// Read fresh for each pair: pair 1's rewrite changes what pair
			// 2's gate and strip see.
			content, rerr := os.ReadFile(hf)
			if rerr != nil {
				continue // an unreadable file is skipped
			}
			// The gate is a whole-file substring test, looser than the
			// strip itself (textblock.Strip only drops lines equal to the
			// marker): a marker appearing only mid-line still fires the
			// gate, runs a no-op strip, and marks the file changed.
			if !bytes.Contains(content, []byte(gb)) {
				continue
			}
			out := textblock.Strip(content, gb, ge)
			// rewriteFile replaces the inode, dropping any exec bits; the
			// chmod below re-adds them.
			if werr := rewriteFile(hf, out); werr != nil {
				return 1
			}
			changed = true
		}
		if changed {
			// Re-add the exec bits the rewrite dropped, masked by umask.
			if info, serr := os.Stat(hf); serr == nil {
				os.Chmod(hf, info.Mode().Perm()|(0o111&^umask))
			}
		}
	}

	// ---- dispatcher deletion (issue #98) ----
	// remove is the only deleter of .git/hooks files, and it deletes a hook
	// only after proving the content is byte-equal to the dispatcher init
	// writes for that name. Anything else at those names was written by
	// another tool — reported, never deleted.
	for _, name := range hook.Names() {
		hf := filepath.Join(hooksDir, name)
		if !lexists(hf) {
			continue
		}
		if hook.Matches(hf, name) {
			if err := removeF(hf); err != nil {
				return 1
			}
			continue
		}
		fmt.Fprintf(stderr, "omakase: NOTE — %s is not omakase's dispatcher (another tool wrote it); left in place.\n", hf)
	}

	// ---- placed-path list ----
	// The provenance ledger (placed.tsv) is authoritative when present: all
	// its rows are deleted, enabled or not — remove is a total teardown; the
	// enabled flag is an off switch, not an uninstall. Rows are processed
	// in file order.
	var rels []string
	ledger := filepath.Join(omk, "placed.tsv")
	if fileRegular(ledger) {
		for _, row := range state.ReadPlaced(ledger) {
			rels = append(rels, row.Rel)
		}
	} else if fileContains(exclude, begin) || isDir(omk) {
		// No ledger (a pre-0.10 install) but omakase was installed here. The
		// install-proof sentinel (the exclude block, or a leftover snapshot
		// dir) is required before falling back to enumerating the payload:
		// without it this would delete a plain repo's own untracked files
		// merely sharing a payload filename. A missing or unreadable payload
		// enumerates nothing — reachable in production since v0.18.0, when a
		// fetched/PATH binary runs without the shim-exported
		// OMAKASE_BASE_PAYLOAD and has no bundled payload/ sibling.
		rels, _ = walkPayload(payload)
	} else {
		fmt.Fprintln(stderr, "omakase: nothing installed here; nothing to remove.")
		return 0
	}

	// A kept file is the USER'S content (they accepted their own edit —
	// issue #98 Part 2), so remove leaves it on disk and says so, the same
	// spirit as the clobber backup: never destroy a user's content. The
	// $OMK teardown below still deletes the kept/ copies with everything
	// else — the on-disk file is the surviving original.
	kept := map[string]bool{}
	for _, rel := range rels {
		if lexists(filepath.Join(omk, "kept", rel)) {
			kept[rel] = true
		}
	}

	// ---- per-worktree sweep ----
	// Placed files, the skeleton lefthook.yml, and .worktreeinclude live
	// per-checkout — heal copies them into every worktree — so remove sweeps
	// every worktree git lists, not just the one it runs from; otherwise
	// siblings keep orphaned copies that the exclude block, stripped below,
	// no longer ignores. Trackedness is re-checked against each worktree's
	// own checkout. An unreachable worktree gets a warning and is skipped;
	// the shared-state teardown below still covers it.
	for _, wtRoot := range state.WorktreeRoots(root) {
		if !isDir(wtRoot) {
			fmt.Fprintf(stderr, "omakase: worktree %s is unreachable; its placed files were not removed.\n", wtRoot)
			continue
		}
		wtTracked := func(rel string) bool { return gitTracked(wtRoot, rel) }

		// placed deletion (kept files are the user's; left in place)
		for _, rel := range rels {
			if kept[rel] {
				continue
			}
			if delErr := DeletePlaced(wtRoot, rel, wtTracked); delErr != nil {
				return 1
			}
		}

		// skeleton lefthook.yml removal
		lefthookYml := filepath.Join(wtRoot, "lefthook.yml")
		if fileRegular(lefthookYml) && !wtTracked("lefthook.yml") && fileContains(lefthookYml, "EXAMPLE USAGE") {
			if err := removeF(lefthookYml); err != nil {
				return 1
			}
		}

		// .worktreeinclude strip. No gate here (unlike the hook stubs): the
		// block is stripped unconditionally whenever the file exists and is
		// untracked — a no-op content-wise if the markers are absent, but
		// still rewritten.
		wtinc := filepath.Join(wtRoot, ".worktreeinclude")
		if fileRegular(wtinc) && !wtTracked(".worktreeinclude") {
			content, _ := os.ReadFile(wtinc)
			out := textblock.Strip(content, begin, end)
			if werr := rewriteFile(wtinc, out); werr != nil {
				return 1
			}
			if len(out) == 0 { // empty after the strip: delete it
				if err := removeF(wtinc); err != nil {
					return 1
				}
			}
		}
	}

	// ---- tear down the worktree harness snapshot ----
	if err := os.RemoveAll(omk); err != nil {
		return 1
	}

	// ---- exclude strip ----
	// Unlike wtinc, the exclude file is never deleted even if stripping
	// leaves it empty.
	if fileRegular(exclude) {
		content, _ := os.ReadFile(exclude)
		out := textblock.Strip(content, begin, end)
		if werr := rewriteFile(exclude, out); werr != nil {
			return 1
		}
	}

	for _, rel := range rels {
		if kept[rel] {
			fmt.Fprintf(stdout, "omakase: %s is yours (kept) — left on disk; with the ignore rules gone, git now sees it as an untracked file.\n", rel)
		}
	}
	fmt.Fprintln(stdout, "omakase: removed. Hooks uninstalled, placed files deleted, worktree snapshot + exclude block stripped.")
	return 0
}

// fileContains reports whether the file's content contains substr. A
// missing or unreadable path is "not found".
func fileContains(path, substr string) bool {
	content, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	return bytes.Contains(content, []byte(substr))
}
