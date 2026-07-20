package render

import (
	"strings"
	"testing"

	"github.com/Yuncun/omakase-harness/internal/probe"
)

// proven is a fully-verified fact sheet; tests mutate copies of it.
func proven() *probe.State {
	return &probe.State{
		Installed:      true,
		Project:        "pixterm-engine",
		Branch:         "main",
		Source:         "https://github.com/Yuncun/pixterm-harness",
		HooksInstalled: probe.OK,
		GatesMigrated:  probe.OK,
		FilesPresent:   probe.OK,
		HashesMatch:    probe.OK,
	}
}

func plain(st *probe.State) string  { return Statusline(st, Opts{Color: false}) }
func colour(st *probe.State) string { return Statusline(st, Opts{Color: true}) }

// ---------------------------------------------------------------- dark bar

func TestStatuslineDarkWhenAbsent(t *testing.T) {
	if got := Statusline(nil, Opts{}); got != "" {
		t.Fatalf("nil state: got %q, want empty", got)
	}
	if got := plain(&probe.State{Installed: false, Root: "/r"}); got != "" {
		t.Fatalf("not installed: got %q, want empty", got)
	}
}

// ---------------------------------------------------------------- proven

func TestStatuslineProven(t *testing.T) {
	got := plain(proven())
	want := "🥡 pixterm-engine ⎇main · pixterm-harness ✓"
	if got != want {
		t.Fatalf("proven plain:\n got %q\nwant %q", got, want)
	}
}

func TestStatuslineProvenIsGreenOnlyWhenAllProofsOK(t *testing.T) {
	if c := colour(proven()); !strings.Contains(c, greenOn) {
		t.Fatalf("proven color misses the green pill: %q", c)
	}
	for _, mutate := range []func(*probe.State){
		func(s *probe.State) { s.HooksInstalled = probe.Problem },
		func(s *probe.State) { s.HooksInstalled = probe.Unknown },
		func(s *probe.State) { s.GatesMigrated = probe.Problem },
		func(s *probe.State) { s.GatesMigrated = probe.Unknown },
		func(s *probe.State) { s.FilesPresent = probe.Problem },
		func(s *probe.State) { s.FilesPresent = probe.Unknown },
		func(s *probe.State) { s.HashesMatch = probe.Problem },
		func(s *probe.State) { s.HashesMatch = probe.Unknown },
	} {
		st := proven()
		mutate(st)
		if c := colour(st); strings.Contains(c, greenOn) {
			t.Fatalf("green pill rendered without full proof: %+v -> %q", st, c)
		}
		if p := plain(st); strings.Contains(p, "✓") {
			t.Fatalf("✓ rendered without full proof: %+v -> %q", st, p)
		}
	}
}

// ---------------------------------------------------------------- problems

// The amber state is one fact + one fix — never counts, never a fact list
// (#85 field audit; detail lives in `omakase status`). Hooks-not-installed
// outranks files-changed: a dead hook runs nothing, and the fix is the same.
func TestStatuslineProblemIsOneFactPlusFix(t *testing.T) {
	st := proven()
	st.HooksInstalled = probe.Problem
	st.FilesPresent = probe.Problem
	st.HashesMatch = probe.Problem
	got := plain(st)
	want := "🥡 pixterm-engine ⎇main · pixterm-harness ⚠ hooks not installed — omakase init"
	if got != want {
		t.Fatalf("problem plain:\n got %q\nwant %q", got, want)
	}
	if c := colour(st); !strings.Contains(c, amberOn) {
		t.Fatalf("problem color misses the amber pill: %q", c)
	}
}

// Missing and drifted files collapse to ONE state with one fix.
func TestStatuslineFileProblemsCollapseToOneState(t *testing.T) {
	for _, mutate := range []func(*probe.State){
		func(s *probe.State) { s.FilesPresent = probe.Problem },
		func(s *probe.State) { s.HashesMatch = probe.Problem },
		func(s *probe.State) { s.FilesPresent = probe.Problem; s.HashesMatch = probe.Problem },
	} {
		st := proven()
		mutate(st)
		got := plain(st)
		want := "🥡 pixterm-engine ⎇main · pixterm-harness ⚠ harness files changed — omakase init"
		if got != want {
			t.Fatalf("file problem plain:\n got %q\nwant %q", got, want)
		}
	}
}

// A stale (lefthook-era) snapshot is its own amber fact + fix, ranked between
// hooks-not-installed and files-changed.
func TestStatuslineStaleSnapshotIsMigrationFact(t *testing.T) {
	st := proven()
	st.GatesMigrated = probe.Problem
	got := plain(st)
	want := "🥡 pixterm-engine ⎇main · pixterm-harness ⚠ harness needs migration — omakase init"
	if got != want {
		t.Fatalf("migration plain:\n got %q\nwant %q", got, want)
	}
	if c := colour(st); !strings.Contains(c, amberOn) {
		t.Fatalf("migration color misses the amber pill: %q", c)
	}
}

