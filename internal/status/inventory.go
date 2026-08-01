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
	"unicode/utf8"

	"github.com/Yuncun/omakase-harness/internal/ctxlayers"
	"github.com/Yuncun/omakase-harness/internal/harness"
	"github.com/Yuncun/omakase-harness/internal/state"
)

// maxLineBuf raises the bufio.Scanner token limit past its 64KiB default.
// None of the files this package reads are expected to exceed it, but a
// pathologically long line should fail closed rather than crash the scan.
const maxLineBuf = 1 << 20

// CommittedList lists the repo's own git-tracked harness surface
// (state.TrackedUnder over harness.CommittedGlobs), in git's own order.
//
// A path the repo commits but that carries the skip-worktree bit is being
// deliberately served from an injected harness copy, so it is not part of the
// repo's live surface — listing it here would report the same file twice in
// two sections that contradict each other. Those paths are filtered out.
func CommittedList(root string) []string {
	live := state.TrackedUnder(root, harness.CommittedGlobs)
	skipped := state.SkipWorktreeUnder(root, harness.CommittedGlobs)
	if len(skipped) == 0 {
		return live
	}
	var out []string
	for _, rel := range live {
		if !skipped[rel] {
			out = append(out, rel)
		}
	}
	return out
}

// PersonalList is a presence-only listing of the user's global harness config
// under home, applying to every repo. Rows are {display path, kind},
// root-qualified (~/.claude/…, ~/.copilot/…, ~/.agents/…) in this order:
// CLAUDE.md, then settings.json, then rules/*.md, commands/*.md, agents/*.md
// (each individually existence-gated), then skills/*/ directories as one row
// each; then the ~/.copilot root: copilot-instructions.md (the user-global
// instruction file, peer of ~/.claude/CLAUDE.md — #164 C7), settings.json,
// and skills/*/ directories (classified like a .github skill); then
// ~/.agents/skills/*/ (Copilot CLI's other documented personal skill root).
// A missing root dir yields no rows for that root.
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
		if exists(filepath.Join(co, "copilot-instructions.md")) {
			rows = append(rows, [2]string{"~/.copilot/copilot-instructions.md", harness.KindOf(".github/copilot-instructions.md")})
		}
		if exists(filepath.Join(co, "settings.json")) {
			rows = append(rows, [2]string{"~/.copilot/settings.json", harness.KindOf(".claude/settings.json")})
		}
		for _, b := range globDirs(filepath.Join(co, "skills")) {
			rows = append(rows, [2]string{"~/.copilot/skills/" + b + "/", harness.KindOf(".github/skills/" + b + "/")})
		}
	}

	ag := home + "/.agents" // concat, not Join — see PersonalList
	if isDir(ag) {
		for _, b := range globDirs(filepath.Join(ag, "skills")) {
			rows = append(rows, [2]string{"~/.agents/skills/" + b + "/", harness.KindOf(".agents/skills/" + b + "/")})
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
		fmt.Fprintln(w)
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
		fmt.Fprintf(w, "  … and %d more\n", elided)
	}
	fmt.Fprintln(w, "  To keep or share one beyond this clone, add it to a harness — the author skill: /omakase:author")
	fmt.Fprintln(w)
}

// committedRows pairs each CommittedList path with its kind, in git's order,
// for renderPathRows.
func committedRows(repo *state.Repo) [][2]string {
	var rows [][2]string
	for _, rel := range CommittedList(repo.Root) {
		if rel == "" {
			continue
		}
		rows = append(rows, [2]string{rel, harness.KindOf(rel)})
	}
	return rows
}

