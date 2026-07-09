// This file (init.go) ports the `omakase init` verb — the engine of
// bin/init.sh: arg parse, payload resolution, the wiring / lefthook /
// incumbent-hook-manager guards, the guarded cut-over, the upstream-collision
// guard, the place loop, the orphan sweep, the exclude + .worktreeinclude
// marked blocks, the snapshot + provenance ledger rebuild, the three hook-time
// template installs, and the closing summary. It reproduces bin/init.sh's
// stdout/stderr bytes, exit codes, side-effect ordering, and on-disk state.
//
// bin/init.sh STAYS bash, untouched: the frozen v1 suite still runs through it.
// This Go verb goes live only at Task 6's shim cutover; init_test.go is this
// task's safety net.
//
// The one sanctioned divergence from bin/init.sh is walk order (Global
// Constraint 6): v1 places files in `find`'s readdir order (fs-dependent,
// never guaranteed); this walks the payload with filepath.WalkDir (lexical).
// That order flows consistently into placed.tsv rows, snapshot copies,
// exclude-block derivation, and the +/^/~/- summary lines — see the walk site
// below. Iterations over EXISTING state files (the collision guard and the
// orphan sweep) follow FILE ROW ORDER, matching v1 exactly — no divergence
// there.
//
// The `--source` arm — shorthand/ref rewrites, the disposable source cache,
// the fail-closed manifest validation, and the base+delta merge staging — lives
// in source.go (expandSource + runSource); RunInit wires its source-conditional
// tails (placed.tsv column 3, $OMK/source, the summary `recommends:` line)
// through this engine. The OMAKASE_PAYLOAD env and the binary-relative
// ../payload default resolution live here (defaultPayload, in source.go).
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

// usageText is the byte-exact usage heredoc of bin/init.sh:13-41 (Global
// Constraint 2). It still reads "init.sh" and prints the source docs even
// though this port stubs the source arm — the entry-point text is frozen;
// Task 6 owns any rename.
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

// Regexes ported from bin/init.sh, compiled once.
var (
	// awk `!/^[[:space:]]*#/` — the wiring scan skips full-line YAML comments
	// (bin/init.sh:218).
	reWiringComment = regexp.MustCompile(`^[[:space:]]*#`)
	// grep -oE '\.omakase/[A-Za-z0-9._/-]+\.sh' — wired script references
	// (bin/init.sh:218).
	reWiringRef = regexp.MustCompile(`\.omakase/[A-Za-z0-9._/-]+\.sh`)
	// grep -Eq '"prepare"...(husky|simple-git-hooks)' — package.json hook-manager
	// prepare script (bin/init.sh:316).
	rePrepare = regexp.MustCompile(`"prepare"[[:space:]]*:[[:space:]]*"[^"]*(husky|simple-git-hooks)`)
	// The four fixed strip patterns of is_stock_git_lfs_hook (bin/init.sh:273-277);
	// the fifth (the `git lfs <evt>` forward) is line-anchored to the event and
	// built per hook.
	reLFSShebang = regexp.MustCompile(`^#!`)
	reLFSComment = regexp.MustCompile(`^[[:space:]]*#`)
	reLFSBlank   = regexp.MustCompile(`^[[:space:]]*$`)
	reLFSGuard   = regexp.MustCompile(`^[[:space:]]*command -v git-lfs`)
)

