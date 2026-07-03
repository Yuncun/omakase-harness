package overlay

import (
	"io"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Yuncun/omakase-harness/internal/state"
)

// allDigits reports whether s is a non-empty run of decimal digits (a plausible
// unix-time epoch field).
func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// isFullSHA reports whether s has the shape of a resolved git commit sha:
// exactly 40 lowercase hex characters (the same shape-only check style as
// allDigits, for sources.tsv column 4).
func isFullSHA(s string) bool {
	if len(s) != 40 {
		return false
	}
	for _, r := range s {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

// wantResolvedCommit recomputes the cache dir the SAME way the production
// code's resolvedCommit does (sourceCacheDir(src), both unexported helpers
// callable in-package) and reads its HEAD directly with the real git binary —
// an independent computation of what a sources.tsv row's commit column MUST
// equal, not a re-assertion of the production code's own return value.
func wantResolvedCommit(t *testing.T, src string) string {
	t.Helper()
	cache := sourceCacheDir(src)
	sha := gitStdout(cache, "rev-parse", "HEAD")
	sha = strings.TrimRight(sha, "\n")
	if sha == "" || !isFullSHA(sha) {
		t.Fatalf("test could not resolve %s's cache (%s) HEAD via git rev-parse", src, cache)
	}
	return sha
}

// These are the Phase 3.5 stacking-integration tests: the `init` decision table
// (single source folds base into layer 1; a second `init` stacks a source on top,
// cap 2; a re-init repairs a recorded layer without reorder; a third distinct
// source errors). "invariance" = the assertion is the ABSENCE of any layer
// artifact (the base-only guarantee GC2); "red-first" = the stacking behavior is
// asserted directly. Path/epoch/commit-bearing expectations are CONSTRUCTED from
// known inputs at test time, never hardcoded temp paths or a frozen clock.
//
// Shared helpers (initRepo, stubLefthook, singleGatePayload, srcTestEnv,
// useBasePayloadDir, newSourceRepo, commitAll, writeFile, readFileT, eq, chdir,
// gitStdout, sha256hex, snapshotTree) live in init_test.go / source_test.go /
// overlay_test.go / layers_test.go.

// isolatePersonalConfig points XDG_CONFIG_HOME at an EMPTY temp dir so a test is
// deterministic regardless of the real machine's per-user config. Retained
// because migrate_test.go isolates the environment through it.
func isolatePersonalConfig(t *testing.T) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
}

// newHarnessSource builds a valid harness source repo shipping the given payload
// files (keyed by dest-relative path) and returns its absolute path — the string
// a test passes to `init --source`.
func newHarnessSource(t *testing.T, name string, payload map[string]string) string {
	t.Helper()
	src := newSourceRepo(t)
	writeFile(t, filepath.Join(src, "omakase.manifest"), "name: "+name+"\n")
	for rel, content := range payload {
		writeFile(t, filepath.Join(src, "payload", rel), content)
	}
	commitAll(t, src, name)
	return src
}

// ---------------------------------------------------------------- GC2 base-only invariance

// TestBaseOnlyInstallWritesNoLayerArtifacts is the invariance proof: a plain
// base-only install (no source) creates NEITHER sources.tsv NOR $OMK/layers/, and
// keeps placed.tsv col 3 = the literal "payload".
func TestBaseOnlyInstallWritesNoLayerArtifacts(t *testing.T) {
	_, repo := initRepo(t)
	stubLefthook(t)
	singleGatePayload(t)

	var stdout, stderr strings.Builder
	if code := RunInit(nil, &stdout, &stderr); code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%q", code, stderr.String())
	}
	if _, err := os.Stat(filepath.Join(repo.OMK, "sources.tsv")); !os.IsNotExist(err) {
		t.Errorf("base-only install wrote sources.tsv (want absent): err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(repo.OMK, "layers")); !os.IsNotExist(err) {
		t.Errorf("base-only install created $OMK/layers/ (want absent): err=%v", err)
	}
	for _, r := range state.ReadPlaced(filepath.Join(repo.OMK, "placed.tsv")) {
		if r.Src != "payload" {
			t.Errorf("placed.tsv col3 = %q for %q, want \"payload\"", r.Src, r.Rel)
		}
	}
}

// TestOmakasePayloadOverridesBaseInvariance: OMAKASE_PAYLOAD set, no source —
// still the base-only path, col3 "payload", NO layer artifacts.
func TestOmakasePayloadOverridesBaseInvariance(t *testing.T) {
	dir, repo := initRepo(t)
	stubLefthook(t)
	p := t.TempDir()
	t.Setenv("OMAKASE_PAYLOAD", p)
	writeFile(t, filepath.Join(p, ".omakase", "gates", "ex.sh"), "#!/bin/sh\ntrue\n")

	var stdout, stderr strings.Builder
	if code := RunInit(nil, &stdout, &stderr); code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%q", code, stderr.String())
	}
	if _, err := os.Stat(filepath.Join(repo.OMK, "sources.tsv")); !os.IsNotExist(err) {
		t.Errorf("OMAKASE_PAYLOAD base-only wrote sources.tsv (want absent)")
	}
	if _, err := os.Stat(filepath.Join(repo.OMK, "layers")); !os.IsNotExist(err) {
		t.Errorf("OMAKASE_PAYLOAD base-only created $OMK/layers/ (want absent)")
	}
	if _, err := os.Stat(filepath.Join(dir, ".omakase", "gates", "ex.sh")); err != nil {
		t.Errorf("OMAKASE_PAYLOAD payload not installed: %v", err)
	}
}

