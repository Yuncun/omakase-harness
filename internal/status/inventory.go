// Package status ports bin/status.sh (the `omakase status` verb) to Go. This
// file (inventory.go) covers the inventory renderer (bin/status.sh:90-178),
// the not-installed message (bin/status.sh:292-301), and the pre-0.10
// message (bin/status.sh:277-289) — the parts of status.sh that never
// require an installed harness. Guards-chart + identity rendering and full
// verb assembly are a later task.
package status

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Yuncun/omakase-harness/internal/harness"
	"github.com/Yuncun/omakase-harness/internal/state"
)

// maxLineBuf raises the bufio.Scanner token limit past its 64KiB default,
// mirroring internal/state's constant of the same name — none of the files
// this package reads are expected to exceed it, but a pathologically long
// line should fail closed rather than crash the scan.
const maxLineBuf = 1 << 20

// CommittedList lists the repo's own git-tracked harness surface: the Go
// twin of committed_list() (bin/status.sh:60-65). It runs
// `git -C root -c core.quotePath=false ls-files -- <harness.CommittedGlobs>`
// (core.quotePath=false so a non-ASCII path isn't quote-escaped, which would
// defeat harness.KindOf's pattern matching) and returns the output lines in
// git's own order. Any error — root isn't a git repo, git isn't on PATH,
// etc. — yields an empty result, matching bash's `2>/dev/null || true`.
func CommittedList(root string) []string {
	args := append([]string{"-C", root, "-c", "core.quotePath=false", "ls-files", "--"}, harness.CommittedGlobs...)
	out, err := exec.Command("git", args...).Output()
	if err != nil {
		return nil
	}
	trimmed := strings.TrimRight(string(out), "\n")
	if trimmed == "" {
		return nil
	}
	return strings.Split(trimmed, "\n")
}

// PersonalList is the Go twin of personal_list() (bin/status.sh:72-88): a
// presence-only listing of the user's GLOBAL harness config under home,
// applying to every repo. Rows are {display path, kind}, root-qualified
// (~/.claude/…, ~/.copilot/…) in this exact order: CLAUDE.md, then
// settings.json, then rules/*.md, commands/*.md, agents/*.md (each
// individually existence-gated), then skills/*/ directories as ONE row
// each, then ~/.copilot/skills/*/ directories (classified like a .github
// skill — deliberate, matching bash's kind_of ".github/skills/$b/"). A
// missing ~/.claude or ~/.copilot yields no rows for that host.
func PersonalList(home string) [][2]string {
	var rows [][2]string

	ch := filepath.Join(home, ".claude")
	if isDir(ch) {
		if exists(filepath.Join(ch, "CLAUDE.md")) {
			rows = append(rows, [2]string{"~/.claude/CLAUDE.md", harness.KindOf("CLAUDE.md")})
		}
		if exists(filepath.Join(ch, "settings.json")) {
			rows = append(rows, [2]string{"~/.claude/settings.json", harness.KindOf(".claude/settings.json")})
		}
		for _, b := range globMDFiles(filepath.Join(ch, "rules")) {
			rows = append(rows, [2]string{"~/.claude/rules/" + b, harness.KindOf(".claude/rules/" + b)})
		}
		for _, b := range globMDFiles(filepath.Join(ch, "commands")) {
			rows = append(rows, [2]string{"~/.claude/commands/" + b, harness.KindOf(".claude/commands/" + b)})
		}
		for _, b := range globMDFiles(filepath.Join(ch, "agents")) {
			rows = append(rows, [2]string{"~/.claude/agents/" + b, harness.KindOf(".claude/agents/" + b)})
		}
		for _, b := range globDirs(filepath.Join(ch, "skills")) {
			rows = append(rows, [2]string{"~/.claude/skills/" + b + "/", harness.KindOf(".claude/skills/" + b + "/")})
		}
	}

	co := filepath.Join(home, ".copilot")
	if isDir(co) {
		for _, b := range globDirs(filepath.Join(co, "skills")) {
			rows = append(rows, [2]string{"~/.copilot/skills/" + b + "/", harness.KindOf(".github/skills/" + b + "/")})
		}
	}

	return rows
}

// globMDFiles lists the base names of dir's *.md dirents (files or
// directories — bash's `*.md` glob doesn't care), bytewise sorted, gated by
// existence (bash's `[ -e ]`, which follows symlinks: a dangling *.md
// symlink is excluded). A missing dir yields nil, matching bash's
// no-match-leaves-pattern-literal-then-fails-`-e` behavior. A leading-dot
// name is excluded: none of the status.sh globs run under `shopt -s
// dotglob`, so bash's bare `*` never matches a dotfile (confirmed against a
// live bash).
func globMDFiles(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}
		if !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		if !exists(filepath.Join(dir, e.Name())) {
			continue
		}
		names = append(names, e.Name())
	}
	sort.Strings(names)
	return names
}

