package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func notice(t *testing.T, repo, session string) string {
	t.Helper()
	var out bytes.Buffer
	stdin := strings.NewReader(`{"cwd":` + jsonStr(repo) + `,"session_id":` + jsonStr(session) + `}`)
	if code := runStopNotice(stdin, &out); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	return out.String()
}

func TestStopNoticeVerbSpeaksOncePerSession(t *testing.T) {
	repo := harnessRepo(t)

	first := notice(t, repo, "s1")
	if !strings.Contains(first, `"systemMessage"`) || !strings.Contains(first, "is active ✓") {
		t.Fatalf("first turn: %q", first)
	}
	if second := notice(t, repo, "s1"); second != "" {
		t.Fatalf("second turn with no change: %q, want silence", second)
	}
	if fresh := notice(t, repo, "s2"); !strings.Contains(fresh, "is active ✓") {
		t.Fatalf("new session: %q", fresh)
	}
}

func TestStopNoticeVerbAnnouncesARun(t *testing.T) {
	repo := harnessRepo(t)
	notice(t, repo, "s1") // settle the marker

	ledger := filepath.Join(repo, ".git", "omakase", "ledger.tsv")
	if err := os.WriteFile(ledger, []byte("2000000000\tsmoke\tpass\tabc\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := notice(t, repo, "s1")
	if !strings.Contains(got, "Last run: 1/1 checks at ") {
		t.Fatalf("run announce: %q", got)
	}
	if again := notice(t, repo, "s1"); again != "" {
		t.Fatalf("same run announced twice: %q", again)
	}
}

func TestStopNoticeVerbAnnouncesAStateChange(t *testing.T) {
	repo := harnessRepo(t)
	notice(t, repo, "s1") // settle the marker

	if err := os.Remove(filepath.Join(repo, ".git", "hooks", "pre-commit")); err != nil {
		t.Fatal(err)
	}
	got := notice(t, repo, "s1")
	if !strings.Contains(got, "is not active") {
		t.Fatalf("disarm announce: %q", got)
	}
	if again := notice(t, repo, "s1"); again != "" {
		t.Fatalf("unchanged degraded state announced twice: %q", again)
	}
}

func TestStopNoticeVerbSilentOutsideAHarnessRepo(t *testing.T) {
	dir := t.TempDir()
	if got := notice(t, dir, "s1"); got != "" {
		t.Fatalf("outside a repo: %q, want silence", got)
	}
}
