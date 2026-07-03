package overlay

import (
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/Yuncun/omakase-harness/internal/state"
)

// sha256Hex("AGENTS.md"), hand-computed via `printf '%s' "AGENTS.md" | shasum
// -a 256` and pinned literally here so the placed.tsv hash-of-symlink
// assertions below are checked against an INDEPENDENTLY known value, not
// merely against another call to the same state.HashOf function under test.
const sha256OfAGENTSmdString = "a54ff182c7e8acf56acfd6e4b9c3ff41e2c41a31c9b211b2deb9df75d9a478f9"

func mkPayload(t *testing.T, files map[string]string, symlinks map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for rel, content := range files {
		full := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for rel, target := range symlinks {
		full := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, full); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// ---------------------------------------------------------------- BuildLayerStore: store bytes

// TestBuildLayerStore_MirrorsPayloadIncludingSymlink pins the core store-byte
// contract: files/ under the new store mirrors the post-mapping payload
// exactly, a symlink source is carried AS a symlink (never dereferenced into
// a regular file), and the returned LayerSet's Rels/Src describe it.
func TestBuildLayerStore_MirrorsPayloadIncludingSymlink(t *testing.T) {
	payload := mkPayload(t,
		map[string]string{
			"AGENTS.md":      "project instructions\n",
			"lefthook.yml":   "pre-commit: {}\n",
			"docs/AGENTS.md": "nested, not the root row\n",
		},
		map[string]string{
			".claude/skills/x.md": "../../shared/x.md", // dangling on purpose; CopyEntry never dereferences
		},
	)
	omk := t.TempDir()

	set, err := BuildLayerStore(omk, LayerProject, "payload", payload, true, false)
	if err != nil {
		t.Fatalf("BuildLayerStore: %v", err)
	}

	wantRels := []string{"AGENTS.md", ".claude/skills/x.md", "docs/AGENTS.md", "lefthook.yml"}
	// Rels is lexical (destRel order): ".claude/..." < "AGENTS.md" < "docs/..." < "lefthook.yml" in
	// byte order? Compute expected order directly rather than asserting a guessed literal.
	expected := append([]string(nil), wantRels...)
	slices.Sort(expected)
	if !slices.Equal(set.Rels, expected) {
		t.Fatalf("Rels = %v, want %v (lexical)", set.Rels, expected)
	}

	filesDir := filepath.Join(omk, "layers", "project", "files")

	got, err := os.ReadFile(filepath.Join(filesDir, "AGENTS.md"))
	if err != nil || string(got) != "project instructions\n" {
		t.Errorf("files/AGENTS.md = %q, %v", got, err)
	}
	got, err = os.ReadFile(filepath.Join(filesDir, "lefthook.yml"))
	if err != nil || string(got) != "pre-commit: {}\n" {
		t.Errorf("files/lefthook.yml = %q, %v", got, err)
	}
	got, err = os.ReadFile(filepath.Join(filesDir, "docs/AGENTS.md"))
	if err != nil || string(got) != "nested, not the root row\n" {
		t.Errorf("files/docs/AGENTS.md = %q, %v", got, err)
	}

	target, err := os.Readlink(filepath.Join(filesDir, ".claude/skills/x.md"))
	if err != nil {
		t.Fatalf("files/.claude/skills/x.md is not a symlink: %v", err)
	}
	if target != "../../shared/x.md" {
		t.Errorf("symlink target = %q, want %q (must carry, never dereference)", target, "../../shared/x.md")
	}

	// Src maps every dest rel to its final on-disk path (post-rename, not the tmp path).
	for _, rel := range set.Rels {
		want := filepath.Join(filesDir, rel)
		if set.Src[rel] != want {
			t.Errorf("Src[%q] = %q, want %q", rel, set.Src[rel], want)
		}
		if _, err := os.Lstat(set.Src[rel]); err != nil {
			t.Errorf("Src[%q] = %q does not exist: %v", rel, set.Src[rel], err)
		}
	}

	// No leftover tmp dir.
	entries, err := os.ReadDir(filepath.Join(omk, "layers"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "project" {
		names := make([]string, len(entries))
		for i, e := range entries {
			names[i] = e.Name()
		}
		t.Errorf("layers/ entries = %v, want exactly [\"project\"]", names)
	}
}

// TestBuildLayerStore_Bridge pins the §7 bridge artifact: a symlink at dest
// "CLAUDE.md" whose readlink target string is exactly "AGENTS.md", placed
// alongside the project's own AGENTS.md when bridge=true.
func TestBuildLayerStore_Bridge(t *testing.T) {
	payload := mkPayload(t, map[string]string{"AGENTS.md": "project instructions\n"}, nil)
	omk := t.TempDir()

	set, err := BuildLayerStore(omk, LayerProject, "payload", payload, true, true)
	if err != nil {
		t.Fatalf("BuildLayerStore: %v", err)
	}

	filesDir := filepath.Join(omk, "layers", "project", "files")
	target, err := os.Readlink(filepath.Join(filesDir, "CLAUDE.md"))
	if err != nil {
		t.Fatalf("files/CLAUDE.md is not a symlink: %v", err)
	}
	if target != "AGENTS.md" {
		t.Errorf("bridge target = %q, want %q", target, "AGENTS.md")
	}
	gotSorted := slices.Sorted(slices.Values(set.Rels))
	if !slices.Equal(gotSorted, []string{"AGENTS.md", "CLAUDE.md"}) {
		t.Errorf("Rels = %v, want [AGENTS.md CLAUDE.md]", set.Rels)
	}

	// placed.tsv's hash for the bridge row is the sha256 of the TARGET
	// STRING "AGENTS.md" — hand-computed, pinned above — not any file's
	// content (state.HashOf's documented symlink rule).
	rows := state.ReadPlaced(filepath.Join(omk, "layers", "project", "placed.tsv"))
	found := false
	for _, r := range rows {
		if r.Rel == "CLAUDE.md" {
			found = true
			if r.Hash != sha256OfAGENTSmdString {
				t.Errorf("CLAUDE.md row hash = %q, want %q", r.Hash, sha256OfAGENTSmdString)
			}
		}
	}
	if !found {
		t.Fatal("no placed.tsv row for CLAUDE.md")
	}
}

// ---------------------------------------------------------------- BuildLayerStore: placed.tsv

// TestBuildLayerStore_PlacedTsvColumns pins the 5-column placed.tsv row shape
// for this store: column 3 (Src) is the caller-supplied label, kind is
// harness.KindOf(destRel), hash is state.HashOf of the copied file, enabled
// is always "1".
func TestBuildLayerStore_PlacedTsvColumns(t *testing.T) {
	payload := mkPayload(t, map[string]string{
		"AGENTS.md":    "hi\n",
		"lefthook.yml": "gates: {}\n",
	}, nil)
	omk := t.TempDir()

	label := "github.com/acme/harness@abc123"
	set, err := BuildLayerStore(omk, LayerProject, label, payload, true, false)
	if err != nil {
		t.Fatalf("BuildLayerStore: %v", err)
	}

	rows := state.ReadPlaced(filepath.Join(omk, "layers", "project", "placed.tsv"))
	if len(rows) != len(set.Rels) {
		t.Fatalf("got %d placed.tsv rows, want %d (one per dest entry)", len(rows), len(set.Rels))
	}
	byRel := make(map[string]state.PlacedRow, len(rows))
	for _, r := range rows {
		byRel[r.Rel] = r
	}

	agents, ok := byRel["AGENTS.md"]
	if !ok {
		t.Fatal("no placed.tsv row for AGENTS.md")
	}
	if agents.Src != label {
		t.Errorf("AGENTS.md row col3 (Src) = %q, want label %q", agents.Src, label)
	}
	if agents.Kind != "doc" {
		t.Errorf("AGENTS.md row Kind = %q, want %q", agents.Kind, "doc")
	}
	if agents.Enabled != "1" {
		t.Errorf("AGENTS.md row Enabled = %q, want %q", agents.Enabled, "1")
	}
	wantHash := state.HashOf(filepath.Join(omk, "layers", "project", "files", "AGENTS.md"))
	if agents.Hash != wantHash || wantHash == "" {
		t.Errorf("AGENTS.md row Hash = %q, want %q (and non-empty)", agents.Hash, wantHash)
	}

	lh, ok := byRel["lefthook.yml"]
	if !ok {
		t.Fatal("no placed.tsv row for lefthook.yml")
	}
	if lh.Kind != "gate" {
		t.Errorf("lefthook.yml row Kind = %q, want %q", lh.Kind, "gate")
	}
}

// ---------------------------------------------------------------- BuildLayerStore: reroute

// TestBuildLayerStore_FallbackRerouteAGENTSmd pins the one §7 reroute end to
// end through the store: with the root slot taken (rootSlotFree=false), a
// layer payload's canonical root AGENTS.md lands at files/CLAUDE.local.md,
// not files/AGENTS.md — and the store records the reroute sidecar marker
// (dest<TAB>orig, NEXT TO files/) RemoveLayer's bottom-removal re-fold reads.
func TestBuildLayerStore_FallbackRerouteAGENTSmd(t *testing.T) {
	payload := mkPayload(t, map[string]string{"AGENTS.md": "stacked additions\n"}, nil)
	omk := t.TempDir()

	// rootSlotFree=false: this layer's canonical root AGENTS.md reroutes to CLAUDE.local.md.
	set, err := BuildLayerStore(omk, LayerName("2"), "you/harness", payload, false, false)
	if err != nil {
		t.Fatalf("BuildLayerStore: %v", err)
	}

	if !slices.Equal(set.Rels, []string{"CLAUDE.local.md"}) {
		t.Fatalf("Rels = %v, want [CLAUDE.local.md]", set.Rels)
	}
	filesDir := filepath.Join(omk, "layers", "2", "files")
	if _, err := os.Stat(filepath.Join(filesDir, "AGENTS.md")); err == nil {
		t.Error("files/AGENTS.md exists — a fallen-back AGENTS.md must be rerouted, never placed as-is")
	}
	got, err := os.ReadFile(filepath.Join(filesDir, "CLAUDE.local.md"))
	if err != nil || string(got) != "stacked additions\n" {
		t.Errorf("files/CLAUDE.local.md = %q, %v", got, err)
	}
	marker, err := os.ReadFile(filepath.Join(omk, "layers", "2", "rerouted"))
	if err != nil || string(marker) != "CLAUDE.local.md\tAGENTS.md\n" {
		t.Errorf("rerouted marker = %q, %v; want %q", marker, err, "CLAUDE.local.md\tAGENTS.md\n")
	}
}

// TestBuildLayerStore_NoFallbackNoMarker: a slot-free build reroutes nothing
// and must write NO marker — and a rebuild over a store that HAD one drops it
// (the wholesale RemoveAll+Rename replaces the store dir, stale marker included).
func TestBuildLayerStore_NoFallbackNoMarker(t *testing.T) {
	payload := mkPayload(t, map[string]string{"AGENTS.md": "doctrine\n"}, nil)
	omk := t.TempDir()

	// Seed a store WITH a marker (rootSlotFree=false)...
	if _, err := BuildLayerStore(omk, LayerName("1"), "you/harness", payload, false, false); err != nil {
		t.Fatalf("seeding: %v", err)
	}
	if _, err := os.Stat(filepath.Join(omk, "layers", "1", "rerouted")); err != nil {
		t.Fatalf("seed store missing its marker: %v", err)
	}
	// ...then rebuild slot-free: the marker must be gone.
	if _, err := BuildLayerStore(omk, LayerName("1"), "you/harness", payload, true, false); err != nil {
		t.Fatalf("rebuild: %v", err)
	}
	if _, err := os.Stat(filepath.Join(omk, "layers", "1", "rerouted")); !os.IsNotExist(err) {
		t.Errorf("stale rerouted marker survived a slot-free rebuild: %v", err)
	}
	if _, err := os.Stat(filepath.Join(omk, "layers", "1", "files", "AGENTS.md")); err != nil {
		t.Errorf("slot-free rebuild did not place AGENTS.md at the root rel: %v", err)
	}
}

// ---------------------------------------------------------------- BuildLayerStore: atomicity

// snapshotTree walks dir and records every regular file's content and every
// symlink's target string, keyed by relative path — a byte-for-byte
// fingerprint used to prove a failed rebuild left a prior store untouched.
func snapshotTree(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := make(map[string]string)
	var walk func(d, rel string)
	walk = func(d, rel string) {
		ents, err := os.ReadDir(d)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range ents {
			p := filepath.Join(d, e.Name())
			r := e.Name()
			if rel != "" {
				r = rel + "/" + e.Name()
			}
			info, err := os.Lstat(p)
			if err != nil {
				t.Fatal(err)
			}
			if info.Mode()&os.ModeSymlink != 0 {
				target, err := os.Readlink(p)
				if err != nil {
					t.Fatal(err)
				}
				out["symlink:"+r] = target
			} else if info.IsDir() {
				walk(p, r)
			} else {
				content, err := os.ReadFile(p)
				if err != nil {
					t.Fatal(err)
				}
				out["file:"+r] = string(content)
			}
		}
	}
	walk(dir, "")
	return out
}

// TestBuildLayerStore_FailedRebuildLeavesPriorStoreIntact is the tmp+rename
// atomicity fault-injection test: build a store successfully, then rebuild
// with a payload containing an unreadable source file. The rebuild must
// fail, its tmp dir must be gone, and the ORIGINAL store must be byte-for-
// byte unchanged from before the failed attempt.
func TestBuildLayerStore_FailedRebuildLeavesPriorStoreIntact(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: unreadable-file permission fault injection does not apply")
	}

	omk := t.TempDir()

	goodPayload := mkPayload(t, map[string]string{
		"AGENTS.md":    "v1 instructions\n",
		"lefthook.yml": "v1 gates\n",
	}, nil)
	if _, err := BuildLayerStore(omk, LayerProject, "payload", goodPayload, true, false); err != nil {
		t.Fatalf("seeding the prior store: %v", err)
	}
	before := snapshotTree(t, filepath.Join(omk, "layers", "project"))
	if len(before) == 0 {
		t.Fatal("snapshot of seeded store is empty — test setup is broken")
	}

	badPayload := mkPayload(t, map[string]string{
		"AGENTS.md":  "v2 instructions, never should land\n",
		"secret.env": "s3cr3t\n",
	}, nil)
	unreadable := filepath.Join(badPayload, "secret.env")
	if err := os.Chmod(unreadable, 0o000); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(unreadable, 0o644) // let TempDir cleanup remove it

	_, err := BuildLayerStore(omk, LayerProject, "payload", badPayload, true, false)
	if err == nil {
		t.Fatal("BuildLayerStore succeeded reading an unreadable source file — want an error")
	}

	// tmp dir gone.
	entries, rerr := os.ReadDir(filepath.Join(omk, "layers"))
	if rerr != nil {
		t.Fatal(rerr)
	}
	if len(entries) != 1 || entries[0].Name() != "project" {
		names := make([]string, len(entries))
		for i, e := range entries {
			names[i] = e.Name()
		}
		t.Fatalf("layers/ entries after failed rebuild = %v, want exactly [\"project\"] (no leftover tmp dir)", names)
	}

	after := snapshotTree(t, filepath.Join(omk, "layers", "project"))
	if !maps.Equal(before, after) {
		t.Errorf("prior store changed after a failed rebuild:\nbefore=%v\nafter=%v", before, after)
	}
}

// ---------------------------------------------------------------- BuildLayerStore: collision

// TestBuildLayerStore_FallbackCollisionAGENTSmdAndCLAUDElocal pins the one
// reachable collision the current §7 table admits: a payload shipping BOTH a
// root AGENTS.md (rerouted to CLAUDE.local.md, root slot taken) and an
// explicit CLAUDE.local.md of its own. Must fail closed, naming both source
// rels and the shared dest — never silently pick a winner.
func TestBuildLayerStore_FallbackCollisionAGENTSmdAndCLAUDElocal(t *testing.T) {
	payload := mkPayload(t, map[string]string{
		"AGENTS.md":       "rerouted to CLAUDE.local.md\n",
		"CLAUDE.local.md": "explicit, same dest\n",
	}, nil)
	omk := t.TempDir()

	// rootSlotFree=false: AGENTS.md reroutes to CLAUDE.local.md, colliding with the explicit one.
	_, err := BuildLayerStore(omk, LayerName("2"), "payload", payload, false, false)
	if err == nil {
		t.Fatal("BuildLayerStore succeeded on a colliding payload — want a fail-closed error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "AGENTS.md") || !strings.Contains(msg, "CLAUDE.local.md") {
		t.Errorf("error %q must name both colliding source rels (AGENTS.md, CLAUDE.local.md)", msg)
	}

	// Nothing left behind: no tmp dir, no final dir either (never built before).
	if _, err := os.Stat(filepath.Join(omk, "layers")); err == nil {
		entries, _ := os.ReadDir(filepath.Join(omk, "layers"))
		if len(entries) != 0 {
			t.Errorf("layers/ has leftover entries after a refused build: %v", entries)
		}
	}
}

// ---------------------------------------------------------------- MergeLayers

// TestMergeLayers_OverlapMatrix covers the design §4 overlap rule across a
// three-set bottom-to-top stack: a rel only the bottom ships stays the
// bottom's; a rel two sets ship is won by the higher; a rel all three ship is
// won by the topmost; and a top-only CLAUDE.local.md never collides with a
// middle-only AGENTS.md — both survive independently.
func TestMergeLayers_OverlapMatrix(t *testing.T) {
	bottom := &LayerSet{Layer: LayerName("1"), Label: "base", Rels: []string{"base-only.md", "lefthook.yml", "shared-by-all.md"}}
	middle := &LayerSet{Layer: LayerName("2"), Label: "acme/harness", Rels: []string{"AGENTS.md", "lefthook.yml", "shared-by-all.md"}}
	top := &LayerSet{Layer: LayerName("3"), Label: "you/harness", Rels: []string{"CLAUDE.local.md", "shared-by-all.md"}}

	view := MergeLayers([]*LayerSet{bottom, middle, top})

	wantRels := []string{"AGENTS.md", "CLAUDE.local.md", "base-only.md", "lefthook.yml", "shared-by-all.md"}
	if !slices.Equal(view.Rels, wantRels) {
		t.Fatalf("Rels = %v, want %v (union, lexical)", view.Rels, wantRels)
	}

	cases := []struct {
		rel  string
		want *LayerSet
	}{
		{"base-only.md", bottom},  // only the bottom ships it
		{"lefthook.yml", middle},  // bottom + middle overlap: the higher wins
		{"shared-by-all.md", top}, // all three overlap: the topmost wins
		{"AGENTS.md", middle},     // middle-only
		{"CLAUDE.local.md", top},  // top-only — must NOT collide with AGENTS.md
	}
	for _, c := range cases {
		got := view.Winner[c.rel]
		if got != c.want {
			t.Errorf("Winner[%q] = %v, want %v", c.rel, layerNameOf(got), layerNameOf(c.want))
		}
	}
}

// TestMergeLayers_Empty pins the trivial empty-input case: no sets, no rels,
// no winners, no panic.
func TestMergeLayers_Empty(t *testing.T) {
	view := MergeLayers(nil)
	if len(view.Rels) != 0 {
		t.Errorf("Rels = %v, want empty", view.Rels)
	}
	if len(view.Winner) != 0 {
		t.Errorf("Winner = %v, want empty", view.Winner)
	}
}

func layerNameOf(s *LayerSet) string {
	if s == nil {
		return "<nil>"
	}
	return string(s.Layer)
}

// ---------------------------------------------------------------- RemoveLayerDir

func TestRemoveLayerDir_Present(t *testing.T) {
	omk := t.TempDir()
	payload := mkPayload(t, map[string]string{"AGENTS.md": "x\n"}, nil)
	if _, err := BuildLayerStore(omk, LayerName("2"), "payload", payload, false, false); err != nil {
		t.Fatalf("seeding: %v", err)
	}
	dir := filepath.Join(omk, "layers", "2")
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("setup: store not built: %v", err)
	}

	if err := RemoveLayerDir(omk, LayerName("2")); err != nil {
		t.Fatalf("RemoveLayerDir: %v", err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("layer dir still exists after RemoveLayerDir: err=%v", err)
	}
}

func TestRemoveLayerDir_Absent(t *testing.T) {
	omk := t.TempDir() // layers/ never created
	if err := RemoveLayerDir(omk, LayerName("2")); err != nil {
		t.Errorf("RemoveLayerDir on an absent store: %v, want nil (missing is fine)", err)
	}
}