// ---------------------------------------------------------------- single source (layer 1)

// TestSingleSourceWritesOrdinalRow: a single `init --source` records ONE row with
// ordinal layer "1", builds $OMK/layers/1/ (NOT a role-named dir), and every placed
// row carries the source label in col 3 (base folds into layer 1).
func TestSingleSourceWritesOrdinalRow(t *testing.T) {
	_, repo := initRepo(t)
	srcTestEnv(t)
	stubLefthook(t)
	base := useBasePayloadDir(t)
	writeFile(t, filepath.Join(base, ".omakase", "bin", "base.sh"), "base\n")

	src := newHarnessSource(t, "proj", map[string]string{".omakase/gates/g.sh": "g\n"})

	var stdout, stderr strings.Builder
	if code := RunInit([]string{"--source", src}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%q", code, stderr.String())
	}
	for _, r := range state.ReadPlaced(filepath.Join(repo.OMK, "placed.tsv")) {
		if r.Src != src {
			t.Errorf("placed.tsv col3 = %q for %q, want %q", r.Src, r.Rel, src)
		}
	}
	rows := state.ReadSources(filepath.Join(repo.OMK, "sources.tsv"))
	if len(rows) != 1 || rows[0].Layer != "1" || rows[0].Source != src || rows[0].Ref != "-" {
		t.Fatalf("sources.tsv = %+v, want one {1 %s -} row", rows, src)
	}
	if !isFullSHA(rows[0].Commit) {
		t.Errorf("row commit = %q, want a 40-hex sha", rows[0].Commit)
	}
	eq(t, "row commit", rows[0].Commit, wantResolvedCommit(t, src))
	if !allDigits(rows[0].Epoch) {
		t.Errorf("row epoch = %q, want a unix-time decimal", rows[0].Epoch)
	}
	if _, err := os.Stat(filepath.Join(repo.OMK, "layers", "1", "placed.tsv")); err != nil {
		t.Errorf("layers/1 not built: %v", err)
	}
	if _, err := os.Stat(filepath.Join(repo.OMK, "layers", "project")); err == nil {
		t.Error("role-named layers/project store exists (want ordinal layers/1 only)")
	}
	// GC4: no `personal`/`add` tokens in the install output.
	if strings.Contains(stdout.String(), "personal") {
		t.Errorf("stdout carries a `personal` token:\n%s", stdout.String())
	}
}

// ---------------------------------------------------------------- bridge (single source)

