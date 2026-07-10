package status

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/Yuncun/omakase-harness/internal/lefthook"
	"github.com/Yuncun/omakase-harness/internal/state"
)

// fixtureDump is a REAL `lefthook dump` (captured from an init'd fixture repo at
// omk-cap, itself base bin/init.sh + payload/lefthook-local.yml) extended BY HAND
// per the task brief to exercise every awk rule in one pass: two hooks
// (pre-commit/pre-push) plus post-checkout, a cosmetic omakase-banner job (cleared),
// a plain gate (markers -> runs every commit), a non-gate job whose run cmd carries a
// literal `|` (lint -> mdcell escaping), a --cacheable + --glob 'a/*|b/*' gate (tests),
// a --glob 'src/*' gate wired but never run (review -> not yet run), and an
// ensure-present.sh job (self-heal). Byte-identical to omk-cap/dump.txt, the input the
// goldens below were captured from via the real bin/status.sh.
const fixtureDump = `pre-commit:
  jobs:
    - name: omakase-banner
      run: bash .omakase/bin/omakase-banner.sh pre-commit
    - name: markers
      run: bash .omakase/bin/omakase-gate.sh markers --step 'bash .omakase/gates/example.sh'
    - name: lint
      run: sh -c 'echo a | grep a'
pre-push:
  jobs:
    - name: tests
      run: bash .omakase/bin/omakase-gate.sh tests --cacheable --glob 'a/*|b/*' --step 'make check'
    - name: review
      run: bash .omakase/bin/omakase-gate.sh review --cacheable --glob 'src/*' --step 'echo BLOCKED; exit 1'
post-checkout:
  jobs:
    - name: omakase-ensure-present
      run: bash "$(git rev-parse --git-common-dir)/omakase/ensure-present.sh"
`

// fixtureLedger is the ledger the goldens were captured with: markers passed 300s
// before now, tests failed 7200s before now (now pinned at 2000000000).
const fixtureLedger = "1999999700\tmarkers\tpass\tabc123\n1999992800\ttests\tfail\tdef456\n"

// contract capture from bin/status.sh @ d5f1757 (guards slice, --markdown,
// fixtureDump + fixtureLedger, OMAKASE_NOW=2000000000). Do not retype.
const wantGuardsMD = "| Run when | Guard | Enforces | Last verdict |\n| --- | --- | --- | --- |\n| `pre-commit` | markers | runs every commit | ✓ pass - 5m ago |\n| `pre-commit` | lint | sh -c 'echo a \\| grep a' | — |\n| `pre-push` | tests | cached; scope: a/*\\|b/* | ✗ fail - 2h ago |\n| `pre-push` | review | cached; scope: src/* | - not yet run |\n| `post-checkout` | omakase-ensure-present | self-heal: restore any missing injected files | — |\n"

// contract capture from bin/status.sh @ d5f1757 (guards slice, terminal,
// fixtureDump + fixtureLedger, OMAKASE_NOW=2000000000).
const wantGuardsTerm = "  RUN WHEN        GUARD                    ENFORCES                                        LAST VERDICT\n  pre-commit      markers                  runs every commit                               ✓ pass - 5m ago\n  pre-commit      lint                     sh -c 'echo a | grep a'                         —\n  pre-push        tests                    cached; scope: a/*|b/*                          ✗ fail - 2h ago\n  pre-push        review                   cached; scope: src/*                            - not yet run\n  post-checkout   omakase-ensure-present   self-heal: restore any missing injected files   —\n"

// contract capture (terminal, same inputs, OMAKASE_NOW=1999999710 -> markers age
// 10s "<1m", tests age 6910s "1h") — covers the <1m + Nh age buckets.
const wantGuardsTermLt1m = "  RUN WHEN        GUARD                    ENFORCES                                        LAST VERDICT\n  pre-commit      markers                  runs every commit                               ✓ pass - <1m ago\n  pre-commit      lint                     sh -c 'echo a | grep a'                         —\n  pre-push        tests                    cached; scope: a/*|b/*                          ✗ fail - 1h ago\n  pre-push        review                   cached; scope: src/*                            - not yet run\n  post-checkout   omakase-ensure-present   self-heal: restore any missing injected files   —\n"

