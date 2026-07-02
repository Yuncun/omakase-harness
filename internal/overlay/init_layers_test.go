package overlay

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Yuncun/omakase-harness/internal/state"
)

// nowUnix is the current unix time, the clock RunInit stamps sources.tsv epochs
// with — captured before a run so a refreshed epoch can be asserted as recent.
func nowUnix() int64 { return time.Now().Unix() }

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

// These are the Task 4 layering-integration tests. Discipline per scenario is
// noted in each test's doc-comment: "invariance" = the assertion is the ABSENCE of
// any layer artifact/output (the base-only guarantee GC2 — the pre-change binary
// cannot be run, so the proof is absence here + every unmodified init/source test
// still passing); "red-first" = the layered behavior is asserted directly and was
// watched to fail before the engine change; "broken-variant" = a deliberately
// malformed personal source drives the fail-closed arm. Path/epoch-bearing
// expectations are CONSTRUCTED from known inputs at test time (repo.OMK, the source
// string, the on-disk epoch), the same way init_test.go / source_test.go build
// theirs — never hardcoded temp paths or a frozen clock.
//
// Shared helpers (initRepo, stubLefthook, singleGatePayload, srcTestEnv,
// useBasePayloadDir, newSourceRepo, commitAll, writeFile, readFileT, eq, chdir,
// gitStdout, sha256hex) live in init_test.go / source_test.go / overlay_test.go.

// setPersonalConfig isolates XDG_CONFIG_HOME to a fresh temp dir and writes the
// personal-source setting (one line) there, so a test controls exactly what
// ${XDG_CONFIG_HOME:-$HOME/.config}/omakase/personal reads back.
func setPersonalConfig(t *testing.T, line string) {
	t.Helper()
	cfg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfg)
	writeFile(t, filepath.Join(cfg, "omakase", "personal"), line+"\n")
}

// isolatePersonalConfig points XDG_CONFIG_HOME at an EMPTY temp dir, so a test that
// must NOT see a personal layer is deterministic regardless of the real machine's
// ~/.config/omakase/personal.
func isolatePersonalConfig(t *testing.T) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
}

// newPersonalSource builds a valid personal harness source repo (manifest + payload)
// and returns its absolute path — the string a test writes into the personal config.
func newPersonalSource(t *testing.T, payload map[string]string) string {
	t.Helper()
	src := newSourceRepo(t)
	writeFile(t, filepath.Join(src, "omakase.manifest"), "name: personal-harness\n")
	for rel, content := range payload {
		writeFile(t, filepath.Join(src, "payload", rel), content)
	}
	commitAll(t, src, "personal")
	return src
}

// ---------------------------------------------------------------- GC2 base-only invariance

// TestBaseOnlyInstallWritesNoLayerArtifacts is the invariance proof: a plain
// base-only install (no project source, no personal setting) creates NEITHER
// sources.tsv NOR $OMK/layers/, and prints none of the new personal lines. Combined
// with every unmodified init/source test still passing, this is the base-only
// byte-invariance guarantee (GC2).
func TestBaseOnlyInstallWritesNoLayerArtifacts(t *testing.T) {
	_, repo := initRepo(t)
	stubLefthook(t)
	singleGatePayload(t)
	isolatePersonalConfig(t)

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
	if strings.Contains(stdout.String(), "personal harness") {
		t.Errorf("base-only stdout carries a personal line:\n%s", stdout.String())
	}
	// col 3 stays the literal "payload" (placed.test.sh:91 pins this).
	for _, r := range state.ReadPlaced(filepath.Join(repo.OMK, "placed.tsv")) {
		if r.Src != "payload" {
			t.Errorf("placed.tsv col3 = %q for %q, want \"payload\"", r.Src, r.Rel)
		}
	}
}

