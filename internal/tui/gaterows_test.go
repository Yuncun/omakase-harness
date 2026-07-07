package tui

import (
	"reflect"
	"testing"
)

// dump is a synthetic `lefthook dump` literal (shape borrowed from the real
// captures in internal/status/guards_test.go's fixtureDump) hand-extended to
// exercise every ParseGateRows rule in one pass:
//   - omakase-banner: cosmetic job, always skipped.
//   - fmt: a plain (non-gate) job -> Gate=false.
//   - a second `run:` line straight after fmt's first (no intervening
//     `- name:`) -> ignored, only the first run: after a name counts.
//   - adr: a ledgered gate (omakase-gate.sh) -> Gate=true, Name is the gate
//     name (not the job name).
//   - an orphan `run:` line with no pending `- name:` -> ignored.
//   - post-checkout/heal: run contains ensure-present.sh -> skipped entirely
//     (self-heal machinery, not a consent item — Task 6 brief, beyond what
//     guards.go itself does).
const dump = `pre-commit:
  jobs:
    - name: omakase-banner
      run: bash .omakase/bin/omakase-banner.sh pre-commit
    - name: fmt
      run: gofmt -l .
      run: echo second-run-ignored
    - name: adr
      run: bash .omakase/bin/omakase-gate.sh adr-required --step 'x'
    run: echo orphan-run-ignored
post-checkout:
  jobs:
    - name: heal
      run: bash .omakase/ensure-present.sh
`

func TestParseGateRows(t *testing.T) {
	got := ParseGateRows(dump)
	want := []GateRow{
		{Hook: "pre-commit", Name: "fmt", Gate: false},
		{Hook: "pre-commit", Name: "adr-required", Gate: true},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ParseGateRows(dump) = %#v, want %#v", got, want)
	}
}

func TestParseGateRows_empty(t *testing.T) {
	if got := ParseGateRows(""); got != nil {
		t.Errorf("ParseGateRows(\"\") = %#v, want nil", got)
	}
}

// GateRows just wires resolveLefthook + dumpLefthook + ParseGateRows
// together (mirrors status.RenderGuards's own resolve/dump wiring); the only
// thing worth a unit test at this layer, without shelling out to a real
// lefthook, is the "unresolvable" short-circuit.
func TestGateRows_unresolvable(t *testing.T) {
	t.Setenv("LEFTHOOK_BIN", "")
	t.Setenv("PATH", "")
	if got := GateRows(t.TempDir()); got != nil {
		t.Errorf("GateRows() with no resolvable lefthook = %#v, want nil", got)
	}
}
