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
	out, err := runGit(root, "worktree", "list", "--porcelain")
	if err != nil {
		return []string{root}
	}
	var roots []string
	cur := ""
	for _, line := range strings.Split(out, "\n") {
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

// PlacedRow is one row of $OMK/placed.tsv: path, kind, source, sha256,
// enabled.
type PlacedRow struct {
	Rel     string
	Kind    string
	Src     string
	Hash    string
	Enabled string
}

// ReadPlaced reads $OMK/placed.tsv one line at a time, splitting each into
// at most 5 tab-separated fields: a 6th tab is absorbed into Enabled rather
// than split off, and missing trailing fields come back as empty strings.
// An empty field is kept in place, not shifted, and a final row with no
// trailing newline is still read. Rows with an empty Rel are dropped. A
// missing file returns nil; order is preserved.
func ReadPlaced(path string) []PlacedRow {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	var rows []PlacedRow
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), maxLineBuf)
	for sc.Scan() {
		fields := strings.SplitN(sc.Text(), "\t", 5)
		var row PlacedRow
		switch len(fields) {
		case 5:
			row.Enabled = fields[4]
			fallthrough
		case 4:
			row.Hash = fields[3]
			fallthrough
		case 3:
			row.Src = fields[2]
			fallthrough
		case 2:
			row.Kind = fields[1]
			fallthrough
		case 1:
			row.Rel = fields[0]
		}
		if row.Rel == "" {
			continue
		}
		rows = append(rows, row)
	}
	return rows
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

// WritePlaced regenerates $OMK/placed.tsv wholesale: exactly 5 tab-separated,
// non-empty fields per row, one "\n" per row, no trailing blank line. The
// file is built in memory and written in one pass, replacing whatever was
// there.
//
// It refuses — returns an error and writes nothing, not even a partial
// prefix — if any row has an empty field or a field containing a tab or
// newline, since a malformed row would corrupt every downstream reader.
// Every row is validated before any is written.
func WritePlaced(path string, rows []PlacedRow) error {
	var buf bytes.Buffer
	for i, row := range rows {
		fields := [...]string{row.Rel, row.Kind, row.Src, row.Hash, row.Enabled}
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
