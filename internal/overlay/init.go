// This file implements the `omakase init` verb: arg parse, payload
// resolution, the wiring / lefthook / incumbent-hook-manager guards, the
// guarded cut-over, the upstream-collision guard, the place loop, the orphan
// sweep, the exclude and .worktreeinclude marked blocks, the snapshot +
// provenance ledger rebuild, the hook-time template installs, and the
// closing summary. Payload files are processed in one lexical walk order;
// iterations over existing state files follow file row order.
//
// The --source arm (shorthand/ref rewrites, the source cache, manifest
// validation, and base+delta merge staging) lives in source.go.
package overlay

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"syscall"

	"github.com/Yuncun/omakase-harness/internal/harness"
	"github.com/Yuncun/omakase-harness/internal/lefthook"
	"github.com/Yuncun/omakase-harness/internal/state"
	"github.com/Yuncun/omakase-harness/internal/templates"
	"github.com/Yuncun/omakase-harness/internal/textblock"
)

// usageText is the `omakase init` usage text; tests pin the exact bytes.
const usageText = "usage: init.sh [<owner/repo[#ref]> | --source <git-url|path>] [--cut-over] [--help]\n" +
	"\n" +
	"Overlay payload/ into the current repo additively (zero committed footprint) and\n" +
	"install lefthook hooks. A payload path the repo already COMMITS is never touched:\n" +
	"it is skipped and reported.\n" +
	"\n" +
	"  <owner/repo[#ref]>\n" +
	"               shorthand for --source https://github.com/owner/repo (optionally pinned to a\n" +
	"               branch or tag with #ref). This is the shareable install line: a harness\n" +
	"               published at github.com/you/harness installs with `init you/harness`.\n" +
	"  --source <git-url|path>\n" +
	"               pull a harness SOURCE — a git repo carrying a payload/ tree plus an\n" +
	"               omakase.manifest (flat key: value; name required, version + recommends optional) —\n" +
	"               into a local cache (${XDG_CACHE_HOME:-~/.cache}/omakase/sources) and inject\n" +
	"               the base harness's payload with the source's payload layered ON TOP (base\n" +
	"               machinery underneath, source wins on overlap), so a source ships only its\n" +
	"               delta and relies on base machinery without keeping its own copy. The source is\n" +
	"               remembered; a later bare init.sh refreshes and re-injects the same source.\n" +
	"  --cut-over   also untrack (git rm --cached) every payload path the repo currently\n" +
	"               commits, so the injected copies take over. With --source this is the MERGED\n" +
	"               base+source set, not only the source delta (a --source install equals a\n" +
	"               built bundle). This STAGES DELETIONS of\n" +
	"               shared files; the next commit applies them for everyone. It prints\n" +
	"               exactly what it will untrack and the consequences, then REFUSES\n" +
	"               unless OMAKASE_CUTOVER_CONFIRM=1 is set. You review and commit the\n" +
	"               staged deletions yourself.\n" +
	"  -h, --help   show this help.\n"

// Scan regexes, compiled once.
var (
	// Full-line YAML comments, skipped by the wiring scan.
	reWiringComment = regexp.MustCompile(`^[[:space:]]*#`)
	// Wired script references (.omakase/….sh).
	reWiringRef = regexp.MustCompile(`\.omakase/[A-Za-z0-9._/-]+\.sh`)
	// A package.json "prepare" script wiring a hook manager.
	rePrepare = regexp.MustCompile(`"prepare"[[:space:]]*:[[:space:]]*"[^"]*(husky|simple-git-hooks)`)
	// The four fixed strip patterns of isStockGitLFSHook; the fifth (the
	// `git lfs <evt>` forward) is line-anchored to the event and built per
	// hook.
	reLFSShebang = regexp.MustCompile(`^#!`)
	reLFSComment = regexp.MustCompile(`^[[:space:]]*#`)
	reLFSBlank   = regexp.MustCompile(`^[[:space:]]*$`)
	reLFSGuard   = regexp.MustCompile(`^[[:space:]]*command -v git-lfs`)
)

