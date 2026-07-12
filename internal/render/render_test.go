package render

import (
	"strings"
	"testing"

	"github.com/Yuncun/omakase-harness/internal/probe"
)

// proven is a fully-verified fact sheet; tests mutate copies of it.
func proven() *probe.State {
	return &probe.State{
		Installed:     true,
		Project:       "pixterm-engine",
		Branch:        "main",
		Source:        "https://github.com/Yuncun/pixterm-harness",
		Armed:         probe.OK,
		FilesPresent:  probe.OK,
		HashesMatch:   probe.OK,
		MainCheckout:  true,
		WorktreeCount: 1,
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
		func(s *probe.State) { s.Armed = probe.Problem },
		func(s *probe.State) { s.Armed = probe.Unknown },
		func(s *probe.State) { s.FilesPresent = probe.Problem; s.Missing = 1 },
		func(s *probe.State) { s.FilesPresent = probe.Unknown },
		func(s *probe.State) { s.HashesMatch = probe.Problem; s.Drifted = 1 },
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

func TestStatuslineProblemFacts(t *testing.T) {
	st := proven()
	st.Armed = probe.Problem
	st.FilesPresent = probe.Problem
	st.Missing = 2
	st.HashesMatch = probe.Problem
	st.Drifted = 1
	got := plain(st)
	want := "🥡 pixterm-engine ⎇main · pixterm-harness ⚠ hooks not armed · 2 files missing · 1 file drifted"
	if got != want {
		t.Fatalf("problem plain:\n got %q\nwant %q", got, want)
	}
	if c := colour(st); !strings.Contains(c, amberOn) {
		t.Fatalf("problem color misses the amber pill: %q", c)
	}
}

func TestStatuslineUnknownNeverReadsAsWorking(t *testing.T) {
	st := proven()
	st.Armed = probe.Unknown
	got := plain(st)
	if !strings.Contains(got, "unverified") {
		t.Fatalf("unknown plain misses 'unverified': %q", got)
	}
	c := colour(st)
	if strings.Contains(c, greenOn) || strings.Contains(c, "✓") {
		t.Fatalf("unknown rendered as working: %q", c)
	}
}

func TestStatuslineProblemPlusUnknownShowsBoth(t *testing.T) {
	st := proven()
	st.Armed = probe.Problem
	st.HashesMatch = probe.Unknown
	got := plain(st)
	if !strings.Contains(got, "hooks not armed") || !strings.Contains(got, "unverified") {
		t.Fatalf("mixed state misses a fact: %q", got)
	}
}

// ---------------------------------------------------------------- discipline

func TestStatuslineMainCheckoutWarning(t *testing.T) {
	st := proven()
	st.WorktreeCount = 3
	got := plain(st)
	if !strings.Contains(got, "main checkout · use a worktree") {
		t.Fatalf("main-checkout warning missing: %q", got)
	}
	if !strings.Contains(got, "✓") {
		t.Fatalf("discipline warning must not suppress the proven state: %q", got)
	}

	st.DisciplineOff = true
	if got := plain(st); strings.Contains(got, "main checkout") {
		t.Fatalf("warning shown despite standdown: %q", got)
	}

	st = proven()
	st.WorktreeCount = 3
	st.MainCheckout = false
	st.Worktree = "feature-x"
	if got := plain(st); strings.Contains(got, "main checkout") {
		t.Fatalf("warning shown in a linked worktree: %q", got)
	}
}

// ---------------------------------------------------------------- identity

// ⎇ always shows the branch: a worktree's folder name is frozen at
// creation and goes stale as branches change inside it (Eric misread the
// folder name as a branch on day one — that confusion is the spec).
func TestStatuslineBranchNotWorktreeFolderName(t *testing.T) {
	st := proven()
	st.MainCheckout = false
	st.WorktreeCount = 2
	st.Worktree = "issue-72-status-failclosed" // stale folder name
	st.Branch = "issue-85-statusline"
	got := plain(st)
	if !strings.Contains(got, "⎇issue-85-statusline") {
		t.Fatalf("branch not shown: %q", got)
	}
	if strings.Contains(got, "issue-72-status-failclosed") {
		t.Fatalf("stale worktree folder name shown: %q", got)
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
	cases := []struct{ override, source, want string }{
		{"acme", "https://github.com/x/y", "acme"},
		{"", "https://github.com/Yuncun/pixterm-harness.git", "pixterm-harness"},
		{"", "github.com/x/team-harness#v2", "team-harness"},
		{"", "", ""},
	}
	for _, c := range cases {
		st := &probe.State{NameOverride: c.override, Source: c.source}
		if got := HarnessSlot(st); got != c.want {
			t.Fatalf("HarnessSlot(%q,%q) = %q, want %q", c.override, c.source, got, c.want)
		}
	}
}

func TestStatuslineIconOverride(t *testing.T) {
	if got := Statusline(proven(), Opts{Icon: "🍱"}); !strings.HasPrefix(got, "🍱 ") {
		t.Fatalf("icon override ignored: %q", got)
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
	st.Armed = probe.Problem
	if got := StopNotice(st, false); !strings.Contains(got, "is not active") {
		t.Fatalf("not-armed notice: %q", got)
	}

	st = proven()
	st.FilesPresent = probe.Problem
	st.Missing = 2
	st.HashesMatch = probe.Problem
	st.Drifted = 1
	got := StopNotice(st, false)
	if !strings.Contains(got, "2 files missing · omakase init to update") ||
		!strings.Contains(got, "files differ from canonical · omakase status to review") {
		t.Fatalf("nudge notice: %q", got)
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
