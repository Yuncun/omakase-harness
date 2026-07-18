// Package status renders the `omakase status` report.
//
// This file covers the inventory renderer, the not-installed message, and the
// pre-0.10 message — the parts that never require an installed harness.
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

// maxLineBuf raises the bufio.Scanner token limit past its 64KiB default.
// None of the files this package reads are expected to exceed it, but a
// pathologically long line should fail closed rather than crash the scan.
const maxLineBuf = 1 << 20

// CommittedList lists the repo's own git-tracked harness surface. It runs
// `git -C root -c core.quotePath=false ls-files -- <harness.CommittedGlobs>`
// (core.quotePath=false so a non-ASCII path isn't quote-escaped, which would
// defeat harness.KindOf's pattern matching) and returns the output lines in
// git's own order. Any error — root isn't a git repo, git isn't on PATH —
// yields an empty result.
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

// PersonalList is a presence-only listing of the user's global harness config
// under home, applying to every repo. Rows are {display path, kind},
// root-qualified (~/.claude/…, ~/.copilot/…) in this order: CLAUDE.md, then
// settings.json, then rules/*.md, commands/*.md, agents/*.md (each
// individually existence-gated), then skills/*/ directories as one row each,
// then ~/.copilot/skills/*/ directories (classified like a .github skill). A
// missing ~/.claude or ~/.copilot yields no rows for that host.
//
// The roots are built by string concatenation, not filepath.Join: with home
// empty, concatenation yields the absolute "/.claude" (which almost never
// exists), while Join would drop the empty element and yield the relative
// ".claude", resolving against the cwd and mislabeling a repo's own .claude/
// as global.
func PersonalList(home string) [][2]string {
	var rows [][2]string

	ch := home + "/.claude"
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

	co := home + "/.copilot" // concat, not Join — see PersonalList
	if isDir(co) {
		for _, b := range globDirs(filepath.Join(co, "skills")) {
			rows = append(rows, [2]string{"~/.copilot/skills/" + b + "/", harness.KindOf(".github/skills/" + b + "/")})
		}
	}

	return rows
}

// globMDFiles lists the base names of dir's *.md dirents (files or
// directories), bytewise sorted, gated by existence (following symlinks, so a
// dangling *.md symlink is excluded). A missing dir yields nil. Leading-dot
// names are excluded.
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

// globDirs lists the base names of dir's dirents that are directories or
// symlinks-to-directories, bytewise sorted. It uses os.Stat (follows
// symlinks) rather than DirEntry.IsDir() (which does not) so a
// symlink-to-a-directory is included. A missing dir yields nil. Leading-dot
// names are excluded.
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

// maxUnmanagedRows caps the rendered "yours, unmanaged" rows; past it the
// elision is stated in a count line — a silent cap would read as "that's
// everything" (the #110 lesson).
const maxUnmanagedRows = 20

// UnmanagedList lists the repo's untracked agent config at the known
// committed-surface paths (harness.CommittedGlobs): present in this clone,
// not git-tracked (ignored or not — ignored ≠ managed), and not in the
// placed ledger at placedPath (any enabled state). These files exist ONLY
// here — the natural candidates for a harness (#123 item 3). Two path
// classes are skipped: harness machinery (harness.IsMachinery — never a
// consent or authoring item; an unledgered machinery file is torn state,
// not the user's config) and Claude Code's own .claude/worktrees/ area
// (whole checkouts, not agent config). Rows are {path, kind} in git's
// sorted order; any git error yields nil.
func UnmanagedList(root, placedPath string) [][2]string {
	args := append([]string{"-C", root, "-c", "core.quotePath=false", "ls-files", "--others", "--"}, harness.CommittedGlobs...)
	out, err := exec.Command("git", args...).Output()
	if err != nil {
		return nil
	}
	placed := map[string]bool{}
	for _, row := range state.ReadPlaced(placedPath) {
		placed[row.Rel] = true
	}
	var rows [][2]string
	for _, rel := range strings.Split(strings.TrimRight(string(out), "\n"), "\n") {
		if rel == "" || placed[rel] || harness.IsMachinery(rel) || strings.HasPrefix(rel, ".claude/worktrees/") {
			continue
		}
		rows = append(rows, [2]string{rel, harness.KindOf(rel)})
	}
	return rows
}