// RunInit is the `omakase init` verb. argv is the arguments after the verb.
// It returns the process exit code: 2 for usage errors, 1 for refusals and
// environment errors, 0 on success.
func RunInit(argv []string, stdout, stderr io.Writer) int {
	// ---- arg parse ----
	cutover := false
	source := ""
	for i := 0; i < len(argv); i++ {
		a := argv[i]
		switch {
		case a == "--cut-over":
			cutover = true
		case a == "--source":
			i++
			if i >= len(argv) {
				fmt.Fprintln(stderr, "omakase: --source needs a git URL or local path")
				return 2
			}
			source = argv[i]
		case a == "-h" || a == "--help":
			fmt.Fprint(stdout, usageText)
			return 0
		case strings.HasPrefix(a, "-"):
			fmt.Fprintf(stderr, "omakase: unknown option '%s'\n", a)
			fmt.Fprint(stderr, usageText)
			return 2
		default: // positional: a harness source
			if source != "" {
				fmt.Fprintf(stderr, "omakase: unexpected extra argument '%s' (source already set)\n", a)
				fmt.Fprint(stderr, usageText)
				return 2
			}
			source = a
		}
	}
	// The source string is recorded verbatim in the tab-separated ledger.
	if strings.ContainsAny(source, "\t\n") {
		fmt.Fprintln(stderr, "omakase: --source must not contain a tab or newline")
		return 2
	}

	// ---- repo discovery ----
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

	// ---- one-time ledger schema upgrade ----
	// A pre-v2 (6-column) run ledger is rotated aside. On rename failure the
	// notice is suppressed and the run continues with the old ledger in
	// place.
	ledger := filepath.Join(omk, "ledger.tsv")
	if fileRegular(ledger) && ledgerNeedsRotate(ledger) {
		if err := os.Rename(ledger, ledger+".pre-v2.bak"); err == nil {
			fmt.Fprintln(stdout, "omakase: rotated a pre-v2 (6-column) run ledger aside to ledger.tsv.pre-v2.bak (the new store starts clean).")
		}
	}

	// ---- source precedence ----
	// Payload precedence: --source flag > OMAKASE_PAYLOAD env > remembered
	// source ($OMK/source) > defaultPayload. Suppression of a remembered
	// source keys on OMAKASE_PAYLOAD only; OMAKASE_BASE_PAYLOAD is the merge
	// base the shims hand over, not a suppression key, so a bare re-run
	// never silently downgrades a remembered source to a plain install.
	if source == "" && os.Getenv("OMAKASE_PAYLOAD") == "" {
		if first := state.FirstLine(filepath.Join(omk, "source")); first != "" {
			source = first
		}
	}
	// ---- shorthand / ref / local-dir absolutize ----
	// Applies to both a freshly given source and a remembered one, so a bare
	// re-run round-trips a pinned ref; skipped when source is empty or
	// already names an existing local path. The #ref split can leave source
	// empty (a pathological "#ref"), so the install-arm decision below tests
	// the post-expansion value.
	sourceRef := ""
	if source != "" {
		source, sourceRef = expandSource(source)
	}

	// ---- payload resolution: --source merge, or the plain default ----
	// A non-empty (post-expansion) source fetches into the disposable cache
	// and merges the base payload under the source delta; otherwise the
	// payload is OMAKASE_PAYLOAD or the binary-relative default. sourceLabel
	// (placed.tsv column 3), rememberedSource ($OMK/source), and recommends
	// (the summary) are source-only.
	sourceLabel := "payload" // the source arm overrides this
	rememberedSource := ""
	recommends := ""
	var payload string
	if source != "" {
		res, code := runSource(source, sourceRef, defaultPayload(), stdout, stderr)
		if code != 0 {
			return code // runSource printed the message + cleaned any staging dir
		}
		defer os.RemoveAll(res.merged) // clean up the merge staging dir
		payload = res.payload
		sourceLabel = res.label
		rememberedSource = res.remembered
		recommends = res.recommends
	} else {
		// OMAKASE_PAYLOAD overrides; otherwise defaultPayload
		// (OMAKASE_BASE_PAYLOAD, else the binary-relative ../payload).
		payload = os.Getenv("OMAKASE_PAYLOAD")
		if payload == "" {
			payload = defaultPayload()
		}
	}
	// Strip one trailing slash so rel derivation stays clean; a pathological
	// OMAKASE_PAYLOAD=/ collapses to "" and is rejected below.
	payload = strings.TrimSuffix(payload, "/")
	if info, statErr := os.Stat(payload); statErr != nil || !info.IsDir() {
		fmt.Fprintf(stderr, "omakase: payload dir not found at %s\n", payload)
		return 1
	}

	// ---- walk the payload ----
	// One stable lexical order feeds the cut-over loop, the place loop,
	// placed.tsv, the snapshot copies, the exclude/wtinc derivation, and the
	// summary.
	payloadRels, err := walkPayload(payload)
	if err != nil {
		// Reachable only for a payload with an unreadable child; aborts the
		// run silently.
		return 1
	}

	// ---- fail-closed wiring guard ----
	wiring := filepath.Join(payload, "lefthook-local.yml")
	if fileRegular(wiring) {
		missing := ""
		for _, ref := range wiringRefs(wiring) {
			if !fileRegular(filepath.Join(payload, ref)) {
				missing += " " + ref
			}
		}
		if missing != "" {
			fmt.Fprintf(stderr, "omakase: hook wiring references script(s) the payload does not ship:%s\n", missing)
			fmt.Fprintln(stderr, "  These would fail at commit time (exit 127). Fix lefthook-local.yml or ship the script(s). Nothing was placed.")
			return 1
		}
	}

	// ---- lefthook resolution, fetching if needed ----
	lhPrefix, ok := lefthook.ResolveForInit(root, stderr)
	if !ok {
		lefthook.Guidance(stderr)
		return 1
	}

	const begin = "# >>> omakase-harness >>>"
	const end = "# <<< omakase-harness <<<"
	// The exclude file and hooks dir live in the shared git dir, so a linked
	// worktree (where $ROOT/.git is a file) resolves correctly.
	exclude := filepath.Join(common, "info", "exclude")
	hooksDir := filepath.Join(common, "hooks")

	// ---- incumbent hook-manager guard ----
	var incumbent []string
	resetHooksPath := false
	hookspath := gitOutTrim(root, "config", "--get", "core.hooksPath")
	if hookspath != "" {
		var hpAbs string
		if strings.HasPrefix(hookspath, "/") {
			hpAbs = hookspath
		} else {
			hpAbs = filepath.Join(root, hookspath)
		}
		hpAbs = physicalResolve(hpAbs)
		stdAbs := physicalResolve(hooksDir)
		if hpAbs != stdAbs {
			incumbent = append(incumbent, "core.hooksPath = '"+hookspath+"' (a foreign hook manager owns the hooks dir; husky v9 sets .husky/_)")
		} else {
			// Redundant config: names the default location, but lefthook refuses
			// to install while any core.hooksPath is set. Flagged here, cleared
			// just before install, so a refusal below mutates nothing.
			resetHooksPath = true
		}
	}
	if strings.TrimRight(gitStdout(root, "ls-files", "--", ".husky"), "\n") != "" {
		incumbent = append(incumbent, ".husky/ content is git-tracked (the project's own husky setup)")
	} else if isDir(filepath.Join(root, ".husky")) && !isDir(filepath.Join(payload, ".husky")) {
		incumbent = append(incumbent, ".husky/ directory (husky)")
	}
	if fileRegular(filepath.Join(root, "package.json")) && fileMatchesLine(filepath.Join(root, "package.json"), rePrepare) {
		incumbent = append(incumbent, "package.json \"prepare\" script wires a hook manager (husky / simple-git-hooks) — npm install would overwrite lefthook's hooks")
	}
	preCommitConfig := fileRegular(filepath.Join(root, ".pre-commit-config.yaml"))
	for _, hf := range sortedHookFiles(hooksDir) {
		if !fileRegular(hf) {
			continue
		}
		if strings.HasSuffix(hf, ".sample") || strings.HasSuffix(hf, ".old") {
			continue
		}
		content, rerr := os.ReadFile(hf)
		if rerr != nil {
			continue
		}
		if bytes.Contains(bytes.ToLower(content), []byte("lefthook")) {
			continue
		}
		if isStockGitLFSHook(hf, content) {
			continue // lefthook absorbs git-lfs natively — not a rival manager
		}
		base := filepath.Base(hf)
		if preCommitConfig && (bytes.Contains(content, []byte("pre-commit.com")) || bytes.Contains(content, []byte("generated by pre-commit"))) {
			incumbent = append(incumbent, base+": installed pre-commit-framework stub (plus .pre-commit-config.yaml)")
		} else {
			incumbent = append(incumbent, base+": existing non-lefthook hook in "+hooksDir)
		}
	}
	if len(incumbent) > 0 {
		fmt.Fprintln(stderr, "omakase: REFUSING to install — an incumbent hook manager is present:")
		for _, i := range incumbent {
			fmt.Fprintf(stderr, "  - %s\n", i)
		}
		fmt.Fprintln(stderr, "  'lefthook install' would displace the project's own hooks (renaming them to .old),")
		fmt.Fprintln(stderr, "  silently disabling its gates — and a husky prepare script would overwrite lefthook")
		fmt.Fprintln(stderr, "  back on the next npm install. omakase does not chain hook managers (v1).")
		fmt.Fprintln(stderr, "  If these are stale leftovers, remove them and re-run. If the project really uses")
		fmt.Fprintln(stderr, "  them, do not install omakase here. Nothing was changed.")
		return 1
	}

	// ---- guarded cut-over ----
	if cutover {
		var cut []string
		for _, rel := range payloadRels {
			if gitTracked(root, rel) {
				cut = append(cut, rel)
			}
		}
		if len(cut) == 0 {
			fmt.Fprintln(stdout, "omakase: --cut-over: no payload path is tracked by this repo — nothing to cut over.")
		} else {
			fmt.Fprintf(stdout, "omakase: cut-over will run  git rm --cached  on %d tracked file(s):\n", len(cut))
			for _, c := range cut {
				fmt.Fprintf(stdout, "    %s\n", c)
			}
			fmt.Fprintln(stdout, "  This STAGES A DELETION of each shared file. The next commit — including an agent")
			fmt.Fprintln(stdout, "  auto-commit — applies that deletion FOR EVERYONE who pulls it, and upstream changes")
			fmt.Fprintln(stdout, "  to these files will then produce modify/delete conflicts. The files stay on disk;")
			fmt.Fprintln(stdout, "  the injected (gitignored) copies take over locally. Undo before committing with")
			fmt.Fprintln(stdout, "  'git restore --staged <file>'; 'git add <file>' re-tracks later.")
			if os.Getenv("OMAKASE_CUTOVER_CONFIRM") != "1" {
				fmt.Fprintln(stderr, "omakase: REFUSING cut-over without confirmation. Re-run with OMAKASE_CUTOVER_CONFIRM=1 to proceed. Nothing was changed.")
				return 1
			}
			args := append([]string{"-C", root, "rm", "--cached", "-q", "--"}, cut...)
			cmd := exec.Command("git", args...)
			cmd.Stdout = stdout
			cmd.Stderr = stderr
			if runErr := cmd.Run(); runErr != nil {
				return exitCode(runErr) // a git rm failure aborts with its code
			}
			fmt.Fprintf(stdout, "omakase: cut-over staged %d deletion(s) — review with 'git status' and commit them yourself.\n", len(cut))
		}
	}

	// ---- upstream-collision guard ----
	// Prior placed paths from placed.tsv col 1 (fallback placed.list), in
	// file row order.
	var priorPaths []string
	if fileRegular(filepath.Join(omk, "placed.tsv")) {
		priorPaths = firstFieldsTSV(filepath.Join(omk, "placed.tsv"))
	} else if fileRegular(filepath.Join(omk, "placed.list")) {
		priorPaths = firstFieldsTSV(filepath.Join(omk, "placed.list"))
	}
	for _, rel := range priorPaths {
		if rel == "" {
			continue
		}
		if !gitTracked(root, rel) {
			continue
		}
		snap := filepath.Join(omk, "payload-snapshot", rel)
		if lexists(snap) {
			// The last-injected copy would be destroyed by the snapshot
			// rebuild; preserve it under $OMK/clobbered/.
			if mkErr := os.MkdirAll(filepath.Join(omk, "clobbered", filepath.Dir(rel)), 0o755); mkErr != nil {
				return 1
			}
			if cpErr := CopyEntry(snap, filepath.Join(omk, "clobbered", rel)); cpErr != nil {
				return 1
			}
		}
		fmt.Fprintf(stderr, "omakase: WARNING — '%s' was injected (personal, gitignored) but is NOW TRACKED by the repo.\n", rel)
		fmt.Fprintln(stderr, "  An upstream commit likely landed a file at this path; git silently overwrites ignored")
		fmt.Fprintln(stderr, "  files on checkout/pull, so your personal copy was likely clobbered. Last-injected copy")
		fmt.Fprintln(stderr, "  preserved at:")
		fmt.Fprintf(stderr, "    %s\n", filepath.Join(omk, "clobbered", rel))
		fmt.Fprintf(stderr, "  Diff it against the tracked file and reconcile: drop '%s' from your payload, or run\n", rel)
		fmt.Fprintln(stderr, "  init --cut-over (guarded) to untrack the file and let the injected copy take over.")
	}

	// ---- consent merge ----
	// A row a prior toggle disabled (enabled=0) stays disabled across re-init:
	// the file is not re-placed, but its snapshot + ledger row are refreshed so
	// `omakase status --enable` can restore the current payload copy later.
	declined := map[string]bool{}
	for _, row := range state.ReadPlaced(filepath.Join(omk, "placed.tsv")) {
		// Machinery is never a consent item (the toggles refuse it), so an
		// enabled=0 machinery row can only be a pre-guard binary's leftover —
		// honoring it would keep the gate primitive missing on every re-init.
		// Ignore it: init re-places the file and the row returns to enabled=1.
		if row.Enabled == "0" && !harness.IsMachinery(row.Rel) {
			declined[row.Rel] = true
		}
	}
	var declinedKept []string

	umask := currentUmask()

	// ---- place loop ----
	var placed, skipped, overwrote []string
	for _, rel := range payloadRels {
		f := filepath.Join(payload, rel)
		dest := filepath.Join(root, rel)
		// Never touch a path the repo tracks (committed file wins).
		if gitTracked(root, rel) {
			skipped = append(skipped, rel)
			fmt.Fprintf(stderr, "omakase: SKIP (already tracked) %s\n", rel)
			continue
		}
		if declined[rel] {
			declinedKept = append(declinedKept, rel)
			fmt.Fprintf(stderr, "omakase: SKIP (toggled off) %s — re-enable: omakase status --enable %s\n", rel, rel)
			continue
		}
		// Fresh placement: nothing there yet.
		if !lexists(dest) {
			if code := placeFile(f, rel, root, umask, stderr); code != 0 {
				return code
			}
			placed = append(placed, rel)
			continue
		}
		// Already current: an untracked copy identical to the payload — leave it.
		if SameFile(dest, f) {
			placed = append(placed, rel)
			continue
		}
		// Differs and not committed: overwrite, preserving the pre-existing
		// copy first (best-effort; a real directory dest is left for
		// placeFile to refuse). A backup failure warns rather than aborting.
		saved := ""
		if !isDir(dest) || isSymlink(dest) {
			if tryClobberBackup(dest, rel, omk) {
				saved = filepath.Join(omk, "clobbered", rel)
			} else {
				fmt.Fprintf(stderr, "omakase: WARNING — could not back up pre-existing '%s' before overwriting it\n", rel)
			}
		}
		if code := placeFile(f, rel, root, umask, stderr); code != 0 {
			return code
		}
		placed = append(placed, rel)
		overwrote = append(overwrote, rel)
		if saved != "" {
			fmt.Fprintf(stderr, "omakase: overwrote %s to match payload (prior copy preserved at %s)\n", rel, saved)
		} else {
			fmt.Fprintf(stderr, "omakase: overwrote %s to match payload (any local edit was replaced)\n", rel)
		}
	}

	// ---- orphan sweep ----
	// Prior ledger rows in file order: a still-placed path is kept; a
	// tracked or already-gone path is skipped; harness residue that still
	// hashes to what init placed is deleted (and empty dirs pruned); a local
	// edit is warned about and kept.
	var swept []string
	if fileRegular(filepath.Join(omk, "placed.tsv")) {
		for _, row := range state.ReadPlaced(filepath.Join(omk, "placed.tsv")) {
			rel := row.Rel
			if rel == "" {
				continue
			}
			if contains(placed, rel) {
				continue
			}
			if gitTracked(root, rel) {
				continue // tracked: upstream owns it (collision guard warned above)
			}
			if !lexists(filepath.Join(root, rel)) {
				continue // already gone
			}
			if state.HashOf(filepath.Join(root, rel)) == row.Hash {
				if delErr := DeletePlaced(root, rel, func(r string) bool { return gitTracked(root, r) }); delErr != nil {
					return 1
				}
				swept = append(swept, rel)
			} else {
				fmt.Fprintf(stderr, "omakase: WARNING — '%s' was placed by a prior init, is no longer in the payload, and differs from what init placed (a local edit?). Leaving it; delete it yourself if unwanted.\n", rel)
			}
		}
	}

	// ---- exclude block ----
	lefthookTracked := gitTracked(root, "lefthook.yml")
	wtincTracked := gitTracked(root, ".worktreeinclude")
	if wtincTracked {
		fmt.Fprintln(stderr, "omakase: .worktreeinclude is tracked — leaving it untouched (re-run omakase init inside a new manual worktree to install it there).")
	}
	isDirRoot := func(p string) bool { return isDir(filepath.Join(root, p)) }
	prefixes := DerivePrefixes(append(append([]string{}, placed...), declinedKept...), harness.SharedTopdirs, isDirRoot, lefthookTracked, wtincTracked)

	if err := os.MkdirAll(filepath.Dir(exclude), 0o755); err != nil {
		return 1
	}
	if err := touch(exclude); err != nil {
		return 1
	}
	// Exclude entries are root-anchored with a leading "/": an unanchored
	// gitignore pattern matches at any depth, so ".omakase/" would also hide
	// a project's own "payload/.omakase". The anchoring is applied only at
	// the exclude write; the shared prefixes slice stays unanchored because
	// the .worktreeinclude block below feeds Claude Code's own matcher.
	anchored := make([]string, len(prefixes))
	for i, p := range prefixes {
		anchored[i] = "/" + p
	}
	excludeContent, _ := os.ReadFile(exclude)
	excludeOut := textblock.AppendBlock(textblock.Strip(excludeContent, begin, end), begin, anchored, end)
	if err := rewriteFile(exclude, excludeOut); err != nil {
		return 1
	}

	// ---- .worktreeinclude block ----
	// Written only when the repo does not track .worktreeinclude and
	// something was placed. Reuses the exclude block's prefixes, skipping
	// the ".worktreeinclude" entry itself (compared with any trailing "/"
	// trimmed), whether it came from the wiring append or from a placed
	// path.
	if !wtincTracked && len(placed)+len(declinedKept) > 0 {
		wtinc := filepath.Join(root, ".worktreeinclude")
		if err := touch(wtinc); err != nil {
			return 1
		}
		var wtEntries []string
		for _, p := range prefixes {
			if strings.TrimSuffix(p, "/") == ".worktreeinclude" {
				continue
			}
			wtEntries = append(wtEntries, p)
		}
		wtContent, _ := os.ReadFile(wtinc)
		wtOut := textblock.AppendBlock(textblock.Strip(wtContent, begin, end), begin, wtEntries, end)
		if err := rewriteFile(wtinc, wtOut); err != nil {
			return 1
		}
	}

	// ---- snapshot + provenance ledger ----
	if err := os.RemoveAll(filepath.Join(omk, "payload-snapshot")); err != nil {
		return 1
	}
	if err := os.MkdirAll(filepath.Join(omk, "payload-snapshot"), 0o755); err != nil {
		return 1
	}
	// Remember a source install so a bare re-run refreshes the same source.
	// A plain install (rememberedSource == "") leaves any remembered source
	// in place; the precedence above decides who wins.
	if rememberedSource != "" {
		if err := os.WriteFile(filepath.Join(omk, "source"), []byte(rememberedSource+"\n"), 0o644); err != nil {
			return 1
		}
	}
	var rows []state.PlacedRow
	for _, rel := range placed {
		if rel == "" {
			continue
		}
		// safeMkdirAll: never write a snapshot copy through a directory
		// symlink out of the snapshot root.
		snapRoot := filepath.Join(omk, "payload-snapshot")
		if err := safeMkdirAll(snapRoot, filepath.Join(snapRoot, filepath.Dir(rel))); err != nil {
			fmt.Fprintf(stderr, "omakase: %v\n", err)
			return 1
		}
		if err := CopyEntry(filepath.Join(root, rel), filepath.Join(omk, "payload-snapshot", rel)); err != nil {
			// A mid-loop failure exits 1 with the prior placed.tsv intact:
			// WritePlaced runs only after this loop finishes.
			return 1
		}
		rows = append(rows, state.PlacedRow{
			Rel:     rel,
			Kind:    harness.KindOf(rel),
			Src:     sourceLabel,
			Hash:    state.HashOf(filepath.Join(root, rel)),
			Enabled: "1",
		})
	}
	for _, rel := range declinedKept {
		src := filepath.Join(payload, rel)
		snapRoot := filepath.Join(omk, "payload-snapshot")
		if err := safeMkdirAll(snapRoot, filepath.Join(snapRoot, filepath.Dir(rel))); err != nil {
			fmt.Fprintf(stderr, "omakase: %v\n", err)
			return 1
		}
		if err := CopyEntry(src, filepath.Join(snapRoot, rel)); err != nil {
			return 1
		}
		rows = append(rows, state.PlacedRow{
			Rel:     rel,
			Kind:    harness.KindOf(rel),
			Src:     sourceLabel,
			Hash:    state.HashOf(src), // hash of what would be placed (the payload copy)
			Enabled: "0",
		})
	}
	if err := state.WritePlaced(filepath.Join(omk, "placed.tsv"), rows); err != nil {
		return 1
	}

	// Heal a placed gate script that a stale (pre-2b) payload just
	// (re)placed; otherwise a bare re-init would revert an already-healed
	// script, silently re-arming a gate the human disabled. healGateScript
	// no-ops when the script is absent, already 2b-capable, or git-tracked
	// (warning), and otherwise rewrites it and refreshes the snapshot and
	// ledger hash so drift detection stays quiet.
	if err := healGateScript(repo, stderr, false); err != nil {
		fmt.Fprintf(stderr, "omakase: %v\n", err)
		return 1
	}
	removeF(filepath.Join(omk, "placed.list")) // pre-0.10 record — superseded

	// ---- install the three hook-time templates ----
	for _, name := range []string{"ensure-present.sh", "verify-overlay.sh", "install-guards.sh"} {
		if instErr := templates.Install(name, filepath.Join(omk, name)); instErr != nil {
			fmt.Fprintln(stderr, instErr.Error())
			return 1
		}
	}

	// ---- redundant hooksPath reset ----
	if resetHooksPath {
		exec.Command("git", "-C", root, "config", "--unset", "core.hooksPath").Run() // 2>/dev/null || true
		fmt.Fprintln(stdout, "omakase: cleared redundant core.hooksPath (it named the repo's own hooks dir; lefthook refuses to install while it is set — the effective hooks dir is unchanged).")
	}

	// ---- lefthook install, from root, streams inherited ----
	lhArgs := append(append([]string{}, lhPrefix...), "install")
	lhCmd := exec.Command(lhArgs[0], lhArgs[1:]...)
	lhCmd.Dir = root
	lhCmd.Stdout = stdout
	lhCmd.Stderr = stderr
	if runErr := lhCmd.Run(); runErr != nil {
		return exitCode(runErr) // a `lefthook install` failure aborts with its code
	}

	// ---- lefthook.yml heal snapshot ----
	// `lefthook install` writes an example lefthook.yml skeleton when the repo
	// ships no config. omakase never placed it, so it is absent from the
	// ledger and the worktree heal loop would leave a linked worktree without
	// it, making lefthook print a sync-hooks failure on every hook run there.
	// Snapshot the untracked file to $OMK/lefthook.yml, outside the ledger (a
	// ledger row would draw re-init sweep and drift warnings on a file the
	// user may edit); ensure-present.sh heals it into a worktree that lacks
	// it. A tracked or absent lefthook.yml clears any stale snapshot so the
	// heal source cannot go stale.
	lefthookSnap := filepath.Join(omk, "lefthook.yml")
	if fileRegular(filepath.Join(root, "lefthook.yml")) && !lefthookTracked {
		if err := CopyEntry(filepath.Join(root, "lefthook.yml"), lefthookSnap); err != nil {
			return 1
		}
	} else if err := removeF(lefthookSnap); err != nil {
		return 1
	}

	// ---- install the hook-stub guard blocks ----
	// install-guards.sh resolves the shared git dir from this process's cwd
	// (inside the repo), streams inherited.
	igCmd := exec.Command("sh", filepath.Join(omk, "install-guards.sh"))
	igCmd.Stdout = stdout
	igCmd.Stderr = stderr
	if runErr := igCmd.Run(); runErr != nil {
		return exitCode(runErr)
	}

	// ---- summary ----
	fmt.Fprintf(stdout, "omakase: placed %d file(s), overwrote %d to match payload, skipped %d committed path(s).\n", len(placed), len(overwrote), len(skipped))
	for _, p := range placed {
		if p != "" {
			fmt.Fprintf(stdout, "  + %s\n", p)
		}
	}
	for _, o := range overwrote {
		if o != "" {
			fmt.Fprintf(stdout, "  ^ overwrote to match payload (any local edit replaced): %s\n", o)
		}
	}
	for _, w := range swept {
		if w != "" {
			fmt.Fprintf(stdout, "  - removed (placed by a prior init, no longer in the payload): %s\n", w)
		}
	}
	for _, s := range skipped {
		if s != "" {
			fmt.Fprintf(stdout, "  ~ skipped (committed — re-run with --cut-over to let the harness copy take over; guarded, see init.sh --help): %s\n", s)
		}
	}
	fmt.Fprintln(stdout, "omakase: ignores -> .git/info/exclude; hooks installed; new worktrees auto-install the harness. Nothing to commit.")
	fmt.Fprintln(stdout, "omakase: see the whole harness any time with  omakase status")
	// A source's manifest recommends: line; only a source install sets it.
	if recommends != "" {
		fmt.Fprintf(stdout, "omakase: this harness recommends — %s\n", recommends)
	}
	fmt.Fprintln(stdout, "omakase: to customize, fork the harness source (clone -> edit -> publish) and")
	fmt.Fprintln(stdout, "         init from your copy; do not edit injected files in place (overwritten on re-init).")
	if fileRegular(filepath.Join(root, ".omakase", "bin", "omakase-statusline.sh")) {
		fmt.Fprintln(stdout, "omakase: status line — compose the scorecard into your existing bar (it never")
		fmt.Fprintln(stdout, "         takes over the bar). Add this command to your status-line script:")
		fmt.Fprintf(stdout, "           bash %s/.omakase/bin/omakase-statusline.sh\n", root)
		fmt.Fprintln(stdout, "         Claude Code: your ~/.claude statusLine script. Copilot CLI: ~/.copilot. tmux: status-right.")
	}
	if fileRegular(filepath.Join(root, ".omakase", "bin", "omakase-stop-notice.sh")) {
		fmt.Fprintln(stdout, "omakase: end-of-turn notice (Claude Code only, opt-in) — a one-line 'harness active'")
		fmt.Fprintln(stdout, "         status when a turn ends. Enable by adding a Stop hook to .claude/settings.json:")
		fmt.Fprintln(stdout, "           bash $CLAUDE_PROJECT_DIR/.omakase/bin/omakase-stop-notice.sh")
	}
	if fileRegular(filepath.Join(root, ".omakase", "bin", "omakase-worktree-guard.sh")) {
		fmt.Fprintln(stdout, "omakase: worktree guard (Claude Code only, opt-in) — while other worktrees are active,")
		fmt.Fprintln(stdout, "         denies edits to product files in the MAIN checkout before they happen. Enable by")
		fmt.Fprintln(stdout, "         adding a PreToolUse hook (matcher \"Edit|Write\") to .claude/settings.json:")
		fmt.Fprintln(stdout, "           bash $CLAUDE_PROJECT_DIR/.omakase/bin/omakase-worktree-guard.sh")
	}
	return 0
}

