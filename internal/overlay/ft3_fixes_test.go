package overlay

// ft3_fixes_test.go holds the FT3 adversarial-review fix wave: Fix D (false
// `^ overrides` narration when a committed file actually wins), Fix E (bare-init
// heal must not strip the survivor's stored CLAUDE.md bridge), Fix F (--cut-over
// must untrack a committed AGENTS.md the payload also ships and land it at the
// root slot, matching legacy), and Fix G (`remove <source>` must refuse before
// mutation when it would strand the survivor's hook wiring). New test functions
// only — the shared helpers live in init_test.go / source_test.go /
// init_layers_test.go / remove_test.go, same package.

import (
	"io"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Yuncun/omakase-harness/internal/state"
)

// ---------------------------------------------------------------- Fix D (#4)

// TestStackOverrideNarrationExcludesCommittedSkip pins Fix D: a path A placed
// that the repo SINCE committed is skipped by init B's place loop (committed file
// wins) — so init B must NOT narrate it as `^ overrides` even though B ships it
// and would win it in the merged view. Narration is the sole precedence carrier;
// claiming B's copy is in effect when the committed file actually wins is a false
// statement. The `~ skipped (committed…)` line fires for the same path in the
// same run.
func TestStackOverrideNarrationExcludesCommittedSkip(t *testing.T) {
	dir, repo := initRepo(t)
	srcTestEnv(t)
	stubLefthook(t)
	base := useBasePayloadDir(t)
	writeFile(t, filepath.Join(base, ".omakase", "bin", "base.sh"), "base\n")

	srcA := newHarnessSource(t, "a", map[string]string{".omakase/gates/shared.sh": "A\n"})
	srcB := newHarnessSource(t, "b", map[string]string{".omakase/gates/shared.sh": "B\n"})

	// init A places shared.sh (untracked, gitignored).
	if code := RunInit([]string{"--source", srcA}, io.Discard, io.Discard); code != 0 {
		t.Fatal("init A failed")
	}
	// The repo SINCE commits shared.sh (force-add: init excluded .omakase/).
	runGitT(t, dir, "add", "-f", ".omakase/gates/shared.sh")
	runGitT(t, dir, "commit", "-q", "-m", "team commits shared.sh")

	// init B ships shared.sh too. B would win it in the merged view, but the
	// committed file wins on disk — the place loop SKIPs it.
	var stdout, stderr strings.Builder
	if code := RunInit([]string{"--source", srcB}, &stdout, &stderr); code != 0 {
		t.Fatalf("init B exit = %d; stderr=%q", code, stderr.String())
	}
	out := stdout.String()

	// The committed-skip line fires for shared.sh...
	wantSkip := "  ~ skipped (committed — re-run with --cut-over to let the harness copy take over; guarded, see init.sh --help): .omakase/gates/shared.sh\n"
	if !strings.Contains(out, wantSkip) {
		t.Errorf("expected the committed-skip line for shared.sh:\n%s", out)
	}
	// ...and NO override line falsely claims B's copy is in effect.
	if strings.Contains(out, "^ overrides "+srcA+": .omakase/gates/shared.sh") {
		t.Errorf("false `^ overrides` narration for a committed-and-skipped path:\n%s", out)
	}
	// The live file keeps the committed bytes (A\n), never B\n.
	eq(t, "committed shared.sh untouched", readFileT(t, filepath.Join(dir, ".omakase", "gates", "shared.sh")), "A\n")
	// The rebuilt placed.tsv has no shared.sh row (skipped, not placed).
	for _, r := range state.ReadPlaced(filepath.Join(repo.OMK, "placed.tsv")) {
		if r.Rel == ".omakase/gates/shared.sh" {
			t.Errorf("placed.tsv has a shared.sh row despite the committed skip: %+v", r)
		}
	}
	if o := gitStdout(dir, "status", "--porcelain"); o != "" {
		t.Errorf("git status not clean: %q", o)
	}
}

// ---------------------------------------------------------------- Fix E (#5)

// readlinkT reads the symlink at path, failing the test with a labelled message
// if it is absent or not a symlink.
func assertStoreBridge(t *testing.T, omk, when string) {
	t.Helper()
	tgt, err := os.Readlink(filepath.Join(omk, "layers", "1", "files", "CLAUDE.md"))
	if err != nil || tgt != "AGENTS.md" {
		t.Fatalf("%s: layers/1 STORE bridge = %q err=%v, want a CLAUDE.md -> AGENTS.md symlink", when, tgt, err)
	}
}