// RunInit is the `omakase init` verb. argv is the arguments AFTER the verb
// (flags only), the same shape status.Run receives. It writes to stdout/stderr
// and returns the process exit code: 2 for usage/arg errors, 1 for refusals and
// environment errors, 0 on success (Global Constraint 2).
func RunInit(argv []string, stdout, stderr io.Writer) int {
	// ---- arg parse (bin/init.sh:44-59) ----
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
		default: // positional: a harness SOURCE (bin/init.sh:52-54)
			if source != "" {
				fmt.Fprintf(stderr, "omakase: unexpected extra argument '%s' (source already set)\n", a)
				fmt.Fprint(stderr, usageText)
				return 2
			}
			source = a
		}
	}
	// TSV column safety: the source string is recorded verbatim in the
	// TAB-separated ledger (bin/init.sh:59).
	if strings.ContainsAny(source, "\t\n") {
		fmt.Fprintln(stderr, "omakase: --source must not contain a tab or newline")
		return 2
	}

	// ---- repo discovery (bin/init.sh:70-76) ----
	wd, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(stderr, "omakase: not inside a git repo")
		return 1
	}
	repo, err := state.Discover(wd)
	if err != nil { // bin/init.sh:71
		fmt.Fprintln(stderr, "omakase: not inside a git repo")
		return 1
	}
	root := repo.Root
	common := repo.CommonDir
	omk := repo.OMK

	// ---- one-time ledger schema upgrade (bin/init.sh:84-87) ----
	// bin/init.sh: `mv -f "$OMK/ledger.tsv" ... && echo "..."` is a single `&&`
	// list inside a bare `if COND; then ...; fi`. mv is not the LAST command of
	// that list, so its failure is exempt from `set -e` (the abort-on-error
	// option only fires on the last command of an && / || list); the failure
	// just short-circuits the list, so the echo never runs — no notice prints —
	// and the script falls through the `fi` and CONTINUES, leaving the pre-v2
	// ledger in place untouched. Match that: on rename failure, print nothing
	// and continue the run (do not exit).
	ledger := filepath.Join(omk, "ledger.tsv")
	if fileRegular(ledger) && ledgerNeedsRotate(ledger) {
		if err := os.Rename(ledger, ledger+".pre-v2.bak"); err == nil {
			fmt.Fprintln(stdout, "omakase: rotated a pre-v2 (6-column) run ledger aside to ledger.tsv.pre-v2.bak (the new store starts clean).")
		}
	}

	// bin/init.sh:91-93 detects shasum/sha256sum here; Go always has
	// crypto/sha256, so that "need shasum" error is unreachable (Global
	// Constraint 9) and omitted.

	// ---- source precedence (bin/init.sh:152-154) ----
	// Payload precedence: --source flag > OMAKASE_PAYLOAD env > remembered
	// source ($OMK/source) > the ../payload default. A remembered source only
	// wins when neither a flag nor OMAKASE_PAYLOAD is set.
	if source == "" && os.Getenv("OMAKASE_PAYLOAD") == "" {
		if first := state.FirstLine(filepath.Join(omk, "source")); first != "" {
			source = first
		}
	}
	// ---- shorthand / ref / local-dir-absolutize (bin/init.sh:160-171) ----
	// Applies to BOTH a freshly given source and a remembered one, so a bare
	// re-run round-trips a pinned ref; skipped when SOURCE is empty or already
	// names an existing local path. The #ref split can leave SOURCE empty (a
	// pathological "#ref"), so the install-arm decision below tests the
	// POST-expansion value, exactly as v1 does.
	sourceRef := ""
	if source != "" {
		source, sourceRef = expandSource(source)
	}

	// ---- payload resolution: --source merge, or the plain default ----
	// A non-empty (post-expansion) source fetches into the disposable cache and
	// merges the base payload UNDER the source delta (bin/init.sh:172-197);
	// otherwise PAYLOAD is OMAKASE_PAYLOAD or the binary-relative default
	// (bin/init.sh:199). sourceLabel (placed.tsv column 3), rememberedSource
	// ($OMK/source) and recommends (the summary) are source-only; a plain install
	// leaves them at their neutral defaults, matching v1's SOURCE_LABEL=payload
	// and empty SOURCE/recommends.
	sourceLabel := "payload" // bin/init.sh:103 (the source arm overrides this)
	rememberedSource := ""
	recommends := ""
	var payload string
	if source != "" {
		res, code := runSource(source, sourceRef, defaultPayload(), stdout, stderr)
		if code != 0 {
			return code // runSource printed the message + cleaned any staging dir
		}
		defer os.RemoveAll(res.merged) // v1's EXIT-trap cleanup (bin/init.sh:63-68)
		payload = res.payload
		sourceLabel = res.label
		rememberedSource = res.remembered
		recommends = res.recommends
	} else {
		// OMAKASE_PAYLOAD overrides; otherwise the binary-relative ../payload
		// default (dist/omakase => repo root => payload/, same as bin/../payload).
		payload = os.Getenv("OMAKASE_PAYLOAD")
		if payload == "" {
			payload = defaultPayload()
		}
	}
	// Strip ONE trailing slash so ${f#"$PAYLOAD"/} derives clean rel values (a
	// pathological OMAKASE_PAYLOAD=/ collapses to "" and is rejected below). A
	// no-op for the merge staging dir / cache (trailing-slash-free by construction).
	payload = strings.TrimSuffix(payload, "/")
	if info, statErr := os.Stat(payload); statErr != nil || !info.IsDir() {
		fmt.Fprintf(stderr, "omakase: payload dir not found at %s\n", payload)
		return 1
	}

	// ---- walk the payload (Global Constraint 6, the one sanctioned divergence)
	// v1 uses `find "$PAYLOAD" \( -type f -o -type l \) -print0` in the cut-over
	// and place loops (fs readdir order); here filepath.WalkDir yields a stable
	// LEXICAL order. This single ordered list feeds the cut-over loop, the place
	// loop, placed.tsv, the snapshot copies, the exclude/wtinc derivation, and
	// the summary — one order, applied consistently everywhere v1 used the find.
	payloadRels, err := walkPayload(payload)
	if err != nil {
		// A walk error here aborts the ENTIRE run, silently, with exit 1 —
		// before the wiring guard below even runs. v1's find instead runs
		// later, inline in the cut-over and place loops, and on an unreadable
		// child prints its own diagnostic to stderr (through the process
		// substitution) while continuing best-effort over whatever files it
		// COULD read. Fault-only divergence — a payload with an unreadable
		// child (permissions, a race) — never reachable through a normal
		// payload/ tree; remove.go documents its twin at the pre-0.10
		// payload-enumeration fallback's walkPayload call (remove.go:172-178).
		return 1
	}

	// ---- fail-closed wiring guard (bin/init.sh:209-226) ----
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

	// ---- lefthook resolution incl. fetch (bin/init.sh:227-238) ----
	lhPrefix, ok := lefthook.ResolveForInit(root, stderr)
	if !ok {
		lefthook.Guidance(stderr)
		return 1
	}

	const begin = "# >>> omakase-harness >>>"
	const end = "# <<< omakase-harness <<<"
	// The exclude file + hooks dir live in the SHARED git dir, so a linked
	// worktree ($ROOT/.git is a FILE there) resolves correctly (bin/init.sh:246-250).
	exclude := filepath.Join(common, "info", "exclude")
	hooksDir := filepath.Join(common, "hooks")

	// ---- incumbent hook-manager guard (bin/init.sh:280-339) ----
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
			// to install while ANY core.hooksPath is set. Flagged here, cleared
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

	// ---- guarded cut-over (bin/init.sh:346-369) ----
	if cutover {
		var cut []string
		for _, rel := range payloadRels { // WALK order (Global Constraint 6)
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
				return exitCode(runErr) // set -e: git rm failure aborts with its code
			}
			fmt.Fprintf(stdout, "omakase: cut-over staged %d deletion(s) — review with 'git status' and commit them yourself.\n", len(cut))
		}
	}

	// ---- upstream-collision guard (bin/init.sh:380-402) ----
	// Prior placed paths from placed.tsv col 1 (fallback placed.list), in FILE
	// ROW ORDER (no walk-order divergence — this iterates an existing state file).
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
			// The last-injected copy would be destroyed by the snapshot rebuild;
			// preserve it under $OMK/clobbered/ (rm-first, cp -P via CopyEntry).
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
	// `omakase status --enable` can restore the CURRENT payload copy later.
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

	// ---- place loop (bin/init.sh:455-497) ----
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
		// Differs and NOT committed: overwrite, preserving the pre-existing copy
		// first (best-effort; a REAL directory dest is skipped and left for
		// place_file to refuse). A backup failure WARNS rather than aborting.
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

	// ---- orphan sweep (bin/init.sh:507-525) ----
	// Prior ledger rows in FILE ORDER; a still-placed path is kept; a tracked or
	// already-gone path is skipped; harness residue that still hashes to what
	// init placed is deleted (+ empty dirs pruned); a local edit is warned + kept.
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

	// ---- exclude block (bin/init.sh:527-564) ----
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
	// bash does this in TWO steps: strip via `awk ... > tmp && mv tmp
	// EXCLUDE` (a fresh 0666&^umask inode), THEN append the block with
	// `>> EXCLUDE` (append mode — writes more bytes to that SAME inode,
	// touching its mode not at all). The net mode after both steps is
	// therefore whatever the strip's tmp+mv established, unaffected by the
	// append. rewriteFile with the fully-combined content (strip + block)
	// reproduces both the final bytes AND that final mode in one call: its
	// own tmp+rename is what sets 0666&^umask, exactly as bash's step 1 did,
	// and — since content alone never affects the mode a creation event
	// picks — writing the already-appended content through the same
	// tmp+rename machinery lands on the identical mode bash's two-step
	// dance does.
	// Exclude entries are root-anchored with a leading "/": a gitignore pattern
	// without one matches at ANY depth, so an unanchored ".omakase/" would also
	// hide a project's own "payload/.omakase" (it did — in this very repo). The
	// anchoring is applied only here, at the exclude write; the shared
	// `prefixes` slice stays unanchored because the .worktreeinclude block
	// below feeds Claude Code's own matcher, whose input we keep unchanged.
	anchored := make([]string, len(prefixes))
	for i, p := range prefixes {
		anchored[i] = "/" + p
	}
	excludeContent, _ := os.ReadFile(exclude)
	excludeOut := textblock.AppendBlock(textblock.Strip(excludeContent, begin, end), begin, anchored, end)
	if err := rewriteFile(exclude, excludeOut); err != nil {
		return 1
	}

	// ---- .worktreeinclude block (bin/init.sh:566-582) ----
	// Only when the repo does not TRACK .worktreeinclude AND something was
	// placed. v1 reuses the SAME prefixes array built for the exclude block
	// and skips any entry equal to ".worktreeinclude" while WRITING the wtinc
	// block (bin/init.sh:577: `[ "$p" = ".worktreeinclude" ] && continue`) —
	// it does not re-derive the list. That comparison runs on the RAW,
	// unsuffixed prefix; the trailing-slash directory suffix is decided after
	// (bin/init.sh:578), by the SAME `[ -d "$ROOT/$p" ]` test already used for
	// the exclude block. Reusing the already-suffixed `prefixes` slice here and
	// trimming a trailing "/" before comparing recovers that raw form exactly,
	// because the only two possible suffixed forms of the raw prefix
	// ".worktreeinclude" are ".worktreeinclude" and ".worktreeinclude/".
	// Filtering `prefixes` this way (instead of re-deriving with
	// wtincTracked=true, the prior approach) also correctly omits a
	// ".worktreeinclude" entry that arose from a PLACED path (a payload
	// shipping a top-level .worktreeinclude) — re-deriving only suppressed the
	// APPENDED wiring entry DerivePrefixes adds when wtincTracked is false, not
	// one contributed by the placed-path loop.
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
		// Same strip-then-append mode reasoning as the exclude block above:
		// rewriteFile with the combined content reproduces bash's
		// tmp+mv-then-append two-step exactly, mode included.
		wtContent, _ := os.ReadFile(wtinc)
		wtOut := textblock.AppendBlock(textblock.Strip(wtContent, begin, end), begin, wtEntries, end)
		if err := rewriteFile(wtinc, wtOut); err != nil {
			return 1
		}
	}

	// ---- snapshot + provenance ledger (bin/init.sh:595-610) ----
	if err := os.RemoveAll(filepath.Join(omk, "payload-snapshot")); err != nil {
		return 1
	}
	if err := os.MkdirAll(filepath.Join(omk, "payload-snapshot"), 0o755); err != nil {
		return 1
	}
	// Remember a source install so a bare re-run refreshes the same source
	// (bin/init.sh:600). A plain install (rememberedSource == "") leaves any
	// remembered source in place — the precedence above (flag > env > remembered)
	// already decides who wins. Positioned exactly as v1: after the snapshot dir
	// is (re)made, before placed.tsv is rewritten.
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
		// safeMkdirAll (not os.MkdirAll): refuse a symlink anywhere in the snapshot
		// parent chain — no snapshot copy is ever written THROUGH a directory-symlink
		// out of the snapshot root. Surfaced fail-closed, never swallowed.
		snapRoot := filepath.Join(omk, "payload-snapshot")
		if err := safeMkdirAll(snapRoot, filepath.Join(snapRoot, filepath.Dir(rel))); err != nil {
			fmt.Fprintf(stderr, "omakase: %v\n", err)
			return 1
		}
		if err := CopyEntry(filepath.Join(root, rel), filepath.Join(omk, "payload-snapshot", rel)); err != nil {
			// A mid-loop failure here exits 1 with the PRIOR placed.tsv fully
			// intact — WritePlaced below runs only after this loop finishes, so
			// nothing has touched the ledger yet. bash truncates the ledger
			// FIRST (`: > placed.tsv`, bin/init.sh:603) and rebuilds it row by
			// row as the loop runs, so the same mid-loop failure there can
			// leave a PARTIAL ledger on disk. Go is deliberately safer here;
			// do not "fix" this toward bash's behavior.
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
			Hash:    state.HashOf(src), // hash of what WOULD be placed (payload copy)
			Enabled: "0",
		})
	}
	if err := state.WritePlaced(filepath.Join(omk, "placed.tsv"), rows); err != nil {
		return 1
	}

	// Heal a placed gate script that a stale (pre-2b) payload just (re)placed —
	// the documented distribution-chain state after a base-binary upgrade whose
	// harness payload has not been rebuilt. Without this, a bare re-init would
	// revert an already-healed script to the pre-2b copy, silently re-arming a
	// gate the human disabled while every consent surface still shows it off.
	// healGateScript no-ops when the script is absent, already 2b-capable, or
	// git-tracked (warning), and otherwise rewrites it + refreshes snapshot and
	// ledger hash so drift detection stays quiet.
	if err := healGateScript(repo, stderr, false); err != nil {
		fmt.Fprintf(stderr, "omakase: %v\n", err)
		return 1
	}
	removeF(filepath.Join(omk, "placed.list")) // pre-0.10 record — superseded

	// ---- install the three hook-time templates (bin/init.sh:612-643) ----
	for _, name := range []string{"ensure-present.sh", "verify-overlay.sh", "install-guards.sh"} {
		if instErr := templates.Install(name, filepath.Join(omk, name)); instErr != nil {
			fmt.Fprintln(stderr, instErr.Error())
			return 1
		}
	}

	// ---- redundant hooksPath reset (bin/init.sh:645-648) ----
	if resetHooksPath {
		exec.Command("git", "-C", root, "config", "--unset", "core.hooksPath").Run() // 2>/dev/null || true
		fmt.Fprintln(stdout, "omakase: cleared redundant core.hooksPath (it named the repo's own hooks dir; lefthook refuses to install while it is set — the effective hooks dir is unchanged).")
	}

	// ---- lefthook install, from root, streams inherited (bin/init.sh:649, GC8) ----
	lhArgs := append(append([]string{}, lhPrefix...), "install")
	lhCmd := exec.Command(lhArgs[0], lhArgs[1:]...)
	lhCmd.Dir = root
	lhCmd.Stdout = stdout
	lhCmd.Stderr = stderr
	if runErr := lhCmd.Run(); runErr != nil {
		return exitCode(runErr) // set -e: `lefthook install` failure aborts with its code
	}

	// ---- install the hook-stub guard blocks (bin/init.sh:650) ----
	// `sh "$OMK/install-guards.sh"`, no cd — it resolves the shared git dir from
	// its cwd (this process's cwd, inside the repo), streams inherited.
	igCmd := exec.Command("sh", filepath.Join(omk, "install-guards.sh"))
	igCmd.Stdout = stdout
	igCmd.Stderr = stderr
	if runErr := igCmd.Run(); runErr != nil {
		return exitCode(runErr)
	}

	// ---- summary (bin/init.sh:652-685) ----
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
	// A source's manifest 'recommends:' is surfaced once here (bin/init.sh:662-664);
	// only a source install sets it — empty on a plain payload install.
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
	return 0
}