// placeFile places one payload file at root/rel: creates the dest parent,
// refuses a real (non-symlink) directory dest with exit 1 (leaving prior
// placements in place), otherwise copies via CopyEntry and adds execute bits
// (masked by umask) iff the dest is a *.sh regular file.
func placeFile(src, rel, root string, umask os.FileMode, stderr io.Writer) int {
	dest := filepath.Join(root, rel)
	// safeMkdirAll: a prior placement may have put a directory symlink at a
	// parent, and writing the child through it would land outside the repo.
	if err := safeMkdirAll(root, filepath.Dir(dest)); err != nil {
		fmt.Fprintf(stderr, "omakase: %v\n", err)
		return 1
	}
	if isDir(dest) && !isSymlink(dest) {
		fmt.Fprintf(stderr, "omakase: refusing to overlay file '%s' — an untracked directory exists there; remove it and re-run\n", rel)
		return 1
	}
	if err := CopyEntry(src, dest); err != nil {
		return 1
	}
	if strings.HasSuffix(rel, ".sh") && !isSymlink(dest) {
		if info, err := os.Stat(dest); err == nil {
			// chmod +x: add execute bits, masked by umask.
			os.Chmod(dest, info.Mode().Perm()|(0o111&^umask))
		}
	}
	return 0
}

