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

// ---------------------------------------------------------------- sources.tsv (Phase 3, design §5/§9)

// SourceRow is one row of $OMK/sources.tsv (design §5): the layer stack,
// bottom-to-top (base has NO row — only project and personal, the two
// layers a source string can ever name). Frozen format (Global Constraint
// 4): layer<TAB>source<TAB>ref<TAB>commit<TAB>installed_epoch. Layer is
// "project" or "personal". Ref is the requested #ref, or "-" if none was
// given. Commit is the full resolved sha at install/update time, or "-" for
// a non-git local path — never guessed. The persisted --no-personal /
// `omakase personal off` opt-out is recorded as its own row:
// personal<TAB>off<TAB>-<TAB>-<TAB><epoch> (see PersonalOff).
type SourceRow struct {
	Layer  string
	Source string
	Ref    string
	Commit string
	Epoch  string
}

// ReadSources reads $OMK/sources.tsv one line at a time via
// strings.Split(line, "\t"). Unlike ReadPlaced (which tolerates short rows
// via SplitN + fallthrough, because bash's `read` can produce them), a
// sources.tsv row has no bash reader on the other end — this file is
// written and read only by this Go package — so ReadSources is stricter:
// a row is kept only if it splits into EXACTLY 5 fields and every field is
// non-empty ("-" counts as non-empty); anything else (wrong field count,
// an empty field) is silently skipped. Order-preserving. Missing file ->
// nil.
//
// Same scanner discipline as ReadPlaced (the maxLineBuf token limit). The
// same final-unterminated-row divergence ReadPlaced documents applies here
// too, for consistency: a last line with no trailing newline is still read
// as a row by bufio.Scanner. That divergence is inert here in practice —
// there is no bash `while read` loop on the other end of this file to
// compare against.
func ReadSources(path string) []SourceRow {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	var rows []SourceRow
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), maxLineBuf)
	for sc.Scan() {
		fields := strings.Split(sc.Text(), "\t")
		if len(fields) != 5 {
			continue
		}
		if fields[0] == "" || fields[1] == "" || fields[2] == "" || fields[3] == "" || fields[4] == "" {
			continue
		}
		rows = append(rows, SourceRow{
			Layer:  fields[0],
			Source: fields[1],
			Ref:    fields[2],
			Commit: fields[3],
			Epoch:  fields[4],
		})
	}
	return rows
}

// WriteSources regenerates $OMK/sources.tsv wholesale: frozen format
// layer<TAB>source<TAB>ref<TAB>commit<TAB>installed_epoch (Global Constraint
// 4), one row per non-base layer, bottom-to-top, exactly as given (the
// caller controls ordering). Refuses — returns an error and writes nothing
// at all — if any row's Layer is not exactly "project" or "personal", or any
// field is empty ("-" is a valid placeholder value and counts as present;
// only a truly empty string refuses), or a field contains an embedded tab or
// newline. Every row is validated BEFORE any of them is written, mirroring
// WritePlaced's discipline — this is the writer-side format test the design
// requires (§5).
//
// Written via the tmp+rename discipline (see writeAtomic's doc comment
// immediately below for the underlying rationale, ported from
// overlay.rewriteFile's doc comment rather than exported from it — a
// deliberate decision, not a refactor of overlay's helper, since the two
// packages have no other reason to depend on each other). After a
// successful write the file's mode is always `0666 &^ umask`, matching every
// other marked-block rewrite site in this codebase (Global Constraint 3).
func WriteSources(path string, rows []SourceRow) error {
	var buf bytes.Buffer
	for i, row := range rows {
		if row.Layer != "project" && row.Layer != "personal" {
			return fmt.Errorf("state.WriteSources: row %d: layer %q is not \"project\" or \"personal\"", i, row.Layer)
		}
		fields := [...]string{row.Layer, row.Source, row.Ref, row.Commit, row.Epoch}
		for j, f := range fields {
			if f == "" {
				return fmt.Errorf("state.WriteSources: row %d field %d: empty field", i, j)
			}
			if strings.ContainsAny(f, "\t\n") {
				return fmt.Errorf("state.WriteSources: row %d field %d: contains a tab or newline: %q", i, j, f)
			}
		}
		buf.WriteString(strings.Join(fields[:], "\t"))
		buf.WriteByte('\n')
	}
	if err := writeAtomic(path, buf.Bytes()); err != nil {
		return fmt.Errorf("state.WriteSources: writing %q: %w", path, err)
	}
	return nil
}

