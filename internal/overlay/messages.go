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
	// Any verb run outside a git repository.
	msgNotAGitRepo = "omakase: not inside a git repo"
)

// hook — what the .git/hooks dispatchers exec. Gate hooks block fail-closed;
// heal (post-checkout / session-start) warns and never fails the checkout.
const (
	// `omakase hook` called without a known hook name.
	msgHookUsage = "usage: omakase hook <pre-commit|pre-push|post-checkout|session-start> [hook args...]"

	// A gate hook fired but no repo can be discovered from cwd.
	msgHookNoRepoBlock = "omakase: BLOCKING — %s: not inside a git repository; the harness cannot be verified.\n"
	// The dispatcher exists but $OMK state is gone (wiped without `omakase remove`).
	msgHookTornStateBlock = "omakase: BLOCKING — %s: omakase hooks are installed but no harness state exists in this repo.\n"
	// Second line of the torn-state block: the two ways out.
	msgHookTornStateFix = "omakase: restore it with  omakase init  — or take the hooks out with  omakase remove."

	// A session opened on a wiped overlay and heal put files back (#164 C5).
	msgSessionStartRestored = "omakase: restored %d missing harness file(s) at session start — run `omakase status` to review.\n"

	// A commit/push fired while enabled placed files are missing (gates would silently not run).
	msgVerifyIncompleteBlock = "omakase: BLOCKING — the injected harness is incomplete; its gates would silently not run:"
	// One row per missing file under the block above.
	msgVerifyMissingRow = "  missing: %s\n"
	// Closing line of the incomplete-harness block.
	msgVerifyIncompleteFix = "omakase: restore it with  omakase init  and retry."

	// Heal found an injected path the repo now commits (upstream likely clobbered the personal copy).
	msgHealTrackedCollision = "omakase: WARNING — injected path '%s' is now TRACKED by the repo; your personal copy was likely clobbered by an upstream commit (git overwrites ignored files on checkout). Last-injected copy: %s — diff it against the tracked file, then drop the path from your payload or cut over (init --cut-over).\n"
	// Heal found a kept (accepted) file edited since it was accepted.
	msgHealKeptDrift = "omakase: WARNING — '%s' differs from your accepted (kept) version. Your copy is left as-is. See the change:  omakase diff %s  — then keep it (omakase status --keep %s) or go back (omakase status --restore %s).\n"
	// Heal found an injected file that differs from canonical; a gate may be weakened.
	msgHealDrift = "omakase: WARNING — injected '%s' has DRIFTED from canonical (ledger %s…, on-disk %s…); a gate may be weakened or stale. Drift only surfaces — your copy is left as-is. Adopt canonical with: %s\n"
	// The %s fix in msgHealDrift when no snapshot copy exists to cp from.
	msgHealDriftFixInit = "omakase init"
	// The %s fix in msgHealDrift when the snapshot copy exists.
	msgHealDriftFixCp = "cp -P '%s' '%s'  (or omakase init to re-sync every file)"
	// A crafted internal error (already a sentence) relayed under the omakase: prefix.
	msgPrefixedErr = "omakase: %v\n"
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
	// --source given without a value.
	msgSourceNeedsValue = "omakase: --source needs a git URL or local path"
	// An option init does not know.
	msgUnknownOption = "omakase: unknown option '%s'\n"
	// A second positional argument after the source was already set.
	msgExtraArgument = "omakase: unexpected extra argument '%s' (source already set)\n"
	// The source string would corrupt the tab-separated state files.
	msgSourceBadChars = "omakase: --source must not contain a tab or newline"
	// "//sub" given with no repo part before the marker.
	msgSourceMissingRepoPart = "omakase: source '//%s' is missing the repo part before the '//' subpath marker\n"
	// A subpath that is absolute or climbs out of the source repo with "..".
	msgSourceSubpathEscapes = "omakase: source subpath '%s' must stay inside the source repo (relative, no '..')\n"

	// Switching the harness from a linked worktree — refused; hooks/ignores/
	// source are shared by every checkout.
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

	// The base payload embedded in the binary could not be written to disk.
	msgBaseMaterializeFailed = "omakase: cannot materialize the base harness payload: %v\n"
	// OMAKASE_PAYLOAD points at a directory that does not exist.
	msgPayloadDirMissing = "omakase: payload dir not found at %s\n"
	// A pre-v0.20 harness still declaring gates the removed lefthook way.
	msgLefthookYmlRefused = "omakase: this harness declares gates in lefthook-local.yml, which omakase no longer reads. Declare them as gate: blocks in omakase.manifest (see the README) and delete the yml. Nothing was changed."
	// omakase.manifest exists but could not be read.
	msgManifestReadFailed = "omakase: could not read %s: %v. Nothing was changed.\n"
	// A malformed gate: block in the manifest.
	msgGateDeclInvalid = "omakase: invalid gate declaration in omakase.manifest: %v. Nothing was changed.\n"
	// A gate references a payload script that is missing or not executable.
	// The %v names the gate and the script. Two causes share this refusal,
	// and the likelier one for an adopter is NOT the wiring: an omakase older
	// than the harness expects merges a base payload that lacks the script
	// (#49's mis-blame incident — the message must not send the user off to
	// edit wiring that is correct).
	msgGateWiringInvalid = `omakase: %v. It would fail at commit time (exit 127). Nothing was changed.
         If you didn't edit this harness yourself, your omakase install is likely older
         than the harness expects — update it (brew upgrade omakase, or update the
         plugin), then re-run. Otherwise fix the run: reference or ship the script.
`

	// An incumbent hook manager (husky and kin) owns .git/hooks — refused;
	// the header, then one "  - path" row per finding, then the consequences.
	msgIncumbentRefusalHeader = "omakase: REFUSING to install — an incumbent hook manager is present:"
	msgIncumbentRefusalBody   = `  Installing omakase's hooks would displace the project's own, silently disabling
  its gates — and a husky prepare script would overwrite them back on the next
  npm install. omakase does not chain hook managers (v1).
  If these are stale leftovers, remove them and re-run. If the project really uses
  them, do not install omakase here. Nothing was changed.
`

	// --cut-over with no payload path tracked — nothing to untrack.
	msgCutOverNothingTracked = "omakase: --cut-over: no payload path is tracked by this repo — nothing to cut over."
	// The untrack-list header; one "    path" row per file follows.
	msgCutOverPlan = "omakase: cut-over will run  git rm --cached  on %d tracked file(s):\n"
	// The consequences, printed before the confirm gate on every cut-over run.
	msgCutOverConsequences = `  This STAGES A DELETION of each shared file. The next commit — including an agent
  auto-commit — applies that deletion FOR EVERYONE who pulls it, and upstream changes
  to these files will then produce modify/delete conflicts. The files stay on disk;
  the injected (gitignored) copies take over locally. Undo before committing with
  'git restore --staged <file>'; 'git add <file>' re-tracks later.
`
	// OMAKASE_CUTOVER_CONFIRM=1 was not set.
	msgCutOverUnconfirmed = "omakase: REFUSING cut-over without confirmation. Re-run with OMAKASE_CUTOVER_CONFIRM=1 to proceed. Nothing was changed."
	// The receipt after git rm --cached succeeded.
	msgCutOverStaged = "omakase: cut-over staged %d deletion(s) — review with 'git status' and commit them yourself.\n"

	// A path a prior init injected is now committed by the repo (upstream
	// landed a file there; the personal copy was likely clobbered).
	msgInitTrackedCollision = `omakase: WARNING — '%s' was injected (personal, gitignored) but is NOW TRACKED by the repo.
  An upstream commit likely landed a file at this path; git silently overwrites ignored
  files on checkout/pull, so your personal copy was likely clobbered. The harness's own
  version still lives in your harness source. Reconcile: drop '%s' from your payload,
  or run init --cut-over (guarded) to untrack the file and let the injected copy take over.
`

	// The placement pre-scan found the user's own untracked files at payload
	// paths — refused; one path per row follows.
	msgFilesInTheWayHeader = "omakase: REFUSING init — files in the way (nothing was changed):"

	// The repo commits its own file at this payload path — never touched.
	msgSkipTracked = "omakase: SKIP (already tracked) %s\n"
	// A row the user toggled off stays un-placed across re-init.
	msgSkipToggledOff = "omakase: SKIP (toggled off) %s — re-enable: omakase status --enable %s\n"
	// A kept (accepted) file was missing; the accepted copy is restored.
	msgKeptRestored = "omakase: restored your kept version of %s (it was missing)\n"
	// A kept file is present; the harness version does not overwrite it.
	msgSkipKept = "omakase: SKIP (kept — yours) %s — see the difference: omakase diff %s; harness version back: omakase status --restore %s\n"
	// An unmodified placed file caught up with a newer payload version.
	msgUpdatedToPayload = "omakase: updated %s to match the payload\n"
	// A file from a prior init left the payload but carries a local edit — left in place.
	msgStaleEditedLeft = "omakase: WARNING — '%s' was placed by a prior init, is no longer in the payload, and differs from what init placed (a local edit?). Leaving it; delete it yourself if unwanted.\n"
	// .worktreeinclude is the repo's own tracked file; init won't rewrite it.
	msgWtincTracked = "omakase: .worktreeinclude is tracked — leaving it untouched (re-run omakase init inside a new manual worktree to install it there)."
	// An untracked directory sits where a payload file must land.
	msgOverlayDirInWay = "omakase: refusing to overlay file '%s' — an untracked directory exists there; remove it and re-run\n"
	// A payload symlink whose target could not be read.
	msgSymlinkUnreadable = "omakase: could not read payload symlink '%s'. Nothing was changed.\n"
	// A payload symlink pointing outside the repo — refused (planted-symlink hardening).
	msgSymlinkEscapesRepo = "omakase: refusing to install — payload symlink '%s' (-> %s) points outside the repo; a placed symlink must stay inside the repo it lands in. Nothing was changed.\n"
	// init cleared a core.hooksPath that named the default hooks dir anyway.
	msgClearedHooksPath = "omakase: cleared redundant core.hooksPath (it named the repo's own hooks dir; the effective hooks dir is unchanged)."
	// Writing a .git/hooks dispatcher failed.
	msgHookWriteFailed = "omakase: could not write the %s hook: %v\n"
	// A stock git-lfs stub was displaced by our dispatcher; LFS is still forwarded.
	msgDisplacedLFSHook = "omakase: displaced the stock git-lfs %s hook — the omakase hook runs 'git lfs %s' itself, so LFS keeps working.\n"
	// The binary the dispatchers exec is missing or not executable — commits would block.
	msgStableBinaryMissing = "omakase: WARNING — the hooks run %s, which is missing or not executable; commits will be blocked until it exists. Re-run 'omakase init' with any installed omakase binary to restore it.\n"
	// An older omakase tried to re-init a repo a newer one set up — refused.
	msgDowngradeRefused = "omakase: refusing to roll this repo's omakase files BACK from %s to %s — a newer omakase set this repo up, and this init came from an older install (usually a stale plugin or binary). Update it (brew upgrade omakase, or update the plugin), then re-run. To go back to %s on purpose:  omakase remove  then  %s. Nothing was changed.\n"

	// The counts line every successful init ends with; rows follow.
	msgPlacedSummary = "omakase: placed %d file(s), updated %d to match the payload, skipped %d committed path(s).\n"
	// Row: a freshly placed file is "  + path" (no letters, inline); an updated file:
	msgRowUpdated = "  ^ updated to match the payload: %s\n"
	// Row: a kept file that was missing and came back as the accepted copy.
	msgRowKeptRestored = "  = kept (yours — was missing, your accepted version restored): %s\n"
	// Row: a kept file left exactly as it was.
	msgRowKeptUntouched = "  = kept (yours — left untouched): %s\n"
	// Row: a prior init's file no longer in the payload, removed (unedited).
	msgRowRemovedStale = "  - removed (placed by a prior init, no longer in the payload): %s\n"
	// Row: a payload path the repo commits — skipped.
	msgRowSkippedCommitted = "  ~ skipped (committed — re-run with --cut-over to let the harness copy take over; guarded, see init.sh --help): %s\n"

	// Closing note when the heal (post-checkout) hook was installed.
	msgIgnoresWiredWorktrees = "omakase: ignores -> .git/info/exclude; new worktrees auto-install the harness. Nothing to commit."
	// Closing note when it was not.
	msgIgnoresWired = "omakase: ignores -> .git/info/exclude. Nothing to commit."
	// A steering-only harness: nothing wired into .git/hooks (#149).
	msgNoGatesDeclared = "omakase: no gates declared — no enforcement hooks installed."
	// No gates AND no heal hook: how to refresh placed files by hand.
	msgHooksLeftUntouched = `omakase: existing git hooks left untouched; without the heal hook, run a bare
         'omakase init' after a checkout or in a new worktree to refresh the files.
`
	// The closing pointer, printed on every successful init.
	msgSeeStatus = "omakase: see the whole harness any time with  omakase status"
	// The source manifest's recommends: line, surfaced once at the end.
	msgRecommends = "omakase: this harness recommends — %s\n"
	// The closing customize pointer.
	msgCustomizeHint = `omakase: to customize, edit an injected file in place (omakase diff shows the change;
         keep or undo it via omakase status) — or fork the harness source and init from your copy.
`
)