// tryClobberBackup copies the live dest into $OMK/clobbered/rel, creating
// the parent and removing any stale backup first. Returns true iff every
// step succeeds.
func tryClobberBackup(dest, rel, omk string) bool {
	if err := os.MkdirAll(filepath.Join(omk, "clobbered", filepath.Dir(rel)), 0o755); err != nil {
		return false
	}
	if err := removeF(filepath.Join(omk, "clobbered", rel)); err != nil {
		return false
	}
	return CopyEntry(dest, filepath.Join(omk, "clobbered", rel)) == nil
}

// walkPayload lists the payload's regular files and symlinks as clean
// payload-relative paths, in filepath.WalkDir's lexical order. Directories
// and other special files are excluded; symlinks are never followed.
func walkPayload(payload string) ([]string, error) {
	var rels []string
	err := filepath.WalkDir(payload, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		t := d.Type()
		if t.IsRegular() || t&os.ModeSymlink != 0 {
			rel, rerr := filepath.Rel(payload, path)
			if rerr != nil {
				return rerr
			}
			rels = append(rels, rel)
		}
		return nil
	})
	return rels, err
}

// wiringRefs extracts the unique `.omakase/….sh` references from path's
// non-comment lines, sorted.
func wiringRefs(path string) []string {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	seen := make(map[string]bool)
	var refs []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		if reWiringComment.MatchString(line) {
			continue
		}
		for _, m := range reWiringRef.FindAllString(line, -1) {
			if !seen[m] {
				seen[m] = true
				refs = append(refs, m)
			}
		}
	}
	sort.Strings(refs)
	return refs
}

