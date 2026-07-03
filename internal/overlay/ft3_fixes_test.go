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