// renderUnmanaged writes the "yours, unmanaged" group. Empty renders
// nothing — the group is a flag, not a fixture. The trailing line is the
// natural offer: a file worth keeping beyond this clone belongs in a
// harness. In md mode the group ends with a blank line (the caller prints
// the next header directly).
func renderUnmanaged(w io.Writer, rows [][2]string, md bool) {
	if len(rows) == 0 {
		return
	}
	shown, elided := rows, 0
	if len(shown) > maxUnmanagedRows {
		elided = len(shown) - maxUnmanagedRows
		shown = shown[:maxUnmanagedRows]
	}
	if md {
		fmt.Fprintln(w, "### Yours, unmanaged — untracked agent config, only in this clone (not committed, not placed by omakase)")
		renderPathRows(w, shown, true)
		if elided > 0 {
			fmt.Fprintf(w, "- … and %d more\n", elided)
		}
		fmt.Fprintln(w)
		fmt.Fprintln(w, "_To keep or share one beyond this clone, add it to a harness — the author skill (`/omakase:author`)._")
		fmt.Fprintln(w)
		return
	}
	fmt.Fprintln(w, "YOURS, UNMANAGED — untracked agent config, only in this clone (not committed, not placed by omakase)")
	renderPathRows(w, shown, false)
	if elided > 0 {
		fmt.Fprintf(w, "    … and %d more\n", elided)
	}
	fmt.Fprintln(w, "    To keep or share one beyond this clone, add it to a harness — the author skill: /omakase:author")
}

// committedRows pairs each CommittedList path with its kind, in git's order,
// for renderPathRows.
func committedRows(root string) [][2]string {
	var rows [][2]string
	for _, rel := range CommittedList(root) {
		if rel == "" {
			continue
		}
		rows = append(rows, [2]string{rel, harness.KindOf(rel)})
	}
	return rows
}

// renderPathRows writes {display path, kind} rows as md bullets or indented
// terminal + rows, with the (none) placeholder when nothing rendered.
func renderPathRows(w io.Writer, rows [][2]string, md bool) {
	shown := false
	for _, row := range rows {
		if row[0] == "" {
			continue
		}
		shown = true
		if md {
			fmt.Fprintf(w, "- `%s` — %s\n", row[0], row[1])
		} else {
			fmt.Fprintf(w, "    + %s   (%s)\n", row[0], row[1])
		}
	}
	if shown {
		return
	}
	if md {
		fmt.Fprintln(w, "- _(none)_")
	} else {
		fmt.Fprintln(w, "    (none)")
	}
}

// RenderInventory renders the harness files grouped by origin: Committed
// (this repo's own git-tracked surface), Injected (from repo.OMK's
// placed.tsv), and Global (home's personal config, not installed by omakase).
// md selects markdown output (### headers, `- ` bullets) vs terminal output
// (all-caps headers, indented +/-/~/! rows).
func RenderInventory(w io.Writer, repo *state.Repo, home string, md bool) {
	comm := committedRows(repo.Root)
	pers := PersonalList(home)
	placedPath := filepath.Join(repo.OMK, "placed.tsv")
	unmanaged := UnmanagedList(repo.Root, placedPath)

	if md {
		fmt.Fprintln(w, "### The project's harness (committed — managed by git, not omakase)")
		renderPathRows(w, comm, true)
		fmt.Fprintln(w)

		fmt.Fprintln(w, "### Injected (omakase) — placed by `omakase init`, gitignored")
		if !renderInjected(w, repo, placedPath, true) {
			fmt.Fprintln(w, "- _(none)_")
		}
		fmt.Fprintln(w)

		renderUnmanaged(w, unmanaged, true)

		fmt.Fprintln(w, "### Global — not installed by omakase (Claude ~/.claude + Copilot ~/.copilot, applies to every repo)")
		renderPathRows(w, pers, true)
		return
	}

	fmt.Fprintln(w, "THE PROJECT'S HARNESS (committed — managed by git, not omakase)")
	renderPathRows(w, comm, false)

	fmt.Fprintln(w, "INJECTED (omakase) — placed by omakase init, gitignored")
	if !renderInjected(w, repo, placedPath, false) {
		fmt.Fprintln(w, "    (none)")
	}

	renderUnmanaged(w, unmanaged, false)

	fmt.Fprintln(w, "GLOBAL — not installed by omakase (Claude ~/.claude + Copilot ~/.copilot, applies to every repo)")
	renderPathRows(w, pers, false)
}