// placeFile ports place_file (bin/init.sh:423-443): mkdir -p the dest parent;
// refuse a REAL (non-symlink) directory dest with the byte-exact message and
// exit 1 (bash `return 1` under set -e aborts the script; here returning a
// non-zero code aborts RunInit, leaving prior placements in place — identical
// net effect); otherwise CopyEntry (which rm-firsts, matching place_file's own
// unlink) and chmod +x iff the dest is a *.sh regular file (never a symlink).
func placeFile(src, rel, root string, umask os.FileMode, stderr io.Writer) int {
	dest := filepath.Join(root, rel)
	// safeMkdirAll (not os.MkdirAll): refuse a symlink anywhere in dest's parent
	// chain under root — a prior placement may have put a directory-symlink at a
	// parent, and writing the child THROUGH it would land outside the repo.
	// Surfaced fail-closed, never swallowed.
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
			// chmod +x: add execute bits masked by umask (bash symbolic +x uses
			// the file mode creation mask).
			os.Chmod(dest, info.Mode().Perm()|(0o111&^umask))
		}
	}
	return 0
}

// tryClobberBackup mirrors the place-loop's best-effort backup (bin/init.sh:483-485):
// mkdir -p the clobbered parent, rm -f a stale dest, cp -P the LIVE dest into
// $OMK/clobbered/. Returns true iff every step succeeds (bash `if { ... && ... && ...; }`).
func tryClobberBackup(dest, rel, omk string) bool {
	if err := os.MkdirAll(filepath.Join(omk, "clobbered", filepath.Dir(rel)), 0o755); err != nil {
		return false
	}
	if err := removeF(filepath.Join(omk, "clobbered", rel)); err != nil {
		return false
	}
	return CopyEntry(dest, filepath.Join(omk, "clobbered", rel)) == nil
}