// globDirs lists the base names of dir's dirents that are directories OR
// symlinks-to-directories (bash's `*/` glob), bytewise sorted. It uses
// os.Stat (follows symlinks) rather than DirEntry.IsDir() (which does not)
// so a symlink-to-a-directory is included, matching bash's `[ -d ]`. A
// missing dir yields nil. A leading-dot name is excluded, matching bash's
// bare `*` without `dotglob` (see globMDFiles).
func globDirs(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}
		info, err := os.Stat(filepath.Join(dir, e.Name()))
		if err != nil || !info.IsDir() {
			continue
		}
		names = append(names, e.Name())
	}
	sort.Strings(names)
	return names
}

func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// RenderInventory is the Go twin of render_inventory() (bin/status.sh:90-
// 178), byte-for-byte: the harness files grouped by origin — Committed
// (this repo's own git-tracked surface), Injected (from repo.OMK's
// placed.tsv), Global (home's personal config, not installed by omakase).
// md selects Markdown output (### headers, `- ` bullets) vs. terminal
// output (ALL-CAPS headers, indented +/-/~/! rows).
func RenderInventory(w io.Writer, repo *state.Repo, home string, md bool) {
	comm := CommittedList(repo.Root)
	pers := PersonalList(home)
	placedPath := filepath.Join(repo.OMK, "placed.tsv")

	if md {
		fmt.Fprintln(w, "### Committed (this repo) — tracked harness files")
		if len(comm) > 0 {
			for _, rel := range comm {
				if rel == "" {
					continue
				}
				fmt.Fprintf(w, "- `%s` — %s\n", rel, harness.KindOf(rel))
			}
		} else {
			fmt.Fprintln(w, "- _(none)_")
		}
		fmt.Fprintln(w)

		fmt.Fprintln(w, "### Injected (omakase) — placed by `omakase init`, gitignored")
		if !renderInjected(w, repo, placedPath, true) {
			fmt.Fprintln(w, "- _(none)_")
		}
		fmt.Fprintln(w)

		fmt.Fprintln(w, "### Global — not installed by omakase (Claude ~/.claude + Copilot ~/.copilot, applies to every repo)")
		if len(pers) > 0 {
			for _, row := range pers {
				if row[0] == "" {
					continue
				}
				fmt.Fprintf(w, "- `%s` — %s\n", row[0], row[1])
			}
		} else {
			fmt.Fprintln(w, "- _(none)_")
		}
		return
	}

	fmt.Fprintln(w, "COMMITTED (this repo) — tracked harness files")
	if len(comm) > 0 {
		for _, rel := range comm {
			if rel == "" {
				continue
			}
			fmt.Fprintf(w, "    + %s   (%s)\n", rel, harness.KindOf(rel))
		}
	} else {
		fmt.Fprintln(w, "    (none)")
	}

	fmt.Fprintln(w, "INJECTED (omakase) — placed by omakase init, gitignored")
	if !renderInjected(w, repo, placedPath, false) {
		fmt.Fprintln(w, "    (none)")
	}

	fmt.Fprintln(w, "GLOBAL — not installed by omakase (Claude ~/.claude + Copilot ~/.copilot, applies to every repo)")
	if len(pers) > 0 {
		for _, row := range pers {
			if row[0] == "" {
				continue
			}
			fmt.Fprintf(w, "    + %s   (%s)\n", row[0], row[1])
		}
	} else {
		fmt.Fprintln(w, "    (none)")
	}
}

// renderInjected writes the Injected group's rows (bin/status.sh:105-125 md,
// 147-166 term) and reports whether anything was shown. The group is
// "empty" — caller prints the (none) placeholder — when placed.tsv is
// missing, zero-size, or every row was skipped ($rel empty or under
// .omakase/, which discloses under Hidden instead).
func renderInjected(w io.Writer, repo *state.Repo, placedPath string, md bool) bool {
	info, err := os.Stat(placedPath)
	if err != nil || info.Size() == 0 {
		return false
	}

	shown := false
	for _, row := range state.ReadPlaced(placedPath) {
		if row.Rel == "" || strings.HasPrefix(row.Rel, ".omakase/") {
			continue
		}
		shown = true
		writeInjectedRow(w, repo, row, md)
	}
	return shown
}