// contract capture (terminal, same inputs, OMAKASE_NOW=2000300000 -> both gates
// ~3d old) — covers the Nd age bucket.
const wantGuardsTermDays = "  RUN WHEN        GUARD                    ENFORCES                                        LAST VERDICT\n  pre-commit      markers                  runs every commit                               ✓ pass - 3d ago\n  pre-commit      lint                     sh -c 'echo a | grep a'                         —\n  pre-push        tests                    cached; scope: a/*|b/*                          ✗ fail - 3d ago\n  pre-push        review                   cached; scope: src/*                            - not yet run\n  post-checkout   omakase-ensure-present   self-heal: restore any missing injected files   —\n"

// A dump whose only job is the cosmetic banner -> zero rows -> the no-guards note.
const bannerOnlyDump = `pre-commit:
  jobs:
    - name: omakase-banner
      run: bash .omakase/bin/omakase-banner.sh pre-commit
`

// A dump with block-scalar run lines, shaped exactly as `lefthook dump`
// re-emits a folded (`run: >`) or literal (`run: |`) wiring line: the
// indicator on the run: line, the command on deeper-indented line(s). The
// gate name, --cacheable/--glob description, and ledger join must all work
// as if the run were single-line; a multi-line literal joins with spaces.
const blockScalarDump = `pre-push:
  jobs:
    - name: visual-verify
      run: |
        bash .omakase/bin/omakase-gate.sh visual-verify --cacheable --glob 'apps/web/*' --step 'echo BLOCKED; exit 1'
    - name: multiline
      run: |
        echo one
        echo two
    - name: after
      run: bash .omakase/bin/omakase-gate.sh after --step 'true'
`

// TestGuardsChartBlockScalar: the visual-verify gate resolves its name, its
// cached+scope description, and its ledger verdict through the block scalar;
// the multi-line non-gate job renders its joined command in ENFORCES; the
// following single-line job still parses (continuation consumption does not
// swallow it).
func TestGuardsChartBlockScalar(t *testing.T) {
	verds := verdictsFrom(t, "1999999700\tvisual-verify\tpass\tabc123\n")
	var buf bytes.Buffer
	renderGuardsChart(&buf, blockScalarDump, verds, 2000000000, true)
	want := "| Run when | Guard | Enforces | Last verdict |\n" +
		"| --- | --- | --- | --- |\n" +
		"| `pre-push` | visual-verify | cached; scope: apps/web/* | ✓ pass - 5m ago |\n" +
		"| `pre-push` | multiline | echo one echo two | — |\n" +
		"| `pre-push` | after | runs every commit | - not yet run |\n"
	if got := buf.String(); got != want {
		t.Errorf("guards chart (block scalar) mismatch\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

// verdictsFrom writes ledger bytes to a temp ledger.tsv and reads them back the way
// production does (state.LatestVerdicts), so the chart tests join verdicts through the
// exact same frozen-format reader the binary uses.
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
	renderGuardsChart(&buf, fixtureDump, verds, 2000000000, true)
	if got := buf.String(); got != wantGuardsMD {
		t.Errorf("guards chart (md) mismatch\n--- got ---\n%s\n--- want ---\n%s", got, wantGuardsMD)
	}
}

func TestGuardsChartTerm(t *testing.T) {
	verds := verdictsFrom(t, fixtureLedger)
	var buf bytes.Buffer
	renderGuardsChart(&buf, fixtureDump, verds, 2000000000, false)
	if got := buf.String(); got != wantGuardsTerm {
		t.Errorf("guards chart (term) mismatch\n--- got ---\n%s\n--- want ---\n%s", got, wantGuardsTerm)
	}
}

func TestGuardsChartAgeBuckets(t *testing.T) {
	verds := verdictsFrom(t, fixtureLedger)
	cases := []struct {
		name string
		now  int64
		want string
	}{
		{"lt1m+Nh", 1999999710, wantGuardsTermLt1m},
		{"Nd", 2000300000, wantGuardsTermDays},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			renderGuardsChart(&buf, fixtureDump, verds, tc.now, false)
			if got := buf.String(); got != tc.want {
				t.Errorf("guards chart (term, now=%d) mismatch\n--- got ---\n%s\n--- want ---\n%s", tc.now, got, tc.want)
			}
		})
	}
}