// isStockGitLFSHook reports whether hf is the pristine stub `git lfs
// install` writes: the right basename, git-lfs's own presence guard, and
// nothing left once the shebang, comments, blank lines, the presence guard,
// and the single anchored `git lfs <evt>` forward are stripped.
func isStockGitLFSHook(hf string, content []byte) bool {
	evt := filepath.Base(hf)
	switch evt {
	case "post-checkout", "post-commit", "post-merge", "pre-push":
	default:
		return false
	}
	if !bytes.Contains(content, []byte("command -v git-lfs")) {
		return false
	}
	// The forward strip is anchored to the whole line; a line that merely
	// contains the substring is not stripped.
	p5 := regexp.MustCompile(`^[[:space:]]*(exec[[:space:]]+)?git lfs ` + regexp.QuoteMeta(evt) + `([[:space:]]+"\$@")?[[:space:]]*$`)
	sc := bufio.NewScanner(bytes.NewReader(content))
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		if reLFSShebang.MatchString(line) || reLFSComment.MatchString(line) ||
			reLFSBlank.MatchString(line) || reLFSGuard.MatchString(line) || p5.MatchString(line) {
			continue // stripped
		}
		return false // a surviving line means it does extra work
	}
	return true // nothing survived: pristine stub
}

// ledgerNeedsRotate reports whether any row has >= 6 tab-separated fields
// (the pre-v2 schema).
func ledgerNeedsRotate(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		if len(strings.Split(sc.Text(), "\t")) >= 6 {
			return true
		}
	}
	return false
}