// source — resolving, caching, and merging the harness source payload.
const (
	// OMAKASE_BASE_PAYLOAD points at nothing (a bad handoff from the shims).
	msgBasePayloadMissing = "omakase: base payload not found at %s — point OMAKASE_BASE_PAYLOAD at a real payload tree, or unset it to use the binary's embedded copy\n"
	// MkdirTemp failed while staging the base+source merge.
	msgMergeTmpFailed = "omakase: could not create a temp dir to merge the base + source payload"
	// Copying the base payload into the staging dir failed.
	msgMergeCopyBaseFailed = "omakase: failed to copy the base payload (%s) into the merge staging dir\n"
	// A source symlink would shadow a base directory's files — refused.
	msgMergeSymlinkShadowRefused = "omakase: source ships '%s' as a symlink where the base payload has a directory — refusing to shadow the base files under it. Nothing was changed.\n"
	// Copying one source file over the base failed.
	msgMergeOverlayFailed = "omakase: failed to overlay source payload file '%s' onto the base payload\n"
	// git fetch on the cache failed; the run continues with the cached copy.
	msgCacheRefreshFailed = "omakase: could not refresh source cache at %s — reusing the cached copy (offline?)\n"
	// The cache dir is not a usable clone; rebuilt from scratch.
	msgCacheCorruptRecloning = "omakase: source cache at %s is stale or corrupt — discarding and re-cloning (a cache is disposable)\n"
	// A fresh clone of the source failed (bad URL, no network, no access).
	msgCloneFailed = "omakase: could not clone source '%s' into the cache (%s)\n"
	// The #ref pin names a branch/tag the source does not have.
	msgNoSuchRef = "omakase: source '%s' has no ref '%s' (no such branch or tag)\n"
	// The //subpath names a directory the source does not have.
	msgNoSuchSubdir = "omakase: source '%s' has no directory '%s' — nothing to adopt\n"
	// A source still using the removed root-manifest layout.
	msgRootManifestRefused = "omakase: source '%s' has a root omakase.manifest, which omakase no longer reads — omakase reads one manifest: payload/omakase.manifest. Move name:/version:/recommends: there and delete the root file. Nothing was changed.\n"
	// The source has no payload/ tree to inject.
	msgNoPayloadTree = "omakase: source '%s' has no non-empty payload/ tree — nothing to inject\n"
	// payload/ exists but carries no manifest — not a harness.
	msgNoManifest = "omakase: source '%s' has no payload/omakase.manifest — not an omakase source\n"
	// The manifest lacks its one required key.
	msgManifestNoName = "omakase: source '%s' manifest is missing the required 'name:' line\n"
	// The successful fetch receipt, before placement begins.
	msgSourceCached = "omakase: source '%s' (name: %s%s%s) cached at %s\n"
)

