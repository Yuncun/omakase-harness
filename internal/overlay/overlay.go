// Package overlay implements the init, remove, and toggle verbs and the
// filesystem primitives they share to place, compare, and tear down the
// harness overlay.
package overlay

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// CopyEntry copies one payload entry to dst. A symlink source is recreated
// as a symlink with the identical target string, never dereferenced; a
// regular-file source is byte-copied with the source's permission bits
// (masked by the process umask on creation). An existing destination is
// removed first, so a regular-file copy can never write through a
// destination symlink. dst's parent directory must already exist.
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

// safeMkdirAll creates dir (like os.MkdirAll, 0o755) but refuses to follow
// a symlink planted as any path component below root: it lstat-walks every
// component from root down and errors on the first existing one that is a
// symlink. A merged payload can supply a directory symlink from one layer
// and a child file from another; following the symlink would let the copy
// write outside root.
//
// root is the boundary the copy must stay within and is assumed to exist;
// only components strictly under it are checked, so a root path that itself
// contains symlinks (e.g. /var -> /private/var) is fine. dir must be root
// or a descendant of root.
func safeMkdirAll(root, dir string) error {
	root = filepath.Clean(root)
	dir = filepath.Clean(dir)

	rel, err := filepath.Rel(root, dir)
	if err != nil {
		return fmt.Errorf("overlay: safeMkdirAll: %q is not under root %q: %w", dir, root, err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("overlay: safeMkdirAll: %q escapes root %q", dir, root)
	}

	if rel != "." {
		cur := root
		for _, seg := range strings.Split(rel, string(filepath.Separator)) {
			cur = filepath.Join(cur, seg)
			info, lerr := os.Lstat(cur)
			if lerr != nil {
				// From here down nothing exists (or the path is unreadable); no
				// existing component can be a symlink, so os.MkdirAll below
				// creates real directories the rest of the way (or fails cleanly).
				break
			}
			if info.Mode()&os.ModeSymlink != 0 {
				return fmt.Errorf("overlay: safeMkdirAll: refusing to create %q: path component %q is a symlink — a harness payload placed a directory symlink here and following it would write outside %q", dir, cur, root)
			}
		}
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("overlay: safeMkdirAll: creating %q: %w", dir, err)
	}
	return nil
}

// SameFile reports whether a and b count as identical for the "leave it, it
// already matches the payload" check. If either path is a symlink, their
// readlink target strings are compared, never the dereferenced content; a
// path that is missing or not a symlink contributes "". Otherwise the two
// files' bytes are compared, and a directory operand is never same.
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

// DerivePrefixes computes the deduped exclude-block prefix list, first-seen
// order:
//  1. For each entry in placed (empty strings skipped): if its top-level
//     segment is listed in sharedTopdirs, the full placed path is added (a
//     shared dir like ".github" is excluded file-by-file, never wholesale);
//     otherwise the bare top-level segment is added (an omakase-owned dir
//     is excluded wholesale).
//  2. ".worktreeinclude", unless wtincTracked.
//
// Each entry is then suffixed with a trailing "/" iff isDir reports true
// for it. isDir receives the bare entry string and resolves it against the
// repo root itself; DerivePrefixes has no notion of a repo root.
func DerivePrefixes(placed []string, sharedTopdirs []string, isDir func(string) bool, wtincTracked bool) []string {
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

// rewriteFile replaces path wholesale: content is written to a fresh
// path+".tmp" (created 0o666, masked by the process umask) and renamed over
// path, so the old inode is gone and the result's mode is 0666 &^ umask
// regardless of the old mode. os.WriteFile over an existing path would
// instead preserve its mode; marked-block rewrites go through rewriteFile
// so the two stay consistent. On failure the ".tmp" file is left behind
// and the original path untouched; the caller aborts the run.
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

// DeletePlaced removes the file at root/rel (a missing file is not an
// error) and prunes now-empty parent directories upward, stopping at "."
// (the repo root is never removed) or the first directory that is missing
// or still non-empty. A tracked path is skipped silently: git, not omakase,
// owns it.
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