// TestOmakasePayloadOverridesBaseInvariance: OMAKASE_PAYLOAD set (base override),
// no source, no personal — still the base-only path, col3 "payload", and NO layer
// artifacts. Pins that OMAKASE_PAYLOAD overrides the BASE layer without becoming a
// project layer (GC2).
func TestOmakasePayloadOverridesBaseInvariance(t *testing.T) {
	dir, repo := initRepo(t)
	stubLefthook(t)
	p := t.TempDir()
	t.Setenv("OMAKASE_PAYLOAD", p)
	writeFile(t, filepath.Join(p, ".omakase", "gates", "ex.sh"), "#!/bin/sh\ntrue\n")
	isolatePersonalConfig(t)

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
	for _, r := range state.ReadPlaced(filepath.Join(repo.OMK, "placed.tsv")) {
		if r.Src != "payload" {
			t.Errorf("placed.tsv col3 = %q, want \"payload\"", r.Src)
		}
	}
}

// TestSourceOnlyWritesProjectRowNoPersonal (red-first): a --source install with NO
// personal setting records a single project row in sources.tsv, builds
// $OMK/layers/project/ but NOT layers/personal/, and every placed row keeps the
// source label in col 3 (I9/sources parity that base files fold into the project).
func TestSourceOnlyWritesProjectRow(t *testing.T) {
	_, repo := initRepo(t)
	srcTestEnv(t)
	stubLefthook(t)
	isolatePersonalConfig(t)
	base := useBasePayloadDir(t)
	writeFile(t, filepath.Join(base, ".omakase", "bin", "base.sh"), "base\n")

	src := newSourceRepo(t)
	writeFile(t, filepath.Join(src, "omakase.manifest"), "name: proj\n")
	writeFile(t, filepath.Join(src, "payload", ".omakase", "gates", "g.sh"), "g\n")
	commitAll(t, src, "src")

	var stdout, stderr strings.Builder
	if code := RunInit([]string{"--source", src}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%q", code, stderr.String())
	}
	// Every placed row (base + delta) carries the source label (folded project).
	for _, r := range state.ReadPlaced(filepath.Join(repo.OMK, "placed.tsv")) {
		if r.Src != src {
			t.Errorf("placed.tsv col3 = %q for %q, want %q", r.Src, r.Rel, src)
		}
	}
	// sources.tsv: exactly one project row, bottom-of-stack.
	rows := state.ReadSources(filepath.Join(repo.OMK, "sources.tsv"))
	if len(rows) != 1 || rows[0].Layer != "project" || rows[0].Source != src || rows[0].Ref != "-" || rows[0].Commit != "-" {
		t.Fatalf("sources.tsv = %+v, want one {project %s - -} row", rows, src)
	}
	if !allDigits(rows[0].Epoch) {
		t.Errorf("project row epoch = %q, want a unix-time decimal", rows[0].Epoch)
	}
	// project store built; personal store not.
	if _, err := os.Stat(filepath.Join(repo.OMK, "layers", "project", "placed.tsv")); err != nil {
		t.Errorf("layers/project not built: %v", err)
	}
	if _, err := os.Stat(filepath.Join(repo.OMK, "layers", "personal")); !os.IsNotExist(err) {
		t.Errorf("layers/personal built for a personal-less install")
	}
	if strings.Contains(stdout.String(), "personal harness") {
		t.Errorf("stdout carries a personal line for a personal-less install:\n%s", stdout.String())
	}
}

// ---------------------------------------------------------------- project + personal stack