// record — the out-of-band PASS for a deferred gate.
const (
	// `omakase record` called without exactly one gate name.
	msgRecordUsage = "usage: omakase record <gate-name>"
	// The PASS row could not be written — loud, it is the only signal the check ran.
	msgRecordFailed = "omakase: FAILED to record a PASS for '%s' (%v)\n"
	// The receipt that an out-of-band check passed at this HEAD.
	msgRecordedPass = "omakase: recorded PASS for '%s' at HEAD\n"
)

// remove — the reverse of init.
const (
	// The saved git-lfs stub went back after our dispatcher was deleted.
	msgRemoveRestoredLFSHook = "omakase: restored the git-lfs %s hook the install had displaced.\n"
	// A hook at our name that another tool wrote — reported, never deleted.
	msgRemoveForeignHook = "omakase: NOTE — %s is not omakase's dispatcher (another tool wrote it); left in place.\n"
	// remove in a repo with no evidence of an install.
	msgRemoveNothingInstalled = "omakase: nothing installed here; nothing to remove."
	// A worktree git lists is unreachable; its placed copies stay orphaned.
	msgRemoveWorktreeSkipped = "omakase: worktree %s is unreachable; its placed files were not removed.\n"
	// A kept file outlives remove — it is the user's content, never deleted.
	msgRemoveKeptSurvives = "omakase: %s is yours (kept) — left on disk; with the ignore rules gone, git now sees it as an untracked file.\n"
	// The closing receipt.
	msgRemoveDone = "omakase: removed. Hooks uninstalled, placed files deleted, worktree snapshot + exclude block stripped."
)

// diff — read-only "what did I change vs the harness".
const (
	// diff takes no flags except --help.
	msgDiffUnknownFlag = "omakase: unknown flag %s (omakase diff is read-only and takes only paths; see omakase diff --help)\n"
	// diff with no harness installed.
	msgDiffNothingInstalled = "omakase: no harness installed here — nothing to diff (install one:  omakase init)"
	// A path argument that matches no placed file or group.
	msgDiffUnknownPath = "omakase: unknown placed path: %s\n"
	// A placed file absent from this worktree.
	msgDiffRowMissing = "%s — missing from this worktree (restored on the next checkout, or:  omakase init)\n"
	// The per-file header above each rendered diff.
	msgDiffRowHeader = "%s — your changes vs %s:\n"
	// git diff --no-index failed to render (anything but its exit 1).
	msgDiffRenderFailed = "omakase: could not diff %s: %v\n"
	// Every enabled placed file matches its baseline.
	msgDiffNoChanges = "omakase: no changes — every placed file matches what you've consented to."
	// The +++ label, relabeled in the user's vocabulary.
	msgDiffYoursLabel = "+++ %s  (yours, on disk)\n"
	// The binary-file notice, stripped of the internal operand paths.
	msgDiffBinary = "Binary files differ"

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