func TestGuardsChartNoGuardsWired(t *testing.T) {
	verds := verdictsFrom(t, "")
	for _, md := range []bool{true, false} {
		var buf bytes.Buffer
		renderGuardsChart(&buf, bannerOnlyDump, verds, 2000000000, md)
		want := "  (no guards wired)\n"
		if md {
			want = "_(no guards wired)_\n"
		}
		if got := buf.String(); got != want {
			t.Errorf("no-guards note (md=%v) = %q, want %q", md, got, want)
		}
	}
}

// writeFakeLefthook writes an executable stub that emits dumpText on `dump` (and
// nothing otherwise), returning its path — the LEFTHOOK_BIN resolution seam.
func writeFakeLefthook(t *testing.T, dumpText string) string {
	t.Helper()
	return writeFakeLefthookAt(t, t.TempDir(), dumpText)
}

// writeFakeLefthookAt plants the same stub inside dir (created if needed) — for
// tests that place it at a specific resolver tier (the omakase cache path). The
// stub needs only /bin/sh and `cat`, both reachable under the reduced PATH the
// resolution tests pin (stripLefthookEnv).
func writeFakeLefthookAt(t *testing.T, dir, dumpText string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	dumpFile := filepath.Join(dir, "dump.txt")
	if err := os.WriteFile(dumpFile, []byte(dumpText), 0o644); err != nil {
		t.Fatal(err)
	}
	lh := filepath.Join(dir, "lefthook")
	script := "#!/bin/sh\ncase \"$1\" in dump) cat " + shellQuote(dumpFile) + " ;; *) : ;; esac\n"
	if err := os.WriteFile(lh, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return lh
}

// stripLefthookEnv isolates lefthook resolution from the host machine:
// LEFTHOOK_BIN cleared, PATH reduced to the system dirs (no brew/npm lefthook
// there, but /bin/sh + cat still work for the stubs), HOME and the cache root
// pinned to fresh temp dirs so a developer's real ~/.cache/omakase/lefthook
// can never satisfy — or pollute — a resolution test. Returns the cache root.
func stripLefthookEnv(t *testing.T) string {
	t.Helper()
	t.Setenv("LEFTHOOK_BIN", "")
	t.Setenv("PATH", "/usr/bin:/bin")
	t.Setenv("HOME", t.TempDir())
	cache := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cache)
	return cache
}

func shellQuote(s string) string { return "'" + s + "'" }

func TestRenderGuardsNotResolved(t *testing.T) {
	// A lefthook that emits nothing -> empty dump -> the not-resolved note.
	lh := writeFakeLefthook(t, "")
	t.Setenv("LEFTHOOK_BIN", lh)
	root := t.TempDir()
	omk := t.TempDir()
	for _, md := range []bool{true, false} {
		var buf bytes.Buffer
		RenderGuards(&buf, root, omk, md)
		want := "  (lefthook not resolved - gates are not running)\n"
		if md {
			want = "_lefthook not resolved - gates are not running._\n"
		}
		if got := buf.String(); got != want {
			t.Errorf("not-resolved note (md=%v) = %q, want %q", md, got, want)
		}
	}
}