// writeAtomic is this package's local twin of overlay.rewriteFile — see
// that function's doc comment (internal/overlay/overlay.go) for the full
// rationale: bash's `awk ... > f.tmp && mv f.tmp f` idiom creates f.tmp
// FRESH via open(..., O_CREAT|O_TRUNC, 0666) masked by the process umask AT
// CREATION TIME, then replaces f's inode wholesale via rename — so the
// result is ALWAYS mode `0666 &^ umask`, regardless of what mode (if any)
// the destination had before. os.WriteFile over an EXISTING path does NOT
// reproduce this (its mode argument only applies at creation, so writing
// over an existing file silently preserves that file's prior mode) — every
// state-package write that must match this bash idiom goes through
// writeAtomic instead of os.WriteFile.
//
// DECISION: duplicated here rather than exporting/refactoring
// overlay.rewriteFile — state and overlay have no other shared dependency,
// and this function is small enough that the duplication costs less than
// the coupling would.
//
// Like overlay.rewriteFile, a write failure leaves the ".tmp" file behind
// and the original path untouched (no cleanup) — every WriteSources caller
// already validates all rows before calling writeAtomic, so in practice
// this function's own I/O errors (out of disk space, permissions, …) are
// the only way it can fail.
func writeAtomic(path string, content []byte) error {
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o666)
	if err != nil {
		return fmt.Errorf("state: writeAtomic: create %q: %w", tmp, err)
	}
	if _, err := f.Write(content); err != nil {
		f.Close()
		return fmt.Errorf("state: writeAtomic: write %q: %w", tmp, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("state: writeAtomic: close %q: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("state: writeAtomic: rename %q -> %q: %w", tmp, path, err)
	}
	return nil
}

// PersonalOff reports whether rows contains the persisted --no-personal /
// `omakase personal off` opt-out row: a row with Layer == "personal" &&
// Source == "off" (design §5).
func PersonalOff(rows []SourceRow) bool {
	for _, r := range rows {
		if r.Layer == "personal" && r.Source == "off" {
			return true
		}
	}
	return false
}

// SynthesizeSources implements the design §9 lazy, read-only migration: the
// first v2 verb run against a still-v1 repo synthesizes an in-memory
// sources.tsv from $OMK/source instead of ever writing to a v1 repo just to
// read it. Returns (nil, false) unless BOTH: <omk>/sources.tsv is ABSENT (a
// repo that has already migrated is left alone — sources.tsv, once it
// exists, is the single source of truth) AND <omk>/source exists with a
// non-empty first line.
//
// That first line is split on its FIRST '#' — the same split rule as
// expandSource's `*#*` case (internal/overlay/source.go: `ref =
// source[i+1:]` / `source = source[:i]` for the first strings.IndexByte
// match) — into the returned row's Source (the pre-# part) and Ref (the
// post-# part, or "-" if there was no '#' at all, or if the '#' was the
// last byte). $OMK/source holds an already-fully-resolved remembered
// string — bin/init.sh:600's `$SOURCE${SOURCE_REF:+#$SOURCE_REF}` (the
// same value as SOURCE_LABEL, placed.tsv's column 3): the post-expansion
// source plus an optional "#"+ref — not raw user input, so re-applying
// expandSource's owner/repo-shorthand and URL rewrites here would be
// re-litigating a decision init.sh already made once.
//
// The one expandSource guard THIS function DOES mirror is the local-path
// guard: expandSource skips the '#'-split entirely when the whole raw
// input names an existing path (its pathExists, `os.Stat` success — file
// or dir). A remembered local-path source containing a literal '#' (e.g.
// init absolutized "/Users/eric/my#project") is stored in $OMK/source
// verbatim, with no ref — splitting it here would corrupt the path into
// Source "/Users/eric/my", Ref "project". So: if the whole line names an
// existing path, skip the split and return it whole with Ref "-". This
// has the same time-of-check nuance as expandSource's own guard — a
// remembered local path that has since been deleted no longer exists,
// won't os.Stat successfully, and so WILL be split (exactly as a fresh
// expandSource call against that same now-absent string would do).
//
// The returned row's Commit is always "-": a v1 repo never recorded a
// resolved sha, and this function only reads back what is on disk, never
// guesses one. Never writes anything itself — persisting the result is a
// later task's job (the brief's Task 6 wiring).
func SynthesizeSources(omk string, epoch string) ([]SourceRow, bool) {
	if _, err := os.Stat(filepath.Join(omk, "sources.tsv")); err == nil {
		return nil, false
	}

	line := FirstLine(filepath.Join(omk, "source"))
	if line == "" {
		return nil, false
	}

	source, ref := line, "-"
	if _, err := os.Stat(line); err != nil { // mirrors expandSource's pathExists guard
		if i := strings.IndexByte(line, '#'); i >= 0 {
			source = line[:i]
			if rest := line[i+1:]; rest != "" {
				ref = rest
			}
		}
	}

	return []SourceRow{{
		Layer:  "project",
		Source: source,
		Ref:    ref,
		Commit: "-",
		Epoch:  epoch,
	}}, true
}
