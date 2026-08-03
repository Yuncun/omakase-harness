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
	"strconv"
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
				fmt.Fprintln(stderr, msgSourceNeedsValue)
				return 2
			}
			source = argv[i]
		case a == "-h" || a == "--help":
			fmt.Fprint(stdout, usageText)
			return 0
		case strings.HasPrefix(a, "-"):
			fmt.Fprintf(stderr, msgUnknownOption, a)
			fmt.Fprint(stderr, usageText)
			return 2
		default: // positional: a harness source
			if source != "" {
				fmt.Fprintf(stderr, msgExtraArgument, a)
				fmt.Fprint(stderr, usageText)
				return 2
			}
			source = a
		}
	}
	// The source string is recorded verbatim in the tab-separated ledger.
	if strings.ContainsAny(source, "\t\n") {
		fmt.Fprintln(stderr, msgSourceBadChars)
		return 2
	}

	// ---- repo discovery ----
	wd, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(stderr, msgNotAGitRepo)
		return 1
	}
	repo, err := state.Discover(wd)
	if err != nil {
		fmt.Fprintln(stderr, msgNotAGitRepo)
		return 1
	}
	root := repo.Root
	common := repo.CommonDir
	omk := repo.OMK

	// ---- source precedence ----
	// Payload precedence: --source flag > OMAKASE_PAYLOAD env > remembered
	// source ($OMK/source); with all three absent, init places nothing (the
	// early return below). Suppression of a remembered source keys on
	// OMAKASE_PAYLOAD only; OMAKASE_BASE_PAYLOAD is the merge base the shims
	// hand over, not a suppression key, so a bare re-run never silently
	// downgrades a remembered source to a plain install.
	explicitSource := source != "" // given this run, not remembered — the re-point guard keys on it
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
			fmt.Fprintf(stderr, msgSourceMissingRepoPart, sourceSub)
			return 2
		}
		clean := path.Clean(sourceSub)
		if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") || strings.HasPrefix(clean, "/") {
			fmt.Fprintf(stderr, msgSourceSubpathEscapes, sourceSub)
			return 2
		}
		sourceSub = clean
	}

	// ---- linked-worktree re-point guard (#184) ----
	// Hooks, the exclude block, and the remembered source live in the git
	// common dir — every checkout of the repository shares them. Switching
	// the repo's harness is therefore a repo-wide act: an explicitly given
	// source (or an OMAKASE_PAYLOAD override, the other door to the same
	// re-point) that differs from what the repo already runs refuses from a
	// linked worktree and names the main checkout. The bare refresh and the
	// same-source re-run stay allowed (the per-worktree heal flow), a first
	// install from a worktree has nothing to clobber, and in a bare-repo +
	// worktrees layout (no main checkout at all) the FIRST listed worktree
	// counts as main — otherwise every checkout refuses and the repo's
	// harness can never be changed.
	payloadOverride := os.Getenv("OMAKASE_PAYLOAD") != ""
	remembered := state.FirstLine(filepath.Join(omk, "source"))
	if (explicitSource || (payloadOverride && remembered != "")) && fileRegular(filepath.Join(omk, "placed.tsv")) {
		gd := gitOutTrim(root, "rev-parse", "--absolute-git-dir")
		mainRoot := state.WorktreeRoots(root)[0]
		if gd != "" && filepath.Clean(gd) != common && filepath.Clean(root) != filepath.Clean(mainRoot) {
			// The canonical label, exactly as runSource records it — a
			// same-source re-run round-trips to the remembered string.
			label := source
			if sourceSub != "" {
				label += "//" + sourceSub
			}
			if sourceRef != "" {
				label += "#" + sourceRef
			}
			if !payloadOverride && sameSourceLabel(label, remembered) {
				// spelled differently, same harness: allow
			} else if payloadOverride || label != remembered {
				given := label
				if payloadOverride && label == "" {
					given = "(a local payload via OMAKASE_PAYLOAD)"
				}
				fmt.Fprintf(stderr, msgLinkedWorktreeRefusal, mainRoot, mainRoot, given)
				return 1
			}
		}
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
			fmt.Fprintln(stdout, msgRefreshNoRememberedSource)
		} else {
			fmt.Fprintln(stdout, msgRefreshNothingInstalled)
		}
		return 0
	}

	// ---- payload resolution: --source merge, or the plain default ----
	// A non-empty (post-expansion) source fetches into the disposable cache
	// and merges the base payload under the source delta; otherwise the
	// payload is OMAKASE_PAYLOAD or the binary-relative default.
	// rememberedSource ($OMK/source — also status's "from" label) and
	// recommends (the summary) are source-only.
	rememberedSource := ""
	recommends := ""
	var payload string
	if source != "" {
		base, baseErr := ensureBasePayload()
		if baseErr != nil {
			// Unreachable with a healthy binary: an on-disk base was absent
			// AND the embedded copy could not be extracted to the cache.
			fmt.Fprintf(stderr, msgBaseMaterializeFailed, baseErr)
			return 1
		}
		res, code := runSource(source, sourceRef, sourceSub, base, stdout, stderr)
		if code != 0 {
			return code // runSource printed the message + cleaned any staging dir
		}
		defer os.RemoveAll(res.merged) // clean up the merge staging dir
		payload = res.payload
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
		fmt.Fprintf(stderr, msgPayloadDirMissing, payload)
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

	// ---- symlink escape guard (issue #30) ----
	// A payload symlink is placed verbatim, hidden from git status, and
	// re-materialized into every worktree by the heal — so a target outside
	// the repo is a persistent read/write hole. Refuse the whole payload
	// before anything is placed; in-tree relative links stay allowed.
	if code := checkPayloadSymlinks(payload, payloadRels, stderr); code != 0 {
		return code
	}

	// ---- base-machinery downgrade guard (#189) ----
	// The entry points update independently (brew binary, Claude plugin,
	// Copilot plugin, dev build), so a stale one can run a bare init against
	// a repo a newer omakase set up and silently roll .omakase/ backwards —
	// the only symptom was status rendering differently. Refuse before
	// anything is placed; a deliberate downgrade is remove-then-init.
	if code := checkBaseDowngrade(root, omk, payload, stderr); code != 0 {
		return code
	}

	// ---- manifest gate guard (nothing runs undeclared) ----
	// A harness that still ships lefthook-local.yml is from before the gate
	// module; omakase no longer reads it. Refuse with migration instructions,
	// place nothing (the unchanged refuse-invariant).
	if fileRegular(filepath.Join(payload, "lefthook-local.yml")) {
		fmt.Fprintln(stderr, msgLefthookYmlRefused)
		return 1
	}
	// Validate the manifest's gate blocks before placing anything: an unknown
	// key, a missing required key, a duplicate name, or a bad hook stage
	// refuses the whole harness; a gate whose run: names a payload script the
	// harness does not ship (or that is not executable) refuses too — the
	// "nothing runs undeclared" check, moved here from the old yml scan.
	// gateCount decides the wiring below (#149): 0 means a steering-only
	// harness — no enforcement hooks, no incumbent refusal. -1 means no
	// manifest at all (a legacy/dev payload shape; every published harness
	// ships one) and keeps the full wiring.
	gateCount := -1
	if manifest := filepath.Join(payload, "omakase.manifest"); fileRegular(manifest) {
		content, rerr := os.ReadFile(manifest)
		if rerr != nil {
			fmt.Fprintf(stderr, msgManifestReadFailed, manifest, rerr)
			return 1
		}
		gates, perr := gate.Parse(content)
		if perr != nil {
			fmt.Fprintf(stderr, msgGateDeclInvalid, perr)
			return 1
		}
		if verr := gate.ValidateRunnable(gates, payload); verr != nil {
			fmt.Fprintf(stderr, msgGateWiringInvalid, verr)
			return 1
		}
		gateCount = len(gates)
	}

	const begin = "# >>> omakase-harness >>>"
	const end = "# <<< omakase-harness <<<"
	// The exclude file and hooks dir live in the shared git dir, so a linked
	// worktree (where $ROOT/.git is a file) resolves correctly.
	exclude := filepath.Join(common, "info", "exclude")
	hooksDir := filepath.Join(common, "hooks")

	// ---- incumbent hook-manager guard ----
	var incumbent []string
	var lfsDisplaced []string
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
			incumbent = append(incumbent, fmt.Sprintf(incHooksPath, hookspath))
		} else {
			// Redundant config: names the default location. Cleared just before
			// the dispatcher writes so git uses the default hooks dir, and so a
			// refusal below mutates nothing.
			resetHooksPath = true
		}
	}
	if strings.TrimRight(gitStdout(root, "ls-files", "--", ".husky"), "\n") != "" {
		incumbent = append(incumbent, incHuskyTracked)
	} else if isDir(filepath.Join(root, ".husky")) && !isDir(filepath.Join(payload, ".husky")) {
		incumbent = append(incumbent, incHuskyDir)
	}
	if fileRegular(filepath.Join(root, "package.json")) && fileMatchesLine(filepath.Join(root, "package.json"), rePrepare) {
		incumbent = append(incumbent, incPrepareScript)
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
		incumbent = append(incumbent, fmt.Sprintf(incLefthookCfg, cfg))
	}
	if strings.TrimRight(gitStdout(root, "ls-files", "--", ".lefthook"), "\n") != "" {
		incumbent = append(incumbent, incLefthookDir)
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
			// `omakase hook` forwards git-lfs — not a rival manager. Remember
			// it so the summary can say the stub was displaced, not disabled
			// (#190): silence here read as "LFS hook clobbered".
			lfsDisplaced = append(lfsDisplaced, filepath.Base(hf))
			continue
		}
		base := filepath.Base(hf)
		switch {
		case bytes.Contains(bytes.ToLower(content), []byte("lefthook")):
			incumbent = append(incumbent, fmt.Sprintf(incLefthookHook, base, hooksDir))
		case preCommitConfig && (bytes.Contains(content, []byte("pre-commit.com")) || bytes.Contains(content, []byte("generated by pre-commit"))):
			incumbent = append(incumbent, fmt.Sprintf(incPreCommitStub, base))
		default:
			incumbent = append(incumbent, fmt.Sprintf(incExistingHook, base, hooksDir))
		}
	}
	// A steering-only harness (zero declared gates) wants nothing in
	// .git/hooks, so there is nothing to conflict over: the refusal is
	// skipped and the incumbent list only decides whether the heal hook can
	// be written (#149).
	if len(incumbent) > 0 && gateCount != 0 {
		fmt.Fprintln(stderr, msgIncumbentRefusalHeader)
		for _, i := range incumbent {
			fmt.Fprintf(stderr, "  - %s\n", i)
		}
		fmt.Fprint(stderr, msgIncumbentRefusalBody)
		return 1
	}

	// ---- one-time ledger schema upgrade ----
	// A pre-v2 (6-column) run ledger is rotated aside, silently: an internal
	// store-format migration is nothing the user can act on, and the notice
	// read as if something went wrong (#49 case 2). On rename failure the run
	// continues with the old ledger in place. Deliberately after every
	// refusal guard above: each of those claims nothing was changed, and
	// rotating first made that a lie.
	ledger := filepath.Join(omk, "ledger.tsv")
	if fileRegular(ledger) && ledgerNeedsRotate(ledger) {
		os.Rename(ledger, ledger+".pre-v2.bak")
	}

	// ---- guarded cut-over ----
	// cutSet feeds the collision scan below: a just-untracked path was
	// explicitly consented (OMAKASE_CUTOVER_CONFIRM=1), so the injected
	// copy taking it over is the point, not a conflict.
	cutSet := map[string]bool{}
	if cutover {
		var cut []string
		for _, rel := range payloadRels {
			if gitTracked(root, rel) {
				cut = append(cut, rel)
			}
		}
		if len(cut) == 0 {
			fmt.Fprintln(stdout, msgCutOverNothingTracked)
		} else {
			fmt.Fprintf(stdout, msgCutOverPlan, len(cut))
			for _, c := range cut {
				fmt.Fprintf(stdout, "    %s\n", c)
			}
			fmt.Fprint(stdout, msgCutOverConsequences)
			if os.Getenv("OMAKASE_CUTOVER_CONFIRM") != "1" {
				fmt.Fprintln(stderr, msgCutOverUnconfirmed)
				return 1
			}
			args := append([]string{"-C", root, "rm", "--cached", "-q", "--"}, cut...)
			cmd := exec.Command("git", args...)
			cmd.Stdout = stdout
			cmd.Stderr = stderr
			if runErr := cmd.Run(); runErr != nil {
				return exitCode(runErr) // a git rm failure aborts with its code
			}
			for _, c := range cut {
				cutSet[c] = true
			}
			fmt.Fprintf(stdout, msgCutOverStaged, len(cut))
		}
	}

	// adopted: rel is tracked but deliberately served from an injected copy
	// (skip-worktree set by the user's adoption flow, #195). An adopted path
	// behaves like an ordinary injected file in every branch below: no
	// collision warning, no tracked skip, ledger row kept.
	adopted := func(rel string) bool {
		return gitMasked(root, rel)
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
		if !gitTracked(root, rel) || adopted(rel) {
			continue
		}
		fmt.Fprintf(stderr, msgInitTrackedCollision, rel, rel)
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
	priorHash := map[string]string{}
	for _, row := range state.ReadPlaced(filepath.Join(omk, "placed.tsv")) {
		priorHash[row.Rel] = row.Hash
		// Machinery is never a consent item (the toggles refuse it), so a
		// disabled machinery row can only be a pre-guard binary's leftover —
		// honoring it would keep the gate primitive missing on every re-init.
		// Ignore it: init re-places the file and the disable mark drops out.
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

	// ---- collision scan ----
	// All conflicts are found first and the whole init refuses before any
	// file is touched (the Stow rule: scan, then terminate without making
	// modifications). A conflict is an untracked on-disk file at a payload
	// path that differs from the payload AND is not omakase's to replace:
	// either a local edit of a placed file (the ledger hash says the user
	// changed it — the edit lifecycle owns that: diff, then keep or restore)
	// or a file omakase never placed at all (never overwrite what was never
	// consented). Two exemptions: a LEDGERED machinery row always heals to
	// canonical (a drifted .omakase/ internal or manifest is torn state,
	// not an edit — but on a fresh install a pre-existing file there is as
	// foreign as anywhere else), and a path the cut-over above just
	// untracked was explicitly consented. A file whose disk hash still
	// matches its ledger row is just outdated and updates in place.
	var conflicts []string
	for _, rel := range payloadRels {
		if (gitTracked(root, rel) && !adopted(rel)) || declined[rel] || cutSet[rel] {
			continue
		}
		if _, ok := keptPrior[rel]; ok {
			continue
		}
		dest := filepath.Join(root, rel)
		if !lexists(dest) || SameFile(dest, filepath.Join(payload, rel)) {
			continue
		}
		if ph, ok := priorHash[rel]; ok {
			if harness.IsMachinery(rel) {
				continue // ledgered machinery: init owns it, heal to canonical
			}
			if h := state.HashOf(dest); ph != "" && h != "" && h == ph {
				continue // unedited prior placement: a plain update
			}
			conflicts = append(conflicts, fmt.Sprintf(msgConflictEditedRow, rel, rel, rel, rel))
			continue
		}
		conflicts = append(conflicts, fmt.Sprintf(msgConflictForeignRow, rel))
	}
	if len(conflicts) > 0 {
		fmt.Fprintln(stderr, msgFilesInTheWayHeader)
		for _, c := range conflicts {
			fmt.Fprintln(stderr, c)
		}
		return 1
	}

	// ---- place loop ----
	var placed, skipped, overwrote []string
	keptRefilled := map[string]bool{}
	for _, rel := range payloadRels {
		f := filepath.Join(payload, rel)
		dest := filepath.Join(root, rel)
		// Never touch a path the repo tracks (committed file wins) — except
		// an ADOPTED one, whose working copy is deliberately the harness's.
		if gitTracked(root, rel) && !adopted(rel) {
			skipped = append(skipped, rel)
			fmt.Fprintf(stderr, msgSkipTracked, rel)
			continue
		}
		if declined[rel] {
			declinedKept = append(declinedKept, rel)
			fmt.Fprintf(stderr, msgSkipToggledOff, rel, rel)
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
				fmt.Fprintf(stderr, msgKeptRestored, rel)
			} else {
				fmt.Fprintf(stderr, msgSkipKept, rel, rel, rel)
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
		// Differs and not committed: either canonical machinery or an
		// unedited prior placement — the collision scan refused everything
		// else before any file was touched — so replacing it loses nothing.
		if code := placeFile(f, rel, root, umask, stderr); code != 0 {
			return code
		}
		placed = append(placed, rel)
		overwrote = append(overwrote, rel)
		fmt.Fprintf(stderr, msgUpdatedToPayload, rel)
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
		fmt.Fprintf(stderr, msgKeptRestored, rel)
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
				fmt.Fprintf(stderr, msgStaleEditedLeft, rel)
			}
		}
	}

	// ---- exclude block ----
	wtincTracked := gitTracked(root, ".worktreeinclude")
	if wtincTracked {
		fmt.Fprintln(stderr, msgWtincTracked)
	}
	// A symlink never gets the trailing slash even when it targets a
	// directory: git matches a "dir/" pattern against directories only, and
	// to git a symlink is a blob — a slashed entry would never match (#148).
	isDirRoot := func(p string) bool {
		q := filepath.Join(root, p)
		return !isSymlink(q) && isDir(q)
	}
	consented := append(append(append([]string{}, placed...), declinedKept...), keptOrder...)
	prefixes := DerivePrefixes(consented, sharedTopdirs(root, consented), isDirRoot, wtincTracked)

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
			fmt.Fprintf(stderr, msgPrefixedErr, err)
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
			fmt.Fprintf(stderr, msgPrefixedErr, err)
			return 1
		}
		if err := CopyEntry(filepath.Join(root, rel), filepath.Join(omk, "payload-snapshot", rel)); err != nil {
			// A mid-loop failure exits 1 with the prior placed.tsv intact:
			// WritePlaced runs only after this loop finishes.
			return 1
		}
		rows = append(rows, state.PlacedRow{
			Rel:  rel,
			Hash: state.HashOf(filepath.Join(root, rel)),
		})
	}
	for _, rel := range declinedKept {
		src := filepath.Join(payload, rel)
		snapRoot := filepath.Join(omk, "payload-snapshot")
		if err := safeMkdirAll(snapRoot, filepath.Join(snapRoot, filepath.Dir(rel))); err != nil {
			fmt.Fprintf(stderr, msgPrefixedErr, err)
			return 1
		}
		if err := CopyEntry(src, filepath.Join(snapRoot, rel)); err != nil {
			return 1
		}
		rows = append(rows, state.PlacedRow{
			Rel:  rel,
			Hash: state.HashOf(src), // hash of what would be placed (the payload copy)
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
				fmt.Fprintf(stderr, msgPrefixedErr, err)
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
	// The disabled-files sidecar is rewritten wholesale to exactly the rows
	// this init declined to place: a stale entry (a machinery leftover the
	// consent merge ignored, or a path the payload no longer ships) drops
	// out, and a legacy in-column disable is migrated in — after this write
	// the sidecar is the only store of file-level consent.
	declinedSet := map[string]bool{}
	for _, rel := range declinedKept {
		declinedSet[rel] = true
	}
	if err := state.WriteDisabledFiles(omk, declinedSet); err != nil {
		return 1
	}
	if err := state.WritePlaced(filepath.Join(omk, "placed.tsv"), rows); err != nil {
		return 1
	}

	removeF(filepath.Join(omk, "placed.list")) // pre-0.10 record — superseded

	// ---- redundant hooksPath reset ----
	if resetHooksPath {
		exec.Command("git", "-C", root, "config", "--unset", "core.hooksPath").Run() // 2>/dev/null || true
		fmt.Fprintln(stdout, msgClearedHooksPath)
	}

	// ---- hook dispatchers ----
	// The permanent dispatchers (issue #98): written only here — and deleted
	// only by remove — atomically, one per hook omakase dispatches. Their
	// content never varies by repo, branch, or version, so a re-init rewrites
	// identical bytes and an upgrade refreshes the binary copy they exec, not
	// the hook files. lefthook stops owning .git/hooks entirely: no
	// `lefthook install`, no run-time stub sync, no skeleton lefthook.yml.
	// Enforcement wiring is opt-in by content (#149): a harness that
	// declares zero gates gets no pre-commit/pre-push dispatchers.
	// post-checkout is the heal hook, not a gate — a steering-only harness
	// keeps it (new worktrees still auto-install) unless an incumbent owns
	// the hooks, in which case it is skipped and the degrade printed below.
	wantHook := map[string]bool{}
	for _, name := range hook.Names() {
		wantHook[name] = gateCount != 0 || (name == "post-checkout" && len(incumbent) == 0)
	}
	gateHooks := false
	for _, name := range hook.Names() {
		if wantHook[name] {
			if hook.IsGate(name) {
				gateHooks = true
			}
			// About to displace a stock git-lfs stub? Save its exact bytes
			// under $OMK so remove can put it back — otherwise init-then-
			// remove leaves git-lfs half-installed with nothing saying so.
			hf := filepath.Join(hooksDir, name)
			if content, rerr := os.ReadFile(hf); rerr == nil && isStockGitLFSHook(hf, content) {
				bdir := filepath.Join(omk, "displaced-hooks")
				if os.MkdirAll(bdir, 0o755) == nil {
					_ = os.WriteFile(filepath.Join(bdir, name), content, 0o755)
				}
			}
			if err := hook.Write(hooksDir, name); err != nil {
				fmt.Fprintf(stderr, msgHookWriteFailed, name, err)
				return 1
			}
			continue
		}
		// A prior gated init may have written this dispatcher; delete only
		// omakase's own bytes — a foreign hook is never touched.
		if hf := filepath.Join(hooksDir, name); hook.Matches(hf, name) {
			if err := removeF(hf); err != nil {
				return 1
			}
		}
	}
	// A displaced stock git-lfs stub deserves one line (#190): the
	// dispatcher runs `git lfs <hook>` itself, so LFS keeps working — but
	// silence read as "my LFS hook got clobbered".
	for _, name := range lfsDisplaced {
		if wantHook[name] {
			fmt.Fprintf(stdout, msgDisplacedLFSHook, name, name)
		}
	}
	// The dispatchers exec the machine-wide copy at StableBinPath, which is
	// load-bearing only for the gate hooks: they fail closed without it,
	// while the post-checkout heal hook fails open (best-effort by
	// contract), so a heal-only install gets no blocked-commits warning.
	// main() self-installs the copy before RunInit; verify it actually
	// landed — never leave fail-closed hooks silently pointing at nothing.
	// (The probe's hook proof checks the same fact, so the verdict below
	// and later status runs agree with what happens at commit time.)
	if stable := hook.StableBinPath(); gateHooks && (stable == "" || !fileExecutable(stable)) {
		fmt.Fprintf(stderr, msgStableBinaryMissing, stable)
	}

	// ---- summary ----
	fmt.Fprintf(stdout, msgPlacedSummary, len(placed), len(overwrote), len(skipped))
	for _, p := range placed {
		if p != "" {
			fmt.Fprintf(stdout, "  + %s\n", p)
		}
	}
	for _, o := range overwrote {
		if o != "" {
			fmt.Fprintf(stdout, msgRowUpdated, o)
		}
	}
	for _, k := range keptOrder {
		if keptRefilled[k] {
			fmt.Fprintf(stdout, msgRowKeptRestored, k)
		} else {
			fmt.Fprintf(stdout, msgRowKeptUntouched, k)
		}
	}
	for _, w := range swept {
		if w != "" {
			fmt.Fprintf(stdout, msgRowRemovedStale, w)
		}
	}
	for _, s := range skipped {
		if s != "" {
			fmt.Fprintf(stdout, msgRowSkippedCommitted, s)
		}
	}
	// The worktree claim is only made while the heal hook is actually
	// installed (a steering-only harness in a repo with incumbent hooks has
	// no post-checkout to auto-install anything).
	if wantHook["post-checkout"] {
		fmt.Fprintln(stdout, msgIgnoresWiredWorktrees)
	} else {
		fmt.Fprintln(stdout, msgIgnoresWired)
	}
	// Make the opt-in wiring legible (#149): say what was deliberately not
	// installed, and — where the heal hook was skipped too — what replaces it.
	if gateCount == 0 {
		fmt.Fprintln(stdout, msgNoGatesDeclared)
		if !wantHook["post-checkout"] {
			fmt.Fprint(stdout, msgHooksLeftUntouched)
		}
	}
	fmt.Fprintln(stdout, msgSeeStatus)
	// A source's manifest recommends: line; only a source install sets it.
	if recommends != "" {
		fmt.Fprintf(stdout, msgRecommends, recommends)
	}
	fmt.Fprint(stdout, msgCustomizeHint)
	// No UX stanzas here: the status-bar wiring is machine config the init
	// verb (cmd/omakase) applies after this returns — wired by default into
	// empty host slots, #123 item 5 — and worktree discipline and its guard
	// are harness POLICY (#172): a harness that wants them ships its own
	// script and recommends the wiring; the binary does not advertise them.

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
		fmt.Fprintf(stderr, msgPrefixedErr, err)
		return 1
	}
	if isDir(dest) && !isSymlink(dest) {
		fmt.Fprintf(stderr, msgOverlayDirInWay, rel)
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

// checkPayloadSymlinks refuses a payload carrying any symlink whose target is
// absolute or lexically climbs out of the repo root (resolved from the link's
// own parent directory). Placement copies symlinks verbatim (CopyEntry), so
// this is the one point that keeps an untrusted payload from planting a link
// that reads or writes outside the repo (issue #30). The check is lexical:
// every payload entry is individually validated, so a chain through another
// payload symlink is caught when that link is itself checked.
func checkPayloadSymlinks(payload string, rels []string, stderr io.Writer) int {
	for _, rel := range rels {
		info, err := os.Lstat(filepath.Join(payload, rel))
		if err != nil || info.Mode()&os.ModeSymlink == 0 {
			continue
		}
		target, err := os.Readlink(filepath.Join(payload, rel))
		if err != nil {
			fmt.Fprintf(stderr, msgSymlinkUnreadable, rel)
			return 1
		}
		resolved := filepath.Clean(filepath.Join(filepath.Dir(rel), target))
		if filepath.IsAbs(target) || resolved == ".." || strings.HasPrefix(resolved, ".."+string(filepath.Separator)) {
			fmt.Fprintf(stderr, msgSymlinkEscapesRepo, rel, target)
			return 1
		}
	}
	return 0
}

// walkPayload lists the payload's regular files and symlinks as clean
// payload-relative paths, in filepath.WalkDir's lexical order. Directories
// and other special files are excluded; symlinks are never followed.
// Editor/OS cruft (.DS_Store, *.bak) is skipped — a source repo that commits
// it, or a local-dir payload carrying it untracked, must not see it placed,
// ledgered, or snapshotted (issue #31).
func walkPayload(payload string) ([]string, error) {
	var rels []string
	err := filepath.WalkDir(payload, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if name := d.Name(); name == ".DS_Store" || strings.HasSuffix(name, ".bak") {
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

// checkBaseDowngrade refuses an init whose payload carries an OLDER
// .omakase/VERSION than the repo already has (#189): that init was run by a
// stale entry point, not by intent. Both sides must parse as x.y.z for the
// guard to fire — a dev build ("dev"), a missing file, or a never-installed
// repo never refuses. A repo that merely COMMITS an .omakase/VERSION with no
// omakase installed is not "set up by a newer omakase" either: the guard
// would refuse a first install forever (init never overwrites a committed
// path, so the file can never change), so that shape is excluded.
func checkBaseDowngrade(root, omk, payload string, stderr io.Writer) int {
	incoming := state.FirstLine(filepath.Join(payload, ".omakase", "VERSION"))
	installed := state.FirstLine(filepath.Join(root, ".omakase", "VERSION"))
	iv, okIn := parseVersion(incoming)
	rv, okRepo := parseVersion(installed)
	if !okIn || !okRepo || !versionLess(iv, rv) {
		return 0
	}
	if !fileRegular(filepath.Join(omk, "placed.tsv")) && gitTracked(root, ".omakase/VERSION") {
		return 0
	}
	// Name the remembered source in the deliberate-downgrade path: remove
	// deletes $OMK/source, so "remove then init again" would otherwise
	// destroy the only copy of the string the re-init needs.
	again := "init again"
	if src := state.FirstLine(filepath.Join(omk, "source")); src != "" {
		again = "omakase init " + src
	}
	fmt.Fprintf(stderr, msgDowngradeRefused, installed, incoming, again)
	return 2
}

// parseVersion parses a strict x.y.z numeric version.
func parseVersion(s string) ([3]int, bool) {
	var v [3]int
	parts := strings.Split(strings.TrimSpace(s), ".")
	if len(parts) != 3 {
		return v, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return v, false
		}
		v[i] = n
	}
	return v, true
}

func versionLess(a, b [3]int) bool {
	for i := range a {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return false
}

// gitMasked reports whether rel is tracked but carries the skip-worktree bit —
// the state you get when someone deliberately overlays a harness copy on top of
// a path the repo commits, without staging a deletion for everyone else (the
// alternative to --cut-over on a shared repo). git is being told to ignore the
// working tree for that path, so an injected copy sitting there is intentional,
// not the upstream-clobber collision the tracked check normally means.
//
// `git ls-files -v` prefixes each entry with a status letter; only S
// (skip-worktree) counts. Lowercase letters mean assume-unchanged, a
// different and much more fragile promise — including 'h', which git shows
// when BOTH bits are set, so an adoption script that sets both gets none of
// the masked-path behavior (set only skip-worktree). The pathspec is
// :(literal) so a rel containing glob characters cannot match a masked
// sibling's row, the returned NAME is compared (not just the tag), and a
// case-insensitive retry keeps this in agreement with gitTracked's :(icase)
// retry on case-folding filesystems.
func gitMasked(root, rel string) bool {
	if rel == "" {
		return false
	}
	if tag, name, ok := lsFileTag(root, ":(literal)"+rel); ok {
		return tag == 'S' && name == rel
	}
	if tag, name, ok := lsFileTag(root, ":(literal,icase)"+rel); ok {
		return tag == 'S' && strings.EqualFold(name, rel)
	}
	return false
}

// lsFileTag runs `ls-files -z -v` on one pathspec and returns the first
// entry's status letter and raw name (-z: quoting would break the name
// comparison). ok is false when git failed or nothing matched.
func lsFileTag(root, spec string) (byte, string, bool) {
	out, err := exec.Command("git", "-C", root, "ls-files", "-z", "-v", "--", spec).Output()
	if err != nil {
		return 0, "", false
	}
	entry, _, _ := strings.Cut(string(out), "\x00")
	if len(entry) < 3 || entry[1] != ' ' {
		return 0, "", false
	}
	return entry[0], entry[2:], true
}

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

// sameSourceLabel reports whether two source labels name the same harness
// despite spellings a person naturally introduces — a trailing slash or
// GitHub's ".git" clone suffix. Anything it cannot equate refuses (the safe
// direction: a false refusal names the main checkout; a false pass would
// re-point every worktree).
func sameSourceLabel(a, b string) bool {
	norm := func(s string) string {
		s = strings.Replace(s, ".git//", "//", 1)
		s = strings.Replace(s, ".git#", "#", 1)
		return strings.TrimSuffix(strings.TrimSuffix(s, "/"), ".git")
	}
	return norm(a) == norm(b)
}