func TestRenderGuardsResolvedChart(t *testing.T) {
	// Exercise the full resolve->dump->join plumbing: a fake lefthook emits the
	// fixture dump, a ledger.tsv lives in omk, OMAKASE_NOW pins the ages.
	lh := writeFakeLefthook(t, fixtureDump)
	t.Setenv("LEFTHOOK_BIN", lh)
	t.Setenv("OMAKASE_NOW", "2000000000")
	root := t.TempDir()
	omk := t.TempDir()
	if err := os.WriteFile(filepath.Join(omk, "ledger.tsv"), []byte(fixtureLedger), 0o644); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	RenderGuards(&buf, root, omk, false)
	if got := buf.String(); got != wantGuardsTerm {
		t.Errorf("RenderGuards chart mismatch\n--- got ---\n%s\n--- want ---\n%s", got, wantGuardsTerm)
	}
}

// TestRenderGuardsResolvesFromOmakaseCache is the #72 regression: on a machine
// with no LEFTHOOK_BIN, no PATH lefthook, and no node_modules — exactly the
// zero-setup adopter init self-provisions for — status must resolve the
// cached binary and render the real chart, not the false
// "gates are not running" note.
func TestRenderGuardsResolvesFromOmakaseCache(t *testing.T) {
	cache := stripLefthookEnv(t)
	writeFakeLefthookAt(t, filepath.Join(cache, "omakase", "lefthook", lefthook.PinnedVersion()), fixtureDump)
	t.Setenv("OMAKASE_NOW", "2000000000")
	root := t.TempDir()
	omk := t.TempDir()
	if err := os.WriteFile(filepath.Join(omk, "ledger.tsv"), []byte(fixtureLedger), 0o644); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	RenderGuards(&buf, root, omk, false)
	if got := buf.String(); got != wantGuardsTerm {
		t.Errorf("RenderGuards (cache tier) mismatch\n--- got ---\n%s\n--- want ---\n%s", got, wantGuardsTerm)
	}
}

// TestRenderGuardsNotResolvedAnywhere drives a GENUINE resolution failure
// (every tier empty, including the cache), unlike TestRenderGuardsNotResolved
// above, which covers the resolved-but-empty-dump path via LEFTHOOK_BIN.
func TestRenderGuardsNotResolvedAnywhere(t *testing.T) {
	stripLefthookEnv(t)
	root := t.TempDir()
	omk := t.TempDir()
	for _, md := range []bool{true, false} {
		var buf bytes.Buffer
		RenderGuards(&buf, root, omk, md)
		want := "  (lefthook not resolved - gates are not running)\n"
		if md {
			want = "_lefthook not resolved - gates are not running._\n"
		}
		if got := buf.String(); got != want {
			t.Errorf("genuinely-unresolved note (md=%v) = %q, want %q", md, got, want)
		}
	}
}

// TestRenderGuardsLefthookBinBeatsCache pins the tier order: an explicit
// LEFTHOOK_BIN wins over a cached binary. The cache stub emits an EMPTY dump
// (which would render the note), the override emits the fixture — a rendered
// chart proves tier 1 was used.
func TestRenderGuardsLefthookBinBeatsCache(t *testing.T) {
	cache := stripLefthookEnv(t)
	writeFakeLefthookAt(t, filepath.Join(cache, "omakase", "lefthook", lefthook.PinnedVersion()), "")
	t.Setenv("LEFTHOOK_BIN", writeFakeLefthook(t, fixtureDump))
	t.Setenv("OMAKASE_NOW", "2000000000")
	root := t.TempDir()
	omk := t.TempDir()
	if err := os.WriteFile(filepath.Join(omk, "ledger.tsv"), []byte(fixtureLedger), 0o644); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	RenderGuards(&buf, root, omk, false)
	if got := buf.String(); got != wantGuardsTerm {
		t.Errorf("LEFTHOOK_BIN-beats-cache chart mismatch\n--- got ---\n%s\n--- want ---\n%s", got, wantGuardsTerm)
	}
}
