// messages.go is the one file that holds every user-facing sentence this
// package can print (#179 D4) — read it top to bottom and you have read
// init/remove/diff/source/record/hook's entire voice. Call sites reference
// these constants only; voice_test.go fails the build on a stray literal.
//
// A constant's name states what its call site must have proven — pick names
// that make a mismatch visible (msgNothingInstalled is a different claim
// than msgNoRememberedSource).
//
// The voice rules (#49, and Eric's no-slop rule):
//   - Every message is either the verb's normal output, or an ACTION — 1–2
//     lines saying exactly what to do next. Internal plumbing that the user
//     cannot act on is not printed at all.
//   - Raw mechanism never leaks: no Go error dumps where a sentence will do,
//     no internal paths the user didn't give us.
//   - When the cause is ambiguous, state the fact and the likely fixes —
//     never a confidently wrong diagnosis.
package overlay

// Shared across verbs.
const (
	msgNotAGitRepo = "omakase: not inside a git repo"
)

// hook — what the .git/hooks dispatchers exec. Gate hooks block fail-closed;
// heal (post-checkout / session-start) warns and never fails the checkout.
const (
	msgHookUsage = "usage: omakase hook <pre-commit|pre-push|post-checkout|session-start> [hook args...]"

	// Fail-closed refusals: the gate hook cannot verify the harness it is
	// supposed to enforce.
	msgHookNoRepoBlock    = "omakase: BLOCKING — %s: not inside a git repository; the harness cannot be verified.\n"
	msgHookTornStateBlock = "omakase: BLOCKING — %s: omakase hooks are installed but no harness state exists in this repo.\n"
	msgHookTornStateFix   = "omakase: restore it with  omakase init  — or take the hooks out with  omakase remove."

	msgSessionStartRestored = "omakase: restored %d missing harness file(s) at session start — run `omakase status` to review.\n"

	// verifyPresent: enabled placed rows missing from this worktree.
	msgVerifyIncompleteBlock = "omakase: BLOCKING — the injected harness is incomplete; its gates would silently not run:"
	msgVerifyMissingRow      = "  missing: %s\n"
	msgVerifyIncompleteFix   = "omakase: restore it with  omakase init  and retry."

	// healWorktree warnings: surface, never overwrite.
	msgHealTrackedCollision = "omakase: WARNING — injected path '%s' is now TRACKED by the repo; your personal copy was likely clobbered by an upstream commit (git overwrites ignored files on checkout). Last-injected copy: %s — diff it against the tracked file, then drop the path from your payload or cut over (init --cut-over).\n"
	msgHealKeptDrift        = "omakase: WARNING — '%s' differs from your accepted (kept) version. Your copy is left as-is. See the change:  omakase diff %s  — then keep it (omakase status --keep %s) or go back (omakase status --restore %s).\n"
	msgHealDrift            = "omakase: WARNING — injected '%s' has DRIFTED from canonical (ledger %s…, on-disk %s…); a gate may be weakened or stale. Drift only surfaces — your copy is left as-is. Adopt canonical with: %s\n"
	msgHealDriftFixInit     = "omakase init"
	msgHealDriftFixCp       = "cp -P '%s' '%s'  (or omakase init to re-sync every file)"
	msgPrefixedErr          = "omakase: %v\n"
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

// init — argument errors, refusals, placement reporting, and the closing
// summary. Every refusal that changed nothing says so.
const (
	msgSourceNeedsValue      = "omakase: --source needs a git URL or local path"
	msgUnknownOption         = "omakase: unknown option '%s'\n"
	msgExtraArgument         = "omakase: unexpected extra argument '%s' (source already set)\n"
	msgSourceBadChars        = "omakase: --source must not contain a tab or newline"
	msgSourceMissingRepoPart = "omakase: source '//%s' is missing the repo part before the '//' subpath marker\n"
	msgSourceSubpathEscapes  = "omakase: source subpath '%s' must stay inside the source repo (relative, no '..')\n"

	// Refused from a linked worktree: hooks/ignores/source are shared state.
	msgLinkedWorktreeRefusal = `omakase: this checkout is a linked worktree of %s.
         Hooks, ignores, and the remembered source are shared by every checkout of the
         repository, so switching its harness from here would silently re-point all of
         them. To change the repository's harness, run from the main checkout:
           cd %s && omakase init %s
omakase: nothing was changed.
`

	// Bare init with nothing to act on. Two different proven states — a
	// harness whose source was forgotten is NOT "no harness installed"
	// (the #126 bug class).
	msgRefreshNoRememberedSource = "omakase: nothing to refresh — a harness is installed here, but no source is remembered to refresh it from. See what's installed:  omakase status"
	msgRefreshNothingInstalled   = "omakase: nothing to refresh — no harness is installed in this repo. See the agent config present here:  omakase status"

	msgBaseMaterializeFailed = "omakase: cannot materialize the base harness payload: %v\n"
	msgPayloadDirMissing     = "omakase: payload dir not found at %s\n"
	msgLefthookYmlRefused    = "omakase: this harness declares gates in lefthook-local.yml, which omakase no longer reads. Declare them as gate: blocks in omakase.manifest (see the README) and delete the yml. Nothing was changed."
	msgManifestReadFailed    = "omakase: could not read %s: %v. Nothing was changed.\n"
	msgGateDeclInvalid       = "omakase: invalid gate declaration in omakase.manifest: %v. Nothing was changed.\n"
	// The %v names the gate and the script it references. Two causes share
	// this refusal, and the likelier one for an adopter is NOT the wiring:
	// an omakase older than the harness expects merges a base payload that
	// lacks the script (#49's mis-blame incident — the message must not send
	// the user off to edit wiring that is correct).
	msgGateWiringInvalid = `omakase: %v. It would fail at commit time (exit 127). Nothing was changed.
         If you didn't edit this harness yourself, your omakase install is likely older
         than the harness expects — update it (brew upgrade omakase, or update the
         plugin), then re-run. Otherwise fix the run: reference or ship the script.
`

	// Refused over an incumbent hook manager (husky and kin) — the header,
	// then one "  - path" row per finding, then the consequences.
	msgIncumbentRefusalHeader = "omakase: REFUSING to install — an incumbent hook manager is present:"
	msgIncumbentRefusalBody   = `  Installing omakase's hooks would displace the project's own, silently disabling
  its gates — and a husky prepare script would overwrite them back on the next
  npm install. omakase does not chain hook managers (v1).
  If these are stale leftovers, remove them and re-run. If the project really uses
  them, do not install omakase here. Nothing was changed.
`

	// Guarded cut-over: the plan, the consequences, the refusal, the receipt.
	msgCutOverNothingTracked = "omakase: --cut-over: no payload path is tracked by this repo — nothing to cut over."
	msgCutOverPlan           = "omakase: cut-over will run  git rm --cached  on %d tracked file(s):\n"
	msgCutOverConsequences   = `  This STAGES A DELETION of each shared file. The next commit — including an agent
  auto-commit — applies that deletion FOR EVERYONE who pulls it, and upstream changes
  to these files will then produce modify/delete conflicts. The files stay on disk;
  the injected (gitignored) copies take over locally. Undo before committing with
  'git restore --staged <file>'; 'git add <file>' re-tracks later.
`
	msgCutOverUnconfirmed = "omakase: REFUSING cut-over without confirmation. Re-run with OMAKASE_CUTOVER_CONFIRM=1 to proceed. Nothing was changed."
	msgCutOverStaged      = "omakase: cut-over staged %d deletion(s) — review with 'git status' and commit them yourself.\n"

	// A previously injected path is now tracked by the repo (upstream landed
	// a file there).
	msgInitTrackedCollision = `omakase: WARNING — '%s' was injected (personal, gitignored) but is NOW TRACKED by the repo.
  An upstream commit likely landed a file at this path; git silently overwrites ignored
  files on checkout/pull, so your personal copy was likely clobbered. The harness's own
  version still lives in your harness source. Reconcile: drop '%s' from your payload,
  or run init --cut-over (guarded) to untrack the file and let the injected copy take over.
`

	msgFilesInTheWayHeader = "omakase: REFUSING init — files in the way (nothing was changed):"

	// Per-path placement reporting.
	msgSkipTracked         = "omakase: SKIP (already tracked) %s\n"
	msgSkipToggledOff      = "omakase: SKIP (toggled off) %s — re-enable: omakase status --enable %s\n"
	msgKeptRestored        = "omakase: restored your kept version of %s (it was missing)\n"
	msgSkipKept            = "omakase: SKIP (kept — yours) %s — see the difference: omakase diff %s; harness version back: omakase status --restore %s\n"
	msgUpdatedToPayload    = "omakase: updated %s to match the payload\n"
	msgStaleEditedLeft     = "omakase: WARNING — '%s' was placed by a prior init, is no longer in the payload, and differs from what init placed (a local edit?). Leaving it; delete it yourself if unwanted.\n"
	msgWtincTracked        = "omakase: .worktreeinclude is tracked — leaving it untouched (re-run omakase init inside a new manual worktree to install it there)."
	msgOverlayDirInWay     = "omakase: refusing to overlay file '%s' — an untracked directory exists there; remove it and re-run\n"
	msgSymlinkUnreadable   = "omakase: could not read payload symlink '%s'. Nothing was changed.\n"
	msgSymlinkEscapesRepo  = "omakase: refusing to install — payload symlink '%s' (-> %s) points outside the repo; a placed symlink must stay inside the repo it lands in. Nothing was changed.\n"
	msgClearedHooksPath    = "omakase: cleared redundant core.hooksPath (it named the repo's own hooks dir; the effective hooks dir is unchanged)."
	msgHookWriteFailed     = "omakase: could not write the %s hook: %v\n"
	msgDisplacedLFSHook    = "omakase: displaced the stock git-lfs %s hook — the omakase hook runs 'git lfs %s' itself, so LFS keeps working.\n"
	msgStableBinaryMissing = "omakase: WARNING — the hooks run %s, which is missing or not executable; commits will be blocked until it exists. Re-run 'omakase init' with any installed omakase binary to restore it.\n"
	msgDowngradeRefused    = "omakase: refusing to roll this repo's omakase files BACK from %s to %s — a newer omakase set this repo up, and this init came from an older install (usually a stale plugin or binary). Update it (brew upgrade omakase, or update the plugin), then re-run. To go back to %s on purpose:  omakase remove  then  %s. Nothing was changed.\n"

	// The closing summary: the counts line, then one row per touched path.
	msgPlacedSummary       = "omakase: placed %d file(s), updated %d to match the payload, skipped %d committed path(s).\n"
	msgRowUpdated          = "  ^ updated to match the payload: %s\n"
	msgRowKeptRestored     = "  = kept (yours — was missing, your accepted version restored): %s\n"
	msgRowKeptUntouched    = "  = kept (yours — left untouched): %s\n"
	msgRowRemovedStale     = "  - removed (placed by a prior init, no longer in the payload): %s\n"
	msgRowSkippedCommitted = "  ~ skipped (committed — re-run with --cut-over to let the harness copy take over; guarded, see init.sh --help): %s\n"

	msgIgnoresWiredWorktrees = "omakase: ignores -> .git/info/exclude; new worktrees auto-install the harness. Nothing to commit."
	msgIgnoresWired          = "omakase: ignores -> .git/info/exclude. Nothing to commit."
	msgNoGatesDeclared       = "omakase: no gates declared — no enforcement hooks installed."
	msgHooksLeftUntouched    = `omakase: existing git hooks left untouched; without the heal hook, run a bare
         'omakase init' after a checkout or in a new worktree to refresh the files.
`
	msgSeeStatus     = "omakase: see the whole harness any time with  omakase status"
	msgRecommends    = "omakase: this harness recommends — %s\n"
	msgCustomizeHint = `omakase: to customize, edit an injected file in place (omakase diff shows the change;
         keep or undo it via omakase status) — or fork the harness source and init from your copy.
`
)

// source — resolving, caching, and merging the harness source payload.
const (
	msgBasePayloadMissing        = "omakase: base payload not found at %s — point OMAKASE_BASE_PAYLOAD at a real payload tree, or unset it to use the binary's embedded copy\n"
	msgMergeTmpFailed            = "omakase: could not create a temp dir to merge the base + source payload"
	msgMergeCopyBaseFailed       = "omakase: failed to copy the base payload (%s) into the merge staging dir\n"
	msgMergeSymlinkShadowRefused = "omakase: source ships '%s' as a symlink where the base payload has a directory — refusing to shadow the base files under it. Nothing was changed.\n"
	msgMergeOverlayFailed        = "omakase: failed to overlay source payload file '%s' onto the base payload\n"
	msgCacheRefreshFailed        = "omakase: could not refresh source cache at %s — reusing the cached copy (offline?)\n"
	msgCacheCorruptRecloning     = "omakase: source cache at %s is stale or corrupt — discarding and re-cloning (a cache is disposable)\n"
	msgCloneFailed               = "omakase: could not clone source '%s' into the cache (%s)\n"
	msgNoSuchRef                 = "omakase: source '%s' has no ref '%s' (no such branch or tag)\n"
	msgNoSuchSubdir              = "omakase: source '%s' has no directory '%s' — nothing to adopt\n"
	msgRootManifestRefused       = "omakase: source '%s' has a root omakase.manifest, which omakase no longer reads — omakase reads one manifest: payload/omakase.manifest. Move name:/version:/recommends: there and delete the root file. Nothing was changed.\n"
	msgNoPayloadTree             = "omakase: source '%s' has no non-empty payload/ tree — nothing to inject\n"
	msgNoManifest                = "omakase: source '%s' has no payload/omakase.manifest — not an omakase source\n"
	msgManifestNoName            = "omakase: source '%s' manifest is missing the required 'name:' line\n"
	msgSourceCached              = "omakase: source '%s' (name: %s%s%s) cached at %s\n"
)

// record — the out-of-band PASS for a deferred gate.
const (
	msgRecordUsage  = "usage: omakase record <gate-name>"
	msgRecordFailed = "omakase: FAILED to record a PASS for '%s' (%v)\n"
	msgRecordedPass = "omakase: recorded PASS for '%s' at HEAD\n"
)

// remove — the reverse of init.
const (
	msgRemoveRestoredLFSHook  = "omakase: restored the git-lfs %s hook the install had displaced.\n"
	msgRemoveForeignHook      = "omakase: NOTE — %s is not omakase's dispatcher (another tool wrote it); left in place.\n"
	msgRemoveNothingInstalled = "omakase: nothing installed here; nothing to remove."
	msgRemoveWorktreeSkipped  = "omakase: worktree %s is unreachable; its placed files were not removed.\n"
	msgRemoveKeptSurvives     = "omakase: %s is yours (kept) — left on disk; with the ignore rules gone, git now sees it as an untracked file.\n"
	msgRemoveDone             = "omakase: removed. Hooks uninstalled, placed files deleted, worktree snapshot + exclude block stripped."
)

// diff — read-only "what did I change vs the harness".
const (
	msgDiffUnknownFlag      = "omakase: unknown flag %s (omakase diff is read-only and takes only paths; see omakase diff --help)\n"
	msgDiffNothingInstalled = "omakase: no harness installed here — nothing to diff (install one:  omakase init)"
	msgDiffUnknownPath      = "omakase: unknown placed path: %s\n"
	msgDiffRowMissing       = "%s — missing from this worktree (restored on the next checkout, or:  omakase init)\n"
	msgDiffRowHeader        = "%s — your changes vs %s:\n"
	msgDiffRenderFailed     = "omakase: could not diff %s: %v\n"
	msgDiffNoChanges        = "omakase: no changes — every placed file matches what you've consented to."
	msgDiffYoursLabel       = "+++ %s  (yours, on disk)\n"
	msgDiffBinary           = "Binary files differ"

	// The two baselines a changed file is compared against, spliced into
	// msgDiffRowHeader and the --- label.
	vsHarnessVersion = "the harness version"
	vsKeptVersion    = "your accepted (kept) version"

	msgDiffUsage = `usage: omakase diff [path…]

Shows what you changed in the files omakase placed, vs the harness version
(or vs the version you accepted with  omakase status --keep). Read-only.

  (no paths)   every changed placed file
  path…        a placed file, or a directory of placed files

After reviewing:  omakase status --keep <path>     make your version the accepted one
                  omakase status --restore <path>  put the harness's version back
`
)