// firstFieldsTSV returns each line's substring before the first tab (the
// whole line when there is no tab), skipping empty fields. Also serves the
// placed.list fallback (whole lines, no tabs).
func firstFieldsTSV(path string) []string {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	s := string(content)
	if s == "" {
		return nil
	}
	lines := strings.Split(s, "\n")
	if strings.HasSuffix(s, "\n") {
		lines = lines[:len(lines)-1] // no record after a final newline
	}
	var out []string
	for _, line := range lines {
		field := line
		if i := strings.IndexByte(line, '\t'); i >= 0 {
			field = line[:i]
		}
		if field == "" {
			continue
		}
		out = append(out, field)
	}
	return out
}

// sortedHookFiles lists hooksDir's entries as full paths in lexical order,
// excluding dot-prefixed names. A missing dir yields nothing.
func sortedHookFiles(hooksDir string) []string {
	entries, err := os.ReadDir(hooksDir) // already name-sorted
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}
		out = append(out, filepath.Join(hooksDir, e.Name()))
	}
	return out
}

// physicalResolve returns the symlink-resolved absolute path when p is an
// existing directory, else p unchanged.
func physicalResolve(p string) string {
	info, err := os.Stat(p)
	if err != nil || !info.IsDir() {
		return p
	}
	resolved, err := filepath.EvalSymlinks(p)
	if err != nil {
		return p
	}
	return resolved
}

