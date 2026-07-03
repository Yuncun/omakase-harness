package status

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestStatusSynthesizesSilently pins the design §9 wiring: the first `status` run
// against a v1 repo (buildStatusFixture ships $OMK/source + placed.tsv, NO
// sources.tsv) synthesizes sources.tsv as a $OMK write — but status's stdout and
// stderr are byte-IDENTICAL before and after the synthesis, because status renders
// nothing from sources.tsv. A second run (sources.tsv now present) must produce the
// exact same bytes and stay silent (no spurious mixed-era warning, since the
// synthesized row agrees with $OMK/source).
func TestStatusSynthesizesSilently(t *testing.T) {
	repo, home, lh := buildStatusFixture(t)
	pinStatusEnv(t, repo, home, lh)
	sources := filepath.Join(repo.OMK, "sources.tsv")

	if _, err := os.Stat(sources); !os.IsNotExist(err) {
		t.Fatalf("fixture unexpectedly ships sources.tsv (precondition unmet): err=%v", err)
	}

	var o1, e1 bytes.Buffer
	if code := Run(nil, &o1, &e1); code != 0 {
		t.Fatalf("first run exit = %d; stderr=%q", code, e1.String())
	}
	// The synthesis happened silently and wrote a byte-frozen v1-migration row.
	got := readFile(t, sources)
	const wantPrefix = "project\tacme/harness\t-\t-\t" // commit "-" (never guessed), ref split
	if !strings.HasPrefix(got, wantPrefix) || !strings.HasSuffix(got, "\n") {
		t.Errorf("sources.tsv = %q, want prefix %q + epoch + newline", got, wantPrefix)
	}
	if e1.String() != "" {
		t.Errorf("first run stderr = %q, want silent", e1.String())
	}

	// Second run: sources.tsv now exists; output must be byte-identical and silent.
	var o2, e2 bytes.Buffer
	if code := Run(nil, &o2, &e2); code != 0 {
		t.Fatalf("second run exit = %d; stderr=%q", code, e2.String())
	}
	if o1.String() != o2.String() {
		t.Errorf("stdout changed across synthesis:\n--- run1 ---\n%s\n--- run2 ---\n%s", o1.String(), o2.String())
	}
	if e2.String() != "" {
		t.Errorf("second run stderr = %q, want silent", e2.String())
	}
}

// TestStatusNoSynthesisWithoutSource: a repo with placed.tsv but NO $OMK/source has
// nothing to migrate — status must write no sources.tsv.
func TestStatusNoSynthesisWithoutSource(t *testing.T) {
	repo, home, lh := buildStatusFixture(t)
	pinStatusEnv(t, repo, home, lh)
	if err := os.Remove(filepath.Join(repo.OMK, "source")); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	if code := Run(nil, &stdout, &stderr); code != 0 {
		t.Fatalf("exit = %d; stderr=%q", code, stderr.String())
	}
	if _, err := os.Stat(filepath.Join(repo.OMK, "sources.tsv")); !os.IsNotExist(err) {
		t.Errorf("status wrote sources.tsv with no $OMK/source to migrate from (err=%v)", err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}