// TestProjectPersonalStack (red-first): the full base<project<personal stack.
// Verifies winners (personal shadows project on overlap; personal AGENTS.md reroutes
// to CLAUDE.local.md), per-row col 3 labels, the two sources.tsv rows bottom-to-top,
// both layer stores, and the personal-layered stdout line.
func TestProjectPersonalStack(t *testing.T) {
	dir, repo := initRepo(t)
	srcTestEnv(t)
	stubLefthook(t)
	base := useBasePayloadDir(t)
	writeFile(t, filepath.Join(base, ".omakase", "bin", "base.sh"), "base\n")

	proj := newSourceRepo(t)
	writeFile(t, filepath.Join(proj, "omakase.manifest"), "name: proj\n")
	writeFile(t, filepath.Join(proj, "payload", ".claude", "rules", "r.md"), "proj rule\n")
	writeFile(t, filepath.Join(proj, "payload", ".omakase", "gates", "shared.sh"), "PROJECT\n")
	commitAll(t, proj, "proj")

	psrc := newPersonalSource(t, map[string]string{
		"AGENTS.md":                "personal doctrine\n", // -> CLAUDE.local.md
		".omakase/gates/shared.sh": "PERSONAL\n",          // overlaps project -> personal wins
	})
	setPersonalConfig(t, psrc)

	var stdout, stderr strings.Builder
	if code := RunInit([]string{"--source", proj}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%q", code, stderr.String())
	}

	// ---- winners on disk ----
	eq(t, "base file (folded under project)", readFileT(t, filepath.Join(dir, ".omakase", "bin", "base.sh")), "base\n")
	eq(t, "project rule", readFileT(t, filepath.Join(dir, ".claude", "rules", "r.md")), "proj rule\n")
	eq(t, "personal wins overlap", readFileT(t, filepath.Join(dir, ".omakase", "gates", "shared.sh")), "PERSONAL\n")
	eq(t, "personal AGENTS.md rerouted", readFileT(t, filepath.Join(dir, "CLAUDE.local.md")), "personal doctrine\n")
	if _, err := os.Stat(filepath.Join(dir, "AGENTS.md")); err == nil {
		t.Error("personal AGENTS.md placed as-is (must reroute to CLAUDE.local.md)")
	}

	// ---- placed.tsv col 3 = winning layer's label ----
	col3 := map[string]string{}
	for _, r := range state.ReadPlaced(filepath.Join(repo.OMK, "placed.tsv")) {
		col3[r.Rel] = r.Src
	}
	eq(t, "col3 base.sh", col3[".omakase/bin/base.sh"], proj)
	eq(t, "col3 rule", col3[".claude/rules/r.md"], proj)
	eq(t, "col3 shared.sh", col3[".omakase/gates/shared.sh"], psrc)
	eq(t, "col3 CLAUDE.local.md", col3["CLAUDE.local.md"], psrc)

	// ---- sources.tsv: project (bottom) then personal (top) ----
	rows := state.ReadSources(filepath.Join(repo.OMK, "sources.tsv"))
	if len(rows) != 2 {
		t.Fatalf("sources.tsv rows = %d, want 2: %+v", len(rows), rows)
	}
	if rows[0].Layer != "project" || rows[0].Source != proj || rows[0].Ref != "-" || rows[0].Commit != "-" {
		t.Errorf("row0 = %+v, want project %s - -", rows[0], proj)
	}
	if rows[1].Layer != "personal" || rows[1].Source != psrc || rows[1].Ref != "-" || rows[1].Commit != "-" {
		t.Errorf("row1 = %+v, want personal %s - -", rows[1], psrc)
	}
	for _, r := range rows {
		if !allDigits(r.Epoch) {
			t.Errorf("row epoch = %q, want a unix-time decimal", r.Epoch)
		}
	}

	// ---- both layer stores built (shadow-restore groundwork) ----
	eq(t, "project store view of shared.sh", readFileT(t, filepath.Join(repo.OMK, "layers", "project", "files", ".omakase", "gates", "shared.sh")), "PROJECT\n")
	eq(t, "personal store CLAUDE.local.md", readFileT(t, filepath.Join(repo.OMK, "layers", "personal", "files", "CLAUDE.local.md")), "personal doctrine\n")

	// ---- stdout personal-layered line ----
	wantLine := "omakase: personal harness layered on top (" + psrc + ") — omakase personal off to remove it everywhere.\n"
	if !strings.Contains(stdout.String(), wantLine) {
		t.Errorf("stdout missing personal-layered line:\n got:\n%s\nwant substr: %q", stdout.String(), wantLine)
	}
	if out := gitStdout(dir, "status", "--porcelain"); out != "" {
		t.Errorf("git status not clean: %q", out)
	}
}

