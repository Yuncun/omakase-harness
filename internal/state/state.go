// Package state provides repo discovery, hashing, drift detection, and the
// reading and writing of the placed.tsv and ledger.tsv state files.
package state

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
)

// maxLineBuf raises the bufio.Scanner token limit past its 64KiB default —
// none of the files this package reads are expected to exceed 64KiB, but a
// pathologically long single line should fail closed (return "") rather
// than crash the scan.
const maxLineBuf = 1 << 20

// Repo is the git repository discovered for the status verb.
type Repo struct {
	Root      string // git rev-parse --show-toplevel
	CommonDir string // git rev-parse --git-common-dir, absolute + Clean
	OMK       string // CommonDir + "/omakase"
}

// Discover finds the git repository containing dir. Root is
// `git rev-parse --show-toplevel`; on error the caller prints the "not
// inside a git repo" line and exits 1, so this function only reports the
// error. CommonDir is `git rev-parse --git-common-dir`, made absolute
// against Root when relative, then cleaned. OMK is CommonDir + "/omakase".
func Discover(dir string) (*Repo, error) {
	root, err := runGit(dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return nil, err
	}

	common, err := runGit(root, "rev-parse", "--git-common-dir")
	if err != nil {
		return nil, err
	}
	if !filepath.IsAbs(common) {
		common = filepath.Join(root, common)
	}
	common = filepath.Clean(common)

	return &Repo{
		Root:      root,
		CommonDir: common,
		OMK:       filepath.Join(common, "omakase"),
	}, nil
}

// WorktreeRoots returns the root directory of every worktree attached to
// the repository whose root is root — the main checkout first, then each
// linked worktree, in `git worktree list --porcelain` order. A bare entry
// has no checkout and is dropped; a listed-but-deleted worktree is still
// returned (the caller decides how to treat an unreachable root). On any
// git failure the list is root alone, so a caller's per-worktree walk
// degrades to single-checkout behavior.
func WorktreeRoots(root string) []string {
	// -z: attribute lines are NUL-terminated, so a path containing a newline
	// (printed verbatim, unquoted in porcelain output) stays one record
	// instead of truncating to a prefix that may name an unrelated directory.
	out, err := runGit(root, "worktree", "list", "--porcelain", "-z")
	if err != nil {
		return []string{root}
	}
	var roots []string
	cur := ""
	for _, line := range strings.Split(out, "\x00") {
		switch {
		case strings.HasPrefix(line, "worktree "):
			// Each block opens with its "worktree " line, so the previous
			// block is complete here; flush it unless "bare" cleared it.
			if cur != "" {
				roots = append(roots, cur)
			}
			cur = strings.TrimPrefix(line, "worktree ")
		case line == "bare":
			cur = ""
		}
	}
	if cur != "" {
		roots = append(roots, cur)
	}
	if len(roots) == 0 {
		return []string{root}
	}
	return roots
}

func runGit(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimRight(string(out), "\n"), nil
}

// PlacedRow is one row of $OMK/placed.tsv: path and sha256. Everything else
// the old 5-column format stored is derived on demand — kind from the path
// (harness.KindOf), source from the single $OMK/source file, and Enabled
// from the disabled-files sidecar. Enabled is populated by ReadPlaced, never
// stored: "0" when the path is listed in $OMK/disabled-files (or by a legacy
// 5-column row's own enabled field), "1" otherwise.
type PlacedRow struct {
	Rel     string
	Hash    string
	Enabled string
}

// DisabledFilesName is the sidecar beside placed.tsv listing the placed
// paths toggled off, one per line — the same existence-is-the-mark shape as
// disabled-gates and the kept/ tree.
const DisabledFilesName = "disabled-files"

// ReadPlaced reads $OMK/placed.tsv one line at a time. The current format is
// 2 tab-separated fields (rel, hash); the pre-0.26 format was 5 (rel, kind,
// src, hash, enabled) and is still read — a row with 4+ fields takes its
// hash from field 4 and its enabled from field 5. Enabled is then derived:
// "0" if the legacy field said so or the disabled-files sidecar (read from
// placed.tsv's directory) lists the path, else "1". Rows with an empty Rel
// are dropped. A missing file returns nil; order is preserved.
func ReadPlaced(path string) []PlacedRow {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	disabled := DisabledFiles(filepath.Dir(path))

	var rows []PlacedRow
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), maxLineBuf)
	for sc.Scan() {
		fields := strings.SplitN(sc.Text(), "\t", 5)
		var row PlacedRow
		row.Enabled = "1"
		switch {
		case len(fields) >= 4: // legacy 5-column row
			row.Rel = fields[0]
			row.Hash = fields[3]
			// The legacy writer only ever wrote "0" or "1"; anything else
			// (an absorbed 6th tab, hand-editing) is not a deliberate
			// disable and reads enabled.
			if len(fields) == 5 && fields[4] == "0" {
				row.Enabled = "0"
			}
		default:
			row.Rel = fields[0]
			if len(fields) >= 2 {
				row.Hash = fields[1]
			}
		}
		if row.Rel == "" {
			continue
		}
		if disabled[row.Rel] {
			row.Enabled = "0"
		}
		rows = append(rows, row)
	}
	return rows
}