func TestStatuslineUnknownNeverReadsAsWorking(t *testing.T) {
	st := proven()
	st.HooksInstalled = probe.Unknown
	got := plain(st)
	if !strings.Contains(got, "unverified") {
		t.Fatalf("unknown plain misses 'unverified': %q", got)
	}
	c := colour(st)
	if strings.Contains(c, greenOn) || strings.Contains(c, "✓") {
		t.Fatalf("unknown rendered as working: %q", c)
	}
}

// A proven problem outranks an unverified proof — the amber fact + fix
// renders alone; green stays impossible (invariant test above).
func TestStatuslineProblemOutranksUnknown(t *testing.T) {
	st := proven()
	st.HooksInstalled = probe.Problem
	st.HashesMatch = probe.Unknown
	got := plain(st)
	if !strings.Contains(got, "hooks not installed — omakase init") {
		t.Fatalf("mixed state misses the problem fact: %q", got)
	}
	if strings.Contains(got, "✓") {
		t.Fatalf("mixed state reads as working: %q", got)
	}
}

// ---------------------------------------------------------------- identity

// ⎇ shows the branch (its conventional meaning) — never a worktree folder
// name, which is frozen at creation and goes stale as branches change
// inside it (Eric misread a stale folder name as a branch on day one).
func TestStatuslineShowsTheBranch(t *testing.T) {
	st := proven()
	st.Branch = "issue-85-statusline"
	if got := plain(st); !strings.Contains(got, "⎇issue-85-statusline") {
		t.Fatalf("branch not shown: %q", got)
	}
}

func TestStatuslineBareInstallShowsNoHarnessSlot(t *testing.T) {
	st := proven()
	st.Source = ""
	got := plain(st)
	want := "🥡 pixterm-engine ⎇main ✓"
	if got != want {
		t.Fatalf("bare install:\n got %q\nwant %q", got, want)
	}
}

func TestHarnessSlot(t *testing.T) {
	cases := []struct{ override, manifest, source, want string }{
		{"acme", "", "https://github.com/x/y", "acme"},
		{"", "", "https://github.com/Yuncun/pixterm-harness.git", "pixterm-harness"},
		{"", "", "github.com/x/team-harness#v2", "team-harness"},
		{"", "", "", ""},
		// The manifest's declared name: outranks the source's last folder
		// (#131 gripe 5) but not the NAME override…
		{"", "omakase-harness-harness", "github.com/Yuncun/omakase-harness//harness", "omakase-harness-harness"},
		{"acme", "omakase-harness-harness", "github.com/Yuncun/omakase-harness//harness", "acme"},
		// …and the base payload's own name: never names a bare install.
		{"", "omakase-base", "", ""},
	}
	for _, c := range cases {
		st := &probe.State{NameOverride: c.override, ManifestName: c.manifest, Source: c.source}
		if got := HarnessSlot(st); got != c.want {
			t.Fatalf("HarnessSlot(%q,%q,%q) = %q, want %q", c.override, c.manifest, c.source, got, c.want)
		}
	}
}

func TestStatuslineIconOverride(t *testing.T) {
	if got := Statusline(proven(), Opts{Icon: "🍱"}); !strings.HasPrefix(got, "🍱 ") {
		t.Fatalf("icon override ignored: %q", got)
	}
}

// ---------------------------------------------------------------- verdict

func TestInitVerdict(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*probe.State)
		want   string
	}{
		{"proven", func(s *probe.State) {}, "omakase: verified — hooks installed ✓ · files present ✓ · files match ✓"},
		{"hooks", func(s *probe.State) { s.HooksInstalled = probe.Problem }, "omakase: NOT verified — hooks not installed — run omakase status"},
		{"migration", func(s *probe.State) { s.GatesMigrated = probe.Problem }, "omakase: NOT verified — harness needs migration — run omakase status"},
		{"files", func(s *probe.State) { s.HashesMatch = probe.Problem }, "omakase: NOT verified — harness files changed — run omakase status"},
		{"unknown", func(s *probe.State) { s.FilesPresent = probe.Unknown }, "omakase: could not verify the install — run omakase status"},
	}
	for _, c := range cases {
		st := proven()
		c.mutate(st)
		if got := InitVerdict(st); got != c.want {
			t.Fatalf("%s:\n got %q\nwant %q", c.name, got, c.want)
		}
	}
	if got := InitVerdict(nil); !strings.Contains(got, "could not verify") {
		t.Fatalf("nil state: %q", got)
	}
}