// TestPersonalOnlyStack (red-first): a personal setting with NO project source.
// The bottom is the BASE layer (label "payload"); personal stacks on top. Pins that
// base files keep col3 "payload", personal's CLAUDE.local.md carries the personal
// label, both base + personal stores are built, and one personal sources.tsv row.
func TestPersonalOnlyStack(t *testing.T) {
	dir, repo := initRepo(t)
	srcTestEnv(t)
	stubLefthook(t)
	base := useBasePayloadDir(t)
	writeFile(t, filepath.Join(base, ".omakase", "gates", "base.sh"), "base gate\n")
	t.Setenv("OMAKASE_PAYLOAD", base) // base-as-bottom resolves via OMAKASE_PAYLOAD

	psrc := newPersonalSource(t, map[string]string{"AGENTS.md": "personal only\n"})
	setPersonalConfig(t, psrc)

	var stdout, stderr strings.Builder
	if code := RunInit(nil, &stdout, &stderr); code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%q", code, stderr.String())
	}
	eq(t, "base gate placed", readFileT(t, filepath.Join(dir, ".omakase", "gates", "base.sh")), "base gate\n")
	eq(t, "personal CLAUDE.local.md", readFileT(t, filepath.Join(dir, "CLAUDE.local.md")), "personal only\n")

	col3 := map[string]string{}
	for _, r := range state.ReadPlaced(filepath.Join(repo.OMK, "placed.tsv")) {
		col3[r.Rel] = r.Src
	}
	eq(t, "base col3", col3[".omakase/gates/base.sh"], "payload")
	eq(t, "personal col3", col3["CLAUDE.local.md"], psrc)

	rows := state.ReadSources(filepath.Join(repo.OMK, "sources.tsv"))
	if len(rows) != 1 || rows[0].Layer != "personal" || rows[0].Source != psrc {
		t.Fatalf("sources.tsv = %+v, want one personal %s row", rows, psrc)
	}
	// base + personal stores both built.
	if _, err := os.Stat(filepath.Join(repo.OMK, "layers", "base", "placed.tsv")); err != nil {
		t.Errorf("layers/base not built for a personal-only stack: %v", err)
	}
	if _, err := os.Stat(filepath.Join(repo.OMK, "layers", "personal", "placed.tsv")); err != nil {
		t.Errorf("layers/personal not built: %v", err)
	}
}

// ---------------------------------------------------------------- --no-personal persistence

// TestNoPersonalRoundTrip (red-first): the persisted per-repo opt-out round trip.
// (1) init --no-personal records a personal|off row and places NO personal layer,
//
//	printing no personal line (the flag was just typed).
//
// (2) a bare re-init keeps it off (the row is the memory, not the flag), prints the
//
//	skipped line, and preserves the off-row's epoch.
func TestNoPersonalRoundTrip(t *testing.T) {
	dir, repo := initRepo(t)
	stubLefthook(t)
	singleGatePayload(t)
	// A personal config IS present — proving --no-personal / the off-row win over it.
	setPersonalConfig(t, "you/would-be-personal")

	// (1) init --no-personal
	var o1, e1 strings.Builder
	if code := RunInit([]string{"--no-personal"}, &o1, &e1); code != 0 {
		t.Fatalf("init --no-personal exit = %d; stderr=%q", code, e1.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "CLAUDE.local.md")); err == nil {
		t.Error("--no-personal placed a personal layer")
	}
	if strings.Contains(o1.String(), "personal harness") {
		t.Errorf("--no-personal printed a personal line (should be silent when freshly given):\n%s", o1.String())
	}
	rows := state.ReadSources(filepath.Join(repo.OMK, "sources.tsv"))
	if len(rows) != 1 || rows[0].Layer != "personal" || rows[0].Source != "off" || rows[0].Ref != "-" || rows[0].Commit != "-" {
		t.Fatalf("after --no-personal, sources.tsv = %+v, want one {personal off - -} row", rows)
	}
	if !allDigits(rows[0].Epoch) {
		t.Fatalf("off-row epoch = %q, want a unix-time decimal", rows[0].Epoch)
	}
	e1epoch := rows[0].Epoch

	// (2) bare re-init: still off, skipped line printed, epoch preserved.
	var o2, e2 strings.Builder
	if code := RunInit(nil, &o2, &e2); code != 0 {
		t.Fatalf("bare re-init exit = %d; stderr=%q", code, e2.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "CLAUDE.local.md")); err == nil {
		t.Error("bare re-init placed a personal layer despite the remembered off-row")
	}
	wantSkip := "omakase: personal harness skipped in this repo (init --no-personal was set; re-init after 'omakase personal' changes to reconsider).\n"
	if !strings.Contains(o2.String(), wantSkip) {
		t.Errorf("bare re-init missing the skipped line:\n got:\n%s\nwant substr: %q", o2.String(), wantSkip)
	}
	rows2 := state.ReadSources(filepath.Join(repo.OMK, "sources.tsv"))
	if len(rows2) != 1 || rows2[0].Source != "off" {
		t.Fatalf("bare re-init off-row lost: %+v", rows2)
	}
	if rows2[0].Epoch != e1epoch {
		t.Errorf("bare re-init changed the off-row epoch %q -> %q (the row is the memory; no flag = no refresh)", e1epoch, rows2[0].Epoch)
	}
}