// TestBareInitKeepsStoredBridgeUnderCLAUDEtop pins Fix E for the bare-init heal
// path: srcA ships a root AGENTS.md (bottom, gets the §7 bridge); srcCl stacks an
// explicit CLAUDE.md on top (top wins the live CLAUDE.md). A routine bare init
// REBUILDS layers/1 from A's own payload — the STORED bottom-layer bridge must be
// derived single-layer (A alone wants it), NOT suppressed by the top layer's
// CLAUDE.md. Otherwise remove Cl later deletes the live CLAUDE.md with no bridge
// to restore, silently dropping Claude Code's root instructions (GC7(a) broken).
func TestBareInitKeepsStoredBridgeUnderCLAUDEtop(t *testing.T) {
	srcTestEnv(t)
	stubLefthook(t)
	useBasePayloadDir(t)

	srcA := newHarnessSource(t, "a", map[string]string{
		"AGENTS.md":           "A doctrine\n",
		".omakase/gates/a.sh": "a gate\n",
	})
	srcCl := newHarnessSource(t, "cl", map[string]string{
		"CLAUDE.md":           "explicit claude\n",
		".omakase/gates/c.sh": "c gate\n",
	})

	dir, repo := initRepo(t)
	if code := RunInit([]string{"--source", srcA}, io.Discard, io.Discard); code != 0 {
		t.Fatal("init A failed")
	}
	assertStoreBridge(t, repo.OMK, "after init A")

	if code := RunInit([]string{"--source", srcCl}, io.Discard, io.Discard); code != 0 {
		t.Fatal("init Cl failed")
	}
	assertStoreBridge(t, repo.OMK, "after init Cl (layers/1 reused)")
	eq(t, "live CLAUDE.md = Cl's explicit (top wins)", readFileT(t, filepath.Join(dir, "CLAUDE.md")), "explicit claude\n")

	// The heal: rebuilds layers/1 while the top layer ships CLAUDE.md.
	if code := RunInit(nil, io.Discard, io.Discard); code != 0 {
		t.Fatal("bare init (heal) failed")
	}
	assertStoreBridge(t, repo.OMK, "after bare init heal (THE Fix-E assertion)")
	eq(t, "live CLAUDE.md still Cl's after heal", readFileT(t, filepath.Join(dir, "CLAUDE.md")), "explicit claude\n")

	// remove Cl: survivor must equal a fresh init A alone — root AGENTS.md + bridge.
	if code := RunRemove([]string{srcCl}, io.Discard, io.Discard); code != 0 {
		t.Fatal("remove Cl failed")
	}
	if tgt, err := os.Readlink(filepath.Join(dir, "CLAUDE.md")); err != nil || tgt != "AGENTS.md" {
		t.Fatalf("live CLAUDE.md bridge missing after remove Cl: tgt=%q err=%v", tgt, err)
	}
	eq(t, "root AGENTS.md survives remove Cl", readFileT(t, filepath.Join(dir, "AGENTS.md")), "A doctrine\n")

	// twin: only ever installed A.
	dirB, repoB := initRepo(t)
	if code := RunInit([]string{"--source", srcA}, io.Discard, io.Discard); code != 0 {
		t.Fatal("fresh init A failed")
	}
	assertRemoveTwinEqual(t, repo, repoB, dir, dirB)
}