// TestBridgePlacedForSingleSourceAGENTS: a single source shipping a root AGENTS.md
// but NO CLAUDE.md, in a repo that does not track CLAUDE.md, gets the §7 bridge — a
// CLAUDE.md symlink -> AGENTS.md owned by layer 1, in both the working tree and the
// layers/1 store.
func TestBridgePlacedForSingleSourceAGENTS(t *testing.T) {
	dir, repo := initRepo(t)
	srcTestEnv(t)
	stubLefthook(t)
	useBasePayloadDir(t) // empty base

	src := newHarnessSource(t, "bridge", map[string]string{"AGENTS.md": "doctrine\n"})

	var stdout, stderr strings.Builder
	if code := RunInit([]string{"--source", src}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%q", code, stderr.String())
	}
	if tgt, err := os.Readlink(filepath.Join(dir, "CLAUDE.md")); err != nil || tgt != "AGENTS.md" {
		t.Fatalf("bridge CLAUDE.md not a symlink -> AGENTS.md: target=%q err=%v", tgt, err)
	}
	var found bool
	for _, r := range state.ReadPlaced(filepath.Join(repo.OMK, "placed.tsv")) {
		if r.Rel == "CLAUDE.md" {
			found = true
			if r.Src != src {
				t.Errorf("bridge col3 = %q, want %q", r.Src, src)
			}
			if r.Hash != sha256hex([]byte("AGENTS.md")) {
				t.Errorf("bridge hash = %q, want sha256(\"AGENTS.md\")", r.Hash)
			}
		}
	}
	if !found {
		t.Error("no placed.tsv row for the bridge CLAUDE.md")
	}
	if bt, err := os.Readlink(filepath.Join(repo.OMK, "layers", "1", "files", "CLAUDE.md")); err != nil || bt != "AGENTS.md" {
		t.Errorf("layers/1 store bridge = %q err=%v, want AGENTS.md", bt, err)
	}
	if out := gitStdout(dir, "status", "--porcelain"); out != "" {
		t.Errorf("git status not clean: %q", out)
	}
}

// TestBridgeSuppressedByShippedCLAUDEmd: a source shipping an EXPLICIT CLAUDE.md
// alongside AGENTS.md suppresses the bridge — CLAUDE.md is the source's own file.
func TestBridgeSuppressedByShippedCLAUDEmd(t *testing.T) {
	dir, _ := initRepo(t)
	srcTestEnv(t)
	stubLefthook(t)
	useBasePayloadDir(t)

	src := newHarnessSource(t, "explicit", map[string]string{
		"AGENTS.md": "doctrine\n",
		"CLAUDE.md": "explicit claude\n",
	})

	var stdout, stderr strings.Builder
	if code := RunInit([]string{"--source", src}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%q", code, stderr.String())
	}
	if isSymlink(filepath.Join(dir, "CLAUDE.md")) {
		t.Error("CLAUDE.md is a bridge symlink — an explicit shipped CLAUDE.md must suppress the bridge")
	}
	eq(t, "explicit CLAUDE.md content", readFileT(t, filepath.Join(dir, "CLAUDE.md")), "explicit claude\n")
}

// TestBridgeSuppressedByTrackedCLAUDEmd: a repo that COMMITS its own CLAUDE.md
// suppresses the bridge — the committed CLAUDE.md is left byte-untouched.
func TestBridgeSuppressedByTrackedCLAUDEmd(t *testing.T) {
	dir, _ := initRepo(t)
	srcTestEnv(t)
	stubLefthook(t)
	useBasePayloadDir(t)

	src := newHarnessSource(t, "tracked", map[string]string{"AGENTS.md": "doctrine\n"})

	writeFile(t, filepath.Join(dir, "CLAUDE.md"), "TEAM CLAUDE\n")
	runGitT(t, dir, "add", "CLAUDE.md")
	runGitT(t, dir, "commit", "-q", "-m", "team")

	var stdout, stderr strings.Builder
	if code := RunInit([]string{"--source", src}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%q", code, stderr.String())
	}
	eq(t, "committed CLAUDE.md untouched", readFileT(t, filepath.Join(dir, "CLAUDE.md")), "TEAM CLAUDE\n")
	if isSymlink(filepath.Join(dir, "CLAUDE.md")) {
		t.Error("bridge overwrote a committed CLAUDE.md (must be suppressed)")
	}
	if out := gitStdout(dir, "status", "--porcelain"); out != "" {
		t.Errorf("git status not clean: %q", out)
	}
}