// TestNoPersonalFlagRefreshesEpoch (red-first): the flag REFRESHES the off-row's
// epoch. Seed a known-stale epoch, re-run WITHOUT the flag (epoch preserved), then
// WITH the flag (epoch refreshed to a recent unix time).
func TestNoPersonalFlagRefreshesEpoch(t *testing.T) {
	dir, repo := initRepo(t)
	stubLefthook(t)
	singleGatePayload(t)
	isolatePersonalConfig(t)
	if err := os.MkdirAll(repo.OMK, 0o755); err != nil {
		t.Fatal(err)
	}
	// Seed a stale off-row (epoch "1").
	if err := state.WriteSources(filepath.Join(repo.OMK, "sources.tsv"),
		[]state.SourceRow{{Layer: "personal", Source: "off", Ref: "-", Commit: "-", Epoch: "1"}}); err != nil {
		t.Fatal(err)
	}

	// Bare re-init: no flag => the stale epoch is preserved (the memory).
	var o1, e1 strings.Builder
	if code := RunInit(nil, &o1, &e1); code != 0 {
		t.Fatalf("bare exit = %d; stderr=%q", code, e1.String())
	}
	if r := state.ReadSources(filepath.Join(repo.OMK, "sources.tsv")); len(r) != 1 || r[0].Epoch != "1" {
		t.Fatalf("bare re-init did not preserve the stale off-row epoch: %+v", r)
	}

	start := nowUnix()
	// --no-personal => the epoch is refreshed to a recent unix time.
	var o2, e2 strings.Builder
	if code := RunInit([]string{"--no-personal"}, &o2, &e2); code != 0 {
		t.Fatalf("--no-personal exit = %d; stderr=%q", code, e2.String())
	}
	r := state.ReadSources(filepath.Join(repo.OMK, "sources.tsv"))
	if len(r) != 1 || r[0].Source != "off" {
		t.Fatalf("off-row lost after refresh: %+v", r)
	}
	got, err := strconv.ParseInt(r[0].Epoch, 10, 64)
	if err != nil {
		t.Fatalf("refreshed epoch %q is not decimal: %v", r[0].Epoch, err)
	}
	if got < start {
		t.Errorf("--no-personal did not refresh the epoch: got %d, want >= %d (the stale 1 must be replaced)", got, start)
	}
	_ = dir
}

// ---------------------------------------------------------------- personal fail-closed