// --- small predicates / helpers ---

// gitTracked is `git -C root ls-files --error-unmatch -- rel` exit 0. On a
// case-insensitive filesystem (core.ignorecase, which git init sets there)
// a tracked file differing from rel only in case occupies the same disk
// path, but exact pathspec matching misses it — writing or deleting
// root/rel would hit the tracked file — so the check is retried with the
// case-folding `:(icase)` pathspec.
func gitTracked(root, rel string) bool {
	if rel == "" {
		return false // :(icase) with an empty pattern matches every tracked file
	}
	if exec.Command("git", "-C", root, "ls-files", "--error-unmatch", "--", rel).Run() == nil {
		return true
	}
	if gitOutTrim(root, "config", "--get", "--type=bool", "core.ignorecase") != "true" {
		return false
	}
	return exec.Command("git", "-C", root, "ls-files", "--error-unmatch", "--", ":(icase)"+rel).Run() == nil
}

// gitStdout returns a git command's stdout; stderr is discarded and the exit
// code ignored.
func gitStdout(root string, args ...string) string {
	out, _ := exec.Command("git", append([]string{"-C", root}, args...)...).Output()
	return string(out)
}

// gitOutTrim returns a git command's stdout with trailing newlines stripped,
// "" on any error.
func gitOutTrim(root string, args ...string) string {
	return strings.TrimRight(gitStdout(root, args...), "\n")
}