// DisabledFiles is the set of placed paths currently toggled off, read from
// dir's disabled-files sidecar. Missing file -> empty set.
func DisabledFiles(dir string) map[string]bool {
	m := map[string]bool{}
	f, err := os.Open(filepath.Join(dir, DisabledFilesName))
	if err != nil {
		return m
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), maxLineBuf)
	for sc.Scan() {
		if l := strings.TrimSpace(sc.Text()); l != "" {
			m[l] = true
		}
	}
	return m
}

// TrackedUnder lists the repo's git-tracked paths matching globs, in git's
// own order, via `git -C root ls-files -z -- globs...`. -z is load-bearing:
// newline-terminated output C-quotes `\`, `"`, and control characters even
// under core.quotePath=false, and a quoted name never matches the real file
// when compared against one read from disk. NUL termination emits every name
// raw. Any error — root isn't a git repo, git isn't on PATH — yields an
// empty result.
func TrackedUnder(root string, globs []string) []string {
	args := append([]string{"-C", root, "ls-files", "-z", "--"}, globs...)
	out, err := exec.Command("git", args...).Output()
	if err != nil {
		return nil
	}
	var rels []string
	for _, rel := range strings.Split(string(out), "\x00") {
		if rel != "" {
			rels = append(rels, rel)
		}
	}
	return rels
}

// SkipWorktreeUnder is the set of tracked paths matching globs that carry
// git's skip-worktree bit — the state left by deliberately overlaying a
// harness copy on top of a path the repo commits (the alternative to
// --cut-over on a shared repo). `ls-files -v` prefixes each entry with a
// status letter (S = skip-worktree; lowercase letters mean assume-unchanged,
// a much weaker promise git feels free to break, so they do not count), and
// -z keeps names raw for the same reason as TrackedUnder.
func SkipWorktreeUnder(root string, globs []string) map[string]bool {
	args := append([]string{"-C", root, "ls-files", "-z", "-v", "--"}, globs...)
	out, err := exec.Command("git", args...).Output()
	if err != nil {
		return nil
	}
	set := map[string]bool{}
	for _, entry := range strings.Split(string(out), "\x00") {
		if strings.HasPrefix(entry, "S ") {
			set[entry[2:]] = true
		}
	}
	return set
}

// CountNonEmptyLines counts non-empty lines in path; a final line without a
// trailing newline still counts. A missing or unreadable file returns 0.
func CountNonEmptyLines(path string) int {
	f, err := os.Open(path)
	if err != nil {
		return 0
	}
	defer f.Close()

	n := 0
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), maxLineBuf)
	for sc.Scan() {
		if sc.Text() != "" {
			n++
		}
	}
	return n
}

// Verdict is the latest recorded outcome for one gate in $OMK/ledger.tsv.
type Verdict struct {
	Epoch   int64
	Verdict string
}

// LatestVerdicts reads $OMK/ledger.tsv and returns, per gate name, the
// latest verdict. A row is kept only if it has >= 4 tab-separated fields and
// field 1 is all-digit; per gate a later-or-equal epoch wins, so the last
// row at a tied epoch overwrites the verdict. A missing file returns an
// empty map.
func LatestVerdicts(path string) map[string]Verdict {
	result := make(map[string]Verdict)

	f, err := os.Open(path)
	if err != nil {
		return result
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), maxLineBuf)
	for sc.Scan() {
		fields := strings.Split(sc.Text(), "\t")
		if len(fields) < 4 || !isAllDigits(fields[0]) {
			continue
		}
		ts, err := strconv.ParseInt(fields[0], 10, 64)
		if err != nil {
			continue
		}
		gate := fields[1]
		if cur, ok := result[gate]; !ok || ts >= cur.Epoch {
			result[gate] = Verdict{Epoch: ts, Verdict: fields[2]}
		}
	}
	return result
}