// TestPersonalFailClosedNoManifest (broken-variant): a personal source with no
// omakase.manifest fails closed with the BYTE-IDENTICAL message a project source
// with no manifest prints (both go through fetchSource) — nothing placed, no
// sources.tsv, no layers/.
func TestPersonalFailClosedNoManifest(t *testing.T) {
	dir, repo := initRepo(t)
	srcTestEnv(t)
	stubLefthook(t)
	useBasePayloadDir(t)

	// A personal source that is a git repo but ships no manifest.
	psrc := newSourceRepo(t)
	writeFile(t, filepath.Join(psrc, "payload", "rule.md"), "a rule\n")
	commitAll(t, psrc, "no-manifest")
	setPersonalConfig(t, psrc)

	var stdout, stderr strings.Builder
	code := RunInit(nil, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("exit = %d, want 1; stderr=%q", code, stderr.String())
	}
	// Same wording as the project-source arm (source_test.go TestSourceMissingManifest).
	if !strings.Contains(stderr.String(), "omakase: source '"+psrc+"' has no omakase.manifest at its root — not an omakase source\n") {
		t.Errorf("personal manifest refusal not byte-identical to the project arm:\n%s", stderr.String())
	}
	if _, err := os.Stat(filepath.Join(dir, ".omakase")); err == nil {
		t.Error("placed files despite a personal-source refusal")
	}
	if _, err := os.Stat(filepath.Join(repo.OMK, "sources.tsv")); err == nil {
		t.Error("wrote sources.tsv despite a personal-source refusal")
	}
	if _, err := os.Stat(filepath.Join(repo.OMK, "layers")); err == nil {
		t.Error("built a layer store despite a personal-source refusal")
	}
	if out := gitStdout(dir, "status", "--porcelain"); out != "" {
		t.Errorf("refusal left changes: %q", out)
	}
}

