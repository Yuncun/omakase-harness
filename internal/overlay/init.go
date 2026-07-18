// This file implements the `omakase init` verb: arg parse, payload
// resolution, the manifest gate-validation and incumbent-hook-manager guards,
// the guarded cut-over, the upstream-collision guard, the place loop, the
// orphan sweep, the exclude and .worktreeinclude marked blocks, the snapshot +
// provenance ledger rebuild, the hook dispatcher writes, and the closing
// summary. Payload files are processed in one lexical walk order;
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
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"

	"github.com/Yuncun/omakase-harness/internal/gate"
	"github.com/Yuncun/omakase-harness/internal/harness"
	"github.com/Yuncun/omakase-harness/internal/hook"
	"github.com/Yuncun/omakase-harness/internal/probe"
	"github.com/Yuncun/omakase-harness/internal/render"
	"github.com/Yuncun/omakase-harness/internal/state"
	"github.com/Yuncun/omakase-harness/internal/textblock"
)

// usageText is the `omakase init` usage text; tests pin the exact bytes.
const usageText = "usage: init.sh [<owner/repo[/subpath][#ref]> | --source <git-url|path>] [--cut-over] [--help]\n" +
	"\n" +
	"Overlay payload/ into the current repo additively (zero committed footprint) and\n" +
	"install its git hooks. A payload path the repo already COMMITS is never touched:\n" +
	"it is skipped and reported.\n" +
	"\n" +
	"  <owner/repo[/subpath][#ref]>\n" +
	"               shorthand for --source https://github.com/owner/repo (optionally pinned to a\n" +
	"               branch or tag with #ref). This is the shareable install line: a harness\n" +
	"               published at github.com/you/harness installs with `init you/harness`.\n" +
	"               Extra segments name a harness directory INSIDE the repo — `init you/hub/tools`\n" +
	"               adopts the harness at hub's tools/ — so one hub repo can publish many harnesses.\n" +
	"  --source <git-url|path>\n" +
	"               pull a harness SOURCE — a git repo carrying a payload/ tree whose\n" +
	"               payload/omakase.manifest (flat key: value; name required, version + recommends\n" +
	"               optional, plus any gate: blocks) is the harness's one manifest —\n" +
	"               into a local cache (${XDG_CACHE_HOME:-~/.cache}/omakase/sources) and inject\n" +
	"               the base harness's payload with the source's payload layered ON TOP (base\n" +
	"               machinery underneath, source wins on overlap), so a source ships only its\n" +
	"               delta and relies on base machinery without keeping its own copy. The source is\n" +
	"               remembered; a later bare init.sh refreshes and re-injects the same source.\n" +
	"               A `//subpath` suffix on the url or path adopts a harness directory inside\n" +
	"               the repo: --source https://host/x/hub//tools, --source /clones/hub//tools.\n" +
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
	// source ($OMK/source); with all three absent, init places nothing (the
	// early return below). Suppression of a remembered source keys on
	// OMAKASE_PAYLOAD only; OMAKASE_BASE_PAYLOAD is the merge base the shims
	// hand over, not a suppression key, so a bare re-run never silently
	// downgrades a remembered source to a plain install.
	if source == "" && os.Getenv("OMAKASE_PAYLOAD") == "" {
		if first := state.FirstLine(filepath.Join(omk, "source")); first != "" {
			source = first
		}
	}
	// ---- shorthand / ref / subpath / local-dir absolutize ----
	// Applies to both a freshly given source and a remembered one, so a bare
	// re-run round-trips a pinned ref and a subpath; skipped when source is
	// empty or already names an existing local path. The #ref split can
	// leave source empty (a pathological "#ref"), so the install-arm
	// decision below tests the post-expansion value.
	sourceRef, sourceSub := "", ""
	if source != "" {
		source, sourceRef, sourceSub = expandSource(source)
	}
	// A subpath can never point outside the clone: fail closed on any form
	// that escapes or degenerates ("..", absolute) before the fetch runs.
	// path.Clean normalizes the benign forms ("sub/", "a/./b") so the
	// canonical remembered string stays stable. A subpath with no repo in
	// front of the marker ("--source //sub") refuses too — the pathological
	// bare "#ref" empties the source and falls into the nothing-to-refresh
	// return below (or a plain install when OMAKASE_PAYLOAD is set), but a
	// parsed subpath is explicit intent and must never be dropped silently.
	if sourceSub != "" {
		if source == "" {
			fmt.Fprintf(stderr, "omakase: source '//%s' is missing the repo part before the '//' subpath marker\n", sourceSub)
			return 2
		}
		clean := path.Clean(sourceSub)
		if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") || strings.HasPrefix(clean, "/") {
			fmt.Fprintf(stderr, "omakase: source subpath '%s' must stay inside the source repo (relative, no '..')\n", sourceSub)
			return 2
		}
		sourceSub = clean
	}

	// ---- nothing to refresh (the newcomer first-run) ----
	// No source (given or remembered) and no OMAKASE_PAYLOAD override means
	// there is nothing to install from: place nothing and point at status.
	// Silently installing the base machinery here — or erroring with the
	// binary-relative cache path — was wrong first-run behavior (#123).
	// OMAKASE_BASE_PAYLOAD does not count as intent: it is the merge base
	// the shims always export, never a request to install. The wording keys
	// on placed.tsv, the same signal status routes on: an OMAKASE_PAYLOAD
	// install writes placed.tsv but remembers no source, and that repo must
	// never be told "no harness is installed" while its gates are live.
	if source == "" && os.Getenv("OMAKASE_PAYLOAD") == "" {
		if fileRegular(filepath.Join(omk, "placed.tsv")) {
			fmt.Fprintln(stdout, "omakase: nothing to refresh — a harness is installed here, but no source is remembered to refresh it from. See what's installed:  omakase status")
		} else {
			fmt.Fprintln(stdout, "omakase: nothing to refresh — no harness is installed in this repo. See the agent config present here:  omakase status")
		}
		return 0
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
		res, code := runSource(source, sourceRef, sourceSub, defaultPayload(), stdout, stderr)
		if code != 0 {
			return code // runSource printed the message + cleaned any staging dir
		}
		defer os.RemoveAll(res.merged) // clean up the merge staging dir
		payload = res.payload
		sourceLabel = res.label
		rememberedSource = res.remembered
		recommends = res.recommends
	} else {
		// A plain install always has OMAKASE_PAYLOAD set: the empty-source,
		// empty-env case took the nothing-to-refresh return above.
		payload = os.Getenv("OMAKASE_PAYLOAD")
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

	// ---- manifest gate guard (nothing runs undeclared) ----
	// A harness that still ships lefthook-local.yml is from before the gate
	// module; omakase no longer reads it. Refuse with migration instructions,
	// place nothing (the unchanged refuse-invariant).
	if fileRegular(filepath.Join(payload, "lefthook-local.yml")) {
		fmt.Fprintln(stderr, "omakase: this harness declares gates in lefthook-local.yml, which omakase no longer reads. Declare them as gate: blocks in omakase.manifest (see the README) and delete the yml. Nothing was changed.")
		return 1
	}
	// Validate the manifest's gate blocks before placing anything: an unknown
	// key, a missing required key, a duplicate name, or a bad hook stage
	// refuses the whole harness; a gate whose run: names a payload script the
	// harness does not ship (or that is not executable) refuses too — the
	// "nothing runs undeclared" check, moved here from the old yml scan.
	if manifest := filepath.Join(payload, "omakase.manifest"); fileRegular(manifest) {
		content, rerr := os.ReadFile(manifest)
		if rerr != nil {
			fmt.Fprintf(stderr, "omakase: could not read %s: %v. Nothing was changed.\n", manifest, rerr)
			return 1
		}
		gates, perr := gate.Parse(content)
		if perr != nil {
			fmt.Fprintf(stderr, "omakase: invalid gate declaration in omakase.manifest: %v. Nothing was changed.\n", perr)
			return 1
		}
		if verr := gate.ValidateRunnable(gates, payload); verr != nil {
			fmt.Fprintf(stderr, "omakase: %v. It would fail at commit time (exit 127). Nothing was changed.\n", verr)
			return 1
		}
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
			// Redundant config: names the default location. Cleared just before
			// the dispatcher writes so git uses the default hooks dir, and so a
			// refusal below mutates nothing.
			resetHooksPath = true
		}
	}
	if strings.TrimRight(gitStdout(root, "ls-files", "--", ".husky"), "\n") != "" {
		incumbent = append(incumbent, ".husky/ content is git-tracked (the project's own husky setup)")
	} else if isDir(filepath.Join(root, ".husky")) && !isDir(filepath.Join(payload, ".husky")) {
		incumbent = append(incumbent, ".husky/ directory (husky)")
	}
	if fileRegular(filepath.Join(root, "package.json")) && fileMatchesLine(filepath.Join(root, "package.json"), rePrepare) {
		incumbent = append(incumbent, "package.json \"prepare\" script wires a hook manager (husky / simple-git-hooks) — npm install would overwrite omakase's hooks")
	}
	// A project's own committed lefthook config: omakase no longer runs
	// lefthook, so installing its dispatchers would displace lefthook's hooks
	// and silently disable the project's gates. A placed (gitignored)
	// lefthook-local.yml from a harness is caught earlier by the manifest
	// guard, not here — this looks only at tracked files. lefthook loads config
	// under several root names — lefthook.{yml,yaml,toml,json}, the dotted
	// .lefthook.*, and the -local overlay variants — so the scan covers the
	// whole set, not just the two .yml names (a repo committing lefthook.yaml
	// but not yet lefthook-installed would otherwise slip past). The `:(glob)`
	// pathspec keeps `*` from crossing '/', so it matches only root-level config.
	cfgOut := gitStdout(root, "ls-files", "--",
		":(glob)lefthook.*", ":(glob)lefthook-local.*",
		":(glob).lefthook.*", ":(glob).lefthook-local.*")
	for _, cfg := range strings.Split(strings.TrimRight(cfgOut, "\n"), "\n") {
		if cfg == "" {
			continue
		}
		incumbent = append(incumbent, cfg+" is git-tracked (the project's own lefthook config)")
	}
	if strings.TrimRight(gitStdout(root, "ls-files", "--", ".lefthook"), "\n") != "" {
		incumbent = append(incumbent, ".lefthook/ content is git-tracked (the project's own lefthook config)")
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
		if bytes.Contains(content, []byte("# omakase dispatcher")) {
			continue // omakase's own dispatcher (a re-init), any version's text
		}
		if bytes.Contains(content, []byte("omakase-harness")) {
			continue // a pre-gate-module omakase stub (guard-block markers) — a bare re-init migrates it
		}
		if isStockGitLFSHook(hf, content) {
			continue // `omakase hook` forwards git-lfs — not a rival manager
		}
		base := filepath.Base(hf)
		switch {
		case bytes.Contains(bytes.ToLower(content), []byte("lefthook")):
			incumbent = append(incumbent, base+": lefthook-installed hook in "+hooksDir+" (the project uses lefthook natively)")
		case preCommitConfig && (bytes.Contains(content, []byte("pre-commit.com")) || bytes.Contains(content, []byte("generated by pre-commit"))):
			incumbent = append(incumbent, base+": installed pre-commit-framework stub (plus .pre-commit-config.yaml)")
		default:
			incumbent = append(incumbent, base+": existing hook in "+hooksDir)
		}
	}
	if len(incumbent) > 0 {
		fmt.Fprintln(stderr, "omakase: REFUSING to install — an incumbent hook manager is present:")
		for _, i := range incumbent {
			fmt.Fprintf(stderr, "  - %s\n", i)
		}
		fmt.Fprintln(stderr, "  Installing omakase's hooks would displace the project's own, silently disabling")
		fmt.Fprintln(stderr, "  its gates — and a husky prepare script would overwrite them back on the next")
		fmt.Fprintln(stderr, "  npm install. omakase does not chain hook managers (v1).")
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
	// A row the user kept (accepted their own edit; the $OMK/kept copy is the
	// mark) is skipped by the place loop and its ledger row carried verbatim:
	// "make this repo match the harness" extends to "match what you've
	// consented to", exactly like disabled rows (issue #98 Part 2). A kept
	// path that is now git-tracked lost to the upstream commit (the collision
	// guard above warned) and drops out like any other tracked row.
	keptPrior := map[string]state.PlacedRow{}
	var keptOrder []string
	for _, row := range state.ReadPlaced(filepath.Join(omk, "placed.tsv")) {
		// Machinery is never a consent item (the toggles refuse it), so an
		// enabled=0 machinery row can only be a pre-guard binary's leftover —
		// honoring it would keep the gate primitive missing on every re-init.
		// Ignore it: init re-places the file and the row returns to enabled=1.
		if row.Enabled == "0" && !harness.IsMachinery(row.Rel) {
			declined[row.Rel] = true
		}
		if row.Enabled == "1" && lexists(keptEntry(omk, row.Rel)) && !gitTracked(root, row.Rel) {
			keptPrior[row.Rel] = row
			keptOrder = append(keptOrder, row.Rel)
		}
	}
	var declinedKept []string

	umask := currentUmask()

	// ---- place loop ----
	var placed, skipped, overwrote []string
	keptRefilled := map[string]bool{}
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
		if _, ok := keptPrior[rel]; ok {
			if !lexists(dest) {
				// Repair refills a missing kept file with the ACCEPTED copy —
				// "match what you've consented to", same as the checkout heal.
				if code := placeFile(keptEntry(omk, rel), rel, root, umask, stderr); code != 0 {
					return code
				}
				keptRefilled[rel] = true
				fmt.Fprintf(stderr, "omakase: restored your kept version of %s (it was missing)\n", rel)
			} else {
				fmt.Fprintf(stderr, "omakase: SKIP (kept — yours) %s — see the difference: omakase diff %s; harness version back: omakase status --restore %s\n", rel, rel, rel)
			}
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

	// A kept path the payload no longer ships never enters the place loop;
	// repair its missing-file case here the same way (accepted copy back).
	for _, rel := range keptOrder {
		if contains(payloadRels, rel) || lexists(filepath.Join(root, rel)) {
			continue
		}
		if code := placeFile(keptEntry(omk, rel), rel, root, umask, stderr); code != 0 {
			return code
		}
		keptRefilled[rel] = true
		fmt.Fprintf(stderr, "omakase: restored your kept version of %s (it was missing)\n", rel)
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
			if _, ok := keptPrior[rel]; ok {
				continue // kept: the user's accepted file, never harness residue
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
	wtincTracked := gitTracked(root, ".worktreeinclude")
	if wtincTracked {
		fmt.Fprintln(stderr, "omakase: .worktreeinclude is tracked — leaving it untouched (re-run omakase init inside a new manual worktree to install it there).")
	}
	isDirRoot := func(p string) bool { return isDir(filepath.Join(root, p)) }
	consented := append(append(append([]string{}, placed...), declinedKept...), keptOrder...)
	prefixes := DerivePrefixes(consented, harness.SharedTopdirs, isDirRoot, wtincTracked)

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
	if !wtincTracked && len(placed)+len(declinedKept)+len(keptOrder) > 0 {
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
	// A kept path the new payload no longer ships still needs its harness
	// version in the snapshot — that copy is what makes --restore always
	// possible offline — so it is carried across the wholesale rebuild.
	carry := filepath.Join(omk, "snapshot-carry")
	if err := os.RemoveAll(carry); err != nil {
		return 1
	}
	for _, rel := range keptOrder {
		if contains(payloadRels, rel) {
			continue // the new payload provides the harness version below
		}
		old := filepath.Join(omk, "payload-snapshot", rel)
		if !lexists(old) {
			continue
		}
		if err := safeMkdirAll(carry, filepath.Join(carry, filepath.Dir(rel))); err != nil {
			fmt.Fprintf(stderr, "omakase: %v\n", err)
			return 1
		}
		if err := CopyEntry(old, filepath.Join(carry, rel)); err != nil {
			return 1
		}
	}
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
	// Kept rows: file untouched (skipped above), ledger row carried verbatim
	// — the hash IS the accepted hash, so the kept file keeps reading green.
	// The snapshot gets the new payload's harness version when it ships one
	// (adopting it is --restore's job), else the carried-over prior version.
	for _, rel := range keptOrder {
		src := filepath.Join(payload, rel)
		if !lexists(src) {
			src = filepath.Join(carry, rel)
		}
		if lexists(src) {
			snapRoot := filepath.Join(omk, "payload-snapshot")
			if err := safeMkdirAll(snapRoot, filepath.Join(snapRoot, filepath.Dir(rel))); err != nil {
				fmt.Fprintf(stderr, "omakase: %v\n", err)
				return 1
			}
			if err := CopyEntry(src, filepath.Join(snapRoot, rel)); err != nil {
				return 1
			}
		}
		rows = append(rows, keptPrior[rel])
	}
	if err := os.RemoveAll(carry); err != nil {
		return 1
	}
	if err := state.WritePlaced(filepath.Join(omk, "placed.tsv"), rows); err != nil {
		return 1
	}

	removeF(filepath.Join(omk, "placed.list")) // pre-0.10 record — superseded

	// ---- redundant hooksPath reset ----
	if resetHooksPath {
		exec.Command("git", "-C", root, "config", "--unset", "core.hooksPath").Run() // 2>/dev/null || true
		fmt.Fprintln(stdout, "omakase: cleared redundant core.hooksPath (it named the repo's own hooks dir; the effective hooks dir is unchanged).")
	}

	// ---- hook dispatchers ----
	// The permanent dispatchers (issue #98): written only here — and deleted
	// only by remove — atomically, one per hook omakase dispatches. Their
	// content never varies by repo, branch, or version, so a re-init rewrites
	// identical bytes and an upgrade refreshes the binary copy they exec, not
	// the hook files. lefthook stops owning .git/hooks entirely: no
	// `lefthook install`, no run-time stub sync, no skeleton lefthook.yml.
	for _, name := range hook.Names() {
		if err := hook.Write(hooksDir, name); err != nil {
			fmt.Fprintf(stderr, "omakase: could not write the %s hook: %v\n", name, err)
			return 1
		}
	}
	// The dispatchers exec the machine-wide copy at StableBinPath, which is
	// now load-bearing: a gate hook fails closed without it. main()
	// self-installs it before RunInit; verify it actually landed — never
	// leave fail-closed hooks silently pointing at nothing. (The probe's
	// hook proof checks the same fact, so the verdict below and later
	// status runs agree with what happens at commit time.)
	if stable := hook.StableBinPath(); stable == "" || !fileExecutable(stable) {
		fmt.Fprintf(stderr, "omakase: WARNING — the hooks run %s, which is missing or not executable; commits will be blocked until it exists. Re-run 'omakase init' with any installed omakase binary to restore it.\n", stable)
	}

	// ---- migration: retire the pre-#98 hook-time machinery ----
	// A repo initialized under the old scheme carries per-repo copies of the
	// hook-time scripts, the lefthook.yml heal snapshot, lefthook's stub-sync
	// checksum, and (per worktree) the skeleton lefthook.yml that `lefthook
	// install` wrote. Those jobs now live in the binary (`omakase hook`), and
	// the dispatcher writes above replaced the lefthook stubs; delete the
	// leftovers. Hooks live once in the shared git dir, so this one init
	// converts every worktree.
	for _, name := range []string{"ensure-present.sh", "install-guards.sh", "verify-overlay.sh", "lefthook.yml"} {
		if err := removeF(filepath.Join(omk, name)); err != nil {
			return 1
		}
	}
	if err := removeF(filepath.Join(common, "info", "lefthook.checksum")); err != nil {
		return 1
	}
	for _, wtRoot := range state.WorktreeRoots(root) {
		skel := filepath.Join(wtRoot, "lefthook.yml")
		if fileRegular(skel) && !gitTracked(wtRoot, "lefthook.yml") && fileContains(skel, "EXAMPLE USAGE") {
			if err := removeF(skel); err != nil {
				return 1
			}
		}
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
	for _, k := range keptOrder {
		if keptRefilled[k] {
			fmt.Fprintf(stdout, "  = kept (yours — was missing, your accepted version restored): %s\n", k)
		} else {
			fmt.Fprintf(stdout, "  = kept (yours — left untouched): %s\n", k)
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
	fmt.Fprintln(stdout, "omakase: ignores -> .git/info/exclude; new worktrees auto-install the harness. Nothing to commit.")
	fmt.Fprintln(stdout, "omakase: see the whole harness any time with  omakase status")
	// A source's manifest recommends: line; only a source install sets it.
	if recommends != "" {
		fmt.Fprintf(stdout, "omakase: this harness recommends — %s\n", recommends)
	}
	fmt.Fprintln(stdout, "omakase: to customize, fork the harness source (clone -> edit -> publish) and")
	fmt.Fprintln(stdout, "         init from your copy; do not edit injected files in place (overwritten on re-init).")
	// The status-bar / stop-notice wiring runs the machine-wide binary copy
	// (main() refreshes it on every real init), so these stanzas print
	// unconditionally — the feature ships in the binary, not the payload.
	stable := hook.StableBinPath()
	if stable == "" {
		stable = "omakase" // no resolvable home: fall back to PATH wiring
	}
	fmt.Fprintln(stdout, "omakase: status bar (optional) — one machine-wide segment for every omakase repo; it")
	fmt.Fprintln(stdout, "         shows this harness's verified state and goes dark elsewhere. Wire your status")
	fmt.Fprintln(stdout, "         line to run:")
	fmt.Fprintf(stdout, "           %s statusline\n", stable)
	fmt.Fprintln(stdout, "         Claude Code: statusLine.command in ~/.claude/settings.json. ccstatusline: a")
	fmt.Fprintln(stdout, "         custom-command widget. Copilot CLI: statusLine in ~/.copilot/settings.json.")
	fmt.Fprintln(stdout, "omakase: end-of-turn notice (Claude Code only, opt-in) — a one-line harness status when")
	fmt.Fprintln(stdout, "         a turn ends. Enable by adding a Stop hook to .claude/settings.json:")
	fmt.Fprintf(stdout, "           %s stop-notice\n", stable)
	if fileRegular(filepath.Join(root, ".omakase", "bin", "omakase-worktree-guard.sh")) {
		fmt.Fprintln(stdout, "omakase: worktree guard (Claude Code only, opt-in) — while other worktrees are active,")
		fmt.Fprintln(stdout, "         denies edits to product files in the MAIN checkout before they happen. Enable by")
		fmt.Fprintln(stdout, "         adding a PreToolUse hook (matcher \"Edit|Write\") to .claude/settings.json:")
		fmt.Fprintln(stdout, "           bash $CLAUDE_PROJECT_DIR/.omakase/bin/omakase-worktree-guard.sh")
	}

	// ---- prove, don't assert ----
	// The closing line is the three status-bar proofs run fresh against what
	// this init just wrote — never an unconditional claim (a "hooks installed"
	// assertion once shipped green-while-broken, #72/#85).
	verdict, err := probe.Collect(root)
	if err != nil {
		verdict = nil
	}
	fmt.Fprintln(stdout, render.InitVerdict(verdict))
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

// fileExecutable reports whether p is a regular file with at least one
// execute bit set.
func fileExecutable(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.Mode().IsRegular() && info.Mode()&0o111 != 0
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
