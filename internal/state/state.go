// Package state ports the repo-discovery, hashing, drift-detection, and
// frozen-format (placed.tsv / ledger.tsv) reading that bin/status.sh
// performs before it renders anything (bin/status.sh:20-47, 108, 150, 212,
// 309, 314), plus the placed.tsv WRITE side ported from bin/init.sh:603-609
// (WritePlaced). Go twin of that logic — DUPLICATED bash<->Go until Phase 2
// retires the bash callers; keep in lockstep.
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

// Discover finds the git repository containing dir (bin/status.sh:20-22).
// Root is `git rev-parse --show-toplevel`; on error the caller is
// responsible for printing the "not inside a git repo" line and exiting 1
// (bin/status.sh:20) — this function only reports the error. CommonDir is
// `git rev-parse --git-common-dir`, made absolute against Root when
// relative, then filepath.Clean — mirroring
// `cd "$ROOT" && cd "$(git rev-parse --git-common-dir)" && pwd`
// (bin/status.sh:21). OMK is CommonDir + "/omakase".
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

func runGit(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimRight(string(out), "\n"), nil
}

// PlacedRow is one row of $OMK/placed.tsv (written by bin/init.sh:608):
// path, kind, source, sha256, enabled.
type PlacedRow struct {
	Rel     string
	Kind    string
	Src     string
	Hash    string
	Enabled string
}

// ReadPlaced reads $OMK/placed.tsv one line at a time, splitting each into
// at most 5 tab-separated fields via strings.SplitN(line, "\t", 5): a 6th
// tab is absorbed into Enabled rather than split off, and missing trailing
// fields come back as empty strings. Rows with an empty Rel are dropped —
// both status.sh render loops skip them (bin/status.sh:108,150).
//
// This SplitN matches bash's `read -r rel kind src hash enabled` for every
// WELL-FORMED row: one with 5 non-empty tab-separated fields, terminated by
// a newline. Every omakase writer emits only well-formed rows
// (bin/init.sh:608 always prints exactly 5 non-empty fields followed by
// "\n"), so the following two divergences from bash are accepted as
// unreachable in practice:
//
//   - Empty field: tab is one of bash's "IFS whitespace" characters, so
//     `read` collapses runs of tabs into a single delimiter and strips a
//     leading tab before splitting. A row with an EMPTY field therefore
//     parses SHIFTED in bash (later fields slide left to fill the gap) but
//     POSITIONALLY here (SplitN keeps the empty field where it is).
//   - Missing trailing newline: bash's `while read` line loop silently
//     drops a final row with no trailing newline (`read` consumes it but
//     returns non-zero, so the loop body never runs for it); bufio.Scanner
//     has no such rule and processes that row like any other.
//
// Consequence: parity and golden fixtures for this reader must be built
// from real writer output only — never a hand-built row with an empty
// field or a missing final newline, since that input exercises one of the
// divergences above instead of testing the reader itself. Missing file ->
// nil. Order-preserving.
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

// CountNonEmptyLines counts non-empty lines in path, mirroring
// `grep -c .` (bin/status.sh:314, nplaced) — a final line without a
// trailing newline still counts. Missing/unreadable file -> 0.
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
// latest verdict — the Go twin of the awk pass-1 accumulator in
// bin/status.sh:212 (`if (NF>=4 && $1 ~ /^[0-9]+$/) { ts=$1+0;
// if (ts>=seen[$2]) { seen[$2]=ts; verd[$2]=$3 } }`). A row is kept only if
// it has >=4 tab-separated fields and field 1 is all-digit (Global
// Constraint 5); per gate, a later-OR-EQUAL epoch wins, so the last row at
// a tied epoch overwrites the verdict. Missing file -> empty map.
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

// HashOf mirrors omakase_hash_of (bin/status.sh:36-40): a symlink's digest
// is the sha256 of its readlink TARGET STRING; a regular readable file's
// digest is the sha256 of its bytes; an unreadable or absent path -> "".
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

// IsDrifted ports is_drifted (bin/status.sh:41-47) in exact order:
//  1. enabled != "1"          -> false (disabled: not managed, never "drifted")
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

// FirstLine returns the first line of path (head -n1 semantics), or "" if
// the file doesn't exist or is empty ([ -s ] semantics, bin/status.sh:309).
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

// WritePlaced regenerates $OMK/placed.tsv wholesale — the writer-side twin
// of bin/init.sh:603-609's `: > "$OMK/placed.tsv"` truncate followed by one
// `printf '%s\t%s\t%s\t%s\t%s\n'` line per placed path (rel, kind, source
// label, sha256, and the literal enabled flag "1"). Frozen format (Global
// Constraint 4 / design §5): exactly 5 tab-separated, NON-EMPTY fields per
// row, one "\n" terminator per row, no trailing blank line — the whole file
// is built in memory and written in one pass, replacing whatever was there
// (never appended to across calls).
//
// Refuses — returns an error and writes nothing at all, not even a partial
// prefix of valid rows — if any row has an empty field or a field
// containing a tab or newline. A malformed row would silently corrupt every
// downstream reader (ReadPlaced's strings.SplitN above, and every hook-time
// sh reader's `IFS=$'\t' read -r rel kind src hash enabled`), so this
// validates every row BEFORE writing any of them rather than trusting the
// caller. This validation is the writer-side format test the design
// requires (§5).
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
