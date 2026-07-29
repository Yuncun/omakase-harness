package basepayload

import (
	"io/fs"
	"regexp"
	"strings"
	"testing"
)

// TestEmbeddedPayloadComplete pins the embedded tree against the on-disk
// payload/ directory's known contents — in particular the dot-paths, which
// a go:embed directive missing the all: prefix would silently drop.
func TestEmbeddedPayloadComplete(t *testing.T) {
	want := map[string]bool{
		"payload/omakase.manifest": false,
		"payload/.omakase/VERSION": false,
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

// The init downgrade guard (#189) and the self-install version guard compare
// versions only when they parse as strict x.y.z — one loose release value
// would silently disable both forever. Pin the format here.
func TestBasePayloadVersionIsStrictSemver(t *testing.T) {
	b, err := FS.ReadFile("payload/.omakase/VERSION")
	if err != nil {
		t.Fatal(err)
	}
	v := strings.TrimSpace(string(b))
	if !regexp.MustCompile(`^\d+\.\d+\.\d+$`).MatchString(v) {
		t.Fatalf("payload/.omakase/VERSION = %q — must be strict x.y.z or the downgrade guards go silent", v)
	}
}