// writeInjectedRow renders one placed.tsv row, branch order exactly as
// bin/status.sh:111-120 (md) / 153-162 (term): Enabled=="0" -> disabled row;
// else a symlink (Lstat) -> arrow row (readlink target, even if dangling);
// else the path exists (Stat) -> plain row; else -> MISSING row. The drift
// suffix (state.IsDrifted) only ever applies to the arrow/plain branches —
// disabled rows are never "managed" and MISSING rows have nothing to diff.
func writeInjectedRow(w io.Writer, repo *state.Repo, row state.PlacedRow, md bool) {
	full := filepath.Join(repo.Root, row.Rel)
	drifted := state.IsDrifted(repo.Root, row.Rel, row.Hash, row.Enabled)

	lstat, lerr := os.Lstat(full)
	isSymlink := lerr == nil && lstat.Mode()&os.ModeSymlink != 0
	_, statErr := os.Stat(full)
	present := statErr == nil

	if md {
		dz := ""
		if drifted {
			dz = " — **DRIFTED** (differs from canonical; `omakase init` to re-sync, or it may be an intentional local edit)"
		}
		switch {
		case row.Enabled == "0":
			fmt.Fprintf(w, "- `%s` — %s, from %s — disabled (not restored, not verified)\n", row.Rel, row.Kind, row.Src)
		case isSymlink:
			target, _ := os.Readlink(full)
			fmt.Fprintf(w, "- `%s` → `%s` — %s, from %s%s\n", row.Rel, target, row.Kind, row.Src, dz)
		case present:
			fmt.Fprintf(w, "- `%s` — %s, from %s%s\n", row.Rel, row.Kind, row.Src, dz)
		default:
			fmt.Fprintf(w, "- `%s` — %s, from %s — **MISSING** (run `omakase init` to restore)\n", row.Rel, row.Kind, row.Src)
		}
		return
	}

	dz, mk := "", "+"
	if drifted {
		dz = "; DRIFTED — differs from canonical, run omakase init to re-sync"
		mk = "~"
	}
	switch {
	case row.Enabled == "0":
		fmt.Fprintf(w, "    - %s   (%s, from %s; disabled — not restored, not verified)\n", row.Rel, row.Kind, row.Src)
	case isSymlink:
		target, _ := os.Readlink(full)
		fmt.Fprintf(w, "    %s %s -> %s   (%s, from %s%s)\n", mk, row.Rel, target, row.Kind, row.Src, dz)
	case present:
		fmt.Fprintf(w, "    %s %s   (%s, from %s%s)\n", mk, row.Rel, row.Kind, row.Src, dz)
	default:
		fmt.Fprintf(w, "    ! %s   (%s, from %s; MISSING — run omakase init to restore)\n", row.Rel, row.Kind, row.Src)
	}
}

// RenderNotInstalled is the Go twin of the not-installed branch
// (bin/status.sh:292-301): the "no harness installed" message, a blank
// line, then the full inventory (the audit view — what does this repo feed
// your agent? — still works on an uninstalled repo). The caller exits 0.
func RenderNotInstalled(w io.Writer, repo *state.Repo, home string, md bool) {
	if md {
		fmt.Fprintln(w, "**No omakase harness is installed in this repo.** Run `omakase init` to inject one.")
	} else {
		fmt.Fprintln(w, "No omakase harness is installed in this repo.")
		fmt.Fprintln(w, "Run  omakase init  to inject one.") // two spaces around the verb, verbatim
	}
	fmt.Fprintln(w)
	RenderInventory(w, repo, home, md)
}

// RenderPre010 is the Go twin of the pre-0.10 branch (bin/status.sh:277-
// 289), triggered when placed.tsv is absent but omk/placed.list (the
// pre-0.10 provenance record) exists: a notice that the harness IS
// installed (never a false negative about an enforcement system) followed
// by each placed.list line, md-wrapped as a backtick-quoted bullet (sed
// 's/^/- `/; s/$/`/') or term-indented by two spaces. The caller exits 0.
func RenderPre010(w io.Writer, omk string, md bool) {
	lines := readLines(filepath.Join(omk, "placed.list"))

	if md {
		fmt.Fprintln(w, "**Pre-0.10 omakase install detected** (record: `placed.list`). Run `omakase init` to migrate to the provenance ledger. Placed files:")
		for _, line := range lines {
			fmt.Fprintf(w, "- `%s`\n", line)
		}
		return
	}

	fmt.Fprintln(w, "Pre-0.10 omakase install detected (record: placed.list).")
	fmt.Fprintln(w, "Run  omakase init  to migrate to the provenance ledger. Placed files:") // two spaces around the verb, verbatim
	for _, line := range lines {
		fmt.Fprintf(w, "  %s\n", line)
	}
}

// readLines reads path line-by-line (sed/cat semantics: no entry for a
// trailing newline). A missing or unreadable file yields nil.
func readLines(path string) []string {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	var lines []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), maxLineBuf)
	for sc.Scan() {
		lines = append(lines, sc.Text())
	}
	return lines
}
