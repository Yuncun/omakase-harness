package overlay

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Yuncun/omakase-harness/internal/state"
)

// The byte-frozen mixed-era warnings (design §9) — pinned here verbatim so a
// wording drift in migrate.go fails loudly. Shared, one line, all verbs.
const (
	wantMixedAxis1 = "omakase: WARNING — a pre-layers omakase run changed this repo's source ($OMK/source disagrees with sources.tsv); run omakase init to reheal.\n"
	wantMixedAxis2 = "omakase: WARNING — a pre-layers omakase run changed this repo's source (a stacked layer is missing from placed.tsv); run omakase init to reheal.\n"
	wantGC8Refusal = "omakase: this repo predates layered state — run omakase init once first\n"
)

// v1omk hand-builds a $OMK directory carrying a v1-shape install: a $OMK/source
// line, a placed.tsv, and a payload-snapshot — but NO sources.tsv (the file v2
// synthesizes on first run). Returns the $OMK path.
func v1omk(t *testing.T, source string, placed string) string {
	t.Helper()
	omk := filepath.Join(t.TempDir(), "omakase")
	if err := os.MkdirAll(filepath.Join(omk, "payload-snapshot"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(omk, "source"), source)
	if placed != "" {
		writeFile(t, filepath.Join(omk, "placed.tsv"), placed)
	}
	return omk
}

// TestEnsureSourcesSynthesizesFromV1 is the core §9 lazy migration: a v1 repo
// ($OMK/source + placed.tsv, no sources.tsv) → the first EnsureSources
// synthesizes sources.tsv with commit "-" (never guessed) and writes it, byte
// for byte, silently.
func TestEnsureSourcesSynthesizesFromV1(t *testing.T) {
	omk := v1omk(t, "acme/harness\n", ".omakase/gates/example.sh\tgate\tpayload\tdeadbeef\t1\n")

	var stderr strings.Builder
	rows := EnsureSources(omk, &stderr)

	if stderr.String() != "" {
		t.Errorf("synthesis emitted stderr = %q, want silent", stderr.String())
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1: %+v", len(rows), rows)
	}
	r := rows[0]
	if r.Layer != "1" || r.Source != "acme/harness" || r.Ref != "-" || r.Commit != "-" {
		t.Errorf("row = %+v, want {1 acme/harness - - <epoch>}", r)
	}
	if !allDigits(r.Epoch) {
		t.Errorf("epoch = %q, want unix digits", r.Epoch)
	}
	// The file was written; its non-epoch prefix is byte-frozen.
	got := readFileT(t, filepath.Join(omk, "sources.tsv"))
	wantPrefix := "1\tacme/harness\t-\t-\t"
	if !strings.HasPrefix(got, wantPrefix) || !strings.HasSuffix(got, "\n") {
		t.Errorf("sources.tsv = %q, want prefix %q + epoch + newline", got, wantPrefix)
	}
	// Round-trips through the strict reader to exactly the returned rows.
	reread := state.ReadSources(filepath.Join(omk, "sources.tsv"))
	if len(reread) != 1 || reread[0] != rows[0] {
		t.Errorf("reread = %+v, want %+v", reread, rows)
	}
}

// TestEnsureSourcesRefSplit: a remembered "source#ref" splits on the first '#'
// exactly as SynthesizeSources / expandSource do.
func TestEnsureSourcesRefSplit(t *testing.T) {
	omk := v1omk(t, "acme/harness#v2\n", "")
	var stderr strings.Builder
	rows := EnsureSources(omk, &stderr)
	if len(rows) != 1 || rows[0].Source != "acme/harness" || rows[0].Ref != "v2" {
		t.Fatalf("rows = %+v, want source=acme/harness ref=v2", rows)
	}
}

// TestEnsureSourcesNoSourceNoWrite: $OMK exists (placed.tsv present) but there is
// no $OMK/source to migrate from → nil, and NO sources.tsv is written.
func TestEnsureSourcesNoSourceNoWrite(t *testing.T) {
	omk := filepath.Join(t.TempDir(), "omakase")
	if err := os.MkdirAll(omk, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(omk, "placed.tsv"), ".omakase/gates/example.sh\tgate\tpayload\tdeadbeef\t1\n")

	var stderr strings.Builder
	if rows := EnsureSources(omk, &stderr); rows != nil {
		t.Errorf("rows = %+v, want nil (no $OMK/source)", rows)
	}
	if _, err := os.Stat(filepath.Join(omk, "sources.tsv")); !os.IsNotExist(err) {
		t.Errorf("sources.tsv written despite no $OMK/source (err=%v)", err)
	}
}

// TestEnsureSourcesOMKAbsent: a not-installed repo ($OMK does not exist) is a
// silent no-op that NEVER creates $OMK — the invariant status/remove depend on.
func TestEnsureSourcesOMKAbsent(t *testing.T) {
	omk := filepath.Join(t.TempDir(), "omakase") // never created
	var stderr strings.Builder
	if rows := EnsureSources(omk, &stderr); rows != nil {
		t.Errorf("rows = %+v, want nil ($OMK absent)", rows)
	}
	if _, err := os.Stat(omk); !os.IsNotExist(err) {
		t.Errorf("EnsureSources created $OMK (want untouched): err=%v", err)
	}
}

// TestEnsureSourcesIdempotentSilent: once sources.tsv exists, a second call reads
// it back unchanged and stays silent (no re-synthesis, no spurious warning) when
// the file agrees with $OMK/source.
func TestEnsureSourcesIdempotentSilent(t *testing.T) {
	omk := v1omk(t, "acme/harness\n", "")
	var e1 strings.Builder
	rows1 := EnsureSources(omk, &e1)
	before := readFileT(t, filepath.Join(omk, "sources.tsv"))

	var e2 strings.Builder
	rows2 := EnsureSources(omk, &e2)
	after := readFileT(t, filepath.Join(omk, "sources.tsv"))

	if e2.String() != "" {
		t.Errorf("second call stderr = %q, want silent", e2.String())
	}
	if before != after {
		t.Errorf("file rewritten on read: before=%q after=%q", before, after)
	}
	if len(rows1) != len(rows2) || rows1[0] != rows2[0] {
		t.Errorf("rows differ: %+v vs %+v", rows1, rows2)
	}
}

// TestEnsureSourcesMixedEraAxis1: sources.tsv's project row disagrees with a
// $OMK/source a v1 tool rewrote → the axis-1 warning (one line), rows returned
// unchanged (warning only; no reheal in EnsureSources).
func TestEnsureSourcesMixedEraAxis1(t *testing.T) {
	omk := filepath.Join(t.TempDir(), "omakase")
	if err := os.MkdirAll(omk, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(omk, "source"), "other/harness\n") // v1 tool rewrote this
	writeFile(t, filepath.Join(omk, "sources.tsv"), "1\tacme/harness\t-\tdeadbeef\t1700000000\n")

	var stderr strings.Builder
	rows := EnsureSources(omk, &stderr)
	if stderr.String() != wantMixedAxis1 {
		t.Errorf("stderr = %q, want %q", stderr.String(), wantMixedAxis1)
	}
	if len(rows) != 1 || rows[0].Source != "acme/harness" {
		t.Errorf("rows = %+v, want the on-disk rows unchanged", rows)
	}
}

// TestEnsureSourcesMixedEraAxis2: a stacked row (layer "2") is recorded but no
// placed.tsv row carries its label (a v1 orphan sweep ate the stacked layer) → the
// axis-2 warning. The bottom row (layer "1") AGREES with $OMK/source, so axis 1 does
// not fire.
func TestEnsureSourcesMixedEraAxis2(t *testing.T) {
	omk := filepath.Join(t.TempDir(), "omakase")
	if err := os.MkdirAll(omk, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(omk, "source"), "acme/harness\n")
	writeFile(t, filepath.Join(omk, "sources.tsv"),
		"1\tacme/harness\t-\tdeadbeef\t1700000000\n"+
			"2\tyou/harness\t-\tcafef00d\t1700000000\n")
	// placed.tsv carries only a bottom-layer-labelled row — no you/harness row.
	writeFile(t, filepath.Join(omk, "placed.tsv"), ".omakase/gates/example.sh\tgate\tacme/harness\tdeadbeef\t1\n")

	var stderr strings.Builder
	EnsureSources(omk, &stderr)
	if stderr.String() != wantMixedAxis2 {
		t.Errorf("stderr = %q, want %q", stderr.String(), wantMixedAxis2)
	}
}

// TestEnsureSourcesConsistentSilent: sources.tsv agrees with $OMK/source AND the
// stacked layer is present in placed.tsv → no warning at all.
func TestEnsureSourcesConsistentSilent(t *testing.T) {
	omk := filepath.Join(t.TempDir(), "omakase")
	if err := os.MkdirAll(omk, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(omk, "source"), "acme/harness\n")
	writeFile(t, filepath.Join(omk, "sources.tsv"),
		"1\tacme/harness\t-\tdeadbeef\t1700000000\n"+
			"2\tyou/harness\t-\tcafef00d\t1700000000\n")
	writeFile(t, filepath.Join(omk, "placed.tsv"),
		"CLAUDE.local.md\tdoc\tyou/harness\tcafef00d\t1\n")

	var stderr strings.Builder
	EnsureSources(omk, &stderr)
	if stderr.String() != "" {
		t.Errorf("stderr = %q, want silent (consistent state)", stderr.String())
	}
}

// TestInitRehealsMixedEraAxis1 is the design §9 init reheal outcome: a v2 project
// install, then a "v1 tool" repoints $OMK/source at a DIFFERENT valid source out
// from under sources.tsv (mixed-era axis 1). A bare init must (a) print the axis-1
// warning EXACTLY once, and (b) reheal — re-record sources.tsv to match the new
// $OMK/source, with a freshly resolved commit — through its normal
// remembered-source flow, with no extra output beyond that one warning.
func TestInitRehealsMixedEraAxis1(t *testing.T) {
	_, repo := initRepo(t)
	srcTestEnv(t)
	stubLefthook(t)
	isolatePersonalConfig(t)
	useBasePayloadDir(t)

	src1 := newSourceRepo(t)
	writeFile(t, filepath.Join(src1, "omakase.manifest"), "name: proj1\n")
	writeFile(t, filepath.Join(src1, "payload", ".omakase", "gates", "g.sh"), "g1\n")
	commitAll(t, src1, "src1")

	var o0, e0 strings.Builder
	if code := RunInit([]string{"--source", src1}, &o0, &e0); code != 0 {
		t.Fatalf("install exit = %d; stderr=%q", code, e0.String())
	}

	// The "v1 tool": a second valid source, and $OMK/source repointed at it while
	// sources.tsv still names src1 → mixed-era axis 1 on the next run.
	src2 := newSourceRepo(t)
	writeFile(t, filepath.Join(src2, "omakase.manifest"), "name: proj2\n")
	writeFile(t, filepath.Join(src2, "payload", ".omakase", "gates", "g.sh"), "g2\n")
	commitAll(t, src2, "src2")
	writeFile(t, filepath.Join(repo.OMK, "source"), src2+"\n")

	var stdout, stderr strings.Builder
	if code := RunInit(nil, &stdout, &stderr); code != 0 {
		t.Fatalf("reheal init exit = %d; stderr=%q", code, stderr.String())
	}
	if n := strings.Count(stderr.String(), "a pre-layers omakase run changed this repo's source"); n != 1 {
		t.Errorf("mixed-era warning count = %d, want exactly 1; stderr=%q", n, stderr.String())
	}
	if !strings.Contains(stderr.String(), "($OMK/source disagrees with sources.tsv)") {
		t.Errorf("stderr missing the axis-1 parenthetical; stderr=%q", stderr.String())
	}
	// Reheal outcome: sources.tsv now names src2 with a resolved commit.
	rows := state.ReadSources(filepath.Join(repo.OMK, "sources.tsv"))
	if len(rows) != 1 || rows[0].Layer != "1" || rows[0].Source != src2 {
		t.Fatalf("post-reheal sources.tsv = %+v, want one {1 %s -} row", rows, src2)
	}
	eq(t, "reheal commit", rows[0].Commit, wantResolvedCommit(t, src2))
}

// TestRequireLayers: the GC8 guard returns true (silent) when $OMK/layers/ exists,
// and false with the byte-frozen refusal when it does not.
func TestRequireLayers(t *testing.T) {
	omk := filepath.Join(t.TempDir(), "omakase")
	if err := os.MkdirAll(omk, 0o755); err != nil {
		t.Fatal(err)
	}
	var e1 strings.Builder
	if RequireLayers(omk, &e1) {
		t.Error("RequireLayers = true with no layers/ dir")
	}
	if e1.String() != wantGC8Refusal {
		t.Errorf("stderr = %q, want %q", e1.String(), wantGC8Refusal)
	}

	if err := os.MkdirAll(filepath.Join(omk, "layers"), 0o755); err != nil {
		t.Fatal(err)
	}
	var e2 strings.Builder
	if !RequireLayers(omk, &e2) {
		t.Error("RequireLayers = false with layers/ present")
	}
	if e2.String() != "" {
		t.Errorf("stderr = %q, want silent", e2.String())
	}
}