// renderPathRows writes {display path, kind} rows as a width-aligned
// PATH/KIND table (the guards chart's house style), or a markdown table,
// with the (none) placeholder when nothing rendered.
func renderPathRows(w io.Writer, rows [][2]string, md bool) {
	var kept [][2]string
	for _, row := range rows {
		if row[0] != "" {
			kept = append(kept, row)
		}
	}
	if len(kept) == 0 {
		if md {
			fmt.Fprintln(w, "- _(none)_")
		} else {
			fmt.Fprintln(w, "    (none)")
		}
		return
	}
	if md {
		fmt.Fprintln(w, "| Path | Kind |")
		fmt.Fprintln(w, "| --- | --- |")
		for _, row := range kept {
			fmt.Fprintf(w, "| `%s` | %s |\n", row[0], row[1])
		}
		return
	}
	wP := utf8.RuneCountInString("PATH")
	for _, row := range kept {
		if l := utf8.RuneCountInString(row[0]); l > wP {
			wP = l
		}
	}
	fmt.Fprintf(w, "  %-*s   %s\n", wP, "PATH", "KIND")
	for _, row := range kept {
		fmt.Fprintf(w, "  %-*s   %s\n", wP, row[0], row[1])
	}
}

// RenderInventory renders the harness files grouped by origin: Committed
// (this repo's own git-tracked surface), Injected (from repo.OMK's
// placed.tsv), and Global (home's personal config, not installed by omakase).
// md selects markdown output (### headers, `- ` bullets) vs terminal output
// (all-caps headers, indented +/-/~/! rows).
func RenderInventory(w io.Writer, repo *state.Repo, home string, md bool) {
	comm := committedRows(repo)
	pers := PersonalList(home)
	placedPath := filepath.Join(repo.OMK, "placed.tsv")
	unmanaged := UnmanagedList(repo.Root, placedPath)

	if md {
		fmt.Fprintln(w, "### The project's harness (committed — managed by git, not omakase)")
		fmt.Fprintln(w)
		renderPathRows(w, comm, true)
		fmt.Fprintln(w)

		fmt.Fprintf(w, "### Injected — placed by `omakase init` from %s · gitignored\n", injectedSrc(repo))
		fmt.Fprintln(w)
		if !renderInjected(w, repo, placedPath, true) {
			fmt.Fprintln(w, "- _(none)_")
		}
		fmt.Fprintln(w)

		renderUnmanaged(w, unmanaged, true)

		renderGlobalLine(w, len(pers), true)
		return
	}

	fmt.Fprintln(w, "THE PROJECT'S HARNESS (committed — managed by git, not omakase)")
	renderPathRows(w, comm, false)
	fmt.Fprintln(w)

	fmt.Fprintf(w, "INJECTED — placed by omakase init from %s · gitignored\n", injectedSrc(repo))
	if !renderInjected(w, repo, placedPath, false) {
		fmt.Fprintln(w, "    (none)")
	}
	fmt.Fprintln(w)

	renderUnmanaged(w, unmanaged, false)

	renderGlobalLine(w, len(pers), false)
}

// RenderProblems prints only the injected rows that are off their canonical
// state — missing, drifted, or toggled off — under a NEEDS ATTENTION header,
// and prints nothing at all when every row is healthy. This is the default
// page's whole per-file surface: a healthy file earns no line (its cost and
// reach are the layers section's job), while a weakened or stale one must
// never be invisible at rest. The row builder is injectedCells, so a problem
// row here reads identically to its --all twin.
func RenderProblems(w io.Writer, repo *state.Repo, md bool) {
	var rows []invRow
	for _, row := range state.ReadPlaced(filepath.Join(repo.OMK, "placed.tsv")) {
		if row.Rel == "" || !rowNeedsAttention(repo, row) {
			continue
		}
		rows = append(rows, injectedCells(repo, row))
	}
	if len(rows) == 0 {
		return
	}
	fmt.Fprintln(w)
	if md {
		fmt.Fprintln(w, "### Needs attention — injected files off their canonical state")
		fmt.Fprintln(w)
	} else {
		fmt.Fprintln(w, "NEEDS ATTENTION — injected files off their canonical state")
	}
	emitInjectedTable(w, rows, md)
}