func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// HashOf returns a hex sha256 digest: for a symlink, the digest of its
// readlink target string; for a regular readable file, the digest of its
// bytes; for an unreadable or absent path, "".
func HashOf(path string) string {
	info, err := os.Lstat(path)
	if err != nil {
		return ""
	}

	if info.Mode()&os.ModeSymlink != 0 {
		target, err := os.Readlink(path)
		if err != nil {
			return ""
		}
		sum := sha256.Sum256([]byte(target))
		return hex.EncodeToString(sum[:])
	}

	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return ""
	}
	return hex.EncodeToString(h.Sum(nil))
}

// IsDrifted reports whether root/rel has drifted from ledgerHash, checked in
// order:
//  1. enabled != "1"          -> false (disabled: not managed, never drifted)
//  2. neither Stat nor Lstat  -> false (missing is its own state, not drift)
//  3. git-tracked at rel      -> false (upstream owns it)
//  4. otherwise: drifted iff ledgerHash != "" && HashOf(root/rel) != "" &&
//     HashOf(root/rel) != ledgerHash
func IsDrifted(root, rel, ledgerHash, enabled string) bool {
	if enabled != "1" {
		return false
	}

	full := filepath.Join(root, rel)
	if _, err := os.Stat(full); err != nil {
		if _, lerr := os.Lstat(full); lerr != nil {
			return false // missing
		}
	}

	cmd := exec.Command("git", "-C", root, "ls-files", "--error-unmatch", "--", rel)
	if err := cmd.Run(); err == nil {
		return false // tracked: upstream owns it
	}

	a := HashOf(full)
	return ledgerHash != "" && a != "" && a != ledgerHash
}

// FirstLine returns the first line of path, or "" if the file doesn't exist
// or is empty.
func FirstLine(path string) string {
	info, err := os.Stat(path)
	if err != nil || info.Size() == 0 {
		return ""
	}

	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), maxLineBuf)
	if sc.Scan() {
		return sc.Text()
	}
	return ""
}

// WritePlaced regenerates $OMK/placed.tsv wholesale: exactly 2 tab-separated,
// non-empty fields per row (rel, hash), one "\n" per row, no trailing blank
// line. Enabled is never written — the disabled-files sidecar is the store —
// so writing a file read in the legacy 5-column format migrates it; the
// caller is responsible for carrying any legacy enabled=0 rows into the
// sidecar first (WriteDisabledFiles).
//
// It refuses — returns an error and writes nothing, not even a partial
// prefix — if any row has an empty field or a field containing a tab or
// newline, since a malformed row would corrupt every downstream reader.
// Every row is validated before any is written.
func WritePlaced(path string, rows []PlacedRow) error {
	var buf bytes.Buffer
	for i, row := range rows {
		fields := [...]string{row.Rel, row.Hash}
		for j, f := range fields {
			if f == "" {
				return fmt.Errorf("state.WritePlaced: row %d field %d: empty field", i, j)
			}
			if strings.ContainsAny(f, "\t\n") {
				return fmt.Errorf("state.WritePlaced: row %d field %d: contains a tab or newline: %q", i, j, f)
			}
		}
		buf.WriteString(strings.Join(fields[:], "\t"))
		buf.WriteByte('\n')
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		return fmt.Errorf("state.WritePlaced: writing %q: %w", path, err)
	}
	return nil
}

// WriteDisabledFiles rewrites dir's disabled-files sidecar wholesale to the
// sorted contents of set; an empty set removes the file. Paths containing a
// newline are skipped (they cannot round-trip a line-oriented file).
func WriteDisabledFiles(dir string, set map[string]bool) error {
	var names []string
	for n := range set {
		if n == "" || strings.ContainsRune(n, '\n') {
			continue
		}
		names = append(names, n)
	}
	path := filepath.Join(dir, DisabledFilesName)
	if len(names) == 0 {
		err := os.Remove(path)
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	sort.Strings(names)
	return os.WriteFile(path, []byte(strings.Join(names, "\n")+"\n"), 0o644)
}

// UserRel normalizes a user-typed placed path for ledger lookup. The
// ledger records slash-form paths on every platform, but a Windows shell
// tab-completes backslashes — accept both there. On Unix the arg passes
// through untouched: a backslash is a legal filename byte.
func UserRel(p string) string {
	if runtime.GOOS == "windows" {
		return strings.ReplaceAll(p, `\`, "/")
	}
	return p
}