// ---------------------------------------------------------------- notice

func TestStopNoticeProven(t *testing.T) {
	if got := StopNotice(proven(), false); got != "pixterm-harness is active ✓" {
		t.Fatalf("proven notice: %q", got)
	}
}

func TestStopNoticeLastRun(t *testing.T) {
	st := proven()
	st.LastRun = &probe.RunSummary{Checks: 8, Failed: 0, Epoch: 1751900000}
	got := StopNotice(st, true)
	if !strings.HasPrefix(got, "pixterm-harness is active ✓\nLast run: 8/8 checks at ") {
		t.Fatalf("pass summary: %q", got)
	}
	st.LastRun.Failed = 2
	if got := StopNotice(st, true); !strings.Contains(got, "Last run: 2 checks failed at ") {
		t.Fatalf("fail summary: %q", got)
	}
	// Not run this turn: the resting line carries no summary.
	if got := StopNotice(st, false); strings.Contains(got, "Last run") {
		t.Fatalf("summary shown without a run this turn: %q", got)
	}
}

func TestStopNoticeProblems(t *testing.T) {
	st := proven()
	st.HooksInstalled = probe.Problem
	if got := StopNotice(st, false); got != "pixterm-harness is not active — hooks not installed · omakase init" {
		t.Fatalf("hooks notice: %q", got)
	}

	st = proven()
	st.GatesMigrated = probe.Problem
	if got := StopNotice(st, false); got != "pixterm-harness — needs migration (initialized before the gate module) · omakase init" {
		t.Fatalf("migration notice: %q", got)
	}

	st = proven()
	st.FilesPresent = probe.Problem
	st.HashesMatch = probe.Problem
	got := StopNotice(st, false)
	if got != "pixterm-harness — harness files changed · omakase init" {
		t.Fatalf("files notice: %q", got)
	}
	if strings.Contains(got, "✓") {
		t.Fatalf("✓ rendered with failing proofs: %q", got)
	}
}

func TestStopNoticeUnknown(t *testing.T) {
	st := proven()
	st.FilesPresent = probe.Unknown
	got := StopNotice(st, false)
	if !strings.Contains(got, "could not be verified") || strings.Contains(got, "✓") {
		t.Fatalf("unknown notice: %q", got)
	}
}

func TestStopNoticeBareInstallFallsBackToOmakase(t *testing.T) {
	st := proven()
	st.Source = ""
	if got := StopNotice(st, false); got != "omakase is active ✓" {
		t.Fatalf("bare notice: %q", got)
	}
}

// The verified init verdict names kept files (consent visible at rest);
// zero kept adds nothing, and a problem verdict never carries the count.
func TestInitVerdictKeptCount(t *testing.T) {
	ok := &probe.State{Installed: true, HooksInstalled: probe.OK, GatesMigrated: probe.OK, FilesPresent: probe.OK, HashesMatch: probe.OK}
	if got := InitVerdict(ok); strings.Contains(got, "kept") {
		t.Errorf("zero kept rendered: %q", got)
	}
	ok.Kept = 2
	if got := InitVerdict(ok); !strings.Contains(got, "2 kept (yours)") {
		t.Errorf("kept count missing: %q", got)
	}
	bad := &probe.State{Installed: true, HooksInstalled: probe.Problem, Kept: 2}
	if got := InitVerdict(bad); strings.Contains(got, "kept") {
		t.Errorf("problem verdict carries the kept count: %q", got)
	}
}

// A linked worktree shows the location as repo:worktree; the branch slot is
// unchanged (#85).
func TestStatuslineWorktreeSlot(t *testing.T) {
	st := proven()
	st.Worktree = "feature-x"
	got := plain(st)
	if !strings.Contains(got, "pixterm-engine:feature-x ⎇main") {
		t.Fatalf("worktree slot missing: %q", got)
	}
}

// While a gate runs (live heartbeat), the healthy pill carries the gate name
// and elapsed seconds; a problem state outranks the progress detail (#85).
func TestStatuslineRunningGate(t *testing.T) {
	st := proven()
	st.Running = &probe.RunningGate{Name: "go-test", Seconds: 12}
	got := plain(st)
	if !strings.Contains(got, "✓ · go-test 12s…") {
		t.Fatalf("running suffix missing: %q", got)
	}

	st.HooksInstalled = probe.Problem
	got = plain(st)
	if strings.Contains(got, "go-test 12s") {
		t.Fatalf("running suffix must not decorate a problem pill: %q", got)
	}
}