// TestCommittedInstructionFallback (design §7 slot-fallback, plan L8): a repo that
// commits its own CLAUDE.md leaves the root instruction slot TAKEN, so a single
// source's canonical AGENTS.md reroutes to CLAUDE.local.md with the fallback
// narration; the committed file is untouched and no root AGENTS.md is placed.
func TestCommittedInstructionFallback(t *testing.T) {
	dir, repo := initRepo(t)
	srcTestEnv(t)
	stubLefthook(t)
	useBasePayloadDir(t)

	writeFile(t, filepath.Join(dir, "CLAUDE.md"), "TEAM\n")
	runGitT(t, dir, "add", "CLAUDE.md")
	runGitT(t, dir, "commit", "-q", "-m", "team")

	src := newHarnessSource(t, "a", map[string]string{"AGENTS.md": "A doctrine\n"})

	var stdout, stderr strings.Builder
	if code := RunInit([]string{"--source", src}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%q", code, stderr.String())
	}
	eq(t, "instructions rerouted to CLAUDE.local.md", readFileT(t, filepath.Join(dir, "CLAUDE.local.md")), "A doctrine\n")
	if _, err := os.Stat(filepath.Join(dir, "AGENTS.md")); err == nil {
		t.Error("root AGENTS.md placed despite a committed root instruction file (must reroute)")
	}
	eq(t, "committed CLAUDE.md untouched", readFileT(t, filepath.Join(dir, "CLAUDE.md")), "TEAM\n")
	wantFallback := "omakase: instructions from " + src + " -> CLAUDE.local.md (root slot taken)\n"
	if !strings.Contains(stdout.String(), wantFallback) {
		t.Errorf("stdout missing fallback line %q:\n%s", wantFallback, stdout.String())
	}
	for _, r := range state.ReadPlaced(filepath.Join(repo.OMK, "placed.tsv")) {
		if r.Rel == "CLAUDE.local.md" && r.Src != src {
			t.Errorf("CLAUDE.local.md col3 = %q, want %q", r.Src, src)
		}
	}
	if out := gitStdout(dir, "status", "--porcelain"); out != "" {
		t.Errorf("git status not clean: %q", out)
	}
}

// ---------------------------------------------------------------- two-source stack

