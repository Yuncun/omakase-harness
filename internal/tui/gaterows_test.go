package tui

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// writeManifest drops a snapshot manifest under omk so GateRows can read it.
func writeManifest(t *testing.T, omk, content string) {
	t.Helper()
	dir := filepath.Join(omk, "payload-snapshot")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "omakase.manifest"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// GateRows turns each declared gate into a GateRow (Gate always true, in
// manifest order) — declaration IS wiring, so there is no non-gate row.
func TestGateRows(t *testing.T) {
	omk := t.TempDir()
	writeManifest(t, omk, "name: h\nversion: 1\n\n"+
		"gate: fmt\n  hook: pre-commit\n  run: gofmt -l .\n\n"+
		"gate: adr-required\n  hook: pre-push\n  run: .omakase/gates/adr.sh\n")
	got := GateRows(omk)
	want := []GateRow{
		{Hook: "pre-commit", Name: "fmt", Gate: true},
		{Hook: "pre-push", Name: "adr-required", Gate: true},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("GateRows() = %#v, want %#v", got, want)
	}
}

// No manifest (or an unreadable one) yields no rows.
func TestGateRows_noManifest(t *testing.T) {
	if got := GateRows(t.TempDir()); got != nil {
		t.Errorf("GateRows() with no manifest = %#v, want nil", got)
	}
}
