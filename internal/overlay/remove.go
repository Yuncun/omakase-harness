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

// RunRemove is the `omakase remove` verb, now `remove [<source>]` (Phase 3.5).
// A BARE `remove` is the v1 total teardown — the reverse of init, byte-identical
// to bin/remove.sh (GC1); its every token was historically ignored, and the arg
// parse below preserves that no-op shape only for zero args. A `remove <source>`
// UNLAYERS one harness from a two-source stack (RemoveLayer), or — with just one
// source installed — is that source's total teardown (the decided edge case).
// Returns the process exit code: 2 for a usage error, 1 for a refusal / not-a-repo
// environment error, 0 on success including the "nothing installed" no-op
// (remove.sh:67-69).
func RunRemove(argv []string, stdout, stderr io.Writer) int {
	// ---- arg parse: remove [<source>] ----
	// A single optional positional (the source to unlayer). -h/--help prints the
	// usage; any other flag, or a second positional, is a usage error (exit 2).
	source := ""
	for i := 0; i < len(argv); i++ {
		a := argv[i]
		switch {
		case a == "-h" || a == "--help":
			fmt.Fprint(stdout, removeUsageText)
			return 0
		case strings.HasPrefix(a, "-"):
			fmt.Fprint(stderr, removeUsageText)
			return 2
		default:
			if source != "" {
				fmt.Fprint(stderr, removeUsageText)
				return 2
			}
			source = a
		}
	}

	// ---- payload default/normalize (remove.sh:8-9) ----
	// The identical rule to init's plain-install default (Task 4):
	// OMAKASE_PAYLOAD overrides; otherwise the binary-relative ../payload
	// default. Unlike init, remove does NOT validate up front that the
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

	// ---- remove <source>: unlayer one harness (design §4 layer removal, Phase 3.5) ----
	// A named source unlayers just that harness. Guarded by RequireLayers (a
	// pre-layers v1 repo has no store to restore from — GC8 refuse, not guess). No
	// match → the GC5 not-installed line. One source recorded and it matched → the
	// total teardown below (the decided edge case, byte-identical to a bare remove).
	// Two sources → RemoveLayer, which returns here. Offline throughout (GC10).
	if source != "" {
		recorded := EnsureSources(omk, stderr)
		if !RequireLayers(omk, stderr) {
			return 1
		}
		idx := matchRecorded(recorded, source)
		if idx < 0 {
			fmt.Fprintf(stderr, "omakase: no harness '%s' installed here (installed: %s)\n", source, installedLabels(recorded))
			return 1
		}
		if len(recorded) >= 2 {
			return RemoveLayer(root, common, omk, recorded, idx, stdout, stderr)
		}
		// Exactly one source, and it matched: fall through to the total-teardown
		// path below (bin/remove.sh's own bytes — no third state invented).
	}

	// v1→v2 migration for uniformity (design §9): EnsureSources synthesizes
	// sources.tsv (and warns on mixed-era) on the first v2 run when $OMK exists. It
	// is a silent no-op when $OMK is absent, so the "nothing installed here"
	// early-out below stays byte-identical; when $OMK IS present, remove wipes it
	// wholesale moments later (os.RemoveAll below), so this write is momentary. The
	// return value is unused — remove reads no layer stack, it just tears down.
	EnsureSources(omk, stderr)

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

// removeUsageText is the byte-exact usage for `remove [<source>]` (Phase 3.5).
// bin/remove.sh had none; this Go verb goes live at Task 6's shim cutover.
const removeUsageText = "usage: omakase remove [<source>]\n" +
	"\n" +
	"  (no argument)  remove the whole omakase harness from this repo (uninstall).\n" +
	"  <source>       remove just that harness, restoring what it overrode.\n"

// matchRecorded returns the index of the recorded stack row the arg names — by
// exact source string, exact display label (source#ref), or the shorthand-
// expanded form of either (the same expandSource init matches a source arg
// through) — or -1 if none match.
func matchRecorded(recorded []state.SourceRow, arg string) int {
	exp, ref := expandSource(arg)
	wantLabel := displayLabel(exp, ref)
	for i := range recorded {
		r := recorded[i]
		if arg == r.Source || arg == reassembleSource(r) || exp == r.Source || wantLabel == reassembleSource(r) {
			return i
		}
	}
	return -1
}

// installedLabels renders the recorded stack's display labels, comma-separated,
// for the GC5 not-installed line's "(installed: …)" list.
func installedLabels(recorded []state.SourceRow) string {
	labels := make([]string, 0, len(recorded))
	for _, r := range recorded {
		labels = append(labels, reassembleSource(r))
	}
	return strings.Join(labels, ", ")
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
