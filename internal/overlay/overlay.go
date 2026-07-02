// Package overlay ports the filesystem-copy, sameness-check, exclude-prefix
// derivation, and placed-path deletion primitives bin/init.sh and
// bin/remove.sh use to place, compare, and tear down the harness overlay.
// Go twin of place_file/same_file/hash_of's symlink handling
// (bin/init.sh:410-443), the exclude-prefix derivation
// (bin/init.sh:527-552,559-563), and delete_placed
// (bin/remove.sh:45-52, the identical loop in bin/init.sh:517-519) —
// DUPLICATED bash<->Go until Phase 2 retires the bash callers; keep in
// lockstep.
package overlay

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// CopyEntry ports `cp -P src dst` as used by every bash overlay call site
// (place_file, the --source merge overlay loop, the clobbered/ backup):
// a symlink source is recreated as a symlink with the identical target
// string (never dereferenced); a regular-file source is byte-copied with
// the source's permission bits carried onto the destination (masked by the
// process umask on creation, matching plain `open(..., O_CREAT, mode)` —
// verified against the live macOS `cp -P`, see overlay_test.go).
//
// Divergence from raw `cp -P`, by design: CopyEntry unconditionally removes
// an existing destination first, for BOTH source kinds. Real `cp -P`
// instead WRITES THROUGH an existing destination symlink when the source is
// a regular file (it follows the link and clobbers the link's target
// in-place) — the exact hazard bin/init.sh:433-441 documents as its reason
// for calling `rm -f` before every `cp -P`. Since every current and planned
// Go caller (the ported place_file, the merge loop, the clobbered/ backup)
// already removes the destination before calling CopyEntry — mirroring
// their bash originals — this divergence is unreachable through any real
// call path; CopyEntry doing its own unlink just makes that safe by
// construction instead of relying on every caller to remember it.
//
// Like real `cp -P`, CopyEntry does NOT create dst's parent directory —
// that is a separate `mkdir -p` step on the bash side (bin/init.sh:424,
// place_file) and remains the caller's job here too.
func CopyEntry(src, dst string) error {
	info, err := os.Lstat(src)
	if err != nil {
		return fmt.Errorf("overlay: CopyEntry: stat source %q: %w", src, err)
	}

	if info.Mode()&os.ModeSymlink != 0 {
		target, err := os.Readlink(src)
		if err != nil {
			return fmt.Errorf("overlay: CopyEntry: readlink %q: %w", src, err)
		}
		if err := os.Remove(dst); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("overlay: CopyEntry: removing existing dest %q: %w", dst, err)
		}
		if err := os.Symlink(target, dst); err != nil {
			return fmt.Errorf("overlay: CopyEntry: symlink %q -> %q: %w", dst, target, err)
		}
		return nil
	}

	if info.IsDir() {
		return fmt.Errorf("overlay: CopyEntry: source %q is a directory", src)
	}

	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("overlay: CopyEntry: open source %q: %w", src, err)
	}
	defer in.Close()

	if err := os.Remove(dst); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("overlay: CopyEntry: removing existing dest %q: %w", dst, err)
	}
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, info.Mode().Perm())
	if err != nil {
		return fmt.Errorf("overlay: CopyEntry: create dest %q: %w", dst, err)
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return fmt.Errorf("overlay: CopyEntry: copying %q -> %q: %w", src, dst, err)
	}
	return out.Close()
}

// SameFile ports same_file (bin/init.sh:416-422): true iff a and b count as
// identical for the "leave it, it already matches the payload" check.
//   - If EITHER path is a symlink, compare their readlink TARGET STRINGS —
//     never the dereferenced content. A path that does not exist, or is not
//     itself a symlink, contributes "" (matching `readlink 2>/dev/null`).
//   - Otherwise, byte-compare the two files' content.
//   - A directory operand (non-symlink) is always "not same" — bash's `cmp`
//     refuses on a directory (bin/init.sh's comment above same_file notes
//     `cmp` emits "Is a directory" to stderr even with -s); here that
//     becomes a plain `false`, no error surfaced.
func SameFile(a, b string) bool {
	aInfo, aErr := os.Lstat(a)
	bInfo, bErr := os.Lstat(b)
	aIsLink := aErr == nil && aInfo.Mode()&os.ModeSymlink != 0
	bIsLink := bErr == nil && bInfo.Mode()&os.ModeSymlink != 0

	if aIsLink || bIsLink {
		aTarget, _ := os.Readlink(a) // error (missing / not a symlink) -> ""
		bTarget, _ := os.Readlink(b)
		return aTarget == bTarget
	}

	return filesEqual(a, b)
}

func filesEqual(a, b string) bool {
	aInfo, err := os.Stat(a)
	if err != nil || aInfo.IsDir() {
		return false
	}
	bInfo, err := os.Stat(b)
	if err != nil || bInfo.IsDir() {
		return false
	}
	if aInfo.Size() != bInfo.Size() {
		return false
	}
	ac, err := os.ReadFile(a)
	if err != nil {
		return false
	}
	bc, err := os.ReadFile(b)
	if err != nil {
		return false
	}
	return string(ac) == string(bc)
}