// renderInjected writes the Injected group's rows and reports whether
// anything was shown. The group is "empty" — the caller prints the (none)
// placeholder — when placed.tsv is missing, zero-size, or every row was
// skipped ($rel empty, or healthy machinery — the .omakase/ tree and the
// omakase.manifest gate declaration, noise the reader didn't place by hand).
// An UNHEALTHY machinery row (drifted, or enabled but missing) still renders:
// a weakened or stale gate must never be invisible at rest.
func renderInjected(w io.Writer, repo *state.Repo, placedPath string, md bool) bool {
	info, err := os.Stat(placedPath)
	if err != nil || info.Size() == 0 {
		return false
	}

	shown := false
	for _, row := range state.ReadPlaced(placedPath) {
		if row.Rel == "" {
			continue
		}
		if harness.IsMachinery(row.Rel) && !machineryNoteworthy(repo, row) {
			continue
		}
		shown = true
		writeInjectedRow(w, repo, row, md)
	}
	return shown
}

// machineryNoteworthy reports whether a machinery row deserves a line in the
// Injected group: drifted from canonical, or enabled but missing from this
// checkout. A dangling symlink counts as present (writeInjectedRow renders it
// as an arrow row, not a missing one).
func machineryNoteworthy(repo *state.Repo, row state.PlacedRow) bool {
	if state.IsDrifted(repo.Root, row.Rel, row.Hash, row.Enabled) {
		return true
	}
	if row.Enabled != "1" {
		return false
	}
	full := filepath.Join(repo.Root, row.Rel)
	if _, err := os.Stat(full); err == nil {
		return false
	}
	if _, err := os.Lstat(full); err == nil {
		return false
	}
	return true
}

// writeInjectedRow renders one placed.tsv row in branch order: Enabled=="0"
// -> disabled row; else a symlink (Lstat) -> arrow row (readlink target, even
// if dangling); else the path exists (Stat) -> plain row; else -> missing
// row. The drift suffix (state.IsDrifted) applies only to the arrow and plain
// branches — disabled rows are never managed and missing rows have nothing to
// diff. A kept row (the $OMK/kept accepted copy exists — the user accepted
// their own edit, #98 Part 2) carries its own state marker: consent must be
// visible at rest. Kept and drifted can coexist — an edit made after the
// keep drifts from the ACCEPTED hash and renders both.
func writeInjectedRow(w io.Writer, repo *state.Repo, row state.PlacedRow, md bool) {
	full := filepath.Join(repo.Root, row.Rel)
	drifted := state.IsDrifted(repo.Root, row.Rel, row.Hash, row.Enabled)
	_, kerr := os.Lstat(filepath.Join(repo.OMK, "kept", row.Rel))
	keptMark := kerr == nil // the accepted copy exists, whatever the row state
	kept := row.Enabled == "1" && keptMark

	lstat, lerr := os.Lstat(full)
	isSymlink := lerr == nil && lstat.Mode()&os.ModeSymlink != 0
	_, statErr := os.Stat(full)
	present := statErr == nil

	if md {
		dz, kz := "", ""
		if drifted {
			dz = " — **DRIFTED** (differs from canonical; `omakase init` to re-sync, or it may be an intentional local edit)"
			if kept {
				dz = " — **DRIFTED** (differs from your accepted version; see `omakase diff`)"
			}
		}
		if kept {
			kz = " — kept (yours)"
		}
		switch {
		case row.Enabled == "0":
			note := ""
			if keptMark {
				note = "; a kept version of yours is saved — `omakase status --enable` brings it back"
			}
			fmt.Fprintf(w, "- `%s` — %s, from %s — disabled (not restored, not verified%s)\n", row.Rel, row.Kind, row.Src, note)
		case isSymlink:
			target, _ := os.Readlink(full)
			fmt.Fprintf(w, "- `%s` → `%s` — %s, from %s%s%s\n", row.Rel, target, row.Kind, row.Src, kz, dz)
		case present:
			fmt.Fprintf(w, "- `%s` — %s, from %s%s%s\n", row.Rel, row.Kind, row.Src, kz, dz)
		case kept:
			fmt.Fprintf(w, "- `%s` — %s, from %s — **MISSING** (your kept version is saved; restored on the next checkout, or run `omakase init`)\n", row.Rel, row.Kind, row.Src)
		default:
			fmt.Fprintf(w, "- `%s` — %s, from %s — **MISSING** (run `omakase init` to restore)\n", row.Rel, row.Kind, row.Src)
		}
		return
	}

	dz, kz, mk := "", "", "+"
	if kept {
		kz = "; kept (yours)"
		mk = "="
	}
	if drifted {
		dz = "; DRIFTED — differs from canonical, run omakase init to re-sync"
		if kept {
			dz = "; DRIFTED — differs from your accepted version, see omakase diff"
		}
		mk = "~"
	}
	switch {
	case row.Enabled == "0":
		note := ""
		if keptMark {
			note = "; kept version of yours saved — omakase status --enable brings it back"
		}
		fmt.Fprintf(w, "    - %s   (%s, from %s; disabled — not restored, not verified%s)\n", row.Rel, row.Kind, row.Src, note)
	case isSymlink:
		target, _ := os.Readlink(full)
		fmt.Fprintf(w, "    %s %s -> %s   (%s, from %s%s%s)\n", mk, row.Rel, target, row.Kind, row.Src, kz, dz)
	case present:
		fmt.Fprintf(w, "    %s %s   (%s, from %s%s%s)\n", mk, row.Rel, row.Kind, row.Src, kz, dz)
	case kept:
		fmt.Fprintf(w, "    ! %s   (%s, from %s; MISSING — your kept version is saved; restored on next checkout, or omakase init)\n", row.Rel, row.Kind, row.Src)
	default:
		fmt.Fprintf(w, "    ! %s   (%s, from %s; MISSING — run omakase init to restore)\n", row.Rel, row.Kind, row.Src)
	}
}

