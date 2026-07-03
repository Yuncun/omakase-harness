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
	"strconv"
	"strings"
	"syscall"
	"time"

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

	// ---- v1→v2 migration (design §9) ----
	// EnsureSources (migrate.go) reads the prior sources.tsv and, for a still-v1
	// repo ($OMK/source present, no sources.tsv), synthesizes + writes it once,
	// silently — and prints the mixed-era warning when a v1 tool rewrote $OMK/source
	// out from under a v2 sources.tsv (init REHEALS below by re-fetching the recorded
	// stack and re-recording sources.tsv with resolved commits). `recorded` is the
	// authoritative layer stack, bottom-to-top, with ordinal labels ("1","2").
	// sourcesExisted is captured BEFORE EnsureSources so the end-of-run
	// faithful-rewrite branch reflects the TRUE pre-run on-disk state — GC2 (a
	// base-only repo has no $OMK/source, so EnsureSources synthesizes nothing and
	// writes no file) holds.
	sourcesPath := filepath.Join(omk, "sources.tsv")
	sourcesExisted := fileRegular(sourcesPath)
	recorded := EnsureSources(omk, stderr)

	// resolveBase resolves the BASE-layer payload dir the plain-install way
	// (bin/init.sh:199): OMAKASE_PAYLOAD overrides, else the binary-relative
	// ../payload default. Used only by a base-only install (no source). Returns
	// (dir, true) or prints the "payload dir not found" line and returns (_, false).
	resolveBase := func() (string, bool) {
		b := os.Getenv("OMAKASE_PAYLOAD")
		if b == "" {
			b = defaultPayload()
		}
		b = strings.TrimSuffix(b, "/")
		if info, statErr := os.Stat(b); statErr != nil || !info.IsDir() {
			fmt.Fprintf(stderr, "omakase: payload dir not found at %s\n", b)
			return "", false
		}
		return b, true
	}

	// ---- resolve the target layer stack (Phase 3.5 decision table) ----
	// A CLI source arg that already matches a recorded layer REPAIRS that layer at
	// its pin (no reorder); a NEW source STACKS on top (cap 2); a bare init re-applies
	// the recorded stack at its pins; a base override (OMAKASE_PAYLOAD) or an empty
	// stack takes the v1 base-only path (GC1/GC2).
	explicitSource := source != ""
	cliExpanded, cliRef := "", ""
	if explicitSource {
		cliExpanded, cliRef = expandSource(source)
	}
	matchIdx := -1
	if explicitSource && cliExpanded != "" {
		for i := range recorded {
			if recorded[i].Source == cliExpanded {
				matchIdx = i
				break
			}
		}
	}

	// bottomRemembered is the v1 remembered bottom source ($OMK/source). On a bare
	// init it re-derives the bottom layer — the §9 reheal signal when a v1 tool
	// rewrote it out from under sources.tsv (recorded). Normally it equals
	// recorded[0]'s source, so it is a no-op.
	bottomRemembered := state.FirstLine(filepath.Join(omk, "source"))

	narrateStack := false
	stackLabelA, stackLabelB := "", ""
	var plan []layerPlan
	switch {
	case explicitSource && cliExpanded == "":
		// pathological "#ref" that expanded to an empty source: v1 treats it as no
		// source — base-only, matching the current post-expansion install decision.
	case explicitSource && matchIdx >= 0:
		// REPAIR the matched layer at the CLI pin; reuse the others. No reorder.
		for i := range recorded {
			pl := layerPlan{source: recorded[i].Source, ref: unRefField(recorded[i].Ref), epoch: recorded[i].Epoch, commit: recorded[i].Commit}
			if i == matchIdx {
				pl.ref = cliRef
				pl.refetch = true
			}
			plan = append(plan, pl)
		}
	case explicitSource:
		// NEW source (matchIdx < 0).
		if len(recorded) >= 2 {
			// GC8 cap: a third distinct source errors and mutates NOTHING (this is
			// BEFORE any fetch/expand of $OMK or the working tree — EnsureSources only
			// read an existing sources.tsv on a 2-stack, it wrote nothing).
			fmt.Fprintf(stderr, "omakase: this repo already has 2 harnesses (%s, %s) — remove one first: omakase remove <source>\n",
				reassembleSource(recorded[0]), reassembleSource(recorded[1]))
			return 1
		}
		for i := range recorded { // reuse the 0-or-1 recorded layer(s) beneath the new top
			plan = append(plan, layerPlan{source: recorded[i].Source, ref: unRefField(recorded[i].Ref), epoch: recorded[i].Epoch, commit: recorded[i].Commit})
		}
		plan = append(plan, layerPlan{source: cliExpanded, ref: cliRef, refetch: true}) // new top: epoch "" → now
		if len(recorded) == 1 {
			narrateStack = true
			stackLabelA = reassembleSource(recorded[0])
			stackLabelB = displayLabel(cliExpanded, cliRef)
		}
	case os.Getenv("OMAKASE_PAYLOAD") != "":
		// env base override, no source arg: base-only, ignoring the recorded stack
		// (GC1 — TestOmakasePayloadOverridesRememberedSource).
	case len(recorded) > 0 || bottomRemembered != "":
		// bare init: re-apply the recorded stack at its pins (repair all). The BOTTOM
		// layer's source is re-derived from $OMK/source (the §9 reheal signal); upper
		// layers come from sources.tsv. When $OMK/source agrees with recorded[0] (the
		// normal case) the bottom row's epoch + commit are preserved, so a plain bare
		// re-init leaves sources.tsv byte-identical.
		bottomSrc, bottomRef := "", ""
		if len(recorded) > 0 {
			bottomSrc, bottomRef = recorded[0].Source, unRefField(recorded[0].Ref)
		}
		if bottomRemembered != "" {
			bottomSrc, bottomRef = expandSource(bottomRemembered)
		}
		bottomEpoch, bottomCommit := "", ""
		if len(recorded) > 0 && recorded[0].Source == bottomSrc {
			bottomEpoch, bottomCommit = recorded[0].Epoch, recorded[0].Commit
		}
		plan = append(plan, layerPlan{source: bottomSrc, ref: bottomRef, refetch: true, epoch: bottomEpoch, commit: bottomCommit})
		for i := 1; i < len(recorded); i++ {
			plan = append(plan, layerPlan{source: recorded[i].Source, ref: unRefField(recorded[i].Ref), refetch: true, epoch: recorded[i].Epoch, commit: recorded[i].Commit})
		}
	default:
		// no source, no recorded, no override: base-only.
	}

	// ---- fetch/reuse each layer, assemble specs + sources.tsv rows ----
	// A fresh layer is fetched and mapped through the §7 slot-fallback rule
	// (rootSlotFree computed bottom-to-top: the first layer to place a root AGENTS.md
	// owns the slot; later layers, and any layer under a committed root instruction
	// file, reroute to CLAUDE.local.md). A reused layer copies its persisted store's
	// files/ tree as-is (offline, no re-fetch). The BOTTOM layer folds the base under
	// its delta (runSource); a stacked layer ships its delta alone (fetchSource).
	epoch := strconv.FormatInt(time.Now().Unix(), 10)
	sourceLabel := "payload" // placed.tsv col3 fallback for the base-only path
	rememberedSource := ""
	recommends := ""

	// ---- Pass 1: fetch/reuse each layer, resolving its payloadDir + metadata,
	// WITHOUT deciding the root slot yet. The slot decision below is cut-over-aware
	// (Fix F): a committed instruction file --cut-over will untrack must NOT count
	// as taking the slot, and that depends on what the payload SHIPS — which is only
	// known after the fetch. Fetching does not depend on the slot decision (rootSlotFree
	// is only USED in the spec/mapping pass), so this split is behavior-preserving. ----
	type fetchedLayer struct {
		ordinal    LayerName
		source     string // plan source string (sources.tsv col2)
		ref        string // plan ref ("" = none), for the sources.tsv ref column
		label      string // displayLabel / runSource label (spec + fellBack narration)
		payloadDir string
		preMapped  bool
		rowEpoch   string
		rowCommit  string
	}
	var fetched []fetchedLayer
	for i := range plan {
		ordinal := LayerName(strconv.Itoa(i + 1))
		pl := plan[i]
		storeDir := filepath.Join(omk, "layers", string(ordinal))
		refetch := pl.refetch || !isDir(storeDir) // a missing store must be (re)built
		fl := fetchedLayer{ordinal: ordinal, source: pl.source, ref: pl.ref, label: displayLabel(pl.source, pl.ref)}
		fl.rowEpoch = pl.epoch
		if fl.rowEpoch == "" {
			fl.rowEpoch = epoch
		}
		fl.rowCommit = pl.commit
		if fl.rowCommit == "" {
			fl.rowCommit = "-"
		}
		if refetch {
			if i == 0 {
				res, code := runSource(pl.source, pl.ref, defaultPayload(), stdout, stderr)
				if code != 0 {
					return code // runSource printed the message + cleaned any staging dir
				}
				defer os.RemoveAll(res.merged) // v1's EXIT-trap cleanup (bin/init.sh:63-68)
				fl.payloadDir = res.payload
				fl.label = res.label
				rememberedSource = res.remembered
				recommends = res.recommends
			} else {
				pDir, _, code := fetchSource(pl.source, pl.ref, stdout, stderr)
				if code != 0 {
					return code
				}
				fl.payloadDir = pDir
			}
			fl.rowCommit = resolvedCommit(pl.source)
		} else {
			// Reuse the persisted store's post-mapping files/ tree (preMapped).
			fl.payloadDir = filepath.Join(storeDir, "files")
			fl.preMapped = true
		}
		fetched = append(fetched, fl)
	}

	// ---- cut-over slot awareness (Fix F) ----
	// A committed root instruction file the payload ALSO ships is untracked by
	// --cut-over below (the cut runs before the place loop, exactly as legacy does),
	// freeing the root slot — so it must NOT count as taking the slot here. Whether
	// the payload ships that canonical path is read from the fetched payload dirs
	// (pre-mapping rels: a canonical AGENTS.md, not the CLAUDE.local.md its fallback
	// would reroute to). With the slot freed, the payload's AGENTS.md is placed at the
	// root and the cut set (built from the merged staging below, which now carries
	// AGENTS.md at the root rather than a reroute) untracks it — matching legacy.
	committedInstr := gitTracked(root, "AGENTS.md") || gitTracked(root, "CLAUDE.md")
	if cutover {
		payloadShips := func(want string) bool {
			for _, fl := range fetched {
				rels, _ := walkPayload(fl.payloadDir)
				for _, rel := range rels {
					if rel == want {
						return true
					}
				}
			}
			return false
		}
		committedAGENTS := gitTracked(root, "AGENTS.md") && !payloadShips("AGENTS.md")
		committedCLAUDE := gitTracked(root, "CLAUDE.md") && !payloadShips("CLAUDE.md")
		committedInstr = committedAGENTS || committedCLAUDE
	}

	// ---- Pass 2: decide the root slot bottom-to-top, assemble specs + sources.tsv rows. ----
	rootTaken := committedInstr
	rootOwnerIdx := -1
	var specs []layerSpec
	var stackRows []state.SourceRow
	var fellBackLabels []string
	for i := range fetched {
		fl := fetched[i]
		rootSlotFree := !rootTaken
		spec := layerSpec{layer: fl.ordinal, label: fl.label, payloadDir: fl.payloadDir, rootSlotFree: rootSlotFree, preMapped: fl.preMapped}
		if !fl.preMapped && !rootSlotFree && lexists(filepath.Join(fl.payloadDir, "AGENTS.md")) {
			fellBackLabels = append(fellBackLabels, fl.label) // GC5 slot-fallback narration
		}

		// Root-slot ownership: a fresh layer owns it iff it placed a root AGENTS.md
		// while the slot was free; a reused store owns it iff its files/ carry one.
		owns := lexists(filepath.Join(spec.payloadDir, "AGENTS.md"))
		if !spec.preMapped {
			owns = owns && rootSlotFree
		}
		if owns {
			rootTaken = true
			if rootOwnerIdx < 0 {
				rootOwnerIdx = i
			}
		}

		specs = append(specs, spec)
		stackRows = append(stackRows, state.SourceRow{Layer: string(fl.ordinal), Source: fl.source, Ref: refField(fl.ref), Commit: fl.rowCommit, Epoch: fl.rowEpoch})
	}

	// §7 bridge: the root-slot-owning layer may ALSO place a CLAUDE.md -> AGENTS.md
	// symlink. BridgeWanted stays project-keyed (Phase 3.5 kept it as-is), so the
	// owning layer's post-mapping rels are keyed under LayerProject and every other
	// layer under its ordinal (so the "CLAUDE.md anywhere suppresses" scan still sees
	// them). Only a FRESH owner adds the bridge; a reused store already carries its own.
	if rootOwnerIdx >= 0 && !specs[rootOwnerIdx].preMapped {
		bridgeSets := make(map[LayerName][]string, len(specs))
		var ownerMapped []string
		for i := range specs {
			rels, werr := walkPayload(specs[i].payloadDir)
			if werr != nil {
				fmt.Fprintf(stderr, "omakase: failed to scan layer %s payload: %s\n", specs[i].layer, werr)
				return 1
			}
			mapped := make([]string, 0, len(rels))
			for _, rel := range rels {
				if specs[i].preMapped {
					mapped = append(mapped, rel)
				} else {
					dest, _ := MapInstruction(rel, specs[i].rootSlotFree)
					mapped = append(mapped, dest)
				}
			}
			key := specs[i].layer
			if i == rootOwnerIdx {
				key = LayerProject
				ownerMapped = mapped
			}
			bridgeSets[key] = mapped
		}
		tracksCLAUDE := gitTracked(root, "CLAUDE.md")
		// LIVE/merged bridge (all-layers rule): a higher layer's explicit CLAUDE.md
		// suppresses the bridge in the working tree, so the live CLAUDE.md is that
		// explicit file, not a bridge symlink.
		if BridgeWanted(LayerProject, bridgeSets, tracksCLAUDE) {
			specs[rootOwnerIdx].bridge = true
		}
		// PERSISTED store bridge (Fix E — single-layer rule): what a fresh install
		// of the root-owner's OWN payload alone would produce, exactly as
		// RemoveLayer's re-fold derives it. Never suppressed by ANOTHER layer's
		// CLAUDE.md — the stored bottom layer must carry its own bridge so a later
		// top-removal (which carries layers/1 through verbatim) restores it.
		if BridgeWanted(LayerProject, map[LayerName][]string{LayerProject: ownerMapped}, tracksCLAUDE) {
			specs[rootOwnerIdx].storeBridge = true
		}
	}

	// ---- assemble the placement tree ----
	// A base-only install (no source layers) takes the v1 path VERBATIM — no merged
	// staging, no layers/, no sources.tsv (GC2 invariance). Any stack routes through
	// buildMergedStaging: higher-layer-wins whole-file replacement (design §4), placed
	// per-row with the WINNING layer's label (labelByRel).
	var payload string
	var labelByRel map[string]string
	if len(specs) > 0 {
		staging, lbl, merr := buildMergedStaging(specs)
		if merr != nil {
			// Fail-closed BEFORE any guard or placement: either two payload files
			// fight over one destination (the §7 AGENTS.md + explicit CLAUDE.local.md
			// case) or a file/symlink collides with a directory beneath it (the
			// stacked-parent conflict). merr describes which. No store touched,
			// nothing placed.
			fmt.Fprintf(stderr, "omakase: refusing to install — %s; nothing was placed.\n", merr)
			return 1
		}
		defer os.RemoveAll(staging)
		payload = staging
		labelByRel = lbl
	} else {
		base, ok := resolveBase()
		if !ok {
			return 1
		}
		payload = base
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

	// ---- persist each FRESH stack layer's store (design §4/§5: $OMK/layers/<layer>/) ----
	// Built tmp+rename HERE — after every refusal-capable guard has passed (wiring,
	// lefthook, incumbent, cut-over) and before the place loop's and the orphan
	// sweep's working-tree mutations. So a refusal above leaves $OMK/layers/
	// untouched, and a post-checkout heal racing a removal never observes a partial
	// store (design §4 rebuild ordering / GC3). A base-only install has no specs and
	// builds NOTHING (GC2). A reused (preMapped) layer's store already exists and is
	// left byte-untouched — only freshly fetched layers are (re)built. Each store is
	// the layer's FULL post-mapping file set — the shadow-restore source `remove
	// <source>` needs (Task 4).
	for _, s := range specs {
		if s.preMapped {
			continue // reused store already persisted
		}
		if _, blErr := BuildLayerStore(omk, s.layer, s.label, s.payloadDir, s.rootSlotFree, s.storeBridge); blErr != nil {
			fmt.Fprintln(stderr, blErr.Error())
			return 1
		}
	}

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
	prefixes := DerivePrefixes(placed, harness.SharedTopdirs, isDirRoot, lefthookTracked, wtincTracked)

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
	excludeContent, _ := os.ReadFile(exclude)
	excludeOut := textblock.AppendBlock(textblock.Strip(excludeContent, begin, end), begin, prefixes, end)
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
	if !wtincTracked && len(placed) > 0 {
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

	// ---- sources.tsv: the layer stack, bottom-to-top, ordinal labels (design §5/§9) ----
	// stackRows was assembled during the fetch/reuse loop: one row per layer, ordinal
	// label "1"/"2" by stack position (GC6), commit = the resolved sha recorded at
	// fetch (or the preserved commit of a reused layer), epoch preserved for an
	// already-recorded layer and stamped now for a freshly added one. Written only
	// when there is a row to record OR the file already exists (then rewritten
	// faithfully); a bare base-only install with no prior sources.tsv writes nothing
	// (GC2).
	if len(stackRows) > 0 || sourcesExisted {
		rowsToWrite := stackRows
		if len(rowsToWrite) == 0 {
			rowsToWrite = recorded // faithful rewrite of an existing file we add nothing to
		}
		if err := state.WriteSources(sourcesPath, rowsToWrite); err != nil {
			fmt.Fprintln(stderr, err.Error())
			return 1
		}
	}

	// labelFor is placed.tsv column 3: the WINNING layer's label for a layered
	// install (a base row says "payload"; any other layer's row is its source
	// label, "source" or "source#ref"), else the base-only sourceLabel
	// ("payload"). Every layered placed rel is in labelByRel; the fallback
	// covers the base-only path (labelByRel is nil).
	labelFor := func(rel string) string {
		if labelByRel != nil {
			if l, ok := labelByRel[rel]; ok {
				return l
			}
		}
		return sourceLabel
	}
	var rows []state.PlacedRow
	for _, rel := range placed {
		if rel == "" {
			continue
		}
		// safeMkdirAll refuses a symlinked parent under the snapshot root (twin of
		// the RemoveLayer snapshot rebuild): the placed set never pairs a leaf `data`
		// with a `data/loot` beneath it, but guard the snapshot mirror the same way
		// so no copy is ever written through a directory-symlink out of the snapshot.
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
			Src:     labelFor(rel),
			Hash:    state.HashOf(filepath.Join(root, rel)),
			Enabled: "1",
		})
	}
	if err := state.WritePlaced(filepath.Join(omk, "placed.tsv"), rows); err != nil {
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
	// GC5 slot-fallback narration: a freshly placed layer whose canonical root
	// AGENTS.md could not take the root slot (a committed root instruction file, or a
	// lower layer already owns it) landed at CLAUDE.local.md instead, one line per layer.
	for _, label := range fellBackLabels {
		fmt.Fprintf(stdout, "omakase: instructions from %s -> CLAUDE.local.md (root slot taken)\n", label)
	}
	// GC5 stacking narration: a second source stacked on top of the first. The
	// override lines name every LIVE (previously-placed) path the new top layer now
	// wins, sorted by rel — the diff of placed.tsv column 3 before/after.
	if narrateStack {
		fmt.Fprintf(stdout, "omakase: stacked %s on top of %s\n", stackLabelB, stackLabelA)
		var overrides []string
		for _, rel := range priorPaths {
			// Fix D: an override line asserts the new top layer's copy is now IN
			// EFFECT at a live path. A rel that the merged view says B wins
			// (labelByRel==stackLabelB) but that the place loop did NOT actually
			// write this run — because the repo has since COMMITTED it, so the
			// committed file wins and B's copy was skipped (printed as `~ skipped
			// (committed…)`) — must NOT be narrated as overridden. Require the rel
			// to be in the run's actually-placed set, never merely in the merged view.
			if rel != "" && labelByRel[rel] == stackLabelB && contains(placed, rel) {
				overrides = append(overrides, rel)
			}
		}
		sort.Strings(overrides)
		for _, rel := range overrides {
			fmt.Fprintf(stdout, "  ^ overrides %s: %s\n", stackLabelA, rel)
		}
	}
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
	// chain under root — a prior placement (or a stacked lower layer) may have put
	// a directory-symlink at a parent, and writing the child THROUGH it would land
	// outside the repo. Surfaced fail-closed, never swallowed.
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

// wiringStrandedRefs reports the .omakase/*.sh scripts filesDir's
// lefthook-local.yml references but that filesDir does not itself ship — the same
// fail-closed wiring check init runs (wiringRefs + a per-ref existence test),
// applied to a SURVIVOR tree so `remove <source>` can refuse before mutation when
// unlayering would leave the surviving wiring pointing at a script only the
// removed layer supplied (which would fail every commit with exit 127). Returns a
// leading-space-joined list of the missing refs, or "" when nothing is stranded
// (including no lefthook-local.yml at all). Mirrors init.go's install-time guard.
func wiringStrandedRefs(filesDir string) string {
	wiring := filepath.Join(filesDir, "lefthook-local.yml")
	if !fileRegular(wiring) {
		return ""
	}
	missing := ""
	for _, ref := range wiringRefs(wiring) {
		if !fileRegular(filepath.Join(filesDir, ref)) {
			missing += " " + ref
		}
	}
	return missing
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