// TestTwoSourceStack (red-first): init A then init B stacks B on top. A (bottom,
// base-folded) owns the root AGENTS.md and its bridge; B (top) wins the overlapping
// gate and its AGENTS.md reroutes to CLAUDE.local.md. Asserts winners, per-row col3
// labels, the two ordinal sources.tsv rows, both stores, and the GC5 narration.
func TestTwoSourceStack(t *testing.T) {
	dir, repo := initRepo(t)
	srcTestEnv(t)
	stubLefthook(t)
	base := useBasePayloadDir(t)
	writeFile(t, filepath.Join(base, ".omakase", "bin", "base.sh"), "base\n")

	srcA := newHarnessSource(t, "a", map[string]string{
		"AGENTS.md":                "A doctrine\n",
		".claude/rules/a.md":       "A rule\n",
		".omakase/gates/shared.sh": "A\n",
	})
	srcB := newHarnessSource(t, "b", map[string]string{
		"AGENTS.md":                "B doctrine\n",
		".omakase/gates/shared.sh": "B\n",
	})

	var oA, eA strings.Builder
	if code := RunInit([]string{"--source", srcA}, &oA, &eA); code != 0 {
		t.Fatalf("init A exit = %d; stderr=%q", code, eA.String())
	}
	var oB, eB strings.Builder
	if code := RunInit([]string{"--source", srcB}, &oB, &eB); code != 0 {
		t.Fatalf("init B exit = %d; stderr=%q", code, eB.String())
	}

	// ---- winners on disk ----
	eq(t, "base folded under A", readFileT(t, filepath.Join(dir, ".omakase", "bin", "base.sh")), "base\n")
	eq(t, "A rule", readFileT(t, filepath.Join(dir, ".claude", "rules", "a.md")), "A rule\n")
	eq(t, "A owns root AGENTS.md", readFileT(t, filepath.Join(dir, "AGENTS.md")), "A doctrine\n")
	eq(t, "B rerouted to CLAUDE.local.md", readFileT(t, filepath.Join(dir, "CLAUDE.local.md")), "B doctrine\n")
	eq(t, "B wins the overlap", readFileT(t, filepath.Join(dir, ".omakase", "gates", "shared.sh")), "B\n")
	if tgt, err := os.Readlink(filepath.Join(dir, "CLAUDE.md")); err != nil || tgt != "AGENTS.md" {
		t.Errorf("bridge CLAUDE.md -> AGENTS.md missing after stacking: tgt=%q err=%v", tgt, err)
	}

	// ---- placed.tsv col3 = winning layer's label ----
	col3 := map[string]string{}
	for _, r := range state.ReadPlaced(filepath.Join(repo.OMK, "placed.tsv")) {
		col3[r.Rel] = r.Src
	}
	eq(t, "col3 base.sh", col3[".omakase/bin/base.sh"], srcA)
	eq(t, "col3 a.md", col3[".claude/rules/a.md"], srcA)
	eq(t, "col3 AGENTS.md", col3["AGENTS.md"], srcA)
	eq(t, "col3 bridge CLAUDE.md", col3["CLAUDE.md"], srcA)
	eq(t, "col3 shared.sh (B wins)", col3[".omakase/gates/shared.sh"], srcB)
	eq(t, "col3 CLAUDE.local.md", col3["CLAUDE.local.md"], srcB)

	// ---- sources.tsv: ordinal rows, bottom-to-top ----
	rows := state.ReadSources(filepath.Join(repo.OMK, "sources.tsv"))
	if len(rows) != 2 {
		t.Fatalf("sources.tsv rows = %d, want 2: %+v", len(rows), rows)
	}
	if rows[0].Layer != "1" || rows[0].Source != srcA || rows[0].Ref != "-" {
		t.Errorf("row0 = %+v, want {1 %s -}", rows[0], srcA)
	}
	if rows[1].Layer != "2" || rows[1].Source != srcB || rows[1].Ref != "-" {
		t.Errorf("row1 = %+v, want {2 %s -}", rows[1], srcB)
	}
	eq(t, "row0 commit", rows[0].Commit, wantResolvedCommit(t, srcA))
	eq(t, "row1 commit", rows[1].Commit, wantResolvedCommit(t, srcB))

	// ---- both stores built, ordinal dirs only ----
	if _, err := os.Stat(filepath.Join(repo.OMK, "layers", "1", "placed.tsv")); err != nil {
		t.Errorf("layers/1 not built: %v", err)
	}
	if _, err := os.Stat(filepath.Join(repo.OMK, "layers", "2", "placed.tsv")); err != nil {
		t.Errorf("layers/2 not built: %v", err)
	}
	for _, role := range []string{"project", "personal", "base"} {
		if _, err := os.Stat(filepath.Join(repo.OMK, "layers", role)); err == nil {
			t.Errorf("role-named layers/%s store exists (want ordinal dirs only)", role)
		}
	}

	// ---- GC5 narration ----
	wantStacked := "omakase: stacked " + srcB + " on top of " + srcA + "\n"
	wantOverride := "  ^ overrides " + srcA + ": .omakase/gates/shared.sh\n"
	wantFallback := "omakase: instructions from " + srcB + " -> CLAUDE.local.md (root slot taken)\n"
	if !strings.Contains(oB.String(), wantStacked) {
		t.Errorf("init B stdout missing stacked line %q:\n%s", wantStacked, oB.String())
	}
	if !strings.Contains(oB.String(), wantOverride) {
		t.Errorf("init B stdout missing override line %q:\n%s", wantOverride, oB.String())
	}
	if !strings.Contains(oB.String(), wantFallback) {
		t.Errorf("init B stdout missing fallback line %q:\n%s", wantFallback, oB.String())
	}
	if strings.Contains(oB.String(), "personal") {
		t.Errorf("init B stdout carries a `personal` token:\n%s", oB.String())
	}
	if out := gitStdout(dir, "status", "--porcelain"); out != "" {
		t.Errorf("git status not clean: %q", out)
	}
}