// DerivePrefixes computes the deduped exclude-block prefix list — the
// frozen derivation from bin/init.sh:527-552 (accumulation via add_prefix)
// plus the trailing-slash suffixing applied when the block is written
// (bin/init.sh:559-563). Go twin — DUPLICATED bash<->Go until Phase 2
// retires the bash callers; keep in lockstep.
//
// Order, first-seen deduped across the whole run:
//  1. For each entry in placed (empty strings skipped): if its top-level
//     segment (up to, not including, the first "/"; the whole string if it
//     has no "/") is listed in sharedTopdirs, the FULL placed path is added
//     (a shared dir like ".github" is excluded file-by-file, never
//     wholesale, so omakase never hides the project's own untracked files
//     under it); otherwise the bare top-level segment is added (an
//     omakase-owned dir is excluded wholesale).
//  2. "lefthook.yml", unless lefthookTracked.
//  3. ".worktreeinclude", unless wtincTracked.
//
// Every resulting entry is then suffixed with a trailing "/" iff isDir
// reports true for it — isDir receives the bare entry string (e.g.
// ".claude" or ".github/workflows/ci.yml") and is expected to resolve it
// against the live repo root itself (bash checks `[ -d "$ROOT/$p" ]`);
// DerivePrefixes has no notion of a repo root.
func DerivePrefixes(placed []string, sharedTopdirs []string, isDir func(string) bool, lefthookTracked, wtincTracked bool) []string {
	var entries []string
	seen := make(map[string]bool)
	add := func(p string) {
		if seen[p] {
			return
		}
		seen[p] = true
		entries = append(entries, p)
	}

	isShared := func(topdir string) bool {
		for _, d := range sharedTopdirs {
			if d == topdir {
				return true
			}
		}
		return false
	}

	for _, rel := range placed {
		if rel == "" {
			continue
		}
		top := rel
		if idx := strings.IndexByte(rel, '/'); idx >= 0 {
			top = rel[:idx]
		}
		if isShared(top) {
			add(rel)
		} else {
			add(top)
		}
	}
	if !lefthookTracked {
		add("lefthook.yml")
	}
	if !wtincTracked {
		add(".worktreeinclude")
	}

	out := make([]string, len(entries))
	for i, p := range entries {
		if isDir(p) {
			out[i] = p + "/"
		} else {
			out[i] = p
		}
	}
	return out
}

// rewriteFile ports the bash idiom every marked-block rewrite in
// bin/init.sh and bin/remove.sh uses: `awk ... > "$f.tmp" && mv "$f.tmp" "$f"`.
// That shell redirection creates $f.tmp FRESH via open(..., O_CREAT|O_TRUNC,
// 0666), masked by the process umask AT CREATION TIME — never by $f's
// pre-existing mode — and `mv` (a rename) then replaces $f's inode wholesale
// with that fresh one. So after a bash rewrite, the file's mode is ALWAYS
// `0666 &^ umask`, regardless of what mode it had going in (e.g. a 0640 hook
// becomes 0644 under umask 022 — confirmed against a live `awk '{print}' f >
// f.tmp && mv f.tmp f` run).
//
// os.WriteFile over an EXISTING path does NOT reproduce this: its mode
// argument only applies when the file is created, so writing over an
// existing file silently preserves whatever mode that file already had.
// Every in-place marked-block rewrite site in init.go/remove.go must go
// through rewriteFile instead of os.WriteFile to match bash's inode
// replacement exactly.
//
// Like bash's own `&&` short-circuit, a write failure leaves the ".tmp"
// file behind and the original path untouched (no cleanup) — the caller
// aborts the run either way (matching bash's `set -e` behavior when this
// idiom's last command, the `mv`, never runs).
func rewriteFile(path string, content []byte) error {
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o666)
	if err != nil {
		return fmt.Errorf("overlay: rewriteFile: create %q: %w", tmp, err)
	}
	if _, err := f.Write(content); err != nil {
		f.Close()
		return fmt.Errorf("overlay: rewriteFile: write %q: %w", tmp, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("overlay: rewriteFile: close %q: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("overlay: rewriteFile: rename %q -> %q: %w", tmp, path, err)
	}
	return nil
}

// DeletePlaced ports delete_placed (bin/remove.sh:45-52; the identical
// pruning loop appears in the orphan sweep at bin/init.sh:517-519): a
// tracked path is skipped silently — git, not omakase, owns it — otherwise
// the file at root/rel is removed (rm -f semantics: a missing file is not
// an error) and each now-empty parent directory is pruned upward, stopping
// at "." (the rel has no more path segments — the repo root is never
// removed) or the first directory that is missing or still non-empty.
func DeletePlaced(root, rel string, isTracked func(string) bool) error {
	if isTracked(rel) {
		return nil
	}

	full := filepath.Join(root, rel)
	if err := os.Remove(full); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("overlay: DeletePlaced: removing %q: %w", full, err)
	}

	for d := filepath.Dir(rel); d != "."; d = filepath.Dir(d) {
		dirFull := filepath.Join(root, d)
		info, err := os.Stat(dirFull)
		if err != nil || !info.IsDir() {
			break
		}
		entries, err := os.ReadDir(dirFull)
		if err != nil || len(entries) != 0 {
			break
		}
		if err := os.Remove(dirFull); err != nil {
			return fmt.Errorf("overlay: DeletePlaced: pruning empty dir %q: %w", dirFull, err)
		}
	}
	return nil
}