// walkPayload lists the payload's regular files and symlinks (find -type f -o
// -type l) as clean payload-relative paths, in filepath.WalkDir's LEXICAL order
// (Global Constraint 6). Directories and other special files are excluded, and
// symlinks are never followed (WalkDir does not descend into them), matching
// find's default.
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

// wiringRefs reproduces the wiring scan (bin/init.sh:218): from lefthook-local.yml,
// keep lines NOT matching `^[[:space:]]*#`, extract every `.omakase/….sh`
// reference from the kept lines, then `sort -u`.
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
	sort.Strings(refs) // sort -u (byte order; refs are ASCII paths)
	return refs
}

// isStockGitLFSHook ports is_stock_git_lfs_hook (bin/init.sh:259-278): true only
// for the pristine stub `git lfs install` writes — the right basename, git-lfs's
// own presence guard, and nothing left once the shebang, comments, blank lines,
// the presence guard, and the single anchored `git lfs <evt>` forward are stripped.
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
	// The forward strip is ANCHORED to the whole line (a line that merely
	// CONTAINS the substring must NOT be stripped).
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

// ledgerNeedsRotate is the awk `-F'\t' NF>=6` test (bin/init.sh:84): true iff
// any row has >= 6 tab-separated fields.
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

// firstFieldsTSV reproduces `cut -f1 FILE` piped through the collision guard's
// line loop (bin/init.sh:381-385): the substring before the first tab of each
// line (the whole line when there is no tab), skipping lines whose first field
// is empty. Also serves the placed.list fallback (whole lines, no tabs).
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
// excluding dot-prefixed names — the Go twin of the bash glob `"$HOOKS_DIR"/*`
// (sorted, no dotfiles). A missing dir yields nothing.
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

