package status

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/Yuncun/omakase-harness/internal/gate"
	"github.com/Yuncun/omakase-harness/internal/state"
)

// fixtureGates exercises every render rule in one pass: a plain gate (markers ->
// runs every fire), a cacheable + glob gate whose glob carries a literal `|`
// (tests -> mdcell escaping), and a cacheable + glob gate wired but never run
// (review -> not yet run).
var fixtureGates = []gate.Gate{
	{Name: "markers", Hook: "pre-commit", Run: ".omakase/gates/example.sh"},
	{Name: "tests", Hook: "pre-push", Run: "make check", Cacheable: true, Glob: []string{"a/*|b/*"}},
	{Name: "review", Hook: "pre-push", Run: "echo BLOCKED; exit 1", Cacheable: true, Glob: []string{"src/*"}},
}

// fixtureLedger: markers passed 300s before now, tests failed 7200s before now
// (now pinned at 2000000000).
const fixtureLedger = "1999999700\tmarkers\tpass\tabc123\n1999992800\ttests\tfail\tdef456\n"

const wantGuardsMD = "| Run when | Guard | Verdict | |\n" +
	"| --- | --- | --- | --- |\n" +
	"| `pre-commit` | markers | ✓ 5m |  |\n" +
	"| `pre-push` | tests | ✗ 2h | cached · a/*\\|b/* |\n" +
	"| `pre-push` | review | — | cached · src/* |\n"

const wantGuardsTerm = "  pre-commit   markers   ✓ 5m\n" +
	"  pre-push     tests     ✗ 2h    cached · a/*|b/*\n" +
	"  pre-push     review    —       cached · src/*\n"

// verdictsFrom writes ledger bytes to a temp ledger.tsv and reads them back the
// way production does, through state.LatestVerdicts, so the chart tests join
// verdicts through the same reader the binary uses.
func verdictsFrom(t *testing.T, ledger string) map[string]state.Verdict {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "ledger.tsv")
	if err := os.WriteFile(path, []byte(ledger), 0o644); err != nil {
		t.Fatal(err)
	}
	return state.LatestVerdicts(path)
}

func TestGuardsChartMD(t *testing.T) {
	verds := verdictsFrom(t, fixtureLedger)
	var buf bytes.Buffer
	renderGuardsChart(&buf, fixtureGates, verds, 2000000000, true)
	if got := buf.String(); got != wantGuardsMD {
		t.Errorf("guards chart (md) mismatch\n--- got ---\n%s\n--- want ---\n%s", got, wantGuardsMD)
	}
}

func TestGuardsChartTerm(t *testing.T) {
	verds := verdictsFrom(t, fixtureLedger)
	var buf bytes.Buffer
	renderGuardsChart(&buf, fixtureGates, verds, 2000000000, false)
	if got := buf.String(); got != wantGuardsTerm {
		t.Errorf("guards chart (term) mismatch\n--- got ---\n%q\n--- want ---\n%q", got, wantGuardsTerm)
	}
}

func TestGuardsChartAgeBuckets(t *testing.T) {
	verds := verdictsFrom(t, fixtureLedger)
	// markers age drives the tested bucket in the first row's verdict cell.
	cases := []struct {
		name    string
		now     int64
		wantAge string
	}{
		{"lt1m", 1999999710, "<1m"},
		{"hours", 1999999700 + 6910, "1h"},
		{"days", 1999999700 + 3*86400, "3d"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			renderGuardsChart(&buf, fixtureGates, verds, tc.now, true)
			wantRow := "| `pre-commit` | markers | ✓ " + tc.wantAge + " |  |"
			if !bytes.Contains(buf.Bytes(), []byte(wantRow)) {
				t.Errorf("age bucket %q: missing row %q in\n%s", tc.wantAge, wantRow, buf.String())
			}
		})
	}
}

// RenderGuards with no snapshot manifest (or none declaring gates) prints the
// no-gates note in both renders.
func TestRenderGuardsNoGates(t *testing.T) {
	omk := t.TempDir()
	for _, md := range []bool{true, false} {
		var buf bytes.Buffer
		RenderGuards(&buf, omk, md)
		want := "  (no gates declared — this harness gates nothing)\n"
		if md {
			want = "_no gates declared — this harness gates nothing._\n"
		}
		if got := buf.String(); got != want {
			t.Errorf("no-gates note (md=%v) = %q, want %q", md, got, want)
		}
	}
}

// RenderGuards reads gates from the snapshot manifest under omk and joins the
// ledger — the full plumbing the binary uses.
func TestRenderGuardsFromManifest(t *testing.T) {
	t.Setenv("OMAKASE_NOW", "2000000000")
	omk := t.TempDir()
	snap := filepath.Join(omk, "payload-snapshot")
	if err := os.MkdirAll(snap, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := "name: h\nversion: 1\n\n" +
		"gate: markers\n  hook: pre-commit\n  run: .omakase/gates/example.sh\n\n" +
		"gate: tests\n  hook: pre-push\n  run: make check\n  cacheable: true\n  glob: a/*|b/*\n\n" +
		"gate: review\n  hook: pre-push\n  run: echo BLOCKED; exit 1\n  cacheable: true\n  glob: src/*\n"
	if err := os.WriteFile(filepath.Join(snap, "omakase.manifest"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(omk, "ledger.tsv"), []byte(fixtureLedger), 0o644); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	RenderGuards(&buf, omk, false)
	if got := buf.String(); got != wantGuardsTerm {
		t.Errorf("RenderGuards chart mismatch\n--- got ---\n%q\n--- want ---\n%q", got, wantGuardsTerm)
	}
}

// A declared purpose: is the gate's dim trailing detail — it wins the slot
// over the scheduling mechanics, which only print for gates without one.
// fixtureGates carry no purposes, so the goldens above double as the proof
// that a purpose-less manifest renders mechanics-only details.
func TestGuardsChartPurposeColumns(t *testing.T) {
	gates := []gate.Gate{
		{Name: "markers", Hook: "pre-commit", Run: "x", Purpose: "merge-conflict markers stay out"},
		{Name: "tests", Hook: "pre-push", Run: "make check", Cacheable: true, Glob: []string{"a/*|b/*"}},
	}
	verds := verdictsFrom(t, fixtureLedger)

	wantTerm := "  pre-commit   markers   ✓ 5m    merge-conflict markers stay out\n" +
		"  pre-push     tests     ✗ 2h    cached · a/*|b/*\n"
	var buf bytes.Buffer
	renderGuardsChart(&buf, gates, verds, 2000000000, false)
	if got := buf.String(); got != wantTerm {
		t.Errorf("purpose chart (term) mismatch\n--- got ---\n%q\n--- want ---\n%q", got, wantTerm)
	}

	wantMD := "| Run when | Guard | Verdict | |\n" +
		"| --- | --- | --- | --- |\n" +
		"| `pre-commit` | markers | ✓ 5m | merge-conflict markers stay out |\n" +
		"| `pre-push` | tests | ✗ 2h | cached · a/*\\|b/* |\n"
	buf.Reset()
	renderGuardsChart(&buf, gates, verds, 2000000000, true)
	if got := buf.String(); got != wantMD {
		t.Errorf("purpose chart (md) mismatch\n--- got ---\n%q\n--- want ---\n%q", got, wantMD)
	}
}
