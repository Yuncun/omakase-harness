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
