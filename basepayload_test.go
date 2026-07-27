package basepayload

import (
	"io/fs"
	"testing"
)

// TestEmbeddedPayloadComplete pins the embedded tree against the on-disk
// payload/ directory's known contents — in particular the dot-paths, which
// a go:embed directive missing the all: prefix would silently drop.
func TestEmbeddedPayloadComplete(t *testing.T) {
	want := map[string]bool{
		"payload/omakase.manifest":          false,
		"payload/.omakase/VERSION":          false,
		"payload/.omakase/gates/example.sh": false,
	}
	err := fs.WalkDir(FS, "payload", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if _, known := want[path]; known {
			want[path] = true
		}
		data, err := fs.ReadFile(FS, path)
		if err != nil {
			return err
		}
		if len(data) == 0 {
			t.Errorf("embedded %s is empty", path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk embedded payload: %v", err)
	}
	for path, seen := range want {
		if !seen {
			t.Errorf("embedded payload missing %s (go:embed dot-path exclusion?)", path)
		}
	}
}