// TestPersonalCollisionFailClosed (broken-variant): a personal payload shipping BOTH
// a root AGENTS.md (rerouted to CLAUDE.local.md) and an explicit CLAUDE.local.md
// fights over one dest — buildMergedStaging refuses fail-closed BEFORE any placement,
// naming both source rels; nothing is placed and no store is touched.
func TestPersonalCollisionFailClosed(t *testing.T) {
	dir, repo := initRepo(t)
	srcTestEnv(t)
	stubLefthook(t)
	useBasePayloadDir(t)

	psrc := newPersonalSource(t, map[string]string{
		"AGENTS.md":       "rerouted\n",
		"CLAUDE.local.md": "explicit, same dest\n",
	})
	setPersonalConfig(t, psrc)

	var stdout, stderr strings.Builder
	code := RunInit(nil, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("exit = %d, want 1; stderr=%q", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "AGENTS.md") || !strings.Contains(stderr.String(), "CLAUDE.local.md") {
		t.Errorf("collision refusal must name both source rels:\n%s", stderr.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "CLAUDE.local.md")); err == nil {
		t.Error("placed a file despite a collision refusal")
	}
	if _, err := os.Stat(filepath.Join(repo.OMK, "layers")); err == nil {
		t.Error("built a store despite a collision refusal")
	}
	if _, err := os.Stat(filepath.Join(repo.OMK, "sources.tsv")); err == nil {
		t.Error("wrote sources.tsv despite a collision refusal")
	}
}

// ---------------------------------------------------------------- bridge

// TestBridgePlacedForProjectAGENTS (red-first): a project source shipping a root
// AGENTS.md but NO CLAUDE.md, in a repo that does not track CLAUDE.md, gets the §7
// bridge — a CLAUDE.md symlink whose target string is "AGENTS.md", owned by the
// project layer (col3 = source label, hash = sha256 of the target string), present
// in both the working tree and the project store.
func TestBridgePlacedForProjectAGENTS(t *testing.T) {
	dir, repo := initRepo(t)
	srcTestEnv(t)
	stubLefthook(t)
	isolatePersonalConfig(t)
	useBasePayloadDir(t) // empty base

	proj := newSourceRepo(t)
	writeFile(t, filepath.Join(proj, "omakase.manifest"), "name: bridge\n")
	writeFile(t, filepath.Join(proj, "payload", "AGENTS.md"), "doctrine\n")
	commitAll(t, proj, "proj")

	var stdout, stderr strings.Builder
	if code := RunInit([]string{"--source", proj}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%q", code, stderr.String())
	}
	target, err := os.Readlink(filepath.Join(dir, "CLAUDE.md"))
	if err != nil || target != "AGENTS.md" {
		t.Fatalf("bridge CLAUDE.md not a symlink -> AGENTS.md: target=%q err=%v", target, err)
	}
	// placed.tsv bridge row: project label, symlink-target-string digest.
	var found bool
	for _, r := range state.ReadPlaced(filepath.Join(repo.OMK, "placed.tsv")) {
		if r.Rel == "CLAUDE.md" {
			found = true
			if r.Src != proj {
				t.Errorf("bridge col3 = %q, want %q", r.Src, proj)
			}
			if r.Hash != sha256hex([]byte("AGENTS.md")) {
				t.Errorf("bridge hash = %q, want sha256(\"AGENTS.md\")", r.Hash)
			}
		}
	}
	if !found {
		t.Error("no placed.tsv row for the bridge CLAUDE.md")
	}
	// The project store also carries the bridge.
	if bt, err := os.Readlink(filepath.Join(repo.OMK, "layers", "project", "files", "CLAUDE.md")); err != nil || bt != "AGENTS.md" {
		t.Errorf("project store bridge = %q err=%v, want AGENTS.md", bt, err)
	}
	if out := gitStdout(dir, "status", "--porcelain"); out != "" {
		t.Errorf("git status not clean: %q", out)
	}
}

// TestBridgeSuppressedByShippedCLAUDEmd (red-first): a project source shipping an
// EXPLICIT CLAUDE.md alongside AGENTS.md suppresses the bridge — CLAUDE.md is the
// source's own file, not a bridge symlink.
func TestBridgeSuppressedByShippedCLAUDEmd(t *testing.T) {
	dir, _ := initRepo(t)
	srcTestEnv(t)
	stubLefthook(t)
	isolatePersonalConfig(t)
	useBasePayloadDir(t)

	proj := newSourceRepo(t)
	writeFile(t, filepath.Join(proj, "omakase.manifest"), "name: explicit\n")
	writeFile(t, filepath.Join(proj, "payload", "AGENTS.md"), "doctrine\n")
	writeFile(t, filepath.Join(proj, "payload", "CLAUDE.md"), "explicit claude\n")
	commitAll(t, proj, "proj")

	var stdout, stderr strings.Builder
	if code := RunInit([]string{"--source", proj}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%q", code, stderr.String())
	}
	if isSymlink(filepath.Join(dir, "CLAUDE.md")) {
		t.Error("CLAUDE.md is a bridge symlink — an explicit shipped CLAUDE.md must suppress the bridge")
	}
	eq(t, "explicit CLAUDE.md content", readFileT(t, filepath.Join(dir, "CLAUDE.md")), "explicit claude\n")
}

// TestBridgeSuppressedByTrackedCLAUDEmd (red-first): a repo that COMMITS its own
// CLAUDE.md suppresses the bridge (the universal "committed file is skipped, never
// overwritten" rule) — the committed CLAUDE.md is left byte-untouched and no bridge
// is placed.
func TestBridgeSuppressedByTrackedCLAUDEmd(t *testing.T) {
	dir, _ := initRepo(t)
	srcTestEnv(t)
	stubLefthook(t)
	isolatePersonalConfig(t)
	useBasePayloadDir(t)

	proj := newSourceRepo(t)
	writeFile(t, filepath.Join(proj, "omakase.manifest"), "name: tracked\n")
	writeFile(t, filepath.Join(proj, "payload", "AGENTS.md"), "doctrine\n")
	commitAll(t, proj, "proj")

	// The repo commits its own CLAUDE.md.
	writeFile(t, filepath.Join(dir, "CLAUDE.md"), "TEAM CLAUDE\n")
	runGitT(t, dir, "add", "CLAUDE.md")
	runGitT(t, dir, "commit", "-q", "-m", "team")

	var stdout, stderr strings.Builder
	if code := RunInit([]string{"--source", proj}, &stdout, &stderr); code != 0 {
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
