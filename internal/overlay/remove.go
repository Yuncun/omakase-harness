// This file (remove.go) ports the `omakase remove` verb — the engine of
// bin/remove.sh: the reverse of init. In order: payload default resolution,
// repo discovery, the lefthook uninstall (never fetching), the hook-stub
// marked-block strip, the placed-path deletion (ledger-driven, or the
// pre-0.10 payload-enumeration fallback behind an install-proof sentinel),
// the skeleton lefthook.yml and .worktreeinclude teardown, the $OMK
// (worktree harness snapshot) wipe, the exclude-block strip, and the
// closing summary line. It reproduces bin/remove.sh's stdout/stderr bytes,
// exit codes, side-effect ordering, and on-disk state.
//
// bin/remove.sh STAYS bash, untouched: the frozen v1 suite still runs
// through it. This Go verb goes live only at Task 6's shim cutover;
// remove_test.go is this task's safety net.
package overlay

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/Yuncun/omakase-harness/internal/lefthook"
	"github.com/Yuncun/omakase-harness/internal/state"
	"github.com/Yuncun/omakase-harness/internal/textblock"
)

// RunRemove is the `omakase remove` verb. bin/remove.sh has no arg-parsing
// at all — every token after the verb is simply never inspected — so argv
// is accepted only to match the shape every other verb's Run function has
// (Global Constraint 2) and is otherwise ignored entirely. It writes to
// stdout/stderr and returns the process exit code: 1 for a not-a-repo
// environment error, 0 on success including the "nothing installed" no-op
// (remove.sh:67-69).
func RunRemove(argv []string, stdout, stderr io.Writer) int {
	// ---- payload default/normalize (remove.sh:8-9) ----
	// The identical rule to init's plain-install default (Task 4):
	// OMAKASE_PAYLOAD overrides; otherwise defaultPayload — OMAKASE_BASE_PAYLOAD
	// (the shim handoff), else the binary-relative ../payload. Unlike init,
	// remove does NOT validate up front that the
	// payload dir exists — it is read only in the pre-0.10 enumeration
	// fallback below, and a missing dir there simply enumerates nothing (see
	// that step's comment).
	payload := os.Getenv("OMAKASE_PAYLOAD")
	if payload == "" {
		payload = defaultPayload()
	}
	payload = strings.TrimSuffix(payload, "/")

	// ---- repo discovery (remove.sh:10) ----
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
	isTracked := func(rel string) bool { return gitTracked(root, rel) }

	const begin = "# >>> omakase-harness >>>"
	const end = "# <<< omakase-harness <<<"
	// The exclude file + hooks dir live in the SHARED git dir, so a linked
	// worktree ($ROOT/.git is a FILE there) resolves correctly
	// (remove.sh:14, matching init.sh:246-250).
	exclude := filepath.Join(common, "info", "exclude")
	hooksDir := filepath.Join(common, "hooks")

	// ---- lefthook uninstall, never fetching (remove.sh:16-22) ----
	// ResolveForRemove walks the same tiers init uses, minus self-fetch, and
	// is silent on failure (no Guidance call here — remove.sh has none on
	// this path either): the $OMK teardown below already neutralizes the
	// fail-closed guard, and the hook stubs are reversed regardless.
	if prefix, ok := lefthook.ResolveForRemove(root); ok {
		args := append(append([]string{}, prefix...), "uninstall")
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = root
		cmd.Stdout = stdout
		cmd.Stderr = stderr
		cmd.Run() // `|| true` (remove.sh:22): failure ignored entirely, no exit-code propagation
		// Accepted divergence: a LEFTHOOK_BIN override naming a nonexistent or
		// non-executable path still resolves (tier 1 trusts it unchecked, same
		// as bash's own tier 1), so the exec itself can fail to even start; bash's
		// subshell then leaks an OS/bash-version-specific diagnostic line to
		// stderr before `|| true` swallows the exit status, while Go's failed
		// exec.Command produces no stderr text at all (the error is only a Go
		// return value here, discarded). Global Constraint 9 territory — the
		// message text is not something this port pins byte-exact; "ignore
		// failure" (the brief's own words) is met either way.
	}

	// ---- hook-stub marked-block strip (remove.sh:24-37) ----
	umask := currentUmask()
	for _, hf := range sortedHookFiles(hooksDir) {
		if !fileRegular(hf) {
			continue
		}
		changed := false
		for _, pair := range []string{"omakase-harness fail-closed", "omakase-harness worktree-bootstrap"} {
			gb := "# >>> " + pair + " >>>"
			ge := "# <<< " + pair + " <<<"
			// Read fresh each pair: pair 1's rewrite (if it fires) changes
			// what pair 2's gate/strip sees, exactly as bash re-runs grep/awk
			// against the file it just rewrote.
			content, rerr := os.ReadFile(hf)
			if rerr != nil {
				continue // grep -qF on an unreadable file fails -> `|| continue`
			}
			// The gate is a SUBSTRING test anywhere in the file (grep -qF),
			// deliberately looser than the strip itself (textblock.Strip only
			// drops lines EQUAL to the marker, whole-line). A begin marker
			// that appears only as a mid-line substring still fires this gate
			// and still runs the strip (a no-op on content, since no line
			// equals gb) and still marks `changed` — byte-matching
			// remove.sh's own quirk here.
			if !bytes.Contains(content, []byte(gb)) {
				continue
			}
			out := textblock.Strip(content, gb, ge)
			// rewriteFile, not os.WriteFile: reproduces bash's `awk ... >
			// tmp && mv tmp hf` inode replacement (a fresh 0666&^umask
			// base), which the trailing chmod +x below then adds exec bits
			// onto — matching bash byte-for-byte even when hf's PRE-existing
			// mode differs from 0666&^umask (e.g. a 0640 hook).
			if werr := rewriteFile(hf, out); werr != nil {
				return 1
			}
			changed = true
		}
		if changed {
			// chmod +x: symbolic +x math (add exec bits masked by umask) —
			// restores the bit bash's `awk ... > tmp && mv tmp file` dance
			// loses (mv's source inode carries the temp file's own
			// umask-derived permissions, not hf's original ones), exactly
			// mirroring place_file's chmod (init.sh:441-442 / placeFile).
			if info, serr := os.Stat(hf); serr == nil {
				os.Chmod(hf, info.Mode().Perm()|(0o111&^umask))
			}
		}
	}

	// ---- placed deletion (remove.sh:39-70) ----
	// The provenance ledger (placed.tsv) is authoritative when present: ALL
	// its rows are deleted, enabled or not (remove is a total teardown; the
	// enabled flag is an off switch, not an uninstall) — DeletePlaced itself
	// carries no notion of "enabled" at all. FILE ORDER, not walk order
	// (Global Constraint 6 exempts this loop — it iterates existing state,
	// not the payload). state.ReadPlaced already drops rows with an empty
	// Rel and processes a final unterminated row, matching the bash loop's
	// `[ -z "$rel" ] && continue` and `|| [ -n "$rel" ]` respectively (see
	// its doc comment).
	ledger := filepath.Join(omk, "placed.tsv")
	if fileRegular(ledger) {
		for _, row := range state.ReadPlaced(ledger) {
			if delErr := DeletePlaced(root, row.Rel, isTracked); delErr != nil {
				return 1
			}
		}
	} else if fileContains(exclude, begin) || isDir(omk) {
		// No ledger (a pre-0.10 install) but omakase WAS installed here — the
		// install-proof sentinel (the exclude block, or a leftover snapshot
		// dir) is REQUIRED before falling back to enumerating the payload:
		// without it this would delete a plain repo's own untracked files
		// merely sharing a payload filename. WALK order (Global Constraint 6,
		// the one sanctioned divergence — v1 uses find's readdir order). A
		// missing/unreadable PAYLOAD enumerates nothing here, matching find's
		// silent (to the loop) failure on a bad start path; bash's find still
		// writes its own diagnostic to the process's real stderr through the
		// process substitution — an accepted, unreachable-in-production
		// divergence (PAYLOAD defaults to the harness's own bundled payload/,
		// which always exists; only a deliberately broken OMAKASE_PAYLOAD
		// override reaches this).
		rels, _ := walkPayload(payload)
		for _, rel := range rels {
			if delErr := DeletePlaced(root, rel, isTracked); delErr != nil {
				return 1
			}
		}
	} else {
		fmt.Fprintln(stderr, "omakase: nothing installed here; nothing to remove.")
		return 0
	}

	// ---- skeleton lefthook.yml removal (remove.sh:72-75) ----
	lefthookYml := filepath.Join(root, "lefthook.yml")
	if fileRegular(lefthookYml) && !isTracked("lefthook.yml") && fileContains(lefthookYml, "EXAMPLE USAGE") {
		// set -e: bash's own `rm -f` failure here would abort the script;
		// propagate rather than discard (removeF only fails on a REAL
		// removal error, never a missing file — rm -f semantics).
		if err := removeF(lefthookYml); err != nil {
			return 1
		}
	}

	// ---- .worktreeinclude strip (remove.sh:77-82) ----
	// No gate here (unlike the hook stubs): the block is stripped
	// unconditionally whenever the file exists and is untracked — a no-op
	// content-wise if the markers are absent, but still rewritten (matching
	// awk's ORS behavior of always \n-terminating a printed final line, per
	// textblock.Strip's doc comment).
	wtinc := filepath.Join(root, ".worktreeinclude")
	if fileRegular(wtinc) && !isTracked(".worktreeinclude") {
		content, _ := os.ReadFile(wtinc)
		out := textblock.Strip(content, begin, end)
		if werr := rewriteFile(wtinc, out); werr != nil {
			return 1
		}
		if len(out) == 0 { // `[ -s ]` false: zero bytes
			// set -e: bash's own `rm -f` failure here would abort the
			// script; propagate rather than discard.
			if err := removeF(wtinc); err != nil {
				return 1
			}
		}
	}

	// ---- tear down the worktree harness snapshot (remove.sh:85) ----
	if err := os.RemoveAll(omk); err != nil {
		return 1
	}

	// ---- exclude strip (remove.sh:87-90) ----
	// Unlike wtinc, the exclude file is never deleted even if stripping
	// leaves it empty — remove.sh has no `[ -s ]` check on this one.
	if fileRegular(exclude) {
		content, _ := os.ReadFile(exclude)
		out := textblock.Strip(content, begin, end)
		if werr := rewriteFile(exclude, out); werr != nil {
			return 1
		}
	}

	fmt.Fprintln(stdout, "omakase: removed. Hooks uninstalled, placed files deleted, worktree snapshot + exclude block stripped.")
	return 0
}

// fileContains collapses remove.sh's two grep gates to a single whole-file
// substring test: `grep -qF "$BEGIN" "$EXCLUDE"` (remove.sh:59, fixed-string
// by flag) and `grep -q "EXAMPLE USAGE" "$ROOT/lefthook.yml"` (remove.sh:74,
// BRE by default — but "EXAMPLE USAGE" has no BRE metacharacters, so it
// matches exactly the same literal substring a fixed-string search would).
// Since neither substr contains a newline, a plain bytes.Contains over the
// whole content agrees with either line-oriented grep on every input — grep
// answers true iff SOME line contains substr, which holds iff substr occurs
// anywhere in the byte stream when substr itself cannot span a line
// boundary. A missing or unreadable path is "not found" (matching `2>/dev/null`).
func fileContains(path, substr string) bool {
	content, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	return bytes.Contains(content, []byte(substr))
}