// rowNeedsAttention reports whether a placed row belongs on the default
// page: toggled off, drifted (kept or not — a kept row still shows, marked
// as yours), or enabled but absent from this checkout. A healthy present row
// does not; a dangling symlink counts as present (it renders as an arrow row
// in the --all inventory, and pointing at nothing is the payload's business).
func rowNeedsAttention(repo *state.Repo, row state.PlacedRow) bool {
	if row.Enabled == "0" {
		return true
	}
	if state.IsDrifted(repo.Root, row.Rel, row.Hash, row.Enabled) {
		return true
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

// renderGlobalLine is the page's whole GLOBAL section: one count line. The
// FACT that personal config steers every repo belongs on the page; the
// enumeration repeats identically in every repo and drowned it (#131 gripe 4)
// — the list lives behind `omakase status --global` (RenderGlobal).
func renderGlobalLine(w io.Writer, n int, md bool) {
	var line string
	switch {
	case n == 0:
		line = " — no personal config found in ~/.claude or ~/.copilot"
	case n == 1:
		line = " — 1 file in ~/.claude + ~/.copilot + ~/.agents steers every repo (list: omakase status --global)"
	default:
		line = fmt.Sprintf(" — %d files in ~/.claude + ~/.copilot + ~/.agents steer every repo (list: omakase status --global)", n)
	}
	if md {
		fmt.Fprintln(w, "### Global"+line)
		return
	}
	fmt.Fprintln(w, "GLOBAL"+line)
}

// RenderGlobal is the `omakase status --global` page: the full personal-config
// listing the status page's GLOBAL line counts. It reads only home — no repo,
// no ledger — so it renders the same everywhere, installed or not.
func RenderGlobal(w io.Writer, home string, md bool) {
	pers := PersonalList(home)
	if md {
		fmt.Fprintln(w, "### Global — not installed by omakase (Claude ~/.claude + Copilot ~/.copilot, applies to every repo)")
		fmt.Fprintln(w)
		renderPathRows(w, pers, true)
		return
	}
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
	var rows []invRow
	for _, row := range state.ReadPlaced(placedPath) {
		if row.Rel == "" {
			continue
		}
		if harness.IsMachinery(row.Rel) && !machineryNoteworthy(repo, row) {
			continue
		}
		rows = append(rows, injectedCells(repo, row))
	}
	if len(rows) == 0 {
		return false
	}
	emitInjectedTable(w, rows, md)
	return true
}

// injectedSrc is the shared "from" fact for the whole Injected group — the
// remembered source ($OMK/source) in its browsable form, or "payload" for a
// plain install. It is stated once, in the group header, never per row.
func injectedSrc(repo *state.Repo) string {
	src := srcDisplay(state.FirstLine(filepath.Join(repo.OMK, "source")))
	if src == "" {
		return "payload"
	}
	return src
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

// invRow is one Injected-table row: the path cell (symlinks render as
// `rel -> target`), the kind, and the state cell.
type invRow struct{ path, kind, state string }

// injectedCells builds one placed.tsv row's table cells, in branch order:
// Enabled=="0" -> disabled; else a symlink (Lstat) -> arrow path (readlink
// target, even if dangling); else the path exists (Stat) -> plain; else ->
// MISSING. Drift (state.IsDrifted) applies only to the arrow and plain
// branches — disabled rows are never managed and missing rows have nothing
// to diff. A kept row (the $OMK/kept accepted copy exists — the user
// accepted their own edit, #98 Part 2) says so: consent must be visible at
// rest. Kept and drifted can coexist — an edit made after the keep drifts
// from the ACCEPTED hash.
func injectedCells(repo *state.Repo, row state.PlacedRow) invRow {
	// Kind is a pure function of the path; the ledger stores only rel + hash.
	kind := harness.KindOf(row.Rel)
	full := filepath.Join(repo.Root, row.Rel)
	drifted := state.IsDrifted(repo.Root, row.Rel, row.Hash, row.Enabled)
	_, kerr := os.Lstat(filepath.Join(repo.OMK, "kept", row.Rel))
	keptMark := kerr == nil // the accepted copy exists, whatever the row state
	kept := row.Enabled == "1" && keptMark

	lstat, lerr := os.Lstat(full)
	isSymlink := lerr == nil && lstat.Mode()&os.ModeSymlink != 0
	_, statErr := os.Stat(full)
	present := statErr == nil

	pathCell := row.Rel
	if row.Enabled != "0" && isSymlink {
		target, _ := os.Readlink(full)
		pathCell = row.Rel + " -> " + target
	}

	var stateCell string
	switch {
	case row.Enabled == "0":
		stateCell = "disabled — omakase status --enable"
		if keptMark {
			stateCell = "disabled — kept copy saved; --enable restores it"
		}
	case drifted:
		// A drifted machinery file is torn state and init re-places it; a
		// drifted user-facing file is a local edit and belongs to the edit
		// lifecycle (init refuses to overwrite it).
		stateCell = "DRIFTED — omakase diff, then --keep or --restore"
		if harness.IsMachinery(row.Rel) {
			stateCell = "DRIFTED — omakase init re-syncs"
		}
		if kept {
			stateCell = "DRIFTED from your kept version — omakase diff"
		}
	case present || isSymlink:
		stateCell = "✓"
		if kept {
			stateCell = "✓ kept (yours)"
		}
	case kept:
		stateCell = "MISSING — kept copy returns on next checkout, or omakase init"
	default:
		stateCell = "MISSING — omakase init restores"
	}
	return invRow{pathCell, kind, stateCell}
}

// emitInjectedTable writes invRows as a width-aligned PATH/KIND/STATE table
// (terminal) or a markdown table, state last and unpadded.
func emitInjectedTable(w io.Writer, rows []invRow, md bool) {
	if md {
		fmt.Fprintln(w, "| Path | Kind | State |")
		fmt.Fprintln(w, "| --- | --- | --- |")
		for _, r := range rows {
			fmt.Fprintf(w, "| `%s` | %s | %s |\n", r.path, r.kind, r.state)
		}
		return
	}
	wP, wK := utf8.RuneCountInString("PATH"), utf8.RuneCountInString("KIND")
	for _, r := range rows {
		if l := utf8.RuneCountInString(r.path); l > wP {
			wP = l
		}
		if l := utf8.RuneCountInString(r.kind); l > wK {
			wK = l
		}
	}
	fmt.Fprintf(w, "  %-*s   %-*s   %s\n", wP, "PATH", wK, "KIND", "STATE")
	for _, r := range rows {
		fmt.Fprintf(w, "  %-*s   %-*s   %s\n", wP, r.path, wK, r.kind, r.state)
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
	comm := committedRows(repo)
	pers := PersonalList(home)
	unmanaged := UnmanagedList(repo.Root, filepath.Join(repo.OMK, "placed.tsv"))

	if md {
		fmt.Fprintln(w, "**No omakase harness is installed in this repo.**")
		fmt.Fprintln(w)
		if entries := ctxlayers.Scan(repo, home, ctxlayers.DetectHost(os.Getenv)); len(entries) > 0 {
			// The steering stack works uninstalled too — the empty harness
			// band is the install prompt, and the page doubles as a "how
			// steered is this repo" gauge.
			ctxlayers.RenderStack(w, entries, true)
			fmt.Fprintln(w)
		}
		fmt.Fprintln(w, "### Agent config committed in this repo (managed by git, not omakase)")
		fmt.Fprintln(w)
		renderPathRows(w, comm, true)
		fmt.Fprintln(w)
		renderUnmanaged(w, unmanaged, true)
		renderGlobalLine(w, len(pers), true)
		fmt.Fprintln(w)
		fmt.Fprintln(w, "_A presence check of known paths for known tools — not exhaustive; a file can be present and never read._")
		fmt.Fprintln(w)
		fmt.Fprintln(w, "_Install a harness:_ `omakase init <owner/repo>`")
		return
	}

	fmt.Fprintln(w, "No omakase harness is installed in this repo.")
	fmt.Fprintln(w)
	if entries := ctxlayers.Scan(repo, home, ctxlayers.DetectHost(os.Getenv)); len(entries) > 0 {
		ctxlayers.RenderStack(w, entries, false)
		fmt.Fprintln(w)
	}
	fmt.Fprintln(w, "AGENT CONFIG COMMITTED IN THIS REPO (managed by git, not omakase)")
	renderPathRows(w, comm, false)
	renderUnmanaged(w, unmanaged, false)
	renderGlobalLine(w, len(pers), false)
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