// physicalResolve is `cd "$p" 2>/dev/null && pwd -P || echo "$p"` (bin/init.sh:299-300):
// the symlink-resolved absolute path when p is an existing directory, else the
// literal p unchanged (cd would fail on a non-directory or missing path).
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

// --- small predicates / helpers (bash test operators) ---

// gitTracked is `git -C root ls-files --error-unmatch -- rel` exit 0.
func gitTracked(root, rel string) bool {
	return exec.Command("git", "-C", root, "ls-files", "--error-unmatch", "--", rel).Run() == nil
}

// gitStdout returns a git command's stdout (stderr discarded, exit code ignored)
// — matching `$(git ... 2>/dev/null)` before the caller's own trimming.
func gitStdout(root string, args ...string) string {
	out, _ := exec.Command("git", append([]string{"-C", root}, args...)...).Output()
	return string(out)
}

// gitOutTrim is `$(git ... 2>/dev/null || true)`: stdout with trailing newlines
// stripped (command-substitution semantics), "" on any error.
func gitOutTrim(root string, args ...string) string {
	return strings.TrimRight(gitStdout(root, args...), "\n")
}

func isDir(p string) bool {
	info, err := os.Stat(p) // follows symlinks, like `[ -d ]`
	return err == nil && info.IsDir()
}

func isSymlink(p string) bool {
	info, err := os.Lstat(p)
	return err == nil && info.Mode()&os.ModeSymlink != 0
}

// fileRegular is `[ -f p ]`: exists (following symlinks) and is a regular file.
func fileRegular(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.Mode().IsRegular()
}

// lexists is `[ -e p ] || [ -L p ]`: the path is present as any type, including
// a dangling symlink (os.Lstat succeeds for a dangling symlink).
func lexists(p string) bool {
	_, err := os.Lstat(p)
	return err == nil
}

// fileMatchesLine reports whether any line of path matches re — the Go twin of
// `grep -Eq PAT FILE` (line-oriented, so a negated class like `[^"]` cannot span
// lines).
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

// removeF is `rm -f p`: remove p, treating a missing file as success.
func removeF(p string) error {
	if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// touch is `touch p`: create p if missing (no truncation of an existing file).
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

// exitCode extracts a child process's exit code from a *exec.ExitError,
// defaulting to 1 for any other error — matching `set -e` propagating a failed
// external command's status.
func exitCode(err error) int {
	if ee, ok := err.(*exec.ExitError); ok {
		if code := ee.ExitCode(); code > 0 {
			return code
		}
	}
	return 1
}