// RenderNotInstalled prints the presence-only audit for a repo with no
// overlay (#119): the agent config that exists — committed in this repo,
// plus the user's global config — with the scan's boundary stated outright.
// It reports presence only ("these files exist"), never influence: a path
// scan cannot see settings hierarchies, MCP servers, or host precedence, so
// "this is what steers your agents" would overclaim. The Injected group is
// omitted (nothing placed, nothing to report), and the install pointer names
// the owner/repo form — a bare init with nothing remembered installs
// nothing. The caller exits 0.
func RenderNotInstalled(w io.Writer, repo *state.Repo, home string, md bool) {
	comm := committedRows(repo.Root)
	pers := PersonalList(home)
	unmanaged := UnmanagedList(repo.Root, filepath.Join(repo.OMK, "placed.tsv"))

	if md {
		fmt.Fprintln(w, "**No omakase harness is installed in this repo.**")
		fmt.Fprintln(w)
		fmt.Fprintln(w, "### Agent config committed in this repo (managed by git, not omakase)")
		renderPathRows(w, comm, true)
		fmt.Fprintln(w)
		renderUnmanaged(w, unmanaged, true)
		fmt.Fprintln(w, "### Global — not installed by omakase (Claude ~/.claude + Copilot ~/.copilot, applies to every repo)")
		renderPathRows(w, pers, true)
		fmt.Fprintln(w)
		fmt.Fprintln(w, "_A presence check of known paths for known tools — not exhaustive; a file can be present and never read._")
		fmt.Fprintln(w)
		fmt.Fprintln(w, "_Install a harness:_ `omakase init <owner/repo>`")
		return
	}

	fmt.Fprintln(w, "No omakase harness is installed in this repo.")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "AGENT CONFIG COMMITTED IN THIS REPO (managed by git, not omakase)")
	renderPathRows(w, comm, false)
	renderUnmanaged(w, unmanaged, false)
	fmt.Fprintln(w, "GLOBAL — not installed by omakase (Claude ~/.claude + Copilot ~/.copilot, applies to every repo)")
	renderPathRows(w, pers, false)
	fmt.Fprintln(w)
	fmt.Fprintln(w, "A presence check of known paths for known tools — not exhaustive; a file can be present and never read.")
	fmt.Fprintln(w, "Install a harness:  omakase init <owner/repo>") // two spaces around the verb
}

// RenderPre010 handles a repo where placed.tsv is absent but omk/placed.list
// (the pre-0.10 provenance record) exists: a notice that the harness is
// installed, followed by each placed.list line, md-wrapped as a
// backtick-quoted bullet or term-indented by two spaces. The caller exits 0.
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
	fmt.Fprintln(w, "Run  omakase init  to migrate to the provenance ledger. Placed files:") // two spaces around the verb
	for _, line := range lines {
		fmt.Fprintf(w, "  %s\n", line)
	}
}

// readLines reads path line by line, with no entry for a trailing newline. A
// missing or unreadable file yields nil.
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