// TestBottomRepairKeepsStoredBridgeUnderCLAUDEtop pins the same Fix E invariant
// for the OTHER rebuild trigger: re-init of the bottom source (A) in the A+Cl
// stack repairs layers/1 in place. The stored bridge must survive that repair
// too — same single-layer derivation, no bare init needed to expose the bug.
func TestBottomRepairKeepsStoredBridgeUnderCLAUDEtop(t *testing.T) {
	srcTestEnv(t)
	stubLefthook(t)
	useBasePayloadDir(t)

	srcA := newHarnessSource(t, "a", map[string]string{
		"AGENTS.md":           "A doctrine\n",
		".omakase/gates/a.sh": "a gate\n",
	})
	srcCl := newHarnessSource(t, "cl", map[string]string{
		"CLAUDE.md":           "explicit claude\n",
		".omakase/gates/c.sh": "c gate\n",
	})

	_, repo := initRepo(t)
	if code := RunInit([]string{"--source", srcA}, io.Discard, io.Discard); code != 0 {
		t.Fatal("init A failed")
	}
	if code := RunInit([]string{"--source", srcCl}, io.Discard, io.Discard); code != 0 {
		t.Fatal("init Cl failed")
	}
	// Repair the bottom (A) in place — no reorder, layers/1 rebuilt fresh.
	if code := RunInit([]string{"--source", srcA}, io.Discard, io.Discard); code != 0 {
		t.Fatal("re-init A (bottom repair) failed")
	}
	assertStoreBridge(t, repo.OMK, "after bottom repair (init A)")
}

// ---------------------------------------------------------------- Fix F (#6)

// TestCutOverUntracksCommittedAGENTS pins Fix F: a repo COMMITS its own AGENTS.md
// and installs a source that ALSO ships AGENTS.md, with --cut-over. The cut must
// untrack the committed AGENTS.md (the canonical path the payload ships, not the
// CLAUDE.local.md its slot-fallback would reroute to), which frees the root slot
// so the payload's AGENTS.md lands at the ROOT — matching legacy
// (bin/legacy/init.sh --cut-over). Before the fix the committed AGENTS.md was
// already rerouted to CLAUDE.local.md before the cut list was built, so it was
// never cut, the payload's doctrine landed at CLAUDE.local.md, and the root slot
// stayed taken forever.
func TestCutOverUntracksCommittedAGENTS(t *testing.T) {
	dir, repo := initRepo(t)
	srcTestEnv(t)
	stubLefthook(t)
	useBasePayloadDir(t)
	t.Setenv("OMAKASE_CUTOVER_CONFIRM", "1")

	// The repo commits its own AGENTS.md.
	writeFile(t, filepath.Join(dir, "AGENTS.md"), "TEAM AGENTS\n")
	runGitT(t, dir, "add", "AGENTS.md")
	runGitT(t, dir, "commit", "-q", "-m", "team AGENTS.md")

	src := newHarnessSource(t, "s", map[string]string{
		"AGENTS.md":           "S doctrine\n",
		".omakase/gates/s.sh": "s gate\n",
	})

	var stdout, stderr strings.Builder
	if code := RunInit([]string{"--source", src, "--cut-over"}, &stdout, &stderr); code != 0 {
		t.Fatalf("init --cut-over exit = %d; stderr=%q", code, stderr.String())
	}

	// The committed AGENTS.md is untracked (git rm --cached staged its deletion).
	if gitTracked(dir, "AGENTS.md") {
		t.Error("AGENTS.md is still tracked — --cut-over did not untrack it")
	}
	// The payload's AGENTS.md landed at the ROOT (not rerouted to CLAUDE.local.md).
	eq(t, "payload AGENTS.md at root", readFileT(t, filepath.Join(dir, "AGENTS.md")), "S doctrine\n")
	if _, err := os.Stat(filepath.Join(dir, "CLAUDE.local.md")); err == nil {
		t.Error("CLAUDE.local.md exists — the payload AGENTS.md should own the freed root slot, not reroute")
	}
	// The prior committed copy is preserved under $OMK/clobbered/ (init's overwrite
	// discipline), matching legacy's clobbered/ backup.
	eq(t, "prior committed copy backed up", readFileT(t, filepath.Join(repo.OMK, "clobbered", "AGENTS.md")), "TEAM AGENTS\n")
	// The cut-over narration named AGENTS.md and staged the deletion.
	out := stdout.String()
	if !strings.Contains(out, "    AGENTS.md\n") || !strings.Contains(out, "cut-over staged 1 deletion") {
		t.Errorf("cut-over narration did not report untracking AGENTS.md:\n%s", out)
	}
	// The index shows exactly the staged AGENTS.md deletion (user commits it).
	if staged := gitStdout(dir, "diff", "--cached", "--name-status"); !strings.Contains(staged, "D\tAGENTS.md") {
		t.Errorf("staged index does not show AGENTS.md deletion: %q", staged)
	}
	// placed.tsv records AGENTS.md at the root (col1), sourced from S.
	var sawRoot bool
	for _, r := range state.ReadPlaced(filepath.Join(repo.OMK, "placed.tsv")) {
		if r.Rel == "AGENTS.md" {
			sawRoot = true
			if r.Src != src {
				t.Errorf("AGENTS.md row src = %q, want %q", r.Src, src)
			}
		}
		if r.Rel == "CLAUDE.local.md" {
			t.Error("placed.tsv has a CLAUDE.local.md row — AGENTS.md should own the root slot")
		}
	}
	if !sawRoot {
		t.Error("placed.tsv has no root AGENTS.md row")
	}
}