// ---------------------------------------------------------------- cap (GC8)

// TestStackCapThirdSourceErrors (red-first): with two sources installed, a third
// distinct source errors (exit 1) with the exact GC5 cap line and mutates NOTHING
// — $OMK is byte-identical before and after, and no third source is even fetched.
func TestStackCapThirdSourceErrors(t *testing.T) {
	dir, repo := initRepo(t)
	srcTestEnv(t)
	stubLefthook(t)
	useBasePayloadDir(t)

	srcA := newHarnessSource(t, "a", map[string]string{".omakase/gates/a.sh": "a\n"})
	srcB := newHarnessSource(t, "b", map[string]string{".omakase/gates/b.sh": "b\n"})
	srcC := newHarnessSource(t, "c", map[string]string{".omakase/gates/c.sh": "c\n"})

	if code := RunInit([]string{"--source", srcA}, io.Discard, io.Discard); code != 0 {
		t.Fatal("init A failed")
	}
	if code := RunInit([]string{"--source", srcB}, io.Discard, io.Discard); code != 0 {
		t.Fatal("init B failed")
	}

	before := snapshotTree(t, repo.OMK)

	var stdout, stderr strings.Builder
	code := RunInit([]string{"--source", srcC}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("cap exit = %d, want 1", code)
	}
	wantErr := "omakase: this repo already has 2 harnesses (" + srcA + ", " + srcB + ") — remove one first: omakase remove <source>\n"
	eq(t, "cap stderr", stderr.String(), wantErr)
	eq(t, "cap stdout", stdout.String(), "")

	if after := snapshotTree(t, repo.OMK); !maps.Equal(before, after) {
		t.Errorf("cap attempt mutated $OMK:\nbefore=%v\nafter=%v", before, after)
	}
	if _, err := os.Stat(filepath.Join(dir, ".omakase", "gates", "c.sh")); err == nil {
		t.Error("third source's gate was placed despite the cap error")
	}
	if out := gitStdout(dir, "status", "--porcelain"); out != "" {
		t.Errorf("cap attempt dirtied the tree: %q", out)
	}
}

// ---------------------------------------------------------------- repair (no reorder)

// TestReinitSameSourceRepairs: re-`init`-ing the one installed source repairs layer
// 1 with v1-parity — no second layer, and sources.tsv + placed.tsv byte-identical.
func TestReinitSameSourceRepairs(t *testing.T) {
	_, repo := initRepo(t)
	srcTestEnv(t)
	stubLefthook(t)
	useBasePayloadDir(t)

	src := newHarnessSource(t, "a", map[string]string{".omakase/gates/a.sh": "a\n"})

	if code := RunInit([]string{"--source", src}, io.Discard, io.Discard); code != 0 {
		t.Fatal("first init failed")
	}
	sources1 := readFileT(t, filepath.Join(repo.OMK, "sources.tsv"))
	placed1 := readFileT(t, filepath.Join(repo.OMK, "placed.tsv"))

	var stdout, stderr strings.Builder
	if code := RunInit([]string{"--source", src}, &stdout, &stderr); code != 0 {
		t.Fatalf("re-init exit = %d; stderr=%q", code, stderr.String())
	}
	eq(t, "sources.tsv byte-identical after repair", readFileT(t, filepath.Join(repo.OMK, "sources.tsv")), sources1)
	eq(t, "placed.tsv byte-identical after repair", readFileT(t, filepath.Join(repo.OMK, "placed.tsv")), placed1)
	if _, err := os.Stat(filepath.Join(repo.OMK, "layers", "2")); err == nil {
		t.Error("re-init of the same source created a second layer (want repair, no stack)")
	}
	if strings.Contains(stdout.String(), "stacked ") {
		t.Errorf("re-init of the same source narrated a stack:\n%s", stdout.String())
	}
}