func isDir(p string) bool {
	info, err := os.Stat(p) // follows symlinks
	return err == nil && info.IsDir()
}

func isSymlink(p string) bool {
	info, err := os.Lstat(p)
	return err == nil && info.Mode()&os.ModeSymlink != 0
}

// fileRegular reports whether p exists (following symlinks) and is a
// regular file.
func fileRegular(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.Mode().IsRegular()
}

// lexists reports whether the path is present as any type, including a
// dangling symlink.
func lexists(p string) bool {
	_, err := os.Lstat(p)
	return err == nil
}

// fileMatchesLine reports whether any line of path matches re. Matching is
// line-oriented, so a negated class like `[^"]` cannot span lines.
func fileMatchesLine(path string, re *regexp.Regexp) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		if re.Match(sc.Bytes()) {
			return true
		}
	}
	return false
}

// removeF removes p, treating a missing file as success.
func removeF(p string) error {
	if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// touch creates p if missing, without truncating an existing file.
func touch(p string) error {
	f, err := os.OpenFile(p, os.O_CREATE, 0o644)
	if err != nil {
		return err
	}
	return f.Close()
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// currentUmask reads the process umask without permanently changing it.
func currentUmask() os.FileMode {
	u := syscall.Umask(0)
	syscall.Umask(u)
	return os.FileMode(u)
}

// exitCode extracts a child process's exit code from an *exec.ExitError,
// defaulting to 1 for any other error.
func exitCode(err error) int {
	if ee, ok := err.(*exec.ExitError); ok {
		if code := ee.ExitCode(); code > 0 {
			return code
		}
	}
	return 1
}