// ---------------------------------------------------------------- Fix G (#11)

// TestRemoveRefusesWhenItStrandsSurvivorWiring pins Fix G: srcA ships a gate
// script; srcE ships ONLY a lefthook-local.yml that runs that script. A merge
// init blesses the pair (the wiring guard runs against the MERGED tree, where the
// script is present). Removing A (the script supplier) would delete the script
// but keep E's wiring — every commit would then fail exit 127. `remove A` must
// refuse before any mutation, naming the stranded script; the repo stays
// committable. Removing E instead strands nothing and works.
func TestRemoveRefusesWhenItStrandsSurvivorWiring(t *testing.T) {
	dir, repo := initRepo(t)
	srcTestEnv(t)
	stubLefthook(t)
	useBasePayloadDir(t) // empty base — the script comes only from A

	srcA := newHarnessSource(t, "a", map[string]string{
		".omakase/gates/gate-a.sh": "#!/bin/sh\ntrue\n",
	})
	srcE := newHarnessSource(t, "e", map[string]string{
		"lefthook-local.yml": "pre-commit:\n  jobs:\n    - run: bash .omakase/gates/gate-a.sh\n",
	})

	if code := RunInit([]string{"--source", srcA}, io.Discard, io.Discard); code != 0 {
		t.Fatal("init A failed")
	}
	// The merged wiring guard passes: gate-a.sh (from A) satisfies E's wiring.
	if code := RunInit([]string{"--source", srcE}, io.Discard, io.Discard); code != 0 {
		t.Fatal("init E failed — the merged wiring guard should pass (gate-a.sh present)")
	}
	before := snapshotTree(t, repo.OMK)

	// remove A: the survivor E's wiring references gate-a.sh, which only A ships →
	// refuse before mutation.
	var stdout, stderr strings.Builder
	code := RunRemove([]string{srcA}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("remove A exit = %d, want 1 (strands survivor wiring); stderr=%q", code, stderr.String())
	}
	eq(t, "stdout", stdout.String(), "")
	if !strings.Contains(stderr.String(), ".omakase/gates/gate-a.sh") {
		t.Errorf("refusal must name the stranded script:\n%s", stderr.String())
	}
	if !strings.HasPrefix(stderr.String(), "omakase:") {
		t.Errorf("refusal must be omakase:-prefixed:\n%s", stderr.String())
	}
	// Zero mutation: $OMK byte-identical, the script still present, tree committable.
	if after := snapshotTree(t, repo.OMK); !maps.Equal(before, after) {
		t.Errorf("stranded-wiring refusal mutated $OMK")
	}
	eq(t, "gate-a.sh still present", readFileT(t, filepath.Join(dir, ".omakase", "gates", "gate-a.sh")), "#!/bin/sh\ntrue\n")
	if o := gitStdout(dir, "status", "--porcelain"); o != "" {
		t.Errorf("stranded-wiring refusal dirtied the tree: %q", o)
	}

	// remove E instead: the survivor A has no lefthook-local.yml, so nothing is
	// stranded — the removal works.
	if code := RunRemove([]string{srcE}, io.Discard, io.Discard); code != 0 {
		t.Fatalf("remove E exit = %d, want 0 (strands nothing)", code)
	}
	rows := state.ReadSources(filepath.Join(repo.OMK, "sources.tsv"))
	if len(rows) != 1 || rows[0].Source != srcA {
		t.Errorf("after remove E, sources.tsv = %+v, want the sole A row", rows)
	}
	if _, err := os.Stat(filepath.Join(dir, ".omakase", "gates", "gate-a.sh")); err != nil {
		t.Errorf("gate-a.sh missing after remove E: %v", err)
	}
}