// TestReinitBottomInTwoStackNoReorder (plan L2): with A+B installed, re-`init`-ing
// A (the bottom) repairs it without reordering — sources.tsv stays byte-identical
// (2 ordinal rows), B still wins the overlap, and no third layer appears.
func TestReinitBottomInTwoStackNoReorder(t *testing.T) {
	dir, repo := initRepo(t)
	srcTestEnv(t)
	stubLefthook(t)
	useBasePayloadDir(t)

	srcA := newHarnessSource(t, "a", map[string]string{".omakase/gates/shared.sh": "A\n"})
	srcB := newHarnessSource(t, "b", map[string]string{".omakase/gates/shared.sh": "B\n"})

	if code := RunInit([]string{"--source", srcA}, io.Discard, io.Discard); code != 0 {
		t.Fatal("init A failed")
	}
	if code := RunInit([]string{"--source", srcB}, io.Discard, io.Discard); code != 0 {
		t.Fatal("init B failed")
	}
	sources2 := readFileT(t, filepath.Join(repo.OMK, "sources.tsv"))

	var stdout, stderr strings.Builder
	if code := RunInit([]string{"--source", srcA}, &stdout, &stderr); code != 0 {
		t.Fatalf("re-init A exit = %d; stderr=%q", code, stderr.String())
	}
	eq(t, "sources.tsv byte-identical (no reorder)", readFileT(t, filepath.Join(repo.OMK, "sources.tsv")), sources2)
	rows := state.ReadSources(filepath.Join(repo.OMK, "sources.tsv"))
	if len(rows) != 2 || rows[0].Layer != "1" || rows[0].Source != srcA || rows[1].Layer != "2" || rows[1].Source != srcB {
		t.Fatalf("sources.tsv reordered/changed: %+v", rows)
	}
	eq(t, "B still wins the overlap", readFileT(t, filepath.Join(dir, ".omakase", "gates", "shared.sh")), "B\n")
	if _, err := os.Stat(filepath.Join(repo.OMK, "layers", "3")); err == nil {
		t.Error("re-init of the bottom created a third layer")
	}
}

// ---------------------------------------------------------------- bare re-init heals a stack

// TestBareInitTwoStackHeals (plan L9): with A+B installed, deleting a B-won file
// and an A-only file, a bare `init` re-places both from the merged view and leaves
// sources.tsv byte-identical.
func TestBareInitTwoStackHeals(t *testing.T) {
	dir, repo := initRepo(t)
	srcTestEnv(t)
	stubLefthook(t)
	useBasePayloadDir(t)

	srcA := newHarnessSource(t, "a", map[string]string{
		".omakase/gates/shared.sh": "A\n",
		".claude/rules/a.md":       "A rule\n",
	})
	srcB := newHarnessSource(t, "b", map[string]string{".omakase/gates/shared.sh": "B\n"})

	if code := RunInit([]string{"--source", srcA}, io.Discard, io.Discard); code != 0 {
		t.Fatal("init A failed")
	}
	if code := RunInit([]string{"--source", srcB}, io.Discard, io.Discard); code != 0 {
		t.Fatal("init B failed")
	}
	sources2 := readFileT(t, filepath.Join(repo.OMK, "sources.tsv"))

	// Delete a B-won file and an A-only file.
	if err := os.Remove(filepath.Join(dir, ".omakase", "gates", "shared.sh")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, ".claude", "rules", "a.md")); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr strings.Builder
	if code := RunInit(nil, &stdout, &stderr); code != 0 {
		t.Fatalf("bare re-init exit = %d; stderr=%q", code, stderr.String())
	}
	eq(t, "B-won file re-placed", readFileT(t, filepath.Join(dir, ".omakase", "gates", "shared.sh")), "B\n")
	eq(t, "A-only file re-placed", readFileT(t, filepath.Join(dir, ".claude", "rules", "a.md")), "A rule\n")
	eq(t, "sources.tsv untouched by heal", readFileT(t, filepath.Join(repo.OMK, "sources.tsv")), sources2)
}

// ---------------------------------------------------------------- stacking fail-closed

// TestStackFetchFailLeavesBottomIntact: stacking a broken source (no manifest)
// fails closed with the byte-identical fetch refusal, exit 1 — the recorded stack
// stays [1=A] with no layers/2, and A's files are untouched.
func TestStackFetchFailLeavesBottomIntact(t *testing.T) {
	dir, repo := initRepo(t)
	srcTestEnv(t)
	stubLefthook(t)
	useBasePayloadDir(t)

	srcA := newHarnessSource(t, "a", map[string]string{".omakase/gates/a.sh": "a\n"})
	if code := RunInit([]string{"--source", srcA}, io.Discard, io.Discard); code != 0 {
		t.Fatal("init A failed")
	}

	// A source repo with NO omakase.manifest.
	badB := newSourceRepo(t)
	writeFile(t, filepath.Join(badB, "payload", "rule.md"), "a rule\n")
	commitAll(t, badB, "no-manifest")

	var stdout, stderr strings.Builder
	code := RunInit([]string{"--source", badB}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("exit = %d, want 1; stderr=%q", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "omakase: source '"+badB+"' has no omakase.manifest at its root — not an omakase source\n") {
		t.Errorf("stacking refusal not byte-identical to the source arm:\n%s", stderr.String())
	}
	rows := state.ReadSources(filepath.Join(repo.OMK, "sources.tsv"))
	if len(rows) != 1 || rows[0].Layer != "1" || rows[0].Source != srcA {
		t.Errorf("recorded stack changed by a failed stack: %+v", rows)
	}
	if _, err := os.Stat(filepath.Join(repo.OMK, "layers", "2")); err == nil {
		t.Error("layers/2 built despite a failed stack")
	}
	eq(t, "A's gate intact", readFileT(t, filepath.Join(dir, ".omakase", "gates", "a.sh")), "a\n")
}

// TestStackCollisionFailClosed: stacking a source that ships BOTH a root AGENTS.md
// (rerouted to CLAUDE.local.md because the bottom owns the root slot) and an
// explicit CLAUDE.local.md fights over one dest — buildMergedStaging refuses
// fail-closed, exit 1, naming both source rels; the recorded stack stays [1=A].
func TestStackCollisionFailClosed(t *testing.T) {
	dir, repo := initRepo(t)
	srcTestEnv(t)
	stubLefthook(t)
	useBasePayloadDir(t)

	srcA := newHarnessSource(t, "a", map[string]string{"AGENTS.md": "A doctrine\n"})
	if code := RunInit([]string{"--source", srcA}, io.Discard, io.Discard); code != 0 {
		t.Fatal("init A failed")
	}

	srcB := newHarnessSource(t, "b", map[string]string{
		"AGENTS.md":       "reroutes to CLAUDE.local.md\n",
		"CLAUDE.local.md": "explicit, same dest\n",
	})

	var stdout, stderr strings.Builder
	code := RunInit([]string{"--source", srcB}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("exit = %d, want 1; stderr=%q", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "AGENTS.md") || !strings.Contains(stderr.String(), "CLAUDE.local.md") {
		t.Errorf("collision refusal must name both source rels:\n%s", stderr.String())
	}
	rows := state.ReadSources(filepath.Join(repo.OMK, "sources.tsv"))
	if len(rows) != 1 || rows[0].Source != srcA {
		t.Errorf("recorded stack changed by a collision refusal: %+v", rows)
	}
	if _, err := os.Stat(filepath.Join(repo.OMK, "layers", "2")); err == nil {
		t.Error("layers/2 built despite a collision refusal")
	}
	if out := gitStdout(dir, "status", "--porcelain"); out != "" {
		t.Errorf("collision refusal dirtied the tree: %q", out)
	}
}
